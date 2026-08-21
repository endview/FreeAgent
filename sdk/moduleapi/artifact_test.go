package moduleapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var artifactTestManifest = []byte(
	`{"api_version":"v1","id":"example.echo","version":"1.0.0"}`,
)

func TestComputeArtifactDigestMatchesFrozenVector(t *testing.T) {
	t.Parallel()

	files := []ArtifactFile{
		{Path: `content\Café.txt`, Content: []byte("world")},
		{Path: "README.md", Content: []byte("hello\n")},
	}
	got, err := ComputeArtifactDigest(artifactTestManifest, files)
	if err != nil {
		t.Fatalf("ComputeArtifactDigest: %v", err)
	}
	const want = "b7ffca75410f6f9ff039ad282b9d4121ce2d0931c0a4ba2c5645af5d31bb808b"
	if got != want {
		t.Fatalf("ArtifactDigest = %q, want %q", got, want)
	}

	// The caller's collection is neither reordered nor normalized in place.
	if files[0].Path != `content\Café.txt` || files[1].Path != "README.md" {
		t.Fatalf("ComputeArtifactDigest mutated caller paths: %#v", files)
	}
}

func TestComputeArtifactDigestFromFileDigestsMatchesContentAPI(t *testing.T) {
	t.Parallel()

	files := []ArtifactFile{
		{Path: "content/value.txt", Content: []byte("value")},
		{Path: "README.md", Content: []byte("readme")},
	}
	digests := make([]ArtifactFileDigest, 0, len(files))
	for _, file := range files {
		sum := sha256.Sum256(file.Content)
		digests = append(digests, ArtifactFileDigest{
			Path:   file.Path,
			SHA256: hex.EncodeToString(sum[:]),
		})
	}
	want, err := ComputeArtifactDigest(artifactTestManifest, files)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ComputeArtifactDigestFromFileDigests(artifactTestManifest, digests)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest-only result %s != content result %s", got, want)
	}
	if digests[0].Path != "content/value.txt" ||
		!ValidSHA256(digests[0].SHA256) {
		t.Fatalf("digest-only API mutated input: %#v", digests)
	}
	bad := append([]ArtifactFileDigest(nil), digests...)
	bad[0].SHA256 = strings.ToUpper(bad[0].SHA256)
	if _, err := ComputeArtifactDigestFromFileDigests(artifactTestManifest, bad); err == nil {
		t.Fatal("digest-only API accepted non-canonical SHA-256")
	}
}

func TestComputeArtifactDigestIsIndependentOfEnumerationAndPathForm(t *testing.T) {
	t.Parallel()

	left := []ArtifactFile{
		{Path: "é.txt", Content: []byte("non-ascii")},
		{Path: "z.txt", Content: []byte("ascii")},
		{Path: `dir\child.txt`, Content: []byte("child")},
	}
	right := []ArtifactFile{
		{Path: "dir/child.txt", Content: []byte("child")},
		{Path: "z.txt", Content: []byte("ascii")},
		{Path: "e\u0301.txt", Content: []byte("non-ascii")},
	}
	leftDigest, err := ComputeArtifactDigest(artifactTestManifest, left)
	if err != nil {
		t.Fatalf("left digest: %v", err)
	}
	rightDigest, err := ComputeArtifactDigest(artifactTestManifest, right)
	if err != nil {
		t.Fatalf("right digest: %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf(
			"equivalent package digests differ: %s != %s",
			leftDigest,
			rightDigest,
		)
	}
}

func TestComputeArtifactDigestDoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	manifest := bytes.Clone(artifactTestManifest)
	content := []byte("immutable at call boundary")
	files := []ArtifactFile{{Path: "content/value.txt", Content: content}}
	manifestBefore := bytes.Clone(manifest)
	contentBefore := bytes.Clone(content)
	pathBefore := files[0].Path
	digest, err := ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatalf("ComputeArtifactDigest: %v", err)
	}
	if !bytes.Equal(manifest, manifestBefore) ||
		!bytes.Equal(content, contentBefore) ||
		files[0].Path != pathBefore {
		t.Fatalf("ComputeArtifactDigest mutated caller inputs: %#v", files)
	}

	// The synchronous digest result has no alias to caller-owned input storage.
	for index := range manifest {
		manifest[index] = 'x'
	}
	for index := range content {
		content[index] = 'x'
	}
	files[0].Path = "changed.txt"
	if digest != "90c6880513a7cbe0664db61b8b8a9a69b9e579fb337685f738482cd807ec7adf" {
		t.Fatalf("returned digest changed unexpectedly: %s", digest)
	}
}

func TestComputeArtifactDigestRejectsNonCanonicalOrNonObjectManifest(
	t *testing.T,
) {
	t.Parallel()

	for _, manifest := range [][]byte{
		[]byte(`{"version":"1.0.0","id":"example.echo","api_version":"v1"}`),
		[]byte(` {"api_version":"v1","id":"example.echo","version":"1.0.0"}`),
		[]byte(`["not","an","object"]`),
		[]byte(`{"id":"duplicate","id":"duplicate"}`),
		[]byte{0xff},
	} {
		if _, err := ComputeArtifactDigest(manifest, nil); err == nil {
			t.Fatalf("accepted invalid canonical manifest %q", manifest)
		}
	}
}

func TestNormalizeArtifactPathCrossPlatform(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "schemas/request.json", want: "schemas/request.json"},
		{input: `schemas\request.json`, want: "schemas/request.json"},
		{input: "content/Cafe\u0301.txt", want: "content/Café.txt"},
		{input: ".well-known/config", want: ".well-known/config"},
	} {
		got, err := NormalizeArtifactPath(test.input)
		if err != nil {
			t.Fatalf("NormalizeArtifactPath(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf(
				"NormalizeArtifactPath(%q) = %q, want %q",
				test.input,
				got,
				test.want,
			)
		}
	}

	windowsBackslashAbsolute := strings.Join(
		[]string{"C", `\absolute\file`},
		":",
	)
	windowsSlashAbsolute := strings.Join(
		[]string{"C", "/absolute/file"},
		":",
	)
	windowsUNCAbsolute := strings.Repeat(string(rune(92)), 2) + strings.Join(
		[]string{"server", "share", "file"},
		string(rune(92)),
	)
	for _, input := range []string{
		"",
		"/absolute/file",
		windowsBackslashAbsolute,
		windowsSlashAbsolute,
		windowsUNCAbsolute,
		"a//b",
		"a/./b",
		"a/../b",
		"a/",
		".",
		"..",
		"a\x00b",
		string([]byte{0xff}),
	} {
		if normalized, err := NormalizeArtifactPath(input); err == nil {
			t.Fatalf(
				"NormalizeArtifactPath(%q) unexpectedly returned %q",
				input,
				normalized,
			)
		}
	}
}

func TestComputeArtifactDigestRejectsPathCollisionsAndManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []ArtifactFile
	}{
		{
			name: "separator duplicate",
			files: []ArtifactFile{
				{Path: `dir\value.txt`},
				{Path: "dir/value.txt"},
			},
		},
		{
			name: "unicode normalization duplicate",
			files: []ArtifactFile{
				{Path: "Café.txt"},
				{Path: "Cafe\u0301.txt"},
			},
		},
		{
			name: "case collision",
			files: []ArtifactFile{
				{Path: "README.md"},
				{Path: "readme.md"},
			},
		},
		{
			name: "directory case collision",
			files: []ArtifactFile{
				{Path: "Content/one.txt"},
				{Path: "content/two.txt"},
			},
		},
		{
			name: "file directory prefix conflict",
			files: []ArtifactFile{
				{Path: "content"},
				{Path: "content/value.txt"},
			},
		},
		{
			name:  "manifest exact",
			files: []ArtifactFile{{Path: "module.yaml"}},
		},
		{
			name:  "manifest case collision",
			files: []ArtifactFile{{Path: "MODULE.YAML"}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ComputeArtifactDigest(
				artifactTestManifest,
				test.files,
			); err == nil {
				t.Fatal("ComputeArtifactDigest accepted invalid file collection")
			}
		})
	}
}

