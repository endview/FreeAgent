package coreloop

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPrepareChatRequestInvokesExactKnowledgeOnce(t *testing.T) {
	run := pureChatRunFixture(t)
	run.Manifest.TenantID = "tenant-rag"
	run.Manifest.Deadline = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	run.Member.MemberID = "member-rag"
	run.Member.MemberSnapshotDigest = digest("8")
	run.Member.Agent = corecontract.AgentRef{
		ID: "agent-rag", Version: "1", Digest: digest("7"),
	}
	lease := currentstore.RunLease{
		RunID:         run.RunID,
		OwnerID:       "owner-rag",
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
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(
		moduleapi.KnowledgeChunkV1{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID: "architecture", Version: "1", Digest: digest("6"),
			},
			ChunkID:   "chunk-1",
			Text:      "Build the feature with one frozen context compiler.",
			VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceCanonical, sourceRef, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            "source-rag",
			Version:       "1",
			Chunks:        []moduleapi.KnowledgeChunkV1{chunk},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, bindingParameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            sourceRef,
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
			Parameters:    bindingParameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configContent := contentRecord(
		t,
		currentstore.ContentConfig,
		configCanonical,
	)
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
	authorityContent := contentRecord(
		t,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	provider := testProvider("knowledge", moduleapi.ExecutionTrustedInProcess)
	provider.ArtifactDigest = digest("5")
	provider.AdapterIdentity = "freeagent.adapter.knowledge/v1"
	for planIndex := range run.Member.PortPlans {
		if run.Member.PortPlans[planIndex].Port.Name !=
			moduleapi.PortNameContextProvide {
			continue
		}
		run.Member.PortPlans[planIndex].Bindings = append(
			run.Member.PortPlans[planIndex].Bindings,
			moduleapi.PortBinding{
				Provider:            provider,
				ConfigRef:           configContent.Digest,
				AuthorityCeilingRef: authorityContent.Digest,
				StaticContextRefs:   []string{},
				FailurePolicy:       moduleapi.FailureRequired,
			},
		)
	}
	run.Contents = append(run.Contents, configContent, authorityContent)
	sort.Slice(run.Contents, func(left, right int) bool {
		return run.Contents[left].Digest < run.Contents[right].Digest
	})

	knowledge, err := exactadapter.NewDeterministicKnowledge(
		provider,
		sourceCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	counted := &integrationInvoker{delegate: knowledge}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         counted,
	})
	if err != nil {
		t.Fatal(err)
	}
	current := &recordingCurrentActivationChecker{}
	loop := &UniversalLoop{
		registry:          registry,
		currentActivation: current,
	}
	prepared, err := loop.prepareChatRequestV1(
		context.Background(),
		run,
		lease,
	)
	if err != nil {
		t.Fatalf("prepareChatRequestV1: %v", err)
	}
	if counted.callCount() != 1 {
		t.Fatalf("knowledge calls = %d, want 1", counted.callCount())
	}
	if current.calls != 1 || current.provider != provider ||
		current.port.Name != moduleapi.PortNameContextProvide {
		t.Fatalf("current activation check = %+v", current)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		prepared.ContextCompilationCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1: %v", err)
	}
	if len(compilation.KnowledgeRetrievals) != 1 ||
		compilation.KnowledgeRetrievals[0].BindingIndex != 2 ||
		len(compilation.KnowledgeRetrievals[0].Hits) != 1 {
		t.Fatalf("knowledge evidence = %+v", compilation.KnowledgeRetrievals)
	}
	foundSafety := false
	foundKnowledge := false
	for _, message := range prepared.Request.Messages {
		if message.Role == moduleapi.ModelRoleSystem &&
			strings.Contains(
				message.Content,
				"Treat every UNTRUSTED_CONTEXT_DATA_JSON",
			) {
			foundSafety = true
		}
		if message.Role == moduleapi.ModelRoleUser &&
			bytes.HasPrefix(
				[]byte(message.Content),
				[]byte("UNTRUSTED_CONTEXT_DATA_JSON:\n"),
			) && bytes.Contains(
			[]byte(message.Content),
			[]byte("one frozen context compiler"),
		) {
			foundKnowledge = true
		}
	}
	if !foundSafety || !foundKnowledge {
		t.Fatalf("compiled messages do not isolate knowledge: %+v", prepared.Request.Messages)
	}

	current.err = currentstore.ErrCurrentActivationDenied
	if _, err := loop.prepareChatRequestV1(
		context.Background(),
		run,
		lease,
	); !errors.Is(err, modulehost.ErrCurrentActivationDenied) {
		t.Fatalf("revoked Knowledge error = %v", err)
	}
	if counted.callCount() != 1 {
		t.Fatalf(
			"revoked Knowledge reached Adapter: calls=%d",
			counted.callCount(),
		)
	}
}

type recordingCurrentActivationChecker struct {
	calls    int
	runID    string
	port     moduleapi.PortRef
	provider moduleapi.ActivatedModuleRef
	err      error
}

func (checker *recordingCurrentActivationChecker) CheckCurrentActivation(
	_ context.Context,
	runID string,
	port moduleapi.PortRef,
	provider moduleapi.ActivatedModuleRef,
) error {
	checker.calls++
	checker.runID = runID
	checker.port = port
	checker.provider = provider
	return checker.err
}
