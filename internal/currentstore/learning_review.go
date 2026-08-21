package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidLearningReview = errors.New(
		"currentstore: invalid Learning Review request",
	)
	ErrLearningReviewConflict = errors.New(
		"currentstore: Learning Review state conflict",
	)
	ErrLearningReviewNotReady = errors.New(
		"currentstore: Learning Review outcome is not ready",
	)
	ErrLearningReviewLineage = errors.New(
		"currentstore: Learning Reviewer lineage is not proven",
	)
)

// CommitLearningReviewAdmissionInput atomically publishes one ordinary,
// model-only Reviewer Run and binds it to a SUBMITTED Proposal. The caller
// cannot supply a Reviewer Attempt or state transition.
type CommitLearningReviewAdmissionInput struct {
	TenantID                 string
	ProposalID               string
	ExpectedProposalRevision uint64
	Run                      CommitRunAdmissionInput
}

// CommitLearningReviewAdmissionResult returns the exact Run and Proposal
// facts committed by the same transaction. Created is true only for the call
// that published the Run and advanced SUBMITTED/0 to REVIEW_PENDING/1.
type CommitLearningReviewAdmissionResult struct {
	Run      RunAdmissionResult
	Proposal LearningProposalRecord
	Created  bool
}

// FinalizeLearningReviewInput carries only Proposal fencing. The Store finds
// the sole ordinary Model Attempt from the frozen Reviewer Run; callers cannot
// substitute an Attempt, result, verdict, or terminal state.
type FinalizeLearningReviewInput struct {
	TenantID                 string
	ProposalID               string
	ExpectedProposalRevision uint64
}

type FinalizeLearningReviewResult struct {
	Proposal LearningProposalRecord
	Applied  bool
}

type learningReviewerClosure struct {
	manifest corecontract.RunManifest
	member   corecontract.MemberExecutionSnapshot
	request  learningcontract.ReviewRequestV1
}

type derivedLearningReview struct {
	state            LearningProposalState
	attempt          ModelDispatchRecord
	verdict          *learningcontract.ReviewVerdictV1
	verdictCanonical []byte
	verdictDigest    string
}

