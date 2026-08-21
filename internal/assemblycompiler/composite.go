package assemblycompiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	compositeChildAdmissionKeyDomain          = "freeagent.composite-child-admission-key/v1"
	compositeChildRunIDDomain                 = "freeagent.composite-child-run-id/v1"
	compositeChildMemberIDDomain              = "freeagent.composite-child-member-id/v1"
	compositeChildRecoveryRootDomain          = "freeagent.composite-child-recovery-root/v1"
	compositeReviewerAdmissionKeyDomain       = "freeagent.composite-reviewer-admission-key/v1"
	compositeReviewerRunIDDomain              = "freeagent.composite-reviewer-run-id/v1"
	compositeReviewerMemberIDDomain           = "freeagent.composite-reviewer-member-id/v1"
	compositeReviewerRecoveryRootDomain       = "freeagent.composite-reviewer-recovery-root/v1"
	compositeRepairChildAdmissionKeyDomain    = "freeagent.composite-repair-child-admission-key/v1"
	compositeRepairChildRunIDDomain           = "freeagent.composite-repair-child-run-id/v1"
	compositeRepairChildMemberIDDomain        = "freeagent.composite-repair-child-member-id/v1"
	compositeRepairChildRecoveryRootDomain    = "freeagent.composite-repair-child-recovery-root/v1"
	compositeRepairChildParentSlotDomain      = "freeagent.composite-repair-child-parent-slot/v1"
	compositeRepairReviewerAdmissionKeyDomain = "freeagent.composite-repair-reviewer-admission-key/v1"
	compositeRepairReviewerRunIDDomain        = "freeagent.composite-repair-reviewer-run-id/v1"
	compositeRepairReviewerMemberIDDomain     = "freeagent.composite-repair-reviewer-member-id/v1"
	compositeRepairReviewerRecoveryRootDomain = "freeagent.composite-repair-reviewer-recovery-root/v1"
	compositeRepairReviewerParentSlotDomain   = "freeagent.composite-repair-reviewer-parent-slot/v1"

	compositeChildAdmissionKeyPrefix          = "composite-child-admission-"
	compositeChildRunIDPrefix                 = "composite-child-run-"
	compositeChildMemberIDPrefix              = "composite-child-member-"
	compositeChildRecoveryRootPrefix          = "composite-child-recovery-"
	compositeReviewerAdmissionKeyPrefix       = "composite-reviewer-admission-"
	compositeReviewerRunIDPrefix              = "composite-reviewer-run-"
	compositeReviewerMemberIDPrefix           = "composite-reviewer-member-"
	compositeReviewerRecoveryRootPrefix       = "composite-reviewer-recovery-"
	compositeRepairChildAdmissionKeyPrefix    = "composite-repair-child-admission-"
	compositeRepairChildRunIDPrefix           = "composite-repair-child-run-"
	compositeRepairChildMemberIDPrefix        = "composite-repair-child-member-"
	compositeRepairChildRecoveryRootPrefix    = "composite-repair-child-recovery-"
	compositeRepairChildParentSlotPrefix      = "__repair_child_r1__"
	compositeRepairReviewerAdmissionKeyPrefix = "composite-repair-reviewer-admission-"
	compositeRepairReviewerRunIDPrefix        = "composite-repair-reviewer-run-"
	compositeRepairReviewerMemberIDPrefix     = "composite-repair-reviewer-member-"
	compositeRepairReviewerRecoveryRootPrefix = "composite-repair-reviewer-recovery-"
	compositeRepairReviewerParentSlotPrefix   = "__repair_reviewer_r1__"
)

var compositeModelGeneratePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameModelGenerate,
	ExactVersion: moduleapi.PortVersionV1,
}

// CompositeCompileInput has exactly one caller-supplied identity: Parent.
// Every Child identity and intent is deterministically derived from the
// Parent intent digest and the frozen Control slot.
type CompositeCompileInput struct {
	Parent CompileInput
}

// CompositeChildCompileOutput is one final Child assembly plus the exact
// derived AdmissionIntent bytes needed by a later atomic family admission.
type CompositeChildCompileOutput struct {
	CompileOutput
	Assignment      corecontract.CompositeAssignmentV1
	IntentCanonical []byte
	IntentDigest    string
}

// CompositeReviewerCompileOutput is the optional, weight-free Reviewer
// assembly. Its identities are derived independently from Specialist slots.
type CompositeReviewerCompileOutput struct {
	CompileOutput
	IntentCanonical []byte
	IntentDigest    string
}

// CompositeCompileOutput is a bounded, one-level family. Children use the
// exact descending-weight/SlotID order frozen into the Parent plan.
type CompositeCompileOutput struct {
	Parent   CompileOutput
	Children []CompositeChildCompileOutput
	Reviewer *CompositeReviewerCompileOutput
	Decision *CompositeDecisionCompileOutput
}

// CompositeDecisionCompileOutput is present only for the explicit W5
// decision protocol. Every possible repair participant is compiled and
// frozen during the original family admission, but remains dormant until the
// persisted round-zero verdict selects its affected slots.
type CompositeDecisionCompileOutput struct {
	RepairChildren []CompositeChildCompileOutput
	RepairReviewer CompositeReviewerCompileOutput
}

type compositeChildIdentitySeedV1 struct {
	ParentIntentDigest string                                `json:"parent_intent_digest"`
	SlotID             string                                `json:"slot_id"`
	Transfer           *corecontract.WorkspaceTransferPlanV1 `json:"transfer,omitempty"`
}

type compositeRepairIdentitySeedV1 struct {
	ParentIntentDigest string                                `json:"parent_intent_digest"`
	SlotID             string                                `json:"slot_id,omitempty"`
	RepairRound        uint32                                `json:"repair_round"`
	Transfer           *corecontract.WorkspaceTransferPlanV1 `json:"transfer,omitempty"`
}

