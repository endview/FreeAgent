package chatui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

const chatUITestSecret = "FA-chatui-sentinel-secret-0001"

func emptyKeyUI(t *testing.T, model harness.ModelClient) *testUI {
	t.Helper()
	base := t.TempDir()
	work, data := filepath.Join(base, "workspace"), filepath.Join(base, "data")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	return setupUIWithConfig(t, model, data, work, harness.DefaultConfig(work))
}

func keyProbeServer(t *testing.T, expectedSecret string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/models" {
			if expectedSecret != "" && request.Header.Get("Authorization") != "Bearer "+expectedSecret {
				http.Error(writer, `{"error":"missing auth"}`, http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"mock-chat"},{"id":"deepseek-flash"}]}`)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[]}`)
	}))
}

func requestRaw(t *testing.T, ui *testUI, method, path, contentType, body string, token bool, origin string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, ui.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token {
		request.Header.Set("X-FreeAgent-Key", ui.server.token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
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

func addSavedKey(t *testing.T, ui *testUI, baseURL, name string) harness.KeyProfileView {
	t.Helper()
	code, raw := ui.request(t, http.MethodPost, "/api/keys", map[string]string{
		"name": name, "base_url": baseURL, "source": harness.KeySourceSaved, "key": chatUITestSecret,
	})
	var view harness.KeyProfileView
	if code != http.StatusCreated || json.Unmarshal(raw, &view) != nil || view.ID == "" {
		t.Fatalf("add key %d %s", code, raw)
	}
	if strings.Contains(string(raw), chatUITestSecret) {
		t.Fatal("key response contained the secret")
	}
	return view
}

func addNoneKey(t *testing.T, ui *testUI, baseURL, name string) harness.KeyProfileView {
	t.Helper()
	code, raw := ui.request(t, http.MethodPost, "/api/keys", map[string]string{
		"name": name, "base_url": baseURL, "source": harness.KeySourceNone,
	})
	var view harness.KeyProfileView
	if code != http.StatusCreated || json.Unmarshal(raw, &view) != nil || view.ID == "" {
		t.Fatalf("add none key %d %s", code, raw)
	}
	return view
}

func keyResponseHasSecret(t *testing.T, raw []byte) {
	t.Helper()
	if strings.Contains(string(raw), chatUITestSecret) {
		t.Fatalf("response contained secret: %s", raw)
	}
}

func TestKeysAPINeverReturnsSecret(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, chatUITestSecret)
	defer provider.Close()
	key := addSavedKey(t, ui, provider.URL, "sentinel")
	responses := [][]byte{}
	code, raw := ui.request(t, http.MethodGet, "/api/keys", nil)
	if code != http.StatusOK {
		t.Fatalf("list keys %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodGet, "/api/environment", nil)
	if code != http.StatusOK {
		t.Fatalf("environment %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/update", map[string]string{
		"name": "sentinel-renamed", "base_url": provider.URL, "note": "local",
	})
	if code != http.StatusOK {
		t.Fatalf("update key %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodPost, "/api/keys/test", map[string]string{"id": key.ID})
	if code != http.StatusOK {
		t.Fatalf("test key %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/models", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("key models %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
		"key": key.ID, "model": "mock-chat", "default": true,
	})
	if code != http.StatusOK {
		t.Fatalf("enable model %d %s", code, raw)
	}
	responses = append(responses, raw)
	var enabled struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &enabled); err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/models/"+enabled.ID+"/disable", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("disable model %d %s", code, raw)
	}
	responses = append(responses, raw)
	code, raw = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/delete", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("delete key %d %s", code, raw)
	}
	responses = append(responses, raw)
	for _, response := range responses {
		keyResponseHasSecret(t, response)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ui.http.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Key", ui.server.token)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || strings.Contains(line, chatUITestSecret) {
		t.Fatalf("SSE exposed secret: %q %v", line, err)
	}
}

func TestKeysAPIRejectsBadRequests(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	addNoneKey(t, ui, provider.URL, "stable")
	before, err := os.ReadFile(ui.tasks.Keys().Path())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, contentType, body, origin string
		token                           bool
		want                            int
	}{
		{"missing token", "application/json", `{}`, ui.http.URL, false, http.StatusUnauthorized},
		{"bad origin", "application/json", `{}`, "https://evil.example", true, http.StatusForbidden},
		{"not json", "text/plain", `{}`, ui.http.URL, true, http.StatusUnsupportedMediaType},
		{"unknown field", "application/json", `{"name":"x","base_url":"` + provider.URL + `","source":"none","unknown":true}`, ui.http.URL, true, http.StatusBadRequest},
		{"too large", "application/json", `{"name":"` + strings.Repeat("x", 1<<20) + `"}`, ui.http.URL, true, http.StatusBadRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			code, _ := requestRaw(t, ui, http.MethodPost, "/api/keys", test.contentType, test.body, test.token, test.origin)
			if code != test.want {
				t.Fatalf("status=%d want=%d", code, test.want)
			}
		})
	}
	after, err := os.ReadFile(ui.tasks.Keys().Path())
	if err != nil || string(before) != string(after) {
		t.Fatal("bad requests changed keys.json")
	}
}

func TestKeysUpdateEmptyKeyKeeps(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, chatUITestSecret)
	defer provider.Close()
	key := addSavedKey(t, ui, provider.URL, "keep")
	before, err := ui.tasks.Keys().Find(key.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/update", map[string]string{
		"name": "keep-renamed", "base_url": provider.URL, "source": "",
	})
	if code != http.StatusOK {
		t.Fatalf("update %d %s", code, raw)
	}
	var after harness.KeyProfileView
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after.Hint != before.Hint || after.Name != "keep-renamed" {
		t.Fatalf("key changed on empty update: before=%+v after=%+v", before, after)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/keys/test", map[string]string{"id": after.ID})
	if code != http.StatusOK || strings.Contains(string(raw), chatUITestSecret) {
		t.Fatalf("saved key test %d %s", code, raw)
	}
}

func TestKeysEnvProfileUpdateLimited(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	view, err := ui.tasks.Keys().Add(harness.KeyInput{
		Name: "env", BaseURL: provider.URL, Source: harness.KeySourceEnv, EnvName: "FA_CHATUI_ENV",
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]map[string]string{
		"source": {"name": "env", "base_url": provider.URL, "source": "env"},
		"key":    {"name": "env", "base_url": provider.URL, "key": "not-allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _ := ui.request(t, http.MethodPost, "/api/keys/"+view.ID+"/update", body)
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400", code)
			}
		})
	}
}

func TestKeysTestRecordsOnlySaved(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, chatUITestSecret)
	defer provider.Close()
	code, raw := ui.request(t, http.MethodPost, "/api/keys/test", map[string]string{"source": harness.KeySourceSaved})
	if code != http.StatusBadRequest || !strings.Contains(string(raw), "服务地址") {
		t.Fatalf("empty form test %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/keys/test", map[string]string{
		"base_url": provider.URL, "source": harness.KeySourceSaved, "key": chatUITestSecret,
	})
	if code != http.StatusOK {
		t.Fatalf("unsaved test %d %s", code, raw)
	}
	views, err := ui.tasks.Keys().List()
	if err != nil || len(views) != 0 {
		t.Fatalf("unsaved test persisted profile: %+v %v", views, err)
	}
	key := addSavedKey(t, ui, provider.URL, "record")
	code, raw = ui.request(t, http.MethodPost, "/api/keys/test", map[string]string{"id": key.ID})
	if code != http.StatusOK {
		t.Fatalf("saved test %d %s", code, raw)
	}
	view, err := ui.tasks.Keys().Find(key.ID)
	if err != nil || view.LastCheck == nil || view.LastCheck.Result != "ok" {
		t.Fatalf("saved test was not recorded: %+v %v", view, err)
	}
}

func TestKeysDeleteReportsAffectedModels(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	key := addNoneKey(t, ui, provider.URL, "used")
	code, raw := ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
		"key": key.ID, "model": "mock-chat", "default": true,
	})
	if code != http.StatusOK {
		t.Fatalf("enable %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/delete", map[string]any{})
	if code != http.StatusConflict {
		t.Fatalf("delete status=%d body=%s", code, raw)
	}
	var failure struct {
		Code     string   `json:"code"`
		Affected []string `json:"affected"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil || failure.Code != "key_in_use" ||
		len(failure.Affected) != 1 || failure.Affected[0] != "mock-chat" {
		t.Fatalf("delete conflict=%s %s", raw, err)
	}
	if _, err := ui.tasks.Keys().Find(key.ID); err != nil {
		t.Fatal("key was deleted after a refused delete")
	}
}

func TestModelsEnableFirstBecomesDefault(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	key := addNoneKey(t, ui, provider.URL, "models")
	code, raw := ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
		"key": key.ID, "model": "deepseek/chat v3",
	})
	if code != http.StatusOK {
		t.Fatalf("first enable %d %s", code, raw)
	}
	var first struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &first)
	if first.ID != "deepseek-chat-v3" || ui.tasks.Config().DefaultModel != first.ID {
		t.Fatalf("first model=%+v config=%+v", first, ui.tasks.Config())
	}
	code, raw = ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
		"key": key.ID, "model": "deepseek/chat v3",
	})
	if code != http.StatusOK {
		t.Fatalf("duplicate enable %d %s", code, raw)
	}
	var second struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &second)
	if second.ID != "deepseek-chat-v3-2" {
		t.Fatalf("duplicate model id=%q", second.ID)
	}
}

func TestKeyPresetsFollowV61Order(t *testing.T) {
	presets := harness.KeyPresets()
	want := []string{"OpenAI", "Anthropic", "Gemini", "xAI", "GLM", "Kimi", "MiniMax", "Muse", "DeepSeek"}
	if len(presets) != len(want) {
		t.Fatalf("preset count=%d want=%d", len(presets), len(want))
	}
	for index, name := range want {
		if presets[index].Name != name || presets[index].Protocol == "" || presets[index].Provider == "" {
			t.Fatalf("preset[%d]=%+v", index, presets[index])
		}
	}
	if presets[1].Protocol != harness.KeyProtocolAnthropic || presets[0].Protocol != harness.KeyProtocolOpenAICompatible {
		t.Fatalf("protocols=%q,%q", presets[0].Protocol, presets[1].Protocol)
	}
}

func TestModelsBatchAndDefaultDoNotReplaceCapabilities(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	key := addNoneKey(t, ui, provider.URL, "batch")
	code, raw := ui.request(t, http.MethodPost, "/api/models/enable-batch", map[string]any{
		"key": key.ID, "models": []string{"alpha", "beta"},
	})
	if code != http.StatusOK {
		t.Fatalf("batch %d %s", code, raw)
	}
	var result struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.IDs) != 2 {
		t.Fatalf("batch result=%s err=%v", raw, err)
	}
	if err := ui.tasks.ConfigureModels(t.Context(), func(config *harness.Config) error {
		model := config.Models[result.IDs[1]]
		model.Capabilities = harness.ModelCapabilities{Strength: "strong"}
		config.Models[result.IDs[1]] = model
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/models/default", map[string]string{"id": result.IDs[1]})
	if code != http.StatusOK {
		t.Fatalf("default %d %s", code, raw)
	}
	config := ui.tasks.Config()
	if config.DefaultModel != result.IDs[1] || config.Models[result.IDs[1]].Capabilities.Strength != "strong" {
		t.Fatalf("default changed model metadata: %+v", config)
	}
}

func TestModelsDisableDefaultNeedsNewDefault(t *testing.T) {
	ui := emptyKeyUI(t, harness.EchoModel{})
	provider := keyProbeServer(t, "")
	defer provider.Close()
	key := addNoneKey(t, ui, provider.URL, "models")
	for _, model := range []string{"one", "two"} {
		code, raw := ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
			"key": key.ID, "model": model, "default": model == "one",
		})
		if code != http.StatusOK {
			t.Fatalf("enable %s %d %s", model, code, raw)
		}
	}
	code, raw := ui.request(t, http.MethodPost, "/api/models/one/disable", map[string]any{})
	if code != http.StatusBadRequest {
		t.Fatalf("disable default=%d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/models/one/disable", map[string]any{"new_default": "two"})
	if code != http.StatusOK {
		t.Fatalf("disable with new default=%d %s", code, raw)
	}
	if config := ui.tasks.Config(); config.DefaultModel != "two" {
		t.Fatalf("default=%q", config.DefaultModel)
	}
}

func TestModelsChangeRejectedWhileRunning(t *testing.T) {
	started := make(chan struct{})
	ui := setupUI(t, fixtureModel(func(ctx context.Context, _ harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		return harness.ModelResponse{}, ctx.Err()
	}))
	provider := keyProbeServer(t, "")
	defer provider.Close()
	key := addNoneKey(t, ui, provider.URL, "during-run")
	chat := ui.create(t)
	code, raw := ui.request(t, http.MethodPost, "/api/chats/"+chat+"/submit", map[string]string{
		"prompt": "hold", "request_key": "hold",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit %d %s", code, raw)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	code, _ = ui.request(t, http.MethodPost, "/api/models/enable", map[string]any{
		"key": key.ID, "model": "during",
	})
	if code != http.StatusConflict {
		t.Fatalf("enable while running status=%d", code)
	}
	code, _ = ui.request(t, http.MethodPost, "/api/models/second/disable", map[string]any{})
	if code != http.StatusConflict {
		t.Fatalf("disable while running status=%d", code)
	}
	code, _ = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/update", map[string]string{
		"name": "during-run-updated", "base_url": provider.URL,
	})
	if code != http.StatusOK {
		t.Fatalf("key update while running status=%d", code)
	}
	code, _ = ui.request(t, http.MethodPost, "/api/keys/"+key.ID+"/delete", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("key delete while running status=%d", code)
	}
	_, _ = ui.request(t, http.MethodPost, "/api/chats/"+chat+"/stop", map[string]any{})
	ui.settled(t, chat)
}

func TestEnvironmentMarksMissingKey(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	err := ui.tasks.ConfigureModels(t.Context(), func(config *harness.Config) error {
		config.Models["missing"] = harness.ModelConfig{
			Provider: "openai-compatible", Key: "k-00000000", Model: "missing-model",
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, http.MethodGet, "/api/environment", nil)
	if code != http.StatusOK {
		t.Fatalf("environment %d %s", code, raw)
	}
	var payload struct {
		Models map[string]struct {
			Key     string `json:"key"`
			Missing bool   `json:"missing"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	model := payload.Models["missing"]
	if model.Key != "k-00000000" || !model.Missing {
		t.Fatalf("missing marker=%+v", model)
	}
}

func TestUserErrorKeyMessages(t *testing.T) {
	tests := []struct {
		err  error
		code string
		text string
	}{
		{harness.ErrKeyNotFound, "key_not_found", "Key"},
		{harness.ErrKeyNeedsReentry, "key_reentry", "重新输入"},
		{harness.ErrKeysBusy, "keys_busy", "稍后"},
		{harness.ErrKeyNameExists, "key_name_exists", "同名"},
		{harness.ErrKeysFormat, "keys_format", "格式"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			code, text := userError(test.err)
			if code != test.code || !strings.Contains(text, test.text) {
				t.Fatalf("got %s %q", code, text)
			}
		})
	}
	failure := &harness.CallError{Certain: true, Category: "authentication"}
	code, text := userError(failure)
	if code != "provider_auth" || !strings.Contains(text, "Key 管理") {
		t.Fatalf("authentication=%s %q", code, text)
	}
}
