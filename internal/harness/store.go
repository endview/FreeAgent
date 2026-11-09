package harness

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrStoreFormat   = errors.New("harness: unsupported store format; choose a new data directory")
	ErrOwnerActive   = errors.New("harness: data directory already in use")
	ErrActiveRun     = errors.New("harness: conversation has an unfinished run")
	ErrUnknownResult = errors.New("harness: unresolved call; automatic replay is forbidden")
	ErrRequestKey    = errors.New("harness: request key was already used for a different request")
	ErrStepLimit     = errors.New("harness: explicit child step budget reached")
	ErrPinnedLimit   = errors.New("harness: pinned conversation limit reached")
)

const (
	storeSchemaVersion = 1
)

type Store struct {
	scheduledMu      sync.Mutex
	scheduled        *scheduledOwner
	channelBridge    atomic.Pointer[ChannelBridge]
	inputMu          sync.Mutex
	cancelMu         sync.Mutex
	requestCancels   map[string]context.CancelFunc
	telegramMu       sync.Mutex
	telegramBot      string
	telegramBots     map[string]bool
	telegramRequests map[string]TelegramAccessRequest
	securityMu       sync.Mutex
	attachmentMu     sync.Mutex
	replyMu          sync.Mutex
	dataDir          string
	db               *pgDatabase
	pgOwner          *pgOwner
	runtimeRelease   func() error
	owner            processFileLock
	mu               sync.Mutex
	runMu            executionGate
	modelMu          sync.Mutex
	modelSlots       chan struct{}
	closed           bool
	summaryVerified  map[string]string
}

type Conversation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	SpaceID   string `json:"space_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ModelID   string `json:"model_id,omitempty"`
	Pinned    bool   `json:"pinned"`
	PinnedAt  string `json:"pinned_at,omitempty"`
}

// Space is the shared organizational boundary for local and external
// conversations. External channel fields are inert until an adapter is
// enabled; the core owns identity and routing rules.
type Space struct {
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	Source                string `json:"source"`
	ChatKind              string `json:"chat_kind,omitempty"`
	ExternalChat          string `json:"external_chat,omitempty"`
	DefaultConversationID string `json:"default_conversation_id,omitempty"`
	ActiveConversationID  string `json:"active_conversation_id,omitempty"`
	MultiConversation     bool   `json:"multi_conversation_enabled"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

const (
	LocalSpaceID        = "local"
	SpaceSourceLocal    = "local"
	SpaceSourceTelegram = "telegram"
	SpaceSourceLark     = "lark"
	SpaceSourceQQ       = "qq"
)

type Message struct {
	ID    int64
	RunID string
	ModelMessage
}

type Run struct {
	LimitSettled     bool                 `json:"limit_settled,omitempty"`
	ContinuationOf   string               `json:"continuation_of,omitempty"`
	ID               string               `json:"id"`
	ConversationID   string               `json:"conversation_id"`
	RequestKey       string               `json:"request_key"`
	Prompt           string               `json:"prompt"`
	ConfigJSON       string               `json:"-"`
	Status           string               `json:"status"`
	StopReason       string               `json:"stop_reason,omitempty"`
	Steps            int                  `json:"steps"`
	ElapsedMS        int64                `json:"elapsed_ms"`
	ActiveSince      string               `json:"-"`
	Output           string               `json:"output,omitempty"`
	CreatedAt        string               `json:"created_at"`
	UpdatedAt        string               `json:"updated_at"`
	InitialModel     string               `json:"initial_model,omitempty"`
	ModelID          string               `json:"model_id,omitempty"`
	ModelRevision    string               `json:"model_revision,omitempty"`
	Routing          []RoutingDecision    `json:"routing,omitempty"`
	Permission       *PermissionSelection `json:"permission,omitempty"`
	ExecutionProfile *ExecutionProfile    `json:"execution_profile,omitempty"`
}

