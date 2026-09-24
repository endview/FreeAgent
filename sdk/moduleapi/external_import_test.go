package moduleapi_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// This test compiles the current SDK from a separate Go module. It prevents
// accidental reliance on internal packages or on retired public contracts.
func TestExternalModuleCanImportCurrentPublicAPI(t *testing.T) {
	tempModule := t.TempDir()
	repositoryRoot := currentRepositoryRoot(t)
	fixtureRoot := filepath.Join(
		repositoryRoot,
		"sdk",
		"moduleapi",
		"testdata",
		"compat",
		"v1",
	)
	manifestCanonical, err := os.ReadFile(filepath.Join(
		fixtureRoot,
		"declarative-role",
		"module.yaml",
	))
	if err != nil {
		t.Fatal(err)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || !reflect.DeepEqual(manifestCanonical, canonical) ||
		manifest.ID != "freeagent.compat.role" {
		t.Fatalf("compat manifest=%+v canonical=%t err=%v", manifest, reflect.DeepEqual(manifestCanonical, canonical), err)
	}
	wantPorts := []moduleapi.PortRef{
		{Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2},
		{Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1},
		{Name: moduleapi.PortNameActionProvider, ExactVersion: moduleapi.PortVersionV1},
		{Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1},
	}
	if ports := moduleapi.S1PortRefs(); !reflect.DeepEqual(ports, wantPorts) {
		t.Fatalf("registered exact ports=%+v want=%+v", ports, wantPorts)
	}

	goMod := `module example.com/freeagent-extension

go 1.26.5

require github.com/endview/freeagent v0.0.0

replace github.com/endview/freeagent => ` +
		strconv.Quote(filepath.ToSlash(repositoryRoot)) + "\n"
	source, err := os.ReadFile(filepath.Join(
		fixtureRoot,
		"go-action-provider",
		"extension.go",
	))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(tempModule, "go.mod"),
		[]byte(goMod),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(tempModule, "extension_test.go"),
		[]byte(source),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goExecutable, "test", "-mod=mod", "./...")
	command.Dir = tempModule
	command.Env = append(
		os.Environ(),
		"GOWORK=off",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOVCS=*:off",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"external go test failed: %v\n%s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
}

func currentRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the external import test path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(testFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read derived repository go.mod %s: %v", goModPath, err)
	}
	if !strings.HasPrefix(
		string(goMod),
		"module github.com/endview/freeagent\n",
	) {
		t.Fatalf("derived repository root %s has unexpected go.mod", root)
	}
	return filepath.Clean(root)
}
