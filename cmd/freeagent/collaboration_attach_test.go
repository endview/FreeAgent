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

func TestCLICollaborationInspectionAttachesToExistingOwner(t *testing.T) {
	data, workspace := initCLI(t)
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "ask"); err != nil {
		t.Fatal(err)
	}
	keys := harness.NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	_, store, service, closeAll, err := harness.OpenRuntime(t.Context(), data, harness.Adapters{Model: harness.EchoModel{}, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	defer store.Close()
	tasks := harness.NewTaskService(service)

	listener := httptest.NewUnstartedServer(nil)
	origin := "http://" + listener.Listener.Addr().String()
	chatToken, attachToken := chatui.NewToken(), chatui.NewToken()
	server, err := chatui.NewServerWithAttach(t.Context(), tasks, origin, chatToken, attachToken)
	if err != nil {
		t.Fatal(err)
	}
	generation := "collab-attach-test-generation"
	server.SetGeneration(generation)
	info := handoff.EngineInfo{Origin: origin, Workspace: workspace, Generation: generation,
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

	rawPlan, err := json.Marshal(cliCollaborationPlan())
	if err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planFile, rawPlan, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"collab", "create", "--data-dir", data, "--plan", planFile}, io.Reader(bytes.NewBuffer(nil)), &stdout, &stderr); err != nil {
		t.Fatalf("attached collaboration create failed: stdout=%s stderr=%s err=%v", stdout.String(), stderr.String(), err)
	}
	var created struct {
		Parent string `json:"parent_run"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil || created.Parent == "" {
		t.Fatalf("create=%s err=%v", stdout.String(), err)
	}
	stdout.Reset()
	if err := run(t.Context(), []string{"collab", "inspect", "--data-dir", data, "--parent", created.Parent}, bytes.NewReader(nil), &stdout, &stderr); err != nil {
		t.Fatalf("attached collaboration inspect failed: %v", err)
	}
	var report harness.CollaborationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Parent.ID != created.Parent {
		t.Fatalf("inspect=%s err=%v", stdout.String(), err)
	}
}
