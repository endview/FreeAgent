package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestDoctorExportExcludesPrivateContentsAndDoesNotOverwrite(t *testing.T) {
	data, _ := initCLI(t)
	secret := "sk-private-diagnostic-sentinel"
	for _, name := range []string{"config.json", "engine.log", "desktop.log"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(secret), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "diagnosis.zip")
	_, _, err := invoke(t, "", "doctor", "--data-dir", data, "--export", path)
	if exitCode(err) != 1 {
		t.Fatalf("expected diagnosis errors, got %v", err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 3 {
		t.Fatalf("unexpected archive files: %d", len(archive.File))
	}
	for _, file := range archive.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), data) {
			t.Fatalf("private content exported in %s", file.Name)
		}
	}
	before, _ := os.ReadFile(path)
	_, _, err = invoke(t, "", "doctor", "--data-dir", data, "--export", path)
	if err == nil {
		t.Fatal("existing export overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed export changed existing archive")
	}
}

func TestDoctorPreflightRequiresIdleAndSameCoreBackup(t *testing.T) {
	data, _ := initCLI(t)
	_, store, service, closeAll, err := openRuntime(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	tasks := harness.NewTaskService(service)
	chat, err := tasks.NewConversation(t.Context(), "preflight-private-title")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "before-update")
	if err := store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	owner, err := startLocalOwner(t.Context(), tasks, data, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(); closeAll() })
	raw, stderr, err := invoke(t, "", "doctor", "--data-dir", data, "--preflight", "--backup", backup)
	var result struct {
		Maintenance maintenanceReport `json:"maintenance"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || !result.Maintenance.ReadyToUpdate || !result.Maintenance.OwnerRunning {
		t.Fatalf("idle attached preflight: %s %s %v", raw, stderr, err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.PlanControlChange(t.Context(), harness.ControlPlanRequest{Kind: harness.ControlSetPermission,
		Authorization: harness.ControlUserDirect, Arguments: json.RawMessage(`{"mode":"ask"}`)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "doctor", "--data-dir", data, "--preflight", "--backup", backup)
	if exitCode(err) != 1 || json.Unmarshal([]byte(raw), &result) != nil || result.Maintenance.PendingControl != 1 {
		t.Fatalf("pending control not blocked: %s %v", raw, err)
	}
	_ = chat
	if strings.Contains(raw, "preflight-private-title") {
		t.Fatal("preflight exposed conversation title")
	}
}

func TestDoctorPreflightWithoutBackupDoesNotClaimReady(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "doctor", "--data-dir", data, "--preflight")
	if exitCode(err) != 1 || !strings.Contains(raw, "verified_backup_required") {
		t.Fatalf("%s %v", raw, err)
	}
}
