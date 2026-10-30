package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
)

// DeploymentView is a deployment in every command that shows one.
type DeploymentView struct {
	ID                string     `json:"id"`
	ProjectID         string     `json:"project_id"`
	BuildNumber       int        `json:"build_number"`
	State             string     `json:"state"`
	RolloutStrategy   string     `json:"rollout_strategy"`
	RolloutPercentage int        `json:"rollout_percentage"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
}

// RolloutView is the result of rollout.
type RolloutView struct {
	Action     string         `json:"action"`
	Deployment DeploymentView `json:"deployment"`
	NextStep   string         `json:"next_step,omitempty"`
}

// DeploymentStatusView is the result of deployment show and deployment wait.
type DeploymentStatusView struct {
	Deployment DeploymentView   `json:"deployment"`
	Devices    deploy.Counts    `json:"devices"`
	Failures   []deploy.Failure `json:"failures"`
	Outcome    deploy.Outcome   `json:"outcome"`
}

// NewDeploymentView converts an API deployment.
func NewDeploymentView(d api.Deployment) DeploymentView {
	return DeploymentView{
		ID:                d.ID,
		ProjectID:         d.ProjectID,
		BuildNumber:       d.ArtifactBuildNumber,
		State:             d.State,
		RolloutStrategy:   d.RolloutStrategy,
		RolloutPercentage: d.RolloutPercentage,
		CreatedAt:         d.CreatedAt,
		StartedAt:         d.StartedAt,
		CompletedAt:       d.CompletedAt,
	}
}

// NewDeploymentStatusView converts a summary and its outcome.
func NewDeploymentStatusView(s deploy.Summary, o deploy.Outcome) DeploymentStatusView {
	failures := s.Failures
	if failures == nil {
		failures = []deploy.Failure{}
	}
	return DeploymentStatusView{Deployment: NewDeploymentView(s.Deployment), Devices: s.Counts, Failures: failures, Outcome: o}
}

// NextStep is the advice printed after a rollout stops. SimpleOTA never
// offers a lower build, so going back means shipping the old code again.
func NextStep(d api.Deployment) string {
	switch d.State {
	case api.StatePaused, api.StateCancelled:
		return "To ship a fix or go back, release the fixed or previous source again as a new build: simpleota release <bin> --version-label <label>"
	}
	return ""
}

// CreatedView is the result of deployment create.
type CreatedView struct {
	Deployment DeploymentView `json:"deployment"`
	// Targets names the channels and groups the deployment reaches; empty
	// means every device in the project.
	Targets    []string         `json:"targets"`
	AlsoActive []DeploymentView `json:"also_active"`
}

// Created prints the result of deployment create.
func (p Printer) Created(v CreatedView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	d := v.Deployment
	audience := "every device in the project"
	if len(v.Targets) > 0 {
		audience = strings.Join(v.Targets, ", ")
	}
	fmt.Fprintf(p.Out, "%s deployment %s\n", p.green("Started"), d.ID)
	fmt.Fprintf(p.Out, "  build    %d, offered to %d%% of %s\n", d.BuildNumber, d.RolloutPercentage, audience)
	p.alsoActive(v.AlsoActive, d.BuildNumber)
	fmt.Fprintf(p.Out, "Watch it with: simpleota deployment wait %s\n", d.ID)
	return nil
}

// alsoActive explains the other active deployments that still reach some
// of the same devices.
func (p Printer) alsoActive(others []DeploymentView, build int) {
	if len(others) == 0 {
		return
	}
	fmt.Fprintln(p.Out, p.yellow("Also active for the same devices:"))
	for _, d := range others {
		fmt.Fprintf(p.Out, "  deployment  %s (build %d, %d%%)\n", d.ID, d.BuildNumber, d.RolloutPercentage)
	}
	fmt.Fprintf(p.Out, "Each device installs the highest build offered to it, so these still reach\nthe devices build %d does not. Stop one with: simpleota deployment complete <id>\n", build)
}

// Rollout prints the result of a rollout action.
func (p Printer) Rollout(v RolloutView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	d := v.Deployment
	fmt.Fprintf(p.Out, "Deployment %s (build %d) is %s at %d%%\n", d.ID, d.BuildNumber, p.state(d.State), d.RolloutPercentage)
	if v.NextStep != "" {
		fmt.Fprintln(p.Out, v.NextStep)
	}
	return nil
}

// DeploymentStatus prints a deployment summary.
func (p Printer) DeploymentStatus(v DeploymentStatusView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	d := v.Deployment
	fmt.Fprintf(p.Out, "Deployment %s\n", p.bold(d.ID))
	fmt.Fprintf(p.Out, "  build    %d\n", d.BuildNumber)
	fmt.Fprintf(p.Out, "  state    %s, %d%% (%s)\n", p.state(d.State), d.RolloutPercentage, d.RolloutStrategy)
	fmt.Fprintf(p.Out, "  devices  %s\n", p.counts(v.Devices))
	for _, f := range v.Failures {
		reason := f.Reason
		if reason == "" {
			reason = "no reason given"
		}
		fmt.Fprintf(p.Out, "  %s  device %s: %s\n", p.red(f.Status), f.DeviceID, reason)
	}
	switch v.Outcome {
	case deploy.OutcomeSucceeded:
		fmt.Fprintln(p.Out, p.green("Every device the rollout reached has confirmed."))
	case deploy.OutcomeDeviceFailed:
		fmt.Fprintln(p.Out, p.red("A device failed or rolled back."))
	case deploy.OutcomeStopped:
		fmt.Fprintln(p.Out, p.yellow("The deployment is not running."))
	case deploy.OutcomeNoDevices:
		fmt.Fprintln(p.Out, p.yellow("The deployment was completed before any device was offered the build."))
	case deploy.OutcomeTimedOut:
		fmt.Fprintln(p.Out, p.yellow("Timed out before every device confirmed."))
	}
	return nil
}

// Progress is the one-line update printed to Err while waiting.
func (p Printer) Progress(s deploy.Summary) {
	p.Notice("%s  %s, %d%%: %s", time.Now().Format("15:04:05"), s.Deployment.State, s.Deployment.RolloutPercentage, p.counts(s.Counts))
}

func (p Printer) counts(c deploy.Counts) string {
	parts := []string{
		fmt.Sprintf("%d offered", c.Offered),
		p.green(fmt.Sprintf("%d confirmed", c.Confirmed)),
		fmt.Sprintf("%d in progress", c.InProgress),
	}
	failed := fmt.Sprintf("%d failed", c.Failed)
	rolled := fmt.Sprintf("%d rolled back", c.RolledBack)
	if c.Failed > 0 {
		failed = p.red(failed)
	}
	if c.RolledBack > 0 {
		rolled = p.red(rolled)
	}
	return strings.Join(append(parts, failed, rolled), ", ")
}

func (p Printer) state(s string) string {
	switch s {
	case api.StateActive, api.StateCompleted:
		return p.green(s)
	case api.StatePaused, api.StateDraft:
		return p.yellow(s)
	case api.StateCancelled:
		return p.red(s)
	}
	return s
}

func (p Printer) red(s string) string    { return p.paint("31", s) }
func (p Printer) yellow(s string) string { return p.paint("33", s) }
func (p Printer) bold(s string) string   { return p.paint("1", s) }
