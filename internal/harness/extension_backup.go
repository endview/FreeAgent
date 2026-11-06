package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const extensionPairFormat = "freeagent-extension-pair-v1"

type extensionPairManifest struct {
	Format      string `json:"format"`
	ID          string `json:"extension_id"`
	Version     string `json:"extension_version"`
	CoreID      string `json:"core_id"`
	DumpHash    string `json:"extension_dump_sha256"`
	SourcesHash string `json:"sources_sha256"`
	JournalHash string `json:"control_journal_sha256"`
	Created     string `json:"created_at"`
}
type extensionSourcePage struct {
	CoreID  string            `json:"core_id"`
	Sources []ExtensionSource `json:"sources"`
	Next    string            `json:"next"`
}

func (service *Service) verifyExtensionBinding(ctx context.Context, id string, client *extensionClient) error {
	client.sourceMu.Lock()
	defer client.sourceMu.Unlock()
	if client.sourcesVerified {
		return nil
	}
	sources, err := NewTaskService(service).extensionSources(ctx, id)
	if err != nil {
		return err
	}
	tx, err := service.Store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyExtensionSources(ctx, tx, sources); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	client.sourcesVerified = true
	return nil
}

func (tasks *TaskService) extensionSources(ctx context.Context, id string) ([]ExtensionSource, error) {
	result := []ExtensionSource{}
	cursor := ""
	seen := map[string]bool{}
	c, err := postgres.Read(filepath.Join(tasks.DataDir(), postgres.ConfigFile))
	if err != nil {
		return nil, err
	}
	for {
		raw, err := tasks.extensionStateRPC(ctx, id, "state.sources", map[string]string{"cursor": cursor})
		if err != nil {
			return nil, err
		}
		var page extensionSourcePage
		if json.Unmarshal(raw, &page) != nil || len(page.Sources) > 128 || len(result)+len(page.Sources) > 100000 {
			return nil, errors.New("harness: extension source validation bound exceeded")
		}
		if page.CoreID != c.CoreID {
			return nil, errors.New("harness: extension/Core identity mismatch")
		}
		result = append(result, page.Sources...)
		if page.Next == "" {
			return result, nil
		}
		if seen[page.Next] || len(page.Next) > 2048 {
			return nil, ErrStoreFormat
		}
		seen[page.Next] = true
		cursor = page.Next
	}
}
func verifyExtensionSources(ctx context.Context, tx *sql.Tx, sources []ExtensionSource) error {
	for _, source := range sources {
		parts := strings.Split(source.ID, "/")
		if len(parts) != 2 {
			return ErrStoreFormat
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id < 1 {
			return ErrStoreFormat
		}
		var raw []byte
		var run, space, created string
		err = tx.QueryRowContext(ctx, `SELECT m.body_json,m.run_id,c.space_id,COALESCE((SELECT COALESCE(NULLIF(document->>'adopted_at',''),document->>'created_at') FROM fa.meta WHERE starts_with(key,$3) AND document->>'message_id'=m.id::text LIMIT 1),r.created_at) FROM fa.messages m JOIN fa.conversations c ON c.id=m.conversation_id JOIN fa.runs r ON r.id=m.run_id WHERE m.conversation_id=$1 AND m.id=$2`, parts[0], id, inputPrefix(parts[0])).Scan(&raw, &run, &space, &created)
		if err != nil {
			return errors.New("harness: extension is ahead of or missing its original Core source; matching paired backup required")
		}
		var m ModelMessage
		if json.Unmarshal(raw, &m) != nil || m.Role != "user" || m.Deferred || run != source.RunID || space != source.SpaceID {
			return ErrSnapshotIncomplete
		}
		actor := "owner"
		if m.Channel != nil {
			actor = m.Channel.PersonID
		}
		if actor != source.ActorID {
			return ErrPermissionDenied
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return ErrStoreFormat
		}
		input := struct {
			ID    string    `json:"id"`
			RunID string    `json:"run_id"`
			Text  string    `json:"text"`
			At    time.Time `json:"occurred_at"`
		}{source.ID, run, truncateUTF8Bytes(m.Content, 64<<10), at}
		if contentHash(mustJSON(input)) != source.Hash {
			return errors.New("harness: extension input content does not match original Core source")
		}
	}
	return nil
}
func (tasks *TaskService) reconcileExtensionWrites(ctx context.Context, id string) error {
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	control.mu.Lock()
	var ids []string
	for key, r := range control.operations {
		if r.Operation.Kind != ControlChangeExtensionState || !r.ExtensionDispatched || r.Operation.Status == ControlApplied || r.Operation.Status == ControlFailed {
			continue
		}
		var a ExtensionStateChange
		if json.Unmarshal(r.Arguments, &a) == nil && a.ID == id {
			ids = append(ids, key)
		}
	}
	control.mu.Unlock()
	for _, key := range ids {
		if _, err = control.ReconcileExtensionChange(ctx, key); err != nil {
			return errors.New("harness: extension write unresolved; verify original operation before backup")
		}
	}
	return nil
}

// Only extension writeback is paused. Ordinary Core model/tool dispatch remains
// available while PostgreSQL exports and dumps its consistent snapshot.
func (tasks *TaskService) BackupExtensionPair(ctx context.Context, id, target string) (returnErr error) {
	if !validExtensionID(id) {
		return ErrPermissionDenied
	}
	target, err := canonicalDir(target)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: paired backup target must not exist")
	}
	if err = tasks.reconcileExtensionWrites(ctx, id); err != nil {
		return err
	}
	deadline := time.Now().Add(110 * time.Second).UTC()
	backupCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	pauseID := newID()
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e := tasks.extensionStateRPC(release, id, "state.resume", map[string]string{"pause_id": pauseID})
		returnErr = errors.Join(returnErr, e)
	}()
	if _, err = tasks.extensionStateRPC(backupCtx, id, "state.pause", map[string]any{"pause_id": pauseID, "until": deadline}); err != nil {
		return err
	}
	if err = tasks.reconcileExtensionWrites(backupCtx, id); err != nil {
		return err
	}
	sources, err := tasks.extensionSources(backupCtx, id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".extension-pair-*")
	if err != nil {
		return err
	}
	defer removeStaging(stage)
	c, err := postgres.Read(extensionStoragePath(tasks.DataDir(), id))
	if err != nil {
		return err
	}
	cmd, err := postgres.Command(backupCtx, c, "pg_dump", "--format=custom", "--data-only", "--schema=aw", "--no-owner", "--no-privileges", "--file="+filepath.Join(stage, "extension.pgdump"))
	if err != nil {
		return err
	}
	if err = cmd.Run(); err != nil {
		return errors.New("harness: extension pg_dump failed; no complete paired backup published")
	}
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	control.mu.Lock()
	locked := true
	defer func() {
		if locked {
			control.mu.Unlock()
		}
	}()
	err = tasks.core.Store.backupSnapshot(backupCtx, filepath.Join(stage, "core"), func(checkCtx context.Context, tx *sql.Tx) error {
		if _, e := os.Stat(control.path); e == nil {
			if e = copyRegularFile(control.path, filepath.Join(stage, "control-journal.jsonl")); e != nil {
				return e
			}
		} else if errors.Is(e, os.ErrNotExist) {
			if e = writeNewFile(filepath.Join(stage, "control-journal.jsonl"), nil); e != nil {
				return e
			}
		} else {
			return e
		}
		locked = false
		control.mu.Unlock()
		return verifyExtensionSources(checkCtx, tx, sources)
	})
	if err != nil {
		return err
	}
	raw, err := tasks.extensionStateRPC(backupCtx, id, "state.pause_status", map[string]string{"pause_id": pauseID})
	if err != nil {
		return err
	}
	var status struct {
		Paused bool `json:"paused"`
	}
	if json.Unmarshal(raw, &status) != nil || !status.Paused {
		return errors.New("harness: extension pause expired or process changed; backup not published")
	}
	info, err := tasks.Extension(ctx, id)
	if err != nil {
		return err
	}
	if err = writeNewFile(filepath.Join(stage, "sources.json"), mustJSON(sources)); err != nil {
		return err
	}
	dumpHash, err := hashFile(filepath.Join(stage, "extension.pgdump"))
	if err != nil {
		return err
	}
	sourceHash, err := hashFile(filepath.Join(stage, "sources.json"))
	if err != nil {
		return err
	}
	journalHash, err := hashFile(filepath.Join(stage, "control-journal.jsonl"))
	if err != nil {
		return err
	}
	m := extensionPairManifest{extensionPairFormat, id, info.Version, c.CoreID, dumpHash, sourceHash, journalHash, nowText()}
	if err = writeNewFile(filepath.Join(stage, "pair.json"), mustJSON(m)); err != nil {
		return err
	}
	if _, err = VerifyExtensionPair(backupCtx, stage); err != nil {
		return err
	}
	return publishDirectory(stage, target)
}
func VerifyExtensionPair(ctx context.Context, path string) (extensionPairManifest, error) {
	var m extensionPairManifest
	for _, name := range []string{"pair.json", "extension.pgdump", "sources.json", "control-journal.jsonl"} {
		if err := verifyRegularFile(filepath.Join(path, name)); err != nil {
			return m, err
		}
	}
	b, err := os.ReadFile(filepath.Join(path, "pair.json"))
	if err != nil || len(b) > 16384 || json.Unmarshal(b, &m) != nil || m.Format != extensionPairFormat || !validExtensionID(m.ID) {
		return m, ErrStoreFormat
	}
	for name, hash := range map[string]string{"extension.pgdump": m.DumpHash, "sources.json": m.SourcesHash, "control-journal.jsonl": m.JournalHash} {
		if actual, e := hashFile(filepath.Join(path, name)); e != nil || actual != hash {
			return m, errors.New("harness: paired backup integrity check failed")
		}
	}
	if err = VerifyBackup(ctx, filepath.Join(path, "core")); err != nil {
		return m, err
	}
	var identity backupIdentity
	b, err = os.ReadFile(filepath.Join(path, "core", "identity.json"))
	if err != nil || json.Unmarshal(b, &identity) != nil || identity.CoreID != m.CoreID {
		return m, errors.New("harness: paired Core identity mismatch")
	}
	if _, _, e := ControlMaintenance(path); e != nil {
		return m, e
	}
	return m, nil
}
