package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidLearningProposal identifies malformed input before any Store
	// write. Source evidence is admission-time only and is never persisted.
	ErrInvalidLearningProposal = errors.New(
		"currentstore: invalid Learning Proposal request",
	)

	// ErrLearningProposalLineage means the claimed proposer does not close to
	// one exact successful terminal Model result in the Current Store.
	ErrLearningProposalLineage = errors.New(
		"currentstore: Learning Proposal proposer lineage is not proven",
	)

	// ErrLearningProposalIntegrity identifies an impossible or tampered
	// persisted Proposal projection.
	ErrLearningProposalIntegrity = errors.New(
		"currentstore: Learning Proposal integrity violation",
	)

	// ErrLearningProposalNotFound is tenant-scoped so callers cannot use Get as
	// a cross-tenant Proposal oracle.
	ErrLearningProposalNotFound = errors.New(
		"currentstore: Learning Proposal not found",
	)

	// ErrLearningSourceDuplicate preserves the first exact source revision for
	// the tenant and kind. The source remains occupied through future terminal
	// review states; this API exposes no delete or release operation.
	ErrLearningSourceDuplicate = errors.New(
		"currentstore: Learning Proposal source already submitted",
	)

	// ErrLearningContentDuplicate rejects semantic duplicates independently of
	// source identity, including version-only and rename-only candidates.
	ErrLearningContentDuplicate = errors.New(
		"currentstore: Learning Proposal content already submitted",
	)

	// ErrLearningTargetDuplicate prevents two immutable candidate bodies from
	// claiming the same exact target module version.
	ErrLearningTargetDuplicate = errors.New(
		"currentstore: Learning Proposal target version already submitted",
	)
)

// LearningProposalState is the Store-owned W4 review projection. Review
// terminal states never imply publication, installation, activation, or an
// authority grant.
type LearningProposalState string

const (
	LearningProposalSubmitted     LearningProposalState = "SUBMITTED"
	LearningProposalReviewPending LearningProposalState = "REVIEW_PENDING"
	LearningProposalApproved      LearningProposalState = "APPROVED"
	LearningProposalRejected      LearningProposalState = "REJECTED"
	LearningProposalReviewFailed  LearningProposalState = "REVIEW_FAILED"
	LearningProposalReviewUnknown LearningProposalState = "REVIEW_UNKNOWN"
)

// SubmitLearningProposalInput contains the complete in-memory admission
// material shared by Knowledge and static Skill drafts. NewProposalV1 derives
// every persisted source/draft/content identity inside this Store call;
// OriginMaterial and RevisionMaterial never reach SQL.
type SubmitLearningProposalInput struct {
	Proposal       learningcontract.ProposalV1
	DraftCanonical []byte
	SourceEvidence learningcontract.SourceEvidenceV1
}

type SubmitKnowledgeProposalInput = SubmitLearningProposalInput
type SubmitStaticSkillProposalInput = SubmitLearningProposalInput

