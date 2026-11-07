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
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
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
	// LoadKey returns the key to sign with, or nil to upload as basic. It
	// is called once the binary has passed its local checks, so a wrong file
	// is reported before a passphrase is asked for. Nil means basic.
	LoadKey func() (ed25519.PrivateKey, error)
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
	ListDeployments(ctx context.Context, projectID string) ([]api.Deployment, error)
	ListChannels(ctx context.Context, projectID string) ([]api.Channel, error)
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
		return p.res, s.uploadErr(ctx, err, o.AllowDuplicate, p.req)
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
		p.req.target = &t
		return p.res, s.uploadErr(ctx, err, o.AllowDuplicate, p.req)
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
	req      request
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
	var key ed25519.PrivateKey
	if o.LoadKey != nil {
		if key, err = o.LoadKey(); err != nil {
			return p, err
		}
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
	if key != nil {
		keyID, err := s.registeredKeyID(ctx, key.Public().(ed25519.PublicKey))
		if err != nil {
			return p, err
		}
		m.SecurityMode = manifest.SecuritySigned
		m.SigningMetadata = &manifest.SigningMetadata{
			KeyID:     keyID,
			Algorithm: manifest.AlgorithmEd25519,
			Signature: signing.Sign(key, binary),
		}
		p.res.KeyID = keyID
	}
	if err := m.Validate(); err != nil {
		return p, err
	}
	p.res.SecurityMode = m.SecurityMode
	p.req = request{sha: m.SHA256, label: m.VersionLabel, framework: m.Framework, chip: m.ChipFamily, security: m.SecurityMode}
	p.manifest, err = json.Marshal(m)
	return p, err
}

// chooseChip settles the manifest's chip family. A --chip that differs from
// the chip the image was built for is refused unless a device in the
// project reports it, since no other device would be offered the build.
func (s Service) chooseChip(ctx context.Context, flag, image, path string) (string, error) {
	if flag == "" {
		if image == "" {
			return "", chipErr("cannot read the chip from %s: its header names no chip simpleota knows\nPass --chip, e.g. --chip esp32s3.", path)
		}
		return image, nil
	}
	chip, err := manifest.NormalizeChip(flag)
	if err != nil || image == "" || chip == image {
		return chip, err
	}
	devices, err := s.API.ListDevices(ctx, s.ProjectID, api.DeviceFilter{})
	if err != nil {
		return "", fmt.Errorf("%s was built for %s, not %s\nChecking whether a device reports %s failed: %w", path, image, chip, chip, err)
	}
	for _, d := range devices {
		if strings.EqualFold(strings.TrimSpace(d.ChipFamily), chip) {
			return chip, nil
		}
	}
	return "", chipErr("%s was built for %s, but --chip is %s\nNo device in this project reports %s, so none would be offered the build.\nLeave out --chip to use %s.", path, image, chip, chip, image)
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
			return fmt.Errorf("%w: build %d (version %s) has the same SHA-256%s\nTo upload it again as a new build, pass --allow-duplicate.",
				ErrDuplicate, a.BuildNumber, termtext.Clean(a.VersionLabel), s.deploymentsOf(ctx, a))
		}
	}
	return nil
}

// deploymentsOf describes the deployments of a build that are still
// running, for the duplicate refusal, or returns "" when there are none or
// they cannot be listed.
func (s Service) deploymentsOf(ctx context.Context, a api.Artifact) string {
	deps, err := s.API.ListDeployments(ctx, s.ProjectID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, d := range deps {
		if d.ArtifactID == a.ID && (d.State == api.StateActive || d.State == api.StatePaused) {
			fmt.Fprintf(&b, "\n  deployment %s is %s at %d%%", termtext.Clean(d.ID), termtext.Clean(d.State), d.RolloutPercentage)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return fmt.Sprintf("\nIt is already deployed:%s\nWatch a deployment with:\n  simpleota deployment wait <deployment-id>", b.String())
}

// uploadErr explains a failed upload. When the connection failed or the
// server answered 5xx, the build may exist anyway, so the project is read to
// show what it now has.
func (s Service) uploadErr(ctx context.Context, err error, allowDuplicate bool, req request) error {
	var ae *api.Error
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest && strings.HasPrefix(ae.Detail, "sha256 mismatch"):
		return fmt.Errorf("%w: the server received different bytes than were sent (%s)\nNothing was created, so it is safe to run the command again.", ErrChecksumMismatch, termtext.Clean(ae.Detail))
	case errors.As(err, &ae) && ae.StatusCode < http.StatusInternalServerError:
		return err
	case allowDuplicate:
		return fmt.Errorf("%w\nThe upload may still have created a build. Check before uploading again:\n  simpleota build list", err)
	}
	return s.uncertainErr(ctx, err, req)
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
			return "", fmt.Errorf("signing key %q is revoked on this project\nSign with an active key.", k.KeyID)
		}
		return k.KeyID, nil
	}
	return "", errors.New("this signing key is not registered on the project\nUse a key file made by 'simpleota key generate', or make a new one:\n  simpleota key generate --key-id <id>")
}
