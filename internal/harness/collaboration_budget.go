package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type CollaborationUsage struct {
	Children       int   `json:"children"`
	ModelCalls     int   `json:"model_calls"`
	ToolCalls      int   `json:"tool_calls"`
	OutputBytes    int   `json:"output_bytes"`
	ActiveChildren int   `json:"active_children"`
	Replans        int   `json:"replans"`
	ActiveMS       int64 `json:"active_ms"`
}

type childRecord struct {
	Spec         ChildSpec         `json:"spec"`
	RunID        string            `json:"run_id"`
	Control      string            `json:"control"`
	OutputBytes  int               `json:"output_bytes"`
	Retired      bool              `json:"retired,omitempty"`
	Delivered    []DeliveredResult `json:"delivered_results,omitempty"`
	ResultsReady bool              `json:"results_ready,omitempty"`
}

type outputReservation struct {
	Child    string `json:"child"`
	Bytes    int    `json:"bytes"`
	Observed int    `json:"observed"`
	Final    bool   `json:"final"`
}

// This small overlay is stored in existing meta records. Runs and attempts
// remain the only execution records; no task/step/evidence schema is added.
type collaborationRecord struct {
	ParentID         string                       `json:"parent_id"`
	Plan             CollaborationPlan            `json:"plan"`
	ConfigJSON       string                       `json:"config_json"`
	Control          string                       `json:"control"`
	Usage            CollaborationUsage           `json:"usage"`
	Children         []childRecord                `json:"children"`
	Active           []string                     `json:"active,omitempty"`
	Reservations     map[string]outputReservation `json:"reservations"`
	AcceptedDigest   string                       `json:"accepted_conflict_digest,omitempty"`
	Decision         string                       `json:"decision,omitempty"`
	Settings         *CollaborationSettings       `json:"settings,omitempty"`
	Planning         bool                         `json:"planning,omitempty"`
	Single           bool                         `json:"single_agent,omitempty"`
	Reason           string                       `json:"reason,omitempty"`
	Revision         int                          `json:"revision"`
	InitialPlan      string                       `json:"initial_plan,omitempty"`
	ReplanAttempt    string                       `json:"replan_attempt,omitempty"`
	Replanned        []string                     `json:"replanned_results,omitempty"`
	SynthesisAttempt string                       `json:"synthesis_attempt,omitempty"`
	SynthesisDigest  string                       `json:"synthesis_digest,omitempty"`
}

type metaReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadCollaboration(ctx context.Context, reader metaReader, parentID string) (collaborationRecord, error) {
	var record collaborationRecord
	var raw string
	err := reader.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "collaboration/"+parentID).Scan(&raw)
	if err != nil {
		return record, err
	}
	if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &record) != nil || record.ParentID != parentID ||
		record.Reservations == nil || record.Revision < 1 {
		return record, errors.New("harness: invalid collaboration record")
	}
	return record, nil
}

func saveCollaboration(ctx context.Context, tx *sql.Tx, record collaborationRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(raw) > 2<<20 {
		return errors.New("harness: collaboration record exceeds limit")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"collaboration/"+record.ParentID, string(raw))
	return err
}

func (store *Store) controlledParent(ctx context.Context, runID string) (string, error) {
	var parent string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "collaboration-run/"+runID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return parent, err
}

