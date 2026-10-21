package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureHTTPTools(origin string) *HTTPTools {
	return &HTTPTools{Config: HTTPConfig{Enabled: true, AllowedOrigins: []string{origin}, TimeoutSecs: 2, MaxOutput: 1024}}
}

func TestHTTPOptInOriginsAndInputValidation(t *testing.T) {
	tools := fixtureHTTPTools("https://docs.example")
	for _, raw := range []string{
		"http://docs.example/path", "https://other.example/path",
		"https://user:secret@docs.example/path", "https://docs.example/path#fragment",
		"https://docs.example\\@127.0.0.1/path", "https://docs.example/path\n",
		"https://10.0.0.1/path", "https://169.254.169.254/path",
		"https://[::ffff:10.0.0.1]/path", "file:///etc/passwd", "https://docs.example:/",
		"https://docs.example:0/", "https://docs.example:65536/", "https://docs.example:0443/",
	} {
		if _, err := tools.Prepare(t.Context(), makeCall("bad", "http_get", map[string]string{"url": raw})); err == nil {
			t.Errorf("accepted URL %q", raw)
		}
	}
	for _, raw := range []string{`null`, `{"url":null}`, `{}`, `{"url":"https://docs.example","headers":{}}`} {
		if _, err := tools.Prepare(t.Context(), ToolCall{Name: "http_get", Arguments: json.RawMessage(raw)}); err == nil {
			t.Errorf("accepted arguments %s", raw)
		}
	}
	call := makeCall("valid", "http_get", map[string]string{"url": "https://docs.example/help?q=exact"})
	prepared, err := tools.Prepare(t.Context(), call)
	if err != nil || prepared.Effect != EffectModify || !strings.Contains(prepared.Preview, "GET https://docs.example/help?q=exact") {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	tools.Config.Enabled = false
	if len(tools.Definitions()) != 0 {
		t.Fatal("disabled HTTP advertised")
	}
	if _, err := tools.Prepare(t.Context(), call); err == nil {
		t.Fatal("disabled HTTP accepted")
	}
	for _, origins := range [][]string{nil, {"https://docs.example/"}, {"https://docs.example?q=1"},
		{"https://user:secret@docs.example"}, {"http://remote.example"}, {"https://DOCS.example"},
		{"https://docs.example", "https://docs.example"}, {"https://10.0.0.1"}} {
		config := HTTPConfig{Enabled: true, AllowedOrigins: origins, TimeoutSecs: 2, MaxOutput: 1024}
		if config.Validate() == nil {
			t.Errorf("accepted origin policy %v", origins)
		}
	}
}

func TestRuntimeToolViewHotReloadsHTTPPolicy(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	files, err := NewFileTools(workspace, data)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	base := NewRuntimeTools(files)
	config := DefaultConfig(workspace)
	manager := NewToolRuntimeManager(base, config)
	for _, definition := range manager.Definitions() {
		if definition.Name == "http_get" {
			t.Fatal("HTTP was advertised while disabled")
		}
	}

	config.Tools.Network = true
	config.HTTP = HTTPConfig{Enabled: true, AllowedOrigins: []string{"https://docs.example"}, TimeoutSecs: 2, MaxOutput: 1024}
	manager.Replace(manager.Build(config))
	found := false
	for _, definition := range manager.Definitions() {
		if definition.Name == "http_get" {
			found = true
		}
	}
	if !found {
		t.Fatal("HTTP was not advertised after runtime policy replacement")
	}
	if _, err := manager.Prepare(t.Context(), makeCall("http", "http_get", map[string]string{"url": "https://docs.example/path"})); err != nil {
		t.Fatalf("reloaded HTTP policy did not prepare: %v", err)
	}
}

func TestHTTPAddressScope(t *testing.T) {
	for _, address := range []string{"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.1.1", "192.168.1.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "224.0.0.1", "240.0.0.1",
		"::", "::1", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "2001:db8::1", "2002::1", "64:ff9b::7f00:1"} {
		if publicHTTPAddress(netip.MustParseAddr(address)) {
			t.Errorf("special address permitted: %s", address)
		}
	}
	public := netip.MustParseAddr("8.8.8.8")
	local := netip.MustParseAddr("127.0.0.1")
	for _, test := range []struct {
		host string
		ips  []netip.Addr
		ok   bool
	}{
		{"docs.example", []netip.Addr{public}, true},
		{"docs.example", []netip.Addr{public, local}, false},
		{"docs.example", []netip.Addr{local}, false},
		{"localhost", []netip.Addr{local, netip.MustParseAddr("::1")}, true},
		{"localhost", []netip.Addr{public}, false},
		{"127.0.0.1", []netip.Addr{local}, true},
		{"docs.example", nil, false},
	} {
		if (validateHTTPAddresses(test.host, test.ips) == nil) != test.ok {
			t.Errorf("scope %s %v expected=%t", test.host, test.ips, test.ok)
		}
	}
}

func TestHTTPApprovalEvidenceAndRemoval(t *testing.T) {
	var requests atomic.Int32
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("FREEAGENT_HTTP_PRIVATE", "must-not-forward")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.String() != "/notes?selected=1" || r.Header.Get("Authorization") != "" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" ||
			strings.Contains(fmt.Sprint(r.Header), "must-not-forward") {
			t.Error("request changed or forwarded credentials")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "explicit project docs")
	}))
	defer server.Close()
	calls := 0
	service, files, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("get", "http_get", map[string]string{"url": server.URL + "/notes?selected=1"})}}, nil
		}
		return ModelResponse{Content: "All external operations succeeded (model claim)."}, nil
	}))
	tools := NewRuntimeTools(files)
	tools.HTTP = fixtureHTTPTools(server.URL)
	service.Config.HTTP = tools.HTTP.Config
	service.Tools = tools
	tasks := NewTaskService(service)
	waiting, err := tasks.Submit(t.Context(), "", "http", "read project docs")
	if err != nil || waiting.Run.Status != "waiting_confirmation" || requests.Load() != 0 {
		t.Fatalf("dispatched before approval: %+v err=%v", waiting, err)
	}
	decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil || requests.Load() != 0 {
		t.Fatal("preview made a request")
	}
	finished, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision, true)
	if err != nil || requests.Load() != 1 || len(finished.Evidence.HTTP) != 1 ||
		finished.Evidence.HTTP[0].HTTPStatus != 200 || finished.Evidence.HTTP[0].Status != "completed" {
		t.Fatalf("finished=%+v requests=%d err=%v", finished, requests.Load(), err)
	}
	duplicate, err := tasks.Submit(t.Context(), waiting.Run.ConversationID, "http", "read project docs")
	if err != nil || !duplicate.Duplicate || requests.Load() != 1 {
		t.Fatal("duplicate made another request")
	}
	tools.HTTP = nil
	service.Config.HTTP.Enabled = false
	service.Model = EchoModel{}
	if _, err := tasks.Submit(t.Context(), waiting.Run.ConversationID, "removed", "core remains usable"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("removal launched work")
	}
}

func TestHTTPBoundedResultsNoRedirectOrRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/must-not-follow", http.StatusFound)
		case "/large":
			fmt.Fprint(w, strings.Repeat("x", 4096))
		case "/binary":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0, 1, 2})
		case "/invalid":
			w.Write([]byte{0xff, 0xfe})
		case "/status":
			w.WriteHeader(503)
			fmt.Fprint(w, "unavailable")
		case "/partial":
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "partial")
		default:
			t.Error("redirect followed")
		}
	}))
	defer server.Close()
	tools := fixtureHTTPTools(server.URL)
	for _, path := range []string{"/redirect", "/large", "/binary", "/invalid", "/status", "/partial"} {
		t.Run(path, func(t *testing.T) {
			before := requests.Load()
			prepared, err := tools.Prepare(t.Context(), makeCall("get", "http_get", map[string]string{"url": server.URL + path}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := prepared.Execute(t.Context())
			if err != nil || !result.IsError || requests.Load() != before+1 || len(result.Content) > 2048 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			receipt := result.Receipts[0]
			if receipt.Status != "completed" || receipt.HTTPStatus == 0 ||
				receipt.Truncated != (path == "/large" || path == "/partial") {
				t.Fatalf("response facts lost: %+v", receipt)
			}
		})
	}
}

func TestHTTPTimeoutUnknownPersistsAndNeverReplays(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	service, files, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("timeout", "http_get", map[string]string{"url": server.URL + "/slow"})}}, nil
	}))
	tools := NewRuntimeTools(files)
	tools.HTTP = fixtureHTTPTools(server.URL)
	tools.HTTP.Config.TimeoutSecs = 1
	service.Tools, service.Config.HTTP = tools, tools.HTTP.Config
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	result, err := service.Run(t.Context(), conversation(t, service), "timeout", "slow request")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" || requests.Load() != 1 ||
		len(result.Evidence.HTTP) != 1 || result.Evidence.HTTP[0].Status != "unknown" {
		t.Fatalf("timeout became replayable: %+v err=%v requests=%d", result, err, requests.Load())
	}
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service.Store = store
	if _, err := service.Continue(t.Context(), result.Run.ID); !errors.Is(err, ErrUnknownResult) || requests.Load() != 1 {
		t.Fatal("reopened HTTP uncertainty replayed")
	}
}

