package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestCollaborationAuthorizedResultsAndDependencyOnlyIsolation(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(map[bool]string{false: "order_only", true: "granted"}[granted], func(t *testing.T) {
			collab, _, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				raw, _ := json.Marshal(request)
				if strings.Contains(string(raw), "PARENT_PRIVATE") {
					t.Fatal("parent private context leaked")
				}
				if strings.Contains(request.Messages[1].Content, "CHILD_ONE") {
					return ModelResponse{Content: `{"summary":"PUBLIC_RESULT","findings":["finding"],"references":["source"],"questions":["unresolved"]}`}, nil
				}
				if strings.Contains(string(raw), "PUBLIC_RESULT") != granted ||
					strings.Contains(string(raw), "CHILD_ONE") {
					t.Fatalf("result grant leaked or was not delivered: %s", raw)
				}
				return ModelResponse{Content: "reviewed"}, nil
			}))
			plan = analysisPlan(plan)
			plan.Children[1].DependsOn = []string{"one"}
			if granted {
				plan.Children[1].Results = []ResultReference{{Source: "one", Scope: "summary", MaxBytes: 64}}
			}
			id := createCollaboration(t, collab, plan)
			report, err := collab.Continue(t.Context(), id)
			if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 2 ||
				report.Usage.ToolCalls != 0 || len(report.Children[1].Delivered) != map[bool]int{false: 0, true: 1}[granted] {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if granted && report.Children[1].Delivered[0].OutputHash != report.Children[0].Result.OutputHash {
				t.Fatal("delivery lost output provenance")
			}
		})
	}
}

func TestCollaborationResultReferenceValidationAndUTF8Bounds(t *testing.T) {
	child := ChildSpec{Name: "review", DependsOn: []string{"source"}}
	valid := ResultReference{Source: "source", Scope: "summary", MaxBytes: 8}
	for _, ref := range []ResultReference{
		{Source: "other", Scope: "summary", MaxBytes: 8},
		{Source: "source", Scope: "private", MaxBytes: 8},
		{Source: "source", Scope: "summary", Offset: -1, MaxBytes: 8},
		{Source: "source", Scope: "summary", MaxBytes: 0},
		{Source: "source", Scope: "summary", MaxBytes: 32769},
	} {
		child.Results = []ResultReference{ref}
		if err := validateResultReferences(child); err == nil {
			t.Fatalf("invalid grant accepted: %+v", ref)
		}
	}
	child.Results, child.Context = []ResultReference{valid, valid}, ""
	if err := validateResultReferences(child); err == nil {
		t.Fatal("duplicate grant accepted")
	}
	child.Results, child.Context = []ResultReference{valid}, strings.Repeat("x", 32768)
	if !errors.Is(validateResultReferences(child), ErrContextLimit) {
		t.Fatal("combined context cap escaped")
	}
	source := ChildReport{Spec: ChildSpec{Name: "source"}, Run: Run{ID: "run", Output: "\u4e2d\u6587abc"}}
	ref := valid
	ref.MaxBytes = 4
	item, err := boundedResult(source, ref)
	if err != nil || len(item.Content) != 3 || item.NextOffset != 3 || !item.Truncated || !utf8.ValidString(item.Content) {
		t.Fatalf("UTF-8 cap=%+v %v", item, err)
	}
	ref.Offset = 1
	if _, err := boundedResult(source, ref); !errors.Is(err, ErrResultReference) {
		t.Fatal("mid-codepoint offset accepted")
	}
}

