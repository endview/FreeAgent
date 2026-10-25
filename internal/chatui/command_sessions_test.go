package chatui

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandOwnerAPIObservationCannotMutate(t *testing.T) {
	ui := setupUI(t, nil)
	client, observer := contextClient(t, ui, true), contextClient(t, ui, false)
	if records, err := observer.Commands(t.Context()); err != nil || len(records) != 0 {
		t.Fatalf("list %+v %v", records, err)
	}
	if _, err := observer.StopCommand(t.Context(), "missing"); err == nil {
		t.Fatal("observer allowed stop")
	}
	chat := ui.create(t)
	if _, err := observer.StartCommand(t.Context(), chat, "start", json.RawMessage(`{"command":"missing","cwd":".","background":true}`)); err == nil {
		t.Fatal("observer allowed start")
	}
	if _, err := client.PollCommand(t.Context(), "missing", 0, 16384); err == nil {
		t.Fatal("missing command reported success")
	}
	code, _ := ui.request(t, http.MethodGet, "/api/attach/commands?cursor=-1&id=missing", nil)
	if code == 200 {
		t.Fatal("invalid cursor accepted")
	}
}
