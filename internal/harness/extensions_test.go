package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/buildinfo"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func runExtensionHelper() int {
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 1024), 16<<20)
	operationDecision := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--freeagent-operation-check=") {
			operationDecision = strings.TrimPrefix(arg, "--freeagent-operation-check=")
		}
	}
	for reader.Scan() {
		message, err := jsonrpc.DecodeMessage(reader.Bytes())
		if err != nil {
			return 2
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok {
			continue
		}
		if !request.ID.IsValid() {
			continue
		}
		var result any
		var errorReply *jsonrpc.Error
		switch request.Method {
		case "initialize":
			var params map[string]any
			if json.Unmarshal(request.Params, &params) != nil ||
				params["protocol"] != extensionProtocol ||
				params["host_version"] != buildinfo.Version ||
				params["workspace"] == "" || params["data_dir"] == "" {
				return 3
			}
			events := []string(nil)
			provides := []string{"context"}
			if operationDecision != "" {
				events = []string{"operation.check"}
				provides = nil
			}
			result = map[string]any{"id": "com.example.test", "version": "1.0.0",
				"provides": provides, "events": events}
		case "context.prepare":
			var params map[string]any
			if json.Unmarshal(request.Params, &params) != nil {
				return 8
			}
			if params["phase"] == "step" {
				step, ok := params["step"].(float64)
				if !ok || step < 0 || step != float64(int(step)) {
					return 9
				}
				result = map[string]any{"blocks": []map[string]any{{
					"id": fmt.Sprintf("step-%d", int(step)), "text": fmt.Sprintf("extension step %d context", int(step)),
					"placement": "tail", "label": "Step", "priority": 75,
				}}}
			} else {
				result = map[string]any{"blocks": []map[string]any{
					{"id": "system", "text": "extension system context", "placement": "system", "label": "System", "priority": 100},
					{"id": "tail", "text": "extension tail context", "placement": "tail", "label": "Tail", "priority": 50},
				}}
			}
		case "operation.check":
			if operationDecision == "" {
				return 10
			}
			var params map[string]any
			if json.Unmarshal(request.Params, &params) != nil ||
				params["kind"] != "tool_call" ||
				params["target"] != "file_external" ||
				params["preview"] == "" ||
				params["parameters"] == nil ||
				params["items"] == nil {
				return 12
			}
			switch operationDecision {
			case "allow", "ask", "deny":
				result = map[string]any{"decision": operationDecision, "reason": "fixture decision"}
			case "bad":
				result = map[string]any{"decision": "invalid"}
			case "rpcerror":
				errorReply = &jsonrpc.Error{Code: -32603, Message: "fixture operation.check failure"}
			case "hang":
				time.Sleep(5 * time.Second)
				result = map[string]any{"decision": "allow"}
			default:
				return 11
			}
		case "turn.finished":
			var params map[string]any
			if json.Unmarshal(request.Params, &params) != nil ||
				params["output"] != "finished output" || params["status"] != "completed" ||
				params["data_dir"] == "" {
				return 7
			}
			result = map[string]any{"activity": map[string]any{
				"summary": "extension activity recorded",
				"items":   []string{"finished output"},
			}}
		case "shutdown":
			result = nil
		default:
			result = map[string]any{"ok": true}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return 4
		}
		response := &jsonrpc.Response{ID: request.ID, Result: raw}
		if errorReply != nil {
			response = &jsonrpc.Response{ID: request.ID, Error: errorReply}
		}
		encoded, err := jsonrpc.EncodeMessage(response)
		if err != nil {
			return 5
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s\n", encoded); err != nil {
			return 6
		}
	}
	return 0
}

func TestBoundExtensionBlocksPreservesEmptyJSONArray(t *testing.T) {
	blocks := boundExtensionBlocks([]ExtensionBlock{}, 1200)
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("empty extension blocks JSON = %s, want []", raw)
	}
}

