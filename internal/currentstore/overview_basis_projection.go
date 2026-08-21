package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	overviewBasisProjectionSchemaV1  = "overview-basis-projection/v1"
	overviewBasisProjectionDomainV1  = "freeagent.current-store.overview-basis-projection.v1"
	overviewBasisMaximumJSONV1       = 128 << 10
	overviewBasisMaximumWorkspacesV1 = 256
)

type overviewBasisProjectionV1 struct {
	SchemaVersion             string                         `json:"schema_version"`
	StoreInstanceID           string                         `json:"store_instance_id"`
	Basis                     controlcontract.PublishedBasis `json:"basis"`
	ControlPublishedAtMicros  uint64                         `json:"control_published_at_micros"`
	CatalogPublishedAtMicros  uint64                         `json:"catalog_published_at_micros"`
	SourceUpdatedAtUnixMicros uint64                         `json:"source_updated_at_unix_micros"`
	Workspaces                []corecontract.WorkspaceRef    `json:"workspaces"`
}

func publishOverviewBasisProjectionV1(
	ctx context.Context,
	q runObservationWriterV1,
	prepared preparedControlCatalogPublication,
	controlPublishedAt int64,
	catalogPublishedAt int64,
) error {
	if ctx == nil || q == nil || controlPublishedAt <= 0 || catalogPublishedAt <= 0 {
		return fmt.Errorf("%w: invalid Overview basis projection input", ErrPublicationConflict)
	}
	var projection overviewBasisProjectionV1
	projection.SchemaVersion = overviewBasisProjectionSchemaV1
	projection.Basis = prepared.basis
	projection.ControlPublishedAtMicros = uint64(controlPublishedAt)
	projection.CatalogPublishedAtMicros = uint64(catalogPublishedAt)
	projection.SourceUpdatedAtUnixMicros = projection.ControlPublishedAtMicros
	if projection.CatalogPublishedAtMicros > projection.SourceUpdatedAtUnixMicros {
		projection.SourceUpdatedAtUnixMicros = projection.CatalogPublishedAtMicros
	}
	if err := q.QueryRowContext(ctx, `SELECT store_instance_id FROM store_meta
		WHERE singleton=1`).Scan(&projection.StoreInstanceID); err != nil {
		return fmt.Errorf("%w: Overview basis Store identity: %v", ErrPublicationConflict, err)
	}
	if len(prepared.control.Workspaces) > overviewBasisMaximumWorkspacesV1 {
		return fmt.Errorf("%w: too many Overview Workspaces", ErrPublicationConflict)
	}
	projection.Workspaces = make([]corecontract.WorkspaceRef, len(prepared.control.Workspaces))
	seen := make(map[string]struct{}, len(projection.Workspaces))
	for i, definition := range prepared.control.Workspaces {
		ref := definition.Workspace
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("%w: invalid Overview Workspace: %v", ErrPublicationConflict, err)
		}
		if _, duplicate := seen[ref.ID]; duplicate {
			return fmt.Errorf("%w: duplicate Overview Workspace", ErrPublicationConflict)
		}
		seen[ref.ID] = struct{}{}
		projection.Workspaces[i] = ref
	}
	sort.Slice(projection.Workspaces, func(i, j int) bool {
		return projection.Workspaces[i].ID < projection.Workspaces[j].ID
	})
	canonical, digest, err := canonicalOverviewBasisProjectionV1(projection)
	if err != nil {
		return err
	}
	var existing string
	err = q.QueryRowContext(ctx, `SELECT projection_digest FROM overview_basis_heads
		WHERE tenant_id=?`, prepared.basis.TenantID).Scan(&existing)
	initial := err == sql.ErrNoRows
	if err != nil && !initial {
		return fmt.Errorf("currentstore: load Overview basis head: %w", err)
	}
	if !initial && existing == digest {
		return verifyCurrentOverviewBasisProjectionV1(ctx, q, prepared.basis.TenantID)
	}
	_, err = q.ExecContext(ctx, `INSERT INTO overview_basis_snapshots(
		projection_digest,store_instance_id,tenant_id,pointer_revision,snapshot_id,
		control_revision,control_digest,control_published_at,catalog_generation_id,
		catalog_generation,catalog_digest,catalog_published_at,source_updated_at,
		workspace_count,canonical_json
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, digest, projection.StoreInstanceID,
		projection.Basis.TenantID, int64(projection.Basis.PointerRevision),
		projection.Basis.Control.SnapshotID, int64(projection.Basis.Control.Revision),
		projection.Basis.Control.Digest, controlPublishedAt,
		projection.Basis.Catalog.GenerationID, int64(projection.Basis.Catalog.Generation),
		projection.Basis.Catalog.Digest, catalogPublishedAt,
		int64(projection.SourceUpdatedAtUnixMicros), len(projection.Workspaces), canonical)
	if err != nil {
		return fmt.Errorf("currentstore: insert Overview basis snapshot: %w", err)
	}
	for ordinal, ref := range projection.Workspaces {
		if _, err := q.ExecContext(ctx, `INSERT INTO overview_basis_workspaces(
			projection_digest,ordinal,workspace_id,workspace_version,workspace_digest
		) VALUES(?,?,?,?,?)`, digest, ordinal, ref.ID, ref.Version, ref.Digest); err != nil {
			return fmt.Errorf("currentstore: insert Overview basis Workspace: %w", err)
		}
	}
	if initial {
		_, err = q.ExecContext(ctx, `INSERT INTO overview_basis_heads(
			tenant_id,snapshot_id,catalog_generation_id,pointer_revision,
			projection_digest,source_updated_at,workspace_count
		) VALUES(?,?,?,?,?,?,?)`, projection.Basis.TenantID,
			projection.Basis.Control.SnapshotID, projection.Basis.Catalog.GenerationID,
			int64(projection.Basis.PointerRevision), digest,
			int64(projection.SourceUpdatedAtUnixMicros), len(projection.Workspaces))
	} else {
		var result sql.Result
		result, err = q.ExecContext(ctx, `UPDATE overview_basis_heads SET
			snapshot_id=?,catalog_generation_id=?,pointer_revision=?,projection_digest=?,
			source_updated_at=?,workspace_count=? WHERE tenant_id=? AND projection_digest=?`,
			projection.Basis.Control.SnapshotID, projection.Basis.Catalog.GenerationID,
			int64(projection.Basis.PointerRevision), digest,
			int64(projection.SourceUpdatedAtUnixMicros), len(projection.Workspaces),
			projection.Basis.TenantID, existing)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				err = fmt.Errorf("Overview basis head CAS affected %d rows", affected)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("currentstore: advance Overview basis head: %w", err)
	}
	return verifyCurrentOverviewBasisProjectionV1(ctx, q, prepared.basis.TenantID)
}

func canonicalOverviewBasisProjectionV1(
	projection overviewBasisProjectionV1,
) ([]byte, string, error) {
	if projection.SchemaVersion != overviewBasisProjectionSchemaV1 ||
		!validLeaseOpaqueID(projection.StoreInstanceID) || projection.Basis.Validate() != nil ||
		projection.ControlPublishedAtMicros == 0 || projection.ControlPublishedAtMicros > math.MaxInt64 ||
		projection.CatalogPublishedAtMicros == 0 || projection.CatalogPublishedAtMicros > math.MaxInt64 ||
		projection.SourceUpdatedAtUnixMicros != maxOverviewMicrosV1(
			projection.ControlPublishedAtMicros, projection.CatalogPublishedAtMicros,
		) || len(projection.Workspaces) > overviewBasisMaximumWorkspacesV1 {
		return nil, "", fmt.Errorf("%w: invalid Overview basis projection", ErrPublicationConflict)
	}
	previous := ""
	for _, ref := range projection.Workspaces {
		if ref.Validate() != nil || (previous != "" && ref.ID <= previous) {
			return nil, "", fmt.Errorf("%w: invalid Overview Workspace projection", ErrPublicationConflict)
		}
		previous = ref.ID
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(raw, moduleapi.CanonicalJSONLimits{
		MaxBytes: overviewBasisMaximumJSONV1, MaxDepth: 6, MaxNodes: 2304,
	})
	if err != nil || len(canonical) > overviewBasisMaximumJSONV1 {
		return nil, "", fmt.Errorf("%w: canonical Overview basis: %v", ErrPublicationConflict, err)
	}
	return canonical, moduleapi.Digest(overviewBasisProjectionDomainV1, canonical), nil
}

func verifyCurrentOverviewBasisProjectionV1(
	ctx context.Context,
	q readQueryerV1,
	tenantID string,
) error {
	var storedDigest, snapshotStoreInstanceID, snapshotTenantID string
	var snapshotID, snapshotControlDigest, snapshotCatalogID, snapshotCatalogDigest string
	var headTenantID, headSnapshotID, headCatalogID, headDigest string
	var currentSnapshotID, currentCatalogID, currentStoreInstanceID string
	var snapshotPointer, snapshotControlRevision, snapshotControlPublishedAt int64
	var snapshotCatalogGeneration, snapshotCatalogPublishedAt, snapshotSourceUpdatedAt int64
	var snapshotWorkspaceCount, headPointer, headSourceUpdatedAt, headWorkspaceCount int64
	var currentPointer int64
	var canonical []byte
	err := q.QueryRowContext(ctx, `SELECT snapshot.canonical_json,snapshot.projection_digest,
		snapshot.store_instance_id,snapshot.tenant_id,snapshot.pointer_revision,
		snapshot.snapshot_id,snapshot.control_revision,snapshot.control_digest,
		snapshot.control_published_at,snapshot.catalog_generation_id,
		snapshot.catalog_generation,snapshot.catalog_digest,snapshot.catalog_published_at,
		snapshot.source_updated_at,snapshot.workspace_count,
		head.tenant_id,head.snapshot_id,head.catalog_generation_id,
		head.pointer_revision,head.projection_digest,head.source_updated_at,
		head.workspace_count,current.snapshot_id,current.catalog_generation_id,
		current.pointer_revision,meta.store_instance_id
		FROM overview_basis_heads AS head JOIN overview_basis_snapshots AS snapshot
		ON snapshot.projection_digest=head.projection_digest
		JOIN control_current AS current ON current.tenant_id=head.tenant_id
		JOIN store_meta AS meta ON meta.singleton=1
		WHERE head.tenant_id=?`, tenantID).Scan(
		&canonical, &storedDigest, &snapshotStoreInstanceID, &snapshotTenantID,
		&snapshotPointer, &snapshotID, &snapshotControlRevision, &snapshotControlDigest,
		&snapshotControlPublishedAt, &snapshotCatalogID, &snapshotCatalogGeneration,
		&snapshotCatalogDigest, &snapshotCatalogPublishedAt, &snapshotSourceUpdatedAt,
		&snapshotWorkspaceCount, &headTenantID, &headSnapshotID, &headCatalogID,
		&headPointer, &headDigest, &headSourceUpdatedAt, &headWorkspaceCount,
		&currentSnapshotID, &currentCatalogID, &currentPointer, &currentStoreInstanceID)
	if err != nil {
		return fmt.Errorf("%w: current Overview basis head: %v", ErrPublicationConflict, err)
	}
	var orphan int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1
		FROM overview_basis_snapshots WHERE tenant_id=? AND pointer_revision>? LIMIT 1)`,
		tenantID, headPointer).Scan(&orphan); err != nil || orphan != 0 {
		return fmt.Errorf("%w: Overview basis orphan tail: %v", ErrPublicationConflict, err)
	}
	var projection overviewBasisProjectionV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projection); err != nil {
		return fmt.Errorf("%w: decode Overview basis projection: %v", ErrPublicationConflict, err)
	}
	rebuilt, digest, err := canonicalOverviewBasisProjectionV1(projection)
	if err != nil || !bytes.Equal(rebuilt, canonical) || digest != storedDigest ||
		digest != headDigest || projection.Basis.TenantID != tenantID ||
		projection.StoreInstanceID != snapshotStoreInstanceID ||
		projection.StoreInstanceID != currentStoreInstanceID || snapshotTenantID != tenantID ||
		headTenantID != tenantID || projection.Basis.PointerRevision != uint64(snapshotPointer) ||
		projection.Basis.PointerRevision != uint64(headPointer) ||
		projection.Basis.PointerRevision != uint64(currentPointer) ||
		projection.Basis.Control.SnapshotID != snapshotID || snapshotID != headSnapshotID ||
		snapshotID != currentSnapshotID ||
		projection.Basis.Control.Revision != uint64(snapshotControlRevision) ||
		projection.Basis.Control.Digest != snapshotControlDigest ||
		projection.ControlPublishedAtMicros != uint64(snapshotControlPublishedAt) ||
		projection.Basis.Catalog.GenerationID != snapshotCatalogID ||
		snapshotCatalogID != headCatalogID || snapshotCatalogID != currentCatalogID ||
		projection.Basis.Catalog.Generation != uint64(snapshotCatalogGeneration) ||
		projection.Basis.Catalog.Digest != snapshotCatalogDigest ||
		projection.CatalogPublishedAtMicros != uint64(snapshotCatalogPublishedAt) ||
		projection.SourceUpdatedAtUnixMicros != uint64(snapshotSourceUpdatedAt) ||
		projection.SourceUpdatedAtUnixMicros != uint64(headSourceUpdatedAt) ||
		len(projection.Workspaces) != int(snapshotWorkspaceCount) ||
		len(projection.Workspaces) != int(headWorkspaceCount) {
		return fmt.Errorf("%w: Overview basis canonical differs", ErrPublicationConflict)
	}
	rows, err := q.QueryContext(ctx, `SELECT ordinal,workspace_id,workspace_version,
		workspace_digest FROM overview_basis_workspaces WHERE projection_digest=?
		ORDER BY ordinal`, storedDigest)
	if err != nil {
		return err
	}
	defer rows.Close()
	ordinal := 0
	for rows.Next() {
		var gotOrdinal int
		var got corecontract.WorkspaceRef
		if err := rows.Scan(&gotOrdinal, &got.ID, &got.Version, &got.Digest); err != nil ||
			ordinal >= len(projection.Workspaces) || gotOrdinal != ordinal ||
			got != projection.Workspaces[ordinal] {
			return fmt.Errorf("%w: Overview Workspace rows differ", ErrPublicationConflict)
		}
		ordinal++
	}
	if err := rows.Err(); err != nil || ordinal != len(projection.Workspaces) {
		return fmt.Errorf("%w: Overview Workspace row count differs: %v", ErrPublicationConflict, err)
	}
	return nil
}