func TestScanArtifactDirectoryExcludesOnlyExplicitMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "module.yaml", "untrusted YAML source")
	writeArtifactTestFile(t, root, "module.yaml.sig", "detached signature")
	writeArtifactTestFile(
		t,
		root,
		".freeagent/install-receipt.json",
		"receipt",
	)
	writeArtifactTestFile(t, root, "README.md", "readme")
	writeArtifactTestFile(t, root, "content/value.txt", "value")

	metadata := ArtifactMetadataPaths{
		DetachedSignaturePath: "module.yaml.sig",
		InstallReceiptPath:    `.freeagent\install-receipt.json`,
	}
	files, err := ScanArtifactDirectory(root, metadata)
	if err != nil {
		t.Fatalf("ScanArtifactDirectory: %v", err)
	}
	want := []ArtifactFile{
		{Path: "README.md", Content: []byte("readme")},
		{Path: "content/value.txt", Content: []byte("value")},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("ScanArtifactDirectory = %#v, want %#v", files, want)
	}

	scannedDigest, err := ComputeArtifactDigestFromDirectory(
		artifactTestManifest,
		root,
		metadata,
	)
	if err != nil {
		t.Fatalf("ComputeArtifactDigestFromDirectory: %v", err)
	}
	pureDigest, err := ComputeArtifactDigest(artifactTestManifest, want)
	if err != nil {
		t.Fatalf("ComputeArtifactDigest: %v", err)
	}
	if scannedDigest != pureDigest {
		t.Fatalf("directory digest %s != pure digest %s", scannedDigest, pureDigest)
	}

	files[0].Content[0] = 'X'
	again, err := ScanArtifactDirectory(root, metadata)
	if err != nil {
		t.Fatalf("second ScanArtifactDirectory: %v", err)
	}
	if string(again[0].Content) != "readme" {
		t.Fatal("returned scanner content aliases a previous result")
	}
}

func TestScanArtifactDirectoryRequiresDeclaredMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "README.md", "readme")
	if _, err := ScanArtifactDirectory(root, ArtifactMetadataPaths{}); err == nil ||
		!strings.Contains(err.Error(), "module.yaml") {
		t.Fatalf("missing module.yaml error = %v", err)
	}

	writeArtifactTestFile(t, root, "module.yaml", "manifest")
	if _, err := ScanArtifactDirectory(root, ArtifactMetadataPaths{
		DetachedSignaturePath: "missing.sig",
	}); err == nil || !strings.Contains(err.Error(), "missing.sig") {
		t.Fatalf("missing declared signature error = %v", err)
	}
}

func TestScanArtifactDirectoryRejectsHardlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "module.yaml", "manifest")
	original := filepath.Join(root, "payload.txt")
	writeArtifactTestFile(t, root, "payload.txt", "payload")
	if err := os.Link(original, filepath.Join(root, "payload-copy.txt")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if _, err := ScanArtifactDirectory(root, ArtifactMetadataPaths{}); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "hardlink") {
		t.Fatalf("hardlink error = %v", err)
	}
}

func TestScanArtifactDirectoryRejectsSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "module.yaml", "manifest")
	target := filepath.Join(root, "target.txt")
	writeArtifactTestFile(t, root, "target.txt", "target")
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := ScanArtifactDirectory(root, ArtifactMetadataPaths{}); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestArtifactRootedTraversalDoesNotEscapeReplacedDescendant(t *testing.T) {
	container := t.TempDir()
	outside := filepath.Join(container, "outside")
	writeArtifactTestFile(t, outside, "marker.txt", "external-marker")

	newRoot := func(t *testing.T) string {
		t.Helper()
		root := filepath.Join(container, strings.ReplaceAll(t.Name(), "/", "-"))
		writeArtifactTestFile(t, root, ArtifactManifestPath, string(artifactTestManifest))
		writeArtifactTestFile(t, root, "nested/marker.txt", "internal-marker")
		return root
	}
	newSwapHook := func(
		t *testing.T,
		root string,
	) (artifactAccessHooks, func() bool, func()) {
		t.Helper()
		var fired bool
		var restore func()
		hooks := artifactAccessHooks{
			afterPathInspect: func(path string, info os.FileInfo) error {
				if fired || path != "nested" || !info.IsDir() {
					return nil
				}
				fired = true
				var err error
				restore, err = replaceArtifactTestDirectoryWithExternalLink(
					filepath.Join(root, "nested"),
					outside,
				)
				return err
			},
		}
		return hooks, func() bool { return fired }, func() {
			if restore != nil {
				restore()
			}
		}
	}

	t.Run("scan", func(t *testing.T) {
		root := newRoot(t)
		hooks, fired, restore := newSwapHook(t, root)
		files, err := scanArtifactDirectoryContextWithHooks(
			context.Background(),
			root,
			ArtifactMetadataPaths{},
			DefaultArtifactScanLimits(),
			hooks,
		)
		restore()
		if !fired() {
			t.Fatal("descendant replacement hook did not run")
		}
		for _, file := range files {
			if bytes.Contains(file.Content, []byte("external-marker")) {
				t.Fatalf("scan escaped root and returned external marker: %v", err)
			}
		}
	})

	t.Run("digest verifier", func(t *testing.T) {
		root := newRoot(t)
		externalDigest, err := ComputeArtifactDigest(
			artifactTestManifest,
			[]ArtifactFile{{Path: "nested/marker.txt", Content: []byte("external-marker")}},
		)
		if err != nil {
			t.Fatal(err)
		}
		hooks, fired, restore := newSwapHook(t, root)
		err = verifyArtifactDirectoryDigestContextWithHooks(
			context.Background(),
			root,
			ArtifactMetadataPaths{},
			externalDigest,
			0,
			DefaultArtifactScanLimits(),
			hooks,
		)
		restore()
		if !fired() {
			t.Fatal("descendant replacement hook did not run")
		}
		if err == nil {
			t.Fatal("digest verifier accepted content reached through an external link")
		}
	})

	t.Run("direct ordinary file read", func(t *testing.T) {
		root := newRoot(t)
		hooks, fired, restore := newSwapHook(t, root)
		content, err := readArtifactOrdinaryFileFromDirectoryContextWithHooks(
			context.Background(),
			root,
			"nested/marker.txt",
			1024,
			hooks,
		)
		restore()
		if !fired() {
			t.Fatal("descendant replacement hook did not run")
		}
		if bytes.Contains(content, []byte("external-marker")) {
			t.Fatalf("direct read escaped root: %v", err)
		}
	})
}

