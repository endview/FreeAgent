package assemblycompiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileCompositeFamilyIsDeterministicAndClosesParentChildren(t *testing.T) {
	input := validCompositeCompileInput(t)
	compiler := Compiler{}
	first, err := compiler.CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatalf("CompileCompositeFamily(first) error = %v", err)
	}
	second, err := compiler.CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatalf("CompileCompositeFamily(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Composite compilation is not deterministic")
	}
	if len(first.Children) != 2 {
		t.Fatalf("Children = %d, want 2", len(first.Children))
	}
	parent := first.Parent.RunManifest
	if parent.Composite == nil ||
		parent.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		parent.Composite.RootRunID != input.Parent.RunID ||
		parent.Composite.Plan == nil ||
		parent.Composite.Plan.MergeLogicalStepID !=
			corecontract.CompositeMergeLogicalStepIDV1 ||
		parent.Composite.Plan.FamilyModelDispatchLimit != 3 ||
		parent.CancellationScope != corecontract.CancellationScopeFamilyV1 ||
		parent.ParentRunID != "" {
		t.Fatalf("Parent manifest did not freeze the family plan: %+v", parent)
	}
	if got := []string{
		first.Children[0].Assignment.SlotID,
		first.Children[1].Assignment.SlotID,
	}; !reflect.DeepEqual(got, []string{"slot.backend", "slot.frontend"}) {
		t.Fatalf("Child output order = %v", got)
	}
	if got := []uint32{
		parent.Composite.Plan.Children[0].Assignment.WeightBasisPoints,
		parent.Composite.Plan.Children[1].Assignment.WeightBasisPoints,
	}; !reflect.DeepEqual(got, []uint32{7000, 3000}) {
		t.Fatalf("Parent plan weight order = %v", got)
	}

	parentIntent, err := corecontract.RestoreAdmissionIntentV1(
		input.Parent.IntentCanonical,
		input.Parent.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, child := range first.Children {
		intent, restoreErr := corecontract.RestoreAdmissionIntentV1(
			child.IntentCanonical,
			child.IntentDigest,
		)
		if restoreErr != nil {
			t.Fatalf("restore Child %d intent: %v", index, restoreErr)
		}
		manifest := child.RunManifest
		planRef := parent.Composite.Plan.Children[index]
		if intent.TenantID != parentIntent.TenantID ||
			intent.WorkspaceID != parentIntent.WorkspaceID ||
			intent.TaskInputRef != parentIntent.TaskInputRef ||
			!intent.Deadline.Equal(parentIntent.Deadline) ||
			intent.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
			len(intent.RequestedPorts) != 1 ||
			intent.RequestedPorts[0] != compositeModelGeneratePortV1 {
			t.Fatalf("Child %d intent did not inherit the family closure: %+v", index, intent)
		}
		if manifest.Composite == nil ||
			manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
			manifest.Composite.RootRunID != parent.RunID ||
			manifest.Composite.ParentManifestDigest != parent.ManifestDigest ||
			manifest.Composite.ParentSlotID != child.Assignment.SlotID ||
			manifest.Composite.Assignment == nil ||
			*manifest.Composite.Assignment != child.Assignment ||
			manifest.ParentRunID != parent.RunID ||
			manifest.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
			manifest.TaskInputRef != parent.TaskInputRef ||
			manifest.Deadline != parent.Deadline {
			t.Fatalf("Child %d manifest did not close to Parent: %+v", index, manifest)
		}
		if planRef.SlotID != child.Assignment.SlotID ||
			planRef.RunID != manifest.RunID ||
			planRef.AdmissionKey != intent.AdmissionKey ||
			planRef.MemberSnapshotDigest != child.MemberSnapshot.MemberSnapshotDigest ||
			planRef.Agent != child.MemberSnapshot.Agent ||
			planRef.Profile != child.MemberSnapshot.Profile ||
			planRef.TaskInputRef != intent.TaskInputRef ||
			planRef.Assignment != child.Assignment {
			t.Fatalf("Child %d plan edge mismatch: %+v", index, planRef)
		}
	}

	if got := compositeModelProviderInstance(t, first.Children[0].MemberSnapshot); got != "model-backend" {
		t.Fatalf("backend provider = %q", got)
	}
	if got := compositeModelProviderInstance(t, first.Children[1].MemberSnapshot); got != "model-frontend" {
		t.Fatalf("frontend provider = %q", got)
	}
}

