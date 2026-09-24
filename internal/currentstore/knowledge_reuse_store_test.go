package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/knowledgecore"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestKnowledgeReuseStoreClosesRetrievalReuseAndShortcutExactlyOnce(
	t *testing.T,
) {
	fixture := newKnowledgeReuseStoreFixture(t, time.Hour)

	source := mustRestoreDynamicContextCompilation(t, fixture.sourceCompiled)
	if len(source.KnowledgeRetrievals) != 1 ||
		len(source.KnowledgeReuses) != 0 ||
		len(source.KnowledgeShortcuts) != 0 {
		t.Fatalf("source Knowledge outcomes=%+v", source)
	}
	current := mustRestoreDynamicContextCompilation(t, fixture.currentCompiled)
	if len(current.KnowledgeRetrievals) != 0 ||
		len(current.KnowledgeReuses) != 1 ||
		len(current.KnowledgeShortcuts) != 0 {
		t.Fatalf("current Knowledge outcomes=%+v", current)
	}
	bindings := mustKnowledgeReuseStoreBindings(t, fixture.currentRun)
	if err := validateKnowledgeRetrievalsForRun(
		current,
		fixture.currentRunWithHead(t),
		fixture.currentCompiled.Request,
		bindings,
	); err != nil {
		t.Fatalf("valid current REUSE closure: %v", err)
	}

	mixed := newMixedKnowledgeRoutingClosure(t)
	if len(mixed.compilation.KnowledgeShortcuts) != 1 {
		t.Fatalf("routing shortcut outcomes=%+v", mixed.compilation)
	}
	if err := validateKnowledgeRetrievalsForRun(
		mixed.compilation,
		mixed.run,
		mixed.request,
		mixed.bindings,
	); err != nil {
		t.Fatalf("valid NOT_SELECTED shortcut closure: %v", err)
	}

	duplicate := current
	duplicate.KnowledgeRetrievals = []corecontract.KnowledgeRetrievalEvidenceV1{
		current.KnowledgeReuses[0].FreshRetrieval,
	}
	if err := validateKnowledgeRetrievalsForRun(
		duplicate,
		fixture.currentRunWithHead(t),
		fixture.currentCompiled.Request,
		bindings,
	); err == nil {
		t.Fatal("accepted retrieval and reuse for the same Knowledge Binding")
	}
}

func TestKnowledgeReuseStoreRejectsSourceAttemptCompilationAndLatestTampering(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *knowledgeReuseStoreFixture, *RunForLoop)
	}{
		{
			name: "source Attempt",
			mutate: func(_ *testing.T, _ *knowledgeReuseStoreFixture, run *RunForLoop) {
				run.ConversationHistory[0].SourceAttemptID = "attempt-tampered"
			},
		},
		{
			name: "source Compilation belongs to Action model-1",
			mutate: func(_ *testing.T, _ *knowledgeReuseStoreFixture, run *RunForLoop) {
				run.ConversationHistory[0].SourceContextCompilationAttemptID =
					"attempt-action-model-1"
			},
		},
		{
			name: "source Compilation ref",
			mutate: func(_ *testing.T, _ *knowledgeReuseStoreFixture, run *RunForLoop) {
				run.ConversationHistory[0].SourceContextCompilation.Digest =
					strings.Repeat("f", 64)
			},
		},
		{
			name: "source Compilation belongs to Action model-1",
			mutate: func(_ *testing.T, _ *knowledgeReuseStoreFixture, run *RunForLoop) {
				run.ConversationHistory[0].SourceContextCompilationAttemptID =
					"attempt-action-model-1"
			},
		},
		{
			name: "latest exact candidate does not fall through",
			mutate: func(_ *testing.T, fixture *knowledgeReuseStoreFixture, run *RunForLoop) {
				run.Manifest.ConversationTurn.TurnIndex = 3
				run.Manifest.ConversationTurn.PredecessorRunID = "run-latest-invalid"
				latest := run.ConversationHistory[0]
				latest.TurnIndex = 2
				latest.SourceRunID = "run-latest-invalid"
				latest.SourceAttemptID = "attempt-latest-invalid"
				latest.SourceContextCompilation = nil
				latest.UserContent = cloneContentRecord(fixture.currentRun.ConversationHistory[0].UserContent)
				run.ConversationHistory = append(run.ConversationHistory, latest)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeReuseStoreFixture(t, time.Hour)
			run := fixture.currentRunWithHead(t)
			run.ConversationHistory = cloneConversationHistoryTurnsForReuseTest(
				run.ConversationHistory,
			)
			test.mutate(t, &fixture, &run)
			compilation := mustRestoreDynamicContextCompilation(
				t,
				fixture.currentCompiled,
			)
			err := validateKnowledgeRetrievalsForRun(
				compilation,
				run,
				fixture.currentCompiled.Request,
				mustKnowledgeReuseStoreBindings(t, run),
			)
			if err == nil {
				t.Fatal("accepted tampered Knowledge reuse source closure")
			}
		})
	}
}