func runExtensionHostHelper() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 16<<20)
	reentrant := false
	for _, arg := range os.Args {
		reentrant = reentrant || arg == "--freeagent-reentrant-tool-call"
	}
	reply := func(response *jsonrpc.Response) bool {
		encoded, err := jsonrpc.EncodeMessage(response)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(os.Stdout, "%s\n", encoded)
		return err == nil
	}
	nextID := 0
	hostCall := func(method string, params map[string]any) (map[string]any, bool) {
		nextID++
		id := fmt.Sprintf("host-%d", nextID)
		requestID, err := jsonrpc.MakeID(id)
		if err != nil {
			return nil, false
		}
		request := &jsonrpc.Request{ID: requestID, Method: method}
		request.Params, _ = json.Marshal(params)
		encoded, err := jsonrpc.EncodeMessage(request)
		if err != nil {
			return nil, false
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s\n", encoded); err != nil {
			return nil, false
		}
		for scanner.Scan() {
			message, err := jsonrpc.DecodeMessage(scanner.Bytes())
			if err != nil {
				return nil, false
			}
			// Like the Python SDK, a handler blocked on a Host call cannot
			// serve a nested Harness request.
			if nested, ok := message.(*jsonrpc.Request); ok {
				if nested.ID.IsValid() && !reply(&jsonrpc.Response{ID: nested.ID,
					Error: &jsonrpc.Error{Code: -32601, Message: "method not found"}}) {
					return nil, false
				}
				continue
			}
			response, ok := message.(*jsonrpc.Response)
			if !ok || fmt.Sprintf("%T:%v", response.ID.Raw(), response.ID.Raw()) !=
				fmt.Sprintf("%T:%v", request.ID.Raw(), request.ID.Raw()) {
				continue
			}
			if response.Error != nil || len(response.Result) == 0 {
				return nil, false
			}
			var result map[string]any
			if json.Unmarshal(response.Result, &result) != nil {
				return nil, false
			}
			return result, true
		}
		return nil, false
	}
	for scanner.Scan() {
		message, err := jsonrpc.DecodeMessage(scanner.Bytes())
		if err != nil {
			return 2
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok || !request.ID.IsValid() {
			continue
		}
		var params map[string]any
		_ = json.Unmarshal(request.Params, &params)
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"id": "com.example.host", "version": "1.0.0",
				"provides":  []string{"context", "workflow", "tools"},
				"workflows": []string{"summarize", "large", "wait"},
			}
			if reentrant {
				result = map[string]any{"id": "com.example.host", "version": "1.0.0",
					"provides": []string{"context"}, "events": []string{"operation.check"}}
			}
		case "context.prepare":
			invocation, _ := params["invocation"].(string)
			if reentrant {
				text := "reentrant tool.call failed"
				if tool, ok := hostCall("tool.call", map[string]any{
					"invocation": invocation, "name": "file_external", "arguments": map[string]any{},
				}); ok {
					content, _ := tool["content"].(string)
					text = "reentrant:" + content
				}
				result = map[string]any{"blocks": []map[string]any{{"id": "reentrant", "text": text, "placement": "tail"}}}
				break
			}
			model, ok := hostCall("model.generate", map[string]any{
				"invocation": invocation,
				"model":      "current",
				"messages":   []map[string]string{{"role": "user", "content": "host request"}},
				"max_tokens": 32,
			})
			if !ok {
				return 3
			}
			_, ok = hostCall("activity.report", map[string]any{
				"invocation": invocation,
				"summary":    "host activity",
				"items":      []string{"model.generate completed"},
			})
			if !ok {
				return 4
			}
			result = map[string]any{"blocks": []map[string]any{
				{"id": "host", "text": model["content"], "placement": "tail"},
			}}
		case "workflow.run":
			name, _ := params["name"].(string)
			input, _ := params["input"].(map[string]any)
			if name == "large" {
				result = map[string]any{"output": strings.Repeat("x", maxExtensionBlockChars+1)}
				break
			}
			if name == "wait" {
				time.Sleep(2 * time.Second)
			}
			text, _ := input["text"].(string)
			result = map[string]any{
				"output": "workflow:" + text,
				"activity": map[string]any{
					"summary": "workflow completed",
					"items":   []string{"summarize"},
				},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "Echo the supplied text",
					"inputSchema": map[string]any{
						"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required": []string{"text"}, "additionalProperties": false,
					}},
			}}
		case "tool.call":
			arguments, _ := params["arguments"].(map[string]any)
			text, _ := arguments["text"].(string)
			result = map[string]any{
				"content":  "tool:" + text,
				"activity": map[string]any{"summary": "tool completed"},
			}
		case "operation.check":
			result = map[string]any{"decision": "allow"}
		case "shutdown":
			result = nil
		default:
			result = map[string]any{}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return 5
		}
		response := &jsonrpc.Response{ID: request.ID, Result: raw}
		encoded, err := jsonrpc.EncodeMessage(response)
		if err != nil {
			return 6
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s\n", encoded); err != nil {
			return 7
		}
	}
	return 0
}

