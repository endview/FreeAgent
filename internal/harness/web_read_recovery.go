package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// A recorded builtin public read has no usable body after interruption.
// Only the new, explicit read intent is eligible; prior UNKNOWNs stay intact.
func recoverInterruptedWebReads(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,request_json,response_json FROM fa.attempts
		WHERE status='dispatched' AND kind='tool' AND request_doc->>'Name'='web_fetch'
		AND response_doc#>>'{Receipts,0,kind}'='http' AND response_doc#>>'{Receipts,0,status}'='reading' ORDER BY seq`)
	if err != nil {
		return err
	}
	type read struct {
		id, run, request, response string
	}
	var reads []read
	for rows.Next() {
		var item read
		if err = rows.Scan(&item.id, &item.run, &item.request, &item.response); err != nil {
			rows.Close()
			return err
		}
		reads = append(reads, item)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, item := range reads {
		var call ToolCall
		var result ToolResult
		if json.Unmarshal([]byte(item.request), &call) != nil || json.Unmarshal([]byte(item.response), &result) != nil || len(result.Receipts) != 1 {
			return ErrStoreFormat
		}
		run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1`, item.run))
		if err != nil {
			return err
		}
		result.Receipts[0].Status = "failed"
		result.IsError = true
		result.Content = "Public page not read: Owner interrupted before a usable body was saved. No automatic retry was made."
		if _, err = saveToolTx(ctx, tx, run, Attempt{ID: item.id}, call, result, nil, 0, true); err != nil {
			return err
		}
	}
	return nil
}
