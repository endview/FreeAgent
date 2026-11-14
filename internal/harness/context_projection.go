package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrContextStructure = errors.New("harness: context_structure_invalid")

type contextSourceRecord struct {
	Message
	raw string
}
type contextSourceGroup struct {
	records  []contextSourceRecord
	messages []Message
}

func contextVisible(message Message, scope, activeRun string, execution bool) ([]Message, error) {
	kind, err := storedMessageKind(message.ModelMessage)
	if err != nil {
		return nil, err
	}
	if kind == messageNotice {
		return nil, nil
	}
	if kind == messageCandidate && !(execution && activeRun != "" && message.RunID == activeRun) {
		return nil, nil
	}
	if kind == messageDelivery && execution && activeRun != "" && message.RunID == activeRun {
		return nil, nil
	}
	if scope == "" {
		if kind == messageDelivery {
			return nil, nil
		}
		return []Message{message}, nil
	}
	if execution && activeRun != "" && message.RunID == activeRun {
		return []Message{message}, nil
	}
	if scope == "channel_public" && (!message.Public || message.Channel != nil && message.Channel.Purpose == "command") {
		return nil, nil
	}
	return publicMessages([]Message{message}), nil
}

func projectContextMessages(messages []Message, scope, activeRun string, execution bool) ([]Message, error) {
	var projected []Message
	var postponed []Message
	var protocol []ModelMessage
	for _, message := range messages {
		if message.Deferred {
			continue
		}
		part, err := contextVisible(message, scope, activeRun, execution)
		if err != nil {
			return nil, err
		}
		for _, p := range part {
			kind, _ := storedMessageKind(p.ModelMessage)
			if len(protocol) > 0 && completeMessageGroups(protocol) != nil && (kind == messageDelivery || kind == messagePublished) {
				postponed = append(postponed, p)
				continue
			}
			projected = append(projected, p)
			protocol = append(protocol, p.ModelMessage)
			if completeMessageGroups(protocol) == nil {
				projected = append(projected, postponed...)
				postponed = nil
				protocol = nil
			}
		}
	}
	if len(postponed) > 0 {
		return nil, ErrContextStructure
	}
	return projected, nil
}

// Source integrity advances over original records, while only the authorized
// semantic projection reaches a summarizer. Transport records cannot split a
// private tool group or contribute private output to a public summary.
func (service *Service) contextSourceGroups(ctx context.Context, run Run, after, through int64) ([]contextSourceGroup, error) {
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,coalesce(run_id,''),raw_json,body_json FROM `+conversationRecords+` WHERE conversation_id=$1 AND id>$2 AND id<=$3 AND coalesce((body_json->>'Deferred')::boolean,false)=false ORDER BY id`, run.ConversationID, after, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scope := ""
	if service.scopedHistory() {
		scope = service.publicSummaryScope()
	}
	var groups []contextSourceGroup
	group := contextSourceGroup{}
	total, count := 0, 0
	flush := func() error {
		if len(group.records) == 0 {
			return nil
		}
		protocol := make([]ModelMessage, len(group.messages))
		for i, m := range group.messages {
			protocol[i] = m.ModelMessage
		}
		if err := completeMessageGroups(protocol); err != nil {
			return fmt.Errorf("%w: %w", ErrContextStructure, err)
		}
		groups = append(groups, group)
		group = contextSourceGroup{}
		return nil
	}
	for rows.Next() {
		var item contextSourceRecord
		var projectedRaw string
		if err = rows.Scan(&item.ID, &item.RunID, &item.raw, &projectedRaw); err != nil {
			return nil, err
		}
		total += len(item.raw)
		count++
		if total > 32<<20 || count > 65536 {
			return nil, &ContextBudgetError{Code: "history_read_bound", Usage: ContextUsage{Bytes: total, OverLimit: true, Window: ContextWindow{ByteLimit: 32 << 20}}}
		}
		if json.Unmarshal([]byte(projectedRaw), &item.ModelMessage) != nil {
			return nil, ErrStoreFormat
		}
		visible, e := contextVisible(item.Message, scope, "", false)
		if e != nil {
			return nil, e
		}
		if len(visible) > 0 && (item.Role == "assistant" || item.Role == "user") && len(group.messages) > 0 {
			if e = flush(); e != nil {
				return nil, e
			}
			if total > 2<<20 || count > 1000 {
				return groups, nil
			}
		}
		group.records = append(group.records, item)
		group.messages = append(group.messages, visible...)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = flush(); err != nil {
		return nil, err
	}
	return groups, nil
}

func advanceContextPrefix(prefix sourcePrefix, group contextSourceGroup) sourcePrefix {
	for _, r := range group.records {
		prefix.add(r.ID, r.RunID, r.raw)
	}
	return prefix
}

// The same authorization projection is used by raw-history tools and model
// assembly; caller code never selects a more privileged scope from an ID.
func readConversationRecords(ctx context.Context, reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, chat string) ([]Message, error) {
	rows, err := reader.QueryContext(ctx, `SELECT id,coalesce(run_id,''),body_json FROM `+conversationRecords+` WHERE conversation_id=$1 ORDER BY id`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var m Message
		var raw string
		if err = rows.Scan(&m.ID, &m.RunID, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &m.ModelMessage) != nil {
			return nil, ErrStoreFormat
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
