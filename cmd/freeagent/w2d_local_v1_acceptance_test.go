package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w2dLocalV1MCPInstance = "w2d-local-v1-mcp"

// TestW2DLocalV1UnifiedModuleAssembly is the concentrated, network-free W2-D
// acceptance proof. It deliberately uses only the production module-apply,
// assembly, chat, Gateway, backup and history paths that the narrower tests
// already exercise independently. Profile selection proves module assembly;
// the frozen Action authority separately proves Workspace access. There is no
// Workspace-to-Profile mapping contract in local-v1.
func TestW2DLocalV1UnifiedModuleAssembly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	initializeMultiProfileForModuleApplyV1(t, root, databasePath, artifactRoot)

	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	skill := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplySkillID,
		moduleApplySkillInstance,
	)
	knowledge := newModuleApplyKnowledgeFixtureV1(t)
	memory := newModuleApplyMemoryFixtureV1(t)
	mcp := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "mcp-fixture"),
		eventPath,
	)

	pointer := uint64(1)
	apply := func(
		name string,
		plan []byte,
		artifactDirectory string,
		artifactGrant string,
		profileID string,
		instanceID string,
		port moduleapi.PortRef,
	) {
		t.Helper()
		path := writeModuleApplyPlanFixtureV1(
			t,
			filepath.Join(root, name+".json"),
			plan,
		)
		result, err := runModuleApplyFixtureV1(
			databasePath,
			artifactRoot,
			path,
			artifactDirectory,
			artifactGrant,
		)
		if err != nil || result.Status != moduleApplyStatusApplied ||
			result.PointerRevision != pointer+1 ||
			result.BindingTarget.ProfileID != profileID || result.InstanceID != instanceID ||
			result.Port != port {
			t.Fatalf("%s apply=%+v error=%v", name, result, err)
		}
		pointer++
	}

	apply(
		"enable-role-a",
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t, role, moduleApplyRoleProfile, pointer, 0,
		),
		role.ArtifactDirectory,
		"",
		moduleApplyRoleProfile,
		role.InstanceID,
		productionContextPort,
	)
	knowledgePlan := newEnabledModuleApplyKnowledgePlanWithPolicyV1(
		t,
		knowledge,
		pointer,
		knowledge.Source,
		knowledge.Source,
		moduleapi.KnowledgeScopeRuleV1{
			TenantID:     defaultTenantID,
			WorkspaceID:  moduleApplyRoleWorkspace,
			AgentID:      defaultAgentID,
			TaskInputRef: "*",
		},
		2,
	)
	apply(
		"enable-knowledge-a",
		w2dLocalV1PlanForProfile(t, knowledgePlan, moduleApplyRoleProfile),
		knowledge.ArtifactDirectory,
		"",
		moduleApplyRoleProfile,
		moduleApplyKnowledgeInstanceIDV1,
		productionContextPort,
	)
	memoryPlan := newEnabledModuleApplyMemoryPlanForInstanceV1(
		t,
		memory,
		pointer,
		moduleApplyMemoryInstanceIDV1,
		3,
		defaultAgentID,
		[]string{moduleApplyRoleWorkspace},
	)
	apply(
		"enable-memory-a",
		w2dLocalV1PlanForProfile(t, memoryPlan, moduleApplyRoleProfile),
		memory.ArtifactDirectory,
		"",
		moduleApplyRoleProfile,
		moduleApplyMemoryInstanceIDV1,
		productionContextPort,
	)
	apply(
		"enable-skill-b",
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t, skill, moduleApplySkillProfile, pointer, 0,
		),
		skill.ArtifactDirectory,
		"",
		moduleApplySkillProfile,
		skill.InstanceID,
		productionContextPort,
	)
	apply(
		"enable-mcp-b",
		w2dLocalV1ActionPlanForWorkspace(
			t,
			newNamedEnabledModuleApplyPlanV1(
				t,
				mcp,
				moduleApplySkillProfile,
				w2dLocalV1MCPInstance,
				"text.stats",
				pointer,
				0,
			),
			moduleApplySkillWorkspace,
		),
		mcp.ArtifactDirectory,
		mcp.ArtifactDigest,
		moduleApplySkillProfile,
		w2dLocalV1MCPInstance,
		productionActionPort,
	)
	if pointer != 6 {
		t.Fatalf("applied pointer=%d want 6", pointer)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	appliedBasis, appliedControl, appliedCatalog := loadModuleApplyPublishedStateV1(
		t,
		databasePath,
	)
	if appliedBasis.PointerRevision != pointer {
		t.Fatalf("applied basis=%+v", appliedBasis)
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open unified local-v1 composition: %v", err)
	}

	// Profile B remains selectable with Workspace A because local-v1 does not
	// define a Workspace-to-Profile mapping. The exact Workspace B Action
	// authority is the independent execution boundary and must fail closed
	// before the MCP process receives any external call.
	deniedInput := moduleApplyChatInputV1(
		"w2d-local-v1-workspace-a-skill-profile",
		"one MCP tool call from Workspace A under the Skill Profile",
	)
	deniedInput.WorkspaceID = moduleApplyRoleWorkspace
	deniedInput.ProfileID = moduleApplySkillProfile
	deniedResult, deniedErr := composition.chat.Chat(ctx, deniedInput)
	if deniedErr == nil ||
		!errors.Is(deniedErr, actionmaterializer.ErrInvalidMaterialization) ||
		!strings.Contains(deniedErr.Error(), "exact tenant/workspace scope") ||
		deniedResult.AdmissionCreated || deniedResult.RunID != "" ||
		deniedResult.TerminalResult != nil || deniedResult.Reply != "" ||
		deniedResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("Workspace A Skill Profile authority denial=%+v error=%v", deniedResult, deniedErr)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	roleMessage := "How does FreeAgent shared knowledge stay independent from Agent and Workspace definitions?"
	roleInput := moduleApplyChatInputV1("w2d-local-v1-workspace-a", roleMessage)
	roleInput.WorkspaceID = moduleApplyRoleWorkspace
	roleInput.ProfileID = moduleApplyRoleProfile
	roleResult, roleErr := composition.chat.Chat(ctx, roleInput)
	if roleErr != nil || !roleResult.AdmissionCreated ||
		roleResult.TerminalResult == nil || roleResult.Reply != roleMessage ||
		roleResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("Workspace A chat=%+v error=%v", roleResult, roleErr)
	}
	roleDispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		roleResult.TerminalResult.AttemptID,
	)
	if err != nil || roleDispatch.Attempt.RunID != roleResult.RunID ||
		roleDispatch.Attempt.SourceDispatchAttemptID != "" ||
		roleDispatch.Attempt.ContextCompilation == nil {
		_ = composition.Close()
		t.Fatalf("Workspace A model dispatch=%+v error=%v", roleDispatch, err)
	}
	roleRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		roleDispatch.Attempt.Request.CanonicalBytes,
	)
	if err != nil || !w2dLocalV1MessagesContain(roleRequest.Messages, moduleApplyRoleText) ||
		w2dLocalV1MessagesContain(roleRequest.Messages, moduleApplySkillText) ||
		len(roleRequest.Actions) != 0 {
		_ = composition.Close()
		t.Fatalf("Workspace A model request=%+v error=%v", roleRequest, err)
	}
	roleCompilation, err := corecontract.RestoreContextCompilationV1(
		roleDispatch.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || len(roleCompilation.KnowledgeRetrievals) != 1 ||
		len(roleCompilation.MemoryReads) != 1 ||
		len(roleCompilation.KnowledgeReuses) != 0 ||
		len(roleCompilation.KnowledgeShortcuts) != 0 {
		_ = composition.Close()
		t.Fatalf("Workspace A compilation=%+v error=%v", roleCompilation, err)
	}
	retrieval := roleCompilation.KnowledgeRetrievals[0]
	memoryRead := roleCompilation.MemoryReads[0]
	if retrieval.BindingIndex != 2 || retrieval.RequestDigest == "" ||
		retrieval.OutputDigest == "" ||
		retrieval.Scope.TenantID != defaultTenantID ||
		retrieval.Scope.Workspace.ID != moduleApplyRoleWorkspace ||
		retrieval.Scope.Agent.ID != defaultAgentID ||
		memoryRead.BindingIndex != 3 || memoryRead.Snapshot.Revision != 1 ||
		memoryRead.RequestDigest == "" || memoryRead.OutputDigest == "" ||
		memoryRead.Scope.TenantID != defaultTenantID ||
		memoryRead.Scope.Workspace.ID != moduleApplyRoleWorkspace ||
		memoryRead.Scope.Agent.ID != defaultAgentID {
		_ = composition.Close()
		t.Fatalf("Workspace A dynamic evidence retrieval=%+v memory=%+v", retrieval, memoryRead)
	}
	memoryAfterRole, err := composition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	)
	if err != nil || memoryAfterRole.SnapshotRef.Revision != 2 ||
		memoryAfterRole.SourceAttemptID != roleResult.TerminalResult.AttemptID {
		_ = composition.Close()
		t.Fatalf("Memory head after Workspace A=%+v error=%v", memoryAfterRole, err)
	}

	skillMessage := "one MCP tool call from isolated Workspace B"
	skillInput := moduleApplyChatInputV1("w2d-local-v1-workspace-b", skillMessage)
	skillInput.WorkspaceID = moduleApplySkillWorkspace
	skillInput.ProfileID = moduleApplySkillProfile
	skillResult, skillErr := composition.chat.Chat(ctx, skillInput)
	if skillErr != nil || !skillResult.AdmissionCreated ||
		skillResult.TerminalResult == nil ||
		skillResult.Reply != "text.stats completed." || skillResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("Workspace B Action chat=%+v error=%v", skillResult, skillErr)
	}
	skillDispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		skillResult.TerminalResult.AttemptID,
	)
	if err != nil || skillDispatch.Attempt.RunID != skillResult.RunID ||
		skillDispatch.Attempt.SourceDispatchAttemptID == "" ||
		skillDispatch.Attempt.ContextCompilation != nil {
		_ = composition.Close()
		t.Fatalf("Workspace B final model dispatch=%+v error=%v", skillDispatch, err)
	}
	skillRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		skillDispatch.Attempt.Request.CanonicalBytes,
	)
	if err != nil || !w2dLocalV1MessagesContain(skillRequest.Messages, moduleApplySkillText) ||
		w2dLocalV1MessagesContain(skillRequest.Messages, moduleApplyRoleText) ||
		w2dLocalV1MessagesContain(
			skillRequest.Messages,
			"shared knowledge remains independent from Agent and Workspace",
		) {
		_ = composition.Close()
		t.Fatalf("Workspace B model request=%+v error=%v", skillRequest, err)
	}
	actionDispatch, err := composition.store.GetActionDispatchRecord(
		ctx,
		skillDispatch.Attempt.SourceDispatchAttemptID,
	)
	if err != nil || actionDispatch.Attempt.State != currentstore.ActionDispatchSucceeded ||
		actionDispatch.Result == nil || actionDispatch.ProviderReceipt == nil ||
		actionDispatch.Attempt.ProviderActionID != "text.stats" ||
		actionDispatch.Attempt.Binding.Provider.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		actionDispatch.Attempt.Binding.Provider.AdapterIdentity != mcpstdio.AdapterIdentityV1 {
		_ = composition.Close()
		t.Fatalf("Workspace B Gateway Action=%+v error=%v", actionDispatch, err)
	}
	skillSourceDispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		actionDispatch.Attempt.SourceModelAttemptID,
	)
	if err != nil || skillSourceDispatch.Attempt.ContextCompilation == nil {
		_ = composition.Close()
		t.Fatalf("Workspace B source model dispatch=%+v error=%v", skillSourceDispatch, err)
	}
	skillCompilation, err := corecontract.RestoreContextCompilationV1(
		skillSourceDispatch.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || len(skillCompilation.KnowledgeRetrievals) != 0 ||
		len(skillCompilation.KnowledgeReuses) != 0 ||
		len(skillCompilation.KnowledgeShortcuts) != 0 ||
		len(skillCompilation.MemoryReads) != 0 {
		_ = composition.Close()
		t.Fatalf("Workspace B source dynamic evidence=%+v error=%v", skillCompilation, err)
	}
	memoryAfterAction, err := composition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	)
	if err != nil || !reflect.DeepEqual(memoryAfterAction, memoryAfterRole) {
		_ = composition.Close()
		t.Fatalf(
			"Workspace B touched Workspace A Memory: before=%+v after=%+v error=%v",
			memoryAfterRole,
			memoryAfterAction,
			err,
		)
	}
	if closeErr := composition.Close(); closeErr != nil {
		t.Fatalf("close unified source composition: %v", closeErr)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	historyA := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(databasePath, appliedBasis, moduleApplyRoleProfile),
	)
	historyB := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(databasePath, appliedBasis, moduleApplySkillProfile),
	)
	var appliedHistoryA moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, historyA, &appliedHistoryA)
	var appliedHistoryB moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, historyB, &appliedHistoryB)
	for _, check := range []struct {
		name     string
		history  moduleHistoryResultV1
		included []string
		excluded []string
	}{
		{
			name:    "Workspace A Profile",
			history: appliedHistoryA,
			included: []string{
				role.InstanceID,
				moduleApplyKnowledgeInstanceIDV1,
				moduleApplyMemoryInstanceIDV1,
			},
			excluded: []string{skill.InstanceID, w2dLocalV1MCPInstance},
		},
		{
			name:     "Workspace B Profile",
			history:  appliedHistoryB,
			included: []string{skill.InstanceID, w2dLocalV1MCPInstance},
			excluded: []string{
				role.InstanceID,
				moduleApplyKnowledgeInstanceIDV1,
				moduleApplyMemoryInstanceIDV1,
			},
		},
	} {
		for _, instanceID := range check.included {
			if !moduleOperatorContainsInstanceV1(check.history.Bindings, instanceID) {
				t.Fatalf("%s history omitted %s", check.name, instanceID)
			}
		}
		for _, instanceID := range check.excluded {
			if moduleOperatorContainsInstanceV1(check.history.Bindings, instanceID) {
				t.Fatalf("%s history leaked %s", check.name, instanceID)
			}
		}
	}

	bundlePath := filepath.Join(root, "w2d-local-v1.bundle")
	createAndVerifyModuleApplyBundleV1(t, databasePath, artifactRoot, bundlePath)
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "restored"),
	)
	restoredBasis, restoredControl, restoredCatalog := loadModuleApplyPublishedStateV1(
		t,
		restoredDatabase,
	)
	if restoredBasis != appliedBasis ||
		!reflect.DeepEqual(restoredControl, appliedControl) ||
		!reflect.DeepEqual(restoredCatalog, appliedCatalog) {
		t.Fatalf("backup/restore changed current module refs")
	}
	if restoredHistory := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			restoredDatabase,
			appliedBasis,
			moduleApplyRoleProfile,
		),
	); !bytes.Equal(restoredHistory, historyA) {
		t.Fatal("Workspace A module history changed across backup/restore")
	}
	if restoredHistory := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			restoredDatabase,
			appliedBasis,
			moduleApplySkillProfile,
		),
	); !bytes.Equal(restoredHistory, historyB) {
		t.Fatal("Workspace B module history changed across backup/restore")
	}

	restoredComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open restored unified composition: %v", err)
	}
	restoredOriginalRole, roleReadErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		roleResult.TerminalResult.AttemptID,
	)
	restoredOriginalSkill, skillReadErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		skillResult.TerminalResult.AttemptID,
	)
	restoredOriginalSkillSource, skillSourceReadErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		actionDispatch.Attempt.SourceModelAttemptID,
	)
	restoredOriginalAction, actionReadErr := restoredComposition.store.GetActionDispatchRecord(
		ctx,
		actionDispatch.Attempt.AttemptID,
	)
	restoredMemoryBeforeChat, memoryReadErr := restoredComposition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	)
	if joined := errors.Join(
		roleReadErr,
		skillReadErr,
		skillSourceReadErr,
		actionReadErr,
		memoryReadErr,
	); joined != nil {
		_ = restoredComposition.Close()
		t.Fatalf("read restored closures: %v", joined)
	}
	if !reflect.DeepEqual(restoredOriginalRole, roleDispatch) ||
		!reflect.DeepEqual(restoredOriginalSkill, skillDispatch) ||
		!reflect.DeepEqual(restoredOriginalSkillSource, skillSourceDispatch) ||
		!reflect.DeepEqual(restoredOriginalAction, actionDispatch) ||
		!reflect.DeepEqual(restoredMemoryBeforeChat, memoryAfterRole) {
		_ = restoredComposition.Close()
		t.Fatal("backup/restore changed Run, Compilation, Attempt, Action or Memory closure")
	}

	restoredRoleMessage := "verify Knowledge and Memory remain live after restore"
	restoredRoleInput := moduleApplyChatInputV1(
		"w2d-local-v1-restored-workspace-a",
		restoredRoleMessage,
	)
	restoredRoleInput.WorkspaceID = moduleApplyRoleWorkspace
	restoredRoleInput.ProfileID = moduleApplyRoleProfile
	restoredRoleResult, restoredRoleErr := restoredComposition.chat.Chat(ctx, restoredRoleInput)
	if restoredRoleErr != nil || !restoredRoleResult.AdmissionCreated ||
		restoredRoleResult.TerminalResult == nil ||
		restoredRoleResult.Reply != restoredRoleMessage || restoredRoleResult.FailureCode != "" {
		_ = restoredComposition.Close()
		t.Fatalf("restored Workspace A chat=%+v error=%v", restoredRoleResult, restoredRoleErr)
	}
	restoredRoleDispatch, restoredRoleReadErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		restoredRoleResult.TerminalResult.AttemptID,
	)
	if restoredRoleReadErr != nil ||
		restoredRoleDispatch.Attempt.RunID != restoredRoleResult.RunID ||
		restoredRoleDispatch.Attempt.SourceDispatchAttemptID != "" ||
		restoredRoleDispatch.Attempt.ContextCompilation == nil {
		_ = restoredComposition.Close()
		t.Fatalf(
			"restored Workspace A dispatch=%+v error=%v",
			restoredRoleDispatch,
			restoredRoleReadErr,
		)
	}
	restoredRoleCompilation, compilationErr := corecontract.RestoreContextCompilationV1(
		restoredRoleDispatch.Attempt.ContextCompilation.CanonicalBytes,
	)
	if compilationErr != nil || len(restoredRoleCompilation.KnowledgeRetrievals) != 1 ||
		len(restoredRoleCompilation.KnowledgeReuses) != 0 ||
		len(restoredRoleCompilation.KnowledgeShortcuts) != 0 ||
		len(restoredRoleCompilation.MemoryReads) != 1 {
		_ = restoredComposition.Close()
		t.Fatalf(
			"restored Workspace A compilation=%+v error=%v",
			restoredRoleCompilation,
			compilationErr,
		)
	}
	restoredRetrieval := restoredRoleCompilation.KnowledgeRetrievals[0]
	restoredMemoryRead := restoredRoleCompilation.MemoryReads[0]
	if restoredRetrieval.BindingIndex != 2 ||
		restoredRetrieval.Scope.Workspace.ID != moduleApplyRoleWorkspace ||
		restoredRetrieval.Scope.Agent.ID != defaultAgentID ||
		restoredMemoryRead.BindingIndex != 3 ||
		restoredMemoryRead.Snapshot.Revision != restoredMemoryBeforeChat.SnapshotRef.Revision ||
		restoredMemoryRead.Scope.Workspace.ID != moduleApplyRoleWorkspace ||
		restoredMemoryRead.Scope.Agent.ID != defaultAgentID {
		_ = restoredComposition.Close()
		t.Fatalf(
			"restored Workspace A evidence retrieval=%+v memory=%+v",
			restoredRetrieval,
			restoredMemoryRead,
		)
	}
	memoryAfterRestoredRole, memoryAfterRestoreErr := restoredComposition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	)
	roleAfterRestoredChat, oldRoleErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		roleResult.TerminalResult.AttemptID,
	)
	skillAfterRestoredChat, oldSkillErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		skillResult.TerminalResult.AttemptID,
	)
	skillSourceAfterRestoredChat, oldSkillSourceErr := restoredComposition.store.GetModelDispatchRecord(
		ctx,
		actionDispatch.Attempt.SourceModelAttemptID,
	)
	actionAfterRestoredChat, oldActionErr := restoredComposition.store.GetActionDispatchRecord(
		ctx,
		actionDispatch.Attempt.AttemptID,
	)
	closeErr := restoredComposition.Close()
	if joined := errors.Join(
		memoryAfterRestoreErr,
		oldRoleErr,
		oldSkillErr,
		oldSkillSourceErr,
		oldActionErr,
		closeErr,
	); joined != nil {
		t.Fatalf("read post-restore live closures: %v", joined)
	}
	if memoryAfterRestoredRole.SnapshotRef.Revision !=
		restoredMemoryBeforeChat.SnapshotRef.Revision+1 ||
		memoryAfterRestoredRole.SourceAttemptID != restoredRoleResult.TerminalResult.AttemptID {
		t.Fatalf("restored Workspace A Memory head=%+v", memoryAfterRestoredRole)
	}
	if !reflect.DeepEqual(roleAfterRestoredChat, roleDispatch) ||
		!reflect.DeepEqual(skillAfterRestoredChat, skillDispatch) ||
		!reflect.DeepEqual(skillSourceAfterRestoredChat, skillSourceDispatch) ||
		!reflect.DeepEqual(actionAfterRestoredChat, actionDispatch) {
		t.Fatal("restored live Chat changed pre-backup Run, Attempt, Compilation or Action facts")
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	disable := func(
		name string,
		profileID string,
		instanceID string,
		port moduleapi.PortRef,
	) {
		t.Helper()
		plan := w2dLocalV1DisabledPlan(
			t,
			profileID,
			instanceID,
			port,
			pointer,
		)
		path := writeModuleApplyPlanFixtureV1(
			t,
			filepath.Join(root, name+".json"),
			plan,
		)
		result, applyErr := runModuleApplyFixtureV1(
			restoredDatabase,
			restoredArtifacts,
			path,
			"",
			"",
		)
		if applyErr != nil || result.Status != moduleApplyStatusApplied ||
			result.DesiredState != moduleApplyDisabledV1 ||
			result.PointerRevision != pointer+1 || result.BindingTarget.ProfileID != profileID ||
			result.InstanceID != instanceID || result.Port != port {
			t.Fatalf("%s disable=%+v error=%v", name, result, applyErr)
		}
		pointer++
	}
	disable("disable-role-a", moduleApplyRoleProfile, role.InstanceID, productionContextPort)
	disable(
		"disable-knowledge-a",
		moduleApplyRoleProfile,
		moduleApplyKnowledgeInstanceIDV1,
		productionContextPort,
	)
	disable(
		"disable-memory-a",
		moduleApplyRoleProfile,
		moduleApplyMemoryInstanceIDV1,
		productionContextPort,
	)
	disable("disable-skill-b", moduleApplySkillProfile, skill.InstanceID, productionContextPort)
	disable(
		"disable-mcp-b",
		moduleApplySkillProfile,
		w2dLocalV1MCPInstance,
		productionActionPort,
	)
	if pointer != 11 {
		t.Fatalf("disabled pointer=%d want 11", pointer)
	}

	if historical := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			restoredDatabase,
			appliedBasis,
			moduleApplyRoleProfile,
		),
	); !bytes.Equal(historical, historyA) {
		t.Fatal("Disable changed Workspace A historical bindings")
	}
	if historical := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			restoredDatabase,
			appliedBasis,
			moduleApplySkillProfile,
		),
	); !bytes.Equal(historical, historyB) {
		t.Fatal("Disable changed Workspace B historical bindings")
	}
	disabledBasis, _, _ := loadModuleApplyPublishedStateV1(t, restoredDatabase)
	for _, profileID := range []string{moduleApplyRoleProfile, moduleApplySkillProfile} {
		payload := runModuleOperatorCommandV1(
			t,
			moduleHistoryCommandV1(restoredDatabase, disabledBasis, profileID),
		)
		var history moduleHistoryResultV1
		decodeModuleOperatorOutputV1(t, payload, &history)
		for _, instanceID := range []string{
			role.InstanceID,
			moduleApplyKnowledgeInstanceIDV1,
			moduleApplyMemoryInstanceIDV1,
			skill.InstanceID,
			w2dLocalV1MCPInstance,
		} {
			if moduleOperatorContainsInstanceV1(history.Bindings, instanceID) {
				t.Fatalf("disabled history for %s retained %s", profileID, instanceID)
			}
		}
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	disabledComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled unified composition: %v", err)
	}
	for _, test := range []struct {
		name        string
		workspaceID string
		profileID   string
		requestID   string
		message     string
	}{
		{
			name:        "Workspace A",
			workspaceID: moduleApplyRoleWorkspace,
			profileID:   moduleApplyRoleProfile,
			requestID:   "w2d-local-v1-disabled-a",
			message:     "pure chat in Workspace A after disable",
		},
		{
			name:        "Workspace B",
			workspaceID: moduleApplySkillWorkspace,
			profileID:   moduleApplySkillProfile,
			requestID:   "w2d-local-v1-disabled-b",
			message:     "pure chat in Workspace B after disable",
		},
	} {
		input := moduleApplyChatInputV1(test.requestID, test.message)
		input.WorkspaceID = test.workspaceID
		input.ProfileID = test.profileID
		result, chatErr := disabledComposition.chat.Chat(ctx, input)
		if chatErr != nil || !result.AdmissionCreated || result.TerminalResult == nil ||
			result.Reply != test.message || result.FailureCode != "" {
			_ = disabledComposition.Close()
			t.Fatalf("%s disabled Pure Chat=%+v error=%v", test.name, result, chatErr)
		}
		dispatch, readErr := disabledComposition.store.GetModelDispatchRecord(
			ctx,
			result.TerminalResult.AttemptID,
		)
		if readErr != nil || dispatch.Attempt.SourceDispatchAttemptID != "" ||
			dispatch.Attempt.ContextCompilation != nil {
			_ = disabledComposition.Close()
			t.Fatalf("%s disabled dispatch=%+v error=%v", test.name, dispatch, readErr)
		}
		request, restoreErr := moduleapi.RestoreModelGenerateRequestV1(
			dispatch.Attempt.Request.CanonicalBytes,
		)
		if restoreErr != nil || len(request.Actions) != 0 ||
			w2dLocalV1MessagesContain(request.Messages, moduleApplyRoleText) ||
			w2dLocalV1MessagesContain(request.Messages, moduleApplySkillText) {
			_ = disabledComposition.Close()
			t.Fatalf("%s disabled request=%+v error=%v", test.name, request, restoreErr)
		}
	}
	afterDisabledMemory, memoryErr := disabledComposition.store.GetCurrentAgentMemory(
		ctx,
		defaultTenantID,
		defaultAgentID,
	)
	afterDisabledRole, roleErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		roleResult.TerminalResult.AttemptID,
	)
	afterDisabledSkill, skillErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		skillResult.TerminalResult.AttemptID,
	)
	afterDisabledSkillSource, skillSourceErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		actionDispatch.Attempt.SourceModelAttemptID,
	)
	afterDisabledAction, actionErr := disabledComposition.store.GetActionDispatchRecord(
		ctx,
		actionDispatch.Attempt.AttemptID,
	)
	afterDisabledRestoredRole, afterRestoredRoleErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		restoredRoleResult.TerminalResult.AttemptID,
	)
	closeErr = disabledComposition.Close()
	if joined := errors.Join(
		memoryErr,
		roleErr,
		skillErr,
		skillSourceErr,
		actionErr,
		afterRestoredRoleErr,
		closeErr,
	); joined != nil {
		t.Fatalf("read disabled historical closures: %v", joined)
	}
	if !reflect.DeepEqual(afterDisabledMemory, memoryAfterRestoredRole) ||
		!reflect.DeepEqual(afterDisabledRole, roleDispatch) ||
		!reflect.DeepEqual(afterDisabledSkill, skillDispatch) ||
		!reflect.DeepEqual(afterDisabledSkillSource, skillSourceDispatch) ||
		!reflect.DeepEqual(afterDisabledAction, actionDispatch) ||
		!reflect.DeepEqual(afterDisabledRestoredRole, restoredRoleDispatch) {
		t.Fatal("Disable or later Pure Chat changed historical local-v1 facts")
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func w2dLocalV1PlanForProfile(
	t *testing.T,
	canonical []byte,
	profileID string,
) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatalf("decode local-v1 apply plan: %v", err)
	}
	bindingTargetJSON, err := json.Marshal(
		moduleApplyProfileBindingTargetTestValue(profileID),
	)
	if err != nil {
		t.Fatalf("encode local-v1 Profile BindingTarget: %v", err)
	}
	object["binding_target"] = bindingTargetJSON
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode local-v1 apply plan: %v", err)
	}
	canonical, err = moduleapi.CanonicalJSON(payload)
	if err != nil {
		t.Fatalf("canonicalize local-v1 apply plan: %v", err)
	}
	return canonical
}

