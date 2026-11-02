package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
)

func privateWebViewFixture(t *testing.T) (string, string) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	profile, err := apppaths.WebView2Dir()
	if err != nil {
		t.Fatal(err)
	}
	if !within(cache, profile) {
		t.Fatal("test profile is outside its temporary cache")
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(profile, "session.txt")
	if err := os.WriteFile(file, []byte("private browser capability fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cache, file
}

func TestFileToolsCannotReadPrivateBrowserProfile(t *testing.T) {
	cache, secret := privateWebViewFixture(t)
	data := t.TempDir()
	files, err := NewFileTools(cache, data)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	relative, _ := filepath.Rel(cache, secret)
	for _, target := range []string{relative, secret} {
		for _, tool := range []string{"file_read", "file_edit", "file_delete", "file_export", "file_create"} {
			path := target
			arguments := map[string]string{"path": path}
			if tool == "file_edit" || tool == "file_delete" {
				arguments["expected_sha256"] = contentHash([]byte("private browser capability fixture"))
			}
			if tool == "file_edit" {
				arguments["old_text"], arguments["new_text"] = "private", "exposed"
			}
			if tool == "file_create" {
				path = filepath.Join(filepath.Dir(path), "new.txt")
				arguments["path"], arguments["content"] = path, "exposed"
			}
			_, err := files.PrepareWithPolicy(t.Context(), makeCall("private", tool, arguments),
				FileAccessPolicy{ComputerFiles: true, Scope: FileScopeWorkspacePlusAllowedRoots, ComputerRoots: []string{cache}})
			if !errors.Is(err, ErrProtectedPath) {
				t.Fatalf("%s did not reject private browser state: path=%s err=%v", tool, path, err)
			}
		}
	}
	prepared, err := files.Prepare(t.Context(), makeCall("search", "file_search", map[string]string{"path": ".", "query": "private browser"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || strings.Contains(result.Content, "private browser capability fixture") {
		t.Fatalf("search disclosed browser state: %+v %v", result, err)
	}
}

func TestSandboxedCommandCannotReadPrivateBrowserProfile(t *testing.T) {
	originalCache := os.Getenv("LOCALAPPDATA")
	cache, secret := privateWebViewFixture(t)
	_, commands, workspace := commandFixture(t)
	// AppContainer initializes its own temporary profile using the real
	// LOCALAPPDATA. The file tools retain the isolated application paths.
	t.Setenv("LOCALAPPDATA", originalCache)
	commands.DataDir = t.TempDir()
	t.Cleanup(func() {
		if err := releaseSandboxGrants(commands.DataDir); err != nil {
			t.Error(err)
		}
	})
	commands.Config.Execution = CommandExecutionSandboxed
	commands.Profile = ExecutionProfile{PermissionMode: PermissionWorkspace, ApprovalPolicy: ApprovalAutoInSandbox,
		SandboxMode: SandboxWorkspaceWrite, WorkspaceRoot: workspace, FileScope: FileScopeWorkspacePlusAllowedRoots,
		AdditionalRoots: []string{cache}, NetworkPolicy: NetworkPolicy{Mode: NetworkOff},
		CommandPolicy: CommandExecutionPolicy{Enabled: true, Execution: CommandExecutionSandboxed}, Generation: "1"}
	prepared, err := commands.Prepare(t.Context(), helperCall(t, "private-browser", secret))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || result.IsError || !strings.Contains(result.Content, "PRIVATE_BROWSER_DENIED") {
		t.Fatalf("sandbox did not isolate browser state: %+v %v", result, err)
	}
}
