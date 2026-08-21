package coreloop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPrepareModelAfterActionRequestAppendsOnlyPersistedEnvelope(t *testing.T) {
	run := modelAfterActionFixture(t)
	prepared, err := prepareModelAfterActionRequestV1(run)
	if err != nil {
		t.Fatal(err)
	}
	source := run.ModelDispatches[0].Attempt.Request.CanonicalBytes
	modelOne, err := moduleapi.RestoreModelGenerateRequestV1(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Request.Messages) != len(modelOne.Messages)+1 ||
		prepared.Request.Messages[0] != modelOne.Messages[0] ||
		!strings.HasPrefix(
			prepared.Request.Messages[len(prepared.Request.Messages)-1].Content,
			corecontract.UntrustedActionResultPrefixV1,
		) {
		t.Fatalf("model-two messages = %+v", prepared.Request.Messages)
	}
	if len(prepared.Request.Actions) != 1 ||
		prepared.Request.Actions[0].ActionID != "text.stats" ||
		prepared.ContextCompilationCanonical != nil {
		t.Fatalf("model-two frozen fields = %+v", prepared)
	}
	restored, err := moduleapi.RestoreModelGenerateRequestV1(
		prepared.RequestCanonical,
	)
	if err != nil || len(restored.Messages) != 2 {
		t.Fatalf("restore model-two request = %+v, %v", restored, err)
	}
}

func TestPrepareModelAfterActionRequestFailsClosedOnResultDrift(t *testing.T) {
	run := modelAfterActionFixture(t)
	run.ActionDispatches[0].Result.CanonicalBytes = []byte(`{"status":"AVAILABLE"}`)
	if _, err := prepareModelAfterActionRequestV1(run); err == nil {
		t.Fatal("tampered Action result was accepted")
	}

	run = modelAfterActionFixture(t)
	run.ModelDispatches[0].Attempt.ContextCompilation = nil
	if _, err := prepareModelAfterActionRequestV1(run); err == nil {
		t.Fatal("missing model-one reservation was accepted")
	}
}

func modelAfterActionFixture(t *testing.T) currentstore.RunForLoop {
	t.Helper()
	definition, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text.stats",
			BindingIndex:     0,
			Description:      "Count deterministic text statistics.",
			InputSchema: json.RawMessage(
				`{"additionalProperties":false,"properties":{"text":{"maxLength":4096,"type":"string"}},"required":["text"],"type":"object"}`,
			),
			EffectClass:    moduleapi.EffectNone,
			MaxResultBytes: 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := corecontract.ModelActionDefinitionsV1(
		[]corecontract.FrozenActionDefinitionV1{definition},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelOne, modelOneCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: "count this",
			}},
			Parameters: json.RawMessage(`{"temperature":0}`),
			Actions:    actions,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	baseEstimate, err := contextcompiler.EstimateModelGenerateRequestV1(modelOne)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := corecontract.NewActionResultReservationV1(
		[]corecontract.FrozenActionDefinitionV1{definition},
	)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		"application/json",
		modelOneCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1_000_000)
	watermark, err := corecontract.ContextRestoreWatermarkTokensV1(budget)
	if err != nil {
		t.Fatal(err)
	}
	workspace := corecontract.WorkspaceRef{
		ID: "workspace-test", Version: "1", Digest: coreloopDigest("a"),
	}
	policy := corecontract.PolicyRef{
		ID: "context-policy", Version: "1", Digest: coreloopDigest("b"),
	}
	_, compilationCanonical, err := corecontract.NewContextCompilationV1(
		corecontract.ContextCompilationV1{
			SchemaVersion:           corecontract.ContextCompilationSchemaVersionV1,
			WorkspaceScope:          workspace,
			ContextPolicy:           policy,
			EstimatorVersion:        corecontract.ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
			SummaryAlgorithmVersion: corecontract.ContextSummaryHeadTailExtractiveV1,
			InputBudgetTokens:       budget,
			RestoreWatermarkTokens:  watermark,
			OriginalEstimateTokens:  baseEstimate + reservation.EstimatedTokens,
			ActionResultReservation: &reservation,
			Drops:                   []corecontract.ContextCompilationDropV1{},
			FinalEstimateTokens:     baseEstimate + reservation.EstimatedTokens,
			StopReason:              corecontract.ContextCompilationActionResultReserved,
			FinalRequestDigest:      requestDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compilationDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		"application/json",
		compilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, resultCanonical, resultDigest, err :=
		corecontract.NewAvailableActionResultV1(
			definition,
			json.RawMessage(`{"bytes":10,"lines":1,"runes":10,"words":2}`),
		)
	if err != nil || result.Status != corecontract.ActionResultAvailable {
		t.Fatalf("Action result: %+v, %v", result, err)
	}
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.AttemptKindAction,
		"action-1",
		"action-attempt-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.RunForLoop{
		RunID: "run-test",
		Member: corecontract.MemberExecutionSnapshot{
			MemberID: "member-test", Workspace: workspace,
			Actions: []corecontract.FrozenActionDefinitionV1{definition},
		},
		Frame: currentstore.LoopFrameRecord{
			RunID:                    "run-test",
			Step:                     corecontract.ModelReadyAfterActionLoopStep,
			BudgetStateRef:           "usage-ledger/v1/run-test/1",
			Continuation:             continuation,
			PendingDispatchAttemptID: "action-attempt-1",
		},
		ModelDispatches: []currentstore.ModelDispatchRecord{{
			Attempt: currentstore.ModelDispatchAttemptRecord{
				AttemptID: "model-attempt-1",
				State:     corecontract.ModelAttemptSucceeded,
				Request: currentstore.ContentRecord{
					Digest: requestDigest, Kind: currentstore.ContentModelRequest,
					MediaType: "application/json", CanonicalBytes: modelOneCanonical,
				},
				ContextCompilation: &currentstore.ContentRecord{
					Digest: compilationDigest, Kind: currentstore.ContentContextCompilation,
					MediaType: "application/json", CanonicalBytes: compilationCanonical,
				},
			},
		}},
		ActionDispatches: []currentstore.ActionDispatchRecord{{
			Attempt: currentstore.ActionDispatchAttemptRecord{
				AttemptID:            "action-attempt-1",
				SourceModelAttemptID: "model-attempt-1",
				PublicActionID:       definition.PublicActionID,
				ProviderActionID:     definition.ProviderActionID,
				BindingIndex:         definition.BindingIndex,
				DefinitionDigest:     definition.DefinitionDigest,
				EffectClass:          definition.EffectClass,
				MaxResultBytes:       definition.MaxResultBytes,
				BudgetStateRef:       "usage-ledger/v1/run-test/1",
				State:                currentstore.ActionDispatchSucceeded,
				ResultRef:            resultDigest,
			},
			Result: &currentstore.ContentRecord{
				Digest: resultDigest, Kind: currentstore.ContentActionResult,
				MediaType: "application/json", CanonicalBytes: resultCanonical,
			},
		}},
	}
}

func coreloopDigest(character string) string {
	return strings.Repeat(character, 64)
}
