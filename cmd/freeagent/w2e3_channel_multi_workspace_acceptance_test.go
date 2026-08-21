package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelservice"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	w2e3SharedChannelInstanceV1 = "channel-shared-w2e3"
	w2e3ChannelTestTokenV1      = "not-a-secret"
)

type w2e3ChannelEndpointV1 struct {
	WorkspaceID    string
	EndpointID     string
	AccountID      string
	ConversationID string
	CursorScopeKey string
	ExternalUserID string
	InboundPath    string
	OutboundURL    string
}

// TestW2E3ChannelMultiWorkspaceIsolationBackupV1 is the concentrated W2-E3
// product acceptance. It starts from operator module-apply rather than a
// hand-built Runtime fixture and keeps the same Agent/Profile and activated
// Instance while varying only the Workspace-owned Endpoint binding.
func TestW2E3ChannelMultiWorkspaceIsolationBackupV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeMultiProfileForModuleApplyV1(
		t,
		root,
		databasePath,
		artifactRoot,
	)

	var healthyCalls atomic.Int32
	healthyServer := newW2E3ChannelDeliveryServerV1(
		t,
		"endpoint-w2e3-a",
		moduleapi.ChannelExecutionSucceeded,
		&healthyCalls,
	)
	defer healthyServer.Close()
	var unknownCalls atomic.Int32
	unknownServer := newW2E3ChannelDeliveryServerV1(
		t,
		"endpoint-w2e3-b",
		moduleapi.ChannelExecutionUnknown,
		&unknownCalls,
	)
	defer unknownServer.Close()

	endpointA := w2e3ChannelEndpointV1{
		WorkspaceID:    moduleApplyRoleWorkspace,
		EndpointID:     "endpoint-w2e3-a",
		AccountID:      "account-w2e3-a",
		ConversationID: "conversation-w2e3-a",
		CursorScopeKey: "cursor/w2e3-a",
		ExternalUserID: "external-user-w2e3-a",
		InboundPath:    "/channel/w2e3-a",
		OutboundURL:    healthyServer.URL + "/channel/outbound",
	}
	endpointB := w2e3ChannelEndpointV1{
		WorkspaceID:    moduleApplySkillWorkspace,
		EndpointID:     "endpoint-w2e3-b",
		AccountID:      "account-w2e3-b",
		ConversationID: "conversation-w2e3-b",
		CursorScopeKey: "cursor/w2e3-b",
		ExternalUserID: "external-user-w2e3-b",
		InboundPath:    "/channel/w2e3-b",
		OutboundURL:    unknownServer.URL + "/channel/outbound",
	}

	assertion, _ := productionLoopbackChannelArtifact(
		t,
		root,
		localLoopbackChannelModuleID,
	)
	identityBasis := publishW2E3ChannelIdentitiesV1(
		t,
		databasePath,
		[]w2e3ChannelEndpointV1{endpointA, endpointB},
	)
	appliedA := applyW2E3ChannelEndpointV1(
		t,
		root,
		databasePath,
		artifactRoot,
		assertion.ArtifactDirectory,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
		identityBasis.PointerRevision,
		endpointA,
	)
	appliedB := applyW2E3ChannelEndpointV1(
		t,
		root,
		databasePath,
		artifactRoot,
		assertion.ArtifactDirectory,
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
		appliedA.PointerRevision,
		endpointB,
	)
	if appliedA.PointerRevision != identityBasis.PointerRevision+1 ||
		appliedB.PointerRevision != appliedA.PointerRevision+1 {
		t.Fatalf(
			"Channel apply revisions identity=%d A=%d B=%d",
			identityBasis.PointerRevision,
			appliedA.PointerRevision,
			appliedB.PointerRevision,
		)
	}

	historyBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	historyA := readW2E3ChannelHistoryV1(
		t,
		databasePath,
		historyBasis,
		endpointA,
	)
	historyB := readW2E3ChannelHistoryV1(
		t,
		databasePath,
		historyBasis,
		endpointB,
	)

	compositionA := openW2E3ChannelCompositionV1(
		t,
		databasePath,
		artifactRoot,
		endpointA,
	)
	inputA1 := newW2E3ChannelIngressV1(
		t,
		endpointA,
		"provider-event-w2e3-a-1",
		"workspace A first message",
		0,
		1,
	)
	resultA1, err := compositionA.channel.service.AdmitAndRun(ctx, inputA1)
	if err != nil || !resultA1.AdmissionCreated || resultA1.Duplicate ||
		resultA1.LoopResult.Disposition != loopapi.DispositionTerminated ||
		healthyCalls.Load() != 1 {
		_ = compositionA.Close()
		t.Fatalf(
			"Workspace A first ingress=%+v sends=%d err=%v",
			resultA1,
			healthyCalls.Load(),
			err,
		)
	}
	duplicateA1, err := compositionA.channel.service.AdmitAndRun(ctx, inputA1)
	if err != nil || !duplicateA1.Duplicate || duplicateA1.AdmissionCreated ||
		duplicateA1.LoopInvoked || duplicateA1.RunID != resultA1.RunID ||
		duplicateA1.LoopResult.Disposition != loopapi.DispositionTerminated ||
		healthyCalls.Load() != 1 {
		_ = compositionA.Close()
		t.Fatalf(
			"Workspace A duplicate=%+v sends=%d err=%v",
			duplicateA1,
			healthyCalls.Load(),
			err,
		)
	}
	if err := compositionA.Close(); err != nil {
		t.Fatalf("close Workspace A composition: %v", err)
	}

	compositionB := openW2E3ChannelCompositionV1(
		t,
		databasePath,
		artifactRoot,
		endpointB,
	)
	inputB1 := newW2E3ChannelIngressV1(
		t,
		endpointB,
		"provider-event-w2e3-b-1",
		"workspace B ambiguous delivery",
		0,
		1,
	)
	resultB1, err := compositionB.channel.service.AdmitAndRun(ctx, inputB1)
	if err != nil || !resultB1.AdmissionCreated || resultB1.Duplicate ||
		resultB1.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation ||
		unknownCalls.Load() != 1 {
		_ = compositionB.Close()
		t.Fatalf(
			"Workspace B UNKNOWN ingress=%+v sends=%d err=%v",
			resultB1,
			unknownCalls.Load(),
			err,
		)
	}
	duplicateB1, err := compositionB.channel.service.AdmitAndRun(ctx, inputB1)
	if err != nil || !duplicateB1.Duplicate || duplicateB1.AdmissionCreated ||
		duplicateB1.LoopInvoked || duplicateB1.RunID != resultB1.RunID ||
		duplicateB1.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation ||
		unknownCalls.Load() != 1 {
		_ = compositionB.Close()
		t.Fatalf(
			"Workspace B UNKNOWN duplicate=%+v sends=%d err=%v",
			duplicateB1,
			unknownCalls.Load(),
			err,
		)
	}
	if err := compositionB.Close(); err != nil {
		t.Fatalf("close Workspace B composition: %v", err)
	}

	// B now owns an unreconciled Channel UNKNOWN. Opening A proves that the
	// reconciliation gate is Endpoint-scoped rather than a tenant-global stop.
	compositionA = openW2E3ChannelCompositionV1(
		t,
		databasePath,
		artifactRoot,
		endpointA,
	)
	inputA2 := newW2E3ChannelIngressV1(
		t,
		endpointA,
		"provider-event-w2e3-a-2",
		"workspace A remains healthy",
		1,
		2,
	)
	resultA2, err := compositionA.channel.service.AdmitAndRun(ctx, inputA2)
	if err != nil || !resultA2.AdmissionCreated || resultA2.Duplicate ||
		resultA2.LoopResult.Disposition != loopapi.DispositionTerminated ||
		healthyCalls.Load() != 2 || unknownCalls.Load() != 1 {
		_ = compositionA.Close()
		t.Fatalf(
			"Workspace A after B UNKNOWN=%+v healthy=%d unknown=%d err=%v",
			resultA2,
			healthyCalls.Load(),
			unknownCalls.Load(),
			err,
		)
	}
	if err := compositionA.Close(); err != nil {
		t.Fatalf("close Workspace A reopened composition: %v", err)
	}

	assertW2E3ChannelRuntimeIsolationV1(
		t,
		databasePath,
		endpointA,
		endpointB,
		[]channelservice.IngressResult{resultA1, resultA2},
		[]string{"workspace A first message", "workspace A remains healthy"},
		resultB1,
		2,
		1,
	)

	disabledB := disableW2E3ChannelEndpointV1(
		t,
		root,
		databasePath,
		artifactRoot,
		appliedB.PointerRevision,
		endpointB,
	)
	if disabledB.Status != moduleApplyStatusApplied ||
		disabledB.PointerRevision != appliedB.PointerRevision+1 {
		t.Fatalf("disable Workspace B Endpoint=%+v", disabledB)
	}
	assertW2E3SharedChannelInstanceV1(
		t,
		databasePath,
		endpointA,
		endpointB,
		assertion.ArtifactDigest,
		2,
		1,
	)
	if currentA := readW2E3ChannelHistoryV1(
		t,
		databasePath,
		historyBasis,
		endpointA,
	); !bytes.Equal(currentA, historyA) {
		t.Fatal("disabling B changed A's immutable module history")
	}
	if currentB := readW2E3ChannelHistoryV1(
		t,
		databasePath,
		historyBasis,
		endpointB,
	); !bytes.Equal(currentB, historyB) {
		t.Fatal("disabling B changed B's immutable module history")
	}

	bundlePath := filepath.Join(root, "w2e3-channel.bundle")
	manifest, err := currentbackup.CreateBundle(
		ctx,
		databasePath,
		artifactRoot,
		bundlePath,
		"freeagent-w2e3-channel-acceptance/v1",
	)
	if err != nil {
		t.Fatalf("create W2-E3 Channel bundle: %v", err)
	}
	if manifest.AttemptCounts.ChannelIngressReceipts != 5 ||
		manifest.AttemptCounts.ChannelCursorScopes != 2 ||
		manifest.AttemptCounts.ChannelSendPending != 0 ||
		manifest.AttemptCounts.ChannelSendUnknown != 1 {
		t.Fatalf("W2-E3 Channel bundle counts=%+v", manifest.AttemptCounts)
	}
	verified, err := currentbackup.VerifyBundle(ctx, bundlePath)
	if err != nil || verified.ManifestDigest != manifest.ManifestDigest ||
		verified.AttemptCounts != manifest.AttemptCounts {
		t.Fatalf("verify W2-E3 Channel bundle=%+v err=%v", verified, err)
	}
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "restored"),
	)

	if restoredA := readW2E3ChannelHistoryV1(
		t,
		restoredDatabase,
		historyBasis,
		endpointA,
	); !bytes.Equal(restoredA, historyA) {
		t.Fatal("Workspace A module history changed across backup/restore")
	}
	if restoredB := readW2E3ChannelHistoryV1(
		t,
		restoredDatabase,
		historyBasis,
		endpointB,
	); !bytes.Equal(restoredB, historyB) {
		t.Fatal("Workspace B module history changed across backup/restore")
	}
	assertW2E3SharedChannelInstanceV1(
		t,
		restoredDatabase,
		endpointA,
		endpointB,
		assertion.ArtifactDigest,
		2,
		1,
	)
	assertW2E3ChannelRuntimeIsolationV1(
		t,
		restoredDatabase,
		endpointA,
		endpointB,
		[]channelservice.IngressResult{resultA1, resultA2},
		[]string{"workspace A first message", "workspace A remains healthy"},
		resultB1,
		2,
		1,
	)

	restoredA := openW2E3ChannelCompositionV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		endpointA,
	)
	inputA3 := newW2E3ChannelIngressV1(
		t,
		endpointA,
		"provider-event-w2e3-a-3",
		"workspace A continues after restore",
		2,
		3,
	)
	resultA3, err := restoredA.channel.service.AdmitAndRun(ctx, inputA3)
	if err != nil || !resultA3.AdmissionCreated || resultA3.Duplicate ||
		resultA3.LoopResult.Disposition != loopapi.DispositionTerminated ||
		healthyCalls.Load() != 3 || unknownCalls.Load() != 1 {
		_ = restoredA.Close()
		t.Fatalf(
			"Workspace A post-restore ingress=%+v healthy=%d unknown=%d err=%v",
			resultA3,
			healthyCalls.Load(),
			unknownCalls.Load(),
			err,
		)
	}
	duplicateA3, err := restoredA.channel.service.AdmitAndRun(ctx, inputA3)
	if err != nil || !duplicateA3.Duplicate || duplicateA3.LoopInvoked ||
		duplicateA3.RunID != resultA3.RunID || healthyCalls.Load() != 3 {
		_ = restoredA.Close()
		t.Fatalf(
			"Workspace A post-restore duplicate=%+v sends=%d err=%v",
			duplicateA3,
			healthyCalls.Load(),
			err,
		)
	}
	if err := restoredA.Close(); err != nil {
		t.Fatalf("close restored Workspace A composition: %v", err)
	}
	assertW2E3ChannelRuntimeIsolationV1(
		t,
		restoredDatabase,
		endpointA,
		endpointB,
		[]channelservice.IngressResult{resultA1, resultA2, resultA3},
		[]string{
			"workspace A first message",
			"workspace A remains healthy",
			"workspace A continues after restore",
		},
		resultB1,
		3,
		1,
	)
}

