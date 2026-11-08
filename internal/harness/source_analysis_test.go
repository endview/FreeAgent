package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceAnalysisIndependentMethodAndModelBinding(t *testing.T) {
	calls := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if req.DenyOperations && (len(req.Tools)+len(req.Memory)+len(req.Extensions) != 0 || !strings.Contains(req.Messages[len(req.Messages)-1].Content, "incident-review")) {
			t.Fatal("method was not isolated or selection was lost")
		}
		return ModelResponse{Content: "Verify the original result; UNKNOWN is not success."}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	source, err := tasks.Submit(t.Context(), chat, "source", "selected source")
	if err != nil {
		t.Fatal(err)
	}
	selection := SourceAnalysisSelection{Skill: "incident-review", RunIDs: []string{source.Run.ID}}
	if _, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection); err == nil {
		t.Fatal("uninstalled method accepted")
	}
	dir := writeSkill(t, filepath.Join(core.Store.DataDir(), "skills"), "incident-review", "Review an explicit incident", "manual", "Summarize the selected facts and unknown outcomes.")
	path := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "trigger: manual", "trigger: manual\ninvocation: selected_sources", 1))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	core.Config.Skills.Enabled = map[string]bool{"incident-review": true}
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection)
	if err != nil || preview.Selection.ModelID == "" || preview.ModelHash == "" {
		t.Fatal(preview, err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "bypass", "/incident-review inspect everything"); err == nil {
		t.Fatal("source-analysis method bypassed bounded invocation")
	}
	result, err := tasks.AnalyzeSources(t.Context(), chat, "incident", preview.Selection, preview.Digest)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result, err)
	}
	var snapshot runSnapshot
	if json.Unmarshal([]byte(result.Run.ConfigJSON), &snapshot) != nil || !snapshot.AnalysisOnly {
		t.Fatal("analysis snapshot missing")
	}
	if _, err := tasks.SaveAnalysisMemory(t.Context(), chat, result.Run.ID, "save-analysis", "Incident", result.Answer); err != nil {
		t.Fatal(err)
	}
	// Changing the default does not retarget a preview whose model is frozen.
	core.Config.Models["alternate"] = core.Config.Models[preview.Selection.ModelID]
	core.Config.DefaultModel = "alternate"
	if _, err := tasks.AnalyzeSources(t.Context(), chat, "incident", preview.Selection, preview.Digest); err != nil {
		t.Fatal("explicit model changed with default", err)
	}
	if calls != 2 {
		t.Fatal("duplicate analysis called model again", calls)
	}
	// Changing the bound model configuration must invalidate the old preview.
	delete(core.Config.Models, preview.Selection.ModelID)
	if _, err := tasks.AnalyzeSources(t.Context(), chat, "changed", preview.Selection, preview.Digest); err == nil {
		t.Fatal("missing binding accepted")
	}
}

func TestSourceAnalysisInvocationValidation(t *testing.T) {
	for _, metadata := range []string{"trigger: auto\ninvocation: selected_sources", "trigger: manual\ninvocation: arbitrary_workflow"} {
		if _, _, _, _, _, err := parseSkillDocument([]byte("---\nname: invalid\ndescription: fixture\n" + metadata + "\n---\nmethod")); err == nil {
			t.Fatal("invalid invocation accepted", metadata)
		}
	}
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if _, err := tasks.SaveAnalysisMemory(t.Context(), chat, "missing", "save", "Title", "body"); err == nil || errors.Is(err, ErrRequestKey) {
		t.Fatal("missing analysis accepted", err)
	}
}
