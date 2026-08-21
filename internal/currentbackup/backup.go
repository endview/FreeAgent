package currentbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// CreateBundle creates one complete, offline, no-overwrite Current Store
// backup. The source Store writer must already be closed. It never reads a
// live database file with ordinary filesystem copy operations.
func CreateBundle(
	ctx context.Context,
	sourceDB string,
	artifactRoot string,
	destination string,
	toolVersion string,
) (result Manifest, returnErr error) {
	if ctx == nil {
		return Manifest{}, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if err := validateOpaque("tool version", toolVersion); err != nil {
		return Manifest{}, err
	}
	sourcePath, err := resolveExistingRegularFile(sourceDB, "source database")
	if err != nil {
		return Manifest{}, err
	}
	artifactPath, err := resolveExistingDirectory(artifactRoot, "artifact root")
	if err != nil {
		return Manifest{}, err
	}
	if _, err := moduleartifactstore.SelectArtifactRootV1(artifactPath); err != nil {
		return Manifest{}, fmt.Errorf("%w: artifact root is unsafe", ErrInvalidInput)
	}
	destinationPath, err := resolveNewPath(destination, "backup destination")
	if err != nil {
		return Manifest{}, err
	}
	if samePath(sourcePath, destinationPath) ||
		pathContains(artifactPath, destinationPath) ||
		pathContains(destinationPath, artifactPath) {
		return Manifest{}, fmt.Errorf(
			"%w: source, artifact root and destination must be disjoint",
			ErrInvalidInput,
		)
	}

	fence, err := acquireOfflineFence(sourcePath)
	if err != nil {
		return Manifest{}, err
	}
	fenceHeld := true
	defer func() {
		if fenceHeld {
			returnErr = errors.Join(returnErr, fence.close())
		}
	}()
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	sourceSidecars, err := captureSQLiteSidecars(sourcePath)
	if err != nil {
		return Manifest{}, err
	}
	sourceSidecarsCleaned := false
	defer func() {
		if !sourceSidecarsCleaned {
			returnErr = errors.Join(
				returnErr,
				removeSQLiteReadResidue(sourcePath, sourceSidecars),
			)
		}
	}()
	if _, err := inspectSnapshot(ctx, sourcePath); err != nil {
		return Manifest{}, err
	}

	temporaryPath, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		filepath.Dir(destinationPath),
		"."+filepath.Base(destinationPath)+".create-*",
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: create bundle staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			returnErr = errors.Join(
				returnErr,
				removePrivateStagingTree(temporaryPath),
			)
		}
	}()
	stagedDatabase := filepath.Join(temporaryPath, databaseName)
	if err := createSQLiteSnapshot(ctx, sourcePath, stagedDatabase); err != nil {
		return Manifest{}, err
	}
	state, err := inspectSnapshot(ctx, stagedDatabase)
	if err != nil {
		return Manifest{}, err
	}
	stagedArtifacts := filepath.Join(temporaryPath, artifactsDirectory)
	if err := os.Mkdir(stagedArtifacts, 0o700); err != nil {
		return Manifest{}, err
	}

	artifactEntries := make([]Artifact, 0, len(state.Installations)+len(state.ModuleArtifacts))
	verifiedArtifacts := make(map[string]verifiedArtifact)
	for _, digest := range distinctArtifactDigests(
		state.Installations,
		state.ModuleArtifacts,
	) {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		sourceArtifact := filepath.Join(artifactPath, digest)
		verified, err := verifyArtifactDirectoryContext(ctx, sourceArtifact, digest)
		if err != nil {
			return Manifest{}, err
		}
		if err := copyVerifiedArtifact(
			ctx,
			sourceArtifact,
			filepath.Join(stagedArtifacts, digest),
			verified,
		); err != nil {
			return Manifest{}, err
		}
		verifiedArtifacts[digest] = verified
		artifactEntries = append(artifactEntries, Artifact{
			Path:      artifactsDirectory + "/" + digest,
			Digest:    digest,
			SizeBytes: verified.sizeBytes,
		})
	}
	if err := verifyArtifactClosure(
		state.Installations,
		state.ModuleArtifacts,
		verifiedArtifacts,
	); err != nil {
		return Manifest{}, err
	}
	databaseIdentity, err := hashRegularFileContext(ctx, stagedDatabase, maxDatabaseBytes)
	if err != nil {
		return Manifest{}, err
	}
	frozen, canonical, err := freezeManifest(Manifest{
		FormatVersion: FormatVersionV1,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		ToolVersion:   toolVersion,
		Database: DatabaseFile{
			Path:      databaseName,
			SHA256:    databaseIdentity.SHA256,
			SizeBytes: databaseIdentity.SizeBytes,
		},
		StoreIdentity: state.Identity,
		Current:       state.Current,
		Artifacts:     artifactEntries,
		ArtifactCount: len(artifactEntries),
		AttemptCounts: state.AttemptCounts,
	})
	if err != nil {
		return Manifest{}, err
	}
	if err := writeExclusiveSyncedFile(
		filepath.Join(temporaryPath, manifestName),
		canonical,
	); err != nil {
		return Manifest{}, err
	}
	verifiedManifest, err := VerifyBundle(ctx, temporaryPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: verify staged bundle: %w", err)
	}
	if !reflect.DeepEqual(frozen, verifiedManifest) {
		return Manifest{}, fmt.Errorf("%w: staged manifest changed", ErrIntegrity)
	}
	if err := syncTreeDirectories(temporaryPath); err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: sync staged bundle: %w", err)
	}
	if err := removeSQLiteReadResidue(sourcePath, sourceSidecars); err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: clean source read residue: %w", err)
	}
	sourceSidecarsCleaned = true
	// Release the source fence before publication so a release failure cannot
	// turn an otherwise successful call into an error with a formal bundle
	// already visible at destination.
	if err := fence.close(); err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: release source fence: %w", err)
	}
	fenceHeld = false
	if err := publishNoReplace(temporaryPath, destinationPath); err != nil {
		return Manifest{}, fmt.Errorf("currentbackup: publish bundle: %w", err)
	}
	published = true
	if err := syncDirectory(filepath.Dir(destinationPath)); err != nil {
		rollbackErr := rollbackPublishedPath(destinationPath)
		published = rollbackErr != nil
		return Manifest{}, errors.Join(
			fmt.Errorf("currentbackup: sync published bundle parent: %w", err),
			rollbackErr,
		)
	}
	return cloneManifest(frozen), nil
}

