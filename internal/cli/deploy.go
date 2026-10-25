package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) rolloutCommand() *cobra.Command {
	var percent int
	cmd := &cobra.Command{
		Use:   "rollout <ramp|pause|resume|complete|cancel> <deployment-id>",
		Short: "Ramp, pause, resume, complete or cancel a deployment",
		Long: `Change a deployment's state. ramp raises the share of devices offered the
build (it cannot go down). There is no rollback: SimpleOTA never offers a
build at or below what a device runs, so to go back you release the
previous source again as a new, higher build.`,
		Example: `  simpleota rollout ramp 5f0c2b1e-... --percent 50
  simpleota rollout pause 5f0c2b1e-...`,
		ValidArgs: deploy.Actions,
		Args:      exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var pct *int
			if cmd.Flags().Changed("percent") {
				pct = &percent
			}
			return a.runRollout(cmd.Context(), args[0], args[1], pct)
		},
	}
	cmd.Flags().IntVar(&percent, "percent", 0, "new rollout percentage (ramp only)")
	return cmd
}

func (a *App) runRollout(ctx context.Context, action, deploymentID string, percent *int) error {
	t, err := deploy.BuildTransition(action, percent)
	if err != nil {
		return usageErr("%v", err)
	}
	if !api.IsUUID(deploymentID) {
		return usageErr("%q is not a deployment ID; release and status print it", deploymentID)
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

func (a *App) statusCommand() *cobra.Command {
	var wait bool
	var timeout, interval time.Duration
	cmd := &cobra.Command{
		Use:   "status [deployment-id]",
		Short: "Show a deployment's state and device progress",
		Long: `Show a deployment (the latest one by default) and how many devices have
been offered it, confirmed, failed or rolled back.

With --wait, poll until every device the current rollout step has reached
has confirmed (exit 0). Exit 4 if a device fails or rolls back or the
deployment is paused, cancelled or not started, and 5 on timeout. Devices
count once they have checked in and been offered the build. Network and
gateway errors while polling are retried until the timeout.`,
		Example: `  simpleota status
  simpleota status 5f0c2b1e-... --wait --timeout 30m`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return a.runStatus(cmd.Context(), id, wait, timeout, interval)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&wait, "wait", false, "wait for the rollout to succeed or fail")
	fl.DurationVar(&timeout, "timeout", 30*time.Minute, "how long --wait waits")
	fl.DurationVar(&interval, "interval", 10*time.Second, "how often --wait polls")
	return cmd
}

func (a *App) runStatus(ctx context.Context, deploymentID string, wait bool, timeout, interval time.Duration) error {
	if deploymentID != "" && !api.IsUUID(deploymentID) {
		return usageErr("%q is not a deployment ID", deploymentID)
	}
	if wait && timeout <= 0 {
		return usageErr("--timeout must be positive")
	}
	if wait && interval < time.Second {
		return usageErr("--interval must be at least 1s")
	}
	s, err := a.openSession()
	if err != nil {
		return err
	}
	projectID, err := s.projectID(ctx)
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
		return p.Status(output.NewStatusView(sum, outcome))
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
	if err := p.Status(output.NewStatusView(sum, outcome)); err != nil {
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
	case deploy.OutcomeTimedOut:
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
