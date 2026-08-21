package currentbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/safefiletree"
	"github.com/endview/freeagent/sdk/moduleapi"
	"golang.org/x/text/cases"
)

type fileIdentity struct {
	SizeBytes int64
	SHA256    string
}

type safeTree struct {
	files                 map[string]fileIdentity
	directories           map[string]struct{}
	rootInfo              os.FileInfo
	artifactCount         uint64
	artifactPathCount     uint64
	artifactFileCount     uint64
	artifactPathNameBytes uint64
}

type safeTreeAccessHooks struct {
	afterRootInspect func(string) error
	afterRootOpen    func(string) error
	afterPathInspect func(string, os.FileInfo) error
}

type safeTreeReadPlan struct {
	capture                       map[string]int64
	copies                        map[string]safeTreeCopyTarget
	allowArtifactRootLeaseControl bool
	allowAggregateArtifactBytes   bool
}

type safeTreeCopyTarget struct {
	destination string
	expected    fileIdentity
}

const (
	boundedRegularFileReadChunkBytes = 32 << 10
	maxBundleSafeTreeEntriesV1       = int(
		moduleartifactstore.MaxPhysicalPathsV1 +
			moduleartifactstore.MaxPhysicalArtifactsV1 + 3,
	)
)

func resolveExistingRegularFile(input, label string) (string, error) {
	absolute, err := resolvePath(input, label)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: inspect %s: %v", ErrInvalidInput, label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf(
			"%w: %s must be an ordinary non-symlink file",
			ErrInvalidInput,
			label,
		)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %v", ErrInvalidInput, label, err)
	}
	if !samePath(absolute, resolved) {
		return "", fmt.Errorf(
			"%w: %s path traverses a symlink or reparse point",
			ErrInvalidInput,
			label,
		)
	}
	return absolute, nil
}

func resolveExistingDirectory(input, label string) (string, error) {
	absolute, err := resolvePath(input, label)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: inspect %s: %v", ErrInvalidInput, label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf(
			"%w: %s must be a non-symlink directory",
			ErrInvalidInput,
			label,
		)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %v", ErrInvalidInput, label, err)
	}
	if !samePath(absolute, resolved) {
		return "", fmt.Errorf(
			"%w: %s path traverses a symlink or reparse point",
			ErrInvalidInput,
			label,
		)
	}
	return absolute, nil
}

func resolveNewPath(input, label string) (string, error) {
	absolute, err := resolvePath(input, label)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("%w: %s", ErrTargetExists, absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: inspect %s: %v", ErrInvalidInput, label, err)
	}
	parent := filepath.Dir(absolute)
	resolvedParent, err := resolveExistingDirectory(parent, label+" parent")
	if err != nil {
		return "", err
	}
	if !samePath(parent, resolvedParent) {
		return "", fmt.Errorf(
			"%w: %s parent path is not stable",
			ErrInvalidInput,
			label,
		)
	}
	return absolute, nil
}

func resolvePath(input, label string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("%w: %s path is required", ErrInvalidInput, label)
	}
	if strings.ContainsAny(input, "?#") {
		return "", fmt.Errorf(
			"%w: %s must be a filesystem path, not a URI",
			ErrInvalidInput,
			label,
		)
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %v", ErrInvalidInput, label, err)
	}
	return filepath.Clean(absolute), nil
}

