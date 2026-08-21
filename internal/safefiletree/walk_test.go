//go:build windows || linux || darwin

package safefiletree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWalkVisitsOpenedFilesInLexicalDepthFirstOrder(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "alpha", "empty"))
	mustWriteFile(t, filepath.Join(root, "alpha", "one.txt"), "one")
	mustWriteFile(t, filepath.Join(root, "zulu.txt"), "zulu")

	var paths []string
	contents := make(map[string]string)
	var retained io.Reader
	err := Walk(context.Background(), root, func(_ context.Context, entry Entry) error {
		paths = append(paths, entry.Path)
		if entry.Kind == KindDirectory {
			if entry.Reader != nil || !entry.Info.IsDir() {
				t.Fatalf("directory entry is inconsistent: %+v", entry)
			}
			return nil
		}
		if entry.Reader == nil || !entry.Info.Mode().IsRegular() {
			t.Fatalf("regular entry is inconsistent: %+v", entry)
		}
		content, err := io.ReadAll(entry.Reader)
		if err != nil {
			return err
		}
		contents[entry.Path] = string(content)
		retained = entry.Reader
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	wantPaths := []string{".", "alpha", "alpha/empty", "alpha/one.txt", "zulu.txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if want := map[string]string{"alpha/one.txt": "one", "zulu.txt": "zulu"}; !reflect.DeepEqual(contents, want) {
		t.Fatalf("contents = %#v, want %#v", contents, want)
	}
	if _, err := retained.Read(make([]byte, 1)); err == nil {
		t.Fatal("retained borrowed reader remained usable after Walk")
	}
}

func TestWalkRejectsSameRootDirectoryLinkSwap(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	target := filepath.Join(root, "target")
	mustMkdirAll(t, nested)
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(nested, "original.txt"), "original")
	mustWriteFile(t, filepath.Join(target, "marker.txt"), "same-root-target")

	var cleanup func()
	fired := false
	var exposedTarget bool
	err := walk(
		context.Background(),
		root,
		Options{Visit: func(_ context.Context, entry Entry) error {
			if entry.Reader == nil {
				return nil
			}
			content, readErr := io.ReadAll(entry.Reader)
			if string(content) == "same-root-target" {
				exposedTarget = true
			}
			return readErr
		}},
		walkHooks{beforeOpen: func(path string) error {
			if fired || path != "nested" {
				return nil
			}
			fired = true
			var err error
			cleanup, err = replaceDirectoryWithSameRootLink(nested, target)
			return err
		}},
	)
	if cleanup != nil {
		cleanup()
	}
	if !fired {
		t.Fatal("directory swap hook did not run")
	}
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("same-root directory link swap error = %v", err)
	}
	if exposedTarget {
		t.Fatal("visitor received bytes reached through the replacement link")
	}
}

func TestWalkRejectsSameRootHardlinkFileSwap(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	target := filepath.Join(root, "target.txt")
	path := filepath.Join(nested, "payload.txt")
	mustMkdirAll(t, nested)
	mustWriteFile(t, path, "original")
	mustWriteFile(t, target, "same-root-hardlink-target")

	var cleanup func()
	fired := false
	var exposedTarget bool
	err := walk(
		context.Background(),
		root,
		Options{Visit: func(_ context.Context, entry Entry) error {
			if entry.Reader == nil {
				return nil
			}
			content, readErr := io.ReadAll(entry.Reader)
			if string(content) == "same-root-hardlink-target" {
				exposedTarget = true
			}
			return readErr
		}},
		walkHooks{beforeOpen: func(relative string) error {
			if fired || relative != "nested/payload.txt" {
				return nil
			}
			fired = true
			moved := path + ".before-swap"
			if err := os.Rename(path, moved); err != nil {
				return err
			}
			if err := os.Link(target, path); err != nil {
				_ = os.Rename(moved, path)
				return err
			}
			cleanup = func() {
				_ = os.Remove(path)
				_ = os.Rename(moved, path)
			}
			return nil
		}},
	)
	if cleanup != nil {
		cleanup()
	}
	if !fired {
		t.Fatal("file swap hook did not run")
	}
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("same-root hardlink file swap error = %v", err)
	}
	if exposedTarget {
		t.Fatal("visitor received bytes from a multiply-linked replacement")
	}
}

func TestWalkRejectsLinkedRoot(t *testing.T) {
	container := t.TempDir()
	target := filepath.Join(container, "target")
	link := filepath.Join(container, "linked-root")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "marker.txt"), "marker")
	cleanup, err := replaceDirectoryWithSameRootLink(link, target)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	err = Walk(context.Background(), link, func(context.Context, Entry) error { return nil })
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("linked root error = %v", err)
	}
}

