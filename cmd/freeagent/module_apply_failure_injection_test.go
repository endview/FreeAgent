package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyFailureHelperEnvironment = "FREEAGENT_MODULE_APPLY_FAILURE_HELPER"
	moduleApplyFailureHelperDatabase    = "FREEAGENT_MODULE_APPLY_FAILURE_DB"
	moduleApplyFailureHelperArtifacts   = "FREEAGENT_MODULE_APPLY_FAILURE_ARTIFACTS"
	moduleApplyFailureHelperPlan        = "FREEAGENT_MODULE_APPLY_FAILURE_PLAN"
	moduleApplyFailureHelperSource      = "FREEAGENT_MODULE_APPLY_FAILURE_SOURCE"
	moduleApplyFailureHelperGrant       = "FREEAGENT_MODULE_APPLY_FAILURE_GRANT"
	moduleApplyFailureHelperMarker      = "FREEAGENT_MODULE_APPLY_FAILURE_MARKER"
)

type moduleApplyFailureInjectionFixtureV1 struct {
	root         string
	databasePath string
	artifactRoot string
	eventPath    string
	planPath     string
	module       moduleApplyMCPFixtureV1
}

func TestModuleApplyExistingFinalArtifactIsRejectedWithoutOverwrite(
	t *testing.T,
) {
	for _, test := range []struct {
		name  string
		stage func(*testing.T, moduleApplyFailureInjectionFixtureV1, string) string
	}{
		{
			name: "corrupt bytes",
			stage: func(
				t *testing.T,
				fixture moduleApplyFailureInjectionFixtureV1,
				destination string,
			) string {
				t.Helper()
				path := filepath.Join(destination, "content", "sentinel.txt")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("do-not-overwrite-corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "extra file",
			stage: func(
				t *testing.T,
				fixture moduleApplyFailureInjectionFixtureV1,
				destination string,
			) string {
				t.Helper()
				if err := stageVerifiedArtifact(
					context.Background(),
					fixture.module.ArtifactDirectory,
					destination,
					fixture.module.ArtifactDigest,
					fixture.module.ArtifactSizeBytes,
				); err != nil {
					t.Fatalf("stage exact conflicting artifact: %v", err)
				}
				path := filepath.Join(destination, "content", "injected-extra.txt")
				if err := os.WriteFile(path, []byte("do-not-overwrite-extra"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleApplyFailureInjectionFixtureV1(t)
			destination := filepath.Join(
				fixture.artifactRoot,
				fixture.module.ArtifactDigest,
			)
			sentinelPath := test.stage(t, fixture, destination)
			sentinelBefore, err := os.ReadFile(sentinelPath)
			if err != nil {
				t.Fatal(err)
			}
			before := observeModuleApplyStateV1(
				t,
				fixture.databasePath,
				fixture.artifactRoot,
			)

			requireModuleApplyInjectedFailureV1(t, fixture, "(ARTIFACT_INVALID)")

			after := observeModuleApplyStateV1(
				t,
				fixture.databasePath,
				fixture.artifactRoot,
			)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf(
					"artifact rejection changed Store or artifact-root entries:\nbefore=%+v\nafter=%+v",
					before,
					after,
				)
			}
			sentinelAfter, err := os.ReadFile(sentinelPath)
			if err != nil || !bytes.Equal(sentinelAfter, sentinelBefore) {
				t.Fatalf("existing final artifact was overwritten: %v", err)
			}
			assertNoModuleApplyStageResidueV1(t, fixture.root)
			assertModuleApplyMCPNotStartedV1(t, fixture.eventPath)

			if err := os.RemoveAll(destination); err != nil {
				t.Fatal(err)
			}
			requireModuleApplyConvergenceV1(t, fixture, 1)
		})
	}
}

func TestModuleApplyImmutableAndPublicationFailuresConvergeOnExactRetry(
	t *testing.T,
) {
	tests := []struct {
		name                   string
		wantFailure            string
		wantActivationRevision uint64
		inject                 func(*testing.T, moduleApplyFailureInjectionFixtureV1) func()
	}{
		{
			name:                   "InstallModule ID collision",
			wantFailure:            "(PUBLICATION_FAILED)",
			wantActivationRevision: 1,
			inject:                 injectModuleApplyInstallationCollisionV1,
		},
		{
			name:                   "ActivateModule ID collision",
			wantFailure:            "(PUBLICATION_FAILED)",
			wantActivationRevision: 1,
			inject:                 injectModuleApplyActivationCollisionV1,
		},
		{
			name:                   "PutContent collision",
			wantFailure:            "(PUBLICATION_FAILED)",
			wantActivationRevision: 2,
			inject:                 injectModuleApplyContentCollisionV1,
		},
		{
			name:                   "Control publication collision before CAS",
			wantFailure:            "(PUBLICATION_FAILED)",
			wantActivationRevision: 2,
			inject:                 injectModuleApplyControlPublicationCollisionV1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleApplyFailureInjectionFixtureV1(t)
			cleanup := test.inject(t, fixture)
			cleaned := false
			t.Cleanup(func() {
				if !cleaned {
					cleanup()
				}
			})

			requireModuleApplyInjectedFailureV1(t, fixture, test.wantFailure)
			assertModuleApplyPointerV1(t, fixture.databasePath, 1, false)
			if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
				context.Background(),
				filepath.Join(fixture.artifactRoot, fixture.module.ArtifactDigest),
				moduleapi.ArtifactMetadataPaths{},
				fixture.module.ArtifactDigest,
				fixture.module.ArtifactSizeBytes,
			); err != nil {
				t.Fatalf("failure did not leave one reusable exact artifact: %v", err)
			}
			assertNoModuleApplyStageResidueV1(t, fixture.root)
			assertModuleApplyMCPNotStartedV1(t, fixture.eventPath)

			cleanup()
			cleaned = true
			requireModuleApplyConvergenceV1(
				t,
				fixture,
				test.wantActivationRevision,
			)
		})
	}
}

func TestModuleApplyProcessCrashAfterVerifiedCASConvergesToAlreadyApplied(
	t *testing.T,
) {
	if os.Getenv(moduleApplyFailureHelperEnvironment) == "1" {
		t.Fatal("parent failure-injection test entered helper mode")
	}
	fixture := newModuleApplyFailureInjectionFixtureV1(t)
	markerPath := filepath.Join(fixture.root, "after-cas.marker")
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestModuleApplyFailureInjectionHelperProcess$",
		"-test.v",
	)
	command.Env = append(
		os.Environ(),
		moduleApplyFailureHelperEnvironment+"=1",
		moduleApplyFailureHelperDatabase+"="+fixture.databasePath,
		moduleApplyFailureHelperArtifacts+"="+fixture.artifactRoot,
		moduleApplyFailureHelperPlan+"="+fixture.planPath,
		moduleApplyFailureHelperSource+"="+fixture.module.ArtifactDirectory,
		moduleApplyFailureHelperGrant+"="+fixture.module.ArtifactDigest,
		moduleApplyFailureHelperMarker+"="+markerPath,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start post-CAS helper: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waitForModuleApplyFailureMarkerV1(t, command, done, markerPath, &output)
	killErr := command.Process.Kill()
	select {
	case waitErr := <-done:
		if killErr != nil || waitErr == nil {
			t.Fatalf(
				"stop post-CAS helper: kill=%v wait=%v; want nil kill error and non-nil wait error",
				killErr,
				waitErr,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("wait for killed post-CAS helper timed out: kill=%v", killErr)
	}

	assertModuleApplyPointerV1(t, fixture.databasePath, 2, true)
	result, err := runModuleApplyFixtureV1(
		fixture.databasePath,
		fixture.artifactRoot,
		fixture.planPath,
		fixture.module.ArtifactDirectory,
		fixture.module.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusAlreadyApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("post-CAS crash retry=%+v, %v", result, err)
	}
	if latest := readLatestModuleApplyActivationV1(t, fixture.databasePath); latest.ActivationRevision != 1 {
		t.Fatalf("post-CAS exact retry created Activation %+v", latest)
	}
	assertNoModuleApplyStageResidueV1(t, fixture.root)
	assertModuleApplyMCPNotStartedV1(t, fixture.eventPath)
}

func TestModuleApplyFailureInjectionHelperProcess(t *testing.T) {
	if os.Getenv(moduleApplyFailureHelperEnvironment) != "1" {
		return
	}
	args := []string{
		"--db", os.Getenv(moduleApplyFailureHelperDatabase),
		"--artifact-root", os.Getenv(moduleApplyFailureHelperArtifacts),
		"--plan", os.Getenv(moduleApplyFailureHelperPlan),
		"--artifact", os.Getenv(moduleApplyFailureHelperSource),
		"--allow-local-mcp-artifact", os.Getenv(moduleApplyFailureHelperGrant),
	}
	err := runModuleApply(
		context.Background(),
		args,
		moduleApplyCrashAfterWriteV1{markerPath: os.Getenv(moduleApplyFailureHelperMarker)},
		io.Discard,
	)
	t.Fatalf("post-CAS helper returned before it was killed: %v", err)
}

type moduleApplyCrashAfterWriteV1 struct {
	markerPath string
}

func (writer moduleApplyCrashAfterWriteV1) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, errors.New("module apply result write was empty")
	}
	if err := os.WriteFile(writer.markerPath, []byte("verified-cas\n"), 0o600); err != nil {
		return 0, err
	}
	select {}
}

func newModuleApplyFailureInjectionFixtureV1(
	t *testing.T,
) moduleApplyFailureInjectionFixtureV1 {
	t.Helper()
	root := t.TempDir()
	fixture := moduleApplyFailureInjectionFixtureV1{
		root:         root,
		databasePath: filepath.Join(root, "current.sqlite"),
		artifactRoot: filepath.Join(root, "artifacts"),
		eventPath:    filepath.Join(root, "mcp-events.log"),
	}
	fixture.module = newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		fixture.eventPath,
	)
	initializePureChatForModuleApplyV1(t, fixture.databasePath, fixture.artifactRoot)
	fixture.planPath = writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture.module, 1, 0),
	)
	return fixture
}

