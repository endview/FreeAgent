package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/postgres"
)

func extensionStoragePath(data, id string) string {
	return filepath.Join(data, "extension-storage", id, "postgres.json")
}

type ExtensionStateScope struct {
	CoreID  string `json:"core_id"`
	SpaceID string `json:"space_id"`
	RoleID  string `json:"role_id"`
	ActorID string `json:"actor_id"`
}
type ExtensionStateChange struct {
	ID               string              `json:"id"`
	Scope            ExtensionStateScope `json:"scope"`
	Action           string              `json:"action"`
	ExpectedRevision int64               `json:"expected_revision"`
	Persona          json.RawMessage     `json:"persona,omitempty"`
	Spaces           []string            `json:"spaces,omitempty"`
	SourceID         string              `json:"source_id,omitempty"`
	Proposal         json.RawMessage     `json:"proposal,omitempty"`
	Decision         json.RawMessage     `json:"decision,omitempty"`
	Outcome          json.RawMessage     `json:"outcome,omitempty"`
	MemoryID         string              `json:"memory_id,omitempty"`
}
type extensionStatePreview struct {
	Revision int64           `json:"revision"`
	Before   json.RawMessage `json:"before"`
	After    json.RawMessage `json:"after"`
	Summary  string          `json:"summary"`
}
type extensionStateResult struct {
	Found       bool   `json:"found"`
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	Revision    int64  `json:"revision"`
}

