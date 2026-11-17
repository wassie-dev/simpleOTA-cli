package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

const (
	betaID = "44444444-4444-4444-8444-444444444444"
	depB   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func strp(s string) *string { return &s }

// fleet has two active devices on stable, one of them in group kitchen, an
// inactive one on stable, and active ones on beta and on no channel.
func fleet() []api.Device {
	return []api.Device{
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000001", DeviceID: "mac-1", Active: true, ChannelID: strp(fakeapi.StableChannelID), GroupIDs: []string{"g1"}},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000002", DeviceID: "mac-2", Active: true, ChannelID: strp(fakeapi.StableChannelID)},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000003", DeviceID: "mac-3", Active: false, ChannelID: strp(fakeapi.StableChannelID)},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000004", DeviceID: "mac-4", Active: true, ChannelID: strp(betaID), GroupIDs: []string{"g1"}},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000005", DeviceID: "mac-5", Active: true},
	}
}

// compatibleHarness serves fleet, with device 2 failing the server's
// compatibility check, so the server's count is below the audience's size.
func compatibleHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.Channels = append(h.fake.Channels, api.Channel{ID: betaID, Name: "beta"})
	h.fake.Groups = []api.Group{{ID: "g1", Name: "kitchen"}}
	h.fake.Devices = fleet()
	h.fake.Incompatible[fleet()[1].ID] = true
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

const devicesPath = "GET /api/v1/projects/" + fakeapi.ProjectID + "/devices/"

func TestReleaseReportsTheServersCompatibleCount(t *testing.T) {
	h := compatibleHarness(t)
	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--percent", "10")
	want := "  devices     1 active device in the audience has compatible metadata\n" +
		"              (rollout percentage not applied; a check-in can change this)\n"
	if r.code != 0 || !strings.Contains(r.stdout, want) {
		t.Fatalf("text: %+v", r)
	}
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32", "--json", "--require-devices")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || objs[0]["compatible_audience_device_count"] != 1.0 {
		t.Fatalf("json: %+v", r)
	}
	if _, ok := objs[0]["targeted_devices"]; ok {
		t.Fatalf("old field still present: %+v", objs[0])
	}
	if n := h.fake.Count(devicesPath); n != 0 {
		t.Fatalf("release listed the project's devices %d times", n)
	}
}

func TestReleaseRequireDevicesFailsOnAZeroCount(t *testing.T) {
	h := compatibleHarness(t)
	h.fake.Incompatible[fleet()[0].ID] = true

	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--json")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || objs[0]["compatible_audience_device_count"] != 0.0 {
		t.Fatalf("without the flag: %+v", r)
	}
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32")
	if r.code != 0 || !strings.Contains(r.stdout, "no active device in the audience has compatible metadata") {
		t.Fatalf("text without the flag: %+v", r)
	}

	r = h.run("release", writeFirmware(t), "--version-label", "3", "--chip", "esp32", "--require-devices", "--json")
	objs = jsonObjects(t, r.stdout)
	if r.code != ExitNoDevices || len(objs) != 2 {
		t.Fatalf("with the flag: %+v", r)
	}
	id, _ := objs[0]["deployment_id"].(string)
	if objs[0]["compatible_audience_device_count"] != 0.0 || id == "" || objs[1]["exit_code"] != float64(ExitNoDevices) {
		t.Fatalf("with the flag: %+v", objs)
	}
	msg, _ := objs[1]["error"].(string)
	if !strings.Contains(msg, "deployment "+id+" has no compatible device") || !strings.Contains(msg, "\n  simpleota deployment cancel "+id) {
		t.Fatalf("error: %q", msg)
	}
	if d := h.fake.Deployments[0]; d.ID != id || d.State != api.StateActive {
		t.Fatalf("the deployment should be left active: %+v", d)
	}
}

func TestDeploymentCreateReportsTheServersCompatibleCount(t *testing.T) {
	h := compatibleHarness(t)
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")
	tests := []struct {
		args []string
		want float64
	}{
		{args: nil, want: 1},
		{args: []string{"--channel", "beta", "--channel", "stable"}, want: 2},
		{args: []string{"--group", "kitchen"}, want: 2},
		{args: []string{"--all-devices"}, want: 3},
	}
	for _, tt := range tests {
		args := append([]string{"deployment", "create", "--build", "1", "--json", "--require-devices"}, tt.args...)
		r := h.run(args...)
		objs := jsonObjects(t, r.stdout)
		if r.code != 0 || len(objs) != 1 || objs[0]["compatible_audience_device_count"] != tt.want {
			t.Errorf("%v: %+v", tt.args, r)
		}
	}
	r := h.run("deployment", "create", "--build", "1", "--group", "kitchen")
	if r.code != 0 || !strings.Contains(r.stdout, "  devices  2 active devices in the audience have compatible metadata\n") {
		t.Fatalf("text: %+v", r)
	}
	if n := h.fake.Count(devicesPath); n != 0 {
		t.Fatalf("deployment create listed the project's devices %d times", n)
	}
}

