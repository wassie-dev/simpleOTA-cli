package browser

import (
	"slices"
	"testing"
)

func TestAvailable(t *testing.T) {
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"darwin", nil, true},
		{"windows", nil, true},
		{"darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 1 5.6.7.8 22"}, false},
		{"windows", map[string]string{"SSH_TTY": "/dev/pts/0"}, false},
		{"linux", nil, false},
		{"linux", map[string]string{"DISPLAY": ":0"}, true},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"linux", map[string]string{"DISPLAY": ":0", "SSH_TTY": "/dev/pts/0"}, false},
		{"freebsd", map[string]string{"DISPLAY": ":0"}, true},
	} {
		if got := Available(tc.goos, func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("%s %v: got %v", tc.goos, tc.env, got)
		}
	}
}

func TestCheckAcceptsOnlyAbsoluteHTTP(t *testing.T) {
	for _, ok := range []string{"https://simpleota.com/tokens/", "http://127.0.0.1:8000/tokens/"} {
		if err := check(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/tokens/", "file:///etc/passwd", "javascript:alert(1)", "-a Calculator", "https://", "ftp://example.com/"} {
		if err := check(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if err := Open("file:///etc/passwd"); err == nil {
		t.Error("Open must refuse what check refuses, before starting anything")
	}
}

func TestOpener(t *testing.T) {
	const u = "https://simpleota.com/tokens/"
	for goos, want := range map[string][]string{
		"darwin":  {"open", u},
		"linux":   {"xdg-open", u},
		"freebsd": {"xdg-open", u},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", u},
	} {
		name, args := opener(goos, u)
		if got := append([]string{name}, args...); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}
}
