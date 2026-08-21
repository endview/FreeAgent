CREATE TABLE store_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id = trim(store_instance_id)
    ),
    schema_identity TEXT NOT NULL CHECK (
        schema_identity = 'github.com/endview/freeagent/current-store-v1'
    ),
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    schema_fingerprint TEXT NOT NULL CHECK (
        length(schema_fingerprint) = 64
        AND schema_fingerprint = lower(schema_fingerprint)
        AND schema_fingerprint NOT GLOB '*[^0-9a-f]*'
    ),
    generator_id TEXT NOT NULL CHECK (
        generator_id = 'freeagent-current-store-draft-v1'
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0)
) STRICT;

CREATE TABLE content_records (
    content_digest TEXT PRIMARY KEY CHECK (
        length(content_digest) = 64
        AND content_digest = lower(content_digest)
        AND content_digest NOT GLOB '*[^0-9a-f]*'
    ),
    kind TEXT NOT NULL CHECK (kind IN (
        'MODULE_MANIFEST',
        'CONFIG',
        'AUTHORITY_CEILING',
        'TASK_INPUT',
        'POLICY',
        'MODEL_REQUEST',
        'CONTEXT_COMPILATION',
        'MODEL_RESULT',
        'ACTION_PROPOSAL',
        'ACTION_RESULT',
        'PROVIDER_RECEIPT',
        'STATIC_CONTEXT',
        'MEMORY_SNAPSHOT',
        'RECONCILIATION_EVIDENCE',
        'RUN_EVENT_PAYLOAD',
        'RUN_CANCELLATION',
        'CHANNEL_CURSOR',
        'CHANNEL_INGRESS_ENVELOPE',
        'CHANNEL_SEND_PROPOSAL',
        'CHANNEL_SEND_RESULT',
        'WORKSPACE_TRANSFER_PAYLOAD',
        'WORKSPACE_TRANSFER_ENVELOPE'
    )),
    media_type TEXT NOT NULL CHECK (
        length(media_type) BETWEEN 1 AND 256
        AND media_type = trim(media_type)
    ),
    canonical_bytes BLOB NOT NULL CHECK (length(canonical_bytes) <= 1048576),
    size_bytes INTEGER NOT NULL CHECK (
        size_bytes BETWEEN 0 AND 1048576
        AND size_bytes = length(canonical_bytes)
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0)
) STRICT;

CREATE TABLE control_snapshots (
    snapshot_id TEXT PRIMARY KEY CHECK (
        length(snapshot_id) BETWEEN 1 AND 256
        AND snapshot_id = trim(snapshot_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    revision INTEGER NOT NULL CHECK (revision > 0),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) <= 67108864),
    digest TEXT NOT NULL CHECK (
        length(digest) = 64
        AND digest = lower(digest)
        AND digest NOT GLOB '*[^0-9a-f]*'
    ),
    published_at INTEGER NOT NULL CHECK (published_at > 0),
    UNIQUE (tenant_id, revision),
    UNIQUE (tenant_id, digest),
    UNIQUE (snapshot_id,tenant_id,revision,digest,published_at)
) STRICT;

CREATE TABLE module_installations (
    installation_id TEXT PRIMARY KEY CHECK (
        length(installation_id) BETWEEN 1 AND 256
        AND installation_id = trim(installation_id)
    ),
    module_id TEXT NOT NULL CHECK (
        length(module_id) BETWEEN 1 AND 256 AND module_id = trim(module_id)
    ),
    exact_version TEXT NOT NULL CHECK (
        length(exact_version) BETWEEN 1 AND 64
        AND exact_version = trim(exact_version)
    ),
    manifest_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    artifact_digest TEXT NOT NULL CHECK (
        length(artifact_digest) = 64
        AND artifact_digest = lower(artifact_digest)
        AND artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    installed_at INTEGER NOT NULL CHECK (installed_at > 0),
    UNIQUE (module_id, exact_version)
) STRICT;

CREATE TABLE module_activations (
    activation_id TEXT PRIMARY KEY CHECK (
        length(activation_id) BETWEEN 1 AND 256
        AND activation_id = trim(activation_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    instance_id TEXT NOT NULL CHECK (
        length(instance_id) BETWEEN 1 AND 256 AND instance_id = trim(instance_id)
    ),
    installation_id TEXT NOT NULL
        REFERENCES module_installations(installation_id),
    activation_revision INTEGER NOT NULL CHECK (activation_revision > 0),
    execution_class TEXT NOT NULL CHECK (
        execution_class IN (
            'DECLARATIVE',
            'TRUSTED_IN_PROCESS',
            'LOCAL_PROCESS',
            'REMOTE',
            'WASM'
        )
    ),
    adapter_identity TEXT NOT NULL CHECK (
        length(adapter_identity) BETWEEN 1 AND 256
        AND adapter_identity = trim(adapter_identity)
    ),
    activated_at INTEGER NOT NULL CHECK (activated_at > 0),
    UNIQUE (tenant_id, instance_id, activation_revision)
) STRICT;

CREATE TABLE runtime_catalog_generations (
    generation_id TEXT PRIMARY KEY CHECK (
        length(generation_id) BETWEEN 1 AND 256
        AND generation_id = trim(generation_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    generation INTEGER NOT NULL CHECK (generation > 0),
    control_snapshot_id TEXT NOT NULL
        REFERENCES control_snapshots(snapshot_id),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) <= 67108864),
    digest TEXT NOT NULL CHECK (
        length(digest) = 64
        AND digest = lower(digest)
        AND digest NOT GLOB '*[^0-9a-f]*'
    ),
    published_at INTEGER NOT NULL CHECK (published_at > 0),
    UNIQUE (tenant_id, generation),
    UNIQUE (tenant_id, digest),
    UNIQUE (
        generation_id,tenant_id,generation,digest,published_at,
        control_snapshot_id
    )
) STRICT;

CREATE TABLE control_current (
    tenant_id TEXT PRIMARY KEY CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    snapshot_id TEXT NOT NULL REFERENCES control_snapshots(snapshot_id),
    catalog_generation_id TEXT NOT NULL
        REFERENCES runtime_catalog_generations(generation_id),
    pointer_revision INTEGER NOT NULL CHECK (pointer_revision > 0),
    FOREIGN KEY (
        tenant_id,snapshot_id,catalog_generation_id,pointer_revision
    ) REFERENCES overview_basis_heads(
        tenant_id,snapshot_id,catalog_generation_id,pointer_revision
    ) DEFERRABLE INITIALLY DEFERRED
) STRICT;

CREATE TRIGGER control_snapshots_reject_update
BEFORE UPDATE ON control_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Control snapshots are immutable');
END;

CREATE TRIGGER control_snapshots_reject_delete
BEFORE DELETE ON control_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Control snapshots are append-only');
END;

CREATE TRIGGER runtime_catalog_generations_reject_update
BEFORE UPDATE ON runtime_catalog_generations
BEGIN
    SELECT RAISE(ABORT, 'Catalog generations are immutable');
END;

CREATE TRIGGER runtime_catalog_generations_reject_delete
BEFORE DELETE ON runtime_catalog_generations
BEGIN
    SELECT RAISE(ABORT, 'Catalog generations are append-only');
END;

CREATE TABLE conversations (
    conversation_id TEXT PRIMARY KEY CHECK (
        length(conversation_id) BETWEEN 1 AND 256
        AND conversation_id = trim(conversation_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    principal_id TEXT NOT NULL CHECK (
        length(principal_id) BETWEEN 1 AND 256
        AND principal_id = trim(principal_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id = trim(workspace_id)
    ),
    agent_id TEXT NOT NULL CHECK (
        length(agent_id) BETWEEN 1 AND 256 AND agent_id = trim(agent_id)
    ),
    profile_id TEXT NOT NULL CHECK (
        length(profile_id) BETWEEN 1 AND 256 AND profile_id = trim(profile_id)
    ),
    head_run_id TEXT UNIQUE REFERENCES runs(run_id),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (
        updated_at > 0 AND updated_at >= created_at
    ),
    CHECK (
        (revision = 0 AND head_run_id IS NULL)
        OR (revision > 0 AND head_run_id IS NOT NULL)
    )
) STRICT;

CREATE TABLE runs (
    run_id TEXT PRIMARY KEY CHECK (
        length(run_id) BETWEEN 1 AND 256 AND run_id = trim(run_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id = trim(workspace_id)
    ),
    conversation_id TEXT REFERENCES conversations(conversation_id),
    conversation_turn_index INTEGER CHECK (
        conversation_turn_index IS NULL OR conversation_turn_index > 0
    ),
    conversation_predecessor_run_id TEXT REFERENCES runs(run_id),
    admission_key TEXT NOT NULL CHECK (
        length(admission_key) BETWEEN 1 AND 256
        AND admission_key = trim(admission_key)
    ),
    admission_intent_digest TEXT NOT NULL CHECK (
        length(admission_intent_digest) = 64
        AND admission_intent_digest = lower(admission_intent_digest)
        AND admission_intent_digest NOT GLOB '*[^0-9a-f]*'
    ),
    parent_run_id TEXT,
    parent_manifest_digest TEXT CHECK (
        parent_manifest_digest IS NULL OR (
            length(parent_manifest_digest) = 64
            AND parent_manifest_digest = lower(parent_manifest_digest)
            AND parent_manifest_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    parent_slot_id TEXT CHECK (
        parent_slot_id IS NULL OR (
            length(parent_slot_id) BETWEEN 1 AND 256
            AND parent_slot_id = trim(parent_slot_id)
        )
    ),
    cancel_request_ref TEXT REFERENCES content_records(content_digest),
    state TEXT NOT NULL CHECK (length(trim(state)) > 0),
    disposition TEXT CHECK (
        disposition IS NULL OR disposition IN (
            'YIELDED',
            'WAITING_INPUT',
            'WAITING_EXTERNAL',
            'WAITING_RECONCILIATION',
            'TERMINATED'
        )
    ),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at > 0),
    UNIQUE (tenant_id, admission_key),
    UNIQUE (run_id, tenant_id, workspace_id),
    UNIQUE (parent_run_id, parent_slot_id),
    UNIQUE (conversation_id, conversation_turn_index),
    UNIQUE (conversation_predecessor_run_id),
    CHECK (
        (conversation_id IS NULL
            AND conversation_turn_index IS NULL
            AND conversation_predecessor_run_id IS NULL)
        OR (
            conversation_id IS NOT NULL
            AND conversation_turn_index IS NOT NULL
            AND (
                (conversation_turn_index = 1
                    AND conversation_predecessor_run_id IS NULL)
                OR (
                    conversation_turn_index > 1
                    AND conversation_predecessor_run_id IS NOT NULL
                )
            )
        )
    ),
    CHECK (
        (parent_run_id IS NULL
            AND parent_manifest_digest IS NULL
            AND parent_slot_id IS NULL)
        OR (
            parent_run_id IS NOT NULL
            AND parent_run_id <> run_id
            AND parent_manifest_digest IS NOT NULL
            AND parent_slot_id IS NOT NULL
        )
    ),
    FOREIGN KEY (parent_run_id, parent_manifest_digest)
        REFERENCES run_manifests(run_id, digest),
    FOREIGN KEY (
        run_id,tenant_id,workspace_id,state,revision,created_at,updated_at
    ) REFERENCES run_observation_heads(
        run_id,tenant_id,workspace_id,run_state,run_revision,created_at,updated_at
    )
        DEFERRABLE INITIALLY DEFERRED
) STRICT;

CREATE INDEX runs_parent_slot_idx
    ON runs(parent_run_id, parent_slot_id);

CREATE TABLE workspace_scheduler_state (
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id = trim(workspace_id)
    ),
    served_units INTEGER NOT NULL CHECK (served_units > 0),
    revision INTEGER NOT NULL CHECK (revision > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at > 0),
    PRIMARY KEY (tenant_id, workspace_id)
) STRICT;

CREATE TABLE channel_ingress_receipts (
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id = trim(workspace_id)
    ),
    endpoint_id TEXT NOT NULL CHECK (
        length(endpoint_id) BETWEEN 1 AND 256
        AND endpoint_id = trim(endpoint_id)
    ),
    cursor_scope_key TEXT NOT NULL CHECK (
        length(cursor_scope_key) BETWEEN 1 AND 256
        AND cursor_scope_key = trim(cursor_scope_key)
    ),
    cursor_revision INTEGER NOT NULL CHECK (cursor_revision >= 0),
    cursor_before_ref TEXT REFERENCES content_records(content_digest),
    cursor_after_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    endpoint_binding_digest TEXT NOT NULL CHECK (
        length(endpoint_binding_digest) = 64
        AND endpoint_binding_digest = lower(endpoint_binding_digest)
        AND endpoint_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    disposition TEXT NOT NULL CHECK (
        disposition IN ('CURSOR_SEED', 'ACCEPTED', 'REJECTED')
    ),
    reason TEXT NOT NULL CHECK (
        length(reason) BETWEEN 1 AND 256 AND reason = trim(reason)
    ),
    ingress_key TEXT CHECK (
        ingress_key IS NULL OR (
            length(ingress_key) = 64
            AND ingress_key = lower(ingress_key)
            AND ingress_key NOT GLOB '*[^0-9a-f]*'
        )
    ),
    provider_event_id_digest TEXT CHECK (
        provider_event_id_digest IS NULL OR (
            length(provider_event_id_digest) = 64
            AND provider_event_id_digest = lower(provider_event_id_digest)
            AND provider_event_id_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    envelope_ref TEXT REFERENCES content_records(content_digest),
    envelope_digest TEXT CHECK (
        envelope_digest IS NULL OR envelope_digest = envelope_ref
    ),
    principal_id TEXT CHECK (
        principal_id IS NULL OR (
            length(principal_id) BETWEEN 1 AND 256
            AND principal_id = trim(principal_id)
        )
    ),
    acl_epoch INTEGER CHECK (acl_epoch IS NULL OR acl_epoch > 0),
    admission_key TEXT CHECK (
        admission_key IS NULL OR (
            length(admission_key) BETWEEN 1 AND 256
            AND admission_key = trim(admission_key)
        )
    ),
    run_id TEXT UNIQUE REFERENCES runs(run_id),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    PRIMARY KEY (
        tenant_id, endpoint_id, cursor_scope_key, cursor_revision
    ),
    UNIQUE (tenant_id, endpoint_id, ingress_key),
    UNIQUE (tenant_id, endpoint_id, provider_event_id_digest),
    CHECK (
        (disposition = 'CURSOR_SEED'
            AND cursor_revision = 0
            AND cursor_before_ref IS NULL
            AND ingress_key IS NULL
            AND provider_event_id_digest IS NULL
            AND envelope_ref IS NULL
            AND envelope_digest IS NULL
            AND principal_id IS NULL
            AND acl_epoch IS NULL
            AND admission_key IS NULL
            AND run_id IS NULL)
        OR (disposition = 'REJECTED'
            AND cursor_revision > 0
            AND cursor_before_ref IS NOT NULL
            AND ingress_key IS NOT NULL
            AND provider_event_id_digest IS NOT NULL
            AND envelope_ref IS NOT NULL
            AND envelope_digest IS NOT NULL
            AND principal_id IS NULL
            AND acl_epoch IS NULL
            AND admission_key IS NULL
            AND run_id IS NULL)
        OR (disposition = 'ACCEPTED'
            AND cursor_revision > 0
            AND cursor_before_ref IS NOT NULL
            AND ingress_key IS NOT NULL
            AND provider_event_id_digest IS NOT NULL
            AND envelope_ref IS NOT NULL
            AND envelope_digest IS NOT NULL
            AND principal_id IS NOT NULL
            AND acl_epoch IS NOT NULL
            AND admission_key IS NOT NULL
            AND run_id IS NOT NULL)
    )
) STRICT;

CREATE TABLE member_execution_snapshots (
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    member_id TEXT NOT NULL CHECK (
        length(member_id) BETWEEN 1 AND 256 AND member_id = trim(member_id)
    ),
    agent_id TEXT NOT NULL CHECK (
        length(agent_id) BETWEEN 1 AND 256 AND agent_id = trim(agent_id)
    ),
    agent_version TEXT NOT NULL CHECK (
        length(agent_version) BETWEEN 1 AND 64
        AND agent_version = trim(agent_version)
    ),
    agent_digest TEXT NOT NULL CHECK (
        length(agent_digest) = 64
        AND agent_digest = lower(agent_digest)
        AND agent_digest NOT GLOB '*[^0-9a-f]*'
    ),
    profile_id TEXT NOT NULL CHECK (
        length(profile_id) BETWEEN 1 AND 256 AND profile_id = trim(profile_id)
    ),
    profile_version TEXT NOT NULL CHECK (
        length(profile_version) BETWEEN 1 AND 64
        AND profile_version = trim(profile_version)
    ),
    profile_digest TEXT NOT NULL CHECK (
        length(profile_digest) = 64
        AND profile_digest = lower(profile_digest)
        AND profile_digest NOT GLOB '*[^0-9a-f]*'
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id = trim(workspace_id)
    ),
    workspace_version TEXT NOT NULL CHECK (
        length(workspace_version) BETWEEN 1 AND 64
        AND workspace_version = trim(workspace_version)
    ),
    workspace_digest TEXT NOT NULL CHECK (
        length(workspace_digest) = 64
        AND workspace_digest = lower(workspace_digest)
        AND workspace_digest NOT GLOB '*[^0-9a-f]*'
    ),
    control_snapshot_id TEXT NOT NULL
        REFERENCES control_snapshots(snapshot_id),
    catalog_generation_id TEXT NOT NULL
        REFERENCES runtime_catalog_generations(generation_id),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) <= 67108864),
    digest TEXT NOT NULL CHECK (
        length(digest) = 64
        AND digest = lower(digest)
        AND digest NOT GLOB '*[^0-9a-f]*'
    ),
    PRIMARY KEY (run_id, member_id),
    UNIQUE (run_id, member_id, digest)
) STRICT;

CREATE TABLE run_manifests (
    run_id TEXT PRIMARY KEY REFERENCES runs(run_id),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) <= 67108864),
    digest TEXT NOT NULL UNIQUE CHECK (
        length(digest) = 64
        AND digest = lower(digest)
        AND digest NOT GLOB '*[^0-9a-f]*'
    ),
    published_at INTEGER NOT NULL CHECK (published_at > 0),
    UNIQUE (run_id, digest)
) STRICT;

CREATE TRIGGER content_records_reject_update
BEFORE UPDATE ON content_records
BEGIN
    SELECT RAISE(ABORT, 'Content records are immutable');
END;

CREATE TRIGGER content_records_reject_delete
BEFORE DELETE ON content_records
BEGIN
    SELECT RAISE(ABORT, 'Content records are append-only');
END;

CREATE TRIGGER run_manifests_reject_update
BEFORE UPDATE ON run_manifests
BEGIN
    SELECT RAISE(ABORT, 'Run manifests are immutable');
END;

CREATE TRIGGER run_manifests_reject_delete
BEFORE DELETE ON run_manifests
BEGIN
    SELECT RAISE(ABORT, 'Run manifests are append-only');
END;

CREATE TRIGGER member_execution_snapshots_reject_update
BEFORE UPDATE ON member_execution_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Member execution snapshots are immutable');
END;

CREATE TRIGGER member_execution_snapshots_reject_delete
BEFORE DELETE ON member_execution_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Member execution snapshots are append-only');
END;

CREATE TABLE model_price_snapshots (
    price_snapshot_id TEXT PRIMARY KEY CHECK (
        length(price_snapshot_id) BETWEEN 1 AND 256
        AND price_snapshot_id = trim(price_snapshot_id)
    ),
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    billing_version TEXT NOT NULL CHECK (length(trim(billing_version)) > 0),
    currency TEXT NOT NULL CHECK (length(trim(currency)) > 0),
    pricing_status TEXT NOT NULL CHECK (length(trim(pricing_status)) > 0),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) <= 1048576),
    digest TEXT NOT NULL UNIQUE CHECK (
        length(digest) = 64
        AND digest = lower(digest)
        AND digest NOT GLOB '*[^0-9a-f]*'
    )
) STRICT;

CREATE TABLE model_dispatch_attempts (
    attempt_id TEXT PRIMARY KEY CHECK (
        length(attempt_id) BETWEEN 1 AND 256 AND attempt_id = trim(attempt_id)
    ),
    logical_operation_key TEXT NOT NULL UNIQUE CHECK (
        length(logical_operation_key) = 64
        AND logical_operation_key = lower(logical_operation_key)
        AND logical_operation_key NOT GLOB '*[^0-9a-f]*'
    ),
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id)
    ),
    member_id TEXT NOT NULL,
    logical_step_id TEXT NOT NULL CHECK (
        length(logical_step_id) BETWEEN 1 AND 256
        AND logical_step_id = trim(logical_step_id)
    ),
    frame_revision INTEGER NOT NULL CHECK (frame_revision >= 0),
    member_snapshot_digest TEXT NOT NULL CHECK (
        length(member_snapshot_digest) = 64
        AND member_snapshot_digest = lower(member_snapshot_digest)
        AND member_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    binding_json BLOB NOT NULL CHECK (length(binding_json) <= 65536),
    context_compilation_ref TEXT REFERENCES content_records(content_digest),
    request_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    request_digest TEXT NOT NULL CHECK (request_digest = request_ref),
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    parameters_json BLOB NOT NULL CHECK (length(parameters_json) <= 65536),
    deadline INTEGER NOT NULL CHECK (deadline > 0),
    budget_json BLOB NOT NULL CHECK (length(budget_json) <= 1048576),
    billing_version TEXT NOT NULL CHECK (length(trim(billing_version)) > 0),
    price_snapshot_id TEXT NOT NULL
        REFERENCES model_price_snapshots(price_snapshot_id),
    source_dispatch_attempt_id TEXT UNIQUE
        REFERENCES dispatch_attempts(attempt_id),
    state TEXT NOT NULL CHECK (
        state IN ('PENDING', 'SUCCEEDED', 'FAILED', 'MODEL_UNKNOWN')
    ),
    provider_request_id TEXT,
    provider_receipt_ref TEXT REFERENCES content_records(content_digest),
    result_ref TEXT REFERENCES content_records(content_digest),
    error_classification TEXT,
    reconciliation_evidence_ref TEXT
        REFERENCES content_records(content_digest),
    unknown_reason TEXT,
    revision INTEGER NOT NULL CHECK (revision >= 0),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at > 0),
    overview_resource_kind TEXT NOT NULL DEFAULT 'MODEL' CHECK (
        overview_resource_kind='MODEL'
    ),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence>0
    ),
    UNIQUE (run_id, member_id, logical_step_id),
    FOREIGN KEY (run_id, member_id)
        REFERENCES member_execution_snapshots(run_id, member_id),
    FOREIGN KEY (run_id, tenant_id, workspace_id)
        REFERENCES runs(run_id, tenant_id, workspace_id),
    FOREIGN KEY (
        overview_resource_kind,attempt_id,state,revision,updated_at
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,state,resource_revision,updated_at
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (overview_resource_kind,attempt_id)
        REFERENCES overview_resource_heads(resource_kind,resource_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        overview_resource_kind,attempt_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        (state = 'PENDING'
            AND result_ref IS NULL
            AND error_classification IS NULL
            AND reconciliation_evidence_ref IS NULL
            AND unknown_reason IS NULL)
        OR (state = 'SUCCEEDED'
            AND result_ref IS NOT NULL
            AND error_classification IS NULL
            AND unknown_reason IS NULL)
        OR (state = 'FAILED'
            AND error_classification IS NOT NULL
            AND result_ref IS NULL
            AND unknown_reason IS NULL)
        OR (state = 'MODEL_UNKNOWN'
            AND result_ref IS NULL
            AND error_classification IS NULL
            AND (
                provider_request_id IS NOT NULL
                OR provider_receipt_ref IS NOT NULL
                OR reconciliation_evidence_ref IS NOT NULL
                OR unknown_reason IS NOT NULL
            ))
    )
) STRICT;

CREATE TABLE model_usage (
    attempt_id TEXT PRIMARY KEY
        REFERENCES model_dispatch_attempts(attempt_id),
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    ledger_sequence INTEGER CHECK (ledger_sequence IS NULL OR ledger_sequence >= 0),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    cached_input_tokens INTEGER CHECK (
        cached_input_tokens IS NULL OR cached_input_tokens >= 0
    ),
    uncached_input_tokens INTEGER CHECK (
        uncached_input_tokens IS NULL OR uncached_input_tokens >= 0
    ),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    reasoning_tokens INTEGER CHECK (
        reasoning_tokens IS NULL OR reasoning_tokens >= 0
    ),
    estimated_cost TEXT,
    provider_reported_cost TEXT,
    reconciled_cost TEXT,
    reconciliation_status TEXT NOT NULL CHECK (
        length(trim(reconciliation_status)) > 0
    ),
    raw_receipt_ref TEXT REFERENCES content_records(content_digest),
    overview_resource_kind TEXT NOT NULL DEFAULT 'MODEL' CHECK (
        overview_resource_kind='MODEL'
    ),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence>0
    ),
    UNIQUE (run_id, ledger_sequence),
    FOREIGN KEY (
        overview_resource_kind,attempt_id,revision,reconciliation_status,
        ledger_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,usage_revision,usage_status,
        usage_ledger_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        overview_resource_kind,attempt_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        input_tokens IS NULL
        OR cached_input_tokens IS NULL
        OR uncached_input_tokens IS NULL
        OR input_tokens = cached_input_tokens + uncached_input_tokens
    )
) STRICT;

