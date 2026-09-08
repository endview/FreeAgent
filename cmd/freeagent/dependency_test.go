package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type cmdWindowsTestOuterGoTmpDirStateV1 struct {
	owner   bool
	present bool
	value   string
}

var cmdWindowsTestOuterGoTmpDirV1 cmdWindowsTestOuterGoTmpDirStateV1

func configureCmdNestedGoCommandTempV1(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	state := cmdWindowsTestOuterGoTmpDirV1
	if !state.owner {
		t.Fatal("nested Go command has no owner-captured outer GOTMPDIR")
	}
	if !state.present || state.value == "" {
		return
	}
	command.Env = cmdNestedGoCommandEnvironmentV1(os.Environ(), state.value)
}

func cmdNestedGoCommandEnvironmentV1(environment []string, goTmpDir string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, present := strings.Cut(entry, "=")
		if present && strings.EqualFold(name, "GOTMPDIR") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOTMPDIR="+goTmpDir)
}

func TestProductionDependencyClosureExcludesLegacyRuntime(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goBinary, "list", "-deps", ".")
	configureCmdNestedGoCommandTempV1(t, command)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowed := map[string]struct{}{
		"github.com/endview/freeagent/cmd/freeagent":                  {},
		"github.com/endview/freeagent/internal/actiongateway":         {},
		"github.com/endview/freeagent/internal/actionmaterializer":    {},
		"github.com/endview/freeagent/internal/activationresolver":    {},
		"github.com/endview/freeagent/internal/assemblycompiler":      {},
		"github.com/endview/freeagent/internal/bootstrapseed":         {},
		"github.com/endview/freeagent/internal/channelservice":        {},
		"github.com/endview/freeagent/internal/controlapicontract":    {},
		"github.com/endview/freeagent/internal/controlapipolicy":      {},
		"github.com/endview/freeagent/internal/controlapp":            {},
		"github.com/endview/freeagent/internal/controlconfirmation":   {},
		"github.com/endview/freeagent/internal/controlcontract":       {},
		"github.com/endview/freeagent/internal/controlcursor":         {},
		"github.com/endview/freeagent/internal/controlhandoff":        {},
		"github.com/endview/freeagent/internal/controlhttp":           {},
		"github.com/endview/freeagent/internal/controlmutation":       {},
		"github.com/endview/freeagent/internal/controloverview":       {},
		"github.com/endview/freeagent/internal/controlruntime":        {},
		"github.com/endview/freeagent/internal/controlsession":        {},
		"github.com/endview/freeagent/internal/controlweb":            {},
		"github.com/endview/freeagent/internal/contextcompiler":       {},
		"github.com/endview/freeagent/internal/corecontract":          {},
		"github.com/endview/freeagent/internal/coreloop":              {},
		"github.com/endview/freeagent/internal/currentbackup":         {},
		"github.com/endview/freeagent/internal/currentstore":          {},
		"github.com/endview/freeagent/internal/deepseekcost":          {},
		"github.com/endview/freeagent/internal/deepseekmodel":         {},
		"github.com/endview/freeagent/internal/exactadapter":          {},
		"github.com/endview/freeagent/internal/knowledgecore":         {},
		"github.com/endview/freeagent/internal/learningcontract":      {},
		"github.com/endview/freeagent/internal/localchat":             {},
		"github.com/endview/freeagent/internal/loopbackchannel":       {},
		"github.com/endview/freeagent/internal/memorycore":            {},
		"github.com/endview/freeagent/internal/mcpstdio":              {},
		"github.com/endview/freeagent/internal/moduleconformance":     {},
		"github.com/endview/freeagent/internal/moduleapplyplan":       {},
		"github.com/endview/freeagent/internal/moduleartifactingress": {},
		"github.com/endview/freeagent/internal/moduleartifactstore":   {},
		"github.com/endview/freeagent/internal/moduledisablecontract": {},
		"github.com/endview/freeagent/internal/moduledisabledryrun":   {},
		"github.com/endview/freeagent/internal/modulehandler":         {},
		"github.com/endview/freeagent/internal/modulehost":            {},
		"github.com/endview/freeagent/internal/moduleupgrade":         {},
		"github.com/endview/freeagent/internal/modulesource":          {},
		"github.com/endview/freeagent/internal/remoteactionhttp":      {},
		"github.com/endview/freeagent/internal/runscheduler":          {},
		"github.com/endview/freeagent/internal/s3eval":                {},
		"github.com/endview/freeagent/internal/s3audit":               {},
		"github.com/endview/freeagent/internal/s3cellaudit":           {},
		"github.com/endview/freeagent/internal/safefiletree":          {},
		"github.com/endview/freeagent/internal/wasmaction":            {},
		"github.com/endview/freeagent/sdk/loopapi":                    {},
		"github.com/endview/freeagent/sdk/moduleapi":                  {},
	}
	seen := make(map[string]struct{}, len(allowed))
	for _, dependency := range strings.Fields(string(output)) {
		if !strings.HasPrefix(dependency, projectPrefix) {
			continue
		}
		if _, ok := allowed[dependency]; !ok {
			t.Fatalf("production dependency closure contains unapproved project package %q", dependency)
		}
		seen[dependency] = struct{}{}
	}
	for dependency := range allowed {
		if _, ok := seen[dependency]; !ok {
			t.Fatalf("production dependency closure is missing approved package %q", dependency)
		}
	}
}

func TestCmdNestedGoCommandEnvironmentRestoresOnlyUniqueOuterGoTmpDirV1(t *testing.T) {
	const (
		workingDirectoryEnvironment = "=C:=C:" + `\working-directory`
		outerGoTmpDir               = "D:" + `\outer-go-tmp`
	)
	environment := []string{
		"TEMP=protected",
		"FREEAGENT_CMD_WINDOWS_TEST_TEMP_ROOT_V1=protected",
		"gotmpdir=stale-one",
		workingDirectoryEnvironment,
		"GOTMPDIR=stale-two",
		"TMP=protected",
	}
	got := cmdNestedGoCommandEnvironmentV1(environment, outerGoTmpDir)
	want := []string{
		"TEMP=protected",
		"FREEAGENT_CMD_WINDOWS_TEST_TEMP_ROOT_V1=protected",
		workingDirectoryEnvironment,
		"TMP=protected",
		"GOTMPDIR=" + outerGoTmpDir,
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("nested Go command environment = %q, want %q", got, want)
	}
}