type compositeChildIDsV1 struct {
	AdmissionKey    string
	RunID           string
	MemberID        string
	RecoveryRootRef string
}

type preparedCompositeChildV1 struct {
	assignment      corecontract.CompositeAssignmentV1
	transfer        *corecontract.WorkspaceTransferPlanV1
	parentSlotID    string
	repairRound     uint32
	intent          corecontract.AdmissionIntentV1
	intentCanonical []byte
	intentDigest    string
	compiledMember  CompileOutput
}

type resolvedCompositeMemberV1 struct {
	definition controlcontract.CompositeAgentMemberV1
	workspace  controlcontract.WorkspaceDefinition
	transfer   *corecontract.WorkspaceTransferPlanV1
}

type preparedCompositeReviewerV1 struct {
	definition      controlcontract.CompositeReviewerDefinitionV1
	parentSlotID    string
	repairRound     uint32
	intent          corecontract.AdmissionIntentV1
	intentCanonical []byte
	intentDigest    string
	compiledMember  CompileOutput
}

type preparedCompositeDecisionV1 struct {
	children []preparedCompositeChildV1
	reviewer preparedCompositeReviewerV1
}

// CompileCompositeFamily compiles one coordinator and its exact Control-owned
// Child set. It remains a pure compiler: no Child ID is accepted from the
// caller and no Store, Scheduler, Runtime, or Provider-selection policy is
// introduced here.
func (compiler Compiler) CompileCompositeFamily(
	ctx context.Context,
	input CompositeCompileInput,
) (CompositeCompileOutput, error) {
	if ctx == nil {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: context is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return CompositeCompileOutput{}, err
	}
	parentIntent, err := corecontract.RestoreAdmissionIntentV1(
		bytes.Clone(input.Parent.IntentCanonical),
		input.Parent.IntentDigest,
	)
	if err != nil {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: restore composite Parent admission intent: %w",
			err,
		)
	}
	if parentIntent.CancellationScope != corecontract.CancellationScopeFamilyV1 {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: composite Parent cancellation scope must be %q",
			corecontract.CancellationScopeFamilyV1,
		)
	}
	if err := input.Parent.PublishedBasis.Validate(); err != nil {
		return CompositeCompileOutput{}, err
	}
	if parentIntent.TenantID != input.Parent.PublishedBasis.TenantID {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: composite Parent tenant does not match published basis",
		)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		bytes.Clone(input.Parent.ControlCanonical),
		input.Parent.PublishedBasis.Control,
	)
	if err != nil {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: restore composite Control snapshot: %w",
			err,
		)
	}
	if control.TenantID != parentIntent.TenantID {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: composite Control tenant does not match Parent intent",
		)
	}
	definition, found := control.FindCompositeAgent(parentIntent.AgentID)
	if !found {
		return CompositeCompileOutput{}, fmt.Errorf(
			"%w: agent %q has no frozen composite definition",
			ErrCapabilityNotAvailable,
			parentIntent.AgentID,
		)
	}
	if definition.CoordinatorProfileID != parentIntent.ProfileID {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: composite coordinator profile %q does not match Parent profile %q",
			definition.CoordinatorProfileID,
			parentIntent.ProfileID,
		)
	}
	rootWorkspace, found := control.FindWorkspace(parentIntent.WorkspaceID)
	if !found {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: composite root Workspace %q is absent",
			parentIntent.WorkspaceID,
		)
	}

	members := append(
		[]controlcontract.CompositeAgentMemberV1(nil),
		definition.Members...,
	)
	sort.Slice(members, func(left, right int) bool {
		if members[left].WeightBasisPoints != members[right].WeightBasisPoints {
			return members[left].WeightBasisPoints > members[right].WeightBasisPoints
		}
		return members[left].SlotID < members[right].SlotID
	})
	resolvedMembers := make([]resolvedCompositeMemberV1, len(members))
	for index, member := range members {
		profile, present := control.FindProfile(member.ProfileID)
		if !present {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: composite Child slot %q profile %q is absent",
				member.SlotID,
				member.ProfileID,
			)
		}
		if err := rejectCompositeChildSideEffectPorts(member.SlotID, profile); err != nil {
			return CompositeCompileOutput{}, err
		}
		if _, present := control.FindAgent(member.AgentID); !present {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: composite Child slot %q agent %q is absent",
				member.SlotID,
				member.AgentID,
			)
		}
		targetWorkspace := rootWorkspace
		var transfer *corecontract.WorkspaceTransferPlanV1
		if member.TargetWorkspaceID != "" &&
			member.TargetWorkspaceID != rootWorkspace.Workspace.ID {
			if definition.Decision == nil {
				return CompositeCompileOutput{}, fmt.Errorf(
					"%w: cross-Workspace composite Child slot %q requires the explicit decision protocol",
					ErrCapabilityNotAvailable,
					member.SlotID,
				)
			}
			target, present := control.FindWorkspace(member.TargetWorkspaceID)
			if !present {
				return CompositeCompileOutput{}, fmt.Errorf(
					"assemblycompiler: composite Child slot %q target Workspace %q is absent",
					member.SlotID,
					member.TargetWorkspaceID,
				)
			}
			plan, resolveErr := resolveCompositeWorkspaceTransferV1(
				rootWorkspace,
				target,
			)
			if resolveErr != nil {
				return CompositeCompileOutput{}, fmt.Errorf(
					"assemblycompiler: composite Child slot %q Workspace transfer: %w",
					member.SlotID,
					resolveErr,
				)
			}
			targetWorkspace = target
			transfer = &plan
		}
		resolvedMembers[index] = resolvedCompositeMemberV1{
			definition: member,
			workspace:  targetWorkspace,
			transfer:   cloneWorkspaceTransferPlanV1(transfer),
		}
	}
	if definition.Reviewer != nil {
		profile, present := control.FindProfile(definition.Reviewer.ProfileID)
		if !present {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: composite Reviewer profile %q is absent",
				definition.Reviewer.ProfileID,
			)
		}
		if err := rejectCompositeChildSideEffectPorts(
			corecontract.CompositeReviewerParentSlotIDV1,
			profile,
		); err != nil {
			return CompositeCompileOutput{}, err
		}
		if _, present := control.FindAgent(definition.Reviewer.AgentID); !present {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: composite Reviewer agent %q is absent",
				definition.Reviewer.AgentID,
			)
		}
	}

	preparedChildren := make([]preparedCompositeChildV1, len(resolvedMembers))
	for index, resolved := range resolvedMembers {
		member := resolved.definition
		ids, deriveErr := deriveCompositeChildIDsV1(
			input.Parent.IntentDigest,
			member.SlotID,
			resolved.transfer,
		)
		if deriveErr != nil {
			return CompositeCompileOutput{}, deriveErr
		}
		childIntent, childIntentCanonical, childIntentDigest, freezeErr :=
			corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
				SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
				TenantID:          parentIntent.TenantID,
				AdmissionKey:      ids.AdmissionKey,
				PrincipalID:       parentIntent.PrincipalID,
				WorkspaceID:       resolved.workspace.Workspace.ID,
				AgentID:           member.AgentID,
				ProfileID:         member.ProfileID,
				TaskInputRef:      parentIntent.TaskInputRef,
				RequestedPorts:    []moduleapi.PortRef{compositeModelGeneratePortV1},
				Deadline:          parentIntent.Deadline,
				CancellationScope: corecontract.CancellationScopeInheritedV1,
				ExplicitLimits:    bytes.Clone(parentIntent.ExplicitLimits),
			})
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite Child slot %q intent: %w",
				member.SlotID,
				freezeErr,
			)
		}
		childInput := input.Parent
		childInput.RunID = ids.RunID
		childInput.MemberID = ids.MemberID
		childInput.RecoveryRootRef = ids.RecoveryRootRef
		childInput.ActionMaterializer = nil
		childInput.ActionBindingMaterials = nil
		compiled, compileErr := compileMemberWithRunCancellationV1(
			ctx,
			compiler,
			childInput,
			childIntent,
		)
		if compileErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: compile composite Child slot %q: %w",
				member.SlotID,
				compileErr,
			)
		}
		assignment := corecontract.CompositeAssignmentV1{
			SlotID:            member.SlotID,
			FocusID:           member.FocusID,
			WeightBasisPoints: member.WeightBasisPoints,
		}
		preparedChildren[index] = preparedCompositeChildV1{
			assignment:      assignment,
			transfer:        cloneWorkspaceTransferPlanV1(resolved.transfer),
			intent:          childIntent,
			intentCanonical: bytes.Clone(childIntentCanonical),
			intentDigest:    childIntentDigest,
			compiledMember:  compiled,
		}
	}

	var preparedReviewer *preparedCompositeReviewerV1
	if definition.Reviewer != nil {
		ids, deriveErr := deriveCompositeReviewerIDsV1(input.Parent.IntentDigest)
		if deriveErr != nil {
			return CompositeCompileOutput{}, deriveErr
		}
		reviewerIntent, reviewerIntentCanonical, reviewerIntentDigest, freezeErr :=
			corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
				SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
				TenantID:          parentIntent.TenantID,
				AdmissionKey:      ids.AdmissionKey,
				PrincipalID:       parentIntent.PrincipalID,
				WorkspaceID:       parentIntent.WorkspaceID,
				AgentID:           definition.Reviewer.AgentID,
				ProfileID:         definition.Reviewer.ProfileID,
				TaskInputRef:      parentIntent.TaskInputRef,
				RequestedPorts:    []moduleapi.PortRef{compositeModelGeneratePortV1},
				Deadline:          parentIntent.Deadline,
				CancellationScope: corecontract.CancellationScopeInheritedV1,
				ExplicitLimits:    bytes.Clone(parentIntent.ExplicitLimits),
			})
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite Reviewer intent: %w",
				freezeErr,
			)
		}
		reviewerInput := input.Parent
		reviewerInput.RunID = ids.RunID
		reviewerInput.MemberID = ids.MemberID
		reviewerInput.RecoveryRootRef = ids.RecoveryRootRef
		reviewerInput.ActionMaterializer = nil
		reviewerInput.ActionBindingMaterials = nil
		compiled, compileErr := compileMemberWithRunCancellationV1(
			ctx,
			compiler,
			reviewerInput,
			reviewerIntent,
		)
		if compileErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: compile composite Reviewer: %w",
				compileErr,
			)
		}
		preparedReviewer = &preparedCompositeReviewerV1{
			definition:      *definition.Reviewer,
			parentSlotID:    corecontract.CompositeReviewerParentSlotIDV1,
			intent:          reviewerIntent,
			intentCanonical: bytes.Clone(reviewerIntentCanonical),
			intentDigest:    reviewerIntentDigest,
			compiledMember:  compiled,
		}
	}

	var preparedDecision *preparedCompositeDecisionV1
	if definition.Decision != nil {
		if preparedReviewer == nil || definition.Reviewer == nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: composite Decision requires the frozen Reviewer",
			)
		}
		repairChildren := make([]preparedCompositeChildV1, len(resolvedMembers))
		for index, resolved := range resolvedMembers {
			member := resolved.definition
			ids, parentSlotID, deriveErr := deriveCompositeRepairChildIDsV1(
				input.Parent.IntentDigest,
				member.SlotID,
				resolved.transfer,
			)
			if deriveErr != nil {
				return CompositeCompileOutput{}, deriveErr
			}
			repairIntent, repairIntentCanonical, repairIntentDigest, freezeErr :=
				corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
					SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
					TenantID:          parentIntent.TenantID,
					AdmissionKey:      ids.AdmissionKey,
					PrincipalID:       parentIntent.PrincipalID,
					WorkspaceID:       resolved.workspace.Workspace.ID,
					AgentID:           member.AgentID,
					ProfileID:         member.ProfileID,
					TaskInputRef:      parentIntent.TaskInputRef,
					RequestedPorts:    []moduleapi.PortRef{compositeModelGeneratePortV1},
					Deadline:          parentIntent.Deadline,
					CancellationScope: corecontract.CancellationScopeInheritedV1,
					ExplicitLimits:    bytes.Clone(parentIntent.ExplicitLimits),
				})
			if freezeErr != nil {
				return CompositeCompileOutput{}, fmt.Errorf(
					"assemblycompiler: freeze composite repair Child slot %q intent: %w",
					member.SlotID,
					freezeErr,
				)
			}
			repairInput := input.Parent
			repairInput.RunID = ids.RunID
			repairInput.MemberID = ids.MemberID
			repairInput.RecoveryRootRef = ids.RecoveryRootRef
			repairInput.ActionMaterializer = nil
			repairInput.ActionBindingMaterials = nil
			compiled, compileErr := compileMemberWithRunCancellationV1(
				ctx,
				compiler,
				repairInput,
				repairIntent,
			)
			if compileErr != nil {
				return CompositeCompileOutput{}, fmt.Errorf(
					"assemblycompiler: compile composite repair Child slot %q: %w",
					member.SlotID,
					compileErr,
				)
			}
			repairChildren[index] = preparedCompositeChildV1{
				assignment: corecontract.CompositeAssignmentV1{
					SlotID:            member.SlotID,
					FocusID:           member.FocusID,
					WeightBasisPoints: member.WeightBasisPoints,
				},
				transfer:        cloneWorkspaceTransferPlanV1(resolved.transfer),
				parentSlotID:    parentSlotID,
				repairRound:     corecontract.CompositeRepairRoundOneV1,
				intent:          repairIntent,
				intentCanonical: bytes.Clone(repairIntentCanonical),
				intentDigest:    repairIntentDigest,
				compiledMember:  compiled,
			}
		}

		repairReviewerIDs, repairReviewerParentSlotID, deriveErr :=
			deriveCompositeRepairReviewerIDsV1(input.Parent.IntentDigest)
		if deriveErr != nil {
			return CompositeCompileOutput{}, deriveErr
		}
		repairReviewerIntent, repairReviewerIntentCanonical,
			repairReviewerIntentDigest, freezeErr :=
			corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
				SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
				TenantID:          parentIntent.TenantID,
				AdmissionKey:      repairReviewerIDs.AdmissionKey,
				PrincipalID:       parentIntent.PrincipalID,
				WorkspaceID:       parentIntent.WorkspaceID,
				AgentID:           definition.Reviewer.AgentID,
				ProfileID:         definition.Reviewer.ProfileID,
				TaskInputRef:      parentIntent.TaskInputRef,
				RequestedPorts:    []moduleapi.PortRef{compositeModelGeneratePortV1},
				Deadline:          parentIntent.Deadline,
				CancellationScope: corecontract.CancellationScopeInheritedV1,
				ExplicitLimits:    bytes.Clone(parentIntent.ExplicitLimits),
			})
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite repair Reviewer intent: %w",
				freezeErr,
			)
		}
		repairReviewerInput := input.Parent
		repairReviewerInput.RunID = repairReviewerIDs.RunID
		repairReviewerInput.MemberID = repairReviewerIDs.MemberID
		repairReviewerInput.RecoveryRootRef = repairReviewerIDs.RecoveryRootRef
		repairReviewerInput.ActionMaterializer = nil
		repairReviewerInput.ActionBindingMaterials = nil
		repairReviewerMember, compileErr := compileMemberWithRunCancellationV1(
			ctx,
			compiler,
			repairReviewerInput,
			repairReviewerIntent,
		)
		if compileErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: compile composite repair Reviewer: %w",
				compileErr,
			)
		}
		preparedDecision = &preparedCompositeDecisionV1{
			children: repairChildren,
			reviewer: preparedCompositeReviewerV1{
				definition:      *definition.Reviewer,
				parentSlotID:    repairReviewerParentSlotID,
				repairRound:     corecontract.CompositeRepairRoundOneV1,
				intent:          repairReviewerIntent,
				intentCanonical: bytes.Clone(repairReviewerIntentCanonical),
				intentDigest:    repairReviewerIntentDigest,
				compiledMember:  repairReviewerMember,
			},
		}
	}

	parentMember, err := compileMemberWithRunCancellationV1(
		ctx,
		compiler,
		input.Parent,
		parentIntent,
	)
	if err != nil {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: compile composite Parent member: %w",
			err,
		)
	}
	childRefs := make(
		[]corecontract.CompositeChildRunRefV1,
		len(preparedChildren),
	)
	for index, child := range preparedChildren {
		childRefs[index] = corecontract.CompositeChildRunRefV1{
			SlotID:               child.assignment.SlotID,
			RunID:                child.compiledMember.RunManifest.RunID,
			AdmissionKey:         child.intent.AdmissionKey,
			MemberSnapshotDigest: child.compiledMember.MemberSnapshot.MemberSnapshotDigest,
			Agent:                child.compiledMember.MemberSnapshot.Agent,
			Profile:              child.compiledMember.MemberSnapshot.Profile,
			TaskInputRef:         child.intent.TaskInputRef,
			Assignment:           child.assignment,
			Transfer:             cloneWorkspaceTransferPlanV1(child.transfer),
		}
	}
	parentManifest, parentManifestCanonical, err := corecontract.NewRunManifest(
		compositeRootManifestV1(
			input.Parent,
			parentIntent,
			parentMember,
			childRefs,
			preparedReviewer,
			preparedDecision,
		),
	)
	if err != nil {
		return CompositeCompileOutput{}, fmt.Errorf(
			"assemblycompiler: freeze composite Parent manifest: %w",
			err,
		)
	}
	if err := parentManifest.ValidateAgainstMember(
		parentMember.MemberSnapshot,
	); err != nil {
		return CompositeCompileOutput{}, err
	}
	parent := compileOutputWithManifestV1(
		parentMember,
		parentManifest,
		parentManifestCanonical,
	)

	children := make(
		[]CompositeChildCompileOutput,
		len(preparedChildren),
	)
	for index, prepared := range preparedChildren {
		childManifest, childManifestCanonical, freezeErr :=
			corecontract.NewRunManifest(compositeChildManifestV1(
				input.Parent,
				prepared,
				parentManifest,
			))
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite Child slot %q manifest: %w",
				prepared.assignment.SlotID,
				freezeErr,
			)
		}
		if err := childManifest.ValidateAgainstMember(
			prepared.compiledMember.MemberSnapshot,
		); err != nil {
			return CompositeCompileOutput{}, err
		}
		children[index] = CompositeChildCompileOutput{
			CompileOutput: compileOutputWithManifestV1(
				prepared.compiledMember,
				childManifest,
				childManifestCanonical,
			),
			Assignment:      prepared.assignment,
			IntentCanonical: bytes.Clone(prepared.intentCanonical),
			IntentDigest:    prepared.intentDigest,
		}
	}
	var reviewer *CompositeReviewerCompileOutput
	if preparedReviewer != nil {
		reviewerManifest, reviewerManifestCanonical, freezeErr :=
			corecontract.NewRunManifest(compositeReviewerManifestV1(
				*preparedReviewer,
				parentManifest,
			))
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite Reviewer manifest: %w",
				freezeErr,
			)
		}
		if err := reviewerManifest.ValidateAgainstMember(
			preparedReviewer.compiledMember.MemberSnapshot,
		); err != nil {
			return CompositeCompileOutput{}, err
		}
		reviewer = &CompositeReviewerCompileOutput{
			CompileOutput: compileOutputWithManifestV1(
				preparedReviewer.compiledMember,
				reviewerManifest,
				reviewerManifestCanonical,
			),
			IntentCanonical: bytes.Clone(preparedReviewer.intentCanonical),
			IntentDigest:    preparedReviewer.intentDigest,
		}
	}
	var decision *CompositeDecisionCompileOutput
	if preparedDecision != nil {
		repairChildren := make(
			[]CompositeChildCompileOutput,
			len(preparedDecision.children),
		)
		for index, prepared := range preparedDecision.children {
			repairManifest, repairManifestCanonical, freezeErr :=
				corecontract.NewRunManifest(compositeChildManifestV1(
					input.Parent,
					prepared,
					parentManifest,
				))
			if freezeErr != nil {
				return CompositeCompileOutput{}, fmt.Errorf(
					"assemblycompiler: freeze composite repair Child slot %q manifest: %w",
					prepared.assignment.SlotID,
					freezeErr,
				)
			}
			if err := repairManifest.ValidateAgainstMember(
				prepared.compiledMember.MemberSnapshot,
			); err != nil {
				return CompositeCompileOutput{}, err
			}
			repairChildren[index] = CompositeChildCompileOutput{
				CompileOutput: compileOutputWithManifestV1(
					prepared.compiledMember,
					repairManifest,
					repairManifestCanonical,
				),
				Assignment:      prepared.assignment,
				IntentCanonical: bytes.Clone(prepared.intentCanonical),
				IntentDigest:    prepared.intentDigest,
			}
		}
		preparedRepairReviewer := preparedDecision.reviewer
		repairReviewerManifest, repairReviewerManifestCanonical, freezeErr :=
			corecontract.NewRunManifest(compositeReviewerManifestV1(
				preparedRepairReviewer,
				parentManifest,
			))
		if freezeErr != nil {
			return CompositeCompileOutput{}, fmt.Errorf(
				"assemblycompiler: freeze composite repair Reviewer manifest: %w",
				freezeErr,
			)
		}
		if err := repairReviewerManifest.ValidateAgainstMember(
			preparedRepairReviewer.compiledMember.MemberSnapshot,
		); err != nil {
			return CompositeCompileOutput{}, err
		}
		decision = &CompositeDecisionCompileOutput{
			RepairChildren: repairChildren,
			RepairReviewer: CompositeReviewerCompileOutput{
				CompileOutput: compileOutputWithManifestV1(
					preparedRepairReviewer.compiledMember,
					repairReviewerManifest,
					repairReviewerManifestCanonical,
				),
				IntentCanonical: bytes.Clone(
					preparedRepairReviewer.intentCanonical,
				),
				IntentDigest: preparedRepairReviewer.intentDigest,
			},
		}
	}
	return CompositeCompileOutput{
		Parent: parent, Children: children, Reviewer: reviewer, Decision: decision,
	}, nil
}

