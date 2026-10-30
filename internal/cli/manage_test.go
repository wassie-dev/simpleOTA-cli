package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func TestBuildUploadAndArchive(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	bin := writeFirmware(t)
	r := h.run("build", "upload", bin, "--version-label", "1.0.0", "--chip", "esp32", "--json")
	var v struct {
		BuildNumber  int    `json:"build_number"`
		SecurityMode string `json:"security_mode"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || r.code != 0 || v.BuildNumber != 1 || v.SecurityMode != "basic" {
		t.Fatalf("upload: %+v %v", r, err)
	}
	if len(h.fake.Deployments) != 0 {
		t.Fatal("build upload must not deploy")
	}
	if !strings.Contains(r.stderr, "Uploaded unsigned") {
		t.Fatalf("unsigned notice: %+v", r)
	}
	if r := h.run("build", "upload", bin, "--version-label", "1.0.1", "--chip", "esp32"); r.code != ExitError || !strings.Contains(r.stderr, "build 1 (version 1.0.0) has the same sha256") {
		t.Fatalf("duplicate: %+v", r)
	}
	if r := h.run("build", "upload", bin); r.code != ExitUsage {
		t.Fatalf("no version label: %+v", r)
	}

	if r := h.run("build", "archive", "1"); r.code != 0 || !strings.Contains(r.stdout, "archived") {
		t.Fatalf("archive: %+v", r)
	}
	if r := h.run("deployment", "create", "--build", "1"); r.code != ExitUsage || !strings.Contains(r.stderr, "unarchive it first") {
		t.Fatalf("deploying an archived build: %+v", r)
	}
	if r := h.run("build", "unarchive", "1"); r.code != 0 || !strings.Contains(r.stdout, "ready") {
		t.Fatalf("unarchive: %+v", r)
	}
	if r := h.run("deployment", "create", "--build", "1"); r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	if r := h.run("build", "archive", "1"); r.code != ExitError || !strings.Contains(r.stderr, "cancel or complete") {
		t.Fatalf("archive with an active deployment: %+v", r)
	}
	if r := h.run("build", "archive", "9"); r.code != ExitUsage {
		t.Fatalf("archive unknown: %+v", r)
	}
}

func TestDeploymentCreate(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	beta := "44444444-4444-4444-8444-444444444444"
	h.fake.Channels = append(h.fake.Channels, api.Channel{ID: beta, Name: "beta"})
	h.fake.Groups = []api.Group{{ID: "g1", Name: "kitchen"}}
	h.fake.Devices = []api.Device{{ID: "d1"}, {ID: "d2"}}
	for i := 0; i < 2; i++ {
		if r := h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32"); r.code != 0 {
			t.Fatal(r)
		}
	}

	r := h.run("deployment", "create", "--build", "1", "--percent", "10")
	if r.code != 0 || !strings.Contains(r.stdout, "Started deployment") || !strings.Contains(r.stdout, "10% of channel stable") {
		t.Fatalf("default channel: %+v", r)
	}
	if req := h.fake.LastDeploymentRequest; len(req.TargetChannelIDs) != 1 || req.TargetChannelIDs[0] != fakeapi.StableChannelID || req.RolloutPercentage != 10 {
		t.Fatalf("request: %+v", req)
	}
	if h.fake.Deployments[0].State != api.StateActive {
		t.Fatalf("not started: %+v", h.fake.Deployments[0])
	}

	r = h.run("deployment", "create", "--build", "2", "--json")
	var created struct {
		Deployment struct {
			BuildNumber int `json:"build_number"`
		} `json:"deployment"`
		AlsoActive []struct {
			BuildNumber int `json:"build_number"`
		} `json:"also_active"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &created); err != nil || created.Deployment.BuildNumber != 2 || len(created.AlsoActive) != 1 || created.AlsoActive[0].BuildNumber != 1 {
		t.Fatalf("also active: %+v %v", r, err)
	}

	if r := h.run("deployment", "create", "--build", "2", "--channel", "beta", "--group", "kitchen"); r.code != 0 || !strings.Contains(r.stdout, "channel beta, group kitchen") {
		t.Fatalf("channel and group: %+v", r)
	}
	if req := h.fake.LastDeploymentRequest; req.TargetChannelIDs[0] != beta || req.TargetGroupIDs[0] != "g1" {
		t.Fatalf("request: %+v", req)
	}
	if r := h.run("deployment", "create", "--build", "2", "--all-devices"); r.code != 0 || !strings.Contains(r.stdout, "every device in the project") {
		t.Fatalf("all devices: %+v", r)
	}
	for _, args := range [][]string{
		{"--percent", "10"},
		{"--build", "2", "--all-devices", "--channel", "beta"},
		{"--build", "2", "--percent", "101"},
		{"--build", "2", "--channel", "nope"},
		{"--build", "2", "--group", "nope"},
		{"--build", "7"},
	} {
		if r := h.run(append([]string{"deployment", "create"}, args...)...); r.code != ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestDeploymentCreateThatCannotStart(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")
	h.fake.FailNext["/api/v1/projects/"+fakeapi.ProjectID+"/deployments/cccccccc-0000-4000-8000-000000000001/transition/"] = []int{400}
	r := h.run("deployment", "create", "--build", "1")
	if r.code != ExitError || !strings.Contains(r.stderr, "was created but did not start") || !strings.Contains(r.stderr, "deployment cancel cccccccc-0000-4000-8000-000000000001") {
		t.Fatalf("%+v", r)
	}
}

func TestDeviceEventsAndUpdate(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	beta := "44444444-4444-4444-8444-444444444444"
	h.fake.Channels = append(h.fake.Channels, api.Channel{ID: beta, Name: "beta"})
	stable := fakeapi.StableChannelID
	id := "dddddddd-dddd-4ddd-8ddd-000000000001"
	h.fake.Devices = []api.Device{{ID: id, DeviceID: "lamp-1", ChannelID: &stable, Active: true}}
	running, offered := 6, 7
	h.fake.CheckEvents[id] = []api.CheckEvent{
		{CreatedAt: fakeapi.Epoch, Decision: "offered", Status: "offered", ReportedBuildNumber: &running, ArtifactBuildNumber: &offered},
		{CreatedAt: fakeapi.Epoch, Decision: "no_update", Status: "not_in_rollout", ReportedBuildNumber: &running, Message: "outside the 10% rollout"},
	}
	r := h.run("device", "events", "lamp-1")
	if r.code != 0 || !strings.Contains(r.stdout, "not_in_rollout") || !strings.Contains(r.stdout, "outside the 10% rollout") {
		t.Fatalf("events: %+v", r)
	}
	if r := h.run("device", "events", "lamp-1", "--limit", "21"); r.code != ExitUsage {
		t.Fatalf("limit: %+v", r)
	}

	r = h.run("device", "update", "lamp-1", "--channel", "beta", "--name", "Kitchen", "--state", "inactive")
	d := h.fake.Devices[0]
	if r.code != 0 || d.Name != "Kitchen" || d.Active || d.ChannelID == nil || *d.ChannelID != beta || !strings.Contains(r.stdout, "beta") {
		t.Fatalf("update: %+v %+v", r, d)
	}
	for _, args := range [][]string{{"lamp-1"}, {"lamp-1", "--state", "asleep"}, {"lamp-1", "--channel", "nope"}, {"lamp-9", "--name", "x"}} {
		if r := h.run(append([]string{"device", "update"}, args...)...); r.code != ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestChannelCreate(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	if r := h.run("channel", "create", "beta", "--default"); r.code != 0 || !strings.Contains(r.stdout, "Channel beta") || !strings.Contains(r.stdout, "new devices join it") {
		t.Fatalf("create: %+v", r)
	}
	if h.fake.Channels[0].IsDefault {
		t.Fatal("stable must stop being the default")
	}
	if r := h.run("channel", "create", "beta"); r.code != ExitError || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("duplicate: %+v", r)
	}
}

func TestGroups(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.Devices = []api.Device{{ID: "d1", DeviceID: "lamp-1"}, {ID: "d2", DeviceID: "lamp-2"}}
	if r := h.run("group", "list"); r.code != 0 || !strings.Contains(r.stdout, "No device groups") {
		t.Fatalf("empty: %+v", r)
	}
	if r := h.run("group", "create", "kitchen", "--description", "Kitchen lights"); r.code != 0 || !strings.Contains(r.stdout, "Group kitchen") {
		t.Fatalf("create: %+v", r)
	}
	if r := h.run("groups", "list"); r.code != 0 || !strings.Contains(r.stdout, "Kitchen lights") {
		t.Fatalf("list: %+v", r)
	}
	if r := h.run("group", "member", "add", "kitchen", "lamp-1", "lamp-9"); r.code != ExitUsage || len(h.fake.Devices[0].GroupIDs) != 0 {
		t.Fatalf("an unknown device must add none: %+v", r)
	}
	if r := h.run("group", "member", "add", "kitchen", "lamp-1", "lamp-2"); r.code != 0 || !strings.Contains(r.stdout, "Added lamp-1, lamp-2 to group kitchen") {
		t.Fatalf("add: %+v", r)
	}
	if r := h.run("group", "members", "list", "kitchen"); r.code != 0 || !strings.Contains(r.stdout, "lamp-2") {
		t.Fatalf("member list: %+v", r)
	}
	if r := h.run("group", "member", "remove", "kitchen", "lamp-2"); r.code != 0 || len(h.fake.Devices[1].GroupIDs) != 0 || len(h.fake.Devices[0].GroupIDs) != 1 {
		t.Fatalf("remove: %+v", r)
	}
	if r := h.run("group", "member", "add", "kitchen"); r.code != ExitUsage {
		t.Fatalf("no devices: %+v", r)
	}
	if r := h.run("group", "member", "list", "hall"); r.code != ExitUsage {
		t.Fatalf("unknown group: %+v", r)
	}
}

func TestKeyRevoke(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env[passphraseEnv] = "correct horse battery"
	h.run("key", "generate", "--key-id", "old")
	if r := h.run("key", "revoke", "old"); r.code != ExitUsage || !strings.Contains(r.stderr, "--yes") || h.fake.SigningKeys[0].RevokedAt != nil {
		t.Fatalf("without --yes: %+v", r)
	}
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.ProjectToken
	if r := h.run("key", "revoke", "old", "--yes"); r.code != ExitAuth || h.fake.SigningKeys[0].RevokedAt != nil {
		t.Fatalf("project token: %+v", r)
	}
	delete(h.env, "SIMPLEOTA_TOKEN")
	if r := h.run("key", "revoke", "old", "--yes"); r.code != 0 || !strings.Contains(r.stdout, "Revoked signing key old") || h.fake.SigningKeys[0].RevokedAt == nil {
		t.Fatalf("revoke: %+v", r)
	}
	if r := h.run("key", "revoke", "old", "--yes"); r.code != ExitError || !strings.Contains(r.stderr, "already revoked") {
		t.Fatalf("again: %+v", r)
	}
}

func TestAudit(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	actor := 7
	h.fake.Audit = []api.AuditEvent{
		{CreatedAt: fakeapi.Epoch.Add(time.Minute), EventType: "deployment.auto_paused", TargetType: "deployment", TargetID: "d1", Payload: map[string]any{}},
		{CreatedAt: fakeapi.Epoch, EventType: "artifact.uploaded", ActorID: &actor, TargetType: "artifact", TargetID: "a1", Payload: map[string]any{"build_number": 3}},
	}
	r := h.run("audit")
	if r.code != 0 || !strings.Contains(r.stdout, "system") || !strings.Contains(r.stdout, "user 7") || !strings.Contains(r.stdout, "build_number=3") {
		t.Fatalf("audit: %+v", r)
	}
	r = h.run("audit", "--type", "artifact.uploaded", "--json")
	var events []struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &events); err != nil || len(events) != 1 || events[0].EventType != "artifact.uploaded" {
		t.Fatalf("--type: %+v %v", r, err)
	}
	if r := h.run("audit", "--limit", "0"); r.code != ExitUsage {
		t.Fatalf("limit: %+v", r)
	}
}
