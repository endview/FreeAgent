package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

type StickerResource struct {
	ID      string                `json:"id"`
	SetName string                `json:"set_name,omitempty"`
	Emoji   string                `json:"emoji,omitempty"`
	Format  string                `json:"format"`
	Width   int                   `json:"width"`
	Height  int                   `json:"height"`
	Preview *channelwire.MediaRef `json:"-"`
}
type storedSticker struct {
	Resource             StickerResource     `json:"resource"`
	Target               ChannelTarget       `json:"target"`
	ConversationID       string              `json:"conversation_id"`
	Sticker              channelwire.Sticker `json:"sticker"`
	Expires              string              `json:"expires"`
	Provenance           string              `json:"provenance,omitempty"`
	CollectionSet        string              `json:"collection_set,omitempty"`
	CollectionGeneration uint64              `json:"collection_generation,omitempty"`
}

func stickerResourceKey(target ChannelTarget, chat, id string) string {
	return "channel/sticker/" + contentHash(mustJSON([]any{target, chat})) + "/" + id
}

func (tasks *TaskService) rememberSticker(ctx context.Context, target ChannelTarget, sticker channelwire.Sticker, chat string) (StickerResource, error) {
	return tasks.rememberStickerFrom(ctx, target, sticker, chat, "received", "", 0)
}

func (tasks *TaskService) rememberStickerFrom(ctx context.Context, target ChannelTarget, sticker channelwire.Sticker, chat, provenance, set string, generation uint64) (StickerResource, error) {
	if target.Source != SpaceSourceTelegram || target.validate() != nil || !validChannelPart(sticker.FileID) || !validChannelPart(sticker.UniqueID) || len(sticker.SetName) > 128 || len(sticker.Emoji) > 128 || sticker.Width < 1 || sticker.Height < 1 || sticker.Width > 4096 || sticker.Height > 4096 || sticker.Format == "" || len(sticker.Format) > 64 || hasControl(sticker.Format) {
		return StickerResource{}, ErrAttachment
	}
	if sticker.Preview != nil && (!validChannelPart(sticker.Preview.ID) || sticker.Preview.Bytes < 0 || sticker.Preview.Bytes > 16<<20 || !sticker.Preview.Image || len(sticker.Preview.PreviewFormat) > 64) {
		return StickerResource{}, ErrAttachment
	}
	if chat == "" {
		space, err := tasks.ChannelSpace(ctx, target)
		if err != nil {
			return StickerResource{}, err
		}
		chat = space.ActiveConversationID
		if chat == "" {
			chat = space.DefaultConversationID
		}
	}
	ref := StickerResource{ID: "sticker-" + contentHash(mustJSON([]any{target.ApplicationID, chat, sticker.UniqueID})), SetName: sticker.SetName, Emoji: sticker.Emoji, Format: sticker.Format, Width: sticker.Width, Height: sticker.Height}
	if provenance != "received" {
		ref.ID = "sticker-" + contentHash(mustJSON([]any{target, chat, sticker.UniqueID, provenance, set, generation}))
	}
	record := storedSticker{Resource: ref, Target: target, ConversationID: chat, Sticker: sticker, Expires: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	record.Provenance, record.CollectionSet, record.CollectionGeneration = provenance, set, generation
	_, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stickerResourceKey(target, chat, ref.ID), string(mustJSON(record)))
	return ref, err
}

func (tasks *TaskService) stickerResource(ctx context.Context, target ChannelTarget, chat, id string) (storedSticker, error) {
	var record storedSticker
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, stickerResourceKey(target, chat, id)).Scan(&raw)
	if err != nil {
		return record, ErrAttachment
	}
	if json.Unmarshal([]byte(raw), &record) != nil {
		return record, ErrStoreFormat
	}
	expiry, err := time.Parse(time.RFC3339Nano, record.Expires)
	if err != nil || !time.Now().Before(expiry) || record.Target != target || record.ConversationID != chat || record.Resource.ID != id {
		return record, ErrPermissionDenied
	}
	if record.Provenance == "collection" {
		sets, err := tasks.stickerSets(ctx, target, chat)
		if err != nil || record.CollectionGeneration == 0 || sets.Generations[record.CollectionSet] != record.CollectionGeneration {
			return record, ErrPermissionDenied
		}
	}
	return record, nil
}

