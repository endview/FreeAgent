package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyPromotesExistingInertIngressLocalProcess(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	sourceRoot := filepath.Join(root, "local-source")
	eventPath := filepath.Join(root, "mcp-started.log")
	fixture := newModuleApplyMCPFixtureV1(t, sourceRoot, eventPath)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	packagePath := filepath.ToSlash(filepath.Join(
		"bootstrap-artifacts", fixture.ModuleID, moduleApplyTestVersion,
	))
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(sourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.apply.ingress",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"freeagent.test.mcp"},
			MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
			MaxPackageBytes:         fixture.ArtifactSizeBytes + 4096,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatalf("freeze ingress Source Policy: %v", err)
	}
	snapshotID := strings.Repeat("a", 64)
	selection := moduleartifactingress.SelectionV1{
		SourceID:       "local.apply.ingress",
		SnapshotID:     snapshotID,
		Module:         moduleapi.Ref{ID: fixture.ModuleID, Version: moduleApplyTestVersion},
		ArtifactDigest: fixture.ArtifactDigest,
	}
	view := moduleartifactingress.BasisViewV1{
		SourceID:                    selection.SourceID,
		SourcePolicyID:              policyID,
		SourcePolicyCanonical:       policyCanonical,
		SourcePolicyRevision:        1,
		SnapshotID:                  snapshotID,
		SnapshotSourcePolicyID:      policyID,
		SnapshotObservationRevision: 1,
		EntryOrdinal:                0,
		Entry: moduleapi.ModuleDiscoveryEntryV1{
			Module:            selection.Module,
			ArtifactDigest:    fixture.ArtifactDigest,
			ArtifactSizeBytes: fixture.ArtifactSizeBytes,
			PackagePath:       packagePath,
		},
	}
	coveredFileCount := uint64(0)
	ingressed, err := moduleartifactingress.IngressV1(
		ctx,
		moduleartifactingress.RequestV1{
			SourceRoot: sourceRoot, ArtifactRoot: artifactRoot, Selection: selection,
		},
		moduleartifactingress.StoreCallbacksV1[int, string, string]{
			Existing: func(
				context.Context,
				moduleartifactingress.SelectionV1,
			) (string, string, moduleartifactingress.ExistingViewV1, bool, error) {
				return "", "", moduleartifactingress.ExistingViewV1{}, false, nil
			},
			Read: func(
				context.Context,
				moduleartifactingress.SelectionV1,
			) (int, moduleartifactingress.BasisViewV1, error) {
				return 1, view, nil
			},
			Commit: func(
				_ context.Context,
				basis int,
				manifest []byte,
				count uint64,
			) (string, string, error) {
				if basis != 1 || len(manifest) == 0 || count == 0 {
					return "", "", errors.New("invalid ingress commit evidence")
				}
				coveredFileCount = count
				return "artifact", "admission", nil
			},
		},
	)
	if err != nil || ingressed.Reused || coveredFileCount == 0 {
		t.Fatalf("real LOCAL_PROCESS ingress result=%+v count=%d err=%v", ingressed, coveredFileCount, err)
	}

	target := filepath.Join(artifactRoot, fixture.ArtifactDigest)
	descriptorCanonical, err := os.ReadFile(filepath.Join(target, "content", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor mcpstdio.HostDescriptorV1
	if err := json.Unmarshal(descriptorCanonical, &descriptor); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(target, filepath.FromSlash(descriptor.Executable))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(executable)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("ingress executable mode=%v err=%v; want inert 0600", info, err)
		}
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, installationErr := store.GetModuleInstallationByIdentity(
		ctx, fixture.ModuleID, moduleApplyTestVersion,
	)
	closeErr := store.Close()
	if !errors.Is(installationErr, currentstore.ErrModuleInstallationNotFound) || closeErr != nil {
		t.Fatalf("ingress granted Store installation authority: %v", errors.Join(installationErr, closeErr))
	}

	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	applied, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, planPath,
		fixture.ArtifactDirectory, fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("Apply exact inert ingress target: %v", err)
	}
	assertModuleApplyResultV1(
		t, applied, moduleApplyStatusApplied, moduleApplyEnabledV1, 2,
	)
	if err := moduleartifactstore.VerifyExistingV1(
		ctx, artifactRoot, fixture.ArtifactDigest, fixture.ArtifactSizeBytes,
		coveredFileCount, moduleartifactstore.ExistingModeStoreProvenInstalledV1,
	); err != nil {
		t.Fatalf("promoted installed physical closure: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(executable)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("Apply executable mode=%v err=%v; want exact 0700", info, err)
		}
	}

	retried, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, planPath,
		fixture.ArtifactDirectory, fixture.ArtifactDigest,
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != 2 {
		t.Fatalf("exact Apply retry=%+v err=%v", retried, err)
	}
}
