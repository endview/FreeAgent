package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCollaborationCrashHelper(t *testing.T) {
	if os.Getenv("HARNESS_COLLAB_CRASH") != "1" {
		return
	}
	tasks, closeAll, err := OpenTasks(context.Background(), os.Getenv("DATA"), Adapters{Model: modelFunc(readingChild)})
	if err != nil {
		os.Exit(2)
	}
	defer closeAll()
	tasks.SetEvents(func(event Event) {
		phase := os.Getenv("PHASE")
		if event.Kind == "collaboration" && ((phase == "model-marker" && event.Message == "model_started") || (phase == "saved-read" && event.Message == "tool_finished")) {
			fmt.Println("READY")
			select {}
		}
	})
	if _, err := NewCollaboration(tasks).Continue(context.Background(), os.Getenv("PARENT")); err != nil {
		os.Exit(3)
	}
}

func TestCollaborationActualKillKeepsParentBudgetAndKnownResults(t *testing.T) {
	for _, phase := range []string{"model-marker", "saved-read"} {
		t.Run(phase, func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
			core.Config.instructionText = ""
			id := createCollaboration(t, collab, plan)
			data := core.Store.DataDir()
			if err := core.Store.Close(); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestCollaborationCrashHelper$")
			command.Env, _, err = mcpEnvironment(MCPServerConfig{Env: map[string]string{"HARNESS_COLLAB_CRASH": "1", "DATA": data, "PARENT": id, "PHASE": phase}}, nil)
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
			go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- strings.TrimSpace(line) }()
			select {
			case line := <-ready:
				if line != "READY" {
					_ = command.Process.Kill()
					_ = command.Wait()
					killed = true
					t.Fatalf("window=%q stderr=%s", line, diagnostics.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatal("crash fixture did not reach marker")
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			killed = true
			store, err := OpenStore(t.Context(), data)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			core.Store = store
			collab = NewCollaboration(NewTaskService(core))
			report, err := collab.Inspect(t.Context(), id)
			if err != nil || report.Usage.ActiveChildren != 0 || report.Usage.ModelCalls != 1 {
				t.Fatalf("recovery=%+v %v", report, err)
			}
			usage := report.Usage
			report, err = collab.Continue(t.Context(), id)
			if phase == "model-marker" {
				if !errors.Is(err, ErrUnknownResult) || report.Usage != usage || report.Children[1].Run.Steps != 0 {
					t.Fatalf("marker replayed=%+v %v", report, err)
				}
			} else if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 4 || report.Usage.ToolCalls != 2 {
				t.Fatalf("known read replayed=%+v %v", report, err)
			}
		})
	}
}
