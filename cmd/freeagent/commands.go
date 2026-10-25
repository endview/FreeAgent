package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

type commandClient interface {
	Commands(context.Context) ([]harness.CommandRecord, error)
	PollCommand(context.Context, string, int64, int) (harness.CommandRecord, error)
	StopCommand(context.Context, string) (harness.CommandRecord, error)
	StartCommand(context.Context, string, string, json.RawMessage) (harness.RunResult, error)
}

func runCommands(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("commands <list|poll|stop|start> [--id <attempt>] [--session <id> --arguments <JSON>] [--json]")
	}
	flags := flag.NewFlagSet("commands", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "owner data directory")
	id := flags.String("id", "", "original command Attempt id")
	chat := flags.String("session", "", "conversation id")
	arguments := flags.String("arguments", "", "exact command_run arguments JSON")
	key := flags.String("request", fmt.Sprintf("command-%d", time.Now().UnixNano()), "idempotency key")
	cursor := flags.Int64("cursor", 0, "output byte cursor")
	asJSON := flags.Bool("json", false, "structured result")
	interactive := flags.Bool("interactive", false, "prompt for pending tool approval")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("unexpected commands arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		if args[0] != "list" && args[0] != "poll" {
			return fmt.Errorf("commands require a running GUI, web or chat owner: %w", err)
		}
		tasks, closeAll, openErr := harness.OpenTasks(ctx, *data, harness.Adapters{})
		if openErr != nil {
			return errors.Join(err, openErr)
		}
		defer closeAll()
		return executeCommandClient(ctx, tasks, args[0], *id, *chat, *key, *arguments, *cursor, stdout, *asJSON)
	}
	if args[0] == "start" {
		if *chat == "" || !json.Valid([]byte(*arguments)) {
			return usageErr("start requires --session and --arguments JSON")
		}
		result, err := client.StartCommand(ctx, *chat, *key, json.RawMessage(*arguments))
		if err != nil {
			return err
		}
		if result.Run.Status == "waiting_confirmation" && *interactive {
			pending, err := client.Preview(ctx, result.Run.ID)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stderr, "%s\nApprove command? [y/N] ", pending.Preview)
			line, err := newTerminalInput(ctx, stdin).read(ctx)
			if err != nil {
				return err
			}
			if err = client.Confirm(ctx, pending, strings.EqualFold(strings.TrimSpace(line), "y")); err != nil {
				return err
			}
			result, err = waitAttachedRun(ctx, client, result.Run.ID)
			if err != nil {
				return err
			}
		}
		if *asJSON {
			return writeJSON(stdout, result)
		}
		_ = printChatResult(stdout, stderr, result, false)
		return runOutcome(stderr, result, nil)
	}
	return executeCommandClient(ctx, client, args[0], *id, *chat, *key, *arguments, *cursor, stdout, *asJSON)
}

func executeCommandClient(ctx context.Context, client commandClient, action, id, chat, key, arguments string, cursor int64, out io.Writer, asJSON bool) error {
	if action == "list" {
		records, err := client.Commands(ctx)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, records)
		}
		for _, r := range records {
			_, _ = fmt.Fprintf(out, "%s  %s  %s  %s\n", r.ID, r.State, r.ExpiresAt, r.Receipt.Command)
		}
		return nil
	}
	if action == "start" {
		if chat == "" || !json.Valid([]byte(arguments)) {
			return usageErr("start requires a conversation and JSON arguments")
		}
		result, err := client.StartCommand(ctx, chat, key, json.RawMessage(arguments))
		if err != nil {
			return err
		}
		return writeJSON(out, result)
	}
	if id == "" {
		return usageErr("poll/stop requires --id")
	}
	var r harness.CommandRecord
	var err error
	switch action {
	case "poll":
		r, err = client.PollCommand(ctx, id, cursor, 16<<10)
	case "stop":
		r, err = client.StopCommand(ctx, id)
	default:
		return usageErr("unknown commands action")
	}
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, r)
	}
	_, _ = fmt.Fprintf(out, "%s  %s; cleanup=%s; cursor=%d; gap=%t\n", r.ID, r.State, r.Receipt.Cleanup, r.NextCursor, r.OutputGap)
	for _, chunk := range r.Output {
		_, _ = fmt.Fprint(out, chunk.Text)
	}
	return nil
}
