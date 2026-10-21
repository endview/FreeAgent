package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionProfileModeMapping(t *testing.T) {
	workspace := t.TempDir()
	config := DefaultConfig(workspace)
	config.Commands.Enabled = true
	config.ComputerFiles = true
	config.ComputerRoots = []string{t.TempDir()}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}

	additional := config.ComputerRoots[0]
	tests := []struct {
		name      string
		selection PermissionSelection
		mode      SandboxMode
		approval  ApprovalPolicy
		scope     FileScope
		network   NetworkMode
		command   CommandExecutionMode
		wantRoots bool
	}{
		{name: "ask", selection: PermissionSelection{Mode: PermissionAsk, Source: "turn"},
			mode: SandboxReadOnly, approval: ApprovalAsk, scope: FileScopeWorkspace,
			network: NetworkOff, command: CommandExecutionSandboxed},
		{name: "workspace", selection: PermissionSelection{Mode: PermissionWorkspace, Source: "turn"},
			mode: SandboxWorkspaceWrite, approval: ApprovalAutoInSandbox, scope: FileScopeWorkspace,
			network: NetworkOff, command: CommandExecutionSandboxed},
		{name: "workspace selected roots", selection: PermissionSelection{Mode: PermissionWorkspace, Source: "turn",
			ComputerFiles: true, ComputerRoots: []string{additional}, FileScope: FileScopeSelectedRoots},
			mode: SandboxSelectedRoots, approval: ApprovalAutoInSandbox, scope: FileScopeSelectedRoots,
			network: NetworkOff, command: CommandExecutionSandboxed, wantRoots: true},
		{name: "full", selection: PermissionSelection{Mode: PermissionFull, Source: "turn"},
			mode: SandboxHostFull, approval: ApprovalNeverHost, scope: FileScopeHostAll,
			network: NetworkHostInherited, command: CommandExecutionHostExplicit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, err := buildExecutionProfile(config, test.selection)
			if err != nil {
				t.Fatal(err)
			}
			if profile.SandboxMode != test.mode || profile.ApprovalPolicy != test.approval ||
				profile.FileScope != test.scope || profile.NetworkPolicy.Mode != test.network ||
				profile.CommandPolicy.Execution != test.command {
				t.Fatalf("profile=%+v", profile)
			}
			if test.wantRoots && len(profile.AdditionalRoots) != 1 {
				t.Fatalf("selected roots=%v", profile.AdditionalRoots)
			}
			if !test.wantRoots && test.scope == FileScopeWorkspace && profile.FileScope != FileScopeWorkspace {
				t.Fatalf("workspace scope=%s", profile.FileScope)
			}
		})
	}
}

func TestHostCommandBackendCanBeConfiguredBeforeFullAccessIsSelected(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.Commands.Enabled = true
	config.Commands.Execution = CommandExecutionHostExplicit
	if err := config.Validate(); err != nil {
		t.Fatalf("host backend should be valid with Ask as the global default: %v", err)
	}

	askProfile, err := buildExecutionProfile(config, PermissionSelection{Mode: PermissionAsk, Source: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if askProfile.CommandPolicy.Execution != CommandExecutionHostExplicit {
		t.Fatalf("Ask profile did not retain the configured backend: %+v", askProfile.CommandPolicy)
	}

	fullProfile, err := buildExecutionProfile(config, PermissionSelection{Mode: PermissionFull, Source: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	if fullProfile.CommandPolicy.Execution != CommandExecutionHostExplicit || fullProfile.SandboxMode != SandboxHostFull {
		t.Fatalf("Full access did not enable the configured host backend: %+v", fullProfile)
	}
}

func TestDefaultCommandRulesCoverCommonWindowsCommands(t *testing.T) {
	rules := defaultCommandRules()
	wantAsk := map[string]bool{"cmd": true, "powershell": true, "pwsh": true, "pytest": true}
	for _, rule := range rules {
		if wantAsk[rule.Program] {
			if rule.Decision != CommandRuleAsk {
				t.Fatalf("default rule %q=%q, want ask", rule.Program, rule.Decision)
			}
			delete(wantAsk, rule.Program)
		}
	}
	if len(wantAsk) != 0 {
		t.Fatalf("missing default command rules: %v", wantAsk)
	}
}

func TestExecutionProfileSnapshotRetainsFrozenContract(t *testing.T) {
	service, _, workspace := testHarness(t, permissionCreateModel())
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	if err := tasks.SelectPermission(t.Context(), chat, PermissionWorkspace, false); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "profile-frozen", "create a file")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	snapshot, err := decodeRunSnapshot(result.Run.ConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ExecutionProfile.PermissionMode != PermissionWorkspace ||
		snapshot.ExecutionProfile.SandboxMode != SandboxWorkspaceWrite ||
		snapshot.ExecutionProfile.Generation != snapshot.ToolGeneration {
		t.Fatalf("profile snapshot=%+v", snapshot.ExecutionProfile)
	}

	base := tasks.RuntimePolicy().Generation
	if _, err := tasks.ConfigureRuntimePolicy(t.Context(), base, func(config *Config) error {
		config.Commands.Enabled = true
		config.Commands.TimeoutSecs = 4
		config.Commands.MaxOutput = 4096
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bound, err := service.forRun(result.Run)
	if err != nil {
		t.Fatalf("rebind frozen run: %v", err)
	}
	if profile := bound.activeToolPolicy().Profile; profile.PermissionMode != PermissionWorkspace ||
		profile.SandboxMode != SandboxWorkspaceWrite || profile.Generation != snapshot.ExecutionProfile.Generation {
		t.Fatalf("rebound profile=%+v", profile)
	}
	if raw, err := os.ReadFile(filepath.Join(workspace, "permission.txt")); err != nil || string(raw) != "permission" {
		t.Fatalf("workspace file=%q err=%v", raw, err)
	}
}

func TestExecutionProfileSnapshotRejectsMismatchedPolicy(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	raw, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRunSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoded.ExecutionProfile.FileScope = FileScopeHostAll
	corrupt, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRunSnapshot(string(corrupt)); !errors.Is(err, ErrSnapshotIncomplete) {
		t.Fatalf("mismatched profile accepted: %v", err)
	}
}
