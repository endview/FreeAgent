package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleListReportsExactCurrentBindingsWithoutChangingStore(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

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
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-role.json"),
		role,
		1,
		0,
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-skill.json"),
		skill,
		2,
		1,
	)

	before := moduleOperatorStoreHashV1(t, databasePath)
	payload := runModuleOperatorCommandV1(t, []string{
		"module-list",
		"--db", databasePath,
		"--tenant", defaultTenantID,
	})
	assertCanonicalModuleOperatorOutputV1(t, payload)
	var result moduleListResultV1
	decodeModuleOperatorOutputV1(t, payload, &result)
	if result.SchemaVersion != moduleListResultSchemaV1 ||
		result.BindingTarget != nil || result.Basis.TenantID != defaultTenantID ||
		result.Basis.PointerRevision != 3 ||
		result.Basis.Control.SnapshotID == "" ||
		result.Basis.Control.Revision == 0 ||
		!moduleapi.ValidSHA256(result.Basis.Control.Digest) ||
		result.Basis.Catalog.GenerationID == "" ||
		result.Basis.Catalog.Generation == 0 ||
		!moduleapi.ValidSHA256(result.Basis.Catalog.Digest) {
		t.Fatalf("module-list identity=%+v", result)
	}
	assertModuleOperatorBindingsMatchControlV1(
		t,
		databasePath,
		result.Basis,
		result.Bindings,
		"",
	)
	contextBindings := make([]moduleOperatorBindingV1, 0)
	for _, binding := range result.Bindings {
		if binding.Port == productionContextPort {
			contextBindings = append(contextBindings, binding)
		}
	}
	if len(contextBindings) != 3 ||
		contextBindings[0].Source.ID != role.ModuleID ||
		contextBindings[0].PortBindingIndex != 0 ||
		contextBindings[1].Source.ID != skill.ModuleID ||
		contextBindings[1].PortBindingIndex != 1 ||
		contextBindings[2].PortBindingIndex != 2 {
		t.Fatalf("ordered Context bindings=%+v", contextBindings)
	}
	assertModuleOperatorOutputRedactedV1(
		t,
		payload,
		artifactRoot,
		role.ArtifactDirectory,
		skill.ArtifactDirectory,
		moduleApplyRoleText,
		moduleApplySkillText,
	)

	filteredPayload := runModuleOperatorCommandV1(t, []string{
		"module-list",
		"--db", databasePath,
		"--tenant", defaultTenantID,
		"--profile", moduleApplyTestProfileID,
	})
	assertCanonicalModuleOperatorOutputV1(t, filteredPayload)
	var filtered moduleListResultV1
	decodeModuleOperatorOutputV1(t, filteredPayload, &filtered)
	if filtered.BindingTarget == nil ||
		filtered.BindingTarget.Kind != moduleApplyBindingTargetProfileV1 ||
		filtered.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		filtered.Basis != result.Basis ||
		len(filtered.Bindings) != len(result.Bindings) {
		t.Fatalf("filtered module-list=%+v", filtered)
	}
	assertModuleOperatorBindingsMatchControlV1(
		t,
		databasePath,
		filtered.Basis,
		filtered.Bindings,
		moduleApplyTestProfileID,
	)

	for _, command := range [][]string{
		{
			"module-list", "--db", databasePath,
			"--tenant", defaultTenantID,
			"--profile", "profile-absent",
		},
		{"module-list", "--db", databasePath},
	} {
		stdout, stderr, err := invokeModuleOperatorCommandV1(command)
		if err == nil || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("invalid module-list stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
		}
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != before {
		t.Fatal("module-list changed Current Store bytes")
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func TestModuleListReturnsCanonicalEmptyBindings(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	publishModuleOperatorEmptyPureChatBindingsV1(t, databasePath)

	payload := runModuleOperatorCommandV1(t, []string{
		"module-list",
		"--db", databasePath,
		"--tenant", defaultTenantID,
		"--profile", moduleApplyTestProfileID,
	})
	assertCanonicalModuleOperatorOutputV1(t, payload)
	if !bytes.Contains(payload, []byte(`"bindings":[]`)) {
		t.Fatalf("empty module-list did not encode bindings as []: %s", payload)
	}
	var result moduleListResultV1
	decodeModuleOperatorOutputV1(t, payload, &result)
	if result.SchemaVersion != moduleListResultSchemaV1 ||
		result.Basis.PointerRevision != 2 || result.Bindings == nil ||
		len(result.Bindings) != 0 {
		t.Fatalf("empty module-list=%+v", result)
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func TestModuleHistoryReadsExactPublishedBindingsAfterDisableWithoutChangingStore(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

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
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-role.json"),
		role,
		1,
		0,
	)
	roleBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-skill.json"),
		skill,
		roleBasis.PointerRevision,
		1,
	)
	skillBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			role.InstanceID,
			skillBasis.PointerRevision,
		),
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	); err != nil {
		t.Fatalf("disable role: %v", err)
	}
	disabledBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)

	before := moduleOperatorStoreHashV1(t, databasePath)
	rolePayload := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(databasePath, roleBasis, ""),
	)
	assertCanonicalModuleOperatorOutputV1(t, rolePayload)
	var roleHistory moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, rolePayload, &roleHistory)
	if roleHistory.SchemaVersion != moduleHistoryResultSchemaV1 ||
		roleHistory.TenantID != defaultTenantID ||
		roleHistory.Control != roleBasis.Control ||
		roleHistory.Catalog != roleBasis.Catalog ||
		roleHistory.BindingTarget != nil ||
		!moduleOperatorContainsInstanceV1(roleHistory.Bindings, role.InstanceID) ||
		moduleOperatorContainsInstanceV1(roleHistory.Bindings, skill.InstanceID) {
		t.Fatalf("role historical projection=%+v", roleHistory)
	}

	skillPayload := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			databasePath,
			skillBasis,
			moduleApplyTestProfileID,
		),
	)
	var skillHistory moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, skillPayload, &skillHistory)
	if skillHistory.BindingTarget == nil ||
		skillHistory.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		!moduleOperatorContainsInstanceV1(skillHistory.Bindings, role.InstanceID) ||
		!moduleOperatorContainsInstanceV1(skillHistory.Bindings, skill.InstanceID) {
		t.Fatalf("role+skill historical projection=%+v", skillHistory)
	}

	disabledPayload := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			databasePath,
			disabledBasis,
			moduleApplyTestProfileID,
		),
	)
	var disabledHistory moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, disabledPayload, &disabledHistory)
	if moduleOperatorContainsInstanceV1(disabledHistory.Bindings, role.InstanceID) ||
		!moduleOperatorContainsInstanceV1(disabledHistory.Bindings, skill.InstanceID) {
		t.Fatalf("disabled historical projection=%+v", disabledHistory)
	}

	second := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(databasePath, roleBasis, ""),
	)
	if !bytes.Equal(rolePayload, second) {
		t.Fatal("module-history output is not deterministic")
	}
	assertModuleOperatorOutputRedactedV1(
		t,
		bytes.Join([][]byte{rolePayload, skillPayload, disabledPayload}, nil),
		artifactRoot,
		role.ArtifactDirectory,
		skill.ArtifactDirectory,
		moduleApplyRoleText,
		moduleApplySkillText,
		`"parameters"`,
		`"authority_ceiling"`,
	)

	for _, test := range []struct {
		name string
		args []string
		code string
	}{
		{
			name: "missing revisions",
			args: []string{
				"module-history", "--db", databasePath,
				"--tenant", defaultTenantID,
			},
			code: "INVALID_FLAGS",
		},
		{
			name: "mismatched pair",
			args: []string{
				"module-history", "--db", databasePath,
				"--tenant", defaultTenantID,
				"--control-revision", strconv.FormatUint(roleBasis.Control.Revision, 10),
				"--catalog-generation", strconv.FormatUint(skillBasis.Catalog.Generation, 10),
			},
			code: "HISTORY_NOT_FOUND",
		},
		{
			name: "unknown profile",
			args: append(
				moduleHistoryCommandV1(databasePath, roleBasis, ""),
				"--profile", "profile-absent",
			),
			code: "TARGET_NOT_FOUND",
		},
		{
			name: "cross tenant pair",
			args: []string{
				"module-history", "--db", databasePath,
				"--tenant", "tenant-other",
				"--control-revision", strconv.FormatUint(roleBasis.Control.Revision, 10),
				"--catalog-generation", strconv.FormatUint(roleBasis.Catalog.Generation, 10),
			},
			code: "HISTORY_NOT_FOUND",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := invokeModuleOperatorCommandV1(test.args)
			if err == nil || !strings.Contains(err.Error(), "("+test.code+")") ||
				stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
			}
		})
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != before {
		t.Fatal("module-history changed Current Store bytes")
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func TestModuleHistoryFailsClosedOnDamagedHistoricalControl(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-role.json"),
		role,
		1,
		0,
	)
	basis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)

	damagedPath := filepath.Join(root, "damaged.sqlite")
	payload, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(damagedPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	execCmdClosedFileTamperV1(
		t,
		damagedPath,
		[]string{"control_snapshots_reject_update"},
		`
		UPDATE control_snapshots
		SET canonical_json=?
		WHERE snapshot_id=?
	`,
		[]byte(`{"broken":true}`),
		basis.Control.SnapshotID,
	)

	stdout, stderr, err := invokeModuleOperatorCommandV1(
		moduleHistoryCommandV1(
			damagedPath,
			basis,
			moduleApplyTestProfileID,
		),
	)
	if err == nil || !strings.Contains(err.Error(), "(STORE_INVALID)") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, damagedPath)
}

