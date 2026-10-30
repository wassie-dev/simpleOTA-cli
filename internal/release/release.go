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

// ErrChecksumMismatch means the server stored different bytes than were sent.
var ErrChecksumMismatch = errors.New("checksum mismatch")

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
	// SHA256Verified is false when the stored checksum could not be read
	// back; Warning then says why.
	SHA256Verified bool
	Warning        string
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

// Release builds the manifest, signs when a key is set, uploads and deploys,
// then checks the server stored exactly the bytes that were sent.
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

	sum := sha256.Sum256(binary)
	res.SHA256 = hex.EncodeToString(sum[:])
	res.Size = int64(len(binary))
	res.SecurityMode = m.SecurityMode

	out, err := s.API.Release(ctx, s.ProjectID, api.ReleaseRequest{
		ManifestJSON:      manifestJSON,
		FileName:          filepath.Base(o.BinaryPath),
		Binary:            binary,
		RolloutPercentage: o.Percent,
		ChannelName:       o.Channel,
	})
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) && !errors.Is(err, context.Canceled) {
			return res, fmt.Errorf("%w; the upload may still have created a build, so check with simpleota deployment status before releasing again (a retry uses another build number)", err)
		}
		return res, err
	}
	res.BuildNumber = out.BuildNumber
	res.ArtifactID = out.ArtifactID
	res.DeploymentID = out.DeploymentID
	res.State = out.State
	res.Channel = out.Channel

	verified, err := s.verifyChecksum(ctx, out, res.SHA256)
	if err != nil {
		return res, err
	}
	res.SHA256Verified = verified
	if !verified {
		res.Warning = fmt.Sprintf("build %d is released, but the server's stored checksum could not be read back to verify it", out.BuildNumber)
	}
	return res, nil
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

// verifyChecksum compares the server's stored SHA-256 with the local one and
// reports false when it cannot be read. On a mismatch the new deployment is
// cancelled so no device is offered it.
func (s Service) verifyChecksum(ctx context.Context, out api.ReleaseResult, want string) (bool, error) {
	artifacts, err := s.API.ListArtifacts(ctx, s.ProjectID)
	if err != nil {
		return false, nil
	}
	for _, a := range artifacts {
		if a.ID != out.ArtifactID {
			continue
		}
		if strings.EqualFold(a.ChecksumSHA256, want) {
			return true, nil
		}
		_, cancelErr := s.API.TransitionDeployment(ctx, s.ProjectID, out.DeploymentID, api.Transition{Action: "cancel"})
		if cancelErr != nil {
			return false, fmt.Errorf("%w: the server stored sha256 %s but %s was sent, and cancelling deployment %s failed (%v); cancel it now with simpleota deployment cancel %s",
				ErrChecksumMismatch, a.ChecksumSHA256, want, out.DeploymentID, cancelErr, out.DeploymentID)
		}
		return false, fmt.Errorf("%w: the server stored sha256 %s but %s was sent; deployment %s was cancelled",
			ErrChecksumMismatch, a.ChecksumSHA256, want, out.DeploymentID)
	}
	return false, nil
}
