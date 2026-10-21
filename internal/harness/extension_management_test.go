package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExtensionManifest(t *testing.T, root, id, name string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := ExtensionManifest{
		Protocol: extensionProtocol, ID: id, Version: "1.0.0", Name: name,
		Description: "test extension", Run: ExtensionRun{Command: "test-extension"},
		Context: ExtensionContext{Phases: []string{"turn"}, Purposes: []string{"chat"}, Placement: []string{"tail"}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionManagementUsesWorkspaceOverrideAndTrustBoundary(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	userRoot := filepath.Join(service.Store.DataDir(), "extensions", "com.example.same")
	workspaceRoot := filepath.Join(workspace, ".freeagent", "extensions", "com.example.same")
	writeExtensionManifest(t, userRoot, "com.example.same", "User extension")
	writeExtensionManifest(t, workspaceRoot, "com.example.same", "Workspace extension")
	writeExtensionManifest(t, filepath.Join(workspace, ".freeagent", "extensions", "com.example.only"), "com.example.only", "Workspace only")

	items, err := tasks.Extensions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("untrusted items=%+v", items)
	}
	for _, item := range items {
		if item.ID == "com.example.same" && (item.Name != "User extension" || !item.Available || item.Source != "user") {
			t.Fatalf("same-name user item=%+v", item)
		}
		if item.ID == "com.example.only" && (item.Available || item.Trusted || item.Source != "workspace") {
			t.Fatalf("untrusted workspace item=%+v", item)
		}
	}
	if err := tasks.SetExtensionEnabled(t.Context(), "com.example.only", true); err == nil ||
		!strings.Contains(err.Error(), "trusted") {
		t.Fatalf("untrusted enable error=%v", err)
	}

	if err := tasks.TrustWorkspaceExtensions(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	items, err = tasks.Extensions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("trusted items=%+v", items)
	}
	for _, item := range items {
		if item.ID == "com.example.same" && (item.Name != "Workspace extension" || item.Source != "workspace" || !item.Available) {
			t.Fatalf("workspace override item=%+v", item)
		}
	}
	if err := tasks.SetExtensionEnabled(t.Context(), "com.example.only", true); err != nil {
		t.Fatalf("trusted workspace enable: %v", err)
	}
	item, err := tasks.Extension(t.Context(), "com.example.only")
	if err != nil || !item.Enabled || item.Source != "workspace" {
		t.Fatalf("enabled workspace item=%+v err=%v", item, err)
	}
}

func TestExtensionImportCheckAndClearData(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	source := filepath.Join(t.TempDir(), "com.example.imported")
	writeExtensionManifest(t, source, "com.example.imported", "Imported extension")
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	checked, err := CheckExtension(source)
	if err != nil || checked.ID != "com.example.imported" || checked.Enabled {
		t.Fatalf("check=%+v err=%v", checked, err)
	}
	imported, err := tasks.ImportExtension(t.Context(), source)
	if err != nil || imported.ID != checked.ID || imported.Enabled {
		t.Fatalf("import=%+v err=%v", imported, err)
	}
	if _, err := os.Stat(filepath.Join(service.Store.DataDir(), "extensions", imported.ID, "README.txt")); err != nil {
		t.Fatalf("imported file missing: %v", err)
	}
	if _, err := tasks.ImportExtension(t.Context(), source); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate import error=%v", err)
	}
	data := filepath.Join(service.Store.DataDir(), "extensions", imported.ID, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tasks.ClearExtensionData(t.Context(), imported.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("extension data remains, err=%v", err)
	}
	if err := tasks.SetExtensionRequired(t.Context(), imported.ID, true); err == nil {
		t.Fatal("required extension accepted while disabled")
	}
	if err := tasks.SetExtensionEnabled(t.Context(), imported.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetExtensionRequired(t.Context(), imported.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetExtensionEnabled(t.Context(), imported.ID, false); err == nil {
		t.Fatal("required extension was disabled")
	}
}

func TestExtensionManagementRejectsMalformedFolder(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	source := filepath.Join(t.TempDir(), "com.example.invalid")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ImportExtension(t.Context(), source); err == nil {
		t.Fatal("malformed extension was imported")
	}
	if _, err := CheckExtension(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing extension passed check")
	}
	if _, err := tasks.Extension(t.Context(), "bad"); err == nil {
		t.Fatal("invalid extension id accepted")
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid id returned not-exist instead of validation error")
	}
}
