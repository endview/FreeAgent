package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/zhipumodel"
)

func TestZhipuRuntimeFlagsAreDefaultOffAndRequireExplicitEnable(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := bindZhipuRuntimeFlags(flags)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	config, err := values.config(flags)
	if err != nil || config != nil {
		t.Fatalf("default Zhipu config=%+v err=%v", config, err)
	}

	flags = flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values = bindZhipuRuntimeFlags(flags)
	if err := flags.Parse([]string{"--zhipu-api-key-env", "TEST_ZHIPU_KEY"}); err != nil {
		t.Fatal(err)
	}
	if _, err := values.config(flags); err == nil || !strings.Contains(err.Error(), "requires explicit --enable-zhipu") {
		t.Fatalf("error=%v", err)
	}
}

func TestEnvironmentZhipuResolverIsLazyAndRejectsMismatchedRef(t *testing.T) {
	lookups := 0
	resolver := &environmentZhipuAPIKeyResolver{name: "TEST_ZHIPU_KEY", lookup: func(name string) (string, bool) {
		lookups++
		if name != "TEST_ZHIPU_KEY" {
			t.Fatal(name)
		}
		return "not-a-secret", true
	}}
	if lookups != 0 {
		t.Fatal("credential was read during construction")
	}
	first, err := resolver.ResolveAPIKey(context.Background(), zhipumodel.APIKeyIdentity{})
	if err != nil || string(first) != "not-a-secret" || lookups != 1 {
		t.Fatalf("first=%q err=%v lookups=%d", first, err, lookups)
	}
	first[0] = 'X'
	second, err := resolver.ResolveAPIKey(context.Background(), zhipumodel.APIKeyIdentity{})
	if err != nil || string(second) != "not-a-secret" || lookups != 2 {
		t.Fatalf("second=%q err=%v lookups=%d", second, err, lookups)
	}
	resolver.reference = "env:TEST_ZHIPU_KEY"
	mismatchedReference := "wrong"
	if _, err := resolver.ResolveAPIKey(context.Background(), zhipumodel.APIKeyIdentity{SecretRef: mismatchedReference}); err == nil || lookups != 2 {
		t.Fatalf("mismatched ref err=%v lookups=%d", err, lookups)
	}
}

func TestRunChatRejectsZhipuCredentialSourceWithoutEnable(t *testing.T) {
	err := run(context.Background(), []string{"chat", "--db", "unused.sqlite", "--message", "unused", "--zhipu-api-key-env", "TEST_ZHIPU_KEY"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "requires explicit --enable-zhipu") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunServeAcceptsExplicitZhipuRuntimeWithoutResolvingSecret(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(filepath.Dir(exampleSeedPath(t)), "current-v1.zhipu.bootstrap.seed.json")
	if _, err := initializeProductionData(context.Background(), initInput{DatabasePath: databasePath, SeedPath: seedPath, ArtifactRoot: artifactRoot}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		err := run(ctx, []string{"serve", "--db", databasePath, "--artifact-root", artifactRoot, "--listen", "127.0.0.1:0", "--enable-zhipu", "--zhipu-api-key-env", "TEST_ZHIPU_KEY_NOT_SET"}, writer, io.Discard)
		_ = writer.CloseWithError(err)
		result <- err
	}()
	var ready map[string]string
	decodeErr := json.NewDecoder(reader).Decode(&ready)
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Zhipu serve did not stop")
	}
	if decodeErr != nil {
		t.Fatalf("decode readiness: %v", decodeErr)
	}
	if ready["zhipu_adapter"] != "enabled" || ready["listen"] == "" {
		t.Fatalf("readiness=%+v", ready)
	}
}
