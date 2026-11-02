package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParallelConversationsBoundSlotsCancelAndFreezeGlobalCommit(t *testing.T) {
	entered := make(chan string, 2)
	releaseB := make(chan struct{})
	var eventsMu sync.Mutex
	var events []Event
	service, _, _ := testHarness(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		text := req.Messages[len(req.Messages)-1].Content
		entered <- text
		emit(text)
		if text == "A" {
			<-ctx.Done()
			return ModelResponse{}, ctx.Err()
		}
		select {
		case <-releaseB:
			return ModelResponse{Content: "B settled"}, nil
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}))
	tasks := NewTaskService(service)
	tasks.SetEvents(func(e Event) { eventsMu.Lock(); events = append(events, e); eventsMu.Unlock() })
	a, b, c := conversation(t, service), conversation(t, service), conversation(t, service)
	type outcome struct {
		r   RunResult
		err error
	}
	doneA, doneB := make(chan outcome, 1), make(chan outcome, 1)
	go func() { r, e := tasks.Submit(t.Context(), a, "A", "A"); doneA <- outcome{r, e} }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("A did not enter")
	}
	go func() { r, e := tasks.Submit(t.Context(), b, "B", "B"); doneB <- outcome{r, e} }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("B did not overlap")
	}
	start := time.Now()
	for _, chat := range []string{a, c} {
		if _, err := tasks.Submit(t.Context(), chat, "noqueue", "third"); !errors.Is(err, ErrActiveRun) {
			t.Fatal(err)
		}
	}
	if time.Since(start) > time.Second {
		t.Fatal("busy rejection waited for model")
	}
	if _, err := tasks.ConfigureRuntimePolicy(t.Context(), "", func(*Config) error { return nil }); !errors.Is(err, ErrActiveRun) || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Fatalf("global commit=%v", err)
	}
	if err := service.Store.Backup(t.Context(), filepath.Join(t.TempDir(), "backup")); !errors.Is(err, ErrActiveRun) {
		t.Fatal(err)
	}
	if !tasks.CancelConversation(a) {
		t.Fatal("A not cancellable")
	}
	select {
	case out := <-doneA:
		if !errors.Is(out.err, ErrUnknownResult) || out.r.Run.Status != "unknown" {
			t.Fatalf("A=%+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A did not stop")
	}
	select {
	case out := <-doneB:
		t.Fatalf("stopping A stopped B: %+v", out)
	default:
	}
	close(releaseB)
	select {
	case out := <-doneB:
		if out.err != nil || out.r.Run.Status != "completed" {
			t.Fatalf("B=%+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B stuck")
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	for _, e := range events {
		if e.Kind == "model_text" && (e.Message == "A" && e.ConversationID != a || e.Message == "B" && e.ConversationID != b || e.RunID == "") {
			t.Fatalf("event crossed conversations: %+v", e)
		}
	}
	if len(tasks.ActiveConversations()) != 0 {
		t.Fatal("execution slots leaked")
	}
}
func TestParallelFileMutationUsesOnePublicationLockAcrossInstances(t *testing.T) {
	_, one, work := testHarness(t, EchoModel{})
	two, err := NewFileTools(work, one.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	path := filepath.Join(work, "shared.txt")
	os.WriteFile(path, []byte("old"), 0600)
	prepare := func(tool *FileTools, name, next string) PreparedTool {
		p, err := tool.Prepare(t.Context(), makeCall(next, "file_edit", map[string]string{"path": name, "expected_sha256": contentHash([]byte("old")), "old_text": "old", "new_text": next}))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, second := prepare(one, "shared.txt", "one"), prepare(two, "./shared.txt", "two")
	fileMutationMu.Lock()
	done := make(chan struct {
		r ToolResult
		e error
	}, 2)
	for _, p := range []PreparedTool{first, second} {
		go func(p PreparedTool) {
			r, e := p.Execute(t.Context())
			done <- struct {
				r ToolResult
				e error
			}{r, e}
		}(p)
	}
	// Both previews refer to the same old bytes; neither can publish until the
	// common final-check lock is released. Exactly one may use those bytes.
	fileMutationMu.Unlock()
	success := 0
	for range 2 {
		out := <-done
		for _, r := range out.r.Receipts {
			if out.e == nil && r.Status == "applied" {
				success++
			}
		}
	}
	if success != 1 {
		t.Fatalf("old hash published %d times", success)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "one" && string(raw) != "two" {
		t.Fatalf("corrupt result %q", raw)
	}
}
func TestChannelLocalContinuationPublishesPromptWithoutPrivateToolHistory(t *testing.T) {
	var last ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		last = r
		return ModelResponse{Content: "public answer"}, nil
	}))
	tasks := NewTaskService(core)
	target := ChannelTarget{SpaceSourceTelegram, "bot", "chat"}
	space, err := tasks.PairChannel(t.Context(), target, "private", "external")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), space.DefaultConversationID, "local-input", "local public prompt")
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	seen := false
	for _, m := range last.Messages {
		if m.Role == "user" && m.Content == "local public prompt" {
			seen = true
		}
	}
	if !seen || channelOriginFromJSON(result.Run.ConfigJSON) == nil {
		t.Fatalf("prompt/origin missing: %+v", last.Messages)
	}
}

