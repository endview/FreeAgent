package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestInitializeProductionArtifactRootIsIngressSelectable(t *testing.T) {
	t.Parallel()

	trustedState, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		t.TempDir(),
		"trusted-state-*",
	)
	if err != nil {
		t.Fatalf("provision trusted state: %v", err)
	}
	databasePath := filepath.Join(trustedState, "current.sqlite")
	artifactRoot := filepath.Join(trustedState, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	if _, err := moduleartifactstore.SelectArtifactRootV1(artifactRoot); err != nil {
		t.Fatalf("select initialized artifact root for ingress: %v", err)
	}
}

func TestResolveExistingArtifactRootV1RejectsUnsafeAncestry(t *testing.T) {
	t.Parallel()
	if _, err := resolveExistingArtifactRootV1(
		artifactRootBelowUnsafeAncestorV1(t),
	); err == nil {
		t.Fatal("content-addressed artifact root below unsafe ancestry was accepted")
	}
}

func TestProductionCompositionInitChatAndRestartIdempotency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	if initialized.DatabasePath != databasePath || initialized.ArtifactRoot != artifactRoot {
		t.Fatalf("unexpected initialized paths: %#v", initialized)
	}
	if len(initialized.ArtifactLocks) != 2 {
		t.Fatalf("artifact locks=%v, want model and declarative context", initialized.ArtifactLocks)
	}
	assertNoInitializationResidue(t, root, databasePath)

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message:     "composition restart smoke",
		RequestID:   "composition-restart-smoke-1",
		Deadline:    deadline,
	}

	firstComposition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open first composition: %v", err)
	}
	first, firstErr := firstComposition.chat.Chat(context.Background(), input)
	if closeErr := firstComposition.Close(); firstErr != nil || closeErr != nil {
		t.Fatalf("first chat/close: %v", errors.Join(firstErr, closeErr))
	}
	if first.Reply != input.Message {
		t.Fatalf("first reply=%q, want %q", first.Reply, input.Message)
	}

	secondComposition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("reopen composition: %v", err)
	}
	second, secondErr := secondComposition.chat.Chat(context.Background(), input)
	if closeErr := secondComposition.Close(); secondErr != nil || closeErr != nil {
		t.Fatalf("second chat/close: %v", errors.Join(secondErr, closeErr))
	}
	if second.RunID != first.RunID || second.Reply != first.Reply ||
		second.LoopResult.Disposition != first.LoopResult.Disposition {
		t.Fatalf("restart retry changed result:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

type scriptedSchedulerCloser struct {
	mu      sync.Mutex
	results []error
	calls   int
}

func (closer *scriptedSchedulerCloser) Close(context.Context) error {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	closer.calls++
	if len(closer.results) == 0 {
		return nil
	}
	result := closer.results[0]
	closer.results = closer.results[1:]
	return result
}

func TestProductionCompositionDoesNotCloseStoreAfterSchedulerDrainFailure(
	t *testing.T,
) {
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(
		context.Background(),
		databasePath,
	); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	drainFailure := errors.New("injected Scheduler drain timeout")
	closer := &scriptedSchedulerCloser{results: []error{drainFailure, nil}}
	composition := &productionComposition{store: store, scheduler: closer}
	if err := composition.Close(); !errors.Is(err, drainFailure) {
		t.Fatalf("first Close error=%v want drain failure", err)
	}
	if _, _, err := store.GetWorkspaceSchedulerState(
		context.Background(),
		"tenant-a",
		"workspace-a",
	); err != nil {
		t.Fatalf("Store closed after failed Scheduler drain: %v", err)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, _, err := store.GetWorkspaceSchedulerState(
		context.Background(),
		"tenant-a",
		"workspace-a",
	); err == nil {
		t.Fatal("Store remained open after successful ordered drain")
	}
}

func TestProductionActionCompositionRunsModelActionModelChain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     actionExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize Action production data: %v", err)
	}
	if len(initialized.ArtifactLocks) != 3 {
		t.Fatalf(
			"Action artifact locks=%v, want model, context and action",
			initialized.ArtifactLocks,
		)
	}
	if initialized.Defaults.ProfileID != "action-chat" {
		t.Fatalf("Action defaults=%+v", initialized.Defaults)
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Action composition: %v", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("text.stats loaded before selected Profile admission = %v, %v", registered, err)
	}

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-action",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     "hello 世界\nsecond line",
		RequestID:   "production-action-chain-1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}
	result, err := composition.chat.Chat(ctx, input)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("Action Chat() error = %v", err)
	}
	if !result.AdmissionCreated || result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.Reply != "text.stats completed." || result.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("Action Chat() = %+v", result)
	}
	terminal := *result.TerminalResult
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != corecontract.ModelAttemptSucceeded {
		_ = composition.Close()
		t.Fatalf("Action terminal = %+v", terminal)
	}
	modelTwo, err := composition.store.GetModelDispatchRecord(
		ctx,
		terminal.AttemptID,
	)
	if err != nil || modelTwo.Attempt.LogicalStepID !=
		corecontract.SecondModelLogicalStepIDV1 ||
		modelTwo.Attempt.SourceDispatchAttemptID == "" {
		_ = composition.Close()
		t.Fatalf("model-2 dispatch = %+v, %v", modelTwo, err)
	}
	modelTwoRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		modelTwo.Attempt.Request.CanonicalBytes,
	)
	if err != nil || len(modelTwoRequest.Messages) == 0 ||
		!strings.HasPrefix(
			modelTwoRequest.Messages[len(modelTwoRequest.Messages)-1].Content,
			corecontract.UntrustedActionResultPrefixV1,
		) {
		_ = composition.Close()
		t.Fatalf("model-2 request did not contain the frozen Action envelope: %v", err)
	}
	action, err := composition.store.GetActionDispatchRecord(
		ctx,
		modelTwo.Attempt.SourceDispatchAttemptID,
	)
	if err != nil || action.Attempt.State != currentstore.ActionDispatchSucceeded ||
		action.Attempt.PublicActionID != "text.stats" ||
		action.Attempt.ProviderActionID != "text.stats" || action.Result == nil {
		_ = composition.Close()
		t.Fatalf("Action dispatch = %+v, %v", action, err)
	}
	var actionResult struct {
		Status string `json:"status"`
		Result struct {
			Bytes uint64 `json:"bytes"`
			Runes uint64 `json:"runes"`
			Words uint64 `json:"words"`
			Lines uint64 `json:"lines"`
		} `json:"result"`
	}
	if err := json.Unmarshal(action.Result.CanonicalBytes, &actionResult); err != nil ||
		actionResult.Status != string(corecontract.ActionResultAvailable) ||
		actionResult.Result.Bytes != 24 || actionResult.Result.Runes != 20 ||
		actionResult.Result.Words != 4 || actionResult.Result.Lines != 2 {
		_ = composition.Close()
		t.Fatalf("stored text.stats result = %+v, %v", actionResult, err)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || !registered {
		_ = composition.Close()
		t.Fatalf("text.stats was not loaded by selected Action admission = %v, %v", registered, err)
	}
	retry, err := composition.chat.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated || retry.RunID != result.RunID ||
		retry.Reply != result.Reply {
		_ = composition.Close()
		t.Fatalf("Action retry=%+v, %v; first=%+v", retry, err, result)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close Action composition: %v", err)
	}
}

