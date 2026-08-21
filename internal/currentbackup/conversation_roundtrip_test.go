package currentbackup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

type conversationBackupFixture struct {
	databasePath      string
	artifactRoot      string
	conversationID    string
	tenantID          string
	principalID       string
	workspaceID       string
	agentID           string
	profileID         string
	turnRunIDs        []string
	nonConversationID string
	invoker           *countingConversationInvoker
	registry          *exactadapter.Registry
	invocations       int64
}

type countingConversationInvoker struct {
	delegate modulehost.ModuleInvoker
	calls    atomic.Int64
}

func (invoker *countingConversationInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.calls.Add(1)
	return invoker.delegate.Invoke(ctx, prepared)
}

type conversationStoredState struct {
	Conversations []conversationStoredRow
	Runs          []conversationStoredRun
	Manifests     []conversationStoredManifest
}

type conversationStoredRow struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
	HeadRunID      sql.NullString
	Revision       int64
	CreatedAt      int64
	UpdatedAt      int64
}

type conversationStoredRun struct {
	RunID        string
	Conversation sql.NullString
	TurnIndex    sql.NullInt64
	Predecessor  sql.NullString
}

type conversationStoredManifest struct {
	RunID     string
	Canonical []byte
	Digest    string
}

func TestConversationBundleRoundTripPreservesLinearClosureWithoutExecution(
	t *testing.T,
) {
	fixture := newConversationBackupFixture(t)
	ctx := context.Background()
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.databasePath); err != nil {
		t.Fatalf("verify source Conversation closure: %v", err)
	}
	source := captureConversationStoredState(t, fixture.databasePath)
	bundle := filepath.Join(t.TempDir(), "conversation.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-conversation-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Conversation): %v", err)
	}
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Conversation): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verified manifest=%q want %q", verified.ManifestDigest, manifest.ManifestDigest)
	}
	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(Conversation): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Conversation closure: %v", err)
	}
	restored := captureConversationStoredState(t, restoredDatabase)
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf(
			"restored Conversation closure differs:\nsource=%#v\nrestored=%#v",
			source,
			restored,
		)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("backup/verify/restore executed model adapter: calls=%d want %d", got, fixture.invocations)
	}
	restoredStore, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored Conversation Store: %v", err)
	}
	defer restoredStore.Close()
	restoredLoop, err := coreloop.NewUniversalLoop(restoredStore, fixture.registry)
	if err != nil {
		t.Fatalf("compose restored Conversation Loop: %v", err)
	}
	restoredService, err := localchat.NewChatService(restoredStore, restoredLoop)
	if err != nil {
		t.Fatalf("compose restored Conversation service: %v", err)
	}
	head, err := restoredStore.GetConversation(
		ctx,
		fixture.tenantID,
		fixture.conversationID,
	)
	if err != nil || head.Revision != 2 || head.HeadRunID != fixture.turnRunIDs[1] {
		t.Fatalf("restored Conversation head=%+v error=%v", head, err)
	}
	continued, err := restoredService.Chat(ctx, localchat.ChatInput{
		TenantID:                     fixture.tenantID,
		PrincipalID:                  fixture.principalID,
		WorkspaceID:                  fixture.workspaceID,
		AgentID:                      fixture.agentID,
		ProfileID:                    fixture.profileID,
		Message:                      "conversation after restore",
		RequestID:                    "conversation-backup-turn-3",
		Deadline:                     time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond),
		ConversationID:               fixture.conversationID,
		ExpectedConversationRevision: head.Revision,
		ExpectedHeadRunID:            head.HeadRunID,
	})
	if err != nil || continued.LoopResult.Disposition != loopapi.DispositionTerminated ||
		continued.ConversationRevision != 3 ||
		continued.Reply != "conversation after restore" {
		t.Fatalf("continue restored Conversation=%+v error=%v", continued, err)
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations+1 {
		t.Fatalf("restored continuation calls=%d want %d", got, fixture.invocations+1)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify continued restored Conversation: %v", err)
	}
}