// VerifyBundle independently verifies one complete bundle without opening any
// writable Current Store path and without trusting the source installation.
func VerifyBundle(
	ctx context.Context,
	bundle string,
) (result Manifest, returnErr error) {
	if ctx == nil {
		return Manifest{}, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	bundlePath, err := resolveExistingDirectory(bundle, "backup bundle")
	if err != nil {
		return Manifest{}, err
	}
	before, captured, err := walkSafeTreeWithPlan(
		ctx,
		bundlePath,
		true,
		safeTreeAccessHooks{},
		safeTreeReadPlan{capture: map[string]int64{
			manifestName: maxManifestBytes,
		}},
	)
	if err != nil {
		return Manifest{}, err
	}
	manifestIdentity, ok := before.files[manifestName]
	if !ok || manifestIdentity.SizeBytes <= 0 ||
		manifestIdentity.SizeBytes > maxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest.json is absent or invalid", ErrInvalidBundle)
	}
	manifestBytes := captured[manifestName]
	manifestSum := sha256.Sum256(manifestBytes)
	if int64(len(manifestBytes)) != manifestIdentity.SizeBytes ||
		hex.EncodeToString(manifestSum[:]) != manifestIdentity.SHA256 {
		return Manifest{}, fmt.Errorf("%w: manifest changed after tree verification", ErrIntegrity)
	}
	manifest, err := restoreManifest(manifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	databaseIdentity, ok := before.files[databaseName]
	if !ok || databaseIdentity.SHA256 != manifest.Database.SHA256 ||
		databaseIdentity.SizeBytes != manifest.Database.SizeBytes {
		return Manifest{}, fmt.Errorf("%w: database identity differs", ErrIntegrity)
	}
	if _, ok := before.directories[artifactsDirectory]; !ok {
		return Manifest{}, fmt.Errorf("%w: artifacts directory is absent", ErrInvalidBundle)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, exists := before.files[databaseName+suffix]; exists {
			return Manifest{}, fmt.Errorf(
				"%w: standalone database has sidecar %s",
				ErrIntegrity,
				suffix,
			)
		}
	}
	inspectionRoot, err := provisionPrivateBundleInspectionRootV1(bundlePath)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			removePrivateStagingTree(inspectionRoot),
		)
	}()
	databasePath := filepath.Join(inspectionRoot, databaseName)
	manifestCaptures := make(map[string]int64, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		manifestCaptures[item.Path+"/"+moduleapi.ArtifactManifestPath] =
			int64(moduleapi.MaxTextBytes)
	}
	consumedTree, artifactManifestBytes, err := walkSafeTreeWithPlan(
		ctx,
		bundlePath,
		true,
		safeTreeAccessHooks{},
		safeTreeReadPlan{
			capture: manifestCaptures,
			copies: map[string]safeTreeCopyTarget{
				databaseName: {
					destination: databasePath,
					expected:    databaseIdentity,
				},
			},
		},
	)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return Manifest{}, err
		}
		return Manifest{}, fmt.Errorf(
			"%w: consume bundle content for inspection: %w",
			ErrIntegrity,
			err,
		)
	}
	if !sameSafeTree(before, consumedTree) {
		return Manifest{}, fmt.Errorf("%w: bundle changed before content inspection", ErrIntegrity)
	}
	if err := verifyNoSQLiteSidecars(databasePath); err != nil {
		return Manifest{}, err
	}
	state, err := inspectSnapshot(ctx, databasePath)
	if err != nil {
		return Manifest{}, err
	}
	if state.Identity != manifest.StoreIdentity ||
		state.AttemptCounts != manifest.AttemptCounts ||
		!reflect.DeepEqual(state.Current, manifest.Current) {
		return Manifest{}, fmt.Errorf("%w: database state differs from manifest", ErrIntegrity)
	}

	manifestArtifacts := make(map[string]Artifact, len(manifest.Artifacts))
	verifiedArtifacts := make(map[string]verifiedArtifact, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		verified, err := verifyRootedBundleArtifactContext(
			ctx,
			consumedTree,
			item,
			artifactManifestBytes[item.Path+"/"+moduleapi.ArtifactManifestPath],
		)
		if err != nil {
			return Manifest{}, err
		}
		manifestArtifacts[item.Digest] = item
		verifiedArtifacts[item.Digest] = verified
	}
	if err := verifyArtifactClosure(
		state.Installations,
		state.ModuleArtifacts,
		verifiedArtifacts,
	); err != nil {
		return Manifest{}, err
	}
	for _, digest := range distinctArtifactDigests(
		state.Installations,
		state.ModuleArtifacts,
	) {
		if _, ok := manifestArtifacts[digest]; !ok {
			return Manifest{}, fmt.Errorf(
				"%w: managed artifact %s is absent",
				ErrIntegrity,
				digest,
			)
		}
	}
	if err := verifyExactBundleTree(before, manifest, verifiedArtifacts); err != nil {
		return Manifest{}, err
	}
	after, err := walkBundleSafeTreeContext(ctx, bundlePath)
	if err != nil {
		return Manifest{}, err
	}
	if !sameSafeTree(before, after) {
		return Manifest{}, fmt.Errorf("%w: bundle changed during verification", ErrIntegrity)
	}
	return cloneManifest(manifest), nil
}

