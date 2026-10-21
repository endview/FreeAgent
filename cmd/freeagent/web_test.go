package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

type launchOutput chan string

func (output launchOutput) Write(raw []byte) (int, error) {
	output <- strings.TrimSpace(string(raw))
	return len(raw), nil
}

func startTestWeb(t *testing.T, data string) (string, string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	output, done := make(launchOutput, 1), make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"web", "--data-dir", data}, strings.NewReader(""), output, io.Discard)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("web shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("web did not close its listener and runtime")
		}
	}
	t.Cleanup(stop)
	select {
	case launch := <-output:
		address, err := url.Parse(launch)
		if err != nil || address.Scheme != "http" || address.Hostname() != "127.0.0.1" ||
			net.ParseIP(address.Hostname()) == nil || address.Port() == "" {
			t.Fatalf("invalid private loopback launch address")
		}
		token := strings.TrimPrefix(address.Fragment, "key=")
		if len(token) != 64 {
			t.Fatal("missing random launch token")
		}
		address.Fragment = ""
		return strings.TrimRight(address.String(), "/"), token, stop
	case err := <-done:
		stopped = true
		cancel()
		t.Fatalf("web exited before launch: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("web did not start")
	}
	return "", "", stop
}

func webRequest(t *testing.T, origin, token, path, body string) (int, []byte) {
	t.Helper()
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Key", token)
	request.Header.Set("Origin", origin)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
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

func TestCLIWebLifecycleAndRestart(t *testing.T) {
	data, _ := initCLI(t)
	origin, token, stop := startTestWeb(t, data)
	if code, raw := webRequest(t, origin, "", "/", ""); code != 200 ||
		!strings.Contains(string(raw), "<title>FreeAgent Chat</title>") {
		t.Fatal("CLI did not serve embedded Chat assets")
	}
	if code, _ := webRequest(t, origin, "", "/api/environment", ""); code != 401 {
		t.Fatal("unauthenticated API accepted")
	}
	if code, _ := webRequest(t, origin, token, "/api/connect", "{}"); code != 200 {
		t.Fatal("launch authorization failed")
	}
	code, raw := webRequest(t, origin, token, "/api/environment", "")
	var before struct {
		Environment string `json:"environment"`
	}
	if code != 200 || json.Unmarshal(raw, &before) != nil || before.Environment == "" {
		t.Fatal("environment identity missing")
	}
	code, raw = webRequest(t, origin, token, "/api/chats", `{"title":"Web lifecycle"}`)
	var chat harness.Conversation
	if code != 201 || json.Unmarshal(raw, &chat) != nil || chat.ID == "" {
		t.Fatal("new Chat failed")
	}
	path := "/api/chats/" + chat.ID
	if code, _ := webRequest(t, origin, token, path+"/submit",
		`{"prompt":"multi-line\n中文","request_key":"cli-web-restart"}`); code != 202 {
		t.Fatal("foreground submit failed")
	}
	var saved harness.ChatView
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, raw = webRequest(t, origin, token, path, "")
		var snapshot struct {
			View harness.ChatView `json:"view"`
			Busy bool             `json:"busy"`
		}
		if code != 200 || json.Unmarshal(raw, &snapshot) != nil {
			t.Fatal("invalid snapshot")
		}
		if !snapshot.Busy && len(snapshot.View.Turns) == 1 && snapshot.View.Turns[0].State == "completed" {
			saved = snapshot.View
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(saved.Turns) != 1 || len(saved.Turns[0].Messages) != 2 ||
		saved.Turns[0].Messages[1].Raw != "Echo: multi-line\n中文" {
		t.Fatal("Chat did not preserve multiline completion")
	}
	stop()
	parsed, _ := url.Parse(origin)
	if connection, err := net.DialTimeout("tcp4", parsed.Host, time.Second); err == nil {
		connection.Close()
		t.Fatal("shutdown left listener running")
	}
	origin2, token2, stop2 := startTestWeb(t, data)
	defer stop2()
	if token2 == token {
		t.Fatal("restart reused launch credential")
	}
	if code, _ := webRequest(t, origin2, token, path, ""); code != 401 {
		t.Fatal("old launch token survived restart")
	}
	code, raw = webRequest(t, origin2, token2, "/api/environment", "")
	var after struct {
		Environment string `json:"environment"`
	}
	if code != 200 || json.Unmarshal(raw, &after) != nil || after.Environment != before.Environment {
		t.Fatal("port restart changed persistent environment identity")
	}
	code, raw = webRequest(t, origin2, token2, path, "")
	var reopened struct {
		View harness.ChatView `json:"view"`
	}
	if code != 200 || json.Unmarshal(raw, &reopened) != nil ||
		len(reopened.View.Turns) != 1 || reopened.View.Turns[0].Key != saved.Turns[0].Key ||
		reopened.View.Turns[0].State != "completed" {
		t.Fatal("restart lost or replayed Chat history")
	}
}

func TestCLIWriteAttachesToExistingWebOwnerAndDeduplicates(t *testing.T) {
	data, _ := initCLI(t)
	_, _, stop := startTestWeb(t, data)
	defer stop()
	raw, stderr, err := invoke(t, "", "run", "--data-dir", data, "--json", "--request", "attached-request", "hello from cli")
	if err != nil {
		t.Fatalf("attached run error=%v stderr=%q raw=%q", err, stderr, raw)
	}
	var first harness.RunResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil || first.Run.Status != "completed" || first.Answer != "Echo: hello from cli" {
		t.Fatalf("attached result=%+v err=%v raw=%q", first, err, raw)
	}
	raw, stderr, err = invoke(t, "", "run", "--data-dir", data, "--json", "--session", first.Run.ConversationID,
		"--request", "attached-request", "hello from cli")
	if err != nil {
		t.Fatalf("attached duplicate error=%v stderr=%q raw=%q", err, stderr, raw)
	}
	var duplicate harness.RunResult
	if err := json.Unmarshal([]byte(raw), &duplicate); err != nil || !duplicate.Duplicate || duplicate.Run.ID != first.Run.ID {
		t.Fatalf("attached duplicate=%+v err=%v raw=%q", duplicate, err, raw)
	}
}

func TestCLIWebOwnerHandoffReleasesLockAndPromotesNextOwner(t *testing.T) {
	data, _ := initCLI(t)
	_, _, stop := startTestWeb(t, data)
	info, attachSecret, _, err := handoff.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	client, err := handoff.NewClient(info, attachSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Handoff(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, readErr := handoff.Read(data); readErr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if _, _, _, err := handoff.Read(data); err == nil {
		t.Fatal("owner discovery remained after handoff")
	}
	info2, attachSecret2, _, err := handoff.Read(data)
	if err == nil {
		t.Fatal("unexpected owner discovery before promotion")
	}
	_, _, stop2 := startTestWeb(t, data)
	defer stop2()
	info2, attachSecret2, _, err = handoff.Read(data)
	if err != nil || info2.Generation == info.Generation || attachSecret2 == attachSecret {
		t.Fatalf("next owner did not publish a fresh generation: info=%+v err=%v", info2, err)
	}
}

func TestCLIControlReadsAndAppliesThroughExistingWebOwner(t *testing.T) {
	data, _ := initCLI(t)
	_, _, stop := startTestWeb(t, data)
	defer stop()
	raw, stderr, err := invoke(t, "", "control", "state", "--data-dir", data, "--json")
	if err != nil || stderr != "" {
		t.Fatalf("attached control state err=%v stderr=%q raw=%q", err, stderr, raw)
	}
	var state harness.ControlState
	if err := json.Unmarshal([]byte(raw), &state); err != nil || state.Generation == "" {
		t.Fatalf("attached control state=%+v err=%v", state, err)
	}
	info, _, chatSecret, err := handoff.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	client, err := handoff.NewInteractiveClient(info, chatSecret)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := client.ControlPlan(t.Context(), harness.ControlSetPermission, json.RawMessage(`{"mode":"full"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := client.ControlConfirmation(t.Context(), operation.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ControlConfirm(t.Context(), operation.ID, "", confirmation.Code)
	if err != nil || !result.Applied {
		t.Fatalf("attached control apply=%+v err=%v", result, err)
	}
	state, err = client.ControlState(t.Context())
	if err != nil || state.Config.PermissionMode != string(harness.PermissionFull) {
		t.Fatalf("attached control state after apply=%+v err=%v", state, err)
	}
}

func TestCLIKeyMutationsAttachToExistingWebOwner(t *testing.T) {
	data, _ := initCLI(t)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer attach-key" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"data":[{"id":"attach-model"}]}`)
	}))
	defer provider.Close()
	_, _, stop := startTestWeb(t, data)
	defer stop()
	raw, stderr, err := invoke(t, "attach-key\n", "key", "add", "--data-dir", data, "--name", "attached-key", "--url", provider.URL+"/v1", "--key-stdin")
	if err != nil {
		t.Fatalf("attached key add err=%v stderr=%q raw=%q", err, stderr, raw)
	}
	var view harness.KeyProfileView
	if err := json.Unmarshal([]byte(raw), &view); err != nil || view.Name != "attached-key" {
		t.Fatalf("attached key add output=%q view=%+v err=%v", raw, view, err)
	}
	raw, stderr, err = invoke(t, "", "key", "models", "--data-dir", data, view.ID)
	if err != nil || raw != "attach-model\n" {
		t.Fatalf("attached key models err=%v stderr=%q raw=%q", err, stderr, raw)
	}
	raw, stderr, err = invoke(t, "", "key", "remove", "--data-dir", data, view.ID)
	if err != nil {
		t.Fatalf("attached key remove err=%v stderr=%q raw=%q", err, stderr, raw)
	}
	stored, err := keyStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stored.Find(view.ID); !errors.Is(err, harness.ErrKeyNotFound) {
		t.Fatalf("attached key remained: %v", err)
	}
}

func TestCLIWebRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"--port", "-1"}, {"--port", "65536"}, {"extra"}, {"--listen", "0.0.0.0"}} {
		if err := runWeb(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard); exitCode(err) != exitUsage {
			t.Fatalf("unsafe or invalid web arguments accepted: %v", args)
		}
	}
}

