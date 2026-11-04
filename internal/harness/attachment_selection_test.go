package harness

import (
	"strings"
	"testing"
)

func TestExportSelectionReusesContentKeepsLatestSourceAndFreezesDispatch(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, nil, nil)
	ctx := t.Context()
	run := "selection-fixture"
	export := func(source, content string, keep bool) AttachmentRef {
		t.Helper()
		ref, err := tasks.createAttachment(ctx, chat, "same.md", strings.NewReader(content), "file_export", run, true, exportSelection{Source: source, KeepVersion: keep})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	a := export("source/a", "first", false)
	same := export("source/a", "first", false)
	if a.ID != same.ID {
		t.Fatal("identical source/content duplicated")
	}
	b := export("source/a", "second", false)
	other := export("source/b", "same name different directory", false)
	for id, want := range map[string]bool{a.ID: false, b.ID: true, other.ID: true} {
		record, err := loadAttachment(ctx, tasks.core.Store.db, id)
		if err != nil || record.ExportSelected != want {
			t.Fatalf("selection %s=%t want %t err=%v", id, record.ExportSelected, want, err)
		}
	}
	if _, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, "channel/file-send/fixture/version", string(mustJSON(map[string]any{"state": "unknown", "attachment": b}))); err != nil {
		t.Fatal(err)
	}
	c := export("source/a", "third", false)
	record, _ := loadAttachment(ctx, tasks.core.Store.db, c.ID)
	if record.ExportSelected {
		t.Fatal("UNKNOWN version silently replaced")
	}
	explicit := export("source/a", "fourth explicitly requested version", true)
	record, _ = loadAttachment(ctx, tasks.core.Store.db, explicit.ID)
	if !record.ExportSelected || !record.ExportKeepVersion {
		t.Fatal("explicit versions lost")
	}
	for _, ref := range []AttachmentRef{a, b, other, c, explicit} {
		if _, _, err := tasks.Attachment(ctx, chat, ref.ID); err != nil {
			t.Fatal("snapshot was overwritten", err)
		}
	}
}
