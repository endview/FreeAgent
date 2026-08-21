package currentstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type admittedKnowledgeFixture struct {
	admission      *admissionCommitFixture
	source         moduleapi.KnowledgeSourceRefV1
	contextConfig  []byte
	authority      []byte
	contextPort    moduleapi.PortRef
	authorityValue moduleapi.KnowledgeAuthorityCeilingV1
}

func TestPublishRejectsDeclarativeContextPlanWithoutStaticRefs(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
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
	control.SnapshotID = "control-empty-declarative"
	control.Revision++
	for profileIndex := range control.Profiles {
		for bindingIndex := range control.Profiles[profileIndex].Bindings {
			binding := &control.Profiles[profileIndex].Bindings[bindingIndex]
			if binding.Port.Name == moduleapi.PortNameContextProvide &&
				binding.Port.ExactVersion == moduleapi.PortVersionV1 {
				binding.StaticContextRefs = []string{}
			}
		}
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-empty-declarative"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	before := publicationRowCounts(t, fixture.store)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("empty DECLARATIVE context plan publication error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != before {
		t.Fatalf("invalid DECLARATIVE publication wrote rows before=%v after=%v", before, after)
	}
}

func TestDynamicKnowledgePublicationAdmissionPendingRecovery(t *testing.T) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	if _, err := fixture.admission.store.CommitRunAdmission(
		context.Background(),
		fixture.admission.input,
	); err != nil {
		t.Fatalf("CommitRunAdmission dynamic Knowledge: %v", err)
	}
	if _, err := fixture.admission.store.PutModelPriceSnapshot(
		context.Background(),
		testModelPriceSnapshot(),
	); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.admission.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-dynamic-knowledge",
			OwnerID:               "knowledge-loop-worker",
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
		t.Fatalf("LoadRunForLoop dynamic Knowledge: %v", err)
	}
	compiled := compileKnowledgeRequestForRun(t, fixture, run)
	manifest := run.Manifest
	input := BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   "attempt-dynamic-knowledge",
		LogicalStepID:               "reply-dynamic-knowledge",
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline: manifest.Deadline.Add(-time.Hour).
			Truncate(time.Microsecond),
	}
	begin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch dynamic Knowledge: %v", err)
	}
	if !begin.Created || !begin.InvokeAllowed ||
		begin.Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			begin.Attempt.ContextCompilation.CanonicalBytes,
			compiled.CompilationCanonical,
		) {
		t.Fatalf("dynamic Knowledge Begin result=%+v", begin)
	}
	recovered, err := fixture.admission.store.LoadRunForLoop(
		context.Background(),
		begin.Lease,
	)
	if err != nil {
		t.Fatalf("recover PENDING dynamic Knowledge: %v", err)
	}
	if len(recovered.ModelDispatches) != 1 ||
		recovered.ModelDispatches[0].Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			recovered.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes,
			compiled.CompilationCanonical,
		) {
		t.Fatalf("recovered dynamic dispatches=%+v", recovered.ModelDispatches)
	}
}

