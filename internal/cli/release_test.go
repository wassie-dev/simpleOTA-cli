package cli

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

func writeFirmware(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(p, []byte{0xe9, 1, 2, 3, 4, 5, 6, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReleaseSignsWithDefaultKey(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env[passphraseEnv] = "correct horse battery"
	if r := h.run("keys", "generate", "--key-id", "prod-2026"); r.code != 0 {
		t.Fatalf("generate: %+v", r)
	}

	// The default key signs the release; a CI project token can release.
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	r := h.run("release", writeFirmware(t), "--version-label", "1.0.0", "--chip", "esp32", "--json")
	if r.code != 0 {
		t.Fatalf("release: %+v", r)
	}
	if !strings.Contains(r.stdout, `"security_mode": "signed"`) || !strings.Contains(r.stdout, `"key_id": "prod-2026"`) {
		t.Fatalf("release output: %s", r.stdout)
	}

	// --security-mode basic overrides the default key.
	r = h.run("release", writeFirmware(t), "--version-label", "1.0.1", "--chip", "esp32", "--security-mode", "basic")
	if r.code != 0 || h.fake.LastManifest.SecurityMode != "basic" || !strings.Contains(r.stderr, "unsigned") {
		t.Fatalf("basic release: %+v", r)
	}
}

func TestReleaseRefusesLooseKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	key := filepath.Join(t.TempDir(), "k.pem")
	priv, _ := signing.Generate()
	pemBytes, _ := signing.PrivateKeyPEM(priv)
	if err := os.WriteFile(key, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--key-file", key)
	if r.code != ExitError || !strings.Contains(r.stderr, "chmod 600") {
		t.Fatalf("%+v", r)
	}
	if len(h.fake.Artifacts) != 0 {
		t.Fatal("nothing should be uploaded")
	}
}

func TestReleaseFromEnvKey(t *testing.T) {
	h := newHarness(t)
	priv, _ := signing.Generate()
	pub, _ := signing.PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	h.fake.SigningKeys = append(h.fake.SigningKeys, api.SigningKey{KeyID: "ci-key", PublicKeyPEM: pub})
	sealed, _ := signing.Encrypt(priv, "correct horse battery")
	plain, _ := signing.PrivateKeyPEM(priv)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken

	h.env[signingKeyEnv] = string(sealed)
	if r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32"); r.code != ExitUsage || !strings.Contains(r.stderr, passphraseEnv) {
		t.Fatalf("encrypted key without passphrase: %+v", r)
	}
	h.env[passphraseEnv] = "correct horse battery"
	if r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32"); r.code != 0 || h.fake.LastManifest.SigningMetadata.KeyID != "ci-key" {
		t.Fatalf("encrypted key: %+v", r)
	}
	delete(h.env, passphraseEnv)
	h.env[signingKeyEnv] = string(plain)
	if r := h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32"); r.code != 0 || h.fake.LastManifest.SecurityMode != "signed" {
		t.Fatalf("plain key: %+v", r)
	}
}

func TestReleaseUsageErrors(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	bin := writeFirmware(t)
	for _, args := range [][]string{
		{"release", bin, "--chip", "esp32"},
		{"release", bin, "--version-label", "1"},
		{"release", bin, "--version-label", "1", "--chip", "esp32", "--percent", "150"},
		{"release", bin, "--version-label", "1", "--chip", "esp32", "--framework", "zephyr"},
		{"release", bin, "--version-label", "1", "--chip", "esp32", "--security-mode", "token"},
		{"release", bin, "--version-label", "1", "--chip", "esp32", "--security-mode", "signed"},
		{"release", "--version-label", "1", "--chip", "esp32"},
	} {
		if r := h.run(args...); r.code != ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestEnvTokenTakesPrecedence(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env["SIMPLEOTA_TOKEN"] = "soto_proj_invalidinvalidinvalid"
	if r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32"); r.code != ExitAuth {
		t.Fatalf("%+v", r)
	}
}
