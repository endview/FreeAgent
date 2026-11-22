package harness

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/endview/freeagent/internal/channelwire"
	"strings"
	"sync"
	"time"
)

// ChannelDispatch is installed by the Owner. A transport never owns an executor.
type ChannelDispatch func(string, func(context.Context) (RunResult, error)) error
type channelButton struct{ Text, Token string }
type channelSend func(context.Context, ChannelTarget, string, string, []channelButton) (string, error)
type channelEvent struct {
	Sticker                           *channelwire.Sticker
	Media                             []channelwire.MediaRef
	ReplyTo                           string
	Target                            ChannelTarget
	Person, Name, Message, Kind, Text string
	Trigger                           bool
	Callback, CallbackMessage         string
}
type channelApproval struct {
	Withdraw         bool
	Generation       string
	SettingsRevision int64
	Confirmation     Confirmation
	Target           ChannelTarget
	Person, Message  string
	Approved         bool
	Expires          time.Time
}

// channelSession contains only volatile transport capabilities. All durable
// authority, messages and execution receipts live in the existing Harness.
type channelSession struct {
	generation          string
	deliveryMu          sync.Mutex
	renderText          func(string) ([]channelwire.TextPart, error)
	formatVersion       string
	remote              bool
	download            func(context.Context, channelEvent, channelwire.MediaRef, int64) ([]byte, error)
	sendFile            func(context.Context, ChannelTarget, string, AttachmentRef, []byte) (string, error)
	tasks               *TaskService
	mu                  sync.Mutex
	pair                telegramPair
	callbacks           map[string]channelApproval
	dispatch            ChannelDispatch
	send                channelSend
	source, application string
	sendGate            chan struct{}
	nextSend            time.Time
	targets             map[ChannelTarget]time.Time
	buttons             bool
	busy                map[ChannelTarget]string
}

