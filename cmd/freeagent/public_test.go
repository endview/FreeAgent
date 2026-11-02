package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestPublicCLIPasswordUsesStdinAndDefaultOffWithoutOwner(t *testing.T) {
	root := t.TempDir()
	data, work := filepath.Join(root, "data"), filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	if err := harness.WriteConfig(data, harness.DefaultConfig(work)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	secret := "fixture administrator password"
	if err := runPublic(context.Background(), []string{"password", "--data-dir", data, "--password-stdin"}, strings.NewReader(secret+"\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatal("password in CLI output")
	}
	stdout.Reset()
	if err := runPublic(t.Context(), []string{"status", "--data-dir", data}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"credential_ready": true`) || !strings.Contains(stdout.String(), `"enabled": false`) {
		t.Fatal(stdout.String())
	}
	if err := runPublic(t.Context(), []string{"password", "--data-dir", data, "--password", secret}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("password argument was accepted")
	}
}
