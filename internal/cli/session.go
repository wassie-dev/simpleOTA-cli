package cli

import (
	"context"
	"errors"
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

// projectID resolves --project, then the project saved at login for this
// API URL, then the single project a project token can see.
func (s *session) projectID(ctx context.Context) (string, error) {
	if ref := s.app.flags.project; ref != "" {
		p, err := findProject(ctx, s.api, ref)
		if err != nil {
			return "", err
		}
		return p.ID, nil
	}
	if s.cfg.ProjectID != "" && s.cfg.APIURL == s.apiURL && s.app.Getenv("SIMPLEOTA_TOKEN") == "" {
		return s.cfg.ProjectID, nil
	}
	projects, err := s.api.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	if len(projects) == 0 {
		return "", errors.New("this token cannot see any projects; create one in the dashboard")
	}
	return "", usageErr("this token can see %d projects; pick one with --project <slug> (%s)", len(projects), projectSlugs(projects))
}
