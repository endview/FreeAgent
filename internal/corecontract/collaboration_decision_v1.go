package corecontract

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	SpecialistContributionSchemaVersionV1           = "specialist-contribution/v1"
	CollaborationContributionSetSchemaVersionV1     = "collaboration-contribution-set/v1"
	CollaborationReviewVerdictSchemaVersionV1       = "collaboration-review-verdict/v1"
	SpecialistContributionMaxProposalBytesV1        = 4096
	SpecialistContributionMaxItemsPerSectionV1      = 8
	SpecialistContributionMaxItemBytesV1            = 1024
	SpecialistContributionMaxCanonicalBytesV1       = 80 << 10
	CollaborationContributionSetMaxCanonicalBytesV1 = 32 << 10
	CollaborationReviewVerdictMaxReasonBytesV1      = 4096
	CollaborationReviewVerdictMaxCanonicalBytesV1   = 16 << 10

	specialistContributionDigestDomainV1       = "freeagent.specialist-contribution/v1"
	collaborationContributionSetDigestDomainV1 = "freeagent.collaboration-contribution-set/v1"
)

// SpecialistEvidenceV1 is a bounded claim attached to an existing immutable
// content reference. Ref is evidence identity only: it grants no read scope,
// transfer permission, capability, or other authority.
type SpecialistEvidenceV1 struct {
	Ref          string `json:"ref"`
	BoundedClaim string `json:"bounded_claim"`
}

// SpecialistContributionV1 is the complete, deliberately small wire emitted
// by one Specialist. Family, Run, Workspace, weight, and authority remain in
// the immutable contribution-set edge; untrusted model prose cannot rewrite
// them here. Every list must be present on the wire, including an empty list.
type SpecialistContributionV1 struct {
	SchemaVersion string                 `json:"schema_version"`
	Proposal      string                 `json:"proposal"`
	Evidence      []SpecialistEvidenceV1 `json:"evidence"`
	Assumptions   []string               `json:"assumptions"`
	Risks         []string               `json:"risks"`
	Conflicts     []string               `json:"conflicts"`
}

// NewSpecialistContributionV1 validates and freezes one Specialist output.
// List order is meaningful and is therefore preserved, while exact duplicate
// statements within one section fail closed.
func NewSpecialistContributionV1(
	input SpecialistContributionV1,
) (SpecialistContributionV1, []byte, string, error) {
	if input.SchemaVersion != SpecialistContributionSchemaVersionV1 {
		return SpecialistContributionV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist contribution schema version must be %q",
			SpecialistContributionSchemaVersionV1,
		)
	}
	if err := validateCollaborationTextV1(
		input.Proposal,
		SpecialistContributionMaxProposalBytesV1,
		"Specialist contribution proposal",
	); err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	// Nil distinguishes a missing or JSON null field from the required empty
	// array. This keeps the model wire closed and unambiguous.
	if input.Evidence == nil || input.Assumptions == nil ||
		input.Risks == nil || input.Conflicts == nil {
		return SpecialistContributionV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist contribution sections must all be explicit arrays",
		)
	}

	evidence, err := freezeSpecialistEvidenceV1(input.Evidence)
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	assumptions, err := freezeSpecialistContributionSectionV1(
		input.Assumptions,
		"assumptions",
	)
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	risks, err := freezeSpecialistContributionSectionV1(input.Risks, "risks")
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	conflicts, err := freezeSpecialistContributionSectionV1(
		input.Conflicts,
		"conflicts",
	)
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}

	frozen := input
	frozen.Evidence = evidence
	frozen.Assumptions = assumptions
	frozen.Risks = risks
	frozen.Conflicts = conflicts
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	if len(canonical) > SpecialistContributionMaxCanonicalBytesV1 {
		return SpecialistContributionV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist contribution exceeds %d canonical bytes",
			SpecialistContributionMaxCanonicalBytesV1,
		)
	}
	digest := moduleapi.Digest(specialistContributionDigestDomainV1, canonical)
	return frozen, canonical, digest, nil
}

