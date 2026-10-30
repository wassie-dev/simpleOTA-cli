package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakerelease"
)

type harness struct {
	t         *testing.T
	fake      *fakeapi.Server
	release   *fakerelease.Server
	configDir string
	env       map[string]string
	stdin     string
	prompts   []string
}

func newHarness(t *testing.T) *harness {
	fake := fakeapi.New(t)
	return &harness{
		t:         t,
		fake:      fake,
		release:   fakerelease.New(t, "0.2.0", "linux", "amd64", []byte("new")),
		configDir: t.TempDir(),
		env:       map[string]string{"SIMPLEOTA_API_URL": fake.URL},
	}
}

type result struct {
	code   int
	stdout string
	stderr string
}

func (h *harness) run(args ...string) result {
	h.t.Helper()
	var out, errBuf bytes.Buffer
	prompts := append([]string(nil), h.prompts...)
	src := h.release.Source()
	a := &App{
		CurrentVersion: func() string { return "0.1.0" },
		UpdateSource:   &src,
		In:             strings.NewReader(h.stdin),
		Out:            &out,
		Err:            &errBuf,
		Getenv:         func(k string) string { return h.env[k] },
		Store:          credentials.File{Path: filepath.Join(h.configDir, "credentials.json")},
		NewAPI: func(u, tok string) api.API {
			return api.New(u, tok, "test", api.WithSleep(func(context.Context, time.Duration) error { return nil }))
		},
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	a.Sleep = func(_ context.Context, d time.Duration) error {
		now = now.Add(d)
		return nil
	}
	if prompts != nil {
		a.StdinIsTerminal = true
		a.Prompt = func(string) (string, error) {
			p := prompts[0]
			prompts = prompts[1:]
			return p, nil
		}
		a.PromptLine = a.Prompt
	}
	h.env["SIMPLEOTA_CONFIG_DIR"] = h.configDir
	code := a.Run(context.Background(), args)
	return result{code: code, stdout: out.String(), stderr: errBuf.String()}
}

func (h *harness) login(token string) {
	h.t.Helper()
	h.stdin = token + "\n"
	if r := h.run("login"); r.code != 0 {
		h.t.Fatalf("login failed: %+v", r)
	}
	h.stdin = ""
}
