//go:build linux || darwin

package recovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Each ancestor is opened independently without following symlinks. Holding
// the resulting directory descriptor prevents a swapped symlink redirecting
// a later open/rename. Parent identity is also bound into the plan.
func openDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("expected clean absolute directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, fmt.Errorf("open directory without symlinks: %w", e)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func parentFor(path string) (*os.File, error) { return openDirectory(filepath.Dir(path)) }

func dirIdentity(dir *os.File) (uint64, uint64, error) {
	i, e := dir.Stat()
	if e != nil {
		return 0, 0, e
	}
	s := i.Sys().(*syscall.Stat_t)
	return uint64(s.Dev), uint64(s.Ino), nil
}

func readRegular(parent *os.File, name string, max int64) (Fingerprint, []byte, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return Fingerprint{}, nil, nil
	}
	if err != nil {
		return Fingerprint{}, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return Fingerprint{}, nil, err
	}
	s := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || s.Nlink != 1 {
		return Fingerprint{}, nil, fmt.Errorf("only regular files with one link and no special mode bits are supported")
	}
	// ACLs on supported Linux filesystems are represented as xattrs; macOS ACL
	// support is checked separately because it is not an xattr there.
	if err := checkExtendedMetadata(fd); err != nil {
		return Fingerprint{}, nil, err
	}
	if i.Size() > max {
		return Fingerprint{}, nil, fmt.Errorf("file exceeds %d-byte limit", max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return Fingerprint{}, nil, err
	}
	if int64(len(b)) > max {
		return Fingerprint{}, nil, fmt.Errorf("file grew beyond limit")
	}
	after, err := f.Stat()
	if err != nil {
		return Fingerprint{}, nil, err
	}
	if i.Size() != after.Size() || !i.ModTime().Equal(after.ModTime()) {
		return Fingerprint{}, nil, fmt.Errorf("file changed while reading")
	}
	return Fingerprint{Exists: true, SHA256: sum(b), Bytes: int64(len(b)), Mode: uint32(i.Mode().Perm()), UID: s.Uid, GID: s.Gid, Device: uint64(s.Dev), Inode: uint64(s.Ino), ModifiedNS: i.ModTime().UnixNano()}, b, nil
}

func lockDirectory(dir *os.File) (func(), error) {
	fd, err := unix.Openat(int(dir.Fd()), "executor.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("another recovery operation is active: %w", err)
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}

func writeAt(dir *os.File, name string, b []byte, fp Fingerprint) error {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	ok := false
	defer func() {
		f.Close()
		if !ok {
			unix.Unlinkat(int(dir.Fd()), name, 0)
		}
	}()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if fp.Exists {
		i, e := f.Stat()
		if e != nil {
			return e
		}
		s := i.Sys().(*syscall.Stat_t)
		if s.Uid != fp.UID || s.Gid != fp.GID {
			if err = f.Chown(int(fp.UID), int(fp.GID)); err != nil {
				return err
			}
		}
		if err = f.Chmod(os.FileMode(fp.Mode)); err != nil {
			return err
		}
		if fp.ModifiedNS != 0 {
			t := time.Unix(0, fp.ModifiedNS)
			times := []unix.Timeval{unix.NsecToTimeval(t.UnixNano()), unix.NsecToTimeval(t.UnixNano())}
			if err = unix.Futimes(fd, times); err != nil {
				return err
			}
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return dir.Sync()
}

func renameAt(dir *os.File, old, new string) error {
	if err := unix.Renameat(int(dir.Fd()), old, int(dir.Fd()), new); err != nil {
		return err
	}
	return dir.Sync()
}

func childPrivateDirectory(parent *os.File, name string, create bool) (*os.File, error) {
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid private directory name")
	}
	if create {
		if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(fd), name)
	i, err := d.Stat()
	if err != nil {
		d.Close()
		return nil, err
	}
	s := i.Sys().(*syscall.Stat_t)
	if i.Mode().Perm() != 0700 || int(s.Uid) != os.Geteuid() {
		d.Close()
		return nil, fmt.Errorf("quarantine directory is not private")
	}
	if create {
		if err = parent.Sync(); err != nil {
			d.Close()
			return nil, err
		}
	}
	return d, nil
}

func moveAt(from *os.File, old string, to *os.File, name string) error {
	if err := unix.Renameat(int(from.Fd()), old, int(to.Fd()), name); err != nil {
		return err
	}
	if err := from.Sync(); err != nil {
		return err
	}
	return to.Sync()
}

func removeAt(dir *os.File, name string) error {
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
		return err
	}
	return dir.Sync()
}

func privateDirectory(path string) (*os.File, error) {
	// New recovery trees are created underneath an already secured state parent.
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	d, err := openDirectory(path)
	if err != nil {
		return nil, err
	}
	i, err := d.Stat()
	if err != nil {
		d.Close()
		return nil, err
	}
	s := i.Sys().(*syscall.Stat_t)
	if i.Mode().Perm() != 0700 || int(s.Uid) != os.Geteuid() {
		d.Close()
		return nil, fmt.Errorf("recovery directory must be owned by executor and mode 0700")
	}
	return d, nil
}
