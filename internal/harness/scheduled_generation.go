package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const wakePrefix = "extension/wake/"

func (tasks *TaskService) cancelScheduledForInput(ctx context.Context, chat string) {
	host := tasks.core.extensions
	if host == nil {
		return
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	for token, inv := range host.active {
		if inv.service != nil && inv.service.extensionTrigger != nil && inv.run.ConversationID == chat {
			inv.cancel()
			delete(host.active, token)
		}
	}
}

// An extension trigger is not a user message. Actor identifies whose scoped
// data the rule may read; AuthorizedBy and Kind describe the actual authority.
type ExtensionTrigger struct {
	Kind         string `json:"kind"`
	Extension    string `json:"extension_id"`
	Rule         string `json:"rule_id"`
	Resource     string `json:"resource"`
	Opportunity  string `json:"opportunity_id"`
	Actor        string `json:"actor_id"`
	AuthorizedBy string `json:"authorized_by"`
}
type WakeOpportunity struct {
	TargetInput     string        `json:"target_input"`
	Target          ChannelTarget `json:"target"`
	DeliveryState   string        `json:"delivery_state,omitempty"`
	DeliveryMessage string        `json:"delivery_message,omitempty"`
	NotBefore       string        `json:"not_before"`
	ID              string        `json:"id"`
	Owner           string        `json:"owner"`
	Rule            FollowupRule  `json:"rule"`
	RuleDigest      string        `json:"rule_digest"`
	SourceRefs      []string      `json:"source_refs"`
	State           string        `json:"state"`
	Reason          string        `json:"reason,omitempty"`
	Created         string        `json:"created_at"`
	Expires         string        `json:"expires"`
	Evaluated       string        `json:"evaluated_at,omitempty"`
	RunID           string        `json:"run_id,omitempty"`
	SourceRun       string        `json:"source_run"`
	InputBound      int64         `json:"input_bound"`
	Text            string        `json:"text,omitempty"`
	After           string        `json:"after,omitempty"`
	DeliveryID      string        `json:"delivery_id,omitempty"`
	Preview         bool          `json:"preview"`
}
type wakeEvaluation struct {
	Decision   string   `json:"decision"`
	Text       string   `json:"text"`
	ReasonCode string   `json:"reason_code"`
	Reason     string   `json:"reason"`
	Sources    []string `json:"source_refs"`
}

func (tasks *TaskService) wakeAuthorized(ctx context.Context, o WakeOpportunity, now time.Time) error {
	if err := tasks.wakeCurrent(ctx, o, now); err != nil {
		return err
	}
	source, err := tasks.core.Store.GetRun(ctx, o.SourceRun)
	if err != nil {
		return err
	}
	return tasks.checkTelegramRun(ctx, source)
}

// The outbox already holds the Telegram dispatch/revocation lock and verifies
// the grant at its existing boundary. Do not recursively acquire that lock.
func (tasks *TaskService) wakeCurrent(ctx context.Context, o WakeOpportunity, now time.Time) error {
	cfg := tasks.core.currentConfig()
	if !cfg.Delivery.Enabled || !cfg.Delivery.GenerationEnabled[o.Rule.Extension] || !cfg.Extensions.Enabled[o.Rule.Extension] {
		return ErrPermissionDenied
	}
	match := false
	for _, r := range cfg.Delivery.Rules {
		if r.Enabled && r.Generation && r.ID == o.Rule.ID && contentHash(mustJSON(r)) == o.RuleDigest {
			match = true
		}
	}
	if !match {
		return ErrPermissionDenied
	}
	var currentState string
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT document->>'state' FROM fa.meta WHERE key=$1`, wakePrefix+o.ID).Scan(&currentState); err == nil && (currentState == "cancelled" || currentState == "unknown" || currentState == "failed") {
		return ErrPermissionDenied
	}
	expires, err := time.Parse(time.RFC3339Nano, o.Expires)
	if err != nil || !now.Before(expires) {
		return ErrPermissionDenied
	}
	selection, err := tasks.SelectedCapabilities(ctx, o.Rule.ConversationID)
	if err != nil || !contains(selection.Extensions, o.Rule.Extension) || selection.Variants[o.Rule.Extension] != o.Rule.Resource {
		return ErrPermissionDenied
	}
	var latest int64
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM fa.messages WHERE conversation_id=$1 AND body_json->>'Role'='user'`, o.Rule.ConversationID).Scan(&latest); err != nil {
		return err
	}
	if latest != o.InputBound {
		return ErrInputPending
	}
	var targetActivity struct {
		Key string    `json:"key"`
		At  time.Time `json:"at"`
	}
	var activityRaw []byte
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, scheduledTargetInputKey(o.Target)).Scan(&activityRaw); err == nil {
		if json.Unmarshal(activityRaw, &targetActivity) != nil {
			return ErrStoreFormat
		}
		if targetActivity.Key != o.TargetInput {
			return ErrInputPending
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	source, err := tasks.core.Store.GetRun(ctx, o.SourceRun)
	if err != nil {
		return err
	}
	origin := channelOriginFromJSON(source.ConfigJSON)
	if origin == nil || origin.Source != SpaceSourceTelegram || source.Permission == nil || o.Target != origin.ChannelTarget {
		return ErrPermissionDenied
	}
	chat, err := tasks.core.Store.GetConversation(ctx, o.Rule.ConversationID)
	if err != nil {
		return err
	}
	space, err := tasks.core.Store.GetSpace(ctx, chat.SpaceID)
	if err != nil || space.ChatKind != "private" {
		return ErrPermissionDenied
	}
	if err = tasks.checkChannelRunDestination(ctx, source, origin.ChannelTarget); err != nil {
		return err
	}
	due, _ := time.Parse(time.RFC3339Nano, o.NotBefore)
	if !o.Preview && !now.Before(due) {
		if o.Rule.QuietStart != "" {
			loc, e := time.LoadLocation(o.Rule.Timezone)
			if e != nil {
				return ErrPermissionDenied
			}
			local := now.In(loc)
			from, e := time.Parse("15:04", o.Rule.QuietStart)
			if e != nil {
				return e
			}
			until, e := time.Parse("15:04", o.Rule.QuietEnd)
			if e != nil {
				return e
			}
			minute := local.Hour()*60 + local.Minute()
			a, z := from.Hour()*60+from.Minute(), until.Hour()*60+until.Minute()
			if a < z && minute >= a && minute < z || a > z && (minute >= a || minute < z) {
				return ErrActiveRun
			}
		}
		idle := o.Rule.IdleHours
		if idle == 0 {
			idle = 4
		}
		started, _ := time.Parse(time.RFC3339Nano, source.CreatedAt)
		var activityRaw []byte
		if tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "scheduled/input/"+o.Rule.ConversationID).Scan(&activityRaw) == nil {
			var a struct {
				ID int64     `json:"message_id"`
				At time.Time `json:"at"`
			}
			if json.Unmarshal(activityRaw, &a) == nil && a.ID == latest && !a.At.IsZero() {
				started = a.At
			}
		}
		if targetActivity.At.After(started) {
			started = targetActivity.At
		}
		if now.Sub(started) < time.Duration(idle)*time.Hour {
			return ErrActiveRun
		}
		var unreplied bool
		if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE starts_with(key,'channel/operation/') AND document->'target'=$1::jsonb AND document->>'proactive'='true' AND COALESCE(document->>'extension','')<>'' AND NULLIF(document->>'last_dispatch','')::timestamptz>=$2 AND COALESCE(document->>'wake_id','')<>$3)`, string(mustJSON(origin.ChannelTarget)), started, o.ID).Scan(&unreplied); err != nil {
			return err
		}
		if unreplied {
			return ErrActiveRun
		}
	}
	return nil
}

func (tasks *TaskService) persistWake(ctx context.Context, o WakeOpportunity) error {
	r, err := tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND (document->>'state' IN ('queued','generating','generated') OR document->>'state'='delivery_queued' AND $3='cancelled')`, string(mustJSON(o)), wakePrefix+o.ID, o.State)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrPermissionDenied
	}
	return nil
}
func (tasks *TaskService) WakeRecords(ctx context.Context, chat string) ([]WakeOpportunity, error) {
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE starts_with(key,$1) AND ($2='' OR document#>>'{rule,conversation_id}'=$2) ORDER BY document->>'created_at' DESC,key DESC LIMIT 50`, wakePrefix, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WakeOpportunity{}
	for rows.Next() {
		var raw []byte
		var o WakeOpportunity
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &o) != nil {
			return nil, ErrStoreFormat
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

// ScheduleWake creates one opportunity per stable time slot or matter source.
// It performs no Provider request and cannot block a channel outbox poll.
func (tasks *TaskService) ScheduleWake(ctx context.Context, rule FollowupRule, key string, refs []string, now time.Time, preview bool, due ...time.Time) (WakeOpportunity, bool, error) {
	if len(key) > 256 || key == "" || len(refs) < 1 || len(refs) > 3 {
		return WakeOpportunity{}, false, ErrRequestKey
	}
	for _, ref := range refs {
		if !contains(rule.SourceRefs, ref) {
			return WakeOpportunity{}, false, ErrPermissionDenied
		}
	}
	source, err := scanRun(tasks.core.Store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND config_doc->'extension_trigger' IS NULL AND status='completed' ORDER BY seq DESC LIMIT 1`, rule.ConversationID))
	if err != nil {
		return WakeOpportunity{}, false, err
	}
	notBefore := now
	if len(due) > 0 {
		notBefore = due[0]
	}
	if len(due) > 1 || notBefore.Sub(now) > time.Duration(rule.MaxDelaySeconds)*time.Second {
		return WakeOpportunity{}, false, ErrPermissionDenied
	}
	origin := channelOriginFromJSON(source.ConfigJSON)
	if origin == nil {
		return WakeOpportunity{}, false, ErrPermissionDenied
	}
	o := WakeOpportunity{Target: origin.ChannelTarget, NotBefore: notBefore.UTC().Format(time.RFC3339Nano), ID: contentHash(mustJSON([]any{rule.ID, contentHash(mustJSON(rule)), key})), Rule: rule, RuleDigest: contentHash(mustJSON(rule)), SourceRun: source.ID, Created: now.UTC().Format(time.RFC3339Nano), Expires: notBefore.Add(time.Hour).UTC().Format(time.RFC3339Nano), State: "queued", SourceRefs: refs, Preview: preview}
	var targetInput string
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT COALESCE(document->>'key','') FROM fa.meta WHERE key=$1`, scheduledTargetInputKey(o.Target)).Scan(&targetInput); err == nil {
		o.TargetInput = targetInput
	} else if err != sql.ErrNoRows {
		return o, false, err
	}
	if tasks.core.Store.scheduled != nil {
		o.Owner = tasks.core.Store.scheduled.id
	}
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM fa.messages WHERE conversation_id=$1 AND body_json->>'Role'='user'`, rule.ConversationID).Scan(&o.InputBound); err != nil {
		return o, false, err
	}
	if err = tasks.wakeAuthorized(ctx, o, now); err != nil {
		return o, false, err
	}
	// One global admission lock covers all rules and all extensions. Quotas
	// cannot be reset by changing rule IDs, personas, restarting or skipping.
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return o, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(87001)`); err != nil {
		return o, false, err
	}
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE starts_with(key,$1) AND document->>'state' IN ('queued','generating','generated','delivery_queued')`, wakePrefix).Scan(&total); err != nil {
		return o, false, err
	}
	if total >= 64 {
		return o, false, errExtensionBudget
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE starts_with(key,$1) AND document->>'state' IN ('queued','generating','generated') AND document->'target'=$2::jsonb`, wakePrefix, string(mustJSON(o.Target))).Scan(&pending); err != nil {
		return o, false, err
	}
	if pending > 0 {
		return o, false, nil
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, wakePrefix+o.ID, string(mustJSON(o)))
	if err != nil {
		return o, false, err
	}
	n, _ := r.RowsAffected()
	return o, n == 1, tx.Commit()
}

// reserveWakeEvaluation consumes rolling quotas before dispatch. Unknown and
// missing usage keep their original reservation in the existing Attempt.
func (tasks *TaskService) reserveWakeEvaluation(ctx context.Context, o *WakeOpportunity) error {
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(87001)`); err != nil {
		return err
	}
	var global, target, active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE NULLIF(document->>'evaluated_at','')::timestamptz>=$2::timestamptz),count(*) FILTER(WHERE NULLIF(document->>'evaluated_at','')::timestamptz>=$2::timestamptz AND document->'target'=$3::jsonb),count(*) FILTER(WHERE document->>'state'='generating') FROM fa.meta WHERE starts_with(key,$1)`, wakePrefix, time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano), string(mustJSON(o.Target))).Scan(&global, &target, &active); err != nil {
		return err
	}
	if global >= 8 || target >= 2 || active > 0 {
		return errExtensionBudget
	}
	var previous string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, wakePrefix+o.ID).Scan(&previous); err != nil {
		return err
	}
	var current WakeOpportunity
	if json.Unmarshal([]byte(previous), &current) != nil || current.State != "queued" {
		return ErrRequestKey
	}
	o.State = "generating"
	o.Evaluated = nowText()
	r, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(o)), wakePrefix+o.ID, previous)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrRequestKey
	}
	return tx.Commit()
}

