//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

// waitAsync starts deployment wait and returns a channel with its result.
func waitAsync(c *cli, id string, timeout string) <-chan result {
	ch := make(chan result, 1)
	go func() {
		ch <- c.run("", "deployment", "wait", id, "--timeout", timeout, "--interval", "1s", "--json")
	}()
	return ch
}

// outcome reads the status object, which comes before any error object.
func outcome(t *testing.T, r result) string {
	t.Helper()
	var v struct {
		Outcome string `json:"outcome"`
	}
	if err := json.NewDecoder(strings.NewReader(r.stdout)).Decode(&v); err != nil {
		t.Fatalf("bad JSON %q: %v", r.stdout, err)
	}
	return v.Outcome
}

func TestStatusWaitSucceedsWhenDevicesConfirm(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	devices := []*device{newDevice(t), newDevice(t)}
	for _, d := range devices {
		if got := d.check(out.BuildNumber); got != out.DeploymentID {
			t.Fatalf("offered deployment %s, want %s", got, out.DeploymentID)
		}
	}
	done := waitAsync(c, out.DeploymentID, "60s")
	time.Sleep(1500 * time.Millisecond)
	for _, d := range devices {
		d.report(out.DeploymentID, "downloaded", out.BuildNumber)
		d.report(out.DeploymentID, "confirmed", out.BuildNumber)
	}
	r := <-done
	if r.code != 0 || outcome(t, r) != "succeeded" {
		t.Fatalf("%+v", r)
	}
}

func TestStatusWaitFailsOnFailedDevice(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := newDevice(t)
	d.check(out.BuildNumber)
	d.report(out.DeploymentID, "failed", out.BuildNumber)
	r := <-waitAsync(c, out.DeploymentID, "30s")
	if r.code != 4 || outcome(t, r) != "device_failed" {
		t.Fatalf("%+v", r)
	}
}

func TestStatusWaitFailsWhenRollbackAutoPauses(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	d := newDevice(t)
	d.check(out.BuildNumber)
	d.report(out.DeploymentID, "rolled_back", out.BuildNumber)
	r := <-waitAsync(c, out.DeploymentID, "30s")
	if r.code != 4 || outcome(t, r) != "device_failed" {
		t.Fatalf("%+v", r)
	}
	st := c.mustRun("deployment", "show", out.DeploymentID, "--json")
	if !strings.Contains(st.stdout, `"state": "paused"`) {
		t.Fatalf("deployment should be auto-paused: %s", st.stdout)
	}
}

func TestStatusWaitFailsWhenPaused(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	c.mustRun("deployment", "pause", out.DeploymentID)
	r := <-waitAsync(c, out.DeploymentID, "30s")
	if r.code != 4 || outcome(t, r) != "stopped" {
		t.Fatalf("%+v", r)
	}
}

func TestStatusWaitTimesOut(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	start := time.Now()
	r := <-waitAsync(c, out.DeploymentID, "3s")
	if r.code != 5 || outcome(t, r) != "timed_out" {
		t.Fatalf("%+v", r)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatalf("timeout took %s", time.Since(start))
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

func TestStatusWaitFailsWhenCompletedWithNoDevices(t *testing.T) {
	c := ciCLI(t)
	out := release(t, c)
	c.mustRun("deployment", "complete", out.DeploymentID)
	r := <-waitAsync(c, out.DeploymentID, "30s")
	if r.code != 4 || outcome(t, r) != "no_devices" || !strings.Contains(r.stdout+r.stderr, "no device updated") {
		t.Fatalf("%+v", r)
	}
}
