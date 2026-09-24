package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	overviewResourceSchemaV1                    = "overview-resource-observation/v1"
	overviewResourceDigestDomainV1              = "freeagent.current-store.overview-resource-observation.v1"
	overviewSemanticDigestDomainV1              = "freeagent.current-store.overview-resource-semantic.v1"
	overviewUsageSemanticDigestDomainV1         = "freeagent.current-store.overview-model-usage-semantic.v1"
	overviewMaterializedCanonicalDigestDomainV1 = "freeagent.current-store.learning-materialized-version-canonical.v1"
	overviewResourceMaximumJSONV1               = 32 << 10

	overviewResourceModelV1            = "MODEL"
	overviewResourceActionV1           = "ACTION"
	overviewResourceChannelV1          = "CHANNEL_SEND"
	overviewResourceLearningProposalV1 = "LEARNING_PROPOSAL"
	overviewResourceLearningTaskV1     = "LEARNING_TASK"
	overviewResourceModuleReviewV1     = "MODULE_REVIEW"

	overviewTransitionModelBeginV1          = "MODEL_BEGIN"
	overviewTransitionModelExpiredV1        = "MODEL_EXPIRED_BEFORE_NETWORK"
	overviewTransitionModelOutcomeV1        = "MODEL_OUTCOME"
	overviewTransitionModelActionSourceV1   = "MODEL_ACTION_SOURCE_OUTCOME"
	overviewTransitionModelChannelSourceV1  = "MODEL_CHANNEL_SOURCE_OUTCOME"
	overviewTransitionModelRecoveryV1       = "MODEL_STARTUP_RECOVERY"
	overviewTransitionActionBeginV1         = "ACTION_BEGIN"
	overviewTransitionActionOutcomeV1       = "ACTION_OUTCOME"
	overviewTransitionActionRecoveryV1      = "ACTION_STARTUP_RECOVERY"
	overviewTransitionChannelBeginV1        = "CHANNEL_BEGIN"
	overviewTransitionChannelOutcomeV1      = "CHANNEL_OUTCOME"
	overviewTransitionChannelRecoveryV1     = "CHANNEL_STARTUP_RECOVERY"
	overviewTransitionProposalSubmitV1      = "PROPOSAL_SUBMIT"
	overviewTransitionProposalAdmissionV1   = "PROPOSAL_REVIEW_ADMISSION"
	overviewTransitionProposalFinalizeV1    = "PROPOSAL_REVIEW_FINALIZATION"
	overviewTransitionProposalMaterializeV1 = "PROPOSAL_MATERIALIZATION"
	overviewTransitionTaskCreateV1          = "TASK_CREATE"
	overviewTransitionTaskAdmissionV1       = "TASK_RUN_ADMISSION"
	overviewTransitionTaskFinalizeV1        = "TASK_FINALIZATION"
	overviewTransitionModuleReviewV1        = "MODULE_REVIEW_COMMIT"
)

type overviewRunObservationRefV1 struct {
	RunID    string `json:"run_id"`
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
}

type overviewResourceCanonicalV1 struct {
	SchemaVersion          string                       `json:"schema_version"`
	ResourceKind           string                       `json:"resource_kind"`
	ResourceID             string                       `json:"resource_id"`
	ObservationSequence    uint64                       `json:"observation_sequence"`
	PreviousSnapshotDigest string                       `json:"previous_snapshot_digest,omitempty"`
	TransitionKind         string                       `json:"transition_kind"`
	StoreInstanceID        string                       `json:"store_instance_id"`
	TenantID               string                       `json:"tenant_id"`
	WorkspaceID            string                       `json:"workspace_id,omitempty"`
	SubjectRun             *overviewRunObservationRefV1 `json:"subject_run,omitempty"`
	RelatedRun             *overviewRunObservationRefV1 `json:"related_run,omitempty"`
	CausalRun              *overviewRunObservationRefV1 `json:"causal_run,omitempty"`
	State                  string                       `json:"state"`
	BindingTargetKind      string                       `json:"binding_target_kind,omitempty"`
	ResourceRevision       uint64                       `json:"resource_revision"`
	CreatedAtUnixMicros    uint64                       `json:"created_at_unix_micros"`
	UpdatedAtUnixMicros    uint64                       `json:"updated_at_unix_micros"`
	SemanticDigest         string                       `json:"semantic_digest"`

	UsageRevision       *uint64 `json:"usage_revision,omitempty"`
	UsageLedgerSequence *uint64 `json:"usage_ledger_sequence,omitempty"`
	UsageSemanticDigest string  `json:"usage_semantic_digest,omitempty"`
	UsageStatus         string  `json:"usage_status,omitempty"`
	InputTokens         *uint64 `json:"input_tokens"`
	CachedInputTokens   *uint64 `json:"cached_input_tokens"`
	UncachedInputTokens *uint64 `json:"uncached_input_tokens"`
	OutputTokens        *uint64 `json:"output_tokens"`
	ReasoningTokens     *uint64 `json:"reasoning_tokens"`

	ProposalKind string `json:"proposal_kind,omitempty"`

	MaterializedVersionCanonicalDigest string  `json:"materialized_version_canonical_digest,omitempty"`
	MaterializedVersionID              string  `json:"materialized_version_id,omitempty"`
	MaterializedArtifactDigest         string  `json:"materialized_artifact_digest,omitempty"`
	MaterializedArtifactSizeBytes      *uint64 `json:"materialized_artifact_size_bytes,omitempty"`
	ApprovalVerdictDigest              string  `json:"approval_verdict_digest,omitempty"`
	MaterializedAtUnixMicros           *uint64 `json:"materialized_at_unix_micros,omitempty"`

	CandidateID           string `json:"candidate_id,omitempty"`
	CurrentInstanceID     string `json:"current_instance_id,omitempty"`
	TargetInstanceID      string `json:"target_instance_id,omitempty"`
	CurrentModuleID       string `json:"current_module_id,omitempty"`
	CurrentExactVersion   string `json:"current_exact_version,omitempty"`
	CurrentArtifactDigest string `json:"current_artifact_digest,omitempty"`
	TargetModuleID        string `json:"target_module_id,omitempty"`
	TargetExactVersion    string `json:"target_exact_version,omitempty"`
	TargetArtifactDigest  string `json:"target_artifact_digest,omitempty"`
}

type overviewResourceRecordV1 struct {
	Snapshot  overviewResourceCanonicalV1
	Digest    string
	Canonical []byte
}

type overviewProposalSemanticV1 struct {
	Proposal        LearningProposalRecord                    `json:"proposal"`
	Materialization overviewProposalMaterializationSemanticV1 `json:"materialization"`
}

type overviewProposalMaterializationSemanticV1 struct {
	Present                bool   `json:"present"`
	VersionCanonicalDigest string `json:"version_canonical_digest,omitempty"`
	VersionSizeBytes       uint64 `json:"version_size_bytes,omitempty"`
	VersionID              string `json:"version_id,omitempty"`
	ArtifactDigest         string `json:"artifact_digest,omitempty"`
	ArtifactSizeBytes      uint64 `json:"artifact_size_bytes,omitempty"`
	ApprovalVerdictDigest  string `json:"approval_verdict_digest,omitempty"`
	MaterializedAt         int64  `json:"materialized_at,omitempty"`
}

type overviewModelUsageSemanticV1 struct {
	AttemptID      string                   `json:"attempt_id"`
	RunID          string                   `json:"run_id"`
	LedgerSequence *uint64                  `json:"ledger_sequence,omitempty"`
	Revision       uint64                   `json:"revision"`
	Tokens         corecontract.UsageTokens `json:"tokens"`
	UsageStatus    string                   `json:"usage_status"`
	RawReceiptRef  string                   `json:"raw_receipt_ref,omitempty"`
}

