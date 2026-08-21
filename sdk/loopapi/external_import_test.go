package loopapi_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestExternalImplementationCanImportLoopAPI(t *testing.T) {
	root := repositoryRoot(t)
	temp := t.TempDir()
	goMod := `module example.com/freeagent-loop

go 1.26.5

require github.com/endview/freeagent v0.0.0

replace github.com/endview/freeagent => ` +
		strconv.Quote(filepath.ToSlash(root)) + "\n"
	source := `package external

import (
	"context"

	"github.com/endview/freeagent/sdk/loopapi"
)

type Loop struct{}

func (Loop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{
		RunID: input.RunID,
		Disposition: loopapi.DispositionYielded,
		FrameRevision: 1,
		ReasonCode: "yielded",
	}, nil
}

var _ loopapi.Loop = Loop{}
`
	if err := os.WriteFile(
		filepath.Join(temp, "go.mod"),
		[]byte(goMod),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(temp, "loop.go"),
		[]byte(source),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		filepath.Join(runtime.GOROOT(), "bin", "go"),
		"test",
		"-mod=mod",
		"./...",
	)
	command.Dir = temp
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"external go test failed: %v\n%s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root, err := filepath.Abs(
		filepath.Join(filepath.Dir(current), "..", ".."),
	)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(
		string(content),
		"module github.com/endview/freeagent\n",
	) {
		t.Fatalf("unexpected repository root %s", root)
	}
	return filepath.Clean(root)
}
