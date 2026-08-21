package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleDryRunDeclarativeWouldApplyMatchesApplyBasisWithoutWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	fixture.ArtifactDirectory = filepath.Join(root, "role-source")
	copyModuleApplyTestTreeV1(
		t,
		filepath.Join(
			filepath.Dir(exampleSeedPath(t)),
			"bootstrap-artifacts",
			fixture.ModuleID,
			moduleApplyTestVersion,
		),
		fixture.ArtifactDirectory,
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)

	observedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)

	result, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("dry-run declarative ENABLED plan: %v", err)
	}
	assertModuleDryRunIdentityV1(
		t,
		result,
		fixture,
		moduleApplyStatusWouldApply,
		observedBasis,
		moduleDryRunProjectedNotReservedV1,
	)
	if result.Changes.Installation != moduleDryRunInstallationCreateV1 ||
		result.Changes.Activation != moduleDryRunActivationCreateV1 ||
		result.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		result.Changes.Binding.PortBindingIndex == nil ||
		*result.Changes.Binding.PortBindingIndex != 0 ||
		result.Changes.Binding.FailurePolicy == nil ||
		*result.Changes.Binding.FailurePolicy != moduleapi.FailureRequired ||
		!moduleapi.ValidSHA256(result.Changes.Binding.ConfigRef) ||
		!moduleapi.ValidSHA256(result.Changes.Binding.AuthorityCeilingRef) ||
		len(result.Changes.Binding.StaticContextRefs) != 1 ||
		!moduleapi.ValidSHA256(result.Changes.Binding.StaticContextRefs[0]) ||
		result.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		t.Fatalf("declarative WOULD_APPLY changes=%+v", result.Changes)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		moduleDryRunTempPrefixV1,
		`"manifest"`,
		`"config":`,
		`"authority_ceiling":`,
		`"activation_id"`,
		`"activation_revision"`,
		`"can_apply"`,
		`"receipt"`,
		`"reservation"`,
		"Authorization-sk-module-dry-run-secret",
	} {
		if bytes.Contains(wire, []byte(forbidden)) {
			t.Fatalf("module-dry-run output contains forbidden data %q: %s", forbidden, wire)
		}
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)

	projectedBasis := publishedModuleDryRunCandidateBasisV1(t, result.CandidateBasis)
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("apply plan after declarative dry-run: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t,
		applied,
		fixture,
		moduleApplyStatusApplied,
		moduleApplyEnabledV1,
		2,
	)
	appliedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if projectedBasis != appliedBasis {
		t.Fatalf(
			"dry-run candidate basis did not equal the subsequent Apply basis:\nprojected=%+v\napplied=%+v",
			projectedBasis,
			appliedBasis,
		)
	}
	if result.PlanDigest != applied.PlanDigest ||
		result.CandidateBasis.Control.SnapshotID != applied.ControlSnapshotID ||
		result.CandidateBasis.Catalog.GenerationID != applied.CatalogGenerationID {
		t.Fatalf("dry-run and Apply identities differ: dry-run=%+v apply=%+v", result, applied)
	}
}

func TestModuleDryRunMCPWouldApplyDoesNotStartProcess(t *testing.T) {
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
		filepath.Join(root, "enable-mcp.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	observedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	result, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("dry-run local MCP ENABLED plan: %v", err)
	}
	if result.SchemaVersion != "freeagent.module-dry-run-result/v1" ||
		result.Status != moduleApplyStatusWouldApply ||
		result.DesiredState != moduleApplyEnabledV1 ||
		result.TenantID != defaultTenantID ||
		result.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		result.InstanceID != moduleApplyTestInstanceID ||
		result.Port != productionActionPort ||
		!moduleapi.ValidSHA256(result.PlanDigest) ||
		result.StartupRecoveryRequired ||
		result.ObservedBasis != observedBasis ||
		result.CandidateBasis.ProjectionStatus != moduleDryRunProjectedNotReservedV1 ||
		result.Module == nil ||
		result.Module.ID != fixture.ModuleID ||
		result.Module.ExactVersion != moduleApplyTestVersion ||
		result.Module.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("local MCP module-dry-run result=%+v", result)
	}
	candidate := publishedModuleDryRunCandidateBasisV1(t, result.CandidateBasis)
	if candidate.PointerRevision != observedBasis.PointerRevision+1 ||
		result.Changes.Installation != moduleDryRunInstallationCreateV1 ||
		result.Changes.Activation != moduleDryRunActivationCreateV1 ||
		result.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		result.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		t.Fatalf("local MCP module-dry-run projection=%+v changes=%+v", candidate, result.Changes)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)
}