func TestProductionRAGLongChainIsByteStableAcrossReentryAndRestart(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     ragExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize RAG production data: %v", err)
	}
	if len(initialized.ArtifactLocks) != 3 {
		t.Fatalf(
			"artifact locks=%v, want model, declarative context, and knowledge",
			initialized.ArtifactLocks,
		)
	}
	assertNoInitializationResidue(t, root, databasePath)

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-rag",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message: "How does FreeAgent shared knowledge stay independent from " +
			"Agent and Workspace definitions?",
		RequestID: "production-rag-long-chain-1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}

	firstComposition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open first RAG composition: %v", err)
	}
	first, err := firstComposition.chat.Chat(ctx, input)
	if err != nil {
		_ = firstComposition.Close()
		t.Fatalf("first RAG Chat() error = %v", err)
	}
	if first.TerminalResult == nil || !first.AdmissionCreated ||
		first.Reply != input.Message {
		_ = firstComposition.Close()
		t.Fatalf("first RAG Chat() = %+v", first)
	}
	firstSnapshot := inspectProductionRAGDispatch(
		t,
		firstComposition.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		"first",
		input.Message,
	)

	second, err := firstComposition.chat.Chat(ctx, input)
	if err != nil {
		_ = firstComposition.Close()
		t.Fatalf("same-process RAG reentry error = %v", err)
	}
	assertExactRAGReentry(t, first, second, "same-process")
	secondSnapshot := inspectProductionRAGDispatch(
		t,
		firstComposition.store,
		second.RunID,
		second.TerminalResult.AttemptID,
		"same-process",
		input.Message,
	)
	assertProductionRAGSnapshotEqual(t, firstSnapshot, secondSnapshot, "same-process")
	if err := firstComposition.Close(); err != nil {
		t.Fatalf("close first RAG composition: %v", err)
	}

	reopened, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("reopen RAG composition: %v", err)
	}
	third, err := reopened.chat.Chat(ctx, input)
	if err != nil {
		_ = reopened.Close()
		t.Fatalf("restart RAG reentry error = %v", err)
	}
	assertExactRAGReentry(t, first, third, "restart")
	thirdSnapshot := inspectProductionRAGDispatch(
		t,
		reopened.store,
		third.RunID,
		third.TerminalResult.AttemptID,
		"restart",
		input.Message,
	)
	assertProductionRAGSnapshotEqual(t, firstSnapshot, thirdSnapshot, "restart")
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened RAG composition: %v", err)
	}
}

type productionRAGSnapshot struct {
	attemptID            string
	requestCanonical     []byte
	compilationCanonical []byte
}

func inspectProductionRAGDispatch(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	attemptID string,
	phase string,
	taskMessage string,
) productionRAGSnapshot {
	t.Helper()

	record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("%s GetModelDispatchRecord() error = %v", phase, err)
	}
	if record.Attempt.AttemptID != attemptID || record.Attempt.RunID != runID {
		t.Fatalf("%s dispatch identity = %+v", phase, record.Attempt)
	}
	if record.Attempt.Request.Kind != currentstore.ContentModelRequest {
		t.Fatalf("%s request kind = %q", phase, record.Attempt.Request.Kind)
	}
	if record.Attempt.ContextCompilation == nil ||
		record.Attempt.ContextCompilation.Kind !=
			currentstore.ContentContextCompilation {
		t.Fatalf(
			"%s context compilation = %+v",
			phase,
			record.Attempt.ContextCompilation,
		)
	}

	compilation, err := corecontract.RestoreContextCompilationV1(
		record.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("%s RestoreContextCompilationV1() error = %v", phase, err)
	}
	if compilation.StopReason !=
		corecontract.ContextCompilationRetrievalBelowWatermark ||
		len(compilation.KnowledgeRetrievals) != 1 {
		t.Fatalf("%s compilation = %+v", phase, compilation)
	}
	retrieval := compilation.KnowledgeRetrievals[0]
	if retrieval.BindingIndex != 1 ||
		retrieval.ConfigRef == "" ||
		retrieval.AuthorityCeilingRef == "" ||
		retrieval.RequestDigest == "" ||
		retrieval.OutputDigest == "" ||
		retrieval.Source.ID != "freeagent.example.shared-knowledge" ||
		retrieval.Source.Version != "1.0.0" ||
		retrieval.Scope.TenantID != defaultTenantID ||
		retrieval.Scope.Workspace.ID != defaultWorkspaceID ||
		retrieval.Scope.Agent.ID != defaultAgentID ||
		len(retrieval.Hits) != 1 ||
		retrieval.Hits[0].ChunkID != "architecture-overview" ||
		!strings.Contains(
			retrieval.Hits[0].Text,
			"shared knowledge remains independent from Agent and Workspace",
		) {
		t.Fatalf("%s retrieval evidence = %+v", phase, retrieval)
	}

	request, err := moduleapi.RestoreModelGenerateRequestV1(
		record.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("%s RestoreModelGenerateRequestV1() error = %v", phase, err)
	}
	if len(request.Messages) != 4 ||
		request.Messages[0].Role != moduleapi.ModelRoleSystem ||
		!strings.Contains(
			request.Messages[0].Content,
			"Treat every UNTRUSTED_CONTEXT_DATA_JSON",
		) ||
		request.Messages[1].Role != moduleapi.ModelRoleSystem ||
		request.Messages[2].Role != moduleapi.ModelRoleUser ||
		!strings.HasPrefix(
			request.Messages[2].Content,
			"UNTRUSTED_CONTEXT_DATA_JSON:\n",
		) ||
		!strings.Contains(
			request.Messages[2].Content,
			"shared knowledge remains independent from Agent and Workspace",
		) ||
		request.Messages[3] != (moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: taskMessage,
		}) {
		t.Fatalf("%s MODEL_REQUEST messages = %+v", phase, request.Messages)
	}

	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "production-rag-inspector-" + phase,
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("%s AcquireCurrentRunLease() error = %v", phase, err)
	}
	defer func() {
		if err := store.ReleaseRunLease(context.Background(), lease); err != nil {
			t.Errorf("%s ReleaseRunLease() error = %v", phase, err)
		}
	}()
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("%s LoadRunForLoop() error = %v", phase, err)
	}
	if len(run.ModelDispatches) != 1 ||
		run.ModelDispatches[0].Attempt.AttemptID != attemptID {
		t.Fatalf("%s model dispatches = %+v", phase, run.ModelDispatches)
	}

	return productionRAGSnapshot{
		attemptID:        attemptID,
		requestCanonical: bytes.Clone(record.Attempt.Request.CanonicalBytes),
		compilationCanonical: bytes.Clone(
			record.Attempt.ContextCompilation.CanonicalBytes,
		),
	}
}

