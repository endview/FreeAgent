package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeChatServiceRunsAtomicParallelFamilyAndReusesExactRetry(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixture(t, false)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := fixture.base.input(
		"composite-stable-request",
		"combine two independent views",
		deadline,
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if !first.AdmissionCreated ||
		first.RequestID != input.RequestID ||
		!first.Deadline.Equal(deadline) ||
		first.RootRunID == "" ||
		len(first.Children) != 2 ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		first.Reply != "result for "+first.RootRunID ||
		first.FailureCode != "" {
		t.Fatalf("first Chat result=%+v", first)
	}
	assertCompositeAdmissionObserved(t, fixture.observer)
	if !fixture.invoker.parallelChildrenObserved() {
		t.Fatal("the two Child model invocations did not overlap")
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("first Chat model calls=%d want 3", got)
	}
	assertSucceededCompositeChatFamily(t, fixture, first)
	rootBeforeRetry := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	if len(rootBeforeRetry.ModelDispatches) != 1 ||
		rootBeforeRetry.ModelDispatches[0].Attempt.ContextCompilation == nil {
		t.Fatalf("root before retry lacks exact model closure: %+v", rootBeforeRetry)
	}
	requestBeforeRetry := append(
		[]byte(nil),
		rootBeforeRetry.ModelDispatches[0].Attempt.Request.CanonicalBytes...,
	)
	compilationBeforeRetry := append(
		[]byte(nil),
		rootBeforeRetry.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes...,
	)

	second, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Chat: %v", err)
	}
	if second.AdmissionCreated ||
		second.RequestID != first.RequestID ||
		!second.Deadline.Equal(first.Deadline) ||
		second.RootRunID != first.RootRunID ||
		second.Reply != first.Reply ||
		second.LoopResult.Disposition != loopapi.DispositionTerminated ||
		len(second.Children) != len(first.Children) {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	for index := range first.Children {
		if second.Children[index].RunID != first.Children[index].RunID {
			t.Fatalf(
				"retry Child %d RunID=%q want %q",
				index,
				second.Children[index].RunID,
				first.Children[index].RunID,
			)
		}
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("retry added model calls=%d want 3", got)
	}
	assertSucceededCompositeChatFamily(t, fixture, second)
	for retryIndex := 0; retryIndex < 2; retryIndex++ {
		retried, retryErr := fixture.service.Chat(context.Background(), input)
		if retryErr != nil || retried.Reply != first.Reply ||
			retried.LoopResult.Disposition != loopapi.DispositionTerminated {
			t.Fatalf("terminal retry %d=%+v error=%v", retryIndex+2, retried, retryErr)
		}
	}
	rootAfterRetries := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	if len(rootAfterRetries.ModelDispatches) != 1 ||
		rootAfterRetries.ModelDispatches[0].Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			rootAfterRetries.ModelDispatches[0].Attempt.Request.CanonicalBytes,
			requestBeforeRetry,
		) ||
		!bytes.Equal(
			rootAfterRetries.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes,
			compilationBeforeRetry,
		) {
		t.Fatal("terminal Child re-entry changed the frozen root request or compilation")
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("repeated terminal retries added model calls=%d want 3", got)
	}
}