func (store *Store) CommitLearningReviewAdmission(
	ctx context.Context,
	input CommitLearningReviewAdmissionInput,
) (result CommitLearningReviewAdmissionResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!moduleapi.ValidSHA256(input.ProposalID) ||
		input.ExpectedProposalRevision != 0 {
		return result, fmt.Errorf(
			"%w: tenant, ProposalID, or expected revision is invalid",
			ErrInvalidLearningReview,
		)
	}
	prepared, err := prepareCompleteRunAdmission(input.Run)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidLearningReview, err)
	}
	if prepared.manifest.Composite != nil ||
		prepared.manifest.ConversationTurn != nil ||
		prepared.manifest.ParentRunID != "" ||
		prepared.intent.ChannelEndpointID != "" {
		return result, fmt.Errorf(
			"%w: Reviewer must be an ordinary non-Conversation, non-Composite, non-Channel Run",
			ErrInvalidLearningReview,
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
			"currentstore: acquire Learning Review Admission connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf(
			"currentstore: begin Learning Review Admission: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	proposal, found, err := queryLearningProposalByID(ctx, connection, input.ProposalID)
	if err != nil {
		return result, err
	}
	if !found || proposal.Proposal.TenantID != input.TenantID {
		return result, ErrLearningProposalNotFound
	}
	if attemptID, lineageErr := proveLearningProposerLineage(
		ctx,
		connection,
		proposal.Proposal,
	); lineageErr != nil || attemptID != proposal.ProposerAttemptID {
		return result, fmt.Errorf(
			"%w: proposer lineage changed: %v",
			ErrLearningProposalIntegrity,
			lineageErr,
		)
	}

	existingRun, runFound, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		prepared.intent.TenantID,
		prepared.intent.AdmissionKey,
		prepared.input.IntentDigest,
	)
	if err != nil {
		return result, err
	}
	if proposal.State != LearningProposalSubmitted {
		if proposal.ReviewRunID != prepared.manifest.RunID || !runFound ||
			existingRun.RunID != prepared.manifest.RunID ||
			existingRun.ManifestDigest != prepared.manifest.ManifestDigest ||
			existingRun.MemberSnapshotDigest !=
				prepared.member.MemberSnapshotDigest {
			return result, fmt.Errorf(
				"%w: Proposal is already bound to another Reviewer Run",
				ErrLearningReviewConflict,
			)
		}
		if err := verifyLearningReviewProjection(
			ctx,
			connection,
			proposal,
		); err != nil {
			return result, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, input.ProposalID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf(
				"currentstore: commit idempotent Learning Review Admission: %w",
				err,
			)
		}
		committed = true
		existingRun.Created = false
		return CommitLearningReviewAdmissionResult{
			Run: existingRun, Proposal: detachLearningProposalRecord(proposal),
		}, nil
	}
	if proposal.Revision != input.ExpectedProposalRevision ||
		proposal.ReviewRunID != "" || proposal.ReviewerAttemptID != "" {
		return result, fmt.Errorf(
			"%w: Proposal is not exact SUBMITTED/0",
			ErrLearningReviewConflict,
		)
	}
	if runFound {
		return result, fmt.Errorf(
			"%w: Reviewer Run exists without the atomic Proposal binding",
			ErrLearningProposalIntegrity,
		)
	}
	if err := verifyProspectiveLearningReviewer(
		ctx,
		connection,
		proposal,
		prepared,
	); err != nil {
		return result, err
	}

	transitionAt := learningReviewTransitionTime(proposal.UpdatedAt.UnixMicro())
	runResult, err := publishPreparedRunAdmission(
		ctx,
		connection,
		prepared,
		transitionAt,
	)
	if err != nil {
		return result, err
	}
	updated, err := connection.ExecContext(ctx, `
		UPDATE learning_proposals
		SET state=?, revision=1, review_run_id=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE proposal_id=? AND tenant_id=?
		  AND state=? AND revision=0
		  AND review_run_id IS NULL AND reviewer_attempt_id IS NULL
	`,
		string(LearningProposalReviewPending),
		prepared.manifest.RunID,
		transitionAt,
		proposal.ProposalID,
		proposal.Proposal.TenantID,
		string(LearningProposalSubmitted),
	)
	if err != nil {
		return result, fmt.Errorf(
			"currentstore: bind Learning Reviewer Run: %w",
			err,
		)
	}
	if err := requireLearningReviewCASRow(updated, "bind Reviewer Run"); err != nil {
		return result, err
	}
	if err := appendProposalResourceObservationV1(
		ctx, connection, proposal.ProposalID, overviewTransitionProposalAdmissionV1,
	); err != nil {
		return result, err
	}
	stored, found, err := queryLearningProposalByID(ctx, connection, proposal.ProposalID)
	if err != nil || !found || stored.State != LearningProposalReviewPending ||
		stored.Revision != 1 || stored.ReviewRunID != prepared.manifest.RunID {
		return result, fmt.Errorf(
			"%w: bound Proposal did not round-trip: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf(
			"currentstore: commit Learning Review Admission: %w",
			err,
		)
	}
	committed = true
	runResult.Created = true
	return CommitLearningReviewAdmissionResult{
		Run: runResult, Proposal: detachLearningProposalRecord(stored), Created: true,
	}, nil
}

