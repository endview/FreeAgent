package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type BranchCommand struct {
	RequestKey string  `json:"request_key"`
	MessageID  int64   `json:"message_id"`
	Mode       string  `json:"mode"` // rerun user input, or continue completed assistant
	Prompt     *string `json:"prompt,omitempty"`
}
type BranchOrigin struct {
	Source    string `json:"source"`
	MessageID int64  `json:"message_id"`
	Cutoff    int64  `json:"cutoff"`
	Mode      string `json:"mode"`
	CreatedAt string `json:"created_at"`
}
type BranchResult struct {
	Conversation Conversation    `json:"conversation"`
	Origin       BranchOrigin    `json:"origin"`
	Prompt       string          `json:"prompt,omitempty"`
	Attachments  []AttachmentRef `json:"attachments,omitempty"`
	Duplicate    bool            `json:"duplicate"`
}

func branchSourceReady(ctx context.Context, tx *sql.Tx, chat string) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.runs r WHERE conversation_id=$1 AND (`+activeRunPredicate+` OR EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=r.id AND a.status IN ('pending','dispatched','unknown'))))`, chat).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		allowed, _ := ctx.Value(branchAllowedRunKey{}).(string)
		if allowed == "" {
			return ErrActiveRun
		}
		var safe bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.runs WHERE id=$1 AND conversation_id=$2 AND status='waiting_confirmation') AND NOT EXISTS(SELECT 1 FROM fa.runs WHERE conversation_id=$3 AND id!=$4 AND `+activeRunPredicate+`) AND NOT EXISTS(SELECT 1 FROM fa.attempts WHERE run_id=$5 AND (status IN ('dispatched','unknown') OR status='pending' AND (request_doc #>> '{Name}')!='control_plan_change'))`, allowed, chat, chat, allowed, allowed).Scan(&safe); err != nil {
			return err
		}
		if !safe {
			return ErrActiveRun
		}
	}
	return nil
}

