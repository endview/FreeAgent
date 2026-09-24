package corecontract

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestModelLogicalOperationKeyIsStableAndStepScoped(t *testing.T) {
	first, err := ModelLogicalOperationKey("run-1", "member-1", "step-1")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := ModelLogicalOperationKey("run-1", "member-1", "step-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated || len(first) != 64 {
		t.Fatalf("logical operation key is not stable: %q %q", first, repeated)
	}
	const want = "06454b5b94e43f892e4780a710c394e9380e8c2ce2e6fa782093a2ab21eb63f4"
	if first != want {
		t.Fatalf("logical operation key = %q, want frozen %q", first, want)
	}
	nextStep, err := ModelLogicalOperationKey("run-1", "member-1", "step-2")
	if err != nil {
		t.Fatal(err)
	}
	if nextStep == first {
		t.Fatal("different logical steps share an operation key")
	}
	if _, err := ModelLogicalOperationKey("run-1", "member-1", ""); err == nil {
		t.Fatal("empty logical step was accepted")
	}
}

func TestModelAttemptTransitionGraphForbidsUnknownReplay(t *testing.T) {
	if !ModelAttemptPending.AllowsTransition(ModelAttemptUnknown) {
		t.Fatal("PENDING cannot enter MODEL_UNKNOWN")
	}
	if ModelAttemptUnknown.AllowsTransition(ModelAttemptPending) {
		t.Fatal("MODEL_UNKNOWN can return to PENDING")
	}
	if !ModelAttemptUnknown.AllowsTransition(ModelAttemptSucceeded) ||
		!ModelAttemptUnknown.AllowsTransition(ModelAttemptFailed) {
		t.Fatal("MODEL_UNKNOWN cannot be reconciled to a terminal state")
	}
	for _, terminal := range []ModelAttemptState{
		ModelAttemptSucceeded,
		ModelAttemptFailed,
	} {
		if terminal.AllowsTransition(ModelAttemptPending) ||
			terminal.AllowsTransition(ModelAttemptUnknown) {
			t.Fatalf("terminal state %s can transition", terminal)
		}
	}
	if ModelAttemptPending.AllowsTransition(ModelAttemptPending) {
		t.Fatal("same-state rewrite was treated as a transition")
	}
}

func TestUsageTokensPreserveUnknownAndKnownZero(t *testing.T) {
	unknown := UsageTokens{}
	if err := unknown.Validate(); err != nil {
		t.Fatal(err)
	}
	if unknown.Clone().Input != nil {
		t.Fatal("UNKNOWN input tokens became known")
	}

	zero := uint64(0)
	knownZero := UsageTokens{
		Input:         &zero,
		CachedInput:   &zero,
		UncachedInput: &zero,
		Output:        &zero,
		Reasoning:     &zero,
	}
	if err := knownZero.Validate(); err != nil {
		t.Fatal(err)
	}
	cloned := knownZero.Clone()
	if cloned.Input == nil || *cloned.Input != 0 {
		t.Fatal("known zero became UNKNOWN")
	}
	*knownZero.Input = 10
	if *cloned.Input != 0 {
		t.Fatal("Usage clone aliases provider input")
	}
}

func TestModelDispatchEventV1RoundTripAndStateShape(t *testing.T) {
	base := ModelDispatchEventV1{
		SchemaVersion:          ModelDispatchEventSchemaVersionV1,
		RunID:                  "run-1",
		AttemptID:              "attempt-1",
		LogicalStepID:          "reply-1",
		LogicalOperationKey:    strings.Repeat("a", 64),
		RequestDigest:          strings.Repeat("b", 64),
		State:                  ModelAttemptPending,
		TransitionOrigin:       DispatchTransitionBeginV1,
		ResourceSemanticDigest: strings.Repeat("d", 64),
	}
	_, canonical, err := NewModelDispatchEventV1(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreModelDispatchEventV1(canonical); err != nil {
		t.Fatal(err)
	}

	success := base
	success.State = ModelAttemptSucceeded
	success.ResultDigest = strings.Repeat("c", 64)
	success.TransitionOrigin = DispatchTransitionOrdinaryOutcomeV1
	success.Usage = &ModelUsageEventV1{
		Revision: 1, UsageStatus: "NO_USAGE_REPORTED",
		SemanticDigest: strings.Repeat("d", 64),
	}
	if _, _, err := NewModelDispatchEventV1(success); err != nil {
		t.Fatal(err)
	}
	missing := success
	missing.ResultDigest = ""
	if _, _, err := NewModelDispatchEventV1(missing); err == nil {
		t.Fatal("success without result accepted")
	}
	unknown := base
	unknown.State = ModelAttemptUnknown
	unknown.ResultDigest = strings.Repeat("c", 64)
	unknown.TransitionOrigin = DispatchTransitionOrdinaryOutcomeV1
	unknown.Usage = &ModelUsageEventV1{
		Revision: 1, UsageStatus: "PENDING_RECONCILIATION",
		SemanticDigest: strings.Repeat("d", 64),
	}
	if _, _, err := NewModelDispatchEventV1(unknown); err == nil {
		t.Fatal("unknown with result accepted")
	}

	tampered := bytes.Replace(
		canonical,
		[]byte(`"PENDING"`),
		[]byte(`"SUCCEEDED"`),
		1,
	)
	if json.Valid(tampered) {
		if _, err := RestoreModelDispatchEventV1(tampered); err == nil {
			t.Fatal("tampered event accepted")
		}
	}
}

func TestUsageTokensOnlyCheckInputEquationWhenAllTermsKnown(t *testing.T) {
	input, cached, uncached := uint64(10), uint64(3), uint64(6)
	partial := UsageTokens{Input: &input, CachedInput: &cached}
	if err := partial.Validate(); err != nil {
		t.Fatal("partial known usage was rejected")
	}
	complete := UsageTokens{
		Input:         &input,
		CachedInput:   &cached,
		UncachedInput: &uncached,
	}
	if err := complete.Validate(); err == nil {
		t.Fatal("invalid complete input equation was accepted")
	}
	tooLarge := uint64(math.MaxInt64) + 1
	if err := (UsageTokens{Output: &tooLarge}).Validate(); err == nil {
		t.Fatal("token value outside SQLite INTEGER range was accepted")
	}
}
