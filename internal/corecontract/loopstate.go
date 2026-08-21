package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	InitialRunState                   = "ADMITTED"
	InitialLoopStep                   = "READY"
	WaitingChildrenLoopStep           = "WAITING_CHILDREN"
	WaitingRepairActivationLoopStep   = "WAITING_REPAIR_ACTIVATION"
	ModelPendingLoopStep              = "MODEL_PENDING"
	ActionPendingLoopStep             = "ACTION_PENDING"
	ChannelPendingLoopStep            = "CHANNEL_PENDING"
	ModelReadyAfterActionLoopStep     = "MODEL_READY_AFTER_ACTION"
	WaitingReconciliationLoopStep     = "WAITING_RECONCILIATION"
	TerminatedLoopStep                = "TERMINATED"
	RunAdmittedEventKind              = "RUN_ADMITTED"
	CoreDeterministicFailureEventKind = "CORE_DETERMINISTIC_FAILURE"
	LoopContinuationSchemaVersionV1   = "loop-continuation/v1"
	RunAdmittedPayloadSchemaVersionV1 = "run-admitted-event/v1"
	budgetStateRefPrefixV1            = "usage-ledger/v1/"
	maxInt64DecimalBytes              = 19
	// MaxBudgetStateRefBytesV1 is deliberately larger than the generic opaque
	// ID limit: it includes the protocol prefix, a maximum-length RunID, the
	// sequence separator, and a signed-SQLite-range decimal sequence.
	MaxBudgetStateRefBytesV1 = len(budgetStateRefPrefixV1) +
		maxOpaqueIDBytes + 1 + maxInt64DecimalBytes
	// MaxLoopContinuationCanonicalBytesV1 is a protocol bound, not an
	// implementation tuning knob. A continuation contains at most two opaque
	// identities plus fixed S1 state, schema and reason fields. The generous
	// bound keeps hashing and persistence constant even for maximum-length IDs.
	MaxLoopContinuationCanonicalBytesV1 = 2048
)

// AttemptKindV1 closes the dispatch family named by a restart-safe Loop
// continuation. MODEL is intentionally omitted from the canonical wire so
// continuations written before Action support retain their exact bytes.
type AttemptKindV1 string

const (
	AttemptKindModel   AttemptKindV1 = "MODEL"
	AttemptKindAction  AttemptKindV1 = "ACTION"
	AttemptKindChannel AttemptKindV1 = "CHANNEL"
)

func (kind AttemptKindV1) Validate() error {
	switch kind {
	case AttemptKindModel, AttemptKindAction, AttemptKindChannel:
		return nil
	default:
		return fmt.Errorf("corecontract: unsupported AttemptKind %q", kind)
	}
}

// LoopContinuationV1 is the smallest restart-safe Universal Loop
// continuation. S1 starts every admitted Run at READY.
type LoopContinuationV1 struct {
	SchemaVersion     string        `json:"schema_version"`
	State             string        `json:"state"`
	AttemptKind       AttemptKindV1 `json:"attempt_kind,omitempty"`
	LogicalStepID     string        `json:"logical_step_id,omitempty"`
	AttemptID         string        `json:"attempt_id,omitempty"`
	CoreFailureReason string        `json:"core_failure_reason,omitempty"`
}

// RunAdmittedEventV1 is the fixed event-zero payload. It contains only
// recovery identities and no dynamic timestamp or caller-selected state.
type RunAdmittedEventV1 struct {
	SchemaVersion        string `json:"schema_version"`
	RunID                string `json:"run_id"`
	ManifestDigest       string `json:"manifest_digest"`
	MemberSnapshotDigest string `json:"member_snapshot_digest"`
}

// NewBudgetStateRefV1 constructs the only canonical S1 Usage Ledger
// reference. Its total wire length has a dedicated limit because a reference
// contains a full opaque RunID plus protocol framing.
func NewBudgetStateRefV1(
	runID string,
	sequence uint64,
) (string, error) {
	if !validOpaque(runID, maxOpaqueIDBytes) {
		return "", fmt.Errorf(
			"corecontract: invalid Run ID for BudgetStateRef",
		)
	}
	if sequence > math.MaxInt64 {
		return "", fmt.Errorf(
			"corecontract: BudgetStateRef sequence exceeds Current Store integer range",
		)
	}
	reference := budgetStateRefPrefixV1 +
		runID +
		"/" +
		strconv.FormatUint(sequence, 10)
	if len(reference) > MaxBudgetStateRefBytesV1 {
		return "", fmt.Errorf(
			"corecontract: BudgetStateRef exceeds %d bytes",
			MaxBudgetStateRefBytesV1,
		)
	}
	return reference, nil
}