func TestWebStartsWithZeroModels(t *testing.T) {
	data, _ := initEmptyCLI(t)
	base, token, stop := startTestWeb(t, data)
	defer stop()
	code, raw := webRequest(t, base, token, "/api/environment", "")
	if code != 200 {
		t.Fatalf("environment status=%d body=%s", code, raw)
	}
}

func TestWebExitsOnStdinClose(t *testing.T) {
	data, _ := initCLI(t)
	stdin, closeStdin := io.Pipe()
	defer closeStdin.Close()
	output, done := make(launchOutput, 1), make(chan error, 1)
	go func() {
		done <- run(t.Context(), []string{"web", "--data-dir", data, "--exit-on-stdin-close"},
			stdin, output, io.Discard)
	}()
	select {
	case <-output:
	case err := <-done:
		t.Fatalf("web exited before launch: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("web did not start")
	}
	if err := closeStdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("web shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("web did not stop after stdin close")
	}
}

func TestCLIWebBrowserOpeningIsOptionalAndFailureKeepsServer(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "automatic"}[open], func(t *testing.T) {
			data, _ := initCLI(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			args := []string{"--data-dir", data}
			if open {
				args = append(args, "--open-browser")
			}
			output, done := make(launchOutput, 1), make(chan error, 1)
			opened := make(chan string, 1)
			var stderr bytes.Buffer
			go func() {
				done <- runWebWithBrowser(ctx, args, strings.NewReader(""), output, &stderr, func(address string) error {
					opened <- address
					return errors.New("no browser is installed")
				})
			}()
			select {
			case address := <-output:
				parsed, err := url.Parse(address)
				if err != nil {
					t.Fatal(err)
				}
				token := strings.TrimPrefix(parsed.Fragment, "key=")
				parsed.Fragment = ""
				if open {
					select {
					case got := <-opened:
						if got != address {
							t.Fatal("browser received a different launch address")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("browser was not called")
					}
				}
				if code, _ := webRequest(t, strings.TrimRight(parsed.String(), "/"), token, "/api/environment", ""); code != 200 {
					t.Fatal("browser failure stopped the Chat server")
				}
			case err := <-done:
				t.Fatalf("server did not launch: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("server did not launch")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("server did not stop")
			}
			if !open && len(opened) != 0 {
				t.Fatal("normal CLI opened a browser without asking")
			}
			if open && !strings.Contains(stderr.String(), "Open the launch address above manually.") {
				t.Fatal("browser failure did not provide a manual launch fallback")
			}
		})
	}
}