func publishW2E3ChannelIdentitiesV1(
	t *testing.T,
	databasePath string,
	endpoints []w2e3ChannelEndpointV1,
) controlcontract.PublishedBasis {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		found := false
		for index := range control.Workspaces {
			workspace := &control.Workspaces[index]
			if workspace.Workspace.ID != endpoint.WorkspaceID {
				continue
			}
			found = true
			workspace.ChannelIdentities = append(
				workspace.ChannelIdentities,
				controlcontract.ChannelIdentityDefinition{
					Channel:        localLoopbackChannelName,
					AccountID:      endpoint.AccountID,
					ExternalUserID: endpoint.ExternalUserID,
					PrincipalID:    defaultPrincipalID,
					ACLEpoch:       1,
					Active:         true,
				},
			)
		}
		if !found {
			t.Fatalf("Workspace %q is absent", endpoint.WorkspaceID)
		}
	}
	control.SnapshotID = "control-w2e3-channel-identities"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-w2e3-channel-identities"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func applyW2E3ChannelEndpointV1(
	t *testing.T,
	root string,
	databasePath string,
	artifactRoot string,
	artifactDirectory string,
	artifactDigest string,
	artifactSize uint64,
	expectedPointer uint64,
	endpoint w2e3ChannelEndpointV1,
) moduleApplyResultV1 {
	t.Helper()
	plan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-"+endpoint.EndpointID+".json"),
		newW2E3ChannelApplyPlanV1(
			t,
			expectedPointer,
			endpoint,
			artifactDigest,
			artifactSize,
		),
	)
	result, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		context.Background(),
		runModuleApply,
		databasePath,
		artifactRoot,
		plan,
		artifactDirectory,
		artifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != expectedPointer+1 ||
		result.BindingTarget.Kind !=
			moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		result.BindingTarget.WorkspaceID != endpoint.WorkspaceID ||
		result.BindingTarget.EndpointID != endpoint.EndpointID ||
		result.InstanceID != w2e3SharedChannelInstanceV1 {
		t.Fatalf("apply Channel Endpoint %s=%+v err=%v", endpoint.EndpointID, result, err)
	}
	return result
}

