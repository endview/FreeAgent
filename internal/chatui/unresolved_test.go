package chatui

import (
	"net/http"
	"strings"
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

func TestPublicUnresolvedRequiresLoginAndCSRFWithoutMaintenanceAccess(t *testing.T) {
	f := newPublicFixture(t, nil)
	chat := f.ui.create(t)
	path := "/api/remote/unresolved?conversation_id=" + chat
	if response := f.request(t, "GET", path, nil, nil); response.Code != 401 {
		t.Fatal(response.Code)
	}
	f.login(t)
	if response := f.request(t, "GET", path, nil, nil); response.Code != 200 || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := f.request(t, "POST", "/api/remote/unresolved", strings.NewReader(`{}`), map[string]string{"X-FreeAgent-CSRF": ""}); response.Code != 403 {
		t.Fatal("CSRF missing", response.Code)
	}
	if response := f.request(t, "GET", "/api/remote/unknown-reconcile", nil, nil); response.Code != 404 {
		t.Fatal("public maintenance exposed", response.Code)
	}
}
