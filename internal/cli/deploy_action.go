package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

var actionHelp = map[string]string{
	deploy.ActionRamp:     "Offer the build to a larger share of devices",
	deploy.ActionPause:    "Stop offering the build until resumed",
	deploy.ActionResume:   "Offer the build again after a pause",
	deploy.ActionComplete: "Mark the deployment finished",
	deploy.ActionCancel:   "Stop the deployment for good",
}

var actionLong = map[string]string{
	deploy.ActionRamp: `Raise the share of targeted devices that are offered the build. Each device
has a fixed place in the rollout, so a device included at 10% stays included
at 50%.

The percentage cannot go down. To shrink a rollout, cancel the deployment
and create a new one.`,
	deploy.ActionPause: `Stop offering the build until the deployment is resumed. Devices that already
installed it keep it.

Rollbacks also pause a deployment, once they reach its rollback threshold.`,
	deploy.ActionResume: `Offer the build again after a pause, at the same percentage.

A deployment that targets every device in a project with two or more devices
needs --all-devices to confirm, as it did when it was created.`,
	deploy.ActionComplete: `Mark an active or paused deployment finished. It stops offering the build,
and stays in the history as completed.

This cannot be undone.`,
	deploy.ActionCancel: `Stop a deployment for good. It stops offering the build, and devices that
already installed it keep it.

This cannot be undone. To go back to earlier firmware, release the previous
source again as a new, higher build.`,
}

func (a *App) deploymentActionCommand(action string) *cobra.Command {
	var percent int
	var allDevices bool
	cmd := &cobra.Command{
		Use:     action + " <deployment-id>",
		Short:   actionHelp[action],
		Long:    actionLong[action],
		Example: "  simpleota deployment " + action + " 5f0c2b1e-...",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var pct *int
			if cmd.Flags().Changed("percent") {
				pct = &percent
			}
			return a.runRollout(cmd.Context(), action, args[0], pct, allDevices)
		},
	}
	switch action {
	case deploy.ActionRamp:
		cmd.Use = "ramp <deployment-id> --percent <n>"
		cmd.Example = "  simpleota deployment ramp 5f0c2b1e-... --percent 50"
		cmd.Flags().IntVar(&percent, "percent", 0, "raise the rollout to `n` percent (required)")
	case deploy.ActionResume:
		cmd.Use = "resume <deployment-id> [--all-devices]"
		cmd.Example = "  simpleota deployment resume 5f0c2b1e-...\n  simpleota deployment resume 5f0c2b1e-... --all-devices"
		cmd.Flags().BoolVar(&allDevices, "all-devices", false, "confirm resuming a deployment that targets every device in the project")
	}
	return cmd
}

func (a *App) runRollout(ctx context.Context, action, deploymentID string, percent *int, allDevices bool) error {
	t, err := deploy.BuildTransition(action, percent)
	if err != nil {
		return usageErr("%v", err)
	}
	if !api.IsUUID(deploymentID) {
		return notDeploymentID(deploymentID)
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return err
	}
	if allDevices {
		d, err := s.api.GetDeployment(ctx, projectID, deploymentID)
		if err != nil {
			return deploymentErr(err, deploymentID)
		}
		if !deploy.TargetsAllDevices(d) {
			return usageErr("deployment %s targets channels or groups, not every device\nResume it without --all-devices.", deploymentID)
		}
		t.ConfirmAll = true
	}
	d, err := s.api.TransitionDeployment(ctx, projectID, deploymentID, t)
	if err != nil {
		if action == deploy.ActionResume && !allDevices && needsAllDevices(ctx, s, projectID, deploymentID, err) {
			return usageErr("deployment %s targets every device in the project\nConfirm with:\n  simpleota deployment resume %s --all-devices", deploymentID, deploymentID)
		}
		return deploymentErr(err, deploymentID)
	}
	return a.printer().Rollout(output.RolloutView{
		Action:     action,
		Deployment: output.NewDeploymentView(d),
		NextStep:   output.NextStep(d),
	})
}

// needsAllDevices reports whether a refused resume was refused for want of
// the confirmation a deployment for every device needs.
func needsAllDevices(ctx context.Context, s *session, projectID, deploymentID string, err error) bool {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadRequest {
		return false
	}
	d, getErr := s.api.GetDeployment(ctx, projectID, deploymentID)
	return getErr == nil && d.State == api.StatePaused && deploy.TargetsAllDevices(d)
}