func assertExactRAGReentry(
	t *testing.T,
	first localchat.ChatResult,
	reentered localchat.ChatResult,
	phase string,
) {
	t.Helper()
	if reentered.AdmissionCreated || reentered.RunID != first.RunID ||
		reentered.TerminalResult == nil || first.TerminalResult == nil ||
		reentered.TerminalResult.AttemptID != first.TerminalResult.AttemptID ||
		reentered.Reply != first.Reply ||
		reentered.LoopResult.Disposition != first.LoopResult.Disposition {
		t.Fatalf("%s reentry changed result:\nfirst=%+v\nreentered=%+v", phase, first, reentered)
	}
}

func assertProductionRAGSnapshotEqual(
	t *testing.T,
	want productionRAGSnapshot,
	got productionRAGSnapshot,
	phase string,
) {
	t.Helper()
	if got.attemptID != want.attemptID ||
		!bytes.Equal(got.requestCanonical, want.requestCanonical) ||
		!bytes.Equal(got.compilationCanonical, want.compilationCanonical) {
		t.Fatalf(
			"%s changed frozen dispatch bytes:\nwant=%+v\ngot=%+v",
			phase,
			want,
			got,
		)
	}
}

func TestInitializeProductionDataNeverOverwritesTargets(t *testing.T) {
	t.Parallel()

	t.Run("database", func(t *testing.T) {
		root := t.TempDir()
		databasePath := filepath.Join(root, "current.sqlite")
		artifactRoot := filepath.Join(root, "artifacts")
		const sentinel = "existing database"
		if err := os.WriteFile(databasePath, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := initializeProductionData(context.Background(), initInput{
			DatabasePath: databasePath,
			SeedPath:     exampleSeedPath(t),
			ArtifactRoot: artifactRoot,
		})
		if err == nil {
			t.Fatal("initialize unexpectedly replaced existing database")
		}
		actual, readErr := os.ReadFile(databasePath)
		if readErr != nil || string(actual) != sentinel {
			t.Fatalf("existing database changed: content=%q err=%v", actual, readErr)
		}
		if _, statErr := os.Lstat(artifactRoot); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("artifact root was created on rejected init: %v", statErr)
		}
	})

	t.Run("artifact root", func(t *testing.T) {
		root := t.TempDir()
		databasePath := filepath.Join(root, "current.sqlite")
		artifactRoot := filepath.Join(root, "artifacts")
		if err := os.Mkdir(artifactRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := initializeProductionData(context.Background(), initInput{
			DatabasePath: databasePath,
			SeedPath:     exampleSeedPath(t),
			ArtifactRoot: artifactRoot,
		})
		if err == nil {
			t.Fatal("initialize unexpectedly replaced existing artifact root")
		}
		if _, statErr := os.Lstat(databasePath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("database was created on rejected init: %v", statErr)
		}
	})
}

func TestInitializeProductionDataResumesExactOrphanArtifactRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	input := initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}
	first, err := initializeProductionData(context.Background(), input)
	if err != nil {
		t.Fatalf("first initialize: %v", err)
	}
	if err := os.Remove(databasePath); err != nil {
		t.Fatalf("simulate crash before database commit marker: %v", err)
	}
	second, err := initializeProductionData(context.Background(), input)
	if err != nil {
		t.Fatalf("resume exact orphan artifact root: %v", err)
	}
	if second.SeedID != first.SeedID ||
		second.Defaults != first.Defaults ||
		len(second.ArtifactLocks) != len(first.ArtifactLocks) {
		t.Fatalf("resumed initialization changed seed result: first=%#v second=%#v", first, second)
	}
	assertNoInitializationResidue(t, root, databasePath)
}

func TestPublishInitNoReplacePreservesExistingTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "target.txt"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishInitNoReplace(source, destination); err == nil {
		t.Fatal("no-replace publication unexpectedly replaced existing target")
	}
	for path, expected := range map[string]string{
		filepath.Join(source, "source.txt"):      "source",
		filepath.Join(destination, "target.txt"): "target",
	} {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != expected {
			t.Fatalf("path %s changed: content=%q err=%v", path, actual, err)
		}
	}
}

func TestStageVerifiedArtifactPreservesIdentityModesAndCompatibilityWrapper(
	t *testing.T,
) {
	t.Parallel()

	assertion := newCompositionKnowledgeAssertion(t)
	sourceFile := filepath.Join(
		assertion.ArtifactDirectory,
		"content",
		"source.json",
	)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(sourceFile, 0o700); err != nil {
			t.Fatalf("mark source executable: %v", err)
		}
	}

	target := filepath.Join(t.TempDir(), "artifact")
	if err := stageVerifiedArtifact(
		context.Background(),
		assertion.ArtifactDirectory,
		target,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
	); err != nil {
		t.Fatalf("stage verified artifact: %v", err)
	}
	assertStagedArtifactIdentity(t, target, assertion)
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{
			filepath.Join(target, moduleapi.ArtifactManifestPath): 0o600,
			filepath.Join(target, "content", "source.json"):       0o700,
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat staged path %s: %v", path, err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Fatalf("staged mode %s = %04o, want %04o", path, got, want)
			}
		}
	}

	compatibilityRoot := t.TempDir()
	if err := stageArtifact(compatibilityRoot, assertion); err != nil {
		t.Fatalf("compatibility stageArtifact wrapper: %v", err)
	}
	assertStagedArtifactIdentity(
		t,
		filepath.Join(compatibilityRoot, assertion.ArtifactDigest),
		assertion,
	)
}

