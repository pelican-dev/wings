//go:build darwin

package ufs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReadDirMapWithInfoAndOpen(t *testing.T) {
	for _, useOpenat2 := range []bool{false, true} {
		t.Run("useOpenat2="+boolStr(useOpenat2), func(t *testing.T) {
			tmp := t.TempDir()
			tmp, _ = filepath.EvalSymlinks(tmp)

			for _, name := range []string{"file1.txt", "file2.txt", "server.jar", "eula.txt"} {
				os.WriteFile(filepath.Join(tmp, name), []byte("test data"), 0644)
			}
			os.MkdirAll(filepath.Join(tmp, "logs"), 0755)

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
					eO := e.(interface {
						Open() (File, error)
					})
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
		os.WriteFile(filepath.Join(tmp, name), []byte("test data"), 0644)
	}
	os.MkdirAll(filepath.Join(tmp, "logs"), 0755)
	os.MkdirAll(filepath.Join(tmp, "plugins"), 0755)

	fs, err := NewUnixFS(tmp, false)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		name string
		size int64
	}

	var wg sync.WaitGroup
	errors := make(chan error, 100)
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
					eO := e.(interface {
						Open() (File, error)
					})
					f, err := eO.Open()
					if err != nil {
						return result{}, fmt.Errorf("goroutine %d: Open() for %s: %w", i, e.Name(), err)
					}
					_ = f.Close()
				}

				return result{name: info.Name(), size: info.Size()}, nil
			})
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			if len(out) != 7 {
				errors <- fmt.Errorf("goroutine %d: expected 7 entries, got %d", i, len(out))
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
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
		os.WriteFile(filepath.Join(actual, name), []byte("test data"), 0644)
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
			eO := e.(interface {
				Open() (File, error)
			})
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

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