// provisionPrivateBundleInspectionRootV1 never falls back to the shared
// system temporary directory. A normal user cache is preferred, while a
// trusted bundle parent keeps offline verification
// available in minimal service environments where the cache directory has not
// been created. Every candidate still passes the complete owner, ACL/mode and
// ancestry checks before any database byte is written.
func provisionPrivateBundleInspectionRootV1(bundlePath string) (string, error) {
	candidates := make([]string, 0, 2)
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		candidates = append(candidates, cache)
	}
	candidates = append(candidates, filepath.Dir(bundlePath))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		key := filepath.Clean(absolute)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		root, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
			key,
			"freeagent-bundle-verify-*",
		)
		if err == nil {
			return root, nil
		}
	}
	return "", errors.New("currentbackup: no trusted private verification staging parent is available")
}

func verifyExactBundleTree(
	tree safeTree,
	manifest Manifest,
	artifacts map[string]verifiedArtifact,
) error {
	expectedFiles := map[string]struct{}{
		manifestName: {},
		databaseName: {},
	}
	expectedDirectories := map[string]struct{}{
		artifactsDirectory: {},
	}
	for _, item := range manifest.Artifacts {
		prefix := item.Path
		expectedDirectories[prefix] = struct{}{}
		artifact := artifacts[item.Digest]
		for directory := range artifact.tree.directories {
			expectedDirectories[prefix+"/"+directory] = struct{}{}
		}
		for path := range artifact.tree.files {
			expectedFiles[prefix+"/"+path] = struct{}{}
		}
	}
	if len(tree.files) != len(expectedFiles) ||
		len(tree.directories) != len(expectedDirectories) {
		return fmt.Errorf("%w: bundle has missing or extra paths", ErrInvalidBundle)
	}
	for path := range expectedFiles {
		if _, ok := tree.files[path]; !ok {
			return fmt.Errorf("%w: missing file %s", ErrInvalidBundle, path)
		}
	}
	for path := range expectedDirectories {
		if _, ok := tree.directories[path]; !ok {
			return fmt.Errorf("%w: missing directory %s", ErrInvalidBundle, path)
		}
	}
	return nil
}

