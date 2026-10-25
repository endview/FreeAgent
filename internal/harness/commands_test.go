package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

func TestCommandHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--command-fixture" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	switch args[0] {
	case "ok":
		fmt.Print("check really ran")
		os.Exit(0)
	case "fail":
		fmt.Print("check failed")
		os.Exit(7)
	case "env":
		fmt.Print(os.Getenv("FREEAGENT_COMMAND_PRIVATE"))
		os.Exit(0)
	case "loud":
		fmt.Print(strings.Repeat("output", 10000))
		os.Exit(0)
	case "read-resource":
		program, _ := os.Executable()
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(program), "..", "share", "resource.txt"))
		if err != nil {
			fmt.Print(err)
			os.Exit(3)
		}
		fmt.Print(string(raw))
		os.Exit(0)
	case "mkdir":
		if err := os.Mkdir(args[1], 0o700); err != nil {
			fmt.Print(err)
			os.Exit(3)
		}
		os.Exit(0)
	case "effect-sleep":
		_ = os.WriteFile(args[1], []byte("already changed"), 0o600)
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "child":
		for {
			_ = os.WriteFile(args[1], []byte(time.Now().String()), 0o600)
			time.Sleep(20 * time.Millisecond)
		}
	case "parent":
		program, _ := os.Executable()
		child := exec.Command(program, "-test.run=TestCommandHelper", "--", "--command-fixture", "child", args[1])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	os.Exit(9)
}

func commandFixture(t *testing.T) (*Service, *CommandTools, string) {
	t.Helper()
	service, files, workspace := testHarness(t, EchoModel{})
	config := CommandConfig{Enabled: true, TimeoutSecs: 5, MaxOutput: 1024}
	commands := &CommandTools{Files: files, Workspace: workspace, Config: config}
	runtimeTools := NewRuntimeTools(files)
	runtimeTools.Commands = commands
	service.Tools = runtimeTools
	service.Config.Commands = config
	return service, commands, workspace
}

func helperCall(t *testing.T, mode string, extra ...string) ToolCall {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-test.run=TestCommandHelper", "--", "--command-fixture", mode}, extra...)
	return makeCall("command", "command_run", commandArguments{Command: program, Args: args, Cwd: ".", Check: true})
}

func TestCommandsOptInAndEnvironmentPolicy(t *testing.T) {
	service, commands, _ := commandFixture(t)
	commands.Config.Enabled = false
	if len(commands.Definitions()) != 0 {
		t.Fatal("disabled commands advertised")
	}
	if _, err := commands.Prepare(t.Context(), helperCall(t, "ok")); err == nil {
		t.Fatal("disabled command accepted")
	}
	commands.Config.Enabled = true
	t.Setenv("FREEAGENT_COMMAND_PRIVATE", "secret-fixture")
	for _, inherit := range []bool{false, true} {
		commands.Config.InheritEnv = inherit
		prepared, err := commands.Prepare(t.Context(), helperCall(t, "env"))
		if err != nil || !strings.Contains(prepared.Preview, "NOT a sandbox") {
			t.Fatalf("preview=%+v err=%v", prepared, err)
		}
		result, err := prepared.Execute(t.Context())
		if err != nil || strings.Contains(result.Content, "secret-fixture") {
			t.Fatalf("inherit=%t result=%+v err=%v", inherit, result, err)
		}
	}
	service.Config.Commands.Enabled = false
}

