package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestW65AModuleArtifactIngressCLIEndToEndAndExactOfflineRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newModuleArtifactIngressCLIEndToEndFixtureV1(t)

	var firstOutput bytes.Buffer
	dependencies := productionModuleArtifactIngressDependenciesV1()
	productionIngress := dependencies.ingress
	var operationErr error
	dependencies.ingress = func(
		ctx context.Context,
		request moduleartifactingress.RequestV1,
		store moduleArtifactIngressStoreV1,
	) (moduleArtifactIngressOperationResultV1, error) {
		result, err := productionIngress(ctx, request, store)
		operationErr = err
		return result, err
	}
	if err := runModuleArtifactIngressWithDependenciesV1(
		ctx,
		fixture.commandArgs(),
		&firstOutput,
		&bytes.Buffer{},
		dependencies,
	); err != nil {
		var causes []string
		for cause := operationErr; cause != nil; cause = errors.Unwrap(cause) {
			causes = append(causes, cause.Error())
		}
		t.Fatalf("first production module-artifact-ingress: %v; operation causes=%q", err, causes)
	}
	first := parseModuleArtifactIngressCLIResultV1(t, firstOutput.Bytes())
	fixture.assertCommandResult(t, first, false)
	assertModuleArtifactIngressCLIOutputPathFreeV1(t, firstOutput.Bytes(), fixture)

	store := openModuleArtifactIngressCLIStoreV1(t, fixture.databasePath)
	selection := fixture.storeSelection()
	admission, found, err := store.GetModuleArtifactAdmissionBySelectionV1(ctx, selection)
	if err != nil || !found {
		_ = store.Close()
		t.Fatalf("read committed ingress Admission: found=%t err=%v", found, err)
	}
	installed, err := store.IsModuleArtifactInstalledV1(ctx, fixture.artifactDigest)
	if err != nil {
		_ = store.Close()
		t.Fatalf("read ingress-only installation state: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Current Store after Admission read: %v", err)
	}
	if installed || admission.AdmissionID != first.AdmissionID ||
		admission.Record.SourceID != fixture.sourceID ||
		admission.Record.SnapshotID != fixture.snapshotID ||
		admission.Record.Module != fixture.module ||
		admission.Record.ArtifactDigest != fixture.artifactDigest ||
		admission.Record.PackagePath != fixture.packagePath ||
		admission.Artifact.ArtifactSizeBytes != fixture.artifactSizeBytes ||
		admission.Artifact.CoveredFileCount != fixture.coveredFileCount ||
		!bytes.Equal(admission.Artifact.ManifestCanonical, fixture.manifestCanonical) {
		t.Fatalf("committed ingress Admission = %+v", admission)
	}
	fixture.assertPublishedArtifactBytes(t)

	advancedSnapshotID := fixture.advanceSourceHead(t)
	if advancedSnapshotID == fixture.snapshotID {
		t.Fatal("source head did not advance away from the admitted Snapshot")
	}
	if err := os.RemoveAll(fixture.sourceRoot); err != nil {
		t.Fatalf("remove transient source before exact retry: %v", err)
	}
	if _, err := os.Stat(fixture.sourceRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists before exact retry: %v", err)
	}

	var retryOutput bytes.Buffer
	if err := runModuleArtifactIngress(
		ctx,
		fixture.commandArgs(),
		&retryOutput,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("exact ingress retry after head advance and source removal: %v", err)
	}
	retried := parseModuleArtifactIngressCLIResultV1(t, retryOutput.Bytes())
	fixture.assertCommandResult(t, retried, true)
	assertModuleArtifactIngressCLIOutputPathFreeV1(t, retryOutput.Bytes(), fixture)
	if retried.AdmissionID != first.AdmissionID ||
		retried.ArtifactDigest != first.ArtifactDigest ||
		retried.ManifestRef != first.ManifestRef ||
		retried.ArtifactSizeBytes != first.ArtifactSizeBytes ||
		retried.CoveredFileCount != first.CoveredFileCount {
		t.Fatalf("exact retry result=%+v, first=%+v", retried, first)
	}

	store = openModuleArtifactIngressCLIStoreV1(t, fixture.databasePath)
	retriedAdmission, found, readErr := store.GetModuleArtifactAdmissionBySelectionV1(
		ctx,
		selection,
	)
	source, sourceErr := store.GetModuleSource(ctx, fixture.sourceID)
	closeErr := store.Close()
	if readErr != nil || !found || sourceErr != nil || closeErr != nil {
		t.Fatalf(
			"read exact retry closure: found=%t admissionErr=%v sourceErr=%v closeErr=%v",
			found,
			readErr,
			sourceErr,
			closeErr,
		)
	}
	if retriedAdmission.AdmissionID != admission.AdmissionID ||
		!bytes.Equal(retriedAdmission.Canonical, admission.Canonical) {
		t.Fatal("exact retry did not resolve the original immutable Admission")
	}
	if source.CurrentSnapshotID != advancedSnapshotID ||
		source.CurrentSnapshotID == fixture.snapshotID {
		t.Fatalf("source head rolled back during exact retry: %+v", source)
	}
	fixture.assertPublishedArtifactBytes(t)
	fixture.assertOfflineBackupRoundTrip(t, admission)
}

