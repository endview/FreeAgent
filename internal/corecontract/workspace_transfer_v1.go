package corecontract

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	WorkspaceTransferGrantSchemaVersionV1    = "workspace-transfer-grant/v1"
	WorkspaceTransferEnvelopeSchemaVersionV1 = "workspace-transfer-envelope/v1"
	WorkspaceTransferPlanSchemaVersionV1     = "workspace-transfer-plan/v1"
	WorkspaceTaskSummarySchemaVersionV1      = "workspace-task-summary/v1"

	WorkspaceTaskSummaryMaximumPayloadBytesV1 = uint32(32 << 10)
	WorkspaceTransferMaximumPayloadBytesV1    = uint32(48 << 10)

	workspaceTransferGrantDigestDomainV1    = "freeagent.workspace-transfer-grant/v1"
	workspaceTransferEnvelopeDigestDomainV1 = "freeagent.workspace-transfer-envelope/v1"
	workspaceTransferContentRecordDomainV1  = "freeagent.content-record/v1"
	workspaceTransferJSONMediaTypeV1        = "application/json"
	workspaceTransferPayloadRecordKindV1    = "WORKSPACE_TRANSFER_PAYLOAD"
	workspaceTransferModelResultKindV1      = "MODEL_RESULT"
	workspaceTransferPayloadKindCountV1     = 2
)

// WorkspaceTransferPayloadKindV1 is the closed set of content classes that a
// Workspace owner may explicitly send or receive. It deliberately excludes
// complete History, Memory, Knowledge bodies, secrets, and arbitrary files.
type WorkspaceTransferPayloadKindV1 string

const (
	WorkspaceTransferPayloadTaskSummaryV1      WorkspaceTransferPayloadKindV1 = "TASK_SUMMARY"
	WorkspaceTransferPayloadSpecialistResultV1 WorkspaceTransferPayloadKindV1 = "SPECIALIST_RESULT"
)

// WorkspaceTransferDirectionV1 distinguishes a root-to-Specialist request
// from the exact Specialist result returned to the root Workspace.
type WorkspaceTransferDirectionV1 string

const (
	WorkspaceTransferDirectionRequestV1 WorkspaceTransferDirectionV1 = "REQUEST"
	WorkspaceTransferDirectionResultV1  WorkspaceTransferDirectionV1 = "RESULT"
)

// WorkspaceTransferGrantV1 is one Workspace owner's independent half of a
// bilateral authorization. A collaboration is authorized only by intersecting
// an enabled source send grant with an enabled target receive grant.
type WorkspaceTransferGrantV1 struct {
	SchemaVersion          string                           `json:"schema_version"`
	GrantID                string                           `json:"grant_id"`
	TenantID               string                           `json:"tenant_id"`
	Workspace              WorkspaceRef                     `json:"workspace"`
	PeerWorkspace          WorkspaceRef                     `json:"peer_workspace"`
	Revision               uint64                           `json:"revision"`
	Enabled                bool                             `json:"enabled"`
	SendPayloadKinds       []WorkspaceTransferPayloadKindV1 `json:"send_payload_kinds"`
	ReceivePayloadKinds    []WorkspaceTransferPayloadKindV1 `json:"receive_payload_kinds"`
	MaxSendPayloadBytes    uint32                           `json:"max_send_payload_bytes"`
	MaxReceivePayloadBytes uint32                           `json:"max_receive_payload_bytes"`
}

// WorkspaceTransferPlanV1 is the immutable bilateral authority edge frozen
// into one composite Child plan. It deliberately contains no payload or
// envelope ref: those facts do not exist until the trusted dispatch boundary
// has loaded the exact task input (and optional repair basis) from the Store.
// Root/Target name the family direction, not the direction of every envelope;
// SPECIALIST_RESULT reverses the grant order.
type WorkspaceTransferPlanV1 struct {
	SchemaVersion     string       `json:"schema_version"`
	RootWorkspace     WorkspaceRef `json:"root_workspace"`
	TargetWorkspace   WorkspaceRef `json:"target_workspace"`
	RootGrantID       string       `json:"root_grant_id"`
	RootGrantDigest   string       `json:"root_grant_digest"`
	TargetGrantID     string       `json:"target_grant_id"`
	TargetGrantDigest string       `json:"target_grant_digest"`
}

