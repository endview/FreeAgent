package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

// TestMain points the default keys file at a temporary directory so no CLI
// test can read or write the user's real keys.
func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "--freeagent-cli-extension-helper" {
			os.Exit(runExtensionCLITestHelper())
		}
	}
	dir, err := os.MkdirTemp("", "freeagent-cli-keys-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv(apppaths.KeysFileEnv, filepath.Join(dir, "keys.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// cliConfig is a fresh configuration whose only model is the default.
func cliConfig(workspace string, model harness.ModelConfig) harness.Config {
	config := harness.DefaultConfig(workspace)
	config.Models = map[string]harness.ModelConfig{"default": model}
	config.DefaultModel = "default"
	return config
}

var fixtureKeyCount atomic.Int64

// fixtureKey adds a uniquely named profile to the test keys file and returns
// its id. Every test shares the file, so the tests stay parallel-safe.
func fixtureKey(t *testing.T, baseURL, source, env, secret string) string {
	t.Helper()
	path, err := apppaths.KeysFile()
	if err != nil {
		t.Fatal(err)
	}
	view, err := harness.NewKeyStore(path).Add(harness.KeyInput{
		Name: fmt.Sprintf("cli fixture %d", fixtureKeyCount.Add(1)), BaseURL: baseURL,
		Source: source, EnvName: env, Secret: []byte(secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	return view.ID
}
