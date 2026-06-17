package filesystem

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// CTime returns the time that the file/folder was created.
//
// TODO: remove. Ctim is not actually ever been correct and doesn't actually
// return the creation time.
func (s *Stat) CTime() time.Time {
	if st, ok := s.Sys().(*unix.Stat_t); ok {
		return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	}
	if st, ok := s.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec))
	}
	return time.Time{}
}
