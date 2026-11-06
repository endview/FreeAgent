package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveProviderAffectWeaveCharacters(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	config.Capabilities.Streaming = "supported"
	config.Capabilities.Usage = "required"
	live := keyedClient(t, config, keys, nil)
	core, tasks, _ := affectHost(t)
	core.Model = live
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	control, _ := tasks.Control()
	if _, err := control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "role_id": "nova", "actor_id": "owner"}, "action": "create_role", "expected_revision": 0, "spaces": []string{"local"}, "persona": map[string]any{"id": "nova", "name": "Nova", "identity_summary": "A patient research companion named Nova", "boundaries": []string{"Do not disclose private messages"}, "frozen": true, "version": 1}}, "", "test-owner"); err != nil {
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
