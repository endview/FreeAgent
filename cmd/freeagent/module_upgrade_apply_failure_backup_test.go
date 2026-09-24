package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// TestModuleUpgradeApplyCurrentDriftStopsBeforeStageOrPublication proves the
// first approved-Apply slice against mutable admission drift. Source drift is
// caught by the production PreStageCheck. Pointer drift is rejected by the
// earlier exact-plan basis gate. Neither path may create an artifact stage,
// install the reviewed target, or publish another Control/Catalog pair.
func TestModuleUpgradeApplyCurrentDriftStopsBeforeStageOrPublication(
	t *testing.T,
) {
	tests := []struct {
		name        string
		wantFailure string
		drift       func(*testing.T, moduleUpgradeApplyAcceptedFixtureV1)
	}{
		{
			name:        "Source policy revision",
			wantFailure: "freeagent module-upgrade-apply: failed (APPROVAL_INVALID)",
			drift: func(t *testing.T, fixture moduleUpgradeApplyAcceptedFixtureV1) {
				t.Helper()
				store := openModuleUpgradeApplyStoreV1(t, fixture.databasePath)
				approval, err := store.LoadModuleUpgradeApplyApproval(
					context.Background(),
					fixture.approvalInput(),
				)
				if err != nil {
					_ = store.Close()
					t.Fatal(err)
				}
				policy := approval.Source.Policy
				policy.AllowedModuleIDPrefixes = append(
					append([]string(nil), policy.AllowedModuleIDPrefixes...),
					"future.approved-prefix",
				)
				_, canonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
				if err == nil {
					_, err = store.RegisterModuleSource(
						context.Background(),
						currentstore.RegisterModuleSourceInput{
							PolicyCanonical:        canonical,
							ExpectedPolicyRevision: approval.Source.PolicyRevision,
						},
					)
				}
				if closeErr := store.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					t.Fatalf("advance Source policy: %v", err)
				}
			},
		},
		{
			name:        "Source snapshot head",
			wantFailure: "freeagent module-upgrade-apply: failed (APPROVAL_INVALID)",
			drift: func(t *testing.T, fixture moduleUpgradeApplyAcceptedFixtureV1) {
				t.Helper()
				store := openModuleUpgradeApplyStoreV1(t, fixture.databasePath)
				approval, err := store.LoadModuleUpgradeApplyApproval(
					context.Background(),
					fixture.approvalInput(),
				)
				if err != nil {
					_ = store.Close()
					t.Fatal(err)
				}
				refresh, err := store.ReadModuleSourceRefreshBasis(
					context.Background(),
					fixture.sourceID,
				)
				entry := approval.TargetEntry
				entry.PackagePath = "packages/current"
				_, indexCanonical, _, indexErr := moduleapi.NewModuleDiscoveryIndexV1(
					moduleapi.ModuleDiscoveryIndexV1{
						SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
						SourceID:      fixture.sourceID,
						Entries:       []moduleapi.ModuleDiscoveryEntryV1{entry},
					},
				)
				if err == nil {
					err = indexErr
				}
				if err == nil {
					_, err = store.CommitModuleSourceRefresh(
						context.Background(),
						refresh,
						indexCanonical,
					)
				}
				if closeErr := store.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					t.Fatalf("advance Source snapshot head: %v", err)
				}
			},
		},
		{
			name:        "current publication pointer",
			wantFailure: "freeagent module-upgrade-apply: failed (POINTER_CONFLICT)",
			drift: func(t *testing.T, fixture moduleUpgradeApplyAcceptedFixtureV1) {
				t.Helper()
				post := newModuleApplyDeclarativeFixtureV1(
					t,
					moduleApplySkillID,
					"u4-drift-before-approved-apply",
				)
				planPath := writeModuleApplyPlanFixtureV1(
					t,
					filepath.Join(fixture.root, "pointer-drift.json"),
					newEnabledDeclarativeModuleApplyPlanV1(
						t,
						post,
						fixture.publishedBasis.PointerRevision,
						1,
					),
				)
				result, err := runModuleApplyFixtureV1(
					fixture.databasePath,
					fixture.artifactRoot,
					planPath,
					post.ArtifactDirectory,
					"",
				)
				if err != nil || result.Status != moduleApplyStatusApplied ||
					result.PointerRevision != fixture.publishedBasis.PointerRevision+1 {
					t.Fatalf("publish pointer drift: %+v, %v", result, err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleUpgradeApplyAcceptedFixtureV1(t)
			test.drift(t, fixture)

			beforeTree := snapshotModuleUpgradeApplyTreeV1(t, fixture.artifactRoot)
			beforeBasis := loadModuleUpgradeApplyPublishedBasisV1(t, fixture.databasePath)
			beforeRows := moduleUpgradeApplyPublicationRowCountsV1(
				t,
				fixture.databasePath,
			)
			var output bytes.Buffer
			err := runModuleUpgradeApply(
				context.Background(),
				fixture.applyArgs(),
				&output,
				ioDiscardModuleUpgradeApplyV1{},
			)
			if err == nil || err.Error() != test.wantFailure || output.Len() != 0 {
				t.Fatalf("drifted Apply error=%v output=%q", err, output.String())
			}
			afterTree := snapshotModuleUpgradeApplyTreeV1(t, fixture.artifactRoot)
			afterBasis := loadModuleUpgradeApplyPublishedBasisV1(t, fixture.databasePath)
			afterRows := moduleUpgradeApplyPublicationRowCountsV1(
				t,
				fixture.databasePath,
			)
			if !reflect.DeepEqual(afterTree, beforeTree) {
				t.Fatalf("drifted Apply touched artifact tree:\nbefore=%v\nafter=%v", beforeTree, afterTree)
			}
			if afterBasis != beforeBasis || afterRows != beforeRows {
				t.Fatalf(
					"drifted Apply published facts: basis before=%+v after=%+v rows before=%+v after=%+v",
					beforeBasis,
					afterBasis,
					beforeRows,
					afterRows,
				)
			}
			assertNoModuleApplyStageResidueV1(t, fixture.artifactRoot)
			if got := moduleUpgradeApplyTargetActivationCountV1(
				t,
				fixture.databasePath,
				fixture.targetInstanceID,
			); got != 0 {
				t.Fatalf("drifted Apply persisted target Activation count=%d", got)
			}
			assertModuleUpgradeApplySchemaFAC2V1(t, fixture.databasePath)
		})
	}
}

