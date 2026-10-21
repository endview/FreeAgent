package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const keysHelperPathEnv = "FREEAGENT_TEST_KEYS_HELPER_PATH"

func sentinelKey(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return "FA_TEST_SENTINEL_" + hex.EncodeToString(raw[:])
}

func TestNormalizeBaseURL(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"https://api.deepseek.com", "https://api.deepseek.com"},
		{"https://API.DeepSeek.com/", "https://api.deepseek.com"},
		{"https://api.deepseek.com/chat/completions", "https://api.deepseek.com"},
		{"https://example.com/v1/chat/completions", "https://example.com/v1"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1"},
		{"http://example.com/v1", ""},
		{"https://user:pass@example.com", ""},
		{"https://example.com/v1?x=1", ""},
	} {
		got, err := NormalizeBaseURL(test.input)
		if test.want == "" {
			if err == nil {
				t.Errorf("%s accepted as %q", test.input, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("%s = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestSuggestedDeepSeekResponsesCapabilities(t *testing.T) {
	key := KeyProfileView{Protocol: KeyProtocolOpenAICompatible, BaseURL: "https://api.deepseek.com"}
	if caps := SuggestedModelCapabilities(key, "deepseek-v4-pro"); caps.Protocol != "responses" || caps.WebSearch != "supported" {
		t.Fatalf("v4 pro capabilities=%+v", caps)
	}
	if caps := SuggestedModelCapabilities(key, "deepseek-flash"); caps.Protocol != "" || caps.WebSearch != "" {
		t.Fatalf("flash unexpectedly enabled Responses search=%+v", caps)
	}
	key.BaseURL = "https://provider.example"
	if caps := SuggestedModelCapabilities(key, "deepseek-v4-pro"); caps.Protocol != "" || caps.WebSearch != "" {
		t.Fatalf("non-DeepSeek host received search capabilities=%+v", caps)
	}
}

func TestKeyStoreRoundTrip(t *testing.T) {
	keys := testKeyStore(t)
	const oldSecret, newSecret = "FA-round-trip-secret-0001", "FA-round-trip-secret-0002"
	saved, err := keys.Add(KeyInput{Name: "Saved One", BaseURL: "https://api.example.com/v1/", Source: KeySourceSaved, Secret: []byte(oldSecret)})
	if err != nil || saved.BaseURL != "https://api.example.com/v1" || saved.Hint != "0001" || saved.Encrypted != (runtime.GOOS == "windows") {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	env, err := keys.Add(KeyInput{Name: "Env One", BaseURL: "https://env.example.com", Source: KeySourceEnv, EnvName: "FA_ROUND_TRIP_KEY"})
	if err != nil || env.EnvName != "FA_ROUND_TRIP_KEY" || env.Encrypted {
		t.Fatalf("env=%+v err=%v", env, err)
	}
	local, err := keys.Add(KeyInput{Name: "Local One", BaseURL: "http://localhost:11434/v1", Source: KeySourceNone})
	if err != nil {
		t.Fatal(err)
	}
	views, err := keys.List()
	if err != nil || len(views) != 3 || views[0].Source != KeySourceSaved || views[1].Source != KeySourceEnv || views[2].Source != KeySourceNone {
		t.Fatalf("list=%+v err=%v", views, err)
	}
	for _, ref := range []string{saved.ID, strings.ToUpper(saved.ID), "saved one", " SAVED ONE "} {
		if found, err := keys.Find(ref); err != nil || found.ID != saved.ID {
			t.Errorf("find %q = %+v, %v", ref, found, err)
		}
	}
	if _, err := keys.Find("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing profile error=%v", err)
	}
	if _, err := keys.Add(KeyInput{Name: "ENV ONE", BaseURL: "https://other.example.com", Source: KeySourceEnv, EnvName: "OTHER_KEY"}); !errors.Is(err, ErrKeyNameExists) {
		t.Fatalf("duplicate add error=%v", err)
	}
	if _, err := keys.Update(local.ID, KeyInput{Name: "saved one", BaseURL: local.BaseURL}); !errors.Is(err, ErrKeyNameExists) {
		t.Fatalf("duplicate rename error=%v", err)
	}

	if err := keys.RecordCheck(saved.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := keys.RecordCheck(saved.ID, "fine"); err == nil {
		t.Fatal("unknown check result recorded")
	}
	updated, err := keys.Update(saved.ID, KeyInput{Name: "Saved Renamed", BaseURL: "https://api.example.com/v1", Note: "work account"})
	if err != nil || updated.Name != "Saved Renamed" || updated.Note != "work account" || updated.LastCheck == nil || updated.LastCheck.Result != "ok" {
		t.Fatalf("metadata update=%+v err=%v", updated, err)
	}
	if resolved, err := keys.Resolve(saved.ID); err != nil || resolved.secret != oldSecret || resolved.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("empty secret did not keep the key: err=%v", err)
	}
	moved, err := keys.Update(saved.ID, KeyInput{Name: "Saved Renamed", BaseURL: "https://api2.example.com"})
	if err != nil || moved.BaseURL != "https://api2.example.com" || moved.LastCheck != nil {
		t.Fatalf("address update=%+v err=%v", moved, err)
	}
	if err := keys.RecordCheck(saved.ID, "auth"); err != nil {
		t.Fatal(err)
	}
	rotated, err := keys.Update(saved.ID, KeyInput{Name: "Saved Renamed", BaseURL: "https://api2.example.com", Secret: []byte(newSecret)})
	if err != nil || rotated.Hint != "0002" || rotated.LastCheck != nil || rotated.Source != KeySourceSaved {
		t.Fatalf("key update=%+v err=%v", rotated, err)
	}
	if resolved, err := keys.Resolve(saved.ID); err != nil || resolved.secret != newSecret {
		t.Fatalf("rotated key not resolved: err=%v", err)
	}

	if err := keys.Remove(env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Find(env.ID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("removed profile error=%v", err)
	}
	if err := keys.Remove(env.ID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second remove error=%v", err)
	}
	reread, err := NewKeyStore(keys.Path()).List()
	if err != nil || len(reread) != 2 || reread[0].Name != "Saved Renamed" || reread[1].ID != local.ID {
		t.Fatalf("persisted=%+v err=%v", reread, err)
	}
}

func TestKeyStoreAnthropicUpdateUsesExistingProtocolWhenOmitted(t *testing.T) {
	keys := testKeyStore(t)
	profile, err := keys.Add(KeyInput{
		Name: "Anthropic", BaseURL: "https://api.anthropic.com/v1/messages",
		Protocol: KeyProtocolAnthropic, Source: KeySourceSaved, Secret: []byte("anthropic-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := keys.Update(profile.ID, KeyInput{
		Name: profile.Name, BaseURL: "https://api.anthropic.com/v1/messages", Note: "updated without protocol",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.BaseURL != "https://api.anthropic.com" || updated.Protocol != KeyProtocolAnthropic || updated.Note == "" {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := keys.Resolve(profile.ID); err != nil {
		t.Fatalf("updated Anthropic key did not resolve: %v", err)
	}
}

// TestKeyStoreHelperProcess is the second writer for TestKeyStoreConcurrentWriters.
func TestKeyStoreHelperProcess(t *testing.T) {
	path := os.Getenv(keysHelperPathEnv)
	if path == "" {
		return
	}
	keys := NewKeyStore(path)
	for index := range 5 {
		if _, err := keys.Add(KeyInput{Name: fmt.Sprintf("helper %d", index), BaseURL: "http://127.0.0.1:1", Source: KeySourceNone}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeyStoreConcurrentWriters(t *testing.T) {
	path := testKeyStore(t).Path()
	var output bytes.Buffer
	helper := exec.Command(os.Args[0], "-test.run=^TestKeyStoreHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(), keysHelperPathEnv+"="+path)
	helper.Stdout, helper.Stderr = &output, &output
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	// Two writers share one store (in-process mutex); two use their own store
	// and meet only at keys.json.lock, like the helper process.
	shared := NewKeyStore(path)
	stores := []*KeyStore{shared, shared, NewKeyStore(path), NewKeyStore(path)}
	var wait sync.WaitGroup
	errs := make(chan error, 20)
	for writer, keys := range stores {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range 5 {
				if _, err := keys.Add(KeyInput{Name: fmt.Sprintf("writer %d-%d", writer, index), BaseURL: "http://127.0.0.1:1", Source: KeySourceNone}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := helper.Wait(); err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, output.String())
	}
	views, err := NewKeyStore(path).List()
	if err != nil || len(views) != 25 {
		t.Fatalf("profiles=%d err=%v\n%s", len(views), err, output.String())
	}
	names := map[string]bool{}
	for _, view := range views {
		names[view.Name] = true
	}
	if len(names) != 25 || !names["helper 4"] || !names["writer 3-4"] {
		t.Fatalf("lost or duplicated updates: %v", names)
	}
}

// tamperKeys rewrites one stored profile directly in keys.json.
func tamperKeys(t *testing.T, path, id string, edit func(*KeyProfile)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file keysFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for index := range file.Profiles {
		if file.Profiles[index].ID == id {
			edit(&file.Profiles[index])
		}
	}
	if raw, err = json.Marshal(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKeyStoreNeedsReentry(t *testing.T) {
	foreign := keyProtectionDPAPI
	if runtime.GOOS == "windows" {
		foreign = keyProtectionPlaintext
	}
	for _, test := range []struct {
		name   string
		source string
		edit   func(*KeyProfile)
	}{
		{"damaged data", KeySourceSaved, func(profile *KeyProfile) { profile.Key.Data = "not base64 !" }},
		{"foreign protection", KeySourceSaved, func(profile *KeyProfile) {
			profile.Key.Protection, profile.Key.Data = foreign, "FA-foreign-protected-key"
		}},
		{"empty environment", KeySourceEnv, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			keys := testKeyStore(t)
			id := ""
			if test.source == KeySourceEnv {
				t.Setenv("FA_REENTRY_KEY", "")
				id = addTestProfile(t, keys, "https://api.example.com", KeySourceEnv, "FA_REENTRY_KEY", "")
			} else {
				id = addTestProfile(t, keys, "https://api.example.com", KeySourceSaved, "", "FA-reentry-secret-0001")
				tamperKeys(t, keys.Path(), id, test.edit)
			}
			fresh := NewKeyStore(keys.Path())
			if _, err := fresh.Resolve(id); !errors.Is(err, ErrKeyNeedsReentry) {
				t.Fatalf("resolve error=%v", err)
			}
			views, err := fresh.List()
			if err != nil || len(views) != 1 || views[0].ID != id || views[0].BaseURL != "https://api.example.com" {
				t.Fatalf("profile not kept: %+v err=%v", views, err)
			}
		})
	}
}

func TestKeyStoreVersionMismatch(t *testing.T) {
	keys := testKeyStore(t)
	original := []byte(`{"version":2,"profiles":[]}`)
	if err := os.WriteFile(keys.Path(), original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.List(); !errors.Is(err, ErrKeysFormat) {
		t.Fatalf("list error=%v", err)
	}
	if _, err := keys.Add(KeyInput{Name: "new", BaseURL: "http://127.0.0.1:1", Source: KeySourceNone}); !errors.Is(err, ErrKeysFormat) {
		t.Fatalf("add error=%v", err)
	}
	if after, _ := os.ReadFile(keys.Path()); !bytes.Equal(after, original) {
		t.Fatalf("unsupported keys file was rewritten: %s", after)
	}
}

func TestKeySecretNeverSerialized(t *testing.T) {
	sentinel := sentinelKey(t)
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, "https://api.example.com", KeySourceSaved, "", sentinel)
	profile, err := keys.profile(id)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := keys.Resolve(id)
	if err != nil || resolved.secret != sentinel {
		t.Fatalf("resolve err=%v", err)
	}
	client, err := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}, resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := KeyInput{Name: "input", BaseURL: "https://api.example.com", Source: KeySourceSaved, Secret: []byte(sentinel)}
	var outputs []string
	for _, value := range []any{profile.Key, profile, &profile, input, &input, resolved, &resolved, client, *client} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
			outputs = append(outputs, fmt.Sprintf(verb, value))
		}
	}
	for _, value := range []any{resolved, input, profile.View()} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(raw))
	}
	for index, output := range outputs {
		if strings.Contains(output, sentinel) {
			t.Fatalf("output %d exposed the key: %s", index, output)
		}
	}
}

func TestKeySourceNoneLoopbackOnly(t *testing.T) {
	keys := testKeyStore(t)
	if _, err := keys.Add(KeyInput{Name: "remote", BaseURL: "https://example.com", Source: KeySourceNone}); err == nil {
		t.Fatal("source none accepted a remote address")
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if _, ok := request.Header["Authorization"]; ok || request.URL.Path != "/chat/completions" {
			t.Errorf("keyless request sent authorization or used %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	id := addTestProfile(t, keys, server.URL, KeySourceNone, "", "")
	view, err := keys.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", KeySourceNone} {
		if _, err := keys.Update(id, KeyInput{Name: view.Name, BaseURL: "https://example.com", Source: source}); err == nil {
			t.Fatalf("source none moved to a remote address (source %q)", source)
		}
	}
	client, err := keyedModelClient(keys)(ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 16}, nil)
	if err != nil || response.Content != "done" || requests != 1 {
		t.Fatalf("response=%+v err=%v requests=%d", response, err, requests)
	}
}
