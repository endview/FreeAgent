package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// This fixture uses only an in-process transport and an isolated PG Owner.
// No Telegram API, production Bot or channel process is contacted.
func experienceChannelFixture(t *testing.T, model ModelClient) (*TaskService, *channelSession, ChannelTarget, *[]string) {
	t.Helper()
	core, _, _ := testHarness(t, model)
	installSummaryFixture(t, core)
	tasks := NewTaskService(core)
	core.Store.telegramIdentity("1")
	target := ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectCapabilities(t.Context(), space.ActiveConversationID, CapabilitySelection{Skills: []string{"experience-summary"}}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetPersonPermission(t.Context(), "telegram:user:7", "Synthetic user", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	texts := []string{}
	send := func(_ context.Context, got ChannelTarget, _, text string, buttons []channelButton) (string, error) {
		if got != target || len(buttons) != 0 {
			t.Fatalf("experience crossed its recipient or manufactured approval buttons: %+v %+v", got, buttons)
		}
		texts = append(texts, text)
		return fmt.Sprint(len(texts)), nil
	}
	session := newChannelSession(tasks, SpaceSourceTelegram, send)
	// Skip transport pacing only; plain/Run delivery retain their access checks.
	session.send = send
	session.reset("1")
	return tasks, session, target, &texts
}

func experienceChannelSource(t *testing.T, tasks *TaskService, target ChannelTarget) Run {
	t.Helper()
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "source", Purpose: "request"}, "PUBLIC-SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.RecordChannelDelivery(t.Context(), result.Run.ID, "source-reply", "PUBLIC-CONFIRMED-REPLY; effect remains UNKNOWN until checked.", "sent"); err != nil {
		t.Fatal(err)
	}
	return result.Run
}

func TestExperienceTelegramAuthenticatedSameScopePreviewStartAndDuplicate(t *testing.T) {
	modelCalls, summaryCalls := 0, 0
	tasks, session, target, sent := experienceChannelFixture(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		modelCalls++
		if !req.DenyOperations {
			return ModelResponse{Content: "PRIVATE-UNDELIVERED-ANSWER"}, nil
		}
		summaryCalls++
		if len(req.Messages) != 2 || len(req.Tools)+len(req.Memory)+len(req.Extensions) != 0 {
			t.Fatal("TG summary acquired tools, memory or a nested loop", req)
		}
		payload := req.Messages[1].Content
		if !strings.Contains(payload, "PUBLIC-SOURCE") || !strings.Contains(payload, "PUBLIC-CONFIRMED-REPLY") || strings.Contains(payload, "PRIVATE-") || strings.Contains(payload, "/analyze") {
			t.Fatal("TG summary source projection violated", payload)
		}
		return ModelResponse{Content: `{"kind":"draft","title":"UNKNOWN review","body":"Check the actual result.","sources":["synthetic"],"limitations":["Outcome UNKNOWN; do not retry blindly."],"verification":"Inspect the original receipt."}`}, nil
	}))
	source := experienceChannelSource(t, tasks, target)
	chat := source.ConversationID
	// A local GUI turn in the same space remains private until delivered.
	if _, err := tasks.Submit(t.Context(), chat, "private-local", "PRIVATE-GUI-PROMPT"); err != nil {
		t.Fatal(err)
	}
	selection := SourceAnalysisSelection{Skill: "experience-summary", RunIDs: []string{source.ID}}
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection)
	if err != nil || !strings.Contains(preview.Source, "PUBLIC-CONFIRMED-REPLY") || strings.Contains(preview.Source, "PRIVATE-") || !strings.Contains(preview.Source, "source_caveats") {
		t.Fatal("GUI/CLI Core preview did not enforce the same public scope", preview, err)
	}
	next := 0
	command := func(person, text string) {
		t.Helper()
		next++
		if err := session.handle(t.Context(), channelEvent{Target: target, Person: person, Message: fmt.Sprintf("command-%d", next), Kind: "private", Text: text, Trigger: true}); err != nil {
			t.Fatal(err)
		}
	}
	before := modelCalls
	command("telegram:user:7", "/analyze help")
	command("telegram:user:7", "/analyze preview experience-summary "+source.ID)
	if modelCalls != before || !strings.Contains(strings.Join(*sent, "\n"), preview.Digest) {
		t.Fatal("preview called the model or failed to return the Core digest", *sent)
	}
	command("telegram:user:8", "/analyze start "+preview.Digest+" experience-summary "+source.ID)
	if modelCalls != before {
		t.Fatal("unauthenticated person started a summary")
	}
	sourceChat, err := tasks.core.Store.GetConversation(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := tasks.NewConversationInSpace(t.Context(), sourceChat.SpaceID, "same-space private sibling")
	if err != nil {
		t.Fatal(err)
	}
	siblingRun, err := tasks.Submit(t.Context(), sibling.ID, "sibling-source", "PRIVATE-OTHER-CONVERSATION")
	if err != nil {
		t.Fatal(err)
	}
	before = modelCalls
	command("telegram:user:7", "/analyze preview experience-summary "+siblingRun.Run.ID)
	if modelCalls != before || !strings.Contains((*sent)[len(*sent)-1], "不属于当前对话") {
		t.Fatal("same-space other-conversation source accepted")
	}
	other := conversation(t, tasks.core)
	otherRun, err := tasks.Submit(t.Context(), other, "other-source", "PRIVATE-OTHER-SPACE")
	if err != nil {
		t.Fatal(err)
	}
	before = modelCalls
	command("telegram:user:7", "/analyze preview experience-summary "+otherRun.Run.ID)
	if !strings.Contains((*sent)[len(*sent)-1], "不属于当前对话") || modelCalls != before {
		t.Fatal("cross-space source accepted", *sent)
	}
	command("telegram:user:7", "/analyze start "+strings.Repeat("0", 64)+" experience-summary "+source.ID)
	if modelCalls != before || !strings.Contains((*sent)[len(*sent)-1], "重新") {
		t.Fatal("stale digest started a summary")
	}
	start := "/analyze start " + preview.Digest + " experience-summary " + source.ID
	command("telegram:user:7", start)
	summary, err := tasks.Latest(t.Context(), chat)
	if err != nil || summary.Status != "completed" || summaryCalls != 1 {
		t.Fatal(summary, err, summaryCalls)
	}
	snapshot, err := decodeRunSnapshot(summary.ConfigJSON)
	if err != nil || !snapshot.AnalysisOnly || snapshot.Channel == nil || snapshot.Channel.ChannelTarget != target || summary.Permission == nil || summary.Permission.PersonID != "telegram:user:7" || snapshot.ChildBudget != nil {
		t.Fatal("summary lost channel authority or analysis isolation", snapshot, err)
	}
	command("telegram:user:7", start)
	latest, err := tasks.Latest(t.Context(), chat)
	if err != nil || latest.ID != summary.ID || summaryCalls != 1 {
		t.Fatal("repeated reviewed start created another Run/model request", latest, err, summaryCalls)
	}
	skills, err := tasks.Skills(t.Context())
	if err != nil || len(skills) != 1 {
		t.Fatal("summary automatically published a Skill", skills, err)
	}
}

