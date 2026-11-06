package harness

import (
	"fmt"
	"testing"
)

func TestConversationPagesReachOldTitlesAndBindFilter(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := t.Context()
	_, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at)
	 SELECT 'page-'||lpad(n::text,4,'0'),'标题 '||n,$1,'local','2026-01-01','2026-01-01' FROM generate_series(1,315) n`, service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Store.db.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES('other-work','标题 999','/other','local','2026-01-01','2026-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	query := ConversationQuery{Workspace: service.Config.Workspace, Limit: 31}
	seen := map[string]bool{}
	for {
		page, err := service.Store.ListConversationPage(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 31 {
			t.Fatal("unbounded page")
		}
		for _, item := range page.Items {
			if seen[item.ID] || item.Workspace != query.Workspace {
				t.Fatalf("duplicate or wrong workspace: %+v", item)
			}
			seen[item.ID] = true
		}
		if !page.HasMore {
			break
		}
		bad := query
		bad.Query = "changed"
		bad.Cursor = page.NextCursor
		if _, err := service.Store.ListConversationPage(ctx, bad); err == nil {
			t.Fatal("cursor accepted another filter")
		}
		query.Cursor = page.NextCursor
	}
	if len(seen) != 315 {
		t.Fatal(fmt.Sprintf("only %d sessions discoverable", len(seen)))
	}
	page, err := service.Store.ListConversationPage(ctx, ConversationQuery{Workspace: query.Workspace, Query: "标题 1", Limit: 100})
	if err != nil || len(page.Items) != 100 || !page.HasMore {
		t.Fatalf("title search: count=%d more=%v err=%v", len(page.Items), page.HasMore, err)
	}
	for _, text := range []string{"%", "_", `\`} {
		page, err := service.Store.ListConversationPage(ctx, ConversationQuery{Workspace: query.Workspace, Query: text})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("literal search %q count=%d err=%v", text, len(page.Items), err)
		}
	}
}
