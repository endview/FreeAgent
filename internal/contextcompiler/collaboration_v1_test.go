package contextcompiler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1CollaborationSpecialistUsesStableStructuredContract(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	child := fixture.plan.Children[0]
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleChildV1,
		RootRunID:            "root-run",
		ParentManifestDigest: fixture.familyDigest,
		ParentSlotID:         child.SlotID,
		Assignment:           &child.Assignment,
	}
	input.CompositeChildResults = nil
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:     fixture.familyDigest,
		ParticipantRunID: child.RunID,
		RootPlan:         fixture.plan,
	}

	first, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(first): %v", err)
	}
	second, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(second): %v", err)
	}
	if !bytes.Equal(first.RequestCanonical, second.RequestCanonical) ||
		!bytes.Equal(first.CompilationCanonical, second.CompilationCanonical) {
		t.Fatal("collaboration Specialist compilation is not deterministic")
	}
	contractIndex := messageIndexWithContentV1(
		first.Request.Messages,
		collaborationSpecialistOutputContractV1,
	)
	assignmentIndex := messageIndexWithPrefixV1(
		first.Request.Messages,
		compositeAssignmentPrefixV1,
	)
	task, err := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if err != nil {
		t.Fatal(err)
	}
	taskIndex := messageIndexWithContentV1(first.Request.Messages, task.Text)
	if contractIndex < 0 || assignmentIndex <= contractIndex ||
		taskIndex <= assignmentIndex ||
		taskIndex != len(first.Request.Messages)-1 ||
		strings.Contains(
			first.Request.Messages[contractIndex].Content,
			child.RunID,
		) || !strings.Contains(
		first.Request.Messages[contractIndex].Content,
		"evidence.ref may use only an immutable knowledge source, document, or chunk digest explicitly present in the current prompt",
	) || strings.Contains(
		first.Request.Messages[contractIndex].Content,
		fixture.familyDigest,
	) {
		t.Fatalf("Specialist messages=%+v", first.Request.Messages)
	}
	if first.Compilation == nil || first.Compilation.Composite == nil ||
		first.Compilation.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		first.Compilation.Composite.Assignment == nil ||
		*first.Compilation.Composite.Assignment != child.Assignment {
		t.Fatalf("Specialist evidence=%+v", first.Compilation)
	}
}

func TestCompileV1NilCollaborationPreservesLegacyReviewerWireCanary(
	t *testing.T,
) {
	input, _, _ := newReviewerEnabledRootCompileInputV1(t)
	if input.CompositeCollaboration != nil || input.Composite.Plan.Decision != nil {
		t.Fatal("legacy Reviewer fixture unexpectedly enables W5 decision material")
	}
	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest := moduleapi.Digest(
		"freeagent.contextcompiler.legacy-reviewer-request-canary/v1",
		compiled.RequestCanonical,
	)
	compilationDigest := moduleapi.Digest(
		"freeagent.contextcompiler.legacy-reviewer-compilation-canary/v1",
		compiled.CompilationCanonical,
	)
	const wantRequest = "b969adf9f6facdeb4fb55f287a009f70004af8083cca170b9706164cfcac8c98"
	const wantCompilation = "15fd36bb21134f67fb1462d6314d97909724bf93173cf2ef1d5ac963afc042df"
	if requestDigest != wantRequest || compilationDigest != wantCompilation {
		t.Fatalf(
			"legacy Reviewer wire canary changed: request=%s compilation=%s",
			requestDigest,
			compilationDigest,
		)
	}
}

func TestCompileV1CollaborationReviewerUsesHostBoundPolicyAndCanonicalSet(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	reviewer := *fixture.plan.Reviewer
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleReviewerV1,
		RootRunID:            "root-run",
		ParentManifestDigest: fixture.familyDigest,
		ParentSlotID:         corecontract.CompositeReviewerParentSlotIDV1,
	}
	input.CompositeCollaboration.ParticipantRunID = reviewer.RunID
	input.CompositeCollaboration.ReviewVerdict = nil

	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1: %v", err)
	}
	policyIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		collaborationReviewPolicyRoundZeroV1,
	)
	firstContribution := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		collaborationContributionPrefixV1,
	)
	if policyIndex < 0 || firstContribution <= policyIndex ||
		strings.Contains(
			compiled.Request.Messages[policyIndex].Content,
			fixture.familyDigest,
		) || strings.Contains(
		compiled.Request.Messages[policyIndex].Content,
		fixture.setDigest,
	) {
		t.Fatalf("Reviewer messages=%+v", compiled.Request.Messages)
	}
	if compiled.Compilation == nil || compiled.Compilation.Composite == nil ||
		compiled.Compilation.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		compiled.Compilation.Composite.SpecialistResultDigest != fixture.setDigest ||
		len(compiled.Compilation.Composite.ChildResults) != len(fixture.set.Contributions) {
		t.Fatalf("Reviewer evidence=%+v", compiled.Compilation)
	}
}