CREATE TRIGGER model_dispatch_attempts_observation_update_guard
BEFORE UPDATE ON model_dispatch_attempts
WHEN NEW.attempt_id<>OLD.attempt_id
  OR NEW.logical_operation_key<>OLD.logical_operation_key
  OR NEW.run_id<>OLD.run_id OR NEW.tenant_id<>OLD.tenant_id
  OR NEW.workspace_id<>OLD.workspace_id OR NEW.member_id<>OLD.member_id
  OR NEW.logical_step_id<>OLD.logical_step_id
  OR NEW.frame_revision<>OLD.frame_revision
  OR NEW.member_snapshot_digest<>OLD.member_snapshot_digest
  OR NEW.binding_json<>OLD.binding_json
  OR NEW.context_compilation_ref IS NOT OLD.context_compilation_ref
  OR NEW.request_ref<>OLD.request_ref OR NEW.request_digest<>OLD.request_digest
  OR NEW.provider<>OLD.provider OR NEW.model<>OLD.model
  OR NEW.parameters_json<>OLD.parameters_json OR NEW.deadline<>OLD.deadline
  OR NEW.budget_json<>OLD.budget_json OR NEW.billing_version<>OLD.billing_version
  OR NEW.price_snapshot_id<>OLD.price_snapshot_id
  OR NEW.source_dispatch_attempt_id IS NOT OLD.source_dispatch_attempt_id
  OR NEW.created_at<>OLD.created_at
  OR NEW.overview_resource_kind<>OLD.overview_resource_kind
  OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
  OR NEW.overview_observation_sequence<>OLD.overview_observation_sequence+1
BEGIN
    SELECT RAISE(ABORT, 'invalid model attempt observation update');
END;

CREATE TRIGGER model_usage_observation_update_guard
BEFORE UPDATE ON model_usage
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.run_id<>OLD.run_id
  OR NEW.overview_resource_kind<>OLD.overview_resource_kind
  OR NEW.revision<>OLD.revision+1
  OR NEW.overview_observation_sequence<>OLD.overview_observation_sequence+1
BEGIN
    SELECT RAISE(ABORT, 'invalid model usage observation update');
END;

CREATE TABLE agent_memory_revisions (
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    agent_id TEXT NOT NULL CHECK (
        length(agent_id) BETWEEN 1 AND 256 AND agent_id = trim(agent_id)
    ),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    snapshot_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    source_attempt_id TEXT REFERENCES model_dispatch_attempts(attempt_id),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    PRIMARY KEY (tenant_id, agent_id, revision),
    UNIQUE (snapshot_ref),
    UNIQUE (source_attempt_id),
    CHECK (
        (revision = 1 AND source_attempt_id IS NULL)
        OR (revision > 1 AND source_attempt_id IS NOT NULL)
    )
) STRICT;

