package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/endview/freeagent/internal/buildinfo"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

type maintenanceReport struct {
	ReadyToUpdate     bool                      `json:"ready_to_update"`
	ObservedAt        string                    `json:"observed_at"`
	Version           string                    `json:"version"`
	DataDir           string                    `json:"data_dir"`
	State             harness.MaintenanceStatus `json:"execution"`
	OwnerRunning      bool                      `json:"owner_running"`
	PendingControl    int                       `json:"pending_control"`
	UnresolvedControl int                       `json:"unresolved_control"`
	Backup            string                    `json:"backup_status"`
	Blockers          []string                  `json:"blockers"`
	Next              []string                  `json:"next"`
}

func inspectMaintenance(ctx context.Context, data, backup string) maintenanceReport {
	report := maintenanceReport{ObservedAt: time.Now().UTC().Format(time.RFC3339), Version: buildinfo.Version,
		DataDir: data, Backup: "not_checked", Blockers: []string{}, Next: []string{
			"This is an observation, not an update lock. Recheck immediately before maintenance.",
			"Use the normal stop procedure only after user work is idle; retain Keys, workspace, attachments and native PG data.",
		}}
	if _, err := harness.LoadConfig(data); err != nil {
		report.Blockers = append(report.Blockers, "config_unavailable")
	}
	if _, _, _, err := handoff.Read(data); err == nil {
		client, err := interactiveOwner(ctx, data)
		if err != nil {
			report.Blockers = append(report.Blockers, "owner_status_unavailable")
		} else if state, err := client.State(ctx); err != nil {
			report.Blockers = append(report.Blockers, "owner_status_unavailable")
		} else {
			report.OwnerRunning = true
			if state.Stopping || len(state.ActiveChats) > 0 || state.ActiveCommands {
				report.Blockers = append(report.Blockers, "owner_busy")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		report.Blockers = append(report.Blockers, "owner_metadata_unavailable")
	}
	store, err := harness.OpenStoreReadOnly(ctx, data)
	if err != nil {
		report.Blockers = append(report.Blockers, "database_unavailable")
	} else {
		defer store.Close()
		report.State, err = store.MaintenanceStatus(ctx)
		if err != nil {
			report.Blockers = append(report.Blockers, "execution_state_unavailable")
		} else if report.State.ActiveRuns > 0 || report.State.WaitingConfirmations > 0 || report.State.ActiveCommands > 0 {
			report.Blockers = append(report.Blockers, "execution_active_or_waiting")
		}
	}
	if report.PendingControl, report.UnresolvedControl, err = harness.ControlMaintenance(data); err != nil {
		report.Blockers = append(report.Blockers, "control_journal_unavailable")
	} else if report.PendingControl > 0 || report.UnresolvedControl > 0 {
		report.Blockers = append(report.Blockers, "control_pending_or_unresolved")
	}
	if backup != "" {
		if err := harness.VerifyBackup(ctx, backup); err != nil {
			report.Backup = "invalid"
			report.Blockers = append(report.Blockers, "backup_invalid")
		} else if sameBackupIdentity(data, backup) {
			report.Backup = "verified_same_core"
		} else {
			report.Backup = "different_core"
			report.Blockers = append(report.Blockers, "backup_identity_mismatch")
		}
	} else {
		report.Blockers = append(report.Blockers, "verified_backup_required")
	}
	report.Next = append(report.Next, "UNKNOWN and stopped records remain unresolved evidence; never replay their side effects during an update.")
	report.ReadyToUpdate = len(report.Blockers) == 0
	return report
}

func sameBackupIdentity(data, backup string) bool {
	var current, saved struct {
		CoreID string `json:"core_id"`
	}
	for path, value := range map[string]*struct {
		CoreID string `json:"core_id"`
	}{
		filepath.Join(data, "postgres.json"): &current, filepath.Join(backup, "identity.json"): &saved,
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return false
		}
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, value) != nil {
			return false
		}
	}
	return current.CoreID != "" && current.CoreID == saved.CoreID
}

// Export allowlisted typed projections only. Raw config, URLs, model IDs,
// journal lines, logs, prompts, tool output and credentials never enter the zip.
func exportDiagnosis(path string, checks []doctorCheck, maintenance maintenanceReport) (returnErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("diagnosis export: create a new writable output file: %w", err)
	}
	defer func() {
		if returnErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	writer := zip.NewWriter(file)
	write := func(name string, value any) error {
		entry, err := writer.Create(name)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(entry)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	type safeCheck struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	safe := make([]safeCheck, 0, len(checks))
	for _, check := range checks {
		safe = append(safe, safeCheck{check.ID, check.Status})
	}
	maintenance.DataDir = "<data-dir>"
	if err := write("diagnosis.json", map[string]any{"format": "freeagent-diagnosis-v1", "version": buildinfo.Version,
		"commit": buildinfo.Commit, "os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version(), "checks": safe,
		"network_tested": false, "credentials_exported": false, "conversation_content_exported": false}); err != nil {
		return err
	}
	if err := write("maintenance.json", maintenance); err != nil {
		return err
	}
	entry, err := writer.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(entry, "Local diagnosis: only allowlisted health categories and aggregate execution counts.\nRaw logs, configuration, Keys, cookies, database credentials, conversation text, attachments and workspace files are excluded.\nRecent failures are counts over the latest 100 Runs; UNKNOWN is not failure.\nUse doctor --text locally for recovery actions. This archive is not a backup or a Provider connectivity test.\n"); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return file.Close()
}