// RestoreBundle restores into two caller-selected targets that must not
// already exist. It stages and re-verifies exact bytes, never initializes,
// migrates, seeds, repairs, or opens the destination as a Store owner.
func RestoreBundle(
	ctx context.Context,
	bundle string,
	destinationDB string,
	destinationArtifactRoot string,
) (returnErr error) {
	manifest, err := VerifyBundle(ctx, bundle)
	if err != nil {
		return err
	}
	bundlePath, err := resolveExistingDirectory(bundle, "backup bundle")
	if err != nil {
		return err
	}
	databaseTarget, err := resolveNewPath(destinationDB, "restore database target")
	if err != nil {
		return err
	}
	artifactTarget, err := resolveNewPath(
		destinationArtifactRoot,
		"restore artifact target",
	)
	if err != nil {
		return err
	}
	if pathContains(bundlePath, databaseTarget) ||
		pathContains(bundlePath, artifactTarget) ||
		pathContains(databaseTarget, artifactTarget) ||
		pathContains(artifactTarget, databaseTarget) {
		return fmt.Errorf("%w: bundle and restore targets must be disjoint", ErrInvalidInput)
	}
	restoreCaptures := make(map[string]int64, len(manifest.Artifacts)+1)
	restoreCaptures[manifestName] = maxManifestBytes
	for _, item := range manifest.Artifacts {
		restoreCaptures[item.Path+"/"+moduleapi.ArtifactManifestPath] =
			int64(moduleapi.MaxTextBytes)
	}
	restoreSourceTree, restoredSourceBytes, err := walkSafeTreeWithPlan(
		ctx,
		bundlePath,
		true,
		safeTreeAccessHooks{},
		safeTreeReadPlan{capture: restoreCaptures},
	)
	if err != nil {
		return err
	}
	_, expectedManifestBytes, err := freezeManifest(manifest)
	if err != nil {
		return err
	}
	manifestIdentity, ok := restoreSourceTree.files[manifestName]
	manifestSum := sha256.Sum256(expectedManifestBytes)
	if !ok || int64(len(expectedManifestBytes)) != manifestIdentity.SizeBytes ||
		hex.EncodeToString(manifestSum[:]) != manifestIdentity.SHA256 {
		return fmt.Errorf("%w: bundle manifest changed before restore", ErrIntegrity)
	}
	currentManifestBytes := restoredSourceBytes[manifestName]
	if !bytes.Equal(currentManifestBytes, expectedManifestBytes) {
		return fmt.Errorf("%w: bundle manifest drifted before restore", ErrIntegrity)
	}
	databaseIdentity, ok := restoreSourceTree.files[databaseName]
	if !ok || databaseIdentity.SizeBytes != manifest.Database.SizeBytes ||
		databaseIdentity.SHA256 != manifest.Database.SHA256 {
		return fmt.Errorf("%w: bundle database changed before restore", ErrIntegrity)
	}

	artifactStage, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		filepath.Dir(artifactTarget),
		"."+filepath.Base(artifactTarget)+".restore-*",
	)
	if err != nil {
		return err
	}
	artifactPublished := false
	defer func() {
		if !artifactPublished {
			returnErr = errors.Join(
				returnErr,
				removePrivateStagingTree(artifactStage),
			)
		}
	}()
	databaseStageRoot, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		filepath.Dir(databaseTarget),
		"."+filepath.Base(databaseTarget)+".database-restore-*",
	)
	if err != nil {
		return err
	}
	databaseStage := filepath.Join(databaseStageRoot, databaseName)
	databasePublished := false
	defer func() {
		if !databasePublished {
			removeSQLiteFiles(databaseStage)
		}
		if databaseStageRoot != "" {
			returnErr = errors.Join(
				returnErr,
				removePrivateStagingTree(databaseStageRoot),
			)
		}
	}()
	sourceArtifacts := make(map[string]verifiedArtifact, len(manifest.Artifacts))
	copyPlan := map[string]safeTreeCopyTarget{
		databaseName: {
			destination: databaseStage,
			expected: fileIdentity{
				SizeBytes: manifest.Database.SizeBytes,
				SHA256:    manifest.Database.SHA256,
			},
		},
	}
	for _, item := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		verified, err := verifyRootedBundleArtifactContext(
			ctx,
			restoreSourceTree,
			item,
			restoredSourceBytes[item.Path+"/"+moduleapi.ArtifactManifestPath],
		)
		if err != nil {
			return err
		}
		destination := filepath.Join(artifactStage, item.Digest)
		if err := os.Mkdir(destination, 0o700); err != nil {
			return err
		}
		if err := prepareVerifiedArtifactDestination(destination, verified); err != nil {
			return err
		}
		for path, identity := range verified.tree.files {
			copyPlan[item.Path+"/"+path] = safeTreeCopyTarget{
				destination: filepath.Join(destination, filepath.FromSlash(path)),
				expected:    identity,
			}
		}
		sourceArtifacts[item.Digest] = verified
	}
	if err := verifyExactBundleTree(
		restoreSourceTree,
		manifest,
		sourceArtifacts,
	); err != nil {
		return err
	}
	copiedSourceTree, _, err := walkSafeTreeWithPlan(
		ctx,
		bundlePath,
		true,
		safeTreeAccessHooks{},
		safeTreeReadPlan{copies: copyPlan},
	)
	if err != nil {
		return err
	}
	if !sameSafeTree(restoreSourceTree, copiedSourceTree) {
		return fmt.Errorf("%w: bundle changed while restore bytes were copied", ErrIntegrity)
	}
	if err := verifyNoSQLiteSidecars(databaseStage); err != nil {
		return err
	}
	stagedState, err := inspectSnapshot(ctx, databaseStage)
	if err != nil {
		return err
	}
	if stagedState.Identity != manifest.StoreIdentity ||
		stagedState.AttemptCounts != manifest.AttemptCounts ||
		!reflect.DeepEqual(stagedState.Current, manifest.Current) {
		return fmt.Errorf("%w: staged database differs from manifest", ErrIntegrity)
	}
	installedArtifacts := make(map[string]struct{}, len(stagedState.Installations))
	for _, installation := range stagedState.Installations {
		installedArtifacts[installation.ArtifactDigest] = struct{}{}
	}
	for _, item := range manifest.Artifacts {
		verified := sourceArtifacts[item.Digest]
		destination := filepath.Join(artifactStage, item.Digest)
		if err := verifyCopiedArtifactDestination(ctx, destination, verified); err != nil {
			return err
		}
		_, installed := installedArtifacts[item.Digest]
		if err := normalizeRestoredArtifactModes(
			ctx,
			destination,
			verified,
			installed,
		); err != nil {
			return err
		}
	}
	if err := syncTreeDirectories(artifactStage); err != nil {
		return err
	}
	stagedArtifactRoot, err := moduleartifactstore.SelectArtifactRootV1(artifactStage)
	if err != nil {
		return fmt.Errorf("%w: restored artifact stage is unsafe", ErrIntegrity)
	}
	if err := moduleartifactstore.VerifyPhysicalRootClosureV1(ctx, stagedArtifactRoot); err != nil {
		return fmt.Errorf("%w: restored artifact stage exceeds physical closure: %v", ErrIntegrity, err)
	}
	if err := syncRegularFile(databaseStage); err != nil {
		return err
	}
	stagedState, err = verifyStagedRestore(
		ctx,
		databaseStage,
		artifactStage,
		manifest,
	)
	if err != nil {
		return err
	}
	// Publish artifacts first; the database is the restore commit marker. If a
	// target races into existence, publication is no-overwrite. Once this root
	// becomes visible, later failures retain it rather than guessing that no
	// other Store has referenced it and deleting a possibly committed tree.
	if err := publishNoReplace(artifactStage, artifactTarget); err != nil {
		return fmt.Errorf("currentbackup: publish restored artifacts: %w", err)
	}
	artifactPublished = true
	if err := syncDirectory(filepath.Dir(artifactTarget)); err != nil {
		return fmt.Errorf("currentbackup: sync restored artifact parent: %w", err)
	}
	publishedArtifactRoot, err := moduleartifactstore.SelectArtifactRootV1(artifactTarget)
	if err != nil {
		return fmt.Errorf("currentbackup: published artifact root is unsafe: %w", err)
	}
	// Once the root is visible, never guess that it remains unreferenced and
	// delete it on a later error. A different Store may have waited for and then
	// acquired this same persistent lease. Retaining a verified, authority-free
	// orphan is safer than deleting a possibly committed publication.
	return moduleartifactstore.WithArtifactRootWriteLeaseV1(
		ctx,
		publishedArtifactRoot,
		func(lease *moduleartifactstore.ArtifactRootWriteLeaseV1) error {
			if err := lease.VerifyPhysicalClosureV1(ctx); err != nil {
				return fmt.Errorf("currentbackup: published artifact root exceeds physical closure: %w", err)
			}
			// A different Store may have won the lease in the short interval after
			// the no-replace directory publication. Re-prove the exact restored DB
			// and artifact set while fenced, before making the database commit marker
			// visible. Any added digest therefore leaves only an authority-free root.
			if _, err := verifyStagedRestore(
				ctx,
				databaseStage,
				artifactTarget,
				manifest,
			); err != nil {
				return err
			}
			if err := syncDirectory(databaseStageRoot); err != nil {
				return fmt.Errorf("currentbackup: sync staged database directory: %w", err)
			}
			if err := publishNoReplace(databaseStage, databaseTarget); err != nil {
				return fmt.Errorf("currentbackup: publish restored database: %w", err)
			}
			databasePublished = true
			if err := syncDirectory(databaseStageRoot); err != nil {
				return fmt.Errorf("currentbackup: sync emptied database stage: %w", err)
			}
			if err := os.Remove(databaseStageRoot); err != nil {
				return fmt.Errorf("currentbackup: remove empty database stage: %w", err)
			}
			databaseStageRoot = ""
			if err := syncDirectory(filepath.Dir(databaseTarget)); err != nil {
				return fmt.Errorf("currentbackup: sync restored database parent: %w", err)
			}
			// Publication does not authorize normal startup. Re-verify the paths now
			// visible to the operator while every cooperating artifact writer is fenced.
			finalDatabaseIdentity, err := hashRegularFileContext(
				ctx,
				databaseTarget,
				maxDatabaseBytes,
			)
			if err != nil {
				return err
			}
			if finalDatabaseIdentity.SHA256 != manifest.Database.SHA256 ||
				finalDatabaseIdentity.SizeBytes != manifest.Database.SizeBytes {
				return fmt.Errorf("%w: published database digest differs", ErrIntegrity)
			}
			finalState, err := inspectSnapshot(ctx, databaseTarget)
			if err != nil {
				return err
			}
			if finalState.Identity != manifest.StoreIdentity ||
				finalState.AttemptCounts != manifest.AttemptCounts ||
				!reflect.DeepEqual(finalState.Current, manifest.Current) {
				return fmt.Errorf("%w: published restore differs", ErrIntegrity)
			}
			finalArtifacts := make(map[string]verifiedArtifact, len(manifest.Artifacts))
			for _, item := range manifest.Artifacts {
				verified, err := verifyArtifactDirectoryContext(
					ctx,
					filepath.Join(artifactTarget, item.Digest),
					item.Digest,
				)
				if err != nil {
					return err
				}
				if verified.sizeBytes != item.SizeBytes {
					return fmt.Errorf(
						"%w: published artifact %s size differs",
						ErrIntegrity,
						item.Digest,
					)
				}
				finalArtifacts[item.Digest] = verified
			}
			if err := verifyArtifactClosure(
				finalState.Installations,
				finalState.ModuleArtifacts,
				finalArtifacts,
			); err != nil {
				return err
			}
			if err := verifyExactArtifactRoot(
				ctx,
				artifactTarget,
				manifest,
				finalArtifacts,
			); err != nil {
				return err
			}
			return lease.VerifyPhysicalClosureV1(ctx)
		},
	)
}

