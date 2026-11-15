package main

import (
	"context"
	"errors"
	"flag"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
	"io"
)

func runUnresolved(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("unresolved <list|ignore|confirm-applied|confirm-not-applied> --session ID [--item N --revision HASH --confirm]")
	}
	action := args[0]
	flags := flag.NewFlagSet("unresolved", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "data directory")
	chat := flags.String("session", "", "conversation")
	number := flags.Int64("item", 0, "stable item number")
	after := flags.Int64("after", 0, "list after item number")
	revision := flags.String("revision", "", "reviewed record revision")
	confirm := flags.Bool("confirm", false, "confirm this exact reviewed user decision")
	note := flags.String("note", "", "bounded user note")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if *chat == "" || flags.NArg() != 0 {
		return usageErr("--session is required")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{})
	var client *handoff.Client
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err = interactiveOwner(ctx, *data)
	} else if err == nil {
		defer closeAll()
	}
	if err != nil {
		return err
	}
	var items []harness.UnresolvedItem
	if action != "list" && *number > 0 {
		*after = *number - 1
	}
	if client != nil {
		items, err = client.Unresolved(ctx, *chat, *after)
	} else {
		items, err = tasks.Unresolved(ctx, *chat, *after)
	}
	if err != nil {
		return err
	}
	if action == "list" {
		return writeJSON(stdout, items)
	}
	actions := map[string]string{"ignore": "ignored", "confirm-applied": "user_confirmed_applied", "confirm-not-applied": "user_confirmed_not_applied"}
	resolution, ok := actions[action]
	if !ok || !*confirm || *revision == "" || *number < 1 {
		return usageErr("review with list, then pass --item, --revision and --confirm")
	}
	for _, item := range items {
		if item.Number == *number {
			if client != nil {
				err = client.ResolveUnresolved(ctx, *chat, item.ID, *revision, resolution, *note)
			} else {
				err = tasks.ResolveUnresolved(ctx, *chat, item.ID, *revision, resolution, *note, "interactive_owner")
			}
			if err != nil {
				return err
			}
			return writeJSON(stdout, map[string]any{"saved": true, "item": *number, "action": resolution, "replayed": false})
		}
	}
	return usageErr("item is unavailable on this page; use --after for later items")
}
