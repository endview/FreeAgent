package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/runscheduler"
	"github.com/endview/freeagent/sdk/loopapi"
)

func TestRunInitAndChatCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	var initOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"init",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--seed", exampleSeedPath(t),
	}, &initOutput, io.Discard); err != nil {
		t.Fatalf("run init: %v", err)
	}
	var initialized initResult
	if err := json.Unmarshal(initOutput.Bytes(), &initialized); err != nil {
		t.Fatalf("decode init output: %v\n%s", err, initOutput.String())
	}
	if initialized.DatabasePath != databasePath || len(initialized.ArtifactLocks) != 2 {
		t.Fatalf("unexpected init result: %#v", initialized)
	}
	if !bytes.Contains(initOutput.Bytes(), []byte(`"tenant_id":"default"`)) ||
		bytes.Contains(initOutput.Bytes(), []byte(`"TenantID"`)) {
		t.Fatalf("init defaults are not stable snake_case JSON: %s", initOutput.String())
	}

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	args := []string{
		"chat",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--message", "CLI smoke",
		"--request-id", "cli-smoke-1",
		"--deadline", deadline.Format(time.RFC3339Nano),
	}
	var firstOutput bytes.Buffer
	if err := run(context.Background(), args, &firstOutput, io.Discard); err != nil {
		t.Fatalf("run first chat: %v", err)
	}
	var first chatCommandResult
	if err := json.Unmarshal(firstOutput.Bytes(), &first); err != nil {
		t.Fatalf("decode first chat: %v\n%s", err, firstOutput.String())
	}
	if first.Reply != "CLI smoke" || first.Disposition != "TERMINATED" || first.RunID == "" {
		t.Fatalf("unexpected first chat: %#v", first)
	}
	if first.Usage == nil || first.Usage.InputTokens != nil ||
		first.Usage.CachedInputTokens != nil ||
		first.Usage.UncachedInputTokens != nil ||
		first.Usage.OutputTokens != nil ||
		first.Usage.ReasoningTokens != nil ||
		first.Usage.Status != "PROVIDER_REPORTED" {
		t.Fatalf("CLI authoritative UNKNOWN Usage=%+v", first.Usage)
	}
	for _, field := range []string{
		`"input_tokens":null`,
		`"cached_input_tokens":null`,
		`"uncached_input_tokens":null`,
		`"output_tokens":null`,
		`"reasoning_tokens":null`,
	} {
		if !bytes.Contains(firstOutput.Bytes(), []byte(field)) {
			t.Fatalf(
				"CLI Usage lost UNKNOWN field %s: %s",
				field,
				firstOutput.String(),
			)
		}
	}

	var retryOutput bytes.Buffer
	if err := run(context.Background(), args, &retryOutput, io.Discard); err != nil {
		t.Fatalf("run retry chat: %v", err)
	}
	var retry chatCommandResult
	if err := json.Unmarshal(retryOutput.Bytes(), &retry); err != nil {
		t.Fatalf("decode retry chat: %v\n%s", err, retryOutput.String())
	}
	if !reflect.DeepEqual(retry, first) {
		t.Fatalf("CLI retry changed result:\nfirst=%#v\nretry=%#v", first, retry)
	}
}

