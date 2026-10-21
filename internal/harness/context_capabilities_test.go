package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCapabilityAssemblyPurposeRequestsPreservesBaselineWhenDisabled(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	run := Run{Prompt: "plain request"}
	base := ModelRequest{
		ModelID:         service.Config.DefaultModel,
		ModelRevision:   "revision",
		Purpose:         "planning",
		Messages:        []ModelMessage{{Role: "system", Content: "system"}, {Role: "user", Content: `{"authorized":{"mode":"ask"}}`}},
		Tools:           []ToolDefinition{{Name: "file_read", Effect: EffectRead}},
		MaxOutputTokens: 32,
	}

	for _, purpose := range []string{"", "planning", "replanning", "synthesis", "assessment"} {
		request := base
		request.Purpose = purpose
		want := request
		got, err := service.prepareCapabilityRequest(context.Background(), run, request)
		if err != nil {
			t.Fatalf("purpose=%q: %v", purpose, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("purpose=%q changed disabled baseline:\n got=%+v\nwant=%+v", purpose, got, want)
		}
	}
}

func TestCapabilityAssemblyPurposeRequestsUseSharedSkillRules(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	writeSkill(t, data+"/skills", "manual", "manual helper", "manual", "manual body")
	writeSkill(t, data+"/skills", "auto", "automatic helper", "auto", "automatic body")
	service.Config.Skills.Enabled = map[string]bool{"manual": true, "auto": true}
	service.capabilities = &CapabilitySelection{Skills: []string{"manual", "auto"}}

	settings := DefaultCollaborationSettings()
	record := collaborationRecord{
		ParentID: "parent",
		Plan:     CollaborationPlan{Objective: "/manual objective"},
		Revision: 1,
	}
	report := CollaborationReport{Revision: 1, Digest: "digest"}
	binding, err := service.binding()
	if err != nil {
		t.Fatal(err)
	}
	service.autoPolicy = &autoRunPolicy{
		Models:         []ModelBinding{binding},
		MaxAssessments: 1,
		MaxUpgrades:    1,
	}
	service.routing = []RoutingDecision{{Candidates: []string{binding.ID}}}

	requests := map[string]ModelRequest{}
	requests["planning"], err = service.planningRequest(Run{Prompt: "/manual objective"}, settings)
	if err != nil {
		t.Fatal(err)
	}
	requests["replanning"], err = replanRequest(service, record, report)
	if err != nil {
		t.Fatal(err)
	}
	requests["synthesis"], err = synthesisRequest(service, record, report)
	if err != nil {
		t.Fatal(err)
	}
	requests["assessment"], err = service.assessmentRequest(Run{Prompt: "/manual choose a model"})
	if err != nil {
		t.Fatal(err)
	}

	for purpose, request := range requests {
		t.Run(purpose, func(t *testing.T) {
			if request.Purpose != purpose {
				t.Fatalf("purpose=%q", request.Purpose)
			}
			assertSharedSkillAssembly(t, request)
		})
	}
}

func TestCompactContextKeepsSkillsWhenModelHasNoTools(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	writeSkill(t, data+"/skills", "auto", "automatic helper", "auto", "automatic body")
	service.Config.Skills.Enabled = map[string]bool{"auto": true}
	service.capabilities = &CapabilitySelection{Skills: []string{"auto"}}
	config := service.Config
	model := config.Models[config.DefaultModel]
	model.Capabilities.Tools = "none"
	config.Models[config.DefaultModel] = model
	service.Config = config

	request, err := service.buildContext(context.Background(), Run{Prompt: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 0 {
		t.Fatalf("tools were retained for a no-tools model: %+v", request.Tools)
	}
	if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "auto: automatic helper") {
		t.Fatalf("automatic skill index missing from no-tools request: %+v", request.Messages)
	}
	if strings.Contains(request.Messages[0].Content, "automatic body") {
		t.Fatal("automatic skill body was injected instead of remaining loadable")
	}
}

func assertSharedSkillAssembly(t *testing.T, request ModelRequest) {
	t.Helper()
	if len(request.Messages) < 3 {
		t.Fatalf("messages=%+v", request.Messages)
	}
	if !strings.Contains(request.Messages[0].Content, "auto: automatic helper") {
		t.Fatalf("automatic skill index missing: %+v", request.Messages)
	}
	if strings.Contains(request.Messages[0].Content, "automatic body") {
		t.Fatal("automatic skill body was injected into system")
	}
	if !strings.Contains(request.Messages[1].Content, "manual body") {
		t.Fatalf("manual skill tail missing: %+v", request.Messages)
	}
	if request.Messages[len(request.Messages)-1].Role != "user" ||
		strings.Contains(request.Messages[len(request.Messages)-1].Content, "manual body") {
		// Purpose-specific requests keep their structured input after the shared
		// manual tail block.
		t.Fatalf("manual tail displaced structured user input: %+v", request.Messages)
	}
	if len(request.Extensions) != 1 || request.Extensions[0].Source != "skill" ||
		request.Extensions[0].Name != "manual" || request.Extensions[0].Placement != "tail" {
		t.Fatalf("extension uses=%+v", request.Extensions)
	}
}
