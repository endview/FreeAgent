package harness

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func insertProjectionFixture(t *testing.T, s *Service, chat string, messages []ModelMessage) int64 {
	t.Helper()
	var through int64
	for _, m := range messages {
		if err := s.Store.db.QueryRowContext(t.Context(), `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2) RETURNING id`, chat, string(mustJSON(m))).Scan(&through); err != nil {
			t.Fatal(err)
		}
	}
	return through
}

func TestContextProjectionKeepsToolGroupAcrossLegacyDelivery(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	messages := []ModelMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "command_run", Arguments: []byte(`{"command":"echo fixture"}`)}}},
		{Role: "assistant", Content: "confirmed transport copy", Public: true, Channel: &ChannelMessageSource{Purpose: "delivery"}},
		{Role: "tool", ToolCallID: "a", Content: "actual tool result"},
	}
	through := insertProjectionFixture(t, s, chat, messages)
	chunk, _, request, err := s.summaryChunk(t.Context(), Run{ConversationID: chat}, sourcePrefix{}, through, "", 1024)
	if err != nil || len(chunk) != 2 || strings.Contains(request.Messages[1].Content, "transport copy") {
		t.Fatalf("broken projection: len=%d err=%v", len(chunk), err)
	}
	if !strings.Contains(request.Messages[1].Content, "actual tool result") {
		t.Fatal("tool result lost")
	}
	projected, err := projectContextMessages([]Message{{ModelMessage: messages[0]}, {ModelMessage: messages[1]}, {ModelMessage: messages[2]}}, "", "", true)
	if err != nil || len(projected) != 2 || completeMessageGroups([]ModelMessage{projected[0].ModelMessage, projected[1].ModelMessage}) != nil {
		t.Fatalf("next-turn projection invalid: %v", err)
	}
	var stored int
	if err := s.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE conversation_id=$1`, chat).Scan(&stored); err != nil || stored != 3 {
		t.Fatal("projection changed original records", stored, err)
	}
}

func TestContextProjectionDistinguishesMissingResultFromCapacity(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	through := insertProjectionFixture(t, s, chat, []ModelMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "missing", Name: "command_run", Arguments: []byte(`{}`)}}}, {Role: "user", Content: "a genuine input, not a transport record"}})
	_, _, _, err := s.summaryChunk(t.Context(), Run{ConversationID: chat}, sourcePrefix{}, through, "", 1024)
	if !errors.Is(err, ErrContextStructure) || errors.Is(err, ErrContextLimit) {
		t.Fatalf("wrong cause for missing result: %v", err)
	}
}

func TestContextProjectionPublicSummaryExcludesPrivateToolData(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	s.permission = &PermissionSelection{Mode: PermissionAsk, SpaceID: "external-fixture"}
	through := insertProjectionFixture(t, s, chat, []ModelMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "private", Name: "file_read", Arguments: []byte(`{}`)}}},
		{Role: "tool", ToolCallID: "private", Content: "PRIVATE-RESULT-MUST-NOT-LEAK"},
		{Role: "assistant", Content: "PUBLIC-CONFIRMED-RESULT", Public: true, Channel: &ChannelMessageSource{Purpose: "delivery"}},
	})
	_, _, request, err := s.summaryChunk(t.Context(), Run{ConversationID: chat}, sourcePrefix{}, through, "", 1024)
	if err != nil || len(request.Messages) < 2 {
		t.Fatal(err)
	}
	if strings.Contains(request.Messages[1].Content, "PRIVATE-RESULT") || !strings.Contains(request.Messages[1].Content, "PUBLIC-CONFIRMED") {
		t.Fatal("public summary did not use audience projection")
	}
}

func TestMessageKindsAreExplicitAndUnknownKindsFail(t *testing.T) {
	cases := map[messageKind]ModelMessage{
		messageUser: {Role: "user"}, messageAssistant: {Role: "assistant"}, messageTool: {Role: "tool"},
		messageDelivery:  {Role: "assistant", Channel: &ChannelMessageSource{Purpose: "delivery"}},
		messageNotice:    {Role: "assistant", Channel: &ChannelMessageSource{Purpose: "command"}},
		messagePublished: {Role: "assistant", Channel: &ChannelMessageSource{Purpose: "published"}},
		messageCandidate: {Kind: messageCandidate, Role: "assistant"},
	}
	for expected, m := range cases {
		got, err := storedMessageKind(m)
		if err != nil || got != expected {
			t.Fatalf("kind %s: %s %v", expected, got, err)
		}
	}
	if _, err := storedMessageKind(ModelMessage{Kind: "future-unregistered", Role: "assistant"}); !errors.Is(err, ErrStoreFormat) {
		t.Fatal("unknown stored kind was accepted", err)
	}
}

func TestNewConversationAtomicRoutingAndConcurrentIdempotency(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	original := conversation(t, s)
	const request = "platform-message-id"
	var wg sync.WaitGroup
	results := make(chan Conversation, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.Store.CreateActiveConversation(t.Context(), LocalSpaceID, "", s.Config.Workspace, request)
			results <- c
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for c := range results {
		if c.Title != "新对话" || c.ID == "" || id != "" && id != c.ID {
			t.Fatal("duplicate creation", c)
		}
		id = c.ID
	}
	space, err := s.Store.GetSpace(t.Context(), LocalSpaceID)
	if err != nil || space.ActiveConversationID != id || space.DefaultConversationID != original {
		t.Fatal("routing/default not preserved", space, err)
	}
	if _, err = s.Store.CreateActiveConversation(t.Context(), LocalSpaceID, "different", s.Config.Workspace, request); !errors.Is(err, ErrRequestKey) {
		t.Fatal("idempotency conflict accepted", err)
	}
	if err = s.Store.SetSpaceActiveConversation(t.Context(), LocalSpaceID, original); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.CreateActiveConversation(t.Context(), LocalSpaceID, "", s.Config.Workspace, request); err != nil {
		t.Fatal(err)
	}
	space, _ = s.Store.GetSpace(t.Context(), LocalSpaceID)
	if space.ActiveConversationID != original {
		t.Fatal("late duplicate changed newer selection")
	}
}

func TestChannelReceiptIsSeparateAndStillReadable(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(s)
	target := ChannelTarget{Source: SpaceSourceLark, ApplicationID: "fixture-app", ChatID: "fixture-chat"}
	if _, err := tasks.PairChannel(t.Context(), target, "private", "fixture"); err != nil {
		t.Fatal(err)
	}
	in, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, MessageID: "1", Purpose: "request"}, "fixture input")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = tasks.RecordChannelDelivery(t.Context(), result.Run.ID, "sent-1", "confirmed public reply", "sent"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE conversation_id=$1 AND body_json#>>'{Channel,purpose}'='delivery'`, in.ConversationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("receipt written into model messages", count, err)
	}
	messages, err := tasks.Messages(t.Context(), in.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, m := range messages {
		if m.Kind == messageDelivery {
			count++
			if m.Content != "confirmed public reply" || !m.Public {
				t.Fatal("public receipt changed")
			}
		}
	}
	if count != 1 {
		t.Fatal("receipt missing or duplicated in history", count)
	}
}
