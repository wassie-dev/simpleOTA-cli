package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/fakerelease"
)

// runUpdate runs simpleota with the binary at exe reporting version current,
// against a fake release of latest.
func runUpdate(t *testing.T, current, latest string, mutate func(*fakerelease.Server), args ...string) (result, string, *fakerelease.Server) {
	t.Helper()
	rel := fakerelease.New(t, latest, "linux", "amd64", []byte("new binary "+latest))
	if mutate != nil {
		rel.Set(mutate)
	}
	exe := filepath.Join(t.TempDir(), "simpleota")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	src := rel.Source()
	a := &App{
		In:             strings.NewReader(""),
		Out:            &out,
		Err:            &errBuf,
		Getenv:         func(string) string { return "" },
		CurrentVersion: func() string { return current },
		Executable:     func() (string, error) { return exe, nil },
		UpdateSource:   &src,
	}
	code := a.Run(context.Background(), append([]string{"update"}, args...))
	return result{code: code, stdout: out.String(), stderr: errBuf.String()}, exe, rel
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateInstallsNewerRelease(t *testing.T) {
	r, exe, _ := runUpdate(t, "0.1.0", "0.2.0", nil)
	if r.code != 0 || !strings.Contains(r.stdout, "Updated simpleota from 0.1.0 to 0.2.0") {
		t.Fatalf("%+v", r)
	}
	if content(t, exe) != "new binary 0.2.0" {
		t.Fatal("binary not replaced")
	}
}

func TestUpdateWhenUpToDate(t *testing.T) {
	r, exe, rel := runUpdate(t, "0.2.0", "0.2.0", nil)
	if r.code != 0 || !strings.Contains(r.stdout, "up to date") {
		t.Fatalf("%+v", r)
	}
	if content(t, exe) != "old binary" || rel.Downloads != 0 {
		t.Fatal("nothing should be downloaded or replaced")
	}
}

func TestUpdateCheck(t *testing.T) {
	r, exe, rel := runUpdate(t, "0.1.0", "0.2.0", nil, "--check", "--json")
	var v struct {
		Current         string `json:"current"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
		Updated         bool   `json:"updated"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || r.code != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if v.Current != "0.1.0" || v.Latest != "0.2.0" || !v.UpdateAvailable || v.Updated {
		t.Fatalf("%+v", v)
	}
	if content(t, exe) != "old binary" || rel.Downloads != 0 {
		t.Fatal("--check must not change anything")
	}
	r, _, _ = runUpdate(t, "0.1.0", "0.2.0", nil, "--check")
	if !strings.Contains(r.stdout, "simpleota 0.2.0 is available (you have 0.1.0)") {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateJSON(t *testing.T) {
	r, exe, _ := runUpdate(t, "0.1.0", "0.2.0", nil, "--json")
	var v struct {
		Updated bool   `json:"updated"`
		Path    string `json:"path"`
	}
	want, _ := filepath.EvalSymlinks(exe)
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || !v.Updated || v.Path != want {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestUpdateSpecificVersionIncludingDowngrade(t *testing.T) {
	r, exe, _ := runUpdate(t, "0.3.0", "0.2.0", nil, "--version", "0.2.0")
	if r.code != 0 || content(t, exe) != "new binary 0.2.0" || !strings.Contains(r.stdout, "Downgraded simpleota from 0.3.0 to 0.2.0") {
		t.Fatalf("%+v", r)
	}
	r, _, _ = runUpdate(t, "0.2.0", "0.2.0", nil, "--version", "v0.2.0")
	if r.code != 0 || !strings.Contains(r.stdout, "already installed") {
		t.Fatalf("%+v", r)
	}
	r, _, _ = runUpdate(t, "0.2.0", "0.2.0", nil, "--version", "latest")
	if r.code != ExitUsage {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateDevBuild(t *testing.T) {
	r, exe, _ := runUpdate(t, "dev", "0.2.0", nil)
	if r.code != ExitUsage || !strings.Contains(r.stderr, "development build") || content(t, exe) != "old binary" {
		t.Fatalf("%+v", r)
	}
	r, exe, _ = runUpdate(t, "dev", "0.2.0", nil, "--version", "0.2.0")
	if r.code != 0 || content(t, exe) != "new binary 0.2.0" || !strings.Contains(r.stdout, "Replaced development build dev with simpleota 0.2.0") {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateRefusesTamperedRelease(t *testing.T) {
	r, exe, _ := runUpdate(t, "0.1.0", "0.2.0", func(s *fakerelease.Server) { s.BadChecksum = true })
	if r.code != ExitError || !strings.Contains(r.stderr, "checksum mismatch") || content(t, exe) != "old binary" {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateBeforeAnyRelease(t *testing.T) {
	r, _, _ := runUpdate(t, "0.1.0", "0.2.0", func(s *fakerelease.Server) { s.NoRelease = true })
	if r.code != ExitError || !strings.Contains(r.stderr, "no simpleota release has been published yet") {
		t.Fatalf("%+v", r)
	}
}
