package backup

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"github.com/pelican-dev/wings/config"
	"github.com/pelican-dev/wings/remote"
	"github.com/pelican-dev/wings/server/filesystem"
)

type ResticBackup struct {
	Backup
	SnapshotId        string
	SnapshotSizeBytes int64
}

var _ BackupInterface = (*ResticBackup)(nil)

func NewRestic(client remote.Client, uuid string, suuid string, ignore string) *ResticBackup {
	return &ResticBackup{
		Backup: Backup{
			client:     client,
			Uuid:       uuid,
			ServerUuid: suuid,
			Ignore:     ignore,
			adapter:    ResticBackupAdapater,
		},
		SnapshotId:        "unknown",
		SnapshotSizeBytes: 0,
	}
}

// WithLogContext attaches additional context to the log output for this backup.
func (r ResticBackup) WithLogContext(c map[string]interface{}) {
	r.logContext = c

	// Add the restic snapshotId to the log context if we know what it is
	if r.SnapshotId != "unknown" {
		r.logContext["snapshotId"] = r.SnapshotId
	}
}

// LocateRestic finds the backup for a server and returns the restic info. This
// will obviously only work if the backup was created as a restic backup.
func LocateRestic(ctx context.Context, client remote.Client, uuid string, suuid string) (*ResticBackup, error) {
	r := NewRestic(client, uuid, suuid, "")

	command := ResticCommand{
		Command:    "snapshots",
		OutputJson: true,
		NoLock:     true,
		Args: []string{
			"--tag", uuid,
		},
	}
	cmd, err := r.createCmd(ctx, command)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to create restic snapshots command")
	}

	r.log().Infof("started restic snapshots command: %s", cmd.String())

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("restic snapshots failed: %w, output: %s", err, output)
	}

	var snapshots []struct {
		ID      string `json:"id"`
		Summary struct {
			TotalBytesProcessed int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(output, &snapshots); err != nil {
		r.log().Errorf("failed to parse restic output: %v", err)
	}

	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no snapshots found for tag %q: %w", uuid, os.ErrNotExist)
	}

	r.SnapshotId = snapshots[0].ID
	r.SnapshotSizeBytes = snapshots[0].Summary.TotalBytesProcessed

	r.log().Debugf("Found restic snapshot for backup: id=%s, size=%d bytes", r.SnapshotId, r.SnapshotSizeBytes)

	return r, nil
}

func (r ResticBackup) Generate(ctx context.Context, filesystem *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	r.log().Debugf("Backing up filesystem: %s", filesystem.Path())
	r.log().Debugf("Ignore patterns: %s", ignore)

	args := []string{
		"--tag", r.Uuid,
		"--group-by", "tags",
	}

	trimmed := strings.TrimSpace(ignore)
	if trimmed != "" {
		exclude := []string{"--exclude"}
		exclude = append(exclude, strings.Split(trimmed, "\n")...)
		args = append(args, exclude...)
	}

	command := ResticCommand{
		Command:        "backup",
		PositionalArgs: []string{filesystem.Path()},
		OutputJson:     true,
		Args:           args,
	}
	cmd, err := r.createCmd(ctx, command)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to create restic backup command")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start restic backup: %w", err)
	}
	r.log().Infof("started restic backup command: %s", cmd.String())

	// collect stderr output async
	errChan := make(chan error, 1)
	var stderrBuffer strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "tls: failed to verify certificate") {
				r.log().Error("restic failed to verify tls certificates")
				errChan <- fmt.Errorf("restic TLS certificate verification failed")
				return
			}

			r.log().Errorf("restic stderr: %s", line)
			stderrBuffer.WriteString(line)
			stderrBuffer.WriteByte('\n')
		}
		errChan <- nil
	}()

	doneChan := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			r.log().Debugf("restic output: %s", line)

			var message struct {
				MessageType         string `json:"message_type"`
				TotalBytesProcessed int64  `json:"total_bytes_processed,omitempty"`
				SnapshotId          string `json:"snapshot_id,omitempty"`
			}
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				r.log().Errorf("failed to parse restic output, invalid json line: %v", err)
				continue
			}

			// Will either be status, error or summary, but we only care about summary for now.
			if message.MessageType == "summary" {
				r.SnapshotSizeBytes = message.TotalBytesProcessed
				r.SnapshotId = message.SnapshotId
			}
		}
		close(doneChan)
	}()

	select {
	case err := <-errChan:
		// If restic fails to verify TLS certificates it'll keep retrying so we will need to just kill it ourselves.
		if killErr := cmd.Process.Kill(); killErr != nil {
			r.log().Errorf("failed to kill restic process after TLS error: %v", killErr)
		}
		return nil, err
	case <-doneChan:
		// It exited normally, so we can go ahead and do other stuff
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf(
			"restic backup failed: %v, stderr: %s",
			err,
			strings.TrimSpace(stderrBuffer.String()),
		)
	}

	r.log().Infof("Backup complete: snapshot_id=%v, bytes_processed=%d", r.SnapshotId, r.SnapshotSizeBytes)
	ad, err := r.Details(ctx, nil)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get archive details for restic backup")
	}
	return ad, nil
}

func (r ResticBackup) Restore(_ context.Context, _ io.Reader, _ RestoreCallback) error {
	return errors.New("restic backups do not support Restore with a callback, use ResticRestore instead")
}

