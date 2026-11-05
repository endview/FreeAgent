package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *commandSupervisor) unreviewed(ctx context.Context) ([]CommandRecord, error) {
	if active := s.liveUncertain(); len(active) > 0 {
		return nil, fmt.Errorf("%w: owner command %s has uncertain cleanup or persistence; inspect before restarting the owner", ErrUnknownResult, active[0].ID)
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'command/process/%' AND (document #>> '{state}')='unknown' AND coalesce(((document #>> '{reviewed}')::boolean),false)=false LIMIT 33`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CommandRecord
	for rows.Next() {
		var raw string
		var r CommandRecord
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &r) != nil {
			return nil, ErrStoreFormat
		}
		r.Output = nil
		result = append(result, r)
	}
	if len(result) > 32 {
		return nil, ErrUnknownResult
	}
	return result, rows.Err()
}
func commandReviewHash(r CommandRecord) string {
	return contentHash(mustJSON(struct {
		ID, Generation, State string
		Revision              int64
	}{r.ID, r.Generation, r.State, r.Revision}))
}
func (s *commandSupervisor) review(ctx context.Context, records []CommandRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, old := range records {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, commandMetaPrefix+old.ID).Scan(&raw); err != nil {
			return err
		}
		var r CommandRecord
		if json.Unmarshal([]byte(raw), &r) != nil || commandReviewHash(r) != commandReviewHash(old) || r.Reviewed {
			return ErrApprovalChanged
		}
		r.Reviewed = true
		r.Revision++
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(r)), commandMetaPrefix+r.ID, raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}