func TestArtifactRootedTraversalRejectsSameRootDirectoryReplacement(t *testing.T) {
	newFixture := func(t *testing.T) (root, nested, target string) {
		t.Helper()
		root = t.TempDir()
		nested = filepath.Join(root, "nested")
		target = filepath.Join(root, "z-target")
		writeArtifactTestFile(t, root, ArtifactManifestPath, string(artifactTestManifest))
		writeArtifactTestFile(t, nested, "marker.txt", "original-marker")
		writeArtifactTestFile(t, target, "marker.txt", "same-root-target")
		return root, nested, target
	}
	type replacementState struct {
		attempted      bool
		followedTarget bool
		hookErr        error
		restore        func()
	}
	newHooks := func(
		t *testing.T,
		root, nested, target string,
	) (artifactAccessHooks, *replacementState) {
		t.Helper()
		rootInfo, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		targetInfo, err := os.Stat(filepath.Join(target, "marker.txt"))
		if err != nil {
			t.Fatal(err)
		}
		forcedRootStamp := rootInfo.ModTime().Add(2 * time.Second)
		state := &replacementState{}
		hooks := artifactAccessHooks{afterPathInspect: func(path string, info os.FileInfo) error {
			if path == "nested/marker.txt" && os.SameFile(info, targetInfo) {
				state.followedTarget = true
			}
			if state.attempted || path != "nested" || !info.IsDir() {
				return nil
			}
			state.attempted = true
			state.restore, state.hookErr = replaceArtifactTestDirectoryWithExternalLink(nested, target)
			if state.hookErr == nil {
				state.hookErr = os.Chtimes(root, forcedRootStamp, forcedRootStamp)
			}
			return state.hookErr
		}}
		return hooks, state
	}
	restoreSwap := func(state *replacementState) {
		if state.restore != nil {
			state.restore()
		}
	}
	assertSwap := func(t *testing.T, state *replacementState) {
		t.Helper()
		if !state.attempted {
			t.Fatal("same-root directory replacement hook did not run")
		}
		if state.hookErr != nil {
			t.Fatalf("same-root directory replacement hook: %v", state.hookErr)
		}
		if state.followedTarget {
			t.Fatal("rooted traversal followed the same-root replacement")
		}
	}

	t.Run("scan", func(t *testing.T) {
		root, nested, target := newFixture(t)
		hooks, state := newHooks(t, root, nested, target)
		files, err := scanArtifactDirectoryContextWithHooks(
			context.Background(),
			root,
			ArtifactMetadataPaths{},
			DefaultArtifactScanLimits(),
			hooks,
		)
		restoreSwap(state)
		assertSwap(t, state)
		if err == nil || files != nil {
			t.Fatalf("same-root directory replacement scan = %#v, %v", files, err)
		}
	})

	t.Run("digest verifier", func(t *testing.T) {
		root, nested, target := newFixture(t)
		targetDigest, err := ComputeArtifactDigest(
			artifactTestManifest,
			[]ArtifactFile{
				{Path: "nested/marker.txt", Content: []byte("same-root-target")},
				{Path: "z-target/marker.txt", Content: []byte("same-root-target")},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		hooks, state := newHooks(t, root, nested, target)
		err = verifyArtifactDirectoryDigestContextWithHooks(
			context.Background(),
			root,
			ArtifactMetadataPaths{},
			targetDigest,
			0,
			DefaultArtifactScanLimits(),
			hooks,
		)
		restoreSwap(state)
		assertSwap(t, state)
		if err == nil {
			t.Fatal("digest verifier accepted a same-root replacement")
		}
	})

	t.Run("direct ordinary file read", func(t *testing.T) {
		root, nested, target := newFixture(t)
		hooks, state := newHooks(t, root, nested, target)
		content, err := readArtifactOrdinaryFileFromDirectoryContextWithHooks(
			context.Background(),
			root,
			"nested/marker.txt",
			1024,
			hooks,
		)
		restoreSwap(state)
		assertSwap(t, state)
		if err == nil || bytes.Contains(content, []byte("same-root-target")) {
			t.Fatalf("direct read accepted same-root replacement: %q, %v", content, err)
		}
	})
}

func TestArtifactRootHandleRejectsOrPinsReplacedRoot(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	externalManifest := []byte(
		`{"api_version":"v1","id":"outside.evil","version":"1.0.0"}`,
	)
	writeArtifactTestFile(t, outside, ArtifactManifestPath, string(externalManifest))

	for _, test := range []struct {
		name      string
		makeHooks func(func(string) error) artifactAccessHooks
		afterOpen bool
	}{
		{
			name: "replacement before handle open is rejected",
			makeHooks: func(replace func(string) error) artifactAccessHooks {
				return artifactAccessHooks{afterRootInspect: replace}
			},
		},
		{
			name: "replacement after handle open stays pinned",
			makeHooks: func(replace func(string) error) artifactAccessHooks {
				return artifactAccessHooks{afterRootOpen: replace}
			},
			afterOpen: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			container := t.TempDir()
			root := filepath.Join(container, "root")
			writeArtifactTestFile(t, root, ArtifactManifestPath, string(artifactTestManifest))
			var restore func()
			hooks := test.makeHooks(func(path string) error {
				var err error
				restore, err = replaceArtifactTestDirectoryWithExternalLink(path, outside)
				return err
			})
			manifest, err := readArtifactManifestV1FromDirectoryContextWithHooks(
				context.Background(),
				root,
				int64(MaxTextBytes),
				hooks,
			)
			if restore != nil {
				restore()
			}
			if bytes.Equal(manifest, externalManifest) {
				t.Fatalf("root replacement escaped to external manifest: %v", err)
			}
			if test.afterOpen {
				// Some platforms prevent renaming an open directory. If the
				// replacement succeeds, the handle must remain pinned; if the
				// replacement itself fails, the operation still fails closed.
				if err == nil && !bytes.Equal(manifest, artifactTestManifest) {
					t.Fatalf("pinned root read = %q, %v", manifest, err)
				}
			} else if err == nil {
				t.Fatal("root replacement before OpenRoot was not rejected")
			}
		})
	}
}

func TestArtifactMetadataPathsRejectAmbiguousExclusions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "module.yaml", "manifest")
	for _, metadata := range []ArtifactMetadataPaths{
		{DetachedSignaturePath: "MODULE.YAML"},
		{
			DetachedSignaturePath: "metadata.sig",
			InstallReceiptPath:    "METADATA.SIG",
		},
		{InstallReceiptPath: "../outside.receipt"},
	} {
		if _, err := ScanArtifactDirectory(root, metadata); err == nil {
			t.Fatalf("accepted ambiguous metadata paths: %#v", metadata)
		}
	}
}