func TestApprovedModuleUpgradeApplyBackupVerifyRestorePreservesExactEvidence(
	t *testing.T,
) {
	fixture := newModuleUpgradeApplyAcceptedFixtureV1(t)
	var output bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		fixture.applyArgs(),
		&output,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("approved Apply: %v", err)
	}
	var applied moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, output.Bytes(), &applied)
	if applied.Status != moduleApplyStatusApplied {
		t.Fatalf("approved Apply result=%+v", applied)
	}

	beforeStore := openModuleUpgradeApplyStoreV1(t, fixture.databasePath)
	beforeApproval, err := beforeStore.LoadModuleUpgradeApplyApproval(
		context.Background(),
		fixture.approvalInput(),
	)
	if err != nil {
		_ = beforeStore.Close()
		t.Fatal(err)
	}
	beforeBasis, _, _, basisErr := beforeStore.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if closeErr := beforeStore.Close(); basisErr == nil {
		basisErr = closeErr
	}
	if basisErr != nil {
		t.Fatal(basisErr)
	}
	if beforeBasis.PointerRevision != applied.PointerRevision ||
		beforeBasis.Control.SnapshotID != applied.ControlSnapshotID ||
		beforeBasis.Catalog.GenerationID != applied.CatalogGenerationID {
		t.Fatalf("final publication=%+v applied=%+v", beforeBasis, applied)
	}

	bundlePath := filepath.Join(fixture.root, "approved-apply-backup")
	created, err := currentbackup.CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundlePath,
		"freeagent-u4-approved-apply-backup/v1",
	)
	if err != nil {
		t.Fatalf("create approved Apply backup: %v", err)
	}
	// Verification and restore must depend only on the closed bundle. The
	// mutable discovery Source and the original artifact root are deliberately
	// absent, so neither can be rescanned, loaded, or executed.
	if err := os.RemoveAll(fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(fixture.artifactRoot); err != nil {
		t.Fatal(err)
	}
	verified, err := currentbackup.VerifyBundle(context.Background(), bundlePath)
	if err != nil || verified.ManifestDigest != created.ManifestDigest {
		t.Fatalf("verify approved Apply backup=%+v, %v", verified, err)
	}
	restoreRoot := filepath.Join(fixture.root, "restore")
	if err := os.MkdirAll(restoreRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	restoredDatabase := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := currentbackup.RestoreBundle(
		context.Background(),
		bundlePath,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("restore approved Apply backup: %v", err)
	}
	if _, err := os.Stat(fixture.sourceRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore recreated or accessed Source root: %v", err)
	}

	afterStore := openModuleUpgradeApplyStoreV1(t, restoredDatabase)
	afterApproval, err := afterStore.LoadModuleUpgradeApplyApproval(
		context.Background(),
		fixture.approvalInput(),
	)
	if err != nil {
		_ = afterStore.Close()
		t.Fatal(err)
	}
	afterBasis, _, _, basisErr := afterStore.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if closeErr := afterStore.Close(); basisErr == nil {
		basisErr = closeErr
	}
	if basisErr != nil {
		t.Fatal(basisErr)
	}
	if afterBasis != beforeBasis ||
		afterApproval.Review.ReviewID != beforeApproval.Review.ReviewID ||
		afterApproval.Decision.DecisionID != beforeApproval.Decision.DecisionID ||
		!bytes.Equal(afterApproval.Review.Canonical, beforeApproval.Review.Canonical) ||
		!bytes.Equal(afterApproval.Decision.Canonical, beforeApproval.Decision.Canonical) ||
		afterApproval.TargetManifest.Digest != beforeApproval.TargetManifest.Digest ||
		!bytes.Equal(
			afterApproval.TargetManifest.CanonicalBytes,
			beforeApproval.TargetManifest.CanonicalBytes,
		) {
		t.Fatalf(
			"approved Apply evidence drifted across restore:\nbasis before=%+v after=%+v\napproval before=%+v after=%+v",
			beforeBasis,
			afterBasis,
			beforeApproval.Input,
			afterApproval.Input,
		)
	}
	assertModuleUpgradeApplySchemaFAC2V1(t, restoredDatabase)
}

func TestModuleUpgradeApplyDefaultOffPureChatTouchesNoStoreOrFile(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	beforeBasis, beforeControl, beforeCatalog := loadModuleUpgradeApplyPublishedClosureV1(
		t,
		databasePath,
	)
	before := snapshotModuleUpgradeApplyTreeV1(t, root)
	missingSource := filepath.Join(root, "must-not-create-source")
	missingArtifact := filepath.Join(root, "must-not-create-artifact")
	var output bytes.Buffer
	err := runModuleUpgradeApply(
		context.Background(),
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--source-root", missingSource,
			"--artifact", missingArtifact,
		},
		&output,
		ioDiscardModuleUpgradeApplyV1{},
	)
	if err == nil ||
		err.Error() != "freeagent module-upgrade-apply: failed (UPGRADE_APPLY_DISABLED)" ||
		output.Len() != 0 {
		t.Fatalf("default-off result error=%v output=%q", err, output.String())
	}
	after := snapshotModuleUpgradeApplyTreeV1(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("default-off command touched Pure Chat files:\nbefore=%v\nafter=%v", before, after)
	}
	for _, path := range []string{missingSource, missingArtifact} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("default-off command touched %q: %v", path, err)
		}
	}
	afterBasis, afterControl, afterCatalog := loadModuleUpgradeApplyPublishedClosureV1(
		t,
		databasePath,
	)
	if afterBasis != beforeBasis ||
		!reflect.DeepEqual(afterControl, beforeControl) ||
		!reflect.DeepEqual(afterCatalog, beforeCatalog) {
		t.Fatalf(
			"default-off command changed Pure Chat closure:\nbasis before=%+v after=%+v\ncontrol before=%+v after=%+v\ncatalog before=%+v after=%+v",
			beforeBasis,
			afterBasis,
			beforeControl,
			afterControl,
			beforeCatalog,
			afterCatalog,
		)
	}
	assertModuleUpgradeApplySchemaFAC2V1(t, databasePath)
}