// NewWorkspaceTransferPlanV1 intersects the two independently owned grants
// needed by a composite Child. Version one requires the complete request and
// result path up front; a partially authorized family is never admitted.
func NewWorkspaceTransferPlanV1(
	root WorkspaceTransferGrantV1,
	target WorkspaceTransferGrantV1,
) (WorkspaceTransferPlanV1, error) {
	frozenRoot, _, rootDigest, err := NewWorkspaceTransferGrantV1(root)
	if err != nil {
		return WorkspaceTransferPlanV1{}, fmt.Errorf(
			"corecontract: root Workspace transfer grant: %w",
			err,
		)
	}
	frozenTarget, _, targetDigest, err := NewWorkspaceTransferGrantV1(target)
	if err != nil {
		return WorkspaceTransferPlanV1{}, fmt.Errorf(
			"corecontract: target Workspace transfer grant: %w",
			err,
		)
	}
	if !frozenRoot.Enabled || !frozenTarget.Enabled ||
		frozenRoot.TenantID != frozenTarget.TenantID ||
		frozenRoot.GrantID == frozenTarget.GrantID ||
		rootDigest == targetDigest ||
		frozenRoot.Workspace != frozenTarget.PeerWorkspace ||
		frozenRoot.PeerWorkspace != frozenTarget.Workspace ||
		!workspaceTransferPayloadKindsContainV1(
			frozenRoot.SendPayloadKinds,
			WorkspaceTransferPayloadTaskSummaryV1,
		) ||
		!workspaceTransferPayloadKindsContainV1(
			frozenRoot.ReceivePayloadKinds,
			WorkspaceTransferPayloadSpecialistResultV1,
		) ||
		!workspaceTransferPayloadKindsContainV1(
			frozenTarget.ReceivePayloadKinds,
			WorkspaceTransferPayloadTaskSummaryV1,
		) ||
		!workspaceTransferPayloadKindsContainV1(
			frozenTarget.SendPayloadKinds,
			WorkspaceTransferPayloadSpecialistResultV1,
		) {
		return WorkspaceTransferPlanV1{}, fmt.Errorf(
			"corecontract: Workspace transfer plan lacks complete bilateral request/result authorization",
		)
	}
	plan := WorkspaceTransferPlanV1{
		SchemaVersion:     WorkspaceTransferPlanSchemaVersionV1,
		RootWorkspace:     frozenRoot.Workspace,
		TargetWorkspace:   frozenTarget.Workspace,
		RootGrantID:       frozenRoot.GrantID,
		RootGrantDigest:   rootDigest,
		TargetGrantID:     frozenTarget.GrantID,
		TargetGrantDigest: targetDigest,
	}
	if err := plan.Validate(); err != nil {
		return WorkspaceTransferPlanV1{}, err
	}
	return plan, nil
}

func (plan WorkspaceTransferPlanV1) Validate() error {
	if plan.SchemaVersion != WorkspaceTransferPlanSchemaVersionV1 ||
		!validOpaque(plan.RootGrantID, maxOpaqueIDBytes) ||
		!validOpaque(plan.TargetGrantID, maxOpaqueIDBytes) ||
		plan.RootGrantID == plan.TargetGrantID ||
		!moduleapi.ValidSHA256(plan.RootGrantDigest) ||
		!moduleapi.ValidSHA256(plan.TargetGrantDigest) ||
		plan.RootGrantDigest == plan.TargetGrantDigest {
		return fmt.Errorf("corecontract: invalid Workspace transfer plan identity")
	}
	if err := plan.RootWorkspace.Validate(); err != nil {
		return fmt.Errorf("corecontract: Workspace transfer plan root: %w", err)
	}
	if err := plan.TargetWorkspace.Validate(); err != nil {
		return fmt.Errorf("corecontract: Workspace transfer plan target: %w", err)
	}
	if plan.RootWorkspace.ID == plan.TargetWorkspace.ID {
		return fmt.Errorf(
			"corecontract: Workspace transfer plan requires distinct Workspaces",
		)
	}
	return nil
}

func (plan WorkspaceTransferPlanV1) ValidateAgainstGrantsV1(
	root WorkspaceTransferGrantV1,
	target WorkspaceTransferGrantV1,
) error {
	expected, err := NewWorkspaceTransferPlanV1(root, target)
	if err != nil {
		return err
	}
	if plan != expected {
		return fmt.Errorf(
			"corecontract: Workspace transfer plan does not match the exact bilateral grants",
		)
	}
	return nil
}

