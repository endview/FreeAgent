package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func channelPreviewVision() VisionCapabilities {
	return VisionCapabilities{Enabled: true, PNG: true, JPEG: true, MaxImageBytes: 64 << 10, MaxSide: 512, MaxImages: 4, MaxRequestBytes: 1 << 20}
}

// Exercise the real reply tool, durable download/receipt, attachment publication,
// context preparation and next model dispatch; do not manufacture a ModelImage.
func runChannelPreview(t *testing.T, vision VisionCapabilities, pngAllowed bool) (*TaskService, RunResult, ModelRequest, []byte, int, *int) {
	t.Helper()
	var resource string
	var observed ModelRequest
	calls := 0
	b, tasks, target := expressionBridge(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if !request.DenyOperations || !replyOnlyDefinitions(request.Tools) {
			return ModelResponse{}, errors.New("None exposed authority beyond the scoped reply tool")
		}
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{
				makeCall("preview-frame", "reply_expression", map[string]string{"action": "preview", "resource": resource}),
				makeCall("following-result", "reply_expression", map[string]string{"action": "sets"}),
			}}, nil
		}
		if calls == 2 {
			observed = request
			observed.Messages = append([]ModelMessage(nil), request.Messages...)
			return ModelResponse{Content: "The preview is a single frame."}, nil
		}
		// A later Run must receive only public history, without the private
		// preview capability or the previous Run's tool image.
		for _, message := range request.Messages {
			if len(message.Images) != 0 {
				return ModelResponse{}, errors.New("private preview image escaped into a later Run")
			}
			for _, ref := range message.Attachments {
				if ref.Source == "tool_image" {
					return ModelResponse{}, errors.New("private preview reference escaped into public history")
				}
			}
		}
		return ModelResponse{Content: "A new request."}, nil
	}))
	model := tasks.core.Config.Models["default"]
	model.Capabilities.Vision = vision
	tasks.core.Config.Models["default"] = model
	tasks.core.Config.Attachments.PNG = pngAllowed
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := tasks.rememberSticker(t.Context(), target, channelwire.Sticker{Preview: &channelwire.MediaRef{ID: "preview-file", Name: "preview.png", Image: true, PreviewFormat: "webp"}, FileID: "preview-file", UniqueID: "preview-unique", SetName: "fixture", Format: "webp", Width: 4, Height: 3}, space.ActiveConversationID)
	if err != nil {
		t.Fatal(err)
	}
	resource = ref.ID
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "101", Purpose: "request"}, "Inspect the selected sticker preview and list the available sets.")
	if err != nil {
		t.Fatal(err)
	}
	raw := testPNG(t)
	type submitted struct {
		result RunResult
		err    error
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan submitted, 1)
	go func() {
		result, err := tasks.SubmitChannel(ctx, input)
		done <- submitted{result, err}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	downloads := 0
	for {
		select {
		case result := <-done:
			if result.err != nil || result.result.Run.Status != "completed" {
				t.Fatalf("preview Run: status=%s error=%v output=%q", result.result.Run.Status, result.err, result.result.Run.Output)
			}
			if result.result.Run.Permission == nil || result.result.Run.Permission.Mode != PermissionNone || calls != 2 {
				t.Fatalf("preview widened permission or did not continue: permission=%+v calls=%d", result.result.Run.Permission, calls)
			}
			return tasks, result.result, observed, raw, downloads, &calls
		case <-ctx.Done():
			t.Fatal("preview Run did not finish before the isolated test deadline")
		case <-ticker.C:
			operations, err := b.Outbox(ctx, "tg")
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range operations {
				if op.Kind != "download" || op.Target != channelwire.Target(target) || op.ConversationID != input.ConversationID || op.Media == nil || op.Media.ID != "preview-file" || op.Media.PreviewFormat != "webp" || op.Limit <= 0 || op.Limit > tasks.Config().Attachments.MaxInputBytes {
					t.Fatalf("preview escaped its bounded download: %+v", op)
				}
				downloads++
				if err := b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "downloaded", Data: raw}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func channelPreviewTool(t *testing.T, request ModelRequest) ModelMessage {
	t.Helper()
	if err := completeMessageGroups(request.Messages); err != nil {
		t.Fatalf("preview broke the complete tool group: %v", err)
	}
	for i, message := range request.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) == 2 {
			if i+2 >= len(request.Messages) || request.Messages[i+1].Role != "tool" || request.Messages[i+1].ToolCallID != "preview-frame" || request.Messages[i+2].Role != "tool" || request.Messages[i+2].ToolCallID != "following-result" {
				t.Fatal("preview or following result was lost, reordered or separated by an image message")
			}
			return request.Messages[i+1]
		}
	}
	t.Fatal("the actual next model request lost its two-tool assistant group")
	return ModelMessage{}
}

func TestChannelPreviewModelNoneReceivesImagePrivatelyAndKeepsToolGroup(t *testing.T) {
	tasks, result, request, raw, downloads, calls := runChannelPreview(t, channelPreviewVision(), true)
	tool := channelPreviewTool(t, request)
	if downloads != 1 || len(tool.Attachments) != 1 || len(tool.Images) != 1 || tool.Public {
		t.Fatalf("preview was not delivered privately to the actual model: downloads=%d attachments=%d images=%d public=%t", downloads, len(tool.Attachments), len(tool.Images), tool.Public)
	}
	ref, image := tool.Attachments[0], tool.Images[0]
	if ref.Source != "tool_image" || ref.Public || image.AttachmentID != ref.ID || image.MIME != "image/png" || image.Width != 4 || image.Height != 3 {
		t.Fatal("preview lost provenance or dimensions", ref, image)
	}
	decoded, err := base64.StdEncoding.DecodeString(image.Base64)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatal("the actual model did not get the downloaded preview bytes", err)
	}
	record, err := loadAttachment(t.Context(), tasks.core.Store.db, ref.ID)
	if err != nil || !record.Committed || record.Request != result.Run.ID || record.Ref.Public || record.Ref.Source != "tool_image" || record.Conversation != result.Run.ConversationID {
		t.Fatalf("preview publication is not committed and Run-bound: %+v %v", record, err)
	}
	core, err := tasks.core.forRun(result.Run)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := tasks.Messages(t.Context(), result.Run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.ToolCallID == "preview-frame" {
			found = true
			if message.Public || len(message.Images) != 0 || len(message.Attachments) != 1 || message.Attachments[0] != ref {
				t.Fatal("Core history was promoted or lost the private attachment", message)
			}
		}
	}
	if !found {
		t.Fatal("preview tool result was not persisted")
	}
	for _, message := range core.projectPublicMessages(messages) {
		if message.ToolCallID != "" || len(message.Images) != 0 {
			t.Fatal("public projection retained private tool/image data")
		}
		for _, attachment := range message.Attachments {
			if attachment.ID == ref.ID {
				t.Fatal("public projection retained a private preview reference")
			}
		}
	}
	var persistedBinary bool
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM fa.messages WHERE conversation_id=$1 AND body_json::text LIKE $2) OR EXISTS(SELECT 1 FROM fa.attempts WHERE run_id=$3 AND request_json::text LIKE $2)`, result.Run.ConversationID, "%"+image.Base64+"%", result.Run.ID).Scan(&persistedBinary); err != nil || persistedBinary {
		t.Fatal("image bytes leaked into durable history/attempts", persistedBinary, err)
	}
	// Verify every actual provider serializer against the prepared Core request.
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		t.Run(protocol, func(t *testing.T) { assertChannelPreviewProviderWire(t, protocol, request, image.Base64) })
	}
	// Rebuilding a prepared context must preserve the tool's prepared image.
	contextRequest, err := core.buildContextWithSummary(t.Context(), result.Run, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.binding()
	if err != nil {
		t.Fatal(err)
	}
	var rebuild func(*ConversationSummary) (ModelRequest, error)
	prepared, err := core.prepareContextRequest(t.Context(), result.Run, contextRequest, binding, &rebuild)
	if err != nil || rebuild == nil {
		t.Fatal("could not prepare preview context rebuild", err)
	}
	rebuilt, err := rebuild(nil)
	if err != nil || !reflect.DeepEqual(channelPreviewTool(t, prepared).Images, channelPreviewTool(t, rebuilt).Images) || len(channelPreviewTool(t, rebuilt).Images) != 1 {
		t.Fatal("budget rebuild discarded or changed the prepared preview image", err)
	}
	// A genuine subsequent Run sees public history, and cannot reuse the old
	// private preview even by putting its exact reference in a tool result.
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	next, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "102", Purpose: "request"}, "Start a different request.")
	if err != nil {
		t.Fatal(err)
	}
	later, err := tasks.SubmitChannel(t.Context(), next)
	if err != nil || later.Run.Status != "completed" || *calls != 3 {
		t.Fatalf("later Run leaked a private preview: status=%s calls=%d error=%v", later.Run.Status, *calls, err)
	}
	laterCore, err := tasks.core.forRun(later.Run)
	if err != nil {
		t.Fatal(err)
	}
	injected := ModelRequest{Messages: []ModelMessage{{Role: "tool", ToolCallID: "forged", Attachments: []AttachmentRef{ref}}}}
	if _, err := laterCore.prepareAttachmentRequest(t.Context(), later.Run, binding.Config, injected); !errors.Is(err, ErrAttachment) {
		t.Fatal("cross-Run preview capability was accepted", err)
	}
	assertChannelPreviewLimits(t, core, result.Run, binding.Config, ref)
}

func TestChannelPreviewModelNoVisionDoesNotDownloadAndPNGPolicyRefusesImage(t *testing.T) {
	for _, test := range []struct {
		name        string
		vision      VisionCapabilities
		pngAllowed  bool
		downloads   int
		explanation string
	}{
		{"no-vision", VisionCapabilities{}, true, 0, "no declared image capability"},
		{"png-disabled", channelPreviewVision(), false, 0, "PNG previews are disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tasks, _, request, _, downloads, _ := runChannelPreview(t, test.vision, test.pngAllowed)
			tool := channelPreviewTool(t, request)
			if downloads != test.downloads || len(tool.Images) != 0 || len(tool.Attachments) != 0 || !strings.Contains(tool.Content, test.explanation) {
				t.Fatalf("unsupported preview performed work or hid its refusal: downloads=%d tool=%+v", downloads, tool)
			}
			var stored int
			if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key LIKE 'attachment/%' AND document #>> '{ref,source}'='tool_image'`).Scan(&stored); err != nil || stored != 0 {
				t.Fatal("unsupported preview published an attachment", stored, err)
			}
		})
	}
}