// LearningProposalRecord is one detached Proposal plus its narrow review
// projection. ProposalCanonical and DraftCanonical remain immutable across
// review transitions. ReviewRunID and ReviewerAttemptID point into the same
// Current Store Run and Model Attempt ledgers; they are not a second review
// ledger.
type LearningProposalRecord struct {
	ProposalID        string
	Proposal          learningcontract.ProposalV1
	ProposalCanonical []byte
	DraftCanonical    []byte
	ProposerAttemptID string
	ReviewRunID       string
	ReviewerAttemptID string
	State             LearningProposalState
	Revision          uint64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// SubmitLearningProposalResult distinguishes the sole creating transaction
// from an exact retry. Neither result grants review, install, activate, Catalog,
// Gateway, or other execution authority.
type SubmitLearningProposalResult struct {
	Record  LearningProposalRecord
	Created bool
}

type SubmitKnowledgeProposalResult = SubmitLearningProposalResult
type SubmitStaticSkillProposalResult = SubmitLearningProposalResult

// SubmitKnowledgeProposal atomically proves one exact successful proposer
// lineage and inserts one Knowledge Proposal. Exact retries return the original
// row. Source, semantic content, and exact target-version conflicts fail
// without writing a candidate row or any Runtime fact.
func (store *Store) SubmitKnowledgeProposal(
	ctx context.Context,
	input SubmitKnowledgeProposalInput,
) (SubmitKnowledgeProposalResult, error) {
	return store.submitLearningProposal(
		ctx,
		input,
		learningcontract.ProposalKindKnowledgeV1,
	)
}

// SubmitStaticSkillProposal applies the same admission, lineage, deduplication,
// and exact-retry rules to one immutable static Skill Draft. It does not install,
// activate, bind, review, publish, or grant authority to the Draft.
func (store *Store) SubmitStaticSkillProposal(
	ctx context.Context,
	input SubmitStaticSkillProposalInput,
) (SubmitStaticSkillProposalResult, error) {
	return store.submitLearningProposal(
		ctx,
		input,
		learningcontract.ProposalKindSkillV1,
	)
}

func (store *Store) submitLearningProposal(
	ctx context.Context,
	input SubmitLearningProposalInput,
	expectedKind learningcontract.ProposalKindV1,
) (result SubmitLearningProposalResult, returnErr error) {
	if ctx == nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidLearningProposal,
		)
	}
	// The immutable Store keeps one bounded draft BLOB for both Proposal kinds.
	// Check all caller-owned byte slices before cloning or parsing them so a
	// contract-valid StaticContext whose JSON envelope exceeds the Store limit
	// fails as an admission error instead of leaking a SQLite CHECK failure.
	if len(input.DraftCanonical) == 0 ||
		len(input.DraftCanonical) > moduleapi.MaxTextBytes {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: draft canonical must contain 1..%d bytes",
			ErrInvalidLearningProposal,
			moduleapi.MaxTextBytes,
		)
	}
	if len(input.SourceEvidence.OriginMaterial) >
		learningcontract.MaxSourceOriginMaterialV1 ||
		len(input.SourceEvidence.RevisionMaterial) >
			learningcontract.MaxSourceRevisionMaterialV1 {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: source evidence exceeds admission limits",
			ErrInvalidLearningProposal,
		)
	}
	draftCanonical := bytes.Clone(input.DraftCanonical)
	sourceEvidence := learningcontract.SourceEvidenceV1{
		Mechanism:        input.SourceEvidence.Mechanism,
		OriginMaterial:   bytes.Clone(input.SourceEvidence.OriginMaterial),
		RevisionMaterial: bytes.Clone(input.SourceEvidence.RevisionMaterial),
	}
	proposal, proposalCanonical, proposalID, err :=
		learningcontract.NewProposalV1(
			input.Proposal,
			draftCanonical,
			sourceEvidence,
		)
	if err != nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidLearningProposal,
			err,
		)
	}
	if proposal.Kind != expectedKind {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: submission kind %q does not match endpoint kind %q",
			ErrInvalidLearningProposal,
			proposal.Kind,
			expectedKind,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return SubmitLearningProposalResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"currentstore: acquire Learning Proposal connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"currentstore: begin Learning Proposal submission: %w",
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

	existing, found, err := queryLearningProposalByID(
		ctx,
		connection,
		proposalID,
	)
	if err != nil {
		return SubmitLearningProposalResult{}, err
	}
	if found {
		if !bytes.Equal(existing.ProposalCanonical, proposalCanonical) ||
			!bytes.Equal(existing.DraftCanonical, draftCanonical) {
			return SubmitLearningProposalResult{}, fmt.Errorf(
				"%w: ProposalID exact retry changed canonical facts",
				ErrLearningProposalIntegrity,
			)
		}
		attemptID, err := proveLearningProposerLineage(
			ctx,
			connection,
			existing.Proposal,
		)
		if err != nil || attemptID != existing.ProposerAttemptID {
			return SubmitLearningProposalResult{}, fmt.Errorf(
				"%w: exact retry lineage: %v",
				ErrLearningProposalIntegrity,
				err,
			)
		}
		if err := verifyLearningReviewProjection(
			ctx,
			connection,
			existing,
		); err != nil {
			return SubmitLearningProposalResult{}, fmt.Errorf(
				"%w: exact retry Review closure: %v",
				ErrLearningProposalIntegrity,
				err,
			)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, proposalID,
		); err != nil {
			return SubmitLearningProposalResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return SubmitLearningProposalResult{}, fmt.Errorf(
				"currentstore: commit Learning Proposal retry: %w",
				err,
			)
		}
		committed = true
		return SubmitLearningProposalResult{
			Record:  detachLearningProposalRecord(existing),
			Created: false,
		}, nil
	}

	proposerAttemptID, err := proveLearningProposerLineage(
		ctx,
		connection,
		proposal,
	)
	if err != nil {
		return SubmitLearningProposalResult{}, err
	}
	if duplicateID, found, err := queryLearningDuplicateID(
		ctx,
		connection,
		`source_fingerprint`,
		proposal.TenantID,
		string(proposal.Kind),
		proposal.SourceFingerprint,
	); err != nil {
		return SubmitLearningProposalResult{}, err
	} else if found {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: existing ProposalID %s",
			ErrLearningSourceDuplicate,
			duplicateID,
		)
	}
	if duplicateID, found, err := queryLearningDuplicateID(
		ctx,
		connection,
		`content_fingerprint`,
		proposal.TenantID,
		string(proposal.Kind),
		proposal.ContentFingerprint,
	); err != nil {
		return SubmitLearningProposalResult{}, err
	} else if found {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: existing ProposalID %s",
			ErrLearningContentDuplicate,
			duplicateID,
		)
	}
	if duplicateID, found, err := queryLearningTargetDuplicateID(
		ctx,
		connection,
		proposal,
	); err != nil {
		return SubmitLearningProposalResult{}, err
	} else if found {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: existing ProposalID %s",
			ErrLearningTargetDuplicate,
			duplicateID,
		)
	}

	createdAt := nowUnixMicro()
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO learning_proposals(
			proposal_id,
			tenant_id,
			workspace_id,
			proposal_kind,
			source_fingerprint,
			content_fingerprint,
			draft_digest,
			target_id,
			target_version,
			proposal_canonical,
			proposal_size_bytes,
			draft_canonical,
			draft_size_bytes,
			proposer_run_id,
			proposer_manifest_digest,
			proposer_member_id,
			proposer_member_digest,
			proposer_attempt_id,
			proposer_result_ref,
			state,
			revision,
			created_at,
			updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
	`,
		proposalID,
		proposal.TenantID,
		proposal.Workspace.ID,
		string(proposal.Kind),
		proposal.SourceFingerprint,
		proposal.ContentFingerprint,
		proposal.DraftDigest,
		proposal.Target.ID,
		proposal.Target.Version,
		proposalCanonical,
		len(proposalCanonical),
		draftCanonical,
		len(draftCanonical),
		proposal.ProposerRunID,
		proposal.ProposerManifestDigest,
		proposal.ProposerMember.MemberID,
		proposal.ProposerMember.Digest,
		proposerAttemptID,
		proposal.ProposerResultRef,
		string(LearningProposalSubmitted),
		createdAt,
		createdAt,
	); err != nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"currentstore: insert Learning Proposal: %w",
			err,
		)
	}
	if err := appendProposalResourceObservationV1(
		ctx, connection, proposalID, overviewTransitionProposalSubmitV1,
	); err != nil {
		return SubmitLearningProposalResult{}, err
	}
	stored, found, err := queryLearningProposalByID(
		ctx,
		connection,
		proposalID,
	)
	if err != nil {
		return SubmitLearningProposalResult{}, err
	}
	if !found || stored.ProposerAttemptID != proposerAttemptID ||
		!bytes.Equal(stored.ProposalCanonical, proposalCanonical) ||
		!bytes.Equal(stored.DraftCanonical, draftCanonical) {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"%w: inserted Proposal did not round-trip",
			ErrLearningProposalIntegrity,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return SubmitLearningProposalResult{}, fmt.Errorf(
			"currentstore: commit Learning Proposal: %w",
			err,
		)
	}
	committed = true
	return SubmitLearningProposalResult{
		Record:  detachLearningProposalRecord(stored),
		Created: true,
	}, nil
}

// GetKnowledgeProposal returns one tenant-scoped Proposal after revalidating
// its canonical projection and proposer lineage. It performs no Runtime work.
func (store *Store) GetKnowledgeProposal(
	ctx context.Context,
	tenantID string,
	proposalID string,
) (record LearningProposalRecord, returnErr error) {
	return store.getLearningProposal(
		ctx,
		tenantID,
		proposalID,
		learningcontract.ProposalKindKnowledgeV1,
	)
}

// GetLearningProposal returns either supported Proposal kind through one
// tenant-scoped read. Kind-specific callers may continue to use the narrower
// Knowledge and static Skill getters.
func (store *Store) GetLearningProposal(
	ctx context.Context,
	tenantID string,
	proposalID string,
) (LearningProposalRecord, error) {
	return store.getLearningProposal(ctx, tenantID, proposalID, "")
}

// GetStaticSkillProposal returns one tenant-scoped static Skill Proposal after
// revalidating its canonical projection and proposer lineage.
func (store *Store) GetStaticSkillProposal(
	ctx context.Context,
	tenantID string,
	proposalID string,
) (LearningProposalRecord, error) {
	return store.getLearningProposal(
		ctx,
		tenantID,
		proposalID,
		learningcontract.ProposalKindSkillV1,
	)
}

func (store *Store) getLearningProposal(
	ctx context.Context,
	tenantID string,
	proposalID string,
	expectedKind learningcontract.ProposalKindV1,
) (record LearningProposalRecord, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) ||
		!moduleapi.ValidSHA256(proposalID) {
		return LearningProposalRecord{}, fmt.Errorf(
			"%w: invalid tenant or ProposalID",
			ErrInvalidLearningProposal,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return LearningProposalRecord{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"currentstore: acquire Learning Proposal read connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"currentstore: begin Learning Proposal read: %w",
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
	record, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil {
		return LearningProposalRecord{}, err
	}
	if !found || record.Proposal.TenantID != tenantID ||
		(expectedKind != "" && record.Proposal.Kind != expectedKind) {
		return LearningProposalRecord{}, ErrLearningProposalNotFound
	}
	attemptID, err := proveLearningProposerLineage(ctx, connection, record.Proposal)
	if err != nil || attemptID != record.ProposerAttemptID {
		return LearningProposalRecord{}, fmt.Errorf(
			"%w: stored proposer lineage: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if err := verifyLearningReviewProjection(
		ctx,
		connection,
		record,
	); err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"%w: stored Review closure: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if err := verifyLearningMaterializedProposalClosure(
		ctx,
		connection,
		record,
	); err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"%w: stored Version closure: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"currentstore: commit Learning Proposal read: %w",
			err,
		)
	}
	committed = true
	return detachLearningProposalRecord(record), nil
}

func detachLearningProposalRecord(
	record LearningProposalRecord,
) LearningProposalRecord {
	record.ProposalCanonical = bytes.Clone(record.ProposalCanonical)
	record.DraftCanonical = bytes.Clone(record.DraftCanonical)
	return record
}

func queryLearningProposalByID(
	ctx context.Context,
	connection readQueryerV1,
	proposalID string,
) (LearningProposalRecord, bool, error) {
	row := connection.QueryRowContext(ctx, `
		SELECT
			proposal_id,
			tenant_id,
			workspace_id,
			proposal_kind,
			source_fingerprint,
			content_fingerprint,
			draft_digest,
			target_id,
			target_version,
			proposal_canonical,
			proposal_size_bytes,
			draft_canonical,
			draft_size_bytes,
			proposer_run_id,
			proposer_manifest_digest,
			proposer_member_id,
			proposer_member_digest,
			proposer_attempt_id,
			proposer_result_ref,
			review_run_id,
			reviewer_attempt_id,
			state,
			revision,
			created_at,
			updated_at
		FROM learning_proposals
		WHERE proposal_id=?
	`, proposalID)
	record, err := scanLearningProposalRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LearningProposalRecord{}, false, nil
	}
	if err != nil {
		return LearningProposalRecord{}, false, fmt.Errorf(
			"%w: load Proposal %s: %v",
			ErrLearningProposalIntegrity,
			proposalID,
			err,
		)
	}
	return record, true, nil
}

type learningProposalScanner interface {
	Scan(...any) error
}

func validLearningProposalReviewProjection(
	state LearningProposalState,
	revision int64,
	reviewRunID sql.NullString,
	reviewerAttemptID sql.NullString,
	createdAtMicros int64,
	updatedAtMicros int64,
) bool {
	if createdAtMicros <= 0 || updatedAtMicros < createdAtMicros ||
		revision < 0 || revision > 3 ||
		(reviewRunID.Valid && !validLeaseOpaqueID(reviewRunID.String)) ||
		(reviewerAttemptID.Valid &&
			!validLeaseOpaqueID(reviewerAttemptID.String)) {
		return false
	}
	switch state {
	case LearningProposalSubmitted:
		return revision == 0 && !reviewRunID.Valid &&
			!reviewerAttemptID.Valid && updatedAtMicros == createdAtMicros
	case LearningProposalReviewPending:
		return revision == 1 && reviewRunID.Valid &&
			!reviewerAttemptID.Valid
	case LearningProposalReviewUnknown:
		return revision == 2 && reviewRunID.Valid &&
			reviewerAttemptID.Valid
	case LearningProposalApproved,
		LearningProposalRejected,
		LearningProposalReviewFailed:
		return (revision == 2 || revision == 3) && reviewRunID.Valid &&
			reviewerAttemptID.Valid
	default:
		return false
	}
}

func scanLearningProposalRecord(
	scanner learningProposalScanner,
) (LearningProposalRecord, error) {
	var (
		proposalID             string
		tenantID               string
		workspaceID            string
		proposalKind           string
		sourceFingerprint      string
		contentFingerprint     string
		draftDigest            string
		targetID               string
		targetVersion          string
		proposalCanonical      []byte
		proposalSize           int64
		draftCanonical         []byte
		draftSize              int64
		proposerRunID          string
		proposerManifestDigest string
		proposerMemberID       string
		proposerMemberDigest   string
		proposerAttemptID      string
		proposerResultRef      string
		reviewRunID            sql.NullString
		reviewerAttemptID      sql.NullString
		state                  string
		revision               int64
		createdAtMicros        int64
		updatedAtMicros        int64
	)
	if err := scanner.Scan(
		&proposalID,
		&tenantID,
		&workspaceID,
		&proposalKind,
		&sourceFingerprint,
		&contentFingerprint,
		&draftDigest,
		&targetID,
		&targetVersion,
		&proposalCanonical,
		&proposalSize,
		&draftCanonical,
		&draftSize,
		&proposerRunID,
		&proposerManifestDigest,
		&proposerMemberID,
		&proposerMemberDigest,
		&proposerAttemptID,
		&proposerResultRef,
		&reviewRunID,
		&reviewerAttemptID,
		&state,
		&revision,
		&createdAtMicros,
		&updatedAtMicros,
	); err != nil {
		return LearningProposalRecord{}, err
	}
	if proposalSize != int64(len(proposalCanonical)) ||
		draftSize != int64(len(draftCanonical)) ||
		!validLearningProposalReviewProjection(
			LearningProposalState(state),
			revision,
			reviewRunID,
			reviewerAttemptID,
			createdAtMicros,
			updatedAtMicros,
		) ||
		!validLeaseOpaqueID(proposerAttemptID) {
		return LearningProposalRecord{}, fmt.Errorf(
			"Learning Proposal row projection is invalid",
		)
	}
	proposal, err := learningcontract.RestoreProposalV1(
		proposalCanonical,
		draftCanonical,
		proposalID,
	)
	if err != nil {
		return LearningProposalRecord{}, fmt.Errorf(
			"restore canonical Learning Proposal: %w",
			err,
		)
	}
	if (proposal.Kind != learningcontract.ProposalKindKnowledgeV1 &&
		proposal.Kind != learningcontract.ProposalKindSkillV1) ||
		proposalKind != string(proposal.Kind) ||
		tenantID != proposal.TenantID || workspaceID != proposal.Workspace.ID ||
		sourceFingerprint != proposal.SourceFingerprint ||
		contentFingerprint != proposal.ContentFingerprint ||
		draftDigest != proposal.DraftDigest ||
		targetID != proposal.Target.ID ||
		targetVersion != proposal.Target.Version ||
		proposerRunID != proposal.ProposerRunID ||
		proposerManifestDigest != proposal.ProposerManifestDigest ||
		proposerMemberID != proposal.ProposerMember.MemberID ||
		proposerMemberDigest != proposal.ProposerMember.Digest ||
		proposerResultRef != proposal.ProposerResultRef {
		return LearningProposalRecord{}, fmt.Errorf(
			"Learning Proposal SQL projection differs from its canonical wire",
		)
	}
	createdAt, err := timeFromUnixMicro(createdAtMicros)
	if err != nil {
		return LearningProposalRecord{}, err
	}
	updatedAt, err := timeFromUnixMicro(updatedAtMicros)
	if err != nil {
		return LearningProposalRecord{}, err
	}
	return LearningProposalRecord{
		ProposalID:        proposalID,
		Proposal:          proposal,
		ProposalCanonical: bytes.Clone(proposalCanonical),
		DraftCanonical:    bytes.Clone(draftCanonical),
		ProposerAttemptID: proposerAttemptID,
		ReviewRunID:       reviewRunID.String,
		ReviewerAttemptID: reviewerAttemptID.String,
		State:             LearningProposalState(state),
		Revision:          uint64(revision),
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, nil
}

func queryLearningDuplicateID(
	ctx context.Context,
	connection readQueryerV1,
	axis string,
	tenantID string,
	proposalKind string,
	fingerprint string,
) (string, bool, error) {
	var query string
	switch axis {
	case "source_fingerprint":
		query = `
			SELECT proposal_id
			FROM learning_proposals
			WHERE tenant_id=? AND proposal_kind=? AND source_fingerprint=?
		`
	case "content_fingerprint":
		query = `
			SELECT proposal_id
			FROM learning_proposals
			WHERE tenant_id=? AND proposal_kind=? AND content_fingerprint=?
		`
	default:
		return "", false, fmt.Errorf(
			"%w: unsupported duplicate axis %q",
			ErrLearningProposalIntegrity,
			axis,
		)
	}
	var proposalID string
	err := connection.QueryRowContext(
		ctx,
		query,
		tenantID,
		proposalKind,
		fingerprint,
	).Scan(&proposalID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf(
			"currentstore: inspect Learning Proposal %s duplicate: %w",
			axis,
			err,
		)
	}
	if !moduleapi.ValidSHA256(proposalID) {
		return "", false, fmt.Errorf(
			"%w: duplicate axis returned an invalid ProposalID",
			ErrLearningProposalIntegrity,
		)
	}
	return proposalID, true, nil
}

func queryLearningTargetDuplicateID(
	ctx context.Context,
	connection readQueryerV1,
	proposal learningcontract.ProposalV1,
) (string, bool, error) {
	var proposalID string
	err := connection.QueryRowContext(ctx, `
		SELECT proposal_id
		FROM learning_proposals
		WHERE tenant_id=? AND proposal_kind=? AND target_id=? AND target_version=?
	`,
		proposal.TenantID,
		string(proposal.Kind),
		proposal.Target.ID,
		proposal.Target.Version,
	).Scan(&proposalID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf(
			"currentstore: inspect Learning Proposal target duplicate: %w",
			err,
		)
	}
	if !moduleapi.ValidSHA256(proposalID) {
		return "", false, fmt.Errorf(
			"%w: target duplicate returned an invalid ProposalID",
			ErrLearningProposalIntegrity,
		)
	}
	return proposalID, true, nil
}

func proveLearningProposerLineage(
	ctx context.Context,
	connection readQueryerV1,
	proposal learningcontract.ProposalV1,
) (string, error) {
	var (
		runTenantID       string
		runWorkspaceID    string
		manifestCanonical []byte
		manifestDigest    string
		memberCanonical   []byte
		memberDigest      string
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			r.tenant_id,
			r.workspace_id,
			manifest.canonical_json,
			manifest.digest,
			member.canonical_json,
			member.digest
		FROM runs AS r
		JOIN run_manifests AS manifest
		  ON manifest.run_id=r.run_id
		JOIN member_execution_snapshots AS member
		  ON member.run_id=r.run_id
		 AND member.member_id=?
		WHERE r.run_id=?
	`,
		proposal.ProposerMember.MemberID,
		proposal.ProposerRunID,
	).Scan(
		&runTenantID,
		&runWorkspaceID,
		&manifestCanonical,
		&manifestDigest,
		&memberCanonical,
		&memberDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf(
			"%w: Run, Manifest, or Member is absent",
			ErrLearningProposalLineage,
		)
	}
	if err != nil {
		return "", fmt.Errorf(
			"currentstore: read Learning proposer lineage: %w",
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		return "", fmt.Errorf(
			"%w: restore proposer Run Manifest: %v",
			ErrLearningProposalLineage,
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		return "", fmt.Errorf(
			"%w: restore proposer Member snapshot: %v",
			ErrLearningProposalLineage,
			err,
		)
	}
	if runTenantID != proposal.TenantID ||
		runWorkspaceID != proposal.Workspace.ID ||
		manifestDigest != proposal.ProposerManifestDigest ||
		manifest.ManifestDigest != proposal.ProposerManifestDigest ||
		manifest.RunID != proposal.ProposerRunID ||
		manifest.TenantID != proposal.TenantID ||
		manifest.Workspace != proposal.Workspace ||
		manifest.PrimaryAgent != proposal.ProposerAgent ||
		manifest.PrimaryMemberID != proposal.ProposerMember.MemberID ||
		len(manifest.Members) != 1 ||
		manifest.Members[0] != proposal.ProposerMember ||
		memberDigest != proposal.ProposerMember.Digest ||
		member.MemberSnapshotDigest != proposal.ProposerMember.Digest ||
		member.MemberID != proposal.ProposerMember.MemberID ||
		member.Agent != proposal.ProposerAgent ||
		member.Profile != proposal.ProposerProfile ||
		member.Workspace != proposal.Workspace {
		return "", fmt.Errorf(
			"%w: Proposal references do not equal the frozen Run and Member",
			ErrLearningProposalLineage,
		)
	}

	terminal, err := loadTerminalRunResult(
		ctx,
		connection,
		proposal.ProposerRunID,
	)
	if err != nil {
		return "", fmt.Errorf(
			"%w: proposer Run is not a valid terminal result: %v",
			ErrLearningProposalLineage,
			err,
		)
	}
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.ModelState != corecontract.ModelAttemptSucceeded ||
		terminal.MemberID != proposal.ProposerMember.MemberID ||
		terminal.AttemptID == "" {
		return "", fmt.Errorf(
			"%w: proposer terminal result is not a successful Model result",
			ErrLearningProposalLineage,
		)
	}
	attemptRecord, err := queryModelDispatchRecord(ctx, connection, terminal.AttemptID)
	if err != nil {
		return "", fmt.Errorf(
			"%w: load terminal Model Attempt: %v",
			ErrLearningProposalLineage,
			err,
		)
	}
	attempt := attemptRecord.Attempt
	if attempt.RunID != proposal.ProposerRunID ||
		attempt.MemberID != proposal.ProposerMember.MemberID ||
		attempt.MemberSnapshotDigest != proposal.ProposerMember.Digest ||
		attempt.State != corecontract.ModelAttemptSucceeded ||
		attempt.ResultRef != proposal.ProposerResultRef {
		return "", fmt.Errorf(
			"%w: terminal Model Attempt differs from Proposal ResultRef",
			ErrLearningProposalLineage,
		)
	}
	result, err := queryContent(ctx, connection, attempt.ResultRef)
	if err != nil || result.Kind != ContentModelResult ||
		result.Digest != proposal.ProposerResultRef {
		return "", fmt.Errorf(
			"%w: proposer ResultRef is not exact MODEL_RESULT content: %v",
			ErrLearningProposalLineage,
			err,
		)
	}
	return attempt.AttemptID, nil
}

// VerifyLearningProposalSemanticClosureV1 validates every W4-L2 Proposal in
// one caller-owned read transaction. Current Backup uses this exact verifier;
// it performs no model, module, network, Secret, or external-effect call.
func VerifyLearningProposalSemanticClosureV1(
	ctx context.Context,
	connection *sql.Conn,
) error {
	if ctx == nil || connection == nil {
		return fmt.Errorf(
			"%w: Learning semantic verifier requires context and connection",
			ErrLearningProposalIntegrity,
		)
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT proposal_id
		FROM learning_proposals
		ORDER BY proposal_id
	`)
	if err != nil {
		return fmt.Errorf(
			"%w: enumerate Learning Proposals: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	var proposalIDs []string
	for rows.Next() {
		var proposalID string
		if err := rows.Scan(&proposalID); err != nil {
			_ = rows.Close()
			return fmt.Errorf(
				"%w: scan Learning Proposal identity: %v",
				ErrLearningProposalIntegrity,
				err,
			)
		}
		if !moduleapi.ValidSHA256(proposalID) {
			_ = rows.Close()
			return fmt.Errorf(
				"%w: invalid persisted ProposalID",
				ErrLearningProposalIntegrity,
			)
		}
		proposalIDs = append(proposalIDs, proposalID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf(
			"%w: iterate Learning Proposals: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf(
			"%w: close Learning Proposal rows: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	for _, proposalID := range proposalIDs {
		record, found, err := queryLearningProposalByID(
			ctx,
			connection,
			proposalID,
		)
		if err != nil || !found {
			return fmt.Errorf(
				"%w: load Proposal %s: %v",
				ErrLearningProposalIntegrity,
				proposalID,
				err,
			)
		}
		attemptID, err := proveLearningProposerLineage(
			ctx,
			connection,
			record.Proposal,
		)
		if err != nil || attemptID != record.ProposerAttemptID {
			return fmt.Errorf(
				"%w: Proposal %s lineage: %v",
				ErrLearningProposalIntegrity,
				proposalID,
				err,
			)
		}
		if err := verifyLearningReviewProjection(
			ctx,
			connection,
			record,
		); err != nil {
			return fmt.Errorf(
				"%w: Proposal %s Review closure: %v",
				ErrLearningProposalIntegrity,
				proposalID,
				err,
			)
		}
		if err := verifyLearningMaterializedProposalClosure(
			ctx,
			connection,
			record,
		); err != nil {
			return fmt.Errorf(
				"%w: Proposal %s materialized closure: %v",
				ErrLearningProposalIntegrity,
				proposalID,
				err,
			)
		}
	}
	return nil
}
