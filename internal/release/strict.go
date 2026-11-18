package release

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// StrictReadyError means a signed build was uploaded but its strict
// signature was not attached, so devices in strict mode are not offered it.
type StrictReadyError struct {
	Build int
	// NotDeployedTo is set when a release stopped here, before it deployed.
	NotDeployedTo *Target
	Err           error
}

func (e *StrictReadyError) Error() string {
	if t := e.NotDeployedTo; t != nil {
		return fmt.Sprintf("build %d was uploaded but is not strict-ready, so it was not deployed: %v\nFinish with:\n  simpleota build make-strict %d <bin>\n  simpleota deployment create --build %d --channel %s --percent %d",
			e.Build, e.Err, e.Build, e.Build, shellWord(t.Channel), t.Percent)
	}
	return fmt.Sprintf("build %d was uploaded but is not strict-ready: %v\nDevices in strict mode will not be offered it until it is.\nFinish with:\n  simpleota build make-strict %d <bin>", e.Build, e.Err, e.Build)
}

func (e *StrictReadyError) Unwrap() error { return e.Err }

// DeployError means a build was uploaded, and made strict-ready when signed,
// but its deployment was not created or did not start.
type DeployError struct {
	Build        int
	DeploymentID string
	Target       Target
	Err          error
}

func (e *DeployError) Error() string {
	if e.DeploymentID == "" {
		// Without an answer from the server, the deployment may exist.
		check := ""
		var apiErr *api.Error
		if !errors.As(e.Err, &apiErr) {
			check = "\nThe server did not answer, so the deployment may exist. Check first with:\n  simpleota deployment list"
		}
		return fmt.Sprintf("build %d was uploaded, but its deployment was not created: %v%s\nDeploy it with:\n  simpleota deployment create --build %d --channel %s --percent %d",
			e.Build, e.Err, check, e.Build, shellWord(e.Target.Channel), e.Target.Percent)
	}
	id := termtext.Clean(e.DeploymentID)
	return fmt.Sprintf("build %d was uploaded, and deployment %s was created but did not start: %v\nCancel it and deploy again with:\n  simpleota deployment cancel %s\n  simpleota deployment create --build %d --channel %s --percent %d",
		e.Build, id, e.Err, id, e.Build, shellWord(e.Target.Channel), e.Target.Percent)
}

func (e *DeployError) Unwrap() error { return e.Err }

// shellWord quotes a channel name for a command the user can paste.
func shellWord(s string) string {
	s = termtext.Clean(s)
	if strings.ContainsAny(s, " \t'\"$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// signManifest signs m and attaches the signature to the artifact. The private
// key stays here: only the signature is sent.
func (s Service) signManifest(ctx context.Context, key ed25519.PrivateKey, artifactID string, m signing.Manifest) (api.Artifact, error) {
	sig, err := signing.SignManifest(key, m)
	if err != nil {
		return api.Artifact{}, err
	}
	return s.API.AttachManifest(ctx, s.ProjectID, artifactID, api.ManifestRequest{ManifestSignature: sig, ManifestVersion: signing.ManifestVersion})
}

// releaseStrictReady uploads a signed build and makes it strict-ready, then deploys it to
// one channel. The channel is looked up first, so a wrong name costs no
// build number.
func (s Service) releaseStrictReady(ctx context.Context, p prepared, allowDuplicate bool, t Target) (Result, error) {
	channelID, err := s.channelID(ctx, t.Channel)
	if err != nil {
		return p.res, err
	}
	res, err := s.uploadAndSignManifest(ctx, p, allowDuplicate, &t)
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
		return res, &DeployError{Build: res.BuildNumber, Target: t, Err: err}
	}
	d, err := s.API.TransitionDeployment(ctx, s.ProjectID, draft.ID, api.Transition{Action: "start"})
	if err != nil {
		return res, &DeployError{Build: res.BuildNumber, DeploymentID: draft.ID, Target: t, Err: err}
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

// MakeStrict makes a build uploaded earlier strict-ready: one whose manifest
// failed after the upload, or one uploaded by an older CLI. The file
// must be the build's own binary and key the key that signed it.
func (s Service) MakeStrict(ctx context.Context, key ed25519.PrivateKey, build int, binaryPath string) (api.Artifact, error) {
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
		return art, fmt.Errorf("build %d is %s, not signed\nOnly a signed build can be made strict-ready.", build, termtext.Clean(art.SecurityMode))
	case !strings.EqualFold(art.ChecksumSHA256, sha):
		return art, fmt.Errorf("%s is not build %d: its SHA-256 is %s, the build's is %s", binaryPath, build, sha, termtext.Clean(art.ChecksumSHA256))
	}
	keyID, err := s.registeredKeyID(ctx, key.Public().(ed25519.PublicKey))
	if err != nil {
		return art, err
	}
	if art.SigningKeyID != "" && art.SigningKeyID != keyID {
		return art, fmt.Errorf("build %d was signed with key %q, not %q\nUse the key that signed it.", build, termtext.Clean(art.SigningKeyID), keyID)
	}
	m := signing.Manifest{ProjectID: strings.ToLower(s.ProjectID), KeyID: keyID, Build: art.BuildNumber, SHA256: sha}
	return s.signManifest(ctx, key, art.ID, m)
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
