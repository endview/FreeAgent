package currentstore

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var controlOperationReceiptColumnNames = []string{
	"receipt_digest",
	"tenant_id",
	"principal_id",
	"scope_digest",
	"operation",
	"idempotency_key_digest",
	"authorization_revision",
	"scope_set_digest",
	"status",
	"request_digest",
	"request_canonical",
	"request_size_bytes",
	"input_digest",
	"input_canonical",
	"input_size_bytes",
	"evaluation_digest",
	"evaluation_canonical",
	"evaluation_size_bytes",
	"control_receipt_canonical",
	"control_receipt_size_bytes",
	"pre_basis_digest",
	"pre_basis_canonical",
	"pre_basis_size_bytes",
	"post_basis_digest",
	"post_basis_canonical",
	"post_basis_size_bytes",
	"domain_receipt_kind",
	"domain_receipt_id",
	"domain_receipt_digest",
	"domain_receipt_canonical",
	"domain_receipt_size_bytes",
	"canonical_total_size_bytes",
	"pre_control_snapshot_id",
	"pre_catalog_generation_id",
	"post_control_snapshot_id",
	"post_catalog_generation_id",
}

var controlOperationReceiptInsertSQL = fmt.Sprintf(
	"INSERT INTO control_operation_receipts(%s) VALUES(%s)",
	strings.Join(controlOperationReceiptColumnNames, ","),
	strings.TrimSuffix(strings.Repeat("?,", len(controlOperationReceiptColumnNames)), ","),
)

type receiptSchemaParents struct {
	preControl  string
	preCatalog  string
	postControl string
	postCatalog string
}