func requireModuleApplyInjectedFailureV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
	wantCode string,
) {
	t.Helper()
	result, err := runModuleApplyFixtureV1(
		fixture.databasePath,
		fixture.artifactRoot,
		fixture.planPath,
		fixture.module.ArtifactDirectory,
		fixture.module.ArtifactDigest,
	)
	if err == nil || !strings.Contains(err.Error(), wantCode) {
		t.Fatalf("injected apply result=%+v error=%v, want %s", result, err, wantCode)
	}
}

func requireModuleApplyConvergenceV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
	wantActivationRevision uint64,
) {
	t.Helper()
	result, err := runModuleApplyFixtureV1(
		fixture.databasePath,
		fixture.artifactRoot,
		fixture.planPath,
		fixture.module.ArtifactDirectory,
		fixture.module.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusApplied || result.PointerRevision != 2 {
		t.Fatalf("same-plan recovery apply=%+v, %v", result, err)
	}
	result, err = runModuleApplyFixtureV1(
		fixture.databasePath,
		fixture.artifactRoot,
		fixture.planPath,
		fixture.module.ArtifactDirectory,
		fixture.module.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusAlreadyApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("same-plan exact retry=%+v, %v", result, err)
	}
	if latest := readLatestModuleApplyActivationV1(t, fixture.databasePath); latest.ActivationRevision != wantActivationRevision {
		t.Fatalf(
			"same-plan recovery Activation=%+v, want revision %d",
			latest,
			wantActivationRevision,
		)
	}
	assertModuleApplyPointerV1(t, fixture.databasePath, 2, true)
	assertNoModuleApplyStageResidueV1(t, fixture.root)
	assertModuleApplyMCPNotStartedV1(t, fixture.eventPath)
}

func assertModuleApplyPointerV1(
	t *testing.T,
	databasePath string,
	wantRevision uint64,
	wantInstance bool,
) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	basis, _, catalog, loadErr := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := store.Close()
	_, found := catalog.FindInstance(moduleApplyTestInstanceID)
	if loadErr != nil || closeErr != nil || basis.PointerRevision != wantRevision ||
		found != wantInstance {
		t.Fatalf(
			"published state basis=%+v instance_found=%v errors=%v",
			basis,
			found,
			errors.Join(loadErr, closeErr),
		)
	}
}

