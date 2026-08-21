package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CompositeSpecialistResultSetSchemaVersionV1 = "composite-specialist-result-set/v1"
	ReviewVerdictSchemaVersionV1                = "review-verdict/v1"
	ReviewVerdictMaxReasonBytesV1               = 4096

	compositeSpecialistResultSetDigestDomainV1 = "freeagent.composite-specialist-result-set/v1"
)

type ReviewDecisionV1 string

const (
	ReviewDecisionApproveV1 ReviewDecisionV1 = "APPROVE"
	ReviewDecisionRejectV1  ReviewDecisionV1 = "REJECT"
)

type ReviewIssueCodeV1 string

const (
	ReviewIssueContradictionV1      ReviewIssueCodeV1 = "CONTRADICTION"
	ReviewIssueMissingEvidenceV1    ReviewIssueCodeV1 = "MISSING_EVIDENCE"
	ReviewIssueScopeMismatchV1      ReviewIssueCodeV1 = "SCOPE_MISMATCH"
	ReviewIssueUnsupportedClaimV1   ReviewIssueCodeV1 = "UNSUPPORTED_CLAIM"
	ReviewIssueIncompleteCoverageV1 ReviewIssueCodeV1 = "INCOMPLETE_COVERAGE"
	ReviewIssueSecurityConcernV1    ReviewIssueCodeV1 = "SECURITY_CONCERN"
)

// CompositeSpecialistResultV1 binds one complete terminal Specialist result
// without copying its MODEL_RESULT bytes into a second fact.
type CompositeSpecialistResultV1 struct {
	SlotID                string `json:"slot_id"`
	FocusID               string `json:"focus_id"`
	WeightBasisPoints     uint32 `json:"weight_basis_points"`
	RunID                 string `json:"run_id"`
	ManifestDigest        string `json:"manifest_digest"`
	MemberSnapshotDigest  string `json:"member_snapshot_digest"`
	ResultRef             string `json:"result_ref"`
	TerminalRunRevision   uint64 `json:"terminal_run_revision"`
	TerminalFrameRevision uint64 `json:"terminal_frame_revision"`
}

// CompositeSpecialistResultSetV1 follows the frozen root-plan order. Its
// separate domain digest is the value a Reviewer verdict must bind.
type CompositeSpecialistResultSetV1 struct {
	SchemaVersion string                        `json:"schema_version"`
	FamilyDigest  string                        `json:"family_digest"`
	TaskInputRef  string                        `json:"task_input_ref"`
	Results       []CompositeSpecialistResultV1 `json:"results"`
}

func NewCompositeSpecialistResultSetV1(
	input CompositeSpecialistResultSetV1,
) (CompositeSpecialistResultSetV1, []byte, string, error) {
	if input.SchemaVersion != CompositeSpecialistResultSetSchemaVersionV1 {
		return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist result-set schema version must be %q",
			CompositeSpecialistResultSetSchemaVersionV1,
		)
	}
	if !moduleapi.ValidSHA256(input.FamilyDigest) ||
		!moduleapi.ValidSHA256(input.TaskInputRef) {
		return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist result set requires family and task digests",
		)
	}
	if len(input.Results) < CompositeMinChildrenV1 ||
		len(input.Results) > CompositeMaxChildrenV1 {
		return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist result set requires between %d and %d results",
			CompositeMinChildrenV1,
			CompositeMaxChildrenV1,
		)
	}
	results := append([]CompositeSpecialistResultV1(nil), input.Results...)
	seenSlots := make(map[string]struct{}, len(results))
	seenFocus := make(map[string]struct{}, len(results))
	seenRuns := make(map[string]struct{}, len(results))
	seenManifests := make(map[string]struct{}, len(results))
	seenMembers := make(map[string]struct{}, len(results))
	var totalWeight uint64
	for index, result := range results {
		assignment := CompositeAssignmentV1{
			SlotID:            result.SlotID,
			FocusID:           result.FocusID,
			WeightBasisPoints: result.WeightBasisPoints,
		}
		if err := assignment.Validate(); err != nil {
			return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
				"corecontract: Specialist result %d assignment: %w", index, err,
			)
		}
		if !validOpaque(result.RunID, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(result.ManifestDigest) ||
			!moduleapi.ValidSHA256(result.MemberSnapshotDigest) ||
			!moduleapi.ValidSHA256(result.ResultRef) ||
			result.TerminalRunRevision == 0 ||
			result.TerminalRunRevision > maximumJSONSafeIntegerV1 ||
			result.TerminalFrameRevision == 0 ||
			result.TerminalFrameRevision > maximumJSONSafeIntegerV1 {
			return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
				"corecontract: invalid Specialist result %d identity", index,
			)
		}
		for label, value := range map[string]string{
			"slot": result.SlotID, "focus": result.FocusID,
			"Run": result.RunID, "Manifest": result.ManifestDigest,
			"member": result.MemberSnapshotDigest,
		} {
			var seen map[string]struct{}
			switch label {
			case "slot":
				seen = seenSlots
			case "focus":
				seen = seenFocus
			case "Run":
				seen = seenRuns
			case "Manifest":
				seen = seenManifests
			case "member":
				seen = seenMembers
			default:
				seen = seenMembers
			}
			if _, duplicate := seen[value]; duplicate {
				return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
					"corecontract: duplicate Specialist result %s", label,
				)
			}
			seen[value] = struct{}{}
		}
		totalWeight += uint64(result.WeightBasisPoints)
	}
	if totalWeight != CompositeWeightBasisPointsV1 {
		return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist result weights sum to %d, want %d",
			totalWeight,
			CompositeWeightBasisPointsV1,
		)
	}
	if !sort.SliceIsSorted(results, func(left, right int) bool {
		if results[left].WeightBasisPoints != results[right].WeightBasisPoints {
			return results[left].WeightBasisPoints > results[right].WeightBasisPoints
		}
		return results[left].SlotID < results[right].SlotID
	}) {
		return CompositeSpecialistResultSetV1{}, nil, "", fmt.Errorf(
			"corecontract: Specialist results do not follow root-plan order",
		)
	}
	frozen := input
	frozen.Results = results
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return CompositeSpecialistResultSetV1{}, nil, "", err
	}
	digest := moduleapi.Digest(
		compositeSpecialistResultSetDigestDomainV1,
		canonical,
	)
	return frozen, canonical, digest, nil
}

