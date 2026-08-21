package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	conversationRestartHelperEnv = "FREEAGENT_CONVERSATION_RESTART_HELPER"
	conversationRestartDBEnv     = "FREEAGENT_CONVERSATION_RESTART_DB"
	conversationRestartRootEnv   = "FREEAGENT_CONVERSATION_RESTART_ARTIFACTS"
	conversationRestartIDEnv     = "FREEAGENT_CONVERSATION_RESTART_ID"
	conversationRestartEndEnv    = "FREEAGENT_CONVERSATION_RESTART_END"
	conversationRestartTimeEnv   = "FREEAGENT_CONVERSATION_RESTART_DEADLINE"
)

// TestRunConversationFiftyTurnsAcrossNormalProcessRestart proves that a
// Conversation head survives a complete, successful process exit. The helper
// process owns turns 1..25, exits normally, and a fresh helper process owns
// turns 26..50.
func TestRunConversationFiftyTurnsAcrossNormalProcessRestart(t *testing.T) {
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

	const conversationID = "normal-process-restart-fifty-turns"
	var createOutput bytes.Buffer
	if err := run(ctx, []string{
		"conversation-create",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &createOutput, io.Discard); err != nil {
		t.Fatalf("create Conversation: %v", err)
	}
	var created conversationCommandResult
	if err := json.Unmarshal(createOutput.Bytes(), &created); err != nil {
		t.Fatalf("decode Conversation creation: %v", err)
	}
	if !created.Created || created.Revision != 0 || created.HeadRunID != "" {
		t.Fatalf("unexpected Conversation genesis: %+v", created)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	baseDeadline := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Microsecond)
	runConversationRestartHelper(
		t,
		executable,
		databasePath,
		artifactRoot,
		conversationID,
		25,
		baseDeadline,
	)
	turn25 := getConversationForSubprocessTest(t, databasePath, conversationID)
	if turn25.Revision != 25 || turn25.HeadRunID == "" {
		t.Fatalf("first process did not commit turn 25: %+v", turn25)
	}

	runConversationRestartHelper(
		t,
		executable,
		databasePath,
		artifactRoot,
		conversationID,
		50,
		baseDeadline,
	)
	turn50 := getConversationForSubprocessTest(t, databasePath, conversationID)
	if turn50.Revision != 50 || turn50.HeadRunID == "" ||
		turn50.HeadRunID == turn25.HeadRunID {
		t.Fatalf("second process did not commit turn 50: %+v", turn50)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open final Store: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close final Store: %v", err)
		}
	}()
	for _, count := range []struct {
		name  string
		query string
		args  []any
		want  int
	}{
		{
			name:  "Runs",
			query: `SELECT COUNT(*) FROM runs WHERE conversation_id = ?`,
			args:  []any{conversationID},
			want:  50,
		},
		{
			name: "model attempts",
			query: `SELECT COUNT(*) FROM model_dispatch_attempts a
				JOIN runs r ON r.run_id = a.run_id
				WHERE r.conversation_id = ?`,
			args: []any{conversationID},
			want: 50,
		},
		{
			name: "Usage rows",
			query: `SELECT COUNT(*) FROM model_usage u
				JOIN model_dispatch_attempts a ON a.attempt_id = u.attempt_id
				JOIN runs r ON r.run_id = a.run_id
				WHERE r.conversation_id = ?`,
			args: []any{conversationID},
			want: 50,
		},
		{
			name:  "Action attempts",
			query: `SELECT COUNT(*) FROM dispatch_attempts`,
			want:  0,
		},
		{
			name:  "Memory revisions",
			query: `SELECT COUNT(*) FROM agent_memory_revisions`,
			want:  0,
		},
		{
			name:  "Channel ingress",
			query: `SELECT COUNT(*) FROM channel_ingress_receipts`,
			want:  0,
		},
	} {
		var got int
		err := database.QueryRowContext(ctx, count.query, count.args...).Scan(&got)
		if err != nil || got != count.want {
			t.Fatalf("final %s count=%d want=%d error=%v", count.name, got, count.want, err)
		}
	}
}