func TestSandboxedCommandsUseWindowsWorker(t *testing.T) {
	_, commands, _ := commandFixture(t)
	commands.Config.Execution = CommandExecutionSandboxed
	commands.Profile = ExecutionProfile{PermissionMode: PermissionWorkspace, ApprovalPolicy: ApprovalAutoInSandbox,
		SandboxMode: SandboxWorkspaceWrite, WorkspaceRoot: commands.Workspace, FileScope: FileScopeWorkspace,
		NetworkPolicy: NetworkPolicy{Mode: NetworkOff}, CommandPolicy: CommandExecutionPolicy{Enabled: true, Execution: CommandExecutionSandboxed}, Generation: "1"}
	prepared, err := commands.Prepare(t.Context(), helperCall(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || len(result.Receipts) != 1 {
		t.Fatalf("sandbox result=%+v err=%v", result, err)
	}
	if runtime.GOOS == "windows" {
		if result.Receipts[0].Status != "completed" || result.Receipts[0].ExitCode == nil || *result.Receipts[0].ExitCode != 0 {
			t.Fatalf("sandbox worker result=%+v err=%v", result, err)
		}
	} else if result.Receipts[0].Status != "unsupported" {
		t.Fatalf("non-Windows sandbox result=%+v err=%v", result, err)
	}
}

func TestHostCommandRequiresFullProfile(t *testing.T) {
	_, commands, _ := commandFixture(t)
	commands.Config.Execution = CommandExecutionHostExplicit
	commands.Profile = ExecutionProfile{PermissionMode: PermissionWorkspace, ApprovalPolicy: ApprovalAutoInSandbox,
		SandboxMode: SandboxWorkspaceWrite, WorkspaceRoot: commands.Workspace, FileScope: FileScopeWorkspace,
		NetworkPolicy: NetworkPolicy{Mode: NetworkOff}, CommandPolicy: CommandExecutionPolicy{Enabled: true, Execution: CommandExecutionHostExplicit}, Generation: "1"}
	if _, err := commands.Prepare(t.Context(), helperCall(t, "ok")); !errors.Is(err, ErrHostExecutionRequiresFull) {
		t.Fatalf("host command error=%v", err)
	}
}

func TestCommandRulesUseStructuredPrecedence(t *testing.T) {
	service, commands, _ := commandFixture(t)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(program)
	commands.Config.Rules = []CommandRule{
		{Program: name, Decision: CommandRuleAllow},
	}
	prepared, err := commands.Prepare(t.Context(), helperCall(t, "ok"))
	if err != nil || !prepared.ruleAllowed || service.needsApproval(prepared) {
		t.Fatalf("allow rule prepared=%+v err=%v", prepared, err)
	}
	commands.Config.Rules = append(commands.Config.Rules, CommandRule{Program: name, Decision: CommandRuleDeny})
	if _, err := commands.Prepare(t.Context(), helperCall(t, "ok")); err == nil {
		t.Fatal("deny rule did not override allow")
	}
}

func TestCommandsEvidenceFromRuntimeNotModelClaims(t *testing.T) {
	for _, mode := range []string{"ok", "fail", "loud"} {
		t.Run(mode, func(t *testing.T) {
			service, _, _ := commandFixture(t)
			calls := 0
			service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls++
				if calls == 1 {
					return ModelResponse{ToolCalls: []ToolCall{helperCall(t, mode)}}, nil
				}
				return ModelResponse{Content: "All checks passed! (untrusted model claim)"}, nil
			})
			service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
			result, err := service.Run(t.Context(), conversation(t, service), "check", "check project")
			if err != nil || result.Run.Status != "completed" || len(result.Evidence.Commands) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if (len(result.Evidence.ChecksPassed) == 1) != (mode == "ok") {
				t.Fatalf("fabricated evidence: %+v", result.Evidence)
			}
			if result.Evidence.Commands[0].Truncated != (mode == "loud") {
				t.Fatalf("truncation lost: %+v", result.Evidence)
			}
		})
	}
}

