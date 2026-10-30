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
	"sync/atomic"
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
	if res.BuildNumber != 1 || res.SecurityMode != "basic" || res.KeyID != "" || res.SHA256 != hex.EncodeToString(sum[:]) || res.Size != int64(len(firmware)) {
		t.Fatalf("res = %+v", res)
	}
	m := fake.LastManifest
	if m.Framework != "esp_idf" || m.ChipFamily != "esp32s3" || m.VersionLabel != "1.0.0" || m.SigningMetadata != nil || m.SHA256 == nil || *m.SHA256 != res.SHA256 {
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

func TestChecksumMismatchCreatesNothing(t *testing.T) {
	fake, svc := service(t)
	fake.CorruptUploads = true
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
	})
	if !errors.Is(err, release.ErrChecksumMismatch) || !strings.Contains(err.Error(), "nothing was created") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 0 || len(fake.Deployments) != 0 {
		t.Fatal("a rejected upload must not create a build")
	}
	if n := fake.Count("POST /api/v1/projects/" + fakeapi.ProjectID + "/release/"); n != 1 {
		t.Fatalf("upload sent %d times, want once", n)
	}
}

func TestDuplicateBinaryIsRefusedBeforeUpload(t *testing.T) {
	fake, svc := service(t)
	bin := writeBin(t, "app.bin", firmware)
	opts := release.Options{BinaryPath: bin, VersionLabel: "1.0.0", Chip: "esp32", Percent: 100, Channel: "stable"}
	if _, err := svc.Release(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.VersionLabel = "1.0.1"
	_, err := svc.Release(context.Background(), opts)
	if !errors.Is(err, release.ErrDuplicate) || !strings.Contains(err.Error(), "build 1 (version 1.0.0)") || !strings.Contains(err.Error(), "--allow-duplicate") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 1 {
		t.Fatalf("%d builds, want 1", len(fake.Artifacts))
	}

	opts.AllowDuplicate = true
	res, err := svc.Release(context.Background(), opts)
	if err != nil || res.BuildNumber != 2 {
		t.Fatalf("--allow-duplicate: %+v %v", res, err)
	}
	if fake.Count("GET /api/v1/projects/"+fakeapi.ProjectID+"/artifacts/") != 2 {
		t.Fatal("--allow-duplicate should skip the artifact lookup")
	}
}

func TestUnreadableArtifactListStopsBeforeUpload(t *testing.T) {
	fake, svc := service(t)
	fake.FailNext["/api/v1/projects/"+fakeapi.ProjectID+"/artifacts/"] = []int{500}
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
	})
	if err == nil || !strings.Contains(err.Error(), "earlier upload of this binary") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 0 {
		t.Fatal("nothing should be uploaded")
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

// unclearServer fails every upload with fail and counts the attempts.
func unclearServer(t *testing.T, fail func(http.ResponseWriter)) (release.Service, *atomic.Int32) {
	var uploads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/release/"):
			uploads.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			fail(w)
		case strings.HasSuffix(r.URL.Path, "/artifacts/"):
			_, _ = io.WriteString(w, `[]`)
		default:
			_, _ = io.WriteString(w, `{"id":"p","default_framework":"esp_idf"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := api.New(srv.URL, fakeapi.ProjectToken, "test", api.WithSleep(func(context.Context, time.Duration) error { return nil }))
	return release.Service{API: c, ProjectID: fakeapi.ProjectID}, &uploads
}

func TestUnclearUploadFailureIsNotRetried(t *testing.T) {
	failures := map[string]func(http.ResponseWriter){
		"dropped connection": func(w http.ResponseWriter) {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
		},
		"bad gateway":      func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"gateway timeout":  func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) },
		"unavailable":      func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) },
		"too many request": func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) },
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			svc, uploads := unclearServer(t, fail)
			_, err := svc.Release(context.Background(), release.Options{
				BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Percent: 100, Channel: "stable",
			})
			if err == nil {
				t.Fatal("want an error")
			}
			if uploads.Load() != 1 {
				t.Fatalf("upload sent %d times, want once", uploads.Load())
			}
			unclear := name != "too many request"
			if strings.Contains(err.Error(), "may still have created a build") != unclear {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
