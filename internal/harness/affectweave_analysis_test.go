package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAffectWeaveBoundedAnalysisAndOrdinaryAnswerFallback(t *testing.T) {
	for _, mode := range []string{"off", "success", "malformed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			core, tasks, _ := affectHost(t)
			cfg := tasks.Config()
			cfg.ModelRetries = 3
			cfg.Extensions.Settings[affectExtensionID]["analysis_enabled"] = mode != "off"
			if err := WriteConfig(core.Store.DataDir(), cfg); err != nil {
				t.Fatal(err)
			}
			core.setConfig(cfg)
			core.extensions.configure(cfg)
			supplementary, ordinary := 0, 0
			core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
				if r.SupplementalKey != "" {
					supplementary++
					if len(r.Tools)+len(r.Memory)+len(r.Extensions) != 0 {
						t.Error("analysis gained ambient context/tools")
					}
					if mode == "unknown" {
						return ModelResponse{}, errors.New("controlled disconnect")
					}
					if mode == "malformed" {
						return ModelResponse{Content: "invalid"}, nil
					}
					return ModelResponse{Content: `{"appraisal":{"kind":"interest","confidence":0.8,"reason":"specific design question"},"emotion_candidates":[{"emotion":"interest","cause_id":"design","cause_summary":"design question","delta":0.2,"confidence":0.8}],"behavior_hint":"Discuss the concrete design","persistent_candidates":{}}`}, nil
				}
				ordinary++
				text := string(mustJSON(r.Messages))
				if mode == "success" && !strings.Contains(text, "Temporary interpretation") {
					t.Error("temporary analysis missing")
				}
				return ModelResponse{Content: "ordinary answer"}, nil
			})
			chat := conversation(t, core)
			result, err := tasks.Submit(t.Context(), chat, "analysis-input", "How does this design work?")
			if err != nil || result.Run.Status != "completed" || ordinary != 1 {
				t.Fatal(result.Run.Status, ordinary, err)
			}
			expected := 1
			if mode == "off" {
				expected = 0
			}
			if supplementary != expected {
				t.Fatalf("analysis calls=%d want %d", supplementary, expected)
			}
			if affectState(t, tasks, chat).Relationship.Trust != .5 {
				t.Fatal("unconfirmed analysis changed long-term state")
			}
			before := supplementary
			if _, err = core.extensions.prepare(t.Context(), affectExtensionID, result.Run, "chat", "turn", nil, nil, core); err != nil {
				t.Fatal(err)
			}
			if supplementary != before {
				t.Fatal("repeated input was analyzed again")
			}
			raw, err := tasks.ExtensionState(t.Context(), affectExtensionID, chat, "maya", "owner")
			if err != nil || !json.Valid(raw) {
				t.Fatal(err)
			}
		})
	}
}
