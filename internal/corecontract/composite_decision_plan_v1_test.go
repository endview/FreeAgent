package corecontract

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCompositeDecisionPlanV1FreezesRepairFamilyAndDefensiveCopies(
	t *testing.T,
) {
	input := decisionPlanManifestInput(t, 2)
	frozen, canonical, err := NewRunManifest(input)
	if err != nil {
		t.Fatal(err)
	}
	plan := frozen.Composite.Plan
	if plan.Decision == nil || plan.FamilyModelDispatchLimit != 7 ||
		len(plan.Decision.RepairChildren) != 2 ||
		plan.Decision.RepairReviewer.ParentSlotID == "" {
		t.Fatalf("frozen decision plan=%+v", plan)
	}
	if !bytes.Contains(canonical, []byte(`"decision":{`)) ||
		!bytes.Contains(canonical, []byte(`"parent_slot_id":"repair.slot.00"`)) {
		t.Fatalf("decision plan absent from canonical Manifest: %s", canonical)
	}

	input.Composite.Plan.Decision.RepairChildren[0].RunID = "mutated-input"
	input.Composite.Plan.Decision.RepairReviewer.RunID = "mutated-reviewer"
	if plan.Decision.RepairChildren[0].RunID != "run-repair-00" ||
		plan.Decision.RepairReviewer.RunID != "run-reviewer-repair" {
		t.Fatalf("frozen decision plan aliases input: %+v", plan.Decision)
	}
	plan.Decision.RepairChildren[0].RunID = "mutated-output"
	plan.Decision.RepairReviewer.RunID = "mutated-output-reviewer"
	if frozen.Composite.Plan.Decision.RepairChildren[0].RunID != "mutated-output" {
		t.Fatal("test did not mutate the local returned value")
	}
	restored, err := RestoreRunManifest(canonical)
	if err != nil || restored.Composite.Plan.Decision.RepairChildren[0].RunID !=
		"run-repair-00" ||
		restored.Composite.Plan.Decision.RepairReviewer.RunID !=
			"run-reviewer-repair" {
		t.Fatalf("restored decision plan error=%v plan=%+v", err, restored.Composite)
	}
}

func TestCompositeDecisionNilPreservesLegacyReviewerWireShape(t *testing.T) {
	root := reviewerTestRootManifest(t, true)
	rebuilt, canonical, err := NewRunManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ManifestDigest != root.ManifestDigest {
		t.Fatalf(
			"Decision=nil changed legacy Reviewer digest got=%s want=%s",
			rebuilt.ManifestDigest,
			root.ManifestDigest,
		)
	}
	for _, forbidden := range [][]byte{
		[]byte(`"decision"`),
		[]byte(`"parent_slot_id"`),
		[]byte(`"repair_round"`),
	} {
		if bytes.Contains(canonical, forbidden) {
			t.Fatalf("Decision=nil emitted %s in legacy wire: %s", forbidden, canonical)
		}
	}
	if rebuilt.Composite.Plan.FamilyModelDispatchLimit != 4 {
		t.Fatalf("legacy Reviewer cap=%d want 4", rebuilt.Composite.Plan.FamilyModelDispatchLimit)
	}
}

func TestCompositeDecisionPlanV1DispatchBoundsForTwoAndEightSpecialists(
	t *testing.T,
) {
	for _, test := range []struct {
		children int
		cap      uint32
	}{
		{children: 2, cap: 7},
		{children: 8, cap: 19},
	} {
		t.Run(fmt.Sprintf("%d Specialists", test.children), func(t *testing.T) {
			input := decisionPlanManifestInput(t, test.children)
			frozen, _, err := NewRunManifest(input)
			if err != nil {
				t.Fatal(err)
			}
			if frozen.Composite.Plan.FamilyModelDispatchLimit != test.cap {
				t.Fatalf(
					"dispatch cap=%d want %d",
					frozen.Composite.Plan.FamilyModelDispatchLimit,
					test.cap,
				)
			}
			input.Composite.Plan.FamilyModelDispatchLimit--
			if _, _, err := NewRunManifest(input); err == nil {
				t.Fatal("decision plan accepted a smaller dispatch cap")
			}
		})
	}
}

