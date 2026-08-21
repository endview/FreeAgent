package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekcost"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w5WorkspaceTransferLiveEnabledEnvironment = "FREEAGENT_W5_X1D_LIVE"

const w5LiveMaximumDelegatedHTTPCalls = uint64(4)

var errW5LiveHTTPCallLimit = errors.New(
	"W5-X1D live HTTP call limit reached before network delegation",
)

func TestW5LiveHTTPCallCounterCapsNetworkDelegation(t *testing.T) {
	var networkCalls atomic.Uint64
	counter := &w5LiveHTTPCallCounter{
		delegate: w5LiveRoundTripperFunc(func(*http.Request) (*http.Response, error) {
			networkCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       http.NoBody,
			}, nil
		}),
		maximumDelegated: w5LiveMaximumDelegatedHTTPCalls,
	}

	const attempts = 32
	var succeeded atomic.Uint64
	var blocked atomic.Uint64
	var unexpected atomic.Uint64
	var group sync.WaitGroup
	group.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer group.Done()
			response, err := counter.RoundTrip(&http.Request{})
			switch {
			case err == nil && response != nil:
				succeeded.Add(1)
				_ = response.Body.Close()
			case errors.Is(err, errW5LiveHTTPCallLimit) && response == nil:
				blocked.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	group.Wait()

	snapshot := counter.Snapshot()
	if networkCalls.Load() != w5LiveMaximumDelegatedHTTPCalls ||
		succeeded.Load() != w5LiveMaximumDelegatedHTTPCalls ||
		blocked.Load() != attempts-w5LiveMaximumDelegatedHTTPCalls ||
		unexpected.Load() != 0 || snapshot.Attempts != attempts ||
		snapshot.Delegated != w5LiveMaximumDelegatedHTTPCalls ||
		snapshot.Blocked != attempts-w5LiveMaximumDelegatedHTTPCalls ||
		snapshot.Succeeded2xx != w5LiveMaximumDelegatedHTTPCalls ||
		snapshot.Rejected4xx != 0 || snapshot.Server5xx != 0 ||
		snapshot.OtherStatus != 0 || snapshot.TransportErrors != 0 {
		t.Fatalf(
			"HTTP cap network/succeeded/blocked/unexpected=%d/%d/%d/%d snapshot=%+v",
			networkCalls.Load(),
			succeeded.Load(),
			blocked.Load(),
			unexpected.Load(),
			snapshot,
		)
	}
}

type w5LiveRoundTripperFunc func(*http.Request) (*http.Response, error)

func (invoke w5LiveRoundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return invoke(request)
}

func TestW5X1DLiveFixturePublishesValidOptionalTransfer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize W5-X1D fixture data: %v", err)
	}
	calls := &w5LiveHTTPCallCounter{
		delegate:         http.DefaultTransport,
		maximumDelegated: w5LiveMaximumDelegatedHTTPCalls,
	}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{DeepSeek: &productionDeepSeekRuntimeConfig{
			APIKeyResolver: &environmentDeepSeekAPIKeyResolver{
				name: defaultDeepSeekAPIKeyEnvironment,
				lookup: func(string) (string, bool) {
					return "not-a-secret", true
				},
			},
			HTTPClient: &http.Client{Transport: calls},
		}},
	)
	if err != nil {
		t.Fatalf("open W5-X1D fixture composition: %v", err)
	}
	defer func() {
		if closeErr := composition.Close(); closeErr != nil {
			t.Errorf("close W5-X1D fixture composition: %v", closeErr)
		}
	}()
	targetWorkspaceID := publishW5LiveWorkspaceTransfer(
		t,
		ctx,
		composition.store,
	)
	_, control, _, err := composition.store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil || len(control.CompositeAgents) != 1 ||
		len(control.Workspaces) != 2 ||
		len(control.CompositeAgents[0].Members) != 2 ||
		control.CompositeAgents[0].Members[0].TargetWorkspaceID != "" ||
		control.CompositeAgents[0].Members[1].TargetWorkspaceID !=
			targetWorkspaceID {
		t.Fatalf("published W5-X1D fixture is incomplete: control=%+v error=%v", control, err)
	}
	if got := calls.Snapshot(); got != (w5LiveHTTPCallSnapshot{}) {
		t.Fatalf(
			"fixture publication reached the model HTTP boundary: attempts=%d delegated=%d blocked=%d",
			got.Attempts,
			got.Delegated,
			got.Blocked,
		)
	}
}

