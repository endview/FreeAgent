package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCrashHelperProcess(t *testing.T) {
	if os.Getenv("HARNESS_CRASH_FIXTURE") != "1" {
		return
	}
	data := os.Getenv("DATA")
	config, err := LoadConfig(data)
	if err != nil {
		os.Exit(2)
	}
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		os.Exit(3)
	}
	files, err := NewFileTools(config.Workspace, data)
	if err != nil {
		os.Exit(4)
	}
	phase := os.Getenv("PHASE")
	service := &Service{Store: store, Config: config, Tools: files}
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		last := request.Messages[len(request.Messages)-1]
		if last.Role == "user" {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("edit", "file_edit", map[string]string{
				"path": "notes.txt", "expected_sha256": contentHash([]byte("before")), "old_text": "before", "new_text": "after",
			})}}, nil
		}
		return ModelResponse{Content: "completed from saved result"}, nil
	})
	freeze := func() {
		fmt.Println("READY")
		select {}
	}
	if phase == "after-effect" {
		service.Tools = &crashFileTools{files: files, freeze: freeze}
	}
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) {
		if phase == "before-dispatch" {
			freeze()
		}
		return true, nil
	}
	service.Emit = func(event Event) {
		if phase == "after-marker" && event.Kind == "tool_started" {
			freeze()
		}
		if phase == "after-result" && event.Kind == "tool_finished" {
			freeze()
		}
	}
	_, err = service.Run(context.Background(), os.Getenv("SESSION"), "crash-request", "change notes")
	if err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

type crashFileTools struct {
	files  *FileTools
	freeze func()
}

func (tools *crashFileTools) Definitions() []ToolDefinition { return tools.files.Definitions() }

func (tools *crashFileTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := tools.files.Prepare(ctx, call)
	if err != nil {
		return prepared, err
	}
	execute := prepared.Execute
	prepared.Execute = func(ctx context.Context) (ToolResult, error) {
		result, err := execute(ctx)
		if err == nil {
			tools.freeze()
		}
		return result, err
	}
	return prepared, nil
}

func TestActualProcessKillRecovery(t *testing.T) {
	for _, phase := range []string{"before-dispatch", "after-marker", "after-effect", "after-result"} {
		t.Run(phase, func(t *testing.T) {
			service, _, workspace := testHarness(t, EchoModel{})
			data := service.Store.DataDir()
			ctx := context.Background()
			id := conversation(t, service)
			if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := service.Store.Close(); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestCrashHelperProcess$")
			serverConfig := MCPServerConfig{Env: map[string]string{
				"HARNESS_CRASH_FIXTURE": "1", "DATA": data, "SESSION": id, "PHASE": phase,
			}}
			command.Env, _, err = mcpEnvironment(serverConfig, nil)
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics strings.Builder
			command.Stderr = &diagnostics
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			killed := false
			defer func() {
				if !killed {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			ready := make(chan string, 1)
			go func() {
				reader := bufio.NewReader(output)
				line, _ := reader.ReadString('\n')
				ready <- strings.TrimSpace(line)
			}()
			select {
			case line := <-ready:
				if line != "READY" {
					_ = command.Process.Kill()
					_ = command.Wait()
					killed = true
					t.Fatalf("child did not reach window: %q stderr=%s", line, diagnostics.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child did not reach crash window in time")
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			killed = true
			reopenHarness(t, service)
			run, err := service.Store.GetLatestRun(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
			attempts, _ := service.Store.ListAttempts(ctx, run.ID)
			modelCalls := 0
			service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				modelCalls++
				last := request.Messages[len(request.Messages)-1]
				if last.Role != "tool" || last.ToolCallID != "edit" {
					t.Fatalf("saved tool response not reused: %+v", last)
				}
				return ModelResponse{Content: "continued"}, nil
			})
			service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
			repeated, err := service.Run(ctx, id, "crash-request", "change notes")
			if err != nil || !repeated.Duplicate || repeated.Run.ID != run.ID || modelCalls != 0 {
				t.Fatalf("request replayed after kill: result=%+v error=%v calls=%d", repeated, err, modelCalls)
			}
			result, err := service.Continue(ctx, run.ID)
			switch phase {
			case "before-dispatch":
				if string(raw) != "before" || attempts[1].Status != "pending" || err != nil || result.Run.Status != "completed" {
					t.Fatalf("pre-dispatch recovery=%+v attempts=%+v file=%q error=%v", result, attempts, raw, err)
				}
			case "after-marker":
				if string(raw) != "before" || attempts[1].Status != "unknown" || !errors.Is(err, ErrUnknownResult) || modelCalls != 0 {
					t.Fatalf("ambiguous marker was replayed: result=%+v error=%v calls=%d", result, err, modelCalls)
				}
			case "after-effect":
				if string(raw) != "after" || attempts[1].Status != "unknown" || !errors.Is(err, ErrUnknownResult) || modelCalls != 0 {
					t.Fatalf("completed effect with missing result was replayed: result=%+v error=%v calls=%d", result, err, modelCalls)
				}
			case "after-result":
				if string(raw) != "after" || attempts[1].Status != "finished" || err != nil || modelCalls != 1 {
					t.Fatalf("saved result recovery=%+v attempts=%+v error=%v calls=%d", result, attempts, err, modelCalls)
				}
			}
		})
	}
}
