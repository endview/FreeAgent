package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/channelservice"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestProductionActionChannelCompositionRunsTwoModelOneActionOneSend(
	t *testing.T,
) {
	const secret = "not-a-secret"
	var outboundCalls atomic.Int32
	outbound := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			outboundCalls.Add(1)
			if request.Method != http.MethodPost ||
				request.URL.Path != "/channel/outbound" ||
				request.Header.Get("Authorization") != "Bearer "+secret {
				t.Errorf(
					"unexpected outbound request %s %s headers=%v",
					request.Method,
					request.URL.Path,
					request.Header,
				)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read outbound request: %v", err)
				return
			}
			var delivery struct {
				AttemptID  string `json:"attempt_id"`
				EndpointID string `json:"endpoint_id"`
				Message    string `json:"message"`
			}
			if err := json.Unmarshal(body, &delivery); err != nil ||
				delivery.AttemptID == "" ||
				delivery.EndpointID != "endpoint-loopback" ||
				delivery.Message != exactadapter.TextStatsCompletedAssistantTextV1 {
				t.Errorf("invalid outbound delivery=%+v err=%v", delivery, err)
				return
			}
			wire, err := json.Marshal(map[string]any{
				"schema_version":        "loopback-channel-delivery-result/v1",
				"attempt_id":            delivery.AttemptID,
				"outcome":               "SUCCEEDED",
				"external_operation_id": "external-action-channel-1",
			})
			if err != nil {
				t.Errorf("marshal outbound response: %v", err)
				return
			}
			canonical, err := moduleapi.CanonicalJSON(wire)
			if err != nil {
				t.Errorf("canonical outbound response: %v", err)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(canonical)
		},
	))
	defer outbound.Close()

	fixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled:     true,
		seedPath:    actionExampleSeedPath(t),
		outboundURL: outbound.URL + "/channel/outbound",
	})
	if fixture.defaults.ProfileID != "action-chat" {
		t.Fatalf("Action Channel defaults=%+v", fixture.defaults)
	}
	ctx := context.Background()
	composition, err := openProductionChannelComposition(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		productionChannelEndpointInput{
			TenantID:       fixture.defaults.TenantID,
			WorkspaceID:    fixture.defaults.WorkspaceID,
			EndpointID:     "endpoint-loopback",
			SecretResolver: fixture.resolver,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	registered, err := composition.registry.IsRegistered(
		ctx,
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		t.Fatalf("Action registered before selected admission=%v, %v", registered, err)
	}

	input := channelservice.IngressInput{
		TenantID:    fixture.defaults.TenantID,
		WorkspaceID: fixture.defaults.WorkspaceID,
		Deadline:    time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
		Envelope: moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "endpoint-loopback",
			ProviderEventID: "provider-event-action-channel-1",
			ExternalUserID:  "external-user",
			Message:         "hello world\nsecond line",
			ReplyTarget: json.RawMessage(
				`{"account_id":"account-loopback","conversation_id":"conversation-loopback"}`,
			),
			CursorBefore: json.RawMessage(`{"offset":0}`),
			CursorAfter:  json.RawMessage(`{"offset":1}`),
		},
	}
	first, err := composition.channel.service.AdmitAndRun(ctx, input)
	if err != nil || !first.AdmissionCreated || first.Duplicate ||
		!first.LoopInvoked ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.Receipt.Disposition != currentstore.ChannelIngressAccepted {
		t.Fatalf("Action Channel admission=%+v, %v", first, err)
	}
	if outboundCalls.Load() != 1 {
		t.Fatalf("outbound calls=%d, want 1", outboundCalls.Load())
	}
	terminal, err := composition.store.GetTerminalRunResult(ctx, first.RunID)
	if err != nil || terminal.AttemptKind != corecontract.AttemptKindChannel ||
		terminal.ChannelState != currentstore.DispatchSucceeded ||
		terminal.Output.AssistantText != exactadapter.TextStatsCompletedAssistantTextV1 {
		t.Fatalf("Action Channel terminal=%+v, %v", terminal, err)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || !registered {
		t.Fatalf("selected Action was not loaded=%v, %v", registered, err)
	}
	assertActionChannelAttemptCounts(t, fixture.databasePath, first.RunID, 2, 1, 1)

	duplicateMaterializer := &rejectingActionMaterializer{}
	duplicateService, err := channelservice.NewActionCapableService(
		composition.store,
		composition.loop,
		duplicateMaterializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := duplicateService.AdmitAndRun(ctx, input)
	if err != nil || !duplicate.Duplicate || duplicate.AdmissionCreated ||
		duplicate.LoopInvoked || duplicate.RunID != first.RunID ||
		duplicate.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("Action Channel duplicate=%+v, %v", duplicate, err)
	}
	if outboundCalls.Load() != 1 {
		t.Fatalf("duplicate outbound calls=%d, want 1", outboundCalls.Load())
	}
	if duplicateMaterializer.calls.Load() != 0 {
		t.Fatalf(
			"terminal duplicate materializer calls=%d, want 0",
			duplicateMaterializer.calls.Load(),
		)
	}
	assertActionChannelAttemptCounts(t, fixture.databasePath, first.RunID, 2, 1, 1)
}

type rejectingActionMaterializer struct {
	calls atomic.Int32
}

func (materializer *rejectingActionMaterializer) Materialize(
	context.Context,
	actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	materializer.calls.Add(1)
	return nil, errors.New("terminal duplicate must reuse frozen Actions")
}

func assertActionChannelAttemptCounts(
	t *testing.T,
	databasePath string,
	runID string,
	wantModels int,
	wantActions int,
	wantChannels int,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var models, actions, channels int
	if err := database.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM dispatch_attempts
			 WHERE run_id=? AND dispatch_kind='ACTION'),
			(SELECT COUNT(*) FROM dispatch_attempts
			 WHERE run_id=? AND dispatch_kind='CHANNEL_SEND')
	`, runID, runID, runID).Scan(&models, &actions, &channels); err != nil {
		t.Fatal(err)
	}
	if models != wantModels || actions != wantActions || channels != wantChannels {
		t.Fatalf(
			"dispatch counts model/action/channel=%d/%d/%d, want %d/%d/%d",
			models,
			actions,
			channels,
			wantModels,
			wantActions,
			wantChannels,
		)
	}
}
