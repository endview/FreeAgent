package corecontract

import (
	"fmt"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CancellationScopeRunV1       = "run"
	CancellationScopeFamilyV1    = "family"
	CancellationScopeInheritedV1 = "inherited"

	CompositeRunNodeSchemaVersionV1      = "composite-run-node/v1"
	CompositeDecisionPlanSchemaVersionV1 = "composite-decision-plan/v1"
	CompositeMergeLogicalStepIDV1        = "composite.merge/v1"
	CompositeReviewLogicalStepIDV1       = "composite.review/v1"
	CompositeReviewerParentSlotIDV1      = "__reviewer__"
	CompositeReviewerPolicyResultsGateV1 = "RESULTS_GATE"
	CompositeReviewerMaxOutputTokensV1   = uint32(1024)
	CompositeRepairRoundOneV1            = uint32(1)
	CompositeWeightBasisPointsV1         = uint64(10000)
	CompositeMinChildrenV1               = 2
	CompositeMaxChildrenV1               = 8
)

// CompositeRunRoleV1 distinguishes the coordinator Run from one specialist
// Child. Every Run remains a normal, exactly-one-member Run.
type CompositeRunRoleV1 string

const (
	CompositeRunRoleRootV1     CompositeRunRoleV1 = "ROOT"
	CompositeRunRoleChildV1    CompositeRunRoleV1 = "CHILD"
	CompositeRunRoleReviewerV1 CompositeRunRoleV1 = "REVIEWER"
)

// ValidateCancellationScopeV1 closes the previously opaque cancellation
// string. Pure Runs use run; composite roots use family; their children use
// inherited.
func ValidateCancellationScopeV1(scope string) error {
	switch scope {
	case CancellationScopeRunV1,
		CancellationScopeFamilyV1,
		CancellationScopeInheritedV1:
		return nil
	default:
		return fmt.Errorf("corecontract: unsupported cancellation scope %q", scope)
	}
}

// CompositeAssignmentV1 is immutable focus metadata. It is not authority and
// never participates in Provider routing.
type CompositeAssignmentV1 struct {
	SlotID            string `json:"slot_id"`
	FocusID           string `json:"focus_id"`
	WeightBasisPoints uint32 `json:"weight_basis_points"`
}

func (assignment CompositeAssignmentV1) Validate() error {
	if !validOpaque(assignment.SlotID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid composite slot ID")
	}
	if !validOpaque(assignment.FocusID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid composite focus ID")
	}
	if assignment.WeightBasisPoints == 0 ||
		uint64(assignment.WeightBasisPoints) > CompositeWeightBasisPointsV1 {
		return fmt.Errorf(
			"corecontract: composite weight must be between 1 and %d basis points",
			CompositeWeightBasisPointsV1,
		)
	}
	return nil
}

// CompositeChildRunRefV1 is the root Manifest's complete immutable plan edge.
// It intentionally omits the Child Manifest digest so the Child can bind the
// already-frozen Parent digest without forming a digest cycle.
type CompositeChildRunRefV1 struct {
	SlotID               string                   `json:"slot_id"`
	ParentSlotID         string                   `json:"parent_slot_id,omitempty"`
	RunID                string                   `json:"run_id"`
	AdmissionKey         string                   `json:"admission_key"`
	MemberSnapshotDigest string                   `json:"member_snapshot_digest"`
	Agent                AgentRef                 `json:"agent"`
	Profile              ProfileRef               `json:"profile"`
	TaskInputRef         string                   `json:"task_input_ref"`
	Assignment           CompositeAssignmentV1    `json:"assignment"`
	Transfer             *WorkspaceTransferPlanV1 `json:"transfer,omitempty"`
}

