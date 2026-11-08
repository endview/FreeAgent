package main

import (
	"context"
	"flag"
	"io"

	"github.com/endview/freeagent/internal/harness"
)

func runStickers(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return usageErr("stickers <sets|targets|list|preview|send|reaction> --session ID [--set NAME --saved --offset 0 --resource ID --message-ref REF --emoji EMOJI --request KEY]; edit saved sets through control plan telegram.set_sticker_sets")
	}
	flags := flag.NewFlagSet("stickers", flag.ContinueOnError)
	flags.SetOutput(errOut)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	chat := flags.String("session", "", "Telegram conversation")
	command := harness.StickerCommand{Action: args[0]}
	flags.StringVar(&command.Set, "set", "", "explicit set name or t.me link")
	flags.BoolVar(&command.Saved, "saved", false, "read a saved space set; its references are invalidated on removal")
	flags.StringVar(&command.MessageRef, "message-ref", "", "known incoming:<Core ID> or outbox:<operation ID> from stickers targets; default current incoming")
	flags.IntVar(&command.Offset, "offset", 0, "page offset")
	flags.StringVar(&command.Resource, "resource", "", "scoped Core sticker reference")
	flags.StringVar(&command.Emoji, "emoji", "", "one ordinary emoji; empty removes reaction")
	flags.StringVar(&command.RequestKey, "request", "", "stable send request key")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if *chat == "" || flags.NArg() != 0 {
		return usageErr("stickers requires --session")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return err
	}
	result, err := client.ChannelStickerCommand(ctx, *chat, command)
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}
