package currentbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type knowledgeReuseBackupFixture struct {
	databasePath     string
	artifactRoot     string
	conversationID   string
	sourceAttemptID  string
	reusedAttemptID  string
	sourceClosure    contextCompilationClosureSnapshot
	reusedClosure    contextCompilationClosureSnapshot
	knowledgeInvoker *countingKnowledgeInvoker
}

func TestFullBundleRoundTripPreservesKnowledgeReuseClosureByteExactly(
	t *testing.T,
) {
	fixture := newKnowledgeReuseBackupFixture(t)
	assertKnowledgeReuseBackupClosure(t, fixture)
	assertKnowledgeReuseInvocationCount(t, fixture, 1, "source and reuse Runs")

	bundle := filepath.Join(t.TempDir(), "knowledge-reuse.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-knowledge-reuse-closure-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	assertKnowledgeReuseInvocationCount(t, fixture, 1, "CreateBundle")

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
	assertKnowledgeReuseInvocationCount(t, fixture, 1, "VerifyBundle")

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
	assertKnowledgeReuseInvocationCount(t, fixture, 1, "RestoreBundle")
	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	); err != nil {
		t.Fatalf("VerifyCurrentStoreReadOnly(restored) error = %v", err)
	}

	// The supported reopen path must load both the earlier fresh retrieval and
	// the later reuse record directly. Backup has no Provider registry and must
	// never turn the reuse evidence into a new retrieval.
	restoredSource := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.sourceAttemptID,
	)
	restoredReuse := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.reusedAttemptID,
	)
	assertKnowledgeReuseInvocationCount(t, fixture, 1, "restored Store reopen")
	if !reflect.DeepEqual(restoredSource, fixture.sourceClosure) {
		t.Fatalf(
			"restored fresh source closure differs:\nsource=%#v\nrestored=%#v",
			fixture.sourceClosure,
			restoredSource,
		)
	}
	if !reflect.DeepEqual(restoredReuse, fixture.reusedClosure) {
		t.Fatalf(
			"restored reuse closure differs:\nsource=%#v\nrestored=%#v",
			fixture.reusedClosure,
			restoredReuse,
		)
	}
	assertKnowledgeReuseBackupClosure(t, knowledgeReuseBackupFixture{
		conversationID:   fixture.conversationID,
		sourceAttemptID:  fixture.sourceAttemptID,
		reusedAttemptID:  fixture.reusedAttemptID,
		sourceClosure:    restoredSource,
		reusedClosure:    restoredReuse,
		knowledgeInvoker: fixture.knowledgeInvoker,
	})
}

