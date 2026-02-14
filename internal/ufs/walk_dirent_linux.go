// SPDX-License-Identifier: BSD-2-Clause

//go:build unix && !darwin

package ufs

// info uses the stored directory file descriptor and name to stat the entry.
// On Linux, Getdents is a real syscall and the directory fd remains fully
// usable for subsequent *at operations.
func (de dirent) info() (FileInfo, error) {
	return de.fs.Lstatat(de.dirfd, de.name)
}

// open uses the stored directory file descriptor and name to open the entry.
func (de dirent) open() (File, error) {
	return de.fs.OpenFileat(de.dirfd, de.name, O_RDONLY, 0)
}