type Attempt struct {
	Replacement   string `json:"replacement_attempt,omitempty"`
	Resolution    string `json:"resolution,omitempty"`
	ID            string `json:"id"`
	RunID         string `json:"run_id"`
	Kind          string `json:"kind"`
	CallID        string `json:"call_id,omitempty"`
	Status        string `json:"status"`
	RequestJSON   string `json:"request_json"`
	ResponseJSON  string `json:"response_json,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	ModelRevision string `json:"model_revision,omitempty"`
	Sequence      int64  `json:"sequence,omitempty"`
}

func InitStore(ctx context.Context, dataDir string) error { return initPostgresStore(ctx, dataDir) }
func OpenStore(ctx context.Context, dataDir string) (*Store, error) {
	return openPostgresStore(ctx, dataDir, false)
}
func OpenStoreReadOnly(ctx context.Context, dataDir string) (*Store, error) {
	return openPostgresStore(ctx, dataDir, true)
}
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	err := store.db.Close()
	err = errors.Join(err, store.pgOwner.close(), store.owner.release())
	if store.runtimeRelease != nil {
		err = errors.Join(err, store.runtimeRelease())
	}
	return err
}
func (store *Store) DataDir() string { return store.dataDir }

func (store *Store) CreateConversation(ctx context.Context, title, workspace string) (Conversation, error) {
	return store.CreateConversationInSpace(ctx, LocalSpaceID, title, workspace)
}

func (store *Store) CreateConversationInSpace(ctx context.Context, spaceID, title, workspace string) (Conversation, error) {
	if title == "" || workspace == "" {
		return Conversation{}, errors.New("harness: title and workspace are required")
	}
	if _, err := store.GetSpace(ctx, spaceID); err != nil {
		return Conversation{}, err
	}
	result := Conversation{ID: newID(), Title: title, Workspace: workspace, CreatedAt: nowText(), UpdatedAt: nowText()}
	result.SpaceID = spaceID
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)`,
		result.ID, result.Title, result.Workspace, result.SpaceID, result.CreatedAt, result.UpdatedAt); err != nil {
		return Conversation{}, err
	}
	var defaultID string
	if err := tx.QueryRowContext(ctx, `SELECT default_conversation_id FROM fa.spaces WHERE id=$1`, spaceID).Scan(&defaultID); err != nil {
		return Conversation{}, err
	}
	if defaultID == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE fa.spaces SET default_conversation_id=$1, active_conversation_id=$2, updated_at=$3 WHERE id=$4`,
			result.ID, result.ID, result.UpdatedAt, spaceID); err != nil {
			return Conversation{}, err
		}
	}
	return result, tx.Commit()
}

var ErrInvalidConversationTitle = errors.New("harness: invalid conversation title")

func (store *Store) RenameConversation(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 80 || strings.ContainsAny(title, "\r\n") || hasControl(title) {
		return ErrInvalidConversationTitle
	}
	result, err := store.db.ExecContext(ctx, `UPDATE fa.conversations SET title=$1 WHERE id=$2`, title, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("harness: conversation %q not found", id)
	}
	return nil
}

func (store *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	err := store.db.QueryRowContext(ctx,
		`SELECT c.id, c.title, c.workspace, c.space_id, c.created_at, c.updated_at, coalesce(pin.value,'')
		 FROM fa.conversations c LEFT JOIN fa.meta pin ON pin.key='chat/pin/'||c.id WHERE c.id=$1`, id,
	).Scan(&result.ID, &result.Title, &result.Workspace, &result.SpaceID, &result.CreatedAt, &result.UpdatedAt, &result.PinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("harness: session %q not found; use sessions to choose one, or omit --session to create one", id)
	}
	if err == nil {
		result.Pinned = result.PinnedAt != ""
		var selected string
		modelErr := store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "chat/model/"+id).Scan(&selected)
		if modelErr != nil && !errors.Is(modelErr, sql.ErrNoRows) {
			return Conversation{}, modelErr
		}
		result.ModelID = selected
	}
	return result, err
}

func (store *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	return store.listConversations(ctx, "")
}

func (store *Store) listConversations(ctx context.Context, spaceID string) ([]Conversation, error) {
	result := make([]Conversation, 0, 250)
	for _, query := range []string{
		`SELECT c.id,c.title,c.workspace,c.space_id,c.created_at,c.updated_at,coalesce(model.value,''),pin.value
		 FROM fa.conversations c JOIN fa.meta pin ON pin.key='chat/pin/'||c.id
		 LEFT JOIN fa.meta model ON model.key='chat/model/'||c.id
		 WHERE ($1='' OR c.space_id=$2) AND pin.value<>'' ORDER BY pin.value DESC,c.updated_at DESC LIMIT 50`,
		`SELECT c.id,c.title,c.workspace,c.space_id,c.created_at,c.updated_at,coalesce(model.value,''),''
		 FROM fa.conversations c LEFT JOIN fa.meta pin ON pin.key='chat/pin/'||c.id
		 LEFT JOIN fa.meta model ON model.key='chat/model/'||c.id
		 WHERE ($1='' OR c.space_id=$2) AND (pin.key IS NULL OR pin.value='') ORDER BY c.updated_at DESC LIMIT 200`,
	} {
		rows, err := store.db.QueryContext(ctx, query, spaceID, spaceID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item Conversation
			if err := rows.Scan(&item.ID, &item.Title, &item.Workspace, &item.SpaceID, &item.CreatedAt, &item.UpdatedAt, &item.ModelID, &item.PinnedAt); err != nil {
				rows.Close()
				return nil, err
			}
			item.Pinned = item.PinnedAt != ""
			result = append(result, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

func (store *Store) ListConversationsInSpace(ctx context.Context, spaceID string) ([]Conversation, error) {
	if _, err := store.GetSpace(ctx, spaceID); err != nil {
		return nil, err
	}
	return store.listConversations(ctx, spaceID)
}

// PinConversation stores the pin timestamp in the existing metadata table. A
// pin is presentation state, so it does not create a new runtime object.
func (store *Store) PinConversation(ctx context.Context, id string, pinned bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM fa.conversations WHERE id=$1`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("harness: conversation %q not found", id)
		}
		return err
	}
	key := "chat/pin/" + id
	if pinned {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'chat/pin/%' AND value<>''`).Scan(&count); err != nil {
			return err
		}
		var already bool
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(value,'') FROM fa.meta WHERE key=$1`, key).Scan(&existing); err == nil {
			already = existing != ""
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !already && count >= 50 {
			return ErrPinnedLimit
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, nowText()); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, key); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, COALESCE(run_id,''), body_json FROM fa.messages WHERE conversation_id=$1 ORDER BY id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Message, 0)
	for rows.Next() {
		var item Message
		var body string
		if err := rows.Scan(&item.ID, &item.RunID, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &item.ModelMessage); err != nil {
			return nil, fmt.Errorf("harness: invalid stored message: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) contextMessages(ctx context.Context, run Run, maxMessages, maxBytes int) ([]Message, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,COALESCE(run_id,''),body_json FROM fa.messages
		WHERE conversation_id=$1 AND (id<=$2 OR run_id=$3) ORDER BY id DESC LIMIT $4`, run.ConversationID, historyBound(run), run.ID, maxMessages+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	total := 0
	for rows.Next() {
		var item Message
		var body string
		if err := rows.Scan(&item.ID, &item.RunID, &body); err != nil {
			return nil, err
		}
		total += len(body)
		if total > maxBytes {
			if item.RunID == run.ID {
				return nil, ErrContextLimit
			}
			// The oldest partially loaded run must not split tool-call groups.
			for len(result) > 0 && result[len(result)-1].RunID == item.RunID {
				result = result[:len(result)-1]
			}
			break
		}
		if err := json.Unmarshal([]byte(body), &item.ModelMessage); err != nil {
			return nil, errors.New("harness: invalid stored context message")
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(result)
	return result, nil
}

const runColumns = `id, conversation_id, request_key, prompt, config_json, status, stop_reason, steps, elapsed_ms, output, created_at, updated_at, active_since`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var result Run
	err := row.Scan(&result.ID, &result.ConversationID, &result.RequestKey, &result.Prompt,
		&result.ConfigJSON, &result.Status, &result.StopReason, &result.Steps, &result.ElapsedMS, &result.Output, &result.CreatedAt, &result.UpdatedAt, &result.ActiveSince)
	if err == nil {
		result = withModelFacts(result)
	}
	return result, err
}

func (store *Store) CreateRun(ctx context.Context, conversationID, requestKey, prompt, configJSON string) (Run, bool, error) {
	return store.createRun(ctx, conversationID, requestKey, prompt, configJSON, false)
}

func (store *Store) createRun(ctx context.Context, conversationID, requestKey, prompt, configJSON string, maintenance bool) (Run, bool, error) {
	return store.createRunWithTool(ctx, conversationID, requestKey, prompt, configJSON, maintenance, nil)
}

func (store *Store) createRunWithTool(ctx context.Context, conversationID, requestKey, prompt, configJSON string, maintenance bool, call *ToolCall) (Run, bool, error) {
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	var input struct {
		InputAttachments []AttachmentRef
		InputRequestID   string            `json:"input_request_id"`
		ExtensionTrigger *ExtensionTrigger `json:"extension_trigger"`
	}
	if json.Unmarshal([]byte(configJSON), &input) != nil {
		return Run{}, false, ErrStoreFormat
	}
	if requestKey == "" || prompt == "" {
		return Run{}, false, errors.New("harness: request key and prompt are required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	if err := lockConversationTx(ctx, tx, conversationID); err != nil {
		return Run{}, false, err
	}
	existing, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND request_key=$2`, conversationID, requestKey))
	if err == nil {
		if existing.Prompt != prompt || !sameInputAttachments(existing.ConfigJSON, input.InputAttachments) {
			return Run{}, false, ErrRequestKey
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, err
	}
	var continuation struct {
		ContinuationOf string `json:"continuation_of"`
	}
	_ = json.Unmarshal([]byte(configJSON), &continuation)
	if continuation.ContinuationOf != "" {
		next, err := successorTx(ctx, tx, continuation.ContinuationOf, conversationID)
		if err != nil {
			return Run{}, false, err
		}
		if next != "" {
			run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1`, next))
			return run, false, err
		}
	}
	var branchRaw string
	branchErr := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/branch/"+conversationID).Scan(&branchRaw)
	if branchErr == nil {
		var origin BranchOrigin
		if json.Unmarshal([]byte(branchRaw), &origin) != nil {
			return Run{}, false, ErrStoreFormat
		}
		if err := branchSourceReady(ctx, tx, origin.Source); err != nil {
			return Run{}, false, err
		}
	} else if !errors.Is(branchErr, sql.ErrNoRows) {
		return Run{}, false, branchErr
	}
	var blocked int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate, conversationID).Scan(&blocked); err != nil {
		return Run{}, false, err
	}
	if blocked > 0 {
		return Run{}, false, ErrActiveRun
	}
	var deferred *PendingInput
	if input.InputRequestID != "" {
		item, e := loadInput(ctx, tx, conversationID, input.InputRequestID)
		if e != nil {
			return Run{}, false, e
		}
		if item.Action != "queue" || item.Status != "pending" || item.Key != requestKey {
			return Run{}, false, ErrRequestKey
		}
		var first string
		if e := tx.QueryRowContext(ctx, `SELECT (document #>> '{key}') FROM fa.meta WHERE key LIKE $1 AND (document #>> '{action}')='queue' AND (document #>> '{status}')='pending' ORDER BY CAST((document #>> '{message_id}') AS INTEGER) LIMIT 1`, inputPrefix(conversationID)+"%").Scan(&first); e != nil {
			return Run{}, false, e
		}
		if first != item.Key {
			return Run{}, false, ErrInputPending
		}
		var pause string
		_ = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "input-paused/"+conversationID).Scan(&pause)
		if pause == "true" {
			return Run{}, false, ErrQueuePaused
		}
		deferred = &item
	}
	result := Run{ID: newID(), ConversationID: conversationID, RequestKey: requestKey, Prompt: prompt, ConfigJSON: configJSON, Status: "running", CreatedAt: nowText(), UpdatedAt: nowText()}
	if deferred != nil && deferred.Channel != nil {
		// The input message moves to adoption order. Freeze that new boundary
		// before publishing the new Run snapshot.
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT last_message_seq+1 FROM fa.conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&next); err != nil {
			return Run{}, false, err
		}
		var frozen runSnapshot
		if json.Unmarshal([]byte(result.ConfigJSON), &frozen) != nil || frozen.Channel == nil {
			return Run{}, false, ErrStoreFormat
		}
		frozen.Channel.InputMessageID, frozen.Channel.HistoryThrough = next, next
		result.ConfigJSON = string(mustJSON(frozen))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.runs(`+runColumns+`) VALUES($1,$2,$3,$4,$5,'running','',0,0,'',$6,$7,'')`,
		result.ID, conversationID, requestKey, prompt, result.ConfigJSON, result.CreatedAt, result.UpdatedAt); err != nil {
		return Run{}, false, err
	}
	if input.ExtensionTrigger != nil {
		trigger := input.ExtensionTrigger
		r, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('run_id',$1::text))::text WHERE key=$2 AND document->>'state'='queued' AND COALESCE(document->>'run_id','')=''`, result.ID, wakePrefix+trigger.Opportunity)
		if err != nil {
			return Run{}, false, err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return Run{}, false, ErrRequestKey
		}
	}
	if err := bindAttachments(ctx, tx, conversationID, result.ID, input.InputAttachments); err != nil {
		return Run{}, false, err
	}
	if deferred != nil {
		if err := moveInputMessage(ctx, tx, deferred, result.ID); err != nil {
			return Run{}, false, err
		}
	} else if maintenance {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, maintenanceMetaPrefix+result.ID, "compaction"); err != nil {
			return Run{}, false, err
		}
	} else if input.ExtensionTrigger != nil {
		// Extension-triggered Runs have no adopted user message.
	} else if origin := channelOriginFromJSON(configJSON); origin != nil && origin.InputMessageID > 0 {
		var original string
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, origin.InputMessageID, conversationID).Scan(&original); err != nil {
			return Run{}, false, err
		}
		var message ModelMessage
		if json.Unmarshal([]byte(original), &message) != nil || !slices.Equal(message.Attachments, input.InputAttachments) {
			return Run{}, false, ErrRequestKey
		}
		// Receipt and Run association commit atomically. The received message
		// already exists; never append a second trigger or execute a duplicate.
		updated, err := tx.ExecContext(ctx, `UPDATE fa.messages SET run_id=$1 WHERE id=$2 AND conversation_id=$3 AND request_key=$4 AND run_id IS NULL`,
			result.ID, origin.InputMessageID, conversationID, requestKey)
		if err != nil {
			return Run{}, false, err
		}
		if count, err := updated.RowsAffected(); err != nil || count != 1 {
			return Run{}, false, ErrRequestKey
		}
	} else {
		if err := insertMessage(ctx, tx, result, ModelMessage{Role: "user", Content: prompt, Attachments: input.InputAttachments, Public: channelOriginFromJSON(configJSON) != nil}); err != nil {
			return Run{}, false, err
		}
	}
	if call != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, directCommandPrefix+result.ID, "explicit_user_command"); err != nil {
			return Run{}, false, err
		}
		if err := insertMessage(ctx, tx, result, ModelMessage{Role: "assistant", ToolCalls: []ToolCall{*call}}); err != nil {
			return Run{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'tool',$3,'pending',$4,'')`, newID(), result.ID, call.ID, string(mustJSON(call))); err != nil {
			return Run{}, false, err
		}
	}
	if continuation.ContinuationOf != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "run/limit-next/"+continuation.ContinuationOf, result.ID); err != nil {
			return Run{}, false, err
		}
	}
	return withModelFacts(result), true, tx.Commit()
}

func (store *Store) repeatedRun(ctx context.Context, conversationID, requestKey, prompt string) (Run, bool, error) {
	run, err := scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs
		WHERE conversation_id=$1 AND request_key=$2`, conversationID, requestKey))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	if run.Prompt != prompt {
		return Run{}, false, ErrRequestKey
	}
	return run, true, nil
}

