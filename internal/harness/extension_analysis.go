package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func analysisAuthorized(config Config, run Run, id string) bool {
	enabled, _ := config.Extensions.Settings[id]["analysis_enabled"].(bool)
	selected, _ := extensionRunSettings(run, id, config)["analysis_enabled"].(bool)
	return config.Extensions.Enabled[id] && enabled && selected
}
func analysisDeadline(settings map[string]any) time.Duration {
	ms := 8000
	if v, ok := settings["analysis_deadline_ms"].(float64); ok {
		ms = int(v)
	}
	return time.Duration(max(2000, min(30000, ms))) * time.Millisecond
}
func isSupplementalAttempt(a Attempt) bool {
	if a.Kind != "model" {
		return false
	}
	var r struct{ SupplementalKey string }
	return json.Unmarshal([]byte(a.RequestJSON), &r) == nil && r.SupplementalKey != ""
}

// Called under the existing Run transaction lock. Durable Attempts, rather than
// process-local counters, enforce the request bound after extension restarts.
func reserveSupplementalTx(ctx context.Context, tx *sql.Tx, run Run, r ModelRequest) error {
	if len(r.SupplementalKey) > 512 || !strings.HasPrefix(r.Purpose, "ext:") || len(r.Tools)+len(r.Memory)+len(r.Extensions) > 0 || r.MaxOutputTokens < 1 || r.MaxOutputTokens > 1024 || estimateContextTokens(string(mustJSON(r.Messages))) > 4096 {
		return errExtensionBudget
	}
	var count int
	var output int64
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(bool_or(request_doc->>'SupplementalKey'=$3),false),COALESCE(sum(CASE WHEN status='finished' AND (response_doc#>>'{Usage,output_tokens}')::bigint>0 THEN (response_doc#>>'{Usage,output_tokens}')::bigint ELSE (request_doc->>'MaxOutputTokens')::bigint END),0) FROM fa.attempts WHERE run_id=$1 AND kind='model' AND request_doc->>'Purpose'=$2`, run.ID, r.Purpose, r.SupplementalKey).Scan(&count, &duplicate, &output); err != nil {
		return err
	}
	limit := 2
	if strings.HasSuffix(r.Purpose, "/wake.evaluate") {
		limit = 1
	}
	if duplicate || count >= limit || output+int64(r.MaxOutputTokens) > int64(limit*1024) {
		return errExtensionBudget
	}
	return nil
}
