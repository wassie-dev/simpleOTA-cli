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
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
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
		return "this account has no projects yet\nCreate one in the dashboard, or pass --project <project-id> for a project\nyou can open."
	}
	return fmt.Sprintf("no project selected, and this token can see %d (%s)\nChoose one with:\n  simpleota project use <slug>\nOr pass --project <slug>.", len(e.projects), projectSlugs(e.projects))
}

// currentProject resolves the project a command acts on: --project, then
// SIMPLEOTA_PROJECT, then the project chosen with project use for this API
// URL, then the only project the token can see. A project token always
// sees just its own project, so project use does not apply to it. A
// project from project use carries only its ID and slug; fullProject
// fetches the rest.
func (s *session) currentProject(ctx context.Context) (api.Project, string, error) {
	if ref := s.app.flags.project; ref != "" {
		p, err := findProject(ctx, s.api, ref)
		return p, projectFromFlag, err
	}
	if ref := strings.TrimSpace(s.app.Getenv(projectEnv)); ref != "" {
		p, err := findProject(ctx, s.api, ref)
		var ee *exitError
		if errors.As(err, &ee) && ee.code == ExitUsage {
			err = usageErr("%s: %w", projectEnv, err)
		}
		return p, projectFromEnv, err
	}
	if s.kind != credentials.KindProject && s.cfg.ProjectID != "" && s.cfg.APIURL == s.apiURL {
		return api.Project{ID: s.cfg.ProjectID, Slug: s.cfg.ProjectSlug}, projectFromConfig, nil
	}
	projects, err := s.api.ListProjects(ctx)
	if isStaffListing(err) {
		return api.Project{}, "", usageErr("this server does not list a staff account's projects\nChoose one by ID with:\n  simpleota project use <project-id>\nOr pass --project <project-id>.")
	}
	if err != nil {
		return api.Project{}, "", err
	}
	if len(projects) == 1 {
		if s.kind == credentials.KindProject {
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

// fullProject resolves the current project and fetches it in full, with
// the source currentProject reports.
func (s *session) fullProject(ctx context.Context) (api.Project, string, error) {
	p, source, err := s.currentProject(ctx)
	if err != nil || source != projectFromConfig {
		return p, source, err
	}
	full, err := s.api.GetProject(ctx, p.ID)
	var ae *api.Error
	if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
		return api.Project{}, source, usageErr("the chosen project (%s) does not exist, or this token cannot see it\nChoose another with:\n  simpleota project use <slug>", termtext.Clean(p.Slug))
	}
	return full, source, err
}

// findProject accepts a project UUID or slug.
func findProject(ctx context.Context, a api.API, ref string) (api.Project, error) {
	if api.IsUUID(ref) {
		p, err := a.GetProject(ctx, ref)
		var ae *api.Error
		if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
			return p, usageErr("project %s does not exist, or this token cannot see it", ref)
		}
		return p, err
	}
	projects, err := a.ListProjects(ctx)
	if isStaffListing(err) {
		return api.Project{}, usageErr("this server does not list a staff account's projects\nUse the project ID from the project's page, not a slug.")
	}
	if err != nil {
		return api.Project{}, err
	}
	for _, p := range projects {
		if p.Slug == ref {
			return p, nil
		}
	}
	return api.Project{}, usageErr("no project with slug %q is visible to this token\nVisible projects: %s", ref, projectSlugs(projects))
}

func projectSlugs(ps []api.Project) string {
	if len(ps) == 0 {
		return "none"
	}
	slugs := make([]string, len(ps))
	for i, p := range ps {
		slugs[i] = termtext.Clean(p.Slug)
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
		Short:   "List projects and choose the current one",
		Long: `A project holds a fleet: its builds, deployments, devices, release channels,
device groups, signing keys and audit log. Projects are created and deleted
in the dashboard.

Most commands act on the current project, the first of these that applies:

  1. --project <slug|id>
  2. SIMPLEOTA_PROJECT
  3. the project chosen with 'simpleota project use' (personal tokens only)
  4. the only project the token can see; a project token sees just its own`,
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
	return withLimit(&cobra.Command{
		Use:   "list",
		Short: "List the projects this token can see",
		Long: `List the projects this token can see. A * marks the current project.

A staff account sees its own projects. To open any other project, use its
ID with --project <id>.`,
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
				return usageErr("this server does not list a staff account's projects\nOpen one by ID with:\n  simpleota project show <project-id>")
			}
			if err != nil {
				return err
			}
			current, _, _ := s.currentProject(ctx)
			views := make([]output.ProjectView, len(projects))
			for i, p := range projects {
				views[i] = output.NewProjectView(p)
				views[i].Current = p.ID == current.ID
			}
			return printList(a, cmd, views, a.printer().ProjectList)
		},
	})
}

func (a *App) projectShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [slug|id]",
		Short: "Show a project",
		Long: `Show a project's name, slug, ID, default framework and last build number.
Without an argument, show the current project.`,
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
				p, _, err = s.fullProject(ctx)
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
		Short: "Choose the current project",
		Long: `Set the current project for the API URL you are logged in to. --project and
SIMPLEOTA_PROJECT still override it, and logging in or out clears it.

A project token always acts on its own project, so this is for personal
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
				return usageErr("a project token always acts on its own project\n'simpleota project use' is for personal tokens.")
			}
			p, err := findProject(ctx, s.api, args[0])
			if err != nil {
				return err
			}
			if _, err := config.Update(s.configDir, func(c *config.Config) bool {
				c.APIURL, c.ProjectID, c.ProjectSlug = s.apiURL, p.ID, p.Slug
				return true
			}); err != nil {
				return err
			}
			return a.printer().ProjectUse(output.ProjectUseView{APIURL: s.apiURL, Project: output.NewProjectView(p)})
		},
	}
}
