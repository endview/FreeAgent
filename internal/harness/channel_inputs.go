package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (s *channelSession) launchInput(ctx context.Context, e channelEvent, input ChannelReceived) error {
	queue := func(run Run, action string) error {
		command := InputCommand{Action: action, RunID: run.ID, RequestKey: input.origin.RequestKey, Prompt: input.text, Attachments: input.attachments}
		if action == "queue" {
			command.RunID = ""
		}
		item, err := s.tasks.acceptInput(WithAttachmentIdentity(ctx, "channel:"+e.Person), input.ConversationID, command, RunOptions{channel: input.origin, personID: e.Person})
		if err != nil {
			return s.plain(ctx, e, "输入未采用，原文仍保留："+safeError(err))
		}
		text := "已排队，将在当前任务正常完成后处理。"
		if action == "steer" {
			text = "收到补充，接下来会处理。"
			if run.Status == "stopped" || run.Status == "unknown" || run.Status == "limited" {
				text = "补充已保留；请先处理当前暂停的任务，再明确继续。"
			}
		}
		if item.Status == "target_ended" {
			text = "目标 Run 已结束，输入保留；请明确作为新请求发送。"
		}
		if action == "steer" && run.Status == "waiting_confirmation" {
			if err := s.launch(ctx, e, input.ConversationID, func(ctx context.Context) (RunResult, error) { return s.tasks.Continue(ctx, run.ID) }); err != nil {
				return err
			}
		}
		return s.plain(ctx, e, text)
	}
	var active Run
	intent, hasIntent := ctx.Value(channelReplayKey{}).(channelInputIntent)
	var err error
	if hasIntent && intent.TargetRun != "" {
		active, err = s.tasks.core.Store.GetRun(ctx, intent.TargetRun)
	} else if hasIntent {
		err = sql.ErrNoRows
	} else {
		active, err = scanRun(s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate+` LIMIT 1`, input.ConversationID))
	}
	if err == nil {
		var snapshot runSnapshot
		if json.Unmarshal([]byte(active.ConfigJSON), &snapshot) == nil && snapshot.ExtensionTrigger != nil {
			s.tasks.CancelConversation(input.ConversationID)
			return queue(active, "queue")
		}
		action := "queue"
		if active.Permission != nil && active.Permission.PersonID == e.Person {
			action = "steer"
		}
		if e.Target.Source == SpaceSourceTelegram && action == "queue" {
			notice := s.plain(ctx, e, "当前任务由其他成员发起，请等它结束后再发送。")
			if s.remote {
				return errors.Join(errChannelInputRejected, notice)
			}
			return notice
		}
		return queue(active, action)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	call := s.withFeedback(e, func(ctx context.Context) (RunResult, error) {
		return s.tasks.SubmitChannel(WithAttachmentIdentity(ctx, "channel:"+e.Person), input)
	})
	s.mu.Lock()
	dispatch := s.dispatch
	s.mu.Unlock()
	if dispatch != nil {
		if err := dispatch(input.ConversationID, call); errors.Is(err, ErrActiveRun) {
			if e.Target.Source == SpaceSourceTelegram {
				notice := s.plain(ctx, e, "上一条消息还在启动，请稍后补充。")
				if s.remote {
					return errors.Join(errChannelInputRejected, notice)
				}
				return notice
			}
			return queue(Run{}, "queue")
		} else {
			return err
		}
	}
	result, err := call(ctx)
	if errors.Is(err, ErrActiveRun) {
		if e.Target.Source == SpaceSourceTelegram {
			return s.plain(ctx, e, "当前任务正在切换，请稍后补充。")
		}
		return queue(Run{}, "queue")
	}
	if result.Run.ID != "" {
		err = errors.Join(err, s.Deliver(ctx, result))
	}
	return err
}

