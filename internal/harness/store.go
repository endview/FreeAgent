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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrStoreFormat   = errors.New("harness: unsupported store format; choose a new data directory")
	ErrOwnerActive   = errors.New("harness: data directory already in use")
	ErrActiveRun     = errors.New("harness: conversation has an unfinished run")
	ErrUnknownResult = errors.New("harness: unresolved call; automatic replay is forbidden")
	ErrRequestKey    = errors.New("harness: request key was already used for a different request")
	ErrStepLimit     = errors.New("harness: step limit reached")
	ErrPinnedLimit   = errors.New("harness: pinned conversation limit reached")
)

const (
	applicationID      = 0x46414833
	storeSchemaVersion = 7
)

type Store struct {
	dataDir         string
	db              *sql.DB
	owner           processFileLock
	mu              sync.Mutex
	runMu           sync.Mutex
	closed          bool
	summaryVerified map[string]string
}

type Conversation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ModelID   string `json:"model_id,omitempty"`
	Pinned    bool   `json:"pinned"`
	PinnedAt  string `json:"pinned_at,omitempty"`
}

type Message struct {
	ID    int64
	RunID string
	ModelMessage
}

type Run struct {
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

func InitStore(ctx context.Context, dataDir string) (returnErr error) {
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("harness: initialization requires an empty new data directory")
	}
	owner, err := acquireProcessFileLock(filepath.Join(dataDir, "owner.lock"))
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, owner.release()) }()
	temporary, err := os.CreateTemp(dataDir, ".state-init-*")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	defer os.Remove(tempPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqliteDSN(tempPath, "rw"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	schemaErr := createSchema(ctx, db)
	closeErr := db.Close()
	if err := errors.Join(schemaErr, closeErr); err != nil {
		return err
	}
	if err := publishNoReplace(tempPath, filepath.Join(dataDir, "state.sqlite")); err != nil {
		return err
	}
	for _, name := range []string{"instructions", "backups"} {
		if err := os.Mkdir(filepath.Join(dataDir, name), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func OpenStore(ctx context.Context, dataDir string) (_ *Store, returnErr error) {
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "state.sqlite")
	if err := verifyRegularFile(path); err != nil {
		return nil, fmt.Errorf("harness: state store: %w", err)
	}
	// Verify without enabling WAL or writing recovery state into an unknown file.
	if err := verifyStore(ctx, path); err != nil {
		return nil, err
	}
	if err := verifyRegularFile(filepath.Join(dataDir, "config.json")); err != nil {
		return nil, err
	}
	if _, err := backupConfig(dataDir); err != nil {
		return nil, err
	}
	owner, err := acquireProcessFileLock(filepath.Join(dataDir, "owner.lock"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			_ = owner.release()
		}
	}()
	db, err := sql.Open("sqlite", sqliteDSN(path, "rw"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{dataDir: dataDir, db: db, owner: owner}
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := store.recover(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// OpenStoreReadOnly opens a verified store for viewing while another process
// may own it. It takes no owner lock, writes no backup and runs no recovery,
// so every write through the returned store fails.
func OpenStoreReadOnly(ctx context.Context, dataDir string) (*Store, error) {
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "state.sqlite")
	if err := verifyRegularFile(path); err != nil {
		return nil, fmt.Errorf("harness: state store: %w", err)
	}
	if err := verifyStore(ctx, path); err != nil {
		return nil, err
	}
	if err := verifyRegularFile(filepath.Join(dataDir, "config.json")); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, "ro"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{dataDir: dataDir, db: db}, nil
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
	return errors.Join(store.db.Close(), store.owner.release())
}

func (store *Store) DataDir() string { return store.dataDir }

func (store *Store) CreateConversation(ctx context.Context, title, workspace string) (Conversation, error) {
	if title == "" || workspace == "" {
		return Conversation{}, errors.New("harness: title and workspace are required")
	}
	result := Conversation{ID: newID(), Title: title, Workspace: workspace, CreatedAt: nowText(), UpdatedAt: nowText()}
	_, err := store.db.ExecContext(ctx, `INSERT INTO conversations VALUES(?, ?, ?, ?, ?)`,
		result.ID, result.Title, result.Workspace, result.CreatedAt, result.UpdatedAt)
	return result, err
}

var ErrInvalidConversationTitle = errors.New("harness: invalid conversation title")

func (store *Store) RenameConversation(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 80 || strings.ContainsAny(title, "\r\n") || hasControl(title) {
		return ErrInvalidConversationTitle
	}
	result, err := store.db.ExecContext(ctx, `UPDATE conversations SET title=? WHERE id=?`, title, id)
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
		`SELECT c.id, c.title, c.workspace, c.created_at, c.updated_at, coalesce(pin.value,'')
		 FROM conversations c LEFT JOIN meta pin ON pin.key='chat/pin/'||c.id WHERE c.id=?`, id,
	).Scan(&result.ID, &result.Title, &result.Workspace, &result.CreatedAt, &result.UpdatedAt, &result.PinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("harness: session %q not found; use sessions to choose one, or omit --session to create one", id)
	}
	if err == nil {
		result.Pinned = result.PinnedAt != ""
		var selected string
		modelErr := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "chat/model/"+id).Scan(&selected)
		if modelErr != nil && !errors.Is(modelErr, sql.ErrNoRows) {
			return Conversation{}, modelErr
		}
		result.ModelID = selected
	}
	return result, err
}

func (store *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	result := make([]Conversation, 0, 250)
	for _, query := range []string{
		`SELECT c.id,c.title,c.workspace,c.created_at,c.updated_at,coalesce(model.value,''),pin.value
		 FROM conversations c JOIN meta pin ON pin.key='chat/pin/'||c.id
		 LEFT JOIN meta model ON model.key='chat/model/'||c.id
		 WHERE pin.value<>'' ORDER BY pin.value DESC,c.updated_at DESC LIMIT 50`,
		`SELECT c.id,c.title,c.workspace,c.created_at,c.updated_at,coalesce(model.value,''),''
		 FROM conversations c LEFT JOIN meta pin ON pin.key='chat/pin/'||c.id
		 LEFT JOIN meta model ON model.key='chat/model/'||c.id
		 WHERE pin.key IS NULL OR pin.value='' ORDER BY c.updated_at DESC LIMIT 200`,
	} {
		rows, err := store.db.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item Conversation
			if err := rows.Scan(&item.ID, &item.Title, &item.Workspace, &item.CreatedAt, &item.UpdatedAt, &item.ModelID, &item.PinnedAt); err != nil {
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

// PinConversation stores the pin timestamp in the existing metadata table. A
// pin is presentation state, so it does not create a new runtime object.
func (store *Store) PinConversation(ctx context.Context, id string, pinned bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE id=?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("harness: conversation %q not found", id)
		}
		return err
	}
	key := "chat/pin/" + id
	if pinned {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key LIKE 'chat/pin/%' AND value<>''`).Scan(&count); err != nil {
			return err
		}
		var already bool
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(value,'') FROM meta WHERE key=?`, key).Scan(&existing); err == nil {
			already = existing != ""
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !already && count >= 50 {
			return ErrPinnedLimit
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, nowText()); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key=?`, key); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, run_id, body_json FROM messages WHERE conversation_id=? ORDER BY id`, conversationID)
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
	rows, err := store.db.QueryContext(ctx, `SELECT id,run_id,body_json FROM messages
		WHERE conversation_id=? ORDER BY id DESC LIMIT ?`, run.ConversationID, maxMessages+1)
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
	if requestKey == "" || prompt == "" {
		return Run{}, false, errors.New("harness: request key and prompt are required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	existing, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE conversation_id=? AND request_key=?`, conversationID, requestKey))
	if err == nil {
		if existing.Prompt != prompt {
			return Run{}, false, ErrRequestKey
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, err
	}
	var blocked int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE conversation_id=? AND status IN ('running','waiting_confirmation','stopped','unknown','limited')`, conversationID).Scan(&blocked); err != nil {
		return Run{}, false, err
	}
	if blocked > 0 {
		return Run{}, false, ErrActiveRun
	}
	result := Run{ID: newID(), ConversationID: conversationID, RequestKey: requestKey, Prompt: prompt, ConfigJSON: configJSON, Status: "running", CreatedAt: nowText(), UpdatedAt: nowText()}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(`+runColumns+`) VALUES(?,?,?,?,?,'running','',0,0,'',?,?,'')`,
		result.ID, conversationID, requestKey, prompt, configJSON, result.CreatedAt, result.UpdatedAt); err != nil {
		return Run{}, false, err
	}
	if maintenance {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, maintenanceMetaPrefix+result.ID, "compaction"); err != nil {
			return Run{}, false, err
		}
	} else {
		if err := insertMessage(ctx, tx, result, ModelMessage{Role: "user", Content: prompt}); err != nil {
			return Run{}, false, err
		}
	}
	return withModelFacts(result), true, tx.Commit()
}

func (store *Store) repeatedRun(ctx context.Context, conversationID, requestKey, prompt string) (Run, bool, error) {
	run, err := scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs
		WHERE conversation_id=? AND request_key=?`, conversationID, requestKey))
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
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, id))
}

func (store *Store) GetLatestRun(ctx context.Context, conversationID string) (Run, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE conversation_id=?
		AND NOT (status='completed' AND EXISTS(SELECT 1 FROM meta WHERE key=?||runs.id))
		ORDER BY rowid DESC LIMIT 1`, conversationID, maintenanceMetaPrefix))
}

func (store *Store) GetRunByRequest(ctx context.Context, conversationID, requestKey string) (Run, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(requestKey) == "" {
		return Run{}, sql.ErrNoRows
	}
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE conversation_id=? AND request_key=?`, conversationID, requestKey))
}

func (store *Store) FinishRun(ctx context.Context, id, status, reason string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE runs SET status=?, stop_reason=?, updated_at=? WHERE id=?`, status, reason, nowText(), id)
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
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, run.ID).Scan(&currentStatus); err != nil {
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
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages
			WHERE run_id=? AND json_extract(body_json,'$.Role')='assistant'
			AND json_extract(body_json,'$.Content')=?`, run.ID, output).Scan(&existing); err != nil {
			return err
		}
		if existing == 0 {
			if err := insertMessage(ctx, tx, run, ModelMessage{Role: "assistant", Content: output}); err != nil {
				return err
			}
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE runs SET status=?, stop_reason=?, output=?, updated_at=?
		WHERE id=? AND status='running'`, status, reason, output, nowText(), run.ID)
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
	if err := incrementStep(ctx, tx, run.ID, maxSteps); err != nil {
		return Attempt{}, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, run.ID).Scan(&status); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'workflow',?,'dispatched',?,'')`,
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
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, run.ID).Scan(&currentStatus); err != nil {
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
	updated, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished', response_json=?
		WHERE id=? AND run_id=? AND kind='workflow' AND status='dispatched'`, string(body), attempt.ID, run.ID)
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
	updated, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, stop_reason=?, output=?, updated_at=?
		WHERE id=? AND status='running'`, status, reason, output, nowText(), run.ID)
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
	response := ToolResult{Content: reason, IsError: true}
	if record != "" {
		_ = json.Unmarshal([]byte(record), &response)
	}
	updated, err := tx.ExecContext(ctx, `UPDATE attempts SET status='unknown', response_json=?
		WHERE id=? AND run_id=? AND kind='workflow' AND status='dispatched'`,
		string(mustJSON(response)), attempt.ID, run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return errors.Join(ErrModelBoundary, err)
	}
	updated, err = tx.ExecContext(ctx, `UPDATE runs SET status='unknown', stop_reason=?, updated_at=?
		WHERE id=? AND status IN ('running','stopped','limited','waiting_confirmation')`, reason, nowText(), run.ID)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, run.ID).Scan(&current); err != nil {
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
		if err := tx.QueryRowContext(ctx, `SELECT status FROM attempts WHERE id=? AND run_id=?`, attempt.ID, run.ID).Scan(&status); err != nil {
			return nil, err
		}
		kind := "completed"
		if result.IsError {
			kind = "failed"
		}
		result.Outcome = &ToolOutcome{Status: kind, Dispatched: status == "dispatched", CompletionKnown: true, Truncated: result.Truncated}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished', response_json=?
		WHERE id=? AND run_id=? AND kind='tool' AND call_id=? AND status IN ('pending','dispatched')`, string(raw), attempt.ID, run.ID, call.ID)
	if err != nil {
		return nil, err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		return nil, errors.Join(ErrModelBoundary, err)
	}
	if addMessage {
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: result.Content}); err != nil {
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
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='limited',stop_reason=?,updated_at=? WHERE id=?`, safeError(outputErr), nowText(), run.ID); err != nil {
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
			if _, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished', response_json=? WHERE id=?`, string(raw), attempt.ID); err != nil {
				return err
			}
		}
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: response.Content}); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='abandoned', stop_reason='abandoned by user; unknown attempt facts are retained', updated_at=? WHERE id=?`, nowText(), runID); err != nil {
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
	response.ToolCalls, response.Failure = nil, failure
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished', response_json=? WHERE id=? AND status='dispatched'`, string(raw), attempt.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='failed', stop_reason=?, updated_at=? WHERE id=?`, safeError(failure), nowText(), run.ID); err != nil {
		return err
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
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, "state.sqlite")
	if err := verifyRegularFile(path); err != nil {
		return err
	}
	return verifyStore(ctx, path)
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
	if modelRequest.Purpose == "assessment" {
		snapshot, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil || snapshot.Auto == nil {
			return Attempt{}, errors.Join(ErrAutoDisabled, err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts
			WHERE run_id=? AND kind='model' AND json_extract(request_json,'$.Purpose')='assessment'`, run.ID).Scan(&count); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'model','','dispatched',?,'')`, attempt.ID, run.ID, request); err != nil {
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
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished', response_json=? WHERE id=? AND status='dispatched'`, string(body), attempt.ID); err != nil {
		return err
	}
	var request ModelRequest
	if err := json.Unmarshal([]byte(attempt.RequestJSON), &request); err != nil {
		return err
	}
	if request.Purpose == "" {
		if err := insertMessage(ctx, tx, run, ModelMessage{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls, ProviderItems: response.ProviderItems}); err != nil {
			return err
		}
		for _, call := range response.ToolCalls {
			raw, err := json.Marshal(call)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'tool',?,'pending',?,'')`, newID(), run.ID, call.ID, string(raw)); err != nil {
				return err
			}
		}
		if len(response.ToolCalls) == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='completed', output=?, stop_reason='', updated_at=? WHERE id=?`, response.Content, nowText(), run.ID); err != nil {
				return err
			}
		}
	} else if len(response.ToolCalls) != 0 {
		return ErrModelCapability
	}
	var outputErr error
	if guard != nil {
		outputErr = guard.Record(ctx, tx, attempt, max(outputBytes, len(body)), true)
		if outputErr != nil && !isOutputLimit(outputErr) {
			return outputErr
		}
		if outputErr != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='limited',stop_reason=?,updated_at=? WHERE id=?`, safeError(outputErr), nowText(), run.ID); err != nil {
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
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE run_id=? AND kind='model'`, runID).Scan(&count)
	return count, err
}

