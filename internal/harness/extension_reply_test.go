package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const replyFixtureText = "First paragraph.\n\nSecond paragraph."
const replyFixtureSchema = `{"type":"object","properties":{"reply_mode":{"type":"string","title":"回复方式","oneOf":[{"const":"single","title":"完整一条"},{"const":"paragraphs","title":"自然分段"}]}},"additionalProperties":false}`

func (helper *extensionDeliveryTestHelper) handleReplyParts(request *jsonrpc.Request) (any, *jsonrpc.Error) {
	switch request.Method {
	case "initialize":
		provides := []string{"delivery"}
		if helper.mode == "parts-unregistered" {
			provides = nil
		}
		return map[string]any{"id": deliveryTestExtension, "version": "1.0.0", "provides": provides, "events": []string{"turn.finished"}}, nil
	case "turn.finished":
		var input extensionInput
		if json.Unmarshal(request.Params, &input) != nil || input.Delivery == nil || !input.Delivery.Reply {
			return nil, &jsonrpc.Error{Code: -32603, Message: "missing reply context"}
		}
		parts := []string{input.Output}
		if input.Settings["reply_mode"] == "paragraphs" {
			parts = []string{"First paragraph.\n\n", "Second paragraph."}
		}
		if helper.mode == "parts-invalid" {
			parts = []string{"Changed output must never be sent."}
		}
		return map[string]any{"reply_parts": parts}, nil
	case "shutdown":
		return nil, nil
	default:
		return nil, &jsonrpc.Error{Code: -32601, Message: "method not found"}
	}
}

func replyExtensionFixture(t *testing.T, installed, enabled, selected bool, mode string) (*ChannelBridge, *TaskService, ChannelTarget, string) {
	t.Helper()
	b, tasks, target := expressionBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: replyFixtureText}, nil
	}))
	core := tasks.core
	if installed {
		root := filepath.Join(core.Store.DataDir(), "extensions", deliveryTestExtension)
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		manifest := ExtensionManifest{Protocol: extensionProtocol, ID: deliveryTestExtension, Version: "1.0.0", Name: "Reply fixture", Description: "Extension-owned expression", Run: ExtensionRun{Command: os.Args[0], Args: []string{"--freeagent-extension-host-helper", "--freeagent-delivery-check=" + mode}}, Delivery: true, Events: []string{"turn.finished"}, ModelSettings: []string{"reply_mode"}, SettingsSchema: json.RawMessage(replyFixtureSchema), TimeoutMS: 10000}
		if err := os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		config := tasks.Config()
		config.Extensions.Enabled = map[string]bool{deliveryTestExtension: enabled}
		config.Extensions.Settings = map[string]map[string]any{deliveryTestExtension: {"reply_mode": "paragraphs"}}
		core.setConfig(config)
	}
	core.extensions = newExtensionHost(core.Store.DataDir(), core.Config.Workspace, core.Config, nil)
	t.Cleanup(func() { _ = core.extensions.close() })
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := core.Store.GetConversation(t.Context(), input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), conversation.SpaceID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetPersonPermission(t.Context(), "telegram:user:7", "Fixture", PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	selection := CapabilitySelection{}
	if selected {
		selection.Extensions = []string{deliveryTestExtension}
	}
	if err := tasks.SelectCapabilities(t.Context(), input.ConversationID, selection); err != nil {
		t.Fatal(err)
	}
	return b, tasks, target, input.ConversationID
}

func submitReplyFixture(t *testing.T, tasks *TaskService, target ChannelTarget, chat, key string, capabilities *CapabilitySelection) RunResult {
	t.Helper()
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: key, Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	if input.ConversationID != chat {
		t.Fatal("destination changed")
	}
	result, err := tasks.submitWithOptions(t.Context(), chat, input.origin.RequestKey, input.text, RunOptions{channel: input.origin, personID: input.source.PersonID, Capabilities: capabilities})
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("status=%s err=%v", result.Run.Status, err)
	}
	return result
}

func frozenReplyFixture(t *testing.T, b *ChannelBridge, result RunResult, suffix string) []channelwire.TextPart {
	t.Helper()
	logical, err := b.connections["tg"].session.extensionReplyParts(t.Context(), "reply/"+result.Run.ID+"/"+suffix, result.Answer)
	if err != nil || strings.Join(logical, "") != result.Answer {
		t.Fatal("logical parts changed the source", logical, err)
	}
	parts, _, err := b.connections["tg"].session.freezeReplyText(t.Context(), "reply/"+result.Run.ID+"/"+suffix, result.Answer)
	if err != nil {
		t.Fatal(err)
	}
	var joined string
	for _, part := range parts {
		joined += part.Text
	}
	if strings.ReplaceAll(joined, "\n", "") != strings.ReplaceAll(replyFixtureText, "\n", "") {
		t.Fatal("reply lost/changed content", joined)
	}
	return parts
}

