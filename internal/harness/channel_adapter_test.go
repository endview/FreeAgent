package harness

import (
	"context"
	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
	"strings"
)

// Existing delivery unit tests use a controlled adapter and retain their
// exact outbox assertions. Real transport preparation is tested separately.
func newTestChannelBridge(tasks *TaskService, origin, generation string, dispatch ChannelDispatch) (*ChannelBridge, error) {
	b, err := NewChannelBridge(tasks, origin, generation, dispatch)
	if err != nil {
		return nil, err
	}
	for id, c := range b.connections {
		caps := channelwire.Capabilities{Version: channelwire.Version, Format: "plain/1", Actions: []string{"prepare_text", "text", "file", "download"}}
		if c.config.Source == "telegram" {
			caps.Format = channels.TelegramFormatVersion
			caps.Actions = append(caps.Actions, "prepare_resource", "sticker_set", "sticker", "reaction")
			caps.Reactions = strings.Fields(channels.TelegramReactions)
		}
		if err := b.RegisterCapabilities(id, caps); err != nil {
			return nil, err
		}
	}
	b.preparer = func(_ context.Context, op channelwire.Operation) (channelwire.Prepared, error) {
		prepared := channelwire.Prepared{SourceHash: op.SourceHash, Format: op.FormatVersion}
		if op.Kind == "prepare_resource" {
			name, err := channels.StickerSetName(op.Text)
			prepared.Resource = strings.ToLower(name)
			prepared.Format = ""
			return prepared, err
		}
		c, _ := b.connectionFor(ChannelTarget(op.Target))
		if c.Source == "telegram" {
			parts, err := channels.RenderTelegramText(op.Text, c.PlainText)
			prepared.Parts = parts
			return prepared, err
		}
		prepared.Parts = channelwire.SplitTextParts(channelwire.TextPart{Text: op.Text}, 3500)
		return prepared, nil
	}
	return b, nil
}
