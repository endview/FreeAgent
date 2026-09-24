-- W6.6 gives server-owned Upgrade Review facts a first-class relational
-- closure. Historical caller-owned U3 Reviews remain readable with NULL
-- values; every W6.6 Review supplies all three columns.
-- The bootstrap store_meta constraint names only version 1. Rebuild that
-- small identity table so the explicit migration can publish version 2 while
-- retaining the same singleton and store instance.
DROP TRIGGER run_observation_snapshots_validate_append;
DROP TRIGGER overview_resource_snapshots_validate_append;
DROP TRIGGER overview_resource_transition_carriers_validate_insert;
DROP TRIGGER overview_basis_snapshots_validate_insert;

ALTER TABLE store_meta RENAME TO store_meta_v1;

CREATE TABLE store_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id = trim(store_instance_id)
    ),
    schema_identity TEXT NOT NULL CHECK (
        schema_identity = 'github.com/endview/freeagent/current-store-v2'
    ),
    schema_version INTEGER NOT NULL CHECK (schema_version = 2),
    schema_fingerprint TEXT NOT NULL CHECK (
        length(schema_fingerprint) = 64
        AND schema_fingerprint = lower(schema_fingerprint)
        AND schema_fingerprint NOT GLOB '*[^0-9a-f]*'
    ),
    generator_id TEXT NOT NULL CHECK (
        generator_id = 'freeagent-current-store-v2'
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0)
) STRICT;

INSERT INTO store_meta(
    singleton, store_instance_id, schema_identity, schema_version,
    schema_fingerprint, generator_id, created_at
)
SELECT singleton, store_instance_id, schema_identity, 2,
       schema_fingerprint, generator_id, created_at
FROM store_meta_v1;

DROP TABLE store_meta_v1;

CREATE TRIGGER run_observation_snapshots_validate_append
BEFORE INSERT ON run_observation_snapshots
WHEN NEW.store_instance_id<>(
    SELECT store_instance_id FROM store_meta WHERE singleton=1
)
OR NOT (
    (NEW.observation_sequence=1
      AND NEW.previous_snapshot_digest IS NULL
      AND NOT EXISTS (
        SELECT 1 FROM run_observation_snapshots WHERE run_id=NEW.run_id
      )
      AND NOT EXISTS (
        SELECT 1 FROM run_observation_heads WHERE run_id=NEW.run_id
      ))
    OR
    (NEW.observation_sequence>1
      AND EXISTS (
        SELECT 1 FROM run_observation_heads
        WHERE run_id=NEW.run_id
          AND observation_sequence=NEW.observation_sequence-1
          AND snapshot_digest=NEW.previous_snapshot_digest
      ))
)
BEGIN
    SELECT RAISE(ABORT, 'invalid run observation snapshot append');
END;

