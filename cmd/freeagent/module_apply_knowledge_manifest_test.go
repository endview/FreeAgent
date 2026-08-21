package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestClassifyExactKnowledgeManifestV1(t *testing.T) {
	governed := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "freeagent.test.knowledge-shape",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides:             []moduleapi.PortRef{productionContextPort},
		Requires:             []moduleapi.PortRef{productionModelPort},
		RequestedPermissions: []moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
	}

	tests := []struct {
		name      string
		mutate    func(*moduleapi.ModuleManifestV1)
		wantShape knowledgeManifestShapeV1
		wantErr   bool
	}{
		{
			name:      "governed",
			wantShape: knowledgeManifestGovernedV1,
		},
		{
			name: "legacy permissionless",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = nil
				manifest.RequestedPermissions = nil
			},
			wantShape: knowledgeManifestLegacyPermissionlessV1,
		},
		{
			name: "permission without required model",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = nil
			},
			wantErr: true,
		},
		{
			name: "required model without permission",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = nil
			},
			wantErr: true,
		},
		{
			name: "unknown permission",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = []moduleapi.Permission{"knowledge.write"}
			},
			wantErr: true,
		},
		{
			name: "extra permission in wrong order",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = []moduleapi.Permission{
					"network.http",
					moduleapi.PermissionKnowledgeReadV1,
				}
			},
			wantErr: true,
		},
		{
			name: "extra require in wrong order",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = []moduleapi.PortRef{
					productionActionPort,
					productionModelPort,
				}
			},
			wantErr: true,
		},
		{
			name: "extra provided port",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Provides = append(manifest.Provides, productionActionPort)
			},
			wantErr: true,
		},
		{
			name: "wrong runtime mode",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Mode = moduleapi.RuntimeModeRequestRemote
			},
			wantErr: true,
		},
		{
			name: "wrong runtime protocol",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Protocol = moduleapi.RuntimeProtocolStaticV1
			},
			wantErr: true,
		},
		{
			name: "noncanonical entrypoint",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Entrypoint = "content/./source.json"
			},
			wantErr: true,
		},
		{
			name: "entrypoint outside content",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Entrypoint = "source.json"
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneKnowledgeManifestShapeTestV1(governed)
			if test.mutate != nil {
				test.mutate(&manifest)
			}
			shape, err := classifyExactKnowledgeManifestV1(manifest)
			if test.wantErr {
				if err == nil {
					t.Fatalf("classify shape=%q, want error", shape)
				}
				return
			}
			if err != nil || shape != test.wantShape {
				t.Fatalf(
					"classify shape=%q error=%v, want shape=%q",
					shape,
					err,
					test.wantShape,
				)
			}
		})
	}
}

func TestModuleApplyGovernedKnowledgeDryRunApplyAndRuntimeShareShape(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "1.1.0")
	planCanonical := newEnabledModuleApplyKnowledgePlanV1(
		t,
		fixture,
		1,
		moduleapi.KnowledgeScopeRuleV1{
			TenantID:     defaultTenantID,
			WorkspaceID:  defaultWorkspaceID,
			AgentID:      defaultAgentID,
			TaskInputRef: "*",
		},
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-governed-knowledge.json"),
		planCanonical,
	)

	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply {
		t.Fatalf("governed Knowledge dry-run=%+v error=%v", dryRun, err)
	}
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("governed Knowledge apply=%+v error=%v", applied, err)
	}
	beforeRetry := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != applied.PointerRevision ||
		retried.ControlSnapshotID != applied.ControlSnapshotID ||
		retried.CatalogGenerationID != applied.CatalogGenerationID {
		t.Fatalf("governed Knowledge exact retry=%+v error=%v", retried, err)
	}
	afterRetry := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf(
			"governed Knowledge exact retry added state:\nbefore=%+v\nafter=%+v",
			beforeRetry,
			afterRetry,
		)
	}

	_, _, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	entry, found := catalog.FindInstance(moduleApplyKnowledgeInstanceIDV1)
	if !found || !moduleApplyCatalogProvidesMatchPolicyV1(
		entry.Provides,
		moduleApplyLocalPolicyV1{
			Port:        productionContextPort,
			HandlerKind: moduleApplyHandlerKnowledgeContextV1,
		},
	) {
		t.Fatalf("governed Knowledge Catalog entry=%+v found=%v", entry, found)
	}
	invoker, err := loadKnowledgeInvokerFromArtifact(
		context.Background(),
		fixture.ArtifactDigest,
		localKnowledgeAdapterID,
		filepath.Join(artifactRoot, fixture.ArtifactDigest),
		fixture.ArtifactSizeBytes,
		&entry.Activation,
	)
	if err != nil || invoker == nil {
		t.Fatalf("load governed Knowledge runtime invoker=%T error=%v", invoker, err)
	}

	ctx := context.Background()
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open governed Knowledge production composition: %v", err)
	}
	message := "How does FreeAgent shared knowledge stay independent from " +
		"Agent and Workspace definitions?"
	result, chatErr := composition.chat.Chat(
		ctx,
		moduleApplyChatInputV1("governed-knowledge-production-chat", message),
	)
	if chatErr != nil || !result.AdmissionCreated ||
		result.TerminalResult == nil || result.Reply != message ||
		result.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("governed Knowledge production Chat=%+v error=%v", result, chatErr)
	}
	inspectProductionRAGDispatch(
		t,
		composition.store,
		result.RunID,
		result.TerminalResult.AttemptID,
		"governed-module-apply",
		message,
	)
	if err := composition.Close(); err != nil {
		t.Fatalf("close governed Knowledge production composition: %v", err)
	}
}

