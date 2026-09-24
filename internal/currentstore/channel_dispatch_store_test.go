package currentstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type channelDispatchHarness struct {
	store      *Store
	fixture    *admissionCommitFixture
	ingress    CommitChannelIngressAdmissionInput
	modelBegin BeginModelDispatchResult
	beginInput CommitModelChannelAndBeginDispatchInput
}

func TestModelChannelBeginIsAtomicIdempotentAndPermitIsOneShot(t *testing.T) {
	harness := newChannelDispatchHarness(t, "run-channel-begin", "event-channel-begin")
	before := channelDispatchCounts(t, harness.store, harness.modelBegin.Attempt.RunID)
	bad := harness.beginInput
	_, badProposal, _, err := moduleapi.NewChannelSendProposalV1(
		harness.modelMemberDigest(t),
		badProposalEndpoint(harness),
		harness.ingress.Ingress.IngressKey,
		harness.replyTarget(t),
		"a different answer",
		json.RawMessage(`{"text":"a different answer"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	bad.ProposalCanonical = badProposal
	if _, err := harness.store.CommitModelChannelAndBeginDispatch(
		context.Background(), bad,
	); !errors.Is(err, ErrChannelDispatchIntegrity) {
		t.Fatalf("mismatched proposal error=%v", err)
	}
	if after := channelDispatchCounts(t, harness.store, harness.modelBegin.Attempt.RunID); after != before {
		t.Fatalf("failed begin leaked state: before=%+v after=%+v", before, after)
	}
	model, err := harness.store.GetModelDispatchRecord(
		context.Background(), harness.modelBegin.Attempt.AttemptID,
	)
	if err != nil || model.Attempt.State != corecontract.ModelAttemptPending ||
		model.Attempt.Revision != harness.modelBegin.Attempt.Revision {
		t.Fatalf("source Model changed after rollback: %+v err=%v", model, err)
	}

	begin, err := harness.store.CommitModelChannelAndBeginDispatch(
		context.Background(), harness.beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !begin.Applied || !begin.GatewayAllowed ||
		begin.Model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		begin.Channel.Attempt.State != DispatchPending ||
		begin.Channel.Attempt.LogicalStepID != corecontract.ChannelSendLogicalStepIDV1 ||
		begin.Model.Usage.LedgerSequence == nil ||
		*begin.Model.Usage.LedgerSequence != 1 {
		t.Fatalf("begin=%+v", begin)
	}
	if !begin.ConsumeChannelGatewayPermit() || begin.ConsumeChannelGatewayPermit() {
		t.Fatal("Channel Gateway permit is not exactly one-shot")
	}
	retry, err := harness.store.CommitModelChannelAndBeginDispatch(
		context.Background(), harness.beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Applied || retry.GatewayAllowed || retry.ConsumeChannelGatewayPermit() ||
		retry.Channel.Attempt.AttemptID != begin.Channel.Attempt.AttemptID {
		t.Fatalf("idempotent begin=%+v", retry)
	}
	run, err := harness.store.LoadRunForLoop(context.Background(), begin.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.Frame.Step != corecontract.ChannelPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != begin.Channel.Attempt.AttemptID ||
		len(run.History) != 1 ||
		run.History[0].SourceAttemptID != begin.Model.Attempt.AttemptID ||
		len(run.ChannelDispatches) != 1 {
		t.Fatalf("Channel pending Run=%+v", run)
	}
}

func TestChannelSucceededAndFailedTerminateWithoutLosingAnswer(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome moduleapi.ChannelExecutionOutcomeV1
	}{
		{name: "succeeded", outcome: moduleapi.ChannelExecutionSucceeded},
		{name: "failed", outcome: moduleapi.ChannelExecutionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newChannelDispatchHarness(
				t, "run-channel-"+test.name, "event-channel-"+test.name,
			)
			begin := harness.mustBegin(t)
			input := channelOutcomeFixture(begin, test.outcome)
			committed, err := harness.store.CommitChannelDispatchOutcome(
				context.Background(), input,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !committed.Applied || committed.Record.Attempt.State != DispatchState(test.outcome) {
				t.Fatalf("outcome=%+v", committed)
			}
			if test.outcome == moduleapi.ChannelExecutionSucceeded {
				if committed.Record.Result == nil {
					t.Fatal("SUCCEEDED lacks Channel result")
				}
				result, err := moduleapi.RestoreChannelExecutionResultV1(
					committed.Record.Result.CanonicalBytes,
				)
				if err != nil || result.AttemptID != begin.Channel.Attempt.AttemptID ||
					result.Outcome != moduleapi.ChannelExecutionSucceeded {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if committed.Record.Result != nil ||
				committed.Record.Attempt.ErrorClassification != "DELIVERY_REJECTED" {
				t.Fatalf("FAILED record=%+v", committed.Record)
			}
			retry, err := harness.store.CommitChannelDispatchOutcome(
				context.Background(), input,
			)
			if err != nil || retry.Applied {
				t.Fatalf("idempotent outcome=%+v err=%v", retry, err)
			}
			run, err := harness.store.LoadRunForLoop(context.Background(), committed.Lease)
			if err != nil {
				t.Fatal(err)
			}
			if run.State != corecontract.TerminatedLoopStep ||
				run.Disposition != corecontract.TerminatedLoopStep ||
				run.Frame.Step != corecontract.TerminatedLoopStep ||
				run.Frame.PendingDispatchAttemptID != "" ||
				len(run.History) != 1 ||
				run.History[0].SourceAttemptID != begin.Model.Attempt.AttemptID {
				t.Fatalf("terminal Run=%+v", run)
			}
		})
	}
}

func TestChannelUnknownNeverReplaysOrReturnsPendingAndReconcilesOriginal(t *testing.T) {
	harness := newChannelDispatchHarness(t, "run-channel-unknown", "event-channel-unknown")
	begin := harness.mustBegin(t)
	unknownInput := channelOutcomeFixture(begin, moduleapi.ChannelExecutionUnknown)
	unknown, err := harness.store.CommitChannelDispatchOutcome(
		context.Background(), unknownInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Record.Attempt.State != DispatchUnknown ||
		unknown.Record.Attempt.Revision != 1 {
		t.Fatalf("UNKNOWN=%+v", unknown)
	}

	semanticReplay := channelOutcomeFixture(begin, moduleapi.ChannelExecutionSucceeded)
	semanticReplay.Lease = unknown.Lease
	semanticReplay.ExpectedAttemptRevision = unknown.Record.Attempt.Revision
	if _, err := harness.store.CommitChannelDispatchOutcome(
		context.Background(), semanticReplay,
	); !errors.Is(err, ErrChannelDispatchConflict) {
		t.Fatalf("UNKNOWN semantic replay error=%v", err)
	}

	stillUnknownInput := ReconcileChannelDispatchOutcomeInput{
		Lease:                           unknown.Lease,
		AttemptID:                       unknown.Record.Attempt.AttemptID,
		InvocationID:                    unknown.Record.Attempt.AttemptID,
		Provider:                        unknown.Record.Attempt.Binding.Provider,
		ExpectedAttemptRevision:         unknown.Record.Attempt.Revision,
		Outcome:                         moduleapi.ChannelExecutionUnknown,
		ReconciliationEvidenceCanonical: []byte(`{"probe":"not-found"}`),
	}
	stillUnknown, err := harness.store.ReconcileChannelDispatchOutcome(
		context.Background(), stillUnknownInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stillUnknown.Record.Attempt.State != DispatchUnknown ||
		stillUnknown.Record.Attempt.Revision != 2 ||
		stillUnknown.Record.ReconciliationEvidence == nil {
		t.Fatalf("advanced UNKNOWN=%+v", stillUnknown)
	}
	run, err := harness.store.LoadRunForLoop(context.Background(), stillUnknown.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.Frame.Step != corecontract.WaitingReconciliationLoopStep ||
		run.Frame.PendingAttemptID != "" || run.Frame.PendingDispatchAttemptID != "" ||
		run.Frame.WaitingReason != channelUnknownWaitingReason {
		t.Fatalf("UNKNOWN Frame=%+v", run.Frame)
	}

	reconciled, err := harness.store.ReconcileChannelDispatchOutcome(
		context.Background(),
		ReconcileChannelDispatchOutcomeInput{
			Lease:                           stillUnknown.Lease,
			AttemptID:                       stillUnknown.Record.Attempt.AttemptID,
			InvocationID:                    stillUnknown.Record.Attempt.AttemptID,
			Provider:                        stillUnknown.Record.Attempt.Binding.Provider,
			ExpectedAttemptRevision:         stillUnknown.Record.Attempt.Revision,
			Outcome:                         moduleapi.ChannelExecutionSucceeded,
			ReconciliationEvidenceCanonical: []byte(`{"probe":"delivered"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Record.Attempt.AttemptID != begin.Channel.Attempt.AttemptID ||
		reconciled.Record.Attempt.State != DispatchSucceeded ||
		reconciled.Record.Attempt.Revision != 3 ||
		reconciled.Record.Result == nil {
		t.Fatalf("reconciled=%+v", reconciled)
	}
	if counts := channelDispatchCounts(t, harness.store, begin.Channel.Attempt.RunID); counts.DispatchAttempts != 1 {
		t.Fatalf("UNKNOWN created replacement Attempt: %+v", counts)
	}
	terminal, err := harness.store.LoadRunForLoop(context.Background(), reconciled.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Frame.Step != corecontract.TerminatedLoopStep ||
		len(terminal.History) != 1 {
		t.Fatalf("reconciled terminal=%+v", terminal)
	}
}

func TestChannelOrdinaryOutcomeCannotInjectReconciliationEvidence(t *testing.T) {
	harness := newChannelDispatchHarness(
		t,
		"run-channel-evidence-boundary",
		"event-channel-evidence-boundary",
	)
	begin := harness.mustBegin(t)
	input := channelOutcomeFixture(begin, moduleapi.ChannelExecutionUnknown)
	input.ReconciliationEvidenceCanonical = []byte(`{"probe":"not-a-reconciliation"}`)
	if _, err := harness.store.CommitChannelDispatchOutcome(
		context.Background(),
		input,
	); !errors.Is(err, ErrInvalidChannelDispatch) {
		t.Fatalf("ordinary outcome evidence error=%v", err)
	}
	record, err := harness.store.GetChannelDispatchRecord(
		context.Background(),
		begin.Channel.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.State != DispatchPending ||
		record.ReconciliationEvidence != nil ||
		record.Attempt.Revision != begin.Channel.Attempt.Revision {
		t.Fatalf("ordinary outcome evidence changed Attempt=%+v", record)
	}
}

func TestModelTwoFinalAnswerCanAtomicallyBeginChannelDispatch(t *testing.T) {
	store, input := newActionChannelModelTwoFixture(t)
	begin, err := store.CommitModelChannelAndBeginDispatch(
		context.Background(), input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !begin.Applied || !begin.GatewayAllowed ||
		begin.Model.Attempt.LogicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		begin.Model.Attempt.SourceDispatchAttemptID == "" ||
		begin.Channel.Attempt.SourceModelAttemptID != begin.Model.Attempt.AttemptID ||
		begin.Channel.Attempt.State != DispatchPending {
		t.Fatalf("model-2 Channel begin=%+v", begin)
	}
	run, err := store.LoadRunForLoop(context.Background(), begin.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.ModelDispatches) != 2 || len(run.ActionDispatches) != 1 ||
		len(run.ChannelDispatches) != 1 || len(run.History) != 1 ||
		run.History[0].SourceAttemptID != begin.Model.Attempt.AttemptID ||
		run.Frame.Step != corecontract.ChannelPendingLoopStep {
		t.Fatalf("model-2 Channel Run=%+v", run)
	}
}

func newChannelDispatchHarness(
	t *testing.T,
	runID string,
	providerEventID string,
) *channelDispatchHarness {
	t.Helper()
	ctx := context.Background()
	fixture := newAdmissionCommitFixture(t)
	fixture.intent.Deadline = time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seed := mustSeedChannelCursor(t, fixture, bindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	ingress := channelAcceptedFixture(
		t, fixture, bindingDigest, seed, providerEventID, runID,
	)
	admission, err := fixture.store.CommitChannelIngressAndRunAdmission(ctx, ingress)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.store.AcquireRunLease(
		ctx,
		AcquireRunLeaseInput{
			RunID:                 admission.Admission.RunID,
			OwnerID:               "channel-store-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				{Role: moduleapi.ModelRoleSystem, Content: "Follow Core constraints."},
				{Role: moduleapi.ModelRoleUser, Content: "Say hello."},
			},
			Parameters: json.RawMessage(`{"max_tokens":64,"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelBegin, err := fixture.store.BeginModelDispatch(
		ctx,
		BeginModelDispatchInput{
			Lease:            lease,
			AttemptID:        "model-" + runID,
			LogicalStepID:    corecontract.PureChatModelLogicalStepIDV1,
			RequestCanonical: requestCanonical,
			Deadline:         time.Now().UTC().Add(45 * time.Minute).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelBegin.ConsumeModelInvocationPermit() {
		t.Fatal("fixture Model invocation permit unavailable")
	}
	const answer = "Hello from the Channel run."
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     answer,
			ProviderRequestID: "provider-" + runID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		ingress.Ingress.Envelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		ingress.Admission.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, proposalCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		member.MemberSnapshotDigest,
		envelope.EndpointID,
		ingress.Ingress.IngressKey,
		envelope.ReplyTarget,
		answer,
		json.RawMessage(`{"text":"Hello from the Channel run."}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &channelDispatchHarness{
		store:      fixture.store,
		fixture:    fixture,
		ingress:    ingress,
		modelBegin: modelBegin,
		beginInput: CommitModelChannelAndBeginDispatchInput{
			Lease:                        modelBegin.Lease,
			ModelAttemptID:               modelBegin.Attempt.AttemptID,
			InvocationID:                 modelBegin.Attempt.AttemptID,
			Provider:                     modelBegin.Attempt.Binding.Provider,
			ExpectedModelAttemptRevision: modelBegin.Attempt.Revision,
			OutputCanonical:              outputCanonical,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t, `{"id":"provider-`+runID+`","status":"completed"}`,
			),
			ProviderRequestID: "provider-" + runID,
			DispatchAttemptID: "channel-" + runID,
			ProposalCanonical: proposalCanonical,
			Deadline:          time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond),
		},
	}
}

func newActionChannelModelTwoFixture(
	t *testing.T,
) (*Store, CommitModelChannelAndBeginDispatchInput) {
	t.Helper()
	ctx := context.Background()
	fixture := newAdmissionCommitFixture(t)
	fixture.intent.Deadline = time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	channelBindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seed := mustSeedChannelCursor(t, fixture, channelBindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	ingress := channelAcceptedFixture(
		t, fixture, channelBindingDigest, seed,
		"event-action-channel", "run-action-channel",
	)

	actionPort := moduleapi.PortRef{
		Name: moduleapi.PortNameActionProvider, ExactVersion: moduleapi.PortVersionV1,
	}
	actionProvider := installActionStoreProvider(t, fixture.store, fixture.intent.TenantID)
	_, actionConfigCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID: "text.stats", ProviderActionID: "builtin.text.stats",
				LocalEffectClass: moduleapi.EffectNone, MaxResultBytes: 1024,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, actionAuthorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 fixture.intent.TenantID,
			AllowedWorkspaceIDs:      []string{fixture.intent.WorkspaceID},
			AllowedProviderActionIDs: []string{"builtin.text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actionConfigRef := putPublicationJSON(
		t, fixture.store, ContentConfig, actionConfigCanonical,
	)
	actionAuthorityRef := putPublicationJSON(
		t, fixture.store, ContentAuthorityCeiling, actionAuthorityCanonical,
	)
	basis, control, catalog, err := fixture.store.LoadPublishedBasis(
		ctx, fixture.intent.TenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	actionContextPolicy := putPublicationContextPolicyWithLimits(
		t, fixture.store, 20_000, 64,
	)
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != fixture.intent.ProfileID {
			continue
		}
		control.Profiles[index].ContextPolicy = actionContextPolicy
		control.Profiles[index].Bindings = append(
			control.Profiles[index].Bindings,
			controlcontract.BindingSpec{
				Port:                actionPort,
				InstanceID:          actionProvider.InstanceID,
				ConfigRef:           actionConfigRef,
				AuthorityCeilingRef: actionAuthorityRef,
				FailurePolicy:       moduleapi.FailureRequired,
			},
		)
	}
	control.SnapshotID = "control-action-channel"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-action-channel"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: actionProvider, Provides: []moduleapi.PortRef{actionPort},
	})
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publishChannelFixtureBasis(
		t, fixture, controlRef, controlCanonical, catalogRef, catalogCanonical,
	)
	basis = fixture.basis

	schema, err := moduleapi.CanonicalizeActionInputSchemaV1(
		json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID: "text.stats", ProviderActionID: "builtin.text.stats",
			BindingIndex:   0,
			Description:    "Count deterministic text statistics.",
			InputSchema:    schema,
			EffectClass:    moduleapi.EffectNone,
			MaxResultBytes: 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ingress.Ingress.PublishedBasis = basis
	ingress.Admission.PublishedBasis = basis
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:    ingress.Admission.IntentCanonical,
			IntentDigest:       ingress.Admission.IntentDigest,
			RunID:              "run-action-channel",
			MemberID:           "member-primary",
			RecoveryRootRef:    "recovery/run-action-channel",
			PublishedBasis:     basis,
			ControlCanonical:   fixture.controlCanonical,
			CatalogCanonical:   fixture.catalogCanonical,
			ActionMaterializer: fixedActionMaterializer{definition: definition},
			ActionBindingMaterials: []actionmaterializer.BindingMaterialV1{{
				ConfigCanonical:    actionConfigCanonical,
				AuthorityCanonical: actionAuthorityCanonical,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ingress.Admission.MemberSnapshotCanonical = compiled.MemberSnapshotCanonical
	ingress.Admission.RunManifestCanonical = compiled.RunManifestCanonical
	admission, err := fixture.store.CommitChannelIngressAndRunAdmission(ctx, ingress)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.store.AcquireRunLease(
		ctx,
		AcquireRunLeaseInput{
			RunID:                 admission.Admission.RunID,
			OwnerID:               "action-channel-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   90 * time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	modelOneRequest, modelOneCompilation := compileActionModelOne(t, fixture.store, run)
	modelOne, err := fixture.store.BeginModelDispatch(
		ctx,
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "model-action-channel-1",
			LogicalStepID:               corecontract.FirstModelLogicalStepIDV1,
			ContextCompilationCanonical: modelOneCompilation,
			RequestCanonical:            modelOneRequest,
			Deadline:                    time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelOne.ConsumeModelInvocationPermit() {
		t.Fatal("model-1 invocation permit unavailable")
	}
	_, modelOneOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID: definition.PublicActionID, CanonicalInput: json.RawMessage(`{}`),
			},
			ProviderRequestID: "provider-action-channel-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, actionProposal, _, err := corecontract.NewActionProposalV1(
		compiled.MemberSnapshot.MemberSnapshotDigest,
		definition,
		json.RawMessage(`{}`),
		json.RawMessage(`{"prepared":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	actionBegin, err := fixture.store.CommitModelActionAndBeginDispatch(
		ctx,
		CommitModelActionAndBeginDispatchInput{
			Lease:                        modelOne.Lease,
			ModelAttemptID:               modelOne.Attempt.AttemptID,
			InvocationID:                 modelOne.Attempt.AttemptID,
			Provider:                     modelOne.Attempt.Binding.Provider,
			ExpectedModelAttemptRevision: modelOne.Attempt.Revision,
			OutputCanonical:              modelOneOutput,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t, `{"id":"provider-action-channel-1","status":"completed"}`,
			),
			ProviderRequestID: "provider-action-channel-1",
			DispatchAttemptID: "action-action-channel",
			ProposalCanonical: actionProposal,
			Deadline:          time.Now().UTC().Add(50 * time.Minute).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actionDone, err := fixture.store.CommitActionDispatchOutcome(
		ctx,
		actionOutcomeInput(
			actionBegin.Action,
			actionBegin.Lease,
			moduleapi.ActionExecutionSucceeded,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	actionHarness := &actionStoreHarness{
		store: fixture.store, definition: definition, modelBegin: modelOne,
	}
	modelTwo, err := fixture.store.BeginModelDispatch(
		ctx, actionHarness.secondModelInput(t, actionDone),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelTwo.ConsumeModelInvocationPermit() {
		t.Fatal("model-2 invocation permit unavailable")
	}
	const finalAnswer = "The text statistics are ready for delivery."
	_, modelTwoOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     finalAnswer,
			ProviderRequestID: "provider-action-channel-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		ingress.Ingress.Envelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, channelProposal, _, err := moduleapi.NewChannelSendProposalV1(
		compiled.MemberSnapshot.MemberSnapshotDigest,
		envelope.EndpointID,
		ingress.Ingress.IngressKey,
		envelope.ReplyTarget,
		finalAnswer,
		json.RawMessage(`{"text":"The text statistics are ready for delivery."}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.store, CommitModelChannelAndBeginDispatchInput{
		Lease:                        modelTwo.Lease,
		ModelAttemptID:               modelTwo.Attempt.AttemptID,
		InvocationID:                 modelTwo.Attempt.AttemptID,
		Provider:                     modelTwo.Attempt.Binding.Provider,
		ExpectedModelAttemptRevision: modelTwo.Attempt.Revision,
		OutputCanonical:              modelTwoOutput,
		UsageReceiptCanonical: modelUsageOutcomeCanonical(
			t, `{"id":"provider-action-channel-2","status":"completed"}`,
		),
		ProviderRequestID: "provider-action-channel-2",
		DispatchAttemptID: "channel-action-channel",
		ProposalCanonical: channelProposal,
		Deadline:          time.Now().UTC().Add(20 * time.Minute).Truncate(time.Microsecond),
	}
}

func (harness *channelDispatchHarness) mustBegin(
	t *testing.T,
) CommitModelChannelAndBeginDispatchResult {
	t.Helper()
	result, err := harness.store.CommitModelChannelAndBeginDispatch(
		context.Background(), harness.beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (harness *channelDispatchHarness) replyTarget(t *testing.T) json.RawMessage {
	t.Helper()
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		harness.ingress.Ingress.Envelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(envelope.ReplyTarget)
}

func (harness *channelDispatchHarness) modelMemberDigest(t *testing.T) string {
	t.Helper()
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		harness.ingress.Admission.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return member.MemberSnapshotDigest
}

func badProposalEndpoint(harness *channelDispatchHarness) string {
	return "endpoint-default"
}

func channelOutcomeFixture(
	begin CommitModelChannelAndBeginDispatchResult,
	outcome moduleapi.ChannelExecutionOutcomeV1,
) CommitChannelDispatchOutcomeInput {
	input := CommitChannelDispatchOutcomeInput{
		Lease:                   begin.Lease,
		AttemptID:               begin.Channel.Attempt.AttemptID,
		InvocationID:            begin.Channel.Attempt.AttemptID,
		Provider:                begin.Channel.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Channel.Attempt.Revision,
		Outcome:                 outcome,
	}
	switch outcome {
	case moduleapi.ChannelExecutionSucceeded:
		input.ProviderReceiptCanonical = []byte(`{"message_id":"message-1"}`)
		input.ExternalOperationID = "external-message-1"
	case moduleapi.ChannelExecutionFailed:
		input.ProviderReceiptCanonical = []byte(`{"request_id":"request-1"}`)
		input.ErrorClassification = "DELIVERY_REJECTED"
	case moduleapi.ChannelExecutionUnknown:
		input.ProviderReceiptCanonical = []byte(`{"request_id":"request-1"}`)
		input.ExternalOperationID = "external-message-1"
		input.UnknownReason = "TRANSPORT_TIMEOUT"
	}
	return input
}

type channelDispatchCountSnapshot struct {
	DispatchAttempts int
	HistoryEntries   int
	ModelResults     int
	ChannelProposals int
	RunEvents        int
}

func channelDispatchCounts(
	t *testing.T,
	store *Store,
	runID string,
) channelDispatchCountSnapshot {
	t.Helper()
	var result channelDispatchCountSnapshot
	if err := store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM history_entries WHERE run_id=?),
			(SELECT COUNT(*) FROM content_records WHERE kind='MODEL_RESULT'),
			(SELECT COUNT(*) FROM content_records WHERE kind='CHANNEL_SEND_PROPOSAL'),
			(SELECT COUNT(*) FROM run_events WHERE run_id=? )
	`, runID, runID, runID).Scan(
		&result.DispatchAttempts,
		&result.HistoryEntries,
		&result.ModelResults,
		&result.ChannelProposals,
		&result.RunEvents,
	); err != nil {
		t.Fatal(err)
	}
	return result
}
