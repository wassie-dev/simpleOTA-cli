package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakerelease"
)

type statusJSON struct {
	Version         string `json:"version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateError     string `json:"update_error"`
	APIURL          string `json:"api_url"`
	Auth            struct {
		State     string `json:"state"`
		TokenType string `json:"token_type"`
		Source    string `json:"source"`
	} `json:"auth"`
	Project *struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"project"`
	SigningKey struct {
		Source string `json:"source"`
		Path   string `json:"path"`
		Error  string `json:"error"`
	} `json:"signing_key"`
}

func cliStatus(t *testing.T, h *harness) (result, statusJSON) {
	t.Helper()
	r := h.run("status", "--json")
	var v statusJSON
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("status --json printed %q: %v", r.stdout, err)
	}
	return r, v
}

func TestStatusNotLoggedIn(t *testing.T) {
	h := newHarness(t)
	r, v := cliStatus(t, h)
	if r.code != ExitAuth || v.Auth.State != "not_logged_in" || v.Project != nil || r.stderr != "" {
		t.Fatalf("%+v %+v", r, v)
	}
	if v.Version != "0.1.0" || v.LatestVersion != "0.2.0" || !v.UpdateAvailable {
		t.Fatalf("update fields: %+v", v)
	}
	r = h.run("status")
	if r.code != ExitAuth || !strings.Contains(r.stdout, "logged in    no") || r.stderr != "" {
		t.Fatalf("human output: %+v", r)
	}
}

func TestStatusLoggedIn(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	r, v := cliStatus(t, h)
	if r.code != 0 || v.Auth.State != "valid" || v.Auth.TokenType != "personal" || !strings.HasSuffix(v.Auth.Source, "credentials.json") {
		t.Fatalf("%+v %+v", r, v)
	}
	if v.Project == nil || v.Project.Slug != "fleet" || v.APIURL != h.fake.URL {
		t.Fatalf("project: %+v", v)
	}
	r = h.run("status")
	for _, want := range []string{"simpleota 0.1.0 (0.2.0 is available; run simpleota update)", "logged in    yes, personal token", "project      fleet"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("human output missing %q:\n%s", want, r.stdout)
		}
	}
}

func TestStatusEnvToken(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	r, v := cliStatus(t, h)
	if r.code != 0 || v.Auth.Source != "SIMPLEOTA_TOKEN" || v.Auth.TokenType != "project" || v.Project == nil || v.Project.Slug != "fleet" {
		t.Fatalf("%+v %+v", r, v)
	}
}

func TestStatusRejectedToken(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = "soto_proj_revokedrevokedrevoked"
	r, v := cliStatus(t, h)
	if r.code != ExitAuth || v.Auth.State != "rejected" {
		t.Fatalf("%+v %+v", r, v)
	}
}

func TestStatusUnreachableAPI(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	h.env["SIMPLEOTA_API_URL"] = "http://127.0.0.1:1"
	r, v := cliStatus(t, h)
	if r.code != ExitError || v.Auth.State != "unreachable" {
		t.Fatalf("%+v %+v", r, v)
	}
}

func TestStatusUpToDateAndUpdateCheckFailure(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.release.Set(func(s *fakerelease.Server) { s.Version = "0.1.0" })
	_, v := cliStatus(t, h)
	if v.UpdateAvailable || v.LatestVersion != "0.1.0" {
		t.Fatalf("%+v", v)
	}
	h.release.Set(func(s *fakerelease.Server) { s.NoRelease = true })
	r, v := cliStatus(t, h)
	if r.code != 0 || v.UpdateError == "" {
		t.Fatalf("a failed update check must not fail status: %+v %+v", r, v)
	}
}

func TestStatusSigningKey(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	if _, v := cliStatus(t, h); v.SigningKey.Source != "none" {
		t.Fatalf("%+v", v.SigningKey)
	}
	key := filepath.Join(t.TempDir(), "k.key")
	cfg, _ := config.Load(h.configDir)
	cfg.SigningKeyFile = key
	if err := config.Save(h.configDir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, v := cliStatus(t, h); v.SigningKey.Source != "file" || v.SigningKey.Error != "missing" {
		t.Fatalf("%+v", v.SigningKey)
	}
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, v := cliStatus(t, h); v.SigningKey.Path != key || v.SigningKey.Error != "" {
		t.Fatalf("%+v", v.SigningKey)
	}
	h.env[signingKeyEnv] = "PEM"
	if _, v := cliStatus(t, h); v.SigningKey.Source != "env" {
		t.Fatalf("%+v", v.SigningKey)
	}
}