func TestCompositeChatServiceUnknownChildDoesNotMergeRoot(t *testing.T) {
	fixture := newCompositeChatServiceFixture(t, true)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := fixture.base.input(
		"composite-unknown-request",
		"leave uncertainty explicit",
		deadline,
	)

	result, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !result.AdmissionCreated || result.RootRunID == "" ||
		len(result.Children) != 2 ||
		result.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		result.LoopResult.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
		result.TerminalResult != nil || result.Reply != "" ||
		result.FailureCode != "" {
		t.Fatalf("Chat result=%+v", result)
	}
	assertCompositeAdmissionObserved(t, fixture.observer)
	if !fixture.invoker.parallelChildrenObserved() {
		t.Fatal("the two Child model invocations did not overlap")
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("model calls=%d want two Child calls and no root call", got)
	}

	root := loadCompositeChatRun(t, fixture.base.store, result.RootRunID)
	if len(root.ModelDispatches) != 0 || len(root.History) != 0 {
		t.Fatalf(
			"root persisted model dispatches=%d History=%d want 0/0",
			len(root.ModelDispatches),
			len(root.History),
		)
	}
	unknownAttempts := 0
	succeededAttempts := 0
	childUnknownResults := 0
	childTerminalResults := 0
	for index, childResult := range result.Children {
		child := loadCompositeChatRun(
			t,
			fixture.base.store,
			childResult.RunID,
		)
		if len(child.ModelDispatches) != 1 {
			t.Fatalf(
				"Child %d model dispatches=%d want 1",
				index,
				len(child.ModelDispatches),
			)
		}
		attempt := child.ModelDispatches[0].Attempt
		if attempt.LogicalStepID != corecontract.PureChatModelLogicalStepIDV1 {
			t.Fatalf("Child %d logical step=%q", index, attempt.LogicalStepID)
		}
		switch attempt.State {
		case corecontract.ModelAttemptUnknown:
			unknownAttempts++
		case corecontract.ModelAttemptSucceeded:
			succeededAttempts++
		default:
			t.Fatalf("Child %d Attempt state=%q", index, attempt.State)
		}
		switch childResult.LoopResult.Disposition {
		case loopapi.DispositionWaitingReconciliation:
			childUnknownResults++
		case loopapi.DispositionTerminated:
			childTerminalResults++
		default:
			t.Fatalf("Child %d result=%+v", index, childResult.LoopResult)
		}
	}
	if unknownAttempts != 1 || succeededAttempts != 1 ||
		childUnknownResults != 1 || childTerminalResults != 1 {
		t.Fatalf(
			"attempt states unknown/succeeded=%d/%d Child results=%d/%d",
			unknownAttempts,
			succeededAttempts,
			childUnknownResults,
			childTerminalResults,
		)
	}
}

type compositeChatServiceFixture struct {
	base     *chatServiceFixture
	service  *CompositeChatService
	invoker  *parallelCompositeEchoInvoker
	observer *compositeAdmissionObservingLoop
}

type compositeReviewerTestOutcome string

const (
	compositeReviewerDisabledTest compositeReviewerTestOutcome = ""
	compositeReviewerApproveTest  compositeReviewerTestOutcome = "APPROVE"
	compositeReviewerRejectTest   compositeReviewerTestOutcome = "REJECT"
	compositeReviewerInvalidTest  compositeReviewerTestOutcome = "INVALID"
	compositeReviewerUnknownTest  compositeReviewerTestOutcome = "UNKNOWN"
)

func newCompositeChatServiceFixture(
	t *testing.T,
	unknownFirstChild bool,
) *compositeChatServiceFixture {
	return newCompositeChatServiceFixtureWithReviewer(
		t,
		unknownFirstChild,
		compositeReviewerDisabledTest,
	)
}