func disableW2E3ChannelEndpointV1(
	t *testing.T,
	root string,
	databasePath string,
	artifactRoot string,
	expectedPointer uint64,
	endpoint w2e3ChannelEndpointV1,
) moduleApplyResultV1 {
	t.Helper()
	plan := canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target": map[string]any{
			"kind":         string(moduleApplyBindingTargetWorkspaceChannelEndpointV1),
			"workspace_id": endpoint.WorkspaceID,
			"endpoint_id":  endpoint.EndpointID,
		},
		"instance_id": w2e3SharedChannelInstanceV1,
		"port": map[string]any{
			"name":          productionChannelPort.Name,
			"exact_version": productionChannelPort.ExactVersion,
		},
	})
	path := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-"+endpoint.EndpointID+".json"),
		plan,
	)
	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		path,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("disable Channel Endpoint %s: %v", endpoint.EndpointID, err)
	}
	return result
}

func newW2E3ChannelApplyPlanV1(
	t *testing.T,
	expectedPointer uint64,
	endpoint w2e3ChannelEndpointV1,
	artifactDigest string,
	artifactSize uint64,
) []byte {
	t.Helper()
	parametersRaw, err := json.Marshal(map[string]any{
		"schema_version":     "loopback-http-parameters/v1",
		"inbound_path":       endpoint.InboundPath,
		"outbound_url":       endpoint.OutboundURL,
		"request_timeout_ms": 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := moduleapi.CanonicalJSON(parametersRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: loopbackchannel.AdapterProtocolV1,
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            defaultTenantID,
			AllowedWorkspaceIDs: []string{endpoint.WorkspaceID},
			AllowedEndpointIDs:  []string{endpoint.EndpointID},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target": map[string]any{
			"kind":         string(moduleApplyBindingTargetWorkspaceChannelEndpointV1),
			"workspace_id": endpoint.WorkspaceID,
			"endpoint_id":  endpoint.EndpointID,
		},
		"instance_id": w2e3SharedChannelInstanceV1,
		"port": map[string]any{
			"name":          productionChannelPort.Name,
			"exact_version": productionChannelPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localLoopbackChannelModuleID,
			"exact_version":       localLoopbackChannelVersion,
			"artifact_digest":     artifactDigest,
			"artifact_size_bytes": artifactSize,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
		"channel_endpoint": map[string]any{
			"channel":           localLoopbackChannelName,
			"account_id":        endpoint.AccountID,
			"conversation_id":   endpoint.ConversationID,
			"target_agent_id":   defaultAgentID,
			"target_profile_id": defaultProfileID,
			"cursor_scope_key":  endpoint.CursorScopeKey,
		},
		"cursor_seed": map[string]any{"offset": 0},
	})
}

func openW2E3ChannelCompositionV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	endpoint w2e3ChannelEndpointV1,
) *productionComposition {
	t.Helper()
	composition, err := openProductionChannelComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		productionChannelEndpointInput{
			TenantID:       defaultTenantID,
			WorkspaceID:    endpoint.WorkspaceID,
			EndpointID:     endpoint.EndpointID,
			SecretResolver: &countingChannelSecretResolver{},
		},
	)
	if err != nil {
		t.Fatalf("open Channel Endpoint %s: %v", endpoint.EndpointID, err)
	}
	return composition
}

