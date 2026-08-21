package contextcompiler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1CompositeReviewerInjectsOrderedCompleteSpecialistResults(
	t *testing.T,
) {
	input, _, specialistDigest := newReviewerEnabledRootCompileInputV1(t)
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleReviewerV1,
		RootRunID:            "root-run",
		ParentManifestDigest: input.CompositeSpecialistResultSet.FamilyDigest,
		ParentSlotID:         corecontract.CompositeReviewerParentSlotIDV1,
	}
	input.CompositeReviewVerdict = nil

	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Compilation == nil || compiled.Compilation.Composite == nil ||
		compiled.Compilation.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		compiled.Compilation.Composite.SpecialistResultDigest != specialistDigest ||
		compiled.Compilation.Composite.ReviewVerdict != nil ||
		len(compiled.Compilation.Composite.ChildResults) != 2 {
		t.Fatalf("Reviewer compilation=%+v", compiled.Compilation)
	}
	messages := compiled.Request.Messages
	if len(messages) != 5 ||
		messages[0].Content != compositeReviewerSafetyInstructionV1 ||
		!strings.HasPrefix(messages[1].Content, compositeReviewerPolicyPrefixV1) ||
		!strings.HasPrefix(messages[2].Content, compositeReviewerResultPrefixV1) ||
		!strings.HasPrefix(messages[3].Content, compositeReviewerResultPrefixV1) ||
		messages[4].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Reviewer messages=%+v", messages)
	}
	var policy compositeReviewerPolicyEnvelopeV1
	if err := json.Unmarshal(
		[]byte(strings.TrimPrefix(messages[1].Content, compositeReviewerPolicyPrefixV1)),
		&policy,
	); err != nil ||
		policy.OutputSchemaVersion != corecontract.ReviewVerdictSchemaVersionV1 ||
		len(policy.RequiredFields) != 7 || len(policy.AllowedDecisions) != 2 ||
		len(policy.AllowedIssueCodes) != 6 || len(policy.OutputRules) < 6 ||
		policy.FamilyDigest != input.CompositeSpecialistResultSet.FamilyDigest ||
		policy.SpecialistResultDigest != specialistDigest {
		t.Fatalf("Reviewer output contract=%+v error=%v", policy, err)
	}
	for index, message := range messages[2:4] {
		payload := []byte(strings.TrimPrefix(
			message.Content,
			compositeReviewerResultPrefixV1,
		))
		canonical, err := moduleapi.CanonicalJSON(payload)
		if err != nil || !bytes.Equal(payload, canonical) {
			t.Fatalf("Reviewer result %d is not canonical: %v", index, err)
		}
		var envelope compositeReviewerResultEnvelopeV1
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		want := input.CompositeSpecialistResultSet.Results[index]
		if envelope.SlotID != want.SlotID ||
			envelope.FocusID != want.FocusID ||
			envelope.WeightBasisPoints != want.WeightBasisPoints ||
			envelope.RunID != want.RunID ||
			envelope.ManifestDigest != want.ManifestDigest ||
			envelope.MemberSnapshotDigest != want.MemberSnapshotDigest ||
			envelope.ResultRef != want.ResultRef ||
			envelope.TerminalRunRevision != want.TerminalRunRevision ||
			envelope.TerminalFrameRevision != want.TerminalFrameRevision ||
			envelope.Result == "" {
			t.Fatalf("Reviewer result envelope %d=%+v want=%+v", index, envelope, want)
		}
	}
}

func TestCompileV1CompositeRootInjectsExactApproveVerdictOnlyWhenEnabled(
	t *testing.T,
) {
	input, verdictCanonical, specialistDigest :=
		newReviewerEnabledRootCompileInputV1(t)
	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	composite := compiled.Compilation.Composite
	if composite == nil ||
		composite.SpecialistResultDigest != specialistDigest ||
		composite.ReviewVerdict == nil ||
		composite.ReviewVerdict.Decision != corecontract.ReviewDecisionApproveV1 ||
		composite.ReviewVerdict.ResultRef != input.CompositeReviewVerdict.ResultRef {
		t.Fatalf("Reviewer-enabled Root evidence=%+v", composite)
	}
	messages := compiled.Request.Messages
	if len(messages) != 5 ||
		messages[0].Content != compositeReviewerSafetyInstructionV1 ||
		!strings.HasPrefix(messages[1].Content, compositeChildResultPrefixV1) ||
		!strings.HasPrefix(messages[2].Content, compositeChildResultPrefixV1) ||
		messages[3].Content != compositeReviewVerdictPrefixV1+string(verdictCanonical) ||
		messages[4].Role != moduleapi.ModelRoleUser {
		t.Fatalf("Reviewer-enabled Root messages=%+v", messages)
	}

	disabled := newCompositeRootCompileInputV1(
		t,
		4000,
		[]string{"risk finding", "delivery finding"},
	)
	first, err := CompileV1(disabled)
	if err != nil {
		t.Fatal(err)
	}
	disabled.CompositeSpecialistResultSet = nil
	disabled.CompositeSpecialistResultDigest = ""
	disabled.CompositeReviewVerdict = nil
	second, err := CompileV1(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.RequestCanonical, second.RequestCanonical) ||
		!bytes.Equal(first.CompilationCanonical, second.CompilationCanonical) ||
		first.Request.Messages[0].Content != compositeUntrustedSafetyInstructionV1 {
		t.Fatal("Reviewer-disabled request bytes changed")
	}
}

