//go:build unix

package ufs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestReadDirMapWithInfoAndOpen(t *testing.T) {
	for _, useOpenat2 := range []bool{false, true} {
		t.Run("useOpenat2="+strconv.FormatBool(useOpenat2), func(t *testing.T) {
			tmp := t.TempDir()
			tmp, _ = filepath.EvalSymlinks(tmp)

			for _, name := range []string{"file1.txt", "file2.txt", "server.jar", "eula.txt"} {
				if err := os.WriteFile(filepath.Join(tmp, name), []byte("test data"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
				t.Fatal(err)
			}

			fs, err := NewUnixFS(tmp, useOpenat2)
			if err != nil {
				t.Fatal(err)
			}

			type result struct {
				name  string
				size  int64
				isDir bool
			}

			out, err := ReadDirMap(fs, "/", func(e DirEntry) (result, error) {
				info, err := e.Info()
				if err != nil {
					return result{}, err
				}

				r := result{
					name:  info.Name(),
					size:  info.Size(),
					isDir: info.IsDir(),
				}

				if e.Type().IsRegular() {
					eO, ok := e.(interface {
						Open() (File, error)
					})
					if !ok {
						return result{}, fmt.Errorf("entry %s does not implement Open()", e.Name())
					}
					f, err := eO.Open()
					if err != nil {
						return result{}, err
					}
					_ = f.Close()
				}

				return r, nil
			})
			if err != nil {
				t.Fatalf("ReadDirMap: %v", err)
			}

			if len(out) != 5 {
				t.Fatalf("expected 5 entries, got %d", len(out))
			}
		})
	}
}

func TestReadDirMapConcurrent(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)

	for _, name := range []string{"file1.txt", "file2.txt", "server.jar", "eula.txt", "config.yml"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("test data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"logs", "plugins"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fs, err := NewUnixFS(tmp, false)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		name string
		size int64
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := ReadDirMap(fs, "/", func(e DirEntry) (result, error) {
				info, err := e.Info()
				if err != nil {
					return result{}, fmt.Errorf("goroutine %d: Info() for %s: %w", i, e.Name(), err)
				}

				if e.Type().IsRegular() {
					eO, ok := e.(interface {
						Open() (File, error)
					})
					if !ok {
						return result{}, fmt.Errorf("goroutine %d: entry %s does not implement Open()", i, e.Name())
					}
					f, err := eO.Open()
					if err != nil {
						return result{}, fmt.Errorf("goroutine %d: Open() for %s: %w", i, e.Name(), err)
					}
					_ = f.Close()
				}

				return result{name: info.Name(), size: info.Size()}, nil
			})
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			if len(out) != 7 {
				errs <- fmt.Errorf("goroutine %d: expected 7 entries, got %d", i, len(out))
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// TestReadDirMapSymlinkedBase tests with a symlinked base path, which is
// common on macOS (/var -> /private/var). NewUnixFS resolves symlinks, but
// let's verify the whole flow works.
func TestReadDirMapSymlinkedBase(t *testing.T) {
	// Create the actual directory
	actual := t.TempDir()
	actual, _ = filepath.EvalSymlinks(actual)

	for _, name := range []string{"server.jar", "eula.txt"} {
		if err := os.WriteFile(filepath.Join(actual, name), []byte("test data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a symlink to it
	linkDir := t.TempDir()
	linkDir, _ = filepath.EvalSymlinks(linkDir)
	linkPath := filepath.Join(linkDir, "link")
	if err := os.Symlink(actual, linkPath); err != nil {
		t.Fatal(err)
	}

	// Create UnixFS using the SYMLINK path (not the resolved path)
	fs, err := NewUnixFS(linkPath, false)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Link path: %s", linkPath)
	t.Logf("Resolved basePath: %s", fs.BasePath())

	type result struct {
		name string
	}

	out, err := ReadDirMap(fs, "/", func(e DirEntry) (result, error) {
		info, err := e.Info()
		if err != nil {
			return result{}, fmt.Errorf("Info() for %s: %w", e.Name(), err)
		}

		if e.Type().IsRegular() {
			eO, ok := e.(interface {
				Open() (File, error)
			})
			if !ok {
				return result{}, fmt.Errorf("entry %s does not implement Open()", e.Name())
			}
			f, err := eO.Open()
			if err != nil {
				return result{}, fmt.Errorf("Open() for %s: %w", e.Name(), err)
			}
			_ = f.Close()
		}

		return result{name: info.Name()}, nil
	})
	if err != nil {
		t.Fatalf("ReadDirMap with symlinked base: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	for _, r := range out {
		t.Logf("  %s", r.name)
	}
}

// TestReadDirRootEntryPaths is a regression test: entries read from the root
// of a directory walk (relative == ".") must carry their own name as their
// path, not the parent directory's name. Linux never reads dirent.path (it
// uses dirfd+name), so a wrong value only surfaced through Darwin's
// path-based Info()/Open() implementations.
func TestReadDirRootEntryPaths(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)

	for _, name := range []string{"alpha.txt", "beta.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("test data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fs, err := NewUnixFS(tmp, false)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := fs.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		de, ok := e.(*dirent)
		if !ok {
			t.Fatalf("expected *dirent, got %T", e)
		}
		if de.path != de.name {
			t.Errorf("root entry %q has path %q; expected it to equal the entry name", de.name, de.path)
		}
	}
}