// loadOverviewBasisProjectionOnlineV1 reads only bounded scalar projection
// rows. Immutable-table triggers and the OpenExisting semantic gate establish
// the large Control/Catalog canonical closure for the process lifetime.
func loadOverviewBasisProjectionOnlineV1(
	ctx context.Context,
	q readQueryerV1,
	tenantID string,
) (controlcontract.PublishedBasis, []corecontract.WorkspaceRef, uint64, error) {
	var basis controlcontract.PublishedBasis
	basis.TenantID = tenantID
	var pointer, controlRevision, catalogGeneration, source, workspaceCount int64
	var projectionDigest, snapshotStoreID, currentStoreID string
	var currentSnapshotID, currentCatalogID string
	err := q.QueryRowContext(ctx, `SELECT snapshot.projection_digest,
		snapshot.store_instance_id,snapshot.pointer_revision,snapshot.snapshot_id,
		snapshot.control_revision,snapshot.control_digest,snapshot.catalog_generation_id,
		snapshot.catalog_generation,snapshot.catalog_digest,snapshot.source_updated_at,
		snapshot.workspace_count,current.snapshot_id,current.catalog_generation_id,
		meta.store_instance_id
		FROM overview_basis_heads AS head JOIN overview_basis_snapshots AS snapshot
		ON snapshot.projection_digest=head.projection_digest
		JOIN control_current AS current ON current.tenant_id=head.tenant_id
		JOIN store_meta AS meta ON meta.singleton=1
		WHERE head.tenant_id=? AND snapshot.tenant_id=head.tenant_id
		AND snapshot.pointer_revision=head.pointer_revision
		AND snapshot.snapshot_id=head.snapshot_id
		AND snapshot.catalog_generation_id=head.catalog_generation_id
		AND snapshot.source_updated_at=head.source_updated_at
		AND snapshot.workspace_count=head.workspace_count
		AND current.snapshot_id=head.snapshot_id
		AND current.catalog_generation_id=head.catalog_generation_id
		AND current.pointer_revision=head.pointer_revision`, tenantID).Scan(
		&projectionDigest, &snapshotStoreID, &pointer, &basis.Control.SnapshotID,
		&controlRevision, &basis.Control.Digest, &basis.Catalog.GenerationID,
		&catalogGeneration, &basis.Catalog.Digest, &source, &workspaceCount,
		&currentSnapshotID, &currentCatalogID, &currentStoreID)
	if err == sql.ErrNoRows {
		return basis, nil, 0, err
	}
	if err != nil || snapshotStoreID != currentStoreID || !moduleapi.ValidSHA256(projectionDigest) ||
		pointer <= 0 || controlRevision <= 0 || catalogGeneration <= 0 || source <= 0 ||
		workspaceCount < 0 || workspaceCount > overviewBasisMaximumWorkspacesV1 ||
		currentSnapshotID != basis.Control.SnapshotID || currentCatalogID != basis.Catalog.GenerationID {
		return basis, nil, 0, fmt.Errorf("%w: invalid online Overview basis projection: %v",
			ErrPublicationConflict, err)
	}
	basis.PointerRevision = uint64(pointer)
	basis.Control.Revision = uint64(controlRevision)
	basis.Catalog.Generation = uint64(catalogGeneration)
	if err := basis.Validate(); err != nil {
		return basis, nil, 0, fmt.Errorf("%w: online Overview basis: %v", ErrPublicationConflict, err)
	}
	var orphan int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1
		FROM overview_basis_snapshots WHERE tenant_id=? AND pointer_revision>? LIMIT 1)`,
		tenantID, pointer).Scan(&orphan); err != nil || orphan != 0 {
		return basis, nil, 0, fmt.Errorf("%w: online Overview basis orphan tail: %v",
			ErrPublicationConflict, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT ordinal,workspace_id,workspace_version,
		workspace_digest FROM overview_basis_workspaces WHERE projection_digest=?
		ORDER BY ordinal`, projectionDigest)
	if err != nil {
		return basis, nil, 0, err
	}
	defer rows.Close()
	workspaces := make([]corecontract.WorkspaceRef, 0, int(workspaceCount))
	for rows.Next() {
		var ordinal int
		var ref corecontract.WorkspaceRef
		if err := rows.Scan(&ordinal, &ref.ID, &ref.Version, &ref.Digest); err != nil ||
			ordinal != len(workspaces) || ordinal >= int(workspaceCount) || ref.Validate() != nil ||
			(ordinal > 0 && ref.ID <= workspaces[ordinal-1].ID) {
			return basis, nil, 0, fmt.Errorf("%w: invalid online Overview Workspace projection: %v",
				ErrPublicationConflict, err)
		}
		workspaces = append(workspaces, ref)
	}
	if err := rows.Err(); err != nil || len(workspaces) != int(workspaceCount) {
		return basis, nil, 0, fmt.Errorf("%w: online Overview Workspace count: %v",
			ErrPublicationConflict, err)
	}
	return basis, workspaces, uint64(source), nil
}