func TestScanArtifactDirectoryEnforcesPathFileAndByteLimits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(t, root, "module.yaml", "manifest")
	writeArtifactTestFile(t, root, "content/value.txt", "value")

	base := ArtifactScanLimits{
		MaxPaths:      8,
		MaxFiles:      4,
		MaxFileBytes:  16,
		MaxTotalBytes: 32,
	}
	tests := []struct {
		name   string
		limits ArtifactScanLimits
		want   string
	}{
		{
			name:   "paths",
			limits: ArtifactScanLimits{MaxPaths: 1, MaxFiles: 4, MaxFileBytes: 16, MaxTotalBytes: 32},
			want:   "path entries",
		},
		{
			name:   "files",
			limits: ArtifactScanLimits{MaxPaths: 8, MaxFiles: 1, MaxFileBytes: 16, MaxTotalBytes: 32},
			want:   "ordinary files",
		},
		{
			name:   "single file",
			limits: ArtifactScanLimits{MaxPaths: 8, MaxFiles: 4, MaxFileBytes: 7, MaxTotalBytes: 32},
			want:   "byte limits",
		},
		{
			name:   "total bytes",
			limits: ArtifactScanLimits{MaxPaths: 8, MaxFiles: 4, MaxFileBytes: 16, MaxTotalBytes: 10},
			want:   "byte limits",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ScanArtifactDirectoryWithLimits(
				root,
				ArtifactMetadataPaths{},
				test.limits,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("limit error=%v, want %q", err, test.want)
			}
		})
	}
	if _, err := ScanArtifactDirectoryWithLimits(
		root,
		ArtifactMetadataPaths{},
		base,
	); err != nil {
		t.Fatalf("bounded scan rejected valid package: %v", err)
	}
}

func TestScanArtifactDirectoryContextPreservesCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArtifactTestFile(
		t,
		root,
		ArtifactManifestPath,
		string(artifactTestManifest),
	)
	ctx, cancel := newCancelAfterErrChecksContext(1)
	t.Cleanup(cancel)
	files, err := ScanArtifactDirectoryContext(
		ctx,
		root,
		ArtifactMetadataPaths{},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanArtifactDirectoryContext() error = %v", err)
	}
	if files != nil {
		t.Fatalf("cancelled scan returned partial files = %#v", files)
	}
	if _, err := ScanArtifactDirectoryContext(
		nil,
		root,
		ArtifactMetadataPaths{},
	); err == nil {
		t.Fatal("ScanArtifactDirectoryContext() accepted a nil context")
	}
}

func TestStreamArtifactReaderCancelsBetweenChunks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	content := bytes.Repeat([]byte("x"), 96<<10)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := newCancelAfterErrChecksContext(2)
	t.Cleanup(cancel)
	_, captured, readBytes, err := streamArtifactReader(
		ctx,
		"large.bin",
		info,
		int64(len(content)),
		true,
		bytes.NewReader(content),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("streamArtifactReader() error = %v", err)
	}
	if readBytes <= 0 || readBytes >= int64(len(content)) {
		t.Fatalf("cancelled read bytes = %d, size = %d", readBytes, len(content))
	}
	if captured != nil {
		t.Fatalf("cancelled read returned partial content of %d bytes", len(captured))
	}
}

func TestVerifyArtifactDirectoryDigestDetectsWholePackageDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestRaw, err := json.Marshal(ModuleManifestV1{
		APIVersion: ModuleManifestAPIVersionV1,
		ID:         "example.mcp.verify",
		Version:    "1.0.0",
		Runtime: RuntimeRequestV1{
			Mode:       RuntimeModeRequestLocalProcess,
			Protocol:   RuntimeProtocolMCPStdio20251125,
			Entrypoint: "content/host.json",
		},
		Provides: []PortRef{{Name: "action.provider", ExactVersion: "v1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := CanonicalJSON(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactTestFile(t, root, ArtifactManifestPath, string(manifest))
	writeArtifactTestFile(t, root, "content/host.json", `{}`)
	writeArtifactTestFile(t, root, "content/sibling.txt", "stable")
	files, err := ScanArtifactDirectory(root, ArtifactMetadataPaths{})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifactDirectoryDigest(
		root,
		ArtifactMetadataPaths{},
		digest,
	); err != nil {
		t.Fatalf("VerifyArtifactDirectoryDigest: %v", err)
	}
	expectedSize := uint64(len(manifest) + len(`{}`) + len("stable"))
	if err := VerifyArtifactDirectoryDigestAndSizeContext(
		context.Background(),
		root,
		ArtifactMetadataPaths{},
		digest,
		expectedSize,
	); err != nil {
		t.Fatalf("VerifyArtifactDirectoryDigestAndSizeContext: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := VerifyArtifactDirectoryDigestContext(
		ctx,
		root,
		ArtifactMetadataPaths{},
		digest,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification error=%v", err)
	}
	descriptor, err := ReadArtifactOrdinaryFileFromDirectoryContext(
		context.Background(),
		root,
		"content/host.json",
		64<<10,
	)
	if err != nil || string(descriptor) != `{}` {
		t.Fatalf("bounded descriptor=%q error=%v", descriptor, err)
	}
	writeArtifactTestFile(t, root, "content/sibling.txt", "drift")
	if err := VerifyArtifactDirectoryDigest(
		root,
		ArtifactMetadataPaths{},
		digest,
	); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("whole-package drift error=%v", err)
	}
}

func writeArtifactTestFile(
	t *testing.T,
	root string,
	relative string,
	content string,
) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test artifact %q: %v", relative, err)
	}
}

func replaceArtifactTestDirectoryWithExternalLink(
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