func (b *ChannelBridge) connectionFor(target ChannelTarget) (channelwire.Connection, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.connections {
		if c.config.Source == target.Source && c.config.ApplicationID == target.ApplicationID {
			return c.config, true
		}
	}
	return channelwire.Connection{}, false
}

type StickerCatalog struct {
	Name   string            `json:"name,omitempty"`
	Title  string            `json:"title,omitempty"`
	Total  int               `json:"total"`
	Offset int               `json:"offset"`
	Items  []StickerResource `json:"items"`
}

func (b *ChannelBridge) stickerCatalog(ctx context.Context, target ChannelTarget, chat, set string, offset, limit int) (StickerCatalog, error) {
	return b.stickerCatalogFrom(ctx, target, chat, set, offset, limit, false)
}

func (b *ChannelBridge) stickerCatalogFrom(ctx context.Context, target ChannelTarget, chat, set string, offset, limit int, saved bool) (StickerCatalog, error) {
	result := StickerCatalog{Offset: offset, Items: []StickerResource{}}
	if offset < 0 || offset > 10000 || limit < 1 || limit > 24 {
		return result, ErrAttachment
	}
	c, ok := b.connectionFor(target)
	if !ok {
		return result, ErrPermissionDenied
	}
	sets, err := b.tasks.stickerSets(ctx, target, chat)
	if err != nil {
		return result, err
	}
	if saved && set == "" {
		return result, ErrRequestKey
	}
	if set != "" {
		name, err := b.prepareResource(ctx, target, set)
		if err != nil {
			return result, err
		}
		provenance, generation := "lookup", uint64(0)
		collectionSet := ""
		if saved {
			collectionSet = strings.ToLower(name)
			generation = sets.Generations[collectionSet]
			if generation == 0 {
				return result, ErrPermissionDenied
			}
			provenance = "collection"
		}
		// A read can be repeated safely. Give each explicit lookup fresh platform
		// evidence rather than renewing old file capabilities from an old receipt.
		lookupCtx := context.WithValue(ctx, channelEventKey{}, "sticker-lookup/"+newID())
		op, err := b.perform(lookupCtx, c.ID, channelwire.Operation{Kind: "sticker_set", Target: channelwire.Target(target), ConversationID: chat, Name: name, PageOffset: offset, PageSize: limit, Limit: 1 << 20})
		if err != nil {
			return result, err
		}
		var page channelwire.StickerPage
		if json.Unmarshal(op.Data, &page) != nil || !strings.EqualFold(page.Name, name) || len(page.Title) > 512 || len(page.Stickers) > limit || page.Offset != offset || page.Total < 0 || page.Total > 10000 || len(page.Stickers) > max(0, page.Total-offset) {
			return result, ErrStoreFormat
		}
		if saved {
			current, err := b.tasks.stickerSets(ctx, target, chat)
			if err != nil || current.Generations[collectionSet] != generation {
				return result, ErrPermissionDenied
			}
		}
		result.Name, result.Title, result.Total = page.Name, page.Title, page.Total
		for _, sticker := range page.Stickers {
			if !strings.EqualFold(sticker.SetName, name) {
				return result, ErrStoreFormat
			}
			ref, err := b.tasks.rememberStickerFrom(ctx, target, sticker, chat, provenance, collectionSet, generation)
			if err != nil {
				return result, err
			}
			result.Items = append(result.Items, ref)
		}
		return result, nil
	}
	prefix := "channel/sticker/" + contentHash(mustJSON([]any{target, chat})) + "/"
	// Filter collection capabilities before paging: removed sets cannot leak
	// through the cached recent-resource listing either.
	valid := ` FROM fa.meta WHERE key >= $1 AND key < $2 AND (document->>'expires')::timestamptz>now() AND document->'target'=$3::jsonb AND document->>'conversation_id'=$4 AND (COALESCE(document->>'provenance','')<>'collection' OR ($5::jsonb->>(document->>'collection_set'))::bigint=(document->>'collection_generation')::bigint)`
	if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*)`+valid, prefix, prefix+"\uffff", string(mustJSON(target)), chat, string(mustJSON(sets.Generations))).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT value`+valid+` ORDER BY key LIMIT $6 OFFSET $7`, prefix, prefix+"\uffff", string(mustJSON(target)), chat, string(mustJSON(sets.Generations)), limit, offset)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var record storedSticker
		if err := rows.Scan(&raw); err != nil {
			return result, err
		}
		if json.Unmarshal([]byte(raw), &record) != nil {
			return result, ErrStoreFormat
		}
		result.Items = append(result.Items, record.Resource)
	}
	return result, rows.Err()
}

