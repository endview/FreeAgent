package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

type ContextWindow struct {
	ModelID          string `json:"model_id"`
	Tokens           int    `json:"tokens"`
	Source           string `json:"source"`
	ByteLimit        int    `json:"byte_limit"`
	OutputReserve    int    `json:"output_reserve_tokens"`
	SafetyTokens     int    `json:"safety_tokens"`
	InputBudget      int    `json:"input_budget_tokens"`
	AutomaticPercent int    `json:"automatic_percent"`
}

type ContextPart struct {
	Kind            string `json:"kind"`
	Bytes           int    `json:"bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

type ContextUsage struct {
	Window          ContextWindow `json:"window"`
	RunID           string        `json:"run_id,omitempty"`
	ObservedAt      string        `json:"observed_at"`
	Through         int64         `json:"through"`
	Bytes           int           `json:"bytes"`
	ReservedBytes   int           `json:"reserved_output_bytes"`
	EstimatedTokens int           `json:"estimated_input_tokens"`
	EstimateMethod  string        `json:"estimate_method"`
	Messages        int           `json:"messages"`
	Occupancy       int           `json:"occupancy_percent"`
	Resource        string        `json:"limiting_resource"`
	Parts           []ContextPart `json:"parts"`
	OverLimit       bool          `json:"over_limit"`
	Stale           bool          `json:"newer_history_available"`
}

type ContextBudgetError struct {
	Code  string
	Usage ContextUsage
}

func (err *ContextBudgetError) Error() string {
	return fmt.Sprintf("harness: context budget %s: %d serialized bytes + %d reserved bytes / %d; estimated input %d tokens, input budget %d; reduce the indicated input or compact completed history", err.Code,
		err.Usage.Bytes, err.Usage.ReservedBytes, err.Usage.Window.ByteLimit, err.Usage.EstimatedTokens, err.Usage.Window.InputBudget)
}
func (err *ContextBudgetError) Unwrap() error { return ErrContextLimit }

func autoCompactPercent(config Config) int {
	if config.AutoCompactAt != 0 {
		return config.AutoCompactAt
	}
	return 80
}

func contextWindow(config Config, limits ExecutionLimits, binding ModelBinding, output int) ContextWindow {
	w := ContextWindow{ModelID: binding.ID, Tokens: binding.Config.Capabilities.ContextTokens,
		ByteLimit: limits.MaxContextBytes, OutputReserve: output, AutomaticPercent: autoCompactPercent(config), Source: "local_byte_budget"}
	if w.Tokens > 0 {
		w.Source = binding.Config.Capabilities.ContextSource
		if w.Source == "" {
			w.Source = "manual"
		}
		w.SafetyTokens = max(128, w.Tokens/20)
		w.InputBudget = max(0, w.Tokens-output-w.SafetyTokens)
	}
	return w
}

// This is a labelled, conservative local estimate, never a Provider token
// count. Provider-reported usage remains a separate field in ContextState.
func estimateContextTokens(text string) int {
	letters, other := 0, 0
	for _, r := range text {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r)) {
			letters++
		} else if r < 128 {
			other++
		} else {
			other += 2
		}
	}
	return (letters+2)/3 + other
}

func measureContext(window ContextWindow, request ModelRequest, run Run) ContextUsage {
	u := ContextUsage{Window: window, RunID: run.ID, ObservedAt: nowText(), EstimateMethod: "conservative_text_and_image_estimate", Messages: len(request.Messages), Resource: "bytes"}
	raw := mustJSON(request)
	u.Bytes = len(raw)
	if window.Tokens == 0 {
		u.ReservedBytes = request.MaxOutputTokens * 4
	}
	u.EstimatedTokens = estimateContextTokens(string(raw))
	parts := map[string]ContextPart{}
	add := func(kind, text string, extraBytes, extraTokens int) {
		p := parts[kind]
		p.Kind, p.Bytes, p.EstimatedTokens = kind, p.Bytes+len(text)+extraBytes, p.EstimatedTokens+estimateContextTokens(text)+extraTokens
		parts[kind] = p
	}
	if len(request.Tools) != 0 {
		add("tools", string(mustJSON(request.Tools)), 0, 0)
	}
	for i, message := range request.Messages {
		kind := "history"
		if message.Role == "system" {
			kind = "instructions"
		} else if strings.HasPrefix(message.Content, "Conversation summary (") {
			kind = "summary"
		} else if message.Role == "user" && i == len(request.Messages)-1 {
			kind = "current_input"
		}
		add(kind, string(mustJSON(message)), 0, 0)
		for _, picture := range message.Images {
			encoded := len(picture.Base64)
			imageTokens := max(1024, ((picture.Width+511)/512)*((picture.Height+511)/512)*256)
			u.Bytes += encoded
			u.EstimatedTokens += imageTokens
			add("images", "", encoded, imageTokens)
		}
	}
	for _, name := range []string{"instructions", "tools", "summary", "history", "current_input", "images"} {
		if part, ok := parts[name]; ok {
			u.Parts = append(u.Parts, part)
		}
	}
	ratio := float64(u.Bytes) / float64(max(1, window.ByteLimit-u.ReservedBytes))
	u.OverLimit = u.Bytes+u.ReservedBytes > window.ByteLimit
	if window.Tokens > 0 {
		tokenRatio := float64(u.EstimatedTokens) / float64(max(1, window.InputBudget))
		if tokenRatio > ratio {
			ratio, u.Resource = tokenRatio, "estimated_tokens"
		}
		u.OverLimit = u.OverLimit || u.EstimatedTokens > window.InputBudget
	}
	u.Occupancy = int(math.Ceil(ratio * 100))
	return u
}

func (usage ContextUsage) budgetError() error {
	if !usage.OverLimit {
		return nil
	}
	code := "history_context_full"
	for _, part := range usage.Parts {
		tooLarge := part.Bytes+usage.ReservedBytes > usage.Window.ByteLimit || usage.Window.Tokens > 0 && part.EstimatedTokens > usage.Window.InputBudget
		if !tooLarge {
			continue
		}
		switch part.Kind {
		case "tools", "instructions":
			code = "fixed_context_too_large"
		case "current_input", "images":
			code = "current_input_too_large"
		}
	}
	return &ContextBudgetError{Code: code, Usage: usage}
}

func (service *Service) requestUsage(run Run, request ModelRequest) (ContextUsage, error) {
	limits, binding, err := service.requestLimits()
	if err != nil {
		return ContextUsage{}, err
	}
	return measureContext(contextWindow(service.Config, limits, binding, request.MaxOutputTokens), request, run), nil
}

func (service *Service) recordContextUsage(ctx context.Context, run Run, usage ContextUsage) error {
	if run.ID == "" {
		return nil
	}
	if err := service.Store.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM fa.messages WHERE conversation_id=$1 AND coalesce(((body_json #>> '{Deferred}')::boolean),false)=false AND (id<=$2 OR run_id=$3)`, run.ConversationID, historyBound(run), run.ID).Scan(&usage.Through); err != nil {
		return err
	}
	tx, err := service.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keys := []string{"context/usage/" + run.ConversationID}
	if service.scopedHistory() {
		keys = append(keys, service.contextUsageKey(run.ConversationID))
	}
	for _, key := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(mustJSON(usage))); err != nil {
			return err
		}
	}
	err = tx.Commit()
	if err == nil {
		var activity map[string]any
		_ = json.Unmarshal(mustJSON(usage), &activity)
		service.event(Event{Kind: EventContextUsage, ConversationID: run.ConversationID, RunID: run.ID, Activity: activity})
	}
	return err
}

