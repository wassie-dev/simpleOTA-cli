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

func TestLoginPersonalToken(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.PersonalToken + "\n"
	r := h.run("login", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var v struct {
		TokenType    string `json:"token_type"`
		Account      struct{ Email string }
		Project      struct{ Slug string }
		ProjectCount int `json:"project_count"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.TokenType != "personal" || v.Account.Email != "me@example.invalid" || v.Project.Slug != "fleet" || v.ProjectCount != 1 {
		t.Fatalf("out=%s err=%v", r.stdout, err)
	}
	if strings.Contains(r.stdout+r.stderr, fakeapi.PersonalToken) {
		t.Fatal("token was printed")
	}
	cfg, _ := config.Load(h.configDir)
	if cfg.ProjectID != "" || cfg.APIURL != h.fake.URL {
		t.Fatalf("login must store the API URL and choose no project: %+v", cfg)
	}
	raw, _ := os.ReadFile(filepath.Join(h.configDir, "config.json"))
	if strings.Contains(string(raw), "soto_") {
		t.Fatal("token must not be written to config.json")
	}
	h.stdin = fakeapi.PersonalToken
	r = h.run("login")
	if r.code != 0 || !strings.Contains(r.stdout, "as me@example.invalid (personal token)") || !strings.Contains(r.stdout, "Commands act on fleet, the only project") {
		t.Fatalf("human output: %+v", r)
	}
}

func TestLoginFromPrompt(t *testing.T) {
	h := newHarness(t)
	h.prompts = []string{fakeapi.ProjectToken}
	r := h.run("login")
	if r.code != 0 || !strings.Contains(r.stdout, "with a project token for fleet") {
		t.Fatalf("%+v", r)
	}
}

func TestLoginClearsChosenProject(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	cfg, _ := config.Load(h.configDir)
	cfg.ProjectID, cfg.ProjectSlug = fakeapi.ProjectID, "fleet"
	if err := config.Save(h.configDir, cfg); err != nil {
		t.Fatal(err)
	}
	h.login(fakeapi.PersonalToken)
	if cfg, _ := config.Load(h.configDir); cfg.ProjectID != "" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoginWithSeveralProjects(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, api.Project{ID: fakeapi.OtherID, Slug: "lab"})
	h.stdin = fakeapi.PersonalToken
	r := h.run("login")
	if r.code != 0 || !strings.Contains(r.stdout, "has 2 projects") || !strings.Contains(r.stdout, "simpleota project use <slug>") {
		t.Fatalf("%+v", r)
	}
}

func TestLoginStaffToken(t *testing.T) {
	h := newHarness(t)
	h.stdin = fakeapi.StaffToken
	r := h.run("login", "--json")
	if r.code != 0 || !strings.Contains(r.stdout, `"is_staff": true`) || !strings.Contains(r.stdout, `"project_count": 0`) {
		t.Fatalf("staff account without projects: %+v", r)
	}
	h.fake.StaffProjects = []api.Project{h.fake.Projects[0]}
	h.stdin = fakeapi.StaffToken
	if r := h.run("login"); r.code != 0 || !strings.Contains(r.stdout, "Commands act on fleet") {
		t.Fatalf("staff account with one project: %+v", r)
	}
	// A server without /me/ answers a staff listing with 400.
	h.fake.NoMe = true
	h.stdin = fakeapi.StaffToken
	if r := h.run("login"); r.code != 0 || !strings.Contains(r.stdout, "simpleota project use <project-id>") {
		t.Fatalf("staff token without /me/: %+v", r)
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

