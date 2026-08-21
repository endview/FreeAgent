package currentbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
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

const actionRoundTripEvidenceCanonical = `{"kind":"provider_lookup","status":"still_unknown"}`

func TestFullBundleRoundTripPreservesActionClosureWithoutExecution(
	t *testing.T,
) {
	fixture := newActionRoundTripFixture(t)
	if got := fixture.executorCalls.Load(); got != 2 {
		t.Fatalf("fixture executor calls=%d want 2", got)
	}
	source := captureActionRoundTripClosures(
		t,
		fixture.databasePath,
		fixture.targets,
		"source",
	)
	assertActionRoundTripClosure(
		t,
		source[0],
		currentstore.ActionDispatchSucceeded,
		corecontract.ModelReadyAfterActionLoopStep,
	)
	assertActionRoundTripClosure(
		t,
		source[1],
		currentstore.ActionDispatchUnknown,
		corecontract.WaitingReconciliationLoopStep,
	)

	bundle := filepath.Join(t.TempDir(), "action-full.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-action-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Action) error = %v", err)
	}
	if got := fixture.executorCalls.Load(); got != 2 {
		t.Fatalf("CreateBundle executed Action: calls=%d", got)
	}
	if manifest.AttemptCounts.ActionPending != 0 ||
		manifest.AttemptCounts.ActionUnknown != 1 ||
		manifest.AttemptCounts.ModelPending != 0 ||
		manifest.AttemptCounts.ModelUnknown != 0 ||
		manifest.ArtifactCount != 3 {
		t.Fatalf("Action bundle manifest = %+v", manifest)
	}

	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Action) error = %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest ||
		verified.AttemptCounts != manifest.AttemptCounts {
		t.Fatalf("verified Action manifest=%+v want %+v", verified, manifest)
	}
	if got := fixture.executorCalls.Load(); got != 2 {
		t.Fatalf("VerifyBundle executed Action: calls=%d", got)
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
		t.Fatalf("RestoreBundle(Action) error = %v", err)
	}
	if got := fixture.executorCalls.Load(); got != 2 {
		t.Fatalf("RestoreBundle executed Action: calls=%d", got)
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		restoredDatabase,
	); err != nil {
		t.Fatalf("VerifyCurrentStoreReadOnly(restored Action) error = %v", err)
	}

	restored := captureActionRoundTripClosures(
		t,
		restoredDatabase,
		fixture.targets,
		"restored",
	)
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf(
			"restored Action closure differs:\nsource=%#v\nrestored=%#v",
			source,
			restored,
		)
	}
	assertActionRoundTripClosure(
		t,
		restored[0],
		currentstore.ActionDispatchSucceeded,
		corecontract.ModelReadyAfterActionLoopStep,
	)
	assertActionRoundTripClosure(
		t,
		restored[1],
		currentstore.ActionDispatchUnknown,
		corecontract.WaitingReconciliationLoopStep,
	)
	if got := fixture.executorCalls.Load(); got != 2 {
		t.Fatalf("reopen/read executed Action: calls=%d", got)
	}
	for _, artifact := range manifest.Artifacts {
		verifiedArtifact, err := verifyArtifactDirectory(
			filepath.Join(restoredArtifacts, artifact.Digest),
			artifact.Digest,
		)
		if err != nil || verifiedArtifact.sizeBytes != artifact.SizeBytes {
			t.Fatalf(
				"restored Action artifact %s = %+v, %v",
				artifact.Digest,
				verifiedArtifact,
				err,
			)
		}
	}
}

type actionRoundTripFixture struct {
	databasePath  string
	artifactRoot  string
	targets       []actionRoundTripTarget
	executorCalls *atomic.Uint32
}

type actionRoundTripTarget struct {
	RunID     string
	AttemptID string
}

type actionRoundTripClosure struct {
	RunID                  string
	RunState               string
	RunDisposition         string
	Manifest               corecontract.RunManifest
	ManifestCanonical      []byte
	Member                 corecontract.MemberExecutionSnapshot
	MemberCanonical        []byte
	Step                   string
	BudgetStateRef         string
	Continuation           []byte
	PendingModelAttempt    string
	PendingActionAttempt   string
	WaitingReason          string
	LastAuthoritativeEvent uint64
	Action                 currentstore.ActionDispatchRecord
	SourceModel            currentstore.ModelDispatchRecord
	SourceModelResult      currentstore.ContentRecord
}

type scriptedBackupAction struct {
	delegate *exactadapter.TextStatsAction
	calls    atomic.Uint32
}

