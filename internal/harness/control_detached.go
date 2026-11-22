package harness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const detachedProposalReason = "control_proposal_detached"

func localWebHandoff(record controlRecord) bool {
	return record.Operation.RequestEntry == "public_web" && record.Operation.LocalApproval
}

// A host can hand off a declarative proposal without retaining the execution
// slot. The proposal remains pending, while its original tool call is closed.
// This is independent of channels and grants no new confirmation authority.
func (service *ControlService) detachProposal(ctx context.Context, operation string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operation]
	if !ok || record.Operation.Status != ControlPending || record.Operation.Kind != ControlSetConversationCapabilities && !localWebHandoff(record) {
		return ErrControlAlreadyUsed
	}
	if !record.Operation.Detached {
		record.Operation.Detached = true
		if err := service.appendLocked("detached", record); err != nil {
			return err
		}
		service.operations[operation] = record
	}
	return service.settleDetachedProposal(ctx, record)
}

func (service *ControlService) settleDetachedProposal(ctx context.Context, record controlRecord) error {
	if !record.Operation.Detached || record.Operation.AttemptID == "" {
		return nil
	}
	attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if attempt.Status != "pending" {
		return nil
	}
	run, err := service.core.Store.GetRun(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	if run.Status != "waiting_confirmation" {
		return nil
	}
	result := ToolResult{Content: "Proposal saved, setting not applied. Confirmation proceeds independently; this task has handed off the decision. Do not repeat the proposal."}
	output := "设置提案已保存，等待确认；你可以继续聊天。设置尚未修改。"
	if localWebHandoff(record) {
		output = fmt.Sprintf("提案已保存，设置尚未修改。需要在服务器本机批准：\n\n%s",
			"freeagent control confirm --operation "+record.Operation.ID)
	}
	err = service.core.Store.settleControlProposal(ctx, run, attempt, result, true, detachedProposalReason, "completed", output)
	if errors.Is(err, ErrControlAlreadyUsed) {
		return nil
	}
	return err
}

func (service *ControlService) checkDetachedExpiryLocked(ctx context.Context, record *controlRecord) error {
	if !record.Operation.Detached {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, record.Operation.ExpiresAt)
	if err == nil && time.Now().Before(at) {
		return nil
	}
	if err := service.invalidateProposalLocked(ctx, record, "expired"); err != nil {
		return err
	}
	return ErrControlExpired
}

func (service *ControlService) checkDetachedDestination(ctx context.Context, record controlRecord) error {
	if !record.Operation.Detached {
		return nil
	}
	attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
	if err != nil {
		return errors.Join(ErrPermissionRevoked, err)
	}
	tasks := NewTaskService(service.core)
	if localWebHandoff(record) {
		if _, remote := PublicEntryFrom(ctx); remote {
			return ErrPublicLocalConfirmation
		}
		run, err := service.core.Store.GetRun(ctx, attempt.RunID)
		if err != nil || run.Status != "completed" || run.StopReason != detachedProposalReason || run.ConversationID != record.Operation.ConversationID {
			return errors.Join(ErrPermissionRevoked, err)
		}
		chat, err := service.core.Store.GetConversation(ctx, run.ConversationID)
		if err != nil || run.Permission == nil || chat.SpaceID != run.Permission.SpaceID {
			return errors.Join(ErrPermissionRevoked, err)
		}
		return nil
	}
	run, origin, err := tasks.ChannelRun(ctx, attempt.RunID)
	if err != nil || origin == nil || run.Status != "completed" || run.StopReason != detachedProposalReason {
		return errors.Join(ErrPermissionRevoked, err)
	}
	if err := tasks.checkChannelRunDestination(ctx, run, origin.ChannelTarget); err != nil {
		return errors.Join(ErrPermissionRevoked, err)
	}
	if err := tasks.checkTelegramRun(ctx, run); err != nil {
		return err
	}
	return tasks.channelSettingRecordEligible(ctx, run, record)
}
