package harness

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func attachmentFixture(t *testing.T, configure func(*Config), model ModelClient) (*TaskService, string, string, string) {
	t.Helper()
	root := t.TempDir()
	data, work := filepath.Join(root, "data"), filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(work)
	config.Models = map[string]ModelConfig{"echo": {Provider: "echo"}}
	config.DefaultModel = "echo"
	if configure != nil {
		configure(&config)
	}
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err := OpenTasks(t.Context(), data, Adapters{Model: model, Keys: NewKeyStore(filepath.Join(root, "keys.json"))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAll)
	chat, err := tasks.NewConversation(t.Context(), "attachments")
	if err != nil {
		t.Fatal(err)
	}
	return tasks, chat.ID, data, work
}
func testPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 4, 3))
	im.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAttachmentsNoneInputIdempotencyAndBackup(t *testing.T) {
	var requests []ModelRequest
	tasks, chat, data, _ := attachmentFixture(t, nil, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "understood"}, nil
	}))
	ctx := t.Context()
	ref, err := tasks.UploadAttachment(ctx, chat, "../../说明 & !.md", strings.NewReader("attachment fact 你好"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "说明 & !.md" {
		t.Fatal(ref)
	}
	if selected, _ := tasks.SelectedPermission(ctx, chat); selected.Mode != PermissionNone {
		t.Fatal(selected)
	}
	result, err := tasks.SubmitWithOptions(ctx, chat, "input", "read this", RunOptions{Attachments: []string{ref.ID}})
	if err != nil || result.Run.Status != "completed" || len(requests) != 1 || !controlOnlyDefinitions(requests[0].Tools) || !requests[0].DenyOperations {
		t.Fatalf("%+v %v", result, err)
	}
	if !strings.Contains(string(mustJSON(requests[0])), "attachment fact") {
		t.Fatal("attachment was not in the actual None request")
	}
	if duplicate, err := tasks.SubmitWithOptions(ctx, chat, "input", "read this", RunOptions{Attachments: []string{ref.ID}}); err != nil || !duplicate.Duplicate || len(requests) != 1 {
		t.Fatalf("duplicate %+v %v", duplicate, err)
	}
	if _, err := tasks.Submit(ctx, chat, "input", "read this"); !errors.Is(err, ErrRequestKey) {
		t.Fatalf("changed attachment input reused key: %v", err)
	}
	other, err := tasks.NewConversation(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Attachment(ctx, other.ID, ref.ID); !errors.Is(err, ErrAttachment) {
		t.Fatal("cross-chat download", err)
	}
	if _, err := tasks.SubmitWithOptions(ctx, other.ID, "cross", "read", RunOptions{Attachments: []string{ref.ID}}); !errors.Is(err, ErrAttachment) {
		t.Fatal("cross-chat model input", err)
	}
	view, err := tasks.Chat(ctx, chat, "")
	if err != nil || len(view.Turns[0].Messages[0].Attachments) != 1 {
		t.Fatalf("chat lost ref %+v %v", view, err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	if err := tasks.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	raw, err := readAttachmentBlob(restored, ref)
	if err != nil || string(raw) != "attachment fact 你好" {
		t.Fatalf("restore %q %v", raw, err)
	}
	if err := os.WriteFile(filepath.Join(backup, "attachments", ref.ID), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, backup); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "attachments")); len(entries) != 1 {
		t.Fatal(entries)
	}
}

func TestAttachmentsPendingIdentityLimitsAndBrokenStreams(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, func(c *Config) {
		c.Attachments.MaxInputBytes = 8
		c.Attachments.MaxBatchBytes = 12
		c.Attachments.QuotaBytes = 14
		c.Attachments.MaxPixels = 4
	}, nil)
	a, b := WithAttachmentIdentity(t.Context(), "session-a"), WithAttachmentIdentity(t.Context(), "session-b")
	ref, err := tasks.UploadAttachment(a, chat, "empty.txt", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Attachment(b, chat, ref.ID); err == nil {
		t.Fatal("pending upload crossed a client identity")
	}
	if _, err := tasks.UploadAttachment(a, chat, "too-big.txt", strings.NewReader("012345678")); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err := tasks.UploadAttachment(a, chat, "invalid.txt", bytes.NewReader([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8")
	}
	if _, err := tasks.UploadAttachment(a, chat, "active.html", strings.NewReader("<script>")); err != nil {
		t.Fatal("HTML should be inert text input", err)
	}
	if _, err := tasks.UploadAttachment(a, chat, "archive.zip", bytes.NewReader([]byte("PK"))); err == nil {
		t.Fatal("archive accepted as input")
	}
	if _, err := tasks.UploadAttachment(a, chat, "interrupted.txt", io.MultiReader(strings.NewReader("a"), errorReader{})); err == nil {
		t.Fatal("interrupted upload accepted")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestAttachmentsRejectImplicitCollaborationAndUseLiveQuota(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, nil, nil)
	ref, err := tasks.UploadAttachment(t.Context(), chat, "one.txt", strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultCollaborationSettings()
	settings.Mode = "auto"
	_, err = tasks.SubmitWithOptions(t.Context(), chat, "collaboration", "read", RunOptions{Attachments: []string{ref.ID}, Collaboration: &settings})
	if err == nil || !strings.Contains(err.Error(), "single-agent") {
		t.Fatalf("collaboration was silently changed: %v", err)
	}
	copy := tasks.core.runCopy()
	copy.Config.Attachments = AttachmentPolicy{}
	next := tasks.Config()
	next.Attachments.QuotaBytes = ref.Bytes
	tasks.core.setConfig(next)
	if _, err := NewTaskService(&copy).createAttachment(t.Context(), chat, "result.md", strings.NewReader("result"), "file_export", "run", true); err == nil {
		t.Fatal("frozen default quota bypassed a lowered live quota")
	}
}

func TestAttachmentImagesResendBudgetAndNoBase64Persistence(t *testing.T) {
	var requests []ModelRequest
	var omissions []Event
	vision := VisionCapabilities{true, true, true, 1 << 20, 100, 1, 4 << 20}
	tasks, chat, data, _ := attachmentFixture(t, func(c *Config) { m := c.Models["echo"]; m.Capabilities.Vision = vision; c.Models["echo"] = m }, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "image seen"}, nil
	}))
	tasks.SetEvents(func(e Event) {
		if e.Kind == "attachment_omitted" {
			omissions = append(omissions, e)
		}
	})
	first, err := tasks.UploadAttachment(t.Context(), chat, "图.png", bytes.NewReader(testPNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SubmitWithOptions(t.Context(), chat, "one", "look", RunOptions{Attachments: []string{first.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "two", "what color?"); err != nil {
		t.Fatal(err)
	}
	second, err := tasks.UploadAttachment(t.Context(), chat, "new.png", bytes.NewReader(testPNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SubmitWithOptions(t.Context(), chat, "three", "new image", RunOptions{Attachments: []string{second.ID}}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatal(len(requests))
	}
	for _, r := range requests {
		count := 0
		for _, m := range r.Messages {
			count += len(m.Images)
		}
		if count != 1 {
			t.Fatal("image budget/resend", count)
		}
	}
	if len(omissions) != 1 {
		t.Fatal("omission was not reported", omissions)
	}
	var requestJSON string
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT request_json FROM fa.attempts WHERE kind='model' LIMIT 1`).Scan(&requestJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(requestJSON, "iVBOR") || strings.Contains(requestJSON, "base64") {
		t.Fatal("binary image persisted in Attempt")
	}
	if _, err := readAttachmentBlob(data, first); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicHistoryOmitsOldImageToFitNewLargeImage(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, nil, nil)
	old, err := tasks.createAttachment(t.Context(), chat, "old.png", bytes.NewReader(testPNG(t)), "upload", "old", true)
	if err != nil {
		t.Fatal(err)
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2001, 1))); err != nil {
		t.Fatal(err)
	}
	large, err := tasks.createAttachment(t.Context(), chat, "large.png", &imageBytes, "upload", "large", true)
	if err != nil {
		t.Fatal(err)
	}
	request := ModelRequest{}
	for range 20 {
		request.Messages = append(request.Messages, ModelMessage{Role: "user", Attachments: []AttachmentRef{old}})
	}
	request.Messages = append(request.Messages, ModelMessage{Role: "user", Attachments: []AttachmentRef{large}})
	core := tasks.core.runCopy()
	core.inputAttachments = []AttachmentRef{large}
	model := ModelConfig{Provider: "anthropic", Capabilities: ModelCapabilities{Vision: VisionCapabilities{true, true, true, 1 << 20, 8000, 32, 4 << 20}}}
	prepared, err := core.prepareAttachmentRequest(t.Context(), Run{ID: "image-budget", ConversationID: chat}, model, request)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range prepared.Messages {
		count += len(message.Images)
	}
	if count != 20 || len(prepared.Messages[0].Images) != 0 || len(prepared.Messages[20].Images) != 1 {
		t.Fatal("new large image rejected or older image not omitted", count)
	}
	if err := validateAnthropicImages(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentReplyNoneIdempotencyAndDeletion(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, nil, nil)
	result, err := tasks.Submit(t.Context(), chat, "r", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var refs [2]AttachmentRef
	var errs [2]error
	var wg sync.WaitGroup
	for i := range refs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			refs[i], errs[i] = tasks.SaveReplyAttachment(t.Context(), chat, result.Run.ID, "save")
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || refs[0].ID != refs[1].ID {
		t.Fatalf("reply replay %+v %v", refs, errs)
	}
	_, raw, err := tasks.Attachment(t.Context(), chat, refs[0].ID)
	if err != nil || string(raw) != result.Answer {
		t.Fatalf("reply bytes %q %v", raw, err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlDeleteAttachment, Arguments: mustJSON(attachmentDeleteArguments{chat, refs[0].ID, refs[0].SHA256}), Authorization: ControlUserDirect, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := control.IssueInteractiveCapability(t.Context(), chat, "test")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, chat, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: chat, Principal: "test", Capability: cap, Code: confirmation.Code}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Attachment(t.Context(), chat, refs[0].ID); err == nil {
		t.Fatal("deleted attachment downloadable")
	}
}

func TestAttachmentExportApprovalHashAndImmutableResult(t *testing.T) {
	var step int
	tasks, chat, _, work := attachmentFixture(t, nil, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		step++
		if step == 1 {
			return ModelResponse{ToolCalls: []ToolCall{{ID: "export", Name: "file_export", Arguments: mustJSON(map[string]string{"path": "result.bin"})}}}, nil
		}
		return ModelResponse{Content: "result ready"}, nil
	}))
	ctx := t.Context()
	if err := tasks.SetSpacePermission(ctx, LocalSpaceID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, "result.bin")
	if err := os.WriteFile(path, []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(ctx, chat, "export", "export result")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("%+v %v", result, err)
	}
	preview, err := tasks.Preview(ctx, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0, 9, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Confirm(ctx, result.Run.ID, preview, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatalf("changed export approved %v", err)
	}
	preview, err = tasks.Preview(ctx, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err = tasks.Confirm(ctx, result.Run.ID, preview, true)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("%+v %v", result, err)
	}
	refs, err := tasks.Attachments(ctx, chat)
	if err != nil || len(refs) != 1 {
		t.Fatalf("%+v %v", refs, err)
	}
	if err := os.WriteFile(path, []byte("new source"), 0600); err != nil {
		t.Fatal(err)
	}
	_, raw, err := tasks.Attachment(ctx, chat, refs[0].Ref.ID)
	if err != nil || !bytes.Equal(raw, []byte{0, 9, 2, 3}) {
		t.Fatalf("export changed with source %v %v", raw, err)
	}
}

func TestAttachmentUploadRequestRetryAndHardlinkDenial(t *testing.T) {
	tasks, chat, data, _ := attachmentFixture(t, nil, nil)
	first, err := tasks.UploadAttachmentRequest(t.Context(), chat, "a.txt", "input-a", strings.NewReader("a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := tasks.UploadAttachmentRequest(t.Context(), chat, "a.txt", "input-a", strings.NewReader("a"))
	if err != nil || first != again {
		t.Fatalf("retry %+v %v", again, err)
	}
	if _, err := tasks.UploadAttachmentRequest(t.Context(), chat, "a.txt", "input-a", strings.NewReader("b")); !errors.Is(err, ErrRequestKey) {
		t.Fatal("changed upload reused request", err)
	}
	path := filepath.Join(data, "attachments", first.ID)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Attachment(t.Context(), chat, first.ID); !errors.Is(err, ErrAttachment) {
		t.Fatal("hardlinked blob accepted", err)
	}
}
