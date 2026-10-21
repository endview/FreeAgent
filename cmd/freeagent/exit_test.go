package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

func TestExitCodesByStatus(t *testing.T) {
	for status, want := range map[string]int{
		"completed": 0, "failed": 1, "waiting_confirmation": 3,
		"stopped": 4, "abandoned": 4, "limited": 4, "unknown": 5, "running": 1,
	} {
		if got := statusExitCode(status); got != want {
			t.Errorf("statusExitCode(%q)=%d want %d", status, got, want)
		}
		var stderr bytes.Buffer
		result := harness.RunResult{Run: harness.Run{ID: "r1", Status: status}}
		err := runOutcome(&stderr, result, nil)
		if got := exitCode(err); got != want {
			t.Errorf("runOutcome(%q) exit=%d want %d err=%v", status, got, want, err)
		}
		if want == 0 && err != nil {
			t.Errorf("completed run returned %v", err)
		}
		if status == "running" && !strings.Contains(stderr.String(), "inspect --run r1") {
			t.Errorf("running result explained as %q", stderr.String())
		}
		if status == "waiting_confirmation" && !strings.Contains(stderr.String(), "preview --run r1") {
			t.Errorf("waiting result explained as %q", stderr.String())
		}
	}
	var stderr bytes.Buffer
	stopped := harness.RunResult{Run: harness.Run{ID: "r2", Status: "stopped"}}
	err := runOutcome(&stderr, stopped, fmt.Errorf("dispatch: %w", harness.ErrKeyNeedsReentry))
	if exitCode(err) != exitStopped || !strings.Contains(stderr.String(), "key edit <name> --key-prompt") {
		t.Fatalf("key re-entry exit=%d stderr=%q", exitCode(err), stderr.String())
	}
	if err := runOutcome(io.Discard, harness.RunResult{}, harness.ErrActiveRun); exitCode(err) != exitFailure {
		t.Fatalf("error without a run exit=%d", exitCode(err))
	}

	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Bool("known", false, "")
	if err := parseFlags(flags, []string{"--unknown"}); exitCode(err) != exitUsage {
		t.Fatalf("parse error exit=%d", exitCode(err))
	}
	if err := parseFlags(flags, []string{"-h"}); err == nil || exitCode(err) != 0 {
		t.Fatalf("help err=%v exit=%d", err, exitCode(err))
	}
	for err, want := range map[error]int{
		nil: 0, errors.New("boom"): 1, harness.ErrActiveRun: 1, harness.ErrKeysBusy: 1,
		harness.ErrOwnerActive: 1, harness.ErrApprovalChanged: 1,
		fmt.Errorf("x: %w", harness.ErrKeyNotFound): 2, harness.ErrKeyNameExists: 2,
		harness.ErrKeysFormat: 2, harness.ErrStoreFormat: 2, apppaths.ErrManifest: 2,
		usageErr("usage: x"): 2, &exitError{code: exitWaiting}: 3,
	} {
		if got := exitCode(err); got != want {
			t.Errorf("exitCode(%v)=%d want %d", err, got, want)
		}
	}

	var report bytes.Buffer
	reportError(&report, fmt.Errorf("open: %w", harness.ErrOwnerActive))
	reportError(&report, &exitError{code: exitWaiting})
	if report.String() != ownerActiveMessage+"\n" {
		t.Fatalf("report=%q", report.String())
	}
}