func compileMemberWithRunCancellationV1(
	ctx context.Context,
	compiler Compiler,
	input CompileInput,
	intent corecontract.AdmissionIntentV1,
) (CompileOutput, error) {
	staging := intent
	staging.CancellationScope = corecontract.CancellationScopeRunV1
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(staging)
	if err != nil {
		return CompileOutput{}, err
	}
	input.IntentCanonical = canonical
	input.IntentDigest = digest
	return compiler.Compile(ctx, input)
}

func compositeRootManifestV1(
	input CompileInput,
	intent corecontract.AdmissionIntentV1,
	member CompileOutput,
	children []corecontract.CompositeChildRunRefV1,
	reviewer *preparedCompositeReviewerV1,
	decision *preparedCompositeDecisionV1,
) corecontract.RunManifest {
	var reviewerRef *corecontract.CompositeReviewerRunRefV1
	if reviewer != nil {
		frozen := compositeReviewerRefV1(*reviewer, false)
		reviewerRef = &frozen
	}
	var decisionPlan *corecontract.CompositeDecisionPlanV1
	if decision != nil {
		repairChildren := make(
			[]corecontract.CompositeChildRunRefV1,
			len(decision.children),
		)
		for index, child := range decision.children {
			repairChildren[index] = corecontract.CompositeChildRunRefV1{
				SlotID:               child.assignment.SlotID,
				ParentSlotID:         child.parentSlotID,
				RunID:                child.compiledMember.RunManifest.RunID,
				AdmissionKey:         child.intent.AdmissionKey,
				MemberSnapshotDigest: child.compiledMember.MemberSnapshot.MemberSnapshotDigest,
				Agent:                child.compiledMember.MemberSnapshot.Agent,
				Profile:              child.compiledMember.MemberSnapshot.Profile,
				TaskInputRef:         child.intent.TaskInputRef,
				Assignment:           child.assignment,
				Transfer:             cloneWorkspaceTransferPlanV1(child.transfer),
			}
		}
		decisionPlan = &corecontract.CompositeDecisionPlanV1{
			SchemaVersion:  corecontract.CompositeDecisionPlanSchemaVersionV1,
			RepairChildren: repairChildren,
			RepairReviewer: compositeReviewerRefV1(decision.reviewer, true),
		}
	}
	return corecontract.RunManifest{
		SchemaVersion:         CurrentRunManifestSchemaVersion,
		CoreRuntimeVersion:    CurrentCoreRuntimeVersion,
		AdmissionKey:          intent.AdmissionKey,
		AdmissionIntentDigest: input.IntentDigest,
		RunID:                 input.RunID,
		TenantID:              intent.TenantID,
		Workspace:             member.MemberSnapshot.Workspace,
		PrimaryAgent:          member.MemberSnapshot.Agent,
		Members: []corecontract.MemberSnapshotRef{{
			MemberID: member.MemberSnapshot.MemberID,
			Digest:   member.MemberSnapshot.MemberSnapshotDigest,
		}},
		PrimaryMemberID:   member.MemberSnapshot.MemberID,
		TaskInputRef:      intent.TaskInputRef,
		TaskInputDigest:   intent.TaskInputRef,
		BudgetPolicy:      member.RunManifest.BudgetPolicy,
		CancellationScope: corecontract.CancellationScopeFamilyV1,
		Deadline:          intent.Deadline,
		RecoveryRootRef:   input.RecoveryRootRef,
		Composite: &corecontract.CompositeRunNodeV1{
			SchemaVersion: corecontract.CompositeRunNodeSchemaVersionV1,
			Role:          corecontract.CompositeRunRoleRootV1,
			RootRunID:     input.RunID,
			Plan: &corecontract.CompositeRunPlanV1{
				MergeLogicalStepID: corecontract.CompositeMergeLogicalStepIDV1,
				FamilyModelDispatchLimit: uint32(
					len(children) + 1 + boolToInt(reviewer != nil) +
						boolToInt(decision != nil)*(len(children)+1),
				),
				Children: append([]corecontract.CompositeChildRunRefV1(nil), children...),
				Reviewer: reviewerRef,
				Decision: decisionPlan,
			},
		},
	}
}

