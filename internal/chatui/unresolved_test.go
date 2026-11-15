package chatui

import (
	"net/http"
	"testing"
)

func TestUnresolvedAPIRequiresInteractiveOwnerAndReconciliationStaysLocal(t *testing.T) {
	ui := setupUI(t, nil)
	chat := ui.create(t)
	client, observer := contextClient(t, ui, true), contextClient(t, ui, false)
	if _, err := observer.Unresolved(t.Context(), chat, 0); err == nil {
		t.Fatal("observation access exposed unresolved items")
	}
	items, err := client.Unresolved(t.Context(), chat, 0)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	if _, err = observer.ReconcileReadDenials(t.Context(), ""); err == nil {
		t.Fatal("observer entered maintenance")
	}
	preview, err := client.ReconcileReadDenials(t.Context(), "")
	if err != nil || len(preview.Attempts) != 0 || preview.Applied {
		t.Fatal(preview, err)
	}
	if _, ok := publicRoute("/api/remote/unknown-reconcile", http.MethodGet); ok {
		t.Fatal("public maintenance route exposed")
	}
}