// CompositeReviewerRunRefV1 is the optional review edge frozen by the root.
// It has no Specialist weight or assignment.
type CompositeReviewerRunRefV1 struct {
	ParentSlotID         string     `json:"parent_slot_id,omitempty"`
	RunID                string     `json:"run_id"`
	AdmissionKey         string     `json:"admission_key"`
	MemberSnapshotDigest string     `json:"member_snapshot_digest"`
	Agent                AgentRef   `json:"agent"`
	Profile              ProfileRef `json:"profile"`
	TaskInputRef         string     `json:"task_input_ref"`
	ReviewLogicalStepID  string     `json:"review_logical_step_id"`
	Policy               string     `json:"policy"`
	MaxOutputTokens      uint32     `json:"max_output_tokens"`
}

// CompositeDecisionPlanV1 is the explicit W5 decision extension. Its
// presence opts a family into structured Specialist contributions and freezes
// every possible round-one Run before execution. The only repair round is
// fixed by this protocol rather than configured by untrusted model output.
type CompositeDecisionPlanV1 struct {
	SchemaVersion  string                    `json:"schema_version"`
	RepairChildren []CompositeChildRunRefV1  `json:"repair_children"`
	RepairReviewer CompositeReviewerRunRefV1 `json:"repair_reviewer"`
}

// CompositeRunPlanV1 is a bounded, one-level fan-out plan. The model dispatch
// budget is statically allocated: one call per Child and one coordinator merge.
type CompositeRunPlanV1 struct {
	MergeLogicalStepID       string                     `json:"merge_logical_step_id"`
	FamilyModelDispatchLimit uint32                     `json:"family_model_dispatch_limit"`
	Children                 []CompositeChildRunRefV1   `json:"children"`
	Reviewer                 *CompositeReviewerRunRefV1 `json:"reviewer,omitempty"`
	Decision                 *CompositeDecisionPlanV1   `json:"decision,omitempty"`
}

// CompositeRunNodeV1 is the optional family identity embedded in RunManifest.
// ROOT carries Plan; CHILD carries the exact parent digest and assignment.
type CompositeRunNodeV1 struct {
	SchemaVersion        string                 `json:"schema_version"`
	Role                 CompositeRunRoleV1     `json:"role"`
	RepairRound          uint32                 `json:"repair_round,omitempty"`
	RootRunID            string                 `json:"root_run_id"`
	ParentManifestDigest string                 `json:"parent_manifest_digest,omitempty"`
	ParentSlotID         string                 `json:"parent_slot_id,omitempty"`
	Assignment           *CompositeAssignmentV1 `json:"assignment,omitempty"`
	Plan                 *CompositeRunPlanV1    `json:"plan,omitempty"`
}

