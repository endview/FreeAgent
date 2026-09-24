package bootstrapseed_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
)

func TestAdditionalDefinitionsImportAndCloseComposite(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedObject(t, canonical)
	addThreeDefinitionClosure(seed)
	seed["model_profile"] = exactModelProfileSeed(t, seed)
	seed["composite_agents"] = []any{
		map[string]any{
			"schema_version":         controlcontract.CompositeAgentSchemaVersionV1,
			"agent_id":               "assistant",
			"coordinator_profile_id": "pure-chat",
			"members": []any{
				map[string]any{
					"slot_id":             "backend",
					"agent_id":            "backend-specialist",
					"profile_id":          "backend-profile",
					"focus_id":            "backend",
					"weight_basis_points": 5000,
				},
				map[string]any{
					"slot_id":             "frontend",
					"agent_id":            "frontend-specialist",
					"profile_id":          "frontend-profile",
					"focus_id":            "frontend",
					"weight_basis_points": 5000,
				},
			},
		},
	}

	prepared, err := bootstrapseed.Prepare(
		canonicalObject(t, seed),
		filepath.Dir(seedPath),
	)
	if err != nil {
		t.Fatalf("Prepare() with additional definitions error = %v", err)
	}
	if got := prepared.DefaultAssembly(); got != (bootstrapseed.DefaultAssembly{
		TenantID:    "default",
		WorkspaceID: "local-chat",
		AgentID:     "assistant",
		ProfileID:   "pure-chat",
	}) {
		t.Fatalf("DefaultAssembly() = %+v", got)
	}

	store := newStore(t)
	if _, err := prepared.Import(
		context.Background(),
		store,
		localResolver(t, prepared.ModelAssertion(), true),
	); err != nil {
		t.Fatalf("Import() with additional definitions error = %v", err)
	}
	_, control, _, err := store.LoadPublishedBasis(context.Background(), "default")
	if err != nil {
		t.Fatalf("LoadPublishedBasis() error = %v", err)
	}
	if len(control.Agents) != 3 || len(control.Workspaces) != 3 ||
		len(control.Profiles) != 3 || len(control.CompositeAgents) != 1 {
		t.Fatalf("published multi-definition closure = %+v", control)
	}
	composite := control.CompositeAgents[0]
	if composite.AgentID != "assistant" ||
		composite.CoordinatorProfileID != "pure-chat" ||
		len(composite.Members) != 2 ||
		composite.Members[0].SlotID != "backend" ||
		composite.Members[1].SlotID != "frontend" {
		t.Fatalf("published Composite Agent = %+v", composite)
	}
	for index := 1; index < len(control.Profiles); index++ {
		if !reflect.DeepEqual(control.Profiles[0].Bindings, control.Profiles[index].Bindings) ||
			!reflect.DeepEqual(control.Profiles[0].ModelProfile, control.Profiles[index].ModelProfile) {
			t.Fatalf(
				"Profile %q did not reuse the exact model/module bindings",
				control.Profiles[index].Profile.ID,
			)
		}
	}
	if control.Profiles[0].ModelProfile == nil {
		t.Fatal("additional Profiles did not reuse the explicit ModelProfile")
	}
}

func TestAdditionalDefinitionsRejectDuplicatesUnknownPoliciesAndOpenCompositeRefs(
	t *testing.T,
) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "duplicate Agent ID",
			mutate: func(seed map[string]any) {
				definitions(seed)["additional_agents"] = []any{
					definition("assistant", "Duplicate Agent"),
				}
			},
		},
		{
			name: "duplicate Workspace ID",
			mutate: func(seed map[string]any) {
				definitions(seed)["additional_workspaces"] = []any{
					workspaceDefinition("local-chat", "Duplicate Workspace", "budget-local-pure-chat"),
				}
			},
		},
		{
			name: "duplicate Profile ID",
			mutate: func(seed map[string]any) {
				definitions(seed)["additional_profiles"] = []any{
					profileDefinition("pure-chat", "Duplicate Profile", "context-pure-chat"),
				}
			},
		},
		{
			name: "unknown Profile policy",
			mutate: func(seed map[string]any) {
				definitions(seed)["additional_profiles"] = []any{
					profileDefinition("frontend-profile", "Frontend", "missing-context"),
				}
			},
		},
		{
			name: "Composite member Agent outside definitions",
			mutate: func(seed map[string]any) {
				addThreeDefinitionClosure(seed)
				seed["composite_agents"] = []any{compositeDefinition(
					"missing-agent",
					"backend-specialist",
				)}
			},
		},
		{
			name: "Composite member Profile outside definitions",
			mutate: func(seed map[string]any) {
				addThreeDefinitionClosure(seed)
				composite := compositeDefinition(
					"frontend-specialist",
					"backend-specialist",
				)
				members := composite["members"].([]any)
				members[0].(map[string]any)["profile_id"] = "missing-profile"
				seed["composite_agents"] = []any{composite}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			seed := seedObject(t, canonical)
			test.mutate(seed)
			if _, err := bootstrapseed.Prepare(
				canonicalObject(t, seed),
				filepath.Dir(seedPath),
			); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
				t.Fatalf("Prepare() error = %v, want ErrInvalidSeed", err)
			}
		})
	}
}

