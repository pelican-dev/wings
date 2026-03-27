// SPDX-License-Identifier: BSD-2-Clause

package ufs

// info resolves the entry by path instead of using the stored directory fd.
//
// On Darwin, unix.Getdirentries is a user-space simulation that uses
// fdopendir/readdir_r/closedir and manipulates the directory fd's seek offset
// via lseek to track position (see x/sys/unix syscall_darwin.go). This
// manipulation can leave the directory fd in a state where subsequent fstatat
// calls return EBADF. Using the path-based approach avoids this by opening a
// fresh directory fd for each stat call via safePath.
func (de dirent) info() (FileInfo, error) {
	return de.fs.Lstat(de.path)
}

// open resolves the entry by path instead of using the stored directory fd.
// See info() for why this is necessary on Darwin.
func (de dirent) open() (File, error) {
	return de.fs.OpenFile(de.path, O_RDONLY, 0)
}
