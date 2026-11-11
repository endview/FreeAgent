package harness

// Attachment bytes live outside PostgreSQL. Only bounded, verified references use
// existing message, Run and meta records. Publication always precedes binding.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrAttachment = errors.New("harness: attachment unavailable or unauthorized")
var ErrAttachmentLimit = errors.New("harness: input attachment limit exceeded")

const attachmentPrefix = "attachment/"

type AttachmentPolicy struct {
	Text           bool  `json:"text"`
	PNG            bool  `json:"png"`
	JPEG           bool  `json:"jpeg"`
	MaxFiles       int   `json:"max_files"`
	MaxBatchBytes  int64 `json:"max_batch_bytes"`
	MaxInputBytes  int64 `json:"max_input_bytes"`
	MaxExportBytes int64 `json:"max_export_bytes"`
	MaxPixels      int64 `json:"max_pixels"`
	QuotaBytes     int64 `json:"quota_bytes"`
	StagingHours   int   `json:"staging_hours"`
}

func DefaultAttachmentPolicy() AttachmentPolicy {
	return AttachmentPolicy{Text: true, PNG: true, JPEG: true, MaxFiles: 8, MaxBatchBytes: 32 << 20, MaxInputBytes: 8 << 20, MaxExportBytes: 32 << 20, MaxPixels: 16_000_000, QuotaBytes: 1 << 30, StagingHours: 24}
}
func (p AttachmentPolicy) effective() AttachmentPolicy {
	if p == (AttachmentPolicy{}) {
		return DefaultAttachmentPolicy()
	}
	return p
}
func (p AttachmentPolicy) validate() error {
	p = p.effective()
	if p.MaxFiles < 1 || p.MaxFiles > 8 || p.MaxBatchBytes < 1 || p.MaxBatchBytes > 64<<20 || p.MaxInputBytes < 1 || p.MaxInputBytes > 16<<20 || p.MaxExportBytes < 1 || p.MaxExportBytes > 64<<20 || p.MaxPixels < 1 || p.MaxPixels > 32_000_000 || p.QuotaBytes < 1 || p.QuotaBytes > 16<<30 || p.StagingHours < 1 || p.StagingHours > 168 {
		return errors.New("harness: attachment limits must be finite and within supported bounds")
	}
	return nil
}