func (store *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1`, id))
}

func (store *Store) GetLatestRun(ctx context.Context, conversationID string) (Run, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1
		AND NOT (status='completed' AND EXISTS(SELECT 1 FROM fa.meta WHERE key=$2||runs.id))
		ORDER BY seq DESC LIMIT 1`, conversationID, maintenanceMetaPrefix))
}

func (store *Store) GetRunByRequest(ctx context.Context, conversationID, requestKey string) (Run, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(requestKey) == "" {
		return Run{}, sql.ErrNoRows
	}
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND request_key=$2`, conversationID, requestKey))
}

func (store *Store) FinishRun(ctx context.Context, id, status, reason string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE fa.runs SET status=$1, stop_reason=$2, updated_at=$3 WHERE id=$4`, status, reason, nowText(), id)
	return err
}

// FinishRunOutput records a workflow's user-visible assistant output and its
// terminal run state in one transaction. It intentionally reuses messages and
// runs instead of introducing a workflow-specific persistence object.
func (store *Store) FinishRunOutput(ctx context.Context, run Run, status, reason, output string) error {
	if !validWorkflowTerminalStatus(status) {
		return errors.New("harness: invalid workflow terminal status")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, run.ID).Scan(&currentStatus); err != nil {
		return err
	}
	if currentStatus != "running" {
		if currentStatus == "unknown" {
			return ErrUnknownResult
		}
		return ErrModelBoundary
	}
	if output != "" {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.messages
			WHERE run_id=$1 AND (body_json #>> '{Role}')='assistant'
			AND (body_json #>> '{Content}')=$2`, run.ID, output).Scan(&existing); err != nil {
			return err
		}
		if existing == 0 {
			if err := insertMessage(ctx, tx, run, ModelMessage{Role: "assistant", Content: output}); err != nil {
				return err
			}
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status=$1, stop_reason=$2, output=$3, updated_at=$4
		WHERE id=$5 AND status='running'`, status, reason, output, nowText(), run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return errors.Join(ErrModelBoundary, err)
	}
	return tx.Commit()
}

func validWorkflowTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "stopped", "unknown", "limited":
		return true
	default:
		return false
	}
}

type workflowAttemptRequest struct {
	ExtensionID string          `json:"extension_id"`
	Workflow    string          `json:"workflow"`
	Input       json.RawMessage `json:"input"`
}

func (store *Store) startWorkflow(ctx context.Context, run Run, extensionID, workflowName string, input any,
	maxSteps int, guard callGuard) (Attempt, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return Attempt{}, err
	}
	request, err := json.Marshal(workflowAttemptRequest{
		ExtensionID: extensionID,
		Workflow:    workflowName,
		Input:       rawInput,
	})
	if err != nil {
		return Attempt{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return Attempt{}, err
	}
	if err := incrementStep(ctx, tx, run.ID, maxSteps); err != nil {
		return Attempt{}, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, run.ID).Scan(&status); err != nil {
		return Attempt{}, err
	}
	if status != "running" {
		return Attempt{}, errors.New("harness: workflow requires a running task")
	}
	attempt := Attempt{ID: newID(), RunID: run.ID, Kind: "workflow", CallID: "workflow-" + newID(),
		Status: "dispatched", RequestJSON: string(request)}
	if guard != nil {
		if err := guard.Reserve(ctx, tx, attempt); err != nil {
			return Attempt{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'workflow',$3,'dispatched',$4,'')`,
		attempt.ID, run.ID, attempt.CallID, string(request)); err != nil {
		return Attempt{}, err
	}
	return attempt, tx.Commit()
}

// finishWorkflow atomically settles the workflow Attempt and its
// foreground Run. The response is deliberately kept out of chat history:
// workflow output is inserted as the single assistant message in the same
// transaction.
func (store *Store) finishWorkflow(ctx context.Context, run Run, attempt Attempt,
	result ToolResult, status, reason, output string, guard callGuard, outputBytes int) error {
	if !validWorkflowTerminalStatus(status) {
		return errors.New("harness: invalid workflow terminal status")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, run.ID).Scan(&currentStatus); err != nil {
		return err
	}
	if currentStatus != "running" {
		if currentStatus == "unknown" {
			return ErrUnknownResult
		}
		return ErrModelBoundary
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished', response_json=$1
		WHERE id=$2 AND run_id=$3 AND kind='workflow' AND status='dispatched'`, string(body), attempt.ID, run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return errors.Join(ErrModelBoundary, err)
	}
	if guard != nil {
		if err := guard.Record(ctx, tx, attempt, max(outputBytes, len(body)), status == "completed"); err != nil {
			if !isOutputLimit(err) {
				return err
			}
			status = "limited"
			reason = safeError(err)
		}
	}
	if output != "" {
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "assistant", Content: output}); err != nil {
			return err
		}
	}
	updated, err = tx.ExecContext(ctx, `UPDATE fa.runs SET status=$1, stop_reason=$2, output=$3, updated_at=$4
		WHERE id=$5 AND status='running'`, status, reason, output, nowText(), run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return errors.Join(ErrModelBoundary, err)
	}
	return tx.Commit()
}

func (store *Store) finishWorkflowUnknown(ctx context.Context, run Run, attempt Attempt, reason string, record string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	response := ToolResult{Content: reason, IsError: true}
	if record != "" {
		_ = json.Unmarshal([]byte(record), &response)
	}
	updated, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='unknown', response_json=$1
		WHERE id=$2 AND run_id=$3 AND kind='workflow' AND status='dispatched'`,
		string(mustJSON(response)), attempt.ID, run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return errors.Join(ErrModelBoundary, err)
	}
	updated, err = tx.ExecContext(ctx, `UPDATE fa.runs SET status='unknown', stop_reason=$1, updated_at=$2
		WHERE id=$3 AND status IN ('running','stopped','limited','waiting_confirmation')`, reason, nowText(), run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, run.ID).Scan(&current); err != nil {
			return err
		}
		if current != "unknown" {
			return errors.Join(ErrModelBoundary, errors.New("harness: workflow run disappeared before UNKNOWN settlement"))
		}
	}
	return tx.Commit()
}

func (store *Store) saveToolNoMessage(ctx context.Context, run Run, attempt Attempt, call ToolCall, result ToolResult) error {
	return store.saveGuardedToolNoMessage(ctx, run, attempt, call, result, nil, 0, false)
}

func (store *Store) saveGuardedToolNoMessage(ctx context.Context, run Run, attempt Attempt, call ToolCall, result ToolResult,
	guard callGuard, outputBytes int, definitive bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	outputErr, err := saveToolTxMode(ctx, tx, run, attempt, call, result, guard, outputBytes, definitive, false)
	if err != nil {
		return err
	}
	return errors.Join(outputErr, tx.Commit())
}

func saveToolTxMode(ctx context.Context, tx *sql.Tx, run Run, attempt Attempt, call ToolCall, result ToolResult,
	guard callGuard, outputBytes int, definitive, addMessage bool) (error, error) {
	if result.Outcome == nil {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.attempts WHERE id=$1 AND run_id=$2`, attempt.ID, run.ID).Scan(&status); err != nil {
			return nil, err
		}
		kind := "completed"
		if result.IsError {
			kind = "failed"
		}
		if status == "pending" {
			kind = "not_run"
		}
		result.Outcome = &ToolOutcome{Status: kind, Dispatched: status == "dispatched", CompletionKnown: true, Truncated: result.Truncated}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished', response_json=$1
		WHERE id=$2 AND run_id=$3 AND kind='tool' AND call_id=$4 AND status IN ('pending','dispatched')`, string(raw), attempt.ID, run.ID, call.ID)
	if err != nil {
		return nil, err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return nil, errors.Join(ErrModelBoundary, err)
	}
	if addMessage {
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: result.Content, Attachments: result.Attachments}); err != nil {
			return nil, err
		}
	}
	var outputErr error
	if guard != nil {
		outputErr = guard.Record(ctx, tx, attempt, max(outputBytes, len(raw)), definitive)
		if outputErr != nil && !isOutputLimit(outputErr) {
			return nil, outputErr
		}
		if outputErr != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='limited',stop_reason=$1,updated_at=$2 WHERE id=$3`, safeError(outputErr), nowText(), run.ID); err != nil {
				return nil, err
			}
		}
	}
	return outputErr, nil
}

func (store *Store) Abandon(ctx context.Context, runID string) error {
	if !store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer store.runMu.Unlock()
	if parent, err := store.controlledParent(ctx, runID); err != nil || parent != "" {
		return errors.Join(ErrControlledRun, err)
	}
	return store.abandonRun(ctx, runID)
}

func (store *Store) abandonRun(ctx context.Context, runID string) error {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == "completed" {
		return errors.New("harness: completed runs cannot be abandoned")
	}
	if run.Status == "abandoned" {
		return nil
	}
	attempts, err := store.ListAttempts(ctx, runID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Status == "dispatched" {
			return ErrUnknownResult
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, runID); err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Kind != "tool" || (attempt.Status != "pending" && attempt.Status != "unknown") {
			continue
		}
		var call ToolCall
		if err := json.Unmarshal([]byte(attempt.RequestJSON), &call); err != nil {
			return err
		}
		response := ToolResult{Content: "user abandoned the run before this tool was dispatched; do not repeat it", IsError: true}
		if attempt.Status == "unknown" {
			response.Content = "outcome UNKNOWN: the operation may have completed; user ended this task without resolving it. This is not a tool success. Do not repeat this operation implicitly."
		}
		raw, _ := json.Marshal(response)
		if attempt.Status == "pending" {
			if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished', response_json=$1 WHERE id=$2`, string(raw), attempt.ID); err != nil {
				return err
			}
		}
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: response.Content}); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='abandoned', stop_reason='abandoned by user; unknown attempt facts are retained', updated_at=$1 WHERE id=$2`, nowText(), runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) failModel(ctx context.Context, run Run, attempt Attempt, response ModelResponse, failure *CallError, guard callGuard, outputBytes int) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	response.ToolCalls, response.Failure = nil, failure
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished', response_json=$1 WHERE id=$2 AND status='dispatched'`, string(raw), attempt.ID); err != nil {
		return err
	}
	if !isSupplementalAttempt(attempt) && !isCompactionAttempt(attempt) {
		if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='failed', stop_reason=$1, updated_at=$2 WHERE id=$3`, safeError(failure), nowText(), run.ID); err != nil {
			return err
		}
	}
	var outputErr error
	if guard != nil {
		// Failure is definitive, but does not refund its output allowance.
		outputErr = guard.Record(ctx, tx, attempt, max(outputBytes, len(raw)), false)
		if outputErr != nil && !isOutputLimit(outputErr) {
			return outputErr
		}
	}
	return errors.Join(outputErr, tx.Commit())
}

func VerifyData(ctx context.Context, dataDir string) error {
	c, release, err := postgres.Acquire(ctx, dataDir)
	if err != nil {
		return err
	}
	defer release()
	db, err := postgres.SQL(c, "", true)
	if err != nil {
		return err
	}
	defer db.Close()
	return verifyPostgres(ctx, db, c.CoreID)
}

func (store *Store) startModel(ctx context.Context, run Run, request string, maxSteps int) (Attempt, error) {
	return store.startGuardedModel(ctx, run, request, maxSteps, nil)
}

func (store *Store) startGuardedModel(ctx context.Context, run Run, request string, maxSteps int, guard callGuard) (Attempt, error) {
	if guard == nil {
		if err := store.checkUncontrolled(ctx, run.ConversationID); err != nil {
			return Attempt{}, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return Attempt{}, err
	}
	var requestPurpose struct{ Purpose string }
	if json.Unmarshal([]byte(request), &requestPurpose) != nil {
		return Attempt{}, ErrStoreFormat
	}
	if requestPurpose.Purpose == "" {
		pending, e := pendingSteer(ctx, tx, run.ID)
		if e != nil {
			return Attempt{}, e
		}
		if pending {
			return Attempt{}, ErrInputPending
		}
	}
	if err := incrementStep(ctx, tx, run.ID, maxSteps); err != nil {
		return Attempt{}, err
	}
	attempt := Attempt{ID: newID(), RunID: run.ID, Kind: "model", Status: "dispatched", RequestJSON: request}
	var modelRequest ModelRequest
	if err := json.Unmarshal([]byte(request), &modelRequest); err != nil {
		return Attempt{}, err
	}
	if err := validateRequestMemory(ctx, tx, run, modelRequest); err != nil {
		return Attempt{}, err
	}
	if modelRequest.SupplementalKey != "" {
		if err := reserveSupplementalTx(ctx, tx, run, modelRequest); err != nil {
			return Attempt{}, err
		}
	}
	if modelRequest.Purpose == "assessment" {
		snapshot, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil || snapshot.Auto == nil {
			return Attempt{}, errors.Join(ErrAutoDisabled, err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts
			WHERE run_id=$1 AND kind='model' AND (request_doc #>> '{Purpose}')='assessment'`, run.ID).Scan(&count); err != nil {
			return Attempt{}, err
		}
		if count >= snapshot.Auto.MaxAssessments || modelRequest.ModelID != snapshot.Auto.AssessmentModel {
			return Attempt{}, ErrAutoDisabled
		}
	}
	attempt.ModelID, attempt.ModelRevision = modelRequest.ModelID, modelRequest.ModelRevision
	if guard != nil {
		if err := guard.Reserve(ctx, tx, attempt); err != nil {
			return Attempt{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'model','','dispatched',$3,'')`, attempt.ID, run.ID, request); err != nil {
		return Attempt{}, err
	}
	if modelRequest.Recovery != nil {
		if err := recordModelReplacement(ctx, tx, attempt, *modelRequest.Recovery); err != nil {
			return Attempt{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "attempt/time/"+attempt.ID, nowText()); err != nil {
		return Attempt{}, err
	}
	return attempt, tx.Commit()
}

func (store *Store) saveModel(ctx context.Context, run Run, attempt Attempt, response ModelResponse) error {
	return store.saveGuardedModel(ctx, run, attempt, response, nil, 0)
}

func (store *Store) saveGuardedModel(ctx context.Context, run Run, attempt Attempt, response ModelResponse, guard callGuard, outputBytes int) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished', response_json=$1 WHERE id=$2 AND status='dispatched'`, string(body), attempt.ID); err != nil {
		return err
	}
	var request ModelRequest
	if err := json.Unmarshal([]byte(attempt.RequestJSON), &request); err != nil {
		return err
	}
	if request.Purpose == "" {
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls, ProviderItems: response.ProviderItems, ChatContinuation: response.ChatContinuation}); err != nil {
			return err
		}
		for _, call := range response.ToolCalls {
			raw, err := json.Marshal(call)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'tool',$3,'pending',$4,'')`, newID(), run.ID, call.ID, string(raw)); err != nil {
				return err
			}
		}
		steered, err := pendingSteer(ctx, tx, run.ID)
		if err != nil {
			return err
		}
		if len(response.ToolCalls) == 0 && !steered {
			if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='completed', output=$1, stop_reason='', updated_at=$2 WHERE id=$3`, response.Content, nowText(), run.ID); err != nil {
				return err
			}
		}
	} else if len(response.ToolCalls) != 0 && request.Purpose != "verification" {
		return ErrModelCapability
	}
	var outputErr error
	if guard != nil {
		outputErr = guard.Record(ctx, tx, attempt, max(outputBytes, len(body)), true)
		if outputErr != nil && !isOutputLimit(outputErr) {
			return outputErr
		}
		if outputErr != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='limited',stop_reason=$1,updated_at=$2 WHERE id=$3`, safeError(outputErr), nowText(), run.ID); err != nil {
				return err
			}
		}
	}
	return errors.Join(outputErr, tx.Commit())
}

func (store *Store) ListAttempts(ctx context.Context, runID string) ([]Attempt, error) {
	return listAttempts(ctx, store.db, runID)
}

// countModelAttempts counts a Run's model calls without decoding their requests.
func (store *Store) countModelAttempts(ctx context.Context, runID string) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts WHERE run_id=$1 AND kind='model'`, runID).Scan(&count)
	return count, err
}