type moduleArtifactIngressCLIEndToEndFixtureV1 struct {
	databasePath        string
	artifactRoot        string
	sourceRoot          string
	sourceID            string
	snapshotID          string
	module              moduleapi.Ref
	artifactDigest      string
	artifactSizeBytes   uint64
	coveredFileCount    uint64
	packagePath         string
	manifestCanonical   []byte
	payloadRelativePath string
	payload             []byte
}

func newModuleArtifactIngressCLIEndToEndFixtureV1(
	t *testing.T,
) moduleArtifactIngressCLIEndToEndFixtureV1 {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatalf("initialize Current Store: %v", err)
	}
	sourceRoot := filepath.Join(root, "w65a-private-source-marker")
	artifactRoot := filepath.Join(root, "w65a-private-artifact-root-marker")
	packagePath := "packages/w65a-private-package-path-marker"
	packageDirectory := filepath.Join(sourceRoot, filepath.FromSlash(packagePath))
	if err := os.MkdirAll(packageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	module := moduleapi.Ref{
		ID:      "freeagent.test.ingress.cli",
		Version: "1.0.0",
	}
	manifestCanonical := moduleArtifactIngressCLIManifestV1(t, module)
	if err := os.WriteFile(
		filepath.Join(packageDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	payloadRelativePath := "payload/nested.txt"
	payload := []byte("W6-5A server-owned ingress payload\n")
	if err := os.MkdirAll(
		filepath.Join(packageDirectory, filepath.Dir(filepath.FromSlash(payloadRelativePath))),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(packageDirectory, filepath.FromSlash(payloadRelativePath)),
		payload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	report, err := moduleconformance.VerifyDirectory(ctx, packageDirectory)
	if err != nil {
		t.Fatalf("verify source artifact fixture: %v", err)
	}
	if report.Module.ID != module.ID || report.Module.ExactVersion != module.Version {
		t.Fatalf("source artifact identity=%+v, want=%+v", report.Module, module)
	}

	sourceID := "w65a.local"
	entry := moduleapi.ModuleDiscoveryEntryV1{
		Module:            module,
		ArtifactDigest:    report.ArtifactDigest,
		ArtifactSizeBytes: report.ArtifactSizeBytes,
		PackagePath:       packagePath,
	}
	indexCanonical := moduleArtifactIngressCLIIndexV1(t, sourceID, entry)
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(sourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                sourceID,
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"freeagent.test.ingress"},
			MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
			MaxPackageBytes:         report.ArtifactSizeBytes + 4096,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatalf("freeze LOCAL_DIRECTORY+DENY Source Policy: %v", err)
	}
	store := openModuleArtifactIngressCLIStoreV1(t, databasePath)
	if _, err := store.RegisterModuleSource(
		ctx,
		currentstore.RegisterModuleSourceInput{PolicyCanonical: policyCanonical},
	); err != nil {
		_ = store.Close()
		t.Fatalf("register LOCAL_DIRECTORY+DENY Source: %v", err)
	}
	refreshBasis, err := store.ReadModuleSourceRefreshBasis(ctx, sourceID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(ctx, refreshBasis, indexCanonical)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("commit discovery Snapshot: err=%v closeErr=%v", err, closeErr)
	}

	return moduleArtifactIngressCLIEndToEndFixtureV1{
		databasePath:        databasePath,
		artifactRoot:        artifactRoot,
		sourceRoot:          sourceRoot,
		sourceID:            sourceID,
		snapshotID:          snapshot.SnapshotID,
		module:              module,
		artifactDigest:      report.ArtifactDigest,
		artifactSizeBytes:   report.ArtifactSizeBytes,
		coveredFileCount:    report.CoveredFileCount,
		packagePath:         packagePath,
		manifestCanonical:   bytes.Clone(manifestCanonical),
		payloadRelativePath: payloadRelativePath,
		payload:             bytes.Clone(payload),
	}
}

func moduleArtifactIngressCLIManifestV1(t *testing.T, module moduleapi.Ref) []byte {
	t.Helper()
	raw, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         module.ID,
		Version:    module.Version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "freeagent.test.ingress.cli/v1",
		},
		Provides: []moduleapi.PortRef{{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, rebuilt, err := moduleapi.ParseModuleManifestV1(canonical); err != nil ||
		!bytes.Equal(rebuilt, canonical) {
		t.Fatalf("freeze source Manifest: %v", err)
	}
	return canonical
}

func moduleArtifactIngressCLIIndexV1(
	t *testing.T,
	sourceID string,
	entry moduleapi.ModuleDiscoveryEntryV1,
) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      sourceID,
			Entries:       []moduleapi.ModuleDiscoveryEntryV1{entry},
		},
	)
	if err != nil {
		t.Fatalf("freeze discovery Index: %v", err)
	}
	return canonical
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) commandArgs() []string {
	return []string{
		"--enable-module-artifact-ingress",
		"--db", fixture.databasePath,
		"--artifact-root", fixture.artifactRoot,
		"--source-root", fixture.sourceRoot,
		"--source-id", fixture.sourceID,
		"--snapshot-id", fixture.snapshotID,
		"--module-id", fixture.module.ID,
		"--exact-version", fixture.module.Version,
		"--artifact-digest", fixture.artifactDigest,
	}
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) storeSelection() currentstore.ModuleArtifactIngressSelectionV1 {
	return currentstore.ModuleArtifactIngressSelectionV1{
		SourceID:       fixture.sourceID,
		SnapshotID:     fixture.snapshotID,
		Module:         fixture.module,
		ArtifactDigest: fixture.artifactDigest,
	}
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) assertCommandResult(
	t *testing.T,
	result moduleArtifactIngressCommandResultV1,
	wantReused bool,
) {
	t.Helper()
	if result.SchemaVersion != moduleArtifactIngressResultSchemaV1 ||
		result.Status != "RECORDED" ||
		!moduleapi.ValidSHA256(result.AdmissionID) ||
		result.SourceID != fixture.sourceID ||
		result.SnapshotID != fixture.snapshotID ||
		result.Module != fixture.module ||
		result.ArtifactDigest != fixture.artifactDigest ||
		result.ArtifactSizeBytes != fixture.artifactSizeBytes ||
		!moduleapi.ValidSHA256(result.ManifestRef) ||
		result.CoveredFileCount != fixture.coveredFileCount ||
		result.PhysicalReused != wantReused {
		t.Fatalf("module-artifact-ingress result = %+v, reused=%t", result, wantReused)
	}
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) assertPublishedArtifactBytes(
	t *testing.T,
) {
	t.Helper()
	directory := filepath.Join(fixture.artifactRoot, fixture.artifactDigest)
	manifest, err := os.ReadFile(filepath.Join(directory, moduleapi.ArtifactManifestPath))
	if err != nil {
		t.Fatalf("read published Manifest: %v", err)
	}
	payload, err := os.ReadFile(
		filepath.Join(directory, filepath.FromSlash(fixture.payloadRelativePath)),
	)
	if err != nil {
		t.Fatalf("read published nested payload: %v", err)
	}
	if !bytes.Equal(manifest, fixture.manifestCanonical) ||
		!bytes.Equal(payload, fixture.payload) {
		t.Fatal("server-owned artifact bytes differ from the verified source fixture")
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		context.Background(),
		directory,
		moduleapi.ArtifactMetadataPaths{},
		fixture.artifactDigest,
		fixture.artifactSizeBytes,
	); err != nil {
		t.Fatalf("verify published content-addressed artifact: %v", err)
	}
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) advanceSourceHead(
	t *testing.T,
) string {
	t.Helper()
	ctx := context.Background()
	store := openModuleArtifactIngressCLIStoreV1(t, fixture.databasePath)
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, fixture.sourceID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	advancedIndex := moduleArtifactIngressCLIIndexV1(
		t,
		fixture.sourceID,
		moduleapi.ModuleDiscoveryEntryV1{
			Module:            fixture.module,
			ArtifactDigest:    fixture.artifactDigest,
			ArtifactSizeBytes: fixture.artifactSizeBytes,
			PackagePath:       "packages/advanced-head-without-source",
		},
	)
	snapshot, err := store.CommitModuleSourceRefresh(ctx, basis, advancedIndex)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("advance Source head: err=%v closeErr=%v", err, closeErr)
	}
	return snapshot.SnapshotID
}