var (
	_ modulehost.ModuleInvoker   = (*scriptedBackupAction)(nil)
	_ moduleapi.ActionProviderV1 = (*scriptedBackupAction)(nil)
	_ modulehost.ActionExecutor  = (*scriptedBackupAction)(nil)
)

func (adapter *scriptedBackupAction) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return adapter.delegate.Invoke(ctx, prepared)
}

func (adapter *scriptedBackupAction) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	return adapter.delegate.Describe(ctx, request)
}

func (adapter *scriptedBackupAction) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	return adapter.delegate.Prepare(ctx, request)
}

func (adapter *scriptedBackupAction) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	call := adapter.calls.Add(1)
	if call == 1 {
		result, err := adapter.delegate.ExecutePrepared(ctx, execution)
		if err != nil {
			return moduleapi.ActionExecutionResultV1{}, err
		}
		frozen, _, err := moduleapi.NewActionExecutionResultV1(
			moduleapi.ActionExecutionResultV1{
				SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
				AttemptID:           result.AttemptID,
				Outcome:             result.Outcome,
				CanonicalResult:     result.CanonicalResult,
				ProviderReceipt:     json.RawMessage(`{"call":1,"executor":"scripted"}`),
				ExternalOperationID: "action-operation-success-1",
			},
		)
		return frozen, err
	}
	frozen, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:           execution.Request.AttemptID,
			Outcome:             moduleapi.ActionExecutionUnknown,
			ProviderReceipt:     json.RawMessage(`{"call":2,"executor":"scripted"}`),
			ExternalOperationID: "action-operation-unknown-2",
			UnknownReason:       "SCRIPTED_UNKNOWN",
		},
	)
	return frozen, err
}

type twoStepBackupLoop struct {
	delegate loopapi.Loop
}

func (loop twoStepBackupLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	input.MaxSteps = 2
	return loop.delegate.Run(ctx, input)
}

