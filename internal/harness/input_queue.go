package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrInputTargetEnded = errors.New("harness: steering target has ended; input retained, no new task started")
var ErrInputPending = errors.New("harness: user steering must be adopted before dispatch")
var ErrQueuePaused = errors.New("harness: input queue is paused; inspect and resume explicitly")
var ErrInputNotPending = errors.New("harness: input has already been sent or cancelled; inspect its saved state")

type InputCommand struct {
	Action      string   `json:"action"`
	RunID       string   `json:"run_id,omitempty"`
	RequestKey  string   `json:"request_key"`
	Prompt      string   `json:"prompt"`
	Attachments []string `json:"attachments,omitempty"`
}

// Only sequence, disposition and authorization bindings are stored in meta.
// User content remains in messages; there are no placeholder Runs or jobs.
type PendingInput struct {
	AdoptedAt      string               `json:"adopted_at,omitempty"`
	ProfileHash    string               `json:"profile_hash"`
	Key            string               `json:"key"`
	Action         string               `json:"action"`
	Status         string               `json:"status"`
	MessageID      int64                `json:"message_id"`
	ConversationID string               `json:"conversation_id"`
	TargetRun      string               `json:"target_run,omitempty"`
	RunID          string               `json:"run_id,omitempty"`
	Principal      string               `json:"principal"`
	CreatedAt      string               `json:"created_at"`
	Prompt         string               `json:"prompt,omitempty"`
	Attachments    []AttachmentRef      `json:"attachments,omitempty"`
	Binding        ModelBinding         `json:"binding"`
	ConfigHash     string               `json:"config_hash"`
	Permission     PermissionMode       `json:"permission"`
	Channel        *ChannelRunOrigin    `json:"channel,omitempty"`
	Person         string               `json:"person,omitempty"`
	Remote         *PublicEntry         `json:"remote,omitempty"`
	Capabilities   *CapabilitySelection `json:"capabilities,omitempty"`
}

type InputState struct {
	Paused  bool           `json:"paused"`
	Entries []PendingInput `json:"entries"`
}

func inputPrefix(chat string) string   { return "input/" + chat + "/" }
func inputKey(chat, key string) string { return inputPrefix(chat) + contentHash([]byte(key)) }
func saveInput(ctx context.Context, tx *sql.Tx, input PendingInput) error {
	input.Prompt = ""
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, inputKey(input.ConversationID, input.Key), string(mustJSON(input)))
	return err
}
func loadInput(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, chat, key string) (PendingInput, error) {
	var input PendingInput
	var raw string
	err := reader.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, inputKey(chat, key)).Scan(&raw)
	if err == nil && json.Unmarshal([]byte(raw), &input) != nil {
		err = ErrStoreFormat
	}
	return input, err
}

func (tasks *TaskService) AcceptInput(ctx context.Context, chat string, command InputCommand) (PendingInput, error) {
	return tasks.acceptInput(ctx, chat, command, RunOptions{})
}

