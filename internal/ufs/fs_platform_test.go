//go:build unix

package ufs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pelican-dev/wings/internal/ufs"
)

func TestFillFileStatFromSys(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()

	// Create a file with known content.
	f, err := fs.Create("test_stat_file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello world")); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	// Stat through UnixFS.
	info, err := fs.Stat("test_stat_file")
	if err != nil {
		t.Fatal(err)
	}

	if info.Name() != "test_stat_file" {
		t.Errorf("expected name 'test_stat_file', got %q", info.Name())
	}
	if info.Size() != 11 {
		t.Errorf("expected size 11, got %d", info.Size())
	}
	if info.IsDir() {
		t.Error("expected file, got directory")
	}
	if info.ModTime().IsZero() {
		t.Error("expected non-zero mod time")
	}
}

func TestOpenatPathValidation(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()

	// Create a file inside the root.
	f, err := fs.Create("safe_file")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Open should succeed.
	f, err = fs.Open("safe_file")
	if err != nil {
		t.Fatalf("expected to open safe_file, got error: %v", err)
	}
	_ = f.Close()

	// Create a symlink pointing outside the root.
	if err := os.Symlink(fs.TmpDir, filepath.Join(fs.Root, "escape_link")); err != nil {
		t.Fatal(err)
	}

	// Opening through the escape symlink should fail with ErrBadPathResolution.
	_, err = fs.Open("escape_link/anything")
	if err == nil {
		t.Error("expected error opening path through escape symlink")
	}
}

func TestChtimesatWithZeroTime(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()

	// Create a file.
	f, err := fs.Create("chtimes_file")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Get original timestamps.
	origInfo, err := fs.Stat("chtimes_file")
	if err != nil {
		t.Fatal(err)
	}
	origMtime := origInfo.ModTime()

	// Wait a bit to ensure timestamps differ if changed.
	time.Sleep(50 * time.Millisecond)

	// Set atime but leave mtime as zero (should preserve original).
	newAtime := time.Now().Add(time.Hour)
	if err := fs.Chtimes("chtimes_file", newAtime, time.Time{}); err != nil {
		t.Fatalf("Chtimes with zero mtime failed: %v", err)
	}

	// Verify mtime was preserved.
	info, err := fs.Stat("chtimes_file")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Sub(origMtime).Abs() > 100*time.Millisecond {
		t.Errorf("expected mtime to be preserved (~%v), got %v", origMtime, info.ModTime())
	}
}

func TestOLargefileConstant(t *testing.T) {
	t.Parallel()

	switch runtime.GOOS {
	case "linux":
		if ufs.O_LARGEFILE == 0 {
			t.Error("expected O_LARGEFILE to be non-zero on Linux")
		}
	case "darwin":
		if ufs.O_LARGEFILE != 0 {
			t.Error("expected O_LARGEFILE to be 0 on Darwin")
		}
	}
}