CREATE TABLE dispatch_attempts (
    attempt_id TEXT PRIMARY KEY CHECK (
        length(attempt_id) BETWEEN 1 AND 256 AND attempt_id = trim(attempt_id)
    ),
    dispatch_kind TEXT NOT NULL CHECK (
        dispatch_kind IN ('ACTION', 'CHANNEL_SEND')
    ),
    logical_operation_key TEXT NOT NULL UNIQUE CHECK (
        length(logical_operation_key) = 64
        AND logical_operation_key = lower(logical_operation_key)
        AND logical_operation_key NOT GLOB '*[^0-9a-f]*'
    ),
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id)
    ),
    member_id TEXT NOT NULL CHECK (
        length(member_id) BETWEEN 1 AND 256 AND member_id = trim(member_id)
    ),
    logical_step_id TEXT NOT NULL CHECK (
        length(logical_step_id) BETWEEN 1 AND 256
        AND logical_step_id = trim(logical_step_id)
    ),
    source_model_attempt_id TEXT NOT NULL UNIQUE
        REFERENCES model_dispatch_attempts(attempt_id),
    frame_revision INTEGER NOT NULL CHECK (frame_revision >= 0),
    member_snapshot_digest TEXT NOT NULL CHECK (
        length(member_snapshot_digest) = 64
        AND member_snapshot_digest = lower(member_snapshot_digest)
        AND member_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    binding_index INTEGER NOT NULL CHECK (binding_index >= 0),
    binding_json BLOB NOT NULL CHECK (length(binding_json) <= 65536),
    public_action_id TEXT CHECK (
        public_action_id IS NULL OR (
            length(public_action_id) BETWEEN 1 AND 256
            AND public_action_id = trim(public_action_id)
        )
    ),
    provider_action_id TEXT CHECK (
        provider_action_id IS NULL OR (
            length(provider_action_id) BETWEEN 1 AND 256
            AND provider_action_id = trim(provider_action_id)
        )
    ),
    definition_digest TEXT CHECK (
        definition_digest IS NULL OR (
            length(definition_digest) = 64
            AND definition_digest = lower(definition_digest)
            AND definition_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    proposal_ref TEXT UNIQUE
        REFERENCES content_records(content_digest),
    channel_endpoint_id TEXT CHECK (
        channel_endpoint_id IS NULL OR (
            length(channel_endpoint_id) BETWEEN 1 AND 256
            AND channel_endpoint_id = trim(channel_endpoint_id)
        )
    ),
    channel_ingress_key TEXT CHECK (
        channel_ingress_key IS NULL OR (
            length(channel_ingress_key) = 64
            AND channel_ingress_key = lower(channel_ingress_key)
            AND channel_ingress_key NOT GLOB '*[^0-9a-f]*'
        )
    ),
    channel_proposal_ref TEXT UNIQUE
        REFERENCES content_records(content_digest),
    effect_class TEXT NOT NULL CHECK (effect_class IN (
        'none', 'read_only', 'reversible_write', 'irreversible_write'
    )),
    max_result_bytes INTEGER NOT NULL CHECK (
        max_result_bytes BETWEEN 1 AND 65536
    ),
    deadline INTEGER NOT NULL CHECK (deadline > 0),
    budget_state_ref TEXT NOT NULL CHECK (
        length(budget_state_ref) BETWEEN 1 AND 292
        AND budget_state_ref = trim(budget_state_ref)
    ),
    state TEXT NOT NULL CHECK (
        state IN ('PENDING', 'SUCCEEDED', 'FAILED', 'UNKNOWN')
    ),
    external_operation_id TEXT CHECK (
        external_operation_id IS NULL
        OR (
            length(external_operation_id) BETWEEN 1 AND 256
            AND external_operation_id = trim(external_operation_id)
        )
    ),
    provider_receipt_ref TEXT REFERENCES content_records(content_digest),
    result_ref TEXT REFERENCES content_records(content_digest),
    error_classification TEXT CHECK (
        error_classification IS NULL
        OR (
            length(error_classification) BETWEEN 1 AND 256
            AND error_classification = trim(error_classification)
        )
    ),
    reconciliation_evidence_ref TEXT
        REFERENCES content_records(content_digest),
    unknown_reason TEXT CHECK (
        unknown_reason IS NULL
        OR (
            length(unknown_reason) BETWEEN 1 AND 256
            AND unknown_reason = trim(unknown_reason)
        )
    ),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at > 0),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence>0
    ),
    UNIQUE (run_id, member_id, logical_step_id),
    FOREIGN KEY (run_id, member_id)
        REFERENCES member_execution_snapshots(run_id, member_id),
    FOREIGN KEY (run_id, tenant_id, workspace_id)
        REFERENCES runs(run_id, tenant_id, workspace_id),
    FOREIGN KEY (
        dispatch_kind,attempt_id,state,revision,updated_at
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,state,resource_revision,updated_at
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (dispatch_kind,attempt_id)
        REFERENCES overview_resource_heads(resource_kind,resource_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        dispatch_kind,attempt_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        (dispatch_kind = 'ACTION'
            AND public_action_id IS NOT NULL
            AND provider_action_id IS NOT NULL
            AND definition_digest IS NOT NULL
            AND proposal_ref IS NOT NULL
            AND channel_endpoint_id IS NULL
            AND channel_ingress_key IS NULL
            AND channel_proposal_ref IS NULL)
        OR (dispatch_kind = 'CHANNEL_SEND'
            AND public_action_id IS NULL
            AND provider_action_id IS NULL
            AND definition_digest IS NULL
            AND proposal_ref IS NULL
            AND channel_endpoint_id IS NOT NULL
            AND channel_ingress_key IS NOT NULL
            AND channel_proposal_ref IS NOT NULL)
    ),
    CHECK (
        (state = 'PENDING'
            AND external_operation_id IS NULL
            AND provider_receipt_ref IS NULL
            AND result_ref IS NULL
            AND error_classification IS NULL
            AND reconciliation_evidence_ref IS NULL
            AND unknown_reason IS NULL)
        OR (state = 'SUCCEEDED'
            AND result_ref IS NOT NULL
            AND error_classification IS NULL
            AND unknown_reason IS NULL)
        OR (state = 'FAILED'
            AND result_ref IS NULL
            AND error_classification IS NOT NULL
            AND unknown_reason IS NULL)
        OR (state = 'UNKNOWN'
            AND result_ref IS NULL
            AND error_classification IS NULL
            AND (
                external_operation_id IS NOT NULL
                OR provider_receipt_ref IS NOT NULL
                OR reconciliation_evidence_ref IS NOT NULL
                OR unknown_reason IS NOT NULL
            ))
    )
) STRICT;

CREATE TRIGGER dispatch_attempts_observation_update_guard
BEFORE UPDATE ON dispatch_attempts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.dispatch_kind<>OLD.dispatch_kind
  OR NEW.logical_operation_key<>OLD.logical_operation_key
  OR NEW.run_id<>OLD.run_id OR NEW.tenant_id<>OLD.tenant_id
  OR NEW.workspace_id<>OLD.workspace_id OR NEW.member_id<>OLD.member_id
  OR NEW.logical_step_id<>OLD.logical_step_id
  OR NEW.source_model_attempt_id<>OLD.source_model_attempt_id
  OR NEW.frame_revision<>OLD.frame_revision
  OR NEW.member_snapshot_digest<>OLD.member_snapshot_digest
  OR NEW.binding_index<>OLD.binding_index OR NEW.binding_json<>OLD.binding_json
  OR NEW.public_action_id IS NOT OLD.public_action_id
  OR NEW.provider_action_id IS NOT OLD.provider_action_id
  OR NEW.definition_digest IS NOT OLD.definition_digest
  OR NEW.proposal_ref IS NOT OLD.proposal_ref
  OR NEW.channel_endpoint_id IS NOT OLD.channel_endpoint_id
  OR NEW.channel_ingress_key IS NOT OLD.channel_ingress_key
  OR NEW.channel_proposal_ref IS NOT OLD.channel_proposal_ref
  OR NEW.effect_class<>OLD.effect_class
  OR NEW.max_result_bytes<>OLD.max_result_bytes OR NEW.deadline<>OLD.deadline
  OR NEW.budget_state_ref<>OLD.budget_state_ref
  OR NEW.created_at<>OLD.created_at
  OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
  OR NEW.overview_observation_sequence<>OLD.overview_observation_sequence+1
BEGIN
    SELECT RAISE(ABORT, 'invalid dispatch observation update');
END;

CREATE INDEX dispatch_attempts_recovery_idx
    ON dispatch_attempts(dispatch_kind, state, run_id, attempt_id);

CREATE TABLE loop_frames (
    run_id TEXT PRIMARY KEY REFERENCES runs(run_id),
    frame_revision INTEGER NOT NULL CHECK (frame_revision >= 0),
    step TEXT NOT NULL CHECK (length(trim(step)) > 0),
    budget_state_ref TEXT NOT NULL CHECK (length(trim(budget_state_ref)) > 0),
    continuation BLOB NOT NULL CHECK (
        length(continuation) BETWEEN 2 AND 2048
    ),
    pending_attempt_id TEXT REFERENCES model_dispatch_attempts(attempt_id),
    pending_dispatch_attempt_id TEXT REFERENCES dispatch_attempts(attempt_id),
    waiting_reason TEXT,
    last_authoritative_event INTEGER NOT NULL CHECK (
        last_authoritative_event >= 0
    ),
    lease_owner TEXT,
    lease_epoch INTEGER NOT NULL CHECK (lease_epoch >= 0),
    lease_expiry INTEGER,
    CHECK (
        pending_attempt_id IS NULL OR pending_dispatch_attempt_id IS NULL
    ),
    CHECK (
        (lease_owner IS NULL AND lease_expiry IS NULL)
        OR (
            lease_owner IS NOT NULL
            AND length(trim(lease_owner)) > 0
            AND lease_epoch > 0
            AND lease_expiry IS NOT NULL
        )
    )
) STRICT;

CREATE TABLE run_events (
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    event_sequence INTEGER NOT NULL CHECK (event_sequence >= 0),
    event_kind TEXT NOT NULL CHECK (length(trim(event_kind)) > 0),
    from_revision INTEGER NOT NULL CHECK (from_revision >= 0),
    to_revision INTEGER NOT NULL CHECK (to_revision >= 0),
    payload_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    payload_digest TEXT NOT NULL CHECK (payload_digest = payload_ref),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    PRIMARY KEY (run_id, event_sequence),
    UNIQUE (
        run_id, event_sequence, event_kind, to_revision, payload_digest
    )
) STRICT;

CREATE TABLE run_observation_snapshots (
    snapshot_digest TEXT PRIMARY KEY CHECK (
        length(snapshot_digest) = 64
        AND snapshot_digest = lower(snapshot_digest)
        AND snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    observation_sequence INTEGER NOT NULL CHECK (observation_sequence > 0),
    previous_snapshot_digest TEXT,
    transition_kind TEXT NOT NULL CHECK (transition_kind IN (
        'ADMISSION',
        'MODEL_BEGIN',
        'MODEL_OUTCOME',
        'ACTION_BEGIN',
        'ACTION_OUTCOME',
        'CHANNEL_BEGIN',
        'CHANNEL_OUTCOME',
        'MODEL_ACTION_REJECTION',
        'CORE_FAILURE',
        'STARTUP_RECOVERY',
        'COMPOSITE_TRANSITION',
        'CANCELLATION'
    )),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id=trim(store_instance_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id=trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id=trim(workspace_id)
    ),
    admission_key TEXT NOT NULL CHECK (
        length(admission_key) BETWEEN 1 AND 256
        AND admission_key=trim(admission_key)
    ),
    admission_intent_digest TEXT NOT NULL CHECK (
        length(admission_intent_digest)=64
        AND admission_intent_digest=lower(admission_intent_digest)
        AND admission_intent_digest NOT GLOB '*[^0-9a-f]*'
    ),
    manifest_digest TEXT NOT NULL CHECK (
        length(manifest_digest)=64
        AND manifest_digest=lower(manifest_digest)
        AND manifest_digest NOT GLOB '*[^0-9a-f]*'
    ),
    member_id TEXT NOT NULL CHECK (
        length(member_id) BETWEEN 1 AND 256 AND member_id=trim(member_id)
    ),
    member_digest TEXT NOT NULL CHECK (
        length(member_digest)=64
        AND member_digest=lower(member_digest)
        AND member_digest NOT GLOB '*[^0-9a-f]*'
    ),
    has_action_port INTEGER NOT NULL CHECK (has_action_port IN (0,1)),
    has_channel_port INTEGER NOT NULL CHECK (has_channel_port IN (0,1)),
    run_state TEXT NOT NULL CHECK (
        run_state IN ('ADMITTED','WAITING_RECONCILIATION','TERMINATED')
    ),
    disposition TEXT,
    run_revision INTEGER NOT NULL CHECK (run_revision >= 0),
    cancel_request_ref TEXT CHECK (
        cancel_request_ref IS NULL OR (
            length(cancel_request_ref)=64
            AND cancel_request_ref=lower(cancel_request_ref)
            AND cancel_request_ref NOT GLOB '*[^0-9a-f]*'
        )
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    frame_step TEXT NOT NULL CHECK (frame_step IN (
        'READY','WAITING_CHILDREN','WAITING_REPAIR_ACTIVATION',
        'MODEL_PENDING','ACTION_PENDING','CHANNEL_PENDING',
        'MODEL_READY_AFTER_ACTION','WAITING_RECONCILIATION','TERMINATED'
    )),
    budget_state_ref TEXT NOT NULL CHECK (
        length(budget_state_ref) BETWEEN 1 AND 292
        AND budget_state_ref=trim(budget_state_ref)
    ),
    continuation_digest TEXT NOT NULL CHECK (
        length(continuation_digest)=64
        AND continuation_digest=lower(continuation_digest)
        AND continuation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    continuation_size_bytes INTEGER NOT NULL CHECK (
        continuation_size_bytes BETWEEN 2 AND 2048
    ),
    continuation_attempt_kind TEXT CHECK (
        continuation_attempt_kind IS NULL
        OR continuation_attempt_kind IN ('MODEL','ACTION','CHANNEL')
    ),
    continuation_logical_step_id TEXT CHECK (
        continuation_logical_step_id IS NULL OR (
            length(continuation_logical_step_id) BETWEEN 1 AND 256
            AND continuation_logical_step_id=trim(continuation_logical_step_id)
        )
    ),
    continuation_attempt_id TEXT CHECK (
        continuation_attempt_id IS NULL OR (
            length(continuation_attempt_id) BETWEEN 1 AND 256
            AND continuation_attempt_id=trim(continuation_attempt_id)
        )
    ),
    continuation_core_failure_reason TEXT CHECK (
        continuation_core_failure_reason IS NULL OR (
            length(continuation_core_failure_reason) BETWEEN 1 AND 256
            AND continuation_core_failure_reason=
                trim(continuation_core_failure_reason)
        )
    ),
    pending_model_attempt_id TEXT CHECK (
        pending_model_attempt_id IS NULL OR (
            length(pending_model_attempt_id) BETWEEN 1 AND 256
            AND pending_model_attempt_id=trim(pending_model_attempt_id)
        )
    ),
    pending_dispatch_attempt_id TEXT CHECK (
        pending_dispatch_attempt_id IS NULL OR (
            length(pending_dispatch_attempt_id) BETWEEN 1 AND 256
            AND pending_dispatch_attempt_id=trim(pending_dispatch_attempt_id)
        )
    ),
    waiting_reason TEXT CHECK (
        waiting_reason IS NULL OR (
            length(waiting_reason) BETWEEN 1 AND 256
            AND waiting_reason=trim(waiting_reason)
        )
    ),
    source_event_sequence INTEGER NOT NULL CHECK (source_event_sequence >= 0),
    source_event_frame_revision INTEGER NOT NULL CHECK (
        source_event_frame_revision >= 0
    ),
    source_event_kind TEXT NOT NULL CHECK (
        length(source_event_kind) BETWEEN 1 AND 128
        AND source_event_kind=trim(source_event_kind)
    ),
    source_event_payload_digest TEXT NOT NULL CHECK (
        length(source_event_payload_digest)=64
        AND source_event_payload_digest=lower(source_event_payload_digest)
        AND source_event_payload_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_event_attempt_id TEXT CHECK (
        source_event_attempt_id IS NULL OR (
            length(source_event_attempt_id) BETWEEN 1 AND 256
            AND source_event_attempt_id=trim(source_event_attempt_id)
        )
    ),
    source_event_logical_step_id TEXT CHECK (
        source_event_logical_step_id IS NULL OR (
            length(source_event_logical_step_id) BETWEEN 1 AND 256
            AND source_event_logical_step_id=trim(source_event_logical_step_id)
        )
    ),
    canonical_json BLOB NOT NULL CHECK (
        length(canonical_json) BETWEEN 2 AND 16384
    ),
    UNIQUE (run_id, observation_sequence),
    UNIQUE (run_id, snapshot_digest),
    UNIQUE (run_id, observation_sequence, snapshot_digest),
    FOREIGN KEY (run_id, tenant_id, workspace_id)
        REFERENCES runs(run_id, tenant_id, workspace_id),
    FOREIGN KEY (run_id, manifest_digest)
        REFERENCES run_manifests(run_id, digest),
    FOREIGN KEY (run_id, member_id, member_digest)
        REFERENCES member_execution_snapshots(run_id, member_id, digest),
    FOREIGN KEY (run_id, previous_snapshot_digest)
        REFERENCES run_observation_snapshots(run_id, snapshot_digest),
    FOREIGN KEY (
        run_id,
        source_event_sequence,
        source_event_kind,
        source_event_frame_revision,
        source_event_payload_digest
    ) REFERENCES run_events(
        run_id, event_sequence, event_kind, to_revision, payload_digest
    ),
    CHECK (
        (observation_sequence = 1
            AND previous_snapshot_digest IS NULL
            AND transition_kind = 'ADMISSION'
            AND source_event_sequence = 0)
        OR (observation_sequence > 1
            AND previous_snapshot_digest IS NOT NULL
            AND transition_kind <> 'ADMISSION')
    ),
    CHECK (
        pending_model_attempt_id IS NULL
        OR pending_dispatch_attempt_id IS NULL
    ),
    CHECK (
        (source_event_attempt_id IS NULL
            AND source_event_logical_step_id IS NULL)
        OR (source_event_attempt_id IS NOT NULL
            AND source_event_logical_step_id IS NOT NULL)
    ),
    CHECK (
        (continuation_core_failure_reason IS NOT NULL
            AND continuation_attempt_kind IS NULL
            AND continuation_logical_step_id IS NULL
            AND continuation_attempt_id IS NULL)
        OR (continuation_core_failure_reason IS NULL
            AND (
                (frame_step IN (
                    'READY','WAITING_CHILDREN','WAITING_REPAIR_ACTIVATION'
                )
                    AND continuation_attempt_kind IS NULL
                    AND continuation_logical_step_id IS NULL
                    AND continuation_attempt_id IS NULL)
                OR (frame_step NOT IN (
                    'READY','WAITING_CHILDREN','WAITING_REPAIR_ACTIVATION'
                )
                    AND continuation_attempt_kind IS NOT NULL
                    AND continuation_logical_step_id IS NOT NULL
                    AND continuation_attempt_id IS NOT NULL)
            ))
    )
) STRICT;

CREATE TRIGGER run_events_reject_update
BEFORE UPDATE ON run_events
BEGIN
    SELECT RAISE(ABORT, 'Run events are immutable');
END;

CREATE TRIGGER run_events_reject_delete
BEFORE DELETE ON run_events
BEGIN
    SELECT RAISE(ABORT, 'Run events are append-only');
END;

CREATE UNIQUE INDEX run_observation_one_cancellation_idx
    ON run_observation_snapshots(run_id)
    WHERE transition_kind='CANCELLATION';

CREATE TABLE run_observation_heads (
    run_id TEXT PRIMARY KEY REFERENCES runs(run_id),
    observation_sequence INTEGER NOT NULL CHECK (observation_sequence > 0),
    snapshot_digest TEXT NOT NULL UNIQUE,
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id=trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256
        AND workspace_id=trim(workspace_id)
    ),
    run_state TEXT NOT NULL CHECK (
        run_state IN ('ADMITTED','WAITING_RECONCILIATION','TERMINATED')
    ),
    run_revision INTEGER NOT NULL CHECK (run_revision>=0),
    created_at INTEGER NOT NULL CHECK (created_at>0),
    updated_at INTEGER NOT NULL CHECK (updated_at>=created_at),
    UNIQUE(
        run_id,tenant_id,workspace_id,run_state,run_revision,created_at,updated_at
    ),
    FOREIGN KEY (run_id, observation_sequence, snapshot_digest)
        REFERENCES run_observation_snapshots(
            run_id, observation_sequence, snapshot_digest
        )
) STRICT;

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

CREATE TRIGGER run_observation_snapshots_reject_update
BEFORE UPDATE ON run_observation_snapshots
BEGIN
    SELECT RAISE(ABORT, 'run observation snapshots are immutable');
END;

CREATE TRIGGER run_observation_snapshots_reject_delete
BEFORE DELETE ON run_observation_snapshots
BEGIN
    SELECT RAISE(ABORT, 'run observation snapshots are append-only');
END;

CREATE TRIGGER run_observation_heads_reject_reinsert
BEFORE INSERT ON run_observation_heads
WHEN EXISTS (
    SELECT 1 FROM run_observation_heads WHERE run_id=NEW.run_id
)
BEGIN
    SELECT RAISE(ABORT, 'run observation head already exists');
END;

CREATE TRIGGER run_observation_heads_validate_insert
BEFORE INSERT ON run_observation_heads
WHEN NOT EXISTS (
    SELECT 1
    FROM run_observation_snapshots AS snapshot
    JOIN runs AS run ON run.run_id=snapshot.run_id
    JOIN run_manifests AS manifest ON manifest.run_id=run.run_id
    JOIN member_execution_snapshots AS member
      ON member.run_id=run.run_id AND member.member_id=snapshot.member_id
    JOIN loop_frames AS frame ON frame.run_id=run.run_id
    JOIN run_events AS event
      ON event.run_id=run.run_id
     AND event.event_sequence=frame.last_authoritative_event
    WHERE snapshot.run_id=NEW.run_id
      AND snapshot.observation_sequence=NEW.observation_sequence
      AND snapshot.snapshot_digest=NEW.snapshot_digest
      AND NEW.tenant_id=snapshot.tenant_id
      AND NEW.workspace_id=snapshot.workspace_id
      AND NEW.run_state=snapshot.run_state
      AND NEW.run_revision=snapshot.run_revision
      AND NEW.created_at=snapshot.created_at
      AND NEW.updated_at=snapshot.updated_at
      AND snapshot.observation_sequence=1
      AND snapshot.previous_snapshot_digest IS NULL
      AND snapshot.transition_kind='ADMISSION'
      AND snapshot.tenant_id=run.tenant_id
      AND snapshot.workspace_id=run.workspace_id
      AND snapshot.admission_key=run.admission_key
      AND snapshot.admission_intent_digest=run.admission_intent_digest
      AND snapshot.manifest_digest=manifest.digest
      AND snapshot.member_digest=member.digest
      AND snapshot.run_state=run.state
      AND COALESCE(snapshot.disposition, '')=COALESCE(run.disposition, '')
      AND snapshot.run_revision=run.revision
      AND COALESCE(snapshot.cancel_request_ref, '')=COALESCE(run.cancel_request_ref, '')
      AND snapshot.created_at=run.created_at
      AND snapshot.updated_at=run.updated_at
      AND snapshot.frame_step=frame.step
      AND snapshot.budget_state_ref=frame.budget_state_ref
      AND snapshot.continuation_size_bytes=length(frame.continuation)
      AND COALESCE(snapshot.pending_model_attempt_id, '')=
          COALESCE(frame.pending_attempt_id, '')
      AND COALESCE(snapshot.pending_dispatch_attempt_id, '')=
          COALESCE(frame.pending_dispatch_attempt_id, '')
      AND COALESCE(snapshot.waiting_reason, '')=COALESCE(frame.waiting_reason, '')
      AND snapshot.source_event_sequence=frame.last_authoritative_event
      AND snapshot.source_event_frame_revision=event.to_revision
      AND snapshot.source_event_kind=event.event_kind
      AND snapshot.source_event_payload_digest=event.payload_digest
      AND frame.frame_revision >= event.to_revision
)
BEGIN
    SELECT RAISE(ABORT, 'invalid initial run observation head');
END;

CREATE TRIGGER run_observation_heads_validate_update
BEFORE UPDATE ON run_observation_heads
WHEN NEW.run_id<>OLD.run_id
  OR NEW.observation_sequence<>OLD.observation_sequence+1
  OR NOT EXISTS (
    SELECT 1
    FROM run_observation_snapshots AS snapshot
    JOIN run_observation_snapshots AS previous
      ON previous.snapshot_digest=OLD.snapshot_digest
    JOIN runs AS run ON run.run_id=snapshot.run_id
    JOIN run_manifests AS manifest ON manifest.run_id=run.run_id
    JOIN member_execution_snapshots AS member
      ON member.run_id=run.run_id AND member.member_id=snapshot.member_id
    JOIN loop_frames AS frame ON frame.run_id=run.run_id
    JOIN run_events AS event
      ON event.run_id=run.run_id
     AND event.event_sequence=frame.last_authoritative_event
    WHERE snapshot.run_id=NEW.run_id
      AND snapshot.observation_sequence=NEW.observation_sequence
      AND snapshot.snapshot_digest=NEW.snapshot_digest
      AND NEW.tenant_id=snapshot.tenant_id
      AND NEW.workspace_id=snapshot.workspace_id
      AND NEW.run_state=snapshot.run_state
      AND NEW.run_revision=snapshot.run_revision
      AND NEW.created_at=snapshot.created_at
      AND NEW.updated_at=snapshot.updated_at
      AND snapshot.previous_snapshot_digest=OLD.snapshot_digest
      AND snapshot.transition_kind<>'ADMISSION'
      AND snapshot.store_instance_id=previous.store_instance_id
      AND snapshot.run_id=previous.run_id
      AND snapshot.tenant_id=previous.tenant_id
      AND snapshot.workspace_id=previous.workspace_id
      AND snapshot.admission_key=previous.admission_key
      AND snapshot.admission_intent_digest=previous.admission_intent_digest
      AND snapshot.manifest_digest=previous.manifest_digest
      AND snapshot.member_id=previous.member_id
      AND snapshot.member_digest=previous.member_digest
      AND snapshot.has_action_port=previous.has_action_port
      AND snapshot.has_channel_port=previous.has_channel_port
      AND snapshot.created_at=previous.created_at
      AND snapshot.updated_at>=previous.updated_at
      AND snapshot.run_revision>=previous.run_revision
      AND snapshot.run_revision<=previous.run_revision+1
      AND snapshot.tenant_id=run.tenant_id
      AND snapshot.workspace_id=run.workspace_id
      AND snapshot.admission_key=run.admission_key
      AND snapshot.admission_intent_digest=run.admission_intent_digest
      AND snapshot.manifest_digest=manifest.digest
      AND snapshot.member_digest=member.digest
      AND snapshot.run_state=run.state
      AND COALESCE(snapshot.disposition, '')=COALESCE(run.disposition, '')
      AND snapshot.run_revision=run.revision
      AND COALESCE(snapshot.cancel_request_ref, '')=COALESCE(run.cancel_request_ref, '')
      AND snapshot.created_at=run.created_at
      AND snapshot.updated_at=run.updated_at
      AND snapshot.frame_step=frame.step
      AND snapshot.budget_state_ref=frame.budget_state_ref
      AND snapshot.continuation_size_bytes=length(frame.continuation)
      AND COALESCE(snapshot.pending_model_attempt_id, '')=
          COALESCE(frame.pending_attempt_id, '')
      AND COALESCE(snapshot.pending_dispatch_attempt_id, '')=
          COALESCE(frame.pending_dispatch_attempt_id, '')
      AND COALESCE(snapshot.waiting_reason, '')=COALESCE(frame.waiting_reason, '')
      AND snapshot.source_event_sequence=frame.last_authoritative_event
      AND snapshot.source_event_frame_revision=event.to_revision
      AND snapshot.source_event_kind=event.event_kind
      AND snapshot.source_event_payload_digest=event.payload_digest
      AND frame.frame_revision >= event.to_revision
      AND (
        (snapshot.transition_kind='CANCELLATION'
          AND previous.cancel_request_ref IS NULL
          AND snapshot.cancel_request_ref IS NOT NULL
          AND snapshot.source_event_sequence=previous.source_event_sequence
          AND snapshot.source_event_frame_revision=
              previous.source_event_frame_revision
          AND snapshot.source_event_kind=previous.source_event_kind
          AND snapshot.source_event_payload_digest=
              previous.source_event_payload_digest
          AND COALESCE(snapshot.source_event_attempt_id, '')=
              COALESCE(previous.source_event_attempt_id, '')
          AND COALESCE(snapshot.source_event_logical_step_id, '')=
              COALESCE(previous.source_event_logical_step_id, '')
          AND snapshot.tenant_id=previous.tenant_id
          AND snapshot.workspace_id=previous.workspace_id
          AND snapshot.admission_key=previous.admission_key
          AND snapshot.admission_intent_digest=
              previous.admission_intent_digest
          AND snapshot.manifest_digest=previous.manifest_digest
          AND snapshot.member_id=previous.member_id
          AND snapshot.member_digest=previous.member_digest
          AND snapshot.has_action_port=previous.has_action_port
          AND snapshot.has_channel_port=previous.has_channel_port
          AND snapshot.run_state=previous.run_state
          AND COALESCE(snapshot.disposition, '')=
              COALESCE(previous.disposition, '')
          AND snapshot.run_revision=previous.run_revision
          AND snapshot.created_at=previous.created_at
          AND snapshot.frame_step=previous.frame_step
          AND snapshot.budget_state_ref=previous.budget_state_ref
          AND snapshot.continuation_digest=previous.continuation_digest
          AND snapshot.continuation_size_bytes=
              previous.continuation_size_bytes
          AND COALESCE(snapshot.pending_model_attempt_id, '')=
              COALESCE(previous.pending_model_attempt_id, '')
          AND COALESCE(snapshot.pending_dispatch_attempt_id, '')=
              COALESCE(previous.pending_dispatch_attempt_id, '')
          AND COALESCE(snapshot.waiting_reason, '')=
              COALESCE(previous.waiting_reason, ''))
        OR (snapshot.transition_kind<>'CANCELLATION'
          AND COALESCE(snapshot.cancel_request_ref, '')=
              COALESCE(previous.cancel_request_ref, '')
          AND snapshot.source_event_sequence=
              previous.source_event_sequence+1
          AND snapshot.source_event_frame_revision>
              previous.source_event_frame_revision)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'invalid run observation head advance');
END;

CREATE TRIGGER run_observation_heads_reject_delete
BEFORE DELETE ON run_observation_heads
BEGIN
    SELECT RAISE(ABORT, 'run observation head is required');
END;

CREATE INDEX run_observation_heads_overview_tenant_recent_idx
    ON run_observation_heads(
        tenant_id,updated_at DESC,run_id COLLATE BINARY DESC
    );

CREATE INDEX run_observation_heads_overview_workspace_recent_idx
    ON run_observation_heads(
        tenant_id,workspace_id,updated_at DESC,run_id COLLATE BINARY DESC
    );

-- Every mutable resource rendered by the Control Overview has an independent
-- append-only observation chain.  The canonical projection stores bounded
-- scalar/ref facts only; OpenExisting and Backup replay the full typed source
-- closure and prove the semantic digest in both directions.
--
-- A separate immutable transition carrier anchors the exact snapshot that a
-- production mutation published.  This prevents a later coordinated rewrite
-- of only the derived observation chain from substituting a newer Run head or
-- removing a historical mutation.  The circular foreign keys are deferred so
-- the transaction can publish snapshot -> head -> carrier atomically.
CREATE TABLE overview_resource_transition_carriers (
    resource_kind TEXT NOT NULL CHECK (resource_kind IN (
        'MODEL','ACTION','CHANNEL_SEND','LEARNING_PROPOSAL',
        'LEARNING_TASK','MODULE_REVIEW'
    )),
    resource_id TEXT NOT NULL CHECK (
        length(resource_id) BETWEEN 1 AND 256 AND resource_id=trim(resource_id)
    ),
    observation_sequence INTEGER NOT NULL CHECK (observation_sequence>0),
    snapshot_digest TEXT NOT NULL UNIQUE CHECK (
        length(snapshot_digest)=64 AND snapshot_digest=lower(snapshot_digest)
        AND snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id=trim(store_instance_id)
    ),
    PRIMARY KEY(resource_kind,resource_id,observation_sequence),
    UNIQUE(resource_kind,resource_id,observation_sequence,snapshot_digest),
    FOREIGN KEY(resource_kind,resource_id,observation_sequence,snapshot_digest)
        REFERENCES overview_resource_snapshots(
            resource_kind,resource_id,observation_sequence,snapshot_digest
        ) DEFERRABLE INITIALLY DEFERRED
) STRICT;

CREATE TABLE overview_resource_snapshots (
    snapshot_digest TEXT PRIMARY KEY CHECK (
        length(snapshot_digest)=64 AND snapshot_digest=lower(snapshot_digest)
        AND snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    resource_kind TEXT NOT NULL CHECK (resource_kind IN (
        'MODEL','ACTION','CHANNEL_SEND','LEARNING_PROPOSAL',
        'LEARNING_TASK','MODULE_REVIEW'
    )),
    resource_id TEXT NOT NULL CHECK (
        length(resource_id) BETWEEN 1 AND 256 AND resource_id=trim(resource_id)
    ),
    observation_sequence INTEGER NOT NULL CHECK (observation_sequence>0),
    previous_snapshot_digest TEXT,
    transition_kind TEXT NOT NULL CHECK (transition_kind IN (
        'MODEL_BEGIN','MODEL_EXPIRED_BEFORE_NETWORK','MODEL_OUTCOME',
        'MODEL_ACTION_SOURCE_OUTCOME',
        'MODEL_CHANNEL_SOURCE_OUTCOME','MODEL_STARTUP_RECOVERY',
        'ACTION_BEGIN','ACTION_OUTCOME','ACTION_STARTUP_RECOVERY',
        'CHANNEL_BEGIN','CHANNEL_OUTCOME','CHANNEL_STARTUP_RECOVERY',
        'PROPOSAL_SUBMIT','PROPOSAL_REVIEW_ADMISSION',
        'PROPOSAL_REVIEW_FINALIZATION','PROPOSAL_MATERIALIZATION',
        'TASK_CREATE','TASK_RUN_ADMISSION','TASK_FINALIZATION',
        'MODULE_REVIEW_COMMIT'
    )),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id=trim(store_instance_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id=trim(tenant_id)
    ),
    workspace_id TEXT CHECK (
        workspace_id IS NULL OR (
            length(workspace_id) BETWEEN 1 AND 256
            AND workspace_id=trim(workspace_id)
        )
    ),
    subject_run_id TEXT,
    subject_run_observation_sequence INTEGER,
    subject_run_observation_digest TEXT,
    related_run_id TEXT,
    related_run_observation_sequence INTEGER,
    related_run_observation_digest TEXT,
    causal_run_id TEXT,
    causal_run_observation_sequence INTEGER,
    causal_run_observation_digest TEXT,
    state TEXT NOT NULL CHECK (
        length(state) BETWEEN 1 AND 128 AND state=trim(state)
    ),
    binding_target_kind TEXT CHECK (
        binding_target_kind IS NULL OR binding_target_kind IN (
            'PROFILE','WORKSPACE_CHANNEL_ENDPOINT'
        )
    ),
    resource_revision INTEGER NOT NULL CHECK (resource_revision>=0),
    created_at INTEGER NOT NULL CHECK (created_at>0),
    updated_at INTEGER NOT NULL CHECK (updated_at>=created_at),
    semantic_digest TEXT NOT NULL CHECK (
        length(semantic_digest)=64 AND semantic_digest=lower(semantic_digest)
        AND semantic_digest NOT GLOB '*[^0-9a-f]*'
    ),
    usage_revision INTEGER CHECK (usage_revision IS NULL OR usage_revision>=0),
    usage_ledger_sequence INTEGER CHECK (
        usage_ledger_sequence IS NULL OR usage_ledger_sequence>0
    ),
    usage_semantic_digest TEXT CHECK (
        usage_semantic_digest IS NULL OR (
            length(usage_semantic_digest)=64
            AND usage_semantic_digest=lower(usage_semantic_digest)
            AND usage_semantic_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    usage_status TEXT CHECK (
        usage_status IS NULL OR usage_status IN (
            'PENDING','PROVIDER_REPORTED','NO_USAGE_REPORTED',
            'PENDING_RECONCILIATION'
        )
    ),
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens>=0),
    cached_input_tokens INTEGER CHECK (
        cached_input_tokens IS NULL OR cached_input_tokens>=0
    ),
    uncached_input_tokens INTEGER CHECK (
        uncached_input_tokens IS NULL OR uncached_input_tokens>=0
    ),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens>=0),
    reasoning_tokens INTEGER CHECK (
        reasoning_tokens IS NULL OR reasoning_tokens>=0
    ),
    canonical_json BLOB NOT NULL CHECK (
        length(canonical_json) BETWEEN 2 AND 32768
    ),
    UNIQUE(resource_kind,resource_id,observation_sequence),
    UNIQUE(resource_kind,resource_id,snapshot_digest),
    UNIQUE(
        resource_kind,resource_id,observation_sequence,snapshot_digest
    ),
    FOREIGN KEY(resource_kind,resource_id,previous_snapshot_digest)
        REFERENCES overview_resource_snapshots(
            resource_kind,resource_id,snapshot_digest
        ),
    FOREIGN KEY(causal_run_id,causal_run_observation_sequence,causal_run_observation_digest)
        REFERENCES run_observation_snapshots(
            run_id,observation_sequence,snapshot_digest
        ),
    FOREIGN KEY(subject_run_id,subject_run_observation_sequence,subject_run_observation_digest)
        REFERENCES run_observation_snapshots(
            run_id,observation_sequence,snapshot_digest
        ),
    FOREIGN KEY(related_run_id,related_run_observation_sequence,related_run_observation_digest)
        REFERENCES run_observation_snapshots(
            run_id,observation_sequence,snapshot_digest
        ),
    FOREIGN KEY(resource_kind,resource_id,observation_sequence,snapshot_digest)
        REFERENCES overview_resource_transition_carriers(
            resource_kind,resource_id,observation_sequence,snapshot_digest
        ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        (observation_sequence=1 AND previous_snapshot_digest IS NULL)
        OR (observation_sequence>1 AND previous_snapshot_digest IS NOT NULL)
    ),
    CHECK (
        (observation_sequence=1 AND transition_kind IN (
            'MODEL_BEGIN','MODEL_EXPIRED_BEFORE_NETWORK',
            'ACTION_BEGIN','CHANNEL_BEGIN','PROPOSAL_SUBMIT',
            'TASK_CREATE','MODULE_REVIEW_COMMIT'
        ))
        OR observation_sequence>1
    ),
    CHECK (
        (resource_kind='MODEL' AND transition_kind IN (
            'MODEL_BEGIN','MODEL_EXPIRED_BEFORE_NETWORK','MODEL_OUTCOME',
            'MODEL_ACTION_SOURCE_OUTCOME',
            'MODEL_CHANNEL_SOURCE_OUTCOME','MODEL_STARTUP_RECOVERY'
        ) AND state IN ('PENDING','SUCCEEDED','FAILED','MODEL_UNKNOWN'))
        OR (resource_kind='ACTION' AND transition_kind IN (
            'ACTION_BEGIN','ACTION_OUTCOME','ACTION_STARTUP_RECOVERY'
        ) AND state IN ('PENDING','SUCCEEDED','FAILED','UNKNOWN'))
        OR (resource_kind='CHANNEL_SEND' AND transition_kind IN (
            'CHANNEL_BEGIN','CHANNEL_OUTCOME','CHANNEL_STARTUP_RECOVERY'
        ) AND state IN ('PENDING','SUCCEEDED','FAILED','UNKNOWN'))
        OR (resource_kind='LEARNING_PROPOSAL' AND transition_kind IN (
            'PROPOSAL_SUBMIT','PROPOSAL_REVIEW_ADMISSION',
            'PROPOSAL_REVIEW_FINALIZATION','PROPOSAL_MATERIALIZATION'
        ) AND state IN (
            'SUBMITTED','REVIEW_PENDING','APPROVED','REJECTED',
            'REVIEW_FAILED','REVIEW_UNKNOWN'
        ))
        OR (resource_kind='LEARNING_TASK' AND transition_kind IN (
            'TASK_CREATE','TASK_RUN_ADMISSION','TASK_FINALIZATION'
        ) AND state IN (
            'PENDING','RUN_ADMITTED','PROPOSAL_SUBMITTED','NO_CHANGE',
            'SOURCE_OCCUPIED','CONTENT_OCCUPIED','TARGET_OCCUPIED',
            'FAILED','INVALID_RESULT','UNKNOWN'
        ))
        OR (resource_kind='MODULE_REVIEW'
            AND transition_kind='MODULE_REVIEW_COMMIT'
            AND state IN ('WOULD_APPLY','CONFLICT','UNSUPPORTED')
            AND resource_revision=0
            AND observation_sequence=1)
    ),
    CHECK (
        (transition_kind='MODEL_BEGIN' AND resource_revision=0
            AND state='PENDING' AND usage_revision=0
            AND usage_ledger_sequence IS NULL
            AND usage_status='PENDING')
        OR (transition_kind='MODEL_EXPIRED_BEFORE_NETWORK'
            AND resource_revision=0 AND state='FAILED'
            AND usage_revision=0 AND usage_status='NO_USAGE_REPORTED'
            AND usage_ledger_sequence IS NULL
            AND input_tokens IS NULL AND cached_input_tokens IS NULL
            AND uncached_input_tokens IS NULL AND output_tokens IS NULL
            AND reasoning_tokens IS NULL)
        OR transition_kind NOT IN (
            'MODEL_BEGIN','MODEL_EXPIRED_BEFORE_NETWORK'
        )
    ),
    CHECK (
        (transition_kind IN ('ACTION_BEGIN','CHANNEL_BEGIN')
            AND resource_revision=0 AND state='PENDING')
        OR transition_kind NOT IN ('ACTION_BEGIN','CHANNEL_BEGIN')
    ),
    CHECK (
        (transition_kind='PROPOSAL_SUBMIT' AND resource_revision=0
            AND state='SUBMITTED')
        OR (transition_kind='PROPOSAL_REVIEW_ADMISSION'
            AND resource_revision=1 AND state='REVIEW_PENDING')
        OR (transition_kind='PROPOSAL_REVIEW_FINALIZATION'
            AND resource_revision IN (2,3) AND state IN (
                'APPROVED','REJECTED','REVIEW_FAILED','REVIEW_UNKNOWN'
            ))
        OR (transition_kind='PROPOSAL_MATERIALIZATION'
            AND state='APPROVED' AND resource_revision IN (2,3))
        OR transition_kind NOT IN (
            'PROPOSAL_SUBMIT','PROPOSAL_REVIEW_ADMISSION',
            'PROPOSAL_REVIEW_FINALIZATION','PROPOSAL_MATERIALIZATION'
        )
    ),
    CHECK (
        (transition_kind='TASK_CREATE' AND resource_revision=0
            AND state='PENDING')
        OR (transition_kind='TASK_RUN_ADMISSION' AND resource_revision=1
            AND state='RUN_ADMITTED')
        OR (transition_kind='TASK_FINALIZATION'
            AND resource_revision IN (2,3)
            AND state NOT IN ('PENDING','RUN_ADMITTED'))
        OR transition_kind NOT IN (
            'TASK_CREATE','TASK_RUN_ADMISSION','TASK_FINALIZATION'
        )
    ),
    CHECK (
        (resource_kind='MODEL' AND usage_revision IS NOT NULL
            AND usage_semantic_digest IS NOT NULL AND usage_status IS NOT NULL)
        OR (resource_kind<>'MODEL' AND usage_revision IS NULL
            AND usage_ledger_sequence IS NULL
            AND usage_semantic_digest IS NULL
            AND usage_status IS NULL AND input_tokens IS NULL
            AND cached_input_tokens IS NULL AND uncached_input_tokens IS NULL
            AND output_tokens IS NULL AND reasoning_tokens IS NULL)
    ),
    CHECK (
        (resource_kind='MODULE_REVIEW'
            AND binding_target_kind IS NOT NULL
            AND ((binding_target_kind='PROFILE' AND workspace_id IS NULL)
              OR (binding_target_kind='WORKSPACE_CHANNEL_ENDPOINT'
                AND workspace_id IS NOT NULL)))
        OR (resource_kind<>'MODULE_REVIEW'
            AND binding_target_kind IS NULL
            AND workspace_id IS NOT NULL)
    ),
    CHECK (
        input_tokens IS NULL OR cached_input_tokens IS NULL
        OR uncached_input_tokens IS NULL
        OR input_tokens=cached_input_tokens+uncached_input_tokens
    ),
    CHECK (
        (causal_run_id IS NULL AND causal_run_observation_sequence IS NULL
            AND causal_run_observation_digest IS NULL)
        OR (causal_run_id IS NOT NULL
            AND causal_run_observation_sequence IS NOT NULL
            AND causal_run_observation_sequence>0
            AND causal_run_observation_digest IS NOT NULL)
    ),
    CHECK (
        (related_run_id IS NULL AND related_run_observation_sequence IS NULL
            AND related_run_observation_digest IS NULL)
        OR (resource_kind='LEARNING_PROPOSAL' AND related_run_id IS NOT NULL
            AND related_run_observation_sequence IS NOT NULL
            AND related_run_observation_sequence>0
            AND related_run_observation_digest IS NOT NULL)
    ),
    CHECK (
        (resource_kind IN ('MODEL','ACTION','CHANNEL_SEND')
          AND subject_run_id IS NOT NULL
          AND subject_run_observation_sequence IS NOT NULL
          AND subject_run_observation_digest IS NOT NULL
          AND causal_run_id IS NOT NULL
          AND causal_run_observation_sequence IS NOT NULL
          AND causal_run_observation_digest IS NOT NULL
          AND subject_run_id=causal_run_id
          AND subject_run_observation_sequence=
              causal_run_observation_sequence
          AND subject_run_observation_digest=
              causal_run_observation_digest
          AND related_run_id IS NULL
          AND related_run_observation_sequence IS NULL
          AND related_run_observation_digest IS NULL)
        OR (resource_kind='LEARNING_PROPOSAL'
          AND subject_run_id IS NOT NULL
          AND subject_run_observation_sequence IS NOT NULL
          AND subject_run_observation_digest IS NOT NULL
          AND ((transition_kind='PROPOSAL_SUBMIT'
              AND related_run_id IS NULL
              AND causal_run_id=subject_run_id
              AND causal_run_observation_sequence=
                  subject_run_observation_sequence
              AND causal_run_observation_digest=
                  subject_run_observation_digest)
            OR (transition_kind<>'PROPOSAL_SUBMIT'
              AND related_run_id IS NOT NULL
              AND causal_run_id IS NOT NULL
              AND causal_run_observation_sequence IS NOT NULL
              AND causal_run_observation_digest IS NOT NULL
              AND causal_run_id=related_run_id
              AND causal_run_observation_sequence=
                  related_run_observation_sequence
              AND causal_run_observation_digest=
                  related_run_observation_digest)))
        OR (resource_kind='LEARNING_TASK'
          AND ((transition_kind='TASK_CREATE'
            AND subject_run_id IS NULL
            AND subject_run_observation_sequence IS NULL
            AND subject_run_observation_digest IS NULL
            AND causal_run_id IS NULL)
            OR (transition_kind<>'TASK_CREATE'
              AND subject_run_id IS NOT NULL
              AND subject_run_observation_sequence IS NOT NULL
              AND subject_run_observation_digest IS NOT NULL
              AND causal_run_id IS NOT NULL
              AND causal_run_observation_sequence IS NOT NULL
              AND causal_run_observation_digest IS NOT NULL
              AND causal_run_id=subject_run_id
              AND causal_run_observation_sequence=
                  subject_run_observation_sequence
              AND causal_run_observation_digest=
                  subject_run_observation_digest)))
        OR (resource_kind='MODULE_REVIEW' AND subject_run_id IS NULL
          AND subject_run_observation_sequence IS NULL
          AND subject_run_observation_digest IS NULL
          AND related_run_id IS NULL
          AND related_run_observation_sequence IS NULL
          AND related_run_observation_digest IS NULL
          AND causal_run_id IS NULL
          AND causal_run_observation_sequence IS NULL
          AND causal_run_observation_digest IS NULL)
    )
) STRICT;

CREATE TABLE overview_resource_heads (
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    observation_sequence INTEGER NOT NULL CHECK (observation_sequence>0),
    snapshot_digest TEXT NOT NULL UNIQUE,
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id=trim(tenant_id)
    ),
    workspace_id TEXT CHECK (
        workspace_id IS NULL OR (
            length(workspace_id) BETWEEN 1 AND 256
            AND workspace_id=trim(workspace_id)
        )
    ),
    subject_run_id TEXT,
    state TEXT NOT NULL CHECK (
        length(state) BETWEEN 1 AND 128 AND state=trim(state)
    ),
    binding_target_kind TEXT,
    resource_revision INTEGER NOT NULL CHECK (resource_revision>=0),
    created_at INTEGER NOT NULL CHECK (created_at>0),
    updated_at INTEGER NOT NULL CHECK (updated_at>=created_at),
    usage_revision INTEGER CHECK (usage_revision IS NULL OR usage_revision>=0),
    usage_ledger_sequence INTEGER CHECK (
        usage_ledger_sequence IS NULL OR usage_ledger_sequence>0
    ),
    usage_status TEXT,
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens>=0),
    cached_input_tokens INTEGER CHECK (
        cached_input_tokens IS NULL OR cached_input_tokens>=0
    ),
    uncached_input_tokens INTEGER CHECK (
        uncached_input_tokens IS NULL OR uncached_input_tokens>=0
    ),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens>=0),
    reasoning_tokens INTEGER CHECK (
        reasoning_tokens IS NULL OR reasoning_tokens>=0
    ),
    PRIMARY KEY(resource_kind,resource_id),
    UNIQUE(resource_kind,resource_id,observation_sequence),
    UNIQUE(resource_kind,resource_id,state,resource_revision,updated_at),
    UNIQUE(
        resource_kind,resource_id,usage_revision,usage_status,
        usage_ledger_sequence
    ),
    FOREIGN KEY(resource_kind,resource_id,observation_sequence,snapshot_digest)
        REFERENCES overview_resource_snapshots(
            resource_kind,resource_id,observation_sequence,snapshot_digest
        )
) STRICT;

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

CREATE TRIGGER overview_resource_snapshots_reject_update
BEFORE UPDATE ON overview_resource_snapshots
BEGIN
    SELECT RAISE(ABORT, 'overview resource snapshots are immutable');
END;

CREATE TRIGGER overview_resource_snapshots_reject_delete
BEFORE DELETE ON overview_resource_snapshots
BEGIN
    SELECT RAISE(ABORT, 'overview resource snapshots are append-only');
END;

CREATE TRIGGER overview_resource_heads_reject_reinsert
BEFORE INSERT ON overview_resource_heads
WHEN EXISTS (
    SELECT 1 FROM overview_resource_heads
    WHERE resource_kind=NEW.resource_kind AND resource_id=NEW.resource_id
)
BEGIN
    SELECT RAISE(ABORT, 'overview resource head already exists');
END;

CREATE TRIGGER overview_resource_heads_validate_insert
BEFORE INSERT ON overview_resource_heads
WHEN NOT EXISTS (
    SELECT 1 FROM overview_resource_snapshots AS snapshot
    WHERE snapshot.resource_kind=NEW.resource_kind
      AND snapshot.resource_id=NEW.resource_id
      AND snapshot.observation_sequence=NEW.observation_sequence
      AND snapshot.snapshot_digest=NEW.snapshot_digest
      AND snapshot.observation_sequence=1
      AND snapshot.previous_snapshot_digest IS NULL
      AND NEW.tenant_id=snapshot.tenant_id
      AND COALESCE(NEW.workspace_id,'')=COALESCE(snapshot.workspace_id,'')
      AND COALESCE(NEW.subject_run_id,'')=COALESCE(snapshot.subject_run_id,'')
      AND NEW.state=snapshot.state
      AND COALESCE(NEW.binding_target_kind,'')=
          COALESCE(snapshot.binding_target_kind,'')
      AND NEW.resource_revision=snapshot.resource_revision
      AND NEW.created_at=snapshot.created_at
      AND NEW.updated_at=snapshot.updated_at
      AND COALESCE(NEW.usage_revision,-1)=COALESCE(snapshot.usage_revision,-1)
      AND COALESCE(NEW.usage_ledger_sequence,-1)=
          COALESCE(snapshot.usage_ledger_sequence,-1)
      AND COALESCE(NEW.usage_status,'')=COALESCE(snapshot.usage_status,'')
      AND COALESCE(NEW.input_tokens,-1)=COALESCE(snapshot.input_tokens,-1)
      AND COALESCE(NEW.cached_input_tokens,-1)=
          COALESCE(snapshot.cached_input_tokens,-1)
      AND COALESCE(NEW.uncached_input_tokens,-1)=
          COALESCE(snapshot.uncached_input_tokens,-1)
      AND COALESCE(NEW.output_tokens,-1)=COALESCE(snapshot.output_tokens,-1)
      AND COALESCE(NEW.reasoning_tokens,-1)=
          COALESCE(snapshot.reasoning_tokens,-1)
)
BEGIN
    SELECT RAISE(ABORT, 'invalid initial overview resource head');
END;

CREATE TRIGGER overview_resource_heads_validate_update
BEFORE UPDATE ON overview_resource_heads
WHEN NEW.resource_kind<>OLD.resource_kind OR NEW.resource_id<>OLD.resource_id
  OR NEW.observation_sequence<>OLD.observation_sequence+1
  OR NOT EXISTS (
    SELECT 1
    FROM overview_resource_snapshots AS snapshot
    JOIN overview_resource_snapshots AS previous
      ON previous.snapshot_digest=OLD.snapshot_digest
    WHERE snapshot.resource_kind=NEW.resource_kind
      AND snapshot.resource_id=NEW.resource_id
      AND snapshot.observation_sequence=NEW.observation_sequence
      AND snapshot.snapshot_digest=NEW.snapshot_digest
      AND NEW.tenant_id=snapshot.tenant_id
      AND COALESCE(NEW.workspace_id,'')=COALESCE(snapshot.workspace_id,'')
      AND COALESCE(NEW.subject_run_id,'')=COALESCE(snapshot.subject_run_id,'')
      AND NEW.state=snapshot.state
      AND COALESCE(NEW.binding_target_kind,'')=
          COALESCE(snapshot.binding_target_kind,'')
      AND NEW.resource_revision=snapshot.resource_revision
      AND NEW.created_at=snapshot.created_at
      AND NEW.updated_at=snapshot.updated_at
      AND COALESCE(NEW.usage_revision,-1)=COALESCE(snapshot.usage_revision,-1)
      AND COALESCE(NEW.usage_ledger_sequence,-1)=
          COALESCE(snapshot.usage_ledger_sequence,-1)
      AND COALESCE(NEW.usage_status,'')=COALESCE(snapshot.usage_status,'')
      AND COALESCE(NEW.input_tokens,-1)=COALESCE(snapshot.input_tokens,-1)
      AND COALESCE(NEW.cached_input_tokens,-1)=
          COALESCE(snapshot.cached_input_tokens,-1)
      AND COALESCE(NEW.uncached_input_tokens,-1)=
          COALESCE(snapshot.uncached_input_tokens,-1)
      AND COALESCE(NEW.output_tokens,-1)=COALESCE(snapshot.output_tokens,-1)
      AND COALESCE(NEW.reasoning_tokens,-1)=
          COALESCE(snapshot.reasoning_tokens,-1)
      AND snapshot.previous_snapshot_digest=OLD.snapshot_digest
      AND snapshot.store_instance_id=previous.store_instance_id
      AND snapshot.tenant_id=previous.tenant_id
      AND COALESCE(snapshot.workspace_id,'')=COALESCE(previous.workspace_id,'')
      AND snapshot.created_at=previous.created_at
      AND (
        snapshot.subject_run_id=previous.subject_run_id
        OR (snapshot.resource_kind='LEARNING_TASK'
          AND previous.subject_run_id IS NULL
          AND snapshot.subject_run_id IS NOT NULL)
      )
      AND snapshot.subject_run_observation_sequence>=
          COALESCE(previous.subject_run_observation_sequence,0)
      AND snapshot.resource_revision>=previous.resource_revision
      AND snapshot.resource_revision<=previous.resource_revision+1
      AND snapshot.updated_at>=previous.updated_at
  )
BEGIN
    SELECT RAISE(ABORT, 'invalid overview resource head advance');
END;

CREATE TRIGGER overview_resource_heads_reject_delete
BEFORE DELETE ON overview_resource_heads
BEGIN
    SELECT RAISE(ABORT, 'overview resource head is required');
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

CREATE TRIGGER overview_resource_transition_carriers_reject_reinsert
BEFORE INSERT ON overview_resource_transition_carriers
WHEN EXISTS (
    SELECT 1 FROM overview_resource_transition_carriers
    WHERE resource_kind=NEW.resource_kind
      AND resource_id=NEW.resource_id
      AND observation_sequence=NEW.observation_sequence
)
BEGIN
    SELECT RAISE(ABORT, 'overview resource transition carrier already exists');
END;

CREATE TRIGGER overview_resource_transition_carriers_reject_update
BEFORE UPDATE ON overview_resource_transition_carriers
BEGIN
    SELECT RAISE(ABORT, 'overview resource transition carriers are immutable');
END;

CREATE TRIGGER overview_resource_transition_carriers_reject_delete
BEFORE DELETE ON overview_resource_transition_carriers
BEGIN
    SELECT RAISE(ABORT, 'overview resource transition carriers are append-only');
END;

CREATE INDEX overview_resource_heads_tenant_state_recent_idx
    ON overview_resource_heads(
        resource_kind,tenant_id,state,updated_at DESC,
        resource_id COLLATE BINARY DESC
    );

CREATE INDEX overview_resource_heads_workspace_state_recent_idx
    ON overview_resource_heads(
        resource_kind,tenant_id,workspace_id,state,updated_at DESC,
        resource_id COLLATE BINARY DESC
    );

CREATE INDEX overview_resource_heads_tenant_recent_idx
    ON overview_resource_heads(
        resource_kind,tenant_id,updated_at DESC,
        resource_id COLLATE BINARY DESC
    );

CREATE INDEX overview_resource_heads_workspace_recent_idx
    ON overview_resource_heads(
        resource_kind,tenant_id,workspace_id,updated_at DESC,
        resource_id COLLATE BINARY DESC
    );

CREATE INDEX overview_resource_snapshots_causal_run_idx
    ON overview_resource_snapshots(
        resource_kind,causal_run_id,causal_run_observation_sequence,
        causal_run_observation_digest,resource_id COLLATE BINARY
    );

CREATE INDEX overview_resource_heads_module_tenant_recent_idx
    ON overview_resource_heads(
        tenant_id,created_at DESC,resource_id COLLATE BINARY DESC
    ) WHERE resource_kind='MODULE_REVIEW';

CREATE INDEX overview_resource_heads_module_workspace_recent_idx
    ON overview_resource_heads(
        tenant_id,binding_target_kind,workspace_id,
        created_at DESC,resource_id COLLATE BINARY DESC
    ) WHERE resource_kind='MODULE_REVIEW';

CREATE INDEX model_dispatch_attempts_run_unsettled_idx
    ON model_dispatch_attempts(run_id,attempt_id)
    WHERE state IN ('PENDING','MODEL_UNKNOWN');

CREATE INDEX dispatch_attempts_run_unsettled_idx
    ON dispatch_attempts(run_id,attempt_id)
    WHERE state IN ('PENDING','UNKNOWN');

CREATE INDEX dispatch_attempts_run_kind_idx
    ON dispatch_attempts(run_id,dispatch_kind,attempt_id);

-- The polling Overview consumes only this bounded publication projection.
-- The full Control/Catalog closure is checked once by the publication writer
-- and again by the OpenExisting/Backup semantic gate.
CREATE TABLE overview_basis_snapshots (
    projection_digest TEXT PRIMARY KEY CHECK (
        length(projection_digest)=64 AND projection_digest=lower(projection_digest)
        AND projection_digest NOT GLOB '*[^0-9a-f]*'
    ),
    store_instance_id TEXT NOT NULL CHECK (
        length(store_instance_id) BETWEEN 1 AND 256
        AND store_instance_id=trim(store_instance_id)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id=trim(tenant_id)
    ),
    pointer_revision INTEGER NOT NULL CHECK (pointer_revision>0),
    snapshot_id TEXT NOT NULL,
    control_revision INTEGER NOT NULL CHECK (control_revision>0),
    control_digest TEXT NOT NULL CHECK (
        length(control_digest)=64 AND control_digest=lower(control_digest)
        AND control_digest NOT GLOB '*[^0-9a-f]*'
    ),
    control_published_at INTEGER NOT NULL CHECK (control_published_at>0),
    catalog_generation_id TEXT NOT NULL,
    catalog_generation INTEGER NOT NULL CHECK (catalog_generation>0),
    catalog_digest TEXT NOT NULL CHECK (
        length(catalog_digest)=64 AND catalog_digest=lower(catalog_digest)
        AND catalog_digest NOT GLOB '*[^0-9a-f]*'
    ),
    catalog_published_at INTEGER NOT NULL CHECK (catalog_published_at>0),
    source_updated_at INTEGER NOT NULL CHECK (
        source_updated_at=MAX(control_published_at,catalog_published_at)
    ),
    workspace_count INTEGER NOT NULL CHECK (workspace_count BETWEEN 0 AND 256),
    canonical_json BLOB NOT NULL CHECK (length(canonical_json) BETWEEN 2 AND 131072),
    UNIQUE(tenant_id,pointer_revision),
    UNIQUE(
        tenant_id,snapshot_id,catalog_generation_id,pointer_revision,
        projection_digest
    ),
    FOREIGN KEY (
        snapshot_id,tenant_id,control_revision,control_digest,control_published_at
    ) REFERENCES control_snapshots(
        snapshot_id,tenant_id,revision,digest,published_at
    ),
    FOREIGN KEY (
        catalog_generation_id,tenant_id,catalog_generation,catalog_digest,
        catalog_published_at,snapshot_id
    ) REFERENCES runtime_catalog_generations(
        generation_id,tenant_id,generation,digest,published_at,control_snapshot_id
    )
) STRICT;

CREATE TABLE overview_basis_workspaces (
    projection_digest TEXT NOT NULL REFERENCES overview_basis_snapshots(projection_digest),
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 255),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id=trim(workspace_id)
    ),
    workspace_version TEXT NOT NULL CHECK (
        length(workspace_version) BETWEEN 1 AND 64 AND workspace_version=trim(workspace_version)
    ),
    workspace_digest TEXT NOT NULL CHECK (
        length(workspace_digest)=64 AND workspace_digest=lower(workspace_digest)
        AND workspace_digest NOT GLOB '*[^0-9a-f]*'
    ),
    PRIMARY KEY(projection_digest,ordinal),
    UNIQUE(projection_digest,workspace_id)
) STRICT;

CREATE TABLE overview_basis_heads (
    tenant_id TEXT PRIMARY KEY,
    snapshot_id TEXT NOT NULL,
    catalog_generation_id TEXT NOT NULL,
    pointer_revision INTEGER NOT NULL CHECK (pointer_revision>0),
    projection_digest TEXT NOT NULL UNIQUE REFERENCES overview_basis_snapshots(projection_digest),
    source_updated_at INTEGER NOT NULL CHECK (source_updated_at>0),
    workspace_count INTEGER NOT NULL CHECK (workspace_count BETWEEN 0 AND 256),
    UNIQUE(tenant_id,snapshot_id,catalog_generation_id,pointer_revision)
) STRICT;

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

CREATE TRIGGER overview_basis_snapshots_reject_update
BEFORE UPDATE ON overview_basis_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Overview basis snapshots are immutable');
END;

CREATE TRIGGER overview_basis_snapshots_reject_delete
BEFORE DELETE ON overview_basis_snapshots
BEGIN
    SELECT RAISE(ABORT, 'Overview basis snapshots are append-only');
END;

CREATE TRIGGER overview_basis_workspaces_reject_update
BEFORE UPDATE ON overview_basis_workspaces
BEGIN
    SELECT RAISE(ABORT, 'Overview basis Workspaces are immutable');
END;

CREATE TRIGGER overview_basis_workspaces_validate_insert
BEFORE INSERT ON overview_basis_workspaces
WHEN EXISTS (
    SELECT 1 FROM overview_basis_snapshots AS snapshot
    JOIN overview_basis_heads AS head ON head.tenant_id=snapshot.tenant_id
    WHERE snapshot.projection_digest=NEW.projection_digest
      AND snapshot.pointer_revision<=head.pointer_revision
)
BEGIN
    SELECT RAISE(ABORT, 'Overview basis Workspaces are sealed');
END;

CREATE TRIGGER overview_basis_workspaces_reject_delete
BEFORE DELETE ON overview_basis_workspaces
BEGIN
    SELECT RAISE(ABORT, 'Overview basis Workspaces are append-only');
END;

CREATE TRIGGER overview_basis_heads_reject_reinsert
BEFORE INSERT ON overview_basis_heads
WHEN EXISTS (SELECT 1 FROM overview_basis_heads WHERE tenant_id=NEW.tenant_id)
BEGIN
    SELECT RAISE(ABORT, 'Overview basis head already exists');
END;

CREATE TRIGGER overview_basis_heads_validate_insert
BEFORE INSERT ON overview_basis_heads
WHEN NOT EXISTS (
    SELECT 1 FROM overview_basis_snapshots AS snapshot
    JOIN control_current AS current ON current.tenant_id=snapshot.tenant_id
    WHERE snapshot.projection_digest=NEW.projection_digest
      AND snapshot.tenant_id=NEW.tenant_id
      AND snapshot.pointer_revision=1
      AND NEW.pointer_revision=snapshot.pointer_revision
      AND NEW.snapshot_id=snapshot.snapshot_id
      AND NEW.catalog_generation_id=snapshot.catalog_generation_id
      AND NEW.source_updated_at=snapshot.source_updated_at
      AND NEW.workspace_count=snapshot.workspace_count
      AND current.snapshot_id=NEW.snapshot_id
      AND current.catalog_generation_id=NEW.catalog_generation_id
      AND current.pointer_revision=NEW.pointer_revision
      AND NEW.workspace_count=(SELECT COUNT(*) FROM overview_basis_workspaces
        WHERE projection_digest=NEW.projection_digest)
      AND (NEW.workspace_count=0 OR (
        (SELECT MIN(ordinal) FROM overview_basis_workspaces
          WHERE projection_digest=NEW.projection_digest)=0
        AND (SELECT MAX(ordinal) FROM overview_basis_workspaces
          WHERE projection_digest=NEW.projection_digest)=NEW.workspace_count-1
      ))
)
BEGIN
    SELECT RAISE(ABORT, 'invalid initial Overview basis head');
END;

CREATE TRIGGER overview_basis_heads_validate_update
BEFORE UPDATE ON overview_basis_heads
WHEN NEW.tenant_id<>OLD.tenant_id OR NEW.pointer_revision<>OLD.pointer_revision+1
  OR NOT EXISTS (
    SELECT 1 FROM overview_basis_snapshots AS snapshot
    JOIN control_current AS current ON current.tenant_id=snapshot.tenant_id
    WHERE snapshot.projection_digest=NEW.projection_digest
      AND snapshot.tenant_id=NEW.tenant_id
      AND snapshot.pointer_revision=NEW.pointer_revision
      AND NEW.snapshot_id=snapshot.snapshot_id
      AND NEW.catalog_generation_id=snapshot.catalog_generation_id
      AND NEW.source_updated_at=snapshot.source_updated_at
      AND NEW.workspace_count=snapshot.workspace_count
      AND current.snapshot_id=NEW.snapshot_id
      AND current.catalog_generation_id=NEW.catalog_generation_id
      AND current.pointer_revision=NEW.pointer_revision
      AND NEW.workspace_count=(SELECT COUNT(*) FROM overview_basis_workspaces
        WHERE projection_digest=NEW.projection_digest)
      AND (NEW.workspace_count=0 OR (
        (SELECT MIN(ordinal) FROM overview_basis_workspaces
          WHERE projection_digest=NEW.projection_digest)=0
        AND (SELECT MAX(ordinal) FROM overview_basis_workspaces
          WHERE projection_digest=NEW.projection_digest)=NEW.workspace_count-1
      ))
  )
BEGIN
    SELECT RAISE(ABORT, 'invalid Overview basis head advance');
END;

CREATE TRIGGER overview_basis_heads_reject_delete
BEFORE DELETE ON overview_basis_heads
BEGIN
    SELECT RAISE(ABORT, 'Overview basis head is required');
END;

CREATE TABLE history_entries (
    run_id TEXT NOT NULL REFERENCES runs(run_id),
    history_sequence INTEGER NOT NULL CHECK (history_sequence > 0),
    member_id TEXT NOT NULL CHECK (
        length(member_id) BETWEEN 1 AND 256 AND member_id = trim(member_id)
    ),
    role TEXT NOT NULL CHECK (length(trim(role)) > 0),
    content_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    content_digest TEXT NOT NULL CHECK (content_digest = content_ref),
    source_attempt_id TEXT REFERENCES model_dispatch_attempts(attempt_id),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    PRIMARY KEY (run_id, history_sequence),
    UNIQUE (source_attempt_id)
) STRICT;

CREATE TABLE learning_proposals (
    proposal_id TEXT PRIMARY KEY CHECK (
        length(proposal_id) = 64
        AND proposal_id = lower(proposal_id)
        AND proposal_id NOT GLOB '*[^0-9a-f]*'
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id)
    ),
    proposal_kind TEXT NOT NULL CHECK (proposal_kind IN ('KNOWLEDGE', 'SKILL')),
    source_fingerprint TEXT NOT NULL CHECK (
        length(source_fingerprint) = 64
        AND source_fingerprint = lower(source_fingerprint)
        AND source_fingerprint NOT GLOB '*[^0-9a-f]*'
    ),
    content_fingerprint TEXT NOT NULL CHECK (
        length(content_fingerprint) = 64
        AND content_fingerprint = lower(content_fingerprint)
        AND content_fingerprint NOT GLOB '*[^0-9a-f]*'
    ),
    draft_digest TEXT NOT NULL CHECK (
        length(draft_digest) = 64
        AND draft_digest = lower(draft_digest)
        AND draft_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_id TEXT NOT NULL CHECK (
        length(target_id) BETWEEN 1 AND 128 AND target_id = trim(target_id)
    ),
    target_version TEXT NOT NULL CHECK (
        length(target_version) BETWEEN 1 AND 64
        AND target_version = trim(target_version)
    ),
    proposal_canonical BLOB NOT NULL CHECK (
        length(proposal_canonical) BETWEEN 1 AND 16384
    ),
    proposal_size_bytes INTEGER NOT NULL CHECK (
        proposal_size_bytes = length(proposal_canonical)
    ),
    draft_canonical BLOB NOT NULL CHECK (
        length(draft_canonical) BETWEEN 1 AND 1048576
    ),
    draft_size_bytes INTEGER NOT NULL CHECK (
        draft_size_bytes = length(draft_canonical)
    ),
    proposer_run_id TEXT NOT NULL REFERENCES runs(run_id),
    proposer_manifest_digest TEXT NOT NULL,
    proposer_member_id TEXT NOT NULL,
    proposer_member_digest TEXT NOT NULL CHECK (
        length(proposer_member_digest) = 64
        AND proposer_member_digest = lower(proposer_member_digest)
        AND proposer_member_digest NOT GLOB '*[^0-9a-f]*'
    ),
    proposer_attempt_id TEXT NOT NULL
        REFERENCES model_dispatch_attempts(attempt_id),
    proposer_result_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    review_run_id TEXT UNIQUE REFERENCES run_manifests(run_id),
    reviewer_attempt_id TEXT UNIQUE
        REFERENCES model_dispatch_attempts(attempt_id),
    state TEXT NOT NULL CHECK (state IN (
        'SUBMITTED',
        'REVIEW_PENDING',
        'APPROVED',
        'REJECTED',
        'REVIEW_FAILED',
        'REVIEW_UNKNOWN'
    )),
    revision INTEGER NOT NULL CHECK (revision BETWEEN 0 AND 3),
    version_canonical BLOB CHECK (
        version_canonical IS NULL OR length(version_canonical) <= 16384
    ),
    version_size_bytes INTEGER CHECK (
        version_size_bytes IS NULL OR (
            version_size_bytes BETWEEN 1 AND 16384
            AND version_size_bytes = length(version_canonical)
        )
    ),
    version_canonical_digest TEXT CHECK (
        version_canonical_digest IS NULL OR (
            length(version_canonical_digest)=64
            AND version_canonical_digest=lower(version_canonical_digest)
            AND version_canonical_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    version_id TEXT UNIQUE CHECK (
        version_id IS NULL OR (
            length(version_id) = 64
            AND version_id = lower(version_id)
            AND version_id NOT GLOB '*[^0-9a-f]*'
        )
    ),
    artifact_digest TEXT CHECK (
        artifact_digest IS NULL OR (
            length(artifact_digest) = 64
            AND artifact_digest = lower(artifact_digest)
            AND artifact_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    artifact_size_bytes INTEGER CHECK (
        artifact_size_bytes IS NULL OR artifact_size_bytes > 0
    ),
    approval_verdict_digest TEXT CHECK (
        approval_verdict_digest IS NULL OR (
            length(approval_verdict_digest) = 64
            AND approval_verdict_digest = lower(approval_verdict_digest)
            AND approval_verdict_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    materialized_at INTEGER CHECK (
        materialized_at IS NULL OR materialized_at > 0
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (
        updated_at > 0 AND updated_at >= created_at
    ),
    overview_resource_kind TEXT NOT NULL DEFAULT 'LEARNING_PROPOSAL' CHECK (
        overview_resource_kind='LEARNING_PROPOSAL'
    ),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence>0
    ),
    UNIQUE (tenant_id, proposal_kind, source_fingerprint),
    UNIQUE (tenant_id, proposal_kind, content_fingerprint),
    UNIQUE (tenant_id, proposal_kind, target_id, target_version),
    FOREIGN KEY (proposer_run_id, tenant_id, workspace_id)
        REFERENCES runs(run_id, tenant_id, workspace_id),
    FOREIGN KEY (proposer_run_id, proposer_manifest_digest)
        REFERENCES run_manifests(run_id, digest),
    FOREIGN KEY (proposer_run_id, proposer_member_id)
        REFERENCES member_execution_snapshots(run_id, member_id),
    FOREIGN KEY (
        overview_resource_kind,proposal_id,state,revision,updated_at
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,state,resource_revision,updated_at
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (overview_resource_kind,proposal_id)
        REFERENCES overview_resource_heads(resource_kind,resource_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        overview_resource_kind,proposal_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        (state = 'SUBMITTED'
            AND revision = 0
            AND review_run_id IS NULL
            AND reviewer_attempt_id IS NULL
            AND updated_at = created_at)
        OR (state = 'REVIEW_PENDING'
            AND revision = 1
            AND review_run_id IS NOT NULL
            AND reviewer_attempt_id IS NULL)
        OR (state IN (
                'APPROVED',
                'REJECTED',
                'REVIEW_FAILED',
                'REVIEW_UNKNOWN'
            )
            AND revision = 2
            AND review_run_id IS NOT NULL
            AND reviewer_attempt_id IS NOT NULL)
        OR (state IN ('APPROVED', 'REJECTED', 'REVIEW_FAILED')
            AND revision = 3
            AND review_run_id IS NOT NULL
            AND reviewer_attempt_id IS NOT NULL)
    ),
    CHECK (
        (version_canonical IS NULL
            AND version_size_bytes IS NULL
            AND version_canonical_digest IS NULL
            AND version_id IS NULL
            AND artifact_digest IS NULL
            AND artifact_size_bytes IS NULL
            AND approval_verdict_digest IS NULL
            AND materialized_at IS NULL)
        OR (version_canonical IS NOT NULL
            AND version_size_bytes IS NOT NULL
            AND version_canonical_digest IS NOT NULL
            AND version_id IS NOT NULL
            AND artifact_digest IS NOT NULL
            AND artifact_size_bytes IS NOT NULL
            AND approval_verdict_digest IS NOT NULL
            AND materialized_at IS NOT NULL
            AND materialized_at >= updated_at
            AND state = 'APPROVED'
            AND revision IN (2, 3))
    )
) STRICT;

CREATE UNIQUE INDEX learning_proposals_materialized_target_unique
    ON learning_proposals(target_id, target_version)
    WHERE version_id IS NOT NULL;

CREATE TRIGGER learning_proposals_observation_update_guard
BEFORE UPDATE ON learning_proposals
WHEN NEW.proposal_id<>OLD.proposal_id OR NEW.tenant_id<>OLD.tenant_id
  OR NEW.workspace_id<>OLD.workspace_id OR NEW.proposal_kind<>OLD.proposal_kind
  OR NEW.source_fingerprint<>OLD.source_fingerprint
  OR NEW.content_fingerprint<>OLD.content_fingerprint
  OR NEW.draft_digest<>OLD.draft_digest OR NEW.target_id<>OLD.target_id
  OR NEW.target_version<>OLD.target_version
  OR NEW.proposal_canonical<>OLD.proposal_canonical
  OR NEW.proposal_size_bytes<>OLD.proposal_size_bytes
  OR NEW.draft_canonical<>OLD.draft_canonical
  OR NEW.draft_size_bytes<>OLD.draft_size_bytes
  OR NEW.proposer_run_id<>OLD.proposer_run_id
  OR NEW.proposer_manifest_digest<>OLD.proposer_manifest_digest
  OR NEW.proposer_member_id<>OLD.proposer_member_id
  OR NEW.proposer_member_digest<>OLD.proposer_member_digest
  OR NEW.proposer_attempt_id<>OLD.proposer_attempt_id
  OR NEW.proposer_result_ref<>OLD.proposer_result_ref
  OR NEW.created_at<>OLD.created_at
  OR NEW.overview_resource_kind<>OLD.overview_resource_kind
  OR NEW.revision NOT IN (OLD.revision,OLD.revision+1)
  OR NEW.updated_at<OLD.updated_at
  OR NEW.overview_observation_sequence<>OLD.overview_observation_sequence+1
BEGIN
    SELECT RAISE(ABORT, 'invalid learning proposal observation update');
END;

CREATE TABLE learning_cycle_schedules (
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id)
    ),
    schedule_id TEXT NOT NULL CHECK (
        length(schedule_id) BETWEEN 1 AND 256 AND schedule_id = trim(schedule_id)
    ),
    schedule_digest TEXT NOT NULL CHECK (
        length(schedule_digest) = 64
        AND schedule_digest = lower(schedule_digest)
        AND schedule_digest NOT GLOB '*[^0-9a-f]*'
    ),
    schedule_canonical BLOB NOT NULL CHECK (
        length(schedule_canonical) BETWEEN 1 AND 32768
    ),
    schedule_size_bytes INTEGER NOT NULL CHECK (
        schedule_size_bytes = length(schedule_canonical)
    ),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    last_scheduled_for INTEGER CHECK (
        last_scheduled_for IS NULL OR (
            last_scheduled_for > 0
            AND last_scheduled_for <= 9007199254740991
        )
    ),
    next_due_at INTEGER NOT NULL CHECK (
        next_due_at > 0 AND next_due_at <= 9007199254740991
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (
        updated_at > 0 AND updated_at >= created_at
    ),
    PRIMARY KEY (tenant_id, schedule_id),
    UNIQUE (schedule_digest),
    UNIQUE (tenant_id, schedule_id, schedule_digest),
    UNIQUE (tenant_id, workspace_id, schedule_id, schedule_digest),
    CHECK (
        (revision = 0 AND enabled = 0 AND last_scheduled_for IS NULL
            AND updated_at = created_at)
        OR revision > 0
    ),
    CHECK (
        last_scheduled_for IS NULL OR next_due_at > last_scheduled_for
    )
) STRICT;

CREATE INDEX learning_cycle_schedules_due_idx
    ON learning_cycle_schedules(
        tenant_id, enabled, next_due_at, schedule_id
    );

CREATE TRIGGER learning_cycle_schedules_frozen_projection_guard
BEFORE UPDATE ON learning_cycle_schedules
WHEN NEW.tenant_id<>OLD.tenant_id OR NEW.workspace_id<>OLD.workspace_id
  OR NEW.schedule_id<>OLD.schedule_id
  OR NEW.schedule_digest<>OLD.schedule_digest
  OR NEW.schedule_canonical<>OLD.schedule_canonical
  OR NEW.schedule_size_bytes<>OLD.schedule_size_bytes
  OR NEW.created_at<>OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'learning schedule identity is immutable');
END;

CREATE TABLE learning_cycle_tasks (
    task_id TEXT PRIMARY KEY CHECK (
        length(task_id) = 64
        AND task_id = lower(task_id)
        AND task_id NOT GLOB '*[^0-9a-f]*'
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    workspace_id TEXT NOT NULL CHECK (
        length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id)
    ),
    schedule_id TEXT NOT NULL CHECK (
        length(schedule_id) BETWEEN 1 AND 256 AND schedule_id = trim(schedule_id)
    ),
    schedule_digest TEXT NOT NULL CHECK (
        length(schedule_digest) = 64
        AND schedule_digest = lower(schedule_digest)
        AND schedule_digest NOT GLOB '*[^0-9a-f]*'
    ),
    scheduled_for INTEGER NOT NULL CHECK (
        scheduled_for > 0 AND scheduled_for <= 9007199254740991
    ),
    request_digest TEXT NOT NULL CHECK (
        length(request_digest) = 64
        AND request_digest = lower(request_digest)
        AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_canonical BLOB NOT NULL CHECK (
        length(request_canonical) BETWEEN 1 AND 32768
    ),
    request_size_bytes INTEGER NOT NULL CHECK (
        request_size_bytes = length(request_canonical)
    ),
    state TEXT NOT NULL CHECK (state IN (
        'PENDING',
        'RUN_ADMITTED',
        'PROPOSAL_SUBMITTED',
        'NO_CHANGE',
        'SOURCE_OCCUPIED',
        'CONTENT_OCCUPIED',
        'TARGET_OCCUPIED',
        'FAILED',
        'INVALID_RESULT',
        'UNKNOWN'
    )),
    revision INTEGER NOT NULL CHECK (revision BETWEEN 0 AND 3),
    run_id TEXT UNIQUE REFERENCES run_manifests(run_id),
    run_manifest_digest TEXT CHECK (
        run_manifest_digest IS NULL OR (
            length(run_manifest_digest) = 64
            AND run_manifest_digest = lower(run_manifest_digest)
            AND run_manifest_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    attempt_id TEXT UNIQUE REFERENCES model_dispatch_attempts(attempt_id),
    result_ref TEXT REFERENCES content_records(content_digest),
    proposal_id TEXT REFERENCES learning_proposals(proposal_id),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    updated_at INTEGER NOT NULL CHECK (
        updated_at > 0 AND updated_at >= created_at
    ),
    overview_resource_kind TEXT NOT NULL DEFAULT 'LEARNING_TASK' CHECK (
        overview_resource_kind='LEARNING_TASK'
    ),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence>0
    ),
    UNIQUE (tenant_id, schedule_id, scheduled_for),
    FOREIGN KEY (tenant_id, workspace_id, schedule_id, schedule_digest)
        REFERENCES learning_cycle_schedules(
            tenant_id, workspace_id, schedule_id, schedule_digest
        ),
    FOREIGN KEY (run_id, run_manifest_digest)
        REFERENCES run_manifests(run_id, digest),
    FOREIGN KEY (
        overview_resource_kind,task_id,state,revision,updated_at
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,state,resource_revision,updated_at
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (overview_resource_kind,task_id)
        REFERENCES overview_resource_heads(resource_kind,resource_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        overview_resource_kind,task_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (
        (state = 'PENDING'
            AND revision = 0
            AND run_id IS NULL
            AND run_manifest_digest IS NULL
            AND attempt_id IS NULL
            AND result_ref IS NULL
            AND proposal_id IS NULL
            AND updated_at = created_at)
        OR (state = 'RUN_ADMITTED'
            AND revision = 1
            AND run_id IS NOT NULL
            AND run_manifest_digest IS NOT NULL
            AND attempt_id IS NULL
            AND result_ref IS NULL
            AND proposal_id IS NULL)
        OR (state IN ('FAILED', 'UNKNOWN')
            AND revision IN (2, 3)
            AND run_id IS NOT NULL
            AND run_manifest_digest IS NOT NULL
            AND attempt_id IS NOT NULL
            AND result_ref IS NULL
            AND proposal_id IS NULL)
        OR (state IN ('NO_CHANGE', 'INVALID_RESULT')
            AND revision IN (2, 3)
            AND run_id IS NOT NULL
            AND run_manifest_digest IS NOT NULL
            AND attempt_id IS NOT NULL
            AND result_ref IS NOT NULL
            AND proposal_id IS NULL)
        OR (state IN (
                'SOURCE_OCCUPIED',
                'CONTENT_OCCUPIED',
                'TARGET_OCCUPIED'
            )
            AND revision IN (2, 3)
            AND run_id IS NOT NULL
            AND run_manifest_digest IS NOT NULL
            AND attempt_id IS NOT NULL
            AND result_ref IS NOT NULL
            AND proposal_id IS NOT NULL)
        OR (state = 'PROPOSAL_SUBMITTED'
            AND revision IN (2, 3)
            AND run_id IS NOT NULL
            AND run_manifest_digest IS NOT NULL
            AND attempt_id IS NOT NULL
            AND result_ref IS NOT NULL
            AND proposal_id IS NOT NULL)
    ),
    CHECK (state <> 'UNKNOWN' OR revision = 2),
    CHECK (revision <> 3 OR state NOT IN ('PENDING', 'RUN_ADMITTED', 'UNKNOWN'))
) STRICT;

CREATE UNIQUE INDEX learning_cycle_tasks_one_open_per_schedule
    ON learning_cycle_tasks(tenant_id, schedule_id)
    WHERE state IN ('PENDING', 'RUN_ADMITTED');

CREATE TRIGGER learning_cycle_tasks_observation_update_guard
BEFORE UPDATE ON learning_cycle_tasks
WHEN NEW.task_id<>OLD.task_id OR NEW.tenant_id<>OLD.tenant_id
  OR NEW.workspace_id<>OLD.workspace_id OR NEW.schedule_id<>OLD.schedule_id
  OR NEW.schedule_digest<>OLD.schedule_digest
  OR NEW.scheduled_for<>OLD.scheduled_for
  OR NEW.request_digest<>OLD.request_digest
  OR NEW.request_canonical<>OLD.request_canonical
  OR NEW.request_size_bytes<>OLD.request_size_bytes
  OR NEW.created_at<>OLD.created_at
  OR NEW.overview_resource_kind<>OLD.overview_resource_kind
  OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
  OR NEW.overview_observation_sequence<>OLD.overview_observation_sequence+1
BEGIN
    SELECT RAISE(ABORT, 'invalid learning task observation update');
END;

CREATE TABLE module_publisher_keys (
    publisher_key_id TEXT PRIMARY KEY CHECK (
        length(publisher_key_id) = 64
        AND publisher_key_id = lower(publisher_key_id)
        AND publisher_key_id NOT GLOB '*[^0-9a-f]*'
    ),
    key_canonical BLOB NOT NULL UNIQUE CHECK (
        length(key_canonical) BETWEEN 1 AND 65536
    ),
    revision INTEGER NOT NULL CHECK (revision > 0),
    imported_at INTEGER NOT NULL CHECK (imported_at > 0),
    revoked_at INTEGER CHECK (
        revoked_at IS NULL OR revoked_at >= imported_at
    )
) STRICT;

CREATE TABLE module_sources (
    source_id TEXT PRIMARY KEY CHECK (
        length(source_id) BETWEEN 1 AND 128
        AND source_id = trim(source_id)
    ),
    source_policy_id TEXT NOT NULL CHECK (
        length(source_policy_id) = 64
        AND source_policy_id = lower(source_policy_id)
        AND source_policy_id NOT GLOB '*[^0-9a-f]*'
    ),
    source_policy_canonical BLOB NOT NULL CHECK (
        length(source_policy_canonical) BETWEEN 1 AND 65536
    ),
    source_kind TEXT NOT NULL CHECK (
        source_kind IN ('LOCAL_DIRECTORY', 'HTTPS_INDEX')
    ),
    origin_digest TEXT NOT NULL CHECK (
        length(origin_digest) = 64
        AND origin_digest = lower(origin_digest)
        AND origin_digest NOT GLOB '*[^0-9a-f]*'
    ),
    publisher_key_id TEXT REFERENCES module_publisher_keys(publisher_key_id),
    policy_revision INTEGER NOT NULL CHECK (policy_revision > 0),
    current_snapshot_id TEXT,
    observation_revision INTEGER NOT NULL CHECK (observation_revision >= 0),
    registered_at INTEGER NOT NULL CHECK (registered_at > 0),
    updated_at INTEGER NOT NULL CHECK (
        updated_at >= registered_at
    ),
    FOREIGN KEY (current_snapshot_id, source_id)
        REFERENCES module_discovery_snapshots(snapshot_id, source_id)
        DEFERRABLE INITIALLY DEFERRED
) STRICT;

CREATE TABLE module_discovery_snapshots (
    snapshot_id TEXT PRIMARY KEY CHECK (
        length(snapshot_id) = 64
        AND snapshot_id = lower(snapshot_id)
        AND snapshot_id NOT GLOB '*[^0-9a-f]*'
    ),
    source_id TEXT NOT NULL REFERENCES module_sources(source_id),
    source_policy_id TEXT NOT NULL CHECK (
        length(source_policy_id) = 64
        AND source_policy_id = lower(source_policy_id)
        AND source_policy_id NOT GLOB '*[^0-9a-f]*'
    ),
    source_policy_canonical BLOB NOT NULL CHECK (
        length(source_policy_canonical) BETWEEN 1 AND 65536
    ),
    index_id TEXT NOT NULL CHECK (
        length(index_id) = 64
        AND index_id = lower(index_id)
        AND index_id NOT GLOB '*[^0-9a-f]*'
    ),
    index_canonical BLOB NOT NULL CHECK (
        length(index_canonical) BETWEEN 1 AND 1048576
    ),
    snapshot_canonical BLOB NOT NULL CHECK (
        length(snapshot_canonical) BETWEEN 1 AND 1114112
    ),
    source_policy_revision INTEGER NOT NULL CHECK (
        source_policy_revision > 0
    ),
    publisher_key_id TEXT REFERENCES module_publisher_keys(publisher_key_id),
    publisher_key_revision INTEGER CHECK (
        publisher_key_revision IS NULL OR publisher_key_revision > 0
    ),
    observation_revision INTEGER NOT NULL CHECK (observation_revision > 0),
    observed_at INTEGER NOT NULL CHECK (observed_at > 0),
    UNIQUE (snapshot_id, source_id),
    UNIQUE (source_id, observation_revision),
    CHECK (
        (publisher_key_id IS NULL AND publisher_key_revision IS NULL)
        OR (publisher_key_id IS NOT NULL AND publisher_key_revision IS NOT NULL)
    )
) STRICT;

CREATE TABLE module_discovery_module_refs (
    module_id TEXT NOT NULL CHECK (
        length(module_id) BETWEEN 1 AND 128
        AND module_id = trim(module_id)
    ),
    exact_version TEXT NOT NULL CHECK (
        length(exact_version) BETWEEN 1 AND 64
        AND exact_version = trim(exact_version)
    ),
    artifact_digest TEXT NOT NULL CHECK (
        length(artifact_digest) = 64
        AND artifact_digest = lower(artifact_digest)
        AND artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    first_source_id TEXT NOT NULL,
    first_snapshot_id TEXT NOT NULL,
    first_observed_at INTEGER NOT NULL CHECK (first_observed_at > 0),
    PRIMARY KEY (module_id, exact_version),
    UNIQUE (module_id, exact_version, artifact_digest),
    FOREIGN KEY (first_snapshot_id, first_source_id)
        REFERENCES module_discovery_snapshots(snapshot_id, source_id)
) STRICT;

CREATE TABLE module_discovery_entries (
    snapshot_id TEXT NOT NULL REFERENCES module_discovery_snapshots(snapshot_id),
    entry_ordinal INTEGER NOT NULL CHECK (entry_ordinal >= 0),
    module_id TEXT NOT NULL CHECK (
        length(module_id) BETWEEN 1 AND 128
        AND module_id = trim(module_id)
    ),
    exact_version TEXT NOT NULL CHECK (
        length(exact_version) BETWEEN 1 AND 64
        AND exact_version = trim(exact_version)
    ),
    artifact_digest TEXT NOT NULL CHECK (
        length(artifact_digest) = 64
        AND artifact_digest = lower(artifact_digest)
        AND artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    artifact_size_bytes INTEGER NOT NULL CHECK (artifact_size_bytes > 0),
    package_path TEXT NOT NULL CHECK (
        length(package_path) BETWEEN 1 AND 4096
    ),
    signature_id TEXT CHECK (
        signature_id IS NULL OR (
            length(signature_id) = 64
            AND signature_id = lower(signature_id)
            AND signature_id NOT GLOB '*[^0-9a-f]*'
        )
    ),
    PRIMARY KEY (snapshot_id, entry_ordinal),
    UNIQUE (snapshot_id, module_id, exact_version),
    FOREIGN KEY (module_id, exact_version, artifact_digest)
        REFERENCES module_discovery_module_refs(
            module_id, exact_version, artifact_digest
        )
) STRICT;

-- W6-5 records the inert, server-owned artifact object separately from the
-- exact supply observation that admitted it.  Neither table grants install,
-- activation, binding, execution, authority, or effect.
CREATE TABLE module_artifacts (
    artifact_digest TEXT PRIMARY KEY CHECK (
        length(artifact_digest) = 64
        AND artifact_digest = lower(artifact_digest)
        AND artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    module_id TEXT NOT NULL CHECK (
        length(module_id) BETWEEN 1 AND 128
        AND module_id = trim(module_id)
    ),
    exact_version TEXT NOT NULL CHECK (
        length(exact_version) BETWEEN 1 AND 64
        AND exact_version = trim(exact_version)
    ),
    manifest_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    artifact_size_bytes INTEGER NOT NULL CHECK (
        artifact_size_bytes BETWEEN 1 AND 268435456
    ),
    covered_file_count INTEGER NOT NULL CHECK (
        covered_file_count BETWEEN 1 AND 4096
    ),
    ingressed_at INTEGER NOT NULL CHECK (ingressed_at > 0),
    UNIQUE (module_id, exact_version),
    UNIQUE (
        artifact_digest,module_id,exact_version,manifest_ref,
        artifact_size_bytes,covered_file_count
    ),
    FOREIGN KEY (module_id, exact_version, artifact_digest)
        REFERENCES module_discovery_module_refs(
            module_id, exact_version, artifact_digest
        )
) STRICT;

CREATE TRIGGER module_artifacts_reject_update
BEFORE UPDATE ON module_artifacts
BEGIN
    SELECT RAISE(ABORT, 'module_artifacts is append-only');
END;

CREATE TRIGGER module_artifacts_reject_delete
BEFORE DELETE ON module_artifacts
BEGIN
    SELECT RAISE(ABORT, 'module_artifacts is append-only');
END;

CREATE TRIGGER module_artifacts_store_quota
BEFORE INSERT ON module_artifacts
WHEN NOT EXISTS (
    SELECT 1 FROM module_artifacts
    WHERE artifact_digest = NEW.artifact_digest
 )
 AND (
    (SELECT COUNT(*) FROM module_artifacts) >= 256
    OR COALESCE((
        SELECT SUM(artifact_size_bytes) FROM module_artifacts
    ), 0) > 536870912 - NEW.artifact_size_bytes
 )
BEGIN
    SELECT RAISE(ABORT, 'module_artifacts store quota exceeded');
END;

CREATE TABLE module_artifact_admissions (
    admission_id TEXT PRIMARY KEY CHECK (
        length(admission_id) = 64
        AND admission_id = lower(admission_id)
        AND admission_id NOT GLOB '*[^0-9a-f]*'
    ),
    admission_canonical BLOB NOT NULL UNIQUE CHECK (
        length(admission_canonical) BETWEEN 1 AND 65536
    ),
    admission_size_bytes INTEGER NOT NULL CHECK (
        admission_size_bytes = length(admission_canonical)
    ),
    source_id TEXT NOT NULL REFERENCES module_sources(source_id),
    source_policy_id TEXT NOT NULL CHECK (
        length(source_policy_id) = 64
        AND source_policy_id = lower(source_policy_id)
        AND source_policy_id NOT GLOB '*[^0-9a-f]*'
    ),
    source_policy_revision INTEGER NOT NULL CHECK (
        source_policy_revision BETWEEN 1 AND 9007199254740991
    ),
    snapshot_id TEXT NOT NULL CHECK (
        length(snapshot_id) = 64
        AND snapshot_id = lower(snapshot_id)
        AND snapshot_id NOT GLOB '*[^0-9a-f]*'
    ),
    observation_revision INTEGER NOT NULL CHECK (
        observation_revision BETWEEN 1 AND 9007199254740991
    ),
    entry_ordinal INTEGER NOT NULL CHECK (entry_ordinal >= 0),
    artifact_digest TEXT NOT NULL REFERENCES module_artifacts(artifact_digest),
    admitted_at INTEGER NOT NULL CHECK (admitted_at > 0),
    UNIQUE (
        admission_id,source_id,source_policy_id,source_policy_revision,
        snapshot_id,observation_revision,entry_ordinal,artifact_digest
    ),
    FOREIGN KEY (snapshot_id, source_id)
        REFERENCES module_discovery_snapshots(snapshot_id, source_id),
    FOREIGN KEY (snapshot_id, entry_ordinal)
        REFERENCES module_discovery_entries(snapshot_id, entry_ordinal)
) STRICT;

CREATE INDEX module_artifact_admissions_by_source
    ON module_artifact_admissions(
        source_id,observation_revision,admission_id
    );

CREATE INDEX module_artifact_admissions_by_artifact
    ON module_artifact_admissions(artifact_digest,admission_id);

-- The insertion edge is deliberately stronger than the historical foreign
-- keys: a new admission may use only the exact current unsigned local source,
-- current observation head, entry, and artifact tuple.  Later source refreshes
-- do not invalidate this immutable provenance fact.
CREATE TRIGGER module_artifact_admissions_exact_parent_guard
BEFORE INSERT ON module_artifact_admissions
WHEN NOT EXISTS (
    SELECT 1
    FROM module_sources src
    JOIN module_discovery_snapshots snap
      ON snap.snapshot_id = NEW.snapshot_id
     AND snap.source_id = src.source_id
    JOIN module_discovery_entries entry
      ON entry.snapshot_id = snap.snapshot_id
     AND entry.entry_ordinal = NEW.entry_ordinal
    JOIN module_artifacts artifact
      ON artifact.artifact_digest = NEW.artifact_digest
     AND artifact.module_id = entry.module_id
     AND artifact.exact_version = entry.exact_version
     AND artifact.artifact_size_bytes = entry.artifact_size_bytes
    WHERE src.source_id = NEW.source_id
      AND src.source_kind = 'LOCAL_DIRECTORY'
      AND src.publisher_key_id IS NULL
      AND src.source_policy_id = NEW.source_policy_id
      AND src.policy_revision = NEW.source_policy_revision
      AND src.current_snapshot_id = NEW.snapshot_id
      AND src.observation_revision = NEW.observation_revision
      AND snap.source_policy_id = NEW.source_policy_id
      AND snap.source_policy_revision = NEW.source_policy_revision
      AND snap.observation_revision = NEW.observation_revision
      AND entry.artifact_digest = NEW.artifact_digest
)
BEGIN
    SELECT RAISE(ABORT, 'module artifact admission parent is not exact current local source');
END;

CREATE TRIGGER module_artifact_admissions_reject_update
BEFORE UPDATE ON module_artifact_admissions
BEGIN
    SELECT RAISE(ABORT, 'module_artifact_admissions is append-only');
END;

CREATE TRIGGER module_artifact_admissions_reject_delete
BEFORE DELETE ON module_artifact_admissions
BEGIN
    SELECT RAISE(ABORT, 'module_artifact_admissions is append-only');
END;

CREATE TRIGGER module_artifact_admissions_source_quota
BEFORE INSERT ON module_artifact_admissions
WHEN (
    SELECT COUNT(*) FROM module_artifact_admissions
    WHERE source_id = NEW.source_id
) >= 256
BEGIN
    SELECT RAISE(ABORT, 'module_artifact_admissions source quota exceeded');
END;

CREATE TRIGGER module_artifact_admissions_store_quota
BEFORE INSERT ON module_artifact_admissions
WHEN (SELECT COUNT(*) FROM module_artifact_admissions) >= 256
BEGIN
    SELECT RAISE(ABORT, 'module_artifact_admissions store quota exceeded');
END;

-- W2-U3 keeps supply observations, tenant-scoped review facts, and terminal
-- Operator decisions immutable in the one Current Store.  None of these rows
-- grants installation, activation, binding, execution, or Apply authority.
CREATE TABLE module_upgrade_candidates (
    candidate_id TEXT PRIMARY KEY CHECK (
        length(candidate_id) = 64
        AND candidate_id = lower(candidate_id)
        AND candidate_id NOT GLOB '*[^0-9a-f]*'
    ),
    candidate_canonical BLOB NOT NULL UNIQUE CHECK (
        length(candidate_canonical) BETWEEN 1 AND 1048576
    ),
    candidate_size_bytes INTEGER NOT NULL CHECK (
        candidate_size_bytes = length(candidate_canonical)
    ),
    review_key TEXT NOT NULL CHECK (
        length(review_key) = 64
        AND review_key = lower(review_key)
        AND review_key NOT GLOB '*[^0-9a-f]*'
    ),
    source_id TEXT NOT NULL REFERENCES module_sources(source_id),
    source_policy_id TEXT NOT NULL CHECK (
        length(source_policy_id) = 64
        AND source_policy_id = lower(source_policy_id)
        AND source_policy_id NOT GLOB '*[^0-9a-f]*'
    ),
    snapshot_id TEXT NOT NULL REFERENCES module_discovery_snapshots(snapshot_id),
    target_module_id TEXT NOT NULL CHECK (
        length(target_module_id) BETWEEN 1 AND 128
        AND target_module_id = trim(target_module_id)
    ),
    target_exact_version TEXT NOT NULL CHECK (
        length(target_exact_version) BETWEEN 1 AND 64
        AND target_exact_version = trim(target_exact_version)
    ),
    target_artifact_digest TEXT NOT NULL CHECK (
        length(target_artifact_digest) = 64
        AND target_artifact_digest = lower(target_artifact_digest)
        AND target_artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    current_module_id TEXT NOT NULL CHECK (
        length(current_module_id) BETWEEN 1 AND 128
        AND current_module_id = trim(current_module_id)
    ),
    current_exact_version TEXT NOT NULL CHECK (
        length(current_exact_version) BETWEEN 1 AND 64
        AND current_exact_version = trim(current_exact_version)
    ),
    current_artifact_digest TEXT NOT NULL CHECK (
        length(current_artifact_digest) = 64
        AND current_artifact_digest = lower(current_artifact_digest)
        AND current_artifact_digest NOT GLOB '*[^0-9a-f]*'
    ),
    admitted_at INTEGER NOT NULL CHECK (admitted_at > 0),
    UNIQUE (candidate_id, review_key),
    UNIQUE (candidate_id, source_id, snapshot_id),
    FOREIGN KEY (snapshot_id, source_id)
        REFERENCES module_discovery_snapshots(snapshot_id, source_id),
    FOREIGN KEY (
        snapshot_id,
        target_module_id,
        target_exact_version
    ) REFERENCES module_discovery_entries(
        snapshot_id,
        module_id,
        exact_version
    ),
    CHECK (current_module_id = target_module_id),
    CHECK (current_exact_version <> target_exact_version),
    CHECK (current_artifact_digest <> target_artifact_digest)
) STRICT;

CREATE TRIGGER module_upgrade_candidates_reject_update
BEFORE UPDATE ON module_upgrade_candidates
BEGIN
    SELECT RAISE(ABORT, 'Module upgrade candidates are immutable');
END;

CREATE TRIGGER module_upgrade_candidates_reject_delete
BEFORE DELETE ON module_upgrade_candidates
BEGIN
    SELECT RAISE(ABORT, 'Module upgrade candidates are append-only');
END;

CREATE TABLE module_upgrade_reviews (
    review_id TEXT PRIMARY KEY CHECK (
        length(review_id) = 64
        AND review_id = lower(review_id)
        AND review_id NOT GLOB '*[^0-9a-f]*'
    ),
    review_canonical BLOB NOT NULL UNIQUE CHECK (
        length(review_canonical) BETWEEN 1 AND 1048576
    ),
    review_size_bytes INTEGER NOT NULL CHECK (
        review_size_bytes = length(review_canonical)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    candidate_id TEXT NOT NULL REFERENCES module_upgrade_candidates(candidate_id),
    review_key TEXT NOT NULL CHECK (
        length(review_key) = 64
        AND review_key = lower(review_key)
        AND review_key NOT GLOB '*[^0-9a-f]*'
    ),
    binding_target_kind TEXT NOT NULL CHECK (
        binding_target_kind IN ('PROFILE', 'WORKSPACE_CHANNEL_ENDPOINT')
    ),
    profile_id TEXT,
    workspace_id TEXT,
    endpoint_id TEXT,
    current_instance_id TEXT NOT NULL CHECK (
        length(current_instance_id) BETWEEN 1 AND 256
        AND current_instance_id = trim(current_instance_id)
    ),
    target_instance_id TEXT NOT NULL CHECK (
        length(target_instance_id) BETWEEN 1 AND 256
        AND target_instance_id = trim(target_instance_id)
    ),
    target_manifest_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    current_installation_id TEXT NOT NULL REFERENCES module_installations(installation_id),
    current_activation_id TEXT NOT NULL REFERENCES module_activations(activation_id),
    current_activation_revision INTEGER NOT NULL CHECK (
        current_activation_revision > 0
    ),
    pointer_revision INTEGER NOT NULL CHECK (pointer_revision > 0),
    control_snapshot_id TEXT NOT NULL REFERENCES control_snapshots(snapshot_id),
    control_revision INTEGER NOT NULL CHECK (control_revision > 0),
    control_digest TEXT NOT NULL CHECK (
        length(control_digest) = 64
        AND control_digest = lower(control_digest)
        AND control_digest NOT GLOB '*[^0-9a-f]*'
    ),
    catalog_generation_id TEXT NOT NULL REFERENCES runtime_catalog_generations(generation_id),
    catalog_generation INTEGER NOT NULL CHECK (catalog_generation > 0),
    catalog_digest TEXT NOT NULL CHECK (
        length(catalog_digest) = 64
        AND catalog_digest = lower(catalog_digest)
        AND catalog_digest NOT GLOB '*[^0-9a-f]*'
    ),
    conclusion TEXT NOT NULL CHECK (
        conclusion IN ('WOULD_APPLY', 'CONFLICT', 'UNSUPPORTED')
    ),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    overview_resource_kind TEXT NOT NULL DEFAULT 'MODULE_REVIEW' CHECK (
        overview_resource_kind='MODULE_REVIEW'
    ),
    overview_resource_revision INTEGER NOT NULL DEFAULT 0 CHECK (
        overview_resource_revision=0
    ),
    overview_observation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (
        overview_observation_sequence=1
    ),
    UNIQUE (review_id, tenant_id, candidate_id, review_key),
    FOREIGN KEY (candidate_id, review_key)
        REFERENCES module_upgrade_candidates(candidate_id, review_key),
    FOREIGN KEY (
        overview_resource_kind,review_id,conclusion,
        overview_resource_revision,created_at
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,state,resource_revision,updated_at
    ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (overview_resource_kind,review_id)
        REFERENCES overview_resource_heads(resource_kind,resource_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        overview_resource_kind,review_id,overview_observation_sequence
    ) REFERENCES overview_resource_heads(
        resource_kind,resource_id,observation_sequence
    ) DEFERRABLE INITIALLY DEFERRED,
    CHECK (current_instance_id <> target_instance_id),
    CHECK (
        (binding_target_kind = 'PROFILE'
            AND profile_id IS NOT NULL
            AND length(profile_id) BETWEEN 1 AND 256
            AND profile_id = trim(profile_id)
            AND workspace_id IS NULL
            AND endpoint_id IS NULL)
        OR (binding_target_kind = 'WORKSPACE_CHANNEL_ENDPOINT'
            AND profile_id IS NULL
            AND workspace_id IS NOT NULL
            AND length(workspace_id) BETWEEN 1 AND 256
            AND workspace_id = trim(workspace_id)
            AND endpoint_id IS NOT NULL
            AND length(endpoint_id) BETWEEN 1 AND 256
            AND endpoint_id = trim(endpoint_id))
    )
) STRICT;

CREATE TRIGGER module_upgrade_reviews_reject_update
BEFORE UPDATE ON module_upgrade_reviews
BEGIN
    SELECT RAISE(ABORT, 'Module upgrade reviews are immutable');
END;

CREATE TRIGGER module_upgrade_reviews_reject_delete
BEFORE DELETE ON module_upgrade_reviews
BEGIN
    SELECT RAISE(ABORT, 'Module upgrade reviews are append-only');
END;

CREATE TABLE module_candidate_decisions (
    review_id TEXT PRIMARY KEY,
    decision_id TEXT NOT NULL CHECK (
        length(decision_id) = 64
        AND decision_id = lower(decision_id)
        AND decision_id NOT GLOB '*[^0-9a-f]*'
    ),
    decision_canonical BLOB NOT NULL CHECK (
        length(decision_canonical) BETWEEN 1 AND 1048576
    ),
    decision_size_bytes INTEGER NOT NULL CHECK (
        decision_size_bytes = length(decision_canonical)
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    candidate_id TEXT NOT NULL,
    review_key TEXT NOT NULL CHECK (
        length(review_key) = 64
        AND review_key = lower(review_key)
        AND review_key NOT GLOB '*[^0-9a-f]*'
    ),
    decision TEXT NOT NULL CHECK (decision IN ('APPROVE', 'REJECT')),
    decided_at INTEGER NOT NULL CHECK (decided_at > 0),
    FOREIGN KEY (review_id, tenant_id, candidate_id, review_key)
        REFERENCES module_upgrade_reviews(review_id, tenant_id, candidate_id, review_key)
) STRICT;

CREATE UNIQUE INDEX module_candidate_decisions_tenant_reject_review_key
    ON module_candidate_decisions(tenant_id, review_key)
    WHERE decision = 'REJECT';

CREATE TABLE control_operation_receipts (
    receipt_digest TEXT PRIMARY KEY CHECK (
        length(receipt_digest) = 64
        AND receipt_digest = lower(receipt_digest)
        AND receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    tenant_id TEXT NOT NULL CHECK (
        length(tenant_id) BETWEEN 1 AND 256 AND tenant_id = trim(tenant_id)
    ),
    principal_id TEXT NOT NULL CHECK (
        length(principal_id) BETWEEN 1 AND 256
        AND principal_id = trim(principal_id)
    ),
    scope_digest TEXT NOT NULL CHECK (
        length(scope_digest) = 64
        AND scope_digest = lower(scope_digest)
        AND scope_digest NOT GLOB '*[^0-9a-f]*'
    ),
    operation TEXT NOT NULL CHECK (operation = 'MODULE_DISABLE'),
    idempotency_key_digest TEXT NOT NULL CHECK (
        length(idempotency_key_digest) = 64
        AND idempotency_key_digest = lower(idempotency_key_digest)
        AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    authorization_revision INTEGER NOT NULL CHECK (
        authorization_revision BETWEEN 1 AND 9007199254740991
    ),
    scope_set_digest TEXT NOT NULL CHECK (
        length(scope_set_digest) = 64
        AND scope_set_digest = lower(scope_set_digest)
        AND scope_set_digest NOT GLOB '*[^0-9a-f]*'
    ),
    status TEXT NOT NULL CHECK (status IN ('NO_CHANGE', 'APPLIED')),
    request_digest TEXT NOT NULL CHECK (
        length(request_digest) = 64
        AND request_digest = lower(request_digest)
        AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_canonical BLOB NOT NULL CHECK (
        typeof(request_canonical) = 'blob'
        AND length(request_canonical) BETWEEN 1 AND 8192
    ),
    request_size_bytes INTEGER NOT NULL CHECK (
        request_size_bytes = length(request_canonical)
    ),
    input_digest TEXT NOT NULL CHECK (
        length(input_digest) = 64
        AND input_digest = lower(input_digest)
        AND input_digest NOT GLOB '*[^0-9a-f]*'
    ),
    input_canonical BLOB NOT NULL CHECK (
        typeof(input_canonical) = 'blob'
        AND length(input_canonical) BETWEEN 1 AND 8192
    ),
    input_size_bytes INTEGER NOT NULL CHECK (
        input_size_bytes = length(input_canonical)
    ),
    evaluation_digest TEXT NOT NULL CHECK (
        length(evaluation_digest) = 64
        AND evaluation_digest = lower(evaluation_digest)
        AND evaluation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    evaluation_canonical BLOB NOT NULL CHECK (
        typeof(evaluation_canonical) = 'blob'
        AND length(evaluation_canonical) BETWEEN 1 AND 65536
    ),
    evaluation_size_bytes INTEGER NOT NULL CHECK (
        evaluation_size_bytes = length(evaluation_canonical)
    ),
    control_receipt_canonical BLOB NOT NULL CHECK (
        typeof(control_receipt_canonical) = 'blob'
        AND length(control_receipt_canonical) BETWEEN 1 AND 16384
    ),
    control_receipt_size_bytes INTEGER NOT NULL CHECK (
        control_receipt_size_bytes = length(control_receipt_canonical)
    ),
    pre_basis_digest TEXT NOT NULL CHECK (
        length(pre_basis_digest) = 64
        AND pre_basis_digest = lower(pre_basis_digest)
        AND pre_basis_digest NOT GLOB '*[^0-9a-f]*'
    ),
    pre_basis_canonical BLOB NOT NULL CHECK (
        typeof(pre_basis_canonical) = 'blob'
        AND length(pre_basis_canonical) BETWEEN 1 AND 8192
    ),
    pre_basis_size_bytes INTEGER NOT NULL CHECK (
        pre_basis_size_bytes = length(pre_basis_canonical)
    ),
    post_basis_digest TEXT NOT NULL CHECK (
        length(post_basis_digest) = 64
        AND post_basis_digest = lower(post_basis_digest)
        AND post_basis_digest NOT GLOB '*[^0-9a-f]*'
    ),
    post_basis_canonical BLOB NOT NULL CHECK (
        typeof(post_basis_canonical) = 'blob'
        AND length(post_basis_canonical) BETWEEN 1 AND 8192
    ),
    post_basis_size_bytes INTEGER NOT NULL CHECK (
        post_basis_size_bytes = length(post_basis_canonical)
    ),
    domain_receipt_kind TEXT CHECK (
        domain_receipt_kind IS NULL
        OR domain_receipt_kind = 'MODULE_DISABLE'
    ),
    domain_receipt_id TEXT CHECK (
        domain_receipt_id IS NULL
        OR (length(domain_receipt_id) = 64
            AND domain_receipt_id = lower(domain_receipt_id)
            AND domain_receipt_id NOT GLOB '*[^0-9a-f]*')
    ),
    domain_receipt_digest TEXT CHECK (
        domain_receipt_digest IS NULL
        OR (length(domain_receipt_digest) = 64
            AND domain_receipt_digest = lower(domain_receipt_digest)
            AND domain_receipt_digest NOT GLOB '*[^0-9a-f]*')
    ),
    domain_receipt_canonical BLOB CHECK (
        domain_receipt_canonical IS NULL
        OR (typeof(domain_receipt_canonical) = 'blob'
            AND length(domain_receipt_canonical) BETWEEN 1 AND 131072)
    ),
    domain_receipt_size_bytes INTEGER CHECK (
        domain_receipt_size_bytes IS NULL
        OR domain_receipt_size_bytes = length(domain_receipt_canonical)
    ),
    canonical_total_size_bytes INTEGER NOT NULL CHECK (
        canonical_total_size_bytes =
            request_size_bytes + input_size_bytes + evaluation_size_bytes +
            control_receipt_size_bytes + pre_basis_size_bytes +
            post_basis_size_bytes + COALESCE(domain_receipt_size_bytes, 0)
        AND canonical_total_size_bytes BETWEEN 1 AND 262144
    ),
    pre_control_snapshot_id TEXT NOT NULL
        REFERENCES control_snapshots(snapshot_id),
    pre_catalog_generation_id TEXT NOT NULL
        REFERENCES runtime_catalog_generations(generation_id),
    post_control_snapshot_id TEXT NOT NULL
        REFERENCES control_snapshots(snapshot_id),
    post_catalog_generation_id TEXT NOT NULL
        REFERENCES runtime_catalog_generations(generation_id),
    CHECK (
        (status = 'NO_CHANGE'
            AND domain_receipt_kind IS NULL
            AND domain_receipt_id IS NULL
            AND domain_receipt_digest IS NULL
            AND domain_receipt_canonical IS NULL
            AND domain_receipt_size_bytes IS NULL
            AND pre_basis_digest = post_basis_digest
            AND pre_basis_canonical = post_basis_canonical
            AND pre_control_snapshot_id = post_control_snapshot_id
            AND pre_catalog_generation_id = post_catalog_generation_id)
        OR (status = 'APPLIED'
            AND domain_receipt_kind = 'MODULE_DISABLE'
            AND domain_receipt_id IS NOT NULL
            AND domain_receipt_digest IS NOT NULL
            AND domain_receipt_canonical IS NOT NULL
            AND domain_receipt_size_bytes IS NOT NULL
            AND pre_basis_digest <> post_basis_digest
            AND pre_basis_canonical <> post_basis_canonical
            AND pre_control_snapshot_id <> post_control_snapshot_id
            AND pre_catalog_generation_id <> post_catalog_generation_id)
    )
) STRICT;

CREATE UNIQUE INDEX control_operation_receipts_identity
    ON control_operation_receipts(
        principal_id,
        scope_digest,
        operation,
        idempotency_key_digest
    );

CREATE UNIQUE INDEX control_operation_receipts_domain_id
    ON control_operation_receipts(domain_receipt_id)
    WHERE status = 'APPLIED';

CREATE UNIQUE INDEX control_operation_receipts_domain_digest
    ON control_operation_receipts(domain_receipt_digest)
    WHERE status = 'APPLIED';

CREATE INDEX control_operation_receipts_tenant
    ON control_operation_receipts(tenant_id, receipt_digest);

CREATE TRIGGER control_operation_receipts_validate_parents
BEFORE INSERT ON control_operation_receipts
WHEN NOT EXISTS (
        SELECT 1
        FROM control_snapshots
        WHERE snapshot_id = NEW.pre_control_snapshot_id
          AND tenant_id = NEW.tenant_id
    )
    OR NOT EXISTS (
        SELECT 1
        FROM runtime_catalog_generations
        WHERE generation_id = NEW.pre_catalog_generation_id
          AND tenant_id = NEW.tenant_id
          AND control_snapshot_id = NEW.pre_control_snapshot_id
    )
    OR NOT EXISTS (
        SELECT 1
        FROM control_snapshots
        WHERE snapshot_id = NEW.post_control_snapshot_id
          AND tenant_id = NEW.tenant_id
    )
    OR NOT EXISTS (
        SELECT 1
        FROM runtime_catalog_generations
        WHERE generation_id = NEW.post_catalog_generation_id
          AND tenant_id = NEW.tenant_id
          AND control_snapshot_id = NEW.post_control_snapshot_id
    )
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts parent mismatch');
END;

CREATE TRIGGER control_operation_receipts_reject_conflicting_insert
BEFORE INSERT ON control_operation_receipts
WHEN EXISTS (
        SELECT 1
        FROM control_operation_receipts
        WHERE receipt_digest = NEW.receipt_digest
    )
    OR EXISTS (
        SELECT 1
        FROM control_operation_receipts
        WHERE principal_id = NEW.principal_id
          AND scope_digest = NEW.scope_digest
          AND operation = NEW.operation
          AND idempotency_key_digest = NEW.idempotency_key_digest
    )
    OR (NEW.status = 'APPLIED' AND EXISTS (
        SELECT 1
        FROM control_operation_receipts
        WHERE status = 'APPLIED'
          AND domain_receipt_id = NEW.domain_receipt_id
    ))
    OR (NEW.status = 'APPLIED' AND EXISTS (
        SELECT 1
        FROM control_operation_receipts
        WHERE status = 'APPLIED'
          AND domain_receipt_digest = NEW.domain_receipt_digest
    ))
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts is append-only');
END;

CREATE TRIGGER control_operation_receipts_reject_update
BEFORE UPDATE ON control_operation_receipts
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts is append-only');
END;

CREATE TRIGGER control_operation_receipts_reject_delete
BEFORE DELETE ON control_operation_receipts
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts is append-only');
END;

CREATE TRIGGER control_operation_receipts_tenant_quota
BEFORE INSERT ON control_operation_receipts
WHEN (
    SELECT COUNT(*)
    FROM control_operation_receipts
    WHERE tenant_id = NEW.tenant_id
) >= 1024
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts tenant quota exceeded');
END;

CREATE TRIGGER control_operation_receipts_store_quota
BEFORE INSERT ON control_operation_receipts
WHEN (SELECT COUNT(*) FROM control_operation_receipts) >= 8192
BEGIN
    SELECT RAISE(ABORT, 'control_operation_receipts store quota exceeded');
END;