func TestStageVerifiedArtifactRejectsInvalidInputBeforeCreatingTarget(
	t *testing.T,
) {
	t.Parallel()

	assertion := newCompositionKnowledgeAssertion(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	wrongDigest := "a" + assertion.ArtifactDigest[1:]
	if wrongDigest == assertion.ArtifactDigest {
		wrongDigest = "b" + assertion.ArtifactDigest[1:]
	}
	tests := []struct {
		name           string
		ctx            context.Context
		expectedDigest string
		expectedSize   uint64
		wantContextErr error
	}{
		{
			name:           "nil context",
			ctx:            nil,
			expectedDigest: assertion.ArtifactDigest,
			expectedSize:   assertion.ArtifactSizeBytes,
		},
		{
			name:           "cancelled before copy",
			ctx:            cancelled,
			expectedDigest: assertion.ArtifactDigest,
			expectedSize:   assertion.ArtifactSizeBytes,
			wantContextErr: context.Canceled,
		},
		{
			name:           "wrong digest",
			ctx:            context.Background(),
			expectedDigest: wrongDigest,
			expectedSize:   assertion.ArtifactSizeBytes,
		},
		{
			name:           "wrong size",
			ctx:            context.Background(),
			expectedDigest: assertion.ArtifactDigest,
			expectedSize:   assertion.ArtifactSizeBytes + 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "artifact")
			err := stageVerifiedArtifact(
				test.ctx,
				assertion.ArtifactDirectory,
				target,
				test.expectedDigest,
				test.expectedSize,
			)
			if err == nil {
				t.Fatal("stage unexpectedly succeeded")
			}
			if test.wantContextErr != nil &&
				!errors.Is(err, test.wantContextErr) {
				t.Fatalf("stage error = %v, want %v", err, test.wantContextErr)
			}
			if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("rejected stage created target: %v", statErr)
			}
		})
	}
}

func TestStageVerifiedArtifactPreservesExistingTarget(t *testing.T) {
	t.Parallel()

	assertion := newCompositionKnowledgeAssertion(t)
	target := filepath.Join(t.TempDir(), "artifact")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageVerifiedArtifact(
		context.Background(),
		assertion.ArtifactDirectory,
		target,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
	); err == nil {
		t.Fatal("stage unexpectedly replaced existing target")
	}
	actual, err := os.ReadFile(sentinelPath)
	if err != nil || string(actual) != "existing" {
		t.Fatalf("existing target changed: content=%q err=%v", actual, err)
	}
}

func TestStageVerifiedArtifactCancellationRemovesPartialTarget(t *testing.T) {
	t.Parallel()

	assertion := newCompositionKnowledgeAssertion(t)
	target := filepath.Join(t.TempDir(), "artifact")
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelWhenPathExistsContext{
		Context: base,
		cancel:  cancel,
		path:    filepath.Join(target, moduleapi.ArtifactManifestPath),
	}
	err := stageVerifiedArtifact(
		ctx,
		assertion.ArtifactDirectory,
		target,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stage error = %v, want context cancellation", err)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled stage left a partial target: %v", statErr)
	}
}

func TestStageVerifiedArtifactPostCopyVerificationRemovesChangedTarget(
	t *testing.T,
) {
	t.Parallel()

	assertion := newCompositionKnowledgeAssertion(t)
	target := filepath.Join(t.TempDir(), "artifact")
	ctx := &mutateWhenPathHasContentContext{
		Context: context.Background(),
		path:    filepath.Join(target, "content", "source.json"),
	}
	err := stageVerifiedArtifact(
		ctx,
		assertion.ArtifactDirectory,
		target,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
	)
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("changed target stage error = %v", err)
	}
	if mutationErr := ctx.MutationError(); mutationErr != nil {
		t.Fatalf("mutate staged target: %v", mutationErr)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed verification left a partial target: %v", statErr)
	}
}

func TestOpenProductionCompositionDoesNotCreateMissingStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "missing.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	); err == nil {
		t.Fatal("open unexpectedly initialized a missing Store")
	}
	if _, err := os.Lstat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Store was created: %v", err)
	}
}

func TestOpenProductionCompositionRejectsArtifactTampering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	readme := filepath.Join(artifactRoot, localEchoArtifactDigest, "README.md")
	file, err := os.OpenFile(readme, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open artifact for tamper: %v", err)
	}
	if _, err := file.WriteString("\ntampered\n"); err != nil {
		_ = file.Close()
		t.Fatalf("tamper artifact: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tampered artifact: %v", err)
	}
	if _, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	); err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("tampered artifact error=%v", err)
	}
}

func TestProductionAdapterRegistryWithoutRAGKeepsExactEchoOnly(t *testing.T) {
	t.Parallel()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() error = %v", err)
	}
	model := prepared.ModelAssertion()
	root := t.TempDir()
	if err := stageArtifact(root, model); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	provider := providerFromAssertion(model)
	registry, err := newProductionAdapterRegistry(
		root,
		[]controlcontract.CatalogEntry{{
			Activation: provider,
			Provides:   []moduleapi.PortRef{productionModelPort},
		}},
	)
	if err != nil {
		t.Fatalf("newProductionAdapterRegistry() error = %v", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || !registered {
		t.Fatalf("exact Echo registration = %v, %v", registered, err)
	}
	registered, err = registry.IsRegistered(
		context.Background(),
		strings.Repeat("f", moduleapi.SHA256HexLength),
		localKnowledgeAdapterID,
	)
	if err != nil || registered {
		t.Fatalf("unexpected knowledge registration = %v, %v", registered, err)
	}
	registered, err = registry.IsRegistered(
		context.Background(),
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		t.Fatalf("Pure Chat unexpectedly loaded text.stats = %v, %v", registered, err)
	}
}

func TestBootstrapActivationAcceptsExactDeepSeekWithoutRuntimeCapability(
	t *testing.T,
) {
	t.Parallel()
	assertion := deepSeekModelAssertion(t)
	if _, err := newBootstrapActivationResolver(
		[]bootstrapseed.ModuleAssertion{assertion},
	); err != nil {
		t.Fatalf("DeepSeek bootstrap activation probe: %v", err)
	}
}

func TestProductionDeepSeekRegistryRequiresExplicitRuntimeAndResolvesExactly(
	t *testing.T,
) {
	t.Parallel()
	assertion := deepSeekModelAssertion(t)
	root := t.TempDir()
	if err := stageArtifact(root, assertion); err != nil {
		t.Fatalf("stage DeepSeek artifact: %v", err)
	}
	provider := providerFromAssertion(assertion)
	entries := []controlcontract.CatalogEntry{{
		Activation: provider,
		Provides:   []moduleapi.PortRef{productionModelPort},
	}}
	if _, err := newProductionAdapterRegistry(root, entries); err == nil ||
		!strings.Contains(err.Error(), "no explicit runtime configuration") {
		t.Fatalf("missing DeepSeek runtime error = %v", err)
	}

	resolverCalls := 0
	transportCalls := 0
	runtimeConfig := &productionDeepSeekRuntimeConfig{
		APIKeyResolver: deepseekmodel.APIKeyResolverFunc(func(
			context.Context,
			deepseekmodel.APIKeyIdentity,
		) ([]byte, error) {
			resolverCalls++
			return []byte("not-a-secret"), nil
		}),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(
			*http.Request,
		) (*http.Response, error) {
			transportCalls++
			return nil, errors.New("unexpected request during registry construction")
		})},
	}
	registry, err := newProductionAdapterRegistryWithRuntime(
		root,
		entries,
		runtimeConfig,
	)
	if err != nil {
		t.Fatalf("construct DeepSeek registry: %v", err)
	}
	invoker, err := registry.ResolveExact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || invoker == nil {
		t.Fatalf("resolve exact DeepSeek adapter = %T, %v", invoker, err)
	}
	if resolverCalls != 0 || transportCalls != 0 {
		t.Fatalf(
			"DeepSeek startup used runtime capability: resolver=%d transport=%d",
			resolverCalls,
			transportCalls,
		)
	}
}

