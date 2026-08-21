package exactadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestDeterministicKnowledgeRanksFiltersAndIsByteStable(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDeterministicKnowledge: %v", err)
	}
	requestCanonical := knowledgeRequestCanonical(
		t,
		sourceRef,
		scope,
		"Go agent",
		3,
		moduleapi.MaxKnowledgeTotalTextBytesV1,
	)
	prepared := knowledgePrepared(provider, requestCanonical)

	first, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke first: %v", err)
	}
	second, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke second: %v", err)
	}
	if first.Provider != provider || first.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("first result identity=%+v", first)
	}
	if len(first.UsageReceipt) != 0 {
		t.Fatalf("UsageReceipt=%s, want absent", first.UsageReceipt)
	}
	if !bytes.Equal(first.Output, second.Output) {
		t.Fatalf("same input produced different bytes\nfirst=%s\nsecond=%s", first.Output, second.Output)
	}
	output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(first.Output)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextOutputV1: %v", err)
	}
	if len(output.Hits) != 2 {
		t.Fatalf("hits=%d, want 2: %+v", len(output.Hits), output.Hits)
	}
	if output.Hits[0].ChunkID != "go-many" ||
		output.Hits[1].ChunkID != "agent-one" {
		t.Fatalf("ranked chunks=%q, %q", output.Hits[0].ChunkID, output.Hits[1].ChunkID)
	}
	if err := moduleapi.ValidateKnowledgeContextOutputForRequestV1(
		mustRestoreKnowledgeRequest(t, requestCanonical),
		output,
	); err != nil {
		t.Fatalf("ValidateKnowledgeContextOutputForRequestV1: %v", err)
	}
}

func TestDeterministicKnowledgeZeroHitSucceeds(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDeterministicKnowledge: %v", err)
	}
	result, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(
			provider,
			knowledgeRequestCanonical(t, sourceRef, scope, "absent-term", 4, 1024),
		),
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(result.Output)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextOutputV1: %v", err)
	}
	if output.Hits == nil || len(output.Hits) != 0 {
		t.Fatalf("Hits=%+v, want explicit empty array", output.Hits)
	}
}

func TestDeterministicKnowledgeUsesStableIdentityTieBreak(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDeterministicKnowledge: %v", err)
	}
	result, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(
			provider,
			knowledgeRequestCanonical(t, sourceRef, scope, "agent", 3, 1024),
		),
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(result.Output)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextOutputV1: %v", err)
	}
	if len(output.Hits) != 2 ||
		output.Hits[0].ChunkID != "agent-one" ||
		output.Hits[1].ChunkID != "go-many" {
		t.Fatalf("tie-broken chunks=%+v", output.Hits)
	}
}

func TestDeterministicKnowledgeHonorsHitAndTextLimits(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDeterministicKnowledge: %v", err)
	}

	limitedHits, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(
			provider,
			knowledgeRequestCanonical(t, sourceRef, scope, "Go agent", 1, 1024),
		),
	)
	if err != nil {
		t.Fatalf("Invoke hit limit: %v", err)
	}
	output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(limitedHits.Output)
	if err != nil || len(output.Hits) != 1 || output.Hits[0].ChunkID != "go-many" {
		t.Fatalf("hit-limited output=%+v, err=%v", output, err)
	}

	limitedBytes, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(
			provider,
			knowledgeRequestCanonical(t, sourceRef, scope, "Go agent", 3, 20),
		),
	)
	if err != nil {
		t.Fatalf("Invoke byte limit: %v", err)
	}
	output, _, err = moduleapi.RestoreKnowledgeContextOutputV1(limitedBytes.Output)
	if err != nil {
		t.Fatalf("Restore byte-limited output: %v", err)
	}
	var total int
	for _, hit := range output.Hits {
		total += len(hit.Text)
	}
	if len(output.Hits) != 1 || output.Hits[0].ChunkID != "go-many" || total != 20 {
		t.Fatalf("byte-limited output=%+v, selected bytes=%d", output, total)
	}
}

