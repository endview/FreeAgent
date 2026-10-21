package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

// Process exit codes. Scripts rely on them, so they never change meaning.
const (
	exitFailure = 1
	exitUsage   = 2
	exitWaiting = 3
	exitStopped = 4
	exitUnknown = 5
)

const ownerActiveMessage = "data directory is in use by another FreeAgent process (the desktop app may be open)"

// exitError carries a process exit code. A nil err means everything the user
// needs is already on stdout or stderr, so main prints nothing more.
type exitError struct {
	code int
	err  error
}

func (failure *exitError) Error() string {
	if failure.err == nil {
		return fmt.Sprintf("exit status %d", failure.code)
	}
	return failure.err.Error()
}

func (failure *exitError) Unwrap() error { return failure.err }

// usageErr reports a missing or invalid argument with exit code 2.
func usageErr(message string) error {
	return &exitError{code: exitUsage, err: errors.New(message)}
}

func usagef(format string, args ...any) error {
	return &exitError{code: exitUsage, err: fmt.Errorf(format, args...)}
}

// parseFlags parses one command's flags. The flag package already printed the
// problem and the usage, so only the exit code travels on.
func parseFlags(flags *flag.FlagSet, args []string) error {
	err := flags.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		return &exitError{code: 0}
	default:
		return &exitError{code: exitUsage}
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var failure *exitError
	switch {
	case errors.As(err, &failure):
		return failure.code
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, harness.ErrKeyNotFound), errors.Is(err, harness.ErrKeyNameExists),
		errors.Is(err, harness.ErrKeysFormat), errors.Is(err, harness.ErrStoreFormat),
		errors.Is(err, apppaths.ErrManifest):
		return exitUsage
	case errors.Is(err, harness.ErrKeyNeedsReentry):
		return exitStopped
	}
	return exitFailure
}

// reportError writes what main shows before exiting. An owner lock held by
// another process always gets the same message.
func reportError(stderr io.Writer, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, harness.ErrOwnerActive) {
		_, _ = fmt.Fprintln(stderr, ownerActiveMessage)
		return
	}
	var failure *exitError
	if errors.As(err, &failure) && failure.err == nil {
		return
	}
	_, _ = fmt.Fprintln(stderr, err)
}

// statusExitCode maps the final status of a run. "running" in a final result
// means the run did not settle, which is a failure for the caller.
func statusExitCode(status string) int {
	switch status {
	case "completed":
		return 0
	case "waiting_confirmation":
		return exitWaiting
	case "stopped", "abandoned", "limited":
		return exitStopped
	case "unknown":
		return exitUnknown
	}
	return exitFailure
}

// runOutcome turns a printed run result into the command's error. The result
// itself is already on stdout; stderr only gets the next step.
func runOutcome(stderr io.Writer, result harness.RunResult, runErr error) error {
	if errors.Is(runErr, harness.ErrKeyNeedsReentry) {
		_, _ = fmt.Fprintln(stderr, "Enter the key again: freeagent key edit <name> --key-prompt")
	}
	status, id := result.Run.Status, result.Run.ID
	code := statusExitCode(status)
	if status == "" || code == 0 {
		return runErr
	}
	switch status {
	case "running":
		_, _ = fmt.Fprintf(stderr, "Run %s has not settled; check it with: freeagent inspect --run %s\n", id, id)
	case "waiting_confirmation":
		if result.Run.StopReason == harness.StopReasonControlProposal {
			_, _ = fmt.Fprintf(stderr, "Setting change waiting: freeagent preview --run %s, then freeagent confirm --run %s --digest <digest> --approve to see and enter the confirmation code; without --approve it is declined\n", id, id)
			break
		}
		_, _ = fmt.Fprintf(stderr, "Confirmation required: freeagent preview --run %s, then freeagent confirm --run %s --digest <digest> [--approve]\n", id, id)
	}
	return &exitError{code: code}
}