// RestoreSpecialistContributionV1 accepts only the exact canonical wire.
func RestoreSpecialistContributionV1(
	canonical []byte,
	expectedDigest string,
) (SpecialistContributionV1, error) {
	if len(canonical) > SpecialistContributionMaxCanonicalBytesV1 {
		return SpecialistContributionV1{}, fmt.Errorf(
			"corecontract: Specialist contribution exceeds %d canonical bytes",
			SpecialistContributionMaxCanonicalBytesV1,
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return SpecialistContributionV1{}, err
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return SpecialistContributionV1{}, fmt.Errorf(
			"corecontract: invalid Specialist contribution digest",
		)
	}
	var decoded SpecialistContributionV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return SpecialistContributionV1{}, err
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err :=
		NewSpecialistContributionV1(decoded)
	if err != nil {
		return SpecialistContributionV1{}, err
	}
	if rebuiltDigest != expectedDigest ||
		!bytes.Equal(rebuiltCanonical, canonical) {
		return SpecialistContributionV1{}, fmt.Errorf(
			"corecontract: Specialist contribution is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ParseSpecialistContributionV1 accepts one bounded strict JSON object in any
// key order and returns the sole canonical fact. Unknown or duplicate fields,
// prefixes, suffixes, missing arrays, JSON null, and a second value fail.
func ParseSpecialistContributionV1(
	input []byte,
) (SpecialistContributionV1, []byte, string, error) {
	canonical, err := canonicalizeCollaborationModelObjectV1(
		input,
		SpecialistContributionMaxCanonicalBytesV1,
		"Specialist contribution",
	)
	if err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	var decoded SpecialistContributionV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return SpecialistContributionV1{}, nil, "", err
	}
	return NewSpecialistContributionV1(decoded)
}

// CollaborationContributionEntryV1 is one Host-owned edge from a frozen
// family slot and Child Run to the exact MODEL_RESULT and the strict
// specialist-contribution/v1 parsed from it. Neither digest grants authority.
type CollaborationContributionEntryV1 struct {
	SlotID             string `json:"slot_id"`
	RunID              string `json:"run_id"`
	ResultRef          string `json:"result_ref"`
	ContributionDigest string `json:"contribution_digest"`
}

// CollaborationContributionSetV1 preserves root-plan order. Round zero has
// no lineage. Round one is the sole repaired set and binds both the previous
// set digest and the exact Reviewer result that requested the repair.
type CollaborationContributionSetV1 struct {
	SchemaVersion     string                             `json:"schema_version"`
	FamilyDigest      string                             `json:"family_digest"`
	RepairRound       uint32                             `json:"repair_round"`
	PreviousSetDigest string                             `json:"previous_set_digest"`
	VerdictRef        string                             `json:"verdict_ref"`
	Contributions     []CollaborationContributionEntryV1 `json:"contributions"`
}

func NewCollaborationContributionSetV1(
	input CollaborationContributionSetV1,
) (CollaborationContributionSetV1, []byte, string, error) {
	if input.SchemaVersion != CollaborationContributionSetSchemaVersionV1 ||
		!moduleapi.ValidSHA256(input.FamilyDigest) ||
		input.RepairRound > 1 {
		return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid collaboration contribution-set identity",
		)
	}
	switch input.RepairRound {
	case 0:
		if input.PreviousSetDigest != "" || input.VerdictRef != "" {
			return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
				"corecontract: initial contribution set cannot carry repair lineage",
			)
		}
	case 1:
		if !moduleapi.ValidSHA256(input.PreviousSetDigest) ||
			!moduleapi.ValidSHA256(input.VerdictRef) {
			return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
				"corecontract: repaired contribution set requires previous set and verdict references",
			)
		}
	}
	if input.Contributions == nil ||
		len(input.Contributions) < CompositeMinChildrenV1 ||
		len(input.Contributions) > CompositeMaxChildrenV1 {
		return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
			"corecontract: collaboration contribution set requires between %d and %d ordered entries",
			CompositeMinChildrenV1,
			CompositeMaxChildrenV1,
		)
	}

	contributions := append(
		[]CollaborationContributionEntryV1{},
		input.Contributions...,
	)
	seenSlots := make(map[string]struct{}, len(contributions))
	seenRuns := make(map[string]struct{}, len(contributions))
	for index, contribution := range contributions {
		if !validOpaque(contribution.SlotID, maxOpaqueIDBytes) ||
			contribution.SlotID == CompositeReviewerParentSlotIDV1 ||
			!validOpaque(contribution.RunID, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(contribution.ResultRef) ||
			!moduleapi.ValidSHA256(contribution.ContributionDigest) {
			return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
				"corecontract: invalid collaboration contribution entry %d",
				index,
			)
		}
		if _, duplicate := seenSlots[contribution.SlotID]; duplicate {
			return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
				"corecontract: collaboration contribution set repeats a slot",
			)
		}
		if _, duplicate := seenRuns[contribution.RunID]; duplicate {
			return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
				"corecontract: collaboration contribution set repeats a Child Run",
			)
		}
		seenSlots[contribution.SlotID] = struct{}{}
		seenRuns[contribution.RunID] = struct{}{}
	}

	frozen := input
	frozen.Contributions = contributions
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return CollaborationContributionSetV1{}, nil, "", err
	}
	if len(canonical) > CollaborationContributionSetMaxCanonicalBytesV1 {
		return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
			"corecontract: collaboration contribution set exceeds %d canonical bytes",
			CollaborationContributionSetMaxCanonicalBytesV1,
		)
	}
	digest := moduleapi.Digest(
		collaborationContributionSetDigestDomainV1,
		canonical,
	)
	if frozen.RepairRound == 1 &&
		(frozen.PreviousSetDigest == digest || frozen.VerdictRef == digest) {
		return CollaborationContributionSetV1{}, nil, "", fmt.Errorf(
			"corecontract: repaired contribution set lineage cannot self-reference",
		)
	}
	return frozen, canonical, digest, nil
}

