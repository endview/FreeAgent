package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	runObservationSchemaV1              = "run-observation/v1"
	runObservationDigestDomainV1        = "freeagent.current-store.run-observation.v1"
	runContinuationDigestDomainV1       = "freeagent.current-store.loop-continuation.v1"
	runObservationMaximumCanonicalV1    = 16 << 10
	runObservationAdmissionV1           = runObservationTransitionV1("ADMISSION")
	runObservationModelBeginV1          = runObservationTransitionV1("MODEL_BEGIN")
	runObservationModelOutcomeV1        = runObservationTransitionV1("MODEL_OUTCOME")
	runObservationActionBeginV1         = runObservationTransitionV1("ACTION_BEGIN")
	runObservationActionOutcomeV1       = runObservationTransitionV1("ACTION_OUTCOME")
	runObservationChannelBeginV1        = runObservationTransitionV1("CHANNEL_BEGIN")
	runObservationChannelOutcomeV1      = runObservationTransitionV1("CHANNEL_OUTCOME")
	runObservationModelRejectionV1      = runObservationTransitionV1("MODEL_ACTION_REJECTION")
	runObservationCoreFailureV1         = runObservationTransitionV1("CORE_FAILURE")
	runObservationStartupRecoveryV1     = runObservationTransitionV1("STARTUP_RECOVERY")
	runObservationCompositeTransitionV1 = runObservationTransitionV1("COMPOSITE_TRANSITION")
	runObservationCancellationV1        = runObservationTransitionV1("CANCELLATION")
)

type runObservationTransitionV1 string

type runObservationCanonicalV1 struct {
	SchemaVersion                 string                     `json:"schema_version"`
	ObservationSequence           uint64                     `json:"observation_sequence"`
	PreviousSnapshotDigest        string                     `json:"previous_snapshot_digest,omitempty"`
	TransitionKind                runObservationTransitionV1 `json:"transition_kind"`
	StoreInstanceID               string                     `json:"store_instance_id"`
	RunID                         string                     `json:"run_id"`
	TenantID                      string                     `json:"tenant_id"`
	WorkspaceID                   string                     `json:"workspace_id"`
	AdmissionKey                  string                     `json:"admission_key"`
	AdmissionIntentDigest         string                     `json:"admission_intent_digest"`
	ManifestDigest                string                     `json:"manifest_digest"`
	MemberID                      string                     `json:"member_id"`
	MemberDigest                  string                     `json:"member_digest"`
	HasActionPort                 bool                       `json:"has_action_port"`
	HasChannelPort                bool                       `json:"has_channel_port"`
	RunState                      string                     `json:"run_state"`
	Disposition                   string                     `json:"disposition,omitempty"`
	RunRevision                   uint64                     `json:"run_revision"`
	CancelRequestRef              string                     `json:"cancel_request_ref,omitempty"`
	CreatedAtUnixMicros           uint64                     `json:"created_at_unix_micros"`
	UpdatedAtUnixMicros           uint64                     `json:"updated_at_unix_micros"`
	FrameStep                     string                     `json:"frame_step"`
	UsageLedgerRef                string                     `json:"usage_ledger_ref"`
	ContinuationDigest            string                     `json:"continuation_digest"`
	ContinuationSizeBytes         uint32                     `json:"continuation_size_bytes"`
	ContinuationAttemptKind       corecontract.AttemptKindV1 `json:"continuation_attempt_kind,omitempty"`
	ContinuationLogicalStepID     string                     `json:"continuation_logical_step_id,omitempty"`
	ContinuationAttemptID         string                     `json:"continuation_attempt_id,omitempty"`
	ContinuationCoreFailureReason string                     `json:"continuation_core_failure_reason,omitempty"`
	PendingModelAttemptID         string                     `json:"pending_model_attempt_id,omitempty"`
	PendingDispatchAttemptID      string                     `json:"pending_dispatch_attempt_id,omitempty"`
	WaitingReason                 string                     `json:"waiting_reason,omitempty"`
	SourceEventSequence           uint64                     `json:"source_event_sequence"`
	SourceEventFrameRevision      uint64                     `json:"source_event_frame_revision"`
	SourceEventKind               string                     `json:"source_event_kind"`
	SourceEventPayloadDigest      string                     `json:"source_event_payload_digest"`
	SourceEventAttemptID          string                     `json:"source_event_attempt_id,omitempty"`
	SourceEventLogicalStepID      string                     `json:"source_event_logical_step_id,omitempty"`
}

type runObservationRecordV1 struct {
	Snapshot     runObservationCanonicalV1
	Digest       string
	Canonical    []byte
	Continuation []byte
}

