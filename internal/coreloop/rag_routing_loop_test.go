package coreloop

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestApplyDynamicContextMaterialsRejectsIncompleteOrExtraneousShape(t *testing.T) {
	trusted := moduleapi.PortBinding{Provider: moduleapi.ActivatedModuleRef{
		ExecutionClass: moduleapi.ExecutionTrustedInProcess,
	}}
	declarative := moduleapi.PortBinding{Provider: moduleapi.ActivatedModuleRef{
		ExecutionClass: moduleapi.ExecutionDeclarative,
	}}
	tests := []struct {
		name     string
		bindings []moduleapi.PortBinding
		dynamic  map[uint32]dynamicContextMaterialV1
	}{
		{
			name:     "missing trusted Binding material",
			bindings: []moduleapi.PortBinding{trusted, trusted},
			dynamic: map[uint32]dynamicContextMaterialV1{
				0: {},
			},
		},
		{
			name:     "extra out of range Binding key",
			bindings: []moduleapi.PortBinding{trusted},
			dynamic: map[uint32]dynamicContextMaterialV1{
				0: {},
				9: {},
			},
		},
		{
			name:     "declarative Binding carries dynamic material",
			bindings: []moduleapi.PortBinding{declarative},
			dynamic: map[uint32]dynamicContextMaterialV1{
				0: {},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := &moduleapi.PortPlan{Bindings: test.bindings}
			materials := make(
				[]contextcompiler.BindingMaterialV1,
				len(test.bindings),
			)
			if err := applyDynamicContextMaterials(
				plan,
				materials,
				test.dynamic,
			); err == nil || !errors.Is(err, ErrInvalidPureChatRequest) {
				t.Fatalf("invalid dynamic material shape error=%v", err)
			}
		})
	}
}

func TestPrepareChatRequestRoutesKnowledgeBeforeProviderInvocation(t *testing.T) {
	fixture := newRoutingCoreLoopFixture(
		t,
		"design an api gateway",
		[]routingKnowledgeSpec{
			{
				id:   "backend",
				text: "api gateway backend evidence",
				routing: &moduleapi.KnowledgeRoutingPolicyV1{
					SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
					CollectionTags: []string{"backend"},
					MatchTerms:     []string{"api"},
					MinMatchTerms:  1,
				},
			},
			{
				id:   "frontend",
				text: "css frontend evidence must never enter this prompt",
				routing: &moduleapi.KnowledgeRoutingPolicyV1{
					SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
					CollectionTags: []string{"frontend"},
					MatchTerms:     []string{"css"},
					MinMatchTerms:  1,
				},
			},
		},
	)
	prepared, err := fixture.loop.prepareChatRequestV1(
		context.Background(),
		fixture.run,
		fixture.lease,
	)
	if err != nil {
		t.Fatalf("prepare routed Knowledge: %v", err)
	}
	if fixture.invokers[0].callCount() != 1 ||
		fixture.invokers[1].callCount() != 0 {
		t.Fatalf(
			"Knowledge calls selected/skipped=%d/%d, want 1/0",
			fixture.invokers[0].callCount(),
			fixture.invokers[1].callCount(),
		)
	}
	if fixture.current.calls != 1 {
		t.Fatalf("current Activation calls=%d, want selected Provider only", fixture.current.calls)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		prepared.ContextCompilationCanonical,
	)
	if err != nil {
		t.Fatalf("restore routed compilation: %v", err)
	}
	if len(compilation.KnowledgeRetrievals) != 1 ||
		len(compilation.KnowledgeShortcuts) != 1 ||
		compilation.KnowledgeRetrievals[0].BindingIndex != 2 ||
		compilation.KnowledgeShortcuts[0].BindingIndex != 3 ||
		compilation.KnowledgeShortcuts[0].Mode !=
			corecontract.KnowledgeShortcutNotSelectedV1 {
		t.Fatalf("routed Knowledge closure=%+v", compilation)
	}
	if bytes.Contains(
		prepared.RequestCanonical,
		[]byte("css frontend evidence must never enter this prompt"),
	) || bytes.Contains(
		prepared.ContextCompilationCanonical,
		[]byte(`"hits":[]`),
	) {
		t.Fatal("NOT_SELECTED was projected into the prompt or disguised as zero-hit")
	}
}

