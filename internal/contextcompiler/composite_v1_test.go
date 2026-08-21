package contextcompiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1CompositeChildInjectsProtectedAssignmentOnly(t *testing.T) {
	input := newCompileInput(t, "review the frozen focus")
	assignment := corecontract.CompositeAssignmentV1{
		SlotID: "risk-slot", FocusID: "risk-review", WeightBasisPoints: 6500,
	}
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleChildV1,
		RootRunID:            "root-run",
		ParentManifestDigest: testDigest("1"),
		ParentSlotID:         assignment.SlotID,
		Assignment:           &assignment,
	}

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil ||
		result.Compilation.StopReason !=
			corecontract.ContextCompilationCompositeBelowWatermark ||
		result.Compilation.Composite == nil ||
		result.Compilation.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		result.Compilation.Composite.Assignment == nil ||
		*result.Compilation.Composite.Assignment != assignment {
		t.Fatalf("Composite CHILD compilation = %+v", result.Compilation)
	}
	if len(result.Request.Messages) != 2 ||
		result.Request.Messages[0].Role != moduleapi.ModelRoleSystem ||
		result.Request.Messages[1].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Composite CHILD messages = %+v", result.Request.Messages)
	}
	content := result.Request.Messages[0].Content
	if !strings.HasPrefix(content, compositeAssignmentPrefixV1) {
		t.Fatalf("assignment instruction = %q", content)
	}
	payload := []byte(strings.TrimPrefix(content, compositeAssignmentPrefixV1))
	canonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil || !bytes.Equal(payload, canonical) {
		t.Fatalf("assignment is not canonical JSON: %s error=%v", payload, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 ||
		fields["schema_version"] != compositeAssignmentSchemaVersionV1 ||
		fields["slot_id"] != assignment.SlotID ||
		fields["focus_id"] != assignment.FocusID ||
		fields["weight_basis_points"] != nil {
		t.Fatalf("assignment fields = %#v", fields)
	}
	for _, forbidden := range []string{
		"permission", "authority", "agent", "profile", "action",
	} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Fatalf("assignment instruction contains forbidden %q: %s", forbidden, content)
		}
	}
	if _, err := corecontract.RestoreContextCompilationV1(
		result.CompilationCanonical,
	); err != nil {
		t.Fatalf("restore Composite CHILD compilation: %v", err)
	}
}