func newW2E3ChannelIngressV1(
	t *testing.T,
	endpoint w2e3ChannelEndpointV1,
	eventID string,
	message string,
	cursorBefore int,
	cursorAfter int,
) channelservice.IngressInput {
	t.Helper()
	replyRaw, err := json.Marshal(map[string]any{
		"account_id":      endpoint.AccountID,
		"conversation_id": endpoint.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := moduleapi.CanonicalJSON(replyRaw)
	if err != nil {
		t.Fatal(err)
	}
	return channelservice.IngressInput{
		TenantID:    defaultTenantID,
		WorkspaceID: endpoint.WorkspaceID,
		Deadline:    time.Now().UTC().Add(time.Minute),
		Envelope: moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      endpoint.EndpointID,
			ProviderEventID: eventID,
			ExternalUserID:  endpoint.ExternalUserID,
			Message:         message,
			ReplyTarget:     json.RawMessage(reply),
			CursorBefore: json.RawMessage(
				`{"offset":` + strconv.Itoa(cursorBefore) + `}`,
			),
			CursorAfter: json.RawMessage(
				`{"offset":` + strconv.Itoa(cursorAfter) + `}`,
			),
		},
	}
}

func newW2E3ChannelDeliveryServerV1(
	t *testing.T,
	expectedEndpoint string,
	outcome moduleapi.ChannelExecutionOutcomeV1,
	calls *atomic.Int32,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			call := calls.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read W2-E3 Channel delivery: %v", err)
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			var delivery struct {
				SchemaVersion string `json:"schema_version"`
				AttemptID     string `json:"attempt_id"`
				EndpointID    string `json:"endpoint_id"`
			}
			if request.Method != http.MethodPost ||
				request.URL.Path != "/channel/outbound" ||
				request.Header.Get("Authorization") != "Bearer "+w2e3ChannelTestTokenV1 ||
				json.Unmarshal(body, &delivery) != nil ||
				delivery.SchemaVersion != "loopback-channel-delivery/v1" ||
				delivery.AttemptID == "" || delivery.EndpointID != expectedEndpoint {
				t.Errorf(
					"invalid W2-E3 Channel delivery method=%s path=%s endpoint=%+v body=%s",
					request.Method,
					request.URL.Path,
					delivery,
					body,
				)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			wire := map[string]any{
				"schema_version": "loopback-channel-delivery-result/v1",
				"attempt_id":     delivery.AttemptID,
				"outcome":        string(outcome),
			}
			if outcome == moduleapi.ChannelExecutionSucceeded {
				wire["external_operation_id"] = fmt.Sprintf(
					"w2e3-delivery-%s-%d",
					expectedEndpoint,
					call,
				)
			}
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Errorf("marshal W2-E3 Channel response: %v", err)
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			canonical, err := moduleapi.CanonicalJSON(raw)
			if err != nil {
				t.Errorf("canonical W2-E3 Channel response: %v", err)
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(canonical)
		},
	))
}

