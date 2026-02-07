package ufs

import (
	"bytes"
	"unsafe"

	"golang.org/x/sys/unix"
)

// getdents wraps the Linux Getdents syscall.
func getdents(fd int, buf []byte) (int, error) {
	return unix.Getdents(fd, buf)
}

// nameOffset is a compile time constant.
const nameOffset = int(unsafe.Offsetof(unix.Dirent{}.Name))

func nameFromDirent(de *unix.Dirent) []byte {
	// Because Linux's Dirent does not provide a field that specifies the name
	// length, this function must first calculate the max possible name length,
	// and then search for the NULL byte.
	ml := int(de.Reclen) - nameOffset

	name := unsafe.Slice((*byte)(unsafe.Pointer(&de.Name[0])), ml)
	if i := bytes.IndexByte(name, 0); i >= 0 {
		return name[:i]
	}

	// NOTE: This branch is not expected, but included for defensive
	// programming, and provides a hard stop on the name based on the structure
	// field array size.
	return name[:len(de.Name)]
}