func TestDeploymentCreateRequireDevicesFailsOnAZeroCount(t *testing.T) {
	h := compatibleHarness(t)
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")

	r := h.run("deployment", "create", "--build", "1", "--channel", "stable", "--group", "kitchen", "--require-devices")
	if r.code != 0 {
		t.Fatalf("one compatible device: %+v", r)
	}
	h.fake.Incompatible[fleet()[0].ID] = true
	r = h.run("deployment", "create", "--build", "1", "--channel", "stable", "--group", "kitchen", "--require-devices")
	id := h.fake.Deployments[0].ID
	if r.code != ExitNoDevices || !strings.Contains(r.stdout, "Started deployment "+id) || !strings.Contains(r.stderr, "deployment "+id+" has no compatible device") {
		t.Fatalf("with the flag: %+v", r)
	}
	if d := h.fake.Deployments[0]; d.State != api.StateActive {
		t.Fatalf("the deployment should be left active: %+v", d)
	}
}

// An older server leaves out compatible_audience_device_count. The count is
// unknown, not 0: it is reported as null, and --require-devices fails with
// exit 1 instead of passing or claiming there are no devices.
func TestMissingCompatibleCount(t *testing.T) {
	h := compatibleHarness(t)
	h.fake.NoCompatibleCount = true
	h.run("build", "upload", writeFirmware(t), "--version-label", "0", "--chip", "esp32")

	for _, args := range [][]string{
		{"release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--json"},
		{"deployment", "create", "--build", "1", "--json"},
	} {
		r := h.run(args...)
		objs := jsonObjects(t, r.stdout)
		if r.code != 0 || len(objs) != 1 || !strings.Contains(r.stderr, "did not report compatible_audience_device_count") {
			t.Fatalf("%s without the flag: %+v", args[0], r)
		}
		if v, ok := objs[0]["compatible_audience_device_count"]; !ok || v != nil {
			t.Fatalf("%s: the count should be null: %+v", args[0], objs[0])
		}
	}

	for _, args := range [][]string{
		{"release", writeFirmware(t), "--version-label", "2", "--chip", "esp32", "--require-devices"},
		{"deployment", "create", "--build", "1", "--require-devices"},
	} {
		r := h.run(args...)
		if r.code != ExitError || !strings.Contains(r.stderr, "--require-devices could not check deployment") ||
			!strings.Contains(r.stderr, "did not report compatible_audience_device_count") || strings.Contains(r.stdout, "  devices") {
			t.Fatalf("%s with the flag: %+v", args[0], r)
		}
	}
}

func TestReleaseCountRequestFailure(t *testing.T) {
	h := compatibleHarness(t)
	// The fake's release deployment IDs end in the build number.
	failNextDeploymentGet := func() {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(h.fake.Artifacts)+1)
		h.fake.FailNext["GET /api/v1/projects/"+fakeapi.ProjectID+"/deployments/"+id+"/"] = []int{500}
	}

	failNextDeploymentGet()
	r := h.run("release", writeFirmware(t), "--version-label", "1", "--chip", "esp32", "--json")
	objs := jsonObjects(t, r.stdout)
	if r.code != 0 || len(objs) != 1 || !strings.Contains(r.stderr, "Could not get the number of compatible devices") || !namesTheServerError(r.stderr) {
		t.Fatalf("without the flag: %+v", r)
	}
	if v, ok := objs[0]["compatible_audience_device_count"]; !ok || v != nil {
		t.Fatalf("the count should be null: %+v", objs[0])
	}

	failNextDeploymentGet()
	r = h.run("release", writeFirmware(t), "--version-label", "2", "--chip", "esp32", "--require-devices")
	if r.code != ExitError || !strings.Contains(r.stderr, "--require-devices could not check deployment") || !namesTheServerError(r.stderr) || strings.Contains(r.stdout, "  devices") {
		t.Fatalf("with the flag: %+v", r)
	}
}

// namesTheServerError is whether stderr gives the injected 500 as the cause,
// rather than blaming an old server.
func namesTheServerError(stderr string) bool {
	return strings.Contains(stderr, "500") && strings.Contains(stderr, "injected failure") && !strings.Contains(stderr, "did not report compatible_audience_device_count")
}

// manyDevices puts n compatible devices on stable, plus one incompatible
// and one inactive device that must not be listed.
func manyDevices(h *harness, n int) {
	stable := fakeapi.StableChannelID
	h.fake.Devices = nil
	for i := range n + 2 {
		h.fake.Devices = append(h.fake.Devices, api.Device{
			ID: fmt.Sprintf("dddddddd-0000-4000-8000-%012d", i), DeviceID: fmt.Sprintf("mac-%04d", n+2-i),
			Active: i != 1, ChannelID: &stable,
		})
	}
	h.fake.Incompatible = map[string]bool{h.fake.Devices[0].ID: true}
}

