package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidLearningMaterialization = errors.New(
		"currentstore: invalid Learning materialization request",
	)
	ErrLearningMaterializationNotApproved = errors.New(
		"currentstore: Learning Proposal is not approved",
	)
	ErrLearningMaterializationConflict = errors.New(
		"currentstore: Learning materialization identity conflict",
	)
	ErrLearningMaterializationIntegrity = errors.New(
		"currentstore: Learning materialization integrity violation",
	)
)

// MaterializeApprovedLearningProposalInput contains only tenant scoping and
// the exact approved review revision fence. The Store derives the Version,
// verdict and artifact identities from authoritative persisted parents.
type MaterializeApprovedLearningProposalInput struct {
	TenantID                 string
	ProposalID               string
	ExpectedProposalRevision uint64
}

// LearningMaterializedVersionRecord is one verified, detached snapshot. It
// grants no install, activation, binding, Catalog or execution authority.
type LearningMaterializedVersionRecord struct {
	Proposal              LearningProposalRecord
	Version               learningcontract.MaterializedVersionV1
	VersionCanonical      []byte
	VersionID             string
	ArtifactDigest        string
	ArtifactSizeBytes     uint64
	ApprovalVerdictDigest string
	MaterializedAt        time.Time
}

type MaterializeApprovedLearningProposalResult struct {
	Record  LearningMaterializedVersionRecord
	Created bool
}