func (store *Store) GetAttempt(ctx context.Context, attemptID string) (Attempt, error) {
	return scanAttempt(store.db.QueryRowContext(ctx, `SELECT id, run_id, kind, call_id, status,
		request_json, response_json, seq FROM fa.attempts WHERE id=$1`, attemptID))
}

type attemptReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type attemptScanner interface {
	Scan(...any) error
}

func scanAttempt(row attemptScanner) (Attempt, error) {
	var item Attempt
	err := row.Scan(&item.ID, &item.RunID, &item.Kind, &item.CallID, &item.Status,
		&item.RequestJSON, &item.ResponseJSON, &item.Sequence)
	if err == nil && item.Kind == "model" {
		var request ModelRequest
		if err := json.Unmarshal([]byte(item.RequestJSON), &request); err != nil {
			return Attempt{}, ErrStoreFormat
		}
		item.ModelID, item.ModelRevision = request.ModelID, request.ModelRevision
	}
	return item, err
}

func listAttempts(ctx context.Context, reader attemptReader, runID string) ([]Attempt, error) {
	rows, err := reader.QueryContext(ctx, `SELECT id, run_id, kind, call_id, status, request_json, response_json,seq FROM fa.attempts WHERE run_id=$1 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Attempt, 0)
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return result, annotateReplacements(ctx, reader, runID, result)
}

func (store *Store) dispatchTool(ctx context.Context, attempt Attempt, maxSteps int) error {
	return store.dispatchGuardedTool(ctx, attempt, maxSteps, nil)
}

func (store *Store) startTool(ctx context.Context, run Run, call ToolCall) (Attempt, error) {
	if call.ID == "" || call.Name == "" || len(call.Arguments) == 0 {
		return Attempt{}, errors.New("harness: invalid tool call")
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return Attempt{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return Attempt{}, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, run.ID).Scan(&status); err != nil {
		return Attempt{}, err
	}
	if status != "running" {
		return Attempt{}, errors.New("harness: tool call requires a running task")
	}
	attempt := Attempt{ID: newID(), RunID: run.ID, Kind: "tool", CallID: call.ID,
		Status: "pending", RequestJSON: string(raw)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'tool',$3,'pending',$4,'')`,
		attempt.ID, run.ID, call.ID, string(raw)); err != nil {
		return Attempt{}, err
	}
	return attempt, tx.Commit()
}

