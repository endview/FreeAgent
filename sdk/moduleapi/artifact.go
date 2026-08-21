package moduleapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/safefiletree"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	// ArtifactDigestDomain is the CORE_RUNTIME_V1 module artifact domain.
	ArtifactDigestDomain = "freeagent.module-artifact/v1"

	// ArtifactManifestPath is represented by the canonical manifest field and
	// therefore must never also appear in the ordinary files collection.
	ArtifactManifestPath = "module.yaml"

	// Default artifact scan limits are intentionally hard security bounds, not
	// configuration hints. They keep an unpacked package from turning install,
	// activation, or selected-adapter verification into an unbounded traversal
	// or allocation. A caller that needs a smaller policy may use
	// ScanArtifactDirectoryWithLimits; no caller may exceed these defaults.
	DefaultArtifactMaxPaths      = 8192
	DefaultArtifactMaxFiles      = 4096
	DefaultArtifactMaxFileBytes  = int64(128 << 20)
	DefaultArtifactMaxTotalBytes = int64(256 << 20)
)

// ArtifactScanLimits bounds one complete directory scan. MaxPaths counts both
// directories and files below the package root; MaxFiles and byte limits count
// every ordinary file, including excluded envelope metadata. This prevents an
// attacker from hiding traversal or allocation cost in excluded files.
type ArtifactScanLimits struct {
	MaxPaths      int
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultArtifactScanLimits returns the immutable platform-independent hard
// ceiling used by the compatibility ScanArtifactDirectory API.
func DefaultArtifactScanLimits() ArtifactScanLimits {
	return ArtifactScanLimits{
		MaxPaths:      DefaultArtifactMaxPaths,
		MaxFiles:      DefaultArtifactMaxFiles,
		MaxFileBytes:  DefaultArtifactMaxFileBytes,
		MaxTotalBytes: DefaultArtifactMaxTotalBytes,
	}
}

// ArtifactFile is one ordinary module-package file covered by ArtifactDigest.
// Path is a package-relative logical path. Content is read synchronously and
// is never mutated; callers must not mutate it concurrently. module.yaml, a
// detached signature, and an installation receipt are metadata, not
// ArtifactFile values.
type ArtifactFile struct {
	Path    string
	Content []byte
}

// ArtifactFileDigest is the already-computed identity of one ordinary package
// file. It is intended for callers that have streamed and policy-checked file
// content through a handle-rooted traversal and retained only its SHA-256.
type ArtifactFileDigest struct {
	Path   string
	SHA256 string
}

// ArtifactMetadataPaths names the two optional envelope-owned metadata files
// excluded by ScanArtifactDirectory in addition to module.yaml. These paths
// must be selected by the installer/package envelope, never by the untrusted
// module manifest. A non-empty path means that the file is expected to exist.
//
// This makes exclusion explicit: exactly module.yaml and these at-most-two
// paths are excluded; every other ordinary file below the package root is
// covered by ArtifactDigest.
type ArtifactMetadataPaths struct {
	DetachedSignaturePath string
	InstallReceiptPath    string
}

type artifactFileDigestWire struct {
	NormalizedPath string `json:"normalized_path"`
	SHA256         string `json:"sha256"`
}

type artifactDigestWire struct {
	Manifest json.RawMessage          `json:"manifest"`
	Files    []artifactFileDigestWire `json:"files"`
}

type artifactPathKind uint8

const (
	artifactPathDirectory artifactPathKind = iota + 1
	artifactPathFile
)

type artifactPathRegistry struct {
	exact map[string]artifactPathKind
	fold  map[string]string
}

// artifactAccessHooks is a private deterministic seam for exercising
// filesystem replacement races. Production callers always use the zero value.
type artifactAccessHooks struct {
	afterRootInspect func(string) error
	afterRootOpen    func(string) error
	afterPathInspect func(string, os.FileInfo) error
}

// ComputeArtifactDigest implements CORE_RUNTIME_V1 section 5.2.
//
// canonicalManifest must already be the exact RFC 8785 JSON object produced
// from the minimal manifest. Digest and signature fields are not part of that
// manifest schema. ordinaryFiles must contain every remaining ordinary package
// file and no envelope metadata file. Callers reading an unpacked directory
// should prefer ComputeArtifactDigestFromDirectory so special files and hard
// links are checked before content is accepted. ComputeArtifactDigest does not
// mutate its inputs; callers must not mutate them concurrently with the call.
func ComputeArtifactDigest(
	canonicalManifest []byte,
	ordinaryFiles []ArtifactFile,
) (string, error) {
	manifest, err := canonicalArtifactDigestManifest(canonicalManifest)
	if err != nil {
		return "", err
	}
	files, err := canonicalArtifactFiles(ordinaryFiles)
	if err != nil {
		return "", err
	}
	return computeArtifactDigestFromDigests(manifest, files)
}

// ComputeArtifactDigestFromFileDigests computes ArtifactDigest from bounded,
// handle-rooted file identities without retaining or recopying package bytes.
// It applies the same path normalization, collision, metadata-exclusion, sort,
// and canonical-manifest rules as ComputeArtifactDigest. The inputs are not
// mutated and must not be mutated concurrently with the call.
func ComputeArtifactDigestFromFileDigests(
	canonicalManifest []byte,
	ordinaryFiles []ArtifactFileDigest,
) (string, error) {
	manifest, err := canonicalArtifactDigestManifest(canonicalManifest)
	if err != nil {
		return "", err
	}
	files, err := canonicalArtifactFileDigests(ordinaryFiles)
	if err != nil {
		return "", err
	}
	return computeArtifactDigestFromDigests(manifest, files)
}

func canonicalArtifactDigestManifest(input []byte) ([]byte, error) {
	manifest := bytes.Clone(input)
	normalized, err := CanonicalJSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("artifact manifest: %w", err)
	}
	if !bytes.Equal(manifest, normalized) {
		return nil, errors.New("artifact manifest must be exact RFC 8785 canonical JSON")
	}
	if len(manifest) == 0 || manifest[0] != '{' {
		return nil, errors.New("artifact manifest must be a JSON object")
	}
	return manifest, nil
}