func (r ResticBackup) ResticRestore(ctx context.Context, path string) error {
	r.log().Debugf("Restoring to filesystem: %s", path)

	command := ResticCommand{
		Command:        "restore",
		PositionalArgs: []string{r.restorePath()},
		OutputJson:     true,
		NoLock:         true,
		Args: []string{
			"--target", path,
			"--limit-download", strconv.Itoa(config.Get().System.Backups.WriteLimit * 1024 * 1024),
		},
	}
	cmd, err := r.createCmd(ctx, command)
	if err != nil {
		return errors.WrapIf(err, "backup: failed to create restic restore command")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start restic restore: %w", err)
	}
	r.log().Infof("started restic restore command: %s", cmd.String())

	errOutput, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf(
			"restic restore failed: %v, stderr: %s",
			err,
			strings.TrimSpace(string(errOutput)),
		)
	}

	return nil
}

func (r ResticBackup) Remove(ctx context.Context) error {
	command := ResticCommand{
		Command:        "forget",
		PositionalArgs: []string{r.SnapshotId},
		Args: []string{
			"--tag", r.ServerUuid,
			"--group-by", "tags",
			"--prune",
		},
	}
	cmd, err := r.createCmd(ctx, command)
	if err != nil {
		return errors.WrapIf(err, "backup: failed to create restic forget command")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start restic forget: %w", err)
	}
	r.log().Infof("started restic forget command: %s", cmd.String())

	errOutput, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf(
			"restic backup failed: %v, stderr: %s",
			err,
			strings.TrimSpace(string(errOutput)),
		)
	}

	return nil
}

func (r ResticBackup) Download(c *gin.Context) error {
	command := ResticCommand{
		Command:        "dump",
		PositionalArgs: []string{r.restorePath(), "/"},
		NoLock:         true,
		Args:           []string{"--archive", "tar"},
	}
	cmd, err := r.createCmd(c, command)
	if err != nil {
		return errors.WrapIf(err, "backup: failed to create restic dump command")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start restic dump: %w", err)
	}
	r.log().Infof("started restic dump command: %s", cmd.String())

	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="snapshot-`+r.SnapshotId+`.tar.gz"`)

	gz := gzip.NewWriter(c.Writer)
	defer gz.Close()

	if _, err := io.Copy(gz, stdout); err != nil {
		return errors.WrapIf(err, "backup: failed to stream gzip compressed dump output")
	}

	errOutput, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf(
			"restic dump failed: %v, stderr: %s",
			err,
			strings.TrimSpace(string(errOutput)),
		)
	}

	return nil
}

func (r ResticBackup) createCmd(ctx context.Context, info ResticCommand) (*exec.Cmd, error) {
	r.log().Debug("Fetching restic details")
	details, err := r.client.GetResticDetails(ctx, r.Backup.Uuid)
	if err != nil {
		return nil, err
	}
	r.log().Debug("Fetched restic details")

	var env []string
	var s3SpecificArgs []string
	repo, err := func() (string, error) {
		if details.UseS3 {
			s3 := details.S3Details

			s3SpecificArgs = append(s3SpecificArgs, "-o", "s3.bucket-lookup=auto")

			env = append(env, "AWS_DEFAULT_REGION="+s3.Region)
			env = append(env, "AWS_ACCESS_KEY_ID="+s3.AccessKeyID)
			env = append(env, "AWS_SECRET_ACCESS_KEY="+s3.AccessKey)

			parsed, err := url.Parse(s3.Endpoint)
			if err != nil {
				return "", fmt.Errorf("invalid s3 url was passed: %w", err)
			}

			// This should handle removing any extra slashes
			parsed.Path = path.Join(parsed.Path, s3.Bucket)

			// s3:https://s3.amazonaws.com/restic-demo
			return "s3:" + parsed.String(), nil
		} else {
			return details.Repository, nil
		}
	}()
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get restic repository path/url")
	}

	args := []string{info.Command}
	args = append(args, info.PositionalArgs...)
	args = append(args, s3SpecificArgs...)

	args = append(args, "--repo", repo)

	if info.OutputJson {
		args = append(args, "--json")
	}

	if info.NoLock {
		args = append(args, "--no-lock")
	} else {
		args = append(args, "--retry-lock", strconv.Itoa(details.RetryLockSeconds)+"s")
	}

	args = append(args, info.Args...)

	r.log().Debugf("Created restic command with args: %s", strings.Join(args, " "))

	cmd := exec.Command("/restic", args...)
	if details.Password != "" {
		cmd.Env = append(env, "RESTIC_PASSWORD="+details.Password)
	}

	return cmd, nil
}

func (r ResticBackup) restorePath() string {
	return r.SnapshotId + ":" + config.Get().System.Data + "/" + r.ServerUuid
}

// Path Override the default Path method to return an error, as Restic backups do not have a traditional path.
func (r ResticBackup) Path() string {
	return fmt.Errorf("restic backups do not have a path like other backups, they are stored in the restic repository").Error()
}

// Size returns the size of the generated backup.
func (r ResticBackup) Size() (int64, error) {
	r.log().Warn("Restic backups should not use Backup.Size(), check ResticBackup.SnapshotSizeBytes instead.")
	return r.SnapshotSizeBytes, nil
}

// Checksum returns the SHA256 snapshotId of a backup.
func (r ResticBackup) Checksum() ([]byte, error) {
	r.log().Warn("Restic backups should not use Backup.Checksum(), check ResticBackup.SnapshotId instead.")
	return []byte(r.SnapshotId), nil
}

// Details returns both the snapshotId and size of the archive currently stored in
// the repo to the caller.
func (r ResticBackup) Details(_ context.Context, parts []remote.BackupPart) (*ArchiveDetails, error) {
	return &ArchiveDetails{
		ChecksumType: string(ResticBackupAdapater),
		Parts:        parts,
		Checksum:     r.SnapshotId,
		Size:         r.SnapshotSizeBytes,
	}, nil
}

type ResticCommand struct {
	Command        string
	PositionalArgs []string // Immediately after command
	OutputJson     bool
	NoLock         bool
	Args           []string // Additional arguments to pass to the command
}