func newChannelSession(tasks *TaskService, source string, send channelSend) *channelSession {
	s := &channelSession{tasks: tasks, source: source, buttons: true, sendGate: make(chan struct{}, 1), targets: map[ChannelTarget]time.Time{}, callbacks: map[string]channelApproval{}, busy: map[ChannelTarget]string{}}
	s.send = func(ctx context.Context, target ChannelTarget, reply, text string, buttons []channelButton) (string, error) {
		if s.remote {
			return send(ctx, target, reply, text, buttons)
		}
		select {
		case s.sendGate <- struct{}{}:
			defer func() { <-s.sendGate }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
		now := time.Now()
		next := s.nextSend
		if at := s.targets[target]; at.After(next) {
			next = at
		}
		delay := time.Until(next)
		if delay > 4*time.Second {
			return "", &CallError{Certain: true, Message: "channel: rate limited; delivery not retried"}
		}
		if delay > 0 && !channelDelay(ctx, delay) {
			return "", ctx.Err()
		}
		id, err := send(ctx, target, reply, text, buttons)
		s.nextSend = time.Now().Add(50 * time.Millisecond)
		if len(s.targets) >= 128 {
			for t, at := range s.targets {
				if now.After(at) {
					delete(s.targets, t)
				}
			}
		}
		if len(s.targets) < 128 {
			interval := time.Second
			if space, err := tasks.ChannelSpace(ctx, target); err == nil && space.ChatKind == "group" {
				interval = 3 * time.Second
			}
			s.targets[target] = time.Now().Add(interval)
		}
		var transport interface{ RetryDelay() time.Duration }
		if errors.As(err, &transport) && transport.RetryDelay() > time.Second {
			s.nextSend = time.Now().Add(transport.RetryDelay())
		}
		return id, err
	}
	return s
}
func (s *channelSession) PairCode() (string, time.Time, error) {
	if s.source == SpaceSourceTelegram {
		return "", time.Time{}, ErrPermissionDenied
	}
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	code := strings.ToUpper(hex.EncodeToString(raw[:]))
	expires := time.Now().UTC().Add(telegramPairTTL)
	s.mu.Lock()
	s.pair = telegramPair{Code: code, CodeHash: sha256.Sum256([]byte(code)), Expires: expires}
	s.mu.Unlock()
	return code, expires, nil
}
func (s *channelSession) reset(application string) {
	s.mu.Lock()
	s.generation = newID()
	s.application = application
	s.callbacks = map[string]channelApproval{}
	s.busy = map[ChannelTarget]string{}
	s.mu.Unlock()
}
func (s *channelSession) setDispatch(dispatch ChannelDispatch) {
	s.mu.Lock()
	s.dispatch = dispatch
	s.mu.Unlock()
}
func (s *channelSession) plain(ctx context.Context, e channelEvent, text string) error {
	if s.source == SpaceSourceTelegram {
		s.tasks.core.Store.telegramMu.Lock()
		grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
		if err != nil {
			s.tasks.core.Store.telegramMu.Unlock()
			return err
		}
		ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: e.Target, Person: e.Person, Revision: grant.Revision})
		if s.remote {
			s.tasks.core.Store.telegramMu.Unlock()
		} else {
			defer s.tasks.core.Store.telegramMu.Unlock()
		}
	}
	_, err := s.send(ctx, e.Target, e.Message, text, nil)
	if isChannelQueued(err) {
		return nil
	}
	return err
}
func (s *channelSession) handle(ctx context.Context, e channelEvent) error {
	if e.Target.validate() != nil || e.Target.Source != s.source || e.Callback == "" && (!validChannelPart(e.Message) || len(e.Text) > 32<<10) {
		return ErrInvalidSpace
	}
	s.mu.Lock()
	application := s.application
	s.mu.Unlock()
	if !s.matchesApp(application, e.Target.ApplicationID) {
		return ErrPermissionDenied
	}
	if s.source == SpaceSourceTelegram {
		if _, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person); err != nil {
			if !errors.Is(err, ErrPermissionDenied) {
				return err
			}
			if e.Callback != "" || !e.Trigger {
				return nil
			}
			words := strings.Fields(e.Text)
			if len(words) == 0 || strings.ToLower(strings.SplitN(words[0], "@", 2)[0]) != "/start" {
				_, err := s.send(ctx, e.Target, e.Message, "尚未获准访问。请发送 /start 申请，由管理员核对并批准。", nil)
				return err
			}
			request, notify, err := s.tasks.requestTelegramAccess(e, time.Now().UTC())
			if err != nil {
				text := "申请未创建：暂时无法处理，请管理员检查渠道诊断。"
				switch {
				case errors.Is(err, errTelegramRequestCapacity):
					text = "申请未创建：待处理申请已满。请管理员处理已有申请，或等申请到期后重试。"
				case errors.Is(err, errTelegramBotUnbound):
					text = "申请未创建：Bot 身份尚未绑定，请管理员重新连接渠道。"
				case errors.Is(err, ErrPermissionDenied):
					text = "申请未创建：无法核实个人身份。请使用本人账号发送；匿名群身份不能申请。"
				}
				_, sendErr := s.send(ctx, e.Target, e.Message, text, nil)
				// A classified refusal has been handled, so it must not pin the
				// polling cursor and starve every later group/private update.
				if errors.Is(err, errTelegramRequestCapacity) || errors.Is(err, errTelegramBotUnbound) || errors.Is(err, ErrPermissionDenied) {
					return sendErr
				}
				return errors.Join(err, sendErr)
			}
			if !notify {
				return nil
			}
			_, err = s.send(ctx, e.Target, e.Message, "接入申请已提交，等待管理员批准。申请码："+request.Code+"（1 小时有效；仅用于身份核对，不能兑换权限）。", nil)
			return err
		}
	}
	if s.source == SpaceSourceTelegram {
		if err := s.tasks.expireChannelSettingCards(ctx, e.Target, s.sessionGeneration()); err != nil {
			return err
		}
	}
	if e.Callback != "" {
		return s.callback(ctx, e)
	}
	words := strings.Fields(e.Text)
	cmd := ""
	if len(words) > 0 {
		cmd = strings.ToLower(strings.SplitN(words[0], "@", 2)[0])
	}
	if cmd == "/pair" && s.source != SpaceSourceTelegram {
		if e.Person == "" {
			return ErrPermissionDenied
		}
		key := "channel/pair-receipt/" + contentHash(mustJSON([]string{e.Target.Source, e.Target.ApplicationID, e.Target.ChatID, e.Message}))
		claimed, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, "received")
		if err != nil {
			return err
		}
		n, _ := claimed.RowsAffected()
		if n == 0 {
			return nil
		}
		if len(words) != 2 {
			return s.plain(ctx, e, "配对格式：/pair <本机配对码>")
		}
		digest := sha256.Sum256([]byte(strings.ToUpper(words[1])))
		s.mu.Lock()
		p := s.pair
		valid := !p.Used && p.Code != "" && p.CodeHash == digest && time.Now().Before(p.Expires)
		if valid {
			s.pair.Used = true
			s.pair.Code = ""
		} else {
			s.pair.Attempts++
			if s.pair.Attempts >= telegramPairAttempts {
				s.pair.Code = ""
			}
		}
		s.mu.Unlock()
		if !valid {
			return s.plain(ctx, e, "配对码无效或已过期。")
		}
		kind := e.Kind
		if kind != "private" {
			kind = "group"
		}
		space, err := s.tasks.PairChannel(ctx, e.Target, kind, s.source+" "+e.Target.ChatID)
		if err != nil {
			return err
		}
		return s.plain(ctx, e, "已配对到空间“"+space.Title+"”。默认 None，可聊天，操作需本机授权。")
	}
	space, err := s.tasks.ChannelSpace(ctx, e.Target)
	if err != nil {
		if e.Trigger {
			return s.plain(ctx, e, "此聊天尚未配对。请在本机生成配对码，再发送 /pair <code>。")
		}
		return nil
	}
	purpose := "background"
	if e.Trigger {
		purpose = "request"
	}
	if strings.HasPrefix(cmd, "/") && e.Trigger {
		purpose = "command"
	}
	if e.Sticker != nil {
		chat, err := s.tasks.ChannelConversation(ctx, e.Target)
		if err != nil {
			return err
		}
		if intent, ok := ctx.Value(channelReplayKey{}).(channelInputIntent); ok && intent.ConversationID != "" {
			chat = intent.ConversationID
		}
		ref, err := s.tasks.rememberSticker(ctx, e.Target, *e.Sticker, chat)
		if err != nil {
			return err
		}
		e.Text += " [可回复的贴纸引用=" + ref.ID + "]"
		selected, _ := s.tasks.SelectedModel(ctx, chat)
		config := s.tasks.Config()
		if selected == "auto" || selected == "" {
			selected = config.DefaultModel
		}
		if !config.Models[selected].Capabilities.Vision.Enabled {
			e.Media = nil
			e.Text += " [当前模型未声明视觉能力；本轮只提供元信息，未识别画面]"
		}
	}
	received, err := s.tasks.ReceiveChannelText(ctx, ChannelMessageSource{ChannelTarget: e.Target, PersonID: e.Person, DisplayName: e.Name, MessageID: e.Message, Purpose: purpose, Media: e.Media, ReplyTo: e.ReplyTo}, e.Text)
	if received.Duplicate && received.RunID == "" && purpose == "request" {
		if _, replay := ctx.Value(channelReplayKey{}).(channelInputIntent); replay {
			revision := uint64(0)
			if grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person); err == nil {
				revision = grant.Revision
			}
			received.Duplicate = false
			received.origin = &ChannelRunOrigin{ChannelTarget: e.Target, InputMessageID: received.MessageID, HistoryThrough: received.MessageID, RequestKey: ChannelMessageSource{ChannelTarget: e.Target, MessageID: e.Message}.requestKey(), AccessRevision: revision}
			received.source = ChannelMessageSource{ChannelTarget: e.Target, PersonID: e.Person, DisplayName: e.Name, MessageID: e.Message, Purpose: purpose, Media: e.Media, ReplyTo: e.ReplyTo}
			received.text = e.Text
		}
	}
	if err != nil || received.Duplicate || !e.Trigger {
		return err
	}
	if purpose == "command" {
		return s.command(ctx, e, space, received.ConversationID, cmd, words[1:])
	}
	if len(e.Media) > 0 {
		if err := s.receiveFiles(ctx, e, &received); err != nil {
			return s.plain(ctx, e, "附件未交给模型："+safeError(err)+"。未自动重试。")
		}
	}
	return s.launchInput(ctx, e, received)
}
func (s *channelSession) launch(ctx context.Context, e channelEvent, chat string, call func(context.Context) (RunResult, error)) error {
	call = s.withFeedback(e, call)
	s.mu.Lock()
	dispatch := s.dispatch
	s.mu.Unlock()
	if dispatch != nil {
		err := dispatch(chat, call)
		if errors.Is(err, ErrActiveRun) {
			s.mu.Lock()
			first := s.busy[e.Target] == ""
			if len(s.busy) < 128 {
				s.busy[e.Target] = e.Message
			}
			s.mu.Unlock()
			if first {
				notice := s.plain(ctx, e, "忙碌或当前对话尚未结束；本条未排队，请空闲后重新发送。")
				if s.remote {
					return errors.Join(errChannelInputRejected, notice)
				}
				return notice
			}
			return nil
		}
		return err
	}
	result, err := call(ctx)
	if result.Run.ID != "" {
		return errors.Join(err, s.Deliver(ctx, result))
	}
	if errors.Is(err, ErrActiveRun) {
		return s.plain(ctx, e, "忙碌；本条未排队，请稍后重新发送。")
	}
	return err
}
func (s *channelSession) command(ctx context.Context, e channelEvent, space Space, chat, cmd string, args []string) error {
	sp, err := s.tasks.SpacePermission(ctx, space.ID)
	if err != nil {
		return err
	}
	person := PersonPermission{Mode: PermissionNone}
	if e.Person != "" {
		person, err = s.tasks.PersonPermission(ctx, e.Person)
	}
	if err != nil {
		return err
	}
	effective := lowerPermission(sp.Mode, person.Mode)
	if handled, err := s.inputCommand(ctx, e, space, chat, cmd, args, effective); handled {
		return err
	}
	switch cmd {
	case "/context":
		core, readErr := s.tasks.contextReader(ctx, chat)
		if readErr != nil {
			return readErr
		}
		if readErr = core.resolveRequestPermission(ctx, chat, e.Person, e.Target.Source); readErr != nil {
			return readErr
		}
		state, readErr := NewTaskService(core).ContextState(ctx, chat)
		if readErr != nil {
			return s.plain(ctx, e, "上下文状态读取失败，请在本机核对记录。")
		}
		text := fmt.Sprintf("上下文：%s；摘要上限=%d UTF-8 字节；请求字节保护=%d；输出预留=%d tokens。", state.Status, state.ByteLimit, state.Window.ByteLimit, state.Window.OutputReserve)
		if state.LastRequest != nil {
			text += fmt.Sprintf("\n最近准备请求：占用=%d%%，%d 字节，估算输入=%d tokens（不是实时值）。", state.LastRequest.Occupancy, state.LastRequest.Bytes, state.LastRequest.EstimatedTokens)
		}
		if state.TargetMode == "auto" {
			text += fmt.Sprintf("\n摘要采用自动软目标，约 %d tokens；字节保护单独生效。", state.TargetTokens)
		}
		if state.Status == "stale" {
			text += "\n旧摘要需要按当前授权视图重建，可用 /compact refresh；原记录保留。"
		}
		if state.AutomaticPaused != "" {
			text += "\n" + state.AutomaticPaused
		}
		if state.Blocked != "" {
			text += "\n存在运行任务、待确认或未知结果，请先处理该任务。"
		}
		text += "\n可用 /compact 手动压缩；已开启多对话时可用 /new。修改模型窗口与辅助思考请打开设置。"
		return s.plain(ctx, e, text)
	case "/analyze":
		if space.ChatKind == "group" {
			return s.plain(ctx, e, "群会话不做经验总结或将成员发言写入 Memory。请在管理端选择已获授权的独立资料。")
		}
		return s.sourceAnalysisCommand(ctx, e, chat, args)
	case "/save-reply":
		return s.saveReplyFile(ctx, e, chat)
	case "/mode":
		profile, profileErr := buildExecutionProfile(s.tasks.Config(), PermissionSelection{Mode: effective, Source: "space"})
		if profileErr != nil {
			return profileErr
		}
		facts := factsForProfile(profile, effectiveBuiltinToolSettings(s.tasks.Config()), PermissionSelection{Mode: effective, PersonID: e.Person, Entry: e.Target.Source})
		text := fmt.Sprintf("下一次请求：空间=%s，人员=%s，有效权限=%s；命令=%s，后端=%s，命令网络=%s。", sp.Mode, person.Mode, effective, facts.Commands.Status, facts.Commands.Backend, facts.Network)
		if run, err := s.tasks.Latest(ctx, chat); err == nil {
			if frozen, err := s.tasks.RuntimeFacts(ctx, chat, run.ID); err == nil {
				text += fmt.Sprintf("\n最近 Run=%s，入口=%s，发起人=%s，冻结权限=%s，后端=%s；当前阻断=%s。", run.ID, frozen.Entry, frozen.PersonID, frozen.Permission, frozen.Commands.Backend, frozen.DispatchBlocked)
			}
		}
		return s.plain(ctx, e, text)
	case "/help", "/start":
		if e.Target.Source == SpaceSourceTelegram {
			return s.plain(ctx, e, "运行中直接发送消息即可补充当前任务。\n/mode /status /context /stop /continue /abandon /compact /save-reply /inputs /history /branch 消息ID；开启多对话：/multi on；/sessions /new [标题] /switch ID /default ID。产品设置与敏感审批在本机确认。")
		}
		return s.plain(ctx, e, "/mode /status /context /stop /continue /abandon /compact /save-reply /steer 文本 /queue 文本 /inputs /queue-cancel key /queue-resume /history /branch 消息ID；开启多对话：/multi on；/sessions /new [标题] /switch ID /default ID。产品设置与敏感审批在本机确认。")
	case "/status":
		run, err := s.tasks.Latest(ctx, chat)
		if err != nil {
			return s.plain(ctx, e, "当前对话没有运行记录。")
		}
		answer := run.Output
		if run.Permission == nil || run.Permission.PersonID != e.Person || s.tasks.checkTelegramRun(ctx, run) != nil {
			answer = "此入口只显示已确认公开的回复；未投递或管理员私有结果请在本机核对。"
			var publicText string
			if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT (body_json #>> '{Content}') FROM `+conversationRecords+` WHERE conversation_id=$1 AND request_key LIKE $2 AND ((body_json #>> '{Public}')::boolean)=true ORDER BY id DESC LIMIT 1`, chat, "channel/delivery/"+run.ID+"/%").Scan(&publicText); err == nil {
				answer = publicText
			}
		}
		if len(answer) > 12000 {
			answer = string([]rune(answer)[:min(len([]rune(answer)), 3000)]) + "\n[已截断]"
		}
		activity := ""
		if run.Permission != nil && run.Permission.PersonID == e.Person && s.tasks.checkTelegramRun(ctx, run) == nil {
			activity = fmt.Sprintf("\n步骤=%d，活动耗时=%dms", run.Steps, activeElapsed(run))
			if view, err := s.tasks.Chat(ctx, chat, ""); err == nil {
				for _, turn := range view.Turns {
					if turn.Key == run.ID {
						start := max(0, len(turn.Activity)-8)
						for _, a := range turn.Activity[start:] {
							if a.Kind == "tool" {
								duration := "耗时未记录"
								if a.DurationMS != nil {
									duration = fmt.Sprintf("%dms", *a.DurationMS)
								}
								activity += fmt.Sprintf("\n%s：%s，%s", a.Name, a.Status, duration)
							}
						}
					}
				}
			}
			core, readErr := s.tasks.contextReader(ctx, chat)
			if readErr == nil {
				readErr = core.resolveRequestPermission(ctx, chat, e.Person, e.Target.Source)
			}
			if readErr == nil && !core.scopedHistory() {
				if report, err := core.unresolvedReport(ctx, run); err == nil && report != "" {
					activity += "\n" + report
				}
				if groups, err := NewTaskService(core).ChannelDeliveries(ctx, chat); err == nil {
					shown := 0
					for _, group := range groups {
						if text := group.issueSummary(); text != "" {
							activity += "\n" + text
							shown++
							if shown == 3 {
								break
							}
						}
					}
				}
			}
		}
		return s.plain(ctx, e, "当前对话="+chat+"，Run="+run.ID+"，状态="+run.Status+activity+"\n"+answer)
	case "/multi", "/new", "/switch", "/default", "/sessions":
		if cmd == "/sessions" {
			if !space.MultiConversation {
				return s.plain(ctx, e, "当前为单对话。使用 /multi on 显式开启多对话。")
			}
			chats, err := s.tasks.SpaceConversations(ctx, space.ID)
			if err != nil {
				return err
			}
			var out strings.Builder
			for _, c := range chats {
				target, routeErr := channelConversationTarget(ctx, s.tasks.core.Store.db, c.ID, e.Target)
				if routeErr != nil || target != e.Target {
					continue
				}
				if out.Len() > 12000 {
					break
				}
				fmt.Fprintf(&out, "%s %s\n", c.ID, c.Title)
			}
			return s.plain(ctx, e, out.String())
		}
		if effective == PermissionNone {
			return s.plain(ctx, e, "None 不能整理对话；请在本机授权。")
		}
		switch cmd {
		case "/multi":
			if len(args) != 1 || args[0] != "on" && args[0] != "off" {
				return s.plain(ctx, e, "/multi on 或 /multi off")
			}
			err = s.tasks.SetSpaceMultiConversation(ctx, space.ID, args[0] == "on")
		case "/new":
			if !space.MultiConversation {
				return s.plain(ctx, e, "请先 /multi on。")
			}
			title := strings.Join(args, " ")
			var c Conversation
			request := string(mustJSON([]any{e.Target, e.Message}))
			c, err = s.tasks.newChannelConversation(ctx, e.Target, title, request)
			if err == nil {
				return s.plain(ctx, e, "已创建并切换到："+c.Title+"\n"+c.ID)
			}
			if errors.Is(err, ErrInvalidConversationTitle) {
				return s.plain(ctx, e, "对话名称需为 1–80 个字符，且不能包含换行或控制字符。")
			}
			return s.plain(ctx, e, "未能创建新对话；请用 /sessions 核对当前对话后重试。")
		default:
			if !space.MultiConversation {
				return s.plain(ctx, e, "请先 /multi on。")
			}
			if len(args) != 1 {
				return s.plain(ctx, e, "请提供完整对话 ID。")
			}
			if cmd == "/switch" {
				err = s.tasks.setChannelConversation(ctx, e.Target, args[0], false)
			} else {
				err = s.tasks.setChannelConversation(ctx, e.Target, args[0], true)
			}
		}
		if err != nil {
			return s.plain(ctx, e, "未更改：对话不属于此空间、正在运行或参数无效。")
		}
		return s.plain(ctx, e, "对话设置已更新。")
	case "/stop", "/continue", "/abandon":
		run, err := s.tasks.Latest(ctx, chat)
		if err != nil {
			return s.plain(ctx, e, "没有可操作的 Run。")
		}
		action := strings.TrimPrefix(cmd, "/")
		if _, err = s.tasks.CheckChannelRunAction(ctx, e.Target, e.Person, run.ID, action); err != nil {
			return s.plain(ctx, e, "你无权操作此 Run。")
		}
		if cmd == "/stop" {
			_ = s.tasks.PauseQueue(ctx, chat, true)
			s.tasks.CancelConversation(chat)
			return s.plain(ctx, e, "已请求停止；副作用及 UNKNOWN 请核对运行记录。")
		}
		if cmd == "/abandon" {
			err = s.tasks.Abandon(ctx, run.ID)
			if err != nil {
				return s.plain(ctx, e, "当前 Run 不能放弃，请先停止并核对记录。")
			}
			return s.plain(ctx, e, "已放弃，不自动重试。")
		}
		return s.launch(ctx, e, chat, func(ctx context.Context) (RunResult, error) { return s.tasks.Continue(ctx, run.ID) })
	case "/compact":
		action := "compact"
		if len(args) > 0 {
			if len(args) != 1 || args[0] != "refresh" {
				return s.plain(ctx, e, "使用 /compact；重建过期摘要使用 /compact refresh。")
			}
			action = "refresh"
		}
		return s.launch(ctx, e, chat, func(ctx context.Context) (RunResult, error) {
			return s.tasks.CompactChannel(ctx, chat, e.Person, e.Target, action)
		})
	default:
		return s.plain(ctx, e, "未知命令。使用 /help 查看支持的命令；不会把未知命令交给模型执行。")
	}
}
func (s *channelSession) callback(ctx context.Context, e channelEvent) error {
	s.mu.Lock()
	item, ok := s.callbacks[e.Callback]
	s.mu.Unlock()
	if (!ok || item.Message == "") && s.remote {
		var raw string
		if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, s.callbackKey(e.Callback)).Scan(&raw); err == nil && json.Unmarshal([]byte(raw), &item) == nil {
			ok = true
			s.mu.Lock()
			s.callbacks[e.Callback] = item
			s.mu.Unlock()
		}
	}
	if !ok || item.Message == "" || item.Message != e.CallbackMessage || item.Target != e.Target || item.Person != e.Person && item.Confirmation.ControlOperationID == "" || time.Now().After(item.Expires) {
		if ok && time.Now().After(item.Expires) {
			channelFeedback(ctx, "确认已过期，请重新提出。")
		}
		return ErrPermissionDenied
	}
	if item.Confirmation.ControlOperationID != "" && item.Generation != s.sessionGeneration() {
		channelFeedback(ctx, "连接已更新，这张旧卡已失效；请重新提出。")
		return ErrPermissionDenied
	}
	if item.Confirmation.ControlOperationID != "" {
		return s.confirmSetting(ctx, e, item)
	}
	if _, err := s.tasks.CheckChannelRunAction(ctx, e.Target, e.Person, item.Confirmation.RunID, "approve"); err != nil {
		return err
	}
	s.mu.Lock()
	current, ok := s.callbacks[e.Callback]
	if ok && current.Confirmation.Digest == item.Confirmation.Digest && current.Message == item.Message {
		delete(s.callbacks, e.Callback)
	} else {
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return ErrApprovalChanged
	}
	run, _, err := s.tasks.ChannelRun(ctx, item.Confirmation.RunID)
	if err != nil {
		return err
	}
	call := func(ctx context.Context) (RunResult, error) {
		return s.tasks.Confirm(ctx, item.Confirmation.RunID, item.Confirmation, item.Approved)
	}
	call = s.withFeedback(e, call)
	s.mu.Lock()
	dispatch := s.dispatch
	s.mu.Unlock()
	if dispatch != nil {
		if err := dispatch(run.ConversationID, call); err != nil {
			if errors.Is(err, ErrActiveRun) {
				s.mu.Lock()
				s.callbacks[e.Callback] = item
				s.mu.Unlock()
			}
			return err
		}
		return nil
	}
	result, err := call(ctx)
	if result.Run.ID != "" {
		err = errors.Join(err, s.Deliver(ctx, result))
	}
	return err

}

