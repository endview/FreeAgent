package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

const attachPollInterval = 40 * time.Millisecond

func attachedOwner(ctx context.Context, dataDir string) (*handoff.Client, error) {
	info, secret, _, err := handoff.Read(dataDir)
	if err != nil {
		return nil, fmt.Errorf("owner discovery is unavailable: %w", err)
	}
	client, err := handoff.NewClient(info, secret)
	if err != nil {
		return nil, err
	}
	state, err := client.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("owner is not attachable: %w", err)
	}
	if state.Generation != info.Generation {
		return nil, errors.New("owner generation changed during attach; retry the command")
	}
	if state.Stopping {
		return nil, errors.New("owner is handing off and is not accepting new work")
	}
	return client, nil
}

func interactiveOwner(ctx context.Context, dataDir string) (*handoff.Client, error) {
	info, attachSecret, chatSecret, err := handoff.Read(dataDir)
	if err != nil {
		return nil, fmt.Errorf("owner discovery is unavailable: %w", err)
	}
	_ = attachSecret
	client, err := handoff.NewInteractiveClient(info, chatSecret)
	if err != nil {
		return nil, err
	}
	if _, err := client.ControlState(ctx); err != nil {
		return nil, fmt.Errorf("owner interactive control is unavailable: %w", err)
	}
	return client, nil
}

func runAttachedControl(ctx context.Context, client *handoff.Client, command, operationID, kind, arguments, conversationID, code string,
	stdin io.Reader, stdout, stderr io.Writer, asJSON bool) error {
	switch command {
	case "state":
		state, err := client.ControlState(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	case "list":
		operations, err := client.ControlOperations(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, operations)
	case "plan":
		operation, err := client.ControlPlan(ctx, kind, []byte(arguments), conversationID)
		if err != nil {
			return err
		}
		return promptAndApplyAttachedControl(ctx, client, operation, conversationID, code, stdin, stdout, stderr, asJSON)
	case "confirm", "revert-plan":
		operations, err := client.ControlOperations(ctx)
		if err != nil {
			return err
		}
		var operation harness.ControlOperation
		for _, item := range operations {
			if item.ID == operationID {
				operation = item
				break
			}
		}
		if operation.ID == "" {
			return harness.ErrControlOperationNotFound
		}
		if command == "revert-plan" {
			operation, err = client.ControlRevertPlan(ctx, operationID, conversationID)
			if err != nil {
				return err
			}
		}
		return promptAndApplyAttachedControl(ctx, client, operation, conversationID, code, stdin, stdout, stderr, asJSON)
	case "reject":
		result, err := client.ControlReject(ctx, operationID, conversationID)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	default:
		return usageError()
	}
}

func promptAndApplyAttachedControl(ctx context.Context, client *handoff.Client, operation harness.ControlOperation,
	conversationID, provided string, stdin io.Reader, stdout, stderr io.Writer, asJSON bool) error {
	result, err := applyAttachedControl(ctx, client, operation, conversationID, provided, stdin, stderr)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, result)
	}
	_, err = fmt.Fprintf(stdout, "%s: %s\n", result.Result, result.Operation.ID)
	return err
}

func applyAttachedControl(ctx context.Context, client *handoff.Client, operation harness.ControlOperation,
	conversationID, provided string, stdin io.Reader, stderr io.Writer) (harness.ControlResult, error) {
	if operation.AttemptID != "" {
		return harness.ControlResult{}, runBoundControlError(operation)
	}
	confirmation, err := client.ControlConfirmation(ctx, operation.ID, conversationID)
	if err != nil {
		return harness.ControlResult{}, err
	}
	approved := true
	if strings.TrimSpace(provided) == "" {
		provided, approved, err = readControlDecision(ctx, newTerminalInput(ctx, stdin).read, stderr, confirmation)
		if err != nil {
			return harness.ControlResult{}, err
		}
	}
	if !approved {
		if _, err := client.ControlReject(ctx, operation.ID, conversationID); err != nil {
			return harness.ControlResult{}, err
		}
		return harness.ControlResult{}, errControlDeclined
	}
	result, err := client.ControlConfirm(ctx, operation.ID, conversationID, provided)
	if err != nil {
		return harness.ControlResult{}, err
	}
	return result, nil
}