func modelUsageSemanticDigestV1(usage ModelUsageRecord) (string, error) {
	projection := overviewModelUsageSemanticV1{
		AttemptID: usage.AttemptID, RunID: usage.RunID,
		LedgerSequence: cloneModelUint(usage.LedgerSequence), Revision: usage.Revision,
		Tokens:      usage.Tokens.Clone(),
		UsageStatus: usage.UsageStatus, RawReceiptRef: usage.RawReceiptRef,
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(raw, moduleapi.CanonicalJSONLimits{
		MaxBytes: overviewResourceMaximumJSONV1, MaxDepth: 5, MaxNodes: 64,
	})
	if err != nil {
		return "", fmt.Errorf("%w: Model Usage semantic canonical: %v", ErrLoopIntegrity, err)
	}
	return moduleapi.Digest(overviewUsageSemanticDigestDomainV1, canonical), nil
}

func modelUsageEventV1(usage ModelUsageRecord) (*corecontract.ModelUsageEventV1, error) {
	digest, err := modelUsageSemanticDigestV1(usage)
	if err != nil {
		return nil, err
	}
	event := &corecontract.ModelUsageEventV1{
		Revision: usage.Revision, LedgerSequence: cloneModelUint(usage.LedgerSequence),
		UsageStatus: usage.UsageStatus,
		Tokens:      usage.Tokens.Clone(), SemanticDigest: digest,
	}
	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("%w: Model Usage event: %v", ErrLoopIntegrity, err)
	}
	return event, nil
}

