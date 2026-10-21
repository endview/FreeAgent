package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

// runPreview shows the one consequential call a run is waiting on. The digest
// binds a later confirm to exactly this call and preview. A setting change
// proposed by the model also needs the confirmation code shown to the user.
func runPreview(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	runID := flags.String("run", "", "run waiting for confirmation")
	asJSON := flags.Bool("json", false, "write structured output")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" {
		return usageErr("preview: --run is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, attachErr := attachedOwner(ctx, *dataDir)
		if attachErr != nil {
			return fmt.Errorf("%w: %v", harness.ErrOwnerActive, attachErr)
		}
		confirmation, previewErr := client.Preview(ctx, *runID)
		if previewErr != nil {
			return previewErr
		}
		if *asJSON {
			return writeJSON(stdout, confirmation)
		}
		if confirmation.ControlOperationID != "" {
			interactive, err := interactiveOwner(ctx, *dataDir)
			if err != nil {
				return err
			}
			rendered, err := interactive.ControlConfirmation(ctx, confirmation.ControlOperationID, "")
			if err != nil {
				return err
			}
			return printControlPreview(stdout, stderr, confirmation, &rendered)
		}
		_, writeErr := fmt.Fprintf(stdout, "Run: %s\nAttempt: %s\nCall: %s (%s)\nDigest: %s\n\n%s\n",
			confirmation.RunID, confirmation.Attempt, confirmation.CallID, confirmation.Tool, confirmation.Digest, confirmation.Preview)
		_, _ = fmt.Fprintf(stderr, "Approve: freeagent confirm --run %s --digest %s --approve\nDecline: the same command without --approve\n",
			confirmation.RunID, confirmation.Digest)
		return writeErr
	}
	if err != nil {
		return err
	}
	defer closeAll()
	confirmation, err := harness.NewTaskService(service).Preview(ctx, *runID)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, confirmation)
	}
	if confirmation.ControlOperationID != "" {
		return printControlPreview(stdout, stderr, confirmation, nil)
	}
	_, err = fmt.Fprintf(stdout, "Run: %s\nAttempt: %s\nCall: %s (%s)\nDigest: %s\n\n%s\n",
		confirmation.RunID, confirmation.Attempt, confirmation.CallID, confirmation.Tool, confirmation.Digest, confirmation.Preview)
	_, _ = fmt.Fprintf(stderr, "Approve: freeagent confirm --run %s --digest %s --approve\nDecline: the same command without --approve\n",
		confirmation.RunID, confirmation.Digest)
	return err
}

// runConfirm decides the pending call named by its digest and continues the
// run. Without --approve the call is declined. Applying a setting change
// also takes the confirmation code: --code passes one an attached owner
// showed, otherwise confirm shows a code and asks for y or the code on stdin.
func runConfirm(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("confirm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	runID := flags.String("run", "", "run waiting for confirmation")
	digest := flags.String("digest", "", "digest printed by preview")
	approve := flags.Bool("approve", false, "approve the call; without it the call is declined")
	code := flags.String("code", "", "confirmation code of a setting change, as shown by preview")
	asJSON := flags.Bool("json", false, "write structured output")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" || strings.TrimSpace(*digest) == "" {
		return usageErr("confirm: --run and --digest are required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	changed := func(err error) error {
		_, _ = fmt.Fprintf(stderr, "The pending call no longer matches this digest. Preview it again: freeagent preview --run %s\n", *runID)
		return &exitError{code: exitFailure, err: err}
	}
	codeOnlyForControl := func(pending harness.Confirmation) error {
		if pending.ControlOperationID == "" && strings.TrimSpace(*code) != "" {
			return usageErr("confirm: --code applies only to a setting change")
		}
		return nil
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, attachErr := attachedOwner(ctx, *dataDir)
		if attachErr != nil {
			return fmt.Errorf("%w: %v", harness.ErrOwnerActive, attachErr)
		}
		current, previewErr := client.Preview(ctx, *runID)
		if previewErr != nil {
			return previewErr
		}
		if current.Digest != strings.TrimSpace(*digest) {
			return changed(harness.ErrApprovalChanged)
		}
		if err := codeOnlyForControl(current); err != nil {
			return err
		}
		if current.ControlOperationID != "" {
			run, _, _, err := client.Inspect(ctx, *runID)
			if err != nil {
				return err
			}
			result, decideErr := attachedControlDecision(ctx, *dataDir, run, current, strings.TrimSpace(*code), *approve,
				newTerminalInput(ctx, stdin).read, stderr)
			return finishControlDecision(stdout, stderr, result, decideErr, *asJSON, false)
		}
		if err := client.Confirm(ctx, current, *approve); err != nil {
			return err
		}
		result, waitErr := waitAttachedRun(ctx, client, *runID)
		if printErr := printResult(stdout, stderr, result, *asJSON, false); printErr != nil {
			return errors.Join(printErr, waitErr)
		}
		return runOutcome(stderr, result, waitErr)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	tasks := harness.NewTaskService(service)
	current, err := tasks.Preview(ctx, *runID)
	if err != nil {
		return err
	}
	if current.Digest != strings.TrimSpace(*digest) {
		return changed(harness.ErrApprovalChanged)
	}
	if err := codeOnlyForControl(current); err != nil {
		return err
	}
	streamed := false
	tasks.SetEvents(terminalEvents(stdout, stderr, *asJSON, &streamed))
	if current.ControlOperationID != "" {
		// Codes live only in the process that rendered them, so a code from a
		// closed owner cannot be checked here; this process shows a new one.
		if strings.TrimSpace(*code) != "" {
			return usageErr("confirm: that code belonged to a FreeAgent window that has closed; run confirm --approve without --code to get a new code")
		}
		run, _, _, err := tasks.Inspect(ctx, *runID)
		if err != nil {
			return err
		}
		result, decideErr := decideLocalControlProposal(ctx, tasks, run, current, "", *approve, newTerminalInput(ctx, stdin).read, stderr)
		return finishControlDecision(stdout, stderr, result, decideErr, *asJSON, streamed)
	}
	result, runErr := tasks.Confirm(ctx, *runID, current, *approve)
	if errors.Is(runErr, harness.ErrApprovalChanged) && result.Run.ID == "" {
		return changed(runErr)
	}
	if err := printResult(stdout, stderr, result, *asJSON, streamed); err != nil {
		return errors.Join(err, runErr)
	}
	return runOutcome(stderr, result, runErr)
}
