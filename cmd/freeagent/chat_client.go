package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

// chatTasks keeps terminal presentation reusable with a local test core and
// with an attached owner. It contains no persistence or execution policy.
type chatTasks interface {
	Config() harness.Config
	NewConversation(context.Context, string) (harness.Conversation, error)
	Conversations(context.Context) ([]harness.Conversation, error)
	Messages(context.Context, string) ([]harness.Message, error)
	Latest(context.Context, string) (harness.Run, error)
	Inspect(context.Context, string) (harness.Run, []harness.Attempt, harness.Evidence, error)
	SelectedModel(context.Context, string) (string, error)
	SelectModel(context.Context, string, string) error
	Limits(context.Context, string) (harness.ExecutionLimits, error)
	IncreaseLimits(context.Context, string, harness.ExecutionLimits) error
	CollaborationSettings(context.Context, string) (harness.CollaborationSettings, error)
	SetCollaborationSettings(context.Context, string, harness.CollaborationSettings) error
	SetDefaultCollaborationSettings(context.Context, harness.CollaborationSettings) error
	AgentIdentity(context.Context, string) (string, error)
	SelectAgent(context.Context, string, string) error
	MemoryScopes(context.Context, string, string) ([]harness.MemoryScope, error)
	SelectedMemory(context.Context, string) (harness.MemorySelection, error)
	ApplyUserMemorySelection(context.Context, string, harness.MemorySelection) error
	ApplyUserMemoryConfig(context.Context, harness.MemoryConfig) error
	ListMemory(context.Context, string, harness.MemoryScope, int) ([]harness.MemoryEntry, error)
	ReadMemory(context.Context, string, harness.MemoryScope, string) (harness.MemoryEntry, error)
	SaveMemory(context.Context, string, harness.MemoryMutation) (harness.MemoryReceipt, error)
	MemorySources(context.Context, string) ([]harness.MemorySourceStatus, error)
	PreviewRunMemory(context.Context, string, string, harness.MemoryScope, string, string, string) (harness.MemoryMutation, error)
	PreviewMemoryCopy(context.Context, string, harness.MemoryReference, harness.MemoryScope, string, string, string) (harness.MemoryMutation, error)
	PreviewKnowledgeImport(context.Context, string, harness.MemoryScope, string, string, string) (harness.MemoryMutation, error)
	IsCollaboration(context.Context, string) (bool, error)
}

type remoteChat struct {
	*handoff.Client
	config       harness.Config
	data         string
	reader       *terminalInput
	stderr       io.Writer
	emit         harness.EventFunc
	cursor       uint64
	cancelMu     sync.Mutex
	binding      string
	requestError string
	activity     handoff.AttachEvents
}

func (remote *remoteChat) bind(binding string) {
	remote.cancelMu.Lock()
	remote.binding = binding
	remote.cancelMu.Unlock()
}

func (remote *remoteChat) interrupt(chat string) {
	remote.cancelMu.Lock()
	binding := remote.binding
	remote.cancelMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = remote.CancelRequest(ctx, chat, binding)
}

func chatValue[T any](remote *remoteChat, ctx context.Context, action, chat, run string, value any) (T, error) {
	var result T
	err := remote.ChatCommand(ctx, action, chat, run, value, &result)
	return result, err
}

func (remote *remoteChat) change(ctx context.Context, action, chat, run string, value any) error {
	return remote.ChatCommand(ctx, action, chat, run, value, nil)
}

func (remote *remoteChat) refresh(ctx context.Context) error {
	state, err := remote.State(ctx)
	if err != nil {
		return err
	}
	if state.Generation != remote.Generation {
		return errors.New("owner changed; reconnect explicitly without resubmitting input")
	}
	if state.Stopping {
		return errors.New("owner is stopping")
	}
	remote.config, err = chatValue[harness.Config](remote, ctx, "config", "", "", nil)
	return err
}

