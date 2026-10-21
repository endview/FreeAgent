package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// One bounded edit can append, split or merge work by retiring old assignments
// and upserting new ones. Dispatched assignments are never rewritten.
type CollaborationEdit struct {
	ExpectedRevision int                    `json:"expected_revision"`
	Reason           string                 `json:"reason"`
	Roles            map[string]RoleProfile `json:"roles,omitempty"`
	Upsert           []ChildSpec            `json:"upsert,omitempty"`
	Retire           []string               `json:"retire,omitempty"`
}

type PlanRevision struct {
	Revision  int               `json:"revision"`
	Source    string            `json:"source"`
	Reason    string            `json:"reason"`
	AttemptID string            `json:"attempt_id,omitempty"`
	CreatedAt string            `json:"created_at"`
	Plan      CollaborationPlan `json:"plan"`
	Retired   []string          `json:"retired,omitempty"`
}

func savePlanRevision(ctx context.Context, tx *sql.Tx, record collaborationRecord, source, reason, attemptID string) error {
	if record.Revision < 1 || record.Revision > 64 {
		return errors.New("harness: finite plan revision limit reached")
	}
	revision := PlanRevision{Revision: record.Revision, Source: source, Reason: reason,
		AttemptID: attemptID, CreatedAt: nowText(), Plan: record.Plan}
	for _, child := range record.Children {
		if child.Retired {
			revision.Retired = append(revision.Retired, child.Spec.Name)
		}
	}
	raw, err := json.Marshal(revision)
	if err != nil || len(raw) > 256<<10 {
		return errors.New("harness: plan revision exceeds limit")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`,
		fmt.Sprintf("collaboration-revision/%s/%04d", record.ParentID, record.Revision), string(raw))
	return err
}

func (collab *Collaboration) Revisions(ctx context.Context, parentID string) ([]PlanRevision, error) {
	core, err := collab.core()
	if err != nil {
		return nil, err
	}
	if _, err := loadCollaboration(ctx, core.Store.db, parentID); err != nil {
		return nil, err
	}
	rows, err := core.Store.db.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE ? ORDER BY key`,
		"collaboration-revision/"+parentID+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revisions []PlanRevision
	for rows.Next() {
		var raw string
		var revision PlanRevision
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 256<<10 || strictDecode([]byte(raw), &revision) != nil {
			return nil, ErrStoreFormat
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (collab *Collaboration) Revise(ctx context.Context, parentID string, edit CollaborationEdit) error {
	core, err := collab.core()
	if err != nil {
		return err
	}
	parent, err := core.Store.GetRun(ctx, parentID)
	if err != nil {
		return err
	}
	core, err = core.forRun(parent)
	if err != nil {
		return err
	}
	if err := core.checkConfig(parent); err != nil {
		return err
	}
	raw, err := json.Marshal(edit)
	if err != nil || len(raw) > 64<<10 || strictDecode(raw, &edit) != nil {
		return errors.New("harness: invalid bounded plan edit")
	}
	return collab.change(ctx, parentID, func(record *collaborationRecord, tx *sql.Tx) error {
		return applyPlanEdit(ctx, tx, core, record, edit, "user", "")
	})
}

func applyPlanEdit(ctx context.Context, tx *sql.Tx, core *Service, record *collaborationRecord, edit CollaborationEdit, source, attemptID string) error {
	if edit.ExpectedRevision != record.Revision {
		return ErrPlanRevision
	}
	parent, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, record.ParentID))
	if err != nil {
		return err
	}
	if record.Planning || record.Single || record.Control == "cancelled" ||
		parent.Status == "completed" || parent.Status == "abandoned" || parent.Status == "failed" {
		return ErrControlledRun
	}
	if parent.Status == "unknown" {
		return ErrUnknownResult
	}
	if record.SynthesisAttempt != "" {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM attempts WHERE id=?`, record.SynthesisAttempt).Scan(&status); err != nil {
			return err
		}
		if status == "dispatched" || status == "unknown" {
			return ErrUnknownResult
		}
	}
	if source == "user" && record.ReplanAttempt != "" {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM attempts WHERE id=?`, record.ReplanAttempt).Scan(&status); err != nil {
			return err
		}
		if status == "unknown" || status == "dispatched" {
			return ErrUnknownResult
		}
		// Explicit user edits supersede an unusable saved proposal, not its
		// attempt or charged capacity.
		record.ReplanAttempt = ""
	}
	if strings.TrimSpace(edit.Reason) == "" || len(edit.Reason) > 4096 ||
		len(edit.Upsert)+len(edit.Retire) == 0 || len(edit.Upsert) > 16 || len(edit.Retire) > 16 {
		return errors.New("harness: a bounded explained work revision is required")
	}
	roles := maps.Clone(record.Plan.Roles)
	for name, role := range edit.Roles {
		for _, child := range record.Children {
			if child.Spec.Role != name || reflect.DeepEqual(roles[name], role) {
				continue
			}
			if !slices.ContainsFunc(edit.Upsert, func(spec ChildSpec) bool { return spec.Name == child.Spec.Name }) {
				return errors.New("harness: role changes must explicitly update each affected assignment")
			}
		}
		roles[name] = role
	}
	plan := record.Plan
	plan.Roles, plan.Children = roles, slices.Clone(record.Plan.Children)
	seen := map[string]bool{}
	for _, spec := range edit.Upsert {
		if seen[spec.Name] || slices.Contains(edit.Retire, spec.Name) {
			return errors.New("harness: duplicate or both revised and retired assignment")
		}
		seen[spec.Name] = true
		index := slices.IndexFunc(plan.Children, func(child ChildSpec) bool { return child.Name == spec.Name })
		if index < 0 {
			plan.Children = append(plan.Children, spec)
		} else {
			plan.Children[index] = spec
		}
	}
	newChildren := len(plan.Children) - len(record.Plan.Children)
	if record.Usage.Children+newChildren > record.Plan.Budget.MaxChildren {
		return ErrParentBudget
	}
	if err := plan.validate(core); err != nil {
		return err
	}
	retired := map[string]bool{}
	for _, child := range record.Children {
		retired[child.Spec.Name] = child.Retired
	}
	for _, name := range edit.Retire {
		if seen[name] {
			return errors.New("harness: duplicate retired assignment")
		}
		seen[name] = true
		if _, exists := retired[name]; !exists || retired[name] {
			return errors.New("harness: retired assignment is missing or already retired")
		}
		retired[name] = true
	}
	for _, spec := range plan.Children {
		if retired[spec.Name] {
			continue
		}
		for _, dependency := range spec.DependsOn {
			if retired[dependency] {
				return errors.New("harness: live work cannot depend on a retired assignment")
			}
		}
	}
	for index := range record.Children {
		child := &record.Children[index]
		if !seen[child.Spec.Name] {
			continue
		}
		if child.Retired || slices.Contains(record.Active, child.Spec.Name) {
			return ErrActiveRun
		}
		run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, child.RunID))
		if err != nil {
			return err
		}
		var uncertain int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE run_id=? AND status IN ('unknown','dispatched')`, run.ID).Scan(&uncertain); err != nil {
			return err
		}
		if uncertain != 0 || run.Status == "unknown" {
			return ErrUnknownResult
		}
		if slices.Contains(edit.Retire, child.Spec.Name) {
			if run.Steps != 0 && run.Status != "failed" {
				return ErrControlledRun
			}
			// A failed result is retained verbatim; only undispatched work is
			// changed to abandoned when its assignment is explicitly retired.
			child.Retired = true
			if run.Steps == 0 {
				child.Control = "cancelled"
				if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='abandoned',stop_reason='undispatched assignment retired',updated_at=? WHERE id=?`, nowText(), run.ID); err != nil {
					return err
				}
			}
			continue
		}
		if run.Steps != 0 || run.Status == "abandoned" || child.Control == "cancelled" {
			return ErrControlledRun
		}
		spec := plan.Children[slices.IndexFunc(plan.Children, func(spec ChildSpec) bool { return spec.Name == child.Spec.Name })]
		saved, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil {
			return err
		}
		agent := ""
		if saved.Memory != nil {
			agent = saved.Memory.AgentID
		}
		snapshot, prompt, err := assignmentSnapshot(core, spec, plan.Roles[spec.Role], parent.ID, agent)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET prompt=?,config_json=?,updated_at=? WHERE id=?`, prompt, snapshot, nowText(), child.RunID); err != nil {
			return err
		}
		body, _ := json.Marshal(ModelMessage{Role: "user", Content: prompt})
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET body_json=? WHERE run_id=? AND json_extract(body_json,'$.Role')='user'`, string(body), child.RunID); err != nil {
			return err
		}
		child.Spec = spec
		child.Delivered, child.ResultsReady = nil, false
	}
	for _, spec := range plan.Children[len(record.Plan.Children):] {
		snapshot, prompt, err := assignmentSnapshot(core, spec, plan.Roles[spec.Role], parent.ID, "")
		if err != nil {
			return err
		}
		run, err := insertCollaborationRun(ctx, tx, core.Config.Workspace, "Child: "+spec.Name,
			parent.RequestKey+"/"+spec.Name, prompt, snapshot)
		if err != nil {
			return err
		}
		record.Children = append(record.Children, childRecord{Spec: spec, RunID: run.ID, Control: "running"})
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES(?,?)`, "collaboration-run/"+run.ID, parent.ID); err != nil {
			return err
		}
	}
	record.Plan = plan
	record.Usage.Children += newChildren
	record.Revision++
	record.AcceptedDigest, record.Decision = "", ""
	// A new work revision may use new synthesis, but historical attempts and
	// charged reservations remain. Unknown synthesis was refused above.
	record.SynthesisAttempt, record.SynthesisDigest = "", ""
	return savePlanRevision(ctx, tx, *record, source, edit.Reason, attemptID)
}

func assignmentSnapshot(core *Service, spec ChildSpec, role RoleProfile, parentID, agentID string) (string, string, error) {
	config, err := childConfig(core.Config, role)
	if err != nil {
		return "", "", err
	}
	tools, err := newAssignedTools(core.Tools, role)
	if err != nil {
		return "", "", err
	}
	service := NewService(core.Store, config, Adapters{Model: core.Model, Tools: tools, Keys: core.Keys})
	service.controlToolsDisabled = true
	if agentID == "" {
		agentID = collaborationAgentID(parentID, spec.Name)
	}
	service.permission = core.permission
	service.memory, err = role.Memory.policy(parentID, config.Workspace, agentID)
	if err != nil {
		return "", "", err
	}
	snapshot, err := service.snapshot()
	return snapshot, childPrompt(spec, role), err
}

func collaborationAgentID(parentID, childName string) string {
	return "expert-" + parentID + "-" + childName
}

// IncreaseLimits is a user operation. No model response can invoke it; the
// saved allowances, active time, attempts and uncertain reservations remain.
func (collab *Collaboration) IncreaseLimits(ctx context.Context, parentID string, budget ParentBudget) error {
	if err := budget.validate(); err != nil {
		return err
	}
	core, err := collab.core()
	if err != nil {
		return err
	}
	if !core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	return collab.change(ctx, parentID, func(record *collaborationRecord, tx *sql.Tx) error {
		parent, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, parentID))
		if err != nil {
			return err
		}
		if parent.Status == "completed" || parent.Status == "failed" || parent.Status == "abandoned" || record.Control == "cancelled" {
			return ErrControlledRun
		}
		old := record.Plan.Budget
		if budget.MaxChildren < old.MaxChildren || budget.MaxModelCalls < old.MaxModelCalls ||
			budget.MaxToolCalls < old.MaxToolCalls || budget.MaxOutputBytes < old.MaxOutputBytes ||
			budget.MaxConcurrentChildren < old.MaxConcurrentChildren || budget.MaxActiveSeconds < old.MaxActiveSeconds {
			return errors.New("harness: parent limits can only increase without resetting consumption")
		}
		if budget == old {
			return nil
		}
		record.Plan.Budget = budget
		if record.Settings != nil {
			record.Settings.Budget = budget
		}
		record.Revision++
		return savePlanRevision(ctx, tx, *record, "limits", "explicit finite parent limit increase; consumption retained", "")
	})
}