// TestParallelRealCommandIntervals records real OS process timestamps. Full
// deliberately exercises the host path; it is not sandbox isolation evidence.
func TestParallelRealCommandAndModelIntervals(t *testing.T) {
	tasks, a, _, _ := backgroundFixture(t, 5)
	b, err := tasks.NewConversation(t.Context(), "parallel real command")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectPermission(t.Context(), b.ID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	var args commandArguments
	json.Unmarshal(helperCall(t, "overlap").Arguments, &args)
	run := func(chat string) (int64, int64, error) {
		result, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
		if err != nil {
			return 0, 0, err
		}
		if result.Run.Status != "completed" || len(result.Evidence.Commands) != 1 || result.Evidence.Commands[0].Status != "completed" {
			return 0, 0, fmt.Errorf("command not completed: status=%s reason=%s", result.Run.Status, result.Run.StopReason)
		}
		attempts, err := tasks.core.Store.ListAttempts(t.Context(), result.Run.ID)
		if err != nil {
			return 0, 0, err
		}
		for _, attempt := range attempts {
			if attempt.Kind == "tool" {
				var output ToolResult
				json.Unmarshal([]byte(attempt.ResponseJSON), &output)
				var start, end int64
				var body struct {
					Output string `json:"output"`
				}
				json.Unmarshal([]byte(output.Content), &body)
				_, err := fmt.Sscan(body.Output, &start, &end)
				return start, end, err
			}
		}
		return 0, 0, fmt.Errorf("missing command output")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	serialAt := time.Now()
	_, _, err = run(a)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = run(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	serial := time.Since(serialAt)
	type interval struct {
		start, end int64
		err        error
	}
	done := make(chan interval, 2)
	parallelAt := time.Now()
	tasks.core.Model = modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		start := time.Now().UnixNano()
		select {
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
			return ModelResponse{Content: fmt.Sprintf("%d %d", start, time.Now().UnixNano())}, nil
		}
	})
	go func() { start, end, err := run(a); done <- interval{start, end, err} }()
	go func() {
		result, err := tasks.Submit(t.Context(), b.ID, newID(), "bounded mock model workload")
		var start, end int64
		if err == nil {
			_, err = fmt.Sscan(result.Answer, &start, &end)
		}
		done <- interval{start, end, err}
	}()
	first, second := <-done, <-done
	if first.err != nil || second.err != nil {
		t.Fatal(first.err, second.err)
	}
	parallel := time.Since(parallelAt)
	overlap := min(first.end, second.end) - max(first.start, second.start)
	if overlap <= 0 {
		t.Fatalf("real OS command and bounded mock model did not overlap: %+v %+v", first, second)
	}
	runtime.ReadMemStats(&after)
	t.Logf("os=%s arch=%s serial_ms=%d parallel_ms=%d overlap_ms=%d alloc_before=%d alloc_after=%d max_foreground=%d model_inflight=2 command_quota=1 workload=real_process_plus_mock_model output_limit=%d host_escape_hatch=true", runtime.GOOS, runtime.GOARCH, serial.Milliseconds(), parallel.Milliseconds(), overlap/int64(time.Millisecond), before.Alloc, after.Alloc, MaxForegroundRuns, tasks.Config().Commands.MaxOutput)
}

func TestMovePublishesConfirmedLocalTextWithoutToolBodiesOrDuplicates(t *testing.T) {
	var last ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		last = r
		return ModelResponse{Content: "original public answer"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "before-move", "original local text")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertMessage(t.Context(), tx, result.Run, ModelMessage{Role: "tool", Content: "PRIVATE_TOOL_BYTES"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	target := ChannelTarget{SpaceSourceTelegram, "bot", "chat"}
	space, err := tasks.PairChannel(t.Context(), target, "group", "published")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.MoveConversation(t.Context(), chat, space.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "after-move", "now answer using published text"); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range last.Messages {
		if strings.Contains(m.Content, "original local text") {
			seen = true
		}
		if strings.Contains(m.Content, "PRIVATE_TOOL_BYTES") {
			t.Fatal("private tool body entered published context")
		}
	}
	if !seen {
		t.Fatal("confirmed local text was lost after move")
	}
	for _, id := range []string{LocalSpaceID, space.ID} {
		if err := tasks.MoveConversation(t.Context(), chat, id); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM messages WHERE conversation_id=? AND request_key LIKE 'channel/publication/%'`, chat).Scan(&count); err != nil || count != 2 {
		t.Fatalf("publication mirrors=%d %v", count, err)
	}
}
func TestCollaborationCancelWaitingDoesNotWaitForOtherConversation(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	id := createCollaboration(t, collab, plan)
	entered, release := make(chan struct{}), make(chan struct{})
	core.Model = modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		case <-release:
			return ModelResponse{Content: "B survived"}, nil
		}
	})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() {
		off := DefaultCollaborationSettings()
		off.Mode = "off"
		_, err := tasks.SubmitWithOptions(t.Context(), chat, "B", "B", RunOptions{Collaboration: &off})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("B did not start")
	}
	if err := collab.Control(t.Context(), id, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	report, err := collab.Inspect(t.Context(), id)
	if err != nil || report.Parent.Status != "abandoned" {
		t.Fatalf("inactive A blocked behind B: %s %v", report.Parent.Status, err)
	}
	select {
	case err := <-done:
		t.Fatal("B unexpectedly stopped", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
