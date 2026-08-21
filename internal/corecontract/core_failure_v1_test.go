package corecontract

import (
	"bytes"
	"testing"
)

func TestCoreDeterministicFailureContinuationAndEventRoundTrip(t *testing.T) {
	for _, reason := range []string{
		AllRequiredChildFailedReasonV1,
		CompositeChildResultOverBudgetReasonV1,
		CompositeReviewRejectedReasonV1,
		CompositeReviewFailedReasonV1,
		CompositeReviewOutputInvalidReasonV1,
		CompositeRepairSkippedReasonV1,
		CollaborationReviewRejectedReasonV1,
		CollaborationReviewFailedReasonV1,
		CollaborationReviewOutputInvalidReasonV1,
		CollaborationRepairLimitReachedReasonV1,
	} {
		t.Run(reason, func(t *testing.T) {
			continuation, err := NewCoreFailureLoopContinuationV1(reason)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := RestoreLoopContinuationV1(continuation)
			if err != nil {
				t.Fatal(err)
			}
			if restored.State != TerminatedLoopStep ||
				restored.CoreFailureReason != reason ||
				restored.AttemptKind != "" || restored.AttemptID != "" ||
				restored.LogicalStepID != "" {
				t.Fatalf("continuation=%+v", restored)
			}

			event, canonical, err := NewCoreDeterministicFailureEventV1(
				"run-core-failure",
				reason,
			)
			if err != nil {
				t.Fatal(err)
			}
			restoredEvent, err := RestoreCoreDeterministicFailureEventV1(
				canonical,
			)
			if err != nil {
				t.Fatal(err)
			}
			if restoredEvent != event {
				t.Fatalf("restored event=%+v want=%+v", restoredEvent, event)
			}
		})
	}
}

func TestCoreDeterministicFailureRejectsAttemptAndUnknownReason(t *testing.T) {
	if _, err := NewCoreFailureLoopContinuationV1("UNKNOWN_REASON"); err == nil {
		t.Fatal("accepted unknown deterministic Core failure reason")
	}
	valid, err := NewCoreFailureLoopContinuationV1(
		AllRequiredChildFailedReasonV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(
		valid,
		[]byte(`"core_failure_reason"`),
		[]byte(`"attempt_id":"fake","core_failure_reason"`),
		1,
	)
	if _, err := RestoreLoopContinuationV1(drifted); err == nil {
		t.Fatal("accepted deterministic Core failure with fake Attempt identity")
	}
}
