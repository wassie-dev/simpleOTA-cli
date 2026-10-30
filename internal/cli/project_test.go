package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

// twoProjects logs in with a personal token that sees fleet and lab.
func twoProjects(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, api.Project{ID: fakeapi.OtherID, Slug: "lab", Name: "Lab"})
	h.login(fakeapi.PersonalToken)
	return h
}

func TestCurrentProjectOrder(t *testing.T) {
	h := twoProjects(t)
	r := h.run("deployment", "list")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "can see 2 (fleet, lab)") || !strings.Contains(r.stderr, "simpleota project use <slug>") {
		t.Fatalf("no project chosen: %+v", r)
	}
	if r := h.run("project", "use", "fleet"); r.code != 0 || !strings.Contains(r.stdout, "now act on fleet (Fleet)") {
		t.Fatalf("project use: %+v", r)
	}
	if cfg, _ := config.Load(h.configDir); cfg.ProjectID != fakeapi.ProjectID || cfg.ProjectSlug != "fleet" {
		t.Fatalf("config = %+v", cfg)
	}
	if r := h.run("deployment", "list"); r.code != 0 {
		t.Fatalf("after project use: %+v", r)
	}
	if _, v := cliStatus(t, h); v.Project == nil || v.Project.Slug != "fleet" || v.ProjectSource != "project use" {
		t.Fatalf("status after project use: %+v", v)
	}

	// SIMPLEOTA_PROJECT beats project use, and --project beats both.
	h.env[projectEnv] = "lab"
	if _, v := cliStatus(t, h); v.Project == nil || v.Project.Slug != "lab" || v.ProjectSource != projectEnv {
		t.Fatalf("SIMPLEOTA_PROJECT: %+v", v)
	}
	r = h.run("--project", "fleet", "status", "--json")
	var v statusJSON
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.ProjectSource != "--project" || v.Project.Slug != "fleet" {
		t.Fatalf("--project: %+v %v", r, err)
	}
	h.env[projectEnv] = "nope"
	if r := h.run("deployment", "list"); r.code != ExitUsage || !strings.Contains(r.stderr, "SIMPLEOTA_PROJECT") {
		t.Fatalf("bad SIMPLEOTA_PROJECT: %+v", r)
	}

	delete(h.env, projectEnv)
	if r := h.run("logout"); r.code != 0 {
		t.Fatal(r)
	}
	if cfg, _ := config.Load(h.configDir); cfg.ProjectID != "" {
		t.Fatalf("logout must clear the chosen project: %+v", cfg)
	}
}

func TestProjectTokenUsesItsOwnProject(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	if r := h.run("project", "use", "fleet"); r.code != ExitUsage || !strings.Contains(r.stderr, "own project") {
		t.Fatalf("project use with a project token: %+v", r)
	}
	if _, v := cliStatus(t, h); v.Project == nil || v.Project.Slug != "fleet" || v.ProjectSource != projectFromToken {
		t.Fatalf("%+v", v)
	}
}

func TestProjectListAndShow(t *testing.T) {
	h := twoProjects(t)
	h.run("project", "use", "fleet")
	r := h.run("project", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "SLUG") || !strings.Contains(r.stdout, "*  fleet") || !strings.Contains(r.stdout, "lab") {
		t.Fatalf("project list: %+v", r)
	}
	r = h.run("project", "list", "--json")
	var list []struct {
		Slug    string `json:"slug"`
		Current bool   `json:"current"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &list); err != nil || len(list) != 2 || !list[0].Current || list[1].Current {
		t.Fatalf("project list --json: %+v %v", r, err)
	}
	r = h.run("project", "show")
	if r.code != 0 || !strings.Contains(r.stdout, "Project fleet") || !strings.Contains(r.stdout, "esp_idf") {
		t.Fatalf("project show: %+v", r)
	}
	if r := h.run("project", "show", "nope"); r.code != ExitUsage {
		t.Fatalf("project show nope: %+v", r)
	}
	if r := h.run("project", "use"); r.code != ExitUsage {
		t.Fatalf("project use without a project: %+v", r)
	}
	if r := h.run("projects", "frobnicate"); r.code != ExitUsage {
		t.Fatalf("unknown subcommand: %+v", r)
	}
}