func TestDeterministicKnowledgeRejectsWrongProviderSourcePortAndUnknownField(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDeterministicKnowledge: %v", err)
	}
	validRequest := knowledgeRequestCanonical(t, sourceRef, scope, "agent", 2, 1024)

	wrongProvider := provider
	wrongProvider.Version = "2.0.0"
	_, err = adapter.Invoke(
		context.Background(),
		knowledgePrepared(wrongProvider, validRequest),
	)
	if !errors.Is(err, ErrKnowledgeInvocation) {
		t.Fatalf("wrong Provider error=%v", err)
	}

	wrongSource := sourceRef
	wrongSource.Digest = strings.Repeat("9", moduleapi.SHA256HexLength)
	_, err = adapter.Invoke(
		context.Background(),
		knowledgePrepared(
			provider,
			knowledgeRequestCanonical(t, wrongSource, scope, "agent", 2, 1024),
		),
	)
	if !errors.Is(err, ErrKnowledgeInvocation) {
		t.Fatalf("wrong source error=%v", err)
	}

	wrongPort := knowledgePrepared(provider, validRequest)
	wrongPort.Invocation.Port = moduleapi.PortRef{
		Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV1,
	}
	_, err = adapter.Invoke(context.Background(), wrongPort)
	if !errors.Is(err, ErrKnowledgeInvocation) {
		t.Fatalf("wrong Port error=%v", err)
	}

	var requestObject map[string]any
	if err := json.Unmarshal(validRequest, &requestObject); err != nil {
		t.Fatal(err)
	}
	requestObject["unknown"] = true
	encoded, err := json.Marshal(requestObject)
	if err != nil {
		t.Fatal(err)
	}
	unknownCanonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Invoke(
		context.Background(),
		knowledgePrepared(provider, unknownCanonical),
	)
	if !errors.Is(err, ErrKnowledgeInvocation) {
		t.Fatalf("unknown request field error=%v", err)
	}
}

func TestDeterministicKnowledgeSharesArtifactAdapterAcrossInstances(t *testing.T) {
	first, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	adapter, err := NewDeterministicKnowledge(first, sourceCanonical)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.InstanceID = "knowledge-secondary"
	second.ActivationRevision = 7
	request := knowledgeRequestCanonical(
		t,
		sourceRef,
		scope,
		"agent",
		2,
		1024,
	)

	for _, provider := range []moduleapi.ActivatedModuleRef{first, second} {
		result, err := adapter.Invoke(
			context.Background(),
			knowledgePrepared(provider, request),
		)
		if err != nil {
			t.Fatalf("Invoke(%s) error = %v", provider.InstanceID, err)
		}
		if result.Provider != provider {
			t.Fatalf(
				"Invoke(%s) Provider = %+v",
				provider.InstanceID,
				result.Provider,
			)
		}
	}
}

func TestNewDeterministicKnowledgeRejectsUntrustedClassAndUnknownSourceField(t *testing.T) {
	provider, sourceCanonical, _, _ := knowledgeFixture(t)
	provider.ExecutionClass = moduleapi.ExecutionRemote
	if _, err := NewDeterministicKnowledge(provider, sourceCanonical); !errors.Is(err, ErrInvalidKnowledgeProvider) {
		t.Fatalf("REMOTE provider error=%v", err)
	}

	var sourceObject map[string]any
	if err := json.Unmarshal(sourceCanonical, &sourceObject); err != nil {
		t.Fatal(err)
	}
	sourceObject["unknown"] = true
	encoded, err := json.Marshal(sourceObject)
	if err != nil {
		t.Fatal(err)
	}
	unknownCanonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	provider.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	if _, err := NewDeterministicKnowledge(provider, unknownCanonical); !errors.Is(err, ErrInvalidKnowledgeProvider) {
		t.Fatalf("unknown source field error=%v", err)
	}
}

