package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
)

// session is the resolved API URL, token and settings for one command.
type session struct {
	app       *App
	configDir string
	cfg       config.Config
	apiURL    string
	token     string
	kind      credentials.Kind
	api       api.API
}

// openSession resolves everything a command needs to call the API.
// SIMPLEOTA_TOKEN takes precedence over the stored token.
func (a *App) openSession() (*session, error) {
	dir, err := a.configDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	apiURL, err := a.resolveAPIURL(cfg)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(a.Getenv("SIMPLEOTA_TOKEN"))
	source := "SIMPLEOTA_TOKEN"
	if token == "" {
		source = "the stored token"
		token, err = a.store(dir).Get(apiURL)
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, authErr("not logged in to %s; run simpleota login, or set SIMPLEOTA_TOKEN", apiURL)
		}
		if err != nil {
			return nil, authErr("not logged in to %s (%v); run simpleota login, or set SIMPLEOTA_TOKEN", apiURL, err)
		}
	}
	kind, err := credentials.Classify(token)
	if err != nil {
		return nil, authErr("%s is invalid: %v", source, err)
	}
	return &session{
		app:       a,
		configDir: dir,
		cfg:       cfg,
		apiURL:    apiURL,
		token:     token,
		kind:      kind,
		api:       a.newAPI(apiURL, token),
	}, nil
}

// projectID resolves the current project; see currentProject.
func (s *session) projectID(ctx context.Context) (string, error) {
	p, _, err := s.app.currentProject(ctx, s.api, s.kind, s.cfg, s.apiURL)
	return p.ID, err
}

// project resolves the current project and fetches it in full.
func (s *session) project(ctx context.Context) (api.Project, error) {
	p, source, err := s.app.currentProject(ctx, s.api, s.kind, s.cfg, s.apiURL)
	if err != nil || source != projectFromConfig {
		return p, err
	}
	full, err := s.api.GetProject(ctx, p.ID)
	return full, savedProjectErr(err, p)
}

// savedProjectErr explains a 404 for the project chosen with project use.
func savedProjectErr(err error, p api.Project) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
		return usageErr("the project chosen with project use (%s) does not exist or this token cannot see it; choose another with simpleota project use <slug>", p.Slug)
	}
	return err
}