func TestExtensionTurnFinishedEmitsBoundedActivity(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.test"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.test",
		"version":"1.0.0",
		"name":"Finished extension",
		"description":"A bounded finish callback",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper"]},
		"needs":["message","output","status","workspace"],
		"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]},
		"events":["turn.finished"]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })

	activity, err := service.extensions.finish(t.Context(), extensionID,
		Run{ID: "run-finished", Prompt: "hello", Output: "finished output", Status: "completed"},
		"completed", []ModelMessage{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if activity["summary"] != "extension activity recorded" {
		t.Fatalf("activity=%+v", activity)
	}
}

func TestExtensionWorkflowParamsHonorNeeds(t *testing.T) {
	history := []ModelMessage{
		{Role: "user", Content: "old user"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current user"},
		{Role: "tool", Content: "tool result"},
	}
	params := extensionWorkflowParams("invocation", "run", map[string]any{"text": "hello"},
		Run{Prompt: "prompt", Output: "secret output", Status: "completed"},
		history, nil, `C:\workspace`, map[string]any{"mode": "test"})
	for _, key := range []string{"message", "history", "workspace", "tool_results", "output", "status"} {
		if _, ok := params[key]; ok {
			t.Fatalf("workflow received undeclared %s: %+v", key, params)
		}
	}
	if params["settings"] == nil {
		t.Fatal("workflow lost extension settings")
	}

	params = extensionWorkflowParams("invocation", "run", map[string]any{"text": "hello"},
		Run{Prompt: "prompt", Output: "workflow output", Status: "completed"},
		history, []string{"message", "history:1", "workspace", "tool_results", "output", "status"},
		`C:\workspace`, map[string]any{"mode": "test"})
	if got, ok := params["message"].(map[string]any); !ok || got["content"] != "prompt" {
		t.Fatalf("message=%+v", params["message"])
	}
	if got, ok := params["workspace"].(string); !ok || got != `C:\workspace` {
		t.Fatalf("workspace=%v", params["workspace"])
	}
	if got, ok := params["output"].(string); !ok || got != "workflow output" {
		t.Fatalf("output=%v", params["output"])
	}
	if got, ok := params["status"].(string); !ok || got != "completed" {
		t.Fatalf("status=%v", params["status"])
	}
	historyValue, ok := params["history"].([]ModelMessage)
	if !ok || len(historyValue) != 2 || historyValue[0].Content != "current user" ||
		historyValue[1].Role != "tool" {
		t.Fatalf("history=%+v", params["history"])
	}
	results, ok := params["tool_results"].([]ModelMessage)
	if !ok || len(results) != 1 || results[0].Content != "tool result" {
		t.Fatalf("tool_results=%+v", params["tool_results"])
	}
}

func TestExtensionHistoryIsBoundedAndUTF8Safe(t *testing.T) {
	history := []ModelMessage{
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: strings.Repeat("x", maxExtensionHistoryBytes)},
		{Role: "user", Content: "new"},
	}
	selected := selectExtensionHistory(history, 2)
	if len(selected) == 0 {
		t.Fatal("history was discarded")
	}
	raw, err := json.Marshal(selected)
	if err != nil || len(raw) > maxExtensionHistoryBytes {
		t.Fatalf("bounded history bytes=%d err=%v", len(raw), err)
	}
	for _, message := range selected {
		if !utf8.ValidString(message.Content) {
			t.Fatalf("invalid UTF-8 message=%+v", message)
		}
	}
	if selected[len(selected)-1].Content != "new" {
		t.Fatalf("latest history message lost: %+v", selected)
	}

	utf8History := []ModelMessage{{Role: "user", Content: strings.Repeat("界", maxExtensionHistoryBytes)}}
	selected = selectExtensionHistory(utf8History, 1)
	if len(selected) != 1 || !utf8.ValidString(selected[0].Content) ||
		len([]byte(selected[0].Content)) > maxExtensionHistoryBytes {
		t.Fatalf("UTF-8 history=%+v", selected)
	}
}

func TestExtensionHostModelAndActivityCallsUseInvocationBoundary(t *testing.T) {
	var purposes []string
	model := modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		purposes = append(purposes, request.Purpose)
		content := "main response"
		if request.Purpose != "" {
			content = "host response"
		}
		if emit != nil {
			emit(content)
		}
		return ModelResponse{Content: content, Usage: &ModelUsage{OutputTokens: 2}}, nil
	})
	service, _, workspace := testHarness(t, model)
	conversationID := conversation(t, service)
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Host API extension",
		"description":"Calls the bounded Host API",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]},
		"budget":{"model_calls":2,"output_tokens":10}
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.Config.Extensions.MaxModelCallsPerTurn = 2
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	var events []Event
	service.Emit = func(event Event) { events = append(events, event) }

	result, err := service.Run(t.Context(), conversationID, "host-api", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "main response" || result.Run.Status != "completed" {
		t.Fatalf("result=%+v", result)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundHost := false
	for _, attempt := range attempts {
		if attempt.Kind != "model" {
			continue
		}
		var request ModelRequest
		if json.Unmarshal([]byte(attempt.RequestJSON), &request) == nil &&
			request.Purpose == "ext:com.example.host/model.generate" {
			foundHost = true
		}
	}
	if !foundHost || len(purposes) != 2 {
		t.Fatalf("purposes=%v attempts=%+v", purposes, attempts)
	}
	foundActivity := false
	for _, event := range events {
		if event.Kind == "extension_activity" && event.Message == "host activity" {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("events=%+v", events)
	}
}

func TestAgentCallableWorkflowUsesNormalToolAttempt(t *testing.T) {
	const extensionID = "com.example.host"
	service, _, workspace := testHarness(t, nil)
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Agent callable workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]},
		"workflows":[{"name":"summarize","description":"Summarize input",
			"input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := "ext_" + contentHash([]byte(extensionID + "\x00workflow\x00summarize"))[:24]
	calls := 0
	var events []Event
	service.Emit = func(event Event) { events = append(events, event) }
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose != "" {
			return ModelResponse{Content: "host context"}, nil
		}
		calls++
		if calls == 1 {
			found := false
			for _, definition := range request.Tools {
				found = found || definition.Name == alias
			}
			if !found {
				t.Fatalf("workflow tool was not advertised: %+v events=%+v", request.Tools, events)
			}
			raw, _ := json.Marshal(map[string]string{"text": "from-agent"})
			return ModelResponse{ToolCalls: []ToolCall{{ID: "workflow-call", Name: alias, Arguments: raw}}}, nil
		}
		return ModelResponse{Content: "agent done"}, nil
	})
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.Refresh = func() (Config, error) { return service.Config, nil }
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	result, err := service.Run(t.Context(), conversation(t, service), "agent-workflow", "run workflow")
	if err != nil || result.Run.Status != "completed" || result.Answer != "agent done" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var workflowTool bool
	for _, attempt := range attempts {
		if attempt.Kind == "tool" && strings.Contains(attempt.RequestJSON, alias) && attempt.Status == "finished" {
			workflowTool = true
		}
	}
	if !workflowTool {
		t.Fatalf("workflow tool attempt missing: %+v", attempts)
	}
}

type embeddingFixtureModel struct{}

func (embeddingFixtureModel) Generate(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
	return ModelResponse{Content: "fixture"}, nil
}

func (embeddingFixtureModel) Embed(ctx context.Context, _ string, inputs []string) (EmbeddingResponse, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingResponse{}, err
	}
	vectors := make([][]float64, len(inputs))
	for index := range inputs {
		vectors[index] = []float64{float64(index), float64(len(inputs))}
	}
	return EmbeddingResponse{Vectors: vectors, Usage: &ModelUsage{InputTokens: int64(len(inputs)),
		TotalTokens: int64(len(inputs))}}, nil
}

func TestExtensionHostEmbeddingUsesModelAttemptBoundary(t *testing.T) {
	service, _, workspace := testHarness(t, embeddingFixtureModel{})
	service.Config.Models["default"] = ModelConfig{Provider: "echo",
		Capabilities: ModelCapabilities{Embeddings: "supported"}}
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	conversationID := conversation(t, service)
	run, created, err := service.Store.CreateRun(t.Context(), conversationID,
		"embedding", "embed this", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.embed", service, run,
		ExtensionManifest{ID: "com.example.embed", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)
	request := &jsonrpc.Request{Method: "model.embed"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id, "model": "current", "inputs": []string{"a", "bb"},
	})
	result, err := host.handleHostRequest(t.Context(), "com.example.embed", request)
	if err != nil {
		t.Fatal(err)
	}
	resultObject, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("embedding host result type=%T value=%+v", result, result)
	}
	vectors, ok := resultObject["vectors"].([][]float64)
	if !ok || len(vectors) != 2 || vectors[1][0] != 1 {
		t.Fatalf("embedding host result=%+v", result)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Kind != "model" ||
		attempts[0].Status != "finished" {
		t.Fatalf("embedding attempts=%+v err=%v", attempts, err)
	}
	var saved ModelRequest
	if err := json.Unmarshal([]byte(attempts[0].RequestJSON), &saved); err != nil ||
		saved.Purpose != "ext:com.example.embed/model.embed" ||
		len(saved.Inputs) != 2 {
		t.Fatalf("embedding request=%+v err=%v", saved, err)
	}
}