func TestCompileV1CollaborationRootRequiresExactHostBoundApprove(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	compiled, err := CompileV1(fixture.input)
	if err != nil {
		t.Fatalf("CompileV1: %v", err)
	}
	mergeIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		collaborationRootMergeInstructionV1,
	)
	contributionIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		collaborationContributionPrefixV1,
	)
	verdictIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		collaborationVerdictPrefixV1,
	)
	if mergeIndex < 0 || contributionIndex <= mergeIndex ||
		verdictIndex <= contributionIndex {
		t.Fatalf("Root messages=%+v", compiled.Request.Messages)
	}
	outer, err := moduleapi.RestoreModelGenerateOutputV1(
		fixture.input.CompositeCollaboration.ReviewVerdict.ResultCanonical,
	)
	if err != nil || compiled.Request.Messages[verdictIndex].Content !=
		collaborationVerdictPrefixV1+outer.AssistantText ||
		strings.Contains(
			compiled.Request.Messages[verdictIndex].Content,
			`"family_digest"`,
		) || strings.Contains(
		compiled.Request.Messages[verdictIndex].Content,
		`"contribution_set_digest"`,
	) || strings.Contains(
		compiled.Request.Messages[verdictIndex].Content,
		`"repair_round"`,
	) {
		t.Fatalf("Root verdict projection=%q error=%v", compiled.Request.Messages[verdictIndex].Content, err)
	}
	if compiled.Compilation == nil || compiled.Compilation.Composite == nil ||
		compiled.Compilation.Composite.ReviewVerdict == nil ||
		compiled.Compilation.Composite.SpecialistResultDigest != fixture.setDigest ||
		compiled.Compilation.Composite.ReviewVerdict.ResultRef !=
			fixture.input.CompositeCollaboration.ReviewVerdict.ResultRef {
		t.Fatalf("Root evidence=%+v", compiled.Compilation)
	}

	rejected := fixture.input
	material := *fixture.input.CompositeCollaboration
	rejected.CompositeCollaboration = &material
	verdict := *material.ReviewVerdict
	verdict.VerdictCanonical = []byte(`{"schema_version":"collaboration-review-verdict/v1"}`)
	material.ReviewVerdict = &verdict
	if _, err := CompileV1(rejected); err == nil {
		t.Fatal("Root accepted a non-APPROVE or non-canonical verdict")
	}
}

