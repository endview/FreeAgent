package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
)

func TestTelegramStickerReactionAPIsAndPagination(t *testing.T) {
	calls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		switch path.Base(r.URL.Path) {
		case "getStickerSet":
			if string(input["name"]) != `"Example_by_fixturebot"` {
				t.Error("incorrect set name")
			}
			fmt.Fprint(w, `{"ok":true,"result":{"name":"Example_by_fixturebot","title":"Fixture","stickers":[{"file_id":"a","file_unique_id":"u1","width":64,"height":64},{"file_id":"b","file_unique_id":"u2","width":64,"height":64,"is_animated":true},{"file_id":"c","file_unique_id":"u3","width":64,"height":64,"is_video":true}]}}`)
		case "sendSticker":
			if string(input["sticker"]) != `"known-file"` || input["allow_paid_broadcast"] != nil {
				t.Error("unexpected sticker body")
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":8,"chat":{"id":90}}}`)
		case "setMessageReaction":
			if string(input["message_id"]) != "7" || string(input["is_big"]) != "false" {
				t.Error("unbound reaction")
			}
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected method")
		}
	}))
	defer apiServer.Close()
	api, err := NewTelegramAPI(apiServer.URL, "1:FIXTURE", nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := api.StickerSet(t.Context(), "https://t.me/addstickers/Example_by_fixturebot", 1, 2)
	if err != nil || page.Total != 3 || len(page.Stickers) != 2 || page.Stickers[0].Format != "tgs" || page.Stickers[1].Format != "webm" {
		t.Fatal(page, err)
	}
	for _, format := range []string{"static", "tgs", "webm"} {
		if _, err := api.SendSticker(t.Context(), 90, 7, "known-file"); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
	}
	if err := api.SetReaction(t.Context(), 90, 7, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetReaction(t.Context(), 90, 7, ""); err != nil {
		t.Fatal(err)
	}
	before := calls
	if _, err := api.StickerSet(t.Context(), "https://evil.example/addstickers/x", 0, 1); err == nil {
		t.Fatal("accepted external URL")
	}
	if err := api.SetReaction(t.Context(), 90, 7, "made-up"); err == nil {
		t.Fatal("accepted invalid reaction")
	}
	if before != calls {
		t.Fatal("invalid input reached network")
	}
}

func TestTelegramStickerAndReactionEvents(t *testing.T) {
	bot := TelegramUser{ID: 1, IsBot: true, Username: "fixturebot"}
	for _, format := range []string{"webp", "tgs", "webm"} {
		s := &TelegramSticker{ID: "file", UniqueID: "unique", Width: 512, Height: 512, Emoji: "👍", Set: "Set", Animated: format == "tgs", Video: format == "webm"}
		e, ok := TelegramEventFromUpdate(TelegramUpdate{ID: 8, Message: &TelegramMessage{ID: 7, Date: 1, From: &TelegramUser{ID: 9}, Chat: TelegramChat{ID: 90, Type: "private"}, Sticker: s}}, bot)
		if !ok || !e.Triggered || e.Sticker == nil || e.Text == "" || e.Sticker.Format != format {
			t.Fatal(e, ok)
		}
		if format == "webp" && len(e.Media) != 1 {
			t.Fatal("static sticker lost preview")
		}
		if format != "webp" && len(e.Media) != 0 {
			t.Fatal("fabricated animated preview")
		}
	}
	e, ok := TelegramEventFromUpdate(TelegramUpdate{ID: 9, Reaction: &TelegramReaction{Chat: TelegramChat{ID: 90, Type: "private"}, Message: 7, Date: 2, User: &TelegramUser{ID: 9}, Old: []TelegramReactionType{{Type: "emoji", Emoji: "👍"}}, New: []TelegramReactionType{}}}, bot)
	if !ok || e.Reaction == nil || e.Triggered || e.PersonID != "telegram:user:9" || len(e.Reaction.New) != 0 {
		t.Fatal(e, ok)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := stickerPreview(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := stickerPreview([]byte("invalid")); err == nil {
		t.Fatal("accepted invalid preview")
	}
}

func TestTelegramAnonymousReactionCountSnapshots(t *testing.T) {
	bot := TelegramUser{ID: 1, IsBot: true, Username: "fixturebot"}
	var update TelegramUpdate
	if err := json.Unmarshal([]byte(`{"update_id":100,"message_reaction_count":{"chat":{"id":90,"type":"group"},"message_id":7,"date":10,"reactions":[{"type":{"type":"emoji","emoji":"👍"},"total_count":3},{"type":{"type":"custom_emoji","custom_emoji_id":"123"},"total_count":2},{"type":{"type":"paid"},"total_count":1}]}}`), &update); err != nil {
		t.Fatal(err)
	}
	event, ok := TelegramEventFromUpdate(update, bot)
	if !ok || event.Triggered || event.PersonID != "" || event.Reaction == nil || !event.Reaction.Anonymous || !event.Reaction.CountSnapshot || len(event.Reaction.Counts) != 3 || event.Reaction.Counts[0].Count != 3 || event.Reaction.Counts[1].Value != "custom:123" || event.Reaction.Counts[2].Value != "paid" {
		t.Fatal(event, ok)
	}
	update.ReactionCount.Reactions[0].Total = -1
	if _, ok := TelegramEventFromUpdate(update, bot); ok {
		t.Fatal("accepted negative count")
	}
	update.ReactionCount.Reactions = nil
	event, ok = TelegramEventFromUpdate(update, bot)
	if !ok || !event.Reaction.CountSnapshot || len(event.Reaction.Counts) != 0 {
		t.Fatal("lost empty snapshot", event, ok)
	}
}