type runObservationWriterV1 interface {
	readQueryerV1
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type runObservationEventFactsV1 struct {
	FromRevision              uint64
	ToRevision                uint64
	CreatedAt                 uint64
	AttemptID                 string
	LogicalStepID             string
	ModelState                corecontract.ModelAttemptState
	DispatchState             DispatchState
	ActionResult              corecontract.ActionResultStatusV1
	CompositeKind             string
	TransitionOrigin          corecontract.DispatchTransitionOriginV1
	ModelUsage                *corecontract.ModelUsageEventV1
	SourceModelAttemptID      string
	ResourceSemanticDigest    string
	SourceModelSemanticDigest string
}

// appendRunObservationV1 is deliberately private. Production writers call
// one of the transition-specific wrappers below after their final semantic
// write and before COMMIT; no public API accepts a caller-selected transition.
func appendRunObservationV1(
	ctx context.Context,
	connection runObservationWriterV1,
	runID string,
	transition runObservationTransitionV1,
) error {
	if ctx == nil || connection == nil || !validLeaseOpaqueID(runID) {
		return fmt.Errorf("%w: invalid Run observation append", ErrLoopIntegrity)
	}
	var previousSequence int64
	var previousDigest string
	headErr := connection.QueryRowContext(ctx, `
		SELECT observation_sequence,snapshot_digest
		FROM run_observation_heads WHERE run_id=?
	`, runID).Scan(&previousSequence, &previousDigest)
	initial := errors.Is(headErr, sql.ErrNoRows)
	if headErr != nil && !initial {
		return fmt.Errorf("currentstore: read Run observation head: %w", headErr)
	}
	if initial != (transition == runObservationAdmissionV1) ||
		(!initial && (previousSequence <= 0 || !moduleapi.ValidSHA256(previousDigest))) {
		return fmt.Errorf("%w: Run observation transition/head", ErrLoopIntegrity)
	}
	if !initial {
		var orphan int
		if err := connection.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM run_observation_snapshots
				WHERE run_id=? AND observation_sequence>? LIMIT 1
			)
		`, runID, previousSequence).Scan(&orphan); err != nil || orphan != 0 {
			return fmt.Errorf("%w: Run observation orphan tail: %v", ErrLoopIntegrity, err)
		}
	}
	record, err := loadCurrentRunObservationV1(ctx, connection, runID)
	if err != nil {
		return err
	}
	record.Snapshot.SchemaVersion = runObservationSchemaV1
	record.Snapshot.TransitionKind = transition
	record.Snapshot.ObservationSequence = uint64(previousSequence + 1)
	record.Snapshot.PreviousSnapshotDigest = previousDigest
	if initial {
		record.Snapshot.ObservationSequence = 1
		record.Snapshot.PreviousSnapshotDigest = ""
	}
	var previous *runObservationRecordV1
	if !initial {
		loaded, err := loadRunObservationSnapshotV1(ctx, connection, previousDigest)
		if err != nil {
			return err
		}
		previous = &loaded
		record.Snapshot.HasActionPort = loaded.Snapshot.HasActionPort
		record.Snapshot.HasChannelPort = loaded.Snapshot.HasChannelPort
		if transition == runObservationCancellationV1 {
			record.Snapshot.SourceEventAttemptID = loaded.Snapshot.SourceEventAttemptID
			record.Snapshot.SourceEventLogicalStepID = loaded.Snapshot.SourceEventLogicalStepID
		}
	} else {
		hasAction, hasChannel, err := loadRunObservationPortCapabilitiesV1(
			ctx, connection, record.Snapshot.RunID, record.Snapshot.MemberID,
			record.Snapshot.MemberDigest,
		)
		if err != nil {
			return err
		}
		record.Snapshot.HasActionPort = hasAction
		record.Snapshot.HasChannelPort = hasChannel
	}
	if transition != runObservationCancellationV1 {
		facts, err := deriveRunObservationEventFactsV1(
			ctx, connection, record.Snapshot,
		)
		if err != nil {
			return err
		}
		record.Snapshot.SourceEventAttemptID = facts.AttemptID
		record.Snapshot.SourceEventLogicalStepID = facts.LogicalStepID
	}
	if err := validateRunObservationAdvanceV1(previous, record); err != nil {
		return err
	}
	record.Canonical, record.Digest, err = canonicalRunObservationV1(record.Snapshot)
	if err != nil {
		return err
	}
	_, err = connection.ExecContext(ctx, `INSERT INTO run_observation_snapshots(
		snapshot_digest,run_id,observation_sequence,previous_snapshot_digest,
		transition_kind,store_instance_id,tenant_id,workspace_id,admission_key,
		admission_intent_digest,manifest_digest,member_id,member_digest,
		has_action_port,has_channel_port,
		run_state,disposition,run_revision,cancel_request_ref,created_at,updated_at,
		frame_step,usage_ledger_ref,continuation_digest,continuation_size_bytes,
		continuation_attempt_kind,continuation_logical_step_id,
		continuation_attempt_id,continuation_core_failure_reason,
		pending_model_attempt_id,pending_dispatch_attempt_id,waiting_reason,
		source_event_sequence,source_event_frame_revision,source_event_kind,
		source_event_payload_digest,source_event_attempt_id,
		source_event_logical_step_id,canonical_json
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.Digest, record.Snapshot.RunID, int64(record.Snapshot.ObservationSequence),
		nullableRunObservationStringV1(record.Snapshot.PreviousSnapshotDigest),
		string(record.Snapshot.TransitionKind), record.Snapshot.StoreInstanceID,
		record.Snapshot.TenantID,
		record.Snapshot.WorkspaceID, record.Snapshot.AdmissionKey,
		record.Snapshot.AdmissionIntentDigest, record.Snapshot.ManifestDigest,
		record.Snapshot.MemberID, record.Snapshot.MemberDigest,
		record.Snapshot.HasActionPort, record.Snapshot.HasChannelPort,
		record.Snapshot.RunState, nullableRunObservationStringV1(record.Snapshot.Disposition),
		int64(record.Snapshot.RunRevision),
		nullableRunObservationStringV1(record.Snapshot.CancelRequestRef),
		int64(record.Snapshot.CreatedAtUnixMicros), int64(record.Snapshot.UpdatedAtUnixMicros),
		record.Snapshot.FrameStep, record.Snapshot.UsageLedgerRef,
		record.Snapshot.ContinuationDigest, int64(record.Snapshot.ContinuationSizeBytes),
		nullableRunObservationStringV1(string(record.Snapshot.ContinuationAttemptKind)),
		nullableRunObservationStringV1(record.Snapshot.ContinuationLogicalStepID),
		nullableRunObservationStringV1(record.Snapshot.ContinuationAttemptID),
		nullableRunObservationStringV1(record.Snapshot.ContinuationCoreFailureReason),
		nullableRunObservationStringV1(record.Snapshot.PendingModelAttemptID),
		nullableRunObservationStringV1(record.Snapshot.PendingDispatchAttemptID),
		nullableRunObservationStringV1(record.Snapshot.WaitingReason),
		int64(record.Snapshot.SourceEventSequence),
		int64(record.Snapshot.SourceEventFrameRevision), record.Snapshot.SourceEventKind,
		record.Snapshot.SourceEventPayloadDigest,
		nullableRunObservationStringV1(record.Snapshot.SourceEventAttemptID),
		nullableRunObservationStringV1(record.Snapshot.SourceEventLogicalStepID),
		record.Canonical,
	)
	if err != nil {
		return fmt.Errorf("currentstore: append Run observation Snapshot: %w", err)
	}
	if initial {
		_, err = connection.ExecContext(ctx, `INSERT INTO run_observation_heads(
			run_id,observation_sequence,snapshot_digest,tenant_id,workspace_id,
			run_state,run_revision,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?)`, runID, 1, record.Digest,
			record.Snapshot.TenantID, record.Snapshot.WorkspaceID,
			record.Snapshot.RunState, int64(record.Snapshot.RunRevision),
			int64(record.Snapshot.CreatedAtUnixMicros),
			int64(record.Snapshot.UpdatedAtUnixMicros))
	} else {
		var result sql.Result
		result, err = connection.ExecContext(ctx, `UPDATE run_observation_heads
			SET observation_sequence=?,snapshot_digest=?,tenant_id=?,workspace_id=?,
			    run_state=?,run_revision=?,created_at=?,updated_at=?
			WHERE run_id=? AND observation_sequence=? AND snapshot_digest=?`,
			int64(record.Snapshot.ObservationSequence), record.Digest,
			record.Snapshot.TenantID, record.Snapshot.WorkspaceID,
			record.Snapshot.RunState, int64(record.Snapshot.RunRevision),
			int64(record.Snapshot.CreatedAtUnixMicros),
			int64(record.Snapshot.UpdatedAtUnixMicros), runID,
			previousSequence, previousDigest,
		)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				err = fmt.Errorf("Run observation head CAS affected %d rows", affected)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("currentstore: advance Run observation head: %w", err)
	}
	return nil
}

func appendAdmissionRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationAdmissionV1)
}
func appendModelBeginRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationModelBeginV1)
}
func appendModelOutcomeRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationModelOutcomeV1)
}
func appendActionBeginRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationActionBeginV1)
}
func appendActionOutcomeRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationActionOutcomeV1)
}
func appendChannelBeginRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationChannelBeginV1)
}
func appendChannelOutcomeRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationChannelOutcomeV1)
}
func appendModelRejectionRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationModelRejectionV1)
}
func appendCoreFailureRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationCoreFailureV1)
}
func appendStartupRecoveryRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationStartupRecoveryV1)
}
func appendCompositeTransitionRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationCompositeTransitionV1)
}
func appendCancellationRunObservationV1(ctx context.Context, q runObservationWriterV1, runID string) error {
	return appendRunObservationV1(ctx, q, runID, runObservationCancellationV1)
}

