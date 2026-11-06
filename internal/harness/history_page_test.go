package harness

import (
	"bytes"
	"strings"
	"testing"
)

func TestHistoryPagesAndExplicitExportKeepOriginalContent(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, service)
	ctx := t.Context()
	_, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.messages(id,conversation_id,body_json)
	SELECT n,$1,jsonb_build_object('Role','user','Content',CASE WHEN n=125 THEN repeat('中',20000) ELSE 'message-'||n END) FROM generate_series(1,125) n`, chat)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.Store.HistoryPage(ctx, chat, HistoryQuery{Limit: 20})
	if err != nil || len(page.Items) != 20 || !page.HasMore || page.Before != 106 || !page.Items[19].Truncated {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	next, err := service.Store.HistoryPage(ctx, chat, HistoryQuery{Limit: 20, Before: page.Before})
	if err != nil || next.Items[19].ID != 105 {
		t.Fatal("history cursor failed", err)
	}
	var output bytes.Buffer
	count, err := service.Store.ExportHistory(ctx, chat, &output)
	if err != nil || count != 125 || !strings.Contains(output.String(), strings.Repeat("中", 20000)) {
		t.Fatal("explicit export dropped source", count, err)
	}
	if len(bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))) != 125 {
		t.Fatal("export is not one JSON record per message")
	}
}
