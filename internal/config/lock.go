package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	lockWait  = 10 * time.Second
	lockStale = 30 * time.Second
)

// WithLock runs fn while holding a lock on path, shared by every simpleota
// process: a file next to path, created exclusively. A lock older than 30
// seconds is left over from a process that stopped and is removed.
func WithLock(path string, fn func() error) error {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- next to the CLI's own file
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, fs.ErrExist) && !errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("locking %s: %w", path, err)
		}
		if fi, statErr := os.Stat(lock); statErr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another simpleota command is using %s\nIf none is running, delete %s.", path, lock)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() { _ = os.Remove(lock) }()
	return fn()
}

// Update changes the saved config under the lock: it reads the file afresh,
// lets change edit it, and saves it if change reports a change. Commands
// running at the same time therefore keep each other's changes. It returns
// the config as saved.
func Update(dir string, change func(*Config) bool) (Config, error) {
	var c Config
	err := WithLock(filepath.Join(dir, fileName), func() error {
		var err error
		if c, err = Load(dir); err != nil {
			return err
		}
		if !change(&c) {
			return nil
		}
		return Save(dir, c)
	})
	return c, err
}
