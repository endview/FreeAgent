package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalFileScopeUsesAuthorizedRootForAllFileOperations(t *testing.T) {
	_, tools, _ := testHarness(t, EchoModel{})
	external := t.TempDir()
	file := filepath.Join(external, "notes", "todo.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := FileAccessPolicy{ComputerFiles: true, ComputerRoots: []string{external}}

	prepared, err := tools.PrepareWithPolicy(t.Context(), makeCall("read", "file_read", map[string]any{
		"path": file,
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.workspaceFile || len(prepared.Items) != 1 || prepared.Items[0].Target != filepath.Clean(file) {
		t.Fatalf("read scope unexpectedly requested workspace approval: %+v", prepared)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.release()
	var read struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(result.Content), &read); err != nil || read.Path != filepath.Clean(file) || read.Content != "one\ntwo\n" {
		t.Fatalf("external read=%+v err=%v result=%+v", read, err, result)
	}

	prepared, err = tools.PrepareWithPolicy(t.Context(), makeCall("edit", "file_edit", map[string]any{
		"path": file, "expected_sha256": contentHash([]byte("one\ntwo\n")), "old_text": "two", "new_text": "three",
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.workspaceFile || prepared.Items[0].Target != filepath.Clean(file) || !strings.Contains(prepared.Preview, filepath.Clean(file)) {
		t.Fatalf("external edit preview=%+v", prepared)
	}
	if _, err := prepared.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != "one\nthree\n" {
		t.Fatalf("external edit raw=%q err=%v", raw, err)
	}

	created := filepath.Join(external, "created.txt")
	prepared, err = tools.PrepareWithPolicy(t.Context(), makeCall("create", "file_create", map[string]any{
		"path": created, "content": "created",
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(created); err != nil || string(raw) != "created" {
		t.Fatalf("external create raw=%q err=%v", raw, err)
	}

	batch := filepath.Join(external, "batch.txt")
	if err := os.WriteFile(batch, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err = tools.PrepareWithPolicy(t.Context(), makeCall("batch", "file_batch_edit", map[string]any{
		"changes": []map[string]any{
			{"operation": "edit", "path": batch, "expected_sha256": contentHash([]byte("before")), "old_text": "before", "new_text": "after"},
			{"operation": "create", "path": filepath.Join(external, "batch-created.txt"), "content": "yes"},
		},
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Items) != 2 || prepared.Items[0].Target != filepath.Clean(batch) {
		t.Fatalf("external batch preview=%+v", prepared.Items)
	}
	if _, err := prepared.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(batch); err != nil || string(raw) != "after" {
		t.Fatalf("external batch edit raw=%q err=%v", raw, err)
	}

	for _, test := range []struct {
		name   string
		policy FileAccessPolicy
		want   error
	}{
		{name: "disabled", policy: FileAccessPolicy{ComputerRoots: []string{external}}, want: ErrComputerFilesDisabled},
		{name: "outside", policy: policy, want: ErrPathNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := file
			if test.name == "outside" {
				target = filepath.Join(t.TempDir(), "outside.txt")
				if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := tools.PrepareWithPolicy(t.Context(), makeCall("read", "file_read", map[string]string{"path": target}), test.policy)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestExternalFileScopeAllowsAnAuthorizedFileRoot(t *testing.T) {
	_, files, workspace := testHarness(t, EchoModel{})
	external := t.TempDir()
	path := filepath.Join(external, "note.txt")
	if err := os.WriteFile(path, []byte("authorized"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := FileAccessPolicy{ComputerFiles: true, ComputerRoots: []string{path}, Scope: FileScopeWorkspacePlusAllowedRoots}
	prepared, err := files.PrepareWithPolicy(t.Context(), makeCall("file", "file_read", map[string]string{"path": path}), policy)
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || !strings.Contains(result.Content, "authorized") {
		t.Fatalf("file root result=%+v err=%v workspace=%s", result, err, workspace)
	}
}

func TestSelectedRootsIncludesWorkspaceForAbsolutePaths(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	file := filepath.Join(workspace, "absolute.txt")
	if err := os.WriteFile(file, []byte("workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := tools.PrepareWithPolicy(t.Context(), makeCall("absolute", "file_read", map[string]string{"path": file}), FileAccessPolicy{
		ComputerFiles: true, Scope: FileScopeSelectedRoots,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.release()
	result, err := prepared.Execute(t.Context())
	if err != nil || result.IsError || !strings.Contains(result.Content, "workspace") {
		t.Fatalf("absolute workspace result=%+v err=%v", result, err)
	}
}

func TestExternalFileScopeRejectsLinksAndProtectedData(t *testing.T) {
	_, tools, _ := testHarness(t, EchoModel{})
	external := t.TempDir()
	data := tools.dataDir
	if _, err := tools.PrepareWithPolicy(context.Background(), makeCall("data", "file_read", map[string]string{"path": data}), FileAccessPolicy{ComputerFiles: true, ComputerRoots: []string{filepath.Dir(data)}}); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("data path err=%v", err)
	}
	link := filepath.Join(external, "link.txt")
	if err := os.Symlink(filepath.Join(external, "missing.txt"), link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	_, err := tools.PrepareWithPolicy(t.Context(), makeCall("link", "file_read", map[string]string{"path": link}), FileAccessPolicy{ComputerFiles: true, ComputerRoots: []string{external}})
	if err == nil || !strings.Contains(err.Error(), "links") {
		t.Fatalf("link was accepted: %v", err)
	}
}

func TestBrokerAllAllowsParentRootButProtectsHarnessAndBlockedSubtrees(t *testing.T) {
	_, tools, _ := testHarness(t, EchoModel{})
	external := t.TempDir()
	file := filepath.Join(external, "allowed.txt")
	blocked := filepath.Join(external, "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("broker"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedFile := filepath.Join(blocked, "secret.txt")
	if err := os.WriteFile(blockedFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := FileAccessPolicy{ComputerFiles: true, Scope: FileScopeBrokerAll, BlockedPaths: []string{blocked}}
	prepared, err := tools.PrepareWithPolicy(t.Context(), makeCall("read", "file_read", map[string]string{"path": file}), policy)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.release()
	result, err := prepared.Execute(t.Context())
	if err != nil || result.IsError || !strings.Contains(result.Content, "broker") {
		t.Fatalf("broker read result=%+v err=%v", result, err)
	}
	if _, err := tools.PrepareWithPolicy(t.Context(), makeCall("blocked", "file_read", map[string]string{"path": blockedFile}), policy); !errors.Is(err, ErrPathBlocked) {
		t.Fatalf("blocked path err=%v", err)
	}
	if _, err := tools.PrepareWithPolicy(t.Context(), makeCall("data", "file_read", map[string]string{"path": tools.dataDir}), policy); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("data path err=%v", err)
	}
}

func TestComputerRootMayContainDataDirectoryParent(t *testing.T) {
	_, tools, _ := testHarness(t, EchoModel{})
	parent := filepath.Dir(tools.dataDir)
	if err := validateComputerRoots(t.Context(), tools.dataDir, []string{parent}); err != nil {
		t.Fatalf("parent root rejected: %v", err)
	}
	if err := validateComputerRoots(t.Context(), tools.dataDir, []string{tools.dataDir}); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("data root err=%v", err)
	}
}

func TestExternalFileReadPermissionMatrix(t *testing.T) {
	service, tools, _ := testHarness(t, EchoModel{})
	external := t.TempDir()
	file := filepath.Join(external, "read.txt")
	if err := os.WriteFile(file, []byte("read"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := tools.PrepareWithPolicy(t.Context(), makeCall("read", "file_read", map[string]string{"path": file}), FileAccessPolicy{ComputerFiles: true, ComputerRoots: []string{external}})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.release()
	for _, test := range []struct {
		name string
		mode PermissionMode
		want bool
	}{
		{name: "ask", mode: PermissionAsk, want: true},
		{name: "workspace", mode: PermissionWorkspace, want: false},
		{name: "full", mode: PermissionFull, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			service.permission = &PermissionSelection{Mode: test.mode, Source: "turn", ComputerFiles: true, ComputerRoots: []string{external}}
			if got := service.needsApproval(prepared); got != test.want {
				t.Fatalf("mode=%s needsApproval=%t want=%t", test.mode, got, test.want)
			}
		})
	}
}
