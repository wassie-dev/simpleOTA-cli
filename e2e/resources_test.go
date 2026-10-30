//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// TestResourceCommands lists and shows each resource against the dev
// server, with a personal token whose only project is the current one.
func TestResourceCommands(t *testing.T) {
	c := newCLI(t)
	c.run(env["SIMPLEOTA_DEV_PERSONAL_TOKEN"]+"\n", "login")
	slug := env["SIMPLEOTA_DEV_PROJECT_SLUG"]

	projects := decode[[]struct {
		Slug    string `json:"slug"`
		Current bool   `json:"current"`
	}](t, c.mustRun("project", "list", "--json").stdout)
	if len(projects) != 1 || projects[0].Slug != slug || !projects[0].Current {
		t.Fatalf("project list: %+v", projects)
	}
	if out := c.mustRun("project", "show").stdout; !strings.Contains(out, "Project "+slug) {
		t.Fatalf("project show: %s", out)
	}
	if out := c.mustRun("project", "use", slug).stdout; !strings.Contains(out, "now act on "+slug) {
		t.Fatalf("project use: %s", out)
	}

	out := release(t, ciCLI(t))
	builds := decode[[]struct {
		BuildNumber int `json:"build_number"`
	}](t, c.mustRun("build", "list", "--json").stdout)
	if len(builds) == 0 || builds[0].BuildNumber != out.BuildNumber {
		t.Fatalf("build list: newest is %+v, want %d", builds, out.BuildNumber)
	}
	if s := c.mustRun("build", "show", strconv.Itoa(out.BuildNumber)).stdout; !strings.Contains(s, "esp32") {
		t.Fatalf("build show: %s", s)
	}
	deps := decode[[]struct {
		ID string `json:"id"`
	}](t, c.mustRun("deployment", "list", "--json").stdout)
	if len(deps) == 0 || deps[0].ID != out.DeploymentID {
		t.Fatalf("deployment list: newest is %+v, want %s", deps, out.DeploymentID)
	}

	d := newDevice(t)
	d.check(out.BuildNumber)
	if s := c.mustRun("device", "list", "--channel", "stable").stdout; !strings.Contains(s, d.id) {
		t.Fatalf("device list: %s", s)
	}
	if s := c.mustRun("device", "show", d.id).stdout; !strings.Contains(s, "Device "+d.id) || !strings.Contains(s, "stable") {
		t.Fatalf("device show: %s", s)
	}
	if s := c.mustRun("channel", "list").stdout; !strings.Contains(s, "stable") {
		t.Fatalf("channel list: %s", s)
	}
	if r := c.run("", "key", "list", "--json"); r.code != 0 || !strings.HasPrefix(r.stdout, "[") {
		t.Fatalf("key list: %+v", r)
	}
}
