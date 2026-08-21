package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestModuleApplyOutputFailureConvergesThroughExactRetry(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	err := runModuleApply(
		context.Background(),
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--plan", planPath,
			"--artifact", fixture.ArtifactDirectory,
			"--allow-local-mcp-artifact", fixture.ArtifactDigest,
		},
		failingModuleVerifyWriter{err: errors.New("injected output failure")},
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "(INTERNAL_ERROR)") {
		t.Fatalf("output failure error=%v", err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusAlreadyApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("retry after output failure=%+v, %v", result, err)
	}
}

func TestModuleApplyRetryRejectsLostExecutableModeButAllowsEmergencyDisable(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix executable mode semantics")
	}
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	enablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare mode fixture=%+v, %v", result, err)
	}
	publishedArtifact := filepath.Join(artifactRoot, fixture.ArtifactDigest)
	executable := ""
	if err := filepath.WalkDir(
		publishedArtifact,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().Perm()&0o111 != 0 {
					executable = path
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if executable == "" {
		t.Fatal("published MCP executable was not found")
	}
	if err := os.Chmod(executable, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err == nil || !strings.Contains(err.Error(), "(ARTIFACT_INVALID)") {
		t.Fatalf("lost executable mode retry error=%v", err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != 3 {
		t.Fatalf("emergency disable after mode loss=%+v, %v", result, err)
	}
}

func TestModuleApplyExactRetryRejectsDifferentWholePublicationBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare exact retry fixture=%+v, %v", result, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, loadErr := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	closeErr := store.Close()
	if loadErr != nil || closeErr != nil {
		t.Fatalf("load current publication: %v", loadErr)
	}
	profileIndex := -1
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == moduleApplyTestProfileID {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		t.Fatal("target Profile is absent")
	}
	control.Profiles[profileIndex].Profile.Digest = strings.Repeat("f", 64)
	control.Digest = ""
	_, alteredControlRef, alteredControlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze altered Control: %v", err)
	}
	if alteredControlRef.SnapshotID != basis.Control.SnapshotID ||
		alteredControlRef.Digest == basis.Control.Digest {
		t.Fatalf("altered Control identity=%+v current=%+v", alteredControlRef, basis.Control)
	}
	catalog.ControlSnapshotDigest = alteredControlRef.Digest
	catalog.Digest = ""
	_, alteredCatalogRef, alteredCatalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze altered Catalog: %v", err)
	}
	if alteredCatalogRef.GenerationID != basis.Catalog.GenerationID ||
		alteredCatalogRef.Digest == basis.Catalog.Digest {
		t.Fatalf("altered Catalog identity=%+v current=%+v", alteredCatalogRef, basis.Catalog)
	}

	withCmdClosedFileTamperV1(
		t,
		databasePath,
		[]string{
			"control_snapshots_reject_update",
			"runtime_catalog_generations_reject_update",
		},
		func(ctx context.Context, connection *sql.Conn) error {
			_, controlUpdateErr := connection.ExecContext(ctx, `
				UPDATE control_snapshots
				SET canonical_json=?, digest=?
				WHERE snapshot_id=?
			`, alteredControlCanonical, alteredControlRef.Digest, alteredControlRef.SnapshotID)
			_, catalogUpdateErr := connection.ExecContext(ctx, `
				UPDATE runtime_catalog_generations
				SET canonical_json=?, digest=?
				WHERE generation_id=?
			`, alteredCatalogCanonical, alteredCatalogRef.Digest, alteredCatalogRef.GenerationID)
			return errors.Join(controlUpdateErr, catalogUpdateErr)
		},
	)
	rowsBeforeRetry := moduleApplyMutationRowCountsV1(t, databasePath)

	_, err = runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err == nil || !strings.Contains(err.Error(), "(STORE_INVALID)") {
		t.Fatalf("whole-publication mismatch retry error=%v", err)
	}
	if rowsAfterRetry := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(
		rowsAfterRetry,
		rowsBeforeRetry,
	) {
		t.Fatalf(
			"whole-publication rejection changed Store rows: before=%v after=%v",
			rowsBeforeRetry,
			rowsAfterRetry,
		)
	}
	if _, err := os.Lstat(eventPath); !os.IsNotExist(err) {
		t.Fatalf("rejected retry started MCP: %v", err)
	}
}