func (store *Store) FinalizeLearningReview(
	ctx context.Context,
	input FinalizeLearningReviewInput,
) (result FinalizeLearningReviewResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!moduleapi.ValidSHA256(input.ProposalID) ||
		input.ExpectedProposalRevision > 3 {
		return result, fmt.Errorf(
			"%w: tenant, ProposalID, or expected revision is invalid",
			ErrInvalidLearningReview,
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
			"currentstore: acquire Learning Review finalization connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf(
			"currentstore: begin Learning Review finalization: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	proposal, found, err := queryLearningProposalByID(ctx, connection, input.ProposalID)
	if err != nil {
		return result, err
	}
	if !found || proposal.Proposal.TenantID != input.TenantID {
		return result, ErrLearningProposalNotFound
	}
	if proposal.State == LearningProposalSubmitted {
		return result, ErrLearningReviewNotReady
	}
	if _, err := loadLearningReviewerClosure(ctx, connection, proposal); err != nil {
		return result, err
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if err != nil {
		return result, err
	}

	if proposal.State == derived.state && proposal.ReviewerAttemptID ==
		derived.attempt.Attempt.AttemptID {
		if input.ExpectedProposalRevision != proposal.Revision &&
			input.ExpectedProposalRevision+1 != proposal.Revision {
			return result, fmt.Errorf(
				"%w: idempotent expected revision %d does not close to %d",
				ErrLearningReviewConflict,
				input.ExpectedProposalRevision,
				proposal.Revision,
			)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, input.ProposalID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf(
				"currentstore: commit idempotent Learning Review finalization: %w",
				err,
			)
		}
		committed = true
		return FinalizeLearningReviewResult{
			Proposal: detachLearningProposalRecord(proposal),
		}, nil
	}

	var nextRevision uint64
	switch proposal.State {
	case LearningProposalReviewPending:
		if proposal.Revision != 1 || input.ExpectedProposalRevision != 1 ||
			proposal.ReviewerAttemptID != "" {
			return result, fmt.Errorf(
				"%w: Proposal is not exact REVIEW_PENDING/1",
				ErrLearningReviewConflict,
			)
		}
		nextRevision = 2
	case LearningProposalReviewUnknown:
		if proposal.Revision != 2 || input.ExpectedProposalRevision != 2 ||
			proposal.ReviewerAttemptID != derived.attempt.Attempt.AttemptID ||
			derived.state == LearningProposalReviewUnknown ||
			derived.attempt.Attempt.ReconciliationEvidenceRef == "" {
			return result, fmt.Errorf(
				"%w: REVIEW_UNKNOWN can advance only from evidence on the same Attempt",
				ErrLearningReviewConflict,
			)
		}
		nextRevision = 3
	default:
		return result, fmt.Errorf(
			"%w: terminal Proposal differs from its authoritative Attempt",
			ErrLearningProposalIntegrity,
		)
	}

	transitionAt := learningReviewTransitionTime(proposal.UpdatedAt.UnixMicro())
	updated, err := connection.ExecContext(ctx, `
		UPDATE learning_proposals
		SET state=?, revision=?, reviewer_attempt_id=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE proposal_id=? AND tenant_id=? AND review_run_id=?
		  AND state=? AND revision=?
		  AND (
		    (?=1 AND reviewer_attempt_id IS NULL)
		    OR (?=2 AND reviewer_attempt_id=?)
		  )
	`,
		string(derived.state),
		int64(nextRevision),
		derived.attempt.Attempt.AttemptID,
		transitionAt,
		proposal.ProposalID,
		proposal.Proposal.TenantID,
		proposal.ReviewRunID,
		string(proposal.State),
		int64(proposal.Revision),
		int64(proposal.Revision),
		int64(proposal.Revision),
		derived.attempt.Attempt.AttemptID,
	)
	if err != nil {
		return result, fmt.Errorf(
			"currentstore: finalize Learning Review: %w",
			err,
		)
	}
	if err := requireLearningReviewCASRow(updated, "finalize review"); err != nil {
		return result, err
	}
	if err := appendProposalResourceObservationV1(
		ctx, connection, proposal.ProposalID, overviewTransitionProposalFinalizeV1,
	); err != nil {
		return result, err
	}
	stored, found, err := queryLearningProposalByID(ctx, connection, proposal.ProposalID)
	if err != nil || !found || stored.State != derived.state ||
		stored.Revision != nextRevision ||
		stored.ReviewerAttemptID != derived.attempt.Attempt.AttemptID {
		return result, fmt.Errorf(
			"%w: finalized Proposal did not round-trip: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf(
			"currentstore: commit Learning Review finalization: %w",
			err,
		)
	}
	committed = true
	return FinalizeLearningReviewResult{
		Proposal: detachLearningProposalRecord(stored), Applied: true,
	}, nil
}

func verifyProspectiveLearningReviewer(
	ctx context.Context,
	connection *sql.Conn,
	proposal LearningProposalRecord,
	prepared preparedRunAdmission,
) error {
	task, err := prospectiveAdmissionContent(
		ctx,
		connection,
		prepared.contents,
		prepared.manifest.TaskInputRef,
	)
	if err != nil {
		return err
	}
	if task.Kind != ContentTaskInput || task.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Reviewer TASK_INPUT has the wrong content kind or media type",
			ErrInvalidLearningReview,
		)
	}
	taskValue, err := corecontract.RestoreTaskInputV1(task.CanonicalBytes)
	if err != nil {
		return fmt.Errorf(
			"%w: Reviewer TASK_INPUT is not exact task-input/v1: %v",
			ErrInvalidLearningReview,
			err,
		)
	}
	request, requestCanonical, _, err :=
		learningcontract.ParseReviewRequestV1([]byte(taskValue.Text))
	if err != nil || !bytes.Equal(requestCanonical, []byte(taskValue.Text)) {
		return fmt.Errorf(
			"%w: Reviewer TASK_INPUT text is not exact learning-review-request/v1: %v",
			ErrInvalidLearningReview,
			err,
		)
	}
	if len(prepared.member.PortPlans) != 1 ||
		len(prepared.member.PortPlans[0].Bindings) != 1 {
		return fmt.Errorf(
			"%w: Reviewer must contain exactly one model Binding",
			ErrInvalidLearningReview,
		)
	}
	modelConfig, err := prospectiveAdmissionContent(
		ctx,
		connection,
		prepared.contents,
		prepared.member.PortPlans[0].Bindings[0].ConfigRef,
	)
	if err != nil {
		return err
	}
	if modelConfig.Kind != ContentConfig ||
		modelConfig.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Reviewer model config has the wrong content kind or media type",
			ErrInvalidLearningReview,
		)
	}
	config, err := moduleapi.RestoreModelBindingConfigV1(modelConfig.CanonicalBytes)
	if err != nil {
		return fmt.Errorf("%w: Reviewer model config: %v", ErrInvalidLearningReview, err)
	}
	return validateLearningReviewerFacts(
		proposal,
		prepared.manifest,
		prepared.member,
		request,
		config,
	)
}

