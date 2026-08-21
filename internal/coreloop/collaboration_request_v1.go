package coreloop

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// collaborationCompilerInputV1 projects only Store-proven W5 material into
// the pure Context Compiler. It does not infer a decision, affected slot, or
// repair lineage from model prose.
func collaborationCompilerInputV1(
	run currentstore.RunForLoop,
) (
	*corecontract.CompositeRunNodeV1,
	[]contextcompiler.CompositeChildResultV1,
	*contextcompiler.CompositeCollaborationMaterialV1,
	error,
) {
	root, enabled, err := collaborationRootForRunV1(run)
	if err != nil {
		return nil, nil, nil, err
	}
	if !enabled {
		return nil, nil, nil, fmt.Errorf(
			"%w: Run is not an explicit collaboration participant",
			ErrInvalidPureChatRequest,
		)
	}
	node := cloneCompositeRunNodeForCompilerV1(*run.Manifest.Composite)
	plan := cloneCompositeRunPlanForCompilerV1(*root.Composite.Plan)
	material := &contextcompiler.CompositeCollaborationMaterialV1{
		FamilyDigest:     root.ManifestDigest,
		ParticipantRunID: run.RunID,
		RootPlan:         &plan,
	}

	addRepairLineage := func() error {
		if run.CompositePreviousContributionSet == nil ||
			run.CompositeRepairVerdict == nil ||
			run.CompositeRepairVerdictResult == nil ||
			run.CompositeRepairVerdictRef == "" {
			return fmt.Errorf(
				"%w: collaboration repair lineage is absent",
				ErrInvalidPureChatRequest,
			)
		}
		previous := cloneCollaborationContributionSetV1(
			*run.CompositePreviousContributionSet,
		)
		request, err := collaborationVerdictMaterialFromRecordV1(
			*run.CompositeRepairVerdictResult,
			plan.Reviewer,
		)
		if err != nil || request.ResultRef != run.CompositeRepairVerdictRef {
			if err == nil {
				err = fmt.Errorf("repair verdict result reference differs")
			}
			return fmt.Errorf(
				"%w: collaboration repair verdict: %v",
				ErrInvalidPureChatRequest,
				err,
			)
		}
		material.PreviousContributionSet = &previous
		material.RepairRequestVerdict = request
		return nil
	}

	switch node.Role {
	case corecontract.CompositeRunRoleChildV1:
		if len(run.CompositeChildren) != 0 ||
			run.CompositeContributionSet != nil ||
			run.CompositeContributionSetDigest != "" ||
			run.CompositeReviewer != nil {
			return nil, nil, nil, fmt.Errorf(
				"%w: collaboration Specialist received sibling result material",
				ErrInvalidPureChatRequest,
			)
		}
		if node.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			if err := addRepairLineage(); err != nil {
				return nil, nil, nil, err
			}
			if run.CompositeRepairBasis == nil ||
				len(run.CompositeRepairBasisCanonical) == 0 {
				return nil, nil, nil, fmt.Errorf(
					"%w: collaboration repair basis is absent",
					ErrInvalidPureChatRequest,
				)
			}
			material.RepairBasisCanonical = bytes.Clone(
				run.CompositeRepairBasisCanonical,
			)
		} else if node.RepairRound != 0 ||
			run.CompositePreviousContributionSet != nil ||
			run.CompositeRepairVerdict != nil ||
			run.CompositeRepairVerdictResult != nil ||
			len(run.CompositeRepairBasisCanonical) != 0 {
			return nil, nil, nil, fmt.Errorf(
				"%w: initial collaboration Specialist carries repair lineage",
				ErrInvalidPureChatRequest,
			)
		}
		return &node, nil, material, nil

	case corecontract.CompositeRunRoleReviewerV1,
		corecontract.CompositeRunRoleRootV1:
		if run.CompositeContributionSet == nil ||
			run.CompositeContributionSetDigest == "" {
			return nil, nil, nil, fmt.Errorf(
				"%w: collaboration contribution set is absent",
				ErrInvalidPureChatRequest,
			)
		}
		set := cloneCollaborationContributionSetV1(
			*run.CompositeContributionSet,
		)
		if set.RepairRound != run.CompositeRepairRound {
			return nil, nil, nil, fmt.Errorf(
				"%w: collaboration contribution-set round differs from Store frontier",
				ErrInvalidPureChatRequest,
			)
		}
		results, err := collaborationChildMaterialsV1(run, root, set)
		if err != nil {
			return nil, nil, nil, err
		}
		material.ContributionSet = &set
		material.ContributionSetDigest = run.CompositeContributionSetDigest
		if set.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			if err := addRepairLineage(); err != nil {
				return nil, nil, nil, err
			}
		} else if set.RepairRound != 0 ||
			run.CompositePreviousContributionSet != nil ||
			run.CompositeRepairVerdictResult != nil {
			return nil, nil, nil, fmt.Errorf(
				"%w: round-zero collaboration set carries repair lineage",
				ErrInvalidPureChatRequest,
			)
		}
		if node.Role == corecontract.CompositeRunRoleReviewerV1 {
			if run.CompositeReviewer != nil {
				return nil, nil, nil, fmt.Errorf(
					"%w: collaboration Reviewer received sibling Reviewer output",
					ErrInvalidPureChatRequest,
				)
			}
			return &node, results, material, nil
		}

		reviewer := run.CompositeReviewer
		if reviewer == nil ||
			reviewer.State != currentstore.CompositeReviewerSucceededV1 ||
			reviewer.CollaborationVerdict == nil ||
			reviewer.CollaborationVerdict.Decision !=
				corecontract.CollaborationReviewDecisionApproveV1 {
			return nil, nil, nil, fmt.Errorf(
				"%w: collaboration Root lacks an exact APPROVE result",
				ErrInvalidPureChatRequest,
			)
		}
		plannedReviewer := plan.Reviewer
		if set.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			plannedReviewer = &plan.Decision.RepairReviewer
		}
		reviewMaterial, err := collaborationVerdictMaterialFromRecordV1(
			*reviewer,
			plannedReviewer,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		material.ReviewVerdict = reviewMaterial
		return &node, results, material, nil
	default:
		return nil, nil, nil, fmt.Errorf(
			"%w: unsupported collaboration role %q",
			ErrInvalidPureChatRequest,
			node.Role,
		)
	}
}

