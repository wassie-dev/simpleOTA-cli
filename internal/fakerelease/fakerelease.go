// Package fakerelease serves a fake GitHub release of simpleota over
// httptest, for tests of the update package and command.
package fakerelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/update"
)

// Server is a release host with one published version.
type Server struct {
	*httptest.Server
	Version string
	GOOS    string
	GOARCH  string
	Binary  []byte

	mu sync.Mutex
	// BadChecksum makes checksums.txt disagree with the archive.
	BadChecksum bool
	// NoRelease makes /latest behave as before any release is published.
	NoRelease bool
	// Downloads counts archive downloads.
	Downloads int
}

// New publishes binary as release version (without the v) for goos/goarch.
func New(t *testing.T, version, goos, goarch string, binary []byte) *Server {
	s := &Server{Version: version, GOOS: goos, GOARCH: goarch, Binary: binary}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Source points the update package at this server.
func (s *Server) Source() update.Source {
	return update.Source{
		HTTP:        s.Client(),
		ReleasesURL: s.URL + "/releases",
		DownloadURL: s.URL + "/releases/download",
		GOOS:        s.GOOS,
		GOARCH:      s.GOARCH,
	}
}

// Set changes a field under the server's lock.
func (s *Server) Set(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *Server) archiveName() string {
	if s.GOOS == "windows" {
		return fmt.Sprintf("simpleota_%s_%s_%s.zip", s.Version, s.GOOS, s.GOARCH)
	}
	return fmt.Sprintf("simpleota_%s_%s_%s.tar.gz", s.Version, s.GOOS, s.GOARCH)
}

func (s *Server) archive() []byte {
	var buf bytes.Buffer
	if s.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("simpleota.exe")
		_, _ = w.Write(s.Binary)
		_ = zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: "simpleota", Mode: 0o755, Size: int64(len(s.Binary)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(s.Binary)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.URL.Path == "/releases/latest":
		if s.NoRelease {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, s.URL+"/releases/tag/v"+s.Version, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/releases/download/v"+s.Version+"/"):
		asset := strings.TrimPrefix(r.URL.Path, "/releases/download/v"+s.Version+"/")
		archive := s.archive()
		switch asset {
		case "checksums.txt":
			sum := sha256.Sum256(archive)
			hexSum := hex.EncodeToString(sum[:])
			if s.BadChecksum {
				hexSum = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, "%s  %s\n%s  install.sh\n", hexSum, s.archiveName(), strings.Repeat("1", 64))
		case s.archiveName():
			s.Downloads++
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
