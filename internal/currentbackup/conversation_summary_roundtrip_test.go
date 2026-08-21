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
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type conversationSummaryBackupFixture struct {
	databasePath    string
	artifactRoot    string
	conversationID  string
	tenantID        string
	principalID     string
	workspaceID     string
	agentID         string
	profileID       string
	firstRunID      string
	secondRunID     string
	firstAttemptID  string
	secondAttemptID string
	firstMessage    string
	secondMessage   string
	sourceClosure   contextCompilationClosureSnapshot
	invoker         *countingConversationInvoker
	registry        *exactadapter.Registry
	invocations     int64
}

func TestConversationSummaryBundleRoundTripReusesDirectPredecessor(
	t *testing.T,
) {
	fixture := newConversationSummaryBackupFixture(t)
	ctx := context.Background()
	sourceCompilation := assertConversationSummarySource(t, fixture)
	if got := fixture.invoker.calls.Load(); got != fixture.invocations || got != 2 {
		t.Fatalf("source model calls=%d want 2", got)
	}

	bundle := filepath.Join(t.TempDir(), "conversation-summary.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-conversation-summary-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Conversation Summary): %v", err)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("CreateBundle executed Model Provider: calls=%d", got)
	}
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Conversation Summary): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest=%q want %q",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("VerifyBundle executed Model Provider: calls=%d", got)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(Conversation Summary): %v", err)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("RestoreBundle executed Model Provider: calls=%d", got)
	}
	restoredSource := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.secondAttemptID,
	)
	if !reflect.DeepEqual(restoredSource, fixture.sourceClosure) {
		t.Fatalf(
			"restored source Compilation/Request bytes differ:\nsource=%#v\nrestored=%#v",
			fixture.sourceClosure,
			restoredSource,
		)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("restored source reopen executed Model Provider: calls=%d", got)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored Summary Store: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, fixture.registry)
	if err != nil {
		_ = store.Close()
		t.Fatalf("compose restored Summary Loop: %v", err)
	}
	service, err := localchat.NewChatService(store, loop)
	if err != nil {
		_ = store.Close()
		t.Fatalf("compose restored Summary service: %v", err)
	}
	head, err := store.GetConversation(ctx, fixture.tenantID, fixture.conversationID)
	if err != nil || head.Revision != 2 || head.HeadRunID != fixture.secondRunID {
		_ = store.Close()
		t.Fatalf("restored Summary Conversation head=%+v error=%v", head, err)
	}
	const thirdMessage = "third turn after restore keeps the same oldest prefix"
	continued, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     fixture.tenantID,
		PrincipalID:                  fixture.principalID,
		WorkspaceID:                  fixture.workspaceID,
		AgentID:                      fixture.agentID,
		ProfileID:                    fixture.profileID,
		Message:                      thirdMessage,
		RequestID:                    "conversation-summary-turn-3",
		Deadline:                     time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond),
		ConversationID:               fixture.conversationID,
		ExpectedConversationRevision: head.Revision,
		ExpectedHeadRunID:            head.HeadRunID,
	})
	if err != nil || continued.LoopResult.Disposition != loopapi.DispositionTerminated ||
		continued.LoopResult.ReasonCode != "MODEL_SUCCEEDED" ||
		continued.ConversationRevision != 3 || continued.Reply != thirdMessage {
		_ = store.Close()
		t.Fatalf("continue restored Summary Conversation=%+v error=%v", continued, err)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations+1 {
		_ = store.Close()
		t.Fatalf("restored continuation Model calls=%d want %d", got, fixture.invocations+1)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	thirdAttemptID := modelAttemptIDForRun(t, restoredDatabase, continued.RunID)
	current := loadContextCompilationClosure(t, restoredDatabase, thirdAttemptID)
	assertConversationSummaryContinuation(
		t,
		fixture,
		sourceCompilation,
		current,
		thirdMessage,
	)
	reopenedCurrent := loadContextCompilationClosure(
		t,
		restoredDatabase,
		thirdAttemptID,
	)
	if !reflect.DeepEqual(reopenedCurrent, current) {
		t.Fatal("third-turn Compilation/Request canonical bytes changed after reopen")
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations+1 {
		t.Fatalf("closure reads executed Model Provider: calls=%d", got)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify continued Conversation Summary closure: %v", err)
	}
}

func TestVerifyBundleRejectsRewiredConversationSummaryAuthority(
	t *testing.T,
) {
	fixture := newConversationSummaryBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "conversation-summary-source.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-conversation-summary-tamper-test/v1",
	); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*corecontract.ContextCompilationV1)
	}{
		{
			name: "source digest",
			mutate: func(compilation *corecontract.ContextCompilationV1) {
				compilation.Summary.SourceTurnDigests[0] = strings.Repeat("f", 64)
			},
		},
		{
			name: "summary text",
			mutate: func(compilation *corecontract.ContextCompilationV1) {
				text := []byte(compilation.Summary.Text)
				if len(text) == 0 {
					t.Fatal("source Summary text is empty")
				}
				if text[len(text)-1] == 'Z' {
					text[len(text)-1] = 'Y'
				} else {
					text[len(text)-1] = 'Z'
				}
				compilation.Summary.Text = string(text)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := filepath.Join(t.TempDir(), "tampered.bundle")
			copyTestTree(t, bundle, tampered)
			rewriteConversationSummaryCompilation(
				t,
				filepath.Join(tampered, databaseName),
				fixture.secondAttemptID,
				test.mutate,
			)
			rewriteBundleDatabaseIdentity(t, tampered)

			if _, err := VerifyBundle(
				context.Background(),
				tampered,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("VerifyBundle(tampered Summary) error=%v want ErrIntegrity", err)
			}
		})
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("Summary tamper verification executed Model Provider: calls=%d", got)
	}
}