func (tasks *TaskService) populateContextUsage(ctx context.Context, chat string, state *ContextState) error {
	config := tasks.Config()
	id, err := tasks.SelectedModel(ctx, chat)
	if err != nil {
		return err
	}
	if id == "auto" {
		id = config.DefaultModel
	}
	if model, ok := config.Models[id]; ok && config.requireProvider(model) == nil {
		// Config is the locked snapshot above. Do not copy the live Service
		// while a settings confirmation may be replacing its Config.
		candidate := Service{Store: tasks.core.Store, Keys: tasks.core.Keys, Config: config}
		selected, err := candidate.forSelection(id)
		if err != nil {
			return err
		}
		state.AutomaticPaused, err = selected.compactionSuppressed(ctx, chat)
		if err != nil {
			return err
		}
	}
	if model, ok := config.Models[id]; ok {
		limits := configLimits(config)
		if model.Capabilities.ContextBytes > 0 {
			limits.MaxContextBytes = min(limits.MaxContextBytes, model.Capabilities.ContextBytes)
		}
		output := limits.MaxOutputTokens
		if model.Capabilities.OutputTokens > 0 {
			output = min(output, model.Capabilities.OutputTokens)
		}
		state.Window = contextWindow(config, limits, ModelBinding{ID: id, Config: model}, output)
	}
	var raw string
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, tasks.core.contextUsageKey(chat)).Scan(&raw)
	if err == nil {
		var usage ContextUsage
		if json.Unmarshal([]byte(raw), &usage) != nil {
			return ErrStoreFormat
		}
		var through int64
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM fa.messages WHERE conversation_id=$1`, chat).Scan(&through); err != nil {
			return err
		}
		usage.Stale = through > usage.Through
		state.LastRequest = &usage
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT a.response_json FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id WHERE r.conversation_id=$1 AND a.kind='model' AND ($2='' OR coalesce((r.config_doc #>> '{Permission,mode}'),'')='none' AND $3='public_text' OR (r.config_doc #>> '{Permission,entry}') IN ('telegram','lark','qq') AND $4='channel_public') AND coalesce((a.request_doc #>> '{Purpose}'),'')='' AND CASE WHEN (a.response_doc IS NOT NULL) THEN (a.response_doc #> '{Usage}') END IS NOT NULL ORDER BY r.created_at DESC,a.seq DESC LIMIT 1`, chat, func() string {
		if tasks.core.scopedHistory() {
			return tasks.core.publicSummaryScope()
		}
		return ""
	}(), tasks.core.publicSummaryScope(), tasks.core.publicSummaryScope()).Scan(&raw)
	if err == nil {
		var response ModelResponse
		if json.Unmarshal([]byte(raw), &response) == nil {
			state.LastUsage = response.Usage
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (service *Service) selectionContext(ctx context.Context, run Run) (ModelRequest, error) {
	// Candidate inspection is pure: use already prepared extension material,
	// but never start a hook or a summary call while considering a profile.
	candidate := *service
	candidate.extensionPrepared = true
	summary, _, err := candidate.Store.contextSummary(ctx, run.ConversationID, false)
	if err != nil && !errors.Is(err, ErrSummaryStale) {
		return ModelRequest{}, err
	}
	if errors.Is(err, ErrSummaryStale) {
		summary = nil
	}
	request, err := candidate.buildContextWithSummary(ctx, run, summary)
	if err != nil {
		return request, err
	}
	usage, err := service.requestUsage(run, request)
	if err != nil {
		return request, err
	}
	if err := usage.budgetError(); err != nil {
		var budget *ContextBudgetError
		if service.Config.TaskSummaryBytes > 0 && errors.As(err, &budget) && budget.Code == "history_context_full" {
			plan, planErr := service.planCompactionFor(ctx, run, ContextCommand{Action: "compact"}, true)
			if planErr == nil && plan.source.Count > plan.base.Count && plan.source.Bytes-plan.base.Bytes > 256 {
				hypothetical, buildErr := service.buildContextWithSummary(ctx, run, &ConversationSummary{CoveredThrough: plan.source.Through, Scope: service.publicSummaryScope(), Text: "summary", TextHash: contentHash([]byte("summary"))})
				if buildErr == nil {
					small, e := service.requestUsage(run, hypothetical)
					if e == nil && !small.OverLimit {
						return request, nil
					}
				}
			}
		}
		return ModelRequest{}, err
	}
	return request, nil
}

func (service *Service) contextUsageKey(chat string) string {
	key := "context/usage/" + chat
	if service.scopedHistory() {
		key += "/" + service.publicSummaryScope()
	}
	return key
}
