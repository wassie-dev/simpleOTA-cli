//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// device simulates an ESP32 checking in through the dev server's OTA API
// with the device-scoped project token.
type device struct {
	t  *testing.T
	id string
}

func newDevice(t *testing.T) *device { return &device{t: t, id: "e2e-" + uid()} }

func (d *device) post(path string, body any) map[string]any {
	d.t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, env["SIMPLEOTA_DEV_API_URL"]+path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+env["SIMPLEOTA_DEV_DEVICE_TOKEN"])
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("POST %s: %d %s", path, resp.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// check polls once and returns the offered deployment, failing if none.
func (d *device) check(wantBuild int) string {
	d.t.Helper()
	out := d.post("/api/v1/ota/check/", map[string]any{
		"device_id": d.id, "current_build_number": 0, "framework": "esp_idf", "chip_family": "esp32",
	})
	if out["update_available"] != true {
		d.t.Fatalf("device %s was not offered an update: %v", d.id, out)
	}
	if int(out["build_number"].(float64)) != wantBuild {
		d.t.Fatalf("device %s offered build %v, want %d", d.id, out["build_number"], wantBuild)
	}
	return out["deployment_id"].(string)
}

func (d *device) report(deploymentID, event string, build int) {
	d.t.Helper()
	body := map[string]any{"device_id": d.id, "deployment_id": deploymentID, "event": event}
	if event == "confirmed" {
		body["build_number"] = build
	} else if event != "downloaded" {
		body["reason"] = "e2e " + event
	}
	d.post("/api/v1/ota/status/", body)
}

func TestRolloutLifecycle(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c, "--percent", "10")
	id := out.DeploymentID
	type rolloutOut struct {
		Deployment struct {
			State             string `json:"state"`
			RolloutPercentage int    `json:"rollout_percentage"`
		} `json:"deployment"`
		NextStep string `json:"next_step"`
	}
	r := decode[rolloutOut](t, c.mustRun("deployment", "ramp", id, "--percent", "50", "--json").stdout)
	if r.Deployment.RolloutPercentage != 50 {
		t.Fatalf("ramp: %+v", r)
	}
	if res := c.run("", "deployment", "ramp", id, "--percent", "20"); res.code != 1 || !strings.Contains(res.stderr, "downward") {
		t.Fatalf("ramp down: %+v", res)
	}
	r = decode[rolloutOut](t, c.mustRun("deployment", "pause", id, "--json").stdout)
	if r.Deployment.State != "paused" || !strings.Contains(r.NextStep, "new build") {
		t.Fatalf("pause: %+v", r)
	}
	r = decode[rolloutOut](t, c.mustRun("deployment", "resume", id, "--json").stdout)
	if r.Deployment.State != "active" {
		t.Fatalf("resume: %+v", r)
	}
	r = decode[rolloutOut](t, c.mustRun("deployment", "complete", id, "--json").stdout)
	if r.Deployment.State != "completed" {
		t.Fatalf("complete: %+v", r)
	}

	second := release(t, c)
	r = decode[rolloutOut](t, c.mustRun("deployment", "cancel", second.DeploymentID, "--json").stdout)
	if r.Deployment.State != "cancelled" || r.NextStep == "" {
		t.Fatalf("cancel: %+v", r)
	}
}

// outcome reads the outcome deployment show gives.
func outcome(t *testing.T, c *cli, id string) string {
	t.Helper()
	return decode[struct {
		Outcome string `json:"outcome"`
	}](t, c.mustRun("deployment", "show", id, "--json").stdout).Outcome
}

func TestShowSucceedsWhenDevicesConfirm(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	devices := []*device{newDevice(t), newDevice(t)}
	for _, d := range devices {
		if got := d.check(out.BuildNumber); got != out.DeploymentID {
			t.Fatalf("offered deployment %s, want %s", got, out.DeploymentID)
		}
	}
	if got := outcome(t, c, out.DeploymentID); got != "in_progress" {
		t.Fatalf("before confirming: %s", got)
	}
	for _, d := range devices {
		d.report(out.DeploymentID, "downloaded", out.BuildNumber)
		d.report(out.DeploymentID, "confirmed", out.BuildNumber)
	}
	if got := outcome(t, c, out.DeploymentID); got != "succeeded" {
		t.Fatalf("after confirming: %s", got)
	}
}

