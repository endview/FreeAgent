package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
)

// ChatView is a read-only projection, not another persisted execution model.
type ChatView struct {
	Conversation Conversation        `json:"conversation"`
	ModelID      string              `json:"model_id"`
	Permission   PermissionSelection `json:"permission"`
	Capabilities CapabilitySelection `json:"capabilities"`
	Turns        []ChatTurn          `json:"turns"`
	HasEarlier   bool                `json:"has_earlier"`
	Before       string              `json:"before,omitempty"`
}

type ChatTurn struct {
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
	Activity        []ChatActivity       `json:"activity"`
	Evidence        Evidence             `json:"evidence"`
	Sources         []MemoryUse          `json:"sources"`
	CanContinue     bool                 `json:"can_continue"`
	CanEnd          bool                 `json:"can_end"`
	Ended           bool                 `json:"ended"`
	Confirmation    *Confirmation        `json:"confirmation,omitempty"`
	Maintenance     bool                 `json:"maintenance,omitempty"`
}

type ChatMessage struct {
	Key       string `json:"key"`
	Role      string `json:"role"`
	Raw       string `json:"raw"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ChatActivity struct {
	Key       string        `json:"key"`
	Kind      string        `json:"kind"`
	Name      string        `json:"name,omitempty"`
	Target    string        `json:"target,omitempty"`
	Status    string        `json:"status"`
	IsError   bool          `json:"is_error,omitempty"`
	Text      string        `json:"text,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	Receipts  []ToolReceipt `json:"receipts,omitempty"`
	Failure   *CallError    `json:"failure,omitempty"`
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
		if attempt.Status == "unknown" {
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
	tx, err := tasks.core.Store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ChatView{}, err
	}
	defer tx.Rollback()
	condition := ""
	args := []any{conversationID}
	if before != "" {
		var rowID int64
		if err := tx.QueryRowContext(ctx, `SELECT rowid FROM runs WHERE id=? AND conversation_id=?`, before, conversationID).Scan(&rowID); err != nil {
			return ChatView{}, err
		}
		condition = " AND rowid<?"
		args = append(args, rowID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+runColumns+` FROM runs WHERE conversation_id=?
		AND NOT(status='completed' AND EXISTS(SELECT 1 FROM meta WHERE key='`+maintenanceMetaPrefix+`'||runs.id))`+condition+` ORDER BY rowid DESC LIMIT 33`, args...)
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
		attempts, err := listAttempts(ctx, tx, run.ID)
		if err != nil {
			return ChatView{}, err
		}
		turn := ChatTurn{Key: run.ID, RequestKey: run.RequestKey, State: chatState(run, attempts), Reason: run.StopReason,
			Steps: run.Steps, ElapsedMS: activeElapsed(run),
			ModelID: run.ModelID, Permission: run.Permission, Messages: make([]ChatMessage, 0), Activity: make([]ChatActivity, 0),
			Sources: make([]MemoryUse, 0), Evidence: attemptEvidence(attempts), Ended: run.Status == "abandoned"}
		if strings.HasPrefix(run.RequestKey, "compact-") {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM meta WHERE key=?)`, maintenanceMetaPrefix+run.ID).Scan(&turn.Maintenance); err != nil {
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
		turn.CanEnd = run.Status != "running" && run.Status != "completed" && run.Status != "failed" && !turn.Ended
		messages, err := tx.QueryContext(ctx, `SELECT id,body_json FROM messages WHERE run_id=? ORDER BY id`, run.ID)
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
				turn.Messages = append(turn.Messages, ChatMessage{Key: strconv.FormatInt(id, 10), Role: message.Role, Raw: text, Truncated: truncated})
			}
		}
		if err := errors.Join(messages.Err(), messages.Close()); err != nil {
			return ChatView{}, err
		}
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
					} else if response.Failure != nil {
						activity.Failure, activity.IsError = response.Failure, true
						activity.Text, activity.Truncated = chatText(response.Content)
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
				addSources(result.Memory)
			}
			turn.Activity = append(turn.Activity, activity)
		}
		view.Turns = append(view.Turns, turn)
	}
	if len(runs) != 0 {
		view.Before = runs[0].ID
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
		err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT m.body_json FROM messages m
			JOIN runs r ON r.id=m.run_id WHERE r.conversation_id=? AND m.id=?`, conversationID, key).Scan(&raw)
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
		err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT a.kind,a.status,a.response_json FROM attempts a
			JOIN runs r ON r.id=a.run_id WHERE r.conversation_id=? AND a.id=?`, conversationID, key).Scan(&activityKind, &activityStatus, &raw)
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
