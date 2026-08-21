package exactadapter

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestDeterministicMemorySelectsBoundedCandidatesAndIsByteStable(t *testing.T) {
	provider := memoryProvider()
	adapter, err := NewDeterministicMemory(provider)
	if err != nil {
		t.Fatalf("NewDeterministicMemory: %v", err)
	}
	requestCanonical := memoryRequestCanonical(t, 2, 128)
	prepared := knowledgePrepared(provider, requestCanonical)
	first, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke first: %v", err)
	}
	second, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke second: %v", err)
	}
	if first.Provider != provider || first.Outcome != modulehost.InvocationSucceeded ||
		len(first.UsageReceipt) != 0 {
		t.Fatalf("result=%+v", first)
	}
	if !bytes.Equal(first.Output, second.Output) {
		t.Fatalf("same request produced different output\n%s\n%s", first.Output, second.Output)
	}
	output, _, err := moduleapi.RestoreMemoryContextOutputV1(first.Output)
	if err != nil {
		t.Fatalf("RestoreMemoryContextOutputV1: %v", err)
	}
	if len(output.SelectedEntryDigests) != 2 ||
		output.SelectedEntryDigests[0] != strings.Repeat("1", 64) ||
		output.SelectedEntryDigests[1] != strings.Repeat("2", 64) {
		t.Fatalf("selected=%v", output.SelectedEntryDigests)
	}
	request, _, err := moduleapi.RestoreMemoryContextRequestV1(requestCanonical)
	if err != nil {
		t.Fatalf("RestoreMemoryContextRequestV1: %v", err)
	}
	if err := moduleapi.ValidateMemoryContextOutputForRequestV1(request, output); err != nil {
		t.Fatalf("ValidateMemoryContextOutputForRequestV1: %v", err)
	}
}

func TestDeterministicMemoryHonorsTextLimitAndRejectsWrongProvider(t *testing.T) {
	provider := memoryProvider()
	adapter, err := NewDeterministicMemory(provider)
	if err != nil {
		t.Fatalf("NewDeterministicMemory: %v", err)
	}
	result, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(provider, memoryRequestCanonical(t, 4, 8)),
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	output, _, err := moduleapi.RestoreMemoryContextOutputV1(result.Output)
	if err != nil {
		t.Fatalf("RestoreMemoryContextOutputV1: %v", err)
	}
	// Text entries do not fit; count entries have no text payload and remain
	// valid selections under the text-byte ceiling.
	if len(output.SelectedEntryDigests) != 1 ||
		output.SelectedEntryDigests[0] != strings.Repeat("3", 64) {
		t.Fatalf("selected=%v", output.SelectedEntryDigests)
	}

	wrong := provider
	wrong.ArtifactDigest = strings.Repeat("9", 64)
	if _, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(wrong, memoryRequestCanonical(t, 1, 128)),
	); err == nil {
		t.Fatal("wrong Provider unexpectedly invoked")
	}
}

func memoryProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.memory.deterministic",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("a", 64),
		InstanceID:         "memory-main",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "memory-deterministic-v1",
		ActivationRevision: 1,
	}
}

func memoryRequestCanonical(
	t *testing.T,
	maxItems uint32,
	maxTextBytes uint32,
) []byte {
	t.Helper()
	scope := moduleapi.MemoryQueryScopeV1{
		TenantID: "tenant-one",
		Workspace: moduleapi.MemoryObjectRefV1{
			ID: "workspace-main", Version: "1", Digest: strings.Repeat("b", 64),
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID: "agent-main", Version: "1", Digest: strings.Repeat("c", 64),
		},
		TaskInputRef: strings.Repeat("d", 64),
	}
	_, canonical, _, err := moduleapi.NewMemoryContextRequestV1(
		moduleapi.MemoryContextRequestV1{
			SchemaVersion: moduleapi.MemoryContextRequestSchemaV1,
			Snapshot: moduleapi.MemorySnapshotRefV1{
				TenantID: "tenant-one", AgentID: "agent-main", Revision: 1,
				Digest: strings.Repeat("e", 64),
			},
			Scope:             scope,
			QueryText:         "prefer Go architecture",
			EvaluatedAtUnixMS: 1_800_000_000_000,
			Candidates: []moduleapi.MemoryCandidateV1{
				{EntryDigest: strings.Repeat("1", 64), Kind: moduleapi.MemoryEntryPreference, Key: "language", Text: "Prefer Go."},
				{EntryDigest: strings.Repeat("2", 64), Kind: moduleapi.MemoryEntryTaskSummary, Key: "architecture", Text: "Designed a modular agent."},
				{EntryDigest: strings.Repeat("3", 64), Kind: moduleapi.MemoryEntryCategoryCount, Key: "network", Count: 12},
			},
			MaxItems:          maxItems,
			MaxTotalTextBytes: maxTextBytes,
		},
	)
	if err != nil {
		t.Fatalf("NewMemoryContextRequestV1: %v", err)
	}
	return canonical
}