func rejectAttachedOptions(template string, modelID string, maxSteps, maxSeconds int) error {
	if template != "" {
		return errors.New("an attached owner must read templates through its own surface; --template is not available in this one-shot CLI attach")
	}
	if modelID != "" || maxSteps != 0 || maxSeconds != 0 {
		return errors.New("model and run-limit changes require the owner control surface; use the attached Chat UI or CLI owner")
	}
	return nil
}

func waitAttachedRun(ctx context.Context, client *handoff.Client, runID string) (harness.RunResult, error) {
	for {
		run, _, evidence, err := client.Inspect(ctx, runID)
		if err != nil {
			return harness.RunResult{}, err
		}
		switch run.Status {
		case "running":
			select {
			case <-ctx.Done():
				return harness.RunResult{Run: run, Evidence: evidence}, ctx.Err()
			case <-time.After(attachPollInterval):
			}
			continue
		default:
			return harness.RunResult{Run: run, Answer: run.Output, Evidence: evidence}, nil
		}
	}
}

// waitAttachedIdle waits until the owner has finished the request it
// accepted for this conversation, so the next Inspect sees its outcome
// instead of the state from before the decision.
func waitAttachedIdle(ctx context.Context, client *handoff.Client, conversationID string) error {
	for {
		state, err := client.State(ctx)
		if err != nil {
			return err
		}
		if state.ActiveChat != conversationID {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(attachPollInterval):
		}
	}
}