func newActionRoundTripFixture(t *testing.T) actionRoundTripFixture {
	t.Helper()
	ctx := context.Background()
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.action.bootstrap.seed.json",
	)
	prepared, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile(Action) error = %v", err)
	}
	assertions := prepared.ModuleAssertions()
	modelAssertion := prepared.ModelAssertion()
	var actionAssertion bootstrapseed.ModuleAssertion
	for _, assertion := range assertions {
		if assertion.ExpectedAdapterIdentity ==
			"freeagent.adapter.action.text-stats/v1" {
			actionAssertion = assertion
			break
		}
	}
	if actionAssertion.ArtifactDigest == "" {
		t.Fatal("Action seed has no text.stats assertion")
	}
	modelProvider := activatedModuleFromAssertion(modelAssertion)
	actionProvider := activatedModuleFromAssertion(actionAssertion)
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	textStats, err := exactadapter.NewTextStatsAction(actionProvider)
	if err != nil {
		t.Fatal(err)
	}
	scripted := &scriptedBackupAction{delegate: textStats}
	registry, err := exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         echo,
		},
		exactadapter.Registration{
			ArtifactDigest:  actionProvider.ArtifactDigest,
			AdapterIdentity: actionProvider.AdapterIdentity,
			Invoker:         scripted,
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
		t.Fatalf("Action seed Import() error = %v", err)
	}
	materializer, err := actionmaterializer.New(registry)
	if err != nil {
		t.Fatal(err)
	}
	realLoop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewActionChatService(
		store,
		twoStepBackupLoop{delegate: realLoop},
		materializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	assembly := prepared.DefaultAssembly()
	deadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	succeeded, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "currentbackup-action-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "count this successful backup action",
		RequestID:   "currentbackup-action-success",
		Deadline:    deadline,
	})
	if err != nil {
		t.Fatalf("successful Action Chat() error = %v", err)
	}
	if succeeded.LoopResult.Disposition != loopapi.DispositionYielded ||
		succeeded.TerminalResult != nil || succeeded.FailureCode != "" {
		t.Fatalf("successful two-step Action Chat = %+v", succeeded)
	}
	unknown, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "currentbackup-action-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "count this unknown backup action",
		RequestID:   "currentbackup-action-unknown",
		Deadline:    deadline.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("UNKNOWN Action Chat() error = %v", err)
	}
	if unknown.LoopResult.Disposition !=
		loopapi.DispositionWaitingReconciliation ||
		unknown.LoopResult.ReasonCode != "SCRIPTED_UNKNOWN" ||
		unknown.TerminalResult != nil || unknown.FailureCode != "" {
		t.Fatalf("UNKNOWN two-step Action Chat = %+v", unknown)
	}
	if scripted.calls.Load() != 2 {
		t.Fatalf("scripted Action calls=%d want 2", scripted.calls.Load())
	}
	unsettled, err := store.ScanUnsettledActionDispatchRecords(ctx, unknown.RunID)
	if err != nil {
		t.Fatalf("ScanUnsettledActionDispatchRecords(UNKNOWN): %v", err)
	}
	if len(unsettled) != 1 ||
		unsettled[0].Attempt.State != currentstore.ActionDispatchUnknown ||
		unsettled[0].ProviderReceipt == nil {
		t.Fatalf("UNKNOWN Action before evidence = %+v", unsettled)
	}
	unknownLease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   unknown.RunID,
			OwnerID: "currentbackup-action-reconciler",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease(UNKNOWN evidence): %v", err)
	}
	withEvidence, err := store.ReconcileActionDispatchOutcome(
		ctx,
		currentstore.ReconcileActionDispatchOutcomeInput{
			Lease:                   unknownLease,
			AttemptID:               unsettled[0].Attempt.AttemptID,
			InvocationID:            unsettled[0].Attempt.AttemptID,
			Provider:                unsettled[0].Attempt.Binding.Provider,
			ExpectedAttemptRevision: unsettled[0].Attempt.Revision,
			Outcome:                 moduleapi.ActionExecutionUnknown,
			ProviderReceiptCanonical: bytes.Clone(
				unsettled[0].ProviderReceipt.CanonicalBytes,
			),
			ExternalOperationID: unsettled[0].Attempt.ExternalOperationID,
			UnknownReason:       unsettled[0].Attempt.UnknownReason,
			ReconciliationEvidenceCanonical: []byte(
				actionRoundTripEvidenceCanonical,
			),
		},
	)
	if err != nil {
		t.Fatalf("ReconcileActionDispatchOutcome(UNKNOWN evidence): %v", err)
	}
	if !withEvidence.Applied ||
		withEvidence.Record.Attempt.State != currentstore.ActionDispatchUnknown ||
		withEvidence.Record.ReconciliationEvidence == nil ||
		!bytes.Equal(
			withEvidence.Record.ReconciliationEvidence.CanonicalBytes,
			[]byte(actionRoundTripEvidenceCanonical),
		) {
		t.Fatalf("UNKNOWN Action evidence = %+v", withEvidence)
	}
	if err := store.ReleaseRunLease(ctx, withEvidence.Lease); err != nil {
		t.Fatalf("ReleaseRunLease(UNKNOWN evidence): %v", err)
	}
	if scripted.calls.Load() != 2 {
		t.Fatalf("UNKNOWN evidence reconciliation executed Action: %d", scripted.calls.Load())
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
			t.Fatalf("verify Action seed artifact %s: %v", assertion.ArtifactDigest, err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatalf("copy Action seed artifact %s: %v", assertion.ArtifactDigest, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return actionRoundTripFixture{
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		targets: []actionRoundTripTarget{
			{
				RunID:     succeeded.RunID,
				AttemptID: actionAttemptIDForBackupRun(t, databasePath, succeeded.RunID),
			},
			{
				RunID:     unknown.RunID,
				AttemptID: withEvidence.Record.Attempt.AttemptID,
			},
		},
		executorCalls: &scripted.calls,
	}
}

func actionAttemptIDForBackupRun(
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
	var attemptID string
	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*), MIN(attempt_id)
		FROM dispatch_attempts
		WHERE run_id=?
	`, runID).Scan(&count, &attemptID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || attemptID == "" {
		t.Fatalf("Run %s Action Attempts=%d/%q", runID, count, attemptID)
	}
	return attemptID
}

func captureActionRoundTripClosures(
	t *testing.T,
	databasePath string,
	targets []actionRoundTripTarget,
	ownerSuffix string,
) []actionRoundTripClosure {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	closures := make([]actionRoundTripClosure, 0, len(targets))
	for index, target := range targets {
		action, err := store.GetActionDispatchRecord(ctx, target.AttemptID)
		if err != nil {
			t.Fatalf("GetActionDispatchRecord(%s): %v", target.AttemptID, err)
		}
		sourceModel, err := store.GetModelDispatchRecord(
			ctx,
			action.Attempt.SourceModelAttemptID,
		)
		if err != nil {
			t.Fatalf("GetModelDispatchRecord(source %s): %v", target.AttemptID, err)
		}
		lease, err := store.AcquireCurrentRunLease(
			ctx,
			currentstore.AcquireCurrentRunLeaseInput{
				RunID: target.RunID,
				OwnerID: "currentbackup-action-read-" + ownerSuffix + "-" +
					string(rune('1'+index)),
				TTL: time.Minute,
			},
		)
		if err != nil {
			t.Fatalf("AcquireCurrentRunLease(%s): %v", target.RunID, err)
		}
		run, err := store.LoadRunForLoop(ctx, lease)
		if err != nil {
			t.Fatalf("LoadRunForLoop(%s): %v", target.RunID, err)
		}
		if err := store.ReleaseRunLease(ctx, lease); err != nil {
			t.Fatalf("ReleaseRunLease(%s): %v", target.RunID, err)
		}
		modelResult, err := store.GetContent(
			ctx,
			sourceModel.Attempt.ResultRef,
		)
		if err != nil {
			t.Fatalf("source Model result %s is absent: %v", sourceModel.Attempt.ResultRef, err)
		}
		closures = append(closures, actionRoundTripClosure{
			RunID:                  run.RunID,
			RunState:               run.State,
			RunDisposition:         run.Disposition,
			Manifest:               run.Manifest,
			ManifestCanonical:      bytes.Clone(run.ManifestCanonical),
			Member:                 run.Member,
			MemberCanonical:        bytes.Clone(run.MemberCanonical),
			Step:                   run.Frame.Step,
			BudgetStateRef:         run.Frame.BudgetStateRef,
			Continuation:           bytes.Clone(run.Frame.Continuation),
			PendingModelAttempt:    run.Frame.PendingAttemptID,
			PendingActionAttempt:   run.Frame.PendingDispatchAttemptID,
			WaitingReason:          run.Frame.WaitingReason,
			LastAuthoritativeEvent: run.Frame.LastAuthoritativeEvent,
			Action:                 action,
			SourceModel:            sourceModel,
			SourceModelResult:      modelResult,
		})
	}
	return closures
}

func assertActionRoundTripClosure(
	t *testing.T,
	closure actionRoundTripClosure,
	wantState currentstore.ActionDispatchState,
	wantStep string,
) {
	t.Helper()
	action := closure.Action
	if action.Attempt.RunID != closure.RunID ||
		action.Attempt.MemberID != closure.Member.MemberID ||
		action.Attempt.MemberSnapshotDigest !=
			closure.Member.MemberSnapshotDigest ||
		action.Attempt.SourceModelAttemptID !=
			closure.SourceModel.Attempt.AttemptID ||
		action.Attempt.State != wantState || closure.Step != wantStep ||
		action.Proposal.Kind != currentstore.ContentActionProposal ||
		action.ProviderReceipt == nil ||
		action.ProviderReceipt.Kind != currentstore.ContentProviderReceipt ||
		action.Attempt.ProviderReceiptRef != action.ProviderReceipt.Digest ||
		action.Attempt.ExternalOperationID == "" {
		t.Fatalf("Action scalar/content closure = %+v / %+v", closure, action)
	}
	if canonical, err := moduleapi.CanonicalJSON(
		action.ProviderReceipt.CanonicalBytes,
	); err != nil || !bytes.Equal(canonical, action.ProviderReceipt.CanonicalBytes) {
		t.Fatalf("Action receipt is not canonical: %s / %v", action.ProviderReceipt.CanonicalBytes, err)
	}

	var definition *corecontract.FrozenActionDefinitionV1
	for index := range closure.Member.Actions {
		candidate := &closure.Member.Actions[index]
		if candidate.PublicActionID == action.Attempt.PublicActionID {
			definition = candidate
			break
		}
	}
	if definition == nil ||
		definition.ProviderActionID != action.Attempt.ProviderActionID ||
		definition.DefinitionDigest != action.Attempt.DefinitionDigest ||
		definition.BindingIndex != action.Attempt.BindingIndex ||
		definition.EffectClass != action.Attempt.EffectClass ||
		definition.MaxResultBytes != action.Attempt.MaxResultBytes {
		t.Fatalf("Action Definition closure = %+v / %+v", definition, action.Attempt)
	}
	var actionPlan *moduleapi.PortPlan
	for index := range closure.Member.PortPlans {
		candidate := &closure.Member.PortPlans[index]
		if candidate.Port.Name == moduleapi.PortNameActionProvider &&
			candidate.Port.ExactVersion == moduleapi.PortVersionV1 {
			actionPlan = candidate
			break
		}
	}
	if actionPlan == nil ||
		uint64(action.Attempt.BindingIndex) >= uint64(len(actionPlan.Bindings)) ||
		!reflect.DeepEqual(
			action.Attempt.Binding,
			actionPlan.Bindings[action.Attempt.BindingIndex],
		) {
		t.Fatalf("Action Binding closure = %+v / %+v", actionPlan, action.Attempt.Binding)
	}
	bindingJSON, err := json.Marshal(actionPlan.Bindings[action.Attempt.BindingIndex])
	if err != nil {
		t.Fatal(err)
	}
	bindingCanonical, err := moduleapi.CanonicalJSON(bindingJSON)
	if err != nil || !bytes.Equal(bindingCanonical, action.Attempt.BindingCanonical) {
		t.Fatalf("Action Binding canonical closure error=%v", err)
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		action.Proposal.CanonicalBytes,
		action.Proposal.Digest,
		closure.Member.MemberSnapshotDigest,
		*definition,
	)
	if err != nil {
		t.Fatalf("RestoreActionProposalV1() error = %v", err)
	}
	if proposal.PublicActionID != action.Attempt.PublicActionID ||
		proposal.ProviderActionID != action.Attempt.ProviderActionID ||
		proposal.DefinitionDigest != action.Attempt.DefinitionDigest {
		t.Fatalf("Action Proposal semantic closure = %+v", proposal)
	}

	model := closure.SourceModel
	if model.Attempt.RunID != closure.RunID ||
		model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		model.Attempt.LogicalStepID != corecontract.FirstModelLogicalStepIDV1 ||
		model.Attempt.SourceDispatchAttemptID != "" ||
		model.Attempt.ResultRef != closure.SourceModelResult.Digest ||
		closure.SourceModelResult.Kind != currentstore.ContentModelResult {
		t.Fatalf("Action source Model closure = %+v / %+v", model, closure.SourceModelResult)
	}
	modelOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		closure.SourceModelResult.CanonicalBytes,
	)
	if err != nil || modelOutput.ActionRequest == nil ||
		modelOutput.ActionRequest.ActionID != proposal.PublicActionID ||
		!bytes.Equal(modelOutput.ActionRequest.CanonicalInput, proposal.CanonicalInput) {
		t.Fatalf("Action source Model output = %+v / %v", modelOutput, err)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		closure.Continuation,
	)
	if err != nil || continuation.AttemptKind != corecontract.AttemptKindAction ||
		continuation.AttemptID != action.Attempt.AttemptID ||
		continuation.LogicalStepID != action.Attempt.LogicalStepID {
		t.Fatalf("Action continuation = %+v / %v", continuation, err)
	}

	switch wantState {
	case currentstore.ActionDispatchSucceeded:
		if action.Result == nil ||
			action.Result.Kind != currentstore.ContentActionResult ||
			action.Attempt.ResultRef != action.Result.Digest ||
			closure.RunState != corecontract.InitialRunState ||
			closure.RunDisposition != "" || closure.PendingModelAttempt != "" ||
			closure.PendingActionAttempt != "" || closure.WaitingReason != "" {
			t.Fatalf("SUCCEEDED/MODEL_READY Action closure = %+v", closure)
		}
		result, err := corecontract.RestoreActionResultV1(
			action.Result.CanonicalBytes,
			action.Result.Digest,
			*definition,
		)
		if err != nil || result.Status != corecontract.ActionResultAvailable {
			t.Fatalf("AVAILABLE Action result = %+v / %v", result, err)
		}
	case currentstore.ActionDispatchUnknown:
		if action.Result != nil || action.Attempt.ResultRef != "" ||
			action.Attempt.UnknownReason != "SCRIPTED_UNKNOWN" ||
			action.ReconciliationEvidence == nil ||
			action.Attempt.ReconciliationEvidenceRef !=
				action.ReconciliationEvidence.Digest ||
			action.ReconciliationEvidence.Kind !=
				currentstore.ContentReconciliationEvidence ||
			!bytes.Equal(
				action.ReconciliationEvidence.CanonicalBytes,
				[]byte(actionRoundTripEvidenceCanonical),
			) ||
			closure.RunState != corecontract.WaitingReconciliationLoopStep ||
			closure.RunDisposition != corecontract.WaitingReconciliationLoopStep ||
			closure.PendingModelAttempt != "" ||
			closure.PendingActionAttempt != "" || closure.WaitingReason == "" {
			t.Fatalf("UNKNOWN Action closure = %+v", closure)
		}
	default:
		t.Fatalf("unsupported expected Action state %q", wantState)
	}
	if !strings.Contains(
		string(action.ProviderReceipt.CanonicalBytes),
		`"executor":"scripted"`,
	) {
		t.Fatalf("Action receipt lost exact executor fact: %s", action.ProviderReceipt.CanonicalBytes)
	}
}