func w2dLocalV1ActionPlanForWorkspace(
	t *testing.T,
	canonical []byte,
	workspaceID string,
) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatalf("decode local-v1 Action apply plan: %v", err)
	}
	var binding map[string]json.RawMessage
	if err := json.Unmarshal(object["binding"], &binding); err != nil {
		t.Fatalf("decode local-v1 Action binding: %v", err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{workspaceID},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatalf("freeze local-v1 exact Workspace Action authority: %v", err)
	}
	binding["authority_ceiling"] = json.RawMessage(authority)
	bindingJSON, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("encode local-v1 Action binding: %v", err)
	}
	bindingCanonical, err := moduleapi.CanonicalJSON(bindingJSON)
	if err != nil {
		t.Fatalf("canonicalize local-v1 Action binding: %v", err)
	}
	object["binding"] = json.RawMessage(bindingCanonical)
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode local-v1 Action apply plan: %v", err)
	}
	canonical, err = moduleapi.CanonicalJSON(payload)
	if err != nil {
		t.Fatalf("canonicalize local-v1 Action apply plan: %v", err)
	}
	return canonical
}

func w2dLocalV1DisabledPlan(
	t *testing.T,
	profileID string,
	instanceID string,
	port moduleapi.PortRef,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":               instanceID,
		"port": map[string]any{
			"name":          port.Name,
			"exact_version": port.ExactVersion,
		},
	})
}

func w2dLocalV1MessagesContain(
	messages []moduleapi.ModelMessageV1,
	fragment string,
) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}
