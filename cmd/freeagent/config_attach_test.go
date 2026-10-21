package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/chatui"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

type ownerConfirmationReader struct {
	client *handoff.Client
	ctx    context.Context
	used   bool
}

func (reader *ownerConfirmationReader) Read(buffer []byte) (int, error) {
	if reader.used {
		return 0, io.EOF
	}
	operations, err := reader.client.ControlOperations(reader.ctx)
	if err != nil {
		return 0, err
	}
	var pending harness.ControlOperation
	for _, operation := range operations {
		if operation.Status == harness.ControlPending {
			pending = operation
		}
	}
	if pending.ID == "" {
		return 0, io.ErrNoProgress
	}
	confirmation, err := reader.client.ControlConfirmation(reader.ctx, pending.ID, "")
	if err != nil {
		return 0, err
	}
	value := []byte(confirmation.Code + "\n")
	if len(buffer) < len(value) {
		return 0, io.ErrShortBuffer
	}
	reader.used = true
	return copy(buffer, value), nil
}

func TestCLIConfigMutationAttachesToExistingOwnerControlService(t *testing.T) {
	data, _ := initCLI(t)
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}

	ownerConfig, store, service, closeAll, err := harness.OpenRuntime(t.Context(), data, harness.Adapters{Model: harness.EchoModel{}, Keys: harness.NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))})
	if err != nil {
		t.Fatal(err)
	}
	if ownerConfig.DefaultModel != config.DefaultModel {
		t.Fatalf("owner opened a different config: %+v vs %+v", ownerConfig, config)
	}
	tasks := harness.NewTaskService(service)
	listener := httptest.NewUnstartedServer(nil)
	origin := "http://" + listener.Listener.Addr().String()
	chatToken, attachToken := chatui.NewToken(), chatui.NewToken()
	server, err := chatui.NewServerWithAttach(t.Context(), tasks, origin, chatToken, attachToken)
	if err != nil {
		_ = store.Close()
		closeAll()
		t.Fatal(err)
	}
	generation := "config-attach-test-generation"
	server.SetGeneration(generation)
	info := handoff.EngineInfo{Origin: origin, Workspace: config.Workspace, Generation: generation,
		PID: os.Getpid(), OwnerKind: "interactive"}
	server.SetOwnerInfo(info)
	listener.Config.Handler = server
	listener.Start()
	defer listener.Close()
	defer server.Close()
	defer closeAll()
	defer store.Close()

	published, err := handoff.Publish(data, info, attachToken, chatToken)
	if err != nil {
		t.Fatal(err)
	}
	defer published.Close()

	client, err := handoff.NewInteractiveClient(info, chatToken)
	if err != nil {
		t.Fatal(err)
	}
	reader := &ownerConfirmationReader{client: client, ctx: t.Context()}
	var stdout, stderr bytes.Buffer
	err = run(t.Context(), []string{"config", "model", "--data-dir", data, "--id", "attached", "--provider", "echo", "--default"}, reader, &stdout, &stderr)
	if err != nil {
		t.Fatalf("attached config failed: stdout=%s stderr=%s err=%v", stdout.String(), stderr.String(), err)
	}
	if !strings.Contains(stderr.String(), "Confirmation code:") {
		t.Fatalf("attached config did not render a CLI confirmation: %s", stderr.String())
	}
	var result struct {
		OK      bool                     `json:"ok"`
		Changed bool                     `json:"changed"`
		Op      harness.ControlOperation `json:"operation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("attached output=%s err=%v", stdout.String(), err)
	}
	if !result.OK || !result.Changed || result.Op.Kind != harness.ControlSetModelProfile || result.Op.Status != harness.ControlApplied {
		t.Fatalf("attached result=%+v output=%s", result, stdout.String())
	}
	if service.Config.DefaultModel != "attached" || service.Config.Models["attached"].Provider != "echo" {
		t.Fatalf("owner config was not updated: default=%q models=%+v", service.Config.DefaultModel, service.Config.Models)
	}
	updated, err := harness.LoadConfig(data)
	if err != nil || updated.DefaultModel != "attached" {
		t.Fatalf("persisted config=%+v err=%v", updated, err)
	}
	journal, err := os.ReadFile(filepath.Join(data, "control-journal.jsonl"))
	if err != nil || !strings.Contains(string(journal), `"action":"applied"`) {
		t.Fatalf("control journal=%s err=%v", journal, err)
	}
	var show bytes.Buffer
	if err := run(t.Context(), []string{"config", "show", "--data-dir", data}, strings.NewReader(""), &show, io.Discard); err != nil {
		t.Fatalf("attached config show failed: %v", err)
	}
	var projection struct {
		Changed      bool   `json:"changed"`
		DefaultModel string `json:"default_model"`
	}
	if err := json.Unmarshal(show.Bytes(), &projection); err != nil || projection.Changed || projection.DefaultModel != "attached" {
		t.Fatalf("attached config show=%s err=%v", show.String(), err)
	}
}
