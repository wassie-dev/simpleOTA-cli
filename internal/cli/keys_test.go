package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

func TestMain(m *testing.M) {
	restore := signing.SetWorkFactorForTesting(10)
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestKeysGenerateRequiresPersonalToken(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	h.env[passphraseEnv] = "correct horse battery"
	r := h.run("keys", "generate", "--key-id", "prod")
	if r.code != ExitAuth || !strings.Contains(r.stderr, "personal token") {
		t.Fatalf("%+v", r)
	}
	if len(h.fake.SigningKeys) != 0 {
		t.Fatal("no key should be registered")
	}
}

func TestKeysGenerateAndExport(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.prompts = []string{"correct horse battery", "correct horse battery"}
	r := h.run("keys", "generate", "--key-id", "prod-2026", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var kv struct {
		PublicKeyPEM      string `json:"public_key_pem"`
		PrivateKeyFile    string `json:"private_key_file"`
		DefaultSigningKey bool   `json:"default_signing_key"`
		FirmwareSnippet   string `json:"firmware_snippet"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &kv); err != nil {
		t.Fatal(err)
	}
	if !kv.DefaultSigningKey || !strings.Contains(kv.FirmwareSnippet, `"prod-2026"`) || !strings.Contains(r.stderr, "WARNING") {
		t.Fatalf("%+v", r)
	}
	if len(h.fake.SigningKeys) != 1 || h.fake.SigningKeys[0].PublicKeyPEM != kv.PublicKeyPEM {
		t.Fatal("public key not registered")
	}
	if strings.Contains(r.stdout, "PRIVATE KEY") {
		t.Fatal("private key printed by generate")
	}
	sealed, _ := os.ReadFile(kv.PrivateKeyFile)
	if !signing.IsEncrypted(sealed) {
		t.Fatal("key file is not encrypted")
	}

	h.prompts = nil
	h.env[passphraseEnv] = "correct horse battery"
	if r := h.run("keys", "export", "--format", "pem"); r.code != 0 || !strings.Contains(r.stdout, "BEGIN PRIVATE KEY") {
		t.Fatalf("export pem: %+v", r)
	}
	if r := h.run("keys", "export"); r.code != 0 || !strings.Contains(r.stdout, "BEGIN AGE ENCRYPTED FILE") {
		t.Fatalf("export encrypted: %+v", r)
	}
}

func TestKeysGenerateRejectsDuplicateAndMismatch(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.fake.SigningKeys = append(h.fake.SigningKeys, api.SigningKey{KeyID: "prod"})
	h.env[passphraseEnv] = "correct horse battery"
	if r := h.run("keys", "generate", "--key-id", "prod"); r.code != ExitUsage {
		t.Fatalf("duplicate: %+v", r)
	}
	delete(h.env, passphraseEnv)
	h.prompts = []string{"correct horse battery", "different passphrase"}
	if r := h.run("keys", "generate", "--key-id", "new"); r.code != ExitUsage || !strings.Contains(r.stderr, "do not match") {
		t.Fatalf("mismatch: %+v", r)
	}
	if r := h.run("keys", "generate", "--key-id", "bad id"); r.code != ExitUsage {
		t.Fatalf("bad id: %+v", r)
	}
}

func TestKeysGenerateRejectsShortEnvPassphrase(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env[passphraseEnv] = "short"
	if r := h.run("keys", "generate", "--key-id", "prod"); r.code != ExitUsage || !strings.Contains(r.stderr, passphraseEnv) {
		t.Fatalf("%+v", r)
	}
	if len(h.fake.SigningKeys) != 0 {
		t.Fatal("no key should be registered")
	}
}

func TestKeysGenerateSnippetPinsBackupKey(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env[passphraseEnv] = "correct horse battery"
	type keyJSON struct {
		FirmwareSnippet string   `json:"firmware_snippet"`
		PinnedKeyIDs    []string `json:"pinned_key_ids"`
	}
	generate := func(id string) (result, keyJSON) {
		t.Helper()
		r := h.run("keys", "generate", "--key-id", id, "--json")
		var kv keyJSON
		if err := json.Unmarshal([]byte(r.stdout), &kv); err != nil || r.code != 0 {
			t.Fatalf("%+v: %v", r, err)
		}
		return r, kv
	}
	if _, kv := generate("prod"); strings.Join(kv.PinnedKeyIDs, ",") != "prod" || !strings.Contains(kv.FirmwareSnippet, "num_signing_keys = 1") {
		t.Fatalf("first key: %+v", kv)
	}
	r, kv := generate("backup")
	if strings.Join(kv.PinnedKeyIDs, ",") != "backup,prod" || !strings.Contains(kv.FirmwareSnippet, "num_signing_keys = 2;") ||
		strings.Contains(kv.FirmwareSnippet, "backup-key-id") || strings.Contains(r.stderr, "other active keys") {
		t.Fatalf("second key: %+v %+v", r, kv)
	}
	now := time.Now()
	h.fake.SigningKeys[0].RevokedAt = &now // prod
	if _, kv := generate("next"); strings.Join(kv.PinnedKeyIDs, ",") != "next,backup" {
		t.Fatalf("a revoked key is not pinned: %+v", kv)
	}
	r, kv = generate("fourth")
	if strings.Join(kv.PinnedKeyIDs, ",") != "fourth,next" || !strings.Contains(r.stderr, "2 other active keys (next, backup)") {
		t.Fatalf("more keys than slots: %+v %+v", r, kv)
	}
	r = h.run("keys", "generate", "--key-id", "fifth")
	if r.code != 0 || !strings.Contains(r.stdout, "Pin fifth and fourth in your firmware") || strings.Contains(r.stdout, "Next: run keys generate again") {
		t.Fatalf("human output: %+v", r)
	}
}
