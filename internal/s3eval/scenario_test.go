package s3eval

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseScenarioV1StrictAndNormalized(t *testing.T) {
	t.Parallel()

	scenario, err := ParseScenarioV1([]byte(validScenarioJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if scenario.SchemaVersion != ScenarioSchemaVersionV1 ||
		scenario.ExperimentID != "s3c-pair-01" ||
		scenario.AgentID != "s3c.architect" ||
		scenario.ProfileID != "s3c.coordinator" ||
		len(scenario.Tasks) != WorkspaceCount ||
		scenario.Tasks[0].CaseKind != CaseKindCleanV1 ||
		scenario.Tasks[1].CaseKind != CaseKindTrapV1 {
		t.Fatalf("scenario=%+v", scenario)
	}

	twoTasks := strings.Replace(
		validScenarioJSON(),
		`,
    {"task_id":"task-c","workspace_id":"workspace-c","case_kind":"clean","message":"Network task"}`,
		``,
		1,
	)
	fourTasks := strings.Replace(
		validScenarioJSON(),
		"\n  ]",
		`,
    {"task_id":"task-d","workspace_id":"workspace-d","case_kind":"clean","message":"Extra task"}
  ]`,
		1,
	)
	tests := map[string]string{
		"unknown top-level field": strings.Replace(
			validScenarioJSON(),
			`"agent_id":"s3c.architect",`,
			`"agent_id":"s3c.architect","request_id":"dynamic",`,
			1,
		),
		"unknown task field": strings.Replace(
			validScenarioJSON(),
			`"case_kind":"clean",`,
			`"case_kind":"clean","deadline":"2026-08-04T00:00:00Z",`,
			1,
		),
		"trailing value": validScenarioJSON() + ` {}`,
		"two tasks":      twoTasks,
		"four tasks":     fourTasks,
		"duplicate workspace": strings.Replace(
			validScenarioJSON(),
			`"workspace_id":"workspace-c"`,
			`"workspace_id":"workspace-a"`,
			1,
		),
		"duplicate task": strings.Replace(
			validScenarioJSON(),
			`"task_id":"task-c"`,
			`"task_id":"task-a"`,
			1,
		),
		"unknown case kind": strings.Replace(
			validScenarioJSON(),
			`"case_kind":"trap"`,
			`"case_kind":"mixed"`,
			1,
		),
		"non-normalized id": strings.Replace(
			validScenarioJSON(),
			`"experiment_id":"s3c-pair-01"`,
			`"experiment_id":"s3c-pair-01 "`,
			1,
		),
		"non-NFC message": strings.Replace(
			validScenarioJSON(),
			`Frontend task`,
			"Cafe\u0301 frontend task",
			1,
		),
	}
	for name, payload := range tests {
		name, payload := name, payload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseScenarioV1([]byte(payload))
			if !errors.Is(err, ErrInvalidScenario) {
				t.Fatalf("ParseScenarioV1 error=%v", err)
			}
		})
	}
}

func TestScenarioToExperimentInputLeavesDynamicIdentityForService(t *testing.T) {
	t.Parallel()

	scenario, err := ParseScenarioV1([]byte(validScenarioJSON()))
	if err != nil {
		t.Fatal(err)
	}
	input, err := ScenarioToExperimentInput(
		scenario,
		"tenant-s3c",
		"principal-s3c",
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range input.Tasks {
		want := scenario.Tasks[index]
		if task.ChatInput.TenantID != "tenant-s3c" ||
			task.ChatInput.PrincipalID != "principal-s3c" ||
			task.ChatInput.WorkspaceID != want.WorkspaceID ||
			task.ChatInput.AgentID != scenario.AgentID ||
			task.ChatInput.ProfileID != scenario.ProfileID ||
			task.ChatInput.Message != want.Message ||
			task.ChatInput.RequestID != "" ||
			!task.ChatInput.Deadline.IsZero() {
			t.Fatalf("task %d input=%+v", index, task.ChatInput)
		}
	}

	if _, err := ScenarioToExperimentInput(
		scenario,
		" tenant-s3c",
		"principal-s3c",
	); !errors.Is(err, ErrInvalidScenario) {
		t.Fatalf("non-normalized caller scope error=%v", err)
	}
}

func TestReportJSONUsesStableSnakeCase(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(ExperimentReport{Families: []FamilyReport{{
		WorkspaceID: "workspace-a",
		Reply:       "answer",
		Failure:     "",
		Tokens: TokenReport{Totals: TokenTotalsReport{
			Input: pointerTo(uint64(1)),
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, key := range []string{
		`"service_order"`,
		`"workspace_id"`,
		`"wall_elapsed"`,
		`"reply"`,
		`"failure"`,
		`"input_tokens"`,
		`"cache_hit_ratio"`,
	} {
		if !strings.Contains(text, key) {
			t.Fatalf("report JSON missing %s: %s", key, text)
		}
	}
	for _, key := range []string{`"ServiceOrder"`, `"WorkspaceID"`, `"Input"`} {
		if strings.Contains(text, key) {
			t.Fatalf("report JSON contains unstable key %s: %s", key, text)
		}
	}
}

func validScenarioJSON() string {
	return `{
  "schema_version":"freeagent.s3-eval-scenario/v1",
  "experiment_id":"s3c-pair-01",
  "agent_id":"s3c.architect",
  "profile_id":"s3c.coordinator",
  "tasks":[
    {"task_id":"task-a","workspace_id":"workspace-a","case_kind":"clean","message":"Frontend task"},
    {"task_id":"task-b","workspace_id":"workspace-b","case_kind":"trap","message":"Backend task"},
    {"task_id":"task-c","workspace_id":"workspace-c","case_kind":"clean","message":"Network task"}
  ]
}`
}

func pointerTo[T any](value T) *T {
	return &value
}
