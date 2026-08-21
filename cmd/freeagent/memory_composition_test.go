package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const memoryExampleArtifactDigest = "8b70dd5241cb5d210619f9e3632f2e18ed5346eca8ae7b3c2bb58038b5076641"

func TestDefaultProductionCompositionHasNoMemoryStateArtifactOrLoad(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile default seed: %v", err)
	}
	for _, assertion := range prepared.ModuleAssertions() {
		if assertion.ExpectedAdapterIdentity == localMemoryAdapterID {
			t.Fatalf("default seed includes Memory assertion: %+v", assertion)
		}
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize default production data: %v", err)
	}
	if len(initialized.ArtifactLocks) != 2 {
		t.Fatalf("default artifact locks=%v, want exactly model and declarative", initialized.ArtifactLocks)
	}
	if _, err := os.Lstat(filepath.Join(
		artifactRoot,
		memoryExampleArtifactDigest,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default init staged Memory artifact: %v", err)
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open default production composition: %v", err)
	}
	defer func() {
		if err := composition.Close(); err != nil {
			t.Errorf("close default composition: %v", err)
		}
	}()
	if _, err := composition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	); !errors.Is(err, currentstore.ErrAgentMemoryNotFound) {
		t.Fatalf("default Memory head error=%v, want not found", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		memoryExampleArtifactDigest,
		localMemoryAdapterID,
	)
	if err != nil || registered {
		t.Fatalf("default Memory adapter registration=%v, %v", registered, err)
	}

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-no-memory",
		WorkspaceID: initialized.Defaults.WorkspaceID,
		AgentID:     initialized.Defaults.AgentID,
		ProfileID:   initialized.Defaults.ProfileID,
		Message:     "Pure Chat must not touch optional Memory.",
		RequestID:   "production-no-memory-1",
		Deadline:    memoryCompositionDeadline(1),
	}
	result, err := composition.chat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("default Chat: %v", err)
	}
	if result.TerminalResult == nil || result.Reply != input.Message {
		t.Fatalf("default Chat result=%+v", result)
	}
	dispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("default GetModelDispatchRecord: %v", err)
	}
	if dispatch.Attempt.ContextCompilation != nil {
		t.Fatalf("default Chat compiled optional context: %+v", dispatch.Attempt.ContextCompilation)
	}
	if _, err := composition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	); !errors.Is(err, currentstore.ErrAgentMemoryNotFound) {
		t.Fatalf("default Chat created Memory: %v", err)
	}
}