func TestRunConversationCommandsAndTwoTurnChat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}

	const conversationID = "cli-conversation-1"
	var createOutput bytes.Buffer
	if err := run(ctx, []string{
		"conversation-create",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &createOutput, io.Discard); err != nil {
		t.Fatalf("conversation-create: %v", err)
	}
	var created conversationCommandResult
	if err := json.Unmarshal(createOutput.Bytes(), &created); err != nil {
		t.Fatalf("decode conversation-create: %v\n%s", err, createOutput.String())
	}
	if !created.Created || created.ConversationID != conversationID ||
		created.Revision != 0 || created.HeadRunID != "" {
		t.Fatalf("created Conversation=%+v", created)
	}

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	var firstOutput bytes.Buffer
	if err := run(ctx, []string{
		"chat",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--conversation", conversationID,
		"--conversation-revision", "0",
		"--message", "CLI turn one",
		"--request-id", "cli-conversation-request-1",
		"--deadline", deadline.Format(time.RFC3339Nano),
	}, &firstOutput, io.Discard); err != nil {
		t.Fatalf("first Conversation chat: %v", err)
	}
	var first chatCommandResult
	if err := json.Unmarshal(firstOutput.Bytes(), &first); err != nil {
		t.Fatalf("decode first Conversation chat: %v", err)
	}
	if first.Reply != "CLI turn one" || first.ConversationID != conversationID ||
		first.ConversationRevision != 1 || first.RunID == "" {
		t.Fatalf("first Conversation chat=%+v", first)
	}

	var secondOutput bytes.Buffer
	if err := run(ctx, []string{
		"chat",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--conversation", conversationID,
		"--conversation-revision", "1",
		"--conversation-head-run", first.RunID,
		"--message", "CLI turn two",
		"--request-id", "cli-conversation-request-2",
		"--deadline", deadline.Add(time.Minute).Format(time.RFC3339Nano),
	}, &secondOutput, io.Discard); err != nil {
		t.Fatalf("second Conversation chat: %v", err)
	}
	var second chatCommandResult
	if err := json.Unmarshal(secondOutput.Bytes(), &second); err != nil {
		t.Fatalf("decode second Conversation chat: %v", err)
	}
	if second.Reply != "CLI turn two" || second.ConversationRevision != 2 ||
		second.RunID == "" || second.RunID == first.RunID {
		t.Fatalf("second Conversation chat=%+v", second)
	}

	var getOutput bytes.Buffer
	if err := run(ctx, []string{
		"conversation-get",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &getOutput, io.Discard); err != nil {
		t.Fatalf("conversation-get: %v", err)
	}
	var got conversationCommandResult
	if err := json.Unmarshal(getOutput.Bytes(), &got); err != nil {
		t.Fatalf("decode conversation-get: %v", err)
	}
	if got.Created || got.Revision != 2 || got.HeadRunID != second.RunID {
		t.Fatalf("queried Conversation=%+v", got)
	}
}

func TestRunConversationFiftyTurnsAcrossReentryBackupRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "source.sqlite")
	artifactRoot := filepath.Join(root, "source-artifacts")
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}

	runJSON := func(args []string, target any) {
		t.Helper()
		var output bytes.Buffer
		if err := run(ctx, args, &output, io.Discard); err != nil {
			t.Fatalf("run %q: %v", strings.Join(args[:1], " "), err)
		}
		if err := json.Unmarshal(output.Bytes(), target); err != nil {
			t.Fatalf("decode %q: %v\n%s", strings.Join(args[:1], " "), err, output.String())
		}
	}

	const conversationID = "cli-conversation-fifty-turns"
	var created conversationCommandResult
	runJSON([]string{
		"conversation-create",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &created)
	if !created.Created || created.Revision != 0 || created.HeadRunID != "" {
		t.Fatalf("Conversation genesis=%+v", created)
	}

	baseDeadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	seenRuns := make(map[string]struct{}, 50)
	var revision uint64
	var headRunID string
	buildChatArgs := func(
		turn int,
		expectedRevision uint64,
		expectedHeadRunID string,
		deadline time.Time,
	) []string {
		args := []string{
			"chat",
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--conversation", conversationID,
			"--conversation-revision", fmt.Sprintf("%d", expectedRevision),
			"--message", fmt.Sprintf("deterministic Conversation turn %02d", turn),
			"--request-id", fmt.Sprintf("cli-conversation-fifty-%02d", turn),
			"--deadline", deadline.Format(time.RFC3339Nano),
		}
		if expectedHeadRunID != "" {
			args = append(args, "--conversation-head-run", expectedHeadRunID)
		}
		return args
	}

	for turn := 1; turn <= 50; turn++ {
		expectedRevision := revision
		expectedHeadRunID := headRunID
		deadline := baseDeadline.Add(time.Duration(turn) * time.Minute)
		args := buildChatArgs(turn, expectedRevision, expectedHeadRunID, deadline)
		var result chatCommandResult
		runJSON(args, &result)
		wantReply := fmt.Sprintf("deterministic Conversation turn %02d", turn)
		if result.Disposition != "TERMINATED" ||
			result.Reason != "MODEL_SUCCEEDED" || result.Reply != wantReply ||
			result.ConversationID != conversationID ||
			result.ConversationRevision != uint64(turn) || result.RunID == "" {
			t.Fatalf("turn %d result=%+v", turn, result)
		}
		if _, duplicate := seenRuns[result.RunID]; duplicate {
			t.Fatalf("turn %d reused Run %q", turn, result.RunID)
		}
		seenRuns[result.RunID] = struct{}{}
		revision = result.ConversationRevision
		headRunID = result.RunID

		if turn == 25 {
			bundle := filepath.Join(root, "turn-25-bundle")
			var backedUp backupCommandResult
			runJSON([]string{
				"backup",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--out", bundle,
			}, &backedUp)
			var verified backupCommandResult
			runJSON([]string{"backup-verify", "--bundle", bundle}, &verified)
			if verified.Manifest.ManifestDigest == "" ||
				verified.Manifest.ManifestDigest != backedUp.Manifest.ManifestDigest {
				t.Fatalf("turn 25 bundle verification=%+v backup=%+v", verified, backedUp)
			}

			restoredDatabase := filepath.Join(root, "restored.sqlite")
			restoredArtifacts := filepath.Join(root, "restored-artifacts")
			var restored restoreCommandResult
			runJSON([]string{
				"restore",
				"--bundle", bundle,
				"--db", restoredDatabase,
				"--artifact-root", restoredArtifacts,
			}, &restored)
			if restored.StoreInstanceID != backedUp.Manifest.StoreIdentity.StoreInstanceID {
				t.Fatalf("restore changed Store identity: restore=%+v backup=%+v", restored, backedUp)
			}
			databasePath = restoredDatabase
			artifactRoot = restoredArtifacts

			var restoredHead conversationCommandResult
			runJSON([]string{
				"conversation-get",
				"--db", databasePath,
				"--conversation", conversationID,
			}, &restoredHead)
			if restoredHead.Revision != revision || restoredHead.HeadRunID != headRunID {
				t.Fatalf("restored Conversation head=%+v want revision=%d head=%s", restoredHead, revision, headRunID)
			}

			var retry chatCommandResult
			runJSON(
				buildChatArgs(turn, expectedRevision, expectedHeadRunID, deadline),
				&retry,
			)
			if retry.RunID != result.RunID ||
				retry.ConversationRevision != revision || retry.Reply != result.Reply {
				t.Fatalf("restored exact retry=%+v original=%+v", retry, result)
			}
		}

		if turn == 50 {
			var retry chatCommandResult
			runJSON(args, &retry)
			if retry.RunID != result.RunID ||
				retry.ConversationRevision != revision || retry.Reply != result.Reply {
				t.Fatalf("final exact retry=%+v original=%+v", retry, result)
			}
		}
	}

	var finalHead conversationCommandResult
	runJSON([]string{
		"conversation-get",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &finalHead)
	if finalHead.Revision != 50 || finalHead.HeadRunID != headRunID || len(seenRuns) != 50 {
		t.Fatalf("final Conversation=%+v unique_runs=%d", finalHead, len(seenRuns))
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open final Store for read-only counts: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	counts := []struct {
		name  string
		query string
		want  int
	}{
		{name: "runs", query: `SELECT COUNT(*) FROM runs WHERE conversation_id = ?`, want: 50},
		{name: "model attempts", query: `SELECT COUNT(*) FROM model_dispatch_attempts a JOIN runs r ON r.run_id = a.run_id WHERE r.conversation_id = ?`, want: 50},
		{name: "usage", query: `SELECT COUNT(*) FROM model_usage u JOIN model_dispatch_attempts a ON a.attempt_id = u.attempt_id JOIN runs r ON r.run_id = a.run_id WHERE r.conversation_id = ?`, want: 50},
		{name: "assistant history", query: `SELECT COUNT(*) FROM history_entries h JOIN runs r ON r.run_id = h.run_id WHERE r.conversation_id = ?`, want: 50},
	}
	for _, count := range counts {
		var got int
		if err := database.QueryRowContext(ctx, count.query, conversationID).Scan(&got); err != nil || got != count.want {
			t.Fatalf("final %s count=%d want=%d error=%v", count.name, got, count.want, err)
		}
	}
	for _, count := range []struct {
		name  string
		query string
	}{
		{name: "Action attempts", query: `SELECT COUNT(*) FROM dispatch_attempts`},
		{name: "Memory revisions", query: `SELECT COUNT(*) FROM agent_memory_revisions`},
		{name: "Channel ingress", query: `SELECT COUNT(*) FROM channel_ingress_receipts`},
	} {
		var got int
		if err := database.QueryRowContext(ctx, count.query).Scan(&got); err != nil || got != 0 {
			t.Fatalf("final %s count=%d want=0 error=%v", count.name, got, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close final read-only Store: %v", err)
	}

	finalBundle := filepath.Join(root, "turn-50-bundle")
	var backedUp backupCommandResult
	runJSON([]string{
		"backup",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--out", finalBundle,
	}, &backedUp)
	var verified backupCommandResult
	runJSON([]string{"backup-verify", "--bundle", finalBundle}, &verified)
	if verified.Manifest.ManifestDigest == "" ||
		verified.Manifest.ManifestDigest != backedUp.Manifest.ManifestDigest {
		t.Fatalf("final bundle verification=%+v backup=%+v", verified, backedUp)
	}
}

func TestRunChatExplicitFairSchedulerUsesProductionComposition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	args := []string{
		"chat",
		"--enable-fair-scheduler",
		"--scheduler-global-workers", "2",
		"--scheduler-workspace-workers", "1",
		"--scheduler-family-workers", "1",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--message", "scheduled CLI smoke",
		"--request-id", "scheduled-cli-smoke-1",
		"--deadline", deadline.Format(time.RFC3339Nano),
	}
	var output bytes.Buffer
	if err := run(context.Background(), args, &output, io.Discard); err != nil {
		t.Fatalf("scheduled chat: %v", err)
	}
	var result chatCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Disposition != "TERMINATED" || result.Reply != "scheduled CLI smoke" {
		t.Fatalf("scheduled CLI result=%+v", result)
	}
	assertProductionSchedulerState(t, databasePath, 1)

	output.Reset()
	if err := run(context.Background(), args, &output, io.Discard); err != nil {
		t.Fatalf("scheduled terminal retry: %v", err)
	}
	assertProductionSchedulerState(t, databasePath, 1)
}

func TestFairSchedulerDisabledPathDoesNotInspectSchedulerState(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec(`
		INSERT INTO workspace_scheduler_state(
			tenant_id, workspace_id, served_units, revision, updated_at
		) VALUES('orphan-tenant', 'orphan-workspace', 1, 1, ?)
	`, time.Now().UTC().UnixMicro())
	if err := errors.Join(insertErr, database.Close()); err != nil {
		t.Fatal(err)
	}

	direct, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("default direct composition inspected Scheduler state: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatalf("close default direct composition: %v", err)
	}
	config := runscheduler.DefaultConfig(defaultTenantID)
	if _, err := openProductionCompositionWithOptions(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{FairScheduler: &config},
	); err == nil || !strings.Contains(err.Error(), "Scheduler semantic enablement gate") {
		t.Fatalf("enabled Scheduler accepted orphan state: %v", err)
	}
}

func TestSchedulerCLIOptionsRequireExplicitEnableAndRejectChannelCombination(
	t *testing.T,
) {
	if err := run(context.Background(), []string{
		"chat",
		"--scheduler-global-workers", "2",
		"--db", "unused.sqlite",
		"--message", "unused",
	}, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "require explicit --enable-fair-scheduler") {
		t.Fatalf("Scheduler limit without enable error=%v", err)
	}
	if err := run(context.Background(), []string{
		"serve",
		"--db", "unused.sqlite",
		"--enable-fair-scheduler",
		"--enable-channel",
		"--channel-workspace", "workspace",
		"--channel-endpoint", "endpoint",
		"--channel-secret-file", "secret.txt",
	}, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "cannot be enabled together") {
		t.Fatalf("Scheduler+Channel error=%v", err)
	}
}

func assertProductionSchedulerState(
	t *testing.T,
	databasePath string,
	want uint64,
) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, found, readErr := store.GetWorkspaceSchedulerState(
		context.Background(),
		defaultTenantID,
		defaultWorkspaceID,
	)
	closeErr := store.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !found || state.ServedUnits != want || state.Revision != want {
		t.Fatalf("production Scheduler state=%+v found=%v want=%d", state, found, want)
	}
}

func TestRunCompositeChatUsesProductionComposition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     compositeExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	args := []string{
		"chat",
		"--composite",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--message", "Composite CLI smoke",
		"--request-id", "composite-cli-smoke-1",
		"--deadline", deadline.Format(time.RFC3339Nano),
		"--agent", initialized.Defaults.AgentID,
		"--profile", initialized.Defaults.ProfileID,
		"--workspace", initialized.Defaults.WorkspaceID,
	}
	var firstOutput bytes.Buffer
	if err := run(
		context.Background(),
		args,
		&firstOutput,
		io.Discard,
	); err != nil {
		t.Fatalf("run first Composite chat: %v", err)
	}
	var first compositeChatCommandResult
	if err := json.Unmarshal(firstOutput.Bytes(), &first); err != nil {
		t.Fatalf("decode first Composite chat: %v\n%s", err, firstOutput.String())
	}
	if first.RequestID != "composite-cli-smoke-1" ||
		first.RootRunID == "" || len(first.Children) != 2 ||
		first.Reviewer != nil ||
		bytes.Contains(firstOutput.Bytes(), []byte(`"reviewer"`)) ||
		first.Disposition != "TERMINATED" || first.Reply == "" {
		t.Fatalf("unexpected first Composite chat: %#v", first)
	}
	for index, child := range first.Children {
		if child.RunID == "" || child.Disposition != "TERMINATED" {
			t.Fatalf("unexpected Composite Child %d: %#v", index, child)
		}
	}

	var retryOutput bytes.Buffer
	if err := run(
		context.Background(),
		args,
		&retryOutput,
		io.Discard,
	); err != nil {
		t.Fatalf("run Composite retry: %v", err)
	}
	var retry compositeChatCommandResult
	if err := json.Unmarshal(retryOutput.Bytes(), &retry); err != nil {
		t.Fatalf("decode Composite retry: %v\n%s", err, retryOutput.String())
	}
	if !bytes.Equal(firstOutput.Bytes(), retryOutput.Bytes()) {
		t.Fatalf(
			"Composite CLI retry changed output:\nfirst=%#v\nretry=%#v",
			first,
			retry,
		)
	}
}

func TestCompositeChatCommandResultIncludesOptionalReviewerView(t *testing.T) {
	result := newCompositeChatCommandResult(localchat.CompositeChatResult{
		RequestID: "request-reviewer-view",
		Deadline:  time.Unix(1, 0).UTC(),
		RootRunID: "run-root",
		Reviewer: &localchat.CompositeReviewerChatResult{
			RunID: "run-reviewer",
			LoopResult: loopapi.RunResult{
				RunID:       "run-reviewer",
				Disposition: loopapi.DispositionWaitingReconciliation,
				ReasonCode:  "COMPOSITE_REVIEW_UNKNOWN",
			},
		},
		LoopResult: loopapi.RunResult{
			RunID:       "run-root",
			Disposition: loopapi.DispositionWaitingReconciliation,
			ReasonCode:  "COMPOSITE_REVIEW_UNKNOWN",
		},
	})
	if result.Reviewer == nil || result.Reviewer.RunID != "run-reviewer" ||
		result.Reviewer.Disposition != "WAITING_RECONCILIATION" ||
		result.Reviewer.Reason != "COMPOSITE_REVIEW_UNKNOWN" {
		t.Fatalf("Reviewer command view=%+v", result.Reviewer)
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal Reviewer command view: %v", err)
	}
	if !bytes.Contains(canonical, []byte(`"reviewer":{"run_id":"run-reviewer"`)) {
		t.Fatalf("Reviewer command JSON=%s", canonical)
	}
}

func compositeExampleSeedPath(t *testing.T) string {
	t.Helper()
	sourceSeed := exampleSeedPath(t)
	sourceRoot := filepath.Dir(sourceSeed)
	targetRoot := t.TempDir()
	if err := copyTestTree(
		filepath.Join(sourceRoot, "bootstrap-artifacts"),
		filepath.Join(targetRoot, "bootstrap-artifacts"),
	); err != nil {
		t.Fatalf("copy Composite bootstrap artifacts: %v", err)
	}
	raw, err := os.ReadFile(sourceSeed)
	if err != nil {
		t.Fatalf("read base bootstrap seed: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode base bootstrap seed: %v", err)
	}
	document["composite_agents"] = []any{map[string]any{
		"schema_version":         "composite-agent/v1",
		"agent_id":               defaultAgentID,
		"coordinator_profile_id": defaultProfileID,
		"members": []any{
			map[string]any{
				"slot_id":             "analysis",
				"agent_id":            defaultAgentID,
				"profile_id":          defaultProfileID,
				"focus_id":            "analyze",
				"weight_basis_points": 6000,
			},
			map[string]any{
				"slot_id":             "delivery",
				"agent_id":            defaultAgentID,
				"profile_id":          defaultProfileID,
				"focus_id":            "deliver",
				"weight_basis_points": 4000,
			},
		},
	}}
	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode Composite bootstrap seed: %v", err)
	}
	target := filepath.Join(targetRoot, filepath.Base(sourceSeed))
	if err := os.WriteFile(target, canonical, 0o600); err != nil {
		t.Fatalf("write Composite bootstrap seed: %v", err)
	}
	return target
}