func TestCompileCompositeFamilyFreezesOptionalReviewerWithoutChangingSpecialists(t *testing.T) {
	disabledInput := validCompositeCompileInput(t)
	disabled, err := (Compiler{}).CompileCompositeFamily(context.Background(), disabledInput)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Reviewer != nil || disabled.Decision != nil ||
		disabled.Parent.RunManifest.Composite.Plan.Reviewer != nil ||
		disabled.Parent.RunManifest.Composite.Plan.Decision != nil ||
		disabled.Parent.RunManifest.Composite.Plan.FamilyModelDispatchLimit != 3 ||
		strings.Contains(string(disabled.Parent.RunManifestCanonical), `"reviewer"`) ||
		strings.Contains(string(disabled.Parent.RunManifestCanonical), `"decision"`) {
		t.Fatalf("Reviewer-disabled family changed: %+v", disabled.Parent.RunManifest.Composite.Plan)
	}

	enabledInput := validCompositeCompileInput(t)
	enabledInput.Parent = rewriteCompositeControl(t, enabledInput.Parent, func(control *controlcontract.ControlSnapshot) {
		control.CompositeAgents[0].Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
			SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
			AgentID:         "agent.backend",
			ProfileID:       "profile.backend",
			MaxOutputTokens: 512,
			Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
		}
	})
	enabled, err := (Compiler{}).CompileCompositeFamily(context.Background(), enabledInput)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Reviewer == nil {
		t.Fatal("enabled Reviewer was not compiled")
	}
	plan := enabled.Parent.RunManifest.Composite.Plan
	if plan.Reviewer == nil || plan.FamilyModelDispatchLimit != 4 ||
		plan.Reviewer.RunID != enabled.Reviewer.RunManifest.RunID ||
		plan.Reviewer.AdmissionKey != enabled.Reviewer.RunManifest.AdmissionKey ||
		plan.Reviewer.MemberSnapshotDigest != enabled.Reviewer.MemberSnapshot.MemberSnapshotDigest ||
		plan.Reviewer.ReviewLogicalStepID != corecontract.CompositeReviewLogicalStepIDV1 ||
		plan.Reviewer.Policy != corecontract.CompositeReviewerPolicyResultsGateV1 ||
		plan.Reviewer.MaxOutputTokens != 512 {
		t.Fatalf("root Reviewer ref did not close: %+v", plan.Reviewer)
	}
	reviewer := enabled.Reviewer.RunManifest
	if reviewer.Composite == nil ||
		reviewer.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		reviewer.Composite.RootRunID != enabled.Parent.RunManifest.RunID ||
		reviewer.Composite.ParentManifestDigest != enabled.Parent.RunManifest.ManifestDigest ||
		reviewer.Composite.ParentSlotID != corecontract.CompositeReviewerParentSlotIDV1 ||
		reviewer.Composite.Assignment != nil || reviewer.Composite.Plan != nil ||
		reviewer.ParentRunID != enabled.Parent.RunManifest.RunID ||
		reviewer.CancellationScope != corecontract.CancellationScopeInheritedV1 {
		t.Fatalf("Reviewer Manifest did not close: %+v", reviewer)
	}
	reviewerIntent, err := corecontract.RestoreAdmissionIntentV1(
		enabled.Reviewer.IntentCanonical,
		enabled.Reviewer.IntentDigest,
	)
	if err != nil || reviewerIntent.AgentID != "agent.backend" ||
		reviewerIntent.ProfileID != "profile.backend" ||
		reviewerIntent.CancellationScope != corecontract.CancellationScopeInheritedV1 {
		t.Fatalf("Reviewer intent did not close: %+v error=%v", reviewerIntent, err)
	}
	for index := range disabled.Children {
		if disabled.Children[index].Assignment != enabled.Children[index].Assignment ||
			disabled.Children[index].RunManifest.RunID != enabled.Children[index].RunManifest.RunID ||
			disabled.Children[index].MemberSnapshot.MemberID != enabled.Children[index].MemberSnapshot.MemberID ||
			disabled.Children[index].IntentDigest != enabled.Children[index].IntentDigest ||
			compositeModelProviderInstance(t, disabled.Children[index].MemberSnapshot) !=
				compositeModelProviderInstance(t, enabled.Children[index].MemberSnapshot) {
			t.Fatalf("enabling Reviewer changed Specialist identity/routing at %d", index)
		}
	}
}

func TestCompileCompositeFamilyDecisionPreFreezesCompleteRepairFamily(t *testing.T) {
	input := validCompositeCompileInput(t)
	input.Parent = rewriteCompositeControl(t, input.Parent, func(control *controlcontract.ControlSnapshot) {
		definition := &control.CompositeAgents[0]
		definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
			SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
			AgentID:         "agent.backend",
			ProfileID:       "profile.backend",
			MaxOutputTokens: 512,
			Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
		}
		definition.Decision = &controlcontract.CompositeDecisionDefinitionV1{
			SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
		}
	})

	first, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("decision family compilation is not deterministic")
	}
	plan := first.Parent.RunManifest.Composite.Plan
	if first.Reviewer == nil || first.Decision == nil || plan.Decision == nil ||
		plan.Reviewer == nil || plan.FamilyModelDispatchLimit != 7 ||
		len(first.Decision.RepairChildren) != len(first.Children) ||
		len(plan.Decision.RepairChildren) != len(plan.Children) {
		t.Fatalf("decision family was not completely pre-frozen: %+v", plan)
	}
	if plan.Decision.SchemaVersion != corecontract.CompositeDecisionPlanSchemaVersionV1 {
		t.Fatalf("decision schema = %q", plan.Decision.SchemaVersion)
	}

	physicalSlots := map[string]struct{}{
		corecontract.CompositeReviewerParentSlotIDV1: {},
	}
	for _, initial := range plan.Children {
		if initial.ParentSlotID != "" {
			t.Fatalf("initial Child ref exposed a physical override: %+v", initial)
		}
		physicalSlots[initial.SlotID] = struct{}{}
	}
	if plan.Reviewer.ParentSlotID != "" {
		t.Fatalf("initial Reviewer ref exposed a physical override: %+v", plan.Reviewer)
	}

	for index, repair := range first.Decision.RepairChildren {
		initial := first.Children[index]
		initialRef := plan.Children[index]
		repairRef := plan.Decision.RepairChildren[index]
		node := repair.RunManifest.Composite
		if repair.Assignment != initial.Assignment ||
			repairRef.SlotID != initialRef.SlotID ||
			repairRef.Assignment != initialRef.Assignment ||
			repairRef.Agent != initialRef.Agent ||
			repairRef.Profile != initialRef.Profile ||
			repairRef.TaskInputRef != initialRef.TaskInputRef ||
			repairRef.RunID != repair.RunManifest.RunID ||
			repairRef.AdmissionKey != repair.RunManifest.AdmissionKey ||
			repairRef.MemberSnapshotDigest != repair.MemberSnapshot.MemberSnapshotDigest {
			t.Fatalf("repair Child %d changed its logical Specialist edge: %+v", index, repairRef)
		}
		if node == nil || node.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			node.ParentSlotID != repairRef.ParentSlotID ||
			node.ParentSlotID == repair.Assignment.SlotID ||
			node.Assignment == nil || *node.Assignment != repair.Assignment {
			t.Fatalf("repair Child %d physical edge did not close: %+v", index, node)
		}
		if repair.RunManifest.RunID == initial.RunManifest.RunID ||
			repair.RunManifest.AdmissionKey == initial.RunManifest.AdmissionKey ||
			repair.MemberSnapshot.MemberSnapshotDigest == initial.MemberSnapshot.MemberSnapshotDigest {
			t.Fatalf("repair Child %d reused an initial identity", index)
		}
		if !reflect.DeepEqual(
			repair.MemberSnapshot.PortPlans,
			initial.MemberSnapshot.PortPlans,
		) {
			t.Fatalf("repair Child %d changed Provider routing", index)
		}
		if _, duplicate := physicalSlots[repairRef.ParentSlotID]; duplicate {
			t.Fatalf("repair Child %d duplicated physical slot %q", index, repairRef.ParentSlotID)
		}
		physicalSlots[repairRef.ParentSlotID] = struct{}{}
	}

	repairReviewer := first.Decision.RepairReviewer
	repairReviewerRef := plan.Decision.RepairReviewer
	repairReviewerNode := repairReviewer.RunManifest.Composite
	if repairReviewerNode == nil ||
		repairReviewerNode.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		repairReviewerNode.ParentSlotID != repairReviewerRef.ParentSlotID ||
		repairReviewerRef.ParentSlotID == corecontract.CompositeReviewerParentSlotIDV1 ||
		repairReviewerRef.RunID != repairReviewer.RunManifest.RunID ||
		repairReviewerRef.AdmissionKey != repairReviewer.RunManifest.AdmissionKey ||
		repairReviewerRef.Agent != plan.Reviewer.Agent ||
		repairReviewerRef.Profile != plan.Reviewer.Profile ||
		repairReviewerRef.Policy != plan.Reviewer.Policy ||
		repairReviewerRef.MaxOutputTokens != plan.Reviewer.MaxOutputTokens {
		t.Fatalf("repair Reviewer did not preserve the frozen review edge: %+v", repairReviewerRef)
	}
	if repairReviewer.RunManifest.RunID == first.Reviewer.RunManifest.RunID ||
		repairReviewer.MemberSnapshot.MemberSnapshotDigest == first.Reviewer.MemberSnapshot.MemberSnapshotDigest ||
		!reflect.DeepEqual(
			repairReviewer.MemberSnapshot.PortPlans,
			first.Reviewer.MemberSnapshot.PortPlans,
		) {
		t.Fatal("repair Reviewer identity or Provider routing did not close")
	}
	if _, duplicate := physicalSlots[repairReviewerRef.ParentSlotID]; duplicate {
		t.Fatalf("repair Reviewer duplicated physical slot %q", repairReviewerRef.ParentSlotID)
	}
}