func TestCompileV1CompositeRootInjectsOrderedBudgetedUntrustedResults(
	t *testing.T,
) {
	wantResults := []string{
		strings.Repeat("🙂", 100),
		strings.Repeat("界", 100),
	}
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		wantResults,
	)
	addHistoryTurn(t, &input, "prior assistant History remains one History unit")

	first, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.RequestCanonical, second.RequestCanonical) ||
		!bytes.Equal(first.CompilationCanonical, second.CompilationCanonical) {
		t.Fatal("Composite ROOT compilation is not deterministic")
	}
	compilation := first.Compilation
	if compilation == nil ||
		compilation.StopReason !=
			corecontract.ContextCompilationCompositeBelowWatermark ||
		compilation.Composite == nil ||
		compilation.Composite.Role != corecontract.CompositeRunRoleRootV1 {
		t.Fatalf("Composite ROOT compilation = %+v", compilation)
	}
	composite := compilation.Composite
	if composite.ChildResultBudgetTokens != 2000 ||
		len(composite.ChildResults) != 2 {
		t.Fatalf("Composite ROOT evidence = %+v", composite)
	}
	wantAllocated := []uint64{1400, 600}
	for index, evidence := range composite.ChildResults {
		message := first.Request.Messages[index+2]
		wantEnvelopeBytes := uint64(len(message.Content))
		wantEstimatedTokens, err := estimateCompositeChildResultMessageV1(message)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.Assignment.SlotID != input.Composite.Plan.Children[index].SlotID ||
			evidence.ChildManifestDigest != input.CompositeChildResults[index].ChildManifestDigest ||
			evidence.MemberSnapshotDigest != input.Composite.Plan.Children[index].MemberSnapshotDigest ||
			evidence.ResultRef != input.CompositeChildResults[index].ResultRef ||
			evidence.TerminalRevision != input.CompositeChildResults[index].TerminalRevision ||
			evidence.AllocatedTokens != wantAllocated[index] ||
			evidence.EstimatedTokens != wantEstimatedTokens ||
			evidence.EstimatedTokens <= evidence.OriginalBytes ||
			evidence.OriginalBytes != wantEnvelopeBytes ||
			evidence.RetainedBytes != wantEnvelopeBytes ||
			evidence.Truncated {
			t.Fatalf("Child evidence %d = %+v", index, evidence)
		}
	}
	if len(first.Request.Messages) != 5 ||
		first.Request.Messages[0].Role != moduleapi.ModelRoleSystem ||
		first.Request.Messages[0].Content != compositeUntrustedSafetyInstructionV1 ||
		first.Request.Messages[1].Role != moduleapi.ModelRoleAssistant ||
		first.Request.Messages[2].Role != moduleapi.ModelRoleUser ||
		first.Request.Messages[3].Role != moduleapi.ModelRoleUser ||
		first.Request.Messages[4].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Composite ROOT message placement = %+v", first.Request.Messages)
	}
	for index := 0; index < 2; index++ {
		message := first.Request.Messages[index+2]
		if !strings.HasPrefix(message.Content, compositeChildResultPrefixV1) {
			t.Fatalf("Child result %d is not untrusted USER data", index)
		}
		payload := []byte(strings.TrimPrefix(
			message.Content,
			compositeChildResultPrefixV1,
		))
		canonical, err := moduleapi.CanonicalJSON(payload)
		if err != nil || !bytes.Equal(payload, canonical) {
			t.Fatalf("Child envelope %d canonical error=%v", index, err)
		}
		var envelope compositeChildResultEnvelopeV1
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.SchemaVersion != compositeChildResultSchemaVersionV1 ||
			envelope.SlotID != input.Composite.Plan.Children[index].SlotID ||
			envelope.FocusID != input.Composite.Plan.Children[index].Assignment.FocusID ||
			envelope.Result != wantResults[index] ||
			!utf8.ValidString(envelope.Result) {
			t.Fatalf("Child envelope %d = %+v", index, envelope)
		}
		var fields map[string]any
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 || fields["weight_basis_points"] != nil ||
			fields["run_id"] != nil || fields["result_ref"] != nil ||
			fields["child_manifest_digest"] != nil ||
			fields["terminal_revision"] != nil ||
			fields["member_snapshot_digest"] != nil {
			t.Fatalf("Child envelope %d fields = %#v", index, fields)
		}
	}
	assistantMessages := 0
	for _, message := range first.Request.Messages {
		if message.Role == moduleapi.ModelRoleAssistant {
			assistantMessages++
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("Child results were copied into History: %+v", first.Request.Messages)
	}
	if _, err := corecontract.RestoreContextCompilationV1(
		first.CompilationCanonical,
	); err != nil {
		t.Fatalf("restore Composite ROOT compilation: %v", err)
	}
}

func TestCompileV1CompositeRootResultsAreProtectedWhileHistoryDrops(
	t *testing.T,
) {
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{"risk finding", "delivery finding"},
	)
	for index := 0; index < 6; index++ {
		addHistoryTurn(t, &input, strings.Repeat("old-history-", 100))
	}
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil ||
		result.Compilation.StopReason !=
			corecontract.ContextCompilationDropToWatermark ||
		len(result.Compilation.Drops) == 0 ||
		result.Compilation.Composite == nil {
		t.Fatalf("Composite full-budget compilation = %+v", result.Compilation)
	}
	for _, drop := range result.Compilation.Drops {
		if drop.UnitKind != corecontract.ContextCompilationUnitHistoryTurn {
			t.Fatalf("non-History Composite drop = %+v", drop)
		}
	}
	if len(result.Request.Messages) < 4 ||
		result.Request.Messages[0].Content != compositeUntrustedSafetyInstructionV1 {
		t.Fatalf("protected Composite messages = %+v", result.Request.Messages)
	}
	for index, slot := range []string{"a-risk", "b-delivery"} {
		message := result.Request.Messages[len(result.Request.Messages)-3+index]
		if message.Role != moduleapi.ModelRoleUser ||
			!strings.HasPrefix(message.Content, compositeChildResultPrefixV1) ||
			!strings.Contains(message.Content, `"slot_id":"`+slot+`"`) {
			t.Fatalf("protected Child result %d = %+v", index, message)
		}
	}
}

