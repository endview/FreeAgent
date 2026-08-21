package coreloop

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestFrozenActionBudgetDecisionRequiresExactEmptyCostPolicy(t *testing.T) {
	run := actionBudgetRun(t, json.RawMessage(`{}`))
	if got := frozenActionBudgetDecision(run); got != currentstore.ActionBudgetAllow {
		t.Fatalf("empty cost policy decision = %q", got)
	}

	nonEmpty := actionBudgetRun(
		t,
		json.RawMessage(`{"max_run_cost_microunits":0}`),
	)
	if got := frozenActionBudgetDecision(nonEmpty); got != currentstore.ActionBudgetUnknown {
		t.Fatalf("untyped non-empty policy decision = %q", got)
	}

	zeroCost := actionBudgetRun(
		t,
		json.RawMessage(
			`{"currency":"USD","max_run_cost_microunits":0,"pricing_mode":"ZERO_COST_DEVELOPMENT"}`,
		),
	)
	if got := frozenActionBudgetDecision(zeroCost); got != currentstore.ActionBudgetAllow {
		t.Fatalf("exact zero-cost policy decision = %q", got)
	}
	unknownRule := actionBudgetRun(
		t,
		json.RawMessage(
			`{"currency":"USD","extra":true,"max_run_cost_microunits":0,"pricing_mode":"ZERO_COST_DEVELOPMENT"}`,
		),
	)
	if got := frozenActionBudgetDecision(unknownRule); got != currentstore.ActionBudgetUnknown {
		t.Fatalf("unknown budget rule decision = %q", got)
	}
	missingField := actionBudgetRun(
		t,
		json.RawMessage(
			`{"currency":"USD","pricing_mode":"ZERO_COST_DEVELOPMENT"}`,
		),
	)
	if got := frozenActionBudgetDecision(missingField); got != currentstore.ActionBudgetUnknown {
		t.Fatalf("missing budget field decision = %q", got)
	}

	drifted := actionBudgetRun(t, json.RawMessage(`{}`))
	drifted.Contents[0].CanonicalBytes = bytes.Clone(
		nonEmpty.Contents[0].CanonicalBytes,
	)
	if got := frozenActionBudgetDecision(drifted); got != currentstore.ActionBudgetUnknown {
		t.Fatalf("digest/body drift decision = %q", got)
	}
}

func actionBudgetRun(
	t *testing.T,
	body json.RawMessage,
) currentstore.RunForLoop {
	t.Helper()
	_, ref, canonical, err := corecontract.NewPolicyDocument(
		"budget-action-test",
		"1",
		corecontract.PolicyCost,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentPolicy,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if digest != ref.Digest {
		t.Fatalf("policy digest mismatch: store=%s core=%s", digest, ref.Digest)
	}
	return currentstore.RunForLoop{
		Manifest: corecontract.RunManifest{BudgetPolicy: ref},
		Contents: []currentstore.ContentRecord{{
			Digest:         digest,
			Kind:           currentstore.ContentPolicy,
			MediaType:      "application/json",
			CanonicalBytes: bytes.Clone(canonical),
			SizeBytes:      int64(len(canonical)),
		}},
	}
}
