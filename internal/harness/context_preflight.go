package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

// Validate protocol structure over the entire selected prefix without loading
// every tool body. Summarization then reads bounded complete source groups.
// The lightweight digest is only a suppression identity; the original SHA-256
// source digest is still checked transactionally at commit.
func (service *Service) preflightContextPrefix(ctx context.Context, run Run, after, through int64) (string, error) {
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,coalesce(run_id,''),
 jsonb_build_object('kind',body_json->>'kind','Role',body_json->>'Role',
 'Content',CASE WHEN length(coalesce(body_json->>'Content',''))>0 THEN 'present' ELSE '' END,
 'Public',coalesce((body_json->>'Public')::boolean,false),
 'Channel',CASE WHEN body_json->'Channel' IS NOT NULL THEN jsonb_build_object('purpose',body_json#>>'{Channel,purpose}') ELSE NULL END,
 'ToolCallID',body_json->>'ToolCallID',
 'ToolCalls',coalesce((SELECT jsonb_agg(jsonb_build_object('ID',v->>'ID')) FROM jsonb_array_elements(coalesce(nullif(body_json->'ToolCalls','null'::jsonb),'[]'::jsonb)) v),'[]'::jsonb),
 'ProviderItems',coalesce((SELECT jsonb_agg(jsonb_build_object('type',v->>'type','call_id',v->>'call_id')) FROM jsonb_array_elements(coalesce(nullif(body_json->'ProviderItems','null'::jsonb),'[]'::jsonb)) v WHERE v->>'type'='function_call'),'[]'::jsonb)),
 md5(body_json::text) FROM `+conversationRecords+` WHERE conversation_id=$1 AND id>$2 AND id<=$3 AND coalesce(body_json->>'Deferred','false')='false' ORDER BY id`, run.ConversationID, after, through)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	scope := ""
	if service.scopedHistory() {
		scope = service.publicSummaryScope()
	}
	var group []ModelMessage
	digest := ""
	count := 0
	for rows.Next() {
		var m Message
		var raw, hash string
		if err = rows.Scan(&m.ID, &m.RunID, &raw, &hash); err != nil {
			return "", err
		}
		if json.Unmarshal([]byte(raw), &m.ModelMessage) != nil {
			return "", ErrStoreFormat
		}
		visible, err := contextVisible(m, scope, "", false)
		if err != nil {
			return "", err
		}
		for _, part := range visible {
			if (part.Role == "user" || part.Role == "assistant") && len(group) > 0 {
				if err = completeMessageGroups(group); err != nil {
					return "", fmt.Errorf("%w: %w", ErrContextStructure, err)
				}
				group = nil
			}
			group = append(group, part.ModelMessage)
			digest = contentHash(mustJSON([]any{digest, m.ID, m.RunID, hash}))
		}
		count++
		if count > 65536 {
			return "", &ContextBudgetError{Code: "history_read_bound", Usage: ContextUsage{OverLimit: true}}
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if err = completeMessageGroups(group); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextStructure, err)
	}
	return digest, nil
}