func TestModuleHistoryRevalidatesEveryCatalogEntryForCachedPackage(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	first := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"module-history-shared-package-a",
	)
	second := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"module-history-shared-package-b",
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-first.json"),
		first,
		1,
		0,
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-second.json"),
		second,
		2,
		1,
	)
	basis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)

	tests := []struct {
		name   string
		mutate func(*moduleapi.ActivatedModuleRef, *[]moduleapi.PortRef)
	}{
		{
			name: "artifact digest",
			mutate: func(activation *moduleapi.ActivatedModuleRef, _ *[]moduleapi.PortRef) {
				activation.ArtifactDigest = strings.Repeat("f", sha256.Size*2)
				if activation.ArtifactDigest == second.ArtifactDigest {
					activation.ArtifactDigest = strings.Repeat("e", sha256.Size*2)
				}
			},
		},
		{
			name: "execution class",
			mutate: func(activation *moduleapi.ActivatedModuleRef, _ *[]moduleapi.PortRef) {
				activation.ExecutionClass = moduleapi.ExecutionTrustedInProcess
			},
		},
		{
			name: "exact provides",
			mutate: func(_ *moduleapi.ActivatedModuleRef, provides *[]moduleapi.PortRef) {
				*provides = append(*provides, moduleapi.PortRef{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV2,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			damagedPath := filepath.Join(
				root,
				"shared-package-"+strings.ReplaceAll(test.name, " ", "-")+".sqlite",
			)
			copyModuleOperatorDatabaseV1(t, databasePath, damagedPath)
			_, _, catalog := loadModuleApplyPublishedStateV1(t, damagedPath)
			found := false
			for index := range catalog.Entries {
				if catalog.Entries[index].Activation.InstanceID != second.InstanceID {
					continue
				}
				test.mutate(
					&catalog.Entries[index].Activation,
					&catalog.Entries[index].Provides,
				)
				found = true
				break
			}
			if !found {
				t.Fatal("second shared-package Catalog Entry is absent")
			}
			catalog.Digest = ""
			_, damagedRef, damagedCanonical, err := controlcontract.NewCatalogGeneration(catalog)
			if err != nil {
				t.Fatal(err)
			}
			if damagedRef.GenerationID != basis.Catalog.GenerationID ||
				damagedRef.Generation != basis.Catalog.Generation ||
				damagedRef.Digest == basis.Catalog.Digest {
				t.Fatalf("damaged Catalog ref=%+v original=%+v", damagedRef, basis.Catalog)
			}
			result := execCmdClosedFileTamperV1(
				t,
				damagedPath,
				[]string{"runtime_catalog_generations_reject_update"},
				`
				UPDATE runtime_catalog_generations
				SET canonical_json=?, digest=?
				WHERE generation_id=?
			`,
				damagedCanonical,
				damagedRef.Digest,
				damagedRef.GenerationID,
			)
			requireModuleOperatorDamagedRowV1(
				t,
				result,
				nil,
				"shared-package Catalog",
			)
			assertModuleHistoryStoreInvalidV1(t, damagedPath, basis)
		})
	}
}

func TestModuleHistoryMapsDamagedHistoricalClosuresToStoreInvalid(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-role.json"),
		role,
		1,
		0,
	)
	basis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)

	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{
			name: "catalog canonical",
			mutate: func(t *testing.T, database *sql.DB) {
				t.Helper()
				result := execCmdClosedFileDatabaseTamperV1(
					t,
					database,
					[]string{"runtime_catalog_generations_reject_update"},
					`
					UPDATE runtime_catalog_generations
					SET canonical_json=?
					WHERE generation_id=?
				`,
					[]byte(`{"broken":true}`),
					basis.Catalog.GenerationID,
				)
				requireModuleOperatorDamagedRowV1(t, result, nil, "Catalog canonical")
			},
		},
		{
			name: "installation missing",
			mutate: func(t *testing.T, database *sql.DB) {
				t.Helper()
				database.SetMaxOpenConns(1)
				if _, err := database.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
				result, err := database.Exec(`
					DELETE FROM module_installations
					WHERE module_id=? AND exact_version=?
				`, role.ModuleID, moduleApplyTestVersion)
				requireModuleOperatorDamagedRowV1(t, result, err, "Installation delete")
			},
		},
		{
			name: "manifest content",
			mutate: func(t *testing.T, database *sql.DB) {
				t.Helper()
				var manifestRef string
				if err := database.QueryRow(`
					SELECT manifest_ref
					FROM module_installations
					WHERE module_id=? AND exact_version=?
				`, role.ModuleID, moduleApplyTestVersion).Scan(&manifestRef); err != nil {
					t.Fatal(err)
				}
				broken := []byte(`{"broken":true}`)
				result := execCmdClosedFileDatabaseTamperV1(
					t,
					database,
					[]string{"content_records_reject_update"},
					`
					UPDATE content_records
					SET canonical_bytes=?, size_bytes=?
					WHERE content_digest=?
				`,
					broken,
					len(broken),
					manifestRef,
				)
				requireModuleOperatorDamagedRowV1(t, result, nil, "Manifest content")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			damagedPath := filepath.Join(root, "damaged-"+strings.ReplaceAll(test.name, " ", "-")+".sqlite")
			copyModuleOperatorDatabaseV1(t, databasePath, damagedPath)
			database, err := sql.Open("sqlite", damagedPath)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			assertModuleHistoryStoreInvalidV1(t, damagedPath, basis)
		})
	}
}

