package chatui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestAttachmentHTTPNoneFlowAndInertDownload(t *testing.T) {
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		return harness.ModelResponse{Content: "done"}, nil
	}))
	if err := ui.tasks.SetSpacePermission(t.Context(), harness.LocalSpaceID, harness.PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	chat := ui.create(t)
	query := url.Values{"conversation_id": {chat}, "name": {"../代码 & !.html"}, "request_key": {"upload"}}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, ui.http.URL+"/api/attachments?"+query.Encode(), strings.NewReader("<script>alert(1)</script>"))
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var ref harness.AttachmentRef
	decodeErr := json.NewDecoder(response.Body).Decode(&ref)
	response.Body.Close()
	if response.StatusCode != 201 || decodeErr != nil {
		t.Fatalf("upload %d %v", response.StatusCode, decodeErr)
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]any{"prompt": "read", "request_key": "one", "attachments": []string{ref.ID}})
	if code != 202 {
		t.Fatalf("submit %d %s", code, raw)
	}
	ui.server.wg.Wait()
	if latest, err := ui.tasks.Latest(t.Context(), chat); err != nil || latest.Status != "completed" {
		t.Fatalf("attachment request did not complete: %+v %v", latest, err)
	}
	request, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, ui.http.URL+"/api/attachments/"+ref.ID+"?conversation_id="+chat+"&inline=1", nil)
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	response, err = ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") || !bytes.Contains(raw, []byte("<script>")) {
		t.Fatalf("unsafe download %d %v %q", response.StatusCode, response.Header, raw)
	}
	request, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, ui.http.URL+"/api/attachments/"+ref.ID+"?conversation_id=other", nil)
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	response, err = ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("cross-chat download", response.StatusCode)
	}
}
