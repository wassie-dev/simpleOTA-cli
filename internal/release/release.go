// Package release signs and uploads a firmware binary and starts its
// deployment with one quick-release call.
package release

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

// MaxBinarySize is the largest single image any SimpleOTA plan accepts.
const MaxBinarySize = 32 << 20

// espImageMagic is the first byte of every ESP32-family app image.
const espImageMagic = 0xE9

// nonAppSuffixes are toolchain outputs that are not OTA app images; the
// server keeps the same list.
var nonAppSuffixes = []string{
	".merged.bin", ".bootloader.bin", ".partitions.bin", ".partitions_flashed.bin",
	".spiffs.bin", ".littlefs.bin", ".fatfs.bin",
}

var (
	// ErrChecksumMismatch means the server received different bytes than
	// were sent and rejected the upload.
	ErrChecksumMismatch = errors.New("checksum mismatch")
	// ErrDuplicate means the project already has a build of this binary.
	ErrDuplicate = errors.New("binary already uploaded")
)

// Options describe one release.
type Options struct {
	BinaryPath   string
	VersionLabel string
	Chip         string
	// Framework defaults to the project's default framework when empty.
	Framework string
	Percent   int
	Channel   string
	// Key signs the release when set; otherwise it is uploaded as basic.
	Key ed25519.PrivateKey
	// AllowDuplicate uploads the binary even when the project already has
	// a build with the same SHA-256.
	AllowDuplicate bool
}

// Result describes the created build and deployment.
type Result struct {
	BuildNumber  int
	ArtifactID   string
	DeploymentID string
	State        string
	Channel      string
	SecurityMode string
	KeyID        string
	SHA256       string
	Size         int64
}

// Service runs releases for one project.
type Service struct {
	API       api.API
	ProjectID string
}