// loadRunObservationPortCapabilitiesV1 is used only while publishing the
// immutable admission observation. Later transitions and online readers copy
// these frozen facts from the preceding observation and never reload the
// potentially large Member canonical.
func loadRunObservationPortCapabilitiesV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID, memberID, expectedDigest string,
) (bool, bool, error) {
	var canonical []byte
	var storedDigest string
	if err := queryer.QueryRowContext(ctx, `SELECT canonical_json,digest
		FROM member_execution_snapshots WHERE run_id=? AND member_id=?`,
		runID, memberID,
	).Scan(&canonical, &storedDigest); err != nil {
		return false, false, fmt.Errorf(
			"%w: Run observation admission Member: %v", ErrLoopIntegrity, err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil || storedDigest != expectedDigest ||
		member.MemberSnapshotDigest != expectedDigest || member.MemberID != memberID {
		return false, false, fmt.Errorf(
			"%w: Run observation admission Member closure: %v", ErrLoopIntegrity, err,
		)
	}
	return memberHasActionPort(member), memberHasChannelPort(member), nil
}

func loadCurrentRunObservationV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID string,
) (runObservationRecordV1, error) {
	var record runObservationRecordV1
	var continuation []byte
	var disposition, cancel, pendingModel, pendingDispatch, waiting sql.NullString
	var runRevision, created, updated, frameRevision, sourceSequence, sourceFrameRevision int64
	err := queryer.QueryRowContext(ctx, `SELECT
		r.run_id,r.tenant_id,r.workspace_id,r.admission_key,r.admission_intent_digest,
		manifest.digest,r.state,r.disposition,r.revision,
		r.cancel_request_ref,r.created_at,r.updated_at,
		frame.frame_revision,frame.step,frame.usage_ledger_ref,frame.continuation,
		frame.pending_attempt_id,frame.pending_dispatch_attempt_id,frame.waiting_reason,
		frame.last_authoritative_event,event.to_revision,event.event_kind,event.payload_digest
		FROM runs AS r
		JOIN run_manifests AS manifest ON manifest.run_id=r.run_id
		JOIN loop_frames AS frame ON frame.run_id=r.run_id
		JOIN run_events AS event ON event.run_id=r.run_id
		 AND event.event_sequence=frame.last_authoritative_event
		WHERE r.run_id=?`, runID).Scan(
		&record.Snapshot.RunID, &record.Snapshot.TenantID,
		&record.Snapshot.WorkspaceID, &record.Snapshot.AdmissionKey,
		&record.Snapshot.AdmissionIntentDigest, &record.Snapshot.ManifestDigest,
		&record.Snapshot.RunState, &disposition, &runRevision,
		&cancel, &created, &updated, &frameRevision, &record.Snapshot.FrameStep,
		&record.Snapshot.UsageLedgerRef, &continuation, &pendingModel,
		&pendingDispatch, &waiting, &sourceSequence, &sourceFrameRevision,
		&record.Snapshot.SourceEventKind, &record.Snapshot.SourceEventPayloadDigest,
	)
	if err != nil {
		return record, fmt.Errorf("%w: load current Run observation: %v", ErrLoopIntegrity, err)
	}
	if err := queryer.QueryRowContext(ctx, `SELECT store_instance_id
		FROM store_meta WHERE singleton=1`).Scan(&record.Snapshot.StoreInstanceID); err != nil {
		return record, fmt.Errorf("%w: Run observation Store identity: %v", ErrLoopIntegrity, err)
	}
	if runRevision < 0 || created <= 0 || updated < created || frameRevision < 0 ||
		sourceSequence < 0 || sourceFrameRevision < 0 || frameRevision < sourceFrameRevision ||
		len(continuation) < 2 || len(continuation) > corecontract.MaxLoopContinuationCanonicalBytesV1 {
		return record, fmt.Errorf("%w: Run observation numeric/continuation projection", ErrLoopIntegrity)
	}
	members, err := queryer.QueryContext(ctx, `SELECT member_id,digest
		FROM member_execution_snapshots WHERE run_id=?
		ORDER BY member_id COLLATE BINARY LIMIT 2`, runID)
	if err != nil {
		return record, fmt.Errorf("%w: Run observation Member rows: %v", ErrLoopIntegrity, err)
	}
	memberCount := 0
	for members.Next() {
		memberCount++
		if err := members.Scan(&record.Snapshot.MemberID, &record.Snapshot.MemberDigest); err != nil {
			_ = members.Close()
			return record, fmt.Errorf("%w: Run observation Member: %v", ErrLoopIntegrity, err)
		}
	}
	if err := members.Err(); err != nil {
		_ = members.Close()
		return record, fmt.Errorf("%w: Run observation Member rows: %v", ErrLoopIntegrity, err)
	}
	if err := members.Close(); err != nil || memberCount != 1 {
		return record, fmt.Errorf("%w: Run observation primary Member cardinality: %v", ErrLoopIntegrity, err)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || continued.State != record.Snapshot.FrameStep {
		return record, fmt.Errorf("%w: Run observation continuation: %v", ErrLoopIntegrity, err)
	}
	frame := LoopFrameRecord{
		RunID: runID, Step: record.Snapshot.FrameStep,
		UsageLedgerRef: record.Snapshot.UsageLedgerRef,
		Continuation:   continuation, PendingAttemptID: pendingModel.String,
		PendingDispatchAttemptID: pendingDispatch.String, WaitingReason: waiting.String,
		LastAuthoritativeEvent: uint64(sourceSequence),
	}
	if err := validateLoopRunFrameProjection(
		record.Snapshot.RunState, disposition.String, frame,
	); err != nil {
		return record, err
	}
	if err := validateFairTargetContinuationPointers(continued, pendingModel, pendingDispatch); err != nil {
		return record, err
	}
	record.Snapshot.Disposition = disposition.String
	record.Snapshot.RunRevision = uint64(runRevision)
	record.Snapshot.CancelRequestRef = cancel.String
	record.Snapshot.CreatedAtUnixMicros = uint64(created)
	record.Snapshot.UpdatedAtUnixMicros = uint64(updated)
	record.Snapshot.ContinuationDigest = moduleapi.Digest(
		runContinuationDigestDomainV1, continuation,
	)
	record.Snapshot.ContinuationSizeBytes = uint32(len(continuation))
	record.Snapshot.ContinuationAttemptKind = continued.AttemptKind
	record.Snapshot.ContinuationLogicalStepID = continued.LogicalStepID
	record.Snapshot.ContinuationAttemptID = continued.AttemptID
	record.Snapshot.ContinuationCoreFailureReason = continued.CoreFailureReason
	record.Snapshot.PendingModelAttemptID = pendingModel.String
	record.Snapshot.PendingDispatchAttemptID = pendingDispatch.String
	record.Snapshot.WaitingReason = waiting.String
	record.Snapshot.SourceEventSequence = uint64(sourceSequence)
	record.Snapshot.SourceEventFrameRevision = uint64(sourceFrameRevision)
	record.Continuation = bytes.Clone(continuation)
	return record, nil
}

func canonicalRunObservationV1(
	snapshot runObservationCanonicalV1,
) ([]byte, string, error) {
	if err := validateRunObservationCanonicalV1(snapshot); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", fmt.Errorf("currentstore: encode Run observation: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(raw, moduleapi.CanonicalJSONLimits{
		MaxBytes: runObservationMaximumCanonicalV1,
		MaxDepth: 4,
		MaxNodes: 64,
	})
	if err != nil || len(canonical) > runObservationMaximumCanonicalV1 {
		return nil, "", fmt.Errorf("%w: Run observation canonical: %v", ErrLoopIntegrity, err)
	}
	return canonical, moduleapi.Digest(runObservationDigestDomainV1, canonical), nil
}

func loadRunObservationSnapshotV1(
	ctx context.Context,
	queryer readQueryerV1,
	digest string,
) (runObservationRecordV1, error) {
	var record runObservationRecordV1
	if !moduleapi.ValidSHA256(digest) {
		return record, fmt.Errorf("%w: invalid Run observation digest", ErrLoopIntegrity)
	}
	var previous, disposition, cancel sql.NullString
	var continuationKind, continuationStep, continuationAttempt, continuationFailure sql.NullString
	var pendingModel, pendingDispatch, waiting sql.NullString
	var sourceAttempt, sourceLogicalStep sql.NullString
	var sequence, runRevision, created, updated, continuationSize int64
	var eventSequence, eventFrameRevision int64
	var canonical []byte
	err := queryer.QueryRowContext(ctx, `SELECT
		snapshot_digest,run_id,observation_sequence,previous_snapshot_digest,
		transition_kind,store_instance_id,tenant_id,workspace_id,admission_key,
		admission_intent_digest,manifest_digest,member_id,member_digest,
		has_action_port,has_channel_port,
		run_state,disposition,run_revision,cancel_request_ref,created_at,updated_at,
		frame_step,usage_ledger_ref,continuation_digest,continuation_size_bytes,
		continuation_attempt_kind,continuation_logical_step_id,
		continuation_attempt_id,continuation_core_failure_reason,
		pending_model_attempt_id,pending_dispatch_attempt_id,waiting_reason,
		source_event_sequence,source_event_frame_revision,source_event_kind,
		source_event_payload_digest,source_event_attempt_id,
		source_event_logical_step_id,canonical_json
		FROM run_observation_snapshots WHERE snapshot_digest=?`, digest).Scan(
		&record.Digest, &record.Snapshot.RunID, &sequence, &previous,
		&record.Snapshot.TransitionKind, &record.Snapshot.StoreInstanceID,
		&record.Snapshot.TenantID, &record.Snapshot.WorkspaceID,
		&record.Snapshot.AdmissionKey, &record.Snapshot.AdmissionIntentDigest,
		&record.Snapshot.ManifestDigest, &record.Snapshot.MemberID,
		&record.Snapshot.MemberDigest, &record.Snapshot.HasActionPort,
		&record.Snapshot.HasChannelPort, &record.Snapshot.RunState, &disposition,
		&runRevision, &cancel, &created, &updated, &record.Snapshot.FrameStep,
		&record.Snapshot.UsageLedgerRef, &record.Snapshot.ContinuationDigest,
		&continuationSize, &continuationKind, &continuationStep, &continuationAttempt,
		&continuationFailure, &pendingModel, &pendingDispatch, &waiting,
		&eventSequence, &eventFrameRevision, &record.Snapshot.SourceEventKind,
		&record.Snapshot.SourceEventPayloadDigest, &sourceAttempt,
		&sourceLogicalStep, &canonical,
	)
	if err != nil || sequence <= 0 || runRevision < 0 || created <= 0 || updated < created ||
		continuationSize <= 0 || eventSequence < 0 || eventFrameRevision < 0 {
		return record, fmt.Errorf("%w: load Run observation snapshot: %v", ErrLoopIntegrity, err)
	}
	record.Snapshot.SchemaVersion = runObservationSchemaV1
	record.Snapshot.ObservationSequence = uint64(sequence)
	record.Snapshot.PreviousSnapshotDigest = previous.String
	record.Snapshot.Disposition = disposition.String
	record.Snapshot.RunRevision = uint64(runRevision)
	record.Snapshot.CancelRequestRef = cancel.String
	record.Snapshot.CreatedAtUnixMicros = uint64(created)
	record.Snapshot.UpdatedAtUnixMicros = uint64(updated)
	record.Snapshot.ContinuationSizeBytes = uint32(continuationSize)
	record.Snapshot.ContinuationAttemptKind = corecontract.AttemptKindV1(continuationKind.String)
	record.Snapshot.ContinuationLogicalStepID = continuationStep.String
	record.Snapshot.ContinuationAttemptID = continuationAttempt.String
	record.Snapshot.ContinuationCoreFailureReason = continuationFailure.String
	record.Snapshot.PendingModelAttemptID = pendingModel.String
	record.Snapshot.PendingDispatchAttemptID = pendingDispatch.String
	record.Snapshot.WaitingReason = waiting.String
	record.Snapshot.SourceEventSequence = uint64(eventSequence)
	record.Snapshot.SourceEventFrameRevision = uint64(eventFrameRevision)
	record.Snapshot.SourceEventAttemptID = sourceAttempt.String
	record.Snapshot.SourceEventLogicalStepID = sourceLogicalStep.String
	record.Canonical = bytes.Clone(canonical)
	rebuilt, rebuiltDigest, err := canonicalRunObservationV1(record.Snapshot)
	if err != nil || rebuiltDigest != record.Digest || !bytes.Equal(rebuilt, canonical) {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: Run observation snapshot canonical/digest: %v", ErrLoopIntegrity, err,
		)
	}
	record.Continuation, err = rebuildRunObservationContinuationV1(record.Snapshot)
	if err != nil {
		return runObservationRecordV1{}, err
	}
	return record, nil
}

func loadRunObservationHeadV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID string,
) (runObservationRecordV1, error) {
	var sequence, projectedRevision, projectedCreated, projectedUpdated int64
	var digest, projectedTenant, projectedWorkspace, projectedState string
	if err := queryer.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest,
		tenant_id,workspace_id,run_state,run_revision,created_at,updated_at
		FROM run_observation_heads WHERE run_id=?`, runID).Scan(
		&sequence, &digest, &projectedTenant, &projectedWorkspace, &projectedState,
		&projectedRevision, &projectedCreated, &projectedUpdated,
	); err != nil ||
		sequence <= 0 || !moduleapi.ValidSHA256(digest) {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: Run observation head: %v", ErrLoopIntegrity, err,
		)
	}
	var orphan int
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM run_observation_snapshots
		WHERE run_id=? AND observation_sequence>? LIMIT 1
	)`, runID, sequence).Scan(&orphan); err != nil || orphan != 0 {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: Run observation head rollback/orphan tail: %v", ErrLoopIntegrity, err,
		)
	}
	head, err := loadRunObservationSnapshotV1(ctx, queryer, digest)
	if err != nil || head.Snapshot.RunID != runID ||
		head.Snapshot.ObservationSequence != uint64(sequence) ||
		head.Snapshot.TenantID != projectedTenant ||
		head.Snapshot.WorkspaceID != projectedWorkspace ||
		head.Snapshot.RunState != projectedState || projectedRevision < 0 ||
		head.Snapshot.RunRevision != uint64(projectedRevision) ||
		projectedCreated <= 0 || projectedUpdated < projectedCreated ||
		head.Snapshot.CreatedAtUnixMicros != uint64(projectedCreated) ||
		head.Snapshot.UpdatedAtUnixMicros != uint64(projectedUpdated) {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: Run observation head snapshot: %v", ErrLoopIntegrity, err,
		)
	}
	current, err := loadCurrentRunObservationV1(ctx, queryer, runID)
	if err != nil {
		return runObservationRecordV1{}, err
	}
	current.Snapshot.SchemaVersion = runObservationSchemaV1
	current.Snapshot.ObservationSequence = head.Snapshot.ObservationSequence
	current.Snapshot.PreviousSnapshotDigest = head.Snapshot.PreviousSnapshotDigest
	current.Snapshot.TransitionKind = head.Snapshot.TransitionKind
	current.Snapshot.HasActionPort = head.Snapshot.HasActionPort
	current.Snapshot.HasChannelPort = head.Snapshot.HasChannelPort
	current.Snapshot.SourceEventAttemptID = head.Snapshot.SourceEventAttemptID
	current.Snapshot.SourceEventLogicalStepID = head.Snapshot.SourceEventLogicalStepID
	canonical, currentDigest, err := canonicalRunObservationV1(current.Snapshot)
	if err != nil || currentDigest != head.Digest || !bytes.Equal(canonical, head.Canonical) ||
		!bytes.Equal(current.Continuation, head.Continuation) {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: current Run differs from observation head: %v", ErrLoopIntegrity, err,
		)
	}
	if head.Snapshot.ObservationSequence == 1 {
		if err := validateRunObservationAdvanceV1(nil, head); err != nil {
			return runObservationRecordV1{}, err
		}
	} else {
		previous, err := loadRunObservationSnapshotV1(
			ctx, queryer, head.Snapshot.PreviousSnapshotDigest,
		)
		if err != nil {
			return runObservationRecordV1{}, err
		}
		if err := validateRunObservationAdvanceV1(&previous, head); err != nil {
			return runObservationRecordV1{}, err
		}
	}
	if err := verifyRunObservationCurrentAttemptV1(ctx, queryer, head.Snapshot); err != nil {
		return runObservationRecordV1{}, err
	}
	return head, nil
}

