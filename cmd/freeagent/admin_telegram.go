package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

func runAdminTelegram(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("admin telegram <status|bind|pause|revoke> [--bot ID --user ID --revision N]")
	}
	action := args[0]
	flags := flag.NewFlagSet("admin telegram "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	bot := flags.String("bot", "", "numeric Bot ID")
	user := flags.String("user", "", "the verified private Telegram user ID")
	revision := flags.Int64("revision", -1, "binding revision from status")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || action != "status" && action != "bind" && action != "pause" && action != "revoke" {
		return usageErr("admin telegram: unknown action")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return err
	}
	if action != "status" {
		if *bot == "" || *revision < 0 || action == "bind" && *user == "" || action != "bind" && *user != "" {
			return usageErr("admin telegram: supply --bot and --revision from status; bind also requires --user")
		}
		_, _ = fmt.Fprintf(stderr, "Telegram administrator binding: %s for Bot %q, user %q, revision %d. Apply? [y/N] ", action, *bot, *user, *revision)
		answer, err := newTerminalInput(ctx, stdin).read(ctx)
		if err != nil && strings.TrimSpace(answer) == "" || !strings.EqualFold(strings.TrimSpace(answer), "y") {
			return errControlDeclined
		}
		if err := client.SaveTelegramAdminBinding(ctx, *bot, action, *user, *revision); err != nil {
			return err
		}
	}
	result, err := client.TelegramAdminBindings(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}