func appendOverviewResourceObservationV1(
	ctx context.Context,
	q runObservationWriterV1,
	kind, resourceID, transition string,
) error {
	if ctx == nil || q == nil || !validOverviewResourceKindV1(kind) ||
		!validLeaseOpaqueID(resourceID) || !validOverviewResourceTransitionV1(kind, transition) {
		return fmt.Errorf("%w: invalid Overview resource observation append", ErrLoopIntegrity)
	}
	current, err := loadCurrentOverviewResourceV1(ctx, q, kind, resourceID)
	if err != nil {
		return err
	}
	current.Snapshot.SchemaVersion = overviewResourceSchemaV1
	current.Snapshot.TransitionKind = transition
	if err := q.QueryRowContext(ctx, `SELECT store_instance_id FROM store_meta
		WHERE singleton=1`).Scan(&current.Snapshot.StoreInstanceID); err != nil {
		return fmt.Errorf("%w: Overview resource Store identity: %v", ErrLoopIntegrity, err)
	}
	var previousSequence int64
	var previousDigest string
	err = q.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest
		FROM overview_resource_heads WHERE resource_kind=? AND resource_id=?`,
		kind, resourceID).Scan(&previousSequence, &previousDigest)
	initial := errors.Is(err, sql.ErrNoRows)
	if err != nil && !initial {
		return fmt.Errorf("currentstore: load Overview resource head: %w", err)
	}
	if initial {
		if current.Snapshot.ObservationSequence != 1 {
			return fmt.Errorf("%w: Overview resource genesis sequence", ErrLoopIntegrity)
		}
	} else {
		if previousSequence <= 0 || current.Snapshot.ObservationSequence != uint64(previousSequence+1) ||
			!moduleapi.ValidSHA256(previousDigest) {
			return fmt.Errorf("%w: Overview resource sequence", ErrLoopIntegrity)
		}
		current.Snapshot.PreviousSnapshotDigest = previousDigest
		var orphan int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM overview_resource_snapshots
			WHERE resource_kind=? AND resource_id=? AND observation_sequence>? LIMIT 1
		)`, kind, resourceID, previousSequence).Scan(&orphan); err != nil || orphan != 0 {
			return fmt.Errorf("%w: Overview resource orphan tail: %v", ErrLoopIntegrity, err)
		}
	}
	if err := validateOverviewResourceCanonicalV1(current.Snapshot); err != nil {
		return err
	}
	current.Canonical, current.Digest, err = canonicalOverviewResourceV1(current.Snapshot)
	if err != nil {
		return err
	}
	s := current.Snapshot
	_, err = q.ExecContext(ctx, `INSERT INTO overview_resource_snapshots(
		snapshot_digest,resource_kind,resource_id,observation_sequence,
		previous_snapshot_digest,transition_kind,store_instance_id,tenant_id,
		workspace_id,subject_run_id,subject_run_observation_sequence,
		subject_run_observation_digest,related_run_id,
		related_run_observation_sequence,related_run_observation_digest,
		causal_run_id,causal_run_observation_sequence,causal_run_observation_digest,
		state,binding_target_kind,resource_revision,created_at,updated_at,
		semantic_digest,usage_revision,usage_ledger_sequence,usage_semantic_digest,usage_status,input_tokens,cached_input_tokens,
		uncached_input_tokens,output_tokens,reasoning_tokens,canonical_json
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		current.Digest, s.ResourceKind, s.ResourceID, int64(s.ObservationSequence),
		nullableRunObservationStringV1(s.PreviousSnapshotDigest), s.TransitionKind,
		s.StoreInstanceID, s.TenantID, nullableRunObservationStringV1(s.WorkspaceID),
		overviewRunRefIDV1(s.SubjectRun), overviewRunRefSequenceV1(s.SubjectRun),
		overviewRunRefDigestV1(s.SubjectRun), overviewRunRefIDV1(s.RelatedRun),
		overviewRunRefSequenceV1(s.RelatedRun), overviewRunRefDigestV1(s.RelatedRun),
		overviewRunRefIDV1(s.CausalRun), overviewRunRefSequenceV1(s.CausalRun),
		overviewRunRefDigestV1(s.CausalRun), s.State,
		nullableRunObservationStringV1(s.BindingTargetKind), int64(s.ResourceRevision),
		int64(s.CreatedAtUnixMicros), int64(s.UpdatedAtUnixMicros), s.SemanticDigest,
		nullableOverviewUintV1(s.UsageRevision), nullableOverviewUintV1(s.UsageLedgerSequence),
		nullableRunObservationStringV1(s.UsageSemanticDigest),
		nullableRunObservationStringV1(s.UsageStatus),
		nullableOverviewUintV1(s.InputTokens), nullableOverviewUintV1(s.CachedInputTokens),
		nullableOverviewUintV1(s.UncachedInputTokens), nullableOverviewUintV1(s.OutputTokens),
		nullableOverviewUintV1(s.ReasoningTokens), current.Canonical,
	)
	if err != nil {
		return fmt.Errorf("currentstore: append Overview resource snapshot: %w", err)
	}
	if initial {
		_, err = q.ExecContext(ctx, `INSERT INTO overview_resource_heads(
			resource_kind,resource_id,observation_sequence,snapshot_digest,tenant_id,
			workspace_id,subject_run_id,state,binding_target_kind,resource_revision,
			created_at,updated_at,usage_revision,usage_ledger_sequence,usage_status,input_tokens,
			cached_input_tokens,uncached_input_tokens,output_tokens,reasoning_tokens
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.ResourceKind, s.ResourceID, int64(s.ObservationSequence), current.Digest,
			s.TenantID, nullableRunObservationStringV1(s.WorkspaceID),
			overviewRunRefIDV1(s.SubjectRun), s.State,
			nullableRunObservationStringV1(s.BindingTargetKind), int64(s.ResourceRevision),
			int64(s.CreatedAtUnixMicros), int64(s.UpdatedAtUnixMicros),
			nullableOverviewUintV1(s.UsageRevision), nullableOverviewUintV1(s.UsageLedgerSequence),
			nullableRunObservationStringV1(s.UsageStatus),
			nullableOverviewUintV1(s.InputTokens), nullableOverviewUintV1(s.CachedInputTokens),
			nullableOverviewUintV1(s.UncachedInputTokens), nullableOverviewUintV1(s.OutputTokens),
			nullableOverviewUintV1(s.ReasoningTokens),
		)
	} else {
		var result sql.Result
		result, err = q.ExecContext(ctx, `UPDATE overview_resource_heads SET
			observation_sequence=?,snapshot_digest=?,tenant_id=?,workspace_id=?,
			subject_run_id=?,state=?,binding_target_kind=?,resource_revision=?,
			created_at=?,updated_at=?,usage_revision=?,usage_ledger_sequence=?,usage_status=?,input_tokens=?,
			cached_input_tokens=?,uncached_input_tokens=?,output_tokens=?,reasoning_tokens=?
			WHERE resource_kind=? AND resource_id=? AND observation_sequence=?
			  AND snapshot_digest=?`, int64(s.ObservationSequence), current.Digest,
			s.TenantID, nullableRunObservationStringV1(s.WorkspaceID),
			overviewRunRefIDV1(s.SubjectRun), s.State,
			nullableRunObservationStringV1(s.BindingTargetKind), int64(s.ResourceRevision),
			int64(s.CreatedAtUnixMicros), int64(s.UpdatedAtUnixMicros),
			nullableOverviewUintV1(s.UsageRevision), nullableOverviewUintV1(s.UsageLedgerSequence),
			nullableRunObservationStringV1(s.UsageStatus),
			nullableOverviewUintV1(s.InputTokens), nullableOverviewUintV1(s.CachedInputTokens),
			nullableOverviewUintV1(s.UncachedInputTokens), nullableOverviewUintV1(s.OutputTokens),
			nullableOverviewUintV1(s.ReasoningTokens), s.ResourceKind, s.ResourceID,
			previousSequence, previousDigest)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				err = fmt.Errorf("Overview resource head CAS affected %d rows", affected)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("currentstore: advance Overview resource head: %w", err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO overview_resource_transition_carriers(
		resource_kind,resource_id,observation_sequence,snapshot_digest,store_instance_id
	) VALUES(?,?,?,?,?)`, s.ResourceKind, s.ResourceID, int64(s.ObservationSequence),
		current.Digest, s.StoreInstanceID); err != nil {
		return fmt.Errorf("currentstore: append Overview resource transition carrier: %w", err)
	}
	return nil
}

func loadCurrentOverviewResourceV1(
	ctx context.Context,
	q readQueryerV1,
	kind, resourceID string,
) (overviewResourceRecordV1, error) {
	var out overviewResourceRecordV1
	out.Snapshot.ResourceKind = kind
	out.Snapshot.ResourceID = resourceID
	var semantic any
	switch kind {
	case overviewResourceModelV1:
		record, err := queryModelDispatchRecord(ctx, q, resourceID)
		if err != nil {
			return out, err
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
			FROM model_dispatch_attempts WHERE attempt_id=?`, resourceID).Scan(&sequence); err != nil {
			return out, err
		}
		var usageSequence int64
		if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
			FROM model_usage WHERE attempt_id=?`, resourceID).Scan(&usageSequence); err != nil ||
			usageSequence != sequence {
			return out, fmt.Errorf("%w: Model/Usage observation sequence: %v", ErrModelDispatchIntegrity, err)
		}
		out.Snapshot.ObservationSequence = uint64(sequence)
		out.Snapshot.TenantID = record.Attempt.TenantID
		out.Snapshot.WorkspaceID = record.Attempt.WorkspaceID
		out.Snapshot.State = string(record.Attempt.State)
		out.Snapshot.ResourceRevision = record.Attempt.Revision
		out.Snapshot.CreatedAtUnixMicros = uint64(record.Attempt.CreatedAt.UnixMicro())
		out.Snapshot.UpdatedAtUnixMicros = uint64(record.Attempt.UpdatedAt.UnixMicro())
		usageRevision := record.Usage.Revision
		out.Snapshot.UsageRevision = &usageRevision
		out.Snapshot.UsageLedgerSequence = cloneModelUint(record.Usage.LedgerSequence)
		out.Snapshot.UsageSemanticDigest, err = modelUsageSemanticDigestV1(record.Usage)
		if err != nil {
			return out, err
		}
		out.Snapshot.UsageStatus = record.Usage.UsageStatus
		out.Snapshot.InputTokens = cloneOverviewTokenV1(record.Usage.Tokens.Input)
		out.Snapshot.CachedInputTokens = cloneOverviewTokenV1(record.Usage.Tokens.CachedInput)
		out.Snapshot.UncachedInputTokens = cloneOverviewTokenV1(record.Usage.Tokens.UncachedInput)
		out.Snapshot.OutputTokens = cloneOverviewTokenV1(record.Usage.Tokens.Output)
		out.Snapshot.ReasoningTokens = cloneOverviewTokenV1(record.Usage.Tokens.Reasoning)
		if err := attachOverviewResourceRunRefsV1(ctx, q, &out.Snapshot,
			record.Attempt.RunID, "", record.Attempt.RunID); err != nil {
			return out, err
		}
		semantic = record
	case overviewResourceActionV1:
		record, err := queryActionDispatchRecord(ctx, q, resourceID)
		if err != nil {
			return out, err
		}
		if err := populateOverviewDispatchResourceV1(ctx, q, &out.Snapshot,
			record.Attempt.RunID, record.Attempt.TenantID, record.Attempt.WorkspaceID,
			string(record.Attempt.State), record.Attempt.Revision,
			record.Attempt.CreatedAt, record.Attempt.UpdatedAt); err != nil {
			return out, err
		}
		semantic = record
	case overviewResourceChannelV1:
		record, err := queryChannelDispatchRecord(ctx, q, resourceID)
		if err != nil {
			return out, err
		}
		if err := populateOverviewDispatchResourceV1(ctx, q, &out.Snapshot,
			record.Attempt.RunID, record.Attempt.TenantID, record.Attempt.WorkspaceID,
			string(record.Attempt.State), record.Attempt.Revision,
			record.Attempt.CreatedAt, record.Attempt.UpdatedAt); err != nil {
			return out, err
		}
		semantic = record
	case overviewResourceLearningProposalV1:
		record, found, err := queryLearningProposalByID(ctx, q, resourceID)
		if err != nil || !found {
			return out, errors.Join(err, ErrLearningProposalIntegrity)
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
			FROM learning_proposals WHERE proposal_id=?`, resourceID).Scan(&sequence); err != nil {
			return out, err
		}
		out.Snapshot.ObservationSequence = uint64(sequence)
		out.Snapshot.TenantID = record.Proposal.TenantID
		out.Snapshot.WorkspaceID = record.Proposal.Workspace.ID
		out.Snapshot.State = string(record.State)
		out.Snapshot.ResourceRevision = record.Revision
		out.Snapshot.CreatedAtUnixMicros = uint64(record.CreatedAt.UnixMicro())
		out.Snapshot.UpdatedAtUnixMicros = uint64(record.UpdatedAt.UnixMicro())
		out.Snapshot.ProposalKind = string(record.Proposal.Kind)
		materialization, err := loadOverviewProposalMaterializationSemanticV1(
			ctx, q, record.ProposalID,
		)
		if err != nil {
			return out, err
		}
		if materialization.Present {
			artifactSize := materialization.ArtifactSizeBytes
			materializedAt := uint64(materialization.MaterializedAt)
			out.Snapshot.MaterializedVersionCanonicalDigest = materialization.VersionCanonicalDigest
			out.Snapshot.MaterializedVersionID = materialization.VersionID
			out.Snapshot.MaterializedArtifactDigest = materialization.ArtifactDigest
			out.Snapshot.MaterializedArtifactSizeBytes = &artifactSize
			out.Snapshot.ApprovalVerdictDigest = materialization.ApprovalVerdictDigest
			out.Snapshot.MaterializedAtUnixMicros = &materializedAt
		}
		causal := record.Proposal.ProposerRunID
		if record.ReviewRunID != "" {
			causal = record.ReviewRunID
		}
		if err := attachOverviewResourceRunRefsV1(ctx, q, &out.Snapshot,
			record.Proposal.ProposerRunID, record.ReviewRunID, causal); err != nil {
			return out, err
		}
		semantic = overviewProposalSemanticV1{Proposal: record, Materialization: materialization}
	case overviewResourceLearningTaskV1:
		var tenantID string
		if err := q.QueryRowContext(ctx, `SELECT tenant_id FROM learning_cycle_tasks
			WHERE task_id=?`, resourceID).Scan(&tenantID); err != nil {
			return out, err
		}
		record, found, err := queryLearningCycleTask(ctx, q, tenantID, resourceID)
		if err != nil || !found {
			return out, errors.Join(err, ErrLearningCycleIntegrity)
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
			FROM learning_cycle_tasks WHERE task_id=?`, resourceID).Scan(&sequence); err != nil {
			return out, err
		}
		out.Snapshot.ObservationSequence = uint64(sequence)
		out.Snapshot.TenantID, out.Snapshot.WorkspaceID = record.TenantID, record.WorkspaceID
		out.Snapshot.State = string(record.State)
		out.Snapshot.ResourceRevision = record.Revision
		out.Snapshot.CreatedAtUnixMicros = uint64(record.CreatedAt.UnixMicro())
		out.Snapshot.UpdatedAtUnixMicros = uint64(record.UpdatedAt.UnixMicro())
		if record.RunID != "" {
			if err := attachOverviewResourceRunRefsV1(ctx, q, &out.Snapshot,
				record.RunID, "", record.RunID); err != nil {
				return out, err
			}
		}
		semantic = record
	case overviewResourceModuleReviewV1:
		record, found, err := queryModuleUpgradeReview(ctx, q, resourceID)
		if err != nil || !found {
			return out, errors.Join(err, ErrModuleUpgradeIntegrity)
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
			FROM module_upgrade_reviews WHERE review_id=?`, resourceID).Scan(&sequence); err != nil {
			return out, err
		}
		out.Snapshot.ObservationSequence = uint64(sequence)
		out.Snapshot.TenantID = record.Review.TenantID
		out.Snapshot.WorkspaceID = record.Review.BindingTarget.WorkspaceID
		out.Snapshot.BindingTargetKind = string(record.Review.BindingTarget.Kind)
		out.Snapshot.State = string(record.Review.Conclusion)
		out.Snapshot.CreatedAtUnixMicros = uint64(record.CreatedAt.UnixMicro())
		out.Snapshot.UpdatedAtUnixMicros = out.Snapshot.CreatedAtUnixMicros
		out.Snapshot.CandidateID = record.Candidate.CandidateID
		out.Snapshot.CurrentInstanceID = record.Review.Current.Activation.InstanceID
		out.Snapshot.TargetInstanceID = record.Review.TargetInstanceID
		out.Snapshot.CurrentModuleID = record.Candidate.Candidate.Current.Module.ID
		out.Snapshot.CurrentExactVersion = record.Candidate.Candidate.Current.Module.Version
		out.Snapshot.CurrentArtifactDigest = record.Candidate.Candidate.Current.ArtifactDigest
		out.Snapshot.TargetModuleID = record.Candidate.Candidate.Target.Module.ID
		out.Snapshot.TargetExactVersion = record.Candidate.Candidate.Target.Module.Version
		out.Snapshot.TargetArtifactDigest = record.Candidate.Candidate.Target.ArtifactDigest
		semantic = record
	default:
		return out, fmt.Errorf("%w: unsupported Overview resource kind", ErrLoopIntegrity)
	}
	semanticDigest, err := overviewResourceSemanticDigestV1(kind, semantic)
	if err != nil {
		return out, err
	}
	out.Snapshot.SemanticDigest = semanticDigest
	return out, nil
}

