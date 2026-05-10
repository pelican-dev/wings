package nest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestCapture_StreamsTarZstdToPresignedUrl(t *testing.T) {
	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "volume")
	if err := os.MkdirAll(filepath.Join(volumePath, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volumePath, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volumePath, "world", "level.dat"), []byte("level"), 0o644); err != nil {
		t.Fatal(err)
	}

	var uploaded bytes.Buffer
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if _, err := io.Copy(&uploaded, r.Body); err != nil {
			t.Errorf("body copy: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := decodeJsonBody(r, &callbackPayload); err != nil {
			t.Errorf("decode callback body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Capture(ctx, volumePath, s3Server.URL, callbackServer.URL); err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	if !callbackPayload.Success {
		t.Fatalf("expected success=true, got error: %s", callbackPayload.ErrorMessage)
	}

	if _, err := os.Stat(volumePath); !os.IsNotExist(err) {
		t.Errorf("volume not deleted after successful capture")
	}

	zr, err := zstd.NewReader(bytes.NewReader(uploaded.Bytes()))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		buf, _ := io.ReadAll(tr)
		files[hdr.Name] = buf
	}

	if string(files["a.txt"]) != "alpha" {
		t.Errorf("a.txt mismatch: %q", files["a.txt"])
	}
	if string(files[filepath.Join("world", "level.dat")]) != "level" {
		t.Errorf("world/level.dat mismatch: %q", files[filepath.Join("world", "level.dat")])
	}

	h := sha256.Sum256(uploaded.Bytes())
	expected := hex.EncodeToString(h[:])
	if callbackPayload.Sha256 != expected {
		t.Errorf("sha256 mismatch: got %s, expected %s", callbackPayload.Sha256, expected)
	}

	if callbackPayload.Size != int64(uploaded.Len()) {
		t.Errorf("size mismatch: got %d, expected %d", callbackPayload.Size, uploaded.Len())
	}
}

func TestCapture_FailsCallbackOnS3Reject(t *testing.T) {
	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "volume")
	if err := os.MkdirAll(volumePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volumePath, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// drain body so the producer goroutine doesn't block on a backed up pipe
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied"))
	}))
	defer s3Server.Close()

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJsonBody(r, &callbackPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = Capture(ctx, volumePath, s3Server.URL, callbackServer.URL)

	if callbackPayload.Success {
		t.Errorf("expected success=false on s3 403")
	}
	if !bytes.Contains([]byte(callbackPayload.ErrorMessage), []byte("status 403")) {
		t.Errorf("expected status 403 in error, got: %s", callbackPayload.ErrorMessage)
	}

	// volume should survive a failed upload, the panel sweep retries
	if _, err := os.Stat(volumePath); err != nil {
		t.Errorf("volume removed despite failed upload: %v", err)
	}
}

func decodeJsonBody(r *http.Request, v any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
