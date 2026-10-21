package harness

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
)

// TestMain points the default keys file at a temporary directory so no test
// can read or write the user's real keys.
func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "--freeagent-extension-helper" {
			os.Exit(runExtensionHelper())
		}
		if arg == "--freeagent-extension-host-helper" {
			os.Exit(runExtensionHostHelper())
		}
	}
	dir, err := os.MkdirTemp("", "freeagent-harness-keys-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv(apppaths.KeysFileEnv, filepath.Join(dir, "keys.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testEchoConfig is the configuration most tests used before DefaultConfig
// stopped shipping a model.
func testEchoConfig(workspace string) Config {
	config := DefaultConfig(workspace)
	config.Models = map[string]ModelConfig{"default": {Provider: "echo"}}
	config.DefaultModel = "default"
	return config
}

func testKeyStore(t *testing.T) *KeyStore {
	t.Helper()
	return NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
}

// mustResolved wraps NewResolvedKey for fixtures whose address is known good.
func mustResolved(key ResolvedKey, err error) ResolvedKey {
	if err != nil {
		panic(err)
	}
	return key
}

// keyedClient resolves config.Key the way a dispatch does and builds the client.
func keyedClient(t *testing.T, config ModelConfig, keys *KeyStore, httpClient *http.Client) *OpenAICompatibleClient {
	t.Helper()
	key, err := keys.Resolve(config.Key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewOpenAICompatibleClient(config, key, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

var testProfileCount atomic.Int64

// addTestProfile adds a uniquely named profile and returns its id.
func addTestProfile(t *testing.T, keys *KeyStore, base, source, env, secret string) string {
	t.Helper()
	view, err := keys.Add(KeyInput{
		Name: fmt.Sprintf("test profile %d", testProfileCount.Add(1)), BaseURL: base,
		Source: source, EnvName: env, Secret: []byte(secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	return view.ID
}