func TestCompileCompositeFamilyFreezesCrossWorkspaceInitialAndRepairChildren(
	t *testing.T,
) {
	legacyInput := validCompositeReviewerInput(t, true)
	legacy, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		legacyInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	crossInput := compositeInputWithWorkspaceTransfer(
		t,
		validCompositeReviewerInput(t, true),
		"slot.backend",
		nil,
	)
	cross, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		crossInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	initialBySlot := compositeChildrenBySlot(cross.Children)
	repairBySlot := compositeChildrenBySlot(cross.Decision.RepairChildren)
	legacyBySlot := compositeChildrenBySlot(legacy.Children)
	backend := initialBySlot["slot.backend"]
	backendRepair := repairBySlot["slot.backend"]
	frontend := initialBySlot["slot.frontend"]
	frontendRepair := repairBySlot["slot.frontend"]
	if backend.MemberSnapshot.Workspace.ID != "workspace.specialist" ||
		backendRepair.MemberSnapshot.Workspace != backend.MemberSnapshot.Workspace ||
		frontend.MemberSnapshot.Workspace.ID != "workspace.main" ||
		frontendRepair.MemberSnapshot.Workspace != frontend.MemberSnapshot.Workspace {
		t.Fatalf(
			"cross/same Workspace routing backend=%+v/%+v frontend=%+v/%+v",
			backend.MemberSnapshot.Workspace,
			backendRepair.MemberSnapshot.Workspace,
			frontend.MemberSnapshot.Workspace,
			frontendRepair.MemberSnapshot.Workspace,
		)
	}
	backendIntent, err := corecontract.RestoreAdmissionIntentV1(
		backend.IntentCanonical,
		backend.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	backendRepairIntent, err := corecontract.RestoreAdmissionIntentV1(
		backendRepair.IntentCanonical,
		backendRepair.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if backendIntent.WorkspaceID != "workspace.specialist" ||
		backendRepairIntent.WorkspaceID != "workspace.specialist" {
		t.Fatalf("cross-Workspace intents=%+v / %+v", backendIntent, backendRepairIntent)
	}
	if cross.Reviewer.MemberSnapshot.Workspace.ID != "workspace.main" ||
		cross.Decision.RepairReviewer.MemberSnapshot.Workspace.ID != "workspace.main" {
		t.Fatal("Reviewer left the root Workspace")
	}

	plan := cross.Parent.RunManifest.Composite.Plan
	initialRef := compositePlanChildBySlot(t, plan.Children, "slot.backend")
	repairRef := compositePlanChildBySlot(
		t,
		plan.Decision.RepairChildren,
		"slot.backend",
	)
	frontendRef := compositePlanChildBySlot(t, plan.Children, "slot.frontend")
	frontendRepairRef := compositePlanChildBySlot(
		t,
		plan.Decision.RepairChildren,
		"slot.frontend",
	)
	if initialRef.Transfer == nil || repairRef.Transfer == nil ||
		*initialRef.Transfer != *repairRef.Transfer ||
		initialRef.Transfer.RootWorkspace != cross.Parent.RunManifest.Workspace ||
		initialRef.Transfer.TargetWorkspace != backend.RunManifest.Workspace {
		t.Fatalf("initial/repair transfer plans=%+v / %+v", initialRef.Transfer, repairRef.Transfer)
	}
	if frontendRef.Transfer != nil || frontendRepairRef.Transfer != nil {
		t.Fatal("same-Workspace Specialist received a transfer plan")
	}
	if backend.RunManifest.RunID == legacyBySlot["slot.backend"].RunManifest.RunID ||
		frontend.RunManifest.RunID != legacyBySlot["slot.frontend"].RunManifest.RunID ||
		cross.Reviewer.RunManifest.RunID != legacy.Reviewer.RunManifest.RunID {
		t.Fatal("Workspace transfer did not isolate only the targeted Specialist identities")
	}
	if strings.Contains(string(frontend.IntentCanonical), `"transfer"`) ||
		strings.Contains(string(cross.Reviewer.IntentCanonical), `"transfer"`) {
		t.Fatal("transfer authority leaked into same-Workspace or Reviewer intent")
	}
}

func TestCompileCompositeFamilyCrossWorkspaceRequiresDecisionAndFourDirections(
	t *testing.T,
) {
	withoutDecision := compositeInputWithWorkspaceTransfer(
		t,
		validCompositeCompileInput(t),
		"slot.backend",
		nil,
	)
	if _, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		withoutDecision,
	); err == nil {
		t.Fatal("cross-Workspace legacy Composite was accepted without Decision")
	}

	for _, test := range []struct {
		name   string
		mutate func(*corecontract.WorkspaceTransferGrantV1, *corecontract.WorkspaceTransferGrantV1)
	}{
		{
			name: "root send task",
			mutate: func(root, _ *corecontract.WorkspaceTransferGrantV1) {
				root.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
					corecontract.WorkspaceTransferPayloadSpecialistResultV1,
				}
			},
		},
		{
			name: "target receive task",
			mutate: func(_, target *corecontract.WorkspaceTransferGrantV1) {
				target.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
					corecontract.WorkspaceTransferPayloadSpecialistResultV1,
				}
			},
		},
		{
			name: "target send result",
			mutate: func(_, target *corecontract.WorkspaceTransferGrantV1) {
				target.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
					corecontract.WorkspaceTransferPayloadTaskSummaryV1,
				}
			},
		},
		{
			name: "root receive result",
			mutate: func(root, _ *corecontract.WorkspaceTransferGrantV1) {
				root.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
					corecontract.WorkspaceTransferPayloadTaskSummaryV1,
				}
			},
		},
		{
			name: "disabled target",
			mutate: func(_, target *corecontract.WorkspaceTransferGrantV1) {
				target.Enabled = false
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := compositeInputWithWorkspaceTransfer(
				t,
				validCompositeReviewerInput(t, true),
				"slot.backend",
				test.mutate,
			)
			if _, err := (Compiler{}).CompileCompositeFamily(
				context.Background(),
				input,
			); err == nil {
				t.Fatal("incomplete cross-Workspace authorization was accepted")
			}
		})
	}

	for _, test := range []struct {
		name       string
		removeRoot bool
		removePeer bool
	}{
		{name: "missing both grants", removeRoot: true, removePeer: true},
		{name: "root-only grant", removePeer: true},
		{name: "target-only grant", removeRoot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := compositeInputWithWorkspaceTransfer(
				t,
				validCompositeReviewerInput(t, true),
				"slot.backend",
				nil,
			)
			input.Parent = rewriteCompositeControl(
				t,
				input.Parent,
				func(control *controlcontract.ControlSnapshot) {
					foundRoot := false
					foundPeer := false
					for index := range control.Workspaces {
						switch control.Workspaces[index].Workspace.ID {
						case "workspace.main":
							foundRoot = true
							if test.removeRoot {
								control.Workspaces[index].TransferGrants = nil
							}
						case "workspace.specialist":
							foundPeer = true
							if test.removePeer {
								control.Workspaces[index].TransferGrants = nil
							}
						}
					}
					if !foundRoot || !foundPeer {
						t.Fatal("cross-Workspace grant owners are absent")
					}
				},
			)
			if _, err := (Compiler{}).CompileCompositeFamily(
				context.Background(),
				input,
			); err == nil {
				t.Fatal("cross-Workspace Composite accepted missing bilateral grant")
			}
		})
	}
}