func TestConversationSemanticVerifierRejectsBrokenClosure(t *testing.T) {
	fixture := newConversationBackupFixture(t)
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB)
	}{
		{
			name: "owner principal",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE conversations SET principal_id='forged-principal'
					WHERE conversation_id=?
				`, fixture.conversationID)
			},
		},
		{
			name: "head",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE conversations SET head_run_id=?
					WHERE conversation_id=?
				`, fixture.turnRunIDs[0], fixture.conversationID)
			},
		},
		{
			name: "revision",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE conversations SET revision=1
					WHERE conversation_id=?
				`, fixture.conversationID)
			},
		},
		{
			name: "turn index",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE runs SET conversation_turn_index=3
					WHERE run_id=?
				`, fixture.turnRunIDs[1])
			},
		},
		{
			name: "predecessor",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE runs SET conversation_predecessor_run_id=run_id
					WHERE run_id=?
				`, fixture.turnRunIDs[1])
			},
		},
		{
			name: "manifest",
			tamper: func(t *testing.T, database *sql.DB) {
				execClosedFileTamperV1(
					t,
					database,
					[]string{"run_manifests_reject_update"},
					`
					UPDATE run_manifests SET canonical_json=x'7b7d'
					WHERE run_id=?
				`,
					fixture.turnRunIDs[1],
				)
			},
		},
		{
			name: "non Conversation Run projection",
			tamper: func(t *testing.T, database *sql.DB) {
				mustTamperConversation(t, database, `
					UPDATE runs
					SET conversation_id=?,
					    conversation_turn_index=3,
					    conversation_predecessor_run_id=run_id
					WHERE run_id=?
				`, fixture.conversationID, fixture.nonConversationID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.databasePath, copyPath)
			database, err := sql.Open(
				"sqlite",
				sqliteFileURI(copyPath, "rw", "foreign_keys(1)"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			test.tamper(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			err = VerifyCurrentStoreSemanticClosure(context.Background(), copyPath)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("tampered Conversation error=%v want ErrIntegrity", err)
			}
		})
	}
	if got := fixture.invoker.calls.Load(); got != fixture.invocations {
		t.Fatalf("semantic tamper checks executed adapter: calls=%d want %d", got, fixture.invocations)
	}
}

func TestConversationSemanticClosureAllowsUnknownHeadWithoutSuccessor(t *testing.T) {
	database := newConversationSemanticUnitDatabase(t, 1, "run-1")
	runs := map[string]*coreRunSemanticState{
		"run-1": newConversationSemanticUnitRun(
			"run-1",
			1,
			"",
			corecontract.WaitingReconciliationLoopStep,
		),
	}
	if err := inspectConversationClosures(
		context.Background(),
		database,
		runs,
	); err != nil {
		t.Fatalf("UNKNOWN current head without successor was rejected: %v", err)
	}
}

func TestConversationSemanticClosureRejectsSuccessorAfterUnknownHead(
	t *testing.T,
) {
	database := newConversationSemanticUnitDatabase(t, 2, "run-2")
	runs := map[string]*coreRunSemanticState{
		"run-1": newConversationSemanticUnitRun(
			"run-1",
			1,
			"",
			corecontract.WaitingReconciliationLoopStep,
		),
		"run-2": newConversationSemanticUnitRun(
			"run-2",
			2,
			"run-1",
			corecontract.TerminatedLoopStep,
		),
	}
	err := inspectConversationClosures(context.Background(), database, runs)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("successor after UNKNOWN head error=%v want ErrIntegrity", err)
	}
}

func newConversationBackupFixture(t *testing.T) conversationBackupFixture {
	t.Helper()
	ctx := context.Background()
	prepared := prepareProfiledExampleSeed(t, exampleSeedPath(t))
	model := prepared.ModelAssertion()
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           model.ModuleID,
		Version:            model.ExactVersion,
		ArtifactDigest:     model.ArtifactDigest,
		InstanceID:         model.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    model.ExpectedAdapterIdentity,
		ActivationRevision: model.ActivationRevision,
	}
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
	databasePath := filepath.Join(t.TempDir(), "conversation.sqlite")
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
		t.Fatalf("Conversation seed Import: %v", err)
	}
	assembly := prepared.DefaultAssembly()
	const (
		conversationID = "backup-conversation-1"
		principalID    = "backup-conversation-principal"
	)
	if _, err := store.CreateConversation(ctx, currentstore.CreateConversationInput{
		ConversationID: conversationID,
		TenantID:       assembly.TenantID,
		PrincipalID:    principalID,
		WorkspaceID:    assembly.WorkspaceID,
		AgentID:        assembly.AgentID,
		ProfileID:      assembly.ProfileID,
	}); err != nil {
		t.Fatalf("CreateConversation: %v", err)
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
		Message:                      "conversation backup turn one",
		RequestID:                    "conversation-backup-turn-1",
		Deadline:                     deadline,
		ConversationID:               conversationID,
		ExpectedConversationRevision: 0,
	})
	if err != nil || first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.ConversationRevision != 1 {
		t.Fatalf("Conversation turn one=%+v error=%v", first, err)
	}
	second, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:                     assembly.TenantID,
		PrincipalID:                  principalID,
		WorkspaceID:                  assembly.WorkspaceID,
		AgentID:                      assembly.AgentID,
		ProfileID:                    assembly.ProfileID,
		Message:                      "conversation backup turn two",
		RequestID:                    "conversation-backup-turn-2",
		Deadline:                     deadline.Add(time.Minute),
		ConversationID:               conversationID,
		ExpectedConversationRevision: 1,
		ExpectedHeadRunID:            first.RunID,
	})
	if err != nil || second.LoopResult.Disposition != loopapi.DispositionTerminated ||
		second.ConversationRevision != 2 {
		t.Fatalf("Conversation turn two=%+v error=%v", second, err)
	}
	nonConversation, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: principalID,
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "non conversation backup control",
		RequestID:   "conversation-backup-control",
		Deadline:    deadline.Add(2 * time.Minute),
	})
	if err != nil || nonConversation.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("non-Conversation control=%+v error=%v", nonConversation, err)
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
			t.Fatalf("verify Conversation seed artifact: %v", err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf("copy Conversation seed artifact: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return conversationBackupFixture{
		databasePath:      databasePath,
		artifactRoot:      artifactRoot,
		conversationID:    conversationID,
		tenantID:          assembly.TenantID,
		principalID:       principalID,
		workspaceID:       assembly.WorkspaceID,
		agentID:           assembly.AgentID,
		profileID:         assembly.ProfileID,
		turnRunIDs:        []string{first.RunID, second.RunID},
		nonConversationID: nonConversation.RunID,
		invoker:           counting,
		registry:          registry,
		invocations:       counting.calls.Load(),
	}
}

func captureConversationStoredState(
	t *testing.T,
	databasePath string,
) conversationStoredState {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := conversationStoredState{}
	rows, err := database.Query(`
		SELECT conversation_id, tenant_id, principal_id, workspace_id,
		       agent_id, profile_id, head_run_id, revision, created_at, updated_at
		FROM conversations ORDER BY conversation_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row conversationStoredRow
		if err := rows.Scan(
			&row.ConversationID, &row.TenantID, &row.PrincipalID,
			&row.WorkspaceID, &row.AgentID, &row.ProfileID, &row.HeadRunID,
			&row.Revision, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			t.Fatal(err)
		}
		state.Conversations = append(state.Conversations, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	rows, err = database.Query(`
		SELECT run_id, conversation_id, conversation_turn_index,
		       conversation_predecessor_run_id
		FROM runs ORDER BY run_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row conversationStoredRun
		if err := rows.Scan(
			&row.RunID, &row.Conversation, &row.TurnIndex, &row.Predecessor,
		); err != nil {
			t.Fatal(err)
		}
		state.Runs = append(state.Runs, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	rows, err = database.Query(`
		SELECT manifest.run_id, manifest.canonical_json, manifest.digest
		FROM run_manifests AS manifest
		JOIN runs AS run ON run.run_id=manifest.run_id
		WHERE run.conversation_id IS NOT NULL
		ORDER BY manifest.run_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row conversationStoredManifest
		if err := rows.Scan(&row.RunID, &row.Canonical, &row.Digest); err != nil {
			t.Fatal(err)
		}
		manifest, err := corecontract.RestoreRunManifest(row.Canonical)
		if err != nil || manifest.ConversationTurn == nil {
			t.Fatalf("capture Conversation Manifest %q: %+v, %v", row.RunID, manifest, err)
		}
		state.Manifests = append(state.Manifests, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return state
}

func mustTamperConversation(
	t *testing.T,
	database *sql.DB,
	statement string,
	arguments ...any,
) {
	t.Helper()
	execClosedFileTamperV1(t, database, nil, statement, arguments...)
}

func newConversationSemanticUnitDatabase(
	t *testing.T,
	revision int64,
	headRunID string,
) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`
		CREATE TABLE conversations(
			conversation_id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			principal_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			profile_id TEXT NOT NULL,
			head_run_id TEXT,
			revision INTEGER NOT NULL
		);
		INSERT INTO conversations(
			conversation_id, tenant_id, principal_id, workspace_id,
			agent_id, profile_id, head_run_id, revision
		) VALUES('conversation-unit', 'tenant-unit', 'principal-unit',
		         'workspace-unit', 'agent-unit', 'profile-unit', ?, ?);
	`, headRunID, revision); err != nil {
		t.Fatal(err)
	}
	return database
}

func newConversationSemanticUnitRun(
	runID string,
	turnIndex int64,
	predecessor string,
	step string,
) *coreRunSemanticState {
	return &coreRunSemanticState{
		row: coreRunRow{
			runID:                 runID,
			tenantID:              "tenant-unit",
			workspaceID:           "workspace-unit",
			conversationID:        sql.NullString{String: "conversation-unit", Valid: true},
			conversationTurnIndex: sql.NullInt64{Int64: turnIndex, Valid: true},
			conversationPrevious: sql.NullString{
				String: predecessor,
				Valid:  predecessor != "",
			},
		},
		manifest: corecontract.RunManifest{
			ConversationTurn: &corecontract.ConversationTurnRefV1{
				SchemaVersion:    corecontract.ConversationTurnRefSchemaVersionV1,
				ConversationID:   "conversation-unit",
				PrincipalID:      "principal-unit",
				TurnIndex:        uint64(turnIndex),
				PredecessorRunID: predecessor,
			},
		},
		member: corecontract.MemberExecutionSnapshot{
			Agent:     corecontract.AgentRef{ID: "agent-unit"},
			Profile:   corecontract.ProfileRef{ID: "profile-unit"},
			Workspace: corecontract.WorkspaceRef{ID: "workspace-unit"},
		},
		frame: coreFrameState{
			continuation: corecontract.LoopContinuationV1{State: step},
		},
		attempts:         map[string]*coreModelAttemptState{},
		historyByAttempt: map[string]int{},
	}
}