func collaborationRootForRunV1(
	run currentstore.RunForLoop,
) (corecontract.RunManifest, bool, error) {
	node := run.Manifest.Composite
	if node == nil {
		if run.CompositeRoot != nil ||
			run.CompositeDecisionFrontier != nil ||
			run.CompositeContributionSet != nil ||
			run.CompositeRepairVerdictResult != nil {
			return corecontract.RunManifest{}, false, fmt.Errorf(
				"%w: non-composite Run exposes collaboration material",
				ErrInvalidPureChatRequest,
			)
		}
		return corecontract.RunManifest{}, false, nil
	}
	var root corecontract.RunManifest
	switch node.Role {
	case corecontract.CompositeRunRoleRootV1:
		root = run.Manifest
	case corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1:
		if run.CompositeRoot == nil {
			if node.RepairRound == corecontract.CompositeRepairRoundOneV1 {
				return corecontract.RunManifest{}, false, fmt.Errorf(
					"%w: repair participant lacks its frozen Root",
					ErrInvalidPureChatRequest,
				)
			}
			return corecontract.RunManifest{}, false, nil
		}
		root = *run.CompositeRoot
	default:
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"%w: unsupported composite role %q",
			ErrInvalidPureChatRequest,
			node.Role,
		)
	}
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil {
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"%w: collaboration Root plan is absent",
			ErrInvalidPureChatRequest,
		)
	}
	if root.Composite.Plan.Decision == nil {
		return corecontract.RunManifest{}, false, nil
	}
	if root.ManifestDigest == "" ||
		(node.Role != corecontract.CompositeRunRoleRootV1 &&
			(node.RootRunID != root.RunID ||
				node.ParentManifestDigest != root.ManifestDigest)) {
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"%w: collaboration participant differs from its frozen Root",
			ErrInvalidPureChatRequest,
		)
	}
	return root, true, nil
}

