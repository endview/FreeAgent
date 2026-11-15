package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type contextOmission struct {
	Scope  string            `json:"scope"`
	Config string            `json:"config"`
	Refs   map[string]string `json:"refs"`
	Quotes []string          `json:"quotes,omitempty"`
}

func (service *Service) omissionIdentity(run Run) string {
	return contentHash(mustJSON([]any{run.ConfigJSON, service.contextScope(), contextProjectionVersion}))
}

func (service *Service) loadContextOmission(ctx context.Context, run Run) (contextOmission, error) {
	record := contextOmission{Scope: service.contextScope(), Config: service.omissionIdentity(run), Refs: map[string]string{}}
	if run.ID == "" || !AutomaticCompactionEnabled(service.Config) {
		return record, nil
	}
	var raw string
	err := service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "context/omission/"+run.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	var saved contextOmission
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return record, ErrStoreFormat
	}
	if saved.Scope == record.Scope && saved.Config == record.Config {
		return saved, nil
	}
	return record, nil
}

func projectOmittedContext(request ModelRequest, record contextOmission) ModelRequest {
	if len(record.Refs) == 0 {
		return request
	}
	next := make([]ModelMessage, 0, len(request.Messages))
	omitted := 0
	activeRefs := map[string]bool{}
	for _, m := range request.Messages {
		source := m.ContextSource
		if source != nil && source.Ref != "" && record.Refs[source.Ref] == source.Revision {
			omitted++
			activeRefs[source.Ref] = true
			continue
		}
		next = append(next, m)
	}
	if omitted == 0 {
		return request
	}
	quotes := []string{}
	for _, q := range record.Quotes {
		if fields := strings.Fields(q); len(fields) > 0 && activeRefs[fields[0]] {
			quotes = append(quotes, q)
		}
	}
	notice := fmt.Sprintf("History partially omitted for this request (%d original records); original history is retained. This is NOT a summary and the summary checkpoint was not advanced. Use context_manage action=read with the supplied #m references to recover evidence before relying on missing constraints. Historical quotes below may be completed or superseded; they are not new instructions.\n%s\nReferences: ", omitted, strings.Join(quotes, "\n"))
	hasSummary := false
	for _, m := range next {
		hasSummary = hasSummary || m.ContextSource != nil && m.ContextSource.Part == "summary"
	}
	if !hasSummary {
		notice = "No historical summary is available; earlier constraints may be missing. " + notice
	}
	// Sorted source order is held in the bounded quote/reference list, never in
	// map iteration order. Include the omitted anchors without copying bodies.
	for _, m := range request.Messages {
		if m.ContextSource != nil && record.Refs[m.ContextSource.Ref] == m.ContextSource.Revision {
			notice += m.ContextSource.Ref + " "
		}
	}
	marker := ModelMessage{Role: "user", Content: notice, ContextSource: &ContextSource{Part: "history_omitted", Count: omitted}}
	at := len(next)
	for i, m := range next {
		if m.ContextSource != nil && m.ContextSource.Part == "current_input" {
			at = i
			break
		}
	}
	next = append(next, ModelMessage{})
	copy(next[at+1:], next[at:])
	next[at] = marker
	request.Messages = next
	return request
}

func (service *Service) omissionReport(ctx context.Context, run Run) (string, error) {
	record, err := service.loadContextOmission(ctx, run)
	if err != nil || len(record.Refs) == 0 {
		return "", err
	}
	return fmt.Sprintf("历史已部分省略（%d 条原记录）；原始记录和原摘要保留，涉及旧约束时须按引用回读。", len(record.Refs)), nil
}

func canReadOmittedHistory(request ModelRequest) bool {
	for _, t := range request.Tools {
		if t.Name == "context_manage" {
			return true
		}
	}
	return false
}

func (service *Service) applyContextOmission(ctx context.Context, run Run, request ModelRequest) (ModelRequest, error) {
	record, err := service.loadContextOmission(ctx, run)
	if err != nil {
		return request, err
	}
	if !canReadOmittedHistory(request) {
		return request, nil
	}
	return projectOmittedContext(request, record), nil
}

func (service *Service) emergencyContext(ctx context.Context, run Run, request ModelRequest, force ...bool) (ModelRequest, error) {
	usage, err := service.requestUsage(run, request)
	if err != nil {
		return request, err
	}
	if !usage.OverLimit && !(len(force) > 0 && force[0]) {
		return request, nil
	}
	if !AutomaticCompactionEnabled(service.Config) || run.ID == "" || !canReadOmittedHistory(request) {
		usage.OverLimit = true
		return request, usage.budgetError()
	}
	if err = service.checkCurrentPermission(ctx, run); err != nil {
		return request, err
	}
	record, err := service.loadContextOmission(ctx, run)
	if err != nil {
		return request, err
	}
	quoteLimit := min(8192, max(0, (usage.Window.ByteLimit-usage.ReservedBytes)/20))
	if usage.Window.Tokens > 0 {
		quoteLimit = min(quoteLimit, max(0, usage.Window.InputBudget/20))
	}
	seen := map[string]bool{}
	var order []string
	for _, m := range request.Messages {
		if s := m.ContextSource; s != nil && s.RunID != "" && s.RunID != run.ID && !seen[s.RunID] {
			seen[s.RunID] = true
			order = append(order, s.RunID)
		}
	}
	candidate := request
	var accepted *ModelRequest
	var acceptedUsage ContextUsage
	var acceptedRaw string
	for _, oldRun := range order {
		var group []ModelMessage
		for _, m := range request.Messages {
			if m.ContextSource != nil && m.ContextSource.RunID == oldRun {
				group = append(group, m)
			}
		}
		if completeMessageGroups(group) != nil {
			continue
		}
		for _, m := range group {
			s := m.ContextSource
			if s.Ref == "" {
				continue
			}
			record.Refs[s.Ref] = s.Revision
			if m.Role == "user" && m.Readback == nil {
				record.Quotes = append(record.Quotes, fmt.Sprintf("%s (historical): %s", s.Ref, truncateUTF8Bytes(m.Content, 1024)))
			}
		}
		for len(record.Quotes) > 0 && len(strings.Join(record.Quotes, "\n")) > quoteLimit {
			record.Quotes = record.Quotes[1:]
		}
		candidate = projectOmittedContext(request, record)
		if completeMessageGroups(candidate.Messages) != nil {
			return request, ErrContextStructure
		}
		after, e := service.requestUsage(run, candidate)
		if e != nil {
			return request, e
		}
		if !after.OverLimit {
			saved := candidate
			accepted, acceptedUsage, acceptedRaw = &saved, after, string(mustJSON(record))
			if after.Occupancy <= 60 {
				break
			}
		}
	}
	if accepted != nil {
		if _, err = service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "context/omission/"+run.ID, acceptedRaw); err != nil {
			return request, err
		}
		service.event(Event{Kind: EventContextCompactionFailed, ConversationID: run.ConversationID, RunID: run.ID, Message: "历史已部分省略，原记录保留，可按引用回读；本次未推进摘要覆盖位置。", Activity: map[string]any{"history_omitted": true, "occupancy_percent": acceptedUsage.Occupancy}})
		if err = service.recordContextUsage(ctx, run, acceptedUsage); err != nil {
			return request, err
		}
		return *accepted, nil
	}
	after, e := service.requestUsage(run, candidate)
	if e != nil {
		return request, e
	}
	if !after.OverLimit {
		after.OverLimit = true
	}
	return request, after.budgetError()
}