func TestModuleDryRunCleanupFailureFailsClosed(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	planCanonical := newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(planCanonical)
	if err != nil {
		t.Fatalf("restore cleanup-failure plan: %v", err)
	}
	observer, err := currentstore.OpenReadOnlyObserver(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open cleanup-failure observer: %v", err)
	}
	basis, control, catalog, err := observer.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		_ = observer.Close()
		t.Fatalf("load cleanup-failure basis: %v", err)
	}
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)

	cleanupFailure := errors.New("injected module-dry-run cleanup failure")
	productionFilesystem := productionModuleDryRunTempFilesystemV1()
	var leakedTemp string
	filesystem := moduleDryRunTempFilesystemV1{
		mkdirTemp: func(directory string, pattern string) (string, error) {
			created, createErr := productionFilesystem.mkdirTemp(directory, pattern)
			leakedTemp = created
			return created, createErr
		},
		removeAll: func(path string) error {
			if path != leakedTemp {
				return errors.New("cleanup targeted an unexpected TEMP path")
			}
			return cleanupFailure
		},
		lstat: productionFilesystem.lstat,
	}
	_, dryRunErr := prepareEnabledModuleDryRunWithFilesystemV1(
		context.Background(),
		observer,
		artifactRoot,
		moduleApplyCommandInputV1{
			DatabasePath:      databasePath,
			ArtifactRoot:      artifactRoot,
			ArtifactDirectory: fixture.ArtifactDirectory,
			Plan:              plan,
			PlanCanonical:     canonical,
			PlanDigest:        digest,
		},
		basis,
		control,
		catalog,
		filesystem,
	)
	closeErr := observer.Close()
	if closeErr != nil {
		t.Fatalf("close cleanup-failure observer: %v", closeErr)
	}
	if dryRunErr == nil ||
		moduleApplyFailureCodeOfV1(dryRunErr) != moduleApplyFailureArtifact ||
		!errors.Is(dryRunErr, cleanupFailure) {
		t.Fatalf(
			"cleanup failure=%v code=%q want ARTIFACT_INVALID with sentinel",
			dryRunErr,
			moduleApplyFailureCodeOfV1(dryRunErr),
		)
	}
	if leakedTemp == "" {
		t.Fatal("cleanup seam did not capture a TEMP root")
	}
	if _, err := os.Lstat(leakedTemp); err != nil {
		t.Fatalf("injected cleanup failure did not leave the expected TEMP root: %v", err)
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)
	if err := os.RemoveAll(leakedTemp); err != nil {
		t.Fatalf("manually clean injected TEMP residue: %v", err)
	}
	if _, err := os.Lstat(leakedTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manual TEMP cleanup left captured root %q: %v", leakedTemp, err)
	}
}

