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

	"github.com/endview/freeagent/internal/deepseekmodel"
)

func TestDeepSeekRuntimeFlagsAreDefaultOffAndRequireExplicitEnable(
	t *testing.T,
) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := bindDeepSeekRuntimeFlags(flags)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	config, err := values.config(flags)
	if err != nil || config != nil {
		t.Fatalf("default DeepSeek config=%+v err=%v", config, err)
	}

	flags = flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values = bindDeepSeekRuntimeFlags(flags)
	if err := flags.Parse([]string{
		"--deepseek-api-key-env", "TEST_DEEPSEEK_KEY",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := values.config(flags); err == nil ||
		!strings.Contains(err.Error(), "requires explicit --enable-deepseek") {
		t.Fatalf("disabled explicit credential source error=%v", err)
	}
}

func TestEnvironmentDeepSeekResolverIsLazyOwnedAndDoesNotLeak(
	t *testing.T,
) {
	const secret = "not-a-secret"
	lookups := 0
	resolver := &environmentDeepSeekAPIKeyResolver{
		name: "TEST_DEEPSEEK_KEY",
		lookup: func(name string) (string, bool) {
			lookups++
			if name != "TEST_DEEPSEEK_KEY" {
				t.Fatalf("lookup name=%q", name)
			}
			return secret, true
		},
	}
	if lookups != 0 {
		t.Fatal("credential was read during resolver construction")
	}
	first, err := resolver.ResolveAPIKey(
		context.Background(),
		deepseekmodel.APIKeyIdentity{},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.ResolveAPIKey(
		context.Background(),
		deepseekmodel.APIKeyIdentity{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if lookups != 2 || string(first) != secret || string(second) != secret {
		t.Fatalf("resolved keys=%q/%q lookups=%d", first, second, lookups)
	}
	first[0] = 'X'
	if string(second) != secret {
		t.Fatal("resolver returned aliased credential slices")
	}

	resolver.lookup = func(string) (string, bool) {
		return secret, false
	}
	_, err = resolver.ResolveAPIKey(
		context.Background(),
		deepseekmodel.APIKeyIdentity{},
	)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("resolver diagnostic leaked credential: %v", err)
	}
}

func TestEnvironmentDeepSeekResolverRejectsSecretRefBeforeCredentialLookup(
	t *testing.T,
) {
	lookups := 0
	resolver := &environmentDeepSeekAPIKeyResolver{
		name:      "TEST_DEEPSEEK_KEY",
		reference: "env:TEST_DEEPSEEK_KEY",
		lookup: func(string) (string, bool) {
			lookups++
			return "must-not-be-read", true
		},
	}
	if _, err := resolver.ResolveAPIKey(
		context.Background(),
		deepseekmodel.APIKeyIdentity{
			Provider:  deepseekmodel.ProviderNameV1,
			Model:     deepseekmodel.ModelV4Pro,
			SecretRef: "placeholder",
		},
	); err == nil {
		t.Fatal("mismatched SecretRef was accepted")
	}
	if lookups != 0 {
		t.Fatalf("mismatched SecretRef performed %d credential lookups", lookups)
	}
}

func TestRunChatRejectsCredentialSourceWithoutDeepSeekEnable(t *testing.T) {
	err := run(context.Background(), []string{
		"chat",
		"--db", "unused.sqlite",
		"--message", "unused",
		"--deepseek-api-key-env", "TEST_DEEPSEEK_KEY",
	}, io.Discard, io.Discard)
	if err == nil ||
		!strings.Contains(err.Error(), "requires explicit --enable-deepseek") {
		t.Fatalf("chat DeepSeek default-off error=%v", err)
	}
}

func TestRunServeRejectsCredentialSourceWithoutDeepSeekEnable(t *testing.T) {
	err := run(context.Background(), []string{
		"serve",
		"--db", "unused.sqlite",
		"--deepseek-api-key-env", "TEST_DEEPSEEK_KEY",
	}, io.Discard, io.Discard)
	if err == nil ||
		!strings.Contains(err.Error(), "requires explicit --enable-deepseek") {
		t.Fatalf("serve DeepSeek default-off error=%v", err)
	}
}

func TestRunServeAcceptsExplicitDeepSeekRuntimeWithoutResolvingSecret(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize DeepSeek serve data: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		err := run(ctx, []string{
			"serve",
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--listen", "127.0.0.1:0",
			"--enable-deepseek",
			"--deepseek-api-key-env", "TEST_DEEPSEEK_KEY_NOT_SET",
		}, writer, io.Discard)
		_ = writer.CloseWithError(err)
		result <- err
	}()
	var ready map[string]string
	decodeErr := json.NewDecoder(reader).Decode(&ready)
	cancel()
	var runErr error
	select {
	case runErr = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("DeepSeek serve did not stop after cancellation")
	}
	if decodeErr != nil {
		t.Fatalf("decode DeepSeek serve readiness: %v", decodeErr)
	}
	if runErr != nil {
		t.Fatalf("stop DeepSeek serve: %v", runErr)
	}
	if ready["deepseek_adapter"] != "enabled" || ready["listen"] == "" {
		t.Fatalf("DeepSeek serve readiness=%+v", ready)
	}
}

func TestValidEnvironmentName(t *testing.T) {
	for _, value := range []string{"KEY", "_KEY_2", "a9"} {
		if !validEnvironmentName(value) {
			t.Fatalf("valid environment name %q rejected", value)
		}
	}
	for _, value := range []string{"", "9KEY", "A-B", " KEY", "密钥"} {
		if validEnvironmentName(value) {
			t.Fatalf("invalid environment name %q accepted", value)
		}
	}
}
