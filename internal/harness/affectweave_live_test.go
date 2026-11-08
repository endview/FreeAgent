package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Paid Provider requests, synthetic characters and isolated PG only. Preview
// never sends to Telegram; no production bot or conversation is used.
func TestLiveProviderAffectWeaveAnalysisAndProactivePreview(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	for _, character := range []struct {
		name       string
		directness float64
	}{{"Maya, concise engineering partner", .7}, {"Robin, exploratory engineering partner", .3}} {
		t.Run(character.name, func(t *testing.T) {
			b, tasks, target, rule := affectWakeHarness(t)
			core := tasks.core
			source, _ := tasks.Latest(t.Context(), rule.ConversationID)
			control, _ := tasks.Control()
			if _, err := control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "resource": "maya", "actor_id": "owner"}, "action": "edit_role", "expected_revision": 2, "payload": map[string]any{"persona": map[string]any{"id": "maya", "name": character.name, "identity_summary": "Synthetic AI character for engineering conversations", "boundaries": []string{"Do not pretend to be a human or invent shared experiences"}, "preferences": []string{"Reviewing concrete software architecture and deployment checks"}, "parameters": map[string]float64{"sensitivity": .5, "recovery_speed": .5, "expression": .5, "regulation": .5, "directness": character.directness, "disclosure": .5, "repair_preference": .5}, "frozen": true, "version": 3}, "spaces": []string{"local", source.Permission.SpaceID}}}, "", "test-owner"); err != nil {
				t.Fatal(err)
			}
			cfg := core.currentConfig()
			cfg.Models[cfg.DefaultModel] = config
			cfg.Extensions.Settings[affectExtensionID]["analysis_enabled"] = true
			cfg.Extensions.Settings[affectExtensionID]["analysis_deadline_ms"] = float64(15000)
			enableTestProviders(&cfg)
			core.Keys = keys
			if err := WriteConfig(tasks.DataDir(), cfg); err != nil {
				t.Fatal(err)
			}
			core.setConfig(cfg)
			core.extensions.configure(cfg)
			client := keyedClient(t, config, keys, nil)
			calls := 0
			var outputTokens int64
			core.Model = modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
				calls++
				if request.SupplementalKey == "" && !strings.Contains(string(mustJSON(request.Messages)), "expression_hints") {
					t.Error("effective expression hints absent from actual Provider request")
				}
				response, err := client.Generate(ctx, request, emit)
				if response.Usage != nil {
					outputTokens += response.Usage.OutputTokens
				}
				return response, err
			})
			input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "live-input", Purpose: "request"}, "The deployment check is still pending. Briefly discuss one concrete way to verify it; do not run tools or claim it succeeded.")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			started := time.Now()
			result, err := tasks.SubmitChannel(ctx, input)
			if err != nil || result.Run.Status != "completed" || result.Answer == "" {
				t.Fatalf("ordinary answer not accepted: %s %v", result.Run.Status, err)
			}
			raw, err := tasks.ExtensionState(ctx, affectExtensionID, rule.ConversationID, "maya", "telegram:user:7")
			var view struct {
				Analyses []struct {
					Analysis string `json:"analysis"`
				} `json:"analyses"`
			}
			if err != nil || json.Unmarshal(raw, &view) != nil || len(view.Analyses) == 0 || view.Analyses[0].Analysis != "complete" {
				t.Fatal("real bounded analysis was not accepted", err)
			}
			o, err := tasks.PreviewScheduledGeneration(ctx, rule.ID, "real-provider-preview")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tasks.EvaluateWake(ctx, o.ID); err != nil {
				t.Fatal(err)
			}
			records, err := tasks.WakeRecords(ctx, rule.ConversationID)
			if err != nil || len(records) != 1 || (records[0].State != "previewed" && records[0].State != "skipped") || calls != 3 {
				t.Fatal("one-shot real proactive preview not accepted", calls, records, err)
			}
			if ops, err := b.Outbox(ctx, "tg"); err != nil || len(ops) != 0 {
				t.Fatal("preview unexpectedly sent", ops, err)
			}
			t.Logf("actual_provider_requests=%d output_tokens=%d total_ms=%d preview=%s reason=%s; synthetic recipients only, no platform send", calls, outputTokens, time.Since(started).Milliseconds(), records[0].State, records[0].Reason)
		})
	}
}

// Preserve the original multi-turn and process-restart live acceptance gate.
func TestLiveProviderAffectWeaveCharacters(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	config.Capabilities.Streaming = "supported"
	config.Capabilities.Usage = "required"
	live := keyedClient(t, config, keys, nil)
	core, tasks, _ := affectHost(t)
	core.Model = live
	core.Keys = keys
	cfg := core.currentConfig()
	cfg.Models[cfg.DefaultModel] = config
	enableTestProviders(&cfg)
	if err := WriteConfig(core.Store.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	core.setConfig(cfg)
	core.extensions.configure(cfg)
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	control, _ := tasks.Control()
	if err := control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "resource": "nova", "actor_id": "owner"}, "action": "create_role", "expected_revision": 0, "payload": map[string]any{"spaces": []string{"local"}, "persona": map[string]any{"id": "nova", "name": "Nova", "identity_summary": "A patient research companion named Nova", "boundaries": []string{"Do not disclose private messages"}, "frozen": true, "version": 1}}}, "", "test-owner"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"maya", "nova"} {
		chat := conversation(t, core)
		if err := tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{Extensions: []string{affectExtensionID}, Variants: map[string]string{affectExtensionID: role}}); err != nil {
			t.Fatal(err)
		}
		for n, prompt := range []string{"只用一句话说明你的角色名字，保留角色名的英文原文。", "继续上一轮，再说一遍你的角色名。"} {
			result, err := tasks.Submit(t.Context(), chat, role+"-"+string(rune('a'+n)), prompt)
			if err != nil || result.Run.Status != "completed" || !strings.Contains(strings.ToLower(result.Answer), role) {
				t.Fatalf("real character=%s status=%s error=%v", role, result.Run.Status, err)
			}
			attempts, _ := core.Store.ListAttempts(t.Context(), result.Run.ID)
			for _, a := range attempts {
				if a.Kind == "model" {
					var response ModelResponse
					_ = json.Unmarshal([]byte(a.ResponseJSON), &response)
					if response.Usage == nil || response.Usage.TotalTokens <= 0 {
						t.Fatal("missing actual provider usage")
					}
				}
			}
		}
		if err := core.extensions.restart(affectExtensionID); err != nil {
			t.Fatal(err)
		}
		result, err := tasks.Submit(t.Context(), chat, role+"-restarted", "扩展进程已重新打开。请再次只说你的角色英文名。")
		if err != nil || result.Run.Status != "completed" || !strings.Contains(strings.ToLower(result.Answer), role) {
			t.Fatalf("real restarted character=%s status=%s error=%v", role, result.Run.Status, err)
		}
		raw, err := tasks.ExtensionState(t.Context(), affectExtensionID, chat, role, "owner")
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			State struct {
				Revision int64 `json:"revision"`
			} `json:"state"`
		}
		if json.Unmarshal(raw, &state) != nil || state.State.Revision != 4 {
			t.Fatal("real inputs were not adopted exactly once", state.State.Revision)
		}
		t.Logf("real character=%s turns=3 stream=provider usage=recorded process_restart=passed", role)
	}
}
