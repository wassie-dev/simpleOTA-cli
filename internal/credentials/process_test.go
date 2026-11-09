package credentials

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const writerEnv = "SIMPLEOTA_CREDENTIALS_TEST_WRITER"

// TestMain runs a writer process when the test binary is started as one.
func TestMain(m *testing.M) {
	if spec := os.Getenv(writerEnv); spec != "" {
		os.Exit(runWriter(spec))
	}
	os.Exit(m.Run())
}

// runWriter stores tokens for servers of its own in the file. spec is
// "path|id".
func runWriter(spec string) int {
	path, id, _ := strings.Cut(spec, "|")
	f := File{Path: path}
	for i := range 10 {
		if err := f.Set(fmt.Sprintf("https://server-%s-%d.example.invalid", id, i), "soto_pat_test"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func TestTokensSavedBySeveralProcessesAreAllKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	var procs []*exec.Cmd
	for i := range 6 {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), writerEnv+"="+path+"|"+strconv.Itoa(i))
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
	tokens, err := File{Path: path}.load()
	if err != nil || len(tokens) != 60 {
		t.Fatalf("%d of 60 tokens kept: %v", len(tokens), err)
	}
}