func loadLearningReviewerClosure(
	ctx context.Context,
	connection readQueryerV1,
	proposal LearningProposalRecord,
) (learningReviewerClosure, error) {
	if !validLeaseOpaqueID(proposal.ReviewRunID) {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Proposal has no exact Reviewer Run",
			ErrLearningReviewLineage,
		)
	}
	var (
		runTenantID, runWorkspaceID             string
		conversationID, predecessorRunID        sql.NullString
		conversationTurnIndex                   sql.NullInt64
		parentRunID, parentManifest, parentSlot sql.NullString
		manifestCanonical                       []byte
		memberCanonical                         []byte
		memberCount                             int
		channelCount                            int
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT tenant_id, workspace_id,
		       conversation_id, conversation_turn_index,
		       conversation_predecessor_run_id,
		       parent_run_id, parent_manifest_digest, parent_slot_id
		FROM runs WHERE run_id=?
	`, proposal.ReviewRunID).Scan(
		&runTenantID,
		&runWorkspaceID,
		&conversationID,
		&conversationTurnIndex,
		&predecessorRunID,
		&parentRunID,
		&parentManifest,
		&parentSlot,
	); err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: load Reviewer Run: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json FROM run_manifests WHERE run_id=?
	`, proposal.ReviewRunID).Scan(&manifestCanonical); err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: load Reviewer Manifest: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: restore Reviewer Manifest: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(canonical_json)
		FROM member_execution_snapshots WHERE run_id=?
	`, proposal.ReviewRunID).Scan(&memberCount, &memberCanonical); err != nil ||
		memberCount != 1 {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Reviewer member cardinality: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || manifest.ValidateAgainstMember(member) != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: restore Reviewer Member: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	task, err := queryContent(ctx, connection, manifest.TaskInputRef)
	if err != nil || task.Kind != ContentTaskInput ||
		task.MediaType != admissionJSONMediaType {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Reviewer TASK_INPUT: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	taskValue, err := corecontract.RestoreTaskInputV1(task.CanonicalBytes)
	if err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: restore exact task-input/v1: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	request, requestCanonical, _, err :=
		learningcontract.ParseReviewRequestV1([]byte(taskValue.Text))
	if err != nil || !bytes.Equal(requestCanonical, []byte(taskValue.Text)) {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: restore exact ReviewRequest: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	if len(member.PortPlans) != 1 || len(member.PortPlans[0].Bindings) != 1 {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Reviewer is not model-only",
			ErrLearningReviewLineage,
		)
	}
	configContent, err := queryContent(
		ctx,
		connection,
		member.PortPlans[0].Bindings[0].ConfigRef,
	)
	if err != nil || configContent.Kind != ContentConfig ||
		configContent.MediaType != admissionJSONMediaType {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Reviewer model config content: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	config, err := moduleapi.RestoreModelBindingConfigV1(configContent.CanonicalBytes)
	if err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: restore Reviewer model config: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM channel_ingress_receipts WHERE run_id=?
	`, proposal.ReviewRunID).Scan(&channelCount); err != nil {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: inspect Reviewer Channel lineage: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	if runTenantID != manifest.TenantID ||
		runWorkspaceID != manifest.Workspace.ID ||
		conversationID.Valid || conversationTurnIndex.Valid ||
		predecessorRunID.Valid ||
		parentRunID.Valid || parentManifest.Valid || parentSlot.Valid ||
		channelCount != 0 {
		return learningReviewerClosure{}, fmt.Errorf(
			"%w: Reviewer Run projection is not standalone",
			ErrLearningReviewLineage,
		)
	}
	if err := validateLearningReviewerFacts(
		proposal,
		manifest,
		member,
		request,
		config,
	); err != nil {
		return learningReviewerClosure{}, err
	}
	return learningReviewerClosure{manifest: manifest, member: member, request: request}, nil
}

