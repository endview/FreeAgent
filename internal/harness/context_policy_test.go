package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSummaryPolicyPreservesLegacyValuesAndIndependentSwitch(t *testing.T) {
	for _, n := range []int{0, 4096, 10240} {
		config := DefaultConfig(t.TempDir())
		config.SummaryTargetMode = ""
		config.TaskSummaryBytes = n
		var restored Config
		if json.Unmarshal(mustJSON(config), &restored) != nil {
			t.Fatal("legacy config did not decode")
		}
		limit, _, source := summaryPolicy(restored, ContextWindow{ByteLimit: 128 << 10})
		if source != "legacy_manual" || limit != max(4096, n) || AutomaticCompactionEnabled(restored) != (n > 0) {
			t.Fatalf("legacy intent changed: %d %d %s", n, limit, source)
		}
	}
	config := DefaultConfig(t.TempDir())
	disabled := false
	config.AutomaticCompaction = &disabled
	bytes, tokens, source := summaryPolicy(config, ContextWindow{Tokens: 32768, InputBudget: 28000, ByteLimit: 128 << 10})
	if AutomaticCompactionEnabled(config) || bytes != maxSummaryBytes || tokens != 2048 || source != "auto_model_window" {
		t.Fatal("auto target and scheduler were coupled", bytes, tokens, source)
	}
}

func TestAutomaticPolicyCanBeProposedButNotAppliedWithoutConfirmation(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	control, err := NewControlService(NewTaskService(s))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetRuntimeLimits, Authorization: ControlModelProposed, ConversationID: chat, Arguments: mustJSON(map[string]any{"automatic_compaction": false, "summary_target_mode": "auto"})})
	if err != nil || operation.Status != ControlPending {
		t.Fatal(operation, err)
	}
	if s.Config.SummaryTargetMode == "auto" || !AutomaticCompactionEnabled(s.Config) {
		t.Fatal("proposal applied before confirmation")
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), operation.ID, chat, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(capability, chat, "local-user", confirmation.Code))
	if err != nil || !result.Applied || s.Config.SummaryTargetMode != "auto" || AutomaticCompactionEnabled(s.Config) {
		t.Fatal(result, err)
	}
	loaded, err := LoadConfig(s.Store.DataDir())
	if err != nil || loaded.SummaryTargetMode != "auto" || AutomaticCompactionEnabled(loaded) {
		t.Fatal("policy not durable", err)
	}
}

func TestSummaryPreflightRejectsLateBrokenGroupBeforeAnyModelCall(t *testing.T) {
	calls := 0
	s, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{Content: "summary"}, nil
	}))
	chat := conversation(t, s)
	var messages []ModelMessage
	for i := 0; i < 35; i++ {
		messages = append(messages, ModelMessage{Role: "user", Content: strings.Repeat("source ", 12000)}, ModelMessage{Role: "assistant", Content: "finished source"})
	}
	messages = append(messages, ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "missing", Name: "read", Arguments: []byte(`{}`)}}}, ModelMessage{Role: "user", Content: "later boundary"})
	through := insertProjectionFixture(t, s, chat, messages)
	prefix, err := readSourcePrefix(t.Context(), s.Store.db, chat, through, sourcePrefix{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyCompaction(t.Context(), Run{ConversationID: chat}, compactionPlan{scope: "full", source: prefix, limit: 1024}, false)
	if !errors.Is(err, ErrContextStructure) || errors.Is(err, ErrContextLimit) || calls != 0 {
		t.Fatal("invalid late group reached a model", calls, err)
	}
}

func TestSummaryReferenceRepairAndReadbackCorrections(t *testing.T) {
	s, tasks, chat, _ := summaryFixture(t)
	calls := 0
	s.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if !strings.Contains(request.Messages[0].Content, "explicit human goals") || !strings.Contains(request.Messages[0].Content, "revoked") {
			t.Fatal("summary request omitted requirement/evidence distinction")
		}
		if calls == 1 {
			return ModelResponse{Content: "Evidence #m999999999"}, nil
		}
		return ModelResponse{Content: "User requirements: retain the project restriction. Uncertain facts: UNKNOWN is not success. Evidence #m1."}, nil
	})
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 512})
	if err != nil || !result.Changed || calls != 2 {
		t.Fatal("invalid reference was not repaired once", result, calls, err)
	}
	read := &ContextReadback{Ref: "#m1", Revision: "version", Offset: 0, Next: 10, Status: "read"}
	request := summaryRequest(ModelBinding{}, ExecutionLimits{MaxOutputTokens: 4096}, 1024, "", []Message{
		{ID: 4, ModelMessage: ModelMessage{Role: "tool", Content: "DUPLICATE-READBACK-QUOTE", Readback: read}},
		{ID: 5, ModelMessage: ModelMessage{Role: "assistant", Content: "NEW-CORRECTION: previous result is still UNKNOWN"}},
	})
	if strings.Contains(request.Messages[1].Content, "DUPLICATE-READBACK-QUOTE") || !strings.Contains(request.Messages[1].Content, "NEW-CORRECTION") {
		t.Fatal("readback suppression discarded a new finding or summarized duplicate text")
	}
}

func TestDelayedDeliveryDoesNotSplitAuthorizedActiveToolGroup(t *testing.T) {
	call := ToolCall{ID: "a", Name: "file_read", Arguments: []byte(`{}`)}
	messages := []Message{
		{ID: 1, RunID: "active", ModelMessage: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{call}}},
		{ID: 2, RunID: "old", ModelMessage: ModelMessage{Kind: messageDelivery, Role: "assistant", Content: "actual earlier delivery", Public: true, Channel: &ChannelMessageSource{Purpose: "delivery"}}},
		{ID: 3, RunID: "active", ModelMessage: ModelMessage{Role: "tool", ToolCallID: "a", Content: "actual current result"}},
	}
	projected, err := projectContextMessages(messages, "channel_public", "active", true)
	if err != nil || len(projected) != 3 {
		t.Fatal(projected, err)
	}
	var protocol []ModelMessage
	for _, m := range projected {
		protocol = append(protocol, m.ModelMessage)
	}
	if completeMessageGroups(protocol) != nil || projected[1].Role != "tool" || projected[2].ID != 2 {
		t.Fatal("late delivery split the protocol", fmt.Sprint(projected))
	}
}
