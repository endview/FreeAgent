package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ActionProposalSchemaVersionV1 = "action-proposal/v1"
	ActionResultSchemaVersionV1   = "action-result/v1"

	ActionResultContextSchemaVersionV1 = "action-result-context/v1"
	UntrustedActionResultPrefixV1      = "UNTRUSTED_ACTION_RESULT_JSON:"

	actionDefinitionDigestDomainV1 = "freeagent.action-definition/v1"
	actionProposalContentKindV1    = "ACTION_PROPOSAL"
	actionResultContentKindV1      = "ACTION_RESULT"
	actionContentMediaTypeV1       = "application/json"
	actionResultMaximumDepthV1     = 128
	actionResultMaximumNodesV1     = 64 << 10
)

// FrozenActionDefinitionV1 is the consumer-owned public alias and effective
// authority closure for one Provider definition. BindingIndex is covered by
// the MemberSnapshotDigest; DefinitionDigest intentionally covers only the
// reusable semantic definition fields.
type FrozenActionDefinitionV1 struct {
	PublicActionID   string                `json:"public_action_id"`
	ProviderActionID string                `json:"provider_action_id"`
	BindingIndex     uint32                `json:"binding_index"`
	Description      string                `json:"description"`
	InputSchema      json.RawMessage       `json:"input_schema"`
	EffectClass      moduleapi.EffectClass `json:"effect_class"`
	MaxResultBytes   uint32                `json:"max_result_bytes"`
	DefinitionDigest string                `json:"definition_digest"`
}

// NewFrozenActionDefinitionV1 validates, canonicalizes and defensively owns
// one effective Action definition. An empty DefinitionDigest is populated;
// a supplied digest must already match.
func NewFrozenActionDefinitionV1(
	input FrozenActionDefinitionV1,
) (FrozenActionDefinitionV1, []byte, error) {
	mapping := moduleapi.ActionBindingMappingV1{
		PublicActionID:   input.PublicActionID,
		ProviderActionID: input.ProviderActionID,
		LocalEffectClass: input.EffectClass,
		MaxResultBytes:   input.MaxResultBytes,
	}
	if err := mapping.Validate(); err != nil {
		return FrozenActionDefinitionV1{}, nil, fmt.Errorf(
			"corecontract: frozen Action mapping: %w",
			err,
		)
	}
	providerDefinition, _, err := moduleapi.NewActionDefinitionV1(
		moduleapi.ActionDefinitionV1{
			ProviderActionID:        input.ProviderActionID,
			Description:             input.Description,
			InputSchema:             input.InputSchema,
			RequestedEffectClass:    input.EffectClass,
			RequestedMaxResultBytes: input.MaxResultBytes,
		},
	)
	if err != nil {
		return FrozenActionDefinitionV1{}, nil, fmt.Errorf(
			"corecontract: frozen Action definition: %w",
			err,
		)
	}

	frozen := input
	frozen.Description = providerDefinition.Description
	frozen.InputSchema = bytes.Clone(providerDefinition.InputSchema)
	frozen.DefinitionDigest = ""
	identityCanonical, err := canonicalActionDefinitionIdentityV1(frozen)
	if err != nil {
		return FrozenActionDefinitionV1{}, nil, err
	}
	digest := moduleapi.Digest(
		actionDefinitionDigestDomainV1,
		identityCanonical,
	)
	if input.DefinitionDigest != "" && input.DefinitionDigest != digest {
		return FrozenActionDefinitionV1{}, nil, fmt.Errorf(
			"corecontract: frozen Action DefinitionDigest mismatch",
		)
	}
	frozen.DefinitionDigest = digest
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return FrozenActionDefinitionV1{}, nil, err
	}
	return cloneFrozenActionDefinitionV1(frozen), bytes.Clone(canonical), nil
}

// RestoreFrozenActionDefinitionV1 accepts only the exact canonical frozen
// definition and verifies its self-derived DefinitionDigest.
func RestoreFrozenActionDefinitionV1(
	canonical []byte,
) (FrozenActionDefinitionV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return FrozenActionDefinitionV1{}, err
	}
	var decoded FrozenActionDefinitionV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return FrozenActionDefinitionV1{}, err
	}
	restored, rebuilt, err := NewFrozenActionDefinitionV1(decoded)
	if err != nil {
		return FrozenActionDefinitionV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return FrozenActionDefinitionV1{}, fmt.Errorf(
			"corecontract: frozen Action definition is not canonical",
		)
	}
	return restored, nil
}

