//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad JSON %q: %v", s, err)
	}
	return v
}

func TestLoginWithBothTokenTypes(t *testing.T) {
	for name, token := range map[string]string{"personal": env["SIMPLEOTA_DEV_PERSONAL_TOKEN"], "project": env["SIMPLEOTA_DEV_PROJECT_TOKEN"]} {
		t.Run(name, func(t *testing.T) {
			c := newCLI(t)
			r := c.run(token+"\n", "login", "--json")
			if r.code != 0 {
				t.Fatalf("login: %+v", r)
			}
			v := decode[struct {
				TokenType       string `json:"token_type"`
				CredentialStore string `json:"credential_store"`
				Project         struct{ ID string }
			}](t, r.stdout)
			if v.TokenType != name || v.Project.ID != env["SIMPLEOTA_DEV_PROJECT_ID"] {
				t.Fatalf("login output: %+v", v)
			}
			if strings.Contains(r.stdout+r.stderr, token) {
				t.Fatal("token printed")
			}
			c.mustRun("logout")
			if _, err := os.Stat(filepath.Join(c.configDir, "credentials.json")); !os.IsNotExist(err) {
				t.Fatalf("token file still present after logout: %v", err)
			}
		})
	}
	c := newCLI(t)
	if r := c.run("soto_pat_thisisnotarealtokenatall\n", "login"); r.code != 3 {
		t.Fatalf("bad token: %+v", r)
	}
}

// TestLoginWithStaffToken covers a staff account, which the server lists
// projects for only when given an owner.
func TestLoginWithStaffToken(t *testing.T) {
	c := newCLI(t)
	token := env["SIMPLEOTA_DEV_STAFF_TOKEN"] + "\n"
	if r := c.run(token, "login"); r.code != 2 || !strings.Contains(r.stderr, "staff account") || !strings.Contains(r.stderr, "--project <project-id>") {
		t.Fatalf("no project: %+v", r)
	}
	if r := c.run(token, "login", "--project", env["SIMPLEOTA_DEV_PROJECT_SLUG"]); r.code != 2 || !strings.Contains(r.stderr, "by ID, not slug") {
		t.Fatalf("slug: %+v", r)
	}
	r := c.run(token, "login", "--project", env["SIMPLEOTA_DEV_PROJECT_ID"], "--json")
	if r.code != 0 {
		t.Fatalf("project ID: %+v", r)
	}
	v := decode[struct {
		TokenType string `json:"token_type"`
		Project   struct{ ID, Slug string }
	}](t, r.stdout)
	if v.TokenType != "personal" || v.Project.ID != env["SIMPLEOTA_DEV_PROJECT_ID"] || v.Project.Slug != env["SIMPLEOTA_DEV_PROJECT_SLUG"] {
		t.Fatalf("login output: %+v", v)
	}
	st := decode[struct {
		Auth    struct{ State string }
		Project struct{ ID, Name string }
	}](t, c.mustRun("status", "--json").stdout)
	if st.Auth.State != "valid" || st.Project.ID != env["SIMPLEOTA_DEV_PROJECT_ID"] || st.Project.Name == "" {
		t.Fatalf("status: %+v", st)
	}
	if r := c.run("", "--project", "does-not-exist-"+uid(), "status"); r.code != 2 || !strings.Contains(r.stdout, "by ID, not slug") {
		t.Fatalf("status with an unknown project: %+v", r)
	}
}

func TestStatusReportsLogin(t *testing.T) {
	c := newCLI(t)
	if r := c.run("", "status"); r.code != 3 {
		t.Fatalf("status before login: %+v", r)
	}
	if r := c.run(env["SIMPLEOTA_DEV_PROJECT_TOKEN"]+"\n", "login"); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	r := c.run("", "status", "--json")
	if r.code != 0 {
		t.Fatalf("status after login: %+v", r)
	}
	v := decode[struct {
		APIURL string `json:"api_url"`
		Auth   struct {
			State     string `json:"state"`
			TokenType string `json:"token_type"`
		} `json:"auth"`
		Project struct{ ID string } `json:"project"`
	}](t, r.stdout)
	if v.Auth.State != "valid" || v.Auth.TokenType != "project" || v.Project.ID != env["SIMPLEOTA_DEV_PROJECT_ID"] || v.APIURL != env["SIMPLEOTA_DEV_API_URL"] {
		t.Fatalf("status: %+v", v)
	}
}