func overviewResourceSemanticDigestV1(kind string, semantic any) (string, error) {
	raw, err := json.Marshal(semantic)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(overviewSemanticDigestDomainV1+"\x00"+kind, raw), nil
}

func loadOverviewProposalMaterializationSemanticV1(
	ctx context.Context,
	q readQueryerV1,
	proposalID string,
) (overviewProposalMaterializationSemanticV1, error) {
	var out overviewProposalMaterializationSemanticV1
	var size, canonicalLength, artifactSize, materializedAt sql.NullInt64
	var storedCanonicalDigest, versionID, artifactDigest, verdictDigest sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT version_size_bytes,length(version_canonical),
		version_canonical_digest,version_id,artifact_digest,artifact_size_bytes,approval_verdict_digest,
		materialized_at FROM learning_proposals WHERE proposal_id=?`, proposalID).Scan(
		&size, &canonicalLength, &storedCanonicalDigest, &versionID, &artifactDigest, &artifactSize,
		&verdictDigest, &materializedAt,
	); err != nil {
		return out, fmt.Errorf("%w: read Proposal materialization observation: %v",
			ErrLearningMaterializationIntegrity, err)
	}
	present := canonicalLength.Valid || size.Valid || storedCanonicalDigest.Valid || versionID.Valid || artifactDigest.Valid ||
		artifactSize.Valid || verdictDigest.Valid || materializedAt.Valid
	if !present {
		return out, nil
	}
	if !canonicalLength.Valid || !size.Valid || !storedCanonicalDigest.Valid || !versionID.Valid || !artifactDigest.Valid ||
		!artifactSize.Valid || !verdictDigest.Valid || !materializedAt.Valid ||
		size.Int64 <= 0 || size.Int64 > learningcontract.MaxMaterializedVersionWireBytesV1 ||
		canonicalLength.Int64 != size.Int64 ||
		artifactSize.Int64 <= 0 || materializedAt.Int64 <= 0 ||
		!moduleapi.ValidSHA256(storedCanonicalDigest.String) ||
		!moduleapi.ValidSHA256(versionID.String) ||
		!moduleapi.ValidSHA256(artifactDigest.String) ||
		!moduleapi.ValidSHA256(verdictDigest.String) {
		return out, fmt.Errorf("%w: invalid Proposal materialization observation",
			ErrLearningMaterializationIntegrity)
	}
	var canonical []byte
	if err := q.QueryRowContext(ctx, `SELECT version_canonical FROM learning_proposals
		WHERE proposal_id=? AND length(version_canonical)=?`, proposalID, size.Int64).Scan(&canonical); err != nil ||
		len(canonical) != int(size.Int64) {
		return out, fmt.Errorf("%w: read bounded Proposal materialization canonical: %v",
			ErrLearningMaterializationIntegrity, err)
	}
	canonicalDigest := moduleapi.Digest(overviewMaterializedCanonicalDigestDomainV1, canonical)
	if storedCanonicalDigest.String != canonicalDigest {
		return out, fmt.Errorf("%w: Proposal materialization canonical digest",
			ErrLearningMaterializationIntegrity)
	}
	out = overviewProposalMaterializationSemanticV1{
		Present: true, VersionCanonicalDigest: canonicalDigest,
		VersionSizeBytes: uint64(size.Int64), VersionID: versionID.String,
		ArtifactDigest: artifactDigest.String, ArtifactSizeBytes: uint64(artifactSize.Int64),
		ApprovalVerdictDigest: verdictDigest.String, MaterializedAt: materializedAt.Int64,
	}
	return out, nil
}

func populateOverviewDispatchResourceV1(ctx context.Context, q readQueryerV1,
	s *overviewResourceCanonicalV1, runID, tenantID, workspaceID, state string,
	revision uint64, createdAt, updatedAt time.Time) error {
	var sequence int64
	if err := q.QueryRowContext(ctx, `SELECT overview_observation_sequence
		FROM dispatch_attempts WHERE dispatch_kind=? AND attempt_id=?`,
		s.ResourceKind, s.ResourceID).Scan(&sequence); err != nil {
		return err
	}
	s.ObservationSequence = uint64(sequence)
	s.TenantID, s.WorkspaceID, s.State = tenantID, workspaceID, state
	s.ResourceRevision = revision
	s.CreatedAtUnixMicros, s.UpdatedAtUnixMicros = uint64(createdAt.UnixMicro()), uint64(updatedAt.UnixMicro())
	return attachOverviewResourceRunRefsV1(ctx, q, s, runID, "", runID)
}

func attachOverviewResourceRunRefsV1(ctx context.Context, q readQueryerV1,
	s *overviewResourceCanonicalV1, subjectID, relatedID, causalID string) error {
	load := func(runID string) (*overviewRunObservationRefV1, error) {
		if runID == "" {
			return nil, nil
		}
		var sequence int64
		var digest, tenantID, workspaceID string
		if err := q.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest,
			tenant_id,workspace_id FROM run_observation_heads WHERE run_id=?`, runID).Scan(
			&sequence, &digest, &tenantID, &workspaceID); err != nil || sequence <= 0 ||
			tenantID != s.TenantID || workspaceID != s.WorkspaceID {
			return nil, fmt.Errorf("%w: Overview resource Run ref: %v", ErrLoopIntegrity, err)
		}
		return &overviewRunObservationRefV1{RunID: runID, Sequence: uint64(sequence), Digest: digest}, nil
	}
	var err error
	if s.SubjectRun, err = load(subjectID); err != nil {
		return err
	}
	if s.RelatedRun, err = load(relatedID); err != nil {
		return err
	}
	if s.CausalRun, err = load(causalID); err != nil {
		return err
	}
	return nil
}

