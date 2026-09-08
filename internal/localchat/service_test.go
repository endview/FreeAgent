package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestChatServiceRunsStablePureChatAdmissionAndReusesTerminalRun(
	t *testing.T,
) {
	fixture := newChatServiceFixture(t)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := fixture.input("stable-request", "explain the stable chain", deadline)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if !first.AdmissionCreated ||
		first.RequestID != input.RequestID ||
		!first.Deadline.Equal(deadline) ||
		first.RunID == "" ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		first.Reply != input.Message ||
		first.FailureCode != "" {
		t.Fatalf("first Chat result=%+v", first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("first Chat adapter calls=%d want 1", got)
	}

	databaseBeforeRetry, err := os.ReadFile(fixture.databasePath)
	if err != nil {
		t.Fatalf("read Current Store before retry: %v", err)
	}
	second, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Chat: %v", err)
	}
	if second.AdmissionCreated ||
		second.RunID != first.RunID ||
		second.Reply != first.Reply ||
		second.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("retry Chat result=%+v first=%+v", second, first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("retry replayed adapter: calls=%d want 1", got)
	}
	databaseAfterRetry, err := os.ReadFile(fixture.databasePath)
	if err != nil {
		t.Fatalf("read Current Store after retry: %v", err)
	}
	if !bytes.Equal(databaseBeforeRetry, databaseAfterRetry) {
		t.Fatal("exact terminal retry mutated durable Current Store bytes")
	}

	requestCanonical := fixture.invoker.onlyInput(t)
	request, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1: %v", err)
	}
	if got := request.Messages[len(request.Messages)-1].Content; got != input.Message {
		t.Fatalf("last semantic message=%q want %q", got, input.Message)
	}
	for _, forbidden := range []string{
		input.RequestID,
		first.RunID,
		"chat-member-",
		"chat-recovery-",
		deadline.Format(time.RFC3339Nano),
	} {
		if bytes.Contains(requestCanonical, []byte(forbidden)) {
			t.Fatalf("dynamic identity %q leaked into model request %s", forbidden, requestCanonical)
		}
	}
	if _, found, err := fixture.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.tenantID,
		fixture.workspaceID,
	); err != nil || found {
		t.Fatalf("direct Chat touched Scheduler state found=%v error=%v", found, err)
	}
}