// TestW5X1DLiveDeepSeekWorkspaceTransfer is deliberately opt-in. It exercises
// the production DeepSeek adapter through exactly two Specialist Runs, one
// Reviewer Run and one Root merge Run. One Specialist is placed in a second
// Workspace, so only the trusted TASK_SUMMARY request and strict
// SPECIALIST_RESULT return may cross that boundary. The test never reads or
// prints the credential value; t.TempDir owns every Store and artifact.
func TestW5X1DLiveDeepSeekWorkspaceTransfer(t *testing.T) {
	if os.Getenv(w5WorkspaceTransferLiveEnabledEnvironment) != "1" {
		t.Skip("set FREEAGENT_W5_X1D_LIVE=1 to run the explicit live transfer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize W5-X1D live data: %v", err)
	}

	calls := &w5LiveHTTPCallCounter{
		delegate:         http.DefaultTransport,
		maximumDelegated: w5LiveMaximumDelegatedHTTPCalls,
	}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{DeepSeek: &productionDeepSeekRuntimeConfig{
			APIKeyResolver: &environmentDeepSeekAPIKeyResolver{
				name:   defaultDeepSeekAPIKeyEnvironment,
				lookup: os.LookupEnv,
			},
			HTTPClient: &http.Client{
				Transport: calls,
				Timeout:   2 * time.Minute,
			},
		}},
	)
	if err != nil {
		t.Fatalf("open W5-X1D live composition: %v", err)
	}
	defer func() {
		if closeErr := composition.Close(); closeErr != nil {
			t.Errorf("close W5-X1D live composition: %v", closeErr)
		}
	}()

	targetWorkspaceID := publishW5LiveWorkspaceTransfer(
		t,
		ctx,
		composition.store,
	)
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "deepseek-chat",
		RequestID:   "w5-x1d-live-transfer-v1",
		Deadline: time.Now().UTC().Round(0).Add(
			6 * time.Minute,
		),
		Message: "Produce a self-contained two-part design for a tiny HTTP " +
			"health service. The architecture specialist should define the " +
			"boundary; the implementation specialist should define one Go " +
			"handler. No immutable evidence references are provided, so both " +
			"specialists must use an empty evidence array and must not invent " +
			"references or citations. Put handler code or pseudocode only inside " +
			"the proposal string, do not add output fields, and keep each of " +
			"assumptions, risks, and conflicts to at most two short items. The " +
			"reviewer should approve when both bounded contributions are " +
			"structurally valid and non-contradictory, and the root should " +
			"merge them concisely.",
	}
	first, err := composition.composite.Chat(ctx, input)
	if err != nil || first.TerminalResult == nil || first.Reviewer == nil ||
		first.FailureCode != "" {
		terminalState := ""
		if first.TerminalResult != nil {
			terminalState = string(first.TerminalResult.State)
		}
		httpState := calls.Snapshot()
		t.Fatalf(
			"execute W5-X1D live Composite failed: error_present=%t root_run_id=%q reviewer_present=%t terminal_present=%t terminal_state=%q failure_code=%q child_outcomes=%q attempt_outcomes=%q http_attempts=%d http_delegated=%d http_blocked=%d http_2xx=%d http_4xx=%d http_5xx=%d http_other=%d transport_errors=%d",
			err != nil,
			first.RootRunID,
			first.Reviewer != nil,
			first.TerminalResult != nil,
			terminalState,
			first.FailureCode,
			w5LiveChildOutcomeSummary(first.Children),
			w5LiveChildAttemptOutcomeSummary(
				ctx,
				composition.store,
				first.Children,
			),
			httpState.Attempts,
			httpState.Delegated,
			httpState.Blocked,
			httpState.Succeeded2xx,
			httpState.Rejected4xx,
			httpState.Server5xx,
			httpState.OtherStatus,
			httpState.TransportErrors,
		)
	}
	firstHTTP := calls.Snapshot()
	if firstHTTP.Attempts != w5LiveMaximumDelegatedHTTPCalls ||
		firstHTTP.Delegated != w5LiveMaximumDelegatedHTTPCalls ||
		firstHTTP.Blocked != 0 ||
		firstHTTP.Succeeded2xx != w5LiveMaximumDelegatedHTTPCalls ||
		firstHTTP.Rejected4xx != 0 || firstHTTP.Server5xx != 0 ||
		firstHTTP.OtherStatus != 0 || firstHTTP.TransportErrors != 0 {
		t.Fatalf(
			"live HTTP boundary attempts=%d delegated=%d blocked=%d http_2xx=%d http_4xx=%d http_5xx=%d http_other=%d transport_errors=%d; want four successful delegations only",
			firstHTTP.Attempts,
			firstHTTP.Delegated,
			firstHTTP.Blocked,
			firstHTTP.Succeeded2xx,
			firstHTTP.Rejected4xx,
			firstHTTP.Server5xx,
			firstHTTP.OtherStatus,
			firstHTTP.TransportErrors,
		)
	}

	crossChild := loadW5LiveCrossWorkspaceChild(
		t,
		ctx,
		composition.store,
		first,
		targetWorkspaceID,
	)
	requestTransfer := assertW5LiveRequestTransfer(t, crossChild)
	reviewer := loadW5LiveRun(
		t,
		ctx,
		composition.store,
		first.Reviewer.RunID,
	)
	rootRun := loadW5LiveRun(t, ctx, composition.store, first.RootRunID)
	resultEnvelopeRef := assertW5LiveSuccessfulResultOrder(
		t,
		crossChild,
		reviewer,
		rootRun,
	)

	usage, err := composition.store.GetCompositeFamilyUsageProjection(
		ctx,
		first.RootRunID,
	)
	if err != nil {
		t.Fatalf("project W5-X1D live Usage: %v", err)
	}
	price, err := composition.store.GetModelPriceSnapshot(
		ctx,
		"price-deepseek-v4-flash-2026-08-04",
	)
	if err != nil {
		t.Fatalf("load W5-X1D frozen PriceSnapshot: %v", err)
	}
	metrics := summarizeW5LiveUsage(t, usage, price.Snapshot)
	if metrics.Attempts != 4 || metrics.Succeeded != 4 {
		t.Fatalf("live Usage attempts=%+v want four succeeded Attempts", metrics)
	}
	firstAttemptIdentities := w5LiveAttemptIdentities(usage)

	retry, err := composition.composite.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated ||
		retry.RootRunID != first.RootRunID || retry.Reply != first.Reply ||
		retry.TerminalResult == nil || retry.FailureCode != "" {
		terminalState := ""
		if retry.TerminalResult != nil {
			terminalState = string(retry.TerminalResult.State)
		}
		httpState := calls.Snapshot()
		t.Fatalf(
			"exact W5-X1D retry failed: error_present=%t admission_created=%t same_root=%t same_reply=%t terminal_present=%t terminal_state=%q failure_code=%q http_attempts=%d http_delegated=%d http_blocked=%d",
			err != nil,
			retry.AdmissionCreated,
			retry.RootRunID == first.RootRunID,
			retry.Reply == first.Reply,
			retry.TerminalResult != nil,
			terminalState,
			retry.FailureCode,
			httpState.Attempts,
			httpState.Delegated,
			httpState.Blocked,
		)
	}
	retryHTTP := calls.Snapshot()
	newHTTPAttempts := retryHTTP.Attempts - firstHTTP.Attempts
	newHTTPDelegated := retryHTTP.Delegated - firstHTTP.Delegated
	newHTTPBlocked := retryHTTP.Blocked - firstHTTP.Blocked
	if retryHTTP != firstHTTP || newHTTPAttempts != 0 ||
		newHTTPDelegated != 0 || newHTTPBlocked != 0 {
		t.Fatalf(
			"exact retry changed HTTP boundary: new_attempts=%d new_delegated=%d new_blocked=%d",
			newHTTPAttempts,
			newHTTPDelegated,
			newHTTPBlocked,
		)
	}
	retryUsage, err := composition.store.GetCompositeFamilyUsageProjection(
		ctx,
		retry.RootRunID,
	)
	if err != nil {
		t.Fatalf("project exact retry Usage: %v", err)
	}
	retryMetrics := summarizeW5LiveUsage(t, retryUsage, price.Snapshot)
	if retryUsage.Aggregate.AttemptSlotsUsed < usage.Aggregate.AttemptSlotsUsed {
		t.Fatal("exact retry reduced the authoritative model Attempt count")
	}
	newModelAttempts := retryUsage.Aggregate.AttemptSlotsUsed -
		usage.Aggregate.AttemptSlotsUsed
	if newModelAttempts != 0 {
		t.Fatalf("exact retry added model Attempts: %d", newModelAttempts)
	}
	if !sameW5LiveAttemptIdentities(
		firstAttemptIdentities,
		w5LiveAttemptIdentities(retryUsage),
	) {
		t.Fatal("exact retry changed persisted model Attempt identities")
	}
	if retryMetrics != metrics {
		t.Fatalf("exact retry changed Usage: first=%+v retry=%+v", metrics, retryMetrics)
	}

	report, err := json.Marshal(struct {
		RootRunID                 string             `json:"root_run_id"`
		TargetWorkspaceID         string             `json:"target_workspace_id"`
		RequestEnvelopeRef        string             `json:"request_envelope_ref"`
		ResultEnvelopeRef         string             `json:"result_envelope_ref"`
		HTTPCalls                 uint64             `json:"http_calls"`
		HTTPAttempts              uint64             `json:"http_attempts"`
		HTTPDelegated             uint64             `json:"http_delegated"`
		HTTPBlocked               uint64             `json:"http_blocked"`
		HTTP2xx                   uint64             `json:"http_2xx"`
		Usage                     w5LiveUsageSummary `json:"usage"`
		ExactRetryNewHTTPCalls    uint64             `json:"exact_retry_new_http_calls"`
		ExactRetryNewHTTPAttempts uint64             `json:"exact_retry_new_http_attempts"`
		ExactRetryNewHTTPBlocked  uint64             `json:"exact_retry_new_http_blocked"`
		ExactRetryNewAttempts     uint32             `json:"exact_retry_new_attempts"`
		RequestTransferVerified   bool               `json:"request_transfer_verified"`
		ResultTransfersVerified   bool               `json:"result_transfers_verified"`
	}{
		RootRunID:                 first.RootRunID,
		TargetWorkspaceID:         targetWorkspaceID,
		RequestEnvelopeRef:        requestTransfer.EnvelopeRef,
		ResultEnvelopeRef:         resultEnvelopeRef,
		HTTPCalls:                 retryHTTP.Delegated,
		HTTPAttempts:              retryHTTP.Attempts,
		HTTPDelegated:             retryHTTP.Delegated,
		HTTPBlocked:               retryHTTP.Blocked,
		HTTP2xx:                   retryHTTP.Succeeded2xx,
		Usage:                     metrics,
		ExactRetryNewHTTPCalls:    newHTTPDelegated,
		ExactRetryNewHTTPAttempts: newHTTPAttempts,
		ExactRetryNewHTTPBlocked:  newHTTPBlocked,
		ExactRetryNewAttempts:     newModelAttempts,
		RequestTransferVerified:   true,
		ResultTransfersVerified:   true,
	})
	if err != nil {
		t.Fatalf("marshal sanitized W5-X1D report: %v", err)
	}
	t.Logf("W5_X1D_LIVE_RESULT %s", report)
}