// attachedApproval asks about the call an attached Run waits on. read is
// shared by every prompt of one command so buffered answers are not lost.
func attachedApproval(ctx context.Context, client *handoff.Client, dataDir string, run harness.Run, read lineReader, stderr io.Writer) (harness.RunResult, error) {
	confirmation, err := client.Preview(ctx, run.ID)
	if err != nil {
		return harness.RunResult{Run: run}, err
	}
	if confirmation.ControlOperationID != "" {
		return attachedControlDecision(ctx, dataDir, run, confirmation, "", true, read, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "\nApprove %s?\n%s\n[y/N] ", confirmation.Tool, confirmation.Preview)
	answer, err := read(ctx)
	if err != nil {
		return harness.RunResult{Run: run}, err
	}
	if err := client.Confirm(ctx, confirmation, strings.EqualFold(strings.TrimSpace(answer), "y")); err != nil {
		return harness.RunResult{Run: run}, err
	}
	if err := waitAttachedIdle(ctx, client, run.ConversationID); err != nil {
		return harness.RunResult{Run: run}, err
	}
	return waitAttachedRun(ctx, client, run.ID)
}

// attachedControlDecision decides a Run-bound model control proposal through
// the owner's interactive surface. attach.secret alone cannot render the code
// or confirm, so this uses the owner's Chat key. Without a code an approval
// shows the owner-rendered code and asks the user, again after a miss.
func attachedControlDecision(ctx context.Context, dataDir string, run harness.Run, pending harness.Confirmation,
	code string, approve bool, read lineReader, stderr io.Writer) (harness.RunResult, error) {
	client, err := interactiveOwner(ctx, dataDir)
	if err != nil {
		return harness.RunResult{Run: run}, err
	}
	prompted := approve && strings.TrimSpace(code) == ""
	for try := 1; ; try++ {
		if prompted {
			confirmation, err := client.ControlConfirmation(ctx, pending.ControlOperationID, run.ConversationID)
			if err != nil {
				return harness.RunResult{Run: run}, err
			}
			code, approve, err = readControlDecision(ctx, read, stderr, confirmation)
			if err != nil {
				return harness.RunResult{Run: run}, err
			}
		}
		if err := client.ConfirmControl(ctx, pending, approve, code); err != nil {
			return harness.RunResult{Run: run}, err
		}
		if err := waitAttachedIdle(ctx, client, run.ConversationID); err != nil {
			return harness.RunResult{Run: run}, err
		}
		result, err := waitAttachedRun(ctx, client, run.ID)
		if err != nil || !approve || result.Run.Status != "waiting_confirmation" {
			return result, err
		}
		// The owner decides asynchronously; a proposal still waiting on the
		// same operation means the code was refused.
		current, previewErr := client.Preview(ctx, run.ID)
		if previewErr != nil || current.ControlOperationID != pending.ControlOperationID {
			return result, nil
		}
		if !prompted || try == controlPromptTries {
			return result, errControlNotAccepted
		}
		_, _ = fmt.Fprintln(stderr, "That code does not match; try again.")
	}
}

func runAttachedSubmit(ctx context.Context, client *handoff.Client, dataDir, conversationID, requestKey, prompt string,
	options harness.RunOptions, interactive bool, stdin io.Reader, stdout, stderr io.Writer, asJSON bool) error {
	if conversationID == "" {
		conversation, err := client.NewConversation(ctx, firstLine(prompt))
		if err != nil {
			return err
		}
		conversationID = conversation.ID
	}
	initial, err := client.Status(ctx, conversationID, requestKey)
	if err != nil {
		return err
	}
	if initial.Found {
		result, err := waitAttachedRun(ctx, client, initial.Run.ID)
		if err != nil {
			return err
		}
		result.Duplicate = true
		if result.Run.Status == "waiting_confirmation" && interactive {
			decided, err := attachedApproval(ctx, client, dataDir, result.Run, newTerminalInput(ctx, stdin).read, stderr)
			if err != nil && !stillPendingControl(stderr, err) {
				return err
			}
			if err == nil {
				decided.Duplicate, result = true, decided
			}
		}
		return runAttachedResult(stdout, stderr, result, asJSON)
	}
	if err := client.SubmitWithOptions(ctx, conversationID, requestKey, prompt, options); err != nil {
		return err
	}
	var read lineReader
	if interactive {
		read = newTerminalInput(ctx, stdin).read
	}
	var result harness.RunResult
	for {
		status, err := client.Status(ctx, conversationID, requestKey)
		if err != nil {
			return err
		}
		if !status.Found {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(attachPollInterval):
			}
			continue
		}
		result, err = waitAttachedRun(ctx, client, status.Run.ID)
		if err != nil {
			return err
		}
		if result.Run.Status != "waiting_confirmation" || !interactive {
			break
		}
		decided, err := attachedApproval(ctx, client, dataDir, result.Run, read, stderr)
		if stillPendingControl(stderr, err) {
			break
		}
		if err != nil {
			return err
		}
		result = decided
		if result.Run.Status != "waiting_confirmation" {
			break
		}
	}
	return runAttachedResult(stdout, stderr, result, asJSON)
}

func runAttachedContinue(ctx context.Context, client *handoff.Client, dataDir, conversationID, runID string,
	interactive bool, stdin io.Reader, stdout, stderr io.Writer, asJSON bool) error {
	if err := client.Continue(ctx, conversationID, runID); err != nil {
		return err
	}
	result, err := waitAttachedRun(ctx, client, runID)
	if err != nil {
		return err
	}
	if result.Run.Status == "waiting_confirmation" && interactive {
		decided, err := attachedApproval(ctx, client, dataDir, result.Run, newTerminalInput(ctx, stdin).read, stderr)
		if err != nil && !stillPendingControl(stderr, err) {
			return err
		}
		if err == nil {
			result = decided
		}
	}
	return runAttachedResult(stdout, stderr, result, asJSON)
}

func runAttachedResult(stdout, stderr io.Writer, result harness.RunResult, asJSON bool) error {
	if err := printResult(stdout, stderr, result, asJSON, false); err != nil {
		return err
	}
	return runOutcome(stderr, result, nil)
}