func RestoreCollaborationContributionSetV1(
	canonical []byte,
	expectedDigest string,
) (CollaborationContributionSetV1, error) {
	if len(canonical) > CollaborationContributionSetMaxCanonicalBytesV1 {
		return CollaborationContributionSetV1{}, fmt.Errorf(
			"corecontract: collaboration contribution set exceeds %d canonical bytes",
			CollaborationContributionSetMaxCanonicalBytesV1,
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return CollaborationContributionSetV1{}, err
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return CollaborationContributionSetV1{}, fmt.Errorf(
			"corecontract: invalid collaboration contribution-set digest",
		)
	}
	var decoded CollaborationContributionSetV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CollaborationContributionSetV1{}, err
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err :=
		NewCollaborationContributionSetV1(decoded)
	if err != nil {
		return CollaborationContributionSetV1{}, err
	}
	if rebuiltDigest != expectedDigest ||
		!bytes.Equal(rebuiltCanonical, canonical) {
		return CollaborationContributionSetV1{}, fmt.Errorf(
			"corecontract: collaboration contribution set is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ValidateForCollaborationRootV1 binds only the initial Host-owned set to the
// exact root plan without copying Agent, Profile, Workspace, focus, or weight
// fields. A round-one set must instead combine ValidateRepairLineageV1 with
// the pre-frozen repair Run refs introduced by W5-D1; accepting it here would
// let a repaired result masquerade as an original Specialist result.
func (set CollaborationContributionSetV1) ValidateForCollaborationRootV1(
	root RunManifest,
) error {
	if _, _, _, err := NewCollaborationContributionSetV1(set); err != nil {
		return err
	}
	if root.Composite == nil ||
		root.Composite.Role != CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		root.Composite.Plan.Reviewer == nil ||
		root.Composite.Plan.Decision == nil ||
		set.RepairRound != 0 ||
		set.FamilyDigest != root.ManifestDigest ||
		len(set.Contributions) != len(root.Composite.Plan.Children) {
		return fmt.Errorf(
			"corecontract: collaboration contribution set does not bind the frozen family",
		)
	}
	for index, planned := range root.Composite.Plan.Children {
		contribution := set.Contributions[index]
		if contribution.SlotID != planned.SlotID ||
			contribution.RunID != planned.RunID {
			return fmt.Errorf(
				"corecontract: collaboration contribution %d differs from root-plan order",
				index,
			)
		}
	}
	return nil
}

// ValidateForCollaborationRepairPlanV1 closes a valid round-one lineage to
// the exact pre-frozen repair Runs in the root decision plan. Unaffected slots
// remain bound to their original Runs; affected slots may only use the repair
// Run reserved for that same logical Specialist slot.
func (set CollaborationContributionSetV1) ValidateForCollaborationRepairPlanV1(
	root RunManifest,
	previous CollaborationContributionSetV1,
	verdict CollaborationReviewVerdictV1,
	verdictRef string,
) error {
	if root.Composite == nil ||
		root.Composite.Role != CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return fmt.Errorf(
			"corecontract: collaboration repair requires a frozen decision plan",
		)
	}
	if err := previous.ValidateForCollaborationRootV1(root); err != nil {
		return fmt.Errorf(
			"corecontract: initial collaboration contribution set: %w",
			err,
		)
	}
	if err := set.ValidateRepairLineageV1(previous, verdict, verdictRef); err != nil {
		return err
	}
	if len(set.Contributions) != len(root.Composite.Plan.Children) ||
		len(set.Contributions) !=
			len(root.Composite.Plan.Decision.RepairChildren) {
		return fmt.Errorf(
			"corecontract: collaboration repair does not match the frozen family cardinality",
		)
	}

	affected := make(map[string]struct{}, len(verdict.AffectedSlotIDs))
	for _, slotID := range verdict.AffectedSlotIDs {
		affected[slotID] = struct{}{}
	}
	for index, contribution := range set.Contributions {
		initial := root.Composite.Plan.Children[index]
		repair := root.Composite.Plan.Decision.RepairChildren[index]
		if contribution.SlotID != initial.SlotID ||
			repair.SlotID != initial.SlotID {
			return fmt.Errorf(
				"corecontract: collaboration repair slot %d differs from the frozen plan",
				index,
			)
		}
		if _, changed := affected[contribution.SlotID]; changed {
			if contribution.RunID != repair.RunID {
				return fmt.Errorf(
					"corecontract: affected collaboration slot %q did not use its pre-frozen repair Run",
					contribution.SlotID,
				)
			}
			continue
		}
		if contribution.RunID != initial.RunID {
			return fmt.Errorf(
				"corecontract: unaffected collaboration slot %q changed its original Run",
				contribution.SlotID,
			)
		}
	}
	return nil
}

// ValidateRepairLineageV1 proves the only allowed transition: an initial set
// plus a REPAIR_REQUIRED verdict becomes round one. Every affected slot keeps
// its slot identity but must change Run, result, and semantic contribution;
// every other entry is exact. This proves lineage only: W5-D1 must separately
// bind each changed RunID to the corresponding pre-frozen repair Run ref.
func (set CollaborationContributionSetV1) ValidateRepairLineageV1(
	previous CollaborationContributionSetV1,
	verdict CollaborationReviewVerdictV1,
	verdictRef string,
) error {
	if _, _, _, err := NewCollaborationContributionSetV1(set); err != nil {
		return err
	}
	_, _, previousDigest, err := NewCollaborationContributionSetV1(previous)
	if err != nil {
		return fmt.Errorf(
			"corecontract: previous collaboration contribution set: %w",
			err,
		)
	}
	if _, _, err := NewCollaborationReviewVerdictV1(verdict); err != nil {
		return err
	}
	if set.RepairRound != 1 || previous.RepairRound != 0 ||
		set.FamilyDigest != previous.FamilyDigest ||
		set.PreviousSetDigest != previousDigest ||
		!moduleapi.ValidSHA256(verdictRef) ||
		set.VerdictRef != verdictRef ||
		verdict.Decision != CollaborationReviewDecisionRepairRequiredV1 ||
		verdict.RepairRound != 0 ||
		verdict.FamilyDigest != previous.FamilyDigest ||
		verdict.ContributionSetDigest != previousDigest ||
		len(set.Contributions) != len(previous.Contributions) {
		return fmt.Errorf(
			"corecontract: invalid collaboration repair lineage identity",
		)
	}
	affected := make(map[string]struct{}, len(verdict.AffectedSlotIDs))
	for _, slot := range verdict.AffectedSlotIDs {
		affected[slot] = struct{}{}
	}
	seenAffected := 0
	for index, before := range previous.Contributions {
		after := set.Contributions[index]
		if after.SlotID != before.SlotID {
			return fmt.Errorf(
				"corecontract: repaired contribution %d changed slot lineage",
				index,
			)
		}
		if _, mustChange := affected[before.SlotID]; mustChange {
			seenAffected++
			if after.RunID == before.RunID ||
				after.ResultRef == before.ResultRef ||
				after.ContributionDigest == before.ContributionDigest {
				return fmt.Errorf(
					"corecontract: affected contribution %q did not change Run, result, and semantic contribution",
					before.SlotID,
				)
			}
			continue
		}
		if after != before {
			return fmt.Errorf(
				"corecontract: unaffected contribution %q changed during repair",
				before.SlotID,
			)
		}
	}
	if seenAffected != len(affected) {
		return fmt.Errorf(
			"corecontract: repair verdict names a slot outside the previous contribution set",
		)
	}
	return nil
}

type CollaborationReviewDecisionV1 string

const (
	CollaborationReviewDecisionApproveV1        CollaborationReviewDecisionV1 = "APPROVE"
	CollaborationReviewDecisionRejectV1         CollaborationReviewDecisionV1 = "REJECT"
	CollaborationReviewDecisionRepairRequiredV1 CollaborationReviewDecisionV1 = "REPAIR_REQUIRED"
)

// CollaborationReviewVerdictV1 is the W5 decision wire. It is distinct from
// review-verdict/v1 so the already-frozen S3-B protocol and bytes do not
// change. RepairRound is the round that produced the bound contribution set:
// zero is the initial set and one is the sole repaired set.
type CollaborationReviewVerdictV1 struct {
	SchemaVersion         string                        `json:"schema_version"`
	FamilyDigest          string                        `json:"family_digest"`
	ContributionSetDigest string                        `json:"contribution_set_digest"`
	RepairRound           uint32                        `json:"repair_round"`
	Decision              CollaborationReviewDecisionV1 `json:"decision"`
	IssueCodes            []ReviewIssueCodeV1           `json:"issue_codes"`
	AffectedSlotIDs       []string                      `json:"affected_slot_ids"`
	BoundedReason         string                        `json:"bounded_reason"`
}

func NewCollaborationReviewVerdictV1(
	input CollaborationReviewVerdictV1,
) (CollaborationReviewVerdictV1, []byte, error) {
	if input.SchemaVersion != CollaborationReviewVerdictSchemaVersionV1 ||
		!moduleapi.ValidSHA256(input.FamilyDigest) ||
		!moduleapi.ValidSHA256(input.ContributionSetDigest) ||
		input.RepairRound > 1 {
		return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: invalid collaboration ReviewVerdict identity",
		)
	}
	if err := validateCollaborationTextV1(
		input.BoundedReason,
		CollaborationReviewVerdictMaxReasonBytesV1,
		"collaboration ReviewVerdict reason",
	); err != nil {
		return CollaborationReviewVerdictV1{}, nil, err
	}
	if input.IssueCodes == nil || input.AffectedSlotIDs == nil {
		return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: collaboration ReviewVerdict issue and slot arrays must be explicit",
		)
	}
	if len(input.IssueCodes) > 6 ||
		len(input.AffectedSlotIDs) > CompositeMaxChildrenV1 {
		return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: collaboration ReviewVerdict arrays exceed their bounds",
		)
	}

	issues := append([]ReviewIssueCodeV1{}, input.IssueCodes...)
	for index, issue := range issues {
		if !validReviewIssueCodeV1(issue) ||
			(index > 0 && issues[index-1] >= issue) {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: collaboration ReviewVerdict issue codes must be supported, unique, and binary sorted",
			)
		}
	}
	slots := append([]string{}, input.AffectedSlotIDs...)
	for index, slot := range slots {
		if !validOpaque(slot, maxOpaqueIDBytes) ||
			slot == CompositeReviewerParentSlotIDV1 ||
			(index > 0 && slots[index-1] >= slot) {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: collaboration ReviewVerdict affected slots must be valid, unique, and binary sorted",
			)
		}
	}

	switch input.Decision {
	case CollaborationReviewDecisionApproveV1:
		if len(issues) != 0 || len(slots) != 0 {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: collaboration APPROVE cannot carry issues or affected slots",
			)
		}
	case CollaborationReviewDecisionRejectV1:
		if len(issues) == 0 || len(slots) == 0 {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: collaboration REJECT requires an issue and affected slot",
			)
		}
	case CollaborationReviewDecisionRepairRequiredV1:
		if input.RepairRound != 0 {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: repair round one cannot request another repair",
			)
		}
		if len(issues) == 0 || len(slots) == 0 {
			return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: REPAIR_REQUIRED requires an issue and affected slot",
			)
		}
	default:
		return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: unsupported collaboration ReviewVerdict decision %q",
			input.Decision,
		)
	}

	frozen := input
	frozen.IssueCodes = issues
	frozen.AffectedSlotIDs = slots
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return CollaborationReviewVerdictV1{}, nil, err
	}
	if len(canonical) > CollaborationReviewVerdictMaxCanonicalBytesV1 {
		return CollaborationReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: collaboration ReviewVerdict exceeds %d canonical bytes",
			CollaborationReviewVerdictMaxCanonicalBytesV1,
		)
	}
	return frozen, canonical, nil
}

