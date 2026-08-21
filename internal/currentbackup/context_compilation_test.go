package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
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

const backupTestJSONMediaType = "application/json"

type contextCompilationClosureSnapshot struct {
	AttemptState                 string
	AttemptContextCompilationRef string
	AttemptRequestRef            string
	AttemptRequestDigest         string
	CompilationDigest            string
	CompilationCanonical         []byte
	RequestDigest                string
	RequestCanonical             []byte
	ContentRows                  int
	CompilationRows              int
	RequestRows                  int
}

type countingKnowledgeInvoker struct {
	delegate modulehost.ModuleInvoker
	calls    atomic.Uint64
}

func (invoker *countingKnowledgeInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.calls.Add(1)
	return invoker.delegate.Invoke(ctx, prepared)
}

type ragBackupFixture struct {
	databasePath       string
	artifactRoot       string
	runID              string
	attemptID          string
	knowledgeAssertion bootstrapseed.ModuleAssertion
	sourceCanonical    []byte
	sourceRef          moduleapi.KnowledgeSourceRefV1
	sourceClosure      contextCompilationClosureSnapshot
	knowledgeInvoker   *countingKnowledgeInvoker
}

type routedRAGBackupFixture struct {
	databasePath      string
	artifactRoot      string
	runID             string
	attemptID         string
	sourceClosure     contextCompilationClosureSnapshot
	knowledgeInvokers []*countingKnowledgeInvoker
}

func TestFullBundleRoundTripPreservesContextCompilationClosureByteExactly(
	t *testing.T,
) {
	fixture := newBackupFixture(t)
	source := loadContextCompilationClosure(
		t,
		fixture.databasePath,
		fixture.pendingAttemptID,
	)
	assertContextCompilationClosureExact(t, source, fixture)

	bundle := filepath.Join(t.TempDir(), "context-compilation.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-context-compilation-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest = %s, want %s",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	); err != nil {
		t.Fatalf("VerifyCurrentStoreReadOnly(restored) error = %v", err)
	}

	// OpenExistingCurrentStore plus GetModelDispatchRecord is the supported
	// recovery read. It must return the persisted closure and never compile or
	// insert a replacement record while reopening the restored Store.
	restored := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.pendingAttemptID,
	)
	assertContextCompilationClosureExact(t, restored, fixture)
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf(
			"restored context closure differs:\nsource=%#v\nrestored=%#v",
			source,
			restored,
		)
	}
}

func TestFullBundleRoundTripPreservesRAGArtifactAndClosureWithoutRetrieval(
	t *testing.T,
) {
	fixture := newRAGBackupFixture(t)
	assertKnowledgeInvocationCount(t, fixture, 1, "source Run")
	assertRAGContextClosure(t, fixture.sourceClosure, fixture)

	bundle := filepath.Join(t.TempDir(), "rag.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-rag-closure-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	assertKnowledgeInvocationCount(t, fixture, 1, "CreateBundle")
	var knowledgeArtifact *Artifact
	for index := range manifest.Artifacts {
		if manifest.Artifacts[index].Digest ==
			fixture.knowledgeAssertion.ArtifactDigest {
			knowledgeArtifact = &manifest.Artifacts[index]
			break
		}
	}
	if knowledgeArtifact == nil ||
		knowledgeArtifact.SizeBytes !=
			int64(fixture.knowledgeAssertion.ArtifactSizeBytes) {
		t.Fatalf(
			"Knowledge artifact lock missing from bundle manifest: %+v",
			manifest.Artifacts,
		)
	}
	bundledSource := readRAGSourceArtifact(
		t,
		filepath.Join(
			bundle,
			artifactsDirectory,
			fixture.knowledgeAssertion.ArtifactDigest,
		),
	)
	if !bytes.Equal(bundledSource, fixture.sourceCanonical) {
		t.Fatal("bundled knowledge-source/v1 bytes changed")
	}

	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest = %s, want %s",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}
	assertKnowledgeInvocationCount(t, fixture, 1, "VerifyBundle")

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}
	assertKnowledgeInvocationCount(t, fixture, 1, "RestoreBundle")
	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	); err != nil {
		t.Fatalf("VerifyCurrentStoreReadOnly(restored) error = %v", err)
	}
	restoredArtifactDirectory := filepath.Join(
		restoredArtifacts,
		fixture.knowledgeAssertion.ArtifactDigest,
	)
	artifact, err := verifyArtifactDirectory(
		restoredArtifactDirectory,
		fixture.knowledgeAssertion.ArtifactDigest,
	)
	if err != nil || artifact.sizeBytes !=
		int64(fixture.knowledgeAssertion.ArtifactSizeBytes) {
		t.Fatalf("restored Knowledge artifact = %+v, %v", artifact, err)
	}
	restoredSource := readRAGSourceArtifact(t, restoredArtifactDirectory)
	if !bytes.Equal(restoredSource, fixture.sourceCanonical) {
		t.Fatal("restored knowledge-source/v1 bytes changed")
	}
	_, restoredSourceRef, err := moduleapi.RestoreKnowledgeSourceV1(
		restoredSource,
	)
	if err != nil || restoredSourceRef != fixture.sourceRef {
		t.Fatalf(
			"restored Knowledge source ref = %+v, want %+v, error=%v",
			restoredSourceRef,
			fixture.sourceRef,
			err,
		)
	}

	// This supported reopen/read path has no adapter registry. The frozen
	// compilation and request must be returned directly from Current Store;
	// backup, verification, restore and reopen must not invoke retrieval again.
	restoredClosure := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.attemptID,
	)
	assertKnowledgeInvocationCount(t, fixture, 1, "restored Store reopen")
	assertRAGContextClosure(t, restoredClosure, fixture)
	if !reflect.DeepEqual(restoredClosure, fixture.sourceClosure) {
		t.Fatalf(
			"restored RAG closure differs:\nsource=%#v\nrestored=%#v",
			fixture.sourceClosure,
			restoredClosure,
		)
	}
}