func compositeReviewerRefV1(
	reviewer preparedCompositeReviewerV1,
	includeParentSlot bool,
) corecontract.CompositeReviewerRunRefV1 {
	parentSlotID := ""
	if includeParentSlot {
		parentSlotID = reviewer.parentSlotID
	}
	return corecontract.CompositeReviewerRunRefV1{
		ParentSlotID:         parentSlotID,
		RunID:                reviewer.compiledMember.RunManifest.RunID,
		AdmissionKey:         reviewer.intent.AdmissionKey,
		MemberSnapshotDigest: reviewer.compiledMember.MemberSnapshot.MemberSnapshotDigest,
		Agent:                reviewer.compiledMember.MemberSnapshot.Agent,
		Profile:              reviewer.compiledMember.MemberSnapshot.Profile,
		TaskInputRef:         reviewer.intent.TaskInputRef,
		ReviewLogicalStepID:  corecontract.CompositeReviewLogicalStepIDV1,
		Policy:               reviewer.definition.Policy,
		MaxOutputTokens:      reviewer.definition.MaxOutputTokens,
	}
}

func compositeReviewerManifestV1(
	reviewer preparedCompositeReviewerV1,
	parent corecontract.RunManifest,
) corecontract.RunManifest {
	member := reviewer.compiledMember.MemberSnapshot
	parentSlotID := reviewer.parentSlotID
	if parentSlotID == "" {
		parentSlotID = corecontract.CompositeReviewerParentSlotIDV1
	}
	return corecontract.RunManifest{
		SchemaVersion:         CurrentRunManifestSchemaVersion,
		CoreRuntimeVersion:    CurrentCoreRuntimeVersion,
		AdmissionKey:          reviewer.intent.AdmissionKey,
		AdmissionIntentDigest: reviewer.intentDigest,
		RunID:                 reviewer.compiledMember.RunManifest.RunID,
		TenantID:              reviewer.intent.TenantID,
		Workspace:             member.Workspace,
		PrimaryAgent:          member.Agent,
		Members: []corecontract.MemberSnapshotRef{{
			MemberID: member.MemberID,
			Digest:   member.MemberSnapshotDigest,
		}},
		PrimaryMemberID:   member.MemberID,
		TaskInputRef:      reviewer.intent.TaskInputRef,
		TaskInputDigest:   reviewer.intent.TaskInputRef,
		ParentRunID:       parent.RunID,
		BudgetPolicy:      reviewer.compiledMember.RunManifest.BudgetPolicy,
		CancellationScope: corecontract.CancellationScopeInheritedV1,
		Deadline:          reviewer.intent.Deadline,
		RecoveryRootRef:   reviewer.compiledMember.RunManifest.RecoveryRootRef,
		Composite: &corecontract.CompositeRunNodeV1{
			SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
			Role:                 corecontract.CompositeRunRoleReviewerV1,
			RepairRound:          reviewer.repairRound,
			RootRunID:            parent.RunID,
			ParentManifestDigest: parent.ManifestDigest,
			ParentSlotID:         parentSlotID,
		},
	}
}