func freezeCompositeRunNodeV1(
	manifest RunManifest,
) (*CompositeRunNodeV1, error) {
	if err := ValidateCancellationScopeV1(manifest.CancellationScope); err != nil {
		return nil, err
	}
	if manifest.Composite == nil {
		if manifest.ParentRunID != "" {
			return nil, fmt.Errorf(
				"corecontract: a ParentRunID requires composite-run-node/v1",
			)
		}
		if manifest.CancellationScope != CancellationScopeRunV1 {
			return nil, fmt.Errorf(
				"corecontract: a non-composite Run requires run cancellation scope",
			)
		}
		return nil, nil
	}

	input := *manifest.Composite
	if input.SchemaVersion != CompositeRunNodeSchemaVersionV1 {
		return nil, fmt.Errorf(
			"corecontract: composite Run node schema version must be %q",
			CompositeRunNodeSchemaVersionV1,
		)
	}
	if !validOpaque(input.RootRunID, maxOpaqueIDBytes) {
		return nil, fmt.Errorf("corecontract: invalid composite root Run ID")
	}

	switch input.Role {
	case CompositeRunRoleRootV1:
		if manifest.ParentRunID != "" || input.RootRunID != manifest.RunID ||
			input.RepairRound != 0 ||
			input.ParentManifestDigest != "" || input.ParentSlotID != "" ||
			input.Assignment != nil || input.Plan == nil ||
			manifest.CancellationScope != CancellationScopeFamilyV1 {
			return nil, fmt.Errorf(
				"corecontract: invalid composite ROOT identity or cancellation scope",
			)
		}
		plan, err := freezeCompositeRunPlanV1(*input.Plan, manifest)
		if err != nil {
			return nil, err
		}
		input.Plan = &plan
	case CompositeRunRoleChildV1:
		if manifest.ParentRunID == "" ||
			manifest.ParentRunID == manifest.RunID ||
			input.RepairRound > CompositeRepairRoundOneV1 ||
			input.RootRunID != manifest.ParentRunID ||
			!moduleapi.ValidSHA256(input.ParentManifestDigest) ||
			!validOpaque(input.ParentSlotID, maxOpaqueIDBytes) ||
			input.Assignment == nil || input.Plan != nil ||
			manifest.CancellationScope != CancellationScopeInheritedV1 {
			return nil, fmt.Errorf(
				"corecontract: invalid composite CHILD identity or cancellation scope",
			)
		}
		assignment := *input.Assignment
		if err := assignment.Validate(); err != nil {
			return nil, err
		}
		if input.RepairRound == 0 {
			if assignment.SlotID != input.ParentSlotID {
				return nil, fmt.Errorf(
					"corecontract: composite Child assignment does not match parent slot",
				)
			}
		} else if assignment.SlotID == input.ParentSlotID ||
			input.ParentSlotID == CompositeReviewerParentSlotIDV1 {
			return nil, fmt.Errorf(
				"corecontract: repaired composite Child requires a distinct physical parent slot",
			)
		}
		input.Assignment = &assignment
	case CompositeRunRoleReviewerV1:
		if manifest.ParentRunID == "" ||
			manifest.ParentRunID == manifest.RunID ||
			input.RepairRound > CompositeRepairRoundOneV1 ||
			input.RootRunID != manifest.ParentRunID ||
			!moduleapi.ValidSHA256(input.ParentManifestDigest) ||
			!validOpaque(input.ParentSlotID, maxOpaqueIDBytes) ||
			input.Assignment != nil || input.Plan != nil ||
			manifest.CancellationScope != CancellationScopeInheritedV1 {
			return nil, fmt.Errorf(
				"corecontract: invalid composite REVIEWER identity or cancellation scope",
			)
		}
		if (input.RepairRound == 0 &&
			input.ParentSlotID != CompositeReviewerParentSlotIDV1) ||
			(input.RepairRound == CompositeRepairRoundOneV1 &&
				input.ParentSlotID == CompositeReviewerParentSlotIDV1) {
			return nil, fmt.Errorf(
				"corecontract: composite Reviewer round does not match its physical parent slot",
			)
		}
	default:
		return nil, fmt.Errorf(
			"corecontract: unsupported composite Run role %q",
			input.Role,
		)
	}
	return &input, nil
}