func TestFullBundleRoundTripPreservesKnowledgeShortcutClosureByteExactly(
	t *testing.T,
) {
	fixture := newRoutedRAGBackupFixture(t)
	assertRoutedKnowledgeInvocationCounts(t, fixture, "source Run")
	sourceCompilation := assertRoutedRAGContextClosure(
		t,
		fixture.sourceClosure,
	)

	bundle := filepath.Join(t.TempDir(), "routed-rag.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-routed-rag-closure-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	assertRoutedKnowledgeInvocationCounts(t, fixture, "CreateBundle")

	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest = %s, want %s",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}
	assertRoutedKnowledgeInvocationCounts(t, fixture, "VerifyBundle")

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}
	assertRoutedKnowledgeInvocationCounts(t, fixture, "RestoreBundle")
	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	); err != nil {
		t.Fatalf("VerifyCurrentStoreReadOnly(restored) error = %v", err)
	}

	// Reopening the restored Store must return the persisted retrieval/shortcut
	// union directly. Backup operations have neither a Provider registry nor
	// authority to turn NOT_SELECTED back into a retrieval call.
	restoredClosure := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.attemptID,
	)
	assertRoutedKnowledgeInvocationCounts(t, fixture, "restored Store reopen")
	restoredCompilation := assertRoutedRAGContextClosure(t, restoredClosure)
	if !bytes.Equal(
		restoredClosure.CompilationCanonical,
		fixture.sourceClosure.CompilationCanonical,
	) {
		t.Fatal("KnowledgeShortcut CONTEXT_COMPILATION canonical bytes changed")
	}
	if !reflect.DeepEqual(
		restoredCompilation.KnowledgeRetrievals,
		sourceCompilation.KnowledgeRetrievals,
	) || !reflect.DeepEqual(
		restoredCompilation.KnowledgeShortcuts,
		sourceCompilation.KnowledgeShortcuts,
	) {
		t.Fatalf(
			"restored retrieval/shortcut closure differs:\nsource=%+v/%+v\nrestored=%+v/%+v",
			sourceCompilation.KnowledgeRetrievals,
			sourceCompilation.KnowledgeShortcuts,
			restoredCompilation.KnowledgeRetrievals,
			restoredCompilation.KnowledgeShortcuts,
		)
	}
	if !reflect.DeepEqual(restoredClosure, fixture.sourceClosure) {
		t.Fatalf(
			"restored routed RAG closure differs:\nsource=%#v\nrestored=%#v",
			fixture.sourceClosure,
			restoredClosure,
		)
	}
}

func newPendingContextCompilation(
	t *testing.T,
	store *currentstore.Store,
	lease currentstore.RunLease,
	requestCanonical []byte,
) ([]byte, string, string) {
	t.Helper()
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop() error = %v", err)
	}
	policyContent, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found || policyContent.Kind != currentstore.ContentPolicy {
		t.Fatalf("frozen context policy content = %+v, found=%v", policyContent, found)
	}
	policyDocument, err := corecontract.RestorePolicyDocument(
		policyContent.CanonicalBytes,
		run.Member.ContextPolicy,
	)
	if err != nil {
		t.Fatalf("RestorePolicyDocument() error = %v", err)
	}
	if policyDocument.PolicyType != corecontract.PolicyContext {
		t.Fatalf("context policy type = %q", policyDocument.PolicyType)
	}
	policy, err := corecontract.RestoreContextPolicyV1(policyDocument.Body)
	if err != nil {
		t.Fatalf("RestoreContextPolicyV1() error = %v", err)
	}
	inputBudget, err := policy.InputBudgetTokens()
	if err != nil {
		t.Fatalf("InputBudgetTokens() error = %v", err)
	}
	restoreWatermark, err := policy.RestoreWatermarkTokens()
	if err != nil {
		t.Fatalf("RestoreWatermarkTokens() error = %v", err)
	}
	if restoreWatermark == 0 || restoreWatermark >= inputBudget {
		t.Fatalf(
			"fixture policy cannot form an under-budget high-water record: %d/%d",
			restoreWatermark,
			inputBudget,
		)
	}
	requestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		backupTestJSONMediaType,
		requestCanonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest(MODEL_REQUEST) error = %v", err)
	}
	_, canonical, err := corecontract.NewContextCompilationV1(
		corecontract.ContextCompilationV1{
			SchemaVersion:  corecontract.ContextCompilationSchemaVersionV1,
			WorkspaceScope: run.Member.Workspace,
			ContextPolicy:  run.Member.ContextPolicy,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
			SummaryAlgorithmVersion: corecontract.
				ContextSummaryHeadTailExtractiveV1,
			InputBudgetTokens:      inputBudget,
			RestoreWatermarkTokens: restoreWatermark,
			OriginalEstimateTokens: restoreWatermark,
			Drops:                  []corecontract.ContextCompilationDropV1{},
			FinalEstimateTokens:    restoreWatermark,
			StopReason: corecontract.
				ContextCompilationNoEligibleSummary,
			FinalRequestDigest: requestDigest,
		},
	)
	if err != nil {
		t.Fatalf("NewContextCompilationV1() error = %v", err)
	}
	compilationDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		backupTestJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest(CONTEXT_COMPILATION) error = %v", err)
	}
	return canonical, compilationDigest, requestDigest
}

