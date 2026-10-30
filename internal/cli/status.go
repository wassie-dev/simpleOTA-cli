package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/fileperm"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/update"
)

const statusCheckTimeout = 10 * time.Second

func (a *App) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the CLI's version, login, project and signing key",
		Long: `Show this simpleota's version and whether a newer release exists, the API
URL, whether you are logged in and the token still works, the selected
project, and the key release would sign with.

Exit 0 when logged in with a working token, 3 when not logged in or the
token is rejected, 2 when --project or the project saved at login cannot
be found, and 1 when the API cannot be reached. For a deployment,
use simpleota deployment status.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runCLIStatus(cmd.Context())
		},
	}
}

func (a *App) runCLIStatus(ctx context.Context) error {
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
	v := output.StatusView{
		Version:    a.currentVersion(),
		APIURL:     apiURL,
		ConfigDir:  dir,
		SigningKey: a.signingKeyState(cfg),
	}
	a.checkForUpdate(ctx, &v)
	project, projectErr := a.checkAuth(ctx, cfg, dir, &v)
	if project != nil {
		v.Project = &output.ProjectView{ID: project.ID, Slug: project.Slug, Name: project.Name}
	}
	if projectErr != nil {
		v.ProjectError = message(projectErr)
	}

	if err := a.printer().Status(v); err != nil {
		return err
	}
	switch v.Auth.State {
	case output.AuthNotLoggedIn:
		return &exitError{code: ExitAuth, err: errors.New("not logged in"), quiet: true}
	case output.AuthRejected:
		return &exitError{code: ExitAuth, err: errors.New("the token was rejected"), quiet: true}
	case output.AuthUnreachable:
		return &exitError{code: ExitError, err: errors.New("could not reach the API"), quiet: true}
	}
	if projectErr != nil {
		return &exitError{code: exitCode(projectErr), err: projectErr, quiet: true}
	}
	return nil
}

func (a *App) checkForUpdate(ctx context.Context, v *output.StatusView) {
	src, err := a.updateSource()
	if err != nil {
		v.UpdateError = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	latest, err := src.Latest(ctx)
	if err != nil {
		v.UpdateError = err.Error()
		return
	}
	v.LatestVersion = strings.TrimPrefix(latest, "v")
	v.UpdateAvailable = update.Normalize(v.Version) != "" && update.Newer(latest, v.Version)
}

// checkAuth fills v.Auth and returns the project commands would use.
func (a *App) checkAuth(ctx context.Context, cfg config.Config, dir string, v *output.StatusView) (*api.Project, error) {
	token := strings.TrimSpace(a.Getenv("SIMPLEOTA_TOKEN"))
	source := "SIMPLEOTA_TOKEN"
	if token == "" {
		t, where, err := credentials.Lookup(a.store(dir), v.APIURL)
		switch {
		case errors.Is(err, credentials.ErrNotFound):
			v.Auth = output.AuthView{State: output.AuthNotLoggedIn}
			return nil, nil
		case err != nil:
			v.Auth = output.AuthView{State: output.AuthNotLoggedIn, Error: err.Error()}
			return nil, nil
		}
		token, source = t, where
	}
	kind, err := credentials.Classify(token)
	if err != nil {
		v.Auth = output.AuthView{State: output.AuthRejected, Source: source, Error: err.Error()}
		return nil, nil
	}
	v.Auth = output.AuthView{TokenType: string(kind), Source: source}

	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	client := a.newAPI(v.APIURL, token)
	projects, err := client.ListProjects(ctx)
	var ae *api.Error
	switch {
	case err == nil:
		v.Auth.State = output.AuthValid
	case errors.As(err, &ae) && ae.StatusCode == http.StatusUnauthorized:
		v.Auth.State = output.AuthRejected
		v.Auth.Error = "the server rejected the token (401)"
		return nil, nil
	case errors.As(err, &ae):
		// Any other API answer means the token was accepted, such as the
		// 400 staff tokens get without a filter.
		v.Auth.State = output.AuthValid
	default:
		v.Auth.State = output.AuthUnreachable
		v.Auth.Error = err.Error()
	}
	return a.statusProject(ctx, client, cfg, v, projects)
}

// statusProject finds the project commands would use: --project, then the
// project saved at login, then the only project the token can see. A
// project that was named but cannot be found is an error.
func (a *App) statusProject(ctx context.Context, client api.API, cfg config.Config, v *output.StatusView, projects []api.Project) (*api.Project, error) {
	ref := a.flags.project
	saved := ref == "" && cfg.ProjectID != "" && cfg.APIURL == v.APIURL && a.Getenv("SIMPLEOTA_TOKEN") == ""
	switch {
	case saved:
		ref = cfg.ProjectID
	case ref == "":
		if len(projects) == 1 {
			return &projects[0], nil
		}
		return nil, nil
	}
	for _, p := range projects {
		if p.ID == ref || p.Slug == ref {
			return &p, nil
		}
	}
	if v.Auth.State != output.AuthValid {
		if saved {
			return &api.Project{ID: cfg.ProjectID, Slug: cfg.ProjectSlug}, nil
		}
		return nil, nil
	}
	// Staff tokens list no projects, and a project token lists only its
	// own, so look the project up directly.
	p, err := findProject(ctx, client, ref)
	if err != nil && saved {
		return nil, fmt.Errorf("the project saved at login (%s): %w; run simpleota login to choose another", cfg.ProjectSlug, err)
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// signingKeyState reports the key release would use, in the same order.
func (a *App) signingKeyState(cfg config.Config) output.SigningKeyState {
	if a.Getenv(signingKeyEnv) != "" {
		return output.SigningKeyState{Source: "env"}
	}
	if cfg.SigningKeyFile == "" {
		return output.SigningKeyState{Source: "none"}
	}
	s := output.SigningKeyState{Source: "file", Path: cfg.SigningKeyFile}
	if err := fileperm.CheckPrivate(cfg.SigningKeyFile); err != nil {
		s.Error = err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			s.Error = "missing"
		}
	}
	return s
}
