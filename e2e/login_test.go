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
