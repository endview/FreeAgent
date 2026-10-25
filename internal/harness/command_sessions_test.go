package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func backgroundFixture(t *testing.T, timeout int) (*TaskService, string, string, func()) {
	return backgroundFixtureConfig(t, timeout, nil)
}
func backgroundFixtureConfig(t *testing.T, timeout int, configure func(*Config)) (*TaskService, string, string, func()) {
	t.Helper()
	root := t.TempDir()
	work, data := filepath.Join(root, "work"), filepath.Join(root, "data")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(work)
	config.Models = map[string]ModelConfig{"fixture": {Provider: "echo"}}
	config.DefaultModel = "fixture"
	config.Commands.Enabled = true
	config.Tools.CommandRun = true
	config.Commands.TimeoutSecs = timeout
	config.Commands.MaxOutput = 1024
	config.Commands.Rules = []CommandRule{{Program: program, Decision: CommandRuleAllow}}
	if configure != nil {
		configure(&config)
	}
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	tasks, closeTasks, err := OpenTasks(t.Context(), data, Adapters{Model: modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		t.Error("explicit command dispatched a model")
		return ModelResponse{}, fmt.Errorf("unexpected model")
	}), Keys: NewKeyStore(filepath.Join(root, "keys.json"))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTasks)
	chat, err := tasks.NewConversation(t.Context(), "background")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectPermission(t.Context(), chat.ID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	return tasks, chat.ID, work, closeTasks
}
func startBackgroundFixture(t *testing.T, tasks *TaskService, chat, mode string, extra ...string) (RunResult, CommandRecord) {
	t.Helper()
	var args commandArguments
	call := helperCall(t, mode, extra...)
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	args.Background = true
	args.Check = false
	result, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("start %+v %v", result, err)
	}
	var record CommandRecord
	if json.Unmarshal([]byte(result.Answer), &record) != nil || record.ID == "" {
		t.Fatalf("startup %q", result.Answer)
	}
	return result, record
}
func settleBackgroundFixture(t *testing.T, tasks *TaskService, id string) CommandRecord {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, err := tasks.PollCommand(t.Context(), id, 0, 16<<10)
		if err != nil {
			t.Fatal(err)
		}
		if r.terminal() {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background did not settle")
	return CommandRecord{}
}

func TestBackgroundCompletionKeepsStartupAndRunImmutable(t *testing.T) {
	tasks, chat, _, _ := backgroundFixture(t, 5)
	result, initial := startBackgroundFixture(t, tasks, chat, "ok")
	before, attempts, _, err := tasks.Inspect(t.Context(), result.Run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Kind != "tool" {
		t.Fatalf("attempts %+v %v", attempts, err)
	}
	r := settleBackgroundFixture(t, tasks, initial.ID)
	if r.State != "completed" || r.Receipt.ExitCode == nil || *r.Receipt.ExitCode != 0 || r.Receipt.Cleanup != "confirmed" {
		t.Fatalf("completion %+v", r)
	}
	var text strings.Builder
	for _, part := range r.Output {
		text.WriteString(part.Text)
	}
	if !strings.Contains(text.String(), "check really ran") {
		t.Fatal("final output not drained")
	}
	after, afterAttempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(attempts, afterAttempts) {
		t.Fatal("background rewrote immutable startup/Run")
	}
}
func TestBackgroundStopAndGlobalCommandExclusion(t *testing.T) {
	tasks, chat, work, _ := backgroundFixture(t, 10)
	_, r := startBackgroundFixture(t, tasks, chat, "effect-sleep", "effect.txt")
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(work, "effect.txt")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	second, err := tasks.NewConversation(t.Context(), "competing")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectPermission(t.Context(), second.ID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "ok").Arguments, &args)
	args.Check = false
	rejected, err := tasks.StartCommand(t.Context(), second.ID, newID(), mustJSON(args))
	if err != nil || rejected.Run.Status != "failed" {
		t.Fatalf("overlapping foreground accepted %+v %v", rejected, err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := tasks.StopCommand(t.Context(), r.ID)
			if err != nil {
				failures <- err
			} else if record.State != "stopped" {
				failures <- fmt.Errorf("stop %s", record.State)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if tasks.HasCommands() {
		t.Fatal("stopped command still owns slot")
	}
	if raw, err := os.ReadFile(filepath.Join(work, "effect.txt")); err != nil || string(raw) != "already changed" {
		t.Fatal("stop lost evidence of prior effects")
	}
}

func TestBackgroundOwnerCloseStopsDescendants(t *testing.T) {
	tasks, chat, work, closeTasks := backgroundFixture(t, 20)
	_, r := startBackgroundFixture(t, tasks, chat, "parent", "heartbeat.txt")
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(work, "heartbeat.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.PID == 0 {
		t.Fatal("missing diagnostic PID")
	}
	if err := tasks.CloseCommands("owner_shutdown"); err != nil {
		t.Fatal(err)
	}
	r, err := tasks.PollCommand(t.Context(), r.ID, 0, 16384)
	if err != nil || r.State != "stopped" || r.Receipt.Cleanup != "confirmed" {
		t.Fatalf("close %+v %v", r, err)
	}
	before, _ := os.ReadFile(filepath.Join(work, "heartbeat.txt"))
	time.Sleep(100 * time.Millisecond)
	after, _ := os.ReadFile(filepath.Join(work, "heartbeat.txt"))
	if string(before) != string(after) {
		t.Fatal("descendant still writes after confirmed cleanup")
	}
	closeTasks()
}

func TestBackgroundStartupCancellationDoesNotOwnProcessLifetime(t *testing.T) {
	tasks, chat, _, _ := backgroundFixture(t, 10)
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "effect-sleep", "effect.txt").Arguments, &args)
	args.Background, args.Check = true, false
	ctx, cancel := context.WithCancel(t.Context())
	result, err := tasks.StartCommand(ctx, chat, newID(), mustJSON(args))
	if err != nil {
		t.Fatal(err)
	}
	var r CommandRecord
	_ = json.Unmarshal([]byte(result.Answer), &r)
	cancel()
	time.Sleep(100 * time.Millisecond)
	r, err = tasks.PollCommand(t.Context(), r.ID, 0, 16384)
	if err != nil || r.State != "running" {
		t.Fatalf("startup context killed owned process %+v %v", r, err)
	}
	if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCommandManagementFrozenAndCrossConversationStop(t *testing.T) {
	tasks, chat, _, _ := backgroundFixture(t, 10)
	result, r := startBackgroundFixture(t, tasks, chat, "effect-sleep", "effect.txt")
	core, err := tasks.core.forRun(result.Run)
	if err != nil {
		t.Fatal(err)
	}
	runner := commandInvocationTools{base: core.Tools, inv: commandInvocation{core, result.Run.ID, ""}}
	call := makeCall("stop", "command_stop", map[string]any{"id": r.ID})
	prepared, err := runner.Prepare(t.Context(), call)
	if err != nil || prepared.userConfirmation {
		t.Fatalf("same chat %+v %v", prepared, err)
	}
	runner.inv.run = "unrelated"
	prepared, err = runner.Prepare(t.Context(), call)
	if err != nil || !prepared.userConfirmation {
		t.Fatalf("cross chat stop bypassed approval %+v %v", prepared, err)
	}
	core.commandManagementDisabled = true
	if _, err := runner.Prepare(t.Context(), call); err == nil {
		t.Fatal("old snapshot gained management tools")
	}
	if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
}
func TestBackgroundTimeoutNonzeroAndBoundedTail(t *testing.T) {
	for _, mode := range []string{"effect-sleep", "fail", "loud"} {
		t.Run(mode, func(t *testing.T) {
			tasks, chat, _, _ := backgroundFixture(t, 2)
			_, r := startBackgroundFixture(t, tasks, chat, mode, "effect.txt")
			r = settleBackgroundFixture(t, tasks, r.ID)
			want := map[string]string{"effect-sleep": "timed_out", "fail": "failed", "loud": "completed"}[mode]
			if r.State != want {
				t.Fatalf("state %+v", r)
			}
			size := 0
			for _, chunk := range r.Output {
				size += len(chunk.Text)
				if !utf8.ValidString(chunk.Text) {
					t.Fatal("invalid UTF-8")
				}
			}
			if size > 1024 || mode == "loud" && !r.OutputGap {
				t.Fatalf("unbounded tail %+v", r)
			}
		})
	}
}
func TestBackgroundRecoveryMarksProjectionOnlyAndRequiresReview(t *testing.T) {
	tasks, chat, _, _ := backgroundFixture(t, 5)
	result, r := startBackgroundFixture(t, tasks, chat, "ok")
	r = settleBackgroundFixture(t, tasks, r.ID)
	before, attempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	r.State = "running"
	r.Generation = "old-owner"
	r.Reviewed = false
	if err := tasks.core.commands.save(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	restored, err := newCommandSupervisor(t.Context(), tasks.core.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close("test_close")
	tasks.core.commands = restored
	recovered, err := tasks.PollCommand(t.Context(), r.ID, 0, 16<<10)
	if err != nil || recovered.State != "unknown" || recovered.Reason != "owner_lost" {
		t.Fatalf("recovery %+v %v", recovered, err)
	}
	after, newAttempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(attempts, newAttempts) {
		t.Fatal("recovery changed closed Run/startup")
	}
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "ok").Arguments, &args)
	args.Background = true
	args.Check = false
	next, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
	if err != nil || next.Run.Status != "waiting_confirmation" {
		t.Fatalf("Full bypassed UNKNOWN review %+v %v", next, err)
	}
}
func TestCommandTailUTF8AcrossChunksAndGap(t *testing.T) {
	tail := &commandTail{limit: 32}
	writer := commandStream{tail, 0}
	for _, b := range []byte(strings.Repeat("中文", 20)) {
		_, _ = writer.Write([]byte{b})
	}
	writer.finish()
	chunks, first, next, total, gap := tail.snapshot(0, 16)
	if first == 0 || next <= first || total != 120 || !gap {
		t.Fatalf("cursor first=%d next=%d total=%d gap=%t", first, next, total, gap)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk.Text) {
			t.Fatal("split UTF-8")
		}
	}
}
