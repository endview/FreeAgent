package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/channels"
)

func TestChannelFilesNoneBackgroundSelectionAndReplyExport(t *testing.T) {
	tasks, _, _, _ := attachmentFixture(t, nil, nil)
	target := ChannelTarget{SpaceSourceTelegram, "bot_1", "chat_1"}
	space, err := tasks.PairChannel(t.Context(), target, "group", "files")
	if err != nil {
		t.Fatal(err)
	}
	sequence, downloads, fileSends := 0, 0, 0
	s := newChannelSession(tasks, SpaceSourceTelegram, func(context.Context, ChannelTarget, string, string, []channelButton) (string, error) {
		sequence++
		return fmt.Sprint(sequence), nil
	})
	s.reset("bot_1")
	s.download = func(context.Context, channelEvent, channels.MediaRef, int64) ([]byte, error) {
		downloads++
		return []byte("public input fact"), nil
	}
	s.sendFile = func(_ context.Context, to ChannelTarget, _ string, ref AttachmentRef, raw []byte) (string, error) {
		fileSends++
		if to != target || ref.Name != "reply.md" || !strings.Contains(string(raw), "public input fact") {
			t.Error("file delivery lost original target or visible content")
		}
		return "file-sent", nil
	}
	e := channelEvent{Target: target, Person: "telegram:user:9", Message: "one", Kind: "group", Text: "[附件]", Media: []channels.MediaRef{{ID: "official_1", Name: "input.md", Bytes: 17}}}
	if err := s.handle(t.Context(), e); err != nil || downloads != 0 {
		t.Fatal("background downloaded", err, downloads)
	}
	e.Message = "two"
	e.Trigger = true
	if err := s.handle(t.Context(), e); err != nil || downloads != 1 {
		t.Fatal("trigger failed", err, downloads)
	}
	last, err := tasks.Latest(t.Context(), space.DefaultConversationID)
	if err != nil || last.Status != "completed" || last.Permission.Mode != PermissionNone || !strings.Contains(last.Output, "public input fact") {
		t.Fatalf("None file input %+v %v", last, err)
	}
	e.Message = "three"
	e.Text = "/save-reply"
	e.Media = nil
	if err := s.handle(t.Context(), e); err != nil || fileSends != 1 {
		t.Fatal("None reply export", err, fileSends)
	}
	if err := s.handle(t.Context(), e); err != nil || fileSends != 1 {
		t.Fatal("duplicate file delivery", err, fileSends)
	}
}
func TestChannelFileUnknownNeverResendsAndDoesNotPublish(t *testing.T) {
	tasks, _, _, _ := attachmentFixture(t, nil, nil)
	target := ChannelTarget{SpaceSourceTelegram, "bot", "chat"}
	space, err := tasks.PairChannel(t.Context(), target, "private", "files")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := tasks.createAttachment(t.Context(), space.DefaultConversationID, "reply.md", strings.NewReader("private"), "reply", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	s := newChannelSession(tasks, SpaceSourceTelegram, func(context.Context, ChannelTarget, string, string, []channelButton) (string, error) { return "", nil })
	calls := 0
	s.sendFile = func(context.Context, ChannelTarget, string, AttachmentRef, []byte) (string, error) {
		calls++
		return "", &channels.TelegramError{Uncertain: true}
	}
	if err := s.sendFileReceipt(t.Context(), space.DefaultConversationID, "run", target, "msg", ref); err == nil {
		t.Fatal("uncertain send accepted")
	}
	if err := s.sendFileReceipt(t.Context(), space.DefaultConversationID, "run", target, "msg", ref); err != nil || calls != 1 {
		t.Fatal("unknown send replayed", err, calls)
	}
	record, err := loadAttachment(t.Context(), tasks.core.Store.db, ref.ID)
	if err != nil || record.Ref.Public {
		t.Fatal("uncertain file published", err)
	}
	notes, err := channelDeliveryNotes(t.Context(), tasks.core.Store.db, "run")
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "UNKNOWN") {
		t.Fatal("lost uncertainty receipt", notes, err)
	}
}
