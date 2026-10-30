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
	"slices"
	"sort"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

// MaxBinarySize is the largest single image any SimpleOTA plan accepts.
const MaxBinarySize = 32 << 20

// espImageMagic is the first byte of every ESP32-family app image.
const espImageMagic = 0xE9

// imageHeaderLen is the size of an app image header (esp_image_header_t).
const imageHeaderLen = 24

// espChipIDs maps the chip_id field of an app image header to the chip
// family devices report, which is ESP-IDF's IDF_TARGET. The IDs come from
// esp_chip_id_t in ESP-IDF's esp_app_format.h.
var espChipIDs = map[uint16]string{
	0x0000: "esp32",
	0x0002: "esp32s2",
	0x0005: "esp32c3",
	0x0009: "esp32s3",
	0x000C: "esp32c2",
	0x000D: "esp32c6",
	0x0010: "esp32h2",
	0x0012: "esp32p4",
	0x0014: "esp32c61",
	0x0017: "esp32c5",
	0x0019: "esp32h21",
	0x001C: "esp32h4",
}

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

// ChipError means the chip family is missing or invalid, or no device could
// match it.
type ChipError struct{ msg string }

func (e *ChipError) Error() string { return e.msg }

func chipErr(format string, args ...any) error {
	return &ChipError{msg: fmt.Sprintf(format, args...)}
}

// Options describe one release.
type Options struct {
	BinaryPath   string
	VersionLabel string
	// Chip defaults to the chip the image header names when empty.
	Chip string
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
	Chip         string
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

// ImageChip returns the chip family an app image was built for, read from
// its header, or "" when the header names no chip simpleota knows.
func ImageChip(data []byte) string {
	if len(data) < imageHeaderLen || data[0] != espImageMagic {
		return ""
	}
	return espChipIDs[uint16(data[12])|uint16(data[13])<<8]
}

// Release builds the manifest, signs when a key is set, uploads and deploys.
// The upload is sent once and never retried. The manifest carries the
// binary's SHA-256, so the server rejects the upload if the bytes it
// received differ.
func (s Service) Release(ctx context.Context, o Options) (Result, error) {
	var res Result
	if o.Chip != "" {
		if _, err := manifest.NormalizeChip(o.Chip); err != nil {
			return res, chipErr("%v", err)
		}
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
	chip, err := s.chooseChip(ctx, o.Chip, ImageChip(binary), o.BinaryPath)
	if err != nil {
		return res, err
	}
	res.Chip = chip
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

// chooseChip settles the manifest's chip family. A --chip that differs from
// the chip the image was built for is refused unless a device in the
// project reports it, since no other device would be offered the build.
func (s Service) chooseChip(ctx context.Context, flag, image, path string) (string, error) {
	if flag == "" {
		if image == "" {
			return "", chipErr("--chip is required, since the header of %s names no chip simpleota knows; e.g. --chip esp32s3", path)
		}
		return image, nil
	}
	chip, err := manifest.NormalizeChip(flag)
	if err != nil || image == "" || chip == image {
		return chip, err
	}
	devices, err := s.API.ListDevices(ctx, s.ProjectID)
	if err != nil {
		return "", fmt.Errorf("%s was built for %s, not %s; checking whether a device reports %s failed: %w", path, image, chip, chip, err)
	}
	for _, d := range devices {
		if strings.EqualFold(strings.TrimSpace(d.ChipFamily), chip) {
			return chip, nil
		}
	}
	return "", chipErr("%s was built for %s, but --chip is %s and no device in this project reports %s, so no device would be offered the build; leave out --chip to use %s", path, image, chip, chip, image)
}

// AlsoActive returns the project's other active deployments that reach the
// release's channel with a build for the same chip. A device installs the
// highest build offered to it, so these go on reaching the devices the
// release does not.
func (s Service) AlsoActive(ctx context.Context, res Result) ([]api.Deployment, error) {
	deps, err := s.API.ListDeployments(ctx, s.ProjectID)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.API.ListArtifacts(ctx, s.ProjectID)
	if err != nil {
		return nil, err
	}
	chips := make(map[string]string, len(artifacts))
	for _, a := range artifacts {
		chips[a.ID] = a.ChipFamily
	}
	var channels []string
	for _, d := range deps {
		if d.ID == res.DeploymentID {
			channels = d.TargetChannelIDs
		}
	}
	var out []api.Deployment
	for _, d := range deps {
		if d.ID == res.DeploymentID || d.State != api.StateActive || !overlaps(channels, d.TargetChannelIDs) {
			continue
		}
		if chip, ok := chips[d.ArtifactID]; ok && chip != "" && !strings.EqualFold(chip, res.Chip) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ArtifactBuildNumber > out[j].ArtifactBuildNumber })
	return out, nil
}

// overlaps reports whether two deployments' channel targets share a channel.
// A deployment without channel targets reaches every channel.
func overlaps(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
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