func copyTestTree(source, destination string) error {
	return filepath.Walk(source, func(
		path string,
		info os.FileInfo,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode().Perm())
	})
}

func TestRunServeLoopbackAndGracefulShutdown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}

	address, cancel, errCh := startProductionTestServer(
		t,
		databasePath,
		artifactRoot,
	)

	healthClient := &http.Client{Timeout: 2 * time.Second}
	var healthErr error
	for attempt := 0; attempt < 20; attempt++ {
		response, err := healthClient.Get(fmt.Sprintf("http://%s/healthz", address))
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				healthErr = nil
				break
			}
			healthErr = fmt.Errorf("health status=%d", response.StatusCode)
		} else {
			healthErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if healthErr != nil {
		t.Fatalf("serve health check: %v", healthErr)
	}
	conversationClient := &http.Client{Timeout: 15 * time.Second}

	const conversationID = "production-http-conversation-1"
	conversationPayload, err := json.Marshal(map[string]string{
		"conversation_id": conversationID,
		"tenant":          defaultTenantID,
		"principal":       defaultPrincipalID,
		"workspace":       defaultWorkspaceID,
		"agent":           defaultAgentID,
		"profile":         defaultProfileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	created := postProductionConversation(
		t,
		conversationClient,
		address,
		conversationPayload,
		http.StatusCreated,
	)
	if !created.Created || created.ConversationID != conversationID ||
		created.Revision != 0 || created.HeadRunID != "" {
		t.Fatalf("unexpected production HTTP Conversation: %#v", created)
	}
	createdRetry := postProductionConversation(
		t,
		conversationClient,
		address,
		conversationPayload,
		http.StatusOK,
	)
	if createdRetry.Created || createdRetry.ConversationID != created.ConversationID ||
		createdRetry.Revision != created.Revision ||
		createdRetry.HeadRunID != created.HeadRunID ||
		createdRetry.CreatedAt != created.CreatedAt ||
		createdRetry.UpdatedAt != created.UpdatedAt {
		t.Fatalf(
			"same-process Conversation create retry changed identity:\ncreated=%#v\nretry=%#v",
			created,
			createdRetry,
		)
	}

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	payload, err := json.Marshal(map[string]any{
		"tenant":                defaultTenantID,
		"principal":             defaultPrincipalID,
		"workspace":             defaultWorkspaceID,
		"agent":                 defaultAgentID,
		"profile":               defaultProfileID,
		"message":               "production HTTP turn one",
		"request_id":            "production-http-retry-1",
		"deadline":              deadline.Format(time.RFC3339Nano),
		"conversation_id":       conversationID,
		"conversation_revision": uint64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	first := postProductionChat(t, conversationClient, address, payload)
	if first.Reply != "production HTTP turn one" ||
		first.Disposition != "TERMINATED" ||
		first.RunID == "" || first.ConversationID != conversationID ||
		first.ConversationRevision != 1 {
		t.Fatalf("unexpected production HTTP result: %#v", first)
	}
	if retry := postProductionChat(t, conversationClient, address, payload); retry != first {
		t.Fatalf(
			"same-process HTTP retry changed result:\nfirst=%#v\nretry=%#v",
			first,
			retry,
		)
	}
	stopProductionTestServer(t, cancel, errCh)

	restartedAddress, restartedCancel, restartedErrCh :=
		startProductionTestServer(t, databasePath, artifactRoot)
	restartedConversation := postProductionConversation(
		t,
		conversationClient,
		restartedAddress,
		conversationPayload,
		http.StatusOK,
	)
	if restartedConversation.Created || restartedConversation.Revision != 1 ||
		restartedConversation.HeadRunID != first.RunID {
		t.Fatalf(
			"restart did not preserve Conversation head: %#v",
			restartedConversation,
		)
	}
	if retry := postProductionChat(
		t,
		conversationClient,
		restartedAddress,
		payload,
	); retry != first {
		t.Fatalf(
			"restart HTTP retry changed result:\nfirst=%#v\nretry=%#v",
			first,
			retry,
		)
	}
	secondPayload, err := json.Marshal(map[string]any{
		"tenant":                   defaultTenantID,
		"principal":                defaultPrincipalID,
		"workspace":                defaultWorkspaceID,
		"agent":                    defaultAgentID,
		"profile":                  defaultProfileID,
		"message":                  "production HTTP turn two",
		"request_id":               "production-http-retry-2",
		"deadline":                 deadline.Add(time.Minute).Format(time.RFC3339Nano),
		"conversation_id":          conversationID,
		"conversation_revision":    uint64(1),
		"conversation_head_run_id": first.RunID,
	})
	if err != nil {
		t.Fatal(err)
	}
	second := postProductionChat(t, conversationClient, restartedAddress, secondPayload)
	if second.Reply != "production HTTP turn two" ||
		second.ConversationID != conversationID ||
		second.ConversationRevision != 2 || second.RunID == "" ||
		second.RunID == first.RunID {
		t.Fatalf("unexpected post-restart Conversation turn: %#v", second)
	}
	stopProductionTestServer(t, restartedCancel, restartedErrCh)
}

func TestReservedCoreHTTPPathsIncludeConversationCreation(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/healthz", "/v1/chat", "/v1/conversations"} {
		if !isReservedCoreHTTPPath(path) {
			t.Fatalf("Core path %q was available to a Channel", path)
		}
	}
	if isReservedCoreHTTPPath("/channel/inbound") {
		t.Fatal("ordinary Channel path was reserved")
	}
}

func startProductionTestServer(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	output := newSignalBuffer()
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{
			"serve",
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--listen", "127.0.0.1:0",
		}, output, io.Discard)
	}()

	select {
	case <-output.wrote:
	case err := <-errCh:
		cancel()
		t.Fatalf("serve exited before readiness: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("serve readiness timed out")
	}
	var ready map[string]string
	if err := json.Unmarshal(output.Bytes(), &ready); err != nil {
		cancel()
		t.Fatalf("decode serve readiness: %v\n%s", err, output.String())
	}
	if ready["status"] != "ready" || ready["listen"] == "" {
		cancel()
		t.Fatalf("unexpected serve readiness: %#v", ready)
	}
	return ready["listen"], cancel, errCh
}

func stopProductionTestServer(
	t *testing.T,
	cancel context.CancelFunc,
	errCh <-chan error,
) {
	t.Helper()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("graceful serve shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("graceful serve shutdown timed out")
	}
}

func postProductionChat(
	t *testing.T,
	client *http.Client,
	address string,
	payload []byte,
) chatCommandResult {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s/v1/chat", address),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("production HTTP chat: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read production HTTP chat: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"production HTTP chat status=%d body=%s",
			response.StatusCode,
			body,
		)
	}
	var result chatCommandResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode production HTTP chat: %v\n%s", err, body)
	}
	return result
}