func verifyStagedRestore(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	manifest Manifest,
) (snapshotState, error) {
	databaseIdentity, err := hashRegularFileContext(ctx, databasePath, maxDatabaseBytes)
	if err != nil {
		return snapshotState{}, err
	}
	if databaseIdentity.SHA256 != manifest.Database.SHA256 ||
		databaseIdentity.SizeBytes != manifest.Database.SizeBytes {
		return snapshotState{}, fmt.Errorf("%w: staged database digest differs", ErrIntegrity)
	}
	if err := verifyNoSQLiteSidecars(databasePath); err != nil {
		return snapshotState{}, err
	}
	state, err := inspectSnapshot(ctx, databasePath)
	if err != nil {
		return snapshotState{}, err
	}
	if state.Identity != manifest.StoreIdentity ||
		state.AttemptCounts != manifest.AttemptCounts ||
		!reflect.DeepEqual(state.Current, manifest.Current) {
		return snapshotState{}, fmt.Errorf("%w: staged database differs from manifest", ErrIntegrity)
	}
	artifacts := make(map[string]verifiedArtifact, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return snapshotState{}, err
		}
		verified, err := verifyArtifactDirectoryContext(
			ctx,
			filepath.Join(artifactRoot, item.Digest),
			item.Digest,
		)
		if err != nil {
			return snapshotState{}, err
		}
		if verified.sizeBytes != item.SizeBytes {
			return snapshotState{}, fmt.Errorf(
				"%w: staged artifact %s size differs",
				ErrIntegrity,
				item.Digest,
			)
		}
		artifacts[item.Digest] = verified
	}
	if err := verifyArtifactClosure(
		state.Installations,
		state.ModuleArtifacts,
		artifacts,
	); err != nil {
		return snapshotState{}, err
	}
	if err := verifyExactArtifactRoot(ctx, artifactRoot, manifest, artifacts); err != nil {
		return snapshotState{}, err
	}
	return state, nil
}

