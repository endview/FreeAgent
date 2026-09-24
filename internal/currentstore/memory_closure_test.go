package currentstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type admittedMemoryFixture struct {
	admission      *admissionCommitFixture
	contextConfig  []byte
	authority      []byte
	contextPort    moduleapi.PortRef
	configValue    moduleapi.MemoryContextBindingV1
	authorityValue moduleapi.MemoryAuthorityCeilingV1
}

func TestDynamicMemoryBeginFreezesCurrentHeadAndRecoveryKeepsExactRevision(
	t *testing.T,
) {
	fixture := prepareDynamicMemoryAdmission(t)
	tenantID := fixture.admission.intent.TenantID
	agentID := mustRestoreMemberSnapshot(t, fixture.admission).Agent.ID
	genesisCanonical := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		[]moduleapi.MemoryEntryV1{memoryFactEntry(t, "remembered preference")},
	)
	genesis, err := fixture.admission.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}

	targetInput := fixture.admission.input
	staleInput := dynamicMemoryAdmissionInput(
		t,
		fixture,
		"run-dynamic-memory-stale",
		"admission-dynamic-memory-stale",
	)
	sourceInput := dynamicMemoryAdmissionInput(
		t,
		fixture,
		"run-dynamic-memory-source",
		"admission-dynamic-memory-source",
	)
	for _, input := range []CommitRunAdmissionInput{
		targetInput,
		staleInput,
		sourceInput,
	} {
		if _, err := fixture.admission.store.CommitRunAdmission(
			context.Background(),
			input,
		); err != nil {
			t.Fatalf("CommitRunAdmission %s: %v", inputRunID(t, input), err)
		}
	}
	head := genesis.Record
	targetLease, targetRun := acquireMemoryRun(
		t,
		fixture,
		"run-dynamic-memory",
		"memory-target-worker",
	)
	targetCompiled := compileMemoryRequestForRun(t, targetRun, head)
	targetBegin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(
			targetRun,
			targetLease,
			"attempt-dynamic-memory-target",
			targetCompiled,
		),
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch target: %v", err)
	}
	if !targetBegin.Created || !targetBegin.InvokeAllowed ||
		targetBegin.Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			targetBegin.Attempt.ContextCompilation.CanonicalBytes,
			targetCompiled.CompilationCanonical,
		) {
		t.Fatalf("target Begin=%+v", targetBegin)
	}

	staleLease, staleRun := acquireMemoryRun(
		t,
		fixture,
		"run-dynamic-memory-stale",
		"memory-stale-worker",
	)
	staleCompiled := compileMemoryRequestForRun(t, staleRun, head)
	staleBeginInput := memoryBeginInput(
		staleRun,
		staleLease,
		"attempt-dynamic-memory-stale",
		staleCompiled,
	)

	sourceLease, sourceRun := acquireMemoryRun(
		t,
		fixture,
		"run-dynamic-memory-source",
		"memory-source-worker",
	)
	sourceCompiled := compileMemoryRequestForRun(t, sourceRun, head)
	sourceBegin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(
			sourceRun,
			sourceLease,
			"attempt-dynamic-memory-source",
			sourceCompiled,
		),
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch source: %v", err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.admission.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   sourceBegin.Lease,
			AttemptID:               sourceBegin.Attempt.AttemptID,
			InvocationID:            sourceBegin.Attempt.AttemptID,
			Provider:                sourceBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: sourceBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatalf("CommitModelDispatchOutcome source: %v", err)
	}

	current, err := fixture.admission.store.GetCurrentAgentMemory(
		context.Background(),
		tenantID,
		agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.SnapshotRef.Revision != 2 {
		t.Fatalf("current Memory revision=%d, want 2", current.SnapshotRef.Revision)
	}

	recovered, err := fixture.admission.store.LoadRunForLoop(
		context.Background(),
		targetBegin.Lease,
	)
	if err != nil {
		t.Fatalf("LoadRunForLoop frozen revision 1: %v", err)
	}
	if len(recovered.ModelDispatches) != 1 {
		t.Fatalf("recovered dispatch count=%d", len(recovered.ModelDispatches))
	}
	recoveredCompilation := recovered.ModelDispatches[0].Attempt.ContextCompilation
	if recoveredCompilation == nil {
		t.Fatal("recovered Attempt has no ContextCompilation")
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		recoveredCompilation.CanonicalBytes,
	)
	if err != nil || len(compilation.MemoryReads) != 1 {
		t.Fatalf("recovered Memory evidence=%+v error=%v", compilation.MemoryReads, err)
	}
	if compilation.MemoryReads[0].Snapshot != genesis.Record.SnapshotRef {
		t.Fatalf(
			"recovered snapshot=%+v, want genesis %+v",
			compilation.MemoryReads[0].Snapshot,
			genesis.Record.SnapshotRef,
		)
	}
	content, found := recovered.FindContent(genesis.Record.SnapshotRef.Digest)
	if !found || content.Kind != ContentMemorySnapshot ||
		!bytes.Equal(content.CanonicalBytes, genesisCanonical) {
		t.Fatal("RunForLoop does not contain the exact frozen Memory snapshot")
	}
	if _, found := recovered.FindContent(current.SnapshotRef.Digest); found {
		t.Fatal("recovery drifted to the newer current Memory head")
	}

	if _, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		staleBeginInput,
	); !errors.Is(err, ErrInvalidModelDispatch) ||
		!strings.Contains(err.Error(), "current Agent Memory head") {
		t.Fatalf("stale Memory Begin error=%v", err)
	}
	var staleAttemptCount int
	if err := fixture.admission.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts WHERE attempt_id=?
	`, staleBeginInput.AttemptID).Scan(&staleAttemptCount); err != nil {
		t.Fatal(err)
	}
	if staleAttemptCount != 0 {
		t.Fatalf("stale Memory Begin persisted %d Attempts", staleAttemptCount)
	}
}

func TestNewAttemptWithoutMemoryNeverConsultsAgentMemoryStore(t *testing.T) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	if _, err := fixture.admission.store.CommitRunAdmission(
		context.Background(),
		fixture.admission.input,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.admission.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-dynamic-knowledge",
			OwnerID:               "knowledge-zero-memory-read",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.admission.store.LoadRunForLoop(
		context.Background(),
		lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled := compileKnowledgeRequestForRun(t, fixture, run)
	record := memoryCompilationRecord(t, compiled.CompilationCanonical)
	restoredRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		compiled.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		compiled.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	// A nil queryer would panic if the no-Memory branch performed even one
	// Agent Memory query. Dynamic Knowledge still receives full validation.
	if err := validateNewContextCompilationForRunAtCurrentHead(
		context.Background(),
		nil,
		&record,
		run,
		restoredRequest,
		requestDigest,
		0,
	); err != nil {
		t.Fatalf("Knowledge-only new Attempt: %v", err)
	}
}

func TestMemoryEvidenceRecomputesRequestOutputAndPrompt(t *testing.T) {
	fixture := prepareDynamicMemoryAdmission(t)
	tenantID := fixture.admission.intent.TenantID
	agentID := mustRestoreMemberSnapshot(t, fixture.admission).Agent.ID
	genesisCanonical := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		[]moduleapi.MemoryEntryV1{memoryFactEntry(t, "closed evidence")},
	)
	genesis, err := fixture.admission.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.admission.store.CommitRunAdmission(
		context.Background(),
		fixture.admission.input,
	); err != nil {
		t.Fatal(err)
	}
	lease, run := acquireMemoryRun(
		t,
		fixture,
		"run-dynamic-memory",
		"memory-evidence-worker",
	)
	_ = lease
	compiled := compileMemoryRequestForRun(t, run, genesis.Record)
	snapshotContent, err := fixture.admission.store.GetContent(
		context.Background(),
		genesis.Record.SnapshotRef.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err = runWithAdditionalContent(run, snapshotContent)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		compiled.CompilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		compiled.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMemoryReadsForRun(
		compilation,
		run,
		request,
		bindings,
	); err != nil {
		t.Fatalf("valid Memory evidence: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*corecontract.ContextCompilationV1, *moduleapi.ModelGenerateRequestV1)
	}{
		{
			name: "request digest",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.MemoryReads[0].RequestDigest = strings.Repeat("d", 64)
			},
		},
		{
			name: "output digest",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.MemoryReads[0].OutputDigest = strings.Repeat("e", 64)
			},
		},
		{
			name: "selected entry",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.MemoryReads[0].SelectedEntries[0].Text = "tampered"
			},
		},
		{
			name: "missing prompt envelope",
			mutate: func(_ *corecontract.ContextCompilationV1, request *moduleapi.ModelGenerateRequestV1) {
				request.Messages = append(
					[]moduleapi.ModelMessageV1(nil),
					request.Messages[2:]...,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedCompilation := compilation
			changedCompilation.MemoryReads = append(
				[]corecontract.MemoryReadEvidenceV1(nil),
				compilation.MemoryReads...,
			)
			changedCompilation.MemoryReads[0].SelectedEntries = append(
				[]moduleapi.MemoryCandidateV1(nil),
				compilation.MemoryReads[0].SelectedEntries...,
			)
			changedRequest := request
			changedRequest.Messages = append(
				[]moduleapi.ModelMessageV1(nil),
				request.Messages...,
			)
			test.mutate(&changedCompilation, &changedRequest)
			if err := validateMemoryReadsForRun(
				changedCompilation,
				run,
				changedRequest,
				bindings,
			); err == nil {
				t.Fatal("accepted tampered Memory closure")
			}
		})
	}
}

func TestFrozenMemoryBindingsRequireExactScopeAndAtMostOne(t *testing.T) {
	fixture := prepareDynamicMemoryAdmission(t)
	manifest, err := corecontract.RestoreRunManifest(
		fixture.admission.input.RunManifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	member := mustRestoreMemberSnapshot(t, fixture.admission)
	getter := func(digest string) (ContentRecord, error) {
		return fixture.admission.store.GetContent(context.Background(), digest)
	}
	if bindings, err := frozenMemoryBindingsForRun(
		manifest,
		member,
		getter,
	); err != nil || len(bindings) != 1 {
		t.Fatalf("valid frozen Memory Bindings=%d error=%v", len(bindings), err)
	}

	tests := []struct {
		name   string
		mutate func(*corecontract.RunManifest, *corecontract.MemberExecutionSnapshot)
	}{
		{
			name: "tenant",
			mutate: func(manifest *corecontract.RunManifest, _ *corecontract.MemberExecutionSnapshot) {
				manifest.TenantID = "other-tenant"
			},
		},
		{
			name: "agent",
			mutate: func(_ *corecontract.RunManifest, member *corecontract.MemberExecutionSnapshot) {
				member.Agent.ID = "other-agent"
			},
		},
		{
			name: "workspace",
			mutate: func(_ *corecontract.RunManifest, member *corecontract.MemberExecutionSnapshot) {
				member.Workspace.ID = "other-workspace"
			},
		},
		{
			name: "workspace wildcard sentinel",
			mutate: func(_ *corecontract.RunManifest, member *corecontract.MemberExecutionSnapshot) {
				member.Workspace.ID = "*"
			},
		},
		{
			name: "agent wildcard sentinel",
			mutate: func(_ *corecontract.RunManifest, member *corecontract.MemberExecutionSnapshot) {
				member.Agent.ID = "*"
			},
		},
		{
			name: "second Memory Binding",
			mutate: func(_ *corecontract.RunManifest, member *corecontract.MemberExecutionSnapshot) {
				for index := range member.PortPlans {
					if member.PortPlans[index].Port.Name == moduleapi.PortNameContextProvide {
						member.PortPlans[index].Bindings = append(
							member.PortPlans[index].Bindings,
							member.PortPlans[index].Bindings[0],
						)
					}
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedManifest := manifest
			changedMember := member
			changedMember.PortPlans = cloneLoopPortPlans(member.PortPlans)
			test.mutate(&changedManifest, &changedMember)
			if _, err := frozenMemoryBindingsForRun(
				changedManifest,
				changedMember,
				getter,
			); err == nil {
				t.Fatal("accepted invalid frozen Memory scope or cardinality")
			}
		})
	}
}

func prepareDynamicMemoryAdmission(t *testing.T) admittedMemoryFixture {
	t.Helper()
	base := prepareDynamicKnowledgeAdmission(t, nil)
	fixture := base.admission
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
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
	configValue, parameters, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion:     moduleapi.MemoryContextBindingSchemaV1,
			Kinds:             []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
			MaxItems:          4,
			MaxTotalTextBytes: 4096,
			CategoryRules:     []moduleapi.MemoryCategoryRuleV1{},
			StopTerms:         []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, contextConfigCanonical, err := moduleapi.NewContextBindingConfigV1(
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
	contextConfigRef := putPublicationJSON(
		t,
		fixture.store,
		ContentConfig,
		contextConfigCanonical,
	)
	authorityValue, authorityCanonical, err :=
		moduleapi.NewMemoryAuthorityCeilingV1(
			moduleapi.MemoryAuthorityCeilingV1{
				SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
				TenantID:            control.TenantID,
				AgentID:             control.Agents[0].ID,
				AllowedWorkspaceIDs: []string{control.Workspaces[0].Workspace.ID},
				AllowedKinds:        []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
				MaxItems:            2,
				MaxTotalTextBytes:   2048,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	authorityRef := putPublicationJSON(
		t,
		fixture.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	for profileIndex := range control.Profiles {
		for bindingIndex := range control.Profiles[profileIndex].Bindings {
			binding := &control.Profiles[profileIndex].Bindings[bindingIndex]
			if binding.Port == base.contextPort {
				binding.ConfigRef = contextConfigRef
				binding.AuthorityCeilingRef = authorityRef
			}
		}
	}
	control.SnapshotID = "control-dynamic-memory"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-dynamic-memory"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("PublishControlCatalog dynamic Memory: %v", err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
	fixture.intent.AdmissionKey = "admission-dynamic-memory"
	fixture.intent.Deadline = time.Now().UTC().Add(3 * time.Hour).
		Truncate(time.Microsecond)
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(fixture.intent)
	if err != nil {
		t.Fatal(err)
	}
	fixture.input = fixture.compileInput(
		t,
		intentCanonical,
		intentDigest,
		"run-dynamic-memory",
	)
	fixture.input.Contents = []ContentInput{fixture.task}
	return admittedMemoryFixture{
		admission:      fixture,
		contextConfig:  contextConfigCanonical,
		authority:      authorityCanonical,
		contextPort:    base.contextPort,
		configValue:    configValue,
		authorityValue: authorityValue,
	}
}

func dynamicMemoryAdmissionInput(
	t *testing.T,
	fixture admittedMemoryFixture,
	runID string,
	admissionKey string,
) CommitRunAdmissionInput {
	t.Helper()
	intent := fixture.admission.intent
	intent.AdmissionKey = admissionKey
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.admission.compileInput(t, canonical, digest, runID)
	input.Contents = []ContentInput{fixture.admission.task}
	return input
}

func acquireMemoryRun(
	t *testing.T,
	fixture admittedMemoryFixture,
	runID string,
	ownerID string,
) (RunLease, RunForLoop) {
	t.Helper()
	lease, err := fixture.admission.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               ownerID,
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.admission.store.LoadRunForLoop(
		context.Background(),
		lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	return lease, run
}

func compileMemoryRequestForRun(
	t *testing.T,
	run RunForLoop,
	head AgentMemoryRevisionRecord,
) contextcompiler.CompileResultV1 {
	t.Helper()
	bindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("frozen Memory Bindings=%d error=%v", len(bindings), err)
	}
	binding := bindings[0]
	taskRecord, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found {
		t.Fatal("TaskInput missing from Run closure")
	}
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	evaluatedAt := uint64(1000)
	candidates, resolved, err := memorycore.FilterCandidates(
		head.Snapshot,
		head.SnapshotRef,
		binding.Scope,
		binding.Config,
		binding.Authority,
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, requestDigest, err :=
		moduleapi.NewMemoryContextRequestV1(
			moduleapi.MemoryContextRequestV1{
				SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
				Snapshot:          head.SnapshotRef,
				Scope:             binding.Scope,
				QueryText:         task.Text,
				EvaluatedAtUnixMS: evaluatedAt,
				Candidates:        candidates,
				MaxItems:          resolved.MaxItems,
				MaxTotalTextBytes: resolved.MaxTotalTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	selectedDigests := make([]string, len(candidates))
	for index, candidate := range candidates {
		selectedDigests[index] = candidate.EntryDigest
	}
	_, outputCanonical, _, err := moduleapi.NewMemoryContextOutputV1(
		moduleapi.MemoryContextOutputV1{
			SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest:        requestDigest,
			Snapshot:             head.SnapshotRef,
			SelectedEntryDigests: selectedDigests,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var contextPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		if run.Member.PortPlans[index].Port.Name == moduleapi.PortNameContextProvide &&
			run.Member.PortPlans[index].Port.ExactVersion == moduleapi.PortVersionV1 {
			contextPlan = &run.Member.PortPlans[index]
			break
		}
	}
	if contextPlan == nil {
		t.Fatal("Memory context plan is absent")
	}
	configRecord, found := run.FindContent(binding.Binding.ConfigRef)
	if !found {
		t.Fatal("Memory Config is absent")
	}
	authorityRecord, found := run.FindContent(binding.Binding.AuthorityCeilingRef)
	if !found {
		t.Fatal("Memory authority is absent")
	}
	policyRecord, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found {
		t.Fatal("ContextPolicy missing from Run closure")
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
	result, err := contextcompiler.CompileV1(
		contextcompiler.CompileInputV1{
			TenantID:                       run.Manifest.TenantID,
			WorkspaceScope:                 run.Member.Workspace,
			AgentScope:                     run.Member.Agent,
			ContextPolicyRef:               run.Member.ContextPolicy,
			ContextPolicyDocumentCanonical: policyRecord.CanonicalBytes,
			ModelParameters:                modelConfig.Parameters,
			ContextPlan:                    contextPlan,
			ContextBindings: []contextcompiler.BindingMaterialV1{{
				ConfigCanonical:         configRecord.CanonicalBytes,
				AuthorityCanonical:      authorityRecord.CanonicalBytes,
				DynamicStateCanonical:   head.CanonicalBytes,
				DynamicRequestCanonical: requestCanonical,
				DynamicOutputCanonical:  outputCanonical,
			}},
			HistoryTurns:             history,
			ConversationHistoryTurns: conversationHistory,
			TaskInputRef:             run.Manifest.TaskInputRef,
			TaskInputCanonical:       taskRecord.CanonicalBytes,
		},
	)
	if err != nil {
		t.Fatalf("CompileV1 dynamic Memory: %v", err)
	}
	if result.Compilation == nil || len(result.Compilation.MemoryReads) != 1 {
		t.Fatalf("dynamic Memory compilation=%+v", result.Compilation)
	}
	return result
}

func memoryBeginInput(
	run RunForLoop,
	lease RunLease,
	attemptID string,
	compiled contextcompiler.CompileResultV1,
) BeginModelDispatchInput {
	return BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   attemptID,
		LogicalStepID:               "reply-" + attemptID,
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline: run.Manifest.Deadline.Add(-time.Hour).
			Truncate(time.Microsecond),
	}
}

func inputRunID(t *testing.T, input CommitRunAdmissionInput) string {
	t.Helper()
	manifest, err := corecontract.RestoreRunManifest(input.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.RunID
}

func memoryCompilationRecord(t *testing.T, canonical []byte) ContentRecord {
	t.Helper()
	digest, err := ComputeContentDigest(
		ContentContextCompilation,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ContentRecord{
		Digest:         digest,
		Kind:           ContentContextCompilation,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(canonical),
		SizeBytes:      int64(len(canonical)),
	}
}
