package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const longChainUniqueRequests = 64

func TestProductionLongChainSurvivesRetryRestartBackupAndRestore(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "source.sqlite")
	artifactRoot := filepath.Join(root, "source-artifacts")

	if err := run(context.Background(), []string{
		"init",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--seed", exampleSeedPath(t),
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("initialize production long chain: %v", err)
	}

	results := make([]chatCommandResult, longChainUniqueRequests)
	for index := 0; index < longChainUniqueRequests/2; index++ {
		results[index] = runLongChainChat(t, databasePath, artifactRoot, index)
		if index%8 == 0 {
			assertLongChainRetry(
				t,
				results[index],
				runLongChainChat(t, databasePath, artifactRoot, index),
			)
		}
	}

	bundle := filepath.Join(root, "midpoint-backup")
	var backupOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"backup",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--out", bundle,
	}, &backupOutput, io.Discard); err != nil {
		t.Fatalf("create midpoint backup: %v", err)
	}
	var backedUp backupCommandResult
	if err := json.Unmarshal(backupOutput.Bytes(), &backedUp); err != nil {
		t.Fatalf("decode midpoint backup: %v\n%s", err, backupOutput.String())
	}

	var verifyOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"backup-verify",
		"--bundle", bundle,
	}, &verifyOutput, io.Discard); err != nil {
		t.Fatalf("verify midpoint backup: %v", err)
	}
	var verified backupCommandResult
	if err := json.Unmarshal(verifyOutput.Bytes(), &verified); err != nil {
		t.Fatalf("decode verified midpoint backup: %v\n%s", err, verifyOutput.String())
	}
	if backedUp.Manifest.ManifestDigest == "" ||
		verified.Manifest.ManifestDigest != backedUp.Manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest=%q, want %q",
			verified.Manifest.ManifestDigest,
			backedUp.Manifest.ManifestDigest,
		)
	}

	restoredDatabase := filepath.Join(root, "restored.sqlite")
	restoredArtifacts := filepath.Join(root, "restored-artifacts")
	var restoreOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"restore",
		"--bundle", bundle,
		"--db", restoredDatabase,
		"--artifact-root", restoredArtifacts,
	}, &restoreOutput, io.Discard); err != nil {
		t.Fatalf("restore midpoint backup: %v", err)
	}
	var restored restoreCommandResult
	if err := json.Unmarshal(restoreOutput.Bytes(), &restored); err != nil {
		t.Fatalf("decode midpoint restore: %v\n%s", err, restoreOutput.String())
	}
	if restored.StoreInstanceID != backedUp.Manifest.StoreIdentity.StoreInstanceID {
		t.Fatalf(
			"restored Store instance=%q, want %q",
			restored.StoreInstanceID,
			backedUp.Manifest.StoreIdentity.StoreInstanceID,
		)
	}

	databasePath = restoredDatabase
	artifactRoot = restoredArtifacts
	assertLongChainRetry(
		t,
		results[0],
		runLongChainChat(t, databasePath, artifactRoot, 0),
	)

	for index := longChainUniqueRequests / 2; index < longChainUniqueRequests; index++ {
		results[index] = runLongChainChat(t, databasePath, artifactRoot, index)
		if index%8 == 0 {
			assertLongChainRetry(
				t,
				results[index],
				runLongChainChat(t, databasePath, artifactRoot, index),
			)
		}
	}

	for _, index := range []int{0, 31, 32, longChainUniqueRequests - 1} {
		assertLongChainRetry(
			t,
			results[index],
			runLongChainChat(t, databasePath, artifactRoot, index),
		)
	}
	assertLongChainStoreCardinality(t, databasePath)
}

func runLongChainChat(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	index int,
) chatCommandResult {
	t.Helper()
	requestID := fmt.Sprintf("long-chain-request-%03d", index)
	message := fmt.Sprintf("long-chain-message-%03d", index)
	deadline := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(index) * time.Second)

	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"chat",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--message", message,
		"--request-id", requestID,
		"--deadline", deadline.Format(time.RFC3339Nano),
	}, &output, io.Discard); err != nil {
		t.Fatalf("chat %d: %v", index, err)
	}
	var result chatCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode chat %d: %v\n%s", index, err, output.String())
	}
	if result.RequestID != requestID ||
		result.Reply != message ||
		result.RunID == "" ||
		result.Disposition != "TERMINATED" ||
		result.Reason != "MODEL_SUCCEEDED" ||
		result.Failure != "" {
		t.Fatalf("chat %d returned unexpected result: %#v", index, result)
	}
	return result
}

func assertLongChainRetry(
	t *testing.T,
	want chatCommandResult,
	got chatCommandResult,
) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact retry changed result:\nwant=%#v\ngot=%#v", want, got)
	}
}

func assertLongChainStoreCardinality(t *testing.T, databasePath string) {
	t.Helper()
	database := openLongChainReadOnly(t, databasePath)
	defer database.Close()

	var (
		runs               int
		distinctAdmissions int
		manifests          int
		members            int
		frames             int
		attempts           int
		distinctAttempts   int
		distinctOperations int
		usage              int
		history            int
		nonTerminalRuns    int
		nonSucceeded       int
	)
	if err := database.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM runs),
			(SELECT COUNT(DISTINCT admission_key) FROM runs),
			(SELECT COUNT(*) FROM run_manifests),
			(SELECT COUNT(*) FROM member_execution_snapshots),
			(SELECT COUNT(*) FROM loop_frames),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(DISTINCT attempt_id) FROM model_dispatch_attempts),
			(SELECT COUNT(DISTINCT logical_operation_key) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COUNT(*) FROM history_entries),
			(SELECT COUNT(*) FROM runs WHERE disposition != 'TERMINATED'),
			(SELECT COUNT(*) FROM model_dispatch_attempts WHERE state != 'SUCCEEDED')
	`).Scan(
		&runs,
		&distinctAdmissions,
		&manifests,
		&members,
		&frames,
		&attempts,
		&distinctAttempts,
		&distinctOperations,
		&usage,
		&history,
		&nonTerminalRuns,
		&nonSucceeded,
	); err != nil {
		t.Fatalf("read long-chain cardinality: %v", err)
	}

	for name, actual := range map[string]int{
		"runs":                        runs,
		"distinct admissions":         distinctAdmissions,
		"run manifests":               manifests,
		"member snapshots":            members,
		"loop frames":                 frames,
		"model attempts":              attempts,
		"distinct model attempts":     distinctAttempts,
		"distinct logical operations": distinctOperations,
		"usage rows":                  usage,
		"history entries":             history,
	} {
		if actual != longChainUniqueRequests {
			t.Errorf("%s=%d, want %d", name, actual, longChainUniqueRequests)
		}
	}
	if nonTerminalRuns != 0 || nonSucceeded != 0 {
		t.Errorf(
			"non-terminal runs=%d non-succeeded attempts=%d, want 0/0",
			nonTerminalRuns,
			nonSucceeded,
		)
	}
}

func openLongChainReadOnly(t *testing.T, path string) *sql.DB {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve long-chain Store: %v", err)
	}
	normalized := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && filepath.VolumeName(absolute) != "" &&
		!strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	uri := url.URL{Scheme: "file", Path: normalized}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "trusted_schema(0)")
	uri.RawQuery = query.Encode()

	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatalf("open long-chain Store read-only: %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("ping long-chain Store read-only: %v", err)
	}
	return database
}