func postProductionConversation(
	t *testing.T,
	client *http.Client,
	address string,
	payload []byte,
	wantStatus int,
) conversationCommandResult {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s/v1/conversations", address),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("production HTTP Conversation create: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read production HTTP Conversation create: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf(
			"production HTTP Conversation status=%d want=%d body=%s",
			response.StatusCode,
			wantStatus,
			body,
		)
	}
	var result conversationCommandResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode production HTTP Conversation: %v\n%s", err, body)
	}
	return result
}

func TestRunBackupVerifyRestoreCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	chatArgs := []string{
		"chat",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--message", "backup restore smoke",
		"--request-id", "backup-restore-smoke-1",
		"--deadline", deadline.Format(time.RFC3339Nano),
	}
	var originalOutput bytes.Buffer
	if err := run(context.Background(), chatArgs, &originalOutput, io.Discard); err != nil {
		t.Fatalf("run source chat: %v", err)
	}
	var original chatCommandResult
	if err := json.Unmarshal(originalOutput.Bytes(), &original); err != nil {
		t.Fatalf("decode source chat: %v", err)
	}

	bundle := filepath.Join(root, "backup")
	var backupOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"backup",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--out", bundle,
	}, &backupOutput, io.Discard); err != nil {
		t.Fatalf("run backup: %v", err)
	}
	var backedUp backupCommandResult
	if err := json.Unmarshal(backupOutput.Bytes(), &backedUp); err != nil {
		t.Fatalf("decode backup: %v\n%s", err, backupOutput.String())
	}
	if backedUp.Manifest.ManifestDigest == "" ||
		backedUp.Manifest.StoreIdentity.StoreInstanceID == "" ||
		len(backedUp.Manifest.Artifacts) != 2 {
		t.Fatalf("incomplete backup result: %#v", backedUp)
	}

	var verifyOutput bytes.Buffer
	if err := run(context.Background(), []string{
		"backup-verify",
		"--bundle", bundle,
	}, &verifyOutput, io.Discard); err != nil {
		t.Fatalf("run backup verify: %v", err)
	}
	var verified backupCommandResult
	if err := json.Unmarshal(verifyOutput.Bytes(), &verified); err != nil {
		t.Fatalf("decode backup verify: %v\n%s", err, verifyOutput.String())
	}
	if verified.Manifest.ManifestDigest != backedUp.Manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest=%q, want %q",
			verified.Manifest.ManifestDigest,
			backedUp.Manifest.ManifestDigest,
		)
	}

	restoredDatabase := filepath.Join(root, "restored.sqlite")
	restoredArtifacts := filepath.Join(root, "restored-artifacts")
	restoreArgs := []string{
		"restore",
		"--bundle", bundle,
		"--db", restoredDatabase,
		"--artifact-root", restoredArtifacts,
	}
	var restoreOutput bytes.Buffer
	if err := run(context.Background(), restoreArgs, &restoreOutput, io.Discard); err != nil {
		t.Fatalf("run restore: %v", err)
	}
	var restored restoreCommandResult
	if err := json.Unmarshal(restoreOutput.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore: %v\n%s", err, restoreOutput.String())
	}
	if restored.StoreInstanceID != backedUp.Manifest.StoreIdentity.StoreInstanceID {
		t.Fatalf(
			"restored Store instance=%q, want %q",
			restored.StoreInstanceID,
			backedUp.Manifest.StoreIdentity.StoreInstanceID,
		)
	}
	if err := run(context.Background(), restoreArgs, io.Discard, io.Discard); err == nil {
		t.Fatal("second restore unexpectedly overwrote existing targets")
	}

	restoredChatArgs := append([]string(nil), chatArgs...)
	for index, argument := range restoredChatArgs {
		switch argument {
		case databasePath:
			restoredChatArgs[index] = restoredDatabase
		case artifactRoot:
			restoredChatArgs[index] = restoredArtifacts
		}
	}
	var restoredChatOutput bytes.Buffer
	if err := run(
		context.Background(),
		restoredChatArgs,
		&restoredChatOutput,
		io.Discard,
	); err != nil {
		t.Fatalf("run restored chat: %v", err)
	}
	var restoredChat chatCommandResult
	if err := json.Unmarshal(restoredChatOutput.Bytes(), &restoredChat); err != nil {
		t.Fatalf("decode restored chat: %v", err)
	}
	if !reflect.DeepEqual(restoredChat, original) {
		t.Fatalf(
			"restored retry changed terminal result:\noriginal=%#v\nrestored=%#v",
			original,
			restoredChat,
		)
	}
}

