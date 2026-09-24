package contextcompiler

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1WorkspaceTransferRequestUsesOnlyTrustedSummaryProjection(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	plan, transfer := workspaceTransferTestPlanV1(t, *fixture.plan, input.WorkspaceScope)
	root := workspaceTransferTestRootManifestV1(t, input, plan)
	childRef := plan.Children[0]
	child := workspaceTransferTestChildManifestV1(t, input, root, childRef, 0)

	input.WorkspaceScope = transfer.TargetWorkspace
	input.Composite = child.Composite
	input.CompositeChildResults = nil
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:     root.ManifestDigest,
		ParticipantRunID: childRef.RunID,
		RootPlan:         &plan,
	}
	summary, summaryCanonical, err := CompileWorkspaceTaskSummaryV1(
		input.TaskInputRef,
		input.TaskInputCanonical,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	knowledge := addKnowledgeContext(t, &input, "authorized target-Workspace fact")
	workspaceTransferRewriteKnowledgeQueryV1(
		t,
		&input,
		knowledge.BindingIndex,
		summary.Summary,
	)
	input.WorkspaceTransfers = []WorkspaceTransferMaterialV1{
		workspaceTransferTestMaterialV1(
			t,
			root,
			child,
			*childRef.Transfer,
			corecontract.WorkspaceTransferDirectionRequestV1,
			summaryCanonical,
		),
	}

	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(cross-Workspace Specialist): %v", err)
	}
	summaryIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		summary.Summary,
	)
	assignmentIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		compositeAssignmentPrefixV1,
	)
	contractIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		collaborationWorkspaceTransferSpecialistOutputContractV1,
	)
	if contractIndex < 0 || summaryIndex <= contractIndex ||
		compiled.Request.Messages[summaryIndex].Role != moduleapi.ModelRoleUser ||
		assignmentIndex <= summaryIndex ||
		assignmentIndex != len(compiled.Request.Messages)-1 {
		t.Fatalf(
			"Specialist summary/assignment placement=%+v, want trusted summary followed by the frozen assignment",
			compiled.Request.Messages,
		)
	}
	request, _, err := moduleapi.RestoreKnowledgeContextRequestV1(
		input.ContextBindings[knowledge.BindingIndex].DynamicRequestCanonical,
	)
	if err != nil || request.QueryText != summary.Summary {
		t.Fatalf("Knowledge query did not use transferred summary: %+v, %v", request, err)
	}
	if compiled.Compilation == nil || len(compiled.Compilation.WorkspaceTransfers) != 1 {
		t.Fatalf("Workspace transfer evidence=%+v", compiled.Compilation)
	}
	evidence := compiled.Compilation.WorkspaceTransfers[0]
	if evidence.Direction != corecontract.WorkspaceTransferDirectionRequestV1 ||
		evidence.EnvelopeRef != input.WorkspaceTransfers[0].EnvelopeRef ||
		evidence.EnvelopeDigest != input.WorkspaceTransfers[0].EnvelopeDigest ||
		evidence.PayloadRef != input.WorkspaceTransfers[0].ResolvedPayload.PayloadRef ||
		evidence.ChildRunID != child.RunID || evidence.SlotID != childRef.SlotID {
		t.Fatalf("REQUEST evidence=%+v", evidence)
	}

	// A self-consistent but caller-authored summary and envelope must still be
	// rejected because the compiler re-runs the sole trusted summary compiler.
	_, forgedCanonical, err := corecontract.NewWorkspaceTaskSummaryV1(
		corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: input.TaskInputRef,
			Summary:            `{"schema_version":"workspace-task-extract/v1","task_excerpt":"forged"}`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forged := input
	forged.WorkspaceTransfers = []WorkspaceTransferMaterialV1{
		workspaceTransferTestMaterialV1(
			t,
			root,
			child,
			*childRef.Transfer,
			corecontract.WorkspaceTransferDirectionRequestV1,
			forgedCanonical,
		),
	}
	if _, err := CompileV1(forged); err == nil {
		t.Fatal("accepted caller-authored Workspace task summary")
	}
	tamperedFamily := input
	collaboration := *input.CompositeCollaboration
	tamperedFamily.CompositeCollaboration = &collaboration
	collaboration.FamilyDigest = testDigest("f")
	tamperedNode := *input.Composite
	tamperedFamily.Composite = &tamperedNode
	tamperedNode.ParentManifestDigest = collaboration.FamilyDigest
	if _, err := CompileV1(tamperedFamily); err == nil {
		t.Fatal("accepted Transfer material outside the frozen root family")
	}
}