func (tasks *TaskService) EvaluateWake(ctx context.Context, id string) (result RunResult, returnErr error) {
	var raw []byte
	var o WakeOpportunity
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, wakePrefix+id).Scan(&raw); err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &o) != nil {
		return result, ErrStoreFormat
	}
	if o.State == "generated" {
		return result, tasks.queueWake(ctx, &o)
	}
	if o.State != "queued" {
		return RunResult{Duplicate: true}, nil
	}
	if due, e := time.Parse(time.RFC3339Nano, o.NotBefore); e != nil || time.Now().Before(due) {
		return RunResult{Duplicate: true}, nil
	}
	if err := tasks.wakeAuthorized(ctx, o, time.Now()); err != nil {
		o.State = "cancelled"
		o.Reason = safeError(err)
		_ = tasks.persistWake(ctx, o)
		return result, nil
	}
	return tasks.execute(ctx, o.Rule.ConversationID, func(ctx context.Context, core *Service) (out RunResult, err error) {
		source, err := core.Store.GetRun(ctx, o.SourceRun)
		if err != nil {
			return out, err
		}
		core.channel = channelOriginFromJSON(source.ConfigJSON)
		permission := *source.Permission
		permission.Mode = PermissionNone
		core.permission = &permission
		selected, err := tasks.SelectedCapabilities(ctx, o.Rule.ConversationID)
		if err != nil {
			return out, err
		}
		core.capabilities = &selected
		model, err := tasks.SelectedModel(ctx, o.Rule.ConversationID)
		if err != nil || model == "auto" {
			return out, ErrModelCapability
		}
		binding, err := core.newBinding(core.Config, model)
		if err != nil {
			return out, err
		}
		core.bindings = []ModelBinding{binding}
		if core.extensions == nil {
			return out, errExtensionUnavailable
		}
		definition, err := core.extensions.definition(o.Rule.Extension)
		if err != nil || !definition.Manifest.ScheduledGeneration {
			return out, errExtensionUnavailable
		}
		trigger := &ExtensionTrigger{Kind: "scheduled", Extension: o.Rule.Extension, Rule: o.Rule.ID, Resource: o.Rule.Resource, Opportunity: o.ID, Actor: source.Permission.PersonID, AuthorizedBy: "confirmed_delivery_rule"}
		if trigger.Actor == "" {
			trigger.Actor = "owner"
		}
		core.extensionTrigger = trigger
		snapshot, err := core.snapshot()
		if err != nil {
			return out, err
		}
		run, created, err := core.Store.CreateRun(ctx, o.Rule.ConversationID, "wake:"+o.ID, "Extension scheduled evaluation", snapshot)
		if err != nil {
			return out, err
		}
		if !created {
			return RunResult{Run: run, Duplicate: true}, nil
		}
		out = RunResult{Run: run, Duplicate: true}
		o.RunID = run.ID
		defer func() {
			persist, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if o.State == "generating" {
				o.State = "unknown"
				o.Reason = "evaluation interrupted; no automatic re-execution"
			}
			_ = tasks.persistWake(persist, o)
			status := "completed"
			if o.State == "unknown" || o.State == "failed" || o.State == "cancelled" {
				status = "failed"
			}
			_ = core.Store.FinishRunOutput(persist, run, status, o.Reason, o.Text)
			out.Run, _ = core.Store.GetRun(persist, run.ID)
		}()
		if err = tasks.reserveWakeEvaluation(ctx, &o); err != nil {
			o.State = "skipped"
			o.Reason = "evaluation quota unavailable"
			return out, nil
		}
		if err = tasks.wakeAuthorized(ctx, o, time.Now()); err != nil {
			o.State = "cancelled"
			o.Reason = "authorization or input changed"
			return out, nil
		}
		deadline := time.Duration(o.Rule.GenerationDeadlineSeconds) * time.Second
		if deadline == 0 {
			deadline = 30 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()
		client, err := core.extensions.client(callCtx, o.Rule.Extension)
		if err != nil {
			return out, nil
		}
		if err = core.verifyExtensionBinding(callCtx, o.Rule.Extension, client); err != nil {
			o.State = "failed"
			o.Reason = "extension source binding invalid"
			return out, nil
		}
		inv, err := core.extensions.beginInvocation(callCtx, o.Rule.Extension, core, run, definition.Manifest)
		if err != nil {
			return out, nil
		}
		defer core.extensions.endInvocation(inv)
		core.extensions.mu.Lock()
		inv.scheduled = true
		inv.contextAnalysis = true
		inv.supplementalKey = o.Rule.Extension + "/wake/" + o.ID
		core.extensions.mu.Unlock()
		identity, err := core.extensionIdentity(callCtx, run)
		if err != nil {
			return out, nil
		}
		history, err := core.wakeHistory(callCtx, o.Rule.ConversationID)
		if err != nil {
			return out, nil
		}
		d, _ := callCtx.Deadline()
		reply, err := client.rpc.Call(inv.ctx, "wake.evaluate", map[string]any{"invocation": inv.id, "identity": identity, "variant": o.Rule.Resource, "rule": o.Rule, "source_refs": o.SourceRefs, "history": history, "now": nowText(), "deadline": d.UTC().Format(time.RFC3339Nano)})
		if err != nil {
			abandonExtensionCall(client, inv.id, err)
			o.State = "unknown"
			o.Reason = "evaluation did not settle; no send or model retry"
			return out, nil
		}
		var evaluation wakeEvaluation
		if decodeControlJSON(reply, &evaluation) != nil || len(evaluation.Reason) > 1024 || !contains([]string{"relevant", "no_topic", "busy", "resolved", "duplicate", "uncertain"}, evaluation.ReasonCode) || evaluation.Decision != "speak" && evaluation.Decision != "skip" {
			o.State = "failed"
			o.Reason = "invalid decision"
			return out, nil
		}
		if evaluation.Decision == "skip" {
			if evaluation.Text != "" {
				o.State = "failed"
			} else {
				o.State = "skipped"
			}
			o.Reason = evaluation.ReasonCode + ": " + evaluation.Reason
			return out, nil
		}
		if strings.TrimSpace(evaluation.Text) == "" || utf8.RuneCountInString(evaluation.Text) > 600 || len(evaluation.Sources) < 1 || len(evaluation.Sources) > 3 {
			o.State = "failed"
			o.Reason = "invalid bounded text or sources"
			return out, nil
		}
		for _, ref := range evaluation.Sources {
			if !contains(o.SourceRefs, ref) {
				o.State = "failed"
				o.Reason = "ungranted source"
				return out, nil
			}
		}
		if err = tasks.wakeAuthorized(ctx, o, time.Now()); err != nil {
			o.State = "cancelled"
			o.Reason = "late input or revoked authorization"
			return out, nil
		}
		o.State = "generated"
		o.Text = evaluation.Text
		o.After = nowText()
		o.Reason = evaluation.ReasonCode + ": " + evaluation.Reason
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = tasks.persistWake(saveCtx, o)
		saveCancel()
		if err != nil {
			return out, nil
		}
		if o.Preview {
			o.State = "previewed"
			return out, nil
		}
		if err = tasks.queueWake(ctx, &o); err != nil {
			o.State = "failed"
			o.Reason = "delivery not queued: " + safeError(err)
		}
		return out, nil
	})
}