func (service *ControlService) validateModelExtensionSettings(raw json.RawMessage) error {
	var args struct {
		ID       string         `json:"id"`
		Settings map[string]any `json:"settings"`
	}
	if json.Unmarshal(raw, &args) != nil || !validExtensionID(args.ID) || len(args.Settings) > 32 {
		return ErrPermissionDenied
	}
	definition, err := service.core.extensions.definition(args.ID)
	if err != nil {
		return err
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal(definition.Manifest.SettingsSchema, &schema) != nil {
		return ErrPermissionDenied
	}
	for key, value := range args.Settings {
		property, ok := schema.Properties[key]
		if !ok || !contains(definition.Manifest.ModelSettings, key) {
			return ErrPermissionDenied
		}
		switch value.(type) {
		case string, bool, float64:
		default:
			return ErrPermissionDenied
		}
		if property.Type != "string" && property.Type != "boolean" && property.Type != "integer" && property.Type != "number" {
			return ErrPermissionDenied
		}
	}
	resolved, err := resolveToolSchema(definition.Manifest.SettingsSchema)
	if err != nil {
		return err
	}
	return resolved.Validate(args.Settings)
}

func (tasks *TaskService) extensionStateRPC(ctx context.Context, id, method string, input any) (json.RawMessage, error) {
	if !validExtensionID(id) || tasks.core.extensions == nil {
		return nil, ErrToolDisabled
	}
	config := tasks.Config()
	if !config.Extensions.Enabled[id] {
		return nil, errors.New("harness: extension must be enabled")
	}
	host := tasks.core.extensions
	host.configure(config)
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	if !definition.Manifest.State {
		return nil, errors.New("harness: extension has no state capability")
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return nil, err
	}
	if !contains(client.definition.Provides, "state") {
		return nil, errors.New("harness: extension state capability not registered")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := client.rpc.Call(callCtx, method, input)
	if err != nil {
		return nil, err
	}
	if len(raw) > 256<<10 || !json.Valid(raw) {
		return nil, errors.New("harness: invalid extension state result")
	}
	return raw, nil
}

// ExtensionState is an explicit read. No process is started when an extension
// is disabled, and the host binds all identities before sending the request.
func (tasks *TaskService) ExtensionState(ctx context.Context, id, chatID, role, actor string, spaces ...string) (json.RawMessage, error) {
	space := ""
	if len(spaces) > 0 {
		space = spaces[0]
	}
	scope, err := tasks.extensionScope(ctx, chatID, ExtensionStateScope{RoleID: role, ActorID: actor, SpaceID: space})
	if err != nil {
		return nil, err
	}
	return tasks.extensionStateRPC(ctx, id, "state.read", map[string]any{"scope": scope})
}
func (tasks *TaskService) extensionScope(ctx context.Context, chatID string, scope ExtensionStateScope) (ExtensionStateScope, error) {
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='core/id'`).Scan(&scope.CoreID); err != nil {
		return scope, err
	}
	if chatID != "" {
		chat, err := tasks.core.Store.GetConversation(ctx, chatID)
		if err != nil {
			return scope, err
		}
		if scope.SpaceID != "" && scope.SpaceID != chat.SpaceID {
			return scope, ErrPermissionDenied
		}
		scope.SpaceID = chat.SpaceID
	} else if scope.SpaceID == "" {
		scope.SpaceID = LocalSpaceID
	}
	if _, err := tasks.core.Store.GetSpace(ctx, scope.SpaceID); err != nil {
		return scope, err
	}
	if scope.ActorID == "" {
		scope.ActorID = "owner"
	}
	if scope.RoleID!=""&&!validCapabilityID(scope.RoleID) || scope.ActorID != "owner" && !validPersonID(scope.ActorID) {
		return scope, ErrPermissionDenied
	}
	return scope, nil
}

func (service *ControlService) buildExtensionState(ctx context.Context, record *controlRecord) error {
	tasks := NewTaskService(service.core)
	if record.Operation.Kind == ControlInitExtensionStorage {
		var args struct {
			ID string `json:"id"`
		}
		if decodeControlJSON(record.Arguments, &args) != nil || !validExtensionID(args.ID) {
			return errors.New("harness: extension ID required")
		}
		definition, err := service.core.extensions.definitionFor(args.ID, tasks.Config())
		if err != nil {
			return err
		}
		if definition.Manifest.Storage != "postgresql" {
			return errors.New("harness: extension does not declare PostgreSQL storage")
		}
		if _, err = os.Lstat(extensionStoragePath(tasks.DataDir(), args.ID)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("harness: extension storage exists or is unavailable")
		}
		resource := "extensions.storage:" + args.ID
		record.Operation.ResourceIDs = []string{resource}
		record.Before = map[string]json.RawMessage{resource: mustJSON(false)}
		record.After = map[string]json.RawMessage{resource: mustJSON(true)}
		record.Operation.Preview = "Initialize a separate restricted PostgreSQL database for " + args.ID + ". Existing Core data is retained."
		record.Operation.RevertClass = "none"
		return nil
	}
	var args ExtensionStateChange
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if !validExtensionID(args.ID) || len(record.Arguments) > 64<<10 || args.Action == "" {
		return errors.New("harness: invalid extension state change")
	}
	scope, err := tasks.extensionScope(ctx, record.Operation.ConversationID, args.Scope)
	if err != nil {
		return err
	}
	args.Scope = scope
	if record.Operation.Authorization == ControlModelProposed {
		chat, err := tasks.core.Store.GetConversation(ctx, record.Operation.ConversationID)
		if err != nil {
			return err
		}
		// Models from external spaces cannot edit global role definitions or
		// target another person, even if a crafted argument claims an identity.
		if strings.HasSuffix(args.Action, "role") && chat.SpaceID != LocalSpaceID {
			return ErrPermissionDenied
		}
		if chat.SpaceID != LocalSpaceID {
			var runID string
			if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT id FROM fa.runs WHERE conversation_id=$1 AND status='running' ORDER BY created_at DESC,id DESC LIMIT 1`, chat.ID).Scan(&runID); err != nil {
				return ErrPermissionDenied
			}
			run, err := tasks.core.Store.GetRun(ctx, runID)
			if err != nil {
				return err
			}
			identity, err := tasks.core.extensionIdentity(ctx, run)
			if err != nil {
				return err
			}
			actor := identity.Inputs[len(identity.Inputs)-1].ActorID
			if args.Scope.ActorID != actor {
				return ErrPermissionDenied
			}
		}
	}
	raw, err := tasks.extensionStateRPC(ctx, args.ID, "state.preview", args)
	if err != nil {
		return err
	}
	var preview extensionStatePreview
	if json.Unmarshal(raw, &preview) != nil || preview.Revision != args.ExpectedRevision || len(preview.Before) > 128<<10 || len(preview.After) > 128<<10 {
		return errors.New("harness: invalid extension state preview")
	}
	resource := "extensions.state:" + args.ID + ":" + contentHash(mustJSON(args.Scope))
	record.Arguments = mustJSON(args)
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: preview.Before}
	record.After = map[string]json.RawMessage{resource: preview.After}
	record.Operation.Preview = truncateUTF8Bytes(preview.Summary, 2048) + "\n" + string(record.Arguments)
	record.Operation.RevertClass = "none"
	return nil
}
func (service *ControlService) initExtensionStorage(ctx context.Context, record *controlRecord) error {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(record.Arguments, &args) != nil || !validExtensionID(args.ID) {
		return ErrPermissionDenied
	}
	return postgres.ProvisionExtension(ctx, service.core.Store.DataDir(), extensionStoragePath(service.core.Store.DataDir(), args.ID))
}
func (service *ControlService) applyExtensionState(ctx context.Context, record *controlRecord) error {
	if record.ExtensionDispatched {
		return ErrUnknownResult
	}
	var args ExtensionStateChange
	if json.Unmarshal(record.Arguments, &args) != nil {
		return ErrControlStaleRevision
	}
	// Persist before sending. A crash at any later point can only query the
	// original result, never automatically issue another state.apply.
	record.ExtensionDispatched = true
	if err := service.appendLocked("extension_dispatched", *record); err != nil {
		return err
	}
	service.operations[record.Operation.ID] = *record
	raw, err := NewTaskService(service.core).extensionStateRPC(ctx, args.ID, "state.apply", map[string]any{"operation_id": record.Operation.ID, "digest": record.Operation.Digest, "change": args})
	if err != nil {
		if workflowOutcomeUnknown(err) {
			return errors.Join(ErrUnknownResult, err)
		}
		return err
	}
	var result extensionStateResult
	if json.Unmarshal(raw, &result) != nil || !result.Found || result.OperationID != record.Operation.ID || result.Digest != record.Operation.Digest {
		return ErrUnknownResult
	}
	record.ResultData = raw
	return nil
}