func freezeCompositeRunPlanV1(
	input CompositeRunPlanV1,
	manifest RunManifest,
) (CompositeRunPlanV1, error) {
	if input.MergeLogicalStepID != CompositeMergeLogicalStepIDV1 {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: composite merge logical step must be %q",
			CompositeMergeLogicalStepIDV1,
		)
	}
	if len(input.Children) < CompositeMinChildrenV1 ||
		len(input.Children) > CompositeMaxChildrenV1 {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: composite plan requires between %d and %d children",
			CompositeMinChildrenV1,
			CompositeMaxChildrenV1,
		)
	}
	expectedDispatches := uint64(len(input.Children)) + 1
	if input.Reviewer != nil {
		expectedDispatches++
	}
	if input.Decision != nil {
		if input.Reviewer == nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite decision plan requires the initial Reviewer",
			)
		}
		expectedDispatches += uint64(len(input.Children)) + 1
	}
	if uint64(input.FamilyModelDispatchLimit) != expectedDispatches {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: composite model-dispatch budget does not match Specialists, optional Reviewer, and merge",
		)
	}

	children := cloneCompositeChildRunRefsV1(input.Children)
	seenSlots := make(map[string]struct{}, len(children))
	seenRuns := make(map[string]struct{}, len(children))
	seenAdmissions := make(map[string]struct{}, len(children))
	var weight uint64
	hasWorkspaceTransfer := false
	for index, child := range children {
		if !validOpaque(child.SlotID, maxOpaqueIDBytes) || child.ParentSlotID != "" ||
			!validOpaque(child.RunID, maxOpaqueIDBytes) ||
			child.RunID == manifest.RunID ||
			!validOpaque(child.AdmissionKey, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(child.MemberSnapshotDigest) ||
			!moduleapi.ValidSHA256(child.TaskInputRef) {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: invalid composite Child ref %d",
				index,
			)
		}
		if err := child.Agent.Validate(); err != nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Child %d Agent: %w", index, err,
			)
		}
		if err := child.Profile.Validate(); err != nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Child %d Profile: %w", index, err,
			)
		}
		if err := child.Assignment.Validate(); err != nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Child %d assignment: %w", index, err,
			)
		}
		if child.SlotID != child.Assignment.SlotID ||
			child.TaskInputRef != manifest.TaskInputRef {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Child %d slot or TaskInput does not close",
				index,
			)
		}
		if child.SlotID == CompositeReviewerParentSlotIDV1 {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Child %d uses the reserved Reviewer slot",
				index,
			)
		}
		if child.Transfer != nil {
			hasWorkspaceTransfer = true
			transfer := *child.Transfer
			if err := transfer.Validate(); err != nil ||
				transfer.RootWorkspace != manifest.Workspace {
				return CompositeRunPlanV1{}, fmt.Errorf(
					"corecontract: composite Child %d has an invalid Workspace transfer plan",
					index,
				)
			}
			children[index].Transfer = &transfer
		}
		if _, duplicate := seenSlots[child.SlotID]; duplicate {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: duplicate composite slot %q", child.SlotID,
			)
		}
		if _, duplicate := seenRuns[child.RunID]; duplicate {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: duplicate composite Child Run %q", child.RunID,
			)
		}
		if _, duplicate := seenAdmissions[child.AdmissionKey]; duplicate {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: duplicate composite Child AdmissionKey %q",
				child.AdmissionKey,
			)
		}
		seenSlots[child.SlotID] = struct{}{}
		seenRuns[child.RunID] = struct{}{}
		seenAdmissions[child.AdmissionKey] = struct{}{}
		weight += uint64(child.Assignment.WeightBasisPoints)
	}
	if weight != CompositeWeightBasisPointsV1 {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: composite Child weights sum to %d, want %d",
			weight,
			CompositeWeightBasisPointsV1,
		)
	}
	if hasWorkspaceTransfer && input.Decision == nil {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: Workspace transfer requires a composite decision plan",
		)
	}
	if !sort.SliceIsSorted(children, func(left, right int) bool {
		leftWeight := children[left].Assignment.WeightBasisPoints
		rightWeight := children[right].Assignment.WeightBasisPoints
		if leftWeight != rightWeight {
			return leftWeight > rightWeight
		}
		return children[left].SlotID < children[right].SlotID
	}) {
		return CompositeRunPlanV1{}, fmt.Errorf(
			"corecontract: composite Children must be ordered by descending weight then slot ID",
		)
	}
	var reviewer *CompositeReviewerRunRefV1
	if input.Reviewer != nil {
		frozen := *input.Reviewer
		if frozen.ParentSlotID != "" ||
			!validOpaque(frozen.RunID, maxOpaqueIDBytes) ||
			frozen.RunID == manifest.RunID ||
			!validOpaque(frozen.AdmissionKey, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(frozen.MemberSnapshotDigest) ||
			!moduleapi.ValidSHA256(frozen.TaskInputRef) ||
			frozen.TaskInputRef != manifest.TaskInputRef ||
			frozen.ReviewLogicalStepID != CompositeReviewLogicalStepIDV1 ||
			frozen.Policy != CompositeReviewerPolicyResultsGateV1 ||
			frozen.MaxOutputTokens == 0 ||
			frozen.MaxOutputTokens > CompositeReviewerMaxOutputTokensV1 {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: invalid composite Reviewer ref",
			)
		}
		if err := frozen.Agent.Validate(); err != nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Reviewer Agent: %w", err,
			)
		}
		if err := frozen.Profile.Validate(); err != nil {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Reviewer Profile: %w", err,
			)
		}
		if _, duplicate := seenRuns[frozen.RunID]; duplicate {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Reviewer duplicates a Child Run",
			)
		}
		if _, duplicate := seenAdmissions[frozen.AdmissionKey]; duplicate {
			return CompositeRunPlanV1{}, fmt.Errorf(
				"corecontract: composite Reviewer duplicates a Child AdmissionKey",
			)
		}
		seenRuns[frozen.RunID] = struct{}{}
		seenAdmissions[frozen.AdmissionKey] = struct{}{}
		reviewer = &frozen
	}
	decision, err := freezeCompositeDecisionPlanV1(
		input.Decision,
		manifest,
		children,
		reviewer,
		seenRuns,
		seenAdmissions,
	)
	if err != nil {
		return CompositeRunPlanV1{}, err
	}
	return CompositeRunPlanV1{
		MergeLogicalStepID:       input.MergeLogicalStepID,
		FamilyModelDispatchLimit: input.FamilyModelDispatchLimit,
		Children:                 children,
		Reviewer:                 reviewer,
		Decision:                 decision,
	}, nil
}