func (core *Service) wakeHistory(ctx context.Context, chat string) ([]ModelMessage, error) {
	rows, err := core.Store.db.QueryContext(ctx, `SELECT body_json FROM fa.messages WHERE conversation_id=$1 AND body_json->>'Public'='true' AND body_json->>'Role' IN ('user','assistant') ORDER BY id DESC LIMIT 16`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []ModelMessage{}
	bytes := 0
	for rows.Next() {
		var raw []byte
		var m ModelMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &m) != nil {
			return nil, ErrStoreFormat
		}
		bytes += len(m.Content)
		if bytes > 8192 {
			break
		}
		history = append([]ModelMessage{{Role: m.Role, Content: m.Content}}, history...)
	}
	return history, rows.Err()
}
func (tasks *TaskService) queueWake(ctx context.Context, o *WakeOpportunity) error {
	if o.Preview {
		return ErrPermissionDenied
	}
	if err := tasks.wakeAuthorized(ctx, *o, time.Now()); err != nil {
		return err
	}
	run, err := tasks.core.Store.GetRun(ctx, o.RunID)
	if err != nil {
		return err
	}
	bridge := tasks.core.Store.channelBridge.Load()
	if bridge == nil {
		return ErrToolDisabled
	}
	candidate := ReplyCandidate{Key: o.ID, RuleID: o.Rule.ID, Text: o.Text, After: o.After, Expires: o.Expires, Timezone: o.Rule.Timezone, Reason: o.Reason}
	op, err := bridge.queueGeneratedCandidate(ctx, run, o.Rule.Extension, candidate, o.ID)
	if err != nil {
		return err
	}
	// Association was committed with the operation. Re-read because an outbox
	// receipt may already have settled; never overwrite it with a stale copy.
	var raw []byte
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, wakePrefix+o.ID).Scan(&raw); err != nil {
		return err
	}
	if json.Unmarshal(raw, o) != nil || o.DeliveryID != op.ID {
		return ErrStoreFormat
	}
	return nil
}

