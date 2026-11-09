package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const lockWait = 10 * time.Second

// WithLock runs fn while holding an exclusive lock on path, shared by every
// simpleota process: an operating system lock on the file path+".lock". The
// system drops the lock if the process ends, so a crash leaves none behind.
// fn must not take the same lock again.
func WithLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- next to the CLI's own file
	if err != nil {
		return fmt.Errorf("locking %s: %w", path, err)
	}
	defer f.Close()
	deadline := time.Now().Add(lockWait)
	for {
		locked, err := tryLock(f)
		if err != nil {
			return fmt.Errorf("locking %s: %w", path, err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another simpleota command has been changing %s for over %s\nTry again once it has finished.", path, lockWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer unlock(f)
	return fn()
}

// Locked runs fn while holding the config's lock, for a change that also
// touches something else, such as the stored token. fn reads and saves the
// config with Load and Save.
func Locked(dir string, fn func() error) error {
	return WithLock(filepath.Join(dir, fileName), fn)
}

// Update changes the saved config under the lock: it reads the file afresh,
// lets change edit it, and saves it if change reports a change. Commands
// running at the same time therefore keep each other's changes. It returns
// the config as saved.
func Update(dir string, change func(*Config) bool) (Config, error) {
	var c Config
	err := Locked(dir, func() error {
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