func newCompositeChatServiceFixtureWithReviewer(
	t *testing.T,
	unknownFirstChild bool,
	reviewerOutcome compositeReviewerTestOutcome,
) *compositeChatServiceFixture {
	t.Helper()
	ctx := context.Background()
	base := newChatServiceFixture(t)
	basis, control, catalog, err := base.store.LoadPublishedBasis(
		ctx,
		base.tenantID,
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	if len(control.Agents) != 1 || len(control.Profiles) != 1 ||
		len(control.Workspaces) != 1 || len(catalog.Entries) != 1 {
		t.Fatalf(
			"base Control/Catalog agents=%d profiles=%d workspaces=%d entries=%d",
			len(control.Agents),
			len(control.Profiles),
			len(control.Workspaces),
			len(catalog.Entries),
		)
	}

	analysisAgent := corecontract.AgentRef{
		ID: "agent-chat-analysis", Version: "v1",
		Digest: strings.Repeat("d", 64),
	}
	reviewAgent := corecontract.AgentRef{
		ID: "agent-chat-review", Version: "v1",
		Digest: strings.Repeat("e", 64),
	}
	reviewerAgent := corecontract.AgentRef{
		ID: "agent-chat-reviewer", Version: "v1",
		Digest: strings.Repeat("1", 64),
	}
	analysisProfile := cloneCompositeChatProfile(control.Profiles[0])
	analysisProfile.Profile = corecontract.ProfileRef{
		ID: "profile-chat-analysis", Version: "v1",
		Digest: strings.Repeat("f", 64),
	}
	reviewProfile := cloneCompositeChatProfile(control.Profiles[0])
	reviewProfile.Profile = corecontract.ProfileRef{
		ID: "profile-chat-review", Version: "v1",
		Digest: strings.Repeat("0", 64),
	}
	reviewerProfile := cloneCompositeChatProfile(control.Profiles[0])
	reviewerProfile.Profile = corecontract.ProfileRef{
		ID: "profile-chat-reviewer", Version: "v1",
		Digest: strings.Repeat("2", 64),
	}
	control.SnapshotID = "control-chat-composite"
	control.Revision++
	control.Agents = append(control.Agents, analysisAgent, reviewAgent)
	control.Profiles = append(
		control.Profiles,
		analysisProfile,
		reviewProfile,
	)
	definition := controlcontract.CompositeAgentDefinitionV1{
		SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
		AgentID:              base.agentID,
		CoordinatorProfileID: base.profileID,
		Members: []controlcontract.CompositeAgentMemberV1{
			{
				SlotID:            "slot-analysis",
				AgentID:           analysisAgent.ID,
				ProfileID:         analysisProfile.Profile.ID,
				FocusID:           "analysis",
				WeightBasisPoints: 6000,
			},
			{
				SlotID:            "slot-review",
				AgentID:           reviewAgent.ID,
				ProfileID:         reviewProfile.Profile.ID,
				FocusID:           "review",
				WeightBasisPoints: 4000,
			},
		},
	}
	if reviewerOutcome != compositeReviewerDisabledTest {
		control.Agents = append(control.Agents, reviewerAgent)
		control.Profiles = append(control.Profiles, reviewerProfile)
		definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
			SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
			AgentID:         reviewerAgent.ID,
			ProfileID:       reviewerProfile.Profile.ID,
			MaxOutputTokens: corecontract.CompositeReviewerMaxOutputTokensV1,
			Policy:          corecontract.CompositeReviewerPolicyResultsGateV1,
		}
	}
	control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{
		definition,
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot(composite): %v", err)
	}
	catalog.GenerationID = "catalog-chat-composite"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration(composite): %v", err)
	}
	if _, err := base.store.PublishControlCatalog(
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
		t.Fatalf("PublishControlCatalog(composite): %v", err)
	}

	provider := catalog.Entries[0].Activation
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho: %v", err)
	}
	invoker := &parallelCompositeEchoInvoker{
		provider:          provider,
		echo:              echo,
		unknownFirstChild: unknownFirstChild,
		reviewerOutcome:   reviewerOutcome,
		childrenReady:     make(chan struct{}),
	}
	base.invoker.delegate = invoker
	observer := &compositeAdmissionObservingLoop{
		store:    base.store,
		delegate: base.service.loop,
	}
	service, err := NewCompositeChatService(base.store, observer)
	if err != nil {
		t.Fatalf("NewCompositeChatService: %v", err)
	}
	return &compositeChatServiceFixture{
		base: base, service: service, invoker: invoker, observer: observer,
	}
}