func loadContextCompilationClosure(
	t *testing.T,
	databasePath string,
	attemptID string,
) contextCompilationClosureSnapshot {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore() error = %v", err)
	}
	record, err := store.GetModelDispatchRecord(ctx, attemptID)
	if err != nil {
		_ = store.Close()
		t.Fatalf("GetModelDispatchRecord() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if record.Attempt.ContextCompilation == nil {
		t.Fatal("Attempt lost context compilation")
	}

	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var contextRef sql.NullString
	var requestRef, requestDigest string
	if err := database.QueryRow(`
		SELECT context_compilation_ref, request_ref, request_digest
		FROM model_dispatch_attempts
		WHERE attempt_id=?
	`, attemptID).Scan(&contextRef, &requestRef, &requestDigest); err != nil {
		t.Fatal(err)
	}
	if !contextRef.Valid {
		t.Fatal("Attempt context_compilation_ref is NULL")
	}

	snapshot := contextCompilationClosureSnapshot{
		AttemptState:                 string(record.Attempt.State),
		AttemptContextCompilationRef: contextRef.String,
		AttemptRequestRef:            requestRef,
		AttemptRequestDigest:         requestDigest,
		CompilationDigest:            record.Attempt.ContextCompilation.Digest,
		CompilationCanonical: bytes.Clone(
			record.Attempt.ContextCompilation.CanonicalBytes,
		),
		RequestDigest:    record.Attempt.Request.Digest,
		RequestCanonical: bytes.Clone(record.Attempt.Request.CanonicalBytes),
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM content_records`).Scan(
		&snapshot.ContentRows,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM content_records WHERE kind='CONTEXT_COMPILATION'
	`).Scan(&snapshot.CompilationRows); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM content_records WHERE kind='MODEL_REQUEST'
	`).Scan(&snapshot.RequestRows); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertContextCompilationClosureExact(
	t *testing.T,
	snapshot contextCompilationClosureSnapshot,
	fixture backupFixture,
) {
	t.Helper()
	if snapshot.AttemptState != string(corecontract.ModelAttemptPending) {
		t.Fatalf("Attempt state = %q", snapshot.AttemptState)
	}
	if snapshot.AttemptContextCompilationRef !=
		fixture.pendingContextCompilationDigest ||
		snapshot.CompilationDigest != fixture.pendingContextCompilationDigest {
		t.Fatalf(
			"context compilation ref/digest = %q/%q, want %q",
			snapshot.AttemptContextCompilationRef,
			snapshot.CompilationDigest,
			fixture.pendingContextCompilationDigest,
		)
	}
	if !bytes.Equal(
		snapshot.CompilationCanonical,
		fixture.pendingContextCompilationCanonical,
	) {
		t.Fatal("CONTEXT_COMPILATION canonical bytes changed")
	}
	if snapshot.AttemptRequestRef != fixture.pendingRequestDigest ||
		snapshot.AttemptRequestDigest != fixture.pendingRequestDigest ||
		snapshot.RequestDigest != fixture.pendingRequestDigest {
		t.Fatalf(
			"MODEL_REQUEST ref/digests = %q/%q/%q, want %q",
			snapshot.AttemptRequestRef,
			snapshot.AttemptRequestDigest,
			snapshot.RequestDigest,
			fixture.pendingRequestDigest,
		)
	}
	if !bytes.Equal(snapshot.RequestCanonical, fixture.pendingRequestCanonical) {
		t.Fatal("MODEL_REQUEST canonical bytes changed")
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		snapshot.CompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1() error = %v", err)
	}
	if compilation.StopReason !=
		corecontract.ContextCompilationNoEligibleSummary ||
		compilation.OriginalEstimateTokens <
			compilation.RestoreWatermarkTokens ||
		compilation.OriginalEstimateTokens >= compilation.InputBudgetTokens {
		t.Fatalf("restored compilation is not the high-water fixture: %+v", compilation)
	}
	if snapshot.CompilationRows != 2 || snapshot.RequestRows != 2 {
		t.Fatalf(
			"content row counts compilation/request = %d/%d, want 2/2",
			snapshot.CompilationRows,
			snapshot.RequestRows,
		)
	}
}

func newRAGBackupFixture(t *testing.T) ragBackupFixture {
	t.Helper()
	ctx := context.Background()
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.rag.bootstrap.seed.json",
	)
	prepared, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile(RAG example) error = %v", err)
	}
	modelAssertion := prepared.ModelAssertion()
	assertions := prepared.ModuleAssertions()
	var knowledgeAssertion bootstrapseed.ModuleAssertion
	knowledgeCount := 0
	for _, assertion := range assertions {
		if assertion.ExpectedAdapterIdentity ==
			"freeagent.adapter.knowledge.lexical/v1" {
			knowledgeAssertion = assertion
			knowledgeCount++
		}
	}
	if knowledgeCount != 1 {
		t.Fatalf("RAG example Knowledge assertion count = %d", knowledgeCount)
	}
	sourceCanonical := readRAGSourceArtifact(
		t,
		knowledgeAssertion.ArtifactDirectory,
	)
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeSourceV1() error = %v", err)
	}

	modelProvider := activatedModuleFromAssertion(modelAssertion)
	knowledgeProvider := activatedModuleFromAssertion(knowledgeAssertion)
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := exactadapter.NewDeterministicKnowledge(
		knowledgeProvider,
		sourceCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	countingKnowledge := &countingKnowledgeInvoker{delegate: knowledge}
	registry, err := exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         echo,
		},
		exactadapter.Registration{
			ArtifactDigest:  knowledgeProvider.ArtifactDigest,
			AdapterIdentity: knowledgeProvider.AdapterIdentity,
			Invoker:         countingKnowledge,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	allowlist := make(
		[]activationresolver.TrustedInProcessAllowlistEntry,
		0,
		2,
	)
	for _, assertion := range assertions {
		if assertion.ExpectedExecutionClass !=
			moduleapi.ExecutionTrustedInProcess {
			continue
		}
		allowlist = append(
			allowlist,
			activationresolver.TrustedInProcessAllowlistEntry{
				ModuleID:        assertion.ModuleID,
				ExactVersion:    assertion.ExactVersion,
				ArtifactDigest:  assertion.ArtifactDigest,
				AdapterIdentity: assertion.ExpectedAdapterIdentity,
			},
		)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist:  allowlist,
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
		t.Fatalf("RAG seed Import() error = %v", err)
	}

	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, assertion := range assertions {
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

	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewChatService(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	assembly := prepared.DefaultAssembly()
	chat, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "currentbackup-rag-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message: "How does FreeAgent shared knowledge relate to Agent and " +
			"Workspace definitions?",
		RequestID: "currentbackup-rag-request",
		Deadline: time.Now().UTC().Add(2 * time.Hour).Truncate(
			time.Microsecond,
		),
	})
	if err != nil {
		t.Fatalf("RAG Chat() error = %v", err)
	}
	if chat.LoopResult.Disposition != loopapi.DispositionTerminated ||
		chat.LoopResult.ReasonCode != "MODEL_SUCCEEDED" {
		t.Fatalf("RAG Chat() = %+v", chat)
	}
	if countingKnowledge.calls.Load() != 1 {
		t.Fatalf(
			"source RAG Run invoked Knowledge %d times, want 1",
			countingKnowledge.calls.Load(),
		)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	attemptID := modelAttemptIDForRun(t, databasePath, chat.RunID)
	sourceClosure := loadContextCompilationClosure(
		t,
		databasePath,
		attemptID,
	)
	return ragBackupFixture{
		databasePath:       databasePath,
		artifactRoot:       artifactRoot,
		runID:              chat.RunID,
		attemptID:          attemptID,
		knowledgeAssertion: knowledgeAssertion,
		sourceCanonical:    bytes.Clone(sourceCanonical),
		sourceRef:          sourceRef,
		sourceClosure:      sourceClosure,
		knowledgeInvoker:   countingKnowledge,
	}
}

func newRoutedRAGBackupFixture(t *testing.T) routedRAGBackupFixture {
	t.Helper()
	ctx := context.Background()
	prepared := prepareRoutedRAGSeed(t)
	modelAssertion := prepared.ModelAssertion()
	assertions := prepared.ModuleAssertions()
	knowledgeAssertions := make([]bootstrapseed.ModuleAssertion, 0, 2)
	for _, assertion := range assertions {
		if assertion.ExpectedAdapterIdentity ==
			"freeagent.adapter.knowledge.lexical/v1" {
			knowledgeAssertions = append(knowledgeAssertions, assertion)
		}
	}
	if len(knowledgeAssertions) != 2 {
		t.Fatalf(
			"routed RAG example Knowledge assertion count = %d, want 2",
			len(knowledgeAssertions),
		)
	}

	modelProvider := activatedModuleFromAssertion(modelAssertion)
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	registrations := []exactadapter.Registration{{
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
		Invoker:         echo,
	}}
	knowledgeInvokers := make([]*countingKnowledgeInvoker, 0, 2)
	for _, assertion := range knowledgeAssertions {
		provider := activatedModuleFromAssertion(assertion)
		sourceCanonical := readRAGSourceArtifact(
			t,
			assertion.ArtifactDirectory,
		)
		knowledge, err := exactadapter.NewDeterministicKnowledge(
			provider,
			sourceCanonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		counting := &countingKnowledgeInvoker{delegate: knowledge}
		knowledgeInvokers = append(knowledgeInvokers, counting)
		registrations = append(registrations, exactadapter.Registration{
			ArtifactDigest:  provider.ArtifactDigest,
			AdapterIdentity: provider.AdapterIdentity,
			Invoker:         counting,
		})
	}
	registry, err := exactadapter.NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	allowlist := make(
		[]activationresolver.TrustedInProcessAllowlistEntry,
		0,
		len(assertions),
	)
	for _, assertion := range assertions {
		if assertion.ExpectedExecutionClass !=
			moduleapi.ExecutionTrustedInProcess {
			continue
		}
		allowlist = append(
			allowlist,
			activationresolver.TrustedInProcessAllowlistEntry{
				ModuleID:        assertion.ModuleID,
				ExactVersion:    assertion.ExactVersion,
				ArtifactDigest:  assertion.ArtifactDigest,
				AdapterIdentity: assertion.ExpectedAdapterIdentity,
			},
		)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist:  allowlist,
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
		t.Fatalf("routed RAG seed Import() error = %v", err)
	}

	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, assertion := range assertions {
		verified, err := verifyArtifactDirectory(
			assertion.ArtifactDirectory,
			assertion.ArtifactDigest,
		)
		if err != nil {
			t.Fatalf(
				"verify routed seed artifact %s: %v",
				assertion.ArtifactDigest,
				err,
			)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf(
				"copy routed seed artifact %s: %v",
				assertion.ArtifactDigest,
				err,
			)
		}
	}

	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewChatService(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	assembly := prepared.DefaultAssembly()
	chat, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "currentbackup-routed-rag-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message: "How does FreeAgent shared knowledge relate to Agent and " +
			"Workspace definitions?",
		RequestID: "currentbackup-routed-rag-request",
		Deadline: time.Now().UTC().Add(2 * time.Hour).Truncate(
			time.Microsecond,
		),
	})
	if err != nil {
		t.Fatalf("routed RAG Chat() error = %v", err)
	}
	if chat.LoopResult.Disposition != loopapi.DispositionTerminated ||
		chat.LoopResult.ReasonCode != "MODEL_SUCCEEDED" {
		t.Fatalf("routed RAG Chat() = %+v", chat)
	}
	if knowledgeInvokers[0].calls.Load() != 1 ||
		knowledgeInvokers[1].calls.Load() != 0 {
		t.Fatalf(
			"source routed RAG Run invoked Knowledge %d/%d times, want 1/0",
			knowledgeInvokers[0].calls.Load(),
			knowledgeInvokers[1].calls.Load(),
		)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	attemptID := modelAttemptIDForRun(t, databasePath, chat.RunID)
	return routedRAGBackupFixture{
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		runID:        chat.RunID,
		attemptID:    attemptID,
		sourceClosure: loadContextCompilationClosure(
			t,
			databasePath,
			attemptID,
		),
		knowledgeInvokers: knowledgeInvokers,
	}
}

func prepareRoutedRAGSeed(t *testing.T) *bootstrapseed.Prepared {
	t.Helper()
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.rag.bootstrap.seed.json",
	)
	original, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile(RAG example) error = %v", err)
	}
	artifactBase := t.TempDir()
	seedBase := filepath.Dir(seedPath)
	for _, assertion := range original.ModuleAssertions() {
		relative, err := filepath.Rel(seedBase, assertion.ArtifactDirectory)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			t.Fatalf(
				"RAG artifact path %q is outside seed base %q: %v",
				assertion.ArtifactDirectory,
				seedBase,
				err,
			)
		}
		destination := filepath.Join(artifactBase, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		verified, err := verifyArtifactDirectory(
			assertion.ArtifactDirectory,
			assertion.ArtifactDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := copyVerifiedArtifact(
			context.Background(),
			assertion.ArtifactDirectory,
			destination,
			verified,
		); err != nil {
			t.Fatalf("copy RAG fixture artifact: %v", err)
		}
	}

	seedCanonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	decoder := json.NewDecoder(bytes.NewReader(seedCanonical))
	decoder.UseNumber()
	if err := decoder.Decode(&seed); err != nil {
		t.Fatal(err)
	}
	providers, ok := seed["knowledge_context_providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("RAG seed Knowledge providers = %#v", seed["knowledge_context_providers"])
	}
	selected, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("RAG seed Knowledge provider = %#v", providers[0])
	}
	setRoutedRAGPolicy(t, selected, "shared", "freeagent")
	skipped := cloneRoutedRAGJSONMap(t, selected)
	setRoutedRAGPolicy(t, skipped, "frontend", "css")

	moduleID := "freeagent.example.knowledge.skipped"
	moduleVersion := "1.0.0"
	relativeArtifact := filepath.ToSlash(filepath.Join(
		"bootstrap-artifacts",
		moduleID,
		moduleVersion,
	))
	artifactDirectory := filepath.Join(
		artifactBase,
		filepath.FromSlash(relativeArtifact),
	)
	if err := os.MkdirAll(
		filepath.Join(artifactDirectory, "content"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleID,
		Version:    moduleVersion,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides: []moduleapi.PortRef{{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		}},
	}
	manifestJSON, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical); err != nil {
		t.Fatalf("generated routed RAG manifest: %v", err)
	}
	sourceCanonical := readRAGSourceArtifact(
		t,
		original.ModuleAssertions()[2].ArtifactDirectory,
	)
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, "content", "source.json"),
		sourceCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		files,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactSize := uint64(len(manifestCanonical))
	for _, file := range files {
		artifactSize += uint64(len(file.Content))
	}
	module, ok := skipped["module"].(map[string]any)
	if !ok {
		t.Fatalf("cloned routed RAG module = %#v", skipped["module"])
	}
	module["artifact_digest"] = artifactDigest
	module["artifact_relative_path"] = relativeArtifact
	module["artifact_size_bytes"] = artifactSize
	module["installation_id"] = "installation-freeagent-example-knowledge-skipped-1"
	module["instance_id"] = "knowledge-skipped"
	module["module_id"] = moduleID
	seed["knowledge_context_providers"] = []any{selected, skipped}

	seedJSON, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	routedCanonical, err := moduleapi.CanonicalJSON(seedJSON)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bootstrapseed.Prepare(routedCanonical, artifactBase)
	if err != nil {
		t.Fatalf("Prepare(routed RAG seed) error = %v", err)
	}
	return prepared
}

func setRoutedRAGPolicy(
	t *testing.T,
	provider map[string]any,
	collectionTag string,
	matchTerm string,
) {
	t.Helper()
	config, ok := provider["config"].(map[string]any)
	if !ok {
		t.Fatalf("routed RAG config = %#v", provider["config"])
	}
	parameters, ok := config["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("routed RAG parameters = %#v", config["parameters"])
	}
	parameters["routing"] = map[string]any{
		"collection_tags": []any{collectionTag},
		"match_terms":     []any{matchTerm},
		"min_match_terms": json.Number("1"),
		"schema_version":  moduleapi.KnowledgeRoutingPolicySchemaV1,
	}
}

func cloneRoutedRAGJSONMap(
	t *testing.T,
	value map[string]any,
) map[string]any {
	t.Helper()
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var cloned map[string]any
	if err := decoder.Decode(&cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func activatedModuleFromAssertion(
	assertion bootstrapseed.ModuleAssertion,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           assertion.ModuleID,
		Version:            assertion.ExactVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         assertion.InstanceID,
		ExecutionClass:     assertion.ExpectedExecutionClass,
		AdapterIdentity:    assertion.ExpectedAdapterIdentity,
		ActivationRevision: assertion.ActivationRevision,
	}
}

func readRAGSourceArtifact(t *testing.T, artifactDirectory string) []byte {
	t.Helper()
	canonical, err := os.ReadFile(filepath.Join(
		artifactDirectory,
		"content",
		"source.json",
	))
	if err != nil {
		t.Fatalf("read RAG source artifact: %v", err)
	}
	return canonical
}

func modelAttemptIDForRun(
	t *testing.T,
	databasePath string,
	runID string,
) string {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	var attemptID sql.NullString
	if err := database.QueryRow(`
		SELECT COUNT(*), MIN(attempt_id)
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, runID).Scan(&count, &attemptID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !attemptID.Valid {
		t.Fatalf("Run %s model attempts = %d/%+v", runID, count, attemptID)
	}
	return attemptID.String
}