func TestExperienceTelegramDispatchRechecksAdmission(t *testing.T) {
	calls := 0
	tasks, session, target, _ := experienceChannelFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{Content: "synthetic answer"}, nil
	}))
	source := experienceChannelSource(t, tasks, target)
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), source.ConversationID, SourceAnalysisSelection{Skill: "experience-summary", RunIDs: []string{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var pending func(context.Context) (RunResult, error)
	session.setDispatch(func(chat string, call func(context.Context) (RunResult, error)) error {
		if chat != source.ConversationID {
			t.Fatal("summary dispatched to another conversation")
		}
		pending = call
		return nil
	})
	if err := session.handle(t.Context(), channelEvent{Target: target, Person: "telegram:user:7", Message: "start", Kind: "private", Text: "/analyze start " + preview.Digest + " experience-summary " + source.ID, Trigger: true}); err != nil {
		t.Fatal(err)
	}
	if pending == nil {
		t.Fatal("summary bypassed the normal Core dispatcher")
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: false, Members: []string{"telegram:user:7"}})
	before := calls
	result, err := pending(t.Context())
	if err == nil || result.Run.ID != "" || calls != before || !errors.Is(err, ErrPermissionDenied) && !errors.Is(err, ErrPermissionRevoked) {
		t.Fatal("revoked requester reached model dispatch", result, err, calls)
	}
}

func TestExperienceTelegramSelectionSyntax(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"run", "1"}, {"run", "0", "3"}, {"run", "3", "2"}, {"run", "1", "overflow"}, {"run,run"}, {"../run"}} {
		if _, err := sourceAnalysisCommandSelection(append([]string{"experience-summary"}, args...)); err == nil {
			t.Fatal("invalid selection accepted", args)
		}
	}
	selection, err := sourceAnalysisCommandSelection([]string{"experience-summary", "run-one,run-two", "1", "3"})
	if err != nil || len(selection.RunIDs) != 2 || selection.FromMessage != 1 || selection.ThroughMessage != 3 {
		t.Fatal(selection, err)
	}
}