func TestChatServiceConversationRestoresCompletePairsAndAdvancesHeadAtomically(
	t *testing.T,
) {
	fixture := newChatServiceFixture(t)
	ctx := context.Background()
	const conversationID = "conversation-chat-e2e"
	created, err := fixture.store.CreateConversation(
		ctx,
		currentstore.CreateConversationInput{
			ConversationID: conversationID,
			TenantID:       fixture.tenantID,
			PrincipalID:    "principal-chat",
			WorkspaceID:    fixture.workspaceID,
			AgentID:        fixture.agentID,
			ProfileID:      fixture.profileID,
		},
	)
	if err != nil || !created.Created || created.Record.Revision != 0 ||
		created.Record.HeadRunID != "" {
		t.Fatalf("CreateConversation result=%+v error=%v", created, err)
	}

	firstInput := fixture.input(
		"conversation-request-1",
		"first user turn",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	firstInput.ConversationID = conversationID
	first, err := fixture.service.Chat(ctx, firstInput)
	if err != nil {
		t.Fatalf("first Conversation Chat: %v", err)
	}
	if !first.AdmissionCreated || first.ConversationID != conversationID ||
		first.ConversationRevision != 1 || first.Reply != firstInput.Message {
		t.Fatalf("first Conversation result=%+v", first)
	}
	firstHead, err := fixture.store.GetConversation(
		ctx,
		fixture.tenantID,
		conversationID,
	)
	if err != nil || firstHead.Revision != 1 || firstHead.HeadRunID != first.RunID {
		t.Fatalf("first Conversation head=%+v error=%v", firstHead, err)
	}

	secondInput := fixture.input(
		"conversation-request-2",
		"second user turn",
		firstInput.Deadline.Add(time.Minute),
	)
	secondInput.ConversationID = conversationID
	secondInput.ExpectedConversationRevision = firstHead.Revision
	secondInput.ExpectedHeadRunID = firstHead.HeadRunID
	second, err := fixture.service.Chat(ctx, secondInput)
	if err != nil {
		t.Fatalf("second Conversation Chat: %v", err)
	}
	if !second.AdmissionCreated || second.ConversationRevision != 2 ||
		second.Reply != secondInput.Message || second.RunID == first.RunID {
		t.Fatalf("second Conversation result=%+v", second)
	}
	secondHead, err := fixture.store.GetConversation(
		ctx,
		fixture.tenantID,
		conversationID,
	)
	if err != nil || secondHead.Revision != 2 || secondHead.HeadRunID != second.RunID {
		t.Fatalf("second Conversation head=%+v error=%v", secondHead, err)
	}

	requests := fixture.invoker.inputsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("model request count=%d want 2", len(requests))
	}
	secondRequest, err := moduleapi.RestoreModelGenerateRequestV1(requests[1])
	if err != nil {
		t.Fatalf("restore second model request: %v", err)
	}
	wantMessages := []moduleapi.ModelMessageV1{
		{Role: moduleapi.ModelRoleUser, Content: firstInput.Message},
		{Role: moduleapi.ModelRoleAssistant, Content: first.Reply},
		{Role: moduleapi.ModelRoleUser, Content: secondInput.Message},
	}
	if !reflect.DeepEqual(secondRequest.Messages, wantMessages) {
		t.Fatalf(
			"second model messages=%+v want complete pairs %+v",
			secondRequest.Messages,
			wantMessages,
		)
	}

	retry, err := fixture.service.Chat(ctx, secondInput)
	if err != nil || retry.AdmissionCreated || retry.RunID != second.RunID ||
		retry.Reply != second.Reply {
		t.Fatalf("exact second-turn retry=%+v error=%v", retry, err)
	}
	if got := fixture.invoker.callCount(); got != 2 {
		t.Fatalf("exact retry replayed model: calls=%d want 2", got)
	}

	stale := fixture.input(
		"conversation-request-stale",
		"must not fork",
		secondInput.Deadline.Add(time.Minute),
	)
	stale.ConversationID = conversationID
	stale.ExpectedConversationRevision = firstHead.Revision
	stale.ExpectedHeadRunID = firstHead.HeadRunID
	if _, err := fixture.service.Chat(ctx, stale); !errors.Is(
		err,
		currentstore.ErrConversationConflict,
	) {
		t.Fatalf("stale Conversation turn error=%v", err)
	}
	if got := fixture.invoker.callCount(); got != 2 {
		t.Fatalf("stale turn invoked model: calls=%d want 2", got)
	}
}

