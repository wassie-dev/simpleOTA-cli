package cli

import (
	"context"
	"errors"
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
URL, whether you are logged in and the token still works (with the account
it belongs to), the current project and where that choice came from, and
the key release would sign with.

Exit 0 when logged in with a working token, 3 when not logged in or the
token is rejected, 2 when --project, SIMPLEOTA_PROJECT or the project
chosen with project use cannot be found, and 1 when the API cannot be
reached. For a deployment, use simpleota deployment show.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runCLIStatus(cmd.Context())
		},
	}
}

func (a *App) runCLIStatus(ctx context.Context) error {
	st, err := a.loadSettings()
	if err != nil {
		return err
	}
	v := output.StatusView{
		Version:    a.currentVersion(),
		APIURL:     st.apiURL,
		ConfigDir:  st.configDir,
		SigningKey: a.signingKeyState(st.cfg),
	}
	a.checkForUpdate(ctx, &v)
	projectErr := a.checkLogin(ctx, st, &v)
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

// checkLogin fills v.Auth and the current project. It returns an error
// only when the project that was asked for cannot be found.
func (a *App) checkLogin(ctx context.Context, st settings, v *output.StatusView) error {
	token, source, err := a.findToken(st)
	switch {
	case errors.Is(err, errNoToken):
		v.Auth = output.AuthView{State: output.AuthNotLoggedIn}
		return nil
	case err != nil:
		v.Auth = output.AuthView{State: output.AuthNotLoggedIn, Error: err.Error()}
		return nil
	}
	s, err := a.newSession(st, token, source)
	if err != nil {
		v.Auth = output.AuthView{State: output.AuthRejected, Source: source, Error: err.Error()}
		return nil
	}
	v.Auth = output.AuthView{TokenType: string(s.kind), Source: source}

	ctx, cancel := context.WithTimeout(ctx, statusCheckTimeout)
	defer cancel()
	me, err := checkToken(ctx, s.api)
	var ae *api.Error
	switch {
	case err == nil:
		v.Auth.State = output.AuthValid
		if me != nil {
			v.Auth.Account = output.NewAccountView(me.Account)
		}
	case errors.As(err, &ae) && ae.StatusCode == http.StatusUnauthorized:
		v.Auth.State = output.AuthRejected
		v.Auth.Error = "the server rejected the token (401)"
		return nil
	case errors.As(err, &ae):
		// Any other API answer means the token was accepted.
		v.Auth.State = output.AuthValid
	default:
		v.Auth.State = output.AuthUnreachable
		v.Auth.Error = err.Error()
		if s.kind != credentials.KindProject && st.cfg.ProjectID != "" && st.cfg.APIURL == st.apiURL {
			v.Project = &output.ProjectView{ID: st.cfg.ProjectID, Slug: st.cfg.ProjectSlug}
			v.ProjectSource = projectFromConfig
		}
		return nil
	}

	p, source, err := s.fullProject(ctx)
	var none *noProjectError
	switch {
	case errors.As(err, &none):
		return nil
	case err != nil:
		return err
	}
	pv := output.NewProjectView(p)
	v.Project, v.ProjectSource = &pv, source
	return nil
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
