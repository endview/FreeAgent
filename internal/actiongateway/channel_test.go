package actiongateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestExecuteChannelRejectsCallerConstructedGrantBeforeAnyLookup(
	t *testing.T,
) {
	run, _, grant := validChannelGatewayClosure(t)
	boundary := &gatewayStoreBoundary{run: run}
	registry := &noCallChannelRegistry{}
	gateway, err := New(boundary, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ExecuteChannel(
		context.Background(),
		grant,
	); !errors.Is(err, ErrInvalidGateway) {
		t.Fatalf("caller-constructed grant error=%v", err)
	}
	if boundary.loadCalls.Load() != 0 ||
		boundary.activation.Load() != 0 ||
		registry.calls.Load() != 0 {
		t.Fatalf(
			"grant without Store permit crossed Gateway: load=%d activation=%d registry=%d",
			boundary.loadCalls.Load(),
			boundary.activation.Load(),
			registry.calls.Load(),
		)
	}
}

func TestValidateChannelGrantClosureFreezesRunBindingAuthorityAndProposal(
	t *testing.T,
) {
	run, _, grant := validChannelGatewayClosure(t)
	now := grant.Channel.Attempt.Deadline.Add(-time.Minute)
	record, proposal, err := validateChannelGrantClosure(
		run,
		grant,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.AttemptID != grant.Channel.Attempt.AttemptID ||
		proposal.EndpointID != grant.Channel.Attempt.EndpointID {
		t.Fatalf("validated Channel closure differs: %+v %+v", record, proposal)
	}

	frameDrift := run
	frameDrift.Frame.Step = corecontract.TerminatedLoopStep
	if _, _, err := validateChannelGrantClosure(
		frameDrift,
		grant,
		now,
	); err == nil {
		t.Fatal("Frame drift was accepted")
	}

	bindingDrift := run
	bindingDrift.ChannelDispatches = append(
		[]currentstore.ChannelDispatchRecord(nil),
		run.ChannelDispatches...,
	)
	bindingDrift.ChannelDispatches[0].Attempt.BindingIndex++
	if _, _, err := validateChannelGrantClosure(
		bindingDrift,
		grant,
		now,
	); err == nil {
		t.Fatal("persisted Binding drift was accepted")
	}

	proposalDrift := run
	proposalDrift.ChannelDispatches = append(
		[]currentstore.ChannelDispatchRecord(nil),
		run.ChannelDispatches...,
	)
	proposalDrift.ChannelDispatches[0].Proposal.CanonicalBytes = bytes.Replace(
		proposalDrift.ChannelDispatches[0].Proposal.CanonicalBytes,
		[]byte(`"endpoint-1"`),
		[]byte(`"endpoint-2"`),
		1,
	)
	if _, _, err := validateChannelGrantClosure(
		proposalDrift,
		grant,
		now,
	); err == nil {
		t.Fatal("Proposal drift was accepted")
	}

	historyMissing := run
	historyMissing.History = nil
	if _, _, err := validateChannelGrantClosure(
		historyMissing,
		grant,
		now,
	); err == nil {
		t.Fatal("missing source Model History was accepted")
	}

	replyTargetDrift := run
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		run.ChannelIngressEnvelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope.ReplyTarget = json.RawMessage(`{"conversation":"c-2"}`)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	envelopeContent := channelGatewayContent(
		t,
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	replyTargetDrift.ChannelIngressEnvelope = &envelopeContent
	receipt := *run.ChannelIngress
	receipt.EnvelopeRef = envelopeContent.Digest
	replyTargetDrift.ChannelIngress = &receipt
	if _, _, err := validateChannelGrantClosure(
		replyTargetDrift,
		grant,
		now,
	); err == nil {
		t.Fatal("reply target drift from accepted ingress was accepted")
	}

	denied := run
	authorityIndex := contentIndex(denied.Contents, grant.Channel.Attempt.Binding.AuthorityCeilingRef)
	if authorityIndex < 0 {
		t.Fatal("authority fixture content is absent")
	}
	_, deniedAuthority, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            run.Manifest.TenantID,
			AllowedWorkspaceIDs: []string{run.Member.Workspace.ID},
			AllowedEndpointIDs:  []string{grant.Channel.Attempt.EndpointID},
			AllowReceive:        true,
			AllowSend:           false,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	denied.Contents = append([]currentstore.ContentRecord(nil), run.Contents...)
	denied.Contents[authorityIndex].CanonicalBytes = deniedAuthority
	if _, _, err := validateChannelGrantClosure(
		denied,
		grant,
		now,
	); err == nil {
		t.Fatal("deny-only Channel Authority was bypassed")
	}
}

func TestNormalizeChannelExecutorResultPreservesPostBoundaryCertainty(
	t *testing.T,
) {
	provider := channelGatewayProvider()
	request := moduleapi.ChannelExecutionRequestV1{
		SchemaVersion:       moduleapi.ChannelExecutionRequestSchemaV1,
		AttemptID:           "channel-attempt-1",
		EndpointID:          "endpoint-1",
		IngressKey:          strings.Repeat("a", moduleapi.SHA256HexLength),
		ProposalDigest:      strings.Repeat("b", moduleapi.SHA256HexLength),
		AssistantTextDigest: strings.Repeat("c", moduleapi.SHA256HexLength),
		ReplyTarget:         json.RawMessage(`{"conversation":"c-1"}`),
		PreparedPayload:     json.RawMessage(`{"message":"answer"}`),
	}
	request, _, err := moduleapi.NewChannelExecutionRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, _, err := moduleapi.NewChannelExecutionResultV1(
		moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           request.AttemptID,
			Outcome:             moduleapi.ChannelExecutionSucceeded,
			ExternalOperationID: "delivery-1",
			ProviderReceipt:     json.RawMessage(`{"accepted":true}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeChannelExecutorResult(request, provider, succeeded, nil)
	if got.Outcome != moduleapi.ChannelExecutionSucceeded ||
		got.ExternalOperationID != "delivery-1" ||
		!bytes.Equal(got.ProviderReceipt, succeeded.ProviderReceipt) {
		t.Fatalf("SUCCEEDED result changed: %+v", got)
	}

	postBoundaryError := normalizeChannelExecutorResult(
		request,
		provider,
		moduleapi.ChannelExecutionResultV1{},
		contextDeadlineError{},
	)
	if postBoundaryError.Outcome != moduleapi.ChannelExecutionUnknown ||
		postBoundaryError.UnknownReason != unknownChannelExecutorError {
		t.Fatalf("post-boundary error was not UNKNOWN: %+v", postBoundaryError)
	}

	mismatch := succeeded
	mismatch.AttemptID = "another-attempt"
	got = normalizeChannelExecutorResult(request, provider, mismatch, nil)
	if got.Outcome != moduleapi.ChannelExecutionUnknown ||
		got.UnknownReason != unknownInvalidChannelExecutorResult {
		t.Fatalf("mismatched result was not UNKNOWN: %+v", got)
	}

	noncanonical := succeeded
	noncanonical.ProviderReceipt = json.RawMessage(`{ "accepted": true }`)
	got = normalizeChannelExecutorResult(request, provider, noncanonical, nil)
	if got.Outcome != moduleapi.ChannelExecutionUnknown {
		t.Fatalf("noncanonical receipt was accepted: %+v", got)
	}

	failed, _, err := moduleapi.NewChannelExecutionResultV1(
		moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           request.AttemptID,
			Outcome:             moduleapi.ChannelExecutionFailed,
			ErrorClassification: "PROVIDER_REJECTED_BEFORE_DELIVERY",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got = normalizeChannelExecutorResult(request, provider, failed, nil)
	if got.Outcome != moduleapi.ChannelExecutionFailed ||
		got.ErrorClassification != failed.ErrorClassification {
		t.Fatalf("confirmed FAILED result changed: %+v", got)
	}
}

type contextDeadlineError struct{}

func (contextDeadlineError) Error() string { return "deadline after dispatch" }

type noCallChannelRegistry struct {
	calls atomic.Uint32
}

func (registry *noCallChannelRegistry) ResolveExact(
	context.Context,
	string,
	string,
) (modulehost.ModuleInvoker, error) {
	registry.calls.Add(1)
	return noCallChannelInvoker{}, nil
}

type noCallChannelInvoker struct{}

func (noCallChannelInvoker) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, errors.New("must not be invoked")
}

func validChannelGatewayClosure(
	t *testing.T,
) (
	currentstore.RunForLoop,
	currentstore.ChannelDispatchRecord,
	currentstore.CommitModelChannelAndBeginDispatchResult,
) {
	t.Helper()
	provider := channelGatewayProvider()
	config, configCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: "loopback-http/v1",
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(`{}`),
		},
	)
	if err != nil || config.SecretRef == "" {
		t.Fatalf("Channel config: %+v %v", config, err)
	}
	configContent := channelGatewayContent(
		t,
		currentstore.ContentConfig,
		configCanonical,
	)
	authority, authorityCanonical, err :=
		moduleapi.NewChannelAuthorityCeilingV1(
			moduleapi.ChannelAuthorityCeilingV1{
				SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
				TenantID:            "tenant-1",
				AllowedWorkspaceIDs: []string{"workspace-1"},
				AllowedEndpointIDs:  []string{"endpoint-1"},
				AllowReceive:        true,
				AllowSend:           true,
				MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
			},
		)
	if err != nil || !authority.AllowSend {
		t.Fatalf("Channel authority: %+v %v", authority, err)
	}
	authorityContent := channelGatewayContent(
		t,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	binding := moduleapi.PortBinding{
		Provider:            provider,
		ConfigRef:           configContent.Digest,
		AuthorityCeilingRef: authorityContent.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	bindingCanonical, _, err := moduleapi.CanonicalChannelEndpointBindingV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	memberDigest := strings.Repeat("d", moduleapi.SHA256HexLength)
	assistantText := "channel answer"
	_, modelOutputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     assistantText,
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelResult := channelGatewayContent(
		t,
		currentstore.ContentModelResult,
		modelOutputCanonical,
	)
	ingressKey := strings.Repeat("e", moduleapi.SHA256HexLength)
	replyTarget := json.RawMessage(`{"conversation":"c-1"}`)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "endpoint-1",
			ProviderEventID: "provider-event-1",
			ExternalUserID:  "external-user-1",
			Message:         "channel question",
			ReplyTarget:     replyTarget,
			CursorBefore:    json.RawMessage(`{"offset":0}`),
			CursorAfter:     json.RawMessage(`{"offset":1}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelopeContent := channelGatewayContent(
		t,
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	_, proposalCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		memberDigest,
		"endpoint-1",
		ingressKey,
		replyTarget,
		assistantText,
		json.RawMessage(`{"message":"channel answer"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	proposalContent := channelGatewayContent(
		t,
		currentstore.ContentChannelSendProposal,
		proposalCanonical,
	)
	deadline := time.Date(2026, time.August, 4, 8, 0, 0, 0, time.UTC)
	lease := currentstore.RunLease{
		RunID:         "run-1",
		OwnerID:       "owner-1",
		LeaseEpoch:    1,
		RunRevision:   3,
		FrameRevision: 2,
		ExpiresAt:     deadline.Add(time.Hour),
	}
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.ChannelPendingLoopStep,
		corecontract.AttemptKindChannel,
		corecontract.ChannelSendLogicalStepIDV1,
		"channel-attempt-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	attempt := currentstore.ChannelDispatchAttemptRecord{
		AttemptID:            "channel-attempt-1",
		LogicalOperationKey:  strings.Repeat("f", moduleapi.SHA256HexLength),
		RunID:                lease.RunID,
		MemberID:             "member-1",
		LogicalStepID:        corecontract.ChannelSendLogicalStepIDV1,
		SourceModelAttemptID: "model-attempt-1",
		FrameRevision:        1,
		MemberSnapshotDigest: memberDigest,
		BindingIndex:         0,
		Binding:              binding,
		BindingCanonical:     bindingCanonical,
		EndpointID:           "endpoint-1",
		IngressKey:           ingressKey,
		ProposalRef:          proposalContent.Digest,
		EffectClass:          moduleapi.EffectIrreversibleWrite,
		MaxResultBytes:       moduleapi.MaxChannelProviderReceiptBytesV1,
		Deadline:             deadline,
		UsageLedgerRef:       "usage-ledger-1",
		State:                currentstore.DispatchPending,
		Revision:             0,
	}
	persisted := currentstore.ChannelDispatchRecord{
		Attempt:  attempt,
		Proposal: proposalContent,
	}
	contents := []currentstore.ContentRecord{
		configContent,
		authorityContent,
	}
	sort.Slice(contents, func(left, right int) bool {
		return contents[left].Digest < contents[right].Digest
	})
	run := currentstore.RunForLoop{
		RunID:       lease.RunID,
		RunRevision: lease.RunRevision,
		Manifest: corecontract.RunManifest{
			TenantID: "tenant-1",
			Deadline: deadline.Add(30 * time.Minute),
		},
		Member: corecontract.MemberExecutionSnapshot{
			MemberID:             attempt.MemberID,
			MemberSnapshotDigest: memberDigest,
			Workspace: corecontract.WorkspaceRef{
				ID: "workspace-1",
			},
			PortPlans: []moduleapi.PortPlan{{
				Port:     channelPortV1,
				Bindings: []moduleapi.PortBinding{binding},
			}},
		},
		Frame: currentstore.LoopFrameRecord{
			RunID:                    lease.RunID,
			Revision:                 lease.FrameRevision,
			Step:                     corecontract.ChannelPendingLoopStep,
			UsageLedgerRef:           attempt.UsageLedgerRef,
			Continuation:             continuation,
			PendingDispatchAttemptID: attempt.AttemptID,
		},
		Contents: contents,
		ModelDispatches: []currentstore.ModelDispatchRecord{{
			Attempt: currentstore.ModelDispatchAttemptRecord{
				AttemptID: attempt.SourceModelAttemptID,
				State:     corecontract.ModelAttemptSucceeded,
				ResultRef: modelResult.Digest,
			},
		}},
		ChannelIngress: &currentstore.ChannelIngressReceipt{
			EndpointID:  attempt.EndpointID,
			Disposition: currentstore.ChannelIngressAccepted,
			IngressKey:  attempt.IngressKey,
			EnvelopeRef: envelopeContent.Digest,
			RunID:       lease.RunID,
		},
		ChannelIngressEnvelope: &envelopeContent,
		ChannelDispatches:      []currentstore.ChannelDispatchRecord{persisted},
		History: []currentstore.HistoryEntryRecord{{
			Sequence:        1,
			MemberID:        attempt.MemberID,
			Role:            string(moduleapi.ModelRoleAssistant),
			Content:         modelResult,
			SourceAttemptID: attempt.SourceModelAttemptID,
		}},
	}
	grant := currentstore.CommitModelChannelAndBeginDispatchResult{
		Channel:        persisted,
		Lease:          lease,
		Applied:        true,
		GatewayAllowed: true,
	}
	return run, persisted, grant
}

func channelGatewayProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "channel.loopback",
		Version:            "v1",
		ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
		InstanceID:         "channel-loopback-1",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "freeagent.adapter.channel.loopback-http/v1",
		ActivationRevision: 1,
	}
}

func channelGatewayContent(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentRecord {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.ContentRecord{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: bytes.Clone(canonical),
		SizeBytes:      int64(len(canonical)),
	}
}

func contentIndex(contents []currentstore.ContentRecord, digest string) int {
	for index := range contents {
		if contents[index].Digest == digest {
			return index
		}
	}
	return -1
}