func assertKnowledgeInvocationCount(
	t *testing.T,
	fixture ragBackupFixture,
	want uint64,
	stage string,
) {
	t.Helper()
	if got := fixture.knowledgeInvoker.calls.Load(); got != want {
		t.Fatalf(
			"Knowledge invocation count after %s = %d, want %d",
			stage,
			got,
			want,
		)
	}
}

func assertRAGContextClosure(
	t *testing.T,
	snapshot contextCompilationClosureSnapshot,
	fixture ragBackupFixture,
) {
	t.Helper()
	if snapshot.AttemptState != string(corecontract.ModelAttemptSucceeded) {
		t.Fatalf("RAG Attempt state = %q", snapshot.AttemptState)
	}
	if snapshot.AttemptContextCompilationRef != snapshot.CompilationDigest {
		t.Fatalf(
			"RAG compilation ref/digest = %q/%q",
			snapshot.AttemptContextCompilationRef,
			snapshot.CompilationDigest,
		)
	}
	compilationDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		backupTestJSONMediaType,
		snapshot.CompilationCanonical,
	)
	if err != nil || compilationDigest != snapshot.CompilationDigest {
		t.Fatalf(
			"RAG compilation digest = %q, want %q, error=%v",
			compilationDigest,
			snapshot.CompilationDigest,
			err,
		)
	}
	if snapshot.AttemptRequestRef != snapshot.RequestDigest ||
		snapshot.AttemptRequestDigest != snapshot.RequestDigest {
		t.Fatalf(
			"RAG request refs/digest = %q/%q/%q",
			snapshot.AttemptRequestRef,
			snapshot.AttemptRequestDigest,
			snapshot.RequestDigest,
		)
	}
	requestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		backupTestJSONMediaType,
		snapshot.RequestCanonical,
	)
	if err != nil || requestDigest != snapshot.RequestDigest {
		t.Fatalf(
			"RAG MODEL_REQUEST digest = %q, want %q, error=%v",
			requestDigest,
			snapshot.RequestDigest,
			err,
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		snapshot.RequestCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1() error = %v", err)
	}
	containsKnowledgeEnvelope := false
	for _, message := range request.Messages {
		if message.Role == moduleapi.ModelRoleUser &&
			strings.HasPrefix(
				message.Content,
				"UNTRUSTED_CONTEXT_DATA_JSON:\n",
			) {
			containsKnowledgeEnvelope = true
			break
		}
	}
	if !containsKnowledgeEnvelope {
		t.Fatal("restored MODEL_REQUEST lost untrusted Knowledge envelope")
	}

	compilation, err := corecontract.RestoreContextCompilationV1(
		snapshot.CompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1() error = %v", err)
	}
	_, rebuiltCompilation, err := corecontract.NewContextCompilationV1(
		compilation,
	)
	if err != nil || !bytes.Equal(
		rebuiltCompilation,
		snapshot.CompilationCanonical,
	) {
		t.Fatalf("RAG compilation canonical rebuild error=%v", err)
	}
	if compilation.StopReason !=
		corecontract.ContextCompilationRetrievalBelowWatermark ||
		compilation.FinalRequestDigest != snapshot.RequestDigest ||
		len(compilation.KnowledgeRetrievals) != 1 {
		t.Fatalf("RAG compilation closure = %+v", compilation)
	}
	evidence := compilation.KnowledgeRetrievals[0]
	if evidence.Source != fixture.sourceRef ||
		!moduleapi.ValidSHA256(evidence.ConfigRef) ||
		!moduleapi.ValidSHA256(evidence.AuthorityCeilingRef) ||
		!moduleapi.ValidSHA256(evidence.RequestDigest) ||
		!moduleapi.ValidSHA256(evidence.OutputDigest) {
		t.Fatalf("RAG retrieval evidence refs = %+v", evidence)
	}
	source, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(
		fixture.sourceCanonical,
	)
	if err != nil || sourceRef != fixture.sourceRef {
		t.Fatalf("fixture Knowledge source ref = %+v, error=%v", sourceRef, err)
	}
	if len(source.Chunks) != 1 || len(evidence.Hits) != 1 {
		t.Fatalf(
			"source chunks/evidence hits = %d/%d",
			len(source.Chunks),
			len(evidence.Hits),
		)
	}
	hit := evidence.Hits[0]
	chunk := source.Chunks[0]
	if hit.Document != chunk.Document ||
		hit.ChunkID != chunk.ChunkID ||
		hit.ChunkDigest != chunk.ChunkDigest ||
		hit.Text != chunk.Text ||
		!reflect.DeepEqual(hit.VisibleTo, chunk.VisibleTo) {
		t.Fatalf("RAG hit does not close source chunk: %+v / %+v", hit, chunk)
	}
	_, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: evidence.RequestDigest,
			Source:        evidence.Source,
			Hits:          evidence.Hits,
		},
	)
	if err != nil || outputDigest != evidence.OutputDigest {
		t.Fatalf(
			"RAG output digest = %q, want %q, error=%v",
			outputDigest,
			evidence.OutputDigest,
			err,
		)
	}
	if snapshot.CompilationRows != 1 || snapshot.RequestRows != 1 {
		t.Fatalf(
			"RAG content row counts compilation/request = %d/%d, want 1/1",
			snapshot.CompilationRows,
			snapshot.RequestRows,
		)
	}
}