func TestExtensionHostToolCallUsesUnifiedAttemptAndPermissionBoundary(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID, "host-tool",
		"host tool", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.host", service, run,
		ExtensionManifest{ID: "com.example.host", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)

	request := &jsonrpc.Request{Method: "tool.call"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id,
		"name":       "file_read",
		"args":       map[string]any{"path": "note.txt"},
	})
	result, err := host.handleHostRequest(t.Context(), "com.example.host", request)
	if err != nil {
		t.Fatal(err)
	}
	reply, ok := result.(map[string]any)
	if !ok || reply["is_error"] != false || !strings.Contains(reply["content"].(string), "hello") {
		t.Fatalf("tool reply=%T %+v", result, result)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Kind != "tool" ||
		attempts[0].Status != "finished" {
		t.Fatalf("tool attempts=%+v err=%v", attempts, err)
	}

	service.permission = &PermissionSelection{Mode: PermissionFull, Source: "turn"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id,
		"name":       "file_create",
		"arguments":  map[string]any{"path": "created.txt", "content": "created by host"},
	})
	if _, err := host.handleHostRequest(t.Context(), "com.example.host", request); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(workspace, "created.txt")); err != nil ||
		string(raw) != "created by host" {
		t.Fatalf("host create raw=%q err=%v", raw, err)
	}
}

func TestExtensionHostToolCallRejectsModifyWithoutApprovalHandler(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID, "host-tool-reject",
		"host tool", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.host", service, run,
		ExtensionManifest{ID: "com.example.host", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)
	request := &jsonrpc.Request{Method: "tool.call"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id,
		"name":       "file_create",
		"arguments":  map[string]any{"path": "rejected.txt", "content": "must not write"},
	})
	result, err := host.handleHostRequest(t.Context(), "com.example.host", request)
	if err != nil {
		t.Fatal(err)
	}
	reply, ok := result.(map[string]any)
	if !ok || reply["is_error"] != true ||
		!strings.Contains(reply["content"].(string), "approval handler is unavailable") {
		t.Fatalf("rejection reply=%T %+v", result, result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "rejected.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected host tool created a file: %v", err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "finished" {
		t.Fatalf("rejection attempts=%+v err=%v", attempts, err)
	}
}

func TestExtensionWorkflowUsesDeclaredSchemaAndReturnsActivity(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[
			{"name":"summarize","description":"Summarize input","agent_callable":true,
			 "input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}},
			{"name":"large","description":"Oversized output","input_schema":{"type":"object"}}
		]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = host.close() })

	output, activity, err := host.workflow(t.Context(), extensionID, "summarize",
		Run{ID: "workflow-run", Prompt: "summarize this"}, map[string]any{"text": "hello"},
		nil, service)
	if err != nil {
		t.Fatal(err)
	}
	if output != "workflow:hello" || activity["summary"] != "workflow completed" {
		t.Fatalf("output=%q activity=%+v", output, activity)
	}
}

func TestServiceRunExtensionWorkflowUsesRunSnapshot(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[{"name":"summarize","description":"Summarize input",
			"input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID, "workflow-request",
		"workflow prompt", snapshot)
	if err != nil || !created {
		t.Fatalf("create run=%+v created=%v err=%v", run, created, err)
	}

	output, activity, err := service.RunExtensionWorkflow(t.Context(), run, extensionID,
		"summarize", map[string]any{"text": "from run"})
	if err != nil {
		t.Fatal(err)
	}
	if output != "workflow:from run" || activity["summary"] != "workflow completed" {
		t.Fatalf("output=%q activity=%+v", output, activity)
	}
}

func TestExtensionToolsUseUnifiedRunnerAndActivityBoundary(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Tool extension",
		"description":"Provides a bounded tool",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"tools":true
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	var events []Event
	service.Emit = func(event Event) { events = append(events, event) }
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID, "tool-request",
		"tool prompt", snapshot)
	if err != nil || !created {
		t.Fatalf("create run=%+v created=%v err=%v", run, created, err)
	}
	request, err := service.prepareCapabilityRequest(t.Context(), run, ModelRequest{
		Purpose: "chat", Messages: []ModelMessage{{Role: "user", Content: "tool prompt"}},
		Tools: service.runtimeTools(run.ID, "").Definitions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var definition ToolDefinition
	for _, candidate := range request.Tools {
		if strings.HasPrefix(candidate.Name, "ext_") {
			definition = candidate
			break
		}
	}
	if definition.Name == "" || definition.Effect != EffectModify {
		t.Fatalf("extension tool definition=%+v", definition)
	}
	raw, _ := json.Marshal(map[string]any{"text": "hello"})
	prepared, err := service.runtimeTools(run.ID, "attempt-1").Prepare(t.Context(),
		ToolCall{ID: "call-1", Name: definition.Name, Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !service.needsApproval(prepared) {
		t.Fatal("extension modify tool bypassed approval")
	}
	result, err := prepared.Execute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "tool:hello" || result.IsError {
		t.Fatalf("tool result=%+v", result)
	}
	foundActivity := false
	for _, event := range events {
		if event.Kind == "extension_activity" && event.Message == "tool completed" {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("events=%+v", events)
	}
}

func TestExtensionWorkflowRejectsInvalidInputAndUndeclaredName(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[
			{"name":"summarize","description":"Summarize input",
			 "input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}
		]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = host.close() })

	_, _, err := host.workflow(t.Context(), extensionID, "summarize",
		Run{ID: "workflow-run", Prompt: "summarize this"}, map[string]any{"wrong": true},
		nil, service)
	if err == nil || !strings.Contains(err.Error(), "input does not satisfy") {
		t.Fatalf("invalid input error=%v", err)
	}
	_, _, err = host.workflow(t.Context(), extensionID, "missing",
		Run{ID: "workflow-run", Prompt: "summarize this"}, map[string]any{"text": "hello"},
		nil, service)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared workflow error=%v", err)
	}
}

func TestExtensionWorkflowBoundsOutput(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[{"name":"large","description":"Oversized output","input_schema":{"type":"object"}}]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = host.close() })

	_, _, err := host.workflow(t.Context(), extensionID, "large",
		Run{ID: "workflow-run", Prompt: "large"}, map[string]any{}, nil, service)
	if err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("oversized output error=%v", err)
	}
}

