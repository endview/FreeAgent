package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type backupFixture struct {
	databasePath                       string
	artifactRoot                       string
	successfulRunID                    string
	pendingRunID                       string
	pendingAttemptID                   string
	pendingAttemptRevision             uint64
	pendingLease                       currentstore.RunLease
	pendingProvider                    moduleapi.ActivatedModuleRef
	pendingContextCompilationCanonical []byte
	pendingContextCompilationDigest    string
	pendingRequestCanonical            []byte
	pendingRequestDigest               string
	modelProfileRef                    corecontract.ModelProfileRef
	modelProfileCanonical              []byte
	memoryGenesisCanonical             []byte
	memoryCurrentCanonical             []byte
	memoryCurrentRef                   moduleapi.MemorySnapshotRefV1
}

type backupStateSnapshot struct {
	Runs     []backupRunReference
	Attempts []backupAttemptState
	Usage    []backupUsageState
	History  []backupHistoryState
	Events   []backupEventState
	Memory   []backupMemoryState
}

type backupRunReference struct {
	RunID          string
	MemberID       string
	ManifestDigest string
	MemberDigest   string
}

type backupAttemptState struct {
	AttemptID     string
	RunID         string
	LogicalStepID string
	State         string
	RequestRef    string
	RequestDigest string
	ResultRef     sql.NullString
	ProviderRef   sql.NullString
	EvidenceRef   sql.NullString
	Revision      int64
}

type backupUsageState struct {
	AttemptID            string
	LedgerSequence       sql.NullInt64
	Revision             int64
	InputTokens          sql.NullInt64
	CachedInputTokens    sql.NullInt64
	UncachedInputTokens  sql.NullInt64
	OutputTokens         sql.NullInt64
	ReasoningTokens      sql.NullInt64
	EstimatedCost        sql.NullString
	ProviderReportedCost sql.NullString
	ReconciledCost       sql.NullString
	ReconciliationStatus string
	RawReceiptRef        sql.NullString
}

type backupHistoryState struct {
	RunID           string
	Sequence        int64
	MemberID        string
	Role            string
	ContentRef      string
	ContentDigest   string
	SourceAttemptID sql.NullString
}

type backupEventState struct {
	RunID         string
	Sequence      int64
	Kind          string
	FromRevision  int64
	ToRevision    int64
	PayloadRef    string
	PayloadDigest string
}

type backupMemoryState struct {
	TenantID       string
	AgentID        string
	Revision       uint64
	SnapshotRef    string
	SourceAttempt  sql.NullString
	CanonicalBytes []byte
}

type yieldLoop struct{}

func (yieldLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{
		RunID:         input.RunID,
		Disposition:   loopapi.DispositionYielded,
		FrameRevision: 0,
		ReasonCode:    "test-yield",
	}, nil
}

func TestFullBundleRoundTripPreservesIdentityPendingAndNullUsage(t *testing.T) {
	fixture := newBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "full.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	if err := verifyNoSQLiteSidecars(fixture.databasePath); err != nil {
		t.Fatalf("CreateBundle() left source read residue: %v", err)
	}
	sourceState := loadBackupStateSnapshot(t, fixture.databasePath)
	assertSuccessfulAndPendingState(t, sourceState, fixture)
	if manifest.AttemptCounts.ModelPending != 1 ||
		manifest.AttemptCounts.ModelUnknown != 0 ||
		manifest.AttemptCounts.ActionPending != 0 ||
		manifest.AttemptCounts.ActionUnknown != 0 ||
		manifest.ArtifactCount == 0 {
		t.Fatalf("manifest = %+v", manifest)
	}
	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verified digest = %s, want %s", verified.ManifestDigest, manifest.ManifestDigest)
	}

	restoreParent := t.TempDir()
	restoredDatabase := filepath.Join(restoreParent, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreParent, "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}
	sourceVerification, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	restoredVerification, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restoredVerification.StoreInstanceID != sourceVerification.StoreInstanceID {
		t.Fatalf(
			"restored store_instance_id = %s, want %s",
			restoredVerification.StoreInstanceID,
			sourceVerification.StoreInstanceID,
		)
	}
	assertPendingNullUsage(t, restoredDatabase)
	restoredState := loadBackupStateSnapshot(t, restoredDatabase)
	if !reflect.DeepEqual(restoredState, sourceState) {
		t.Fatalf(
			"restored authoritative state differs:\nsource=%#v\nrestored=%#v",
			sourceState,
			restoredState,
		)
	}
	assertSuccessfulAndPendingState(t, restoredState, fixture)
	assertRestoredModelProfileClosure(t, restoredDatabase, fixture)
	for _, artifact := range manifest.Artifacts {
		verified, err := verifyArtifactDirectory(
			filepath.Join(restoredArtifacts, artifact.Digest),
			artifact.Digest,
		)
		if err != nil || verified.sizeBytes != artifact.SizeBytes {
			t.Fatalf("restored artifact %s = %+v, %v", artifact.Digest, verified, err)
		}
	}
}

