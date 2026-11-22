package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	tgpolicy "github.com/endview/freeagent/internal/connectors/telegram"
)

type channelSettingBinding struct {
	Closed         bool          `json:"closed,omitempty"`
	Detached       bool          `json:"detached,omitempty"`
	Generation     string        `json:"generation"`
	AccessRevision uint64        `json:"access_revision"`
	Person         string        `json:"person"`
	Run            string        `json:"run_id"`
	Target         ChannelTarget `json:"target"`
	Revision       int64         `json:"revision"`
	Expires        time.Time     `json:"expires"`
}

func (tasks *TaskService) bindSettingCard(ctx context.Context, operation string, binding channelSettingBinding) (channelSettingBinding, error) {
	key := "connector/telegram/" + binding.Target.ApplicationID + "/setting-card/" + operation
	if _, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, string(mustJSON(binding))); err != nil {
		return binding, err
	}
	var raw string
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
		return binding, err
	}
	var saved channelSettingBinding
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return binding, ErrStoreFormat
	}
	if saved.Revision != binding.Revision || saved.Target != binding.Target || saved.Run != binding.Run || saved.Generation != binding.Generation {
		return binding, ErrControlStaleRevision
	}
	return saved, nil
}

func (s *channelSession) sessionGeneration() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (tasks *TaskService) expireChannelSettingCards(ctx context.Context, target ChannelTarget, generations ...string) error {
	if target.Source != SpaceSourceTelegram {
		return nil
	}
	people, err := tasks.TelegramConfirmers(ctx, target.ApplicationID)
	if err != nil {
		return err
	}
	prefix := "connector/telegram/" + target.ApplicationID + "/setting-card/"
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT m.key,m.value FROM fa.meta m JOIN fa.runs r ON r.id=m.document->>'run_id' WHERE m.key>=$1 AND m.key<$2 AND m.document#>>'{target,chat_id}'=$3 AND coalesce((m.document->>'closed')::boolean,false)=false AND (r.status='waiting_confirmation' OR m.document->>'detached'='true') LIMIT 128`, prefix, prefix+"\uffff", target.ChatID)
	if err != nil {
		return err
	}
	type pending struct {
		op      string
		binding channelSettingBinding
	}
	items := []pending{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var binding channelSettingBinding
		if json.Unmarshal([]byte(raw), &binding) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		items = append(items, pending{strings.TrimPrefix(key, prefix), binding})
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	for _, item := range items {
		if item.binding.Target != target {
			continue
		}
		code := ""
		if time.Now().After(item.binding.Expires) {
			code = "expired"
		}
		if item.binding.Revision != people.Revision {
			code = "confirmation_people_changed"
		}
		if len(generations) > 0 && item.binding.Generation != generations[0] {
			code = "connection_changed"
		}
		if item.binding.AccessRevision != 0 {
			grant, e := tasks.CheckTelegramAccess(ctx, target, item.binding.Person)
			if e != nil && !errors.Is(e, ErrPermissionDenied) {
				return e
			}
			if e != nil || grant.Revision != item.binding.AccessRevision {
				code = "access_changed"
			}
		}
		if code == "" {
			continue
		}
		record, ok := control.operations[item.op]
		if !ok || record.Operation.Status != ControlPending {
			continue
		}
		if err := control.invalidateProposalLocked(ctx, &record, code); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tasks.closeSettingBinding(ctx, target.ApplicationID, item.op); err != nil {
			return err
		}
	}
	return nil
}

func (s *channelSession) settingCard(ctx context.Context, run Run, confirmation Confirmation) (string, []channelButton, error) {
	control, err := s.tasks.Control()
	if err != nil {
		return "", nil, err
	}
	record, ok := control.operationForAttempt(confirmation.Attempt)
	if !ok {
		return "", nil, ErrControlOperationNotFound
	}
	if !s.supportsButtons() || s.tasks.channelSettingRecordEligible(ctx, run, record) != nil {
		text := "这项设置请在认证管理页面办理，尚未应用。"
		web := s.tasks.Config().PublicWeb.Effective()
		if web.Enabled && web.Origin != "" {
			text += "\n" + strings.TrimRight(web.Origin, "/") + "/?chat=" + url.QueryEscape(run.ConversationID)
		}
		return text, nil, nil
	}
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || run.Permission == nil {
		return "", nil, ErrPermissionDenied
	}
	space, err := s.tasks.ChannelSpace(ctx, origin.ChannelTarget)
	if err != nil {
		return "", nil, err
	}
	group := space.ChatKind == "group"
	people, err := s.tasks.TelegramConfirmers(ctx, origin.ApplicationID)
	if err != nil {
		return "", nil, err
	}
	var args struct {
		Model        string              `json:"model_id"`
		Capabilities CapabilitySelection `json:"capabilities"`
	}
	if json.Unmarshal(record.Arguments, &args) != nil {
		return "", nil, ErrStoreFormat
	}
	text := "修改当前对话的一个扩展选项；其他能力保持原值。下一轮生效。"
	if record.Operation.Kind == ControlSetConversationModel {
		text = fmt.Sprintf("当前对话模型：%s → %s。下一轮生效。", run.ModelID, args.Model)
	} else {
		var before CapabilitySelection
		if json.Unmarshal(record.Before["conversation.capabilities:"+run.ConversationID], &before) != nil {
			return "", nil, ErrStoreFormat
		}
		id, key, value, e := tgpolicy.OneOption(mustJSON(before.normalize()), mustJSON(args.Capabilities.normalize()))
		if e != nil {
			return "", nil, e
		}
		previous := before.Variants[id]
		if key != "" {
			previous = before.Settings[id][key]
		}
		if previous == "" {
			previous = "继承扩展设置"
		}
		label := key
		if label == "" {
			label = "角色"
		}
		text = "当前对话 · " + id + " · " + label + "：" + previous + " → " + value + "。下一轮生效；其他能力保持原值。"
	}
	expires := time.Now().Add(telegramCallbackTTL)
	if at, e := time.Parse(time.RFC3339Nano, record.Operation.ExpiresAt); e == nil && at.Before(expires) {
		expires = at
	}
	binding, err := s.tasks.bindSettingCard(ctx, record.Operation.ID, channelSettingBinding{Detached: group, Run: run.ID, Target: origin.ChannelTarget, Revision: people.Revision, Expires: expires, AccessRevision: origin.AccessRevision, Person: run.Permission.PersonID, Generation: s.sessionGeneration()})
	if err != nil {
		if errors.Is(err, ErrControlStaleRevision) {
			control.mu.Lock()
			saved := control.operations[record.Operation.ID]
			e := control.invalidateProposalLocked(ctx, &saved, "connection_changed")
			control.mu.Unlock()
			if e != nil {
				return "", nil, e
			}
			return "设置预览已失效，请按当前值重新提出。", nil, nil
		}
		return "", nil, err
	}
	expires = binding.Expires
	var buttons []channelButton
	for _, approved := range []bool{true, false} {
		token, e := s.button(channelApproval{Confirmation: confirmation, Target: origin.ChannelTarget, Person: run.Permission.PersonID, Approved: approved, Expires: expires, SettingsRevision: people.Revision})
		if e != nil {
			return "", nil, e
		}
		label := "拒绝"
		if approved {
			label = "保存设置"
		}
		buttons = append(buttons, channelButton{Text: label, Token: token})
	}
	if group {
		text += "\n由本群已准入的设置确认人员决定；原发起人可撤回。等待期间可以继续聊天。"
		token, e := s.button(channelApproval{Withdraw: true, Confirmation: confirmation, Target: origin.ChannelTarget, Person: run.Permission.PersonID, Expires: expires, SettingsRevision: people.Revision})
		if e != nil {
			return "", nil, e
		}
		buttons = append(buttons, channelButton{Text: "撤回提案", Token: token})
	}
	return text, buttons, nil
}

func (s *channelSession) confirmSetting(ctx context.Context, e channelEvent, item channelApproval) error {
	run, origin, err := s.tasks.ChannelRun(ctx, item.Confirmation.RunID)
	if err != nil || origin == nil || origin.ChannelTarget != e.Target || run.Permission == nil {
		return ErrPermissionDenied
	}
	space, err := s.tasks.ChannelSpace(ctx, e.Target)
	if err != nil {
		return err
	}
	group := space.ChatKind == "group"
	if !group && run.Permission.PersonID != e.Person || item.Withdraw && (!group || run.Permission.PersonID != e.Person) {
		return ErrPermissionDenied
	}
	// Shared lock orders this decision with an owner's roster revocation.
	// Revocation cannot commit between the last check and the setting write.
	key, err := s.tasks.confirmerKey(e.Target.ApplicationID)
	if err != nil {
		return err
	}
	rosterLock, err := s.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rosterLock.Rollback()
	var lockedRaw string
	if err := rosterLock.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR SHARE`, key).Scan(&lockedRaw); err != nil {
		return err
	}
	var lockedPeople ChannelConfirmers
	if json.Unmarshal([]byte(lockedRaw), &lockedPeople) != nil {
		return ErrStoreFormat
	}
	if lockedPeople.Revision != item.SettingsRevision || !item.Withdraw && !slices.Contains(lockedPeople.Members, e.Person) {
		channelFeedback(ctx, "确认名单已改变，此卡不再有效；请重新提出。")
		return ErrPermissionDenied
	}
	people, err := s.tasks.TelegramConfirmers(ctx, e.Target.ApplicationID)
	if err != nil {
		return err
	}
	if people.Revision != item.SettingsRevision || !item.Withdraw && !slices.Contains(people.Members, e.Person) {
		return ErrPermissionDenied
	}
	control, err := s.tasks.Control()
	if err != nil {
		return err
	}
	record, ok := control.operationForAttempt(item.Confirmation.Attempt)
	if !ok {
		channelFeedback(ctx, "这项设置已经处理或已失效，请查看当前值。")
		return nil
	}
	if record.Operation.Status != ControlPending {
		channelFeedback(ctx, "这项设置已经决定，请查看已保存值。")
		return nil
	}
	if err := s.tasks.channelSettingRecordEligible(ctx, run, record); err != nil {
		var notice *controlFlowNotice
		if errors.As(err, &notice) {
			control.mu.Lock()
			saved := control.operations[record.Operation.ID]
			closeErr := control.invalidateProposalLocked(ctx, &saved, "scope_changed")
			control.mu.Unlock()
			if closeErr != nil {
				return closeErr
			}
			channelFeedback(ctx, notice.text)
			return s.plain(ctx, e, notice.text)
		}
		return err
	}
	principal := "channel/" + e.Person + "/" + fmt.Sprint(ctx.Value(channelEventKey{}))
	capability, err := control.IssueInteractiveCapability(ctx, run.ConversationID, principal)
	if err != nil {
		return err
	}
	confirmation, err := control.renderConfirmation(ctx, record.Operation.ID, run.ConversationID, principal, true)
	if err != nil {
		return err
	}
	auth := ControlAuthorization{Source: ControlUserDirect, ConversationID: run.ConversationID, Principal: principal, Code: confirmation.Code, Capability: capability, finishWithoutModel: true, acceptSatisfied: true}
	auth.revalidate = func(ctx context.Context) error {
		if item.Generation != s.sessionGeneration() {
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
				current = current || connection.session == s
			}
			bridge.mu.Unlock()
			if !current {
				return ErrPermissionDenied
			}
		}
		grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
		if err != nil {
			return err
		}
		if grant.Revision != origin.AccessRevision {
			return ErrPermissionRevoked
		}
		if err := s.tasks.checkChannelRunDestination(ctx, run, e.Target); err != nil {
			return errors.Join(ErrPermissionRevoked, err)
		}
		if item.Approved {
			if err := s.tasks.channelSettingRecordEligible(ctx, run, record); err != nil {
				return errors.Join(ErrPermissionRevoked, err)
			}
		}
		return nil
	}
	if group && record.Operation.Detached {
		var result ControlResult
		if item.Approved && !item.Withdraw {
			result, err = control.confirmRunProposal(ctx, record.Operation.ID, auth)
		} else {
			result, err = control.rejectControlChange(ctx, record.Operation.ID, auth, true)
		}
		_ = rosterLock.Rollback()
		if err != nil {
			if errors.Is(err, ErrControlStaleRevision) || errors.Is(err, ErrControlExpired) || errors.Is(err, ErrControlAlreadyUsed) {
				channelFeedback(ctx, "这张预览已失效，请按当前值重新提出。")
				return nil
			}
			if errors.Is(err, ErrControlBusy) {
				channelFeedback(ctx, "暂时无法提交，请稍后再点原卡。设置未修改。")
				return nil
			}
			return err
		}
		if err = s.tasks.closeSettingBinding(ctx, e.Target.ApplicationID, record.Operation.ID); err != nil {
			return err
		}
		text := "已拒绝，设置未更改。"
		if item.Withdraw {
			text = "提案已撤回，设置未更改。"
		} else if result.Applied {
			text = "已保存，下一轮生效。"
		} else if result.ErrorCode == "target_already_satisfied" {
			text = "已由另一笔修改完成，本提案没有再次写入。"
		}
		if _, err = s.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.runs SET output=$1,updated_at=$2 WHERE id=$3 AND status='completed' AND stop_reason=$4`, text, nowText(), run.ID, detachedProposalReason); err != nil {
			return err
		}
		channelFeedback(ctx, text)
		return s.plain(ctx, e, text)
	}
	result, err := s.tasks.ConfirmControl(ctx, run.ID, item.Confirmation, auth, item.Approved)
	_ = rosterLock.Rollback()
	if errors.Is(err, ErrControlStaleRevision) || errors.Is(err, ErrControlExpired) || errors.Is(err, ErrApprovalChanged) {
		channelFeedback(ctx, "预览已失效，请重新查看设置；本次没有写入。")
		return s.plain(ctx, e, "设置预览已失效，请按当前值重新提出；本次没有写入。")
	}
	if err != nil {
		return err
	}
	channelFeedback(ctx, result.Answer)
	s.mu.Lock()
	for token, card := range s.callbacks {
		if card.Confirmation.ControlOperationID == record.Operation.ID {
			delete(s.callbacks, token)
		}
	}
	s.mu.Unlock()
	return s.Deliver(ctx, result)
}

func (tasks *TaskService) closeSettingBinding(ctx context.Context, bot, operation string) error {
	_, err := tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{closed}','true'::jsonb,true)::text WHERE key=$1`, "connector/telegram/"+bot+"/setting-card/"+operation)
	return err
}
