package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const ControlSetStickerSets = "telegram.set_sticker_sets"
const maxSpaceStickerSets = 32

// Only set names are durable. Bot file capabilities are resolved afresh in a
// conversation and expire after 24h, including capabilities from saved sets.
type StickerSets struct {
	SpaceID     string            `json:"space_id"`
	Target      ChannelTarget     `json:"target"`
	Revision    uint64            `json:"revision"`
	Sets        []string          `json:"sets"`
	Generations map[string]uint64 `json:"generations,omitempty"`
}

type stickerSetsArguments struct {
	ConversationID   string   `json:"conversation_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	Sets             []string `json:"sets"`
}

func stickerSetsKey(space string) string { return "channel/sticker-sets/" + space }

func (tasks *TaskService) stickerSpace(ctx context.Context, chat string) (Space, ChannelTarget, error) {
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return Space{}, ChannelTarget{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return Space{}, ChannelTarget{}, ErrConversationSpace
	}
	space, err := tasks.core.Store.GetSpace(ctx, conversation.SpaceID)
	if err != nil {
		return space, ChannelTarget{}, err
	}
	target, ok := channelTarget(space)
	if !ok || !validTelegramTarget(target) {
		return space, target, ErrInvalidSpace
	}
	bridge := tasks.core.Store.channelBridge.Load()
	if bridge == nil {
		return space, target, ErrToolDisabled
	}
	if _, ok := bridge.connectionFor(target); !ok {
		return space, target, ErrPermissionDenied
	}
	return space, target, nil
}

func readStickerSets(ctx context.Context, q metaReader, space string, target ChannelTarget) (StickerSets, error) {
	result := StickerSets{SpaceID: space, Target: target, Sets: []string{}, Generations: map[string]uint64{}}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, stickerSetsKey(space)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.SpaceID != space || result.Target != target || result.Sets == nil || len(result.Sets) > maxSpaceStickerSets || len(result.Generations) != len(result.Sets) {
		return result, ErrStoreFormat
	}
	for i, name := range result.Sets {
		if !validPreparedResource(name) || result.Generations[name] == 0 || result.Generations[name] > result.Revision || i > 0 && result.Sets[i-1] >= name {
			return result, ErrStoreFormat
		}
	}
	return result, nil
}

func (tasks *TaskService) stickerSets(ctx context.Context, target ChannelTarget, chat string) (StickerSets, error) {
	space, actual, err := tasks.stickerSpace(ctx, chat)
	if err != nil {
		return StickerSets{}, err
	}
	if actual != target {
		return StickerSets{}, ErrPermissionDenied
	}
	return readStickerSets(ctx, tasks.core.Store.db, space.ID, target)
}

func (service *ControlService) buildStickerSets(ctx context.Context, record *controlRecord) error {
	var args stickerSetsArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if args.ConversationID == "" {
		args.ConversationID = record.Operation.ConversationID
	}
	if record.Operation.ConversationID != "" && record.Operation.ConversationID != args.ConversationID {
		return ErrConversationSpace
	}
	if args.Sets == nil || len(args.Sets) > maxSpaceStickerSets {
		return fmt.Errorf("harness: supply an explicit list of at most %d sticker sets", maxSpaceStickerSets)
	}
	tasks := NewTaskService(service.core)
	space, target, err := tasks.stickerSpace(ctx, args.ConversationID)
	if err != nil {
		return err
	}
	grant, err := tasks.telegramDeliveryGrant(ctx, target, "", true)
	if err != nil || grant.SpaceID != space.ID {
		return ErrPermissionDenied
	}
	for i, raw := range args.Sets {
		if !validPreparedResource(raw) {
			return ErrRequestKey
		}
		args.Sets[i] = raw
	}
	slices.Sort(args.Sets)
	args.Sets = slices.Compact(args.Sets)
	before, err := readStickerSets(ctx, service.core.Store.db, space.ID, target)
	if err != nil {
		return err
	}
	if before.Revision != args.ExpectedRevision {
		return ErrControlStaleRevision
	}
	after := StickerSets{SpaceID: space.ID, Target: target, Revision: before.Revision + 1, Sets: args.Sets, Generations: map[string]uint64{}}
	for _, name := range after.Sets {
		after.Generations[name] = before.Generations[name]
		if after.Generations[name] == 0 {
			after.Generations[name] = after.Revision
		}
	}
	resource := "telegram.sticker_sets:" + space.ID
	record.Operation.ConversationID = args.ConversationID
	record.Operation.ResourceIDs = []string{resource}
	record.Operation.RevertClass = "none"
	record.Before = map[string]json.RawMessage{resource: mustJSON(before), "access": mustJSON(grant.Revision)}
	record.After = map[string]json.RawMessage{resource: mustJSON(after)}
	record.Arguments = mustJSON(args)
	record.Operation.Preview = fmt.Sprintf("Persist available sticker sets in space %s (%s), Telegram Bot %s / chat %s only. Revision %d → %d. Exact names: %s → %s. Maximum %d sets; removed collection references become invalid. Received and explicit lookup references retain their independent 24h scope.", space.Title, space.ID, target.ApplicationID, target.ChatID, before.Revision, after.Revision, string(mustJSON(before.Sets)), string(mustJSON(after.Sets)), maxSpaceStickerSets)
	return nil
}

func (service *ControlService) applyStickerSets(ctx context.Context, record *controlRecord) error {
	var args stickerSetsArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	tasks := NewTaskService(service.core)
	space, target, err := tasks.stickerSpace(ctx, args.ConversationID)
	if err != nil {
		return err
	}
	store := service.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	grant, err := tasks.telegramDeliveryGrant(ctx, target, "", true)
	if err != nil || grant.SpaceID != space.ID || string(mustJSON(grant.Revision)) != string(record.Before["access"]) || !store.telegramBots[target.ApplicationID] {
		return ErrPermissionRevoked
	}
	resource := "telegram.sticker_sets:" + space.ID
	var after StickerSets
	if len(record.Operation.ResourceIDs) != 1 || record.Operation.ResourceIDs[0] != resource || json.Unmarshal(record.After[resource], &after) != nil || after.Target != target || after.SpaceID != space.ID {
		return ErrControlStaleRevision
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, stickerSetsKey(space.ID)); err != nil {
		return err
	}
	before, err := readStickerSets(ctx, tx, space.ID, target)
	if err != nil {
		return err
	}
	if string(mustJSON(before)) != string(record.Before[resource]) || before.Revision != args.ExpectedRevision {
		return ErrControlStaleRevision
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stickerSetsKey(space.ID), string(mustJSON(after))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(ErrUnknownResult, err)
	}
	record.ResultData = mustJSON(after)
	return nil
}