// ParseBudgetStateRefV1 parses a canonical S1 Usage Ledger reference and
// binds it to the expected Run. RunIDs may themselves contain '/', so the
// sequence is separated using the final slash.
func ParseBudgetStateRefV1(
	reference string,
	expectedRunID string,
) (uint64, error) {
	if len(reference) > MaxBudgetStateRefBytesV1 ||
		!strings.HasPrefix(reference, budgetStateRefPrefixV1) {
		return 0, fmt.Errorf(
			"corecontract: invalid BudgetStateRef format",
		)
	}
	remainder := strings.TrimPrefix(reference, budgetStateRefPrefixV1)
	separator := strings.LastIndexByte(remainder, '/')
	if separator <= 0 || separator == len(remainder)-1 {
		return 0, fmt.Errorf(
			"corecontract: invalid BudgetStateRef format",
		)
	}
	runID := remainder[:separator]
	sequenceText := remainder[separator+1:]
	if !validOpaque(expectedRunID, maxOpaqueIDBytes) ||
		!validOpaque(runID, maxOpaqueIDBytes) ||
		runID != expectedRunID {
		return 0, fmt.Errorf(
			"corecontract: BudgetStateRef does not belong to expected Run",
		)
	}
	if !canonicalUnsignedDecimal(sequenceText) {
		return 0, fmt.Errorf(
			"corecontract: invalid BudgetStateRef sequence",
		)
	}
	sequence, err := strconv.ParseUint(sequenceText, 10, 64)
	if err != nil || sequence > math.MaxInt64 {
		return 0, fmt.Errorf(
			"corecontract: BudgetStateRef sequence exceeds Current Store integer range",
		)
	}
	rebuilt, err := NewBudgetStateRefV1(runID, sequence)
	if err != nil || rebuilt != reference {
		return 0, fmt.Errorf(
			"corecontract: BudgetStateRef is not canonical",
		)
	}
	return sequence, nil
}

