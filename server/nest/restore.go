package nest

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Restore streams a tar.zst archive from presignedUrl into volumePath and
// verifies sha256 matches expectedSha256. bytes flow http body → zstd.Reader
// → tar.Reader → volume files, with the hasher teeing the http body so the
// integrity check covers what came off s3, matching what capture wrote.
//
// returns nil on success or the http error from the callback POST. payload
// errors are encoded into the callback.
func Restore(ctx context.Context, volumePath, presignedUrl, expectedSha256, callbackUrl string) error {
	startedAt := time.Now()

	if entries, err := os.ReadDir(volumePath); err == nil && len(entries) > 0 {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("%v: %s", ErrVolumeAlreadyExists, volumePath)))
	}

	if err := os.MkdirAll(volumePath, 0o755); err != nil {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("create volume dir: %v", err)))
	}

	dlCtx, cancel := context.WithTimeout(ctx, RestoreDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, presignedUrl, nil)
	if err != nil {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("build GET request: %v", err)))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("%v: %v", ErrPresignedDownloadFailed, err)))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("%v: status %d", ErrPresignedDownloadFailed, resp.StatusCode)))
	}

	hasher := sha256.New()
	teeReader := io.TeeReader(resp.Body, hasher)

	zr, err := zstd.NewReader(teeReader)
	if err != nil {
		return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("zstd reader: %v", err)))
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("tar next: %v", err)))
		}

		target := filepath.Join(volumePath, hdr.Name)
		// guard against zip slip: the resolved target must stay inside the
		// volume root. relative paths like ../escape would otherwise write
		// outside the per-server filesystem.
		absVolume, _ := filepath.Abs(volumePath)
		absTarget, _ := filepath.Abs(target)
		rel, err := filepath.Rel(absVolume, absTarget)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("tar entry escapes volume: %s", hdr.Name)))
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("mkdir %s: %v", target, err)))
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("mkdir parent of %s: %v", target, err)))
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("open %s: %v", target, err)))
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return postCallback(callbackUrl, failurePayload(startedAt, fmt.Sprintf("write %s: %v", target, err)))
			}
			_ = f.Close()
		default:
			// game server volumes are regular files and directories only,
			// skip symlinks, fifos, devices to avoid surprising the runtime.
		}
	}

	// drain any tail bytes the zstd decoder did not pull, the hasher needs
	// to see every byte that came off the wire to match what s3 holds.
	_, _ = io.Copy(io.Discard, teeReader)

	gotSha := hex.EncodeToString(hasher.Sum(nil))
	if gotSha != expectedSha256 {
		return postCallback(callbackUrl, CallbackPayload{
			Success:      false,
			ErrorMessage: fmt.Sprintf("%v: got %s expected %s", ErrShaMismatch, gotSha, expectedSha256),
			Sha256:       gotSha,
			StartedAt:    startedAt,
			FinishedAt:   time.Now(),
		})
	}

	return postCallback(callbackUrl, CallbackPayload{
		Success:    true,
		Sha256:     gotSha,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	})
}
