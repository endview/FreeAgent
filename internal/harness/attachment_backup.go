package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func snapshotAttachmentRefs(ctx context.Context, state string) ([]AttachmentRef, error) {
	db, err := sql.Open("sqlite", sqliteDSN(state, "ro"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'attachment/%' AND COALESCE(json_extract(value,'$.deleted'),0)=0 ORDER BY key LIMIT 10001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []AttachmentRef
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r attachmentRecord
		if json.Unmarshal([]byte(raw), &r) != nil || !attachmentID(r.Ref.ID) || len(r.Ref.SHA256) != 64 {
			return nil, ErrAttachment
		}
		refs = append(refs, r.Ref)
		if len(refs) > 10000 {
			return nil, ErrAttachment
		}
	}
	return refs, rows.Err()
}
func copyAttachmentSnapshot(ctx context.Context, source, target string) error {
	refs, err := snapshotAttachmentRefs(ctx, filepath.Join(target, "state.sqlite"))
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	if err := os.Mkdir(filepath.Join(target, "attachments"), 0700); err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err := readAttachmentBlob(source, ref); err != nil {
			return err
		}
		if err := copyRegularFile(filepath.Join(source, "attachments", ref.ID), filepath.Join(target, "attachments", ref.ID)); err != nil {
			return err
		}
	}
	return nil
}
func attachmentManifest(ctx context.Context, directory string) (map[string]string, error) {
	refs, err := snapshotAttachmentRefs(ctx, filepath.Join(directory, "state.sqlite"))
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, ref := range refs {
		if _, err := readAttachmentBlob(directory, ref); err != nil {
			return nil, err
		}
		result[ref.ID] = ref.SHA256
	}
	info, err := os.Lstat(filepath.Join(directory, "attachments"))
	if errors.Is(err, os.ErrNotExist) && len(refs) == 0 {
		return nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAttachment
	}
	root, err := attachmentRoot(directory, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	entries, err := folder.ReadDir(10001)
	if err != nil && len(entries) == 0 && len(refs) != 0 {
		return nil, ErrAttachment
	}
	if len(entries) != len(result) {
		return nil, ErrAttachment
	}
	for _, entry := range entries {
		if entry.IsDir() || result[entry.Name()] == "" {
			return nil, ErrAttachment
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}