func cloneCompositeChatProfile(
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

type parallelCompositeEchoInvoker struct {
	provider                 moduleapi.ActivatedModuleRef
	echo                     modulehost.ModuleInvoker
	unknownFirstChild        bool
	failFirstChild           bool
	reviewerOutcome          compositeReviewerTestOutcome
	childrenReady            chan struct{}
	readyOnce                sync.Once
	mu                       sync.Mutex
	runIDs                   []string
	activeChildren           int
	maxActiveChildren        int
	reviewerFamilyDigest     string
	reviewerSpecialistDigest string
}

func (invoker *parallelCompositeEchoInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	familyDigest, specialistDigest, affectedSlot, isReviewer, err :=
		parseCompositeReviewerRequest(prepared.Invocation.Input)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	invoker.mu.Lock()
	callIndex := len(invoker.runIDs)
	invoker.runIDs = append(invoker.runIDs, prepared.Invocation.RunID)
	isChild := callIndex < 2
	if isChild {
		invoker.activeChildren++
		if invoker.activeChildren > invoker.maxActiveChildren {
			invoker.maxActiveChildren = invoker.activeChildren
		}
		if callIndex == 1 {
			invoker.readyOnce.Do(func() { close(invoker.childrenReady) })
		}
	}
	unknown := invoker.unknownFirstChild && callIndex == 0
	failed := invoker.failFirstChild && callIndex == 0
	if isReviewer {
		invoker.reviewerFamilyDigest = familyDigest
		invoker.reviewerSpecialistDigest = specialistDigest
	}
	invoker.mu.Unlock()

	if isChild {
		defer func() {
			invoker.mu.Lock()
			invoker.activeChildren--
			invoker.mu.Unlock()
		}()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-invoker.childrenReady:
		case <-ctx.Done():
			return modulehost.InvocationResult{}, ctx.Err()
		case <-timer.C:
			return modulehost.InvocationResult{}, fmt.Errorf(
				"composite Child model invocations were not parallel",
			)
		}
	}
	if unknown {
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationUnknown,
		}, nil
	}
	if failed {
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationFailed,
		}, nil
	}
	if isReviewer {
		if invoker.reviewerOutcome == compositeReviewerUnknownTest {
			return modulehost.InvocationResult{
				Provider: invoker.provider,
				Outcome:  modulehost.InvocationUnknown,
			}, nil
		}
		if invoker.reviewerOutcome == compositeReviewerInvalidTest {
			result, invokeErr := invoker.echo.Invoke(ctx, prepared)
			if invokeErr != nil {
				return modulehost.InvocationResult{}, invokeErr
			}
			_, outputCanonical, freezeErr := moduleapi.NewModelGenerateOutputV1(
				moduleapi.ModelGenerateOutputV1{
					SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
					AssistantText: `{"schema_version":"review-verdict/v1"}`,
				},
			)
			if freezeErr != nil {
				return modulehost.InvocationResult{}, freezeErr
			}
			result.Output = outputCanonical
			return result, nil
		}
		verdict := corecontract.ReviewVerdictV1{
			SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
			FamilyDigest:           familyDigest,
			SpecialistResultDigest: specialistDigest,
			Decision:               corecontract.ReviewDecisionApproveV1,
			BoundedReason:          "specialist results satisfy the frozen review gate",
		}
		if invoker.reviewerOutcome == compositeReviewerRejectTest {
			verdict.Decision = corecontract.ReviewDecisionRejectV1
			verdict.IssueCodes = []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueContradictionV1,
			}
			verdict.AffectedSlotIDs = []string{affectedSlot}
			verdict.BoundedReason = "specialist results contradict each other"
		} else if invoker.reviewerOutcome != compositeReviewerApproveTest {
			return modulehost.InvocationResult{}, fmt.Errorf(
				"unexpected Reviewer call with configured outcome %q",
				invoker.reviewerOutcome,
			)
		}
		_, verdictCanonical, freezeErr := corecontract.NewReviewVerdictV1(verdict)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		_, outputCanonical, freezeErr := moduleapi.NewModelGenerateOutputV1(
			moduleapi.ModelGenerateOutputV1{
				SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
				AssistantText: string(verdictCanonical),
			},
		)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		result, invokeErr := invoker.echo.Invoke(ctx, prepared)
		if invokeErr != nil {
			return modulehost.InvocationResult{}, invokeErr
		}
		result.Output = outputCanonical
		return result, nil
	}
	result, err := invoker.echo.Invoke(ctx, prepared)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	output.AssistantText = "result for " + prepared.Invocation.RunID
	_, result.Output, err = moduleapi.NewModelGenerateOutputV1(output)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return result, nil
}