CREATE TRIGGER overview_resource_snapshots_validate_append
BEFORE INSERT ON overview_resource_snapshots
WHEN NOT (
    (NEW.observation_sequence=1 AND NEW.previous_snapshot_digest IS NULL
      AND NOT EXISTS (
        SELECT 1 FROM overview_resource_snapshots
        WHERE resource_kind=NEW.resource_kind AND resource_id=NEW.resource_id
      )
      AND NOT EXISTS (
        SELECT 1 FROM overview_resource_heads
        WHERE resource_kind=NEW.resource_kind AND resource_id=NEW.resource_id
      ))
    OR
    (NEW.observation_sequence>1 AND EXISTS (
        SELECT 1 FROM overview_resource_heads
        WHERE resource_kind=NEW.resource_kind AND resource_id=NEW.resource_id
          AND observation_sequence=NEW.observation_sequence-1
          AND snapshot_digest=NEW.previous_snapshot_digest
    ))
)
OR (
    NEW.causal_run_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM run_observation_heads AS head
        WHERE head.run_id=NEW.causal_run_id
          AND head.observation_sequence=NEW.causal_run_observation_sequence
          AND head.snapshot_digest=NEW.causal_run_observation_digest
          AND head.tenant_id=NEW.tenant_id
          AND head.workspace_id=NEW.workspace_id
    )
)
OR (
    NEW.subject_run_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM run_observation_heads AS head
        WHERE head.run_id=NEW.subject_run_id
          AND head.observation_sequence=NEW.subject_run_observation_sequence
          AND head.snapshot_digest=NEW.subject_run_observation_digest
          AND head.tenant_id=NEW.tenant_id
          AND head.workspace_id=NEW.workspace_id
    )
)
OR (
    NEW.related_run_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM run_observation_heads AS head
        WHERE head.run_id=NEW.related_run_id
          AND head.observation_sequence=NEW.related_run_observation_sequence
          AND head.snapshot_digest=NEW.related_run_observation_digest
          AND head.tenant_id=NEW.tenant_id
          AND head.workspace_id=NEW.workspace_id
    )
)
OR (
    NEW.store_instance_id<>(SELECT store_instance_id FROM store_meta WHERE singleton=1)
)
BEGIN
    SELECT RAISE(ABORT, 'invalid overview resource snapshot append');
END;

CREATE TRIGGER overview_resource_transition_carriers_validate_insert
BEFORE INSERT ON overview_resource_transition_carriers
WHEN NEW.store_instance_id<>(
    SELECT store_instance_id FROM store_meta WHERE singleton=1
)
OR NOT EXISTS (
    SELECT 1 FROM overview_resource_heads AS head
    WHERE head.resource_kind=NEW.resource_kind
      AND head.resource_id=NEW.resource_id
      AND head.observation_sequence=NEW.observation_sequence
      AND head.snapshot_digest=NEW.snapshot_digest
)
OR NOT EXISTS (
    SELECT 1 FROM overview_resource_snapshots AS snapshot
    WHERE snapshot.resource_kind=NEW.resource_kind
      AND snapshot.resource_id=NEW.resource_id
      AND snapshot.observation_sequence=NEW.observation_sequence
      AND snapshot.snapshot_digest=NEW.snapshot_digest
      AND snapshot.store_instance_id=NEW.store_instance_id
)
BEGIN
    SELECT RAISE(ABORT, 'invalid overview resource transition carrier');
END;

CREATE TRIGGER overview_basis_snapshots_validate_insert
BEFORE INSERT ON overview_basis_snapshots
WHEN NEW.store_instance_id<>(SELECT store_instance_id FROM store_meta WHERE singleton=1)
  OR NOT EXISTS (
    SELECT 1 FROM control_current AS current
    WHERE current.tenant_id=NEW.tenant_id
      AND current.snapshot_id=NEW.snapshot_id
      AND current.catalog_generation_id=NEW.catalog_generation_id
      AND current.pointer_revision=NEW.pointer_revision
  )
BEGIN
    SELECT RAISE(ABORT, 'invalid Overview basis snapshot');
END;

ALTER TABLE module_upgrade_reviews
    ADD COLUMN artifact_admission_id TEXT REFERENCES module_artifact_admissions(admission_id);

ALTER TABLE module_upgrade_reviews
    ADD COLUMN operator_principal_id TEXT CHECK (
        operator_principal_id IS NULL
        OR (
            length(operator_principal_id) BETWEEN 1 AND 256
            AND operator_principal_id = trim(operator_principal_id)
        )
    );

ALTER TABLE module_upgrade_reviews
    ADD COLUMN review_request_digest TEXT CHECK (
        review_request_digest IS NULL
        OR (
            length(review_request_digest) = 64
            AND review_request_digest = lower(review_request_digest)
            AND review_request_digest NOT GLOB '*[^0-9a-f]*'
        )
    );

CREATE INDEX module_upgrade_reviews_by_admission
    ON module_upgrade_reviews(artifact_admission_id, review_id)
    WHERE artifact_admission_id IS NOT NULL;
