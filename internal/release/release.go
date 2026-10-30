// Package release checks, signs and uploads a firmware build, and deploys it
// with one quick-release call.
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
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

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
	// Key signs the release when set; otherwise it is uploaded as basic.
	Key ed25519.PrivateKey
	// AllowDuplicate uploads the binary even when the project already has
	// a build with the same SHA-256.
	AllowDuplicate bool
}

// Target is where Release deploys: one channel, by name, and the share of
// its devices to offer the build to.
type Target struct {
	Channel string
	Percent int
}

// Result describes the created build, and for Release its deployment.
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

// API is the part of the developer API that uploads need.
type API interface {
	GetProject(ctx context.Context, projectID string) (api.Project, error)
	ListSigningKeys(ctx context.Context, projectID string) ([]api.SigningKey, error)
	ListArtifacts(ctx context.Context, projectID string) ([]api.Artifact, error)
	ListDevices(ctx context.Context, projectID string, f api.DeviceFilter) ([]api.Device, error)
	UploadArtifact(ctx context.Context, projectID string, req api.UploadRequest) (api.Artifact, error)
	Release(ctx context.Context, projectID string, req api.ReleaseRequest) (api.ReleaseResult, error)
}

// Service runs releases for one project.
type Service struct {
	API       API
	ProjectID string
}

// Upload checks, signs when a key is set, and uploads a build without
// deploying it. Like Release, the upload is sent once and never retried.
func (s Service) Upload(ctx context.Context, o Options) (Result, error) {
	p, err := s.prepare(ctx, o)
	if err != nil {
		return p.res, err
	}
	art, err := s.API.UploadArtifact(ctx, s.ProjectID, api.UploadRequest{ManifestJSON: p.manifest, FileName: p.fileName, Binary: p.binary})
	if err != nil {
		return p.res, uploadErr(err, o.AllowDuplicate)
	}
	p.res.BuildNumber = art.BuildNumber
	p.res.ArtifactID = art.ID
	return p.res, nil
}

// Release checks, signs when a key is set, uploads and deploys. The upload
// is sent once and never retried. The manifest carries the binary's
// SHA-256, so the server rejects the upload if the bytes it received
// differ.
func (s Service) Release(ctx context.Context, o Options, t Target) (Result, error) {
	if t.Percent < 0 || t.Percent > 100 {
		return Result{}, errors.New("--percent must be between 0 and 100")
	}
	if strings.TrimSpace(t.Channel) == "" {
		return Result{}, errors.New("--channel must not be empty")
	}
	p, err := s.prepare(ctx, o)
	if err != nil {
		return p.res, err
	}
	out, err := s.API.Release(ctx, s.ProjectID, api.ReleaseRequest{
		ManifestJSON:      p.manifest,
		FileName:          p.fileName,
		Binary:            p.binary,
		RolloutPercentage: t.Percent,
		ChannelName:       t.Channel,
	})
	if err != nil {
		return p.res, uploadErr(err, o.AllowDuplicate)
	}
	p.res.BuildNumber = out.BuildNumber
	p.res.ArtifactID = out.ArtifactID
	p.res.DeploymentID = out.DeploymentID
	p.res.State = out.State
	p.res.Channel = out.Channel
	return p.res, nil
}

// prepared is a checked and signed build, ready to upload.
type prepared struct {
	binary   []byte
	fileName string
	manifest []byte
	res      Result
}

// prepare reads the binary, settles its chip, refuses a duplicate, and
// builds and signs the manifest.
func (s Service) prepare(ctx context.Context, o Options) (prepared, error) {
	var p prepared
	if o.Chip != "" {
		if _, err := manifest.NormalizeChip(o.Chip); err != nil {
			return p, chipErr("%v", err)
		}
	}
	binary, err := ReadBinary(o.BinaryPath)
	if err != nil {
		return p, err
	}
	p.binary, p.fileName = binary, filepath.Base(o.BinaryPath)
	chip, err := s.chooseChip(ctx, o.Chip, ImageChip(binary), o.BinaryPath)
	if err != nil {
		return p, err
	}
	sum := sha256.Sum256(binary)
	p.res.Chip, p.res.SHA256, p.res.Size = chip, hex.EncodeToString(sum[:]), int64(len(binary))
	if !o.AllowDuplicate {
		if err := s.refuseDuplicate(ctx, p.res.SHA256); err != nil {
			return p, err
		}
	}

	framework := o.Framework
	if framework == "" {
		proj, err := s.API.GetProject(ctx, s.ProjectID)
		if err != nil {
			return p, fmt.Errorf("reading the project's default framework: %w", err)
		}
		framework = proj.DefaultFramework
	}
	m := manifest.Manifest{
		Framework:    framework,
		VersionLabel: o.VersionLabel,
		ChipFamily:   chip,
		SecurityMode: manifest.SecurityBasic,
		SHA256:       p.res.SHA256,
	}
	if o.Key != nil {
		keyID, err := s.registeredKeyID(ctx, o.Key.Public().(ed25519.PublicKey))
		if err != nil {
			return p, err
		}
		m.SecurityMode = manifest.SecuritySigned
		m.SigningMetadata = &manifest.SigningMetadata{
			KeyID:     keyID,
			Algorithm: manifest.AlgorithmEd25519,
			Signature: signing.Sign(o.Key, binary),
		}
		p.res.KeyID = keyID
	}
	if err := m.Validate(); err != nil {
		return p, err
	}
	p.res.SecurityMode = m.SecurityMode
	p.manifest, err = json.Marshal(m)
	return p, err
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
	devices, err := s.API.ListDevices(ctx, s.ProjectID, api.DeviceFilter{})
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
		return fmt.Errorf("%w: the server received different bytes than were sent (%s); nothing was created, so it is safe to run the command again", ErrChecksumMismatch, ae.Detail)
	case errors.As(err, &ae) && ae.StatusCode < http.StatusInternalServerError:
		return err
	case allowDuplicate:
		return fmt.Errorf("%w; the upload may still have created a build, so check the project's builds before releasing again", err)
	default:
		return fmt.Errorf("%w; the upload may still have created a build: run the same command again to find out, since it stops if the project already has this binary", err)
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
	return "", errors.New("this signing key is not registered on the project; register it with simpleota key generate, or use the key file that command created")
}