func (fixture moduleArtifactIngressCLIEndToEndFixtureV1) assertOfflineBackupRoundTrip(
	t *testing.T,
	wantAdmission currentstore.ModuleArtifactAdmissionV1,
) {
	t.Helper()
	ctx := context.Background()
	bundle := filepath.Join(t.TempDir(), "w65a-ingress-only.bundle")
	created, err := currentbackup.CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"freeagent-w65a-module-artifact-ingress-e2e/v1",
	)
	if err != nil {
		t.Fatalf("create offline ingress-only backup: %v", err)
	}
	if created.ArtifactCount != 1 || len(created.Artifacts) != 1 ||
		created.Artifacts[0].Digest != fixture.artifactDigest {
		t.Fatalf("offline ingress-only backup artifacts = %+v", created.Artifacts)
	}
	verified, err := currentbackup.VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("verify offline ingress-only backup: %v", err)
	}
	if verified.ArtifactCount != created.ArtifactCount ||
		len(verified.Artifacts) != 1 ||
		verified.Artifacts[0].Digest != fixture.artifactDigest {
		t.Fatalf("verified offline ingress-only backup = %+v", verified)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := currentbackup.RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("restore offline ingress-only backup: %v", err)
	}
	restoredStore := openModuleArtifactIngressCLIStoreV1(t, restoredDatabase)
	restoredAdmission, found, readErr := restoredStore.GetModuleArtifactAdmissionBySelectionV1(
		ctx,
		fixture.storeSelection(),
	)
	installed, installedErr := restoredStore.IsModuleArtifactInstalledV1(
		ctx,
		fixture.artifactDigest,
	)
	closeErr := restoredStore.Close()
	if readErr != nil || !found || installedErr != nil || closeErr != nil {
		t.Fatalf(
			"read restored ingress Admission: found=%t readErr=%v installedErr=%v closeErr=%v",
			found,
			readErr,
			installedErr,
			closeErr,
		)
	}
	if installed || restoredAdmission.AdmissionID != wantAdmission.AdmissionID ||
		!bytes.Equal(restoredAdmission.Canonical, wantAdmission.Canonical) ||
		!bytes.Equal(
			restoredAdmission.Artifact.ManifestCanonical,
			wantAdmission.Artifact.ManifestCanonical,
		) {
		t.Fatal("restored Store does not contain the exact ingress Admission closure")
	}
	restoredFixture := fixture
	restoredFixture.artifactRoot = restoredArtifacts
	restoredFixture.assertPublishedArtifactBytes(t)
}