func samePath(left, right string) bool {
	left, _ = filepath.Abs(left)
	right, _ = filepath.Abs(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func hashRegularFile(path string, limit int64) (fileIdentity, error) {
	return hashRegularFileContext(context.Background(), path, limit)
}

func hashRegularFileContext(
	ctx context.Context,
	path string,
	limit int64,
) (fileIdentity, error) {
	if ctx == nil {
		return fileIdentity{}, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	file, before, err := openStableRegularFile(path, limit)
	if err != nil {
		return fileIdentity{}, err
	}
	defer file.Close()
	return hashOpenedRegularFileContext(ctx, file, before, limit)
}

func hashOpenedRegularFileContext(
	ctx context.Context,
	file *os.File,
	before os.FileInfo,
	limit int64,
) (fileIdentity, error) {
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return fileIdentity{}, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			written += int64(count)
			if written > limit {
				return fileIdentity{}, fmt.Errorf("%w: file exceeds limit", ErrIntegrity)
			}
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fileIdentity{}, fmt.Errorf("hash regular file: %w", readErr)
		}
	}
	if written != before.Size() || written > limit {
		return fileIdentity{}, fmt.Errorf("%w: file size changed or exceeds limit", ErrIntegrity)
	}
	if err := verifyStableOpenedFile(file, before); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{
		SizeBytes: written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	return readBoundedRegularFileContext(context.Background(), path, limit)
}

func readBoundedRegularFileContext(
	ctx context.Context,
	path string,
	limit int64,
) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, before, err := openStableRegularFile(path, limit)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := readBoundedReaderContext(ctx, file, limit)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) != before.Size() || int64(len(content)) > limit {
		return nil, fmt.Errorf("%w: file size changed or exceeds limit", ErrIntegrity)
	}
	if err := verifyStableOpenedFile(file, before); err != nil {
		return nil, err
	}
	// readBoundedReaderContext already returns detached caller-owned bytes.
	// Returning them directly avoids a second full-size allocation without
	// exposing either its temporary buffer or the opened file.
	return content, nil
}

