package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const learningMaterializeStagePrefixV1 = ".freeagent-learning-materialize-stage-"

// exportLearningMaterializedArtifactV1 publishes one reproducible, inert
// module package into an Operator-owned handoff path. The path is not the
// active artifact root and carries no install, activation, Binding, authority,
// Trust, Secret, or Catalog decision. An exact pre-existing target is an
// idempotent retry; any other target fails closed.
func exportLearningMaterializedArtifactV1(
	ctx context.Context,
	outputPath string,
	artifact learningcontract.MaterializedArtifactV1,
) (resolvedPath string, created bool, returnErr error) {
	if ctx == nil {
		return "", false, errors.New(
			"learning materialize: export context must not be nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := validateLearningMaterializedArtifactV1(artifact); err != nil {
		return "", false, err
	}
	target, err := resolveNewTarget(outputPath, "Learning artifact handoff")
	if err != nil {
		return "", false, err
	}
	requestedAbsolute, err := filepath.Abs(outputPath)
	if err != nil || !samePath(target, requestedAbsolute) {
		return "", false, errors.New(
			"learning materialize: handoff path must not traverse a symlink or reparse point",
		)
	}
	if _, err := os.Lstat(target); err == nil {
		if err := verifyLearningMaterializedArtifactDirectoryV1(
			ctx,
			target,
			artifact,
		); err != nil {
			return "", false, fmt.Errorf(
				"learning materialize: existing handoff differs: %w",
				err,
			)
		}
		if err := syncInitTreeDirectoriesContext(ctx, target); err != nil {
			return "", false, err
		}
		if err := syncInitDirectory(filepath.Dir(target)); err != nil {
			return "", false, err
		}
		if err := verifyLearningMaterializedArtifactDirectoryV1(
			ctx,
			target,
			artifact,
		); err != nil {
			return "", false, fmt.Errorf(
				"learning materialize: existing handoff drifted during retry verification: %w",
				err,
			)
		}
		return target, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf(
			"learning materialize: inspect handoff target: %w",
			err,
		)
	}

	stage, err := os.MkdirTemp(
		filepath.Dir(target),
		learningMaterializeStagePrefixV1,
	)
	if err != nil {
		return "", false, fmt.Errorf(
			"learning materialize: create handoff stage: %w",
			err,
		)
	}
	stageOwned := true
	defer func() {
		if !stageOwned {
			return
		}
		cleanupErr := os.RemoveAll(stage)
		syncErr := syncInitDirectory(filepath.Dir(stage))
		if cleanupErr != nil || syncErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf(
					"learning materialize: clean handoff stage: %w",
					errors.Join(cleanupErr, syncErr),
				),
			)
		}
	}()
	if err := os.Chmod(stage, 0o700); err != nil {
		return "", false, fmt.Errorf(
			"learning materialize: make handoff stage private: %w",
			err,
		)
	}
	if err := writeExclusiveFileContext(
		ctx,
		filepath.Join(stage, moduleapi.ArtifactManifestPath),
		artifact.ManifestCanonical,
		0o600,
	); err != nil {
		return "", false, err
	}
	payloadPath := filepath.Join(
		stage,
		filepath.FromSlash(artifact.EntrypointPath),
	)
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o700); err != nil {
		return "", false, fmt.Errorf(
			"learning materialize: create payload directory: %w",
			err,
		)
	}
	if err := writeExclusiveFileContext(
		ctx,
		payloadPath,
		artifact.PayloadCanonical,
		0o600,
	); err != nil {
		return "", false, err
	}
	if err := verifyLearningMaterializedArtifactDirectoryV1(
		ctx,
		stage,
		artifact,
	); err != nil {
		return "", false, err
	}
	if err := syncInitTreeDirectoriesContext(ctx, stage); err != nil {
		return "", false, err
	}
	if err := syncInitDirectory(filepath.Dir(stage)); err != nil {
		return "", false, err
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	recheckedTarget, err := resolveNewTarget(
		target,
		"Learning artifact handoff",
	)
	if err != nil || !samePath(recheckedTarget, target) {
		return "", false, errors.New(
			"learning materialize: handoff parent changed before publication",
		)
	}
	recheckedStage, err := resolveExistingDirectory(
		stage,
		"Learning artifact handoff stage",
	)
	if err != nil || !samePath(recheckedStage, stage) {
		return "", false, errors.New(
			"learning materialize: handoff stage changed before publication",
		)
	}

	publishErr := publishInitNoReplace(stage, target)
	if publishErr != nil {
		if verifyErr := verifyLearningMaterializedArtifactDirectoryV1(
			ctx,
			target,
			artifact,
		); verifyErr != nil {
			return "", false, errors.Join(publishErr, verifyErr)
		}
		// Another exporter may have won the no-replace race with the exact
		// same immutable bytes. Its staging path already synced every file
		// before publication; do not require write access to an inert target.
		if err := syncInitTreeDirectoriesContext(ctx, target); err != nil {
			return "", false, err
		}
		if err := syncInitDirectory(filepath.Dir(target)); err != nil {
			return "", false, err
		}
		if err := verifyLearningMaterializedArtifactDirectoryV1(
			ctx,
			target,
			artifact,
		); err != nil {
			return "", false, fmt.Errorf(
				"learning materialize: concurrent handoff drifted after publication: %w",
				err,
			)
		}
		return target, false, nil
	}
	stageOwned = false
	if err := verifyLearningMaterializedArtifactDirectoryV1(
		ctx,
		target,
		artifact,
	); err != nil {
		return "", false, fmt.Errorf(
			"learning materialize: published handoff verification: %w",
			err,
		)
	}
	if err := syncInitTreeDirectoriesContext(ctx, target); err != nil {
		return "", false, err
	}
	if err := syncInitDirectory(filepath.Dir(target)); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func validateLearningMaterializedArtifactV1(
	artifact learningcontract.MaterializedArtifactV1,
) error {
	if len(artifact.ManifestCanonical) == 0 ||
		len(artifact.PayloadCanonical) == 0 ||
		!moduleapi.ValidSHA256(artifact.ArtifactDigest) ||
		artifact.ArtifactSizeBytes == 0 {
		return errors.New(
			"learning materialize: artifact identity is incomplete",
		)
	}
	normalized, err := moduleapi.NormalizeArtifactPath(artifact.EntrypointPath)
	if err != nil || normalized != artifact.EntrypointPath ||
		filepath.ToSlash(filepath.Dir(normalized)) != "content" {
		return errors.New(
			"learning materialize: artifact entrypoint is not one canonical content/ file",
		)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(
		artifact.ManifestCanonical,
	)
	if err != nil || !bytes.Equal(manifestCanonical, artifact.ManifestCanonical) ||
		manifest.Runtime.Entrypoint != artifact.EntrypointPath {
		return errors.New(
			"learning materialize: artifact manifest is not the exact entrypoint parent",
		)
	}
	digest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{{
			Path:    artifact.EntrypointPath,
			Content: bytes.Clone(artifact.PayloadCanonical),
		}},
	)
	if err != nil || digest != artifact.ArtifactDigest ||
		uint64(len(manifestCanonical)+len(artifact.PayloadCanonical)) !=
			artifact.ArtifactSizeBytes {
		return errors.New(
			"learning materialize: artifact digest or size differs from exact bytes",
		)
	}
	return nil
}

func verifyLearningMaterializedArtifactDirectoryV1(
	ctx context.Context,
	directory string,
	artifact learningcontract.MaterializedArtifactV1,
) error {
	if ctx == nil {
		return errors.New("learning materialize: verify context must not be nil")
	}
	resolved, err := resolveExistingDirectory(directory, "Learning artifact handoff")
	if err != nil {
		return err
	}
	// Reject links, hardlinks, special files, oversized files, path-count
	// abuse, digest drift, and size drift before allocating or comparing any
	// complete file contents from an existing Operator-owned directory.
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		resolved,
		moduleapi.ArtifactMetadataPaths{},
		artifact.ArtifactDigest,
		artifact.ArtifactSizeBytes,
	); err != nil {
		return err
	}
	rootEntries, err := os.ReadDir(resolved)
	if err != nil {
		return err
	}
	if len(rootEntries) != 2 || rootEntries[0].Name() != "content" ||
		!rootEntries[0].IsDir() ||
		rootEntries[1].Name() != moduleapi.ArtifactManifestPath ||
		rootEntries[1].IsDir() {
		return errors.New(
			"learning materialize: handoff must contain only content/ and module.yaml",
		)
	}
	contentEntries, err := os.ReadDir(filepath.Join(resolved, "content"))
	if err != nil {
		return err
	}
	expectedPayloadName := filepath.Base(filepath.FromSlash(artifact.EntrypointPath))
	if len(contentEntries) != 1 ||
		contentEntries[0].Name() != expectedPayloadName ||
		contentEntries[0].IsDir() {
		return errors.New(
			"learning materialize: handoff content/ does not contain the exact single payload",
		)
	}
	manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		resolved,
	)
	if err != nil || !bytes.Equal(manifest, artifact.ManifestCanonical) {
		return errors.New(
			"learning materialize: handoff manifest bytes differ",
		)
	}
	payload, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		resolved,
		artifact.EntrypointPath,
		int64(len(artifact.PayloadCanonical)),
	)
	if err != nil || !bytes.Equal(payload, artifact.PayloadCanonical) {
		return errors.New(
			"learning materialize: handoff payload bytes differ",
		)
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		resolved,
		moduleapi.ArtifactMetadataPaths{},
		artifact.ArtifactDigest,
		artifact.ArtifactSizeBytes,
	); err != nil {
		return err
	}
	return ctx.Err()
}
