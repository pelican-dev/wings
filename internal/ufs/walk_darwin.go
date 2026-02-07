package ufs

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// getdents wraps the Darwin Getdirentries syscall, which is the macOS
// equivalent of Linux's Getdents.
func getdents(fd int, buf []byte) (int, error) {
	return unix.Getdirentries(fd, buf, nil)
}

func nameFromDirent(de *unix.Dirent) []byte {
	// Darwin's Dirent provides a Namlen field with the exact name length.
	return unsafe.Slice((*byte)(unsafe.Pointer(&de.Name[0])), de.Namlen)
}