func TestCollaborationResultFreezeAtomicAndUnavailableSources(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	plan = analysisPlan(plan)
	plan.Children[1].DependsOn = []string{"one"}
	plan.Children[1].Results = []ResultReference{{Source: "one", Scope: "summary", MaxBytes: 1024}}
	id := createCollaboration(t, collab, plan)
	if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
		t.Fatal(err)
	}
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Children[0].Run.Status != "completed" {
		t.Fatalf("source=%+v %v", report, err)
	}
	if err := collab.Control(t.Context(), id, "two", "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER freeze_fault BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT,'freeze failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := collab.deliverResults(t.Context(), core, id, "two", 1); err == nil {
		t.Fatal("freeze fault ignored")
	}
	after, _ := collab.Inspect(t.Context(), id)
	if after.Children[1].Run.Prompt != report.Children[1].Run.Prompt || len(after.Children[1].Delivered) != 0 {
		t.Fatal("failed transaction partially delivered results")
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `DROP TRIGGER freeze_fault`); err != nil {
		t.Fatal(err)
	}
	if err := collab.deliverResults(t.Context(), core, id, "two", 1); err != nil {
		t.Fatal(err)
	}
	after, _ = collab.Inspect(t.Context(), id)
	frozen := after.Children[1].Run.Prompt
	if err := collab.deliverResults(t.Context(), core, id, "two", 1); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "abandoned", "unknown"} {
		if err := core.Store.FinishRun(t.Context(), report.Children[0].Run.ID, status, "fixture invalidation"); err != nil {
			t.Fatal(err)
		}
		if err := collab.deliverResults(t.Context(), core, id, "two", 1); !errors.Is(err, ErrResultReference) {
			t.Fatalf("%s source was delivered: %v", status, err)
		}
		if err := core.Store.FinishRun(t.Context(), report.Children[0].Run.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := collab.activate(t.Context(), id, []string{"two"}); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.FinishRun(t.Context(), report.Children[1].Run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.FinishRun(t.Context(), report.Children[0].Run.ID, "unknown", "fixture invalidation"); err != nil {
		t.Fatal(err)
	}
	run, _ := core.Store.GetRun(t.Context(), report.Children[1].Run.ID)
	if _, err := core.Store.startGuardedModel(t.Context(), run, `{}`, 6, collaborationGuard{parentID: id, childName: "two"}); !errors.Is(err, ErrResultReference) {
		t.Fatalf("dispatch did not recheck source: %v", err)
	}
	run, _ = core.Store.GetRun(t.Context(), run.ID)
	if run.Steps != 0 || run.Prompt != frozen {
		t.Fatal("refused dispatch changed steps or frozen context")
	}
}

func TestCollaborationFrozenResultIntegrityBeforeDispatch(t *testing.T) {
	for _, scenario := range []string{"unchanged", "content", "section_hash", "truncated", "source_evidence", "source_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
			plan.Children[1].DependsOn = []string{"one"}
			plan.Children[1].Results = []ResultReference{{Source: "one", Scope: "evidence", MaxBytes: 1024}}
			id := createCollaboration(t, collab, plan)
			if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
				t.Fatal(err)
			}
			report, err := collab.Continue(t.Context(), id)
			if err != nil || report.Children[0].Run.Status != "completed" {
				t.Fatalf("source did not complete: %+v %v", report, err)
			}
			if err := collab.Control(t.Context(), id, "two", "resume"); err != nil {
				t.Fatal(err)
			}
			if err := collab.deliverResults(t.Context(), core, id, "two", 1); err != nil {
				t.Fatal(err)
			}
			if err := collab.change(t.Context(), id, func(record *collaborationRecord, tx *sql.Tx) error {
				switch scenario {
				case "content":
					item := &record.Children[1].Delivered[0]
					item.Content = "x" + item.Content[1:]
				case "section_hash":
					record.Children[1].Delivered[0].SectionHash = strings.Repeat("0", 64)
				case "truncated":
					item := &record.Children[1].Delivered[0]
					item.Truncated = !item.Truncated
				case "source_evidence":
					attempts, err := listAttempts(t.Context(), tx, record.Children[0].RunID)
					if err != nil {
						return err
					}
					for _, attempt := range attempts {
						if attempt.Kind == "tool" {
							var result ToolResult
							if err := json.Unmarshal([]byte(attempt.ResponseJSON), &result); err != nil {
								return err
							}
							result.Content += " "
							raw, _ := json.Marshal(result)
							_, err := tx.ExecContext(t.Context(), `UPDATE attempts SET response_json=? WHERE id=?`, string(raw), attempt.ID)
							return err
						}
					}
					t.Fatal("source has no tool attempt")
				case "source_unknown":
					_, err := tx.ExecContext(t.Context(), `UPDATE attempts SET status='unknown' WHERE run_id=? AND kind='tool'`, record.Children[0].RunID)
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := collab.activate(t.Context(), id, []string{"two"}); err != nil {
				t.Fatal(err)
			}
			if err := core.Store.FinishRun(t.Context(), report.Children[1].Run.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			run, _ := core.Store.GetRun(t.Context(), report.Children[1].Run.ID)
			before, _ := loadCollaboration(t.Context(), core.Store.db, id)
			attempt, err := core.Store.startGuardedModel(t.Context(), run, `{}`, 6, collaborationGuard{parentID: id, childName: "two"})
			if scenario == "unchanged" {
				if err != nil || attempt.ID == "" {
					t.Fatalf("valid frozen evidence was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrResultReference) {
				t.Fatalf("tampered source was dispatched: %+v %v", attempt, err)
			}
			after, _ := loadCollaboration(t.Context(), core.Store.db, id)
			attempts, _ := core.Store.ListAttempts(t.Context(), run.ID)
			saved, _ := core.Store.GetRun(t.Context(), run.ID)
			if saved.Steps != 0 || len(attempts) != 0 || after.Usage.ModelCalls != before.Usage.ModelCalls ||
				after.Usage.OutputBytes != before.Usage.OutputBytes || len(after.Reservations) != len(before.Reservations) {
				t.Fatal("refused delivery consumed capacity or left a dispatch marker")
			}
		})
	}
}

func TestCollaborationFileVersionOrderMatrix(t *testing.T) {
	plan := CollaborationPlan{Children: []ChildSpec{
		{Name: "one"}, {Name: "two", DependsOn: []string{"one"}}, {Name: "three", DependsOn: []string{"two"}},
		{Name: "independent"},
	}}
	for _, scenario := range []struct {
		name     string
		events   []fileVersion
		conflict bool
	}{
		{"same_child", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "one", sequence: 2, before: "A", hash: "B", change: true}, {child: "one", sequence: 3, hash: "B"}}, false},
		{"transitive_dag", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "three", sequence: 2, before: "A", hash: "B", change: true}}, false},
		{"parallel_equal_reads", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "independent", sequence: 2, hash: "A"}}, false},
		{"parallel_different_reads", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "independent", sequence: 2, hash: "B"}}, true},
		{"parallel_matching_writer", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "independent", sequence: 2, before: "A", hash: "B", change: true}}, true},
		{"stale_write", []fileVersion{{child: "one", sequence: 1, before: "A", hash: "B", change: true}, {child: "two", sequence: 2, before: "A", hash: "C", change: true}}, true},
		{"external_mutation", []fileVersion{{child: "one", sequence: 1, hash: "A"}, {child: "one", sequence: 2, hash: "B"}}, true},
		{"same_attempt_partial_batch", []fileVersion{{child: "one", sequence: 1, before: "A", hash: "B", change: true}, {child: "one", sequence: 1, before: "B", hash: "C", change: true}}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := fileConflicts(plan, map[string][]fileVersion{"facts.txt": scenario.events})
			if (len(got) != 0) != scenario.conflict {
				t.Fatalf("conflicts=%+v, expected conflict=%t", got, scenario.conflict)
			}
		})
	}
}

