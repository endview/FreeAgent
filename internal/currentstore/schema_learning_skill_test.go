package currentstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

const (
	// These values retain earlier schema identities as historical review
	// markers. Current Store initialization is frozen by the latest constants
	// below; it never accepts a historical projection.
	w4L1BSchemaFingerprint = "18722b0c8a18e9f6563e65cf30d0ebc50b723aa6f6465aa4b95d83ba001bc422"
	w4L1BMigrationSHA256   = "75b7fe488932e0f562c84073a659c9803ffcd4bfdb4efa500b19720c7648c47f"
	w4L1BMigrationBytes    = 34592

	w4L2SchemaFingerprint = "feed0dd35f98e4f177934092d93a1b3d7f834f0fae3b076e722aa6bcd4722484"
	w4L2MigrationSHA256   = "715c2904f417ecf8732f554eda5c47af1a6c45eecbf8e21f408f51badb70519c"
	w4L2MigrationBytes    = 35743

	w4L3SchemaFingerprint = "a744ea62d0ead80b5eb0669409f0ab4cd0d297b662e67181b0a88f760b2c5024"
	w4L3MigrationSHA256   = "025acc044f53a71c674d5963c171806e87095911025a129f65b4be33a0601070"
	w4L3MigrationBytes    = 37810

	w4L4BSchemaFingerprint = "9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518"
	w4L4BMigrationSHA256   = "4cf260d368fb7b69e0a86dad99ff593bea216e9d0da341f607d000999e076be4"
	w4L4BMigrationBytes    = 44138

	w5X1SchemaFingerprint = "98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588"
	w5X1MigrationSHA256   = "0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a"
	w5X1MigrationBytes    = 44215

	w2R1SchemaFingerprint = "dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5"
	w2R1MigrationSHA256   = "fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665"
	w2R1MigrationBytes    = 44237

	w2R2SchemaFingerprint = "7d2e0850a0253a630d5e2264c720b10fbac3ad0fdb6f9e53fcd722c2dd2615a8"
	w2R2MigrationSHA256   = "2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f"
	w2R2MigrationBytes    = 44257

	w2U2SchemaFingerprint = "6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1"
	w2U2MigrationSHA256   = "d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e"
	w2U2MigrationBytes    = 49972

	w2U3SchemaFingerprint = "37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd"
	w2U3MigrationSHA256   = "e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d"
	w2U3MigrationBytes    = 57652

	w6OverviewSchemaFingerprint = "87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1"
	w6OverviewMigrationSHA256   = "5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22"
	w6OverviewMigrationBytes    = 143588

	w6ModuleArtifactIngressSchemaFingerprint = "47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d"
	w6ModuleArtifactIngressMigrationSHA256   = "6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86"
	w6ModuleArtifactIngressMigrationBytes    = 150301
)

