package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/loopapi"
)

var errW2R1MaterialUnavailable = errors.New(
	"W2-R1 test material is deliberately unavailable",
)

// TestW2R1NativeRemoteProductionChainFailsClosedBeforePOSTV1 closes the
// production Catalog -> lazy loader -> NewFromArtifact -> native Adapter ->
// Gateway connection without weakening the production transport. The
// late-bound resolver observes the already-committed PENDING Attempt and then
// refuses material, so the native Adapter deterministically stops before POST.
func TestW2R1NativeRemoteProductionChainFailsClosedBeforePOSTV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture, _ := newW2R1RemoteActionFixtureV1(t, root)
	applyW2R1RemoteActionV1(t, root, databasePath, artifactRoot, fixture)

	resolver := &w2r1UnavailableMaterialResolverV1{databasePath: databasePath}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			RemoteAction: &productionRemoteActionRuntimeConfig{
				SecretResolver: resolver,
			},
		},
	)
	if err != nil {
		t.Fatalf("open production REMOTE composition: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = composition.Close()
		}
	})

	registered, err := composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil || registered {
		t.Fatalf("REMOTE adapter was materialized before use: %v, %v", registered, err)
	}

	input := moduleApplyChatInputV1(
		"w2r1-production-native-pre-post-failure",
		"attempt the production native REMOTE action once",
	)
	result, err := composition.chat.Chat(ctx, input)
	if err != nil || !result.AdmissionCreated || result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.FailureCode != "REMOTE_ACTION_SECRET_UNAVAILABLE" {
		t.Fatalf("production native REMOTE result=%+v error=%v", result, err)
	}

	record := w2r1ActionForRunV1(t, composition.store, result.RunID)
	assertW2R1RemoteActionClosureV1(
		t,
		composition.store,
		record,
		fixture,
		currentstore.ActionDispatchFailed,
	)
	if record.Attempt.ErrorClassification != "REMOTE_ACTION_SECRET_UNAVAILABLE" ||
		record.Result != nil || record.ProviderReceipt != nil ||
		w2r1ActionAttemptCountV1(t, databasePath, result.RunID) != 1 {
		t.Fatalf("production native REMOTE failure closure=%+v", record)
	}

	observation := resolver.snapshot()
	if observation.calls != 1 || observation.err != nil ||
		observation.pendingAttemptID != record.Attempt.AttemptID ||
		observation.identity.Provider != record.Attempt.Binding.Provider ||
		observation.identity.EndpointURL != fixture.EndpointURL ||
		observation.identity.SecretRef != fixture.SecretRef {
		t.Fatalf("production resolver observation=%+v", observation)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil || !registered {
		t.Fatalf("native REMOTE adapter was not lazily registered: %v, %v", registered, err)
	}
	invoker, err := composition.registry.ResolveExact(
		ctx,
		fixture.ArtifactDigest,
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil {
		t.Fatalf("resolve lazily registered native REMOTE adapter: %v", err)
	}
	if _, ok := invoker.(*remoteactionhttp.Adapter); !ok {
		t.Fatalf("production REMOTE invoker=%T, want *remoteactionhttp.Adapter", invoker)
	}

	retry, err := composition.chat.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated || retry.RunID != result.RunID ||
		retry.TerminalResult == nil || retry.FailureCode != result.FailureCode ||
		resolver.snapshot().calls != 1 ||
		w2r1ActionAttemptCountV1(t, databasePath, result.RunID) != 1 {
		t.Fatalf("production native REMOTE exact retry=%+v error=%v", retry, err)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close production REMOTE composition: %v", err)
	}
	closed = true
}

// TestW2R1ProductionLazyLoaderRejectsTamperedArtifactV1 proves the production
// Registry remains lazy and that its NewFromArtifact path fails before
// resolving runtime material when the installed descriptor drifts.
func TestW2R1ProductionLazyLoaderRejectsTamperedArtifactV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture, _ := newW2R1RemoteActionFixtureV1(t, root)
	applyW2R1RemoteActionV1(t, root, databasePath, artifactRoot, fixture)

	descriptorPath := filepath.Join(
		artifactRoot,
		fixture.ArtifactDigest,
		"content",
		"actions.json",
	)
	descriptor, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, append(descriptor, ' '), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := &w2r1UnavailableMaterialResolverV1{databasePath: databasePath}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			RemoteAction: &productionRemoteActionRuntimeConfig{
				SecretResolver: resolver,
			},
		},
	)
	if err != nil {
		t.Fatalf("open lazy production REMOTE composition: %v", err)
	}
	defer composition.Close()

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := composition.registry.ResolveExact(
			ctx,
			fixture.ArtifactDigest,
			remoteactionhttp.AdapterIdentityV1,
		); err == nil {
			t.Fatal("production lazy loader accepted a tampered REMOTE descriptor")
		}
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil || registered || resolver.snapshot().calls != 0 {
		t.Fatalf(
			"tampered REMOTE state registered=%v resolutions=%d error=%v",
			registered,
			resolver.snapshot().calls,
			err,
		)
	}
}