func newKnowledgeReuseBackupFixture(t *testing.T) knowledgeReuseBackupFixture {
	t.Helper()
	ctx := context.Background()
	prepared := prepareKnowledgeReuseBackupSeed(t)
	modelAssertion := prepared.ModelAssertion()
	assertions := prepared.ModuleAssertions()

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
	var countingKnowledge *countingKnowledgeInvoker
	knowledgeCount := 0
	memoryCount := 0
	for _, assertion := range assertions {
		provider := activatedModuleFromAssertion(assertion)
		switch assertion.ExpectedAdapterIdentity {
		case "freeagent.adapter.knowledge.lexical/v1":
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
			countingKnowledge = &countingKnowledgeInvoker{delegate: knowledge}
			registrations = append(registrations, exactadapter.Registration{
				ArtifactDigest:  provider.ArtifactDigest,
				AdapterIdentity: provider.AdapterIdentity,
				Invoker:         countingKnowledge,
			})
			knowledgeCount++
		case "freeagent.adapter.memory.deterministic/v1":
			memory, err := exactadapter.NewDeterministicMemory(provider)
			if err != nil {
				t.Fatal(err)
			}
			registrations = append(registrations, exactadapter.Registration{
				ArtifactDigest:  provider.ArtifactDigest,
				AdapterIdentity: provider.AdapterIdentity,
				Invoker:         memory,
			})
			memoryCount++
		}
	}
	if knowledgeCount != 1 || memoryCount != 1 || countingKnowledge == nil {
		t.Fatalf(
			"knowledge/memory assertion counts = %d/%d",
			knowledgeCount,
			memoryCount,
		)
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

	databasePath := filepath.Join(t.TempDir(), "knowledge-reuse.sqlite")
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
		t.Fatalf("Knowledge reuse seed Import() error = %v", err)
	}
	assembly := prepared.DefaultAssembly()
	const (
		conversationID = "backup-knowledge-reuse-conversation"
		principalID    = "backup-knowledge-reuse-principal"
		question       = "How does freeagent backend api shared knowledge work?"
	)
	if _, err := store.CreateConversation(ctx, currentstore.CreateConversationInput{
		ConversationID: conversationID,
		TenantID:       assembly.TenantID,
		PrincipalID:    principalID,
		WorkspaceID:    assembly.WorkspaceID,
		AgentID:        assembly.AgentID,
		ProfileID:      assembly.ProfileID,
	}); err != nil {
		t.Fatalf("CreateConversation() error = %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewChatService(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	first, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     assembly.TenantID,
		PrincipalID:                  principalID,
		WorkspaceID:                  assembly.WorkspaceID,
		AgentID:                      assembly.AgentID,
		ProfileID:                    assembly.ProfileID,
		Message:                      question,
		RequestID:                    "backup-knowledge-reuse-turn-1",
		Deadline:                     deadline,
		ConversationID:               conversationID,
		ExpectedConversationRevision: 0,
	})
	if err != nil || first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.ReasonCode != "MODEL_SUCCEEDED" ||
		first.ConversationRevision != 1 {
		t.Fatalf("fresh retrieval turn=%+v error=%v", first, err)
	}
	second, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     assembly.TenantID,
		PrincipalID:                  principalID,
		WorkspaceID:                  assembly.WorkspaceID,
		AgentID:                      assembly.AgentID,
		ProfileID:                    assembly.ProfileID,
		Message:                      question,
		RequestID:                    "backup-knowledge-reuse-turn-2",
		Deadline:                     deadline.Add(time.Minute),
		ConversationID:               conversationID,
		ExpectedConversationRevision: 1,
		ExpectedHeadRunID:            first.RunID,
	})
	if err != nil || second.LoopResult.Disposition != loopapi.DispositionTerminated ||
		second.LoopResult.ReasonCode != "MODEL_SUCCEEDED" ||
		second.ConversationRevision != 2 {
		t.Fatalf("reuse turn=%+v error=%v", second, err)
	}
	if countingKnowledge.calls.Load() != 1 {
		t.Fatalf(
			"fresh/reuse Knowledge calls = %d, want 1",
			countingKnowledge.calls.Load(),
		)
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
			t.Fatalf("verify Knowledge reuse seed artifact: %v", err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf("copy Knowledge reuse seed artifact: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	sourceAttemptID := modelAttemptIDForRun(t, databasePath, first.RunID)
	reusedAttemptID := modelAttemptIDForRun(t, databasePath, second.RunID)
	return knowledgeReuseBackupFixture{
		databasePath:    databasePath,
		artifactRoot:    artifactRoot,
		conversationID:  conversationID,
		sourceAttemptID: sourceAttemptID,
		reusedAttemptID: reusedAttemptID,
		sourceClosure: loadContextCompilationClosure(
			t,
			databasePath,
			sourceAttemptID,
		),
		reusedClosure: loadContextCompilationClosure(
			t,
			databasePath,
			reusedAttemptID,
		),
		knowledgeInvoker: countingKnowledge,
	}
}

func prepareKnowledgeReuseBackupSeed(t *testing.T) *bootstrapseed.Prepared {
	t.Helper()
	memoryPath := memoryExampleSeedPath(t)
	ragPath := filepath.Join(
		filepath.Dir(memoryPath),
		"current-v1.rag.bootstrap.seed.json",
	)
	memorySeed := decodeKnowledgeReuseBackupSeed(t, memoryPath)
	ragSeed := decodeKnowledgeReuseBackupSeed(t, ragPath)
	providers, ok := ragSeed["knowledge_context_providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf(
			"RAG seed Knowledge providers = %#v",
			ragSeed["knowledge_context_providers"],
		)
	}
	provider, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("RAG seed Knowledge provider = %#v", providers[0])
	}
	config, ok := provider["config"].(map[string]any)
	if !ok {
		t.Fatalf("RAG seed Knowledge config = %#v", provider["config"])
	}
	parameters, ok := config["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("RAG seed Knowledge parameters = %#v", config["parameters"])
	}
	parameters["routing"] = map[string]any{
		"schema_version":  moduleapi.KnowledgeRoutingPolicySchemaV1,
		"collection_tags": []any{"backend"},
		"match_terms":     []any{"freeagent"},
		"min_match_terms": json.Number("1"),
		"reuse": map[string]any{
			"exact_question_only":     true,
			"min_category_count":      json.Number("1"),
			"min_repeated_term_count": json.Number("1"),
			"max_lookback_turns":      json.Number("8"),
			"reuse_ttl_seconds":       json.Number("3600"),
		},
	}
	memorySeed["knowledge_context_providers"] = []any{provider}
	seedJSON, err := json.Marshal(memorySeed)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(seedJSON)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(memoryPath))
	if err != nil {
		t.Fatalf("Prepare(Knowledge reuse seed) error = %v", err)
	}
	return prepared
}