func injectModuleApplyInstallationCollisionV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
) func() {
	t.Helper()
	fakeManifestRef := moduleapi.Digest(
		"freeagent.test.module-apply-install-collision-manifest/v1",
		[]byte(t.Name()),
	)
	fakeArtifactDigest := moduleapi.Digest(
		"freeagent.test.module-apply-install-collision-artifact/v1",
		[]byte(t.Name()),
	)
	installationID := moduleApplyInstallationPrefix + fixture.module.ArtifactDigest
	canonical := []byte(`{"collision":true}`)
	withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO content_records(
				content_digest, kind, media_type, canonical_bytes,
				size_bytes, created_at
			) VALUES(?, 'MODULE_MANIFEST', 'application/json', ?, ?, ?)
		`, fakeManifestRef, canonical, len(canonical), time.Now().UTC().UnixMicro()); err != nil {
			t.Fatalf("inject installation manifest collision: %v", err)
		}
		if _, err := database.Exec(`
			INSERT INTO module_installations(
				installation_id, module_id, exact_version, manifest_ref,
				artifact_digest, installed_at
			) VALUES(?, 'freeagent.test.install_collision', '9.9.9', ?, ?, ?)
		`, installationID, fakeManifestRef, fakeArtifactDigest, time.Now().UTC().UnixMicro()); err != nil {
			t.Fatalf("inject installation ID collision: %v", err)
		}
	})
	return func() {
		withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
			if _, err := database.Exec(
				`DELETE FROM module_installations WHERE installation_id=?`,
				installationID,
			); err != nil {
				t.Fatalf("remove installation collision: %v", err)
			}
		})
		execCmdClosedFileTamperV1(
			t,
			fixture.databasePath,
			[]string{"content_records_reject_delete"},
			`DELETE FROM content_records WHERE content_digest=?`,
			fakeManifestRef,
		)
	}
}

func injectModuleApplyActivationCollisionV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
) func() {
	t.Helper()
	installation := installExactModuleApplyFixtureV1(t, fixture)
	activationID := moduleApplyActivationIDV1(t, installation)
	withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO module_activations(
				activation_id, tenant_id, instance_id, installation_id,
				activation_revision, execution_class, adapter_identity,
				activated_at
			) VALUES(?, 'collision-tenant', 'collision-instance', ?, 1,
				'LOCAL_PROCESS', ?, ?)
		`, activationID, installation.InstallationID, mcpstdio.AdapterIdentityV1,
			time.Now().UTC().UnixMicro()); err != nil {
			t.Fatalf("inject activation ID collision: %v", err)
		}
	})
	return func() {
		withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
			if _, err := database.Exec(
				`DELETE FROM module_activations WHERE activation_id=?`,
				activationID,
			); err != nil {
				t.Fatalf("remove activation collision: %v", err)
			}
		})
	}
}

