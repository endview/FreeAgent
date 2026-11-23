package harness

import (
	"context"
	"errors"
	"time"
)

func (s *channelSession) telegramAdminCard(ctx context.Context, run Run, confirmation Confirmation, record controlRecord) (string, []channelButton, error) {
	alternative := run.Output
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || run.Permission == nil || !s.supportsButtons() {
		return alternative, nil, nil
	}
	control, err := s.tasks.Control()
	if err != nil {
		return "", nil, err
	}
	release, err := control.lockTelegramAdminDecision(ctx, record, true, true)
	if err != nil {
		return alternative, nil, nil
	}
	ok, text, _ := control.telegramAdminDirectDiff(record)
	release()
	expires, err := time.Parse(time.RFC3339Nano, record.Operation.ExpiresAt)
	if err != nil || !ok || !time.Now().Before(expires) {
		return alternative, nil, nil
	}
	var buttons []channelButton
	for _, approved := range []bool{true, false} {
		token, err := s.button(channelApproval{Confirmation: confirmation, Target: origin.ChannelTarget, Person: run.Permission.PersonID, Approved: approved, Expires: expires})
		if err != nil {
			return "", nil, err
		}
		label := "拒绝"
		if approved {
			label = "保存全局设置"
		}
		buttons = append(buttons, channelButton{Text: label, Token: token})
	}
	return text, buttons, nil
}

func (s *channelSession) confirmTelegramAdmin(ctx context.Context, event channelEvent, item channelApproval, record controlRecord) error {
	validCard := func(context.Context) error {
		if s.source != SpaceSourceTelegram || item.Person != event.Person || item.Target != event.Target ||
			item.Confirmation.ControlOperationID != record.Operation.ID || item.Message != event.CallbackMessage ||
			item.Generation != s.sessionGeneration() || item.Withdraw || !time.Now().Before(item.Expires) {
			return ErrPermissionDenied
		}
		if s.remote {
			bridge := s.tasks.core.Store.channelBridge.Load()
			if bridge == nil {
				return ErrPermissionDenied
			}
			bridge.mu.Lock()
			current := false
			for _, connection := range bridge.connections {
				current = current || connection.session == s && connection.capabilities.Buttons
			}
			bridge.mu.Unlock()
			if !current {
				return ErrPermissionDenied
			}
		}
		return nil
	}
	if err := validCard(ctx); err != nil {
		return err
	}
	control, err := s.tasks.Control()
	if err != nil {
		return err
	}
	principal := "telegram-admin-decision"
	cap, err := control.IssueInteractiveCapability(ctx, record.Operation.ConversationID, principal)
	if err != nil {
		return err
	}
	card, err := control.renderConfirmation(ctx, record.Operation.ID, record.Operation.ConversationID, principal, true)
	if err == nil {
		auth := ControlAuthorization{Source: ControlUserDirect, ConversationID: record.Operation.ConversationID, Principal: principal,
			Code: card.Code, Capability: cap, telegramAdmin: true, revalidate: validCard}
		if item.Approved {
			_, err = control.ConfirmControlChange(ctx, record.Operation.ID, auth)
		} else {
			_, err = control.RejectControlChange(ctx, record.Operation.ID, auth)
		}
	}
	switch {
	case errors.Is(err, ErrControlExpired), errors.Is(err, ErrControlStaleRevision), errors.Is(err, ErrTelegramAdminChanged):
		channelFeedback(ctx, "这张预览已失效。设置未修改，请重新提出。")
		return nil
	case errors.Is(err, ErrControlAlreadyUsed), errors.Is(err, ErrControlOperationNotFound):
		text := "此提案已处理，请核对当前设置。"
		if op, readErr := control.ReadControlOperation(ctx, record.Operation.ID); readErr == nil {
			switch op.Status {
			case ControlApplied:
				text = "此提案已保存，不会重复修改。"
			case ControlRejected:
				text = "此提案已拒绝，设置未修改。"
			case ControlPartiallyApplied:
				text = "保存结果待核实。请在管理页查看原提案，不要重复提交。"
			}
		}
		channelFeedback(ctx, text)
		return nil
	case errors.Is(err, ErrControlBusy), errors.Is(err, ErrActiveRun):
		channelFeedback(ctx, "暂时无法保存，请稍后再点原卡。")
		return nil
	case errors.Is(err, ErrUnknownResult):
		channelFeedback(ctx, "保存结果待核实。请在管理页查看原提案，不要重复提交。")
		return nil
	case err != nil:
		return err
	}
	text := "已拒绝，设置未修改。"
	if item.Approved {
		text = "全局设置已保存，下一轮生效。"
	}
	channelFeedback(ctx, text)
	return nil
}