func TestCompileV1CollaborationRepairChildRequiresBoundedLineage(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	repair := fixture.plan.Decision.RepairChildren[0]
	request, requestCanonical := collaborationRepairVerdictV1(t, fixture)
	_, basisCanonical, err := corecontract.NewWorkspaceTaskSummaryV1(
		corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: input.TaskInputRef,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			PreviousSetDigest:  fixture.setDigest,
			VerdictRef:         request.ResultRef,
			Summary:            "Repair the affected risk contribution using the bounded review finding.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleChildV1,
		RepairRound:          corecontract.CompositeRepairRoundOneV1,
		RootRunID:            "root-run",
		ParentManifestDigest: fixture.familyDigest,
		ParentSlotID:         repair.ParentSlotID,
		Assignment:           &repair.Assignment,
	}
	input.CompositeChildResults = nil
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:            fixture.familyDigest,
		ParticipantRunID:        repair.RunID,
		RootPlan:                fixture.plan,
		PreviousContributionSet: &fixture.set,
		RepairRequestVerdict:    &request,
		RepairBasisCanonical:    basisCanonical,
	}
	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1: %v\nverdict=%s", err, requestCanonical)
	}
	contractIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		collaborationSpecialistOutputContractV1,
	)
	basisIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		collaborationRepairBasisPrefixV1,
	)
	assignmentIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		compositeAssignmentPrefixV1,
	)
	task, taskErr := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	taskIndex := messageIndexWithContentV1(compiled.Request.Messages, task.Text)
	if contractIndex < 0 || assignmentIndex <= contractIndex ||
		basisIndex <= assignmentIndex || taskIndex <= basisIndex ||
		taskIndex != len(compiled.Request.Messages)-1 {
		t.Fatalf("Repair messages=%+v", compiled.Request.Messages)
	}

	tampered := input
	material := *input.CompositeCollaboration
	tampered.CompositeCollaboration = &material
	material.RepairBasisCanonical = bytes.Replace(
		basisCanonical,
		[]byte(fixture.setDigest),
		[]byte(testDigest("0")),
		1,
	)
	if _, err := CompileV1(tampered); err == nil {
		t.Fatal("repair Child accepted a mismatched previous-set digest")
	}
	if compiled.Compilation == nil ||
		compiled.Compilation.FinalRequestDigest != contentDigest(
			"MODEL_REQUEST",
			jsonMediaType,
			compiled.RequestCanonical,
		) {
		t.Fatal("repair Child compilation did not retain the exact final request ref")
	}
}

func TestCompileV1CollaborationRoundOneReviewerAndRootCloseLineage(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	rootInput, repairedSetDigest := collaborationRoundOneRootInputV1(t, fixture)

	root, err := CompileV1(rootInput)
	if err != nil {
		t.Fatalf("CompileV1(round-one Root): %v", err)
	}
	evidence := root.Compilation.Composite
	if evidence == nil || evidence.Role != corecontract.CompositeRunRoleRootV1 ||
		evidence.SpecialistResultDigest != repairedSetDigest ||
		evidence.ReviewVerdict == nil ||
		evidence.ReviewVerdict.ReviewerRunID !=
			fixture.plan.Decision.RepairReviewer.RunID ||
		evidence.ReviewVerdict.Decision != corecontract.ReviewDecisionApproveV1 ||
		len(evidence.ChildResults) != len(fixture.plan.Children) ||
		evidence.ChildResults[0].RunID !=
			fixture.plan.Decision.RepairChildren[0].RunID ||
		evidence.ChildResults[1].RunID != fixture.plan.Children[1].RunID ||
		root.Compilation.FinalRequestDigest != contentDigest(
			"MODEL_REQUEST",
			jsonMediaType,
			root.RequestCanonical,
		) {
		t.Fatalf("round-one Root recovery evidence=%+v", evidence)
	}
	mergeIndex := messageIndexWithContentV1(
		root.Request.Messages,
		collaborationRootMergeInstructionV1,
	)
	contributionIndex := messageIndexWithPrefixV1(
		root.Request.Messages,
		collaborationContributionPrefixV1,
	)
	verdictIndex := messageIndexWithPrefixV1(
		root.Request.Messages,
		collaborationVerdictPrefixV1,
	)
	if mergeIndex < 0 || contributionIndex <= mergeIndex ||
		verdictIndex <= contributionIndex ||
		strings.Contains(root.Request.Messages[mergeIndex].Content, repairedSetDigest) {
		t.Fatalf("round-one Root message order=%+v", root.Request.Messages)
	}

	reviewerInput := rootInput
	repairReviewer := fixture.plan.Decision.RepairReviewer
	reviewerInput.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleReviewerV1,
		RepairRound:          corecontract.CompositeRepairRoundOneV1,
		RootRunID:            "root-run",
		ParentManifestDigest: fixture.familyDigest,
		ParentSlotID:         repairReviewer.ParentSlotID,
	}
	reviewerMaterial := *rootInput.CompositeCollaboration
	reviewerMaterial.ParticipantRunID = repairReviewer.RunID
	reviewerMaterial.ReviewVerdict = nil
	reviewerInput.CompositeCollaboration = &reviewerMaterial
	reviewer, err := CompileV1(reviewerInput)
	if err != nil {
		t.Fatalf("CompileV1(round-one Reviewer): %v", err)
	}
	if messageIndexWithContentV1(
		reviewer.Request.Messages,
		collaborationReviewPolicyRoundOneV1,
	) < 0 || reviewer.Compilation.Composite == nil ||
		reviewer.Compilation.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		reviewer.Compilation.Composite.SpecialistResultDigest != repairedSetDigest ||
		reviewer.Compilation.Composite.ReviewVerdict != nil {
		t.Fatalf("round-one Reviewer compilation=%+v", reviewer.Compilation)
	}
}