func TestVerifyBundleRejectsCoordinatedConversationSummaryRequestRewrite(
	t *testing.T,
) {
	fixture := newConversationSummaryBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "conversation-summary-request-source.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-conversation-summary-request-tamper-test/v1",
	); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*moduleapi.ModelGenerateRequestV1, string)
	}{
		{
			name: "duplicate summary",
			mutate: func(request *moduleapi.ModelGenerateRequestV1, summary string) {
				for index, message := range request.Messages {
					if message.Role == moduleapi.ModelRoleAssistant &&
						message.Content == summary {
						request.Messages = append(request.Messages, moduleapi.ModelMessageV1{})
						copy(request.Messages[index+2:], request.Messages[index+1:])
						request.Messages[index+1] = message
						return
					}
				}
				t.Fatal("source request has no exact Summary")
			},
		},
		{
			name: "retain summarized raw prefix",
			mutate: func(request *moduleapi.ModelGenerateRequestV1, summary string) {
				for index, message := range request.Messages {
					if message.Role == moduleapi.ModelRoleAssistant &&
						message.Content == summary {
						raw := []moduleapi.ModelMessageV1{
							{Role: moduleapi.ModelRoleUser, Content: fixture.firstMessage},
							{Role: moduleapi.ModelRoleAssistant, Content: fixture.firstMessage},
						}
						messages := make(
							[]moduleapi.ModelMessageV1,
							0,
							len(request.Messages)+len(raw),
						)
						messages = append(messages, request.Messages[:index]...)
						messages = append(messages, raw...)
						messages = append(messages, request.Messages[index:]...)
						request.Messages = messages
						return
					}
				}
				t.Fatal("source request has no exact Summary")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := filepath.Join(t.TempDir(), "tampered.bundle")
			copyTestTree(t, bundle, tampered)
			rewriteConversationSummaryRequestProjection(
				t,
				filepath.Join(tampered, databaseName),
				fixture.secondRunID,
				fixture.secondAttemptID,
				test.mutate,
			)
			rewriteBundleDatabaseIdentity(t, tampered)

			if _, err := VerifyBundle(
				context.Background(),
				tampered,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf(
					"VerifyBundle(coordinated Summary request rewrite) error=%v want ErrIntegrity",
					err,
				)
			}
		})
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("Summary request tamper verification executed Model Provider: calls=%d", got)
	}
}