func TestWalkPreservesContextAndVisitorErrors(t *testing.T) {
	root := t.TempDir()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Walk(cancelled, root, func(context.Context, Entry) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Walk error = %v", err)
	}
	want := errors.New("visitor stopped")
	err := Walk(context.Background(), root, func(context.Context, Entry) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("visitor error = %v", err)
	}
}

func TestWalkRejectsSameSizeInPlaceMutation(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "payload.txt")
	mustWriteFile(t, filePath, "before")
	mutated := false
	err := Walk(context.Background(), root, func(_ context.Context, entry Entry) error {
		if entry.Path != "payload.txt" {
			return nil
		}
		file, err := os.OpenFile(filePath, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		if _, err := file.WriteAt([]byte("after!"), 0); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		stamp := entry.Info.ModTime().Add(2 * time.Second)
		if err := os.Chtimes(filePath, stamp, stamp); err != nil {
			return err
		}
		mutated = true
		return nil
	})
	if !mutated {
		t.Fatal("same-size mutation did not run")
	}
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("same-size in-place mutation error = %v", err)
	}
}

func TestWalkRejectsDirectoryNamespaceMutationAfterEnumeration(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "a")
	var rootStamp time.Time
	mutated := false
	visitedInjected := false
	err := Walk(context.Background(), root, func(_ context.Context, entry Entry) error {
		if entry.Path == "." {
			rootStamp = entry.Info.ModTime()
			return nil
		}
		if entry.Path == "injected.txt" {
			visitedInjected = true
		}
		if entry.Path != "a.txt" {
			return nil
		}
		mustWriteFile(t, filepath.Join(root, "injected.txt"), "injected")
		stamp := rootStamp.Add(2 * time.Second)
		if err := os.Chtimes(root, stamp, stamp); err != nil {
			return err
		}
		mutated = true
		return nil
	})
	if !mutated {
		t.Fatal("namespace mutation did not run")
	}
	if visitedInjected {
		t.Fatal("entry inserted after enumeration was unexpectedly visited")
	}
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("directory namespace mutation error = %v", err)
	}
}

func TestWalkWithOptionsProvidesPostorderSyncAndExpiringControl(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "alpha", "empty"))
	mustWriteFile(t, filepath.Join(root, "alpha", "file.txt"), "file")
	var leaves []string
	var retained Entry
	controlCalled := false
	err := WalkWithOptions(context.Background(), root, Options{
		OpenForSync: true,
		Visit: func(_ context.Context, entry Entry) error {
			if entry.Path == "alpha/file.txt" {
				if err := entry.Sync(); err != nil {
					return err
				}
				if err := entry.Control(func(uintptr) error {
					controlCalled = true
					return nil
				}); err != nil {
					return err
				}
				retained = entry
			}
			return nil
		},
		LeaveDirectory: func(_ context.Context, entry Entry) error {
			leaves = append(leaves, entry.Path)
			return entry.Sync()
		},
	})
	if err != nil {
		t.Fatalf("WalkWithOptions: %v", err)
	}
	if !controlCalled {
		t.Fatal("Control callback did not run")
	}
	if want := []string{"alpha/empty", "alpha", "."}; !reflect.DeepEqual(leaves, want) {
		t.Fatalf("directory leaves = %#v, want %#v", leaves, want)
	}
	if err := retained.Sync(); !errors.Is(err, ErrEntryExpired) {
		t.Fatalf("expired Sync error = %v", err)
	}
	if err := retained.Control(func(uintptr) error { return nil }); !errors.Is(err, ErrEntryExpired) {
		t.Fatalf("expired Control error = %v", err)
	}
	if err := retained.ControlMetadata(func(uintptr) error { return nil }); !errors.Is(err, ErrEntryExpired) {
		t.Fatalf("expired ControlMetadata error = %v", err)
	}
}