func canonicalActionDefinitionIdentityV1(
	definition FrozenActionDefinitionV1,
) ([]byte, error) {
	return canonicalJSON(struct {
		PublicActionID   string                `json:"public_action_id"`
		ProviderActionID string                `json:"provider_action_id"`
		Description      string                `json:"description"`
		InputSchema      json.RawMessage       `json:"input_schema"`
		EffectClass      moduleapi.EffectClass `json:"effect_class"`
		MaxResultBytes   uint32                `json:"max_result_bytes"`
	}{
		PublicActionID:   definition.PublicActionID,
		ProviderActionID: definition.ProviderActionID,
		Description:      definition.Description,
		InputSchema:      definition.InputSchema,
		EffectClass:      definition.EffectClass,
		MaxResultBytes:   definition.MaxResultBytes,
	})
}

func cloneFrozenActionDefinitionV1(
	definition FrozenActionDefinitionV1,
) FrozenActionDefinitionV1 {
	cloned := definition
	cloned.InputSchema = bytes.Clone(definition.InputSchema)
	return cloned
}

// ActionProposalV1 is the Core-owned, content-addressed closure of one model
// Action request and one read-only Prepare result.
type ActionProposalV1 struct {
	SchemaVersion        string          `json:"schema_version"`
	MemberSnapshotDigest string          `json:"member_snapshot_digest"`
	PublicActionID       string          `json:"public_action_id"`
	ProviderActionID     string          `json:"provider_action_id"`
	DefinitionDigest     string          `json:"definition_digest"`
	CanonicalInput       json.RawMessage `json:"canonical_input"`
	PreparedPayload      json.RawMessage `json:"prepared_payload"`
}

// NewActionProposalV1 binds caller-supplied data to one frozen definition by
// construction; the Provider cannot inject routing, Effect, Attempt or
// snapshot identity into the Proposal.
func NewActionProposalV1(
	memberSnapshotDigest string,
	definition FrozenActionDefinitionV1,
	input json.RawMessage,
	preparedPayload json.RawMessage,
) (ActionProposalV1, []byte, string, error) {
	if !moduleapi.ValidSHA256(memberSnapshotDigest) {
		return ActionProposalV1{}, nil, "", fmt.Errorf(
			"corecontract: Action Proposal requires a valid MemberSnapshotDigest",
		)
	}
	frozenDefinition, _, err := NewFrozenActionDefinitionV1(definition)
	if err != nil {
		return ActionProposalV1{}, nil, "", err
	}
	canonicalInput, err := moduleapi.CanonicalizeAndValidateActionInputV1(
		frozenDefinition.InputSchema,
		input,
	)
	if err != nil {
		return ActionProposalV1{}, nil, "", fmt.Errorf(
			"corecontract: Action Proposal input: %w",
			err,
		)
	}
	canonicalPrepared, err := moduleapi.CanonicalizeActionPreparedPayloadV1(
		preparedPayload,
	)
	if err != nil {
		return ActionProposalV1{}, nil, "", fmt.Errorf(
			"corecontract: Action Proposal prepared payload: %w",
			err,
		)
	}
	proposal := ActionProposalV1{
		SchemaVersion:        ActionProposalSchemaVersionV1,
		MemberSnapshotDigest: memberSnapshotDigest,
		PublicActionID:       frozenDefinition.PublicActionID,
		ProviderActionID:     frozenDefinition.ProviderActionID,
		DefinitionDigest:     frozenDefinition.DefinitionDigest,
		CanonicalInput:       bytes.Clone(canonicalInput),
		PreparedPayload:      bytes.Clone(canonicalPrepared),
	}
	canonical, err := canonicalJSON(proposal)
	if err != nil {
		return ActionProposalV1{}, nil, "", err
	}
	digest := contentDigest(
		actionProposalContentKindV1,
		actionContentMediaTypeV1,
		canonical,
	)
	return cloneActionProposalV1(proposal), bytes.Clone(canonical), digest, nil
}

