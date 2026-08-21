package currentstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestAgentMemoryGenesisIsExplicitIdempotentAndConflictSafe(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	tenantID := fixture.intent.TenantID
	agentID := mustRestoreMemberSnapshot(t, fixture).Agent.ID
	canonical := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		nil,
	)

	created, err := fixture.store.PutAgentMemoryGenesis(
		context.Background(),
		canonical,
	)
	if err != nil {
		t.Fatalf("PutAgentMemoryGenesis: %v", err)
	}
	if !created.Applied || created.Record.SnapshotRef.Revision != 1 ||
		created.Record.SnapshotRef.TenantID != tenantID ||
		created.Record.SnapshotRef.AgentID != agentID ||
		created.Record.SourceAttemptID != "" ||
		len(created.Record.SnapshotRef.Digest) != 64 {
		t.Fatalf("created=%+v", created)
	}
	beforeRetry := agentMemoryCounts(t, fixture.store)
	retried, err := fixture.store.PutAgentMemoryGenesis(
		context.Background(),
		canonical,
	)
	if err != nil {
		t.Fatalf("idempotent genesis: %v", err)
	}
	if retried.Applied || retried.Record.SnapshotRef != created.Record.SnapshotRef {
		t.Fatalf("retried=%+v", retried)
	}
	if got := agentMemoryCounts(t, fixture.store); got != beforeRetry {
		t.Fatalf("idempotent genesis changed counts: got=%v want=%v", got, beforeRetry)
	}

	conflicting := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		[]moduleapi.MemoryEntryV1{memoryFactEntry(t, "different")},
	)
	beforeConflict := agentMemoryCounts(t, fixture.store)
	if _, err := fixture.store.PutAgentMemoryGenesis(
		context.Background(),
		conflicting,
	); !errors.Is(err, ErrAgentMemoryConflict) {
		t.Fatalf("conflicting genesis error=%v", err)
	}
	if got := agentMemoryCounts(t, fixture.store); got != beforeConflict {
		t.Fatalf("conflicting genesis wrote rows: got=%v want=%v", got, beforeConflict)
	}

	current, err := fixture.store.GetCurrentAgentMemory(
		context.Background(),
		tenantID,
		agentID,
	)
	if err != nil || current.SnapshotRef != created.Record.SnapshotRef {
		t.Fatalf("current=%+v error=%v", current, err)
	}
	exact, err := fixture.store.GetAgentMemoryRevision(
		context.Background(),
		tenantID,
		agentID,
		1,
	)
	if err != nil || exact.SnapshotRef != created.Record.SnapshotRef {
		t.Fatalf("exact=%+v error=%v", exact, err)
	}
}

func TestAgentMemoryRejectsBrokenStoredOwner(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	tenantID := fixture.intent.TenantID
	agentID := mustRestoreMemberSnapshot(t, fixture).Agent.ID
	genesisCanonical := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		nil,
	)
	_, err := fixture.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`
		UPDATE agent_memory_revisions
		SET agent_id='agent-corrupted'
		WHERE tenant_id=? AND agent_id=? AND revision=1
	`, tenantID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.GetAgentMemoryRevision(
		context.Background(),
		tenantID,
		"agent-corrupted",
		1,
	); !errors.Is(err, ErrAgentMemoryIntegrity) {
		t.Fatalf("broken owner error=%v", err)
	}
}

func canonicalAgentMemorySnapshot(
	t *testing.T,
	tenantID string,
	agentID string,
	revision uint64,
	previousDigest string,
	sourceAttemptID string,
	entries []moduleapi.MemoryEntryV1,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion:          moduleapi.MemorySnapshotSchemaV1,
			TenantID:               tenantID,
			AgentID:                agentID,
			Revision:               revision,
			PreviousSnapshotDigest: previousDigest,
			SourceAttemptID:        sourceAttemptID,
			Entries:                entries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func memoryFactEntry(t *testing.T, text string) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:             "fact-" + text,
		Kind:                moduleapi.MemoryEntryFact,
		Key:                 "fact-" + text,
		Text:                text,
		VisibleWorkspaceIDs: []string{"*"},
		SourceRefs:          []string{strings.Repeat("a", 64)},
		CreatedAtUnixMS:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func agentMemoryCounts(t *testing.T, store *Store) [2]int {
	t.Helper()
	var counts [2]int
	if err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM content_records
		WHERE kind='MEMORY_SNAPSHOT'
	`).Scan(&counts[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM agent_memory_revisions
	`).Scan(&counts[1]); err != nil {
		t.Fatal(err)
	}
	return counts
}