type moduleUpgradeApplyAcceptedFixtureV1 struct {
	moduleUpgradeCLIIntegrationFixtureV1
	artifactRoot     string
	reviewID         string
	decisionID       string
	targetInstanceID string
}

func newModuleUpgradeApplyAcceptedFixtureV1(
	t *testing.T,
) moduleUpgradeApplyAcceptedFixtureV1 {
	t.Helper()
	base := newModuleUpgradeCLIIntegrationFixtureV1(t)
	var reviewOutput bytes.Buffer
	if err := runModuleUpgradeReview(
		context.Background(),
		base.reviewArgs(),
		&reviewOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record Review: %v", err)
	}
	var review moduleUpgradeReviewCommandResultV1
	decodeModuleOperatorOutputV1(t, reviewOutput.Bytes(), &review)
	reasonPath := filepath.Join(base.root, "approved-apply-reason.txt")
	if err := os.WriteFile(reasonPath, []byte("approve exact U4 replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	var decisionOutput bytes.Buffer
	if err := runModuleUpgradeDecide(
		context.Background(),
		[]string{
			"--enable-module-upgrade-review",
			"--db", base.databasePath,
			"--tenant", defaultTenantID,
			"--review-id", review.ReviewID,
			"--candidate-id", review.CandidateID,
			"--decision", "APPROVE",
			"--operator-principal", "operator.u4.failure-backup",
			"--reason-file", reasonPath,
		},
		&decisionOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record Decision: %v", err)
	}
	var decision struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		TenantID      string `json:"tenant_id"`
		CandidateID   string `json:"candidate_id"`
		ReviewID      string `json:"review_id"`
		DecisionID    string `json:"decision_id"`
	}
	decodeModuleOperatorOutputV1(t, decisionOutput.Bytes(), &decision)
	return moduleUpgradeApplyAcceptedFixtureV1{
		moduleUpgradeCLIIntegrationFixtureV1: base,
		artifactRoot:                         filepath.Join(base.root, "artifacts"),
		reviewID:                             review.ReviewID,
		decisionID:                           decision.DecisionID,
		targetInstanceID:                     base.upgradeBasis.Selection.TargetInstanceID,
	}
}

func (fixture moduleUpgradeApplyAcceptedFixtureV1) approvalInput() currentstore.ModuleUpgradeApplyApprovalInput {
	return currentstore.ModuleUpgradeApplyApprovalInput{
		TenantID: defaultTenantID, ReviewID: fixture.reviewID,
		DecisionID: fixture.decisionID,
	}
}

func (fixture moduleUpgradeApplyAcceptedFixtureV1) applyArgs() []string {
	return []string{
		"--enable-module-upgrade-apply",
		"--db", fixture.databasePath,
		"--artifact-root", fixture.artifactRoot,
		"--source-root", fixture.sourceRoot,
		"--tenant", defaultTenantID,
		"--review-id", fixture.reviewID,
		"--decision-id", fixture.decisionID,
		"--artifact", fixture.targetDirectory,
		"--signature=",
		"--allow-local-mcp-artifact=",
		"--allow-trusted-in-process-artifact=",
		"--allow-remote-action-artifact=",
		"--allow-wasm-action-artifact=",
		"--allow-remote-action-endpoint=",
		"--allow-remote-action-secret-ref=",
		"--allow-model-secret-ref=",
	}
}

type moduleUpgradeApplyPublicationRowsV1 struct {
	ControlSnapshots   int
	CatalogGenerations int
	Installations      int
	Activations        int
}

func moduleUpgradeApplyPublicationRowCountsV1(
	t *testing.T,
	databasePath string,
) moduleUpgradeApplyPublicationRowsV1 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	result := moduleUpgradeApplyPublicationRowsV1{}
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM control_snapshots`:           &result.ControlSnapshots,
		`SELECT COUNT(*) FROM runtime_catalog_generations`: &result.CatalogGenerations,
		`SELECT COUNT(*) FROM module_installations`:        &result.Installations,
		`SELECT COUNT(*) FROM module_activations`:          &result.Activations,
	} {
		if err := database.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func moduleUpgradeApplyTargetActivationCountV1(
	t *testing.T,
	databasePath string,
	instanceID string,
) int {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM module_activations WHERE instance_id=?`,
		instanceID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertModuleUpgradeApplySchemaFAC2V1(t *testing.T, databasePath string) {
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
	if count != 42 {
		t.Fatalf("ordinary table count=%d want=42", count)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_schema
		WHERE type='table' AND name='control_operation_receipts'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("control_operation_receipts table count=%d want=1", count)
	}
}

func openModuleUpgradeApplyStoreV1(
	t *testing.T,
	databasePath string,
) *currentstore.Store {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func loadModuleUpgradeApplyPublishedBasisV1(
	t *testing.T,
	databasePath string,
) controlcontract.PublishedBasis {
	t.Helper()
	basis, _, _ := loadModuleUpgradeApplyPublishedClosureV1(t, databasePath)
	return basis
}

func loadModuleUpgradeApplyPublishedClosureV1(
	t *testing.T,
	databasePath string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) {
	t.Helper()
	store := openModuleUpgradeApplyStoreV1(t, databasePath)
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return basis, control, catalog
}

func snapshotModuleUpgradeApplyTreeV1(t *testing.T, root string) []string {
	t.Helper()
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return []string{"<absent>"}
	} else if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, 16)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			result = append(result, "d:"+relative)
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		result = append(
			result,
			fmt.Sprintf(
				"f:%s:%d:%s",
				relative,
				len(payload),
				hex.EncodeToString(digest[:]),
			),
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(result)
	return result
}
