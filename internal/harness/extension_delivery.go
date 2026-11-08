package harness

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/channelwire"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"time"
)

type ExtensionDeliveryContext struct {
	WakeRuleIDs  []string          `json:"wake_rule_ids,omitempty"`
	Resources    []StickerResource `json:"resources,omitempty"`
	Reactions    []string          `json:"reactions,omitempty"`
	Reply        bool              `json:"reply"`
	RuleIDs      []string          `json:"rule_ids"`
	MaxTextParts int               `json:"max_text_parts"`
	MaxStickers  int               `json:"max_stickers"`
}

func (service *Service) extensionDeliveryContext(run Run, id string) *ExtensionDeliveryContext {
	if !service.replyExpressionsEnabled() {
		return nil
	}
	policy := service.currentConfig().Delivery.effective()
	result := &ExtensionDeliveryContext{Reply: true, RuleIDs: []string{}, MaxTextParts: maxExtensionReplyParts, MaxStickers: policy.StickerLimit}
	if policy.Enabled {
		for _, rule := range policy.Rules {
			if rule.Extension == id && rule.Enabled && rule.Generation && rule.ConversationID == run.ConversationID && policy.GenerationEnabled[id] {
				result.WakeRuleIDs = append(result.WakeRuleIDs, rule.ID)
			}
			if rule.Extension == id && rule.Enabled && !rule.Generation && rule.ConversationID == run.ConversationID {
				result.RuleIDs = append(result.RuleIDs, rule.ID)
			}
		}
	}
	if bridge := service.Store.channelBridge.Load(); bridge != nil && service.channel != nil {
		result.Reactions = bridge.capabilitiesFor(service.channel.ChannelTarget).Reactions
		queryCtx, queryCancel := context.WithTimeout(context.Background(), time.Second)
		defer queryCancel()
		if catalog, err := bridge.stickerCatalog(service.replyAuthority(queryCtx), service.channel.ChannelTarget, run.ConversationID, "", 0, 12); err == nil {
			result.Resources = catalog.Items
		}
	}
	return result
}
func isExtensionDeliveryMethod(method string) bool {
	return method == "delivery.reply" || method == "delivery.propose" || method == "delivery.read" || method == "delivery.withdraw" || method == "delivery.resources"
}
func (host *extensionHost) handleDeliveryRequest(inv *extensionInvocation, method string, params map[string]json.RawMessage) (any, error) {
	if inv == nil || !inv.manifest.Delivery || inv.service == nil || !inv.service.replyExpressionsEnabled() {
		return nil, errExtensionUnavailable
	}
	bridge := inv.service.Store.channelBridge.Load()
	if bridge == nil {
		return nil, errExtensionUnavailable
	}
	for field := range params {
		if field != "invocation" && field != "candidate" && field != "id" {
			return nil, &jsonrpc.Error{Code: -32602, Message: "unexpected delivery field"}
		}
	}
	switch method {
	case "delivery.resources":
		return bridge.stickerCatalog(inv.service.replyAuthority(inv.ctx), inv.service.channel.ChannelTarget, inv.run.ConversationID, "", 0, 12)
	case "delivery.propose":
		var candidate ReplyCandidate
		if decodeControlJSON(params["candidate"], &candidate) != nil {
			return nil, &jsonrpc.Error{Code: -32602, Message: "invalid bounded delivery candidate"}
		}
		op, err := bridge.queueCandidate(inv.ctx, inv.run, inv.extensionID, candidate)
		if err != nil {
			return nil, &jsonrpc.Error{Code: -32020, Message: safeError(err)}
		}
		return candidateReceipt(op), nil
	case "delivery.read", "delivery.withdraw":
		var id string
		if json.Unmarshal(params["id"], &id) != nil || len(id) != 64 || !validChannelPart(id) {
			return nil, errExtensionUnavailable
		}
		connection, ok := bridge.connectionFor(inv.service.channel.ChannelTarget)
		if !ok {
			return nil, errExtensionUnavailable
		}
		key := channelOperationPrefix + connection.ID + "/" + id
		var raw string
		var op channelwire.Operation
		if err := inv.service.Store.db.QueryRowContext(inv.ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
			return nil, errExtensionUnavailable
		}
		if json.Unmarshal([]byte(raw), &op) != nil || op.Extension != inv.extensionID || op.ConversationID != inv.run.ConversationID {
			return nil, errExtensionUnavailable
		}
		if method == "delivery.withdraw" && (op.State == "scheduled" || op.State == "reviewing" || op.State == "pending") && op.Attempts == 0 {
			op.State, op.SuppressedReason = "cancelled", "extension_withdrawn"
			if _, err := inv.service.Store.db.ExecContext(inv.ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), key, raw); err != nil {
				return nil, err
			}
			if err := inv.service.Store.db.QueryRowContext(inv.ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
				return nil, err
			}
			if json.Unmarshal([]byte(raw), &op) != nil {
				return nil, ErrStoreFormat
			}
		}
		return candidateReceipt(op), nil
	}
	return nil, errExtensionUnavailable
}
func candidateReceipt(op channelwire.Operation) map[string]any {
	return map[string]any{"id": op.ID, "state": op.State, "suppressed_reason": op.SuppressedReason, "message_id": op.MessageID, "sent": op.State == "sent", "after_ms": op.NotBeforeMS, "expires_ms": op.ExpiresMS}
}

func (host *extensionHost) reviewDelivery(parent context.Context, core *Service, op channelwire.Operation) bool {
	config := core.currentConfig()
	definition, err := host.definitionFor(op.Extension, config)
	if err != nil || !definition.Manifest.Delivery || !contains(definition.Manifest.Events, "delivery.check") {
		return false
	}
	run, err := core.Store.GetRun(parent, op.RunID)
	if err != nil {
		return false
	}
	service, err := core.forRun(run)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, min(extensionTimeout(definition.Manifest), 5*time.Second))
	defer cancel()
	client, err := host.clientFor(ctx, definition, config)
	if err != nil || !contains(client.registration.Events, "delivery.check") || !contains(client.registration.Provides, "delivery") {
		return false
	}
	inv, err := host.beginInvocation(ctx, op.Extension, service, run, definition.Manifest)
	if err != nil {
		return false
	}
	defer host.endInvocation(inv)
	host.mu.Lock()
	inv.deliveryReview = true
	host.mu.Unlock()
	identity, err := service.extensionIdentity(ctx, run)
	if err != nil {
		return false
	}
	raw, err := client.rpc.Call(ctx, "delivery.check", map[string]any{"invocation": inv.id, "identity": identity, "settings": extensionRunSettings(run, op.Extension, config), "variant": extensionVariant(run, op.Extension, config), "candidate": map[string]any{"id": op.ID, "key": op.CandidateID, "rule_id": op.RuleID, "text": op.Text, "kind": op.Kind, "expires_ms": op.ExpiresMS}})
	if err != nil {
		abandonExtensionCall(client, inv.id, err)
		return false
	}
	var result struct {
		Allow bool `json:"allow"`
	}
	return decodeControlJSON(raw, &result) == nil && result.Allow
}
