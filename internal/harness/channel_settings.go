package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	tgpolicy "github.com/endview/freeagent/internal/connectors/telegram"
)

func (tasks *TaskService) channelSettingEligible(ctx context.Context, run Run, kind string, arguments json.RawMessage) error {
	return tasks.channelSettingAgainst(ctx, run, kind, arguments, nil)
}

func (tasks *TaskService) channelSettingRecordEligible(ctx context.Context, run Run, record controlRecord) error {
	var before *CapabilitySelection
	if record.Operation.Kind == ControlSetConversationCapabilities {
		var selection CapabilitySelection
		if json.Unmarshal(record.Before["conversation.capabilities:"+run.ConversationID], &selection) != nil {
			return ErrStoreFormat
		}
		before = &selection
	}
	return tasks.channelSettingAgainst(ctx, run, record.Operation.Kind, record.Arguments, before)
}

func (tasks *TaskService) channelSettingAgainst(ctx context.Context, run Run, kind string, arguments json.RawMessage, original *CapabilitySelection) error {
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil {
		return nil
	}
	unsupported := &controlFlowNotice{"这项设置需要在认证管理页面办理；本次未修改，也没有建立等待中的群设置提案。"}
	if web := tasks.Config().PublicWeb.Effective(); web.Enabled && web.Origin != "" {
		unsupported.text += "\n" + web.Origin
	}
	if origin.Source != SpaceSourceTelegram || run.Permission == nil {
		return unsupported
	}
	space, err := tasks.ChannelSpace(ctx, origin.ChannelTarget)
	if err != nil {
		return err
	}
	// W2 is deliberately private-only. W4 adds group decisions after topic and
	// audience bindings are present; never issue a detailed group preview now.
	if space.ChatKind != "private" {
		return unsupported
	}
	if kind != ControlSetConversationModel && kind != ControlSetConversationCapabilities {
		return unsupported
	}
	if _, err := tasks.CheckTelegramAccess(ctx, origin.ChannelTarget, run.Permission.PersonID); err != nil {
		return err
	}
	people, err := tasks.TelegramConfirmers(ctx, origin.ApplicationID)
	if err != nil {
		return err
	}
	if !slices.Contains(people.Members, run.Permission.PersonID) {
		return &controlFlowNotice{"此私聊尚无设置确认资格，请管理员在渠道设置中维护确认名单；设置未修改。"}
	}
	var args struct {
		Conversation string              `json:"conversation_id"`
		Model        string              `json:"model_id"`
		Capabilities CapabilitySelection `json:"capabilities"`
	}
	if json.Unmarshal(arguments, &args) != nil || args.Conversation != "" && args.Conversation != run.ConversationID {
		return unsupported
	}
	switch kind {
	case ControlSetConversationModel:
		_, ok := tasks.Config().Models[args.Model]
		_, availableErr := tasks.core.newBinding(tasks.Config(), args.Model)
		if !ok || availableErr != nil {
			return &controlFlowNotice{"目标模型未配置或不可用，请到管理页面核对；设置未修改。"}
		}
		return nil
	case ControlSetConversationCapabilities:
		before, err := tasks.SelectedCapabilities(ctx, run.ConversationID)
		if err != nil {
			return err
		}
		next := args.Capabilities.normalize()
		current := before
		if original != nil {
			before = *original
		}
		id, key, value, err := tgpolicy.OneOption(mustJSON(before.normalize()), mustJSON(next))
		if err != nil || !tasks.Config().Extensions.Enabled[id] {
			return unsupported
		}
		if !slices.Contains(current.Extensions, id) || next.validate(tasks.Config()) != nil {
			return &controlFlowNotice{"扩展或相关能力已改变，请到会话能力中核对；本次没有修改设置。"}
		}
		var snapshot runSnapshot
		if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil || snapshot.Capabilities == nil || !slices.Contains(snapshot.Capabilities.Extensions, id) {
			return &controlFlowNotice{"扩展本轮未选中；请先在管理界面明确选择，不能通过换人物自动启用扩展。"}
		}
		if err := tasks.core.validateCapabilitySettings(next); err != nil {
			return err
		}
		if key != "" {
			return nil
		}
		raw, err := tasks.ExtensionState(ctx, id, run.ConversationID, "", run.Permission.PersonID)
		if err != nil {
			return &controlFlowNotice{"暂时无法核实此空间可用的人物或选项，请到扩展管理页面检查；设置未修改。"}
		}
		var state struct {
			Choices []struct {
				ID string `json:"id"`
			} `json:"choices"`
		}
		if json.Unmarshal(raw, &state) != nil {
			return errors.New("invalid extension choices")
		}
		for _, choice := range state.Choices {
			if choice.ID == value {
				return nil
			}
		}
		return &controlFlowNotice{"这个选项在当前空间不可用；设置未修改。"}
	default:
		return unsupported
	}
}
