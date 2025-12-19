package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// PushArchiveToTarget POSTs the archive to the target node and returns the
// response body.
func (t *Transfer) PushArchiveToTarget(url, token string) ([]byte, error) {
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	t.SendMessage("Preparing to stream server data to destination...")
	t.SetStatus(StatusProcessing)

	a, err := t.Archive()
	if err != nil {
		t.Error(err, "Failed to get archive for transfer.")
		return nil, errors.New("failed to get archive for transfer")
	}

	t.SendMessage("Streaming archive to destination...")

	// Send the upload progress to the websocket every 5 seconds.
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func(ctx context.Context, a *Archive, tc *time.Ticker) {
		defer tc.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tc.C:
				progress := a.Progress()
				if progress != nil {
					message := "Uploading " + progress.Progress(25)
					// We can't easily show backup count here without tracking totalBackups
					// But we're already showing individual backup progress in StreamBackups
					t.SendMessage(message)
					t.Log().Info(message)
				}
			}
		}
	}(ctx2, a, time.NewTicker(5*time.Second))

	// Create a new request using the pipe as the body.
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)

	// Create a new multipart writer that writes the archive to the pipe.
	mp := multipart.NewWriter(writer)
	defer mp.Close()
	req.Header.Set("Content-Type", mp.FormDataContentType())

	// Create a new goroutine to write the archive to the pipe used by the
	// multipart writer.
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		defer writer.Close()
		defer mp.Close()

		// Stream server data with its own checksum
		src, pw := io.Pipe()
		defer src.Close()
		defer pw.Close()

		mainHasher := sha256.New()
		mainTee := io.TeeReader(src, mainHasher)

		dest, err := mp.CreateFormFile("archive", "archive.tar.gz")
		if err != nil {
			errChan <- errors.New("failed to create form file")
			return
		}

		ch := make(chan error)
		go func() {
			defer close(ch)

			if _, err := io.Copy(dest, mainTee); err != nil {
				ch <- fmt.Errorf("failed to stream archive to destination: %w", err)
				return
			}

			t.Log().Debug("finished copying main archive to destination")
		}()

		// Stream server data
		if err := a.Stream(ctx, pw); err != nil {
			errChan <- errors.New("failed to stream archive to pipe")
			return
		}
		t.Log().Debug("finished streaming archive to pipe")

		// Close the pipe writer to ensure data gets flushed
		_ = pw.Close()

		// Wait for the copy to finish
		t.Log().Debug("waiting on main archive copy to finish")
		if err := <-ch; err != nil {
			errChan <- err
			return
		}

		// Write main archive checksum
		if err := mp.WriteField("checksum_archive", hex.EncodeToString(mainHasher.Sum(nil))); err != nil {
			errChan <- errors.New("failed to stream main archive checksum")
			return
		}

		if len(t.BackupUUIDs) > 0 {
			t.SendMessage(fmt.Sprintf("Streaming %d backup files to destination...", len(t.BackupUUIDs)))
			if err := a.StreamBackups(ctx, mp); err != nil {
				errChan <- fmt.Errorf("failed to stream backups: %w", err)
				return
			}
		} else {
			t.Log().Debug("no backups specified for transfer")
		}

		cancel2()
		t.SendMessage("Finished streaming archive and backups to destination.")

		// Stream install logs if they exist
		if err := a.StreamInstallLogs(ctx, mp); err != nil {
			errChan <- fmt.Errorf("failed to stream install logs: %w", err)
			return
		}
		t.SendMessage("Finished streaming the install logs to destination.")

		if err := mp.Close(); err != nil {
			t.Log().WithError(err).Error("error while closing multipart writer")
		}
		t.Log().Debug("closed multipart writer")
	}()

	t.Log().Debug("sending archive to destination")
	client := http.Client{Timeout: 0}
	res, err := client.Do(req)
	if err != nil {
		t.Log().Debug("error while sending archive to destination")
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from destination: %d", res.StatusCode)
	}
	t.Log().Debug("waiting for stream to complete")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err2 := <-errChan:
		t.Log().Debug("stream completed")
		if err != nil || err2 != nil {
			if err == context.Canceled {
				return nil, err
			}

			t.Log().WithError(err).Debug("failed to send archive to destination")
			return nil, fmt.Errorf("http error: %w, multipart error: %v", err, err2)
		}
		defer res.Body.Close()
		t.Log().Debug("received response from destination")

		v, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if res.StatusCode != http.StatusOK {
			return nil, errors.New(string(v))
		}

		return v, nil
	}
}
