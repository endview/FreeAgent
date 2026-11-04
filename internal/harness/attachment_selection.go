package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type exportSelection struct {
	Source      string
	KeepVersion bool
}

func (store *Store) identicalExport(ctx context.Context, chat, run, source, hash string, keep bool) (AttachmentRef, error) {
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key LIKE 'attachment/%' AND json_extract(value,'$.conversation')=? AND json_extract(value,'$.request')=? AND json_extract(value,'$.export_source')=? AND json_extract(value,'$.ref.sha256')=? AND coalesce(json_extract(value,'$.deleted'),0)=0 LIMIT 1`, chat, run, source, hash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentRef{}, nil
	}
	if err != nil {
		return AttachmentRef{}, err
	}
	var r attachmentRecord
	if json.Unmarshal([]byte(raw), &r) != nil {
		return AttachmentRef{}, ErrAttachment
	}
	if _, err := readAttachmentBlob(store.dataDir, r.Ref); err != nil {
		return AttachmentRef{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return AttachmentRef{}, err
	}
	defer tx.Rollback()
	r.ExportKeepVersion = r.ExportKeepVersion || keep
	if err := selectExportTx(ctx, tx, &r); err != nil {
		return AttachmentRef{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=? WHERE key=?`, string(mustJSON(r)), attachmentPrefix+r.Ref.ID); err != nil {
		return AttachmentRef{}, err
	}
	return r.Ref, tx.Commit()
}

func exportDispatchStarted(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key LIKE 'channel/file-send/%' AND json_extract(value,'$.attachment.id')=? AND json_extract(value,'$.state') IN ('dispatching','sent','unknown')`, id).Scan(&count)
	return count > 0, err
}

func selectExportTx(ctx context.Context, tx *sql.Tx, record *attachmentRecord) error {
	rows, err := tx.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'attachment/%' AND json_extract(value,'$.request')=? AND json_extract(value,'$.export_source')=? AND json_extract(value,'$.export_selected')=1 AND coalesce(json_extract(value,'$.deleted'),0)=0`, record.Request, record.ExportSource)
	if err != nil {
		return err
	}
	var previous []attachmentRecord
	for rows.Next() {
		var raw string
		var r attachmentRecord
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(raw), &r) != nil {
			rows.Close()
			return ErrAttachment
		}
		previous = append(previous, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, r := range previous {
		if r.Ref.ID == record.Ref.ID {
			continue
		}
		frozen, err := exportDispatchStarted(ctx, tx, r.Ref.ID)
		if err != nil {
			return err
		}
		if frozen && !record.ExportKeepVersion {
			record.ExportSelected = false
			return nil
		}
	}
	record.ExportSelected = true
	if record.ExportKeepVersion {
		return nil
	}
	for _, r := range previous {
		if r.Ref.ID == record.Ref.ID || r.ExportKeepVersion {
			continue
		}
		r.ExportSelected = false
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=? WHERE key=?`, string(mustJSON(r)), attachmentPrefix+r.Ref.ID); err != nil {
			return err
		}
	}
	return nil
}
