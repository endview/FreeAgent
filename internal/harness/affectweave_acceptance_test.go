package harness

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const affectExtensionID = "cc.endview.affectweave"

func affectHost(t *testing.T) (*Service, *TaskService, string) {
	t.Helper()
	binary := os.Getenv("FREEAGENT_TEST_AFFECTWEAVE_BINARY")
	if binary == "" {
		t.Skip("NOT_RUN: provide the independently built AffectWeave Go binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	fixture, tools, workspace := testHarness(t, EchoModel{})
	core := NewService(fixture.Store, fixture.Config, Adapters{Model: EchoModel{}, Tools: tools})
	t.Cleanup(func() { _ = core.extensions.close() })
	tasks := NewTaskService(core)
	root := filepath.Join(core.Store.DataDir(), "extensions", affectExtensionID)
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	name := "affectweave"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = copyRegularFile(binary, filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, name), 0700); err != nil {
		t.Fatal(err)
	}
	var manifest ExtensionManifest
	rawManifest, err := os.ReadFile(filepath.Join("testdata", "affectweave-manifest.json"))
	if err != nil || json.Unmarshal(rawManifest, &manifest) != nil {
		t.Fatal("invalid fixture manifest", err)
	}
	manifest.Run = ExtensionRun{Command: "./" + name}

	if err = os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	config := cloneConfig(core.Config)
	config.Extensions.Enabled = map[string]bool{affectExtensionID: true}
	config.Extensions.Settings = map[string]map[string]any{affectExtensionID: {"role_id": "maya", "context_bytes": 6000}}
	if err = WriteConfig(core.Store.DataDir(), config); err != nil {
		t.Fatal(err)
	}
	core.setConfig(config)
	core.extensions.configure(config)
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlInitExtensionStorage, map[string]string{"id": affectExtensionID}, "", "test-owner"); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "resource": "maya", "actor_id": "owner"}, "action": "create_role", "expected_revision": 0, "payload": map[string]any{"persona": map[string]any{"id": "maya", "name": "Maya", "identity_summary": "Precise engineering partner", "boundaries": []string{"Never disclose private messages"}, "version": 1, "frozen": true}, "spaces": []string{"local"}}}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, args, "", "test-owner"); err != nil {
		t.Fatal(err)
	}
	_ = workspace
	return core, tasks, root
}
func affectState(t *testing.T, tasks *TaskService, chat string) struct {
	Revision     int64 `json:"revision"`
	Relationship struct {
		Trust float64 `json:"trust"`
	} `json:"relationship"`
} {
	t.Helper()
	var data struct {
		State struct {
			Revision     int64 `json:"revision"`
			Relationship struct {
				Trust float64 `json:"trust"`
			} `json:"relationship"`
		} `json:"state"`
	}
	raw, err := tasks.ExtensionState(t.Context(), affectExtensionID, chat, "maya", "owner")
	if err != nil || json.Unmarshal(raw, &data) != nil {
		t.Fatal(string(raw), err)
	}
	return data.State
}
func TestAffectWeaveActualHostContinuityConfirmationAndPairRestore(t *testing.T) {
	core, tasks, pkg := affectHost(t)
	var calls atomic.Int32
	core.Model = modelFunc(func(_ context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		found := false
		for _, m := range request.Messages {
			if strings.Contains(m.Content, "Precise engineering partner") && strings.Contains(m.Content, "Never disclose private messages") {
				found = true
			}
		}
		if !found {
			t.Error("character fragment missing from actual model request")
		}
		calls.Add(1)
		emit("hello")
		return ModelResponse{Content: "hello"}, nil
	})
	chat := conversation(t, core)
	first, err := tasks.Submit(t.Context(), chat, "first", "Hello Maya")
	if err != nil || first.Run.Status != "completed" {
		t.Fatal(first, err)
	}
	before := affectState(t, tasks, chat)
	if before.Revision != 2 {
		t.Fatal(before)
	}
	// Both conversations start with message 1, but their adopted IDs must differ.
	other := conversation(t, core)
	second, err := tasks.Submit(t.Context(), other, "second", "Continue in this conversation")
	if err != nil || second.Run.Status != "completed" {
		t.Fatal(second, err)
	}
	after := affectState(t, tasks, other)
	if after.Revision != 3 {
		t.Fatalf("same-space cross-conversation adoption: %+v", after)
	}
	a, _ := core.extensionIdentity(t.Context(), first.Run)
	b, _ := core.extensionIdentity(t.Context(), second.Run)
	if a.Inputs[0].ID == b.Inputs[0].ID {
		t.Fatal("per-conversation source ID collision")
	}
	control, _ := tasks.Control()
	change := map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "resource": "maya", "actor_id": "owner"}, "action": "apply_proposal", "expected_revision": after.Revision, "payload": map[string]any{"source_id": b.Inputs[0].ID, "proposal": map[string]any{"relationship_delta": map[string]any{"trust": .05}}}}
	proposal, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlChangeExtensionState, Arguments: mustJSON(change), Authorization: ControlModelProposed, ConversationID: other})
	if err != nil {
		t.Fatal(err)
	}
	if affectState(t, tasks, other).Relationship.Trust != .5 {
		t.Fatal("unconfirmed proposal changed state")
	}
	cap, _ := control.IssueInteractiveCapability(t.Context(), other, "test-owner")
	if _, err = control.RejectControlChange(t.Context(), proposal.ID, controlAuth(cap, other, "test-owner", "")); err != nil {
		t.Fatal(err)
	}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, change, "", "test-owner"); err != nil {
		t.Fatal(err)
	}
	saved := affectState(t, tasks, other)
	if saved.Revision != 4 || saved.Relationship.Trust != .55 {
		t.Fatal(saved)
	}
	// Retrying the exact hook keeps the version frozen at adoption.
	if _, err = core.extensions.prepare(t.Context(), affectExtensionID, first.Run, "chat", "turn", nil, nil, core); err != nil {
		t.Fatal(err)
	}
	if current := affectState(t, tasks, chat); current.Revision != saved.Revision {
		t.Fatal("repeated context advanced state", current)
	}
	// The restricted extension credentials cannot connect to the Core database.
	extConn, _ := postgres.Read(extensionStoragePath(tasks.DataDir(), affectExtensionID))
	coreConn, _ := postgres.Read(filepath.Join(tasks.DataDir(), postgres.ConfigFile))
	extConn.Database = coreConn.Database
	denied, _ := postgres.SQL(extConn, "", false)
	err = denied.PingContext(t.Context())
	denied.Close()
	if err == nil {
		t.Fatal("extension obtained Core database access")
	}
	backup := filepath.Join(t.TempDir(), "paired")
	if err = tasks.BackupExtensionPair(t.Context(), affectExtensionID, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyExtensionPair(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	// Missing original source blocks a complete backup and always releases pause.
	_, err = core.Store.db.ExecContext(t.Context(), `DELETE FROM fa.messages WHERE conversation_id=$1 AND id=1`, chat)
	if err != nil {
		t.Fatal(err)
	}
	failed := filepath.Join(t.TempDir(), "must-not-publish")
	if err = tasks.BackupExtensionPair(t.Context(), affectExtensionID, failed); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err = os.Stat(failed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete backup published")
	}
	if err = core.extensions.restart(affectExtensionID); err != nil {
		t.Fatal(err)
	}
	if _, err = core.extensions.prepare(t.Context(), affectExtensionID, second.Run, "chat", "turn", nil, nil, core); err == nil {
		t.Fatal("extension ahead of missing Core source was enabled after restart")
	}
	if _, err = tasks.ExtensionState(t.Context(), affectExtensionID, other, "maya", "owner"); err != nil {
		t.Fatal("pause not released", err)
	}
	if err = core.extensions.close(); err != nil {
		t.Fatal(err)
	}
	if err = core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err = RestoreExtensionPair(t.Context(), backup, restored, pkg); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(restored)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	again := NewService(store, config, Adapters{Model: EchoModel{}})
	defer again.extensions.close()
	actual := affectState(t, NewTaskService(again), other)
	if actual.Revision != saved.Revision || actual.Relationship.Trust != saved.Relationship.Trust {
		t.Fatal("restore lost character state", actual, saved)
	}
	if calls.Load() != 2 {
		t.Fatal("backup/restart replayed a model", calls.Load())
	}
}
func TestAffectWeavePausedWritebackKeepsCoreAvailable(t *testing.T) {
	core, tasks, _ := affectHost(t)
	pause := "test-pause"
	if _, err := tasks.extensionStateRPC(t.Context(), affectExtensionID, "state.pause", map[string]any{"pause_id": pause, "until": time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "paused-core", "Core remains available")
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result, err)
	}
	if _, err = tasks.extensionStateRPC(t.Context(), affectExtensionID, "state.resume", map[string]string{"pause_id": pause}); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.Submit(t.Context(), chat, "after-pause", "Recover existing adopted facts"); err != nil {
		t.Fatal(err)
	}
	if state := affectState(t, tasks, chat); state.Revision != 3 {
		t.Fatal("paused Core input was not recovered once", state)
	}
}

func TestAffectWeavePassiveContextInNoneDoesNotGrantTools(t *testing.T) {
	core, tasks, _ := affectHost(t)
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	var calls int
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if len(r.Tools) != 0 || !r.DenyOperations {
			t.Error("passive state granted tools")
		}
		found := false
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "Precise engineering partner") {
				found = true
			}
		}
		if !found {
			t.Error("None lost explicitly enabled character data")
		}
		return ModelResponse{Content: "character chat without tools"}, nil
	})
	result, err := tasks.Submit(t.Context(), conversation(t, core), "none-character", "Hello")
	if err != nil || result.Run.Status != "completed" || calls != 1 {
		t.Fatal(result, err, calls)
	}
}