func TestModuleDryRunDisableProjectionMatchesApplyBasis(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if enabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || enabled.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare disable projection: result=%+v err=%v", enabled, err)
	}
	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, fixture.InstanceID, 2),
	)
	observed, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)

	result, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("dry-run DISABLED plan: %v", err)
	}
	if result.SchemaVersion != moduleDryRunResultSchemaV1 ||
		result.Status != moduleApplyStatusWouldApply ||
		result.DesiredState != moduleApplyDisabledV1 ||
		result.ObservedBasis != observed ||
		result.CandidateBasis.ProjectionStatus != moduleDryRunProjectedNotReservedV1 ||
		result.Module != nil || result.StartupRecoveryRequired ||
		result.Changes.Installation != moduleDryRunInstallationNoneV1 ||
		result.Changes.Activation != moduleDryRunActivationNoneV1 ||
		result.Changes.Binding.Change != moduleDryRunBindingRemoveV1 ||
		result.Changes.Binding.PortBindingIndex == nil ||
		*result.Changes.Binding.PortBindingIndex != 0 ||
		!moduleapi.ValidSHA256(result.Changes.Binding.ConfigRef) ||
		!moduleapi.ValidSHA256(result.Changes.Binding.AuthorityCeilingRef) ||
		len(result.Changes.Binding.StaticContextRefs) != 1 ||
		result.Changes.Binding.FailurePolicy == nil ||
		*result.Changes.Binding.FailurePolicy != moduleapi.FailureRequired ||
		result.Changes.Catalog != moduleDryRunCatalogRemoveInstanceV1 {
		t.Fatalf("DISABLED dry-run result=%+v", result)
	}
	projected := publishedModuleDryRunCandidateBasisV1(t, result.CandidateBasis)
	if databaseAfter := snapshotModuleDryRunDatabaseV1(t, databasePath); !reflect.DeepEqual(databaseAfter, databaseBefore) {
		t.Fatalf("DISABLED dry-run changed database: before=%+v after=%+v", databaseBefore, databaseAfter)
	}
	if artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot); !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
		t.Fatalf("DISABLED dry-run changed artifact root: before=%+v after=%+v", artifactsBefore, artifactsAfter)
	}

	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied {
		t.Fatalf("apply DISABLED plan: result=%+v err=%v", disabled, err)
	}
	applied, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if projected != applied {
		t.Fatalf("DISABLED projected basis=%+v applied=%+v", projected, applied)
	}
	retried, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.CandidateBasis.ProjectionStatus != moduleDryRunObservedCurrentV1 {
		t.Fatalf("DISABLED retry dry-run=%+v err=%v", retried, err)
	}
	assertModuleDryRunNoChangesV1(t, retried)
	noChangePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role-no-change.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, fixture.InstanceID, 3),
	)
	noChange, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		noChangePath,
		"",
		"",
	)
	if err != nil || noChange.Status != moduleApplyStatusNoChange ||
		noChange.CandidateBasis.ProjectionStatus != moduleDryRunObservedCurrentV1 {
		t.Fatalf("DISABLED NO_CHANGE dry-run=%+v err=%v", noChange, err)
	}
	assertModuleDryRunNoChangesV1(t, noChange)
}

func TestModuleDryRunDisableRetainsSharedCatalogInstance(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	seedDualActionChannelPublicationV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-shared-channel.json"),
		newNamedDisabledModuleApplyPlanV1(
			t,
			moduleApplyTestProfileID,
			moduleApplyDualInstanceID,
			2,
		),
	)
	observed, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	result, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		"",
		"",
	)
	if err != nil || result.Status != moduleApplyStatusWouldApply ||
		result.ObservedBasis != observed ||
		result.Changes.Binding.Change != moduleDryRunBindingRemoveV1 ||
		result.Changes.Catalog != moduleDryRunCatalogRetainInstanceV1 ||
		result.Changes.Installation != moduleDryRunInstallationNoneV1 ||
		result.Changes.Activation != moduleDryRunActivationNoneV1 {
		t.Fatalf("shared-reference DISABLED dry-run=%+v err=%v", result, err)
	}
	projected := publishedModuleDryRunCandidateBasisV1(t, result.CandidateBasis)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied {
		t.Fatalf("apply shared-reference DISABLED plan=%+v err=%v", disabled, err)
	}
	applied, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if projected != applied {
		t.Fatalf("shared-reference projected=%+v applied=%+v", projected, applied)
	}
}