func TestShowReportsAFailedDevice(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := newDevice(t)
	d.check(out.BuildNumber)
	d.report(out.DeploymentID, "failed", out.BuildNumber)
	if got := outcome(t, c, out.DeploymentID); got != "device_failed" {
		t.Fatal(got)
	}
}

func TestShowReportsARollbackAndTheAutoPause(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := newDevice(t)
	d.check(out.BuildNumber)
	d.report(out.DeploymentID, "rolled_back", out.BuildNumber)
	if got := outcome(t, c, out.DeploymentID); got != "device_failed" {
		t.Fatal(got)
	}
	st := c.mustRun("deployment", "show", out.DeploymentID, "--json")
	if !strings.Contains(st.stdout, `"state": "paused"`) {
		t.Fatalf("deployment should be auto-paused: %s", st.stdout)
	}
}

func TestShowReportsAPausedDeployment(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	c.mustRun("deployment", "pause", out.DeploymentID)
	if got := outcome(t, c, out.DeploymentID); got != "stopped" {
		t.Fatal(got)
	}
}

func TestDeploymentStatusAfterLogoutIsAuthError(t *testing.T) {
	c := newCLI(t)
	if r := c.run(env["SIMPLEOTA_DEV_PROJECT_TOKEN"]+"\n", "login"); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	c.mustRun("logout")
	if r := c.run("", "deployment", "show"); r.code != 3 {
		t.Fatalf("deployment status after logout: %+v", r)
	}
}

func TestShowReportsACompletionBeforeAnyDevice(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	c.mustRun("deployment", "complete", out.DeploymentID)
	if got := outcome(t, c, out.DeploymentID); got != "no_devices" {
		t.Fatal(got)
	}
}

func TestResumeAllDevicesDeployment(t *testing.T) {
	c := ciCLI(t)
	for range 2 {
		d := newDevice(t)
		d.post("/api/v1/ota/check/", map[string]any{"device_id": d.id, "current_build_number": 0, "framework": "esp_idf", "chip_family": "esp32"})
	}
	build := decode[struct {
		BuildNumber int `json:"build_number"`
	}](t, c.mustRun("build", "upload", firmware(t), "--version-label", "e2e-"+uid(), "--chip", "esp32", "--json").stdout)
	created := decode[struct {
		Deployment struct {
			ID string `json:"id"`
		} `json:"deployment"`
	}](t, c.mustRun("deployment", "create", "--build", strconv.Itoa(build.BuildNumber), "--all-devices", "--percent", "50", "--json").stdout)
	id := created.Deployment.ID
	t.Cleanup(func() { cancelDeployment(t, id) })
	c.mustRun("deployment", "pause", id)

	if r := c.run("", "deployment", "resume", id); r.code != 2 || !strings.Contains(r.stderr, "--all-devices") {
		t.Fatalf("resume without confirmation: %+v", r)
	}
	r := decode[struct {
		Deployment struct {
			State string `json:"state"`
		} `json:"deployment"`
	}](t, c.mustRun("deployment", "resume", id, "--all-devices", "--json").stdout)
	if r.Deployment.State != "active" {
		t.Fatalf("resume: %+v", r)
	}
}

func TestResumeAllDevicesDeploymentAfterAutoPause(t *testing.T) {
	c := ciCLI(t)
	d, other := newDevice(t), newDevice(t)
	other.post("/api/v1/ota/check/", map[string]any{"device_id": other.id, "current_build_number": 0, "framework": "esp_idf", "chip_family": "esp32"})
	build := decode[struct {
		BuildNumber int `json:"build_number"`
	}](t, c.mustRun("build", "upload", firmware(t), "--version-label", "e2e-"+uid(), "--chip", "esp32", "--json").stdout)
	created := decode[struct {
		Deployment struct {
			ID string `json:"id"`
		} `json:"deployment"`
	}](t, c.mustRun("deployment", "create", "--build", strconv.Itoa(build.BuildNumber), "--all-devices", "--json").stdout)
	id := created.Deployment.ID
	t.Cleanup(func() { cancelDeployment(t, id) })
	if got := d.check(build.BuildNumber); got != id {
		t.Fatalf("offered deployment %s, want %s", got, id)
	}
	d.report(id, "rolled_back", build.BuildNumber)
	if st := c.mustRun("deployment", "show", id, "--json"); !strings.Contains(st.stdout, `"state": "paused"`) {
		t.Fatalf("the rollback should auto-pause the deployment: %s", st.stdout)
	}
	if r := c.run("", "deployment", "resume", id); r.code != 2 || !strings.Contains(r.stderr, "--all-devices") {
		t.Fatalf("resume without confirmation: %+v", r)
	}
	if r := c.mustRun("deployment", "resume", id, "--all-devices", "--json"); !strings.Contains(r.stdout, `"state": "active"`) {
		t.Fatalf("resume: %+v", r)
	}
}