func TestCommandTimeoutRetainsEffectsAndStopsChildren(t *testing.T) {
	_, commands, workspace := commandFixture(t)
	commands.Config.TimeoutSecs = 1
	for _, mode := range []string{"effect-sleep", "parent"} {
		marker := filepath.Join(workspace, mode+".txt")
		prepared, err := commands.Prepare(t.Context(), helperCall(t, mode, marker))
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		result, err := prepared.Execute(t.Context())
		if err != nil || result.Receipts[0].Status != "timed_out" || time.Since(start) > 4*time.Second {
			t.Fatalf("result=%+v err=%v duration=%s", result, err, time.Since(start))
		}
		raw, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal("timeout erased or failed to retain side effect")
		}
		time.Sleep(120 * time.Millisecond)
		after, _ := os.ReadFile(marker)
		if string(after) != string(raw) {
			t.Fatal("child remained active after cleanup")
		}
		if len(result.Receipts) < 2 || result.Receipts[1].Status != "observed_change" {
			t.Fatalf("missing observed effect: %+v", result)
		}
	}
}

func TestCommandCancelledAndDeclinedNotLaunched(t *testing.T) {
	service, commands, workspace := commandFixture(t)
	marker := filepath.Join(workspace, "must-not-exist")
	prepared, err := commands.Prepare(t.Context(), helperCall(t, "effect-sleep", marker))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := prepared.Execute(ctx)
	if err != nil || result.Receipts[0].Status != "not_launched" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	calls := 0
	service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{helperCall(t, "effect-sleep", marker)}}, nil
		}
		return ModelResponse{Content: "Check not run."}, nil
	})
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return false, nil }
	completed, err := service.Run(t.Context(), conversation(t, service), "decline", "check")
	if err != nil || len(completed.Evidence.Commands) != 0 || len(completed.Evidence.ChecksNotRun) != 1 {
		t.Fatalf("result=%+v err=%v", completed, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("declined command had side effects")
	}
}

func TestCommandAllowlistStillNeedsExactApproval(t *testing.T) {
	service, commands, workspace := commandFixture(t)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands.Config.AllowedExecutables = []string{program}
	service.Config.Commands = commands.Config
	disallowed := helperCall(t, "ok")
	var args commandArguments
	_ = json.Unmarshal(disallowed.Arguments, &args)
	args.Command = filepath.Join(workspace, "not-allowed")
	disallowed = makeCall("denied", "command_run", args)
	if _, err := commands.Prepare(t.Context(), disallowed); err == nil {
		t.Fatal("disallowed executable accepted")
	}
	calls := 0
	service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{helperCall(t, "ok")}}, nil
		}
		return ModelResponse{Content: "actual check complete"}, nil
	})
	tasks := NewTaskService(service)
	waiting, err := tasks.Submit(t.Context(), "", "allowed", "check")
	if err != nil || waiting.Run.Status != "waiting_confirmation" || len(waiting.Evidence.Commands) != 0 {
		t.Fatalf("allowlist bypassed approval: %+v err=%v", waiting, err)
	}
	decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil || !strings.Contains(decision.Preview, "exact configured path") {
		t.Fatalf("preview=%+v err=%v", decision, err)
	}
	completed, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision, true)
	if err != nil || len(completed.Evidence.ChecksPassed) != 1 || len(completed.Evidence.Commands) != 1 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	for _, paths := range [][]string{{"relative"}, {program, program}} {
		if validateCommandAllowlist(paths) == nil {
			t.Fatalf("invalid allowlist accepted: %v", paths)
		}
	}
	if !commandAllowed(filepath.Dir(program)+string(filepath.Separator)+"."+string(filepath.Separator)+filepath.Base(program), []string{program}) {
		t.Fatal("normalized path alias did not match policy")
	}
}