func validateLearningReviewerFacts(
	proposal LearningProposalRecord,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	request learningcontract.ReviewRequestV1,
	config moduleapi.ModelBindingConfigV1,
) error {
	if manifest.RunID == proposal.Proposal.ProposerRunID ||
		member.MemberID == proposal.Proposal.ProposerMember.MemberID ||
		member.MemberSnapshotDigest == proposal.Proposal.ProposerMember.Digest ||
		manifest.TenantID != proposal.Proposal.TenantID ||
		manifest.Workspace != proposal.Proposal.Workspace ||
		member.Workspace != proposal.Proposal.Workspace ||
		member.Agent.ID == proposal.Proposal.ProposerAgent.ID ||
		member.Profile.ID == proposal.Proposal.ProposerProfile.ID ||
		manifest.PrimaryAgent != member.Agent ||
		manifest.ConversationTurn != nil || manifest.Composite != nil ||
		manifest.ParentRunID != "" || len(manifest.Members) != 1 ||
		len(member.PortPlans) != 1 || len(member.Actions) != 0 {
		return fmt.Errorf(
			"%w: Reviewer Run is not exact independent model-only scope",
			ErrLearningReviewLineage,
		)
	}
	plan := member.PortPlans[0]
	if plan.Port.Name != moduleapi.PortNameModelGenerate ||
		plan.Port.ExactVersion != moduleapi.PortVersionV1 ||
		len(plan.Bindings) != 1 ||
		plan.Bindings[0].FailurePolicy != moduleapi.FailureRequired {
		return fmt.Errorf(
			"%w: Reviewer must contain exactly one required model.generate/v1 Binding",
			ErrLearningReviewLineage,
		)
	}
	if request.ProposalID != proposal.ProposalID ||
		request.SourceFingerprint != proposal.Proposal.SourceFingerprint ||
		request.ContentFingerprint != proposal.Proposal.ContentFingerprint ||
		request.DraftDigest != proposal.Proposal.DraftDigest ||
		!bytes.Equal(request.ProposalCanonical, proposal.ProposalCanonical) ||
		!bytes.Equal(request.DraftCanonical, proposal.DraftCanonical) {
		return fmt.Errorf(
			"%w: ReviewRequest does not bind exact Proposal and Draft",
			ErrLearningReviewLineage,
		)
	}
	tightened, err := corecontract.TightenReviewerModelParametersV1(
		config.Parameters,
		request.MaxOutputTokens,
	)
	if err != nil || !bytes.Equal(tightened, config.Parameters) {
		return fmt.Errorf(
			"%w: Reviewer model parameters require explicit max_tokens between 1 and %d: %v",
			ErrLearningReviewLineage,
			learningcontract.MaxReviewOutputTokensV1,
			err,
		)
	}
	return nil
}

