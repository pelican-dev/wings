package quotas

import (
	"os"
	"unsafe"

	"emperror.dev/errors"
	"golang.org/x/sys/unix"
)

// Pulled definitions from /usr/include/linux/fs.h

/*
 * Flags for the fsx_xflags field
 */
const (
	FS_XFLAG_REALTIME     uint32 = 0x00000001 /* data in realtime volume */
	FS_XFLAG_PREALLOC     uint32 = 0x00000002 /* preallocated file extents */
	FS_XFLAG_IMMUTABLE    uint32 = 0x00000008 /* file cannot be modified */
	FS_XFLAG_APPEND       uint32 = 0x00000010 /* all writes append */
	FS_XFLAG_SYNC         uint32 = 0x00000020 /* all writes synchronous */
	FS_XFLAG_NOATIME      uint32 = 0x00000040 /* do not update access time */
	FS_XFLAG_NODUMP       uint32 = 0x00000080 /* do not include in backups */
	FS_XFLAG_RTINHERIT    uint32 = 0x00000100 /* create with rt bit set */
	FS_XFLAG_PROJINHERIT  uint32 = 0x00000200 /* create with parents projid */
	FS_XFLAG_NOSYMLINKS   uint32 = 0x00000400 /* disallow symlink creation */
	FS_XFLAG_EXTSIZE      uint32 = 0x00000800 /* extent size allocator hint */
	FS_XFLAG_EXTSZINHERIT uint32 = 0x00001000 /* inherit inode extent size */
	FS_XFLAG_NODEFRAG     uint32 = 0x00002000 /* do not defragment */
	FS_XFLAG_FILESTREAM   uint32 = 0x00004000 /* use filestream allocator */
	FS_XFLAG_DAX          uint32 = 0x00008000 /* use DAX for IO */
	FS_XFLAG_COWEXTSIZE   uint32 = 0x00010000 /* CoW extent size allocator hint */
	FS_XFLAG_HASATTR      uint32 = 0x80000000 /* no DIFLAG for this   */
)

/*
#define FS_IOC_GETFLAGS                 _IOR('f', 1, long)
#define FS_IOC_SETFLAGS                 _IOW('f', 2, long)
#define FS_IOC_FSGETXATTR               _IOR('X', 31, struct fsxattr)
#define FS_IOC_FSSETXATTR               _IOW('X', 32, struct fsxattr)
*/

const (
	FS_IOC_FSGETXATTR uintptr = 0x801c581f // https://docs.rs/linux-raw-sys/latest/linux_raw_sys/ioctl/constant.FS_IOC_FSGETXATTR.html
	FS_IOC_FSSETXATTR uintptr = 0x401c5820 // https://docs.rs/linux-raw-sys/latest/linux_raw_sys/ioctl/constant.FS_IOC_FSSETXATTR.html
)

//struct fsxattr {
//__u32           fsx_xflags;     /* xflags field value (get/set) */
//__u32           fsx_extsize;    /* extsize field value (get/set)*/
//__u32           fsx_nextents;   /* nextents field value (get)   */
//__u32           fsx_projid;     /* project identifier (get/set) */
//__u32           fsx_cowextsize; /* CoW extsize field value (get/set)*/
//unsigned char   fsx_pad[8];
//};

// fsXAttr is the struct defining the structure
// for FS_IOC_FSGETXATTR and FS_IOC_FSSETXATTR
type fsXAttr struct {
	XFlags     uint32
	ExtSize    uint32
	NextENTs   uint32
	ProjectID  uint32
	CowExtSize uint32
	FSXPad     [8]byte
}

// xAttrCtl handles the xattr calls for a specified file
func xAttrCtl(f *os.File, request uintptr, xattr *fsXAttr) (err error) {
	xattreq := uintptr(unsafe.Pointer(xattr))

	_, _, errno := unix.RawSyscall(unix.SYS_IOCTL, f.Fd(), request, xattreq)
	if errno != 0 {
		return os.NewSyscallError("ioctl", errno)
	}

	return
}

// getXAttr gets the extended attributes of a file
func getXAttr(f *os.File) (attr fsXAttr, err error) {
	if err = xAttrCtl(f, FS_IOC_FSGETXATTR, &attr); err != nil {
		return
	}

	return
}

// setXAttr sets xattr values for a specified file
func setXAttr(f *os.File, projectID int, attr uint32) (err error) {
	if projectID < 0 || uint64(projectID) > uint64(^uint32(0)) {
		return errors.New("projectID out of range")
	}

	fxattr, err := getXAttr(f)
	if err != nil {
		return err
	}

	fxattr.XFlags |= attr
	fxattr.ProjectID = uint32(projectID)

	err = xAttrCtl(f, FS_IOC_FSSETXATTR, &fxattr)
	return
}