func TestWalkDefaultDoesNotGrantSyncAccess(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "file.txt"), "file")
	checked := false
	err := Walk(context.Background(), root, func(_ context.Context, entry Entry) error {
		if entry.Kind != KindRegularFile {
			return nil
		}
		checked = true
		if err := entry.Sync(); !errors.Is(err, ErrSyncUnavailable) {
			t.Fatalf("default Sync error = %v", err)
		}
		if err := entry.ControlMetadata(func(uintptr) error { return nil }); !errors.Is(err, ErrMetadataControlUnavailable) {
			t.Fatalf("default ControlMetadata error = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("regular file was not visited")
	}
}

func TestWalkControlMetadataRefreshesOnlyPermissionMetadata(t *testing.T) {
	t.Run("ordinary control detects permission drift", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "payload.txt"), "before")
		err := WalkWithOptions(context.Background(), root, Options{
			OpenForSync:            true,
			OpenForMetadataControl: true,
			Visit: func(_ context.Context, entry Entry) error {
				if entry.Path != "payload.txt" {
					return nil
				}
				return entry.Control(changeTestPermissionMetadataAfterChangeTick)
			},
		})
		if err == nil || !errors.Is(err, ErrUnsafeTree) {
			t.Fatalf("ordinary metadata Control error = %v", err)
		}
	})

	t.Run("metadata control refreshes change timestamp", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFile(t, filepath.Join(root, "payload.txt"), "before")
		err := WalkWithOptions(context.Background(), root, Options{
			OpenForSync:            true,
			OpenForMetadataControl: true,
			Visit: func(_ context.Context, entry Entry) error {
				if entry.Path != "payload.txt" {
					return nil
				}
				if err := entry.ControlMetadata(changeTestPermissionMetadata); err != nil {
					return err
				}
				return entry.Sync()
			},
		})
		if err != nil {
			t.Fatalf("permission-only ControlMetadata: %v", err)
		}
	})

	for name, mutate := range map[string]func(uintptr) error{
		"content": func(descriptor uintptr) error {
			if err := changeTestContent(descriptor); err != nil {
				return err
			}
			return changeTestModificationTime(descriptor)
		},
		"size":              changeTestSize,
		"modification time": changeTestModificationTime,
	} {
		t.Run(name+" rejected", func(t *testing.T) {
			root := t.TempDir()
			mustWriteFile(t, filepath.Join(root, "payload.txt"), "before")
			err := WalkWithOptions(context.Background(), root, Options{
				OpenForSync:            true,
				OpenForMetadataControl: true,
				Visit: func(_ context.Context, entry Entry) error {
					if entry.Path != "payload.txt" {
						return nil
					}
					return entry.ControlMetadata(mutate)
				},
			})
			if err == nil || !errors.Is(err, ErrUnsafeTree) {
				t.Fatalf("%s ControlMetadata error = %v", name, err)
			}
		})
	}

	t.Run("namespace identity replacement rejected", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "payload.txt")
		moved := filepath.Join(root, "old-payload.txt")
		mustWriteFile(t, path, "before")
		rootInfo, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		forcedRootStamp := rootInfo.ModTime().Add(2 * time.Second)
		var hookErr error
		var heldReadErr error
		var heldContent string
		hookFired := false
		err = WalkWithOptions(context.Background(), root, Options{
			OpenForSync:            true,
			OpenForMetadataControl: true,
			Visit: func(_ context.Context, entry Entry) error {
				if entry.Path != "payload.txt" {
					return nil
				}
				if err := entry.ControlMetadata(func(uintptr) error {
					hookFired = true
					hookErr = os.Rename(path, moved)
					if hookErr == nil {
						hookErr = os.WriteFile(path, []byte("after!"), 0o600)
					}
					if hookErr == nil {
						hookErr = os.Chtimes(root, forcedRootStamp, forcedRootStamp)
					}
					return hookErr
				}); err != nil {
					return err
				}
				content, err := io.ReadAll(entry.Reader)
				heldReadErr = err
				heldContent = string(content)
				return err
			},
		})
		if !hookFired {
			t.Fatal("namespace replacement hook did not run")
		}
		if hookErr != nil {
			t.Fatalf("namespace replacement hook: %v", hookErr)
		}
		if heldReadErr != nil {
			t.Fatalf("read held file after namespace replacement: %v", heldReadErr)
		}
		if heldContent != "before" {
			t.Fatalf("held file content after namespace replacement = %q", heldContent)
		}
		if err == nil || !errors.Is(err, ErrUnsafeTree) {
			t.Fatalf("identity replacement error = %v", err)
		}
	})
}

func TestWalkRejectsDirectoryEntryFloodBeforeChildVisitor(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		mustWriteFile(t, filepath.Join(root, name), name)
	}
	var visited []string
	err := WalkWithOptions(context.Background(), root, Options{
		MaxEntries:             16,
		MaxEntriesPerDirectory: 4,
		Visit: func(_ context.Context, entry Entry) error {
			visited = append(visited, entry.Path)
			return nil
		},
	})
	if err == nil || !errors.Is(err, ErrUnsafeTree) || !errors.Is(err, ErrEntryLimit) {
		t.Fatalf("directory flood error = %v", err)
	}
	if want := []string{"."}; !reflect.DeepEqual(visited, want) {
		t.Fatalf("visited paths = %#v, want %#v", visited, want)
	}
}