func readW2E3ChannelHistoryV1(
	t *testing.T,
	databasePath string,
	basis controlcontract.PublishedBasis,
	endpoint w2e3ChannelEndpointV1,
) []byte {
	t.Helper()
	payload := runModuleOperatorCommandV1(t, []string{
		"module-history",
		"--db", databasePath,
		"--tenant", defaultTenantID,
		"--control-revision", strconv.FormatUint(basis.Control.Revision, 10),
		"--catalog-generation", strconv.FormatUint(basis.Catalog.Generation, 10),
		"--workspace", endpoint.WorkspaceID,
		"--endpoint", endpoint.EndpointID,
	})
	var history moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, payload, &history)
	if history.BindingTarget == nil ||
		history.BindingTarget.Kind !=
			moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		history.BindingTarget.WorkspaceID != endpoint.WorkspaceID ||
		history.BindingTarget.EndpointID != endpoint.EndpointID ||
		len(history.Bindings) != 1 ||
		history.Bindings[0].BindingTarget != *history.BindingTarget ||
		history.Bindings[0].Activation.InstanceID !=
			w2e3SharedChannelInstanceV1 ||
		history.Bindings[0].Port != productionChannelPort {
		t.Fatalf("Channel history %s=%+v", endpoint.EndpointID, history)
	}
	return payload
}

