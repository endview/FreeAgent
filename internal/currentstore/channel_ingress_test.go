package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestChannelIngressAcceptedCommitsCursorAndRunAtomicallyAndDedupes(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seeded := mustSeedChannelCursor(t, fixture, bindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	input := channelAcceptedFixture(
		t,
		fixture,
		bindingDigest,
		seeded,
		"event-accepted",
		"run-channel",
	)

	created, err := fixture.store.CommitChannelIngressAndRunAdmission(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("CommitChannelIngressAndRunAdmission: %v", err)
	}
	if !created.Created || !created.Receipt.Created ||
		created.Receipt.CursorRevision != 1 ||
		created.Receipt.Disposition != ChannelIngressAccepted ||
		created.Receipt.RunID != "run-channel" ||
		created.Admission.RunID != "run-channel" ||
		!created.Admission.Created {
		t.Fatalf("created=%+v", created)
	}
	if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 2 {
		t.Fatalf("receipt count=%d want 2", got)
	}

	resolved, found, err := fixture.store.ResolveChannelIngress(
		context.Background(),
		fixture.intent.TenantID,
		"endpoint-default",
		input.Ingress.IngressKey,
		input.Ingress.Envelope.Digest,
	)
	if err != nil || !found || resolved.RunID != "run-channel" {
		t.Fatalf("ResolveChannelIngress found=%v receipt=%+v err=%v", found, resolved, err)
	}
	fixture.basis = advancePublishedBasisForTestV1(
		t, fixture.store, fixture.basis, fixture.controlCanonical, fixture.catalogCanonical,
	)
	retried, err := fixture.store.CommitChannelIngressAndRunAdmission(
		context.Background(),
		input,
	)
	if err != nil || retried.Created || retried.Receipt.Created ||
		retried.Admission.Created {
		t.Fatalf("duplicate=%+v err=%v", retried, err)
	}
	if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 2 {
		t.Fatalf("duplicate appended receipt: %d", got)
	}
}

func TestChannelIngressStaleCursorRollsBackWholeAdmission(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seeded := mustSeedChannelCursor(t, fixture, bindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	input := channelAcceptedFixture(
		t,
		fixture,
		bindingDigest,
		seeded,
		"event-stale",
		"run-stale-channel",
	)
	input.Ingress.ExpectedCursorRevision = 1
	beforeContents := countTableRows(t, fixture.store, "content_records")
	if _, err := fixture.store.CommitChannelIngressAndRunAdmission(
		context.Background(),
		input,
	); !errors.Is(err, ErrChannelIngressConflict) {
		t.Fatalf("stale cursor error=%v", err)
	}
	if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 1 {
		t.Fatalf("stale cursor receipts=%d want 1", got)
	}
	if got := countTableRows(t, fixture.store, "runs"); got != 0 {
		t.Fatalf("stale cursor runs=%d want 0", got)
	}
	if got := countTableRows(t, fixture.store, "content_records"); got != beforeContents {
		t.Fatalf("stale cursor leaked content: before=%d after=%d", beforeContents, got)
	}
}

func TestRejectedChannelIngressAdvancesOnceAndCannotBecomeAccepted(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seeded := mustSeedChannelCursor(t, fixture, bindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	accepted := channelAcceptedFixture(
		t,
		fixture,
		bindingDigest,
		seeded,
		"event-rejected",
		"run-rejected",
	)
	wire, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		accepted.Ingress.Envelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	wire.ExternalUserID = "unknown-user"
	_, rejectedCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(wire)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Ingress.Envelope = putativeChannelEnvelopeContent(t, rejectedCanonical)
	rejected, err := fixture.store.CommitRejectedChannelIngress(
		context.Background(),
		accepted.Ingress,
	)
	if err != nil {
		t.Fatalf("CommitRejectedChannelIngress: %v", err)
	}
	if !rejected.Created || rejected.Disposition != ChannelIngressRejected ||
		rejected.CursorRevision != 1 || rejected.RunID != "" ||
		rejected.PrincipalID != "" || rejected.ACLEpoch != 0 {
		t.Fatalf("rejected=%+v", rejected)
	}
	retry, err := fixture.store.CommitRejectedChannelIngress(
		context.Background(),
		accepted.Ingress,
	)
	if err != nil || retry.Created {
		t.Fatalf("rejected retry=%+v err=%v", retry, err)
	}
	if _, err := fixture.store.CommitChannelIngressAndRunAdmission(
		context.Background(),
		accepted,
	); !errors.Is(err, ErrChannelIngressConflict) {
		t.Fatalf("rejected-to-accepted error=%v", err)
	}
	if got := countTableRows(t, fixture.store, "runs"); got != 0 {
		t.Fatalf("rejected event created %d Runs", got)
	}
}

func TestRejectedChannelIngressCannotOverrideActiveIdentity(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	seeded := mustSeedChannelCursor(t, fixture, bindingDigest)
	publishChannelEndpointEnabled(t, fixture, true)
	input := channelAcceptedFixture(
		t,
		fixture,
		bindingDigest,
		seeded,
		"event-active-identity",
		"run-active-identity",
	)
	beforeContents := countTableRows(t, fixture.store, "content_records")
	if _, err := fixture.store.CommitRejectedChannelIngress(
		context.Background(),
		input.Ingress,
	); !errors.Is(err, ErrChannelIngressConflict) {
		t.Fatalf("active identity rejection error=%v", err)
	}
	if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 1 {
		t.Fatalf("active identity rejection appended receipt: %d", got)
	}
	if got := countTableRows(t, fixture.store, "content_records"); got != beforeContents {
		t.Fatalf("active identity rejection leaked content: before=%d after=%d", beforeContents, got)
	}
}

func TestChannelCursorSeedRequiresCurrentDisabledEndpointAndIsIdempotent(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	input := channelCursorSeedInput(t, fixture, bindingDigest)
	input.EndpointDisabled = false
	if _, err := fixture.store.SeedChannelCursor(
		context.Background(),
		input,
	); !errors.Is(err, ErrInvalidChannelIngress) {
		t.Fatalf("enabled assertion seed error=%v", err)
	}
	input.EndpointDisabled = true
	first, err := fixture.store.SeedChannelCursor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.store.SeedChannelCursor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.CursorAfterRef != second.CursorAfterRef {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func installDisabledChannelEndpointFixture(
	t *testing.T,
	fixture *admissionCommitFixture,
) string {
	t.Helper()
	_, configCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: "loopback-http/v1",
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := putChannelFixtureContent(t, fixture.store, ContentConfig, configCanonical)
	_, authorityCanonical, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            fixture.intent.TenantID,
			AllowedWorkspaceIDs: []string{fixture.intent.WorkspaceID},
			AllowedEndpointIDs:  []string{"endpoint-default"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := putChannelFixtureContent(
		t,
		fixture.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	port := moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	manifest := canonicalModuleManifest(
		t,
		"firstparty.loopback.channel",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{"provides": []any{map[string]any{
			"name": port.Name, "exact_version": port.ExactVersion,
		}}},
	)
	installation, err := fixture.store.InstallModule(
		context.Background(),
		installInput(t, "installation-channel", manifest, strings.Repeat("d", 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := fixture.store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-channel",
			TenantID:           fixture.intent.TenantID,
			InstanceID:         "instance-channel",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "loopback-http/v1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	bindingSpec := controlcontract.BindingSpec{
		Port:                port,
		InstanceID:          activation.InstanceID,
		ConfigRef:           config.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "control-channel-disabled"
	control.Revision++
	control.Digest = ""
	control.Workspaces[0].ChannelEndpoints = []controlcontract.ChannelEndpointDefinition{{
		SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
		EndpointID:      "endpoint-default",
		Channel:         "loopback-http",
		AccountID:       "account-default",
		ConversationID:  "conversation-default",
		TargetAgentID:   fixture.intent.AgentID,
		TargetProfileID: fixture.intent.ProfileID,
		CursorScopeKey:  "conversation-default",
		Enabled:         false,
		Binding:         bindingSpec,
	}}
	control.Workspaces[0].ChannelIdentities = []controlcontract.ChannelIdentityDefinition{{
		Channel:        "loopback-http",
		AccountID:      "account-default",
		ExternalUserID: "external-user",
		PrincipalID:    "channel-principal",
		ACLEpoch:       1,
		Active:         true,
	}}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-channel-disabled"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: moduleapi.ActivatedModuleRef{
			ModuleID:           installation.ModuleID,
			Version:            installation.ExactVersion,
			ArtifactDigest:     installation.ArtifactDigest,
			InstanceID:         activation.InstanceID,
			ExecutionClass:     activation.ExecutionClass,
			AdapterIdentity:    activation.AdapterIdentity,
			ActivationRevision: activation.ActivationRevision,
		},
		Provides: []moduleapi.PortRef{port},
	})
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publishChannelFixtureBasis(
		t,
		fixture,
		controlRef,
		controlCanonical,
		catalogRef,
		catalogCanonical,
	)
	resolved := moduleapi.PortBinding{
		Provider:            catalog.Entries[len(catalog.Entries)-1].Activation,
		ConfigRef:           config.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	digest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func publishChannelEndpointEnabled(
	t *testing.T,
	fixture *admissionCommitFixture,
	enabled bool,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.Revision++
	control.SnapshotID = fmt.Sprintf("control-channel-%d", control.Revision)
	control.Digest = ""
	control.Workspaces[0].ChannelEndpoints[0].Enabled = enabled
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Generation++
	catalog.GenerationID = fmt.Sprintf("catalog-channel-%d", catalog.Generation)
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publishChannelFixtureBasis(
		t,
		fixture,
		controlRef,
		controlCanonical,
		catalogRef,
		catalogCanonical,
	)
}

func publishChannelFixtureBasis(
	t *testing.T,
	fixture *admissionCommitFixture,
	controlRef controlcontract.ControlSnapshotRef,
	controlCanonical []byte,
	catalogRef controlcontract.CatalogGenerationRef,
	catalogCanonical []byte,
) {
	t.Helper()
	basis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
}

func mustSeedChannelCursor(
	t *testing.T,
	fixture *admissionCommitFixture,
	bindingDigest string,
) ChannelIngressReceipt {
	t.Helper()
	receipt, err := fixture.store.SeedChannelCursor(
		context.Background(),
		channelCursorSeedInput(t, fixture, bindingDigest),
	)
	if err != nil {
		t.Fatalf("SeedChannelCursor: %v", err)
	}
	return receipt
}

func channelCursorSeedInput(
	t *testing.T,
	fixture *admissionCommitFixture,
	bindingDigest string,
) ChannelCursorSeedInput {
	t.Helper()
	return ChannelCursorSeedInput{
		PublishedBasis:        fixture.basis,
		TenantID:              fixture.intent.TenantID,
		WorkspaceID:           fixture.intent.WorkspaceID,
		EndpointID:            "endpoint-default",
		CursorScopeKey:        "conversation-default",
		EndpointBindingDigest: bindingDigest,
		CursorAfter:           channelCursorContent(t, `{"offset":0}`),
		Reason:                "OPERATOR_SEED",
		EndpointDisabled:      true,
	}
}

func channelAcceptedFixture(
	t *testing.T,
	fixture *admissionCommitFixture,
	bindingDigest string,
	current ChannelIngressReceipt,
	providerEventID string,
	runID string,
) CommitChannelIngressAdmissionInput {
	t.Helper()
	before, err := fixture.store.GetContent(context.Background(), current.CursorAfterRef)
	if err != nil {
		t.Fatal(err)
	}
	after := channelCursorContent(t, `{"offset":1}`)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "endpoint-default",
			ProviderEventID: providerEventID,
			ExternalUserID:  "external-user",
			Message:         "hello",
			ReplyTarget:     json.RawMessage(`{"conversation":"conversation-default"}`),
			CursorBefore:    json.RawMessage(before.CanonicalBytes),
			CursorAfter:     json.RawMessage(after.CanonicalBytes),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := putativeChannelEnvelopeContent(t, envelopeCanonical)
	ingressKey, eventDigest, err := ComputeChannelIngressIdentity(
		fixture.intent.TenantID,
		"endpoint-default",
		providerEventID,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent := fixture.intent
	intent.AdmissionKey = channelAdmissionKey(ingressKey)
	intent.PrincipalID = "channel-principal"
	intent.ChannelEndpointID = "endpoint-default"
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	return CommitChannelIngressAdmissionInput{
		Ingress: ChannelIngressEventInput{
			PublishedBasis:         fixture.basis,
			TenantID:               intent.TenantID,
			WorkspaceID:            intent.WorkspaceID,
			EndpointID:             "endpoint-default",
			CursorScopeKey:         "conversation-default",
			ExpectedCursorRevision: current.CursorRevision,
			CursorBeforeRef:        current.CursorAfterRef,
			CursorAfter:            after,
			EndpointBindingDigest:  bindingDigest,
			IngressKey:             ingressKey,
			ProviderEventIDDigest:  eventDigest,
			Envelope:               envelope,
			Reason:                 "AUTHORIZED",
		},
		PrincipalID: "channel-principal",
		ACLEpoch:    1,
		Admission: fixture.compileInput(
			t,
			canonical,
			digest,
			runID,
		),
	}
}

func channelCursorContent(t *testing.T, cursor string) ContentInput {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON([]byte(cursor))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeContentDigest(
		ContentChannelCursor,
		channelCursorMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ContentInput{
		Digest: digest, Kind: ContentChannelCursor,
		MediaType: channelCursorMediaType, CanonicalBytes: canonical,
	}
}

func putativeChannelEnvelopeContent(t *testing.T, canonical []byte) ContentInput {
	t.Helper()
	digest, err := ComputeContentDigest(
		ContentChannelIngressEnvelope,
		channelEnvelopeMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ContentInput{
		Digest: digest, Kind: ContentChannelIngressEnvelope,
		MediaType: channelEnvelopeMediaType, CanonicalBytes: canonical,
	}
}

func putChannelFixtureContent(
	t *testing.T,
	store *Store,
	kind ContentKind,
	canonical []byte,
) ContentRecord {
	t.Helper()
	digest, err := ComputeContentDigest(kind, admissionJSONMediaType, canonical)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.PutContent(context.Background(), ContentInput{
		Digest: digest, Kind: kind, MediaType: admissionJSONMediaType,
		CanonicalBytes: canonical,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func countTableRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
