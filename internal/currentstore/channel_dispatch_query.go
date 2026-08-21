package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func (store *Store) GetChannelDispatchRecord(
	ctx context.Context,
	attemptID string,
) (ChannelDispatchRecord, error) {
	if ctx == nil || !validLeaseOpaqueID(attemptID) {
		return ChannelDispatchRecord{}, fmt.Errorf(
			"%w: invalid context or Attempt ID",
			ErrInvalidChannelDispatch,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ChannelDispatchRecord{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	record, err := queryChannelDispatchRecord(ctx, connection, attemptID)
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ChannelDispatchRecord{}, err
	}
	committed = true
	return cloneChannelDispatchRecord(record), nil
}

func queryChannelDispatchRecord(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attemptID string,
) (ChannelDispatchRecord, error) {
	var (
		record                 ChannelDispatchAttemptRecord
		bindingIndex           int64
		maxResultBytes         int64
		deadline               int64
		state                  string
		externalOperationID    sql.NullString
		providerReceiptRef     sql.NullString
		resultRef              sql.NullString
		errorClassification    sql.NullString
		reconciliationEvidence sql.NullString
		unknownReason          sql.NullString
		revision               int64
		createdAt              int64
		updatedAt              int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT attempt_id, logical_operation_key, run_id, tenant_id, workspace_id, member_id,
			logical_step_id, source_model_attempt_id, frame_revision,
			member_snapshot_digest, binding_index, binding_json,
			channel_endpoint_id, channel_ingress_key, channel_proposal_ref,
			effect_class, max_result_bytes, deadline, budget_state_ref,
			state, external_operation_id, provider_receipt_ref, result_ref,
			error_classification, reconciliation_evidence_ref, unknown_reason,
			revision, created_at, updated_at
		FROM dispatch_attempts
		WHERE attempt_id=? AND dispatch_kind='CHANNEL_SEND'
	`, attemptID).Scan(
		&record.AttemptID,
		&record.LogicalOperationKey,
		&record.RunID,
		&record.TenantID,
		&record.WorkspaceID,
		&record.MemberID,
		&record.LogicalStepID,
		&record.SourceModelAttemptID,
		&record.FrameRevision,
		&record.MemberSnapshotDigest,
		&bindingIndex,
		&record.BindingCanonical,
		&record.EndpointID,
		&record.IngressKey,
		&record.ProposalRef,
		&record.EffectClass,
		&maxResultBytes,
		&deadline,
		&record.BudgetStateRef,
		&state,
		&externalOperationID,
		&providerReceiptRef,
		&resultRef,
		&errorClassification,
		&reconciliationEvidence,
		&unknownReason,
		&revision,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelDispatchRecord{}, fmt.Errorf(
			"%w: Channel Attempt %q does not exist",
			ErrChannelDispatchConflict,
			attemptID,
		)
	}
	if err != nil {
		return ChannelDispatchRecord{}, fmt.Errorf(
			"currentstore: query Channel Attempt %q: %w",
			attemptID,
			err,
		)
	}
	if record.FrameRevision > math.MaxInt64 || bindingIndex < 0 ||
		bindingIndex > math.MaxUint32 || maxResultBytes < 1 ||
		maxResultBytes > moduleapi.MaxChannelProviderReceiptBytesV1 ||
		deadline <= 0 || revision < 0 || createdAt <= 0 || updatedAt <= 0 {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "numeric projection")
	}
	for name, value := range map[string]string{
		"Attempt ID":              record.AttemptID,
		"Run ID":                  record.RunID,
		"Tenant ID":               record.TenantID,
		"Workspace ID":            record.WorkspaceID,
		"Member ID":               record.MemberID,
		"logical step ID":         record.LogicalStepID,
		"source Model Attempt ID": record.SourceModelAttemptID,
		"Endpoint ID":             record.EndpointID,
	} {
		if !validLeaseOpaqueID(value) {
			return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, name)
		}
	}
	if !moduleapi.ValidSHA256(record.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(record.MemberSnapshotDigest) ||
		!moduleapi.ValidSHA256(record.IngressKey) ||
		!moduleapi.ValidSHA256(record.ProposalRef) {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "digest projection")
	}
	expectedKey, err := channelLogicalOperationKey(
		record.RunID,
		record.MemberID,
		record.LogicalStepID,
	)
	if err != nil || expectedKey != record.LogicalOperationKey {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "logical operation key")
	}
	record.BindingIndex = uint32(bindingIndex)
	record.MaxResultBytes = uint32(maxResultBytes)
	record.State = DispatchState(state)
	if err := record.State.validate(); err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "state")
	}
	binding, err := restoreModelBinding(record.BindingCanonical)
	if err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "frozen Binding")
	}
	record.Binding = binding
	if record.EffectClass != moduleapi.EffectIrreversibleWrite {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "EffectClass")
	}
	record.Deadline, err = timeFromUnixMicro(deadline)
	if err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "deadline")
	}
	if _, err := corecontract.ParseBudgetStateRefV1(record.BudgetStateRef, record.RunID); err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "BudgetStateRef")
	}
	record.ExternalOperationID = externalOperationID.String
	record.ProviderReceiptRef = providerReceiptRef.String
	record.ResultRef = resultRef.String
	record.ErrorClassification = errorClassification.String
	record.ReconciliationEvidenceRef = reconciliationEvidence.String
	record.UnknownReason = unknownReason.String
	record.Revision = uint64(revision)
	record.CreatedAt, err = timeFromUnixMicro(createdAt)
	if err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "created_at")
	}
	record.UpdatedAt, err = timeFromUnixMicro(updatedAt)
	if err != nil || record.UpdatedAt.Before(record.CreatedAt) {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "updated_at")
	}
	if err := validateChannelDispatchStateFacts(record); err != nil {
		return ChannelDispatchRecord{}, err
	}

	member, plan, err := loadChannelAttemptMember(ctx, queryer, record)
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	if uint64(record.BindingIndex) >= uint64(len(plan.Bindings)) {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "BindingIndex")
	}
	bindingCanonical, err := canonicalModelBinding(plan.Bindings[record.BindingIndex])
	if err != nil || !bytes.Equal(bindingCanonical, record.BindingCanonical) {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "Binding closure")
	}

	proposal, err := queryActionContent(
		ctx,
		queryer,
		record.ProposalRef,
		ContentChannelSendProposal,
	)
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	source, err := queryModelDispatchRecord(ctx, queryer, record.SourceModelAttemptID)
	if err != nil || source.Attempt.RunID != record.RunID ||
		source.Attempt.MemberID != record.MemberID ||
		source.Attempt.State != corecontract.ModelAttemptSucceeded ||
		source.Attempt.ResultRef == "" {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "source Model Attempt")
	}
	modelResult, err := queryActionContent(
		ctx,
		queryer,
		source.Attempt.ResultRef,
		ContentModelResult,
	)
	if err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "source Model result")
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(modelResult.CanonicalBytes)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "source Model output")
	}
	decodedProposal, _, err := restoreExactChannelProposal(
		proposal,
		member.MemberSnapshotDigest,
		record.EndpointID,
		record.IngressKey,
		output.AssistantText,
	)
	if err != nil {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "Channel Proposal closure")
	}
	if decodedProposal.EndpointID != record.EndpointID ||
		decodedProposal.IngressKey != record.IngressKey {
		return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "Channel Proposal identity")
	}
	if err := validateChannelIngressForDispatch(ctx, queryer, record, decodedProposal); err != nil {
		return ChannelDispatchRecord{}, err
	}

	result := ChannelDispatchRecord{Attempt: record, Proposal: proposal}
	if record.ResultRef != "" {
		content, err := queryActionContent(ctx, queryer, record.ResultRef, ContentChannelSendResult)
		if err != nil {
			return ChannelDispatchRecord{}, err
		}
		executed, err := moduleapi.RestoreChannelExecutionResultV1(content.CanonicalBytes)
		if err != nil || executed.AttemptID != record.AttemptID ||
			executed.Outcome != moduleapi.ChannelExecutionSucceeded {
			return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "Channel Result closure")
		}
		result.Result = &content
	}
	if record.ProviderReceiptRef != "" {
		content, err := queryActionContent(ctx, queryer, record.ProviderReceiptRef, ContentProviderReceipt)
		if err != nil || len(content.CanonicalBytes) == 0 ||
			len(content.CanonicalBytes) > moduleapi.MaxChannelProviderReceiptBytesV1 {
			return ChannelDispatchRecord{}, channelAttemptIntegrity(attemptID, "Provider receipt closure")
		}
		result.ProviderReceipt = &content
	}
	if record.ReconciliationEvidenceRef != "" {
		content, err := queryActionContent(
			ctx,
			queryer,
			record.ReconciliationEvidenceRef,
			ContentReconciliationEvidence,
		)
		if err != nil {
			return ChannelDispatchRecord{}, err
		}
		result.ReconciliationEvidence = &content
	}
	return result, nil
}

func loadChannelAttemptMember(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	record ChannelDispatchAttemptRecord,
) (corecontract.MemberExecutionSnapshot, moduleapi.PortPlan, error) {
	var canonical []byte
	var digest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, record.RunID, record.MemberID).Scan(&canonical, &digest); err != nil {
		return corecontract.MemberExecutionSnapshot{}, moduleapi.PortPlan{},
			channelAttemptIntegrity(record.AttemptID, "member snapshot")
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil || digest != member.MemberSnapshotDigest ||
		digest != record.MemberSnapshotDigest {
		return corecontract.MemberExecutionSnapshot{}, moduleapi.PortPlan{},
			channelAttemptIntegrity(record.AttemptID, "member snapshot closure")
	}
	plan, err := exactChannelPlan(member)
	if err != nil {
		return corecontract.MemberExecutionSnapshot{}, moduleapi.PortPlan{}, err
	}
	return member, plan, nil
}

func exactChannelPlan(member corecontract.MemberExecutionSnapshot) (moduleapi.PortPlan, error) {
	for _, plan := range member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameChannelTransport &&
			plan.Port.ExactVersion == moduleapi.PortVersionV1 {
			return moduleapi.NewPortPlan(plan)
		}
	}
	return moduleapi.PortPlan{}, fmt.Errorf(
		"%w: frozen member lacks channel.transport/v1",
		ErrChannelDispatchIntegrity,
	)
}

func memberHasChannelPort(member corecontract.MemberExecutionSnapshot) bool {
	_, err := exactChannelPlan(member)
	return err == nil
}

func restoreExactChannelProposal(
	content ContentRecord,
	memberDigest string,
	endpointID string,
	ingressKey string,
	assistantText string,
) (moduleapi.ChannelSendProposalV1, string, error) {
	// Decode first to recover the Core-owned reply target and prepared payload,
	// then rebuild from the source model text. The exported constructor is the
	// canonical authority and also recomputes the proposal-domain digest.
	wireDigest, err := moduleapi.ChannelSendProposalDigestV1(content.CanonicalBytes)
	if err != nil {
		return moduleapi.ChannelSendProposalV1{}, "", err
	}
	loose, err := moduleapi.RestoreChannelSendProposalV1(
		content.CanonicalBytes,
		wireDigest,
	)
	if err != nil {
		return moduleapi.ChannelSendProposalV1{}, "", err
	}
	rebuilt, canonical, wireDigest, err := moduleapi.NewChannelSendProposalV1(
		memberDigest,
		endpointID,
		ingressKey,
		loose.ReplyTarget,
		assistantText,
		loose.PreparedPayload,
	)
	if err != nil || !bytes.Equal(canonical, content.CanonicalBytes) {
		return moduleapi.ChannelSendProposalV1{}, "", fmt.Errorf("proposal differs: %v", err)
	}
	return rebuilt, wireDigest, nil
}

func validateChannelIngressForDispatch(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	record ChannelDispatchAttemptRecord,
	proposal moduleapi.ChannelSendProposalV1,
) error {
	var endpointID, ingressKey, envelopeRef, disposition string
	if err := queryer.QueryRowContext(ctx, `
		SELECT endpoint_id, ingress_key, envelope_ref, disposition
		FROM channel_ingress_receipts
		WHERE run_id=?
	`, record.RunID).Scan(&endpointID, &ingressKey, &envelopeRef, &disposition); err != nil {
		return channelAttemptIntegrity(record.AttemptID, "ingress receipt")
	}
	if disposition != "ACCEPTED" || endpointID != record.EndpointID ||
		ingressKey != record.IngressKey || !moduleapi.ValidSHA256(envelopeRef) {
		return channelAttemptIntegrity(record.AttemptID, "ingress identity")
	}
	envelopeContent, err := queryActionContent(
		ctx,
		queryer,
		envelopeRef,
		ContentChannelIngressEnvelope,
	)
	if err != nil {
		return err
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(envelopeContent.CanonicalBytes)
	if err != nil || envelope.EndpointID != endpointID ||
		!bytes.Equal(envelope.ReplyTarget, proposal.ReplyTarget) {
		return channelAttemptIntegrity(record.AttemptID, "ingress envelope closure")
	}
	return nil
}

func validateChannelDispatchStateFacts(record ChannelDispatchAttemptRecord) error {
	switch record.State {
	case DispatchPending:
		if record.ExternalOperationID != "" || record.ProviderReceiptRef != "" ||
			record.ResultRef != "" || record.ErrorClassification != "" ||
			record.ReconciliationEvidenceRef != "" || record.UnknownReason != "" {
			return channelAttemptIntegrity(record.AttemptID, "PENDING facts")
		}
	case DispatchSucceeded:
		if record.ResultRef == "" || record.ErrorClassification != "" ||
			record.UnknownReason != "" {
			return channelAttemptIntegrity(record.AttemptID, "SUCCEEDED facts")
		}
	case DispatchFailed:
		if record.ResultRef != "" || record.ErrorClassification == "" ||
			record.UnknownReason != "" {
			return channelAttemptIntegrity(record.AttemptID, "FAILED facts")
		}
	case DispatchUnknown:
		if record.ResultRef != "" || record.ErrorClassification != "" ||
			(record.ExternalOperationID == "" && record.ProviderReceiptRef == "" &&
				record.ReconciliationEvidenceRef == "" && record.UnknownReason == "") {
			return channelAttemptIntegrity(record.AttemptID, "UNKNOWN facts")
		}
	default:
		return channelAttemptIntegrity(record.AttemptID, "state")
	}
	return nil
}

func channelAttemptIntegrity(attemptID, subject string) error {
	return fmt.Errorf(
		"%w: Channel Attempt %q has invalid %s",
		ErrChannelDispatchIntegrity,
		attemptID,
		subject,
	)
}

var _ = time.Time{}
