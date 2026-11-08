package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const genericWakeID = "com.example.followup"

func runScheduledHelper() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	writer := json.NewEncoder(os.Stdout)
	coreID := ""
	type frame struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method,omitempty"`
		Params  json.RawMessage `json:"params,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	child := func(method string, params any) (frame, bool) {
		raw, _ := json.Marshal(params)
		if writer.Encode(frame{JSONRPC: "2.0", ID: json.RawMessage(`"child"`), Method: method, Params: raw}) != nil || !scanner.Scan() {
			return frame{}, false
		}
		var f frame
		err := json.Unmarshal(scanner.Bytes(), &f)
		return f, err == nil
	}
	for scanner.Scan() {
		var f frame
		if json.Unmarshal(scanner.Bytes(), &f) != nil {
			return 2
		}
		var result any
		switch f.Method {
		case "initialize":
			var p struct {
				Core string `json:"core_id"`
			}
			_ = json.Unmarshal(f.Params, &p)
			coreID = p.Core
			result = map[string]any{"id": genericWakeID, "version": "1.0", "state_version": 2, "provides": []string{"context", "state"}, "events": []string{"turn.finished"}, "capabilities": []string{"scheduled_generation"}}
		case "state.sources":
			result = map[string]any{"core_id": coreID, "sources": []any{}}
		case "context.prepare":
			result = map[string]any{"blocks": []any{}}
		case "turn.finished":
			result = map[string]any{}
		case "shutdown":
			result = map[string]bool{"ok": true}
		case "wake.evaluate":
			var p struct {
				Invocation string            `json:"invocation"`
				Identity   ExtensionIdentity `json:"identity"`
			}
			if json.Unmarshal(f.Params, &p) != nil || p.Identity.Trigger == nil || len(p.Identity.Inputs) != 0 {
				return 3
			}
			for _, method := range []string{"tool.call", "delivery.propose", "model.embed"} {
				r, ok := child(method, map[string]any{"invocation": p.Invocation})
				if !ok || (len(r.Error) == 0 || string(r.Error) == "null") {
					return 4
				}
			}
			r, ok := child("model.generate", map[string]any{"invocation": p.Invocation, "model": "current", "messages": []map[string]string{{"role": "user", "content": "Return the authorized follow-up decision."}}, "max_tokens": 1024})
			if !ok || (len(r.Error) > 0 && string(r.Error) != "null") {
				return 5
			}
			var body struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(r.Result, &body) != nil {
				return 6
			}
			result = json.RawMessage(body.Content)
		default:
			return 7
		}
		encoded, _ := json.Marshal(result)
		if writer.Encode(frame{JSONRPC: "2.0", ID: f.ID, Result: encoded}) != nil {
			return 8
		}
		if f.Method == "shutdown" {
			return 0
		}
	}
	return 0
}
func TestScheduledGenerationWorksForNonPersonaExtension(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	t.Cleanup(func() { _ = tasks.core.extensions.close() })
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "normal source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.SubmitChannel(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ExtensionManifest{Protocol: extensionProtocol, ID: genericWakeID, Version: "1.0", Name: "Generic follow-up", Run: ExtensionRun{Command: exe, Args: []string{"--freeagent-scheduled-helper"}}, State: true, StateVersion: 2, StateOperations: map[string]ExtensionStateOperation{"update": {Scope: "actor", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}, PassiveContext: true, ScheduledGeneration: true, Budget: ExtensionBudget{ModelCalls: 1, OutputTokens: 1024}, Events: []string{"turn.finished"}, Context: ExtensionContext{Phases: []string{"turn"}, Purposes: []string{"chat"}, Placement: []string{"tail"}}}
	dir := filepath.Join(tasks.DataDir(), "extensions", genericWakeID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "extension.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if tasks.core.extensions == nil {
		tasks.core.extensions = newExtensionHost(tasks.DataDir(), tasks.Config().Workspace, tasks.Config(), tasks.core.Keys)
	}
	cfg := tasks.Config()
	cfg.Extensions.Enabled = map[string]bool{genericWakeID: true}
	rule := FollowupRule{ID: "generic-rule", Extension: genericWakeID, ConversationID: input.ConversationID, Enabled: true, Timezone: "UTC", DailyMax: 1, CooldownHours: 6, CancelOnInput: true, MaxDelaySeconds: 86400, Generation: true, Resource: "plain-resource", SourceRefs: []string{"topic:authorized"}}
	cfg.Delivery = DeliveryConfig{Enabled: true, GenerationEnabled: map[string]bool{genericWakeID: true}, Rules: []FollowupRule{rule}}
	if err = WriteConfig(tasks.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	tasks.core.setConfig(cfg)
	tasks.core.extensions.configure(cfg)
	control, _ := tasks.Control()
	if err = control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = tasks.SelectCapabilities(t.Context(), input.ConversationID, CapabilitySelection{Extensions: []string{genericWakeID}, Variants: map[string]string{genericWakeID: "plain-resource"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "scheduled/input/"+input.ConversationID, string(mustJSON(map[string]any{"message_id": input.MessageID, "at": time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339Nano)}))); err != nil {
		t.Fatal(err)
	}
	calls := 0
	tasks.core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{Content: `{"decision":"speak","text":"A plain extension follow-up.","reason_code":"relevant","reason":"Authorized topic","source_refs":["topic:authorized"]}`}, nil
	})
	o, created, err := tasks.ScheduleWake(t.Context(), rule, "generic-slot", rule.SourceRefs, time.Now(), false)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || calls != 1 {
		records, _ := tasks.WakeRecords(t.Context(), input.ConversationID)
		t.Fatal(fmt.Sprint(records), ops, calls, err)
	}
}
