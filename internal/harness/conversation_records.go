package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// conversationRecords is a read projection, not another persistent transcript.
// Receipts share the conversation sequence but never become model messages in
// fa.messages. Legacy mirror rows remain readable without rewriting user data.
const conversationRecordRows = `(SELECT m.id,m.conversation_id,m.run_id,m.request_key,m.body_json AS raw_json,
 CASE WHEN coalesce(m.body_json->>'kind','')=''
 THEN jsonb_set(m.body_json,'{kind}',to_jsonb((CASE
 WHEN r.config_doc#>>'{extension_trigger,kind}'='scheduled' THEN CASE WHEN m.body_json#>>'{Channel,purpose}'='delivery' THEN 'published_content' ELSE 'generated_candidate' END
 WHEN m.body_json#>>'{Channel,purpose}'='command' THEN 'notification'
 WHEN m.body_json#>>'{Channel,purpose}'='delivery' THEN 'delivery_receipt'
 WHEN m.body_json#>>'{Channel,purpose}'='published' THEN 'published_content'
 WHEN m.body_json->>'Role'='user' THEN 'user_input'
 WHEN m.body_json->>'Role'='assistant' THEN 'model_output'
 WHEN m.body_json->>'Role'='tool' THEN 'tool_result'
 ELSE 'unknown_legacy_record' END)::text),true)
 ELSE m.body_json END AS body_json
 FROM fa.messages m LEFT JOIN fa.runs r ON r.id=coalesce(m.run_id,CASE WHEN starts_with(m.request_key,'channel/delivery/') THEN split_part(m.request_key,'/',3) END)
 UNION ALL SELECT (document->>'sequence')::bigint AS id,document->>'conversation_id' AS conversation_id,
 nullif(document->>'run_id','') AS run_id,document->>'request_key' AS request_key,document->'message' AS raw_json,document->'message' AS body_json
 FROM fa.meta WHERE starts_with(key,'channel/receipt/') AND document->>'version'='1' AND document->'message' IS NOT NULL)`

const conversationRecords = conversationRecordRows + ` AS messages`

type messageKind string

const (
	messageUser      messageKind = "user_input"
	messageAssistant messageKind = "model_output"
	messageTool      messageKind = "tool_result"
	messageDelivery  messageKind = "delivery_receipt"
	messageNotice    messageKind = "notification"
	messagePublished messageKind = "published_content"
	messageCandidate messageKind = "generated_candidate"
)

func storedMessageKind(m ModelMessage) (messageKind, error) {
	if m.Kind != "" {
		switch m.Kind {
		case messageUser, messageAssistant, messageTool, messageDelivery, messageNotice, messagePublished, messageCandidate:
			return m.Kind, nil
		}
		return "", fmt.Errorf("%w: unsupported message kind", ErrStoreFormat)
	}
	if m.Channel != nil {
		switch m.Channel.Purpose {
		case "command":
			return messageNotice, nil
		case "delivery":
			return messageDelivery, nil
		case "published":
			return messagePublished, nil
		}
	}
	switch m.Role {
	case "user":
		return messageUser, nil
	case "assistant":
		return messageAssistant, nil
	case "tool":
		return messageTool, nil
	}
	return "", fmt.Errorf("%w: unsupported message role", ErrStoreFormat)
}

type channelReceiptRecord struct {
	Version        int           `json:"version"`
	Sequence       int64         `json:"sequence,omitempty"`
	ConversationID string        `json:"conversation_id"`
	RunID          string        `json:"run_id,omitempty"`
	RequestKey     string        `json:"request_key"`
	State          string        `json:"state"`
	OccurredAt     string        `json:"occurred_at"`
	SourceMessages []int64       `json:"source_messages,omitempty"`
	Message        *ModelMessage `json:"message,omitempty"`
}

func saveChannelReceipt(ctx context.Context, tx *sql.Tx, key, chat, run, state string, body *ModelMessage) error {
	// Serialize with message allocation, including concurrent retry receipts.
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT last_message_seq FROM fa.conversations WHERE id=$1 FOR UPDATE`, chat).Scan(&last); err != nil {
		return err
	}
	var legacy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.messages WHERE conversation_id=$1 AND request_key=$2)`, chat, key).Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		return nil
	}
	metaKey := "channel/receipt/" + contentHash([]byte(key))
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, metaKey).Scan(&raw)
	if err == nil {
		var previous channelReceiptRecord
		if json.Unmarshal([]byte(raw), &previous) != nil || previous.Version != 1 || previous.ConversationID != chat {
			return ErrStoreFormat
		}
		if previous.Message != nil || body == nil {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	record := channelReceiptRecord{Version: 1, ConversationID: chat, RunID: run, RequestKey: key, State: state, OccurredAt: nowText()}
	if body != nil && body.Content != "" && (state == "sent" || state == "partial") {
		m := *body
		if m.Channel != nil && m.Channel.Purpose == "command" {
			m.Kind = messageNotice
		} else {
			m.Kind = messageDelivery
			if run != "" {
				var generated bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.runs WHERE id=$1 AND config_doc#>>'{extension_trigger,kind}'='scheduled')`, run).Scan(&generated); err != nil {
					return err
				}
				if generated {
					m.Kind = messagePublished
				}
			}
		}
		record.Message = &m
		if err := tx.QueryRowContext(ctx, `UPDATE fa.conversations SET last_message_seq=last_message_seq+1 WHERE id=$1 RETURNING last_message_seq`, chat).Scan(&record.Sequence); err != nil {
			return err
		}
		if run != "" {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM fa.messages WHERE run_id=$1 AND conversation_id=$2 AND body_json->>'Role'='assistant' AND position($3 in body_json->>'Content')>0 ORDER BY id`, run, chat, m.Content)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				record.SourceMessages = append(record.SourceMessages, id)
			}
			if err = errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, metaKey, string(mustJSON(record)))
	return err
}
