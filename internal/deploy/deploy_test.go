package deploy

import (
	"context"
	"fmt"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func st(status string) api.DeviceState {
	return api.DeviceState{DeviceID: "dev-" + status, FinalStatus: status, AssignedToRollout: true, Eligible: true}
}

func TestEvaluate(t *testing.T) {
	active := api.Deployment{State: api.StateActive}
	tests := []struct {
		name   string
		dep    api.Deployment
		states []api.DeviceState
		want   Outcome
	}{
		{name: "nobody offered yet", dep: active, want: OutcomeInProgress},
		{name: "one in progress", dep: active, states: []api.DeviceState{st("confirmed"), st("downloaded")}, want: OutcomeInProgress},
		{name: "all confirmed", dep: active, states: []api.DeviceState{st("confirmed"), st("confirmed")}, want: OutcomeSucceeded},
		{name: "a failure", dep: active, states: []api.DeviceState{st("confirmed"), st("failed")}, want: OutcomeDeviceFailed},
		{name: "a rollback", dep: active, states: []api.DeviceState{st("rolled_back")}, want: OutcomeDeviceFailed},
		{name: "paused", dep: api.Deployment{State: api.StatePaused}, states: []api.DeviceState{st("confirmed")}, want: OutcomeStopped},
		{name: "cancelled", dep: api.Deployment{State: api.StateCancelled}, want: OutcomeStopped},
		{name: "draft", dep: api.Deployment{State: api.StateDraft}, want: OutcomeStopped},
		{name: "completed", dep: api.Deployment{State: api.StateCompleted}, states: []api.DeviceState{st("offered")}, want: OutcomeSucceeded},
		{name: "completed with no device offered", dep: api.Deployment{State: api.StateCompleted}, want: OutcomeNoDevices},
		{name: "failure beats completed", dep: api.Deployment{State: api.StateCompleted}, states: []api.DeviceState{st("failed")}, want: OutcomeDeviceFailed},
		{name: "unassigned pending row ignored", dep: active, states: []api.DeviceState{st("confirmed"), {FinalStatus: "pending"}}, want: OutcomeSucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Evaluate(Summarize(tt.dep, tt.states)); got != tt.want {
				t.Fatalf("Evaluate = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSummarizeCounts(t *testing.T) {
	s := Summarize(api.Deployment{}, []api.DeviceState{
		st("offered"), st("download_started"), st("confirmed"), st("failed"),
		{DeviceID: "d9", FinalStatus: "rolled_back", StatusReason: "task_wdt"},
	})
	want := Counts{Offered: 5, InProgress: 2, Confirmed: 1, Failed: 1, RolledBack: 1}
	if s.Counts != want {
		t.Fatalf("counts = %+v, want %+v", s.Counts, want)
	}
	if len(s.Failures) != 2 || s.Failures[1].Reason != "task_wdt" {
		t.Fatalf("failures = %+v", s.Failures)
	}
}

func TestBuildTransition(t *testing.T) {
	p := func(n int) *int { return &n }
	tests := []struct {
		action  string
		percent *int
		wantErr bool
	}{
		{action: "ramp", percent: p(50)},
		{action: "ramp", wantErr: true},
		{action: "ramp", percent: p(101), wantErr: true},
		{action: "pause"},
		{action: "pause", percent: p(10), wantErr: true},
		{action: "resume"},
		{action: "complete"},
		{action: "cancel"},
		{action: "rollback", wantErr: true},
		{action: "start", wantErr: true},
	}
	for _, tt := range tests {
		tr, err := BuildTransition(tt.action, tt.percent)
		if (err != nil) != tt.wantErr {
			t.Errorf("BuildTransition(%q) err = %v", tt.action, err)
			continue
		}
		if err == nil && (tr.Action != tt.action || (tt.action == "ramp") != (tr.RolloutPercentage != nil)) {
			t.Errorf("BuildTransition(%q) = %+v", tt.action, tr)
		}
	}
}

func TestLatest(t *testing.T) {
	fake := fakeapi.New(t)
	if _, err := Latest(context.Background(), api.New(fake.URL, fakeapi.ProjectToken, "t"), fakeapi.ProjectID); err == nil {
		t.Fatal("want error when there are no deployments")
	}
	fake.AddDeployment("11111111-0000-4000-8000-000000000001", api.StateActive, 1, 100)
	fake.AddDeployment("11111111-0000-4000-8000-000000000002", api.StateActive, 2, 100)
	d, err := Latest(context.Background(), api.New(fake.URL, fakeapi.ProjectToken, "t"), fakeapi.ProjectID)
	if err != nil || d.ArtifactBuildNumber != 2 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestCompatibleDevicesReadsEveryPage(t *testing.T) {
	fake := fakeapi.New(t)
	d := fake.AddDeployment("11111111-0000-4000-8000-000000000001", api.StateActive, 1, 10)
	stable := fakeapi.StableChannelID
	const n = 2*devicesPageSize + 3
	for i := range n + 2 {
		fake.Devices = append(fake.Devices, api.Device{ID: fmt.Sprintf("dev-%04d", i), DeviceID: fmt.Sprintf("mac-%04d", n+1-i), Active: true, ChannelID: &stable})
	}
	fake.Incompatible["dev-0000"] = true
	fake.Devices[1].Active = false

	got, err := CompatibleDevices(context.Background(), api.New(fake.URL, fakeapi.ProjectToken, "t"), fakeapi.ProjectID, d.ID)
	if err != nil || len(got) != n {
		t.Fatalf("got %d devices, %v; want %d", len(got), err, n)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].DeviceID >= got[i].DeviceID {
			t.Fatalf("not in device_id order at %d: %s, %s", i, got[i-1].DeviceID, got[i].DeviceID)
		}
	}
	path := "GET /api/v1/projects/" + fakeapi.ProjectID + "/deployments/" + d.ID + "/devices/"
	if c := fake.Count(path); c != 3 {
		t.Fatalf("%d requests, want 3", c)
	}
}