func freezeCompositeDecisionPlanV1(
	input *CompositeDecisionPlanV1,
	manifest RunManifest,
	children []CompositeChildRunRefV1,
	reviewer *CompositeReviewerRunRefV1,
	seenRuns map[string]struct{},
	seenAdmissions map[string]struct{},
) (*CompositeDecisionPlanV1, error) {
	if input == nil {
		return nil, nil
	}
	if input.SchemaVersion != CompositeDecisionPlanSchemaVersionV1 ||
		reviewer == nil || len(input.RepairChildren) != len(children) {
		return nil, fmt.Errorf(
			"corecontract: invalid composite decision plan identity or cardinality",
		)
	}

	repairChildren := cloneCompositeChildRunRefsV1(input.RepairChildren)
	seenParentSlots := make(map[string]struct{}, len(children)*2+2)
	seenMemberDigests := make(map[string]struct{}, len(children)*2+2)
	for _, child := range children {
		seenParentSlots[child.SlotID] = struct{}{}
		seenMemberDigests[child.MemberSnapshotDigest] = struct{}{}
	}
	seenParentSlots[CompositeReviewerParentSlotIDV1] = struct{}{}
	seenMemberDigests[reviewer.MemberSnapshotDigest] = struct{}{}

	for index, repair := range repairChildren {
		initial := children[index]
		if !validOpaque(repair.ParentSlotID, maxOpaqueIDBytes) ||
			repair.ParentSlotID == repair.SlotID ||
			!validOpaque(repair.RunID, maxOpaqueIDBytes) ||
			repair.RunID == manifest.RunID ||
			!validOpaque(repair.AdmissionKey, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(repair.MemberSnapshotDigest) ||
			!moduleapi.ValidSHA256(repair.TaskInputRef) ||
			repair.SlotID != initial.SlotID ||
			repair.Agent != initial.Agent ||
			repair.Profile != initial.Profile ||
			repair.TaskInputRef != initial.TaskInputRef ||
			repair.Assignment != initial.Assignment ||
			!equalWorkspaceTransferPlanV1(repair.Transfer, initial.Transfer) {
			return nil, fmt.Errorf(
				"corecontract: repair Child %d does not preserve its frozen Specialist slot and routing",
				index,
			)
		}
		if _, duplicate := seenParentSlots[repair.ParentSlotID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate composite physical parent slot %q",
				repair.ParentSlotID,
			)
		}
		if _, duplicate := seenRuns[repair.RunID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate composite repair Child Run %q",
				repair.RunID,
			)
		}
		if _, duplicate := seenAdmissions[repair.AdmissionKey]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate composite repair Child AdmissionKey %q",
				repair.AdmissionKey,
			)
		}
		if _, duplicate := seenMemberDigests[repair.MemberSnapshotDigest]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate composite repair Child member snapshot",
			)
		}
		seenParentSlots[repair.ParentSlotID] = struct{}{}
		seenRuns[repair.RunID] = struct{}{}
		seenAdmissions[repair.AdmissionKey] = struct{}{}
		seenMemberDigests[repair.MemberSnapshotDigest] = struct{}{}
	}

	repairReviewer := input.RepairReviewer
	if !validOpaque(repairReviewer.ParentSlotID, maxOpaqueIDBytes) ||
		!validOpaque(repairReviewer.RunID, maxOpaqueIDBytes) ||
		repairReviewer.RunID == manifest.RunID ||
		!validOpaque(repairReviewer.AdmissionKey, maxOpaqueIDBytes) ||
		!moduleapi.ValidSHA256(repairReviewer.MemberSnapshotDigest) ||
		repairReviewer.Agent != reviewer.Agent ||
		repairReviewer.Profile != reviewer.Profile ||
		repairReviewer.TaskInputRef != reviewer.TaskInputRef ||
		repairReviewer.ReviewLogicalStepID != reviewer.ReviewLogicalStepID ||
		repairReviewer.Policy != reviewer.Policy ||
		repairReviewer.MaxOutputTokens != reviewer.MaxOutputTokens {
		return nil, fmt.Errorf(
			"corecontract: repair Reviewer does not preserve the initial Reviewer routing and policy",
		)
	}
	if _, duplicate := seenParentSlots[repairReviewer.ParentSlotID]; duplicate {
		return nil, fmt.Errorf(
			"corecontract: duplicate composite repair Reviewer physical parent slot",
		)
	}
	if _, duplicate := seenRuns[repairReviewer.RunID]; duplicate {
		return nil, fmt.Errorf(
			"corecontract: duplicate composite repair Reviewer Run",
		)
	}
	if _, duplicate := seenAdmissions[repairReviewer.AdmissionKey]; duplicate {
		return nil, fmt.Errorf(
			"corecontract: duplicate composite repair Reviewer AdmissionKey",
		)
	}
	if _, duplicate := seenMemberDigests[repairReviewer.MemberSnapshotDigest]; duplicate {
		return nil, fmt.Errorf(
			"corecontract: duplicate composite repair Reviewer member snapshot",
		)
	}

	return &CompositeDecisionPlanV1{
		SchemaVersion:  input.SchemaVersion,
		RepairChildren: repairChildren,
		RepairReviewer: repairReviewer,
	}, nil
}