// RestoreActionProposalV1 verifies exact bytes, content identity, snapshot
// identity and the frozen public-to-provider mapping.
func RestoreActionProposalV1(
	canonical []byte,
	expectedContentDigest string,
	expectedMemberSnapshotDigest string,
	definition FrozenActionDefinitionV1,
) (ActionProposalV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ActionProposalV1{}, err
	}
	var decoded ActionProposalV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ActionProposalV1{}, err
	}
	if decoded.SchemaVersion != ActionProposalSchemaVersionV1 {
		return ActionProposalV1{}, fmt.Errorf(
			"corecontract: unsupported Action Proposal schema version",
		)
	}
	restored, rebuilt, digest, err := NewActionProposalV1(
		expectedMemberSnapshotDigest,
		definition,
		decoded.CanonicalInput,
		decoded.PreparedPayload,
	)
	if err != nil {
		return ActionProposalV1{}, err
	}
	if expectedContentDigest != digest ||
		!bytes.Equal(rebuilt, canonical) {
		return ActionProposalV1{}, fmt.Errorf(
			"corecontract: Action Proposal does not match its frozen identities",
		)
	}
	return cloneActionProposalV1(restored), nil
}

func cloneActionProposalV1(input ActionProposalV1) ActionProposalV1 {
	cloned := input
	cloned.CanonicalInput = bytes.Clone(input.CanonicalInput)
	cloned.PreparedPayload = bytes.Clone(input.PreparedPayload)
	return cloned
}

// ActionResultStatusV1 is the complete Core-owned result exposure set.
// RESULT_REJECTED records a confirmed Effect whose result cannot safely be
// exposed; it is not an UNKNOWN execution outcome.
type ActionResultStatusV1 string

const (
	ActionResultAvailable ActionResultStatusV1 = "AVAILABLE"
	ActionResultRejected  ActionResultStatusV1 = "RESULT_REJECTED"
)

func (status ActionResultStatusV1) Validate() error {
	switch status {
	case ActionResultAvailable, ActionResultRejected:
		return nil
	default:
		return fmt.Errorf("corecontract: unsupported Action result status %q", status)
	}
}

// ActionResultV1 is the sole ACTION_RESULT content body. Attempt, Provider,
// receipt, time and route identities remain in their authoritative records.
type ActionResultV1 struct {
	SchemaVersion       string               `json:"schema_version"`
	PublicActionID      string               `json:"public_action_id"`
	DefinitionDigest    string               `json:"definition_digest"`
	Status              ActionResultStatusV1 `json:"status"`
	Result              json.RawMessage      `json:"result,omitempty"`
	ErrorClassification string               `json:"error_classification,omitempty"`
}

func NewAvailableActionResultV1(
	definition FrozenActionDefinitionV1,
	canonicalResult json.RawMessage,
) (ActionResultV1, []byte, string, error) {
	return newActionResultV1(
		definition,
		ActionResultAvailable,
		canonicalResult,
		"",
	)
}

func NewRejectedActionResultV1(
	definition FrozenActionDefinitionV1,
	errorClassification string,
) (ActionResultV1, []byte, string, error) {
	return newActionResultV1(
		definition,
		ActionResultRejected,
		nil,
		errorClassification,
	)
}

func newActionResultV1(
	definition FrozenActionDefinitionV1,
	status ActionResultStatusV1,
	canonicalResult json.RawMessage,
	errorClassification string,
) (ActionResultV1, []byte, string, error) {
	frozenDefinition, _, err := NewFrozenActionDefinitionV1(definition)
	if err != nil {
		return ActionResultV1{}, nil, "", err
	}
	if err := status.Validate(); err != nil {
		return ActionResultV1{}, nil, "", err
	}
	result := ActionResultV1{
		SchemaVersion:       ActionResultSchemaVersionV1,
		PublicActionID:      frozenDefinition.PublicActionID,
		DefinitionDigest:    frozenDefinition.DefinitionDigest,
		Status:              status,
		ErrorClassification: errorClassification,
	}
	switch status {
	case ActionResultAvailable:
		if errorClassification != "" {
			return ActionResultV1{}, nil, "", fmt.Errorf(
				"corecontract: AVAILABLE Action result cannot carry an error classification",
			)
		}
		checked, err := validateCanonicalActionResultV1(
			canonicalResult,
			frozenDefinition.MaxResultBytes,
		)
		if err != nil {
			return ActionResultV1{}, nil, "", err
		}
		result.Result = checked
	case ActionResultRejected:
		if len(canonicalResult) != 0 ||
			!validOpaque(errorClassification, maxOpaqueIDBytes) {
			return ActionResultV1{}, nil, "", fmt.Errorf(
				"corecontract: RESULT_REJECTED requires one canonical error classification and no result",
			)
		}
		result.Result = nil
	}
	canonical, err := canonicalJSON(result)
	if err != nil {
		return ActionResultV1{}, nil, "", err
	}
	digest := contentDigest(
		actionResultContentKindV1,
		actionContentMediaTypeV1,
		canonical,
	)
	return cloneActionResultV1(result), bytes.Clone(canonical), digest, nil
}