func verifyExactArtifactRoot(
	ctx context.Context,
	root string,
	manifest Manifest,
	artifacts map[string]verifiedArtifact,
) error {
	tree, _, err := walkSafeTreeWithPlan(
		ctx,
		root,
		false,
		safeTreeAccessHooks{},
		safeTreeReadPlan{
			allowArtifactRootLeaseControl: true,
			allowAggregateArtifactBytes:   true,
		},
	)
	if err != nil {
		return err
	}
	expectedFiles := make(map[string]struct{})
	expectedDirectories := make(map[string]struct{})
	for _, item := range manifest.Artifacts {
		expectedDirectories[item.Digest] = struct{}{}
		artifact := artifacts[item.Digest]
		for directory := range artifact.tree.directories {
			expectedDirectories[item.Digest+"/"+directory] = struct{}{}
		}
		for path := range artifact.tree.files {
			expectedFiles[item.Digest+"/"+path] = struct{}{}
		}
	}
	actualFileCount := len(tree.files)
	for path := range tree.files {
		if moduleartifactstore.IsArtifactRootControlEntryV1(path) {
			actualFileCount--
		}
	}
	if actualFileCount != len(expectedFiles) ||
		len(tree.directories) != len(expectedDirectories) {
		return fmt.Errorf("%w: artifact root has missing or extra paths", ErrIntegrity)
	}
	for path := range expectedFiles {
		if _, ok := tree.files[path]; !ok {
			return fmt.Errorf("%w: staged artifact file %s is absent", ErrIntegrity, path)
		}
	}
	for path := range expectedDirectories {
		if _, ok := tree.directories[path]; !ok {
			return fmt.Errorf("%w: staged artifact directory %s is absent", ErrIntegrity, path)
		}
	}
	return nil
}

func rollbackPublishedPath(path string) error {
	parent := filepath.Dir(path)
	placeholder, err := os.MkdirTemp(parent, ".freeagent-restore-rollback-*")
	if err != nil {
		return fmt.Errorf("currentbackup: create restore rollback path: %w", err)
	}
	if err := os.Remove(placeholder); err != nil {
		return fmt.Errorf("currentbackup: clear restore rollback path: %w", err)
	}
	if err := publishNoReplace(path, placeholder); err != nil {
		return fmt.Errorf("currentbackup: roll back artifact publication: %w", err)
	}
	if err := os.RemoveAll(placeholder); err != nil {
		return fmt.Errorf("currentbackup: remove rolled-back artifact tree: %w", err)
	}
	return syncDirectory(parent)
}