func canonicalOverviewResourceV1(s overviewResourceCanonicalV1) ([]byte, string, error) {
	if err := validateOverviewResourceCanonicalV1(s); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(raw, moduleapi.CanonicalJSONLimits{
		MaxBytes: overviewResourceMaximumJSONV1, MaxDepth: 6, MaxNodes: 128,
	})
	if err != nil || len(canonical) > overviewResourceMaximumJSONV1 {
		return nil, "", fmt.Errorf("%w: Overview resource canonical: %v", ErrLoopIntegrity, err)
	}
	return canonical, moduleapi.Digest(overviewResourceDigestDomainV1, canonical), nil
}

func validateOverviewResourceCanonicalV1(s overviewResourceCanonicalV1) error {
	if s.SchemaVersion != overviewResourceSchemaV1 || !validOverviewResourceKindV1(s.ResourceKind) ||
		!validOverviewResourceTransitionV1(s.ResourceKind, s.TransitionKind) ||
		!validLeaseOpaqueID(s.ResourceID) || !validLeaseOpaqueID(s.StoreInstanceID) ||
		!validLeaseOpaqueID(s.TenantID) || (s.WorkspaceID != "" && !validLeaseOpaqueID(s.WorkspaceID)) ||
		s.ObservationSequence == 0 || s.ObservationSequence > math.MaxInt64 ||
		s.ResourceRevision > math.MaxInt64 || s.CreatedAtUnixMicros == 0 ||
		s.CreatedAtUnixMicros > math.MaxInt64 || s.UpdatedAtUnixMicros < s.CreatedAtUnixMicros ||
		s.UpdatedAtUnixMicros > math.MaxInt64 || !validOverviewTextV1(s.State) ||
		!moduleapi.ValidSHA256(s.SemanticDigest) ||
		(s.PreviousSnapshotDigest != "" && !moduleapi.ValidSHA256(s.PreviousSnapshotDigest)) ||
		(s.ObservationSequence == 1) != (s.PreviousSnapshotDigest == "") {
		return fmt.Errorf("%w: invalid Overview resource canonical", ErrLoopIntegrity)
	}
	for _, ref := range []*overviewRunObservationRefV1{s.SubjectRun, s.RelatedRun, s.CausalRun} {
		if ref != nil && (!validLeaseOpaqueID(ref.RunID) || ref.Sequence == 0 ||
			ref.Sequence > math.MaxInt64 || !moduleapi.ValidSHA256(ref.Digest)) {
			return fmt.Errorf("%w: invalid Overview resource Run ref", ErrLoopIntegrity)
		}
	}
	sameRunRef := func(left, right *overviewRunObservationRefV1) bool {
		return left != nil && right != nil && left.RunID == right.RunID &&
			left.Sequence == right.Sequence && left.Digest == right.Digest
	}
	if s.ResourceKind != overviewResourceModuleReviewV1 && s.WorkspaceID == "" {
		return fmt.Errorf("%w: missing Overview resource Workspace", ErrLoopIntegrity)
	}
	switch s.ResourceKind {
	case overviewResourceModelV1, overviewResourceActionV1, overviewResourceChannelV1:
		if !sameRunRef(s.SubjectRun, s.CausalRun) || s.RelatedRun != nil {
			return fmt.Errorf("%w: invalid dispatch resource Run refs", ErrLoopIntegrity)
		}
	case overviewResourceLearningProposalV1:
		if s.SubjectRun == nil || (s.ProposalKind != "KNOWLEDGE" && s.ProposalKind != "SKILL") {
			return fmt.Errorf("%w: invalid Proposal observation identity", ErrLoopIntegrity)
		}
		if s.TransitionKind == overviewTransitionProposalSubmitV1 {
			if s.RelatedRun != nil || !sameRunRef(s.SubjectRun, s.CausalRun) {
				return fmt.Errorf("%w: invalid Proposal submit Run refs", ErrLoopIntegrity)
			}
		} else if !sameRunRef(s.RelatedRun, s.CausalRun) {
			return fmt.Errorf("%w: invalid Proposal review Run refs", ErrLoopIntegrity)
		}
	case overviewResourceLearningTaskV1:
		if s.TransitionKind == overviewTransitionTaskCreateV1 {
			if s.SubjectRun != nil || s.RelatedRun != nil || s.CausalRun != nil {
				return fmt.Errorf("%w: invalid Task genesis Run refs", ErrLoopIntegrity)
			}
		} else if !sameRunRef(s.SubjectRun, s.CausalRun) || s.RelatedRun != nil {
			return fmt.Errorf("%w: invalid Task Run refs", ErrLoopIntegrity)
		}
	case overviewResourceModuleReviewV1:
		if s.SubjectRun != nil || s.RelatedRun != nil || s.CausalRun != nil ||
			(s.BindingTargetKind != "PROFILE" &&
				s.BindingTargetKind != "WORKSPACE_CHANNEL_ENDPOINT") ||
			(s.BindingTargetKind == "PROFILE") != (s.WorkspaceID == "") ||
			!moduleapi.ValidSHA256(s.ResourceID) || !moduleapi.ValidSHA256(s.CandidateID) ||
			!validLeaseOpaqueID(s.CurrentInstanceID) || !validLeaseOpaqueID(s.TargetInstanceID) ||
			s.CurrentInstanceID == s.TargetInstanceID ||
			!validLeaseOpaqueID(s.CurrentModuleID) || s.CurrentModuleID != s.TargetModuleID ||
			!validOverviewTextV1(s.CurrentExactVersion) || !validOverviewTextV1(s.TargetExactVersion) ||
			s.CurrentExactVersion == s.TargetExactVersion ||
			!moduleapi.ValidSHA256(s.CurrentArtifactDigest) ||
			!moduleapi.ValidSHA256(s.TargetArtifactDigest) ||
			s.CurrentArtifactDigest == s.TargetArtifactDigest ||
			s.ResourceRevision != 0 || s.ObservationSequence != 1 {
			return fmt.Errorf("%w: invalid Module Review observation", ErrLoopIntegrity)
		}
	}
	if s.ResourceKind == overviewResourceModelV1 {
		if s.UsageRevision == nil || !moduleapi.ValidSHA256(s.UsageSemanticDigest) ||
			(s.UsageLedgerSequence != nil &&
				(*s.UsageLedgerSequence == 0 || *s.UsageLedgerSequence > math.MaxInt64)) ||
			!validOverviewUsageStatusStoreV1(s.UsageStatus) ||
			!validOverviewUsageTokensStoreV1(corecontract.UsageTokens{
				Input: s.InputTokens, CachedInput: s.CachedInputTokens,
				UncachedInput: s.UncachedInputTokens, Output: s.OutputTokens,
				Reasoning: s.ReasoningTokens,
			}) {
			return fmt.Errorf("%w: invalid Overview Model Usage", ErrLoopIntegrity)
		}
		if s.TransitionKind == overviewTransitionModelExpiredV1 &&
			(s.ResourceRevision != 0 || s.State != string(corecontract.ModelAttemptFailed) ||
				s.UsageRevision == nil || *s.UsageRevision != 0 ||
				s.UsageLedgerSequence != nil ||
				s.UsageStatus != "NO_USAGE_REPORTED" || s.InputTokens != nil ||
				s.CachedInputTokens != nil || s.UncachedInputTokens != nil ||
				s.OutputTokens != nil || s.ReasoningTokens != nil) {
			return fmt.Errorf("%w: invalid expired-before-network observation", ErrLoopIntegrity)
		}
	} else if s.UsageRevision != nil || s.UsageLedgerSequence != nil || s.UsageSemanticDigest != "" ||
		s.UsageStatus != "" || s.InputTokens != nil ||
		s.CachedInputTokens != nil || s.UncachedInputTokens != nil || s.OutputTokens != nil ||
		s.ReasoningTokens != nil {
		return fmt.Errorf("%w: non-Model Usage", ErrLoopIntegrity)
	}
	materializationPresent := s.MaterializedVersionCanonicalDigest != "" ||
		s.MaterializedVersionID != "" || s.MaterializedArtifactDigest != "" ||
		s.MaterializedArtifactSizeBytes != nil || s.ApprovalVerdictDigest != "" ||
		s.MaterializedAtUnixMicros != nil
	if materializationPresent {
		if s.ResourceKind != overviewResourceLearningProposalV1 ||
			s.TransitionKind != overviewTransitionProposalMaterializeV1 ||
			s.State != string(LearningProposalApproved) ||
			!moduleapi.ValidSHA256(s.MaterializedVersionCanonicalDigest) ||
			!moduleapi.ValidSHA256(s.MaterializedVersionID) ||
			!moduleapi.ValidSHA256(s.MaterializedArtifactDigest) ||
			s.MaterializedArtifactSizeBytes == nil || *s.MaterializedArtifactSizeBytes == 0 ||
			!moduleapi.ValidSHA256(s.ApprovalVerdictDigest) ||
			s.MaterializedAtUnixMicros == nil || *s.MaterializedAtUnixMicros < s.UpdatedAtUnixMicros ||
			*s.MaterializedAtUnixMicros > math.MaxInt64 {
			return fmt.Errorf("%w: invalid Proposal materialization observation", ErrLoopIntegrity)
		}
	} else if s.ResourceKind == overviewResourceLearningProposalV1 &&
		s.TransitionKind == overviewTransitionProposalMaterializeV1 {
		return fmt.Errorf("%w: missing Proposal materialization observation", ErrLoopIntegrity)
	}
	if s.ResourceKind != overviewResourceLearningProposalV1 &&
		(s.ProposalKind != "" || materializationPresent) {
		return fmt.Errorf("%w: non-Proposal learning facts", ErrLoopIntegrity)
	}
	return nil
}