func TestRunMigrateCreatesMandatoryBackupBeforeNoOp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	bundle := filepath.Join(root, "pre-migration-backup")
	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"migrate",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--backup-out", bundle,
	}, &output, io.Discard); err != nil {
		t.Fatalf("run migrate: %v", err)
	}
	var result migrateCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode migrate: %v\n%s", err, output.String())
	}
	if result.FromVersion != currentstore.UserVersion ||
		result.ToVersion != currentstore.UserVersion ||
		len(result.AppliedVersions) != 0 ||
		result.BackupManifest.StoreIdentity.UserVersion != currentstore.UserVersion ||
		result.SchemaFingerprint != currentstore.ExpectedSchemaFingerprint {
		t.Fatalf("migrate result = %+v", result)
	}
	verified, err := currentbackup.VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("verify mandatory backup: %v", err)
	}
	if verified.ManifestDigest != result.BackupManifest.ManifestDigest {
		t.Fatalf("backup digest = %q, want %q", verified.ManifestDigest, result.BackupManifest.ManifestDigest)
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(context.Background(), databasePath); err != nil {
		t.Fatalf("verify migrated Store: %v", err)
	}
}

func TestRunMigrateRejectsActiveAndUnknownStoreBeforeBackup(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string) func()
	}{
		{
			name: "active",
			mutate: func(t *testing.T, path string) func() {
				store, err := currentstore.OpenExistingCurrentStore(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				return func() { _ = store.Close() }
			},
		},
		{
			name: "unknown-version",
			mutate: func(t *testing.T, path string) func() {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`PRAGMA user_version=99`); err != nil {
					_ = db.Close()
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				return func() {}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			if _, err := initializeProductionData(context.Background(), initInput{
				DatabasePath: databasePath,
				SeedPath:     exampleSeedPath(t),
				ArtifactRoot: artifactRoot,
			}); err != nil {
				t.Fatal(err)
			}
			cleanup := test.mutate(t, databasePath)
			defer cleanup()
			bundle := filepath.Join(root, "must-not-exist")
			err := run(context.Background(), []string{
				"migrate",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--backup-out", bundle,
			}, io.Discard, io.Discard)
			if err == nil {
				t.Fatal("migrate unexpectedly succeeded")
			}
			if _, statErr := os.Stat(bundle); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("failed migrate published backup: %v", statErr)
			}
		})
	}
}

type signalBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	wrote chan struct{}
	once  sync.Once
}

func newSignalBuffer() *signalBuffer {
	return &signalBuffer{wrote: make(chan struct{})}
}

func (buffer *signalBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written, err := buffer.data.Write(content)
	buffer.once.Do(func() { close(buffer.wrote) })
	return written, err
}

func (buffer *signalBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.data.Bytes())
}

func (buffer *signalBuffer) String() string {
	return string(buffer.Bytes())
}
