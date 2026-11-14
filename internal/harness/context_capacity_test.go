package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMatchedAttemptCalibrationAcrossRuntimeRequests(t *testing.T) {
	var measured []ContextUsage
	s, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.ContextUsage == nil {
			t.Fatal("runtime request has no paired measurement")
		}
		measured = append(measured, *request.ContextUsage)
		return ModelResponse{Content: "ACK", Usage: &ModelUsage{InputTokens: 5000, OutputTokens: 1, TotalTokens: 5001}}, nil
	}))
	s.Config.TaskSummaryBytes = 0
	tasks := NewTaskService(s)
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, s)
	for i := range 3 {
		if _, err := tasks.Submit(t.Context(), chat, fmt.Sprint(i), "Append synthetic input."); err != nil {
			t.Fatal(err)
		}
	}
	if len(measured) != 3 || measured[0].CalibrationAttempt != "" {
		t.Fatal("unexpected initial calibration", measured)
	}
	for _, usage := range measured[1:] {
		if usage.EstimateMethod != "matched_attempt_prefix_plus_new_input" || usage.CalibrationAttempt == "" {
			t.Fatal("unchanged runtime prefix did not reuse matched usage", usage.EstimateMethod, usage.CalibrationAttempt)
		}
	}
}

func TestToolProjectionCanReadEveryOmittedRangeWithoutChangingSource(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	s.Config.MaxContextBytes = 16000
	s.Config.MaxOutputTokens = 128
	chat := conversation(t, s)
	text := "HEAD: confirmed exit 7\n" + strings.Repeat("工具原始数据。", 2500) + "\nTAIL: pending UNKNOWN verification"
	code := 7
	m := toolMessage(ToolCall{ID: "fixture"}, ToolResult{Content: text, Outcome: &ToolOutcome{Status: "unknown"}, Receipts: []ToolReceipt{{Kind: "command", Status: "unknown", ExitCode: &code}}})
	id := insertProjectionFixture(t, s, chat, []ModelMessage{m})
	run := Run{ConversationID: chat}
	projected := sourceMessage(Message{ID: id, ModelMessage: m}, run)
	request := s.foldToolContext(ModelRequest{Tools: []ToolDefinition{contextToolDefinition()}, Messages: []ModelMessage{projected}})
	preview := request.Messages[0]
	if !preview.ContextSource.Folded || !strings.Contains(preview.Content, "HEAD") || !strings.Contains(preview.Content, "TAIL") || !strings.Contains(preview.Content, `"ExitCode":7`) || !strings.Contains(preview.Content, "unknown") {
		t.Fatalf("lost result facts: %s", preview.Content)
	}
	if preview.ToolCallID != m.ToolCallID || preview.Role != "tool" {
		t.Fatal("protocol result identity changed")
	}
	var restored strings.Builder
	for offset := 0; offset < len(text); {
		result, err := s.contextRead(t.Context(), run, fmt.Sprintf("#m%d", id), projected.ContextSource.Revision, offset, 1000)
		if err != nil || result.Readback == nil || result.Readback.Next <= offset {
			t.Fatal(offset, err)
		}
		body := strings.SplitN(result.Content, "\n", 2)
		restored.WriteString(body[1])
		offset = result.Readback.Next
	}
	if restored.String() != text {
		t.Fatal("original source could not be reconstructed")
	}
	stored, _ := s.Store.ListMessages(t.Context(), chat)
	if len(stored) != 1 || stored[0].Content != text {
		t.Fatal("projection modified original source")
	}
}