func TestModuleHistoryCloseFailureOverridesBusinessReadError(t *testing.T) {
	closeErr := errors.New("injected observer close failure")
	for _, readErr := range []error{
		currentstore.ErrPublishedBasisNotFound,
		errModuleOperatorTargetNotFoundV1,
		nil,
	} {
		mapped := mapModuleHistoryReadErrorV1(readErr, closeErr)
		if mapped == nil || !strings.Contains(mapped.Error(), "(STORE_INVALID)") ||
			strings.Contains(mapped.Error(), "HISTORY_NOT_FOUND") ||
			strings.Contains(mapped.Error(), "TARGET_NOT_FOUND") {
			t.Fatalf("read=%v close=%v mapped=%v", readErr, closeErr, mapped)
		}
	}
}

func TestModuleOperatorSameExactPortSetRejectsDuplicates(t *testing.T) {
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	modelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV2,
	}
	for _, test := range []struct {
		name  string
		left  []moduleapi.PortRef
		right []moduleapi.PortRef
		want  bool
	}{
		{
			name:  "same set in different order",
			left:  []moduleapi.PortRef{contextPort, modelPort},
			right: []moduleapi.PortRef{modelPort, contextPort},
			want:  true,
		},
		{
			name:  "duplicate left",
			left:  []moduleapi.PortRef{contextPort, contextPort},
			right: []moduleapi.PortRef{contextPort, modelPort},
		},
		{
			name:  "duplicate right",
			left:  []moduleapi.PortRef{contextPort, modelPort},
			right: []moduleapi.PortRef{contextPort, contextPort},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleOperatorSameExactPortSetV1(test.left, test.right); got != test.want {
				t.Fatalf("same exact Port set=%t want %t", got, test.want)
			}
		})
	}
}

