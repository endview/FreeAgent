package moduleapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestChannelBindingConfigAndAuthorityAreStrictCanonicalClosures(t *testing.T) {
	config, canonical, err := NewChannelBindingConfigV1(ChannelBindingConfigV1{
		SchemaVersion:   ChannelBindingConfigSchemaV1,
		AdapterProtocol: "loopback-http/v1",
		SecretRef:       "placeholder",
		Parameters:      json.RawMessage(`{ "outbound_url": "http://127.0.0.1:8090/deliver" }`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(config.Parameters) != `{"outbound_url":"http://127.0.0.1:8090/deliver"}` {
		t.Fatalf("parameters were not canonicalized: %s", config.Parameters)
	}
	if _, err := RestoreChannelBindingConfigV1(canonical); err != nil {
		t.Fatal(err)
	}
	credentialKey := "bearer_" + "token"
	invalidParameters, err := json.Marshal(map[string]string{credentialKey: "plaintext"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewChannelBindingConfigV1(ChannelBindingConfigV1{
		SchemaVersion:   ChannelBindingConfigSchemaV1,
		AdapterProtocol: "loopback-http/v1",
		SecretRef:       "placeholder",
		Parameters:      invalidParameters,
	}); err == nil {
		t.Fatal("plaintext secret material was accepted in Parameters")
	}
	referenceField := []byte(`"secret_` + `ref":`)
	unknownReferenceField := []byte(`"unknown":true,"secret_` + `ref":`)
	unknown := bytes.Replace(
		canonical,
		referenceField,
		unknownReferenceField,
		1,
	)
	if _, err := RestoreChannelBindingConfigV1(unknown); err == nil {
		t.Fatal("unknown config field was accepted")
	}

	ceiling, ceilingCanonical, err := NewChannelAuthorityCeilingV1(
		ChannelAuthorityCeilingV1{
			SchemaVersion:       ChannelAuthorityCeilingSchemaV1,
			TenantID:            "tenant-1",
			AllowedWorkspaceIDs: []string{"workspace-z", "workspace-a"},
			AllowedEndpointIDs:  []string{"endpoint-z", "endpoint-a"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ceiling.AllowedWorkspaceIDs, ","); got != "workspace-a,workspace-z" {
		t.Fatalf("workspace set is not canonical: %s", got)
	}
	ceiling.AllowedWorkspaceIDs[0] = "mutated"
	restored, err := RestoreChannelAuthorityCeilingV1(ceilingCanonical)
	if err != nil || restored.AllowedWorkspaceIDs[0] != "workspace-a" {
		t.Fatalf("authority closure aliases input or failed restore: %+v %v", restored, err)
	}
	invalid := restored
	invalid.AllowedEndpointIDs = []string{"*"}
	if _, _, err := NewChannelAuthorityCeilingV1(invalid); err == nil {
		t.Fatal("wildcard endpoint authority was accepted")
	}
}

func TestChannelEndpointBindingDigestIsCanonicalAndCoversExactProvider(t *testing.T) {
	binding := PortBinding{
		Provider: ActivatedModuleRef{
			ModuleID:           "firstparty.loopback.channel",
			Version:            "v1",
			ArtifactDigest:     strings.Repeat("a", 64),
			InstanceID:         "channel-loopback",
			ExecutionClass:     ExecutionTrustedInProcess,
			AdapterIdentity:    "loopback-http/v1",
			ActivationRevision: 1,
		},
		ConfigRef:           strings.Repeat("b", 64),
		AuthorityCeilingRef: strings.Repeat("c", 64),
		StaticContextRefs:   []string{},
		FailurePolicy:       FailureRequired,
	}
	canonical, digest, err := CanonicalChannelEndpointBindingV1(binding)
	if err != nil || !ValidSHA256(digest) || len(canonical) == 0 {
		t.Fatalf("canonical=%s digest=%s err=%v", canonical, digest, err)
	}
	again, err := ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil || again != digest {
		t.Fatalf("repeat digest=%s err=%v want %s", again, err, digest)
	}
	binding.Provider.ActivationRevision++
	changed, err := ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil || changed == digest {
		t.Fatalf("provider change digest=%s err=%v", changed, err)
	}
}

func TestChannelInboundEnvelopeIsBoundedCanonicalAndOpaque(t *testing.T) {
	envelope, canonical, err := NewChannelInboundEnvelopeV1(
		ChannelInboundEnvelopeV1{
			SchemaVersion:   ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "endpoint-1",
			ProviderEventID: "event-1",
			ExternalUserID:  "external-1",
			Message:         "hello\nworld",
			ReplyTarget:     json.RawMessage(`{ "conversation": "c-1" }`),
			CursorBefore:    json.RawMessage(`{ "offset": 1 }`),
			CursorAfter:     json.RawMessage(`{ "offset": 2 }`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope.CursorAfter) != `{"offset":2}` {
		t.Fatalf("cursor was not canonicalized: %s", envelope.CursorAfter)
	}
	restored, err := RestoreChannelInboundEnvelopeV1(canonical)
	if err != nil || restored.Message != "hello\nworld" {
		t.Fatalf("restore envelope: %+v %v", restored, err)
	}
	invalid := envelope
	invalid.CursorAfter = bytes.Clone(invalid.CursorBefore)
	if _, _, err := NewChannelInboundEnvelopeV1(invalid); err == nil {
		t.Fatal("non-advancing cursor was accepted")
	}
	invalid = envelope
	invalid.ReplyTarget = json.RawMessage(`[]`)
	if _, _, err := NewChannelInboundEnvelopeV1(invalid); err == nil {
		t.Fatal("non-object reply target was accepted")
	}
}

func TestChannelPrepareProposalAndExecutionCloseExactIdentity(t *testing.T) {
	request, requestCanonical, err := NewChannelPrepareSendRequestV1(
		ChannelPrepareSendRequestV1{
			SchemaVersion: ChannelPrepareSendRequestSchemaV1,
			EndpointID:    "endpoint-1",
			ReplyTarget:   json.RawMessage(`{"conversation":"c-1"}`),
			AssistantText: "answer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreChannelPrepareSendRequestV1(requestCanonical); err != nil {
		t.Fatal(err)
	}
	prepared := json.RawMessage(`{"conversation":"c-1","text":"answer"}`)
	proposal, proposalCanonical, proposalDigest, err := NewChannelSendProposalV1(
		strings.Repeat("a", SHA256HexLength),
		request.EndpointID,
		strings.Repeat("b", SHA256HexLength),
		request.ReplyTarget,
		request.AssistantText,
		prepared,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantTextDigest, err := ChannelAssistantTextDigestV1("answer")
	if err != nil || proposal.AssistantTextDigest != wantTextDigest {
		t.Fatalf("assistant text digest = %q, want %q (%v)", proposal.AssistantTextDigest, wantTextDigest, err)
	}
	if _, err := RestoreChannelSendProposalV1(
		proposalCanonical,
		proposalDigest,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreChannelSendProposalV1(
		proposalCanonical,
		strings.Repeat("c", SHA256HexLength),
	); err == nil {
		t.Fatal("proposal digest mismatch was accepted")
	}

	execution, executionCanonical, err := NewChannelExecutionRequestV1(
		ChannelExecutionRequestV1{
			SchemaVersion:       ChannelExecutionRequestSchemaV1,
			AttemptID:           "dispatch-1",
			EndpointID:          proposal.EndpointID,
			IngressKey:          proposal.IngressKey,
			ProposalDigest:      proposalDigest,
			AssistantTextDigest: proposal.AssistantTextDigest,
			ReplyTarget:         proposal.ReplyTarget,
			PreparedPayload:     proposal.PreparedPayload,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreChannelExecutionRequestV1(executionCanonical); err != nil {
		t.Fatal(err)
	}
	succeeded, succeededCanonical, err := NewChannelExecutionResultV1(
		ChannelExecutionResultV1{
			SchemaVersion:       ChannelExecutionResultSchemaV1,
			AttemptID:           execution.AttemptID,
			Outcome:             ChannelExecutionSucceeded,
			ExternalOperationID: "delivery-1",
			ProviderReceipt:     json.RawMessage(`{"status":"accepted"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreChannelExecutionResultV1(succeededCanonical); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChannelExecutionResultForRequestV1(execution, succeeded); err != nil {
		t.Fatal(err)
	}
	succeeded.AttemptID = "another-attempt"
	if err := ValidateChannelExecutionResultForRequestV1(execution, succeeded); err == nil {
		t.Fatal("execution result for a different Attempt was accepted")
	}

	for _, invalid := range []ChannelExecutionResultV1{
		{SchemaVersion: ChannelExecutionResultSchemaV1, AttemptID: "dispatch-1", Outcome: ChannelExecutionSucceeded},
		{SchemaVersion: ChannelExecutionResultSchemaV1, AttemptID: "dispatch-1", Outcome: ChannelExecutionFailed},
		{SchemaVersion: ChannelExecutionResultSchemaV1, AttemptID: "dispatch-1", Outcome: ChannelExecutionUnknown},
	} {
		if _, _, err := NewChannelExecutionResultV1(invalid); err == nil {
			t.Fatalf("invalid terminal combination was accepted: %+v", invalid)
		}
	}
}

func TestChannelPortPlanIsSingleRequiredTrustedProvider(t *testing.T) {
	binding := validPortBinding(
		"channel.loopback",
		"channel-loopback",
		FailureRequired,
	)
	binding.Provider.ExecutionClass = ExecutionTrustedInProcess
	plan := PortPlan{
		Port: PortRef{
			Name:         PortNameChannelTransport,
			ExactVersion: PortVersionV1,
		},
		Bindings: []PortBinding{binding},
	}
	if _, err := NewPortPlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Bindings = append(plan.Bindings, binding)
	if _, err := NewPortPlan(plan); err == nil {
		t.Fatal("multiple Channel bindings were accepted")
	}
	plan.Bindings = plan.Bindings[:1]
	plan.Bindings[0].FailurePolicy = FailureOptional
	if _, err := NewPortPlan(plan); err == nil {
		t.Fatal("OPTIONAL Channel binding was accepted")
	}
	plan.Bindings[0].FailurePolicy = FailureRequired
	plan.Bindings[0].Provider.ExecutionClass = ExecutionDeclarative
	if _, err := NewPortPlan(plan); err == nil {
		t.Fatal("DECLARATIVE Channel provider was accepted")
	}
}