func newConversationSummaryBackupFixture(
	t *testing.T,
) conversationSummaryBackupFixture {
	t.Helper()
	ctx := context.Background()
	prepared := prepareConversationSummaryBackupSeed(t)
	model := prepared.ModelAssertion()
	provider := activatedModuleFromAssertion(model)
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingConversationInvoker{delegate: echo}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  model.ArtifactDigest,
		AdapterIdentity: model.ExpectedAdapterIdentity,
		Invoker:         counting,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist: []activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        model.ModuleID,
				ExactVersion:    model.ExactVersion,
				ArtifactDigest:  model.ArtifactDigest,
				AdapterIdentity: model.ExpectedAdapterIdentity,
			}},
		},
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "conversation-summary.sqlite")
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
		t.Fatalf("Conversation Summary seed Import: %v", err)
	}
	assembly := prepared.DefaultAssembly()
	const (
		conversationID = "backup-conversation-summary"
		principalID    = "backup-conversation-summary-principal"
	)
	if _, err := store.CreateConversation(ctx, currentstore.CreateConversationInput{
		ConversationID: conversationID,
		TenantID:       assembly.TenantID,
		PrincipalID:    principalID,
		WorkspaceID:    assembly.WorkspaceID,
		AgentID:        assembly.AgentID,
		ProfileID:      assembly.ProfileID,
	}); err != nil {
		t.Fatalf("CreateConversation(Summary): %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewChatService(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	firstMessage := strings.Repeat("first-history-payload-", 350)
	const secondMessage = "second turn retains the compressed oldest pair"
	deadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	first, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     assembly.TenantID,
		PrincipalID:                  principalID,
		WorkspaceID:                  assembly.WorkspaceID,
		AgentID:                      assembly.AgentID,
		ProfileID:                    assembly.ProfileID,
		Message:                      firstMessage,
		RequestID:                    "conversation-summary-turn-1",
		Deadline:                     deadline,
		ConversationID:               conversationID,
		ExpectedConversationRevision: 0,
	})
	if err != nil || first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.ReasonCode != "MODEL_SUCCEEDED" || first.Reply != firstMessage {
		t.Fatalf("Conversation Summary first turn=%+v error=%v", first, err)
	}
	second, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     assembly.TenantID,
		PrincipalID:                  principalID,
		WorkspaceID:                  assembly.WorkspaceID,
		AgentID:                      assembly.AgentID,
		ProfileID:                    assembly.ProfileID,
		Message:                      secondMessage,
		RequestID:                    "conversation-summary-turn-2",
		Deadline:                     deadline.Add(time.Minute),
		ConversationID:               conversationID,
		ExpectedConversationRevision: 1,
		ExpectedHeadRunID:            first.RunID,
	})
	if err != nil || second.LoopResult.Disposition != loopapi.DispositionTerminated ||
		second.LoopResult.ReasonCode != "MODEL_SUCCEEDED" ||
		second.ConversationRevision != 2 || second.Reply != secondMessage {
		t.Fatalf("Conversation Summary second turn=%+v error=%v", second, err)
	}

	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, assertion := range prepared.ModuleAssertions() {
		verified, err := verifyArtifactDirectory(
			assertion.ArtifactDirectory,
			assertion.ArtifactDigest,
		)
		if err != nil {
			t.Fatalf("verify Summary seed artifact: %v", err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf("copy Summary seed artifact: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	firstAttemptID := modelAttemptIDForRun(t, databasePath, first.RunID)
	secondAttemptID := modelAttemptIDForRun(t, databasePath, second.RunID)
	return conversationSummaryBackupFixture{
		databasePath:    databasePath,
		artifactRoot:    artifactRoot,
		conversationID:  conversationID,
		tenantID:        assembly.TenantID,
		principalID:     principalID,
		workspaceID:     assembly.WorkspaceID,
		agentID:         assembly.AgentID,
		profileID:       assembly.ProfileID,
		firstRunID:      first.RunID,
		secondRunID:     second.RunID,
		firstAttemptID:  firstAttemptID,
		secondAttemptID: secondAttemptID,
		firstMessage:    firstMessage,
		secondMessage:   secondMessage,
		sourceClosure: loadContextCompilationClosure(
			t,
			databasePath,
			secondAttemptID,
		),
		invoker:     counting,
		registry:    registry,
		invocations: counting.calls.Load(),
	}
}

func prepareConversationSummaryBackupSeed(
	t *testing.T,
) *bootstrapseed.Prepared {
	t.Helper()
	seedBytes, err := os.ReadFile(exampleSeedPath(t))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(seedBytes))
	decoder.UseNumber()
	var seed map[string]any
	if err := decoder.Decode(&seed); err != nil {
		t.Fatal(err)
	}
	seed["tenant_id"] = "backup-summary"
	seed["seed_id"] = "freeagent.backup.conversation-summary"
	seed["catalog"].(map[string]any)["generation_id"] =
		"catalog-backup-conversation-summary"
	seed["control"].(map[string]any)["snapshot_id"] =
		"control-backup-conversation-summary"
	policies, ok := seed["policies"].([]any)
	if !ok {
		t.Fatalf("Summary seed policies=%#v", seed["policies"])
	}
	foundContext := false
	for _, raw := range policies {
		policy, ok := raw.(map[string]any)
		if !ok || policy["policy_type"] != "CONTEXT" {
			continue
		}
		body, ok := policy["body"].(map[string]any)
		if !ok {
			t.Fatalf("Summary seed Context body=%#v", policy["body"])
		}
		body["context_window_tokens"] = json.Number("18000")
		body["reserved_output_tokens"] = json.Number("0")
		body["recent_history_turns"] = json.Number("0")
		foundContext = true
	}
	if !foundContext {
		t.Fatal("Summary seed has no ContextPolicy")
	}
	encoded, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(exampleSeedPath(t)))
	if err != nil {
		t.Fatalf("Prepare(Conversation Summary seed): %v", err)
	}
	return prepared
}

func assertConversationSummarySource(
	t *testing.T,
	fixture conversationSummaryBackupFixture,
) corecontract.ContextCompilationV1 {
	t.Helper()
	compilation, err := corecontract.RestoreContextCompilationV1(
		fixture.sourceClosure.CompilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Summary == nil ||
		len(compilation.Summary.SourceTurnDigests) != 1 ||
		compilation.StopReason != corecontract.ContextCompilationSummaryToWatermark ||
		len(compilation.Drops) != 0 {
		t.Fatalf("source Conversation Summary Compilation=%+v", compilation)
	}
	expectedDigest := conversationSourceTurnDigest(
		t,
		fixture.databasePath,
		fixture.firstRunID,
		fixture.firstAttemptID,
	)
	expectedText, err := corecontract.ContextHeadTailSummaryV1(
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleUser, Content: fixture.firstMessage},
			{Role: moduleapi.ModelRoleAssistant, Content: fixture.firstMessage},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Summary.SourceTurnDigests[0] != expectedDigest ||
		compilation.Summary.Text != expectedText {
		t.Fatalf("source Summary raw-prefix closure=%+v", compilation.Summary)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		fixture.sourceClosure.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuiltRequest, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil || !bytes.Equal(rebuiltRequest, fixture.sourceClosure.RequestCanonical) {
		t.Fatalf("source MODEL_REQUEST canonical differs: %v", err)
	}
	_, rebuiltCompilation, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil || !bytes.Equal(
		rebuiltCompilation,
		fixture.sourceClosure.CompilationCanonical,
	) {
		t.Fatalf("source CONTEXT_COMPILATION canonical differs: %v", err)
	}
	return compilation
}

func assertConversationSummaryContinuation(
	t *testing.T,
	fixture conversationSummaryBackupFixture,
	source corecontract.ContextCompilationV1,
	current contextCompilationClosureSnapshot,
	thirdMessage string,
) {
	t.Helper()
	compilation, err := corecontract.RestoreContextCompilationV1(
		current.CompilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Summary == nil || source.Summary == nil ||
		!reflect.DeepEqual(
			compilation.Summary.SourceTurnDigests,
			source.Summary.SourceTurnDigests,
		) || compilation.Summary.Text != source.Summary.Text ||
		compilation.Summary.BeforeEstimateTokens <=
			source.Summary.BeforeEstimateTokens ||
		compilation.Summary.AfterEstimateTokens <=
			source.Summary.AfterEstimateTokens {
		t.Fatalf(
			"continued Summary did not reuse source/text with current estimates:\nsource=%+v\ncurrent=%+v",
			source.Summary,
			compilation.Summary,
		)
	}
	sourceRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		fixture.sourceClosure.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	summaryIndex := -1
	for index, message := range sourceRequest.Messages {
		if message.Role == moduleapi.ModelRoleAssistant &&
			message.Content == source.Summary.Text {
			summaryIndex = index
			break
		}
	}
	if summaryIndex < 0 {
		t.Fatal("source MODEL_REQUEST has no Summary message")
	}
	expected := sourceRequest
	expected.Messages = append(
		[]moduleapi.ModelMessageV1(nil),
		sourceRequest.Messages[:summaryIndex+1]...,
	)
	expected.Messages = append(
		expected.Messages,
		moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: fixture.secondMessage,
		},
		moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleAssistant, Content: fixture.secondMessage,
		},
		moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: thirdMessage,
		},
	)
	_, expectedCanonical, err := moduleapi.NewModelGenerateRequestV1(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current.RequestCanonical, expectedCanonical) {
		t.Fatalf(
			"continued MODEL_REQUEST bytes differ\ngot  %s\nwant %s",
			current.RequestCanonical,
			expectedCanonical,
		)
	}
	_, rebuiltCompilation, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil || !bytes.Equal(rebuiltCompilation, current.CompilationCanonical) {
		t.Fatalf("continued CONTEXT_COMPILATION canonical differs: %v", err)
	}
}

func conversationSourceTurnDigest(
	t *testing.T,
	databasePath string,
	runID string,
	attemptID string,
) string {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var manifestCanonical []byte
	if err := database.QueryRow(`
		SELECT canonical_json FROM run_manifests WHERE run_id=?
	`, runID).Scan(&manifestCanonical); err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	var resultRef string
	if err := database.QueryRow(`
		SELECT result_ref FROM model_dispatch_attempts WHERE attempt_id=?
	`, attemptID).Scan(&resultRef); err != nil {
		t.Fatal(err)
	}
	digest, err := corecontract.ContextConversationTurnDigestV1(
		1,
		manifest.TaskInputRef,
		resultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func rewriteConversationSummaryCompilation(
	t *testing.T,
	databasePath string,
	attemptID string,
	mutate func(*corecontract.ContextCompilationV1),
) {
	t.Helper()
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(databasePath, "rw", "foreign_keys(1)"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var oldDigest string
	var canonical []byte
	if err := database.QueryRow(`
		SELECT attempt.context_compilation_ref, content.canonical_bytes
		FROM model_dispatch_attempts AS attempt
		JOIN content_records AS content
		  ON content.content_digest=attempt.context_compilation_ref
		WHERE attempt.attempt_id=?
	`, attemptID).Scan(&oldDigest, &canonical); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil || compilation.Summary == nil {
		t.Fatalf("restore source Summary for tamper: %+v / %v", compilation, err)
	}
	mutate(&compilation)
	_, rewritten, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatalf("freeze rewired Summary: %v", err)
	}
	newDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		backupTestJSONMediaType,
		rewritten,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes,
			size_bytes, created_at
		)
		SELECT ?, kind, media_type, ?, length(?), created_at
		FROM content_records WHERE content_digest=?
	`, newDigest, rewritten, rewritten, oldDigest); err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts SET context_compilation_ref=?
		WHERE attempt_id=?
	`,
		newDigest,
		attemptID,
	)
}

func rewriteConversationSummaryRequestProjection(
	t *testing.T,
	databasePath string,
	runID string,
	attemptID string,
	mutate func(*moduleapi.ModelGenerateRequestV1, string),
) {
	t.Helper()
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(databasePath, "rw", "foreign_keys(1)"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var oldRequestDigest, oldCompilationDigest string
	var requestCanonical, compilationCanonical []byte
	if err := database.QueryRow(`
		SELECT
			attempt.request_ref,
			request.canonical_bytes,
			attempt.context_compilation_ref,
			compilation.canonical_bytes
		FROM model_dispatch_attempts AS attempt
		JOIN content_records AS request
		  ON request.content_digest=attempt.request_ref
		JOIN content_records AS compilation
		  ON compilation.content_digest=attempt.context_compilation_ref
		WHERE attempt.attempt_id=? AND attempt.run_id=?
	`, attemptID, runID).Scan(
		&oldRequestDigest,
		&requestCanonical,
		&oldCompilationDigest,
		&compilationCanonical,
	); err != nil {
		t.Fatal(err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		compilationCanonical,
	)
	if err != nil || compilation.Summary == nil {
		t.Fatalf("restore Summary request projection: %+v / %v", compilation, err)
	}
	mutate(&request, compilation.Summary.Text)
	_, rewrittenRequest, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		t.Fatalf("freeze rewired Summary request: %v", err)
	}
	newRequestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		backupTestJSONMediaType,
		rewrittenRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	compilation.FinalRequestDigest = newRequestDigest
	_, rewrittenCompilation, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatalf("freeze rewired Summary compilation: %v", err)
	}
	newCompilationDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		backupTestJSONMediaType,
		rewrittenCompilation,
	)
	if err != nil {
		t.Fatal(err)
	}

	type rewrittenEvent struct {
		sequence     int64
		oldDigest    string
		newDigest    string
		oldCanonical []byte
		newCanonical []byte
	}
	rows, err := database.Query(`
		SELECT event.event_sequence, event.payload_ref, content.canonical_bytes
		FROM run_events AS event
		JOIN content_records AS content ON content.content_digest=event.payload_ref
		WHERE event.run_id=?
		ORDER BY event.event_sequence
	`, runID)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]rewrittenEvent, 0, 2)
	for rows.Next() {
		var event rewrittenEvent
		if err := rows.Scan(
			&event.sequence,
			&event.oldDigest,
			&event.oldCanonical,
		); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		modelEvent, err := corecontract.RestoreModelDispatchEventV1(
			event.oldCanonical,
		)
		if err != nil || modelEvent.AttemptID != attemptID {
			continue
		}
		modelEvent.RequestDigest = newRequestDigest
		_, event.newCanonical, err = corecontract.NewModelDispatchEventV1(modelEvent)
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		event.newDigest, err = currentstore.ComputeContentDigest(
			currentstore.ContentRunEventPayload,
			backupTestJSONMediaType,
			event.newCanonical,
		)
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("rewired model RunEvents=%d want 2", len(events))
	}

	insertReplacement := func(
		newDigest string,
		newCanonical []byte,
		oldDigest string,
	) {
		t.Helper()
		if _, err := database.Exec(`
			INSERT INTO content_records(
				content_digest, kind, media_type, canonical_bytes,
				size_bytes, created_at
			)
			SELECT ?, kind, media_type, ?, length(?), created_at
			FROM content_records WHERE content_digest=?
		`, newDigest, newCanonical, newCanonical, oldDigest); err != nil {
			t.Fatal(err)
		}
	}
	insertReplacement(newRequestDigest, rewrittenRequest, oldRequestDigest)
	insertReplacement(
		newCompilationDigest,
		rewrittenCompilation,
		oldCompilationDigest,
	)
	for _, event := range events {
		insertReplacement(event.newDigest, event.newCanonical, event.oldDigest)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET request_ref=?, request_digest=?, context_compilation_ref=?
		WHERE attempt_id=? AND run_id=?
	`,
		newRequestDigest,
		newRequestDigest,
		newCompilationDigest,
		attemptID,
		runID,
	)
	for _, event := range events {
		execClosedFileTamperV1(
			t,
			database,
			[]string{"run_events_reject_update"},
			`
			UPDATE run_events SET payload_ref=?, payload_digest=?
			WHERE run_id=? AND event_sequence=?
		`,
			event.newDigest,
			event.newDigest,
			runID,
			event.sequence,
		)
	}
}

func rewriteBundleDatabaseIdentity(t *testing.T, bundle string) {
	t.Helper()
	manifestPath := filepath.Join(bundle, manifestName)
	canonical, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := restoreManifest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := hashRegularFile(
		filepath.Join(bundle, databaseName),
		maxDatabaseBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Database.SHA256 = identity.SHA256
	manifest.Database.SizeBytes = identity.SizeBytes
	_, rewritten, err := freezeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
}
