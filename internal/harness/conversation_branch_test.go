package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMessageBranchCutsFutureHistoryAndCopiesAttachments(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	ref, err := tasks.UploadAttachment(t.Context(), chat, "note.txt", strings.NewReader("immutable attachment content"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitWithOptions(t.Context(), chat, "attachment", "original selected input", RunOptions{Attachments: []string{ref.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "future", "future secret must be absent"); err != nil {
		t.Fatal(err)
	}
	messages, _ := tasks.Messages(t.Context(), chat)
	var cut int64
	for _, m := range messages {
		if m.RunID == result.Run.ID && m.Role == "user" {
			cut = m.ID
		}
	}
	text := "edited replacement"
	command := BranchCommand{RequestKey: "branch-once", MessageID: cut, Mode: "rerun", Prompt: &text}
	branch, err := tasks.BranchConversation(t.Context(), chat, command)
	if err != nil {
		t.Fatal(err)
	}
	if branch.Prompt != text || len(branch.Attachments) != 1 || branch.Attachments[0].ID == ref.ID {
		t.Fatal("branch input or attachment ownership lost")
	}
	if _, raw, err := tasks.Attachment(t.Context(), branch.Conversation.ID, branch.Attachments[0].ID); err != nil || string(raw) != "immutable attachment content" {
		t.Fatal("branch attachment failed", err)
	}
	if _, _, err := tasks.Attachment(t.Context(), chat, branch.Attachments[0].ID); !errors.Is(err, ErrAttachment) {
		t.Fatal("branch attachment crossed conversation ownership")
	}
	copy, err := tasks.BranchConversation(t.Context(), chat, command)
	if err != nil || copy.Conversation.ID != branch.Conversation.ID || !copy.Duplicate {
		t.Fatal("branch request duplicated", err)
	}
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "future secret") || m.Content == "original selected input" {
				t.Error("branch included replaced/future messages")
			}
		}
		return ModelResponse{Content: "branch complete"}, nil
	})
	if _, err := tasks.SubmitWithOptions(t.Context(), branch.Conversation.ID, "branch-start", branch.Prompt, RunOptions{Attachments: []string{branch.Attachments[0].ID}}); err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Chat(t.Context(), branch.Conversation.ID, "")
	if err != nil || view.Branch == nil || len(view.Background) == 0 {
		t.Fatal("branch provenance or copied prefix missing", err)
	}
	after, _ := tasks.Messages(t.Context(), chat)
	if len(messages) != len(after) {
		t.Fatal("source conversation changed")
	}
}

func TestBranchRefusesPendingAndUNKNOWNAndDanglingToolCuts(t *testing.T) {
	core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{}, errors.New("lost response")
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	_, _ = tasks.Submit(t.Context(), chat, "unknown", "input")
	messages, _ := tasks.Messages(t.Context(), chat)
	if _, err := tasks.BranchConversation(t.Context(), chat, BranchCommand{RequestKey: "blocked", MessageID: messages[0].ID, Mode: "rerun"}); !errors.Is(err, ErrActiveRun) {
		t.Fatal("branch bypassed UNKNOWN", err)
	}
}
