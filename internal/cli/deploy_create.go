package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

type createFlags struct {
	build      string
	channels   []string
	groups     []string
	allDevices bool
	percent    int
	notes      string
}

func (a *App) deploymentCreateCommand() *cobra.Command {
	var f createFlags
	cmd := &cobra.Command{
		Use:   "create --build <build-number>",
		Short: "Deploy an uploaded build",
		Long: `Create a deployment of an uploaded build and start it. It is offered to
the devices on the given channels (stable by default) and in the given
groups, in the rollout percentage, that can run the build. Channels are a
union, as are groups; with both, a device must match each.

--all-devices targets every device in the project instead, whatever its
channel or group. An archived build cannot be deployed.

When another deployment for the same chip is still active for the same
devices, create names it. A device installs the highest build offered to
it, so the older deployment goes on reaching the devices this one does not.`,
		Example: `  simpleota deployment create --build 42
  simpleota deployment create --build 42 --channel beta --percent 10
  simpleota deployment create --build 42 --group kitchen --group hall
  simpleota deployment create --build 42 --all-devices`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runCreateDeployment(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.build, "build", "", "build number to deploy (required)")
	fl.StringArrayVar(&f.channels, "channel", nil, "release channel to target; repeat for more (default: stable)")
	fl.StringArrayVar(&f.groups, "group", nil, "device group to target; repeat for more")
	fl.BoolVar(&f.allDevices, "all-devices", false, "target every device in the project")
	fl.IntVar(&f.percent, "percent", 100, "percentage of the targeted devices to offer the build to")
	fl.StringVar(&f.notes, "notes", "", "a note to keep with the deployment")
	return cmd
}

func (a *App) runCreateDeployment(ctx context.Context, f createFlags) error {
	switch {
	case f.build == "":
		return usageErr("--build is required; simpleota build list prints build numbers")
	case f.percent < 0 || f.percent > 100:
		return usageErr("--percent must be between 0 and 100")
	case f.allDevices && (len(f.channels) > 0 || len(f.groups) > 0):
		return usageErr("--all-devices targets every device, so it cannot be combined with --channel or --group")
	case !f.allDevices && len(f.channels) == 0 && len(f.groups) == 0:
		f.channels = []string{api.DefaultChannel}
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return err
	}
	art, err := findBuild(ctx, s.api, projectID, f.build)
	if err != nil {
		return err
	}
	if art.Archived {
		return usageErr("build %d is archived; unarchive it first with simpleota build unarchive %d", art.BuildNumber, art.BuildNumber)
	}
	req := api.DeploymentRequest{
		ArtifactID:        art.ID,
		RolloutStrategy:   api.RolloutImmediate,
		RolloutPercentage: f.percent,
		TargetChannelIDs:  []string{},
		TargetGroupIDs:    []string{},
		Notes:             f.notes,
	}
	targets := []string{}
	if len(f.channels) > 0 {
		channels, err := s.api.ListChannels(ctx, projectID)
		if err != nil {
			return err
		}
		for _, name := range f.channels {
			id, err := matchChannelID(channels, name)
			if err != nil {
				return err
			}
			req.TargetChannelIDs = append(req.TargetChannelIDs, id)
			targets = append(targets, "channel "+name)
		}
	}
	for _, name := range f.groups {
		id, err := findGroupID(ctx, s.api, projectID, name)
		if err != nil {
			return err
		}
		req.TargetGroupIDs = append(req.TargetGroupIDs, id)
		targets = append(targets, "group "+name)
	}

	draft, err := s.api.CreateDeployment(ctx, projectID, req)
	if err != nil {
		return err
	}
	d, err := s.api.TransitionDeployment(ctx, projectID, draft.ID, api.Transition{Action: deploy.ActionStart, ConfirmAll: f.allDevices})
	if err != nil {
		return fmt.Errorf("deployment %s was created but did not start (%w); cancel it with simpleota deployment cancel %s", draft.ID, err, draft.ID)
	}
	p := a.printer()
	return p.Created(output.CreatedView{
		Deployment: output.NewDeploymentView(d),
		Targets:    targets,
		AlsoActive: alsoActive(ctx, p, s, projectID, d.ID, art.ChipFamily),
	})
}
