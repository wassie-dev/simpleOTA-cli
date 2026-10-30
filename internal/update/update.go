// Package update finds, verifies and installs simpleota releases published
// on GitHub, so the CLI can replace its own binary.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/wassie-dev/simpleOTA-cli/internal/config"
)

// ReleasesURL is the GitHub releases page of the CLI.
const ReleasesURL = "https://github.com/wassie-dev/simpleOTA-cli/releases"

const (
	maxArchive   = 64 << 20
	maxChecksums = 1 << 20
	maxBinary    = 64 << 20
)

// ErrNoRelease means no release has been published yet.
var ErrNoRelease = errors.New("no simpleota release has been published yet")

// Source locates releases and downloads their assets.
type Source struct {
	HTTP *http.Client
	// ReleasesURL serves /latest, which redirects to /tag/v<version>.
	ReleasesURL string
	// DownloadURL serves /v<version>/<asset>.
	DownloadURL string
	GOOS        string
	GOARCH      string
}

// NewSource returns a Source for GitHub, or for the mirror in downloadURL
// when it is set. A mirror must be https, or http on a loopback host.
func NewSource(downloadURL string) (Source, error) {
	s := Source{
		HTTP:        httpClient(),
		ReleasesURL: ReleasesURL,
		DownloadURL: ReleasesURL + "/download",
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
	}
	if downloadURL != "" {
		u, err := url.Parse(downloadURL)
		if err != nil || !allowedURL(u) {
			return s, fmt.Errorf("SIMPLEOTA_DOWNLOAD_URL must be https (or http on localhost), got %q", downloadURL)
		}
		s.DownloadURL = strings.TrimRight(downloadURL, "/")
	}
	return s, nil
}

func allowedURL(u *url.URL) bool {
	return u.Host != "" && (u.Scheme == "https" || (u.Scheme == "http" && config.IsLoopback(u.Hostname())))
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		// GitHub serves assets through redirects to its CDN; never follow
		// one to plain http.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if !allowedURL(req.URL) {
				return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
			}
			return nil
		},
	}
}

// Normalize returns v as a full major.minor.patch semver string with a
// leading v, or "" when v is not a version (a "dev" build).
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	core, _, _ := strings.Cut(v, "-")
	core, _, _ = strings.Cut(core, "+")
	if !semver.IsValid(v) || strings.Count(core, ".") != 2 {
		return ""
	}
	return v
}

// Newer reports whether candidate is a higher version than current.
func Newer(candidate, current string) bool {
	return semver.Compare(Normalize(candidate), Normalize(current)) > 0
}

// Latest returns the version of the newest published release, such as
// "v0.1.0", from the redirect GitHub sends for /releases/latest.
func (s Source) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.ReleasesURL+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := *s.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for the latest release: %w", err)
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode/100 == 3 && !strings.Contains(loc, "/tag/")) {
		return "", ErrNoRelease
	}
	if resp.StatusCode/100 != 3 {
		return "", fmt.Errorf("checking for the latest release: %s returned %d", s.ReleasesURL, resp.StatusCode)
	}
	v := Normalize(path.Base(loc))
	if v == "" {
		return "", fmt.Errorf("checking for the latest release: unexpected redirect to %q", loc)
	}
	return v, nil
}

// archiveName is the release asset for this OS and CPU.
func (s Source) archiveName(version string) string {
	ext := "tar.gz"
	if s.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("simpleota_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), s.GOOS, s.GOARCH, ext)
}

func (s Source) fetch(ctx context.Context, version, asset string, limit int64) ([]byte, error) {
	u := s.DownloadURL + "/" + version + "/" + asset
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("simpleota %s has no %s; check the version at %s", version, asset, ReleasesURL)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: server returned %d", asset, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", asset, limit)
	}
	return data, nil
}

// Download fetches the release archive for this platform, checks it against
// the release's checksums.txt and returns the simpleota binary inside it.
func (s Source) Download(ctx context.Context, version string) ([]byte, error) {
	version = Normalize(version)
	if version == "" {
		return nil, errors.New("invalid version")
	}
	name := s.archiveName(version)
	sums, err := s.fetch(ctx, version, "checksums.txt", maxChecksums)
	if err != nil {
		return nil, err
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return nil, err
	}
	archive, err := s.fetch(ctx, version, name, maxArchive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("checksum mismatch for %s: checksums.txt says %s, the download is %s", name, want, got)
	}
	if s.GOOS == "windows" {
		return fromZip(archive, "simpleota.exe")
	}
	return fromTarGz(archive, "simpleota")
}

func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in the release's checksums.txt", name)
}

func fromTarGz(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("reading the release archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the release archive has no %s", name)
		}
		if err != nil {
			return nil, fmt.Errorf("reading the release archive: %w", err)
		}
		if h.Name == name && h.Typeflag == tar.TypeReg {
			return readBinary(tr)
		}
	}
}

func fromZip(archive []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("reading the release archive: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != name || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("reading the release archive: %w", err)
		}
		defer rc.Close()
		return readBinary(rc)
	}
	return nil, fmt.Errorf("the release archive has no %s", name)
}

func readBinary(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, fmt.Errorf("reading the release archive: %w", err)
	}
	if len(data) > maxBinary || len(data) == 0 {
		return nil, errors.New("the binary in the release archive has an unexpected size")
	}
	return data, nil
}

// Replace installs binary at exe. The new file is written next to exe and
// renamed over it, so exe is never left half-written. Windows cannot
// overwrite a running program, so there the old binary is first moved aside
// to exe+".old", which the next update removes.
func Replace(exe string, binary []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".simpleota-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { // #nosec G302 -- an executable must be executable
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Rename(tmpName, exe)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("moving the running simpleota aside: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}

// RemoveLeftover deletes the binary a previous Windows update moved aside.
func RemoveLeftover(exe string) {
	_ = os.Remove(exe + ".old")
}
