package harness

import (
	"context"
	"errors"
	"strings"
)

// MemoryScopes lists only local metadata for explicit user management. It is
// not an Agent grant and never scans files or dispatches a model.
func (tasks *TaskService) MemoryScopes(ctx context.Context, chat, kind string) ([]MemoryScope, error) {
	authority, err := tasks.userMemory(ctx, chat)
	if err != nil {
		return nil, err
	}
	if kind != "private" && kind != "shared" && kind != "knowledge" {
		return nil, ErrMemoryDenied
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT DISTINCT scope,owner,project,task_id FROM memories
		WHERE scope=? AND (project='' OR project=?) AND state!='deleted' ORDER BY owner,project,task_id LIMIT 101`,
		kind, authority.project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := []MemoryScope{}
	for rows.Next() {
		var scope MemoryScope
		if err := rows.Scan(&scope.Kind, &scope.Owner, &scope.Project, &scope.TaskID); err != nil {
			return nil, err
		}
		if scope.validate() != nil {
			return nil, ErrStoreFormat
		}
		scopes = append(scopes, scope)
	}
	if len(scopes) > 100 {
		return nil, ErrContextLimit
	}
	return scopes, rows.Err()
}

// PreviewMemoryCopy freezes the exact selected revision. Saving the preview
// revalidates it; collaboration never implies private-memory export authority.
func (tasks *TaskService) PreviewMemoryCopy(ctx context.Context, chat string, source MemoryReference, destination MemoryScope, kind, title, key string) (MemoryMutation, error) {
	entry, err := tasks.ReadMemory(ctx, chat, source.Scope, source.EntryID)
	if err != nil {
		return MemoryMutation{}, err
	}
	if entry.State != "active" {
		return MemoryMutation{}, ErrMemoryDisabled
	}
	if kind == "" {
		kind = entry.Kind
	}
	if title == "" {
		title = entry.Title
	}
	if key == "" {
		key = newID()
	}
	mutation := MemoryMutation{RequestKey: key, Action: "create", Scope: destination, Kind: kind,
		Title: title, Body: entry.Body, Sources: []MemorySource{{Kind: "memory", EntryID: entry.ID,
			Revision: entry.Revision, Hash: contentHash([]byte(entry.Body))}}}
	return mutation, mutation.validate()
}

// PreviewRunMemory saves only a user-selected completed output, never a
// claimed check or incomplete/UNKNOWN model outcome.
func (tasks *TaskService) PreviewRunMemory(ctx context.Context, chat, runID string, destination MemoryScope, kind, title, key string) (MemoryMutation, error) {
	authority, err := tasks.userMemory(ctx, chat)
	if err != nil {
		return MemoryMutation{}, err
	}
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return MemoryMutation{}, err
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, run.ConversationID)
	if err != nil || conversation.Workspace != authority.project {
		return MemoryMutation{}, ErrMemoryDenied
	}
	if run.Status != "completed" || strings.TrimSpace(run.Output) == "" {
		return MemoryMutation{}, errors.New("harness: select a completed task with saved output")
	}
	if kind == "" {
		kind = "finding"
	}
	if key == "" {
		key = newID()
	}
	mutation := MemoryMutation{RequestKey: key, Action: "create", Scope: destination, Kind: kind,
		Title: title, Body: run.Output, Sources: []MemorySource{{Kind: "run", RunID: run.ID, Hash: contentHash([]byte(run.Output))}}}
	return mutation, mutation.validate()
}

// SetMemoryConfig requires an idle current environment. It does not dispatch
// anything or alter the authority/consumption of existing tasks.
func (tasks *TaskService) SetMemoryConfig(ctx context.Context, memory MemoryConfig) error {
	if err := memory.validate(); err != nil {
		return err
	}
	if ctx == nil || tasks == nil || tasks.core == nil {
		return ErrMemoryDenied
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if tasks.cancel != nil || !tasks.core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer tasks.core.Store.runMu.Unlock()
	var unfinished int
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM runs
		WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`).Scan(&unfinished); err != nil {
		return err
	}
	if unfinished != 0 {
		return ErrActiveRun
	}
	config := tasks.core.currentConfig()
	config.Memory = memory
	if err := WriteConfig(tasks.core.Store.DataDir(), config); err != nil {
		return err
	}
	tasks.core.setConfig(config)
	return nil
}
