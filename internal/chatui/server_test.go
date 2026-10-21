package chatui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/winlaunch"
)

type fixtureModel func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error)

func (model fixtureModel) Generate(ctx context.Context, request harness.ModelRequest, emit func(string)) (harness.ModelResponse, error) {
	return model(ctx, request, emit)
}

type testUI struct {
	server *Server
	http   *httptest.Server
	tasks  *harness.TaskService
	data   string
	work   string
}

func setupUI(t *testing.T, model harness.ModelClient) *testUI {
	t.Helper()
	base := t.TempDir()
	work, data := filepath.Join(base, "workspace"), filepath.Join(base, "data")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	config := harness.DefaultConfig(work)
	config.Models = map[string]harness.ModelConfig{
		"default": {Provider: "echo"},
		"second":  {Provider: "echo", Capabilities: harness.ModelCapabilities{Strength: "light"}},
	}
	config.DefaultModel = "default"
	return setupUIWithConfig(t, model, data, work, config)
}

func setupUIWithConfig(t *testing.T, model harness.ModelClient, data, work string, config harness.Config) *testUI {
	t.Helper()
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	keys := harness.NewKeyStore(filepath.Join(filepath.Dir(data), "keys.json"))
	tasks, closeTasks, err := harness.OpenTasks(t.Context(), data, harness.Adapters{Model: model, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTasks)
	httpServer := httptest.NewUnstartedServer(nil)
	origin := "http://" + httpServer.Listener.Addr().String()
	server, err := NewServer(t.Context(), tasks, origin, NewToken())
	if err != nil {
		t.Fatal(err)
	}
	httpServer.Config.Handler = server
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	t.Cleanup(server.Close)
	return &testUI{server: server, http: httpServer, tasks: tasks, data: data, work: work}
}

func (ui *testUI) request(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	request, err := http.NewRequestWithContext(t.Context(), method, ui.http.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	request.Header.Set("Origin", ui.http.URL)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func (ui *testUI) create(t *testing.T) string {
	t.Helper()
	code, raw := ui.request(t, "POST", "/api/chats", map[string]string{"title": "中文会话"})
	var chat harness.Conversation
	if code != 201 || json.Unmarshal(raw, &chat) != nil || chat.ID == "" {
		t.Fatalf("create %d %s", code, raw)
	}
	return chat.ID
}

func TestChatServerPinConversation(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	code, raw := ui.request(t, http.MethodPost, "/api/chats/"+chat+"/pin", map[string]bool{"pinned": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"pinned":true`) {
		t.Fatalf("pin %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/chats", nil)
	if code != http.StatusOK {
		t.Fatalf("list %d %s", code, raw)
	}
	var chats []harness.Conversation
	if err := json.Unmarshal(raw, &chats); err != nil || len(chats) != 1 || !chats[0].Pinned {
		t.Fatalf("listed chats=%+v err=%v", chats, err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/chats/"+chat+"/pin", map[string]bool{"pinned": false})
	if code != http.StatusOK || strings.Contains(string(raw), `"pinned":true`) {
		t.Fatalf("unpin %d %s", code, raw)
	}
}

func TestControlAPIUsesCoreShortCodeAndAppliesFuturePolicy(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, http.MethodPost, "/api/control/plan", map[string]any{
		"kind":      harness.ControlSetPermission,
		"arguments": map[string]any{"mode": harness.PermissionFull},
	})
	if code != http.StatusAccepted {
		t.Fatalf("plan %d %s", code, raw)
	}
	var operation harness.ControlOperation
	if err := json.Unmarshal(raw, &operation); err != nil || operation.ID == "" || operation.Digest == "" {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/control/confirmation?operation_id="+operation.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("confirmation %d %s", code, raw)
	}
	var confirmation harness.ControlConfirmation
	if err := json.Unmarshal(raw, &confirmation); err != nil || len(confirmation.Code) != 10 {
		t.Fatalf("confirmation=%+v err=%v", confirmation, err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/control/confirm", map[string]any{
		"operation_id": operation.ID, "code": confirmation.Code,
	})
	if code != http.StatusOK {
		t.Fatalf("confirm %d %s", code, raw)
	}
	stateCode, stateRaw := ui.request(t, http.MethodGet, "/api/control/state", nil)
	if stateCode != http.StatusOK || !strings.Contains(string(stateRaw), `"permission_mode":"full"`) {
		t.Fatalf("state %d %s", stateCode, stateRaw)
	}
}

func TestAttachStateEventsAndControlBoundary(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ui.http.URL+"/api/attach/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.token)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state map[string]any
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&state) != nil || state["protocol"] != "freeagent-attach-v1" {
		t.Fatalf("attach state status=%d state=%+v", response.StatusCode, state)
	}
	request, err = http.NewRequestWithContext(t.Context(), http.MethodGet, ui.http.URL+"/api/attach/events?generation=wrong&after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.token)
	response, err = ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("generation mismatch status=%d", response.StatusCode)
	}
}

func TestAttachClientSubmitDeduplicatesValidatesConversationAndHandsOff(t *testing.T) {
	var modelCalls atomic.Int32
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		modelCalls.Add(1)
		return harness.ModelResponse{Content: "attached result"}, nil
	}))
	info := handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}
	client, err := handoff.NewClient(info, ui.server.token)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.State(t.Context())
	if err != nil || state.Generation != ui.server.generation || state.Stopping {
		t.Fatalf("attach state=%+v err=%v", state, err)
	}
	chat, err := client.NewConversation(t.Context(), "attached")
	if err != nil || chat.ID == "" {
		t.Fatalf("new conversation=%+v err=%v", chat, err)
	}
	if err := client.Submit(t.Context(), chat.ID, "attach-request", "hello from attach"); err != nil {
		t.Fatal(err)
	}
	view := ui.settled(t, chat.ID)
	if len(view.Turns) != 1 || view.Turns[0].State != "completed" || modelCalls.Load() != 1 {
		t.Fatalf("attached submit view=%+v calls=%d", view, modelCalls.Load())
	}
	status, err := client.Status(t.Context(), chat.ID, "attach-request")
	if err != nil || !status.Found || status.Run.Status != "completed" {
		t.Fatalf("attached status=%+v err=%v", status, err)
	}
	if err := client.Submit(t.Context(), chat.ID, "attach-request", "hello from attach"); err != nil {
		t.Fatal("duplicate submit was rejected instead of being idempotent: " + err.Error())
	}
	ui.settled(t, chat.ID)
	if modelCalls.Load() != 1 {
		t.Fatal("duplicate attach request dispatched a second model call")
	}
	other, err := client.NewConversation(t.Context(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Continue(t.Context(), other.ID, status.Run.ID); err == nil || !strings.Contains(err.Error(), "conversation_mismatch") {
		t.Fatalf("cross-conversation continue error=%v", err)
	}
	events, err := client.Events(t.Context(), 0)
	if err != nil || events.Generation != ui.server.generation || len(events.Events) == 0 {
		t.Fatalf("attach events=%+v err=%v", events, err)
	}
	stale := *client
	stale.Generation = "stale-generation"
	if _, err := stale.Events(t.Context(), 0); err == nil || !strings.Contains(err.Error(), "generation_changed") {
		t.Fatalf("stale generation error=%v", err)
	}
	if err := client.Handoff(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ui.server.StopRequested():
	case <-time.After(2 * time.Second):
		t.Fatal("handoff did not request owner shutdown")
	}
	state, err = client.State(t.Context())
	if err != nil || !state.Stopping {
		t.Fatalf("stopping attach state=%+v err=%v", state, err)
	}
	if err := client.Submit(t.Context(), chat.ID, "after-handoff", "must not run"); err == nil {
		t.Fatal("owner accepted a new request after handoff")
	}
}

func TestAttachSecretCannotReachOrdinaryMutationRoutes(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ui.http.URL+"/api/chats", strings.NewReader(`{"title":"must reject"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.attachToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", ui.http.URL)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("attach token reached ordinary mutation route: status=%d", response.StatusCode)
	}
	request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, ui.http.URL+"/api/control/plan", strings.NewReader(`{"kind":"execution.set_permission","arguments":{"mode":"full"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.attachToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", ui.http.URL)
	response, err = ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("attach token created a pending control operation: status=%d", response.StatusCode)
	}
}

func TestAttachEventsRejectExpiredBoundedCursor(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for index := 0; index < 300; index++ {
		ui.server.notify("refresh", "chat", fmt.Sprintf("event-%d", index))
	}
	client, err := handoff.NewClient(handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}, ui.server.attachToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Events(t.Context(), 0); err == nil || !strings.Contains(err.Error(), "cursor_expired") {
		t.Fatalf("expired attach cursor was silently truncated: %v", err)
	}
}

func TestSettingsCommandRunCanBeEnabledAndSelected(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	type commandProjection struct {
		Enabled bool   `json:"enabled"`
		Backend string `json:"backend"`
		Status  string `json:"status"`
	}
	type settingsProjection struct {
		Revision     string            `json:"revision"`
		EnabledTools map[string]bool   `json:"enabled_tools"`
		CommandRun   commandProjection `json:"command_run"`
	}
	getSettings := func() settingsProjection {
		t.Helper()
		code, raw := ui.request(t, http.MethodGet, "/api/settings", nil)
		var settings settingsProjection
		if code != http.StatusOK || json.Unmarshal(raw, &settings) != nil {
			t.Fatalf("settings %d %s", code, raw)
		}
		return settings
	}

	initial := getSettings()
	if initial.EnabledTools["command_run"] || initial.CommandRun.Enabled {
		t.Fatalf("command_run was enabled by default: %+v", initial)
	}

	code, raw := ui.request(t, http.MethodPatch, "/api/settings/tools", map[string]any{
		"command_run": true, "expected_revision": initial.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("enable command_run %d %s", code, raw)
	}
	enabled := getSettings()
	wantSandbox := "unsupported"
	if winlaunch.SandboxSupported() {
		wantSandbox = "ready"
	}
	if !enabled.EnabledTools["command_run"] || !enabled.CommandRun.Enabled || enabled.CommandRun.Status != wantSandbox {
		t.Fatalf("command_run enable was not persisted accurately: %+v", enabled)
	}

	code, raw = ui.request(t, http.MethodPatch, "/api/settings/command-run", map[string]any{
		"enabled": true, "execution": "host_explicit", "expected_revision": enabled.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("select host command backend %d %s", code, raw)
	}
	host := getSettings()
	if !host.EnabledTools["command_run"] || !host.CommandRun.Enabled || host.CommandRun.Backend != "host_explicit" || host.CommandRun.Status != "permission_denied" {
		t.Fatalf("host backend was not saved or status was inaccurate: %+v", host)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/runtime-tools", nil)
	var restrictedRuntime struct {
		Definitions []struct {
			Tool   string `json:"tool"`
			Status string `json:"status"`
		} `json:"definitions"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &restrictedRuntime) != nil {
		t.Fatalf("restricted runtime tools %d %s", code, raw)
	}
	restrictedFound := false
	for _, definition := range restrictedRuntime.Definitions {
		if definition.Tool == "command_run" && definition.Status != "permission_denied" {
			t.Fatalf("restricted host backend was reported as %q", definition.Status)
		}
		if definition.Tool == "command_run" {
			restrictedFound = true
		}
	}
	if !restrictedFound {
		t.Fatal("enabled command_run was missing from the restricted runtime view")
	}

	code, raw = ui.request(t, http.MethodPatch, "/api/settings/permissions", map[string]any{
		"default_permission_mode": "full", "expected_revision": host.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("select Full access default %d %s", code, raw)
	}
	full := getSettings()
	if full.CommandRun.Status != "ready" {
		t.Fatalf("host backend did not become ready under Full access: %+v", full)
	}

	code, raw = ui.request(t, http.MethodGet, "/api/runtime-tools", nil)
	var runtime struct {
		Definitions []struct {
			Tool    string `json:"tool"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"definitions"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &runtime) != nil {
		t.Fatalf("runtime tools %d %s", code, raw)
	}
	found := false
	for _, definition := range runtime.Definitions {
		if definition.Tool == "command_run" {
			found = definition.Enabled && definition.Status == "ready"
		}
	}
	if !found {
		t.Fatalf("Full access runtime did not advertise command_run: %+v", runtime.Definitions)
	}

	code, raw = ui.request(t, http.MethodPatch, "/api/settings/tools", map[string]any{
		"command_run": false, "expected_revision": full.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("disable command_run %d %s", code, raw)
	}
	disabled := getSettings()
	if disabled.EnabledTools["command_run"] || disabled.CommandRun.Enabled || disabled.CommandRun.Status != "disabled" {
		t.Fatalf("command_run disable was not persisted accurately: %+v", disabled)
	}
}

func TestSettingsNetworkToolRequiresOriginsAndHotReloads(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	type settingsProjection struct {
		Revision     string          `json:"revision"`
		EnabledTools map[string]bool `json:"enabled_tools"`
		Network      struct {
			Enabled        bool     `json:"enabled"`
			AllowedOrigins []string `json:"allowed_origins"`
		} `json:"network"`
	}
	getSettings := func() settingsProjection {
		t.Helper()
		code, raw := ui.request(t, http.MethodGet, "/api/settings", nil)
		var settings settingsProjection
		if code != http.StatusOK || json.Unmarshal(raw, &settings) != nil {
			t.Fatalf("settings %d %s", code, raw)
		}
		return settings
	}
	initial := getSettings()
	code, raw := ui.request(t, http.MethodPatch, "/api/settings/tools", map[string]any{
		"network": true, "network_allowed_origins": []string{}, "expected_revision": initial.Revision,
	})
	var failure map[string]string
	if code != http.StatusConflict || json.Unmarshal(raw, &failure) != nil || failure["code"] != "network_origins_required" {
		t.Fatalf("missing network origins was not rejected clearly: %d %s", code, raw)
	}
	unchanged := getSettings()
	if unchanged.EnabledTools["network"] || unchanged.Network.Enabled {
		t.Fatalf("failed network update changed state: %+v", unchanged)
	}

	code, raw = ui.request(t, http.MethodPatch, "/api/settings/tools", map[string]any{
		"network": true, "network_allowed_origins": []string{"https://docs.example"}, "expected_revision": unchanged.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("network enable %d %s", code, raw)
	}
	enabled := getSettings()
	if !enabled.EnabledTools["network"] || !enabled.Network.Enabled || len(enabled.Network.AllowedOrigins) != 1 || enabled.Network.AllowedOrigins[0] != "https://docs.example" {
		t.Fatalf("network enable was not persisted: %+v", enabled)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/runtime-tools", nil)
	var runtime struct {
		Definitions []struct {
			Tool    string `json:"tool"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"definitions"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &runtime) != nil {
		t.Fatalf("runtime tools %d %s", code, raw)
	}
	found := false
	for _, definition := range runtime.Definitions {
		if definition.Tool == "network" && definition.Enabled && definition.Status == "ready" {
			found = true
		}
	}
	if !found {
		t.Fatal("hot-reloaded network tool was not ready")
	}
	policy := ui.tasks.RuntimePolicy()
	if !policy.EnabledTools.Network || !policy.HTTP.Enabled || len(policy.HTTP.AllowedOrigins) != 1 || policy.HTTP.AllowedOrigins[0] != "https://docs.example" {
		t.Fatalf("hot-reloaded HTTP policy was not installed: %+v", policy)
	}

	code, raw = ui.request(t, http.MethodPatch, "/api/settings/tools", map[string]any{
		"network": false, "network_allowed_origins": []string{"https://docs.example"}, "expected_revision": enabled.Revision,
	})
	if code != http.StatusOK {
		t.Fatalf("network disable %d %s", code, raw)
	}
	disabled := getSettings()
	if disabled.EnabledTools["network"] || disabled.Network.Enabled || len(disabled.Network.AllowedOrigins) != 0 {
		t.Fatalf("network disable was not persisted: %+v", disabled)
	}
}

func (ui *testUI) settled(t *testing.T, chat string) harness.ChatView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, raw := ui.request(t, "GET", "/api/chats/"+chat, nil)
		var payload struct {
			View harness.ChatView `json:"view"`
			Busy bool             `json:"busy"`
		}
		if code != 200 || json.Unmarshal(raw, &payload) != nil {
			t.Fatalf("snapshot %d %s", code, raw)
		}
		if !payload.Busy {
			return payload.View
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("foreground operation did not settle")
	return harness.ChatView{}
}

func TestChatServerSubmissionDedupRawSwitchAndReconnect(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	for range 2 {
		code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{
			"prompt": "多行\n中文\n$$x^2$$", "request_key": "same-request",
		})
		if code != 202 {
			t.Fatalf("submit %d %s", code, raw)
		}
		view := ui.settled(t, chat)
		if len(view.Turns) != 1 || view.Turns[0].State != "completed" || len(view.Turns[0].Messages) != 2 {
			t.Fatalf("duplicate changed history: %+v", view)
		}
	}
	view := ui.settled(t, chat)
	code, raw := ui.request(t, "GET", "/api/chats/"+chat+"/raw?kind=message&key="+view.Turns[0].Messages[1].Key, nil)
	if code != 200 || !strings.Contains(string(raw), "Echo:") {
		t.Fatalf("raw %d %s", code, raw)
	}
	code, raw = ui.request(t, "POST", "/api/chats/"+chat+"/model", map[string]string{"model": "second"})
	if code != 200 {
		t.Fatalf("model %d %s", code, raw)
	}
	view = ui.settled(t, chat)
	if view.ModelID != "second" || len(view.Turns) != 1 {
		t.Fatal("model selection rewrote history")
	}
	identity := ui.tasks.EnvironmentID()
	other, err := NewServer(t.Context(), ui.tasks, "http://127.0.0.1:12345", NewToken())
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if identity != ui.tasks.EnvironmentID() {
		t.Fatal("port/token changed environment identity")
	}
	otherChat := ui.create(t)
	code, _ = ui.request(t, "GET", "/api/chats/"+otherChat+"/raw?kind=message&key="+view.Turns[0].Messages[1].Key, nil)
	if code == 200 {
		t.Fatal("raw body crossed Chat boundary")
	}
}

func TestChatMemoryProjectionAndExclusion(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	tasks := ui.tasks
	agent, err := tasks.AgentIdentity(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	scope := harness.MemoryScope{Kind: "private", Owner: agent, Project: tasks.Config().Workspace}
	receipt, err := tasks.SaveMemory(t.Context(), chat, harness.MemoryMutation{
		RequestKey: "chat-memory-entry", Action: "create", Scope: scope, Kind: "finding",
		Title: "Visible title", Body: "secret body", Sources: []harness.MemorySource{{Kind: "user"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectMemory(t.Context(), chat, harness.MemorySelection{
		Entries: []harness.MemoryReference{{Scope: scope, EntryID: receipt.EntryID}},
	}); err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, "GET", "/api/chats/"+chat+"/memory", nil)
	var view struct {
		Entries []struct {
			Title string `json:"title"`
			State string `json:"state"`
		} `json:"entries"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &view) != nil || len(view.Entries) != 1 ||
		view.Entries[0].Title != "Visible title" || view.Entries[0].State != "active" ||
		strings.Contains(string(raw), "secret body") {
		t.Fatalf("memory projection %d %s", code, raw)
	}
	code, raw = ui.request(t, "POST", "/api/chats/"+chat+"/exclude-memory", map[string]any{
		"entry": map[string]any{"scope": scope, "entry_id": receipt.EntryID},
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"removed":true`) {
		t.Fatalf("memory exclusion %d %s", code, raw)
	}
	code, raw = ui.request(t, "GET", "/api/chats/"+chat+"/memory", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"entries":[]`) {
		t.Fatalf("memory after exclusion %d %s", code, raw)
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+chat+"/exclude-memory", map[string]any{
		"entry":     map[string]any{"scope": scope, "entry_id": receipt.EntryID},
		"knowledge": scope,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("ambiguous memory exclusion status=%d", code)
	}
}

func TestChatServerExactApprovalAndEvidence(t *testing.T) {
	var count atomic.Int64
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		if count.Add(1) == 1 {
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "create-exact", Name: "file_create",
				Arguments: json.RawMessage(`{"path":"approved.txt","content":"EXACT-CONTENT"}`)}}}, nil
		}
		return harness.ModelResponse{Content: "Model explanation is not execution evidence."}, nil
	}))
	chat := ui.create(t)
	code, _ := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "create", "request_key": "write"})
	if code != 202 || ui.settled(t, chat).Turns[0].State != "awaiting_approval" {
		t.Fatal("write skipped approval")
	}
	if _, err := os.Stat(filepath.Join(ui.work, "approved.txt")); !os.IsNotExist(err) {
		t.Fatal("file created before approval")
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/preview", map[string]any{})
	var confirmation harness.Confirmation
	if code != 200 || json.Unmarshal(raw, &confirmation) != nil || confirmation.Digest == "" {
		t.Fatalf("preview %d %s", code, raw)
	}
	stale := confirmation
	stale.Digest = strings.Repeat("0", 64)
	code, _ = ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{"confirmation": stale, "approved": true})
	if code != 202 || ui.settled(t, chat).Turns[0].State != "awaiting_approval" || count.Load() != 1 {
		t.Fatal("stale confirmation dispatched work")
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{"confirmation": confirmation, "approved": true})
	view := ui.settled(t, chat)
	if code != 202 || view.Turns[0].State != "completed" || len(view.Turns[0].Evidence.Files) != 1 {
		t.Fatalf("confirm/evidence %+v", view)
	}
	content, err := os.ReadFile(filepath.Join(ui.work, "approved.txt"))
	if err != nil || string(content) != "EXACT-CONTENT" {
		t.Fatal("confirmed result missing")
	}
}

func TestChatServerPermissionModesAndFrozenSnapshot(t *testing.T) {
	calls := map[string]int{}
	ui := setupUI(t, fixtureModel(func(_ context.Context, request harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		prompt := ""
		for _, message := range request.Messages {
			if message.Role == "user" {
				prompt = message.Content
			}
		}
		calls[prompt]++
		if calls[prompt] == 1 {
			path := "permission-workspace.txt"
			if strings.Contains(prompt, "full") {
				path = "permission-full.txt"
			}
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "create-permission", Name: "file_create",
				Arguments: json.RawMessage(`{"path":"` + path + `","content":"permission"}`)}}}, nil
		}
		return harness.ModelResponse{Content: "done"}, nil
	}))

	code, raw := ui.request(t, "GET", "/api/environment", nil)
	var environment struct {
		PermissionModes []harness.PermissionMode `json:"permission_modes"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &environment) != nil ||
		len(environment.PermissionModes) != 3 {
		t.Fatalf("permission environment %d %s", code, raw)
	}

	ask := ui.create(t)
	code, raw = ui.request(t, "GET", "/api/chats/"+ask, nil)
	var askSnapshot struct {
		View harness.ChatView `json:"view"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &askSnapshot) != nil ||
		askSnapshot.View.Permission.Mode != harness.PermissionAsk ||
		askSnapshot.View.Permission.Source != "default" {
		t.Fatalf("default permission %d %s", code, raw)
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+ask+"/submit",
		map[string]any{"prompt": "ask", "request_key": "ask", "permission": "ask"})
	if code != http.StatusAccepted || ui.settled(t, ask).Turns[0].State != "awaiting_approval" {
		t.Fatal("Ask did not wait for approval")
	}
	if _, err := os.Stat(filepath.Join(ui.work, "permission-workspace.txt")); !os.IsNotExist(err) {
		t.Fatal("Ask dispatched a file mutation")
	}

	workspace := ui.create(t)
	code, _ = ui.request(t, "POST", "/api/chats/"+workspace+"/submit",
		map[string]any{"prompt": "workspace", "request_key": "workspace", "permission": "workspace"})
	workspaceView := ui.settled(t, workspace)
	if code != http.StatusAccepted || workspaceView.Turns[0].State != "completed" ||
		workspaceView.Turns[0].Permission == nil ||
		workspaceView.Turns[0].Permission.Mode != harness.PermissionWorkspace {
		t.Fatalf("Workspace access did not execute or freeze: code=%d view=%+v", code, workspaceView)
	}
	if rawFile, err := os.ReadFile(filepath.Join(ui.work, "permission-workspace.txt")); err != nil || string(rawFile) != "permission" {
		t.Fatalf("workspace file=%q err=%v view=%+v", rawFile, err, workspaceView)
	}

	full := ui.create(t)
	code, raw = ui.request(t, "POST", "/api/chats/"+full+"/submit",
		map[string]any{"prompt": "full", "request_key": "full-unconfirmed", "permission": "full"})
	var failure map[string]string
	if code != http.StatusConflict || json.Unmarshal(raw, &failure) != nil ||
		failure["code"] != "permission_confirmation" {
		t.Fatalf("unconfirmed Full access was not rejected synchronously: %d %s", code, raw)
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+full+"/submit",
		map[string]any{"prompt": "full", "request_key": "full-confirmed", "permission": "full", "confirm_full_access": true})
	fullView := ui.settled(t, full)
	if code != http.StatusAccepted || fullView.Turns[0].State != "completed" ||
		fullView.Turns[0].Permission == nil || fullView.Turns[0].Permission.Mode != harness.PermissionFull {
		t.Fatalf("confirmed Full access did not execute or freeze: code=%d view=%+v", code, fullView)
	}
	if rawFile, err := os.ReadFile(filepath.Join(ui.work, "permission-full.txt")); err != nil || string(rawFile) != "permission" {
		t.Fatalf("full file=%q err=%v", rawFile, err)
	}
}

func TestChatServerStopUnknownAndAbandonDoNotReplay(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	ui := setupUI(t, fixtureModel(func(ctx context.Context, _ harness.ModelRequest, emit func(string)) (harness.ModelResponse, error) {
		calls.Add(1)
		emit("未完成 $$x")
		close(started)
		<-ctx.Done()
		return harness.ModelResponse{}, &harness.CallError{Message: "interrupted"}
	}))
	chat := ui.create(t)
	ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "work", "request_key": "stop"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	code, _ := ui.request(t, "POST", "/api/chats/"+chat+"/stop", map[string]any{})
	view := ui.settled(t, chat)
	if code != 200 || view.Turns[0].State != "unknown" || view.Turns[0].CanContinue {
		t.Fatal("stopped dispatch was marked safe to replay")
	}
	ui.request(t, "POST", "/api/chats/"+chat+"/continue", map[string]any{})
	ui.settled(t, chat)
	if calls.Load() != 1 {
		t.Fatal("UNKNOWN replayed")
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+chat+"/end", map[string]bool{"acknowledge": false})
	if code == 200 {
		t.Fatal("abandonment silently acknowledged")
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+chat+"/end", map[string]bool{"acknowledge": true})
	view = ui.settled(t, chat)
	if code != 200 || !view.Turns[0].Ended || view.Turns[0].State != "unknown" || calls.Load() != 1 {
		t.Fatal("ending erased uncertainty")
	}
}

func TestChatServerStopBeforeTaskServiceEnters(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	gate, done := make(chan struct{}), make(chan error, 1)
	writer := httptest.NewRecorder()
	ui.server.start(writer, chat, func(ctx context.Context) (harness.RunResult, error) {
		<-gate
		done <- ctx.Err()
		return harness.RunResult{}, ctx.Err()
	})
	code, _ := ui.request(t, "POST", "/api/chats/"+chat+"/stop", map[string]any{})
	close(gate)
	if code != 200 || !errors.Is(<-done, context.Canceled) {
		t.Fatal("pre-entry stop race lost cancellation")
	}
	ui.settled(t, chat)
}

func TestChatActiveChat(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		once.Do(func() { close(started) })
		<-release
		return harness.ModelResponse{Content: "done"}, nil
	}))
	first, second := ui.create(t), ui.create(t)
	code, _ := ui.request(t, "POST", "/api/chats/"+first+"/submit", map[string]string{"prompt": "slow", "request_key": "active"})
	if code != http.StatusAccepted {
		t.Fatalf("submit status=%d", code)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	for _, chat := range []string{first, second} {
		code, raw := ui.request(t, "GET", "/api/chats/"+chat, nil)
		var payload struct {
			ActiveChat string `json:"active_chat"`
			Busy       bool   `json:"busy"`
		}
		if code != http.StatusOK || json.Unmarshal(raw, &payload) != nil || payload.ActiveChat != first {
			t.Fatalf("chat=%s payload=%s code=%d", chat, raw, code)
		}
		if chat == first && !payload.Busy {
			t.Fatal("active chat was not busy")
		}
	}
	close(release)
	ui.settled(t, first)
	code, raw := ui.request(t, "GET", "/api/chats/"+second, nil)
	var payload struct {
		ActiveChat string `json:"active_chat"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &payload) != nil || payload.ActiveChat != "" {
		t.Fatalf("idle active_chat=%q payload=%s code=%d", payload.ActiveChat, raw, code)
	}
}

func TestChatServerSecurityAndStrictInput(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for _, spec := range []struct {
		host, origin, key, fetch string
		want                     int
	}{
		{"", "", "", "", 401},
		{"", "https://evil.example", ui.server.token, "", 403},
		{"evil.example", "", ui.server.token, "", 403},
		{"", "", ui.server.token, "cross-site", 403},
	} {
		request := httptest.NewRequest("GET", ui.http.URL+"/api/environment", nil)
		if spec.host != "" {
			request.Host = spec.host
		}
		request.Header.Set("Origin", spec.origin)
		request.Header.Set("X-FreeAgent-Key", spec.key)
		request.Header.Set("Sec-Fetch-Site", spec.fetch)
		writer := httptest.NewRecorder()
		ui.server.ServeHTTP(writer, request)
		if writer.Code != spec.want {
			t.Fatalf("security %+v: %d", spec, writer.Code)
		}
	}
	code, _ := ui.request(t, "POST", "/api/chats", map[string]any{"title": "test", "automatic_approval": true})
	if code != 400 {
		t.Fatal("unknown input field accepted")
	}
	code, raw := ui.request(t, "GET", "/", nil)
	if code != 200 || !strings.Contains(string(raw), "<title>FreeAgent Chat</title>") || !strings.Contains(string(raw), "/assets/") {
		t.Fatal("usable embedded app was not served")
	}
	request := httptest.NewRequest("POST", ui.http.URL+"/api/connect", nil)
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	writer := httptest.NewRecorder()
	ui.server.ServeHTTP(writer, request)
	cookie := writer.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/" {
		t.Fatal("unsafe SSE cookie")
	}
	if !strings.Contains(writer.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing CSP")
	}
}

func TestChatServerSSEReconnectReadsSnapshot(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", ui.http.URL+"/api/events", nil)
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, `"kind":"reconnect"`) {
		t.Fatal("reconnect was not requested")
	}
	cancel()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not SSE")
	}
}

func TestChatServerRejectsNonloopbackOrigin(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for _, origin := range []string{"http://localhost:80", "https://127.0.0.1:80", "http://192.0.2.1:80", "http://127.0.0.1"} {
		if server, err := NewServer(t.Context(), ui.tasks, origin, NewToken()); err == nil {
			server.Close()
			t.Fatalf("accepted %s", origin)
		}
	}
	if net.ParseIP("127.0.0.1") == nil {
		t.Fatal("invalid fixture")
	}
}

func TestChatProviderErrorClassificationDoesNotAuthorizeRetry(t *testing.T) {
	for category, code := range map[string]string{
		"authentication": "provider_auth", "permission": "provider_permission", "quota": "provider_quota",
		"rate_limit": "provider_rate", "context_limit": "capability", "capability": "capability", "usage": "capability",
		"output_limit": "provider_output", "refusal": "provider_refusal", "content_filter": "provider_refusal",
		"request": "provider_request", "redirect": "provider_request", "unavailable": "provider_unavailable",
		"timeout": "provider_unavailable", "cancelled": "stopped", "provider": "provider_failure",
	} {
		t.Run(category, func(t *testing.T) {
			failure := &harness.CallError{Certain: true, Category: category, Retryable: true, Message: "untrusted-detail"}
			got, text := userError(errors.Join(failure, errors.New("extra")))
			if got != code || text == "" || strings.Contains(text, failure.Message) {
				t.Fatalf("wrong or unsafe classification: %s %s", got, text)
			}
			failure.Certain = false
			got, text = userError(failure)
			if got != "unknown" || !strings.Contains(text, "不要直接重试") {
				t.Fatal("retry advice hid uncertain outcome")
			}
			failure.Certain = true
			got, _ = userError(errors.Join(failure, harness.ErrUnknownResult))
			if got != "unknown" {
				t.Fatal("retained UNKNOWN lost precedence")
			}
		})
	}
}

func TestChatServerProviderFailureSnapshotRetainsClassification(t *testing.T) {
	var calls atomic.Int32
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		calls.Add(1)
		return harness.ModelResponse{Content: "partial response"},
			&harness.CallError{Certain: true, Category: "output_limit", Message: "length"}
	}))
	chat := ui.create(t)
	code, _ := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "task", "request_key": "failure"})
	view := ui.settled(t, chat)
	if code != 202 || len(view.Turns) != 1 || view.Turns[0].State != "failed" ||
		view.Turns[0].Activity[0].Failure == nil || view.Turns[0].Activity[0].Failure.Category != "output_limit" ||
		view.Turns[0].Activity[0].Text != "partial response" || len(view.Turns[0].Messages) != 1 {
		t.Fatalf("snapshot hid Provider failure: %+v", view)
	}
	ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "task", "request_key": "failure"})
	ui.settled(t, chat)
	if calls.Load() != 1 {
		t.Fatal("UI duplicate retried failed request")
	}
}
