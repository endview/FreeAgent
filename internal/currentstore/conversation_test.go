package currentstore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCreateConversationReadAndExactRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.db")
	if _, err := InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Conversation Store: %v", err)
		}
	})

	input := validConversationInput()
	created, err := store.CreateConversation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created {
		t.Fatal("first CreateConversation was not reported as created")
	}
	if created.Record.ConversationID != input.ConversationID ||
		created.Record.TenantID != input.TenantID ||
		created.Record.PrincipalID != input.PrincipalID ||
		created.Record.WorkspaceID != input.WorkspaceID ||
		created.Record.AgentID != input.AgentID ||
		created.Record.ProfileID != input.ProfileID ||
		created.Record.HeadRunID != "" || created.Record.Revision != 0 ||
		created.Record.CreatedAt.IsZero() ||
		!created.Record.CreatedAt.Equal(created.Record.UpdatedAt) {
		t.Fatalf("created Conversation = %+v", created.Record)
	}

	read, err := store.GetConversation(ctx, input.TenantID, input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, created.Record) {
		t.Fatalf("read Conversation = %+v, want %+v", read, created.Record)
	}

	retried, err := store.CreateConversation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Created {
		t.Fatal("exact CreateConversation retry was reported as created")
	}
	if !reflect.DeepEqual(retried.Record, created.Record) {
		t.Fatalf("retried Conversation = %+v, want %+v", retried.Record, created.Record)
	}
	if _, err := store.GetConversation(
		ctx,
		"another-tenant",
		input.ConversationID,
	); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("cross-Tenant GetConversation error = %v", err)
	}
}

func TestCreateConversationRejectsSameIDWithDifferentFixedScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation-conflict.db")
	if _, err := InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	input := validConversationInput()
	if _, err := store.CreateConversation(ctx, input); err != nil {
		t.Fatal(err)
	}
	conflicts := []CreateConversationInput{
		func() CreateConversationInput { changed := input; changed.TenantID = "tenant-2"; return changed }(),
		func() CreateConversationInput { changed := input; changed.PrincipalID = "principal-2"; return changed }(),
		func() CreateConversationInput { changed := input; changed.WorkspaceID = "workspace-2"; return changed }(),
		func() CreateConversationInput { changed := input; changed.AgentID = "agent-2"; return changed }(),
		func() CreateConversationInput { changed := input; changed.ProfileID = "profile-2"; return changed }(),
	}
	for _, conflict := range conflicts {
		if _, err := store.CreateConversation(
			ctx,
			conflict,
		); !errors.Is(err, ErrConversationConflict) {
			t.Fatalf("conflicting CreateConversation(%+v) error = %v", conflict, err)
		}
	}
	read, err := store.GetConversation(ctx, input.TenantID, input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if !conversationScopeMatchesInput(read, input) || read.Revision != 0 ||
		read.HeadRunID != "" {
		t.Fatalf("conflicts changed Conversation = %+v", read)
	}
}

func TestConversationSurvivesStoreReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation-reopen.db")
	if _, err := InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	input := validConversationInput()
	created, err := store.CreateConversation(ctx, input)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	read, err := reopened.GetConversation(ctx, input.TenantID, input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, created.Record) {
		t.Fatalf("reopened Conversation = %+v, want %+v", read, created.Record)
	}
	retried, err := reopened.CreateConversation(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Created || !reflect.DeepEqual(retried.Record, created.Record) {
		t.Fatalf("reopened retry = %+v, want existing %+v", retried, created.Record)
	}
}

func TestConversationRejectsInvalidCanonicalIDsAndMissingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation-validation.db")
	if _, err := InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, invalid := range []string{"", " padded", "line\nbreak", "e\u0301"} {
		input := validConversationInput()
		input.ConversationID = invalid
		if _, err := store.CreateConversation(
			ctx,
			input,
		); !errors.Is(err, ErrInvalidConversation) {
			t.Fatalf("CreateConversation invalid ID %q error = %v", invalid, err)
		}
	}
	if _, err := store.GetConversation(
		ctx,
		"tenant-1",
		"missing-conversation",
	); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("missing GetConversation error = %v", err)
	}
	if _, err := store.GetConversation(
		nil,
		"tenant-1",
		"conversation-1",
	); !errors.Is(err, ErrInvalidConversation) {
		t.Fatalf("nil-context GetConversation error = %v", err)
	}
}

func validConversationInput() CreateConversationInput {
	return CreateConversationInput{
		ConversationID: "conversation-1",
		TenantID:       "tenant-1",
		PrincipalID:    "principal-1",
		WorkspaceID:    "workspace-1",
		AgentID:        "agent-1",
		ProfileID:      "profile-1",
	}
}
