//go:build windows

package harness

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeToolchainGrantCost(t *testing.T) {
	program := os.Getenv("FREEAGENT_NATIVE_GIT")
	if program == "" {
		t.Skip("NOT_RUN: set FREEAGENT_NATIVE_GIT to measure the installed toolchain explicitly")
	}
	service, tools, _ := commandFixture(t)
	tools.Config.TimeoutSecs = 30
	tools.Config.Execution = CommandExecutionSandboxed
	tools.DataDir = service.Store.DataDir()
	tools.Profile = ExecutionProfile{Generation: "native-probe", SandboxMode: SandboxWorkspaceWrite, FileScope: FileScopeWorkspace, NetworkPolicy: NetworkPolicy{Mode: NetworkOff}}
	t.Cleanup(func() {
		if err := releaseSandboxGrants(tools.DataDir); err != nil {
			t.Error(err)
		}
	})
	for i := range 2 {
		began := time.Now()
		prepared, err := tools.Prepare(t.Context(), makeCall("native-git", "command_run", commandArguments{Command: program, Args: []string{"--version"}, Cwd: "."}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := prepared.Execute(t.Context())
		if err != nil || result.IsError || !strings.Contains(result.Content, "git version") {
			t.Fatalf("native git %+v %v", result, err)
		}
		t.Logf("launch %d elapsed=%s installation=%s backend=%s cleanup=%s", i+1, time.Since(began), result.Receipts[0].InstallationRoot, result.Receipts[0].Backend, result.Receipts[0].Cleanup)
	}
}