func validOverviewResourceKindV1(kind string) bool {
	switch kind {
	case overviewResourceModelV1, overviewResourceActionV1, overviewResourceChannelV1,
		overviewResourceLearningProposalV1, overviewResourceLearningTaskV1,
		overviewResourceModuleReviewV1:
		return true
	default:
		return false
	}
}

func validOverviewResourceTransitionV1(kind, transition string) bool {
	switch kind {
	case overviewResourceModelV1:
		switch transition {
		case overviewTransitionModelBeginV1, overviewTransitionModelExpiredV1,
			overviewTransitionModelOutcomeV1, overviewTransitionModelActionSourceV1,
			overviewTransitionModelChannelSourceV1, overviewTransitionModelRecoveryV1:
			return true
		}
	case overviewResourceActionV1:
		switch transition {
		case overviewTransitionActionBeginV1, overviewTransitionActionOutcomeV1,
			overviewTransitionActionRecoveryV1:
			return true
		}
	case overviewResourceChannelV1:
		switch transition {
		case overviewTransitionChannelBeginV1, overviewTransitionChannelOutcomeV1,
			overviewTransitionChannelRecoveryV1:
			return true
		}
	case overviewResourceLearningProposalV1:
		switch transition {
		case overviewTransitionProposalSubmitV1, overviewTransitionProposalAdmissionV1,
			overviewTransitionProposalFinalizeV1, overviewTransitionProposalMaterializeV1:
			return true
		}
	case overviewResourceLearningTaskV1:
		switch transition {
		case overviewTransitionTaskCreateV1, overviewTransitionTaskAdmissionV1,
			overviewTransitionTaskFinalizeV1:
			return true
		}
	case overviewResourceModuleReviewV1:
		return transition == overviewTransitionModuleReviewV1
	}
	return false
}