func TestChatServiceCreatesConversationWithoutAdmittingRun(t *testing.T) {
	fixture := newChatServiceFixture(t)
	ctx := context.Background()
	input := ConversationCreateInput{
		ConversationID: "conversation-service-create",
		TenantID:       fixture.tenantID,
		PrincipalID:    "principal-chat",
		WorkspaceID:    fixture.workspaceID,
		AgentID:        fixture.agentID,
		ProfileID:      fixture.profileID,
	}
	created, err := fixture.service.CreateConversation(ctx, input)
	if err != nil || !created.Created || created.Revision != 0 ||
		created.HeadRunID != "" || created.ConversationID != input.ConversationID {
		t.Fatalf("CreateConversation result=%+v error=%v", created, err)
	}
	retry, err := fixture.service.CreateConversation(ctx, input)
	if err != nil || retry.Created || retry.ConversationID != created.ConversationID ||
		retry.CreatedAt != created.CreatedAt || retry.UpdatedAt != created.UpdatedAt {
		t.Fatalf("exact CreateConversation retry=%+v error=%v", retry, err)
	}
	input.ProfileID = "other-profile"
	if _, err := fixture.service.CreateConversation(ctx, input); !errors.Is(
		err,
		currentstore.ErrConversationConflict,
	) {
		t.Fatalf("conflicting CreateConversation error=%v", err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("Conversation create invoked model %d times", got)
	}
}

func TestChatServiceConversationContextThresholdsKeepPairsIndivisible(
	t *testing.T,
) {
	tests := []struct {
		name        string
		first       string
		second      string
		wantSummary bool
		wantDrops   int
	}{
		{
			name:        "soft watermark summarizes one complete pair",
			first:       strings.Repeat("old-summary-", 300),
			second:      strings.Repeat("current-", 70),
			wantSummary: true,
		},
		{
			name:      "full budget drops one complete pair",
			first:     strings.Repeat("old-drop-", 520),
			second:    strings.Repeat("current-", 70),
			wantDrops: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChatServiceFixtureWithContextPolicy(t, 10000, 1000, 0)
			ctx := context.Background()
			conversationID := "threshold-" + strings.ReplaceAll(test.name, " ", "-")
			if _, err := fixture.store.CreateConversation(
				ctx,
				currentstore.CreateConversationInput{
					ConversationID: conversationID,
					TenantID:       fixture.tenantID,
					PrincipalID:    "principal-chat",
					WorkspaceID:    fixture.workspaceID,
					AgentID:        fixture.agentID,
					ProfileID:      fixture.profileID,
				},
			); err != nil {
				t.Fatalf("CreateConversation: %v", err)
			}
			deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
			firstInput := fixture.input("threshold-request-1", test.first, deadline)
			firstInput.ConversationID = conversationID
			first, err := fixture.service.Chat(ctx, firstInput)
			if err != nil {
				t.Fatalf("first threshold Chat: %v", err)
			}
			secondInput := fixture.input(
				"threshold-request-2",
				test.second,
				deadline.Add(time.Minute),
			)
			secondInput.ConversationID = conversationID
			secondInput.ExpectedConversationRevision = 1
			secondInput.ExpectedHeadRunID = first.RunID
			second, err := fixture.service.Chat(ctx, secondInput)
			if err != nil {
				t.Fatalf("second threshold Chat: %v", err)
			}
			if second.TerminalResult == nil {
				t.Fatalf("second threshold result has no terminal closure: %+v", second)
			}
			dispatch, err := fixture.store.GetModelDispatchRecord(
				ctx,
				second.TerminalResult.AttemptID,
			)
			if err != nil || dispatch.Attempt.ContextCompilation == nil {
				t.Fatalf("threshold dispatch=%+v error=%v", dispatch, err)
			}
			compilation, err := corecontract.RestoreContextCompilationV1(
				dispatch.Attempt.ContextCompilation.CanonicalBytes,
			)
			if err != nil {
				t.Fatalf("restore Context compilation: %v", err)
			}
			if (compilation.Summary != nil) != test.wantSummary ||
				len(compilation.Drops) != test.wantDrops {
				t.Fatalf(
					"threshold compilation summary=%v drops=%d stop=%s original=%d budget=%d watermark=%d final=%d",
					compilation.Summary != nil,
					len(compilation.Drops),
					compilation.StopReason,
					compilation.OriginalEstimateTokens,
					compilation.InputBudgetTokens,
					compilation.RestoreWatermarkTokens,
					compilation.FinalEstimateTokens,
				)
			}
			if test.wantSummary && len(compilation.Summary.SourceTurnDigests) != 1 {
				t.Fatalf("summary source turns=%v", compilation.Summary.SourceTurnDigests)
			}
			if test.wantDrops == 1 &&
				compilation.Drops[0].UnitKind != corecontract.ContextCompilationUnitHistoryTurn {
				t.Fatalf("Drop unit=%+v", compilation.Drops[0])
			}
			request, err := moduleapi.RestoreModelGenerateRequestV1(
				dispatch.Attempt.Request.CanonicalBytes,
			)
			if err != nil {
				t.Fatalf("restore threshold model request: %v", err)
			}
			for index := range request.Messages {
				message := request.Messages[index]
				if message.Content == test.first ||
					message.Role == moduleapi.ModelRoleAssistant &&
						message.Content == first.Reply {
					t.Fatalf(
						"threshold left half/original pair message %d: %+v",
						index,
						message,
					)
				}
			}
			last := request.Messages[len(request.Messages)-1]
			if last.Role != moduleapi.ModelRoleUser || last.Content != test.second {
				t.Fatalf("threshold current task=%+v", last)
			}
		})
	}
}

func TestChatServiceSameRequestIDRejectsChangedStableIntent(t *testing.T) {
	fixture := newChatServiceFixture(t)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := fixture.input("conflict-request", "first body", deadline)
	if _, err := fixture.service.Chat(context.Background(), input); err != nil {
		t.Fatalf("first Chat: %v", err)
	}

	input.Message = "changed body"
	result, err := fixture.service.Chat(context.Background(), input)
	if !errors.Is(err, currentstore.ErrAdmissionConflict) {
		t.Fatalf("changed intent error=%v result=%+v", err, result)
	}
	if result.RequestID != input.RequestID || !result.Deadline.Equal(deadline) {
		t.Fatalf("partial conflict result lost retry identity: %+v", result)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("conflicting retry invoked adapter: calls=%d want 1", got)
	}
}

