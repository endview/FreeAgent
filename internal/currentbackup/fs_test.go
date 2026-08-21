package currentbackup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/safefiletree"
)

func TestBundleArtifactNamespaceQuotaV1(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := safeTree{
		artifactCount:         moduleartifactstore.MaxPhysicalArtifactsV1 - 1,
		artifactPathCount:     moduleartifactstore.MaxPhysicalPathsV1 - 1,
		artifactFileCount:     moduleartifactstore.MaxPhysicalFilesV1 - 1,
		artifactPathNameBytes: moduleartifactstore.MaxPhysicalPathBytesV1 - 1,
	}
	if err := accountBundleArtifactNamespaceV1(
		&valid, artifactsDirectory+"/"+digest, safefiletree.KindDirectory,
	); err != nil {
		t.Fatalf("last artifact slot rejected: %v", err)
	}
	if err := accountBundleArtifactNamespaceV1(
		&valid, artifactsDirectory+"/"+digest+"/x", safefiletree.KindRegularFile,
	); err != nil {
		t.Fatalf("last namespace slot rejected: %v", err)
	}

	for name, test := range map[string]struct {
		tree safeTree
		path string
		kind safefiletree.Kind
	}{
		"artifact roots": {
			tree: safeTree{artifactCount: moduleartifactstore.MaxPhysicalArtifactsV1},
			path: artifactsDirectory + "/" + digest,
			kind: safefiletree.KindDirectory,
		},
		"paths": {
			tree: safeTree{artifactPathCount: moduleartifactstore.MaxPhysicalPathsV1},
			path: artifactsDirectory + "/" + digest + "/empty",
			kind: safefiletree.KindDirectory,
		},
		"files": {
			tree: safeTree{artifactFileCount: moduleartifactstore.MaxPhysicalFilesV1},
			path: artifactsDirectory + "/" + digest + "/empty.bin",
			kind: safefiletree.KindRegularFile,
		},
		"path-name bytes": {
			tree: safeTree{artifactPathNameBytes: moduleartifactstore.MaxPhysicalPathBytesV1},
			path: artifactsDirectory + "/" + digest + "/x",
			kind: safefiletree.KindDirectory,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := accountBundleArtifactNamespaceV1(&test.tree, test.path, test.kind); err == nil {
				t.Fatal("over-limit bundle namespace unexpectedly accepted")
			}
		})
	}
}

func TestBackupManifestRejectsMoreArtifactsThanPhysicalRoot(t *testing.T) {
	artifacts := make([]Artifact, int(moduleartifactstore.MaxPhysicalArtifactsV1)+1)
	for index := range artifacts {
		digest := fmt.Sprintf("%064x", index+1)
		artifacts[index] = Artifact{
			Digest: digest, Path: artifactsDirectory + "/" + digest, SizeBytes: 1,
		}
	}
	if _, err := canonicalArtifacts(artifacts); err == nil {
		t.Fatal("manifest exceeding physical artifact-root quota unexpectedly accepted")
	}
}

func TestBundleWalkRejectsUnrelatedDirectoryFloodBeforeAccounting(t *testing.T) {
	root := t.TempDir()
	for index := range 1024 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("junk-%04d", index)), 0o700); err != nil {
			t.Fatalf("create unrelated directory %d: %v", index, err)
		}
	}

	inspected := 0
	_, err := walkSafeTreeWithDatabaseHooks(
		context.Background(),
		root,
		true,
		safeTreeAccessHooks{
			afterPathInspect: func(string, os.FileInfo) error {
				inspected++
				return nil
			},
		},
	)
	if err == nil || !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "unexpected bundle path") {
		t.Fatalf("unrelated directory flood did not fail closed: %v", err)
	}
	if inspected != 0 {
		t.Fatalf("unrelated directory flood reached accounting hook %d times", inspected)
	}
}

func TestReadBoundedRegularFileContextPreservesCompatibilityAndCancellation(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("bounded-context-read\n"), 4096)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	legacy, err := readBoundedRegularFile(path, int64(len(content)))
	if err != nil {
		t.Fatalf("readBoundedRegularFile() error = %v", err)
	}
	contextual, err := readBoundedRegularFileContext(
		context.Background(),
		path,
		int64(len(content)),
	)
	if err != nil {
		t.Fatalf("readBoundedRegularFileContext() error = %v", err)
	}
	if !bytes.Equal(legacy, content) || !bytes.Equal(contextual, content) {
		t.Fatal("legacy or Context read changed successful bytes")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := readBoundedRegularFileContext(canceled, path, int64(len(content)))
	if !errors.Is(err, context.Canceled) || got != nil || errors.Is(err, ErrIntegrity) {
		t.Fatalf("canceled read = %d bytes, %v", len(got), err)
	}

	deadline, cancelDeadline := context.WithDeadline(
		context.Background(),
		time.Now().Add(-time.Second),
	)
	defer cancelDeadline()
	got, err = readBoundedRegularFileContext(deadline, path, int64(len(content)))
	if !errors.Is(err, context.DeadlineExceeded) || got != nil || errors.Is(err, ErrIntegrity) {
		t.Fatalf("expired-deadline read = %d bytes, %v", len(got), err)
	}
}

func TestReadBoundedReaderContextCancelsBetweenChunksWithoutPartial(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstRead{
		cancel: cancel,
		data: bytes.Repeat(
			[]byte("x"),
			boundedRegularFileReadChunkBytes*2,
		),
	}
	got, err := readBoundedReaderContext(
		ctx,
		reader,
		int64(boundedRegularFileReadChunkBytes*2),
	)
	if !errors.Is(err, context.Canceled) || got != nil || errors.Is(err, ErrIntegrity) {
		t.Fatalf("between-chunk cancellation = %d bytes, %v", len(got), err)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.calls)
	}
}

