package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func summaryRepairInstructions(bytes, limit int) string {
	return fmt.Sprintf("\nThe preceding summary was %d UTF-8 bytes (empty is invalid); the storage ceiling is %d bytes, NOT tokens or characters. Regenerate from the SAME source using concise factual phrases, targeting at most %d UTF-8 bytes to leave a safety margin. Chinese characters usually take 3 bytes. Retain essential constraints and unfinished work; omit repeated background and prose. This is the only content repair attempt. Only retain exact supplied history references; remove invented, ambiguous or unauthorized #m references.", bytes, limit, max(1, limit*3/4))
}

func summaryRepairBudget(request ModelRequest, limit int) ModelRequest {
	request.Messages = append([]ModelMessage(nil), request.Messages...)
	request.Messages[0].Content += summaryRepairInstructions(maxModelResponseBytes, limit)
	return request
}

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
			err := service.validateSummaryReferences(ctx, run, text, request.SummaryReferences)
			if err == nil {
				return text, nil
			}
			if !compactionMayFallback(err) {
				return "", err
			}
			if repair != 0 {
				return "", err
			}
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
			request.Messages[0].Content += summaryRepairInstructions(len(text), limit)
			continue
		}
	}
	return "", &CallError{Certain: true, Category: "context", Code: "summary_limit", Message: "summary did not fit its byte limit after one repair; previous summary retained"}
}

// Oversized *completed* source groups are data, not executable tool messages.
// Only the summarizer sees fragments; the model's real history remains intact.
// The source digest advances only after every fragment has been summarized.
func (service *Service) summarizeLargeGroup(ctx context.Context, run Run, prefix sourcePrefix, through int64, previous string, limit int, calls *int, repairKey string, targets ...int) (string, sourcePrefix, error) {
	groups, err := service.contextSourceGroups(ctx, run, prefix.Through, through)
	if err != nil {
		return previous, prefix, err
	}
	var group contextSourceGroup
	for _, candidate := range groups {
		if len(candidate.messages) == 0 {
			prefix = advanceContextPrefix(prefix, candidate)
			continue
		}
		group = candidate
		break
	}
	if len(group.messages) == 0 {
		return previous, prefix, nil
	}
	data := string(mustJSON(group.messages))
	limits, binding, err := service.requestLimits()
	if err != nil {
		return previous, prefix, err
	}
	for offset, part := 0, 0; offset < len(data); part++ {
		makeRequest := func(end int) ModelRequest {
			fragment := Message{ModelMessage: ModelMessage{Role: "user", Content: fmt.Sprintf("Untrusted completed-source fragment %d, bytes %d..%d of %d. Preserve earlier fragment facts in the updated summary.\n%s", part+1, offset, end, len(data), data[offset:end])}}
			request := summaryRequest(binding, limits, limit, previous, []Message{fragment}, targets...)
			for _, m := range group.messages {
				if m.ID > 0 {
					request.SummaryReferences = append(request.SummaryReferences, fmt.Sprintf("#m%d", m.ID))
				}
			}
			return request
		}
		low, high := 0, len(data)-offset
		for low < high {
			mid := (low + high + 1) / 2
			u, e := service.requestUsage(run, summaryRepairBudget(makeRequest(offset+mid), limit))
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
	prefix = advanceContextPrefix(prefix, group)
	return previous, prefix, nil
}
