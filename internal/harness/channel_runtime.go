package harness

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/endview/freeagent/internal/channels"
	"strings"
	"sync"
	"time"
)

// ChannelDispatch is installed by the Owner. A transport never owns an executor.
type ChannelDispatch func(string, func(context.Context) (RunResult, error)) error
type channelButton struct{ Text, Token string }
type channelSend func(context.Context, ChannelTarget, string, string, []channelButton) (string, error)
type channelEvent struct {
	Media                             []channels.MediaRef
	ReplyTo                           string
	Target                            ChannelTarget
	Person, Name, Message, Kind, Text string
	Trigger                           bool
	Callback, CallbackMessage         string
}
type channelApproval struct {
	Confirmation    Confirmation
	Target          ChannelTarget
	Person, Message string
	Approved        bool
	Expires         time.Time
}

// channelSession contains only volatile transport capabilities. All durable
// authority, messages and execution receipts live in the existing Harness.
type channelSession struct {
	download            func(context.Context, channelEvent, channels.MediaRef, int64) ([]byte, error)
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
		var tg *channels.TelegramError
		if errors.As(err, &tg) && tg.RetryAfter > time.Second {
			s.nextSend = time.Now().Add(tg.RetryAfter)
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
	if s.source == SpaceSourceTelegram {
		s.tasks.core.Store.telegramIdentity(application)
	}
	s.mu.Lock()
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
		defer s.tasks.core.Store.telegramMu.Unlock()
		if _, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person); err != nil {
			return err
		}
	}
	_, err := s.send(ctx, e.Target, e.Message, text, nil)
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
				_, sendErr := s.send(ctx, e.Target, e.Message, "申请未创建：请求已满或身份不可验证，请稍后重试。", nil)
				return errors.Join(err, sendErr)
			}
			if !notify {
				return nil
			}
			_, err = s.send(ctx, e.Target, e.Message, "接入申请已提交，等待管理员批准。申请码："+request.Code+"（1 小时有效；仅用于身份核对，不能兑换权限）。", nil)
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
		claimed, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, "received")
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
	received, err := s.tasks.ReceiveChannelText(ctx, ChannelMessageSource{ChannelTarget: e.Target, PersonID: e.Person, DisplayName: e.Name, MessageID: e.Message, Purpose: purpose, Media: e.Media}, e.Text)
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
	return s.launch(ctx, e, received.ConversationID, func(ctx context.Context) (RunResult, error) {
		return s.tasks.SubmitChannel(WithAttachmentIdentity(ctx, "channel:"+e.Person), received)
	})
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
				return s.plain(ctx, e, "忙碌或当前对话尚未结束；本条未排队，请空闲后重新发送。")
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
	switch cmd {
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
		return s.plain(ctx, e, "/mode /status /stop /continue /abandon /compact /save-reply；开启多对话：/multi on；/sessions /new 标题 /switch ID /default ID。产品设置与敏感审批在本机确认。")
	case "/status":
		run, err := s.tasks.Latest(ctx, chat)
		if err != nil {
			return s.plain(ctx, e, "当前对话没有运行记录。")
		}
		answer := run.Output
		if run.Permission == nil || run.Permission.PersonID != e.Person || s.tasks.checkTelegramRun(ctx, run) != nil {
			answer = "此入口只显示已确认公开的回复；未投递或管理员私有结果请在本机核对。"
			var publicText string
			if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT json_extract(body_json,'$.Content') FROM messages WHERE conversation_id=? AND request_key LIKE ? AND json_extract(body_json,'$.Public')=1 ORDER BY id DESC LIMIT 1`, chat, "channel/delivery/"+run.ID+"/%").Scan(&publicText); err == nil {
				answer = publicText
			}
		}
		if len(answer) > 12000 {
			answer = string([]rune(answer)[:min(len([]rune(answer)), 3000)]) + "\n[已截断]"
		}
		return s.plain(ctx, e, "当前对话="+chat+"，Run="+run.ID+"，状态="+run.Status+"\n"+answer)
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
			c, err = s.tasks.NewConversationInSpace(ctx, space.ID, title)
			if err == nil {
				err = s.tasks.SetSpaceActiveConversation(ctx, space.ID, c.ID)
			}
		default:
			if !space.MultiConversation {
				return s.plain(ctx, e, "请先 /multi on。")
			}
			if len(args) != 1 {
				return s.plain(ctx, e, "请提供完整对话 ID。")
			}
			if cmd == "/switch" {
				err = s.tasks.SetSpaceActiveConversation(ctx, space.ID, args[0])
			} else {
				err = s.tasks.SetSpaceDefaultConversation(ctx, space.ID, args[0])
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
		return s.launch(ctx, e, chat, func(ctx context.Context) (RunResult, error) {
			return s.tasks.CompactChannel(ctx, chat, e.Person, e.Target)
		})
	default:
		return s.plain(ctx, e, "未知命令。使用 /help 查看支持的命令；不会把未知命令交给模型执行。")
	}
}
func (s *channelSession) callback(ctx context.Context, e channelEvent) error {
	s.mu.Lock()
	item, ok := s.callbacks[e.Callback]
	s.mu.Unlock()
	if !ok || item.Message == "" || item.Message != e.CallbackMessage || item.Target != e.Target || item.Person != e.Person || time.Now().After(item.Expires) {
		return ErrPermissionDenied
	}
	if _, err := s.tasks.CheckChannelRunAction(ctx, e.Target, e.Person, item.Confirmation.RunID, "approve"); err != nil {
		return err
	}
	if item.Confirmation.ControlOperationID != "" {
		return ErrPermissionDenied
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.callbacks {
		if time.Now().After(v.Expires) {
			delete(s.callbacks, k)
		}
	}
	if len(s.callbacks) >= 128 {
		return "", errors.New("harness: channel callback capacity reached")
	}
	s.callbacks[token] = item
	return token, nil
}
func (s *channelSession) Deliver(ctx context.Context, result RunResult) (returnErr error) {
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
	if run.Status != "waiting_confirmation" && !result.Maintenance {
		defer func() { returnErr = errors.Join(returnErr, s.deliverFiles(ctx, run, *origin)) }()
	}
	text := strings.TrimSpace(result.Answer)
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
	if run.Status == "waiting_confirmation" {
		confirmation, err := s.tasks.Preview(ctx, run.ID)
		if err != nil || confirmation.ControlOperationID != "" {
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
	if text == "" {
		text = "Run 状态：" + run.Status + "。UNKNOWN 不会自动重试，请查看本机证据。"
	}
	if run.Status == "limited" {
		text += "\n本轮因限额停止：" + run.StopReason + "。目标尚未完成；已有文件会继续投递。"
		if run.LimitSettled {
			text += "如需处理剩余工作，请明确发送“继续”或 /continue。"
		}
	}
	// Bound transport output independently of model limits. Do not edit/retry a
	// possibly delivered message; only confirmed sends enter public context.
	if len(text) > 64<<10 {
		text = string([]rune(text)[:min(len([]rune(text)), 16000)]) + "\n[回复已截断；完整结果在本机]"
	}
	parts := channels.SplitTelegramText(text)
	for index, part := range parts {
		if err := s.tasks.checkTelegramRun(ctx, run); err != nil {
			return err
		}
		key := "channel/send/" + run.ID + "/" + contentHash([]byte(fmt.Sprintf("%s/%d", revision, index)))
		claim, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, `{"state":"dispatching","replay":false}`)
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
		message, sendErr := s.sendRunText(ctx, run, origin.ChannelTarget, s.replyMessage(ctx, run), part, b)
		state, confirmed := "sent", part
		if sendErr != nil || message == "" {
			state, confirmed = "unknown", ""
			var tg *channels.TelegramError
			var app *channels.APIError
			var call *CallError
			if errors.As(sendErr, &tg) && !tg.Uncertain || errors.As(sendErr, &app) && !app.Uncertain || errors.As(sendErr, &call) && call.Certain {
				state = "failed"
			}
		}
		persist, done := context.WithTimeout(context.Background(), 5*time.Second)
		_, saveErr := s.tasks.core.Store.db.ExecContext(persist, `UPDATE meta SET value=? WHERE key=?`, string(mustJSON(map[string]string{"state": state, "message_id": message})), key)
		if message == "" {
			message = "unknown-" + shortDeliveryID(run.ID, index) + "-" + contentHash([]byte(revision))[:8]
		}
		recordErr := s.tasks.RecordChannelDelivery(persist, run.ID, message, confirmed, state, result.Maintenance)
		done()
		if sendErr == nil && state == "sent" {
			s.mu.Lock()
			for _, button := range b {
				item := s.callbacks[button.Token]
				item.Message = message
				s.callbacks[button.Token] = item
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
	err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM messages WHERE conversation_id=? AND id<=? AND json_extract(body_json,'$.Channel.purpose') IN ('request','command') ORDER BY id DESC LIMIT 1`, run.ConversationID, origin.HistoryThrough).Scan(&raw)
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
