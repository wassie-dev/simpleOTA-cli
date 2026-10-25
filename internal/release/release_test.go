package release_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

func writeBin(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func service(t *testing.T) (*fakeapi.Server, release.Service) {
	fake := fakeapi.New(t)
	c := api.New(fake.URL, fakeapi.ProjectToken, "test", api.WithSleep(func(context.Context, time.Duration) error { return nil }))
	return fake, release.Service{API: c, ProjectID: fakeapi.ProjectID}
}

func registerKey(t *testing.T, fake *fakeapi.Server, keyID string, revoked bool) ed25519.PrivateKey {
	t.Helper()
	priv, err := signing.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := signing.PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	k := api.SigningKey{KeyID: keyID, PublicKeyPEM: pub}
	if revoked {
		now := time.Now()
		k.RevokedAt = &now
	}
	fake.SigningKeys = append(fake.SigningKeys, k)
	return priv
}

var firmware = []byte{0xe9, 0x04, 0x02, 0x20, 1, 2, 3, 4}

func TestBasicRelease(t *testing.T) {
	fake, svc := service(t)
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "ESP32S3", Percent: 10, Channel: "stable",
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(firmware)
	if !res.SHA256Verified || res.Warning != "" {
		t.Fatalf("checksum should be verified: %+v", res)
	}
	if res.BuildNumber != 1 || res.SecurityMode != "basic" || res.KeyID != "" || res.SHA256 != hex.EncodeToString(sum[:]) || res.Size != int64(len(firmware)) {
		t.Fatalf("res = %+v", res)
	}
	m := fake.LastManifest
	if m.Framework != "esp_idf" || m.ChipFamily != "esp32s3" || m.VersionLabel != "1.0.0" || m.SigningMetadata != nil {
		t.Fatalf("manifest = %+v", m)
	}
	if fake.Deployments[0].RolloutPercentage != 10 {
		t.Fatalf("percent = %d", fake.Deployments[0].RolloutPercentage)
	}
}

func TestExplicitFrameworkSkipsProjectLookup(t *testing.T) {
	fake, svc := service(t)
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Framework: "arduino", Percent: 100, Channel: "stable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.LastManifest.Framework != "arduino" || fake.Count("GET /api/v1/projects/"+fakeapi.ProjectID+"/") != 0 {
		t.Fatalf("framework=%q", fake.LastManifest.Framework)
	}
}

func TestSignedReleaseUsesRegisteredKeyID(t *testing.T) {
	fake, svc := service(t)
	registerKey(t, fake, "other", false)
	priv := registerKey(t, fake, "prod-2026", false)
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", Percent: 100, Channel: "stable", Key: priv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SecurityMode != manifest.SecuritySigned || res.KeyID != "prod-2026" {
		t.Fatalf("res = %+v", res)
	}
	if meta := fake.LastManifest.SigningMetadata; meta == nil || meta.KeyID != "prod-2026" || meta.Algorithm != "ed25519" {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestUnregisteredKeyIsRefusedBeforeUpload(t *testing.T) {
	fake, svc := service(t)
	registerKey(t, fake, "prod-2026", false)
	stranger, _ := signing.Generate()
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable", Key: stranger,
	})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 0 {
		t.Fatal("nothing should be uploaded")
	}
}

func TestRevokedKeyIsRefused(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "old", true)
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable", Key: priv,
	})
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("err = %v", err)
	}
}

func TestChecksumMismatchCancelsDeployment(t *testing.T) {
	fake, svc := service(t)
	fake.StoredChecksumOverride = strings.Repeat("0", 64)
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
	})
	if !errors.Is(err, release.ErrChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
	if fake.Deployments[0].State != api.StateCancelled {
		t.Fatalf("deployment state = %s, want cancelled", fake.Deployments[0].State)
	}
}

func TestServerErrorsPassThrough(t *testing.T) {
	_, svc := service(t)
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "beta",
	})
	if err == nil || !strings.Contains(err.Error(), "channel 'beta' not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadBinary(t *testing.T) {
	big := make([]byte, release.MaxBinarySize+1)
	big[0] = 0xe9
	tests := []struct {
		name, file string
		data       []byte
		wantErr    string
	}{
		{name: "ok", file: "app.bin", data: firmware},
		{name: "empty", file: "app.bin", data: nil, wantErr: "empty"},
		{name: "elf", file: "app.elf", data: []byte("\x7fELF"), wantErr: "not the .elf"},
		{name: "merged", file: "sketch.ino.merged.bin", data: firmware, wantErr: "not an OTA app image"},
		{name: "partitions", file: "x.partitions.bin", data: firmware, wantErr: "not an OTA app image"},
		{name: "too big", file: "app.bin", data: big, wantErr: "limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := release.ReadBinary(writeBin(t, tt.file, tt.data))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	if _, err := release.ReadBinary(t.TempDir()); err == nil {
		t.Fatal("a directory must be refused")
	}
}

func TestInvalidInputFailsBeforeNetwork(t *testing.T) {
	fake, svc := service(t)
	bin := writeBin(t, "app.bin", firmware)
	for _, o := range []release.Options{
		{BinaryPath: bin, VersionLabel: "1", Chip: "esp 32", Percent: 100, Channel: "stable"},
		{BinaryPath: bin, VersionLabel: "1", Chip: "esp32", Percent: 101, Channel: "stable"},
		{BinaryPath: bin, VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: " "},
		{BinaryPath: bin, VersionLabel: "", Chip: "esp32", Framework: "arduino", Percent: 100, Channel: "stable"},
	} {
		if _, err := svc.Release(context.Background(), o); err == nil {
			t.Errorf("options %+v should fail", o)
		}
	}
	for k, n := range fake.Requests {
		if strings.HasPrefix(k, "POST") && n > 0 {
			t.Fatalf("unexpected %s", k)
		}
	}
}

func TestUnreadableChecksumIsAWarningNotAFailure(t *testing.T) {
	fake, svc := service(t)
	fake.FailNext["/api/v1/projects/"+fakeapi.ProjectID+"/artifacts/"] = []int{500}
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA256Verified || res.Warning == "" || res.BuildNumber != 1 {
		t.Fatalf("res = %+v", res)
	}
	if fake.Deployments[0].State != api.StateActive {
		t.Fatal("an unverifiable checksum must not cancel the deployment")
	}
}

func TestDroppedConnectionWarnsThatTheBuildMayExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release/") {
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		_, _ = io.WriteString(w, `{"id":"p","default_framework":"esp_idf"}`)
	}))
	defer srv.Close()
	svc := release.Service{API: api.New(srv.URL, fakeapi.ProjectToken, "test"), ProjectID: fakeapi.ProjectID}
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
	})
	if err == nil || !strings.Contains(err.Error(), "may still have created a build") {
		t.Fatalf("err = %v", err)
	}
}