func compositeChildManifestV1(
	parentInput CompileInput,
	child preparedCompositeChildV1,
	parent corecontract.RunManifest,
) corecontract.RunManifest {
	member := child.compiledMember.MemberSnapshot
	parentSlotID := child.parentSlotID
	if parentSlotID == "" {
		parentSlotID = child.assignment.SlotID
	}
	return corecontract.RunManifest{
		SchemaVersion:         CurrentRunManifestSchemaVersion,
		CoreRuntimeVersion:    CurrentCoreRuntimeVersion,
		AdmissionKey:          child.intent.AdmissionKey,
		AdmissionIntentDigest: child.intentDigest,
		RunID:                 child.compiledMember.RunManifest.RunID,
		TenantID:              child.intent.TenantID,
		Workspace:             member.Workspace,
		PrimaryAgent:          member.Agent,
		Members: []corecontract.MemberSnapshotRef{{
			MemberID: member.MemberID,
			Digest:   member.MemberSnapshotDigest,
		}},
		PrimaryMemberID:   member.MemberID,
		TaskInputRef:      child.intent.TaskInputRef,
		TaskInputDigest:   child.intent.TaskInputRef,
		ParentRunID:       parent.RunID,
		BudgetPolicy:      child.compiledMember.RunManifest.BudgetPolicy,
		CancellationScope: corecontract.CancellationScopeInheritedV1,
		Deadline:          child.intent.Deadline,
		RecoveryRootRef:   child.compiledMember.RunManifest.RecoveryRootRef,
		Composite: &corecontract.CompositeRunNodeV1{
			SchemaVersion:        corecontract.CompositeRunNodeSchemaVersionV1,
			Role:                 corecontract.CompositeRunRoleChildV1,
			RepairRound:          child.repairRound,
			RootRunID:            parentInput.RunID,
			ParentManifestDigest: parent.ManifestDigest,
			ParentSlotID:         parentSlotID,
			Assignment:           &child.assignment,
		},
	}
}

