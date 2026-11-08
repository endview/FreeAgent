package main

import (
	"context"
	"flag"
	"github.com/endview/freeagent/internal/harness"
	"io"
)

func runDelivery(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		return usageErr("delivery <list|resume|abandon> --session ID [--group ID --digest HASH --confirm]")
	}
	flags := flag.NewFlagSet("delivery", flag.ContinueOnError)
	flags.SetOutput(errOut)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	chat := flags.String("session", "", "original conversation")
	group := flags.String("group", "", "exact original reply group")
	cursor := flags.String("cursor", "", "cursor of the last group from the previous list page")
	digest := flags.String("digest", "", "current preview digest")
	rule := flags.String("rule", "", "authorized scheduled generation rule")
	key := flags.String("key", "", "idempotency key for a no-send preview")
	confirm := flags.Bool("confirm", false, "reviewed: skip uncertain/failed parts, only continue unsent tail")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if *chat == "" || flags.NArg() != 0 {
		return usageErr("delivery requires --session")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return err
	}
	if args[0] == "wake-list" {
		items, err := client.WakeRecords(ctx, *chat)
		if err != nil {
			return err
		}
		return writeJSON(out, items)
	}
	if args[0] == "preview" {
		if !*confirm || *rule == "" || *key == "" {
			return usageErr("delivery preview --session ID --rule ID --key UNIQUE --confirm; consumes model allowance, never sends")
		}
		if err := client.ControlDelivery(ctx, *chat, harness.DeliveryCommand{Action: "preview", Rule: *rule, Key: *key}); err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"accepted": true, "sends": false})
	}
	if args[0] == "list" {
		items, err := client.ChannelDeliveries(ctx, *chat, *cursor)
		if err != nil {
			return err
		}
		return writeJSON(out, items)
	}
	if args[0] != "resume" && args[0] != "abandon" || !*confirm {
		return usageErr("inspect delivery list, then use resume or abandon with --group, --digest and --confirm; UNKNOWN is never resent")
	}
	if err := client.ControlDelivery(ctx, *chat, harness.DeliveryCommand{Group: *group, Digest: *digest, Action: args[0]}); err != nil {
		return err
	}
	return writeJSON(out, map[string]any{"ok": true, "action": args[0], "unknown_resent": false})
}