func parseCompositeReviewerRequest(
	canonical []byte,
) (string, string, string, bool, error) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(canonical)
	if err != nil {
		return "", "", "", false, err
	}
	const (
		policyPrefix = "COMPOSITE_REVIEW_POLICY_JSON:\n"
		resultPrefix = "UNTRUSTED_COMPOSITE_SPECIALIST_RESULT_JSON:\n"
	)
	type policyEnvelope struct {
		SchemaVersion          string   `json:"schema_version"`
		Policy                 string   `json:"policy"`
		FamilyDigest           string   `json:"family_digest"`
		SpecialistResultDigest string   `json:"specialist_result_digest"`
		OutputSchemaVersion    string   `json:"output_schema_version"`
		AllowedDecisions       []string `json:"allowed_decisions"`
		AllowedIssueCodes      []string `json:"allowed_issue_codes"`
		RequiredFields         []string `json:"required_fields"`
		OutputRules            []string `json:"output_rules"`
	}
	type resultEnvelope struct {
		SchemaVersion         string `json:"schema_version"`
		SlotID                string `json:"slot_id"`
		FocusID               string `json:"focus_id"`
		WeightBasisPoints     uint32 `json:"weight_basis_points"`
		RunID                 string `json:"run_id"`
		ManifestDigest        string `json:"manifest_digest"`
		MemberSnapshotDigest  string `json:"member_snapshot_digest"`
		ResultRef             string `json:"result_ref"`
		TerminalRunRevision   uint64 `json:"terminal_run_revision"`
		TerminalFrameRevision uint64 `json:"terminal_frame_revision"`
		Result                string `json:"result"`
	}
	var policy *policyEnvelope
	firstSlot := ""
	for _, message := range request.Messages {
		switch {
		case strings.HasPrefix(message.Content, policyPrefix):
			if policy != nil {
				return "", "", "", false, fmt.Errorf(
					"Reviewer request contains duplicate policy envelopes",
				)
			}
			value := policyEnvelope{}
			if err := decodeExactTestJSON(
				[]byte(strings.TrimPrefix(message.Content, policyPrefix)),
				&value,
			); err != nil {
				return "", "", "", false, err
			}
			policy = &value
		case firstSlot == "" && strings.HasPrefix(message.Content, resultPrefix):
			envelope := resultEnvelope{}
			if err := decodeExactTestJSON(
				[]byte(strings.TrimPrefix(message.Content, resultPrefix)),
				&envelope,
			); err != nil {
				return "", "", "", false, err
			}
			if envelope.SchemaVersion !=
				"composite-review-specialist-result-context/v1" ||
				envelope.SlotID == "" || envelope.FocusID == "" ||
				envelope.WeightBasisPoints == 0 || envelope.RunID == "" ||
				!moduleapi.ValidSHA256(envelope.ManifestDigest) ||
				!moduleapi.ValidSHA256(envelope.MemberSnapshotDigest) ||
				!moduleapi.ValidSHA256(envelope.ResultRef) ||
				envelope.TerminalRunRevision == 0 ||
				envelope.TerminalFrameRevision == 0 || envelope.Result == "" {
				return "", "", "", false, fmt.Errorf(
					"Reviewer Specialist envelope is not exact",
				)
			}
			firstSlot = envelope.SlotID
		}
	}
	if policy == nil {
		return "", "", "", false, nil
	}
	if policy.SchemaVersion != "composite-review-policy-context/v1" ||
		policy.Policy != corecontract.CompositeReviewerPolicyResultsGateV1 ||
		policy.OutputSchemaVersion != corecontract.ReviewVerdictSchemaVersionV1 ||
		len(policy.AllowedDecisions) != 2 ||
		len(policy.AllowedIssueCodes) == 0 ||
		len(policy.RequiredFields) == 0 || len(policy.OutputRules) == 0 ||
		!moduleapi.ValidSHA256(policy.FamilyDigest) ||
		!moduleapi.ValidSHA256(policy.SpecialistResultDigest) ||
		firstSlot == "" {
		return "", "", "", false, fmt.Errorf(
			"Reviewer request has an invalid frozen policy envelope",
		)
	}
	return policy.FamilyDigest, policy.SpecialistResultDigest, firstSlot, true, nil
}

func decodeExactTestJSON(canonical []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains a second value")
		}
		return err
	}
	return nil
}

