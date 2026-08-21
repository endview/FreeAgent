package corecontract

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestInitialLoopStateIsFixedAndRestartSafe(t *testing.T) {
	budgetRef, continuation, err := NewInitialLoopState("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if budgetRef != "usage-ledger/v1/run-1/0" {
		t.Fatalf("budget ref=%q", budgetRef)
	}
	if string(continuation) !=
		`{"schema_version":"loop-continuation/v1","state":"READY"}` {
		t.Fatalf("continuation=%s", continuation)
	}
	restored, err := RestoreLoopContinuationV1(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != InitialLoopStep {
		t.Fatalf("state=%q", restored.State)
	}

	drifted := bytes.Replace(
		continuation,
		[]byte(`"READY"`),
		[]byte(`"CALL_MODEL"`),
		1,
	)
	if _, err := RestoreLoopContinuationV1(drifted); err == nil {
		t.Fatal("unsupported continuation state was accepted")
	}
}

func TestBudgetStateRefV1RoundTripAndDedicatedLengthLimit(t *testing.T) {
	runID := strings.Repeat("r", maxOpaqueIDBytes)
	reference, err := NewBudgetStateRefV1(runID, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if len(reference) != MaxBudgetStateRefBytesV1 {
		t.Fatalf(
			"reference length=%d want=%d",
			len(reference),
			MaxBudgetStateRefBytesV1,
		)
	}
	sequence, err := ParseBudgetStateRefV1(reference, runID)
	if err != nil {
		t.Fatal(err)
	}
	if sequence != math.MaxInt64 {
		t.Fatalf("sequence=%d", sequence)
	}

	initial, _, err := NewInitialLoopState(runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBudgetStateRefV1(initial, runID); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetStateRefV1RejectsWrongRunFormatAndSequence(t *testing.T) {
	reference, err := NewBudgetStateRefV1("run/with/slash", 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBudgetStateRefV1(
		reference,
		"other-run",
	); err == nil {
		t.Fatal("BudgetStateRef for a different Run was accepted")
	}
	if sequence, err := ParseBudgetStateRefV1(
		reference,
		"run/with/slash",
	); err != nil || sequence != 7 {
		t.Fatalf("sequence=%d error=%v", sequence, err)
	}

	tests := []string{
		"",
		"usage-ledger/v2/run-1/0",
		"usage-ledger/v1//0",
		"usage-ledger/v1/run-1/",
		"usage-ledger/v1/run-1/00",
		"usage-ledger/v1/run-1/01",
		"usage-ledger/v1/run-1/+1",
		"usage-ledger/v1/run-1/-1",
		"usage-ledger/v1/run-1/1.0",
		"usage-ledger/v1/run-1/1e2",
		"usage-ledger/v1/run-1/1/extra",
		"usage-ledger/v1/run-1/" +
			strconv.FormatUint(uint64(math.MaxInt64)+1, 10),
	}
	for _, test := range tests {
		t.Run(test, func(t *testing.T) {
			if _, err := ParseBudgetStateRefV1(
				test,
				"run-1",
			); err == nil {
				t.Fatalf("accepted %q", test)
			}
		})
	}
	if _, err := NewBudgetStateRefV1(
		"run-1",
		uint64(math.MaxInt64)+1,
	); err == nil {
		t.Fatal("overflowing BudgetStateRef sequence was accepted")
	}
}

func TestRunAdmittedEventV1HasNoCallerSelectedState(t *testing.T) {
	_, canonical, err := NewRunAdmittedEventV1(
		"run-1",
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"manifest_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","member_snapshot_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","run_id":"run-1","schema_version":"run-admitted-event/v1"}`
	if string(canonical) != want {
		t.Fatalf("event payload=%s", canonical)
	}
	restored, err := RestoreRunAdmittedEventV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.RunID != "run-1" {
		t.Fatalf("restored run ID=%q", restored.RunID)
	}
}

func TestLoopContinuationV1RestoresEveryS1State(t *testing.T) {
	tests := []struct {
		state         string
		logicalStepID string
		attemptID     string
	}{
		{state: InitialLoopStep},
		{state: WaitingRepairActivationLoopStep},
		{
			state:         ModelPendingLoopStep,
			logicalStepID: "reply-1",
			attemptID:     "attempt-1",
		},
		{
			state:         WaitingReconciliationLoopStep,
			logicalStepID: "reply-1",
			attemptID:     "attempt-1",
		},
		{
			state:         TerminatedLoopStep,
			logicalStepID: "reply-1",
			attemptID:     "attempt-1",
		},
	}
	for _, test := range tests {
		t.Run(test.state, func(t *testing.T) {
			canonical, err := NewLoopContinuationV1(
				test.state,
				test.logicalStepID,
				test.attemptID,
			)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := RestoreLoopContinuationV1(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if restored.State != test.state ||
				restored.LogicalStepID != test.logicalStepID ||
				restored.AttemptID != test.attemptID {
				t.Fatalf("restored=%+v", restored)
			}
		})
	}
}

func TestWaitingRepairActivationLoopStateHasNoAttemptIdentity(t *testing.T) {
	budgetRef, canonical, err := NewWaitingRepairActivationLoopState(
		"run-repair-child",
	)
	if err != nil {
		t.Fatal(err)
	}
	if budgetRef != "usage-ledger/v1/run-repair-child/0" {
		t.Fatalf("repair budget ref=%q", budgetRef)
	}
	const want = `{"schema_version":"loop-continuation/v1","state":"WAITING_REPAIR_ACTIVATION"}`
	if string(canonical) != want {
		t.Fatalf("repair activation continuation=%s want=%s", canonical, want)
	}
	restored, err := RestoreLoopContinuationV1(canonical)
	if err != nil || restored.State != WaitingRepairActivationLoopStep ||
		restored.AttemptKind != "" || restored.LogicalStepID != "" ||
		restored.AttemptID != "" {
		t.Fatalf("restored repair activation=%+v err=%v", restored, err)
	}
	if _, err := NewLoopContinuationV1(
		WaitingRepairActivationLoopStep,
		"step-forbidden",
		"attempt-forbidden",
	); err == nil {
		t.Fatal("dormant repair continuation accepted an Attempt identity")
	}
}

func TestLoopContinuationV1KeepsLegacyModelWireAndClosesActionStates(
	t *testing.T,
) {
	legacy, err := NewLoopContinuationV1(
		ModelPendingLoopStep,
		"reply-1",
		"attempt-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	const legacyWant = `{"attempt_id":"attempt-1","logical_step_id":"reply-1","schema_version":"loop-continuation/v1","state":"MODEL_PENDING"}`
	if string(legacy) != legacyWant {
		t.Fatalf("legacy MODEL wire changed: %s", legacy)
	}
	restoredLegacy, err := RestoreLoopContinuationV1(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if restoredLegacy.AttemptKind != AttemptKindModel {
		t.Fatalf("legacy AttemptKind=%q", restoredLegacy.AttemptKind)
	}

	for _, state := range []string{
		ActionPendingLoopStep,
		ModelReadyAfterActionLoopStep,
		WaitingReconciliationLoopStep,
		TerminatedLoopStep,
	} {
		t.Run(state, func(t *testing.T) {
			canonical, err := NewLoopContinuationForAttemptV1(
				state,
				AttemptKindAction,
				"action-1",
				"attempt-2",
			)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(canonical, []byte(`"attempt_kind":"ACTION"`)) {
				t.Fatalf("Action AttemptKind omitted: %s", canonical)
			}
			restored, err := RestoreLoopContinuationV1(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if restored.AttemptKind != AttemptKindAction ||
				restored.State != state {
				t.Fatalf("restored=%+v", restored)
			}
		})
	}

	channel, err := NewLoopContinuationForAttemptV1(
		ChannelPendingLoopStep,
		AttemptKindChannel,
		ChannelSendLogicalStepIDV1,
		"channel-attempt-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(channel, []byte(`"attempt_kind":"CHANNEL"`)) {
		t.Fatalf("Channel AttemptKind omitted: %s", channel)
	}
	restoredChannel, err := RestoreLoopContinuationV1(channel)
	if err != nil || restoredChannel.AttemptKind != AttemptKindChannel ||
		restoredChannel.State != ChannelPendingLoopStep {
		t.Fatalf("restored Channel=%+v err=%v", restoredChannel, err)
	}
}

func TestLoopContinuationV1RejectsAttemptKindStateDrift(t *testing.T) {
	tests := []struct {
		state string
		kind  AttemptKindV1
	}{
		{state: ModelPendingLoopStep, kind: AttemptKindAction},
		{state: ActionPendingLoopStep, kind: AttemptKindModel},
		{state: ModelReadyAfterActionLoopStep, kind: AttemptKindModel},
		{state: ChannelPendingLoopStep, kind: AttemptKindAction},
		{state: TerminatedLoopStep, kind: "UNKNOWN"},
	}
	for _, test := range tests {
		if _, err := NewLoopContinuationForAttemptV1(
			test.state,
			test.kind,
			"step-1",
			"attempt-1",
		); err == nil {
			t.Fatalf("accepted state=%s kind=%s", test.state, test.kind)
		}
	}

	explicitLegacyKind := json.RawMessage(
		`{"attempt_id":"attempt-1","attempt_kind":"MODEL","logical_step_id":"reply-1","schema_version":"loop-continuation/v1","state":"MODEL_PENDING"}`,
	)
	if _, err := RestoreLoopContinuationV1(explicitLegacyKind); err == nil {
		t.Fatal("accepted non-canonical explicit legacy MODEL AttemptKind")
	}
}

func TestLoopContinuationV1RejectsImpossibleShapes(t *testing.T) {
	tests := []struct {
		state         string
		logicalStepID string
		attemptID     string
	}{
		{state: "UNKNOWN"},
		{state: InitialLoopStep, attemptID: "attempt-1"},
		{state: WaitingRepairActivationLoopStep, attemptID: "attempt-1"},
		{state: ModelPendingLoopStep, logicalStepID: "reply-1"},
		{state: WaitingReconciliationLoopStep, attemptID: "attempt-1"},
		{state: TerminatedLoopStep},
	}
	for _, test := range tests {
		if _, err := NewLoopContinuationV1(
			test.state,
			test.logicalStepID,
			test.attemptID,
		); err == nil {
			t.Fatalf("accepted %+v", test)
		}
	}

	nonCanonical := json.RawMessage(
		`{"state":"MODEL_PENDING","schema_version":"loop-continuation/v1","logical_step_id":"reply-1","attempt_id":"attempt-1"}`,
	)
	if _, err := RestoreLoopContinuationV1(nonCanonical); err == nil {
		t.Fatal("accepted non-canonical continuation")
	}
}