func TestContextReadUsesAudienceRevisionAndEpochCapacity(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	s.Config.MaxContextBytes = 16000
	s.Config.MaxOutputTokens = 128
	chat := conversation(t, s)
	id := insertProjectionFixture(t, s, chat, []ModelMessage{{Role: "tool", ToolCallID: "private", Content: strings.Repeat("private", 1000)}})
	run := Run{ConversationID: chat}
	ref := fmt.Sprintf("#m%d", id)
	if _, err := s.contextRead(t.Context(), run, ref, "stale-revision", 0, 100); err == nil {
		t.Fatal("stale reference accepted")
	}
	if _, err := s.contextRead(t.Context(), run, "#m01", "", 0, 100); err == nil {
		t.Fatal("approximate reference accepted")
	}
	other := conversation(t, s)
	if _, err := s.contextRead(t.Context(), Run{ConversationID: other}, ref, "", 0, 100); err == nil {
		t.Fatal("cross-conversation reference accepted")
	}
	read, err := s.contextRead(t.Context(), run, ref, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	insertProjectionFixture(t, s, chat, []ModelMessage{toolMessage(ToolCall{ID: "read"}, read)})
	again, err := s.contextRead(t.Context(), run, ref, "", 0, 1000)
	if err != nil || again.Readback.Status != "unchanged" || strings.Contains(again.Content, "privateprivate") {
		t.Fatal("unchanged range was duplicated", err)
	}
	s.permission = &PermissionSelection{Mode: PermissionAsk, SpaceID: "public"}
	if _, err = s.contextRead(t.Context(), run, ref, "", 0, 100); err != ErrPermissionDenied {
		t.Fatal("hidden tool output exposed", err)
	}
}

func TestMatchedAttemptCalibrationRequiresExactPrefixAndConfiguration(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	run := Run{ConversationID: chat}
	request := ModelRequest{MaxOutputTokens: 128, Messages: []ModelMessage{{Role: "user", Content: strings.Repeat("中文上下文", 1000)}}}
	before, err := s.requestUsage(run, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.observeContextUsage(t.Context(), run, Attempt{ID: "exact-attempt"}, request, ModelResponse{Usage: &ModelUsage{InputTokens: 3000}}); err != nil {
		t.Fatal(err)
	}
	request.Messages = append(request.Messages, ModelMessage{Role: "assistant", Content: "known answer"}, ModelMessage{Role: "user", Content: "next"})
	after, err := s.requestUsage(run, request)
	if err != nil || after.EstimateMethod != "matched_attempt_prefix_plus_new_input" || after.EstimatedTokens >= before.EstimatedTokens || after.CalibrationAttempt != "exact-attempt" {
		t.Fatalf("matched usage not applied: %+v %v", after, err)
	}
	request.Messages[0].Content += "changed source"
	changed, _ := s.requestUsage(run, request)
	if changed.CalibrationAttempt != "" {
		t.Fatal("rewritten prefix reused usage")
	}
	request.Messages[0].Content = strings.Repeat("中文上下文", 1000)
	request.MaxOutputTokens = 129
	changed, _ = s.requestUsage(run, request)
	if changed.CalibrationAttempt != "" {
		t.Fatal("changed configuration reused usage")
	}
}

func TestPayloadBudgetExcludesCoreMetadataAndCountsImageOnce(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	request := ModelRequest{MaxOutputTokens: 128, Messages: []ModelMessage{{Role: "user", Content: "look", Images: []ModelImage{{MIME: "image/png", Base64: strings.Repeat("a", 5000), Width: 512, Height: 512}}}}}
	_, binding, err := s.requestLimits()
	if err != nil {
		t.Fatal(err)
	}
	body, err := providerRequestPayload(binding, request)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.requestUsage(Run{}, request)
	if err != nil || usage.Bytes != len(body) || usage.EstimatedTokens > 2000 {
		t.Fatal("image counted as text or twice", usage, err)
	}
	request.Messages[0].ContextSource = &ContextSource{Part: "current_input", Revision: strings.Repeat("x", 1000)}
	request.Messages[0].Channel = &ChannelMessageSource{PersonID: strings.Repeat("y", 1000)}
	after, err := providerRequestPayload(binding, request)
	if err != nil || string(body) != string(after) {
		t.Fatal("Core metadata entered HTTP payload", err)
	}
}

func TestCacheUsageKeepsMissingDistinctFromZeroAcrossProviders(t *testing.T) {
	var chat openAIUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":3,"total_tokens":103,"prompt_cache_hit_tokens":60}`), &chat); err != nil {
		t.Fatal(err)
	}
	u, err := modelUsage(&chat)
	if err != nil || u.CacheReadTokens == nil || *u.CacheReadTokens != 60 {
		t.Fatal("DeepSeek cache usage missing", err)
	}
	var anthropic anthropicUsage
	_ = json.Unmarshal([]byte(`{"input_tokens":20,"output_tokens":3,"cache_creation_input_tokens":30,"cache_read_input_tokens":50}`), &anthropic)
	u, err = anthropicUsageValue(&anthropic)
	if err != nil || u.InputTokens != 100 || *u.CacheReadTokens != 50 || *u.CacheCreatedTokens != 30 {
		t.Fatal("Anthropic cache input accounting", err)
	}
	_ = json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":3,"total_tokens":103}`), &chat)
	// Unmarshal into a fresh sample: missing fields are unknown, not zero.
	var missing openAIUsage
	_ = json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":3,"total_tokens":103}`), &missing)
	u, err = modelUsage(&missing)
	if err != nil || u.CacheReadTokens != nil {
		t.Fatal("missing cache usage became zero", err)
	}
}