func RestoreActionResultV1(
	canonical []byte,
	expectedContentDigest string,
	definition FrozenActionDefinitionV1,
) (ActionResultV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ActionResultV1{}, err
	}
	var decoded ActionResultV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ActionResultV1{}, err
	}
	if decoded.SchemaVersion != ActionResultSchemaVersionV1 {
		return ActionResultV1{}, fmt.Errorf(
			"corecontract: unsupported Action result schema version",
		)
	}
	var (
		restored ActionResultV1
		rebuilt  []byte
		digest   string
		err      error
	)
	switch decoded.Status {
	case ActionResultAvailable:
		restored, rebuilt, digest, err = NewAvailableActionResultV1(
			definition,
			decoded.Result,
		)
	case ActionResultRejected:
		restored, rebuilt, digest, err = NewRejectedActionResultV1(
			definition,
			decoded.ErrorClassification,
		)
	default:
		err = decoded.Status.Validate()
	}
	if err != nil {
		return ActionResultV1{}, err
	}
	if expectedContentDigest != digest || !bytes.Equal(rebuilt, canonical) {
		return ActionResultV1{}, fmt.Errorf(
			"corecontract: Action result does not match its frozen identities",
		)
	}
	return cloneActionResultV1(restored), nil
}

func validateCanonicalActionResultV1(
	input json.RawMessage,
	maximum uint32,
) (json.RawMessage, error) {
	if len(input) == 0 || uint32(len(input)) > maximum {
		return nil, fmt.Errorf(
			"corecontract: Action result must contain between 1 and %d canonical bytes",
			maximum,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: int(maximum),
			MaxDepth: actionResultMaximumDepthV1,
			MaxNodes: actionResultMaximumNodesV1,
		},
	)
	if err != nil || !bytes.Equal(canonical, input) {
		return nil, fmt.Errorf(
			"corecontract: Action result is not bounded canonical JSON",
		)
	}
	return bytes.Clone(canonical), nil
}

func cloneActionResultV1(input ActionResultV1) ActionResultV1 {
	cloned := input
	cloned.Result = bytes.Clone(input.Result)
	return cloned
}

type actionResultContextV1 struct {
	SchemaVersion    string          `json:"schema_version"`
	PublicActionID   string          `json:"public_action_id"`
	DefinitionDigest string          `json:"definition_digest"`
	Result           json.RawMessage `json:"result"`
}

// BuildUntrustedActionResultEnvelopeV1 constructs the exact final USER
// message appended for model two. Only AVAILABLE can cross this boundary.
func BuildUntrustedActionResultEnvelopeV1(
	result ActionResultV1,
	definition FrozenActionDefinitionV1,
) (moduleapi.ModelMessageV1, error) {
	frozenDefinition, _, err := NewFrozenActionDefinitionV1(definition)
	if err != nil {
		return moduleapi.ModelMessageV1{}, err
	}
	available, _, _, err := NewAvailableActionResultV1(
		frozenDefinition,
		result.Result,
	)
	if err != nil || result.Status != ActionResultAvailable ||
		result.SchemaVersion != ActionResultSchemaVersionV1 ||
		result.PublicActionID != available.PublicActionID ||
		result.DefinitionDigest != available.DefinitionDigest ||
		result.ErrorClassification != "" {
		return moduleapi.ModelMessageV1{}, fmt.Errorf(
			"corecontract: only the exact AVAILABLE Action result can enter model context",
		)
	}
	canonical, err := canonicalJSON(actionResultContextV1{
		SchemaVersion:    ActionResultContextSchemaVersionV1,
		PublicActionID:   available.PublicActionID,
		DefinitionDigest: available.DefinitionDigest,
		Result:           bytes.Clone(available.Result),
	})
	if err != nil {
		return moduleapi.ModelMessageV1{}, err
	}
	content := UntrustedActionResultPrefixV1 + string(canonical)
	if err := validateChatText("untrusted Action result envelope", content); err != nil {
		return moduleapi.ModelMessageV1{}, err
	}
	return moduleapi.ModelMessageV1{
		Role:    moduleapi.ModelRoleUser,
		Content: content,
	}, nil
}

