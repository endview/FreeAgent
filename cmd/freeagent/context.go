package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/endview/freeagent/internal/harness"
)

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("context <inspect|compact|refresh|clear> --session <id> [--summary-bytes <n>] [--json]")
	}
	action := args[0]
	if action != "inspect" && action != "compact" && action != "refresh" && action != "clear" {
		return usageErr("unknown context action")
	}
	flags := flag.NewFlagSet("context", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "harness data directory")
	chat := flags.String("session", "", "conversation id")
	limit := flags.Int("summary-bytes", 0, "per-operation summary byte bound; does not enable automatic compaction")
	asJSON := flags.Bool("json", false, "print structured context state")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *chat == "" || *limit < 0 || *limit > 16384 {
		return usageErr("context requires --session and summary bytes in 1..16384 (or omit)")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	command := harness.ContextCommand{Action: action, SummaryBytes: *limit}
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{})
	var result harness.ContextResult
	if errors.Is(err, harness.ErrOwnerActive) {
		client, attachErr := interactiveOwner(ctx, *data)
		if attachErr != nil {
			return attachErr
		}
		if action == "inspect" {
			result.State, err = client.ContextState(ctx, *chat)
		} else {
			result, err = client.ManageContext(ctx, *chat, command)
		}
	} else if err == nil {
		defer closeAll()
		result, err = tasks.ManageContext(ctx, *chat, command)
	}
	if err != nil {
		return err
	}
	return printContextResult(stdout, result, *asJSON)
}

func printContextResult(writer io.Writer, result harness.ContextResult, asJSON bool) error {
	if asJSON {
		return writeJSON(writer, result)
	}
	_, err := fmt.Fprintf(writer, "Context: %s; changed=%t; model_calls=%d\n", result.State.Status, result.Changed, result.ModelCalls)
	if err != nil {
		return err
	}
	if result.BeforeBytes > 0 && result.AfterBytes > 0 {
		_, err = fmt.Fprintf(writer, "Covered context prefix: %d -> %d bytes.\n", result.BeforeBytes, result.AfterBytes)
		if err != nil {
			return err
		}
	}
	if result.State.Summary != nil {
		summary := result.State.Summary
		_, err = fmt.Fprintf(writer, "Summary revision %d: %d source messages; %d/%d UTF-8 bytes. Original history retained.\n", summary.Revision, summary.SourceCount, len(summary.Text), summary.ByteLimit)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(writer, "Covered through %d; source SHA-256 %s\n", summary.CoveredThrough, summary.SourceDigest)
		if err != nil {
			return err
		}
	}
	if result.State.Blocked != "" {
		_, err = fmt.Fprintln(writer, result.State.Blocked)
	}
	return err
}