func RestoreCollaborationReviewVerdictV1(
	canonical []byte,
) (CollaborationReviewVerdictV1, error) {
	if len(canonical) > CollaborationReviewVerdictMaxCanonicalBytesV1 {
		return CollaborationReviewVerdictV1{}, fmt.Errorf(
			"corecontract: collaboration ReviewVerdict exceeds %d canonical bytes",
			CollaborationReviewVerdictMaxCanonicalBytesV1,
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return CollaborationReviewVerdictV1{}, err
	}
	var decoded CollaborationReviewVerdictV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CollaborationReviewVerdictV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewCollaborationReviewVerdictV1(decoded)
	if err != nil {
		return CollaborationReviewVerdictV1{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) {
		return CollaborationReviewVerdictV1{}, fmt.Errorf(
			"corecontract: collaboration ReviewVerdict is not frozen canonically",
		)
	}
	return rebuilt, nil
}

type collaborationReviewModelVerdictV1 struct {
	SchemaVersion   string                        `json:"schema_version"`
	Decision        CollaborationReviewDecisionV1 `json:"decision"`
	IssueCodes      []ReviewIssueCodeV1           `json:"issue_codes"`
	AffectedSlotIDs []string                      `json:"affected_slot_ids"`
	BoundedReason   string                        `json:"bounded_reason"`
}

// CanonicalCollaborationReviewModelVerdictV1 projects one already Host-bound
// verdict back to the exact canonical semantic wire owned by the model. It is
// used to compare AssistantText without exposing family, contribution-set, or
// repair-round identity fields to the model contract.
func CanonicalCollaborationReviewModelVerdictV1(
	verdict CollaborationReviewVerdictV1,
) ([]byte, error) {
	frozen, _, err := NewCollaborationReviewVerdictV1(verdict)
	if err != nil {
		return nil, err
	}
	return canonicalJSON(collaborationReviewModelVerdictV1{
		SchemaVersion:   frozen.SchemaVersion,
		Decision:        frozen.Decision,
		IssueCodes:      append([]ReviewIssueCodeV1{}, frozen.IssueCodes...),
		AffectedSlotIDs: append([]string{}, frozen.AffectedSlotIDs...),
		BoundedReason:   frozen.BoundedReason,
	})
}

// ParseCollaborationReviewVerdictV1 parses only model-owned decision fields.
// Trusted Host identity is supplied out-of-band and frozen into the returned
// persisted verdict. A model that echoes or invents family/set/round fields is
// rejected as an unknown-field violation.
func ParseCollaborationReviewVerdictV1(
	input []byte,
	familyDigest string,
	contributionSetDigest string,
	repairRound uint32,
) (CollaborationReviewVerdictV1, []byte, error) {
	canonical, err := canonicalizeCollaborationModelObjectV1(
		input,
		CollaborationReviewVerdictMaxCanonicalBytesV1,
		"collaboration ReviewVerdict",
	)
	if err != nil {
		return CollaborationReviewVerdictV1{}, nil, err
	}
	var decoded collaborationReviewModelVerdictV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CollaborationReviewVerdictV1{}, nil, err
	}
	return NewCollaborationReviewVerdictV1(CollaborationReviewVerdictV1{
		SchemaVersion:         decoded.SchemaVersion,
		FamilyDigest:          familyDigest,
		ContributionSetDigest: contributionSetDigest,
		RepairRound:           repairRound,
		Decision:              decoded.Decision,
		IssueCodes:            decoded.IssueCodes,
		AffectedSlotIDs:       decoded.AffectedSlotIDs,
		BoundedReason:         decoded.BoundedReason,
	})
}

// ValidateForCollaborationReviewV1 binds a structurally valid verdict to the
// exact frozen Reviewer family, contribution-set digest, repair round, and
// planned Specialist slots. It grants no merge or repair authority.
func (verdict CollaborationReviewVerdictV1) ValidateForCollaborationReviewV1(
	root RunManifest,
	contributionSetDigest string,
	repairRound uint32,
) error {
	if _, _, err := NewCollaborationReviewVerdictV1(verdict); err != nil {
		return err
	}
	if root.Composite == nil ||
		root.Composite.Role != CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		root.Composite.Plan.Reviewer == nil ||
		root.Composite.Plan.Decision == nil ||
		!moduleapi.ValidSHA256(contributionSetDigest) ||
		repairRound > 1 ||
		verdict.FamilyDigest != root.ManifestDigest ||
		verdict.ContributionSetDigest != contributionSetDigest ||
		verdict.RepairRound != repairRound {
		return fmt.Errorf(
			"corecontract: collaboration ReviewVerdict does not bind the frozen family decision",
		)
	}
	allowed := make(map[string]struct{}, len(root.Composite.Plan.Children))
	for _, child := range root.Composite.Plan.Children {
		allowed[child.SlotID] = struct{}{}
	}
	for _, slot := range verdict.AffectedSlotIDs {
		if _, ok := allowed[slot]; !ok {
			return fmt.Errorf(
				"corecontract: collaboration ReviewVerdict names an unknown Specialist slot %q",
				slot,
			)
		}
	}
	return nil
}

func freezeSpecialistEvidenceV1(
	input []SpecialistEvidenceV1,
) ([]SpecialistEvidenceV1, error) {
	if len(input) > SpecialistContributionMaxItemsPerSectionV1 {
		return nil, fmt.Errorf(
			"corecontract: Specialist contribution evidence exceeds %d items",
			SpecialistContributionMaxItemsPerSectionV1,
		)
	}
	frozen := append([]SpecialistEvidenceV1{}, input...)
	seenRefs := make(map[string]struct{}, len(frozen))
	seenClaims := make(map[string]struct{}, len(frozen))
	for index, evidence := range frozen {
		if !moduleapi.ValidSHA256(evidence.Ref) {
			return nil, fmt.Errorf(
				"corecontract: Specialist contribution evidence %d has an invalid reference",
				index,
			)
		}
		if err := validateCollaborationTextV1(
			evidence.BoundedClaim,
			SpecialistContributionMaxItemBytesV1,
			fmt.Sprintf("Specialist contribution evidence claim %d", index),
		); err != nil {
			return nil, err
		}
		if _, duplicate := seenRefs[evidence.Ref]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: Specialist contribution evidence repeats a reference",
			)
		}
		if _, duplicate := seenClaims[evidence.BoundedClaim]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: Specialist contribution evidence repeats a claim",
			)
		}
		seenRefs[evidence.Ref] = struct{}{}
		seenClaims[evidence.BoundedClaim] = struct{}{}
	}
	return frozen, nil
}

