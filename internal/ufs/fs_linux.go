package ufs

import (
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// fdPath returns the filesystem path associated with a file descriptor by
// reading the /proc/self/fd/ symlink.
func fdPath(fd int) (string, error) {
	return filepath.EvalSymlinks(filepath.Join("/proc/self/fd/", strconv.Itoa(fd)))
}

// _openat2 is a wonderful syscall that supersedes the `openat` syscall. It has
// improved validation and security characteristics that weren't available or
// considered when `openat` was originally implemented. As such, it is only
// present in Kernel 5.6 and above.
//
// This method should never be directly called, use `openat` instead.
func (fs *UnixFS) _openat2(dirfd int, name string, flag, mode uint64) (int, error) {
	// Ensure the O_CLOEXEC flag is set.
	// Go sets this when using the os package, but since we are directly using
	// the unix package we need to set it ourselves.
	if flag&O_CLOEXEC == 0 {
		flag |= O_CLOEXEC
	}
	// Ensure the O_LARGEFILE flag is set.
	// Go sets this for unix.Open, unix.Openat, but not unix.Openat2.
	if flag&O_LARGEFILE == 0 {
		flag |= O_LARGEFILE
	}
	fd, err := unix.Openat2(dirfd, name, &unix.OpenHow{
		Flags: flag,
		Mode:  mode,
		// This is the bread and butter of preventing a symlink escape, without
		// this option, we have to handle path validation fully on our own.
		//
		// This is why using Openat2 over Openat is preferred if available.
		Resolve: unix.RESOLVE_BENEATH,
	})
	switch {
	case err == nil:
		return fd, nil
	case err == unix.EINTR:
		return fd, err
	case err == unix.EAGAIN:
		return fd, err
	default:
		return fd, ensurePathError(err, "openat2", name)
	}
}

// Chtimesat is like Chtimes but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Chtimesat(dirfd int, name string, atime, mtime time.Time) error {
	var utimes [2]unix.Timespec
	set := func(i int, t time.Time) {
		if t.IsZero() {
			utimes[i] = unix.Timespec{Sec: unix.UTIME_OMIT, Nsec: unix.UTIME_OMIT}
		} else {
			utimes[i] = unix.NsecToTimespec(t.UnixNano())
		}
	}
	set(0, atime)
	set(1, mtime)

	// This does support `AT_SYMLINK_NOFOLLOW` as well if needed.
	return ensurePathError(unix.UtimesNanoAt(dirfd, name, utimes[0:], 0), "chtimes", name)
}
