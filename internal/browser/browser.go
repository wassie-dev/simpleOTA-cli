// Package browser opens a URL in the user's default browser.
package browser

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// Available reports whether this session can probably show a browser: not
// over SSH, and on Linux or BSD only under a graphical session.
func Available(goos string, getenv func(string) string) bool {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return false
	}
	switch goos {
	case "darwin", "windows":
		return true
	}
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

// Open opens an http or https URL in the default browser. It runs the
// platform's opener directly, without a shell, and does not wait for it.
func Open(rawURL string) error {
	if err := check(rawURL); err != nil {
		return err
	}
	name, args := opener(runtime.GOOS, rawURL)
	cmd := exec.Command(name, args...) // #nosec G204 -- a fixed program; check accepts only an absolute http(s) URL
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func check(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("refusing to open %q: only http and https URLs open in a browser", rawURL)
	}
	return nil
}

func opener(goos, rawURL string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	}
	return "xdg-open", []string{rawURL}
}
