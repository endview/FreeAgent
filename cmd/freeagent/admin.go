package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

func runAdmin(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("admin <status|init|reset-password> [--data-dir DIR]")
	}
	action := args[0]
	flags := flag.NewFlagSet("admin "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	username := flags.String("username", publicweb.DefaultUsername, "username for the new administrator")
	fromStdin := flags.Bool("password-stdin", false, "read one password line from stdin")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || action != "status" && action != "init" && action != "reset-password" {
		return usageErr("admin <status|init|reset-password> [--data-dir DIR]")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	var password []byte
	if action != "status" {
		var err error
		password, err = readPublicPassword(ctx, stdin, stderr, *fromStdin)
		if err != nil {
			return err
		}
		defer clear(password)
	}
	if err := applyAdminCommand(ctx, *data, action, *username, password, stdout); err != nil {
		return err
	}
	if action != "init" {
		return nil
	}
	clear(password)
	_, _ = fmt.Fprintln(stderr, "Local access is ready. Use freeagent public enable --origin https://YOUR-HOST to enable HTTPS access.")
	if file, ok := stdin.(*os.File); ok && !*fromStdin && isTerminalInput(file) {
		reader := bufio.NewReader(stdin)
		_, _ = fmt.Fprint(stderr, "Configure the optional public Web entrance now? [y/N] ")
		answer, err := reader.ReadString('\n')
		if err != nil || !strings.EqualFold(strings.TrimSpace(answer), "y") {
			return nil
		}
		_, _ = fmt.Fprint(stderr, "HTTPS origin: ")
		origin, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		return runPublic(ctx, []string{"enable", "--data-dir", *data, "--origin", strings.TrimSpace(origin)}, reader, stdout, stderr)
	}
	return nil
}

func isTerminalInput(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func applyAdminCommand(ctx context.Context, data, action, username string, password []byte, stdout io.Writer) error {
	store, err := harness.OpenStore(ctx, data)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err := interactiveOwner(ctx, data)
		if err != nil {
			return err
		}
		result, err := client.AdminAccount(ctx, action, username, password)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	}
	if err != nil {
		return err
	}
	defer store.Close()
	switch action {
	case "init":
		_, err = publicweb.InitAccount(data, username, password)
	case "reset-password":
		err = publicweb.ResetPassword(data, password)
	}
	if err != nil {
		return err
	}
	return writeJSON(stdout, publicweb.InspectAccount(data))
}
