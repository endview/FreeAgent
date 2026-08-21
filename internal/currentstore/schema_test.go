package currentstore

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

var currentTableNames = []string{
	"agent_memory_revisions",
	"channel_ingress_receipts",
	"content_records",
	"control_current",
	"control_operation_receipts",
	"control_snapshots",
	"conversations",
	"dispatch_attempts",
	"history_entries",
	"learning_cycle_schedules",
	"learning_cycle_tasks",
	"learning_proposals",
	"loop_frames",
	"member_execution_snapshots",
	"model_dispatch_attempts",
	"model_price_snapshots",
	"model_usage",
	"module_activations",
	"module_artifact_admissions",
	"module_artifacts",
	"module_candidate_decisions",
	"module_discovery_entries",
	"module_discovery_module_refs",
	"module_discovery_snapshots",
	"module_installations",
	"module_publisher_keys",
	"module_sources",
	"module_upgrade_candidates",
	"module_upgrade_reviews",
	"overview_basis_heads",
	"overview_basis_snapshots",
	"overview_basis_workspaces",
	"overview_resource_heads",
	"overview_resource_snapshots",
	"overview_resource_transition_carriers",
	"run_events",
	"run_manifests",
	"run_observation_heads",
	"run_observation_snapshots",
	"runs",
	"runtime_catalog_generations",
	"store_meta",
	"workspace_scheduler_state",
}

func TestMigration0001CreatesExactlyCurrentStoreTables(t *testing.T) {
	db := createSchemaTestDatabase(t)
	rows, err := db.Query(`
		SELECT name
		FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, currentTableNames) {
		t.Fatalf("tables = %v, want %v", got, currentTableNames)
	}
	if len(got) != 43 {
		t.Fatalf("ordinary table count = %d, want 43", len(got))
	}
}

func TestSchemaFingerprintIsFrozenAndDetectsDrift(t *testing.T) {
	db := createSchemaTestDatabase(t)
	got, err := DatabaseSchemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ExpectedSchemaFingerprint == "PENDING" {
		t.Fatalf("computed schema fingerprint before freeze: %s", got)
	}
	if got != ExpectedSchemaFingerprint {
		t.Fatalf(
			"schema fingerprint = %s, want %s",
			got,
			ExpectedSchemaFingerprint,
		)
	}
	if _, err := db.Exec(
		`CREATE INDEX forbidden_drift ON runs(tenant_id)`,
	); err != nil {
		t.Fatal(err)
	}
	drifted, err := DatabaseSchemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if drifted == got {
		t.Fatal("schema drift did not change the fingerprint")
	}
}

func TestSchemaFingerprintOrderingAndLineEndingsAreDeterministic(t *testing.T) {
	objects := []SchemaObject{
		{
			Type: "table", Name: "é", TableName: "é",
			SQL: "  CREATE TABLE é(id TEXT)\r\n",
		},
		{
			Type: "index", Name: "z", TableName: "é",
			SQL: "CREATE INDEX z ON é(id)\r",
		},
	}
	first, err := ComputeSchemaFingerprint(objects)
	if err != nil {
		t.Fatal(err)
	}
	objects[0], objects[1] = objects[1], objects[0]
	objects[0].SQL = "CREATE INDEX z ON é(id)\n"
	objects[1].SQL = "CREATE TABLE é(id TEXT)"
	second, err := ComputeSchemaFingerprint(objects)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent schema fingerprints differ: %s %s", first, second)
	}
}

func TestCurrentStoreEmbedsOnlyMigration0001(t *testing.T) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 ||
		entries[0].IsDir() ||
		entries[0].Name() != "0001_current.sql" {
		t.Fatalf("embedded migrations = %+v", entries)
	}
}

func TestMigrationHasValidForeignKeysAndIntegrity(t *testing.T) {
	db := createSchemaTestDatabase(t)
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("fresh Current Store has a foreign-key violation")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}
}

func TestMigrationModuleActivationExecutionClassAllowsOnlyCurrentSet(t *testing.T) {
	db := createSchemaTestDatabase(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{
		"DECLARATIVE",
		"TRUSTED_IN_PROCESS",
		"LOCAL_PROCESS",
		"REMOTE",
		"WASM",
	} {
		if _, err := db.Exec(`
			INSERT INTO module_activations(
				activation_id, tenant_id, instance_id, installation_id,
				activation_revision, execution_class, adapter_identity,
				activated_at
			) VALUES(?, 'tenant', ?, 'installation', 1, ?, 'adapter', 1)
		`, "activation-"+strings.ToLower(class), "instance-"+strings.ToLower(class), class); err != nil {
			t.Fatalf("insert execution class %q: %v", class, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO module_activations(
			activation_id, tenant_id, instance_id, installation_id,
			activation_revision, execution_class, adapter_identity,
			activated_at
		) VALUES('activation-sandbox', 'tenant', 'instance-sandbox',
			'installation', 1, 'SANDBOX', 'adapter', 1)
	`); err == nil {
		t.Fatal("migration CHECK accepted unknown execution class SANDBOX")
	}
}

func TestMigrationContentKindAllowsOnlyFrozenRunCancellationKind(t *testing.T) {
	db := createSchemaTestDatabase(t)
	canonical := []byte(`{"reason_code":"USER_REQUEST","root_manifest_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_run_id":"run-1","schema_version":"cancel-request/v1","scope":"run"}`)
	digest, err := ComputeContentDigest(
		ContentRunCancellation,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`, digest, string(ContentRunCancellation), admissionJSONMediaType, canonical, len(canonical), int64(1)); err != nil {
		t.Fatalf("insert RUN_CANCELLATION: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, 'CANCELLATION', 'application/octet-stream', X'00', 1, 1)
	`, strings.Repeat("b", 64)); err == nil {
		t.Fatal("migration CHECK accepted an unfrozen cancellation kind")
	}
}

func TestCurrentStoreIdentityConstantsAreFAC1(t *testing.T) {
	if ApplicationID != 0x46414331 ||
		UserVersion != 1 ||
		SchemaIdentity != "github.com/endview/freeagent/current-store-v1" ||
		GeneratorID != "freeagent-current-store-draft-v1" {
		t.Fatalf(
			"identity = %#x/%d/%q/%q",
			ApplicationID,
			UserVersion,
			SchemaIdentity,
			GeneratorID,
		)
	}
}

func createSchemaTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close schema test database: %v", err)
		}
	})
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	migration, err := Migration0001()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply 0001_current.sql: %v", err)
	}
	return db
}
