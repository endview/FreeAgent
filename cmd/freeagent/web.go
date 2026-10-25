package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

const oneShotLease = 15 * time.Minute

func runWeb(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runWebWithBrowser(ctx, args, stdin, stdout, stderr, openBrowser)
}

func runWebWithBrowser(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, browser func(string) error) error {
	var exitOnStdinClose bool
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--exit-on-stdin-close" {
			exitOnStdinClose = true
			continue
		}
		filtered = append(filtered, arg)
	}
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "current Harness data directory")
	port := flags.Int("port", 0, "loopback port; 0 chooses an available port")
	open := flags.Bool("open-browser", false, "open the local Chat in the default Windows browser")
	if err := parseFlags(flags, filtered); err != nil {
		return err
	}
	if flags.NArg() != 0 || *port < 0 || *port > 65535 {
		return usageErr("web: invalid port or arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	if exitOnStdinClose {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			cancel()
		}()
		ctx = runCtx
	}
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{})
	if err != nil {
		return err
	}
	defer closeAll()
	owner, err := startLocalOwner(ctx, tasks, *data, *port, exitOnStdinClose)
	if err != nil {
		return err
	}
	defer owner.Close()
	address := owner.address
	_, _ = fmt.Fprintln(stdout, address)
	_, _ = fmt.Fprintln(stderr, "Local single-Agent Chat. Keep this launch address private. Closing the browser does not stop tasks.")
	if *open {
		if err := browser(address); err != nil {
			_, _ = fmt.Fprintln(stderr, "The browser could not open. Open the launch address above manually.")
		}
	}
	select {
	case <-owner.done:
		return owner.Close()
	case <-ctx.Done():
		return owner.Close()
	case <-owner.handler.StopRequested():
		return owner.Close()
	}
}
