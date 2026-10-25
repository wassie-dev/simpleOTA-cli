// Package fileperm enforces owner-only permissions on secret files.
package fileperm

import (
	"fmt"
	"os"
	"runtime"
)

// CheckPrivate returns an error wrapping fs.ErrNotExist if path is missing,
// and refuses files that are not regular files or that group or others can
// access. Windows has no POSIX mode bits, so only the file type is checked.
func CheckPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has permissions %04o, which lets other users read it; run: chmod 600 %s", path, perm, path)
	}
	return nil
}
