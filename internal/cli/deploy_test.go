package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

const depA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestRollout(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 10)

	if r := h.run("deployment", "ramp", depA, "--percent", "50"); r.code != 0 || !strings.Contains(r.stdout, "at 50%") {
		t.Fatalf("ramp: %+v", r)
	}
	if r := h.run("deployment", "ramp", depA, "--percent", "20"); r.code != ExitError || !strings.Contains(r.stderr, "downward") {
		t.Fatalf("ramp down: %+v", r)
	}
	r := h.run("deployment", "pause", depA, "--json")
	if r.code != 0 || !strings.Contains(r.stdout, `"state": "paused"`) || !strings.Contains(r.stdout, "new build") {
		t.Fatalf("pause: %+v", r)
	}
	if r := h.run("deployment", "resume", depA); r.code != 0 {
		t.Fatalf("resume: %+v", r)
	}
	if r := h.run("deployment", "complete", depA); r.code != 0 || !strings.Contains(r.stdout, "completed") {
		t.Fatalf("complete: %+v", r)
	}
	for _, args := range [][]string{
		{"deployment", "rollback", depA},
		{"deployment", "ramp", depA},
		{"deployment", "pause", depA, "--percent", "10"},
		{"deployment", "pause", "not-a-uuid"},
		{"deployment", "pause"},
	} {
		if r := h.run(args...); r.code != ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestStatusWaitExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		status string
		want   int
	}{
		{name: "success", state: api.StateActive, status: "confirmed", want: ExitOK},
		{name: "device failed", state: api.StateActive, status: "failed", want: ExitDeployment},
		{name: "rolled back", state: api.StatePaused, status: "rolled_back", want: ExitDeployment},
		{name: "paused", state: api.StatePaused, status: "confirmed", want: ExitDeployment},
		{name: "timeout", state: api.StateActive, status: "downloaded", want: ExitTimeout},
		{name: "completed with no devices", state: api.StateCompleted, want: ExitDeployment},
		{name: "no devices yet", state: api.StateActive, want: ExitTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.login(fakeapi.ProjectToken)
			h.fake.AddDeployment(depA, tt.state, 5, 100)
			if tt.status != "" {
				h.fake.SetStates(depA, api.DeviceState{DeviceID: "d1", FinalStatus: tt.status, AssignedToRollout: true})
			}
			r := h.run("deployment", "wait", depA, "--timeout", "30s", "--json")
			if r.code != tt.want {
				t.Fatalf("exit = %d, want %d: %+v", r.code, tt.want, r)
			}
			dec := json.NewDecoder(strings.NewReader(r.stdout))
			var status struct {
				Outcome string `json:"outcome"`
			}
			if err := dec.Decode(&status); err != nil || status.Outcome == "" {
				t.Fatalf("stdout is not a status object: %s", r.stdout)
			}
		})
	}
}

func TestStatusDefaultsToLatestDeployment(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment("bbbbbbbb-bbbb-4bbb-8bbb-000000000001", api.StateCompleted, 1, 100)
	h.fake.AddDeployment("bbbbbbbb-bbbb-4bbb-8bbb-000000000002", api.StateActive, 2, 100)
	r := h.run("deployment", "show")
	if r.code != 0 || !strings.Contains(r.stdout, "000000000002") {
		t.Fatalf("%+v", r)
	}
}