func TestPrepareChatRequestKnowledgeRoutingLowConfidenceFallsBackFresh(t *testing.T) {
	fixture := newRoutingCoreLoopFixture(
		t,
		"write a poem",
		[]routingKnowledgeSpec{
			{
				id: "backend", text: "backend api evidence",
				routing: routingPolicyForCoreLoopTest("backend", "api"),
			},
			{
				id: "frontend", text: "frontend css evidence",
				routing: routingPolicyForCoreLoopTest("frontend", "css"),
			},
		},
	)
	prepared, err := fixture.loop.prepareChatRequestV1(
		context.Background(),
		fixture.run,
		fixture.lease,
	)
	if err != nil {
		t.Fatalf("prepare low-confidence fallback: %v", err)
	}
	if fixture.invokers[0].callCount() != 1 ||
		fixture.invokers[1].callCount() != 1 || fixture.current.calls != 2 {
		t.Fatalf(
			"fallback calls=%d/%d activation=%d, want 1/1/2",
			fixture.invokers[0].callCount(),
			fixture.invokers[1].callCount(),
			fixture.current.calls,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		prepared.ContextCompilationCanonical,
	)
	if err != nil || len(compilation.KnowledgeRetrievals) != 2 ||
		len(compilation.KnowledgeShortcuts) != 0 {
		t.Fatalf("fallback closure=%+v error=%v", compilation, err)
	}
}

func TestPrepareChatRequestLegacyKnowledgeWithoutRoutingRemainsFresh(t *testing.T) {
	fixture := newRoutingCoreLoopFixture(
		t,
		"build the feature",
		[]routingKnowledgeSpec{{
			id: "legacy", text: "build the feature with legacy knowledge",
		}},
	)
	prepared, err := fixture.loop.prepareChatRequestV1(
		context.Background(),
		fixture.run,
		fixture.lease,
	)
	if err != nil {
		t.Fatalf("prepare legacy Knowledge: %v", err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		prepared.ContextCompilationCanonical,
	)
	if err != nil || fixture.invokers[0].callCount() != 1 ||
		fixture.current.calls != 1 ||
		len(compilation.KnowledgeRetrievals) != 1 ||
		len(compilation.KnowledgeShortcuts) != 0 {
		t.Fatalf(
			"legacy calls=%d activation=%d closure=%+v error=%v",
			fixture.invokers[0].callCount(),
			fixture.current.calls,
			compilation,
			err,
		)
	}
}

func TestPrepareChatRequestNotSelectedStillRequiresExactAuthority(t *testing.T) {
	fixture := newRoutingCoreLoopFixture(
		t,
		"design an api gateway",
		[]routingKnowledgeSpec{
			{
				id: "frontend", text: "frontend css evidence",
				routing: routingPolicyForCoreLoopTest("frontend", "css"),
			},
			{
				id: "backend", text: "backend api evidence",
				routing: routingPolicyForCoreLoopTest("backend", "api"),
			},
		},
	)
	plan := routingContextPlanForTest(t, &fixture.run)
	skipped := &plan.Bindings[2]
	authorityRecord, found := fixture.run.FindContent(skipped.AuthorityCeilingRef)
	if !found {
		t.Fatal("skipped authority content is absent")
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority.AllowedScopes[0].AgentID = "other-agent"
	_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(authority)
	if err != nil {
		t.Fatal(err)
	}
	replacement := contentRecord(
		t,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	replaceRunContent(&fixture.run, skipped.AuthorityCeilingRef, replacement)
	skipped.AuthorityCeilingRef = replacement.Digest

	_, err = fixture.loop.prepareChatRequestV1(
		context.Background(),
		fixture.run,
		fixture.lease,
	)
	if !errors.Is(err, ErrInvalidPureChatRequest) {
		t.Fatalf("denied NOT_SELECTED authority error=%v", err)
	}
	if fixture.invokers[0].callCount() != 0 ||
		fixture.invokers[1].callCount() != 0 || fixture.current.calls != 0 {
		t.Fatalf(
			"denied shortcut reached Provider: calls=%d/%d activation=%d",
			fixture.invokers[0].callCount(),
			fixture.invokers[1].callCount(),
			fixture.current.calls,
		)
	}
}

func TestPrepareChatRequestPreflightsLaterNotSelectedAuthorityBeforeProvider(
	t *testing.T,
) {
	fixture := newRoutingCoreLoopFixture(
		t,
		"design an api gateway",
		[]routingKnowledgeSpec{
			{
				id: "backend", text: "backend api evidence",
				routing: routingPolicyForCoreLoopTest("backend", "api"),
			},
			{
				id: "frontend", text: "frontend css evidence",
				routing: routingPolicyForCoreLoopTest("frontend", "css"),
			},
		},
	)
	plan := routingContextPlanForTest(t, &fixture.run)
	skipped := &plan.Bindings[3]
	authorityRecord, found := fixture.run.FindContent(skipped.AuthorityCeilingRef)
	if !found {
		t.Fatal("later skipped authority content is absent")
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority.AllowedScopes[0].AgentID = "other-agent"
	_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(authority)
	if err != nil {
		t.Fatal(err)
	}
	replacement := contentRecord(
		t,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	replaceRunContent(&fixture.run, skipped.AuthorityCeilingRef, replacement)
	skipped.AuthorityCeilingRef = replacement.Digest

	_, err = fixture.loop.prepareChatRequestV1(
		context.Background(),
		fixture.run,
		fixture.lease,
	)
	if !errors.Is(err, ErrInvalidPureChatRequest) {
		t.Fatalf("later denied NOT_SELECTED authority error=%v", err)
	}
	if fixture.invokers[0].callCount() != 0 ||
		fixture.invokers[1].callCount() != 0 || fixture.current.calls != 0 {
		t.Fatalf(
			"later denied shortcut allowed an earlier Provider: calls=%d/%d activation=%d",
			fixture.invokers[0].callCount(),
			fixture.invokers[1].callCount(),
			fixture.current.calls,
		)
	}
}

type routingKnowledgeSpec struct {
	id      string
	text    string
	routing *moduleapi.KnowledgeRoutingPolicyV1
}

type routingCoreLoopFixture struct {
	run      currentstore.RunForLoop
	lease    currentstore.RunLease
	loop     *UniversalLoop
	invokers []*integrationInvoker
	current  *recordingCurrentActivationChecker
}

func newRoutingCoreLoopFixture(
	t *testing.T,
	taskText string,
	specs []routingKnowledgeSpec,
) routingCoreLoopFixture {
	t.Helper()
	run := pureChatRunFixture(t)
	oldTaskRef := run.Manifest.TaskInputRef
	task := taskContent(t, taskText)
	replaceRunContent(&run, oldTaskRef, task)
	run.Manifest.TaskInputRef = task.Digest
	run.Manifest.TenantID = "tenant-routing"
	run.Manifest.Deadline = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	run.Member.MemberID = "member-routing"
	run.Member.MemberSnapshotDigest = moduleapi.Digest(
		"freeagent.test.routing-member/v1",
		[]byte(taskText),
	)
	run.Member.Agent = corecontract.AgentRef{
		ID: "agent-routing", Version: "1", Digest: digest("7"),
	}
	lease := currentstore.RunLease{
		RunID:         run.RunID,
		OwnerID:       "owner-routing",
		LeaseEpoch:    1,
		RunRevision:   run.RunRevision,
		FrameRevision: run.Frame.Revision,
		ExpiresAt: time.Now().UTC().Add(30 * time.Minute).
			Truncate(time.Microsecond),
	}
	run.Frame.Lease = lease
	rule := moduleapi.KnowledgeScopeRuleV1{
		TenantID:     run.Manifest.TenantID,
		WorkspaceID:  run.Member.Workspace.ID,
		AgentID:      run.Member.Agent.ID,
		TaskInputRef: run.Manifest.TaskInputRef,
	}
	registrations := make([]exactadapter.Registration, 0, len(specs))
	invokers := make([]*integrationInvoker, 0, len(specs))
	plan := routingContextPlanForTest(t, &run)
	for _, spec := range specs {
		chunk, _, err := moduleapi.NewKnowledgeChunkV1(
			moduleapi.KnowledgeChunkV1{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      "document-" + spec.id,
					Version: "1",
					Digest: moduleapi.Digest(
						"freeagent.test.routing-document/v1",
						[]byte(spec.id),
					),
				},
				ChunkID:   "chunk-" + spec.id,
				Text:      spec.text,
				VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, sourceCanonical, sourceRef, err := moduleapi.NewKnowledgeSourceV1(
			moduleapi.KnowledgeSourceV1{
				SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
				ID:            "source-" + spec.id,
				Version:       "1",
				Chunks:        []moduleapi.KnowledgeChunkV1{chunk},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		bindingValue := moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            sourceRef,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		}
		if spec.routing != nil {
			routing := *spec.routing
			routing.CollectionTags = append([]string(nil), spec.routing.CollectionTags...)
			routing.MatchTerms = append([]string(nil), spec.routing.MatchTerms...)
			bindingValue.Routing = &routing
		}
		_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(bindingValue)
		if err != nil {
			t.Fatal(err)
		}
		_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
			moduleapi.ContextBindingConfigV1{
				SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
				Placement:     moduleapi.ContextPlacementUntrustedData,
				AllowSummary:  false,
				AllowDrop:     false,
				Parameters:    parameters,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		config := contentRecord(t, currentstore.ContentConfig, configCanonical)
		_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
			moduleapi.KnowledgeAuthorityCeilingV1{
				SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
				Source:            sourceRef,
				AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
				MaxHits:           8,
				MaxTotalTextBytes: 8192,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		authority := contentRecord(
			t,
			currentstore.ContentAuthorityCeiling,
			authorityCanonical,
		)
		provider := testProvider(
			"knowledge-"+spec.id,
			moduleapi.ExecutionTrustedInProcess,
		)
		provider.ArtifactDigest = moduleapi.Digest(
			"freeagent.test.routing-artifact/v1",
			[]byte(spec.id),
		)
		provider.AdapterIdentity = "freeagent.adapter.knowledge/v1"
		plan.Bindings = append(plan.Bindings, moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           config.Digest,
			AuthorityCeilingRef: authority.Digest,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		})
		run.Contents = append(run.Contents, config, authority)
		knowledge, err := exactadapter.NewDeterministicKnowledge(
			provider,
			sourceCanonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		counted := &integrationInvoker{delegate: knowledge}
		invokers = append(invokers, counted)
		registrations = append(registrations, exactadapter.Registration{
			ArtifactDigest:  provider.ArtifactDigest,
			AdapterIdentity: provider.AdapterIdentity,
			Invoker:         counted,
		})
	}
	sort.Slice(run.Contents, func(left, right int) bool {
		return run.Contents[left].Digest < run.Contents[right].Digest
	})
	registry, err := exactadapter.NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	current := &recordingCurrentActivationChecker{}
	return routingCoreLoopFixture{
		run: run, lease: lease, invokers: invokers, current: current,
		loop: &UniversalLoop{
			registry:          registry,
			currentActivation: current,
		},
	}
}

func routingContextPlanForTest(
	t *testing.T,
	run *currentstore.RunForLoop,
) *moduleapi.PortPlan {
	t.Helper()
	for index := range run.Member.PortPlans {
		plan := &run.Member.PortPlans[index]
		if plan.Port.Name == moduleapi.PortNameContextProvide &&
			plan.Port.ExactVersion == moduleapi.PortVersionV1 {
			return plan
		}
	}
	t.Fatal("context.provide/v1 PortPlan is absent")
	return nil
}

func routingPolicyForCoreLoopTest(
	tag string,
	term string,
) *moduleapi.KnowledgeRoutingPolicyV1 {
	return &moduleapi.KnowledgeRoutingPolicyV1{
		SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
		CollectionTags: []string{strings.ToLower(tag)},
		MatchTerms:     []string{strings.ToLower(term)},
		MinMatchTerms:  1,
	}
}