// ReadBinary loads and checks a firmware image.
func ReadBinary(path string) ([]byte, error) {
	lower := strings.ToLower(filepath.Base(path))
	for _, s := range nonAppSuffixes {
		if strings.HasSuffix(lower, s) {
			return nil, fmt.Errorf("%s is a %s file, not an OTA app image; pass the application .bin (Arduino: build/<sketch>.ino.bin, ESP-IDF: build/<project>.bin)", path, s)
		}
	}
	f, err := os.Open(path) // #nosec G304 -- the user names the firmware to upload
	if err != nil {
		return nil, fmt.Errorf("opening firmware: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	if fi.Size() > MaxBinarySize {
		return nil, fmt.Errorf("%s is %.1f MiB, over the %d MiB limit for one firmware image", path, float64(fi.Size())/(1<<20), MaxBinarySize>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBinarySize+1))
	if err != nil {
		return nil, fmt.Errorf("reading firmware: %w", err)
	}
	if len(data) > MaxBinarySize {
		return nil, fmt.Errorf("%s grew past the %d MiB limit while reading", path, MaxBinarySize>>20)
	}
	if data[0] != espImageMagic {
		return nil, fmt.Errorf("%s does not look like an ESP32 app image (first byte 0x%02x, expected 0xe9); pass the .bin, not the .elf", path, data[0])
	}
	return data, nil
}

// Release builds the manifest, signs when a key is set, uploads and deploys.
// The upload is sent once and never retried. The manifest carries the
// binary's SHA-256, so the server rejects the upload if the bytes it
// received differ.
func (s Service) Release(ctx context.Context, o Options) (Result, error) {
	var res Result
	chip, err := manifest.NormalizeChip(o.Chip)
	if err != nil {
		return res, err
	}
	if o.Percent < 0 || o.Percent > 100 {
		return res, errors.New("--percent must be between 0 and 100")
	}
	if strings.TrimSpace(o.Channel) == "" {
		return res, errors.New("--channel must not be empty")
	}
	binary, err := ReadBinary(o.BinaryPath)
	if err != nil {
		return res, err
	}
	sum := sha256.Sum256(binary)
	res.SHA256 = hex.EncodeToString(sum[:])
	res.Size = int64(len(binary))
	if !o.AllowDuplicate {
		if err := s.refuseDuplicate(ctx, res.SHA256); err != nil {
			return res, err
		}
	}

	framework := o.Framework
	if framework == "" {
		p, err := s.API.GetProject(ctx, s.ProjectID)
		if err != nil {
			return res, fmt.Errorf("reading the project's default framework: %w", err)
		}
		framework = p.DefaultFramework
	}

	m := manifest.Manifest{
		Framework:    framework,
		VersionLabel: o.VersionLabel,
		ChipFamily:   chip,
		SecurityMode: manifest.SecurityBasic,
		SHA256:       res.SHA256,
	}
	if o.Key != nil {
		keyID, err := s.registeredKeyID(ctx, o.Key.Public().(ed25519.PublicKey))
		if err != nil {
			return res, err
		}
		m.SecurityMode = manifest.SecuritySigned
		m.SigningMetadata = &manifest.SigningMetadata{
			KeyID:     keyID,
			Algorithm: manifest.AlgorithmEd25519,
			Signature: signing.Sign(o.Key, binary),
		}
		res.KeyID = keyID
	}
	if err := m.Validate(); err != nil {
		return res, err
	}
	manifestJSON, err := json.Marshal(m)
	if err != nil {
		return res, err
	}

	res.SecurityMode = m.SecurityMode

	out, err := s.API.Release(ctx, s.ProjectID, api.ReleaseRequest{
		ManifestJSON:      manifestJSON,
		FileName:          filepath.Base(o.BinaryPath),
		Binary:            binary,
		RolloutPercentage: o.Percent,
		ChannelName:       o.Channel,
	})
	if err != nil {
		return res, uploadErr(err, o.AllowDuplicate)
	}
	res.BuildNumber = out.BuildNumber
	res.ArtifactID = out.ArtifactID
	res.DeploymentID = out.DeploymentID
	res.State = out.State
	res.Channel = out.Channel
	return res, nil
}

// refuseDuplicate fails when the project already has a build of this
// binary, so running a release again after an unclear failure cannot
// create a second build.
func (s Service) refuseDuplicate(ctx context.Context, sha string) error {
	artifacts, err := s.API.ListArtifacts(ctx, s.ProjectID)
	if err != nil {
		return fmt.Errorf("checking the project for an earlier upload of this binary: %w", err)
	}
	for _, a := range artifacts {
		if strings.EqualFold(a.ChecksumSHA256, sha) {
			return fmt.Errorf("%w: build %d (version %s) has the same sha256 %s; pass --allow-duplicate to upload it again as a new build",
				ErrDuplicate, a.BuildNumber, a.VersionLabel, sha)
		}
	}
	return nil
}

// uploadErr explains a failed upload. When the connection failed or the
// server answered 5xx, the build may exist anyway.
func uploadErr(err error, allowDuplicate bool) error {
	var ae *api.Error
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest && strings.HasPrefix(ae.Detail, "sha256 mismatch"):
		return fmt.Errorf("%w: the server received different bytes than were sent (%s); nothing was created, so it is safe to run the release again", ErrChecksumMismatch, ae.Detail)
	case errors.As(err, &ae) && ae.StatusCode < http.StatusInternalServerError:
		return err
	case allowDuplicate:
		return fmt.Errorf("%w; the upload may still have created a build, so check the project's builds before releasing again", err)
	default:
		return fmt.Errorf("%w; the upload may still have created a build: run the same release again to find out, since it stops if the project already has this binary", err)
	}
}

// registeredKeyID finds the active project key matching pub, so a release
// never names a key the server would reject.
func (s Service) registeredKeyID(ctx context.Context, pub ed25519.PublicKey) (string, error) {
	keys, err := s.API.ListSigningKeys(ctx, s.ProjectID)
	if err != nil {
		return "", fmt.Errorf("listing the project's signing keys: %w", err)
	}
	for _, k := range keys {
		kp, err := signing.ParsePublicKeyPEM(k.PublicKeyPEM)
		if err != nil || !bytes.Equal(kp, pub) {
			continue
		}
		if k.RevokedAt != nil {
			return "", fmt.Errorf("signing key %q is revoked on this project; sign with an active key", k.KeyID)
		}
		return k.KeyID, nil
	}
	return "", errors.New("this signing key is not registered on the project; register it with simpleota keys generate, or use the key file that command created")
}