func TestFullBundleRoundTripPreservesOriginalModelUnknownAndKnownZeroUsage(
	t *testing.T,
) {
	fixture := newBackupFixture(t)
	transitionPendingAttemptToUnknown(t, fixture)

	bundle := filepath.Join(t.TempDir(), "unknown.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	if manifest.AttemptCounts.ModelPending != 0 ||
		manifest.AttemptCounts.ModelUnknown != 1 ||
		manifest.AttemptCounts.ActionPending != 0 ||
		manifest.AttemptCounts.ActionUnknown != 0 {
		t.Fatalf("manifest AttemptCounts = %+v", manifest.AttemptCounts)
	}
	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest ||
		verified.AttemptCounts != manifest.AttemptCounts {
		t.Fatalf("verified manifest = %+v, want %+v", verified, manifest)
	}
	sourceState := loadBackupStateSnapshot(t, fixture.databasePath)
	assertSuccessfulAndUnknownState(t, sourceState, fixture)

	restoreParent := t.TempDir()
	restoredDatabase := filepath.Join(restoreParent, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreParent, "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}
	sourceVerification, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	restoredVerification, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restoredVerification.StoreInstanceID != sourceVerification.StoreInstanceID {
		t.Fatalf(
			"restored store_instance_id = %s, want %s",
			restoredVerification.StoreInstanceID,
			sourceVerification.StoreInstanceID,
		)
	}

	restoredState := loadBackupStateSnapshot(t, restoredDatabase)
	if !reflect.DeepEqual(restoredState, sourceState) {
		t.Fatalf(
			"restored authoritative state differs:\nsource=%#v\nrestored=%#v",
			sourceState,
			restoredState,
		)
	}
	assertSuccessfulAndUnknownState(t, restoredState, fixture)
	assertRestoredUnknownTypedState(t, restoredDatabase, fixture)
}

func TestVerifyBundleRejectsTamperedDatabaseArtifactAndManifest(t *testing.T) {
	fixture := newBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-test/v1",
	); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		tamper func(*testing.T, string)
	}{
		{
			name: "database",
			tamper: func(t *testing.T, copied string) {
				flipLastByte(t, filepath.Join(copied, databaseName))
			},
		},
		{
			name: "artifact",
			tamper: func(t *testing.T, copied string) {
				path := firstArtifactOrdinaryFile(t, copied)
				flipLastByte(t, path)
			},
		},
		{
			name: "manifest",
			tamper: func(t *testing.T, copied string) {
				flipLastByte(t, filepath.Join(copied, manifestName))
			},
		},
		{
			name: "extra path",
			tamper: func(t *testing.T, copied string) {
				if err := os.WriteFile(
					filepath.Join(copied, "unexpected"),
					[]byte("not in manifest"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hard link",
			tamper: func(t *testing.T, copied string) {
				source := firstArtifactOrdinaryFile(t, copied)
				if err := os.Link(source, source+".hardlink"); err != nil {
					t.Skipf("hard links are unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copied := filepath.Join(t.TempDir(), "tampered.bundle")
			copyTestTree(t, bundle, copied)
			test.tamper(t, copied)
			if _, err := VerifyBundle(context.Background(), copied); err == nil {
				t.Fatal("VerifyBundle() accepted tampered bundle")
			}
		})
	}
}

func TestFullBackupRejectsTamperedAgentMemorySemanticClosure(t *testing.T) {
	fixture := newBackupFixture(t)
	tests := []struct {
		name   string
		tamper func(*testing.T, *moduleapi.AgentMemorySnapshotV1)
	}{
		{
			name: "parent",
			tamper: func(_ *testing.T, snapshot *moduleapi.AgentMemorySnapshotV1) {
				snapshot.PreviousSnapshotDigest = strings.Repeat("f", 64)
			},
		},
		{
			name: "owner",
			tamper: func(_ *testing.T, snapshot *moduleapi.AgentMemorySnapshotV1) {
				snapshot.AgentID = "tampered-agent"
			},
		},
		{
			name: "source",
			tamper: func(_ *testing.T, snapshot *moduleapi.AgentMemorySnapshotV1) {
				snapshot.SourceAttemptID = fixture.pendingAttemptID
			},
		},
		{
			name: "count",
			tamper: func(t *testing.T, snapshot *moduleapi.AgentMemorySnapshotV1) {
				for index := range snapshot.Entries {
					if snapshot.Entries[index].Kind == moduleapi.MemoryEntryCategoryCount {
						snapshot.Entries[index].Count = 0
						return
					}
				}
				t.Fatal("fixture has no CATEGORY_COUNT entry")
			},
		},
		{
			name: "summary",
			tamper: func(t *testing.T, snapshot *moduleapi.AgentMemorySnapshotV1) {
				for index := range snapshot.Entries {
					if snapshot.Entries[index].Kind == moduleapi.MemoryEntryTaskSummary {
						snapshot.Entries[index].Text += " Tampered."
						return
					}
				}
				t.Fatal("fixture has no TASK_SUMMARY entry")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copiedDatabase := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.databasePath, copiedDatabase)
			rewriteBackupMemoryHead(
				t,
				copiedDatabase,
				fixture.memoryCurrentRef,
				func(snapshot *moduleapi.AgentMemorySnapshotV1) {
					test.tamper(t, snapshot)
				},
			)
			if _, err := CreateBundle(
				context.Background(),
				copiedDatabase,
				fixture.artifactRoot,
				filepath.Join(t.TempDir(), "rejected.bundle"),
				"currentbackup-test/v1",
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle() error=%v, want ErrIntegrity", err)
			}
		})
	}
}

func TestFullBackupRejectsDiscontinuousAgentMemoryChain(t *testing.T) {
	fixture := newBackupFixture(t)
	copiedDatabase := filepath.Join(t.TempDir(), "missing-genesis.sqlite")
	copyTestFile(t, fixture.databasePath, copiedDatabase)
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(
			copiedDatabase,
			"rw",
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
			"journal_mode(DELETE)",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.Exec(`
		DELETE FROM agent_memory_revisions
		WHERE tenant_id=? AND agent_id=? AND revision=1
	`, fixture.memoryCurrentRef.TenantID, fixture.memoryCurrentRef.AgentID)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		_ = database.Close()
		t.Fatalf("delete Memory genesis affected=%d error=%v", affected, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := consolidateSQLiteSnapshot(context.Background(), copiedDatabase); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateBundle(
		context.Background(),
		copiedDatabase,
		fixture.artifactRoot,
		filepath.Join(t.TempDir(), "rejected.bundle"),
		"currentbackup-test/v1",
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("CreateBundle() error=%v, want ErrIntegrity", err)
	}
}

func TestNoOverwriteAndCreateFailureLeaveNoFormalTarget(t *testing.T) {
	fixture := newBackupFixture(t)
	bundleParent := t.TempDir()
	bundle := filepath.Join(bundleParent, "full.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-test/v1",
	); err != nil {
		t.Fatal(err)
	}
	existingDatabase := filepath.Join(t.TempDir(), "existing.sqlite")
	if err := os.WriteFile(existingDatabase, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactTarget := filepath.Join(t.TempDir(), "new-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		existingDatabase,
		artifactTarget,
	); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("RestoreBundle(existing DB) error = %v", err)
	}
	content, err := os.ReadFile(existingDatabase)
	if err != nil || string(content) != "do not replace" {
		t.Fatalf("existing database changed: %q, %v", content, err)
	}
	if _, err := os.Lstat(artifactTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact target exists after rejection: %v", err)
	}
	existingArtifactTarget := filepath.Join(t.TempDir(), "existing-artifacts")
	if err := os.Mkdir(existingArtifactTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existingArtifactTarget, "marker")
	if err := os.WriteFile(marker, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	newDatabaseTarget := filepath.Join(t.TempDir(), "new.sqlite")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		newDatabaseTarget,
		existingArtifactTarget,
	); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("RestoreBundle(existing artifact root) error = %v", err)
	}
	if _, err := os.Lstat(newDatabaseTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database target exists after artifact rejection: %v", err)
	}
	markerContent, err := os.ReadFile(marker)
	if err != nil || string(markerContent) != "do not replace" {
		t.Fatalf("existing artifact root changed: %q, %v", markerContent, err)
	}

	missingRoot := t.TempDir()
	failedDestination := filepath.Join(bundleParent, "must-not-exist.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		missingRoot,
		failedDestination,
		"currentbackup-test/v1",
	); err == nil {
		t.Fatal("CreateBundle() unexpectedly succeeded without referenced artifacts")
	}
	if _, err := os.Lstat(failedDestination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed bundle destination exists: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(bundleParent, ".must-not-exist.bundle.create-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("failed CreateBundle left staging paths: %v, %v", matches, err)
	}
}

func TestCreateBundleRejectsActiveStoreOwner(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "active.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "must-not-exist.bundle")
	if _, err := CreateBundle(
		ctx,
		databasePath,
		artifactRoot,
		destination,
		"currentbackup-test/v1",
	); !errors.Is(err, ErrSourceActive) {
		t.Fatalf("CreateBundle(active Store) error = %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active-source destination exists: %v", err)
	}
}

func TestEmptyStoreBundleUsesExplicitEmptyLists(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "empty.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "empty.bundle")
	manifest, err := CreateBundle(
		ctx,
		databasePath,
		artifactRoot,
		bundle,
		"currentbackup-test/v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ArtifactCount != 0 || manifest.Artifacts == nil || manifest.Current == nil {
		t.Fatalf("empty manifest lists = %+v", manifest)
	}
	canonical, err := os.ReadFile(filepath.Join(bundle, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"artifacts":[]`)) ||
		!bytes.Contains(canonical, []byte(`"current":[]`)) {
		t.Fatalf("manifest does not encode explicit empty lists: %s", canonical)
	}
}

func TestManifestRejectsUnsafeArtifactPaths(t *testing.T) {
	digest := strings.Repeat("a", 64)
	base := Manifest{
		FormatVersion: FormatVersionV1,
		CreatedAt:     "2026-08-01T00:00:00Z",
		ToolVersion:   "currentbackup-test/v1",
		Database: DatabaseFile{
			Path:      databaseName,
			SHA256:    strings.Repeat("b", 64),
			SizeBytes: 1,
		},
		StoreIdentity: StoreIdentity{
			ApplicationID:     currentstore.ApplicationID,
			UserVersion:       currentstore.UserVersion,
			SchemaIdentity:    currentstore.SchemaIdentity,
			SchemaFingerprint: currentstore.ExpectedSchemaFingerprint,
			GeneratorID:       currentstore.GeneratorID,
			StoreInstanceID:   "store-test",
		},
		Current:       []CurrentPublication{},
		ArtifactCount: 1,
	}
	windowsAbsolute := strings.Join(
		[]string{"C", "/artifacts/" + digest},
		":",
	)
	for _, path := range []string{
		"../" + digest,
		"/absolute/" + digest,
		windowsAbsolute,
		"ARTIFACTS/" + digest,
	} {
		t.Run(path, func(t *testing.T) {
			candidate := base
			candidate.Artifacts = []Artifact{{
				Path:      path,
				Digest:    digest,
				SizeBytes: 1,
			}}
			if _, _, err := freezeManifest(candidate); err == nil {
				t.Fatalf("freezeManifest() accepted unsafe path %q", path)
			}
		})
	}
}

func newBackupFixture(t *testing.T) backupFixture {
	t.Helper()
	ctx := context.Background()
	seedPath := exampleSeedPath(t)
	prepared := prepareProfiledExampleSeed(t, seedPath)
	memoryPrepared, err := bootstrapseed.PrepareFile(memoryExampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() Memory example error = %v", err)
	}
	model := prepared.ModelAssertion()
	modelProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           model.ModuleID,
		Version:            model.ExactVersion,
		ArtifactDigest:     model.ArtifactDigest,
		InstanceID:         model.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    model.ExpectedAdapterIdentity,
		ActivationRevision: model.ActivationRevision,
	}
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	var memory bootstrapseed.ModuleAssertion
	for _, assertion := range memoryPrepared.ModuleAssertions() {
		if assertion.ExpectedAdapterIdentity ==
			"freeagent.adapter.memory.deterministic/v1" {
			memory = assertion
			break
		}
	}
	if memory.ArtifactDigest == "" {
		t.Fatal("Memory example has no deterministic Memory assertion")
	}
	memoryProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           memory.ModuleID,
		Version:            memory.ExactVersion,
		ArtifactDigest:     memory.ArtifactDigest,
		InstanceID:         memory.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    memory.ExpectedAdapterIdentity,
		ActivationRevision: memory.ActivationRevision,
	}
	memoryAdapter, err := exactadapter.NewDeterministicMemory(memoryProvider)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  model.ArtifactDigest,
			AdapterIdentity: model.ExpectedAdapterIdentity,
			Invoker:         echo,
		},
		exactadapter.Registration{
			ArtifactDigest:  memory.ArtifactDigest,
			AdapterIdentity: memory.ExpectedAdapterIdentity,
			Invoker:         memoryAdapter,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist: []activationresolver.TrustedInProcessAllowlistEntry{
				{
					ModuleID:        model.ModuleID,
					ExactVersion:    model.ExactVersion,
					ArtifactDigest:  model.ArtifactDigest,
					AdapterIdentity: model.ExpectedAdapterIdentity,
				},
				{
					ModuleID:        memory.ModuleID,
					ExactVersion:    memory.ExactVersion,
					ArtifactDigest:  memory.ArtifactDigest,
					AdapterIdentity: memory.ExpectedAdapterIdentity,
				},
			},
		},
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	if _, err := prepared.Import(ctx, store, resolver); err != nil {
		t.Fatalf("profiled Pure Chat seed Import() error = %v", err)
	}
	if _, err := memoryPrepared.Import(ctx, store, resolver); err != nil {
		t.Fatalf("Memory seed Import() error = %v", err)
	}
	assembly := prepared.DefaultAssembly()
	memoryAssembly := memoryPrepared.DefaultAssembly()
	realLoop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	realService, err := localchat.NewChatService(store, realLoop)
	if err != nil {
		t.Fatal(err)
	}
	successDeadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	successfulChat, err := realService.Chat(ctx, localchat.ChatInput{
		TenantID:    memoryAssembly.TenantID,
		PrincipalID: "backup-test-principal",
		WorkspaceID: memoryAssembly.WorkspaceID,
		AgentID:     memoryAssembly.AgentID,
		ProfileID:   memoryAssembly.ProfileID,
		Message:     "preserve this successful programming history",
		RequestID:   "backup-success-request",
		Deadline:    successDeadline,
	})
	if err != nil {
		t.Fatalf("successful Chat() error = %v", err)
	}
	if successfulChat.Reply != "preserve this successful programming history" ||
		successfulChat.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("successful Chat() = %+v", successfulChat)
	}
	if successfulChat.TerminalResult == nil ||
		successfulChat.TerminalResult.AttemptID == "" {
		t.Fatalf("successful Chat() has no source Attempt: %+v", successfulChat)
	}
	memoryGenesis, err := store.GetAgentMemoryRevision(
		ctx,
		memoryAssembly.TenantID,
		memoryAssembly.AgentID,
		1,
	)
	if err != nil {
		t.Fatalf("GetAgentMemoryRevision(1) = %+v, %v", memoryGenesis, err)
	}
	memoryCurrent, err := store.GetCurrentAgentMemory(
		ctx,
		memoryAssembly.TenantID,
		memoryAssembly.AgentID,
	)
	if err != nil || memoryCurrent.SnapshotRef.Revision != 2 ||
		memoryCurrent.Snapshot.PreviousSnapshotDigest !=
			memoryGenesis.SnapshotRef.Digest ||
		memoryCurrent.SourceAttemptID != successfulChat.TerminalResult.AttemptID {
		t.Fatalf("successful terminal Memory revision = %+v, %v", memoryCurrent, err)
	}

	yieldService, err := localchat.NewChatService(store, yieldLoop{})
	if err != nil {
		t.Fatal(err)
	}
	pendingDeadline := successDeadline.Add(time.Hour)
	pendingChat, err := yieldService.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "backup-test-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "preserve this pending attempt",
		RequestID:   "backup-test-request",
		Deadline:    pendingDeadline,
	})
	if err != nil {
		t.Fatalf("pending Chat() error = %v", err)
	}
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 pendingChat.RunID,
		OwnerID:               "backup-test-loop",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: 0,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("AcquireRunLease() error = %v", err)
	}
	_, request, err := moduleapi.NewModelGenerateRequestV1(moduleapi.ModelGenerateRequestV1{
		SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
		Messages: []moduleapi.ModelMessageV1{{
			Role:    moduleapi.ModelRoleUser,
			Content: "pending backup test",
		}},
		Parameters: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	contextCompilationCanonical,
		contextCompilationDigest,
		requestDigest := newPendingContextCompilation(
		t,
		store,
		lease,
		request,
	)
	begin, err := store.BeginModelDispatch(ctx, currentstore.BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   "backup-test-attempt",
		LogicalStepID:               "backup-test-step",
		ContextCompilationCanonical: contextCompilationCanonical,
		RequestCanonical:            request,
		Deadline:                    pendingDeadline.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("BeginModelDispatch() error = %v", err)
	}
	profileRun, err := store.LoadRunForLoop(ctx, begin.Lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop() profiled closure error = %v", err)
	}
	if profileRun.Member.ModelProfile == nil {
		t.Fatal("profiled backup fixture lost ModelProfileRef")
	}
	modelProfileRef := *profileRun.Member.ModelProfile
	modelProfileContent, found := profileRun.FindContent(modelProfileRef.Digest)
	if !found || modelProfileContent.Kind != currentstore.ContentConfig {
		t.Fatalf(
			"profiled backup fixture content = %+v, found=%v",
			modelProfileContent,
			found,
		)
	}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	seenArtifacts := make(map[string]struct{})
	allAssertions := append(
		append([]bootstrapseed.ModuleAssertion(nil), prepared.ModuleAssertions()...),
		memoryPrepared.ModuleAssertions()...,
	)
	for _, assertion := range allAssertions {
		if _, found := seenArtifacts[assertion.ArtifactDigest]; found {
			continue
		}
		seenArtifacts[assertion.ArtifactDigest] = struct{}{}
		verified, err := verifyArtifactDirectory(
			assertion.ArtifactDirectory,
			assertion.ArtifactDigest,
		)
		if err != nil {
			t.Fatalf("verify seed artifact %s: %v", assertion.ArtifactDigest, err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf("copy seed artifact %s: %v", assertion.ArtifactDigest, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return backupFixture{
		databasePath:                       databasePath,
		artifactRoot:                       artifactRoot,
		successfulRunID:                    successfulChat.RunID,
		pendingRunID:                       pendingChat.RunID,
		pendingAttemptID:                   begin.Attempt.AttemptID,
		pendingAttemptRevision:             begin.Attempt.Revision,
		pendingLease:                       begin.Lease,
		pendingProvider:                    begin.Attempt.Binding.Provider,
		pendingContextCompilationCanonical: bytes.Clone(contextCompilationCanonical),
		pendingContextCompilationDigest:    contextCompilationDigest,
		pendingRequestCanonical:            bytes.Clone(request),
		pendingRequestDigest:               requestDigest,
		modelProfileRef:                    modelProfileRef,
		modelProfileCanonical: bytes.Clone(
			modelProfileContent.CanonicalBytes,
		),
		memoryGenesisCanonical: bytes.Clone(memoryGenesis.CanonicalBytes),
		memoryCurrentCanonical: bytes.Clone(memoryCurrent.CanonicalBytes),
		memoryCurrentRef:       memoryCurrent.SnapshotRef,
	}
}

func prepareProfiledExampleSeed(
	t *testing.T,
	seedPath string,
) *bootstrapseed.Prepared {
	t.Helper()
	seedBytes, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	if err := json.Unmarshal(seedBytes, &seed); err != nil {
		t.Fatal(err)
	}
	// Keep the profiled Pure Chat and Memory fixtures in separate tenants so
	// one backup can prove both a PENDING pure run and a real Memory-enabled
	// successful terminal without replacing either tenant's current basis.
	seed["tenant_id"] = "backup-pure"
	seed["seed_id"] = "freeagent.backup.pure-chat"
	seed["catalog"].(map[string]any)["generation_id"] =
		"catalog-backup-pure-chat"
	seed["control"].(map[string]any)["snapshot_id"] =
		"control-backup-pure-chat"
	modelBinding := seed["model_binding"].(map[string]any)
	modelConfig := modelBinding["config"].(map[string]any)
	configEncoded, err := json.Marshal(modelConfig)
	if err != nil {
		t.Fatal(err)
	}
	configCanonical, err := moduleapi.CanonicalJSON(configEncoded)
	if err != nil {
		t.Fatal(err)
	}
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		"application/json",
		configCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	module := seed["module"].(map[string]any)
	profile, _, profileCanonical, err := corecontract.NewModelProfileV1(
		corecontract.ModelProfileV1{
			SchemaVersion:          corecontract.ModelProfileSchemaVersionV1,
			ID:                     "backup-profile-echo",
			Version:                "1",
			Provider:               modelConfig["provider"].(string),
			Model:                  modelConfig["model"].(string),
			ModelBuildID:           modelConfig["model_build_id"].(string),
			ModelConfigRef:         configRef,
			AdapterArtifactDigest:  module["artifact_digest"].(string),
			AdapterIdentity:        module["expected_adapter_identity"].(string),
			ContextWindowTokens:    32768,
			EvaluationSuite:        "backup-evaluation-suite",
			EvaluationVersion:      "1",
			EvaluationResultDigest: strings.Repeat("e", 64),
			CapabilityTendencies: []corecontract.ModelTendencyV1{{
				MetricID: "general", ScoreBasisPoints: 8000,
			}},
			ReliabilityTendencies: []corecontract.ModelTendencyV1{{
				MetricID: "determinism", ScoreBasisPoints: 10000,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ModelBuildID == "" {
		t.Fatal("profiled seed has no exact model build")
	}
	var profileValue any
	if err := json.Unmarshal(profileCanonical, &profileValue); err != nil {
		t.Fatal(err)
	}
	seed["model_profile"] = profileValue
	encoded, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(seedPath))
	if err != nil {
		t.Fatalf("Prepare() profiled example error = %v", err)
	}
	return prepared
}

func assertRestoredModelProfileClosure(
	t *testing.T,
	databasePath string,
	fixture backupFixture,
) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, err := store.LoadRunForLoop(context.Background(), fixture.pendingLease)
	if err != nil {
		t.Fatalf("restored profiled LoadRunForLoop() error = %v", err)
	}
	if run.Member.ModelProfile == nil ||
		*run.Member.ModelProfile != fixture.modelProfileRef {
		t.Fatalf(
			"restored ModelProfileRef = %+v, want %+v",
			run.Member.ModelProfile,
			fixture.modelProfileRef,
		)
	}
	content, found := run.FindContent(fixture.modelProfileRef.Digest)
	if !found || content.Kind != currentstore.ContentConfig ||
		!bytes.Equal(content.CanonicalBytes, fixture.modelProfileCanonical) {
		t.Fatalf("restored ModelProfile CONFIG = %+v, found=%v", content, found)
	}
	if _, err := corecontract.RestoreModelProfileV1(
		content.CanonicalBytes,
		fixture.modelProfileRef,
	); err != nil {
		t.Fatalf("restored model-profile/v1 error = %v", err)
	}
}

func exampleSeedPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source),
		"..",
		"..",
		"examples",
		"current-v1.bootstrap.seed.json",
	))
}

func memoryExampleSeedPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source),
		"..",
		"..",
		"examples",
		"current-v1.memory.bootstrap.seed.json",
	))
}

func assertPendingNullUsage(t *testing.T, databasePath string) {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var state string
	var ledger, input, cached, uncached, output, reasoning sql.NullInt64
	var estimatedCost, providerCost, reconciledCost sql.NullString
	if err := database.QueryRow(`
		SELECT
			attempt.state,
			usage.ledger_sequence,
			usage.input_tokens,
			usage.cached_input_tokens,
			usage.uncached_input_tokens,
			usage.output_tokens,
			usage.reasoning_tokens,
			usage.estimated_cost,
			usage.provider_reported_cost,
			usage.reconciled_cost
		FROM model_dispatch_attempts AS attempt
		JOIN model_usage AS usage ON usage.attempt_id=attempt.attempt_id
		WHERE attempt.attempt_id='backup-test-attempt'
	`).Scan(
		&state,
		&ledger,
		&input,
		&cached,
		&uncached,
		&output,
		&reasoning,
		&estimatedCost,
		&providerCost,
		&reconciledCost,
	); err != nil {
		t.Fatal(err)
	}
	if state != "PENDING" || ledger.Valid || input.Valid || cached.Valid ||
		uncached.Valid || output.Valid || reasoning.Valid || estimatedCost.Valid ||
		providerCost.Valid || reconciledCost.Valid {
		t.Fatalf(
			"state/usage = %s %+v %+v %+v %+v %+v %+v %+v %+v %+v",
			state,
			ledger,
			input,
			cached,
			uncached,
			output,
			reasoning,
			estimatedCost,
			providerCost,
			reconciledCost,
		)
	}
}

func transitionPendingAttemptToUnknown(t *testing.T, fixture backupFixture) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	zero := uint64(0)
	zeroCost := "0"
	_, usageCanonical, err := moduleapi.NewModelUsageReceiptV1(
		moduleapi.ModelUsageReceiptV1{
			SchemaVersion:        moduleapi.ModelUsageReceiptSchemaV1,
			InputTokens:          &zero,
			CachedInputTokens:    &zero,
			UncachedInputTokens:  &zero,
			OutputTokens:         &zero,
			ReasoningTokens:      &zero,
			ProviderReportedCost: &zeroCost,
			RawReceipt:           []byte(`{"provider":"backup-test","status":"unknown"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := store.CommitModelDispatchOutcome(
		ctx,
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   fixture.pendingLease,
			AttemptID:               fixture.pendingAttemptID,
			InvocationID:            fixture.pendingAttemptID,
			Provider:                fixture.pendingProvider,
			ExpectedAttemptRevision: fixture.pendingAttemptRevision,
			State:                   corecontract.ModelAttemptUnknown,
			UsageReceiptCanonical:   usageCanonical,
			ProviderRequestID:       "backup-test-provider-request",
			UnknownReason: string(
				modulehost.UnknownClassNoUsableResponse,
			),
		},
	)
	if err != nil {
		t.Fatalf("CommitModelDispatchOutcome(MODEL_UNKNOWN) error = %v", err)
	}
	if !unknown.Applied ||
		unknown.Record.Attempt.AttemptID != fixture.pendingAttemptID ||
		unknown.Record.Attempt.State != corecontract.ModelAttemptUnknown ||
		unknown.Record.Attempt.UnknownReason != string(
			modulehost.UnknownClassNoUsableResponse,
		) ||
		unknown.Record.Attempt.Revision != fixture.pendingAttemptRevision+1 {
		t.Fatalf("unknown outcome = %+v", unknown)
	}
	unsettled, err := store.ScanUnsettledModelDispatchRecords(
		ctx,
		fixture.pendingRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsettled) != 1 ||
		unsettled[0].Attempt.AttemptID != fixture.pendingAttemptID ||
		unsettled[0].Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("unsettled after MODEL_UNKNOWN = %+v", unsettled)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
}

func loadBackupStateSnapshot(t *testing.T, databasePath string) backupStateSnapshot {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	state := backupStateSnapshot{
		Runs:     []backupRunReference{},
		Attempts: []backupAttemptState{},
		Usage:    []backupUsageState{},
		History:  []backupHistoryState{},
		Events:   []backupEventState{},
		Memory:   []backupMemoryState{},
	}
	runRows, err := database.Query(`
		SELECT
			run.run_id,
			member.member_id,
			manifest.digest,
			member.digest
		FROM runs AS run
		JOIN run_manifests AS manifest ON manifest.run_id=run.run_id
		JOIN member_execution_snapshots AS member ON member.run_id=run.run_id
		ORDER BY run.run_id, member.member_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for runRows.Next() {
		var row backupRunReference
		if err := runRows.Scan(
			&row.RunID,
			&row.MemberID,
			&row.ManifestDigest,
			&row.MemberDigest,
		); err != nil {
			_ = runRows.Close()
			t.Fatal(err)
		}
		state.Runs = append(state.Runs, row)
	}
	closeSnapshotRows(t, runRows)

	attemptRows, err := database.Query(`
		SELECT
			attempt_id,
			run_id,
			logical_step_id,
			state,
			request_ref,
			request_digest,
			result_ref,
			provider_receipt_ref,
			reconciliation_evidence_ref,
			revision
		FROM model_dispatch_attempts
		ORDER BY run_id, logical_step_id, attempt_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for attemptRows.Next() {
		var row backupAttemptState
		if err := attemptRows.Scan(
			&row.AttemptID,
			&row.RunID,
			&row.LogicalStepID,
			&row.State,
			&row.RequestRef,
			&row.RequestDigest,
			&row.ResultRef,
			&row.ProviderRef,
			&row.EvidenceRef,
			&row.Revision,
		); err != nil {
			_ = attemptRows.Close()
			t.Fatal(err)
		}
		state.Attempts = append(state.Attempts, row)
	}
	closeSnapshotRows(t, attemptRows)

	usageRows, err := database.Query(`
		SELECT
			attempt_id,
			ledger_sequence,
			revision,
			input_tokens,
			cached_input_tokens,
			uncached_input_tokens,
			output_tokens,
			reasoning_tokens,
			estimated_cost,
			provider_reported_cost,
			reconciled_cost,
			reconciliation_status,
			raw_receipt_ref
		FROM model_usage
		ORDER BY attempt_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for usageRows.Next() {
		var row backupUsageState
		if err := usageRows.Scan(
			&row.AttemptID,
			&row.LedgerSequence,
			&row.Revision,
			&row.InputTokens,
			&row.CachedInputTokens,
			&row.UncachedInputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.EstimatedCost,
			&row.ProviderReportedCost,
			&row.ReconciledCost,
			&row.ReconciliationStatus,
			&row.RawReceiptRef,
		); err != nil {
			_ = usageRows.Close()
			t.Fatal(err)
		}
		state.Usage = append(state.Usage, row)
	}
	closeSnapshotRows(t, usageRows)

	historyRows, err := database.Query(`
		SELECT
			run_id,
			history_sequence,
			member_id,
			role,
			content_ref,
			content_digest,
			source_attempt_id
		FROM history_entries
		ORDER BY run_id, history_sequence
	`)
	if err != nil {
		t.Fatal(err)
	}
	for historyRows.Next() {
		var row backupHistoryState
		if err := historyRows.Scan(
			&row.RunID,
			&row.Sequence,
			&row.MemberID,
			&row.Role,
			&row.ContentRef,
			&row.ContentDigest,
			&row.SourceAttemptID,
		); err != nil {
			_ = historyRows.Close()
			t.Fatal(err)
		}
		state.History = append(state.History, row)
	}
	closeSnapshotRows(t, historyRows)

	eventRows, err := database.Query(`
		SELECT
			run_id,
			event_sequence,
			event_kind,
			from_revision,
			to_revision,
			payload_ref,
			payload_digest
		FROM run_events
		ORDER BY run_id, event_sequence
	`)
	if err != nil {
		t.Fatal(err)
	}
	for eventRows.Next() {
		var row backupEventState
		if err := eventRows.Scan(
			&row.RunID,
			&row.Sequence,
			&row.Kind,
			&row.FromRevision,
			&row.ToRevision,
			&row.PayloadRef,
			&row.PayloadDigest,
		); err != nil {
			_ = eventRows.Close()
			t.Fatal(err)
		}
		state.Events = append(state.Events, row)
	}
	closeSnapshotRows(t, eventRows)

	memoryRows, err := database.Query(`
		SELECT
			memory.tenant_id,
			memory.agent_id,
			memory.revision,
			memory.snapshot_ref,
			memory.source_attempt_id,
			content.canonical_bytes
		FROM agent_memory_revisions AS memory
		JOIN content_records AS content
		  ON content.content_digest=memory.snapshot_ref
		ORDER BY memory.tenant_id, memory.agent_id, memory.revision
	`)
	if err != nil {
		t.Fatal(err)
	}
	for memoryRows.Next() {
		var (
			row      backupMemoryState
			revision int64
		)
		if err := memoryRows.Scan(
			&row.TenantID,
			&row.AgentID,
			&revision,
			&row.SnapshotRef,
			&row.SourceAttempt,
			&row.CanonicalBytes,
		); err != nil {
			_ = memoryRows.Close()
			t.Fatal(err)
		}
		if revision < 1 {
			_ = memoryRows.Close()
			t.Fatalf("invalid test Memory revision %d", revision)
		}
		row.Revision = uint64(revision)
		row.CanonicalBytes = bytes.Clone(row.CanonicalBytes)
		state.Memory = append(state.Memory, row)
	}
	closeSnapshotRows(t, memoryRows)
	return state
}

func closeSnapshotRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertSuccessfulAndPendingState(
	t *testing.T,
	state backupStateSnapshot,
	fixture backupFixture,
) {
	t.Helper()
	assertCommonSuccessfulState(t, state, fixture)
	pending := exactAttemptForRun(t, state.Attempts, fixture.pendingRunID)
	if pending.AttemptID != fixture.pendingAttemptID ||
		pending.State != string(corecontract.ModelAttemptPending) ||
		pending.ResultRef.Valid || pending.ProviderRef.Valid ||
		pending.EvidenceRef.Valid {
		t.Fatalf("pending Attempt = %+v", pending)
	}
	assertCanonicalReference(t, "pending request", pending.RequestRef, pending.RequestDigest)
	usage := exactUsageForAttempt(t, state.Usage, pending.AttemptID)
	if usage.LedgerSequence.Valid || usage.InputTokens.Valid ||
		usage.CachedInputTokens.Valid || usage.UncachedInputTokens.Valid ||
		usage.OutputTokens.Valid || usage.ReasoningTokens.Valid ||
		usage.EstimatedCost.Valid || usage.ProviderReportedCost.Valid ||
		usage.ReconciledCost.Valid || usage.RawReceiptRef.Valid ||
		usage.ReconciliationStatus != "PENDING" {
		t.Fatalf("pending Usage = %+v", usage)
	}
	assertEventSequence(t, state.Events, fixture.pendingRunID, []string{
		corecontract.RunAdmittedEventKind,
		corecontract.ModelDispatchPendingEventKind,
	})
}

func assertSuccessfulAndUnknownState(
	t *testing.T,
	state backupStateSnapshot,
	fixture backupFixture,
) {
	t.Helper()
	assertCommonSuccessfulState(t, state, fixture)
	unknown := exactAttemptForRun(t, state.Attempts, fixture.pendingRunID)
	if unknown.AttemptID != fixture.pendingAttemptID ||
		unknown.LogicalStepID != "backup-test-step" ||
		unknown.State != string(corecontract.ModelAttemptUnknown) ||
		unknown.ResultRef.Valid || !unknown.ProviderRef.Valid ||
		unknown.Revision != int64(fixture.pendingAttemptRevision+1) {
		t.Fatalf("MODEL_UNKNOWN Attempt = %+v", unknown)
	}
	assertCanonicalReference(t, "MODEL_UNKNOWN request", unknown.RequestRef, unknown.RequestDigest)
	usage := exactUsageForAttempt(t, state.Usage, unknown.AttemptID)
	for name, value := range map[string]sql.NullInt64{
		"input":          usage.InputTokens,
		"cached input":   usage.CachedInputTokens,
		"uncached input": usage.UncachedInputTokens,
		"output":         usage.OutputTokens,
		"reasoning":      usage.ReasoningTokens,
	} {
		if !value.Valid || value.Int64 != 0 {
			t.Fatalf("MODEL_UNKNOWN %s tokens = %+v", name, value)
		}
	}
	if !usage.LedgerSequence.Valid ||
		!usage.ProviderReportedCost.Valid ||
		usage.ProviderReportedCost.String != "0" ||
		usage.EstimatedCost.Valid || usage.ReconciledCost.Valid ||
		!usage.RawReceiptRef.Valid ||
		usage.ReconciliationStatus != "PENDING_RECONCILIATION" {
		t.Fatalf("MODEL_UNKNOWN Usage = %+v", usage)
	}
	assertEventSequence(t, state.Events, fixture.pendingRunID, []string{
		corecontract.RunAdmittedEventKind,
		corecontract.ModelDispatchPendingEventKind,
		corecontract.ModelDispatchTerminalEventKind,
	})
}

func assertCommonSuccessfulState(
	t *testing.T,
	state backupStateSnapshot,
	fixture backupFixture,
) {
	t.Helper()
	if len(state.Runs) != 2 || len(state.Attempts) != 2 || len(state.Usage) != 2 {
		t.Fatalf("unexpected Run/Attempt/Usage cardinality: %+v", state)
	}
	for _, run := range state.Runs {
		assertCanonicalReference(t, "Run manifest", run.ManifestDigest, run.ManifestDigest)
		assertCanonicalReference(t, "member snapshot", run.MemberDigest, run.MemberDigest)
	}
	success := exactAttemptForRun(t, state.Attempts, fixture.successfulRunID)
	if success.State != string(corecontract.ModelAttemptSucceeded) ||
		!success.ResultRef.Valid || !success.ProviderRef.Valid ||
		success.EvidenceRef.Valid {
		t.Fatalf("successful Attempt = %+v", success)
	}
	assertCanonicalReference(
		t,
		"successful request",
		success.RequestRef,
		success.RequestDigest,
	)
	assertCanonicalReference(
		t,
		"successful result",
		success.ResultRef.String,
		success.ResultRef.String,
	)
	successUsage := exactUsageForAttempt(t, state.Usage, success.AttemptID)
	if successUsage.LedgerSequence.Valid || successUsage.InputTokens.Valid ||
		successUsage.CachedInputTokens.Valid ||
		successUsage.UncachedInputTokens.Valid ||
		successUsage.OutputTokens.Valid || successUsage.ReasoningTokens.Valid ||
		successUsage.EstimatedCost.Valid ||
		successUsage.ProviderReportedCost.Valid ||
		successUsage.ReconciledCost.Valid || !successUsage.RawReceiptRef.Valid ||
		successUsage.ReconciliationStatus != "PROVIDER_REPORTED" {
		t.Fatalf("successful Echo Usage = %+v", successUsage)
	}
	if len(state.Memory) != 2 {
		t.Fatalf("Agent Memory revisions = %+v", state.Memory)
	}
	genesis := state.Memory[0]
	head := state.Memory[1]
	if genesis.Revision != 1 || genesis.SourceAttempt.Valid ||
		!bytes.Equal(genesis.CanonicalBytes, fixture.memoryGenesisCanonical) {
		t.Fatalf("Agent Memory genesis = %+v", genesis)
	}
	if head.TenantID != fixture.memoryCurrentRef.TenantID ||
		head.AgentID != fixture.memoryCurrentRef.AgentID ||
		head.Revision != fixture.memoryCurrentRef.Revision ||
		head.SnapshotRef != fixture.memoryCurrentRef.Digest ||
		!head.SourceAttempt.Valid ||
		head.SourceAttempt.String != success.AttemptID ||
		!bytes.Equal(head.CanonicalBytes, fixture.memoryCurrentCanonical) {
		t.Fatalf("Agent Memory current MAX ref/body = %+v", head)
	}
	if len(state.History) != 1 {
		t.Fatalf("History = %+v", state.History)
	}
	history := state.History[0]
	if history.RunID != fixture.successfulRunID || history.Sequence != 1 ||
		history.Role != string(moduleapi.ModelRoleAssistant) ||
		!history.SourceAttemptID.Valid ||
		history.SourceAttemptID.String != success.AttemptID {
		t.Fatalf("successful History = %+v", history)
	}
	assertCanonicalReference(
		t,
		"History content",
		history.ContentRef,
		history.ContentDigest,
	)
	assertEventSequence(t, state.Events, fixture.successfulRunID, []string{
		corecontract.RunAdmittedEventKind,
		corecontract.ModelDispatchPendingEventKind,
		corecontract.ModelDispatchTerminalEventKind,
	})
}

func assertEventSequence(
	t *testing.T,
	events []backupEventState,
	runID string,
	wantKinds []string,
) {
	t.Helper()
	selected := make([]backupEventState, 0, len(wantKinds))
	for _, event := range events {
		if event.RunID == runID {
			selected = append(selected, event)
		}
	}
	if len(selected) != len(wantKinds) {
		t.Fatalf("Run %s events = %+v, want kinds %v", runID, selected, wantKinds)
	}
	for index, event := range selected {
		if event.Sequence != int64(index) || event.Kind != wantKinds[index] {
			t.Fatalf("Run %s event[%d] = %+v, want %q", runID, index, event, wantKinds[index])
		}
		assertCanonicalReference(t, "RunEvent payload", event.PayloadRef, event.PayloadDigest)
	}
}

func assertCanonicalReference(t *testing.T, name, reference, digest string) {
	t.Helper()
	if reference != digest || !moduleapi.ValidSHA256(reference) {
		t.Fatalf("%s ref/digest = %q/%q", name, reference, digest)
	}
}

func exactAttemptForRun(
	t *testing.T,
	attempts []backupAttemptState,
	runID string,
) backupAttemptState {
	t.Helper()
	var selected []backupAttemptState
	for _, attempt := range attempts {
		if attempt.RunID == runID {
			selected = append(selected, attempt)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("Run %s Attempts = %+v", runID, selected)
	}
	return selected[0]
}

func exactUsageForAttempt(
	t *testing.T,
	usage []backupUsageState,
	attemptID string,
) backupUsageState {
	t.Helper()
	var selected []backupUsageState
	for _, record := range usage {
		if record.AttemptID == attemptID {
			selected = append(selected, record)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("Attempt %s Usage = %+v", attemptID, selected)
	}
	return selected[0]
}

func assertRestoredUnknownTypedState(
	t *testing.T,
	databasePath string,
	fixture backupFixture,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.GetModelDispatchRecord(ctx, fixture.pendingAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.AttemptID != fixture.pendingAttemptID ||
		record.Attempt.State != corecontract.ModelAttemptUnknown ||
		record.Attempt.UnknownReason != string(
			modulehost.UnknownClassNoUsableResponse,
		) {
		t.Fatalf("restored MODEL_UNKNOWN = %+v", record)
	}
	for name, value := range map[string]*uint64{
		"input":          record.Usage.Tokens.Input,
		"cached input":   record.Usage.Tokens.CachedInput,
		"uncached input": record.Usage.Tokens.UncachedInput,
		"output":         record.Usage.Tokens.Output,
		"reasoning":      record.Usage.Tokens.Reasoning,
	} {
		if value == nil || *value != 0 {
			t.Fatalf("restored MODEL_UNKNOWN %s tokens = %v", name, value)
		}
	}
	if record.Usage.ProviderReportedCost == nil ||
		*record.Usage.ProviderReportedCost != "0" ||
		record.Usage.EstimatedCost != nil || record.Usage.ReconciledCost != nil ||
		record.Usage.ReconciliationStatus != "PENDING_RECONCILIATION" {
		t.Fatalf("restored MODEL_UNKNOWN Usage = %+v", record.Usage)
	}
	unsettled, err := store.ScanUnsettledModelDispatchRecords(ctx, fixture.pendingRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsettled) != 1 ||
		unsettled[0].Attempt.AttemptID != fixture.pendingAttemptID ||
		unsettled[0].Attempt.State != corecontract.ModelAttemptUnknown ||
		unsettled[0].Attempt.UnknownReason != string(
			modulehost.UnknownClassNoUsableResponse,
		) {
		t.Fatalf("restored unsettled Attempts = %+v", unsettled)
	}
}

func copyTestTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func copyTestFile(t *testing.T, source string, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rewriteBackupMemoryHead(
	t *testing.T,
	databasePath string,
	head moduleapi.MemorySnapshotRefV1,
	mutate func(*moduleapi.AgentMemorySnapshotV1),
) {
	t.Helper()
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(
			databasePath,
			"rw",
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
			"journal_mode(DELETE)",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = database.Close()
		}
	}()
	var (
		canonical []byte
		createdAt int64
	)
	if err := database.QueryRow(`
		SELECT content.canonical_bytes, content.created_at
		FROM agent_memory_revisions AS memory
		JOIN content_records AS content
		  ON content.content_digest=memory.snapshot_ref
		WHERE memory.tenant_id=? AND memory.agent_id=? AND memory.revision=?
	`, head.TenantID, head.AgentID, head.Revision).Scan(
		&canonical,
		&createdAt,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&snapshot)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentMemorySnapshot,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	if _, err := transaction.Exec(`
		INSERT INTO content_records(
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		) VALUES(?, 'MEMORY_SNAPSHOT', 'application/json', ?, ?, ?)
	`, digest, canonical, len(canonical), createdAt); err != nil {
		t.Fatal(err)
	}
	result, err := transaction.Exec(`
		UPDATE agent_memory_revisions
		SET snapshot_ref=?, source_attempt_id=?
		WHERE tenant_id=? AND agent_id=? AND revision=?
	`,
		digest,
		snapshot.SourceAttemptID,
		head.TenantID,
		head.AgentID,
		head.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("rewrite Agent Memory affected=%d error=%v", affected, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := consolidateSQLiteSnapshot(context.Background(), databasePath); err != nil {
		t.Fatal(err)
	}
	if err := verifyNoSQLiteSidecars(databasePath); err != nil {
		t.Fatal(err)
	}
}

func flipLastByte(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatalf("cannot tamper empty file %s", path)
	}
	content[len(content)-1] ^= 0xff
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func firstArtifactOrdinaryFile(t *testing.T, bundle string) string {
	t.Helper()
	root := filepath.Join(bundle, artifactsDirectory)
	var selected string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if selected == "" && !entry.IsDir() && entry.Name() != moduleapi.ArtifactManifestPath {
			selected = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if selected == "" {
		t.Fatal("bundle has no ordinary artifact file")
	}
	return selected
}
