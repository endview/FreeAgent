package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

// TestMain points the keys file at a temporary directory. The caller fixture
// subprocess inherits the parent's file so both see the same profile.
func TestMain(m *testing.M) {
	if os.Getenv("FREEAGENT_LOCAL_CALLER_FIXTURE") == "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "freeagent-local-caller-keys-")
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_ = os.Setenv(apppaths.KeysFileEnv, filepath.Join(dir, "keys.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestLocalCallerProcess(t *testing.T) {
	if os.Getenv("FREEAGENT_LOCAL_CALLER_FIXTURE") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[index+1:]...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			main()
			os.Exit(0)
		}
	}
	os.Exit(9)
}

func TestLocalCallerApprovalEventsAndCancellation(t *testing.T) {
	for _, mode := range []string{"approve", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			workspace, data := t.TempDir(), filepath.Join(t.TempDir(), "data")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
				sequence := calls.Add(1)
				if mode == "cancel" {
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
						t.Error("caller did not close its connection")
					}
					return
				}
				message := map[string]any{"role": "assistant", "content": "File created."}
				finish := "stop"
				if sequence == 1 {
					arguments, _ := json.Marshal(map[string]string{"path": "example.txt", "content": "exact local caller operation"})
					message["content"] = ""
					message["tool_calls"] = []any{map[string]any{"id": "example", "type": "function",
						"function": map[string]any{"name": "file_create", "arguments": string(arguments)}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
			}))
			defer server.Close()
			if err := harness.InitStore(t.Context(), data); err != nil {
				t.Fatal(err)
			}
			keys, err := apppaths.KeysFile()
			if err != nil {
				t.Fatal(err)
			}
			profile, err := harness.NewKeyStore(keys).Add(harness.KeyInput{
				Name: "local caller " + mode, BaseURL: server.URL, Source: harness.KeySourceNone})
			if err != nil {
				t.Fatal(err)
			}
			config := harness.DefaultConfig(workspace)
			config.Models = map[string]harness.ModelConfig{"default": {Provider: "openai-compatible", Key: profile.ID, Model: "local-caller-fixture"}}
			config.DefaultModel = "default"
			if err := harness.WriteConfig(data, config); err != nil {
				t.Fatal(err)
			}
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			timeout := "10s"
			if mode == "cancel" {
				timeout = "2s"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, program, "-test.run=TestLocalCallerProcess", "--",
				"--data-dir", data, "--timeout", timeout, "create the selected file")
			command.Env = append(os.Environ(), "FREEAGENT_LOCAL_CALLER_FIXTURE=1")
			command.Stdin = strings.NewReader("y\n")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			runErr := command.Run()
			var result harness.RunResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("caller output=%s stderr=%s err=%v", &stdout, &stderr, err)
			}
			if !strings.Contains(stderr.String(), "run_started") || !strings.Contains(stderr.String(), "model_started") {
				t.Fatal("caller did not receive events")
			}
			if mode == "approve" {
				content, err := os.ReadFile(filepath.Join(workspace, "example.txt"))
				if runErr != nil || err != nil || string(content) != "exact local caller operation" ||
					result.Run.Status != "completed" || calls.Load() != 2 || len(result.Evidence.Files) != 1 ||
					!strings.Contains(stderr.String(), "Approve exactly this operation?") {
					t.Fatalf("caller approval result=%+v stderr=%s err=%v", result, &stderr, runErr)
				}
			} else if runErr == nil || result.Run.Status != "unknown" || calls.Load() != 1 {
				t.Fatalf("caller cancellation lost UNKNOWN: %+v calls=%d err=%v", result, calls.Load(), runErr)
			}
			store, err := harness.OpenStore(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			attempts, err := store.ListAttempts(t.Context(), result.Run.ID)
			if err != nil || len(attempts) == 0 {
				t.Fatalf("caller results not durable: %v", err)
			}
			if mode == "cancel" && attempts[0].Status != "unknown" {
				t.Fatalf("cancelled attempt=%+v", attempts[0])
			}
		})
	}
}