func injectModuleApplyContentCollisionV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
) func() {
	t.Helper()
	plan, _, _, err := readModuleApplyPlanV1(fixture.planPath)
	if err != nil || plan.Binding == nil {
		t.Fatalf("read content-collision plan: %v", err)
	}
	configRef, _, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte(`{"collision":true}`)
	withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO content_records(
				content_digest, kind, media_type, canonical_bytes,
				size_bytes, created_at
			) VALUES(?, 'TASK_INPUT', 'application/json', ?, ?, ?)
		`, configRef, canonical, len(canonical), time.Now().UTC().UnixMicro()); err != nil {
			t.Fatalf("inject PutContent collision: %v", err)
		}
	})
	return func() {
		execCmdClosedFileTamperV1(
			t,
			fixture.databasePath,
			[]string{"content_records_reject_delete"},
			`DELETE FROM content_records WHERE content_digest=?`,
			configRef,
		)
	}
}

func injectModuleApplyControlPublicationCollisionV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
) func() {
	t.Helper()
	_, _, planDigest, err := readModuleApplyPlanV1(fixture.planPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, control, _, loadErr := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := store.Close()
	if loadErr != nil || closeErr != nil {
		t.Fatal(errors.Join(loadErr, closeErr))
	}
	snapshotID := moduleApplyControlIDPrefixV1 + planDigest
	fakeDigest := moduleapi.Digest(
		"freeagent.test.module-apply-control-collision/v1",
		[]byte(t.Name()),
	)
	withModuleApplyFaultDatabaseV1(t, fixture.databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO control_snapshots(
				snapshot_id, tenant_id, revision, canonical_json,
				digest, published_at
			) VALUES(?, ?, ?, ?, ?, ?)
		`, snapshotID, defaultTenantID, control.Revision+1, []byte(`{}`), fakeDigest,
			time.Now().UTC().UnixMicro()); err != nil {
			t.Fatalf("inject Control publication collision: %v", err)
		}
	})
	return func() {
		execCmdClosedFileTamperV1(
			t,
			fixture.databasePath,
			[]string{"control_snapshots_reject_delete"},
			`DELETE FROM control_snapshots WHERE snapshot_id=?`,
			snapshotID,
		)
	}
}