func (tasks *TaskService) acceptInput(ctx context.Context, chat string, command InputCommand, options RunOptions) (PendingInput, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || (command.Action != "steer" && command.Action != "queue") || command.RequestKey == "" || len(command.RequestKey) > 128 || strings.TrimSpace(command.Prompt) == "" || len(command.Prompt) > tasks.Config().MaxContextBytes {
		return PendingInput{}, ErrRequestKey
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return PendingInput{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return PendingInput{}, ErrConversationSpace
	}
	first := strings.SplitN(strings.Fields(command.Prompt)[0], "@", 2)[0]
	switch first {
	case "/stop", "/continue", "/compact", "/context", "/approve", "/reject":
		return PendingInput{}, errors.New("harness: use the Owner control command directly; control commands are not queued model input")
	}
	core := tasks.core.runCopy()
	core.Config = tasks.Config()
	if err := core.resolvePermission(ctx, chat, "", false); err != nil {
		return PendingInput{}, err
	}
	if options.channel != nil {
		if err := core.resolveRequestPermission(ctx, chat, options.personID, options.channel.Source); err != nil {
			return PendingInput{}, err
		}
		if err := core.bindChannel(ctx, chat, options.channel); err != nil {
			return PendingInput{}, err
		}
	}
	principal := InteractivePrincipal(ctx, "local")
	if options.personID != "" {
		principal = "channel:" + options.personID
	}
	id, err := tasks.SelectedModel(ctx, chat)
	if err != nil {
		return PendingInput{}, err
	}
	if id == "auto" && command.Action == "queue" {
		return PendingInput{}, errors.New("harness: choose an explicit model before queueing input so its binding can be retained")
	}
	if id == "auto" {
		id = core.Config.DefaultModel
	}
	binding, err := core.newBinding(core.Config, id)
	if err != nil {
		return PendingInput{}, err
	}
	refs, err := tasks.resolveAttachments(ctx, chat, command.Attachments)
	if err != nil {
		return PendingInput{}, err
	}
	caps, err := tasks.SelectedCapabilities(ctx, chat)
	if err != nil {
		return PendingInput{}, err
	}
	safety := string(mustJSON(core.Config))
	input := PendingInput{Key: command.RequestKey, Action: command.Action, Status: "pending", ConversationID: chat, TargetRun: command.RunID, Principal: principal, CreatedAt: nowText(), Binding: binding, ConfigHash: contentHash([]byte(safety)), Permission: core.permission.Mode, Attachments: refs, Channel: options.channel, Person: options.personID, Capabilities: &caps}
	if remote, ok := PublicEntryFrom(ctx); ok {
		input.Remote = &remote
	}
	profile, err := buildExecutionProfile(core.Config, *core.permission)
	if err != nil {
		return input, err
	}
	input.ProfileHash = contentHash(mustJSON(profile))
	store := core.Store
	store.inputMu.Lock()
	defer store.inputMu.Unlock()
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer tx.Rollback()
	if old, e := loadInput(ctx, tx, chat, input.Key); e == nil {
		var body string
		if e = tx.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, old.MessageID, chat).Scan(&body); e != nil {
			return old, e
		}
		var message ModelMessage
		if json.Unmarshal([]byte(body), &message) != nil {
			return old, ErrStoreFormat
		}
		if old.Principal != principal || old.Action != command.Action || old.TargetRun != command.RunID || message.Content != command.Prompt || string(mustJSON(old.Attachments)) != string(mustJSON(refs)) {
			return old, ErrRequestKey
		}
		old.Prompt = message.Content
		return old, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return input, e
	}
	if command.Action == "steer" {
		run, e := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1 AND conversation_id=$2`, command.RunID, chat))
		if e != nil {
			return input, e
		}
		if options.personID != "" && (run.Permission == nil || run.Permission.PersonID != options.personID || channelOriginFromJSON(run.ConfigJSON) == nil || channelOriginFromJSON(run.ConfigJSON).ChannelTarget != options.channel.ChannelTarget) {
			return input, ErrPermissionDenied
		}
		if input.Remote != nil {
			var original PublicEntry
			var originalRaw string
			e := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "run/remote/"+run.ID).Scan(&originalRaw)
			if e != nil || json.Unmarshal([]byte(originalRaw), &original) != nil || original.Principal != input.Remote.Principal {
				return input, ErrPermissionDenied
			}
		}
		if run.Status != "running" && run.Status != "waiting_confirmation" && run.Status != "unknown" && run.Status != "stopped" && run.Status != "limited" {
			input.Status = "target_ended"
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE $1 AND (document #>> '{status}')='pending'`, inputPrefix(chat)+"%").Scan(&count); err != nil {
		return input, err
	}
	if count >= 20 {
		return input, errors.New("harness: at most 20 pending inputs per conversation; input was not accepted")
	}
	message := ModelMessage{Role: "user", Content: command.Prompt, Attachments: refs, Deferred: true, Public: options.channel != nil}
	if options.channel != nil {
		input.MessageID = options.channel.InputMessageID
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2 AND run_id IS NULL`, input.MessageID, chat).Scan(&raw); err != nil {
			return input, err
		}
		if json.Unmarshal([]byte(raw), &message) != nil {
			return input, ErrStoreFormat
		}
		if message.Content != command.Prompt {
			words := strings.Fields(message.Content)
			if message.Channel == nil || message.Channel.Purpose != "command" || len(words) < 2 || (strings.SplitN(words[0], "@", 2)[0] != "/queue" && strings.SplitN(words[0], "@", 2)[0] != "/steer") || strings.TrimSpace(strings.TrimPrefix(message.Content, words[0])) != command.Prompt {
				return input, ErrRequestKey
			}
			message.Content = command.Prompt
			source := *message.Channel
			source.Purpose = "request"
			message.Channel = &source
		}
		message.Deferred = true
		message.Attachments = refs
		if _, err := tx.ExecContext(ctx, `UPDATE fa.messages SET body_json=$1 WHERE id=$2 AND conversation_id=$3`, string(mustJSON(message)), input.MessageID, chat); err != nil {
			return input, err
		}
	} else {
		err = tx.QueryRowContext(ctx, `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2) RETURNING id`, chat, string(mustJSON(message))).Scan(&input.MessageID)
		if err != nil {
			return input, err
		}

	}
	// Retain accepted input attachments using their existing committed binding.
	if err := bindAttachments(ctx, tx, chat, "input:"+input.Key, refs); err != nil {
		return input, err
	}
	if err := saveInput(ctx, tx, input); err != nil {
		return input, err
	}
	if err := tx.Commit(); err != nil {
		return input, err
	}
	input.Prompt = command.Prompt
	tasks.inputEvent(input)
	return input, nil
}

func (tasks *TaskService) inputEvent(input PendingInput) {
	tasks.mu.Lock()
	emit := tasks.emit
	tasks.mu.Unlock()
	if emit != nil {
		emit(Event{Kind: EventInputChanged, ConversationID: input.ConversationID, RunID: input.TargetRun, Message: input.Status})
	}
}

func (tasks *TaskService) Inputs(ctx context.Context, chat string) (InputState, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return InputState{}, errors.New("harness: task service and context are required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return InputState{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return InputState{}, ErrConversationSpace
	}
	state := InputState{Entries: []PendingInput{}}
	var paused string
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "input-paused/"+chat).Scan(&paused)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	state.Paused = paused == "true"
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE $1 ORDER BY ((document #>> '{status}')='pending') DESC,CAST((document #>> '{message_id}') AS INTEGER) DESC LIMIT 40`, inputPrefix(chat)+"%")
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var raw string
		var i PendingInput
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return state, err
		}
		if json.Unmarshal([]byte(raw), &i) != nil {
			rows.Close()
			return state, ErrStoreFormat
		}
		state.Entries = append(state.Entries, i)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return state, err
	}
	for n := range state.Entries {
		i := &state.Entries[n]
		var raw string
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, i.MessageID, chat).Scan(&raw); err != nil {
			return state, err
		}
		var m ModelMessage
		if json.Unmarshal([]byte(raw), &m) != nil {
			return state, ErrStoreFormat
		}
		i.Prompt = m.Content
		// Client state contains no server config, model address or authority token.
		i.Binding = ModelBinding{ID: i.Binding.ID}
		i.ConfigHash = ""
		i.Remote = nil
		i.Channel = nil
	}
	return state, nil
}

