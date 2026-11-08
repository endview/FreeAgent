package main

import (
	"context"
	"flag"
	"github.com/endview/freeagent/internal/harness"
	"io"
)

func runExtensionStoragePreflight(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("ext storage-preflight", flag.ContinueOnError)
	flags.SetOutput(errOut)
	data := flags.String("data-dir", defaultDataDir(), "stopped Owner data directory")
	id := flags.String("id", "", "installed extension ID")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *id == "" || flags.NArg() != 0 {
		return usageErr("ext storage-preflight requires --id; stop the Owner first")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	_, _, service, closeAll, err := openRuntime(ctx, *data)
	if err != nil {
		return err
	}
	defer closeAll()
	if err := harness.NewTaskService(service).PreflightExtensionStorage(ctx, *id); err != nil {
		return err
	}
	return writeJSON(out, map[string]any{"ok": true, "extension": *id, "binding_verified": true})
}
