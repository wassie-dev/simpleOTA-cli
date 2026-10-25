package cli

import (
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/version"
)

func (a *App) configDir() (string, error) {
	return config.Dir(a.Getenv)
}

func (a *App) store(configDir string) credentials.Store {
	if a.Store != nil {
		return a.Store
	}
	return credentials.Default(configDir, a.Getenv("SIMPLEOTA_NO_KEYRING") != "")
}

func (a *App) newAPI(baseURL, token string) api.API {
	if a.NewAPI != nil {
		return a.NewAPI(baseURL, token)
	}
	return api.New(baseURL, token, version.UserAgent())
}

// resolveAPIURL applies --api-url, then SIMPLEOTA_API_URL, then the URL saved
// at login, then the hosted default.
func (a *App) resolveAPIURL(cfg config.Config) (string, error) {
	raw := config.DefaultAPIURL
	switch {
	case a.flags.apiURL != "":
		raw = a.flags.apiURL
	case a.Getenv("SIMPLEOTA_API_URL") != "":
		raw = a.Getenv("SIMPLEOTA_API_URL")
	case cfg.APIURL != "":
		raw = cfg.APIURL
	}
	u, err := config.NormalizeAPIURL(raw)
	if err != nil {
		return "", usageErr("%v", err)
	}
	return u, nil
}