type AttachmentRef struct {
	Unavailable bool   `json:"unavailable,omitempty"`
	ID          string `json:"id"`
	SHA256      string `json:"sha256"`
	Name        string `json:"name"`
	MIME        string `json:"mime"`
	Bytes       int64  `json:"bytes"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Source      string `json:"source"`
	Public      bool   `json:"public,omitempty"`
}
type attachmentRecord struct {
	ExportSource      string        `json:"export_source,omitempty"`
	ExportSelected    bool          `json:"export_selected,omitempty"`
	ExportKeepVersion bool          `json:"export_keep_version,omitempty"`
	Ref               AttachmentRef `json:"ref"`
	Conversation      string        `json:"conversation"`
	Space             string        `json:"space"`
	Principal         string        `json:"principal"`
	Created           string        `json:"created"`
	Committed         bool          `json:"committed"`
	Deleted           bool          `json:"deleted,omitempty"`
	Request           string        `json:"request,omitempty"`
}

type attachmentIdentityKey struct{}

// WithAttachmentIdentity binds pending uploads to the authenticated entry.
// Committed references still require the exact current conversation and space.
func WithAttachmentIdentity(ctx context.Context, identity string) context.Context {
	return context.WithValue(ctx, attachmentIdentityKey{}, identity)
}
func attachmentIdentity(ctx context.Context) string {
	if value, _ := ctx.Value(attachmentIdentityKey{}).(string); value != "" {
		return value
	}
	return "local"
}
func attachmentID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}
func attachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	if len([]rune(name)) > 160 {
		name = string([]rune(name)[:160])
	}
	return name
}
func attachmentRoot(data string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(data)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if create {
		if err := root.Mkdir("attachments", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := root.Lstat("attachments")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAttachment
	}
	if err := rejectReparsePoint(filepath.Join(data, "attachments")); err != nil {
		return nil, ErrAttachment
	}
	return root.OpenRoot("attachments")
}
func detectAttachment(file *os.File, name string, size int64, p AttachmentPolicy, input bool) (AttachmentRef, error) {
	ref := AttachmentRef{Name: attachmentName(name), Bytes: size, Source: "upload"}
	if _, err := file.Seek(0, 0); err != nil {
		return ref, err
	}
	prefix := make([]byte, min(size, 512))
	_, _ = io.ReadFull(file, prefix)
	ref.MIME = http.DetectContentType(prefix)
	if ref.MIME == "image/png" || ref.MIME == "image/jpeg" {
		if input && (ref.MIME == "image/png" && !p.PNG || ref.MIME == "image/jpeg" && !p.JPEG) {
			return ref, errors.New("harness: this image format is disabled")
		}
		_, _ = file.Seek(0, 0)
		config, format, err := image.DecodeConfig(file)
		if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > p.MaxPixels/int64(config.Height) || config.Width > 16384 || config.Height > 16384 {
			if !input {
				ref.MIME = "application/octet-stream"
				return ref, nil
			}
			return ref, errors.New("harness: image dimensions are invalid or exceed the pixel limit")
		}
		if format != "png" && format != "jpeg" {
			return ref, ErrAttachment
		}
		ref.Width, ref.Height = config.Width, config.Height
		// Decode once, with bounded pixels and serialized imports, to reject
		// truncated/invalid images before allowing inline browser display.
		_, _ = file.Seek(0, 0)
		if _, _, err := image.Decode(file); err != nil {
			if !input {
				ref.MIME = "application/octet-stream"
				ref.Width = 0
				ref.Height = 0
				return ref, nil
			}
			return ref, errors.New("harness: invalid or incomplete image")
		}
	} else if input {
		if !p.Text {
			return ref, errors.New("harness: text attachments are disabled")
		}
		ext := strings.ToLower(filepath.Ext(ref.Name))
		allowed := strings.Contains("|.txt|.md|.json|.csv|.go|.py|.js|.mjs|.ts|.tsx|.jsx|.html|.css|.xml|.yaml|.yml|.toml|.ini|.sh|.ps1|.rs|.c|.h|.cpp|.sql|.log|", "|"+ext+"|")
		if !allowed {
			return ref, errors.New("harness: input supports UTF-8 text, PNG and JPEG only")
		}
		_, _ = file.Seek(0, 0)
		raw, err := io.ReadAll(io.LimitReader(file, p.MaxInputBytes+1))
		if err != nil || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
			return ref, errors.New("harness: input must be NUL-free UTF-8 text")
		}
		ref.MIME = "text/plain; charset=utf-8"
	}
	return ref, nil
}

func loadAttachment(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (attachmentRecord, error) {
	if !attachmentID(id) {
		return attachmentRecord{}, ErrAttachment
	}
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, attachmentPrefix+id).Scan(&raw); err != nil {
		return attachmentRecord{}, ErrAttachment
	}
	var record attachmentRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.Ref.ID != id || len(record.Ref.SHA256) != 64 || record.Ref.Bytes < 0 {
		return record, ErrAttachment
	}
	return record, nil
}
func (tasks *TaskService) attachmentConversation(ctx context.Context, chat string) (Conversation, error) {
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil || conversation.Workspace != tasks.Config().Workspace {
		return Conversation{}, ErrAttachment
	}
	return conversation, nil
}

func (tasks *TaskService) UploadAttachment(ctx context.Context, chat, name string, reader io.Reader) (AttachmentRef, error) {
	return tasks.createAttachment(ctx, chat, name, reader, "upload", "", false)
}
func (tasks *TaskService) UploadAttachmentRequest(ctx context.Context, chat, name, request string, reader io.Reader) (AttachmentRef, error) {
	if len(request) > 160 {
		return AttachmentRef{}, ErrRequestKey
	}
	return tasks.createAttachment(ctx, chat, name, reader, "upload", request, false)
}
func OpenUserAttachmentFile(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := canonicalDir(filepath.Dir(absolute)); err != nil {
		return nil, err
	}
	if err := verifyRegularFile(absolute); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := openWorkspaceFile(root, filepath.Base(absolute))
	if err != nil {
		return nil, err
	}
	if err := checkRegularHandle(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func (service *Service) attachmentPolicy() AttachmentPolicy {
	if service.liveConfig != nil {
		return service.liveConfig().Attachments.effective()
	}
	return service.currentConfig().Attachments.effective()
}

func (tasks *TaskService) createAttachment(ctx context.Context, chat, name string, reader io.Reader, source, request string, committed bool, selections ...exportSelection) (AttachmentRef, error) {
	conversation, err := tasks.attachmentConversation(ctx, chat)
	if err != nil {
		return AttachmentRef{}, err
	}
	store := tasks.core.Store
	// Same publication lock is used by binding, cleanup, deletion and backup.
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	p := tasks.core.attachmentPolicy()
	limit := p.MaxInputBytes
	if source != "upload" {
		limit = p.MaxExportBytes
	}
	root, err := attachmentRoot(store.dataDir, true)
	if err != nil {
		return AttachmentRef{}, err
	}
	defer root.Close()
	if err := store.cleanupAttachmentsLocked(ctx, root, p, 64); err != nil {
		return AttachmentRef{}, err
	}
	used, count, err := attachmentUsage(root)
	if err != nil {
		return AttachmentRef{}, err
	}
	limit = min(limit, p.QuotaBytes)
	id := newID()
	file, err := root.OpenFile(id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return AttachmentRef{}, err
	}
	published := false
	defer func() {
		file.Close()
		if !published {
			_ = root.Chmod(id, 0600)
			_ = root.Remove(id)
		}
	}()
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, limit+1))
	if copyErr != nil || ctx.Err() != nil {
		return AttachmentRef{}, errors.Join(copyErr, ctx.Err())
	}
	if size > limit {
		return AttachmentRef{}, errors.New("harness: attachment exceeds the file or storage limit")
	}
	ref, err := detectAttachment(file, name, size, p, source == "upload")
	if err != nil {
		return AttachmentRef{}, err
	}
	ref.ID, ref.SHA256, ref.Source = id, hex.EncodeToString(hash.Sum(nil)), source
	space, err := tasks.core.Store.GetSpace(ctx, conversation.SpaceID)
	if err != nil {
		return AttachmentRef{}, err
	}
	ref.Public = space.Source != SpaceSourceLocal && source == "upload"
	var selection exportSelection
	if source == "file_export" && len(selections) > 0 {
		selection = selections[0]
	}
	if selection.Source != "" {
		if previous, err := store.identicalExport(ctx, chat, request, selection.Source, ref.SHA256, selection.KeepVersion); err != nil {
			return AttachmentRef{}, err
		} else if previous.ID != "" {
			return previous, nil
		}
	}
	if source == "upload" && request != "" {
		var existing string
		err := store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'attachment/%' AND (document #>> '{conversation}')=$1 AND (document #>> '{principal}')=$2 AND (document #>> '{request}')=$3 AND (document #>> '{ref,source}')='upload' LIMIT 1`, chat, attachmentIdentity(ctx), request).Scan(&existing)
		if err == nil {
			var previous attachmentRecord
			if json.Unmarshal([]byte(existing), &previous) != nil || previous.Deleted || previous.Ref.SHA256 != ref.SHA256 || previous.Ref.Name != ref.Name || previous.Ref.Bytes != ref.Bytes {
				return AttachmentRef{}, ErrRequestKey
			}
			if _, err := readAttachmentBlob(store.dataDir, previous.Ref); err != nil {
				return AttachmentRef{}, err
			}
			return previous.Ref, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return AttachmentRef{}, err
		}
	}
	if used+size > p.QuotaBytes || count >= 10000 {
		return AttachmentRef{}, errors.New("harness: attachment storage quota or file count reached; explicitly delete unused attachments")
	}
	if source == "file_export" {
		var exports int
		if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'attachment/%' AND (document #>> '{request}')=$1 AND (document #>> '{ref,source}')='file_export'`, request).Scan(&exports); err != nil {
			return AttachmentRef{}, err
		}
		if exports >= p.MaxFiles {
			return AttachmentRef{}, errors.New("harness: this Run has reached its export reference limit")
		}
	}
	if err := file.Sync(); err != nil {
		return ref, err
	}
	if err := file.Chmod(0400); err != nil {
		return ref, err
	}
	if err := file.Close(); err != nil {
		return ref, err
	}
	record := attachmentRecord{Ref: ref, Conversation: chat, Space: conversation.SpaceID, Principal: attachmentIdentity(ctx), Created: nowText(), Committed: committed, Request: request}
	record.ExportSource, record.ExportKeepVersion = selection.Source, selection.KeepVersion
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return ref, err
	}
	defer tx.Rollback()
	if selection.Source != "" {
		if err := selectExportTx(ctx, tx, &record); err != nil {
			return ref, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, attachmentPrefix+id, string(mustJSON(record)))
	if err != nil {
		return ref, err
	}
	// A missing COMMIT response cannot prove that the reference was rejected.
	// Keep the immutable bytes until the exact transaction is reconciled.
	published = true
	if err := store.commitAttachment(ctx, tx, ref); err != nil {
		return ref, err
	}
	return ref, nil
}

