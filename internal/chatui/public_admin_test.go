package chatui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

func TestPublicAdministratorRoutesAndSessionRevocation(t *testing.T) {
	first := newPublicFixture(t, nil)
	first.login(t)
	if w := first.request(t, "POST", "/api/admin-account", strings.NewReader("{}"), nil); w.Code != 404 {
		t.Fatal("local account mutation exposed publicly", w.Code)
	}
	second := &publicFixture{ui: first.ui, server: first.server}
	second.login(t)
	w := second.request(t, "GET", "/api/public/account", nil, nil)
	var state struct{ Sessions []publicweb.SessionView }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Sessions) != 2 {
		t.Fatal("session list unavailable", w.Code)
	}
	for _, s := range state.Sessions {
		if s.Current {
			w := first.request(t, "POST", "/api/public/revoke-session", strings.NewReader(fmt.Sprintf(`{"id":%q}`, s.ID)), nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	if w := second.request(t, "POST", "/api/public/reauthenticate", strings.NewReader("{}"), nil); w.Code != 401 {
		t.Fatal("revoked session authenticated", w.Code)
	}
}

func TestPublicCredentialWritesDoNotReturnSecret(t *testing.T) {
	f := newPublicFixture(t, nil)
	f.login(t)
	for _, item := range []struct{ path, payload, secret string }{
		{"/api/keys", `{"name":"fixture model","protocol":"openai-compatible","base_url":"https://model.example.test/v1","source":"saved","key":"fixture-provider-only-123456"}`, "fixture-provider-only-123456"},
		{"/api/channels/credentials", `{"name":"fixture bot","protocol":"telegram","base_url":"https://api.telegram.org","source":"saved","secret":"123456789:fixture-not-real-token"}`, "123456789:fixture-not-real-token"},
	} {
		if w := f.request(t, "POST", item.path, strings.NewReader(item.payload), map[string]string{"X-FreeAgent-CSRF": ""}); w.Code != 403 {
			t.Fatal("credential write bypassed CSRF", item.path, w.Code)
		}
		w := f.request(t, "POST", item.path, strings.NewReader(item.payload), nil)
		if w.Code/100 != 2 || strings.Contains(w.Body.String(), item.secret) {
			t.Fatal("credential write failed or leaked", item.path, w.Code, w.Body.String())
		}
		w = f.request(t, "GET", item.path, nil, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), item.secret) {
			t.Fatal("credential readback failed or leaked", item.path, w.Code)
		}
	}
}

func TestPublicMCPImportQueuesLocalDecisionWithoutStartingProcess(t *testing.T) {
	f := newPublicFixture(t, nil)
	f.login(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	candidate := harness.MCPServerConfig{Command: exe, Args: []string{"-test.run=^TestAdminMCPProcessFixture$", "freeagent-mcp-fixture"}, Cwd: work}
	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": candidate}})
	if err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "POST", "/api/mcp/import", bytes.NewReader(raw), nil)
	id := regexp.MustCompile("control confirm --operation ([a-f0-9]+)").FindStringSubmatch(w.Body.String())
	if len(id) != 2 || !strings.Contains(w.Body.String(), "local_confirmation_required") {
		t.Fatal("import did not return the pending local proposal", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(work, "spawned")); !os.IsNotExist(err) {
		t.Fatal("candidate process ran before approval")
	}
	if _, exists := f.ui.tasks.Config().MCPServers["fixture"]; exists {
		t.Fatal("remote import committed configuration")
	}
	w = f.request(t, "GET", "/api/control/confirmation?operation_id="+id[1], nil, nil)
	if w.Code/100 == 2 {
		t.Fatal("remote user obtained local confirmation code")
	}
	control, err := f.ui.tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	operations, err := control.ListControlOperations(t.Context())
	if err != nil || len(operations) == 0 {
		t.Fatal("local UI could not read pending proposal", err)
	}
	operation := operations[len(operations)-1]
	if operation.ID != id[1] || !operation.LocalApproval || operation.Status != harness.ControlPending || operation.RequestEntry != "public_web" {
		t.Fatal("local proposal did not retain its identity or entry", operation)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), "", "cli")
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.RejectControlChange(t.Context(), id[1], harness.ControlAuthorization{Source: harness.ControlUserDirect, Principal: "cli", Capability: capability})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdminMCPProcessFixture(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "freeagent-mcp-fixture" {
			if err := os.WriteFile("spawned", []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}
