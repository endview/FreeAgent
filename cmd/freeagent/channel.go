package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"io"
)

func runChannel(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || args[0] != "telegram" && args[0] != "lark" && args[0] != "qq" || args[1] != "status" && args[1] != "pair-code" && args[1] != "configure" {
		return usageErr("usage: freeagent channel <telegram|lark|qq> <status|pair-code|configure> --data-dir <directory> [--enabled true|false --key ID --app-id ID --listen 127.0.0.1:PORT]")
	}
	flags := flag.NewFlagSet("channel "+args[0]+" "+args[1], flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	enabled := flags.String("enabled", "", "true or false")
	key := flags.String("key", "", "saved credential profile ID")
	app := flags.String("app-id", "", "official application ID")
	listen := flags.String("listen", "", "QQ loopback callback host:port")
	if err := parseFlags(flags, args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("unexpected channel arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return fmt.Errorf("channel commands require a running GUI/web/chat Owner: %w", err)
	}
	var out json.RawMessage
	switch args[1] {
	case "status":
		out, err = client.ChannelStatus(ctx, args[0])
	case "pair-code":
		out, err = client.ChannelPair(ctx, args[0])
	case "configure":
		current, e := client.ChannelStatus(ctx, args[0])
		if e != nil {
			return e
		}
		on := false
		if *enabled != "" {
			if *enabled != "true" && *enabled != "false" {
				return usageErr("--enabled must be true or false")
			}
			on = *enabled == "true"
		}
		if args[0] == "telegram" {
			var s harness.TelegramChannelStatus
			if json.Unmarshal(current, &s) != nil {
				return usageErr("invalid channel status")
			}
			config := harness.TelegramConfig{Enabled: s.Enabled, Key: s.Key}
			if *key != "" {
				config.Key = *key
			}
			if *enabled != "" {
				config.Enabled = on
			}
			out, err = client.ChannelConfigure(ctx, args[0], config)
		} else {
			var status harness.AppChannelStatus
			if json.Unmarshal(current, &status) != nil {
				return usageErr("invalid channel status")
			}
			c := status.Config
			if *enabled != "" {
				c.Enabled = on
			}
			if *key != "" {
				c.Key = *key
			}
			if *app != "" {
				c.AppID = *app
			}
			if *listen != "" {
				c.Listen = *listen
			}
			out, err = client.ChannelConfigure(ctx, args[0], c)
		}
	}
	if err != nil {
		return err
	}
	return writeJSON(stdout, out)
}
