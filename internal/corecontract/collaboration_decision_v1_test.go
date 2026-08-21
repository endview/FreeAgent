package corecontract

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestSpecialistContributionV1CanonicalDigestRoundTripAndDefensiveCopy(
	t *testing.T,
) {
	input := validSpecialistContributionV1()
	frozen, canonical, digest, err := NewSpecialistContributionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"assumptions":["The API remains backward compatible."],"conflicts":[],"evidence":[{"bounded_claim":"Trace e-1 reproduces the failure.","ref":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}],"proposal":"Add an idempotent retry guard.","risks":["A stale lease can delay retry."],"schema_version":"specialist-contribution/v1"}`
	if string(canonical) != want {
		t.Fatalf("canonical contribution=%s\nwant=%s", canonical, want)
	}
	if !moduleapi.ValidSHA256(digest) || digest != moduleapi.Digest(
		specialistContributionDigestDomainV1,
		canonical,
	) {
		t.Fatalf("invalid Specialist contribution digest %q", digest)
	}

	input.Evidence[0].BoundedClaim = "mutated"
	input.Conflicts = append(input.Conflicts, "mutated")
	if frozen.Evidence[0].BoundedClaim !=
		"Trace e-1 reproduces the failure." || len(frozen.Conflicts) != 0 {
		t.Fatalf("contribution was not defensively copied: %+v", frozen)
	}
	restored, err := RestoreSpecialistContributionV1(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	restored.Evidence[0].BoundedClaim = "restored mutation"
	if frozen.Evidence[0].BoundedClaim !=
		"Trace e-1 reproduces the failure." {
		t.Fatal("restored contribution aliases frozen input")
	}
	if _, err := RestoreSpecialistContributionV1(
		canonical,
		strings.Repeat("f", 64),
	); err == nil {
		t.Fatal("Specialist contribution accepted a different digest")
	}
	if _, err := RestoreSpecialistContributionV1(
		append([]byte{' '}, canonical...),
		digest,
	); err == nil {
		t.Fatal("non-canonical Specialist contribution was restored")
	}
}

func TestParseSpecialistContributionV1IsStrictAndAcceptsNormalizedLF(
	t *testing.T,
) {
	raw := []byte(` {
  "risks":[],
  "schema_version":"specialist-contribution/v1",
  "proposal":"Use the bounded path.\nRetain exact evidence.",
  "conflicts":[],
  "evidence":[{"ref":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","bounded_claim":"First line.\nSecond line."}],
  "assumptions":[]
} `)
	parsed, canonical, digest, err := ParseSpecialistContributionV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, canonical) ||
		parsed.Proposal != "Use the bounded path.\nRetain exact evidence." ||
		!moduleapi.ValidSHA256(digest) {
		t.Fatalf("contribution was not rebuilt canonically: %s", canonical)
	}

	invalid := [][]byte{
		bytes.Replace(raw, []byte(`"proposal"`), []byte(`"unknown":1,"proposal"`), 1),
		append(bytes.Clone(raw), []byte(` {}`)...),
		[]byte("```json\n" + string(raw) + "\n```"),
		[]byte(`{"schema_version":"specialist-contribution/v1","proposal":"x","evidence":[],"assumptions":[],"risks":[]}`),
		[]byte(`{"schema_version":"specialist-contribution/v1","proposal":"x","evidence":null,"assumptions":[],"risks":[],"conflicts":[]}`),
		[]byte(`{"schema_version":"specialist-contribution/v1","proposal":"first","proposal":"second","evidence":[],"assumptions":[],"risks":[],"conflicts":[]}`),
		[]byte(`{"schema_version":"specialist-contribution/v1","proposal":"first\r\nsecond","evidence":[],"assumptions":[],"risks":[],"conflicts":[]}`),
	}
	for index, candidate := range invalid {
		if _, _, _, err := ParseSpecialistContributionV1(candidate); err == nil {
			t.Fatalf("invalid Specialist contribution %d was accepted", index)
		}
	}
}

func TestSpecialistContributionV1RejectsInvalidEvidenceTextAndBounds(
	t *testing.T,
) {
	tests := []func(*SpecialistContributionV1){
		func(value *SpecialistContributionV1) { value.Proposal = "" },
		func(value *SpecialistContributionV1) { value.Proposal = " trailing " },
		func(value *SpecialistContributionV1) { value.Proposal = "Cafe\u0301" },
		func(value *SpecialistContributionV1) { value.Proposal = "tab\tbreak" },
		func(value *SpecialistContributionV1) {
			value.Proposal = strings.Repeat("p", SpecialistContributionMaxProposalBytesV1+1)
		},
		func(value *SpecialistContributionV1) { value.Evidence = nil },
		func(value *SpecialistContributionV1) { value.Assumptions = nil },
		func(value *SpecialistContributionV1) {
			value.Evidence[0].Ref = "not-a-digest"
		},
		func(value *SpecialistContributionV1) {
			value.Evidence = append(value.Evidence, value.Evidence[0])
		},
		func(value *SpecialistContributionV1) {
			value.Evidence = append(value.Evidence, SpecialistEvidenceV1{
				Ref:          strings.Repeat("d", 64),
				BoundedClaim: value.Evidence[0].BoundedClaim,
			})
		},
		func(value *SpecialistContributionV1) {
			value.Evidence[0].BoundedClaim = strings.Repeat(
				"e",
				SpecialistContributionMaxItemBytesV1+1,
			)
		},
		func(value *SpecialistContributionV1) {
			value.Risks = []string{"duplicate", "duplicate"}
		},
		func(value *SpecialistContributionV1) {
			value.Conflicts = make([]string, SpecialistContributionMaxItemsPerSectionV1+1)
			for index := range value.Conflicts {
				value.Conflicts[index] = string(rune('a' + index))
			}
		},
	}
	for index, mutate := range tests {
		value := validSpecialistContributionV1()
		mutate(&value)
		if _, _, _, err := NewSpecialistContributionV1(value); err == nil {
			t.Fatalf("invalid Specialist contribution %d was accepted: %+v", index, value)
		}
	}
}

func TestCollaborationContributionSetV1CanonicalRoundTripAndRootBinding(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	input := validCollaborationContributionSetV1(root)
	frozen, canonical, digest, err := NewCollaborationContributionSetV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := frozen.ValidateForCollaborationRootV1(root); err != nil {
		t.Fatal(err)
	}
	if !moduleapi.ValidSHA256(digest) || !bytes.Contains(
		canonical,
		[]byte(`"previous_set_digest":"","repair_round":0`),
	) || !bytes.Contains(canonical, []byte(`"verdict_ref":""`)) {
		t.Fatalf("invalid initial contribution set: %s digest=%q", canonical, digest)
	}
	restored, err := RestoreCollaborationContributionSetV1(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	input.Contributions[0].RunID = "mutated"
	if frozen.Contributions[0].RunID != "run-backend" ||
		restored.Contributions[0].RunID != "run-backend" {
		t.Fatal("contribution set was not defensively copied")
	}
	if _, err := RestoreCollaborationContributionSetV1(
		canonical,
		strings.Repeat("f", 64),
	); err == nil {
		t.Fatal("contribution set accepted a different digest")
	}

	reversed := frozen
	reversed.Contributions = append(
		[]CollaborationContributionEntryV1{},
		frozen.Contributions...,
	)
	reversed.Contributions[0], reversed.Contributions[1] =
		reversed.Contributions[1], reversed.Contributions[0]
	if _, _, _, err := NewCollaborationContributionSetV1(reversed); err != nil {
		t.Fatalf("standalone Host order was unexpectedly rewritten: %v", err)
	}
	if err := reversed.ValidateForCollaborationRootV1(root); err == nil {
		t.Fatal("contribution set outside root-plan order was accepted")
	}
}

func TestCollaborationContributionSetV1RejectsInvalidRoundAndStrictWire(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	base := validCollaborationContributionSetV1(root)
	invalid := []func(*CollaborationContributionSetV1){
		func(value *CollaborationContributionSetV1) { value.RepairRound = 2 },
		func(value *CollaborationContributionSetV1) { value.PreviousSetDigest = strings.Repeat("d", 64) },
		func(value *CollaborationContributionSetV1) { value.VerdictRef = strings.Repeat("e", 64) },
		func(value *CollaborationContributionSetV1) { value.RepairRound = 1 },
		func(value *CollaborationContributionSetV1) { value.Contributions = nil },
		func(value *CollaborationContributionSetV1) { value.Contributions[0].ResultRef = "bad" },
		func(value *CollaborationContributionSetV1) {
			value.Contributions[1].SlotID = value.Contributions[0].SlotID
		},
		func(value *CollaborationContributionSetV1) {
			value.Contributions[1].RunID = value.Contributions[0].RunID
		},
	}
	for index, mutate := range invalid {
		value := cloneCollaborationContributionSetTestV1(base)
		mutate(&value)
		if _, _, _, err := NewCollaborationContributionSetV1(value); err == nil {
			t.Fatalf("invalid contribution set %d accepted: %+v", index, value)
		}
	}

	_, canonical, digest, err := NewCollaborationContributionSetV1(base)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(
		canonical,
		[]byte(`"family_digest"`),
		[]byte(`"unknown":true,"family_digest"`),
		1,
	)
	unknown, err = moduleapi.CanonicalJSON(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCollaborationContributionSetV1(unknown, digest); err == nil {
		t.Fatal("unknown contribution-set field was accepted")
	}
}

func TestCollaborationContributionSetV1ValidatesOneAffectedSlotRepairLineage(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	previous := validCollaborationContributionSetV1(root)
	_, _, previousDigest, err := NewCollaborationContributionSetV1(previous)
	if err != nil {
		t.Fatal(err)
	}
	verdict := CollaborationReviewVerdictV1{
		SchemaVersion:         CollaborationReviewVerdictSchemaVersionV1,
		FamilyDigest:          root.ManifestDigest,
		ContributionSetDigest: previousDigest,
		RepairRound:           0,
		Decision:              CollaborationReviewDecisionRepairRequiredV1,
		IssueCodes:            []ReviewIssueCodeV1{ReviewIssueMissingEvidenceV1},
		AffectedSlotIDs:       []string{"slot.backend"},
		BoundedReason:         "Repair the backend evidence.",
	}
	verdictRef := strings.Repeat("9", 64)
	repaired := cloneCollaborationContributionSetTestV1(previous)
	repaired.RepairRound = 1
	repaired.PreviousSetDigest = previousDigest
	repaired.VerdictRef = verdictRef
	repaired.Contributions[0].RunID =
		root.Composite.Plan.Decision.RepairChildren[0].RunID
	repaired.Contributions[0].ResultRef = strings.Repeat("7", 64)
	repaired.Contributions[0].ContributionDigest = strings.Repeat("8", 64)
	if _, _, _, err := NewCollaborationContributionSetV1(repaired); err != nil {
		t.Fatal(err)
	}
	if err := repaired.ValidateRepairLineageV1(
		previous,
		verdict,
		verdictRef,
	); err != nil {
		t.Fatal(err)
	}
	if err := repaired.ValidateForCollaborationRepairPlanV1(
		root,
		previous,
		verdict,
		verdictRef,
	); err != nil {
		t.Fatalf("repair set did not close to pre-frozen Runs: %v", err)
	}
	wrongRepairRun := cloneCollaborationContributionSetTestV1(repaired)
	wrongRepairRun.Contributions[0].RunID = "run-valid-lineage-but-not-planned"
	if err := wrongRepairRun.ValidateRepairLineageV1(
		previous,
		verdict,
		verdictRef,
	); err != nil {
		t.Fatalf("wrong repair Run should still pass pure lineage: %v", err)
	}
	if err := wrongRepairRun.ValidateForCollaborationRepairPlanV1(
		root,
		previous,
		verdict,
		verdictRef,
	); err == nil {
		t.Fatal("repair set accepted a Run outside the pre-frozen decision plan")
	}
	if err := repaired.ValidateForCollaborationRootV1(root); err == nil {
		t.Fatal("round-one repair set masqueraded as the initial root-bound set")
	}

	invalid := []func(*CollaborationContributionSetV1, *CollaborationReviewVerdictV1, *string){
		func(value *CollaborationContributionSetV1, _ *CollaborationReviewVerdictV1, _ *string) {
			value.Contributions[0].RunID = previous.Contributions[0].RunID
		},
		func(value *CollaborationContributionSetV1, _ *CollaborationReviewVerdictV1, _ *string) {
			value.Contributions[0].ContributionDigest = previous.Contributions[0].ContributionDigest
		},
		func(value *CollaborationContributionSetV1, _ *CollaborationReviewVerdictV1, _ *string) {
			value.Contributions[1].ResultRef = strings.Repeat("6", 64)
		},
		func(value *CollaborationContributionSetV1, _ *CollaborationReviewVerdictV1, _ *string) {
			value.PreviousSetDigest = strings.Repeat("5", 64)
		},
		func(_ *CollaborationContributionSetV1, value *CollaborationReviewVerdictV1, _ *string) {
			value.AffectedSlotIDs = []string{"slot.unknown"}
		},
		func(_ *CollaborationContributionSetV1, value *CollaborationReviewVerdictV1, _ *string) {
			value.Decision = CollaborationReviewDecisionRejectV1
		},
		func(_ *CollaborationContributionSetV1, _ *CollaborationReviewVerdictV1, value *string) {
			*value = strings.Repeat("4", 64)
		},
	}
	for index, mutate := range invalid {
		candidate := cloneCollaborationContributionSetTestV1(repaired)
		candidateVerdict := verdict
		candidateVerdict.IssueCodes = append([]ReviewIssueCodeV1{}, verdict.IssueCodes...)
		candidateVerdict.AffectedSlotIDs = append([]string{}, verdict.AffectedSlotIDs...)
		candidateRef := verdictRef
		mutate(&candidate, &candidateVerdict, &candidateRef)
		if err := candidate.ValidateRepairLineageV1(
			previous,
			candidateVerdict,
			candidateRef,
		); err == nil {
			t.Fatalf("invalid repair lineage %d accepted", index)
		}
	}
}

func TestCollaborationReviewVerdictV1HostBoundParseRoundTripAndBinding(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	contributionSetDigest := strings.Repeat("a", 64)
	raw := []byte(` {
  "schema_version":"collaboration-review-verdict/v1",
  "decision":"REPAIR_REQUIRED",
  "issue_codes":["CONTRADICTION","MISSING_EVIDENCE"],
  "affected_slot_ids":["slot.backend","slot.frontend"],
  "bounded_reason":"Resolve the contradiction.\nAttach exact evidence."
} `)
	frozen, canonical, err := ParseCollaborationReviewVerdictV1(
		raw,
		root.ManifestDigest,
		contributionSetDigest,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, canonical) || !bytes.Contains(
		canonical,
		[]byte(`"contribution_set_digest":"`+contributionSetDigest+`"`),
	) || !bytes.Contains(canonical, []byte(`"repair_round":0`)) {
		t.Fatalf("Host identity was not frozen into verdict: %s", canonical)
	}
	if err := frozen.ValidateForCollaborationReviewV1(
		root,
		contributionSetDigest,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCollaborationReviewVerdictV1(canonical); err != nil {
		t.Fatal(err)
	}
	raw[0] = '['
	if frozen.Decision != CollaborationReviewDecisionRepairRequiredV1 {
		t.Fatal("verdict aliases model input")
	}
}

func TestCanonicalCollaborationReviewModelVerdictV1ProjectsOnlySemanticWire(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	set := validCollaborationContributionSetV1(root)
	_, _, digest, err := NewCollaborationContributionSetV1(set)
	if err != nil {
		t.Fatal(err)
	}
	verdict := CollaborationReviewVerdictV1{
		SchemaVersion:         CollaborationReviewVerdictSchemaVersionV1,
		FamilyDigest:          root.ManifestDigest,
		ContributionSetDigest: digest,
		RepairRound:           0,
		Decision:              CollaborationReviewDecisionApproveV1,
		IssueCodes:            []ReviewIssueCodeV1{},
		AffectedSlotIDs:       []string{},
		BoundedReason:         "The contribution set is complete.",
	}
	modelCanonical, err := CanonicalCollaborationReviewModelVerdictV1(verdict)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"affected_slot_ids":[],"bounded_reason":"The contribution set is complete.","decision":"APPROVE","issue_codes":[],"schema_version":"collaboration-review-verdict/v1"}`
	if string(modelCanonical) != want ||
		bytes.Contains(modelCanonical, []byte(`"family_digest"`)) ||
		bytes.Contains(modelCanonical, []byte(`"contribution_set_digest"`)) ||
		bytes.Contains(modelCanonical, []byte(`"repair_round"`)) {
		t.Fatalf("model-owned verdict canonical=%s", modelCanonical)
	}
	parsed, hostCanonical, err := ParseCollaborationReviewVerdictV1(
		modelCanonical,
		root.ManifestDigest,
		digest,
		0,
	)
	if err != nil || !reflect.DeepEqual(parsed, verdict) {
		t.Fatalf("projected verdict did not parse back: %+v error=%v", parsed, err)
	}
	_, expectedHostCanonical, err := NewCollaborationReviewVerdictV1(verdict)
	if err != nil || !bytes.Equal(hostCanonical, expectedHostCanonical) {
		t.Fatal("projected verdict did not rebuild the exact Host-bound canonical")
	}
	invalid := verdict
	invalid.Decision = CollaborationReviewDecisionRepairRequiredV1
	if _, err := CanonicalCollaborationReviewModelVerdictV1(invalid); err == nil {
		t.Fatal("invalid Host-bound verdict was projected")
	}
}

func TestCollaborationReviewVerdictV1DecisionAndRoundRules(t *testing.T) {
	root := reviewerTestRootManifest(t, true, true)
	digest := strings.Repeat("b", 64)
	base := CollaborationReviewVerdictV1{
		SchemaVersion:         CollaborationReviewVerdictSchemaVersionV1,
		FamilyDigest:          root.ManifestDigest,
		ContributionSetDigest: digest,
		RepairRound:           0,
		Decision:              CollaborationReviewDecisionRepairRequiredV1,
		IssueCodes:            []ReviewIssueCodeV1{ReviewIssueMissingEvidenceV1},
		AffectedSlotIDs:       []string{"slot.backend"},
		BoundedReason:         "Evidence is missing.",
	}

	invalid := []func(*CollaborationReviewVerdictV1){
		func(value *CollaborationReviewVerdictV1) { value.RepairRound = 2 },
		func(value *CollaborationReviewVerdictV1) { value.RepairRound = 1 },
		func(value *CollaborationReviewVerdictV1) { value.IssueCodes = nil },
		func(value *CollaborationReviewVerdictV1) { value.AffectedSlotIDs = nil },
		func(value *CollaborationReviewVerdictV1) { value.Decision = "RETRY" },
		func(value *CollaborationReviewVerdictV1) { value.Decision = CollaborationReviewDecisionApproveV1 },
		func(value *CollaborationReviewVerdictV1) {
			value.Decision = CollaborationReviewDecisionRejectV1
			value.IssueCodes = []ReviewIssueCodeV1{}
		},
		func(value *CollaborationReviewVerdictV1) {
			value.IssueCodes = []ReviewIssueCodeV1{
				ReviewIssueMissingEvidenceV1,
				ReviewIssueContradictionV1,
			}
		},
		func(value *CollaborationReviewVerdictV1) { value.BoundedReason = "Cafe\u0301" },
		func(value *CollaborationReviewVerdictV1) { value.BoundedReason = "bad\tcontrol" },
	}
	for index, mutate := range invalid {
		value := base
		value.IssueCodes = append([]ReviewIssueCodeV1{}, base.IssueCodes...)
		value.AffectedSlotIDs = append([]string{}, base.AffectedSlotIDs...)
		mutate(&value)
		if _, _, err := NewCollaborationReviewVerdictV1(value); err == nil {
			t.Fatalf("invalid collaboration verdict %d accepted: %+v", index, value)
		}
	}

	for _, decision := range []CollaborationReviewDecisionV1{
		CollaborationReviewDecisionApproveV1,
		CollaborationReviewDecisionRejectV1,
	} {
		value := base
		value.RepairRound = 1
		value.Decision = decision
		if decision == CollaborationReviewDecisionApproveV1 {
			value.IssueCodes = []ReviewIssueCodeV1{}
			value.AffectedSlotIDs = []string{}
		}
		if _, _, err := NewCollaborationReviewVerdictV1(value); err != nil {
			t.Fatalf("round-one terminal %s rejected: %v", decision, err)
		}
	}
}

func TestParseCollaborationReviewVerdictV1RejectsDynamicEchoAndLegacyWire(
	t *testing.T,
) {
	root := reviewerTestRootManifest(t, true, true)
	digest := strings.Repeat("c", 64)
	model := []byte(`{"schema_version":"collaboration-review-verdict/v1","decision":"APPROVE","issue_codes":[],"affected_slot_ids":[],"bounded_reason":"The contribution set is complete."}`)
	withDynamicEcho := bytes.Replace(
		model,
		[]byte(`"decision"`),
		[]byte(`"family_digest":"`+root.ManifestDigest+`","decision"`),
		1,
	)
	duplicate := bytes.Replace(
		model,
		[]byte(`"decision":"APPROVE"`),
		[]byte(`"decision":"REJECT","decision":"APPROVE"`),
		1,
	)
	legacy := []byte(`{"schema_version":"review-verdict/v1","family_digest":"` + root.ManifestDigest + `","specialist_result_digest":"` + digest + `","decision":"APPROVE","issue_codes":[],"affected_slot_ids":[],"bounded_reason":"legacy"}`)
	for index, candidate := range [][]byte{
		withDynamicEcho,
		duplicate,
		append(bytes.Clone(model), []byte(` {}`)...),
		legacy,
	} {
		if _, _, err := ParseCollaborationReviewVerdictV1(
			candidate,
			root.ManifestDigest,
			digest,
			0,
		); err == nil {
			t.Fatalf("invalid collaboration model verdict %d accepted", index)
		}
	}
	if _, _, err := ParseReviewVerdictV1(model); err == nil {
		t.Fatal("legacy review-verdict/v1 parser accepted collaboration wire")
	}
	legacyVerdict, legacyCanonical, err := NewReviewVerdictV1(ReviewVerdictV1{
		SchemaVersion:          ReviewVerdictSchemaVersionV1,
		FamilyDigest:           root.ManifestDigest,
		SpecialistResultDigest: digest,
		Decision:               ReviewDecisionApproveV1,
		IssueCodes:             []ReviewIssueCodeV1{},
		AffectedSlotIDs:        []string{},
		BoundedReason:          "legacy",
	})
	if err != nil || legacyVerdict.Decision != ReviewDecisionApproveV1 ||
		!bytes.Contains(legacyCanonical, []byte(`"schema_version":"review-verdict/v1"`)) ||
		bytes.Contains(legacyCanonical, []byte(`"repair_round"`)) {
		t.Fatalf("legacy review verdict canary changed: %s err=%v", legacyCanonical, err)
	}
}

func TestCollaborationReviewVerdictV1ExactBindingAndUnknownSlot(t *testing.T) {
	root := reviewerTestRootManifest(t, true, true)
	digest := strings.Repeat("d", 64)
	verdict, canonical, err := ParseCollaborationReviewVerdictV1(
		[]byte(`{"schema_version":"collaboration-review-verdict/v1","decision":"APPROVE","issue_codes":[],"affected_slot_ids":[],"bounded_reason":"Complete."}`),
		root.ManifestDigest,
		digest,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, binding := range []struct {
		digest string
		round  uint32
	}{
		{strings.Repeat("e", 64), 1},
		{digest, 0},
	} {
		if err := verdict.ValidateForCollaborationReviewV1(
			root,
			binding.digest,
			binding.round,
		); err == nil {
			t.Fatalf("incorrect binding %d was accepted", index)
		}
	}
	if _, err := RestoreCollaborationReviewVerdictV1(
		append([]byte{' '}, canonical...),
	); err == nil {
		t.Fatal("non-canonical persisted collaboration verdict was restored")
	}

	unknownSlot := verdict
	unknownSlot.Decision = CollaborationReviewDecisionRejectV1
	unknownSlot.IssueCodes = []ReviewIssueCodeV1{ReviewIssueScopeMismatchV1}
	unknownSlot.AffectedSlotIDs = []string{"slot.unknown"}
	if err := unknownSlot.ValidateForCollaborationReviewV1(root, digest, 1); err == nil {
		t.Fatal("unknown Specialist slot was accepted")
	}
}

func validSpecialistContributionV1() SpecialistContributionV1 {
	return SpecialistContributionV1{
		SchemaVersion: SpecialistContributionSchemaVersionV1,
		Proposal:      "Add an idempotent retry guard.",
		Evidence: []SpecialistEvidenceV1{{
			Ref:          strings.Repeat("e", 64),
			BoundedClaim: "Trace e-1 reproduces the failure.",
		}},
		Assumptions: []string{"The API remains backward compatible."},
		Risks:       []string{"A stale lease can delay retry."},
		Conflicts:   []string{},
	}
}

func validCollaborationContributionSetV1(
	root RunManifest,
) CollaborationContributionSetV1 {
	return CollaborationContributionSetV1{
		SchemaVersion: CollaborationContributionSetSchemaVersionV1,
		FamilyDigest:  root.ManifestDigest,
		RepairRound:   0,
		Contributions: []CollaborationContributionEntryV1{
			{
				SlotID: "slot.backend", RunID: "run-backend",
				ResultRef:          strings.Repeat("1", 64),
				ContributionDigest: strings.Repeat("a", 64),
			},
			{
				SlotID: "slot.frontend", RunID: "run-frontend",
				ResultRef:          strings.Repeat("2", 64),
				ContributionDigest: strings.Repeat("b", 64),
			},
		},
	}
}

func cloneCollaborationContributionSetTestV1(
	input CollaborationContributionSetV1,
) CollaborationContributionSetV1 {
	cloned := input
	cloned.Contributions = append(
		[]CollaborationContributionEntryV1{},
		input.Contributions...,
	)
	return cloned
}