func TestCompositeDecisionPlanV1RejectsIdentityRoutingAndCardinalityDrift(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*RunManifest)
	}{
		{"schema", func(v *RunManifest) {
			v.Composite.Plan.Decision.SchemaVersion = "composite-decision-plan/v2"
		}},
		{"without initial Reviewer", func(v *RunManifest) {
			v.Composite.Plan.Reviewer = nil
		}},
		{"repair cardinality", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren =
				v.Composite.Plan.Decision.RepairChildren[:1]
		}},
		{"repair order", func(v *RunManifest) {
			repairs := v.Composite.Plan.Decision.RepairChildren
			repairs[0], repairs[1] = repairs[1], repairs[0]
		}},
		{"missing physical slot", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].ParentSlotID = ""
		}},
		{"physical slot collides with initial", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].ParentSlotID =
				v.Composite.Plan.Children[0].SlotID
		}},
		{"duplicate physical slots", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[1].ParentSlotID =
				v.Composite.Plan.Decision.RepairChildren[0].ParentSlotID
		}},
		{"repair Run reuses initial", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].RunID =
				v.Composite.Plan.Children[0].RunID
		}},
		{"repair Admission reuses initial", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].AdmissionKey =
				v.Composite.Plan.Children[0].AdmissionKey
		}},
		{"repair member reuses initial", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].MemberSnapshotDigest =
				v.Composite.Plan.Children[0].MemberSnapshotDigest
		}},
		{"logical slot drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].SlotID = "slot.changed"
		}},
		{"Agent drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].Agent.ID = "agent.changed"
		}},
		{"Profile drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].Profile.ID = "profile.changed"
		}},
		{"TaskInput drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].TaskInputRef = strings.Repeat("f", 64)
		}},
		{"Assignment drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairChildren[0].Assignment.FocusID = "changed"
		}},
		{"repair Reviewer physical collision", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairReviewer.ParentSlotID =
				v.Composite.Plan.Decision.RepairChildren[0].ParentSlotID
		}},
		{"repair Reviewer Run collision", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairReviewer.RunID =
				v.Composite.Plan.Reviewer.RunID
		}},
		{"repair Reviewer Agent drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairReviewer.Agent.ID = "agent.changed"
		}},
		{"repair Reviewer policy drift", func(v *RunManifest) {
			v.Composite.Plan.Decision.RepairReviewer.Policy = "AUTO"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := decisionPlanManifestInput(t, 2)
			test.mutate(&input)
			if _, _, err := NewRunManifest(input); err == nil {
				t.Fatal("invalid composite decision plan was accepted")
			}
		})
	}
}

func TestCompositeRepairRunNodesRequireRoundOneAndDistinctPhysicalSlots(
	t *testing.T,
) {
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	child := validRunManifestInput(member)
	child.RunID = "run-repair-child"
	child.ParentRunID = "run-root"
	child.CancellationScope = CancellationScopeInheritedV1
	child.Composite = &CompositeRunNodeV1{
		SchemaVersion:        CompositeRunNodeSchemaVersionV1,
		Role:                 CompositeRunRoleChildV1,
		RepairRound:          CompositeRepairRoundOneV1,
		RootRunID:            child.ParentRunID,
		ParentManifestDigest: strings.Repeat("a", 64),
		ParentSlotID:         "repair.slot.backend",
		Assignment: &CompositeAssignmentV1{
			SlotID: "slot.backend", FocusID: "backend", WeightBasisPoints: 5000,
		},
	}
	if _, _, err := NewRunManifest(child); err != nil {
		t.Fatalf("valid repair Child: %v", err)
	}
	child.Composite.ParentSlotID = child.Composite.Assignment.SlotID
	if _, _, err := NewRunManifest(child); err == nil {
		t.Fatal("repair Child reused its logical slot as the physical parent slot")
	}
	child.Composite.ParentSlotID = "repair.slot.backend"
	child.Composite.RepairRound = 2
	if _, _, err := NewRunManifest(child); err == nil {
		t.Fatal("repair Child accepted round two")
	}

	reviewer := validRunManifestInput(member)
	reviewer.RunID = "run-repair-reviewer"
	reviewer.ParentRunID = "run-root"
	reviewer.CancellationScope = CancellationScopeInheritedV1
	reviewer.Composite = &CompositeRunNodeV1{
		SchemaVersion:        CompositeRunNodeSchemaVersionV1,
		Role:                 CompositeRunRoleReviewerV1,
		RepairRound:          CompositeRepairRoundOneV1,
		RootRunID:            reviewer.ParentRunID,
		ParentManifestDigest: strings.Repeat("a", 64),
		ParentSlotID:         "repair.reviewer",
	}
	if _, _, err := NewRunManifest(reviewer); err != nil {
		t.Fatalf("valid repair Reviewer: %v", err)
	}
	reviewer.Composite.ParentSlotID = CompositeReviewerParentSlotIDV1
	if _, _, err := NewRunManifest(reviewer); err == nil {
		t.Fatal("round-one Reviewer reused the initial Reviewer parent slot")
	}
}