func TestW2R1DisabledRemoteProviderHasZeroProductionAccessV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture, _ := newW2R1RemoteActionFixtureV1(t, root)
	applyW2R1RemoteActionV1(t, root, databasePath, artifactRoot, fixture)

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "remote-disable.json"),
		newModuleApplyRemoteDisablePlanV1(t, 2),
	)
	_, disabled, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		disablePath,
		"",
		moduleApplyRemoteGrantsV1{},
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("disable REMOTE result=%+v error=%v", disabled, err)
	}
	assertModuleApplyRemoteInstanceAbsentV1(t, databasePath)
	if err := os.RemoveAll(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("remove disabled REMOTE artifact: %v", err)
	}

	resolver := &w2r1UnavailableMaterialResolverV1{databasePath: databasePath}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			RemoteAction: &productionRemoteActionRuntimeConfig{
				SecretResolver: resolver,
			},
		},
	)
	if err != nil {
		t.Fatalf("open composition after REMOTE disable: %v", err)
	}
	defer composition.Close()

	before := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath)
	input := moduleApplyChatInputV1(
		"w2r1-disabled-remote-pure-chat",
		"disabled REMOTE must leave this as ordinary Pure Chat",
	)
	result, err := composition.chat.Chat(ctx, input)
	if err != nil || !result.AdmissionCreated || result.TerminalResult == nil ||
		result.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.Reply != input.Message || result.FailureCode != "" {
		t.Fatalf("Pure Chat after REMOTE disable=%+v error=%v", result, err)
	}
	if after := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); after != before {
		t.Fatalf("disabled REMOTE created Action Attempts: before=%d after=%d", before, after)
	}
	if resolver.snapshot().calls != 0 {
		t.Fatal("disabled REMOTE resolved runtime material")
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil || registered {
		t.Fatalf("disabled REMOTE adapter state=%v error=%v", registered, err)
	}
}

type w2r1MaterialObservationV1 struct {
	calls            int
	identity         remoteactionhttp.SecretIdentityV1
	pendingAttemptID string
	err              error
}

type w2r1UnavailableMaterialResolverV1 struct {
	mu           sync.Mutex
	databasePath string
	observation  w2r1MaterialObservationV1
}

func (resolver *w2r1UnavailableMaterialResolverV1) ResolveSecret(
	ctx context.Context,
	identity remoteactionhttp.SecretIdentityV1,
) ([]byte, error) {
	observation := w2r1MaterialObservationV1{
		calls:    1,
		identity: identity,
	}
	database, err := sql.Open("sqlite", resolver.databasePath)
	if err == nil {
		err = database.QueryRowContext(ctx, `
			SELECT attempt_id
			FROM dispatch_attempts
			WHERE dispatch_kind='ACTION' AND state='PENDING'
			ORDER BY created_at, attempt_id
			LIMIT 1
		`).Scan(&observation.pendingAttemptID)
		closeErr := database.Close()
		err = errors.Join(err, closeErr)
	}
	observation.err = err
	resolver.mu.Lock()
	if resolver.observation.calls != 0 {
		observation.calls += resolver.observation.calls
	}
	resolver.observation = observation
	resolver.mu.Unlock()
	return nil, errW2R1MaterialUnavailable
}

func (resolver *w2r1UnavailableMaterialResolverV1) snapshot() w2r1MaterialObservationV1 {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.observation
}

func w2r1ActionForRunV1(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) currentstore.ActionDispatchRecord {
	t.Helper()
	database, err := sql.Open("sqlite", store.Path())
	if err != nil {
		t.Fatal(err)
	}
	var attemptID string
	queryErr := database.QueryRow(`
		SELECT attempt_id
		FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind='ACTION'
		ORDER BY created_at, attempt_id
		LIMIT 1
	`, runID).Scan(&attemptID)
	closeErr := database.Close()
	if queryErr != nil || closeErr != nil {
		t.Fatalf("read production REMOTE Attempt identity: %v", errors.Join(queryErr, closeErr))
	}
	record, err := store.GetActionDispatchRecord(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("read production REMOTE Attempt: %v", err)
	}
	return record
}
