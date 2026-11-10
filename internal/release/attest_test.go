package release_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

const projectPath = "/api/v1/projects/" + fakeapi.ProjectID + "/"

// checkManifest verifies an attested build's signature the way a device
// does: over the manifest rebuilt from the project ID and the build.
func checkManifest(t *testing.T, fake *fakeapi.Server, priv ed25519.PrivateKey, a api.Artifact, keyID string) {
	t.Helper()
	m := signing.Manifest{ProjectID: fakeapi.ProjectID, KeyID: keyID, Build: a.BuildNumber, SHA256: a.ChecksumSHA256}
	msg, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.StdEncoding.DecodeString(fake.ManifestSignatures[a.ID])
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), msg, sig) {
		t.Fatalf("build %d: no manifest signature that verifies", a.BuildNumber)
	}
}

func TestSignedUploadIsAttested(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	res, err := svc.Upload(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Attested || !fake.Artifacts[0].Attested {
		t.Fatalf("res = %+v", res)
	}
	checkManifest(t, fake, priv, fake.Artifacts[0], "prod-2026")
}

func TestBasicUploadIsNotAttested(t *testing.T) {
	fake, svc := service(t)
	res, err := svc.Upload(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32",
	})
	if err != nil || res.Attested {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if n := fake.Count("POST " + projectPath + "artifacts/" + res.ArtifactID + "/attest/"); n != 0 {
		t.Fatalf("attest calls = %d", n)
	}
}

func TestFailedAttestationNamesTheCommandToFinish(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	fake.FailNext["POST "+projectPath+"artifacts/aaaaaaaa-0000-4000-8000-000000000001/attest/"] = []int{503}

	res, err := svc.Upload(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	})

	var ae *release.AttestError
	if !errors.As(err, &ae) || ae.Build != 1 || !strings.Contains(err.Error(), "simpleota build attest 1 <bin>") {
		t.Fatalf("err = %v", err)
	}
	if res.BuildNumber != 1 || res.Attested {
		t.Fatalf("res = %+v", res)
	}
}

func TestSignedReleaseAttestsBeforeItDeploys(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	res, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	}, release.Target{Channel: "stable", Percent: 25})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Attested || res.DeploymentID == "" || res.State != api.StateActive || res.Channel != "stable" {
		t.Fatalf("res = %+v", res)
	}
	if fake.Count("POST "+projectPath+"release/") != 0 {
		t.Fatal("a signed release must not use the quick-release call, which cannot attest")
	}
	req := fake.LastDeploymentRequest
	if req.ArtifactID != res.ArtifactID || req.RolloutPercentage != 25 || len(req.TargetChannelIDs) != 1 || req.TargetChannelIDs[0] != fakeapi.StableChannelID {
		t.Fatalf("deployment request = %+v", req)
	}
	checkManifest(t, fake, priv, fake.Artifacts[0], "prod-2026")
}

func TestSignedReleaseToUnknownChannelUploadsNothing(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	}, release.Target{Channel: "beta", Percent: 100})
	if err == nil || !strings.Contains(err.Error(), "channel 'beta' not found") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Artifacts) != 0 {
		t.Fatal("nothing should be uploaded")
	}
}

func TestSignedReleaseThatCannotStartSaysWhatExists(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	fake.FailNext["POST "+projectPath+"deployments/cccccccc-0000-4000-8000-000000000001/transition/"] = []int{400}

	_, err := svc.Release(context.Background(), release.Options{
		BinaryPath: writeBin(t, "app.bin", firmware), VersionLabel: "1.0.0", Chip: "esp32", LoadKey: keyLoader(priv),
	}, everyone)

	var de *release.DeployError
	if !errors.As(err, &de) || !strings.Contains(err.Error(), "simpleota deployment cancel cccccccc-0000-4000-8000-000000000001") {
		t.Fatalf("err = %v", err)
	}
}

func TestAttestAnEarlierBuild(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	bin := writeBin(t, "app.bin", firmware)
	fake.FailNext["POST "+projectPath+"artifacts/aaaaaaaa-0000-4000-8000-000000000001/attest/"] = []int{503}
	if _, err := svc.Upload(context.Background(), release.Options{BinaryPath: bin, VersionLabel: "1", Chip: "esp32", LoadKey: keyLoader(priv)}); err == nil {
		t.Fatal("the injected failure should leave the build unattested")
	}

	art, err := svc.Attest(context.Background(), priv, 1, bin)
	if err != nil {
		t.Fatal(err)
	}
	if !art.Attested {
		t.Fatalf("art = %+v", art)
	}
	checkManifest(t, fake, priv, fake.Artifacts[0], "prod-2026")
}

func TestAttestRefusals(t *testing.T) {
	fake, svc := service(t)
	priv := registerKey(t, fake, "prod-2026", false)
	other := registerKey(t, fake, "other", false)
	bin := writeBin(t, "app.bin", firmware)
	if _, err := svc.Upload(context.Background(), release.Options{BinaryPath: bin, VersionLabel: "1", Chip: "esp32", LoadKey: keyLoader(priv)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), release.Options{BinaryPath: writeBin(t, "basic.bin", append(firmware, 9)), VersionLabel: "2", Chip: "esp32"}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		key   ed25519.PrivateKey
		build int
		bin   string
		want  string
	}{
		"another file":  {priv, 1, writeBin(t, "x.bin", append(firmware, 7)), "is not build 1"},
		"another key":   {other, 1, bin, `signed with key "prod-2026", not "other"`},
		"basic build":   {priv, 2, bin, "build 2 is basic, not signed"},
		"no such build": {priv, 9, bin, "no build 9"},
	} {
		if _, err := svc.Attest(context.Background(), tc.key, tc.build, tc.bin); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