func TestDeviceTextCannotDriveTheTerminal(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := newDevice(t)
	id := d.check(out.BuildNumber)
	reason := "e2e \x1b[31mRED\x1b]0;TITLE\x07\rOVERWRITE\xc2\x9b31m\n2nd-line"
	d.post("/api/v1/ota/status/", map[string]any{"device_id": d.id, "deployment_id": id, "event": "failed", "reason": reason})
	r := c.run("", "deployment", "show", id)
	for _, b := range []string{"\x1b", "\x07", "\r", "\xc2\x9b", "\n2nd-line"} {
		if strings.Contains(r.stdout, b) {
			t.Fatalf("show printed %q: %q", b, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, `\x1b[31mRED`) {
		t.Fatalf("reason not shown escaped: %q", r.stdout)
	}
}

func TestHostileDeviceIDIsNotPrinted(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := &device{t: t, id: "e2e-" + uid() + "\x1b[2J\x1b]0;PWNED\x07\rFORGED\nError: none"}
	id := d.check(out.BuildNumber)
	d.report(id, "failed", out.BuildNumber)
	r := c.run("", "deployment", "show", id)
	if strings.ContainsAny(r.stdout+r.stderr, "\x1b\x07\r") || strings.Contains(r.stdout+r.stderr, "FORGED") || !strings.Contains(r.stdout, "failure  device ") {
		t.Errorf("%q %q", r.stdout, r.stderr)
	}
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

func TestRequireDevicesCountsTheTargetedDevices(t *testing.T) {
	c := ciCLI(t)
	channel := "e2e-" + uid()
	c.mustRun("channel", "create", channel)

	r := c.run("", "release", firmware(t), "--version-label", "e2e-"+uid(), "--chip", "esp32", "--channel", channel, "--require-devices", "--json")
	objs := jsonObjects(t, r.stdout)
	if len(objs) > 0 {
		if id, ok := objs[0]["deployment_id"].(string); ok {
			t.Cleanup(func() { cancelDeployment(t, id) })
		}
	}
	if r.code != 4 || len(objs) != 2 || objs[0]["targeted_devices"] != 0.0 || objs[0]["state"] != "active" || objs[1]["exit_code"] != 4.0 {
		t.Fatalf("empty channel: %+v", r)
	}

	d := newDevice(t)
	d.post("/api/v1/ota/check/", map[string]any{"device_id": d.id, "current_build_number": 0, "framework": "esp_idf", "chip_family": "esp32"})
	c.mustRun("device", "update", d.id, "--channel", channel)
	out := release(t, c, "--channel", channel, "--require-devices")
	if out.TargetedDevices == nil || *out.TargetedDevices != 1 {
		t.Fatalf("one device on the channel: %+v", out)
	}
	if got := d.check(out.BuildNumber); got != out.DeploymentID {
		t.Fatalf("the counted device was offered %s, want %s", got, out.DeploymentID)
	}

	c.mustRun("device", "update", d.id, "--state", "inactive")
	build := strconv.Itoa(out.BuildNumber)
	r = c.run("", "deployment", "create", "--build", build, "--channel", channel, "--require-devices", "--json")
	objs = jsonObjects(t, r.stdout)
	if len(objs) > 0 {
		if dep, ok := objs[0]["deployment"].(map[string]any); ok {
			id, _ := dep["id"].(string)
			t.Cleanup(func() { cancelDeployment(t, id) })
		}
	}
	if r.code != 4 || len(objs) != 2 || objs[0]["targeted_devices"] != 0.0 {
		t.Fatalf("inactive device: %+v", r)
	}
}