func TestDeploymentDevicesReadsEveryPage(t *testing.T) {
	h := compatibleHarness(t)
	const n = 1003
	manyDevices(h, n)
	h.fake.AddDeployment(depA, api.StateActive, 1, 10)

	r := h.run("deployment", "devices", depA, "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); r.code != 0 || err != nil || len(got) != n {
		t.Fatalf("json: %d devices, %v, %+v", len(got), err, r.stderr)
	}
	ids := make([]string, len(got))
	for i, d := range got {
		ids[i], _ = d["device_id"].(string)
	}
	if !slices.IsSorted(ids) || ids[0] != "mac-0001" || ids[n-1] != fmt.Sprintf("mac-%04d", n) {
		t.Fatalf("devices out of order or wrong: first %s, last %s", ids[0], ids[n-1])
	}
	pages := "GET /api/v1/projects/" + fakeapi.ProjectID + "/deployments/" + depA + "/devices/"
	if c := h.fake.Count(pages); c != 3 {
		t.Fatalf("%d page requests, want 3", c)
	}

	// Text shows 20 rows by default, so one page is enough.
	r = h.run("deployment", "devices", depA)
	if r.code != 0 || !strings.Contains(r.stdout, "mac-0020") || strings.Contains(r.stdout, "mac-0021") || !strings.Contains(r.stderr, fmt.Sprintf("Showing 20 of %d.", n)) {
		t.Fatalf("text: %+v", r)
	}
	if c := h.fake.Count(pages); c != 4 {
		t.Fatalf("text read %d pages, want 1", c-3)
	}
	r = h.run("deployment", "devices", depA, "--json", "--limit", "600")
	if err := json.Unmarshal([]byte(r.stdout), &got); r.code != 0 || err != nil || len(got) != 600 {
		t.Fatalf("json --limit 600: %d devices, %v", len(got), err)
	}
	if c := h.fake.Count(pages); c != 6 {
		t.Fatalf("--limit 600 read %d pages, want 2", c-4)
	}

	// A server that serves more than asked for is still cut to the limit.
	h.fake.DevicesPageItems = func(_, _ int) int { return 500 }
	r = h.run("deployment", "devices", depA)
	if r.code != 0 || !strings.Contains(r.stdout, "mac-0020") || strings.Contains(r.stdout, "mac-0021") || !strings.Contains(r.stderr, fmt.Sprintf("Showing 20 of %d.", n)) {
		t.Fatalf("oversized page: %+v", r)
	}
}

// With --limit 0 nothing was left out on purpose, so a listing that ends up
// shorter than the first page's count gets no "Showing" note.
func TestDeploymentDevicesLimitZeroGetsNoShowingNote(t *testing.T) {
	h := compatibleHarness(t)
	manyDevices(h, 600)
	h.fake.AddDeployment(depA, api.StateActive, 1, 10)
	h.fake.BeforeDevicesPage = func(offset int) {
		if offset > 0 {
			h.fake.Devices = h.fake.Devices[:len(h.fake.Devices)-50]
		}
	}
	r := h.run("deployment", "devices", depA, "--limit", "0")
	if r.code != 0 || strings.Contains(r.stderr, "Showing") || strings.Count(r.stdout, "mac-") != 550 {
		t.Fatalf("rows %d, stderr %q", strings.Count(r.stdout, "mac-"), r.stderr)
	}
}

func TestDeploymentDevicesJSONMatchesDeviceList(t *testing.T) {
	h := compatibleHarness(t)
	h.fake.AddDeployment(depA, api.StateActive, 1, 100)

	r := h.run("deployment", "devices", depA, "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); r.code != 0 || err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	r = h.run("device", "list", "--json")
	var all []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &all); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(d map[string]any) bool { return d["device_id"] == "mac-1" })
	if got[0]["device_id"] != "mac-1" || got[0]["channel"] != "stable" || fmt.Sprint(got[0]) != fmt.Sprint(all[i]) {
		t.Fatalf("deployment devices %v\ndevice list %v", got[0], all[i])
	}
}

func TestDeploymentDevicesWhenNoneAreCompatible(t *testing.T) {
	h := compatibleHarness(t)
	h.fake.Incompatible[fleet()[0].ID] = true
	h.fake.AddDeployment(depA, api.StateActive, 1, 100)

	if r := h.run("deployment", "devices", depA, "--json"); r.code != 0 || strings.TrimSpace(r.stdout) != "[]" {
		t.Fatalf("json: %+v", r)
	}
	if r := h.run("deployment", "devices"); r.code != 0 || !strings.Contains(r.stdout, "No active device in the deployment's audience has metadata compatible") {
		t.Fatalf("text: %+v", r)
	}
}

func TestDeploymentDevicesErrors(t *testing.T) {
	h := compatibleHarness(t)
	h.fake.AddDeployment(depA, api.StateActive, 1, 100)

	if r := h.run("deployment", "devices", depB); r.code != ExitUsage || !strings.Contains(r.stderr, "no deployment "+depB+" in this project") {
		t.Fatalf("unknown deployment: %+v", r)
	}
	h.fake.NoCompatibleCount = true
	if r := h.run("deployment", "devices", depA); r.code != ExitError || !strings.Contains(r.stderr, "the server does not list a deployment's compatible devices") {
		t.Fatalf("older server: %+v", r)
	}
}
