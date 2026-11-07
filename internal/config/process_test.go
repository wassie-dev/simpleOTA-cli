package config

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
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
