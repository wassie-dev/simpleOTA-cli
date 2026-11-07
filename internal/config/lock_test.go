package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A lock file the user cannot write, read-only on Windows, is a permission
// error to report at once, not another command holding the lock.
func TestLockReportsAnUnwritableLockFileAsItIs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	lock := filepath.Join(dir, fileName+".lock")
	if err := os.WriteFile(lock, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lock, 0o600) })
	start := time.Now()
	_, err := Update(dir, func(*Config) bool { return true })
	if !errors.Is(err, fs.ErrPermission) || strings.Contains(err.Error(), "another simpleota command") || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}