func (store *Store) GetAttempt(ctx context.Context, attemptID string) (Attempt, error) {
	return scanAttempt(store.db.QueryRowContext(ctx, `SELECT id, run_id, kind, call_id, status,
		request_json, response_json, rowid FROM attempts WHERE id=?`, attemptID))
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
	rows, err := reader.QueryContext(ctx, `SELECT id, run_id, kind, call_id, status, request_json, response_json,rowid FROM attempts WHERE run_id=? ORDER BY rowid`, runID)
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
	return result, rows.Err()
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
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, run.ID).Scan(&status); err != nil {
		return Attempt{}, err
	}
	if status != "running" {
		return Attempt{}, errors.New("harness: tool call requires a running task")
	}
	attempt := Attempt{ID: newID(), RunID: run.ID, Kind: "tool", CallID: call.ID,
		Status: "pending", RequestJSON: string(raw)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'tool',?,'pending',?,'')`,
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
	if guard != nil {
		if err := guard.Reserve(ctx, tx, attempt); err != nil {
			return err
		}
	}
	if err := incrementStep(ctx, tx, attempt.RunID, maxSteps); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE attempts SET status='dispatched' WHERE id=? AND status='pending'`, attempt.ID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return errors.New("harness: tool attempt is not pending")
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
	var status, saved, request string
	if err := tx.QueryRowContext(ctx, `SELECT status,response_json,request_json FROM attempts
		WHERE id=? AND run_id=? AND kind='tool' AND call_id=?`,
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
	body := reason
	responseExpr := `CASE WHEN response_json='' THEN ? ELSE response_json END`
	if len(record) != 0 {
		body = record[0]
		responseExpr = `?`
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attempts SET status='unknown', response_json=`+responseExpr+` WHERE id=?`, body, attempt.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='unknown', stop_reason=?, updated_at=? WHERE id=?`, reason, nowText(), attempt.RunID); err != nil {
		return err
	}
	return tx.Commit()
}

