package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

const ControlScheduleDelivery = "delivery.schedule"

func (tasks *TaskService) cancelRevokedCandidates(ctx context.Context) error {
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'proactive'='true' AND coalesce(document->>'extension','')<>'' AND document->>'state' IN ('scheduled','reviewing','pending') AND coalesce((document->>'attempts')::int,0)=0 LIMIT 2048`)
	if err != nil {
		return err
	}
	type entry struct {
		key, raw string
		op       channelwire.Operation
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(e.raw), &e.op) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		entries = append(entries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	config := tasks.Config()
	for _, e := range entries {
		allowed := false
		for _, rule := range config.Delivery.Rules {
			if config.Delivery.Enabled && rule.Enabled && rule.ID == e.op.RuleID && rule.Extension == e.op.Extension && rule.ConversationID == e.op.ConversationID && contentHash(mustJSON(rule)) == e.op.RuleDigest {
				allowed = true
				break
			}
		}
		if !allowed {
			e.op.State, e.op.SuppressedReason = "cancelled", "rule_disabled_or_changed"
			if _, err := tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(e.op)), e.key, e.raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReplyCandidate is a fixed, one-time communication request. It carries no
// executable command, contact discovery or periodic scheduling instruction.
type ReplyCandidate struct {
	Key        string `json:"key"`
	RuleID     string `json:"rule_id,omitempty"`
	Text       string `json:"text,omitempty"`
	StickerRef string `json:"sticker_ref,omitempty"`
	After      string `json:"after"`
	Expires    string `json:"expires"`
	Timezone   string `json:"timezone,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
type scheduleDeliveryArguments struct {
	RunID     string         `json:"run_id"`
	Candidate ReplyCandidate `json:"candidate"`
}

// checkChannelRunDestination verifies the current publication scope against the
// Run's frozen space and recipient. Routing selections within that space do not
// redirect a delivery. Callers retain their existing grant/dispatch locks.
func (tasks *TaskService) checkChannelRunDestination(ctx context.Context, run Run, target ChannelTarget) error {
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || run.Permission == nil || run.Permission.SpaceID == "" || origin.ChannelTarget != target {
		return ErrConversationSpace
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, run.ConversationID)
	if err != nil {
		return err
	}
	if conversation.Workspace != tasks.Config().Workspace || conversation.SpaceID != run.Permission.SpaceID {
		return ErrConversationSpace
	}
	space, err := tasks.core.Store.GetSpace(ctx, conversation.SpaceID)
	if err != nil {
		return err
	}
	current, ok := channelTarget(space)
	if !ok || current != target {
		return ErrConversationSpace
	}
	return nil
}

func (b *ChannelBridge) candidateOperation(ctx context.Context, run Run, extension string, candidate ReplyCandidate, now time.Time) (channelwire.Operation, error) {
	var op channelwire.Operation
	if len(candidate.Key) < 1 || len(candidate.Key) > 128 || hasControl(candidate.Key) || len(candidate.Reason) > 1024 || len(candidate.Text) > 32<<10 || (candidate.Text == "") == (candidate.StickerRef == "") {
		return op, errors.New("harness: a candidate needs one bounded text or sticker and a stable source key")
	}
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || origin.Source != SpaceSourceTelegram || run.Permission == nil {
		return op, ErrPermissionDenied
	}
	if err := b.tasks.checkTelegramRun(ctx, run); err != nil {
		return op, err
	}
	if err := b.tasks.checkChannelRunDestination(ctx, run, origin.ChannelTarget); err != nil {
		return op, err
	}
	connection, ok := b.connectionFor(origin.ChannelTarget)
	if !ok {
		return op, ErrToolDisabled
	}
	after, err := time.Parse(time.RFC3339Nano, candidate.After)
	if err != nil {
		return op, errors.New("harness: use an absolute RFC3339 time with timezone")
	}
	expires, err := time.Parse(time.RFC3339Nano, candidate.Expires)
	if err != nil || !expires.After(after) || !expires.After(now) || expires.Sub(after) > 24*time.Hour || after.Before(now.Add(-5*time.Second)) {
		return op, errors.New("harness: candidate expiry/time is invalid")
	}
	delay := 7 * 24 * time.Hour
	timezone := candidate.Timezone
	ruleHash := ""
	config := b.tasks.Config()
	if extension != "" {
		if !config.Delivery.Enabled || !config.Extensions.Enabled[extension] {
			return op, ErrPermissionDenied
		}
		found := false
		for _, rule := range config.Delivery.Rules {
			if rule.ID == candidate.RuleID && rule.Enabled && rule.Extension == extension && rule.ConversationID == run.ConversationID {
				found = true
				timezone = rule.Timezone
				delay = time.Duration(rule.MaxDelaySeconds) * time.Second
				ruleHash = contentHash(mustJSON(rule))
				if candidate.StickerRef != "" && !rule.AllowSticker {
					return op, ErrPermissionDenied
				}
			}
		}
		if !found {
			return op, ErrPermissionDenied
		}
	} else if candidate.RuleID != "" {
		return op, ErrPermissionDenied
	}
	if after.Sub(now) > delay {
		return op, errors.New("harness: candidate exceeds the authorized scheduling interval")
	}
	if timezone == "" {
		return op, errors.New("harness: explicitly select the recipient timezone")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return op, errors.New("harness: invalid recipient timezone")
	}
	op = channelwire.Operation{Kind: "text", Target: channelwire.Target(origin.ChannelTarget), ConversationID: run.ConversationID, RunID: run.ID, Proactive: true, CandidateID: candidate.Key, Extension: extension, RuleID: candidate.RuleID, RuleDigest: ruleHash, Timezone: timezone, NotBeforeMS: after.UnixMilli(), ExpiresMS: expires.UnixMilli(), Person: run.Permission.PersonID, AccessRevision: origin.AccessRevision, LocalOwner: run.Permission.PersonID == ""}
	op.CandidateDigest = contentHash(mustJSON(candidate))
	if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM fa.messages WHERE conversation_id=$1 AND body_json->>'Role'='user'`, run.ConversationID).Scan(&op.InputBound); err != nil {
		return op, err
	}
	if candidate.StickerRef != "" {
		if !connection.ReplyStickers {
			return op, ErrToolDisabled
		}
		resource, err := b.tasks.stickerResource(ctx, origin.ChannelTarget, run.ConversationID, candidate.StickerRef)
		if err != nil {
			return op, err
		}
		op.Kind, op.Sticker = "sticker", &resource.Sticker
	} else {
		if b.tasks.core.redactSecrets(candidate.Text) != candidate.Text {
			return op, errors.New("harness: candidate contains a credential")
		}
		prepareCtx := context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: origin.ChannelTarget, Person: run.Permission.PersonID, Revision: origin.AccessRevision, LocalOwner: run.Permission.PersonID == ""})
		parts, format, err := b.prepareText(prepareCtx, connection.ID, origin.ChannelTarget, candidate.Text)
		if err != nil || len(parts) != 1 {
			return op, errors.New("harness: a proactive candidate must fit one platform text message")
		}
		op.Text, op.Entities, op.FormatVersion = parts[0].Text, parts[0].Entities, format
	}
	return op, nil
}

func (b *ChannelBridge) queueCandidate(ctx context.Context, run Run, extension string, candidate ReplyCandidate) (channelwire.Operation, error) {
	return b.queueCandidateWithWake(ctx, run, extension, candidate, "")
}
func (b *ChannelBridge) queueGeneratedCandidate(ctx context.Context, run Run, extension string, candidate ReplyCandidate, wake string) (channelwire.Operation, error) {
	return b.queueCandidateWithWake(ctx, run, extension, candidate, wake)
}
func (b *ChannelBridge) queueCandidateWithWake(ctx context.Context, run Run, extension string, candidate ReplyCandidate, wake string) (channelwire.Operation, error) {
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil {
		return channelwire.Operation{}, ErrPermissionDenied
	}
	connection, exists := b.connectionFor(origin.ChannelTarget)
	if !exists {
		return channelwire.Operation{}, ErrToolDisabled
	}
	stableID := contentHash(mustJSON([]any{connection.ID, run.ConversationID, run.ID, extension, candidate.Key}))
	var existing string
	if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+connection.ID+"/"+stableID).Scan(&existing); err == nil {
		var saved channelwire.Operation
		if json.Unmarshal([]byte(existing), &saved) != nil {
			return saved, ErrStoreFormat
		}
		if saved.CandidateDigest != contentHash(mustJSON(candidate)) {
			return saved, ErrRequestKey
		}
		if err := b.tasks.checkChannelRunDestination(ctx, run, origin.ChannelTarget); err != nil {
			return saved, err
		}
		return saved, nil
	}
	op, err := b.candidateOperation(ctx, run, extension, candidate, time.Now())
	if err != nil {
		return op, err
	}
	op.WakeID = wake
	c, ok := b.connectionFor(ChannelTarget(op.Target))
	if !ok {
		return op, ErrToolDisabled
	}
	// Authorization is carried by the original Run, not by a new user/target.
	ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: ChannelTarget(op.Target), Person: op.Person, Revision: op.AccessRevision, LocalOwner: op.LocalOwner})
	ctx = context.WithValue(ctx, channelEventKey{}, "candidate/"+run.ID+"/"+extension+"/"+candidate.Key)
	result, err := b.perform(ctx, c.ID, op)
	if isChannelQueued(err) {
		err = nil
	}
	return result, err
}

// candidateEligibility is checked again immediately before each transport
// claim. It never calls an extension while the outbox mutex is held.
func (b *ChannelBridge) candidateEligibility(ctx context.Context, op channelwire.Operation, now time.Time) (int64, string, error) {
	if !op.Proactive {
		return 0, "", nil
	}
	if op.ExpiresMS <= now.UnixMilli() {
		return 0, "expired", nil
	}
	grant, err := b.tasks.telegramDeliveryGrant(ctx, ChannelTarget(op.Target), op.Person, op.LocalOwner)
	if err != nil || grant.Revision != op.AccessRevision {
		return 0, "authorization_revoked", nil
	}
	run, err := b.tasks.core.Store.GetRun(ctx, op.RunID)
	if err != nil {
		return 0, "", err
	}
	if run.ConversationID != op.ConversationID {
		return 0, "conversation_destination_changed", nil
	}
	if err := b.tasks.checkChannelRunDestination(ctx, run, ChannelTarget(op.Target)); err != nil {
		if errors.Is(err, ErrConversationSpace) {
			return 0, "conversation_destination_changed", nil
		}
		return 0, "", err
	}
	if op.Extension == "" {
		if op.NotBeforeMS > now.UnixMilli() {
			return op.NotBeforeMS, "", nil
		}
		return 0, "", nil
	}
	if op.WakeID != "" {
		var raw []byte
		var wake WakeOpportunity
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, wakePrefix+op.WakeID).Scan(&raw); err != nil {
			return 0, "generation_record_unavailable", nil
		}
		if json.Unmarshal(raw, &wake) != nil || wake.RunID != op.RunID || wake.State != "delivery_queued" {
			return 0, "generation_cancelled", nil
		}
		if err := b.tasks.wakeCurrent(ctx, wake, now); err != nil {
			return 0, "generation_revoked_or_stale", nil
		}
	}
	config := b.tasks.Config()
	if !config.Delivery.Enabled || !config.Extensions.Enabled[op.Extension] {
		return 0, "disabled", nil
	}
	selection, err := b.tasks.SelectedCapabilities(ctx, op.ConversationID)
	if err != nil {
		return 0, "", err
	}
	if !contains(selection.Extensions, op.Extension) {
		return 0, "extension_deselected", nil
	}
	if op.WakeID == "" && (selection.Variants[op.Extension] != extensionVariant(run, op.Extension, config) || contentHash(mustJSON(extensionRunSettings(run, op.Extension, config))) != contentHash(mustJSON(config.Extensions.Settings[op.Extension]))) {
		return 0, "extension_selection_changed", nil
	}
	var rule FollowupRule
	found := false
	for _, r := range config.Delivery.Rules {
		if r.ID == op.RuleID && r.Enabled && r.Extension == op.Extension && r.ConversationID == op.ConversationID && contentHash(mustJSON(r)) == op.RuleDigest {
			rule = r
			found = true
			break
		}
	}
	if !found {
		return 0, "rule_changed_or_revoked", nil
	}
	if rule.CancelOnInput {
		var newest int64
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM fa.messages WHERE conversation_id=$1 AND body_json->>'Role'='user'`, op.ConversationID).Scan(&newest); err != nil {
			return 0, "", err
		}
		if newest > op.InputBound {
			return 0, "new_input", nil
		}
	}
	if op.NotBeforeMS > now.UnixMilli() {
		return op.NotBeforeMS, "", nil
	}
	loc, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		return 0, "invalid_timezone", nil
	}
	local := now.In(loc)
	if rule.QuietStart != "" {
		a, _ := time.Parse("15:04", rule.QuietStart)
		z, _ := time.Parse("15:04", rule.QuietEnd)
		current := local.Hour()*60 + local.Minute()
		start, end := a.Hour()*60+a.Minute(), z.Hour()*60+z.Minute()
		quiet := start < end && current >= start && current < end || start > end && (current >= start || current < end)
		if quiet {
			until := time.Date(local.Year(), local.Month(), local.Day(), z.Hour(), z.Minute(), 0, 0, loc)
			if !until.After(local) {
				until = until.AddDate(0, 0, 1)
			}
			if until.UnixMilli() >= op.ExpiresMS {
				return 0, "quiet_until_expiry", nil
			}
			return until.UnixMilli(), "", nil
		}
	}
	day := now.Add(-24 * time.Hour)
	var count int
	var last int64
	err = b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE (document->>'last_dispatch')::timestamptz >= $3 AND (document->>'last_dispatch')::timestamptz < $4),coalesce(max(extract(epoch FROM (document->>'last_dispatch')::timestamptz)*1000),0)::bigint FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->'target'=$1::jsonb AND document->>'id'<>$2 AND document->>'proactive'='true' AND coalesce(document->>'extension','')<>'' AND coalesce(document->>'last_dispatch','')<>''`, string(mustJSON(op.Target)), op.ID, day, now).Scan(&count, &last)
	if err != nil {
		return 0, "", err
	}
	if count >= rule.DailyMax {
		return 0, "daily_limit", nil
	}
	cool := last + int64(rule.CooldownHours)*3600000
	if last > 0 && cool > now.UnixMilli() {
		if cool >= op.ExpiresMS {
			return 0, "cooldown_until_expiry", nil
		}
		return cool, "", nil
	}
	return 0, "", nil
}

func (b *ChannelBridge) prepareScheduled(ctx context.Context, id string, now time.Time) error {
	prefix := channelOperationPrefix + id + "/"
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key >= $1 AND key < $2 AND document->>'state' IN ('scheduled','reviewing') ORDER BY (document->>'not_before_ms')::bigint LIMIT 32`, prefix, prefix+"\uffff")
	if err != nil {
		return err
	}
	type item struct {
		key, raw string
		op       channelwire.Operation
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.key, &i.raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(i.raw), &i.op) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		items = append(items, i)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	// Optional extension review must not make a single transport poll wait for
	// every candidate's subprocess timeout (32 * 5s). Review at most one due
	// candidate per poll; ordinary cancellation/expiry checks remain bounded.
	reviewed := false
	for _, i := range items {
		op := i.op
		if op.State == "reviewing" && op.ReviewUntilMS > now.UnixMilli() {
			continue
		}
		wait, reason, err := b.candidateEligibility(ctx, op, now)
		if err != nil {
			return err
		}
		if op.State == "reviewing" {
			reason = "interrupted_extension_review"
		}
		if reason != "" {
			op.State, op.SuppressedReason = "cancelled", reason
		} else if wait > now.UnixMilli() {
			op.NotBeforeMS = wait
		} else if op.Extension != "" && op.WakeID == "" {
			if reviewed {
				continue
			}
			reviewed = true
			op.State, op.ReviewUntilMS = "reviewing", now.Add(10*time.Second).UnixMilli()
			reviewRaw := string(mustJSON(op))
			changed, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, reviewRaw, i.key, i.raw)
			if err != nil {
				return err
			}
			n, err := changed.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			allow := b.tasks.core.extensions != nil && b.tasks.core.extensions.reviewDelivery(ctx, b.tasks.core, op)
			if !allow {
				op.State, op.SuppressedReason = "cancelled", "extension_review_unavailable_or_withdrawn"
			} else {
				op.State = "pending"
			}
			wait, reason, err := b.candidateEligibility(ctx, op, time.Now())
			if err != nil {
				return err
			}
			if reason != "" {
				op.State, op.SuppressedReason = "cancelled", reason
			} else if wait > time.Now().UnixMilli() {
				op.State, op.NotBeforeMS = "scheduled", wait
			}
			i.raw = reviewRaw
		} else {
			op.State = "pending"
		}
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw); err != nil {
			return err
		}
	}
	return nil
}