func TestW6ModuleArtifactIngressSchemaIdentityIsFrozen(t *testing.T) {
	migration, err := Migration0001()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(migration)
	if len(migration) != w6ModuleArtifactIngressMigrationBytes {
		t.Fatalf("migration bytes = %d, want %d", len(migration), w6ModuleArtifactIngressMigrationBytes)
	}
	if got := fmt.Sprintf("%x", digest); got != w6ModuleArtifactIngressMigrationSHA256 {
		t.Fatalf("migration SHA-256 = %s, want %s", got, w6ModuleArtifactIngressMigrationSHA256)
	}
	if ExpectedSchemaFingerprint != w6ModuleArtifactIngressSchemaFingerprint {
		t.Fatalf(
			"ExpectedSchemaFingerprint = %s, want %s",
			ExpectedSchemaFingerprint,
			w6ModuleArtifactIngressSchemaFingerprint,
		)
	}
	if len(migration) == w6OverviewMigrationBytes ||
		fmt.Sprintf("%x", digest) == w6OverviewMigrationSHA256 ||
		w6ModuleArtifactIngressSchemaFingerprint == w6OverviewSchemaFingerprint {
		t.Fatal("W6 module Artifact ingress schema identity did not advance from W6 Overview")
	}
	if w2U3MigrationBytes == w2U2MigrationBytes ||
		w2U3MigrationSHA256 == w2U2MigrationSHA256 ||
		w2U3SchemaFingerprint == w2U2SchemaFingerprint {
		t.Fatal("W2-U3 schema identity did not advance from W2-U2")
	}
	if w2R2SchemaFingerprint == w2U2SchemaFingerprint ||
		w2R2MigrationSHA256 == w2U2MigrationSHA256 ||
		w2R2MigrationBytes == w2U2MigrationBytes {
		t.Fatal("W2-U2 schema identity did not advance from W2-R2")
	}
	if w2R1SchemaFingerprint == w2R2SchemaFingerprint ||
		w2R1MigrationSHA256 == w2R2MigrationSHA256 ||
		w2R1MigrationBytes == w2R2MigrationBytes {
		t.Fatal("W2-R2 schema identity did not advance from W2-R1")
	}
	if w4L4BSchemaFingerprint == w5X1SchemaFingerprint ||
		w4L4BMigrationSHA256 == w5X1MigrationSHA256 ||
		w4L4BMigrationBytes == w5X1MigrationBytes {
		t.Fatal("W5-X1 schema identity did not advance from W4-L4B")
	}

	db := createSchemaTestDatabase(t)
	var tableCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
	`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 43 {
		t.Fatalf("ordinary table count = %d, want 43", tableCount)
	}
	fingerprint, err := DatabaseSchemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != w6ModuleArtifactIngressSchemaFingerprint {
		t.Fatalf(
			"database schema fingerprint = %s, want %s",
			fingerprint,
			w6ModuleArtifactIngressSchemaFingerprint,
		)
	}
}

func TestW4L1BLearningProposalKindAllowsOnlyKnowledgeAndSkill(t *testing.T) {
	db := createSchemaTestDatabase(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}

	sharedSource := w4L1BSchemaDigest(1001)
	sharedContent := w4L1BSchemaDigest(1002)
	for index, kind := range []string{"KNOWLEDGE", "SKILL"} {
		if err := insertW4L1BLearningProposalSchemaRow(
			db,
			index+1,
			kind,
			"tenant-a",
			sharedSource,
			sharedContent,
			"shared-target",
			"v1",
		); err != nil {
			t.Fatalf("insert allowed proposal_kind %q: %v", kind, err)
		}
	}

	for index, kind := range []string{"TOOL", "knowledge", "SKILL ", ""} {
		row := index + 10
		err := insertW4L1BLearningProposalSchemaRow(
			db,
			row,
			kind,
			"tenant-a",
			w4L1BSchemaDigest(2000+row*10+1),
			w4L1BSchemaDigest(2000+row*10+2),
			fmt.Sprintf("unknown-target-%d", row),
			"v1",
		)
		if err == nil {
			t.Fatalf("migration accepted unknown proposal_kind %q", kind)
		}
	}
}

func TestW4L2LearningProposalUniqueAxesIncludeReviewerIdentity(t *testing.T) {
	db := createSchemaTestDatabase(t)
	rows, err := db.Query(`PRAGMA index_list('learning_proposals')`)
	if err != nil {
		t.Fatal(err)
	}
	var uniqueConstraintIndexes []string
	for rows.Next() {
		var (
			sequence int
			name     string
			unique   int
			origin   string
			partial  int
		)
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if unique == 1 && origin == "u" {
			uniqueConstraintIndexes = append(uniqueConstraintIndexes, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	got := make(map[string]bool, len(uniqueConstraintIndexes))
	for _, indexName := range uniqueConstraintIndexes {
		indexRows, err := db.Query(fmt.Sprintf(`PRAGMA index_info(%q)`, indexName))
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for indexRows.Next() {
			var sequence, columnID int
			var columnName string
			if err := indexRows.Scan(&sequence, &columnID, &columnName); err != nil {
				indexRows.Close()
				t.Fatal(err)
			}
			columns = append(columns, columnName)
		}
		if err := indexRows.Err(); err != nil {
			indexRows.Close()
			t.Fatal(err)
		}
		if err := indexRows.Close(); err != nil {
			t.Fatal(err)
		}
		got[strings.Join(columns, ",")] = true
	}

	want := map[string]bool{
		"tenant_id,proposal_kind,source_fingerprint":       true,
		"tenant_id,proposal_kind,content_fingerprint":      true,
		"tenant_id,proposal_kind,target_id,target_version": true,
		"review_run_id":       true,
		"reviewer_attempt_id": true,
		"version_id":          true,
	}
	if len(got) != len(want) {
		t.Fatalf("learning_proposals UNIQUE axes = %v, want %v", got, want)
	}
	for columns := range want {
		if !got[columns] {
			t.Fatalf("learning_proposals UNIQUE axes = %v, missing %s", got, columns)
		}
	}
}

func TestW4L2LearningReviewStateRevisionMatrixIsClosed(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		revision   int
		reviewRun  any
		attempt    any
		wantAccept bool
	}{
		{name: "SUBMITTED exact", state: "SUBMITTED", revision: 0, wantAccept: true},
		{name: "REVIEW_PENDING exact", state: "REVIEW_PENDING", revision: 1, reviewRun: "review-run", wantAccept: true},
		{name: "APPROVED first terminal", state: "APPROVED", revision: 2, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "REJECTED first terminal", state: "REJECTED", revision: 2, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "REVIEW_FAILED first terminal", state: "REVIEW_FAILED", revision: 2, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "REVIEW_UNKNOWN exact", state: "REVIEW_UNKNOWN", revision: 2, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "APPROVED reconciled", state: "APPROVED", revision: 3, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "REJECTED reconciled", state: "REJECTED", revision: 3, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "REVIEW_FAILED reconciled", state: "REVIEW_FAILED", revision: 3, reviewRun: "review-run", attempt: "review-attempt", wantAccept: true},
		{name: "SUBMITTED with Run", state: "SUBMITTED", revision: 0, reviewRun: "review-run"},
		{name: "SUBMITTED changed time", state: "SUBMITTED", revision: 0},
		{name: "PENDING wrong revision", state: "REVIEW_PENDING", revision: 0, reviewRun: "review-run"},
		{name: "PENDING with Attempt", state: "REVIEW_PENDING", revision: 1, reviewRun: "review-run", attempt: "review-attempt"},
		{name: "UNKNOWN revision three", state: "REVIEW_UNKNOWN", revision: 3, reviewRun: "review-run", attempt: "review-attempt"},
		{name: "terminal without Attempt", state: "APPROVED", revision: 2, reviewRun: "review-run"},
		{name: "terminal revision one", state: "REJECTED", revision: 1, reviewRun: "review-run", attempt: "review-attempt"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := createSchemaTestDatabase(t)
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			row := 100 + index
			updatedAt := int64(row + 2)
			if test.name == "SUBMITTED exact" {
				updatedAt = int64(row + 1)
			}
			err := insertW4L2LearningReviewMatrixSchemaRow(
				db,
				row,
				test.state,
				test.revision,
				test.reviewRun,
				test.attempt,
				int64(row+1),
				updatedAt,
			)
			if test.wantAccept && err != nil {
				t.Fatalf("valid state/revision rejected: %v", err)
			}
			if !test.wantAccept && err == nil {
				t.Fatal("invalid state/revision matrix was accepted")
			}
		})
	}
}

func insertW4L1BLearningProposalSchemaRow(
	db *sql.DB,
	row int,
	kind string,
	tenantID string,
	sourceFingerprint string,
	contentFingerprint string,
	targetID string,
	targetVersion string,
) error {
	return insertW4L2LearningProposalSchemaRow(
		db,
		row,
		kind,
		tenantID,
		sourceFingerprint,
		contentFingerprint,
		targetID,
		targetVersion,
		"SUBMITTED",
		0,
		nil,
		nil,
		int64(row+1),
		int64(row+1),
	)
}

func insertW4L2LearningReviewMatrixSchemaRow(
	db *sql.DB,
	row int,
	state string,
	revision int,
	reviewRun any,
	attempt any,
	createdAt int64,
	updatedAt int64,
) error {
	return insertW4L2LearningProposalSchemaRow(
		db,
		row,
		"KNOWLEDGE",
		"tenant-review-matrix",
		w4L1BSchemaDigest(8000+row*10+1),
		w4L1BSchemaDigest(8000+row*10+2),
		fmt.Sprintf("review-matrix-target-%d", row),
		"v1",
		state,
		revision,
		reviewRun,
		attempt,
		createdAt,
		updatedAt,
	)
}

func insertW4L2LearningProposalSchemaRow(
	db *sql.DB,
	row int,
	kind string,
	tenantID string,
	sourceFingerprint string,
	contentFingerprint string,
	targetID string,
	targetVersion string,
	state string,
	revision int,
	reviewRun any,
	reviewerAttempt any,
	createdAt int64,
	updatedAt int64,
) error {
	proposalCanonical := []byte(`{"schema_version":"learning-proposal/v1"}`)
	draftCanonical := []byte(`{"schema_version":"static-context/v1","text":"draft"}`)
	_, err := db.Exec(`
		INSERT INTO learning_proposals(
			proposal_id,
			tenant_id,
			workspace_id,
			proposal_kind,
			source_fingerprint,
			content_fingerprint,
			draft_digest,
			target_id,
			target_version,
			proposal_canonical,
			proposal_size_bytes,
			draft_canonical,
			draft_size_bytes,
			proposer_run_id,
			proposer_manifest_digest,
			proposer_member_id,
			proposer_member_digest,
			proposer_attempt_id,
			proposer_result_ref,
			review_run_id,
			reviewer_attempt_id,
			state,
			revision,
			created_at,
			updated_at
		) VALUES(?, ?, 'workspace-schema', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		w4L1BSchemaDigest(row),
		tenantID,
		kind,
		sourceFingerprint,
		contentFingerprint,
		w4L1BSchemaDigest(3000+row*10+1),
		targetID,
		targetVersion,
		proposalCanonical,
		len(proposalCanonical),
		draftCanonical,
		len(draftCanonical),
		fmt.Sprintf("run-%d", row),
		w4L1BSchemaDigest(3000+row*10+2),
		fmt.Sprintf("member-%d", row),
		w4L1BSchemaDigest(3000+row*10+3),
		fmt.Sprintf("attempt-%d", row),
		w4L1BSchemaDigest(3000+row*10+4),
		reviewRun,
		reviewerAttempt,
		state,
		revision,
		createdAt,
		updatedAt,
	)
	return err
}

func w4L1BSchemaDigest(value int) string {
	return fmt.Sprintf("%064x", value)
}