func (store *Store) checkUncontrolled(ctx context.Context, conversationID string) error {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM runs r JOIN meta m ON m.key='collaboration-run/'||r.id
		WHERE r.conversation_id=? AND r.status NOT IN ('completed','failed','abandoned')`, conversationID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrControlledRun
	}
	return nil
}

type collaborationGuard struct {
	parentID  string
	childName string
}

func (guard collaborationGuard) Reserve(ctx context.Context, tx *sql.Tx, attempt Attempt) error {
	record, err := loadCollaboration(ctx, tx, guard.parentID)
	if err != nil {
		return err
	}
	if record.Control != "running" {
		return ErrChildPaused
	}
	parent, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, guard.parentID))
	if err != nil {
		return err
	}
	if activeElapsed(parent) >= int64(record.Plan.Budget.MaxActiveSeconds)*1000 {
		return ErrTimeLimit
	}
	var child *childRecord
	for index := range record.Children {
		if record.Children[index].Spec.Name == guard.childName {
			child = &record.Children[index]
			break
		}
	}
	if guard.childName == "" {
		if attempt.RunID != record.ParentID {
			return ErrControlledRun
		}
		if attempt.Kind == "model" {
			var request ModelRequest
			if json.Unmarshal([]byte(attempt.RequestJSON), &request) != nil {
				return ErrStoreFormat
			}
			if request.Purpose == "planning" {
				if !record.Planning {
					return ErrControlledRun
				}
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE run_id=? AND kind='model'
					AND json_extract(request_json,'$.Purpose')='planning'`, record.ParentID).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return errors.New("harness: planning has already been dispatched; use the saved result")
				}
			}
			if request.Purpose == "replanning" {
				if record.Planning || record.Single || record.Usage.Replans >= record.Plan.MaxReplans {
					return ErrParentBudget
				}
				if record.ReplanAttempt != "" {
					return errors.New("harness: use the saved replanning attempt before another dispatch")
				}
				var input struct {
					Revision int `json:"revision"`
				}
				if len(request.Messages) < 2 || json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil {
					return ErrStoreFormat
				}
				if input.Revision != record.Revision {
					return ErrPlanRevision
				}
				record.Usage.Replans++
				record.ReplanAttempt = attempt.ID
			}
			if request.Purpose == "synthesis" {
				if record.Planning || record.Single || !record.Plan.Synthesis || record.SynthesisAttempt != "" ||
					len(record.Active) != 0 {
					return ErrControlledRun
				}
				var input struct {
					Revision int    `json:"revision"`
					Digest   string `json:"digest"`
				}
				if len(request.Messages) < 2 || json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil ||
					input.Revision != record.Revision || len(input.Digest) != 64 {
					return ErrPlanRevision
				}
				for _, child := range record.Children {
					if child.Retired {
						continue
					}
					var status string
					if child.Control != "running" ||
						tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, child.RunID).Scan(&status) != nil ||
						status != "completed" {
						return ErrControlledRun
					}
				}
				record.SynthesisAttempt, record.SynthesisDigest = attempt.ID, input.Digest
			}
		}
		limit := ChildBudget{MaxOutputBytes: record.Plan.Budget.MaxOutputBytes, MaxCallOutputBytes: min(record.Plan.Budget.MaxOutputBytes, 16<<10)}
		if record.Settings != nil {
			limit.MaxCallOutputBytes = record.Settings.Child.MaxCallOutputBytes
		}
		return reserveCollaborationCall(ctx, tx, &record, nil, attempt, limit)
	}
	if child == nil || child.RunID != attempt.RunID || child.Control != "running" || child.Retired {
		return ErrChildPaused
	}
	if len(child.Spec.Results) != 0 && !child.ResultsReady {
		return ErrResultReference
	}
	if err := validateDeliveredResults(ctx, tx, record, *child); err != nil {
		return err
	}
	active := false
	for _, name := range record.Active {
		if name == guard.childName {
			active = true
		}
	}
	if !active || len(record.Active) > record.Plan.Budget.MaxConcurrentChildren {
		return ErrParentBudget
	}
	limit := record.Plan.Roles[child.Spec.Role].Budget
	return reserveCollaborationCall(ctx, tx, &record, child, attempt, limit)
}

