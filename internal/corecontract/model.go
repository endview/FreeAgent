package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const modelOperationDigestDomain = "freeagent.model-operation/v1"

const (
	ModelDispatchEventSchemaVersionV1 = "model-dispatch-event/v1"
	ModelDispatchPendingEventKind     = "MODEL_DISPATCH_PENDING"
	ModelDispatchTerminalEventKind    = "MODEL_DISPATCH_TERMINAL"
)

// ModelAttemptState is the complete authoritative S1 state set.
type ModelAttemptState string

const (
	ModelAttemptPending   ModelAttemptState = "PENDING"
	ModelAttemptSucceeded ModelAttemptState = "SUCCEEDED"
	ModelAttemptFailed    ModelAttemptState = "FAILED"
	ModelAttemptUnknown   ModelAttemptState = "MODEL_UNKNOWN"
)

func (state ModelAttemptState) Validate() error {
	switch state {
	case ModelAttemptPending,
		ModelAttemptSucceeded,
		ModelAttemptFailed,
		ModelAttemptUnknown:
		return nil
	default:
		return fmt.Errorf("corecontract: invalid model attempt state %q", state)
	}
}

// AllowsTransition returns only real state transitions. Rewriting a state to
// itself is not a transition; an idempotent Store operation must instead
// verify the existing record byte-for-byte.
func (state ModelAttemptState) AllowsTransition(next ModelAttemptState) bool {
	switch state {
	case ModelAttemptPending:
		return next == ModelAttemptSucceeded ||
			next == ModelAttemptFailed ||
			next == ModelAttemptUnknown
	case ModelAttemptUnknown:
		return next == ModelAttemptSucceeded || next == ModelAttemptFailed
	default:
		return false
	}
}

// ModelLogicalOperationKey binds one semantic step independently of its
// provider Binding and RequestDigest. Excluding those values prevents a
// rebuilt request or provider switch from bypassing MODEL_UNKNOWN.
func ModelLogicalOperationKey(
	runID string,
	memberID string,
	logicalStepID string,
) (string, error) {
	for name, value := range map[string]string{
		"run ID":          runID,
		"member ID":       memberID,
		"logical step ID": logicalStepID,
	} {
		if !validOpaque(value, maxOpaqueIDBytes) {
			return "", fmt.Errorf("corecontract: invalid %s", name)
		}
	}
	identity := struct {
		RunID         string `json:"run_id"`
		MemberID      string `json:"member_id"`
		LogicalStepID string `json:"logical_step_id"`
	}{
		RunID:         runID,
		MemberID:      memberID,
		LogicalStepID: logicalStepID,
	}
	canonical, err := canonicalJSON(identity)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(modelOperationDigestDomain, canonical), nil
}

// UsageTokens keeps every token field independent. nil means UNKNOWN; a
// non-nil zero means the provider explicitly reported zero.
type UsageTokens struct {
	Input         *uint64 `json:"input_tokens"`
	CachedInput   *uint64 `json:"cached_input_tokens"`
	UncachedInput *uint64 `json:"uncached_input_tokens"`
	Output        *uint64 `json:"output_tokens"`
	Reasoning     *uint64 `json:"reasoning_tokens"`
}

func (usage UsageTokens) Validate() error {
	for name, value := range map[string]*uint64{
		"input":          usage.Input,
		"cached input":   usage.CachedInput,
		"uncached input": usage.UncachedInput,
		"output":         usage.Output,
		"reasoning":      usage.Reasoning,
	} {
		if value != nil && *value > math.MaxInt64 {
			return fmt.Errorf(
				"corecontract: %s tokens exceed Current Store integer range",
				name,
			)
		}
	}
	if usage.Input != nil &&
		usage.CachedInput != nil &&
		usage.UncachedInput != nil &&
		*usage.Input != *usage.CachedInput+*usage.UncachedInput {
		return fmt.Errorf(
			"corecontract: input tokens do not equal cached plus uncached input",
		)
	}
	return nil
}

