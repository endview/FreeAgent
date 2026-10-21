package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/chatui"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func TestCLIMemoryMutationAttachesToExistingOwner(t *testing.T) {
	data, _ := initCLI(t)
	keys := harness.NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	config, store, service, closeAll, err := harness.OpenRuntime(t.Context(), data, harness.Adapters{Model: harness.EchoModel{}, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	defer store.Close()
	tasks := harness.NewTaskService(service)
	conversation, err := tasks.NewConversation(t.Context(), "memory attach")
	if err != nil {
		t.Fatal(err)
	}

	listener := httptest.NewUnstartedServer(nil)
	origin := "http://" + listener.Listener.Addr().String()
	chatToken, attachToken := chatui.NewToken(), chatui.NewToken()
	server, err := chatui.NewServerWithAttach(t.Context(), tasks, origin, chatToken, attachToken)
	if err != nil {
		t.Fatal(err)
	}
	generation := "memory-attach-test-generation"
	server.SetGeneration(generation)
	info := handoff.EngineInfo{Origin: origin, Workspace: config.Workspace, Generation: generation,
		PID: os.Getpid(), OwnerKind: "interactive"}
	server.SetOwnerInfo(info)
	listener.Config.Handler = server
	listener.Start()
	defer listener.Close()
	defer server.Close()
	published, err := handoff.Publish(data, info, attachToken, chatToken)
	if err != nil {
		t.Fatal(err)
	}
	defer published.Close()

	var stdout, stderr bytes.Buffer
	err = run(t.Context(), []string{"memory", "save", "--data-dir", data, "--session", conversation.ID,
		"--scope", "private", "--title", "Attached note", "--text", "owner-side memory",
		"--request", "memory-attach-save", "--yes"}, io.Reader(bytes.NewBuffer(nil)), &stdout, &stderr)
	if err != nil {
		t.Fatalf("attached memory save failed: stdout=%s stderr=%s err=%v", stdout.String(), stderr.String(), err)
	}
	var receipt harness.MemoryReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt.EntryID == "" || receipt.Revision != 1 {
		t.Fatalf("receipt=%s err=%v", stdout.String(), err)
	}
	stdout.Reset()
	if err := run(t.Context(), []string{"memory", "read", "--data-dir", data, "--session", conversation.ID,
		"--scope", "private", "--entry", receipt.EntryID}, bytes.NewReader(nil), &stdout, &stderr); err != nil {
		t.Fatalf("attached memory read failed: %v", err)
	}
	var entry harness.MemoryEntry
	if err := json.Unmarshal(stdout.Bytes(), &entry); err != nil || entry.Body != "owner-side memory" {
		t.Fatalf("entry=%s err=%v", stdout.String(), err)
	}
	if _, err := tasks.ReadMemory(t.Context(), conversation.ID, harness.MemoryScope{Kind: "private", Owner: "chat-" + conversation.ID, Project: config.Workspace}, receipt.EntryID); err != nil {
		t.Fatalf("owner store cannot read attached memory: %v", err)
	}
}