func TestMemoryProductionLongChainIsLazyAtomicAndRestartSafe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seedPath := memoryProductionSeedPath(t)
	prepared, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile Memory seed: %v", err)
	}
	memoryAssertion := productionMemoryAssertion(t, prepared)
	if memoryAssertion.ArtifactDigest != memoryExampleArtifactDigest {
		t.Fatalf("Memory example digest=%s, want %s", memoryAssertion.ArtifactDigest, memoryExampleArtifactDigest)
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize Memory production data: %v", err)
	}
	if len(initialized.ArtifactLocks) != 3 {
		t.Fatalf("Memory artifact locks=%v, want model, declarative, Memory", initialized.ArtifactLocks)
	}
	assertNoInitializationResidue(t, root, databasePath)

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Memory composition: %v", err)
	}
	genesis := currentMemoryHead(t, composition.store)
	if genesis.SnapshotRef.Revision != 1 ||
		len(genesis.Snapshot.Entries) != 0 ||
		genesis.SourceAttemptID != "" {
		_ = composition.Close()
		t.Fatalf("Memory genesis=%+v", genesis)
	}
	assertMemoryRegistration(t, composition, memoryAssertion, false, "startup")

	firstInput := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-memory",
		WorkspaceID: initialized.Defaults.WorkspaceID,
		AgentID:     initialized.Defaults.AgentID,
		ProfileID:   initialized.Defaults.ProfileID,
		Message:     "Build a Go backend API.",
		RequestID:   "production-memory-1",
		Deadline:    memoryCompositionDeadline(2),
	}
	first, err := composition.chat.Chat(ctx, firstInput)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("first Memory Chat: %v", err)
	}
	if first.TerminalResult == nil || first.Reply != firstInput.Message {
		_ = composition.Close()
		t.Fatalf("first Memory Chat=%+v", first)
	}
	firstCompilation := inspectProductionMemoryDispatch(
		t,
		composition.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		1,
		0,
		firstInput.Message,
	)
	assertMemoryRegistration(t, composition, memoryAssertion, true, "first request")
	headAfterFirst := currentMemoryHead(t, composition.store)
	if headAfterFirst.SnapshotRef.Revision != 2 ||
		headAfterFirst.Snapshot.PreviousSnapshotDigest != genesis.SnapshotRef.Digest ||
		headAfterFirst.SourceAttemptID != first.TerminalResult.AttemptID ||
		len(headAfterFirst.Snapshot.Entries) == 0 {
		_ = composition.Close()
		t.Fatalf("Memory head after first success=%+v", headAfterFirst)
	}
	assertMemoryDerivedEntries(t, headAfterFirst, firstInput.WorkspaceID)

	reentered, err := composition.chat.Chat(ctx, firstInput)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("same-process Memory reentry: %v", err)
	}
	assertExactRAGReentry(t, first, reentered, "same-process Memory")
	headAfterReentry := currentMemoryHead(t, composition.store)
	if headAfterReentry.SnapshotRef != headAfterFirst.SnapshotRef ||
		!bytes.Equal(headAfterReentry.CanonicalBytes, headAfterFirst.CanonicalBytes) {
		_ = composition.Close()
		t.Fatalf("same request appended Memory twice:\nfirst=%+v\nreentry=%+v", headAfterFirst, headAfterReentry)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close first Memory composition: %v", err)
	}

	reopened, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("reopen Memory composition: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened Memory composition: %v", err)
		}
	}()
	assertMemoryRegistration(t, reopened, memoryAssertion, false, "restart before use")
	restartReentry, err := reopened.chat.Chat(ctx, firstInput)
	if err != nil {
		t.Fatalf("restart Memory reentry: %v", err)
	}
	assertExactRAGReentry(t, first, restartReentry, "restart Memory")
	restoredFirstCompilation := inspectProductionMemoryDispatch(
		t,
		reopened.store,
		restartReentry.RunID,
		restartReentry.TerminalResult.AttemptID,
		1,
		0,
		firstInput.Message,
	)
	if !bytes.Equal(firstCompilation, restoredFirstCompilation) {
		t.Fatal("restart changed frozen first Memory compilation bytes")
	}
	// Terminal reentry reads the stored result and must not invoke the adapter.
	assertMemoryRegistration(t, reopened, memoryAssertion, false, "terminal restart reentry")

	secondInput := firstInput
	secondInput.Message = "Use the previous Go backend context to plan the next API step."
	secondInput.RequestID = "production-memory-2"
	secondInput.Deadline = memoryCompositionDeadline(3)
	second, err := reopened.chat.Chat(ctx, secondInput)
	if err != nil {
		t.Fatalf("second Memory Chat: %v", err)
	}
	if second.TerminalResult == nil || second.Reply != secondInput.Message {
		t.Fatalf("second Memory Chat=%+v", second)
	}
	inspectProductionMemoryDispatch(
		t,
		reopened.store,
		second.RunID,
		second.TerminalResult.AttemptID,
		2,
		1,
		secondInput.Message,
	)
	assertMemoryRegistration(t, reopened, memoryAssertion, true, "second request")
	headAfterSecond := currentMemoryHead(t, reopened.store)
	if headAfterSecond.SnapshotRef.Revision != 3 ||
		headAfterSecond.Snapshot.PreviousSnapshotDigest != headAfterFirst.SnapshotRef.Digest ||
		headAfterSecond.SourceAttemptID != second.TerminalResult.AttemptID {
		t.Fatalf("Memory head after second success=%+v", headAfterSecond)
	}
}

