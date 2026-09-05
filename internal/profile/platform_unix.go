//go:build !windows

package profile

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func restrictDir(path string) error { return os.Chmod(path, 0700) }

func (s *Store) Lock() (func(), error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