func TestExtensionHostRejectsExpiredInvocation(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.host", service,
		Run{ID: "run"}, ExtensionManifest{ID: "com.example.host", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	host.endInvocation(invocation)
	request := &jsonrpc.Request{Method: "activity.report"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id,
		"summary":    "should be rejected",
	})
	_, err = host.handleHostRequest(t.Context(), "com.example.host", request)
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32005 {
		t.Fatalf("expired invocation error=%v", err)
	}
}

func activeDelegationPlan(modelID string) CollaborationPlan {
	return CollaborationPlan{
		Objective: "delegate bounded analysis",
		Budget: ParentBudget{
			MaxChildren: 2, MaxModelCalls: 8, MaxToolCalls: 8,
			MaxOutputBytes: 8192, MaxConcurrentChildren: 2, MaxActiveSeconds: 30,
		},
		Roles: map[string]RoleProfile{
			"analyst": {
				Instructions: "Return a concise analysis.",
				ModelID:      modelID,
				OutputFormat: "text",
				Acceptance:   "analysis",
				Budget: ChildBudget{
					MaxSteps: 4, MaxSeconds: 20,
					MaxOutputBytes: 4096, MaxCallOutputBytes: 1024,
				},
			},
		},
		Children: []ChildSpec{
			{Name: "first", Role: "analyst", Objective: "Analyze the first item."},
			{Name: "second", Role: "analyst", Objective: "Analyze the second item."},
		},
		Parallel: true,
	}
}

func TestExtensionHostTaskDelegateRunsChildrenInsideActiveRun(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	service.Config.Extensions.Enabled = map[string]bool{"com.example.extension": true}
	service.Config.Extensions.Required = map[string]bool{"com.example.extension": true}
	service.Config.Extensions.Settings = map[string]map[string]any{
		"com.example.extension": {"mode": "test"},
	}
	service.Config.Extensions.EnvFrom = map[string]map[string]string{
		"com.example.extension": {"TOKEN": "key:test"},
	}
	service.capabilities = &CapabilitySelection{Extensions: []string{"com.example.extension"}}
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID,
		"delegate", "parent turn", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.delegate",
		service, run, ExtensionManifest{ID: "com.example.delegate", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)
	plan := activeDelegationPlan("")
	rawPlan, _ := json.Marshal(plan)
	request := &jsonrpc.Request{Method: "task.delegate"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id, "plan": json.RawMessage(rawPlan),
	})
	result, err := host.handleHostRequest(t.Context(), "com.example.delegate", request)
	if err != nil {
		t.Fatal(err)
	}
	reply, ok := result.(map[string]any)
	if !ok || reply["control"] != "completed" {
		t.Fatalf("delegate result=%T %+v", result, result)
	}
	parent, err := service.Store.GetRun(t.Context(), run.ID)
	if err != nil || parent.Status != "running" {
		t.Fatalf("active parent was settled: run=%+v err=%v", parent, err)
	}
	report, err := NewCollaboration(NewTaskService(service)).Inspect(t.Context(), run.ID)
	if err != nil || report.Control != "completed" || len(report.Children) != 2 {
		t.Fatalf("delegation report=%+v err=%v", report, err)
	}
	for _, child := range report.Children {
		if child.Run.Status != "completed" || !strings.Contains(child.Run.Output, "Echo:") {
			t.Fatalf("child=%+v", child)
		}
		snapshot, err := decodeRunSnapshot(child.Run.ConfigJSON)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Capabilities != nil && len(snapshot.Capabilities.Extensions) != 0 {
			t.Fatalf("child inherited extension capabilities: %+v", snapshot.Capabilities)
		}
		if len(snapshot.Config.Extensions.Enabled) != 0 ||
			len(snapshot.Config.Extensions.Required) != 0 ||
			len(snapshot.Config.Extensions.Settings) != 0 ||
			len(snapshot.Config.Extensions.EnvFrom) != 0 {
			t.Fatalf("child inherited extension configuration: %+v", snapshot.Config.Extensions)
		}
	}
	_, err = host.handleHostRequest(t.Context(), "com.example.delegate", request)
	if !errors.Is(err, errExtensionDenied) {
		t.Fatalf("second delegation error=%v", err)
	}
}