func assertRoutedKnowledgeInvocationCounts(
	t *testing.T,
	fixture routedRAGBackupFixture,
	stage string,
) {
	t.Helper()
	if len(fixture.knowledgeInvokers) != 2 {
		t.Fatalf(
			"Knowledge invoker count after %s = %d, want 2",
			stage,
			len(fixture.knowledgeInvokers),
		)
	}
	selected := fixture.knowledgeInvokers[0].calls.Load()
	skipped := fixture.knowledgeInvokers[1].calls.Load()
	if selected != 1 || skipped != 0 {
		t.Fatalf(
			"Knowledge invocation count after %s = %d/%d, want 1/0",
			stage,
			selected,
			skipped,
		)
	}
}

func assertRoutedRAGContextClosure(
	t *testing.T,
	snapshot contextCompilationClosureSnapshot,
) corecontract.ContextCompilationV1 {
	t.Helper()
	if snapshot.AttemptState != string(corecontract.ModelAttemptSucceeded) {
		t.Fatalf("routed RAG Attempt state = %q", snapshot.AttemptState)
	}
	if snapshot.AttemptContextCompilationRef != snapshot.CompilationDigest {
		t.Fatalf(
			"routed RAG compilation ref/digest = %q/%q",
			snapshot.AttemptContextCompilationRef,
			snapshot.CompilationDigest,
		)
	}
	compilationDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		backupTestJSONMediaType,
		snapshot.CompilationCanonical,
	)
	if err != nil || compilationDigest != snapshot.CompilationDigest {
		t.Fatalf(
			"routed RAG compilation digest = %q, want %q, error=%v",
			compilationDigest,
			snapshot.CompilationDigest,
			err,
		)
	}
	if snapshot.AttemptRequestRef != snapshot.RequestDigest ||
		snapshot.AttemptRequestDigest != snapshot.RequestDigest {
		t.Fatalf(
			"routed RAG request refs/digest = %q/%q/%q",
			snapshot.AttemptRequestRef,
			snapshot.AttemptRequestDigest,
			snapshot.RequestDigest,
		)
	}
	requestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		backupTestJSONMediaType,
		snapshot.RequestCanonical,
	)
	if err != nil || requestDigest != snapshot.RequestDigest {
		t.Fatalf(
			"routed RAG MODEL_REQUEST digest = %q, want %q, error=%v",
			requestDigest,
			snapshot.RequestDigest,
			err,
		)
	}
	if bytes.Contains(snapshot.RequestCanonical, []byte("css")) {
		t.Fatal("NOT_SELECTED Knowledge content entered the MODEL_REQUEST")
	}

	compilation, err := corecontract.RestoreContextCompilationV1(
		snapshot.CompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1() error = %v", err)
	}
	_, rebuiltCompilation, err := corecontract.NewContextCompilationV1(
		compilation,
	)
	if err != nil || !bytes.Equal(
		rebuiltCompilation,
		snapshot.CompilationCanonical,
	) {
		t.Fatalf("routed RAG compilation canonical rebuild error=%v", err)
	}
	if compilation.StopReason !=
		corecontract.ContextCompilationRetrievalBelowWatermark ||
		compilation.FinalRequestDigest != snapshot.RequestDigest ||
		len(compilation.KnowledgeRetrievals) != 1 ||
		len(compilation.KnowledgeShortcuts) != 1 {
		t.Fatalf("routed RAG compilation closure = %+v", compilation)
	}
	retrieval := compilation.KnowledgeRetrievals[0]
	shortcut := compilation.KnowledgeShortcuts[0]
	if retrieval.BindingIndex == shortcut.BindingIndex ||
		!moduleapi.ValidSHA256(retrieval.ConfigRef) ||
		!moduleapi.ValidSHA256(retrieval.AuthorityCeilingRef) ||
		!moduleapi.ValidSHA256(retrieval.RequestDigest) ||
		!moduleapi.ValidSHA256(retrieval.OutputDigest) ||
		len(retrieval.Hits) != 1 {
		t.Fatalf("routed RAG retrieval evidence = %+v", retrieval)
	}
	if !moduleapi.ValidSHA256(shortcut.ConfigRef) ||
		!moduleapi.ValidSHA256(shortcut.AuthorityCeilingRef) ||
		!moduleapi.ValidSHA256(shortcut.DecisionSetDigest) ||
		!moduleapi.ValidSHA256(shortcut.ExactQuestionFingerprint) ||
		shortcut.Source != retrieval.Source ||
		shortcut.Scope != retrieval.Scope ||
		!reflect.DeepEqual(shortcut.CollectionTags, []string{"frontend"}) ||
		len(shortcut.MatchedTerms) != 0 || shortcut.MinMatchTerms != 1 ||
		shortcut.Mode != corecontract.KnowledgeShortcutNotSelectedV1 {
		t.Fatalf("routed RAG shortcut evidence = %+v", shortcut)
	}
	if snapshot.CompilationRows != 1 || snapshot.RequestRows != 1 {
		t.Fatalf(
			"routed RAG content row counts compilation/request = %d/%d, want 1/1",
			snapshot.CompilationRows,
			snapshot.RequestRows,
		)
	}
	return compilation
}