func TestCompileV1CompositeReviewMaterialsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompileInputV1)
	}{
		{
			name: "result set digest",
			mutate: func(input *CompileInputV1) {
				input.CompositeSpecialistResultDigest = testDigest("0")
			},
		},
		{
			name: "result set order",
			mutate: func(input *CompileInputV1) {
				input.CompositeSpecialistResultSet.Results[0],
					input.CompositeSpecialistResultSet.Results[1] =
					input.CompositeSpecialistResultSet.Results[1],
					input.CompositeSpecialistResultSet.Results[0]
			},
		},
		{
			name: "verdict result digest",
			mutate: func(input *CompileInputV1) {
				input.CompositeReviewVerdict.ResultRef = testDigest("1")
			},
		},
		{
			name: "verdict text",
			mutate: func(input *CompileInputV1) {
				input.CompositeReviewVerdict.VerdictCanonical = []byte(`{"decision":"APPROVE"}`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, _, _ := newReviewerEnabledRootCompileInputV1(t)
			test.mutate(&input)
			if _, err := CompileV1(input); err == nil {
				t.Fatal("invalid Reviewer material was accepted")
			}
		})
	}
}

func newReviewerEnabledRootCompileInputV1(
	t *testing.T,
) (CompileInputV1, []byte, string) {
	t.Helper()
	input := newCompositeRootCompileInputV1(
		t,
		12000,
		[]string{"risk finding", "delivery finding"},
	)
	rootPlan := input.Composite.Plan
	reviewer := corecontract.CompositeReviewerRunRefV1{
		RunID:                "reviewer-run",
		AdmissionKey:         "reviewer-admission",
		MemberSnapshotDigest: testDigest("c"),
		Agent: corecontract.AgentRef{
			ID: "reviewer-agent", Version: "1", Digest: testDigest("d"),
		},
		Profile: corecontract.ProfileRef{
			ID: "reviewer-profile", Version: "1", Digest: testDigest("e"),
		},
		TaskInputRef:        input.TaskInputRef,
		ReviewLogicalStepID: corecontract.CompositeReviewLogicalStepIDV1,
		Policy:              corecontract.CompositeReviewerPolicyResultsGateV1,
		MaxOutputTokens:     512,
	}
	rootPlan.Reviewer = &reviewer
	rootPlan.FamilyModelDispatchLimit++
	results := make([]corecontract.CompositeSpecialistResultV1, len(rootPlan.Children))
	for index, child := range rootPlan.Children {
		material := &input.CompositeChildResults[index]
		material.Assignment = child.Assignment
		material.TerminalFrameRevision = uint64(index + 21)
		results[index] = corecontract.CompositeSpecialistResultV1{
			SlotID:                child.SlotID,
			FocusID:               child.Assignment.FocusID,
			WeightBasisPoints:     child.Assignment.WeightBasisPoints,
			RunID:                 child.RunID,
			ManifestDigest:        material.ChildManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			ResultRef:             material.ResultRef,
			TerminalRunRevision:   material.TerminalRevision,
			TerminalFrameRevision: material.TerminalFrameRevision,
		}
	}
	set, _, specialistDigest, err :=
		corecontract.NewCompositeSpecialistResultSetV1(
			corecontract.CompositeSpecialistResultSetV1{
				SchemaVersion: corecontract.CompositeSpecialistResultSetSchemaVersionV1,
				FamilyDigest:  testDigest("f"),
				TaskInputRef:  input.TaskInputRef,
				Results:       results,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	verdict, verdictCanonical, err := corecontract.NewReviewVerdictV1(
		corecontract.ReviewVerdictV1{
			SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
			FamilyDigest:           set.FamilyDigest,
			SpecialistResultDigest: specialistDigest,
			Decision:               corecontract.ReviewDecisionApproveV1,
			IssueCodes:             []corecontract.ReviewIssueCodeV1{},
			AffectedSlotIDs:        []string{},
			BoundedReason:          "The specialist results are consistent.",
		},
	)
	if err != nil || verdict.Decision != corecontract.ReviewDecisionApproveV1 {
		t.Fatal(err)
	}
	_, resultCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdictCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.CompositeSpecialistResultSet = &set
	input.CompositeSpecialistResultDigest = specialistDigest
	input.CompositeReviewVerdict = &CompositeReviewVerdictMaterialV1{
		ReviewerRunID:          reviewer.RunID,
		ReviewerManifestDigest: testDigest("a"),
		MemberSnapshotDigest:   reviewer.MemberSnapshotDigest,
		AttemptID:              "reviewer-attempt",
		LogicalStepID:          reviewer.ReviewLogicalStepID,
		ResultRef: contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			resultCanonical,
		),
		TerminalRunRevision:   31,
		TerminalFrameRevision: 32,
		ResultCanonical:       resultCanonical,
		VerdictCanonical:      verdictCanonical,
	}
	return input, verdictCanonical, specialistDigest
}