func openModuleArtifactIngressCLIStoreV1(
	t *testing.T,
	databasePath string,
) *currentstore.Store {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open Current Store: %v", err)
	}
	return store
}

func parseModuleArtifactIngressCLIResultV1(
	t *testing.T,
	output []byte,
) moduleArtifactIngressCommandResultV1 {
	t.Helper()
	if len(output) < 2 || output[len(output)-1] != '\n' ||
		bytes.Contains(output[:len(output)-1], []byte{'\n'}) {
		t.Fatalf("command output is not one newline-terminated JSON object: %q", output)
	}
	payload := output[:len(output)-1]
	canonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil || !bytes.Equal(canonical, payload) {
		t.Fatalf("command output is not exact canonical JSON: %v, %q", err, output)
	}
	var result moduleArtifactIngressCommandResultV1
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("decode command output: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]struct{}{
		"schema_version": {}, "status": {}, "admission_id": {},
		"source_id": {}, "snapshot_id": {}, "module": {},
		"artifact_digest": {}, "artifact_size_bytes": {},
		"manifest_ref": {}, "covered_file_count": {}, "physical_reused": {},
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("command output fields=%v", fields)
	}
	for field := range fields {
		if _, ok := wantFields[field]; !ok {
			t.Fatalf("command output contains unexpected field %q", field)
		}
	}
	return result
}

func assertModuleArtifactIngressCLIOutputPathFreeV1(
	t *testing.T,
	output []byte,
	fixture moduleArtifactIngressCLIEndToEndFixtureV1,
) {
	t.Helper()
	for _, forbiddenField := range []string{
		"source_root", "artifact_root", "database_path", "package_path",
	} {
		if bytes.Contains(output, []byte(`"`+forbiddenField+`"`)) {
			t.Fatalf("command output contains path field %q: %s", forbiddenField, output)
		}
	}
	for _, forbiddenValue := range []string{
		fixture.databasePath,
		fixture.artifactRoot,
		fixture.sourceRoot,
		fixture.packagePath,
		filepath.ToSlash(fixture.databasePath),
		filepath.ToSlash(fixture.artifactRoot),
		filepath.ToSlash(fixture.sourceRoot),
	} {
		encoded, err := json.Marshal(forbiddenValue)
		if err != nil {
			t.Fatal(err)
		}
		encoded = encoded[1 : len(encoded)-1]
		if len(encoded) != 0 && bytes.Contains(output, encoded) {
			t.Fatalf("command output leaked local path %q: %s", forbiddenValue, output)
		}
	}
	if strings.Contains(string(output), "w65a-private-") {
		t.Fatalf("command output leaked a private fixture path marker: %s", output)
	}
}