func TestControlOperationReceiptSchemaAllowlistIsFrozen(t *testing.T) {
	db := createSchemaTestDatabase(t)
	rows, err := db.Query(`PRAGMA table_info(control_operation_receipts)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var gotNames []string
	var gotTypes []string
	for rows.Next() {
		var (
			columnID    int
			name        string
			columnType  string
			notNull     int
			defaultExpr sql.NullString
			primaryKey  int
		)
		if err := rows.Scan(
			&columnID,
			&name,
			&columnType,
			&notNull,
			&defaultExpr,
			&primaryKey,
		); err != nil {
			t.Fatal(err)
		}
		if columnID != len(gotNames) || defaultExpr.Valid {
			t.Fatalf(
				"column %q metadata = id %d/default %q",
				name,
				columnID,
				defaultExpr.String,
			)
		}
		gotNames = append(gotNames, name)
		gotTypes = append(gotTypes, columnType)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotNames, controlOperationReceiptColumnNames) {
		t.Fatalf("receipt columns = %v, want %v", gotNames, controlOperationReceiptColumnNames)
	}

	wantTypes := make([]string, len(controlOperationReceiptColumnNames))
	for index, name := range controlOperationReceiptColumnNames {
		switch {
		case strings.HasSuffix(name, "_canonical"):
			wantTypes[index] = "BLOB"
		case name == "authorization_revision",
			strings.HasSuffix(name, "_size_bytes"):
			wantTypes[index] = "INTEGER"
		default:
			wantTypes[index] = "TEXT"
		}
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("receipt column types = %v, want %v", gotTypes, wantTypes)
	}

	var strict int
	if err := db.QueryRow(`
		SELECT strict
		FROM pragma_table_list
		WHERE schema = 'main' AND name = 'control_operation_receipts'
	`).Scan(&strict); err != nil {
		t.Fatal(err)
	}
	if strict != 1 {
		t.Fatalf("control_operation_receipts strict = %d, want 1", strict)
	}

	assertReceiptSchemaObjectNames(
		t,
		db,
		"index",
		[]string{
			"control_operation_receipts_domain_digest",
			"control_operation_receipts_domain_id",
			"control_operation_receipts_identity",
			"control_operation_receipts_tenant",
		},
	)
	assertReceiptSchemaObjectNames(
		t,
		db,
		"trigger",
		[]string{
			"control_operation_receipts_reject_conflicting_insert",
			"control_operation_receipts_reject_delete",
			"control_operation_receipts_reject_update",
			"control_operation_receipts_store_quota",
			"control_operation_receipts_tenant_quota",
			"control_operation_receipts_validate_parents",
		},
	)
}

func TestControlOperationReceiptShapeAndCeilings(t *testing.T) {
	db := createSchemaTestDatabase(t)
	parents := seedReceiptSchemaParents(t, db, "tenant-a")

	if _, err := execReceiptSchemaRow(
		db,
		newReceiptSchemaRow(1, "tenant-a", parents, false),
	); err != nil {
		t.Fatalf("insert valid NO_CHANGE receipt: %v", err)
	}
	if _, err := execReceiptSchemaRow(
		db,
		newReceiptSchemaRow(2, "tenant-a", parents, true),
	); err != nil {
		t.Fatalf("insert valid APPLIED receipt: %v", err)
	}

	maximum := newReceiptSchemaRow(3, "tenant-a", parents, true)
	setReceiptSchemaCanonical(maximum, "request_canonical", "request_size_bytes", bytes.Repeat([]byte{'r'}, 8192))
	setReceiptSchemaCanonical(maximum, "input_canonical", "input_size_bytes", bytes.Repeat([]byte{'i'}, 8192))
	setReceiptSchemaCanonical(maximum, "evaluation_canonical", "evaluation_size_bytes", bytes.Repeat([]byte{'e'}, 65536))
	setReceiptSchemaCanonical(maximum, "control_receipt_canonical", "control_receipt_size_bytes", bytes.Repeat([]byte{'c'}, 16384))
	setReceiptSchemaCanonical(maximum, "pre_basis_canonical", "pre_basis_size_bytes", bytes.Repeat([]byte{'p'}, 8192))
	setReceiptSchemaCanonical(maximum, "post_basis_canonical", "post_basis_size_bytes", bytes.Repeat([]byte{'q'}, 8192))
	setReceiptSchemaCanonical(maximum, "domain_receipt_canonical", "domain_receipt_size_bytes", bytes.Repeat([]byte{'d'}, 131072))
	if got := maximum["canonical_total_size_bytes"]; got != int64(245760) {
		t.Fatalf("maximum canonical total = %v, want 245760", got)
	}
	if _, err := execReceiptSchemaRow(db, maximum); err != nil {
		t.Fatalf("insert receipt at every individual ceiling: %v", err)
	}

	tests := []struct {
		name    string
		applied bool
		mutate  func(map[string]any)
	}{
		{
			name: "unsupported operation",
			mutate: func(row map[string]any) {
				row["operation"] = "MODULE_APPLY"
			},
		},
		{
			name: "rejected status",
			mutate: func(row map[string]any) {
				row["status"] = "REJECTED"
			},
		},
		{
			name: "zero authorization revision",
			mutate: func(row map[string]any) {
				setReceiptSchemaValue(row, "authorization_revision", int64(0))
			},
		},
		{
			name: "non JSON-safe authorization revision",
			mutate: func(row map[string]any) {
				setReceiptSchemaValue(row, "authorization_revision", int64(9007199254740992))
			},
		},
		{
			name: "uppercase digest",
			mutate: func(row map[string]any) {
				row["request_digest"] = strings.Repeat("A", 64)
			},
		},
		{
			name: "canonical text instead of blob",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "request_canonical", "request_size_bytes", "{}")
			},
		},
		{
			name: "empty request canonical",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "request_canonical", "request_size_bytes", []byte{})
			},
		},
		{
			name: "request over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "request_canonical", "request_size_bytes", make([]byte, 8193))
			},
		},
		{
			name: "input over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "input_canonical", "input_size_bytes", make([]byte, 8193))
			},
		},
		{
			name: "evaluation over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "evaluation_canonical", "evaluation_size_bytes", make([]byte, 65537))
			},
		},
		{
			name: "control receipt over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "control_receipt_canonical", "control_receipt_size_bytes", make([]byte, 16385))
			},
		},
		{
			name: "pre basis over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "pre_basis_canonical", "pre_basis_size_bytes", make([]byte, 8193))
			},
		},
		{
			name: "post basis over ceiling",
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "post_basis_canonical", "post_basis_size_bytes", make([]byte, 8193))
			},
		},
		{
			name:    "domain receipt over ceiling",
			applied: true,
			mutate: func(row map[string]any) {
				setReceiptSchemaCanonical(row, "domain_receipt_canonical", "domain_receipt_size_bytes", make([]byte, 131073))
			},
		},
		{
			name: "mismatched component size",
			mutate: func(row map[string]any) {
				row["request_size_bytes"] = row["request_size_bytes"].(int64) + 1
				refreshReceiptSchemaTotal(row)
			},
		},
		{
			name: "mismatched total size",
			mutate: func(row map[string]any) {
				row["canonical_total_size_bytes"] = int64(262145)
			},
		},
		{
			name: "NO_CHANGE domain receipt",
			mutate: func(row map[string]any) {
				setReceiptSchemaDomain(row, 100)
			},
		},
		{
			name: "NO_CHANGE changed basis",
			mutate: func(row map[string]any) {
				row["post_basis_digest"] = receiptSchemaDigest("changed-post")
			},
		},
		{
			name:    "APPLIED missing domain ID",
			applied: true,
			mutate: func(row map[string]any) {
				row["domain_receipt_id"] = nil
			},
		},
		{
			name:    "APPLIED wrong domain kind",
			applied: true,
			mutate: func(row map[string]any) {
				row["domain_receipt_kind"] = "MODULE_APPLY"
			},
		},
		{
			name:    "APPLIED uppercase domain ID",
			applied: true,
			mutate: func(row map[string]any) {
				row["domain_receipt_id"] = strings.Repeat("A", 64)
			},
		},
		{
			name:    "APPLIED short domain ID",
			applied: true,
			mutate: func(row map[string]any) {
				row["domain_receipt_id"] = strings.Repeat("a", 63)
			},
		},
		{
			name:    "APPLIED non-hex domain ID",
			applied: true,
			mutate: func(row map[string]any) {
				row["domain_receipt_id"] = strings.Repeat("a", 63) + "g"
			},
		},
		{
			name:    "APPLIED unchanged basis",
			applied: true,
			mutate: func(row map[string]any) {
				row["post_basis_digest"] = row["pre_basis_digest"]
				setReceiptSchemaCanonical(
					row,
					"post_basis_canonical",
					"post_basis_size_bytes",
					row["pre_basis_canonical"],
				)
				row["post_control_snapshot_id"] = row["pre_control_snapshot_id"]
				row["post_catalog_generation_id"] = row["pre_catalog_generation_id"]
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := newReceiptSchemaRow(1000+index, "tenant-a", parents, test.applied)
			test.mutate(row)
			if _, err := execReceiptSchemaRow(db, row); err == nil {
				t.Fatal("invalid receipt row was accepted")
			}
		})
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM control_operation_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("persisted receipt rows = %d, want 3 valid rows", count)
	}
}

func TestControlOperationReceiptIdentityDomainAndAppendOnly(t *testing.T) {
	db := createSchemaTestDatabase(t)
	parents := seedReceiptSchemaParents(t, db, "tenant-a")
	original := newReceiptSchemaRow(1, "tenant-a", parents, true)
	if _, err := execReceiptSchemaRow(db, original); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "identity",
			mutate: func(row map[string]any) {
				row["principal_id"] = original["principal_id"]
				row["scope_digest"] = original["scope_digest"]
				row["idempotency_key_digest"] = original["idempotency_key_digest"]
			},
		},
		{
			name: "domain ID",
			mutate: func(row map[string]any) {
				row["domain_receipt_id"] = original["domain_receipt_id"]
			},
		},
		{
			name: "domain digest",
			mutate: func(row map[string]any) {
				row["domain_receipt_digest"] = original["domain_receipt_digest"]
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := newReceiptSchemaRow(10+index, "tenant-a", parents, true)
			test.mutate(row)
			if _, err := execReceiptSchemaRow(db, row); err == nil ||
				!strings.Contains(err.Error(), "append-only") {
				t.Fatalf("conflicting insert error = %v, want append-only rejection", err)
			}
		})
	}

	for _, statement := range []string{
		`UPDATE control_operation_receipts SET status = status`,
		`UPDATE OR REPLACE control_operation_receipts SET status = status`,
		`DELETE FROM control_operation_receipts`,
	} {
		if _, err := db.Exec(statement); err == nil ||
			!strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%q error = %v, want append-only rejection", statement, err)
		}
	}

	replaceSQL := strings.Replace(
		controlOperationReceiptInsertSQL,
		"INSERT INTO",
		"INSERT OR REPLACE INTO",
		1,
	)
	if _, err := db.Exec(replaceSQL, receiptSchemaRowArgs(original)...); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("INSERT OR REPLACE error = %v, want append-only rejection", err)
	}

	var count int
	var digest string
	if err := db.QueryRow(`
		SELECT COUNT(*), min(receipt_digest)
		FROM control_operation_receipts
	`).Scan(&count, &digest); err != nil {
		t.Fatal(err)
	}
	if count != 1 || digest != original["receipt_digest"] {
		t.Fatalf("append-only row after attacks = %d/%q", count, digest)
	}
}

func TestControlOperationReceiptParentClosure(t *testing.T) {
	db := createSchemaTestDatabase(t)
	parents := seedReceiptSchemaParents(t, db, "tenant-a")
	otherParents := seedReceiptSchemaParents(t, db, "tenant-b")

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "missing parent",
			mutate: func(row map[string]any) {
				row["pre_control_snapshot_id"] = "missing-snapshot"
				row["post_control_snapshot_id"] = "missing-snapshot"
			},
		},
		{
			name: "cross tenant parents",
			mutate: func(row map[string]any) {
				row["pre_control_snapshot_id"] = otherParents.preControl
				row["post_control_snapshot_id"] = otherParents.preControl
				row["pre_catalog_generation_id"] = otherParents.preCatalog
				row["post_catalog_generation_id"] = otherParents.preCatalog
			},
		},
		{
			name: "catalog and control mismatch",
			mutate: func(row map[string]any) {
				row["pre_catalog_generation_id"] = parents.postCatalog
				row["post_catalog_generation_id"] = parents.postCatalog
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := newReceiptSchemaRow(index+1, "tenant-a", parents, false)
			test.mutate(row)
			if _, err := execReceiptSchemaRow(db, row); err == nil ||
				!strings.Contains(err.Error(), "parent mismatch") {
				t.Fatalf("invalid parent error = %v, want parent mismatch", err)
			}
		})
	}
}

func TestControlOperationReceiptQuotasFailClosed(t *testing.T) {
	t.Run("tenant 1024", func(t *testing.T) {
		db := createSchemaTestDatabase(t)
		parents := seedReceiptSchemaParents(t, db, "tenant-quota")
		fillReceiptSchemaRows(t, db, "tenant-quota", parents, 1, 1024)
		row := newReceiptSchemaRow(1025, "tenant-quota", parents, false)
		if _, err := execReceiptSchemaRow(db, row); err == nil ||
			!strings.Contains(err.Error(), "tenant quota exceeded") {
			t.Fatalf("tenant row 1025 error = %v, want tenant quota", err)
		}
		assertReceiptSchemaRowCount(t, db, 1024)
	})

	t.Run("store 8192", func(t *testing.T) {
		db := createSchemaTestDatabase(t)
		for tenantIndex := range 8 {
			tenantID := fmt.Sprintf("tenant-store-%d", tenantIndex)
			parents := seedReceiptSchemaParents(t, db, tenantID)
			fillReceiptSchemaRows(
				t,
				db,
				tenantID,
				parents,
				tenantIndex*1024+1,
				1024,
			)
		}
		parents := seedReceiptSchemaParents(t, db, "tenant-store-overflow")
		row := newReceiptSchemaRow(8193, "tenant-store-overflow", parents, false)
		if _, err := execReceiptSchemaRow(db, row); err == nil ||
			!strings.Contains(err.Error(), "store quota exceeded") {
			t.Fatalf("store row 8193 error = %v, want store quota", err)
		}
		assertReceiptSchemaRowCount(t, db, 8192)
	})
}

func assertReceiptSchemaObjectNames(
	t *testing.T,
	db *sql.DB,
	objectType string,
	want []string,
) {
	t.Helper()
	rows, err := db.Query(`
		SELECT name
		FROM sqlite_schema
		WHERE type = ?
		  AND tbl_name = 'control_operation_receipts'
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`, objectType)
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt %ss = %v, want %v", objectType, got, want)
	}
}

func seedReceiptSchemaParents(
	t *testing.T,
	db *sql.DB,
	tenantID string,
) receiptSchemaParents {
	t.Helper()
	parents := receiptSchemaParents{
		preControl:  "control-pre-" + tenantID,
		preCatalog:  "catalog-pre-" + tenantID,
		postControl: "control-post-" + tenantID,
		postCatalog: "catalog-post-" + tenantID,
	}
	if _, err := db.Exec(`
		INSERT INTO control_snapshots(
			snapshot_id, tenant_id, revision, canonical_json, digest, published_at
		) VALUES
			(?, ?, 1, X'7B7D', ?, 1),
			(?, ?, 2, X'7B7D', ?, 2)
	`,
		parents.preControl,
		tenantID,
		receiptSchemaDigest("pre-control-"+tenantID),
		parents.postControl,
		tenantID,
		receiptSchemaDigest("post-control-"+tenantID),
	); err != nil {
		t.Fatalf("seed receipt control parents for %q: %v", tenantID, err)
	}
	if _, err := db.Exec(`
		INSERT INTO runtime_catalog_generations(
			generation_id, tenant_id, generation, control_snapshot_id,
			canonical_json, digest, published_at
		) VALUES
			(?, ?, 1, ?, X'7B7D', ?, 1),
			(?, ?, 2, ?, X'7B7D', ?, 2)
	`,
		parents.preCatalog,
		tenantID,
		parents.preControl,
		receiptSchemaDigest("pre-catalog-"+tenantID),
		parents.postCatalog,
		tenantID,
		parents.postControl,
		receiptSchemaDigest("post-catalog-"+tenantID),
	); err != nil {
		t.Fatalf("seed receipt catalog parents for %q: %v", tenantID, err)
	}
	return parents
}

func newReceiptSchemaRow(
	sequence int,
	tenantID string,
	parents receiptSchemaParents,
	applied bool,
) map[string]any {
	preCanonical := []byte(fmt.Sprintf(`{"basis":"pre-%d"}`, sequence))
	row := map[string]any{
		"receipt_digest":             receiptSchemaDigest(fmt.Sprintf("receipt-%d", sequence)),
		"tenant_id":                  tenantID,
		"principal_id":               "principal-" + tenantID,
		"scope_digest":               receiptSchemaDigest("scope-" + tenantID),
		"operation":                  "MODULE_DISABLE",
		"idempotency_key_digest":     receiptSchemaDigest(fmt.Sprintf("key-%d", sequence)),
		"scope_set_digest":           receiptSchemaDigest("scope-set-" + tenantID),
		"status":                     "NO_CHANGE",
		"request_digest":             receiptSchemaDigest(fmt.Sprintf("request-%d", sequence)),
		"request_canonical":          []byte(fmt.Sprintf(`{"request":%d}`, sequence)),
		"input_digest":               receiptSchemaDigest(fmt.Sprintf("input-%d", sequence)),
		"input_canonical":            []byte(fmt.Sprintf(`{"input":%d}`, sequence)),
		"evaluation_digest":          receiptSchemaDigest(fmt.Sprintf("evaluation-%d", sequence)),
		"evaluation_canonical":       []byte(fmt.Sprintf(`{"evaluation":%d}`, sequence)),
		"control_receipt_canonical":  []byte(fmt.Sprintf(`{"receipt":%d}`, sequence)),
		"pre_basis_digest":           receiptSchemaDigest(fmt.Sprintf("pre-basis-%d", sequence)),
		"pre_basis_canonical":        preCanonical,
		"post_basis_digest":          receiptSchemaDigest(fmt.Sprintf("pre-basis-%d", sequence)),
		"post_basis_canonical":       bytes.Clone(preCanonical),
		"domain_receipt_kind":        nil,
		"domain_receipt_id":          nil,
		"domain_receipt_digest":      nil,
		"domain_receipt_canonical":   nil,
		"domain_receipt_size_bytes":  nil,
		"pre_control_snapshot_id":    parents.preControl,
		"pre_catalog_generation_id":  parents.preCatalog,
		"post_control_snapshot_id":   parents.preControl,
		"post_catalog_generation_id": parents.preCatalog,
	}
	setReceiptSchemaValue(row, "authorization_revision", int64(1))
	for _, columns := range [][2]string{
		{"request_canonical", "request_size_bytes"},
		{"input_canonical", "input_size_bytes"},
		{"evaluation_canonical", "evaluation_size_bytes"},
		{"control_receipt_canonical", "control_receipt_size_bytes"},
		{"pre_basis_canonical", "pre_basis_size_bytes"},
		{"post_basis_canonical", "post_basis_size_bytes"},
	} {
		setReceiptSchemaCanonical(row, columns[0], columns[1], row[columns[0]])
	}
	if applied {
		row["status"] = "APPLIED"
		row["post_basis_digest"] = receiptSchemaDigest(fmt.Sprintf("post-basis-%d", sequence))
		setReceiptSchemaCanonical(
			row,
			"post_basis_canonical",
			"post_basis_size_bytes",
			[]byte(fmt.Sprintf(`{"basis":"post-%d"}`, sequence)),
		)
		row["post_control_snapshot_id"] = parents.postControl
		row["post_catalog_generation_id"] = parents.postCatalog
		setReceiptSchemaDomain(row, sequence)
	}
	refreshReceiptSchemaTotal(row)
	return row
}

func setReceiptSchemaValue(row map[string]any, column string, value any) {
	row[column] = value
}

func setReceiptSchemaDomain(row map[string]any, sequence int) {
	row["domain_receipt_kind"] = "MODULE_DISABLE"
	row["domain_receipt_id"] = receiptSchemaDigest(fmt.Sprintf("domain-id-%d", sequence))
	row["domain_receipt_digest"] = receiptSchemaDigest(fmt.Sprintf("domain-%d", sequence))
	setReceiptSchemaCanonical(
		row,
		"domain_receipt_canonical",
		"domain_receipt_size_bytes",
		[]byte(fmt.Sprintf(`{"domain":%d}`, sequence)),
	)
}

func setReceiptSchemaCanonical(
	row map[string]any,
	canonicalColumn string,
	sizeColumn string,
	value any,
) {
	row[canonicalColumn] = value
	switch value := value.(type) {
	case []byte:
		row[sizeColumn] = int64(len(value))
	case string:
		row[sizeColumn] = int64(len(value))
	case nil:
		row[sizeColumn] = nil
	default:
		panic(fmt.Sprintf("unsupported receipt schema canonical type %T", value))
	}
	refreshReceiptSchemaTotal(row)
}

func refreshReceiptSchemaTotal(row map[string]any) {
	var total int64
	for _, column := range []string{
		"request_size_bytes",
		"input_size_bytes",
		"evaluation_size_bytes",
		"control_receipt_size_bytes",
		"pre_basis_size_bytes",
		"post_basis_size_bytes",
		"domain_receipt_size_bytes",
	} {
		if row[column] != nil {
			total += row[column].(int64)
		}
	}
	row["canonical_total_size_bytes"] = total
}

func execReceiptSchemaRow(
	execer interface {
		Exec(string, ...any) (sql.Result, error)
	},
	row map[string]any,
) (sql.Result, error) {
	return execer.Exec(controlOperationReceiptInsertSQL, receiptSchemaRowArgs(row)...)
}

func receiptSchemaRowArgs(row map[string]any) []any {
	arguments := make([]any, len(controlOperationReceiptColumnNames))
	for index, column := range controlOperationReceiptColumnNames {
		arguments[index] = row[column]
	}
	return arguments
}

func fillReceiptSchemaRows(
	t *testing.T,
	db *sql.DB,
	tenantID string,
	parents receiptSchemaParents,
	firstSequence int,
	count int,
) {
	t.Helper()
	transaction, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	statement, err := transaction.Prepare(controlOperationReceiptInsertSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for offset := range count {
		row := newReceiptSchemaRow(firstSequence+offset, tenantID, parents, false)
		if _, err := statement.Exec(receiptSchemaRowArgs(row)...); err != nil {
			t.Fatalf("insert receipt quota fixture row %d: %v", offset, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertReceiptSchemaRowCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM control_operation_receipts`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("control operation receipt count = %d, want %d", got, want)
	}
}

func receiptSchemaDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest)
}
