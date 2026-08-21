package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// normalizeStagedMemoryArtifactModesV1 validates the package as inert local
// data for the compiled deterministic Memory adapter and then freezes every
// file read-only. It constructs an invoker elsewhere but never invokes it.
func normalizeStagedMemoryArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
) ([]byte, error) {
	manifestCanonical, err := readModuleApplyMemoryMetadataV1(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, err
	}
	if err := normalizeStagedReadOnlyArtifactModesV1(
		ctx,
		artifactDirectory,
		expectedDigest,
		expectedSize,
	); err != nil {
		return nil, err
	}
	return bytes.Clone(manifestCanonical), nil
}

// readModuleApplyMemoryMetadataV1 closes the package-level contract. The
// exact adapter descriptor and its adapter_identity binding are independently
// proved by loadMemoryInvokerFromArtifact.
func readModuleApplyMemoryMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) ([]byte, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.Runtime.Mode !=
		moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != productionContextPort ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return nil, errors.Join(
			err,
			errors.New(
				"artifact is not exact TRUSTED_IN_PROCESS local Memory context.provide/v1",
			),
		)
	}
	entrypoint, err := moduleapi.NormalizeArtifactPath(manifest.Runtime.Entrypoint)
	if err != nil || entrypoint != manifest.Runtime.Entrypoint ||
		!strings.HasPrefix(entrypoint, "content/") {
		return nil, errors.Join(
			err,
			errors.New("Memory artifact entrypoint is not a canonical content/ path"),
		)
	}
	return bytes.Clone(manifestCanonical), nil
}

func moduleApplyMemoryAuthorityV1(
	plan moduleApplyPlanV1,
) (moduleapi.MemoryAuthorityCeilingV1, bool, error) {
	if plan.DesiredState != moduleApplyEnabledV1 || plan.Binding == nil {
		return moduleapi.MemoryAuthorityCeilingV1{}, false, nil
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return moduleapi.MemoryAuthorityCeilingV1{}, false, err
	}
	if policy.HandlerKind != moduleApplyHandlerMemoryContextV1 {
		return moduleapi.MemoryAuthorityCeilingV1{}, false, nil
	}
	authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return moduleapi.MemoryAuthorityCeilingV1{}, false, err
	}
	if authority.TenantID != plan.TenantID {
		return moduleapi.MemoryAuthorityCeilingV1{}, false, errors.New(
			"Memory authority tenant differs from apply plan",
		)
	}
	return authority, true, nil
}

func newModuleApplyMemoryGenesisV1(
	tenantID string,
	agentID string,
) ([]byte, error) {
	_, canonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      tenantID,
			AgentID:       agentID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{},
		},
	)
	return canonical, err
}

// ensureModuleApplyMemoryGenesisV1 is intentionally called only after an
// exact current Catalog publication has been proved. Therefore a failed or
// losing Catalog CAS cannot create an orphan Agent Memory revision. An exact
// published retry repeats this idempotent closer and repairs a process failure
// in the post-CAS/pre-genesis window.
//
// An existing head is verified through Current Store and then left completely
// untouched: no replacement, merge, revision append, timestamp update, or
// provider invocation occurs.
func ensureModuleApplyMemoryGenesisV1(
	ctx context.Context,
	store *currentstore.Store,
	plan moduleApplyPlanV1,
) error {
	if ctx == nil || store == nil {
		return errors.New("Memory genesis closer requires context and Current Store")
	}
	authority, memoryEnabled, err := moduleApplyMemoryAuthorityV1(plan)
	if err != nil || !memoryEnabled {
		return err
	}
	existing, err := store.GetCurrentAgentMemory(
		ctx,
		authority.TenantID,
		authority.AgentID,
	)
	if err == nil {
		if existing.SnapshotRef.TenantID != authority.TenantID ||
			existing.SnapshotRef.AgentID != authority.AgentID ||
			existing.Snapshot.TenantID != authority.TenantID ||
			existing.Snapshot.AgentID != authority.AgentID ||
			len(existing.CanonicalBytes) == 0 {
			return errors.New("existing Agent Memory head owner or bytes differ")
		}
		// GetCurrentAgentMemory has already restored the immutable row and
		// canonical bytes. Returning without a write is the preservation proof.
		return nil
	}
	if !errors.Is(err, currentstore.ErrAgentMemoryNotFound) {
		return fmt.Errorf("read existing Agent Memory head: %w", err)
	}
	canonical, err := newModuleApplyMemoryGenesisV1(
		authority.TenantID,
		authority.AgentID,
	)
	if err != nil {
		return fmt.Errorf("freeze deterministic Agent Memory genesis: %w", err)
	}
	put, err := store.PutAgentMemoryGenesis(ctx, canonical)
	if err != nil {
		return fmt.Errorf("persist deterministic Agent Memory genesis: %w", err)
	}
	if put.Record.SnapshotRef.TenantID != authority.TenantID ||
		put.Record.SnapshotRef.AgentID != authority.AgentID ||
		put.Record.SnapshotRef.Revision != 1 ||
		put.Record.SourceAttemptID != "" ||
		len(put.Record.Snapshot.Entries) != 0 ||
		!bytes.Equal(put.Record.CanonicalBytes, canonical) {
		return errors.New("persisted Agent Memory genesis did not close exactly")
	}
	return nil
}