func TestCompileV1CompositeRootOverBudgetFailsWithoutCompilation(
	t *testing.T,
) {
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{
			strings.Repeat("🙂", 500),
			"delivery finding",
		},
	)
	result, err := CompileV1(input)
	if !errors.Is(err, ErrContextBudgetExceeded) ||
		!strings.Contains(err.Error(), CompositeChildResultOverBudgetCodeV1) {
		t.Fatalf("over-budget error = %v", err)
	}
	if len(result.Request.Messages) != 0 ||
		len(result.RequestCanonical) != 0 ||
		result.Compilation != nil ||
		len(result.CompilationCanonical) != 0 {
		t.Fatalf("over-budget compilation leaked output: %+v", result)
	}
}

func TestCompileV1CompositeRootHardCapUsesCompleteMessageEstimate(
	t *testing.T,
) {
	assignment := corecontract.CompositeAssignmentV1{
		SlotID: "a-risk", FocusID: "risk", WeightBasisPoints: 7000,
	}
	var boundaryText string
	for count := 1; count <= 2000; count++ {
		candidate := strings.Repeat("\n", count)
		message, err := compositeChildResultMessageV1(assignment, candidate)
		if err != nil {
			break
		}
		estimatedTokens, err := estimateCompositeChildResultMessageV1(message)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(len(message.Content)) <= 1400 && estimatedTokens > 1400 {
			boundaryText = candidate
			break
		}
	}
	if boundaryText == "" {
		t.Fatal("failed to construct an envelope-estimator boundary fixture")
	}
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{boundaryText, "delivery finding"},
	)
	result, err := CompileV1(input)
	if !errors.Is(err, ErrContextBudgetExceeded) ||
		!strings.Contains(err.Error(), CompositeChildResultOverBudgetCodeV1) {
		t.Fatalf("complete-message estimate error = %v", err)
	}
	if result.Compilation != nil || len(result.RequestCanonical) != 0 {
		t.Fatalf("hard-cap failure leaked compilation: %+v", result)
	}
}

func TestCompileV1CompositeRootPreservesTrustedBeforeUntrustedOrdering(
	t *testing.T,
) {
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{"risk finding", "delivery finding"},
	)
	addStaticContext(
		t,
		&input,
		moduleapi.ContextPlacementTrustedInstruction,
		false,
		false,
		"trusted coordinator instruction",
	)
	addHistoryTurn(t, &input, "prior assistant result")
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	messages := result.Request.Messages
	if len(messages) != 6 ||
		messages[0].Role != moduleapi.ModelRoleSystem ||
		messages[0].Content != compositeUntrustedSafetyInstructionV1 ||
		messages[1].Role != moduleapi.ModelRoleSystem ||
		messages[1].Content != "trusted coordinator instruction" ||
		messages[2].Role != moduleapi.ModelRoleAssistant ||
		messages[3].Role != moduleapi.ModelRoleUser ||
		messages[4].Role != moduleapi.ModelRoleUser ||
		messages[5].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Composite authority/message order = %+v", messages)
	}
}