func (s *channelSession) inputCommand(ctx context.Context, e channelEvent, space Space, chat, cmd string, args []string, effective PermissionMode) (bool, error) {
	if e.Target.Source == SpaceSourceTelegram && (cmd == "/queue" || strings.HasPrefix(cmd, "/queue-")) {
		return true, s.plain(ctx, e, "TG 不使用排队。运行时直接发送补充即可；新任务请等本轮结束后再发送。")
	}
	switch cmd {
	case "/inputs", "/queue":
		if len(args) == 0 {
			state, err := s.tasks.Inputs(ctx, chat)
			if err != nil {
				return true, err
			}
			var text strings.Builder
			if e.Target.Source == SpaceSourceTelegram {
				text.WriteString("补充消息：\n")
			} else {
				fmt.Fprintf(&text, "队列暂停=%v\n", state.Paused)
			}
			for _, i := range state.Entries {
				if i.Principal == "channel:"+e.Person && (e.Target.Source != SpaceSourceTelegram || i.Action == "steer") {
					fmt.Fprintf(&text, "%s %s %s\n", i.Key, i.Action, i.Status)
				}
			}
			return true, s.plain(ctx, e, text.String())
		}
		fallthrough
	case "/steer":
		prompt := strings.TrimSpace(strings.TrimPrefix(e.Text, strings.Fields(e.Text)[0]))
		if prompt == "" {
			return true, s.plain(ctx, e, "请提供输入正文。")
		}
		source := ChannelMessageSource{ChannelTarget: e.Target, PersonID: e.Person, DisplayName: e.Name, MessageID: e.Message, Purpose: "request"}
		var id int64
		if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT id FROM fa.messages WHERE request_key=$1 AND conversation_id=$2 AND run_id IS NULL`, source.requestKey(), chat).Scan(&id); err != nil {
			return true, err
		}
		var revision uint64
		if e.Target.Source == SpaceSourceTelegram {
			grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
			if err != nil {
				return true, err
			}
			revision = grant.Revision
		}
		origin := &ChannelRunOrigin{ChannelTarget: e.Target, InputMessageID: id, HistoryThrough: id, RequestKey: source.requestKey(), AccessRevision: revision}
		action := "queue"
		runID := ""
		if cmd == "/steer" {
			run, err := s.tasks.Latest(ctx, chat)
			if err != nil {
				return true, err
			}
			runID = run.ID
			action = "steer"
		}
		item, err := s.tasks.acceptInput(ctx, chat, InputCommand{Action: action, RunID: runID, RequestKey: source.requestKey(), Prompt: prompt}, RunOptions{channel: origin, personID: e.Person})
		if err != nil {
			return true, s.plain(ctx, e, "未采用："+safeError(err))
		}
		if action == "queue" {
			s.mu.Lock()
			dispatch := s.dispatch
			s.mu.Unlock()
			if dispatch != nil {
				_ = dispatch(chat, func(ctx context.Context) (RunResult, error) { return s.tasks.RunQueued(ctx, chat) })
			}
		}
		return true, s.plain(ctx, e, "输入 "+item.Status+"；/inputs 可查看。")
	case "/queue-pause", "/queue-resume":
		if effective == PermissionNone {
			return true, ErrPermissionDenied
		}
		if err := s.tasks.PauseQueue(ctx, chat, cmd == "/queue-pause"); err != nil {
			return true, err
		}
		if cmd == "/queue-resume" {
			s.mu.Lock()
			dispatch := s.dispatch
			s.mu.Unlock()
			if dispatch != nil {
				_ = dispatch(chat, func(ctx context.Context) (RunResult, error) { return s.tasks.RunQueued(ctx, chat) })
			}
		}
		return true, s.plain(ctx, e, "队列状态已更新。")
	case "/queue-cancel":
		if len(args) != 1 {
			return true, s.plain(ctx, e, "/queue-cancel <输入key>")
		}
		item, err := s.tasks.CancelInput(WithPublicEntry(ctx, "channel:"+e.Person, ""), chat, args[0])
		if err != nil {
			return true, err
		}
		return true, s.plain(ctx, e, item.Status)
	case "/history":
		messages, err := s.tasks.Messages(ctx, chat)
		if err != nil {
			return true, err
		}
		var text strings.Builder
		for _, m := range messages {
			if m.Public && !m.Deferred && (m.Role == "user" || m.Role == "assistant") && text.Len() < 10000 {
				fmt.Fprintf(&text, "[%d] %s: %s\n", m.ID, m.Role, truncateUTF8Bytes(m.Content, 400))
			}
		}
		return true, s.plain(ctx, e, text.String())
	case "/branch", "/branch-reply":
		if effective == PermissionNone {
			return true, ErrPermissionDenied
		}
		if len(args) < 1 {
			return true, s.plain(ctx, e, "/branch <消息ID> [替换文本] 或 /branch-reply <消息ID>")
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return true, err
		}
		mode := "rerun"
		if cmd == "/branch-reply" {
			mode = "continue"
		}
		command := BranchCommand{RequestKey: "channel-branch-" + e.Message, MessageID: id, Mode: mode}
		if len(args) > 1 {
			text := strings.Join(args[1:], " ")
			command.Prompt = &text
		}
		branch, err := s.tasks.BranchConversation(ctx, chat, command)
		if err != nil {
			return true, s.plain(ctx, e, "未创建分支："+safeError(err))
		}
		if err := s.tasks.SetSpaceActiveConversation(ctx, space.ID, branch.Conversation.ID); err != nil {
			return true, err
		}
		if branch.Prompt == "" {
			return true, s.plain(ctx, e, "已切换到新分支；请输入下一条要求。工作区文件不会回滚。")
		}
		source := ChannelMessageSource{ChannelTarget: e.Target, PersonID: e.Person, DisplayName: e.Name, MessageID: e.Message + "-branch", Purpose: "request"}
		input, err := s.tasks.ReceiveChannelText(ctx, source, branch.Prompt)
		if err != nil {
			return true, err
		}
		if input.Duplicate {
			return true, nil
		}
		for _, ref := range branch.Attachments {
			input.attachments = append(input.attachments, ref.ID)
		}
		return true, s.launchInput(ctx, e, input)
	}
	return false, nil
}