// Clone prevents provider adapters or observers from mutating authoritative
// Usage values after normalization.
func (usage UsageTokens) Clone() UsageTokens {
	return UsageTokens{
		Input:         cloneUint64(usage.Input),
		CachedInput:   cloneUint64(usage.CachedInput),
		UncachedInput: cloneUint64(usage.UncachedInput),
		Output:        cloneUint64(usage.Output),
		Reasoning:     cloneUint64(usage.Reasoning),
	}
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// ModelDispatchEventV1 is the small authoritative RunEvent payload for one
// frozen attempt. Provider receipts, result bytes, and Usage remain in their
// own content/ledger records rather than being duplicated into the event.
type ModelDispatchEventV1 struct {
	SchemaVersion          string                     `json:"schema_version"`
	RunID                  string                     `json:"run_id"`
	AttemptID              string                     `json:"attempt_id"`
	LogicalStepID          string                     `json:"logical_step_id"`
	LogicalOperationKey    string                     `json:"logical_operation_key"`
	RequestDigest          string                     `json:"request_digest"`
	State                  ModelAttemptState          `json:"state"`
	ResultDigest           string                     `json:"result_digest,omitempty"`
	TransitionOrigin       DispatchTransitionOriginV1 `json:"transition_origin"`
	Usage                  *ModelUsageEventV1         `json:"usage,omitempty"`
	ResourceSemanticDigest string                     `json:"resource_semantic_digest"`
}

// DispatchTransitionOriginV1 is the immutable provenance of an Attempt event.
// It prevents an ordinary completion from being re-labelled as startup
// recovery (or vice versa) by rewriting only derived observation ledgers.
type DispatchTransitionOriginV1 string

const (
	DispatchTransitionBeginV1                DispatchTransitionOriginV1 = "BEGIN"
	DispatchTransitionOrdinaryOutcomeV1      DispatchTransitionOriginV1 = "ORDINARY_OUTCOME"
	DispatchTransitionStartupRecoveryV1      DispatchTransitionOriginV1 = "STARTUP_RECOVERY"
	DispatchTransitionExpiredBeforeNetworkV1 DispatchTransitionOriginV1 = "EXPIRED_BEFORE_NETWORK"
)

func (origin DispatchTransitionOriginV1) Validate() error {
	switch origin {
	case DispatchTransitionBeginV1, DispatchTransitionOrdinaryOutcomeV1,
		DispatchTransitionStartupRecoveryV1, DispatchTransitionExpiredBeforeNetworkV1:
		return nil
	default:
		return fmt.Errorf("corecontract: invalid dispatch transition origin %q", origin)
	}
}

// ModelUsageEventV1 freezes the bounded Overview-visible Usage projection and
// a digest of the complete typed Usage record at the same terminal mutation.
// The latter binds receipt and cost facts without copying them into RunEvent.
type ModelUsageEventV1 struct {
	Revision             uint64      `json:"revision"`
	LedgerSequence       *uint64     `json:"ledger_sequence,omitempty"`
	ReconciliationStatus string      `json:"reconciliation_status"`
	Tokens               UsageTokens `json:"tokens"`
	SemanticDigest       string      `json:"semantic_digest"`
}

func (usage ModelUsageEventV1) Validate() error {
	if usage.ReconciliationStatus == "" ||
		!moduleapi.ValidSHA256(usage.SemanticDigest) || usage.Tokens.Validate() != nil ||
		(usage.LedgerSequence != nil && *usage.LedgerSequence == 0) {
		return fmt.Errorf("corecontract: invalid model Usage event")
	}
	return nil
}

func NewModelDispatchEventV1(
	input ModelDispatchEventV1,
) (ModelDispatchEventV1, []byte, error) {
	if input.SchemaVersion != ModelDispatchEventSchemaVersionV1 {
		return ModelDispatchEventV1{}, nil, fmt.Errorf(
			"corecontract: model event schema version must be %q",
			ModelDispatchEventSchemaVersionV1,
		)
	}
	for name, value := range map[string]string{
		"run ID":          input.RunID,
		"attempt ID":      input.AttemptID,
		"logical step ID": input.LogicalStepID,
	} {
		if !validOpaque(value, maxOpaqueIDBytes) {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: invalid model event %s",
				name,
			)
		}
	}
	if !moduleapi.ValidSHA256(input.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(input.RequestDigest) ||
		!moduleapi.ValidSHA256(input.ResourceSemanticDigest) {
		return ModelDispatchEventV1{}, nil, fmt.Errorf(
			"corecontract: model event requires operation and request digests",
		)
	}
	if err := input.State.Validate(); err != nil {
		return ModelDispatchEventV1{}, nil, err
	}
	if err := input.TransitionOrigin.Validate(); err != nil {
		return ModelDispatchEventV1{}, nil, err
	}
	switch input.State {
	case ModelAttemptPending:
		if input.TransitionOrigin != DispatchTransitionBeginV1 || input.Usage != nil {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: pending model event requires BEGIN origin without Usage",
			)
		}
		if input.ResultDigest != "" {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: model event state %s cannot carry a result digest",
				input.State,
			)
		}
	case ModelAttemptFailed, ModelAttemptUnknown:
		if input.ResultDigest != "" {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: model event state %s cannot carry a result digest",
				input.State,
			)
		}
	case ModelAttemptSucceeded:
		if !moduleapi.ValidSHA256(input.ResultDigest) {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: successful model event requires result digest",
			)
		}
	}
	if input.State != ModelAttemptPending {
		if input.TransitionOrigin == DispatchTransitionBeginV1 || input.Usage == nil ||
			input.Usage.Validate() != nil {
			return ModelDispatchEventV1{}, nil, fmt.Errorf(
				"corecontract: terminal model event requires origin and Usage",
			)
		}
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return ModelDispatchEventV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreModelDispatchEventV1(
	canonical json.RawMessage,
) (ModelDispatchEventV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ModelDispatchEventV1{}, err
	}
	var decoded ModelDispatchEventV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ModelDispatchEventV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewModelDispatchEventV1(decoded)
	if err != nil {
		return ModelDispatchEventV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return ModelDispatchEventV1{}, fmt.Errorf(
			"corecontract: model event is not frozen canonically",
		)
	}
	return rebuilt, nil
}
