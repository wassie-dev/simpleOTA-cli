package cli

import (
	"context"

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
	deploy.ActionResume: `Offer the build again after a pause, at the same percentage.`,
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
			return a.runRollout(cmd.Context(), action, args[0], pct)
		},
	}
	if action == deploy.ActionRamp {
		cmd.Use = "ramp <deployment-id> --percent <n>"
		cmd.Example = "  simpleota deployment ramp 5f0c2b1e-... --percent 50"
		cmd.Flags().IntVar(&percent, "percent", 0, "raise the rollout to `n` percent (required)")
	}
	return cmd
}

func (a *App) runRollout(ctx context.Context, action, deploymentID string, percent *int) error {
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
	d, err := s.api.TransitionDeployment(ctx, projectID, deploymentID, t)
	if err != nil {
		return deploymentErr(err, deploymentID)
	}
	return a.printer().Rollout(output.RolloutView{
		Action:     action,
		Deployment: output.NewDeploymentView(d),
		NextStep:   output.NextStep(d),
	})
}