func TestChatServiceExplicitRequestRequiresStableUTCDeadline(t *testing.T) {
	fixture := newChatServiceFixture(t)
	input := fixture.input("explicit-request", "hello", time.Time{})
	if _, err := fixture.service.Chat(context.Background(), input); !errors.Is(err, ErrInvalidChat) {
		t.Fatalf("missing deadline error=%v", err)
	}

	input.Deadline = time.Date(
		2030, time.January, 2, 3, 4, 5, 0,
		time.FixedZone("UTC+8", 8*60*60),
	)
	if _, err := fixture.service.Chat(context.Background(), input); !errors.Is(err, ErrInvalidChat) {
		t.Fatalf("non-UTC deadline error=%v", err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("invalid request invoked adapter %d times", got)
	}
}

func TestChatServiceAutoIdentityCanBeRetriedAndWaitingIsReturnedUnchanged(
	t *testing.T,
) {
	fixture := newChatServiceFixture(t)
	waiting := &chatWaitingLoop{}
	service, err := NewChatService(fixture.store, waiting)
	if err != nil {
		t.Fatalf("NewChatService: %v", err)
	}
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 600, time.UTC)
	service.now = func() time.Time { return fixedNow }
	service.newRequestID = func() (string, error) { return "generated-request", nil }
	input := fixture.input("", "waiting body", time.Time{})

	first, err := service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("auto Chat: %v", err)
	}
	if first.RequestID != "generated-request" ||
		!first.Deadline.Equal(fixedNow.Add(chatDefaultDeadline)) ||
		!first.AdmissionCreated ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.FrameRevision != 77 ||
		first.LoopResult.ReasonCode != "TEST_RECONCILIATION_REQUIRED" ||
		first.TerminalResult != nil ||
		first.Reply != "" ||
		first.FailureCode != "" {
		t.Fatalf("auto waiting result=%+v", first)
	}

	input.RequestID = first.RequestID
	input.Deadline = first.Deadline
	second, err := service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("waiting retry: %v", err)
	}
	if second.AdmissionCreated ||
		second.RunID != first.RunID ||
		second.LoopResult != first.LoopResult ||
		second.TerminalResult != nil {
		t.Fatalf("waiting retry=%+v first=%+v", second, first)
	}
	if waiting.calls != 2 {
		t.Fatalf("Loop calls=%d want one bounded call per Chat", waiting.calls)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("waiting test reached model adapter %d times", got)
	}
}

type chatServiceFixture struct {
	store        *currentstore.Store
	loop         *coreloop.UniversalLoop
	service      *ChatService
	invoker      *recordingChatInvoker
	databasePath string
	tenantID     string
	agentID      string
	workspaceID  string
	profileID    string
}

func (fixture *chatServiceFixture) input(
	requestID string,
	message string,
	deadline time.Time,
) ChatInput {
	return ChatInput{
		TenantID:    fixture.tenantID,
		PrincipalID: "principal-chat",
		WorkspaceID: fixture.workspaceID,
		AgentID:     fixture.agentID,
		ProfileID:   fixture.profileID,
		Message:     message,
		RequestID:   requestID,
		Deadline:    deadline,
	}
}

func newChatServiceFixture(t *testing.T) *chatServiceFixture {
	return newChatServiceFixtureWithContextPolicy(t, 32768, 4096, 8)
}

