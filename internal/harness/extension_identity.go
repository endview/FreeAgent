package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// These values come only from adopted persistent messages. Display names,
// queued inputs and model arguments never supply an execution identity.
type ExtensionAdoptedInput struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	Text       string `json:"text"`
	OccurredAt string `json:"occurred_at"`
}
type ExtensionIdentity struct {
	CoreID         string                  `json:"core_id"`
	SpaceID        string                  `json:"space_id"`
	ConversationID string                  `json:"conversation_id"`
	RunID          string                  `json:"run_id"`
	Inputs         []ExtensionAdoptedInput `json:"adopted_inputs"`
}

func extensionVariant(run Run, id string, config Config) string {
	var snapshot runSnapshot
	if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) == nil && snapshot.Capabilities != nil {
		if v := snapshot.Capabilities.Variants[id]; v != "" {
			return v
		}
	}
	if v, ok := extensionRunSettings(run, id, config)["role_id"].(string); ok {
		return v
	}
	return ""
}
func extensionRunSettings(run Run, id string, config Config) map[string]any {
	var snapshot runSnapshot
	if run.ConfigJSON != "" && json.Unmarshal([]byte(run.ConfigJSON), &snapshot) == nil {
		return snapshot.Config.Extensions.Settings[id]
	}
	return config.Extensions.Settings[id]
}
func (service *Service) extensionIdentity(ctx context.Context, run Run) (*ExtensionIdentity, error) {
	if run.ID == "" || run.ConversationID == "" {
		return nil, errors.New("harness: adopted extension input requires a Run")
	}
	chat, err := service.Store.GetConversation(ctx, run.ConversationID)
	if err != nil {
		return nil, err
	}
	id := &ExtensionIdentity{SpaceID: chat.SpaceID, ConversationID: chat.ID, RunID: run.ID, Inputs: []ExtensionAdoptedInput{}}
	if err = service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='core/id'`).Scan(&id.CoreID); err != nil {
		return nil, err
	}
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,body_json,COALESCE((SELECT COALESCE(NULLIF(document->>'adopted_at',''),document->>'created_at') FROM fa.meta WHERE starts_with(key,$3) AND document->>'message_id'=m.id::text LIMIT 1),$4) FROM fa.messages m WHERE run_id=$1 AND conversation_id=$2 AND body_json->>'Role'='user' AND COALESCE(body_json->>'Deferred','false')='false' ORDER BY id LIMIT 129`, run.ID, chat.ID, inputPrefix(chat.ID), run.CreatedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID int64
		var raw []byte
		var occurred string
		var m ModelMessage
		if err = rows.Scan(&messageID, &raw, &occurred); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &m) != nil {
			return nil, ErrSnapshotIncomplete
		}
		if m.Role != "user" || m.Deferred {
			continue
		}
		actor := "owner"
		if m.Channel != nil {
			if m.Channel.Purpose != "request" || !validPersonID(m.Channel.PersonID) {
				continue
			}
			actor = m.Channel.PersonID
		}
		id.Inputs = append(id.Inputs, ExtensionAdoptedInput{ID: chat.ID + "/" + fmt.Sprint(messageID), ActorID: actor, Text: truncateUTF8Bytes(m.Content, 64<<10), OccurredAt: occurred})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(id.Inputs) == 0 || len(id.Inputs) > 128 {
		return nil, errors.New("harness: adopted extension input unavailable or over bound")
	}
	return id, nil
}

type ExtensionRunFacts struct {
	Identity   *ExtensionIdentity `json:"identity"`
	Status     string             `json:"status"`
	Output     string             `json:"output"`
	UpdatedAt  string             `json:"updated_at"`
	Deliveries []json.RawMessage  `json:"deliveries"`
}

// A bounded reread of original Run/receipt records. It never invokes a model,
// sends a channel message or replays a tool to fill missing evidence.
func (tasks *TaskService) ExtensionRunFacts(ctx context.Context, chatID, runID string) (ExtensionRunFacts, error) {
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return ExtensionRunFacts{}, err
	}
	if run.ConversationID != chatID {
		return ExtensionRunFacts{}, ErrPermissionDenied
	}
	id, err := tasks.core.extensionIdentity(ctx, run)
	if err != nil {
		return ExtensionRunFacts{}, err
	}
	facts := ExtensionRunFacts{Identity: id, Status: run.Status, Output: truncateUTF8Bytes(run.Output, 64<<10), UpdatedAt: run.UpdatedAt, Deliveries: []json.RawMessage{}}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE starts_with(key,$1) ORDER BY key LIMIT 129`, "channel/delivery/"+run.ID+"/")
	if err != nil {
		return facts, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return facts, err
		}
		var v map[string]any
		if json.Unmarshal([]byte(value), &v) != nil {
			return facts, ErrSnapshotIncomplete
		}
		v["receipt_id"] = key
		facts.Deliveries = append(facts.Deliveries, mustJSON(v))
	}
	if len(facts.Deliveries) > 128 {
		return facts, errors.New("harness: extension receipt bound exceeded")
	}
	return facts, rows.Err()
}

// Source identity is conversation/message, because PostgreSQL message
// sequences are per conversation. No global message watermark is invented.
type ExtensionSource struct {
	ID      string `json:"id"`
	RunID   string `json:"run_id"`
	SpaceID string `json:"space_id"`
	ActorID string `json:"actor_id"`
	Hash    string `json:"source_hash"`
}

func (service *Service) extensionRecentFacts(ctx context.Context, run Run, id, variant string) ([]ExtensionRunFacts, error) {
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id FROM fa.runs WHERE conversation_id=$1 ORDER BY seq DESC LIMIT 32`, run.ConversationID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []ExtensionRunFacts{}
	total := 0
	for n := len(ids) - 1; n >= 0; n-- {
		previous, err := service.Store.GetRun(ctx, ids[n])
		if err != nil {
			return nil, err
		}
		if previous.ID == run.ID {
			continue
		}
		var snapshot runSnapshot
		if json.Unmarshal([]byte(previous.ConfigJSON), &snapshot) != nil || snapshot.Capabilities == nil || !contains(snapshot.Capabilities.Extensions, id) || extensionVariant(previous, id, snapshot.Config) != variant {
			continue
		}
		facts, err := NewTaskService(service).ExtensionRunFacts(ctx, run.ConversationID, previous.ID)
		if err != nil {
			return nil, err
		}
		facts.Output = truncateUTF8Bytes(facts.Output, 8192)
		if service.operationsDenied() {
			facts.Output = ""
		}
		if len(result) >= 16 || total+len(mustJSON(facts)) > 192<<10 {
			break
		}
		total += len(mustJSON(facts))
		result = append(result, facts)
	}
	return result, nil
}
