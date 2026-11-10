package release

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// AttestError means a signed build was uploaded but its manifest was not
// attached, so devices that require attested builds are not offered it.
type AttestError struct {
	Build int
	Err   error
}

func (e *AttestError) Error() string {
	return fmt.Sprintf("build %d was uploaded but not attested: %v\nDevices that require attested builds will not be offered it until it is.\nAttest it with:\n  simpleota build attest %d <bin>", e.Build, e.Err, e.Build)
}

func (e *AttestError) Unwrap() error { return e.Err }

// DeployError means a build was uploaded, and attested when signed, but its
// deployment was not created or did not start.
type DeployError struct {
	Build        int
	DeploymentID string
	Err          error
}

func (e *DeployError) Error() string {
	if e.DeploymentID == "" {
		return fmt.Sprintf("build %d was uploaded and attested, but its deployment was not created: %v\nDeploy it with:\n  simpleota deployment create --build %d", e.Build, e.Err, e.Build)
	}
	id := termtext.Clean(e.DeploymentID)
	return fmt.Sprintf("build %d was uploaded and attested, and deployment %s was created but did not start: %v\nCancel it with:\n  simpleota deployment cancel %s", e.Build, id, e.Err, id)
}

func (e *DeployError) Unwrap() error { return e.Err }

// attest signs m and attaches the signature to the artifact. The private
// key stays here: only the signature is sent.
func (s Service) attest(ctx context.Context, key ed25519.PrivateKey, artifactID string, m signing.Manifest) (api.Artifact, error) {
	sig, err := signing.SignManifest(key, m)
	if err != nil {
		return api.Artifact{}, err
	}
	return s.API.AttestArtifact(ctx, s.ProjectID, artifactID, api.AttestRequest{ManifestSignature: sig, ManifestVersion: signing.ManifestVersion})
}

// releaseAttested uploads and attests a signed build, then deploys it to
// one channel. The channel is looked up first, so a wrong name costs no
// build number.
func (s Service) releaseAttested(ctx context.Context, p prepared, allowDuplicate bool, t Target) (Result, error) {
	channelID, err := s.channelID(ctx, t.Channel)
	if err != nil {
		return p.res, err
	}
	res, err := s.uploadAndAttest(ctx, p, allowDuplicate)
	if err != nil {
		return res, err
	}
	draft, err := s.API.CreateDeployment(ctx, s.ProjectID, api.DeploymentRequest{
		ArtifactID:        res.ArtifactID,
		RolloutStrategy:   api.RolloutImmediate,
		RolloutPercentage: t.Percent,
		TargetChannelIDs:  []string{channelID},
		TargetGroupIDs:    []string{},
		Notes:             "Created by simpleota release.",
	})
	if err != nil {
		return res, &DeployError{Build: res.BuildNumber, Err: err}
	}
	d, err := s.API.TransitionDeployment(ctx, s.ProjectID, draft.ID, api.Transition{Action: "start"})
	if err != nil {
		return res, &DeployError{Build: res.BuildNumber, DeploymentID: draft.ID, Err: err}
	}
	res.DeploymentID, res.State, res.Channel = d.ID, d.State, t.Channel
	return res, nil
}

// channelID finds a channel by its exact name, as the quick-release
// endpoint does.
func (s Service) channelID(ctx context.Context, name string) (string, error) {
	channels, err := s.API.ListChannels(ctx, s.ProjectID)
	if err != nil {
		return "", fmt.Errorf("listing the project's channels: %w", err)
	}
	for _, c := range channels {
		if c.Name == name {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("channel '%s' not found on this project", termtext.Clean(name))
}

// Attest attests a build uploaded earlier: one whose attestation failed
// after the upload, or one uploaded by a CLI that did not attest. The file
// must be the build's own binary and key the key that signed it.
func (s Service) Attest(ctx context.Context, key ed25519.PrivateKey, build int, binaryPath string) (api.Artifact, error) {
	binary, err := ReadBinary(binaryPath)
	if err != nil {
		return api.Artifact{}, err
	}
	art, err := s.artifact(ctx, build)
	if err != nil {
		return art, err
	}
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	switch {
	case art.SecurityMode != manifest.SecuritySigned:
		return art, fmt.Errorf("build %d is %s, not signed\nOnly a signed build can be attested.", build, termtext.Clean(art.SecurityMode))
	case !strings.EqualFold(art.ChecksumSHA256, sha):
		return art, fmt.Errorf("%s is not build %d: its SHA-256 is %s, the build's is %s", binaryPath, build, sha, termtext.Clean(art.ChecksumSHA256))
	}
	keyID, err := s.registeredKeyID(ctx, key.Public().(ed25519.PublicKey))
	if err != nil {
		return art, err
	}
	if art.SigningKeyID != "" && art.SigningKeyID != keyID {
		return art, fmt.Errorf("build %d was signed with key %q, not %q\nAttest it with the key that signed it.", build, termtext.Clean(art.SigningKeyID), keyID)
	}
	m := signing.Manifest{ProjectID: strings.ToLower(s.ProjectID), KeyID: keyID, Build: art.BuildNumber, SHA256: sha}
	return s.attest(ctx, key, art.ID, m)
}

// artifact finds a build by number.
func (s Service) artifact(ctx context.Context, build int) (api.Artifact, error) {
	artifacts, err := s.API.ListArtifacts(ctx, s.ProjectID)
	if err != nil {
		return api.Artifact{}, err
	}
	for _, a := range artifacts {
		if a.BuildNumber == build {
			return a, nil
		}
	}
	return api.Artifact{}, fmt.Errorf("this project has no build %d\nList builds with:\n  simpleota build list", build)
}