func TestCollaborationTaskAwareRequirementsCannotUseModelClaims(t *testing.T) {
	collab, _, plan := collaborationFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: `{"summary":"Read and checked facts.txt successfully","findings":[]}`}, nil
	}))
	role := plan.Roles["reviewer"]
	role.Acceptance = "execution"
	role.RequiredEvidence = []EvidenceRequirement{{Kind: "read", Target: "facts.txt"}}
	plan.Roles["reviewer"] = role
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrMissingEvidence) || len(report.Gaps) != 2 || len(report.CombinedEvidence().ChecksPassed) != 0 {
		t.Fatalf("model assertion became fact: %+v %v", report, err)
	}
	zero := 0
	child := ChildReport{Spec: ChildSpec{Name: "execution"}, Role: RoleProfile{RequiredEvidence: []EvidenceRequirement{
		{Kind: "read", Target: "facts.txt"}, {Kind: "file_change", Target: "facts.txt"},
		{Kind: "command", Target: "tool"}, {Kind: "check", Target: "check"},
		{Kind: "http", Target: "https://example.invalid"},
	}}, Observed: []ObservedResult{{Tool: "file_read", Path: "facts.txt"}},
		Evidence: Evidence{Files: []ToolReceipt{{Path: "facts.txt", Status: "applied"}},
			Commands:     []ToolReceipt{{Command: "tool", Status: "completed", ExitCode: &zero}},
			ChecksNotRun: []string{"check"},
			HTTP:         []ToolReceipt{{URL: "https://example.invalid", Status: "completed", HTTPStatus: 200}}}}
	gaps := missingRequirements(child)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "check") {
		t.Fatalf("requirements=%v", gaps)
	}
}