func TestExtensionReplyBoundaryAbsentDisabledUnselectedAndSelected(t *testing.T) {
	for _, test := range []struct {
		name                         string
		installed, enabled, selected bool
		want                         int
	}{
		{"plain-core", false, false, false, 1},
		{"installed-disabled", true, false, false, 1},
		{"enabled-unselected", true, true, false, 1},
		{"selected", true, true, true, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, tasks, target, chat := replyExtensionFixture(t, test.installed, test.enabled, test.selected, "parts-normal")
			catalog := tasks.CapabilityCatalog()
			options := 0
			for _, item := range catalog.Extensions {
				options += len(item.TurnSettings)
			}
			if (options != 0) != (test.installed && test.enabled) {
				t.Fatal("unavailable extension exposed options", catalog)
			}
			result := submitReplyFixture(t, tasks, target, chat, "2", nil)
			parts := frozenReplyFixture(t, b, result, "boundary")
			if len(parts) != test.want {
				t.Fatalf("parts=%+v want=%d", parts, test.want)
			}
			for i, part := range parts {
				if part.LogicalPart != i+1 {
					t.Fatal(parts)
				}
			}
		})
	}
}

func TestExtensionReplyTurnOverrideFrozenSettingsAndRevocation(t *testing.T) {
	b, tasks, target, chat := replyExtensionFixture(t, true, true, true, "parts-normal")
	selection := CapabilitySelection{Extensions: []string{deliveryTestExtension}, Settings: map[string]map[string]string{deliveryTestExtension: {"reply_mode": "single"}}}
	result := submitReplyFixture(t, tasks, target, chat, "2", &selection)
	if parts := frozenReplyFixture(t, b, result, "override"); len(parts) != 1 {
		t.Fatal(parts)
	}
	if tasks.Config().Extensions.Settings[deliveryTestExtension]["reply_mode"] != "paragraphs" {
		t.Fatal("turn override changed global defaults")
	}
	restored, err := tasks.core.forRun(result.Run)
	if err != nil {
		t.Fatal(err)
	}
	if settings := extensionRunSettings(result.Run, deliveryTestExtension, restored.Config); settings["reply_mode"] != "single" {
		t.Fatal("restored Run lost settings", settings)
	}
	result = submitReplyFixture(t, tasks, target, chat, "3", nil)
	if parts := frozenReplyFixture(t, b, result, "frozen"); len(parts) != 2 {
		t.Fatal(parts)
	}
	// Keep a conversation default to verify disable also revokes that selection.
	if err := tasks.SelectCapabilities(t.Context(), chat, selection); err != nil {
		t.Fatal(err)
	}
	config := tasks.Config()
	config.Extensions.Enabled[deliveryTestExtension] = false
	tasks.core.setConfig(config)
	tasks.core.extensions.configure(config)
	if parts := frozenReplyFixture(t, b, result, "before-enqueue-revoked"); len(parts) != 1 {
		t.Fatal(parts)
	}
	if parts := frozenReplyFixture(t, b, result, "frozen"); len(parts) != 2 {
		t.Fatal("revocation rewrote a frozen group", parts)
	}
	defaults, err := tasks.SelectedCapabilities(t.Context(), chat)
	if err != nil || len(defaults.Extensions) != 0 || len(defaults.Settings) != 0 {
		t.Fatal("revocation left broken defaults", defaults, err)
	}
	if err := tasks.SelectCapabilities(t.Context(), chat, selection); err == nil {
		t.Fatal("disabled extension regained a turn override")
	}
}

func TestExtensionReplyRejectsChangedContentAndUnregisteredCapability(t *testing.T) {
	for _, mode := range []string{"parts-invalid", "parts-unregistered"} {
		t.Run(mode, func(t *testing.T) {
			b, tasks, target, chat := replyExtensionFixture(t, true, true, true, mode)
			var reported bool
			tasks.SetEvents(func(event Event) {
				if event.Kind == EventExtensionError {
					reported = true
				}
			})
			result := submitReplyFixture(t, tasks, target, chat, "2", nil)
			if !reported {
				t.Fatal("invalid extension result was not reported")
			}
			if parts := frozenReplyFixture(t, b, result, "invalid"); len(parts) != 1 {
				t.Fatal("failed extension changed delivery", parts)
			}
		})
	}
}

