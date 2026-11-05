package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *commandSupervisor) liveUncertain() []CommandRecord {
	s.mu.Lock()
	active := make([]*ownedCommand, 0, len(s.active))
	for _, p := range s.active {
		if p != nil {
			active = append(active, p)
		}
	}
	s.mu.Unlock()
	var out []CommandRecord
	for _, p := range active {
		p.mu.Lock()
		r := p.record
		p.mu.Unlock()
		if r.State == "unknown" || r.Reason == "startup_persistence_failed" {
			out = append(out, r)
		}
	}
	return out
}
func (s *commandSupervisor) unknownCount(ctx context.Context) (int, error) {
	var count int
	if err := s.store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'command/process/%' AND (document #>> '{state}')='unknown'`).Scan(&count); err != nil {
		return 0, err
	}
	for _, r := range s.liveUncertain() {
		var exists bool
		if err := s.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1 AND (document #>> '{state}')='unknown')`, commandMetaPrefix+r.ID).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			count++
		}
	}
	return count, nil
}

// The dispatch Attempt is already durable before process creation. If the
// owner dies before its startup projection is saved, derive uncertainty from
// that Attempt rather than losing the command or replaying it.
func (s *commandSupervisor) recoverMissingCommands(ctx context.Context) error {
	rows, err := s.store.db.QueryContext(ctx, `SELECT a.id,a.run_id,r.conversation_id,a.request_json,a.response_json
	FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id WHERE a.kind='tool'
	AND (a.request_doc IS NOT NULL) AND (a.request_doc #>> '{Name}')='command_run'
	AND (((a.request_doc #>> '{Arguments,background}')::boolean)=true OR (a.request_doc #>> '{Arguments,mode}') IN ('background','service'))
	AND (a.status IN ('unknown','dispatched') OR (a.status='finished' AND (a.response_doc IS NOT NULL) AND (a.response_doc #>> '{Receipts,0,command_id}')=a.id))
	AND NOT EXISTS(SELECT 1 FROM fa.meta WHERE key='command/process/'||a.id) LIMIT 201`)
	if err != nil {
		return err
	}
	var recovered []CommandRecord
	for rows.Next() {
		var id, run, chat, request, response string
		if err := rows.Scan(&id, &run, &chat, &request, &response); err != nil {
			rows.Close()
			return err
		}
		var call ToolCall
		if json.Unmarshal([]byte(request), &call) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		if call.Name != "command_run" {
			continue
		}
		var args commandArguments
		if json.Unmarshal(call.Arguments, &args) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		if !args.Background && args.Mode != "service" && args.Mode != "background" {
			continue
		}
		mode := "background"
		if args.Mode == "service" {
			mode = "service"
		}
		receipt := ToolReceipt{Kind: "command", Command: args.Command, CommandID: id}
		var result ToolResult
		if response != "" && json.Unmarshal([]byte(response), &result) == nil {
			for _, r := range result.Receipts {
				if r.Kind == "command" {
					receipt = r
					break
				}
			}
		}
		receipt.Status, receipt.Cleanup = "unknown", "unknown"
		recovered = append(recovered, CommandRecord{ID: id, RunID: run, ConversationID: chat, Mode: mode, Generation: "unrecorded_previous_owner", Revision: 1, State: "unknown", Reason: "missing_start_projection", Receipt: receipt, ObservedAt: nowText(), OutputGap: true})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(recovered) > 200 {
		return ErrContextLimit
	}
	for _, r := range recovered {
		if err := s.save(ctx, r); err != nil {
			return fmt.Errorf("recover command %s: %w", r.ID, err)
		}
	}
	return nil
}
