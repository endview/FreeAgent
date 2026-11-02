package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/endview/freeagent/internal/handoff"
)

func runOwner(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return usageErr("owner <status|url|stop> [--data-dir DIR]")
	}
	action := args[0]
	flags := flag.NewFlagSet("owner "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || (action != "status" && action != "url" && action != "stop") {
		return usageErr("owner <status|url|stop> [--data-dir DIR]")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	info, _, _, err := handoff.Read(*data)
	if action == "status" && errors.Is(err, os.ErrNotExist) {
		return writeJSON(stdout, map[string]any{"running": false})
	}
	if err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return err
	}
	switch action {
	case "status":
		state, err := client.State(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"running": true, "origin": info.Origin, "generation": info.Generation, "pid": info.PID, "mode": info.OwnerKind, "stopping": state.Stopping, "active_chats": state.ActiveChats, "active_commands": state.ActiveCommands})
	case "url":
		_, err = fmt.Fprintln(stdout, client.Origin+"/#key="+client.Secret)
		return err
	default:
		if err := client.Handoff(ctx, true); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"stop_requested": true, "generation": info.Generation, "effects_undone": false})
	}
}
