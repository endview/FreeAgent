package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
)

func TestModuleDryRunCleansExactTempAfterSuccessErrorAndCancellation(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		wantCode moduleApplyFailureCodeV1
		prepare  func(*testing.T, string, context.CancelFunc)
	}{
		{
			name: "successful candidate",
			prepare: func(_ *testing.T, _ string, _ context.CancelFunc) {
			},
		},
		{
			name:     "artifact drifts after TEMP creation",
			wantCode: moduleApplyFailureArtifact,
			prepare: func(t *testing.T, source string, _ context.CancelFunc) {
				t.Helper()
				manifest := filepath.Join(source, "module.yaml")
				canonical, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.WriteFile(manifest, canonical, 0o600); err != nil {
						t.Errorf("restore source manifest: %v", err)
					}
				})
				if err := os.WriteFile(manifest, append(canonical, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "context cancels after TEMP creation",
			wantCode: moduleApplyFailureCancelled,
			prepare: func(_ *testing.T, _ string, cancel context.CancelFunc) {
				cancel()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			fixture := newModuleApplyDeclarativeFixtureV1(
				t,
				moduleApplyRoleID,
				moduleApplyRoleInstance,
			)
			fixture.ArtifactDirectory = filepath.Join(root, "source")
			copyModuleApplyTestTreeV1(
				t,
				filepath.Join(
					filepath.Dir(exampleSeedPath(t)),
					"bootstrap-artifacts",
					fixture.ModuleID,
					moduleApplyTestVersion,
				),
				fixture.ArtifactDirectory,
			)
			canonicalPlan := newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0)
			plan, canonical, digest, err := restoreModuleApplyPlanV1(canonicalPlan)
			if err != nil {
				t.Fatal(err)
			}
			observer, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
			if err != nil {
				t.Fatal(err)
			}
			basis, control, catalog, err := observer.LoadPublishedBasis(
				ctx,
				defaultTenantID,
			)
			if err != nil {
				_ = observer.Close()
				t.Fatal(err)
			}
			databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
			artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
			production := productionModuleDryRunTempFilesystemV1()
			var capturedTemp string
			filesystem := moduleDryRunTempFilesystemV1{
				mkdirTemp: func(directory string, pattern string) (string, error) {
					created, createErr := production.mkdirTemp(directory, pattern)
					if createErr == nil {
						capturedTemp = created
						test.prepare(t, fixture.ArtifactDirectory, cancel)
					}
					return created, createErr
				},
				removeAll: production.removeAll,
				lstat:     production.lstat,
			}
			_, dryErr := prepareEnabledModuleDryRunWithFilesystemV1(
				ctx,
				observer,
				artifactRoot,
				moduleApplyCommandInputV1{
					DatabasePath:      databasePath,
					ArtifactRoot:      artifactRoot,
					ArtifactDirectory: fixture.ArtifactDirectory,
					Plan:              plan,
					PlanCanonical:     canonical,
					PlanDigest:        digest,
				},
				basis,
				control,
				catalog,
				filesystem,
			)
			closeErr := observer.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if test.wantCode == "" {
				if dryErr != nil {
					t.Fatalf("successful TEMP candidate: %v", dryErr)
				}
			} else if dryErr == nil || moduleApplyFailureCodeOfV1(dryErr) != test.wantCode {
				t.Fatalf("post-TEMP error=%v code=%q", dryErr, moduleApplyFailureCodeOfV1(dryErr))
			}
			if capturedTemp == "" {
				t.Fatal("TEMP creation seam was not reached")
			}
			if _, err := os.Lstat(capturedTemp); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("post-TEMP failure left %q: %v", capturedTemp, err)
			}
			if databaseAfter := snapshotModuleDryRunDatabaseV1(t, databasePath); !reflect.DeepEqual(databaseAfter, databaseBefore) {
				t.Fatalf("post-TEMP failure changed Store: before=%+v after=%+v", databaseBefore, databaseAfter)
			}
			if artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot); !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
				t.Fatalf("post-TEMP failure changed artifact root: before=%+v after=%+v", artifactsBefore, artifactsAfter)
			}
		})
	}
}

type moduleDryRunSchemaSnapshotV1 struct {
	TableCount  int
	Fingerprint string
}

func TestModuleDryRunPreservesOwnerLockAndExactSchema(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	ownerLock := databasePath + ".freeagent.owner.lock"
	const ownerSentinel = "preexisting-owner-lock-observation-sentinel"
	if err := os.WriteFile(ownerLock, []byte(ownerSentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	filesBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	if _, included := filesBefore[filepath.Base(ownerLock)]; !included {
		t.Fatal("database snapshot omitted .freeagent.owner.lock")
	}
	schemaBefore := inspectModuleDryRunSchemaV1(t, databasePath)
	if schemaBefore.TableCount != 43 ||
		schemaBefore.Fingerprint != currentstore.ExpectedSchemaFingerprint {
		t.Fatalf("pre-dry-run schema=%+v", schemaBefore)
	}
	if _, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	); err != nil {
		t.Fatal(err)
	}
	filesAfter := snapshotModuleDryRunDatabaseV1(t, databasePath)
	if !reflect.DeepEqual(filesAfter, filesBefore) {
		t.Fatalf("dry-run changed database/owner files: before=%+v after=%+v", filesBefore, filesAfter)
	}
	schemaAfter := inspectModuleDryRunSchemaV1(t, databasePath)
	if schemaAfter != schemaBefore || schemaAfter.TableCount != 43 ||
		schemaAfter.Fingerprint != currentstore.ExpectedSchemaFingerprint {
		t.Fatalf("dry-run schema drift: before=%+v after=%+v", schemaBefore, schemaAfter)
	}
}

func inspectModuleDryRunSchemaV1(
	t *testing.T,
	databasePath string,
) moduleDryRunSchemaSnapshotV1 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite_%'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := currentstore.DatabaseSchemaFingerprint(
		context.Background(),
		database,
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleDryRunSchemaSnapshotV1{
		TableCount:  count,
		Fingerprint: fingerprint,
	}
}
