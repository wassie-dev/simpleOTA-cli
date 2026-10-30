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
		Short:   "Create, list, watch and change deployments",
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
	cmd.AddCommand(a.deploymentListCommand(), a.deploymentShowCommand(), a.deploymentWaitCommand(), a.deploymentCreateCommand())
	for _, action := range deploy.Actions {
		cmd.AddCommand(a.deploymentActionCommand(action))
	}
	return cmd
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
			views := viewsOf(deps, output.NewDeploymentView)
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
			return a.runDeploymentShow(cmd.Context(), firstArg(args))
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
			return a.runDeploymentWait(cmd.Context(), firstArg(args), timeout, interval)
		},
	}
	fl := cmd.Flags()
	fl.DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait")
	fl.DurationVar(&interval, "interval", 10*time.Second, "how often to poll")
	return cmd
}

// deploymentTarget validates an optional deployment ID, resolves the
// current project, and defaults the ID to the newest deployment.
func (a *App) deploymentTarget(ctx context.Context, ref string) (*session, string, string, error) {
	if ref != "" && !api.IsUUID(ref) {
		return nil, "", "", usageErr("%q is not a deployment ID; deployment list prints them", ref)
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return nil, "", "", err
	}
	if ref == "" {
		latest, err := deploy.Latest(ctx, s.api, projectID)
		if err != nil {
			return nil, "", "", err
		}
		ref = latest.ID
	}
	return s, projectID, ref, nil
}

func (a *App) runDeploymentShow(ctx context.Context, ref string) error {
	s, projectID, id, err := a.deploymentTarget(ctx, ref)
	if err != nil {
		return err
	}
	sum, err := deploy.Status(ctx, s.api, projectID, id)
	if err != nil {
		return deploymentErr(err, id)
	}
	return a.printer().DeploymentStatus(output.NewDeploymentStatusView(sum, deploy.Evaluate(sum)))
}

func (a *App) runDeploymentWait(ctx context.Context, ref string, timeout, interval time.Duration) error {
	if timeout <= 0 {
		return usageErr("--timeout must be positive")
	}
	if interval < time.Second {
		return usageErr("--interval must be at least 1s")
	}
	s, projectID, id, err := a.deploymentTarget(ctx, ref)
	if err != nil {
		return err
	}
	p := a.printer()
	w := deploy.Waiter{
		API:         s.api,
		ProjectID:   projectID,
		Interval:    interval,
		Timeout:     timeout,
		Now:         a.Now,
		Sleep:       a.Sleep,
		OnPollError: func(err error) { p.Notice("Polling failed, retrying: %v", err) },
	}
	if !p.JSON {
		w.OnProgress = p.Progress
	}
	sum, outcome, err := w.Wait(ctx, id)
	if err != nil {
		return deploymentErr(err, id)
	}
	if err := p.DeploymentStatus(output.NewDeploymentStatusView(sum, outcome)); err != nil {
		return err
	}
	return waitErr(sum, outcome, timeout)
}

// waitErr turns a wait outcome into the exit code CI gates on.
func waitErr(sum deploy.Summary, outcome deploy.Outcome, timeout time.Duration) error {
	d := sum.Deployment
	switch outcome {
	case deploy.OutcomeDeviceFailed:
		return &exitError{code: ExitDeployment, err: fmt.Errorf("deployment %s: %s", d.ID, failureText(sum))}
	case deploy.OutcomeStopped:
		msg := fmt.Sprintf("deployment %s is %s", d.ID, d.State)
		if next := output.NextStep(d); next != "" {
			msg += ". " + next
		}
		return &exitError{code: ExitDeployment, err: errors.New(msg)}
	case deploy.OutcomeNoDevices:
		return &exitError{code: ExitDeployment, err: fmt.Errorf("deployment %s was completed before any device was offered build %d, so no device updated", d.ID, d.ArtifactBuildNumber)}
	case deploy.OutcomeTimedOut:
		if sum.Counts.Offered == 0 {
			return &exitError{code: ExitTimeout, err: fmt.Errorf("timed out after %s: no device has been offered build %d yet; a device is offered it when it checks in, if it is on the channel, in the rollout and compatible", timeout, d.ArtifactBuildNumber)}
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