func collaborationChildMaterialsV1(
	run currentstore.RunForLoop,
	root corecontract.RunManifest,
	set corecontract.CollaborationContributionSetV1,
) ([]contextcompiler.CompositeChildResultV1, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil ||
		len(run.CompositeChildren) != len(set.Contributions) ||
		len(set.Contributions) != len(root.Composite.Plan.Children) {
		return nil, fmt.Errorf(
			"%w: collaboration Child material cardinality does not close",
			ErrInvalidPureChatRequest,
		)
	}
	results := make(
		[]contextcompiler.CompositeChildResultV1,
		len(run.CompositeChildren),
	)
	for index, child := range run.CompositeChildren {
		entry := set.Contributions[index]
		planned := root.Composite.Plan.Children[index]
		if entry.RunID != planned.RunID {
			planned = root.Composite.Plan.Decision.RepairChildren[index]
		}
		if child.State != currentstore.CompositeChildSucceededV1 ||
			child.SlotID != entry.SlotID || child.RunID != entry.RunID ||
			child.RunID != planned.RunID ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.Assignment != planned.Assignment ||
			child.ResultRef != entry.ResultRef ||
			child.Contribution == nil ||
			child.ContributionDigest != entry.ContributionDigest ||
			len(child.OutputCanonical) == 0 {
			return nil, fmt.Errorf(
				"%w: collaboration Child %d differs from its exact contribution edge",
				ErrInvalidPureChatRequest,
				index,
			)
		}
		results[index] = contextcompiler.CompositeChildResultV1{
			SlotID:                child.SlotID,
			RunID:                 child.RunID,
			AdmissionKey:          planned.AdmissionKey,
			ChildManifestDigest:   child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			Assignment:            child.Assignment,
			ResultRef:             child.ResultRef,
			TerminalRevision:      child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
			ResultCanonical:       bytes.Clone(child.OutputCanonical),
		}
	}
	return results, nil
}

func collaborationVerdictMaterialFromRecordV1(
	record currentstore.CompositeReviewerResultRecordV1,
	planned *corecontract.CompositeReviewerRunRefV1,
) (*contextcompiler.CompositeCollaborationReviewVerdictMaterialV1, error) {
	if planned == nil || record.State != currentstore.CompositeReviewerSucceededV1 ||
		record.RunID != planned.RunID ||
		record.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
		record.CollaborationVerdict == nil || record.AttemptID == "" ||
		record.ResultRef == "" || len(record.OutputCanonical) == 0 ||
		len(record.VerdictCanonical) == 0 ||
		!moduleapi.ValidSHA256(record.ManifestDigest) {
		return nil, fmt.Errorf(
			"%w: collaboration Reviewer result is incomplete",
			ErrInvalidPureChatRequest,
		)
	}
	return &contextcompiler.CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID:          record.RunID,
		ReviewerManifestDigest: record.ManifestDigest,
		MemberSnapshotDigest:   record.MemberSnapshotDigest,
		AttemptID:              record.AttemptID,
		LogicalStepID:          planned.ReviewLogicalStepID,
		ResultRef:              record.ResultRef,
		TerminalRunRevision:    record.RunRevision,
		TerminalFrameRevision:  record.FrameRevision,
		ResultCanonical:        bytes.Clone(record.OutputCanonical),
		VerdictCanonical:       bytes.Clone(record.VerdictCanonical),
	}, nil
}

func cloneCollaborationContributionSetV1(
	input corecontract.CollaborationContributionSetV1,
) corecontract.CollaborationContributionSetV1 {
	input.Contributions = append(
		[]corecontract.CollaborationContributionEntryV1(nil),
		input.Contributions...,
	)
	return input
}

func cloneCompositeRunNodeForCompilerV1(
	input corecontract.CompositeRunNodeV1,
) corecontract.CompositeRunNodeV1 {
	if input.Assignment != nil {
		assignment := *input.Assignment
		input.Assignment = &assignment
	}
	if input.Plan != nil {
		plan := cloneCompositeRunPlanForCompilerV1(*input.Plan)
		input.Plan = &plan
	}
	return input
}

func cloneCompositeRunPlanForCompilerV1(
	input corecontract.CompositeRunPlanV1,
) corecontract.CompositeRunPlanV1 {
	input.Children = append(
		[]corecontract.CompositeChildRunRefV1(nil),
		input.Children...,
	)
	if input.Reviewer != nil {
		reviewer := *input.Reviewer
		input.Reviewer = &reviewer
	}
	if input.Decision != nil {
		decision := *input.Decision
		decision.RepairChildren = append(
			[]corecontract.CompositeChildRunRefV1(nil),
			input.Decision.RepairChildren...,
		)
		input.Decision = &decision
	}
	return input
}