func reserveCollaborationCall(ctx context.Context, tx *sql.Tx, record *collaborationRecord, child *childRecord, attempt Attempt, limit ChildBudget) error {
	budget := record.Plan.Budget
	reservation := limit.MaxCallOutputBytes
	if child != nil && reservation > limit.MaxOutputBytes-child.OutputBytes {
		return ErrChildBudget
	}
	if reservation > budget.MaxOutputBytes-record.Usage.OutputBytes {
		return ErrParentBudget
	}
	switch attempt.Kind {
	case "model":
		if record.Usage.ModelCalls >= budget.MaxModelCalls {
			return ErrParentBudget
		}
		record.Usage.ModelCalls++
	case "tool":
		if record.Usage.ToolCalls >= budget.MaxToolCalls {
			return ErrParentBudget
		}
		record.Usage.ToolCalls++
	default:
		return errors.New("harness: unsupported budgeted call kind")
	}
	name := ""
	if child != nil {
		child.OutputBytes += reservation
		name = child.Spec.Name
	}
	record.Usage.OutputBytes += reservation
	record.Reservations[attempt.ID] = outputReservation{Child: name, Bytes: reservation}
	return saveCollaboration(ctx, tx, *record)
}

func (guard collaborationGuard) Record(ctx context.Context, tx *sql.Tx, attempt Attempt, size int, final bool) error {
	record, err := loadCollaboration(ctx, tx, guard.parentID)
	if err != nil {
		return err
	}
	reservation, exists := record.Reservations[attempt.ID]
	if !exists && attempt.Kind == "tool" && attempt.Status == "pending" {
		// Rejected or invalid proposals are saved tool messages, not dispatch.
		return nil
	}
	if !exists || reservation.Child != guard.childName || reservation.Final {
		return errors.New("harness: missing or settled output reservation")
	}
	reservation.Observed = max(reservation.Observed, size)
	billed := max(reservation.Bytes, reservation.Observed)
	if final {
		billed = reservation.Observed
	}
	delta := billed - reservation.Bytes
	record.Usage.OutputBytes += delta
	for index := range record.Children {
		if record.Children[index].Spec.Name == guard.childName {
			record.Children[index].OutputBytes += delta
		}
	}
	reservation.Bytes, reservation.Final = billed, final
	record.Reservations[attempt.ID] = reservation
	if err := saveCollaboration(ctx, tx, record); err != nil {
		return err
	}
	if final && guard.childName == "" && record.Control == "cancelled" {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='abandoned',
			stop_reason='collaboration cancelled; saved results and UNKNOWN retained',updated_at=? WHERE id=?`, nowText(), record.ParentID); err != nil {
			return err
		}
	}
	limit := ChildBudget{MaxCallOutputBytes: min(record.Plan.Budget.MaxOutputBytes, 16<<10)}
	if guard.childName != "" {
		limit = record.Plan.Roles[recordChild(record, guard.childName).Spec.Role].Budget
	} else if record.Settings != nil {
		limit = record.Settings.Child
	}
	if reservation.Observed > limit.MaxCallOutputBytes ||
		guard.childName != "" && recordChild(record, guard.childName).OutputBytes > limit.MaxOutputBytes {
		return ErrChildBudget
	}
	if record.Usage.OutputBytes > record.Plan.Budget.MaxOutputBytes {
		return ErrParentBudget
	}
	return nil
}

func recordChild(record collaborationRecord, name string) childRecord {
	for _, child := range record.Children {
		if child.Spec.Name == name {
			return child
		}
	}
	return childRecord{}
}

func recoverCollaborationLeases(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM meta WHERE key LIKE 'collaboration/%'`)
	if err != nil {
		return err
	}
	var records []collaborationRecord
	for rows.Next() {
		var key, raw string
		var record collaborationRecord
		if err := rows.Scan(&key, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &record) != nil || key != "collaboration/"+record.ParentID || record.Reservations == nil {
			_ = rows.Close()
			return ErrStoreFormat
		}
		record.Active, record.Usage.ActiveChildren = nil, 0
		records = append(records, record)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, record := range records {
		if err := saveCollaboration(ctx, tx, record); err != nil {
			return err
		}
	}
	return nil
}

func activeElapsed(run Run) int64 {
	elapsed := run.ElapsedMS
	if run.ActiveSince != "" {
		if start, err := time.Parse(time.RFC3339Nano, run.ActiveSince); err == nil {
			elapsed += max(0, time.Since(start).Milliseconds())
		}
	}
	return elapsed
}
