package version

import (
	"runtime/debug"
	"testing"
)

func TestCurrent(t *testing.T) {
	module := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	tests := []struct {
		linked string
		bi     *debug.BuildInfo
		want   string
	}{
		{"0.1.3", module("v9.9.9"), "0.1.3"},
		{"dev", module("v0.1.3"), "0.1.3"},
		{"dev", module("v0.1.4-0.20261107120000-abcdef123456"), "0.1.4-0.20261107120000-abcdef123456"},
		{"dev", module("(devel)"), "dev"},
		{"dev", nil, "dev"},
	}
	for _, tt := range tests {
		if got := current(tt.linked, tt.bi); got != tt.want {
			t.Errorf("current(%q, %+v) = %q, want %q", tt.linked, tt.bi, got, tt.want)
		}
	}
}