func TestExtensionReplySettingsValidateAndQueueKeepsExplicitSelection(t *testing.T) {
	_, tasks, _, chat := replyExtensionFixture(t, true, true, true, "parts-normal")
	selection := CapabilitySelection{Extensions: []string{deliveryTestExtension}, Settings: map[string]map[string]string{deliveryTestExtension: {"reply_mode": "single"}}}
	queued, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "queued", Prompt: "queued", Capabilities: &selection})
	if err != nil || queued.Capabilities.Settings[deliveryTestExtension]["reply_mode"] != "single" {
		t.Fatal(queued, err)
	}
	selection.Settings[deliveryTestExtension]["reply_mode"] = "paragraphs"
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "queued", Prompt: "queued", Capabilities: &selection}); !errors.Is(err, ErrRequestKey) {
		t.Fatal("changed queue selection reused key", err)
	}
	for key, value := range map[string]string{"reply_mode": "undeclared", "authority": "full"} {
		selection.Settings[deliveryTestExtension] = map[string]string{key: value}
		if err := tasks.SelectCapabilities(t.Context(), chat, selection); err == nil {
			t.Fatal("undeclared settings accepted", key)
		}
	}
	selection.Extensions = nil
	if err := selection.validate(tasks.Config()); err == nil {
		t.Fatal("unselected extension accepted settings")
	}
}

func TestExtensionReplyPartsAreLosslessBoundedAndNonempty(t *testing.T) {
	for _, parts := range [][]string{nil, {"changed"}, {"First paragraph.", "Second paragraph."}, {"", replyFixtureText}, {replyFixtureText, " "}, {"a", "b", "c", "d"}, {string([]byte{255})}} {
		if validExtensionReplyParts(parts, replyFixtureText) {
			t.Fatal("invalid parts accepted", parts)
		}
	}
	if !validExtensionReplyParts([]string{"First paragraph.\n\n", "Second paragraph."}, replyFixtureText) || validExtensionReplyParts([]string{strings.Repeat("x", maxExtensionFinishedOutput+1)}, strings.Repeat("x", maxExtensionFinishedOutput+1)) {
		t.Fatal("reply envelope invalid")
	}
}

// Independently compiled AffectWeave, actual bidirectional process transport,
// restricted PG, and the ordinary channel path. No Telegram requests are sent.
func TestAffectWeaveActualReplyExpressionIsOptionalAndTurnScoped(t *testing.T) {
	core, tasks, _ := affectHost(t)
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: replyFixtureText}, nil
	})
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Fixture Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: KeySourceSaved, Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	config := cloneConfig(tasks.Config())
	config.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}}}
	// Expression policy also works without choosing a character; it does not
	// create any Persona, relationship, memory or automatic analysis request.
	config.Extensions.Settings[affectExtensionID] = map[string]any{"reply_mode": "paragraphs"}
	if err := WriteConfig(core.Store.DataDir(), config); err != nil {
		t.Fatal(err)
	}
	core.setConfig(config)
	core.extensions.configure(config)
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	if err := control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	chat := input.ConversationID
	if err := tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{Extensions: []string{affectExtensionID}}); err != nil {
		t.Fatal(err)
	}
	result := submitReplyFixture(t, tasks, target, chat, "2", nil)
	if parts := frozenReplyFixture(t, b, result, "actual-extension"); len(parts) != 2 {
		t.Fatal(parts)
	}
	selected := CapabilitySelection{Extensions: []string{affectExtensionID}, Settings: map[string]map[string]string{affectExtensionID: {"reply_mode": "single"}}}
	result = submitReplyFixture(t, tasks, target, chat, "3", &selected)
	if parts := frozenReplyFixture(t, b, result, "explicit-user-setting"); len(parts) != 1 {
		t.Fatal(parts)
	}
	if err := core.extensions.restart(affectExtensionID); err != nil {
		t.Fatal(err)
	}
	result = submitReplyFixture(t, tasks, target, chat, "4", nil)
	if parts := frozenReplyFixture(t, b, result, "after-extension-restart"); len(parts) != 2 {
		t.Fatal(parts)
	}
	if err := tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{}); err != nil {
		t.Fatal(err)
	}
	result = submitReplyFixture(t, tasks, target, chat, "5", nil)
	if parts := frozenReplyFixture(t, b, result, "unselected"); len(parts) != 1 {
		t.Fatal(parts)
	}
}