func TestProductionCompositionLazilyLoadsExactMemoryAndRejectsArtifactDrift(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(memoryProductionSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile Memory seed: %v", err)
	}
	memoryAssertion := productionMemoryAssertion(t, prepared)
	modelAssertion := prepared.ModelAssertion()
	root := t.TempDir()
	if err := stageArtifact(root, modelAssertion); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	if err := stageArtifact(root, memoryAssertion); err != nil {
		t.Fatalf("stage Memory artifact: %v", err)
	}
	modelProvider := providerFromAssertion(modelAssertion)
	memoryProvider := providerFromAssertion(memoryAssertion)
	entries := []controlcontract.CatalogEntry{
		{Activation: modelProvider, Provides: []moduleapi.PortRef{productionModelPort}},
		{Activation: memoryProvider, Provides: []moduleapi.PortRef{productionContextPort}},
	}
	registry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("new production Registry with Memory: %v", err)
	}
	registered, err := registry.IsRegistered(
		ctx,
		memoryProvider.ArtifactDigest,
		memoryProvider.AdapterIdentity,
	)
	if err != nil || registered {
		t.Fatalf("Memory eagerly registered=%v, %v", registered, err)
	}
	if _, err := registry.ResolveExact(
		ctx,
		memoryProvider.ArtifactDigest,
		memoryProvider.AdapterIdentity,
	); err != nil {
		t.Fatalf("lazy exact Memory resolution: %v", err)
	}
	registered, err = registry.IsRegistered(
		ctx,
		memoryProvider.ArtifactDigest,
		memoryProvider.AdapterIdentity,
	)
	if err != nil || !registered {
		t.Fatalf("resolved Memory registration=%v, %v", registered, err)
	}
	registered, err = registry.IsRegistered(
		ctx,
		memoryProvider.ArtifactDigest,
		"freeagent.adapter.memory.other/v1",
	)
	if err != nil || registered {
		t.Fatalf("fuzzy Memory adapter identity registered=%v, %v", registered, err)
	}

	wrongDigest := memoryProvider
	wrongDigest.ArtifactDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
	if _, err := newMemoryRegistrationFromArtifact(
		wrongDigest,
		memoryAssertion.ArtifactDirectory,
		memoryAssertion.ArtifactSizeBytes,
	); err == nil {
		t.Fatal("Memory provider with wrong exact artifact digest registered")
	}

	stagedDescriptor := filepath.Join(
		root,
		memoryProvider.ArtifactDigest,
		"content",
		"adapter.json",
	)
	if err := os.Remove(stagedDescriptor); err != nil {
		t.Fatalf("remove staged Memory descriptor: %v", err)
	}
	driftedRegistry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("unused missing Memory blocked Registry construction: %v", err)
	}
	if _, err := driftedRegistry.ResolveExact(
		ctx,
		memoryProvider.ArtifactDigest,
		memoryProvider.AdapterIdentity,
	); err == nil {
		t.Fatal("Memory provider with missing descriptor resolved")
	}
	if _, err := driftedRegistry.ResolveExact(
		ctx,
		memoryProvider.ArtifactDigest,
		localKnowledgeAdapterID,
	); err == nil {
		t.Fatal("same Memory artifact resolved through another adapter identity")
	}
}

func inspectProductionMemoryDispatch(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	attemptID string,
	wantRevision uint64,
	minimumSelected int,
	taskMessage string,
) []byte {
	t.Helper()
	record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord: %v", err)
	}
	if record.Attempt.RunID != runID || record.Attempt.ContextCompilation == nil {
		t.Fatalf("Memory dispatch identity/compilation=%+v", record.Attempt)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1: %v", err)
	}
	if compilation.StopReason != corecontract.ContextCompilationRetrievalBelowWatermark ||
		len(compilation.KnowledgeRetrievals) != 0 ||
		len(compilation.MemoryReads) != 1 {
		t.Fatalf("Memory compilation=%+v", compilation)
	}
	read := compilation.MemoryReads[0]
	if read.BindingIndex != 1 ||
		read.Scope.TenantID != defaultTenantID ||
		read.Scope.Agent.ID != defaultAgentID ||
		read.Scope.Workspace.ID != defaultWorkspaceID ||
		read.Snapshot.Revision != wantRevision ||
		len(read.SelectedEntries) < minimumSelected ||
		read.RequestDigest == "" || read.OutputDigest == "" ||
		read.ConfigRef == "" || read.AuthorityCeilingRef == "" ||
		read.EvaluatedAtUnixMS == 0 {
		t.Fatalf("Memory read evidence=%+v", read)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		record.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1: %v", err)
	}
	if len(request.Messages) != 4 ||
		request.Messages[0].Role != moduleapi.ModelRoleSystem ||
		!strings.Contains(request.Messages[0].Content, "UNTRUSTED_CONTEXT_DATA_JSON") ||
		request.Messages[1].Role != moduleapi.ModelRoleSystem ||
		request.Messages[2].Role != moduleapi.ModelRoleUser ||
		!strings.HasPrefix(request.Messages[2].Content, "UNTRUSTED_CONTEXT_DATA_JSON:\n") ||
		request.Messages[3] != (moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: taskMessage,
		}) {
		t.Fatalf("Memory MODEL_REQUEST messages=%+v", request.Messages)
	}
	return bytes.Clone(record.Attempt.ContextCompilation.CanonicalBytes)
}

