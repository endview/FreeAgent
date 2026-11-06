package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ChatView is a read-only projection, not another persisted execution model.
type ChatView struct {
	Branch       *BranchOrigin       `json:"branch,omitempty"`
	Conversation Conversation        `json:"conversation"`
	ModelID      string              `json:"model_id"`
	Permission   PermissionSelection `json:"permission"`
	Capabilities CapabilitySelection `json:"capabilities"`
	Background   []ChatMessage       `json:"background,omitempty"`
	Turns        []ChatTurn          `json:"turns"`
	HasEarlier   bool                `json:"has_earlier"`
	Before       string              `json:"before,omitempty"`
}

type ChatTurn struct {
	MessagesEarlier bool                 `json:"messages_has_earlier,omitempty"`
	Attachments     []AttachmentRef      `json:"attachments,omitempty"`
	Key             string               `json:"key"`
	State           string               `json:"state"`
	Reason          string               `json:"reason,omitempty"`
	ModelID         string               `json:"model_id,omitempty"`
	Permission      *PermissionSelection `json:"permission,omitempty"`
	RuntimeRevision string               `json:"runtime_revision,omitempty"`
	EnabledTools    []string             `json:"enabled_tools,omitempty"`
	RequestKey      string               `json:"request_key"`
	Steps           int                  `json:"steps"`
	ElapsedMS       int64                `json:"elapsed_ms"`
	Messages        []ChatMessage        `json:"messages"`
	ActivityEarlier bool                 `json:"activity_has_earlier,omitempty"`
	Activity        []ChatActivity       `json:"activity"`
	Evidence        Evidence             `json:"evidence"`
	Sources         []MemoryUse          `json:"sources"`
	CanContinue     bool                 `json:"can_continue"`
	CanEnd          bool                 `json:"can_end"`
	Ended           bool                 `json:"ended"`
	Confirmation    *Confirmation        `json:"confirmation,omitempty"`
	Maintenance     bool                 `json:"maintenance,omitempty"`
	LimitSettled    bool                 `json:"limit_settled,omitempty"`
}

type ChatMessage struct {
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	Key         string          `json:"key"`
	Role        string          `json:"role"`
	Raw         string          `json:"raw"`
	Truncated   bool            `json:"truncated,omitempty"`
}

type ChatActivity struct {
	Outcome    *ToolOutcome  `json:"outcome,omitempty"`
	DurationMS *int64        `json:"duration_ms,omitempty"`
	StartedAt  string        `json:"started_at,omitempty"`
	Usage      *ModelUsage   `json:"usage,omitempty"`
	Key        string        `json:"key"`
	Kind       string        `json:"kind"`
	Name       string        `json:"name,omitempty"`
	Target     string        `json:"target,omitempty"`
	Status     string        `json:"status"`
	IsError    bool          `json:"is_error,omitempty"`
	Text       string        `json:"text,omitempty"`
	Truncated  bool          `json:"truncated,omitempty"`
	Receipts   []ToolReceipt `json:"receipts,omitempty"`
	Failure    *CallError    `json:"failure,omitempty"`
}

const chatTextBytes = 64 << 10

// Presentation is bounded; the persisted text and explicit raw reads are exact.
func chatText(raw string) (string, bool) {
	if len(raw) <= chatTextBytes {
		return raw, false
	}
	end := chatTextBytes
	for end > 0 && raw[end]&0xc0 == 0x80 {
		end--
	}
	return raw[:end], true
}

func (tasks *TaskService) EnvironmentID() string {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return ""
	}
	return contentHash([]byte(tasks.core.Store.DataDir() + "\x00" + tasks.Config().Workspace))
}

func chatState(run Run, attempts []Attempt) string {
	for _, attempt := range attempts {
		if attempt.Status == "unknown" && attempt.Replacement == "" && attempt.Resolution == "" {
			return "unknown"
		}
	}
	switch run.Status {
	case "running":
		return "generating"
	case "waiting_confirmation":
		return "awaiting_approval"
	case "completed", "failed", "unknown":
		return run.Status
	case "stopped", "abandoned":
		return "stopped"
	default:
		return "needs_attention"
	}
}

func activityTarget(call ToolCall) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &fields); err != nil {
		return ""
	}
	for _, key := range []string{"path", "command", "url", "query"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			continue
		}
		runes := []rune(value)
		if len(runes) > 160 {
			value = string(runes[:160])
		}
		return value
	}
	return ""
}