func TestCollaborationSequentialFileChainAndStaleSnapshots(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "chain", true: "stale"}[stale], func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, nil)
			core.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				last := request.Messages[len(request.Messages)-1]
				if last.Role == "user" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_read", map[string]string{"path": "facts.txt"})}}, nil
				}
				if len(request.Messages) == 4 {
					var file struct {
						Hash string `json:"sha256"`
					}
					if json.Unmarshal([]byte(last.Content), &file) != nil {
						t.Fatal("invalid actual file result")
					}
					return ModelResponse{ToolCalls: []ToolCall{makeCall("edit", "file_edit", map[string]string{
						"path": "facts.txt", "expected_sha256": file.Hash, "old_text": "known facts", "new_text": "revised facts",
					})}}, nil
				}
				return ModelResponse{Content: `{"summary":"updated facts","findings":[]}`}, nil
			})
			core.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
			collab = NewCollaboration(NewTaskService(core))
			role := plan.Roles["reviewer"]
			role.Tools = []string{"file_read", "file_edit"}
			plan.Roles["reviewer"] = role
			plan.Children = plan.Children[:1]
			id := createCollaboration(t, collab, plan)
			report, err := collab.Continue(t.Context(), id)
			if err != nil || report.Parent.Status != "completed" || len(report.Conflicts) != 0 ||
				len(report.CombinedEvidence().Files) != 1 {
				t.Fatalf("legal chain=%+v %v", report, err)
			}
			hashA, hashB := contentHash([]byte("A")), contentHash([]byte("B"))
			events := map[string][]fileVersion{"facts.txt": {
				{child: "one", sequence: 1, hash: hashA},
				{child: "two", sequence: 2, before: hashA, hash: hashB, change: true},
				{child: "three", sequence: 3, hash: hashB},
			}}
			if stale {
				events["facts.txt"][2].hash = hashA
			}
			plan.Children = []ChildSpec{{Name: "one"}, {Name: "two", DependsOn: []string{"one"}}, {Name: "three", DependsOn: []string{"two"}}}
			if got := len(fileConflicts(plan, events)); (got != 0) != stale {
				t.Fatalf("chain stale=%t conflicts=%d", stale, got)
			}
			plan.Children[1].DependsOn = nil
			if len(fileConflicts(plan, events)) == 0 {
				t.Fatal("incomparable branches were treated as an established chain")
			}
		})
	}
}

func TestCollaborationResultsRestoreWithoutSourceReplay(t *testing.T) {
	var sourceCalls atomic.Int32
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if strings.Contains(request.Messages[1].Content, "CHILD_ONE") {
			sourceCalls.Add(1)
			return ModelResponse{Content: "public source"}, nil
		}
		if !strings.Contains(request.Messages[1].Content, "public source") {
			t.Fatal("restored grant missing")
		}
		return ModelResponse{Content: "used source"}, nil
	})
	collab, core, plan := collaborationFixture(t, model)
	core.Config.instructionText = ""
	plan = analysisPlan(plan)
	plan.Children[1].DependsOn = []string{"one"}
	plan.Children[1].Results = []ResultReference{{Source: "one", Scope: "summary", MaxBytes: 64}}
	id := createCollaboration(t, collab, plan)
	if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := collab.Continue(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	backup, restored := filepath.Join(base, "backup"), filepath.Join(base, "restored")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err := OpenTasks(t.Context(), restored, Adapters{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	other := NewCollaboration(tasks)
	if err := other.Control(t.Context(), id, "two", "resume"); err != nil {
		t.Fatal(err)
	}
	report, err := other.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || sourceCalls.Load() != 1 ||
		len(report.Children[1].Delivered) != 1 {
		t.Fatalf("restore=%+v %v calls=%d", report, err, sourceCalls.Load())
	}
	if _, err := os.Stat(filepath.Join(restored, "state.sqlite")); err != nil {
		t.Fatal(err)
	}
}