func TestFrozenKnowledgeAttemptValidationDoesNotRecompile(t *testing.T) {
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
			OwnerID:               "knowledge-frozen-proof",
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

	// These estimates remain structurally valid frozen evidence but are not
	// the exact bytes emitted by the compiler. A new Attempt must reject them;
	// a persisted Attempt read must not call CompileV1 again.
	tampered := *compiled.Compilation
	tampered.OriginalEstimateTokens++
	tampered.FinalEstimateTokens++
	_, compilationCanonical, err := corecontract.NewContextCompilationV1(tampered)
	if err != nil {
		t.Fatalf("freeze structurally valid recovery evidence: %v", err)
	}
	compilationDigest, err := ComputeContentDigest(
		ContentContextCompilation,
		admissionJSONMediaType,
		compilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	record := &ContentRecord{
		Digest:         compilationDigest,
		Kind:           ContentContextCompilation,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: compilationCanonical,
		SizeBytes:      int64(len(compilationCanonical)),
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
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
	if err := validateFrozenContextCompilationForRun(
		record,
		run,
		request,
		requestDigest,
		run.Frame.Revision,
	); err != nil {
		t.Fatalf("frozen recovery proof unexpectedly recompiled: %v", err)
	}
	if err := validateNewContextCompilationForRun(
		record,
		run,
		request,
		requestDigest,
		run.Frame.Revision,
	); err == nil || !strings.Contains(
		err.Error(),
		"exact Context Compiler output",
	) {
		t.Fatalf("new Attempt accepted non-compiler evidence: %v", err)
	}
}

func TestBeginModelDispatchRejectsKnowledgeRequestNotProducedByCompiler(
	t *testing.T,
) {
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
			OwnerID:               "knowledge-loop-worker",
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
	tamperedRequest := compiled.Request
	tamperedRequest.Messages = append(
		[]moduleapi.ModelMessageV1(nil),
		compiled.Request.Messages...,
	)
	insertAt := len(tamperedRequest.Messages) - 1
	tamperedRequest.Messages = append(
		tamperedRequest.Messages,
		moduleapi.ModelMessageV1{},
	)
	copy(
		tamperedRequest.Messages[insertAt+1:],
		tamperedRequest.Messages[insertAt:],
	)
	tamperedRequest.Messages[insertAt] = moduleapi.ModelMessageV1{
		Role:    moduleapi.ModelRoleUser,
		Content: "extra low-watermark instruction",
	}
	_, requestCanonical, err :=
		moduleapi.NewModelGenerateRequestV1(tamperedRequest)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		requestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	tamperedCompilation := *compiled.Compilation
	tamperedCompilation.FinalRequestDigest = requestDigest
	_, compilationCanonical, err := corecontract.NewContextCompilationV1(
		tamperedCompilation,
	)
	if err != nil {
		t.Fatalf("freeze structurally valid forged compilation: %v", err)
	}

	before := admissionCommitCounts(t, fixture.admission.store)
	result, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-forged-knowledge-request",
			LogicalStepID:               "reply-forged-knowledge-request",
			ContextCompilationCanonical: compilationCanonical,
			RequestCanonical:            requestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if !errors.Is(err, ErrInvalidModelDispatch) || result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() {
		t.Fatalf("forged request result=%+v error=%v", result, err)
	}
	if !strings.Contains(err.Error(), "exact Context Compiler output") {
		t.Fatalf("forged request rejected for the wrong reason: %v", err)
	}
	if after := admissionCommitCounts(t, fixture.admission.store); after != before {
		t.Fatalf("forged request wrote rows before=%v after=%v", before, after)
	}
}

func TestDynamicKnowledgeAdmissionRejectsEachScopeLevelWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*moduleapi.KnowledgeScopeRuleV1)
	}{
		{"tenant", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.TenantID = "other-tenant" }},
		{"workspace", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.WorkspaceID = "other-workspace" }},
		{"agent", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.AgentID = "other-agent" }},
		{"task", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.TaskInputRef = strings.Repeat("e", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareDynamicKnowledgeAdmission(t, test.mutate)
			before := admissionCommitCounts(t, fixture.admission.store)
			if _, err := fixture.admission.store.CommitRunAdmission(
				context.Background(),
				fixture.admission.input,
			); !errors.Is(err, ErrAdmissionIntegrity) {
				t.Fatalf("disallowed %s scope error=%v", test.name, err)
			}
			if after := admissionCommitCounts(t, fixture.admission.store); after != before {
				t.Fatalf("disallowed %s scope wrote rows before=%v after=%v", test.name, before, after)
			}
		})
	}
}

