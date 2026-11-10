package output

import (
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// ReleaseView is the result of release.
type ReleaseView struct {
	BuildNumber  int    `json:"build_number"`
	ArtifactID   string `json:"artifact_id"`
	DeploymentID string `json:"deployment_id"`
	State        string `json:"state"`
	Channel      string `json:"channel"`
	SecurityMode string `json:"security_mode"`
	KeyID        string `json:"key_id,omitempty"`
	StrictReady  bool   `json:"strict_ready"`
	Chip         string `json:"chip"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	// AlsoActive lists the other active deployments for the same chip on
	// the channel, highest build first.
	AlsoActive []DeploymentView `json:"also_active"`
}

// NewReleaseView converts a release result.
func NewReleaseView(r release.Result) ReleaseView {
	return ReleaseView{
		BuildNumber:  r.BuildNumber,
		ArtifactID:   r.ArtifactID,
		DeploymentID: r.DeploymentID,
		State:        r.State,
		Channel:      r.Channel,
		SecurityMode: r.SecurityMode,
		KeyID:        r.KeyID,
		StrictReady:  r.StrictReady,
		Chip:         r.Chip,
		SHA256:       r.SHA256,
		Size:         r.Size,
		AlsoActive:   []DeploymentView{},
	}
}

// Release prints a release result.
func (p Printer) Release(r ReleaseView) error {
	if p.JSON {
		return p.WriteJSON(r)
	}
	fmt.Fprintf(p.Out, "%s build %d for %s (%s)\n", p.green("Released"), r.BuildNumber, r.Chip, securityText(r.SecurityMode, r.KeyID, r.StrictReady))
	fmt.Fprintf(p.Out, "  deployment  %s (%s, channel %s)\n", termtext.Clean(r.DeploymentID), termtext.Clean(r.State), termtext.Clean(r.Channel))
	fmt.Fprintf(p.Out, "  sha256      %s\n  size        %d bytes\n", r.SHA256, r.Size)
	p.alsoActive(r.AlsoActive, r.BuildNumber)
	p.nextStep("Watch it", "simpleota deployment wait "+termtext.Clean(r.DeploymentID))
	return nil
}