func (store *Store) dispatchGuardedTool(ctx context.Context, attempt Attempt, maxSteps int, guard callGuard) error {
	if guard == nil {
		if parent, err := store.controlledParent(ctx, attempt.RunID); err != nil || parent != "" {
			return errors.Join(ErrControlledRun, err)
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, attempt.RunID); err != nil {
		return err
	}
	steered, err := pendingSteer(ctx, tx, attempt.RunID)
	if err != nil {
		return err
	}
	if steered {
		return ErrInputPending
	}
	if guard != nil {
		if err := guard.Reserve(ctx, tx, attempt); err != nil {
			return err
		}
	}
	if err := incrementStep(ctx, tx, attempt.RunID, maxSteps); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='dispatched' WHERE id=$1 AND status='pending'`, attempt.ID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return errors.New("harness: tool attempt is not pending")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "attempt/time/"+attempt.ID, nowText()); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) saveTool(ctx context.Context, run Run, attempt Attempt, call ToolCall, result ToolResult) error {
	return store.saveGuardedTool(ctx, run, attempt, call, result, nil, 0, false)
}

func (store *Store) saveGuardedTool(ctx context.Context, run Run, attempt Attempt, call ToolCall, result ToolResult, guard callGuard, outputBytes int, definitive bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	outputErr, err := saveToolTxMode(ctx, tx, run, attempt, call, result, guard, outputBytes, definitive, true)
	if err != nil {
		return err
	}
	return errors.Join(outputErr, tx.Commit())
}

func saveToolTx(ctx context.Context, tx *sql.Tx, run Run, attempt Attempt, call ToolCall, result ToolResult, guard callGuard, outputBytes int, definitive bool) (error, error) {
	return saveToolTxMode(ctx, tx, run, attempt, call, result, guard, outputBytes, definitive, true)
}

func (store *Store) executeLocalTool(ctx context.Context, run Run, attempt Attempt, call ToolCall,
	execute func(context.Context, *sql.Tx) (ToolResult, error), guard callGuard, maxResult int) (ToolResult, error) {
	return store.executeLocalToolMode(ctx, run, attempt, call, execute, guard, maxResult, true)
}

func (store *Store) executeLocalToolNoMessage(ctx context.Context, run Run, attempt Attempt, call ToolCall,
	execute func(context.Context, *sql.Tx) (ToolResult, error), guard callGuard, maxResult int) (ToolResult, error) {
	return store.executeLocalToolMode(ctx, run, attempt, call, execute, guard, maxResult, false)
}

func (store *Store) executeLocalToolMode(ctx context.Context, run Run, attempt Attempt, call ToolCall,
	execute func(context.Context, *sql.Tx) (ToolResult, error), guard callGuard, maxResult int, addMessage bool) (ToolResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolResult{}, err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return ToolResult{}, err
	}
	var status, saved, request string
	if err := tx.QueryRowContext(ctx, `SELECT status,response_json,request_json FROM fa.attempts
		WHERE id=$1 AND run_id=$2 AND kind='tool' AND call_id=$3`,
		attempt.ID, run.ID, call.ID).Scan(&status, &saved, &request); err != nil {
		return ToolResult{}, err
	}
	callJSON, err := json.Marshal(call)
	if err != nil || request != string(callJSON) {
		return ToolResult{}, errors.Join(ErrRequestKey, err)
	}
	if status == "finished" {
		var result ToolResult
		if err := json.Unmarshal([]byte(saved), &result); err != nil {
			return ToolResult{}, ErrStoreFormat
		}
		return result, nil
	}
	if status != "dispatched" {
		return ToolResult{}, ErrUnknownResult
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT local_tool`); err != nil {
		return ToolResult{}, err
	}
	started := time.Now()
	result, callErr := execute(ctx, tx)
	if callErr != nil {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO local_tool`); err != nil {
			return ToolResult{}, errors.Join(callErr, err)
		}
		result = ToolResult{Content: "local data operation error: " + safeError(callErr), IsError: true}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE local_tool`); err != nil {
		return ToolResult{}, err
	}
	// Transaction rollback makes local callback errors definite; duration still
	// measures the actual callback rather than approval waiting.
	result = normalizeToolOutcome(ctx, result, nil, started, maxResult)
	raw, err := json.Marshal(result)
	if err != nil {
		return ToolResult{}, err
	}
	outputBytes := len(raw)
	if len(result.Content) > maxResult {
		return ToolResult{}, ErrContextLimit
	}
	outputErr, err := saveToolTxMode(ctx, tx, run, attempt, call, result, guard, outputBytes, true, addMessage)
	if err != nil {
		return ToolResult{}, err
	}
	return result, errors.Join(outputErr, tx.Commit())
}