func TestCompileV1WorkspaceTransferFamilyPlacesSameWorkspaceAssignmentAfterTask(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	plan, _ := workspaceTransferTestPlanV1(t, *fixture.plan, input.WorkspaceScope)
	root := workspaceTransferTestRootManifestV1(t, input, plan)
	if len(plan.Children) < 2 || plan.Children[1].Transfer != nil {
		t.Fatal("Workspace transfer fixture lacks a same-Workspace sibling")
	}
	childRef := plan.Children[1]
	child := workspaceTransferTestChildManifestV1(t, input, root, childRef, 0)
	input.Composite = child.Composite
	input.CompositeChildResults = nil
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:     root.ManifestDigest,
		ParticipantRunID: childRef.RunID,
		RootPlan:         &plan,
	}

	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(same-Workspace Specialist in transfer family): %v", err)
	}
	task, err := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if err != nil {
		t.Fatal(err)
	}
	taskIndex := messageIndexWithContentV1(compiled.Request.Messages, task.Text)
	assignmentIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		compositeAssignmentPrefixV1,
	)
	contractIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		collaborationWorkspaceTransferSpecialistOutputContractV1,
	)
	if contractIndex < 0 || taskIndex <= contractIndex ||
		assignmentIndex <= taskIndex ||
		assignmentIndex != len(compiled.Request.Messages)-1 {
		t.Fatalf(
			"same-Workspace transfer-family task/assignment placement=%+v",
			compiled.Request.Messages,
		)
	}
}

