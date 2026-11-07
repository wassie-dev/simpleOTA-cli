package config

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const writerEnv = "SIMPLEOTA_CONFIG_TEST_WRITER"

// TestMain runs a writer process when the test binary is started as one.
func TestMain(m *testing.M) {
	if spec := os.Getenv(writerEnv); spec != "" {
		os.Exit(runWriter(spec))
	}
	os.Exit(m.Run())
}

// runWriter updates one field of the config in dir many times. spec is
// "dir|field|id".
func runWriter(spec string) int {
	parts := strings.Split(spec, "|")
	for i := range 40 {
		value := fmt.Sprintf("%s-%s-%02d", parts[1], parts[2], i)
		if _, err := Update(parts[0], func(c *Config) bool {
			if parts[1] == "key" {
				c.SigningKeyFile = value
			} else {
				c.ProjectID = value
			}
			return true
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func TestUpdatesFromSeveralProcessesKeepEachOthersFields(t *testing.T) {
	dir := t.TempDir()
	var procs []*exec.Cmd
	for i := range 8 {
		field := "project"
		if i == 0 {
			field = "key"
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), writerEnv+"="+dir+"|"+field+"|"+strconv.Itoa(i))
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, cmd)
	}
	for _, p := range procs {
		if err := p.Wait(); err != nil {
			t.Fatalf("writer: %v", err)
		}
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.SigningKeyFile != "key-0-39" || !strings.HasSuffix(c.ProjectID, "-39") {
		t.Fatalf("a field was lost: %+v", c)
	}
}

const holderEnv = "SIMPLEOTA_CONFIG_TEST_HOLDER"

func init() {
	if dir := os.Getenv(holderEnv); dir != "" {
		_, _ = Update(dir, func(*Config) bool {
			fmt.Println("locked")
			time.Sleep(time.Minute)
			return false
		})
		os.Exit(0)
	}
}

func TestLockIsFreeOnceItsHolderDies(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), holderEnv+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("holder said %q", line)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	start := time.Now()
	if _, err := Update(dir, func(c *Config) bool { c.ProjectID = "p"; return true }); err != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("update after the holder died: %v after %s", err, time.Since(start))
	}
}
