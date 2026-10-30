package output

import (
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/release"
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
	fmt.Fprintf(p.Out, "%s build %d for %s (%s)\n", p.green("Released"), r.BuildNumber, r.Chip, securityText(r.SecurityMode, r.KeyID))
	fmt.Fprintf(p.Out, "  deployment  %s (%s, channel %s)\n", r.DeploymentID, r.State, r.Channel)
	fmt.Fprintf(p.Out, "  sha256      %s (%d bytes, checked by the server)\n", r.SHA256, r.Size)
	p.alsoActive(r.AlsoActive, r.BuildNumber)
	fmt.Fprintf(p.Out, "Watch it with: simpleota deployment wait %s\n", r.DeploymentID)
	return nil
}
