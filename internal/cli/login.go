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
		Long: `Check an API token against SimpleOTA and store it for later commands, in
the OS keychain or, where there is none, a 0600 file in the config
directory. Paste the token at the prompt, or pipe it on stdin.

Two kinds of token work:

  personal token (soto_pat_...)  for your own machine; it reaches every
                                 project your account can open. Make one
                                 under API tokens in the dashboard.
  project token (soto_proj_...)  for CI; it reaches one project. Make one
                                 with Issue token on the project's page,
                                 with the API scope.

login does not choose a project. A project token always acts on its own
project. With a personal token, commands act on your only project, or on
the one you choose with simpleota project use.

Logging in again replaces the stored token and clears the chosen project.
SIMPLEOTA_TOKEN, when set, takes precedence over the stored token.`,
		Example: `  simpleota login
  echo "$SIMPLEOTA_TOKEN" | simpleota login
  simpleota --api-url http://127.0.0.1:8000 login`,
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
	me, err := checkToken(ctx, client)
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
	cfg.APIURL, cfg.ProjectID, cfg.ProjectSlug = apiURL, "", ""
	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	view := output.LoginView{APIURL: apiURL, TokenType: string(kind), CredentialStore: where, ProjectCount: -1}
	if me != nil {
		view.Account = output.NewAccountView(me.Account)
	}
	projects, err := client.ListProjects(ctx)
	switch {
	case isStaffListing(err):
	case err != nil:
		return fmt.Errorf("the token is stored, but listing its projects failed: %w", err)
	default:
		view.ProjectCount = len(projects)
		if len(projects) == 1 {
			pv := output.NewProjectView(projects[0])
			view.Project = &pv
		}
	}

	p := a.printer()
	if a.Getenv("SIMPLEOTA_TOKEN") != "" {
		p.Notice("Note: SIMPLEOTA_TOKEN is set and takes precedence over the stored token.")
	}
	return p.Login(view)
}

// checkToken asks /me/ who the token belongs to. On a server without /me/
// it checks the token by listing projects instead and returns no Me; the
// 400 a staff token gets there still means the token was accepted.
func checkToken(ctx context.Context, client api.API) (*api.Me, error) {
	me, err := client.Me(ctx)
	if err == nil {
		return &me, nil
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound {
		return nil, err
	}
	if _, err := client.ListProjects(ctx); err != nil && !isStaffListing(err) {
		return nil, err
	}
	return nil, nil
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

func (a *App) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored token and chosen project",
		Long: `Remove the token stored for the current API URL and the project chosen
with project use. SIMPLEOTA_TOKEN, when set, is not affected.`,
		Args: exactArgs(0),
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
			store := a.store(dir)
			_, err = store.Get(apiURL)
			hadToken := err == nil
			if err := store.Delete(apiURL); err != nil {
				return fmt.Errorf("removing the stored token: %w", err)
			}
			hadProject := cfg.APIURL == apiURL
			if hadProject {
				cfg.APIURL, cfg.ProjectID, cfg.ProjectSlug = "", "", ""
				if err := config.Save(dir, cfg); err != nil {
					return err
				}
			}
			loggedOut := hadToken || hadProject
			p := a.printer()
			if p.JSON {
				return p.WriteJSON(output.LogoutView{APIURL: apiURL, LoggedOut: loggedOut})
			}
			if loggedOut {
				fmt.Fprintf(a.Out, "Logged out of %s.\n", apiURL)
			} else {
				fmt.Fprintf(a.Out, "Not logged in to %s; nothing to remove.\n", apiURL)
			}
			if a.Getenv("SIMPLEOTA_TOKEN") != "" {
				p.Notice("Note: SIMPLEOTA_TOKEN is still set in this environment.")
			}
			return nil
		},
	}
}
