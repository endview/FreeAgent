package handoff

import (
	"os"
	"path/filepath"
	"testing"
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
