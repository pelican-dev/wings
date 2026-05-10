package nest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Capture streams a tar+zstd archive of volumePath to presignedUrl, deletes
// the volume on success, and POSTs a CallbackPayload to callbackUrl. bytes
// flow tar.Writer → zstd.Writer → http PUT body without an intermediate file
// on disk. sha256 and byte count are teed off the encoded stream so the
// panel records what landed in s3, not what came off the volume.
//
// returns nil on success or the http error from the callback POST. payload
// errors are encoded into the callback rather than returned, the caller
// already has nothing useful to do with them.
func Capture(ctx context.Context, volumePath, presignedUrl, callbackUrl string) error {
	startedAt := time.Now()

	pr, pw := io.Pipe()
	hasher := sha256.New()
	counter := &countingWriter{}
	multi := io.MultiWriter(pw, hasher, counter)

	encErr := make(chan error, 1)
	go func() {
		encErr <- encodeVolume(volumePath, multi)
		_ = pw.Close()
	}()

	uploadCtx, cancel := context.WithTimeout(ctx, CaptureUploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(uploadCtx, http.MethodPut, presignedUrl, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-encErr
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("build PUT request: %v", err)))
	}
	req.Header.Set("Content-Type", "application/zstd")

	resp, err := httpClient.Do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-encErr
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("%v: %v", ErrPresignedUploadFailed, err)))
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if encodeErr := <-encErr; encodeErr != nil {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("encoder: %v", encodeErr)))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("%v: status %d body %s", ErrPresignedUploadFailed, resp.StatusCode, truncate(string(bodyBytes), 512))))
	}

	sha := hex.EncodeToString(hasher.Sum(nil))
	size := counter.n

	if err := os.RemoveAll(volumePath); err != nil {
		// bytes are already on minio, treat as success but flag the orphan so
		// the panel sweep can retry the volume delete.
		return postCallback(callbackUrl, CallbackPayload{
			Success:      true,
			ErrorMessage: fmt.Sprintf("upload succeeded but volume delete failed: %v", err),
			Size:         size,
			Sha256:       sha,
			StartedAt:    startedAt,
			FinishedAt:   time.Now(),
		})
	}

	return postCallback(callbackUrl, CallbackPayload{
		Success:    true,
		Size:       size,
		Sha256:     sha,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	})
}

// encodeVolume walks the volume directory and writes a tar.zst stream into w.
// closes the zstd and tar writers on the way out so the consumer sees a
// well formed end of stream.
func encodeVolume(volumePath string, w io.Writer) error {
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return fmt.Errorf("create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	walkErr := filepath.WalkDir(volumePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(volumePath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if d.Type().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			_ = f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	})

	if walkErr != nil {
		return fmt.Errorf("walk volume: %w", walkErr)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd writer: %w", err)
	}
	return nil
}

// postCallback POSTs payload to callbackUrl. callback delivery is best
// effort, the returned error only describes the http leg.
func postCallback(callbackUrl string, payload CallbackPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal callback: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), CallbackTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackUrl, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post callback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("callback returned status %d: %s", resp.StatusCode, truncate(string(respBody), 256))
	}
	return nil
}

func failurePayload(startedAt time.Time, message string) CallbackPayload {
	return CallbackPayload{
		Success:      false,
		ErrorMessage: message,
		StartedAt:    startedAt,
		FinishedAt:   time.Now(),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// countingWriter tallies bytes written for the size we report back to the
// panel. used as one of the multiwriter sinks alongside the sha256 hasher.
type countingWriter struct {
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
