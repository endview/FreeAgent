//go:build windows

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func scoopFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	shim := filepath.Join(root, "shims", "tool.exe")
	target := filepath.Join(root, "apps", "tool", "1.0", "bin", "tool.exe")
	for _, path := range []string{shim, target} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := strings.TrimSuffix(shim, ".exe") + ".shim"
	return shim, target, metadata
}

func TestScoopTargetBindsArgumentsAndInstallationRoot(t *testing.T) {
	shim, program, metadata := scoopFixture(t)
	if err := os.WriteFile(metadata, []byte("path = \""+program+"\"\r\nargs = --prefix \"two words\"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := resolveCommandTarget(shim, []string{"--user"})
	if err != nil || !samePath(target.program, program) || !slices.Equal(target.args, []string{"--prefix", "two words", "--user"}) || !samePath(target.root, filepath.Dir(filepath.Dir(program))) {
		t.Fatalf("target: %+v %v", target, err)
	}
	_, commands, _ := commandFixture(t)
	commands.Config.AllowedExecutables = []string{shim}
	call := makeCall("shim", "command_run", commandArguments{Command: shim, Cwd: "."})
	if _, err := commands.Prepare(t.Context(), call); err == nil {
		t.Fatal("shim allowlist bypassed final executable check")
	}
	commands.Config.AllowedExecutables = []string{program}
	commands.Profile.Generation = "rules"
	commands.Config.Rules = []CommandRule{{Program: "tool.exe", Args: []string{"--unexpanded"}, Decision: CommandRuleAllow}}
	if _, err := commands.Prepare(t.Context(), call); err == nil {
		t.Fatal("command rule ignored shim prefix arguments")
	}
	commands.Config.Rules[0].Args = []string{"--prefix", "two words"}
	prepared, err := commands.Prepare(t.Context(), call)
	if err != nil || !strings.Contains(prepared.Preview, "two words") || !strings.Contains(prepared.Preview, target.root) {
		t.Fatalf("preview: %+v %v", prepared, err)
	}
	if err := os.WriteFile(metadata, []byte("path = \""+program+"\"\nargs = --changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || !result.IsError || len(result.Receipts) != 1 || result.Receipts[0].Status != "blocked" {
		t.Fatalf("changed shim executed: %+v %v", result, err)
	}
}

func TestScoopTargetRejectsUnknownAndOutsideMetadata(t *testing.T) {
	shim, program, metadata := scoopFixture(t)
	for _, body := range []string{
		"path = \"" + program + "\"\npath = \"" + program + "\"", "path = \"" + program + "\"\nother = ignored",
		"path = \"" + filepath.Join(t.TempDir(), "outside.exe") + "\"", "path = relative.exe", strings.Repeat("x", 32769),
	} {
		if err := os.WriteFile(metadata, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveCommandTarget(shim, nil); err == nil {
			t.Fatalf("unsafe metadata accepted: %.80s", body)
		}
	}
	if _, err := resolveCommandTarget(strings.TrimSuffix(shim, ".exe")+".cmd", nil); err == nil {
		t.Fatal("script shim accepted")
	}
}

func TestScoopCurrentJunctionAndSandboxSiblingResources(t *testing.T) {
	shim, program, metadata := scoopFixture(t)
	installation := filepath.Dir(filepath.Dir(program))
	current := filepath.Join(filepath.Dir(installation), "current")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", current, installation).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %s %v", output, err)
	}
	// Remove the junction itself; never recursively remove its resolved target.
	t.Cleanup(func() { _ = os.Remove(current) })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, raw, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(installation, "share"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installation, "share", "resource.txt"), []byte("SIBLING_RESOURCE_READ"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(current, "bin", "tool.exe")
	resolvedAlias, aliasErr := canonicalCommandPath(alias)
	resolvedRoot, rootErr := canonicalCommandPath(current)
	if aliasErr != nil || rootErr != nil || !samePath(resolvedAlias, program) || !samePath(resolvedRoot, installation) {
		link, linkErr := os.Readlink(current)
		info, statErr := os.Lstat(current)
		t.Fatalf("junction resolution: program=%q root=%q errors=%v/%v link=%q/%v stat=%+v/%v", resolvedAlias, resolvedRoot, aliasErr, rootErr, link, linkErr, info, statErr)
	}
	if err := os.WriteFile(metadata, []byte("path = \""+alias+"\"\nargs = -test.run=TestCommandHelper -- --command-fixture read-resource"), 0600); err != nil {
		t.Fatal(err)
	}
	_, commands, _ := commandFixture(t)
	commands.Config.Execution = CommandExecutionSandboxed
	commands.Config.AllowedExecutables = []string{alias}
	commands.Config.Rules = []CommandRule{{Program: alias, Args: []string{"-test.run=TestCommandHelper", "--", "--command-fixture", "read-resource"}, Decision: CommandRuleAllow}}
	commands.Profile = ExecutionProfile{PermissionMode: PermissionWorkspace, SandboxMode: SandboxWorkspaceWrite,
		NetworkPolicy: NetworkPolicy{Mode: NetworkOff}, Generation: "shim-test"}
	prepared, err := commands.Prepare(t.Context(), makeCall("shim", "command_run", commandArguments{Command: shim, Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || result.IsError || !strings.Contains(result.Content, "SIBLING_RESOURCE_READ") ||
		result.Receipts[0].Backend != "appcontainer" || !samePath(result.Receipts[0].InstallationRoot, installation) {
		t.Fatalf("native sandbox resource: %+v %v", result, err)
	}
}