func TestAdditionalDefinitionsCloseKnowledgeAndActionAuthorityByExactIDSets(
	t *testing.T,
) {
	t.Run("Knowledge", func(t *testing.T) {
		fixture := newKnowledgeSeedFixture(t)
		definitions(fixture.seed)["additional_agents"] = []any{
			definition("frontend-specialist", "Frontend Specialist"),
		}
		definitions(fixture.seed)["additional_workspaces"] = []any{
			workspaceDefinition("workspace-ui", "UI Workspace", "budget-local-pure-chat"),
		}
		provider := fixture.seed["knowledge_context_providers"].([]any)[0].(map[string]any)
		authority := provider["authority_ceiling"].(map[string]any)
		rule := authority["allowed_scopes"].([]any)[0].(map[string]any)
		rule["agent_id"] = "frontend-specialist"
		rule["workspace_id"] = "workspace-ui"
		if _, err := bootstrapseed.Prepare(
			canonicalObject(t, fixture.seed),
			fixture.artifactBase,
		); err != nil {
			t.Fatalf("Prepare() exact additional Knowledge scope error = %v", err)
		}
		rule["workspace_id"] = "workspace-outside"
		if _, err := bootstrapseed.Prepare(
			canonicalObject(t, fixture.seed),
			fixture.artifactBase,
		); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
			t.Fatalf("Prepare() escaped Knowledge scope error = %v", err)
		}
	})

	t.Run("Action", func(t *testing.T) {
		seedPath := actionExampleSeedPath(t)
		canonical, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatal(err)
		}
		seed := seedObject(t, canonical)
		definitions(seed)["additional_workspaces"] = []any{
			workspaceDefinition("workspace-network", "Network Workspace", "budget-local-pure-chat"),
		}
		provider := seed["action_providers"].([]any)[0].(map[string]any)
		authority := provider["authority_ceiling"].(map[string]any)
		authority["allowed_workspace_ids"] = []any{"workspace-network"}
		if _, err := bootstrapseed.Prepare(
			canonicalObject(t, seed),
			filepath.Dir(seedPath),
		); err != nil {
			t.Fatalf("Prepare() exact additional Action scope error = %v", err)
		}
		authority["allowed_workspace_ids"] = []any{"workspace-outside"}
		if _, err := bootstrapseed.Prepare(
			canonicalObject(t, seed),
			filepath.Dir(seedPath),
		); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
			t.Fatalf("Prepare() escaped Action scope error = %v", err)
		}
	})
}

func TestMemoryBootstrapRejectsAdditionalDefinitions(t *testing.T) {
	seedPath := memoryExampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedObject(t, canonical)
	definitions(seed)["additional_agents"] = []any{
		definition("frontend-specialist", "Frontend Specialist"),
	}
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, seed),
		filepath.Dir(seedPath),
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("Prepare() multi-definition Memory error = %v", err)
	}
}

func definitions(seed map[string]any) map[string]any {
	return seed["definitions"].(map[string]any)
}

func definition(id, name string) map[string]any {
	return map[string]any{
		"id":      id,
		"version": "1",
		"body": map[string]any{
			"name": name,
		},
	}
}

func workspaceDefinition(id, name, _ string) map[string]any {
	return definition(id, name)
}

func profileDefinition(id, name, contextPolicyAlias string) map[string]any {
	result := definition(id, name)
	result["context_policy_alias"] = contextPolicyAlias
	result["scheduling_policy_alias"] = "scheduling-single-member"
	return result
}

func addThreeDefinitionClosure(seed map[string]any) {
	definitions := definitions(seed)
	definitions["additional_agents"] = []any{
		definition("frontend-specialist", "Frontend Specialist"),
		definition("backend-specialist", "Backend Specialist"),
	}
	definitions["additional_workspaces"] = []any{
		workspaceDefinition("workspace-ui", "UI Workspace", "budget-local-pure-chat"),
		workspaceDefinition("workspace-api", "API Workspace", "budget-local-pure-chat"),
	}
	definitions["additional_profiles"] = []any{
		profileDefinition("frontend-profile", "Frontend Profile", "context-pure-chat"),
		profileDefinition("backend-profile", "Backend Profile", "context-pure-chat"),
	}
}

func compositeDefinition(firstAgent, secondAgent string) map[string]any {
	return map[string]any{
		"schema_version":         controlcontract.CompositeAgentSchemaVersionV1,
		"agent_id":               "assistant",
		"coordinator_profile_id": "pure-chat",
		"members": []any{
			map[string]any{
				"slot_id":             "frontend",
				"agent_id":            firstAgent,
				"profile_id":          "frontend-profile",
				"focus_id":            "frontend",
				"weight_basis_points": 5000,
			},
			map[string]any{
				"slot_id":             "backend",
				"agent_id":            secondAgent,
				"profile_id":          "backend-profile",
				"focus_id":            "backend",
				"weight_basis_points": 5000,
			},
		},
	}
}
