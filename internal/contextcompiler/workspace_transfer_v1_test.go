package contextcompiler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestCompileWorkspaceTaskSummaryV1UsesExactTaskAndDeterministicBound(t *testing.T) {
	taskText := "前端接口\t需要稳定。\n" + strings.Repeat("长上下文🙂", 6000)
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          taskText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	taskRef := contentDigest("TASK_INPUT", jsonMediaType, taskCanonical)
	first, firstCanonical, err := CompileWorkspaceTaskSummaryV1(
		taskRef,
		taskCanonical,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, secondCanonical, err := CompileWorkspaceTaskSummaryV1(
		taskRef,
		taskCanonical,
		nil,
	)
	if err != nil || first != second || !bytes.Equal(firstCanonical, secondCanonical) {
		t.Fatalf("Workspace task summary is not deterministic: %v", err)
	}
	if first.SourceTaskInputRef != taskRef || first.RepairRound != 0 ||
		len(firstCanonical) > int(corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1) {
		t.Fatalf("unexpected bounded Workspace task summary: %+v", first)
	}
	var summary struct {
		SchemaVersion string `json:"schema_version"`
		TaskExcerpt   string `json:"task_excerpt"`
	}
	if err := json.Unmarshal([]byte(first.Summary), &summary); err != nil ||
		summary.SchemaVersion != workspaceTaskExtractSchemaVersionV1 ||
		summary.TaskExcerpt == "" ||
		!strings.Contains(summary.TaskExcerpt, workspaceTaskOmissionV1) {
		t.Fatalf("unexpected Workspace task extract: %+v, %v", summary, err)
	}
}

func TestCompileWorkspaceTaskSummaryV1KeepsRepairBasisWhole(t *testing.T) {
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "修复后端接口，并保持原并发约束。",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	taskRef := contentDigest("TASK_INPUT", jsonMediaType, taskCanonical)
	repairSummary := `{"bounded_reason":"race","issue_codes":["CONSISTENCY"],"schema_version":"composite-repair-basis-summary/v1","slot_id":"backend"}`
	_, repairCanonical, err := corecontract.NewWorkspaceTaskSummaryV1(
		corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: taskRef,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			PreviousSetDigest:  strings.Repeat("a", 64),
			VerdictRef:         strings.Repeat("b", 64),
			Summary:            repairSummary,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, canonical, err := CompileWorkspaceTaskSummaryV1(
		taskRef,
		taskCanonical,
		repairCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		compiled.PreviousSetDigest != strings.Repeat("a", 64) ||
		compiled.VerdictRef != strings.Repeat("b", 64) {
		t.Fatalf("repair lineage changed: %+v", compiled)
	}
	var summary struct {
		SchemaVersion string          `json:"schema_version"`
		TaskExcerpt   string          `json:"task_excerpt"`
		RepairBasis   json.RawMessage `json:"repair_basis"`
	}
	if err := json.Unmarshal([]byte(compiled.Summary), &summary); err != nil ||
		summary.SchemaVersion != workspaceRepairExtractSchemaV1 ||
		summary.TaskExcerpt == "" ||
		!bytes.Equal(summary.RepairBasis, []byte(repairSummary)) ||
		len(canonical) > int(corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1) {
		t.Fatalf("repair summary changed: %+v, %v", summary, err)
	}
}

func TestCompileWorkspaceTaskSummaryV1RejectsWrongSourceAndUntrustedBasis(t *testing.T) {
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "bounded task",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompileWorkspaceTaskSummaryV1(
		strings.Repeat("f", 64),
		taskCanonical,
		nil,
	); err == nil {
		t.Fatal("Workspace task compiler accepted the wrong TASK_INPUT ref")
	}
	taskRef := contentDigest("TASK_INPUT", jsonMediaType, taskCanonical)
	if _, _, err := CompileWorkspaceTaskSummaryV1(
		taskRef,
		taskCanonical,
		[]byte(`{"schema_version":"workspace-task-summary/v1"}`),
	); err == nil {
		t.Fatal("Workspace task compiler accepted caller-authored repair bytes")
	}
}
