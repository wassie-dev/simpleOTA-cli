package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) deploymentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deployment",
		Aliases: []string{"deployments"},
		Short:   "Create, list, show and change deployments",
		Long: `A deployment offers one build to devices in the current project. A device
is offered the build if it is on a targeted channel, inside the rollout
percentage, and able to run the build.

A deployment is active, paused, completed or cancelled. Several can be
active at once; each device installs the highest build offered to it.

There is no rollback: devices are never offered a build at or below the one
they run. To go back, release the previous source again as a new, higher
build.`,
		Example: `  simpleota deployment list
  simpleota deployment show
  simpleota deployment ramp 5f0c2b1e-... --percent 50`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(a.deploymentListCommand(), a.deploymentShowCommand(), a.deploymentCreateCommand())
	for _, action := range deploy.Actions {
		cmd.AddCommand(a.deploymentActionCommand(action))
	}
	return cmd
}

func (a *App) deploymentListCommand() *cobra.Command {
	return withLimit(&cobra.Command{
		Use:   "list",
		Short: "List the project's deployments, newest first",
		Long: `List the current project's deployments, newest first, with each one's build,
state and rollout percentage.`,
		Example: `  simpleota deployment list
  simpleota deployment list --json | jq -r '.[0].id'`,
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
			return printList(a, cmd, views, a.printer().DeploymentList)
		},
	})
}

func (a *App) deploymentShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [deployment-id]",
		Short: "Show a deployment's state and device progress",
		Long: `Show a deployment's build, state and rollout percentage, and how many devices
have been offered the build, confirmed it, are installing it, failed or
rolled back. Failures list the reason each device gave.

Without an ID, show the newest deployment. A device counts once it has
checked in and been offered the build.`,
		Example: `  simpleota deployment show
  simpleota deployment show 5f0c2b1e-... --json`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDeploymentShow(cmd.Context(), firstArg(args))
		},
	}
}

// deploymentTarget validates an optional deployment ID, resolves the
// current project, and defaults the ID to the newest deployment.
func (a *App) deploymentTarget(ctx context.Context, ref string) (*session, string, string, error) {
	if ref != "" && !api.IsUUID(ref) {
		return nil, "", "", notDeploymentID(ref)
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

// notDeploymentID refuses something that cannot be a deployment ID.
func notDeploymentID(ref string) error {
	return usageErr("%q is not a deployment ID\nList deployments with:\n  simpleota deployment list", ref)
}

// deploymentErr turns a 404 into a usage error naming the likely cause.
func deploymentErr(err error, id string) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
		msg := fmt.Sprintf("no deployment %s in this project\nCheck the ID and the current project.", id)
		return &exitError{code: ExitUsage, err: &explainedErr{msg: msg, cause: err}}
	}
	return err
}
