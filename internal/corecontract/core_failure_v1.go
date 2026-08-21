package corecontract

import (
	"bytes"
	"fmt"
)

const (
	CoreDeterministicFailureSchemaVersionV1 = "core-deterministic-failure-event/v1"

	AllRequiredChildFailedReasonV1           = "ALL_REQUIRED_CHILD_FAILED"
	CompositeChildResultOverBudgetReasonV1   = "COMPOSITE_CHILD_RESULT_OVER_BUDGET"
	CompositeReviewRejectedReasonV1          = "COMPOSITE_REVIEW_REJECTED"
	CompositeReviewFailedReasonV1            = "COMPOSITE_REVIEW_FAILED"
	CompositeReviewOutputInvalidReasonV1     = "COMPOSITE_REVIEW_OUTPUT_INVALID"
	CompositeRepairSkippedReasonV1           = "COMPOSITE_REPAIR_SKIPPED"
	CollaborationReviewRejectedReasonV1      = "COLLABORATION_REVIEW_REJECTED"
	CollaborationReviewFailedReasonV1        = "COLLABORATION_REVIEW_FAILED"
	CollaborationReviewOutputInvalidReasonV1 = "COLLABORATION_REVIEWER_OUTPUT_INVALID"
	CollaborationRepairLimitReachedReasonV1  = "COLLABORATION_REPAIR_LIMIT_REACHED"
)

// CoreDeterministicFailureEventV1 is the attempt-free terminal fact emitted
// when frozen Core policy can decide that a Run cannot proceed before any
// provider dispatch is permitted.
type CoreDeterministicFailureEventV1 struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	Reason        string `json:"reason"`
}

func ValidateCoreDeterministicFailureReasonV1(reason string) error {
	switch reason {
	case AllRequiredChildFailedReasonV1,
		CompositeChildResultOverBudgetReasonV1,
		CompositeReviewRejectedReasonV1,
		CompositeReviewFailedReasonV1,
		CompositeReviewOutputInvalidReasonV1,
		CompositeRepairSkippedReasonV1,
		CollaborationReviewRejectedReasonV1,
		CollaborationReviewFailedReasonV1,
		CollaborationReviewOutputInvalidReasonV1,
		CollaborationRepairLimitReachedReasonV1:
		return nil
	default:
		return fmt.Errorf(
			"corecontract: unsupported deterministic Core failure reason %q",
			reason,
		)
	}
}

func NewCoreDeterministicFailureEventV1(
	runID string,
	reason string,
) (CoreDeterministicFailureEventV1, []byte, error) {
	if !validOpaque(runID, maxOpaqueIDBytes) {
		return CoreDeterministicFailureEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid Run ID for deterministic Core failure event",
		)
	}
	if err := ValidateCoreDeterministicFailureReasonV1(reason); err != nil {
		return CoreDeterministicFailureEventV1{}, nil, err
	}
	event := CoreDeterministicFailureEventV1{
		SchemaVersion: CoreDeterministicFailureSchemaVersionV1,
		RunID:         runID,
		Reason:        reason,
	}
	canonical, err := canonicalJSON(event)
	if err != nil {
		return CoreDeterministicFailureEventV1{}, nil, err
	}
	return event, canonical, nil
}

func RestoreCoreDeterministicFailureEventV1(
	canonical []byte,
) (CoreDeterministicFailureEventV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return CoreDeterministicFailureEventV1{}, err
	}
	var decoded CoreDeterministicFailureEventV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CoreDeterministicFailureEventV1{}, err
	}
	if decoded.SchemaVersion != CoreDeterministicFailureSchemaVersionV1 {
		return CoreDeterministicFailureEventV1{}, fmt.Errorf(
			"corecontract: unsupported deterministic Core failure event schema version",
		)
	}
	rebuilt, rebuiltCanonical, err := NewCoreDeterministicFailureEventV1(
		decoded.RunID,
		decoded.Reason,
	)
	if err != nil {
		return CoreDeterministicFailureEventV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return CoreDeterministicFailureEventV1{}, fmt.Errorf(
			"corecontract: deterministic Core failure event is not frozen canonically",
		)
	}
	return rebuilt, nil
}
