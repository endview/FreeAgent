package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
)

const learningMaterializeResultSchemaV1 = "freeagent.learning-materialization-result/v1"

type learningMaterializeModuleResultV1 struct {
	ID                string `json:"id"`
	ExactVersion      string `json:"exact_version"`
	ArtifactDigest    string `json:"artifact_digest"`
	ArtifactSizeBytes uint64 `json:"artifact_size_bytes"`
}

type learningMaterializeResultV1 struct {
	SchemaVersion    string                                 `json:"schema_version"`
	ProposalID       string                                 `json:"proposal_id"`
	ProposalRevision uint64                                 `json:"proposal_revision"`
	VersionID        string                                 `json:"version_id"`
	Version          learningcontract.MaterializedVersionV1 `json:"version"`
	Module           learningMaterializeModuleResultV1      `json:"module"`
	MaterializedAt   string                                 `json:"materialized_at"`
	ArtifactSource   string                                 `json:"artifact_source"`
	VersionCreated   bool                                   `json:"version_created"`
	ExportCreated    bool                                   `json:"export_created"`
	OperatorAction   string                                 `json:"operator_action"`
}

func runLearningMaterialize(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("learning-materialize", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String(
		"artifact-root",
		"",
		"active content-addressed artifact root; used only to enforce a disjoint handoff path",
	)
	tenantID := flags.String("tenant", defaultTenantID, "tenant identity")
	proposalID := flags.String("proposal", "", "exact approved Learning Proposal identity")
	expectedRevision := flags.Uint64(
		"proposal-revision",
		0,
		"exact APPROVED Proposal review revision (2 or 3)",
	)
	outputArtifact := flags.String(
		"output-artifact",
		"",
		"new or exact existing Operator-owned artifact handoff directory",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" ||
		strings.TrimSpace(*proposalID) == "" ||
		*expectedRevision == 0 ||
		strings.TrimSpace(*outputArtifact) == "" {
		return errors.New(
			"freeagent learning-materialize: --db, --tenant, --proposal, --proposal-revision and --output-artifact are required",
		)
	}
	activeArtifactRoot, err := resolveExistingDirectory(
		resolvedArtifactRoot(*databasePath, *artifactRoot),
		"active artifact root",
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-materialize: %w", err)
	}
	handoffTarget, err := resolveNewTarget(
		*outputArtifact,
		"Learning artifact handoff",
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-materialize: %w", err)
	}
	databaseAbsolute, err := filepath.Abs(*databasePath)
	if err != nil {
		return fmt.Errorf(
			"freeagent learning-materialize: resolve Current Store path: %w",
			err,
		)
	}
	databaseAbsolute, err = filepath.EvalSymlinks(databaseAbsolute)
	if err != nil {
		return fmt.Errorf(
			"freeagent learning-materialize: resolve Current Store path: %w",
			err,
		)
	}
	if err := requireLearningMaterializeHandoffDisjointV1(
		handoffTarget,
		activeArtifactRoot,
		databaseAbsolute,
	); err != nil {
		return fmt.Errorf("freeagent learning-materialize: %w", err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent learning-materialize: %w", err)
	}
	materialized, materializeErr := store.MaterializeApprovedLearningProposal(
		ctx,
		currentstore.MaterializeApprovedLearningProposalInput{
			TenantID:                 *tenantID,
			ProposalID:               *proposalID,
			ExpectedProposalRevision: *expectedRevision,
		},
	)
	closeErr := store.Close()
	if err := errors.Join(materializeErr, closeErr); err != nil {
		return fmt.Errorf("freeagent learning-materialize: %w", err)
	}
	handoffTarget, err = recheckLearningMaterializeHandoffPathsV1(
		handoffTarget,
		activeArtifactRoot,
		databaseAbsolute,
	)
	if err != nil {
		return fmt.Errorf(
			"freeagent learning-materialize: Version is durable; handoff path may be corrected and retried: %w",
			err,
		)
	}
	record := materialized.Record
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		record.VersionCanonical,
		record.VersionID,
		record.Proposal.ProposalCanonical,
		record.Proposal.DraftCanonical,
	)
	if err != nil {
		return fmt.Errorf(
			"freeagent learning-materialize: reconstruct exact artifact: %w",
			err,
		)
	}
	resolvedHandoff, exportCreated, err := exportLearningMaterializedArtifactV1(
		ctx,
		handoffTarget,
		artifact,
	)
	if err != nil {
		return fmt.Errorf(
			"freeagent learning-materialize: Version is durable; exact handoff export may be retried: %w",
			err,
		)
	}
	result := learningMaterializeResultV1{
		SchemaVersion:    learningMaterializeResultSchemaV1,
		ProposalID:       record.Proposal.ProposalID,
		ProposalRevision: record.Version.ProposalRevision,
		VersionID:        record.VersionID,
		Version:          record.Version,
		Module: learningMaterializeModuleResultV1{
			ID:                record.Version.Target.ID,
			ExactVersion:      record.Version.Target.Version,
			ArtifactDigest:    record.ArtifactDigest,
			ArtifactSizeBytes: record.ArtifactSizeBytes,
		},
		MaterializedAt: record.MaterializedAt.UTC().Format(time.RFC3339Nano),
		ArtifactSource: resolvedHandoff,
		VersionCreated: materialized.Created,
		ExportCreated:  exportCreated,
		OperatorAction: "AUTHOR_EXACT_MODULE_APPLY_PLAN_THEN_RUN_MODULE_DRY_RUN",
	}
	return writeCommandJSON(stdout, result)
}

func requireLearningMaterializeHandoffDisjointV1(
	handoffTarget string,
	activeArtifactRoot string,
	databasePath string,
) error {
	if samePath(handoffTarget, activeArtifactRoot) ||
		pathContains(activeArtifactRoot, handoffTarget) ||
		pathContains(handoffTarget, activeArtifactRoot) ||
		samePath(handoffTarget, databasePath) ||
		pathContains(handoffTarget, databasePath) ||
		pathContains(databasePath, handoffTarget) {
		return errors.New(
			"handoff must be disjoint from the Current Store and active artifact root",
		)
	}
	return nil
}

func recheckLearningMaterializeHandoffPathsV1(
	handoffTarget string,
	activeArtifactRoot string,
	databasePath string,
) (string, error) {
	recheckedHandoff, err := resolveNewTarget(
		handoffTarget,
		"Learning artifact handoff",
	)
	if err != nil || !samePath(recheckedHandoff, handoffTarget) {
		return "", errors.New("handoff parent changed after Version materialization")
	}
	recheckedArtifactRoot, err := resolveExistingDirectory(
		activeArtifactRoot,
		"active artifact root",
	)
	if err != nil || !samePath(recheckedArtifactRoot, activeArtifactRoot) {
		return "", errors.New("active artifact root changed after Version materialization")
	}
	recheckedDatabase, err := filepath.EvalSymlinks(databasePath)
	if err != nil || !samePath(recheckedDatabase, databasePath) {
		return "", errors.New("Current Store path changed after Version materialization")
	}
	if err := requireLearningMaterializeHandoffDisjointV1(
		recheckedHandoff,
		recheckedArtifactRoot,
		recheckedDatabase,
	); err != nil {
		return "", err
	}
	return recheckedHandoff, nil
}