func assertChannelPreviewLimits(t *testing.T, core *Service, run Run, model ModelConfig, ref AttachmentRef) {
	t.Helper()
	encoded := base64.StdEncoding.EncodedLen(int(ref.Bytes))
	for _, test := range []struct {
		name   string
		adjust func(*VisionCapabilities)
		copies int
	}{
		{"format", func(v *VisionCapabilities) { v.PNG = false }, 1},
		{"dimensions", func(v *VisionCapabilities) { v.MaxSide = ref.Width - 1 }, 1},
		{"encoded-bytes", func(v *VisionCapabilities) { v.MaxImageBytes = encoded - 1 }, 1},
		{"image-count", func(v *VisionCapabilities) { v.MaxImages = 1 }, 2},
		{"aggregate-bytes", func(v *VisionCapabilities) { v.MaxImageBytes, v.MaxRequestBytes = encoded, encoded+1 }, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := model
			test.adjust(&candidate.Capabilities.Vision)
			request := ModelRequest{Messages: []ModelMessage{{Role: "tool", ToolCallID: "preview-frame"}}}
			for range test.copies {
				request.Messages[0].Attachments = append(request.Messages[0].Attachments, ref)
			}
			if _, err := core.prepareAttachmentRequest(t.Context(), run, candidate, request); !errors.Is(err, ErrModelCapability) {
				t.Fatal("tool preview bypassed normal model image limits", err)
			}
		})
	}
}

