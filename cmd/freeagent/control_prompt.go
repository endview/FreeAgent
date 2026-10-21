package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

var (
	// errControlDeclined reports a control change the user chose not to apply.
	errControlDeclined = errors.New("control change declined; nothing was changed")
	// errControlNoAnswer leaves the proposal pending when input ends first.
	errControlNoAnswer = errors.New("no answer was given; the control proposal is still pending")
	// errControlNotAccepted reports an owner-side refusal of the typed code.
	errControlNotAccepted = errors.New("control confirmation was not accepted; the proposal is still pending")
)

// controlPromptTries bounds how often one decision re-asks after a code
// that does not match. Every miss still counts against the owner's limit.
const controlPromptTries = 3

type lineReader func(context.Context) (string, error)

// formatControlCode groups the ten-character code for reading; the owner
// ignores the separator and letter case.
func formatControlCode(code string) string {
	value := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(value) > 5 {
		return value[:5] + "-" + value[5:]
	}
	return value
}

func validUntil(expiresAt string) string {
	if at, err := time.Parse(time.RFC3339Nano, expiresAt); err == nil {
		return " (valid until " + at.Local().Format("15:04") + ")"
	}
	return ""
}

// stillPendingControl shows why a decision left the setting change waiting
// and reports whether err was such an outcome. The caller then exits with
// the waiting status instead of a failure.
func stillPendingControl(stderr io.Writer, err error) bool {
	var message string
	switch {
	case errors.Is(err, errControlNoAnswer):
		message = "No answer was given; the setting change is still waiting."
	case errors.Is(err, errControlNotAccepted), errors.Is(err, harness.ErrControlWrongCode):
		message = "The confirmation code was not accepted; the setting change is still waiting."
	case errors.Is(err, harness.ErrControlExpired), errors.Is(err, harness.ErrControlConfirmationRequired):
		message = "The confirmation code is no longer valid; confirm again to get a new code."
	default:
		return false
	}
	_, _ = fmt.Fprintln(stderr, message)
	return true
}

// readControlDecision shows an owner-rendered control confirmation and
// returns the code to submit. "y" or the shown code applies; Enter or "n"
// declines. Other input is submitted as a typed code, so the owner counts a
// mismatch as one failed attempt.
func readControlDecision(ctx context.Context, read lineReader, stderr io.Writer, confirmation harness.ControlConfirmation) (string, bool, error) {
	_, _ = fmt.Fprintf(stderr, "\nControl proposal %s\n%s\nConfirmation code: %s%s\nApply? Type y or the code; press Enter to decline: ",
		confirmation.OperationID, confirmation.Preview, formatControlCode(confirmation.Code), validUntil(confirmation.ExpiresAt))
	line, err := read(ctx)
	if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
		_, _ = fmt.Fprintln(stderr)
		return "", false, errControlNoAnswer
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	answer := strings.TrimSpace(line)
	switch strings.ToLower(answer) {
	case "", "n", "no":
		return "", false, nil
	case "y", "yes":
		return confirmation.Code, true, nil
	}
	return answer, true, nil
}

// decideLocalControlProposal decides the model control proposal a Run is
// waiting on in this process. Without a code an approval asks the user and
// asks again, a bounded number of times, after a code that does not match.
func decideLocalControlProposal(ctx context.Context, tasks *harness.TaskService, run harness.Run, pending harness.Confirmation,
	code string, approve bool, read lineReader, stderr io.Writer) (harness.RunResult, error) {
	control, err := tasks.Control()
	if err != nil {
		return harness.RunResult{Run: run}, err
	}
	capability, err := control.IssueInteractiveCapability(ctx, run.ConversationID, "cli")
	if err != nil {
		return harness.RunResult{Run: run}, err
	}
	prompted := approve && strings.TrimSpace(code) == ""
	for try := 1; ; try++ {
		if prompted {
			confirmation, err := control.RenderConfirmation(ctx, pending.ControlOperationID, run.ConversationID, "cli")
			if err != nil {
				return harness.RunResult{Run: run}, err
			}
			code, approve, err = readControlDecision(ctx, read, stderr, confirmation)
			if err != nil {
				return harness.RunResult{Run: run}, err
			}
		}
		result, err := tasks.ConfirmControl(ctx, run.ID, pending, harness.ControlAuthorization{
			Source: harness.ControlModelProposed, ConversationID: run.ConversationID, Principal: "cli",
			Code: code, Capability: capability,
		}, approve)
		if !prompted || !approve || !errors.Is(err, harness.ErrControlWrongCode) || try == controlPromptTries {
			return result, err
		}
		_, _ = fmt.Fprintln(stderr, "That code does not match; try again.")
	}
}

// settleLocalControlProposals asks about each control proposal an
// interactive local Run stops on. Ordinary tool approvals are already asked
// inline by terminalApproval. A proposal left waiting ends the loop with the
// waiting result, so the command reports how to decide it later.
func settleLocalControlProposals(ctx context.Context, tasks *harness.TaskService, result harness.RunResult,
	read lineReader, stderr io.Writer) (harness.RunResult, error) {
	for result.Run.Status == "waiting_confirmation" {
		pending, err := tasks.Preview(ctx, result.Run.ID)
		if err != nil || pending.ControlOperationID == "" {
			return result, err
		}
		next, err := decideLocalControlProposal(ctx, tasks, result.Run, pending, "", true, read, stderr)
		if stillPendingControl(stderr, err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		result = next
	}
	return result, nil
}

// finishControlDecision prints the Run after a setting-change decision. A
// decision that left the change waiting is not a failure: the Run is still
// waiting and the command exits with that status and the next step.
func finishControlDecision(stdout, stderr io.Writer, result harness.RunResult, err error, asJSON, streamed bool) error {
	if err != nil && !stillPendingControl(stderr, err) {
		return err
	}
	if printErr := printResult(stdout, stderr, result, asJSON, streamed); printErr != nil {
		return printErr
	}
	return runOutcome(stderr, result, nil)
}

// printControlPreview shows a waiting setting change. An attached owner keeps
// a rendered code alive, so its code is shown for confirm --code. A process
// without an owner cannot keep one, so confirm --approve shows a code itself.
func printControlPreview(stdout, stderr io.Writer, confirmation harness.Confirmation, rendered *harness.ControlConfirmation) error {
	_, err := fmt.Fprintf(stdout, "Run: %s\nAttempt: %s\nSetting change: %s\nDigest: %s\n\n%s\n",
		confirmation.RunID, confirmation.Attempt, confirmation.ControlOperationID, confirmation.Digest, confirmation.Preview)
	apply := fmt.Sprintf("freeagent confirm --run %s --digest %s --approve", confirmation.RunID, confirmation.Digest)
	if rendered != nil {
		shown := formatControlCode(rendered.Code)
		_, _ = fmt.Fprintf(stderr, "Confirmation code: %s%s\nApply: %s --code %s\n", shown, validUntil(rendered.ExpiresAt), apply, shown)
	} else {
		_, _ = fmt.Fprintf(stderr, "Apply: %s (it shows a confirmation code and asks you to type y or the code)\n", apply)
	}
	_, _ = fmt.Fprintf(stderr, "Decline: freeagent confirm --run %s --digest %s\n", confirmation.RunID, confirmation.Digest)
	return err
}
