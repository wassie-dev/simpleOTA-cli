package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
)

const tokenEnv = "SIMPLEOTA_TOKEN"

// settings are what every command resolves before it needs a token: the
// config directory, its config, and the API URL.
type settings struct {
	configDir string
	cfg       config.Config
	apiURL    string
}

func (a *App) loadSettings() (settings, error) {
	dir, err := a.configDir()
	if err != nil {
		return settings{}, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return settings{}, err
	}
	apiURL, err := a.resolveAPIURL(cfg)
	if err != nil {
		return settings{}, err
	}
	return settings{configDir: dir, cfg: cfg, apiURL: apiURL}, nil
}

// errNoToken means neither SIMPLEOTA_TOKEN nor a stored token exists.
var errNoToken = errors.New("no token")

// findToken returns the token for the API URL and where it came from:
// SIMPLEOTA_TOKEN, then the stored token. It returns errNoToken when there
// is none, and the store's error when the stored token cannot be read.
func (a *App) findToken(st settings) (token, source string, err error) {
	if t := strings.TrimSpace(a.Getenv(tokenEnv)); t != "" {
		return t, tokenEnv, nil
	}
	token, source, err = credentials.Lookup(a.store(st.configDir), st.apiURL)
	if errors.Is(err, credentials.ErrNotFound) {
		return "", "", errNoToken
	}
	if err != nil {
		return "", "", fmt.Errorf("reading the stored token: %w", err)
	}
	return token, source, nil
}

// session is a command's resolved settings, token and API client.
type session struct {
	settings
	app         *App
	tokenSource string
	kind        credentials.Kind
	api         api.API
}

// newSession classifies the token and builds the client.
func (a *App) newSession(st settings, token, source string) (*session, error) {
	kind, err := credentials.Classify(token)
	if err != nil {
		return nil, authErr("%s is invalid: %v", source, err)
	}
	return &session{settings: st, app: a, tokenSource: source, kind: kind, api: a.newAPI(st.apiURL, token)}, nil
}

// openSession resolves everything a command needs to call the API.
func (a *App) openSession() (*session, error) {
	st, err := a.loadSettings()
	if err != nil {
		return nil, err
	}
	token, source, err := a.findToken(st)
	switch {
	case errors.Is(err, errNoToken):
		return nil, authErr("not logged in to %s; run simpleota login, or set %s", st.apiURL, tokenEnv)
	case err != nil:
		return nil, authErr("not logged in to %s (%v); run simpleota login, or set %s", st.apiURL, err, tokenEnv)
	}
	return a.newSession(st, token, source)
}

// projectSession opens a session and resolves the current project's ID.
func (a *App) projectSession(ctx context.Context) (*session, string, error) {
	s, err := a.openSession()
	if err != nil {
		return nil, "", err
	}
	p, _, err := s.currentProject(ctx)
	if err != nil {
		return nil, "", err
	}
	return s, p.ID, nil
}

// requirePersonal refuses project tokens for commands that change who can
// sign firmware.
func (s *session) requirePersonal(what string) error {
	if s.kind != credentials.KindPersonal {
		return authErr("%s needs a personal token (soto_pat_...), so a leaked CI token cannot change signing keys; log in with a personal token from the dashboard", what)
	}
	return nil
}