func (tasks *TaskService) BranchConversation(ctx context.Context, chat string, command BranchCommand) (result BranchResult, returnErr error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return result, errors.New("harness: task service and context are required")
	}
	if command.RequestKey == "" || len(command.RequestKey) > 128 || command.MessageID < 1 || (command.Mode != "rerun" && command.Mode != "continue") {
		return result, ErrRequestKey
	}
	config := tasks.Config()
	if command.Prompt != nil && len(*command.Prompt) > config.MaxContextBytes {
		return result, ErrContextLimit
	}
	store := tasks.core.Store
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	requestMeta := "branch/request/" + chat + "/" + contentHash([]byte(command.RequestKey))
	digest := contentHash(mustJSON(command))
	var saved string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, requestMeta).Scan(&saved); err == nil {
		var previous struct {
			Digest string
			Result BranchResult
		}
		if json.Unmarshal([]byte(saved), &previous) != nil {
			return result, ErrStoreFormat
		}
		if previous.Digest != digest {
			return result, ErrRequestKey
		}
		previous.Result.Duplicate = true
		return previous.Result, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var source Conversation
	err = tx.QueryRowContext(ctx, `SELECT id,title,workspace,space_id,created_at,updated_at FROM fa.conversations WHERE id=$1`, chat).Scan(&source.ID, &source.Title, &source.Workspace, &source.SpaceID, &source.CreatedAt, &source.UpdatedAt)
	if err != nil {
		return result, err
	}
	_ = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "chat/model/"+chat).Scan(&source.ModelID)
	if source.Workspace != config.Workspace {
		return result, ErrConversationSpace
	}
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1`, source.SpaceID))
	if err != nil {
		return result, err
	}
	if space.Source != SpaceSourceLocal && !space.MultiConversation {
		return result, errors.New("harness: explicitly enable multiple conversations in this external space before branching")
	}
	if err := branchSourceReady(ctx, tx, chat); err != nil {
		return result, err
	}
	var cutRaw, cutRun string
	if err := tx.QueryRowContext(ctx, `SELECT body_json,coalesce(run_id,'') FROM fa.messages WHERE id=$1 AND conversation_id=$2`, command.MessageID, chat).Scan(&cutRaw, &cutRun); err != nil {
		return result, err
	}
	var cut ModelMessage
	if json.Unmarshal([]byte(cutRaw), &cut) != nil {
		return result, ErrStoreFormat
	}
	if cut.Deferred || command.Mode == "rerun" && cut.Role != "user" || command.Mode == "continue" && (cut.Role != "assistant" || len(cut.ToolCalls) != 0 || strings.TrimSpace(cut.Content) == "") {
		return result, errors.New("harness: choose a user message or a completed assistant reply")
	}
	if space.Source != SpaceSourceLocal && !cut.Public {
		return result, ErrPermissionDenied
	}
	if command.Mode == "continue" && cutRun != "" {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM fa.runs WHERE id=$1`, cutRun).Scan(&status); err != nil {
			return result, err
		}
		if status != "completed" {
			return result, ErrModelBoundary
		}
	}
	cutoff := command.MessageID
	if command.Mode == "rerun" {
		cutoff--
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,body_json FROM fa.messages WHERE conversation_id=$1 AND id<=$2 AND coalesce(((body_json #>> '{Deferred}')::boolean),false)=false ORDER BY id`, chat, cutoff)
	if err != nil {
		return result, err
	}
	var messages []ModelMessage
	total := 0
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return result, err
		}
		total += len(raw)
		if total > 16<<20 || len(messages) >= 65536 {
			rows.Close()
			return result, ErrContextLimit
		}
		var m ModelMessage
		if json.Unmarshal([]byte(raw), &m) != nil {
			rows.Close()
			return result, ErrStoreFormat
		}
		if space.Source != SpaceSourceLocal {
			projected := publicMessages([]Message{{ID: id, ModelMessage: m}})
			for _, p := range projected {
				if p.Public {
					messages = append(messages, p.ModelMessage)
				}
			}
		} else {
			messages = append(messages, m)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	if err := completeMessageGroups(messages); err != nil {
		return result, ErrModelBoundary
	}
	title := []rune(source.Title)
	if len(title) > 72 {
		title = title[:72]
	}
	result.Conversation = Conversation{ID: newID(), Title: string(title) + " · 分支", Workspace: source.Workspace, SpaceID: source.SpaceID, CreatedAt: nowText(), UpdatedAt: nowText(), ModelID: source.ModelID}
	result.Origin = BranchOrigin{Source: chat, MessageID: command.MessageID, Cutoff: cutoff, Mode: command.Mode, CreatedAt: nowText()}
	if command.Mode == "rerun" {
		result.Prompt = cut.Content
		result.Attachments = append([]AttachmentRef(nil), cut.Attachments...)
	}
	if command.Prompt != nil {
		result.Prompt = *command.Prompt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)`, result.Conversation.ID, result.Conversation.Title, result.Conversation.Workspace, result.Conversation.SpaceID, result.Conversation.CreatedAt, result.Conversation.UpdatedAt); err != nil {
		return result, err
	}
	if source.ModelID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "chat/model/"+result.Conversation.ID, source.ModelID); err != nil {
			return result, err
		}
	}
	var root *os.Root
	var published []string
	defer func() {
		if root != nil {
			if returnErr != nil {
				for _, id := range published {
					_ = root.Chmod(id, 0600)
					_ = root.Remove(id)
				}
			}
			root.Close()
		}
	}()
	cache := map[string]AttachmentRef{}
	var used int64
	var files int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(CAST(((document #>> '{ref,bytes}')::bigint) AS INTEGER)),0),count(*) FROM fa.meta WHERE key LIKE 'attachment/%' AND coalesce(((document #>> '{deleted}')::boolean),false)=false`).Scan(&used, &files); err != nil {
		return result, err
	}
	copyRefs := func(refs []AttachmentRef) ([]AttachmentRef, error) {
		copied := make([]AttachmentRef, 0, len(refs))
		for _, ref := range refs {
			if saved, ok := cache[ref.ID]; ok {
				copied = append(copied, saved)
				continue
			}
			r, err := loadAttachment(ctx, tx, ref.ID)
			if err != nil || r.Deleted || !r.Committed || r.Conversation != chat || r.Space != source.SpaceID || !sameAttachmentPayload(r.Ref, ref) || space.Source != SpaceSourceLocal && !ref.Public {
				return nil, ErrAttachment
			}
			raw, err := readAttachmentBlob(store.dataDir, ref)
			if err != nil {
				return nil, err
			}
			used += int64(len(raw))
			files++
			if used > config.Attachments.effective().QuotaBytes || files > 10000 {
				return nil, ErrAttachment
			}
			if root == nil {
				root, err = attachmentRoot(store.dataDir, true)
				if err != nil {
					return nil, err
				}
			}
			old := ref.ID
			ref.ID = newID()
			ref.Unavailable = false
			file, err := root.OpenFile(ref.ID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return nil, err
			}
			published = append(published, ref.ID)
			_, writeErr := file.Write(raw)
			err = errors.Join(writeErr, file.Sync(), file.Chmod(0400), file.Close())
			if err != nil {
				return nil, err
			}
			r.Ref = ref
			r.Conversation = result.Conversation.ID
			r.Principal = attachmentIdentity(ctx)
			r.Request = "branch:" + command.RequestKey
			r.ExportSource = ""
			r.ExportSelected = false
			r.ExportKeepVersion = false
			r.Created = nowText()
			if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, attachmentPrefix+ref.ID, string(mustJSON(r))); err != nil {
				return nil, err
			}
			cache[old] = ref
			copied = append(copied, ref)
		}
		return copied, nil
	}
	for _, m := range messages {
		m.Attachments, err = copyRefs(m.Attachments)
		if err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2)`, result.Conversation.ID, string(mustJSON(m))); err != nil {
			return result, err
		}
	}
	result.Attachments, err = copyRefs(result.Attachments)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "conversation/branch/"+result.Conversation.ID, string(mustJSON(result.Origin))); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, requestMeta, string(mustJSON(struct {
		Digest string
		Result BranchResult
	}{digest, result}))); err != nil {
		return result, err
	}
	// No summary is copied: the independent raw prefix cannot carry future text.
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("branch commit unconfirmed; inspect the same request key before retrying: %w", err)
	}
	return result, nil
}
