package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPinnedMemoryNoneAndUnpinDoesNotResurrect(t *testing.T) {
	var requests []ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "pin-source", "PINNED_CONVENTION")
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{RequestKey: "pin", Action: "pin", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "read-pin", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || len(requests[0].Memory) != 1 || len(requests[0].Tools) != 0 {
		t.Fatal("text visibility incorrectly coupled to tool permissions", requests)
	}
	raw, _ := json.Marshal(requests[0].Messages)
	if strings.Count(string(raw), "PINNED_CONVENTION") != 1 {
		t.Fatal("missing or duplicated pin", string(raw))
	}
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{RequestKey: "unpin", Action: "unpin", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "after-unpin", "hello again"); err != nil {
		t.Fatal(err)
	}
	if len(requests[1].Memory) != 0 {
		t.Fatal("unpin was resurrected")
	}
}

func TestMemoryPinControlRequiresExactSourceAndConfirmation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "control-pin", "EXACT_PIN_CONTENT")
	control, err := NewControlService(tasks)
	if err != nil {
		t.Fatal(err)
	}
	plan := func(revision int, pin *MemoryPin) (ControlOperation, error) {
		return control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetMemoryPin, Arguments: mustJSON(memoryPinArguments{ConversationID: chat, Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: revision, Pin: pin}), Authorization: ControlModelProposed, ConversationID: chat})
	}
	if _, err := plan(1, &MemoryPin{Scope: "conversation", Target: newID()}); err == nil {
		t.Fatal("preview accepted a nonexistent pin target")
	}
	op, err := plan(1, &MemoryPin{Scope: "conversation", Target: chat})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := tasks.ReadMemory(t.Context(), chat, ref.Scope, ref.EntryID)
	if err != nil || entry.Pinned != nil || op.Status != ControlPending {
		t.Fatal("proposal changed retained memory", entry, err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, chat, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "local-user", confirmation.Code))
	if err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	op, err = plan(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err = control.RenderConfirmation(t.Context(), op.ID, chat, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{Action: "unpin", RequestKey: "new-revision", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "local-user", confirmation.Code)); err == nil {
		t.Fatal("stale memory pin proposal applied")
	}
}

func TestPinnedMemoryExclusionRevokesFrozenDispatch(t *testing.T) {
	var requests []ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "frozen-pin", "REVOKED_PIN")
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{Action: "pin", RequestKey: "pin", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "pin-use", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.ExcludePinnedMemory(t.Context(), chat, ref.EntryID); err != nil {
		t.Fatal(err)
	}
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := validateRequestMemory(t.Context(), tx, result.Run, requests[0]); !errors.Is(err, ErrMemoryDenied) {
		t.Fatal("revoked pin reached a new dispatch", err)
	}
}

func TestPinnedMemorySpaceGrantAndExplicitExclusion(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "global-source", "PRIVATE_GLOBAL_PIN")
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{RequestKey: "global-pin", Action: "pin", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 1, Pinned: &MemoryPin{Scope: "global"}}); err != nil {
		t.Fatal(err)
	}
	space, err := tasks.PairChannel(t.Context(), ChannelTarget{Source: "lark", ApplicationID: "app:tenant", ChatID: "group"}, "group", "Group")
	if err != nil {
		t.Fatal(err)
	}
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: ChannelTarget{Source: "lark", ApplicationID: "app:tenant", ChatID: "group"}, PersonID: "lark:app:tenant:person", MessageID: "1", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	_ = space
	if pins, err := core.selectPinnedMemory(t.Context(), input.ConversationID, MemorySelection{}); err != nil || len(pins) != 0 {
		t.Fatal("global pin leaked into external space", pins, err)
	}
	selection := MemorySelection{PinnedGrants: []string{ref.EntryID}}
	if pins, err := core.selectPinnedMemory(t.Context(), input.ConversationID, selection); err != nil || len(pins) != 1 {
		t.Fatal("explicit grant missing", pins, err)
	}
	selection.ExcludedPins = []string{ref.EntryID}
	if pins, err := core.selectPinnedMemory(t.Context(), input.ConversationID, selection); err != nil || len(pins) != 0 {
		t.Fatal("exclusion lost", pins, err)
	}
	if defaults, err := tasks.SelectedCapabilities(t.Context(), input.ConversationID); err != nil || len(defaults.Skills)+len(defaults.Extensions) != 0 {
		t.Fatal("external space inherited private defaults", defaults, err)
	}
}