func (store *Store) markUnknown(ctx context.Context, attempt Attempt, reason string, record ...string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, attempt.RunID); err != nil {
		return err
	}
	body := reason
	responseExpr := `CASE WHEN response_json='' THEN $1 ELSE response_json END`
	if len(record) != 0 {
		body = record[0]
		responseExpr = `$1`
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='unknown', response_json=`+responseExpr+` WHERE id=$2`, body, attempt.ID); err != nil {
		return err
	}
	if !isSupplementalAttempt(attempt) {
		if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='unknown', stop_reason=$1, updated_at=$2 WHERE id=$3`, reason, nowText(), attempt.RunID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// settleControlProposal closes the pending proposal Attempt without dispatching
// an external effect. Applied changes complete the proposing Run; a rejection
// leaves it running so the existing continuation path can produce the next
// model response.
func (store *Store) settleControlProposal(ctx context.Context, run Run, attempt Attempt, result ToolResult, completed bool, reason string, terminal ...string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.attempts WHERE id=$1 AND run_id=$2 AND kind='tool'`, attempt.ID, run.ID).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return ErrControlAlreadyUsed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished',response_json=$1 WHERE id=$2 AND status='pending'`, string(raw), attempt.ID); err != nil {
		return err
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(attempt.RequestJSON), &call); err != nil {
		return err
	}
	if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: result.Content, Attachments: result.Attachments}); err != nil {
		return err
	}
	nextStatus, stopReason := "running", ""
	if completed {
		nextStatus, stopReason = "completed", reason
	}
	if len(terminal) > 0 {
		nextStatus, stopReason = terminal[0], reason
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status=$1,stop_reason=$2,updated_at=$3 WHERE id=$4 AND status IN ('waiting_confirmation','stopped','running')`, nextStatus, stopReason, nowText(), run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) recover(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, active_since FROM fa.runs WHERE active_since!=''`)
	if err != nil {
		return err
	}
	type elapsedRun struct {
		id      string
		elapsed int64
	}
	var elapsed []elapsedRun
	for rows.Next() {
		var id, since string
		if err := rows.Scan(&id, &since); err != nil {
			_ = rows.Close()
			return err
		}
		start, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			_ = rows.Close()
			return ErrStoreFormat
		}
		elapsed = append(elapsed, elapsedRun{id, max(0, time.Since(start).Milliseconds())})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, item := range elapsed {
		if _, err := tx.ExecContext(ctx, `UPDATE fa.runs SET elapsed_ms=elapsed_ms+$1, active_since='' WHERE id=$2`, item.elapsed, item.id); err != nil {
			return err
		}
	}
	statements := []string{
		`UPDATE fa.meta SET value=(jsonb_set(document,'{state}','"unknown"'::jsonb,true))::text WHERE (key LIKE 'channel/send/%' OR key LIKE 'channel/file-send/%' OR key LIKE 'channel/reply-file/%') AND (document #>> '{state}') IN ('dispatching','creating')`,
		`UPDATE fa.attempts SET status='unknown', response_json=CASE WHEN response_json='' THEN 'process interrupted after dispatch marker' ELSE response_json END WHERE status='dispatched'`,
		`UPDATE fa.runs SET status='unknown', stop_reason='unresolved call after restart' WHERE status NOT IN ('abandoned','unknown') AND id IN (SELECT a.run_id FROM fa.attempts a WHERE a.status='unknown' AND COALESCE(a.request_doc->>'SupplementalKey','')='' AND NOT ` + resolvedAttemptSQL + `)`,
		`UPDATE fa.runs SET status='stopped', stop_reason='process interrupted; continue explicitly' WHERE status='running'`,
		`UPDATE fa.runs SET status='failed',stop_reason='scheduled evaluation interrupted; inspect original Attempt, no automatic re-execution' WHERE config_doc->'extension_trigger' IS NOT NULL AND status IN ('stopped','unknown')`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := recoverCollaborationLeases(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func insertMessage(ctx context.Context, tx *sql.Tx, run Run, message ModelMessage) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.messages(conversation_id, run_id, body_json) VALUES($1,$2,$3)`, run.ConversationID, run.ID, string(raw)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.conversations SET updated_at=$1 WHERE id=$2`, nowText(), run.ConversationID)
	return err
}

