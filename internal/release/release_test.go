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

func keyLoader(k ed25519.PrivateKey) func() (ed25519.PrivateKey, error) {
	return func() (ed25519.PrivateKey, error) { return k, nil }
}

// everyone targets every device on the stable channel.
var everyone = release.Target{Channel: "stable", Percent: 100}

var firmware = []byte{0xe9, 0x04, 0x02, 0x20, 1, 2, 3, 4}

func TestBasicRelease(t *testing.T) {
	fake, svc := service(t)
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "ESP32S3",
	}, release.Target{Channel: "stable", Percent: 10})
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", Framework: "arduino",
	}, everyone)
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	}, everyone)
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", LoadKey: keyLoader(stranger),
	}, everyone)
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", LoadKey: keyLoader(priv),
	}, everyone)
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("err = %v", err)
	}
}

func TestChecksumMismatchCreatesNothing(t *testing.T) {
	fake, svc := service(t)
	fake.CorruptUploads = true
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32",
	}, everyone)
	if !errors.Is(err, release.ErrChecksumMismatch) || !strings.Contains(err.Error(), "Nothing was created") {
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
	opts := release.Options{BinaryPath: bin, VersionLabel: "1.0.0", Chip: "esp32"}
	if _, err := svc.Release(context.Background(), opts, everyone); err != nil {
		t.Fatal(err)
	}
	opts.VersionLabel = "1.0.1"
	_, err := svc.Release(context.Background(), opts, everyone)
	if !errors.Is(err, release.ErrDuplicate) || !strings.Contains(err.Error(), "build 1 (version 1.0.0)") || !strings.Contains(err.Error(), "--allow-duplicate") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 1 {
		t.Fatalf("%d builds, want 1", len(fake.Artifacts))
	}

	opts.AllowDuplicate = true
	res, err := svc.Release(context.Background(), opts, everyone)
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32",
	}, everyone)
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
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32",
	}, release.Target{Channel: "beta", Percent: 100})
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
		{name: "esp-idf bootloader", file: "bootloader.bin", data: firmware, wantErr: "ESP-IDF bootloader"},
		{name: "esp-idf partition table", file: "Partition-Table.bin", data: firmware, wantErr: "not an OTA app image"},
		{name: "esp-idf ota data", file: "ota_data_initial.bin", data: firmware, wantErr: "not an OTA app image"},
		{name: "app named after its project", file: "bootloader-tool.bin", data: firmware},
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
	valid := release.Options{BinaryPath: bin, VersionLabel: "1", Chip: "esp32"}
	for _, tc := range []struct {
		opts   release.Options
		target release.Target
	}{
		{release.Options{BinaryPath: bin, VersionLabel: "1", Chip: "esp 32"}, everyone},
		{valid, release.Target{Channel: "stable", Percent: 101}},
		{valid, release.Target{Channel: " ", Percent: 100}},
		{release.Options{BinaryPath: bin, VersionLabel: "", Chip: "esp32", Framework: "arduino"}, everyone},
	} {
		if _, err := svc.Release(context.Background(), tc.opts, tc.target); err == nil {
			t.Errorf("%+v should fail", tc)
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
				BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32",
			}, everyone)
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

// image returns an app image whose header names chipID.
func image(chipID uint16, fill byte) []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = fill
	}
	b[0] = 0xe9
	b[12], b[13] = byte(chipID), byte(chipID>>8)
	return b
}

func TestImageChip(t *testing.T) {
	for id, want := range map[uint16]string{0x0000: "esp32", 0x0009: "esp32s3", 0x0005: "esp32c3", 0x0014: "esp32c61", 0x001C: "esp32h4", 0x0042: ""} {
		if got := release.ImageChip(image(id, 0)); got != want {
			t.Errorf("chip ID 0x%04x: got %q, want %q", id, got, want)
		}
	}
	if got := release.ImageChip(firmware); got != "" {
		t.Errorf("a short image names no chip, got %q", got)
	}
}

func TestChipComesFromImageHeader(t *testing.T) {
	fake, svc := service(t)
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", image(0x0009, 1)), VersionLabel: "1",
	}, everyone)
	if err != nil || res.Chip != "esp32s3" || fake.LastManifest.ChipFamily != "esp32s3" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	_, err = svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1",
	}, everyone)
	var chipErr *release.ChipError
	if !errors.As(err, &chipErr) || !strings.Contains(err.Error(), "Pass --chip") {
		t.Fatalf("an image without a known chip needs --chip: %v", err)
	}
}

func TestChipMismatchIsRefusedUnlessADeviceReportsIt(t *testing.T) {
	fake, svc := service(t)
	opts := release.Options{BinaryPath: writeBin(t, "app.bin", image(0x0009, 2)), VersionLabel: "1", Chip: "esp99"}
	_, err := svc.Release(context.Background(), opts, everyone)
	var chipErr *release.ChipError
	if !errors.As(err, &chipErr) || !strings.Contains(err.Error(), "built for esp32s3, but --chip is esp99") {
		t.Fatalf("err = %v", err)
	}
	if fake.Count("POST /api/v1/projects/"+fakeapi.ProjectID+"/release/") != 0 {
		t.Fatal("a refused chip must not upload")
	}
	fake.Devices = []api.Device{{ID: "d1", DeviceID: "lamp-1", ChipFamily: "ESP99"}}
	res, err := svc.Release(context.Background(), opts, everyone)
	if err != nil || res.Chip != "esp99" {
		t.Fatalf("a chip a device reports is allowed: %+v %v", res, err)
	}
	opts.Chip, opts.AllowDuplicate = "esp32s3", true
	if _, err := svc.Release(context.Background(), opts, everyone); err != nil {
		t.Fatalf("a matching --chip: %v", err)
	}
}

