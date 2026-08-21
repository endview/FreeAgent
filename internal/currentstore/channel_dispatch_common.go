package currentstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	channelOperationDigestDomain     = "freeagent.channel-send-operation/v1"
	channelDispatchEventSchemaV1     = "channel-dispatch-event/v1"
	channelDispatchPendingEvent      = "CHANNEL_DISPATCH_PENDING"
	channelDispatchTerminalEvent     = "CHANNEL_DISPATCH_TERMINAL"
	channelUnknownWaitingReason      = "CHANNEL_DELIVERY_UNKNOWN"
	channelDispatchAttemptIDPrefixV1 = "channel-attempt-"
)

var (
	ErrInvalidChannelDispatch   = errors.New("currentstore: invalid Channel dispatch")
	ErrChannelDispatchConflict  = errors.New("currentstore: Channel dispatch conflict")
	ErrChannelDispatchIntegrity = errors.New(
		"currentstore: Channel dispatch integrity violation",
	)
)

// ChannelDispatchAttemptRecord is the Channel view of one row in the shared
// dispatch_attempts ledger. The table, state machine and UNKNOWN rules are
// identical to Action; only the kind-specific frozen closure differs.
type ChannelDispatchAttemptRecord struct {
	AttemptID                 string
	LogicalOperationKey       string
	RunID                     string
	TenantID                  string
	WorkspaceID               string
	MemberID                  string
	LogicalStepID             string
	SourceModelAttemptID      string
	FrameRevision             uint64
	MemberSnapshotDigest      string
	BindingIndex              uint32
	Binding                   moduleapi.PortBinding
	BindingCanonical          []byte
	EndpointID                string
	IngressKey                string
	ProposalRef               string
	EffectClass               moduleapi.EffectClass
	MaxResultBytes            uint32
	Deadline                  time.Time
	BudgetStateRef            string
	State                     DispatchState
	ExternalOperationID       string
	ProviderReceiptRef        string
	ResultRef                 string
	ErrorClassification       string
	ReconciliationEvidenceRef string
	UnknownReason             string
	Revision                  uint64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type ChannelDispatchRecord struct {
	Attempt                ChannelDispatchAttemptRecord
	Proposal               ContentRecord
	Result                 *ContentRecord
	ProviderReceipt        *ContentRecord
	ReconciliationEvidence *ContentRecord
}

type channelGatewayPermit struct {
	consumed atomic.Bool
	closure  channelGatewayPermitClosure
}

type channelGatewayPermitClosure struct {
	attemptID            string
	runID                string
	memberID             string
	memberSnapshotDigest string
	bindingIndex         uint32
	bindingCanonical     []byte
	proposalDigest       string
	deadline             time.Time
	lease                RunLease
}

type CommitModelChannelAndBeginDispatchResult struct {
	Model          ModelDispatchRecord
	Channel        ChannelDispatchRecord
	Lease          RunLease
	Applied        bool
	GatewayAllowed bool
	permit         *channelGatewayPermit
}

func (result CommitModelChannelAndBeginDispatchResult) ConsumeChannelGatewayPermit() bool {
	if result.permit == nil || !result.Applied || !result.GatewayAllowed ||
		!result.permit.matches(result.Channel.Attempt, result.Lease) {
		return false
	}
	return result.permit.consumed.CompareAndSwap(false, true)
}

func (permit *channelGatewayPermit) matches(
	attempt ChannelDispatchAttemptRecord,
	lease RunLease,
) bool {
	if permit == nil {
		return false
	}
	closure := permit.closure
	return closure.attemptID == attempt.AttemptID &&
		closure.runID == attempt.RunID &&
		closure.memberID == attempt.MemberID &&
		closure.memberSnapshotDigest == attempt.MemberSnapshotDigest &&
		closure.bindingIndex == attempt.BindingIndex &&
		bytes.Equal(closure.bindingCanonical, attempt.BindingCanonical) &&
		closure.proposalDigest == attempt.ProposalRef &&
		closure.deadline.Equal(attempt.Deadline) &&
		closure.lease == lease
}

func channelLogicalOperationKey(runID, memberID, logicalStepID string) (string, error) {
	for name, value := range map[string]string{
		"run ID": runID, "member ID": memberID, "logical step ID": logicalStepID,
	} {
		if !validLeaseOpaqueID(value) {
			return "", fmt.Errorf("invalid %s", name)
		}
	}
	encoded, err := json.Marshal(struct {
		RunID         string `json:"run_id"`
		MemberID      string `json:"member_id"`
		LogicalStepID string `json:"logical_step_id"`
	}{runID, memberID, logicalStepID})
	if err != nil {
		return "", err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(channelOperationDigestDomain, canonical), nil
}

type channelDispatchEventV1 struct {
	SchemaVersion             string                                  `json:"schema_version"`
	RunID                     string                                  `json:"run_id"`
	AttemptID                 string                                  `json:"attempt_id"`
	LogicalStepID             string                                  `json:"logical_step_id"`
	LogicalOperationKey       string                                  `json:"logical_operation_key"`
	ProposalDigest            string                                  `json:"proposal_digest"`
	State                     DispatchState                           `json:"state"`
	ResultDigest              string                                  `json:"result_digest,omitempty"`
	TransitionOrigin          corecontract.DispatchTransitionOriginV1 `json:"transition_origin"`
	SourceModelAttemptID      string                                  `json:"source_model_attempt_id,omitempty"`
	SourceModelUsage          *corecontract.ModelUsageEventV1         `json:"source_model_usage,omitempty"`
	ResourceSemanticDigest    string                                  `json:"resource_semantic_digest"`
	SourceModelSemanticDigest string                                  `json:"source_model_semantic_digest,omitempty"`
}

func prepareChannelDispatchEvent(
	event channelDispatchEventV1,
) ([]byte, string, error) {
	if event.SchemaVersion != channelDispatchEventSchemaV1 ||
		!validLeaseOpaqueID(event.RunID) ||
		!validLeaseOpaqueID(event.AttemptID) ||
		!validLeaseOpaqueID(event.LogicalStepID) ||
		!moduleapi.ValidSHA256(event.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(event.ProposalDigest) ||
		!moduleapi.ValidSHA256(event.ResourceSemanticDigest) ||
		event.State.validate() != nil || event.TransitionOrigin.Validate() != nil ||
		(event.ResultDigest != "" && !moduleapi.ValidSHA256(event.ResultDigest)) {
		return nil, "", fmt.Errorf("%w: invalid Channel event", ErrInvalidChannelDispatch)
	}
	if event.State == DispatchPending {
		if event.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			!validLeaseOpaqueID(event.SourceModelAttemptID) || event.SourceModelUsage == nil ||
			event.SourceModelUsage.Validate() != nil ||
			!moduleapi.ValidSHA256(event.SourceModelSemanticDigest) {
			return nil, "", fmt.Errorf("%w: invalid Channel begin provenance", ErrInvalidChannelDispatch)
		}
	} else if event.TransitionOrigin == corecontract.DispatchTransitionBeginV1 ||
		event.SourceModelAttemptID != "" || event.SourceModelUsage != nil ||
		event.SourceModelSemanticDigest != "" {
		return nil, "", fmt.Errorf("%w: invalid Channel terminal provenance", ErrInvalidChannelDispatch)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return nil, "", err
	}
	digest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	return canonical, digest, err
}

func cloneChannelDispatchRecord(record ChannelDispatchRecord) ChannelDispatchRecord {
	record.Attempt.BindingCanonical = bytes.Clone(record.Attempt.BindingCanonical)
	record.Attempt.Binding.StaticContextRefs = append(
		[]string{},
		record.Attempt.Binding.StaticContextRefs...,
	)
	record.Proposal = cloneContentRecord(record.Proposal)
	if record.Result != nil {
		content := cloneContentRecord(*record.Result)
		record.Result = &content
	}
	if record.ProviderReceipt != nil {
		content := cloneContentRecord(*record.ProviderReceipt)
		record.ProviderReceipt = &content
	}
	if record.ReconciliationEvidence != nil {
		content := cloneContentRecord(*record.ReconciliationEvidence)
		record.ReconciliationEvidence = &content
	}
	return record
}