func TestModuleInspectRejectsUnboundCatalogEntryWithIncompleteProvides(t *testing.T) {
	const (
		sourceModelInstanceID = "model-dev-echo"
		modelInstanceID       = "model-dev-echo-unbound"
	)
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	_, control, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	entry, found := catalog.FindInstance(sourceModelInstanceID)
	if !found || entry.Activation.ModuleID != localEchoModuleID ||
		len(entry.Provides) != 1 ||
		entry.Provides[0].Name != moduleapi.PortNameModelGenerate {
		t.Fatalf("model Catalog Entry=%+v found=%v", entry, found)
	}
	entry.Activation.InstanceID = modelInstanceID
	catalog.Entries = append(catalog.Entries, entry)
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID == modelInstanceID {
				t.Fatal("model fixture unexpectedly has a Profile Binding")
			}
		}
	}

	sourceDirectory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		localEchoModuleID,
		localEchoModuleVersion,
	)
	stagedDirectory := filepath.Join(root, "multi-port-model")
	copyModuleApplyTestTreeV1(t, sourceDirectory, stagedDirectory)
	manifestPath := filepath.Join(stagedDirectory, moduleapi.ArtifactManifestPath)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provides = append(manifest.Provides, moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	})
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonicalManifest, err := moduleapi.CanonicalJSON(encodedManifest)
	if err != nil {
		t.Fatal(err)
	}
	parsedManifest, reparsedCanonical, err := moduleapi.ParseModuleManifestV1(
		canonicalManifest,
	)
	if err != nil || !bytes.Equal(reparsedCanonical, canonicalManifest) ||
		len(parsedManifest.Provides) != 2 {
		t.Fatalf("multi-Port manifest=%+v error=%v", parsedManifest, err)
	}
	if err := os.WriteFile(manifestPath, canonicalManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		stagedDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(canonicalManifest, files)
	if err != nil {
		t.Fatal(err)
	}
	artifactSize := uint64(len(canonicalManifest))
	for _, file := range files {
		artifactSize += uint64(len(file.Content))
	}
	installedDirectory := filepath.Join(artifactRoot, artifactDigest)
	copyModuleApplyTestTreeV1(t, stagedDirectory, installedDirectory)
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		installedDirectory,
		moduleapi.ArtifactMetadataPaths{},
		artifactDigest,
		artifactSize,
	); err != nil {
		t.Fatalf("verify multi-Port inspection artifact: %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleApplyJSONMediaType,
		canonicalManifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, putErr := store.PutContent(ctx, currentstore.ContentInput{
		Digest:         manifestRef,
		Kind:           currentstore.ContentModuleManifest,
		MediaType:      moduleApplyJSONMediaType,
		CanonicalBytes: canonicalManifest,
	})
	closeErr := store.Close()
	if joined := errors.Join(putErr, closeErr); joined != nil {
		t.Fatalf("store multi-Port Manifest: %v", joined)
	}

	for index := range catalog.Entries {
		if catalog.Entries[index].Activation.InstanceID == modelInstanceID {
			catalog.Entries[index].Activation.ArtifactDigest = artifactDigest
		}
	}
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var activationRows int64
	withCmdClosedFileDatabaseTamperV1(
		t,
		database,
		[]string{"runtime_catalog_generations_reject_update"},
		func(ctx context.Context, connection *sql.Conn) error {
			tx, err := connection.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			activationResult, err := tx.ExecContext(ctx, `
				INSERT INTO module_activations(
					activation_id, tenant_id, instance_id, installation_id,
					activation_revision, execution_class, adapter_identity, activated_at
				)
				SELECT ?, ?, ?, installation_id, ?, ?, ?, 1
				FROM module_installations
				WHERE module_id=? AND exact_version=?
			`,
				"activation-"+modelInstanceID,
				defaultTenantID,
				modelInstanceID,
				int64(entry.Activation.ActivationRevision),
				string(entry.Activation.ExecutionClass),
				entry.Activation.AdapterIdentity,
				localEchoModuleID,
				localEchoModuleVersion,
			)
			if err != nil {
				return err
			}
			activationRows, err = activationResult.RowsAffected()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE module_installations
				SET manifest_ref=?, artifact_digest=?
				WHERE module_id=? AND exact_version=?
			`, manifestRef, artifactDigest, localEchoModuleID, localEchoModuleVersion); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE runtime_catalog_generations
				SET canonical_json=?, digest=?
				WHERE generation_id=?
			`, catalogCanonical, catalogRef.Digest, catalogRef.GenerationID); err != nil {
				return err
			}
			return tx.Commit()
		},
	)
	if activationRows != 1 {
		_ = database.Close()
		t.Fatalf("stage unbound Activation rows=%d", activationRows)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := invokeModuleOperatorCommandV1([]string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", defaultTenantID,
		"--instance", modelInstanceID,
	})
	if err == nil || !strings.Contains(err.Error(), "(STORE_INVALID)") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func TestModuleInspectReverifiesCurrentArtifactAndRedactsPolicyBodies(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "enable-role.json"),
		role,
		1,
		0,
	)

	before := moduleOperatorStoreHashV1(t, databasePath)
	command := []string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", defaultTenantID,
		"--instance", role.InstanceID,
	}
	payload := runModuleOperatorCommandV1(t, command)
	assertCanonicalModuleOperatorOutputV1(t, payload)
	var result moduleInspectResultV1
	decodeModuleOperatorOutputV1(t, payload, &result)
	if result.SchemaVersion != moduleInspectResultSchemaV1 ||
		result.Basis.PointerRevision != 2 ||
		result.Source.ID != role.ModuleID ||
		result.Source.ExactVersion != moduleApplyTestVersion ||
		result.Source.ArtifactDigest != role.ArtifactDigest ||
		result.Source.InstallationID == "" ||
		!moduleapi.ValidSHA256(result.Source.ManifestRef) ||
		result.Activation.InstanceID != role.InstanceID ||
		result.Activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
		result.ArtifactSizeBytes != role.ArtifactSizeBytes ||
		result.Manifest.APIVersion != moduleapi.ModuleManifestAPIVersionV1 ||
		result.Manifest.Runtime.Mode != moduleapi.RuntimeModeRequestDeclarative ||
		result.Manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 ||
		len(result.CatalogProvides) != 1 ||
		result.CatalogProvides[0] != productionContextPort ||
		len(result.Bindings) != 1 ||
		result.Bindings[0].BindingTarget.ProfileID != moduleApplyTestProfileID ||
		result.Bindings[0].PortBindingIndex != 0 {
		t.Fatalf("module-inspect=%+v", result)
	}
	assertModuleOperatorOutputRedactedV1(
		t,
		payload,
		artifactRoot,
		role.ArtifactDirectory,
		moduleApplyRoleText,
		`"parameters"`,
		`"authority_ceiling"`,
		`"config"`,
	)
	second := runModuleOperatorCommandV1(t, command)
	if !bytes.Equal(payload, second) {
		t.Fatal("module-inspect output is not deterministic")
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != before {
		t.Fatal("module-inspect changed Current Store bytes")
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)

	stdout, stderr, err := invokeModuleOperatorCommandV1([]string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", defaultTenantID,
		"--instance", "instance-absent",
	})
	if err == nil || !strings.Contains(err.Error(), "INSTANCE_NOT_FOUND") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("missing inspect stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	stdout, stderr, err = invokeModuleOperatorCommandV1([]string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--instance", role.InstanceID,
	})
	if err == nil || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("tenant-less inspect stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}

	tamperedPath := filepath.Join(artifactRoot, role.ArtifactDigest, "tampered.txt")
	if err := os.WriteFile(tamperedPath, []byte("private-policy-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeFailure := moduleOperatorStoreHashV1(t, databasePath)
	stdout, stderr, err = invokeModuleOperatorCommandV1(command)
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_INVALID") ||
		strings.Contains(err.Error(), "private-policy-sentinel") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("tampered inspect stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != beforeFailure {
		t.Fatal("failed module-inspect changed Current Store bytes")
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func TestModuleDisableUsesExactApplyAndRejectsEnabledPlansBeforeMutation(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := filepath.Join(root, "enable-role.json")
	applyModuleOperatorDeclarativeFixtureV1(
		t,
		databasePath,
		artifactRoot,
		enablePath,
		role,
		1,
		0,
	)
	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, role.InstanceID, 2),
	)
	command := []string{
		"module-disable",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", disablePath,
	}
	payload := runModuleOperatorCommandV1(t, command)
	assertCanonicalModuleOperatorOutputV1(t, payload)
	var result moduleApplyResultV1
	decodeModuleOperatorOutputV1(t, payload, &result)
	assertDeclarativeModuleApplyResultV1(
		t,
		result,
		role,
		moduleApplyStatusApplied,
		moduleApplyDisabledV1,
		3,
	)
	retryPayload := runModuleOperatorCommandV1(t, command)
	assertCanonicalModuleOperatorOutputV1(t, retryPayload)
	var retry moduleApplyResultV1
	decodeModuleOperatorOutputV1(t, retryPayload, &retry)
	assertDeclarativeModuleApplyResultV1(
		t,
		retry,
		role,
		moduleApplyStatusAlreadyApplied,
		moduleApplyDisabledV1,
		3,
	)

	listPayload := runModuleOperatorCommandV1(t, []string{
		"module-list",
		"--db", databasePath,
		"--tenant", defaultTenantID,
	})
	var listed moduleListResultV1
	decodeModuleOperatorOutputV1(t, listPayload, &listed)
	for _, binding := range listed.Bindings {
		if binding.Activation.InstanceID == role.InstanceID {
			t.Fatal("disabled instance remains in a current Profile Binding")
		}
	}
	stdout, stderr, err := invokeModuleOperatorCommandV1([]string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", defaultTenantID,
		"--instance", role.InstanceID,
	})
	if err == nil || !strings.Contains(err.Error(), "INSTANCE_NOT_FOUND") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("disabled inspect stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}

	observer, err := currentstore.OpenReadOnlyObserver(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := observer.GetModuleInstallationByIdentity(
		context.Background(),
		role.ModuleID,
		moduleApplyTestVersion,
	)
	closeErr := observer.Close()
	if err != nil || closeErr != nil ||
		installation.ArtifactDigest != role.ArtifactDigest {
		t.Fatalf("disabled installation was not preserved: installation=%+v error=%v close=%v", installation, err, closeErr)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, role.ArtifactDigest)); err != nil {
		t.Fatalf("disabled artifact was not preserved: %v", err)
	}

	beforeRejected := moduleOperatorStoreHashV1(t, databasePath)
	stdout, stderr, err = invokeModuleOperatorCommandV1([]string{
		"module-disable",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", enablePath,
	})
	if err == nil || !strings.Contains(err.Error(), "PLAN_INVALID") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("ENABLED disable stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != beforeRejected {
		t.Fatal("module-disable mutated Store while rejecting an ENABLED plan")
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func applyModuleOperatorDeclarativeFixtureV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	planPath string,
	fixture moduleApplyDeclarativeFixtureV1,
	expectedPointer uint64,
	portBindingIndex uint32,
) {
	t.Helper()
	writeModuleApplyPlanFixtureV1(
		t,
		planPath,
		newEnabledDeclarativeModuleApplyPlanV1(
			t,
			fixture,
			expectedPointer,
			portBindingIndex,
		),
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	); err != nil {
		t.Fatalf("apply declarative module %s: %v", fixture.ModuleID, err)
	}
}

func publishModuleOperatorEmptyPureChatBindingsV1(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	profileFound := false
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == moduleApplyTestProfileID {
			control.Profiles[index].Bindings = []controlcontract.BindingSpec{}
			profileFound = true
			break
		}
	}
	if !profileFound {
		t.Fatal("Pure Chat profile is absent")
	}
	control.SnapshotID = "module-operator-empty-bindings-control"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "module-operator-empty-bindings-catalog"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
}

func invokeModuleOperatorCommandV1(
	args []string,
) (*bytes.Buffer, *bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), args, &stdout, &stderr)
	return &stdout, &stderr, err
}

