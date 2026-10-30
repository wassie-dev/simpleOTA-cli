package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func TestBuildListAndShow(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	if r := h.run("build", "list"); r.code != 0 || !strings.Contains(r.stdout, "No builds yet") {
		t.Fatalf("empty: %+v", r)
	}
	for i := 0; i < 2; i++ {
		if r := h.run("release", writeFirmware(t), "--version-label", "1.0."+string(rune('0'+i)), "--chip", "esp32"); r.code != 0 {
			t.Fatalf("release: %+v", r)
		}
	}
	r := h.run("builds", "list")
	if r.code != 0 || strings.Index(r.stdout, "1.0.1") > strings.Index(r.stdout, "1.0.0") {
		t.Fatalf("newest first: %+v", r)
	}
	r = h.run("build", "list", "--json")
	var builds []struct {
		BuildNumber int    `json:"build_number"`
		Chip        string `json:"chip"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &builds); err != nil || len(builds) != 2 || builds[0].BuildNumber != 2 || builds[0].Chip != "esp32" {
		t.Fatalf("--json: %+v %v", r, err)
	}
	if r := h.run("build", "show", "1"); r.code != 0 || !strings.Contains(r.stdout, "Build 1") || !strings.Contains(r.stdout, "1.0.0") {
		t.Fatalf("show: %+v", r)
	}
	for _, ref := range []string{"9", "x"} {
		if r := h.run("build", "show", ref); r.code != ExitUsage {
			t.Fatalf("show %s: %+v", ref, r)
		}
	}
}

func TestDeviceListAndShow(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	beta := "44444444-4444-4444-8444-444444444444"
	h.fake.Channels = append(h.fake.Channels, api.Channel{ID: beta, Name: "beta"})
	h.fake.Groups = []api.Group{{ID: "g1", Name: "kitchen"}}
	stable, build := fakeapi.StableChannelID, 7
	seen := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	h.fake.Devices = []api.Device{
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000001", DeviceID: "lamp-1", ChipFamily: "esp32", ChannelID: &stable, Active: true, CheckState: "checking_in", CurrentBuildNumber: &build, CurrentVersionLabel: "1.0.0", LastSeen: &seen, GroupIDs: []string{"g1"}},
		{ID: "dddddddd-dddd-4ddd-8ddd-000000000002", DeviceID: "lamp-2", ChipFamily: "esp32", ChannelID: &beta, Active: false},
	}
	r := h.run("device", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "lamp-1") || !strings.Contains(r.stdout, "7 (1.0.0)") || !strings.Contains(r.stdout, "2026-10-30 09:00") || !strings.Contains(r.stdout, "inactive") {
		t.Fatalf("list: %+v", r)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--channel", "beta"}, "lamp-2"},
		{[]string{"--group", "kitchen"}, "lamp-1"},
		{[]string{"--state", "active"}, "lamp-1"},
	} {
		r := h.run(append([]string{"devices", "list", "--json"}, tc.args...)...)
		var ds []struct {
			DeviceID string `json:"device_id"`
			Channel  string `json:"channel"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &ds); err != nil || len(ds) != 1 || ds[0].DeviceID != tc.want || ds[0].Channel == "" {
			t.Fatalf("%v: %+v %v", tc.args, r, err)
		}
	}
	for _, args := range [][]string{{"--channel", "nope"}, {"--group", "nope"}, {"--state", "asleep"}} {
		if r := h.run(append([]string{"device", "list"}, args...)...); r.code != ExitUsage {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	for _, ref := range []string{"lamp-1", "dddddddd-dddd-4ddd-8ddd-000000000001"} {
		r := h.run("device", "show", ref)
		if r.code != 0 || !strings.Contains(r.stdout, "Device lamp-1") || !strings.Contains(r.stdout, "stable") {
			t.Fatalf("show %s: %+v", ref, r)
		}
	}
	if r := h.run("device", "show", "lamp-9"); r.code != ExitUsage {
		t.Fatalf("show unknown: %+v", r)
	}
}

func TestChannelAndKeyList(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.PersonalToken)
	if r := h.run("channel", "list"); r.code != 0 || !strings.Contains(r.stdout, "stable") || !strings.Contains(r.stdout, "yes") {
		t.Fatalf("channel list: %+v", r)
	}
	if r := h.run("key", "list"); r.code != 0 || !strings.Contains(r.stdout, "No signing keys") {
		t.Fatalf("empty key list: %+v", r)
	}
	h.env[passphraseEnv] = "correct horse battery"
	h.run("key", "generate", "--key-id", "old")
	h.run("keys", "generate", "--key-id", "new")
	now := time.Now()
	h.fake.SigningKeys[0].RevokedAt = &now
	r := h.run("key", "list")
	if r.code != 0 || strings.Index(r.stdout, "new") > strings.Index(r.stdout, "old") || !strings.Contains(r.stdout, "revoked") {
		t.Fatalf("key list: %+v", r)
	}
	r = h.run("key", "list", "--json")
	if r.code != 0 || !strings.Contains(r.stdout, "BEGIN PUBLIC KEY") || strings.Contains(r.stdout, "PRIVATE") {
		t.Fatalf("key list --json: %+v", r)
	}
}

func TestDeploymentList(t *testing.T) {
	h := newHarness(t)
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment("bbbbbbbb-bbbb-4bbb-8bbb-000000000001", api.StateCompleted, 1, 100)
	h.fake.AddDeployment("bbbbbbbb-bbbb-4bbb-8bbb-000000000002", api.StateActive, 2, 25)
	r := h.run("deployment", "list")
	if r.code != 0 || strings.Index(r.stdout, "000000000002") > strings.Index(r.stdout, "000000000001") || !strings.Contains(r.stdout, "25%") {
		t.Fatalf("%+v", r)
	}
	if r := h.run("deployments", "list", "--json"); r.code != 0 || !strings.HasPrefix(r.stdout, "[") {
		t.Fatalf("--json: %+v", r)
	}
}

func TestHelpAtEveryLevel(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"--help"}, {"project"}, {"build", "--help"}, {"deployment"}, {"device"}, {"channel"}, {"key"},
		{"deployment", "wait", "--help"}, {"deployment", "ramp", "--help"}, {"project", "use", "--help"},
	} {
		r := h.run(args...)
		if r.code != 0 || !strings.Contains(r.stdout, "Usage:") || len(r.stdout) < 400 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
