package channels

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

// Read-only official-platform acceptance. Never polls getUpdates, changes the
// Bot's cursor/settings, or sends activity, messages, stickers or reactions.
func TestLiveTelegramReadOnlyStickerCatalogAndPreview(t *testing.T) {
	secret := os.Getenv("FREEAGENT_LIVE_TELEGRAM_READONLY_SECRET")
	if secret == "" {
		t.Skip("NOT_RUN: explicit read-only Telegram test credential is absent")
	}
	api, err := NewTelegramAPI("https://api.telegram.org", secret, nil)
	if err != nil {
		t.Fatal("read-only Telegram test configuration is invalid")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	bot, err := api.Me(ctx)
	if err != nil || !bot.IsBot || bot.ID < 1 {
		t.Fatalf("real Telegram identity check failed: %v", err)
	}
	page, err := api.StickerSet(ctx, "GreatMinds", 0, 24)
	if err != nil || page.Total < 1 || len(page.Stickers) < 1 || len(page.Stickers) > 24 {
		t.Fatalf("real bounded Telegram catalog check failed: %v", err)
	}
	var preview *channelwire.MediaRef
	for _, sticker := range page.Stickers {
		if sticker.Format == "webp" {
			preview = &channelwire.MediaRef{ID: sticker.FileID, Name: "sticker.png", Bytes: sticker.Bytes, Image: true, PreviewFormat: "webp"}
			break
		}
		if sticker.Thumbnail != nil {
			preview = sticker.Thumbnail
			break
		}
	}
	if preview == nil {
		t.Fatal("real selected catalog had no testable static image/preview frame")
	}
	raw, err := api.DownloadStickerPreview(ctx, *preview, 8<<20)
	if err != nil || len(raw) < 8 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("real preview conversion failed: %v", err)
	}
	t.Logf("real getMe/getStickerSet/getFile/download passed; page=%d total=%d preview_bytes=%d; no outgoing actions or polling", len(page.Stickers), page.Total, len(raw))
}