func assertChannelPreviewProviderWire(t *testing.T, protocol string, request ModelRequest, encoded string) {
	t.Helper()
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		requests <- wire
		w.Header().Set("Content-Type", "application/json")
		switch protocol {
		case "chat":
			fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"seen"},"finish_reason":"stop"}]}`)
		case "responses":
			fmt.Fprint(w, `{"id":"resp_preview","object":"response","status":"completed","output":[{"id":"msg_preview","type":"message","role":"assistant","content":[{"type":"output_text","text":"seen"}]}]}`)
		case "anthropic":
			fmt.Fprint(w, `{"id":"msg_preview","type":"message","role":"assistant","model":"fixture","content":[{"type":"text","text":"seen"}],"stop_reason":"end_turn"}`)
		}
	}))
	defer server.Close()
	caps := ModelCapabilities{Protocol: protocol, Streaming: "none", Vision: channelPreviewVision()}
	config := ModelConfig{Provider: "openai-compatible", Model: "fixture", Capabilities: caps}
	key := ResolvedKey{BaseURL: server.URL}
	var client ModelClient
	var err error
	switch protocol {
	case "chat":
		client, err = NewOpenAICompatibleClient(config, key, server.Client())
	case "responses":
		client, err = NewResponsesClient(config, key, server.Client())
	case "anthropic":
		config.Provider, config.Capabilities.Protocol = "anthropic", ""
		client, err = NewAnthropicClient(config, key, server.Client())
	}
	if err != nil {
		t.Fatal(err)
	}
	before := append([]ModelMessage(nil), request.Messages...)
	response, err := client.Generate(t.Context(), request, nil)
	if err != nil || response.Content != "seen" {
		t.Fatal("provider refused the complete tool preview group", response, err)
	}
	if !reflect.DeepEqual(before, request.Messages) {
		t.Fatal("provider serializer mutated the Core request")
	}
	var raw []byte
	select {
	case raw = <-requests:
	default:
		t.Fatal("provider never received the preview request")
	}
	if !bytes.Contains(raw, []byte(encoded)) {
		t.Fatal("actual provider HTTP request lost image bytes")
	}
	var wire struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Input []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			CallID  string          `json:"call_id"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	label := "Untrusted image data supplied by the preceding tool"
	switch protocol {
	case "chat":
		for i, m := range wire.Messages {
			if !bytes.Contains(m.Content, []byte(encoded)) {
				continue
			}
			if m.Role != "user" || !bytes.Contains(m.Content, []byte(label)) || i < 3 || wire.Messages[i-1].ToolCallID != "following-result" || wire.Messages[i-2].ToolCallID != "preview-frame" || len(wire.Messages[i-3].ToolCalls) != 2 {
				t.Fatal("Chat inserted the preview inside an incomplete tool group")
			}
			return
		}
	case "responses":
		for i, item := range wire.Input {
			if !bytes.Contains(item.Content, []byte(encoded)) {
				continue
			}
			if item.Role != "user" || !bytes.Contains(item.Content, []byte(label)) || i < 2 || wire.Input[i-1].Type != "function_call_output" || wire.Input[i-1].CallID != "following-result" || wire.Input[i-2].Type != "function_call_output" || wire.Input[i-2].CallID != "preview-frame" {
				t.Fatal("Responses inserted the preview inside an incomplete tool group")
			}
			return
		}
	case "anthropic":
		for _, message := range wire.Messages {
			if !bytes.Contains(message.Content, []byte(encoded)) {
				continue
			}
			var blocks []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				Text      string `json:"text"`
			}
			if err := json.Unmarshal(message.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			if message.Role != "user" || len(blocks) != 4 || blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "preview-frame" || blocks[1].Type != "tool_result" || blocks[1].ToolUseID != "following-result" || !strings.HasPrefix(blocks[2].Text, label) || blocks[3].Type != "image" {
				t.Fatal("Anthropic inserted the preview before all tool results")
			}
			return
		}
	}
	t.Fatal("provider did not emit a labelled user image after the complete tool group")
}