func (remote *remoteChat) Config() harness.Config { return remote.config }
func (remote *remoteChat) Conversations(ctx context.Context) ([]harness.Conversation, error) {
	state, err := remote.State(ctx)
	return state.Conversations, err
}
func (remote *remoteChat) Messages(ctx context.Context, chat string) ([]harness.Message, error) {
	return chatValue[[]harness.Message](remote, ctx, "messages", chat, "", nil)
}
func (remote *remoteChat) Latest(ctx context.Context, chat string) (harness.Run, error) {
	return chatValue[harness.Run](remote, ctx, "latest", chat, "", nil)
}
func (remote *remoteChat) SelectedModel(ctx context.Context, chat string) (string, error) {
	return chatValue[string](remote, ctx, "selected-model", chat, "", nil)
}
func (remote *remoteChat) SelectModel(ctx context.Context, chat, model string) error {
	return remote.change(ctx, "select-model", chat, "", model)
}
func (remote *remoteChat) Limits(ctx context.Context, run string) (harness.ExecutionLimits, error) {
	return chatValue[harness.ExecutionLimits](remote, ctx, "limits", "", run, nil)
}
func (remote *remoteChat) IncreaseLimits(ctx context.Context, run string, limits harness.ExecutionLimits) error {
	return remote.change(ctx, "increase-limits", "", run, limits)
}
func (remote *remoteChat) CollaborationSettings(ctx context.Context, chat string) (harness.CollaborationSettings, error) {
	return chatValue[harness.CollaborationSettings](remote, ctx, "collaboration-settings", chat, "", nil)
}
func (remote *remoteChat) SetCollaborationSettings(ctx context.Context, chat string, settings harness.CollaborationSettings) error {
	return remote.change(ctx, "set-collaboration", chat, "", settings)
}
func (remote *remoteChat) SetDefaultCollaborationSettings(ctx context.Context, settings harness.CollaborationSettings) error {
	return remote.change(ctx, "default-collaboration", "", "", settings)
}
func (remote *remoteChat) AgentIdentity(ctx context.Context, chat string) (string, error) {
	return chatValue[string](remote, ctx, "agent", chat, "", nil)
}
func (remote *remoteChat) SelectAgent(ctx context.Context, chat, agent string) error {
	return remote.change(ctx, "select-agent", chat, "", agent)
}
func (remote *remoteChat) MemoryScopes(ctx context.Context, chat, kind string) ([]harness.MemoryScope, error) {
	return chatValue[[]harness.MemoryScope](remote, ctx, "memory-scopes", chat, "", kind)
}
func (remote *remoteChat) SelectedMemory(ctx context.Context, chat string) (harness.MemorySelection, error) {
	return chatValue[harness.MemorySelection](remote, ctx, "memory-selected", chat, "", nil)
}
func (remote *remoteChat) ApplyUserMemorySelection(ctx context.Context, chat string, selection harness.MemorySelection) error {
	return remote.change(ctx, "memory-select", chat, "", selection)
}
func (remote *remoteChat) ApplyUserMemoryConfig(ctx context.Context, config harness.MemoryConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	return remote.change(ctx, "memory-config", "", "", config)
}
func (remote *remoteChat) ListMemory(ctx context.Context, chat string, scope harness.MemoryScope, limit int) ([]harness.MemoryEntry, error) {
	return chatValue[[]harness.MemoryEntry](remote, ctx, "memory-list", chat, "", struct {
		Scope harness.MemoryScope
		Limit int
	}{scope, limit})
}
func (remote *remoteChat) ReadMemory(ctx context.Context, chat string, scope harness.MemoryScope, id string) (harness.MemoryEntry, error) {
	return chatValue[harness.MemoryEntry](remote, ctx, "memory-read", chat, "", struct {
		Scope harness.MemoryScope
		ID    string
	}{scope, id})
}
func (remote *remoteChat) SaveMemory(ctx context.Context, chat string, mutation harness.MemoryMutation) (harness.MemoryReceipt, error) {
	return chatValue[harness.MemoryReceipt](remote, ctx, "memory-save", chat, "", mutation)
}
func (remote *remoteChat) MemorySources(ctx context.Context, run string) ([]harness.MemorySourceStatus, error) {
	return chatValue[[]harness.MemorySourceStatus](remote, ctx, "memory-sources", "", run, nil)
}
func (remote *remoteChat) PreviewRunMemory(ctx context.Context, chat, run string, scope harness.MemoryScope, kind, title, key string) (harness.MemoryMutation, error) {
	return chatValue[harness.MemoryMutation](remote, ctx, "memory-preview-run", chat, run, struct {
		Scope            harness.MemoryScope
		Kind, Title, Key string
	}{scope, kind, title, key})
}
func (remote *remoteChat) PreviewMemoryCopy(ctx context.Context, chat string, source harness.MemoryReference, scope harness.MemoryScope, kind, title, key string) (harness.MemoryMutation, error) {
	return chatValue[harness.MemoryMutation](remote, ctx, "memory-preview-copy", chat, "", struct {
		Source           harness.MemoryReference
		Scope            harness.MemoryScope
		Kind, Title, Key string
	}{source, scope, kind, title, key})
}
func (remote *remoteChat) PreviewKnowledgeImport(ctx context.Context, chat string, scope harness.MemoryScope, path, title, key string) (harness.MemoryMutation, error) {
	return chatValue[harness.MemoryMutation](remote, ctx, "memory-preview-import", chat, "", struct {
		Scope            harness.MemoryScope
		Path, Title, Key string
	}{scope, path, title, key})
}
func (remote *remoteChat) IsCollaboration(ctx context.Context, run string) (bool, error) {
	return chatValue[bool](remote, ctx, "is-collaboration", "", run, nil)
}
func (remote *remoteChat) Abandon(ctx context.Context, run string) error {
	return remote.change(ctx, "abandon", "", run, nil)
}
func (remote *remoteChat) ExpandReferences(ctx context.Context, prompt string, paths []string) (string, error) {
	return chatValue[string](remote, ctx, "expand-references", "", "", struct {
		Prompt string
		Paths  []string
	}{prompt, paths})
}