func (invoker *parallelCompositeEchoInvoker) parallelChildrenObserved() bool {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return len(invoker.runIDs) >= 2 && invoker.maxActiveChildren == 2
}

func (invoker *parallelCompositeEchoInvoker) invocationRunIDs() []string {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return append([]string(nil), invoker.runIDs...)
}

type compositeAdmissionObservingLoop struct {
	store    *currentstore.Store
	delegate loopapi.Loop
	once     sync.Once
	mu       sync.Mutex
	checked  bool
	checkErr error
}

func (loop *compositeAdmissionObservingLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	loop.once.Do(func() {
		err := verifyAtomicCompositeAdmission(ctx, loop.store, input.RunID)
		loop.mu.Lock()
		loop.checked = true
		loop.checkErr = err
		loop.mu.Unlock()
	})
	loop.mu.Lock()
	checkErr := loop.checkErr
	loop.mu.Unlock()
	if checkErr != nil {
		return loopapi.RunResult{}, checkErr
	}
	return loop.delegate.Run(ctx, input)
}

func (loop *compositeAdmissionObservingLoop) checkResult() (bool, error) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.checked, loop.checkErr
}

func verifyAtomicCompositeAdmission(
	ctx context.Context,
	store *currentstore.Store,
	firstRunID string,
) error {
	first, err := readCompositeChatRun(ctx, store, firstRunID, "atomic-child")
	if err != nil {
		return fmt.Errorf("read first admitted Child: %w", err)
	}
	if first.Manifest.Composite == nil ||
		first.Manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 {
		return fmt.Errorf("first Loop Run %q is not a composite Child", firstRunID)
	}
	rootRunID := first.Manifest.Composite.RootRunID
	root, err := readCompositeChatRun(ctx, store, rootRunID, "atomic-root")
	if err != nil {
		return fmt.Errorf("read admitted root: %w", err)
	}
	if root.Manifest.Composite == nil ||
		root.Manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Manifest.Composite.Plan == nil ||
		len(root.Manifest.Composite.Plan.Children) != 2 ||
		root.Frame.Step != corecontract.WaitingChildrenLoopStep {
		return fmt.Errorf("root family closure is incomplete")
	}
	for index, planned := range root.Manifest.Composite.Plan.Children {
		child, err := readCompositeChatRun(
			ctx,
			store,
			planned.RunID,
			fmt.Sprintf("atomic-child-%d", index),
		)
		if err != nil {
			return fmt.Errorf("read admitted Child %d: %w", index, err)
		}
		if child.Manifest.Composite == nil ||
			child.Manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
			child.Manifest.Composite.RootRunID != rootRunID ||
			child.Manifest.Composite.ParentManifestDigest !=
				root.Manifest.ManifestDigest ||
			child.Manifest.ParentRunID != rootRunID ||
			child.Frame.Step != corecontract.InitialLoopStep {
			return fmt.Errorf("Child %d family closure is incomplete", index)
		}
	}
	if planned := root.Manifest.Composite.Plan.Reviewer; planned != nil {
		reviewer, err := readCompositeChatRun(
			ctx,
			store,
			planned.RunID,
			"atomic-reviewer",
		)
		if err != nil {
			return fmt.Errorf("read admitted Reviewer: %w", err)
		}
		if reviewer.Manifest.Composite == nil ||
			reviewer.Manifest.Composite.Role !=
				corecontract.CompositeRunRoleReviewerV1 ||
			reviewer.Manifest.Composite.RootRunID != rootRunID ||
			reviewer.Manifest.Composite.ParentManifestDigest !=
				root.Manifest.ManifestDigest ||
			reviewer.Manifest.Composite.ParentSlotID !=
				corecontract.CompositeReviewerParentSlotIDV1 ||
			reviewer.Manifest.ParentRunID != rootRunID ||
			reviewer.Frame.Step != corecontract.WaitingChildrenLoopStep {
			return fmt.Errorf("Reviewer family closure is incomplete")
		}
	}
	return nil
}