func TestCompileCompositeFamilyDecisionDoesNotRewriteInitialContracts(t *testing.T) {
	reviewerOnly, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		validCompositeReviewerInput(t, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	withDecision, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		validCompositeReviewerInput(t, true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if reviewerOnly.Decision != nil || withDecision.Decision == nil ||
		reviewerOnly.Reviewer == nil || withDecision.Reviewer == nil {
		t.Fatal("Reviewer/Decision fixture did not compile as requested")
	}

	beforeBySlot := compositeChildrenBySlot(reviewerOnly.Children)
	afterBySlot := compositeChildrenBySlot(withDecision.Children)
	if len(beforeBySlot) != len(afterBySlot) {
		t.Fatalf("initial Specialist count changed: %d -> %d", len(beforeBySlot), len(afterBySlot))
	}
	for slot, before := range beforeBySlot {
		after, found := afterBySlot[slot]
		if !found {
			t.Fatalf("initial Specialist slot %q disappeared", slot)
		}
		if before.Assignment != after.Assignment ||
			before.RunManifest.RunID != after.RunManifest.RunID ||
			before.RunManifest.AdmissionKey != after.RunManifest.AdmissionKey ||
			before.RunManifest.RecoveryRootRef != after.RunManifest.RecoveryRootRef ||
			before.IntentDigest != after.IntentDigest ||
			!reflect.DeepEqual(before.IntentCanonical, after.IntentCanonical) {
			t.Fatalf("Decision opt-in rewrote initial Specialist %q identity or intent", slot)
		}
		assertCompositeMemberContractEqual(t, before.MemberSnapshot, after.MemberSnapshot)
	}

	beforeReviewer := reviewerOnly.Reviewer
	afterReviewer := withDecision.Reviewer
	if beforeReviewer.RunManifest.RunID != afterReviewer.RunManifest.RunID ||
		beforeReviewer.RunManifest.AdmissionKey != afterReviewer.RunManifest.AdmissionKey ||
		beforeReviewer.RunManifest.RecoveryRootRef != afterReviewer.RunManifest.RecoveryRootRef ||
		beforeReviewer.IntentDigest != afterReviewer.IntentDigest ||
		!reflect.DeepEqual(beforeReviewer.IntentCanonical, afterReviewer.IntentCanonical) {
		t.Fatal("Decision opt-in rewrote initial Reviewer identity or intent")
	}
	assertCompositeMemberContractEqual(
		t,
		beforeReviewer.MemberSnapshot,
		afterReviewer.MemberSnapshot,
	)
	beforeRef := reviewerOnly.Parent.RunManifest.Composite.Plan.Reviewer
	afterRef := withDecision.Parent.RunManifest.Composite.Plan.Reviewer
	if beforeRef.Policy != afterRef.Policy ||
		beforeRef.MaxOutputTokens != afterRef.MaxOutputTokens ||
		beforeRef.ReviewLogicalStepID != afterRef.ReviewLogicalStepID {
		t.Fatal("Decision opt-in rewrote the initial Reviewer policy")
	}
}

func TestCompileCompositeFamilyDecisionSupportsEightSpecialists(t *testing.T) {
	input := validEightSpecialistDecisionInput(t)
	first, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("eight-Specialist compilation is not deterministic")
	}
	plan := first.Parent.RunManifest.Composite.Plan
	if first.Decision == nil || first.Reviewer == nil || plan.Decision == nil ||
		len(first.Children) != corecontract.CompositeMaxChildrenV1 ||
		len(first.Decision.RepairChildren) != corecontract.CompositeMaxChildrenV1 ||
		len(plan.Children) != corecontract.CompositeMaxChildrenV1 ||
		len(plan.Decision.RepairChildren) != corecontract.CompositeMaxChildrenV1 ||
		plan.FamilyModelDispatchLimit != 19 {
		t.Fatalf("eight-Specialist repair family did not freeze at cap 19: %+v", plan)
	}
	expectedSlots := []string{
		"slot.00", "slot.01", "slot.02", "slot.03",
		"slot.04", "slot.05", "slot.06", "slot.07",
	}
	seenRuns := make(map[string]struct{}, 18)
	for index, slot := range expectedSlots {
		initial := first.Children[index]
		repair := first.Decision.RepairChildren[index]
		if initial.Assignment.SlotID != slot || repair.Assignment.SlotID != slot ||
			plan.Children[index].SlotID != slot ||
			plan.Decision.RepairChildren[index].SlotID != slot {
			t.Fatalf("Specialist order[%d] did not remain stable: initial=%q repair=%q", index, initial.Assignment.SlotID, repair.Assignment.SlotID)
		}
		for _, runID := range []string{initial.RunManifest.RunID, repair.RunManifest.RunID} {
			if _, duplicate := seenRuns[runID]; duplicate {
				t.Fatalf("duplicate Specialist RunID %q", runID)
			}
			seenRuns[runID] = struct{}{}
		}
	}
	for _, runID := range []string{
		first.Reviewer.RunManifest.RunID,
		first.Decision.RepairReviewer.RunManifest.RunID,
	} {
		if _, duplicate := seenRuns[runID]; duplicate {
			t.Fatalf("duplicate Reviewer RunID %q", runID)
		}
		seenRuns[runID] = struct{}{}
	}
}

func TestCompileCompositeFamilyReturnsIndependentDecisionCopies(t *testing.T) {
	input := validCompositeReviewerInput(t, true)
	expected, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	mutable, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	mutable.Parent.RunManifestCanonical[0] ^= 0xff
	mutable.Parent.RunManifest.Composite.Plan.Decision.RepairChildren[0].ParentSlotID = "mutated"
	mutable.Decision.RepairChildren[0].IntentCanonical[0] ^= 0xff
	mutable.Decision.RepairChildren[0].MemberSnapshotCanonical[0] ^= 0xff
	mutable.Decision.RepairChildren[0].RunManifestCanonical[0] ^= 0xff
	mutable.Decision.RepairChildren[0].MemberSnapshot.PortPlans[0].Bindings[0].Provider.InstanceID = "mutated"
	mutable.Decision.RepairChildren = append(
		mutable.Decision.RepairChildren,
		CompositeChildCompileOutput{},
	)

	afterMutation, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, afterMutation) {
		t.Fatal("mutating one compile result leaked into a later Decision compilation")
	}
}