func runConversationRestartHelper(
	t *testing.T,
	executable string,
	databasePath string,
	artifactRoot string,
	conversationID string,
	endTurn int,
	baseDeadline time.Time,
) {
	t.Helper()
	command := exec.Command(
		executable,
		"-test.run=^TestConversationNormalRestartHelper$",
		"-test.count=1",
		"-test.v",
	)
	command.Env = append(os.Environ(),
		conversationRestartHelperEnv+"=1",
		conversationRestartDBEnv+"="+databasePath,
		conversationRestartRootEnv+"="+artifactRoot,
		conversationRestartIDEnv+"="+conversationID,
		conversationRestartEndEnv+"="+strconv.Itoa(endTurn),
		conversationRestartTimeEnv+"="+baseDeadline.Format(time.RFC3339Nano),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"Conversation helper through turn %d exited unsuccessfully: %v\n%s",
			endTurn,
			err,
			output,
		)
	}
}

// TestConversationNormalRestartHelper is inert during ordinary test discovery.
// The parent test executes it in a new OS process with only non-secret paths and
// deterministic turn metadata in the environment.
func TestConversationNormalRestartHelper(t *testing.T) {
	if os.Getenv(conversationRestartHelperEnv) != "1" {
		return
	}
	databasePath := os.Getenv(conversationRestartDBEnv)
	artifactRoot := os.Getenv(conversationRestartRootEnv)
	conversationID := os.Getenv(conversationRestartIDEnv)
	endTurn, err := strconv.Atoi(os.Getenv(conversationRestartEndEnv))
	if err != nil || endTurn < 1 || endTurn > 50 {
		t.Fatalf("invalid helper end turn: %q", os.Getenv(conversationRestartEndEnv))
	}
	baseDeadline, err := time.Parse(
		time.RFC3339Nano,
		os.Getenv(conversationRestartTimeEnv),
	)
	if err != nil {
		t.Fatalf("invalid helper deadline: %v", err)
	}

	current := getConversationForSubprocessTest(t, databasePath, conversationID)
	if current.Revision > uint64(endTurn) {
		t.Fatalf("Conversation revision=%d exceeds helper end=%d", current.Revision, endTurn)
	}
	ctx := context.Background()
	for turn := int(current.Revision) + 1; turn <= endTurn; turn++ {
		args := []string{
			"chat",
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--conversation", conversationID,
			"--conversation-revision", strconv.FormatUint(current.Revision, 10),
			"--message", fmt.Sprintf("normal process restart turn %02d", turn),
			"--request-id", fmt.Sprintf("normal-process-restart-%02d", turn),
			"--deadline", baseDeadline.Add(time.Duration(turn) * time.Minute).Format(time.RFC3339Nano),
		}
		if current.HeadRunID != "" {
			args = append(args, "--conversation-head-run", current.HeadRunID)
		}
		var output bytes.Buffer
		if err := run(ctx, args, &output, io.Discard); err != nil {
			t.Fatalf("chat turn %d: %v", turn, err)
		}
		var result chatCommandResult
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("decode chat turn %d: %v", turn, err)
		}
		if result.Disposition != "TERMINATED" ||
			result.Reason != "MODEL_SUCCEEDED" ||
			result.ConversationID != conversationID ||
			result.ConversationRevision != uint64(turn) || result.RunID == "" {
			t.Fatalf("chat turn %d result=%+v", turn, result)
		}
		current.Revision = result.ConversationRevision
		current.HeadRunID = result.RunID
	}
}

func getConversationForSubprocessTest(
	t *testing.T,
	databasePath string,
	conversationID string,
) conversationCommandResult {
	t.Helper()
	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"conversation-get",
		"--db", databasePath,
		"--conversation", conversationID,
	}, &output, io.Discard); err != nil {
		t.Fatalf("get Conversation: %v", err)
	}
	var result conversationCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode Conversation: %v", err)
	}
	return result
}