// loadOverviewResourceHeadOnlineV1 validates one bounded immutable resource
// projection. It intentionally does not invoke the full typed record loaders;
// those are reserved for writers and the OpenExisting/Backup replay gate.
func loadOverviewResourceHeadOnlineV1(
	ctx context.Context,
	q readQueryerV1,
	kind, resourceID string,
) (overviewResourceRecordV1, error) {
	var out overviewResourceRecordV1
	var sequence, revision, created, updated int64
	var digest, tenantID, state string
	var workspaceID, subjectRunID, bindingTargetKind sql.NullString
	var usageRevision, usageLedgerSequence, inputTokens, cachedTokens, uncachedTokens, outputTokens, reasoningTokens sql.NullInt64
	var usageStatus sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest,
		tenant_id,workspace_id,subject_run_id,state,binding_target_kind,resource_revision,
		created_at,updated_at,usage_revision,usage_ledger_sequence,usage_status,input_tokens,
		cached_input_tokens,uncached_input_tokens,output_tokens,reasoning_tokens
		FROM overview_resource_heads WHERE resource_kind=? AND resource_id=?`,
		kind, resourceID).Scan(&sequence, &digest, &tenantID, &workspaceID,
		&subjectRunID, &state, &bindingTargetKind, &revision, &created, &updated,
		&usageRevision, &usageLedgerSequence, &usageStatus, &inputTokens, &cachedTokens, &uncachedTokens,
		&outputTokens, &reasoningTokens); err != nil || sequence <= 0 ||
		!moduleapi.ValidSHA256(digest) {
		return out, fmt.Errorf("%w: Overview resource head: %v", ErrLoopIntegrity, err)
	}
	var orphan int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM overview_resource_snapshots
		WHERE resource_kind=? AND resource_id=? AND observation_sequence>? LIMIT 1)`,
		kind, resourceID, sequence).Scan(&orphan); err != nil || orphan != 0 {
		return out, fmt.Errorf("%w: Overview resource orphan tail: %v", ErrLoopIntegrity, err)
	}
	var canonical []byte
	if err := q.QueryRowContext(ctx, `SELECT canonical_json FROM overview_resource_snapshots
		WHERE resource_kind=? AND resource_id=? AND observation_sequence=? AND snapshot_digest=?`,
		kind, resourceID, sequence, digest).Scan(&canonical); err != nil {
		return out, fmt.Errorf("%w: Overview resource snapshot: %v", ErrLoopIntegrity, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out.Snapshot); err != nil {
		return out, fmt.Errorf("%w: Overview resource canonical decode: %v", ErrLoopIntegrity, err)
	}
	rebuilt, rebuiltDigest, err := canonicalOverviewResourceV1(out.Snapshot)
	if err != nil || !bytes.Equal(rebuilt, canonical) || rebuiltDigest != digest ||
		out.Snapshot.ResourceKind != kind || out.Snapshot.ResourceID != resourceID ||
		out.Snapshot.ObservationSequence != uint64(sequence) ||
		out.Snapshot.TenantID != tenantID || out.Snapshot.WorkspaceID != workspaceID.String ||
		overviewRunRefStringV1(out.Snapshot.SubjectRun) != subjectRunID.String ||
		out.Snapshot.State != state || out.Snapshot.BindingTargetKind != bindingTargetKind.String ||
		revision < 0 || out.Snapshot.ResourceRevision != uint64(revision) || created <= 0 ||
		updated < created || out.Snapshot.CreatedAtUnixMicros != uint64(created) ||
		out.Snapshot.UpdatedAtUnixMicros != uint64(updated) ||
		!overviewNullableUintMatchesV1(out.Snapshot.UsageRevision, usageRevision) ||
		!overviewNullableUintMatchesV1(out.Snapshot.UsageLedgerSequence, usageLedgerSequence) ||
		out.Snapshot.UsageStatus != usageStatus.String ||
		!overviewNullableUintMatchesV1(out.Snapshot.InputTokens, inputTokens) ||
		!overviewNullableUintMatchesV1(out.Snapshot.CachedInputTokens, cachedTokens) ||
		!overviewNullableUintMatchesV1(out.Snapshot.UncachedInputTokens, uncachedTokens) ||
		!overviewNullableUintMatchesV1(out.Snapshot.OutputTokens, outputTokens) ||
		!overviewNullableUintMatchesV1(out.Snapshot.ReasoningTokens, reasoningTokens) {
		return out, fmt.Errorf("%w: Overview resource canonical differs: %v", ErrLoopIntegrity, err)
	}
	out.Digest, out.Canonical = digest, bytes.Clone(canonical)
	if err := verifyOverviewResourceTransitionCarrierV1(ctx, q, out); err != nil {
		return out, err
	}
	if err := verifyOverviewResourceRunRefsOnlineV1(ctx, q, out.Snapshot); err != nil {
		return out, err
	}
	if err := verifyOverviewResourceRawProjectionOnlineV1(ctx, q, out.Snapshot); err != nil {
		return out, err
	}
	return out, nil
}

func overviewRunRefStringV1(ref *overviewRunObservationRefV1) string {
	if ref == nil {
		return ""
	}
	return ref.RunID
}

func overviewNullableUintMatchesV1(value *uint64, sqlValue sql.NullInt64) bool {
	return value == nil && !sqlValue.Valid || value != nil && sqlValue.Valid &&
		sqlValue.Int64 >= 0 && *value == uint64(sqlValue.Int64)
}

