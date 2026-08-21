package corecontract

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCompositeSpecialistResultSetV1RoundTripAllowsSharedResultRef(t *testing.T) {
	root := reviewerTestRootManifest(t, true)
	sharedResult := strings.Repeat("d", 64)
	input := CompositeSpecialistResultSetV1{
		SchemaVersion: CompositeSpecialistResultSetSchemaVersionV1,
		FamilyDigest:  root.ManifestDigest,
		TaskInputRef:  root.TaskInputRef,
		Results: []CompositeSpecialistResultV1{
			{
				SlotID: "slot.backend", FocusID: "backend", WeightBasisPoints: 6000,
				RunID: "run-backend", ManifestDigest: strings.Repeat("1", 64),
				MemberSnapshotDigest: strings.Repeat("2", 64), ResultRef: sharedResult,
				TerminalRunRevision: 2, TerminalFrameRevision: 3,
			},
			{
				SlotID: "slot.frontend", FocusID: "frontend", WeightBasisPoints: 4000,
				RunID: "run-frontend", ManifestDigest: strings.Repeat("3", 64),
				MemberSnapshotDigest: strings.Repeat("4", 64), ResultRef: sharedResult,
				TerminalRunRevision: 4, TerminalFrameRevision: 5,
			},
		},
	}
	frozen, canonical, digest, err := NewCompositeSpecialistResultSetV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := frozen.ValidateForCompositeReviewV1(root); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreCompositeSpecialistResultSetV1(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%+v", restored) != fmt.Sprintf("%+v", frozen) {
		t.Fatalf("restored result set differs: %+v", restored)
	}
	if _, err := RestoreCompositeSpecialistResultSetV1(canonical, strings.Repeat("e", 64)); err == nil {
		t.Fatal("result set accepted a different digest")
	}
}