func compileOutputWithManifestV1(
	member CompileOutput,
	manifest corecontract.RunManifest,
	manifestCanonical []byte,
) CompileOutput {
	return CompileOutput{
		PublishedBasis:          member.PublishedBasis,
		MemberSnapshot:          member.MemberSnapshot,
		MemberSnapshotCanonical: bytes.Clone(member.MemberSnapshotCanonical),
		RunManifest:             manifest,
		RunManifestCanonical:    bytes.Clone(manifestCanonical),
	}
}

func rejectCompositeChildSideEffectPorts(
	slotID string,
	profile controlcontract.ProfileDefinition,
) error {
	for _, binding := range profile.Bindings {
		switch binding.Port.Name {
		case moduleapi.PortNameActionProvider,
			moduleapi.PortNameChannelTransport:
			return fmt.Errorf(
				"%w: composite Child slot %q profile %q binds %s/%s",
				ErrCapabilityNotAvailable,
				slotID,
				profile.Profile.ID,
				binding.Port.Name,
				binding.Port.ExactVersion,
			)
		}
	}
	return nil
}

func resolveCompositeWorkspaceTransferV1(
	root controlcontract.WorkspaceDefinition,
	target controlcontract.WorkspaceDefinition,
) (corecontract.WorkspaceTransferPlanV1, error) {
	rootGrant, present := root.FindTransferGrantForPeer(target.Workspace)
	if !present {
		return corecontract.WorkspaceTransferPlanV1{}, fmt.Errorf(
			"root Workspace %q has no exact grant for target %q",
			root.Workspace.ID,
			target.Workspace.ID,
		)
	}
	targetGrant, present := target.FindTransferGrantForPeer(root.Workspace)
	if !present {
		return corecontract.WorkspaceTransferPlanV1{}, fmt.Errorf(
			"target Workspace %q has no exact grant for root %q",
			target.Workspace.ID,
			root.Workspace.ID,
		)
	}
	plan, err := corecontract.NewWorkspaceTransferPlanV1(rootGrant, targetGrant)
	if err != nil {
		return corecontract.WorkspaceTransferPlanV1{}, err
	}
	return plan, nil
}