// settleControlProposal closes the pending proposal Attempt without dispatching
// an external effect. Applied changes complete the proposing Run; a rejection
// leaves it running so the existing continuation path can produce the next
// model response.
func (store *Store) settleControlProposal(ctx context.Context, run Run, attempt Attempt, result ToolResult, completed bool, reason string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM attempts WHERE id=? AND run_id=? AND kind='tool'`, attempt.ID, run.ID).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return ErrControlAlreadyUsed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attempts SET status='finished',response_json=? WHERE id=? AND status='pending'`, string(raw), attempt.ID); err != nil {
		return err
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(attempt.RequestJSON), &call); err != nil {
		return err
	}
	if err := insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: result.Content}); err != nil {
		return err
	}
	nextStatus, stopReason := "running", ""
	if completed {
		nextStatus, stopReason = "completed", reason
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status=?,stop_reason=?,updated_at=? WHERE id=? AND status IN ('waiting_confirmation','stopped','running')`, nextStatus, stopReason, nowText(), run.ID); err != nil {
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
	rows, err := tx.QueryContext(ctx, `SELECT id, active_since FROM runs WHERE active_since!=''`)
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
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET elapsed_ms=elapsed_ms+?, active_since='' WHERE id=?`, item.elapsed, item.id); err != nil {
			return err
		}
	}
	statements := []string{
		`UPDATE attempts SET status='unknown', response_json=CASE WHEN response_json='' THEN 'process interrupted after dispatch marker' ELSE response_json END WHERE status='dispatched'`,
		`UPDATE runs SET status='unknown', stop_reason='unresolved call after restart' WHERE status NOT IN ('abandoned','unknown') AND id IN (SELECT run_id FROM attempts WHERE status='unknown')`,
		`UPDATE runs SET status='stopped', stop_reason='process interrupted; continue explicitly' WHERE status='running'`,
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(conversation_id, run_id, body_json) VALUES(?,?,?)`, run.ConversationID, run.ID, string(raw)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, nowText(), run.ConversationID)
	return err
}

func incrementStep(ctx context.Context, tx *sql.Tx, runID string, maxSteps int) error {
	result, err := tx.ExecContext(ctx, `UPDATE runs SET steps=steps+1 WHERE id=? AND steps<? AND status='running'`, runID, maxSteps)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return ErrStepLimit
	}
	return nil
}

func createSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		fmt.Sprintf(`PRAGMA application_id=%d`, applicationID),
		fmt.Sprintf(`PRAGMA user_version=%d`, storeSchemaVersion),
		`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		fmt.Sprintf(`INSERT INTO meta VALUES('format','%s')`, StoreFormat),
		`CREATE TABLE conversations(id TEXT PRIMARY KEY, title TEXT NOT NULL, workspace TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE runs(
			id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
			request_key TEXT NOT NULL, prompt TEXT NOT NULL, config_json TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('running','waiting_confirmation','completed','stopped','unknown','limited','failed','abandoned')),
			stop_reason TEXT NOT NULL, steps INTEGER NOT NULL DEFAULT 0 CHECK(steps>=0), elapsed_ms INTEGER NOT NULL DEFAULT 0 CHECK(elapsed_ms>=0), output TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, active_since TEXT NOT NULL DEFAULT '', UNIQUE(conversation_id,request_key))`,
		`CREATE UNIQUE INDEX one_active_run ON runs(conversation_id) WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`,
		`CREATE TABLE messages(
			id INTEGER PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
			run_id TEXT NOT NULL REFERENCES runs(id), body_json TEXT NOT NULL CHECK(json_valid(body_json)))`,
		`CREATE INDEX message_history ON messages(conversation_id,id)`,
		`CREATE TABLE attempts(
			id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), kind TEXT NOT NULL CHECK(kind IN ('model','tool','workflow')),
			call_id TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','dispatched','finished','unknown')),
			request_json TEXT NOT NULL CHECK(json_valid(request_json)), response_json TEXT NOT NULL)`,
		`CREATE TABLE memories(
			id TEXT PRIMARY KEY, scope TEXT NOT NULL CHECK(scope IN ('private','shared','knowledge')), owner TEXT NOT NULL,
			project TEXT NOT NULL, task_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>=1),
			state TEXT NOT NULL CHECK(state IN ('active','disabled','deleted')), kind TEXT NOT NULL,
			title TEXT NOT NULL, body TEXT NOT NULL, author TEXT NOT NULL, sources_json TEXT NOT NULL CHECK(json_valid(sources_json)),
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX memory_scope ON memories(scope,owner,project,task_id,state,updated_at)`,
		`CREATE TABLE memory_revisions(
			entry_id TEXT NOT NULL REFERENCES memories(id), revision INTEGER NOT NULL CHECK(revision>=1),
			state TEXT NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, author TEXT NOT NULL,
			sources_json TEXT NOT NULL CHECK(json_valid(sources_json)), reason TEXT NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY(entry_id,revision))`,
		`CREATE TABLE memory_operations(
			id TEXT PRIMARY KEY, request_hash TEXT NOT NULL, receipt_json TEXT NOT NULL CHECK(json_valid(receipt_json)), created_at TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE knowledge_fts USING fts5(entry_id UNINDEXED,body,tokenize='trigram')`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("harness: initialize schema: %w", err)
		}
	}
	return tx.Commit()
}