func installExactModuleApplyFixtureV1(
	t *testing.T,
	fixture moduleApplyFailureInjectionFixtureV1,
) currentstore.ModuleInstallation {
	t.Helper()
	manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		context.Background(),
		fixture.module.ArtifactDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		manifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, installErr := store.InstallModule(
		context.Background(),
		currentstore.InstallModuleInput{
			InstallationID:      moduleApplyInstallationPrefix + fixture.module.ArtifactDigest,
			ModuleID:            fixture.module.ModuleID,
			ExactVersion:        moduleApplyTestVersion,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifest,
			ArtifactDigest:      fixture.module.ArtifactDigest,
		},
	)
	closeErr := store.Close()
	if installErr != nil || closeErr != nil {
		t.Fatal(errors.Join(installErr, closeErr))
	}
	return installation
}

func moduleApplyActivationIDV1(
	t *testing.T,
	installation currentstore.ModuleInstallation,
) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tenant_id":           defaultTenantID,
		"instance_id":         moduleApplyTestInstanceID,
		"activation_revision": uint64(1),
		"installation": map[string]any{
			"installation_id": installation.InstallationID,
			"module_id":       installation.ModuleID,
			"exact_version":   installation.ExactVersion,
			"manifest_ref":    installation.ManifestRef,
			"artifact_digest": installation.ArtifactDigest,
		},
		"provider": map[string]any{
			"execution_class":  string(moduleapi.ExecutionLocalProcess),
			"adapter_identity": mcpstdio.AdapterIdentityV1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	return moduleapi.Digest("freeagent.module-activation/v1", canonical)
}

func withModuleApplyFaultDatabaseV1(
	t *testing.T,
	databasePath string,
	operation func(*sql.DB),
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = database.Close()
		}
	}()
	operation(database)
	if err := database.Close(); err != nil {
		t.Fatalf("close module-apply fault database: %v", err)
	}
	closed = true
}

func waitForModuleApplyFailureMarkerV1(
	t *testing.T,
	command *exec.Cmd,
	done <-chan error,
	markerPath string,
	output *bytes.Buffer,
) {
	t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("post-CAS helper exited before marker: %v\n%s", err, output.String())
		case <-ticker.C:
			content, err := os.ReadFile(markerPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || string(content) != "verified-cas\n" {
				killErr := command.Process.Kill()
				select {
				case waitErr := <-done:
					t.Fatalf(
						"read post-CAS marker: content=%q error=%v kill=%v wait=%v",
						content,
						err,
						killErr,
						waitErr,
					)
				case <-time.After(5 * time.Second):
					t.Fatalf(
						"read post-CAS marker: content=%q error=%v kill=%v wait timed out",
						content,
						err,
						killErr,
					)
				}
			}
			return
		case <-timer.C:
			killErr := command.Process.Kill()
			select {
			case waitErr := <-done:
				t.Fatalf(
					"timed out waiting for post-CAS marker (kill=%v wait=%v)\n%s",
					killErr,
					waitErr,
					output.String(),
				)
			case <-time.After(5 * time.Second):
				t.Fatalf(
					"timed out waiting for post-CAS marker (kill=%v wait timed out)\n%s",
					killErr,
					output.String(),
				)
			}
		}
	}
}