func TestBeginModelDispatchKnowledgeReuseMutableStateDenialsAreAtomic(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *knowledgeReuseStoreFixture)
		want    error
	}{
		{
			name: "Memory head advanced",
			prepare: func(t *testing.T, fixture *knowledgeReuseStoreFixture) {
				pending := fixture.beginAuxiliaryFreshAttempt(t)
				fixture.completeAttempt(t, pending)
			},
		},
		{
			name: "Knowledge activation revoked",
			prepare: func(t *testing.T, fixture *knowledgeReuseStoreFixture) {
				fixture.revokeCurrentProvider(t, fixture.knowledgeProvider.InstanceID)
			},
			want: ErrCurrentActivationDenied,
		},
		{
			name: "Memory activation revoked",
			prepare: func(t *testing.T, fixture *knowledgeReuseStoreFixture) {
				fixture.revokeCurrentProvider(t, fixture.memoryProvider.InstanceID)
			},
			want: ErrCurrentActivationDenied,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeReuseStoreFixture(t, time.Hour)
			before := modelDispatchAtomicFootprintForReuseTest(
				t,
				fixture.admission.store,
				fixture.currentRun.RunID,
			)
			test.prepare(t, &fixture)
			result, err := fixture.admission.store.BeginModelDispatch(
				context.Background(),
				fixture.currentBeginInput,
			)
			if err == nil {
				t.Fatal("mutable Knowledge reuse denial created a model Attempt")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("BeginModelDispatch error=%v, want %v", err, test.want)
			}
			if result.Created || result.InvokeAllowed ||
				result.ConsumeModelInvocationPermit() {
				t.Fatalf("denied BeginModelDispatch leaked permit: %+v", result)
			}
			after := modelDispatchAtomicFootprintForReuseTest(
				t,
				fixture.admission.store,
				fixture.currentRun.RunID,
			)
			if after != before {
				t.Fatalf("denied BeginModelDispatch changed target footprint before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestBeginModelDispatchKnowledgeReuseRejectsPhysicalSourceTamperingAtomically(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *knowledgeReuseStoreFixture)
	}{
		{
			name: "source Attempt is no longer SUCCEEDED",
			mutate: func(t *testing.T, fixture *knowledgeReuseStoreFixture) {
				execClosedFileTamperV1(
					t,
					fixture.admission.store,
					[]string{"model_dispatch_attempts_observation_update_guard"},
					`
					UPDATE model_dispatch_attempts
					SET state='FAILED',
						result_ref=NULL,
						error_classification='TEST_TAMPER',
						revision=revision+1
					WHERE attempt_id=? AND state='SUCCEEDED'
				`,
					fixture.sourceAttemptID,
				)
			},
		},
		{
			name: "source Attempt points at a non-Compilation content record",
			mutate: func(t *testing.T, fixture *knowledgeReuseStoreFixture) {
				execClosedFileTamperV1(
					t,
					fixture.admission.store,
					[]string{"model_dispatch_attempts_observation_update_guard"},
					`
					UPDATE model_dispatch_attempts
					SET context_compilation_ref=request_ref,
						revision=revision+1
					WHERE attempt_id=?
					  AND state='SUCCEEDED'
					  AND context_compilation_ref IS NOT NULL
				`,
					fixture.sourceAttemptID,
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeReuseStoreFixture(t, time.Hour)
			test.mutate(t, &fixture)
			before := modelDispatchAtomicFootprintForReuseTest(
				t,
				fixture.admission.store,
				fixture.currentRun.RunID,
			)
			result, err := fixture.admission.store.BeginModelDispatch(
				context.Background(),
				fixture.currentBeginInput,
			)
			if err == nil {
				t.Fatal("physical source tampering created a target model Attempt")
			}
			if result.Created || result.InvokeAllowed ||
				result.ConsumeModelInvocationPermit() {
				t.Fatalf("physical source tampering leaked permit: %+v", result)
			}
			after := modelDispatchAtomicFootprintForReuseTest(
				t,
				fixture.admission.store,
				fixture.currentRun.RunID,
			)
			if after != before {
				t.Fatalf("physical source rejection changed target footprint before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestBeginModelDispatchKnowledgeReuseExactRetryUsesFrozenAttempt(
	t *testing.T,
) {
	const reuseTTL = 30 * time.Second
	fixture := newKnowledgeReuseStoreFixture(t, reuseTTL)
	first, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		fixture.currentBeginInput,
	)
	if err != nil || !first.Created || !first.InvokeAllowed {
		t.Fatalf("first BeginModelDispatch=%+v error=%v", first, err)
	}
	// Advance only the pure TTL validator: a real wall-clock sleep made the
	// pre-Attempt setup race the one-second policy under -race. The boundary
	// proof below still requires this evidence to miss for any fresh Attempt.
	requireKnowledgeReuseTTLBoundaryForTest(t, fixture, reuseTTL)

	auxiliary := fixture.beginAuxiliaryFreshAttempt(t)
	fixture.completeAttempt(t, auxiliary)
	fixture.revokeCurrentProvider(t, fixture.memoryProvider.InstanceID)

	before := modelDispatchAtomicFootprintForReuseTest(
		t,
		fixture.admission.store,
		fixture.currentRun.RunID,
	)
	retryInput := fixture.currentBeginInput
	retryInput.Lease = first.Lease
	retry, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		retryInput,
	)
	if err != nil {
		t.Fatalf("exact retry after mutable changes: %v", err)
	}
	if retry.Created || retry.InvokeAllowed ||
		retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID ||
		retry.Attempt.State != first.Attempt.State ||
		!bytes.Equal(
			retry.Attempt.ContextCompilation.CanonicalBytes,
			first.Attempt.ContextCompilation.CanonicalBytes,
		) {
		t.Fatalf("exact retry did not return frozen Attempt: first=%+v retry=%+v", first, retry)
	}
	after := modelDispatchAtomicFootprintForReuseTest(
		t,
		fixture.admission.store,
		fixture.currentRun.RunID,
	)
	if after != before {
		t.Fatalf("exact retry changed persisted footprint before=%+v after=%+v", before, after)
	}
}

func TestKnowledgeReuseStoreRejectsCounterAlgorithmConfigAndWorkspaceTampering(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*moduleapi.MemoryEntryV1)
	}{
		{
			name: "algorithm",
			mutate: func(entry *moduleapi.MemoryEntryV1) {
				entry.AlgorithmVersion = "freeagent.memory-successful-revision/v2"
			},
		},
		{
			name: "Config",
			mutate: func(entry *moduleapi.MemoryEntryV1) {
				entry.AlgorithmConfigDigest = strings.Repeat("e", 64)
			},
		},
		{
			name: "Workspace",
			mutate: func(entry *moduleapi.MemoryEntryV1) {
				entry.VisibleWorkspaceIDs = []string{"workspace-other"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeReuseStoreFixture(t, time.Hour)
			compilation := mustRestoreDynamicContextCompilation(
				t,
				fixture.currentCompiled,
			)
			run := fixture.currentRun
			head := cloneAgentMemoryRevisionRecord(fixture.currentHead)
			head.Snapshot.Entries = append(
				[]moduleapi.MemoryEntryV1(nil),
				head.Snapshot.Entries...,
			)
			changed := false
			for index := range head.Snapshot.Entries {
				if head.Snapshot.Entries[index].Kind !=
					moduleapi.MemoryEntryCategoryCount {
					continue
				}
				entry := head.Snapshot.Entries[index]
				entry.EntryDigest = ""
				test.mutate(&entry)
				frozen, _, err := moduleapi.NewMemoryEntryV1(entry)
				if err != nil {
					t.Fatal(err)
				}
				head.Snapshot.Entries[index] = frozen
				changed = true
				break
			}
			if !changed {
				t.Fatal("current Memory head has no category counter")
			}
			frozenSnapshot, snapshotCanonical, err :=
				moduleapi.NewAgentMemorySnapshotV1(head.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			snapshotDigest, err := ComputeContentDigest(
				ContentMemorySnapshot,
				admissionJSONMediaType,
				snapshotCanonical,
			)
			if err != nil {
				t.Fatal(err)
			}
			head.Snapshot = frozenSnapshot
			head.SnapshotRef.Digest = snapshotDigest
			head.CanonicalBytes = snapshotCanonical
			run, err = runWithAdditionalContent(
				run,
				memoryHeadContentRecordForReuseTest(head),
			)
			if err != nil {
				t.Fatal(err)
			}
			compilation.MemoryReads = append(
				[]corecontract.MemoryReadEvidenceV1(nil),
				compilation.MemoryReads...,
			)
			compilation.MemoryReads[0].Snapshot = head.SnapshotRef
			compilation.KnowledgeReuses = append(
				[]corecontract.KnowledgeReuseEvidenceV1(nil),
				compilation.KnowledgeReuses...,
			)
			reuse := compilation.KnowledgeReuses[0]
			for _, entry := range head.Snapshot.Entries {
				if entry.Kind != moduleapi.MemoryEntryCategoryCount {
					continue
				}
				reuse.CategoryCounter, err = moduleapi.NewMemoryCandidateV1(entry)
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			compilation.KnowledgeReuses[0] = reuse

			knowledgeBindings := mustKnowledgeReuseStoreBindings(t, run)
			memoryBindings, err := frozenMemoryBindingsForRun(
				run.Manifest,
				run.Member,
				runKnowledgeContentGetter(run),
			)
			if err != nil {
				t.Fatal(err)
			}
			taskRecord, _ := run.FindContent(run.Manifest.TaskInputRef)
			task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
			if err != nil {
				t.Fatal(err)
			}
			decisionSet, decisionDigest, err := decideFrozenKnowledgeBindings(
				task.Text,
				knowledgeBindings,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateKnowledgeReuseForRun(
				compilation,
				run,
				task.Text,
				decisionDigest,
				qualifiedKnowledgeReuseBindingCount(decisionSet),
				knowledgeBindings[0],
				decisionSet.Decisions[0],
				reuse,
				memoryBindings,
				nil,
			); err == nil {
				t.Fatal("accepted tampered deterministic Memory counter proof")
			}
		})
	}
}

type knowledgeReuseStoreFixture struct {
	admission                *admissionCommitFixture
	conversationID           string
	sourceRunID              string
	sourceAttemptID          string
	sourceRetrievedAtUnixMS  uint64
	sourceCompiled           contextcompiler.CompileResultV1
	currentRun               RunForLoop
	currentLease             RunLease
	currentHead              AgentMemoryRevisionRecord
	currentCompiled          contextcompiler.CompileResultV1
	currentBeginInput        BeginModelDispatchInput
	knowledgeProvider        moduleapi.ActivatedModuleRef
	memoryProvider           moduleapi.ActivatedModuleRef
	knowledgeConfigCanonical []byte
	knowledgeAuthority       []byte
	memoryConfigCanonical    []byte
	memoryAuthorityCanonical []byte
	memoryConfig             moduleapi.MemoryContextBindingV1
	memoryAuthority          moduleapi.MemoryAuthorityCeilingV1
}

func newKnowledgeReuseStoreFixture(
	t *testing.T,
	reuseTTL time.Duration,
) knowledgeReuseStoreFixture {
	t.Helper()
	if reuseTTL <= 0 || reuseTTL%time.Second != 0 {
		t.Fatalf("invalid test reuse TTL %s", reuseTTL)
	}
	base := prepareDynamicKnowledgeAdmission(t, nil)
	admission := base.admission
	control, err := controlcontract.RestoreControlSnapshot(
		admission.controlCanonical,
		admission.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		admission.catalogCanonical,
		admission.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}

	knowledgeSpecIndex := -1
	for index, spec := range control.Profiles[0].Bindings {
		if spec.Port == base.contextPort && spec.InstanceID ==
			"instance-dynamic-knowledge" {
			knowledgeSpecIndex = index
			break
		}
	}
	if knowledgeSpecIndex < 0 {
		t.Fatal("dynamic Knowledge Binding is absent from fixture Control")
	}
	knowledgeSpec := control.Profiles[0].Bindings[knowledgeSpecIndex]
	knowledgeEntry, found := catalog.FindInstance(knowledgeSpec.InstanceID)
	if !found {
		t.Fatal("dynamic Knowledge Provider is absent from fixture Catalog")
	}
	_, knowledgeParameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            base.source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
			Routing: &moduleapi.KnowledgeRoutingPolicyV1{
				SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
				CollectionTags: []string{"greeting"},
				MatchTerms:     []string{"hello"},
				MinMatchTerms:  1,
				Reuse: &moduleapi.KnowledgeReusePolicyV1{
					ExactQuestionOnly:    true,
					MinCategoryCount:     1,
					MinRepeatedTermCount: 1,
					MaxLookbackTurns:     8,
					ReuseTTLSeconds:      uint64(reuseTTL / time.Second),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, knowledgeConfigCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    knowledgeParameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeConfigRef := putPublicationJSON(
		t,
		admission.store,
		ContentConfig,
		knowledgeConfigCanonical,
	)
	control.Profiles[0].Bindings[knowledgeSpecIndex].ConfigRef = knowledgeConfigRef

	memoryManifest := canonicalModuleManifest(
		t,
		"test.memory.reuse",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"provides": []any{map[string]any{
				"name":          base.contextPort.Name,
				"exact_version": base.contextPort.ExactVersion,
			}},
		},
	)
	memoryInstallation, err := admission.store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-memory-reuse",
			memoryManifest,
			strings.Repeat("7", 64),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	memoryActivation, err := admission.store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-memory-reuse",
			TenantID:           control.TenantID,
			InstanceID:         "instance-memory-reuse",
			InstallationID:     memoryInstallation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core.memory.reuse.adapter",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	memoryProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           memoryInstallation.ModuleID,
		Version:            memoryInstallation.ExactVersion,
		ArtifactDigest:     memoryInstallation.ArtifactDigest,
		InstanceID:         memoryActivation.InstanceID,
		ExecutionClass:     memoryActivation.ExecutionClass,
		AdapterIdentity:    memoryActivation.AdapterIdentity,
		ActivationRevision: memoryActivation.ActivationRevision,
	}
	memoryConfig, memoryParameters, memoryConfigDigest, err :=
		moduleapi.NewMemoryContextBindingV1(
			moduleapi.MemoryContextBindingV1{
				SchemaVersion: moduleapi.MemoryContextBindingSchemaV1,
				Kinds: []moduleapi.MemoryEntryKindV1{
					moduleapi.MemoryEntryCategoryCount,
					moduleapi.MemoryEntryRepeatedTermCount,
				},
				MaxItems:          8,
				MaxTotalTextBytes: 4096,
				CategoryRules: []moduleapi.MemoryCategoryRuleV1{{
					Key: "greeting", Terms: []string{"hello"},
				}},
				StopTerms:           []string{},
				SummaryMaxTextBytes: 0,
				EntryTTLSeconds:     7200,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	_, memoryConfigCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    memoryParameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	memoryConfigRef := putPublicationJSON(
		t,
		admission.store,
		ContentConfig,
		memoryConfigCanonical,
	)
	memoryAuthority, memoryAuthorityCanonical, err :=
		moduleapi.NewMemoryAuthorityCeilingV1(
			moduleapi.MemoryAuthorityCeilingV1{
				SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
				TenantID:            control.TenantID,
				AgentID:             control.Agents[0].ID,
				AllowedWorkspaceIDs: []string{control.Workspaces[0].Workspace.ID},
				AllowedKinds: []moduleapi.MemoryEntryKindV1{
					moduleapi.MemoryEntryCategoryCount,
					moduleapi.MemoryEntryRepeatedTermCount,
				},
				MaxItems:          8,
				MaxTotalTextBytes: 4096,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	memoryAuthorityRef := putPublicationJSON(
		t,
		admission.store,
		ContentAuthorityCeiling,
		memoryAuthorityCanonical,
	)
	control.Profiles[0].Bindings = append(
		control.Profiles[0].Bindings,
		controlcontract.BindingSpec{
			Port:                base.contextPort,
			InstanceID:          memoryProvider.InstanceID,
			ConfigRef:           memoryConfigRef,
			AuthorityCeilingRef: memoryAuthorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	)

	control.SnapshotID += "-reuse"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID += "-reuse"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: memoryProvider,
		Provides:   []moduleapi.PortRef{base.contextPort},
	})
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := admission.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: admission.basis.PointerRevision,
			NewPointerRevision:      admission.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish Knowledge reuse fixture: %v", err)
	}
	admission.basis = basis
	admission.controlCanonical = controlCanonical
	admission.catalogCanonical = catalogCanonical
	widenDynamicContextConversationPolicy(t, admission)

	nowMillis := uint64(time.Now().UTC().UnixMilli())
	createdAt := nowMillis - 1000
	expiresAt := nowMillis + uint64((2*time.Hour)/time.Millisecond)
	category := knowledgeReuseStoreCounterEntry(
		t,
		"seed-category-greeting",
		moduleapi.MemoryEntryCategoryCount,
		"greeting",
		control.Workspaces[0].Workspace.ID,
		memoryConfigDigest,
		createdAt,
		expiresAt,
	)
	repeated := knowledgeReuseStoreCounterEntry(
		t,
		"seed-term-hello",
		moduleapi.MemoryEntryRepeatedTermCount,
		"hello",
		control.Workspaces[0].Workspace.ID,
		memoryConfigDigest,
		createdAt,
		expiresAt,
	)
	_, genesisCanonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      control.TenantID,
			AgentID:       control.Agents[0].ID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{category, repeated},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admission.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	); err != nil {
		t.Fatal(err)
	}

	fixture := knowledgeReuseStoreFixture{
		admission:                admission,
		conversationID:           "conversation-knowledge-reuse",
		sourceRunID:              "run-knowledge-reuse-source",
		sourceAttemptID:          "attempt-knowledge-reuse-source",
		knowledgeProvider:        knowledgeEntry.Activation,
		memoryProvider:           memoryProvider,
		knowledgeConfigCanonical: knowledgeConfigCanonical,
		knowledgeAuthority:       bytes.Clone(base.authority),
		memoryConfigCanonical:    memoryConfigCanonical,
		memoryAuthorityCanonical: memoryAuthorityCanonical,
		memoryConfig:             memoryConfig,
		memoryAuthority:          memoryAuthority,
	}
	createConversationForAdmission(t, admission, fixture.conversationID)
	sourceInput := dynamicConversationAdmissionInput(
		t,
		admission,
		fixture.conversationID,
		"admission-knowledge-reuse-source",
		0,
		"",
		fixture.sourceRunID,
	)
	if _, err := admission.store.CommitConversationTurnAdmission(
		context.Background(),
		sourceInput,
	); err != nil {
		t.Fatal(err)
	}
	sourceLease, sourceRun := acquireDynamicContextConversationRun(
		t,
		admission.store,
		fixture.sourceRunID,
		"knowledge-reuse-source-worker",
	)
	sourceHead, err := admission.store.GetCurrentAgentMemory(
		context.Background(),
		control.TenantID,
		control.Agents[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.sourceRetrievedAtUnixMS = uint64(time.Now().UTC().UnixMilli())
	fixture.sourceCompiled = fixture.compileForRun(
		t,
		sourceRun,
		sourceHead,
		nil,
		fixture.sourceRetrievedAtUnixMS,
	)
	completeDynamicContextConversationAttempt(
		t,
		admission.store,
		sourceRun,
		sourceLease,
		fixture.sourceAttemptID,
		fixture.sourceCompiled,
	)

	currentInput := dynamicConversationAdmissionInput(
		t,
		admission,
		fixture.conversationID,
		"admission-knowledge-reuse-current",
		1,
		fixture.sourceRunID,
		"run-knowledge-reuse-current",
	)
	if _, err := admission.store.CommitConversationTurnAdmission(
		context.Background(),
		currentInput,
	); err != nil {
		t.Fatal(err)
	}
	fixture.currentLease, fixture.currentRun =
		acquireDynamicContextConversationRun(
			t,
			admission.store,
			"run-knowledge-reuse-current",
			"knowledge-reuse-current-worker",
		)
	fixture.currentHead, err = admission.store.GetCurrentAgentMemory(
		context.Background(),
		control.TenantID,
		control.Agents[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	reuse := fixture.exactReuseEvidence(t)
	fixture.currentCompiled = fixture.compileForRun(
		t,
		fixture.currentRun,
		fixture.currentHead,
		&reuse,
		0,
	)
	fixture.currentBeginInput = memoryBeginInput(
		fixture.currentRun,
		fixture.currentLease,
		"attempt-knowledge-reuse-current",
		fixture.currentCompiled,
	)
	return fixture
}

func knowledgeReuseStoreCounterEntry(
	t *testing.T,
	entryID string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspaceID string,
	configDigest string,
	createdAt uint64,
	expiresAt uint64,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               entryID,
		Kind:                  kind,
		Key:                   key,
		Count:                 1,
		VisibleWorkspaceIDs:   []string{workspaceID},
		SourceRefs:            []string{strings.Repeat("d", 64)},
		AlgorithmVersion:      memorycore.SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: configDigest,
		CreatedAtUnixMS:       createdAt,
		ExpiresAtUnixMS:       expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func (fixture knowledgeReuseStoreFixture) compileForRun(
	t *testing.T,
	run RunForLoop,
	head AgentMemoryRevisionRecord,
	reuse *corecontract.KnowledgeReuseEvidenceV1,
	retrievedAtUnixMS uint64,
) contextcompiler.CompileResultV1 {
	t.Helper()
	knowledgeBindings := mustKnowledgeReuseStoreBindings(t, run)
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil || len(memoryBindings) != 1 {
		t.Fatalf("frozen Memory reuse Bindings=%d error=%v", len(memoryBindings), err)
	}
	knowledgeBinding := knowledgeBindings[0]
	memoryBinding := memoryBindings[0]
	var contextPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		if run.Member.PortPlans[index].Port.Name ==
			moduleapi.PortNameContextProvide &&
			run.Member.PortPlans[index].Port.ExactVersion ==
				moduleapi.PortVersionV1 {
			contextPlan = &run.Member.PortPlans[index]
			break
		}
	}
	if contextPlan == nil || len(contextPlan.Bindings) != 2 {
		t.Fatalf("Knowledge reuse context plan=%+v", contextPlan)
	}
	taskRecord, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found {
		t.Fatal("Knowledge reuse Task is absent")
	}
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, decisionSetDigest, err := decideFrozenKnowledgeBindings(
		task.Text,
		knowledgeBindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	materials := make(
		[]contextcompiler.BindingMaterialV1,
		len(contextPlan.Bindings),
	)
	knowledgeConfig, found := run.FindContent(
		knowledgeBinding.Binding.ConfigRef,
	)
	if !found {
		t.Fatal("Knowledge reuse Config is absent")
	}
	knowledgeAuthority, found := run.FindContent(
		knowledgeBinding.Binding.AuthorityCeilingRef,
	)
	if !found {
		t.Fatal("Knowledge reuse authority is absent")
	}
	knowledgeMaterial := contextcompiler.BindingMaterialV1{
		ConfigCanonical:         knowledgeConfig.CanonicalBytes,
		AuthorityCanonical:      knowledgeAuthority.CanonicalBytes,
		StaticContextCanonicals: [][]byte{},
	}
	if reuse == nil {
		request, requestCanonical, requestDigest, err :=
			moduleapi.NewKnowledgeContextRequestV1(
				moduleapi.KnowledgeContextRequestV1{
					SchemaVersion: moduleapi.KnowledgeContextRequestSchemaV1,
					Source:        knowledgeBinding.Config.Source,
					Scope:         knowledgeBinding.Scope,
					QueryText:     task.Text,
					MaxHits:       knowledgeBinding.MaxHits,
					MaxTotalTextBytes: knowledgeBinding.
						MaxTextBytes,
				},
			)
		if err != nil {
			t.Fatal(err)
		}
		chunk, _, err := moduleapi.NewKnowledgeChunkV1(
			moduleapi.KnowledgeChunkV1{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      "knowledge-reuse-doc",
					Version: "v1",
					Digest:  strings.Repeat("a", 64),
				},
				ChunkID: "knowledge-reuse-chunk",
				Text:    "Stable greeting knowledge from the exact source.",
				VisibleTo: []moduleapi.KnowledgeScopeRuleV1{
					knowledgeBinding.Authority.AllowedScopes[0],
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, outputCanonical, _, err := moduleapi.NewKnowledgeContextOutputV1(
			moduleapi.KnowledgeContextOutputV1{
				SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
				RequestDigest: requestDigest,
				Source:        request.Source,
				Hits: []moduleapi.KnowledgeHitV1{{
					Rank:        1,
					Document:    chunk.Document,
					ChunkID:     chunk.ChunkID,
					ChunkDigest: chunk.ChunkDigest,
					Text:        chunk.Text,
					VisibleTo:   chunk.VisibleTo,
				}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if retrievedAtUnixMS == 0 {
			t.Fatal("fresh Knowledge retrieval time is zero")
		}
		knowledgeMaterial.DynamicRequestCanonical = requestCanonical
		knowledgeMaterial.DynamicOutputCanonical = outputCanonical
		knowledgeMaterial.KnowledgeProvenance =
			&corecontract.KnowledgeRetrievalProvenanceV1{
				Provider:                knowledgeBinding.Binding.Provider,
				RoutingAlgorithmVersion: knowledgecore.RoutingAlgorithmVersionV1,
				DecisionSetDigest:       decisionSetDigest,
				RetrievedAtUnixMS:       retrievedAtUnixMS,
			}
	} else {
		cloned := cloneKnowledgeReuseEvidenceForCompiler(*reuse)
		knowledgeMaterial.KnowledgeReuse = &cloned
	}
	materials[knowledgeBinding.BindingIndex] = knowledgeMaterial

	memoryConfig, found := run.FindContent(memoryBinding.Binding.ConfigRef)
	if !found {
		t.Fatal("Knowledge reuse Memory Config is absent")
	}
	memoryAuthority, found := run.FindContent(
		memoryBinding.Binding.AuthorityCeilingRef,
	)
	if !found {
		t.Fatal("Knowledge reuse Memory authority is absent")
	}
	evaluatedAtUnixMS := uint64(time.Now().UTC().UnixMilli())
	candidates, resolved, err := memorycore.FilterCandidates(
		head.Snapshot,
		head.SnapshotRef,
		memoryBinding.Scope,
		memoryBinding.Config,
		memoryBinding.Authority,
		evaluatedAtUnixMS,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, memoryRequestCanonical, memoryRequestDigest, err :=
		moduleapi.NewMemoryContextRequestV1(
			moduleapi.MemoryContextRequestV1{
				SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
				Snapshot:          head.SnapshotRef,
				Scope:             memoryBinding.Scope,
				QueryText:         task.Text,
				EvaluatedAtUnixMS: evaluatedAtUnixMS,
				Candidates:        candidates,
				MaxItems:          resolved.MaxItems,
				MaxTotalTextBytes: resolved.MaxTotalTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	_, memoryOutputCanonical, _, err := moduleapi.NewMemoryContextOutputV1(
		moduleapi.MemoryContextOutputV1{
			SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest:        memoryRequestDigest,
			Snapshot:             head.SnapshotRef,
			SelectedEntryDigests: []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	materials[memoryBinding.BindingIndex] = contextcompiler.BindingMaterialV1{
		ConfigCanonical:         memoryConfig.CanonicalBytes,
		AuthorityCanonical:      memoryAuthority.CanonicalBytes,
		StaticContextCanonicals: [][]byte{},
		DynamicStateCanonical:   head.CanonicalBytes,
		DynamicRequestCanonical: memoryRequestCanonical,
		DynamicOutputCanonical:  memoryOutputCanonical,
	}

	policyRecord, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found {
		t.Fatal("Knowledge reuse Context Policy is absent")
	}
	modelBinding, err := exactModelBinding(run.Member)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig, err := frozenModelBindingConfig(run, modelBinding)
	if err != nil {
		t.Fatal(err)
	}
	history, conversationHistory := dynamicContextCompilerHistoryForTest(t, run)
	compiled, err := contextcompiler.CompileV1(
		contextcompiler.CompileInputV1{
			TenantID:                       run.Manifest.TenantID,
			WorkspaceScope:                 run.Member.Workspace,
			AgentScope:                     run.Member.Agent,
			ContextPolicyRef:               run.Member.ContextPolicy,
			ContextPolicyDocumentCanonical: policyRecord.CanonicalBytes,
			ModelParameters:                modelConfig.Parameters,
			ContextPlan:                    contextPlan,
			ContextBindings:                materials,
			HistoryTurns:                   history,
			ConversationHistoryTurns:       conversationHistory,
			TaskInputRef:                   run.Manifest.TaskInputRef,
			TaskInputCanonical:             taskRecord.CanonicalBytes,
		},
	)
	if err != nil {
		t.Fatalf("compile Knowledge reuse Store request: %v", err)
	}
	if compiled.Compilation == nil || len(compiled.Compilation.MemoryReads) != 1 {
		t.Fatalf("Knowledge reuse Store compilation=%+v", compiled.Compilation)
	}
	return compiled
}

func (fixture knowledgeReuseStoreFixture) exactReuseEvidence(
	t *testing.T,
) corecontract.KnowledgeReuseEvidenceV1 {
	t.Helper()
	if fixture.sourceCompiled.Compilation == nil ||
		len(fixture.sourceCompiled.Compilation.KnowledgeRetrievals) != 1 {
		t.Fatal("source fresh Knowledge retrieval is absent")
	}
	if len(fixture.currentRun.ConversationHistory) != 1 ||
		fixture.currentRun.ConversationHistory[0].SourceContextCompilation == nil {
		t.Fatalf("source Conversation closure=%+v", fixture.currentRun.ConversationHistory)
	}
	knowledgeBindings := mustKnowledgeReuseStoreBindings(t, fixture.currentRun)
	memoryBindings, err := frozenMemoryBindingsForRun(
		fixture.currentRun.Manifest,
		fixture.currentRun.Member,
		runKnowledgeContentGetter(fixture.currentRun),
	)
	if err != nil || len(memoryBindings) != 1 {
		t.Fatalf("reuse Memory Bindings=%d error=%v", len(memoryBindings), err)
	}
	taskRecord, _ := fixture.currentRun.FindContent(
		fixture.currentRun.Manifest.TaskInputRef,
	)
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	decisionSet, _, err := decideFrozenKnowledgeBindings(
		task.Text,
		knowledgeBindings,
	)
	if err != nil || len(decisionSet.Decisions) != 1 {
		t.Fatalf("reuse routing decisions=%+v error=%v", decisionSet, err)
	}
	evaluatedAtUnixMS := uint64(time.Now().UTC().UnixMilli())
	policy := knowledgeBindings[0].Config.Routing.Reuse
	proof, found, err := memorycore.SelectKnowledgeReuseCounterProofV1(
		fixture.currentHead.Snapshot,
		fixture.currentHead.SnapshotRef,
		memoryBindings[0].Scope,
		memoryBindings[0].Config,
		memoryBindings[0].Authority,
		evaluatedAtUnixMS,
		decisionSet.Decisions[0].CollectionTags,
		decisionSet.Decisions[0].MatchedTerms,
		policy.MinCategoryCount,
		policy.MinRepeatedTermCount,
	)
	if err != nil || !found {
		t.Fatalf("select exact reuse counter proof found=%t error=%v", found, err)
	}
	category, err := moduleapi.NewMemoryCandidateV1(proof.CategoryCounter)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := moduleapi.NewMemoryCandidateV1(proof.RepeatedTermCounter)
	if err != nil {
		t.Fatal(err)
	}
	source := fixture.currentRun.ConversationHistory[0]
	return corecontract.KnowledgeReuseEvidenceV1{
		SourceConversationID: fixture.conversationID,
		SourceTurnIndex:      source.TurnIndex,
		SourceRunID:          source.SourceRunID,
		SourceAttemptID:      source.SourceAttemptID,
		SourceCompilationRef: source.SourceContextCompilation.Digest,
		FreshRetrieval: fixture.sourceCompiled.Compilation.
			KnowledgeRetrievals[0],
		MemoryBindingIndex:  memoryBindings[0].BindingIndex,
		CategoryCounter:     category,
		RepeatedTermCounter: repeated,
	}
}

func mustKnowledgeReuseStoreBindings(
	t *testing.T,
	run RunForLoop,
) []frozenKnowledgeBinding {
	t.Helper()
	bindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("frozen Knowledge reuse Bindings=%d error=%v", len(bindings), err)
	}
	return bindings
}

func (fixture knowledgeReuseStoreFixture) currentRunWithHead(
	t *testing.T,
) RunForLoop {
	t.Helper()
	run, err := runWithAdditionalContent(
		fixture.currentRun,
		memoryHeadContentRecordForReuseTest(fixture.currentHead),
	)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func memoryHeadContentRecordForReuseTest(
	head AgentMemoryRevisionRecord,
) ContentRecord {
	return ContentRecord{
		Digest:         head.SnapshotRef.Digest,
		Kind:           ContentMemorySnapshot,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(head.CanonicalBytes),
		SizeBytes:      int64(len(head.CanonicalBytes)),
	}
}

func cloneConversationHistoryTurnsForReuseTest(
	input []ConversationHistoryTurnRecord,
) []ConversationHistoryTurnRecord {
	if input == nil {
		return nil
	}
	cloned := make([]ConversationHistoryTurnRecord, len(input))
	for index, entry := range input {
		entry.UserContent = cloneContentRecord(entry.UserContent)
		entry.AssistantContent = cloneContentRecord(entry.AssistantContent)
		if entry.SourceContextCompilation != nil {
			record := cloneContentRecord(*entry.SourceContextCompilation)
			entry.SourceContextCompilation = &record
		}
		cloned[index] = entry
	}
	return cloned
}

func (fixture *knowledgeReuseStoreFixture) beginAuxiliaryFreshAttempt(
	t *testing.T,
) BeginModelDispatchResult {
	t.Helper()
	intent := fixture.admission.intent
	intent.AdmissionKey = "admission-knowledge-reuse-head-advance"
	intent.ConversationTurn = nil
	intent.Deadline = time.Now().UTC().Add(2 * time.Hour).
		Truncate(time.Microsecond)
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.admission.compileInput(
		t,
		canonical,
		digest,
		"run-knowledge-reuse-head-advance",
	)
	input.Contents = []ContentInput{fixture.admission.task}
	if _, err := fixture.admission.store.CommitRunAdmission(
		context.Background(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	lease, run := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-knowledge-reuse-head-advance",
		"knowledge-reuse-head-advance-worker",
	)
	head, err := fixture.admission.store.GetCurrentAgentMemory(
		context.Background(),
		run.Manifest.TenantID,
		run.Member.Agent.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled := fixture.compileForRun(
		t,
		run,
		head,
		nil,
		uint64(time.Now().UTC().UnixMilli()),
	)
	begin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(
			run,
			lease,
			"attempt-knowledge-reuse-head-advance",
			compiled,
		),
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("begin auxiliary Memory head advance=%+v error=%v", begin, err)
	}
	return begin
}

func (fixture *knowledgeReuseStoreFixture) completeAttempt(
	t *testing.T,
	begin BeginModelDispatchResult,
) {
	t.Helper()
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.admission.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatalf("complete auxiliary Memory head advance: %v", err)
	}
}

func (fixture *knowledgeReuseStoreFixture) revokeCurrentProvider(
	t *testing.T,
	instanceID string,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.admission.controlCanonical,
		fixture.admission.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.admission.catalogCanonical,
		fixture.admission.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	removedBinding := false
	for profileIndex := range control.Profiles {
		bindings := control.Profiles[profileIndex].Bindings[:0]
		for _, binding := range control.Profiles[profileIndex].Bindings {
			if binding.InstanceID == instanceID {
				removedBinding = true
				continue
			}
			bindings = append(bindings, binding)
		}
		control.Profiles[profileIndex].Bindings = append(
			[]controlcontract.BindingSpec(nil),
			bindings...,
		)
	}
	entries := catalog.Entries[:0]
	removedEntry := false
	for _, entry := range catalog.Entries {
		if entry.Activation.InstanceID == instanceID {
			removedEntry = true
			continue
		}
		entries = append(entries, entry)
	}
	catalog.Entries = append([]controlcontract.CatalogEntry(nil), entries...)
	if !removedBinding || !removedEntry {
		t.Fatalf("Provider %q was not present in current publication", instanceID)
	}

	suffix := strings.ReplaceAll(instanceID, ".", "-")
	control.SnapshotID += "-without-" + suffix
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID += "-without-" + suffix
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := fixture.admission.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.admission.basis.PointerRevision,
			NewPointerRevision:      fixture.admission.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish current Provider revocation: %v", err)
	}
	fixture.admission.basis = basis
	fixture.admission.controlCanonical = controlCanonical
	fixture.admission.catalogCanonical = catalogCanonical
}

type modelDispatchAtomicFootprintReuseTest struct {
	Attempts      int
	Usage         int
	Pending       int
	Events        int
	FrameRevision int64
	Step          string
	PendingID     sql.NullString
}

func modelDispatchAtomicFootprintForReuseTest(
	t *testing.T,
	store *Store,
	runID string,
) modelDispatchAtomicFootprintReuseTest {
	t.Helper()
	var footprint modelDispatchAtomicFootprintReuseTest
	if err := store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM model_usage WHERE run_id=?),
			(SELECT COUNT(*) FROM model_dispatch_attempts
			 WHERE run_id=? AND state='PENDING'),
			(SELECT COUNT(*) FROM run_events WHERE run_id=?),
			frame_revision,
			step,
			pending_attempt_id
		FROM loop_frames
		WHERE run_id=?
	`, runID, runID, runID, runID, runID).Scan(
		&footprint.Attempts,
		&footprint.Usage,
		&footprint.Pending,
		&footprint.Events,
		&footprint.FrameRevision,
		&footprint.Step,
		&footprint.PendingID,
	); err != nil {
		t.Fatal(err)
	}
	return footprint
}

func requireOneReuseTestMutation(
	t *testing.T,
	result sql.Result,
	err error,
) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("physical source mutation affected %d rows: %v", affected, err)
	}
}

func requireKnowledgeReuseTTLBoundaryForTest(
	t *testing.T,
	fixture knowledgeReuseStoreFixture,
	ttl time.Duration,
) {
	t.Helper()
	if ttl <= 0 || ttl%time.Millisecond != 0 {
		t.Fatalf("invalid Knowledge reuse TTL boundary %s", ttl)
	}
	if fixture.currentCompiled.Compilation == nil {
		t.Fatal("Knowledge reuse compilation is absent")
	}
	run := fixture.currentRunWithHead(t)
	knowledgeBindings := mustKnowledgeReuseStoreBindings(t, run)
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		t.Fatalf("frozen Memory reuse Bindings: %v", err)
	}
	expiresAt := fixture.sourceRetrievedAtUnixMS + uint64(ttl/time.Millisecond)
	if err := validateKnowledgeReusesForRunAtEvaluationTime(
		*fixture.currentCompiled.Compilation,
		run,
		knowledgeBindings,
		memoryBindings,
		expiresAt-1,
	); err != nil {
		t.Fatalf("Knowledge reuse expired before its frozen TTL boundary: %v", err)
	}
	err = validateKnowledgeReusesForRunAtEvaluationTime(
		*fixture.currentCompiled.Compilation,
		run,
		knowledgeBindings,
		memoryBindings,
		expiresAt,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"exact reuse evaluation fell back to fresh retrieval",
	) {
		t.Fatalf("Knowledge reuse at its frozen TTL boundary error=%v", err)
	}
}