func newChatServiceFixtureWithContextPolicy(
	t *testing.T,
	contextWindowTokens uint64,
	reservedOutputTokens uint64,
	recentHistoryTurns uint64,
) *chatServiceFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatalf("InitFreshCurrentStore: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close Current Store: %v", err)
		}
	})

	const (
		tenantID       = "tenant-chat"
		agentID        = "agent-chat"
		workspaceID    = "workspace-chat"
		profileID      = "profile-chat"
		providerName   = "test-provider"
		modelName      = "test-model"
		billingVersion = "billing-v1"
		priceID        = "price-chat-v1"
	)
	if _, err := store.PutModelPriceSnapshot(
		ctx,
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: priceID,
			Provider:        providerName,
			Model:           modelName,
			BillingVersion:  billingVersion,
			Currency:        "USD",
			PricingStatus:   corecontract.PricingKnown,
			Pricing: json.RawMessage(
				`{"input_per_million":1,"output_per_million":2}`,
			),
		},
	); err != nil {
		t.Fatalf("PutModelPriceSnapshot: %v", err)
	}
	_, configCanonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        providerName,
			Model:           modelName,
			ModelBuildID:    "test-model-build-v1",
			BillingVersion:  billingVersion,
			PriceSnapshotID: priceID,
			Parameters:      json.RawMessage(`{"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef := putChatContent(t, store, currentstore.ContentConfig, configCanonical)
	authorityRef := putChatContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		[]byte(`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`),
	)
	contextPolicy := putChatContextPolicy(
		t,
		store,
		"context-policy",
		contextWindowTokens,
		reservedOutputTokens,
		recentHistoryTurns,
	)
	costPolicy := putChatPolicy(t, store, "cost-policy", corecontract.PolicyCost)
	schedulingPolicy := putChatPolicy(
		t,
		store,
		"scheduling-policy",
		corecontract.PolicyScheduling,
	)
	budgetPolicy := putChatPolicy(t, store, "budget-policy", corecontract.PolicyCost)

	manifestBytes := chatModuleManifest(t)
	parsedManifest, _, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1: %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		chatJSONMediaType,
		manifestBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      "installation-chat-model",
		ModuleID:            parsedManifest.ID,
		ExactVersion:        parsedManifest.Version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       manifestBytes,
		ArtifactDigest:      strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	activation, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       "activation-chat-model",
		TenantID:           tenantID,
		InstanceID:         "instance-chat-model",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "localchat.test.echo",
	})
	if err != nil {
		t.Fatalf("ActivateModule: %v", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}

	agent := corecontract.AgentRef{
		ID: agentID, Version: "v1", Digest: strings.Repeat("2", 64),
	}
	workspace := corecontract.WorkspaceRef{
		ID: workspaceID, Version: "v1", Digest: strings.Repeat("3", 64),
	}
	profile := corecontract.ProfileRef{
		ID: profileID, Version: "v1", Digest: strings.Repeat("4", 64),
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-chat",
			TenantID:      tenantID,
			Revision:      1,
			Agents:        []corecontract.AgentRef{agent},
			Workspaces: []controlcontract.WorkspaceDefinition{{
				Workspace: workspace, BudgetPolicy: budgetPolicy,
			}},
			Profiles: []controlcontract.ProfileDefinition{{
				Profile:          profile,
				ContextPolicy:    contextPolicy,
				CostPolicy:       costPolicy,
				SchedulingPolicy: schedulingPolicy,
				Bindings: []controlcontract.BindingSpec{{
					Port:                chatModelGeneratePortV1,
					InstanceID:          provider.InstanceID,
					ConfigRef:           configRef,
					AuthorityCeilingRef: authorityRef,
					FailurePolicy:       moduleapi.FailureRequired,
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("NewControlSnapshot: %v", err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(
		controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          "catalog-chat",
			Generation:            1,
			TenantID:              tenantID,
			ControlSnapshotID:     controlRef.SnapshotID,
			ControlSnapshotDigest: controlRef.Digest,
			Entries: []controlcontract.CatalogEntry{{
				Activation: provider,
				Provides:   []moduleapi.PortRef{chatModelGeneratePortV1},
			}},
		},
	)
	if err != nil {
		t.Fatalf("NewCatalogGeneration: %v", err)
	}
	if _, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: 0,
			NewPointerRevision:      1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("PublishControlCatalog: %v", err)
	}
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho: %v", err)
	}
	invoker := &recordingChatInvoker{delegate: echo}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatalf("NewUniversalLoop: %v", err)
	}
	service, err := NewChatService(store, loop)
	if err != nil {
		t.Fatalf("NewChatService: %v", err)
	}
	return &chatServiceFixture{
		store:        store,
		loop:         loop,
		service:      service,
		invoker:      invoker,
		databasePath: path,
		tenantID:     tenantID,
		agentID:      agentID,
		workspaceID:  workspaceID,
		profileID:    profileID,
	}
}

type recordingChatInvoker struct {
	mu       sync.Mutex
	delegate modulehost.ModuleInvoker
	inputs   [][]byte
}

func (invoker *recordingChatInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.mu.Lock()
	invoker.inputs = append(
		invoker.inputs,
		append([]byte(nil), prepared.Invocation.Input...),
	)
	invoker.mu.Unlock()
	return invoker.delegate.Invoke(ctx, prepared)
}

func (invoker *recordingChatInvoker) callCount() int {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return len(invoker.inputs)
}

func (invoker *recordingChatInvoker) onlyInput(t *testing.T) []byte {
	t.Helper()
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	if len(invoker.inputs) != 1 {
		t.Fatalf("recorded model requests=%d want 1", len(invoker.inputs))
	}
	return append([]byte(nil), invoker.inputs[0]...)
}

func (invoker *recordingChatInvoker) inputsSnapshot() [][]byte {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	result := make([][]byte, len(invoker.inputs))
	for index := range invoker.inputs {
		result[index] = append([]byte(nil), invoker.inputs[index]...)
	}
	return result
}

type chatWaitingLoop struct {
	calls int
}

func (loop *chatWaitingLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	loop.calls++
	return loopapi.RunResult{
		RunID:         input.RunID,
		Disposition:   loopapi.DispositionWaitingReconciliation,
		FrameRevision: 77,
		ReasonCode:    "TEST_RECONCILIATION_REQUIRED",
	}, nil
}

func chatModuleManifest(t *testing.T) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          "test.chat.model",
		"version":     "v1",
		"runtime": map[string]any{
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
			"entrypoint": "builtin.localchat.test.echo",
		},
		"provides": []any{map[string]any{
			"name":          moduleapi.PortNameModelGenerate,
			"exact_version": moduleapi.PortVersionV1,
		}},
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

func putChatPolicy(
	t *testing.T,
	store *currentstore.Store,
	id string,
	policyType corecontract.PolicyType,
) corecontract.PolicyRef {
	t.Helper()
	body := json.RawMessage(`{"enabled":true}`)
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
		Digest:         ref.Digest,
		Kind:           currentstore.ContentPolicy,
		MediaType:      chatJSONMediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("PutContent policy %s: %v", id, err)
	}
	return ref
}

func putChatContextPolicy(
	t *testing.T,
	store *currentstore.Store,
	id string,
	contextWindowTokens uint64,
	reservedOutputTokens uint64,
	recentHistoryTurns uint64,
) corecontract.PolicyRef {
	t.Helper()
	policy, canonical, err := corecontract.NewContextPolicyV1(
		corecontract.ContextPolicyV1{
			SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
			ContextWindowTokens:  contextWindowTokens,
			ReservedOutputTokens: reservedOutputTokens,
			RecentHistoryTurns:   recentHistoryTurns,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		},
	)
	if err != nil {
		t.Fatalf("NewContextPolicyV1: %v", err)
	}
	_, ref, documentCanonical, err := corecontract.NewPolicyDocument(
		id,
		"v1",
		corecontract.PolicyContext,
		canonical,
	)
	if err != nil {
		t.Fatalf("NewPolicyDocument(Context): %v", err)
	}
	restored, err := corecontract.RestoreContextPolicyV1(policyBodyForTest(
		t,
		documentCanonical,
		ref,
	))
	if err != nil || restored != policy {
		t.Fatalf("restore Context policy=%+v error=%v want=%+v", restored, err, policy)
	}
	if _, err := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest:         ref.Digest,
		Kind:           currentstore.ContentPolicy,
		MediaType:      chatJSONMediaType,
		CanonicalBytes: documentCanonical,
	}); err != nil {
		t.Fatalf("PutContent(Context policy): %v", err)
	}
	return ref
}

func policyBodyForTest(
	t *testing.T,
	canonical []byte,
	ref corecontract.PolicyRef,
) json.RawMessage {
	t.Helper()
	document, err := corecontract.RestorePolicyDocument(canonical, ref)
	if err != nil {
		t.Fatalf("RestorePolicyDocument: %v", err)
	}
	return document.Body
}

func putChatContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("CanonicalJSON %s: %v", kind, err)
	}
	digest, err := currentstore.ComputeContentDigest(
		kind,
		chatJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest %s: %v", kind, err)
	}
	if _, err := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      chatJSONMediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("PutContent %s: %v", kind, err)
	}
	return digest
}
