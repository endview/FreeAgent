package coreloop

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
)

// prepareWorkspaceTransferContextV1 resolves the only task body and transfer
// edges that may reach request preparation. For a cross-Workspace Specialist,
// the raw root TASK_INPUT remains Host-only and only the trusted TASK_SUMMARY is
// returned as DynamicTaskText. Root and Reviewer Runs retain the root task but
// must provide one verified RESULT record for every transferred contribution.
func prepareWorkspaceTransferContextV1(
	run currentstore.RunForLoop,
) (preparedWorkspaceTransferContextV1, error) {
	root, collaboration, err := collaborationRootForRunV1(run)
	if err != nil {
		return preparedWorkspaceTransferContextV1{}, err
	}

	if collaboration && run.Manifest.Composite != nil &&
		run.Manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 {
		planned, err := plannedWorkspaceTransferChildV1(run, root)
		if err != nil {
			return preparedWorkspaceTransferContextV1{}, err
		}
		if planned.Transfer != nil {
			if run.WorkspaceTransfer == nil {
				return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
					"%w: cross-Workspace Child lacks its Host transfer closure",
					ErrInvalidPureChatRequest,
				)
			}
			record, err := currentstore.PrepareWorkspaceTransferRequestV1(run)
			if err != nil || record == nil {
				if err == nil {
					err = fmt.Errorf("request transfer record is absent")
				}
				return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
					"%w: prepare cross-Workspace Child request: %v",
					ErrInvalidPureChatRequest,
					err,
				)
			}
			summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
				record.Payload.CanonicalBytes,
			)
			if err != nil || summary.Summary == "" {
				return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
					"%w: restore trusted Workspace task summary: %v",
					ErrInvalidPureChatRequest,
					err,
				)
			}
			return preparedWorkspaceTransferContextV1{
				TaskInput: cloneWorkspaceTransferTaskInputV1(
					run.WorkspaceTransfer.RootTaskInput,
				),
				DynamicTaskText: summary.Summary,
				Materials: []contextcompiler.WorkspaceTransferMaterialV1{
					record.ContextMaterialV1(),
				},
			}, nil
		}
		if run.WorkspaceTransfer != nil {
			return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
				"%w: same-Workspace Child exposes a transfer closure",
				ErrInvalidPureChatRequest,
			)
		}
	} else if run.WorkspaceTransfer != nil {
		return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
			"%w: only a planned cross-Workspace Child may expose a transfer closure",
			ErrInvalidPureChatRequest,
		)
	}

	taskContent, err := exactContent(
		run,
		run.Manifest.TaskInputRef,
		currentstore.ContentTaskInput,
		"task input",
	)
	if err != nil {
		return preparedWorkspaceTransferContextV1{}, err
	}
	task, err := corecontract.RestoreTaskInputV1(taskContent.CanonicalBytes)
	if err != nil {
		return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
			"%w: restore task input for Host preparation: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	prepared := preparedWorkspaceTransferContextV1{
		TaskInput:       taskContent,
		DynamicTaskText: task.Text,
	}
	if !collaboration || run.Manifest.Composite == nil {
		return prepared, nil
	}
	switch run.Manifest.Composite.Role {
	case corecontract.CompositeRunRoleRootV1,
		corecontract.CompositeRunRoleReviewerV1:
		for index := range run.CompositeChildren {
			child := run.CompositeChildren[index]
			planned, err := plannedWorkspaceTransferResultV1(root, child)
			if err != nil {
				return preparedWorkspaceTransferContextV1{}, err
			}
			if planned.Transfer == nil {
				if child.WorkspaceTransfer != nil {
					return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
						"%w: same-Workspace contribution %q exposes a RESULT transfer",
						ErrInvalidPureChatRequest,
						child.RunID,
					)
				}
				continue
			}
			if child.WorkspaceTransfer == nil {
				return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
					"%w: cross-Workspace contribution %q lacks its RESULT transfer",
					ErrInvalidPureChatRequest,
					child.RunID,
				)
			}
			prepared.Materials = append(
				prepared.Materials,
				child.WorkspaceTransfer.ContextMaterialV1(),
			)
		}
	case corecontract.CompositeRunRoleChildV1:
		// Same-Workspace Decision Children have no transfer material.
	default:
		return preparedWorkspaceTransferContextV1{}, fmt.Errorf(
			"%w: unsupported Workspace transfer participant role %q",
			ErrInvalidPureChatRequest,
			run.Manifest.Composite.Role,
		)
	}
	return prepared, nil
}

func plannedWorkspaceTransferChildV1(
	run currentstore.RunForLoop,
	root corecontract.RunManifest,
) (corecontract.CompositeChildRunRefV1, error) {
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Assignment == nil ||
		root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return corecontract.CompositeChildRunRefV1{}, fmt.Errorf(
			"%w: collaboration Child plan is absent",
			ErrInvalidPureChatRequest,
		)
	}
	candidates := root.Composite.Plan.Children
	if run.Manifest.Composite.RepairRound ==
		corecontract.CompositeRepairRoundOneV1 {
		candidates = root.Composite.Plan.Decision.RepairChildren
	}
	for _, candidate := range candidates {
		if candidate.RunID == run.RunID &&
			candidate.SlotID == run.Manifest.Composite.Assignment.SlotID {
			return candidate, nil
		}
	}
	return corecontract.CompositeChildRunRefV1{}, fmt.Errorf(
		"%w: collaboration Child is absent from its frozen round",
		ErrInvalidPureChatRequest,
	)
}

func plannedWorkspaceTransferResultV1(
	root corecontract.RunManifest,
	child currentstore.CompositeChildResultRecordV1,
) (corecontract.CompositeChildRunRefV1, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return corecontract.CompositeChildRunRefV1{}, fmt.Errorf(
			"%w: collaboration result plan is absent",
			ErrInvalidPureChatRequest,
		)
	}
	for _, candidate := range root.Composite.Plan.Children {
		if candidate.RunID == child.RunID && candidate.SlotID == child.SlotID {
			return candidate, nil
		}
	}
	for _, candidate := range root.Composite.Plan.Decision.RepairChildren {
		if candidate.RunID == child.RunID && candidate.SlotID == child.SlotID {
			return candidate, nil
		}
	}
	return corecontract.CompositeChildRunRefV1{}, fmt.Errorf(
		"%w: contribution %q is absent from the frozen collaboration family",
		ErrInvalidPureChatRequest,
		child.RunID,
	)
}

func cloneWorkspaceTransferTaskInputV1(
	record currentstore.ContentRecord,
) currentstore.ContentRecord {
	record.CanonicalBytes = bytes.Clone(record.CanonicalBytes)
	return record
}