func TestJSONErrorOutput(t *testing.T) {
	h := newHarness(t)
	r := h.run("deployment", "show", "--json")
	var v struct {
		Error    string `json:"error"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.ExitCode != ExitAuth || r.code != ExitAuth {
		t.Fatalf("%+v", r)
	}
}

func TestProjectSlugFlag(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = fakeapi.PersonalToken
	h.fake.Projects = append(h.fake.Projects, api.Project{ID: fakeapi.OtherID, Slug: "lab"})
	if r := h.run("deployment", "show"); r.code != ExitUsage {
		t.Fatalf("ambiguous project: %+v", r)
	}
	if r := h.run("deployment", "show", "--project", "nope"); r.code != ExitUsage {
		t.Fatalf("unknown slug: %+v", r)
	}
	h.fake.AddDeployment(depA, api.StateActive, 1, 100)
	if r := h.run("deployment", "show", "--project", "fleet"); r.code != 0 {
		t.Fatalf("slug: %+v", r)
	}
}

func TestStatusNeedsLogin(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.run("logout")
	if r := h.run("deployment", "show"); r.code != ExitAuth || !strings.Contains(r.stderr, "not logged in") {
		t.Fatalf("%+v", r)
	}
}

func TestUnknownDeploymentIsExplained(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	for _, args := range [][]string{{"deployment", "show", depA}, {"deployment", "pause", depA}, {"deployment", "wait", depA}} {
		r := h.run(args...)
		if r.code != ExitUsage || !strings.Contains(r.stderr, "no deployment aaaaaaaa") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestRejectedTokenIsExplained(t *testing.T) {
	h := newHarness(t)
	h.env["SIMPLEOTA_TOKEN"] = "soto_proj_unknownunknownunknown"
	r := h.run("deployment", "show")
	if r.code != ExitAuth || !strings.Contains(r.stderr, "rejected the token (401)") {
		t.Fatalf("%+v", r)
	}
}

func TestGroupsRejectUnknownSubcommands(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"deployment", "rollback", depA}, {"keys", "rotate"}} {
		if r := h.run(args...); r.code != ExitUsage || !strings.Contains(r.stderr, "unknown command") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := h.run("deployment"); r.code != 0 || !strings.Contains(r.stdout, "Commands:") {
		t.Errorf("bare group should print help: %+v", r)
	}
}

func TestResumeAllDevicesNeedsConfirmation(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.Devices = []api.Device{{ID: "dddddddd-dddd-4ddd-8ddd-000000000001"}, {ID: "dddddddd-dddd-4ddd-8ddd-000000000002"}}
	h.run("build", "upload", writeFirmware(t), "--version-label", "1", "--chip", "esp32")
	if r := h.run("deployment", "create", "--build", "1", "--all-devices", "--percent", "50"); r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	id := h.fake.Deployments[0].ID
	if r := h.run("deployment", "pause", id); r.code != 0 {
		t.Fatalf("pause: %+v", r)
	}
	r := h.run("deployment", "resume", id)
	if r.code != ExitUsage || !strings.Contains(r.stderr, "resume "+id+" --all-devices") || h.fake.Deployments[0].State != api.StatePaused {
		t.Fatalf("resume without confirmation: %+v", r)
	}
	if r := h.run("deployment", "resume", id, "--all-devices", "--json"); r.code != 0 || !strings.Contains(r.stdout, `"state": "active"`) {
		t.Fatalf("confirmed resume: %+v", r)
	}

	h.fake.AddDeployment(depA, api.StatePaused, 1, 10)
	if r := h.run("deployment", "resume", depA, "--all-devices"); r.code != ExitUsage || !strings.Contains(r.stderr, "channels or groups") {
		t.Fatalf("--all-devices on a channel deployment: %+v", r)
	}
	if r := h.run("deployment", "resume", depA); r.code != 0 {
		t.Fatalf("channel resume: %+v", r)
	}
}

func TestResumeShowsOtherRefusalsAsTheyAre(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StatePaused, 1, 100)
	h.fake.Deployments[0].TargetChannelIDs = nil
	r := h.run("deployment", "resume", depA)
	if r.code != ExitError || !strings.Contains(r.stderr, "with no devices") || strings.Contains(r.stderr, "--all-devices") {
		t.Fatalf("%+v", r)
	}
}

// hostile is device-reported text that tries to recolour, retitle,
// overwrite and forge lines on the operator's terminal.
const hostile = "probe \x1b[31mRED\x1b[0m \x1b]0;TITLE\x07 \rOVERWRITE \bX \xc2\x9b31mC1\n2nd-line"

func hasTerminalControl(s string) bool {
	for _, r := range s {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			return true
		}
	}
	return false
}

func TestServerTextCannotDriveTheTerminal(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	h.fake.SetStates(depA, api.DeviceState{DeviceID: "dev\x1b[2J", FinalStatus: api.DeviceFailed, AssignedToRollout: true, StatusReason: hostile})
	h.fake.Devices = []api.Device{{ID: "dddddddd-dddd-4ddd-8ddd-000000000001", DeviceID: hostile, Active: true}}

	for _, args := range [][]string{{"deployment", "show", depA}, {"deployment", "wait", depA}, {"device", "list"}} {
		r := h.run(args...)
		if hasTerminalControl(r.stdout) || hasTerminalControl(r.stderr) || strings.Contains(r.stdout, "\n2nd-line") {
			t.Errorf("%v printed control characters: %q %q", args, r.stdout, r.stderr)
		}
	}

	r := h.run("deployment", "show", depA, "--json")
	if strings.Contains(r.stdout, "\xc2\x9b") {
		t.Fatalf("raw C1 in JSON: %q", r.stdout)
	}
	var v struct {
		Failures []struct {
			Reason string `json:"reason"`
		} `json:"failures"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || len(v.Failures) != 1 || v.Failures[0].Reason != hostile {
		t.Fatalf("JSON must keep the exact reason: %+v %v", v, err)
	}
}

func TestWaitErrorShowsServerIDsEscaped(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment("dep\x1b]0;PWNED\x07\nError: forged", api.StatePaused, 5, 100)
	r := h.run("deployment", "wait")
	if r.code != ExitDeployment || hasTerminalControl(r.stdout+r.stderr) || strings.Contains(r.stderr, "\nError: forged") {
		t.Fatalf("%+v", r)
	}
}

func TestFailuresNameTheReportedDeviceID(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	uuid := "dddddddd-dddd-4ddd-8ddd-000000000001"
	h.fake.Devices = []api.Device{{ID: uuid, DeviceID: "lamp-kitchen"}}
	h.fake.SetStates(depA, api.DeviceState{DeviceID: uuid, FinalStatus: api.DeviceFailed, AssignedToRollout: true, StatusReason: "boom"})
	r := h.run("deployment", "wait", depA)
	if r.code != ExitDeployment || !strings.Contains(r.stdout, "device lamp-kitchen failed: boom") || !strings.Contains(r.stderr, "device lamp-kitchen failed") {
		t.Fatalf("%+v", r)
	}
	r = h.run("deployment", "show", depA, "--json")
	if !strings.Contains(r.stdout, `"device_id": "`+uuid+`"`) || !strings.Contains(r.stdout, `"device": "lamp-kitchen"`) {
		t.Fatalf("%+v", r)
	}
}

func TestStaffWriteToAnotherOwnersProjectIsExplained(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.StaffToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	for _, args := range [][]string{{"channel", "create", "beta"}, {"deployment", "pause", depA}} {
		r := h.run(append(args, "--project", fakeapi.ProjectID)...)
		if r.code != ExitAuth || !strings.Contains(r.stderr, "can read the project but not change it") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := h.run("channel", "list", "--project", fakeapi.ProjectID); r.code != 0 {
		t.Fatalf("staff read: %+v", r)
	}
}

func TestParseErrorsFollowJSON(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"build", "list", "--bogus", "--json"}, {"--json", "nonsense"},
		{"build", "list", "--bogus", "--json=1"}, {"build", "list", "--bogus", "--json=t"}, {"build", "list", "--bogus", "--json=TRUE"},
		{"build", "list", "--json=false", "--bogus", "--json"},
	} {
		r := h.run(args...)
		var v struct {
			Error    string `json:"error"`
			ExitCode int    `json:"exit_code"`
		}
		if r.code != ExitUsage || r.stderr != "" || json.Unmarshal([]byte(r.stdout), &v) != nil || v.ExitCode != ExitUsage || v.Error == "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestProjectEnvPrefixOnlyOnProjectErrors(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	h.env["SIMPLEOTA_PROJECT"] = "nope"
	if r := h.run("build", "list"); r.code != ExitUsage || !strings.Contains(r.stderr, "SIMPLEOTA_PROJECT: no project with slug") {
		t.Fatalf("unknown project: %+v", r)
	}
	h.fake.FailNext["/api/v1/projects/"] = []int{500}
	if r := h.run("build", "list"); r.code != ExitError || strings.Contains(r.stderr, "SIMPLEOTA_PROJECT") {
		t.Fatalf("server error: %+v", r)
	}
}

func TestFailuresDoNotPrintAHostileDeviceID(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	uuid := "dddddddd-dddd-4ddd-8ddd-000000000001"
	hostileID := "lamp\x1b[2J\x1b]0;PWNED\x07\rFORGED\nError: none"
	h.fake.Devices = []api.Device{{ID: uuid, DeviceID: hostileID}}
	h.fake.SetStates(depA, api.DeviceState{DeviceID: uuid, FinalStatus: api.DeviceFailed, AssignedToRollout: true})
	for _, args := range [][]string{{"deployment", "wait", depA}, {"deployment", "show", depA}} {
		r := h.run(args...)
		if strings.ContainsAny(r.stdout+r.stderr, "\x1b\x07\r") || strings.Contains(r.stdout+r.stderr, "FORGED") || !strings.Contains(r.stdout, "device "+uuid) {
			t.Errorf("%v: %q %q", args, r.stdout, r.stderr)
		}
	}
	r := h.run("deployment", "show", depA, "--json")
	var v struct {
		Failures []struct {
			Device string `json:"device"`
		} `json:"failures"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || len(v.Failures) != 1 || v.Failures[0].Device != hostileID {
		t.Fatalf("JSON keeps the exact ID: %+v %v", v, err)
	}
}

func TestStaffCanChangeTheirOwnProject(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.StaffToken)
	h.fake.StaffProjects = []api.Project{h.fake.Projects[0]}
	if r := h.run("channel", "create", "beta", "--project", fakeapi.ProjectID); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestParseErrorsStayTextWithoutJSON(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"build", "list", "--bogus", "--json=false"}, {"build", "list", "--bogus", "--json=0"},
		{"--json", "--json=f", "build", "list", "--bogus"}, {"build", "list", "--bogus", "--", "--json"},
	} {
		if r := h.run(args...); r.code != ExitUsage || r.stdout != "" || !strings.HasPrefix(r.stderr, "Error: ") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestWaitTimesOutWhenNoPollSucceeds(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	path := "/api/v1/projects/" + fakeapi.ProjectID + "/deployments/" + depA + "/states/"
	for range 100 {
		h.fake.FailNext[path] = append(h.fake.FailNext[path], 503)
	}
	r := h.run("deployment", "wait", depA, "--timeout", "30s", "--json")
	if r.code != ExitTimeout || !strings.Contains(r.stdout, "could not be read before the timeout") || !strings.Contains(r.stdout, "503") {
		t.Fatalf("%+v", r)
	}
}