func computeArtifactDigestFromDigests(
	canonicalManifest []byte,
	files []artifactFileDigestWire,
) (string, error) {
	wire := artifactDigestWire{
		Manifest: json.RawMessage(bytes.Clone(canonicalManifest)),
		Files:    files,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("marshal artifact digest input: %w", err)
	}
	canonical, err := CanonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("canonicalize artifact digest input: %w", err)
	}
	return Digest(ArtifactDigestDomain, canonical), nil
}

// ComputeArtifactDigestFromDirectory safely enumerates a module package and
// computes its digest. The scan rejects links and non-ordinary files and
// excludes only the metadata paths described by ArtifactMetadataPaths.
func ComputeArtifactDigestFromDirectory(
	canonicalManifest []byte,
	root string,
	metadata ArtifactMetadataPaths,
) (string, error) {
	files, err := ScanArtifactDirectory(root, metadata)
	if err != nil {
		return "", err
	}
	return ComputeArtifactDigest(
		canonicalManifest,
		files,
	)
}

// NormalizeArtifactPath converts package separators to '/', normalizes Unicode
// to NFC, and rejects paths whose interpretation could escape or vary by host.
func NormalizeArtifactPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("artifact path must not be empty")
	}
	if !utf8.ValidString(value) {
		return "", errors.New("artifact path must be valid UTF-8")
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("artifact path must not contain NUL")
	}

	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") ||
		hasWindowsDrivePrefix(normalized) ||
		filepath.IsAbs(value) {
		return "", fmt.Errorf("artifact path %q must be package-relative", value)
	}

	segments := strings.Split(normalized, "/")
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("artifact path %q contains an empty segment", value)
		}
		if segment == "." || segment == ".." {
			return "", fmt.Errorf(
				"artifact path %q contains forbidden segment %q",
				value,
				segment,
			)
		}
	}
	normalized = norm.NFC.String(strings.Join(segments, "/"))
	if normalized == "" {
		return "", errors.New("artifact path must not normalize to empty")
	}
	return normalized, nil
}

// ScanArtifactDirectory returns only ordinary files covered by ArtifactDigest.
// Returned paths are normalized and sorted; returned content is owned by the
// caller. The root itself, every directory, and every file are inspected
// without following symlinks.
func ScanArtifactDirectory(
	root string,
	metadata ArtifactMetadataPaths,
) ([]ArtifactFile, error) {
	return ScanArtifactDirectoryContext(
		context.Background(),
		root,
		metadata,
	)
}

// ScanArtifactDirectoryContext is the cancellable form of
// ScanArtifactDirectory. It checks cancellation while walking and after every
// bounded file-read chunk; returned content and ordering are otherwise
// identical to the compatibility API.
func ScanArtifactDirectoryContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
) ([]ArtifactFile, error) {
	return scanArtifactDirectoryContext(
		ctx,
		root,
		metadata,
		DefaultArtifactScanLimits(),
	)
}

// ScanArtifactDirectoryWithLimits is ScanArtifactDirectory with a caller-
// supplied policy that may only tighten the default hard ceilings.
func ScanArtifactDirectoryWithLimits(
	root string,
	metadata ArtifactMetadataPaths,
	limits ArtifactScanLimits,
) ([]ArtifactFile, error) {
	return ScanArtifactDirectoryWithLimitsContext(
		context.Background(),
		root,
		metadata,
		limits,
	)
}

// ScanArtifactDirectoryWithLimitsContext is the cancellable policy-tight
// variant. The supplied limits may only reduce the platform-independent hard
// ceilings and apply to every ordinary file, including metadata.
func ScanArtifactDirectoryWithLimitsContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	limits ArtifactScanLimits,
) ([]ArtifactFile, error) {
	return scanArtifactDirectoryContext(ctx, root, metadata, limits)
}

func scanArtifactDirectoryContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	limits ArtifactScanLimits,
) ([]ArtifactFile, error) {
	return scanArtifactDirectoryContextWithHooks(
		ctx,
		root,
		metadata,
		limits,
		artifactAccessHooks{},
	)
}

func scanArtifactDirectoryContextWithHooks(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	limits ArtifactScanLimits,
	hooks artifactAccessHooks,
) ([]ArtifactFile, error) {
	if ctx == nil {
		return nil, errors.New("artifact scan context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	absoluteRoot, rootInfo, err := inspectArtifactRoot(root)
	if err != nil {
		return nil, err
	}
	if hooks.afterRootInspect != nil {
		if err := hooks.afterRootInspect(absoluteRoot); err != nil {
			return nil, err
		}
	}

	excluded, expected, err := artifactExcludedPaths(metadata)
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool, len(expected))
	registry := newArtifactPathRegistry()
	var files []ArtifactFile
	pathCount := 0
	fileCount := 0
	totalBytes := int64(0)

	err = safefiletree.WalkWithOptions(
		ctx,
		absoluteRoot,
		safefiletree.Options{
			MaxEntries:             limits.MaxPaths,
			MaxEntriesPerDirectory: limits.MaxPaths,
			Visit: func(_ context.Context, entry safefiletree.Entry) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Path == "." {
					if !entry.Info.IsDir() || !os.SameFile(rootInfo, entry.Info) {
						return errors.New("artifact root changed while it was opened")
					}
					if err := rejectArtifactPlatformSpecial(entry.Info); err != nil {
						return fmt.Errorf("inspect opened artifact root: %w", err)
					}
					if hooks.afterRootOpen != nil {
						if err := hooks.afterRootOpen(absoluteRoot); err != nil {
							return err
						}
					}
					return nil
				}
				pathCount++
				if pathCount > limits.MaxPaths {
					return fmt.Errorf(
						"artifact exceeds %d path entries",
						limits.MaxPaths,
					)
				}
				normalized, normalizeErr := NormalizeArtifactPath(entry.Path)
				if normalizeErr != nil {
					return normalizeErr
				}

				info := entry.Info
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("artifact path %q must not be a symlink", normalized)
				}
				if platformErr := rejectArtifactPlatformSpecial(info); platformErr != nil {
					return fmt.Errorf("artifact path %q: %w", normalized, platformErr)
				}
				if boundaryErr := rejectArtifactFilesystemBoundary(rootInfo, info); boundaryErr != nil {
					return fmt.Errorf("artifact path %q: %w", normalized, boundaryErr)
				}

				kind := artifactPathFile
				if info.IsDir() {
					kind = artifactPathDirectory
				} else if !info.Mode().IsRegular() {
					return fmt.Errorf("artifact path %q is not an ordinary file", normalized)
				}
				if registerErr := registry.add(normalized, kind); registerErr != nil {
					return registerErr
				}
				if hooks.afterPathInspect != nil {
					if hookErr := hooks.afterPathInspect(normalized, info); hookErr != nil {
						return hookErr
					}
				}
				if kind == artifactPathDirectory {
					return nil
				}
				fileCount++
				if fileCount > limits.MaxFiles {
					return fmt.Errorf(
						"artifact exceeds %d ordinary files",
						limits.MaxFiles,
					)
				}
				remaining := limits.MaxTotalBytes - totalBytes
				fileLimit := limits.MaxFileBytes
				if normalized == ArtifactManifestPath {
					fileLimit = minInt64(fileLimit, int64(MaxTextBytes))
				}
				if info.Size() < 0 || info.Size() > fileLimit ||
					info.Size() > remaining {
					return fmt.Errorf(
						"artifact file %q exceeds scan byte limits",
						normalized,
					)
				}

				_, content, readBytes, readErr := streamArtifactReader(
					ctx,
					normalized,
					info,
					minInt64(fileLimit, remaining),
					true,
					entry.Reader,
				)
				if readErr != nil {
					return readErr
				}
				if readBytes > remaining {
					return fmt.Errorf(
						"artifact exceeds %d total bytes",
						limits.MaxTotalBytes,
					)
				}
				totalBytes += readBytes
				if _, skip := excluded[normalized]; skip {
					found[normalized] = true
					return nil
				}
				files = append(files, ArtifactFile{
					Path:    normalized,
					Content: content,
				})
				return nil
			},
		},
	)
	if err != nil {
		if errors.Is(err, safefiletree.ErrEntryLimit) {
			return nil, fmt.Errorf(
				"scan artifact directory: artifact exceeds %d path entries: %w",
				limits.MaxPaths,
				err,
			)
		}
		return nil, fmt.Errorf("scan artifact directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("scan artifact directory: %w", err)
	}
	for path := range expected {
		if !found[path] {
			return nil, fmt.Errorf("required artifact metadata %q is missing", path)
		}
	}
	sort.Slice(files, func(left, right int) bool {
		return bytes.Compare(
			[]byte(files[left].Path),
			[]byte(files[right].Path),
		) < 0
	})
	return files, nil
}

