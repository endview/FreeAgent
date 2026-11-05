package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// ModelRecovery links new dispatches, never mutating the original Attempt.
type ModelRecovery struct {
	Root        string `json:"root"`
	Previous    string `json:"previous"`
	Number      int    `json:"number"`
	Reason      string `json:"reason"`
	Fingerprint string `json:"fingerprint"`
}

func withRetryAfter(err *CallError, header http.Header) *CallError {
	if seconds, e := strconv.ParseInt(header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 {
		err.RetryAfterMS = min(seconds, 86401) * 1000
	} else if when, e := http.ParseTime(header.Get("Retry-After")); e == nil {
		err.RetryAfterMS = max(0, min(time.Until(when).Milliseconds(), 86401000))
	}
	return err
}

func (service *Service) canRetryModel(ctx context.Context, request ModelRequest, failure *CallError) bool {
	count := 0
	if request.Recovery != nil {
		count = request.Recovery.Number
	}
	if ctx.Err() != nil || count >= service.Config.ModelRetries {
		return false
	}
	binding, err := service.binding()
	if err != nil || binding.Config.EffectiveCapabilities().WebSearch == "supported" {
		return false
	}
	if failure == nil {
		return true
	}
	if failure.RetryAfterMS > 86400000 {
		return false
	}
	switch failure.Category {
	case "authentication", "permission", "quota", "request", "capability", "cancelled", "redirect", "refusal", "content_filter", "usage":
		return false
	case "output_limit", "rate_limit", "unavailable", "transport", "timeout":
		return true
	default:
		return !failure.Certain
	}
}

func (service *Service) retryModel(ctx context.Context, run Run, previous Attempt, request ModelRequest, response ModelResponse, failure *CallError, rebuild func(*ConversationSummary) (ModelRequest, error)) (Attempt, ModelResponse, error) {
	recovery := ModelRecovery{Root: previous.ID, Previous: previous.ID, Number: 1, Reason: "response_unknown", Fingerprint: contentHash([]byte(previous.RequestJSON))}
	if request.Recovery != nil {
		recovery.Root = request.Recovery.Root
		recovery.Number = request.Recovery.Number + 1
	}
	if failure != nil {
		recovery.Reason = failure.Category
	}
	// Reserve before waiting. A crash consumes the reservation and cannot reset
	// the budget; without a replacement dispatch UNKNOWN remains blocking.
	if err := service.Store.reserveModelRetry(ctx, run, recovery, service.Config.ModelRetries); err != nil {
		return previous, response, err
	}
	delay := time.Second*time.Duration(1+2*(recovery.Number-1)) + time.Duration(rand.IntN(201))*time.Millisecond
	if failure != nil {
		delay = max(delay, time.Duration(failure.RetryAfterMS)*time.Millisecond)
	}
	service.event(Event{Kind: "model_retry_wait", RunID: run.ID, ConversationID: run.ConversationID, AttemptID: previous.ID, Message: fmt.Sprintf("model retry %d/%d in %s; prior result retained; provider may charge each request", recovery.Number, service.Config.ModelRetries, delay)})
	wait := service.retryWait
	if wait == nil {
		wait = waitModelRetry
	}
	if err := wait(ctx, delay); err != nil {
		if previous.Status == "unknown" || failure == nil || !failure.Certain {
			return previous, response, errors.Join(err, ErrUnknownResult)
		}
		return previous, response, err
	}
	if err := service.checkCurrentPermission(ctx, run); err != nil {
		return previous, response, err
	}
	if err := service.checkDispatchConfig(run, true); err != nil {
		return previous, response, err
	}
	if request.Purpose != "verification" {
		if _, err := service.adoptSteeringForRetry(ctx, run, previous.ID); err != nil {
			return previous, response, err
		}
	}
	if request.Purpose == "" {
		current, err := service.buildContext(ctx, run)
		if err != nil {
			return previous, response, err
		}
		request = current
	}
	if recovery.Reason == "output_limit" {
		// Regenerate from the complete source. Never dispatch truncated tool calls
		// or concatenate a fragment and claim that it was one completed answer.
		request.Tools = nil
		request.DenyOperations = true
		request.Messages = append(request.Messages, ModelMessage{Role: "user", Content: "The previous model response reached its output limit and was not accepted. Give a concise complete answer from the same source within the current output budget. Do not call tools or claim unfinished actions completed. State any remaining work."})
	}
	request.Recovery = &recovery
	raw, err := json.Marshal(request)
	if err != nil {
		return previous, response, err
	}
	usage, err := service.requestUsage(run, request)
	if err == nil {
		err = usage.budgetError()
	}
	if err != nil {
		return previous, response, err
	}
	return service.dispatchModel(ctx, run, request, raw, rebuild, false)
}

func waitModelRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (store *Store) reserveModelRetry(ctx context.Context, run Run, r ModelRecovery, limit int) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old ModelRecovery
	var saved string
	err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "recovery/model/"+r.Root).Scan(&saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if saved != "" {
		if err := json.Unmarshal([]byte(saved), &old); err != nil {
			return ErrStoreFormat
		}
	}
	if r.Number != old.Number+1 || r.Number > limit {
		return errors.New("harness: model retry budget exhausted")
	}
	var kind, status, source string
	if err := tx.QueryRowContext(ctx, `SELECT kind,status,request_json FROM fa.attempts WHERE id=$1 AND run_id=$2`, r.Previous, run.ID).Scan(&kind, &status, &source); err != nil {
		return err
	}
	if kind != "model" || (status != "finished" && status != "unknown") || contentHash([]byte(source)) != r.Fingerprint {
		return ErrModelBoundary
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "recovery/model/"+r.Root, string(mustJSON(r))); err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE fa.runs SET status='running',stop_reason='',updated_at=$1 WHERE id=$2 AND status IN ('failed','unknown','running')`, nowText(), run.ID)
	if err != nil {
		return err
	}
	if n, e := updated.RowsAffected(); e != nil || n != 1 {
		return errors.Join(ErrActiveRun, e)
	}
	return tx.Commit()
}

func recordModelReplacement(ctx context.Context, tx *sql.Tx, attempt Attempt, r ModelRecovery) error {
	var saved string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "recovery/model/"+r.Root).Scan(&saved); err != nil {
		return err
	}
	var reserved ModelRecovery
	if json.Unmarshal([]byte(saved), &reserved) != nil || reserved != r {
		return ErrModelBoundary
	}
	value := string(mustJSON(map[string]any{"replacement": attempt.ID, "root": r.Root, "number": r.Number, "reason": r.Reason, "fingerprint": r.Fingerprint}))
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "attempt/replaced/"+r.Previous, value)
	return err
}

func (store *Store) attemptReplaced(ctx context.Context, attempt Attempt) (bool, error) {
	if attempt.Status != "unknown" {
		return false, nil
	}
	var exists bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.id=$1 AND a.run_id=$2 AND `+resolvedAttemptSQL+`)`, attempt.ID, attempt.RunID).Scan(&exists)
	return exists, err
}