// NewWorkspaceTransferGrantV1 freezes one grant half. Payload kinds are
// canonicalized in binary order; duplicate or unsupported kinds fail closed.
func NewWorkspaceTransferGrantV1(
	input WorkspaceTransferGrantV1,
) (WorkspaceTransferGrantV1, []byte, string, error) {
	if input.SchemaVersion != WorkspaceTransferGrantSchemaVersionV1 {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer grant schema version must be %q",
			WorkspaceTransferGrantSchemaVersionV1,
		)
	}
	if !validOpaque(input.GrantID, maxOpaqueIDBytes) {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid Workspace transfer grant ID",
		)
	}
	if !validOpaque(input.TenantID, maxOpaqueIDBytes) {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid Workspace transfer grant tenant ID",
		)
	}
	if err := input.Workspace.Validate(); err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer grant owner: %w", err,
		)
	}
	if err := input.PeerWorkspace.Validate(); err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer grant peer: %w", err,
		)
	}
	if input.Workspace.ID == input.PeerWorkspace.ID {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer grant requires distinct Workspaces",
		)
	}
	if input.Revision == 0 || input.Revision > maximumJSONSafeIntegerV1 {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid Workspace transfer grant revision",
		)
	}
	send, err := freezeWorkspaceTransferPayloadKindsV1(input.SendPayloadKinds)
	if err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer send kinds: %w", err,
		)
	}
	receive, err := freezeWorkspaceTransferPayloadKindsV1(input.ReceivePayloadKinds)
	if err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer receive kinds: %w", err,
		)
	}
	if err := validateWorkspaceTransferDirectionalLimitV1(
		"send",
		len(send),
		input.MaxSendPayloadBytes,
	); err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", err
	}
	if err := validateWorkspaceTransferDirectionalLimitV1(
		"receive",
		len(receive),
		input.MaxReceivePayloadBytes,
	); err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", err
	}
	if input.Enabled && len(send) == 0 && len(receive) == 0 {
		return WorkspaceTransferGrantV1{}, nil, "", fmt.Errorf(
			"corecontract: enabled Workspace transfer grant cannot deny both directions",
		)
	}
	frozen := input
	frozen.SendPayloadKinds = send
	frozen.ReceivePayloadKinds = receive
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return WorkspaceTransferGrantV1{}, nil, "", err
	}
	digest := moduleapi.Digest(workspaceTransferGrantDigestDomainV1, canonical)
	return frozen, canonical, digest, nil
}

// WorkspaceTaskSummaryV1 is the only request payload in the V1 transfer
// protocol. It carries one exact source task reference, bounded repair basis,
// and bounded summary text. X1 must construct it in a trusted compiler from
// Store-loaded facts: this structural contract cannot detect whether free-form
// Summary text contains a Secret.
type WorkspaceTaskSummaryV1 struct {
	SchemaVersion      string `json:"schema_version"`
	SourceTaskInputRef string `json:"source_task_input_ref"`
	RepairRound        uint32 `json:"repair_round"`
	PreviousSetDigest  string `json:"previous_set_digest"`
	VerdictRef         string `json:"verdict_ref"`
	Summary            string `json:"summary"`
}

func NewWorkspaceTaskSummaryV1(
	input WorkspaceTaskSummaryV1,
) (WorkspaceTaskSummaryV1, []byte, error) {
	if input.SchemaVersion != WorkspaceTaskSummarySchemaVersionV1 ||
		!moduleapi.ValidSHA256(input.SourceTaskInputRef) ||
		input.RepairRound > 1 ||
		(input.RepairRound == 0 &&
			(input.PreviousSetDigest != "" || input.VerdictRef != "")) ||
		(input.RepairRound == 1 &&
			(!moduleapi.ValidSHA256(input.PreviousSetDigest) ||
				!moduleapi.ValidSHA256(input.VerdictRef))) {
		return WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
			"corecontract: invalid Workspace task summary identity",
		)
	}
	if err := validateCollaborationTextV1(
		input.Summary,
		int(WorkspaceTaskSummaryMaximumPayloadBytesV1),
		"Workspace task summary",
	); err != nil {
		return WorkspaceTaskSummaryV1{}, nil, err
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return WorkspaceTaskSummaryV1{}, nil, err
	}
	if len(canonical) > int(WorkspaceTaskSummaryMaximumPayloadBytesV1) {
		return WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
			"corecontract: Workspace task summary exceeds %d canonical bytes",
			WorkspaceTaskSummaryMaximumPayloadBytesV1,
		)
	}
	return input, canonical, nil
}