func TestCompileV1CollaborationRequiresCompleteOuterModelResults(
	t *testing.T,
) {
	t.Run("Specialist contribution", func(t *testing.T) {
		fixture := newCollaborationCompileFixtureV1(t)
		input := fixture.input
		reviewer := *fixture.plan.Reviewer
		input.Composite = &corecontract.CompositeRunNodeV1{
			SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
			Role:                 corecontract.CompositeRunRoleReviewerV1,
			RootRunID:            "root-run",
			ParentManifestDigest: fixture.familyDigest,
			ParentSlotID:         corecontract.CompositeReviewerParentSlotIDV1,
		}
		input.CompositeChildResults = append(
			[]CompositeChildResultV1(nil),
			fixture.input.CompositeChildResults...,
		)
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			input.CompositeChildResults[0].ResultCanonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		bare := []byte(output.AssistantText)
		bareRef := contentDigest("MODEL_RESULT", jsonMediaType, bare)
		input.CompositeChildResults[0].ResultCanonical = bare
		input.CompositeChildResults[0].ResultRef = bareRef
		set := fixture.set
		set.Contributions = append(
			[]corecontract.CollaborationContributionEntryV1(nil),
			fixture.set.Contributions...,
		)
		set.Contributions[0].ResultRef = bareRef
		set, _, setDigest, err := corecontract.NewCollaborationContributionSetV1(set)
		if err != nil {
			t.Fatal(err)
		}
		material := *fixture.input.CompositeCollaboration
		material.ParticipantRunID = reviewer.RunID
		material.ContributionSet = &set
		material.ContributionSetDigest = setDigest
		material.ReviewVerdict = nil
		input.CompositeCollaboration = &material
		if _, err := CompileV1(input); err == nil {
			t.Fatal("bare specialist-contribution/v1 was accepted as MODEL_RESULT")
		}
	})

	t.Run("Reviewer verdict", func(t *testing.T) {
		fixture := newCollaborationCompileFixtureV1(t)
		input := fixture.input
		material := *fixture.input.CompositeCollaboration
		verdict := *material.ReviewVerdict
		verdict.ResultCanonical = bytes.Clone(verdict.VerdictCanonical)
		verdict.ResultRef = contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			verdict.ResultCanonical,
		)
		material.ReviewVerdict = &verdict
		input.CompositeCollaboration = &material
		if _, err := CompileV1(input); err == nil {
			t.Fatal("bare collaboration-review-verdict/v1 was accepted as MODEL_RESULT")
		}
	})
}

func TestCompileV1CollaborationRejectsHostIdentityEchoInModelVerdict(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	validOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		fixture.input.CompositeCollaboration.ReviewVerdict.ResultCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(validOutput.AssistantText, `"family_digest"`) ||
		!bytes.Contains(
			fixture.input.CompositeCollaboration.ReviewVerdict.VerdictCanonical,
			[]byte(`"family_digest"`),
		) {
		t.Fatal("fixture does not separate model-owned and Host-bound verdict wires")
	}

	input := fixture.input
	material := *fixture.input.CompositeCollaboration
	verdict := *material.ReviewVerdict
	_, echoedOuter, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdict.VerdictCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	verdict.ResultCanonical = echoedOuter
	verdict.ResultRef = contentDigest("MODEL_RESULT", jsonMediaType, echoedOuter)
	material.ReviewVerdict = &verdict
	input.CompositeCollaboration = &material
	if _, err := CompileV1(input); err == nil {
		t.Fatal("model verdict that echoed Host family/set/round identity was accepted")
	}

	nonCanonicalInput := fixture.input
	nonCanonicalMaterial := *fixture.input.CompositeCollaboration
	nonCanonicalVerdict := *nonCanonicalMaterial.ReviewVerdict
	_, nonCanonicalOuter, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: " " + validOutput.AssistantText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	nonCanonicalVerdict.ResultCanonical = nonCanonicalOuter
	nonCanonicalVerdict.ResultRef = contentDigest(
		"MODEL_RESULT",
		jsonMediaType,
		nonCanonicalOuter,
	)
	nonCanonicalMaterial.ReviewVerdict = &nonCanonicalVerdict
	nonCanonicalInput.CompositeCollaboration = &nonCanonicalMaterial
	if _, err := CompileV1(nonCanonicalInput); err == nil {
		t.Fatal("non-canonical model-owned Reviewer AssistantText was accepted")
	}
}