func (tasks *TaskService) CancelInput(ctx context.Context, chat, key string) (PendingInput, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return PendingInput{}, errors.New("harness: task service and context are required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return PendingInput{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return PendingInput{}, ErrConversationSpace
	}
	store := tasks.core.Store
	store.inputMu.Lock()
	defer store.inputMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return PendingInput{}, err
	}
	defer tx.Rollback()
	i, err := loadInput(ctx, tx, chat, key)
	if err != nil {
		return i, err
	}
	if i.Principal != InteractivePrincipal(ctx, "local") && InteractivePrincipal(ctx, "local") != "local" {
		return i, ErrPermissionDenied
	}
	if i.Status == "pending" {
		i.Status = "cancelled"
		if err := saveInput(ctx, tx, i); err != nil {
			return i, err
		}
	}
	return i, tx.Commit()
}

// SteerInput moves one accepted queue entry into the specified current Run.
// Its message, request key and authorization remain the same. Consumption,
// cancellation and this operation compete in the same transaction boundary.
func (tasks *TaskService) SteerInput(ctx context.Context, chat, key, runID string) (PendingInput, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || chat == "" || key == "" || runID == "" {
		return PendingInput{}, ErrRequestKey
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return PendingInput{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return PendingInput{}, ErrConversationSpace
	}
	store := tasks.core.Store
	store.inputMu.Lock()
	defer store.inputMu.Unlock()
	// Validate the saved authority before the write transaction, since these
	// checks read the Store through its normal connection.
	input, err := loadInput(ctx, store.db, chat, key)
	if err != nil {
		return input, err
	}
	principal := InteractivePrincipal(ctx, "local")
	if input.Principal != principal && principal != "local" {
		return PendingInput{}, ErrPermissionDenied
	}
	if _, err := tasks.validateQueuedInput(ctx, chat, input); err != nil {
		return input, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer tx.Rollback()
	input, err = loadInput(ctx, tx, chat, key)
	if err != nil {
		return input, err
	}
	if input.Action == "steer" && input.TargetRun == runID {
		return input, nil // A lost response never turns this back into a new input.
	}
	if input.Action != "queue" || input.Status != "pending" {
		return input, ErrInputNotPending
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1 AND conversation_id=$2`, runID, chat))
	if err != nil {
		return input, err
	}
	if run.Status != "running" {
		return input, ErrInputTargetEnded
	}
	if input.Person != "" && (run.Permission == nil || run.Permission.PersonID != input.Person || input.Channel == nil || channelOriginFromJSON(run.ConfigJSON) == nil || channelOriginFromJSON(run.ConfigJSON).ChannelTarget != input.Channel.ChannelTarget) {
		return PendingInput{}, ErrPermissionDenied
	}
	if input.Remote != nil {
		var raw string
		var original PublicEntry
		if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "run/remote/"+run.ID).Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &original) != nil || original.Principal != input.Remote.Principal {
			return PendingInput{}, ErrPermissionDenied
		}
	}
	input.Action, input.TargetRun = "steer", run.ID
	if err := saveInput(ctx, tx, input); err != nil {
		return input, err
	}
	if err := tx.Commit(); err != nil {
		return input, err
	}
	tasks.inputEvent(input)
	return input, nil
}

func (tasks *TaskService) PauseQueue(ctx context.Context, chat string, paused bool) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return ErrConversationSpace
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "input-paused/"+chat, fmt.Sprint(paused))
	return err
}

func (tasks *TaskService) QueuedConversations(ctx context.Context) ([]string, error) {
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT (m.document #>> '{conversation_id}') AS chat FROM fa.meta m JOIN fa.conversations c ON c.id=(m.document #>> '{conversation_id}') WHERE m.key LIKE 'input/%' AND c.workspace=$1 AND (m.document #>> '{action}')='queue' AND (m.document #>> '{status}')='pending' GROUP BY chat ORDER BY min(CAST((m.document #>> '{message_id}') AS INTEGER)) LIMIT 1000`, tasks.Config().Workspace)
	if err != nil {
		return nil, err
	}
	var chats []string
	for rows.Next() {
		var chat string
		if err := rows.Scan(&chat); err != nil {
			rows.Close()
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, errors.Join(rows.Err(), rows.Close())
}

// RunQueued is driven by the existing Owner dispatcher, so foreground quotas,
// cancellation, delivery and shutdown use the same path as ordinary input.
func (tasks *TaskService) RunQueued(ctx context.Context, chat string) (RunResult, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return RunResult{}, errors.New("harness: task service and context are required")
	}
	var paused string
	_ = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "input-paused/"+chat).Scan(&paused)
	if paused == "true" {
		return RunResult{}, ErrQueuePaused
	}
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE $1 AND (document #>> '{status}')='pending' AND (document #>> '{action}')='queue' ORDER BY CAST((document #>> '{message_id}') AS INTEGER) LIMIT 1`, inputPrefix(chat)+"%").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return RunResult{}, nil
	}
	if err != nil {
		return RunResult{}, err
	}
	var input PendingInput
	if json.Unmarshal([]byte(raw), &input) != nil {
		return RunResult{}, ErrStoreFormat
	}
	if input.Remote != nil {
		ctx = WithPublicEntry(ctx, input.Remote.Principal, input.Remote.IP)
	}
	ctx = WithAttachmentIdentity(ctx, input.Principal)
	core, err := tasks.validateQueuedInput(ctx, chat, input)
	if err != nil {
		_ = tasks.PauseQueue(ctx, chat, true)
		return RunResult{}, err
	}
	var body string
	if err := core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, input.MessageID, input.ConversationID).Scan(&body); err != nil {
		return RunResult{}, err
	}
	var message ModelMessage
	if json.Unmarshal([]byte(body), &message) != nil {
		return RunResult{}, ErrStoreFormat
	}
	ids := make([]string, 0, len(input.Attachments))
	for _, r := range input.Attachments {
		ids = append(ids, r.ID)
	}
	off := DefaultCollaborationSettings()
	off.Mode = "off"
	return tasks.submitWithOptions(ctx, chat, input.Key, message.Content, RunOptions{inputKey: input.Key, ModelID: input.Binding.ID, Permission: input.Permission, ConfirmFullAccess: true, Attachments: ids, channel: input.Channel, personID: input.Person, Capabilities: input.Capabilities, Collaboration: &off})
}

func (tasks *TaskService) validateQueuedInput(ctx context.Context, chat string, input PendingInput) (*Service, error) {
	config := tasks.Config()
	core := tasks.core.runCopy()
	core.Config = config
	binding, err := core.newBinding(config, input.Binding.ID)
	if err != nil || contentHash(mustJSON(config)) != input.ConfigHash || string(mustJSON(binding)) != string(mustJSON(input.Binding)) {
		return nil, errors.Join(ErrConfigChanged, err)
	}
	if err := core.resolvePermission(ctx, chat, "", false); err != nil {
		return nil, err
	}
	if input.Channel != nil {
		if err := core.resolveRequestPermission(ctx, chat, input.Person, input.Channel.Source); err != nil {
			return nil, err
		}
	}
	if input.Remote != nil {
		core.permission.Mode = lowerPermission(core.permission.Mode, config.PublicWeb.Effective().MaxPermission)
	}
	profile, err := buildExecutionProfile(config, *core.permission)
	if err != nil || contentHash(mustJSON(profile)) != input.ProfileHash {
		return nil, errors.Join(ErrPermissionRevoked, err)
	}
	return &core, nil
}

func pendingSteer(ctx context.Context, tx *sql.Tx, run string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key LIKE 'input/%' AND (document #>> '{target_run}')=$1 AND (document #>> '{status}')='pending' AND (document #>> '{action}')='steer')`, run).Scan(&exists)
	return exists, err
}

func moveInputMessage(ctx context.Context, tx *sql.Tx, input *PendingInput, run string) error {
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT last_message_seq+1 FROM fa.conversations WHERE id=$1 FOR UPDATE`, input.ConversationID).Scan(&next); err != nil {
		return err
	}
	// Move the existing received record to its adoption boundary. Pending input
	// never becomes part of an earlier summary's covered prefix.
	err := tx.QueryRowContext(ctx, `UPDATE fa.messages SET id=$1,run_id=$2,body_json=jsonb_set(body_json,'{Deferred}','false'::jsonb,true) WHERE id=$3 AND run_id IS NULL AND conversation_id=$4 RETURNING id`, next, run, input.MessageID, input.ConversationID).Scan(&next)
	if err != nil {
		return err
	}
	input.MessageID = next
	input.Status = "adopted"
	input.AdoptedAt = nowText()
	input.RunID = run
	return saveInput(ctx, tx, *input)
}

func (service *Service) adoptSteering(ctx context.Context, run Run) (bool, error) {
	return service.adoptSteeringForRetry(ctx, run, "")
}

func (service *Service) adoptSteeringForRetry(ctx context.Context, run Run, retryModel string) (bool, error) {
	store := service.Store
	store.inputMu.Lock()
	defer store.inputMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var unresolved bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=$1 AND a.status IN ('unknown','dispatched') AND NOT ((a.kind='model' AND a.id=$2) OR `+resolvedAttemptSQL+`))`, run.ID, retryModel).Scan(&unresolved); err != nil {
		return false, err
	}
	if unresolved {
		return false, ErrUnknownResult
	}
	rows, err := tx.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE $1 AND (document #>> '{target_run}')=$2 AND (document #>> '{status}')='pending' AND (document #>> '{action}')='steer' ORDER BY CAST((document #>> '{message_id}') AS INTEGER)`, inputPrefix(run.ConversationID)+"%", run.ID)
	if err != nil {
		return false, err
	}
	var inputs []PendingInput
	for rows.Next() {
		var raw string
		var i PendingInput
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return false, err
		}
		if json.Unmarshal([]byte(raw), &i) != nil {
			rows.Close()
			return false, ErrStoreFormat
		}
		inputs = append(inputs, i)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	if len(inputs) == 0 {
		return false, nil
	}
	// checkCurrentPermission performs Store reads; the caller validates authority
	// before this transaction (see process). No tool is dispatched by this method.
	attempts, err := listAttempts(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	for _, a := range attempts {
		if a.Kind != "tool" || a.Status != "pending" {
			continue
		}
		var call ToolCall
		if json.Unmarshal([]byte(a.RequestJSON), &call) != nil {
			return false, ErrStoreFormat
		}
		result := ToolResult{Content: "NOT_RUN: superseded by verified user steering before dispatch; no approval was reused", IsError: true, Outcome: &ToolOutcome{Status: "not_run", CompletionKnown: true}}
		if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished',response_json=$1 WHERE id=$2 AND status='pending'`, string(mustJSON(result)), a.ID); err != nil {
			return false, err
		}
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: result.Content}); err != nil {
			return false, err
		}
	}
	for n := range inputs {
		if err := moveInputMessage(ctx, tx, &inputs[n], run.ID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='running',stop_reason='',updated_at=$1 WHERE id=$2`, nowText(), run.ID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	for _, a := range attempts {
		if a.Kind == "tool" && a.Status == "pending" && service.control != nil {
			if op, ok := service.control.operationForAttempt(a.ID); ok {
				_ = service.control.withdrawProposal(op.Operation.ID, "superseded_by_user")
			}
		}
	}
	service.event(Event{Kind: EventInputAdopted, ConversationID: run.ConversationID, RunID: run.ID, Message: fmt.Sprintf("%d user steering inputs adopted; undispatched plans marked NOT_RUN", len(inputs))})
	return true, nil
}
