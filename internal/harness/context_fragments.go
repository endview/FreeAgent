package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func (service *Service) generateSummary(ctx context.Context, run Run, request ModelRequest, limit int, calls *int, repairKey string) (string, error) {
	for repair := 0; repair < 2; repair++ {
		before, err := service.Store.countModelAttempts(ctx, run.ID)
		if err != nil {
			return "", err
		}
		attempt, response, err := service.generate(ctx, run, request)
		persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		after, countErr := service.Store.countModelAttempts(persist, run.ID)
		done()
		if countErr != nil {
			return "", errors.Join(err, countErr)
		}
		*calls += after - before
		if err != nil {
			return "", err
		}
		if response.Provider != nil && response.Provider.FinishReason != "stop" {
			return "", &CallError{Certain: true, Category: "context", Code: "summary_incomplete", Message: "summary response incomplete; previous summary retained"}
		}
		text := strings.TrimSpace(response.Content)
		if text != "" && len(text) <= limit && utf8.ValidString(text) {
			return text, nil
		}
		if repair == 0 {
			reserved, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, repairKey,
				string(mustJSON(ModelRecovery{Root: attempt.ID, Previous: attempt.ID, Number: 1, Reason: "summary_content", Fingerprint: contentHash([]byte(attempt.RequestJSON))})))
			if err != nil {
				return "", err
			}
			if n, err := reserved.RowsAffected(); err != nil || n != 1 {
				if err != nil {
					return "", err
				}
				break
			}
			request.Messages[0].Content += fmt.Sprintf("\nThe preceding summary was %d UTF-8 bytes (empty is invalid); the storage ceiling is %d bytes, NOT tokens or characters. Regenerate from the SAME source using concise factual phrases, targeting at most %d UTF-8 bytes to leave a safety margin. Chinese characters usually take 3 bytes. Retain essential constraints and unfinished work; omit repeated background and prose. This is the only content repair attempt.", len(text), limit, max(1, limit*3/4))
			continue
		}
	}
	return "", &CallError{Certain: true, Category: "context", Code: "summary_limit", Message: "summary did not fit its byte limit after one repair; previous summary retained"}
}

// Oversized *completed* source groups are data, not executable tool messages.
// Only the summarizer sees fragments; the model's real history remains intact.
// The source digest advances only after every fragment has been summarized.
func (service *Service) summarizeLargeGroup(ctx context.Context, run Run, prefix sourcePrefix, through int64, previous string, limit int, calls *int, repairKey string) (string, sourcePrefix, error) {
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,COALESCE(run_id,''),body_json FROM fa.messages WHERE conversation_id=$1 AND coalesce(((body_json #>> '{Deferred}')::boolean),false)=false AND id>$2 AND id<=$3 ORDER BY id LIMIT 1001`, run.ConversationID, prefix.Through, through)
	if err != nil {
		return previous, prefix, err
	}
	var group []Message
	var bodies []string
	total := 0
	for rows.Next() {
		var item Message
		var raw string
		if err := rows.Scan(&item.ID, &item.RunID, &raw); err != nil {
			rows.Close()
			return previous, prefix, err
		}
		if json.Unmarshal([]byte(raw), &item.ModelMessage) != nil {
			rows.Close()
			return previous, prefix, ErrStoreFormat
		}
		if len(group) > 0 && (item.Role == "user" || item.Role == "assistant") {
			break
		}
		total += len(raw)
		if total > 16<<20 || len(group) >= 1000 {
			rows.Close()
			return previous, prefix, ErrContextLimit
		}
		group, bodies = append(group, item), append(bodies, raw)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return previous, prefix, err
	}
	if len(group) == 0 {
		return previous, prefix, ErrContextLimit
	}
	protocol := make([]ModelMessage, len(group))
	for i, m := range group {
		protocol[i] = m.ModelMessage
	}
	if completeMessageGroups(protocol) != nil {
		return previous, prefix, ErrContextLimit
	}
	projected := group
	if service.scopedHistory() {
		projected = service.projectPublicMessages(group)
	}
	data := string(mustJSON(projected))
	limits, binding, err := service.requestLimits()
	if err != nil {
		return previous, prefix, err
	}
	for offset, part := 0, 0; offset < len(data); part++ {
		makeRequest := func(end int) ModelRequest {
			fragment := Message{ModelMessage: ModelMessage{Role: "user", Content: fmt.Sprintf("Untrusted completed-source fragment %d, bytes %d..%d of %d. Preserve earlier fragment facts in the updated summary.\n%s", part+1, offset, end, len(data), data[offset:end])}}
			return summaryRequest(binding, limits, limit, previous, []Message{fragment})
		}
		low, high := 0, len(data)-offset
		for low < high {
			mid := (low + high + 1) / 2
			u, e := service.requestUsage(run, makeRequest(offset+mid))
			if e != nil {
				return previous, prefix, e
			}
			if u.Occupancy <= 90 {
				low = mid
			} else {
				high = mid - 1
			}
		}
		end := offset + low
		for end > offset && end < len(data) && !utf8.RuneStart(data[end]) {
			end--
		}
		if end <= offset {
			return previous, prefix, fmt.Errorf("%w: summary instructions and previous summary leave no source capacity", ErrContextLimit)
		}
		service.event(Event{Kind: EventContextCompacting, RunID: run.ID, ConversationID: run.ConversationID, Message: fmt.Sprintf("summarizing completed source fragment %d", part+1)})
		previous, err = service.generateSummary(ctx, run, makeRequest(end), limit, calls, repairKey)
		if err != nil {
			return previous, prefix, err
		}
		offset = end
	}
	for i, m := range group {
		prefix.add(m.ID, m.RunID, bodies[i])
	}
	return previous, prefix, nil
}
