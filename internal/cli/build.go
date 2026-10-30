package cli

import (
	"context"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) buildCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "build",
		Aliases: []string{"builds"},
		Short:   "List, upload and archive firmware builds",
		Long: `A build is one uploaded firmware image in the current project, numbered
by the server in upload order. Devices are offered only builds higher than
the one they run. The API calls builds artifacts.

simpleota release uploads a build and deploys it in one step; build upload
only uploads it, for simpleota deployment create to deploy later. Builds
are never edited or deleted. Archiving takes one out of service: it is no
longer offered, and cannot be deployed until it is unarchived.`,
		Example: `  simpleota build list
  simpleota build show 42
  simpleota build upload build/app.bin --version-label 1.4.0
  simpleota build archive 41`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the project's builds, newest first",
		Long: `List the current project's builds, newest first, with their version
label, chip, security mode and whether they are archived.`,
		Example: `  simpleota build list
  simpleota build list --json | jq '.[0].build_number'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			artifacts, err := s.api.ListArtifacts(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			sort.SliceStable(artifacts, func(i, j int) bool { return artifacts[i].BuildNumber > artifacts[j].BuildNumber })
			views := viewsOf(artifacts, output.NewBuildView)
			return a.printer().BuildList(views)
		},
	}, &cobra.Command{
		Use:   "show <build-number>",
		Short: "Show one build",
		Long: `Show a build's version label, compatibility (framework, chip, board,
partition profile, NVS schema), security mode, SHA-256, size and any
warnings the server recorded at upload.`,
		Example: `  simpleota build show 42`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			art, err := findBuild(cmd.Context(), s.api, projectID, args[0])
			if err != nil {
				return err
			}
			return a.printer().BuildShow(output.NewBuildView(art))
		},
	}, a.buildUploadCommand(), a.buildArchiveCommand(true), a.buildArchiveCommand(false))
	return cmd
}

func (a *App) buildUploadCommand() *cobra.Command {
	var f buildFlags
	cmd := &cobra.Command{
		Use:   "upload <bin> --version-label <label>",
		Short: "Sign and upload a build without deploying it",
		Long: `Upload an application binary to the current project as a new build,
without deploying it. Deploy it later with simpleota deployment create.

` + uploadHelp,
		Example: `  simpleota build upload build/app.bin --version-label 1.4.0
  simpleota deployment create --build 42 --percent 10`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job, err := a.prepareBuild(cmd.Context(), args[0], f)
			if err != nil {
				return err
			}
			res, err := job.svc.Upload(cmd.Context(), job.opts)
			if err != nil {
				return buildErr(err)
			}
			p := a.printer()
			noticeUnsigned(p, res)
			return p.Upload(output.NewUploadView(res))
		},
	}
	f.register(cmd.Flags())
	return cmd
}

func (a *App) buildArchiveCommand(archive bool) *cobra.Command {
	use, short, long := "archive", "Take a build out of service", `Archive a build: devices are no longer offered it, and it cannot be
deployed. The record of which devices ran it is kept, and builds are
never deleted. The server refuses while a draft, active or paused
deployment still offers the build; complete or cancel it first.`
	if !archive {
		use, short, long = "unarchive", "Return an archived build to service", `Unarchive a build, so it can be offered and deployed again.`
	}
	return &cobra.Command{
		Use:     use + " <build-number>",
		Short:   short,
		Long:    long,
		Example: "  simpleota build " + use + " 41",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			art, err := findBuild(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			art, err = s.api.UpdateArtifact(ctx, projectID, art.ID, archive)
			if err != nil {
				return err
			}
			return a.printer().BuildShow(output.NewBuildView(art))
		},
	}
}

// findBuild looks a build up by its number.
func findBuild(ctx context.Context, client api.API, projectID, ref string) (api.Artifact, error) {
	number, err := strconv.Atoi(ref)
	if err != nil || number < 1 {
		return api.Artifact{}, usageErr("%q is not a build number; simpleota build list prints them", ref)
	}
	artifacts, err := client.ListArtifacts(ctx, projectID)
	if err != nil {
		return api.Artifact{}, err
	}
	for _, art := range artifacts {
		if art.BuildNumber == number {
			return art, nil
		}
	}
	return api.Artifact{}, usageErr("this project has no build %d; simpleota build list prints them", number)
}