func moduleHistoryCommandV1(
	databasePath string,
	basis controlcontract.PublishedBasis,
	profileID string,
) []string {
	command := []string{
		"module-history",
		"--db", databasePath,
		"--tenant", basis.TenantID,
		"--control-revision", strconv.FormatUint(basis.Control.Revision, 10),
		"--catalog-generation", strconv.FormatUint(basis.Catalog.Generation, 10),
	}
	if profileID != "" {
		command = append(command, "--profile", profileID)
	}
	return command
}

func runModuleOperatorCommandV1(t *testing.T, args []string) []byte {
	t.Helper()
	stdout, stderr, err := invokeModuleOperatorCommandV1(args)
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("module operator command %v stdout=%q stderr=%q error=%v", args, stdout.String(), stderr.String(), err)
	}
	return bytes.Clone(stdout.Bytes())
}

func decodeModuleOperatorOutputV1(t *testing.T, payload []byte, output any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		t.Fatalf("decode module operator output: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("module operator output has trailing JSON: %v", err)
	}
}

func assertCanonicalModuleOperatorOutputV1(t *testing.T, payload []byte) {
	t.Helper()
	if len(payload) < 2 || payload[len(payload)-1] != '\n' ||
		bytes.Contains(payload[:len(payload)-1], []byte{'\n'}) {
		t.Fatalf("module operator output is not one newline-terminated object: %q", payload)
	}
	body := payload[:len(payload)-1]
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil || !bytes.Equal(body, canonical) {
		t.Fatalf("module operator output is not RFC 8785 canonical: %v", err)
	}
}