func TestHTTPCancelAndDeclineDoNotDispatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	tools := fixtureHTTPTools(server.URL)
	prepared, err := tools.Prepare(t.Context(), makeCall("get", "http_get", map[string]string{"url": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := prepared.Execute(ctx)
	if err != nil || result.Receipts[0].Status != "not_dispatched" || requests.Load() != 0 {
		t.Fatalf("cancelled request=%+v err=%v", result, err)
	}
	if declined := prepared.Reject(); declined.Receipts[0].Status != "not_dispatched" || requests.Load() != 0 {
		t.Fatal("declined request dispatched")
	}
	_, err = prepared.ExecuteRecorded(t.Context(), func(ToolResult) error { return errors.New("record unavailable") })
	var certainty *CallError
	if !errors.As(err, &certainty) || !certainty.Certain || requests.Load() != 0 {
		t.Fatalf("unpersisted HTTP request was sent: %v", err)
	}
}

func TestHTTPCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	tools := fixtureHTTPTools(server.URL)
	prepared, err := tools.Prepare(t.Context(), makeCall("get", "http_get", map[string]string{"url": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := prepared.Execute(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		var certainty *CallError
		if !errors.As(err, &certainty) || certainty.Certain {
			t.Fatalf("caller cancellation lost uncertainty: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not propagate")
	}
}

func TestHTTPProgressDoesNotClaimCompleteBodyAtHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "bounded body")
	}))
	defer server.Close()
	tools := fixtureHTTPTools(server.URL)
	prepared, err := tools.Prepare(t.Context(), makeCall("get", "http_get", map[string]string{"url": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	var progress []ToolReceipt
	result, err := prepared.ExecuteRecorded(t.Context(), func(result ToolResult) error {
		progress = append(progress, result.Receipts[0])
		return nil
	})
	if err != nil || len(progress) != 2 || progress[0].Status != "unknown" ||
		progress[1].Status != "response_received" || progress[1].HTTPStatus != 200 ||
		progress[1].Detail == "" || result.Receipts[0].Status != "completed" ||
		result.Receipts[0].Detail != "" || result.IsError {
		t.Fatalf("premature completion: progress=%+v result=%+v err=%v", progress, result, err)
	}
}