func annotateReplacements(ctx context.Context, reader attemptReader, runID string, attempts []Attempt) error {
	rows, err := reader.QueryContext(ctx, `SELECT source.id,replacement.id FROM fa.attempts source JOIN fa.meta d ON d.key='attempt/replaced/'||source.id JOIN fa.attempts replacement ON replacement.id=(d.document #>> '{replacement}') AND replacement.run_id=source.run_id AND replacement.kind='model' WHERE source.run_id=$1 AND source.kind='model'`, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[string]string)
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		byID[a] = b
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range attempts {
		attempts[i].Replacement = byID[attempts[i].ID]
	}
	if err := rows.Close(); err != nil {
		return err
	}
	verified, err := reader.QueryContext(ctx, `SELECT a.id FROM fa.attempts a JOIN fa.meta d ON d.key='attempt/verified/'||a.id WHERE a.run_id=$1 AND a.kind='tool' AND (d.document #>> '{outcome}')='happened' AND (d.document #>> '{run_id}')=a.run_id`, runID)
	if err != nil {
		return err
	}
	defer verified.Close()
	for verified.Next() {
		var id string
		if err := verified.Scan(&id); err != nil {
			return err
		}
		for i := range attempts {
			if attempts[i].ID == id {
				attempts[i].Resolution = "verified_effect"
			}
		}
	}
	if err := verified.Err(); err != nil {
		return err
	}
	return nil
}

const resolvedAttemptSQL = `(EXISTS(SELECT 1 FROM fa.meta d JOIN fa.attempts replacement ON replacement.id=(d.document #>> '{replacement}') AND replacement.run_id=a.run_id AND replacement.kind='model' WHERE d.key='attempt/replaced/'||a.id AND a.kind='model') OR EXISTS(SELECT 1 FROM fa.meta v WHERE v.key='attempt/verified/'||a.id AND a.kind='tool' AND (v.document #>> '{outcome}')='happened' AND (v.document #>> '{run_id}')=a.run_id))`
