//go:build !windows

package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLockReportsAPermissionErrorAsItIs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	start := time.Now()
	_, err := Update(dir, func(*Config) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "another simpleota command") || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}