func (remote *remoteChat) events(ctx context.Context, chat string) error {
	events, err := remote.Events(ctx, remote.cursor)
	if err != nil {
		var failure *handoff.APIError
		if !errors.As(err, &failure) || failure.Code != "cursor_expired" {
			return err
		}
		state, readErr := remote.State(ctx)
		if readErr != nil {
			return readErr
		}
		if state.Generation != remote.Generation {
			return errors.New("owner changed; input was not resubmitted")
		}
		remote.cursor = state.LastSeq
		remote.activity = handoff.AttachEvents{Generation: state.Generation, ActiveChat: state.ActiveChat, ActiveBinding: state.ActiveBinding}
		_, _ = fmt.Fprintln(remote.stderr, "Event history expired; final output will be read from the saved result. No input was resubmitted.")
		remote.emit = nil
		return nil
	}
	remote.cursor = events.NextSeq
	remote.activity = events
	for _, event := range events.Events {
		if event.ConversationID == chat && event.Kind == "request_error" {
			remote.requestError = event.Message
		}
		if event.ConversationID == chat && remote.emit != nil {
			remote.emit(event)
		}
	}
	return nil
}

func (remote *remoteChat) wait(ctx context.Context, chat, key, runID string) (harness.RunResult, error) {
	ticker := time.NewTicker(attachPollInterval)
	defer ticker.Stop()
	for {
		if err := remote.events(ctx, chat); err != nil {
			return harness.RunResult{}, err
		}
		state := remote.activity
		if state.Generation != remote.Generation {
			return harness.RunResult{}, errors.New("owner changed; input was not resubmitted")
		}
		binding := key
		if runID != "" {
			binding = runID
		}
		if state.ActiveChat != chat || state.ActiveBinding != binding {
			if runID == "" {
				status, err := remote.Status(ctx, chat, key)
				if err != nil {
					return harness.RunResult{}, err
				}
				if !status.Found {
					if remote.requestError != "" {
						return harness.RunResult{}, errors.New(remote.requestError)
					}
					return harness.RunResult{}, errors.New("owner did not create the requested Run; inspect before retrying")
				}
				runID = status.Run.ID
			}
			run, _, evidence, err := remote.Inspect(ctx, runID)
			// Drain events after reading the settled Run to include its final delta.
			if err == nil {
				err = remote.events(ctx, chat)
			}
			return harness.RunResult{Run: run, Answer: run.Output, Evidence: evidence}, err
		}
		select {
		case <-ctx.Done():
			return harness.RunResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (remote *remoteChat) settle(ctx context.Context, result harness.RunResult, options harness.RunOptions) (harness.RunResult, error) {
	for result.Run.Status == "waiting_confirmation" {
		remote.bind(result.Run.ID)
		_, attempts, _, err := remote.Inspect(ctx, result.Run.ID)
		if err != nil {
			return result, err
		}
		var suggestion *harness.CollaborationSuggestion
		if options.Review != nil && result.Run.StopReason == "expert suggestion requires user review" {
			for _, attempt := range attempts {
				var request harness.ModelRequest
				var response harness.ModelResponse
				if attempt.Kind == "model" && attempt.Status == "finished" && json.Unmarshal([]byte(attempt.RequestJSON), &request) == nil && request.Purpose == "planning" && json.Unmarshal([]byte(attempt.ResponseJSON), &response) == nil {
					var value harness.CollaborationSuggestion
					if err := json.Unmarshal([]byte(response.Content), &value); err != nil {
						return result, err
					}
					suggestion = &value
				}
			}
		}
		if suggestion != nil {
			value, approved, err := options.Review(ctx, *suggestion)
			if err != nil {
				return result, err
			}
			if err := remote.ContinueReviewed(ctx, result.Run.ConversationID, result.Run.ID, &value, approved); err != nil {
				return result, err
			}
			result, err = remote.wait(ctx, result.Run.ConversationID, "", result.Run.ID)
			if err != nil {
				return result, err
			}
		} else {
			result, err = attachedApproval(ctx, remote.Client, remote.data, result.Run, remote.reader.read, remote.stderr)
			if err != nil {
				return result, err
			}
			if err := remote.events(ctx, result.Run.ConversationID); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
