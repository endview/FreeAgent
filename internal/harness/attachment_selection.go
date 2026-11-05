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
	err := store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'attachment/%' AND (document #>> '{conversation}')=$1 AND (document #>> '{request}')=$2 AND (document #>> '{export_source}')=$3 AND (document #>> '{ref,sha256}')=$4 AND coalesce(((document #>> '{deleted}')::boolean),false)=false LIMIT 1`, chat, run, source, hash).Scan(&raw)
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
	if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+r.Ref.ID); err != nil {
		return AttachmentRef{}, err
	}
	return r.Ref, tx.Commit()
}

func exportDispatchStarted(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/file-send/%' AND (document #>> '{attachment,id}')=$1 AND (document #>> '{state}') IN ('dispatching','sent','unknown')`, id).Scan(&count)
	return count > 0, err
}

func selectExportTx(ctx context.Context, tx *sql.Tx, record *attachmentRecord) error {
	rows, err := tx.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'attachment/%' AND (document #>> '{request}')=$1 AND (document #>> '{export_source}')=$2 AND ((document #>> '{export_selected}')::boolean)=true AND coalesce(((document #>> '{deleted}')::boolean),false)=false`, record.Request, record.ExportSource)
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
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+r.Ref.ID); err != nil {
			return err
		}
	}
	return nil
}
