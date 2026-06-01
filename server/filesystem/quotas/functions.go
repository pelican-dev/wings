//go:build linux

package quotas

import (
	"sync/atomic"

	"golang.org/x/sys/unix"

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

var fsType atomic.Int64

// IsSupportedFS checks if the filesystem for the data files is supported.
// currently only EXT4 and XFS are supported
func IsSupportedFS() (supported bool) {
	log.WithField("path", config.Get().System.Data).Debug("checking filesystem type")

	var stat unix.Statfs_t

	if err := unix.Statfs(config.Get().System.Data, &stat); err != nil {
		log.WithError(err).Error("there was an issue when reading the fs stat")
		return false
	}

	fsType.Store(int64(stat.Type))

	switch fsType.Load() {
	case FSEXT4, FSXFS:
		log.WithFields(log.Fields{
			"fs-type":   "ext4/xfs",
			"supported": true,
		}).Debug("found filesystem")
		// check if project quotas are enabled
		projectSupported, err := fsquota.ProjectQuotasSupported(config.Get().System.Data)
		if err != nil {
			log.WithField("quota-check", "failed while running fsquota support check").
				WithError(err).
				Error("there was an issue with checking quota support")
			return false
		}

		// if project quotas are not supported then log an error
		if !projectSupported {
			log.Error("project quotas are not enabled on this filesystem")
			return false
		}

		return true
	case FSBTRFS:
		log.WithFields(log.Fields{
			"fs-type":   "btrfs",
			"supported": false,
		}).Debug("found filesystem")
		return false
	case FSZFS:
		log.WithFields(log.Fields{
			"fs-type":   "zfs",
			"supported": false,
		}).Debug("found filesystem")
		return false
	default:
		log.WithFields(log.Fields{
			"fs-type":  "unknown",
			"fs-magic": stat.Type,
		}).Error("unknown filesystem type")
		return false
	}
}

// AddQuota adds a server to the configured quotas
func AddQuota(serverID int, serverUUID string) (err error) {
	log.Debug("adding server to stored quota projects")
	if t := fsType.Load(); t == FSEXT4 || t == FSXFS {
		return exfsProject{ID: serverID, Name: serverUUID, BasePath: config.Get().System.Data}.addProject()
	}

	return errors.New("failed to set a quota")
}

// SetQuota configures quotas for a specified server
// When the limit is set to a negative number it is set to 0
// 0 is treated as unlimited.
func SetQuota(limit int64, serverUUID string) (err error) {
	log.WithField("server", serverUUID).Debug("setting quota")
	if limit < 0 {
		log.WithField("requested_limit", limit).Error("quota limit cannot be negative, setting to zero")
		limit = 0
	}
	if t := fsType.Load(); t == FSEXT4 || t == FSXFS {
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
	if t := fsType.Load(); t == FSEXT4 || t == FSXFS {
		fsProject, err := getProject(serverUUID)
		if err != nil {
			return used, err
		}
		return fsProject.getQuota()
	}
	return
}

// DelQuota removes a server from the configured quotas
func DelQuota(serverUUID string) (err error) {
	if t := fsType.Load(); t == FSEXT4 || t == FSXFS {
		fsProject, err := getProject(serverUUID)
		if err != nil {
			return err
		}
		return fsProject.removeProject()
	}
	return
}
