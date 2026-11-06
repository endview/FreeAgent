package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAffectWeaveCurrentSpaceAuthorizationAndActorIsolation(t *testing.T) {
	core, tasks, _ := affectHost(t)
	chat := conversation(t, core)
	if _, err := tasks.Submit(t.Context(), chat, "owner-input", "hello"); err != nil {
		t.Fatal(err)
	}
	original, _ := core.Store.GetLatestRun(t.Context(), chat)
	identity, err := core.extensionIdentity(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	independent := *identity
	independent.Inputs = append([]ExtensionAdoptedInput(nil), identity.Inputs...)
	independent.Inputs[0].ID = "other-chat/1"
	independent.Inputs[0].ActorID = "telegram:user:987654321"
	if _, err = tasks.extensionStateRPC(t.Context(), affectExtensionID, "context.prepare", map[string]any{"identity": independent, "variant": "maya", "settings": map[string]any{"context_bytes": 6000}}); err != nil {
		t.Fatal(err)
	}
	owner := affectState(t, tasks, chat)
	if owner.Revision != 2 {
		t.Fatal("another actor changed owner state", owner)
	}
	other, err := tasks.ExtensionState(t.Context(), affectExtensionID, chat, "maya", "telegram:user:987654321")
	if err != nil {
		t.Fatal(err)
	}
	var otherState struct {
		State struct {
			Revision int64 `json:"revision"`
		} `json:"state"`
	}
	if json.Unmarshal(other, &otherState) != nil || otherState.State.Revision != 2 {
		t.Fatal(string(other))
	}
	group, err := tasks.NewSpace(t.Context(), "Group isolation", SpaceSourceLocal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	groupChat, err := tasks.NewConversationInSpace(t.Context(), group.ID, "isolated group")
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.SetSpacePermission(t.Context(), group.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err = tasks.SelectCapabilities(t.Context(), groupChat.ID, CapabilitySelection{Extensions: []string{affectExtensionID}, Variants: map[string]string{affectExtensionID: "maya"}}); err != nil {
		t.Fatal(err)
	}
	leaked := false
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "Precise engineering partner") {
				leaked = true
			}
		}
		return ModelResponse{Content: "no private role"}, nil
	})
	if _, err = tasks.Submit(t.Context(), groupChat.ID, "group-input", "hello"); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("unauthorized space received private character")
	}
	raw, err := tasks.ExtensionState(t.Context(), affectExtensionID, groupChat.ID, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Choices []any `json:"choices"`
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Choices) != 0 {
		t.Fatal("unauthorized character discoverable", string(raw))
	}
}