// loadRunObservationHeadForOverviewV1 verifies the bounded immutable head and
// exact current Run scalar projection without rebuilding history, Attempts, or
// large admission canonicals. The process-open semantic gate has already
// replayed those immutable closures.
func loadRunObservationHeadForOverviewV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID string,
) (runObservationRecordV1, error) {
	var sequence, projectedRevision, projectedCreated, projectedUpdated int64
	var digest, projectedTenant, projectedWorkspace, projectedState string
	if err := queryer.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest,
		tenant_id,workspace_id,run_state,run_revision,created_at,updated_at
		FROM run_observation_heads WHERE run_id=?`, runID).Scan(
		&sequence, &digest, &projectedTenant, &projectedWorkspace, &projectedState,
		&projectedRevision, &projectedCreated, &projectedUpdated,
	); err != nil || sequence <= 0 || !moduleapi.ValidSHA256(digest) {
		return runObservationRecordV1{}, fmt.Errorf("%w: Overview Run head: %v", ErrLoopIntegrity, err)
	}
	var orphan int
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1
		FROM run_observation_snapshots WHERE run_id=? AND observation_sequence>? LIMIT 1)`,
		runID, sequence).Scan(&orphan); err != nil || orphan != 0 {
		return runObservationRecordV1{}, fmt.Errorf("%w: Overview Run orphan tail: %v", ErrLoopIntegrity, err)
	}
	head, err := loadRunObservationSnapshotV1(ctx, queryer, digest)
	if err != nil || head.Snapshot.RunID != runID ||
		head.Snapshot.ObservationSequence != uint64(sequence) ||
		head.Snapshot.TenantID != projectedTenant || head.Snapshot.WorkspaceID != projectedWorkspace ||
		head.Snapshot.RunState != projectedState || projectedRevision < 0 ||
		head.Snapshot.RunRevision != uint64(projectedRevision) || projectedCreated <= 0 ||
		projectedUpdated < projectedCreated ||
		head.Snapshot.CreatedAtUnixMicros != uint64(projectedCreated) ||
		head.Snapshot.UpdatedAtUnixMicros != uint64(projectedUpdated) {
		return runObservationRecordV1{}, fmt.Errorf("%w: Overview Run head snapshot: %v", ErrLoopIntegrity, err)
	}
	var tenant, workspace, state string
	var disposition sql.NullString
	var revision, created, updated int64
	if err := queryer.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,state,disposition,
		revision,created_at,updated_at FROM runs WHERE run_id=?`, runID).Scan(
		&tenant, &workspace, &state, &disposition, &revision, &created, &updated,
	); err != nil || tenant != head.Snapshot.TenantID || workspace != head.Snapshot.WorkspaceID ||
		state != head.Snapshot.RunState || disposition.String != head.Snapshot.Disposition ||
		revision < 0 || uint64(revision) != head.Snapshot.RunRevision || created <= 0 ||
		updated < created || uint64(created) != head.Snapshot.CreatedAtUnixMicros ||
		uint64(updated) != head.Snapshot.UpdatedAtUnixMicros {
		return runObservationRecordV1{}, fmt.Errorf("%w: Overview Run raw projection: %v", ErrLoopIntegrity, err)
	}
	current, err := loadCurrentRunObservationV1(ctx, queryer, runID)
	if err != nil {
		return runObservationRecordV1{}, err
	}
	current.Snapshot.SchemaVersion = runObservationSchemaV1
	current.Snapshot.ObservationSequence = head.Snapshot.ObservationSequence
	current.Snapshot.PreviousSnapshotDigest = head.Snapshot.PreviousSnapshotDigest
	current.Snapshot.TransitionKind = head.Snapshot.TransitionKind
	current.Snapshot.HasActionPort = head.Snapshot.HasActionPort
	current.Snapshot.HasChannelPort = head.Snapshot.HasChannelPort
	current.Snapshot.SourceEventAttemptID = head.Snapshot.SourceEventAttemptID
	current.Snapshot.SourceEventLogicalStepID = head.Snapshot.SourceEventLogicalStepID
	canonical, currentDigest, err := canonicalRunObservationV1(current.Snapshot)
	if err != nil || currentDigest != head.Digest || !bytes.Equal(canonical, head.Canonical) ||
		!bytes.Equal(current.Continuation, head.Continuation) {
		return runObservationRecordV1{}, fmt.Errorf(
			"%w: current Overview Run differs from observation head: %v", ErrLoopIntegrity, err,
		)
	}
	if err := verifyRunObservationCurrentAttemptV1(ctx, queryer, head.Snapshot); err != nil {
		return runObservationRecordV1{}, err
	}
	return head, nil
}

func verifyRunObservationCurrentAttemptV1(
	ctx context.Context,
	queryer readQueryerV1,
	snapshot runObservationCanonicalV1,
) error {
	continuation, err := rebuildRunObservationContinuationV1(snapshot)
	if err != nil {
		return err
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil {
		return fmt.Errorf("%w: current observation continuation: %v", ErrLoopIntegrity, err)
	}
	if err := verifyRunObservationAttemptCardinalityV1(
		ctx, queryer, snapshot, continued,
	); err != nil {
		return err
	}
	expectedState := ""
	switch continued.State {
	case corecontract.ModelPendingLoopStep:
		expectedState = string(corecontract.ModelAttemptPending)
	case corecontract.ModelReadyAfterActionLoopStep:
		if continued.AttemptKind != corecontract.AttemptKindAction {
			return fmt.Errorf("%w: ready-after-Action carrier kind", ErrLoopIntegrity)
		}
		expectedState = string(ActionDispatchSucceeded)
	case corecontract.ActionPendingLoopStep, corecontract.ChannelPendingLoopStep:
		expectedState = "PENDING"
	case corecontract.WaitingReconciliationLoopStep:
		if continued.AttemptKind == corecontract.AttemptKindModel {
			expectedState = string(corecontract.ModelAttemptUnknown)
		} else {
			expectedState = "UNKNOWN"
		}
	case corecontract.TerminatedLoopStep:
		// The exact current carrier is checked below. Its full immutable
		// semantic closure is anchored by the resource observation ledger and
		// replayed by the startup/Backup semantic gate.
	default:
		if continued.AttemptID != "" {
			return fmt.Errorf("%w: unsupported current observation step", ErrLoopIntegrity)
		}
	}
	if continued.AttemptID == "" {
		return nil
	}
	if snapshot.SourceEventAttemptID != continued.AttemptID ||
		snapshot.SourceEventLogicalStepID != continued.LogicalStepID {
		return fmt.Errorf("%w: current observation/event carrier", ErrLoopIntegrity)
	}
	var attemptID, runID, tenantID, workspaceID, memberID, logicalStepID string
	var memberDigest, state string
	var revision, updatedAt int64
	switch continued.AttemptKind {
	case corecontract.AttemptKindModel:
		err = queryer.QueryRowContext(ctx, `SELECT attempt_id,run_id,tenant_id,workspace_id,
			member_id,logical_step_id,member_snapshot_digest,state,revision,updated_at
			FROM model_dispatch_attempts WHERE attempt_id=?`, continued.AttemptID).Scan(
			&attemptID, &runID, &tenantID, &workspaceID, &memberID, &logicalStepID,
			&memberDigest, &state, &revision, &updatedAt,
		)
		if continued.State == corecontract.TerminatedLoopStep &&
			state != string(corecontract.ModelAttemptSucceeded) &&
			state != string(corecontract.ModelAttemptFailed) {
			err = ErrLoopIntegrity
		}
	case corecontract.AttemptKindAction:
		if !snapshot.HasActionPort {
			return fmt.Errorf("%w: Action carrier without frozen port", ErrLoopIntegrity)
		}
		err = queryer.QueryRowContext(ctx, `SELECT attempt_id,run_id,tenant_id,workspace_id,
			member_id,logical_step_id,member_snapshot_digest,state,revision,updated_at
			FROM dispatch_attempts WHERE attempt_id=? AND dispatch_kind='ACTION'`,
			continued.AttemptID).Scan(
			&attemptID, &runID, &tenantID, &workspaceID, &memberID, &logicalStepID,
			&memberDigest, &state, &revision, &updatedAt,
		)
		if continued.State == corecontract.TerminatedLoopStep &&
			state != string(ActionDispatchSucceeded) && state != string(ActionDispatchFailed) {
			err = ErrLoopIntegrity
		}
	case corecontract.AttemptKindChannel:
		if !snapshot.HasChannelPort {
			return fmt.Errorf("%w: Channel carrier without frozen port", ErrLoopIntegrity)
		}
		err = queryer.QueryRowContext(ctx, `SELECT attempt_id,run_id,tenant_id,workspace_id,
			member_id,logical_step_id,member_snapshot_digest,state,revision,updated_at
			FROM dispatch_attempts WHERE attempt_id=? AND dispatch_kind='CHANNEL_SEND'`,
			continued.AttemptID).Scan(
			&attemptID, &runID, &tenantID, &workspaceID, &memberID, &logicalStepID,
			&memberDigest, &state, &revision, &updatedAt,
		)
		if continued.State == corecontract.TerminatedLoopStep &&
			state != string(DispatchSucceeded) && state != string(DispatchFailed) {
			err = ErrLoopIntegrity
		}
	default:
		return fmt.Errorf("%w: current observation AttemptKind", ErrLoopIntegrity)
	}
	if err != nil || attemptID != continued.AttemptID || runID != snapshot.RunID ||
		tenantID != snapshot.TenantID || workspaceID != snapshot.WorkspaceID ||
		memberID != snapshot.MemberID || memberDigest != snapshot.MemberDigest ||
		logicalStepID != continued.LogicalStepID || revision < 0 || updatedAt <= 0 ||
		(expectedState != "" && state != expectedState) {
		return fmt.Errorf("%w: current Attempt scalar observation: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func verifyRunObservationAttemptCardinalityV1(
	ctx context.Context,
	queryer readQueryerV1,
	snapshot runObservationCanonicalV1,
	continued corecontract.LoopContinuationV1,
) error {
	excludedModel, excludedDispatch := "", ""
	if continued.AttemptKind == corecontract.AttemptKindModel {
		excludedModel = continued.AttemptID
	} else if continued.AttemptKind == corecontract.AttemptKindAction ||
		continued.AttemptKind == corecontract.AttemptKindChannel {
		excludedDispatch = continued.AttemptID
	}
	var hiddenModel, hiddenDispatch int
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM model_dispatch_attempts
		WHERE run_id=? AND state IN ('PENDING','MODEL_UNKNOWN')
		  AND attempt_id<>? LIMIT 1)`, snapshot.RunID, excludedModel).Scan(&hiddenModel); err != nil {
		return fmt.Errorf("%w: hidden Model Attempt probe: %w", ErrLoopIntegrity, err)
	}
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM dispatch_attempts
		WHERE run_id=? AND state IN ('PENDING','UNKNOWN')
		  AND attempt_id<>? LIMIT 1)`, snapshot.RunID, excludedDispatch).Scan(&hiddenDispatch); err != nil {
		return fmt.Errorf("%w: hidden dispatch Attempt probe: %w", ErrLoopIntegrity, err)
	}
	if hiddenModel != 0 || hiddenDispatch != 0 {
		return fmt.Errorf("%w: multiple unsettled Attempts", ErrLoopIntegrity)
	}
	if err := requireDisabledRunObservationFamilyEmptyV1(
		ctx, queryer, snapshot.RunID, "ACTION", snapshot.HasActionPort,
	); err != nil {
		return err
	}
	if err := requireDisabledRunObservationFamilyEmptyV1(
		ctx, queryer, snapshot.RunID, "CHANNEL_SEND", snapshot.HasChannelPort,
	); err != nil {
		return err
	}
	if continued.AttemptID == "" &&
		(continued.State == corecontract.WaitingChildrenLoopStep ||
			continued.State == corecontract.WaitingRepairActivationLoopStep) {
		var anyAttempt int
		if err := queryer.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM model_dispatch_attempts WHERE run_id=? LIMIT 1)
			OR EXISTS(SELECT 1 FROM dispatch_attempts WHERE run_id=? LIMIT 1)`,
			snapshot.RunID, snapshot.RunID).Scan(&anyAttempt); err != nil || anyAttempt != 0 {
			return fmt.Errorf("%w: dormant/children Run has Attempt: %v", ErrLoopIntegrity, err)
		}
	}
	return nil
}

