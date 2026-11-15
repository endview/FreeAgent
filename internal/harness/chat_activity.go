package harness

import (
	"context"
	"database/sql"
	"errors"
	"slices"
)

// Metadata comes from the saved binding, never today's editable model profile.
func recordedModelName(raw, id string) string {
	snapshot, err := decodeRunSnapshot(raw)
	if err != nil {
		return id
	}
	return bindingModelName(snapshot, id)
}

func bindingModelName(snapshot runSnapshot, id string) string {
	for _, binding := range snapshot.Bindings {
		if binding.ID == id && binding.Config.Model != "" {
			return binding.Config.Model
		}
	}
	return id
}

func activityNeedsAttention(a ChatActivity, runStatus string) bool {
	return a.Replacement == "" && (a.Resolution == "" || a.Resolution == "handed_off") && (a.Status == "unknown" || a.Status == "failed" || a.IsError || runStatus == "waiting_confirmation" && a.Status == "pending")
}

// Long runs retain their newest unresolved entries even when they have fallen
// out of the live tail. This is bounded read-side metadata, not a second queue.
func chatAttention(ctx context.Context, tx *sql.Tx, run Run) ([]ChatActivity, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.run_id,a.kind,a.call_id,a.status,a.request_json,a.response_json,a.seq,count(*) OVER() FROM fa.attempts a WHERE a.run_id=$1 AND (NOT `+resolvedAttemptSQL+` OR EXISTS(SELECT 1 FROM fa.meta u WHERE u.key='attempt/unresolved/'||a.id AND u.document->>'handoff'='true' AND coalesce(u.document->>'resolution','')='')) AND (a.status IN ('unknown','failed') OR a.status='pending' AND $2='waiting_confirmation' OR a.response_doc->>'IsError'='true' OR a.response_doc#>>'{Outcome,status}' IN ('unknown','failed') OR coalesce(a.response_doc->'Failure','null'::jsonb)!='null'::jsonb) ORDER BY a.seq DESC LIMIT 3`, run.ID, run.Status)
	if err != nil {
		return nil, 0, err
	}
	var attempts []Attempt
	total := 0
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.RunID, &a.Kind, &a.CallID, &a.Status, &a.RequestJSON, &a.ResponseJSON, &a.Sequence, &total); err != nil {
			rows.Close()
			return nil, 0, err
		}
		attempts = append(attempts, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, 0, err
	}
	var items []ChatActivity
	if err := annotateReplacements(ctx, tx, run.ID, attempts); err != nil {
		return nil, 0, err
	}
	snapshot, _ := decodeRunSnapshot(run.ConfigJSON)
	for _, a := range attempts {
		item, _, err := projectChatActivity(ctx, tx, a)
		if err != nil {
			return nil, 0, err
		}
		if a.Kind == "model" && item.ModelName == "" {
			item.ModelName = bindingModelName(snapshot, item.Name)
		}
		items = append(items, item)
	}
	return items, total, nil
}

type ChatActivityPage struct {
	RunID      string         `json:"run_id"`
	Items      []ChatActivity `json:"items"`
	Before     string         `json:"before,omitempty"`
	HasEarlier bool           `json:"has_earlier"`
	Total      int            `json:"total"`
}

// ChatActivities pages one Run's saved chain without expanding every live snapshot.
// Both the Run and opaque cursor must belong to the requested conversation.
func (tasks *TaskService) ChatActivities(ctx context.Context, conversationID, runID, before string) (ChatActivityPage, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return ChatActivityPage{}, errors.New("harness: task service and context are required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return ChatActivityPage{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return ChatActivityPage{}, errors.New("harness: conversation workspace does not match")
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ChatActivityPage{}, err
	}
	defer tx.Rollback()
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1 AND conversation_id=$2`, runID, conversationID))
	if err != nil {
		return ChatActivityPage{}, err
	}
	args := []any{runID}
	condition := ""
	if before != "" {
		var sequence int64
		if err := tx.QueryRowContext(ctx, `SELECT seq FROM fa.attempts WHERE id=$1 AND run_id=$2`, before, runID).Scan(&sequence); err != nil {
			return ChatActivityPage{}, err
		}
		condition = " AND seq<$2"
		args = append(args, sequence)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,kind,call_id,status,request_json,response_json,seq FROM fa.attempts WHERE run_id=$1`+condition+` ORDER BY seq DESC LIMIT 129`, args...)
	if err != nil {
		return ChatActivityPage{}, err
	}
	var attempts []Attempt
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			rows.Close()
			return ChatActivityPage{}, err
		}
		attempts = append(attempts, attempt)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ChatActivityPage{}, err
	}
	page := ChatActivityPage{RunID: runID, Items: make([]ChatActivity, 0), HasEarlier: len(attempts) > 128}
	if page.HasEarlier {
		attempts = attempts[:128]
	}
	slices.Reverse(attempts)
	if err := annotateReplacements(ctx, tx, runID, attempts); err != nil {
		return ChatActivityPage{}, err
	}
	snapshot, _ := decodeRunSnapshot(run.ConfigJSON)
	for _, attempt := range attempts {
		activity, _, err := projectChatActivity(ctx, tx, attempt)
		if err != nil {
			return ChatActivityPage{}, err
		}
		if attempt.Kind == "model" && activity.ModelName == "" {
			activity.ModelName = bindingModelName(snapshot, attempt.ModelID)
		}
		page.Items = append(page.Items, activity)
	}
	if len(attempts) > 0 {
		page.Before = attempts[0].ID
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts WHERE run_id=$1`, runID).Scan(&page.Total); err != nil {
		return ChatActivityPage{}, err
	}
	return page, tx.Commit()
}
