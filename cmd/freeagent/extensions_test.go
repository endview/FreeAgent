package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestGeneratedExtensionTemplatesPassProcessCheck(t *testing.T) {
	if _, err := exec.LookPath("python"); errors.Is(err, exec.ErrNotFound) {
		t.Skip("python is not available")
	}
	for _, kind := range []string{"context", "workflow", "tool"} {
		t.Run(kind, func(t *testing.T) {
			id := "com.example.generated" + kind
			root := filepath.Join(t.TempDir(), id)
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest, source := extensionTemplate(kind, id)
			if err := os.WriteFile(filepath.Join(root, "extension.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "main.py"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := harness.CheckExtensionProcess(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if result.Info.ID != id || result.Info.State != "checked" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExtensionTestCLIReplaysFixture(t *testing.T) {
	if _, err := exec.LookPath("python"); errors.Is(err, exec.ErrNotFound) {
		t.Skip("python is not available")
	}
	root := filepath.Join(t.TempDir(), "com.example.fixture")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, source := extensionTemplate("context", "com.example.fixture")
	if err := os.WriteFile(filepath.Join(root, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(t.TempDir(), "fixture.json")
	fixture := harness.ExtensionFixture{
		Turn: harness.ExtensionFixtureTurn{Message: "test fixture"},
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	output, _, err := invoke(t, "", "ext", "test", "--path", root, "--fixture", fixturePath)
	if err != nil {
		t.Fatalf("ext test failed: %v; output=%s", err, output)
	}
	var result harness.ExtensionFixtureResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Matched || len(result.Blocks) != 1 || result.Blocks[0].Text == "" {
		t.Fatalf("fixture result=%+v", result)
	}
}

func TestExtensionRunCLIExecutesLiveWorkflowAndDeduplicates(t *testing.T) {
	if _, err := exec.LookPath("python"); errors.Is(err, exec.ErrNotFound) {
		t.Skip("python is not available")
	}
	data, _ := initCLI(t)
	root := filepath.Join(t.TempDir(), "com.example.live")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, source := extensionTemplate("workflow", "com.example.live")
	if err := os.WriteFile(filepath.Join(root, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	output, stderr, err := invoke(t, "", "ext", "run", "--data-dir", data,
		"--path", root, "--workflow", "run", "--input", `{"text":"hello"}`,
		"--request", "live-workflow-1", "--json")
	if err != nil {
		t.Fatalf("live workflow failed: %v; stdout=%s stderr=%s", err, output, stderr)
	}
	var first extensionRunOutput
	if err := json.Unmarshal([]byte(output), &first); err != nil {
		t.Fatalf("live workflow output=%q error=%v", output, err)
	}
	if first.Extension != "com.example.live" || first.Workflow != "run" ||
		first.Run.Status != "completed" || first.Answer != "Replace this generated workflow: hello" ||
		first.Session == "" || first.Run.ID == "" {
		t.Fatalf("live workflow result=%+v", first)
	}

	output, stderr, err = invoke(t, "", "ext", "run", "--data-dir", data,
		"--path", root, "--workflow", "run", "--input", `{"text":"hello"}`,
		"--session", first.Session, "--request", "live-workflow-1", "--json")
	if err != nil {
		t.Fatalf("duplicate workflow failed: %v; stdout=%s stderr=%s", err, output, stderr)
	}
	var duplicate extensionRunOutput
	if err := json.Unmarshal([]byte(output), &duplicate); err != nil {
		t.Fatalf("duplicate output=%q error=%v", output, err)
	}
	if !duplicate.Duplicate || duplicate.Run.ID != first.Run.ID || duplicate.Answer != first.Answer {
		t.Fatalf("duplicate workflow result=%+v", duplicate)
	}
	if !strings.Contains(first.Run.Prompt, "com.example.live/run") {
		t.Fatalf("workflow prompt=%q", first.Run.Prompt)
	}
}