func TestProductionDeepSeekRegistryRejectsUntrustedIdentityAndArtifact(
	t *testing.T,
) {
	t.Parallel()
	assertion := deepSeekModelAssertion(t)
	provider := providerFromAssertion(assertion)
	runtimeConfig := &productionDeepSeekRuntimeConfig{
		APIKeyResolver: deepseekmodel.APIKeyResolverFunc(func(
			context.Context,
			deepseekmodel.APIKeyIdentity,
		) ([]byte, error) {
			return []byte("not-a-secret"), nil
		}),
	}
	for _, test := range []struct {
		name   string
		mutate func(*moduleapi.ActivatedModuleRef)
	}{
		{
			name: "digest",
			mutate: func(candidate *moduleapi.ActivatedModuleRef) {
				candidate.ArtifactDigest = strings.Repeat("a", moduleapi.SHA256HexLength)
			},
		},
		{
			name: "module id",
			mutate: func(candidate *moduleapi.ActivatedModuleRef) {
				candidate.ModuleID = "freeagent.builtin.model.deepseek.other"
			},
		},
		{
			name: "adapter identity",
			mutate: func(candidate *moduleapi.ActivatedModuleRef) {
				candidate.AdapterIdentity = "freeagent.adapter.model.deepseek.other/v1"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := provider
			test.mutate(&candidate)
			entries := []controlcontract.CatalogEntry{{
				Activation: candidate,
				Provides:   []moduleapi.PortRef{productionModelPort},
			}}
			if _, err := newProductionAdapterRegistryWithRuntime(
				t.TempDir(),
				entries,
				runtimeConfig,
			); err == nil || !strings.Contains(err.Error(), "compiled local trust set") {
				t.Fatalf("untrusted %s error = %v", test.name, err)
			}
		})
	}

	root := t.TempDir()
	if err := stageArtifact(root, assertion); err != nil {
		t.Fatalf("stage DeepSeek artifact: %v", err)
	}
	manifestPath := filepath.Join(
		root,
		provider.ArtifactDigest,
		moduleapi.ArtifactManifestPath,
	)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = bytes.Replace(
		manifest,
		[]byte(localDeepSeekEntrypoint),
		[]byte("freeagent.manifest-request.model.deepseeq/v1"),
		1,
	)
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []controlcontract.CatalogEntry{{
		Activation: provider,
		Provides:   []moduleapi.PortRef{productionModelPort},
	}}
	if _, err := newProductionAdapterRegistryWithRuntime(
		root,
		entries,
		runtimeConfig,
	); err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("tampered DeepSeek manifest error = %v", err)
	}
}

func TestProductionEchoRejectsUnusedDeepSeekRuntimeConfiguration(t *testing.T) {
	t.Parallel()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatal(err)
	}
	model := prepared.ModelAssertion()
	root := t.TempDir()
	if err := stageArtifact(root, model); err != nil {
		t.Fatal(err)
	}
	provider := providerFromAssertion(model)
	entries := []controlcontract.CatalogEntry{{
		Activation: provider,
		Provides:   []moduleapi.PortRef{productionModelPort},
	}}
	if _, err := newProductionAdapterRegistryWithRuntime(
		root,
		entries,
		&productionDeepSeekRuntimeConfig{},
	); err == nil || !strings.Contains(err.Error(), "Catalog selects Echo") {
		t.Fatalf("unused DeepSeek runtime error = %v", err)
	}
}

func TestExampleTextStatsArtifactLock(t *testing.T) {
	t.Parallel()
	digest, size, err := inspectArtifact(textStatsExampleArtifactPath(t))
	if err != nil {
		t.Fatalf("inspect text.stats artifact: %v", err)
	}
	if digest != localTextStatsDigest || size != 1936 {
		t.Fatalf("text.stats artifact lock = %s / %d", digest, size)
	}
}

