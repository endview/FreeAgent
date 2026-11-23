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

func TestPublicCredentialDraftRevisionPreventsOverwritingRotation(t *testing.T) {
	first := newPublicFixture(t, nil)
	first.login(t)
	second := &publicFixture{ui: first.ui, server: first.server}
	second.login(t)
	for _, tc := range []struct {
		name, write, read, protocol, endpoint, secretField, firstValue, secondValue string
	}{
		{"model", "/api/keys", "/api/keys", "openai-compatible", "https://model.example.test/v1", "key", "fixture-only-key-one", "fixture-only-key-two"},
		{"bot", "/api/channels/credentials", "/api/channels/credentials?revision=1", "telegram", "https://api.telegram.org", "secret", "123456789:fixture-only-one", "123456789:fixture-only-two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type snapshot struct {
				Profiles []struct{ ID, Name string }
				Revision uint64
			}
			read := func(f *publicFixture) snapshot {
				w := f.request(t, "GET", tc.read, nil, nil)
				var s snapshot
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil || s.Revision == 0 ||
					strings.Contains(w.Body.String(), tc.firstValue) || strings.Contains(w.Body.String(), tc.secondValue) {
					t.Fatal("invalid or unsafe settings readback", w.Code)
				}
				return s
			}
			s := read(first)
			payload := map[string]any{"name": tc.name, "protocol": tc.protocol, "base_url": tc.endpoint, "source": "saved",
				tc.secretField: tc.firstValue, "expected_revision": s.Revision}
			post := func(f *publicFixture, path string) (int, string) {
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				w := f.request(t, "POST", path, bytes.NewReader(raw), nil)
				return w.Code, w.Body.String()
			}
			if code, _ := post(first, tc.write); code/100 != 2 {
				t.Fatal("initial credential save failed", code)
			}
			s = read(second)
			if len(s.Profiles) != 1 || s.Profiles[0].Name != tc.name {
				t.Fatal("initial credential did not round-trip")
			}
			id := s.Profiles[0].ID
			path := tc.write
			if tc.name == "bot" {
				payload["id"] = id
			} else {
				path += "/" + id + "/update"
			}
			payload["expected_revision"], payload[tc.secretField] = s.Revision, tc.secondValue
			if code, body := post(first, path); code/100 != 2 || strings.Contains(body, tc.secondValue) {
				t.Fatal("rotation failed or exposed secret", code)
			}
			payload["name"], payload[tc.secretField] = "old draft", tc.firstValue
			if code, body := post(second, path); code != 409 || !strings.Contains(body, "control_stale") {
				t.Fatal("old draft was not rejected", code)
			}
			after := read(second)
			if after.Revision != s.Revision+1 || after.Profiles[0].Name != tc.name {
				t.Fatal("stale draft changed the saved profile")
			}
		})
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
	local, err := control.RenderConfirmation(t.Context(), id[1], "", "cli")
	if err != nil || !strings.Contains(local.Preview, "freeagent-mcp-fixture") || strings.Contains(operation.Preview, "freeagent-mcp-fixture") {
		t.Fatal("local launch review is missing or written to the public journal", err)
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
