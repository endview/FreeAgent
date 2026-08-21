package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileChannelSecretResolverDefersReadAndSupportsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel.secret")
	if err := os.WriteFile(path, []byte("first-secret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := newFileChannelSecretResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rotated-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.ResolveSecret(context.Background(), "secret/ref")
	if err != nil {
		t.Fatal(err)
	}
	secret := resolved
	defer clear(secret)
	if string(secret) != "rotated-secret" {
		t.Fatalf("resolved secret length/content mismatch")
	}
}

func TestFileChannelSecretResolverRejectsDirectoryAndOversize(t *testing.T) {
	root := t.TempDir()
	if _, err := newFileChannelSecretResolver(root); err == nil {
		t.Fatal("directory accepted as Channel secret file")
	}
	path := filepath.Join(root, "oversize.secret")
	if err := os.WriteFile(path, make([]byte, maxChannelCredentialFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newFileChannelSecretResolver(path); err == nil {
		t.Fatal("oversize Channel secret file accepted")
	}
}