// Chat reads a consistent page of complete turns. before is an opaque run key.
func (tasks *TaskService) Chat(ctx context.Context, conversationID, before string) (ChatView, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return ChatView{}, errors.New("harness: task service and context are required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return ChatView{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return ChatView{}, errors.New("harness: conversation workspace does not match")
	}
	model, err := tasks.SelectedModel(ctx, conversationID)
	if err != nil {
		return ChatView{}, err
	}
	permission, err := tasks.SelectedPermission(ctx, conversationID)
	if err != nil {
		return ChatView{}, err
	}
	capabilities, err := tasks.SelectedCapabilities(ctx, conversationID)
	if err != nil {
		return ChatView{}, err
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ChatView{}, err
	}
	defer tx.Rollback()
	condition := ""
	args := []any{conversationID}
	if before != "" {
		var rowID int64
		if err := tx.QueryRowContext(ctx, `SELECT seq FROM fa.runs WHERE id=$1 AND conversation_id=$2`, before, conversationID).Scan(&rowID); err != nil {
			return ChatView{}, err
		}
		condition = " AND seq<$2"
		args = append(args, rowID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1
		AND NOT(status='completed' AND EXISTS(SELECT 1 FROM fa.meta WHERE key='`+maintenanceMetaPrefix+`'||runs.id))`+condition+` ORDER BY seq DESC LIMIT 33`, args...)
	if err != nil {
		return ChatView{}, err
	}
	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			_ = rows.Close()
			return ChatView{}, err
		}
		runs = append(runs, run)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ChatView{}, err
	}
	view := ChatView{Conversation: conversation, ModelID: model, Permission: permission,
		Capabilities: capabilities, Turns: make([]ChatTurn, 0), HasEarlier: len(runs) > 32}
	if len(runs) > 32 {
		runs = runs[:32]
	}
	slices.Reverse(runs)
	for _, run := range runs {
		attempts, earlier, err := listChatAttempts(ctx, tx, run.ID)
		if err != nil {
			return ChatView{}, err
		}
		turn := ChatTurn{ActivityEarlier: earlier, Key: run.ID, RequestKey: run.RequestKey, State: chatState(run, attempts), Reason: run.StopReason,
			Steps: run.Steps, ElapsedMS: activeElapsed(run),
			ModelID: run.ModelID, Permission: run.Permission, Messages: make([]ChatMessage, 0), Activity: make([]ChatActivity, 0),
			Sources: make([]MemoryUse, 0), Evidence: attemptEvidence(attempts), Ended: run.Status == "abandoned"}
		if strings.HasPrefix(run.RequestKey, "compact-") {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, maintenanceMetaPrefix+run.ID).Scan(&turn.Maintenance); err != nil {
				return ChatView{}, err
			}
		}
		if snapshot, snapshotErr := decodeRunSnapshot(run.ConfigJSON); snapshotErr == nil {
			turn.RuntimeRevision = snapshot.ToolGeneration
			if snapshot.EnabledTools.File {
				turn.EnabledTools = append(turn.EnabledTools, "file")
			}
			if snapshot.EnabledTools.CommandRun && snapshot.CommandPolicy.Enabled {
				turn.EnabledTools = append(turn.EnabledTools, "command_run")
			}
			if snapshot.EnabledTools.Network {
				turn.EnabledTools = append(turn.EnabledTools, "network")
			}
			if snapshot.EnabledTools.Memory {
				turn.EnabledTools = append(turn.EnabledTools, "memory")
			}
		}
		turn.CanContinue = (run.Status == "stopped" || run.Status == "limited" || run.Status == "waiting_confirmation") && turn.State != "unknown"
		if run.LimitSettled && turn.State != "unknown" {
			turn.State = "limited"
			turn.LimitSettled = true
		}
		deliveryNotes, err := channelDeliveryNotes(ctx, tx, run.ID)
		if err != nil {
			return ChatView{}, err
		}
		turn.Evidence.Uncertainty = append(turn.Evidence.Uncertainty, deliveryNotes...)
		turn.CanEnd = run.Status != "running" && run.Status != "completed" && run.Status != "failed" && !turn.Ended
		if run.LimitSettled && turn.State != "unknown" {
			turn.CanEnd = false
		}
		messages, err := tx.QueryContext(ctx, `SELECT id,body_json FROM fa.messages WHERE conversation_id=$1 AND run_id=$2 AND ((body_json #>> '{Role}')='user' OR (body_json #>> '{Role}')='assistant' AND length((body_json #>> '{Content}'))>0 OR jsonb_array_length((body_json #> '{Attachments}'))>0) ORDER BY id DESC LIMIT 129`, conversationID, run.ID)
		if err != nil {
			return ChatView{}, err
		}
		for messages.Next() {
			var id int64
			var raw string
			if err := messages.Scan(&id, &raw); err != nil {
				_ = messages.Close()
				return ChatView{}, err
			}
			var message ModelMessage
			if json.Unmarshal([]byte(raw), &message) != nil {
				_ = messages.Close()
				return ChatView{}, ErrStoreFormat
			}
			if message.Role == "user" || message.Role == "assistant" && message.Content != "" {
				text, truncated := chatText(message.Content)
				turn.Messages = append(turn.Messages, ChatMessage{Key: strconv.FormatInt(id, 10), Role: message.Role, Raw: text, Truncated: truncated, Attachments: message.Attachments})
			}
			if message.Role == "tool" {
				turn.Attachments = append(turn.Attachments, message.Attachments...)
			}
		}
		if err := errors.Join(messages.Err(), messages.Close()); err != nil {
			return ChatView{}, err
		}
		if len(turn.Messages) > 128 {
			turn.Messages = turn.Messages[:128]
			turn.MessagesEarlier = true
		}
		slices.Reverse(turn.Messages)
		slices.Reverse(turn.Attachments)
		projectRefs := func(refs []AttachmentRef) {
			for i := range refs {
				record, err := loadAttachment(ctx, tx, refs[i].ID)
				refs[i].Unavailable = err != nil || record.Deleted || record.Space != conversation.SpaceID
			}
		}
		for i := range turn.Messages {
			projectRefs(turn.Messages[i].Attachments)
		}
		projectRefs(turn.Attachments)
		seen := map[string]bool{}
		addSources := func(sources []MemoryUse) {
			for _, source := range sources {
				raw, _ := json.Marshal(source)
				if !seen[string(raw)] {
					turn.Sources = append(turn.Sources, source)
					seen[string(raw)] = true
				}
			}
		}
		for _, attempt := range attempts {
			activity := ChatActivity{Key: attempt.ID, Kind: attempt.Kind, Status: attempt.Status, Name: attempt.ModelID}
			var start string
			if e := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "attempt/time/"+attempt.ID).Scan(&start); e == nil {
				if _, e := time.Parse(time.RFC3339Nano, start); e == nil {
					activity.StartedAt = start
				}
			} else if !errors.Is(e, sql.ErrNoRows) {
				return ChatView{}, e
			}
			if attempt.Kind == "model" {
				var request ModelRequest
				if json.Unmarshal([]byte(attempt.RequestJSON), &request) != nil {
					return ChatView{}, ErrStoreFormat
				}
				if request.Purpose != "" {
					activity.Kind = request.Purpose
				}
				addSources(request.Memory)
				if attempt.ResponseJSON != "" {
					var response ModelResponse
					if json.Unmarshal([]byte(attempt.ResponseJSON), &response) != nil {
						// Lost calls can retain a runtime diagnostic instead of a response.
						if attempt.Status != "unknown" || json.Valid([]byte(attempt.ResponseJSON)) {
							return ChatView{}, ErrStoreFormat
						}
						activity.Text, activity.Truncated = chatText(attempt.ResponseJSON)
					} else {
						if activity.StartedAt != "" || response.LatencyMS > 0 {
							duration := response.LatencyMS
							activity.DurationMS = &duration
						}
						activity.Usage = response.Usage
						if response.Failure != nil {
							activity.Failure, activity.IsError = response.Failure, true
							activity.Text, activity.Truncated = chatText(response.Content)
						}
					}
				}
			} else if attempt.Kind == "workflow" {
				var request workflowAttemptRequest
				var result ToolResult
				if json.Unmarshal([]byte(attempt.RequestJSON), &request) != nil {
					return ChatView{}, ErrStoreFormat
				}
				if json.Unmarshal([]byte(attempt.ResponseJSON), &result) != nil && attempt.ResponseJSON != "" {
					return ChatView{}, ErrStoreFormat
				}
				activity.Kind = "workflow"
				activity.Name = request.ExtensionID + "/" + request.Workflow
				activity.Text, activity.IsError = result.Content, result.IsError
				activity.Text, activity.Truncated = chatText(activity.Text)
			} else {
				var call ToolCall
				var result ToolResult
				if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
					return ChatView{}, ErrStoreFormat
				}
				_ = json.Unmarshal([]byte(attempt.ResponseJSON), &result)
				activity.Name, activity.Target, activity.Text, activity.IsError, activity.Receipts =
					call.Name, activityTarget(call), result.Content, result.IsError, result.Receipts
				activity.Text, activity.Truncated = chatText(activity.Text)
				if result.Outcome != nil {
					activity.Outcome = result.Outcome
					activity.Status = result.Outcome.Status
					if result.Outcome.Dispatched && (result.Outcome.DurationKnown || result.Outcome.DurationMS > 0) {
						duration := result.Outcome.DurationMS
						activity.DurationMS = &duration
					}
				}
				addSources(result.Memory)
			}
			turn.Activity = append(turn.Activity, activity)
		}
		view.Turns = append(view.Turns, turn)
	}
	if len(runs) != 0 {
		view.Before = runs[0].ID
	}
	var branchRaw string
	if e := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/branch/"+conversationID).Scan(&branchRaw); e == nil {
		var origin BranchOrigin
		if json.Unmarshal([]byte(branchRaw), &origin) != nil {
			return ChatView{}, ErrStoreFormat
		}
		view.Branch = &origin
	} else if !errors.Is(e, sql.ErrNoRows) {
		return ChatView{}, e
	}
	if before == "" {
		rows, err := tx.QueryContext(ctx, `SELECT id,body_json FROM fa.messages WHERE conversation_id=$1 AND run_id IS NULL AND coalesce(((body_json #>> '{Deferred}')::boolean),false)=false AND ((body_json #>> '{Channel,purpose}') IN ('background','command','request') OR EXISTS(SELECT 1 FROM fa.meta WHERE key='conversation/branch/'||messages.conversation_id)) ORDER BY id DESC LIMIT 100`, conversationID)
		if err != nil {
			return ChatView{}, err
		}
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return ChatView{}, err
			}
			var message ModelMessage
			if json.Unmarshal([]byte(raw), &message) != nil {
				rows.Close()
				return ChatView{}, ErrStoreFormat
			}
			text := message.Content
			if message.Channel != nil {
				text = message.Channel.DisplayName + " [" + message.Channel.PersonID + "] " + text
			}
			text, truncated := chatText(text)
			view.Background = append(view.Background, ChatMessage{Key: strconv.FormatInt(id, 10), Role: message.Role, Raw: text, Truncated: truncated, Attachments: message.Attachments})
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return ChatView{}, err
		}
		slices.Reverse(view.Background)
	}
	return view, tx.Commit()
}

