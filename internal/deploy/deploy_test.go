package deploy

import (
	"context"
	"errors"
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

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

func newWaiter(t *testing.T, fake *fakeapi.Server, clock *fakeClock, onSleep func(n int)) Waiter {
	c := api.New(fake.URL, fakeapi.ProjectToken, "test", api.WithSleep(func(context.Context, time.Duration) error { return nil }))
	n := 0
	return Waiter{
		API: c, ProjectID: fakeapi.ProjectID, Interval: 10 * time.Second, Timeout: time.Minute,
		Now: clock.Now,
		Sleep: func(ctx context.Context, d time.Duration) error {
			n++
			if onSleep != nil {
				onSleep(n)
			}
			return clock.Sleep(ctx, d)
		},
	}
}

const depID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestWaitSucceedsWhenDevicesConfirm(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	fake.SetStates(depID, st("offered"))
	clock := &fakeClock{now: time.Unix(0, 0)}
	var progress int
	w := newWaiter(t, fake, clock, func(n int) {
		if n == 2 {
			fake.SetStates(depID, st("confirmed"))
		}
	})
	w.OnProgress = func(Summary) { progress++ }
	s, o, err := w.Wait(context.Background(), depID)
	if err != nil || o != OutcomeSucceeded || s.Counts.Confirmed != 1 {
		t.Fatalf("o=%s err=%v counts=%+v", o, err, s.Counts)
	}
	if progress != 2 {
		t.Fatalf("progress callbacks = %d, want 2 (only on change)", progress)
	}
}

func TestWaitFailsOnDeviceFailure(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	fake.SetStates(depID, st("failed"))
	_, o, err := newWaiter(t, fake, &fakeClock{}, nil).Wait(context.Background(), depID)
	if err != nil || o != OutcomeDeviceFailed {
		t.Fatalf("o=%s err=%v", o, err)
	}
}

func TestWaitStopsWhenPaused(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	w := newWaiter(t, fake, &fakeClock{}, func(int) { fake.SetDeploymentState(depID, api.StatePaused) })
	_, o, err := w.Wait(context.Background(), depID)
	if err != nil || o != OutcomeStopped {
		t.Fatalf("o=%s err=%v", o, err)
	}
}

func TestWaitTimesOut(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	clock := &fakeClock{now: time.Unix(0, 0)}
	w := newWaiter(t, fake, clock, nil)
	_, o, err := w.Wait(context.Background(), depID)
	if err != nil || o != OutcomeTimedOut {
		t.Fatalf("o=%s err=%v", o, err)
	}
	if got := clock.now.Sub(time.Unix(0, 0)); got != time.Minute {
		t.Fatalf("waited %s, want exactly the timeout", got)
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

func TestWaitSurvivesTransientPollFailures(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	fake.SetStates(depID, st("confirmed"))
	fake.FailNext["/api/v1/projects/"+fakeapi.ProjectID+"/deployments/"+depID+"/"] = []int{503, 503, 503}
	var pollErrors int
	w := newWaiter(t, fake, &fakeClock{}, nil)
	w.OnPollError = func(error) { pollErrors++ }
	_, o, err := w.Wait(context.Background(), depID)
	if err != nil || o != OutcomeSucceeded || pollErrors != 1 {
		t.Fatalf("o=%s err=%v pollErrors=%d", o, err, pollErrors)
	}
}

func TestWaitStopsOnPermanentError(t *testing.T) {
	fake := fakeapi.New(t)
	_, _, err := newWaiter(t, fake, &fakeClock{}, nil).Wait(context.Background(), depID)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode != 404 {
		t.Fatalf("err = %v, want the 404", err)
	}
}

func TestWaitReturnsErrorWhenNoPollEverSucceeded(t *testing.T) {
	fake := fakeapi.New(t)
	fake.AddDeployment(depID, api.StateActive, 3, 100)
	path := "/api/v1/projects/" + fakeapi.ProjectID + "/deployments/" + depID + "/"
	for i := 0; i < 30; i++ {
		fake.FailNext[path] = append(fake.FailNext[path], 503)
	}
	_, o, err := newWaiter(t, fake, &fakeClock{now: time.Unix(0, 0)}, nil).Wait(context.Background(), depID)
	if err == nil || o != OutcomeInProgress {
		t.Fatalf("o=%s err=%v, want the poll error", o, err)
	}
}
