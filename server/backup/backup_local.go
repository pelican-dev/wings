package backup

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	"github.com/juju/ratelimit"
	"github.com/mholt/archives"

	"github.com/pelican-dev/wings/config"
	"github.com/pelican-dev/wings/remote"
	"github.com/pelican-dev/wings/server/filesystem"
)

type LocalBackup struct {
	Backup
}

var _ BackupInterface = (*LocalBackup)(nil)

func NewLocal(client remote.Client, uuid string, suuid string, ignore string) *LocalBackup {
	return &LocalBackup{
		Backup{
			client:     client,
			Uuid:       uuid,
			ServerUuid: suuid,
			Ignore:     ignore,
			adapter:    LocalBackupAdapter,
		},
	}
}

// LocateLocal finds the backup for a server and returns the local path. This
// will obviously only work if the backup was created as a local backup.
func LocateLocal(client remote.Client, uuid string, suuid string) (*LocalBackup, error) {
	b := NewLocal(client, uuid, suuid, "")
	st, err := os.Stat(b.Path())
	if err != nil {
		return nil, err
	}

	if st.IsDir() {
		return nil, errors.New("invalid archive, is directory")
	}

	return b, nil
}

// Remove removes a backup from the system.
func (b *LocalBackup) Remove(_ context.Context) error {
	err := os.Remove(b.Path())
	if err != nil {
		return err
	}
	d, err := os.ReadDir(filepath.Dir(b.Path()))
	if err != nil {
		return err
	}
	if len(d) == 0 {
		err := os.Remove(filepath.Dir(b.Path()))
		if err != nil {
			return err
		}
	}
	return nil
}

// WithLogContext attaches additional context to the log output for this backup.
func (b *LocalBackup) WithLogContext(c map[string]interface{}) {
	b.logContext = c
}

// Generate generates a backup of the selected files and pushes it to the
// defined location for this instance.
func (b *LocalBackup) Generate(ctx context.Context, fsys *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	a := &filesystem.Archive{
		Filesystem: fsys,
		Ignore:     ignore,
	}

	b.log().WithField("path", b.Path()).Info("creating backup for server")
	if _, err := os.Stat(filepath.Dir(b.Path())); os.IsNotExist(err) {
		err := os.Mkdir(filepath.Dir(b.Path()), 0o700)
		if err != nil {
			return nil, err
		}
	}
	if err := a.Create(ctx, b.Path()); err != nil {
		return nil, err
	}
	b.log().Info("created backup successfully")

	ad, err := b.Details(ctx, nil)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get archive details for local backup")
	}
	return ad, nil
}

// Restore will walk over the archive and call the callback function for each
// file encountered.
func (b *LocalBackup) Restore(ctx context.Context, _ io.Reader, callback RestoreCallback) error {
	f, err := os.Open(b.Path())
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	// Steal the logic we use for making backups which will be applied when restoring
	// this specific backup. This allows us to prevent overloading the disk unintentionally.
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		reader = ratelimit.Reader(f, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}
	if err := format.Extract(ctx, reader, func(ctx context.Context, f archives.FileInfo) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()

		return callback(f.NameInArchive, f.FileInfo, r)
	}); err != nil {
		return err
	}
	return nil
}

// Download streams the backup to the caller
func (b *LocalBackup) Download(c *gin.Context) error {
	f, err := os.Open(b.Path())
	if err != nil {
		return errors.WrapIf(err, "backup: could not read archive from disk")
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return errors.WrapIf(err, "backup: could not read archive from disk")
	}

	c.Header("Content-Length", strconv.Itoa(int(st.Size())))
	c.Header("Content-Disposition", "attachment; filename="+strconv.Quote(st.Name()))
	c.Header("Content-Type", "application/octet-stream")

	_, _ = bufio.NewReader(f).WriteTo(c.Writer)

	return nil
}
