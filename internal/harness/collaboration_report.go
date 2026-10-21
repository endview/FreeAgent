package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ChildReport struct {
	Spec        ChildSpec         `json:"spec"`
	Role        RoleProfile       `json:"role"`
	Control     string            `json:"control"`
	Run         Run               `json:"run"`
	Evidence    Evidence          `json:"evidence"`
	Observed    []ObservedResult  `json:"observed_results,omitempty"`
	OutputBytes int               `json:"output_bytes"`
	Retired     bool              `json:"retired,omitempty"`
	Delivered   []DeliveredResult `json:"delivered_results,omitempty"`
	Result      ResultPacket      `json:"result"`
}

type ObservedResult struct {
	AttemptID   string `json:"attempt_id"`
	Tool        string `json:"tool"`
	ContentHash string `json:"content_sha256"`
	Path        string `json:"path,omitempty"`
	FileHash    string `json:"file_sha256,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	NextOffset  int    `json:"next_offset,omitempty"`
}

type EvidenceConflict struct {
	Path     string   `json:"path"`
	Children []string `json:"children"`
	Hashes   []string `json:"sha256"`
}

type CollaborationReport struct {
	Parent    Run                `json:"parent"`
	Budget    ParentBudget       `json:"budget"`
	Usage     CollaborationUsage `json:"usage"`
	Remaining CollaborationUsage `json:"remaining"`
	Revision  int                `json:"revision"`
	Control   string             `json:"control"`
	Children  []ChildReport      `json:"children"`
	Conflicts []EvidenceConflict `json:"conflicts,omitempty"`
	Gaps      []string           `json:"evidence_gaps,omitempty"`
	Digest    string             `json:"digest"`
	Decision  string             `json:"user_conflict_decision,omitempty"`
	Evidence  Evidence           `json:"parent_evidence"`
	Synthesis SynthesisResult    `json:"synthesis"`
}

func (collab *Collaboration) Inspect(ctx context.Context, parentID string) (CollaborationReport, error) {
	var report CollaborationReport
	core, err := collab.core()
	if err != nil {
		return report, err
	}
	record, err := loadCollaboration(ctx, core.Store.db, parentID)
	if err != nil {
		return report, err
	}
	report.Parent, err = core.Store.GetRun(ctx, parentID)
	if err != nil {
		return report, err
	}
	// Parent output is itself a saved report. Do not recursively embed it.
	report.Parent.Output = ""
	report.Evidence, err = core.Store.Evidence(ctx, parentID)
	if err != nil {
		return report, err
	}
	report.Budget, report.Usage, report.Control, report.Decision = record.Plan.Budget, record.Usage, record.Control, record.Decision
	report.Revision = record.Revision
	report.Usage.ActiveMS = activeElapsed(report.Parent)
	report.Remaining = CollaborationUsage{
		Children:       max(0, record.Plan.Budget.MaxChildren-record.Usage.Children),
		ModelCalls:     max(0, record.Plan.Budget.MaxModelCalls-record.Usage.ModelCalls),
		ToolCalls:      max(0, record.Plan.Budget.MaxToolCalls-record.Usage.ToolCalls),
		OutputBytes:    max(0, record.Plan.Budget.MaxOutputBytes-record.Usage.OutputBytes),
		ActiveChildren: max(0, record.Plan.Budget.MaxConcurrentChildren-record.Usage.ActiveChildren),
		ActiveMS:       max(0, int64(record.Plan.Budget.MaxActiveSeconds)*1000-report.Usage.ActiveMS),
		Replans:        max(0, record.Plan.MaxReplans-record.Usage.Replans)}
	versions := map[string][]fileVersion{}
	for _, child := range record.Children {
		run, attempts, evidence, err := collab.tasks.Inspect(ctx, child.RunID)
		if err != nil {
			return report, err
		}
		item := ChildReport{Spec: child.Spec, Role: record.Plan.Roles[child.Spec.Role], Control: child.Control, Run: run, Evidence: evidence, OutputBytes: child.OutputBytes, Retired: child.Retired, Delivered: child.Delivered}
		observed, childVersions := childRuntimeResults(child.Spec.Name, attempts, record.Reservations)
		item.Observed = observed
		for path, events := range childVersions {
			versions[path] = append(versions[path], events...)
		}
		if run.Status == "completed" && !child.Retired {
			report.Gaps = append(report.Gaps, childEvidenceGaps(item)...)
		}
		item.Result = resultPacket(item)
		report.Children = append(report.Children, item)
	}
	report.Conflicts = fileConflicts(record.Plan, versions)
	// Bind approval to actual child facts and outputs, not merely a path name.
	raw, err := json.Marshal(struct {
		Children  []ChildReport
		Conflicts []EvidenceConflict
		Gaps      []string
	}{report.Children, report.Conflicts, report.Gaps})
	if err != nil {
		return report, err
	}
	report.Digest = contentHash(raw)
	report.Synthesis, err = inspectSynthesis(ctx, core.Store, record, report.Digest)
	return report, err
}

func childRuntimeResults(name string, attempts []Attempt, reservations map[string]outputReservation) ([]ObservedResult, map[string][]fileVersion) {
	var observed []ObservedResult
	versions := map[string][]fileVersion{}
	for _, attempt := range attempts {
		_, dispatched := reservations[attempt.ID]
		if attempt.Kind != "tool" || attempt.Status != "finished" || !dispatched {
			continue
		}
		var result ToolResult
		var call ToolCall
		if json.Unmarshal([]byte(attempt.ResponseJSON), &result) != nil ||
			json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
			continue
		}
		// Partial batches retain applied receipts even when another item failed.
		for _, receipt := range result.Receipts {
			if receipt.Kind == "file" && receipt.AfterHash != "" && receipt.Status == "applied" {
				versions[receipt.Path] = append(versions[receipt.Path], fileVersion{
					child: name, sequence: attempt.Sequence, before: receipt.BeforeHash, hash: receipt.AfterHash, change: true})
			}
		}
		if result.IsError {
			continue
		}
		observation := ObservedResult{AttemptID: attempt.ID, Tool: call.Name, ContentHash: contentHash([]byte(result.Content))}
		if call.Name == "file_read" {
			var file struct {
				Path       string `json:"path"`
				Hash       string `json:"sha256"`
				Offset     int    `json:"offset"`
				NextOffset int    `json:"next_offset"`
			}
			if json.Unmarshal([]byte(result.Content), &file) == nil && file.Path != "" && len(file.Hash) == 64 {
				observation.Path, observation.FileHash, observation.Offset, observation.NextOffset = file.Path, file.Hash, file.Offset, file.NextOffset
				versions[file.Path] = append(versions[file.Path], fileVersion{child: name, sequence: attempt.Sequence, hash: file.Hash})
			}
		}
		observed = append(observed, observation)
	}
	return observed, versions
}

func childEvidenceGaps(child ChildReport) []string {
	var gaps []string
	if child.Role.Acceptance != "analysis" && len(child.Role.RequiredEvidence) == 0 && len(child.Observed) == 0 {
		gaps = append(gaps, child.Spec.Name+": no successful dispatched tool result; model text is not execution evidence")
	}
	gaps = append(gaps, missingRequirements(child)...)
	if len(child.Evidence.Uncertainty) != 0 {
		gaps = append(gaps, child.Spec.Name+": unresolved or truncated runtime evidence")
	}
	if child.Role.OutputFormat == "json" {
		var output struct {
			Summary  string   `json:"summary"`
			Findings []string `json:"findings"`
		}
		if json.Unmarshal([]byte(child.Run.Output), &output) != nil || output.Summary == "" || output.Findings == nil {
			gaps = append(gaps, child.Spec.Name+": JSON output contract not satisfied")
		}
	}
	return gaps
}

// Aggregate runtime projections only. Model assertions never enter these facts.
func (report CollaborationReport) CombinedEvidence() Evidence {
	evidence := Evidence{}
	appendEvidence := func(source Evidence, prefix string) {
		evidence.Files = append(evidence.Files, source.Files...)
		evidence.Commands = append(evidence.Commands, source.Commands...)
		evidence.HTTP = append(evidence.HTTP, source.HTTP...)
		evidence.ChecksPassed = append(evidence.ChecksPassed, source.ChecksPassed...)
		evidence.ChecksNotRun = append(evidence.ChecksNotRun, source.ChecksNotRun...)
		for _, uncertainty := range source.Uncertainty {
			evidence.Uncertainty = append(evidence.Uncertainty, prefix+uncertainty)
		}
	}
	appendEvidence(report.Evidence, "")
	for _, child := range report.Children {
		appendEvidence(child.Evidence, child.Spec.Name+": ")
	}
	evidence.Uncertainty = append(evidence.Uncertainty, report.Gaps...)
	for _, conflict := range report.Conflicts {
		evidence.Uncertainty = append(evidence.Uncertainty, conflict.Path+": differing runtime source versions retained")
	}
	return evidence
}

// The ordinary answer omits internal IDs and counters. Inspect retains the
// deterministic facts independently of what the experts say.
func (report CollaborationReport) Answer() string {
	var answer strings.Builder
	if report.Synthesis.Status == "finished" {
		answer.WriteString("Model synthesis (interpretation, not execution evidence)\n" + report.Synthesis.Text + "\n\n")
	} else if report.Synthesis.Status != "" && report.Synthesis.Status != "disabled" && report.Synthesis.Status != "not_started" {
		answer.WriteString("Model synthesis: " + report.Synthesis.Status + "; saved expert results retained.\n\n")
	}
	answer.WriteString("Expert results")
	for _, child := range report.Children {
		answer.WriteString("\n\n")
		output := child.Run.Output
		if child.Retired {
			fmt.Fprintf(&answer, "%s\nAssignment retired; saved outcome: %s.", child.Spec.Objective, child.Run.Status)
			continue
		}
		if child.Role.OutputFormat == "json" {
			var content struct {
				Summary  string   `json:"summary"`
				Findings []string `json:"findings"`
			}
			if json.Unmarshal([]byte(output), &content) == nil {
				output = content.Summary
				for _, finding := range content.Findings {
					output += "\n- " + finding
				}
			}
		}
		fmt.Fprintf(&answer, "%s\n%s", child.Spec.Objective, output)
		if child.Role.Acceptance == "analysis" {
			answer.WriteString("\nAnalysis conclusion; not an independently verified execution result.")
		}
		if len(child.Result.References) != 0 {
			answer.WriteString("\nReported external sources (not independent verification): " + strings.Join(child.Result.References, "; "))
		}
		if len(child.Result.Artifacts) != 0 {
			answer.WriteString("\nReported artifacts: " + strings.Join(child.Result.Artifacts, "; "))
		}
		if len(child.Result.Questions) != 0 {
			answer.WriteString("\nOpen questions: " + strings.Join(child.Result.Questions, "; "))
		}
	}
	if len(report.Conflicts) != 0 {
		answer.WriteString("\n\nDiffering source versions were retained.")
		if report.Decision != "" {
			answer.WriteString("\nUser decision: " + report.Decision)
		}
	}
	evidence := report.CombinedEvidence()
	answer.WriteString("\n\nRuntime facts")
	if len(evidence.Files)+len(evidence.Commands)+len(evidence.HTTP)+len(evidence.ChecksPassed)+len(evidence.ChecksNotRun) == 0 {
		answer.WriteString("\nNo file changes, commands, network results or passed checks recorded.")
	}
	for _, file := range evidence.Files {
		fmt.Fprintf(&answer, "\nFile %s: %s", file.Path, file.Status)
	}
	for _, command := range evidence.Commands {
		fmt.Fprintf(&answer, "\nCommand %s: %s", command.Command, command.Status)
		if command.ExitCode != nil {
			fmt.Fprintf(&answer, " (exit %d)", *command.ExitCode)
		}
	}
	for _, receipt := range evidence.HTTP {
		fmt.Fprintf(&answer, "\nHTTP %s: %s (%d)", receipt.URL, receipt.Status, receipt.HTTPStatus)
	}
	for _, check := range evidence.ChecksPassed {
		answer.WriteString("\nCheck passed: " + check)
	}
	for _, check := range evidence.ChecksNotRun {
		answer.WriteString("\nCheck not run: " + check)
	}
	if len(evidence.Uncertainty) != 0 {
		answer.WriteString("\n\nUnresolved evidence and uncertainty\n" + strings.Join(evidence.Uncertainty, "\n"))
	}
	return answer.String()
}

func (collab *Collaboration) Check(ctx context.Context, parentID string) error {
	report, err := collab.Inspect(ctx, parentID)
	if err != nil {
		return err
	}
	if report.Parent.Status != "completed" {
		return fmt.Errorf("harness: collaboration is %s, not completed", report.Parent.Status)
	}
	if len(report.Gaps) != 0 {
		return ErrMissingEvidence
	}
	if len(report.Conflicts) != 0 && report.Decision == "" {
		return ErrMergeDecision
	}
	for _, child := range report.Children {
		if child.Run.Status != "completed" && !child.Retired {
			return errors.New("harness: incomplete child")
		}
	}
	return nil
}