func decisionPlanManifestInput(t *testing.T, childCount int) RunManifest {
	t.Helper()
	if childCount != 2 && childCount != 8 {
		t.Fatalf("unsupported decision test child count %d", childCount)
	}
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	input := validRunManifestInput(member)
	input.CancellationScope = CancellationScopeFamilyV1
	weight := uint32(CompositeWeightBasisPointsV1 / uint64(childCount))
	children := make([]CompositeChildRunRefV1, childCount)
	repairs := make([]CompositeChildRunRefV1, childCount)
	for index := 0; index < childCount; index++ {
		slotID := fmt.Sprintf("slot.%02d", index)
		assignment := CompositeAssignmentV1{
			SlotID: slotID, FocusID: fmt.Sprintf("focus.%02d", index),
			WeightBasisPoints: weight,
		}
		children[index] = CompositeChildRunRefV1{
			SlotID: slotID, RunID: fmt.Sprintf("run-initial-%02d", index),
			AdmissionKey:         fmt.Sprintf("admission-initial-%02d", index),
			MemberSnapshotDigest: decisionTestDigest(index + 1),
			Agent:                member.Agent, Profile: member.Profile,
			TaskInputRef: input.TaskInputRef, Assignment: assignment,
		}
		repairs[index] = children[index]
		repairs[index].ParentSlotID = fmt.Sprintf("repair.slot.%02d", index)
		repairs[index].RunID = fmt.Sprintf("run-repair-%02d", index)
		repairs[index].AdmissionKey = fmt.Sprintf("admission-repair-%02d", index)
		repairs[index].MemberSnapshotDigest = decisionTestDigest(childCount + index + 2)
	}
	reviewer := CompositeReviewerRunRefV1{
		RunID: "run-reviewer", AdmissionKey: "admission-reviewer",
		MemberSnapshotDigest: decisionTestDigest(childCount + 1),
		Agent:                member.Agent, Profile: member.Profile,
		TaskInputRef:        input.TaskInputRef,
		ReviewLogicalStepID: CompositeReviewLogicalStepIDV1,
		Policy:              CompositeReviewerPolicyResultsGateV1, MaxOutputTokens: 512,
	}
	repairReviewer := reviewer
	repairReviewer.ParentSlotID = "repair.reviewer"
	repairReviewer.RunID = "run-reviewer-repair"
	repairReviewer.AdmissionKey = "admission-reviewer-repair"
	repairReviewer.MemberSnapshotDigest = decisionTestDigest(2*childCount + 2)
	input.Composite = &CompositeRunNodeV1{
		SchemaVersion: CompositeRunNodeSchemaVersionV1,
		Role:          CompositeRunRoleRootV1, RootRunID: input.RunID,
		Plan: &CompositeRunPlanV1{
			MergeLogicalStepID:       CompositeMergeLogicalStepIDV1,
			FamilyModelDispatchLimit: uint32(2*childCount + 3),
			Children:                 children,
			Reviewer:                 &reviewer,
			Decision: &CompositeDecisionPlanV1{
				SchemaVersion:  CompositeDecisionPlanSchemaVersionV1,
				RepairChildren: repairs,
				RepairReviewer: repairReviewer,
			},
		},
	}
	return input
}

func decisionTestDigest(value int) string {
	return fmt.Sprintf("%064x", value)
}
