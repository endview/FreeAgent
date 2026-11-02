package handoff

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPublishReadAndRemoveDiscoveryKeepsSecretsSeparate(t *testing.T) {
	dir := t.TempDir()
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	chat, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	published, err := Publish(dir, EngineInfo{Origin: "http://127.0.0.1:4567", Workspace: filepath.Join(dir, "workspace"), Generation: "generation", OwnerKind: "interactive"}, secret, chat)
	if err != nil {
		t.Fatal(err)
	}
	engine, gotSecret, gotChat, err := Read(dir)
	if err != nil || engine.Origin != "http://127.0.0.1:4567" || gotSecret != secret || gotChat != chat {
		t.Fatalf("engine=%+v secret=%q chat=%q err=%v", engine, gotSecret, gotChat, err)
	}
	engineRaw, err := os.ReadFile(filepath.Join(dir, EngineFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(engineRaw) == secret || string(engineRaw) == chat || string(engineRaw) == secret+chat {
		t.Fatal("engine discovery copied a secret")
	}
	if err := published.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Read(dir); err == nil {
		t.Fatal("removed discovery remained readable")
	}
}

func TestCloseDiscoveryWaitsForBriefWindowsReader(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows deletion sharing")
	}
	dir := t.TempDir()
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	published, err := Publish(dir, EngineInfo{Origin: "http://127.0.0.1:4567", Workspace: filepath.Join(dir, "workspace"), Generation: "generation", OwnerKind: "interactive"}, secret, secret)
	if err != nil {
		t.Fatal(err)
	}
	readers := make([]*os.File, 0, 3)
	for _, name := range []string{EngineFile, SecretFile, ChatFile} {
		reader, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, reader)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		for _, reader := range readers {
			_ = reader.Close()
		}
		close(closed)
	}()
	t.Cleanup(func() { <-closed })
	if err := published.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Read(dir); err == nil {
		t.Fatal("closed Owner discovery is still attachable")
	}
	if err := published.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}
