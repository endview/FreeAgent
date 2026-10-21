package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

func runControl(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("usage: freeagent control <state|catalog|list|plan|confirm|reject|revert-plan|review> [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet("control "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "current Harness data directory")
	operationID := flags.String("operation", "", "control operation id")
	conversationID := flags.String("conversation", "", "conversation id for a proposal")
	kind := flags.String("kind", "", "typed control operation kind")
	argumentsText := flags.String("arguments", "{}", "typed operation arguments as JSON")
	code := flags.String("code", "", "short confirmation code")
	asJSON := flags.Bool("json", false, "write structured output")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("control: unexpected arguments")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	if command == "catalog" {
		return writeJSON(stdout, harness.ControlOperationCatalog())
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, attachErr := interactiveOwner(ctx, *dataDir)
		if attachErr != nil {
			return fmt.Errorf("%w: %v", harness.ErrOwnerActive, attachErr)
		}
		return runAttachedControl(ctx, client, command, *operationID, *kind, *argumentsText, *conversationID, *code, stdin, stdout, stderr, *asJSON)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	control, err := service.Control()
	if err != nil {
		return err
	}
	switch command {
	case "review":
		if err := control.ReviewExternalChange(ctx); err != nil {
			return err
		}
		state, err := control.ReadControlState(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	case "state":
		state, err := control.ReadControlState(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	case "list":
		operations, err := control.ListControlOperations(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, operations)
	case "plan":
		if strings.TrimSpace(*kind) == "" {
			return usageErr("control plan: --kind is required")
		}
		if !json.Valid([]byte(*argumentsText)) {
			return usageErr("control plan: --arguments must be valid JSON")
		}
		operation, err := control.PlanControlChange(ctx, harness.ControlPlanRequest{Kind: *kind, Arguments: json.RawMessage(*argumentsText),
			Authorization: harness.ControlUserDirect, ConversationID: *conversationID})
		if err != nil {
			return err
		}
		return promptAndApplyControl(ctx, control, operation, *conversationID, *code, stdin, stdout, stderr, *asJSON)
	case "confirm":
		if strings.TrimSpace(*operationID) == "" {
			return usageErr("control confirm: --operation is required")
		}
		operation, err := findControlOperation(ctx, control, *operationID)
		if err != nil {
			return err
		}
		return promptAndApplyControl(ctx, control, operation, *conversationID, *code, stdin, stdout, stderr, *asJSON)
	case "reject":
		if strings.TrimSpace(*operationID) == "" {
			return usageErr("control reject: --operation is required")
		}
		capability, err := control.IssueInteractiveCapability(ctx, *conversationID, "cli")
		if err != nil {
			return err
		}
		result, err := control.RejectControlChange(ctx, *operationID, harness.ControlAuthorization{Source: harness.ControlUserDirect,
			ConversationID: *conversationID, Principal: "cli", Capability: capability})
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	case "revert-plan":
		if strings.TrimSpace(*operationID) == "" {
			return usageErr("control revert-plan: --operation is required")
		}
		operation, err := control.PlanRevertControlChange(ctx, *operationID, harness.ControlUserDirect, *conversationID)
		if err != nil {
			return err
		}
		return promptAndApplyControl(ctx, control, operation, *conversationID, *code, stdin, stdout, stderr, *asJSON)
	default:
		return usageErr("usage: freeagent control <state|catalog|list|plan|confirm|reject|revert-plan|review> [flags]")
	}
}

func findControlOperation(ctx context.Context, control *harness.ControlService, id string) (harness.ControlOperation, error) {
	operations, err := control.ListControlOperations(ctx)
	if err != nil {
		return harness.ControlOperation{}, err
	}
	for _, operation := range operations {
		if operation.ID == id {
			return operation, nil
		}
	}
	return harness.ControlOperation{}, harness.ErrControlOperationNotFound
}

func promptAndApplyControl(ctx context.Context, control *harness.ControlService, operation harness.ControlOperation,
	conversationID, provided string, stdin io.Reader, stdout, stderr io.Writer, asJSON bool) error {
	if operation.AttemptID != "" {
		return runBoundControlError(operation)
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "cli")
	if err != nil {
		return err
	}
	confirmation, err := control.RenderConfirmation(ctx, operation.ID, conversationID, "cli")
	if err != nil {
		return err
	}
	approved := true
	if strings.TrimSpace(provided) == "" {
		provided, approved, err = readControlDecision(ctx, newTerminalInput(ctx, stdin).read, stderr, confirmation)
		if err != nil {
			return err
		}
	}
	auth := harness.ControlAuthorization{Source: harness.ControlUserDirect,
		ConversationID: conversationID, Principal: "cli", Code: provided, Capability: capability}
	if !approved {
		if _, err := control.RejectControlChange(ctx, operation.ID, auth); err != nil {
			return err
		}
		return errControlDeclined
	}
	result, err := control.ConfirmControlChange(ctx, operation.ID, auth)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, result)
	}
	_, err = fmt.Fprintf(stdout, "%s: %s\n", result.Result, result.Operation.ID)
	return err
}

// runBoundControlError points a direct confirm at the Run that owns the
// model proposal; applying it here would leave that Run waiting forever.
func runBoundControlError(operation harness.ControlOperation) error {
	return fmt.Errorf("%w: run freeagent preview --run <run> and freeagent confirm for the waiting Run (operation %s)",
		harness.ErrControlRunConfirmation, operation.ID)
}