func TestReadBoundedReaderContextPreservesOrdinaryReadError(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("synthetic read failure")
	reader := io.MultiReader(
		bytes.NewReader([]byte("partial")),
		errorReader{err: readFailure},
	)
	got, err := readBoundedReaderContext(context.Background(), reader, 1024)
	if !errors.Is(err, readFailure) || got != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrIntegrity) {
		t.Fatalf("ordinary read failure = %d bytes, %v", len(got), err)
	}
}

func TestWalkSafeTreeContextPreservesMidHashCancellationWithoutPartial(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		walk func(context.Context, string) (safeTree, error)
	}{
		{name: "artifact", walk: walkSafeTreeContext},
		{name: "bundle", walk: walkBundleSafeTreeContext},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			payload := bytes.Repeat([]byte("x"), (256<<10)*2)
			if err := os.WriteFile(
				filepath.Join(root, "payload.bin"),
				payload,
				0o600,
			); err != nil {
				t.Fatalf("write payload: %v", err)
			}

			ctx, cancel := newCancelAfterErrChecksContext(3)
			t.Cleanup(cancel)
			tree, err := test.walk(ctx, root)
			if !errors.Is(err, context.Canceled) ||
				errors.Is(err, ErrInvalidBundle) ||
				errors.Is(err, ErrIntegrity) {
				t.Fatalf("mid-hash cancellation classification = %v", err)
			}
			if tree.files != nil || tree.directories != nil {
				t.Fatalf("cancelled walk returned a partial tree: %+v", tree)
			}
		})
	}
}

func TestWalkSafeTreeRootedTraversalDoesNotEscapeReplacedDescendant(t *testing.T) {
	container := t.TempDir()
	root := filepath.Join(container, "root")
	outside := filepath.Join(container, "outside")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "nested", "marker.txt"),
		[]byte("internal-marker"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(outside, "marker.txt")
	if err := os.WriteFile(externalPath, []byte("external-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	externalInfo, err := os.Stat(externalPath)
	if err != nil {
		t.Fatal(err)
	}
	forcedRootStamp := rootInfo.ModTime().Add(2 * time.Second)

	var fired bool
	var followedExternal bool
	var hookErr error
	var restore func()
	_, err = walkSafeTreeWithDatabaseHooks(
		context.Background(),
		root,
		false,
		safeTreeAccessHooks{
			afterPathInspect: func(path string, info os.FileInfo) error {
				if path == "nested/marker.txt" && os.SameFile(info, externalInfo) {
					followedExternal = true
				}
				if fired || path != "nested" || !info.IsDir() {
					return nil
				}
				fired = true
				restore, hookErr = replaceCurrentBackupTestDirectoryWithExternalLink(
					filepath.Join(root, "nested"),
					outside,
				)
				if hookErr == nil {
					hookErr = os.Chtimes(root, forcedRootStamp, forcedRootStamp)
				}
				return hookErr
			},
		},
	)
	if restore != nil {
		restore()
	}
	if !fired {
		t.Fatal("descendant replacement hook did not run")
	}
	if hookErr != nil {
		t.Fatalf("descendant replacement hook: %v", hookErr)
	}
	if followedExternal {
		t.Fatalf("safe-tree walk followed the replacement to the external marker: %v", err)
	}
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("descendant replacement did not fail closed: %v", err)
	}

	staticLink := filepath.Join(root, "static-junction")
	if err := os.Mkdir(staticLink, 0o755); err != nil {
		t.Fatal(err)
	}
	restoreStatic, err := replaceCurrentBackupTestDirectoryWithExternalLink(
		staticLink,
		outside,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = walkSafeTree(root)
	restoreStatic()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("static reparse/symlink path did not fail closed: %v", err)
	}
}

func replaceCurrentBackupTestDirectoryWithExternalLink(
	path string,
	target string,
) (func(), error) {
	moved := path + ".before-swap"
	if err := os.Rename(path, moved); err != nil {
		return nil, fmt.Errorf("move directory before replacement: %w", err)
	}
	restoreOriginal := func() {
		_ = os.Remove(path)
		_ = os.Rename(moved, path)
	}
	var linkErr error
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", path, target).CombinedOutput()
		if err != nil {
			linkErr = fmt.Errorf("create test junction: %w: %s", err, output)
		}
	} else {
		linkErr = os.Symlink(target, path)
	}
	if linkErr != nil {
		restoreOriginal()
		return nil, linkErr
	}
	var once sync.Once
	return func() {
		once.Do(restoreOriginal)
	}, nil
}

type cancelAfterFirstRead struct {
	cancel context.CancelFunc
	data   []byte
	calls  int
}

func (reader *cancelAfterFirstRead) Read(destination []byte) (int, error) {
	reader.calls++
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, reader.data)
	reader.data = reader.data[count:]
	if reader.calls == 1 {
		reader.cancel()
	}
	return count, nil
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

type cancelAfterErrChecksContext struct {
	context.Context

	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
}

func newCancelAfterErrChecksContext(
	checks int,
) (*cancelAfterErrChecksContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return &cancelAfterErrChecksContext{
		Context:   ctx,
		remaining: checks,
		cancel:    cancel,
	}, cancel
}

func (ctx *cancelAfterErrChecksContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	err := ctx.Context.Err()
	if err == nil && ctx.remaining > 0 {
		ctx.remaining--
		if ctx.remaining == 0 {
			ctx.cancel()
		}
	}
	return err
}