// VerifyArtifactDirectoryDigest re-reads an unpacked package and verifies that
// its canonical manifest and every covered ordinary file still produce the
// exact authorized ArtifactDigest. It is suitable for a selected adapter's
// use boundary; callers must not move it into global startup probing.
func VerifyArtifactDirectoryDigest(
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
) error {
	return VerifyArtifactDirectoryDigestContext(
		context.Background(),
		root,
		metadata,
		expectedDigest,
	)
}

// VerifyArtifactDirectoryDigestContext is the cancellable streaming verifier
// for a selected adapter use boundary. It retains only the canonical manifest
// and at most MaxFiles path/digest records, never whole artifact contents.
func VerifyArtifactDirectoryDigestContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
) error {
	return verifyArtifactDirectoryDigestContext(
		ctx,
		root,
		metadata,
		expectedDigest,
		0,
		DefaultArtifactScanLimits(),
	)
}

// VerifyArtifactDirectoryDigestAndSizeContext additionally checks the covered
// manifest+ordinary-file byte size used by installation assertions. Envelope
// metadata remains outside both ArtifactDigest and expectedSize.
func VerifyArtifactDirectoryDigestAndSizeContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
	expectedSize uint64,
) error {
	return verifyArtifactDirectoryDigestContext(
		ctx,
		root,
		metadata,
		expectedDigest,
		expectedSize,
		DefaultArtifactScanLimits(),
	)
}

// VerifyArtifactDirectoryDigestWithLimits is the bounded policy variant of
// VerifyArtifactDirectoryDigest. The supplied limits may only tighten the
// package-wide hard ceilings.
func VerifyArtifactDirectoryDigestWithLimits(
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
	limits ArtifactScanLimits,
) error {
	return verifyArtifactDirectoryDigestContext(
		context.Background(),
		root,
		metadata,
		expectedDigest,
		0,
		limits,
	)
}

// VerifyArtifactDirectoryDigestAndSizeWithLimitsContext combines the exact
// digest/covered-size assertion with a cancellable caller policy that may only
// tighten the default scan ceilings.
func VerifyArtifactDirectoryDigestAndSizeWithLimitsContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
	expectedSize uint64,
	limits ArtifactScanLimits,
) error {
	return verifyArtifactDirectoryDigestContext(
		ctx,
		root,
		metadata,
		expectedDigest,
		expectedSize,
		limits,
	)
}

func verifyArtifactDirectoryDigestContext(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
	expectedSize uint64,
	limits ArtifactScanLimits,
) error {
	return verifyArtifactDirectoryDigestContextWithHooks(
		ctx,
		root,
		metadata,
		expectedDigest,
		expectedSize,
		limits,
		artifactAccessHooks{},
	)
}