type w5LiveHTTPCallCounter struct {
	delegate         http.RoundTripper
	maximumDelegated uint64
	attempts         atomic.Uint64
	delegated        atomic.Uint64
	blocked          atomic.Uint64
	succeeded2xx     atomic.Uint64
	rejected4xx      atomic.Uint64
	server5xx        atomic.Uint64
	otherStatus      atomic.Uint64
	transportErrors  atomic.Uint64
}

type w5LiveHTTPCallSnapshot struct {
	Attempts        uint64
	Delegated       uint64
	Blocked         uint64
	Succeeded2xx    uint64
	Rejected4xx     uint64
	Server5xx       uint64
	OtherStatus     uint64
	TransportErrors uint64
}

func (counter *w5LiveHTTPCallCounter) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	counter.attempts.Add(1)
	for {
		delegated := counter.delegated.Load()
		if counter.maximumDelegated != 0 &&
			delegated >= counter.maximumDelegated {
			counter.blocked.Add(1)
			return nil, errW5LiveHTTPCallLimit
		}
		if counter.delegated.CompareAndSwap(delegated, delegated+1) {
			break
		}
	}
	response, err := counter.delegate.RoundTrip(request)
	if err != nil {
		counter.transportErrors.Add(1)
		return response, err
	}
	if response == nil {
		counter.otherStatus.Add(1)
		return nil, nil
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		counter.succeeded2xx.Add(1)
	case response.StatusCode >= 400 && response.StatusCode < 500:
		counter.rejected4xx.Add(1)
	case response.StatusCode >= 500 && response.StatusCode < 600:
		counter.server5xx.Add(1)
	default:
		counter.otherStatus.Add(1)
	}
	return response, nil
}

func (counter *w5LiveHTTPCallCounter) Snapshot() w5LiveHTTPCallSnapshot {
	return w5LiveHTTPCallSnapshot{
		Attempts:        counter.attempts.Load(),
		Delegated:       counter.delegated.Load(),
		Blocked:         counter.blocked.Load(),
		Succeeded2xx:    counter.succeeded2xx.Load(),
		Rejected4xx:     counter.rejected4xx.Load(),
		Server5xx:       counter.server5xx.Load(),
		OtherStatus:     counter.otherStatus.Load(),
		TransportErrors: counter.transportErrors.Load(),
	}
}

func w5LiveChildOutcomeSummary(
	children []localchat.CompositeChildChatResult,
) string {
	parts := make([]string, len(children))
	for index, child := range children {
		parts[index] = fmt.Sprintf(
			"%d:%s:%s",
			index,
			child.LoopResult.Disposition,
			child.LoopResult.ReasonCode,
		)
	}
	return strings.Join(parts, ",")
}

func w5LiveChildAttemptOutcomeSummary(
	ctx context.Context,
	store *currentstore.Store,
	children []localchat.CompositeChildChatResult,
) string {
	parts := make([]string, len(children))
	for index, child := range children {
		lease, err := store.AcquireCurrentRunLease(
			ctx,
			currentstore.AcquireCurrentRunLeaseInput{
				RunID: child.RunID, OwnerID: "w5-x1d-live-diagnostic", TTL: time.Minute,
			},
		)
		if err != nil {
			parts[index] = fmt.Sprintf("%d:run_unavailable=lease", index)
			continue
		}
		run, loadErr := store.LoadRunForLoop(ctx, lease)
		releaseErr := store.ReleaseRunLease(ctx, lease)
		if loadErr != nil {
			parts[index] = fmt.Sprintf("%d:run_unavailable=load", index)
			continue
		}
		if releaseErr != nil {
			parts[index] = fmt.Sprintf("%d:run_unavailable=release", index)
			continue
		}
		if len(run.ModelDispatches) != 1 {
			parts[index] = fmt.Sprintf(
				"%d:dispatch_count=%d",
				index,
				len(run.ModelDispatches),
			)
			continue
		}
		attempt := run.ModelDispatches[0].Attempt
		parts[index] = fmt.Sprintf(
			"%d:%s:%s:result=%t:provider_request=%t:receipt=%t",
			index,
			attempt.State,
			attempt.ErrorClassification,
			attempt.ResultRef != "",
			attempt.ProviderRequestID != "",
			attempt.ProviderReceiptRef != "",
		)
	}
	return strings.Join(parts, ",")
}