func (service *ControlService) buildScheduledDelivery(ctx context.Context, record *controlRecord) error {
	var args scheduleDeliveryArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	run, err := service.core.Store.GetRun(ctx, args.RunID)
	if err != nil {
		return err
	}
	if record.Operation.ConversationID != "" && record.Operation.ConversationID != run.ConversationID {
		return ErrConversationSpace
	}
	bridge := service.core.Store.channelBridge.Load()
	if bridge == nil {
		return ErrToolDisabled
	}
	op, err := bridge.candidateOperation(ctx, run, "", args.Candidate, time.Now())
	if err != nil {
		return err
	}
	resource := "delivery.schedule:" + contentHash(mustJSON([]string{run.ID, args.Candidate.Key}))
	record.Operation.ResourceIDs = []string{resource}
	record.Operation.ConversationID = run.ConversationID
	record.Before = map[string]json.RawMessage{resource: mustJSON(nil)}
	record.After = map[string]json.RawMessage{resource: mustJSON(args)}
	record.Operation.Preview = fmt.Sprintf("One-time delivery to the original conversation at %s; expires %s; timezone %s. No fresh model call or command runs at delivery time. Content:\n%s\nSticker reference: %s", args.Candidate.After, args.Candidate.Expires, op.Timezone, args.Candidate.Text, args.Candidate.StickerRef)
	return nil
}
func (service *ControlService) applyScheduledDelivery(ctx context.Context, record *controlRecord) error {
	var args scheduleDeliveryArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	run, err := service.core.Store.GetRun(ctx, args.RunID)
	if err != nil {
		return err
	}
	bridge := service.core.Store.channelBridge.Load()
	if bridge == nil {
		return ErrToolDisabled
	}
	result, err := bridge.queueCandidate(ctx, run, "", args.Candidate)
	if err == nil {
		record.ResultData = mustJSON(map[string]any{"id": result.ID, "state": result.State, "sent": false})
	}
	return err
}
