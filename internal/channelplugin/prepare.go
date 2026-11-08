package channelplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
)

func capabilities(connection channelwire.Connection) channelwire.Capabilities {
	result := channelwire.Capabilities{Version: channelwire.Version, Format: "plain/1", Actions: []string{"prepare_text", "text", "file", "download"}}
	if connection.Source == "telegram" {
		result.Format = channels.TelegramFormatVersion
		if connection.PlainText {
			result.Format += "/plain"
		}
		result.Actions = append(result.Actions, "prepare_resource", "sticker_set", "sticker", "reaction")
		result.Reactions = strings.Fields(channels.TelegramReactions)
	}
	return result
}

func prepareOperation(connection channelwire.Connection, op channelwire.Operation) (channelwire.Receipt, bool) {
	if !channelwire.PurePreparation(op.Kind) {
		return channelwire.Receipt{}, false
	}
	sum := sha256.Sum256([]byte(op.Text))
	if hex.EncodeToString(sum[:]) != op.SourceHash {
		return channelwire.Receipt{State: "failed"}, true
	}
	prepared := channelwire.Prepared{SourceHash: op.SourceHash}
	if op.Kind == "prepare_resource" {
		if connection.Source != "telegram" || op.Name != "sticker_set" {
			return channelwire.Receipt{State: "failed"}, true
		}
		name, err := channels.StickerSetName(op.Text)
		if err != nil {
			return channelwire.Receipt{State: "failed"}, true
		}
		prepared.Resource = strings.ToLower(name)
	} else {
		prepared.Format = capabilities(connection).Format
		if prepared.Format != op.FormatVersion {
			return channelwire.Receipt{State: "failed"}, true
		}
		if connection.Source == "telegram" {
			parts, err := channels.RenderTelegramText(op.Text, connection.PlainText)
			if err != nil {
				return channelwire.Receipt{State: "failed"}, true
			}
			prepared.Parts = parts
		} else {
			prepared.Parts = channelwire.SplitTextParts(channelwire.TextPart{Text: op.Text}, 3500)
		}
	}
	raw, err := json.Marshal(prepared)
	if err != nil || len(raw) > 1<<20 {
		return channelwire.Receipt{State: "failed"}, true
	}
	return channelwire.Receipt{State: "downloaded", Data: raw}, true
}