func knowledgeFixture(
	t *testing.T,
) (
	moduleapi.ActivatedModuleRef,
	[]byte,
	moduleapi.KnowledgeSourceRefV1,
	moduleapi.KnowledgeQueryScopeV1,
) {
	t.Helper()
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.knowledge.deterministic",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
		InstanceID:         "knowledge-main",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "knowledge-deterministic-v1",
		ActivationRevision: 1,
	}
	scope := moduleapi.KnowledgeQueryScopeV1{
		TenantID: "tenant-one",
		Workspace: moduleapi.KnowledgeObjectRefV1{
			ID: "workspace-main", Version: "1", Digest: strings.Repeat("b", moduleapi.SHA256HexLength),
		},
		Agent: moduleapi.KnowledgeObjectRefV1{
			ID: "agent-main", Version: "1", Digest: strings.Repeat("c", moduleapi.SHA256HexLength),
		},
		TaskInputRef: strings.Repeat("d", moduleapi.SHA256HexLength),
	}
	visible := moduleapi.KnowledgeScopeRuleV1{
		TenantID: "tenant-one", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
	}
	hidden := moduleapi.KnowledgeScopeRuleV1{
		TenantID: "tenant-two", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
	}
	document := func(id, digest string) moduleapi.KnowledgeDocumentRefV1 {
		return moduleapi.KnowledgeDocumentRefV1{ID: id, Version: "1", Digest: digest}
	}
	chunks := []moduleapi.KnowledgeChunkV1{
		{Document: document("doc-go", strings.Repeat("1", 64)), ChunkID: "go-many", Text: "Go Go builds agents.", VisibleTo: []moduleapi.KnowledgeScopeRuleV1{visible}},
		{Document: document("doc-agent", strings.Repeat("2", 64)), ChunkID: "agent-one", Text: "An agent plans work.", VisibleTo: []moduleapi.KnowledgeScopeRuleV1{visible}},
		{Document: document("doc-hidden", strings.Repeat("3", 64)), ChunkID: "hidden", Text: "Go agent secret.", VisibleTo: []moduleapi.KnowledgeScopeRuleV1{hidden}},
	}
	for index := range chunks {
		var err error
		chunks[index], _, err = moduleapi.NewKnowledgeChunkV1(chunks[index])
		if err != nil {
			t.Fatalf("NewKnowledgeChunkV1(%d): %v", index, err)
		}
	}
	_, canonical, ref, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            "shared-core",
			Version:       "2026.08.03",
			Chunks:        chunks,
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1: %v", err)
	}
	return provider, canonical, ref, scope
}

func knowledgeRequestCanonical(
	t *testing.T,
	source moduleapi.KnowledgeSourceRefV1,
	scope moduleapi.KnowledgeQueryScopeV1,
	query string,
	maxHits uint32,
	maxBytes uint32,
) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewKnowledgeContextRequestV1(
		moduleapi.KnowledgeContextRequestV1{
			SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
			Source:            source,
			Scope:             scope,
			QueryText:         query,
			MaxHits:           maxHits,
			MaxTotalTextBytes: maxBytes,
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeContextRequestV1: %v", err)
	}
	return canonical
}

func knowledgePrepared(
	provider moduleapi.ActivatedModuleRef,
	requestCanonical []byte,
) modulehost.PreparedInvocation {
	return modulehost.PreparedInvocation{
		Invocation: modulehost.ModuleInvocation{
			Port:  contextProvidePortV1,
			Input: append([]byte(nil), requestCanonical...),
		},
		Binding: moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           strings.Repeat("e", moduleapi.SHA256HexLength),
			AuthorityCeilingRef: strings.Repeat("f", moduleapi.SHA256HexLength),
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	}
}

func mustRestoreKnowledgeRequest(
	t *testing.T,
	canonical []byte,
) moduleapi.KnowledgeContextRequestV1 {
	t.Helper()
	request, _, err := moduleapi.RestoreKnowledgeContextRequestV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextRequestV1: %v", err)
	}
	return request
}
