package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func serviceFixture(t *testing.T, maxSeconds int) (*TaskService, string, string) {
	t.Helper()
	tasks, chat, work, _ := backgroundFixtureConfig(t, 20, func(c *Config) {
		c.Commands.Services = ServicePolicy{Enabled: true, DefaultSeconds: 10, MaxTotalSeconds: maxSeconds, AllowLocalListen: true}
		if maxSeconds < 10 {
			c.Commands.Services.DefaultSeconds = maxSeconds
		}
	})
	return tasks, chat, work
}
func startService(t *testing.T, tasks *TaskService, chat, file, extra string, duration int, ready string, readyTimeout int) (RunResult, CommandRecord) {
	t.Helper()
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "service", file, extra).Arguments, &args)
	args.Check = false
	args.Mode = "service"
	args.DurationSeconds = duration
	args.ReadyText = ready
	args.ReadyTimeout = readyTimeout
	result, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("service start %+v %v", result, err)
	}
	var r CommandRecord
	if json.Unmarshal([]byte(result.Answer), &r) != nil || r.Mode != "service" {
		t.Fatalf("startup %+v", result)
	}
	return result, r
}
func waitReady(t *testing.T, tasks *TaskService, id string) CommandRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := tasks.PollCommand(t.Context(), id, 0, 16384)
		if err != nil {
			t.Fatal(err)
		}
		if r.Ready {
			return r
		}
		if r.terminal() {
			t.Fatalf("early end %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ready text not observed")
	return CommandRecord{}
}
func finishService(t *testing.T, tasks *TaskService, id string) CommandRecord {
	t.Helper()
	s := tasks.core.commands
	s.mu.Lock()
	p := s.active[id]
	s.mu.Unlock()
	if p != nil {
		select {
		case <-p.done:
		case <-time.After(14 * time.Second):
			t.Fatal("service failed to settle")
		}
	}
	r, err := tasks.PollCommand(t.Context(), id, 0, 16384)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestServiceHTTPParallelCommandQuotaAndHostGate(t *testing.T) {
	tasks, chat, work := serviceFixture(t, 30)
	parent, a := startService(t, tasks, chat, "one.addr", "", 20, "SERVICE_READY", 3)
	_, b := startService(t, tasks, chat, "two.addr", "", 20, "SERVICE_READY", 3)
	a = waitReady(t, tasks, a.ID)
	b = waitReady(t, tasks, b.ID)
	before, attempts, _, _ := tasks.Inspect(t.Context(), parent.Run.ID)
	address, _ := os.ReadFile(filepath.Join(work, "one.addr"))
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(string(address))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(raw) != "SERVICE_OK" {
		t.Fatalf("HTTP %s", raw)
	}
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "http-client", string(address)).Arguments, &args)
	result, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
	if err != nil || result.Run.Status != "completed" || !strings.Contains(result.Answer, "SERVICE_OK") || len(result.Evidence.ChecksPassed) != 1 {
		t.Fatalf("parallel check %+v %v", result, err)
	}
	_, c := startBackgroundFixture(t, tasks, chat, "effect-sleep", "effect.txt")
	for _, mode := range []string{"background", "service"} {
		args.Check = false
		args.Background = true
		args.Mode = mode
		args.Args = helperArguments(t, "ok")
		if mode == "service" {
			args.DurationSeconds = 10
		}
		rejected, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
		if err != nil || rejected.Run.Status != "failed" {
			t.Fatalf("quota bypass %+v %v", rejected, err)
		}
	}
	var wg sync.WaitGroup
	for _, id := range []string{a.ID, b.ID, c.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := tasks.StopCommand(t.Context(), id)
			if err != nil || r.State != "stopped" {
				t.Errorf("stop %+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	after, afterAttempts, _, _ := tasks.Inspect(t.Context(), parent.Run.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(attempts, afterAttempts) {
		t.Fatal("service changed startup Run/Attempt")
	}
	if tasks.HasCommands() {
		t.Fatal("confirmed cleanup did not release quota")
	}
}
func helperArguments(t *testing.T, mode string) []string {
	var a commandArguments
	_ = json.Unmarshal(helperCall(t, mode).Arguments, &a)
	return a.Args
}

func TestServiceFiniteExtensionAndExpiryWithoutPolling(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 6)
	_, r := startService(t, tasks, chat, "addr", "", 2, "", 0)
	deadline, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	request := CommandExtension{ID: r.ID, Revision: r.Revision, ExpiresAt: deadline.Add(2 * time.Second).Format(time.RFC3339Nano), RequestKey: "exact-extension"}
	next, err := tasks.ExtendCommand(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := tasks.ExtendCommand(t.Context(), request)
	if err != nil || duplicate.Revision != next.Revision {
		t.Fatalf("dedup %+v %v", duplicate, err)
	}
	bad := request
	bad.RequestKey = "stale"
	if _, err := tasks.ExtendCommand(t.Context(), bad); err == nil {
		t.Fatal("stale extension accepted")
	}
	bad = request
	bad.Revision = next.Revision
	bad.ExpiresAt = deadline.Add(10 * time.Second).Format(time.RFC3339Nano)
	if _, err := tasks.ExtendCommand(t.Context(), bad); err == nil {
		t.Fatal("request key/total limit bypass")
	}
	r = finishService(t, tasks, r.ID)
	if r.State != "timed_out" || r.Reason != "deadline" || r.Receipt.Cleanup != "confirmed" {
		t.Fatalf("expiry %+v", r)
	}
	bad.RequestKey = "after-expiry"
	if _, err := tasks.ExtendCommand(t.Context(), bad); err == nil {
		t.Fatal("expired service revived")
	}
	if _, err := tasks.ExtendCommand(t.Context(), request); err != nil {
		t.Fatal("lost accepted key after termination", err)
	}
}

func TestServiceReadinessTimeoutAndBoundedLogs(t *testing.T) {
	for _, mode := range []string{"no-ready", "loud", "steady"} {
		t.Run(mode, func(t *testing.T) {
			tasks, chat, _ := serviceFixture(t, 20)
			_, r := startService(t, tasks, chat, "addr", mode, 10, "SERVICE_READY", 1)
			if mode == "no-ready" {
				r = finishService(t, tasks, r.ID)
				if r.Reason != "readiness_timeout" || r.Ready {
					t.Fatalf("readiness %+v", r)
				}
				return
			}
			r = waitReady(t, tasks, r.ID)
			deadline := time.Now().Add(5 * time.Second)
			minimum := int64(6 << 20)
			if mode == "steady" {
				minimum = 1 << 20
				time.Sleep(1200 * time.Millisecond)
			}
			for r.TotalBytes < minimum && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				r, _ = tasks.PollCommand(t.Context(), r.ID, 0, 16384)
			}
			if r.TotalBytes < minimum || !r.OutputGap || r.FirstCursor == 0 {
				t.Fatalf("tail %+v", r)
			}
			size := 0
			for _, part := range r.Output {
				size += len(part.Text)
			}
			if size > 16384 {
				t.Fatal("page exceeded 16 KiB")
			}
			tasks.core.commands.mu.Lock()
			p := tasks.core.commands.active[r.ID]
			tasks.core.commands.mu.Unlock()
			if p == nil {
				t.Fatal("service ended before log observation")
			}
			p.tail.mu.Lock()
			retained := p.tail.bytes
			p.tail.mu.Unlock()
			if retained > 256<<10 {
				t.Fatal("unbounded retained logs")
			}
			t.Logf("mode=%s observed=%d bytes; retained %d bytes; page %d bytes", mode, r.TotalBytes, retained, size)
			if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceCleanupPersistenceFailureRetainsUNKNOWNAndCapacity(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 20)
	parent, r := startService(t, tasks, chat, "addr", "", 10, "", 0)
	_, err := tasks.core.Store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_command_projection BEFORE UPDATE ON meta WHEN NEW.key LIKE 'command/process/%' BEGIN SELECT RAISE(FAIL,'fixture persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	r, err = tasks.StopCommand(t.Context(), r.ID)
	if err != nil || r.State != "unknown" || !tasks.HasCommands() {
		t.Fatalf("uncertain persistence %+v %v", r, err)
	}
	run, _, _, _ := tasks.Inspect(t.Context(), parent.Run.ID)
	if run.Status != "completed" {
		t.Fatal("reopened parent")
	}
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `DROP TRIGGER fail_command_projection`); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CloseCommands("test_close"); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("uncertain close hidden %v", err)
	}
}

func TestServiceTailRedactsSplitSecretsBeforePublication(t *testing.T) {
	tail := &commandTail{limit: 64, secrets: []string{"fixture-secret-value"}, readyText: "READY", ready: make(chan struct{})}
	w := commandStream{tail, 0}
	for _, chunk := range []string{"fixture-", "secret-", "value RE", "AD", "Y"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.finish()
	out, _, _, _, _ := tail.snapshot(0, 64)
	var text string
	for _, chunk := range out {
		text += chunk.Text
	}
	if strings.Contains(text, "fixture-secret") || !strings.Contains(text, "[redacted]") || !tail.readyMatched {
		t.Fatalf("redaction/readiness %q", text)
	}
}

func TestHostControlGateCountsEveryLiveProcess(t *testing.T) {
	gate := &controlDecisionGate{}
	if err := gate.beginHost(); err != nil {
		t.Fatal(err)
	}
	if err := gate.beginHost(); err != nil {
		t.Fatal(err)
	}
	gate.endHost()
	if !errors.Is(gate.beginControl(), ErrControlBusy) {
		t.Fatal("first exit released shared host gate")
	}
	gate.endHost()
	if err := gate.beginControl(); err != nil {
		t.Fatal(err)
	}
	gate.endControl()
}

func TestServiceMonotonicExtensionHardCapAndWallClockRollback(t *testing.T) {
	now := time.Now()
	oldWall := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	maximum := oldWall.Add(8*time.Hour - 30*time.Minute)
	deadline := now.Add(20 * time.Minute)
	target := oldWall.Add(time.Hour)
	next, err := finiteExtensionDeadline(now, deadline, oldWall, maximum, target)
	if err != nil || next.Sub(deadline) != time.Hour {
		t.Fatalf("lost monotonic allocation %s %v", next.Sub(deadline), err)
	}
	// Wall presentation moved a day back, but the elapsed allocation is the same.
	rolled, err := finiteExtensionDeadline(now, deadline, oldWall.Add(-24*time.Hour), maximum.Add(-24*time.Hour), target.Add(-24*time.Hour))
	if err != nil || !rolled.Equal(next) {
		t.Fatal("wall rollback changed lifetime")
	}
	if _, err := finiteExtensionDeadline(now.Add(time.Hour), deadline, oldWall, maximum, target); err == nil {
		t.Fatal("expired deadline revived")
	}
	if _, err := finiteExtensionDeadline(now, deadline, oldWall, maximum, maximum.Add(time.Nanosecond)); err == nil {
		t.Fatal("8h bound exceeded")
	}
	if err := (ServicePolicy{Enabled: true, DefaultSeconds: 1800, MaxTotalSeconds: 28801}).Validate(); err == nil {
		t.Fatal("product hard cap configurable")
	}
	t.Log("8h bounds and wall-clock rollback simulated; no claim of an actual 8h run")
}

func TestServiceAIExtensionNeedsConfirmationAndConfigBusy(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 20)
	parent, r := startService(t, tasks, chat, "addr", "", 10, "", 0)
	core, err := tasks.core.forRun(parent.Run)
	if err != nil {
		t.Fatal(err)
	}
	runner := commandInvocationTools{base: core.Tools, inv: commandInvocation{core, parent.Run.ID, ""}}
	expires, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	request := CommandExtension{ID: r.ID, Revision: r.Revision, ExpiresAt: expires.Add(time.Second).Format(time.RFC3339Nano), RequestKey: "model-extension"}
	prepared, err := runner.Prepare(t.Context(), makeCall("extend", "command_extend", request))
	if err != nil || !prepared.userConfirmation {
		t.Fatalf("Full bypasses model extension review %+v %v", prepared, err)
	}
	control, err := NewControlService(tasks)
	if err != nil {
		t.Fatal(err)
	}
	policy := tasks.Config().Commands
	policy.GitWrite = !policy.GitWrite
	result, err := control.ApplyDirectControlChange(t.Context(), ControlSetCommandPolicy, policy, "", "test-user")
	if !errors.Is(err, ErrCommandBusy) || result.ErrorCode != "execution_profile_busy" || !strings.Contains(err.Error(), r.ID) {
		t.Fatalf("running authorization changed %+v %v", result, err)
	}
	// User Stop remains outside both the tool enable switch and control gate.
	tasks.core.Config.Commands.Enabled = false
	if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	tasks.core.Config.Commands.Enabled = true
	result, err = control.ApplyDirectControlChange(t.Context(), ControlSetCommandPolicy, policy, "", "test-user")
	if err != nil || !result.Applied {
		t.Fatalf("confirmed cleanup still blocks config %+v %v", result, err)
	}
}

func TestServiceConcurrentExtensionAndStop(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 20)
	_, r := startService(t, tasks, chat, "addr", "", 10, "", 0)
	expires, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	var wg sync.WaitGroup
	accepted := make(chan bool, 8)
	start := make(chan struct{})
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := tasks.ExtendCommand(t.Context(), CommandExtension{ID: r.ID, Revision: r.Revision, ExpiresAt: expires.Add(time.Second).Format(time.RFC3339Nano), RequestKey: fmt.Sprintf("extension-%d", i)})
			accepted <- err == nil
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	close(accepted)
	count := 0
	for ok := range accepted {
		if ok {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("same revision extended %d times", count)
	}
	final, err := tasks.PollCommand(t.Context(), r.ID, 0, 16384)
	if err != nil || final.State != "stopped" || len(final.Extensions) > 1 {
		t.Fatalf("race %+v %v", final, err)
	}
}

func TestServiceBackupRecoveryDoesNotReopenParentOrAttach(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 20)
	parent, r := startService(t, tasks, chat, "addr", "", 10, "", 0)
	backup := filepath.Join(t.TempDir(), "backup")
	if err := tasks.core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.StopCommand(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, data); err != nil {
		t.Fatal(err)
	}
	restored, closeTasks, err := OpenTasks(t.Context(), data, Adapters{Keys: NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTasks()
	recovered, err := restored.PollCommand(t.Context(), r.ID, 0, 16384)
	if err != nil || recovered.State != "unknown" || recovered.Reason != "owner_lost" || restored.HasCommands() {
		t.Fatalf("recovery attached %+v %v", recovered, err)
	}
	run, _, _, err := restored.Inspect(t.Context(), parent.Run.ID)
	if err != nil || run.Status != "completed" || run.Output != parent.Run.Output {
		t.Fatalf("reopened parent %+v %v", run, err)
	}
}

func TestServiceStartupPersistenceFailureRecoversFromOriginalAttempt(t *testing.T) {
	tasks, chat, _ := serviceFixture(t, 20)
	_, err := tasks.core.Store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_command_insert BEFORE INSERT ON meta WHEN NEW.key LIKE 'command/process/%' BEGIN SELECT RAISE(FAIL,'fixture startup persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	var args commandArguments
	_ = json.Unmarshal(helperCall(t, "service", "addr", "").Arguments, &args)
	args.Check = false
	args.Mode = "service"
	args.DurationSeconds = 10
	result, err := tasks.StartCommand(t.Context(), chat, newID(), mustJSON(args))
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
		t.Fatalf("startup invented certainty %+v %v", result, err)
	}
	list, err := tasks.Commands(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("unpersisted process hidden %+v %v", list, err)
	}
	id := list[0].ID
	if _, err := tasks.core.commands.unreviewed(t.Context()); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("new command can bypass live uncertainty: %v", err)
	}
	_ = finishService(t, tasks, id)
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `DROP TRIGGER fail_command_insert`); err != nil {
		t.Fatal(err)
	}
	before, attempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	recovered, err := newCommandSupervisor(t.Context(), tasks.core.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.close("test")
	r, err := recovered.poll(t.Context(), id, 0, 16384)
	if err != nil || r.State != "unknown" || r.Reason != "missing_start_projection" || recovered.hasActive() {
		t.Fatalf("lost dispatch evidence %+v %v", r, err)
	}
	after, afterAttempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(attempts, afterAttempts) {
		t.Fatal("recovery rewrote original Run or Attempt")
	}
}

func TestServicePolicyValidationAndUnsupportedBackend(t *testing.T) {
	tools := &CommandTools{Config: CommandConfig{Execution: CommandExecutionHostExplicit, Services: DefaultServicePolicy()}}
	a := commandArguments{Mode: "service"}
	if err := tools.serviceArguments(&a); err == nil {
		t.Fatal("implicit listening enabled")
	}
	tools.Config.Services.AllowLocalListen = true
	if err := tools.serviceArguments(&a); err != nil || a.DurationSeconds != 1800 || !a.Background {
		t.Fatalf("default %+v %v", a, err)
	}
	tools.Config.Execution = CommandExecutionSandboxed
	if err := tools.serviceArguments(&a); !errors.Is(err, ErrSandboxUnsupported) {
		t.Fatalf("silent sandbox fallback: %v", err)
	}
	tools.Config.Execution = CommandExecutionHostExplicit
	for _, a := range []commandArguments{{Mode: "service", DurationSeconds: 28801}, {Mode: "service", DurationSeconds: 10, ReadyText: "ready", ReadyTimeout: 11}, {Mode: "background", ReadyText: "ready"}, {Mode: "service", DurationSeconds: -1}} {
		if err := tools.serviceArguments(&a); err == nil {
			t.Fatalf("invalid request accepted %+v", a)
		}
	}
}

func TestCommandTailCursorRecoveryAcrossUTF8AndWrap(t *testing.T) {
	tail := &commandTail{limit: 128}
	w := commandStream{tail, 0}
	_, _ = w.Write([]byte("中文 ready"))
	w.finish()
	out, _, next, _, gap := tail.snapshot(1, 128)
	if !gap || next != int64(len("中文 ready")) || len(out) != 1 || out[0].Text != "文 ready" {
		t.Fatalf("mid-rune cursor stuck %+v next=%d gap=%t", out, next, gap)
	}
	tail = &commandTail{limit: 16}
	w = commandStream{tail, 0}
	_, _ = w.Write([]byte(strings.Repeat("log", 30)))
	w.finish()
	_, first, _, _, _ := tail.snapshot(0, 16)
	_, _, _, _, gap = tail.snapshot(first, 16)
	if gap {
		t.Fatal("caught-up cursor incorrectly reports a fresh output gap")
	}
}