func canonicalUnsignedDecimal(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func NewInitialLoopState(
	runID string,
) (string, []byte, error) {
	budgetStateRef, err := NewBudgetStateRefV1(runID, 0)
	if err != nil {
		return "", nil, err
	}
	continuation, err := NewLoopContinuationV1(
		InitialLoopStep,
		"",
		"",
	)
	if err != nil {
		return "", nil, err
	}
	return budgetStateRef,
		continuation,
		nil
}

// NewWaitingChildrenLoopState constructs the initial coordinator frame for a
// composite family. It has no Attempt identity and shares the normal per-Run
// Usage ledger starting at sequence zero.
func NewWaitingChildrenLoopState(runID string) (string, []byte, error) {
	budgetStateRef, err := NewBudgetStateRefV1(runID, 0)
	if err != nil {
		return "", nil, err
	}
	continuation, err := NewLoopContinuationV1(
		WaitingChildrenLoopStep,
		"",
		"",
	)
	if err != nil {
		return "", nil, err
	}
	return budgetStateRef, continuation, nil
}

// NewWaitingRepairActivationLoopState constructs a dormant, restart-safe
// frame for one pre-published round-one repair participant. It carries no
// Attempt identity and therefore cannot authorize a Provider call before the
// Host has persisted and validated a round-zero REPAIR_REQUIRED verdict.
func NewWaitingRepairActivationLoopState(
	runID string,
) (string, []byte, error) {
	budgetStateRef, err := NewBudgetStateRefV1(runID, 0)
	if err != nil {
		return "", nil, err
	}
	continuation, err := NewLoopContinuationV1(
		WaitingRepairActivationLoopStep,
		"",
		"",
	)
	if err != nil {
		return "", nil, err
	}
	return budgetStateRef, continuation, nil
}

// NewLoopContinuationV1 creates the complete S1 continuation state. Attempt
// identity is carried only once a semantic model step exists; READY has no
// hidden identity that could be reconstructed differently after restart.
func NewLoopContinuationV1(
	state string,
	logicalStepID string,
	attemptID string,
) ([]byte, error) {
	kind := AttemptKindModel
	if state == InitialLoopStep || state == WaitingChildrenLoopStep ||
		state == WaitingRepairActivationLoopStep {
		kind = ""
	}
	return NewLoopContinuationForAttemptV1(
		state,
		kind,
		logicalStepID,
		attemptID,
	)
}

// NewLoopContinuationForAttemptV1 creates a continuation for either the
// legacy MODEL dispatch family or the Action dispatch family. Callers must
// preserve the Action Attempt identity through MODEL_READY_AFTER_ACTION so a
// restart cannot silently regenerate or replay an external effect.
func NewLoopContinuationForAttemptV1(
	state string,
	attemptKind AttemptKindV1,
	logicalStepID string,
	attemptID string,
) ([]byte, error) {
	switch state {
	case InitialLoopStep, WaitingChildrenLoopStep,
		WaitingRepairActivationLoopStep:
		if attemptKind != "" || logicalStepID != "" || attemptID != "" {
			return nil, fmt.Errorf(
				"corecontract: %s continuation cannot name an attempt",
				state,
			)
		}
	case ModelPendingLoopStep:
		if attemptKind != AttemptKindModel {
			return nil, fmt.Errorf(
				"corecontract: MODEL_PENDING continuation requires MODEL AttemptKind",
			)
		}
	case ActionPendingLoopStep, ModelReadyAfterActionLoopStep:
		if attemptKind != AttemptKindAction {
			return nil, fmt.Errorf(
				"corecontract: %s continuation requires ACTION AttemptKind",
				state,
			)
		}
	case ChannelPendingLoopStep:
		if attemptKind != AttemptKindChannel {
			return nil, fmt.Errorf(
				"corecontract: CHANNEL_PENDING continuation requires CHANNEL AttemptKind",
			)
		}
	case WaitingReconciliationLoopStep, TerminatedLoopStep:
		if err := attemptKind.Validate(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf(
			"corecontract: unsupported S1 Loop continuation state %q",
			state,
		)
	}
	if state != InitialLoopStep && state != WaitingChildrenLoopStep &&
		state != WaitingRepairActivationLoopStep &&
		(!validOpaque(logicalStepID, maxOpaqueIDBytes) ||
			!validOpaque(attemptID, maxOpaqueIDBytes)) {
		return nil, fmt.Errorf(
			"corecontract: %s continuation requires logical step and attempt identity",
			state,
		)
	}
	return canonicalLoopContinuationV1(LoopContinuationV1{
		SchemaVersion: LoopContinuationSchemaVersionV1,
		State:         state,
		AttemptKind:   attemptKind,
		LogicalStepID: logicalStepID,
		AttemptID:     attemptID,
	})
}

// NewCoreFailureLoopContinuationV1 closes a Run on a deterministic Core
// decision made before any dispatch permit exists. It deliberately carries
// no Attempt identity: the authoritative Core failure event is the source of
// the terminal classification.
func NewCoreFailureLoopContinuationV1(reason string) ([]byte, error) {
	if err := ValidateCoreDeterministicFailureReasonV1(reason); err != nil {
		return nil, err
	}
	return canonicalLoopContinuationV1(LoopContinuationV1{
		SchemaVersion:     LoopContinuationSchemaVersionV1,
		State:             TerminatedLoopStep,
		CoreFailureReason: reason,
	})
}

func canonicalLoopContinuationV1(
	continuation LoopContinuationV1,
) ([]byte, error) {
	// MODEL is the legacy/default dispatch family. Keeping it out of the wire
	// preserves every pre-Action continuation byte-for-byte.
	type wire struct {
		SchemaVersion     string        `json:"schema_version"`
		State             string        `json:"state"`
		AttemptKind       AttemptKindV1 `json:"attempt_kind,omitempty"`
		LogicalStepID     string        `json:"logical_step_id,omitempty"`
		AttemptID         string        `json:"attempt_id,omitempty"`
		CoreFailureReason string        `json:"core_failure_reason,omitempty"`
	}
	kind := continuation.AttemptKind
	if kind == AttemptKindModel {
		kind = ""
	}
	canonical, err := canonicalJSON(wire{
		SchemaVersion:     continuation.SchemaVersion,
		State:             continuation.State,
		AttemptKind:       kind,
		LogicalStepID:     continuation.LogicalStepID,
		AttemptID:         continuation.AttemptID,
		CoreFailureReason: continuation.CoreFailureReason,
	})
	if err != nil {
		return nil, err
	}
	if len(canonical) > MaxLoopContinuationCanonicalBytesV1 {
		return nil, fmt.Errorf(
			"corecontract: Loop continuation exceeds %d bytes",
			MaxLoopContinuationCanonicalBytesV1,
		)
	}
	return canonical, nil
}

func RestoreLoopContinuationV1(
	canonical json.RawMessage,
) (LoopContinuationV1, error) {
	if len(canonical) == 0 || len(canonical) > MaxLoopContinuationCanonicalBytesV1 {
		return LoopContinuationV1{}, fmt.Errorf(
			"corecontract: Loop continuation exceeds protocol bounds",
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return LoopContinuationV1{}, err
	}
	var continuation LoopContinuationV1
	if err := decodeStrict(canonical, &continuation); err != nil {
		return LoopContinuationV1{}, err
	}
	if continuation.SchemaVersion != LoopContinuationSchemaVersionV1 {
		return LoopContinuationV1{}, fmt.Errorf(
			"corecontract: unsupported Loop continuation schema version",
		)
	}
	if continuation.CoreFailureReason != "" {
		if continuation.State != TerminatedLoopStep ||
			continuation.AttemptKind != "" ||
			continuation.LogicalStepID != "" ||
			continuation.AttemptID != "" {
			return LoopContinuationV1{}, fmt.Errorf(
				"corecontract: deterministic Core failure continuation cannot name an Attempt",
			)
		}
		rebuilt, err := NewCoreFailureLoopContinuationV1(
			continuation.CoreFailureReason,
		)
		if err != nil {
			return LoopContinuationV1{}, err
		}
		if !bytes.Equal(rebuilt, canonical) {
			return LoopContinuationV1{}, fmt.Errorf(
				"corecontract: Loop continuation is not frozen canonically",
			)
		}
		return continuation, nil
	}
	if continuation.AttemptKind == "" &&
		continuation.State != InitialLoopStep &&
		continuation.State != WaitingChildrenLoopStep &&
		continuation.State != WaitingRepairActivationLoopStep {
		continuation.AttemptKind = AttemptKindModel
	}
	rebuilt, err := NewLoopContinuationForAttemptV1(
		continuation.State,
		continuation.AttemptKind,
		continuation.LogicalStepID,
		continuation.AttemptID,
	)
	if err != nil {
		return LoopContinuationV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return LoopContinuationV1{}, fmt.Errorf(
			"corecontract: Loop continuation is not frozen canonically",
		)
	}
	return continuation, nil
}

func NewRunAdmittedEventV1(
	runID string,
	manifestDigest string,
	memberSnapshotDigest string,
) (RunAdmittedEventV1, []byte, error) {
	if !validOpaque(runID, maxOpaqueIDBytes) {
		return RunAdmittedEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid Run ID for admitted event",
		)
	}
	if !moduleapi.ValidSHA256(manifestDigest) ||
		!moduleapi.ValidSHA256(memberSnapshotDigest) {
		return RunAdmittedEventV1{}, nil, fmt.Errorf(
			"corecontract: admitted event requires valid recovery digests",
		)
	}
	event := RunAdmittedEventV1{
		SchemaVersion:        RunAdmittedPayloadSchemaVersionV1,
		RunID:                runID,
		ManifestDigest:       manifestDigest,
		MemberSnapshotDigest: memberSnapshotDigest,
	}
	canonical, err := canonicalJSON(event)
	if err != nil {
		return RunAdmittedEventV1{}, nil, err
	}
	return event, canonical, nil
}

func RestoreRunAdmittedEventV1(
	canonical []byte,
) (RunAdmittedEventV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return RunAdmittedEventV1{}, err
	}
	var decoded RunAdmittedEventV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return RunAdmittedEventV1{}, err
	}
	if decoded.SchemaVersion != RunAdmittedPayloadSchemaVersionV1 {
		return RunAdmittedEventV1{}, fmt.Errorf(
			"corecontract: unsupported admitted event schema version",
		)
	}
	rebuilt, rebuiltCanonical, err := NewRunAdmittedEventV1(
		decoded.RunID,
		decoded.ManifestDigest,
		decoded.MemberSnapshotDigest,
	)
	if err != nil {
		return RunAdmittedEventV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return RunAdmittedEventV1{}, fmt.Errorf(
			"corecontract: admitted event is not frozen canonically",
		)
	}
	return rebuilt, nil
}