func (b *ChannelBridge) recordReaction(ctx context.Context, connection string, event channelwire.Event) error {
	r := event.Reaction
	target := ChannelTarget(event.Target)
	if !validTelegramTarget(target) || r == nil || r.Message != event.Message || !validReactionMessage(r.Message) || !validReactionMessage(event.ID) || r.Date <= 0 || len(r.Old) > 32 || len(r.New) > 32 || len(r.Counts) > 128 {
		return ErrRequestKey
	}
	if r.Anonymous && !r.CountSnapshot {
		return nil // Actor-chat events do not provide a user identity or counts.
	}
	if r.CountSnapshot && (!r.Anonymous || event.Person != "" || len(r.Old) != 0 || len(r.New) != 0) || !r.CountSnapshot && len(r.Counts) != 0 {
		return ErrRequestKey
	}
	seen := map[string]bool{}
	for _, count := range r.Counts {
		if count.Value == "" || len(count.Value) > 160 || hasControl(count.Value) || count.Count < 0 || count.Count > 1<<53 || seen[count.Value] {
			return ErrRequestKey
		}
		seen[count.Value] = true
	}
	for _, value := range append(append([]string{}, r.Old...), r.New...) {
		if value == "" || len(value) > 160 || hasControl(value) {
			return ErrRequestKey
		}
	}
	store := b.tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	if !store.telegramBots[target.ApplicationID] {
		return nil
	}
	grant, err := b.tasks.telegramDeliveryGrant(ctx, target, event.Person, r.CountSnapshot)
	if err != nil || grant.Kind != event.Kind {
		return nil // Unadmitted observations are ignored, never routed as input.
	}
	var known bool
	prefix := channelOperationPrefix + connection + "/"
	err = b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.messages m JOIN fa.conversations c ON c.id=m.conversation_id WHERE c.space_id=$1 AND c.workspace=$2 AND body_json #>> '{Channel,source}'=$3 AND body_json #>> '{Channel,application_id}'=$4 AND body_json #>> '{Channel,chat_id}'=$5 AND body_json #>> '{Channel,message_id}'=$6 AND body_json->>'Role'='user') OR EXISTS(SELECT 1 FROM fa.meta o JOIN fa.conversations c ON c.id=o.document->>'conversation_id' WHERE c.space_id=$1 AND c.workspace=$2 AND key >= $7 AND key < $8 AND document->'target'=$9::jsonb AND document->>'message_id'=$6 AND document->>'state'='sent' AND document->>'kind' IN ('text','file','sticker'))`, grant.SpaceID, b.tasks.Config().Workspace, target.Source, target.ApplicationID, target.ChatID, r.Message, prefix, prefix+"\uffff", string(mustJSON(target))).Scan(&known)
	if err != nil || !known {
		return err
	}
	key := "channel/reaction/" + connection + "/" + contentHash(mustJSON([]any{target, r.Message, event.Person}))
	if r.CountSnapshot {
		key = "channel/reaction-count/" + connection + "/" + contentHash(mustJSON([]any{target, r.Message}))
	}
	record := struct {
		Event    string                `json:"event"`
		Target   ChannelTarget         `json:"target"`
		Person   string                `json:"person,omitempty"`
		Reaction *channelwire.Reaction `json:"reaction"`
	}{event.ID, target, event.Person, r}
	// The platform sends absolute snapshots. Replace, never add; update_id
	// orders changes in the same second and makes duplicate/stale events inert.
	_, err = b.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE (fa.meta.document #>> '{reaction,date}')::bigint < $3 OR ((fa.meta.document #>> '{reaction,date}')::bigint=$3 AND (fa.meta.document->>'event')::numeric < $4::numeric)`, key, string(mustJSON(record)), r.Date, event.ID)
	return err
}

type replyExpressionTools struct {
	base             ToolRunner
	service          *Service
	runID, attemptID string
}

type ReactionTarget struct {
	Ref       string `json:"ref"`
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
	Preview   string `json:"preview,omitempty"`
}

func validReactionMessage(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

// A reference names an existing Core fact, never an unverified platform ID.
func (tasks *TaskService) reactionTarget(ctx context.Context, connection string, target ChannelTarget, chat, ref string) (ReactionTarget, error) {
	result := ReactionTarget{Ref: ref}
	_, actual, err := tasks.stickerSpace(ctx, chat)
	if err != nil || actual != target {
		return result, ErrPermissionDenied
	}
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || len(ref) > 160 {
		return result, ErrPermissionDenied
	}
	var raw string
	switch kind {
	case "incoming":
		if !validReactionMessage(id) {
			return result, ErrPermissionDenied
		}
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, id, chat).Scan(&raw); err != nil {
			return result, ErrPermissionDenied
		}
		var input ModelMessage
		if json.Unmarshal([]byte(raw), &input) != nil || input.Role != "user" || !input.Public || input.Channel == nil || input.Channel.ChannelTarget != target || input.Channel.validate() != nil || input.Channel.Purpose == "delivery" || input.Channel.Purpose == "published" {
			return result, ErrPermissionDenied
		}
		result.MessageID, result.Kind = input.Channel.MessageID, "incoming"
		text := []rune(input.Content)
		result.Preview = string(text[:min(len(text), 120)])
	case "outbox":
		if !validChannelPart(id) {
			return result, ErrPermissionDenied
		}
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+connection+"/"+id).Scan(&raw); err != nil {
			return result, ErrPermissionDenied
		}
		var op channelwire.Operation
		if json.Unmarshal([]byte(raw), &op) != nil || op.ID != id || op.Connection != connection || op.Target != channelwire.Target(target) || op.ConversationID != chat || op.State != "sent" || op.Kind != "text" && op.Kind != "file" && op.Kind != "sticker" {
			return result, ErrPermissionDenied
		}
		result.MessageID, result.Kind = op.MessageID, "outbox"
		text := []rune(op.Text)
		result.Preview = string(text[:min(len(text), 120)])
		if op.Kind == "sticker" && op.Sticker != nil {
			result.Preview = "[sticker] " + op.Sticker.Emoji
		}
	default:
		return result, ErrPermissionDenied
	}
	if !validReactionMessage(result.MessageID) {
		return result, ErrPermissionDenied
	}
	return result, nil
}