func TestKnowledgeApplyAndRuntimeRejectUnsupportedManifestShapes(t *testing.T) {
	base := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "freeagent.test.knowledge-invalid-shape",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides:             []moduleapi.PortRef{productionContextPort},
		Requires:             []moduleapi.PortRef{productionModelPort},
		RequestedPermissions: []moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
	}
	tests := []struct {
		name   string
		mutate func(*moduleapi.ModuleManifestV1)
	}{
		{
			name: "missing require",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = nil
			},
		},
		{
			name: "unknown extra permission",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = append(
					manifest.RequestedPermissions,
					moduleapi.Permission("knowledge.write"),
				)
			},
		},
		{
			name: "requires wrong order and extra port",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = []moduleapi.PortRef{
					productionActionPort,
					productionModelPort,
				}
			},
		},
		{
			name: "extra provide",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Provides = append(manifest.Provides, productionActionPort)
			},
		},
		{
			name: "wrong runtime protocol",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Protocol = moduleapi.RuntimeProtocolStaticV1
			},
		},
		{
			name: "noncanonical entrypoint",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Runtime.Entrypoint = "content/./source.json"
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneKnowledgeManifestShapeTestV1(base)
			manifest.Version = "1.0." + string(rune('1'+index))
			test.mutate(&manifest)
			fixture := writeKnowledgeManifestShapeFixtureV1(t, manifest)

			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			planCanonical := newEnabledModuleApplyKnowledgePlanV1(
				t,
				fixture,
				1,
				moduleapi.KnowledgeScopeRuleV1{
					TenantID:     defaultTenantID,
					WorkspaceID:  defaultWorkspaceID,
					AgentID:      defaultAgentID,
					TaskInputRef: "*",
				},
			)
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "invalid-shape.json"),
				planCanonical,
			)
			before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			if result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil {
				t.Fatalf("invalid Knowledge dry-run unexpectedly succeeded: %+v", result)
			}
			if result, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil {
				t.Fatalf("invalid Knowledge apply unexpectedly succeeded: %+v", result)
			}
			after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid Knowledge shape mutated state:\nbefore=%+v\nafter=%+v", before, after)
			}
			if invoker, err := loadKnowledgeInvokerFromArtifact(
				context.Background(),
				fixture.ArtifactDigest,
				localKnowledgeAdapterID,
				fixture.ArtifactDirectory,
				fixture.ArtifactSizeBytes,
				nil,
			); err == nil || invoker != nil {
				t.Fatalf("invalid Knowledge runtime load invoker=%T error=%v", invoker, err)
			}
		})
	}
}

func cloneKnowledgeManifestShapeTestV1(
	manifest moduleapi.ModuleManifestV1,
) moduleapi.ModuleManifestV1 {
	clone := manifest
	clone.Provides = append([]moduleapi.PortRef{}, manifest.Provides...)
	clone.Requires = append([]moduleapi.PortRef{}, manifest.Requires...)
	clone.RequestedPermissions = append(
		[]moduleapi.Permission{},
		manifest.RequestedPermissions...,
	)
	return clone
}

func writeKnowledgeManifestShapeFixtureV1(
	t *testing.T,
	manifest moduleapi.ModuleManifestV1,
) moduleApplyKnowledgeFixtureV1 {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		"freeagent.example.knowledge.shared",
		"1.1.0",
		"content",
		"source.json",
	)
	sourceCanonical, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "content", "source.json"),
		sourceCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return moduleApplyKnowledgeFixtureV1{
		ModuleID:          manifest.ID,
		ExactVersion:      manifest.Version,
		ArtifactDirectory: directory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
		Source:            sourceRef,
	}
}
