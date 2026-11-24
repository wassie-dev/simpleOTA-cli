package api_test

import (
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

func TestCompareDeviceIDs(t *testing.T) {
	saved := func(id string) api.Device { return api.Device{DeviceID: new(id)} }
	for _, tc := range []struct {
		name string
		a, b api.Device
		want int
	}{
		{"both unsaved", api.Device{}, api.Device{}, 0},
		{"unsaved first", api.Device{}, saved("a"), -1},
		{"saved after unsaved", saved("a"), api.Device{}, 1},
		{"by ID", saved("a"), saved("b"), -1},
		{"equal IDs", saved("a"), saved("a"), 0},
	} {
		if got := api.CompareDeviceIDs(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