func assertModuleOperatorOutputRedactedV1(
	t *testing.T,
	payload []byte,
	forbidden ...string,
) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && bytes.Contains(payload, []byte(value)) {
			t.Fatalf("module operator output disclosed forbidden value %q", value)
		}
	}
}

func assertModuleOperatorBindingsMatchControlV1(
	t *testing.T,
	databasePath string,
	wantBasis controlcontract.PublishedBasis,
	actual []moduleOperatorBindingV1,
	profileFilter string,
) {
	t.Helper()
	observer, err := currentstore.OpenReadOnlyObserver(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, _, err := observer.LoadPublishedBasis(
		context.Background(),
		wantBasis.TenantID,
	)
	closeErr := observer.Close()
	if err != nil || closeErr != nil || basis != wantBasis {
		t.Fatalf("read exact Control: basis=%+v error=%v close=%v", basis, err, closeErr)
	}
	wantedBindings := make([]struct {
		profileID string
		binding   controlcontract.BindingSpec
		index     uint32
	}, 0)
	for _, profile := range control.Profiles {
		if profileFilter != "" && profile.Profile.ID != profileFilter {
			continue
		}
		indices := make(map[string]uint32)
		for _, binding := range profile.Bindings {
			key, err := binding.Port.CanonicalKey()
			if err != nil {
				t.Fatal(err)
			}
			index := indices[key]
			indices[key] = index + 1
			wantedBindings = append(wantedBindings, struct {
				profileID string
				binding   controlcontract.BindingSpec
				index     uint32
			}{profile.Profile.ID, binding, index})
		}
	}
	if len(actual) != len(wantedBindings) {
		t.Fatalf("binding count=%d want %d", len(actual), len(wantedBindings))
	}
	for index, want := range wantedBindings {
		got := actual[index]
		if got.BindingTarget.Kind != moduleApplyBindingTargetProfileV1 ||
			got.BindingTarget.ProfileID != want.profileID ||
			got.Port != want.binding.Port ||
			got.PortBindingIndex != want.index ||
			got.Activation.InstanceID != want.binding.InstanceID ||
			got.ConfigRef != want.binding.ConfigRef ||
			got.AuthorityCeilingRef != want.binding.AuthorityCeilingRef ||
			got.FailurePolicy != want.binding.FailurePolicy ||
			!equalModuleOperatorStringsV1(got.StaticContextRefs, want.binding.StaticContextRefs) {
			t.Fatalf("binding[%d]=%+v want profile=%s binding=%+v port index=%d", index, got, want.profileID, want.binding, want.index)
		}
	}
}

func equalModuleOperatorStringsV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func moduleOperatorContainsInstanceV1(
	bindings []moduleOperatorBindingV1,
	instanceID string,
) bool {
	for _, binding := range bindings {
		if binding.Activation.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func moduleOperatorStoreHashV1(t *testing.T, databasePath string) [sha256.Size]byte {
	t.Helper()
	payload, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(payload)
}

func assertModuleOperatorNoSQLiteSidecarsV1(t *testing.T, databasePath string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(databasePath + suffix); err == nil || !os.IsNotExist(err) {
			t.Fatalf("module operator left SQLite sidecar %q: %v", suffix, err)
		}
	}
}

func assertModuleHistoryStoreInvalidV1(
	t *testing.T,
	databasePath string,
	basis controlcontract.PublishedBasis,
) {
	t.Helper()
	stdout, stderr, err := invokeModuleOperatorCommandV1(
		moduleHistoryCommandV1(
			databasePath,
			basis,
			moduleApplyTestProfileID,
		),
	)
	if err == nil || !strings.Contains(err.Error(), "(STORE_INVALID)") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
	}
	assertModuleOperatorNoSQLiteSidecarsV1(t, databasePath)
}

func copyModuleOperatorDatabaseV1(
	t *testing.T,
	sourcePath string,
	destinationPath string,
) {
	t.Helper()
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireModuleOperatorDamagedRowV1(
	t *testing.T,
	result sql.Result,
	err error,
	operation string,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("%s rows=%d error=%v", operation, affected, err)
	}
}
