package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModuleDryRunApplyConflictAndPreflightOrderParity(t *testing.T) {
	tests := []struct {
		name          string
		plan          func(*testing.T, moduleApplyMCPFixtureV1) []byte
		wantFailure   moduleApplyFailureCodeV1
		missingSource bool
	}{
		{
			name: "pointer conflict wins over missing source",
			plan: func(t *testing.T, fixture moduleApplyMCPFixtureV1) []byte {
				return newEnabledModuleApplyPlanFixtureV1(t, fixture, 2, 0)
			},
			wantFailure:   moduleApplyFailurePointer,
			missingSource: true,
		},
		{
			name: "target conflict after valid source preflight",
			plan: func(t *testing.T, fixture moduleApplyMCPFixtureV1) []byte {
				return newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 1)
			},
			wantFailure: moduleApplyFailureTarget,
		},
		{
			name: "authority failure wins over missing source",
			plan: func(t *testing.T, fixture moduleApplyMCPFixtureV1) []byte {
				return moduleApplyPlanWithUnknownAuthorityWorkspaceV1(
					t,
					newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
				)
			},
			wantFailure:   moduleApplyFailureTarget,
			missingSource: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			eventPath := filepath.Join(root, "mcp-events.log")
			fixture := newModuleApplyMCPFixtureV1(
				t,
				filepath.Join(root, "fixture"),
				eventPath,
			)
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "conflict.json"),
				test.plan(t, fixture),
			)
			artifactDirectory := fixture.ArtifactDirectory
			if test.missingSource {
				artifactDirectory = filepath.Join(root, "source-must-not-be-read")
				if _, err := os.Lstat(artifactDirectory); !os.IsNotExist(err) {
					t.Fatalf("missing-source prerequisite: %v", err)
				}
			}

			assertModuleDryRunApplyFailureParityV1(
				t,
				databasePath,
				artifactRoot,
				planPath,
				artifactDirectory,
				fixture.ArtifactDigest,
				test.wantFailure,
			)
			assertModuleApplyMCPNotStartedV1(t, eventPath)
		})
	}
}

func TestModuleDryRunApplyDuplicateActionFailureParity(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	firstPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "first.json"),
		newNamedEnabledModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			"duplicate-action-existing",
			"duplicate.action",
			1,
			0,
		),
	)
	first, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		firstPlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || first.Status != moduleApplyStatusApplied || first.PointerRevision != 2 {
		t.Fatalf("establish existing Action binding=%+v, %v", first, err)
	}
	duplicatePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "duplicate.json"),
		newNamedEnabledModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			"duplicate-action-candidate",
			"duplicate.action",
			2,
			1,
		),
	)
	assertModuleDryRunApplyFailureParityV1(
		t,
		databasePath,
		artifactRoot,
		duplicatePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		moduleApplyFailureTarget,
	)
	after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if after.PointerRevision != 2 || !after.InstallationExists {
		t.Fatalf("duplicate Action checks changed published state: %+v", after)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func assertModuleDryRunApplyFailureParityV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	grant string,
	want moduleApplyFailureCodeV1,
) {
	t.Helper()
	_, dryRunErr := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		artifactDirectory,
		grant,
	)
	_, applyErr := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		artifactDirectory,
		grant,
	)
	wantDryRun := "freeagent module-dry-run: failed (" + string(want) + ")"
	wantApply := "freeagent module-apply: failed (" + string(want) + ")"
	if dryRunErr == nil || dryRunErr.Error() != wantDryRun {
		t.Fatalf("module-dry-run failure=%v want %q", dryRunErr, wantDryRun)
	}
	if applyErr == nil || applyErr.Error() != wantApply {
		t.Fatalf("module-apply failure=%v want %q", applyErr, wantApply)
	}
}

func moduleApplyPlanWithUnknownAuthorityWorkspaceV1(
	t *testing.T,
	canonical []byte,
) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatalf("decode module apply plan for authority conflict: %v", err)
	}
	binding, ok := value["binding"].(map[string]any)
	if !ok {
		t.Fatal("module apply plan binding is absent")
	}
	authority, ok := binding["authority_ceiling"].(map[string]any)
	if !ok {
		t.Fatal("module apply plan authority is absent")
	}
	authority["allowed_workspace_ids"] = []any{"unknown-workspace"}
	return canonicalModuleApplyPlanTestJSON(t, value)
}
