//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

type releaseOut struct {
	BuildNumber  int    `json:"build_number"`
	DeploymentID string `json:"deployment_id"`
	ArtifactID   string `json:"artifact_id"`
	SecurityMode string `json:"security_mode"`
	KeyID        string `json:"key_id"`
	SHA256       string `json:"sha256"`
}

// firmware writes a unique fake ESP32 app image.
func firmware(t *testing.T) string {
	t.Helper()
	b := make([]byte, 64<<10)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[0] = 0xe9
	p := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func release(t *testing.T, c *cli, args ...string) releaseOut {
	t.Helper()
	all := append([]string{"release", firmware(t), "--version-label", "e2e-" + uid(), "--chip", "esp32", "--json"}, args...)
	r := release0(t, c, all)
	t.Cleanup(func() { cancelDeployment(t, r.DeploymentID) })
	return r
}

func release0(t *testing.T, c *cli, args []string) releaseOut {
	t.Helper()
	out := decode[releaseOut](t, c.mustRun(args...).stdout)
	if out.BuildNumber == 0 || out.DeploymentID == "" {
		t.Fatalf("release output incomplete: %+v", out)
	}
	return out
}

// cancelDeployment stops a test deployment through the API directly.
func cancelDeployment(t *testing.T, id string) {
	req, _ := http.NewRequest(http.MethodPost, env["SIMPLEOTA_DEV_API_URL"]+"/api/v1/projects/"+env["SIMPLEOTA_DEV_PROJECT_ID"]+"/deployments/"+id+"/transition/", strings.NewReader(`{"action":"cancel"}`))
	req.Header.Set("Authorization", "Bearer "+env["SIMPLEOTA_DEV_PROJECT_TOKEN"])
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("cancelling %s: %v", id, err)
		return
	}
	resp.Body.Close()
}

func TestSignedReleaseAndTamperedSignature(t *testing.T) {
	admin := newCLI(t)
	admin.run(env["SIMPLEOTA_DEV_PERSONAL_TOKEN"]+"\n", "login")
	admin.extra["SIMPLEOTA_SIGNING_KEY_PASSPHRASE"] = "e2e passphrase long"
	keyID := "e2e-" + uid()
	gen := decode[struct {
		PrivateKeyFile string `json:"private_key_file"`
	}](t, admin.mustRun("keys", "generate", "--key-id", keyID, "--json").stdout)

	// CI: project token, exported encrypted key and passphrase in the environment.
	exported := admin.mustRun("keys", "export", "--key-file", gen.PrivateKeyFile).stdout
	ci := ciCLI(t)
	ci.extra["SIMPLEOTA_SIGNING_KEY"] = exported
	ci.extra["SIMPLEOTA_SIGNING_KEY_PASSPHRASE"] = "e2e passphrase long"
	out := release(t, ci)
	if out.SecurityMode != "signed" || out.KeyID != keyID {
		t.Fatalf("release: %+v", out)
	}

	// Sign one binary, then change a byte: the server must reject it.
	pemKey := admin.mustRun("keys", "export", "--format", "pem", "--key-file", gen.PrivateKeyFile).stdout
	priv, err := signing.ParsePrivateKey([]byte(pemKey), nil)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadFile(firmware(t))
	sig := signing.Sign(priv, bin)
	bin[len(bin)-1] ^= 0xff
	status, body := rawSignedRelease(t, bin, keyID, sig)
	if status != http.StatusBadRequest || !strings.Contains(body, "signature verification failed") {
		t.Fatalf("tampered binary: %d %s", status, body)
	}
}

// rawSignedRelease posts to the release endpoint directly with a chosen
// signature, which the CLI never produces.
func rawSignedRelease(t *testing.T, bin []byte, keyID, signature string) (int, string) {
	t.Helper()
	manifest, _ := json.Marshal(map[string]any{
		"framework": "esp_idf", "version_label": "tampered", "chip_family": "esp32", "security_mode": "signed",
		"signing_metadata": map[string]string{"key_id": keyID, "algorithm": "ed25519", "signature": signature},
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("manifest", string(manifest))
	fw, _ := mw.CreateFormFile("binary", "app.bin")
	_, _ = fw.Write(bin)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, env["SIMPLEOTA_DEV_API_URL"]+"/api/v1/projects/"+env["SIMPLEOTA_DEV_PROJECT_ID"]+"/release/", &body)
	req.Header.Set("Authorization", "Bearer "+env["SIMPLEOTA_DEV_PROJECT_TOKEN"])
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestUnregisteredKeyIsRefused(t *testing.T) {
	priv, err := signing.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pemKey, _ := signing.PrivateKeyPEM(priv)
	keyFile := filepath.Join(t.TempDir(), "stray.pem")
	if err := os.WriteFile(keyFile, pemKey, 0o600); err != nil {
		t.Fatal(err)
	}
	ci := ciCLI(t)
	r := ci.run("", "release", firmware(t), "--version-label", "stray", "--chip", "esp32", "--key-file", keyFile)
	if r.code != 1 || !strings.Contains(r.stderr, "not registered") {
		t.Fatalf("%+v", r)
	}
}

func TestReleaseValidatesBeforeUpload(t *testing.T) {
	c := ciCLI(t)
	merged := filepath.Join(t.TempDir(), "sketch.ino.merged.bin")
	if err := os.WriteFile(merged, []byte{0xe9, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := c.run("", "release", merged, "--version-label", "x", "--chip", "esp32"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if r := c.run("", "release", firmware(t), "--version-label", "x", "--chip", "esp32", "--channel", "nope-"+uid()); r.code != 1 || !strings.Contains(r.stderr, "not found") {
		t.Fatalf("%+v", r)
	}
}