func TestCompileCompositeFamilyLegacyRootWireOmitsDecisionFields(t *testing.T) {
	inputs := map[string]CompositeCompileInput{
		"specialists only": validCompositeCompileInput(t),
		"with Reviewer":    validCompositeReviewerInput(t, false),
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			output, err := (Compiler{}).CompileCompositeFamily(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			wire := string(output.Parent.RunManifestCanonical)
			for _, field := range []string{
				`"decision"`, `"parent_slot_id"`, `"repair_round"`, `"transfer"`,
			} {
				if strings.Contains(wire, field) {
					t.Fatalf("legacy root canonical wire unexpectedly contains %s: %s", field, wire)
				}
			}
			plan := output.Parent.RunManifest.Composite.Plan
			for _, child := range plan.Children {
				if child.ParentSlotID != "" {
					t.Fatalf("legacy Child ref has physical slot override: %+v", child)
				}
			}
			if plan.Reviewer != nil && plan.Reviewer.ParentSlotID != "" {
				t.Fatalf("legacy Reviewer ref has physical slot override: %+v", plan.Reviewer)
			}
		})
	}
}

func TestCompositeRepairIdentityDomainsAreDeterministicAndSeparate(t *testing.T) {
	parentDigest := strings.Repeat("a", 64)
	childFirst, childSlotFirst, err := deriveCompositeRepairChildIDsV1(
		parentDigest,
		"slot.backend",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	childSecond, childSlotSecond, err := deriveCompositeRepairChildIDsV1(
		parentDigest,
		"slot.backend",
		nil,
	)
	if err != nil || childFirst != childSecond || childSlotFirst != childSlotSecond {
		t.Fatalf("repair Child identity is not deterministic: %v", err)
	}
	reviewer, reviewerSlot, err := deriveCompositeRepairReviewerIDsV1(parentDigest)
	if err != nil {
		t.Fatal(err)
	}
	initialChild, err := deriveCompositeChildIDsV1(parentDigest, "slot.backend", nil)
	if err != nil {
		t.Fatal(err)
	}
	initialReviewer, err := deriveCompositeReviewerIDsV1(parentDigest)
	if err != nil {
		t.Fatal(err)
	}
	if childFirst == reviewer || childFirst == initialChild || childFirst == initialReviewer ||
		reviewer == initialChild || reviewer == initialReviewer ||
		childSlotFirst == reviewerSlot || childSlotFirst == "slot.backend" ||
		reviewerSlot == corecontract.CompositeReviewerParentSlotIDV1 {
		t.Fatalf("repair identity domains collided: child=%+v reviewer=%+v", childFirst, reviewer)
	}
}

func TestCompositeReviewerIdentityDerivationIsDeterministicAndSeparate(t *testing.T) {
	first, err := deriveCompositeReviewerIDsV1(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveCompositeReviewerIDsV1(strings.Repeat("a", 64))
	if err != nil || first != second {
		t.Fatalf("Reviewer identity is not deterministic: %+v %+v %v", first, second, err)
	}
	child, err := deriveCompositeChildIDsV1(
		strings.Repeat("a", 64),
		corecontract.CompositeReviewerParentSlotIDV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == child || first.RunID == child.RunID || first.AdmissionKey == child.AdmissionKey {
		t.Fatalf("Reviewer identity collided with Child domains: %+v %+v", first, child)
	}
}

func TestCompileCompositeFamilyWeightsDoNotRouteProvidersOrChangeChildIDs(t *testing.T) {
	baseInput := validCompositeCompileInput(t)
	base, err := (Compiler{}).CompileCompositeFamily(context.Background(), baseInput)
	if err != nil {
		t.Fatal(err)
	}
	changedParent := rewriteCompositeControl(t, baseInput.Parent, func(control *controlcontract.ControlSnapshot) {
		for definitionIndex := range control.CompositeAgents {
			definition := &control.CompositeAgents[definitionIndex]
			if definition.AgentID != "agent.chat" {
				continue
			}
			for memberIndex := range definition.Members {
				member := &definition.Members[memberIndex]
				switch member.SlotID {
				case "slot.backend":
					member.WeightBasisPoints = 3000
				case "slot.frontend":
					member.WeightBasisPoints = 7000
				}
			}
		}
	})
	changed, err := (Compiler{}).CompileCompositeFamily(
		context.Background(),
		CompositeCompileInput{Parent: changedParent},
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Children[0].Assignment.SlotID != "slot.frontend" ||
		changed.Children[1].Assignment.SlotID != "slot.backend" {
		t.Fatalf("changed Child order = %q/%q", changed.Children[0].Assignment.SlotID, changed.Children[1].Assignment.SlotID)
	}

	baseBySlot := compositeChildrenBySlot(base.Children)
	changedBySlot := compositeChildrenBySlot(changed.Children)
	for slot, before := range baseBySlot {
		after := changedBySlot[slot]
		beforeIntent, restoreErr := corecontract.RestoreAdmissionIntentV1(
			before.IntentCanonical,
			before.IntentDigest,
		)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		afterIntent, restoreErr := corecontract.RestoreAdmissionIntentV1(
			after.IntentCanonical,
			after.IntentDigest,
		)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if before.RunManifest.RunID != after.RunManifest.RunID ||
			before.MemberSnapshot.MemberID != after.MemberSnapshot.MemberID ||
			before.RunManifest.RecoveryRootRef != after.RunManifest.RecoveryRootRef ||
			beforeIntent.AdmissionKey != afterIntent.AdmissionKey ||
			before.IntentDigest != after.IntentDigest {
			t.Fatalf("derived Child identity for %q changed with weight", slot)
		}
		beforeProvider := compositeModelProviderInstance(t, before.MemberSnapshot)
		afterProvider := compositeModelProviderInstance(t, after.MemberSnapshot)
		if beforeProvider != afterProvider {
			t.Fatalf("provider for %q changed with weight: %q -> %q", slot, beforeProvider, afterProvider)
		}
	}
}

func TestCompositeChildIdentityDerivationGolden(t *testing.T) {
	got, err := deriveCompositeChildIDsV1(
		strings.Repeat("a", 64),
		"slot.backend",
		nil,
	)
	if err != nil {
		t.Fatalf("deriveCompositeChildIDsV1: %v", err)
	}
	want := compositeChildIDsV1{
		AdmissionKey: "composite-child-admission-" +
			"909d2d4a5e6c7b034cf20c7d5bac5a0c3efe324796ac2c79fc2c02301ad6454b",
		RunID: "composite-child-run-" +
			"6ec421d4358673cc9bda878dbd604d03bae70fc821b11b819677b2073cc262a6",
		MemberID: "composite-child-member-" +
			"98687f0e6a735818fab8a477aa6bfe4f62ea74d17ee621590ecf3a3414c69e24",
		RecoveryRootRef: "composite-child-recovery-" +
			"b7f12f8be22a8580c6a6de33dea816b5d0bab548b134ed211ad1f2e16fdfb356",
	}
	if got != want {
		t.Fatalf("derived Composite Child identities=%+v", got)
	}
}

func TestCompileCompositeFamilyRejectsScopeDefinitionProfileAndWeightErrors(t *testing.T) {
	t.Run("Parent cancellation scope", func(t *testing.T) {
		input := validCompositeCompileInput(t)
		intent, err := corecontract.RestoreAdmissionIntentV1(
			input.Parent.IntentCanonical,
			input.Parent.IntentDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		intent.CancellationScope = corecontract.CancellationScopeRunV1
		_, input.Parent.IntentCanonical, input.Parent.IntentDigest, err =
			corecontract.NewAdmissionIntentV1(intent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (Compiler{}).CompileCompositeFamily(context.Background(), input); err == nil {
			t.Fatal("run-scoped composite Parent was accepted")
		}
	})

	t.Run("missing definition", func(t *testing.T) {
		parent := rewriteCompositeControl(
			t,
			validCompositeCompileInput(t).Parent,
			func(control *controlcontract.ControlSnapshot) {
				control.CompositeAgents = nil
			},
		)
		if _, err := (Compiler{}).CompileCompositeFamily(
			context.Background(),
			CompositeCompileInput{Parent: parent},
		); err == nil {
			t.Fatal("Parent without composite definition was accepted")
		}
	})

	t.Run("coordinator Profile mismatch", func(t *testing.T) {
		parent := rewriteCompositeControl(
			t,
			validCompositeCompileInput(t).Parent,
			func(control *controlcontract.ControlSnapshot) {
				control.CompositeAgents[0].CoordinatorProfileID = "profile.backend"
			},
		)
		if _, err := (Compiler{}).CompileCompositeFamily(
			context.Background(),
			CompositeCompileInput{Parent: parent},
		); err == nil {
			t.Fatal("mismatched coordinator Profile was accepted")
		}
	})

	t.Run("Child Action Profile", func(t *testing.T) {
		parent := rewriteCompositeControl(
			t,
			validCompositeCompileInput(t).Parent,
			func(control *controlcontract.ControlSnapshot) {
				for index := range control.Profiles {
					if control.Profiles[index].Profile.ID == "profile.backend" {
						control.Profiles[index].Bindings = append(
							control.Profiles[index].Bindings,
							binding(
								moduleapi.PortNameActionProvider,
								"action-child",
								"1",
								"2",
								moduleapi.FailureRequired,
							),
						)
					}
				}
			},
		)
		if _, err := (Compiler{}).CompileCompositeFamily(
			context.Background(),
			CompositeCompileInput{Parent: parent},
		); err == nil {
			t.Fatal("Action-capable Child Profile was accepted")
		}
	})

	t.Run("invalid frozen weight", func(t *testing.T) {
		input := corruptCompositeWeight(t, validCompositeCompileInput(t))
		if _, err := (Compiler{}).CompileCompositeFamily(context.Background(), input); err == nil {
			t.Fatal("invalid frozen composite weight was accepted")
		}
	})
}

func TestCompositeChildSideEffectPortGateRejectsActionAndChannel(t *testing.T) {
	for _, port := range []string{
		moduleapi.PortNameActionProvider,
		moduleapi.PortNameChannelTransport,
	} {
		t.Run(port, func(t *testing.T) {
			profile := controlcontract.ProfileDefinition{
				Profile: corecontract.ProfileRef{
					ID: "profile.child", Version: "1", Digest: hash("1"),
				},
				Bindings: []controlcontract.BindingSpec{{
					Port: currentPort(port),
				}},
			}
			if err := rejectCompositeChildSideEffectPorts("slot.child", profile); err == nil {
				t.Fatalf("%s Child binding was accepted", port)
			}
		})
	}
}

func TestCompositeCompileInputExposesOnlyParentIdentity(t *testing.T) {
	kind := reflect.TypeOf(CompositeCompileInput{})
	if kind.NumField() != 1 || kind.Field(0).Name != "Parent" ||
		kind.Field(0).Type != reflect.TypeOf(CompileInput{}) {
		t.Fatalf("CompositeCompileInput fields = %+v", kind)
	}
}

func validCompositeCompileInput(t *testing.T) CompositeCompileInput {
	t.Helper()
	parent := validCurrentCompileInput(t)
	parent = rewriteCompositeControl(t, parent, func(control *controlcontract.ControlSnapshot) {
		control.Agents = append(
			control.Agents,
			corecontract.AgentRef{ID: "agent.backend", Version: "1", Digest: hash("b")},
			corecontract.AgentRef{ID: "agent.frontend", Version: "1", Digest: hash("c")},
		)
		baseProfile := control.Profiles[0]
		backend := baseProfile
		backend.Profile = corecontract.ProfileRef{
			ID: "profile.backend", Version: "1", Digest: hash("d"),
		}
		backend.Bindings = []controlcontract.BindingSpec{binding(
			moduleapi.PortNameModelGenerate,
			"model-backend",
			"3",
			"4",
			moduleapi.FailureRequired,
		)}
		frontend := baseProfile
		frontend.Profile = corecontract.ProfileRef{
			ID: "profile.frontend", Version: "1", Digest: hash("e"),
		}
		frontend.Bindings = []controlcontract.BindingSpec{binding(
			moduleapi.PortNameModelGenerate,
			"model-frontend",
			"5",
			"6",
			moduleapi.FailureRequired,
		)}
		control.Profiles = append(control.Profiles, backend, frontend)
		control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{{
			SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
			AgentID:              "agent.chat",
			CoordinatorProfileID: "profile.chat",
			Members: []controlcontract.CompositeAgentMemberV1{
				{
					SlotID:            "slot.frontend",
					AgentID:           "agent.frontend",
					ProfileID:         "profile.frontend",
					FocusID:           "frontend",
					WeightBasisPoints: 3000,
				},
				{
					SlotID:            "slot.backend",
					AgentID:           "agent.backend",
					ProfileID:         "profile.backend",
					FocusID:           "backend",
					WeightBasisPoints: 7000,
				},
			},
		}}
	})

	catalog, err := controlcontract.RestoreCatalogGeneration(
		parent.CatalogCanonical,
		parent.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Entries = append(
		catalog.Entries,
		catalogEntry("model-backend", "model.backend", moduleapi.PortNameModelGenerate),
		catalogEntry("model-frontend", "model.frontend", moduleapi.PortNameModelGenerate),
	)
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	parent.CatalogCanonical = catalogCanonical
	parent.PublishedBasis.Catalog = catalogRef

	intent, err := corecontract.RestoreAdmissionIntentV1(
		parent.IntentCanonical,
		parent.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.CancellationScope = corecontract.CancellationScopeFamilyV1
	_, parent.IntentCanonical, parent.IntentDigest, err =
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	return CompositeCompileInput{Parent: parent}
}

func validCompositeReviewerInput(
	t *testing.T,
	decision bool,
) CompositeCompileInput {
	t.Helper()
	input := validCompositeCompileInput(t)
	input.Parent = rewriteCompositeControl(
		t,
		input.Parent,
		func(control *controlcontract.ControlSnapshot) {
			definition := &control.CompositeAgents[0]
			definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
				SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
				AgentID:         "agent.backend",
				ProfileID:       "profile.backend",
				MaxOutputTokens: 512,
				Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
			}
			if decision {
				definition.Decision = &controlcontract.CompositeDecisionDefinitionV1{
					SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
				}
			}
		},
	)
	return input
}

func validEightSpecialistDecisionInput(t *testing.T) CompositeCompileInput {
	t.Helper()
	input := validCompositeCompileInput(t)
	weights := [...]uint32{2500, 2000, 1500, 1200, 1000, 800, 600, 400}
	input.Parent = rewriteCompositeControl(
		t,
		input.Parent,
		func(control *controlcontract.ControlSnapshot) {
			baseProfile := control.Profiles[0]
			members := make(
				[]controlcontract.CompositeAgentMemberV1,
				corecontract.CompositeMaxChildrenV1,
			)
			for index := range members {
				suffix := fmt.Sprintf("%02d", index)
				digestCharacter := fmt.Sprintf("%x", index)
				agentID := "agent.specialist." + suffix
				profileID := "profile.specialist." + suffix
				instanceID := "model-specialist-" + suffix
				control.Agents = append(control.Agents, corecontract.AgentRef{
					ID: agentID, Version: "1", Digest: hash(digestCharacter),
				})
				profile := baseProfile
				profile.Profile = corecontract.ProfileRef{
					ID: profileID, Version: "1", Digest: hash(digestCharacter),
				}
				profile.Bindings = []controlcontract.BindingSpec{binding(
					moduleapi.PortNameModelGenerate,
					instanceID,
					digestCharacter,
					digestCharacter,
					moduleapi.FailureRequired,
				)}
				control.Profiles = append(control.Profiles, profile)
				members[index] = controlcontract.CompositeAgentMemberV1{
					SlotID:            "slot." + suffix,
					AgentID:           agentID,
					ProfileID:         profileID,
					FocusID:           "focus." + suffix,
					WeightBasisPoints: weights[index],
				}
			}
			definition := &control.CompositeAgents[0]
			definition.Members = members
			definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
				SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
				AgentID:         members[0].AgentID,
				ProfileID:       members[0].ProfileID,
				MaxOutputTokens: 512,
				Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
			}
			definition.Decision = &controlcontract.CompositeDecisionDefinitionV1{
				SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
			}
		},
	)

	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.Parent.CatalogCanonical,
		input.Parent.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < corecontract.CompositeMaxChildrenV1; index++ {
		suffix := fmt.Sprintf("%02d", index)
		catalog.Entries = append(
			catalog.Entries,
			catalogEntry(
				"model-specialist-"+suffix,
				"model.specialist.s"+suffix,
				moduleapi.PortNameModelGenerate,
			),
		)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	input.Parent.CatalogCanonical = catalogCanonical
	input.Parent.PublishedBasis.Catalog = catalogRef
	return input
}

func assertCompositeMemberContractEqual(
	t *testing.T,
	before corecontract.MemberExecutionSnapshot,
	after corecontract.MemberExecutionSnapshot,
) {
	t.Helper()
	if before.MemberID != after.MemberID ||
		before.Agent != after.Agent ||
		before.Profile != after.Profile ||
		!reflect.DeepEqual(before.ModelProfile, after.ModelProfile) ||
		before.Workspace != after.Workspace ||
		!reflect.DeepEqual(before.PortPlans, after.PortPlans) ||
		!reflect.DeepEqual(before.Actions, after.Actions) ||
		before.ContextPolicy != after.ContextPolicy ||
		before.SchedulingPolicy != after.SchedulingPolicy {
		t.Fatalf("Decision opt-in rewrote initial member contract:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func rewriteCompositeControl(
	t *testing.T,
	input CompileInput,
	mutate func(*controlcontract.ControlSnapshot),
) CompileInput {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		input.ControlCanonical,
		input.PublishedBasis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&control)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.CatalogCanonical,
		input.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	input.ControlCanonical = controlCanonical
	input.CatalogCanonical = catalogCanonical
	input.PublishedBasis.Control = controlRef
	input.PublishedBasis.Catalog = catalogRef
	return input
}

func corruptCompositeWeight(t *testing.T, input CompositeCompileInput) CompositeCompileInput {
	t.Helper()
	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.Parent.CatalogCanonical,
		input.Parent.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	var control map[string]any
	if err := json.Unmarshal(input.Parent.ControlCanonical, &control); err != nil {
		t.Fatal(err)
	}
	definitions := control["composite_agents"].([]any)
	members := definitions[0].(map[string]any)["members"].([]any)
	members[0].(map[string]any)["weight_basis_points"] = float64(6999)
	delete(control, "digest")
	identityEncoded, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	identityCanonical, err := moduleapi.CanonicalJSON(identityEncoded)
	if err != nil {
		t.Fatal(err)
	}
	digest := moduleapi.Digest("freeagent.control-snapshot/v1", identityCanonical)
	control["digest"] = digest
	encoded, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	input.Parent.ControlCanonical = canonical
	input.Parent.PublishedBasis.Control.Digest = digest
	catalog.ControlSnapshotDigest = digest
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	input.Parent.CatalogCanonical = catalogCanonical
	input.Parent.PublishedBasis.Catalog = catalogRef
	return input
}

func compositeChildrenBySlot(
	children []CompositeChildCompileOutput,
) map[string]CompositeChildCompileOutput {
	result := make(map[string]CompositeChildCompileOutput, len(children))
	for _, child := range children {
		result[child.Assignment.SlotID] = child
	}
	return result
}

func compositePlanChildBySlot(
	t *testing.T,
	children []corecontract.CompositeChildRunRefV1,
	slotID string,
) corecontract.CompositeChildRunRefV1 {
	t.Helper()
	for _, child := range children {
		if child.SlotID == slotID {
			return child
		}
	}
	t.Fatalf("composite plan Child slot %q is absent", slotID)
	return corecontract.CompositeChildRunRefV1{}
}

func compositeInputWithWorkspaceTransfer(
	t *testing.T,
	input CompositeCompileInput,
	slotID string,
	mutate func(
		*corecontract.WorkspaceTransferGrantV1,
		*corecontract.WorkspaceTransferGrantV1,
	),
) CompositeCompileInput {
	t.Helper()
	input.Parent = rewriteCompositeControl(
		t,
		input.Parent,
		func(control *controlcontract.ControlSnapshot) {
			rootIndex := -1
			for index := range control.Workspaces {
				if control.Workspaces[index].Workspace.ID == "workspace.main" {
					rootIndex = index
					break
				}
			}
			if rootIndex < 0 {
				t.Fatal("root Workspace is absent")
			}
			root := control.Workspaces[rootIndex]
			target := controlcontract.WorkspaceDefinition{
				Workspace: corecontract.WorkspaceRef{
					ID:      "workspace.specialist",
					Version: "1",
					Digest:  hash("f"),
				},
			}
			rootGrant := compositeWorkspaceTransferGrant(
				"grant-root-specialist",
				control.TenantID,
				root.Workspace,
				target.Workspace,
				true,
			)
			targetGrant := compositeWorkspaceTransferGrant(
				"grant-specialist-root",
				control.TenantID,
				target.Workspace,
				root.Workspace,
				false,
			)
			if mutate != nil {
				mutate(&rootGrant, &targetGrant)
			}
			root.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
				rootGrant,
			}
			target.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
				targetGrant,
			}
			control.Workspaces[rootIndex] = root
			control.Workspaces = append(control.Workspaces, target)

			found := false
			for definitionIndex := range control.CompositeAgents {
				definition := &control.CompositeAgents[definitionIndex]
				if definition.AgentID != "agent.chat" {
					continue
				}
				for memberIndex := range definition.Members {
					if definition.Members[memberIndex].SlotID != slotID {
						continue
					}
					definition.Members[memberIndex].TargetWorkspaceID =
						target.Workspace.ID
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("composite member slot %q is absent", slotID)
			}
		},
	)
	return input
}

func compositeWorkspaceTransferGrant(
	id string,
	tenantID string,
	owner corecontract.WorkspaceRef,
	peer corecontract.WorkspaceRef,
	root bool,
) corecontract.WorkspaceTransferGrantV1 {
	grant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       id,
		TenantID:      tenantID,
		Workspace:     owner,
		PeerWorkspace: peer,
		Revision:      1,
		Enabled:       true,
	}
	if root {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
		return grant
	}
	grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
	}
	grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
	}
	grant.MaxSendPayloadBytes = corecontract.WorkspaceTransferMaximumPayloadBytesV1
	grant.MaxReceivePayloadBytes =
		corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
	return grant
}

func compositeModelProviderInstance(
	t *testing.T,
	member corecontract.MemberExecutionSnapshot,
) string {
	t.Helper()
	for _, plan := range member.PortPlans {
		if plan.Port == compositeModelGeneratePortV1 {
			if len(plan.Bindings) != 1 {
				t.Fatalf("model PortPlan bindings = %d", len(plan.Bindings))
			}
			return plan.Bindings[0].Provider.InstanceID
		}
	}
	t.Fatal("model PortPlan missing")
	return ""
}