func TestWalkAcceptsExactDirectoryAndGlobalEntryLimits(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d"} {
		mustWriteFile(t, filepath.Join(root, name), name)
	}
	visited := 0
	err := WalkWithOptions(context.Background(), root, Options{
		MaxEntries:             4,
		MaxEntriesPerDirectory: 4,
		Visit: func(context.Context, Entry) error {
			visited++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("exact entry limits rejected: %v", err)
	}
	if visited != 5 {
		t.Fatalf("visited %d entries at exact limit, want 5 including root", visited)
	}
}

func TestWalkRejectsGlobalEntryFloodBeforeOverBudgetChildren(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "a"))
	mustMkdirAll(t, filepath.Join(root, "b"))
	mustWriteFile(t, filepath.Join(root, "a", "one"), "one")
	mustWriteFile(t, filepath.Join(root, "b", "two"), "two")
	var visited []string
	err := WalkWithOptions(context.Background(), root, Options{
		MaxEntries:             3,
		MaxEntriesPerDirectory: 8,
		Visit: func(_ context.Context, entry Entry) error {
			visited = append(visited, entry.Path)
			return nil
		},
	})
	if err == nil || !errors.Is(err, ErrUnsafeTree) || !errors.Is(err, ErrEntryLimit) {
		t.Fatalf("global entry flood error = %v", err)
	}
	for _, entry := range visited {
		if entry == "b/two" {
			t.Fatal("visitor received child beyond the global enumeration bound")
		}
	}
}

func TestWalkPathWithOptionsRejectsSiblingFloodBeforeTargetVisitor(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "target"} {
		mustWriteFile(t, filepath.Join(root, name), name)
	}
	var visited []string
	err := WalkPathWithOptions(context.Background(), root, "target", Options{
		MaxEntries:             4,
		MaxEntriesPerDirectory: 4,
		Visit: func(_ context.Context, entry Entry) error {
			visited = append(visited, entry.Path)
			return nil
		},
	})
	if err == nil || !errors.Is(err, ErrUnsafeTree) || !errors.Is(err, ErrEntryLimit) {
		t.Fatalf("targeted sibling flood error = %v", err)
	}
	if want := []string{"."}; !reflect.DeepEqual(visited, want) {
		t.Fatalf("targeted visited paths = %#v, want %#v", visited, want)
	}
}

func TestWalkPathVisitsOnlyHeldTargetComponents(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "nested"))
	mustWriteFile(t, filepath.Join(root, "nested", "target.txt"), "target")
	mustWriteFile(t, filepath.Join(root, "sibling.txt"), "sibling")
	var paths []string
	var content string
	err := WalkPath(
		context.Background(),
		root,
		"nested/target.txt",
		func(_ context.Context, entry Entry) error {
			paths = append(paths, entry.Path)
			if entry.Reader != nil {
				read, err := io.ReadAll(entry.Reader)
				content = string(read)
				return err
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("WalkPath: %v", err)
	}
	if want := []string{".", "nested", "nested/target.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("targeted paths = %#v, want %#v", paths, want)
	}
	if content != "target" {
		t.Fatalf("targeted content = %q", content)
	}
}

func TestWalkPathPinsDirectoryAcrossSameRootLinkSwap(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	target := filepath.Join(root, "target")
	mustMkdirAll(t, nested)
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(nested, "marker.txt"), "original")
	mustWriteFile(t, filepath.Join(target, "marker.txt"), "same-root-target")
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	forcedRootStamp := rootInfo.ModTime().Add(2 * time.Second)
	var cleanup func()
	var hookErr error
	hookFired := false
	var content string
	err = WalkPath(
		context.Background(),
		root,
		"nested/marker.txt",
		func(_ context.Context, entry Entry) error {
			if entry.Path == "nested" && !hookFired {
				hookFired = true
				cleanup, hookErr = replaceDirectoryWithSameRootLink(nested, target)
				if hookErr == nil {
					hookErr = os.Chtimes(root, forcedRootStamp, forcedRootStamp)
				}
				return hookErr
			}
			if entry.Reader != nil {
				read, err := io.ReadAll(entry.Reader)
				content = string(read)
				return err
			}
			return nil
		},
	)
	if cleanup != nil {
		cleanup()
	}
	if !hookFired {
		t.Fatal("same-root targeted swap hook did not run")
	}
	if hookErr != nil {
		t.Fatalf("same-root targeted swap hook: %v", hookErr)
	}
	if content == "same-root-target" {
		t.Fatal("WalkPath followed the replacement link")
	}
	if content != "original" {
		t.Fatalf("WalkPath pinned content = %q", content)
	}
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("same-root targeted swap error = %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
