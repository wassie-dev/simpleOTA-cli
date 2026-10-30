package output

import (
	"testing"
	"time"
)

func TestResourceListGolden(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	build, channel := 7, "33333333-3333-4333-8333-333333333333"
	cases := map[string]func(Printer) error{
		"project_list": func(p Printer) error {
			return p.ProjectList([]ProjectView{{ID: "11111111-1111-4111-8111-111111111111", Slug: "fleet", Name: "Fleet", Current: true}, {ID: "22222222-2222-4222-8222-222222222222", Slug: "lab", Name: "Lab"}})
		},
		"build_list": func(p Printer) error {
			return p.BuildList([]BuildView{{BuildNumber: 2, VersionLabel: "1.0.1", Chip: "esp32s3", SecurityMode: "signed", CreatedAt: at, Warnings: []string{}}, {BuildNumber: 1, VersionLabel: "1.0.0", Chip: "esp32s3", SecurityMode: "basic", Archived: true, CreatedAt: at, Warnings: []string{}}})
		},
		"device_list": func(p Printer) error {
			return p.DeviceList([]DeviceView{{DeviceID: "lamp-1", Name: "Kitchen", Chip: "esp32", CurrentBuildNumber: &build, CurrentVersionLabel: "1.0.0", ChannelID: &channel, Channel: "stable", Active: true, CheckState: "checking_in", LastSeen: &at, GroupIDs: []string{}}})
		},
		"channel_list": func(p Printer) error {
			return p.ChannelList([]ChannelView{{ID: channel, Name: "stable", IsDefault: true}})
		},
		"key_list": func(p Printer) error {
			return p.KeyList([]SigningKeyView{{KeyID: "prod-2026", Algorithm: "ed25519", PublicKeyPEM: "PEM", CreatedAt: at}})
		},
		"deployment_list": func(p Printer) error {
			return p.DeploymentList([]DeploymentView{{ID: "5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab", BuildNumber: 2, State: "active", RolloutStrategy: "immediate", RolloutPercentage: 25, CreatedAt: at}})
		},
	}
	for name, f := range cases {
		golden(t, name+".txt", render(t, false, f))
		golden(t, name+".json", render(t, true, f))
	}
}
