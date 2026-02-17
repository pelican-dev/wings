package quotas

import (
	"syscall"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/parkervcp/fsquota"
	"github.com/pelican-dev/wings/config"
)

const (
	FSBTRFS int64 = 2435016766
	FSEXT4  int64 = 61267
	FSXFS   int64 = 1481003842
	FSZFS   int64 = 801189825
)

var fsType int64

func getFSType(mount string) (err error) {
	var stat syscall.Statfs_t

	if mount == "" {
		return errors.New("must specify path to check the filesystem type")
	}

	err = syscall.Statfs(mount, &stat)
	if err != nil {
		return err
	}

	fsType = stat.Type

	switch fsType {
	case FSBTRFS:
		log.WithField("fs-type", "brtfs").Debug("found filesystem")
		return nil
	case FSEXT4:
		log.WithField("fs-type", "ext4").Debug("found filesystem")
		return nil
	case FSXFS:
		log.WithField("fs-type", "xfs").Debug("found filesystem")
		return nil
	case FSZFS:
		log.WithField("fs-type", "zfs").Debug("found filesystem")
		return nil
	default:
		return errors.New("unknown filesystem type")
	}
}

// IsSupportedFS checks if the filesystem for the data files is supported.
// currently only EXT4 and XFS are supported
func IsSupportedFS() (supported bool) {
	log.WithField("path", config.Get().System.Data).Debug("checking filesystem type")
	err := getFSType(config.Get().System.Data)
	if err != nil {
		log.Error(err.Error())
		return
	}

	if fsType == FSEXT4 || fsType == FSXFS {
		// technically tested on EXT4 and will need to be validated for XFS
		supported, err = fsquota.ProjectQuotasSupported(config.Get().System.Data)
		if err != nil {
			return
		}

		if !supported {
			log.WithField("path", config.Get().System.Data).Error("quotas are not enabled")
			return
		}
		log.Debug("using kernel based quota management")
	} else if fsType == FSBTRFS {
		log.WithField("path", config.Get().System.Data).Error("btrfs is not supported")
	} else if fsType == FSZFS {
		log.WithField("path", config.Get().System.Data).Error("btrfs is not supported")
	}

	return
}

// AddQuota adds a server to the configured quotas
func AddQuota(serverID int, serverUUID string) (err error) {
	log.Debug("adding server to stored quota projects")
	if fsType == FSEXT4 || fsType == FSXFS {
		return exfsProject{ID: serverID, Name: serverUUID, BasePath: config.Get().System.Data}.addProject()
	}

	return errors.New("failed to set a quota")
}

// DelQuota removes a server from the configured quotas
func DelQuota(serverUUID string) (err error) {
	if fsType == FSEXT4 || fsType == FSXFS {
		fsProject, err := getProject(serverUUID)
		if err != nil {
			return err
		}
		return fsProject.removeProject()
	}
	return
}

// SetQuota configures quotas for a specified server
func SetQuota(limit int64, serverUUID string) (err error) {
	log.WithField("server", serverUUID).Debug("setting quota")
	if fsType == FSEXT4 || fsType == FSXFS {
		fsProject, err := getProject(serverUUID)
		if err != nil {
			return err
		}
		return fsProject.setQuota(uint64(limit))
	}
	return
}

// GetQuota gets the data usage for a specified server
func GetQuota(serverUUID string) (used int64, err error) {
	if fsType == FSEXT4 || fsType == FSXFS {
		fsProject, err := getProject(serverUUID)
		if err != nil {
			return used, err
		}
		return fsProject.getQuota()
	}
	return
}
