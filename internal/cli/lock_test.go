package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

const configHolderEnv = "SIMPLEOTA_CLI_TEST_CONFIG_HOLDER"

// init makes the test binary, started with configHolderEnv set, a process
// that holds the config lock for a while and then saves a project.
func init() {
	dir := os.Getenv(configHolderEnv)
	if dir == "" {
		return
	}
	_, err := config.Update(dir, func(c *config.Config) bool {
		fmt.Println("locked")
		time.Sleep(time.Second)
		c.ProjectID = "held"
		return true
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestKeysGenerateWaitsForTheConfigLock(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env[passphraseEnv] = "correct horse battery"
	holder := exec.Command(os.Args[0], "-test.run=^$")
	holder.Env = append(os.Environ(), configHolderEnv+"="+h.configDir)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("holder said %q", line)
	}
	if r := h.run("keys", "generate", "--key-id", "prod"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
	cfg, err := config.Load(h.configDir)
	if err != nil || cfg.ProjectID != "held" || cfg.SigningKeyFile == "" {
		t.Fatalf("a change was lost: %+v %v", cfg, err)
	}
}