func RestoreWorkspaceTaskSummaryV1(
	canonical []byte,
) (WorkspaceTaskSummaryV1, error) {
	if len(canonical) > int(WorkspaceTaskSummaryMaximumPayloadBytesV1) {
		return WorkspaceTaskSummaryV1{}, fmt.Errorf(
			"corecontract: Workspace task summary exceeds %d canonical bytes",
			WorkspaceTaskSummaryMaximumPayloadBytesV1,
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return WorkspaceTaskSummaryV1{}, err
	}
	var decoded WorkspaceTaskSummaryV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return WorkspaceTaskSummaryV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewWorkspaceTaskSummaryV1(decoded)
	if err != nil {
		return WorkspaceTaskSummaryV1{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) {
		return WorkspaceTaskSummaryV1{}, fmt.Errorf(
			"corecontract: Workspace task summary is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func RestoreWorkspaceTransferGrantV1(
	canonical []byte,
	expectedDigest string,
) (WorkspaceTransferGrantV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return WorkspaceTransferGrantV1{}, err
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return WorkspaceTransferGrantV1{}, fmt.Errorf(
			"corecontract: invalid Workspace transfer grant digest",
		)
	}
	var decoded WorkspaceTransferGrantV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return WorkspaceTransferGrantV1{}, err
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err :=
		NewWorkspaceTransferGrantV1(decoded)
	if err != nil {
		return WorkspaceTransferGrantV1{}, err
	}
	if rebuiltDigest != expectedDigest || !bytes.Equal(rebuiltCanonical, canonical) {
		return WorkspaceTransferGrantV1{}, fmt.Errorf(
			"corecontract: Workspace transfer grant is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// WorkspaceTransferEnvelopeV1 is an immutable, content-addressed audit edge.
// PayloadRef points at the sole payload fact; the envelope never copies it.
type WorkspaceTransferEnvelopeV1 struct {
	SchemaVersion        string                         `json:"schema_version"`
	TenantID             string                         `json:"tenant_id"`
	SourceGrantID        string                         `json:"source_grant_id"`
	TargetGrantID        string                         `json:"target_grant_id"`
	Direction            WorkspaceTransferDirectionV1   `json:"direction"`
	PayloadKind          WorkspaceTransferPayloadKindV1 `json:"payload_kind"`
	PayloadSchemaVersion string                         `json:"payload_schema_version"`
	TaskInputRef         string                         `json:"task_input_ref"`
	SourceWorkspace      WorkspaceRef                   `json:"source_workspace"`
	TargetWorkspace      WorkspaceRef                   `json:"target_workspace"`
	SourceGrantDigest    string                         `json:"source_grant_digest"`
	TargetGrantDigest    string                         `json:"target_grant_digest"`
	RootRunID            string                         `json:"root_run_id"`
	ChildRunID           string                         `json:"child_run_id"`
	SlotID               string                         `json:"slot_id"`
	PayloadRef           string                         `json:"payload_ref"`
	PayloadSizeBytes     uint32                         `json:"payload_size_bytes"`
}

func NewWorkspaceTransferEnvelopeV1(
	input WorkspaceTransferEnvelopeV1,
) (WorkspaceTransferEnvelopeV1, []byte, string, error) {
	if input.SchemaVersion != WorkspaceTransferEnvelopeSchemaVersionV1 ||
		!validOpaque(input.TenantID, maxOpaqueIDBytes) ||
		!validOpaque(input.SourceGrantID, maxOpaqueIDBytes) ||
		!validOpaque(input.TargetGrantID, maxOpaqueIDBytes) ||
		input.SourceGrantID == input.TargetGrantID ||
		!validOpaque(input.RootRunID, maxOpaqueIDBytes) ||
		!validOpaque(input.ChildRunID, maxOpaqueIDBytes) ||
		input.RootRunID == input.ChildRunID ||
		!validOpaque(input.SlotID, maxOpaqueIDBytes) ||
		input.SlotID == CompositeReviewerParentSlotIDV1 ||
		!moduleapi.ValidSHA256(input.SourceGrantDigest) ||
		!moduleapi.ValidSHA256(input.TargetGrantDigest) ||
		input.SourceGrantDigest == input.TargetGrantDigest ||
		!moduleapi.ValidSHA256(input.TaskInputRef) ||
		!moduleapi.ValidSHA256(input.PayloadRef) ||
		input.PayloadSizeBytes == 0 ||
		input.PayloadSizeBytes > workspaceTransferPayloadMaximumBytesV1(
			input.PayloadKind,
		) {
		return WorkspaceTransferEnvelopeV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid Workspace transfer envelope identity",
		)
	}
	if err := input.SourceWorkspace.Validate(); err != nil {
		return WorkspaceTransferEnvelopeV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer envelope source: %w", err,
		)
	}
	if err := input.TargetWorkspace.Validate(); err != nil {
		return WorkspaceTransferEnvelopeV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer envelope target: %w", err,
		)
	}
	if input.SourceWorkspace.ID == input.TargetWorkspace.ID {
		return WorkspaceTransferEnvelopeV1{}, nil, "", fmt.Errorf(
			"corecontract: Workspace transfer envelope requires distinct Workspaces",
		)
	}
	if !validWorkspaceTransferPayloadKindV1(input.PayloadKind) ||
		!validWorkspaceTransferDirectionV1(input.Direction) ||
		!workspaceTransferDirectionAllowsPayloadV1(input.Direction, input.PayloadKind) ||
		input.PayloadSchemaVersion !=
			workspaceTransferPayloadSchemaVersionV1(input.PayloadKind) {
		return WorkspaceTransferEnvelopeV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid Workspace transfer direction or payload kind",
		)
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return WorkspaceTransferEnvelopeV1{}, nil, "", err
	}
	digest := moduleapi.Digest(workspaceTransferEnvelopeDigestDomainV1, canonical)
	return input, canonical, digest, nil
}

// WorkspaceTransferResolvedPayloadV1 carries the actual immutable
// ContentRecord fields loaded by the Store. It is deliberately not a wire
// contract: callers must not build it from model-declared metadata.
type WorkspaceTransferResolvedPayloadV1 struct {
	PayloadRef     string
	ContentKind    string
	MediaType      string
	CanonicalBytes []byte
}

// ValidateResolvedPayloadV1 closes an authorized envelope against the actual
// immutable payload. It proves the exact ContentRecord digest, byte size,
// content kind, media type, schema, and strict canonical payload shape.
func (envelope WorkspaceTransferEnvelopeV1) ValidateResolvedPayloadV1(
	resolved WorkspaceTransferResolvedPayloadV1,
) error {
	frozenEnvelope, _, _, err := NewWorkspaceTransferEnvelopeV1(envelope)
	if err != nil {
		return fmt.Errorf("corecontract: Workspace transfer envelope: %w", err)
	}
	wantContentKind := workspaceTransferPayloadContentKindV1(
		frozenEnvelope.PayloadKind,
	)
	if resolved.PayloadRef != frozenEnvelope.PayloadRef ||
		resolved.ContentKind != wantContentKind ||
		resolved.MediaType != workspaceTransferJSONMediaTypeV1 ||
		len(resolved.CanonicalBytes) == 0 ||
		len(resolved.CanonicalBytes) >
			int(WorkspaceTransferMaximumPayloadBytesV1) ||
		uint32(len(resolved.CanonicalBytes)) !=
			frozenEnvelope.PayloadSizeBytes {
		return fmt.Errorf(
			"corecontract: resolved Workspace transfer payload metadata does not close",
		)
	}
	if workspaceTransferContentDigestV1(
		resolved.ContentKind,
		resolved.MediaType,
		resolved.CanonicalBytes,
	) != frozenEnvelope.PayloadRef {
		return fmt.Errorf(
			"corecontract: resolved Workspace transfer payload digest does not close",
		)
	}
	switch frozenEnvelope.PayloadKind {
	case WorkspaceTransferPayloadTaskSummaryV1:
		summary, err := RestoreWorkspaceTaskSummaryV1(resolved.CanonicalBytes)
		if err != nil {
			return fmt.Errorf(
				"corecontract: resolved Workspace transfer task summary: %w",
				err,
			)
		}
		if summary.SourceTaskInputRef != frozenEnvelope.TaskInputRef {
			return fmt.Errorf(
				"corecontract: resolved Workspace transfer task summary does not bind TaskInputRef",
			)
		}
	case WorkspaceTransferPayloadSpecialistResultV1:
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			resolved.CanonicalBytes,
		)
		if err != nil || output.ActionRequest != nil ||
			output.AssistantText == "" {
			return fmt.Errorf(
				"corecontract: resolved Workspace transfer Specialist result is not an assistant MODEL_RESULT",
			)
		}
		contributionCanonical := []byte(output.AssistantText)
		if _, err := RestoreSpecialistContributionV1(
			contributionCanonical,
			moduleapi.Digest(
				specialistContributionDigestDomainV1,
				contributionCanonical,
			),
		); err != nil {
			return fmt.Errorf(
				"corecontract: resolved Workspace transfer Specialist result: %w",
				err,
			)
		}
	default:
		return fmt.Errorf(
			"corecontract: unsupported resolved Workspace transfer payload kind",
		)
	}
	return nil
}

func RestoreWorkspaceTransferEnvelopeV1(
	canonical []byte,
	expectedDigest string,
) (WorkspaceTransferEnvelopeV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return WorkspaceTransferEnvelopeV1{}, err
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return WorkspaceTransferEnvelopeV1{}, fmt.Errorf(
			"corecontract: invalid Workspace transfer envelope digest",
		)
	}
	var decoded WorkspaceTransferEnvelopeV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return WorkspaceTransferEnvelopeV1{}, err
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err :=
		NewWorkspaceTransferEnvelopeV1(decoded)
	if err != nil {
		return WorkspaceTransferEnvelopeV1{}, err
	}
	if rebuiltDigest != expectedDigest || !bytes.Equal(rebuiltCanonical, canonical) {
		return WorkspaceTransferEnvelopeV1{}, fmt.Errorf(
			"corecontract: Workspace transfer envelope is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ValidateAgainstGrantsV1 proves the two independent grant halves and the
// exact send/receive intersection. Disabled or stale grants always fail.
func (envelope WorkspaceTransferEnvelopeV1) ValidateAgainstGrantsV1(
	source WorkspaceTransferGrantV1,
	target WorkspaceTransferGrantV1,
) error {
	frozenEnvelope, _, _, err := NewWorkspaceTransferEnvelopeV1(envelope)
	if err != nil {
		return fmt.Errorf("corecontract: Workspace transfer envelope: %w", err)
	}
	frozenSource, _, sourceDigest, err := NewWorkspaceTransferGrantV1(source)
	if err != nil {
		return fmt.Errorf("corecontract: source Workspace transfer grant: %w", err)
	}
	frozenTarget, _, targetDigest, err := NewWorkspaceTransferGrantV1(target)
	if err != nil {
		return fmt.Errorf("corecontract: target Workspace transfer grant: %w", err)
	}
	if !frozenSource.Enabled || !frozenTarget.Enabled ||
		frozenEnvelope.TenantID != frozenSource.TenantID ||
		frozenEnvelope.TenantID != frozenTarget.TenantID ||
		frozenEnvelope.SourceGrantID != frozenSource.GrantID ||
		frozenEnvelope.TargetGrantID != frozenTarget.GrantID ||
		frozenEnvelope.SourceWorkspace != frozenSource.Workspace ||
		frozenEnvelope.TargetWorkspace != frozenSource.PeerWorkspace ||
		frozenEnvelope.TargetWorkspace != frozenTarget.Workspace ||
		frozenEnvelope.SourceWorkspace != frozenTarget.PeerWorkspace ||
		frozenEnvelope.SourceGrantDigest != sourceDigest ||
		frozenEnvelope.TargetGrantDigest != targetDigest ||
		!workspaceTransferPayloadKindsContainV1(
			frozenSource.SendPayloadKinds,
			frozenEnvelope.PayloadKind,
		) ||
		!workspaceTransferPayloadKindsContainV1(
			frozenTarget.ReceivePayloadKinds,
			frozenEnvelope.PayloadKind,
		) ||
		frozenEnvelope.PayloadSizeBytes > frozenSource.MaxSendPayloadBytes ||
		frozenEnvelope.PayloadSizeBytes > frozenTarget.MaxReceivePayloadBytes {
		return fmt.Errorf(
			"corecontract: Workspace transfer envelope lacks exact bilateral authorization",
		)
	}
	return nil
}

// ValidateAgainstPlanV1 proves that an envelope uses the exact grant refs and
// Workspace direction frozen before the family was admitted. The grant bodies
// must still be loaded from the historical ControlSnapshot and validated with
// ValidateAgainstGrantsV1; a plan ref never grants authority by itself.
func (envelope WorkspaceTransferEnvelopeV1) ValidateAgainstPlanV1(
	plan WorkspaceTransferPlanV1,
) error {
	frozenEnvelope, _, _, err := NewWorkspaceTransferEnvelopeV1(envelope)
	if err != nil {
		return fmt.Errorf("corecontract: Workspace transfer envelope: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	switch frozenEnvelope.Direction {
	case WorkspaceTransferDirectionRequestV1:
		if frozenEnvelope.SourceWorkspace != plan.RootWorkspace ||
			frozenEnvelope.TargetWorkspace != plan.TargetWorkspace ||
			frozenEnvelope.SourceGrantID != plan.RootGrantID ||
			frozenEnvelope.SourceGrantDigest != plan.RootGrantDigest ||
			frozenEnvelope.TargetGrantID != plan.TargetGrantID ||
			frozenEnvelope.TargetGrantDigest != plan.TargetGrantDigest {
			return fmt.Errorf(
				"corecontract: Workspace transfer request does not match the frozen plan",
			)
		}
	case WorkspaceTransferDirectionResultV1:
		if frozenEnvelope.SourceWorkspace != plan.TargetWorkspace ||
			frozenEnvelope.TargetWorkspace != plan.RootWorkspace ||
			frozenEnvelope.SourceGrantID != plan.TargetGrantID ||
			frozenEnvelope.SourceGrantDigest != plan.TargetGrantDigest ||
			frozenEnvelope.TargetGrantID != plan.RootGrantID ||
			frozenEnvelope.TargetGrantDigest != plan.RootGrantDigest {
			return fmt.Errorf(
				"corecontract: Workspace transfer result does not match the frozen plan",
			)
		}
	default:
		return fmt.Errorf(
			"corecontract: unsupported Workspace transfer plan direction",
		)
	}
	return nil
}

// ValidateForCompositeFamilyV1 binds the transfer edge to one exact frozen
// root/Child family. It does not prove the payload ContentRecord, real
// MemberSnapshot, or terminal result: X1 must close those Store-loaded facts
// and also invoke ValidateResolvedPayloadV1.
func (envelope WorkspaceTransferEnvelopeV1) ValidateForCompositeFamilyV1(
	root RunManifest,
	child RunManifest,
) error {
	frozenEnvelope, _, _, err := NewWorkspaceTransferEnvelopeV1(envelope)
	if err != nil {
		return fmt.Errorf("corecontract: Workspace transfer envelope: %w", err)
	}
	frozenRoot, _, err := NewRunManifest(root)
	if err != nil || frozenRoot.ManifestDigest != root.ManifestDigest {
		return fmt.Errorf(
			"corecontract: Workspace transfer root Manifest is not frozen",
		)
	}
	frozenChild, _, err := NewRunManifest(child)
	if err != nil || frozenChild.ManifestDigest != child.ManifestDigest {
		return fmt.Errorf(
			"corecontract: Workspace transfer Child Manifest is not frozen",
		)
	}
	if frozenRoot.Composite == nil ||
		frozenRoot.Composite.Role != CompositeRunRoleRootV1 ||
		frozenRoot.Composite.Plan == nil ||
		frozenChild.Composite == nil ||
		frozenChild.Composite.Role != CompositeRunRoleChildV1 ||
		frozenChild.Composite.Assignment == nil ||
		frozenEnvelope.TenantID != frozenRoot.TenantID ||
		frozenEnvelope.TenantID != frozenChild.TenantID ||
		frozenEnvelope.RootRunID != frozenRoot.RunID ||
		frozenEnvelope.ChildRunID != frozenChild.RunID ||
		frozenChild.ParentRunID != frozenRoot.RunID ||
		frozenChild.Composite.RootRunID != frozenRoot.RunID ||
		frozenChild.Composite.ParentManifestDigest != frozenRoot.ManifestDigest ||
		frozenChild.Composite.Assignment.SlotID != frozenEnvelope.SlotID ||
		frozenEnvelope.TaskInputRef != frozenRoot.TaskInputRef ||
		frozenEnvelope.TaskInputRef != frozenChild.TaskInputRef {
		return fmt.Errorf(
			"corecontract: Workspace transfer envelope does not bind the exact composite family",
		)
	}
	var candidates []CompositeChildRunRefV1
	switch frozenChild.Composite.RepairRound {
	case 0:
		candidates = frozenRoot.Composite.Plan.Children
	case CompositeRepairRoundOneV1:
		if frozenRoot.Composite.Plan.Decision == nil {
			return fmt.Errorf(
				"corecontract: Workspace transfer repair Child lacks a frozen decision plan",
			)
		}
		candidates = frozenRoot.Composite.Plan.Decision.RepairChildren
	default:
		return fmt.Errorf(
			"corecontract: unsupported Workspace transfer repair round",
		)
	}
	var planned *CompositeChildRunRefV1
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.SlotID == frozenEnvelope.SlotID {
			planned = candidate
			break
		}
	}
	if planned == nil ||
		planned.RunID != frozenChild.RunID ||
		planned.AdmissionKey != frozenChild.AdmissionKey ||
		planned.TaskInputRef != frozenEnvelope.TaskInputRef ||
		planned.MemberSnapshotDigest != frozenChild.Members[0].Digest ||
		planned.Agent != frozenChild.PrimaryAgent ||
		frozenChild.Composite.Assignment == nil ||
		*frozenChild.Composite.Assignment != planned.Assignment ||
		(frozenChild.Composite.RepairRound == 0 &&
			frozenChild.Composite.ParentSlotID != planned.SlotID) ||
		(frozenChild.Composite.RepairRound == CompositeRepairRoundOneV1 &&
			frozenChild.Composite.ParentSlotID != planned.ParentSlotID) ||
		planned.Transfer == nil {
		return fmt.Errorf(
			"corecontract: Workspace transfer Child differs from the frozen root plan",
		)
	}
	if planned.Transfer.RootWorkspace != frozenRoot.Workspace ||
		planned.Transfer.TargetWorkspace != frozenChild.Workspace {
		return fmt.Errorf(
			"corecontract: Workspace transfer plan does not match the frozen family Workspaces",
		)
	}
	if err := frozenEnvelope.ValidateAgainstPlanV1(*planned.Transfer); err != nil {
		return err
	}
	return nil
}

func freezeWorkspaceTransferPayloadKindsV1(
	input []WorkspaceTransferPayloadKindV1,
) ([]WorkspaceTransferPayloadKindV1, error) {
	if len(input) > workspaceTransferPayloadKindCountV1 {
		return nil, fmt.Errorf("payload kind list exceeds its bound")
	}
	// A grant always has two JSON arrays. Normalize nil and empty inputs to []
	// so semantically identical grants cannot acquire distinct null/[] wires.
	frozen := make([]WorkspaceTransferPayloadKindV1, len(input))
	copy(frozen, input)
	sort.Slice(frozen, func(left, right int) bool { return frozen[left] < frozen[right] })
	for index, kind := range frozen {
		if !validWorkspaceTransferPayloadKindV1(kind) ||
			(index > 0 && frozen[index-1] == kind) {
			return nil, fmt.Errorf("payload kinds must be supported and unique")
		}
	}
	return frozen, nil
}

func workspaceTransferPayloadKindsContainV1(
	kinds []WorkspaceTransferPayloadKindV1,
	want WorkspaceTransferPayloadKindV1,
) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func validWorkspaceTransferPayloadKindV1(
	kind WorkspaceTransferPayloadKindV1,
) bool {
	switch kind {
	case WorkspaceTransferPayloadTaskSummaryV1,
		WorkspaceTransferPayloadSpecialistResultV1:
		return true
	default:
		return false
	}
}

func validWorkspaceTransferDirectionV1(
	direction WorkspaceTransferDirectionV1,
) bool {
	return direction == WorkspaceTransferDirectionRequestV1 ||
		direction == WorkspaceTransferDirectionResultV1
}

func validateWorkspaceTransferDirectionalLimitV1(
	direction string,
	kindCount int,
	limit uint32,
) error {
	if (kindCount == 0 && limit != 0) ||
		(kindCount != 0 &&
			(limit == 0 || limit > WorkspaceTransferMaximumPayloadBytesV1)) {
		return fmt.Errorf(
			"corecontract: Workspace transfer %s limit does not match its payload kinds",
			direction,
		)
	}
	return nil
}

func workspaceTransferDirectionAllowsPayloadV1(
	direction WorkspaceTransferDirectionV1,
	kind WorkspaceTransferPayloadKindV1,
) bool {
	switch direction {
	case WorkspaceTransferDirectionRequestV1:
		return kind == WorkspaceTransferPayloadTaskSummaryV1
	case WorkspaceTransferDirectionResultV1:
		return kind == WorkspaceTransferPayloadSpecialistResultV1
	default:
		return false
	}
}

func workspaceTransferPayloadSchemaVersionV1(
	kind WorkspaceTransferPayloadKindV1,
) string {
	switch kind {
	case WorkspaceTransferPayloadTaskSummaryV1:
		return WorkspaceTaskSummarySchemaVersionV1
	case WorkspaceTransferPayloadSpecialistResultV1:
		return SpecialistContributionSchemaVersionV1
	default:
		return ""
	}
}

func workspaceTransferPayloadContentKindV1(
	kind WorkspaceTransferPayloadKindV1,
) string {
	switch kind {
	case WorkspaceTransferPayloadTaskSummaryV1:
		return workspaceTransferPayloadRecordKindV1
	case WorkspaceTransferPayloadSpecialistResultV1:
		return workspaceTransferModelResultKindV1
	default:
		return ""
	}
}

func workspaceTransferPayloadMaximumBytesV1(
	kind WorkspaceTransferPayloadKindV1,
) uint32 {
	switch kind {
	case WorkspaceTransferPayloadTaskSummaryV1:
		return WorkspaceTaskSummaryMaximumPayloadBytesV1
	case WorkspaceTransferPayloadSpecialistResultV1:
		return WorkspaceTransferMaximumPayloadBytesV1
	default:
		return 0
	}
}

func workspaceTransferContentDigestV1(
	kind string,
	mediaType string,
	canonical []byte,
) string {
	preimage := make([]byte, 0, len(kind)+len(mediaType)+len(canonical)+2)
	preimage = append(preimage, kind...)
	preimage = append(preimage, 0)
	preimage = append(preimage, mediaType...)
	preimage = append(preimage, 0)
	preimage = append(preimage, canonical...)
	return moduleapi.Digest(workspaceTransferContentRecordDomainV1, preimage)
}
