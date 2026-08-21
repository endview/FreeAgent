package moduleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	ActionBindingConfigSchemaV1    = "action-binding-config/v1"
	ActionAuthorityCeilingSchemaV1 = "action-authority-ceiling/v1"
	ActionDescribeRequestSchemaV1  = "action-describe-request/v1"
	ActionRequestSchemaV1          = "action-request/v1"
	ActionExecutionRequestSchemaV1 = "action-execution-request/v1"
	ActionExecutionResultSchemaV1  = "action-execution-result/v1"

	MaxActionsPerMemberV1               = 32
	MaxActionDescriptionBytesV1         = 1024
	MaxActionSchemaBytesV1              = 16 << 10
	MaxActionDefinitionAggregateBytesV1 = 256 << 10
	MaxActionResultBytesV1              = 16 << 10
	MaxActionPreparedPayloadBytesV1     = 64 << 10
	MaxActionReceiptBytesV1             = 64 << 10
	maxActionExecutionWireBytesV1       = 2 * MaxConfigBytes
	maxActionRequestWireBytesV1         = MaxTextBytes + MaxConfigBytes
	maxActionDefinitionCanonicalBytesV1 = MaxActionSchemaBytesV1 + MaxConfigBytes
	maxActionBoundedJSONNodesV1         = 64 << 10
	maxActionPreparedOrReceiptDepthV1   = 32
)

// EffectClass is the closed risk order used by action.provider/v1. A Provider
// requests an effect class; only consumer-owned Binding and authority records
// can grant the effective class used by Gateway.
type EffectClass string

const (
	EffectNone              EffectClass = "none"
	EffectReadOnly          EffectClass = "read_only"
	EffectReversibleWrite   EffectClass = "reversible_write"
	EffectIrreversibleWrite EffectClass = "irreversible_write"
)

func (effect EffectClass) Validate() error {
	if _, ok := actionEffectRankV1(effect); !ok {
		return fmt.Errorf("unsupported action effect class %q", effect)
	}
	return nil
}

// ActionEffectAtMostV1 reports whether candidate is no more permissive than
// ceiling in the frozen v1 risk order.
func ActionEffectAtMostV1(candidate, ceiling EffectClass) (bool, error) {
	candidateRank, candidateOK := actionEffectRankV1(candidate)
	ceilingRank, ceilingOK := actionEffectRankV1(ceiling)
	if !candidateOK {
		return false, fmt.Errorf("unsupported action effect class %q", candidate)
	}
	if !ceilingOK {
		return false, fmt.Errorf("unsupported action effect class %q", ceiling)
	}
	return candidateRank <= ceilingRank, nil
}

// HigherActionEffectV1 returns the higher-risk of two valid effect classes.
// Core uses this when combining a Provider request with the consumer's local
// classification before checking the authority ceiling.
func HigherActionEffectV1(left, right EffectClass) (EffectClass, error) {
	leftRank, leftOK := actionEffectRankV1(left)
	rightRank, rightOK := actionEffectRankV1(right)
	if !leftOK {
		return "", fmt.Errorf("unsupported action effect class %q", left)
	}
	if !rightOK {
		return "", fmt.Errorf("unsupported action effect class %q", right)
	}
	if leftRank >= rightRank {
		return left, nil
	}
	return right, nil
}

func actionEffectRankV1(effect EffectClass) (int, bool) {
	switch effect {
	case EffectNone:
		return 0, true
	case EffectReadOnly:
		return 1, true
	case EffectReversibleWrite:
		return 2, true
	case EffectIrreversibleWrite:
		return 3, true
	default:
		return 0, false
	}
}

// ActionBindingMappingV1 is consumer-owned. PublicActionID is the only name
// exposed to a model; ProviderActionID is resolved before Prepare and never
// participates in fallback.
type ActionBindingMappingV1 struct {
	PublicActionID   string      `json:"public_action_id"`
	ProviderActionID string      `json:"provider_action_id"`
	LocalEffectClass EffectClass `json:"local_effect_class"`
	MaxResultBytes   uint32      `json:"max_result_bytes"`
}

