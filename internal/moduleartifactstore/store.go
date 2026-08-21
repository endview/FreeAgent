// Package moduleartifactstore owns durable, digest-addressed module artifact
// publication. It performs filesystem work only and grants no module runtime
// authority.
package moduleartifactstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/safefiletree"
	"github.com/endview/freeagent/sdk/moduleapi"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	stageBasePrefixV1                  = ".module-artifact-ingress-stage-"
	legacyModuleApplyStageBasePrefixV1 = ".freeagent-module-apply-stage-"
	artifactRootLeaseNameV1            = ".freeagent-artifact-ingress.lock"
	BackupMaxArtifactFileV1            = int64(64 << 20)
	BackupMaxArtifactClosureV1         = int64(512 << 20)
	MaxPhysicalArtifactsV1             = uint64(256)
	MaxPhysicalCoveredBytesV1          = uint64(512 << 20)
	MaxPhysicalPathsV1                 = uint64(32_768)
	MaxPhysicalFilesV1                 = uint64(16_384)
	MaxPhysicalPathBytesV1             = uint64(16 << 20)
	maxArtifactRootDirectEntriesV1     = MaxPhysicalArtifactsV1 + 2 // digests + lease + stage
	artifactRootReadBatchV1            = 32
)

var (
	stagePrefixV1    = stageBasePrefixV1 + strconv.Itoa(os.Getpid()) + "-"
	artifactRootLock sync.Map
)

var ErrTargetExists = errors.New("module artifact store: target already exists")

type ExistingModePolicyV1 uint8

const (
	ExistingModeInertOnlyV1 ExistingModePolicyV1 = iota
	ExistingModeRootCompatibleV1
	ExistingModeStoreProvenInstalledV1
)

// SourcePackageV1 is an inspected transient path selection. Its fields are
// private so it can only be produced by SelectSourcePackageV1.
type SourcePackageV1 struct {
	sourceRoot   stableDirectoryV1
	artifactRoot ArtifactRootV1
	packageRoot  stableDirectoryV1
}

// ArtifactRootV1 freezes one private server-owned root identity independently
// from any transient source package.
type ArtifactRootV1 struct {
	directory stableDirectoryV1
}

// PhysicalArtifactReservationV1 is an opaque, exact-digest-bound reservation
// for one prepared artifact's covered bytes and namespace. Only this package
// can construct one, so a production writer cannot understate path, file, or
// path-name-byte usage when reserving aggregate physical-root capacity.
type PhysicalArtifactReservationV1 struct {
	digest    string
	size      uint64
	namespace physicalNamespaceV1
}

// ArtifactRootWriteLeaseV1 is the callback-scoped proof that one process owns
// the persistent artifact-root writer lease. It is deliberately opaque and
// cannot be retained or released by callers.
type ArtifactRootWriteLeaseV1 struct {
	root   ArtifactRootV1
	inner  *artifactRootLeaseV1
	active bool
}

// IsArtifactRootControlEntryV1 identifies the one fixed persistent lease file
// that exact-root verifiers may exclude from artifact data. Callers must still
// run SelectArtifactRootV1 plus physical-closure verification; this predicate
// alone does not validate the file's identity, links, ownership, or mode.
func IsArtifactRootControlEntryV1(name string) bool {
	return name == artifactRootLeaseNameV1
}

// ProvisionArtifactRootV1 creates exactly one new empty private root below an
// already trusted canonical state directory. It never chmods, rewrites, or
// adopts an existing path.
func ProvisionArtifactRootV1(parentPath, name string) (ArtifactRootV1, error) {
	if name == "" || name != strings.TrimSpace(name) ||
		name != moduleapi.CanonicalText(name) || !filepath.IsLocal(name) ||
		filepath.Base(name) != name || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\:?#`) {
		return ArtifactRootV1{}, errors.New("module artifact store: provisioned root name is invalid")
	}
	parent, err := inspectStableDirectoryV1(parentPath, true)
	if err != nil || !artifactProvisionParentPrivateV1(parent.path, parent.info) ||
		!artifactRootAncestryPrivateV1(parent.path) {
		return ArtifactRootV1{}, errors.New("module artifact store: provision parent is not trusted private state")
	}
	target := filepath.Join(parent.path, name)
	if !pathStrictlyWithinV1(parent.path, target) || !samePathV1(filepath.Dir(target), parent.path) {
		return ArtifactRootV1{}, errors.New("module artifact store: provisioned root target is invalid")
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return ArtifactRootV1{}, errors.New("module artifact store: create new artifact root failed")
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(target)
			_ = syncDirectoryV1(parent.path)
		}
	}()
	if err := provisionArtifactPathPrivateV1(target); err != nil {
		return ArtifactRootV1{}, errors.New("module artifact store: secure new artifact root failed")
	}
	if err := syncDirectoryV1(target); err != nil {
		return ArtifactRootV1{}, errors.New("module artifact store: sync new artifact root failed")
	}
	if err := syncDirectoryV1(parent.path); err != nil {
		return ArtifactRootV1{}, errors.New("module artifact store: sync artifact root parent failed")
	}
	root, err := SelectArtifactRootV1(target)
	if err != nil {
		return ArtifactRootV1{}, err
	}
	created = false
	return root, nil
}

// ProvisionArtifactStagingRootLeafV1 creates and hardens a brand-new empty
// staging directory below a private parent whose complete ancestry is trusted.
// It never modifies or adopts an existing live path.
func ProvisionArtifactStagingRootLeafV1(
	parentPath, pattern string,
) (result string, returnErr error) {
	if pattern == "" || pattern != strings.TrimSpace(pattern) ||
		pattern != moduleapi.CanonicalText(pattern) || filepath.Base(pattern) != pattern ||
		strings.ContainsAny(pattern, `/\:?#`) || strings.Count(pattern, "*") > 1 {
		return "", errors.New("module artifact store: staging root pattern is invalid")
	}
	parent, err := inspectStableDirectoryV1(parentPath, true)
	if err != nil || !artifactProvisionParentPrivateV1(parent.path, parent.info) ||
		!artifactRootAncestryPrivateV1(parent.path) {
		return "", errors.New("module artifact store: staging parent is not trusted private state")
	}
	target, err := os.MkdirTemp(parent.path, pattern)
	if err != nil {
		return "", errors.New("module artifact store: create artifact staging root failed")
	}
	keep := false
	defer func() {
		if keep {
			return
		}
		if removeErr := os.RemoveAll(target); removeErr != nil {
			returnErr = errors.Join(returnErr, errors.New("module artifact store: remove failed staging root failed"))
		}
		_ = syncDirectoryV1(parent.path)
	}()
	created, err := os.Lstat(target)
	entries, readErr := os.ReadDir(target)
	if err != nil || readErr != nil || !created.IsDir() ||
		created.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(created) || len(entries) != 0 {
		return "", errors.New("module artifact store: new artifact staging root is invalid")
	}
	if err := provisionArtifactPathPrivateV1(target); err != nil {
		return "", errors.New("module artifact store: secure artifact staging root failed")
	}
	current, err := os.Lstat(target)
	if err != nil || !os.SameFile(created, current) || !artifactRootPrivateV1(target, current) {
		return "", errors.New("module artifact store: artifact staging root privacy verification failed")
	}
	if err := syncDirectoryV1(target); err != nil {
		return "", errors.New("module artifact store: sync artifact staging root failed")
	}
	if err := syncDirectoryV1(parent.path); err != nil {
		return "", errors.New("module artifact store: sync artifact staging parent failed")
	}
	keep = true
	return target, nil
}

// PublishRequestV1 binds a verified source selection to exact Store-owned
// discovery facts. MaxPackageBytes may only tighten SDK and Backup ceilings.
type PublishRequestV1 struct {
	Source             SourcePackageV1
	ArtifactDigest     string
	ArtifactSizeBytes  uint64
	MaxPackageBytes    uint64
	ExistingModePolicy ExistingModePolicyV1
}

// PublishResultV1 contains only authority-free evidence needed by the Store
// commit. No host path crosses this boundary.
type PublishResultV1 struct {
	ManifestCanonical []byte
	CoveredFileCount  uint64
	Reused            bool
}

// SelectSourcePackageV1 validates both roots, rejects any overlap, resolves
// one exact canonical source-relative directory without following links, and
// freezes all three directory identities for later TOCTOU checks.
func SelectSourcePackageV1(sourceRoot, artifactRoot, packagePath string) (SourcePackageV1, error) {
	artifacts, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		return SourcePackageV1{}, err
	}
	return SelectSourcePackageAtRootV1(sourceRoot, artifacts, packagePath)
}

// SelectSourcePackageAtRootV1 binds a source package to an already-frozen
// artifact root so the exact-retry lookup and later commit share one root
// identity without depending on source state.
func SelectSourcePackageAtRootV1(sourceRoot string, artifacts ArtifactRootV1, packagePath string) (SourcePackageV1, error) {
	normalized, err := moduleapi.NormalizeArtifactPath(packagePath)
	if err != nil || normalized != packagePath || len(packagePath) > moduleapi.MaxModulePackagePathBytesV1 {
		return SourcePackageV1{}, errors.New("module artifact store: package path is not exact canonical relative path")
	}
	source, err := inspectStableDirectoryV1(sourceRoot, true)
	if err != nil {
		return SourcePackageV1{}, errors.New("module artifact store: source root is invalid")
	}
	if err := artifacts.verifyCurrentV1(); err != nil {
		return SourcePackageV1{}, err
	}
	if pathsOverlapV1(source.path, artifacts.directory.path) {
		return SourcePackageV1{}, errors.New("module artifact store: source and artifact roots overlap")
	}
	candidate := filepath.Join(source.path, filepath.FromSlash(normalized))
	if !pathStrictlyWithinV1(source.path, candidate) {
		return SourcePackageV1{}, errors.New("module artifact store: package path escapes source root")
	}
	selected, err := inspectStableDirectoryV1(candidate, false)
	if err != nil || !pathStrictlyWithinV1(source.path, selected.path) {
		return SourcePackageV1{}, errors.New("module artifact store: package directory is invalid")
	}
	return SourcePackageV1{
		sourceRoot: source, artifactRoot: artifacts, packageRoot: selected,
	}, nil
}

