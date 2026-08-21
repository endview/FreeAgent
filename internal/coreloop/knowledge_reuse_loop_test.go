package coreloop

import (
	"context"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/knowledgecore"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPrepareKnowledgeReuseV1FreshThenReuseSkipsKnowledgeProvider(
	t *testing.T,
) {
	fixture := newCoreLoopKnowledgeReuseFixture(t)
	if fixture.knowledgeInvoker.callCount() != 1 {
		t.Fatalf(
			"source fresh Knowledge calls=%d, want 1",
			fixture.knowledgeInvoker.callCount(),
		)
	}

	prepared, required, err := prepareKnowledgeReuseV1(
		fixture.run,
		fixture.prepared,
		fixture.decisions,
		fixture.decisionSetDigest,
	)
	if err != nil {
		t.Fatalf("prepareKnowledgeReuseV1: %v", err)
	}
	if len(required) != 2 || prepared[0].knowledgeReuse == nil {
		t.Fatalf("reuse closure=%+v required=%v", prepared[0], required)
	}
	if err := fixture.loop.preflightKnowledgeReuseActivationsV1(
		context.Background(),
		fixture.run,
		prepared,
		required,
	); err != nil {
		t.Fatalf("preflightKnowledgeReuseActivationsV1: %v", err)
	}
	before := fixture.knowledgeInvoker.callCount()
	material, err := fixture.loop.invokePreparedDynamicContextV1(
		context.Background(),
		fixture.run,
		fixture.lease,
		fixture.run.Manifest.Deadline,
		prepared[0],
	)
	if err != nil {
		t.Fatalf("invoke prepared REUSE: %v", err)
	}
	if fixture.knowledgeInvoker.callCount() != before {
		t.Fatalf(
			"REUSE reached Knowledge Provider: before=%d after=%d",
			before,
			fixture.knowledgeInvoker.callCount(),
		)
	}
	if material.KnowledgeReuse == nil ||
		material.KnowledgeProvenance != nil ||
		len(material.StateCanonical) != 0 ||
		len(material.RequestCanonical) != 0 ||
		len(material.OutputCanonical) != 0 {
		t.Fatalf("REUSE dynamic material=%+v", material)
	}
}

func TestPrepareKnowledgeReuseV1MissesRemainFresh(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*coreLoopKnowledgeReuseFixture)
	}{
		{
			name: "ttl boundary",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				policy := f.prepared[0].knowledgeConfig.Routing.Reuse
				f.prepared[1].memoryEvaluatedAt =
					f.sourceRetrievedAt + policy.ReuseTTLSeconds*1000
			},
		},
		{
			name: "low confidence fallback",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				decision := f.decisions[f.prepared[0].bindingIndex]
				decision.MatchedTerms = []string{}
				f.decisions[f.prepared[0].bindingIndex] = decision
				f.prepared[0].knowledgeDecision = &decision
			},
		},
		{
			name: "multiple routed hits",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				other := f.prepared[0]
				other.bindingIndex++
				decision := *other.knowledgeDecision
				decision.BindingIndex = other.bindingIndex
				other.knowledgeDecision = &decision
				f.prepared = append(f.prepared, other)
				f.decisions[other.bindingIndex] = decision
			},
		},
		{
			name: "no Memory Binding",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				f.prepared = f.prepared[:1]
			},
		},
		{
			name: "Action model-1 Compilation is not a K3 source",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				f.run.ConversationHistory[0].SourceContextCompilationAttemptID =
					"source-model-1-attempt"
			},
		},
		{
			name: "latest invalid candidate does not fall through",
			mutate: func(f *coreLoopKnowledgeReuseFixture) {
				validOlder := f.run.ConversationHistory[0]
				validOlder.TurnIndex = 1
				latestInvalid := validOlder
				latestInvalid.TurnIndex = 2
				latestInvalid.SourceRunID = "source-run-latest"
				latestInvalid.SourceAttemptID = "source-attempt-latest"
				latestInvalid.SourceContextCompilation = nil
				f.run.Manifest.ConversationTurn.TurnIndex = 3
				f.run.Manifest.ConversationTurn.PredecessorRunID =
					latestInvalid.SourceRunID
				f.run.ConversationHistory = []currentstore.ConversationHistoryTurnRecord{
					validOlder,
					latestInvalid,
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoreLoopKnowledgeReuseFixture(t)
			test.mutate(&fixture)
			prepared, required, err := prepareKnowledgeReuseV1(
				fixture.run,
				fixture.prepared,
				fixture.decisions,
				fixture.decisionSetDigest,
			)
			if err != nil {
				t.Fatalf("prepareKnowledgeReuseV1: %v", err)
			}
			if len(required) != 0 || prepared[0].knowledgeReuse != nil {
				t.Fatalf("miss became REUSE: %+v required=%v", prepared[0], required)
			}
		})
	}
}

