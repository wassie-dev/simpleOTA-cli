package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

// pagedFake serves n compatible devices on stable, plus an incompatible and
// an inactive one, for one active deployment.
func pagedFake(t *testing.T, n int) (*fakeapi.Server, api.Deployment) {
	fake := fakeapi.New(t)
	d := fake.AddDeployment("11111111-0000-4000-8000-000000000001", api.StateActive, 1, 10)
	stable := fakeapi.StableChannelID
	for i := range n + 2 {
		fake.Devices = append(fake.Devices, api.Device{ID: fmt.Sprintf("dev-%04d", i), DeviceID: fmt.Sprintf("mac-%04d", n+2-i), Active: true, ChannelID: &stable})
	}
	fake.Incompatible["dev-0000"] = true
	fake.Devices[1].Active = false
	return fake, d
}

func compatibleDevices(t *testing.T, fake *fakeapi.Server, d api.Deployment, max int) ([]api.Device, int, error) {
	t.Helper()
	// The deadline turns a pager that never stops into a failure.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return CompatibleDevices(ctx, api.New(fake.URL, fakeapi.ProjectToken, "t"), fakeapi.ProjectID, d.ID, max)
}

func pageRequests(fake *fakeapi.Server, d api.Deployment) int {
	return fake.Count("GET /api/v1/projects/" + fakeapi.ProjectID + "/deployments/" + d.ID + "/devices/")
}

// wantAll checks got is the n devices pagedFake serves, once each, in
// device_id order.
func wantAll(t *testing.T, got []api.Device, n int) {
	t.Helper()
	if len(got) != n {
		t.Fatalf("got %d devices, want %d", len(got), n)
	}
	for i, dev := range got {
		if want := fmt.Sprintf("mac-%04d", i+1); dev.DeviceID != want {
			t.Fatalf("device %d is %s, want %s", i, dev.DeviceID, want)
		}
	}
}

func TestCompatibleDevicesReadsEveryPage(t *testing.T) {
	const n = 2*devicesPageSize + 3
	fake, d := pagedFake(t, n)
	got, total, err := compatibleDevices(t, fake, d, 0)
	if err != nil || total != n {
		t.Fatalf("total %d, %v", total, err)
	}
	wantAll(t, got, n)
	if c := pageRequests(fake, d); c != 3 {
		t.Fatalf("%d requests, want 3", c)
	}
}

// A page can be short while more devices follow, for instance when the
// server leaves out a device deleted while it built the page.
func TestCompatibleDevicesContinuesAfterAShortPage(t *testing.T) {
	const n = 2*devicesPageSize + 3
	fake, d := pagedFake(t, n)
	fake.DevicesPageItems = func(offset, limit int) int {
		if offset == devicesPageSize {
			return 200
		}
		return limit
	}
	got, _, err := compatibleDevices(t, fake, d, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, n)
}

func TestCompatibleDevicesFollowsALowerServerMaximum(t *testing.T) {
	const n = 250
	fake, d := pagedFake(t, n)
	fake.DevicesPageItems = func(_, limit int) int { return min(limit, 100) }
	got, _, err := compatibleDevices(t, fake, d, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, n)
	if c := pageRequests(fake, d); c != 3 {
		t.Fatalf("%d requests, want 3", c)
	}
}

// A device that joins before the offset between two pages shifts the second
// page back by one, so its first device was already on the first page.
func TestCompatibleDevicesListsAShiftedDeviceOnce(t *testing.T) {
	const n = devicesPageSize + 10
	fake, d := pagedFake(t, n)
	stable := fakeapi.StableChannelID
	fake.BeforeDevicesPage = func(offset int) {
		if offset == devicesPageSize {
			fake.Devices = append(fake.Devices, api.Device{ID: "dev-new", DeviceID: "mac-0000", Active: true, ChannelID: &stable})
		}
	}
	got, _, err := compatibleDevices(t, fake, d, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, n)
}

func TestCompatibleDevicesStopsOnAnEmptyPage(t *testing.T) {
	const n = 2*devicesPageSize + 3
	fake, d := pagedFake(t, n)
	fake.DevicesPageItems = func(offset, limit int) int {
		if offset > 0 {
			return 0
		}
		return limit
	}
	if _, _, err := compatibleDevices(t, fake, d, 0); err == nil || !strings.Contains(err.Error(), "no devices at offset 500 of 1003") {
		t.Fatalf("err = %v", err)
	}
	if c := pageRequests(fake, d); c != 2 {
		t.Fatalf("%d requests, want 2", c)
	}
}

func TestCompatibleDevicesReadsOnlyWhatMaxNeeds(t *testing.T) {
	const n = 2*devicesPageSize + 3
	fake, d := pagedFake(t, n)
	got, total, err := compatibleDevices(t, fake, d, 20)
	if err != nil || total != n || len(got) != 20 || got[19].DeviceID != "mac-0020" {
		t.Fatalf("got %d devices, total %d, %v", len(got), total, err)
	}
	if c := pageRequests(fake, d); c != 1 {
		t.Fatalf("%d requests, want 1", c)
	}
}