func assertMemoryDerivedEntries(
	t *testing.T,
	record currentstore.AgentMemoryRevisionRecord,
	workspaceID string,
) {
	t.Helper()
	kinds := make(map[moduleapi.MemoryEntryKindV1]int)
	keys := make(map[string]struct{})
	for _, entry := range record.Snapshot.Entries {
		kinds[entry.Kind]++
		keys[string(entry.Kind)+"\x00"+entry.Key] = struct{}{}
		if len(entry.VisibleWorkspaceIDs) != 1 ||
			entry.VisibleWorkspaceIDs[0] != workspaceID {
			t.Fatalf("derived entry escaped Workspace: %+v", entry)
		}
	}
	if kinds[moduleapi.MemoryEntryTaskSummary] != 1 ||
		kinds[moduleapi.MemoryEntryCategoryCount] < 2 ||
		kinds[moduleapi.MemoryEntryRepeatedTermCount] == 0 {
		t.Fatalf("derived Memory kinds=%v entries=%+v", kinds, record.Snapshot.Entries)
	}
	for _, expected := range []string{
		string(moduleapi.MemoryEntryCategoryCount) + "\x00backend",
		string(moduleapi.MemoryEntryCategoryCount) + "\x00programming",
		string(moduleapi.MemoryEntryRepeatedTermCount) + "\x00go",
	} {
		if _, found := keys[expected]; !found {
			t.Fatalf("missing derived Memory key %q in %+v", expected, record.Snapshot.Entries)
		}
	}
}

func assertMemoryRegistration(
	t *testing.T,
	composition *productionComposition,
	assertion bootstrapseed.ModuleAssertion,
	want bool,
	phase string,
) {
	t.Helper()
	registered, err := composition.registry.IsRegistered(
		context.Background(),
		assertion.ArtifactDigest,
		assertion.ExpectedAdapterIdentity,
	)
	if err != nil || registered != want {
		t.Fatalf("%s Memory registration=%v, %v, want %v", phase, registered, err, want)
	}
}

func currentMemoryHead(
	t *testing.T,
	store *currentstore.Store,
) currentstore.AgentMemoryRevisionRecord {
	t.Helper()
	head, err := store.GetCurrentAgentMemory(
		context.Background(),
		defaultTenantID,
		defaultAgentID,
	)
	if err != nil {
		t.Fatalf("GetCurrentAgentMemory: %v", err)
	}
	return head
}

func productionMemoryAssertion(
	t *testing.T,
	prepared *bootstrapseed.Prepared,
) bootstrapseed.ModuleAssertion {
	t.Helper()
	for _, assertion := range prepared.ModuleAssertions() {
		if assertion.ExpectedAdapterIdentity == localMemoryAdapterID {
			return assertion
		}
	}
	t.Fatal("Memory seed has no Memory assertion")
	return bootstrapseed.ModuleAssertion{}
}

func memoryProductionSeedPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Memory composition test source")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source),
		"..",
		"..",
		"examples",
		"current-v1.memory.bootstrap.seed.json",
	))
}

func memoryCompositionDeadline(day int) time.Time {
	return time.Date(2099, time.January, day, 3, 4, 5, 0, time.UTC)
}