func TestProductionCompositionDoesNotReadUnboundKnowledgeArtifact(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(ragExampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() error = %v", err)
	}
	var knowledge bootstrapseed.ModuleAssertion
	for _, assertion := range prepared.ModuleAssertions() {
		if assertion.ExpectedAdapterIdentity == localKnowledgeAdapterID {
			knowledge = assertion
			break
		}
	}
	if knowledge.ArtifactDigest == "" {
		t.Fatal("RAG seed has no knowledge assertion")
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     ragExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	publishControlWithoutKnowledgeBinding(
		t,
		databasePath,
		knowledge.InstanceID,
	)
	if err := os.Remove(filepath.Join(
		artifactRoot,
		knowledge.ArtifactDigest,
		"content",
		"source.json",
	)); err != nil {
		t.Fatalf("remove unbound knowledge source: %v", err)
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("unbound missing knowledge blocked startup: %v", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		knowledge.ArtifactDigest,
		knowledge.ExpectedAdapterIdentity,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("unbound knowledge registered/read = %v, %v", registered, err)
	}
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-no-rag-with-rag-catalog",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message:     "Catalog availability must not imply a RAG read.",
		RequestID:   "no-rag-with-rag-catalog-1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}
	result, err := composition.chat.Chat(ctx, input)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("no-RAG Chat() error = %v", err)
	}
	if result.TerminalResult == nil || result.Reply != input.Message {
		_ = composition.Close()
		t.Fatalf("no-RAG Chat() = %+v", result)
	}
	dispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("GetModelDispatchRecord() error = %v", err)
	}
	if dispatch.Attempt.ContextCompilation != nil {
		_ = composition.Close()
		t.Fatalf(
			"unbound knowledge produced compilation = %+v",
			dispatch.Attempt.ContextCompilation,
		)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		knowledge.ArtifactDigest,
		knowledge.ExpectedAdapterIdentity,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("no-RAG request loaded knowledge = %v, %v", registered, err)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close composition: %v", err)
	}
}

func TestProductionRegistrySharesExactModelAndContextAdaptersAcrossInstances(
	t *testing.T,
) {
	t.Parallel()

	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatal(err)
	}
	modelAssertion := prepared.ModelAssertion()
	knowledgeAssertion := newCompositionKnowledgeAssertion(t)
	root := t.TempDir()
	if err := stageArtifact(root, modelAssertion); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	if err := stageArtifact(root, knowledgeAssertion); err != nil {
		t.Fatalf("stage knowledge artifact: %v", err)
	}
	modelFirst := providerFromAssertion(modelAssertion)
	modelSecond := modelFirst
	modelSecond.InstanceID = "model-dev-echo-secondary"
	modelSecond.ActivationRevision = 2
	knowledgeFirst := providerFromAssertion(knowledgeAssertion)
	knowledgeSecond := knowledgeFirst
	knowledgeSecond.InstanceID = "knowledge-composition-secondary"
	knowledgeSecond.ActivationRevision = 2
	multipleModelEntries := []controlcontract.CatalogEntry{
		{Activation: modelFirst, Provides: []moduleapi.PortRef{productionModelPort}},
		{Activation: modelSecond, Provides: []moduleapi.PortRef{productionModelPort}},
		{Activation: knowledgeFirst, Provides: []moduleapi.PortRef{productionContextPort}},
		{Activation: knowledgeSecond, Provides: []moduleapi.PortRef{productionContextPort}},
	}
	if _, _, _, err := controlcontract.NewCatalogGeneration(
		controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          "catalog-multi-instance",
			Generation:            1,
			TenantID:              defaultTenantID,
			ControlSnapshotID:     "control-multi-instance",
			ControlSnapshotDigest: strings.Repeat("a", moduleapi.SHA256HexLength),
			Entries:               multipleModelEntries,
		},
	); err != nil {
		t.Fatalf("same artifact+adapter Catalog is invalid: %v", err)
	}
	differentModel := moduleapi.ActivatedModuleRef{
		ModuleID:           localDeepSeekModuleID,
		Version:            localDeepSeekVersion,
		ArtifactDigest:     localDeepSeekDigest,
		InstanceID:         "model-deepseek-other-artifact",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    deepseekmodel.AdapterIdentityV1,
		ActivationRevision: 1,
	}
	if _, err := exactLocalModelProvider([]controlcontract.CatalogEntry{
		{Activation: modelFirst, Provides: []moduleapi.PortRef{productionModelPort}},
		{Activation: differentModel, Provides: []moduleapi.PortRef{productionModelPort}},
	}); err == nil || !strings.Contains(err.Error(), "explicit multi-model composition") {
		t.Fatalf("different model artifact error = %v", err)
	}
	registry, err := newProductionAdapterRegistry(root, multipleModelEntries)
	if err != nil {
		t.Fatalf("multi-instance production registry: %v", err)
	}

	modelInvoker, err := registry.ResolveExact(
		context.Background(),
		modelFirst.ArtifactDigest,
		modelFirst.AdapterIdentity,
	)
	if err != nil {
		t.Fatalf("resolve shared model adapter: %v", err)
	}
	_, modelRequest, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: "multi-instance",
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []moduleapi.ActivatedModuleRef{modelFirst, modelSecond} {
		result, err := modelInvoker.Invoke(
			context.Background(),
			productionPreparedInvocation(productionModelPort, provider, modelRequest),
		)
		if err != nil || result.Provider != provider {
			t.Fatalf("model result=%+v err=%v", result, err)
		}
	}

	registered, err := registry.IsRegistered(
		context.Background(),
		knowledgeFirst.ArtifactDigest,
		knowledgeFirst.AdapterIdentity,
	)
	if err != nil || registered {
		t.Fatalf("knowledge was not lazy before use: %v, %v", registered, err)
	}
	knowledgeInvoker, err := registry.ResolveExact(
		context.Background(),
		knowledgeFirst.ArtifactDigest,
		knowledgeFirst.AdapterIdentity,
	)
	if err != nil {
		t.Fatalf("resolve shared knowledge adapter: %v", err)
	}
	sourceCanonical, err := os.ReadFile(filepath.Join(
		knowledgeAssertion.ArtifactDirectory,
		"content",
		"source.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		t.Fatal(err)
	}
	_, knowledgeRequest, _, err := moduleapi.NewKnowledgeContextRequestV1(
		moduleapi.KnowledgeContextRequestV1{
			SchemaVersion: moduleapi.KnowledgeContextRequestSchemaV1,
			Source:        sourceRef,
			Scope: moduleapi.KnowledgeQueryScopeV1{
				TenantID: defaultTenantID,
				Workspace: moduleapi.KnowledgeObjectRefV1{
					ID: "workspace", Version: "1", Digest: strings.Repeat("b", 64),
				},
				Agent: moduleapi.KnowledgeObjectRefV1{
					ID: "agent", Version: "1", Digest: strings.Repeat("c", 64),
				},
				TaskInputRef: strings.Repeat("d", 64),
			},
			QueryText:         "Composition registers knowledge",
			MaxHits:           2,
			MaxTotalTextBytes: 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []moduleapi.ActivatedModuleRef{
		knowledgeFirst,
		knowledgeSecond,
	} {
		result, err := knowledgeInvoker.Invoke(
			context.Background(),
			productionPreparedInvocation(
				productionContextPort,
				provider,
				knowledgeRequest,
			),
		)
		if err != nil || result.Provider != provider {
			t.Fatalf(
				"knowledge Instance %s result=%+v err=%v",
				provider.InstanceID,
				result,
				err,
			)
		}
	}
}

func TestProductionModuleLoaderPreservesCancellationCause(t *testing.T) {
	t.Parallel()

	loader := newProductionModuleLoader(t.TempDir())
	tests := []struct {
		name            string
		artifactDigest  string
		adapterIdentity string
	}{
		{
			name:            "knowledge",
			artifactDigest:  strings.Repeat("a", moduleapi.SHA256HexLength),
			adapterIdentity: localKnowledgeAdapterID,
		},
		{
			name:            "memory",
			artifactDigest:  strings.Repeat("b", moduleapi.SHA256HexLength),
			adapterIdentity: localMemoryAdapterID,
		},
		{
			name:            "text_stats",
			artifactDigest:  localTextStatsDigest,
			adapterIdentity: localTextStatsAdapterID,
		},
		{
			name:            "mcp_stdio",
			artifactDigest:  strings.Repeat("c", moduleapi.SHA256HexLength),
			adapterIdentity: mcpstdio.AdapterIdentityV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := newCancelAfterFirstErrCheckContext()
			t.Cleanup(cancel)
			invoker, err := loader(
				ctx,
				test.artifactDigest,
				test.adapterIdentity,
			)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("loader error = %v", err)
			}
			if invoker != nil {
				t.Fatalf("cancelled loader returned invoker = %T", invoker)
			}
		})
	}
}