func TestSandboxSpecScopesRootsAndProtectsGit(t *testing.T) {
	_, commands, workspace := commandFixture(t)
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	commands.DataDir = filepath.Join(t.TempDir(), "data")
	commands.Profile = ExecutionProfile{SandboxMode: SandboxWorkspaceWrite, FileScope: FileScopeWorkspace,
		AdditionalRoots: []string{other}, NetworkPolicy: NetworkPolicy{Mode: NetworkOff}}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	build := func() (winlaunch.Spec, map[string]winlaunch.ProtectMode, bool) {
		t.Helper()
		spec := winlaunch.Spec{Command: program}
		if err := commands.sandboxSpec(&spec); err != nil {
			t.Fatal(err)
		}
		modes := map[string]winlaunch.ProtectMode{}
		for _, protected := range spec.ProtectedPaths {
			modes[filepath.Base(protected.Path)] = protected.Mode
		}
		otherGranted := false
		for _, grant := range spec.Grants {
			otherGranted = otherGranted || samePath(grant.Root, other)
		}
		return spec, modes, otherGranted
	}

	spec, modes, otherGranted := build()
	if otherGranted {
		t.Fatal("a workspace-only file scope granted an additional root to commands")
	}
	if spec.AppContainerName == "" || !spec.Grants[0].Keep || !samePath(spec.Grants[0].Root, workspace) {
		t.Fatalf("workspace grant is not kept per data directory: %+v", spec)
	}
	if modes[".git"] != winlaunch.ProtectReadOnly || modes[".freeagent"] != winlaunch.ProtectReadOnly || modes["hooks"] != "" {
		t.Fatalf("default git protection = %v", modes)
	}

	commands.Config.GitWrite = true
	commands.Profile.FileScope = FileScopeWorkspacePlusAllowedRoots
	_, modes, otherGranted = build()
	if !otherGranted {
		t.Fatal("selected roots were not granted to commands")
	}
	if modes[".git"] != winlaunch.ProtectGitWrite || modes["hooks"] != winlaunch.ProtectReadOnly || modes["config"] != winlaunch.ProtectReadOnly {
		t.Fatalf("git_write protection = %v", modes)
	}

	commands.Profile.SandboxMode = SandboxReadOnly
	spec, _, _ = build()
	if len(spec.ProtectedPaths) != 0 || spec.Grants[0].Access != winlaunch.AccessRead || !strings.HasSuffix(spec.AppContainerName, ".ro") {
		t.Fatalf("read-only sandbox spec = %+v", spec)
	}
}

func TestSandboxedCommandMarksCreatedGitMetadata(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the AppContainer sandbox is Windows-only")
	}
	_, commands, workspace := commandFixture(t)
	commands.DataDir = filepath.Join(t.TempDir(), "data")
	t.Cleanup(func() {
		if err := releaseSandboxGrants(commands.DataDir); err != nil {
			t.Errorf("release sandbox grants: %v", err)
		}
	})
	commands.Config.Execution = CommandExecutionSandboxed
	commands.Profile = ExecutionProfile{PermissionMode: PermissionWorkspace, ApprovalPolicy: ApprovalAutoInSandbox,
		SandboxMode: SandboxWorkspaceWrite, WorkspaceRoot: workspace, FileScope: FileScopeWorkspace,
		NetworkPolicy: NetworkPolicy{Mode: NetworkOff}, CommandPolicy: CommandExecutionPolicy{Enabled: true, Execution: CommandExecutionSandboxed}, Generation: "1"}
	run := func(path string) ToolReceipt {
		t.Helper()
		prepared, err := commands.Prepare(t.Context(), helperCall(t, "mkdir", path))
		if err != nil {
			t.Fatal(err)
		}
		result, err := prepared.Execute(t.Context())
		if err != nil {
			t.Fatalf("sandboxed mkdir result=%+v err=%v", result, err)
		}
		return result.Receipts[0]
	}
	created := run(filepath.Join(workspace, ".git"))
	if created.Status != "completed" || created.ExitCode == nil || *created.ExitCode != 0 || !created.GitMetadataCreated {
		t.Fatalf("creating .git was not marked: %+v", created)
	}
	// The new repository is now protected like any other: readable, not writable.
	blocked := run(filepath.Join(workspace, ".git", "objects"))
	if blocked.ExitCode == nil || *blocked.ExitCode == 0 || blocked.GitMetadataCreated {
		t.Fatalf("existing .git stayed writable without git_write: %+v", blocked)
	}
}
