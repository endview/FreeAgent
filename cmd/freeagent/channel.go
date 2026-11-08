package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"strconv"
	"strings"
)

func runChannel(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[1] == "credential" {
		return runChannelCredential(ctx, args, stdin, stdout, stderr)
	}
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
	connection := flags.String("connection", "", "explicit optional connection ID")
	app := flags.String("app-id", "", "official application ID")
	listen := flags.String("listen", "", "QQ loopback callback host:port")
	richText := flags.String("rich-text", "", "Telegram rich text: true or false")
	typing := flags.String("typing", "", "Telegram private processing indicator: true or false")
	replyStickers := flags.String("reply-stickers", "", "allow scoped sticker replies: true or false")
	autonomousReactions := flags.String("autonomous-reactions", "", "allow Bot reaction changes on the current input: true or false")
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
		if *connection == "" {
			return usageErr("--connection is required; use one explicit connection per account")
		}
		current, e := client.ChannelStatus(ctx, "")
		if e != nil {
			return e
		}
		var status struct {
			Config harness.ChannelsConfig `json:"config"`
		}
		if json.Unmarshal(current, &status) != nil {
			return usageErr("invalid channel state")
		}
		config := status.Config
		index := -1
		for n, c := range config.Connections {
			if c.ID == *connection {
				index = n
			}
		}
		c := channelwire.Connection{ID: *connection, Source: args[0]}
		if index >= 0 {
			c = config.Connections[index]
		}
		if c.Source != args[0] {
			return usageErr("connection belongs to another platform")
		}
		if *enabled != "" {
			if *enabled != "true" && *enabled != "false" {
				return usageErr("--enabled must be true or false")
			}
			c.Enabled = *enabled == "true"
		}
		if *key != "" {
			c.Credential = *key
		}
		if *app != "" {
			c.ApplicationID = *app
		}
		if *listen != "" {
			c.Listen = *listen
		}
		for _, setting := range []struct {
			name, value string
			field       *bool
		}{{"rich-text", *richText, &c.PlainText}, {"typing", *typing, &c.DisableTyping}} {
			if setting.value == "" {
				continue
			}
			if c.Source != "telegram" || setting.value != "true" && setting.value != "false" {
				return usageErr("--" + setting.name + " requires Telegram and true or false")
			}
			*setting.field = setting.value == "false"
		}
		for _, setting := range []struct {
			name, value string
			field       *bool
		}{{"reply-stickers", *replyStickers, &c.ReplyStickers}, {"autonomous-reactions", *autonomousReactions, &c.AutonomousReactions}} {
			if setting.value == "" {
				continue
			}
			if c.Source != "telegram" || setting.value != "true" && setting.value != "false" {
				return usageErr("--" + setting.name + " requires Telegram and true or false")
			}
			*setting.field = setting.value == "true"
		}
		if index < 0 {
			config.Connections = append(config.Connections, c)
		} else {
			config.Connections[index] = c
		}
		out, err = client.ChannelConfigure(ctx, "", config)
	}
	if err != nil {
		return err
	}
	return writeJSON(stdout, out)
}
