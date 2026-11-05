package main

import (
	"context"
	"flag"
	"io"

	"github.com/endview/freeagent/internal/harness"
)

func runChannelCredential(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) < 3 {
		return usageErr("channel <telegram|lark|qq> credential <list|add|delete> [flags]")
	}
	if args[0] != "telegram" && args[0] != "lark" && args[0] != "qq" {
		return usageErr("unsupported channel")
	}
	command := args[2]
	if command != "list" && command != "add" && command != "delete" {
		return usageErr("credential: use list, add or delete")
	}
	flags := flag.NewFlagSet("channel credential", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	name := flags.String("name", "", "credential name")
	base := flags.String("base-url", "", "official API origin")
	ref := flags.String("ref", "", "saved credential ID")
	keyStdin := flags.Bool("key-stdin", false, "read one bounded secret from stdin")
	keyEnv := flags.String("key-env", "", "reference a process environment variable")
	if err := parseFlags(flags, args[3:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("unexpected credential arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return err
	}
	list, err := client.ChannelCredentials(ctx)
	if err != nil {
		return err
	}
	if command == "list" {
		filtered := []harness.KeyProfileView{}
		for _, p := range list {
			if p.Protocol == args[0] {
				filtered = append(filtered, p)
			}
		}
		return writeJSON(stdout, filtered)
	}
	request := harness.KeyControlRequest{Namespace: "channels", Ref: *ref, Remove: command == "delete"}
	if command == "add" {
		request.Input = harness.KeyInput{Name: *name, BaseURL: *base, Protocol: args[0]}
		if err := (keySource{stdin: *keyStdin, env: *keyEnv}).apply(ctx, &request.Input, true, stdin, stderr); err != nil {
			return err
		}
	} else {
		found := false
		for _, p := range list {
			if p.ID == *ref && p.Protocol == args[0] {
				found = true
			}
		}
		if !found {
			return harness.ErrKeyNotFound
		}
	}
	defer clear(request.Input.Secret)
	result, err := client.ControlKeyMutation(ctx, request)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}