func assertW2E3SharedChannelInstanceV1(
	t *testing.T,
	databasePath string,
	enabled w2e3ChannelEndpointV1,
	disabled w2e3ChannelEndpointV1,
	artifactDigest string,
	wantEnabledCursor uint64,
	wantDisabledCursor uint64,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	references := 0
	foundEnabled := false
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.EndpointID == disabled.EndpointID {
				t.Fatal("disabled Channel Endpoint remains in current Control")
			}
			if endpoint.Binding.InstanceID == w2e3SharedChannelInstanceV1 {
				references++
			}
			if workspace.Workspace.ID == enabled.WorkspaceID &&
				endpoint.EndpointID == enabled.EndpointID {
				foundEnabled = endpoint.Enabled &&
					endpoint.TargetAgentID == defaultAgentID &&
					endpoint.TargetProfileID == defaultProfileID
			}
		}
	}
	entry, found := catalog.FindInstance(w2e3SharedChannelInstanceV1)
	if !foundEnabled || references != 1 || !found ||
		entry.Activation.ArtifactDigest != artifactDigest ||
		len(entry.Provides) != 1 || entry.Provides[0] != productionChannelPort {
		t.Fatalf(
			"shared Channel after disable enabled=%v refs=%d entry=%+v found=%v",
			foundEnabled,
			references,
			entry,
			found,
		)
	}
	assertW2E3CursorV1(t, store, enabled, wantEnabledCursor)
	assertW2E3CursorV1(t, store, disabled, wantDisabledCursor)
}