func decodeKnowledgeReuseBackupSeed(
	t *testing.T,
	path string,
) map[string]any {
	t.Helper()
	canonical, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var seed map[string]any
	if err := decoder.Decode(&seed); err != nil {
		t.Fatal(err)
	}
	return seed
}

func assertKnowledgeReuseBackupClosure(
	t *testing.T,
	fixture knowledgeReuseBackupFixture,
) {
	t.Helper()
	if fixture.sourceClosure.AttemptState !=
		string(corecontract.ModelAttemptSucceeded) ||
		fixture.reusedClosure.AttemptState !=
			string(corecontract.ModelAttemptSucceeded) {
		t.Fatalf(
			"fresh/reuse Attempt states = %q/%q",
			fixture.sourceClosure.AttemptState,
			fixture.reusedClosure.AttemptState,
		)
	}
	if fixture.sourceClosure.AttemptContextCompilationRef !=
		fixture.sourceClosure.CompilationDigest ||
		fixture.reusedClosure.AttemptContextCompilationRef !=
			fixture.reusedClosure.CompilationDigest {
		t.Fatal("fresh/reuse Attempt lost its exact Compilation ref")
	}
	source, err := corecontract.RestoreContextCompilationV1(
		fixture.sourceClosure.CompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1(fresh) error = %v", err)
	}
	reused, err := corecontract.RestoreContextCompilationV1(
		fixture.reusedClosure.CompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1(reuse) error = %v", err)
	}
	if len(source.KnowledgeRetrievals) != 1 ||
		len(source.KnowledgeReuses) != 0 ||
		source.KnowledgeRetrievals[0].Provenance == nil {
		t.Fatalf("fresh Knowledge closure = %+v", source)
	}
	if len(reused.KnowledgeRetrievals) != 0 ||
		len(reused.KnowledgeShortcuts) != 0 ||
		len(reused.KnowledgeReuses) != 1 {
		t.Fatalf("reused Knowledge closure = %+v", reused)
	}
	reuse := reused.KnowledgeReuses[0]
	if reuse.SourceConversationID != fixture.conversationID ||
		reuse.SourceTurnIndex != 1 ||
		reuse.SourceAttemptID != fixture.sourceAttemptID ||
		reuse.SourceCompilationRef != fixture.sourceClosure.CompilationDigest ||
		!reflect.DeepEqual(
			reuse.FreshRetrieval,
			source.KnowledgeRetrievals[0],
		) {
		t.Fatalf("Knowledge reuse source closure = %+v", reuse)
	}
	if !bytes.Equal(
		fixture.sourceClosure.CompilationCanonical,
		mustRebuildKnowledgeReuseCompilation(t, source),
	) || !bytes.Equal(
		fixture.reusedClosure.CompilationCanonical,
		mustRebuildKnowledgeReuseCompilation(t, reused),
	) {
		t.Fatal("fresh/reuse CONTEXT_COMPILATION canonical bytes changed")
	}
}

func mustRebuildKnowledgeReuseCompilation(
	t *testing.T,
	compilation corecontract.ContextCompilationV1,
) []byte {
	t.Helper()
	_, canonical, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatalf("NewContextCompilationV1() error = %v", err)
	}
	return canonical
}

func assertKnowledgeReuseInvocationCount(
	t *testing.T,
	fixture knowledgeReuseBackupFixture,
	want uint64,
	stage string,
) {
	t.Helper()
	if fixture.knowledgeInvoker == nil {
		t.Fatalf("Knowledge invoker is nil after %s", stage)
	}
	if got := fixture.knowledgeInvoker.calls.Load(); got != want {
		t.Fatalf(
			"Knowledge invocation count after %s = %d, want %d",
			stage,
			got,
			want,
		)
	}
}
