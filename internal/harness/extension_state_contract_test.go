package harness

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"testing"
)

func TestExtensionStateScopeUsesDeclarationAndOriginalAdoptedInput(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	installStateTest(t, core)
	tasks := NewTaskService(core)
	core.Store.telegramIdentity("1")
	target := ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectCapabilities(t.Context(), space.ActiveConversationID, CapabilitySelection{Extensions: []string{"com.example.state-test"}}); err != nil {
		t.Fatal(err)
	}
	if err = tasks.SetPersonPermission(t.Context(), "telegram:user:7", "Fixture", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "source", Purpose: "request"}, "change project label")
	if err != nil {
		t.Fatal(err)
	}
	run, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	change := ExtensionStateChange{ID: "com.example.state-test", Scope: ExtensionStateScope{SpaceID: space.ID, Resource: "project-one", ActorID: "telegram:user:7"}, Action: "set_label", ExpectedRevision: 1, Payload: json.RawMessage(`{"label":"reviewed"}`)}
	request := ControlPlanRequest{Kind: ControlChangeExtensionState, Authorization: ControlModelProposed, ConversationID: run.Run.ConversationID, Arguments: mustJSON(change)}
	if _, err := control.PlanControlChange(t.Context(), request); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("unbound external model proposal accepted", err)
	}
	request.originRunID = run.Run.ID
	operation, err := control.PlanControlChange(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	control.mu.Lock()
	record := control.operations[operation.ID]
	control.mu.Unlock()
	var bound ExtensionStateChange
	if json.Unmarshal(record.Arguments, &bound) != nil || bound.Origin == nil || bound.Origin.RunID != run.Run.ID {
		t.Fatal("source not frozen")
	}
	change.Scope.ActorID = "telegram:user:8"
	request.Arguments = mustJSON(change)
	if _, err := control.PlanControlChange(t.Context(), request); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("another actor accepted", err)
	}
	// A global operation with a neutral name cannot evade the host check.
	path := filepath.Join(tasks.DataDir(), "extensions", change.ID, "extension.json")
	raw, _ := os.ReadFile(path)
	var manifest ExtensionManifest
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("bad fixture")
	}
	manifest.StateOperations["set_label"] = ExtensionStateOperation{Scope: "global", Model: true, InputSchema: manifest.StateOperations["set_label"].InputSchema}
	if err := os.WriteFile(path, mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	change.Scope.ActorID = "telegram:user:7"
	request.Arguments = mustJSON(change)
	if _, err := control.PlanControlChange(t.Context(), request); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("global edit through neutral action accepted", err)
	}
	if err := control.validateExtensionChange(context.Background(), &record, &bound, ""); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatal("changed declaration accepted old confirmation", err)
	}
}

func TestExtensionStorageDifferentSchemaAndExplicitBinding(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	paths := []string{extensionStoragePath(core.Store.DataDir(), "com.example.labels"), extensionStoragePath(core.Store.DataDir(), "com.example.notes")}
	for i, schema := range []string{"labels", "notes"} {
		if err := postgres.ProvisionExtension(t.Context(), core.Store.DataDir(), paths[i], []string{"com.example.labels", "com.example.notes"}[i], schema, "1"); err != nil {
			t.Fatal(err)
		}
		c, err := postgres.Read(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		db, err := postgres.SQL(c, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(t.Context(), "CREATE TABLE "+schema+".items (id text PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	first, _ := postgres.Read(paths[0])
	second, _ := postgres.Read(paths[1])
	first.Database = second.Database
	db, _ := postgres.SQL(first, "", false)
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("cross-extension database access")
	}
	db.Close()
	// Existing data keeps its schema and receives metadata only in explicit maintenance.
	first, _ = postgres.Read(paths[0])
	first.ExtensionID, first.Schema, first.StorageVersion = "", "", ""
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Write(paths[0], first); err != nil {
		t.Fatal(err)
	}
	if err := postgres.BindExistingExtension(t.Context(), core.Store.DataDir(), paths[0], "com.example.labels", "notes", "1"); err == nil {
		t.Fatal("wrong existing schema rebound")
	}
	if err := postgres.BindExistingExtension(t.Context(), core.Store.DataDir(), paths[0], "com.example.labels", "labels", "1"); err != nil {
		t.Fatal(err)
	}
	c, _ := postgres.Read(paths[0])
	db, _ = postgres.SQL(c, "", false)
	defer db.Close()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM labels.items`).Scan(&n); err != nil {
		t.Fatal("data object lost", err)
	}
	if err := postgres.BindExistingExtension(t.Context(), core.Store.DataDir(), paths[0], "com.example.labels", "labels", "1"); err != nil {
		t.Fatal("unchanged binding preflight", err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE SCHEMA unexpected`); err == nil {
		t.Fatal("restricted role unexpectedly created another schema")
	}
	// It does own its existing schema; metadata alone must not hide its loss.
	if _, err := db.ExecContext(t.Context(), `DROP SCHEMA labels CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.BindExistingExtension(t.Context(), core.Store.DataDir(), paths[0], "com.example.labels", "labels", "1"); err == nil {
		t.Fatal("matching metadata hid changed actual schema")
	}
}

func TestExtensionSettingsValidateUserDirectBeforePersistence(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	installStateTest(t, core)
	id := "com.example.state-test"
	path := filepath.Join(core.Store.DataDir(), "extensions", id, "extension.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SettingsSchema = json.RawMessage(`{"type":"object","properties":{"label":{"type":"string","maxLength":32}},"additionalProperties":false}`)
	if err := os.WriteFile(path, mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	control, err := NewTaskService(core).Control()
	if err != nil {
		t.Fatal(err)
	}
	request := ControlPlanRequest{Kind: ControlSetExtensionPolicy, Authorization: ControlUserDirect,
		Arguments: mustJSON(map[string]any{"id": id, "settings": map[string]any{"resource": "wrong-field"}})}
	if _, err := control.PlanControlChange(t.Context(), request); err == nil {
		t.Fatal("user-direct settings bypassed the installed schema")
	}
	if len(core.currentConfig().Extensions.Settings[id]) != 0 {
		t.Fatal("rejected settings persisted")
	}
	request.Arguments = mustJSON(map[string]any{"id": id, "settings": map[string]any{"label": "reviewed"}})
	if _, err := control.PlanControlChange(t.Context(), request); err != nil {
		t.Fatal("valid user setting rejected", err)
	}
}