// ReconcileExtensionChange only reads the result of an already authorized
// original operation. It cannot approve, repeat or replace that operation.
func (service *ControlService) ReconcileExtensionChange(ctx context.Context, id string) (ControlResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[id]
	if !ok {
		return ControlResult{}, ErrControlOperationNotFound
	}
	if record.Operation.Kind != ControlChangeExtensionState || !record.ExtensionDispatched {
		return ControlResult{}, ErrPermissionDenied
	}
	if record.Operation.Status == ControlApplied {
		if err := service.settleReconciledExtension(ctx, record); err != nil {
			return ControlResult{Operation: record.Operation, Applied: true}, err
		}
		return ControlResult{Operation: record.Operation, Applied: true, Data: record.ResultData}, nil
	}
	var args ExtensionStateChange
	if json.Unmarshal(record.Arguments, &args) != nil {
		return ControlResult{}, ErrControlStaleRevision
	}
	raw, err := NewTaskService(service.core).extensionStateRPC(ctx, args.ID, "state.result", map[string]any{"operation_id": id, "digest": record.Operation.Digest})
	if err != nil {
		return ControlResult{Operation: record.Operation}, err
	}
	var result extensionStateResult
	if json.Unmarshal(raw, &result) != nil || !result.Found || result.OperationID != id || result.Digest != record.Operation.Digest {
		return ControlResult{Operation: record.Operation}, ErrUnknownResult
	}
	record.ResultData = raw
	record.Operation.Status = ControlApplied
	record.Operation.ErrorCode = ""
	record.Operation.Recovery = ""
	record.Operation.AppliedAt = nowText()
	record.Operation.NewRevisions = map[string]uint64{}
	for _, resource := range record.Operation.ResourceIDs {
		service.revisions[resource] = service.revisionLocked(resource) + 1
		record.Operation.NewRevisions[resource] = service.revisions[resource]
	}
	service.operations[id] = record
	if err = service.appendLocked("extension_reconciled", record); err != nil {
		return ControlResult{Operation: record.Operation, Applied: true}, err
	}
	if err = service.settleReconciledExtension(ctx, record); err != nil {
		return ControlResult{Operation: record.Operation, Applied: true}, err
	}
	return ControlResult{Operation: record.Operation, Applied: true, Data: raw, Result: "original_operation_verified"}, nil
}

// Keep the original uncertain Attempt. Later read-only ledger evidence resolves
// its effect through the existing verification receipt, and ends the proposal
// Run without replaying its model/tool group.
func (service *ControlService) settleReconciledExtension(ctx context.Context, record controlRecord) error {
	if record.Operation.AttemptID == "" {
		return nil
	}
	attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
	if err != nil {
		return err
	}
	if attempt.Status == "pending" {
		run, err := service.core.Store.GetRun(ctx, attempt.RunID)
		if err != nil {
			return err
		}
		return service.core.Store.settleControlProposal(ctx, run, attempt, ToolResult{Content: "Original extension control result verified; no replay was performed."}, true, "extension_control_verified")
	}
	if attempt.Status != "unknown" {
		return nil
	}
	tx, err := service.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockExecutionTx(ctx, tx, attempt.RunID); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, "attempt/verified/"+attempt.ID, string(mustJSON(map[string]any{"run_id": attempt.RunID, "attempt_id": attempt.ID, "outcome": "happened", "operation_id": record.Operation.ID, "evidence": "extension_transaction_ledger", "digest": record.Operation.Digest})))
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 1 {
		var call ToolCall
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
			return ErrStoreFormat
		}
		run, err := service.core.Store.GetRun(ctx, attempt.RunID)
		if err != nil {
			return err
		}
		if err = insertMessage(ctx, tx, run, ModelMessage{Role: "tool", ToolCallID: call.ID, Content: "Original extension control remains recorded UNKNOWN. Its original transaction ledger now proves the confirmed change was applied. No model, tool or state operation was replayed."}); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.runs SET status='completed',stop_reason='extension_control_verified',updated_at=$1 WHERE id=$2 AND status IN ('unknown','waiting_confirmation','stopped')`, nowText(), attempt.RunID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
