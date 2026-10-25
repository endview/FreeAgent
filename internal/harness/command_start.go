package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
)

// StartCommand records the user's explicit tool request without fabricating a
// Provider response. Normal tool Preview/Confirm and Run budgets still apply.
func (tasks *TaskService) StartCommand(ctx context.Context, chat, key string, arguments json.RawMessage) (RunResult, error) {
	var args commandArguments
	if err := decodeArguments(arguments, &args); err != nil {
		return RunResult{}, err
	}
	prompt := "Explicit command request: " + string(mustJSON(args))
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		if !core.Store.runMu.TryLock() {
			return RunResult{}, ErrActiveRun
		}
		defer core.Store.runMu.Unlock()
		if err := core.validate(ctx); err != nil {
			return RunResult{}, err
		}
		conversation, err := core.Store.GetConversation(ctx, chat)
		if err != nil {
			return RunResult{}, err
		}
		if filepath.Clean(conversation.Workspace) != filepath.Clean(core.Config.Workspace) {
			return RunResult{}, errors.New("harness: command conversation workspace mismatch")
		}
		if repeated, found, err := core.Store.repeatedRun(ctx, chat, key, prompt); err != nil {
			return RunResult{}, err
		} else if found {
			return RunResult{Run: repeated, Answer: repeated.Output, Duplicate: true}, nil
		}
		if err := core.Store.checkUncontrolled(ctx, chat); err != nil {
			return RunResult{}, err
		}
		if err := core.resolvePermission(ctx, chat, "", false); err != nil {
			return RunResult{}, err
		}
		selected, err := core.Store.selectedCapabilities(ctx, chat, core.Config)
		if err != nil {
			return RunResult{}, err
		}
		core.capabilities = &selected
		model, err := tasks.SelectedModel(ctx, chat)
		if err != nil {
			return RunResult{}, err
		}
		if model == "auto" {
			model = core.Config.DefaultModel
		}
		core, err = core.forSelection(model)
		if err != nil {
			return RunResult{}, err
		}
		core.bindCurrentToolView()
		snapshot, err := core.snapshot()
		if err != nil {
			return RunResult{}, err
		}
		call := ToolCall{ID: newID(), Name: "command_run", Arguments: mustJSON(args)}
		run, _, err := core.Store.createRunWithTool(ctx, chat, key, prompt, snapshot, false, &call)
		if err != nil {
			return RunResult{}, err
		}
		return core.process(ctx, run)
	})
}