func (store *Store) cleanupAttachmentsLocked(ctx context.Context, root *os.Root, p AttachmentPolicy, limit int) error {
	if err := store.reconcileAttachmentCommits(ctx); err != nil {
		return err
	}
	cutoff := time.Now().Add(-time.Duration(p.StagingHours) * time.Hour).UTC().Format(time.RFC3339Nano)
	rows, err := store.db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE key LIKE 'attachment/%' AND ((document #>> '{committed}')::boolean)=false AND COALESCE((document->>'deleted')::boolean,false)=false AND (document #>> '{created}')<$1 LIMIT $2`, cutoff, limit)
	if err != nil {
		return err
	}
	var stale []attachmentRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var r attachmentRecord
		if json.Unmarshal([]byte(raw), &r) != nil || !attachmentID(r.Ref.ID) {
			rows.Close()
			return ErrAttachment
		}
		stale = append(stale, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, r := range stale {
		if _, err := os.Lstat(store.attachmentCommitPath(r.Ref.ID)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		r.Deleted = true
		if _, err := store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+r.Ref.ID); err != nil {
			return err
		}
		_ = root.Chmod(r.Ref.ID, 0600)
		if err := root.Remove(r.Ref.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// A process may die after durable file publication but before SQLite commit.
	// Inspect a rotating bounded page of such unreferenced files, never a live
	// upload (publication/binding/backup all share attachmentMu).
	folder, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, scanErr := folder.ReadDir(10001)
	folder.Close()
	if scanErr != nil && scanErr != io.EOF {
		return scanErr
	}
	if len(entries) > 10000 {
		return errors.New("harness: attachment directory exceeds its finite file bound")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var cursorText string
	_ = store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='attachment-cleanup-cursor'`).Scan(&cursorText)
	cursor, _ := strconv.Atoi(cursorText)
	if len(entries) > 0 {
		cursor = cursor % len(entries)
		for i := 0; i < min(limit, len(entries)); i++ {
			entry := entries[(cursor+i)%len(entries)]
			if !attachmentID(entry.Name()) || !entry.Type().IsRegular() {
				continue
			}
			if _, err := os.Lstat(store.attachmentCommitPath(entry.Name())); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			info, err := entry.Info()
			if err != nil || !info.ModTime().Before(time.Now().Add(-time.Duration(p.StagingHours)*time.Hour)) {
				continue
			}
			var exists int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key=$1`, attachmentPrefix+entry.Name()).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				_ = root.Chmod(entry.Name(), 0600)
				if err := root.Remove(entry.Name()); err != nil {
					return err
				}
			}
		}
		cursor = (cursor + limit) % len(entries)
	} else {
		cursor = 0
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('attachment-cleanup-cursor',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(cursor))
	if err != nil {
		return err
	}
	return nil
}

func (tasks *TaskService) Attachment(ctx context.Context, chat, id string) (AttachmentRef, []byte, error) {
	conversation, err := tasks.attachmentConversation(ctx, chat)
	if err != nil {
		return AttachmentRef{}, nil, err
	}
	store := tasks.core.Store
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	r, err := loadAttachment(ctx, store.db, id)
	if err != nil || r.Deleted || r.Conversation != chat || r.Space != conversation.SpaceID || !r.Committed && r.Principal != attachmentIdentity(ctx) {
		return AttachmentRef{}, nil, ErrAttachment
	}
	raw, err := readAttachmentBlob(store.dataDir, r.Ref)
	return r.Ref, raw, err
}
func readAttachmentBlob(data string, ref AttachmentRef) ([]byte, error) {
	if !attachmentID(ref.ID) || ref.Bytes < 0 || ref.Bytes > 64<<20 {
		return nil, ErrAttachment
	}
	root, err := attachmentRoot(data, false)
	if err != nil {
		return nil, ErrAttachment
	}
	defer root.Close()
	file, err := openWorkspaceFile(root, ref.ID)
	if err != nil {
		return nil, ErrAttachment
	}
	defer file.Close()
	if err := checkRegularHandle(file); err != nil {
		return nil, ErrAttachment
	}
	raw, err := io.ReadAll(io.LimitReader(file, ref.Bytes+1))
	if err != nil || int64(len(raw)) != ref.Bytes || contentHash(raw) != ref.SHA256 {
		return nil, ErrAttachment
	}
	return raw, nil
}
func (tasks *TaskService) resolveAttachments(ctx context.Context, chat string, ids []string) ([]AttachmentRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	p := tasks.core.attachmentPolicy()
	if len(ids) > p.MaxFiles {
		return nil, fmt.Errorf("%w: at most %d files", ErrAttachmentLimit, p.MaxFiles)
	}
	conversation, err := tasks.attachmentConversation(ctx, chat)
	if err != nil {
		return nil, err
	}
	refs := make([]AttachmentRef, 0, len(ids))
	seen := map[string]bool{}
	var total int64
	for _, id := range ids {
		r, err := loadAttachment(ctx, tasks.core.Store.db, id)
		if err != nil || r.Deleted || r.Conversation != chat || r.Space != conversation.SpaceID || !r.Committed && r.Principal != attachmentIdentity(ctx) || seen[id] {
			return nil, ErrAttachment
		}
		seen[id] = true
		total += r.Ref.Bytes
		if total > p.MaxBatchBytes || r.Ref.Bytes > p.MaxInputBytes {
			return nil, fmt.Errorf("%w: reduce file size or batch size", ErrAttachmentLimit)
		}
		if _, err := readAttachmentBlob(tasks.DataDir(), r.Ref); err != nil {
			return nil, err
		}
		refs = append(refs, r.Ref)
	}
	return refs, nil
}
func bindAttachments(ctx context.Context, tx *sql.Tx, chat, runID string, refs []AttachmentRef) error {
	var space string
	if err := tx.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1`, chat).Scan(&space); err != nil {
		return err
	}
	for _, ref := range refs {
		r, err := loadAttachment(ctx, tx, ref.ID)
		if err != nil || r.Deleted || r.Conversation != chat || r.Space != space || !sameAttachmentPayload(r.Ref, ref) || !r.Committed && r.Principal != attachmentIdentity(ctx) {
			return ErrAttachment
		}
		r.Committed = true
		if r.Ref.Source != "upload" {
			r.Request = runID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+ref.ID); err != nil {
			return err
		}
	}
	return nil
}

func sameAttachmentPayload(a, b AttachmentRef) bool {
	a.Public = false
	b.Public = false
	a.Unavailable = false
	b.Unavailable = false
	return a == b
}
func (tasks *TaskService) SaveReplyAttachment(ctx context.Context, chat, runID, request string) (AttachmentRef, error) {
	if request == "" || len(request) > 128 {
		return AttachmentRef{}, ErrRequestKey
	}
	tasks.core.Store.replyMu.Lock()
	defer tasks.core.Store.replyMu.Unlock()
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil || run.ConversationID != chat || run.Output == "" {
		return AttachmentRef{}, ErrAttachment
	}
	// Existing meta provides idempotency; no separate delivery/task objects.
	key := "attachment-reply/" + contentHash([]byte(chat+"\x00"+request))
	var raw string
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err == nil {
		var saved struct {
			Run string
			Ref AttachmentRef
		}
		if json.Unmarshal([]byte(raw), &saved) != nil || saved.Run != runID {
			return AttachmentRef{}, ErrRequestKey
		}
		return saved.Ref, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AttachmentRef{}, err
	}
	ref, err := tasks.createAttachment(ctx, chat, "reply.md", strings.NewReader(run.Output), "reply", runID, true)
	if err != nil {
		return ref, err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(struct {
		Run string
		Ref AttachmentRef
	}{runID, ref})))
	return ref, err
}

func (refs AttachmentRef) String() string {
	return fmt.Sprintf("%s (%d bytes; SHA-256 %s)", refs.Name, refs.Bytes, refs.SHA256)
}

func attachmentUsage(root *os.Root) (int64, int, error) {
	folder, err := root.Open(".")
	if err != nil {
		return 0, 0, err
	}
	defer folder.Close()
	entries, err := folder.ReadDir(10001)
	if err != nil && err != io.EOF {
		return 0, 0, err
	}
	if len(entries) > 10000 {
		return 0, 0, errors.New("harness: attachment file count reached")
	}
	var total int64
	for _, entry := range entries {
		if !attachmentID(entry.Name()) || !entry.Type().IsRegular() {
			return 0, 0, ErrAttachment
		}
		info, err := entry.Info()
		if err != nil {
			return 0, 0, err
		}
		total += info.Size()
	}
	return total, len(entries), nil
}