func freezeSpecialistContributionSectionV1(
	input []string,
	section string,
) ([]string, error) {
	if len(input) > SpecialistContributionMaxItemsPerSectionV1 {
		return nil, fmt.Errorf(
			"corecontract: Specialist contribution %s exceeds %d items",
			section,
			SpecialistContributionMaxItemsPerSectionV1,
		)
	}
	frozen := append([]string{}, input...)
	seen := make(map[string]struct{}, len(frozen))
	for index, item := range frozen {
		if err := validateCollaborationTextV1(
			item,
			SpecialistContributionMaxItemBytesV1,
			fmt.Sprintf("Specialist contribution %s item %d", section, index),
		); err != nil {
			return nil, err
		}
		if _, duplicate := seen[item]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: Specialist contribution %s repeats an item",
				section,
			)
		}
		seen[item] = struct{}{}
	}
	return frozen, nil
}

func validateCollaborationTextV1(value string, maximum int, label string) error {
	if value == "" ||
		len(value) > maximum ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"corecontract: %s must be non-empty trimmed Unicode NFC and at most %d bytes",
			label,
			maximum,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' {
			return fmt.Errorf(
				"corecontract: %s can contain normalized LF but no other control characters",
				label,
			)
		}
	}
	return nil
}

func canonicalizeCollaborationModelObjectV1(
	input []byte,
	maximum int,
	label string,
) ([]byte, error) {
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 4,
			MaxNodes: 128,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("corecontract: parse %s: %w", label, err)
	}
	if len(canonical) == 0 || canonical[0] != '{' || len(canonical) > maximum {
		return nil, fmt.Errorf(
			"corecontract: %s must be one bounded JSON object",
			label,
		)
	}
	return canonical, nil
}
