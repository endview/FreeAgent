package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type attachmentCommit struct {
	XID string        `json:"xid"`
	Ref AttachmentRef `json:"ref"`
}

func (store *Store) attachmentCommitPath(id string) string {
	return filepath.Join(store.dataDir, "attachment-commits", id+".json")
}
func (store *Store) commitAttachment(ctx context.Context, tx *sql.Tx, ref AttachmentRef) error {
	var xid string
	if err := tx.QueryRowContext(ctx, `SELECT pg_current_xact_id()::text`).Scan(&xid); err != nil {
		return err
	}
	path := store.attachmentCommitPath(ref.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := writeNewFile(path, mustJSON(attachmentCommit{XID: xid, Ref: ref})); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(errors.New("harness: attachment commit UNKNOWN; immutable file retained for transaction reconciliation"), err)
	}
	return os.Remove(path)
}
func (store *Store) reconcileAttachmentCommits(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(store.dataDir, "attachment-commits"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(64, len(entries))] {
		if entry.IsDir() {
			return ErrAttachment
		}
		path := filepath.Join(store.dataDir, "attachment-commits", entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > 16384 {
			return ErrAttachment
		}
		var pending attachmentCommit
		if json.Unmarshal(raw, &pending) != nil || !attachmentID(pending.Ref.ID) || pending.XID == "" || filepath.Base(store.attachmentCommitPath(pending.Ref.ID)) != entry.Name() {
			return ErrAttachment
		}
		var status sql.NullString
		if err := store.db.QueryRowContext(ctx, `SELECT pg_xact_status($1::xid8)`, pending.XID).Scan(&status); err != nil {
			return err
		}
		if !status.Valid || status.String == "in progress" {
			continue
		}
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key=$1`, attachmentPrefix+pending.Ref.ID).Scan(&count); err != nil {
			return err
		}
		if status.String == "committed" && count != 1 || status.String == "aborted" && count != 0 {
			return ErrAttachment
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