func TestReviewVerdictV1ParsesAnyKeyOrderAndBindsFamily(t *testing.T) {
	root := reviewerTestRootManifest(t, true)
	resultDigest := strings.Repeat("a", 64)
	raw := []byte(` {
  "specialist_result_digest":"` + resultDigest + `",
  "schema_version":"review-verdict/v1",
  "issue_codes":[],
  "family_digest":"` + root.ManifestDigest + `",
  "decision":"APPROVE",
  "bounded_reason":"Evidence is complete.",
  "affected_slot_ids":[]
} `)
	verdict, canonical, err := ParseReviewVerdictV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, canonical) || verdict.Decision != ReviewDecisionApproveV1 {
		t.Fatalf("parse did not rebuild canonical verdict: %s", canonical)
	}
	if err := verdict.ValidateForCompositeReviewV1(root, resultDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreReviewVerdictV1(canonical); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseReviewVerdictV1(append(bytes.Clone(raw), []byte(` {}`)...)); err == nil {
		t.Fatal("a second JSON value was accepted")
	}
	unknown := bytes.Replace(raw, []byte(`"decision"`), []byte(`"unknown":1,"decision"`), 1)
	if _, _, err := ParseReviewVerdictV1(unknown); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestReviewVerdictV1RejectsInvalidDecisionShapeAndUnknownSlot(t *testing.T) {
	root := reviewerTestRootManifest(t, true)
	resultDigest := strings.Repeat("a", 64)
	for _, verdict := range []ReviewVerdictV1{
		{
			SchemaVersion: ReviewVerdictSchemaVersionV1,
			FamilyDigest:  root.ManifestDigest, SpecialistResultDigest: resultDigest,
			Decision:        ReviewDecisionApproveV1,
			IssueCodes:      []ReviewIssueCodeV1{ReviewIssueSecurityConcernV1},
			AffectedSlotIDs: []string{"slot.backend"}, BoundedReason: "bad approve",
		},
		{
			SchemaVersion: ReviewVerdictSchemaVersionV1,
			FamilyDigest:  root.ManifestDigest, SpecialistResultDigest: resultDigest,
			Decision:   ReviewDecisionRejectV1,
			IssueCodes: []ReviewIssueCodeV1{}, AffectedSlotIDs: []string{},
			BoundedReason: "bad reject",
		},
	} {
		if _, _, err := NewReviewVerdictV1(verdict); err == nil {
			t.Fatalf("invalid verdict accepted: %+v", verdict)
		}
	}
	verdict, _, err := NewReviewVerdictV1(ReviewVerdictV1{
		SchemaVersion: ReviewVerdictSchemaVersionV1,
		FamilyDigest:  root.ManifestDigest, SpecialistResultDigest: resultDigest,
		Decision:        ReviewDecisionRejectV1,
		IssueCodes:      []ReviewIssueCodeV1{ReviewIssueSecurityConcernV1},
		AffectedSlotIDs: []string{"slot.unknown"}, BoundedReason: "unknown slot",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := verdict.ValidateForCompositeReviewV1(root, resultDigest); err == nil {
		t.Fatal("unknown affected slot was accepted")
	}
}

func reviewerTestRootManifest(
	t *testing.T,
	reviewerEnabled bool,
	decisionEnabled ...bool,
) RunManifest {
	t.Helper()
	if len(decisionEnabled) > 1 ||
		(len(decisionEnabled) == 1 && decisionEnabled[0] && !reviewerEnabled) {
		t.Fatal("invalid Reviewer/Decision test helper request")
	}
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	input := validRunManifestInput(member)
	input.CancellationScope = CancellationScopeFamilyV1
	children := []CompositeChildRunRefV1{
		{
			SlotID: "slot.backend", RunID: "run-backend", AdmissionKey: "admission-backend",
			MemberSnapshotDigest: strings.Repeat("2", 64), Agent: member.Agent,
			Profile: member.Profile, TaskInputRef: input.TaskInputRef,
			Assignment: CompositeAssignmentV1{SlotID: "slot.backend", FocusID: "backend", WeightBasisPoints: 6000},
		},
		{
			SlotID: "slot.frontend", RunID: "run-frontend", AdmissionKey: "admission-frontend",
			MemberSnapshotDigest: strings.Repeat("4", 64), Agent: member.Agent,
			Profile: member.Profile, TaskInputRef: input.TaskInputRef,
			Assignment: CompositeAssignmentV1{SlotID: "slot.frontend", FocusID: "frontend", WeightBasisPoints: 4000},
		},
	}
	plan := &CompositeRunPlanV1{
		MergeLogicalStepID:       CompositeMergeLogicalStepIDV1,
		FamilyModelDispatchLimit: 3,
		Children:                 children,
	}
	if reviewerEnabled {
		plan.FamilyModelDispatchLimit = 4
		plan.Reviewer = &CompositeReviewerRunRefV1{
			RunID: "run-reviewer", AdmissionKey: "admission-reviewer",
			MemberSnapshotDigest: strings.Repeat("5", 64), Agent: member.Agent,
			Profile: member.Profile, TaskInputRef: input.TaskInputRef,
			ReviewLogicalStepID: CompositeReviewLogicalStepIDV1,
			Policy:              CompositeReviewerPolicyResultsGateV1, MaxOutputTokens: 512,
		}
	}
	if len(decisionEnabled) == 1 && decisionEnabled[0] {
		plan.FamilyModelDispatchLimit = uint32(2*len(children) + 3)
		repairChildren := append([]CompositeChildRunRefV1(nil), children...)
		repairChildren[0].ParentSlotID = "repair.slot.backend"
		repairChildren[0].RunID = "run-backend-repair"
		repairChildren[0].AdmissionKey = "admission-backend-repair"
		repairChildren[0].MemberSnapshotDigest = strings.Repeat("6", 64)
		repairChildren[1].ParentSlotID = "repair.slot.frontend"
		repairChildren[1].RunID = "run-frontend-repair"
		repairChildren[1].AdmissionKey = "admission-frontend-repair"
		repairChildren[1].MemberSnapshotDigest = strings.Repeat("7", 64)
		repairReviewer := *plan.Reviewer
		repairReviewer.ParentSlotID = "repair.reviewer"
		repairReviewer.RunID = "run-reviewer-repair"
		repairReviewer.AdmissionKey = "admission-reviewer-repair"
		repairReviewer.MemberSnapshotDigest = strings.Repeat("8", 64)
		plan.Decision = &CompositeDecisionPlanV1{
			SchemaVersion:  CompositeDecisionPlanSchemaVersionV1,
			RepairChildren: repairChildren,
			RepairReviewer: repairReviewer,
		}
	}
	input.Composite = &CompositeRunNodeV1{
		SchemaVersion: CompositeRunNodeSchemaVersionV1,
		Role:          CompositeRunRoleRootV1, RootRunID: input.RunID, Plan: plan,
	}
	frozen, canonical, err := NewRunManifest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reviewerEnabled && bytes.Contains(canonical, []byte(`"reviewer"`)) {
		t.Fatalf("Reviewer-disabled Manifest changed its wire: %s", canonical)
	}
	return frozen
}
