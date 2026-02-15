package ufs

import (
	"bytes"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fdPath returns the filesystem path associated with a file descriptor using
// the F_GETPATH fcntl command on macOS.
func fdPath(fd int) (string, error) {
	buf := make([]byte, unix.PathMax)
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	if errno != 0 {
		return "", errno
	}
	n := bytes.IndexByte(buf, 0)
	if n < 0 {
		n = len(buf)
	}
	return filepath.EvalSymlinks(string(buf[:n]))
}

// _openat2 is a stub on Darwin. The openat2 syscall is Linux-specific (kernel
// 5.6+). On Darwin, this always returns ENOSYS to signal that the caller
// should fall back to the regular openat path.
func (fs *UnixFS) _openat2(dirfd int, name string, flag, mode uint64) (int, error) {
	return 0, unix.ENOSYS
}

// Chtimesat is like Chtimes but allows passing an existing directory file
// descriptor rather than needing to resolve one. On Darwin, UTIME_OMIT is not
// available, so zero times are handled by reading the current timestamps and
// preserving them.
func (fs *UnixFS) Chtimesat(dirfd int, name string, atime, mtime time.Time) error {
	if atime.IsZero() || mtime.IsZero() {
		var st unix.Stat_t
		if err := unix.Fstatat(dirfd, name, &st, 0); err != nil {
			return ensurePathError(err, "chtimes", name)
		}
		if atime.IsZero() {
			atime = time.Unix(st.Atim.Unix())
		}
		if mtime.IsZero() {
			mtime = time.Unix(st.Mtim.Unix())
		}
	}

	utimes := [2]unix.Timespec{
		unix.NsecToTimespec(atime.UnixNano()),
		unix.NsecToTimespec(mtime.UnixNano()),
	}
	return ensurePathError(unix.UtimesNanoAt(dirfd, name, utimes[0:], 0), "chtimes", name)
}
