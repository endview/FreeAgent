// Package currentstore owns the single FAC2 SQLite schema used by the current
// Core runtime. It intentionally has no dependency on the frozen FAC1 Store.
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
	SchemaIdentity = "github.com/endview/freeagent/current-store-v2"
	ApplicationID  = 1178682162 // 0x46414332, ASCII FAC2.
	UserVersion    = 2
	GeneratorID    = "freeagent-current-store-v2"

	schemaFingerprintDomain = "freeagent.current-store.schema-fingerprint.v2\n"
)

// ExpectedSchemaFingerprint freezes the exact sqlite_schema projection
// produced by 0001_current.sql.
const ExpectedSchemaFingerprint = "d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e"

// Migration0001SHA256 freezes the bootstrap migration bytes. Existing
// migrations are immutable once released; later schema changes must add a new
// numbered migration.
const Migration0001SHA256 = "dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a"

const Migration0002SHA256 = "3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb"

// SchemaVersion identifies one schema shape accepted by this binary.
type SchemaVersion struct {
	UserVersion       int
	SchemaFingerprint string
}

var knownSchemaVersions = map[int]SchemaVersion{
	1: {
		UserVersion:       1,
		SchemaFingerprint: "9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561",
	},
	2: {
		UserVersion:       2,
		SchemaFingerprint: ExpectedSchemaFingerprint,
	},
}

// KnownSchemaVersion returns the immutable identity registered for one
// released FAC2 schema version. Unknown and future versions fail closed.
func KnownSchemaVersion(userVersion int) (SchemaVersion, bool) {
	version, ok := knownSchemaVersions[userVersion]
	return version, ok
}

//go:embed migrations/fac2/*.sql
var migrationFS embed.FS

// Migration0001 returns an owned copy of the only Current Store migration.
func Migration0001() ([]byte, error) {
	content, err := migrationFS.ReadFile("migrations/fac2/0001_current.sql")
	if err != nil {
		return nil, fmt.Errorf("currentstore: read 0001 migration: %w", err)
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != Migration0001SHA256 {
		return nil, fmt.Errorf("currentstore: 0001 migration bytes changed")
	}
	return bytes.Clone(content), nil
}

// Migration0002 returns the immutable server-owned Upgrade Review migration.
func Migration0002() ([]byte, error) {
	content, err := migrationFS.ReadFile("migrations/fac2/0002_server_owned_review.sql")
	if err != nil {
		return nil, fmt.Errorf("currentstore: read 0002 migration: %w", err)
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != Migration0002SHA256 {
		return nil, fmt.Errorf("currentstore: 0002 migration bytes changed")
	}
	return bytes.Clone(content), nil
}

// SchemaObject is the sqlite_schema projection covered by the FAC2 schema
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