func cloneCompositeRunNodeV1(input *CompositeRunNodeV1) *CompositeRunNodeV1 {
	if input == nil {
		return nil
	}
	cloned := *input
	if input.Assignment != nil {
		assignment := *input.Assignment
		cloned.Assignment = &assignment
	}
	if input.Plan != nil {
		plan := *input.Plan
		plan.Children = cloneCompositeChildRunRefsV1(input.Plan.Children)
		if input.Plan.Reviewer != nil {
			reviewer := *input.Plan.Reviewer
			plan.Reviewer = &reviewer
		}
		if input.Plan.Decision != nil {
			decision := *input.Plan.Decision
			decision.RepairChildren = cloneCompositeChildRunRefsV1(
				input.Plan.Decision.RepairChildren,
			)
			plan.Decision = &decision
		}
		cloned.Plan = &plan
	}
	return &cloned
}

func cloneCompositeChildRunRefsV1(
	input []CompositeChildRunRefV1,
) []CompositeChildRunRefV1 {
	if input == nil {
		return nil
	}
	cloned := make([]CompositeChildRunRefV1, len(input))
	for index, child := range input {
		cloned[index] = child
		if child.Transfer != nil {
			transfer := *child.Transfer
			cloned[index].Transfer = &transfer
		}
	}
	return cloned
}

func equalWorkspaceTransferPlanV1(
	left *WorkspaceTransferPlanV1,
	right *WorkspaceTransferPlanV1,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