func verifyArtifactDirectoryDigestContextWithHooks(
	ctx context.Context,
	root string,
	metadata ArtifactMetadataPaths,
	expectedDigest string,
	expectedSize uint64,
	limits ArtifactScanLimits,
	hooks artifactAccessHooks,
) error {
	if ctx == nil {
		return errors.New("artifact verification context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidSHA256(expectedDigest) {
		return errors.New("expected artifact digest must be lowercase SHA-256")
	}
	if err := limits.validate(); err != nil {
		return err
	}
	absoluteRoot, rootInfo, err := inspectArtifactRoot(root)
	if err != nil {
		return err
	}
	if hooks.afterRootInspect != nil {
		if err := hooks.afterRootInspect(absoluteRoot); err != nil {
			return err
		}
	}
	excluded, expected, err := artifactExcludedPaths(metadata)
	if err != nil {
		return err
	}
	found := make(map[string]bool, len(expected))
	registry := newArtifactPathRegistry()
	files := make([]artifactFileDigestWire, 0)
	var manifest []byte
	pathCount := 0
	fileCount := 0
	totalBytes := int64(0)
	coveredBytes := uint64(0)
	var openedRootInfo os.FileInfo

	err = safefiletree.WalkWithOptions(
		ctx,
		absoluteRoot,
		safefiletree.Options{
			MaxEntries:             limits.MaxPaths,
			MaxEntriesPerDirectory: limits.MaxPaths,
			Visit: func(_ context.Context, entry safefiletree.Entry) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Path == "." {
					if !entry.Info.IsDir() || !os.SameFile(rootInfo, entry.Info) {
						return errors.New("artifact root changed while it was opened")
					}
					if err := rejectArtifactPlatformSpecial(entry.Info); err != nil {
						return fmt.Errorf("inspect opened artifact root: %w", err)
					}
					openedRootInfo = entry.Info
					if hooks.afterRootOpen != nil {
						if err := hooks.afterRootOpen(absoluteRoot); err != nil {
							return err
						}
					}
					return nil
				}
				pathCount++
				if pathCount > limits.MaxPaths {
					return fmt.Errorf("artifact exceeds %d path entries", limits.MaxPaths)
				}
				normalized, err := NormalizeArtifactPath(entry.Path)
				if err != nil {
					return err
				}
				info := entry.Info
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("artifact path %q must not be a symlink", normalized)
				}
				if err := rejectArtifactPlatformSpecial(info); err != nil {
					return fmt.Errorf("artifact path %q: %w", normalized, err)
				}
				if err := rejectArtifactFilesystemBoundary(rootInfo, info); err != nil {
					return fmt.Errorf("artifact path %q: %w", normalized, err)
				}
				kind := artifactPathFile
				if info.IsDir() {
					kind = artifactPathDirectory
				} else if !info.Mode().IsRegular() {
					return fmt.Errorf("artifact path %q is not an ordinary file", normalized)
				}
				if err := registry.add(normalized, kind); err != nil {
					return err
				}
				if hooks.afterPathInspect != nil {
					if err := hooks.afterPathInspect(normalized, info); err != nil {
						return err
					}
				}
				if kind == artifactPathDirectory {
					return nil
				}
				fileCount++
				if fileCount > limits.MaxFiles {
					return fmt.Errorf("artifact exceeds %d ordinary files", limits.MaxFiles)
				}
				remaining := limits.MaxTotalBytes - totalBytes
				fileLimit := limits.MaxFileBytes
				capture := normalized == ArtifactManifestPath
				if capture {
					fileLimit = minInt64(fileLimit, int64(MaxTextBytes))
				}
				if info.Size() < 0 || info.Size() > fileLimit || info.Size() > remaining {
					return fmt.Errorf("artifact file %q exceeds scan byte limits", normalized)
				}
				digest, content, readBytes, err := streamArtifactReader(
					ctx,
					normalized,
					info,
					minInt64(fileLimit, remaining),
					capture,
					entry.Reader,
				)
				if err != nil {
					return err
				}
				totalBytes += readBytes
				if _, skip := excluded[normalized]; skip {
					found[normalized] = true
					if capture {
						manifest = content
						coveredBytes += uint64(readBytes)
					}
					return nil
				}
				coveredBytes += uint64(readBytes)
				files = append(files, artifactFileDigestWire{
					NormalizedPath: normalized,
					SHA256:         digest,
				})
				return nil
			},
		},
	)
	if err != nil {
		if errors.Is(err, safefiletree.ErrEntryLimit) {
			return fmt.Errorf(
				"verify artifact directory: artifact exceeds %d path entries: %w",
				limits.MaxPaths,
				err,
			)
		}
		return fmt.Errorf("verify artifact directory: %w", err)
	}
	for path := range expected {
		if !found[path] {
			return fmt.Errorf("required artifact metadata %q is missing", path)
		}
	}
	if _, canonicalManifest, err := ParseModuleManifestV1(manifest); err != nil ||
		!bytes.Equal(manifest, canonicalManifest) {
		return fmt.Errorf("artifact manifest is not exact canonical JSON: %v", err)
	}
	manifestAfter, err := readArtifactManifestV1FromDirectoryAgainstRootContext(
		ctx,
		absoluteRoot,
		openedRootInfo,
		minInt64(limits.MaxFileBytes, int64(MaxTextBytes)),
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(manifest, manifestAfter) {
		return errors.New("artifact manifest changed while it was verified")
	}
	sort.Slice(files, func(left, right int) bool {
		return bytes.Compare(
			[]byte(files[left].NormalizedPath),
			[]byte(files[right].NormalizedPath),
		) < 0
	})
	digest, err := computeArtifactDigestFromDigests(manifest, files)
	if err != nil {
		return fmt.Errorf("compute artifact digest: %w", err)
	}
	if digest != expectedDigest {
		return errors.New("artifact digest mismatch")
	}
	if expectedSize != 0 && coveredBytes != expectedSize {
		return fmt.Errorf(
			"artifact covered size %d does not match expected %d",
			coveredBytes,
			expectedSize,
		)
	}
	return nil
}

// ReadArtifactManifestV1FromDirectory reads only the fixed module.yaml entry,
// rejects links and non-ordinary files, and enforces the manifest contract's
// 1 MiB ceiling before allocating. The returned bytes are exact canonical JSON
// owned by the caller.
func ReadArtifactManifestV1FromDirectory(root string) ([]byte, error) {
	return ReadArtifactManifestV1FromDirectoryContext(
		context.Background(),
		root,
	)
}

// ReadArtifactManifestV1FromDirectoryContext is the cancellable variant of
// ReadArtifactManifestV1FromDirectory.
func ReadArtifactManifestV1FromDirectoryContext(
	ctx context.Context,
	root string,
) ([]byte, error) {
	return readArtifactManifestV1FromDirectoryContext(ctx, root, int64(MaxTextBytes))
}