func TestCompileV1CompositeRootPlacesSummaryBeforeChildResults(
	t *testing.T,
) {
	input := newCompositeRootCompileInputV1(
		t,
		100000,
		[]string{"risk finding", "delivery finding"},
	)
	addHistoryTurn(t, &input, strings.Repeat("first-history-", 240))
	addHistoryTurn(t, &input, strings.Repeat("second-history-", 240))
	policy, err := restorePolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := canonicalParameters(input.ModelParameters)
	if err != nil {
		t.Fatal(err)
	}
	units, _, _, _, _, err := restoreUnits(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	units, _, err = injectCompositeContextV1(input, 100000, units)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := estimateUnits(units, parameters)
	if err != nil {
		t.Fatal(err)
	}
	setContextPolicy(t, &input, budgetForExactWatermark(t, estimate), 0)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil || result.Compilation.Summary == nil {
		t.Fatalf("Composite summary compilation = %+v", result.Compilation)
	}
	messages := result.Request.Messages
	childStart := len(messages) - 3
	if childStart < 2 ||
		!strings.HasPrefix(messages[childStart].Content, compositeChildResultPrefixV1) ||
		!strings.HasPrefix(messages[childStart+1].Content, compositeChildResultPrefixV1) ||
		messages[len(messages)-1].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Composite summary/result/task order = %+v", messages)
	}
	foundSummary := false
	for _, message := range messages[:childStart] {
		if message.Role == moduleapi.ModelRoleAssistant &&
			message.Content == result.Compilation.Summary.Text {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("summary did not remain before Child results: %+v", messages)
	}
}

func TestCompileV1CompositeRootPreservesTrustedContextBeforeChildData(
	t *testing.T,
) {
	input := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{"risk finding", "delivery finding"},
	)
	addStaticContext(
		t,
		&input,
		moduleapi.ContextPlacementTrustedInstruction,
		false,
		false,
		"Frozen trusted instruction.",
	)
	addHistoryTurn(t, &input, "prior History")
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Request.Messages) != 6 ||
		result.Request.Messages[0].Content != compositeUntrustedSafetyInstructionV1 ||
		result.Request.Messages[1].Role != moduleapi.ModelRoleSystem ||
		result.Request.Messages[1].Content != "Frozen trusted instruction." ||
		result.Request.Messages[2].Role != moduleapi.ModelRoleAssistant ||
		result.Request.Messages[3].Role != moduleapi.ModelRoleUser ||
		result.Request.Messages[4].Role != moduleapi.ModelRoleUser ||
		result.Request.Messages[5].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Composite ROOT authority ordering = %+v", result.Request.Messages)
	}
	for _, index := range []int{3, 4} {
		if !strings.HasPrefix(
			result.Request.Messages[index].Content,
			compositeChildResultPrefixV1,
		) {
			t.Fatalf("Child result %d is not untrusted data", index-3)
		}
	}
}

func TestCompileV1CompositeInputClosureFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *CompileInputV1)
	}{
		{
			name: "results without node",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite = nil
			},
		},
		{
			name: "fewer than two plan children",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite.Plan.Children = input.Composite.Plan.Children[:1]
				input.Composite.Plan.FamilyModelDispatchLimit = 2
				input.CompositeChildResults = input.CompositeChildResults[:1]
			},
		},
		{
			name: "unsorted plan",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite.Plan.Children[0], input.Composite.Plan.Children[1] =
					input.Composite.Plan.Children[1], input.Composite.Plan.Children[0]
				input.CompositeChildResults[0], input.CompositeChildResults[1] =
					input.CompositeChildResults[1], input.CompositeChildResults[0]
			},
		},
		{
			name: "weight sum",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite.Plan.Children[1].Assignment.WeightBasisPoints = 2999
			},
		},
		{
			name: "Task closure",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite.Plan.Children[0].TaskInputRef = testDigest("8")
			},
		},
		{
			name: "result order",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[0], input.CompositeChildResults[1] =
					input.CompositeChildResults[1], input.CompositeChildResults[0]
			},
		},
		{
			name: "result identity",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[0].MemberSnapshotDigest = testDigest("9")
			},
		},
		{
			name: "missing Child Manifest digest",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[0].ChildManifestDigest = ""
			},
		},
		{
			name: "duplicate Child Manifest digest",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[1].ChildManifestDigest =
					input.CompositeChildResults[0].ChildManifestDigest
			},
		},
		{
			name: "duplicate Member snapshot",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.Composite.Plan.Children[1].MemberSnapshotDigest =
					input.Composite.Plan.Children[0].MemberSnapshotDigest
				input.CompositeChildResults[1].MemberSnapshotDigest =
					input.CompositeChildResults[0].MemberSnapshotDigest
			},
		},
		{
			name: "missing terminal revision",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[0].TerminalRevision = 0
			},
		},
		{
			name: "result digest",
			mutate: func(_ *testing.T, input *CompileInputV1) {
				input.CompositeChildResults[0].ResultRef = testDigest("7")
			},
		},
		{
			name: "Action request is not a merge result",
			mutate: func(t *testing.T, input *CompileInputV1) {
				_, canonical, err := moduleapi.NewModelGenerateOutputV1(
					moduleapi.ModelGenerateOutputV1{
						SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
						ActionRequest: &moduleapi.ModelActionRequestV1{
							ActionID: "tool.test", CanonicalInput: json.RawMessage(`{}`),
						},
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				input.CompositeChildResults[0].ResultCanonical = canonical
				input.CompositeChildResults[0].ResultRef = contentDigest(
					"MODEL_RESULT", jsonMediaType, canonical,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := newCompositeRootCompileInputV1(
				t,
				4000,
				[]string{"first", "second"},
			)
			test.mutate(t, &input)
			if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
				t.Fatalf("closure error = %v", err)
			}
		})
	}
}

