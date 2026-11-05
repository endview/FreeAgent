package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func snapshotAttachmentRefs(ctx context.Context, db attemptReader) ([]AttachmentRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'attachment/%' AND COALESCE(((document #>> '{deleted}')::boolean),false)=false ORDER BY key LIMIT 10001`)
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
func readSnapshotAttachmentRefs(directory string) ([]AttachmentRef, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "attachment-refs.json"))
	if err != nil || len(raw) > 4<<20 {
		return nil, ErrAttachment
	}
	var refs []AttachmentRef
	if json.Unmarshal(raw, &refs) != nil || len(refs) > 10000 {
		return nil, ErrAttachment
	}
	for _, ref := range refs {
		if !attachmentID(ref.ID) || len(ref.SHA256) != 64 {
			return nil, ErrAttachment
		}
	}
	return refs, nil
}
func copyAttachmentRefs(source, target string, refs []AttachmentRef) error {
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
	refs, err := readSnapshotAttachmentRefs(directory)
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