func workspaceTransferRewriteKnowledgeQueryV1(
	t *testing.T,
	input *CompileInputV1,
	bindingIndex uint32,
	queryText string,
) {
	t.Helper()
	material := &input.ContextBindings[bindingIndex]
	request, _, err := moduleapi.RestoreKnowledgeContextRequestV1(
		material.DynamicRequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.QueryText = queryText
	_, requestCanonical, requestDigest, err :=
		moduleapi.NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(
		material.DynamicOutputCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	output.RequestDigest = requestDigest
	_, outputCanonical, _, err := moduleapi.NewKnowledgeContextOutputV1(output)
	if err != nil {
		t.Fatal(err)
	}
	material.DynamicRequestCanonical = requestCanonical
	material.DynamicOutputCanonical = outputCanonical
}

func TestCompileV1WorkspaceTransferRepairRecompilesTaskAndBasis(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	plan, transfer := workspaceTransferTestPlanV1(t, *fixture.plan, input.WorkspaceScope)
	root := workspaceTransferTestRootManifestV1(t, input, plan)
	previous, previousDigest := workspaceTransferTestContributionSetV1(
		t,
		root.ManifestDigest,
		input.CompositeChildResults,
	)
	repairVerdict := workspaceTransferTestRepairVerdictV1(
		t,
		root.ManifestDigest,
		previousDigest,
		plan,
	)
	_, basisCanonical, err := corecontract.NewWorkspaceTaskSummaryV1(
		corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: input.TaskInputRef,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			PreviousSetDigest:  previousDigest,
			VerdictRef:         repairVerdict.ResultRef,
			Summary:            `{"bounded_reason":"repair risk","schema_version":"repair-basis/v1","slot_id":"risk"}`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repairRef := plan.Decision.RepairChildren[0]
	repairChild := workspaceTransferTestChildManifestV1(
		t,
		input,
		root,
		repairRef,
		corecontract.CompositeRepairRoundOneV1,
	)
	input.WorkspaceScope = transfer.TargetWorkspace
	input.Composite = repairChild.Composite
	input.CompositeChildResults = nil
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:            root.ManifestDigest,
		ParticipantRunID:        repairRef.RunID,
		RootPlan:                &plan,
		PreviousContributionSet: &previous,
		RepairRequestVerdict:    &repairVerdict,
		RepairBasisCanonical:    basisCanonical,
	}
	summary, summaryCanonical, err := CompileWorkspaceTaskSummaryV1(
		input.TaskInputRef,
		input.TaskInputCanonical,
		basisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.WorkspaceTransfers = []WorkspaceTransferMaterialV1{
		workspaceTransferTestMaterialV1(
			t,
			root,
			repairChild,
			*repairRef.Transfer,
			corecontract.WorkspaceTransferDirectionRequestV1,
			summaryCanonical,
		),
	}
	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(cross-Workspace repair Specialist): %v", err)
	}
	summaryIndex := messageIndexWithContentV1(
		compiled.Request.Messages,
		summary.Summary,
	)
	assignmentIndex := messageIndexWithPrefixV1(
		compiled.Request.Messages,
		compositeAssignmentPrefixV1,
	)
	if summaryIndex < 0 ||
		compiled.Request.Messages[summaryIndex].Role != moduleapi.ModelRoleUser ||
		assignmentIndex <= summaryIndex ||
		assignmentIndex != len(compiled.Request.Messages)-1 {
		t.Fatalf(
			"repair Specialist summary/assignment placement=%+v",
			compiled.Request.Messages,
		)
	}
	if bytes.Equal(summaryCanonical, basisCanonical) {
		t.Fatal("test fixture failed to distinguish repair basis from compiled REQUEST")
	}
}

func TestCompileV1WorkspaceTransferResultsAreRequiredAndPlanOrdered(
	t *testing.T,
) {
	fixture := newCollaborationCompileFixtureV1(t)
	input := fixture.input
	plan, _ := workspaceTransferTestPlanV1(t, *fixture.plan, input.WorkspaceScope)
	// Exercise two edges so reversing only the Transfer material (while every
	// envelope remains individually valid) proves deterministic plan ordering.
	secondTransfer := *plan.Children[0].Transfer
	plan.Children[1].Transfer = &secondTransfer
	secondRepairTransfer := secondTransfer
	plan.Decision.RepairChildren[1].Transfer = &secondRepairTransfer
	root := workspaceTransferTestRootManifestV1(t, input, plan)
	children := make([]corecontract.RunManifest, len(plan.Children))
	for index, childRef := range plan.Children {
		children[index] = workspaceTransferTestChildManifestV1(
			t,
			input,
			root,
			childRef,
			0,
		)
		input.CompositeChildResults[index].ChildManifestDigest =
			children[index].ManifestDigest
	}
	set, setDigest := workspaceTransferTestContributionSetV1(
		t,
		root.ManifestDigest,
		input.CompositeChildResults,
	)
	reviewer := *plan.Reviewer
	input.Composite = &corecontract.CompositeRunNodeV1{
		SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
		Role:                 corecontract.CompositeRunRoleReviewerV1,
		RootRunID:            root.RunID,
		ParentManifestDigest: root.ManifestDigest,
		ParentSlotID:         corecontract.CompositeReviewerParentSlotIDV1,
	}
	input.CompositeCollaboration = &CompositeCollaborationMaterialV1{
		FamilyDigest:          root.ManifestDigest,
		ParticipantRunID:      reviewer.RunID,
		RootPlan:              &plan,
		ContributionSet:       &set,
		ContributionSetDigest: setDigest,
	}
	input.CompositeSpecialistResultSet = nil
	input.CompositeSpecialistResultDigest = ""
	input.CompositeReviewVerdict = nil
	input.WorkspaceTransfers = make(
		[]WorkspaceTransferMaterialV1,
		len(plan.Children),
	)
	for index, childRef := range plan.Children {
		input.WorkspaceTransfers[index] = workspaceTransferTestMaterialV1(
			t,
			root,
			children[index],
			*childRef.Transfer,
			corecontract.WorkspaceTransferDirectionResultV1,
			input.CompositeChildResults[index].ResultCanonical,
		)
	}

	compiled, err := CompileV1(input)
	if err != nil {
		t.Fatalf("CompileV1(cross-Workspace Reviewer): %v", err)
	}
	if compiled.Compilation == nil ||
		len(compiled.Compilation.WorkspaceTransfers) != len(plan.Children) {
		t.Fatalf("RESULT evidence=%+v", compiled.Compilation)
	}
	for index, evidence := range compiled.Compilation.WorkspaceTransfers {
		if evidence.ChildRunID != plan.Children[index].RunID ||
			evidence.PayloadRef != input.CompositeChildResults[index].ResultRef {
			t.Fatalf("RESULT evidence[%d]=%+v", index, evidence)
		}
	}

	missing := input
	missing.WorkspaceTransfers = input.WorkspaceTransfers[:1]
	if _, err := CompileV1(missing); err == nil {
		t.Fatal("accepted cross-Workspace contribution without RESULT edge")
	}
	reordered := input
	reordered.WorkspaceTransfers = append(
		[]WorkspaceTransferMaterialV1(nil),
		input.WorkspaceTransfers...,
	)
	reordered.WorkspaceTransfers[0], reordered.WorkspaceTransfers[1] =
		reordered.WorkspaceTransfers[1], reordered.WorkspaceTransfers[0]
	if _, err := CompileV1(reordered); err == nil {
		t.Fatal("accepted RESULT edges outside frozen contribution order")
	}
	tampered := input
	tampered.WorkspaceTransfers = append(
		[]WorkspaceTransferMaterialV1(nil),
		input.WorkspaceTransfers...,
	)
	tampered.WorkspaceTransfers[0].EnvelopeRef = strings.Repeat("f", 64)
	if _, err := CompileV1(tampered); err == nil {
		t.Fatal("accepted tampered Workspace transfer EnvelopeRef")
	}
}

func workspaceTransferTestPlanV1(
	t *testing.T,
	input corecontract.CompositeRunPlanV1,
	rootWorkspace corecontract.WorkspaceRef,
) (corecontract.CompositeRunPlanV1, corecontract.WorkspaceTransferPlanV1) {
	t.Helper()
	targetWorkspace := corecontract.WorkspaceRef{
		ID: "workspace.specialist", Version: "1", Digest: testDigest("7"),
	}
	payloads := []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
	}
	rootGrant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       "root-to-specialist", TenantID: "tenant-transfer",
		Workspace: rootWorkspace, PeerWorkspace: targetWorkspace,
		Revision: 1, Enabled: true,
		SendPayloadKinds: payloads, ReceivePayloadKinds: payloads,
		MaxSendPayloadBytes:    corecontract.WorkspaceTransferMaximumPayloadBytesV1,
		MaxReceivePayloadBytes: corecontract.WorkspaceTransferMaximumPayloadBytesV1,
	}
	targetGrant := rootGrant
	targetGrant.GrantID = "specialist-to-root"
	targetGrant.Workspace = targetWorkspace
	targetGrant.PeerWorkspace = rootWorkspace
	transfer, err := corecontract.NewWorkspaceTransferPlanV1(rootGrant, targetGrant)
	if err != nil {
		t.Fatal(err)
	}
	plan := input
	plan.Children = append([]corecontract.CompositeChildRunRefV1(nil), input.Children...)
	plan.Decision = &corecontract.CompositeDecisionPlanV1{
		SchemaVersion: input.Decision.SchemaVersion,
		RepairChildren: append(
			[]corecontract.CompositeChildRunRefV1(nil),
			input.Decision.RepairChildren...,
		),
		RepairReviewer: input.Decision.RepairReviewer,
	}
	initialTransfer := transfer
	plan.Children[0].Transfer = &initialTransfer
	repairTransfer := transfer
	plan.Decision.RepairChildren[0].Transfer = &repairTransfer
	return plan, transfer
}

