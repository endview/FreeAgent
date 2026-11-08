package harness

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"github.com/endview/freeagent/internal/channelwire"
)

type StickerCommand struct {
	Action     string `json:"action"`
	Set        string `json:"set,omitempty"`
	Saved      bool   `json:"saved,omitempty"`
	MessageRef string `json:"message_ref,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Resource   string `json:"resource,omitempty"`
	Emoji      string `json:"emoji,omitempty"`
	RequestKey string `json:"request_key,omitempty"`
}

func (tasks *TaskService) ChannelStickerCommand(ctx context.Context, chat string, command StickerCommand) (any, error) {
	space, target, err := tasks.stickerSpace(ctx, chat)
	if err != nil {
		return nil, err
	}
	bridge := tasks.core.Store.channelBridge.Load()
	if bridge == nil {
		return nil, ErrToolDisabled
	}
	connection, ok := bridge.connectionFor(target)
	if !ok {
		return nil, ErrToolDisabled
	}
	grant, err := tasks.telegramDeliveryGrant(ctx, target, "", true)
	if err != nil {
		return nil, err
	}
	if grant.SpaceID != space.ID {
		return nil, ErrPermissionDenied
	}
	ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: target, Revision: grant.Revision, LocalOwner: true})
	switch command.Action {
	case "sets":
		return tasks.stickerSets(ctx, target, chat)
	case "targets":
		return tasks.reactionTargets(ctx, connection.ID, target, chat)
	case "list":
		return bridge.stickerCatalogFrom(ctx, target, chat, command.Set, command.Offset, 24, command.Saved)
	case "preview":
		resource, err := tasks.stickerResource(ctx, target, chat, command.Resource)
		if err != nil {
			return nil, err
		}
		if resource.Sticker.Preview == nil {
			return nil, errors.New("harness: no preview is available for this resource")
		}
		media := *resource.Sticker.Preview
		op, err := bridge.perform(ctx, connection.ID, channelwire.Operation{Kind: "download", Target: channelwire.Target(target), ConversationID: chat, Media: &media, Limit: min(tasks.Config().Attachments.effective().MaxInputBytes, 16<<20)})
		if err != nil {
			return nil, err
		}
		return tasks.UploadAttachmentRequest(ctx, chat, "sticker-preview.png", "sticker-preview/"+command.Resource, bytes.NewReader(op.Data))
	case "send", "reaction":
		if len(command.RequestKey) < 1 || len(command.RequestKey) > 128 {
			return nil, ErrRequestKey
		}
		var run Run
		if command.Action == "send" || command.MessageRef == "" {
			run, err = tasks.Latest(ctx, chat)
			if err != nil {
				return nil, err
			}
		}
		op := channelwire.Operation{Kind: "sticker", Target: channelwire.Target(target), ConversationID: chat, RunID: run.ID, Group: "selection/" + contentHash(mustJSON([]string{chat, command.RequestKey})), Sequence: 1, Total: 1}
		if command.Action == "send" {
			resource, err := tasks.stickerResource(ctx, target, chat, command.Resource)
			if err != nil {
				return nil, err
			}
			op.Sticker = &resource.Sticker
		} else {
			if !bridge.validReaction(target, command.Emoji) {
				return nil, ErrRequestKey
			}
			ref := command.MessageRef
			if ref == "" {
				origin := channelOriginFromJSON(run.ConfigJSON)
				if origin == nil || origin.ChannelTarget != target || origin.InputMessageID < 1 {
					return nil, ErrPermissionDenied
				}
				ref = "incoming:" + strconv.FormatInt(origin.InputMessageID, 10)
			}
			selected, err := tasks.reactionTarget(ctx, connection.ID, target, chat, ref)
			if err != nil {
				return nil, err
			}
			op.Kind, op.Reply, op.Reaction = "reaction", selected.MessageID, command.Emoji
		}
		ctx = context.WithValue(ctx, channelEventKey{}, "selection/"+chat+"/"+command.RequestKey)
		op, err = bridge.perform(ctx, connection.ID, op)
		if isChannelQueued(err) {
			err = nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": op.ID, "state": op.State, "sent": false}, nil
	}
	return nil, ErrRequestKey
}