func publishW5LiveWorkspaceTransfer(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
) string {
	t.Helper()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load W5-X1D Control/Catalog: %v", err)
	}
	if len(control.Agents) != 1 || len(control.Profiles) != 1 ||
		len(control.Workspaces) != 1 || len(control.Profiles[0].Bindings) != 1 {
		t.Fatalf("W5-X1D base composition has unexpected shape: %+v", control)
	}

	rootWorkspace := control.Workspaces[0]
	targetWorkspace := rootWorkspace
	targetWorkspace.Workspace = corecontract.WorkspaceRef{
		ID:      "w5-live-specialist-workspace",
		Version: "v1",
		Digest:  strings.Repeat("7", moduleapi.SHA256HexLength),
	}
	targetWorkspace.ChannelEndpoints = nil
	targetWorkspace.ChannelIdentities = nil
	rootWorkspace.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		w5LiveTransferGrant(
			"w5-live-root-target",
			rootWorkspace.Workspace,
			targetWorkspace.Workspace,
			true,
		),
	}
	targetWorkspace.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		w5LiveTransferGrant(
			"w5-live-target-root",
			targetWorkspace.Workspace,
			rootWorkspace.Workspace,
			false,
		),
	}
	control.Workspaces = []controlcontract.WorkspaceDefinition{
		rootWorkspace,
		targetWorkspace,
	}

	jsonConfig := moduleapi.ModelBindingConfigV1{
		SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		ModelBuildID:    localDeepSeekFlashBuild,
		BillingVersion:  "deepseek-public-price-2026-08-04",
		PriceSnapshotID: "price-deepseek-v4-flash-2026-08-04",
		Parameters: json.RawMessage(
			`{"max_tokens":1024,"response_format":{"type":"json_object"},"temperature":0,"thinking":{"type":"disabled"}}`,
		),
	}
	_, jsonConfigCanonical, err := moduleapi.NewModelBindingConfigV1(jsonConfig)
	if err != nil {
		t.Fatalf("freeze W5-X1D JSON model Config: %v", err)
	}
	jsonConfigRef := putW5LiveContent(
		t,
		ctx,
		store,
		currentstore.ContentConfig,
		jsonConfigCanonical,
	)

	baseProfile := control.Profiles[0]
	newProfile := func(id string, digestByte string) controlcontract.ProfileDefinition {
		profile := cloneW5LiveProfile(baseProfile)
		profile.Profile = corecontract.ProfileRef{
			ID: id, Version: "v1", Digest: strings.Repeat(digestByte, 64),
		}
		profile.Bindings[0].ConfigRef = jsonConfigRef
		return profile
	}
	architectureProfile := newProfile("w5-live-architecture-profile", "8")
	implementationProfile := newProfile("w5-live-implementation-profile", "9")
	reviewerProfile := newProfile("w5-live-reviewer-profile", "a")
	architectureAgent := corecontract.AgentRef{
		ID: "w5-live-architecture-agent", Version: "v1", Digest: strings.Repeat("b", 64),
	}
	implementationAgent := corecontract.AgentRef{
		ID: "w5-live-implementation-agent", Version: "v1", Digest: strings.Repeat("c", 64),
	}
	reviewerAgent := corecontract.AgentRef{
		ID: "w5-live-reviewer-agent", Version: "v1", Digest: strings.Repeat("d", 64),
	}
	control.Agents = append(
		control.Agents,
		architectureAgent,
		implementationAgent,
		reviewerAgent,
	)
	control.Profiles = append(
		control.Profiles,
		architectureProfile,
		implementationProfile,
		reviewerProfile,
	)
	control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{{
		SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
		AgentID:              defaultAgentID,
		CoordinatorProfileID: "deepseek-chat",
		Members: []controlcontract.CompositeAgentMemberV1{
			{
				SlotID:            "slot-architecture",
				AgentID:           architectureAgent.ID,
				ProfileID:         architectureProfile.Profile.ID,
				FocusID:           "architecture-boundary",
				WeightBasisPoints: 6000,
			},
			{
				SlotID:            "slot-implementation",
				AgentID:           implementationAgent.ID,
				ProfileID:         implementationProfile.Profile.ID,
				FocusID:           "go-handler",
				WeightBasisPoints: 4000,
				TargetWorkspaceID: targetWorkspace.Workspace.ID,
			},
		},
		Reviewer: &controlcontract.CompositeReviewerDefinitionV1{
			SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
			AgentID:         reviewerAgent.ID,
			ProfileID:       reviewerProfile.Profile.ID,
			MaxOutputTokens: 1024,
			Policy:          corecontract.CompositeReviewerPolicyResultsGateV1,
		},
		Decision: &controlcontract.CompositeDecisionDefinitionV1{
			SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
		},
	}}
	control.SnapshotID = "control-w5-x1d-live-transfer"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze W5-X1D Control: %v", err)
	}
	catalog.GenerationID = "catalog-w5-x1d-live-transfer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze W5-X1D Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("publish W5-X1D Control/Catalog: %v", err)
	}
	return targetWorkspace.Workspace.ID
}

func putW5LiveContent(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("canonicalize W5-X1D %s: %v", kind, err)
	}
	const mediaType = "application/json"
	digest, err := currentstore.ComputeContentDigest(kind, mediaType, canonical)
	if err != nil {
		t.Fatalf("digest W5-X1D %s: %v", kind, err)
	}
	if _, err := store.PutContent(ctx, currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      mediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("put W5-X1D %s: %v", kind, err)
	}
	return digest
}

func cloneW5LiveProfile(
	input controlcontract.ProfileDefinition,
) controlcontract.ProfileDefinition {
	cloned := input
	if input.ModelProfile != nil {
		modelProfile := *input.ModelProfile
		cloned.ModelProfile = &modelProfile
	}
	cloned.Bindings = append([]controlcontract.BindingSpec(nil), input.Bindings...)
	for index := range cloned.Bindings {
		cloned.Bindings[index].StaticContextRefs = append(
			[]string(nil),
			input.Bindings[index].StaticContextRefs...,
		)
	}
	return cloned
}

func w5LiveTransferGrant(
	grantID string,
	workspace corecontract.WorkspaceRef,
	peer corecontract.WorkspaceRef,
	root bool,
) corecontract.WorkspaceTransferGrantV1 {
	grant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       grantID,
		TenantID:      defaultTenantID,
		Workspace:     workspace,
		PeerWorkspace: peer,
		Revision:      1,
		Enabled:       true,
	}
	if root {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
		return grant
	}
	grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
	}
	grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
	}
	grant.MaxSendPayloadBytes = corecontract.WorkspaceTransferMaximumPayloadBytesV1
	grant.MaxReceivePayloadBytes =
		corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
	return grant
}

func loadW5LiveRun(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	runID string,
) currentstore.RunForLoop {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID: runID, OwnerID: "w5-x1d-live-reader", TTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("acquire W5-X1D read lease for %q: %v", runID, err)
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf(
			"load/release W5-X1D Run %q: load=%v release=%v",
			runID,
			loadErr,
			releaseErr,
		)
	}
	return run
}