// A no-op compaction or a failure before Run creation still needs a channel
// response. It must not manufacture a Run or imply an unconfirmed dispatch
// was safe to repeat.
func (s *channelSession) withFeedback(event channelEvent, call func(context.Context) (RunResult, error)) func(context.Context) (RunResult, error) {
	return func(ctx context.Context) (RunResult, error) {
		var progress sync.Once
		ctx = context.WithValue(ctx, runEventObserverKey{}, func(update Event) {
			if update.Kind == "context_compacting" {
				progress.Do(func() { _ = s.plain(ctx, event, "正在压缩当前公开上下文，原始记录保留。") })
			}
			// Only this bounded business notice is public. Other failure events
			// can contain private diagnostics and stay in the local activity view.
			if update.Kind == EventContextCompactionFailed && update.Activity != nil && update.Activity["continued"] != nil {
				_ = s.plain(ctx, event, update.Message)
			}
		})
		result, err := call(ctx)
		if result.Run.ID == "" || result.Duplicate {
			message := "当前无需更改；原始记录保留。"
			if err != nil {
				message = "请求未完成，请查看本机记录并先处理未结束的 Run；未自动重试。"
			}
			if result.Duplicate {
				message = "此 Run 已有保存结果；使用 /status 查看，未重新执行。"
			}
			err = errors.Join(err, s.plain(ctx, event, message))
		}
		return result, err
	}
}
func (s *channelSession) button(item channelApproval) (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := "fa_" + hex.EncodeToString(raw[:])
	if item.Confirmation.ControlOperationID != "" {
		token = "fa_" + contentHash(mustJSON([]any{s.sessionGeneration(), item.Confirmation.ControlOperationID, item.Approved, item.Withdraw}))[:36]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Generation = s.generation
	if existing, ok := s.callbacks[token]; ok && existing.Target == item.Target && existing.Confirmation.Digest == item.Confirmation.Digest {
		return token, nil
	}
	for k, v := range s.callbacks {
		if time.Now().After(v.Expires) {
			delete(s.callbacks, k)
		}
	}
	if len(s.callbacks) >= 128 {
		return "", errors.New("harness: channel callback capacity reached")
	}
	s.callbacks[token] = item
	if s.remote {
		if err := s.saveCallback(token, item); err != nil {
			delete(s.callbacks, token)
			return "", err
		}
	}
	return token, nil
}
func (s *channelSession) Deliver(ctx context.Context, result RunResult) (returnErr error) {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	defer s.notifyBusy(ctx)
	run, origin, err := s.tasks.ChannelRun(ctx, result.Run.ID)
	if err != nil {
		return err
	}
	if origin == nil || origin.Source != s.source {
		return nil
	}
	if err := s.tasks.checkTelegramRun(ctx, run); err != nil {
		return err
	}
	s.mu.Lock()
	application := s.application
	s.mu.Unlock()
	if !s.matchesApp(application, origin.ApplicationID) {
		return nil
	}
	preparationPending := false
	if run.Status != "waiting_confirmation" && !result.Maintenance {
		defer func() {
			if !preparationPending {
				returnErr = errors.Join(returnErr, s.deliverFiles(ctx, run, *origin))
			}
		}()
	}
	text := result.Answer
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	var buttons []channelButton
	defer func() {
		s.mu.Lock()
		for _, button := range buttons {
			if s.callbacks[button.Token].Message == "" {
				delete(s.callbacks, button.Token)
			}
		}
		s.mu.Unlock()
	}()
	revision := run.Status + "/" + contentHash([]byte(text))
	if correlation, _ := ctx.Value(channelEventKey{}).(string); correlation != "" {
		revision += "/request/" + contentHash([]byte(correlation))
	}
	if run.Status == "waiting_confirmation" {
		confirmation, err := s.tasks.Preview(ctx, run.ID)
		if err == nil && confirmation.ControlOperationID != "" {
			text, buttons, err = s.settingCard(ctx, run, confirmation)
			if err != nil {
				return err
			}
			if len(buttons) > 0 {
				if space, e := s.tasks.ChannelSpace(ctx, origin.ChannelTarget); e == nil && space.ChatKind == "group" {
					control, e := s.tasks.Control()
					if e != nil {
						return e
					}
					if e = control.detachProposal(ctx, confirmation.ControlOperationID); e != nil {
						return e
					}
					run, e = s.tasks.core.Store.GetRun(ctx, run.ID)
					if e != nil {
						return e
					}
					revision = "setting/" + confirmation.ControlOperationID
				}
			}
			if run.StopReason != detachedProposalReason {
				revision += "/" + confirmation.Digest
			}
		} else if err != nil || run.Permission == nil || run.Permission.PersonID == "" {
			text = "操作等待本机确认，请打开 GUI 或 CLI 查看。"
		} else if !s.buttons {
			text = "操作等待本机 GUI 或 CLI 确认；此渠道不支持审批按钮。"
		} else {
			text = "需要确认：\n" + confirmation.Preview
			revision += "/" + confirmation.Digest
			for _, approved := range []bool{true, false} {
				token, err := s.button(channelApproval{Confirmation: confirmation, Target: origin.ChannelTarget, Person: run.Permission.PersonID, Approved: approved, Expires: time.Now().Add(telegramCallbackTTL)})
				if err != nil {
					return err
				}
				label := "拒绝"
				if approved {
					label = "批准"
				}
				buttons = append(buttons, channelButton{label, token})
			}
		}
	}
	if run.Status == "completed" && run.StopReason == detachedProposalReason && len(buttons) == 0 {
		confirmation, ok, e := s.tasks.detachedSettingConfirmation(ctx, run)
		if e != nil {
			return e
		}
		if ok {
			revision = "setting/" + confirmation.ControlOperationID
			text, buttons, err = s.settingCard(ctx, run, confirmation)
			if err != nil {
				return err
			}
		}
	}
	expressionOnly := false
	if text == "" && run.Status == "completed" && s.remote {
		if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'run_id'=$1 AND document->>'kind' IN ('sticker','reaction') AND document->>'state' NOT IN ('failed','cancelled'))`, run.ID).Scan(&expressionOnly); err != nil {
			return err
		}
	}
	if text == "" && !expressionOnly {
		text = channelRunStatus(run)
	}
	if run.Status == "limited" {
		text += "\n本轮因限额停止，目标尚未完成；已有文件会继续投递。"
		if run.LimitSettled {
			text += "如需处理剩余工作，请明确发送“继续”或 /continue。"
		}
	}
	// Bound transport output independently of model limits. Do not edit/retry a
	// possibly delivered message; only confirmed sends enter public context.
	if len(text) > 64<<10 {
		text = string([]rune(text)[:min(len([]rune(text)), 16000)]) + "\n[回复已截断；完整结果在本机]"
	}
	group := "reply/" + run.ID + "/" + contentHash([]byte(revision))
	var parts []channelwire.TextPart
	format := ""
	pendingKey := ""
	if s.remote {
		pendingKey, err = s.pendingReply(ctx, group, result, run.Status)
		if err != nil {
			return err
		}
		person := ""
		if run.Permission != nil {
			person = run.Permission.PersonID
		}
		ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: origin.ChannelTarget, Person: person, Revision: origin.AccessRevision, LocalOwner: person == ""})
		ctx = context.WithValue(ctx, channelDeliveryKey{}, channelDeliveryInfo{Run: run.ID, Chat: run.ConversationID, Group: "prepare/" + group, Total: 1, Sequence: 1})
	}
	if !expressionOnly {
		parts, format, err = s.freezeReplyText(ctx, group, text)
		if errors.Is(err, errChannelPreparing) {
			preparationPending = true
			return nil
		}
		if err != nil {
			return err
		}
	}
	gap := 0
	if snapshot, err := decodeRunSnapshot(run.ConfigJSON); err == nil {
		gap = snapshot.Config.Delivery.IntervalMS
	}
	if s.remote {
		if err := s.queueReplyGroup(ctx, run, origin, group, revision, format, parts, buttons, result.Maintenance); err != nil {
			return err
		}
		_, err := s.tasks.core.Store.db.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, pendingKey)
		return err
	}
	totalParts, err := s.stageReplyStickers(ctx, run, group, len(parts))
	if err != nil {
		return err
	}
	for index, part := range parts {
		if err := s.tasks.checkTelegramRun(ctx, run); err != nil {
			return err
		}
		key := "channel/send/" + run.ID + "/" + contentHash([]byte(fmt.Sprintf("%s/%d", revision, index)))
		claim, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, `{"state":"dispatching","replay":false}`)
		if err != nil {
			return err
		}
		n, err := claim.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		b := buttons
		if index != len(parts)-1 {
			b = nil
		}
		sendCtx := context.WithValue(ctx, channelDeliveryKey{}, channelDeliveryInfo{Claim: key, Run: run.ID, Chat: run.ConversationID, Maintenance: result.Maintenance,
			Group: group, Sequence: index + 1, Total: totalParts, Entities: part.Entities, Format: format, LogicalPart: part.LogicalPart, PlatformPart: part.PlatformPart, MinGapMS: gap})
		reply := ""
		if index == 0 {
			reply = s.replyMessage(ctx, run)
		}
		message, sendErr := s.sendRunText(sendCtx, run, origin.ChannelTarget, reply, part.Text, b)
		state, confirmed := "sent", part.Text
		if isChannelQueued(sendErr) {
			state, confirmed, sendErr = "queued", "", nil
		} else if sendErr != nil || message == "" {
			state, confirmed = "unknown", ""
			var transport interface{ DispatchCertain() bool }
			var call *CallError
			if errors.As(sendErr, &transport) && transport.DispatchCertain() || errors.As(sendErr, &call) && call.Certain {
				state = "failed"
			}
		}
		persist, done := context.WithTimeout(context.Background(), 5*time.Second)
		_, saveErr := s.tasks.core.Store.db.ExecContext(persist, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND (document #>> '{state}')='dispatching'`, string(mustJSON(map[string]string{"state": state, "message_id": message})), key)
		if message == "" {
			message = "unknown-" + shortDeliveryID(run.ID, index) + "-" + contentHash([]byte(revision))[:8]
		}
		var recordErr error
		if state != "queued" {
			recordErr = s.tasks.RecordChannelDelivery(persist, run.ID, message, confirmed, state, result.Maintenance)
		}
		done()
		if sendErr == nil && state == "sent" {
			s.mu.Lock()
			for _, button := range b {
				item := s.callbacks[button.Token]
				item.Message = message
				s.callbacks[button.Token] = item
				if s.remote {
					_ = s.saveCallback(button.Token, item)
				}
			}
			s.mu.Unlock()
		}
		if err := errors.Join(sendErr, saveErr, recordErr); err != nil {
			return err
		}
	}

	return nil
}

func (s *channelSession) replyMessage(ctx context.Context, run Run) string {
	var raw string
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil {
		return ""
	}
	err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM `+conversationRecords+` WHERE conversation_id=$1 AND id<=$2 AND (body_json #>> '{Channel,purpose}') IN ('request','command') ORDER BY id DESC LIMIT 1`, run.ConversationID, origin.HistoryThrough).Scan(&raw)
	if err != nil {
		return ""
	}
	var message ModelMessage
	if json.Unmarshal([]byte(raw), &message) != nil || message.Channel == nil {
		return ""
	}
	return message.Channel.MessageID
}

func (s *channelSession) matchesApp(application, target string) bool {
	return application == "" || application == target || s.source == SpaceSourceLark && strings.HasPrefix(target, application+":")
}

func (s *channelSession) notifyBusy(ctx context.Context) {
	s.mu.Lock()
	waiting := s.busy
	s.busy = map[ChannelTarget]string{}
	s.mu.Unlock()
	for target, message := range waiting {
		if ctx.Err() != nil {
			return
		}
		_ = s.plain(ctx, channelEvent{Target: target, Message: message}, "有请求处理结束；之前的忙碌请求未排队，请处理未结束 Run 后重新发送。")
	}
}
