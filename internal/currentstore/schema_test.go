package currentstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
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
	db := createSchemaV1TestDatabase(t)
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
	if len(got) != 42 {
		t.Fatalf("ordinary table count = %d, want 42", len(got))
	}
}

func TestSchemaFingerprintIsFrozenAndDetectsDrift(t *testing.T) {
	db := createSchemaV1TestDatabase(t)
	got, err := DatabaseSchemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatalf("computed schema fingerprint before freeze: %s", got)
	}
	const wantV1Fingerprint = "9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561"
	if got != wantV1Fingerprint {
		t.Fatalf(
			"schema fingerprint = %s, want %s",
			got,
			wantV1Fingerprint,
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
	entries, err := fs.ReadDir(migrationFS, "migrations/fac2")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].IsDir() || entries[0].Name() != "0001_current.sql" ||
		entries[1].IsDir() || entries[1].Name() != "0002_server_owned_review.sql" {
		t.Fatalf("embedded migrations = %+v", entries)
	}
}

func TestMigration0001BytesAreFrozen(t *testing.T) {
	if Migration0001SHA256 != "dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a" {
		t.Fatalf("Migration0001SHA256 = %q", Migration0001SHA256)
	}
	if _, err := Migration0001(); err != nil {
		t.Fatal(err)
	}
}

func TestFAC1MigrationBytesRemainFrozen(t *testing.T) {
	content, err := os.ReadFile("migrations/0001_current.sql")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if got := fmt.Sprintf("%x", digest); got !=
		"6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86" {
		t.Fatalf("FAC1 migration SHA-256 = %s", got)
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

func TestCurrentStoreIdentityConstantsAreFAC2(t *testing.T) {
	if ApplicationID != 0x46414332 ||
		UserVersion != 2 ||
		SchemaIdentity != "github.com/endview/freeagent/current-store-v2" ||
		GeneratorID != "freeagent-current-store-v2" {
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
	return createSchemaDatabaseWithMigrationV2(t, true)
}

func createSchemaV1TestDatabase(t *testing.T) *sql.DB {
	return createSchemaDatabaseWithMigrationV2(t, false)
}

func createSchemaDatabaseWithMigrationV2(t *testing.T, current bool) *sql.DB {
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
	if current {
		migration, err = Migration0002()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatalf("apply 0002_server_owned_review.sql: %v", err)
		}
	}
	return db
}