// ValidateUntrustedActionResultEnvelopeV1 rejects a role, prefix, identity or
// byte drift by rebuilding the envelope solely from the persisted result.
func ValidateUntrustedActionResultEnvelopeV1(
	message moduleapi.ModelMessageV1,
	result ActionResultV1,
	definition FrozenActionDefinitionV1,
) error {
	expected, err := BuildUntrustedActionResultEnvelopeV1(result, definition)
	if err != nil {
		return err
	}
	if message != expected {
		return fmt.Errorf(
			"corecontract: untrusted Action result envelope does not match persisted result",
		)
	}
	return nil
}

// NewActionResultReservationV1 computes the largest complete result envelope
// and conservative model-request increment across all frozen definitions.
// The latter includes the second JSON-string escaping layer of ModelMessageV1
// plus the comma needed to append it to non-empty messages.
func NewActionResultReservationV1(
	definitions []FrozenActionDefinitionV1,
) (ActionResultReservationV1, error) {
	if len(definitions) == 0 ||
		len(definitions) > moduleapi.MaxActionsPerMemberV1 {
		return ActionResultReservationV1{}, fmt.Errorf(
			"corecontract: Action result reservation requires between 1 and %d definitions",
			moduleapi.MaxActionsPerMemberV1,
		)
	}
	var maximumEnvelopeBytes uint64
	var maximumEstimatedTokens uint64
	for index, input := range definitions {
		definition, _, err := NewFrozenActionDefinitionV1(input)
		if err != nil {
			return ActionResultReservationV1{}, fmt.Errorf(
				"corecontract: Action result reservation definition %d: %w",
				index,
				err,
			)
		}
		result, _, _, err := NewAvailableActionResultV1(
			definition,
			worstCaseCanonicalActionResultV1(definition.MaxResultBytes),
		)
		if err != nil {
			return ActionResultReservationV1{}, err
		}
		message, err := BuildUntrustedActionResultEnvelopeV1(
			result,
			definition,
		)
		if err != nil {
			return ActionResultReservationV1{}, err
		}
		envelopeBytes := uint64(len(message.Content))
		if envelopeBytes > maximumEnvelopeBytes {
			maximumEnvelopeBytes = envelopeBytes
		}
		estimatorMessage, err := json.Marshal(message)
		if err != nil {
			return ActionResultReservationV1{}, err
		}
		// A valid model request always has at least one existing message. One
		// comma is therefore the exact array framing increment for appending
		// this complete Action result message.
		estimatedTokens := uint64(len(estimatorMessage) + 1)
		if estimatedTokens > maximumEstimatedTokens {
			maximumEstimatedTokens = estimatedTokens
		}
	}
	reservation := ActionResultReservationV1{
		MaxEnvelopeBytes: maximumEnvelopeBytes,
		EstimatedTokens:  maximumEstimatedTokens,
	}
	if err := reservation.Validate(); err != nil {
		return ActionResultReservationV1{}, err
	}
	return reservation, nil
}

func worstCaseCanonicalActionResultV1(maximum uint32) json.RawMessage {
	if maximum == 1 {
		return json.RawMessage(`0`)
	}
	// RFC 8785 leaves '<' unescaped in the inner canonical result, while the
	// frozen V1 encoding/json upper-bound estimator expands every '<' to the
	// six-byte sequence \u003c at the outer ModelMessage string layer.
	return json.RawMessage(`"` + strings.Repeat("<", int(maximum)-2) + `"`)
}