// ChatRaw reads one explicitly selected body without exposing wire requests.
func (tasks *TaskService) ChatRaw(ctx context.Context, conversationID, kind, key string) (string, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return "", errors.New("harness: task service and context are required")
	}
	chat, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return "", err
	}
	if chat.Workspace != tasks.Config().Workspace {
		return "", errors.New("harness: conversation workspace does not match")
	}
	var raw string
	switch kind {
	case "message":
		err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT m.body_json FROM fa.messages m WHERE m.conversation_id=$1 AND m.id=$2`, conversationID, key).Scan(&raw)
		if err != nil {
			return "", err
		}
		var message ModelMessage
		if json.Unmarshal([]byte(raw), &message) != nil {
			return "", ErrStoreFormat
		}
		if message.Role != "user" && message.Role != "assistant" {
			return "", errors.New("harness: only Chat message bodies can be read")
		}
		return message.Content, nil
	case "activity":
		var activityKind, activityStatus string
		err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT a.kind,a.status,a.response_json FROM fa.attempts a
			JOIN fa.runs r ON r.id=a.run_id WHERE r.conversation_id=$1 AND a.id=$2`, conversationID, key).Scan(&activityKind, &activityStatus, &raw)
		if err != nil {
			return "", err
		}
		if activityKind == "model" {
			var response ModelResponse
			if json.Unmarshal([]byte(raw), &response) != nil {
				if activityStatus == "unknown" && !json.Valid([]byte(raw)) {
					return raw, nil
				}
				return "", ErrStoreFormat
			}
			if response.Failure == nil {
				return "", errors.New("harness: only failed model response bodies can be read as activity")
			}
			return response.Content, nil
		}
		if activityKind == "workflow" {
			var result ToolResult
			if json.Unmarshal([]byte(raw), &result) != nil {
				return "", ErrStoreFormat
			}
			return result.Content, nil
		}
		var result ToolResult
		if json.Unmarshal([]byte(raw), &result) != nil {
			return "", ErrStoreFormat
		}
		return result.Content, nil
	default:
		return "", errors.New("harness: unknown Chat body kind")
	}
}

// Keep snapshots bounded independently of task length. Full saved attempts are
// available through Inspect and explicit raw reads, not repeated on each tick.
func listChatAttempts(ctx context.Context, tx *sql.Tx, run string) ([]Attempt, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,kind,call_id,status,request_json,response_json,seq FROM fa.attempts WHERE run_id=$1 ORDER BY seq DESC LIMIT 129`, run)
	if err != nil {
		return nil, false, err
	}
	var attempts []Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			rows.Close()
			return nil, false, err
		}
		attempts = append(attempts, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, false, err
	}
	earlier := len(attempts) > 128
	if earlier {
		attempts = attempts[:128]
	}
	slices.Reverse(attempts)
	return attempts, earlier, nil
}