func canonicalArtifactFiles(
	input []ArtifactFile,
) ([]artifactFileDigestWire, error) {
	registry := newArtifactPathRegistry()
	files := make([]artifactFileDigestWire, 0, len(input))
	reservedFold := artifactCaseFold(ArtifactManifestPath)
	for _, file := range input {
		normalized, err := NormalizeArtifactPath(file.Path)
		if err != nil {
			return nil, err
		}
		if artifactCaseFold(normalized) == reservedFold {
			return nil, fmt.Errorf(
				"artifact ordinary files must exclude %q",
				ArtifactManifestPath,
			)
		}
		if err := registry.add(normalized, artifactPathFile); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(file.Content)
		files = append(files, artifactFileDigestWire{
			NormalizedPath: normalized,
			SHA256:         hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(files, func(left, right int) bool {
		return bytes.Compare(
			[]byte(files[left].NormalizedPath),
			[]byte(files[right].NormalizedPath),
		) < 0
	})
	return files, nil
}

func canonicalArtifactFileDigests(
	input []ArtifactFileDigest,
) ([]artifactFileDigestWire, error) {
	registry := newArtifactPathRegistry()
	files := make([]artifactFileDigestWire, 0, len(input))
	reservedFold := artifactCaseFold(ArtifactManifestPath)
	for _, file := range input {
		normalized, err := NormalizeArtifactPath(file.Path)
		if err != nil {
			return nil, err
		}
		if artifactCaseFold(normalized) == reservedFold {
			return nil, fmt.Errorf(
				"artifact ordinary files must exclude %q",
				ArtifactManifestPath,
			)
		}
		if err := registry.add(normalized, artifactPathFile); err != nil {
			return nil, err
		}
		if !ValidSHA256(file.SHA256) {
			return nil, fmt.Errorf(
				"artifact file %q digest must be lowercase SHA-256",
				normalized,
			)
		}
		files = append(files, artifactFileDigestWire{
			NormalizedPath: normalized,
			SHA256:         file.SHA256,
		})
	}
	sort.Slice(files, func(left, right int) bool {
		return bytes.Compare(
			[]byte(files[left].NormalizedPath),
			[]byte(files[right].NormalizedPath),
		) < 0
	})
	return files, nil
}

func artifactExcludedPaths(
	metadata ArtifactMetadataPaths,
) (map[string]struct{}, map[string]struct{}, error) {
	excluded := map[string]struct{}{ArtifactManifestPath: {}}
	expected := map[string]struct{}{ArtifactManifestPath: {}}
	registry := newArtifactPathRegistry()
	if err := registry.add(ArtifactManifestPath, artifactPathFile); err != nil {
		return nil, nil, err
	}
	for _, item := range []struct {
		label string
		path  string
	}{
		{label: "detached signature", path: metadata.DetachedSignaturePath},
		{label: "install receipt", path: metadata.InstallReceiptPath},
	} {
		if item.path == "" {
			continue
		}
		normalized, err := NormalizeArtifactPath(item.path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s path: %w", item.label, err)
		}
		if err := registry.add(normalized, artifactPathFile); err != nil {
			return nil, nil, fmt.Errorf("%s path: %w", item.label, err)
		}
		excluded[normalized] = struct{}{}
		expected[normalized] = struct{}{}
	}
	return excluded, expected, nil
}

func streamArtifactReader(
	ctx context.Context,
	normalized string,
	before os.FileInfo,
	maxBytes int64,
	capture bool,
	reader io.Reader,
) (digest string, content []byte, readBytes int64, returnErr error) {
	if ctx == nil {
		return "", nil, 0, errors.New("artifact read context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, 0, err
	}
	if reader == nil {
		return "", nil, 0, fmt.Errorf("artifact file %q has no opened reader", normalized)
	}
	if maxBytes < 0 || before.Size() < 0 || before.Size() > maxBytes {
		return "", nil, 0, fmt.Errorf(
			"artifact file %q exceeds %d bytes",
			normalized,
			maxBytes,
		)
	}

	hasher := sha256.New()
	var captured bytes.Buffer
	if capture && before.Size() > 0 {
		captured.Grow(int(before.Size()))
	}
	limited := io.LimitReader(reader, maxBytes+1)
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, readBytes, err
		}
		count, readErr := limited.Read(buffer)
		if count > 0 {
			readBytes += int64(count)
			if readBytes > maxBytes {
				return "", nil, readBytes, fmt.Errorf(
					"artifact file %q exceeds %d bytes",
					normalized,
					maxBytes,
				)
			}
			_, _ = hasher.Write(buffer[:count])
			if capture {
				_, _ = captured.Write(buffer[:count])
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", nil, readBytes, fmt.Errorf(
				"read artifact file %q: %w",
				normalized,
				readErr,
			)
		}
	}
	if capture {
		content = bytes.Clone(captured.Bytes())
	}
	return hex.EncodeToString(hasher.Sum(nil)), content, readBytes, nil
}

func readArtifactManifestV1FromDirectory(root string, maxBytes int64) ([]byte, error) {
	return readArtifactManifestV1FromDirectoryContext(
		context.Background(),
		root,
		maxBytes,
	)
}

func readArtifactManifestV1FromDirectoryContext(
	ctx context.Context,
	root string,
	maxBytes int64,
) ([]byte, error) {
	return readArtifactManifestV1FromDirectoryContextWithHooks(
		ctx,
		root,
		maxBytes,
		artifactAccessHooks{},
	)
}

func readArtifactManifestV1FromDirectoryContextWithHooks(
	ctx context.Context,
	root string,
	maxBytes int64,
	hooks artifactAccessHooks,
) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("artifact read context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absoluteRoot, rootInfo, err := inspectArtifactRoot(root)
	if err != nil {
		return nil, err
	}
	if hooks.afterRootInspect != nil {
		if err := hooks.afterRootInspect(absoluteRoot); err != nil {
			return nil, err
		}
	}
	return readArtifactManifestV1FromSafeTreeContext(
		ctx,
		absoluteRoot,
		rootInfo,
		maxBytes,
		hooks,
	)
}

func readArtifactManifestV1FromDirectoryAgainstRootContext(
	ctx context.Context,
	absoluteRoot string,
	expectedRoot os.FileInfo,
	maxBytes int64,
) ([]byte, error) {
	return readArtifactManifestV1FromSafeTreeContext(
		ctx,
		absoluteRoot,
		expectedRoot,
		maxBytes,
		artifactAccessHooks{},
	)
}

func readArtifactManifestV1FromSafeTreeContext(
	ctx context.Context,
	absoluteRoot string,
	expectedRoot os.FileInfo,
	maxBytes int64,
	hooks artifactAccessHooks,
) ([]byte, error) {
	var manifest []byte
	err := safefiletree.WalkPathWithOptions(
		ctx,
		absoluteRoot,
		ArtifactManifestPath,
		safefiletree.Options{
			MaxEntries:             DefaultArtifactMaxPaths,
			MaxEntriesPerDirectory: DefaultArtifactMaxPaths,
			Visit: func(_ context.Context, entry safefiletree.Entry) error {
				if entry.Path == "." {
					if !entry.Info.IsDir() || expectedRoot == nil ||
						!os.SameFile(expectedRoot, entry.Info) {
						return errors.New("artifact root changed while it was opened")
					}
					if err := rejectArtifactPlatformSpecial(entry.Info); err != nil {
						return fmt.Errorf("inspect opened artifact root: %w", err)
					}
					if hooks.afterRootOpen != nil {
						return hooks.afterRootOpen(absoluteRoot)
					}
					return nil
				}
				info := entry.Info
				if entry.Kind != safefiletree.KindRegularFile ||
					info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return errors.New("artifact manifest must be an ordinary non-symlink file")
				}
				if err := rejectArtifactPlatformSpecial(info); err != nil {
					return fmt.Errorf("artifact manifest: %w", err)
				}
				if err := rejectArtifactFilesystemBoundary(expectedRoot, info); err != nil {
					return fmt.Errorf("artifact manifest: %w", err)
				}
				_, content, _, err := streamArtifactReader(
					ctx,
					ArtifactManifestPath,
					info,
					maxBytes,
					true,
					entry.Reader,
				)
				if err == nil {
					manifest = content
				}
				return err
			},
		},
	)
	if err != nil {
		return nil, err
	}
	_, canonical, err := ParseModuleManifestV1(manifest)
	if err != nil || !bytes.Equal(manifest, canonical) {
		return nil, fmt.Errorf("artifact manifest is not exact canonical JSON: %v", err)
	}
	return bytes.Clone(canonical), nil
}

// ReadArtifactOrdinaryFileFromDirectoryContext safely reads one exact
// package-relative ordinary file under a hard caller-supplied bound. It never
// follows links or accepts a hardlink and checks cancellation while reading.
func ReadArtifactOrdinaryFileFromDirectoryContext(
	ctx context.Context,
	root string,
	normalizedPath string,
	maxBytes int64,
) ([]byte, error) {
	return readArtifactOrdinaryFileFromDirectoryContextWithHooks(
		ctx,
		root,
		normalizedPath,
		maxBytes,
		artifactAccessHooks{},
	)
}

func readArtifactOrdinaryFileFromDirectoryContextWithHooks(
	ctx context.Context,
	root string,
	normalizedPath string,
	maxBytes int64,
	hooks artifactAccessHooks,
) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > DefaultArtifactMaxFileBytes {
		return nil, errors.New("artifact ordinary-file bound is outside hard ceilings")
	}
	normalized, err := NormalizeArtifactPath(normalizedPath)
	if err != nil || normalized != normalizedPath {
		return nil, errors.New("artifact ordinary-file path must be exact canonical relative path")
	}
	absoluteRoot, rootInfo, err := inspectArtifactRoot(root)
	if err != nil {
		return nil, err
	}
	if hooks.afterRootInspect != nil {
		if err := hooks.afterRootInspect(absoluteRoot); err != nil {
			return nil, err
		}
	}
	var content []byte
	err = safefiletree.WalkPathWithOptions(
		ctx,
		absoluteRoot,
		normalized,
		safefiletree.Options{
			MaxEntries:             DefaultArtifactMaxPaths,
			MaxEntriesPerDirectory: DefaultArtifactMaxPaths,
			Visit: func(_ context.Context, entry safefiletree.Entry) error {
				if entry.Path == "." {
					if !entry.Info.IsDir() || !os.SameFile(rootInfo, entry.Info) {
						return errors.New("artifact root changed while it was opened")
					}
					if err := rejectArtifactPlatformSpecial(entry.Info); err != nil {
						return fmt.Errorf("inspect opened artifact root: %w", err)
					}
					if hooks.afterRootOpen != nil {
						return hooks.afterRootOpen(absoluteRoot)
					}
					return nil
				}
				info := entry.Info
				if info.Mode()&os.ModeSymlink != 0 {
					return errors.New("artifact ordinary-file path escapes or traverses a symlink")
				}
				if err := rejectArtifactPlatformSpecial(info); err != nil {
					return fmt.Errorf("artifact ordinary-file path %q: %w", entry.Path, err)
				}
				if err := rejectArtifactFilesystemBoundary(rootInfo, info); err != nil {
					return fmt.Errorf("artifact ordinary-file path %q: %w", entry.Path, err)
				}
				if hooks.afterPathInspect != nil {
					if err := hooks.afterPathInspect(entry.Path, info); err != nil {
						return err
					}
				}
				if entry.Path != normalized {
					if entry.Kind != safefiletree.KindDirectory || !info.IsDir() {
						return errors.New("artifact ordinary-file path traverses a non-directory")
					}
					return nil
				}
				if entry.Kind != safefiletree.KindRegularFile || !info.Mode().IsRegular() {
					return errors.New("artifact ordinary-file path is not an ordinary non-symlink file")
				}
				_, captured, _, err := streamArtifactReader(
					ctx,
					normalized,
					info,
					maxBytes,
					true,
					entry.Reader,
				)
				if err == nil {
					content = captured
				}
				return err
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func inspectArtifactRoot(root string) (string, os.FileInfo, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil, errors.New("artifact root must not be empty")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", nil, errors.New("artifact root must be a non-symlink directory")
	}
	if err := rejectArtifactPlatformSpecial(rootInfo); err != nil {
		return "", nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil || !artifactSamePath(absoluteRoot, resolved) {
		return "", nil, errors.New("artifact root must not traverse a symlink")
	}
	return absoluteRoot, rootInfo, nil
}

func artifactSamePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (limits ArtifactScanLimits) validate() error {
	defaults := DefaultArtifactScanLimits()
	if limits.MaxPaths <= 0 || limits.MaxPaths > defaults.MaxPaths ||
		limits.MaxFiles <= 0 || limits.MaxFiles > defaults.MaxFiles ||
		limits.MaxFileBytes <= 0 || limits.MaxFileBytes > defaults.MaxFileBytes ||
		limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes > defaults.MaxTotalBytes {
		return errors.New("artifact scan limits must be positive and not exceed hard ceilings")
	}
	return nil
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func newArtifactPathRegistry() *artifactPathRegistry {
	return &artifactPathRegistry{
		exact: make(map[string]artifactPathKind),
		fold:  make(map[string]string),
	}
}

func (registry *artifactPathRegistry) add(
	normalized string,
	kind artifactPathKind,
) error {
	segments := strings.Split(normalized, "/")
	for index := range segments {
		path := strings.Join(segments[:index+1], "/")
		pathKind := artifactPathDirectory
		if index == len(segments)-1 {
			pathKind = kind
		}
		if existing, ok := registry.exact[path]; ok {
			if index == len(segments)-1 || existing == artifactPathFile {
				if existing == pathKind {
					return fmt.Errorf(
						"artifact path %q is duplicated after normalization",
						normalized,
					)
				}
				return fmt.Errorf(
					"artifact path %q conflicts with a file/directory prefix",
					normalized,
				)
			}
		} else {
			registry.exact[path] = pathKind
		}

		folded := artifactCaseFold(path)
		if existing, ok := registry.fold[folded]; ok && existing != path {
			return fmt.Errorf(
				"artifact path %q has a case collision with %q",
				path,
				existing,
			)
		}
		registry.fold[folded] = path
	}
	return nil
}

func artifactCaseFold(value string) string {
	return norm.NFC.String(cases.Fold().String(value))
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	return value[0] >= 'A' && value[0] <= 'Z' ||
		value[0] >= 'a' && value[0] <= 'z'
}
