package channelservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestActionCapableServiceKeepsPureProfileMaterializerCold(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	materializer := &countingActionMaterializer{}
	service, err := NewActionCapableService(
		fixture.store,
		fixture.loop,
		materializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = fixture.service.now

	result, err := service.AdmitAndRun(
		context.Background(),
		fixture.input(
			"event-action-capable-pure",
			"external-user",
			`{"offset":0}`,
			`{"offset":1}`,
			time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		),
	)
	if err != nil || !result.AdmissionCreated || !result.LoopInvoked {
		t.Fatalf("Pure Profile admission = %+v, %v", result, err)
	}
	if materializer.calls != 0 {
		t.Fatalf("Pure Profile materializer calls = %d, want 0", materializer.calls)
	}
	if call := fixture.loop.onlyCall(t); call.MaxSteps != channelActionMaxSteps {
		t.Fatalf("Action-capable Loop input = %+v", call)
	}
}

type countingActionMaterializer struct {
	calls int
}

func (materializer *countingActionMaterializer) Materialize(
	context.Context,
	actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	materializer.calls++
	return nil, errors.New("Pure Profile must not materialize Actions")
}

func TestServiceAdmitsAndResumesOnlyTheOriginalRun(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	deadline := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	input := fixture.input(
		"event-1",
		"external-user",
		`{"offset":0}`,
		`{"offset":1}`,
		deadline,
	)

	first, err := fixture.service.AdmitAndRun(context.Background(), input)
	if err != nil {
		t.Fatalf("first AdmitAndRun: %v", err)
	}
	if first.RunID == "" || !first.AdmissionCreated || first.Duplicate ||
		!first.LoopInvoked || first.Deadline != deadline ||
		first.Receipt.Disposition != currentstore.ChannelIngressAccepted ||
		first.Receipt.RunID != first.RunID ||
		first.IngressKey != first.Receipt.IngressKey {
		t.Fatalf("first result = %+v", first)
	}
	if first.LoopResult.RunID != first.RunID ||
		first.LoopResult.Disposition != loopapi.DispositionYielded {
		t.Fatalf("first Loop result = %+v", first.LoopResult)
	}
	if got := fixture.loop.callCount(); got != 1 {
		t.Fatalf("Loop calls after first ingress = %d, want 1", got)
	}
	call := fixture.loop.onlyCall(t)
	if call.RunID != first.RunID || call.MaxSteps != channelPureLoopMaxSteps ||
		call.MaxDuration != channelLoopMaxDuration {
		t.Fatalf("Loop input = %+v", call)
	}

	second, err := fixture.service.AdmitAndRun(context.Background(), input)
	if err != nil {
		t.Fatalf("duplicate AdmitAndRun: %v", err)
	}
	if !second.Duplicate || second.AdmissionCreated || !second.LoopInvoked ||
		second.RunID != first.RunID ||
		second.Receipt.CursorRevision != first.Receipt.CursorRevision ||
		second.LoopResult.RunID != first.RunID {
		t.Fatalf("duplicate result = %+v", second)
	}
	if got := fixture.loop.callCount(); got != 2 {
		t.Fatalf("duplicate resume Loop calls = %d, want 2", got)
	}
	calls := fixture.loop.allCalls()
	if calls[0].RunID != calls[1].RunID {
		t.Fatalf("duplicate resumed another Run: %+v", calls)
	}
	current, err := fixture.store.GetCurrentChannelCursor(
		context.Background(),
		fixture.tenantID,
		fixture.endpointID,
		fixture.cursorScope,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.CursorRevision != 1 {
		t.Fatalf("duplicate advanced Cursor to revision %d", current.CursorRevision)
	}
}

func TestServiceDuplicateClosesPostCommitPreLoopCrashWindow(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	crashLoop := &failFirstLoop{}
	service, err := New(fixture.store, crashLoop)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.input(
		"event-post-commit-crash",
		"external-user",
		`{"offset":0}`,
		`{"offset":1}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	first, err := service.AdmitAndRun(context.Background(), input)
	if !errors.Is(err, errSimulatedPreLoopCrash) ||
		!first.AdmissionCreated || first.RunID == "" || !first.LoopInvoked {
		t.Fatalf("first result = %+v, error = %v", first, err)
	}
	second, err := service.AdmitAndRun(context.Background(), input)
	if err != nil {
		t.Fatalf("duplicate resume: %v", err)
	}
	if !second.Duplicate || second.AdmissionCreated || !second.LoopInvoked ||
		second.RunID != first.RunID ||
		second.LoopResult.Disposition != loopapi.DispositionYielded {
		t.Fatalf("duplicate resume result = %+v", second)
	}
	calls := crashLoop.allCalls()
	if len(calls) != 2 || calls[0].RunID != calls[1].RunID {
		t.Fatalf("crash-window Loop calls = %+v", calls)
	}
	current, err := fixture.store.GetCurrentChannelCursor(
		context.Background(),
		fixture.tenantID,
		fixture.endpointID,
		fixture.cursorScope,
	)
	if err != nil || current.CursorRevision != 1 {
		t.Fatalf("Cursor after resume = %+v, %v", current, err)
	}
}

func TestServiceAcceptedDuplicateDoesNotResumeAfterCurrentRevocation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*controlcontract.ControlSnapshot)
	}{
		{
			name: "endpoint disabled",
			mutate: func(control *controlcontract.ControlSnapshot) {
				control.Workspaces[0].ChannelEndpoints[0].Enabled = false
			},
		},
		{
			name: "identity authority changed",
			mutate: func(control *controlcontract.ControlSnapshot) {
				control.Workspaces[0].ChannelIdentities[0].PrincipalID =
					"principal-channel-reauthorized"
				control.Workspaces[0].ChannelIdentities[0].ACLEpoch++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChannelServiceFixture(
				t,
				fixtureOptions{enableEndpoint: true},
			)
			input := fixture.input(
				"event-revoked-duplicate",
				"external-user",
				`{"offset":0}`,
				`{"offset":1}`,
				time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			)
			first, err := fixture.service.AdmitAndRun(context.Background(), input)
			if err != nil || !first.AdmissionCreated || !first.LoopInvoked {
				t.Fatalf("first AdmitAndRun = %+v, %v", first, err)
			}
			publishChannelControlMutation(t, fixture.store, test.mutate)

			second, err := fixture.service.AdmitAndRun(context.Background(), input)
			if !errors.Is(err, ErrIngressDenied) {
				t.Fatalf("revoked duplicate = %+v, %v; want ErrIngressDenied", second, err)
			}
			if !second.Duplicate || second.RunID != first.RunID ||
				second.LoopInvoked {
				t.Fatalf("revoked duplicate result = %+v", second)
			}
			if got := fixture.loop.callCount(); got != 1 {
				t.Fatalf("revoked duplicate invoked Loop %d times, want 1", got)
			}
		})
	}
}

func TestServiceRunsRealUniversalLoopModelToChannelTerminal(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	model, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &modelChannelAdapter{model: model}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  fixture.provider.ArtifactDigest,
		AdapterIdentity: fixture.provider.AdapterIdentity,
		Invoker:         adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := coreloop.NewUniversalLoop(fixture.store, registry)
	if err != nil {
		t.Fatal(err)
	}
	countedLoop := &countingDelegatingLoop{next: loop}
	service, err := New(fixture.store, countedLoop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.AdmitAndRun(
		context.Background(),
		fixture.input(
			"event-real-loop",
			"external-user",
			`{"offset":0}`,
			`{"offset":1}`,
			time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		),
	)
	if err != nil {
		t.Fatalf("AdmitAndRun(real UniversalLoop): %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		!result.AdmissionCreated || result.Duplicate {
		t.Fatalf("real UniversalLoop result = %+v", result)
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		result.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.ChannelState != currentstore.DispatchSucceeded ||
		terminal.Output.AssistantText == "" {
		t.Fatalf("terminal result = %+v", terminal)
	}
	if adapter.executeCount() != 1 {
		t.Fatalf("Channel executions = %d, want 1", adapter.executeCount())
	}
	if countedLoop.callCount() != 1 {
		t.Fatalf("Universal Loop calls = %d, want 1", countedLoop.callCount())
	}
	heldLease := holdCurrentRunLease(
		t,
		fixture.store,
		result.RunID,
		"terminal-duplicate-held-lease",
	)
	if heldLease.FrameRevision <= terminal.FrameRevision {
		t.Fatalf(
			"held lease Frame revision = %d, want later than terminal event revision %d",
			heldLease.FrameRevision,
			terminal.FrameRevision,
		)
	}
	publishChannelControlMutation(t, fixture.store, func(control *controlcontract.ControlSnapshot) {
		control.Workspaces[0].ChannelEndpoints[0].Enabled = false
	})

	duplicate, err := service.AdmitAndRun(
		context.Background(),
		fixture.input(
			"event-real-loop",
			"external-user",
			`{"offset":0}`,
			`{"offset":1}`,
			time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC),
		),
	)
	if err != nil {
		t.Fatalf("terminal duplicate resume: %v", err)
	}
	if !duplicate.Duplicate ||
		duplicate.RunID != result.RunID ||
		duplicate.LoopInvoked ||
		duplicate.LoopResult.Disposition != loopapi.DispositionTerminated ||
		duplicate.LoopResult.FrameRevision != terminal.FrameRevision ||
		duplicate.LoopResult.ReasonCode != result.LoopResult.ReasonCode ||
		countedLoop.callCount() != 1 ||
		adapter.executeCount() != 1 {
		t.Fatalf(
			"terminal duplicate = %+v, loop calls=%d, sends=%d",
			duplicate,
			countedLoop.callCount(),
			adapter.executeCount(),
		)
	}
}

func TestServiceUnknownDuplicateReturnsOriginalAfterRouteRevocation(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	model, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &modelChannelAdapter{
		model:   model,
		outcome: moduleapi.ChannelExecutionUnknown,
	}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest: fixture.provider.ArtifactDigest, AdapterIdentity: fixture.provider.AdapterIdentity,
		Invoker: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := coreloop.NewUniversalLoop(fixture.store, registry)
	if err != nil {
		t.Fatal(err)
	}
	countedLoop := &countingDelegatingLoop{next: loop}
	service, err := New(fixture.store, countedLoop)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.input(
		"event-unknown-route-revoked",
		"external-user",
		`{"offset":0}`,
		`{"offset":1}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	first, err := service.AdmitAndRun(context.Background(), input)
	if err != nil || first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		adapter.executeCount() != 1 {
		t.Fatalf("first UNKNOWN result=%+v sends=%d err=%v", first, adapter.executeCount(), err)
	}
	if countedLoop.callCount() != 1 {
		t.Fatalf("Universal Loop calls = %d, want 1", countedLoop.callCount())
	}
	heldLease := holdCurrentRunLease(
		t,
		fixture.store,
		first.RunID,
		"waiting-duplicate-held-lease",
	)
	publishChannelControlMutation(t, fixture.store, func(control *controlcontract.ControlSnapshot) {
		control.Workspaces[0].ChannelEndpoints[0].Enabled = false
	})
	duplicate, err := service.AdmitAndRun(context.Background(), input)
	if err != nil || !duplicate.Duplicate || duplicate.RunID != first.RunID ||
		duplicate.LoopInvoked ||
		duplicate.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		duplicate.LoopResult.FrameRevision != heldLease.FrameRevision ||
		duplicate.LoopResult.ReasonCode != first.LoopResult.ReasonCode ||
		countedLoop.callCount() != 1 ||
		adapter.executeCount() != 1 {
		t.Fatalf(
			"revoked UNKNOWN duplicate=%+v loop calls=%d sends=%d err=%v",
			duplicate,
			countedLoop.callCount(),
			adapter.executeCount(),
			err,
		)
	}
}

func TestServiceMissingOrDisabledRouteDoesNotAdvanceCursor(t *testing.T) {
	tests := []struct {
		name     string
		options  fixtureOptions
		endpoint string
		user     string
	}{
		{
			name:     "disabled endpoint",
			options:  fixtureOptions{enableEndpoint: false},
			endpoint: "endpoint-default",
			user:     "external-user",
		},
		{
			name:     "missing endpoint",
			options:  fixtureOptions{enableEndpoint: true},
			endpoint: "endpoint-missing",
			user:     "external-user",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChannelServiceFixture(t, test.options)
			input := fixture.input(
				"event-denied",
				test.user,
				`{"offset":0}`,
				`{"offset":1}`,
				time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			)
			input.Envelope.EndpointID = test.endpoint
			_, err := fixture.service.AdmitAndRun(context.Background(), input)
			if !errors.Is(err, ErrIngressDenied) {
				t.Fatalf("AdmitAndRun error = %v, want ErrIngressDenied", err)
			}
			if got := fixture.loop.callCount(); got != 0 {
				t.Fatalf("denied ingress invoked Loop %d times", got)
			}
			assertNoIngressReceipt(t, fixture, input.Envelope)
		})
	}
}

func TestServiceAuthenticatedPoisonIdentityAdvancesOnceThenAllowsValidEvent(
	t *testing.T,
) {
	tests := []struct {
		name       string
		options    fixtureOptions
		poisonUser string
		validUser  string
	}{
		{
			name:       "unknown identity",
			options:    fixtureOptions{enableEndpoint: true},
			poisonUser: "unknown-user",
			validUser:  "external-user",
		},
		{
			name: "inactive exact identity",
			options: fixtureOptions{
				enableEndpoint: true,
				inactiveUser:   true,
			},
			poisonUser: "external-user",
			validUser:  "fallback-user",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChannelServiceFixture(t, test.options)
			poison := fixture.input(
				"event-poison",
				test.poisonUser,
				`{"offset":0}`,
				`{"offset":1}`,
				time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			)
			first, err := fixture.service.AdmitAndRun(
				context.Background(),
				poison,
			)
			if err != nil || first.Duplicate || first.LoopInvoked ||
				first.RunID != "" || first.AdmissionCreated ||
				!first.Receipt.Created ||
				first.Receipt.Disposition != currentstore.ChannelIngressRejected ||
				first.Receipt.CursorRevision != 1 {
				t.Fatalf("first poison result=%+v err=%v", first, err)
			}
			if got := fixture.loop.callCount(); got != 0 {
				t.Fatalf("poison event invoked Loop %d times", got)
			}

			duplicate, err := fixture.service.AdmitAndRun(
				context.Background(),
				poison,
			)
			if err != nil || !duplicate.Duplicate || duplicate.LoopInvoked ||
				duplicate.RunID != "" || duplicate.Receipt.Created ||
				duplicate.Receipt.Disposition != currentstore.ChannelIngressRejected ||
				duplicate.Receipt.CursorRevision != 1 {
				t.Fatalf("duplicate poison result=%+v err=%v", duplicate, err)
			}

			valid := fixture.input(
				"event-after-poison",
				test.validUser,
				`{"offset":1}`,
				`{"offset":2}`,
				time.Date(2030, 1, 1, 0, 1, 0, 0, time.UTC),
			)
			accepted, err := fixture.service.AdmitAndRun(
				context.Background(),
				valid,
			)
			if err != nil || !accepted.AdmissionCreated ||
				!accepted.LoopInvoked || accepted.RunID == "" ||
				accepted.Receipt.Disposition != currentstore.ChannelIngressAccepted ||
				accepted.Receipt.CursorRevision != 2 {
				t.Fatalf("valid event after poison=%+v err=%v", accepted, err)
			}
			if got := fixture.loop.callCount(); got != 1 {
				t.Fatalf("valid event after poison Loop calls=%d, want 1", got)
			}
			current, err := fixture.store.GetCurrentChannelCursor(
				context.Background(),
				fixture.tenantID,
				fixture.endpointID,
				fixture.cursorScope,
			)
			if err != nil || current.CursorRevision != 2 {
				t.Fatalf("Cursor after poison and valid event=%+v err=%v", current, err)
			}
		})
	}
}

func TestServicePoisonIdentityWithStaleCursorIsNotPersisted(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	input := fixture.input(
		"event-stale-poison",
		"unknown-user",
		`{"offset":99}`,
		`{"offset":100}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	_, err := fixture.service.AdmitAndRun(context.Background(), input)
	if !errors.Is(err, currentstore.ErrChannelIngressConflict) {
		t.Fatalf("stale poison error=%v, want cursor conflict", err)
	}
	if got := fixture.loop.callCount(); got != 0 {
		t.Fatalf("stale poison invoked Loop %d times", got)
	}
	assertNoIngressReceipt(t, fixture, input.Envelope)
	current, err := fixture.store.GetCurrentChannelCursor(
		context.Background(),
		fixture.tenantID,
		fixture.endpointID,
		fixture.cursorScope,
	)
	if err != nil || current.CursorRevision != 0 {
		t.Fatalf("stale poison changed Cursor=%+v err=%v", current, err)
	}
}

func TestServiceRejectsStaleCursorWithoutAdmissionOrLoop(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	input := fixture.input(
		"event-stale",
		"external-user",
		`{"offset":99}`,
		`{"offset":100}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	_, err := fixture.service.AdmitAndRun(context.Background(), input)
	if !errors.Is(err, currentstore.ErrChannelIngressConflict) {
		t.Fatalf("AdmitAndRun error = %v, want cursor conflict", err)
	}
	if got := fixture.loop.callCount(); got != 0 {
		t.Fatalf("stale Cursor invoked Loop %d times", got)
	}
	assertNoIngressReceipt(t, fixture, input.Envelope)
	current, err := fixture.store.GetCurrentChannelCursor(
		context.Background(),
		fixture.tenantID,
		fixture.endpointID,
		fixture.cursorScope,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.CursorRevision != 0 {
		t.Fatalf("stale event advanced Cursor to revision %d", current.CursorRevision)
	}
}

func TestServiceRejectsDuplicateFromAnotherWorkspaceScope(t *testing.T) {
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	input := fixture.input(
		"event-scope",
		"external-user",
		`{"offset":0}`,
		`{"offset":1}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	first, err := fixture.service.AdmitAndRun(context.Background(), input)
	if err != nil || !first.AdmissionCreated {
		t.Fatalf("first AdmitAndRun = %+v, %v", first, err)
	}
	input.WorkspaceID = "workspace-other"
	_, err = fixture.service.AdmitAndRun(context.Background(), input)
	if !errors.Is(err, ErrIngressIntegrity) {
		t.Fatalf("wrong-scope duplicate error = %v", err)
	}
	if got := fixture.loop.callCount(); got != 1 {
		t.Fatalf("wrong-scope duplicate changed Loop calls to %d", got)
	}
}

type fixtureOptions struct {
	enableEndpoint bool
	inactiveUser   bool
}

type channelServiceFixture struct {
	store       *currentstore.Store
	service     *Service
	loop        *recordingLoop
	tenantID    string
	workspaceID string
	endpointID  string
	cursorScope string
	provider    moduleapi.ActivatedModuleRef
}

func (fixture *channelServiceFixture) input(
	eventID string,
	externalUser string,
	cursorBefore string,
	cursorAfter string,
	deadline time.Time,
) IngressInput {
	return IngressInput{
		TenantID:    fixture.tenantID,
		WorkspaceID: fixture.workspaceID,
		Deadline:    deadline,
		Envelope: moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      fixture.endpointID,
			ProviderEventID: eventID,
			ExternalUserID:  externalUser,
			Message:         "hello from channel",
			ReplyTarget:     json.RawMessage(`{"conversation":"conversation-default"}`),
			CursorBefore:    json.RawMessage(cursorBefore),
			CursorAfter:     json.RawMessage(cursorAfter),
		},
	}
}

func newChannelServiceFixture(
	t *testing.T,
	options fixtureOptions,
) *channelServiceFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close: %v", err)
		}
	})

	const (
		tenantID    = "tenant-channel"
		workspaceID = "workspace-channel"
		agentID     = "agent-channel"
		profileID   = "profile-channel"
		endpointID  = "endpoint-default"
		cursorScope = "conversation-default"
	)
	modelPort := modelGeneratePortV1
	channelPort := channelTransportPortV1
	manifest := testModuleManifest(t, modelPort, channelPort)
	parsed, _, err := moduleapi.ParseModuleManifestV1(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		channelJSONMediaType,
		manifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      "installation-channel-test",
		ModuleID:            parsed.ID,
		ExactVersion:        parsed.Version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       manifest,
		ArtifactDigest:      strings.Repeat("a", moduleapi.SHA256HexLength),
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       "activation-channel-test",
		TenantID:           tenantID,
		InstanceID:         "instance-channel-test",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "channelservice.test.adapter/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID: installation.ModuleID, Version: installation.ExactVersion,
		ArtifactDigest: installation.ArtifactDigest,
		InstanceID:     activation.InstanceID, ExecutionClass: activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	_, modelConfigCanonical, err := moduleapi.NewModelBindingConfigV2(
		moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      "test-provider", Model: "test-model",
			ModelBuildID: "build-v1",
			Parameters:   json.RawMessage(`{"max_tokens":10}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig := putContent(
		t,
		store,
		currentstore.ContentConfig,
		modelConfigCanonical,
	)
	modelAuthority := putContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		[]byte(`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`),
	)
	_, channelConfigCanonical, err := moduleapi.NewChannelBindingConfigV1(
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
	channelConfig := putContent(
		t,
		store,
		currentstore.ContentConfig,
		channelConfigCanonical,
	)
	_, channelAuthorityCanonical, err :=
		moduleapi.NewChannelAuthorityCeilingV1(
			moduleapi.ChannelAuthorityCeilingV1{
				SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
				TenantID:            tenantID,
				AllowedWorkspaceIDs: []string{workspaceID},
				AllowedEndpointIDs:  []string{endpointID},
				AllowReceive:        true, AllowSend: true,
				MaxMessageBytes: moduleapi.MaxChannelMessageBytesV1,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	channelAuthority := putContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		channelAuthorityCanonical,
	)
	contextPolicy := putPolicy(t, store, "context-policy", corecontract.PolicyContext)
	schedulingPolicy := putPolicy(t, store, "scheduling-policy", corecontract.PolicyScheduling)

	channelBinding := controlcontract.BindingSpec{
		Port: channelPort, InstanceID: provider.InstanceID,
		ConfigRef: channelConfig, AuthorityCeilingRef: channelAuthority,
		StaticContextRefs: []string{}, FailurePolicy: moduleapi.FailureRequired,
	}
	identities := []controlcontract.ChannelIdentityDefinition{{
		Channel: "loopback-http", AccountID: "account-default",
		ExternalUserID: "external-user", PrincipalID: "principal-channel",
		ACLEpoch: 7, Active: !options.inactiveUser,
	}}
	if options.inactiveUser {
		identities = append(identities, controlcontract.ChannelIdentityDefinition{
			Channel: "loopback-http", AccountID: "account-default",
			ExternalUserID: "fallback-user", PrincipalID: "principal-fallback",
			ACLEpoch: 8, Active: true,
		})
	}
	agent := corecontract.AgentRef{ID: agentID, Version: "v1", Digest: strings.Repeat("2", 64)}
	workspace := corecontract.WorkspaceRef{ID: workspaceID, Version: "v1", Digest: strings.Repeat("3", 64)}
	profile := corecontract.ProfileRef{ID: profileID, Version: "v1", Digest: strings.Repeat("4", 64)}
	control := controlcontract.ControlSnapshot{
		SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
		SnapshotID:    "control-channel-disabled", TenantID: tenantID, Revision: 1,
		Agents: []corecontract.AgentRef{agent},
		Workspaces: []controlcontract.WorkspaceDefinition{{
			Workspace: workspace,
			ChannelEndpoints: []controlcontract.ChannelEndpointDefinition{{
				SchemaVersion: controlcontract.ChannelEndpointSchemaVersionV1,
				EndpointID:    endpointID, Channel: "loopback-http",
				AccountID: "account-default", ConversationID: "conversation-default",
				TargetAgentID: agentID, TargetProfileID: profileID,
				CursorScopeKey: cursorScope, Enabled: false, Binding: channelBinding,
			}},
			ChannelIdentities: identities,
		}},
		Profiles: []controlcontract.ProfileDefinition{{
			Profile: profile, ContextPolicy: contextPolicy,
			SchedulingPolicy: schedulingPolicy,
			Bindings: []controlcontract.BindingSpec{{
				Port: modelPort, InstanceID: provider.InstanceID,
				ConfigRef: modelConfig, AuthorityCeilingRef: modelAuthority,
				StaticContextRefs: []string{}, FailurePolicy: moduleapi.FailureRequired,
			}},
		}},
	}
	catalog := controlcontract.CatalogGeneration{
		SchemaVersion: controlcontract.CatalogGenerationSchemaVersionV1,
		GenerationID:  "catalog-channel-disabled", Generation: 1,
		TenantID: tenantID,
		Entries: []controlcontract.CatalogEntry{{
			Activation: provider, Provides: []moduleapi.PortRef{modelPort, channelPort},
		}},
	}
	basis, controlCanonical, catalogCanonical := publishBasis(
		t,
		store,
		controlcontract.PublishedBasis{},
		control,
		catalog,
	)
	resolvedBinding := moduleapi.PortBinding{
		Provider: provider, ConfigRef: channelConfig,
		AuthorityCeilingRef: channelAuthority,
		StaticContextRefs:   []string{}, FailurePolicy: moduleapi.FailureRequired,
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(resolvedBinding)
	if err != nil {
		t.Fatal(err)
	}
	seedCursor := cursorContent(t, `{"offset":0}`)
	if _, err := store.SeedChannelCursor(ctx, currentstore.ChannelCursorSeedInput{
		PublishedBasis: basis, TenantID: tenantID, WorkspaceID: workspaceID,
		EndpointID: endpointID, CursorScopeKey: cursorScope,
		EndpointBindingDigest: bindingDigest, CursorAfter: seedCursor,
		Reason: "OPERATOR_SEED", EndpointDisabled: true,
	}); err != nil {
		t.Fatalf("SeedChannelCursor: %v", err)
	}
	_ = controlCanonical
	_ = catalogCanonical
	if options.enableEndpoint {
		control.SnapshotID = "control-channel-enabled"
		control.Revision = 2
		control.Digest = ""
		control.Workspaces[0].ChannelEndpoints[0].Enabled = true
		catalog.GenerationID = "catalog-channel-enabled"
		catalog.Generation = 2
		catalog.Digest = ""
		basis, _, _ = publishBasis(t, store, basis, control, catalog)
		_ = basis
	}

	loop := &recordingLoop{}
	service, err := New(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return &channelServiceFixture{
		store: store, service: service, loop: loop,
		tenantID: tenantID, workspaceID: workspaceID,
		endpointID: endpointID, cursorScope: cursorScope,
		provider: provider,
	}
}

func publishBasis(
	t *testing.T,
	store *currentstore.Store,
	previous controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (controlcontract.PublishedBasis, []byte, []byte) {
	t.Helper()
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: previous.PointerRevision,
			NewPointerRevision:      previous.PointerRevision + 1,
			ControlRef:              controlRef, ControlCanonical: controlCanonical,
			CatalogRef: catalogRef, CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return basis, controlCanonical, catalogCanonical
}

func publishChannelControlMutation(
	t *testing.T,
	store *currentstore.Store,
	mutate func(*controlcontract.ControlSnapshot),
) {
	t.Helper()
	basis, control, catalog, err := store.LoadPublishedBasis(context.Background(), "tenant-channel")
	if err != nil {
		t.Fatal(err)
	}
	mutate(&control)
	control.Revision++
	control.SnapshotID = fmt.Sprintf("control-channel-mutated-%d", control.Revision)
	control.Digest = ""
	catalog.Generation++
	catalog.GenerationID = fmt.Sprintf("catalog-channel-mutated-%d", catalog.Generation)
	catalog.Digest = ""
	publishBasis(t, store, basis, control, catalog)
}

type recordingLoop struct {
	mu    sync.Mutex
	calls []loopapi.RunInput
}

type countingDelegatingLoop struct {
	next  loopapi.Loop
	mu    sync.Mutex
	calls int
}

func (loop *countingDelegatingLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	loop.mu.Lock()
	loop.calls++
	loop.mu.Unlock()
	return loop.next.Run(ctx, input)
}

func (loop *countingDelegatingLoop) callCount() int {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.calls
}

func holdCurrentRunLease(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	ownerID string,
) currentstore.RunLease {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: ownerID,
			TTL:     time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("hold current Run lease: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ReleaseRunLease(context.Background(), lease)
	})
	return lease
}

type modelChannelAdapter struct {
	model   modulehost.ModuleInvoker
	outcome moduleapi.ChannelExecutionOutcomeV1
	mu      sync.Mutex
	sends   int
}

func (adapter *modelChannelAdapter) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return adapter.model.Invoke(ctx, prepared)
}

func (*modelChannelAdapter) PrepareSend(
	_ context.Context,
	request moduleapi.ChannelPrepareSendRequestV1,
) (json.RawMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return moduleapi.CanonicalizeChannelPreparedPayloadV1(
		json.RawMessage(`{"delivery":"original-reply-target"}`),
	)
}

func (adapter *modelChannelAdapter) ExecutePrepared(
	_ context.Context,
	request moduleapi.ChannelExecutionRequestV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	frozen, _, err := moduleapi.NewChannelExecutionRequestV1(request)
	if err != nil {
		return moduleapi.ChannelExecutionResultV1{}, err
	}
	adapter.mu.Lock()
	adapter.sends++
	adapter.mu.Unlock()
	if adapter.outcome == moduleapi.ChannelExecutionUnknown {
		result, _, err := moduleapi.NewChannelExecutionResultV1(
			moduleapi.ChannelExecutionResultV1{
				SchemaVersion: moduleapi.ChannelExecutionResultSchemaV1,
				AttemptID:     frozen.AttemptID,
				Outcome:       moduleapi.ChannelExecutionUnknown,
				UnknownReason: "TEST_AMBIGUOUS_DELIVERY",
			},
		)
		return result, err
	}
	result, _, err := moduleapi.NewChannelExecutionResultV1(
		moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           frozen.AttemptID,
			Outcome:             moduleapi.ChannelExecutionSucceeded,
			ProviderReceipt:     json.RawMessage(`{"delivered":true}`),
			ExternalOperationID: "delivery-1",
		},
	)
	return result, err
}

func (adapter *modelChannelAdapter) executeCount() int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.sends
}

var errSimulatedPreLoopCrash = errors.New("simulated pre-Loop process loss")

type failFirstLoop struct {
	mu    sync.Mutex
	calls []loopapi.RunInput
}

func (loop *failFirstLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	loop.mu.Lock()
	loop.calls = append(loop.calls, input)
	call := len(loop.calls)
	loop.mu.Unlock()
	if call == 1 {
		return loopapi.RunResult{}, errSimulatedPreLoopCrash
	}
	return loopapi.RunResult{
		RunID: input.RunID, Disposition: loopapi.DispositionYielded,
		ReasonCode: "TEST_RESUMED_ORIGINAL_RUN",
	}, nil
}

func (loop *failFirstLoop) allCalls() []loopapi.RunInput {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return append([]loopapi.RunInput(nil), loop.calls...)
}

func (loop *recordingLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	loop.mu.Lock()
	loop.calls = append(loop.calls, input)
	loop.mu.Unlock()
	return loopapi.RunResult{
		RunID: input.RunID, Disposition: loopapi.DispositionYielded,
		FrameRevision: 0, ReasonCode: "TEST_BOUNDED_YIELD",
	}, nil
}

func (loop *recordingLoop) callCount() int {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return len(loop.calls)
}

func (loop *recordingLoop) onlyCall(t *testing.T) loopapi.RunInput {
	t.Helper()
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.calls) != 1 {
		t.Fatalf("Loop calls = %d, want 1", len(loop.calls))
	}
	return loop.calls[0]
}

func (loop *recordingLoop) allCalls() []loopapi.RunInput {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return append([]loopapi.RunInput(nil), loop.calls...)
}

func assertNoIngressReceipt(
	t *testing.T,
	fixture *channelServiceFixture,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) {
	t.Helper()
	_, canonical, err := moduleapi.NewChannelInboundEnvelopeV1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	content, err := contentInput(currentstore.ContentChannelIngressEnvelope, canonical)
	if err != nil {
		t.Fatal(err)
	}
	ingressKey, _, err := currentstore.ComputeChannelIngressIdentity(
		fixture.tenantID,
		envelope.EndpointID,
		envelope.ProviderEventID,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := fixture.store.ResolveChannelIngress(
		context.Background(),
		fixture.tenantID,
		envelope.EndpointID,
		ingressKey,
		content.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("denied event created an ingress receipt")
	}
}

func cursorContent(t *testing.T, raw string) currentstore.ContentInput {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	input, err := contentInput(currentstore.ContentChannelCursor, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func putContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	input, err := contentInput(kind, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutContent(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return input.Digest
}

func putPolicy(
	t *testing.T,
	store *currentstore.Store,
	id string,
	policyType corecontract.PolicyType,
) corecontract.PolicyRef {
	t.Helper()
	body := json.RawMessage(`{"enabled":true}`)
	if policyType == corecontract.PolicyContext {
		body = json.RawMessage(
			`{"context_window_tokens":32768,` +
				`"estimator_version":"canonical-json-utf8-byte-upper-bound/v1",` +
				`"recent_history_turns":8,"reserved_output_tokens":4096,` +
				`"schema_version":"context-policy/v1"}`,
		)
	}
	_, ref, canonical, err := corecontract.NewPolicyDocument(
		id,
		"v1",
		policyType,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest: ref.Digest, Kind: currentstore.ContentPolicy,
		MediaType: channelJSONMediaType, CanonicalBytes: canonical,
	}); err != nil {
		t.Fatal(err)
	}
	return ref
}

func testModuleManifest(t *testing.T, ports ...moduleapi.PortRef) []byte {
	t.Helper()
	provides := make([]any, len(ports))
	for index, port := range ports {
		provides[index] = map[string]any{
			"name": port.Name, "exact_version": port.ExactVersion,
		}
	}
	encoded, err := json.Marshal(map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          "channelservice.test.module", "version": "v1",
		"runtime": map[string]any{
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
			"entrypoint": "builtin.channelservice.test",
		},
		"provides": provides,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestNewAndResultValidationFailClosed(t *testing.T) {
	if _, err := New(nil, &recordingLoop{}); !errors.Is(err, ErrInvalidIngress) {
		t.Fatalf("New(nil) error = %v", err)
	}
	fixture := newChannelServiceFixture(t, fixtureOptions{enableEndpoint: true})
	fixture.loop = nil
	service, err := New(fixture.store, invalidResultLoop{})
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.input(
		"event-invalid-loop",
		"external-user",
		`{"offset":0}`,
		`{"offset":1}`,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	_, err = service.AdmitAndRun(context.Background(), input)
	if !errors.Is(err, ErrIngressIntegrity) {
		t.Fatalf("invalid Loop result error = %v", err)
	}
}

type invalidResultLoop struct{}

func (invalidResultLoop) Run(
	context.Context,
	loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{
		RunID: "another-run", Disposition: loopapi.DispositionYielded,
		ReasonCode: "TEST_INVALID_RESULT",
	}, nil
}

func ExampleService_AdmitAndRun() {
	// A trusted composition root obtains envelope from its concrete Adapter,
	// then supplies only the Core-owned Tenant/Workspace scope here.
	_ = IngressInput{
		TenantID: "tenant-a", WorkspaceID: "workspace-a",
		Envelope: moduleapi.ChannelInboundEnvelopeV1{},
	}
	fmt.Println("one Current Store, one Run, one Universal Loop")
	// Output: one Current Store, one Run, one Universal Loop
}