func TestProductionCompositionLazilyLoadsExactKnowledgeAndRejectsArtifactDrift(
	t *testing.T,
) {
	t.Parallel()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() error = %v", err)
	}
	knowledge := newCompositionKnowledgeAssertion(t)
	assertions := append(prepared.ModuleAssertions(), knowledge)
	if _, err := newBootstrapActivationResolver(assertions); err != nil {
		t.Fatalf("newBootstrapActivationResolver() with knowledge error = %v", err)
	}

	root := t.TempDir()
	model := prepared.ModelAssertion()
	if err := stageArtifact(root, model); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	if err := stageArtifact(root, knowledge); err != nil {
		t.Fatalf("stage knowledge artifact: %v", err)
	}
	modelProvider := providerFromAssertion(model)
	knowledgeProvider := providerFromAssertion(knowledge)
	entries := []controlcontract.CatalogEntry{
		{Activation: modelProvider, Provides: []moduleapi.PortRef{productionModelPort}},
		{Activation: knowledgeProvider, Provides: []moduleapi.PortRef{productionContextPort}},
	}
	registry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("newProductionAdapterRegistry() with knowledge error = %v", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		knowledgeProvider.ArtifactDigest,
		knowledgeProvider.AdapterIdentity,
	)
	if err != nil || registered {
		t.Fatalf("knowledge was eagerly registered = %v, %v", registered, err)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		knowledgeProvider.ArtifactDigest,
		knowledgeProvider.AdapterIdentity,
	); err != nil {
		t.Fatalf("lazy exact knowledge resolution error = %v", err)
	}
	registered, err = registry.IsRegistered(
		context.Background(),
		knowledgeProvider.ArtifactDigest,
		knowledgeProvider.AdapterIdentity,
	)
	if err != nil || !registered {
		t.Fatalf("resolved knowledge registration = %v, %v", registered, err)
	}
	registered, err = registry.IsRegistered(
		context.Background(),
		knowledgeProvider.ArtifactDigest,
		"freeagent.adapter.knowledge.other/v1",
	)
	if err != nil || registered {
		t.Fatalf("fuzzy adapter identity registered = %v, %v", registered, err)
	}

	wrongDigest := knowledgeProvider
	wrongDigest.ArtifactDigest = strings.Repeat(
		"e",
		moduleapi.SHA256HexLength,
	)
	if _, err := newKnowledgeRegistrationFromArtifact(
		wrongDigest,
		knowledge.ArtifactDirectory,
		knowledge.ArtifactSizeBytes,
	); err == nil {
		t.Fatal("knowledge provider with wrong frozen digest was registered")
	}

	stagedSource := filepath.Join(
		root,
		knowledgeProvider.ArtifactDigest,
		"content",
		"source.json",
	)
	if err := os.Remove(stagedSource); err != nil {
		t.Fatalf("remove staged knowledge source: %v", err)
	}
	driftedRegistry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("missing unused knowledge blocked registry construction: %v", err)
	}
	if _, err := driftedRegistry.ResolveExact(
		context.Background(),
		knowledgeProvider.ArtifactDigest,
		knowledgeProvider.AdapterIdentity,
	); err == nil {
		t.Fatal("knowledge provider with missing source resolved")
	}
}

func publishControlWithoutKnowledgeBinding(
	t *testing.T,
	databasePath string,
	knowledgeInstanceID string,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for no-RAG publication: %v", err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("LoadPublishedBasis() error = %v", err)
	}
	removed := 0
	for profileIndex := range control.Profiles {
		bindings := control.Profiles[profileIndex].Bindings[:0]
		for _, binding := range control.Profiles[profileIndex].Bindings {
			if binding.InstanceID == knowledgeInstanceID {
				removed++
				continue
			}
			bindings = append(bindings, binding)
		}
		control.Profiles[profileIndex].Bindings = bindings
	}
	if removed != 1 {
		_ = store.Close()
		t.Fatalf("removed knowledge bindings = %d, want 1", removed)
	}
	if _, found := catalog.FindInstance(knowledgeInstanceID); !found {
		_ = store.Close()
		t.Fatal("knowledge Instance disappeared from Catalog")
	}
	control.SnapshotID = "control-local-s1-no-rag"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		_ = store.Close()
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}
	catalog.GenerationID = "catalog-local-s1-no-rag"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		_ = store.Close()
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	if _, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		_ = store.Close()
		t.Fatalf("PublishControlCatalog() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Store after no-RAG publication: %v", err)
	}
}

func productionPreparedInvocation(
	port moduleapi.PortRef,
	provider moduleapi.ActivatedModuleRef,
	input []byte,
) modulehost.PreparedInvocation {
	return modulehost.PreparedInvocation{
		Invocation: modulehost.ModuleInvocation{
			Port:  port,
			Input: bytes.Clone(input),
		},
		Binding: moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           strings.Repeat("e", moduleapi.SHA256HexLength),
			AuthorityCeilingRef: strings.Repeat("f", moduleapi.SHA256HexLength),
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	}
}