func TestModuleDryRunPredictsExistingFinalArtifactWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		conflict  bool
		wantError string
	}{
		{name: "exact orphan is reusable"},
		{name: "conflicting orphan fails closed", conflict: true, wantError: "(ARTIFACT_INVALID)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			fixture := newModuleApplyDeclarativeFixtureV1(
				t,
				moduleApplyRoleID,
				moduleApplyRoleInstance,
			)
			finalArtifact := filepath.Join(artifactRoot, fixture.ArtifactDigest)
			if test.conflict {
				if err := os.Mkdir(finalArtifact, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(finalArtifact, moduleapi.ArtifactManifestPath),
					[]byte(`{"conflict":true}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			} else {
				copyModuleApplyTestTreeV1(t, fixture.ArtifactDirectory, finalArtifact)
			}
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "enable-role.json"),
				newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
			)
			databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
			artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
			sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)
			result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("conflicting orphan result=%+v error=%v", result, err)
				}
			} else if err != nil || result.Status != moduleApplyStatusWouldApply {
				t.Fatalf("exact orphan result=%+v error=%v", result, err)
			}
			assertModuleDryRunInputsUnchangedV1(
				t,
				databasePath,
				artifactRoot,
				fixture.ArtifactDirectory,
				databaseBefore,
				artifactsBefore,
				sourceBefore,
			)
		})
	}
}

func TestRunModuleDryRunFlagsCancellationAndSidecarAreFailClosed(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	validArgs := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
		"--artifact", fixture.ArtifactDirectory,
	}
	privateSentinel := "Authorization-sk-module-dry-run-secret"
	tests := []struct {
		name string
		ctx  context.Context
		args []string
		want string
	}{
		{
			name: "nil context",
			ctx:  nil,
			want: "freeagent module-dry-run: failed (INVALID_FLAGS)",
		},
		{
			name: "unknown secret-shaped flag",
			ctx:  context.Background(),
			args: []string{"--" + privateSentinel},
			want: "freeagent module-dry-run: failed (INVALID_FLAGS)",
		},
		{
			name: "missing artifact",
			ctx:  context.Background(),
			args: []string{
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--plan", planPath,
			},
			want: "freeagent module-dry-run: failed (GRANT_REQUIRED)",
		},
		{
			name: "grant forbidden for declarative",
			ctx:  context.Background(),
			args: append(append([]string{}, validArgs...),
				"--allow-local-mcp-artifact", privateSentinel),
			want: "freeagent module-dry-run: failed (INVALID_FLAGS)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := runModuleDryRun(test.ctx, test.args, &stdout, &stderr)
			if err == nil || err.Error() != test.want ||
				stdout.Len() != 0 || stderr.Len() != 0 ||
				strings.Contains(err.Error(), privateSentinel) {
				t.Fatalf("error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleDryRun(ctx, validArgs, &stdout, &stderr)
	if err == nil || err.Error() != "freeagent module-dry-run: failed (CANCELLED)" ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("pre-cancelled error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}

	sidecarPath := databasePath + "-wal"
	if err := os.WriteFile(sidecarPath, []byte(privateSentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)
	stdout.Reset()
	stderr.Reset()
	err = runModuleDryRun(context.Background(), validArgs, &stdout, &stderr)
	if err == nil || err.Error() != "freeagent module-dry-run: failed (STORE_INVALID)" ||
		stdout.Len() != 0 || stderr.Len() != 0 ||
		strings.Contains(err.Error(), privateSentinel) {
		t.Fatalf("sidecar error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)
	if err := os.Remove(sidecarPath); err != nil {
		t.Fatal(err)
	}
}

func TestModuleDryRunReportsRecoveryRequirementWithoutRecovering(t *testing.T) {
	for _, test := range []struct {
		name          string
		commitUnknown bool
		wantRequired  bool
	}{
		{name: "pending", wantRequired: true},
		{name: "unknown-only", commitUnknown: true, wantRequired: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			store, err := currentstore.OpenExistingCurrentStore(
				context.Background(),
				databasePath,
			)
			if err != nil {
				t.Fatal(err)
			}
			runs := admitStartupRecoveryRuns(t, store)
			attemptID := beginStartupRecoveryPending(t, store, runs["pending"])
			if test.commitUnknown {
				commitStartupRecoveryUnknown(t, store, runs["pending"], attemptID)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
				context.Background(),
				databasePath,
			); err != nil {
				t.Fatal(err)
			}
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "no-change.json"),
				newDisabledModuleApplyPlanFixtureV1(t, 1),
			)
			databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
			artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
			result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				"",
				"",
			)
			if err != nil || result.Status != moduleApplyStatusNoChange ||
				result.StartupRecoveryRequired != test.wantRequired ||
				result.CandidateBasis.ProjectionStatus != moduleDryRunObservedCurrentV1 {
				t.Fatalf("recovery observation result=%+v err=%v", result, err)
			}
			if databaseAfter := snapshotModuleDryRunDatabaseV1(t, databasePath); !reflect.DeepEqual(databaseAfter, databaseBefore) {
				t.Fatalf("recovery observation changed Store: before=%+v after=%+v", databaseBefore, databaseAfter)
			}
			if artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot); !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
				t.Fatalf("recovery observation changed artifacts: before=%+v after=%+v", artifactsBefore, artifactsAfter)
			}
		})
	}
}

func TestModuleDryRunDeclarativeTerminalStatesSkipSourceAndWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	fixture.ArtifactDirectory = filepath.Join(root, "invalid-terminal-source")
	copyModuleApplyTestTreeV1(
		t,
		filepath.Join(
			filepath.Dir(exampleSeedPath(t)),
			"bootstrap-artifacts",
			fixture.ModuleID,
			moduleApplyTestVersion,
		),
		fixture.ArtifactDirectory,
	)
	exactRetryPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		exactRetryPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare terminal dry-run fixture: result=%+v err=%v", applied, err)
	}

	if err := os.RemoveAll(fixture.ArtifactDirectory); err != nil {
		t.Fatalf("remove valid source fixture: %v", err)
	}
	invalidSource := []byte("terminal dry-run must not read this non-directory source\n")
	if err := os.WriteFile(fixture.ArtifactDirectory, invalidSource, 0o600); err != nil {
		t.Fatalf("write invalid terminal source sentinel: %v", err)
	}
	noChangePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role-no-change.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 2, 0),
	)
	currentBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)

	for attempt := 0; attempt < 2; attempt++ {
		result, err := runModuleDryRunFixtureV1(
			databasePath,
			artifactRoot,
			exactRetryPath,
			fixture.ArtifactDirectory,
			"",
		)
		if err != nil {
			t.Fatalf("terminal ALREADY_APPLIED dry-run %d: %v", attempt+1, err)
		}
		assertModuleDryRunIdentityV1(
			t,
			result,
			fixture,
			moduleApplyStatusAlreadyApplied,
			currentBasis,
			moduleDryRunObservedCurrentV1,
		)
		assertModuleDryRunNoChangesV1(t, result)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := runModuleDryRunFixtureV1(
			databasePath,
			artifactRoot,
			noChangePath,
			fixture.ArtifactDirectory,
			"",
		)
		if err != nil {
			t.Fatalf("terminal NO_CHANGE dry-run %d: %v", attempt+1, err)
		}
		assertModuleDryRunIdentityV1(
			t,
			result,
			fixture,
			moduleApplyStatusNoChange,
			currentBasis,
			moduleDryRunObservedCurrentV1,
		)
		assertModuleDryRunNoChangesV1(t, result)
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)
}

func runModuleDryRunFixtureV1(
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	grant string,
) (moduleDryRunResultV1, error) {
	args := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
	}
	if artifactDirectory != "" {
		args = append(args, "--artifact", artifactDirectory)
	}
	if grant != "" {
		args = append(args, "--allow-local-mcp-artifact", grant)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleDryRun(context.Background(), args, &stdout, &stderr)
	if stderr.Len() != 0 {
		return moduleDryRunResultV1{}, errors.Join(
			err,
			errors.New("module-dry-run unexpectedly wrote stderr: "+stderr.String()),
		)
	}
	if err != nil {
		if stdout.Len() != 0 {
			return moduleDryRunResultV1{}, errors.Join(
				err,
				errors.New("failed module-dry-run unexpectedly wrote stdout"),
			)
		}
		return moduleDryRunResultV1{}, err
	}
	payload := bytes.Clone(stdout.Bytes())
	var result moduleDryRunResultV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&result); decodeErr != nil {
		return moduleDryRunResultV1{}, decodeErr
	}
	var trailing any
	if decodeErr := decoder.Decode(&trailing); !errors.Is(decodeErr, io.EOF) {
		return moduleDryRunResultV1{}, errors.New("module-dry-run result has trailing JSON")
	}
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		return moduleDryRunResultV1{}, encodeErr
	}
	canonical, canonicalErr := moduleapi.CanonicalJSON(encoded)
	if canonicalErr != nil {
		return moduleDryRunResultV1{}, canonicalErr
	}
	if !bytes.Equal(payload, append(canonical, '\n')) {
		return moduleDryRunResultV1{}, errors.New("module-dry-run result is not exact canonical JSON")
	}
	return result, nil
}

func assertModuleDryRunIdentityV1(
	t *testing.T,
	result moduleDryRunResultV1,
	fixture moduleApplyDeclarativeFixtureV1,
	status moduleApplyStatusV1,
	observed controlcontract.PublishedBasis,
	projectionStatus moduleDryRunProjectionStatusV1,
) {
	t.Helper()
	if result.SchemaVersion != "freeagent.module-dry-run-result/v1" ||
		result.Status != status ||
		result.DesiredState != moduleApplyEnabledV1 ||
		result.TenantID != defaultTenantID ||
		result.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		result.InstanceID != fixture.InstanceID ||
		result.Port != productionContextPort ||
		!moduleapi.ValidSHA256(result.PlanDigest) ||
		result.StartupRecoveryRequired ||
		result.ObservedBasis != observed ||
		result.CandidateBasis.ProjectionStatus != projectionStatus ||
		result.Module == nil ||
		result.Module.ID != fixture.ModuleID ||
		result.Module.ExactVersion != moduleApplyTestVersion ||
		result.Module.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("module-dry-run result=%+v observed=%+v", result, observed)
	}
	candidate := publishedModuleDryRunCandidateBasisV1(t, result.CandidateBasis)
	if projectionStatus == moduleDryRunObservedCurrentV1 && candidate != observed {
		t.Fatalf("terminal candidate basis=%+v want observed=%+v", candidate, observed)
	}
}

func publishedModuleDryRunCandidateBasisV1(
	t *testing.T,
	candidate moduleDryRunCandidateBasisV1,
) controlcontract.PublishedBasis {
	t.Helper()
	basis := controlcontract.PublishedBasis{
		TenantID:        candidate.TenantID,
		PointerRevision: candidate.PointerRevision,
		Control:         candidate.Control,
		Catalog:         candidate.Catalog,
	}
	if err := basis.Validate(); err != nil {
		t.Fatalf("module-dry-run candidate basis is incomplete: %+v: %v", candidate, err)
	}
	return basis
}

func assertModuleDryRunNoChangesV1(t *testing.T, result moduleDryRunResultV1) {
	t.Helper()
	changes := result.Changes
	if changes.Installation != moduleDryRunInstallationNoneV1 ||
		changes.Activation != moduleDryRunActivationNoneV1 ||
		changes.Binding.Change != moduleDryRunBindingNoneV1 ||
		changes.Binding.PortBindingIndex != nil ||
		changes.Binding.ConfigRef != "" ||
		changes.Binding.AuthorityCeilingRef != "" ||
		len(changes.Binding.StaticContextRefs) != 0 ||
		changes.Binding.FailurePolicy != nil ||
		changes.Catalog != moduleDryRunCatalogNoneV1 {
		t.Fatalf("terminal module-dry-run projected changes=%+v", changes)
	}
}

type moduleDryRunFileSnapshotV1 struct {
	Mode       os.FileMode
	Content    []byte
	LinkTarget string
}

type moduleDryRunTreeSnapshotV1 struct {
	Exists  bool
	Entries map[string]moduleDryRunFileSnapshotV1
}

func snapshotModuleDryRunDatabaseV1(
	t *testing.T,
	databasePath string,
) map[string]moduleDryRunFileSnapshotV1 {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(databasePath))
	if err != nil {
		t.Fatalf("read database parent for snapshot: %v", err)
	}
	base := filepath.Base(databasePath)
	ownerLock := base + ".freeagent.owner.lock"
	snapshot := make(map[string]moduleDryRunFileSnapshotV1)
	for _, entry := range entries {
		if entry.Name() != base && entry.Name() != ownerLock &&
			!strings.HasPrefix(entry.Name(), base+"-") {
			continue
		}
		snapshot[entry.Name()] = snapshotModuleDryRunFileV1(
			t,
			filepath.Join(filepath.Dir(databasePath), entry.Name()),
		)
	}
	if _, found := snapshot[base]; !found {
		t.Fatalf("database %q is absent from snapshot", databasePath)
	}
	return snapshot
}

func snapshotModuleDryRunTreeV1(t *testing.T, root string) moduleDryRunTreeSnapshotV1 {
	t.Helper()
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return moduleDryRunTreeSnapshotV1{}
	} else if err != nil {
		t.Fatalf("inspect snapshot root %q: %v", root, err)
	}
	snapshot := moduleDryRunTreeSnapshotV1{
		Exists:  true,
		Entries: make(map[string]moduleDryRunFileSnapshotV1),
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot.Entries[relative] = snapshotModuleDryRunFileV1(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree %q: %v", root, err)
	}
	return snapshot
}

func snapshotModuleDryRunFileV1(t *testing.T, path string) moduleDryRunFileSnapshotV1 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect snapshot entry %q: %v", path, err)
	}
	entry := moduleDryRunFileSnapshotV1{Mode: info.Mode()}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		entry.LinkTarget, err = os.Readlink(path)
	case info.Mode().IsRegular():
		entry.Content, err = os.ReadFile(path)
	}
	if err != nil {
		t.Fatalf("read snapshot entry %q: %v", path, err)
	}
	return entry
}

func assertModuleDryRunInputsUnchangedV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	source string,
	databaseBefore map[string]moduleDryRunFileSnapshotV1,
	artifactsBefore moduleDryRunTreeSnapshotV1,
	sourceBefore moduleDryRunTreeSnapshotV1,
) {
	t.Helper()
	databaseAfter := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceAfter := snapshotModuleDryRunTreeV1(t, source)
	if !reflect.DeepEqual(databaseAfter, databaseBefore) {
		t.Fatalf("module-dry-run changed database bytes or sidecars:\nbefore=%+v\nafter=%+v", databaseBefore, databaseAfter)
	}
	if !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
		t.Fatalf("module-dry-run changed artifact root:\nbefore=%+v\nafter=%+v", artifactsBefore, artifactsAfter)
	}
	if !reflect.DeepEqual(sourceAfter, sourceBefore) {
		t.Fatalf("module-dry-run changed source artifact:\nbefore=%+v\nafter=%+v", sourceBefore, sourceAfter)
	}
}