func TestExtensionHostTaskDelegateRejectsUnauthorizedModelAndTool(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID,
		"delegate-reject", "parent turn", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	invocation, err := host.beginInvocation(t.Context(), "com.example.delegate",
		service, run, ExtensionManifest{ID: "com.example.delegate", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)

	plan := activeDelegationPlan("missing-model")
	rawPlan, _ := json.Marshal(plan)
	request := &jsonrpc.Request{Method: "task.delegate"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id, "plan": json.RawMessage(rawPlan),
	})
	_, err = host.handleHostRequest(t.Context(), "com.example.delegate", request)
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 ||
		!strings.Contains(rpcErr.Message, "outside the active turn") {
		t.Fatalf("unauthorized model error=%v", err)
	}

	plan = activeDelegationPlan("")
	role := plan.Roles["analyst"]
	role.Tools = []string{"not_enabled_tool"}
	role.Paths = []string{"."}
	plan.Roles["analyst"] = role
	rawPlan, _ = json.Marshal(plan)
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id, "plan": json.RawMessage(rawPlan),
	})
	_, err = host.handleHostRequest(t.Context(), "com.example.delegate", request)
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 ||
		!strings.Contains(rpcErr.Message, "tool") {
		t.Fatalf("unauthorized tool error=%v", err)
	}
}

func TestExtensionContextPrepareUsesRealProcessAndPlacement(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.test"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.test",
		"version":"1.0.0",
		"name":"Test extension",
		"description":"A bounded test extension",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper"]},
		"needs":["message","history:1","workspace"],
		"context":{"phases":["turn","step"],"purposes":["chat"],"max_chars":2000,"placement":["system","tail"]},
		"required":true
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.Config.Extensions.Required = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })

	run := Run{Prompt: "hello"}
	request := ModelRequest{Purpose: "chat", Messages: []ModelMessage{
		{Role: "system", Content: "core"},
		{Role: "user", Content: "hello"},
	}}
	request, err := service.prepareCapabilityRequest(context.Background(), run, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Extensions) != 2 || request.Extensions[0].Source != "extension" {
		t.Fatalf("extension uses = %+v", request.Extensions)
	}
	if len(request.Messages) != 4 ||
		request.Messages[0].Role != "system" || request.Messages[0].Content != "core" ||
		request.Messages[1].Role != "system" || !strings.Contains(request.Messages[1].Content, "extension system context") ||
		request.Messages[2].Role != "user" || !strings.Contains(request.Messages[2].Content, "extension tail context") ||
		request.Messages[3].Role != "user" || request.Messages[3].Content != "hello" {
		t.Fatalf("messages = %+v", request.Messages)
	}
	dataPath := filepath.Join(service.Store.DataDir(), "extensions", extensionID, "data")
	if info, err := os.Stat(dataPath); err != nil || !info.IsDir() {
		t.Fatalf("extension data dir = %q, err=%v", dataPath, err)
	}
}

func operationCheckFixture(t *testing.T, decision string, required bool) (*Service, *TaskService, string, string) {
	t.Helper()
	service, _, workspace := testHarness(t, nil)
	marker := filepath.Join(workspace, "operation-check-marker")
	service.Tools = permissionExternalTool{marker: marker}
	service.Model = permissionToolModel(makeCall("operation-check", "file_external", map[string]any{}))
	extensionID := "com.example.test"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.test",
		"version":"1.0.0",
		"name":"Operation check extension",
		"description":"Checks consequential operations before dispatch.",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper","--freeagent-operation-check=` + decision + `"]},
		"events":["operation.check"],
		"required":` + fmt.Sprintf("%t", required) + `
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.Config.Extensions.Required = map[string]bool{extensionID: required}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	return service, NewTaskService(service), marker, extensionID
}

func TestExtensionOperationCheckRealProcessControlsDispatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		decision   string
		required   bool
		wantStatus string
		wantMarker bool
		wantError  bool
	}{
		{name: "allow", decision: "allow", wantStatus: "completed", wantMarker: true},
		{name: "deny", decision: "deny", wantStatus: "completed", wantMarker: false},
		{name: "optional failure", decision: "bad", wantStatus: "completed", wantMarker: true},
		{name: "required failure", decision: "bad", required: true, wantStatus: "stopped", wantMarker: false, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []Event
			service, tasks, marker, _ := operationCheckFixture(t, tc.decision, tc.required)
			service.Emit = func(event Event) { events = append(events, event) }
			tasks.SetEvents(service.Emit)
			chat := conversation(t, service)
			result, err := tasks.SubmitWithOptions(t.Context(), chat, tc.name, "run checked operation", RunOptions{
				Permission:        PermissionFull,
				ConfirmFullAccess: true,
			})
			if tc.wantError {
				if err == nil {
					t.Fatalf("required operation.check unexpectedly succeeded: result=%+v", result)
				}
			} else if err != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.Run.Status != tc.wantStatus {
				t.Fatalf("status=%s want=%s err=%v", result.Run.Status, tc.wantStatus, err)
			}
			_, statErr := os.Stat(marker)
			if got := statErr == nil; got != tc.wantMarker {
				t.Fatalf("marker exists=%t want=%t err=%v", got, tc.wantMarker, statErr)
			}
			if tc.name == "optional failure" {
				found := false
				for _, event := range events {
					if event.Kind == "extension_error" && strings.Contains(event.Message, "operation.check") {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("optional failure did not emit extension_error: %+v", events)
				}
			}
		})
	}
}

func TestExtensionOperationCheckAskStillRequiresFullAccessConfirmation(t *testing.T) {
	service, tasks, marker, _ := operationCheckFixture(t, "ask", false)
	chat := conversation(t, service)
	result, err := tasks.SubmitWithOptions(t.Context(), chat, "ask", "run checked operation", RunOptions{
		Permission:        PermissionFull,
		ConfirmFullAccess: true,
	})
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("ask did not stop for confirmation: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ask dispatched before confirmation: %v", err)
	}
	decision, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tool != "file_external" || decision.Preview == "" || len(decision.Items) != 1 {
		t.Fatalf("confirmation=%+v", decision)
	}
	continued, err := tasks.Confirm(t.Context(), result.Run.ID, decision, true)
	if err != nil || continued.Run.Status != "completed" {
		t.Fatalf("confirmed ask did not complete: result=%+v err=%v", continued, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("confirmed ask did not dispatch: %v", err)
	}
}