func TestKnowledgeReuseActivationDenialPrecedesEveryProviderCall(t *testing.T) {
	fixture := newCoreLoopKnowledgeReuseFixture(t)
	prepared, required, err := prepareKnowledgeReuseV1(
		fixture.run,
		fixture.prepared,
		fixture.decisions,
		fixture.decisionSetDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.knowledgeInvoker.callCount()
	fixture.current.err = currentstore.ErrCurrentActivationDenied
	err = fixture.loop.preflightKnowledgeReuseActivationsV1(
		context.Background(),
		fixture.run,
		prepared,
		required,
	)
	if !errors.Is(err, modulehost.ErrCurrentActivationDenied) {
		t.Fatalf("activation denial error=%v", err)
	}
	if fixture.knowledgeInvoker.callCount() != before {
		t.Fatalf("activation denial reached a Provider")
	}
}

type coreLoopKnowledgeReuseFixture struct {
	run               currentstore.RunForLoop
	lease             currentstore.RunLease
	loop              *UniversalLoop
	current           *recordingCurrentActivationChecker
	knowledgeInvoker  *integrationInvoker
	prepared          []preparedDynamicContextReadV1
	decisions         map[uint32]knowledgecore.BindingDecision
	decisionSetDigest string
	sourceRetrievedAt uint64
}

func newCoreLoopKnowledgeReuseFixture(
	t *testing.T,
) coreLoopKnowledgeReuseFixture {
	t.Helper()
	policy := routingPolicyForCoreLoopTest("backend", "api")
	policy.Reuse = &moduleapi.KnowledgeReusePolicyV1{
		ExactQuestionOnly:    true,
		MinCategoryCount:     1,
		MinRepeatedTermCount: 1,
		MaxLookbackTurns:     8,
		ReuseTTLSeconds:      3600,
	}
	source := newRoutingCoreLoopFixture(
		t,
		"api backend design",
		[]routingKnowledgeSpec{{
			id: "reuse", text: "stable backend knowledge", routing: policy,
		}},
	)
	fresh, err := source.loop.prepareChatRequestV1(
		context.Background(),
		source.run,
		source.lease,
	)
	if err != nil {
		t.Fatalf("prepare source fresh request: %v", err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		fresh.ContextCompilationCanonical,
	)
	if err != nil || len(compilation.KnowledgeRetrievals) != 1 ||
		compilation.KnowledgeRetrievals[0].Provenance == nil {
		t.Fatalf("source fresh Compilation=%+v err=%v", compilation, err)
	}
	retrieval := compilation.KnowledgeRetrievals[0]
	sourceRetrievedAt := retrieval.Provenance.RetrievedAtUnixMS

	plan, materials, err := restoreContextCompilerBindings(source.run)
	if err != nil || plan == nil {
		t.Fatalf("restore source context plan: %v", err)
	}
	decisions, decisionSetDigest, err := decideDynamicKnowledgeBindingsV1(
		*plan,
		materials,
		"api backend design",
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.Bindings[retrieval.BindingIndex]
	config, err := moduleapi.RestoreContextBindingConfigV1(
		materials[retrieval.BindingIndex].ConfigCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeConfig, err :=
		moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
	if err != nil {
		t.Fatal(err)
	}
	authorityContent, err := exactContent(
		source.run,
		binding.AuthorityCeilingRef,
		currentstore.ContentAuthorityCeiling,
		"source Knowledge authority",
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		authorityContent.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	maxHits, maxBytes, err := moduleapi.ResolveKnowledgeLimitsV1(
		knowledgeConfig,
		authority,
		retrieval.Scope,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, _, requestDigest, err := moduleapi.NewKnowledgeContextRequestV1(
		moduleapi.KnowledgeContextRequestV1{
			SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
			Source:            retrieval.Source,
			Scope:             retrieval.Scope,
			QueryText:         "api backend design",
			MaxHits:           maxHits,
			MaxTotalTextBytes: maxBytes,
		},
	)
	if err != nil || requestDigest != retrieval.RequestDigest {
		t.Fatalf("rebuild source request digest=%q err=%v", requestDigest, err)
	}
	decision := decisions[retrieval.BindingIndex]
	knowledgePrepared := preparedDynamicContextReadV1{
		bindingIndex:       retrieval.BindingIndex,
		binding:            binding,
		protocol:           moduleapi.KnowledgeContextBindingSchemaV1,
		authorityCanonical: authorityContent.CanonicalBytes,
		requestCanonical:   []byte("private-current-request"),
		requestDigest:      requestDigest,
		knowledgeConfig:    &knowledgeConfig,
		knowledgeRequest:   &request,
		knowledgeDecision:  &decision,
		decisionSetDigest:  decisionSetDigest,
	}

	memoryConfig, _, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion: moduleapi.MemoryContextBindingSchemaV1,
			Kinds: []moduleapi.MemoryEntryKindV1{
				moduleapi.MemoryEntryCategoryCount,
				moduleapi.MemoryEntryRepeatedTermCount,
			},
			MaxItems:          8,
			MaxTotalTextBytes: 1024,
			CategoryRules: []moduleapi.MemoryCategoryRuleV1{{
				Key: "backend", Terms: []string{"api"},
			}},
			StopTerms:           []string{},
			SummaryMaxTextBytes: 0,
			EntryTTLSeconds:     7200,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configDigest, err := moduleapi.ComputeMemoryContextBindingDigestV1(
		memoryConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluatedAt := sourceRetrievedAt + 1
	expiresAt := sourceRetrievedAt + 7200*1000
	category := coreLoopReuseCounterEntry(
		t,
		"category-backend",
		moduleapi.MemoryEntryCategoryCount,
		"backend",
		source.run.Member.Workspace.ID,
		configDigest,
		evaluatedAt-1,
		expiresAt,
	)
	repeated := coreLoopReuseCounterEntry(
		t,
		"term-api",
		moduleapi.MemoryEntryRepeatedTermCount,
		"api",
		source.run.Member.Workspace.ID,
		configDigest,
		evaluatedAt-1,
		expiresAt,
	)
	snapshot, _, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      source.run.Manifest.TenantID,
			AgentID:       source.run.Member.Agent.ID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{category, repeated},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRef := moduleapi.MemorySnapshotRefV1{
		TenantID: snapshot.TenantID,
		AgentID:  snapshot.AgentID,
		Revision: snapshot.Revision,
		Digest: moduleapi.Digest(
			"freeagent.test.memory-snapshot-content/v1",
			[]byte("reuse"),
		),
	}
	memoryScope := moduleapi.MemoryQueryScopeV1{
		TenantID: source.run.Manifest.TenantID,
		Workspace: moduleapi.MemoryObjectRefV1{
			ID: source.run.Member.Workspace.ID, Version: source.run.Member.Workspace.Version,
			Digest: source.run.Member.Workspace.Digest,
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID: source.run.Member.Agent.ID, Version: source.run.Member.Agent.Version,
			Digest: source.run.Member.Agent.Digest,
		},
		TaskInputRef: source.run.Manifest.TaskInputRef,
	}
	memoryAuthority, _, err := moduleapi.NewMemoryAuthorityCeilingV1(
		moduleapi.MemoryAuthorityCeilingV1{
			SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
			TenantID:            memoryScope.TenantID,
			AgentID:             memoryScope.Agent.ID,
			AllowedWorkspaceIDs: []string{memoryScope.Workspace.ID},
			AllowedKinds: []moduleapi.MemoryEntryKindV1{
				moduleapi.MemoryEntryCategoryCount,
				moduleapi.MemoryEntryRepeatedTermCount,
			},
			MaxItems:          8,
			MaxTotalTextBytes: 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	memoryPrepared := preparedDynamicContextReadV1{
		bindingIndex: retrieval.BindingIndex + 1,
		binding: moduleapi.PortBinding{
			Provider: testProvider(
				"memory-reuse",
				moduleapi.ExecutionTrustedInProcess,
			),
			ConfigRef:           moduleapi.Digest("freeagent.test.config/v1", []byte("memory")),
			AuthorityCeilingRef: moduleapi.Digest("freeagent.test.authority/v1", []byte("memory")),
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
		protocol:          moduleapi.MemoryContextBindingSchemaV1,
		memoryConfig:      &memoryConfig,
		memoryAuthority:   &memoryAuthority,
		memorySnapshot:    &snapshot,
		memorySnapshotRef: &snapshotRef,
		memoryScope:       &memoryScope,
		memoryEvaluatedAt: evaluatedAt,
	}

	currentRun := source.run
	currentRun.Manifest.ConversationTurn = &corecontract.ConversationTurnRefV1{
		SchemaVersion:    corecontract.ConversationTurnRefSchemaVersionV1,
		ConversationID:   "conversation-reuse",
		PrincipalID:      "principal-reuse",
		TurnIndex:        2,
		PredecessorRunID: "source-run",
	}
	sourceCompilation := contentRecord(
		t,
		currentstore.ContentContextCompilation,
		fresh.ContextCompilationCanonical,
	)
	userContent, err := exactContent(
		source.run,
		source.run.Manifest.TaskInputRef,
		currentstore.ContentTaskInput,
		"source task",
	)
	if err != nil {
		t.Fatal(err)
	}
	currentRun.ConversationHistory = []currentstore.ConversationHistoryTurnRecord{{
		TurnIndex:                         1,
		SourceRunID:                       "source-run",
		UserContent:                       userContent,
		SourceAttemptID:                   "source-attempt",
		SourceContextCompilationAttemptID: "source-attempt",
		SourceContextCompilation:          &sourceCompilation,
	}}
	return coreLoopKnowledgeReuseFixture{
		run:               currentRun,
		lease:             source.lease,
		loop:              source.loop,
		current:           source.current,
		knowledgeInvoker:  source.invokers[0],
		prepared:          []preparedDynamicContextReadV1{knowledgePrepared, memoryPrepared},
		decisions:         decisions,
		decisionSetDigest: decisionSetDigest,
		sourceRetrievedAt: sourceRetrievedAt,
	}
}

func coreLoopReuseCounterEntry(
	t *testing.T,
	id string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspaceID string,
	configDigest string,
	createdAt uint64,
	expiresAt uint64,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               id,
		Kind:                  kind,
		Key:                   key,
		Count:                 1,
		VisibleWorkspaceIDs:   []string{workspaceID},
		SourceRefs:            []string{moduleapi.Digest("freeagent.test.memory-source/v1", []byte(id))},
		AlgorithmVersion:      memorycore.SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: configDigest,
		CreatedAtUnixMS:       createdAt,
		ExpiresAtUnixMS:       expiresAt,
	})
	if err != nil {
		t.Fatalf("NewMemoryEntryV1(%s): %v", id, err)
	}
	return entry
}