type scheduledOwner struct {
	mu       sync.Mutex
	id       string
	tasks    *TaskService
	ctx      context.Context
	dispatch ChannelDispatch
	cancel   context.CancelFunc
	active   bool
}

func (tasks *TaskService) SetScheduledGenerationOwner(ctx context.Context, dispatch ChannelDispatch) {
	store := tasks.core.Store
	store.scheduledMu.Lock()
	defer store.scheduledMu.Unlock()
	if store.scheduled != nil {
		return
	}
	store.scheduled = &scheduledOwner{id: newID(), tasks: tasks, ctx: ctx, dispatch: dispatch}
	store.scheduled.configure()
}
func (s *scheduledOwner) configure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.tasks.core.currentConfig()
	enabled := false
	for _, r := range cfg.Delivery.Rules {
		enabled = enabled || cfg.Delivery.Enabled && r.Enabled && r.Generation && cfg.Delivery.GenerationEnabled[r.Extension] && cfg.Extensions.Enabled[r.Extension]
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
		s.active = false
	}
	if !enabled || s.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.active = true
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_ = s.tick(ctx, time.Now())
				timer.Reset(time.Minute)
			}
		}
	}()
}
func (s *scheduledOwner) invalidateInterrupted(ctx context.Context) error {
	_, err := s.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('state',CASE WHEN document->>'state'='generating' THEN 'unknown' ELSE 'cancelled' END,'reason','owner changed; no automatic replay'))::text WHERE starts_with(key,$1) AND COALESCE(document->>'owner','')<>$2 AND document->>'state' IN ('queued','generating')`, wakePrefix, s.id)
	return err
}

func (s *scheduledOwner) tick(ctx context.Context, now time.Time) error {
	if _, err := s.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('state','cancelled','reason','opportunity expired'))::text WHERE starts_with(key,$1) AND document->>'state' IN ('queued','generating','generated','delivery_queued') AND (document->>'expires')::timestamptz<=$2`, wakePrefix, now); err != nil {
		return err
	}
	if err := s.invalidateInterrupted(ctx); err != nil {
		return err
	}
	cfg := s.tasks.core.currentConfig()
	for _, r := range cfg.Delivery.Rules {
		if !cfg.Delivery.Enabled || !r.Enabled || !r.Generation || !cfg.Delivery.GenerationEnabled[r.Extension] || !cfg.Extensions.Enabled[r.Extension] {
			continue
		}
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			continue
		}
		local := now.In(loc)
		slots := r.Slots
		if len(slots) == 0 {
			slots = []string{"10:00", "18:00"}
		}
		for _, slot := range slots {
			at, err := time.Parse("15:04", slot)
			if err != nil {
				continue
			}
			due := time.Date(local.Year(), local.Month(), local.Day(), at.Hour(), at.Minute(), 0, 0, loc)
			if due.In(loc).Hour() != at.Hour() || due.In(loc).Minute() != at.Minute() || now.Before(due) || now.Sub(due) > time.Hour {
				continue
			}
			refs := r.SourceRefs
			if len(refs) == 0 {
				continue
			}
			o, created, err := s.tasks.ScheduleWake(ctx, r, local.Format("2006-01-02")+"/"+slot, refs, now, false)
			if err == nil && created && s.dispatch != nil {
				opID := o.ID
				_ = s.dispatch(r.ConversationID, func(c context.Context) (RunResult, error) {
					execCtx, cancel := context.WithCancel(c)
					defer cancel()
					stop := context.AfterFunc(ctx, cancel)
					defer stop()
					return s.tasks.EvaluateWake(execCtx, opID)
				})
			}
		}
	}
	records, err := s.tasks.pendingWakes(ctx)
	if err != nil {
		return err
	}
	for _, o := range records {
		if o.State != "queued" && o.State != "generated" {
			continue
		}
		if err := s.tasks.wakeAuthorized(ctx, o, now); err != nil {
			o.State = "cancelled"
			o.Reason = "opportunity expired or invalid"
			_ = s.tasks.persistWake(ctx, o)
			continue
		}
		if s.dispatch != nil {
			id := o.ID
			_ = s.dispatch(o.Rule.ConversationID, func(c context.Context) (RunResult, error) {
				execCtx, cancel := context.WithCancel(c)
				defer cancel()
				stop := context.AfterFunc(ctx, cancel)
				defer stop()
				return s.tasks.EvaluateWake(execCtx, id)
			})
		}
	}
	return nil
}
