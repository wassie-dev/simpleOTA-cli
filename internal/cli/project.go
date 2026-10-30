package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

const projectEnv = "SIMPLEOTA_PROJECT"

// Where the current project came from, as status reports it.
const (
	projectFromFlag   = "--project"
	projectFromEnv    = projectEnv
	projectFromToken  = "the project token"
	projectFromConfig = "project use"
	projectFromOnly   = "the only project"
)

// noProjectError means no project was chosen and the token sees zero or
// several.
type noProjectError struct{ projects []api.Project }

func (e *noProjectError) Error() string {
	if len(e.projects) == 0 {
		return "this account has no projects yet; create one in the dashboard, or pass --project <project-id> for a project you can open"
	}
	return fmt.Sprintf("no project selected, and this token can see %d (%s); choose one with simpleota project use <slug>, or pass --project <slug>", len(e.projects), projectSlugs(e.projects))
}

// currentProject resolves the project a command acts on: --project, then
// SIMPLEOTA_PROJECT, then the project chosen with project use for this API
// URL, then the only project the token can see. A project token always
// sees just its own project, so project use does not apply to it. A
// project from project use carries only its ID and slug.
func (a *App) currentProject(ctx context.Context, client api.API, kind credentials.Kind, cfg config.Config, apiURL string) (api.Project, string, error) {
	if ref := a.flags.project; ref != "" {
		p, err := findProject(ctx, client, ref)
		return p, projectFromFlag, err
	}
	if ref := strings.TrimSpace(a.Getenv(projectEnv)); ref != "" {
		p, err := findProject(ctx, client, ref)
		if err != nil {
			err = fmt.Errorf("%s: %w", projectEnv, err)
		}
		return p, projectFromEnv, err
	}
	if kind != credentials.KindProject && cfg.ProjectID != "" && cfg.APIURL == apiURL {
		return api.Project{ID: cfg.ProjectID, Slug: cfg.ProjectSlug}, projectFromConfig, nil
	}
	projects, err := client.ListProjects(ctx)
	if isStaffListing(err) {
		return api.Project{}, "", usageErr("this server does not list a staff account's projects; choose one with simpleota project use <project-id>, or pass --project <project-id>")
	}
	if err != nil {
		return api.Project{}, "", err
	}
	if len(projects) == 1 {
		if kind == credentials.KindProject {
			return projects[0], projectFromToken, nil
		}
		return projects[0], projectFromOnly, nil
	}
	code := ExitUsage
	if len(projects) == 0 {
		code = ExitError
	}
	return api.Project{}, "", &exitError{code: code, err: &noProjectError{projects: projects}}
}

// findProject accepts a project UUID or slug.
func findProject(ctx context.Context, a api.API, ref string) (api.Project, error) {
	if api.IsUUID(ref) {
		p, err := a.GetProject(ctx, ref)
		var ae *api.Error
		if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
			return p, usageErr("project %s does not exist or this token cannot see it", ref)
		}
		return p, err
	}
	projects, err := a.ListProjects(ctx)
	if isStaffListing(err) {
		return api.Project{}, usageErr("this server does not list a staff account's projects, so pass the project ID instead of a slug, copying it from the project's page")
	}
	if err != nil {
		return api.Project{}, err
	}
	for _, p := range projects {
		if p.Slug == ref {
			return p, nil
		}
	}
	return api.Project{}, usageErr("no project with slug %q is visible to this token (visible: %s)", ref, projectSlugs(projects))
}

func projectSlugs(ps []api.Project) string {
	if len(ps) == 0 {
		return "none"
	}
	slugs := make([]string, len(ps))
	for i, p := range ps {
		slugs[i] = p.Slug
	}
	return strings.Join(slugs, ", ")
}

// isStaffListing reports the 400 a server without /me/ gives a staff token
// that lists projects without naming an owner.
func isStaffListing(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest
}

func (a *App) projectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"projects"},
		Short:   "List projects and choose the one commands act on",
		Long: `A project holds a fleet: its builds, deployments, devices, release
channels and signing keys. Every resource command acts on one project, the
current project, chosen in this order:

  1. --project <slug|id>
  2. SIMPLEOTA_PROJECT
  3. the project chosen with simpleota project use (personal tokens only)
  4. the only project the token can see; a project token sees just its own

Projects are created and deleted in the dashboard.`,
		Example: `  simpleota project list
  simpleota project use my-fleet
  simpleota project show`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(a.projectListCommand(), a.projectShowCommand(), a.projectUseCommand())
	return cmd
}

func (a *App) projectListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the projects this token can see",
		Long: `List the projects this token can see, marking the current project with *.
A staff account sees its own projects; open any other project by ID with
--project <id>.`,
		Example: `  simpleota project list
  simpleota project list --json | jq -r '.[].slug'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.openSession()
			if err != nil {
				return err
			}
			projects, err := s.api.ListProjects(ctx)
			if isStaffListing(err) {
				return usageErr("this server does not list a staff account's projects; open one by ID with simpleota project show <project-id>")
			}
			if err != nil {
				return err
			}
			current, _, _ := a.currentProject(ctx, s.api, s.kind, s.cfg, s.apiURL)
			views := make([]output.ProjectView, len(projects))
			for i, p := range projects {
				views[i] = output.NewProjectView(p)
				views[i].Current = p.ID == current.ID
			}
			return a.printer().ProjectList(views)
		},
	}
}

func (a *App) projectShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [slug|id]",
		Short: "Show a project (the current one by default)",
		Long: `Show a project's name, slug, ID, default framework and last build number.
Without an argument it shows the current project.`,
		Example: `  simpleota project show
  simpleota project show my-fleet`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.openSession()
			if err != nil {
				return err
			}
			var p api.Project
			if len(args) == 1 {
				p, err = findProject(ctx, s.api, args[0])
			} else {
				p, err = s.project(ctx)
			}
			if err != nil {
				return err
			}
			return a.printer().ProjectShow(output.NewProjectView(p))
		},
	}
}

func (a *App) projectUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use <slug|id>",
		Short: "Choose the project commands act on",
		Long: `Remember a project as the current project for the API URL you are logged
in to. --project and SIMPLEOTA_PROJECT still override it for one command.
logout and login clear it.

A project token always acts on its own project, so use is for personal
tokens.`,
		Example: `  simpleota project use my-fleet
  simpleota project use 5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.openSession()
			if err != nil {
				return err
			}
			if s.kind == credentials.KindProject {
				return usageErr("a project token always acts on its own project; project use is for personal tokens")
			}
			p, err := findProject(ctx, s.api, args[0])
			if err != nil {
				return err
			}
			s.cfg.APIURL, s.cfg.ProjectID, s.cfg.ProjectSlug = s.apiURL, p.ID, p.Slug
			if err := config.Save(s.configDir, s.cfg); err != nil {
				return err
			}
			return a.printer().ProjectUse(output.ProjectUseView{APIURL: s.apiURL, Project: output.NewProjectView(p)})
		},
	}
}