func (tasks *TaskService) reactionTargets(ctx context.Context, connection string, target ChannelTarget, chat string) ([]ReactionTarget, error) {
	// A small recent page of visible incoming facts and confirmed Bot deliveries.
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT ref FROM (
	 (SELECT 'incoming:'||id::text AS ref, id AS ordering FROM fa.messages WHERE conversation_id=$1 AND body_json #>> '{Channel,source}'=$2 AND body_json #>> '{Channel,application_id}'=$3 AND body_json #>> '{Channel,chat_id}'=$4 AND body_json->>'Role'='user' ORDER BY id DESC LIMIT 12)
	 UNION ALL (SELECT 'outbox:'||(document->>'id'), 0 FROM fa.meta WHERE key >= $5 AND key < $6 AND document->>'conversation_id'=$1 AND document->'target'=$7::jsonb AND document->>'state'='sent' AND document->>'kind' IN ('text','file','sticker') ORDER BY document->>'created_at' DESC,key DESC LIMIT 12)
	 ) facts ORDER BY ordering DESC,ref LIMIT 24`, chat, target.Source, target.ApplicationID, target.ChatID, channelOperationPrefix+connection+"/", channelOperationPrefix+connection+"/\uffff", string(mustJSON(target)))
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	result := []ReactionTarget{}
	for _, ref := range refs {
		item, err := tasks.reactionTarget(ctx, connection, target, chat, ref)
		if err == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (service *Service) replyExpressionsEnabled() bool {
	if service == nil || service.analysisOnly || service.Store == nil || service.channel == nil || service.guard != nil || service.verificationMode {
		return false
	}
	bridge := service.Store.channelBridge.Load()
	if bridge == nil {
		return false
	}
	caps := bridge.capabilitiesFor(service.channel.ChannelTarget)
	return slices.Contains(caps.Actions, "sticker") || slices.Contains(caps.Actions, "reaction")
}
func (tools replyExpressionTools) Definitions() []ToolDefinition {
	definitions := tools.base.Definitions()
	if !tools.service.replyExpressionsEnabled() {
		return definitions
	}
	return append(append([]ToolDefinition(nil), definitions...), ToolDefinition{Name: "reply_expression", Effect: EffectModify, Description: "Current connected channel conversation only: list saved space sets or a bounded sticker page (saved=true derives revocable collection references), send one authorized sticker, list known reaction targets, set/remove the Bot's single reaction on the incoming message by default, inspect deliveries, or propose resume/abandon of an exact delivery group. No arbitrary target, file ID, URL or host/network authority. Delivery acceptance is not a sent receipt. Resume/abandon always require confirmation and never resend failed/UNKNOWN parts. message_ref accepts only incoming:<Core message ID> or outbox:<confirmed operation ID> from targets in this conversation and Bot/chat. An alternate model-selected message always requires confirmation, even with autonomous reactions enabled; the default incoming target follows the autonomous setting. deliveries returns a bounded page; pass the last group cursor to inspect older groups and use that same page cursor when reviewing resume/abandon. Plain Unicode emoji may appear in text. Available ordinary reactions: " + strings.Join(tools.service.Store.channelBridge.Load().capabilitiesFor(tools.service.channel.ChannelTarget).Reactions, " "), InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["sets","targets","list","sticker","reaction","preview","deliveries","resume_delivery","abandon_delivery","schedule"]},"set":{"type":"string"},"saved":{"type":"boolean"},"message_ref":{"type":"string"},"offset":{"type":"integer","minimum":0},"resource":{"type":"string"},"emoji":{"type":"string"},"group":{"type":"string"},"digest":{"type":"string"},"cursor":{"type":"string"},"candidate":{"type":"object","properties":{"key":{"type":"string"},"text":{"type":"string"},"sticker_ref":{"type":"string"},"after":{"type":"string"},"expires":{"type":"string"},"timezone":{"type":"string"},"reason":{"type":"string"}},"required":["key","after","expires","timezone"],"additionalProperties":false}},"required":["action"],"additionalProperties":false}`)})
}
func (tools replyExpressionTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if call.Name != "reply_expression" {
		return tools.base.Prepare(ctx, call)
	}
	if !tools.service.replyExpressionsEnabled() {
		return PreparedTool{}, ErrToolDisabled
	}
	var args struct {
		Action     string          `json:"action"`
		Set        string          `json:"set,omitempty"`
		Saved      bool            `json:"saved,omitempty"`
		MessageRef string          `json:"message_ref,omitempty"`
		Offset     int             `json:"offset,omitempty"`
		Resource   string          `json:"resource,omitempty"`
		Emoji      string          `json:"emoji,omitempty"`
		Group      string          `json:"group,omitempty"`
		Digest     string          `json:"digest,omitempty"`
		Cursor     string          `json:"cursor,omitempty"`
		Candidate  *ReplyCandidate `json:"candidate,omitempty"`
	}
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	service := tools.service
	run, err := service.Store.GetRun(ctx, tools.runID)
	if err != nil {
		return PreparedTool{}, err
	}
	target := service.channel.ChannelTarget
	bridge := service.Store.channelBridge.Load()
	c, ok := bridge.connectionFor(target)
	if !ok {
		return PreparedTool{}, ErrPermissionDenied
	}
	if err := NewTaskService(service).checkTelegramRun(ctx, run); err != nil {
		return PreparedTool{}, err
	}
	prepared := PreparedTool{Effect: EffectRead}
	switch args.Action {
	case "sets", "targets":
		if args.Action == "sets" && !c.ReplyStickers {
			return prepared, ErrToolDisabled
		}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			if err := NewTaskService(service).checkTelegramRun(ctx, run); err != nil {
				return ToolResult{}, err
			}
			if args.Action == "sets" {
				sets, err := NewTaskService(service).stickerSets(ctx, target, run.ConversationID)
				if err != nil {
					return ToolResult{}, err
				}
				return jsonResult(sets)
			}
			items, err := NewTaskService(service).reactionTargets(ctx, c.ID, target, run.ConversationID)
			if err != nil {
				return ToolResult{}, err
			}
			return jsonResult(items)
		}
	case "schedule":
		if args.Candidate == nil {
			return prepared, ErrRequestKey
		}
		op, err := bridge.candidateOperation(ctx, run, "", *args.Candidate, time.Now())
		if err != nil {
			return prepared, err
		}
		prepared.userConfirmation = true
		prepared.Preview = fmt.Sprintf("One-time message to this conversation. Due: %s; expires %s; timezone %s. Content: %s. No command or model will run at send time.", args.Candidate.After, args.Candidate.Expires, args.Candidate.Timezone, args.Candidate.Text)
		prepared.Items = []ApprovalItem{{Kind: "delivery_schedule", Target: run.ConversationID, Hash: contentHash(mustJSON(op))}}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			op, err := bridge.queueCandidate(ctx, run, "", *args.Candidate)
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return jsonResult(candidateReceipt(op))
		}
	case "deliveries":
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			items, err := NewTaskService(service).ChannelDeliveries(ctx, run.ConversationID, args.Cursor)
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return jsonResult(items)
		}
	case "resume_delivery", "abandon_delivery":
		action := "resume"
		if args.Action == "abandon_delivery" {
			action = "abandon"
		}
		items, err := NewTaskService(service).ChannelDeliveries(ctx, run.ConversationID, args.Cursor)
		if err != nil {
			return prepared, err
		}
		found := false
		for _, item := range items {
			if item.ID == args.Group && item.Digest == args.Digest {
				found = true
				break
			}
		}
		if !found {
			return prepared, ErrApprovalChanged
		}
		prepared.userConfirmation = true
		prepared.Preview = "Channel delivery " + action + ": only never-dispatched parts; failed and UNKNOWN attempts remain unchanged and are not resent."
		prepared.Items = []ApprovalItem{{Kind: "channel_delivery", Target: args.Group, Hash: args.Digest}}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			err := NewTaskService(service).ControlDelivery(ctx, run.ConversationID, DeliveryCommand{Group: args.Group, Digest: args.Digest, Action: action})
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return jsonResult(map[string]any{"action": action, "unknown_resent": false})
		}
	case "list":
		if !c.ReplyStickers {
			return prepared, ErrToolDisabled
		}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			if err := NewTaskService(service).checkTelegramRun(ctx, run); err != nil {
				return ToolResult{}, err
			}
			ctx = service.replyAuthority(ctx)
			catalog, err := bridge.stickerCatalogFrom(ctx, target, run.ConversationID, args.Set, args.Offset, 12, args.Saved)
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return jsonResult(catalog)
		}
	case "preview":
		if !c.ReplyStickers {
			return prepared, ErrToolDisabled
		}
		resource, err := NewTaskService(service).stickerResource(ctx, target, run.ConversationID, args.Resource)
		if err != nil {
			return prepared, err
		}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			if err := NewTaskService(service).checkTelegramRun(ctx, run); err != nil {
				return ToolResult{}, err
			}
			if _, err := NewTaskService(service).stickerResource(ctx, target, run.ConversationID, args.Resource); err != nil {
				return ToolResult{}, err
			}
			binding, err := service.binding()
			if err != nil {
				return ToolResult{}, err
			}
			if !binding.Config.Capabilities.Vision.Enabled {
				return ToolResult{Content: "The current model has no declared image capability; only the sticker metadata is available. No visual interpretation was performed."}, nil
			}
			if !service.Config.Attachments.effective().PNG {
				return ToolResult{Content: "PNG previews are disabled by the attachment policy.", IsError: true}, nil
			}
			if resource.Sticker.Preview == nil {
				return ToolResult{Content: "No preview is available for this resource.", IsError: true}, nil
			}
			media := *resource.Sticker.Preview
			op, err := bridge.perform(service.replyAuthority(ctx), c.ID, channelwire.Operation{Kind: "download", Target: channelwire.Target(target), ConversationID: run.ConversationID, Media: &media, Limit: min(service.Config.Attachments.effective().MaxInputBytes, 16<<20)})
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: "sticker preview unavailable; no sticker was sent"}
			}
			ref, err := NewTaskService(service).createAttachment(WithAttachmentIdentity(ctx, "channel:"+service.permission.PersonID), run.ConversationID, "sticker-preview.png", bytes.NewReader(op.Data), "tool_image", run.ID, true)
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return ToolResult{Content: "Selected media preview only; it may represent one frame rather than the complete source.", Attachments: []AttachmentRef{ref}}, nil
		}
	case "sticker", "reaction":
		op := channelwire.Operation{Target: channelwire.Target(target), ConversationID: run.ConversationID, RunID: run.ID}
		if args.Action == "sticker" {
			if !c.ReplyStickers {
				return prepared, ErrToolDisabled
			}
			resource, err := NewTaskService(service).stickerResource(ctx, target, run.ConversationID, args.Resource)
			if err != nil {
				return prepared, err
			}
			op.Kind, op.Sticker = "sticker", &resource.Sticker
		} else {
			if !bridge.validReaction(target, args.Emoji) {
				return prepared, errors.New("harness: unsupported ordinary reaction")
			}
			defaultRef := "incoming:" + strconv.FormatInt(service.channel.InputMessageID, 10)
			ref := args.MessageRef
			if ref == "" {
				ref = defaultRef
			}
			selected, err := NewTaskService(service).reactionTarget(ctx, c.ID, target, run.ConversationID, ref)
			if err != nil {
				return prepared, err
			}
			op.Kind, op.Reply, op.Reaction = "reaction", selected.MessageID, args.Emoji
			prepared.userConfirmation = ref != defaultRef || !c.AutonomousReactions
			prepared.Preview = fmt.Sprintf("Set this Bot's reaction to %q on known message %s (%s), conversation %s, Bot %s / chat %s. Message preview: %q", args.Emoji, ref, op.Reply, run.ConversationID, target.ApplicationID, target.ChatID, selected.Preview)
			prepared.Items = []ApprovalItem{{Kind: "reply_reaction", Target: ref, Hash: contentHash(mustJSON(op))}}
		}
		prepared.Execute = func(ctx context.Context) (ToolResult, error) {
			if err := NewTaskService(service).checkTelegramRun(ctx, run); err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			if args.Action == "reaction" {
				ref := args.MessageRef
				if ref == "" {
					ref = "incoming:" + strconv.FormatInt(service.channel.InputMessageID, 10)
				}
				selected, err := NewTaskService(service).reactionTarget(ctx, c.ID, target, run.ConversationID, ref)
				if err != nil || selected.MessageID != op.Reply {
					return ToolResult{}, &CallError{Certain: true, Message: "reaction target is no longer available"}
				}
			}
			if args.Action == "sticker" {
				if _, err := NewTaskService(service).stickerResource(ctx, target, run.ConversationID, args.Resource); err != nil {
					return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
				}
				reserved, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'{"count":1}') ON CONFLICT(key) DO UPDATE SET value=jsonb_set(fa.meta.document,'{count}',to_jsonb(COALESCE((fa.meta.document->>'count')::int,0)+1),true)::text WHERE COALESCE((fa.meta.document->>'count')::int,0)<$2`, "channel/sticker-reply/"+run.ID, service.Config.Delivery.effective().StickerLimit)
				if err != nil {
					return ToolResult{}, err
				}
				n, _ := reserved.RowsAffected()
				if n == 0 {
					return ToolResult{Content: "One sticker has already been requested for this Run; inspect delivery, do not repeat it.", IsError: true}, nil
				}
			}
			auth := telegramFileAuthority{Target: target, Revision: service.channel.AccessRevision, Person: service.permission.PersonID, LocalOwner: service.permission.PersonID == ""}
			ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, auth)
			ctx = context.WithValue(ctx, channelEventKey{}, "expression/"+run.ID+"/"+tools.attemptID)
			saved, err := bridge.perform(ctx, c.ID, op)
			if err != nil && !isChannelQueued(err) {
				return ToolResult{Content: "Reply expression was not queued: " + safeError(err), IsError: true}, nil
			}
			return jsonResult(map[string]any{"state": saved.State, "operation": saved.ID, "kind": op.Kind, "delivery_confirmed": false})
		}
	default:
		return prepared, errors.New("harness: invalid reply expression action")
	}
	return prepared, nil
}

func replyOnlyDefinitions(definitions []ToolDefinition) bool {
	return len(definitions) == 1 && definitions[0].Name == "reply_expression"
}

func (service *Service) replyAuthority(ctx context.Context) context.Context {
	if service.channel == nil || service.permission == nil {
		return ctx
	}
	return context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: service.channel.ChannelTarget, Person: service.permission.PersonID, Revision: service.channel.AccessRevision, LocalOwner: service.permission.PersonID == ""})
}