func TestExtensionOperationCheckBrokenManifestFailsClosedOnlyWhenRequired(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(fmt.Sprintf("required=%t", required), func(t *testing.T) {
			service, _, marker, extensionID := operationCheckFixture(t, "deny", required)
			var events []Event
			service.Emit = func(event Event) { events = append(events, event) }
			manifest := filepath.Join(service.Store.DataDir(), "extensions", extensionID, "extension.json")
			if err := os.WriteFile(manifest, []byte(`{"protocol":`), 0o600); err != nil {
				t.Fatal(err)
			}
			call := makeCall("operation-check", "file_external", map[string]any{})
			prepared, err := permissionExternalTool{marker: marker}.Prepare(t.Context(), call)
			if err != nil {
				t.Fatal(err)
			}
			ask, deny, err := service.checkOperation(t.Context(), Run{ID: "run"}, call, prepared)
			if required {
				if err == nil || !strings.Contains(err.Error(), "required extension") {
					t.Fatalf("broken required extension did not stop the operation: ask=%t deny=%q err=%v", ask, deny, err)
				}
				return
			}
			if err != nil || ask || deny != "" {
				t.Fatalf("optional broken extension changed the decision: ask=%t deny=%q err=%v", ask, deny, err)
			}
			found := false
			for _, event := range events {
				found = found || event.Kind == "extension_error" && strings.Contains(event.Message, "operation.check")
			}
			if !found {
				t.Fatalf("optional broken extension was not reported: %+v", events)
			}
		})
	}
}

func extensionClientClosed(t *testing.T, host *extensionHost, id string) bool {
	t.Helper()
	host.mu.Lock()
	defer host.mu.Unlock()
	client := host.clients[id]
	return client == nil || client.rpc.Closed()
}

func TestExtensionEnvironmentRedactsOnlyCredentialLikeValues(t *testing.T) {
	const secret = `long"secret\value`
	t.Setenv("FREEAGENT_TEST_FLAG", "1")
	t.Setenv("FREEAGENT_TEST_MODE", "true")
	t.Setenv("FREEAGENT_TEST_TOKEN", secret)
	workspace := t.TempDir()
	host := newExtensionHost(t.TempDir(), workspace, DefaultConfig(workspace), nil)
	t.Cleanup(func() { _ = host.close() })
	env, err := host.environment("com.example.env", ExtensionManifest{
		Env: []string{"FREEAGENT_TEST_FLAG", "FREEAGENT_TEST_MODE", "FREEAGENT_TEST_TOKEN"},
	}, DefaultConfig(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "FREEAGENT_TEST_FLAG=1") || !slices.Contains(env, "FREEAGENT_TEST_TOKEN="+secret) {
		t.Fatalf("environment was not passed through: %q", env)
	}
	if got := host.redact("retry 1 time: true"); got != "retry 1 time: true" {
		t.Fatalf("short environment values were redacted: %q", got)
	}
	value, err := redactExtensionJSONValue(host, map[string]any{
		"count": 1, "enabled": true, "note": "token " + secret, secret: "key",
	}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("redacted JSON is invalid: %s", raw)
	}
	if decoded["count"] != float64(1) || decoded["enabled"] != true ||
		decoded["note"] != "token "+redactedText || decoded[redactedText] != "key" ||
		strings.Contains(string(raw), "secret") {
		t.Fatalf("redacted value=%s", raw)
	}
}

func TestExtensionOperationCheckKeepsProcessAfterErrorReplyOnly(t *testing.T) {
	for _, tc := range []struct {
		decision   string
		wantClosed bool
	}{
		{decision: "rpcerror", wantClosed: false},
		{decision: "hang", wantClosed: true},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			service, _, marker, extensionID := operationCheckFixture(t, tc.decision, false)
			manifest := filepath.Join(service.Store.DataDir(), "extensions", extensionID, "extension.json")
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.Replace(string(raw), "{", `{"timeout_ms":300,`, 1))
			if err := os.WriteFile(manifest, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			call := makeCall("operation-check", "file_external", map[string]any{})
			prepared, err := permissionExternalTool{marker: marker}.Prepare(t.Context(), call)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.extensions.checkOperation(t.Context(), extensionID, Run{ID: "run"}, call, prepared, service); err == nil {
				t.Fatal("failing operation.check returned no error")
			}
			if closed := extensionClientClosed(t, service.extensions, extensionID); closed != tc.wantClosed {
				t.Fatalf("client closed=%t want=%t", closed, tc.wantClosed)
			}
		})
	}
}

func TestExtensionHostToolCallSkipsCallerOperationCheck(t *testing.T) {
	const extensionID = "com.example.host"
	var requests []ModelRequest
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		return ModelResponse{Content: "done"}, nil
	})
	service, _, workspace := testHarness(t, model)
	marker := filepath.Join(workspace, "reentrant-marker")
	service.Tools = permissionExternalTool{marker: marker}
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Reentrant extension",
		"description":"Calls a consequential Host tool while it also checks operations",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper","--freeagent-reentrant-tool-call"]},
		"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]},
		"events":["operation.check"],
		"required":true
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.Config.Extensions.Required = map[string]bool{extensionID: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	tasks := NewTaskService(service)
	result, err := tasks.SubmitWithOptions(t.Context(), conversation(t, service), "reentrant", "hello", RunOptions{
		Permission: PermissionFull, ConfirmFullAccess: true,
	})
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("host tool.call did not dispatch: %v", err)
	}
	found := false
	for _, request := range requests {
		for _, message := range request.Messages {
			found = found || strings.Contains(message.Content, `reentrant:{"executed":true}`)
		}
	}
	if !found {
		t.Fatalf("tool.call result did not reach the context block: %+v", requests)
	}
	if extensionClientClosed(t, service.extensions, extensionID) {
		t.Fatal("re-entrant tool.call closed the calling extension process")
	}
}

