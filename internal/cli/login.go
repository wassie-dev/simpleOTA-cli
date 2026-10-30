package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) loginCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Store an API token from the dashboard",
		Long: `Store a personal token (soto_pat_...) or an api-scoped project token
(soto_proj_...) made in the SimpleOTA dashboard. Paste it at the prompt or
pipe it on stdin. The token is checked against the API, kept in the OS
keychain (or a 0600 file when there is none), and its project is remembered.

The project is picked automatically when the token can see only one;
otherwise pass --project. A staff account selects its project by ID: paste
the Project ID from the project's page when asked, or pass --project <id>.

SIMPLEOTA_TOKEN, when set, takes precedence over the stored token.`,
		Example: `  simpleota login
  echo "$SIMPLEOTA_TOKEN" | simpleota login --project my-fleet`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runLogin(cmd.Context())
		},
	}
}

func (a *App) runLogin(ctx context.Context) error {
	dir, err := a.configDir()
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	apiURL, err := a.resolveAPIURL(cfg)
	if err != nil {
		return err
	}
	token, err := a.readToken(apiURL)
	if err != nil {
		return err
	}
	kind, err := credentials.Classify(token)
	if err != nil {
		return authErr("%v", err)
	}

	client := a.newAPI(apiURL, token)
	project, err := a.loginProject(ctx, client, kind, apiURL)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.StatusCode == http.StatusUnauthorized {
			return authErr("%s rejected the token; check it was copied whole and has not been revoked (project tokens need the api scope)", apiURL)
		}
		return err
	}

	store := a.store(dir)
	where := store.Name()
	if fb, ok := store.(credentials.Fallback); ok {
		where, err = fb.SetWhere(apiURL, token)
	} else {
		err = store.Set(apiURL, token)
	}
	if err != nil {
		return fmt.Errorf("storing the token: %w", err)
	}
	cfg.APIURL = apiURL
	cfg.ProjectID = project.ID
	cfg.ProjectSlug = project.Slug
	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	p := a.printer()
	if a.Getenv("SIMPLEOTA_TOKEN") != "" {
		p.Notice("Note: SIMPLEOTA_TOKEN is set and takes precedence over the stored token.")
	}
	view := output.LoginView{
		APIURL:          apiURL,
		TokenType:       string(kind),
		CredentialStore: where,
		Project:         output.ProjectView{ID: project.ID, Slug: project.Slug, Name: project.Name},
	}
	if p.JSON {
		return p.WriteJSON(view)
	}
	fmt.Fprintf(a.Out, "Logged in to %s with a %s token for project %s.\n", apiURL, kind, project.Slug)
	fmt.Fprintf(a.Out, "Token stored in %s.\n", where)
	return nil
}

// readToken prompts on a terminal, otherwise reads the first line of stdin.
func (a *App) readToken(apiURL string) (string, error) {
	var raw string
	if a.StdinIsTerminal {
		if a.Prompt == nil {
			return "", usageErr("cannot prompt for the token; pipe it on stdin")
		}
		a.printer().Notice(`Log in with a token from the SimpleOTA dashboard:
  - a personal token (soto_pat_...) for your own machine; make one under
    API tokens at %s/tokens/
  - a project token (soto_proj_...) with the API scope for CI; make one with
    Issue token on the project's page
The token is not shown as you paste it.`, apiURL)
		t, err := a.Prompt("Paste your SimpleOTA token: ")
		if err != nil {
			return "", err
		}
		raw = t
	} else {
		line, err := bufio.NewReader(io.LimitReader(a.In, 4096)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading the token from stdin: %w", err)
		}
		raw = line
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", usageErr("no token given; paste it at the prompt or pipe it on stdin")
	}
	return token, nil
}

// loginProject validates the token and picks its project.
func (a *App) loginProject(ctx context.Context, client api.API, kind credentials.Kind, apiURL string) (api.Project, error) {
	if ref := a.flags.project; ref != "" {
		return findProject(ctx, client, ref)
	}
	projects, err := client.ListProjects(ctx)
	if isStaffListing(err) {
		return a.staffProject(ctx, client, apiURL)
	}
	if err != nil {
		return api.Project{}, err
	}
	switch {
	case len(projects) == 1:
		return projects[0], nil
	case len(projects) == 0 && kind == credentials.KindPersonal:
		return api.Project{}, errors.New("this account has no projects yet; create one in the dashboard, then log in again")
	case len(projects) == 0:
		return api.Project{}, errors.New("this token cannot see any project")
	default:
		return api.Project{}, usageErr("this token can see %d projects; pick one with --project <slug> (%s)", len(projects), projectSlugs(projects))
	}
}

// staffProject asks a staff account for its project ID, because the API
// lists projects for staff only when given an owner.
func (a *App) staffProject(ctx context.Context, client api.API, apiURL string) (api.Project, error) {
	if !a.StdinIsTerminal || a.PromptLine == nil {
		return api.Project{}, usageErr("this token belongs to a staff account, which the API does not list projects for; pass --project <project-id>, copying the Project ID from the project's page at %s", apiURL)
	}
	a.printer().Notice("This token belongs to a staff account, which the API does not list projects for.\nOpen the project at %s and copy the Project ID shown under its name.", apiURL)
	ref, err := a.PromptLine("Project ID: ")
	if err != nil {
		return api.Project{}, err
	}
	ref = strings.TrimSpace(ref)
	if !api.IsUUID(ref) {
		return api.Project{}, usageErr("%q is not a project ID; copy the Project ID from the project's page, or pass --project <project-id>", ref)
	}
	return findProject(ctx, client, ref)
}

func (a *App) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored token and project",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := a.configDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			apiURL, err := a.resolveAPIURL(cfg)
			if err != nil {
				return err
			}
			if err := a.store(dir).Delete(apiURL); err != nil {
				return fmt.Errorf("removing the stored token: %w", err)
			}
			if cfg.APIURL == apiURL {
				cfg.APIURL, cfg.ProjectID, cfg.ProjectSlug = "", "", ""
				if err := config.Save(dir, cfg); err != nil {
					return err
				}
			}
			p := a.printer()
			if p.JSON {
				return p.WriteJSON(output.LogoutView{APIURL: apiURL, LoggedOut: true})
			}
			fmt.Fprintf(a.Out, "Logged out of %s.\n", apiURL)
			if a.Getenv("SIMPLEOTA_TOKEN") != "" {
				p.Notice("Note: SIMPLEOTA_TOKEN is still set in this environment.")
			}
			return nil
		},
	}
}
