package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
		return nil, authErr("the token from %s is invalid: %v", source, err)
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
		return nil, authErr("not logged in to %s\nLog in with:\n  simpleota login\nOr set %s.", st.apiURL, tokenEnv)
	case err != nil:
		return nil, authErr("not logged in to %s (%v)\nLog in with:\n  simpleota login\nOr set %s.", st.apiURL, err, tokenEnv)
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
	a.opened = &openedProject{session: s, projectID: p.ID}
	return s, p.ID, nil
}

// openedProject is the project a command resolved, for explaining its
// errors afterwards.
type openedProject struct {
	session   *session
	projectID string
}

// explainReadOnly explains a change refused because the token can read the
// current project but not change it, as for a staff account on another
// owner's project. The server answers such a change with the 404 it gives
// for a project it cannot find, so this reads the project to tell the two
// apart. The API has no field that says the project was what it could not
// find; its "No Project matches" detail is the only sign, so a 404 without
// that detail, such as one for a missing deployment, is left as it is.
func (a *App) explainReadOnly(ctx context.Context, err error) error {
	var ae *api.Error
	if a.opened == nil || !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound ||
		ae.Method == http.MethodGet || !strings.Contains(ae.Detail, "No Project matches") {
		return err
	}
	if _, getErr := a.opened.session.api.GetProject(ctx, a.opened.projectID); getErr != nil {
		return err
	}
	return &exitError{code: ExitAuth, err: &explainedErr{
		msg:   "this token can read the project but not change it\nA staff account can open any project, but only its owner can change it.",
		cause: err,
	}}
}

// requirePersonal refuses project tokens for commands that change who can
// sign firmware.
func (s *session) requirePersonal(what string) error {
	if s.kind != credentials.KindPersonal {
		return authErr("'simpleota %s' needs a personal token (soto_pat_...)\nThis stops a leaked CI token from changing signing keys. Log in with one:\n  simpleota login", what)
	}
	return nil
}