func incrementStep(ctx context.Context, tx *sql.Tx, runID string, maxSteps int) error {
	result, err := tx.ExecContext(ctx, `UPDATE fa.runs SET steps=steps+1 WHERE id=$1 AND ($2=0 OR steps<$3) AND status='running'`, runID, maxSteps, maxSteps)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if maxSteps == 0 {
			return errors.Join(ErrActiveRun, err)
		}
		return ErrStepLimit
	}
	return nil
}

func seedLocalSpace(ctx context.Context, db *sql.DB) error {
	now := nowText()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.spaces(id,title,source,chat_kind,external_chat,default_conversation_id,active_conversation_id,multi_conversation_enabled,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		LocalSpaceID, "本地", SpaceSourceLocal, "", "", "", "", false, now, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "space/permission/"+LocalSpaceID, string(mustJSON(PermissionSelection{SpaceID: LocalSpaceID, Source: "space", Mode: PermissionNone}))); err != nil {
		return err
	}
	return tx.Commit()
}

func canonicalDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("harness: explicit data directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("harness: linked data directories are not supported")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return absolute, nil
}

func newID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func contentHash(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (store *Store) unresolvedAttempts(ctx context.Context, run string) ([]Attempt, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,run_id,kind,call_id,status,request_json,response_json,seq FROM fa.attempts WHERE run_id=$1 AND status IN ('pending','dispatched','unknown') ORDER BY seq`, run)
	if err != nil {
		return nil, err
	}
	var attempts []Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, errors.Join(rows.Err(), rows.Close())
}
