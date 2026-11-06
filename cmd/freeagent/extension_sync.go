package main

import (
	"context"
	"errors"
	"flag"
	"github.com/endview/freeagent/internal/harness"
	"io"
)

func runExtensionSync(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Core data directory")
	id := flags.String("id", "", "extension ID")
	chat := flags.String("conversation", "", "optional conversation")
	role := flags.String("role", "", "role selected in the original Runs")
	cursor := flags.String("cursor", "", "original next cursor")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *id == "" || *role == "" {
		return usageErr("ext sync requires --id and --role; continue with returned --cursor")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	_, _, service, closeAll, err := openRuntime(ctx, *data)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, e := interactiveOwner(ctx, *data)
		if e != nil {
			return e
		}
		page, e := client.ExtensionSync(ctx, *id, *chat, *role, *cursor)
		if e != nil {
			return e
		}
		return writeJSON(stdout, page)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	page, err := harness.NewTaskService(service).SyncExtensionFacts(ctx, *id, *chat, *role, *cursor)
	if err != nil {
		return err
	}
	return writeJSON(stdout, page)
}