func readBoundedReaderContext(
	ctx context.Context,
	reader io.Reader,
	limit int64,
) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if reader == nil {
		return nil, fmt.Errorf("%w: reader is nil", ErrInvalidInput)
	}
	if limit < 0 {
		return nil, fmt.Errorf("%w: file exceeds limit", ErrIntegrity)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limited := &io.LimitedReader{R: reader, N: limit + 1}
	content := bytes.NewBuffer(nil)
	buffer := make([]byte, boundedRegularFileReadChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, readErr := limited.Read(buffer)
		if count > 0 {
			_, _ = content.Write(buffer[:count])
			if int64(content.Len()) > limit {
				return nil, fmt.Errorf("%w: file exceeds limit", ErrIntegrity)
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read regular file: %w", readErr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return bytes.Clone(content.Bytes()), nil
}

func openStableRegularFile(
	path string,
	limit int64,
) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	return openStableRegularFileWithInfo(
		path,
		before,
		limit,
		func() (*os.File, error) { return os.Open(path) },
	)
}

func openStableRegularFileWithInfo(
	label string,
	before os.FileInfo,
	limit int64,
	open func() (*os.File, error),
) (*os.File, os.FileInfo, error) {
	if err := rejectSafeTreePlatformSpecial(before); err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		before.Size() < 0 || before.Size() > limit {
		return nil, nil, fmt.Errorf(
			"%w: path %q is not a bounded ordinary file",
			ErrIntegrity,
			label,
		)
	}
	file, err := open()
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		file.Close()
		return nil, nil, fmt.Errorf("%w: file changed while opening", ErrIntegrity)
	}
	links, err := openedFileLinkCount(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if links != 1 {
		file.Close()
		return nil, nil, fmt.Errorf(
			"%w: hard-linked file %q is forbidden",
			ErrIntegrity,
			label,
		)
	}
	return file, opened, nil
}

func verifyStableOpenedFile(file *os.File, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("%w: file changed while it was read", ErrIntegrity)
	}
	return nil
}

func copyRegularFile(
	ctx context.Context,
	source string,
	destination string,
	expected fileIdentity,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	input, before, err := openStableRegularFile(source, expected.SizeBytes)
	if err != nil {
		return err
	}
	return copyOpenedRegularFile(ctx, input, before, destination, expected)
}

func copyOpenedRegularFile(
	ctx context.Context,
	input *os.File,
	before os.FileInfo,
	destination string,
	expected fileIdentity,
) error {
	defer input.Close()
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = output.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := input.Read(buffer)
		if count > 0 {
			copied += int64(count)
			if copied > expected.SizeBytes {
				return fmt.Errorf("%w: source grew while copying", ErrIntegrity)
			}
			_, _ = hash.Write(buffer[:count])
			if _, err := output.Write(buffer[:count]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if copied != expected.SizeBytes ||
		hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("%w: copied file does not match expected identity", ErrIntegrity)
	}
	if err := verifyStableOpenedFile(input, before); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func writeExclusiveSyncedFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func syncRegularFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func walkSafeTree(root string) (safeTree, error) {
	return walkSafeTreeContext(context.Background(), root)
}

func walkSafeTreeContext(ctx context.Context, root string) (safeTree, error) {
	tree, _, err := walkSafeTreeWithPlan(
		ctx,
		root,
		false,
		safeTreeAccessHooks{},
		safeTreeReadPlan{},
	)
	return tree, err
}

func walkBundleSafeTreeContext(ctx context.Context, root string) (safeTree, error) {
	tree, _, err := walkSafeTreeWithPlan(
		ctx,
		root,
		true,
		safeTreeAccessHooks{},
		safeTreeReadPlan{},
	)
	return tree, err
}

func walkSafeTreeWithDatabase(
	ctx context.Context,
	root string,
	allowDatabase bool,
) (safeTree, error) {
	tree, _, err := walkSafeTreeWithPlan(
		ctx,
		root,
		allowDatabase,
		safeTreeAccessHooks{},
		safeTreeReadPlan{},
	)
	return tree, err
}

func walkSafeTreeWithDatabaseHooks(
	ctx context.Context,
	root string,
	allowDatabase bool,
	hooks safeTreeAccessHooks,
) (safeTree, error) {
	tree, _, err := walkSafeTreeWithPlan(
		ctx,
		root,
		allowDatabase,
		hooks,
		safeTreeReadPlan{},
	)
	return tree, err
}

func walkSafeTreeWithPlan(
	ctx context.Context,
	root string,
	allowDatabase bool,
	hooks safeTreeAccessHooks,
	plan safeTreeReadPlan,
) (safeTree, map[string][]byte, error) {
	if ctx == nil {
		return safeTree{}, nil, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return safeTree{}, nil, err
	}
	absolute, err := resolvePath(root, "bundle root")
	if err != nil {
		return safeTree{}, nil, err
	}
	before, err := os.Lstat(absolute)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return safeTree{}, nil, fmt.Errorf(
			"%w: unsafe bundle tree: bundle root must be a non-symlink directory",
			ErrInvalidBundle,
		)
	}
	if err := rejectSafeTreePlatformSpecial(before); err != nil {
		return safeTree{}, nil, fmt.Errorf("%w: unsafe bundle tree: %w", ErrInvalidBundle, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !samePath(absolute, resolved) {
		return safeTree{}, nil, fmt.Errorf(
			"%w: unsafe bundle tree: bundle root must not traverse a symlink or reparse point",
			ErrInvalidBundle,
		)
	}
	if hooks.afterRootInspect != nil {
		if err := hooks.afterRootInspect(absolute); err != nil {
			return safeTree{}, nil, err
		}
	}
	for path, limit := range plan.capture {
		if err := validateSafeTreePlanPath(path); err != nil || limit < 0 {
			return safeTree{}, nil, fmt.Errorf(
				"%w: invalid safe-tree capture path %q",
				ErrInvalidInput,
				path,
			)
		}
	}
	for path, target := range plan.copies {
		if err := validateSafeTreePlanPath(path); err != nil ||
			target.destination == "" || target.expected.SizeBytes < 0 ||
			!moduleapi.ValidSHA256(target.expected.SHA256) {
			return safeTree{}, nil, fmt.Errorf(
				"%w: invalid safe-tree copy path %q",
				ErrInvalidInput,
				path,
			)
		}
	}
	tree := safeTree{
		files:       make(map[string]fileIdentity),
		directories: make(map[string]struct{}),
	}
	captured := make(map[string][]byte, len(plan.capture))
	copied := make(map[string]struct{}, len(plan.copies))
	folded := make(map[string]string)
	folder := cases.Fold()
	fileCount := 0
	var totalBytes int64
	totalLimit := maxArtifactBytes
	if plan.allowAggregateArtifactBytes {
		totalLimit = maxArtifactAggregateBytes
	}
	if allowDatabase {
		totalLimit = maxArtifactAggregateBytes + maxDatabaseBytes + maxManifestBytes
	}
	maxWalkEntries := moduleapi.DefaultArtifactMaxPaths
	if allowDatabase {
		maxWalkEntries = maxBundleSafeTreeEntriesV1
	}
	err = safefiletree.WalkWithOptions(ctx, absolute, safefiletree.Options{
		MaxEntries:             maxWalkEntries,
		MaxEntriesPerDirectory: moduleapi.DefaultArtifactMaxPaths,
		Visit: func(ctx context.Context, entry safefiletree.Entry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Path == "." {
				if entry.Kind != safefiletree.KindDirectory || entry.Reader != nil ||
					entry.Info == nil || !entry.Info.IsDir() ||
					!os.SameFile(before, entry.Info) {
					return fmt.Errorf("bundle root changed while it was opened")
				}
				if err := rejectSafeTreePlatformSpecial(entry.Info); err != nil {
					return err
				}
				tree.rootInfo = entry.Info
				if hooks.afterRootOpen != nil {
					if err := hooks.afterRootOpen(absolute); err != nil {
						return err
					}
				}
				return nil
			}
			normalized, err := moduleapi.NormalizeArtifactPath(entry.Path)
			if err != nil {
				return err
			}
			if normalized != entry.Path {
				return fmt.Errorf("noncanonical bundle path %q", entry.Path)
			}
			if allowDatabase {
				if err := validateBundleTreePathV1(normalized); err != nil {
					return err
				}
			}
			fold := folder.String(normalized)
			if previous, collision := folded[fold]; collision && previous != normalized {
				return fmt.Errorf(
					"case-fold collision between %q and %q",
					previous,
					normalized,
				)
			}
			folded[fold] = normalized
			info := entry.Info
			if info == nil {
				return fmt.Errorf("path %q has no opened identity", normalized)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink %q is forbidden", normalized)
			}
			if err := rejectSafeTreePlatformSpecial(info); err != nil {
				return fmt.Errorf("path %q: %w", normalized, err)
			}
			if err := rejectSafeTreeFilesystemBoundary(tree.rootInfo, info); err != nil {
				return fmt.Errorf("path %q: %w", normalized, err)
			}
			if allowDatabase {
				if err := accountBundleArtifactNamespaceV1(&tree, normalized, entry.Kind); err != nil {
					return err
				}
			}
			if hooks.afterPathInspect != nil {
				if err := hooks.afterPathInspect(normalized, info); err != nil {
					return err
				}
			}
			if entry.Kind == safefiletree.KindDirectory {
				if !info.IsDir() || entry.Reader != nil {
					return fmt.Errorf("directory %q has an inconsistent opened kind", normalized)
				}
				tree.directories[normalized] = struct{}{}
				return nil
			}
			if entry.Kind != safefiletree.KindRegularFile ||
				!info.Mode().IsRegular() || entry.Reader == nil {
				return fmt.Errorf("non-ordinary path %q is forbidden", normalized)
			}
			if plan.allowArtifactRootLeaseControl &&
				moduleartifactstore.IsArtifactRootControlEntryV1(normalized) {
				// The persistent writer lease may be range-locked by this same restore
				// callback on Windows. Only the caller that is scanning the physical
				// artifact-root namespace may opt into this exception. A same-named file
				// inside an individual module artifact remains ordinary digest-covered
				// content and must always be read and hashed.
				tree.files[normalized] = fileIdentity{SizeBytes: info.Size()}
				return nil
			}
			fileCount++
			if fileCount > maxArtifactFiles {
				return fmt.Errorf("bundle file count exceeds %d", maxArtifactFiles)
			}
			limit := maxArtifactFileBytes
			if allowDatabase && normalized == databaseName {
				limit = maxDatabaseBytes
			}
			captureLimit, capture := plan.capture[normalized]
			copyTarget, copyFile := plan.copies[normalized]
			identity, content, err := consumeSafeTreeRegularFileContext(
				ctx,
				entry,
				limit,
				capture,
				captureLimit,
				copyFile,
				copyTarget,
			)
			if err != nil {
				return err
			}
			if identity.SizeBytes > totalLimit-totalBytes {
				return fmt.Errorf("bundle byte size exceeds verification limit")
			}
			totalBytes += identity.SizeBytes
			tree.files[normalized] = identity
			if capture {
				captured[normalized] = content
			}
			if copyFile {
				copied[normalized] = struct{}{}
			}
			return nil
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return safeTree{}, nil, err
		}
		return safeTree{}, nil, fmt.Errorf("%w: unsafe bundle tree: %w", ErrInvalidBundle, err)
	}
	if tree.rootInfo == nil {
		return safeTree{}, nil, fmt.Errorf("%w: unsafe bundle tree: root was not visited", ErrInvalidBundle)
	}
	for path := range plan.copies {
		if _, ok := copied[path]; !ok {
			return safeTree{}, nil, fmt.Errorf(
				"%w: planned file %q is absent",
				ErrIntegrity,
				path,
			)
		}
	}
	return tree, captured, nil
}

func validateBundleTreePathV1(normalized string) error {
	if normalized == manifestName || normalized == databaseName ||
		normalized == artifactsDirectory ||
		strings.HasPrefix(normalized, artifactsDirectory+"/") {
		return nil
	}
	return fmt.Errorf("unexpected bundle path %q", normalized)
}

func accountBundleArtifactNamespaceV1(
	tree *safeTree,
	normalized string,
	kind safefiletree.Kind,
) error {
	if tree == nil || normalized == artifactsDirectory ||
		!strings.HasPrefix(normalized, artifactsDirectory+"/") {
		return nil
	}
	remainder := strings.TrimPrefix(normalized, artifactsDirectory+"/")
	components := strings.Split(remainder, "/")
	if len(components) == 1 {
		tree.artifactCount++
		if tree.artifactCount > moduleartifactstore.MaxPhysicalArtifactsV1 {
			return fmt.Errorf("artifact root count exceeds physical quota")
		}
		return nil
	}
	relative := strings.Join(components[1:], "/")
	tree.artifactPathCount++
	if tree.artifactPathCount > moduleartifactstore.MaxPhysicalPathsV1 {
		return fmt.Errorf("artifact path count exceeds physical quota")
	}
	nameBytes := uint64(len(relative))
	if tree.artifactPathNameBytes > moduleartifactstore.MaxPhysicalPathBytesV1 ||
		nameBytes > moduleartifactstore.MaxPhysicalPathBytesV1-tree.artifactPathNameBytes {
		return fmt.Errorf("artifact path-name bytes exceed physical quota")
	}
	tree.artifactPathNameBytes += nameBytes
	if kind == safefiletree.KindRegularFile {
		tree.artifactFileCount++
		if tree.artifactFileCount > moduleartifactstore.MaxPhysicalFilesV1 {
			return fmt.Errorf("artifact file count exceeds physical quota")
		}
	}
	return nil
}

func validateSafeTreePlanPath(input string) error {
	normalized, err := moduleapi.NormalizeArtifactPath(input)
	if err != nil || normalized != input {
		return fmt.Errorf("noncanonical path")
	}
	return nil
}

func consumeSafeTreeRegularFileContext(
	ctx context.Context,
	entry safefiletree.Entry,
	limit int64,
	capture bool,
	captureLimit int64,
	copyFile bool,
	copyTarget safeTreeCopyTarget,
) (identity fileIdentity, captured []byte, returnErr error) {
	if entry.Reader == nil || entry.Info == nil ||
		!entry.Info.Mode().IsRegular() || entry.Info.Size() < 0 ||
		entry.Info.Size() > limit {
		return fileIdentity{}, nil, fmt.Errorf(
			"%w: path %q is not a bounded ordinary file",
			ErrIntegrity,
			entry.Path,
		)
	}
	if capture && entry.Info.Size() > captureLimit {
		return fileIdentity{}, nil, fmt.Errorf(
			"%w: path %q exceeds capture limit",
			ErrIntegrity,
			entry.Path,
		)
	}
	if copyFile && entry.Info.Size() != copyTarget.expected.SizeBytes {
		return fileIdentity{}, nil, fmt.Errorf(
			"%w: planned copy %q size differs",
			ErrIntegrity,
			entry.Path,
		)
	}

	var output *os.File
	keepOutput := false
	if copyFile {
		var err error
		output, err = os.OpenFile(
			copyTarget.destination,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0o600,
		)
		if err != nil {
			return fileIdentity{}, nil, err
		}
		defer func() {
			if !keepOutput {
				returnErr = errors.Join(returnErr, output.Close())
				returnErr = errors.Join(returnErr, os.Remove(copyTarget.destination))
			}
		}()
	}

	hash := sha256.New()
	var content bytes.Buffer
	buffer := make([]byte, 256<<10)
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return fileIdentity{}, nil, err
		}
		count, readErr := entry.Reader.Read(buffer)
		if count > 0 {
			read += int64(count)
			if read > limit || capture && read > captureLimit {
				return fileIdentity{}, nil, fmt.Errorf(
					"%w: path %q exceeds its read limit",
					ErrIntegrity,
					entry.Path,
				)
			}
			_, _ = hash.Write(buffer[:count])
			if capture {
				_, _ = content.Write(buffer[:count])
			}
			if output != nil {
				if _, err := output.Write(buffer[:count]); err != nil {
					return fileIdentity{}, nil, err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fileIdentity{}, nil, fmt.Errorf("read regular file: %w", readErr)
		}
	}
	if read != entry.Info.Size() {
		return fileIdentity{}, nil, fmt.Errorf(
			"%w: path %q size changed while it was read",
			ErrIntegrity,
			entry.Path,
		)
	}
	identity = fileIdentity{
		SizeBytes: read,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}
	if copyFile && identity != copyTarget.expected {
		return fileIdentity{}, nil, fmt.Errorf(
			"%w: planned copy %q identity differs",
			ErrIntegrity,
			entry.Path,
		)
	}
	if output != nil {
		if err := output.Sync(); err != nil {
			return fileIdentity{}, nil, err
		}
		if err := output.Close(); err != nil {
			return fileIdentity{}, nil, err
		}
		keepOutput = true
	}
	if capture {
		captured = bytes.Clone(content.Bytes())
	}
	return identity, captured, nil
}

func sameSafeTree(left, right safeTree) bool {
	if left.rootInfo == nil || right.rootInfo == nil ||
		!os.SameFile(left.rootInfo, right.rootInfo) ||
		left.artifactCount != right.artifactCount ||
		left.artifactPathCount != right.artifactPathCount ||
		left.artifactFileCount != right.artifactFileCount ||
		left.artifactPathNameBytes != right.artifactPathNameBytes ||
		len(left.files) != len(right.files) ||
		len(left.directories) != len(right.directories) {
		return false
	}
	for path, identity := range left.files {
		if right.files[path] != identity {
			return false
		}
	}
	for path := range left.directories {
		if _, ok := right.directories[path]; !ok {
			return false
		}
	}
	return true
}

func verifyNoSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf(
				"%w: standalone database has sidecar %s",
				ErrIntegrity,
				suffix,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

type sqliteSidecarBaseline map[string]struct{}

func captureSQLiteSidecars(path string) (sqliteSidecarBaseline, error) {
	baseline := make(sqliteSidecarBaseline)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(
				"%w: source SQLite sidecar %s is not a regular file",
				ErrIntegrity,
				suffix,
			)
		}
		baseline[suffix] = struct{}{}
	}
	return baseline, nil
}

// removeSQLiteReadResidue removes only sidecars that were absent before the
// offline read. SQLite may create an empty WAL and a shared-memory file merely
// by opening a checkpointed WAL database read-only. A non-empty new WAL or
// journal is never discarded.
func removeSQLiteReadResidue(path string, baseline sqliteSidecarBaseline) error {
	var removable []string
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		if _, existed := baseline[suffix]; existed {
			continue
		}
		candidate := path + suffix
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf(
				"%w: new SQLite sidecar %s is not a regular file",
				ErrIntegrity,
				suffix,
			)
		}
		if suffix != "-shm" && info.Size() != 0 {
			return fmt.Errorf(
				"%w: new SQLite sidecar %s contains uncheckpointed bytes",
				ErrIntegrity,
				suffix,
			)
		}
		removable = append(removable, candidate)
	}
	for _, candidate := range removable {
		if err := os.Remove(candidate); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func syncTreeDirectories(root string) error {
	return safefiletree.WalkWithOptions(
		context.Background(),
		root,
		safefiletree.Options{
			OpenForSync:            true,
			MaxEntries:             maxBundleSafeTreeEntriesV1,
			MaxEntriesPerDirectory: moduleapi.DefaultArtifactMaxPaths,
			LeaveDirectory: func(_ context.Context, entry safefiletree.Entry) error {
				if entry.Kind != safefiletree.KindDirectory {
					return fmt.Errorf("sync callback received non-directory %q", entry.Path)
				}
				return entry.Sync()
			},
		},
	)
}

func removePrivateStagingTree(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("currentbackup: private staging path is empty")
	}
	parent := filepath.Dir(path)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("currentbackup: remove private staging tree: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("currentbackup: sync private staging parent: %w", err)
	}
	return nil
}
