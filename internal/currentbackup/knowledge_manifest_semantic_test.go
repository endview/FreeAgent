package currentbackup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestKnowledgePartialManifestDeclarationsFailClosedForCurrentBackup(
	t *testing.T,
) {
	fixture := newRAGBackupFixture(t)
	tests := []struct {
		name      string
		configure func(*moduleapi.ModuleManifestV1)
	}{
		{
			name: "permission only",
			configure: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = nil
				manifest.RequestedPermissions = []moduleapi.Permission{
					moduleapi.PermissionKnowledgeReadV1,
				}
			},
		},
		{
			name: "require only",
			configure: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = []moduleapi.PortRef{
					moduleapi.ExactModelGeneratePortV1(),
				}
				manifest.RequestedPermissions = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "partial-knowledge.sqlite")
			copyTestFile(t, fixture.databasePath, copyPath)
			database, err := sql.Open("sqlite", sqliteFileURI(copyPath, "rw"))
			if err != nil {
				t.Fatal(err)
			}
			tamperKnowledgeBackupManifestShape(
				t,
				database,
				fixture.knowledgeAssertion.ArtifactDirectory,
				fixture.knowledgeAssertion.ModuleID,
				fixture.knowledgeAssertion.ExactVersion,
				fixture.knowledgeAssertion.ArtifactDigest,
				test.configure,
			)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			before := knowledgePublicationRowCounts(t, copyPath)
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				copyPath,
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), "must be exactly legacy permissionless or governed") {
				t.Fatalf("VerifyCurrentStoreSemanticClosure(partial Knowledge) error=%v", err)
			}

			bundle := filepath.Join(t.TempDir(), "rejected.bundle")
			if _, err := CreateBundle(
				context.Background(),
				copyPath,
				fixture.artifactRoot,
				bundle,
				"currentbackup-knowledge-e5a-test/v1",
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), "must be exactly legacy permissionless or governed") {
				t.Fatalf("CreateBundle(partial Knowledge) error=%v", err)
			}
			if _, err := os.Stat(bundle); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed Knowledge backup published destination: %v", err)
			}
			if after := knowledgePublicationRowCounts(t, copyPath); after != before {
				t.Fatalf(
					"failed Knowledge verification wrote rows: before=%v after=%v",
					before,
					after,
				)
			}
		})
	}
}

func tamperKnowledgeBackupManifestShape(
	t *testing.T,
	database *sql.DB,
	artifactDirectory string,
	moduleID string,
	version string,
	artifactDigest string,
	configure func(*moduleapi.ModuleManifestV1),
) {
	t.Helper()
	original, err := os.ReadFile(filepath.Join(artifactDirectory, "module.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(original)
	if err != nil {
		t.Fatal(err)
	}
	configure(&manifest)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatalf("partial Knowledge Manifest is not structurally valid: %v", err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(currentstore.ContentModuleManifest),
		"application/json",
		canonical,
		len(canonical),
		time.Now().UTC().UnixMicro(),
	); err != nil {
		t.Fatalf("insert partial Knowledge Manifest: %v", err)
	}
	result, err := database.Exec(`
		UPDATE module_installations SET manifest_ref=?
		WHERE module_id=? AND exact_version=? AND artifact_digest=?
	`, digest, moduleID, version, artifactDigest)
	if err != nil {
		t.Fatalf("replace Knowledge Manifest: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Knowledge Manifest affected=%d error=%v", affected, err)
	}
}

func knowledgePublicationRowCounts(t *testing.T, databasePath string) [5]int {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var counts [5]int
	for index, table := range []string{
		"control_snapshots",
		"runtime_catalog_generations",
		"control_current",
		"module_installations",
		"content_records",
	} {
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(
			&counts[index],
		); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}
