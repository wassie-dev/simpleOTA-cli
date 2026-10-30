package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

// writeFirmware writes a unique fake app image.
func writeFirmware(t *testing.T) string {
	t.Helper()
	b := []byte{0xe9, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := rand.Read(b[1:]); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReleaseRefusesDuplicateUnlessAllowed(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	bin := writeFirmware(t)
	if r := h.run("release", bin, "--version-label", "1", "--chip", "esp32"); r.code != 0 {
		t.Fatalf("first release: %+v", r)
	}
	r := h.run("release", bin, "--version-label", "2", "--chip", "esp32")
	if r.code != ExitError || !strings.Contains(r.stderr, "--allow-duplicate") {
		t.Fatalf("duplicate: %+v", r)
	}
	r = h.run("release", bin, "--version-label", "2", "--chip", "esp32", "--allow-duplicate")
	if r.code != 0 || !strings.Contains(r.stdout, "Released build 2") {
		t.Fatalf("--allow-duplicate: %+v", r)
	}
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

func TestReleaseChipFromImageAndAlsoActive(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	img := make([]byte, 64)
	if _, err := rand.Read(img); err != nil {
		t.Fatal(err)
	}
	img[0], img[12], img[13] = 0xe9, 0x09, 0x00 // esp32s3
	bin := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(bin, img, 0o644); err != nil {
		t.Fatal(err)
	}

	r := h.run("release", bin, "--version-label", "1", "--chip", "esp99")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "built for esp32s3, but --chip is esp99") {
		t.Fatalf("mismatched --chip: %+v", r)
	}
	r = h.run("release", bin, "--version-label", "1")
	if r.code != 0 || !strings.Contains(r.stdout, "Released build 1 for esp32s3") || strings.Contains(r.stdout, "Also active") {
		t.Fatalf("first release: %+v", r)
	}
	r = h.run("release", bin, "--version-label", "2", "--allow-duplicate", "--json")
	var v struct {
		Chip       string `json:"chip"`
		AlsoActive []struct {
			ID          string `json:"id"`
			BuildNumber int    `json:"build_number"`
		} `json:"also_active"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("%+v: %v", r, err)
	}
	if r.code != 0 || v.Chip != "esp32s3" || len(v.AlsoActive) != 1 || v.AlsoActive[0].BuildNumber != 1 {
		t.Fatalf("%+v %+v", r, v)
	}
}
