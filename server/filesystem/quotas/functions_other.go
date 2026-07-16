//go:build !linux

package quotas

import (
	"emperror.dev/errors"
)

var errUnsupported = errors.New("quotas: server disk quotas are only supported on Linux")

// IsSupportedFS checks if the filesystem for the data files is supported.
// Quotas rely on Linux-specific filesystem features, so this always returns
// false on other platforms.
func IsSupportedFS() bool {
	return false
}

// AddQuota is unsupported on non-Linux platforms.
func AddQuota(serverID int, serverUUID string) error {
	return errUnsupported
}

// SetQuota is unsupported on non-Linux platforms.
func SetQuota(limit int64, serverUUID string) error {
	return errUnsupported
}

// GetQuota is unsupported on non-Linux platforms.
func GetQuota(serverUUID string) (int64, error) {
	return 0, errUnsupported
}

// DelQuota is unsupported on non-Linux platforms.
func DelQuota(serverUUID string) error {
	return errUnsupported
}