func TestBeginModelDispatchDynamicKnowledgeNilCompilationIsAtomic(
	t *testing.T,
) {
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
			OwnerID:               "knowledge-loop-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: "short request",
			}},
			Parameters: json.RawMessage(`{"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	before := admissionCommitCounts(t, fixture.admission.store)
	result, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:            lease,
			AttemptID:        "attempt-missing-compilation",
			LogicalStepID:    "reply-missing-compilation",
			RequestCanonical: requestCanonical,
			Deadline: time.Now().UTC().Add(time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if !errors.Is(err, ErrInvalidModelDispatch) || result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() {
		t.Fatalf("nil dynamic compilation result=%+v error=%v", result, err)
	}
	if after := admissionCommitCounts(t, fixture.admission.store); after != before {
		t.Fatalf("nil dynamic compilation wrote rows before=%v after=%v", before, after)
	}
	if got := contextCompilationContentCount(t, fixture.admission.store); got != 0 {
		t.Fatalf("nil dynamic compilation wrote %d compilation records", got)
	}
}

type knowledgeClosureFixture struct {
	manifest       corecontract.RunManifest
	member         corecontract.MemberExecutionSnapshot
	binding        moduleapi.PortBinding
	source         moduleapi.KnowledgeSourceRefV1
	contextConfig  ContentRecord
	authority      ContentRecord
	task           ContentRecord
	contextPolicy  ContentRecord
	authorityValue moduleapi.KnowledgeAuthorityCeilingV1
}

func TestDynamicContextBindingDefinitionRejectsInvalidShapes(t *testing.T) {
	fixture := newKnowledgeClosureFixture(t)
	port := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	contents := knowledgeFixtureContents(fixture)
	getterCalls := 0
	getter := func(digest string) (ContentRecord, error) {
		getterCalls++
		record, found := contents[digest]
		if !found {
			return ContentRecord{}, errors.New("missing content")
		}
		return cloneContentRecord(record), nil
	}

	declarative := fixture.binding
	declarative.Provider.ExecutionClass = moduleapi.ExecutionDeclarative
	declarative.StaticContextRefs = []string{strings.Repeat("a", 64)}
	declarative.FailurePolicy = moduleapi.FailureOptional
	if _, _, dynamic, err := validateContextBindingDefinitionV1(
		port,
		declarative,
		getter,
	); err != nil || dynamic || getterCalls != 0 {
		t.Fatalf(
			"declarative path=(dynamic=%v,calls=%d,error=%v), want unchanged zero-read path",
			dynamic,
			getterCalls,
			err,
		)
	}

	tests := []struct {
		name   string
		mutate func(*moduleapi.PortBinding, map[string]ContentRecord)
	}{
		{
			name: "non trusted execution",
			mutate: func(binding *moduleapi.PortBinding, _ map[string]ContentRecord) {
				binding.Provider.ExecutionClass = moduleapi.ExecutionRemote
			},
		},
		{
			name: "optional",
			mutate: func(binding *moduleapi.PortBinding, _ map[string]ContentRecord) {
				binding.FailurePolicy = moduleapi.FailureOptional
			},
		},
		{
			name: "static refs",
			mutate: func(binding *moduleapi.PortBinding, _ map[string]ContentRecord) {
				binding.StaticContextRefs = []string{strings.Repeat("a", 64)}
			},
		},
		{
			name: "wrong config schema",
			mutate: func(binding *moduleapi.PortBinding, records map[string]ContentRecord) {
				record := records[binding.ConfigRef]
				record.CanonicalBytes = []byte(`{"schema_version":"other"}`)
				records[binding.ConfigRef] = record
			},
		},
		{
			name: "wrong authority schema",
			mutate: func(binding *moduleapi.PortBinding, records map[string]ContentRecord) {
				record := records[binding.AuthorityCeilingRef]
				record.CanonicalBytes = []byte(`{"schema_version":"other"}`)
				records[binding.AuthorityCeilingRef] = record
			},
		},
		{
			name: "source mismatch",
			mutate: func(binding *moduleapi.PortBinding, records map[string]ContentRecord) {
				changed := fixture.authorityValue
				changed.Source.Digest = strings.Repeat("f", 64)
				_, canonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(changed)
				if err != nil {
					t.Fatal(err)
				}
				record := records[binding.AuthorityCeilingRef]
				record.CanonicalBytes = canonical
				records[binding.AuthorityCeilingRef] = record
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := fixture.binding
			binding.StaticContextRefs = append(
				[]string(nil),
				fixture.binding.StaticContextRefs...,
			)
			records := knowledgeFixtureContents(fixture)
			test.mutate(&binding, records)
			if _, _, _, err := validateContextBindingDefinitionV1(
				port,
				binding,
				func(digest string) (ContentRecord, error) {
					record, found := records[digest]
					if !found {
						return ContentRecord{}, errors.New("missing content")
					}
					return record, nil
				},
			); err == nil {
				t.Fatal("accepted invalid dynamic context Binding")
			}
		})
	}
}

func TestFrozenKnowledgeBindingsRequireFourLevelScopeAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*moduleapi.KnowledgeScopeRuleV1)
	}{
		{"tenant", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.TenantID = "other-tenant" }},
		{"workspace", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.WorkspaceID = "other-workspace" }},
		{"agent", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.AgentID = "other-agent" }},
		{"task", func(rule *moduleapi.KnowledgeScopeRuleV1) { rule.TaskInputRef = strings.Repeat("e", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeClosureFixture(t)
			rule := fixture.authorityValue.AllowedScopes[0]
			test.mutate(&rule)
			changed := fixture.authorityValue
			changed.AllowedScopes = []moduleapi.KnowledgeScopeRuleV1{rule}
			_, canonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(changed)
			if err != nil {
				t.Fatal(err)
			}
			fixture.authority.CanonicalBytes = canonical
			contents := knowledgeFixtureContents(fixture)
			if _, err := frozenKnowledgeBindingsForRun(
				fixture.manifest,
				fixture.member,
				func(digest string) (ContentRecord, error) {
					return contents[digest], nil
				},
			); err == nil {
				t.Fatal("accepted authority that does not allow exact Run scope")
			}
		})
	}
}

func TestDynamicKnowledgeEvidenceClosesRequestAndRejectsTampering(t *testing.T) {
	fixture := newKnowledgeClosureFixture(t)
	run := knowledgeRunForValidation(fixture)
	bindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		t.Fatal(err)
	}
	compilation, request := validKnowledgeCompilationAndRequest(
		t,
		fixture,
		bindings[0],
	)
	if err := validateKnowledgeRetrievalsForRun(
		compilation,
		run,
		request,
		bindings,
	); err != nil {
		t.Fatalf("valid Knowledge evidence: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*corecontract.ContextCompilationV1, *moduleapi.ModelGenerateRequestV1)
	}{
		{
			name: "binding index",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.KnowledgeRetrievals[0].BindingIndex++
			},
		},
		{
			name: "config ref",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.KnowledgeRetrievals[0].ConfigRef = strings.Repeat("d", 64)
			},
		},
		{
			name: "workspace scope",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.KnowledgeRetrievals[0].Scope.Workspace.ID = "other-workspace"
			},
		},
		{
			name: "request digest",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.KnowledgeRetrievals[0].RequestDigest = strings.Repeat("d", 64)
			},
		},
		{
			name: "output digest",
			mutate: func(value *corecontract.ContextCompilationV1, _ *moduleapi.ModelGenerateRequestV1) {
				value.KnowledgeRetrievals[0].OutputDigest = strings.Repeat("d", 64)
			},
		},
		{
			name: "missing final request message",
			mutate: func(_ *corecontract.ContextCompilationV1, request *moduleapi.ModelGenerateRequestV1) {
				request.Messages = request.Messages[1:]
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedCompilation := compilation
			changedCompilation.KnowledgeRetrievals = append(
				[]corecontract.KnowledgeRetrievalEvidenceV1(nil),
				compilation.KnowledgeRetrievals...,
			)
			changedRequest := request
			changedRequest.Messages = append(
				[]moduleapi.ModelMessageV1(nil),
				request.Messages...,
			)
			test.mutate(&changedCompilation, &changedRequest)
			if err := validateKnowledgeRetrievalsForRun(
				changedCompilation,
				run,
				changedRequest,
				bindings,
			); err == nil {
				t.Fatal("accepted tampered Knowledge evidence")
			}
		})
	}
}

func TestDynamicKnowledgeRequiresCompilationBelowWatermark(t *testing.T) {
	fixture := newKnowledgeClosureFixture(t)
	run := knowledgeRunForValidation(fixture)
	_, request := validKnowledgeCompilationAndRequest(
		t,
		fixture,
		mustFrozenKnowledgeBinding(t, run),
	)
	request.Messages = []moduleapi.ModelMessageV1{{
		Role: moduleapi.ModelRoleUser, Content: "short",
	}}
	_, canonical, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := moduleapi.RestoreModelGenerateRequestV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNewContextCompilationForRun(
		nil,
		run,
		restored,
		digest,
		0,
	); err == nil || !strings.Contains(err.Error(), "dynamic context.provide/v1") {
		t.Fatalf("nil low-watermark dynamic compilation error=%v", err)
	}
}

func newKnowledgeClosureFixture(t *testing.T) knowledgeClosureFixture {
	t.Helper()
	digest := func(character string) string { return strings.Repeat(character, 64) }
	manifest := corecontract.RunManifest{
		TenantID:     "tenant-a",
		TaskInputRef: digest("1"),
	}
	member := corecontract.MemberExecutionSnapshot{
		Agent: corecontract.AgentRef{
			ID: "agent-a", Version: "v1", Digest: digest("2"),
		},
		Workspace: corecontract.WorkspaceRef{
			ID: "workspace-a", Version: "v1", Digest: digest("3"),
		},
		ContextPolicy: corecontract.PolicyRef{
			ID: "context-policy", Version: "v1", Digest: digest("4"),
		},
	}
	source := moduleapi.KnowledgeSourceRefV1{
		ID: "shared.docs", Version: "v1", Digest: digest("5"),
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
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
	contextConfig := knowledgeContentRecord(
		t,
		ContentConfig,
		configCanonical,
	)
	authorityValue, authorityCanonical, err :=
		moduleapi.NewKnowledgeAuthorityCeilingV1(
			moduleapi.KnowledgeAuthorityCeilingV1{
				SchemaVersion: moduleapi.KnowledgeAuthorityCeilingSchemaV1,
				Source:        source,
				AllowedScopes: []moduleapi.KnowledgeScopeRuleV1{{
					TenantID:     manifest.TenantID,
					WorkspaceID:  member.Workspace.ID,
					AgentID:      member.Agent.ID,
					TaskInputRef: manifest.TaskInputRef,
				}},
				MaxHits:           2,
				MaxTotalTextBytes: 2048,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	authority := knowledgeContentRecord(
		t,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "What does the shared source say?",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := knowledgeContentRecord(t, ContentTaskInput, taskCanonical)
	manifest.TaskInputRef = task.Digest
	// Re-freeze authority after the real content-addressed TaskInputRef is known.
	authorityValue.AllowedScopes[0].TaskInputRef = task.Digest
	authorityValue, authorityCanonical, err =
		moduleapi.NewKnowledgeAuthorityCeilingV1(authorityValue)
	if err != nil {
		t.Fatal(err)
	}
	authority = knowledgeContentRecord(
		t,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	binding := moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           "test.knowledge",
			Version:            "v1",
			ArtifactDigest:     digest("6"),
			InstanceID:         "knowledge-instance",
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core.knowledge.adapter",
			ActivationRevision: 1,
		},
		ConfigRef:           contextConfig.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	member.PortPlans = []moduleapi.PortPlan{{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{binding},
	}}
	contextPolicy := knowledgeContextPolicyRecord(
		t,
		member.ContextPolicy.ID,
		member.ContextPolicy.Version,
	)
	member.ContextPolicy.Digest = contextPolicy.Digest
	return knowledgeClosureFixture{
		manifest:       manifest,
		member:         member,
		binding:        binding,
		source:         source,
		contextConfig:  contextConfig,
		authority:      authority,
		task:           task,
		contextPolicy:  contextPolicy,
		authorityValue: authorityValue,
	}
}

func knowledgeContentRecord(
	t *testing.T,
	kind ContentKind,
	canonical []byte,
) ContentRecord {
	t.Helper()
	digest, err := ComputeContentDigest(
		kind,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ContentRecord{
		Digest:         digest,
		Kind:           kind,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(canonical),
		SizeBytes:      int64(len(canonical)),
	}
}

func knowledgeContextPolicyRecord(
	t *testing.T,
	id string,
	version string,
) ContentRecord {
	t.Helper()
	_, body, err := corecontract.NewContextPolicyV1(
		corecontract.ContextPolicyV1{
			SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
			ContextWindowTokens:  1000,
			ReservedOutputTokens: 0,
			RecentHistoryTurns:   0,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, actualRef, canonical, err := corecontract.NewPolicyDocument(
		id,
		version,
		corecontract.PolicyContext,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	record := knowledgeContentRecord(t, ContentPolicy, canonical)
	if record.Digest != actualRef.Digest {
		t.Fatalf("policy digest=%s ref=%s", record.Digest, actualRef.Digest)
	}
	return record
}

func knowledgeFixtureContents(
	fixture knowledgeClosureFixture,
) map[string]ContentRecord {
	return map[string]ContentRecord{
		fixture.contextConfig.Digest: fixture.contextConfig,
		fixture.authority.Digest:     fixture.authority,
		fixture.task.Digest:          fixture.task,
		fixture.contextPolicy.Digest: fixture.contextPolicy,
	}
}

func knowledgeRunForValidation(
	fixture knowledgeClosureFixture,
) RunForLoop {
	contents := knowledgeFixtureContents(fixture)
	records := make([]ContentRecord, 0, len(contents))
	for _, record := range contents {
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		return records[left].Digest < records[right].Digest
	})
	return RunForLoop{
		Manifest: fixture.manifest,
		Member:   fixture.member,
		Contents: records,
	}
}

func mustFrozenKnowledgeBinding(
	t *testing.T,
	run RunForLoop,
) frozenKnowledgeBinding {
	t.Helper()
	bindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("frozen Knowledge Bindings=%d error=%v", len(bindings), err)
	}
	return bindings[0]
}

func validKnowledgeCompilationAndRequest(
	t *testing.T,
	fixture knowledgeClosureFixture,
	binding frozenKnowledgeBinding,
) (corecontract.ContextCompilationV1, moduleapi.ModelGenerateRequestV1) {
	t.Helper()
	task, err := corecontract.RestoreTaskInputV1(fixture.task.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, _, requestDigest, err :=
		moduleapi.NewKnowledgeContextRequestV1(
			moduleapi.KnowledgeContextRequestV1{
				SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
				Source:            binding.Config.Source,
				Scope:             binding.Scope,
				QueryText:         task.Text,
				MaxHits:           binding.MaxHits,
				MaxTotalTextBytes: binding.MaxTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	rule := fixture.authorityValue.AllowedScopes[0]
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(
		moduleapi.KnowledgeChunkV1{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID: "doc-a", Version: "v1", Digest: strings.Repeat("7", 64),
			},
			ChunkID:   "chunk-a",
			Text:      "Shared source answer.",
			VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	output, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: requestDigest,
			Source:        binding.Config.Source,
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
	message, err := contextcompiler.KnowledgeContextMessageV1(output)
	if err != nil {
		t.Fatal(err)
	}
	modelRequest, _, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				message,
				{Role: moduleapi.ModelRoleUser, Content: task.Text},
			},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return corecontract.ContextCompilationV1{
		KnowledgeRetrievals: []corecontract.KnowledgeRetrievalEvidenceV1{{
			BindingIndex:        binding.BindingIndex,
			ConfigRef:           binding.Binding.ConfigRef,
			AuthorityCeilingRef: binding.Binding.AuthorityCeilingRef,
			RequestDigest:       requestDigest,
			Scope:               binding.Scope,
			Source:              output.Source,
			Hits:                output.Hits,
			OutputDigest:        outputDigest,
		}},
	}, modelRequest
}

func prepareDynamicKnowledgeAdmission(
	t *testing.T,
	mutateRule func(*moduleapi.KnowledgeScopeRuleV1),
) admittedKnowledgeFixture {
	t.Helper()
	fixture := newAdmissionCommitFixture(t)
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
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	manifestBytes := canonicalModuleManifest(
		t,
		"test.knowledge",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"runtime": map[string]any{
				"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
				"entrypoint": "content/source.json",
			},
			"provides": []any{map[string]any{
				"name":          contextPort.Name,
				"exact_version": contextPort.ExactVersion,
			}},
		},
	)
	installation, err := fixture.store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-dynamic-knowledge",
			manifestBytes,
			strings.Repeat("8", 64),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := fixture.store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-dynamic-knowledge",
			TenantID:           control.TenantID,
			InstanceID:         "instance-dynamic-knowledge",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core.knowledge.adapter",
		},
	)
	if err != nil {
		t.Fatal(err)
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
	source := moduleapi.KnowledgeSourceRefV1{
		ID:      "shared.dynamic.docs",
		Version: "v1",
		Digest:  strings.Repeat("9", 64),
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
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
	rule := moduleapi.KnowledgeScopeRuleV1{
		TenantID:     control.TenantID,
		WorkspaceID:  control.Workspaces[0].Workspace.ID,
		AgentID:      control.Agents[0].ID,
		TaskInputRef: fixture.task.Digest,
	}
	if mutateRule != nil {
		mutateRule(&rule)
	}
	authorityValue, authorityCanonical, err :=
		moduleapi.NewKnowledgeAuthorityCeilingV1(
			moduleapi.KnowledgeAuthorityCeilingV1{
				SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
				Source:            source,
				AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
				MaxHits:           2,
				MaxTotalTextBytes: 2048,
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

	dynamicSpec := controlcontract.BindingSpec{
		Port:                contextPort,
		InstanceID:          provider.InstanceID,
		ConfigRef:           contextConfigRef,
		AuthorityCeilingRef: authorityRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	replaced := false
	for index, binding := range control.Profiles[0].Bindings {
		if binding.Port == contextPort {
			control.Profiles[0].Bindings[index] = dynamicSpec
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("fixture Control lacks context.provide/v1 Binding")
	}
	control.SnapshotID = "control-dynamic-knowledge"
	control.Revision = 2
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-dynamic-knowledge"
	catalog.Generation = 2
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(
		catalog.Entries,
		controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   []moduleapi.PortRef{contextPort},
		},
	)
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
		t.Fatalf("PublishControlCatalog dynamic Knowledge: %v", err)
	}
	intent := fixture.intent
	intent.AdmissionKey = "admission-dynamic-knowledge"
	intent.RequestedPorts = []moduleapi.PortRef{
		{Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV1},
		contextPort,
	}
	intent.Deadline = time.Now().UTC().Add(3 * time.Hour).
		Truncate(time.Microsecond)
	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-dynamic-knowledge",
			MemberID:         "member-primary",
			RecoveryRootRef:  "recovery/run-dynamic-knowledge",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("compile dynamic Knowledge Admission: %v", err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
	fixture.intent = intent
	fixture.input = CommitRunAdmissionInput{
		PublishedBasis:          basis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents:                []ContentInput{fixture.task},
	}
	return admittedKnowledgeFixture{
		admission:      fixture,
		source:         source,
		contextConfig:  contextConfigCanonical,
		authority:      authorityCanonical,
		contextPort:    contextPort,
		authorityValue: authorityValue,
	}
}

func compileKnowledgeRequestForRun(
	t *testing.T,
	fixture admittedKnowledgeFixture,
	run RunForLoop,
) contextcompiler.CompileResultV1 {
	t.Helper()
	var contextPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		plan := &run.Member.PortPlans[index]
		if plan.Port == fixture.contextPort {
			contextPlan = plan
			break
		}
	}
	if contextPlan == nil || len(contextPlan.Bindings) != 1 {
		t.Fatalf("dynamic context plan=%+v", contextPlan)
	}
	binding := mustFrozenKnowledgeBinding(t, run)
	taskRecord, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found {
		t.Fatal("TaskInput missing from Run closure")
	}
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	request, requestCanonical, requestDigest, err :=
		moduleapi.NewKnowledgeContextRequestV1(
			moduleapi.KnowledgeContextRequestV1{
				SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
				Source:            fixture.source,
				Scope:             binding.Scope,
				QueryText:         task.Text,
				MaxHits:           binding.MaxHits,
				MaxTotalTextBytes: binding.MaxTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(
		moduleapi.KnowledgeChunkV1{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID: "dynamic-doc", Version: "v1", Digest: strings.Repeat("a", 64),
			},
			ChunkID: "dynamic-chunk",
			Text:    "The shared dynamic source is active.",
			VisibleTo: []moduleapi.KnowledgeScopeRuleV1{
				fixture.authorityValue.AllowedScopes[0],
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
				ConfigCanonical:         fixture.contextConfig,
				AuthorityCanonical:      fixture.authority,
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
		t.Fatalf("CompileV1 dynamic Knowledge: %v", err)
	}
	if result.Compilation == nil ||
		len(result.Compilation.KnowledgeRetrievals) != 1 {
		t.Fatalf("dynamic compilation=%+v", result.Compilation)
	}
	return result
}