func verifyOverviewResourceRunRefsOnlineV1(
	ctx context.Context,
	q readQueryerV1,
	s overviewResourceCanonicalV1,
) error {
	var storeID string
	if err := q.QueryRowContext(ctx, `SELECT store_instance_id FROM store_meta WHERE singleton=1`).Scan(
		&storeID); err != nil || storeID != s.StoreInstanceID {
		return fmt.Errorf("%w: Overview resource Store identity: %v", ErrLoopIntegrity, err)
	}
	for _, ref := range []*overviewRunObservationRefV1{s.SubjectRun, s.RelatedRun, s.CausalRun} {
		if ref == nil {
			continue
		}
		var tenant, workspace, snapshotStore string
		if err := q.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,store_instance_id
			FROM run_observation_snapshots WHERE run_id=? AND observation_sequence=?
			AND snapshot_digest=?`, ref.RunID, int64(ref.Sequence), ref.Digest).Scan(
			&tenant, &workspace, &snapshotStore); err != nil || tenant != s.TenantID ||
			workspace != s.WorkspaceID || snapshotStore != s.StoreInstanceID {
			return fmt.Errorf("%w: Overview resource frozen Run ref: %v", ErrLoopIntegrity, err)
		}
	}
	return nil
}

func verifyOverviewResourceRawProjectionOnlineV1(
	ctx context.Context,
	q readQueryerV1,
	s overviewResourceCanonicalV1,
) error {
	switch s.ResourceKind {
	case overviewResourceModelV1:
		var runID, tenant, workspace, state, usageStatus string
		var revision, created, updated, sequence, usageRevision, usageSequence int64
		var ledgerSequence sql.NullInt64
		var input, cached, uncached, output, reasoning sql.NullInt64
		err := q.QueryRowContext(ctx, `SELECT attempt.run_id,attempt.tenant_id,
			attempt.workspace_id,attempt.state,attempt.revision,attempt.created_at,
			attempt.updated_at,attempt.overview_observation_sequence,usage.revision,
			usage.ledger_sequence,usage.usage_status,usage.input_tokens,usage.cached_input_tokens,
			usage.uncached_input_tokens,usage.output_tokens,usage.reasoning_tokens,
			usage.overview_observation_sequence FROM model_dispatch_attempts AS attempt
			JOIN model_usage AS usage ON usage.attempt_id=attempt.attempt_id
			WHERE attempt.attempt_id=?`, s.ResourceID).Scan(&runID, &tenant, &workspace,
			&state, &revision, &created, &updated, &sequence, &usageRevision,
			&ledgerSequence, &usageStatus, &input, &cached, &uncached, &output, &reasoning, &usageSequence)
		if err != nil || s.SubjectRun == nil || runID != s.SubjectRun.RunID ||
			tenant != s.TenantID || workspace != s.WorkspaceID || state != s.State ||
			revision < 0 || uint64(revision) != s.ResourceRevision || created <= 0 ||
			uint64(created) != s.CreatedAtUnixMicros || uint64(updated) != s.UpdatedAtUnixMicros ||
			sequence <= 0 || uint64(sequence) != s.ObservationSequence || sequence != usageSequence ||
			s.UsageRevision == nil || usageRevision < 0 || uint64(usageRevision) != *s.UsageRevision ||
			!overviewNullableUintMatchesV1(s.UsageLedgerSequence, ledgerSequence) ||
			usageStatus != s.UsageStatus || !overviewNullableUintMatchesV1(s.InputTokens, input) ||
			!overviewNullableUintMatchesV1(s.CachedInputTokens, cached) ||
			!overviewNullableUintMatchesV1(s.UncachedInputTokens, uncached) ||
			!overviewNullableUintMatchesV1(s.OutputTokens, output) ||
			!overviewNullableUintMatchesV1(s.ReasoningTokens, reasoning) {
			return fmt.Errorf("%w: Overview Model raw projection: %v", ErrLoopIntegrity, err)
		}
	case overviewResourceActionV1, overviewResourceChannelV1:
		var runID, tenant, workspace, state string
		var revision, created, updated, sequence int64
		err := q.QueryRowContext(ctx, `SELECT run_id,tenant_id,workspace_id,state,
			revision,created_at,updated_at,overview_observation_sequence
			FROM dispatch_attempts WHERE dispatch_kind=? AND attempt_id=?`,
			s.ResourceKind, s.ResourceID).Scan(&runID, &tenant, &workspace, &state,
			&revision, &created, &updated, &sequence)
		if err != nil || s.SubjectRun == nil || runID != s.SubjectRun.RunID ||
			tenant != s.TenantID || workspace != s.WorkspaceID || state != s.State ||
			revision < 0 || uint64(revision) != s.ResourceRevision || created <= 0 ||
			uint64(created) != s.CreatedAtUnixMicros || uint64(updated) != s.UpdatedAtUnixMicros ||
			sequence <= 0 || uint64(sequence) != s.ObservationSequence {
			return fmt.Errorf("%w: Overview dispatch raw projection: %v", ErrLoopIntegrity, err)
		}
	case overviewResourceLearningProposalV1:
		var tenant, workspace, kind, state, proposerRun string
		var reviewRun sql.NullString
		var revision, created, updated, sequence int64
		var versionSize, artifactSize, materializedAt sql.NullInt64
		var canonicalDigest, versionID, artifactDigest, verdictDigest sql.NullString
		err := q.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,proposal_kind,state,
			revision,created_at,updated_at,overview_observation_sequence,proposer_run_id,
			review_run_id,version_size_bytes,version_canonical_digest,version_id,artifact_digest,
			artifact_size_bytes,approval_verdict_digest,materialized_at
			FROM learning_proposals WHERE proposal_id=?`, s.ResourceID).Scan(&tenant, &workspace,
			&kind, &state, &revision, &created, &updated, &sequence, &proposerRun,
			&reviewRun, &versionSize, &canonicalDigest, &versionID, &artifactDigest,
			&artifactSize, &verdictDigest, &materializedAt)
		if err != nil || s.SubjectRun == nil || proposerRun != s.SubjectRun.RunID ||
			tenant != s.TenantID || workspace != s.WorkspaceID || kind != s.ProposalKind ||
			state != s.State || revision < 0 || uint64(revision) != s.ResourceRevision ||
			uint64(created) != s.CreatedAtUnixMicros || uint64(updated) != s.UpdatedAtUnixMicros ||
			uint64(sequence) != s.ObservationSequence ||
			(s.RelatedRun == nil) != !reviewRun.Valid ||
			(s.RelatedRun != nil && reviewRun.String != s.RelatedRun.RunID) {
			return fmt.Errorf("%w: Overview Proposal raw projection: %v", ErrLoopIntegrity, err)
		}
		present := versionSize.Valid || canonicalDigest.Valid || versionID.Valid ||
			artifactDigest.Valid || artifactSize.Valid || verdictDigest.Valid || materializedAt.Valid
		if present != (s.MaterializedVersionID != "") || present &&
			(!versionSize.Valid || versionSize.Int64 <= 0 ||
				versionSize.Int64 > learningcontract.MaxMaterializedVersionWireBytesV1 ||
				canonicalDigest.String != s.MaterializedVersionCanonicalDigest ||
				versionID.String != s.MaterializedVersionID ||
				artifactDigest.String != s.MaterializedArtifactDigest ||
				s.MaterializedArtifactSizeBytes == nil || artifactSize.Int64 < 0 ||
				uint64(artifactSize.Int64) != *s.MaterializedArtifactSizeBytes ||
				verdictDigest.String != s.ApprovalVerdictDigest ||
				s.MaterializedAtUnixMicros == nil || materializedAt.Int64 < 0 ||
				uint64(materializedAt.Int64) != *s.MaterializedAtUnixMicros) {
			return fmt.Errorf("%w: Overview Proposal materialization raw projection", ErrLoopIntegrity)
		}
	case overviewResourceLearningTaskV1:
		var tenant, workspace, state string
		var runID sql.NullString
		var revision, created, updated, sequence int64
		err := q.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,state,revision,
			created_at,updated_at,overview_observation_sequence,run_id
			FROM learning_cycle_tasks WHERE task_id=?`, s.ResourceID).Scan(&tenant, &workspace,
			&state, &revision, &created, &updated, &sequence, &runID)
		if err != nil || tenant != s.TenantID || workspace != s.WorkspaceID || state != s.State ||
			revision < 0 || uint64(revision) != s.ResourceRevision ||
			uint64(created) != s.CreatedAtUnixMicros || uint64(updated) != s.UpdatedAtUnixMicros ||
			uint64(sequence) != s.ObservationSequence || (s.SubjectRun == nil) != !runID.Valid ||
			(s.SubjectRun != nil && s.SubjectRun.RunID != runID.String) {
			return fmt.Errorf("%w: Overview Task raw projection: %v", ErrLoopIntegrity, err)
		}
	case overviewResourceModuleReviewV1:
		var tenant, targetKind, conclusion, currentInstance, targetInstance string
		var candidateID, currentModule, currentVersion, currentArtifact string
		var targetModule, targetVersion, targetArtifact string
		var workspace sql.NullString
		var created, sequence int64
		err := q.QueryRowContext(ctx, `SELECT review.tenant_id,review.workspace_id,
			review.binding_target_kind,review.conclusion,review.current_instance_id,
			review.target_instance_id,review.created_at,review.overview_observation_sequence,
			review.candidate_id,candidate.current_module_id,candidate.current_exact_version,
			candidate.current_artifact_digest,candidate.target_module_id,
			candidate.target_exact_version,candidate.target_artifact_digest
			FROM module_upgrade_reviews AS review JOIN module_upgrade_candidates AS candidate
			ON candidate.candidate_id=review.candidate_id WHERE review.review_id=?`,
			s.ResourceID).Scan(&tenant, &workspace, &targetKind, &conclusion,
			&currentInstance, &targetInstance, &created, &sequence, &candidateID,
			&currentModule, &currentVersion, &currentArtifact, &targetModule,
			&targetVersion, &targetArtifact)
		if err != nil || tenant != s.TenantID || workspace.String != s.WorkspaceID ||
			targetKind != s.BindingTargetKind || conclusion != s.State ||
			currentInstance != s.CurrentInstanceID || targetInstance != s.TargetInstanceID ||
			uint64(created) != s.CreatedAtUnixMicros || uint64(sequence) != s.ObservationSequence ||
			candidateID != s.CandidateID || currentModule != s.CurrentModuleID ||
			currentVersion != s.CurrentExactVersion || currentArtifact != s.CurrentArtifactDigest ||
			targetModule != s.TargetModuleID || targetVersion != s.TargetExactVersion ||
			targetArtifact != s.TargetArtifactDigest {
			return fmt.Errorf("%w: Overview Module raw projection: %v", ErrLoopIntegrity, err)
		}
	default:
		return fmt.Errorf("%w: unsupported Overview raw projection", ErrLoopIntegrity)
	}
	return nil
}

func overviewRunRefIDV1(ref *overviewRunObservationRefV1) any {
	if ref == nil {
		return nil
	}
	return ref.RunID
}
func overviewRunRefSequenceV1(ref *overviewRunObservationRefV1) any {
	if ref == nil {
		return nil
	}
	return int64(ref.Sequence)
}
func overviewRunRefDigestV1(ref *overviewRunObservationRefV1) any {
	if ref == nil {
		return nil
	}
	return ref.Digest
}
func nullableOverviewUintV1(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func appendModelResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	attemptID, transition string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceModelV1, attemptID, transition)
}
func appendActionResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	attemptID, transition string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceActionV1, attemptID, transition)
}
func appendChannelResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	attemptID, transition string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceChannelV1, attemptID, transition)
}
func appendProposalResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	proposalID, transition string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceLearningProposalV1, proposalID, transition)
}
func appendTaskResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	taskID, transition string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceLearningTaskV1, taskID, transition)
}
func appendModuleReviewResourceObservationV1(ctx context.Context, q runObservationWriterV1,
	reviewID string) error {
	return appendOverviewResourceObservationV1(ctx, q, overviewResourceModuleReviewV1,
		reviewID, overviewTransitionModuleReviewV1)
}