func newCompositionKnowledgeAssertion(
	t *testing.T,
) bootstrapseed.ModuleAssertion {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "knowledge-artifact")
	if err := os.MkdirAll(filepath.Join(directory, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	rule := moduleapi.KnowledgeScopeRuleV1{
		TenantID: "default", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
	}
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(moduleapi.KnowledgeChunkV1{
		Document: moduleapi.KnowledgeDocumentRefV1{
			ID:      "freeagent.test.composition",
			Version: "1",
			Digest: moduleapi.Digest(
				"freeagent.test.composition-document/v1",
				[]byte("composition"),
			),
		},
		ChunkID:   "registration",
		Text:      "Composition registers knowledge only by exact digest and adapter identity.",
		VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
	})
	if err != nil {
		t.Fatalf("NewKnowledgeChunkV1() error = %v", err)
	}
	_, sourceCanonical, _, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            "freeagent.test.composition-source",
			Version:       "1.0.0",
			Chunks:        []moduleapi.KnowledgeChunkV1{chunk},
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1() error = %v", err)
	}
	manifestInput, err := moduleapi.CanonicalJSON([]byte(
		`{"api_version":"freeagent.module/v1","id":"freeagent.test.knowledge.composition","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/source.json","mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"version":"1.0.0"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestInput)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "content", "source.json"),
		sourceCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("ScanArtifactDirectory() error = %v", err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatalf("ComputeArtifactDigest() error = %v", err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return bootstrapseed.ModuleAssertion{
		ModuleID:                "freeagent.test.knowledge.composition",
		ExactVersion:            "1.0.0",
		ArtifactDigest:          digest,
		ArtifactSizeBytes:       size,
		ArtifactDirectory:       directory,
		InstanceID:              "knowledge-composition",
		ActivationRevision:      1,
		ExpectedExecutionClass:  moduleapi.ExecutionTrustedInProcess,
		ExpectedAdapterIdentity: localKnowledgeAdapterID,
	}
}

func assertStagedArtifactIdentity(
	t *testing.T,
	target string,
	assertion bootstrapseed.ModuleAssertion,
) {
	t.Helper()
	digest, size, err := inspectArtifact(target)
	if err != nil {
		t.Fatalf("inspect staged artifact: %v", err)
	}
	if digest != assertion.ArtifactDigest || size != assertion.ArtifactSizeBytes {
		t.Fatalf(
			"staged identity = %s / %d, want %s / %d",
			digest,
			size,
			assertion.ArtifactDigest,
			assertion.ArtifactSizeBytes,
		)
	}
}

type cancelWhenPathExistsContext struct {
	context.Context

	cancel context.CancelFunc
	path   string
	once   sync.Once
}

func (ctx *cancelWhenPathExistsContext) Err() error {
	if _, err := os.Lstat(ctx.path); err == nil {
		ctx.once.Do(ctx.cancel)
	}
	return ctx.Context.Err()
}

type mutateWhenPathHasContentContext struct {
	context.Context

	path string
	once sync.Once
	mu   sync.Mutex
	err  error
}

func (ctx *mutateWhenPathHasContentContext) Err() error {
	if info, err := os.Stat(ctx.path); err == nil && info.Size() > 0 {
		ctx.once.Do(func() {
			file, openErr := os.OpenFile(ctx.path, os.O_WRONLY|os.O_APPEND, 0)
			if openErr == nil {
				_, writeErr := file.Write([]byte("changed-after-copy"))
				openErr = errors.Join(writeErr, file.Close())
			}
			ctx.mu.Lock()
			ctx.err = openErr
			ctx.mu.Unlock()
		})
	}
	return ctx.Context.Err()
}

func (ctx *mutateWhenPathHasContentContext) MutationError() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

type cancelAfterFirstErrCheckContext struct {
	context.Context

	mu        sync.Mutex
	cancelled bool
	cancel    context.CancelFunc
}

func newCancelAfterFirstErrCheckContext() (
	*cancelAfterFirstErrCheckContext,
	context.CancelFunc,
) {
	ctx, cancel := context.WithCancel(context.Background())
	return &cancelAfterFirstErrCheckContext{
		Context: ctx,
		cancel:  cancel,
	}, cancel
}

func (ctx *cancelAfterFirstErrCheckContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	err := ctx.Context.Err()
	if err == nil && !ctx.cancelled {
		ctx.cancelled = true
		ctx.cancel()
	}
	return err
}

func assertNoInitializationResidue(t *testing.T, root, databasePath string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".freeagent-current-") ||
			strings.HasPrefix(name, ".freeagent-artifacts-") ||
			strings.HasSuffix(name, ".init.lock") {
			t.Fatalf("initialization residue remains: %s", name)
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal", ".freeagent.owner.lock"} {
		if _, err := os.Lstat(databasePath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("database sidecar %s remains: %v", suffix, err)
		}
	}
}

func exampleSeedPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"current-v1.bootstrap.seed.json",
	)
}

func deepSeekExampleArtifactPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"bootstrap-artifacts",
		localDeepSeekModuleID,
		localDeepSeekVersion,
	)
}

func deepSeekModelAssertion(t *testing.T) bootstrapseed.ModuleAssertion {
	t.Helper()
	directory := deepSeekExampleArtifactPath(t)
	digest, size, err := inspectArtifact(directory)
	if err != nil {
		t.Fatalf("inspect DeepSeek example artifact: %v", err)
	}
	if digest != localDeepSeekDigest || size != localDeepSeekSize {
		t.Fatalf(
			"DeepSeek example artifact lock = %s / %d, want %s / %d",
			digest,
			size,
			localDeepSeekDigest,
			localDeepSeekSize,
		)
	}
	return bootstrapseed.ModuleAssertion{
		ModuleID:                localDeepSeekModuleID,
		ExactVersion:            localDeepSeekVersion,
		ArtifactDigest:          localDeepSeekDigest,
		ArtifactSizeBytes:       localDeepSeekSize,
		ArtifactDirectory:       directory,
		InstanceID:              "model-deepseek-production",
		ActivationRevision:      1,
		ExpectedExecutionClass:  moduleapi.ExecutionTrustedInProcess,
		ExpectedAdapterIdentity: deepseekmodel.AdapterIdentityV1,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func ragExampleSeedPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"current-v1.rag.bootstrap.seed.json",
	)
}

func actionExampleSeedPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"current-v1.action.bootstrap.seed.json",
	)
}

func textStatsExampleArtifactPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"bootstrap-artifacts",
		localTextStatsModuleID,
		localTextStatsVersion,
	)
}
