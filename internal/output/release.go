package output

import (
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/release"
)

// ReleaseView is the result of release.
type ReleaseView struct {
	BuildNumber    int    `json:"build_number"`
	ArtifactID     string `json:"artifact_id"`
	DeploymentID   string `json:"deployment_id"`
	State          string `json:"state"`
	Channel        string `json:"channel"`
	SecurityMode   string `json:"security_mode"`
	KeyID          string `json:"key_id,omitempty"`
	SHA256         string `json:"sha256"`
	SHA256Verified bool   `json:"sha256_verified"`
	Size           int64  `json:"size"`
}

// NewReleaseView converts a release result.
func NewReleaseView(r release.Result) ReleaseView {
	return ReleaseView{
		BuildNumber:    r.BuildNumber,
		ArtifactID:     r.ArtifactID,
		DeploymentID:   r.DeploymentID,
		State:          r.State,
		Channel:        r.Channel,
		SecurityMode:   r.SecurityMode,
		KeyID:          r.KeyID,
		SHA256:         r.SHA256,
		SHA256Verified: r.SHA256Verified,
		Size:           r.Size,
	}
}

// Release prints a release result.
func (p Printer) Release(r ReleaseView) error {
	if p.JSON {
		return p.WriteJSON(r)
	}
	mode := r.SecurityMode
	if r.KeyID != "" {
		mode += " with key " + r.KeyID
	}
	fmt.Fprintf(p.Out, "%s build %d (%s)\n", p.green("Released"), r.BuildNumber, mode)
	fmt.Fprintf(p.Out, "  deployment  %s (%s, channel %s)\n", r.DeploymentID, r.State, r.Channel)
	check := "verified"
	if !r.SHA256Verified {
		check = "not verified"
	}
	fmt.Fprintf(p.Out, "  sha256      %s (%d bytes, %s)\n", r.SHA256, r.Size, check)
	fmt.Fprintf(p.Out, "Watch it with: simpleota status %s --wait\n", r.DeploymentID)
	return nil
}

func (p Printer) paint(code, s string) string {
	if !p.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Printer) green(s string) string { return p.paint("32", s) }