func loadW5LiveCrossWorkspaceChild(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	result localchat.CompositeChatResult,
	targetWorkspaceID string,
) currentstore.RunForLoop {
	t.Helper()
	for _, child := range result.Children {
		run := loadW5LiveRun(t, ctx, store, child.RunID)
		if run.Member.Workspace.ID == targetWorkspaceID {
			if run.WorkspaceTransfer == nil {
				t.Fatalf("cross-Workspace Child %q lacks transfer material", run.RunID)
			}
			return run
		}
	}
	t.Fatalf("no Child executed in target Workspace %q", targetWorkspaceID)
	return currentstore.RunForLoop{}
}

func assertW5LiveRequestTransfer(
	t *testing.T,
	child currentstore.RunForLoop,
) *currentstore.WorkspaceTransferRecordV1 {
	t.Helper()
	if _, found := child.FindContent(child.Manifest.TaskInputRef); found {
		t.Fatal("cross-Workspace Child exposes root TASK_INPUT in generic Contents")
	}
	record, err := currentstore.PrepareWorkspaceTransferRequestV1(child)
	if err != nil || record == nil {
		t.Fatalf(
			"prepare live REQUEST transfer failed: record_present=%t error_present=%t",
			record != nil,
			err != nil,
		)
	}
	summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		record.Payload.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore live trusted TaskSummary: %v", err)
	}
	compilation := restoreW5LiveCompilation(t, child)
	if len(compilation.WorkspaceTransfers) != 1 {
		t.Fatalf(
			"live REQUEST transfer evidence count=%d want 1",
			len(compilation.WorkspaceTransfers),
		)
	}
	evidence := compilation.WorkspaceTransfers[0]
	if evidence.Direction != corecontract.WorkspaceTransferDirectionRequestV1 ||
		evidence.PayloadKind != corecontract.WorkspaceTransferPayloadTaskSummaryV1 ||
		evidence.EnvelopeRef != record.EnvelopeRef ||
		evidence.EnvelopeDigest != record.EnvelopeDigest ||
		evidence.PayloadRef != record.Payload.Digest ||
		evidence.RootRunID != record.RootManifest.RunID ||
		evidence.ChildRunID != child.RunID ||
		evidence.SlotID != child.Manifest.Composite.Assignment.SlotID ||
		record.Envelope.PayloadRef != record.Payload.Digest {
		t.Fatal("live REQUEST transfer evidence does not close to its frozen envelope")
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		child.ModelDispatches[0].Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore live cross-Workspace model request: %v", err)
	}
	lastUser := ""
	for _, message := range request.Messages {
		if message.Role == moduleapi.ModelRoleUser {
			lastUser = message.Content
		}
	}
	if lastUser != summary.Summary {
		t.Fatalf("cross-Workspace final USER is not the trusted TaskSummary")
	}
	return record
}

func assertW5LiveSuccessfulResultOrder(
	t *testing.T,
	crossChild currentstore.RunForLoop,
	reviewer currentstore.RunForLoop,
	root currentstore.RunForLoop,
) string {
	t.Helper()
	if root.Manifest.Composite == nil || root.Manifest.Composite.Plan == nil ||
		root.Manifest.Composite.Plan.Decision == nil ||
		len(root.Manifest.Composite.Plan.Children) != 2 {
		t.Fatal("live Root does not carry the frozen two-Specialist decision plan")
	}
	plan := root.Manifest.Composite.Plan
	if plan.Children[0].SlotID != "slot-architecture" ||
		plan.Children[1].SlotID != "slot-implementation" {
		t.Fatal("live Root Specialist plan order changed")
	}
	crossIndex := -1
	transferPlans := 0
	for index, child := range plan.Children {
		if child.Transfer != nil {
			transferPlans++
		}
		if child.RunID == crossChild.RunID {
			crossIndex = index
		}
	}
	if crossIndex < 0 || transferPlans != 1 ||
		plan.Children[crossIndex].Transfer == nil {
		t.Fatal("live Root plan does not identify exactly one transferred Specialist")
	}

	reviewerTransfer := assertW5LiveOrderedResultConsumer(
		t,
		reviewer,
		plan.Children,
		corecontract.CompositeRunRoleReviewerV1,
		crossIndex,
	)
	rootTransfer := assertW5LiveOrderedResultConsumer(
		t,
		root,
		plan.Children,
		corecontract.CompositeRunRoleRootV1,
		crossIndex,
	)
	for index := range plan.Children {
		reviewerResult := reviewer.CompositeChildren[index]
		rootResult := root.CompositeChildren[index]
		if reviewerResult.RunID != rootResult.RunID ||
			reviewerResult.ResultRef != rootResult.ResultRef ||
			reviewerResult.ContributionDigest != rootResult.ContributionDigest {
			t.Fatalf(
				"Reviewer/Root successful result identity differs at frozen index %d",
				index,
			)
		}
	}
	if reviewerTransfer.EnvelopeRef != rootTransfer.EnvelopeRef ||
		reviewerTransfer.EnvelopeDigest != rootTransfer.EnvelopeDigest ||
		reviewerTransfer.Payload.Digest != rootTransfer.Payload.Digest {
		t.Fatal("Reviewer and Root did not consume the same frozen RESULT envelope")
	}
	if len(crossChild.ModelDispatches) != 1 ||
		crossChild.ModelDispatches[0].Attempt.State !=
			corecontract.ModelAttemptSucceeded ||
		crossChild.ModelDispatches[0].Attempt.ResultRef !=
			reviewerTransfer.Payload.Digest {
		t.Fatal("transferred RESULT does not close to the cross-Workspace Child Attempt")
	}
	if root.CompositeReviewer == nil ||
		root.CompositeReviewer.State != currentstore.CompositeReviewerSucceededV1 ||
		root.CompositeReviewer.CollaborationVerdict == nil ||
		root.CompositeReviewer.CollaborationVerdict.Decision !=
			corecontract.CollaborationReviewDecisionApproveV1 ||
		len(reviewer.ModelDispatches) != 1 ||
		root.CompositeReviewer.ResultRef !=
			reviewer.ModelDispatches[0].Attempt.ResultRef {
		t.Fatal("Root merge is not bound to the successful APPROVE Reviewer result")
	}
	assertW5LiveRootReviewVerdictInput(t, reviewer, root)
	return rootTransfer.EnvelopeRef
}

