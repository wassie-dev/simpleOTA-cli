//go:build e2e

// Package e2e runs the built CLI against a local SimpleOTA dev server
// started with scripts/devserver.sh. It refuses any non-loopback server.
package e2e

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	binary string
	env    map[string]string
)

func TestMain(m *testing.M) {
	envFile := os.Getenv("SIMPLEOTA_E2E_ENV")
	if envFile == "" {
		envFile = filepath.Join("..", ".devserver", "env")
	}
	var err error
	env, err = readEnv(envFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v; start the dev server with scripts/devserver.sh up\n", err)
		os.Exit(1)
	}
	u, err := url.Parse(env["SIMPLEOTA_DEV_API_URL"])
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		fmt.Fprintf(os.Stderr, "e2e: refusing to run against %q: only a local dev server is allowed\n", env["SIMPLEOTA_DEV_API_URL"])
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "simpleota-e2e-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "simpleota")
	build := exec.Command("go", "build", "-o", binary, "../cmd/simpleota")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build failed: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func readEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			out[k] = v
		}
	}
	for _, k := range []string{"SIMPLEOTA_DEV_API_URL", "SIMPLEOTA_DEV_PROJECT_ID", "SIMPLEOTA_DEV_PERSONAL_TOKEN", "SIMPLEOTA_DEV_STAFF_TOKEN", "SIMPLEOTA_DEV_PROJECT_TOKEN", "SIMPLEOTA_DEV_DEVICE_TOKEN"} {
		if out[k] == "" {
			return nil, fmt.Errorf("%s is missing %s", path, k)
		}
	}
	return out, sc.Err()
}

// cli runs the binary with an isolated config directory and no keychain.
type cli struct {
	t         *testing.T
	configDir string
	extra     map[string]string
}

func newCLI(t *testing.T) *cli {
	return &cli{t: t, configDir: t.TempDir(), extra: map[string]string{}}
}

type result struct {
	code           int
	stdout, stderr string
}

func (c *cli) run(stdin string, args ...string) result {
	c.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + c.configDir,
		"SIMPLEOTA_CONFIG_DIR=" + c.configDir,
		"SIMPLEOTA_NO_KEYRING=1",
		"SIMPLEOTA_API_URL=" + env["SIMPLEOTA_DEV_API_URL"],
		"NO_COLOR=1",
	}
	for k, v := range c.extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		c.t.Fatal(err)
	}
	return result{code: code, stdout: out.String(), stderr: errBuf.String()}
}

func (c *cli) mustRun(args ...string) result {
	c.t.Helper()
	r := c.run("", args...)
	if r.code != 0 {
		c.t.Fatalf("simpleota %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func TestBinaryRuns(t *testing.T) {
	r := newCLI(t).mustRun("--version")
	if !strings.HasPrefix(r.stdout, "simpleota version ") {
		t.Fatalf("--version printed %q", r.stdout)
	}
}

func TestDevServerIsHealthy(t *testing.T) {
	resp, err := http.Get(env["SIMPLEOTA_DEV_API_URL"] + "/api/health/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health check returned %d", resp.StatusCode)
	}
}
