//go:build ignore

// Run only with scripts/test-postgres.py's isolated service. This exercises
// persistent TaskService operations, approvals, deduplication, cancellation,
// read projections, backups and runtime reopen; it is not a real Provider test.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/postgres"
)

type soakModel struct{ cancelStarted chan struct{} }

func (model soakModel) Generate(ctx context.Context, request harness.ModelRequest, emit func(string)) (harness.ModelResponse, error) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		message := request.Messages[i]
		if message.Role == "tool" {
			return harness.ModelResponse{Content: "SOAK_TOOL_FINISHED"}, nil
		}
		if message.Role != "user" {
			continue
		}
		if strings.HasPrefix(message.Content, "cancel:") {
			close(model.cancelStarted)
			<-ctx.Done()
			return harness.ModelResponse{}, ctx.Err()
		}
		if strings.HasPrefix(message.Content, "create:") {
			arguments, _ := json.Marshal(map[string]string{"path": strings.TrimPrefix(message.Content, "create:"), "content": "SOAK_CREATED_ONCE"})
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "soak-create", Name: "file_create", Arguments: arguments}}}, nil
		}
		for _, part := range []string{"SOAK_", "CHAT_", "OK"} {
			emit(part)
		}
		return harness.ModelResponse{Content: "SOAK_CHAT_OK"}, nil
	}
	return harness.ModelResponse{Content: "SOAK_CHAT_OK"}, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	duration := flag.Duration("duration", 8*time.Hour, "minimum wall-clock workload duration")
	interval := flag.Duration("interval", 20*time.Second, "interval between cycles")
	output := flag.String("out", "", "new private results directory")
	flag.Parse()
	if os.Getenv(postgres.ServiceFileEnv) == "" || *duration <= 0 || *interval < 0 || *output == "" {
		panic("isolated PG service, positive duration and new --out are required")
	}
	root, err := filepath.Abs(*output)
	must(err)
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		panic("output must be new")
	}
	must(os.MkdirAll(root, 0700))
	data, workspace := filepath.Join(root, "data"), filepath.Join(root, "workspace")
	must(os.Mkdir(workspace, 0700))
	ctx := context.Background()
	must(harness.InitStore(ctx, data))
	config := harness.DefaultConfig(workspace)
	config.Models = map[string]harness.ModelConfig{"soak": {Provider: "echo"}}
	config.DefaultModel = "soak"
	config.TaskSummaryBytes = 0
	must(harness.WriteConfig(data, config))
	started := time.Now()
	log, err := os.OpenFile(filepath.Join(root, "samples.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(err)
	defer log.Close()
	cycles, operations, backups := 0, 0, 0
	for {
		cycleStarted := time.Now()
		cancelStarted := make(chan struct{})
		_, store, core, closeAll, err := harness.OpenRuntime(ctx, data, harness.Adapters{Model: soakModel{cancelStarted: cancelStarted}, Keys: harness.NewKeyStore(filepath.Join(root, "unused-keys.json"))})
		must(err)
		tasks := harness.NewTaskService(core)
		must(tasks.SetSpacePermission(ctx, harness.LocalSpaceID, harness.PermissionAsk, false))
		chat, err := tasks.NewConversation(ctx, "synthetic soak")
		must(err)
		result, err := tasks.Submit(ctx, chat.ID, "chat", "hello")
		must(err)
		if result.Run.Status != "completed" {
			panic("chat did not complete")
		}
		repeated, err := tasks.Submit(ctx, chat.ID, "chat", "hello")
		must(err)
		if !repeated.Duplicate || repeated.Run.ID != result.Run.ID {
			panic("duplicate changed run identity")
		}
		operations += 2
		for _, approve := range []bool{true, false} {
			chat, err := tasks.NewConversation(ctx, "synthetic approval")
			must(err)
			name := fmt.Sprintf("cycle-%d-%t.txt", cycles, approve)
			result, err := tasks.Submit(ctx, chat.ID, "create", "create:"+name)
			if result.Run.Status != "waiting_confirmation" {
				panic(fmt.Sprintf("expected approval status=%s err=%v", result.Run.Status, err))
			}
			preview, err := tasks.Preview(ctx, result.Run.ID)
			must(err)
			_, err = tasks.Confirm(ctx, result.Run.ID, preview, approve)
			must(err)
			raw, err := os.ReadFile(filepath.Join(workspace, name))
			if approve {
				must(err)
				if string(raw) != "SOAK_CREATED_ONCE" {
					panic("changed file")
				}
			} else if !os.IsNotExist(err) {
				panic("rejected operation wrote a file")
			}
			operations += 2
		}
		cancelChat, err := tasks.NewConversation(ctx, "synthetic cancel")
		must(err)
		// Cancel an actual dispatched call, not a DB setup transaction that
		// happened to take longer than a wall-clock guess on a throttled host.
		cancelCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		go func() {
			select {
			case <-cancelStarted:
				cancel()
			case <-cancelCtx.Done():
			}
		}()
		cancelled, err := tasks.Submit(cancelCtx, cancelChat.ID, "cancel", "cancel: stop model")
		cancel()
		if err == nil {
			panic("cancelled model unexpectedly succeeded")
		}
		if cancelled.Run.ID != "" {
			if abandonErr := tasks.Abandon(ctx, cancelled.Run.ID); abandonErr != nil {
				panic(fmt.Sprintf("cancel result=%s submit=%v abandon=%v", cancelled.Run.Status, err, abandonErr))
			}
		}
		operations++
		if cycles%10 == 0 {
			backup := filepath.Join(root, fmt.Sprintf("backup-%d", cycles))
			must(store.Backup(ctx, backup))
			must(harness.VerifyBackup(ctx, backup))
			backups++
		}
		status, err := store.MaintenanceStatus(ctx)
		must(err)
		if status.ActiveRuns != 0 || status.WaitingConfirmations != 0 || status.ActiveCommands != 0 {
			panic("unsettled work after cycle")
		}
		closeAll()
		cycles++
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		sample := map[string]any{"cycle": cycles, "operations": operations, "backups": backups, "elapsed_seconds": time.Since(started).Seconds(), "cycle_ms": time.Since(cycleStarted).Milliseconds(), "goroutines": runtime.NumGoroutine(), "heap_bytes": memory.HeapAlloc, "execution": status, "real_provider": false, "finished": time.Since(started) >= *duration}
		raw, _ := json.Marshal(sample)
		_, err = log.Write(append(raw, '\n'))
		must(err)
		must(log.Sync())
		tmp := filepath.Join(root, ".progress.json")
		must(os.WriteFile(tmp, append(raw, '\n'), 0600))
		must(os.Rename(tmp, filepath.Join(root, "progress.json")))
		fmt.Println(string(raw))
		if time.Since(started) >= *duration {
			break
		}
		time.Sleep(*interval)
	}
}
