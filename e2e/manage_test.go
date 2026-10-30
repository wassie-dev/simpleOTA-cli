//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// TestManageCommands drives the commands that change resources, against
// the dev server only.
func TestManageCommands(t *testing.T) {
	ci := ciCLI(t)
	up := decode[struct {
		BuildNumber int `json:"build_number"`
	}](t, ci.mustRun("build", "upload", firmware(t), "--version-label", "e2e-"+uid(), "--json").stdout)
	build := strconv.Itoa(up.BuildNumber)

	created := decode[struct {
		Deployment struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"deployment"`
	}](t, ci.mustRun("deployment", "create", "--build", build, "--percent", "100", "--json").stdout)
	if created.Deployment.State != "active" {
		t.Fatalf("deployment create: %+v", created)
	}
	t.Cleanup(func() { cancelDeployment(t, created.Deployment.ID) })
	if r := ci.run("", "build", "archive", build); r.code != 1 || !strings.Contains(r.stderr, "cancel or complete") {
		t.Fatalf("archive with an active deployment: %+v", r)
	}

	d := newDevice(t)
	if got := d.check(up.BuildNumber); got != created.Deployment.ID {
		t.Fatalf("offered %s, want %s", got, created.Deployment.ID)
	}
	if s := ci.mustRun("device", "events", d.id).stdout; !strings.Contains(s, "offered") {
		t.Fatalf("device events: %s", s)
	}
	channel := "e2e-" + uid()
	ci.mustRun("channel", "create", channel)
	if s := ci.mustRun("device", "update", d.id, "--name", "E2E lamp", "--channel", channel).stdout; !strings.Contains(s, "E2E lamp") || !strings.Contains(s, channel) {
		t.Fatalf("device update: %s", s)
	}

	group := "e2e-" + uid()
	ci.mustRun("group", "create", group)
	ci.mustRun("group", "member", "add", group, d.id)
	if s := ci.mustRun("group", "member", "list", group).stdout; !strings.Contains(s, d.id) {
		t.Fatalf("group member list: %s", s)
	}
	ci.mustRun("group", "member", "remove", group, d.id)
	if s := ci.mustRun("group", "member", "list", group).stdout; strings.Contains(s, d.id) {
		t.Fatalf("device still in the group: %s", s)
	}

	ci.mustRun("deployment", "cancel", created.Deployment.ID)
	ci.mustRun("build", "archive", build)
	if r := ci.run("", "deployment", "create", "--build", build); r.code != 2 || !strings.Contains(r.stderr, "archived") {
		t.Fatalf("deploying an archived build: %+v", r)
	}
	ci.mustRun("build", "unarchive", build)

	if s := ci.mustRun("audit", "--type", "artifact.uploaded", "--limit", "5").stdout; !strings.Contains(s, "build_number="+build) {
		t.Fatalf("audit: %s", s)
	}
}

func TestKeyRevoke(t *testing.T) {
	admin := newCLI(t)
	admin.run(env["SIMPLEOTA_DEV_PERSONAL_TOKEN"]+"\n", "login")
	admin.extra["SIMPLEOTA_SIGNING_KEY_PASSPHRASE"] = "e2e passphrase long"
	keyID := "e2e-" + uid()
	admin.mustRun("key", "generate", "--key-id", keyID)
	if r := admin.run("", "key", "revoke", keyID); r.code != 2 {
		t.Fatalf("without --yes: %+v", r)
	}
	admin.mustRun("key", "revoke", keyID, "--yes")
	keys := decode[[]struct {
		KeyID     string  `json:"key_id"`
		RevokedAt *string `json:"revoked_at"`
	}](t, admin.mustRun("key", "list", "--json").stdout)
	for _, k := range keys {
		if k.KeyID == keyID && k.RevokedAt == nil {
			t.Fatalf("key %s is still active", keyID)
		}
	}
}