func TestCompileV1CollaborationRejectsReviewerMemberCollision(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	plan := *fixture.plan
	plan.Children = append(
		[]corecontract.CompositeChildRunRefV1(nil),
		fixture.plan.Children...,
	)
	reviewer := *fixture.plan.Reviewer
	reviewer.MemberSnapshotDigest = plan.Children[0].MemberSnapshotDigest
	plan.Reviewer = &reviewer
	node := *input.Composite
	node.Plan = &plan
	input.Composite = &node
	material := *input.CompositeCollaboration
	material.RootPlan = &plan
	input.CompositeCollaboration = &material
	if _, err := CompileV1(input); err == nil {
		t.Fatal("Reviewer member snapshot collision with a Specialist was accepted")
	}
}

type collaborationCompileFixtureV1 struct {
	input        CompileInputV1
	plan         *corecontract.CompositeRunPlanV1
	familyDigest string
	set          corecontract.CollaborationContributionSetV1
	setDigest    string
}

func newCollaborationCompileFixtureV1(
	t *testing.T,
) collaborationCompileFixtureV1 {
	t.Helper()
	input, _, _ := newReviewerEnabledRootCompileInputV1(t)
	input.CompositeSpecialistResultSet = nil
	input.CompositeSpecialistResultDigest = ""
	input.CompositeReviewVerdict = nil
	plan := input.Composite.Plan
	repairs := make([]corecontract.CompositeChildRunRefV1, len(plan.Children))
	for index, child := range plan.Children {
		repair := child
		repair.ParentSlotID = "__repair__" + child.SlotID
		repair.RunID = "repair-" + child.RunID
		repair.AdmissionKey = "repair-" + child.AdmissionKey
		repair.MemberSnapshotDigest = testDigest(string(rune('0' + index)))
		repairs[index] = repair
	}
	repairReviewer := *plan.Reviewer
	repairReviewer.ParentSlotID = "__reviewer_repair__"
	repairReviewer.RunID = "repair-reviewer-run"
	repairReviewer.AdmissionKey = "repair-reviewer-admission"
	repairReviewer.MemberSnapshotDigest = testDigest("b")
	plan.Decision = &corecontract.CompositeDecisionPlanV1{
		SchemaVersion:  corecontract.CompositeDecisionPlanSchemaVersionV1,
		RepairChildren: repairs,
		RepairReviewer: repairReviewer,
	}
	plan.FamilyModelDispatchLimit = uint32(len(plan.Children)*2 + 3)

	familyDigest := testDigest("f")
	entries := make(
		[]corecontract.CollaborationContributionEntryV1,
		len(plan.Children),
	)
	for index, child := range plan.Children {
		contribution, contributionCanonical, contributionDigest, err :=
			corecontract.NewSpecialistContributionV1(
				corecontract.SpecialistContributionV1{
					SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
					Proposal:      "Structured proposal for " + child.Assignment.FocusID + ".",
					Evidence:      []corecontract.SpecialistEvidenceV1{},
					Assumptions:   []string{},
					Risks:         []string{"The proposal requires review."},
					Conflicts:     []string{},
				},
			)
		if err != nil || contribution.Proposal == "" {
			t.Fatal(err)
		}
		_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
			moduleapi.ModelGenerateOutputV1{
				SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
				AssistantText: string(contributionCanonical),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		material := &input.CompositeChildResults[index]
		material.Assignment = child.Assignment
		material.ResultCanonical = outputCanonical
		material.ResultRef = contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			outputCanonical,
		)
		entries[index] = corecontract.CollaborationContributionEntryV1{
			SlotID:             child.SlotID,
			RunID:              child.RunID,
			ResultRef:          material.ResultRef,
			ContributionDigest: contributionDigest,
		}
	}
	set, _, setDigest, err := corecontract.NewCollaborationContributionSetV1(
		corecontract.CollaborationContributionSetV1{
			SchemaVersion: corecontract.CollaborationContributionSetSchemaVersionV1,
			FamilyDigest:  familyDigest,
			RepairRound:   0,
			Contributions: entries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	approve, approveCanonical, err := corecontract.NewCollaborationReviewVerdictV1(
		corecontract.CollaborationReviewVerdictV1{
			SchemaVersion:         corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:          familyDigest,
			ContributionSetDigest: setDigest,
			RepairRound:           0,
			Decision:              corecontract.CollaborationReviewDecisionApproveV1,
			IssueCodes:            []corecontract.ReviewIssueCodeV1{},
			AffectedSlotIDs:       []string{},
			BoundedReason:         "The structured contribution set is complete.",
		},
	)
	if err != nil || approve.Decision != corecontract.CollaborationReviewDecisionApproveV1 {
		t.Fatal(err)
	}
	approveModelCanonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(approve)
	if err != nil {
		t.Fatal(err)
	}
	_, approveOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(approveModelCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewMaterial := &CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID:          plan.Reviewer.RunID,
		ReviewerManifestDigest: testDigest("a"),
		MemberSnapshotDigest:   plan.Reviewer.MemberSnapshotDigest,
		AttemptID:              "collaboration-review-attempt",
		LogicalStepID:          plan.Reviewer.ReviewLogicalStepID,
		ResultRef: contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			approveOutput,
		),
		TerminalRunRevision:   41,
		TerminalFrameRevision: 42,
		ResultCanonical:       approveOutput,
		VerdictCanonical:      approveCanonical,
	}
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:          familyDigest,
		ParticipantRunID:      "root-run",
		RootPlan:              plan,
		ContributionSet:       &set,
		ContributionSetDigest: setDigest,
		ReviewVerdict:         reviewMaterial,
	}
	return collaborationCompileFixtureV1{
		input: input, plan: plan, familyDigest: familyDigest,
		set: set, setDigest: setDigest,
	}
}

