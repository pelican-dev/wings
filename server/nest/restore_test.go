package nest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestRestore_StreamsArchiveIntoVolume(t *testing.T) {
	files := map[string][]byte{
		"server.properties":             []byte("motd=hello"),
		filepath.Join("world", "level"): []byte("level"),
	}
	archive := buildArchive(t, files)
	expectedSha := sha256.Sum256(archive.Bytes())
	expectedShaHex := hex.EncodeToString(expectedSha[:])

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zstd")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archive.Bytes())
	}))
	defer s3Server.Close()

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJsonBody(r, &callbackPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "newvolume")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Restore(ctx, volumePath, s3Server.URL, expectedShaHex, callbackServer.URL); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	if !callbackPayload.Success {
		t.Fatalf("expected success=true, got error: %s", callbackPayload.ErrorMessage)
	}

	for name, expected := range files {
		path := filepath.Join(volumePath, name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("%s mismatch: got %q expected %q", name, got, expected)
		}
	}
}

func TestRestore_FailsOnShaMismatch(t *testing.T) {
	archive := buildArchive(t, map[string][]byte{"x": []byte("y")})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer s3Server.Close()

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJsonBody(r, &callbackPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "newvolume")

	wrongSha := "0000000000000000000000000000000000000000000000000000000000000000"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = Restore(ctx, volumePath, s3Server.URL, wrongSha, callbackServer.URL)

	if callbackPayload.Success {
		t.Errorf("expected success=false on sha mismatch")
	}
	if !bytes.Contains([]byte(callbackPayload.ErrorMessage), []byte("sha256 mismatch")) {
		t.Errorf("expected sha mismatch error, got: %s", callbackPayload.ErrorMessage)
	}
}

func TestRestore_RefusesNonEmptyDestination(t *testing.T) {
	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "newvolume")
	if err := os.MkdirAll(volumePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volumePath, "stale"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJsonBody(r, &callbackPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	// presigned url is never reached, the destination check fails first
	_ = Restore(context.Background(), volumePath, "http://unused", "00", callbackServer.URL)

	if callbackPayload.Success {
		t.Errorf("expected success=false when destination is non empty")
	}
	if !bytes.Contains([]byte(callbackPayload.ErrorMessage), []byte("already exists")) {
		t.Errorf("expected already exists error, got: %s", callbackPayload.ErrorMessage)
	}
}

func TestRestore_RefusesZipSlipEntry(t *testing.T) {
	// craft an archive with a tar entry that escapes the volume via ../
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = zw.Close()

	expectedSha := sha256.Sum256(buf.Bytes())
	expectedShaHex := hex.EncodeToString(expectedSha[:])

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer s3Server.Close()

	var callbackPayload CallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJsonBody(r, &callbackPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	tempDir := t.TempDir()
	volumePath := filepath.Join(tempDir, "newvolume")

	_ = Restore(context.Background(), volumePath, s3Server.URL, expectedShaHex, callbackServer.URL)

	if callbackPayload.Success {
		t.Errorf("expected success=false on zip slip attempt")
	}
	if !bytes.Contains([]byte(callbackPayload.ErrorMessage), []byte("escapes volume")) {
		t.Errorf("expected escapes volume error, got: %s", callbackPayload.ErrorMessage)
	}
}

// buildArchive builds an in-memory tar.zst from name → content map for tests.
func buildArchive(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}