func assertW2E3ChannelRuntimeIsolationV1(
	t *testing.T,
	databasePath string,
	endpointA w2e3ChannelEndpointV1,
	endpointB w2e3ChannelEndpointV1,
	resultsA []channelservice.IngressResult,
	messagesA []string,
	resultB channelservice.IngressResult,
	wantCursorA uint64,
	wantCursorB uint64,
) {
	t.Helper()
	if len(resultsA) != len(messagesA) {
		t.Fatal("W2-E3 successful Run/message fixture differs")
	}
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertW2E3CursorV1(t, store, endpointA, wantCursorA)
	assertW2E3CursorV1(t, store, endpointB, wantCursorB)
	for _, endpoint := range []w2e3ChannelEndpointV1{endpointA, endpointB} {
		seed, err := store.GetChannelCursorSeed(
			ctx,
			defaultTenantID,
			endpoint.EndpointID,
			endpoint.CursorScopeKey,
		)
		if err != nil || seed.CursorRevision != 0 ||
			seed.WorkspaceID != endpoint.WorkspaceID ||
			seed.EndpointID != endpoint.EndpointID ||
			seed.CursorScopeKey != endpoint.CursorScopeKey {
			t.Fatalf("Channel Cursor seed %s=%+v err=%v", endpoint.EndpointID, seed, err)
		}
	}

	runIDs := make(map[string]struct{}, len(resultsA)+1)
	for index, result := range resultsA {
		if result.RunID == "" || result.Receipt.RunID != result.RunID ||
			result.Receipt.WorkspaceID != endpointA.WorkspaceID ||
			result.Receipt.EndpointID != endpointA.EndpointID ||
			result.Receipt.CursorScopeKey != endpointA.CursorScopeKey ||
			result.Receipt.CursorRevision != uint64(index+1) {
			t.Fatalf("Workspace A receipt %d=%+v", index, result)
		}
		if _, duplicate := runIDs[result.RunID]; duplicate {
			t.Fatalf("Workspace A reused Run %q across events", result.RunID)
		}
		runIDs[result.RunID] = struct{}{}
		terminal, err := store.GetTerminalRunResult(ctx, result.RunID)
		if err != nil || terminal.ChannelState != currentstore.DispatchSucceeded ||
			terminal.Output.AssistantText != messagesA[index] {
			t.Fatalf(
				"Workspace A terminal %d=%+v err=%v",
				index,
				terminal,
				err,
			)
		}
	}
	if resultB.RunID == "" || resultB.Receipt.RunID != resultB.RunID ||
		resultB.Receipt.WorkspaceID != endpointB.WorkspaceID ||
		resultB.Receipt.EndpointID != endpointB.EndpointID ||
		resultB.Receipt.CursorScopeKey != endpointB.CursorScopeKey ||
		resultB.Receipt.CursorRevision != 1 ||
		resultB.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("Workspace B receipt/Run=%+v", resultB)
	}
	if _, duplicate := runIDs[resultB.RunID]; duplicate {
		t.Fatalf("Workspace B Run %q crossed into Workspace A", resultB.RunID)
	}

	recovery, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unknownCount := 0
	for _, candidate := range recovery {
		if candidate.UnsettledChannelAttemptID == "" {
			continue
		}
		record, err := store.GetChannelDispatchRecord(
			ctx,
			candidate.UnsettledChannelAttemptID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if record.Attempt.State != currentstore.DispatchUnknown {
			continue
		}
		unknownCount++
		if record.Attempt.EndpointID != endpointB.EndpointID ||
			record.Attempt.RunID != resultB.RunID {
			t.Fatalf("UNKNOWN Channel Attempt crossed Endpoint scope: %+v", record)
		}
	}
	if unknownCount != 1 {
		t.Fatalf("UNKNOWN Channel Attempts=%d want 1", unknownCount)
	}
}

func assertW2E3CursorV1(
	t *testing.T,
	store *currentstore.Store,
	endpoint w2e3ChannelEndpointV1,
	wantRevision uint64,
) {
	t.Helper()
	cursor, err := store.GetCurrentChannelCursor(
		context.Background(),
		defaultTenantID,
		endpoint.EndpointID,
		endpoint.CursorScopeKey,
	)
	if err != nil || cursor.CursorRevision != wantRevision ||
		cursor.WorkspaceID != endpoint.WorkspaceID ||
		cursor.EndpointID != endpoint.EndpointID ||
		cursor.CursorScopeKey != endpoint.CursorScopeKey {
		t.Fatalf(
			"Channel Cursor %s=%+v want revision=%d err=%v",
			endpoint.EndpointID,
			cursor,
			wantRevision,
			err,
		)
	}
}