func collaborationRepairVerdictV1(
	t *testing.T,
	fixture collaborationCompileFixtureV1,
) (CompositeCollaborationReviewVerdictMaterialV1, []byte) {
	t.Helper()
	verdict, canonical, err := corecontract.NewCollaborationReviewVerdictV1(
		corecontract.CollaborationReviewVerdictV1{
			SchemaVersion:         corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:          fixture.familyDigest,
			ContributionSetDigest: fixture.setDigest,
			RepairRound:           0,
			Decision:              corecontract.CollaborationReviewDecisionRepairRequiredV1,
			IssueCodes: []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueMissingEvidenceV1,
			},
			AffectedSlotIDs: []string{fixture.plan.Children[0].SlotID},
			BoundedReason:   "The risk contribution needs bounded repair.",
		},
	)
	if err != nil || verdict.Decision !=
		corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		t.Fatal(err)
	}
	modelCanonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
	if err != nil {
		t.Fatal(err)
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(modelCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := fixture.plan.Reviewer
	return CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID:          reviewer.RunID,
		ReviewerManifestDigest: testDigest("a"),
		MemberSnapshotDigest:   reviewer.MemberSnapshotDigest,
		AttemptID:              "collaboration-repair-request-attempt",
		LogicalStepID:          reviewer.ReviewLogicalStepID,
		ResultRef: contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			outputCanonical,
		),
		TerminalRunRevision:   51,
		TerminalFrameRevision: 52,
		ResultCanonical:       outputCanonical,
		VerdictCanonical:      canonical,
	}, canonical
}