func TestExtensionStepPrepareUsesRealProcessAndDoesNotLeakTurnOrPreviousStep(t *testing.T) {
	var requests []ModelRequest
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			raw, _ := json.Marshal(map[string]any{"path": "."})
			return ModelResponse{ToolCalls: []ToolCall{{ID: "pending-list", Name: "file_list", Arguments: raw}}}, nil
		}
		return ModelResponse{Content: fmt.Sprintf("answer %d", len(requests))}, nil
	})
	service, _, workspace := testHarness(t, model)
	extensionID := "com.example.test"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.test",
		"version":"1.0.0",
		"name":"Test extension",
		"description":"A bounded test extension",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper"]},
		"needs":["message","history:1","workspace"],
		"context":{"phases":["turn","step"],"purposes":["chat"],"max_chars":2000,"placement":["system","tail"]},
		"required":true
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.Config.Extensions.Required = map[string]bool{extensionID: true}
	service.Config.MaxSteps = 4
	service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })

	conversationID := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversationID, "step-hooks", "hello", snapshot)
	if err != nil || !created {
		t.Fatalf("create run=%+v created=%v err=%v", run, created, err)
	}
	request, err := service.buildContext(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Extensions) != 2 || request.Extensions[0].Phase != "turn" {
		t.Fatalf("turn extensions=%+v", request.Extensions)
	}
	if len(request.Messages) != 4 || !strings.Contains(request.Messages[1].Content, "extension system context") ||
		!strings.Contains(request.Messages[2].Content, "extension tail context") {
		t.Fatalf("turn messages=%+v", request.Messages)
	}
	if _, _, err := service.generate(t.Context(), run, request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.generate(t.Context(), run, request); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("model calls=%d requests=%+v", len(requests), requests)
	}
	for index, request := range requests {
		if len(request.Messages) == 0 || strings.Contains(request.Messages[len(request.Messages)-1].Content, "extension step") == false {
			t.Fatalf("request %d missing step tail: %+v", index, request.Messages)
		}
		if strings.Contains(request.Messages[len(request.Messages)-1].Content, fmt.Sprintf("step %d", index)) == false {
			t.Fatalf("request %d has wrong step block: %+v", index, request.Messages)
		}
		if strings.Contains(request.Messages[len(request.Messages)-1].Content, fmt.Sprintf("step %d", 1-index)) {
			t.Fatalf("request %d leaked another step block: %+v", index, request.Messages)
		}
		if len(request.Extensions) != 3 || request.Extensions[2].Phase != "step" {
			t.Fatalf("request %d extension uses=%+v", index, request.Extensions)
		}
	}
	attempts, err := service.Store.ListAttempts(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.Kind != "model" {
			continue
		}
		var stored ModelRequest
		if err := json.Unmarshal([]byte(attempt.RequestJSON), &stored); err != nil {
			t.Fatal(err)
		}
		if len(stored.Extensions) != 3 || stored.Extensions[2].Phase != "step" {
			t.Fatalf("stored request=%+v", stored)
		}
	}
}

func TestExtensionOptionalFailureIsWarningAndRequiredFailureStops(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	service.Config.Extensions.Enabled = map[string]bool{
		"com.example.optional": true,
		"com.example.required": true,
	}
	service.Config.Extensions.Required = map[string]bool{"com.example.required": true}
	service.capabilities = &CapabilitySelection{Extensions: []string{"com.example.optional"}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	var events []Event
	service.Emit = func(event Event) { events = append(events, event) }
	_, err := service.prepareCapabilityRequest(context.Background(), Run{Prompt: "hello"}, ModelRequest{
		Purpose: "chat", Messages: []ModelMessage{{Role: "system", Content: "core"}, {Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("optional extension error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != "extension_error" {
		t.Fatalf("events = %+v", events)
	}

	service.extensionPrepared = false
	service.extensionBlocks = nil
	service.extensionUses = nil
	service.capabilities = &CapabilitySelection{Extensions: []string{"com.example.required"}}
	_, err = service.prepareCapabilityRequest(context.Background(), Run{Prompt: "hello"}, ModelRequest{
		Purpose: "chat", Messages: []ModelMessage{{Role: "system", Content: "core"}, {Role: "user", Content: "hello"}},
	})
	if err == nil || !strings.Contains(err.Error(), "required extension") {
		t.Fatalf("required extension error = %v", err)
	}
	_ = service.extensions.close()
}

func TestExtensionStepPlacementAndFailurePolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		required bool
		wantErr  bool
	}{
		{name: "optional", required: false, wantErr: false},
		{name: "required", required: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, workspace := testHarness(t, EchoModel{})
			extensionID := "com.example.step-" + tc.name
			extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
			if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := `{
				"protocol":"freeagent-ext/1",
				"id":"` + extensionID + `",
				"version":"1.0.0",
				"name":"Step placement test",
				"description":"Reject non-tail step blocks",
				"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper"]},
				"context":{"phases":["step"],"purposes":["chat"],"placement":["system"]},
				"required":` + fmt.Sprintf("%t", tc.required) + `
			}`
			if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
			service.Config.Extensions.Required = map[string]bool{extensionID: tc.required}
			service.capabilities = &CapabilitySelection{Extensions: []string{extensionID}}
			service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
			t.Cleanup(func() { _ = service.extensions.close() })
			var events []Event
			service.Emit = func(event Event) { events = append(events, event) }

			_, err := service.prepareExtensionStep(t.Context(), Run{Prompt: "hello"}, ModelRequest{
				Purpose:  "chat",
				Messages: []ModelMessage{{Role: "system", Content: "core"}, {Role: "user", Content: "hello"}},
			})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "required extension") {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil {
				t.Fatalf("optional step error=%v", err)
			}
			if (!tc.wantErr && (len(events) != 1 || events[0].Kind != "extension_error")) ||
				(tc.wantErr && len(events) != 0) {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}
