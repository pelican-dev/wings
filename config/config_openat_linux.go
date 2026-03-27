package config

import (
	"sync/atomic"

	"github.com/apex/log"
	"golang.org/x/sys/unix"
)

var (
	openat2    atomic.Bool
	openat2Set atomic.Bool
)

func UseOpenat2() bool {
	if openat2Set.Load() {
		return openat2.Load()
	}
	defer openat2Set.Store(true)

	c := Get()
	openatMode := c.System.OpenatMode
	switch openatMode {
	case "openat2":
		openat2.Store(true)
		return true
	case "openat":
		openat2.Store(false)
		return false
	default:
		fd, err := unix.Openat2(unix.AT_FDCWD, "/", &unix.OpenHow{})
		if err != nil {
			log.WithError(err).Warn("error occurred while checking for openat2 support, falling back to openat")
			openat2.Store(false)
			return false
		}
		_ = unix.Close(fd)
		openat2.Store(true)
		return true
	}
}
