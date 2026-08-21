package s3eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const ScenarioSchemaVersionV1 = "freeagent.s3-eval-scenario/v1"

var ErrInvalidScenario = errors.New("s3eval: invalid scenario")

// CaseKindV1 labels a scenario case without adding a quality judge to the
// experiment harness.
type CaseKindV1 string

const (
	CaseKindCleanV1 CaseKindV1 = "clean"
	CaseKindTrapV1  CaseKindV1 = "trap"
)

// ScenarioTaskV1 is stable scenario source text. Runtime-generated admission
// identity and timing fields intentionally do not belong to this wire value.
type ScenarioTaskV1 struct {
	TaskID      string     `json:"task_id"`
	WorkspaceID string     `json:"workspace_id"`
	CaseKind    CaseKindV1 `json:"case_kind"`
	Message     string     `json:"message"`
}

// ScenarioV1 is the strict, versioned source for one exactly-three-Workspace
// S3-C experiment. It contains no RequestID, deadline, Run ID or Attempt ID.
type ScenarioV1 struct {
	SchemaVersion string           `json:"schema_version"`
	ExperimentID  string           `json:"experiment_id"`
	AgentID       string           `json:"agent_id"`
	ProfileID     string           `json:"profile_id"`
	Tasks         []ScenarioTaskV1 `json:"tasks"`
}

// ParseScenarioV1 decodes exactly one JSON value, rejects unknown fields and
// validates the normalized v1 scenario contract.
func ParseScenarioV1(payload []byte) (ScenarioV1, error) {
	var scenario ScenarioV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return ScenarioV1{}, fmt.Errorf("%w: decode: %v", ErrInvalidScenario, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ScenarioV1{}, fmt.Errorf("%w: trailing JSON", ErrInvalidScenario)
	}
	if err := scenario.Validate(); err != nil {
		return ScenarioV1{}, err
	}
	return scenario, nil
}

// Validate checks the complete normalized v1 scenario without mutating it.
func (scenario ScenarioV1) Validate() error {
	if scenario.SchemaVersion != ScenarioSchemaVersionV1 {
		return fmt.Errorf(
			"%w: schema_version must be %q",
			ErrInvalidScenario,
			ScenarioSchemaVersionV1,
		)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "experiment_id", value: scenario.ExperimentID},
		{name: "agent_id", value: scenario.AgentID},
		{name: "profile_id", value: scenario.ProfileID},
	} {
		if err := validateScenarioID(field.name, field.value); err != nil {
			return err
		}
	}
	if len(scenario.Tasks) != WorkspaceCount {
		return fmt.Errorf(
			"%w: tasks must contain exactly %d entries",
			ErrInvalidScenario,
			WorkspaceCount,
		)
	}
	seenTasks := make(map[string]struct{}, WorkspaceCount)
	seenWorkspaces := make(map[string]struct{}, WorkspaceCount)
	for index, task := range scenario.Tasks {
		if err := validateScenarioID("task_id", task.TaskID); err != nil {
			return fmt.Errorf("%w: task %d: %v", ErrInvalidScenario, index, err)
		}
		if _, duplicate := seenTasks[task.TaskID]; duplicate {
			return fmt.Errorf(
				"%w: duplicate task_id %q",
				ErrInvalidScenario,
				task.TaskID,
			)
		}
		seenTasks[task.TaskID] = struct{}{}
		if err := validateScenarioID("workspace_id", task.WorkspaceID); err != nil {
			return fmt.Errorf("%w: task %d: %v", ErrInvalidScenario, index, err)
		}
		if _, duplicate := seenWorkspaces[task.WorkspaceID]; duplicate {
			return fmt.Errorf(
				"%w: duplicate workspace_id %q",
				ErrInvalidScenario,
				task.WorkspaceID,
			)
		}
		seenWorkspaces[task.WorkspaceID] = struct{}{}
		switch task.CaseKind {
		case CaseKindCleanV1, CaseKindTrapV1:
		default:
			return fmt.Errorf(
				"%w: task %d case_kind must be %q or %q",
				ErrInvalidScenario,
				index,
				CaseKindCleanV1,
				CaseKindTrapV1,
			)
		}
		if task.Message != strings.TrimSpace(task.Message) {
			return fmt.Errorf(
				"%w: task %d message must not have surrounding whitespace",
				ErrInvalidScenario,
				index,
			)
		}
		if _, _, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          task.Message,
		}); err != nil {
			return fmt.Errorf(
				"%w: task %d message: %v",
				ErrInvalidScenario,
				index,
				err,
			)
		}
	}
	return nil
}

// ScenarioToExperimentInput applies caller-owned tenancy and principal scope
// while leaving RequestID and Deadline zero so Composite Chat freezes them.
func ScenarioToExperimentInput(
	scenario ScenarioV1,
	tenantID string,
	principalID string,
) (ExperimentInput, error) {
	if err := scenario.Validate(); err != nil {
		return ExperimentInput{}, err
	}
	if err := validateScenarioID("tenant_id", tenantID); err != nil {
		return ExperimentInput{}, err
	}
	if err := validateScenarioID("principal_id", principalID); err != nil {
		return ExperimentInput{}, err
	}
	var result ExperimentInput
	for index, task := range scenario.Tasks {
		result.Tasks[index] = WorkspaceTask{ChatInput: localchat.ChatInput{
			TenantID:    tenantID,
			PrincipalID: principalID,
			WorkspaceID: task.WorkspaceID,
			AgentID:     scenario.AgentID,
			ProfileID:   scenario.ProfileID,
			Message:     task.Message,
		}}
	}
	return result, nil
}

func validateScenarioID(name string, value string) error {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"%w: %s must be canonical, non-empty and at most %d bytes",
			ErrInvalidScenario,
			name,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf(
				"%w: %s contains a control character",
				ErrInvalidScenario,
				name,
			)
		}
	}
	return nil
}
