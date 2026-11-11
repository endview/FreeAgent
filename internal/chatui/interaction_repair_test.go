package chatui

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestSubmitReceiptRequiresDurableInput(t *testing.T) {
	release := make(chan struct{})
	ui := setupUI(t, fixtureModel(func(ctx context.Context, _ harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		select {
		case <-release:
			return harness.ModelResponse{Content: "done"}, nil
		case <-ctx.Done():
			return harness.ModelResponse{}, ctx.Err()
		}
	}))
	defer close(release)
	chat := ui.create(t)
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]any{"prompt": "accepted input", "request_key": "receipt"})
	if code != 202 {
		t.Fatalf("submit: %d %s", code, raw)
	}
	run, found, err := ui.tasks.RequestStatus(t.Context(), chat, "receipt")
	if err != nil || !found || run.Prompt != "accepted input" {
		t.Fatalf("202 without durable input: %t %v", found, err)
	}
}

func TestSettingsRejectStaleBrowserDrafts(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for _, path := range []string{"/api/attachment-policy", "/api/public-settings", "/api/channels"} {
		t.Run(path, func(t *testing.T) {
			code, raw := ui.request(t, "GET", path, nil)
			if code != 200 {
				t.Fatal(code, string(raw))
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			form := map[string]json.RawMessage{}
			if path == "/api/attachment-policy" {
				form = response
			} else {
				if err := json.Unmarshal(response["config"], &form); err != nil {
					t.Fatal(err)
				}
				form["expected_revisions"] = response["expected_revisions"]
			}
			if len(form["expected_revisions"]) == 0 {
				t.Fatal("missing edit baseline")
			}
			method := "PUT"
			if path == "/api/channels" {
				method = "PATCH"
			}
			code, raw = ui.request(t, method, path, form)
			if code != 200 {
				t.Fatal("first save", code, string(raw))
			}
			code, raw = ui.request(t, method, path, form)
			if code != 409 || !strings.Contains(string(raw), "control_stale") {
				t.Fatal("stale draft overwritten", code, string(raw))
			}
		})
	}
}

func TestPublicMemoryEditorRequiresSessionAndCSRF(t *testing.T) {
	f := newPublicFixture(t, harness.EchoModel{})
	chat := f.ui.create(t)
	raw, _ := json.Marshal(map[string]any{"command": "save", "conversation_id": chat, "scope_kind": "private", "title": "fixture", "body": "confirmed background", "kind": "background", "yes": true, "request_key": "save-once"})
	if w := f.request(t, "POST", "/api/remote/memory", bytes.NewReader(raw), nil); w.Code != 401 {
		t.Fatal("unauthenticated memory", w.Code)
	}
	f.login(t)
	if w := f.request(t, "POST", "/api/remote/memory", bytes.NewReader(raw), map[string]string{"X-FreeAgent-CSRF": ""}); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	w := f.request(t, "POST", "/api/remote/memory", bytes.NewReader(raw), nil)
	if w.Code != 200 {
		t.Fatal("memory save", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/remote/memory", strings.NewReader(`{"command":"list","conversation_id":"`+chat+`","scope_kind":"private","limit":64}`), nil)
	var entries []harness.MemoryEntry
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &entries) != nil || len(entries) != 1 {
		t.Fatal("memory readback", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/remote/memory", strings.NewReader(`{"command":"read","conversation_id":"`+chat+`","scope_kind":"private","entries":["`+entries[0].ID+`"]}`), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "confirmed background") {
		t.Fatal("memory body", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/extensions/com.example.fixture/state", "/api/extensions/com.example.fixture/scope"} {
		if w := f.request(t, "GET", path+"?conversation_id="+chat, nil, nil); w.Code == 404 {
			t.Fatal("management route absent", path)
		}
	}
	if w := f.request(t, "POST", "/api/remote/memory", strings.NewReader(`{"command":"export","conversation_id":"`+chat+`","scope_kind":"private","file":"/tmp/private"}`), nil); w.Code != 403 {
		t.Fatal("CLI file surface exposed", w.Code)
	}
}

func TestInvalidAttachmentsAreRejectedBeforeSubmitReceipt(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	ids := make([]string, ui.tasks.Config().Attachments.MaxFiles+1)
	for i := range ids {
		ids[i] = "not-read-because-count-is-invalid"
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]any{"prompt": "retain this draft", "request_key": "rejected", "attachments": ids})
	if code != 409 || !strings.Contains(string(raw), "attachment_limit") {
		t.Fatalf("accepted invalid input: %d %s", code, raw)
	}
	if _, found, err := ui.tasks.RequestStatus(t.Context(), chat, "rejected"); err != nil || found {
		t.Fatal("invalid input created a Run", err)
	}
}

func TestProviderMetadataSupportsUnchangedEnvAndExplicitEmptyNote(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	profile, err := ui.tasks.Keys().Add(harness.KeyInput{Name: "env fixture", BaseURL: "https://example.test/v1", Source: harness.KeySourceEnv, EnvName: "FREEAGENT_METADATA_FIXTURE", Note: "remove this"})
	if err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, "POST", "/api/keys/"+profile.ID+"/update", map[string]string{"name": "renamed", "source": "env", "base_url": profile.BaseURL, "note": ""})
	var updated harness.KeyProfileView
	if code != 200 || json.Unmarshal(raw, &updated) != nil || updated.Note != "" || updated.Name != "renamed" || updated.EnvName != profile.EnvName {
		t.Fatalf("metadata not saved: %d %s", code, raw)
	}
}

func TestPublicDocumentLinkAllowedWhileCrossSiteAPIStaysDenied(t *testing.T) {
	f := newPublicFixture(t, harness.EchoModel{})
	headers := map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-User": "?1"}
	if response := f.request(t, "GET", "/", nil, headers); response.Code != 200 {
		t.Fatalf("document: %d", response.Code)
	}
	f.login(t)
	if response := f.request(t, "GET", "/api/environment", nil, headers); response.Code != 403 {
		t.Fatalf("API accepted cross-site navigation: %d", response.Code)
	}
}