func cloneWorkspaceTransferPlanV1(
	input *corecontract.WorkspaceTransferPlanV1,
) *corecontract.WorkspaceTransferPlanV1 {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func deriveCompositeChildIDsV1(
	parentIntentDigest string,
	slotID string,
	transfer *corecontract.WorkspaceTransferPlanV1,
) (compositeChildIDsV1, error) {
	if transfer != nil {
		if err := transfer.Validate(); err != nil {
			return compositeChildIDsV1{}, fmt.Errorf(
				"assemblycompiler: invalid composite Child Workspace transfer identity: %w",
				err,
			)
		}
	}
	encoded, err := json.Marshal(compositeChildIdentitySeedV1{
		ParentIntentDigest: parentIntentDigest,
		SlotID:             slotID,
		Transfer:           cloneWorkspaceTransferPlanV1(transfer),
	})
	if err != nil {
		return compositeChildIDsV1{}, fmt.Errorf(
			"assemblycompiler: encode composite Child identity seed: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return compositeChildIDsV1{}, fmt.Errorf(
			"assemblycompiler: canonicalize composite Child identity seed: %w",
			err,
		)
	}
	return compositeChildIDsV1{
		AdmissionKey: compositeChildAdmissionKeyPrefix + moduleapi.Digest(
			compositeChildAdmissionKeyDomain,
			canonical,
		),
		RunID: compositeChildRunIDPrefix + moduleapi.Digest(
			compositeChildRunIDDomain,
			canonical,
		),
		MemberID: compositeChildMemberIDPrefix + moduleapi.Digest(
			compositeChildMemberIDDomain,
			canonical,
		),
		RecoveryRootRef: compositeChildRecoveryRootPrefix + moduleapi.Digest(
			compositeChildRecoveryRootDomain,
			canonical,
		),
	}, nil
}

func deriveCompositeReviewerIDsV1(
	parentIntentDigest string,
) (compositeChildIDsV1, error) {
	if !moduleapi.ValidSHA256(parentIntentDigest) {
		return compositeChildIDsV1{}, fmt.Errorf(
			"assemblycompiler: invalid Parent intent digest for Reviewer identity",
		)
	}
	encoded, err := json.Marshal(struct {
		ParentIntentDigest string `json:"parent_intent_digest"`
	}{ParentIntentDigest: parentIntentDigest})
	if err != nil {
		return compositeChildIDsV1{}, fmt.Errorf(
			"assemblycompiler: encode Reviewer identity seed: %w", err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return compositeChildIDsV1{}, fmt.Errorf(
			"assemblycompiler: canonicalize Reviewer identity seed: %w", err,
		)
	}
	return compositeChildIDsV1{
		AdmissionKey: compositeReviewerAdmissionKeyPrefix + moduleapi.Digest(
			compositeReviewerAdmissionKeyDomain, canonical,
		),
		RunID: compositeReviewerRunIDPrefix + moduleapi.Digest(
			compositeReviewerRunIDDomain, canonical,
		),
		MemberID: compositeReviewerMemberIDPrefix + moduleapi.Digest(
			compositeReviewerMemberIDDomain, canonical,
		),
		RecoveryRootRef: compositeReviewerRecoveryRootPrefix + moduleapi.Digest(
			compositeReviewerRecoveryRootDomain, canonical,
		),
	}, nil
}

func deriveCompositeRepairChildIDsV1(
	parentIntentDigest string,
	slotID string,
	transfer *corecontract.WorkspaceTransferPlanV1,
) (compositeChildIDsV1, string, error) {
	if !moduleapi.ValidSHA256(parentIntentDigest) || slotID == "" {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: invalid repair Child identity seed",
		)
	}
	if transfer != nil {
		if err := transfer.Validate(); err != nil {
			return compositeChildIDsV1{}, "", fmt.Errorf(
				"assemblycompiler: invalid repair Child Workspace transfer identity: %w",
				err,
			)
		}
	}
	encoded, err := json.Marshal(compositeRepairIdentitySeedV1{
		ParentIntentDigest: parentIntentDigest,
		SlotID:             slotID,
		RepairRound:        corecontract.CompositeRepairRoundOneV1,
		Transfer:           cloneWorkspaceTransferPlanV1(transfer),
	})
	if err != nil {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: encode repair Child identity seed: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: canonicalize repair Child identity seed: %w",
			err,
		)
	}
	return compositeChildIDsV1{
			AdmissionKey: compositeRepairChildAdmissionKeyPrefix + moduleapi.Digest(
				compositeRepairChildAdmissionKeyDomain,
				canonical,
			),
			RunID: compositeRepairChildRunIDPrefix + moduleapi.Digest(
				compositeRepairChildRunIDDomain,
				canonical,
			),
			MemberID: compositeRepairChildMemberIDPrefix + moduleapi.Digest(
				compositeRepairChildMemberIDDomain,
				canonical,
			),
			RecoveryRootRef: compositeRepairChildRecoveryRootPrefix + moduleapi.Digest(
				compositeRepairChildRecoveryRootDomain,
				canonical,
			),
		}, compositeRepairChildParentSlotPrefix + moduleapi.Digest(
			compositeRepairChildParentSlotDomain,
			canonical,
		), nil
}

