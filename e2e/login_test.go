//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
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
			// The dev user has one project, and the project token reaches
			// only that project, so both resolve it without project use.
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
	if r := c.run(env["SIMPLEOTA_DEV_STAFF_TOKEN"]+"\n", "login", "--json"); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	unknownSlug := "pass the project ID instead of a slug"
	if serverHasMe(t) {
		// The staff account's own project is listed and current.
		st := decode[struct {
			Auth struct {
				Account struct {
					IsStaff bool `json:"is_staff"`
				}
			}
			Project struct{ Slug string }
		}](t, c.mustRun("status", "--json").stdout)
		if !st.Auth.Account.IsStaff || st.Project.Slug != env["SIMPLEOTA_DEV_STAFF_PROJECT_SLUG"] {
			t.Fatalf("status: %+v", st)
		}
		unknownSlug = "is visible to this token"
	} else if r := c.run("", "deployment", "list"); r.code != 2 || !strings.Contains(r.stderr, "project use <project-id>") {
		t.Fatalf("no project: %+v", r)
	}
	// Another account's project is reached by ID, not slug.
	if r := c.run("", "project", "use", env["SIMPLEOTA_DEV_PROJECT_SLUG"]); r.code != 2 || !strings.Contains(r.stderr, unknownSlug) {
		t.Fatalf("another account's slug: %+v", r)
	}
	c.mustRun("project", "use", env["SIMPLEOTA_DEV_PROJECT_ID"])
	st := decode[struct {
		Auth          struct{ State string }
		Project       struct{ ID, Name string }
		ProjectSource string `json:"project_source"`
	}](t, c.mustRun("status", "--json").stdout)
	if st.Auth.State != "valid" || st.Project.ID != env["SIMPLEOTA_DEV_PROJECT_ID"] || st.Project.Name == "" || st.ProjectSource != "project use" {
		t.Fatalf("status: %+v", st)
	}
	c.mustRun("deployment", "list")
	if r := c.run("", "--project", "does-not-exist-"+uid(), "status"); r.code != 2 || !strings.Contains(r.stdout, unknownSlug) {
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

// serverHasMe reports whether the dev server has GET /api/v1/me/.
func serverHasMe(t *testing.T) bool {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env["SIMPLEOTA_DEV_API_URL"]+"/api/v1/me/", nil)
	req.Header.Set("Authorization", "Bearer "+env["SIMPLEOTA_DEV_STAFF_TOKEN"])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
