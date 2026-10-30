package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func TestLoginPersonalTokenAutoSelectsOnlyProject(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.PersonalToken + "\n"
	r := h.run("login", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var v struct {
		TokenType string `json:"token_type"`
		Project   struct{ Slug string }
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.TokenType != "personal" || v.Project.Slug != "fleet" {
		t.Fatalf("out=%s err=%v", r.stdout, err)
	}
	if strings.Contains(r.stdout+r.stderr, fakeapi.PersonalToken) {
		t.Fatal("token was printed")
	}
	cfg, _ := config.Load(h.configDir)
	if cfg.ProjectID != fakeapi.ProjectID || cfg.APIURL != h.fake.URL {
		t.Fatalf("config = %+v", cfg)
	}
	raw, _ := os.ReadFile(filepath.Join(h.configDir, "config.json"))
	if strings.Contains(string(raw), "soto_") {
		t.Fatal("token must not be written to config.json")
	}
}

func TestLoginFromPrompt(t *testing.T) {
	h := newHarness(t)
	h.prompts = []string{fakeapi.ProjectToken}
	r := h.run("login")
	if r.code != 0 || !strings.Contains(r.stdout, "project token for project fleet") {
		t.Fatalf("%+v", r)
	}
}

func TestLoginPromptExplainsTokens(t *testing.T) {
	h := newHarness(t)
	h.prompts = []string{fakeapi.PersonalToken}
	r := h.run("login")
	if r.code != 0 || !strings.Contains(r.stderr, h.fake.URL+"/tokens/") || !strings.Contains(r.stderr, "Issue token") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, "/tokens/") {
		t.Fatal("instructions belong on stderr")
	}
}

func TestLoginStaffTokenAsksForProjectID(t *testing.T) {
	h := newHarness(t)
	h.prompts = []string{fakeapi.StaffToken, " " + fakeapi.ProjectID + "\n"}
	r := h.run("login")
	if r.code != 0 || !strings.Contains(r.stderr, "staff account") || !strings.Contains(r.stdout, "personal token for project fleet") {
		t.Fatalf("%+v", r)
	}
	if cfg, _ := config.Load(h.configDir); cfg.ProjectID != fakeapi.ProjectID {
		t.Fatalf("config = %+v", cfg)
	}

	h.prompts = []string{fakeapi.StaffToken, "fleet"}
	if r := h.run("login"); r.code != ExitUsage || !strings.Contains(r.stderr, "not a project ID") {
		t.Fatalf("slug at the prompt: %+v", r)
	}
}

func TestLoginStaffTokenWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.StaffToken
	if r := h.run("login"); r.code != ExitUsage || !strings.Contains(r.stderr, "--project <project-id>") {
		t.Fatalf("no project: %+v", r)
	}
	h.stdin = fakeapi.StaffToken
	if r := h.run("login", "--project", "fleet"); r.code != ExitUsage || !strings.Contains(r.stderr, "by ID, not slug") {
		t.Fatalf("slug: %+v", r)
	}
	h.stdin = fakeapi.StaffToken
	if r := h.run("login", "--project", fakeapi.ProjectID); r.code != 0 {
		t.Fatalf("project ID: %+v", r)
	}
}

func TestLoginNeedsProjectWhenSeveralVisible(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, api.Project{ID: fakeapi.OtherID, Slug: "lab"})
	h.stdin = fakeapi.PersonalToken
	if r := h.run("login"); r.code != ExitUsage || !strings.Contains(r.stderr, "--project") {
		t.Fatalf("%+v", r)
	}
	h.stdin = fakeapi.PersonalToken
	if r := h.run("login", "--project", "fleet"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestLoginRejectsBadTokens(t *testing.T) {
	h := newHarness(t)
	h.stdin = "not-a-token"
	if r := h.run("login"); r.code != ExitAuth {
		t.Fatalf("malformed: %+v", r)
	}
	h.stdin = "soto_pat_revokedrevokedrevoked"
	r := h.run("login")
	if r.code != ExitAuth || !strings.Contains(r.stderr, "rejected the token") {
		t.Fatalf("revoked: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "credentials.json")); !os.IsNotExist(err) {
		t.Fatal("a rejected token must not be stored")
	}
}

func TestLoginRefusesPlainHTTPForRemoteHost(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.PersonalToken
	if r := h.run("login", "--api-url", "http://simpleota.com"); r.code != ExitUsage {
		t.Fatalf("%+v", r)
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	if r := h.run("logout"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "credentials.json")); !os.IsNotExist(err) {
		t.Fatal("token should be removed")
	}
	cfg, _ := config.Load(h.configDir)
	if cfg.ProjectID != "" || cfg.APIURL != "" {
		t.Fatalf("config not cleared: %+v", cfg)
	}
	r := h.run("logout")
	if r.code != 0 || !strings.Contains(r.stdout, "Not logged in to") || strings.Contains(r.stdout, "Logged out") {
		t.Fatalf("second logout: %+v", r)
	}
	r = h.run("logout", "--json")
	if r.code != 0 || !strings.Contains(r.stdout, `"logged_out": false`) {
		t.Fatalf("second logout --json: %+v", r)
	}
}

func TestLoginUnknownProjectID(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.PersonalToken
	r := h.run("login", "--project", fakeapi.OtherID)
	if r.code != ExitUsage || !strings.Contains(r.stderr, "does not exist or this token cannot see it") {
		t.Fatalf("%+v", r)
	}
}