// MaterializeApprovedLearningProposal deterministically freezes one exact
// APPROVED/2 or reconciled APPROVED/3 Proposal. It does not write files or
// call module installation, activation, Catalog, Provider or Gateway APIs.
func (store *Store) MaterializeApprovedLearningProposal(
	ctx context.Context,
	input MaterializeApprovedLearningProposalInput,
) (result MaterializeApprovedLearningProposalResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!moduleapi.ValidSHA256(input.ProposalID) ||
		(input.ExpectedProposalRevision != 2 &&
			input.ExpectedProposalRevision != 3) {
		return result, fmt.Errorf(
			"%w: tenant, ProposalID, or expected revision is invalid",
			ErrInvalidLearningMaterialization,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf(
			"currentstore: acquire Learning materialization connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf(
			"currentstore: begin Learning materialization: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(
				context.Background(),
				`ROLLBACK`,
			)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	proposal, found, err := queryLearningProposalByID(
		ctx,
		connection,
		input.ProposalID,
	)
	if err != nil {
		return result, err
	}
	if !found || proposal.Proposal.TenantID != input.TenantID {
		return result, ErrLearningProposalNotFound
	}
	if proposal.State != LearningProposalApproved ||
		proposal.Revision != input.ExpectedProposalRevision {
		return result, fmt.Errorf(
			"%w: expected exact APPROVED/%d",
			ErrLearningMaterializationNotApproved,
			input.ExpectedProposalRevision,
		)
	}
	if err := verifyLearningReviewProjection(ctx, connection, proposal); err != nil {
		return result, fmt.Errorf(
			"%w: approved Review closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if err != nil || derived.state != LearningProposalApproved ||
		derived.verdict == nil || len(derived.verdictCanonical) == 0 ||
		derived.verdictDigest == "" {
		return result, fmt.Errorf(
			"%w: exact APPROVE verdict is unavailable: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	version, versionCanonical, versionID, err :=
		learningcontract.NewMaterializedVersionV1(
			learningcontract.MaterializedVersionV1{
				SchemaVersion:     learningcontract.MaterializedVersionSchemaVersionV1,
				ProposalID:        proposal.ProposalID,
				ProposalRevision:  proposal.Revision,
				ReviewRunID:       proposal.ReviewRunID,
				ReviewerAttemptID: proposal.ReviewerAttemptID,
			},
			proposal.ProposalCanonical,
			proposal.DraftCanonical,
			derived.verdictCanonical,
		)
	if err != nil {
		return result, fmt.Errorf(
			"%w: derive immutable Version: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}

	existing, materialized, err := queryLearningMaterializedProjection(
		ctx,
		connection,
		proposal,
		derived.verdictCanonical,
	)
	if err != nil {
		return result, err
	}
	if materialized {
		if existing.VersionID != versionID ||
			!bytes.Equal(existing.VersionCanonical, versionCanonical) {
			return result, fmt.Errorf(
				"%w: exact retry derives a different Version",
				ErrLearningMaterializationConflict,
			)
		}
		if err := verifyLearningMaterializedCompatibility(
			ctx,
			connection,
			existing,
		); err != nil {
			return result, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, proposal.ProposalID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf(
				"currentstore: commit Learning materialization retry: %w",
				err,
			)
		}
		committed = true
		return MaterializeApprovedLearningProposalResult{
			Record: detachLearningMaterializedVersionRecord(existing),
		}, nil
	}

	if err := requireLearningMaterializedTargetAvailable(
		ctx,
		connection,
		proposal.Proposal.Target,
		proposal.ProposalID,
	); err != nil {
		return result, err
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		versionCanonical,
		versionID,
		proposal.ProposalCanonical,
		proposal.DraftCanonical,
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: rebuild module artifact: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if err := requireLearningInstallationCompatible(
		ctx,
		connection,
		proposal.Proposal.Target,
		artifact.ManifestCanonical,
		artifact.ArtifactDigest,
	); err != nil {
		return result, err
	}
	if err := requireModuleRefCompatibleWithDiscovery(
		ctx,
		connection,
		proposal.Proposal.Target,
		version.ArtifactDigest,
	); err != nil {
		return result, err
	}

	materializedAtMicros := nowUnixMicro()
	write, err := connection.ExecContext(ctx, `
		UPDATE learning_proposals
		SET version_canonical=?, version_size_bytes=?, version_canonical_digest=?, version_id=?,
			artifact_digest=?, artifact_size_bytes=?,
			approval_verdict_digest=?, materialized_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE proposal_id=? AND tenant_id=? AND state='APPROVED'
			AND revision=? AND version_id IS NULL
	`,
		versionCanonical,
		len(versionCanonical),
		moduleapi.Digest(overviewMaterializedCanonicalDigestDomainV1, versionCanonical),
		versionID,
		version.ArtifactDigest,
		version.ArtifactSizeBytes,
		version.ApproveVerdictDigest,
		materializedAtMicros,
		proposal.ProposalID,
		proposal.Proposal.TenantID,
		proposal.Revision,
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: persist immutable Version: %v",
			ErrLearningMaterializationConflict,
			err,
		)
	}
	affected, err := write.RowsAffected()
	if err != nil || affected != 1 {
		return result, fmt.Errorf(
			"%w: Version CAS affected %d rows: %v",
			ErrLearningMaterializationConflict,
			affected,
			err,
		)
	}
	if err := appendProposalResourceObservationV1(
		ctx, connection, proposal.ProposalID, overviewTransitionProposalMaterializeV1,
	); err != nil {
		return result, err
	}
	stored, materialized, err := queryLearningMaterializedProjection(
		ctx,
		connection,
		proposal,
		derived.verdictCanonical,
	)
	if err != nil || !materialized || stored.VersionID != versionID {
		return result, fmt.Errorf(
			"%w: re-read stored Version: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf(
			"currentstore: commit Learning materialization: %w",
			err,
		)
	}
	committed = true
	return MaterializeApprovedLearningProposalResult{
		Record:  detachLearningMaterializedVersionRecord(stored),
		Created: true,
	}, nil
}

// GetLearningMaterializedVersion returns one tenant-scoped immutable Version
// only after revalidating its Proposal, Review, artifact and installation
// compatibility closure in one read transaction.
func (store *Store) GetLearningMaterializedVersion(
	ctx context.Context,
	tenantID string,
	proposalID string,
) (record LearningMaterializedVersionRecord, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) ||
		!moduleapi.ValidSHA256(proposalID) {
		return record, fmt.Errorf(
			"%w: invalid tenant or ProposalID",
			ErrInvalidLearningMaterialization,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return record, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return record, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return record, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(
				context.Background(),
				`ROLLBACK`,
			)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	proposal, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil {
		return record, err
	}
	if !found || proposal.Proposal.TenantID != tenantID {
		return record, ErrLearningProposalNotFound
	}
	if err := verifyLearningReviewProjection(ctx, connection, proposal); err != nil {
		return record, fmt.Errorf(
			"%w: approved Review closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if err != nil || derived.state != LearningProposalApproved ||
		len(derived.verdictCanonical) == 0 {
		return record, fmt.Errorf(
			"%w: exact APPROVE verdict is unavailable: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	record, materialized, err := queryLearningMaterializedProjection(
		ctx,
		connection,
		proposal,
		derived.verdictCanonical,
	)
	if err != nil {
		return LearningMaterializedVersionRecord{}, err
	}
	if !materialized {
		return LearningMaterializedVersionRecord{}, ErrLearningProposalNotFound
	}
	if err := verifyLearningMaterializedCompatibility(
		ctx,
		connection,
		record,
	); err != nil {
		return LearningMaterializedVersionRecord{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return LearningMaterializedVersionRecord{}, err
	}
	committed = true
	return detachLearningMaterializedVersionRecord(record), nil
}

func queryLearningMaterializedProjection(
	ctx context.Context,
	connection readQueryerV1,
	proposal LearningProposalRecord,
	approveVerdictCanonical []byte,
) (LearningMaterializedVersionRecord, bool, error) {
	var (
		canonical      []byte
		size           sql.NullInt64
		versionID      sql.NullString
		artifactDigest sql.NullString
		artifactSize   sql.NullInt64
		verdictDigest  sql.NullString
		materializedAt sql.NullInt64
	)
	err := connection.QueryRowContext(ctx, `
		SELECT version_canonical, version_size_bytes, version_id,
			artifact_digest, artifact_size_bytes,
			approval_verdict_digest, materialized_at
		FROM learning_proposals WHERE proposal_id=?
	`, proposal.ProposalID).Scan(
		&canonical,
		&size,
		&versionID,
		&artifactDigest,
		&artifactSize,
		&verdictDigest,
		&materializedAt,
	)
	if err != nil {
		return LearningMaterializedVersionRecord{}, false, fmt.Errorf(
			"%w: read Version projection: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	present := canonical != nil || size.Valid || versionID.Valid ||
		artifactDigest.Valid || artifactSize.Valid || verdictDigest.Valid ||
		materializedAt.Valid
	if !present {
		return LearningMaterializedVersionRecord{}, false, nil
	}
	if canonical == nil || !size.Valid || !versionID.Valid ||
		!artifactDigest.Valid || !artifactSize.Valid || !verdictDigest.Valid ||
		!materializedAt.Valid || size.Int64 != int64(len(canonical)) ||
		artifactSize.Int64 <= 0 {
		return LearningMaterializedVersionRecord{}, false, fmt.Errorf(
			"%w: partial or invalid Version SQL projection",
			ErrLearningMaterializationIntegrity,
		)
	}
	version, err := learningcontract.RestoreMaterializedVersionV1(
		canonical,
		versionID.String,
		proposal.ProposalCanonical,
		proposal.DraftCanonical,
		approveVerdictCanonical,
	)
	if err != nil || version.ProposalID != proposal.ProposalID ||
		version.ProposalRevision != proposal.Revision ||
		version.ReviewRunID != proposal.ReviewRunID ||
		version.ReviewerAttemptID != proposal.ReviewerAttemptID ||
		version.ArtifactDigest != artifactDigest.String ||
		version.ArtifactSizeBytes != uint64(artifactSize.Int64) ||
		version.ApproveVerdictDigest != verdictDigest.String {
		return LearningMaterializedVersionRecord{}, false, fmt.Errorf(
			"%w: Version canonical differs from SQL or Review projection: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	when, err := timeFromUnixMicro(materializedAt.Int64)
	if err != nil {
		return LearningMaterializedVersionRecord{}, false, fmt.Errorf(
			"%w: materialized_at: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if when.Before(proposal.UpdatedAt) {
		return LearningMaterializedVersionRecord{}, false, fmt.Errorf(
			"%w: materialized_at predates the Proposal review projection",
			ErrLearningMaterializationIntegrity,
		)
	}
	return LearningMaterializedVersionRecord{
		Proposal:              detachLearningProposalRecord(proposal),
		Version:               version,
		VersionCanonical:      bytes.Clone(canonical),
		VersionID:             versionID.String,
		ArtifactDigest:        artifactDigest.String,
		ArtifactSizeBytes:     uint64(artifactSize.Int64),
		ApprovalVerdictDigest: verdictDigest.String,
		MaterializedAt:        when,
	}, true, nil
}

// learningMaterializedProjectionPresence independently closes the SQL 0/7
// shape even when SQLite CHECK constraints were bypassed by offline tamper.
func learningMaterializedProjectionPresence(
	ctx context.Context,
	connection readQueryerV1,
	proposalID string,
) (bool, error) {
	var (
		canonical      []byte
		size           sql.NullInt64
		versionID      sql.NullString
		artifactDigest sql.NullString
		artifactSize   sql.NullInt64
		verdictDigest  sql.NullString
		materializedAt sql.NullInt64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT version_canonical, version_size_bytes, version_id,
			artifact_digest, artifact_size_bytes,
			approval_verdict_digest, materialized_at
		FROM learning_proposals WHERE proposal_id=?
	`, proposalID).Scan(
		&canonical,
		&size,
		&versionID,
		&artifactDigest,
		&artifactSize,
		&verdictDigest,
		&materializedAt,
	); err != nil {
		return false, err
	}
	presentCount := 0
	for _, present := range []bool{
		canonical != nil,
		size.Valid,
		versionID.Valid,
		artifactDigest.Valid,
		artifactSize.Valid,
		verdictDigest.Valid,
		materializedAt.Valid,
	} {
		if present {
			presentCount++
		}
	}
	switch presentCount {
	case 0:
		return false, nil
	case 7:
		return true, nil
	default:
		return false, fmt.Errorf(
			"%w: partial Version SQL projection has %d of 7 fields",
			ErrLearningMaterializationIntegrity,
			presentCount,
		)
	}
}

func requireLearningMaterializedTargetAvailable(
	ctx context.Context,
	connection readQueryerV1,
	target moduleapi.Ref,
	proposalID string,
) error {
	var existingProposalID string
	err := connection.QueryRowContext(ctx, `
		SELECT proposal_id FROM learning_proposals
		WHERE target_id=? AND target_version=? AND version_id IS NOT NULL
	`, target.ID, target.Version).Scan(&existingProposalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"%w: inspect global target: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if existingProposalID != proposalID {
		return fmt.Errorf(
			"%w: target %s@%s is already materialized",
			ErrLearningMaterializationConflict,
			target.ID,
			target.Version,
		)
	}
	return nil
}

func requireLearningInstallationCompatible(
	ctx context.Context,
	connection readQueryerV1,
	target moduleapi.Ref,
	manifestCanonical []byte,
	artifactDigest string,
) error {
	installation, err := queryModuleInstallationByIdentity(
		ctx,
		connection,
		target.ID,
		target.Version,
	)
	if errors.Is(err, ErrModuleInstallationNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"%w: inspect existing Installation: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if !bytes.Equal(installation.ManifestBytes, manifestCanonical) ||
		installation.ArtifactDigest != artifactDigest {
		return fmt.Errorf(
			"%w: installed target %s@%s differs",
			ErrLearningMaterializationConflict,
			target.ID,
			target.Version,
		)
	}
	return nil
}

// requireInstallationCompatibleWithLearningMaterialization is the symmetric
// InstallModule gate. It runs inside InstallModule's BEGIN IMMEDIATE before
// any ContentRecord or Installation write.
func requireInstallationCompatibleWithLearningMaterialization(
	ctx context.Context,
	connection *sql.Conn,
	target moduleapi.Ref,
	manifestCanonical []byte,
	artifactDigest string,
) error {
	var proposalID string
	err := connection.QueryRowContext(ctx, `
		SELECT proposal_id FROM learning_proposals
		WHERE target_id=? AND target_version=? AND version_id IS NOT NULL
	`, target.ID, target.Version).Scan(&proposalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"%w: inspect materialized target: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	proposal, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil || !found || proposal.State != LearningProposalApproved {
		return fmt.Errorf(
			"%w: materialized target Proposal closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if err := verifyLearningReviewProjection(ctx, connection, proposal); err != nil {
		return fmt.Errorf(
			"%w: materialized target Review closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if err != nil || derived.state != LearningProposalApproved ||
		len(derived.verdictCanonical) == 0 {
		return fmt.Errorf(
			"%w: materialized target APPROVE verdict: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	record, present, err := queryLearningMaterializedProjection(
		ctx,
		connection,
		proposal,
		derived.verdictCanonical,
	)
	if err != nil || !present {
		return fmt.Errorf(
			"%w: materialized target Version closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		record.VersionCanonical,
		record.VersionID,
		record.Proposal.ProposalCanonical,
		record.Proposal.DraftCanonical,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: rebuild materialized target artifact: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if !bytes.Equal(artifact.ManifestCanonical, manifestCanonical) ||
		artifact.ArtifactDigest != artifactDigest {
		return fmt.Errorf(
			"%w: module %s@%s differs from materialized Version",
			ErrModuleConflict,
			target.ID,
			target.Version,
		)
	}
	return nil
}

func verifyLearningMaterializedCompatibility(
	ctx context.Context,
	connection readQueryerV1,
	record LearningMaterializedVersionRecord,
) error {
	if err := requireLearningMaterializedTargetAvailable(
		ctx,
		connection,
		record.Version.Target,
		record.Proposal.ProposalID,
	); err != nil {
		return err
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		record.VersionCanonical,
		record.VersionID,
		record.Proposal.ProposalCanonical,
		record.Proposal.DraftCanonical,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: rebuild materialized artifact: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	return requireLearningInstallationCompatible(
		ctx,
		connection,
		record.Version.Target,
		artifact.ManifestCanonical,
		artifact.ArtifactDigest,
	)
}

func verifyLearningMaterializedProposalClosure(
	ctx context.Context,
	connection readQueryerV1,
	proposal LearningProposalRecord,
) error {
	present, err := learningMaterializedProjectionPresence(
		ctx,
		connection,
		proposal.ProposalID,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: Version projection: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	if !present {
		return nil
	}
	if proposal.State != LearningProposalApproved ||
		(proposal.Revision != 2 && proposal.Revision != 3) {
		return fmt.Errorf(
			"%w: Version exists outside APPROVED/2|3",
			ErrLearningMaterializationIntegrity,
		)
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if err != nil || derived.state != LearningProposalApproved ||
		len(derived.verdictCanonical) == 0 {
		return fmt.Errorf(
			"%w: Version has no exact APPROVE verdict: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	record, materialized, err := queryLearningMaterializedProjection(
		ctx,
		connection,
		proposal,
		derived.verdictCanonical,
	)
	if err != nil || !materialized {
		return fmt.Errorf(
			"%w: Version canonical closure: %v",
			ErrLearningMaterializationIntegrity,
			err,
		)
	}
	return verifyLearningMaterializedCompatibility(ctx, connection, record)
}

func detachLearningMaterializedVersionRecord(
	record LearningMaterializedVersionRecord,
) LearningMaterializedVersionRecord {
	record.Proposal = detachLearningProposalRecord(record.Proposal)
	record.VersionCanonical = bytes.Clone(record.VersionCanonical)
	return record
}