const (
	releasePath = "POST /api/v1/projects/" + fakeapi.ProjectID + "/release/"
	uploadPath  = "POST /api/v1/projects/" + fakeapi.ProjectID + "/artifacts/"
)

func TestReleaseWithLostReplyListsWhatItFindsAndFails(t *testing.T) {
	for name, status := range map[string]int{"dropped": 0, "bad gateway": 502} {
		t.Run(name, func(t *testing.T) {
			fake, svc := service(t)
			fake.LoseReply[releasePath] = []int{status}
			opts := release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32"}
			_, err := svc.Release(context.Background(), opts, everyone)
			if err == nil || !strings.Contains(err.Error(), "It is not known whether the upload created anything") {
				t.Fatalf("err = %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, `build 1, version "1": matches the request`) ||
				!strings.Contains(msg, "deployment "+fake.Deployments[0].ID+", active at 100% to channel \"stable\": matches the request") {
				t.Fatalf("candidates missing: %s", msg)
			}
			if fake.Count(releasePath) != 1 || len(fake.Artifacts) != 1 {
				t.Fatalf("%d release requests, %d builds", fake.Count(releasePath), len(fake.Artifacts))
			}
		})
	}
}

// concurrentJob serves the fake API, but the first release request instead
// finds that another job uploaded the same binary under another label and
// created a draft deployment at 7%. With forward set, the request then
// reaches the fake and only its reply is lost.
func concurrentJob(t *testing.T, fake *fakeapi.Server, forward bool) release.Service {
	sum := sha256.Sum256(firmware)
	var fired atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != releasePath || !fired.CompareAndSwap(false, true) {
			fake.Config.Handler.ServeHTTP(w, r)
			return
		}
		fake.Mu.Lock()
		fake.Artifacts = append(fake.Artifacts, api.Artifact{ID: "other-build", BuildNumber: 41, VersionLabel: "other", ChecksumSHA256: hex.EncodeToString(sum[:]), SecurityMode: "basic", ChipFamily: "esp32", Framework: "esp_idf"})
		fake.Deployments = append(fake.Deployments, api.Deployment{ID: "other-deployment", ArtifactID: "other-build", State: api.StateDraft, RolloutPercentage: 7, TargetChannelIDs: []string{fakeapi.StableChannelID}})
		fake.Mu.Unlock()
		if forward {
			fake.Config.Handler.ServeHTTP(httptest.NewRecorder(), r)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(front.Close)
	c := api.New(front.URL, fakeapi.ProjectToken, "test", api.WithSleep(func(context.Context, time.Duration) error { return nil }))
	return release.Service{API: c, ProjectID: fakeapi.ProjectID}
}

func TestReleaseDoesNotClaimAnotherJobsBuild(t *testing.T) {
	for name, forward := range map[string]bool{"request lost": false, "reply lost": true} {
		t.Run(name, func(t *testing.T) {
			fake := fakeapi.New(t)
			svc := concurrentJob(t, fake, forward)
			_, err := svc.Release(context.Background(), release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32"}, everyone)
			if err == nil {
				t.Fatal("a release whose outcome is unknown must fail")
			}
			msg := err.Error()
			for _, want := range []string{
				`build 41, version "other": differs: version "other", not "1"`,
				`deployment other-deployment, draft at 7% to channel "stable": differs: draft, not active; 7%, not 100%`,
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q in:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "matches the request") != forward {
				t.Errorf("forward=%v, message:\n%s", forward, msg)
			}
		})
	}
}

func TestUploadWithLostReplyListsTheBuildAndFails(t *testing.T) {
	fake, svc := service(t)
	fake.LoseReply[uploadPath] = []int{0}
	_, err := svc.Upload(context.Background(), release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32"})
	if err == nil || !strings.Contains(err.Error(), `build 1, version "1": matches the request`) || strings.Contains(err.Error(), "deployment") || fake.Count(uploadPath) != 1 {
		t.Fatalf("err = %v, %d uploads", err, fake.Count(uploadPath))
	}
}

func TestReleaseStaysUncertainWhenNothingIsFound(t *testing.T) {
	fake, svc := service(t)
	fake.FailNext[releasePath] = []int{503}
	_, err := svc.Release(context.Background(), release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32"}, everyone)
	if err == nil || !strings.Contains(err.Error(), "shows none of\nthis binary yet") || fake.Count(releasePath) != 1 {
		t.Fatalf("err = %v, %d requests", err, fake.Count(releasePath))
	}
}

func TestReleaseWithAllowDuplicateDoesNotLookAfterLostReply(t *testing.T) {
	fake, svc := service(t)
	opts := release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32", AllowDuplicate: true}
	if _, err := svc.Release(context.Background(), opts, everyone); err != nil {
		t.Fatal(err)
	}
	fake.LoseReply[releasePath] = []int{0}
	if _, err := svc.Release(context.Background(), opts, everyone); err == nil || !strings.Contains(err.Error(), "simpleota build list") {
		t.Fatalf("err = %v", err)
	}
}

func TestDuplicateRefusalNamesRunningDeployments(t *testing.T) {
	fake, svc := service(t)
	opts := release.Options{BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1", Chip: "esp32"}
	if _, err := svc.Release(context.Background(), opts, everyone); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Release(context.Background(), opts, everyone)
	if !errors.Is(err, release.ErrDuplicate) || !strings.Contains(err.Error(), "deployment "+fake.Deployments[0].ID+" is active at 100%") {
		t.Fatalf("err = %v", err)
	}
}
