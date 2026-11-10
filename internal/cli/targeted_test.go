package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

const betaID = "44444444-4444-4444-8444-444444444444"

func strp(s string) *string { return &s }

// fleet has two active devices on stable, one of them in group kitchen, an
// inactive one on stable, and active ones on beta and on no channel.
func fleet() []api.Device {
	return []api.Device{
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000001", Active: true, ChannelID: strp(fakeapi.StableChannelID), GroupIDs: []string{"g1"}},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000002", Active: true, ChannelID: strp(fakeapi.StableChannelID)},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000003", Active: false, ChannelID: strp(fakeapi.StableChannelID)},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000004", Active: true, ChannelID: strp(betaID), GroupIDs: []string{"g1"}},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000005", Active: true},
	}
}

func targetedHarness(t *testing.T, devices []api.Device) *harness {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.Channels = append(h.fake.Channels, api.Channel{ID: betaID, Name: "beta"})
	h.fake.Groups = []api.Group{{ID: "g1", Name: "kitchen"}}
	h.fake.Devices = devices
	return h
}

// jsonObjects decodes each JSON object on stdout in turn.
func jsonObjects(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var out []map[string]any
	for dec.More() {
		var v map[string]any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("bad JSON %q: %v", stdout, err)
		}
		out = append(out, v)
	}
	return out
}

func TestReleaseReportsTargetedDevices(t *testing.T) {
	h := targetedHarness(t, fleet())
	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32")
	if r.code != 0 || !strings.Contains(r.stdout, "  devices     2 active devices targeted\n") {
		t.Fatalf("text: %+v", r)
	}
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32", "--json", "--require-devices")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || objs[0]["targeted_devices"] != 2.0 {
		t.Fatalf("json: %+v", r)
	}
}

func TestReleaseRequireDevicesFailsWhenNoneTargeted(t *testing.T) {
	devices := fleet()[2:]
	h := targetedHarness(t, devices)

	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--json")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || objs[0]["targeted_devices"] != 0.0 {
		t.Fatalf("without the flag: %+v", r)
	}
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32")
	if r.code != 0 || !strings.Contains(r.stdout, "no active device is targeted") {
		t.Fatalf("text without the flag: %+v", r)
	}

	r = h.run("release", writeFirmware(t), "--version-label", "3", "--chip", "esp32", "--require-devices", "--json")
	objs = jsonObjects(t, r.stdout)
	if r.code != ExitNoDevices || len(objs) != 2 {
		t.Fatalf("with the flag: %+v", r)
	}
	id, _ := objs[0]["deployment_id"].(string)
	if objs[0]["targeted_devices"] != 0.0 || id == "" || objs[1]["exit_code"] != float64(ExitNoDevices) {
		t.Fatalf("with the flag: %+v", objs)
	}
	msg, _ := objs[1]["error"].(string)
	if !strings.Contains(msg, "deployment "+id+" targets no active device") || !strings.Contains(msg, "\n  simpleota deployment cancel "+id) {
		t.Fatalf("error: %q", msg)
	}
	if d := h.fake.Deployments[0]; d.ID != id || d.State != api.StateActive {
		t.Fatalf("the deployment should be left active: %+v", d)
	}
}

func TestDeploymentCreateReportsTargetedDevices(t *testing.T) {
	h := targetedHarness(t, fleet())
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")
	tests := []struct {
		args []string
		want float64
	}{
		{args: nil, want: 2},
		{args: []string{"--channel", "beta", "--channel", "stable"}, want: 3},
		{args: []string{"--group", "kitchen"}, want: 2},
		{args: []string{"--channel", "stable", "--group", "kitchen"}, want: 1},
		{args: []string{"--all-devices"}, want: 4},
	}
	for _, tt := range tests {
		args := append([]string{"deployment", "create", "--build", "1", "--json", "--require-devices"}, tt.args...)
		r := h.run(args...)
		objs := jsonObjects(t, r.stdout)
		if r.code != 0 || len(objs) != 1 || objs[0]["targeted_devices"] != tt.want {
			t.Errorf("%v: %+v", tt.args, r)
		}
	}
	r := h.run("deployment", "create", "--build", "1", "--group", "kitchen")
	if r.code != 0 || !strings.Contains(r.stdout, "  devices  2 active devices targeted\n") {
		t.Fatalf("text: %+v", r)
	}
}

func TestDeploymentCreateRequireDevicesFailsWhenNoneTargeted(t *testing.T) {
	h := targetedHarness(t, fleet())
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")

	h.fake.Groups = append(h.fake.Groups, api.Group{ID: "g2", Name: "hall"})
	r := h.run("deployment", "create", "--build", "1", "--group", "hall")
	if r.code != 0 || !strings.Contains(r.stdout, "no active device is targeted") {
		t.Fatalf("without the flag: %+v", r)
	}
	r = h.run("deployment", "create", "--build", "1", "--group", "hall", "--require-devices")
	id := h.fake.Deployments[0].ID
	if r.code != ExitNoDevices || !strings.Contains(r.stdout, "Started deployment "+id) || !strings.Contains(r.stderr, "deployment "+id+" targets no active device") {
		t.Fatalf("with the flag: %+v", r)
	}
}

func TestTargetedCountFailure(t *testing.T) {
	h := targetedHarness(t, fleet())
	devices := "GET /api/v1/projects/" + fakeapi.ProjectID + "/devices/"

	h.fake.FailNext[devices] = []int{500}
	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--json")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || !strings.Contains(r.stderr, "Counting the targeted devices failed") {
		t.Fatalf("without the flag: %+v", r)
	}
	if v, ok := objs[0]["targeted_devices"]; !ok || v != nil {
		t.Fatalf("targeted_devices should be null: %+v", objs[0])
	}

	h.fake.FailNext[devices] = []int{500}
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32", "--require-devices")
	if r.code != ExitError || !strings.Contains(r.stderr, "could not count the devices deployment") || strings.Contains(r.stdout, "  devices") {
		t.Fatalf("with the flag: %+v", r)
	}
}
