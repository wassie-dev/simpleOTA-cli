//go:build e2e

package e2e

import (
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
)

func uid() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ciCLI is a CLI authenticated with the api-scoped project token from the
// environment, as in CI.
func ciCLI(t *testing.T) *cli {
	c := newCLI(t)
	c.extra["SIMPLEOTA_TOKEN"] = env["SIMPLEOTA_DEV_PROJECT_TOKEN"]
	return c
}

func TestKeysGenerateNeedsPersonalToken(t *testing.T) {
	c := ciCLI(t)
	c.extra["SIMPLEOTA_SIGNING_KEY_PASSPHRASE"] = "e2e passphrase long"
	if r := c.run("", "key", "generate", "--key-id", "e2e-"+uid()); r.code != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestKeysGenerateAndExport(t *testing.T) {
	admin := newCLI(t)
	admin.run(env["SIMPLEOTA_DEV_PERSONAL_TOKEN"]+"\n", "login")
	admin.extra["SIMPLEOTA_SIGNING_KEY_PASSPHRASE"] = "e2e passphrase long"
	keyID := "e2e-" + uid()
	gen := decode[struct {
		KeyID          string `json:"key_id"`
		PrivateKeyFile string `json:"private_key_file"`
		PublicKeyPEM   string `json:"public_key_pem"`
	}](t, admin.mustRun("key", "generate", "--key-id", keyID, "--json").stdout)
	if gen.KeyID != keyID || !strings.HasPrefix(gen.PublicKeyPEM, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("generate: %+v", gen)
	}
	if r := admin.run("", "key", "generate", "--key-id", keyID); r.code != 2 {
		t.Fatalf("duplicate key id: %+v", r)
	}
	if out := admin.mustRun("key", "export", "--key-file", gen.PrivateKeyFile).stdout; !strings.Contains(out, "BEGIN AGE ENCRYPTED FILE") {
		t.Fatalf("encrypted export: %q", out)
	}
	if out := admin.mustRun("key", "export", "--format", "pem", "--key-file", gen.PrivateKeyFile).stdout; !strings.Contains(out, "BEGIN PRIVATE KEY") {
		t.Fatal("pem export missing the key")
	}
}