func RestoreCompositeSpecialistResultSetV1(
	canonical []byte,
	expectedDigest string,
) (CompositeSpecialistResultSetV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return CompositeSpecialistResultSetV1{}, err
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return CompositeSpecialistResultSetV1{}, fmt.Errorf(
			"corecontract: invalid Specialist result-set digest",
		)
	}
	var decoded CompositeSpecialistResultSetV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CompositeSpecialistResultSetV1{}, err
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err :=
		NewCompositeSpecialistResultSetV1(decoded)
	if err != nil {
		return CompositeSpecialistResultSetV1{}, err
	}
	if rebuiltDigest != expectedDigest || !bytes.Equal(rebuiltCanonical, canonical) {
		return CompositeSpecialistResultSetV1{}, fmt.Errorf(
			"corecontract: Specialist result set is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ValidateForCompositeReviewV1 binds the dynamic terminal result set to the
// immutable Specialist edges in one exact root Manifest.
func (set CompositeSpecialistResultSetV1) ValidateForCompositeReviewV1(
	root RunManifest,
) error {
	if root.Composite == nil ||
		root.Composite.Role != CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		root.Composite.Plan.Reviewer == nil ||
		set.FamilyDigest != root.ManifestDigest ||
		set.TaskInputRef != root.TaskInputRef ||
		len(set.Results) != len(root.Composite.Plan.Children) {
		return fmt.Errorf(
			"corecontract: Specialist result set does not bind the frozen family",
		)
	}
	for index, planned := range root.Composite.Plan.Children {
		result := set.Results[index]
		if result.SlotID != planned.SlotID ||
			result.FocusID != planned.Assignment.FocusID ||
			result.WeightBasisPoints != planned.Assignment.WeightBasisPoints ||
			result.RunID != planned.RunID ||
			result.MemberSnapshotDigest != planned.MemberSnapshotDigest {
			return fmt.Errorf(
				"corecontract: Specialist result %d differs from the root plan",
				index,
			)
		}
	}
	return nil
}

// ReviewVerdictV1 is untrusted model data with a deliberately small and
// closed wire. External family closure is checked separately.
type ReviewVerdictV1 struct {
	SchemaVersion          string              `json:"schema_version"`
	FamilyDigest           string              `json:"family_digest"`
	SpecialistResultDigest string              `json:"specialist_result_digest"`
	Decision               ReviewDecisionV1    `json:"decision"`
	IssueCodes             []ReviewIssueCodeV1 `json:"issue_codes"`
	AffectedSlotIDs        []string            `json:"affected_slot_ids"`
	BoundedReason          string              `json:"bounded_reason"`
}

func NewReviewVerdictV1(
	input ReviewVerdictV1,
) (ReviewVerdictV1, []byte, error) {
	if input.SchemaVersion != ReviewVerdictSchemaVersionV1 ||
		!moduleapi.ValidSHA256(input.FamilyDigest) ||
		!moduleapi.ValidSHA256(input.SpecialistResultDigest) {
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: invalid ReviewVerdict identity",
		)
	}
	if input.BoundedReason == "" ||
		len(input.BoundedReason) > ReviewVerdictMaxReasonBytesV1 ||
		!utf8.ValidString(input.BoundedReason) ||
		input.BoundedReason != moduleapi.CanonicalText(input.BoundedReason) {
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: ReviewVerdict reason must be non-empty NFC and at most %d bytes",
			ReviewVerdictMaxReasonBytesV1,
		)
	}
	if len(input.IssueCodes) > 6 || len(input.AffectedSlotIDs) > CompositeMaxChildrenV1 {
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: ReviewVerdict arrays exceed their bounds",
		)
	}
	issues := append([]ReviewIssueCodeV1{}, input.IssueCodes...)
	for index, issue := range issues {
		if !validReviewIssueCodeV1(issue) ||
			(index != 0 && issues[index-1] >= issue) {
			return ReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: ReviewVerdict issue codes must be supported, unique, and binary sorted",
			)
		}
	}
	slots := append([]string{}, input.AffectedSlotIDs...)
	for index, slot := range slots {
		if !validOpaque(slot, maxOpaqueIDBytes) ||
			slot == CompositeReviewerParentSlotIDV1 ||
			(index != 0 && slots[index-1] >= slot) {
			return ReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: ReviewVerdict affected slots must be valid, unique, and binary sorted",
			)
		}
	}
	switch input.Decision {
	case ReviewDecisionApproveV1:
		if len(issues) != 0 || len(slots) != 0 {
			return ReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: APPROVE cannot carry issues or affected slots",
			)
		}
	case ReviewDecisionRejectV1:
		if len(issues) == 0 || len(slots) == 0 {
			return ReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: REJECT requires an issue and affected slot",
			)
		}
	default:
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: unsupported ReviewVerdict decision %q",
			input.Decision,
		)
	}
	frozen := input
	frozen.IssueCodes = issues
	frozen.AffectedSlotIDs = slots
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return ReviewVerdictV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreReviewVerdictV1(canonical []byte) (ReviewVerdictV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ReviewVerdictV1{}, err
	}
	var decoded ReviewVerdictV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ReviewVerdictV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewReviewVerdictV1(decoded)
	if err != nil {
		return ReviewVerdictV1{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) {
		return ReviewVerdictV1{}, fmt.Errorf(
			"corecontract: ReviewVerdict is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ParseReviewVerdictV1 accepts one strict JSON object in any key order and
// rebuilds the canonical bytes that become the sole review fact. Unknown
// fields, prefixes, suffixes, code fences, and a second JSON value fail.
func ParseReviewVerdictV1(
	input []byte,
) (ReviewVerdictV1, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var decoded ReviewVerdictV1
	if err := decoder.Decode(&decoded); err != nil {
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: decode ReviewVerdict: %w", err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ReviewVerdictV1{}, nil, fmt.Errorf(
				"corecontract: ReviewVerdict contains a second JSON value",
			)
		}
		return ReviewVerdictV1{}, nil, fmt.Errorf(
			"corecontract: ReviewVerdict contains trailing data: %w", err,
		)
	}
	return NewReviewVerdictV1(decoded)
}

// ValidateForCompositeReviewV1 binds a structurally valid verdict to one
// exact root plan and result-set digest.
func (verdict ReviewVerdictV1) ValidateForCompositeReviewV1(
	root RunManifest,
	specialistResultDigest string,
) error {
	if root.Composite == nil ||
		root.Composite.Role != CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		root.Composite.Plan.Reviewer == nil ||
		verdict.FamilyDigest != root.ManifestDigest ||
		verdict.SpecialistResultDigest != specialistResultDigest {
		return fmt.Errorf(
			"corecontract: ReviewVerdict does not bind the frozen family",
		)
	}
	allowed := make(map[string]struct{}, len(root.Composite.Plan.Children))
	for _, child := range root.Composite.Plan.Children {
		allowed[child.SlotID] = struct{}{}
	}
	for _, slot := range verdict.AffectedSlotIDs {
		if _, ok := allowed[slot]; !ok {
			return fmt.Errorf(
				"corecontract: ReviewVerdict names an unknown Specialist slot %q",
				slot,
			)
		}
	}
	return nil
}

func validReviewIssueCodeV1(issue ReviewIssueCodeV1) bool {
	switch issue {
	case ReviewIssueContradictionV1,
		ReviewIssueMissingEvidenceV1,
		ReviewIssueScopeMismatchV1,
		ReviewIssueUnsupportedClaimV1,
		ReviewIssueIncompleteCoverageV1,
		ReviewIssueSecurityConcernV1:
		return true
	default:
		return false
	}
}
