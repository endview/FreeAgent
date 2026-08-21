// Package currentstore owns the single FAC1 SQLite schema used by the current
// Core runtime. It intentionally has no dependency on the legacy Store.
package currentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	SchemaIdentity = "github.com/endview/freeagent/current-store-v1"
	ApplicationID  = 1178682161 // 0x46414331, ASCII FAC1.
	UserVersion    = 1
	GeneratorID    = "freeagent-current-store-draft-v1"

	schemaFingerprintDomain = "freeagent.current-store.schema-fingerprint.v1\n"
)

// ExpectedSchemaFingerprint freezes the exact sqlite_schema projection
// produced by 0001_current.sql.
const ExpectedSchemaFingerprint = "47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d"

//go:embed migrations/0001_current.sql
var migrationFS embed.FS

// Migration0001 returns an owned copy of the only Current Store migration.
func Migration0001() ([]byte, error) {
	content, err := migrationFS.ReadFile("migrations/0001_current.sql")
	if err != nil {
		return nil, fmt.Errorf("currentstore: read 0001 migration: %w", err)
	}
	return bytes.Clone(content), nil
}

// SchemaObject is the sqlite_schema projection covered by the FAC1 schema
// fingerprint.
type SchemaObject struct {
	Type      string
	Name      string
	TableName string
	SQL       string
}

// LoadSchemaObjects reads exactly the non-internal sqlite_schema objects whose
// SQL text participates in the fingerprint.
func LoadSchemaObjects(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
) ([]SchemaObject, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql
		FROM sqlite_schema
		WHERE sql IS NOT NULL
		  AND name NOT GLOB 'sqlite_*'
	`)
	if err != nil {
		return nil, fmt.Errorf("currentstore: read sqlite_schema: %w", err)
	}
	defer rows.Close()

	var objects []SchemaObject
	for rows.Next() {
		var object SchemaObject
		if err := rows.Scan(
			&object.Type,
			&object.Name,
			&object.TableName,
			&object.SQL,
		); err != nil {
			return nil, fmt.Errorf("currentstore: scan sqlite_schema: %w", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("currentstore: iterate sqlite_schema: %w", err)
	}
	return objects, nil
}

// ComputeSchemaFingerprint implements CURRENT_STORE_V1 section 3.3. It only
// normalizes line endings and surrounding whitespace in SQL text.
func ComputeSchemaFingerprint(objects []SchemaObject) (string, error) {
	normalized := make([]SchemaObject, len(objects))
	for index, object := range objects {
		for field, value := range map[string]string{
			"type":       object.Type,
			"name":       object.Name,
			"table name": object.TableName,
			"SQL":        object.SQL,
		} {
			if !utf8.ValidString(value) {
				return "", fmt.Errorf(
					"currentstore: schema object %s is not valid UTF-8",
					field,
				)
			}
			if strings.IndexByte(value, 0) >= 0 {
				return "", fmt.Errorf(
					"currentstore: schema object %s contains NUL",
					field,
				)
			}
		}
		if object.Type == "" || object.Name == "" || object.TableName == "" ||
			object.SQL == "" {
			return "", fmt.Errorf("currentstore: schema object is incomplete")
		}
		object.SQL = strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(object.SQL, "\r\n", "\n"),
			"\r",
			"\n",
		))
		normalized[index] = object
	}
	sort.Slice(normalized, func(left, right int) bool {
		if comparison := bytes.Compare(
			[]byte(normalized[left].Type),
			[]byte(normalized[right].Type),
		); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(
			[]byte(normalized[left].Name),
			[]byte(normalized[right].Name),
		) < 0
	})

	digest := sha256.New()
	_, _ = digest.Write([]byte(schemaFingerprintDomain))
	for _, object := range normalized {
		for _, value := range []string{
			object.Type,
			object.Name,
			object.TableName,
			object.SQL,
		} {
			_, _ = digest.Write([]byte(value))
			_, _ = digest.Write([]byte{0})
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// DatabaseSchemaFingerprint computes the runtime fingerprint from one
// database without changing it.
func DatabaseSchemaFingerprint(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
) (string, error) {
	objects, err := LoadSchemaObjects(ctx, queryer)
	if err != nil {
		return "", err
	}
	return ComputeSchemaFingerprint(objects)
}