func assertCompositeAdmissionObserved(
	t *testing.T,
	observer *compositeAdmissionObservingLoop,
) {
	t.Helper()
	checked, err := observer.checkResult()
	if !checked || err != nil {
		t.Fatalf("atomic Admission observed=%t error=%v", checked, err)
	}
}

func assertSucceededCompositeChatFamily(
	t *testing.T,
	fixture *compositeChatServiceFixture,
	result CompositeChatResult,
) {
	t.Helper()
	root := loadCompositeChatRun(t, fixture.base.store, result.RootRunID)
	if root.Manifest.Composite == nil ||
		root.Manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Manifest.Composite.Plan == nil ||
		len(root.Manifest.Composite.Plan.Children) != len(result.Children) ||
		len(root.ModelDispatches) != 1 {
		t.Fatalf("root closure=%+v", root)
	}
	rootAttempt := root.ModelDispatches[0].Attempt
	if rootAttempt.LogicalStepID != corecontract.CompositeMergeLogicalStepIDV1 ||
		rootAttempt.State != corecontract.ModelAttemptSucceeded ||
		result.TerminalResult == nil ||
		result.TerminalResult.AttemptID != rootAttempt.AttemptID {
		t.Fatalf("root Attempt=%+v terminal=%+v", rootAttempt, result.TerminalResult)
	}
	if len(root.History) != 1 {
		t.Fatalf("root History entries=%d want 1", len(root.History))
	}
	history := root.History[0]
	if history.MemberID != root.Member.MemberID ||
		history.SourceAttemptID != rootAttempt.AttemptID ||
		history.Content.Kind != currentstore.ContentModelResult ||
		history.Content.Digest != rootAttempt.ResultRef {
		t.Fatalf("root History=%+v root Attempt=%+v", history, rootAttempt)
	}

	totalAttempts := len(root.ModelDispatches)
	for index, childResult := range result.Children {
		planned := root.Manifest.Composite.Plan.Children[index]
		if childResult.RunID != planned.RunID {
			t.Fatalf(
				"Child %d result RunID=%q plan=%q",
				index,
				childResult.RunID,
				planned.RunID,
			)
		}
		child := loadCompositeChatRun(t, fixture.base.store, childResult.RunID)
		if child.Manifest.Composite == nil ||
			child.Manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
			child.Manifest.ParentRunID != result.RootRunID ||
			len(child.ModelDispatches) != 1 {
			t.Fatalf("Child %d closure=%+v", index, child)
		}
		childAttempt := child.ModelDispatches[0].Attempt
		if childAttempt.LogicalStepID !=
			corecontract.PureChatModelLogicalStepIDV1 ||
			childAttempt.State != corecontract.ModelAttemptSucceeded {
			t.Fatalf("Child %d Attempt=%+v", index, childAttempt)
		}
		if history.SourceAttemptID == childAttempt.AttemptID ||
			history.Content.Digest == childAttempt.ResultRef {
			t.Fatalf(
				"root History copied Child %d Attempt/result: history=%+v child=%+v",
				index,
				history,
				childAttempt,
			)
		}
		totalAttempts += len(child.ModelDispatches)
	}
	if totalAttempts != len(result.Children)+1 {
		t.Fatalf(
			"family model Attempts=%d want N+1=%d",
			totalAttempts,
			len(result.Children)+1,
		)
	}
}

func loadCompositeChatRun(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) currentstore.RunForLoop {
	t.Helper()
	run, err := readCompositeChatRun(
		context.Background(),
		store,
		runID,
		"inspect",
	)
	if err != nil {
		t.Fatalf("read Run %s: %v", runID, err)
	}
	return run
}

func readCompositeChatRun(
	ctx context.Context,
	store *currentstore.Store,
	runID string,
	ownerSuffix string,
) (currentstore.RunForLoop, error) {
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "composite-chat-test-" + ownerSuffix,
			TTL:     time.Minute,
		},
	)
	if err != nil {
		return currentstore.RunForLoop{}, err
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	if loadErr != nil {
		return currentstore.RunForLoop{}, loadErr
	}
	if releaseErr != nil {
		return currentstore.RunForLoop{}, releaseErr
	}
	return run, nil
}