func TestCompileV1CompositeChildRejectsOpenClosure(t *testing.T) {
	input := newCompileInput(t, "child task")
	assignment := corecontract.CompositeAssignmentV1{
		SlotID: "slot", FocusID: "focus", WeightBasisPoints: 5000,
	}
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleChildV1,
		RootRunID:            "root",
		ParentManifestDigest: testDigest("1"),
		ParentSlotID:         "different-slot",
		Assignment:           &assignment,
	}
	if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
		t.Fatalf("CHILD closure error = %v", err)
	}
}

func newCompositeRootCompileInputV1(
	t *testing.T,
	inputBudget uint64,
	texts []string,
) CompileInputV1 {
	t.Helper()
	if len(texts) != 2 {
		t.Fatal("test fixture requires two Child result texts")
	}
	input := newCompileInput(t, "merge the Child findings")
	setContextPolicy(t, &input, inputBudget, 0)
	children := []corecontract.CompositeChildRunRefV1{
		{
			SlotID:               "a-risk",
			RunID:                "child-a",
			AdmissionKey:         "admission-a",
			MemberSnapshotDigest: testDigest("2"),
			Agent: corecontract.AgentRef{
				ID: "agent-a", Version: "1", Digest: testDigest("3"),
			},
			Profile: corecontract.ProfileRef{
				ID: "profile-a", Version: "1", Digest: testDigest("4"),
			},
			TaskInputRef: input.TaskInputRef,
			Assignment: corecontract.CompositeAssignmentV1{
				SlotID: "a-risk", FocusID: "risk", WeightBasisPoints: 7000,
			},
		},
		{
			SlotID:               "b-delivery",
			RunID:                "child-b",
			AdmissionKey:         "admission-b",
			MemberSnapshotDigest: testDigest("5"),
			Agent: corecontract.AgentRef{
				ID: "agent-b", Version: "1", Digest: testDigest("6"),
			},
			Profile: corecontract.ProfileRef{
				ID: "profile-b", Version: "1", Digest: testDigest("7"),
			},
			TaskInputRef: input.TaskInputRef,
			Assignment: corecontract.CompositeAssignmentV1{
				SlotID: "b-delivery", FocusID: "delivery", WeightBasisPoints: 3000,
			},
		},
	}
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion: corecontract.CompositeRunNodeSchemaVersionV1,
		Role:          corecontract.CompositeRunRoleRootV1,
		RootRunID:     "root-run",
		Plan: &corecontract.CompositeRunPlanV1{
			MergeLogicalStepID:       corecontract.CompositeMergeLogicalStepIDV1,
			FamilyModelDispatchLimit: 3,
			Children:                 children,
		},
	}
	input.CompositeChildResults = make([]CompositeChildResultV1, len(children))
	for index, child := range children {
		_, canonical, err := moduleapi.NewModelGenerateOutputV1(
			moduleapi.ModelGenerateOutputV1{
				SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
				AssistantText: texts[index],
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		input.CompositeChildResults[index] = CompositeChildResultV1{
			SlotID:               child.SlotID,
			RunID:                child.RunID,
			AdmissionKey:         child.AdmissionKey,
			ChildManifestDigest:  testDigest(string(rune('8' + index))),
			MemberSnapshotDigest: child.MemberSnapshotDigest,
			ResultRef: contentDigest(
				"MODEL_RESULT", jsonMediaType, canonical,
			),
			TerminalRevision: uint64(index + 11),
			ResultCanonical:  canonical,
		}
	}
	return input
}
