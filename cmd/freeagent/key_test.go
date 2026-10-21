package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

func testKeyStore(t *testing.T) *harness.KeyStore {
	t.Helper()
	path, err := apppaths.KeysFile()
	if err != nil {
		t.Fatal(err)
	}
	return harness.NewKeyStore(path)
}

func TestKeyAddFromStdin(t *testing.T) {
	const secret = "sk-stdin-sentinel-0f9a"
	name := "stdin " + t.Name()
	raw, stderr, err := invoke(t, secret+"\r\n", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--key-stdin")
	if err != nil {
		t.Fatalf("key add: stderr=%q err=%v", stderr, err)
	}
	var added map[string]string
	if err := json.Unmarshal([]byte(raw), &added); err != nil || added["id"] == "" || added["name"] != name || len(added) != 2 {
		t.Fatalf("stdout=%q err=%v", raw, err)
	}
	if strings.Contains(raw+stderr, secret) {
		t.Fatal("key add output repeated the key")
	}
	if !strings.Contains(stderr, "config model") {
		t.Fatalf("missing next step: %q", stderr)
	}
	view, err := testKeyStore(t).Find(added["id"])
	if err != nil || view.Source != harness.KeySourceSaved || view.Name != name {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	resolved, err := testKeyStore(t).Resolve(added["id"])
	if err != nil || resolved.BaseURL == "" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	if _, _, err := invoke(t, secret+"\n", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--key-stdin"); exitCode(err) != exitUsage {
		t.Fatalf("duplicate name exit=%d err=%v", exitCode(err), err)
	}
	for _, input := range []string{"", "\n", "one\ntwo\n", strings.Repeat("k", maxKeyInput+1)} {
		_, stderr, err := invoke(t, input, "key", "add", "--name", name+" bad", "--url", "http://127.0.0.1:9/v1", "--key-stdin")
		if exitCode(err) != exitUsage || strings.Contains(stderr, "two") {
			t.Fatalf("input of %d bytes: exit=%d err=%v", len(input), exitCode(err), err)
		}
	}
	if _, err := testKeyStore(t).Find(name + " bad"); !errors.Is(err, harness.ErrKeyNotFound) {
		t.Fatalf("rejected input still saved a profile: %v", err)
	}
}

func TestKeyAddNonConsoleExit2(t *testing.T) {
	name := "prompt " + t.Name()
	_, stderr, err := invoke(t, "sk-never-read\n", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1")
	if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "--key-stdin") {
		t.Fatalf("exit=%d err=%v stderr=%q", exitCode(err), err, stderr)
	}
	if _, err := testKeyStore(t).Find(name); !errors.Is(err, harness.ErrKeyNotFound) {
		t.Fatalf("profile saved without a key: %v", err)
	}
	if _, _, err := invoke(t, "", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--key-stdin", "--no-key"); exitCode(err) != exitUsage {
		t.Fatalf("two sources exit=%d err=%v", exitCode(err), err)
	}
	if _, _, err := invoke(t, "", "key", "edit", "missing "+t.Name(), "--note", "x"); exitCode(err) != exitUsage {
		t.Fatalf("missing profile exit=%d err=%v", exitCode(err), err)
	}
	if _, _, err := invoke(t, "", "key", "frobnicate"); exitCode(err) != exitUsage {
		t.Fatalf("unknown action exit=%d err=%v", exitCode(err), err)
	}
}

func TestKeyListNoSecret(t *testing.T) {
	const secret = "sk-list-sentinel-77c1"
	data, _ := initCLI(t)
	name := "list " + t.Name()
	if _, _, err := invoke(t, secret+"\n", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--key-stdin"); err != nil {
		t.Fatal(err)
	}
	envName := "env " + t.Name()
	if _, _, err := invoke(t, "", "key", "add", "--name", envName, "--url", "https://api.example.com/v1", "--key-env", "FREEAGENT_TEST_UNSET_KEY"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"key", "list"},
		{"key", "list", "--json"},
		{"doctor", "--data-dir", data},
		{"config", "show", "--data-dir", data},
	} {
		raw, stderr, err := invoke(t, "", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(raw+stderr, secret) || strings.Contains(raw+stderr, "list-sentinel") {
			t.Fatalf("%v printed the key", args)
		}
	}
	raw, _, err := invoke(t, "", "key", "list")
	if err != nil || !strings.Contains(raw, name) || !strings.Contains(raw, "env FREEAGENT_TEST_UNSET_KEY") {
		t.Fatalf("list=%q err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "key", "list", "--json")
	var views []map[string]any
	if err != nil || json.Unmarshal([]byte(raw), &views) != nil {
		t.Fatalf("list --json=%q err=%v", raw, err)
	}
	for _, view := range views {
		for _, field := range []string{"secret", "data", "key"} {
			if _, ok := view[field]; ok {
				t.Fatalf("list --json has field %q", field)
			}
		}
	}
}

func TestKeyTestExitCodes(t *testing.T) {
	const secret = "sk-probe-sentinel-41ad"
	var status atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if code := int(status.Load()); code != http.StatusOK {
			writer.WriteHeader(code)
			_, _ = writer.Write([]byte(`{"error":{"message":"invalid key ` + secret + `"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":[{"id":"fixture-a"},{"id":"fixture-b"}]}`))
	}))
	defer server.Close()
	name := "probe " + t.Name()
	if _, _, err := invoke(t, secret+"\n", "key", "add", "--name", name, "--url", server.URL+"/v1", "--key-stdin"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "key", "test", name)
	if err != nil || !strings.Contains(raw, "ok: 2 models") {
		t.Fatalf("ok probe=%q err=%v", raw, err)
	}
	if view, err := testKeyStore(t).Find(name); err != nil || view.LastCheck == nil || view.LastCheck.Result != "ok" {
		t.Fatalf("ok check not recorded: %+v %v", view, err)
	}
	raw, _, err = invoke(t, "", "key", "models", name)
	if err != nil || raw != "fixture-a\nfixture-b\n" {
		t.Fatalf("models=%q err=%v", raw, err)
	}
	status.Store(http.StatusUnauthorized)
	raw, stderr, err := invoke(t, "", "key", "test", name, "--json")
	if exitCode(err) != exitFailure {
		t.Fatalf("auth probe exit=%d err=%v", exitCode(err), err)
	}
	var result map[string]any
	if json.Unmarshal([]byte(raw), &result) != nil || result["category"] != "auth" || result["status"] != float64(401) {
		t.Fatalf("auth probe=%q", raw)
	}
	if strings.Contains(raw+stderr, secret) {
		t.Fatal("probe output repeated the key")
	}
	if view, err := testKeyStore(t).Find(name); err != nil || view.LastCheck == nil || view.LastCheck.Result != "auth" {
		t.Fatalf("auth check not recorded: %+v %v", view, err)
	}
	status.Store(http.StatusNotFound)
	if _, stderr, err := invoke(t, "", "key", "test", name); exitCode(err) != exitFailure || !strings.Contains(stderr, "config model") {
		t.Fatalf("not_found probe exit=%d stderr=%q", exitCode(err), stderr)
	}
	envName := "unset " + t.Name()
	if _, _, err := invoke(t, "", "key", "add", "--name", envName, "--url", server.URL+"/v1", "--key-env", "FREEAGENT_TEST_UNSET_PROBE_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := invoke(t, "", "key", "test", envName); exitCode(err) != exitFailure || !strings.Contains(stderr, "key edit") {
		t.Fatalf("unset env exit=%d stderr=%q err=%v", exitCode(err), stderr, err)
	}
}