func deriveCompositeRepairReviewerIDsV1(
	parentIntentDigest string,
) (compositeChildIDsV1, string, error) {
	if !moduleapi.ValidSHA256(parentIntentDigest) {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: invalid repair Reviewer identity seed",
		)
	}
	encoded, err := json.Marshal(compositeRepairIdentitySeedV1{
		ParentIntentDigest: parentIntentDigest,
		RepairRound:        corecontract.CompositeRepairRoundOneV1,
	})
	if err != nil {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: encode repair Reviewer identity seed: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return compositeChildIDsV1{}, "", fmt.Errorf(
			"assemblycompiler: canonicalize repair Reviewer identity seed: %w",
			err,
		)
	}
	return compositeChildIDsV1{
			AdmissionKey: compositeRepairReviewerAdmissionKeyPrefix + moduleapi.Digest(
				compositeRepairReviewerAdmissionKeyDomain,
				canonical,
			),
			RunID: compositeRepairReviewerRunIDPrefix + moduleapi.Digest(
				compositeRepairReviewerRunIDDomain,
				canonical,
			),
			MemberID: compositeRepairReviewerMemberIDPrefix + moduleapi.Digest(
				compositeRepairReviewerMemberIDDomain,
				canonical,
			),
			RecoveryRootRef: compositeRepairReviewerRecoveryRootPrefix + moduleapi.Digest(
				compositeRepairReviewerRecoveryRootDomain,
				canonical,
			),
		}, compositeRepairReviewerParentSlotPrefix + moduleapi.Digest(
			compositeRepairReviewerParentSlotDomain,
			canonical,
		), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