func (mapping ActionBindingMappingV1) Validate() error {
	if !validDottedIdentifier(mapping.PublicActionID, MaxIdentifierBytes) {
		return fmt.Errorf("action binding public_action_id %q must be a dotted identifier", mapping.PublicActionID)
	}
	if !validDottedIdentifier(mapping.ProviderActionID, MaxIdentifierBytes) {
		return fmt.Errorf("action binding provider_action_id %q must be a dotted identifier", mapping.ProviderActionID)
	}
	if err := mapping.LocalEffectClass.Validate(); err != nil {
		return err
	}
	return validateActionMaxResultBytesV1("action binding", mapping.MaxResultBytes)
}

// ActionBindingConfigV1 freezes the ordered local aliases, classifications,
// result ceilings and inert Provider parameters for one PortBinding.
type ActionBindingConfigV1 struct {
	SchemaVersion string                   `json:"schema_version"`
	Actions       []ActionBindingMappingV1 `json:"actions"`
	Parameters    json.RawMessage          `json:"parameters"`
}

func (config ActionBindingConfigV1) Validate() error {
	_, _, err := NewActionBindingConfigV1(config)
	return err
}

func NewActionBindingConfigV1(
	input ActionBindingConfigV1,
) (ActionBindingConfigV1, []byte, error) {
	if input.SchemaVersion != ActionBindingConfigSchemaV1 {
		return ActionBindingConfigV1{}, nil, fmt.Errorf(
			"action binding config schema_version must be %q",
			ActionBindingConfigSchemaV1,
		)
	}
	if len(input.Actions) == 0 || len(input.Actions) > MaxActionsPerMemberV1 {
		return ActionBindingConfigV1{}, nil, fmt.Errorf(
			"action binding config must contain between 1 and %d actions",
			MaxActionsPerMemberV1,
		)
	}
	actions := append([]ActionBindingMappingV1(nil), input.Actions...)
	publicIDs := make(map[string]struct{}, len(actions))
	providerIDs := make(map[string]struct{}, len(actions))
	for index, mapping := range actions {
		if err := mapping.Validate(); err != nil {
			return ActionBindingConfigV1{}, nil, fmt.Errorf(
				"action binding config action %d: %w",
				index,
				err,
			)
		}
		if _, duplicate := publicIDs[mapping.PublicActionID]; duplicate {
			return ActionBindingConfigV1{}, nil, fmt.Errorf(
				"action binding config has duplicate public_action_id %q",
				mapping.PublicActionID,
			)
		}
		if _, duplicate := providerIDs[mapping.ProviderActionID]; duplicate {
			return ActionBindingConfigV1{}, nil, fmt.Errorf(
				"action binding config has duplicate provider_action_id %q",
				mapping.ProviderActionID,
			)
		}
		publicIDs[mapping.PublicActionID] = struct{}{}
		providerIDs[mapping.ProviderActionID] = struct{}{}
	}
	parameters, err := canonicalConfig(input.Parameters)
	if err != nil {
		return ActionBindingConfigV1{}, nil, fmt.Errorf(
			"action binding config parameters: %w",
			err,
		)
	}
	frozen := input
	frozen.Actions = actions
	frozen.Parameters = bytes.Clone(parameters)
	canonical, err := marshalCanonicalActionWire(frozen, MaxConfigBytes)
	if err != nil {
		return ActionBindingConfigV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionBindingConfigV1(
	canonical []byte,
) (ActionBindingConfigV1, error) {
	var decoded ActionBindingConfigV1
	if err := decodeExactActionWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return ActionBindingConfigV1{}, err
	}
	restored, rebuilt, err := NewActionBindingConfigV1(decoded)
	if err != nil {
		return ActionBindingConfigV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionBindingConfigV1{}, fmt.Errorf("action binding config is not frozen canonically")
	}
	return restored, nil
}

// ActionAuthorityCeilingV1 is Core-owned. It may deny or narrow a consumer
// request but cannot rewrite a Provider definition or grant new authority.
type ActionAuthorityCeilingV1 struct {
	SchemaVersion            string      `json:"schema_version"`
	TenantID                 string      `json:"tenant_id"`
	AllowedWorkspaceIDs      []string    `json:"allowed_workspace_ids"`
	AllowedProviderActionIDs []string    `json:"allowed_provider_action_ids"`
	MaxEffectClass           EffectClass `json:"max_effect_class"`
	MaxResultBytes           uint32      `json:"max_result_bytes"`
}

func (ceiling ActionAuthorityCeilingV1) Validate() error {
	_, _, err := NewActionAuthorityCeilingV1(ceiling)
	return err
}

func NewActionAuthorityCeilingV1(
	input ActionAuthorityCeilingV1,
) (ActionAuthorityCeilingV1, []byte, error) {
	if input.SchemaVersion != ActionAuthorityCeilingSchemaV1 {
		return ActionAuthorityCeilingV1{}, nil, fmt.Errorf(
			"action authority ceiling schema_version must be %q",
			ActionAuthorityCeilingSchemaV1,
		)
	}
	if input.TenantID == "*" {
		return ActionAuthorityCeilingV1{}, nil, fmt.Errorf("action authority ceiling tenant_id must be exact")
	}
	if err := validateOpaqueID("action authority ceiling tenant_id", input.TenantID); err != nil {
		return ActionAuthorityCeilingV1{}, nil, err
	}
	workspaceIDs, err := normalizeActionWorkspaceIDsV1(input.AllowedWorkspaceIDs)
	if err != nil {
		return ActionAuthorityCeilingV1{}, nil, fmt.Errorf(
			"action authority ceiling allowed_workspace_ids: %w",
			err,
		)
	}
	providerActionIDs, err := normalizeActionProviderIDsV1(input.AllowedProviderActionIDs)
	if err != nil {
		return ActionAuthorityCeilingV1{}, nil, fmt.Errorf(
			"action authority ceiling allowed_provider_action_ids: %w",
			err,
		)
	}
	if err := input.MaxEffectClass.Validate(); err != nil {
		return ActionAuthorityCeilingV1{}, nil, err
	}
	if err := validateActionMaxResultBytesV1("action authority ceiling", input.MaxResultBytes); err != nil {
		return ActionAuthorityCeilingV1{}, nil, err
	}
	frozen := input
	frozen.AllowedWorkspaceIDs = workspaceIDs
	frozen.AllowedProviderActionIDs = providerActionIDs
	canonical, err := marshalCanonicalActionWire(frozen, MaxConfigBytes)
	if err != nil {
		return ActionAuthorityCeilingV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionAuthorityCeilingV1(
	canonical []byte,
) (ActionAuthorityCeilingV1, error) {
	var decoded ActionAuthorityCeilingV1
	if err := decodeExactActionWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return ActionAuthorityCeilingV1{}, err
	}
	restored, rebuilt, err := NewActionAuthorityCeilingV1(decoded)
	if err != nil {
		return ActionAuthorityCeilingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionAuthorityCeilingV1{}, fmt.Errorf("action authority ceiling is not frozen canonically")
	}
	return restored, nil
}

// ActionDescribeRequestV1 deliberately contains no Run, Attempt, routing,
// authority or credential identity. It is stable input for a read-only
// admission-time Describe call.
type ActionDescribeRequestV1 struct {
	SchemaVersion string          `json:"schema_version"`
	Parameters    json.RawMessage `json:"parameters"`
}

func (request ActionDescribeRequestV1) Validate() error {
	_, _, err := NewActionDescribeRequestV1(request)
	return err
}

func NewActionDescribeRequestV1(
	input ActionDescribeRequestV1,
) (ActionDescribeRequestV1, []byte, error) {
	if input.SchemaVersion != ActionDescribeRequestSchemaV1 {
		return ActionDescribeRequestV1{}, nil, fmt.Errorf(
			"action describe request schema_version must be %q",
			ActionDescribeRequestSchemaV1,
		)
	}
	parameters, err := canonicalConfig(input.Parameters)
	if err != nil {
		return ActionDescribeRequestV1{}, nil, fmt.Errorf(
			"action describe request parameters: %w",
			err,
		)
	}
	frozen := input
	frozen.Parameters = bytes.Clone(parameters)
	canonical, err := marshalCanonicalActionWire(frozen, MaxConfigBytes)
	if err != nil {
		return ActionDescribeRequestV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionDescribeRequestV1(
	canonical []byte,
) (ActionDescribeRequestV1, error) {
	var decoded ActionDescribeRequestV1
	if err := decodeExactActionWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return ActionDescribeRequestV1{}, err
	}
	restored, rebuilt, err := NewActionDescribeRequestV1(decoded)
	if err != nil {
		return ActionDescribeRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionDescribeRequestV1{}, fmt.Errorf("action describe request is not frozen canonically")
	}
	return restored, nil
}

// ActionDefinitionV1 is one Provider request. Local aliases, effective effect
// and effective result bounds are absent because only Core can assign them.
type ActionDefinitionV1 struct {
	ProviderActionID        string          `json:"provider_action_id"`
	Description             string          `json:"description"`
	InputSchema             json.RawMessage `json:"input_schema"`
	RequestedEffectClass    EffectClass     `json:"requested_effect_class"`
	RequestedMaxResultBytes uint32          `json:"requested_max_result_bytes"`
}

func (definition ActionDefinitionV1) Validate() error {
	_, _, err := NewActionDefinitionV1(definition)
	return err
}

func NewActionDefinitionV1(
	input ActionDefinitionV1,
) (ActionDefinitionV1, []byte, error) {
	if !validDottedIdentifier(input.ProviderActionID, MaxIdentifierBytes) {
		return ActionDefinitionV1{}, nil, fmt.Errorf(
			"action definition provider_action_id %q must be a dotted identifier",
			input.ProviderActionID,
		)
	}
	if err := validateActionSchemaTextV1(
		"action definition description",
		input.Description,
		MaxActionDescriptionBytesV1,
		false,
	); err != nil {
		return ActionDefinitionV1{}, nil, err
	}
	inputSchema, err := CanonicalizeActionInputSchemaV1(input.InputSchema)
	if err != nil {
		return ActionDefinitionV1{}, nil, err
	}
	if err := input.RequestedEffectClass.Validate(); err != nil {
		return ActionDefinitionV1{}, nil, err
	}
	if err := validateActionMaxResultBytesV1(
		"action definition requested",
		input.RequestedMaxResultBytes,
	); err != nil {
		return ActionDefinitionV1{}, nil, err
	}
	frozen := input
	frozen.InputSchema = bytes.Clone(inputSchema)
	canonical, err := marshalCanonicalActionWire(
		frozen,
		maxActionDefinitionCanonicalBytesV1,
	)
	if err != nil {
		return ActionDefinitionV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionDefinitionV1(
	canonical []byte,
) (ActionDefinitionV1, error) {
	var decoded ActionDefinitionV1
	if err := decodeExactActionWire(
		canonical,
		maxActionDefinitionCanonicalBytesV1,
		&decoded,
	); err != nil {
		return ActionDefinitionV1{}, err
	}
	restored, rebuilt, err := NewActionDefinitionV1(decoded)
	if err != nil {
		return ActionDefinitionV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionDefinitionV1{}, fmt.Errorf("action definition is not frozen canonically")
	}
	return restored, nil
}

// FreezeActionDefinitionsV1 validates one Provider's ordered Describe result.
// The per-member schema+description aggregate is intentionally not enforced
// here because Core must calculate it across all member Bindings.
func FreezeActionDefinitionsV1(
	input []ActionDefinitionV1,
) ([]ActionDefinitionV1, error) {
	if len(input) == 0 || len(input) > MaxActionsPerMemberV1 {
		return nil, fmt.Errorf(
			"action definitions must contain between 1 and %d values",
			MaxActionsPerMemberV1,
		)
	}
	frozen := make([]ActionDefinitionV1, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, definition := range input {
		validated, _, err := NewActionDefinitionV1(definition)
		if err != nil {
			return nil, fmt.Errorf("action definition %d: %w", index, err)
		}
		if _, duplicate := seen[validated.ProviderActionID]; duplicate {
			return nil, fmt.Errorf(
				"action definitions have duplicate provider_action_id %q",
				validated.ProviderActionID,
			)
		}
		seen[validated.ProviderActionID] = struct{}{}
		frozen[index] = validated
	}
	return frozen, nil
}

// ActionRequestV1 is the exact public-to-provider resolution passed to
// Prepare. CanonicalInput is validated against the frozen definition by Core.
type ActionRequestV1 struct {
	SchemaVersion    string          `json:"schema_version"`
	PublicActionID   string          `json:"public_action_id"`
	ProviderActionID string          `json:"provider_action_id"`
	DefinitionDigest string          `json:"definition_digest"`
	CanonicalInput   json.RawMessage `json:"canonical_input"`
}

func (request ActionRequestV1) Validate() error {
	_, _, err := NewActionRequestV1(request)
	return err
}

func NewActionRequestV1(
	input ActionRequestV1,
) (ActionRequestV1, []byte, error) {
	if input.SchemaVersion != ActionRequestSchemaV1 {
		return ActionRequestV1{}, nil, fmt.Errorf(
			"action request schema_version must be %q",
			ActionRequestSchemaV1,
		)
	}
	if !validDottedIdentifier(input.PublicActionID, MaxIdentifierBytes) {
		return ActionRequestV1{}, nil, fmt.Errorf("action request public_action_id must be a dotted identifier")
	}
	if !validDottedIdentifier(input.ProviderActionID, MaxIdentifierBytes) {
		return ActionRequestV1{}, nil, fmt.Errorf("action request provider_action_id must be a dotted identifier")
	}
	if !ValidSHA256(input.DefinitionDigest) {
		return ActionRequestV1{}, nil, fmt.Errorf("action request definition_digest must be a lowercase SHA-256 digest")
	}
	canonicalInput, err := validateCanonicalActionObjectV1(
		"action request canonical_input",
		input.CanonicalInput,
		MaxTextBytes,
		128,
	)
	if err != nil {
		return ActionRequestV1{}, nil, err
	}
	frozen := input
	frozen.CanonicalInput = canonicalInput
	canonical, err := marshalCanonicalActionWire(frozen, maxActionRequestWireBytesV1)
	if err != nil {
		return ActionRequestV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionRequestV1(canonical []byte) (ActionRequestV1, error) {
	var decoded ActionRequestV1
	if err := decodeExactActionWire(canonical, maxActionRequestWireBytesV1, &decoded); err != nil {
		return ActionRequestV1{}, err
	}
	restored, rebuilt, err := NewActionRequestV1(decoded)
	if err != nil {
		return ActionRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionRequestV1{}, fmt.Errorf("action request is not frozen canonically")
	}
	return restored, nil
}

// ActionProviderV1 is the complete public Action port. Execute is
// intentionally absent: only the private Module Host/Gateway path can obtain
// an executor after consuming a persisted DispatchAttempt permit.
type ActionProviderV1 interface {
	Describe(context.Context, ActionDescribeRequestV1) ([]ActionDefinitionV1, error)
	Prepare(context.Context, ActionRequestV1) (json.RawMessage, error)
}

// CanonicalizeActionPreparedPayloadV1 re-normalizes one Provider Prepare
// result before Core creates an Action Proposal.
func CanonicalizeActionPreparedPayloadV1(
	payload json.RawMessage,
) (json.RawMessage, error) {
	return canonicalizeActionObjectV1(
		"action prepared payload",
		payload,
		MaxActionPreparedPayloadBytesV1,
		maxActionPreparedOrReceiptDepthV1,
	)
}

// ActionExecutionOutcomeV1 is terminal for the public execution result wire.
// Reconciliation updates the original private ledger record, not this value.
type ActionExecutionOutcomeV1 string

const (
	ActionExecutionSucceeded ActionExecutionOutcomeV1 = "SUCCEEDED"
	ActionExecutionFailed    ActionExecutionOutcomeV1 = "FAILED"
	ActionExecutionUnknown   ActionExecutionOutcomeV1 = "UNKNOWN"
)

func (outcome ActionExecutionOutcomeV1) Validate() error {
	switch outcome {
	case ActionExecutionSucceeded, ActionExecutionFailed, ActionExecutionUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported action execution outcome %q", outcome)
	}
}

// ActionExecutionRequestV1 is constructed by Gateway only. Its presence in
// the SDK permits exact Host Adapter wires but grants no right to call an
// executor.
type ActionExecutionRequestV1 struct {
	SchemaVersion    string          `json:"schema_version"`
	AttemptID        string          `json:"attempt_id"`
	PublicActionID   string          `json:"public_action_id"`
	ProviderActionID string          `json:"provider_action_id"`
	DefinitionDigest string          `json:"definition_digest"`
	MaxResultBytes   uint32          `json:"max_result_bytes"`
	PreparedPayload  json.RawMessage `json:"prepared_payload"`
}

func (request ActionExecutionRequestV1) Validate() error {
	_, _, err := NewActionExecutionRequestV1(request)
	return err
}

func NewActionExecutionRequestV1(
	input ActionExecutionRequestV1,
) (ActionExecutionRequestV1, []byte, error) {
	if input.SchemaVersion != ActionExecutionRequestSchemaV1 {
		return ActionExecutionRequestV1{}, nil, fmt.Errorf(
			"action execution request schema_version must be %q",
			ActionExecutionRequestSchemaV1,
		)
	}
	if err := validateOpaqueID("action execution request attempt_id", input.AttemptID); err != nil {
		return ActionExecutionRequestV1{}, nil, err
	}
	if !validDottedIdentifier(input.PublicActionID, MaxIdentifierBytes) {
		return ActionExecutionRequestV1{}, nil, fmt.Errorf("action execution request public_action_id must be a dotted identifier")
	}
	if !validDottedIdentifier(input.ProviderActionID, MaxIdentifierBytes) {
		return ActionExecutionRequestV1{}, nil, fmt.Errorf("action execution request provider_action_id must be a dotted identifier")
	}
	if !ValidSHA256(input.DefinitionDigest) {
		return ActionExecutionRequestV1{}, nil, fmt.Errorf("action execution request definition_digest must be a lowercase SHA-256 digest")
	}
	if err := validateActionMaxResultBytesV1("action execution request", input.MaxResultBytes); err != nil {
		return ActionExecutionRequestV1{}, nil, err
	}
	preparedPayload, err := CanonicalizeActionPreparedPayloadV1(input.PreparedPayload)
	if err != nil {
		return ActionExecutionRequestV1{}, nil, err
	}
	frozen := input
	frozen.PreparedPayload = bytes.Clone(preparedPayload)
	canonical, err := marshalCanonicalActionWire(frozen, maxActionExecutionWireBytesV1)
	if err != nil {
		return ActionExecutionRequestV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionExecutionRequestV1(
	canonical []byte,
) (ActionExecutionRequestV1, error) {
	var decoded ActionExecutionRequestV1
	if err := decodeExactActionWire(canonical, maxActionExecutionWireBytesV1, &decoded); err != nil {
		return ActionExecutionRequestV1{}, err
	}
	restored, rebuilt, err := NewActionExecutionRequestV1(decoded)
	if err != nil {
		return ActionExecutionRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionExecutionRequestV1{}, fmt.Errorf("action execution request is not frozen canonically")
	}
	return restored, nil
}

type ActionExecutionResultV1 struct {
	SchemaVersion       string                   `json:"schema_version"`
	AttemptID           string                   `json:"attempt_id"`
	Outcome             ActionExecutionOutcomeV1 `json:"outcome"`
	CanonicalResult     json.RawMessage          `json:"canonical_result,omitempty"`
	ProviderReceipt     json.RawMessage          `json:"provider_receipt,omitempty"`
	ExternalOperationID string                   `json:"external_operation_id,omitempty"`
	ErrorClassification string                   `json:"error_classification,omitempty"`
	UnknownReason       string                   `json:"unknown_reason,omitempty"`
}

func (result ActionExecutionResultV1) Validate() error {
	_, _, err := NewActionExecutionResultV1(result)
	return err
}

func NewActionExecutionResultV1(
	input ActionExecutionResultV1,
) (ActionExecutionResultV1, []byte, error) {
	if input.SchemaVersion != ActionExecutionResultSchemaV1 {
		return ActionExecutionResultV1{}, nil, fmt.Errorf(
			"action execution result schema_version must be %q",
			ActionExecutionResultSchemaV1,
		)
	}
	if err := validateOpaqueID("action execution result attempt_id", input.AttemptID); err != nil {
		return ActionExecutionResultV1{}, nil, err
	}
	if err := input.Outcome.Validate(); err != nil {
		return ActionExecutionResultV1{}, nil, err
	}
	if input.ExternalOperationID != "" {
		if err := validateOpaqueID(
			"action execution result external_operation_id",
			input.ExternalOperationID,
		); err != nil {
			return ActionExecutionResultV1{}, nil, err
		}
	}
	if input.ErrorClassification != "" {
		if err := validateOpaqueID(
			"action execution result error_classification",
			input.ErrorClassification,
		); err != nil {
			return ActionExecutionResultV1{}, nil, err
		}
	}
	if input.UnknownReason != "" {
		if err := validateActionSchemaTextV1(
			"action execution result unknown_reason",
			input.UnknownReason,
			MaxActionDescriptionBytesV1,
			false,
		); err != nil {
			return ActionExecutionResultV1{}, nil, err
		}
	}

	frozen := input
	if len(input.ProviderReceipt) != 0 {
		receipt, err := canonicalizeActionObjectV1(
			"action execution result provider_receipt",
			input.ProviderReceipt,
			MaxActionReceiptBytesV1,
			maxActionPreparedOrReceiptDepthV1,
		)
		if err != nil {
			return ActionExecutionResultV1{}, nil, err
		}
		frozen.ProviderReceipt = receipt
	} else {
		frozen.ProviderReceipt = nil
	}

	switch input.Outcome {
	case ActionExecutionSucceeded:
		if input.ErrorClassification != "" || input.UnknownReason != "" {
			return ActionExecutionResultV1{}, nil, fmt.Errorf(
				"SUCCEEDED action execution result cannot contain error or unknown fields",
			)
		}
		result, err := validateCanonicalActionJSONV1(
			"action execution canonical_result",
			input.CanonicalResult,
			MaxActionResultBytesV1,
			128,
		)
		if err != nil {
			return ActionExecutionResultV1{}, nil, err
		}
		frozen.CanonicalResult = result
	case ActionExecutionFailed:
		if len(input.CanonicalResult) != 0 || input.ErrorClassification == "" || input.UnknownReason != "" {
			return ActionExecutionResultV1{}, nil, fmt.Errorf(
				"FAILED action execution result requires error_classification and cannot contain result or unknown_reason",
			)
		}
		frozen.CanonicalResult = nil
	case ActionExecutionUnknown:
		if len(input.CanonicalResult) != 0 || input.ErrorClassification != "" {
			return ActionExecutionResultV1{}, nil, fmt.Errorf(
				"UNKNOWN action execution result cannot contain final result or error_classification",
			)
		}
		if input.ExternalOperationID == "" &&
			len(frozen.ProviderReceipt) == 0 &&
			input.UnknownReason == "" {
			return ActionExecutionResultV1{}, nil, fmt.Errorf(
				"UNKNOWN action execution result requires external operation, receipt, or unknown_reason",
			)
		}
		frozen.CanonicalResult = nil
	}
	canonical, err := marshalCanonicalActionWire(frozen, maxActionExecutionWireBytesV1)
	if err != nil {
		return ActionExecutionResultV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreActionExecutionResultV1(
	canonical []byte,
) (ActionExecutionResultV1, error) {
	var decoded ActionExecutionResultV1
	if err := decodeExactActionWire(canonical, maxActionExecutionWireBytesV1, &decoded); err != nil {
		return ActionExecutionResultV1{}, err
	}
	restored, rebuilt, err := NewActionExecutionResultV1(decoded)
	if err != nil {
		return ActionExecutionResultV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ActionExecutionResultV1{}, fmt.Errorf("action execution result is not frozen canonically")
	}
	return restored, nil
}

// ValidateActionExecutionResultForRequestV1 additionally closes the exact
// Attempt identity and the effective per-action result ceiling frozen into a
// Gateway request.
func ValidateActionExecutionResultForRequestV1(
	request ActionExecutionRequestV1,
	result ActionExecutionResultV1,
) error {
	frozenRequest, _, err := NewActionExecutionRequestV1(request)
	if err != nil {
		return err
	}
	frozenResult, _, err := NewActionExecutionResultV1(result)
	if err != nil {
		return err
	}
	if frozenResult.AttemptID != frozenRequest.AttemptID {
		return fmt.Errorf("action execution result attempt_id does not match request")
	}
	if frozenResult.Outcome == ActionExecutionSucceeded &&
		uint32(len(frozenResult.CanonicalResult)) > frozenRequest.MaxResultBytes {
		return fmt.Errorf(
			"action execution result exceeds request max_result_bytes %d",
			frozenRequest.MaxResultBytes,
		)
	}
	return nil
}

func validateActionMaxResultBytesV1(name string, maximum uint32) error {
	if maximum == 0 || maximum > MaxActionResultBytesV1 {
		return fmt.Errorf(
			"%s max_result_bytes must be between 1 and %d",
			name,
			MaxActionResultBytesV1,
		)
	}
	return nil
}

func normalizeActionWorkspaceIDsV1(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > MaxManifestEntries {
		return nil, fmt.Errorf(
			"workspace IDs must contain between 1 and %d values",
			MaxManifestEntries,
		)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if value == "*" {
			if len(values) != 1 {
				return nil, fmt.Errorf("workspace wildcard must be the sole value")
			}
			continue
		}
		if err := validateOpaqueID("action workspace ID", value); err != nil {
			return nil, fmt.Errorf("workspace ID %d: %w", index, err)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf("workspace IDs must be unique")
		}
	}
	return values, nil
}

func normalizeActionProviderIDsV1(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > MaxActionsPerMemberV1 {
		return nil, fmt.Errorf(
			"provider action IDs must contain between 1 and %d values",
			MaxActionsPerMemberV1,
		)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if !validDottedIdentifier(value, MaxIdentifierBytes) {
			return nil, fmt.Errorf(
				"provider action ID %d %q must be a dotted identifier",
				index,
				value,
			)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf("provider action IDs must be unique")
		}
	}
	return values, nil
}

func canonicalizeActionObjectV1(
	name string,
	input json.RawMessage,
	maximum int,
	maxDepth int,
) (json.RawMessage, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	canonical, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: maxDepth,
			MaxNodes: maxActionBoundedJSONNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", name)
	}
	if len(canonical) > maximum {
		return nil, fmt.Errorf("%s exceeds %d canonical bytes", name, maximum)
	}
	return bytes.Clone(canonical), nil
}

func validateCanonicalActionObjectV1(
	name string,
	input json.RawMessage,
	maximum int,
	maxDepth int,
) (json.RawMessage, error) {
	canonical, err := canonicalizeActionObjectV1(name, input, maximum, maxDepth)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, input) {
		return nil, fmt.Errorf("%s is not canonical JSON", name)
	}
	return canonical, nil
}

func validateCanonicalActionJSONV1(
	name string,
	input json.RawMessage,
	maximum int,
	maxDepth int,
) (json.RawMessage, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	canonical, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: maxDepth,
			MaxNodes: maxActionBoundedJSONNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !bytes.Equal(canonical, input) {
		return nil, fmt.Errorf("%s is not canonical JSON", name)
	}
	return bytes.Clone(canonical), nil
}

func marshalCanonicalActionWire(value any, maximum int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal action.provider/v1 wire value: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 128,
			MaxNodes: maxActionBoundedJSONNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("canonicalize action.provider/v1 wire value: %w", err)
	}
	return bytes.Clone(canonical), nil
}

func decodeExactActionWire(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf(
			"action.provider/v1 wire value must contain between 1 and %d bytes",
			maximum,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 128,
			MaxNodes: maxActionBoundedJSONNodesV1,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("action.provider/v1 wire value is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode action.provider/v1 wire value: %w", err)
	}
	return nil
}