func workspaceTransferTestRootManifestV1(
	t *testing.T,
	input CompileInputV1,
	plan corecontract.CompositeRunPlanV1,
) corecontract.RunManifest {
	t.Helper()
	root, _, err := corecontract.NewRunManifest(corecontract.RunManifest{
		SchemaVersion:         corecontract.RunManifestSchemaVersionV2,
		CoreRuntimeVersion:    corecontract.CoreRuntimeVersionV2,
		AdmissionKey:          "root-admission",
		AdmissionIntentDigest: testDigest("1"),
		RunID:                 "root-run",
		TenantID:              "tenant-transfer",
		Workspace:             input.WorkspaceScope,
		PrimaryAgent:          input.AgentScope,
		Members: []corecontract.MemberSnapshotRef{{
			MemberID: "root-member", Digest: testDigest("2"),
		}},
		PrimaryMemberID:   "root-member",
		TaskInputRef:      input.TaskInputRef,
		TaskInputDigest:   input.TaskInputRef,
		CancellationScope: corecontract.CancellationScopeFamilyV1,
		Deadline:          time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC),
		RecoveryRootRef:   "recovery/root-run",
		Composite: &corecontract.CompositeRunNodeV1{
			SchemaVersion: corecontract.CompositeRunNodeSchemaVersionV1,
			Role:          corecontract.CompositeRunRoleRootV1,
			RootRunID:     "root-run",
			Plan:          &plan,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func workspaceTransferTestChildManifestV1(
	t *testing.T,
	input CompileInputV1,
	root corecontract.RunManifest,
	planned corecontract.CompositeChildRunRefV1,
	repairRound uint32,
) corecontract.RunManifest {
	t.Helper()
	parentSlot := planned.SlotID
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		parentSlot = planned.ParentSlotID
	}
	workspace := input.WorkspaceScope
	if planned.Transfer != nil {
		workspace = planned.Transfer.TargetWorkspace
	}
	child, _, err := corecontract.NewRunManifest(corecontract.RunManifest{
		SchemaVersion:         corecontract.RunManifestSchemaVersionV2,
		CoreRuntimeVersion:    corecontract.CoreRuntimeVersionV2,
		AdmissionKey:          planned.AdmissionKey,
		AdmissionIntentDigest: testDigest("3"),
		RunID:                 planned.RunID,
		TenantID:              root.TenantID,
		Workspace:             workspace,
		PrimaryAgent:          planned.Agent,
		Members: []corecontract.MemberSnapshotRef{{
			MemberID: "member-" + planned.RunID,
			Digest:   planned.MemberSnapshotDigest,
		}},
		PrimaryMemberID:   "member-" + planned.RunID,
		TaskInputRef:      input.TaskInputRef,
		TaskInputDigest:   input.TaskInputRef,
		ParentRunID:       root.RunID,
		CancellationScope: corecontract.CancellationScopeInheritedV1,
		Deadline:          root.Deadline,
		RecoveryRootRef:   "recovery/" + planned.RunID,
		Composite: &corecontract.CompositeRunNodeV1{
			SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
			Role:                 corecontract.CompositeRunRoleChildV1,
			RepairRound:          repairRound,
			RootRunID:            root.RunID,
			ParentManifestDigest: root.ManifestDigest,
			ParentSlotID:         parentSlot,
			Assignment:           &planned.Assignment,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return child
}

func workspaceTransferTestMaterialV1(
	t *testing.T,
	root corecontract.RunManifest,
	child corecontract.RunManifest,
	plan corecontract.WorkspaceTransferPlanV1,
	direction corecontract.WorkspaceTransferDirectionV1,
	payloadCanonical []byte,
) WorkspaceTransferMaterialV1 {
	t.Helper()
	payloadKind := corecontract.WorkspaceTransferPayloadTaskSummaryV1
	payloadSchema := corecontract.WorkspaceTaskSummarySchemaVersionV1
	contentKind := "WORKSPACE_TRANSFER_PAYLOAD"
	sourceWorkspace := plan.RootWorkspace
	targetWorkspace := plan.TargetWorkspace
	sourceGrantID, sourceGrantDigest := plan.RootGrantID, plan.RootGrantDigest
	targetGrantID, targetGrantDigest := plan.TargetGrantID, plan.TargetGrantDigest
	if direction == corecontract.WorkspaceTransferDirectionResultV1 {
		payloadKind = corecontract.WorkspaceTransferPayloadSpecialistResultV1
		payloadSchema = corecontract.SpecialistContributionSchemaVersionV1
		contentKind = "MODEL_RESULT"
		sourceWorkspace, targetWorkspace = targetWorkspace, sourceWorkspace
		sourceGrantID, targetGrantID = targetGrantID, sourceGrantID
		sourceGrantDigest, targetGrantDigest = targetGrantDigest, sourceGrantDigest
	}
	payloadRef := contentDigest(contentKind, jsonMediaType, payloadCanonical)
	envelope, envelopeCanonical, envelopeDigest, err :=
		corecontract.NewWorkspaceTransferEnvelopeV1(
			corecontract.WorkspaceTransferEnvelopeV1{
				SchemaVersion:        corecontract.WorkspaceTransferEnvelopeSchemaVersionV1,
				TenantID:             root.TenantID,
				SourceGrantID:        sourceGrantID,
				TargetGrantID:        targetGrantID,
				Direction:            direction,
				PayloadKind:          payloadKind,
				PayloadSchemaVersion: payloadSchema,
				TaskInputRef:         root.TaskInputRef,
				SourceWorkspace:      sourceWorkspace,
				TargetWorkspace:      targetWorkspace,
				SourceGrantDigest:    sourceGrantDigest,
				TargetGrantDigest:    targetGrantDigest,
				RootRunID:            root.RunID,
				ChildRunID:           child.RunID,
				SlotID:               child.Composite.Assignment.SlotID,
				PayloadRef:           payloadRef,
				PayloadSizeBytes:     uint32(len(payloadCanonical)),
			},
		)
	if err != nil || envelope.PayloadRef != payloadRef {
		t.Fatal(err)
	}
	return WorkspaceTransferMaterialV1{
		EnvelopeRef:       contentDigest("WORKSPACE_TRANSFER_ENVELOPE", jsonMediaType, envelopeCanonical),
		EnvelopeDigest:    envelopeDigest,
		EnvelopeCanonical: envelopeCanonical,
		ResolvedPayload: corecontract.WorkspaceTransferResolvedPayloadV1{
			PayloadRef: payloadRef, ContentKind: contentKind,
			MediaType: jsonMediaType, CanonicalBytes: bytes.Clone(payloadCanonical),
		},
		RootManifest:  root,
		ChildManifest: child,
	}
}

func workspaceTransferTestContributionSetV1(
	t *testing.T,
	familyDigest string,
	results []CompositeChildResultV1,
) (corecontract.CollaborationContributionSetV1, string) {
	t.Helper()
	entries := make([]corecontract.CollaborationContributionEntryV1, len(results))
	for index, result := range results {
		output, err := moduleapi.RestoreModelGenerateOutputV1(result.ResultCanonical)
		if err != nil {
			t.Fatal(err)
		}
		_, _, contributionDigest, err :=
			corecontract.ParseSpecialistContributionV1([]byte(output.AssistantText))
		if err != nil {
			t.Fatal(err)
		}
		entries[index] = corecontract.CollaborationContributionEntryV1{
			SlotID: result.SlotID, RunID: result.RunID,
			ResultRef: result.ResultRef, ContributionDigest: contributionDigest,
		}
	}
	set, _, digest, err := corecontract.NewCollaborationContributionSetV1(
		corecontract.CollaborationContributionSetV1{
			SchemaVersion: corecontract.CollaborationContributionSetSchemaVersionV1,
			FamilyDigest:  familyDigest, Contributions: entries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return set, digest
}

func workspaceTransferTestRepairVerdictV1(
	t *testing.T,
	familyDigest string,
	setDigest string,
	plan corecontract.CompositeRunPlanV1,
) CompositeCollaborationReviewVerdictMaterialV1 {
	t.Helper()
	verdict, hostCanonical, err := corecontract.NewCollaborationReviewVerdictV1(
		corecontract.CollaborationReviewVerdictV1{
			SchemaVersion:         corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:          familyDigest,
			ContributionSetDigest: setDigest,
			Decision:              corecontract.CollaborationReviewDecisionRepairRequiredV1,
			IssueCodes: []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueMissingEvidenceV1,
			},
			AffectedSlotIDs: []string{plan.Children[0].SlotID},
			BoundedReason:   "The risk contribution needs repair.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelCanonical, err := corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
	if err != nil {
		t.Fatal(err)
	}
	_, resultCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(modelCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := plan.Reviewer
	return CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID: reviewer.RunID, ReviewerManifestDigest: testDigest("8"),
		MemberSnapshotDigest: reviewer.MemberSnapshotDigest,
		AttemptID:            "repair-verdict-attempt", LogicalStepID: reviewer.ReviewLogicalStepID,
		ResultRef:           contentDigest("MODEL_RESULT", jsonMediaType, resultCanonical),
		TerminalRunRevision: 31, TerminalFrameRevision: 32,
		ResultCanonical: resultCanonical, VerdictCanonical: hostCanonical,
	}
}
