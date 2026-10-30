package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) deploymentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deployment",
		Aliases: []string{"deployments"},
		Short:   "List, watch and change deployments",
		Long: `A deployment offers one build to devices in the current project: those on
the channels it targets, in its rollout percentage, that can run the
build. simpleota release creates one and starts it.

A deployment is active, paused, completed or cancelled. Ramp it to reach
more devices, pause it to stop offering the build, complete it once it is
done, or cancel it for good. Several deployments can be active at once;
each device installs the highest build offered to it.

There is no rollback: SimpleOTA never offers a build at or below the one a
device runs, so to go back you release the previous source again as a
new, higher build.

Commands that take a deployment ID print it, as do release and
deployment list. show and wait default to the newest deployment.`,
		Example: `  simpleota deployment list
  simpleota deployment show
  simpleota deployment wait 5f0c2b1e-... --timeout 30m
  simpleota deployment ramp 5f0c2b1e-... --percent 50`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(a.deploymentListCommand(), a.deploymentShowCommand(), a.deploymentWaitCommand())
	for _, action := range deploy.Actions {
		cmd.AddCommand(a.deploymentActionCommand(action))
	}
	return cmd
}

var actionHelp = map[string]string{
	deploy.ActionRamp:     "Offer the build to a larger share of devices (it cannot go down)",
	deploy.ActionPause:    "Stop offering the build until the deployment is resumed",
	deploy.ActionResume:   "Offer the build again after a pause",
	deploy.ActionComplete: "Mark the deployment finished",
	deploy.ActionCancel:   "Stop the deployment for good",
}

var actionLong = map[string]string{
	deploy.ActionRamp: `Raise the share of the targeted devices that are offered the build. Each
device falls in a fixed bucket, so a device in at 10% stays in at 50%. The
percentage cannot go down, since devices that already took the build
would be decided again; to shrink a rollout, cancel the deployment and
create a new one.`,
	deploy.ActionPause: `Stop offering the build. Devices that already installed it keep it.
Rollbacks also pause a deployment, once they reach its rollback threshold.
Resume it to offer the build again.`,
	deploy.ActionResume: `Offer the build again after a pause, at the same percentage.`,
	deploy.ActionComplete: `Mark an active or paused deployment finished. It stops offering the build,
and stays in the history as completed. This cannot be undone.`,
	deploy.ActionCancel: `Stop a deployment for good. It stops offering the build, and devices that
already installed it keep it. This cannot be undone; to go back, release
the previous source again as a new, higher build.`,
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
		cmd.Flags().IntVar(&percent, "percent", 0, "new rollout percentage (required)")
	}
	return cmd
}