func assertW5LiveRootReviewVerdictInput(
	t *testing.T,
	reviewer currentstore.RunForLoop,
	root currentstore.RunForLoop,
) {
	t.Helper()
	if len(reviewer.ModelDispatches) != 1 || len(root.ModelDispatches) != 1 ||
		root.CompositeReviewer == nil ||
		root.CompositeReviewer.CollaborationVerdict == nil {
		t.Fatal("Root ReviewVerdict input lacks one Reviewer and Root Attempt")
	}
	reviewerAttempt := reviewer.ModelDispatches[0].Attempt
	rootAttempt := root.ModelDispatches[0].Attempt
	persisted := root.CompositeReviewer
	compilation := restoreW5LiveCompilation(t, root)
	if compilation.Composite == nil ||
		compilation.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		compilation.Composite.ReviewVerdict == nil {
		t.Fatal("Root compilation lacks ReviewVerdict input evidence")
	}
	evidence := compilation.Composite.ReviewVerdict
	if persisted.RunID != reviewer.RunID ||
		persisted.ManifestDigest != reviewer.Manifest.ManifestDigest ||
		persisted.MemberSnapshotDigest != reviewer.Member.MemberSnapshotDigest ||
		persisted.AttemptID != reviewerAttempt.AttemptID ||
		persisted.ResultRef != reviewerAttempt.ResultRef ||
		evidence.ReviewerRunID != persisted.RunID ||
		evidence.ReviewerManifestDigest != persisted.ManifestDigest ||
		evidence.MemberSnapshotDigest != persisted.MemberSnapshotDigest ||
		evidence.AttemptID != persisted.AttemptID ||
		evidence.LogicalStepID != reviewerAttempt.LogicalStepID ||
		evidence.ResultRef != persisted.ResultRef ||
		evidence.TerminalRunRevision != persisted.RunRevision ||
		evidence.TerminalFrameRevision != persisted.FrameRevision ||
		evidence.SpecialistResultDigest != persisted.ContributionSetDigest ||
		compilation.Composite.SpecialistResultDigest !=
			persisted.ContributionSetDigest ||
		evidence.Decision != corecontract.ReviewDecisionApproveV1 ||
		compilation.FinalRequestDigest != rootAttempt.Request.Digest {
		t.Fatal("Root ReviewVerdict evidence does not bind the persisted Reviewer result and final request")
	}

	modelVerdict, err := corecontract.CanonicalCollaborationReviewModelVerdictV1(
		*persisted.CollaborationVerdict,
	)
	if err != nil {
		t.Fatalf("canonicalize Root ReviewVerdict model input: %v", err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		rootAttempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore Root model request for ReviewVerdict binding: %v", err)
	}
	const verdictPrefix = "UNTRUSTED_COLLABORATION_REVIEW_VERDICT_JSON:\n"
	want := verdictPrefix + string(modelVerdict)
	verdictMessages := 0
	for _, message := range request.Messages {
		if !strings.HasPrefix(message.Content, verdictPrefix) {
			continue
		}
		verdictMessages++
		if message.Role != moduleapi.ModelRoleUser || message.Content != want {
			t.Fatal("Root model request carries a ReviewVerdict other than the bound Reviewer result")
		}
	}
	if verdictMessages != 1 {
		t.Fatalf(
			"Root model request ReviewVerdict message count=%d want 1",
			verdictMessages,
		)
	}
}

func assertW5LiveOrderedResultConsumer(
	t *testing.T,
	run currentstore.RunForLoop,
	planned []corecontract.CompositeChildRunRefV1,
	wantRole corecontract.CompositeRunRoleV1,
	crossIndex int,
) *currentstore.WorkspaceTransferRecordV1 {
	t.Helper()
	if len(run.CompositeChildren) != len(planned) {
		t.Fatalf(
			"Run %q successful result count=%d want %d",
			run.RunID,
			len(run.CompositeChildren),
			len(planned),
		)
	}
	compilation := restoreW5LiveCompilation(t, run)
	if compilation.Composite == nil || compilation.Composite.Role != wantRole ||
		len(compilation.Composite.ChildResults) != len(planned) {
		t.Fatalf("Run %q lacks the expected ordered Composite result evidence", run.RunID)
	}
	for index, childPlan := range planned {
		result := run.CompositeChildren[index]
		evidence := compilation.Composite.ChildResults[index]
		if result.State != currentstore.CompositeChildSucceededV1 ||
			result.RunID != childPlan.RunID || result.SlotID != childPlan.SlotID ||
			result.Assignment != childPlan.Assignment || result.ResultRef == "" ||
			result.Contribution == nil ||
			!moduleapi.ValidSHA256(result.ContributionDigest) ||
			evidence.RunID != result.RunID ||
			evidence.ChildManifestDigest != result.ManifestDigest ||
			evidence.MemberSnapshotDigest != result.MemberSnapshotDigest ||
			evidence.ResultRef != result.ResultRef ||
			evidence.Assignment != result.Assignment {
			t.Fatalf(
				"Run %q successful result at frozen index %d is not plan-ordered",
				run.RunID,
				index,
			)
		}
		if index == crossIndex {
			if result.WorkspaceTransfer == nil ||
				result.WorkspaceTransfer.Envelope.Direction !=
					corecontract.WorkspaceTransferDirectionResultV1 ||
				result.WorkspaceTransfer.Envelope.PayloadKind !=
					corecontract.WorkspaceTransferPayloadSpecialistResultV1 ||
				result.WorkspaceTransfer.Envelope.PayloadRef != result.ResultRef ||
				result.WorkspaceTransfer.Payload.Digest != result.ResultRef ||
				result.WorkspaceTransfer.RootManifest.RunID !=
					run.Manifest.Composite.RootRunID ||
				result.WorkspaceTransfer.ChildManifest.RunID != result.RunID {
				t.Fatalf("Run %q transferred result is not the frozen Child result", run.RunID)
			}
		} else if result.WorkspaceTransfer != nil {
			t.Fatalf("Run %q attached a transfer to a same-Workspace result", run.RunID)
		}
	}
	if len(compilation.WorkspaceTransfers) != 1 {
		t.Fatalf(
			"Run %q RESULT transfer evidence count=%d want 1",
			run.RunID,
			len(compilation.WorkspaceTransfers),
		)
	}
	transfer := run.CompositeChildren[crossIndex].WorkspaceTransfer
	evidence := compilation.WorkspaceTransfers[0]
	if transfer == nil ||
		evidence.Direction != corecontract.WorkspaceTransferDirectionResultV1 ||
		evidence.PayloadKind !=
			corecontract.WorkspaceTransferPayloadSpecialistResultV1 ||
		evidence.EnvelopeRef != transfer.EnvelopeRef ||
		evidence.EnvelopeDigest != transfer.EnvelopeDigest ||
		evidence.PayloadRef != transfer.Payload.Digest ||
		evidence.RootRunID != transfer.RootManifest.RunID ||
		evidence.ChildRunID != transfer.ChildManifest.RunID ||
		evidence.SlotID != planned[crossIndex].SlotID {
		t.Fatalf("Run %q RESULT transfer evidence is not envelope-correlated", run.RunID)
	}
	return transfer
}

func restoreW5LiveCompilation(
	t *testing.T,
	run currentstore.RunForLoop,
) corecontract.ContextCompilationV1 {
	t.Helper()
	if len(run.ModelDispatches) != 1 ||
		run.ModelDispatches[0].Attempt.ContextCompilation == nil {
		t.Fatalf("Run %q lacks one ContextCompilation", run.RunID)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		run.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore Run %q ContextCompilation: %v", run.RunID, err)
	}
	return compilation
}

type w5LiveUsageSummary struct {
	Attempts                   uint32 `json:"attempts"`
	Succeeded                  uint32 `json:"succeeded"`
	InputTokens                uint64 `json:"input_tokens"`
	CachedInputTokens          uint64 `json:"cached_input_tokens"`
	UncachedInputTokens        uint64 `json:"uncached_input_tokens"`
	OutputTokens               uint64 `json:"output_tokens"`
	ReasoningTokensStatus      string `json:"reasoning_tokens_status"`
	ReasoningTokens            uint64 `json:"reasoning_tokens,omitempty"`
	RequestCacheHits           uint32 `json:"request_cache_hits"`
	TokenWeightedCacheHitRate  string `json:"token_weighted_cache_hit_rate"`
	EstimatedCostStatus        string `json:"estimated_cost_status"`
	EstimatedCostCNY           string `json:"estimated_cost_cny,omitempty"`
	EstimatedCostCurrency      string `json:"estimated_cost_currency,omitempty"`
	EstimatedCostFormula       string `json:"estimated_cost_formula,omitempty"`
	PriceSnapshotID            string `json:"price_snapshot_id"`
	PriceSnapshotDigest        string `json:"price_snapshot_digest"`
	ProviderReportedCostStatus string `json:"provider_reported_cost_status"`
	ReconciledCostStatus       string `json:"reconciled_cost_status"`
}

func summarizeW5LiveUsage(
	t *testing.T,
	projection currentstore.CompositeFamilyUsageProjectionV1,
	price corecontract.ModelPriceSnapshotV1,
) w5LiveUsageSummary {
	t.Helper()
	if price.PriceSnapshotID != "price-deepseek-v4-flash-2026-08-04" ||
		price.Provider != "deepseek" || price.Model != "deepseek-v4-flash" ||
		price.BillingVersion != "deepseek-public-price-2026-08-04" ||
		price.Currency != "CNY" ||
		price.PricingStatus != corecontract.PricingKnown ||
		!moduleapi.ValidSHA256(price.Digest) {
		t.Fatal("W5-X1D frozen PriceSnapshot identity or pricing status differs")
	}
	expectedRuns := []struct {
		role       corecontract.CompositeRunRoleV1
		slot       string
		hasAttempt bool
	}{
		{corecontract.CompositeRunRoleChildV1, "slot-architecture", true},
		{corecontract.CompositeRunRoleChildV1, "slot-implementation", true},
		{corecontract.CompositeRunRoleReviewerV1, corecontract.CompositeReviewerParentSlotIDV1, true},
		{corecontract.CompositeRunRoleChildV1, "slot-architecture", false},
		{corecontract.CompositeRunRoleChildV1, "slot-implementation", false},
		{corecontract.CompositeRunRoleReviewerV1, corecontract.CompositeReviewerParentSlotIDV1, false},
		{corecontract.CompositeRunRoleRootV1, "", true},
	}
	if projection.FamilyModelDispatchLimit != 7 ||
		len(projection.Runs) != len(expectedRuns) {
		t.Fatalf(
			"W5-X1D Usage family shape limit=%d projected_runs=%d want limit 7 and 7 physical Runs",
			projection.FamilyModelDispatchLimit,
			len(projection.Runs),
		)
	}
	aggregate := projection.Aggregate
	if aggregate.TokenTotals.Input == nil ||
		aggregate.TokenTotals.CachedInput == nil ||
		aggregate.TokenTotals.UncachedInput == nil ||
		aggregate.TokenTotals.Output == nil {
		t.Fatal("W5-X1D required aggregate Usage tokens are UNKNOWN")
	}
	if *aggregate.TokenTotals.Input == 0 || *aggregate.TokenTotals.Output == 0 ||
		*aggregate.TokenTotals.Input != *aggregate.TokenTotals.CachedInput+
			*aggregate.TokenTotals.UncachedInput {
		t.Fatal("W5-X1D aggregate token totals are zero or fail cache reconciliation")
	}
	if aggregate.TokenTotals.Reasoning != nil &&
		*aggregate.TokenTotals.Reasoning > *aggregate.TokenTotals.Output {
		t.Fatal("W5-X1D aggregate reasoning tokens exceed output tokens")
	}
	aggregateEstimate, err := deepseekcost.Calculate(
		price,
		corecontract.UsageTokens{
			Input:         aggregate.TokenTotals.Input,
			CachedInput:   aggregate.TokenTotals.CachedInput,
			UncachedInput: aggregate.TokenTotals.UncachedInput,
			Output:        aggregate.TokenTotals.Output,
			Reasoning:     aggregate.TokenTotals.Reasoning,
		},
	)
	if err != nil || aggregateEstimate.Status != deepseekcost.StatusKnown ||
		aggregateEstimate.Value == nil || *aggregateEstimate.Value == "0" ||
		aggregate.EstimatedCost.Status != currentstore.CompositeFamilyCostKnownV1 ||
		aggregate.EstimatedCost.Value == nil ||
		*aggregate.EstimatedCost.Value != *aggregateEstimate.Value ||
		aggregate.EstimatedCost.Currency != price.Currency ||
		len(aggregate.EstimatedCost.Currencies) != 0 {
		t.Fatal("W5-X1D aggregate estimated cost does not match frozen token pricing")
	}
	if aggregate.ProviderReportedCost.Status !=
		currentstore.CompositeFamilyCostUnknownV1 ||
		aggregate.ProviderReportedCost.Value != nil ||
		aggregate.ProviderReportedCost.Currency != "" ||
		len(aggregate.ProviderReportedCost.Currencies) != 0 ||
		aggregate.ReconciledCost.Status != currentstore.CompositeFamilyCostUnknownV1 ||
		aggregate.ReconciledCost.Value != nil ||
		aggregate.ReconciledCost.Currency != "" ||
		len(aggregate.ReconciledCost.Currencies) != 0 {
		t.Fatal("W5-X1D absent provider/reconciled costs were not preserved as UNKNOWN")
	}
	const expectedAttempts = uint32(4)
	if aggregate.AttemptSlotsUsed != expectedAttempts {
		t.Fatalf(
			"W5-X1D aggregate Attempt count=%d want independently observed %d",
			aggregate.AttemptSlotsUsed,
			expectedAttempts,
		)
	}
	result := w5LiveUsageSummary{
		Attempts:                   aggregate.AttemptSlotsUsed,
		InputTokens:                *aggregate.TokenTotals.Input,
		CachedInputTokens:          *aggregate.TokenTotals.CachedInput,
		UncachedInputTokens:        *aggregate.TokenTotals.UncachedInput,
		OutputTokens:               *aggregate.TokenTotals.Output,
		EstimatedCostStatus:        string(aggregate.EstimatedCost.Status),
		EstimatedCostCNY:           *aggregate.EstimatedCost.Value,
		EstimatedCostCurrency:      aggregate.EstimatedCost.Currency,
		EstimatedCostFormula:       deepseekcost.EstimateFormulaV1,
		PriceSnapshotID:            price.PriceSnapshotID,
		PriceSnapshotDigest:        price.Digest,
		ProviderReportedCostStatus: string(aggregate.ProviderReportedCost.Status),
		ReconciledCostStatus:       string(aggregate.ReconciledCost.Status),
	}
	if aggregate.TokenTotals.Reasoning == nil {
		result.ReasoningTokensStatus = "UNKNOWN"
	} else {
		result.ReasoningTokensStatus = "KNOWN"
		result.ReasoningTokens = *aggregate.TokenTotals.Reasoning
	}
	var independentlySummed struct {
		input         uint64
		cachedInput   uint64
		uncachedInput uint64
		output        uint64
		reasoning     uint64
	}
	reasoningKnown := true
	independentEstimatedCost := new(big.Rat)
	addUsageTokenCount := func(field string, total *uint64, value uint64) {
		if ^uint64(0)-*total < value {
			t.Fatalf("W5-X1D independent %s token sum overflowed", field)
		}
		*total += value
	}
	for index, run := range projection.Runs {
		expectedRun := expectedRuns[index]
		if run.Role != expectedRun.role || run.SlotID != expectedRun.slot {
			t.Fatalf("W5-X1D Usage Run at frozen index %d has the wrong role or slot", index)
		}
		if !expectedRun.hasAttempt {
			if run.Attempt != nil {
				t.Fatalf("W5-X1D dormant repair Run at frozen index %d unexpectedly has an Attempt", index)
			}
			continue
		}
		if run.Attempt == nil {
			t.Fatalf("W5-X1D active Run at frozen index %d has no Attempt", index)
		}
		attempt := run.Attempt
		tokens := attempt.Usage.Tokens
		if attempt.State != corecontract.ModelAttemptSucceeded ||
			attempt.Provider != "deepseek" || attempt.Model != "deepseek-v4-flash" ||
			attempt.PriceSnapshotID != price.PriceSnapshotID ||
			attempt.PriceSnapshotDigest != price.Digest ||
			attempt.Currency != price.Currency ||
			!moduleapi.ValidSHA256(attempt.RequestDigest) ||
			tokens.Input == nil || tokens.CachedInput == nil ||
			tokens.UncachedInput == nil || tokens.Output == nil ||
			*tokens.Input == 0 || *tokens.Output == 0 ||
			*tokens.Input != *tokens.CachedInput+*tokens.UncachedInput ||
			(tokens.Reasoning != nil && *tokens.Reasoning > *tokens.Output) ||
			attempt.Usage.RawReceiptRef == "" ||
			!moduleapi.ValidSHA256(attempt.Usage.RawReceiptRef) ||
			attempt.Usage.ReconciliationStatus != "PROVIDER_REPORTED" ||
			attempt.Usage.ProviderReportedCost != nil ||
			attempt.Usage.ReconciledCost != nil {
			t.Fatalf("W5-X1D Attempt at frozen index %d has an invalid Usage closure", index)
		}
		expected, estimateErr := deepseekcost.Calculate(price, tokens)
		if estimateErr != nil || expected.Status != deepseekcost.StatusKnown ||
			expected.Value == nil || attempt.Usage.EstimatedCost == nil ||
			*attempt.Usage.EstimatedCost != *expected.Value {
			t.Fatalf("W5-X1D Attempt at frozen index %d has the wrong estimated cost", index)
		}
		attemptCost, ok := new(big.Rat).SetString(*attempt.Usage.EstimatedCost)
		if !ok {
			t.Fatalf(
				"W5-X1D Attempt at frozen index %d has a non-numeric estimated cost",
				index,
			)
		}
		independentEstimatedCost.Add(independentEstimatedCost, attemptCost)
		addUsageTokenCount("input", &independentlySummed.input, *tokens.Input)
		addUsageTokenCount("cached input", &independentlySummed.cachedInput, *tokens.CachedInput)
		addUsageTokenCount(
			"uncached input",
			&independentlySummed.uncachedInput,
			*tokens.UncachedInput,
		)
		addUsageTokenCount("output", &independentlySummed.output, *tokens.Output)
		if tokens.Reasoning == nil {
			reasoningKnown = false
		} else {
			addUsageTokenCount("reasoning", &independentlySummed.reasoning, *tokens.Reasoning)
		}
		result.Succeeded++
		if *tokens.CachedInput > 0 {
			result.RequestCacheHits++
		}
	}
	if independentlySummed.input != *aggregate.TokenTotals.Input ||
		independentlySummed.cachedInput != *aggregate.TokenTotals.CachedInput ||
		independentlySummed.uncachedInput != *aggregate.TokenTotals.UncachedInput ||
		independentlySummed.output != *aggregate.TokenTotals.Output {
		t.Fatal("W5-X1D aggregate tokens do not equal the four independently summed Attempts")
	}
	if reasoningKnown {
		if aggregate.TokenTotals.Reasoning == nil ||
			independentlySummed.reasoning != *aggregate.TokenTotals.Reasoning {
			t.Fatal("W5-X1D known aggregate reasoning does not equal the four Attempt sum")
		}
	} else if aggregate.TokenTotals.Reasoning != nil {
		t.Fatal("W5-X1D aggregate reasoning became KNOWN when an Attempt was UNKNOWN")
	}
	aggregateEstimatedCost, ok := new(big.Rat).SetString(
		*aggregate.EstimatedCost.Value,
	)
	if !ok || independentEstimatedCost.Cmp(aggregateEstimatedCost) != 0 {
		t.Fatal("W5-X1D aggregate estimated cost does not equal the four Attempt sum")
	}
	if result.InputTokens == 0 {
		result.TokenWeightedCacheHitRate = "0.000000%"
	} else {
		result.TokenWeightedCacheHitRate = fmt.Sprintf(
			"%.6f%%",
			100*float64(result.CachedInputTokens)/float64(result.InputTokens),
		)
	}
	return result
}

type w5LiveAttemptIdentity struct {
	RunID         string
	Role          corecontract.CompositeRunRoleV1
	SlotID        string
	AttemptID     string
	RequestDigest string
}

func w5LiveAttemptIdentities(
	projection currentstore.CompositeFamilyUsageProjectionV1,
) []w5LiveAttemptIdentity {
	identities := make([]w5LiveAttemptIdentity, len(projection.Runs))
	for index, run := range projection.Runs {
		identities[index] = w5LiveAttemptIdentity{
			RunID:  run.RunID,
			Role:   run.Role,
			SlotID: run.SlotID,
		}
		if run.Attempt != nil {
			identities[index].AttemptID = run.Attempt.AttemptID
			identities[index].RequestDigest = run.Attempt.RequestDigest
		}
	}
	return identities
}

func sameW5LiveAttemptIdentities(
	left []w5LiveAttemptIdentity,
	right []w5LiveAttemptIdentity,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
