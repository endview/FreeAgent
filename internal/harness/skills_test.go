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

func writeSkill(t *testing.T, root, name, description, trigger, body string) string {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: " + name + "\ndescription: " + description + "\n"
	if trigger != "" {
		document += "trigger: " + trigger + "\n"
	}
	document += "---\n" + body
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestSkillParserAndTrustBoundaries(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	userRoot := filepath.Join(data, "skills")
	writeSkill(t, userRoot, "weekly", "write a weekly report", "auto", "User skill body.\n")
	workspaceRoot := filepath.Join(workspace, ".freeagent", "skills")
	workspaceSkill := writeSkill(t, workspaceRoot, "weekly", "workspace report", "manual", "Workspace skill body.\n")
	if err := os.WriteFile(filepath.Join(workspaceSkill, "template.txt"), []byte("template"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := scanSkills(data, workspace, service.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Skills["weekly"].Body; got != "User skill body.\n" {
		t.Fatalf("untrusted workspace skill was loaded: %q", got)
	}
	if len(registry.Skills["weekly"].Files) != 0 {
		t.Fatalf("unexpected untrusted attachments: %+v", registry.Skills["weekly"].Files)
	}

	canonical, err := canonicalDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	service.Config.Skills.TrustedWorkspaces = map[string]bool{canonical: true}
	registry, err = scanSkills(data, workspace, service.Config)
	if err != nil {
		t.Fatal(err)
	}
	item := registry.Skills["weekly"]
	if item.Body != "Workspace skill body.\n" || item.Source != "workspace" || item.Trigger != skillTriggerManual {
		t.Fatalf("workspace override not selected: %+v", item)
	}
	if len(item.Files) != 1 || item.Files[0].Path != "template.txt" {
		t.Fatalf("workspace attachment list=%+v", item.Files)
	}
	if _, _, _, _, err := parseSkillDocument([]byte("---\nname: bad\n---\nbody")); err == nil {
		t.Fatal("skill without description was accepted")
	}
}

func TestSkillSelectionAndManualTailAssembly(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	writeSkill(t, filepath.Join(data, "skills"), "manual", "manual helper", "manual", "Use this current-request checklist.\n")
	writeSkill(t, filepath.Join(data, "skills"), "auto", "automatic helper", "auto", "Never injected automatically.\n")
	service.Config.Skills.Enabled = map[string]bool{"manual": true, "auto": true}
	service.capabilities = &CapabilitySelection{Skills: []string{"manual", "auto"}}
	run := Run{Prompt: "/manual finish the report"}
	request, err := service.buildContext(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 3 || request.Messages[1].Role != "user" ||
		!strings.Contains(request.Messages[1].Content, "Use this current-request checklist.") ||
		request.Messages[2].Content != run.Prompt {
		t.Fatalf("manual skill was not inserted before user prompt: %+v", request.Messages)
	}
	if !strings.Contains(request.Messages[0].Content, "auto: automatic helper") ||
		strings.Contains(request.Messages[0].Content, "Never injected automatically.") {
		t.Fatalf("auto skill index has wrong content: %q", request.Messages[0].Content)
	}
	if len(request.Extensions) != 1 || request.Extensions[0].Name != "manual" ||
		request.Extensions[0].Source != "skill" || request.Extensions[0].Placement != "tail" {
		t.Fatalf("skill extension use=%+v", request.Extensions)
	}
	definitions := service.runtimeTools("", "").Definitions()
	var skillDefinition ToolDefinition
	for _, definition := range definitions {
		if definition.Name == "skill_load" {
			skillDefinition = definition
		}
	}
	if skillDefinition.Name == "" {
		t.Fatalf("skill_load missing from definitions: %+v", definitions)
	}
	prepared, err := service.runtimeTools("run", "attempt").Prepare(context.Background(),
		makeCall("load", "skill_load", map[string]string{"name": "auto"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(context.Background())
	if err != nil || result.IsError {
		t.Fatalf("skill_load result=%+v err=%v", result, err)
	}
	var payload struct {
		Name    string      `json:"name"`
		Content string      `json:"content"`
		Files   []skillFile `json:"files"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil ||
		payload.Name != "auto" || payload.Content != "Never injected automatically.\n" {
		t.Fatalf("skill_load payload=%s err=%v", result.Content, err)
	}
	if len(result.Extensions) != 1 || result.Extensions[0].Name != "auto" {
		t.Fatalf("skill_load extension use=%+v", result.Extensions)
	}
}

func TestSkillLoadRejectsUnselectedAndUnsafeAttachment(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	directory := writeSkill(t, filepath.Join(data, "skills"), "safe", "safe helper", "auto", "body")
	if err := os.WriteFile(filepath.Join(directory, "note.txt"), []byte("note"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Skills.Enabled = map[string]bool{"safe": true}
	service.capabilities = &CapabilitySelection{}
	if _, err := service.buildContext(context.Background(), Run{Prompt: "plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.runtimeTools("", "").Prepare(context.Background(),
		makeCall("load", "skill_load", map[string]string{"name": "safe"})); err == nil {
		t.Fatal("unselected skill_load was accepted")
	}

	service.capabilities = &CapabilitySelection{Skills: []string{"safe"}}
	if err := service.loadSkills(context.Background(), "plain"); err != nil {
		t.Fatal(err)
	}
	_, err := service.runtimeTools("", "").Prepare(context.Background(),
		makeCall("load", "skill_load", map[string]string{"name": "safe", "file": "../SKILL.md"}))
	if err == nil {
		t.Fatal("path traversal attachment was accepted")
	}
	if _, err := service.runtimeTools("", "").Prepare(context.Background(),
		makeCall("load", "skill_load", map[string]string{"name": "safe", "file": "missing.txt"})); err == nil {
		t.Fatal("undiscovered attachment was accepted")
	}
}

func TestSkillLoadOversizedAttachmentIsCertainToolError(t *testing.T) {
	var requests []ModelRequest
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("load", "skill_load",
				map[string]string{"name": "large", "file": "large.txt"})}}, nil
		}
		return ModelResponse{Content: "continued without the attachment"}, nil
	})
	service, _, _ := testHarness(t, model)
	directory := writeSkill(t, filepath.Join(service.Store.DataDir(), "skills"), "large", "large helper", "auto", "body")
	size := service.limits().MaxToolResult + 1024
	if size > maxSkillFileBytes {
		t.Fatalf("fixture size %d exceeds skill attachment limit", size)
	}
	if err := os.WriteFile(filepath.Join(directory, "large.txt"), []byte(strings.Repeat("a", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Skills.Enabled = map[string]bool{"large": true}
	service.capabilities = &CapabilitySelection{Skills: []string{"large"}}

	if err := service.loadSkills(t.Context(), "plain"); err != nil {
		t.Fatal(err)
	}
	prepared, err := service.runtimeTools("", "").Prepare(t.Context(),
		makeCall("load", "skill_load", map[string]string{"name": "large", "file": "large.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	var callErr *CallError
	if _, err := prepared.Execute(t.Context()); !errors.As(err, &callErr) || !callErr.Certain {
		t.Fatalf("oversized skill_load err=%v, want certain CallError", err)
	}

	result, err := service.Run(t.Context(), conversation(t, service), "large-skill", "use the large skill")
	if err != nil || result.Run.Status != "completed" || result.Answer != "continued without the attachment" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(requests) != 2 {
		t.Fatalf("model requests=%d, want 2", len(requests))
	}
	found := false
	for _, message := range requests[1].Messages {
		found = found || message.Role == "tool" && strings.Contains(message.Content, "exceeds the tool result limit")
	}
	if !found {
		t.Fatalf("certain skill_load error was not returned to the model: %+v", requests[1].Messages)
	}
}
