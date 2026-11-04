package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"strconv"
	"strings"
)

func runChannel(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || args[0] != "telegram" && args[0] != "lark" && args[0] != "qq" || args[1] != "status" && args[1] != "pair-code" && args[1] != "configure" && args[1] != "access" {
		return usageErr("usage: freeagent channel <telegram|lark|qq> <status|pair-code|configure> --data-dir <directory> [--enabled true|false --key ID --app-id ID --listen 127.0.0.1:PORT]")
	}
	if args[0] == "telegram" && args[1] == "pair-code" {
		return usageErr("Telegram uses /start applications. Use: channel telegram access --data-dir DIR; approve with --request-code CODE --confirm")
	}
	if args[1] == "access" && args[0] != "telegram" {
		return usageErr("access applications are currently Telegram only")
	}
	flags := flag.NewFlagSet("channel "+args[0]+" "+args[1], flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	enabled := flags.String("enabled", "", "true or false")
	key := flags.String("key", "", "saved credential profile ID")
	app := flags.String("app-id", "", "official application ID")
	listen := flags.String("listen", "", "QQ loopback callback host:port")
	code := flags.String("request-code", "", "approve one identity-bound Telegram application")
	chat := flags.String("chat-id", "", "known Telegram numeric chat ID")
	kind := flags.String("kind", "private", "private or group")
	members := flags.String("members", "", "comma-separated verified numeric user IDs")
	whole := flags.Bool("whole-group", false, "all group members may chat; does not grant tools")
	accessOn := flags.Bool("access-enabled", true, "allow or revoke this chat; history is retained")
	confirm := flags.Bool("confirm", false, "explicitly confirm the displayed Telegram admission change")
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
	case "access":
		if *code == "" && *chat == "" {
			out, err = client.TelegramAccess(ctx, nil)
			break
		}
		var listing struct {
			Requests []harness.TelegramAccessRequest `json:"requests"`
			Access   []harness.TelegramAccess        `json:"access"`
		}
		raw, e := client.TelegramAccess(ctx, nil)
		if e != nil {
			return e
		}
		if json.Unmarshal(raw, &listing) != nil {
			return usageErr("invalid access state")
		}
		change := harness.TelegramAccessChange{Enabled: *accessOn, Kind: *kind, WholeGroup: *whole}
		if *code != "" {
			for _, request := range listing.Requests {
				if strings.EqualFold(request.Code, *code) {
					change.RequestID = request.ID
					change.Target = request.Target
					change.Kind = request.Kind
					change.Members = []string{request.PersonID}
				}
			}
			if change.RequestID == "" {
				return usageErr("application is expired or not found; ask the applicant to send /start")
			}
		} else {
			status, e := client.TelegramStatus(ctx)
			if e != nil {
				return e
			}
			change.Target = harness.ChannelTarget{Source: "telegram", ApplicationID: strconv.FormatInt(status.BotID, 10), ChatID: *chat}
			for _, grant := range listing.Access {
				if grant.Target == change.Target {
					change.Kind = grant.Kind
					change.Members = grant.Members
				}
			}
		}
		if *members != "" {
			change.Members = nil
			for _, id := range strings.Split(*members, ",") {
				change.Members = append(change.Members, "telegram:user:"+strings.TrimSpace(id))
			}
		}
		preview, _ := json.MarshalIndent(change, "", "  ")
		fmt.Fprintf(stderr, "Telegram CHAT admission only; tool permissions remain separate.\n%s\n", preview)
		if !*confirm {
			return usageErr("not applied; verify the numeric Bot/chat/person identities and repeat with --confirm")
		}
		out, err = client.TelegramAccess(ctx, change)
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