func (a *App) runRollout(ctx context.Context, action, deploymentID string, percent *int) error {
	t, err := deploy.BuildTransition(action, percent)
	if err != nil {
		return usageErr("%v", err)
	}
	if !api.IsUUID(deploymentID) {
		return usageErr("%q is not a deployment ID; release and deployment list print it", deploymentID)
	}
	s, err := a.openSession()
	if err != nil {
		return err
	}
	projectID, err := s.projectID(ctx)
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

func (a *App) deploymentListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the project's deployments, newest first",
		Long: `List the current project's deployments, newest first, with the build each
one offers, its state and its rollout percentage.`,
		Example: `  simpleota deployment list
  simpleota deployment list --json | jq -r '.[] | select(.state == "active") | .id'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			deps, err := s.api.ListDeployments(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			sort.SliceStable(deps, func(i, j int) bool { return deps[i].CreatedAt.After(deps[j].CreatedAt) })
			views := make([]output.DeploymentView, len(deps))
			for i, d := range deps {
				views[i] = output.NewDeploymentView(d)
			}
			return a.printer().DeploymentList(views)
		},
	}
}

func (a *App) deploymentShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [deployment-id]",
		Short: "Show a deployment's state and device progress",
		Long: `Show a deployment (the newest by default): its build, state and rollout
percentage, and how many devices have been offered the build, confirmed
it, are still installing, failed or rolled back, with the reason each
failure gave. A device counts once it has checked in and been offered the
build.`,
		Example: `  simpleota deployment show
  simpleota deployment show 5f0c2b1e-... --json`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDeploymentStatus(cmd.Context(), firstArg(args), false, 0, 0)
		},
	}
}

func (a *App) deploymentWaitCommand() *cobra.Command {
	var timeout, interval time.Duration
	cmd := &cobra.Command{
		Use:   "wait [deployment-id]",
		Short: "Wait for a deployment to succeed or fail, for CI",
		Long: `Poll a deployment (the newest by default) until it succeeds or fails, and
exit with a code a CI job can gate on:

  0  every device the current rollout step reached has confirmed
  4  a device failed or rolled back; the deployment is paused, cancelled or
     not started; or it was completed before any device was offered it
  5  --timeout passed first

A device counts once it has checked in and been offered the build, so a
wait finishes only after devices check in. Network and gateway errors
while polling are retried until the timeout. Progress goes to stderr, and
the final state to stdout.`,
		Example: `  simpleota deployment wait
  ID=$(simpleota release build/app.bin --version-label 1.4.0 --percent 10 --json | jq -r .deployment_id)
  simpleota deployment wait "$ID" --timeout 30m`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDeploymentStatus(cmd.Context(), firstArg(args), true, timeout, interval)
		},
	}
	fl := cmd.Flags()
	fl.DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait")
	fl.DurationVar(&interval, "interval", 10*time.Second, "how often to poll")
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func (a *App) runDeploymentStatus(ctx context.Context, deploymentID string, wait bool, timeout, interval time.Duration) error {
	if deploymentID != "" && !api.IsUUID(deploymentID) {
		return usageErr("%q is not a deployment ID", deploymentID)
	}
	if wait && timeout <= 0 {
		return usageErr("--timeout must be positive")
	}
	if wait && interval < time.Second {
		return usageErr("--interval must be at least 1s")
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return err
	}
	if deploymentID == "" {
		latest, err := deploy.Latest(ctx, s.api, projectID)
		if err != nil {
			return err
		}
		deploymentID = latest.ID
	}
	p := a.printer()

	if !wait {
		sum, err := deploy.Status(ctx, s.api, projectID, deploymentID)
		if err != nil {
			return deploymentErr(err, deploymentID)
		}
		outcome := deploy.Evaluate(sum)
		return p.DeploymentStatus(output.NewDeploymentStatusView(sum, outcome))
	}

	w := deploy.Waiter{
		API:       s.api,
		ProjectID: projectID,
		Interval:  interval,
		Timeout:   timeout,
		Now:       a.Now,
		Sleep:     a.Sleep,
	}
	if !p.JSON {
		w.OnProgress = p.Progress
	}
	w.OnPollError = func(err error) { p.Notice("Polling failed, retrying: %v", err) }
	sum, outcome, err := w.Wait(ctx, deploymentID)
	if err != nil {
		return deploymentErr(err, deploymentID)
	}
	if err := p.DeploymentStatus(output.NewDeploymentStatusView(sum, outcome)); err != nil {
		return err
	}
	switch outcome {
	case deploy.OutcomeDeviceFailed:
		return &exitError{code: ExitDeployment, err: fmt.Errorf("deployment %s: %s", deploymentID, failureText(sum))}
	case deploy.OutcomeStopped:
		msg := fmt.Sprintf("deployment %s is %s", deploymentID, sum.Deployment.State)
		if next := output.NextStep(sum.Deployment); next != "" {
			msg += ". " + next
		}
		return &exitError{code: ExitDeployment, err: fmt.Errorf("%s", msg)}
	case deploy.OutcomeNoDevices:
		return &exitError{code: ExitDeployment, err: fmt.Errorf("deployment %s was completed before any device was offered build %d, so no device updated", deploymentID, sum.Deployment.ArtifactBuildNumber)}
	case deploy.OutcomeTimedOut:
		if sum.Counts.Offered == 0 {
			return &exitError{code: ExitTimeout, err: fmt.Errorf("timed out after %s: no device has been offered build %d yet; a device is offered it when it checks in, if it is on the channel, in the rollout and compatible", timeout, sum.Deployment.ArtifactBuildNumber)}
		}
		return &exitError{code: ExitTimeout, err: fmt.Errorf("timed out after %s: %d of %d offered devices confirmed", timeout, sum.Counts.Confirmed, sum.Counts.Offered)}
	}
	return nil
}

// deploymentErr turns a 404 into a message naming the likely cause.
func deploymentErr(err error, id string) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
		return fmt.Errorf("deployment %s was not found in this project; check the ID and --project", id)
	}
	return err
}

func failureText(s deploy.Summary) string {
	parts := make([]string, 0, len(s.Failures))
	for _, f := range s.Failures {
		parts = append(parts, fmt.Sprintf("device %s %s", f.DeviceID, strings.ReplaceAll(f.Status, "_", " ")))
	}
	return strings.Join(parts, "; ")
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usageErr("%s takes at most %d argument(s), got %d; see simpleota %s --help", cmd.Name(), n, len(args), cmd.Name())
		}
		return nil
	}
}