func verifyStore(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "ro"))
	if err != nil {
		return ErrStoreFormat
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var format, integrity string
	var app, version int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&app); err != nil || app != applicationID {
		return ErrStoreFormat
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != storeSchemaVersion {
		return ErrStoreFormat
	}
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='format'`).Scan(&format); err != nil || format != StoreFormat {
		return ErrStoreFormat
	}
	expected, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer expected.Close()
	expected.SetMaxOpenConns(1)
	if err := createSchema(ctx, expected); err != nil {
		return err
	}
	want, err := schemaIdentity(ctx, expected)
	if err != nil {
		return err
	}
	actual, err := schemaIdentity(ctx, db)
	if err != nil || actual != want {
		return ErrStoreFormat
	}
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("harness: store integrity check failed")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var crossConversation int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN runs r ON r.id=m.run_id WHERE m.conversation_id!=r.conversation_id`).Scan(&crossConversation)
	if err != nil || broken || crossConversation != 0 {
		return errors.New("harness: store relationships are invalid")
	}
	return nil
}

func schemaIdentity(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name LIMIT 65`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var entries [][4]string
	for rows.Next() {
		var entry [4]string
		if err := rows.Scan(&entry[0], &entry[1], &entry[2], &entry[3]); err != nil {
			return "", err
		}
		if len(entry[3]) > 64<<10 || len(entries) >= 64 {
			return "", ErrStoreFormat
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(entries)
	return contentHash(raw), err
}

func sqliteDSN(path, mode string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String() + "?mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
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