// SelectArtifactRootV1 validates a canonical absolute, non-link, current-user
// owned private directory and freezes its exact filesystem identity.
func SelectArtifactRootV1(path string) (ArtifactRootV1, error) {
	directory, err := inspectStableDirectoryV1(path, true)
	if err != nil || !artifactRootPrivateV1(directory.path, directory.info) ||
		!artifactRootAncestryPrivateV1(directory.path) {
		return ArtifactRootV1{}, errors.New("module artifact store: artifact root is not a private server-owned directory")
	}
	return ArtifactRootV1{directory: directory}, nil
}

// VerifyPhysicalRootClosureV1 re-derives one offline artifact root twice and
// enforces the same content and namespace budgets used by ingress. The caller
// must already own the surrounding offline lifecycle; unlike ingress publish,
// this verifier does not create or acquire the persistent root lease.
func VerifyPhysicalRootClosureV1(ctx context.Context, root ArtifactRootV1) error {
	if ctx == nil {
		return errors.New("module artifact store: physical root context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.verifyCurrentV1(); err != nil {
		return err
	}
	first, err := scanPhysicalArtifactRootV1(ctx, root.directory.path, "", 0)
	if err != nil {
		return err
	}
	second, err := scanPhysicalArtifactRootV1(ctx, root.directory.path, "", 0)
	if err != nil {
		return err
	}
	if !first.sameClosureV1(second) {
		return errors.New("module artifact store: physical artifact root drifted during closure verification")
	}
	return root.verifyCurrentV1()
}

// PreparePhysicalArtifactReservationV1 completely captures one prepared
// artifact under the same Backup-compatible limits as the physical-root
// scanner and returns only an opaque aggregate reservation. ArtifactDigest
// commits to the canonical path set as well as file contents, so a later
// same-digest stage cannot consume a different namespace reservation.
func PreparePhysicalArtifactReservationV1(
	ctx context.Context,
	artifactDirectory string,
	digest string,
	size uint64,
) (PhysicalArtifactReservationV1, error) {
	if ctx == nil || strings.TrimSpace(artifactDirectory) == "" ||
		!moduleapi.ValidSHA256(digest) || size == 0 {
		return PhysicalArtifactReservationV1{}, errors.New(
			"module artifact store: physical reservation request is invalid",
		)
	}
	if err := ctx.Err(); err != nil {
		return PhysicalArtifactReservationV1{}, err
	}
	limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
	if err != nil || size > uint64(limits.MaxTotalBytes) {
		return PhysicalArtifactReservationV1{}, errors.New(
			"module artifact store: physical reservation exceeds hard limits",
		)
	}
	captured, err := captureArtifactV1(ctx, artifactDirectory, digest, size, limits)
	if err != nil {
		return PhysicalArtifactReservationV1{}, err
	}
	namespace, err := capturedPhysicalNamespaceV1(captured)
	for index := range captured.files {
		captured.files[index].Content = nil
	}
	captured.files = nil
	captured.manifest = nil
	if err != nil {
		return PhysicalArtifactReservationV1{}, err
	}
	return PhysicalArtifactReservationV1{
		digest: digest, size: size, namespace: namespace,
	}, nil
}

// WithArtifactRootWriteLeaseV1 serializes one production filesystem writer
// across goroutines and processes through the persistent root lease. Before
// entering the callback it durably recovers only strictly named, private
// stages owned by the ingress or legacy Apply writers. Callers must not nest
// this function for the same root.
func WithArtifactRootWriteLeaseV1(
	ctx context.Context,
	root ArtifactRootV1,
	action func(*ArtifactRootWriteLeaseV1) error,
) (returnErr error) {
	if action == nil {
		return errors.New("module artifact store: root write action is nil")
	}
	inner, err := acquireArtifactRootLeaseV1(ctx, root)
	if err != nil {
		return err
	}
	lease := &ArtifactRootWriteLeaseV1{root: root, inner: inner, active: true}
	defer func() {
		lease.active = false
		lease.inner = nil
		if releaseErr := inner.releaseV1(); releaseErr != nil {
			returnErr = errors.Join(returnErr, releaseErr)
		}
	}()
	if err := recoverOrphanStagesV1(root); err != nil {
		return err
	}
	return action(lease)
}

// VerifyPhysicalReservationV1 checks aggregate root capacity for one opaque
// prepared artifact while this writer owns the persistent root lease.
func (lease *ArtifactRootWriteLeaseV1) VerifyPhysicalReservationV1(
	ctx context.Context,
	reservation PhysicalArtifactReservationV1,
	requireTarget bool,
) error {
	if err := lease.verifyActiveV1(); err != nil {
		return err
	}
	if !moduleapi.ValidSHA256(reservation.digest) || reservation.size == 0 ||
		reservation.namespace.paths == 0 || reservation.namespace.files == 0 ||
		reservation.namespace.pathBytes == 0 {
		return errors.New("module artifact store: physical reservation proof is invalid")
	}
	return verifyPhysicalQuotaV1(
		ctx,
		lease.root,
		reservation.digest,
		reservation.size,
		reservation.namespace,
		requireTarget,
	)
}

// VerifyPhysicalClosureV1 re-derives the complete root while this writer owns
// the persistent lease. It is the post-publication counterpart of
// VerifyPhysicalReservationV1.
func (lease *ArtifactRootWriteLeaseV1) VerifyPhysicalClosureV1(ctx context.Context) error {
	if err := lease.verifyActiveV1(); err != nil {
		return err
	}
	return VerifyPhysicalRootClosureV1(ctx, lease.root)
}

// PromoteStoreProvenInstalledModesV1 turns one fully reserved, exact physical
// artifact into the platform-normalized mode closure required by a subsequent
// Store installation. It grants no Store authority. The destination path is
// derived only from the opaque reservation, and every metadata mutation is
// performed through an already-opened safefiletree handle while the root
// writer lease is active.
func (lease *ArtifactRootWriteLeaseV1) PromoteStoreProvenInstalledModesV1(
	ctx context.Context,
	reservation PhysicalArtifactReservationV1,
) error {
	if ctx == nil {
		return errors.New("module artifact store: installed mode promotion context is nil")
	}
	if err := lease.VerifyPhysicalReservationV1(ctx, reservation, true); err != nil {
		return err
	}
	limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
	if err != nil || reservation.size > uint64(limits.MaxTotalBytes) {
		return errors.New("module artifact store: installed mode promotion exceeds hard limits")
	}
	destination := filepath.Join(lease.root.directory.path, reservation.digest)
	if err := verifyArtifactV1(
		ctx, destination, reservation.digest, reservation.size, limits,
	); err != nil {
		return errors.New("module artifact store: installed mode promotion target is invalid")
	}
	if err := verifyArtifactModesV1(
		ctx, destination, ExistingModeRootCompatibleV1,
	); err != nil {
		return err
	}
	executable, err := exactLocalMCPExecutableV1(ctx, destination)
	if err != nil {
		return errors.New("module artifact store: installed mode promotion descriptor closure is invalid")
	}
	if err := normalizeArtifactInstalledModesV1(
		ctx, destination, executable, limits,
	); err != nil {
		return err
	}
	if err := verifyArtifactV1(
		ctx, destination, reservation.digest, reservation.size, limits,
	); err != nil {
		return errors.New("module artifact store: promoted artifact content changed")
	}
	if err := verifyArtifactModesV1(
		ctx, destination, ExistingModeStoreProvenInstalledV1,
	); err != nil {
		return err
	}
	if err := syncDirectoryV1(lease.root.directory.path); err != nil {
		return errors.New("module artifact store: sync promoted artifact root failed")
	}
	if err := verifyArtifactV1(
		ctx, destination, reservation.digest, reservation.size, limits,
	); err != nil {
		return errors.New("module artifact store: promoted artifact drifted during sync")
	}
	if err := verifyArtifactModesV1(
		ctx, destination, ExistingModeStoreProvenInstalledV1,
	); err != nil {
		return err
	}
	if err := lease.VerifyPhysicalReservationV1(ctx, reservation, true); err != nil {
		return err
	}
	return lease.VerifyPhysicalClosureV1(ctx)
}

func (lease *ArtifactRootWriteLeaseV1) verifyActiveV1() error {
	if lease == nil || !lease.active || lease.inner == nil {
		return errors.New("module artifact store: root write lease is not active")
	}
	return lease.root.verifyCurrentV1()
}

func normalizeArtifactInstalledModesV1(
	ctx context.Context,
	root string,
	executable string,
	limits moduleapi.ArtifactScanLimits,
) error {
	normalizer, err := newArtifactInstalledModeNormalizerV1()
	if err != nil {
		return errors.New("module artifact store: initialize installed mode normalization failed")
	}
	err = safefiletree.WalkWithOptions(ctx, root, safefiletree.Options{
		OpenForSync:            true,
		OpenForMetadataControl: true,
		MaxEntries:             limits.MaxPaths,
		MaxEntriesPerDirectory: limits.MaxPaths,
		Visit: func(_ context.Context, entry safefiletree.Entry) error {
			if !artifactWalkedEntryPrivateV1(entry) {
				return errors.New("module artifact store: promoted artifact entry is not private")
			}
			if _, safe := artifactModeCandidateV1(entry.Info); !safe {
				return errors.New("module artifact store: promoted artifact mode precondition is invalid")
			}
			if entry.Kind == safefiletree.KindDirectory {
				return nil
			}
			relative, err := moduleapi.NormalizeArtifactPath(entry.Path)
			if err != nil || relative != entry.Path {
				return errors.New("module artifact store: promoted artifact path is invalid")
			}
			isExecutable := executable != "" && relative == executable
			if err := normalizer.normalizeV1(entry, isExecutable); err != nil {
				return errors.New("module artifact store: normalize installed artifact file failed")
			}
			if err := entry.Sync(); err != nil {
				return errors.New("module artifact store: sync promoted artifact file failed")
			}
			return nil
		},
		LeaveDirectory: func(_ context.Context, entry safefiletree.Entry) error {
			if !artifactWalkedEntryPrivateV1(entry) {
				return errors.New("module artifact store: promoted artifact directory is not private")
			}
			if err := normalizer.normalizeV1(entry, false); err != nil {
				return errors.New("module artifact store: normalize installed artifact directory failed")
			}
			if err := entry.Sync(); err != nil {
				return errors.New("module artifact store: sync promoted artifact directory failed")
			}
			return nil
		},
	})
	if err != nil {
		return errors.Join(errors.New("module artifact store: installed mode normalization failed"), err)
	}
	return nil
}

// SourceRoot returns the transient verified source root for an in-process
// verifier. Callers must never persist or serialize it.
func (selection SourcePackageV1) SourceRoot() string { return selection.sourceRoot.path }

// PackageRoot returns the transient verified candidate root for an in-process
// verifier. Callers must never persist or serialize it.
func (selection SourcePackageV1) PackageRoot() string { return selection.packageRoot.path }

func (selection SourcePackageV1) ArtifactRootV1() ArtifactRootV1 {
	return selection.artifactRoot
}

func (root ArtifactRootV1) verifyCurrentV1() error {
	current, err := root.directory.currentV1()
	if err != nil || !artifactRootPrivateV1(root.directory.path, current) ||
		!artifactRootAncestryPrivateV1(root.directory.path) {
		return errors.New("module artifact store: artifact root identity or privacy changed")
	}
	return nil
}

// VerifyCurrentV1 checks that no selected root identity or resolved path has
// changed since selection.
func (selection SourcePackageV1) VerifyCurrentV1() error {
	if err := selection.sourceRoot.verifyCurrentV1(); err != nil {
		return errors.New("module artifact store: source root changed")
	}
	if err := selection.artifactRoot.verifyCurrentV1(); err != nil {
		return errors.New("module artifact store: artifact root changed")
	}
	if err := selection.packageRoot.verifyCurrentV1(); err != nil {
		return errors.New("module artifact store: package root changed")
	}
	if pathsOverlapV1(selection.sourceRoot.path, selection.artifactRoot.directory.path) ||
		!pathStrictlyWithinV1(selection.sourceRoot.path, selection.packageRoot.path) {
		return errors.New("module artifact store: selected root relationship changed")
	}
	return nil
}

// PublishV1 copies the selected exact artifact through a hidden direct-child
// stage, syncs and re-verifies it, atomically publishes without replacement,
// and fully verifies and syncs an exact pre-existing target. Success therefore
// always precedes any caller's database commit.
func PublishV1(ctx context.Context, request PublishRequestV1) (result PublishResultV1, returnErr error) {
	if ctx == nil {
		return result, errors.New("module artifact store: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !moduleapi.ValidSHA256(request.ArtifactDigest) || request.ArtifactSizeBytes == 0 ||
		!request.ExistingModePolicy.validV1() {
		return result, errors.New("module artifact store: artifact identity is invalid")
	}
	limits, err := backupCompatibleLimitsV1(request.MaxPackageBytes)
	if err != nil {
		return result, err
	}
	if request.ArtifactSizeBytes > uint64(limits.MaxTotalBytes) {
		return result, errors.New("module artifact store: artifact size exceeds package limit")
	}
	if err := request.Source.VerifyCurrentV1(); err != nil {
		return result, err
	}
	captured, err := captureArtifactV1(ctx, request.Source.packageRoot.path, request.ArtifactDigest, request.ArtifactSizeBytes, limits)
	if err != nil {
		return result, err
	}
	if err := request.Source.VerifyCurrentV1(); err != nil {
		return result, err
	}
	result.ManifestCanonical = bytes.Clone(captured.manifest)
	result.CoveredFileCount = uint64(len(captured.files)) + 1
	targetNamespace, err := capturedPhysicalNamespaceV1(captured)
	if err != nil {
		return PublishResultV1{}, err
	}
	lease, err := acquireArtifactRootLeaseV1(ctx, request.Source.artifactRoot)
	if err != nil {
		return PublishResultV1{}, err
	}
	defer func() {
		if releaseErr := lease.releaseV1(); releaseErr != nil {
			returnErr = errors.Join(returnErr, releaseErr)
		}
	}()
	if err := request.Source.VerifyCurrentV1(); err != nil {
		return PublishResultV1{}, err
	}
	if err := recoverOrphanStagesV1(request.Source.artifactRoot); err != nil {
		return PublishResultV1{}, err
	}
	if err := verifyPhysicalQuotaV1(
		ctx, request.Source.artifactRoot, request.ArtifactDigest,
		request.ArtifactSizeBytes, targetNamespace, false,
	); err != nil {
		return PublishResultV1{}, err
	}

	destination := filepath.Join(request.Source.artifactRoot.directory.path, request.ArtifactDigest)
	if _, err := os.Lstat(destination); err == nil {
		for index := range captured.files {
			captured.files[index].Content = nil
		}
		captured.files = nil
		if err := verifyAndSyncExistingV1(
			ctx, destination, request.Source.artifactRoot.directory.path,
			request.ArtifactDigest, request.ArtifactSizeBytes,
			result.CoveredFileCount, limits, request.ExistingModePolicy,
		); err != nil {
			return PublishResultV1{}, err
		}
		if err := request.Source.VerifyCurrentV1(); err != nil {
			return PublishResultV1{}, err
		}
		if err := verifyPhysicalQuotaV1(
			ctx, request.Source.artifactRoot, request.ArtifactDigest,
			request.ArtifactSizeBytes, targetNamespace, true,
		); err != nil {
			return PublishResultV1{}, err
		}
		result.Reused = true
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublishResultV1{}, errors.New("module artifact store: inspect publish target failed")
	}
	if request.ExistingModePolicy == ExistingModeStoreProvenInstalledV1 {
		// Bind the missing-target decision to the exact bytes already captured
		// and digest-checked above. Re-reading the mutable source here could
		// transiently observe a different runtime mode and publish an installed
		// LOCAL_PROCESS artifact with inert-only modes.
		executable, err := exactLocalMCPExecutableFromCapturedV1(
			ctx, captured.manifest, captured.files,
		)
		if err != nil {
			return PublishResultV1{}, errors.New("module artifact store: installed source mode closure is invalid")
		}
		if executable != "" {
			return PublishResultV1{}, errors.New("module artifact store: installed local artifact target is missing")
		}
	}

	stageRoot, err := os.MkdirTemp(request.Source.artifactRoot.directory.path, stagePrefixV1+"*")
	if err != nil {
		return PublishResultV1{}, errors.New("module artifact store: create stage failed")
	}
	if err := validateStageRootV1(request.Source.artifactRoot.directory.path, stageRoot); err != nil {
		return PublishResultV1{}, err
	}
	defer func() {
		if stageRoot == "" {
			return
		}
		cleanupErr := cleanupStageV1(request.Source.artifactRoot.directory.path, stageRoot)
		if cleanupErr != nil {
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()

	stagedArtifact := filepath.Join(stageRoot, request.ArtifactDigest)
	if err := writeCapturedArtifactV1(ctx, stagedArtifact, captured); err != nil {
		return PublishResultV1{}, err
	}
	// ScanArtifactDirectory returns owned contents. Release them before the
	// later bounded verifier/sync scans so this offline operation never
	// intentionally retains two package-sized copies at once.
	for index := range captured.files {
		captured.files[index].Content = nil
	}
	captured.files = nil
	if err := verifyArtifactV1(ctx, stagedArtifact, request.ArtifactDigest, request.ArtifactSizeBytes, limits); err != nil {
		return PublishResultV1{}, errors.New("module artifact store: staged artifact verification failed")
	}
	if err := verifyNewIngressModesV1(ctx, stagedArtifact); err != nil {
		return PublishResultV1{}, err
	}
	count, err := syncArtifactTreeV1(ctx, stagedArtifact, limits)
	if err != nil {
		return PublishResultV1{}, err
	}
	if count != result.CoveredFileCount {
		return PublishResultV1{}, errors.New("module artifact store: staged artifact file count differs")
	}
	if err := syncDirectoryV1(stageRoot); err != nil {
		return PublishResultV1{}, err
	}
	if err := request.Source.VerifyCurrentV1(); err != nil {
		return PublishResultV1{}, err
	}

	reused, err := publishOrReuseV1(
		ctx, stagedArtifact, destination, request.Source.artifactRoot.directory.path,
		request.ArtifactDigest, request.ArtifactSizeBytes, result.CoveredFileCount, limits,
		request.ExistingModePolicy,
	)
	if err != nil {
		return PublishResultV1{}, err
	}
	if err := request.Source.VerifyCurrentV1(); err != nil {
		return PublishResultV1{}, err
	}
	if err := verifyArtifactV1(ctx, destination, request.ArtifactDigest, request.ArtifactSizeBytes, limits); err != nil {
		return PublishResultV1{}, errors.New("module artifact store: published artifact final verification failed")
	}
	if reused {
		if err := verifyArtifactModesV1(ctx, destination, request.ExistingModePolicy); err != nil {
			return PublishResultV1{}, err
		}
	} else if err := verifyNewIngressModesV1(ctx, destination); err != nil {
		return PublishResultV1{}, err
	}
	if err := cleanupStageV1(request.Source.artifactRoot.directory.path, stageRoot); err != nil {
		return PublishResultV1{}, err
	}
	stageRoot = ""
	if err := verifyPhysicalQuotaV1(
		ctx, request.Source.artifactRoot, request.ArtifactDigest,
		request.ArtifactSizeBytes, targetNamespace, true,
	); err != nil {
		return PublishResultV1{}, err
	}
	result.Reused = reused
	return result, nil
}

// VerifyExistingV1 is the source-independent exact-retry path. It validates a
// canonical server-owned root, applies the Backup-compatible byte ceiling,
// rejects unsafe modes, syncs every file/directory, and re-verifies the full
// digest/size/count closure without consulting SourceRoot or a live Snapshot.
func VerifyExistingV1(
	ctx context.Context,
	artifactRoot, digest string,
	size, coveredFileCount uint64,
	modePolicy ExistingModePolicyV1,
) error {
	root, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		return err
	}
	return VerifyExistingRootV1(ctx, root, digest, size, coveredFileCount, modePolicy)
}

func VerifyExistingRootV1(
	ctx context.Context,
	root ArtifactRootV1,
	digest string,
	size, coveredFileCount uint64,
	modePolicy ExistingModePolicyV1,
) (returnErr error) {
	if ctx == nil || !moduleapi.ValidSHA256(digest) || size == 0 ||
		coveredFileCount == 0 || coveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) ||
		!modePolicy.validV1() {
		return errors.New("module artifact store: existing artifact request is invalid")
	}
	limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
	if err != nil || size > uint64(limits.MaxTotalBytes) {
		return errors.New("module artifact store: existing artifact exceeds hard limits")
	}
	if err := root.verifyCurrentV1(); err != nil {
		return err
	}
	lease, err := acquireArtifactRootLeaseV1(ctx, root)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lease.releaseV1(); releaseErr != nil {
			returnErr = errors.Join(returnErr, releaseErr)
		}
	}()
	if err := recoverOrphanStagesV1(root); err != nil {
		return err
	}
	if err := verifyPhysicalQuotaV1(
		ctx, root, digest, size, physicalNamespaceV1{}, true,
	); err != nil {
		return err
	}
	destination := filepath.Join(root.directory.path, digest)
	if err := verifyArtifactV1(ctx, destination, digest, size, limits); err != nil {
		return errors.New("module artifact store: existing artifact verification failed")
	}
	if err := verifyArtifactModesV1(ctx, destination, modePolicy); err != nil {
		return err
	}
	count, err := syncArtifactTreeV1(ctx, destination, limits)
	if err != nil || count != coveredFileCount {
		return errors.New("module artifact store: existing artifact file count differs")
	}
	if err := syncDirectoryV1(root.directory.path); err != nil {
		return errors.New("module artifact store: sync artifact root failed")
	}
	if err := verifyArtifactV1(ctx, destination, digest, size, limits); err != nil {
		return errors.New("module artifact store: existing artifact drifted during sync")
	}
	if err := verifyArtifactModesV1(ctx, destination, modePolicy); err != nil {
		return err
	}
	if err := verifyPhysicalQuotaV1(
		ctx, root, digest, size, physicalNamespaceV1{}, true,
	); err != nil {
		return err
	}
	return root.verifyCurrentV1()
}

type artifactRootLeaseV1 struct {
	fileLock      *artifactRootFileLockV1
	processUnlock func()
}

func acquireArtifactRootLeaseV1(ctx context.Context, root ArtifactRootV1) (*artifactRootLeaseV1, error) {
	if ctx == nil {
		return nil, errors.New("module artifact store: root lease context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := root.verifyCurrentV1(); err != nil {
		return nil, err
	}
	key := artifactLeasePathKeyV1(root.directory.path)
	value, _ := artifactRootLock.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	for !mutex.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	processHeld := true
	defer func() {
		if processHeld {
			mutex.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := root.verifyCurrentV1(); err != nil {
		return nil, err
	}
	path := filepath.Join(root.directory.path, artifactRootLeaseNameV1)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("module artifact store: open root lease failed")
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()
	before, err := os.Lstat(path)
	opened, statErr := file.Stat()
	if err != nil || statErr != nil || !before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(before) ||
		!os.SameFile(before, opened) || !artifactOpenedSingleLinkV1(file) {
		return nil, errors.New("module artifact store: root lease identity is invalid")
	}
	if err := provisionArtifactLeaseFilePrivateV1(path); err != nil {
		return nil, errors.New("module artifact store: secure root lease failed")
	}
	current, err := os.Lstat(path)
	opened, statErr = file.Stat()
	if err != nil || statErr != nil || !os.SameFile(before, current) ||
		!os.SameFile(current, opened) || !artifactOpenedLeaseFilePrivateV1(file, opened) {
		return nil, errors.New("module artifact store: root lease privacy is invalid")
	}
	if err := file.Sync(); err != nil {
		return nil, errors.New("module artifact store: sync root lease failed")
	}
	if err := syncDirectoryV1(root.directory.path); err != nil {
		return nil, errors.New("module artifact store: sync root lease parent failed")
	}
	for {
		fileLock, busy, lockErr := tryLockArtifactRootFileV1(file)
		if lockErr != nil {
			return nil, lockErr
		}
		if !busy {
			if err := root.verifyCurrentV1(); err != nil {
				_ = fileLock.releaseV1()
				closeFile = false
				return nil, err
			}
			closeFile = false
			processHeld = false
			return &artifactRootLeaseV1{
				fileLock: fileLock, processUnlock: mutex.Unlock,
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (lease *artifactRootLeaseV1) releaseV1() error {
	if lease == nil {
		return nil
	}
	lockErr := lease.fileLock.releaseV1()
	lease.fileLock = nil
	if lease.processUnlock != nil {
		lease.processUnlock()
		lease.processUnlock = nil
	}
	return lockErr
}

func recoverOrphanStagesV1(root ArtifactRootV1) error {
	if err := root.verifyCurrentV1(); err != nil {
		return err
	}
	rooted, rootInfo, err := openArtifactTreeRootV1(root.directory.path)
	if err != nil {
		return err
	}
	defer rooted.Close()
	directory, err := rooted.Open(".")
	if err != nil {
		return errors.New("module artifact store: enumerate recovery stages failed")
	}
	stageNames := make([]string, 0, 1)
	var directEntries uint64
	for {
		entries, readErr := directory.ReadDir(artifactRootReadBatchV1)
		for _, entry := range entries {
			directEntries++
			if directEntries > maxArtifactRootDirectEntriesV1 {
				_ = directory.Close()
				return errors.New("module artifact store: artifact root direct-entry limit exceeded")
			}
			name := entry.Name()
			recognized, valid := recoverableStageNameV1(name)
			if !recognized {
				continue
			}
			if !valid {
				_ = directory.Close()
				return errors.New("module artifact store: artifact root contains an invalid stage")
			}
			stageNames = append(stageNames, name)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || len(entries) == 0 {
			_ = directory.Close()
			return errors.New("module artifact store: enumerate recovery stages failed")
		}
	}
	if err := directory.Close(); err != nil {
		return errors.New("module artifact store: close recovery enumeration failed")
	}
	removed := false
	for _, name := range stageNames {
		file, info, err := openRootedArtifactEntryV1(rooted, rootInfo, name)
		if err != nil {
			return errors.New("module artifact store: recovery stage identity is invalid")
		}
		_, modeSafe := artifactModeCandidateV1(info)
		closeErr := file.Close()
		if !info.IsDir() || !modeSafe || closeErr != nil {
			return errors.New("module artifact store: recovery stage is not private directory")
		}
		if err := rooted.RemoveAll(name); err != nil {
			return errors.New("module artifact store: remove recovery stage failed")
		}
		removed = true
	}
	if removed {
		if err := syncDirectoryV1(root.directory.path); err != nil {
			return errors.New("module artifact store: sync recovered artifact root failed")
		}
	}
	return root.verifyCurrentV1()
}

func recoverableStageNameV1(name string) (recognized bool, valid bool) {
	switch {
	case strings.HasPrefix(name, stageBasePrefixV1):
		return true, validStageNameV1(name)
	case strings.HasPrefix(name, legacyModuleApplyStageBasePrefixV1):
		return true, validLegacyModuleApplyStageNameV1(name)
	default:
		return false, false
	}
}

func validStageNameV1(name string) bool {
	remainder := strings.TrimPrefix(name, stageBasePrefixV1)
	if remainder == name || remainder == "" {
		return false
	}
	parts := strings.Split(remainder, "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	processID, err := strconv.ParseUint(parts[0], 10, 31)
	if err != nil || processID == 0 || strconv.FormatUint(processID, 10) != parts[0] {
		return false
	}
	random, err := strconv.ParseUint(parts[1], 10, 32)
	return err == nil && strconv.FormatUint(random, 10) == parts[1]
}

func validLegacyModuleApplyStageNameV1(name string) bool {
	remainder := strings.TrimPrefix(name, legacyModuleApplyStageBasePrefixV1)
	if remainder == name || remainder == "" || strings.Contains(remainder, "-") {
		return false
	}
	random, err := strconv.ParseUint(remainder, 10, 32)
	return err == nil && strconv.FormatUint(random, 10) == remainder
}

// verifyPhysicalQuotaV1 bounds retained inert publications after an
// ambiguous Store commit. The private root may contain only exact digest
// directories plus the fixed private lease. Stages are recovered under the
// exclusive lease before this scan; any residual or malformed stage and every
// unknown direct child fails closed. Every digest tree is fully re-derived
// under Backup-compatible limits; a missing target is charged as a reservation
// before publication.
func verifyPhysicalQuotaV1(
	ctx context.Context,
	root ArtifactRootV1,
	targetDigest string,
	targetSize uint64,
	targetNamespace physicalNamespaceV1,
	requireTarget bool,
) error {
	if ctx == nil || !moduleapi.ValidSHA256(targetDigest) || targetSize == 0 {
		return errors.New("module artifact store: physical quota request is invalid")
	}
	if err := root.verifyCurrentV1(); err != nil {
		return err
	}
	first, err := scanPhysicalArtifactRootV1(ctx, root.directory.path, targetDigest, targetSize)
	if err != nil {
		return err
	}
	scan, err := scanPhysicalArtifactRootV1(ctx, root.directory.path, targetDigest, targetSize)
	if err != nil {
		return err
	}
	if !first.sameClosureV1(scan) {
		return errors.New("module artifact store: physical artifact root drifted during quota scan")
	}
	if err := checkPhysicalQuotaReservationV1(
		scan.artifactCount, scan.totalBytes, scan.namespace,
		scan.targetFound, targetSize, targetNamespace, requireTarget,
	); err != nil {
		return err
	}
	return root.verifyCurrentV1()
}

type physicalArtifactRootScanV1 struct {
	targetDigest string
	targetSize   uint64

	active        *physicalArtifactAccumulatorV1
	artifactCount uint64
	totalBytes    uint64
	namespace     physicalNamespaceV1
	targetFound   bool
	artifacts     map[string]uint64
}

type physicalArtifactAccumulatorV1 struct {
	name                 string
	limits               moduleapi.ArtifactScanLimits
	registry             *artifactTreePathRegistryV1
	pathCount            int
	fileCount            int
	totalBytes           int64
	pathBytes            uint64
	manifestFound        bool
	manifest             []byte
	files                []moduleapi.ArtifactFile
	executableCandidates []string
}

func scanPhysicalArtifactRootV1(
	ctx context.Context,
	root, targetDigest string,
	targetSize uint64,
) (physicalArtifactRootScanV1, error) {
	scan := physicalArtifactRootScanV1{
		targetDigest: targetDigest,
		targetSize:   targetSize,
		artifacts:    make(map[string]uint64),
	}
	err := safefiletree.WalkWithOptions(ctx, root, safefiletree.Options{
		Visit:                  scan.visitV1,
		LeaveDirectory:         scan.leaveDirectoryV1,
		MaxEntries:             int(MaxPhysicalPathsV1 + MaxPhysicalArtifactsV1 + 2),
		MaxEntriesPerDirectory: moduleapi.DefaultArtifactMaxPaths,
	})
	if err != nil {
		return physicalArtifactRootScanV1{}, errors.Join(
			errors.New("module artifact store: physical artifact closure is invalid"), err,
		)
	}
	if scan.active != nil {
		return physicalArtifactRootScanV1{}, errors.New("module artifact store: physical artifact closure is invalid")
	}
	return scan, nil
}

func (scan physicalArtifactRootScanV1) sameClosureV1(other physicalArtifactRootScanV1) bool {
	if scan.artifactCount != other.artifactCount || scan.totalBytes != other.totalBytes ||
		scan.namespace != other.namespace ||
		scan.targetFound != other.targetFound || len(scan.artifacts) != len(other.artifacts) {
		return false
	}
	for digest, size := range scan.artifacts {
		if other.artifacts[digest] != size {
			return false
		}
	}
	return true
}

func (scan *physicalArtifactRootScanV1) visitV1(
	ctx context.Context,
	entry safefiletree.Entry,
) error {
	if !artifactWalkedEntryPrivateV1(entry) {
		return errors.New("module artifact store: physical artifact entry is not private")
	}
	if entry.Path == "." {
		if entry.Kind != safefiletree.KindDirectory {
			return errors.New("module artifact store: physical artifact root is invalid")
		}
		return nil
	}
	if !strings.Contains(entry.Path, "/") {
		name := entry.Path
		if name == artifactRootLeaseNameV1 {
			if scan.active != nil || !artifactWalkedLeaseFilePrivateV1(entry) {
				return errors.New("module artifact store: root lease closure is invalid")
			}
			return nil
		}
		if strings.HasPrefix(name, stageBasePrefixV1) ||
			strings.HasPrefix(name, legacyModuleApplyStageBasePrefixV1) {
			return errors.New("module artifact store: artifact root contains an incomplete cleanup stage")
		}
		if !moduleapi.ValidSHA256(name) || entry.Kind != safefiletree.KindDirectory || scan.active != nil {
			return errors.New("module artifact store: artifact root contains an unknown entry")
		}
		_, modeSafe := artifactModeCandidateV1(entry.Info)
		if !modeSafe {
			return errors.New("module artifact store: physical artifact root mode is invalid")
		}
		scan.artifactCount++
		if scan.artifactCount > MaxPhysicalArtifactsV1 || scan.totalBytes >= MaxPhysicalCoveredBytesV1 {
			return errors.New("module artifact store: physical artifact quota exceeded")
		}
		limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
		if err != nil {
			return err
		}
		remaining := MaxPhysicalCoveredBytesV1 - scan.totalBytes
		if remaining < uint64(limits.MaxTotalBytes) {
			limits.MaxTotalBytes = int64(remaining)
			if limits.MaxFileBytes > limits.MaxTotalBytes {
				limits.MaxFileBytes = limits.MaxTotalBytes
			}
		}
		scan.active = &physicalArtifactAccumulatorV1{
			name: name, limits: limits, registry: newArtifactTreePathRegistryV1(),
		}
		return nil
	}

	artifact := scan.active
	if artifact == nil || !strings.HasPrefix(entry.Path, artifact.name+"/") {
		return errors.New("module artifact store: physical artifact traversal is inconsistent")
	}
	relativePath := strings.TrimPrefix(entry.Path, artifact.name+"/")
	artifact.pathCount++
	if artifact.pathCount > artifact.limits.MaxPaths {
		return errors.New("module artifact store: physical artifact path limit exceeded")
	}
	normalized, err := moduleapi.NormalizeArtifactPath(relativePath)
	if err != nil {
		return errors.New("module artifact store: physical artifact path is invalid")
	}
	if scan.namespace.paths > MaxPhysicalPathsV1 ||
		uint64(artifact.pathCount) > MaxPhysicalPathsV1-scan.namespace.paths {
		return errors.New("module artifact store: physical root path quota exceeded")
	}
	pathBytes := uint64(len(normalized))
	usedPathBytes := scan.namespace.pathBytes + artifact.pathBytes
	if usedPathBytes > MaxPhysicalPathBytesV1 ||
		pathBytes > MaxPhysicalPathBytesV1-usedPathBytes {
		return errors.New("module artifact store: physical root path-byte quota exceeded")
	}
	artifact.pathBytes += pathBytes
	kind := artifactTreeRegularFileV1
	if entry.Kind == safefiletree.KindDirectory {
		kind = artifactTreeDirectoryV1
	}
	if err := artifact.registry.addV1(normalized, kind); err != nil {
		return err
	}
	executable, modeSafe := artifactModeCandidateV1(entry.Info)
	if !modeSafe {
		return errors.New("module artifact store: physical artifact mode is invalid")
	}
	if executable {
		artifact.executableCandidates = append(artifact.executableCandidates, normalized)
		if len(artifact.executableCandidates) > 1 {
			return errors.New("module artifact store: physical artifact has multiple executable files")
		}
	}
	if entry.Kind == safefiletree.KindDirectory {
		return nil
	}
	artifact.fileCount++
	if artifact.fileCount > artifact.limits.MaxFiles {
		return errors.New("module artifact store: physical artifact file limit exceeded")
	}
	if scan.namespace.files > MaxPhysicalFilesV1 ||
		uint64(artifact.fileCount) > MaxPhysicalFilesV1-scan.namespace.files {
		return errors.New("module artifact store: physical root file quota exceeded")
	}
	fileLimit := artifact.limits.MaxFileBytes
	if normalized == moduleapi.ArtifactManifestPath && fileLimit > int64(moduleapi.MaxTextBytes) {
		fileLimit = int64(moduleapi.MaxTextBytes)
	}
	remaining := artifact.limits.MaxTotalBytes - artifact.totalBytes
	if fileLimit > remaining {
		fileLimit = remaining
	}
	content, err := readWalkedArtifactFileV1(ctx, entry, fileLimit)
	if err != nil {
		return err
	}
	artifact.totalBytes += int64(len(content))
	if normalized == moduleapi.ArtifactManifestPath {
		if artifact.manifestFound {
			return errors.New("module artifact store: physical artifact manifest is duplicated")
		}
		artifact.manifestFound = true
		artifact.manifest = content
		return nil
	}
	artifact.files = append(artifact.files, moduleapi.ArtifactFile{Path: normalized, Content: content})
	return nil
}

func (scan *physicalArtifactRootScanV1) leaveDirectoryV1(
	ctx context.Context,
	entry safefiletree.Entry,
) error {
	if entry.Path == "." || strings.Contains(entry.Path, "/") ||
		!moduleapi.ValidSHA256(entry.Path) {
		return nil
	}
	artifact := scan.active
	if artifact == nil || artifact.name != entry.Path || !artifact.manifestFound {
		return errors.New("module artifact store: physical artifact closure is incomplete")
	}
	if _, canonical, err := moduleapi.ParseModuleManifestV1(artifact.manifest); err != nil ||
		!bytes.Equal(canonical, artifact.manifest) {
		return errors.New("module artifact store: physical artifact manifest is invalid")
	}
	digest, err := moduleapi.ComputeArtifactDigest(artifact.manifest, artifact.files)
	if err != nil || digest != artifact.name {
		return errors.New("module artifact store: physical artifact digest differs from directory")
	}
	size := coveredSizeV1(artifact.manifest, artifact.files)
	count := uint64(len(artifact.files)) + 1
	if size == 0 || size != uint64(artifact.totalBytes) ||
		count != uint64(artifact.fileCount) || count > uint64(moduleapi.DefaultArtifactMaxFiles) {
		return errors.New("module artifact store: physical artifact size or count is invalid")
	}
	if len(artifact.executableCandidates) == 1 {
		executable, err := exactLocalMCPExecutableFromCapturedV1(
			ctx, artifact.manifest, artifact.files,
		)
		if err != nil || executable != artifact.executableCandidates[0] {
			return errors.New("module artifact store: executable mode lacks exact descriptor evidence")
		}
	}
	if size > MaxPhysicalCoveredBytesV1-scan.totalBytes {
		return errors.New("module artifact store: physical artifact byte quota exceeded")
	}
	scan.totalBytes += size
	scan.namespace.paths += uint64(artifact.pathCount)
	scan.namespace.files += uint64(artifact.fileCount)
	scan.namespace.pathBytes += artifact.pathBytes
	scan.artifacts[artifact.name] = size
	if scan.targetDigest != "" && artifact.name == scan.targetDigest {
		scan.targetFound = true
		if size != scan.targetSize {
			return errors.New("module artifact store: physical target size differs")
		}
	}
	for index := range artifact.files {
		artifact.files[index].Content = nil
	}
	artifact.files = nil
	artifact.manifest = nil
	scan.active = nil
	return nil
}

func readWalkedArtifactFileV1(
	ctx context.Context,
	entry safefiletree.Entry,
	maximum int64,
) ([]byte, error) {
	if entry.Kind != safefiletree.KindRegularFile || entry.Reader == nil || entry.Info == nil ||
		maximum < 0 || entry.Info.Size() < 0 || entry.Info.Size() > maximum {
		return nil, errors.New("module artifact store: physical artifact file exceeds limits")
	}
	var content bytes.Buffer
	if entry.Info.Size() > 0 {
		content.Grow(int(entry.Info.Size()))
	}
	reader := io.LimitReader(entry.Reader, maximum+1)
	buffer := make([]byte, 32<<10)
	readBytes := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, readErr := reader.Read(buffer)
		if count > 0 {
			readBytes += int64(count)
			if readBytes > maximum {
				return nil, errors.New("module artifact store: physical artifact file exceeds limits")
			}
			_, _ = content.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, errors.New("module artifact store: read physical artifact file failed")
		}
	}
	if readBytes != entry.Info.Size() {
		return nil, errors.New("module artifact store: physical artifact file size drifted")
	}
	return content.Bytes(), nil
}

func checkPhysicalQuotaReservationV1(
	artifactCount, totalBytes uint64,
	namespace physicalNamespaceV1,
	targetFound bool,
	targetSize uint64,
	targetNamespace physicalNamespaceV1,
	requireTarget bool,
) error {
	if artifactCount > MaxPhysicalArtifactsV1 || totalBytes > MaxPhysicalCoveredBytesV1 ||
		namespace.paths > MaxPhysicalPathsV1 || namespace.files > MaxPhysicalFilesV1 ||
		namespace.pathBytes > MaxPhysicalPathBytesV1 {
		return errors.New("module artifact store: physical artifact quota exceeded")
	}
	if requireTarget && !targetFound {
		return errors.New("module artifact store: exact physical target is missing")
	}
	if targetFound {
		return nil
	}
	if targetNamespace.paths == 0 || targetNamespace.files == 0 ||
		targetNamespace.pathBytes == 0 {
		return errors.New("module artifact store: physical namespace reservation is invalid")
	}
	if artifactCount == MaxPhysicalArtifactsV1 ||
		targetSize > MaxPhysicalCoveredBytesV1-totalBytes ||
		targetNamespace.paths > MaxPhysicalPathsV1-namespace.paths ||
		targetNamespace.files > MaxPhysicalFilesV1-namespace.files ||
		targetNamespace.pathBytes > MaxPhysicalPathBytesV1-namespace.pathBytes {
		return errors.New("module artifact store: physical artifact reservation exceeds quota")
	}
	return nil
}

type physicalNamespaceV1 struct {
	paths     uint64
	files     uint64
	pathBytes uint64
}

type capturedArtifactV1 struct {
	manifest []byte
	files    []moduleapi.ArtifactFile
}

func capturedPhysicalNamespaceV1(captured capturedArtifactV1) (physicalNamespaceV1, error) {
	paths := make(map[string]struct{}, len(captured.files)*2+1)
	addPath := func(input string) error {
		normalized, err := moduleapi.NormalizeArtifactPath(input)
		if err != nil || normalized != input {
			return errors.New("module artifact store: captured namespace path is invalid")
		}
		segments := strings.Split(normalized, "/")
		for index := range segments {
			paths[strings.Join(segments[:index+1], "/")] = struct{}{}
		}
		return nil
	}
	if err := addPath(moduleapi.ArtifactManifestPath); err != nil {
		return physicalNamespaceV1{}, err
	}
	for _, file := range captured.files {
		if err := addPath(file.Path); err != nil {
			return physicalNamespaceV1{}, err
		}
	}
	namespace := physicalNamespaceV1{
		paths: uint64(len(paths)), files: uint64(len(captured.files)) + 1,
	}
	for path := range paths {
		pathBytes := uint64(len(path))
		if pathBytes > MaxPhysicalPathBytesV1-namespace.pathBytes {
			return physicalNamespaceV1{}, errors.New("module artifact store: captured namespace exceeds path-byte quota")
		}
		namespace.pathBytes += pathBytes
	}
	if namespace.paths == 0 || namespace.paths > MaxPhysicalPathsV1 ||
		namespace.files == 0 || namespace.files > MaxPhysicalFilesV1 {
		return physicalNamespaceV1{}, errors.New("module artifact store: captured namespace exceeds root quota")
	}
	return namespace, nil
}

func captureArtifactV1(ctx context.Context, root, digest string, size uint64, limits moduleapi.ArtifactScanLimits) (capturedArtifactV1, error) {
	if err := verifyArtifactV1(ctx, root, digest, size, limits); err != nil {
		return capturedArtifactV1{}, captureArtifactFailureV1(
			err,
			"module artifact store: source artifact verification failed",
		)
	}
	manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(ctx, root)
	if err != nil {
		return capturedArtifactV1{}, captureArtifactFailureV1(
			err,
			"module artifact store: source manifest read failed",
		)
	}
	files, err := moduleapi.ScanArtifactDirectoryWithLimitsContext(ctx, root, moduleapi.ArtifactMetadataPaths{}, limits)
	if err != nil {
		return capturedArtifactV1{}, captureArtifactFailureV1(
			err,
			"module artifact store: source artifact capture failed",
		)
	}
	capturedDigest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil || capturedDigest != digest || coveredSizeV1(manifest, files) != size {
		return capturedArtifactV1{}, errors.New("module artifact store: source artifact changed during capture")
	}
	if err := verifyArtifactV1(ctx, root, digest, size, limits); err != nil {
		return capturedArtifactV1{}, captureArtifactFailureV1(
			err,
			"module artifact store: source artifact drifted during capture",
		)
	}
	return capturedArtifactV1{manifest: bytes.Clone(manifest), files: files}, nil
}

func captureArtifactFailureV1(err error, message string) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errors.New(message)
	}
}

func writeCapturedArtifactV1(ctx context.Context, target string, captured capturedArtifactV1) error {
	if err := os.Mkdir(target, 0o700); err != nil {
		return errors.New("module artifact store: create staged artifact failed")
	}
	if err := writeFileV1(ctx, filepath.Join(target, moduleapi.ArtifactManifestPath), captured.manifest, 0o600); err != nil {
		return err
	}
	for _, file := range captured.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(target, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return errors.New("module artifact store: create staged directory failed")
		}
		// File modes are not covered by ArtifactDigest. Ingress is inert and
		// therefore normalizes every file to non-executable 0600; a future
		// installer may enable only an exact descriptor-owned executable.
		if err := writeFileV1(ctx, path, file.Content, 0o600); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func writeFileV1(ctx context.Context, path string, content []byte, mode os.FileMode) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return errors.New("module artifact store: create staged file failed")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, errors.New("module artifact store: close staged file failed"))
		}
	}()
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := content
		if len(chunk) > 32<<10 {
			chunk = chunk[:32<<10]
		}
		written, err := file.Write(chunk)
		if err != nil || written != len(chunk) {
			return errors.New("module artifact store: write staged file failed")
		}
		content = content[written:]
	}
	if err := file.Sync(); err != nil {
		return errors.New("module artifact store: sync staged file failed")
	}
	return ctx.Err()
}

func publishOrReuseV1(
	ctx context.Context,
	staged, destination, artifactRoot, digest string,
	size, coveredFileCount uint64,
	limits moduleapi.ArtifactScanLimits,
	modePolicy ExistingModePolicyV1,
) (bool, error) {
	if _, err := os.Lstat(destination); err == nil {
		if err := verifyAndSyncExistingV1(ctx, destination, artifactRoot, digest, size, coveredFileCount, limits, modePolicy); err != nil {
			return false, err
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("module artifact store: inspect publish target failed")
	}
	if err := publishNoReplaceV1(staged, destination); err != nil {
		if !errors.Is(err, ErrTargetExists) {
			return false, errors.New("module artifact store: atomic publish failed")
		}
		if err := verifyAndSyncExistingV1(ctx, destination, artifactRoot, digest, size, coveredFileCount, limits, modePolicy); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := verifyArtifactV1(ctx, destination, digest, size, limits); err != nil {
		return false, errors.New("module artifact store: newly published artifact differs")
	}
	if err := verifyNewIngressModesV1(ctx, destination); err != nil {
		return false, err
	}
	count, err := syncArtifactTreeV1(ctx, destination, limits)
	if err != nil {
		return false, err
	}
	if count != coveredFileCount {
		return false, errors.New("module artifact store: newly published artifact file count differs")
	}
	if err := syncDirectoryV1(artifactRoot); err != nil {
		return false, errors.New("module artifact store: sync artifact root failed")
	}
	return false, nil
}

func verifyAndSyncExistingV1(
	ctx context.Context,
	destination, artifactRoot, digest string,
	size, coveredFileCount uint64,
	limits moduleapi.ArtifactScanLimits,
	modePolicy ExistingModePolicyV1,
) error {
	if err := verifyArtifactV1(ctx, destination, digest, size, limits); err != nil {
		return errors.New("module artifact store: existing artifact target conflicts")
	}
	if err := verifyArtifactModesV1(ctx, destination, modePolicy); err != nil {
		return err
	}
	count, err := syncArtifactTreeV1(ctx, destination, limits)
	if err != nil || count != coveredFileCount {
		return errors.New("module artifact store: existing artifact file count differs")
	}
	if err := verifyArtifactV1(ctx, destination, digest, size, limits); err != nil {
		return errors.New("module artifact store: existing artifact target drifted during sync")
	}
	if err := verifyArtifactModesV1(ctx, destination, modePolicy); err != nil {
		return err
	}
	if err := syncDirectoryV1(artifactRoot); err != nil {
		return errors.New("module artifact store: sync artifact root failed")
	}
	return nil
}

func verifyArtifactV1(ctx context.Context, root, digest string, size uint64, limits moduleapi.ArtifactScanLimits) error {
	return moduleapi.VerifyArtifactDirectoryDigestAndSizeWithLimitsContext(ctx, root, moduleapi.ArtifactMetadataPaths{}, digest, size, limits)
}

func openArtifactTreeRootV1(path string) (*os.Root, os.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || !exactAbsolutePathV1(path, filepath.Clean(absolute)) {
		return nil, nil, errors.New("module artifact store: rooted artifact path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !samePathV1(absolute, resolved) {
		return nil, nil, errors.New("module artifact store: rooted artifact path traverses a link")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(before) {
		return nil, nil, errors.New("module artifact store: rooted artifact directory is invalid")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, errors.New("module artifact store: open rooted artifact directory failed")
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) ||
		!artifactSameFilesystemV1(before, opened) {
		_ = root.Close()
		return nil, nil, errors.New("module artifact store: rooted artifact identity changed")
	}
	return root, opened, nil
}

func openRootedArtifactEntryV1(
	root *os.Root,
	rootInfo os.FileInfo,
	relative string,
) (*os.File, os.FileInfo, error) {
	if root == nil || rootInfo == nil || relative == "" {
		return nil, nil, errors.New("module artifact store: rooted artifact entry request is invalid")
	}
	before, err := root.Lstat(relative)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(before) ||
		!artifactSameFilesystemV1(rootInfo, before) {
		return nil, nil, errors.New("module artifact store: rooted artifact entry is invalid")
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, nil, errors.New("module artifact store: open rooted artifact entry failed")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.IsDir() != before.IsDir() ||
		opened.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(opened) ||
		!artifactSameFilesystemV1(rootInfo, opened) ||
		(!opened.IsDir() && !artifactOpenedSingleLinkV1(file)) ||
		!artifactOpenedPathPrivateV1(file, opened) {
		_ = file.Close()
		return nil, nil, errors.New("module artifact store: rooted artifact entry changed while opened")
	}
	afterPath, err := root.Lstat(relative)
	afterOpened, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(opened, afterPath) ||
		!os.SameFile(opened, afterOpened) || afterPath.Mode()&os.ModeSymlink != 0 ||
		pathIsReparsePointV1(afterPath) || !artifactSameFilesystemV1(rootInfo, afterOpened) ||
		(!afterOpened.IsDir() && !artifactOpenedSingleLinkV1(file)) ||
		!artifactOpenedPathPrivateV1(file, afterOpened) {
		_ = file.Close()
		return nil, nil, errors.New("module artifact store: rooted artifact entry drifted during inspection")
	}
	return file, afterOpened, nil
}

type artifactTreePathKindV1 uint8

const (
	artifactTreeDirectoryV1 artifactTreePathKindV1 = iota + 1
	artifactTreeRegularFileV1
)

type artifactTreePathRegistryV1 struct {
	exact map[string]artifactTreePathKindV1
	fold  map[string]string
}

func newArtifactTreePathRegistryV1() *artifactTreePathRegistryV1 {
	return &artifactTreePathRegistryV1{
		exact: make(map[string]artifactTreePathKindV1),
		fold:  make(map[string]string),
	}
}

func (registry *artifactTreePathRegistryV1) addV1(
	normalized string,
	kind artifactTreePathKindV1,
) error {
	segments := strings.Split(normalized, "/")
	for index := range segments {
		current := strings.Join(segments[:index+1], "/")
		currentKind := artifactTreeDirectoryV1
		if index == len(segments)-1 {
			currentKind = kind
		}
		if existing, ok := registry.exact[current]; ok {
			if index == len(segments)-1 || existing == artifactTreeRegularFileV1 {
				return errors.New("module artifact store: artifact path normalization collision")
			}
		} else {
			registry.exact[current] = currentKind
		}
		folded := norm.NFC.String(cases.Fold().String(current))
		if existing, ok := registry.fold[folded]; ok && existing != current {
			return errors.New("module artifact store: artifact path case collision")
		}
		registry.fold[folded] = current
	}
	return nil
}

func syncArtifactTreeV1(
	ctx context.Context,
	root string,
	limits moduleapi.ArtifactScanLimits,
) (uint64, error) {
	if limits.MaxPaths <= 0 || limits.MaxFiles <= 0 || limits.MaxFileBytes <= 0 ||
		limits.MaxTotalBytes <= 0 {
		return 0, errors.New("module artifact store: artifact sync limits are invalid")
	}
	registry := newArtifactTreePathRegistryV1()
	pathCount, fileCount := 0, 0
	totalBytes := int64(0)
	manifestFound := false
	err := safefiletree.WalkWithOptions(ctx, root, safefiletree.Options{
		OpenForSync:            true,
		MaxEntries:             limits.MaxPaths,
		MaxEntriesPerDirectory: limits.MaxPaths,
		Visit: func(_ context.Context, entry safefiletree.Entry) error {
			if !artifactWalkedEntryPrivateV1(entry) {
				return errors.New("module artifact store: synced artifact entry is not private")
			}
			if entry.Path == "." {
				return nil
			}
			pathCount++
			if pathCount > limits.MaxPaths {
				return errors.New("module artifact store: artifact sync path limit exceeded")
			}
			normalized, err := moduleapi.NormalizeArtifactPath(entry.Path)
			if err != nil {
				return errors.New("module artifact store: synced artifact path is invalid")
			}
			kind := artifactTreeRegularFileV1
			if entry.Kind == safefiletree.KindDirectory {
				kind = artifactTreeDirectoryV1
			}
			if err := registry.addV1(normalized, kind); err != nil {
				return err
			}
			if entry.Kind == safefiletree.KindDirectory {
				return nil
			}
			fileCount++
			if fileCount > limits.MaxFiles {
				return errors.New("module artifact store: artifact sync file limit exceeded")
			}
			fileLimit := limits.MaxFileBytes
			if normalized == moduleapi.ArtifactManifestPath {
				manifestFound = true
				if fileLimit > int64(moduleapi.MaxTextBytes) {
					fileLimit = int64(moduleapi.MaxTextBytes)
				}
			}
			size := entry.Info.Size()
			if size < 0 || size > fileLimit || size > limits.MaxTotalBytes-totalBytes {
				return errors.New("module artifact store: synced artifact exceeds byte limits")
			}
			totalBytes += size
			if err := entry.Sync(); err != nil {
				return errors.New("module artifact store: sync rooted artifact file failed")
			}
			return nil
		},
		LeaveDirectory: func(_ context.Context, entry safefiletree.Entry) error {
			if !artifactWalkedEntryPrivateV1(entry) {
				return errors.New("module artifact store: synced artifact directory is not private")
			}
			if err := entry.Sync(); err != nil {
				return errors.New("module artifact store: sync rooted artifact directory failed")
			}
			return nil
		},
	})
	if err != nil {
		return 0, errors.Join(errors.New("module artifact store: sync artifact tree failed"), err)
	}
	if !manifestFound {
		return 0, errors.New("module artifact store: synced artifact manifest is missing")
	}
	return uint64(fileCount), nil
}

func (policy ExistingModePolicyV1) validV1() bool {
	return policy == ExistingModeInertOnlyV1 ||
		policy == ExistingModeRootCompatibleV1 ||
		policy == ExistingModeStoreProvenInstalledV1
}

func verifyArtifactModesV1(ctx context.Context, root string, policy ExistingModePolicyV1) error {
	if !policy.validV1() {
		return errors.New("module artifact store: existing mode policy is invalid")
	}
	executableCandidates, err := inspectArtifactModesV1(ctx, root)
	if err != nil {
		return err
	}
	if policy == ExistingModeInertOnlyV1 {
		if len(executableCandidates) == 0 {
			return nil
		}
		return errors.New("module artifact store: executable mode lacks Store installation evidence")
	}
	executable, err := exactLocalMCPExecutableV1(ctx, root)
	if err != nil && (policy == ExistingModeStoreProvenInstalledV1 || len(executableCandidates) != 0) {
		return errors.New("module artifact store: installed executable mode closure is invalid")
	}
	if policy == ExistingModeRootCompatibleV1 {
		// A physical root can be shared by independently fenced Current Stores.
		// A Store that has not installed this digest may therefore encounter an
		// exact tree whose one descriptor-owned executable was enabled by another
		// Store. Mode compatibility proves only physical safety; it grants this
		// Store no Installation, Activation, Binding, or execution authority.
		if len(executableCandidates) == 0 {
			return nil
		}
		if executable == "" {
			return errors.New("module artifact store: compatible executable mode lacks exact descriptor evidence")
		}
		if !artifactExecutableModeObservableV1() {
			return nil
		}
		if len(executableCandidates) != 1 || executableCandidates[0] != executable {
			return errors.New("module artifact store: compatible executable mode differs from descriptor")
		}
		return nil
	}
	if executable == "" {
		if len(executableCandidates) != 0 {
			return errors.New("module artifact store: installed non-local artifact has executable content")
		}
		return nil
	}
	if !artifactExecutableModeObservableV1() {
		// Windows FileMode has no Unix execute-bit analogue. The exact
		// descriptor/SHA closure above plus per-path owner/DACL checks is the
		// platform-equivalent installed boundary.
		return nil
	}
	if len(executableCandidates) != 1 || executableCandidates[0] != executable {
		return errors.New("module artifact store: installed local executable mode differs from descriptor")
	}
	return nil
}

func verifyNewIngressModesV1(ctx context.Context, root string) error {
	executableCandidates, err := inspectArtifactModesV1(ctx, root)
	if err != nil {
		return err
	}
	if len(executableCandidates) != 0 {
		return errors.New("module artifact store: new ingress publication gained executable mode")
	}
	return nil
}

func inspectArtifactModesV1(ctx context.Context, root string) ([]string, error) {
	var executableCandidates []string
	err := safefiletree.WalkWithOptions(ctx, root, safefiletree.Options{
		MaxEntries:             moduleapi.DefaultArtifactMaxPaths,
		MaxEntriesPerDirectory: moduleapi.DefaultArtifactMaxPaths,
		Visit: func(_ context.Context, entry safefiletree.Entry) error {
			if !artifactWalkedEntryPrivateV1(entry) {
				return errors.New("module artifact store: artifact mode entry is not private")
			}
			relative := ""
			if entry.Path != "." {
				var err error
				relative, err = moduleapi.NormalizeArtifactPath(entry.Path)
				if err != nil {
					return errors.New("module artifact store: artifact mode path is invalid")
				}
			}
			executableCandidate, safe := artifactModeCandidateV1(entry.Info)
			if !safe {
				return errors.New("module artifact store: artifact mode is not inert")
			}
			if executableCandidate {
				executableCandidates = append(executableCandidates, relative)
				if len(executableCandidates) > 1 {
					return errors.New("module artifact store: artifact has multiple executable files")
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, errors.Join(errors.New("module artifact store: inspect artifact mode failed"), err)
	}
	return executableCandidates, nil
}

func exactLocalMCPExecutableV1(ctx context.Context, root string) (string, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(ctx, root)
	if err != nil {
		return "", errors.New("module artifact store: read manifest for mode verification failed")
	}
	return exactLocalMCPExecutableWithReaderV1(
		ctx,
		manifestCanonical,
		func(path string, maximum int64) ([]byte, error) {
			return moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(ctx, root, path, maximum)
		},
	)
}

type artifactOrdinaryReaderV1 func(string, int64) ([]byte, error)

func exactLocalMCPExecutableWithReaderV1(
	ctx context.Context,
	manifestCanonical []byte,
	read artifactOrdinaryReaderV1,
) (string, error) {
	if ctx == nil || read == nil {
		return "", errors.New("module artifact store: local process read request is invalid")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || !bytes.Equal(canonical, manifestCanonical) {
		return "", errors.New("module artifact store: manifest mode closure is invalid")
	}
	if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestLocalProcess {
		return "", nil
	}
	if manifest.Runtime.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 {
		return "", errors.New("module artifact store: local process protocol is unsupported")
	}
	descriptorCanonical, err := read(manifest.Runtime.Entrypoint, mcpstdio.MaxHostDescriptorBytesV1)
	if err != nil {
		return "", errors.New("module artifact store: read local process descriptor failed")
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		descriptorCanonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: mcpstdio.MaxHostDescriptorBytesV1, MaxDepth: 32, MaxNodes: 4096,
		},
	)
	if err != nil || !bytes.Equal(checked, descriptorCanonical) || len(checked) == 0 || checked[0] != '{' {
		return "", errors.New("module artifact store: local process descriptor is not exact canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(descriptorCanonical))
	decoder.DisallowUnknownFields()
	var descriptor mcpstdio.HostDescriptorV1
	if err := decoder.Decode(&descriptor); err != nil {
		return "", errors.New("module artifact store: local process descriptor is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("module artifact store: local process descriptor is invalid")
	}
	if descriptor.SchemaVersion != mcpstdio.HostDescriptorSchemaV1 ||
		descriptor.ProtocolVersion != mcpstdio.ProtocolVersionV1 {
		return "", errors.New("module artifact store: local process descriptor schema is invalid")
	}
	executable, err := moduleapi.NormalizeArtifactPath(descriptor.Executable)
	if err != nil || executable != descriptor.Executable || !strings.HasPrefix(executable, "content/") ||
		!moduleapi.ValidSHA256(descriptor.ExecutableSHA256) {
		return "", errors.New("module artifact store: local process executable identity is invalid")
	}
	content, err := read(executable, BackupMaxArtifactFileV1)
	if err != nil {
		return "", errors.New("module artifact store: read local process executable failed")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != descriptor.ExecutableSHA256 {
		return "", errors.New("module artifact store: local process executable digest differs")
	}
	return executable, nil
}

func exactLocalMCPExecutableFromCapturedV1(
	ctx context.Context,
	manifest []byte,
	files []moduleapi.ArtifactFile,
) (string, error) {
	byPath := make(map[string][]byte, len(files)+1)
	byPath[moduleapi.ArtifactManifestPath] = manifest
	for _, file := range files {
		byPath[file.Path] = file.Content
	}
	return exactLocalMCPExecutableWithReaderV1(
		ctx,
		manifest,
		func(path string, maximum int64) ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			content, ok := byPath[path]
			if !ok || int64(len(content)) > maximum {
				return nil, errors.New("captured artifact ordinary file is missing or exceeds limit")
			}
			return content, nil
		},
	)
}

func cleanupStageV1(artifactRoot, stageRoot string) error {
	if err := validateStageRootV1(artifactRoot, stageRoot); err != nil {
		return err
	}
	removeErr := os.RemoveAll(stageRoot)
	if removeErr != nil {
		removeErr = errors.New("module artifact store: remove stage failed")
	}
	var residueErr error
	if _, err := os.Lstat(stageRoot); err == nil {
		residueErr = errors.New("module artifact store: stage remains after cleanup")
	} else if !errors.Is(err, os.ErrNotExist) {
		residueErr = errors.New("module artifact store: stage cleanup verification failed")
	}
	syncErr := syncDirectoryV1(artifactRoot)
	if syncErr != nil {
		syncErr = errors.New("module artifact store: sync artifact root after cleanup failed")
	}
	return errors.Join(removeErr, residueErr, syncErr)
}

func validateStageRootV1(artifactRoot, stageRoot string) error {
	root, err := filepath.Abs(artifactRoot)
	if err != nil {
		return errors.New("module artifact store: artifact root resolution failed")
	}
	stage, err := filepath.Abs(stageRoot)
	if err != nil {
		return errors.New("module artifact store: stage root resolution failed")
	}
	name := filepath.Base(stage)
	if !samePathV1(filepath.Dir(stage), root) || !strings.HasPrefix(name, stagePrefixV1) || moduleapi.ValidSHA256(name) {
		return errors.New("module artifact store: stage is not a hidden direct child")
	}
	return nil
}

func backupCompatibleLimitsV1(maximum uint64) (moduleapi.ArtifactScanLimits, error) {
	defaults := moduleapi.DefaultArtifactScanLimits()
	if maximum == 0 || maximum > uint64(defaults.MaxTotalBytes) || maximum > uint64(BackupMaxArtifactClosureV1) {
		return moduleapi.ArtifactScanLimits{}, errors.New("module artifact store: package limit is outside hard ceilings")
	}
	limits := defaults
	limits.MaxFileBytes = BackupMaxArtifactFileV1
	limits.MaxTotalBytes = int64(maximum)
	if limits.MaxFileBytes > limits.MaxTotalBytes {
		limits.MaxFileBytes = limits.MaxTotalBytes
	}
	return limits, nil
}

func coveredSizeV1(manifest []byte, files []moduleapi.ArtifactFile) uint64 {
	total := uint64(len(manifest))
	for _, file := range files {
		if uint64(len(file.Content)) > ^uint64(0)-total {
			return ^uint64(0)
		}
		total += uint64(len(file.Content))
	}
	return total
}

type stableDirectoryV1 struct {
	path string
	info os.FileInfo
}

func inspectStableDirectoryV1(input string, requireCanonicalAbsolute bool) (stableDirectoryV1, error) {
	if input == "" || input != strings.TrimSpace(input) || !utf8.ValidString(input) || input != moduleapi.CanonicalText(input) || strings.ContainsAny(input, "?#") {
		return stableDirectoryV1{}, errors.New("invalid directory input")
	}
	if requireCanonicalAbsolute && !filepath.IsAbs(input) {
		return stableDirectoryV1{}, errors.New("directory must be absolute")
	}
	if runtime.GOOS == "windows" && unsafeWindowsNamespaceV1(input) {
		return stableDirectoryV1{}, errors.New("unsafe Windows namespace")
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return stableDirectoryV1{}, err
	}
	absolute = filepath.Clean(absolute)
	if requireCanonicalAbsolute && !exactAbsolutePathV1(input, absolute) {
		return stableDirectoryV1{}, errors.New("directory path is not clean")
	}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(absolute)
		if unsafeWindowsNamespaceV1(absolute) || strings.Contains(strings.TrimPrefix(absolute, volume), ":") {
			return stableDirectoryV1{}, errors.New("unsafe Windows directory path")
		}
	}
	before, err := os.Lstat(absolute)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(before) || !before.IsDir() {
		return stableDirectoryV1{}, errors.New("directory is not an ordinary non-link directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !samePathV1(absolute, resolved) {
		return stableDirectoryV1{}, errors.New("directory traverses a link")
	}
	after, err := os.Stat(resolved)
	if err != nil || !after.IsDir() || !os.SameFile(before, after) {
		return stableDirectoryV1{}, errors.New("directory changed during inspection")
	}
	return stableDirectoryV1{path: resolved, info: after}, nil
}

func (directory stableDirectoryV1) verifyCurrentV1() error {
	_, err := directory.currentV1()
	return err
}

func (directory stableDirectoryV1) currentV1() (os.FileInfo, error) {
	if directory.path == "" || directory.info == nil {
		return nil, errors.New("directory selection is empty")
	}
	current, err := os.Lstat(directory.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || pathIsReparsePointV1(current) || !current.IsDir() || !os.SameFile(directory.info, current) {
		return nil, errors.New("directory identity changed")
	}
	resolved, err := filepath.EvalSymlinks(directory.path)
	if err != nil || !samePathV1(directory.path, resolved) {
		return nil, errors.New("directory resolved path changed")
	}
	after, err := os.Stat(resolved)
	if err != nil || !after.IsDir() || !os.SameFile(current, after) || !os.SameFile(directory.info, after) {
		return nil, errors.New("directory changed during current inspection")
	}
	return after, nil
}

func unsafeWindowsNamespaceV1(input string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(normalized, `\\`) || strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) || strings.HasPrefix(lower, `\\.\`) ||
		strings.HasPrefix(filepath.VolumeName(normalized), `\\`)
}

func pathsOverlapV1(left, right string) bool {
	return pathContainsV1(left, right) || pathContainsV1(right, left)
}

func pathContainsV1(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func pathStrictlyWithinV1(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePathV1(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func exactAbsolutePathV1(input, absolute string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(input, absolute)
	}
	return input == absolute
}