func requireDisabledRunObservationFamilyEmptyV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID, kind string,
	enabled bool,
) error {
	if enabled {
		return nil
	}
	var present int
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind=? LIMIT 1)`, runID, kind).Scan(&present); err != nil || present != 0 {
		return fmt.Errorf("%w: disabled %s family present: %v", ErrLoopIntegrity, kind, err)
	}
	return nil
}

func validateRunObservationCanonicalV1(snapshot runObservationCanonicalV1) error {
	if snapshot.SchemaVersion != runObservationSchemaV1 ||
		snapshot.ObservationSequence == 0 || snapshot.ObservationSequence > math.MaxInt64 ||
		!validLeaseOpaqueID(snapshot.StoreInstanceID) ||
		!validLeaseOpaqueID(snapshot.RunID) || !validLeaseOpaqueID(snapshot.TenantID) ||
		!validLeaseOpaqueID(snapshot.WorkspaceID) || !validLeaseOpaqueID(snapshot.AdmissionKey) ||
		!validLeaseOpaqueID(snapshot.MemberID) ||
		!moduleapi.ValidSHA256(snapshot.AdmissionIntentDigest) ||
		!moduleapi.ValidSHA256(snapshot.ManifestDigest) ||
		!moduleapi.ValidSHA256(snapshot.MemberDigest) ||
		!moduleapi.ValidSHA256(snapshot.ContinuationDigest) ||
		!moduleapi.ValidSHA256(snapshot.SourceEventPayloadDigest) ||
		snapshot.ContinuationSizeBytes < 2 ||
		snapshot.ContinuationSizeBytes > corecontract.MaxLoopContinuationCanonicalBytesV1 ||
		snapshot.RunRevision > math.MaxInt64 ||
		snapshot.CreatedAtUnixMicros == 0 || snapshot.CreatedAtUnixMicros > math.MaxInt64 ||
		snapshot.UpdatedAtUnixMicros < snapshot.CreatedAtUnixMicros ||
		snapshot.UpdatedAtUnixMicros > math.MaxInt64 ||
		snapshot.SourceEventSequence > math.MaxInt64 ||
		snapshot.SourceEventFrameRevision > math.MaxInt64 ||
		!validOverviewTextV1(snapshot.RunState) ||
		(snapshot.Disposition != "" && !validOverviewTextV1(snapshot.Disposition)) ||
		!validOverviewTextV1(snapshot.FrameStep) ||
		!validOverviewTextV1(snapshot.UsageLedgerRef) ||
		!validOverviewTextV1(snapshot.SourceEventKind) ||
		(snapshot.PreviousSnapshotDigest != "" &&
			!moduleapi.ValidSHA256(snapshot.PreviousSnapshotDigest)) ||
		(snapshot.CancelRequestRef != "" && !moduleapi.ValidSHA256(snapshot.CancelRequestRef)) ||
		(snapshot.PendingModelAttemptID != "" && !validLeaseOpaqueID(snapshot.PendingModelAttemptID)) ||
		(snapshot.PendingDispatchAttemptID != "" && !validLeaseOpaqueID(snapshot.PendingDispatchAttemptID)) ||
		(snapshot.ContinuationLogicalStepID != "" && !validLeaseOpaqueID(snapshot.ContinuationLogicalStepID)) ||
		(snapshot.ContinuationAttemptID != "" && !validLeaseOpaqueID(snapshot.ContinuationAttemptID)) ||
		(snapshot.SourceEventAttemptID != "" && !validLeaseOpaqueID(snapshot.SourceEventAttemptID)) ||
		(snapshot.SourceEventLogicalStepID != "" && !validLeaseOpaqueID(snapshot.SourceEventLogicalStepID)) ||
		((snapshot.SourceEventAttemptID == "") != (snapshot.SourceEventLogicalStepID == "")) ||
		(snapshot.WaitingReason != "" && !validOverviewTextV1(snapshot.WaitingReason)) ||
		(snapshot.PendingModelAttemptID != "" && snapshot.PendingDispatchAttemptID != "") {
		return fmt.Errorf("%w: invalid Run observation fields", ErrLoopIntegrity)
	}
	if (snapshot.ObservationSequence == 1) !=
		(snapshot.PreviousSnapshotDigest == "" && snapshot.TransitionKind == runObservationAdmissionV1) {
		return fmt.Errorf("%w: Run observation genesis", ErrLoopIntegrity)
	}
	if _, err := rebuildRunObservationContinuationV1(snapshot); err != nil {
		return err
	}
	switch snapshot.TransitionKind {
	case runObservationAdmissionV1, runObservationModelBeginV1,
		runObservationModelOutcomeV1, runObservationActionBeginV1,
		runObservationActionOutcomeV1, runObservationChannelBeginV1,
		runObservationChannelOutcomeV1, runObservationModelRejectionV1,
		runObservationCoreFailureV1, runObservationStartupRecoveryV1,
		runObservationCompositeTransitionV1, runObservationCancellationV1:
	default:
		return fmt.Errorf("%w: Run observation transition", ErrLoopIntegrity)
	}
	return nil
}

func validateRunObservationAdvanceV1(
	previous *runObservationRecordV1,
	current runObservationRecordV1,
) error {
	snapshot := current.Snapshot
	if previous == nil {
		if snapshot.ObservationSequence != 1 || snapshot.TransitionKind != runObservationAdmissionV1 ||
			snapshot.PreviousSnapshotDigest != "" || snapshot.SourceEventSequence != 0 ||
			snapshot.SourceEventKind != corecontract.RunAdmittedEventKind ||
			snapshot.CancelRequestRef != "" {
			return fmt.Errorf("%w: initial Run observation", ErrLoopIntegrity)
		}
		return nil
	}
	old := previous.Snapshot
	if snapshot.ObservationSequence != old.ObservationSequence+1 ||
		snapshot.PreviousSnapshotDigest != previous.Digest ||
		snapshot.StoreInstanceID != old.StoreInstanceID ||
		snapshot.RunID != old.RunID || snapshot.TenantID != old.TenantID ||
		snapshot.WorkspaceID != old.WorkspaceID || snapshot.AdmissionKey != old.AdmissionKey ||
		snapshot.AdmissionIntentDigest != old.AdmissionIntentDigest ||
		snapshot.ManifestDigest != old.ManifestDigest || snapshot.MemberID != old.MemberID ||
		snapshot.MemberDigest != old.MemberDigest ||
		snapshot.HasActionPort != old.HasActionPort ||
		snapshot.HasChannelPort != old.HasChannelPort ||
		snapshot.CreatedAtUnixMicros != old.CreatedAtUnixMicros {
		return fmt.Errorf("%w: Run observation immutable identity", ErrLoopIntegrity)
	}
	if snapshot.TransitionKind == runObservationCancellationV1 {
		if old.CancelRequestRef != "" || snapshot.CancelRequestRef == "" ||
			snapshot.UpdatedAtUnixMicros < old.UpdatedAtUnixMicros ||
			snapshot.SourceEventSequence != old.SourceEventSequence ||
			snapshot.SourceEventFrameRevision != old.SourceEventFrameRevision ||
			snapshot.SourceEventKind != old.SourceEventKind ||
			snapshot.SourceEventPayloadDigest != old.SourceEventPayloadDigest ||
			snapshot.SourceEventAttemptID != old.SourceEventAttemptID ||
			snapshot.SourceEventLogicalStepID != old.SourceEventLogicalStepID ||
			snapshot.RunState != old.RunState || snapshot.Disposition != old.Disposition ||
			snapshot.RunRevision != old.RunRevision || snapshot.FrameStep != old.FrameStep ||
			snapshot.UsageLedgerRef != old.UsageLedgerRef ||
			snapshot.ContinuationDigest != old.ContinuationDigest ||
			snapshot.ContinuationSizeBytes != old.ContinuationSizeBytes ||
			snapshot.PendingModelAttemptID != old.PendingModelAttemptID ||
			snapshot.PendingDispatchAttemptID != old.PendingDispatchAttemptID ||
			snapshot.WaitingReason != old.WaitingReason {
			return fmt.Errorf("%w: cancellation Run observation delta", ErrLoopIntegrity)
		}
		return nil
	}
	if snapshot.CancelRequestRef != old.CancelRequestRef ||
		snapshot.SourceEventSequence != old.SourceEventSequence+1 ||
		snapshot.SourceEventFrameRevision <= old.SourceEventFrameRevision ||
		snapshot.UpdatedAtUnixMicros < old.UpdatedAtUnixMicros ||
		snapshot.RunRevision < old.RunRevision ||
		snapshot.RunRevision > old.RunRevision+1 {
		return fmt.Errorf("%w: event-backed Run observation delta", ErrLoopIntegrity)
	}
	return validateRunObservationTransitionEventV1(snapshot)
}

func rebuildRunObservationContinuationV1(
	snapshot runObservationCanonicalV1,
) ([]byte, error) {
	var canonical []byte
	var err error
	if snapshot.ContinuationCoreFailureReason != "" {
		canonical, err = corecontract.NewCoreFailureLoopContinuationV1(
			snapshot.ContinuationCoreFailureReason,
		)
	} else {
		canonical, err = corecontract.NewLoopContinuationForAttemptV1(
			snapshot.FrameStep,
			snapshot.ContinuationAttemptKind,
			snapshot.ContinuationLogicalStepID,
			snapshot.ContinuationAttemptID,
		)
	}
	if err != nil || len(canonical) != int(snapshot.ContinuationSizeBytes) ||
		moduleapi.Digest(runContinuationDigestDomainV1, canonical) !=
			snapshot.ContinuationDigest {
		return nil, fmt.Errorf("%w: Run observation continuation semantics: %v", ErrLoopIntegrity, err)
	}
	return canonical, nil
}

func validateRunObservationTransitionEventV1(snapshot runObservationCanonicalV1) error {
	valid := false
	attemptEvent := false
	switch snapshot.TransitionKind {
	case runObservationAdmissionV1:
		valid = snapshot.SourceEventKind == corecontract.RunAdmittedEventKind
	case runObservationModelBeginV1:
		valid = snapshot.SourceEventKind == corecontract.ModelDispatchPendingEventKind
		attemptEvent = true
	case runObservationModelOutcomeV1:
		valid = snapshot.SourceEventKind == corecontract.ModelDispatchTerminalEventKind
		attemptEvent = true
	case runObservationActionBeginV1:
		valid = snapshot.SourceEventKind == actionDispatchPendingEvent
		attemptEvent = true
	case runObservationActionOutcomeV1:
		valid = snapshot.SourceEventKind == actionDispatchTerminalEvent
		attemptEvent = true
	case runObservationChannelBeginV1:
		valid = snapshot.SourceEventKind == channelDispatchPendingEvent
		attemptEvent = true
	case runObservationChannelOutcomeV1:
		valid = snapshot.SourceEventKind == channelDispatchTerminalEvent
		attemptEvent = true
	case runObservationModelRejectionV1:
		valid = snapshot.SourceEventKind == modelActionRejectionEventKind
		attemptEvent = true
	case runObservationCoreFailureV1:
		valid = snapshot.SourceEventKind == corecontract.CoreDeterministicFailureEventKind
	case runObservationStartupRecoveryV1:
		valid = snapshot.SourceEventKind == corecontract.ModelDispatchTerminalEventKind ||
			snapshot.SourceEventKind == actionDispatchTerminalEvent ||
			snapshot.SourceEventKind == channelDispatchTerminalEvent
		attemptEvent = true
	case runObservationCompositeTransitionV1:
		valid = snapshot.SourceEventKind == corecontract.CompositeRepairActivatedEventKind ||
			snapshot.SourceEventKind == corecontract.CompositeRepairSkippedEventKind
	case runObservationCancellationV1:
		valid = true
		attemptEvent = snapshot.SourceEventAttemptID != ""
	}
	if !valid || attemptEvent != (snapshot.SourceEventAttemptID != "") ||
		attemptEvent != (snapshot.SourceEventLogicalStepID != "") {
		return fmt.Errorf("%w: Run observation transition/event", ErrLoopIntegrity)
	}
	if attemptEvent && snapshot.ContinuationAttemptID != "" &&
		snapshot.SourceEventAttemptID != snapshot.ContinuationAttemptID {
		return fmt.Errorf("%w: Run observation event/continuation Attempt", ErrLoopIntegrity)
	}
	return nil
}

func deriveRunObservationEventFactsV1(
	ctx context.Context,
	queryer readQueryerV1,
	snapshot runObservationCanonicalV1,
) (runObservationEventFactsV1, error) {
	var facts runObservationEventFactsV1
	var payloadRef, payloadDigest, kind, mediaType string
	var fromRevision, toRevision, createdAt int64
	var canonical []byte
	err := queryer.QueryRowContext(ctx, `SELECT
		event.from_revision,event.to_revision,event.created_at,
		event.payload_ref,event.payload_digest,content.kind,content.media_type,
		content.canonical_bytes
		FROM run_events AS event
		JOIN content_records AS content ON content.content_digest=event.payload_ref
		WHERE event.run_id=? AND event.event_sequence=?
		  AND event.event_kind=? AND event.to_revision=?`,
		snapshot.RunID, int64(snapshot.SourceEventSequence),
		snapshot.SourceEventKind, int64(snapshot.SourceEventFrameRevision),
	).Scan(&fromRevision, &toRevision, &createdAt, &payloadRef, &payloadDigest,
		&kind, &mediaType, &canonical)
	computed, digestErr := ComputeContentDigest(
		ContentRunEventPayload, admissionJSONMediaType, canonical,
	)
	if err != nil || digestErr != nil || fromRevision < 0 || toRevision < 0 ||
		createdAt <= 0 || payloadRef != payloadDigest ||
		payloadDigest != snapshot.SourceEventPayloadDigest || computed != payloadDigest ||
		kind != string(ContentRunEventPayload) || mediaType != admissionJSONMediaType {
		return facts, fmt.Errorf("%w: Run observation event content: %v/%v", ErrLoopIntegrity, err, digestErr)
	}
	facts.FromRevision = uint64(fromRevision)
	facts.ToRevision = uint64(toRevision)
	facts.CreatedAt = uint64(createdAt)
	switch snapshot.SourceEventKind {
	case corecontract.RunAdmittedEventKind:
		event, err := corecontract.RestoreRunAdmittedEventV1(canonical)
		if err != nil || event.RunID != snapshot.RunID ||
			event.ManifestDigest != snapshot.ManifestDigest ||
			event.MemberSnapshotDigest != snapshot.MemberDigest {
			return facts, fmt.Errorf("%w: admitted observation event: %v", ErrLoopIntegrity, err)
		}
		return facts, nil
	case corecontract.ModelDispatchPendingEventKind,
		corecontract.ModelDispatchTerminalEventKind:
		event, err := corecontract.RestoreModelDispatchEventV1(canonical)
		if err != nil || event.RunID != snapshot.RunID ||
			(snapshot.SourceEventKind == corecontract.ModelDispatchPendingEventKind) !=
				(event.State == corecontract.ModelAttemptPending) {
			return facts, fmt.Errorf("%w: Model observation event: %v", ErrLoopIntegrity, err)
		}
		facts.AttemptID, facts.LogicalStepID, facts.ModelState = event.AttemptID, event.LogicalStepID, event.State
		facts.TransitionOrigin, facts.ModelUsage = event.TransitionOrigin, event.Usage
		facts.ResourceSemanticDigest = event.ResourceSemanticDigest
		return facts, nil
	case actionDispatchPendingEvent, actionDispatchTerminalEvent:
		var event actionDispatchEventV1
		if err := json.Unmarshal(canonical, &event); err != nil {
			return facts, fmt.Errorf("%w: Action observation event: %v", ErrLoopIntegrity, err)
		}
		rebuilt, rebuiltDigest, err := prepareActionDispatchEvent(event)
		if err != nil || !bytes.Equal(rebuilt, canonical) || rebuiltDigest != payloadDigest ||
			event.RunID != snapshot.RunID ||
			(snapshot.SourceEventKind == actionDispatchPendingEvent) !=
				(event.State == ActionDispatchPending) {
			return facts, fmt.Errorf("%w: Action observation event: %v", ErrLoopIntegrity, err)
		}
		facts.AttemptID, facts.LogicalStepID, facts.DispatchState = event.AttemptID, event.LogicalStepID, event.State
		facts.TransitionOrigin, facts.ModelUsage = event.TransitionOrigin, event.SourceModelUsage
		facts.SourceModelAttemptID = event.SourceModelAttemptID
		facts.ResourceSemanticDigest = event.ResourceSemanticDigest
		facts.SourceModelSemanticDigest = event.SourceModelSemanticDigest
		if event.ResultDigest != "" {
			content, contentErr := queryContent(ctx, queryer, event.ResultDigest)
			if contentErr != nil || content.Kind != ContentActionResult ||
				content.Digest != event.ResultDigest {
				return facts, fmt.Errorf("%w: Action observation result: %v", ErrLoopIntegrity, contentErr)
			}
			var result corecontract.ActionResultV1
			if decodeErr := json.Unmarshal(content.CanonicalBytes, &result); decodeErr != nil ||
				result.SchemaVersion != corecontract.ActionResultSchemaVersionV1 ||
				result.Status.Validate() != nil {
				return facts, fmt.Errorf("%w: Action observation result: %v", ErrLoopIntegrity, decodeErr)
			}
			facts.ActionResult = result.Status
		}
		return facts, nil
	case channelDispatchPendingEvent, channelDispatchTerminalEvent:
		var event channelDispatchEventV1
		if err := json.Unmarshal(canonical, &event); err != nil {
			return facts, fmt.Errorf("%w: Channel observation event: %v", ErrLoopIntegrity, err)
		}
		rebuilt, rebuiltDigest, err := prepareChannelDispatchEvent(event)
		if err != nil || !bytes.Equal(rebuilt, canonical) || rebuiltDigest != payloadDigest ||
			event.RunID != snapshot.RunID ||
			(snapshot.SourceEventKind == channelDispatchPendingEvent) !=
				(event.State == DispatchPending) {
			return facts, fmt.Errorf("%w: Channel observation event: %v", ErrLoopIntegrity, err)
		}
		facts.AttemptID, facts.LogicalStepID, facts.DispatchState = event.AttemptID, event.LogicalStepID, event.State
		facts.TransitionOrigin, facts.ModelUsage = event.TransitionOrigin, event.SourceModelUsage
		facts.SourceModelAttemptID = event.SourceModelAttemptID
		facts.ResourceSemanticDigest = event.ResourceSemanticDigest
		facts.SourceModelSemanticDigest = event.SourceModelSemanticDigest
		return facts, nil
	case modelActionRejectionEventKind:
		var event modelActionRejectionEventV1
		if err := json.Unmarshal(canonical, &event); err != nil {
			return facts, fmt.Errorf("%w: rejection observation event: %v", ErrLoopIntegrity, err)
		}
		rebuilt, rebuiltDigest, err := prepareModelActionRejectionEvent(event)
		if err != nil || !bytes.Equal(rebuilt, canonical) || rebuiltDigest != payloadDigest ||
			event.RunID != snapshot.RunID {
			return facts, fmt.Errorf("%w: rejection observation event: %v", ErrLoopIntegrity, err)
		}
		facts.AttemptID, facts.LogicalStepID = event.ModelAttemptID, event.LogicalStepID
		facts.ModelUsage = event.ModelUsage
		facts.ResourceSemanticDigest = event.ModelResourceSemanticDigest
		return facts, nil
	case corecontract.CoreDeterministicFailureEventKind:
		event, err := corecontract.RestoreCoreDeterministicFailureEventV1(canonical)
		if err != nil || event.RunID != snapshot.RunID ||
			event.Reason != snapshot.ContinuationCoreFailureReason {
			return facts, fmt.Errorf("%w: Core failure observation event: %v", ErrLoopIntegrity, err)
		}
		return facts, nil
	case corecontract.CompositeRepairActivatedEventKind:
		event, err := corecontract.RestoreCompositeRepairActivatedEventV1(canonical)
		if err != nil || event.RunID != snapshot.RunID {
			return facts, fmt.Errorf("%w: repair activation observation event: %v", ErrLoopIntegrity, err)
		}
		facts.CompositeKind = corecontract.CompositeRepairActivatedEventKind
		return facts, nil
	case corecontract.CompositeRepairSkippedEventKind:
		event, err := corecontract.RestoreCompositeRepairSkippedEventV1(canonical)
		if err != nil || event.RunID != snapshot.RunID {
			return facts, fmt.Errorf("%w: repair skip observation event: %v", ErrLoopIntegrity, err)
		}
		facts.CompositeKind = corecontract.CompositeRepairSkippedEventKind
		return facts, nil
	default:
		return facts, fmt.Errorf("%w: unsupported Run observation event", ErrLoopIntegrity)
	}
}

func verifyRunObservationEventV1(
	ctx context.Context,
	queryer readQueryerV1,
	snapshot runObservationCanonicalV1,
) error {
	facts, err := deriveRunObservationEventFactsV1(ctx, queryer, snapshot)
	if err != nil || facts.AttemptID != snapshot.SourceEventAttemptID ||
		facts.LogicalStepID != snapshot.SourceEventLogicalStepID {
		return fmt.Errorf("%w: Run observation event facts: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func nullableRunObservationStringV1(value string) any {
	if value == "" {
		return nil
	}
	return value
}