func collaborationRoundOneRootInputV1(
	t *testing.T,
	fixture collaborationCompileFixtureV1,
) (CompileInputV1, string) {
	t.Helper()
	input := fixture.input
	input.CompositeChildResults = append(
		[]CompositeChildResultV1(nil),
		fixture.input.CompositeChildResults...,
	)
	for index := range input.CompositeChildResults {
		input.CompositeChildResults[index].ResultCanonical = bytes.Clone(
			input.CompositeChildResults[index].ResultCanonical,
		)
	}
	repairRequest, _ := collaborationRepairVerdictV1(t, fixture)
	repair := fixture.plan.Decision.RepairChildren[0]
	contribution, contributionCanonical, contributionDigest, err :=
		corecontract.NewSpecialistContributionV1(
			corecontract.SpecialistContributionV1{
				SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
				Proposal:      "Repaired bounded proposal for the affected risk slot.",
				Evidence:      []corecontract.SpecialistEvidenceV1{},
				Assumptions:   []string{},
				Risks:         []string{"The repaired proposal still requires final review."},
				Conflicts:     []string{},
			},
		)
	if err != nil || contribution.Proposal == "" {
		t.Fatal(err)
	}
	_, resultCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(contributionCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resultRef := contentDigest("MODEL_RESULT", jsonMediaType, resultCanonical)
	input.CompositeChildResults[0] = CompositeChildResultV1{
		SlotID:                repair.SlotID,
		RunID:                 repair.RunID,
		AdmissionKey:          repair.AdmissionKey,
		ChildManifestDigest:   testDigest("8"),
		MemberSnapshotDigest:  repair.MemberSnapshotDigest,
		Assignment:            repair.Assignment,
		ResultRef:             resultRef,
		TerminalRevision:      61,
		TerminalFrameRevision: 62,
		ResultCanonical:       resultCanonical,
	}
	entries := append(
		[]corecontract.CollaborationContributionEntryV1(nil),
		fixture.set.Contributions...,
	)
	entries[0] = corecontract.CollaborationContributionEntryV1{
		SlotID:             repair.SlotID,
		RunID:              repair.RunID,
		ResultRef:          resultRef,
		ContributionDigest: contributionDigest,
	}
	repairedSet, _, repairedSetDigest, err :=
		corecontract.NewCollaborationContributionSetV1(
			corecontract.CollaborationContributionSetV1{
				SchemaVersion:     corecontract.CollaborationContributionSetSchemaVersionV1,
				FamilyDigest:      fixture.familyDigest,
				RepairRound:       corecontract.CompositeRepairRoundOneV1,
				PreviousSetDigest: fixture.setDigest,
				VerdictRef:        repairRequest.ResultRef,
				Contributions:     entries,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	approve, approveCanonical, err := corecontract.NewCollaborationReviewVerdictV1(
		corecontract.CollaborationReviewVerdictV1{
			SchemaVersion:         corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:          fixture.familyDigest,
			ContributionSetDigest: repairedSetDigest,
			RepairRound:           corecontract.CompositeRepairRoundOneV1,
			Decision:              corecontract.CollaborationReviewDecisionApproveV1,
			IssueCodes:            []corecontract.ReviewIssueCodeV1{},
			AffectedSlotIDs:       []string{},
			BoundedReason:         "The repaired contribution set is complete.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	approveModelCanonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(approve)
	if err != nil {
		t.Fatal(err)
	}
	_, approveOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(approveModelCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repairReviewer := fixture.plan.Decision.RepairReviewer
	reviewMaterial := &CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID:          repairReviewer.RunID,
		ReviewerManifestDigest: testDigest("9"),
		MemberSnapshotDigest:   repairReviewer.MemberSnapshotDigest,
		AttemptID:              "collaboration-repair-review-attempt",
		LogicalStepID:          repairReviewer.ReviewLogicalStepID,
		ResultRef: contentDigest(
			"MODEL_RESULT",
			jsonMediaType,
			approveOutput,
		),
		TerminalRunRevision:   71,
		TerminalFrameRevision: 72,
		ResultCanonical:       approveOutput,
		VerdictCanonical:      approveCanonical,
	}
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:            fixture.familyDigest,
		ParticipantRunID:        "root-run",
		RootPlan:                fixture.plan,
		ContributionSet:         &repairedSet,
		ContributionSetDigest:   repairedSetDigest,
		PreviousContributionSet: &fixture.set,
		RepairRequestVerdict:    &repairRequest,
		ReviewVerdict:           reviewMaterial,
	}
	return input, repairedSetDigest
}

func messageIndexWithContentV1(
	messages []moduleapi.ModelMessageV1,
	content string,
) int {
	for index, message := range messages {
		if message.Content == content {
			return index
		}
	}
	return -1
}

func messageIndexWithPrefixV1(
	messages []moduleapi.ModelMessageV1,
	prefix string,
) int {
	for index, message := range messages {
		if strings.HasPrefix(message.Content, prefix) {
			return index
		}
	}
	return -1
}