func deriveLearningReview(
	ctx context.Context,
	connection readQueryerV1,
	proposal LearningProposalRecord,
) (derivedLearningReview, error) {
	closure, err := loadLearningReviewerClosure(ctx, connection, proposal)
	if err != nil {
		return derivedLearningReview{}, err
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id FROM model_dispatch_attempts
		WHERE run_id=? ORDER BY attempt_id
	`, proposal.ReviewRunID)
	if err != nil {
		return derivedLearningReview{}, fmt.Errorf(
			"%w: enumerate Reviewer Attempts: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	var attemptIDs []string
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return derivedLearningReview{}, err
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return derivedLearningReview{}, err
	}
	if err := rows.Close(); err != nil {
		return derivedLearningReview{}, err
	}
	if len(attemptIDs) == 0 {
		return derivedLearningReview{}, ErrLearningReviewNotReady
	}
	if len(attemptIDs) != 1 {
		return derivedLearningReview{}, fmt.Errorf(
			"%w: Reviewer Run has %d Model Attempts",
			ErrLearningReviewLineage,
			len(attemptIDs),
		)
	}
	attempt, err := queryModelDispatchRecord(ctx, connection, attemptIDs[0])
	if err != nil {
		return derivedLearningReview{}, err
	}
	if attempt.Attempt.RunID != proposal.ReviewRunID ||
		attempt.Attempt.MemberID != closure.member.MemberID ||
		attempt.Attempt.MemberSnapshotDigest !=
			closure.member.MemberSnapshotDigest ||
		attempt.Attempt.LogicalStepID !=
			corecontract.PureChatModelLogicalStepIDV1 ||
		attempt.Attempt.SourceDispatchAttemptID != "" {
		return derivedLearningReview{}, fmt.Errorf(
			"%w: Reviewer Attempt is not the unique ordinary Pure Chat step",
			ErrLearningReviewLineage,
		)
	}
	var dispatchCount int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, proposal.ReviewRunID).Scan(&dispatchCount); err != nil || dispatchCount != 0 {
		return derivedLearningReview{}, fmt.Errorf(
			"%w: Reviewer Run has Action or Channel dispatch: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	derived := derivedLearningReview{attempt: attempt}
	switch attempt.Attempt.State {
	case corecontract.ModelAttemptPending:
		return derivedLearningReview{}, ErrLearningReviewNotReady
	case corecontract.ModelAttemptUnknown:
		derived.state = LearningProposalReviewUnknown
		return derived, nil
	case corecontract.ModelAttemptFailed:
		if err := requireLearningReviewerTerminal(
			ctx,
			connection,
			proposal.ReviewRunID,
			attempt.Attempt.AttemptID,
			corecontract.ModelAttemptFailed,
		); err != nil {
			return derivedLearningReview{}, err
		}
		derived.state = LearningProposalReviewFailed
		return derived, nil
	case corecontract.ModelAttemptSucceeded:
		if err := requireLearningReviewerTerminal(
			ctx,
			connection,
			proposal.ReviewRunID,
			attempt.Attempt.AttemptID,
			corecontract.ModelAttemptSucceeded,
		); err != nil {
			return derivedLearningReview{}, err
		}
	default:
		return derivedLearningReview{}, fmt.Errorf(
			"%w: unsupported Reviewer Attempt state %q",
			ErrLearningReviewLineage,
			attempt.Attempt.State,
		)
	}
	result, err := queryContent(ctx, connection, attempt.Attempt.ResultRef)
	if err != nil || result.Kind != ContentModelResult ||
		result.MediaType != admissionJSONMediaType {
		return derivedLearningReview{}, fmt.Errorf(
			"%w: Reviewer MODEL_RESULT: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.CanonicalBytes)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" {
		derived.state = LearningProposalReviewFailed
		return derived, nil
	}
	verdict, canonical, digest, err :=
		learningcontract.ParseReviewVerdictV1([]byte(output.AssistantText))
	if err != nil || verdict.ValidateForProposalV1(
		proposal.Proposal,
		proposal.ProposalID,
	) != nil {
		derived.state = LearningProposalReviewFailed
		return derived, nil
	}
	derived.verdict = &verdict
	derived.verdictCanonical = bytes.Clone(canonical)
	derived.verdictDigest = digest
	if verdict.Decision == learningcontract.ReviewDecisionApproveV1 {
		derived.state = LearningProposalApproved
	} else {
		derived.state = LearningProposalRejected
	}
	return derived, nil
}

// verifyLearningReviewProjection validates the Store-owned review projection
// while preserving the only legitimate crash windows. REVIEW_PENDING may
// coexist with no Attempt or with one authoritative Attempt whose outcome has
// not yet been projected. REVIEW_UNKNOWN may coexist with that same Attempt
// after evidence-based reconciliation but before the revision-3 projection.
func verifyLearningReviewProjection(
	ctx context.Context,
	connection readQueryerV1,
	proposal LearningProposalRecord,
) error {
	if proposal.State == LearningProposalSubmitted {
		if proposal.Revision != 0 || proposal.ReviewRunID != "" ||
			proposal.ReviewerAttemptID != "" {
			return fmt.Errorf(
				"%w: SUBMITTED Proposal contains Review facts",
				ErrLearningProposalIntegrity,
			)
		}
		return nil
	}
	if _, err := loadLearningReviewerClosure(ctx, connection, proposal); err != nil {
		return fmt.Errorf(
			"%w: Reviewer closure: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	derived, err := deriveLearningReview(ctx, connection, proposal)
	if proposal.State == LearningProposalReviewPending {
		if errors.Is(err, ErrLearningReviewNotReady) {
			return nil
		}
		if err != nil {
			return fmt.Errorf(
				"%w: pending Reviewer Attempt closure: %v",
				ErrLearningProposalIntegrity,
				err,
			)
		}
		// A unique UNKNOWN or terminal Attempt may be durable immediately
		// before FinalizeLearningReview projects its outcome.
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"%w: terminal Reviewer Attempt closure: %v",
			ErrLearningProposalIntegrity,
			err,
		)
	}
	if proposal.ReviewerAttemptID != derived.attempt.Attempt.AttemptID {
		return fmt.Errorf(
			"%w: Proposal does not bind the authoritative Reviewer Attempt",
			ErrLearningProposalIntegrity,
		)
	}

	switch proposal.State {
	case LearningProposalReviewUnknown:
		if proposal.Revision != 2 {
			return fmt.Errorf(
				"%w: REVIEW_UNKNOWN must be revision 2",
				ErrLearningProposalIntegrity,
			)
		}
		if derived.state == LearningProposalReviewUnknown {
			return nil
		}
		if derived.attempt.Attempt.ReconciliationEvidenceRef == "" {
			return fmt.Errorf(
				"%w: reconciled REVIEW_UNKNOWN has no evidence",
				ErrLearningProposalIntegrity,
			)
		}
		// The same Attempt may already be reconciled while the Proposal is
		// still at revision 2 after a crash. The next finalization projects
		// derived.state at revision 3.
		return nil
	case LearningProposalApproved,
		LearningProposalRejected,
		LearningProposalReviewFailed:
		if derived.state != proposal.State {
			return fmt.Errorf(
				"%w: terminal Review state %s differs from derived %s",
				ErrLearningProposalIntegrity,
				proposal.State,
				derived.state,
			)
		}
		if proposal.Revision == 3 &&
			derived.attempt.Attempt.ReconciliationEvidenceRef == "" {
			return fmt.Errorf(
				"%w: revision-3 Review has no reconciliation evidence",
				ErrLearningProposalIntegrity,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: unsupported Review projection %s/%d",
			ErrLearningProposalIntegrity,
			proposal.State,
			proposal.Revision,
		)
	}
}

func requireLearningReviewerTerminal(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	attemptID string,
	state corecontract.ModelAttemptState,
) error {
	terminal, err := loadTerminalRunResult(ctx, connection, runID)
	if err != nil || terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.AttemptID != attemptID || terminal.ModelState != state {
		return fmt.Errorf(
			"%w: Reviewer Run terminal does not bind exact Attempt: %v",
			ErrLearningReviewLineage,
			err,
		)
	}
	return nil
}

func requireLearningReviewCASRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Learning Review %s CAS: %w",
			operation,
			err,
		)
	}
	if rows != 1 {
		return fmt.Errorf(
			"%w: %s CAS affected %d rows",
			ErrLearningReviewConflict,
			operation,
			rows,
		)
	}
	return nil
}

func learningReviewTransitionTime(previous int64) int64 {
	now := nowUnixMicro()
	if now < previous {
		return previous
	}
	return now
}
