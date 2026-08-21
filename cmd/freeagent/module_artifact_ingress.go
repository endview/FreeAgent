package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleArtifactIngressResultSchemaV1 = "freeagent.module-artifact-ingress-result/v1"

type moduleArtifactIngressStoreV1 interface {
	ReadModuleArtifactIngressBasisV1(context.Context, currentstore.ModuleArtifactIngressSelectionV1) (currentstore.ModuleArtifactIngressBasisV1, error)
	CommitModuleArtifactIngressV1(context.Context, currentstore.ModuleArtifactIngressBasisV1, []byte, uint64) (currentstore.ModuleArtifactV1, currentstore.ModuleArtifactAdmissionV1, error)
	IsModuleArtifactInstalledV1(context.Context, string) (bool, error)
	Close() error
}

type moduleArtifactIngressExistingStoreV1 interface {
	GetModuleArtifactAdmissionBySelectionV1(
		context.Context,
		currentstore.ModuleArtifactIngressSelectionV1,
	) (currentstore.ModuleArtifactAdmissionV1, bool, error)
}

type moduleArtifactIngressOperationResultV1 struct {
	Artifact  currentstore.ModuleArtifactV1
	Admission currentstore.ModuleArtifactAdmissionV1
	Reused    bool
}

type moduleArtifactIngressDependenciesV1 struct {
	openStore func(context.Context, string) (moduleArtifactIngressStoreV1, error)
	ingress   func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error)
}

type moduleArtifactIngressCommandResultV1 struct {
	SchemaVersion     string        `json:"schema_version"`
	Status            string        `json:"status"`
	AdmissionID       string        `json:"admission_id"`
	SourceID          string        `json:"source_id"`
	SnapshotID        string        `json:"snapshot_id"`
	Module            moduleapi.Ref `json:"module"`
	ArtifactDigest    string        `json:"artifact_digest"`
	ArtifactSizeBytes uint64        `json:"artifact_size_bytes"`
	ManifestRef       string        `json:"manifest_ref"`
	CoveredFileCount  uint64        `json:"covered_file_count"`
	PhysicalReused    bool          `json:"physical_reused"`
}

func productionModuleArtifactIngressDependenciesV1() moduleArtifactIngressDependenciesV1 {
	return moduleArtifactIngressDependenciesV1{
		openStore: func(ctx context.Context, path string) (moduleArtifactIngressStoreV1, error) {
			return currentstore.OpenExistingCurrentStore(ctx, path)
		},
		ingress: runModuleArtifactIngressOperationV1,
	}
}

func runModuleArtifactIngress(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModuleArtifactIngressWithDependenciesV1(
		ctx, args, stdout, stderr, productionModuleArtifactIngressDependenciesV1(),
	)
}

func runModuleArtifactIngressWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleArtifactIngressDependenciesV1,
) (returnErr error) {
	flags := newFlagSet("module-artifact-ingress", io.Discard)
	enabled := flags.Bool("enable-module-artifact-ingress", false, "explicitly enable trusted local artifact ingress")
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "server-owned content-addressed artifact root")
	sourceRoot := flags.String("source-root", "", "exact local source root bound by Source Policy")
	sourceID := flags.String("source-id", "", "registered Source identity")
	snapshotID := flags.String("snapshot-id", "", "exact Store-owned discovery Snapshot ID")
	moduleID := flags.String("module-id", "", "exact discovered module identity")
	exactVersion := flags.String("exact-version", "", "exact discovered module version")
	artifactDigest := flags.String("artifact-digest", "", "exact discovered artifact SHA-256")
	if err := flags.Parse(args); err != nil {
		return moduleArtifactIngressCommandFailureV1("INVALID_FLAGS")
	}
	if !*enabled {
		return moduleArtifactIngressCommandFailureV1("INGRESS_DISABLED")
	}
	selection := moduleartifactingress.SelectionV1{
		SourceID:       *sourceID,
		SnapshotID:     *snapshotID,
		Module:         moduleapi.Ref{ID: *moduleID, Version: *exactVersion},
		ArtifactDigest: *artifactDigest,
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.openStore == nil ||
		dependencies.ingress == nil || !exactNonEmptyFlag(*databasePath) ||
		!exactNonEmptyFlag(*artifactRoot) || !exactNonEmptyFlag(*sourceRoot) ||
		!exactNonEmptyFlag(*sourceID) || !moduleapi.ValidSHA256(*snapshotID) ||
		!exactNonEmptyFlag(*moduleID) || !exactNonEmptyFlag(*exactVersion) ||
		!moduleapi.ValidSHA256(*artifactDigest) || selection.Module.Validate() != nil {
		return moduleArtifactIngressCommandFailureV1("INVALID_FLAGS")
	}
	if !moduleArtifactIngressPathsDisjointV1(
		*databasePath,
		*sourceRoot,
		*artifactRoot,
	) {
		return moduleArtifactIngressCommandFailureV1("INVALID_FLAGS")
	}
	store, err := dependencies.openStore(ctx, *databasePath)
	if err != nil {
		return moduleArtifactIngressCommandFailureFromErrorV1(err)
	}
	if store == nil {
		return moduleArtifactIngressCommandFailureV1("INTERNAL_ERROR")
	}
	closed := false
	closeStore := func() error {
		if closed {
			return nil
		}
		closed = true
		return store.Close()
	}
	defer func() {
		if closeErr := closeStore(); closeErr != nil && returnErr == nil {
			returnErr = moduleArtifactIngressCommandFailureV1("STORE_INVALID")
		}
	}()
	result, err := dependencies.ingress(ctx, moduleartifactingress.RequestV1{
		SourceRoot: *sourceRoot, ArtifactRoot: *artifactRoot, Selection: selection,
	}, store)
	if err != nil {
		return moduleArtifactIngressCommandFailureFromErrorV1(err)
	}
	if result.Admission.AdmissionID == "" ||
		result.Admission.Record.SourceID != selection.SourceID ||
		result.Admission.Record.SnapshotID != selection.SnapshotID ||
		result.Admission.Record.Module != selection.Module ||
		result.Admission.Record.ArtifactDigest != selection.ArtifactDigest ||
		result.Artifact.ArtifactDigest != selection.ArtifactDigest ||
		result.Admission.Artifact.ArtifactDigest != selection.ArtifactDigest {
		return moduleArtifactIngressCommandFailureV1("STORE_INVALID")
	}
	// A successful response is emitted only after the single Store owner has
	// durably released its resources. This keeps a Close failure from being
	// paired with a misleading success object on stdout.
	if err := closeStore(); err != nil {
		return moduleArtifactIngressCommandFailureV1("STORE_INVALID")
	}
	if err := writeCanonicalModuleCommandJSON(stdout, moduleArtifactIngressCommandResultV1{
		SchemaVersion:     moduleArtifactIngressResultSchemaV1,
		Status:            "RECORDED",
		AdmissionID:       result.Admission.AdmissionID,
		SourceID:          selection.SourceID,
		SnapshotID:        selection.SnapshotID,
		Module:            selection.Module,
		ArtifactDigest:    result.Artifact.ArtifactDigest,
		ArtifactSizeBytes: result.Artifact.ArtifactSizeBytes,
		ManifestRef:       result.Artifact.ManifestRef,
		CoveredFileCount:  result.Artifact.CoveredFileCount,
		PhysicalReused:    result.Reused,
	}); err != nil {
		return moduleArtifactIngressCommandFailureV1("OUTPUT_FAILED")
	}
	return nil
}

func moduleArtifactIngressPathsDisjointV1(databasePath, sourceRoot, artifactRoot string) bool {
	database, _, ok := inspectModuleArtifactIngressPathV1(databasePath, false, false)
	if !ok {
		return false
	}
	source, _, ok := inspectModuleArtifactIngressPathV1(sourceRoot, true, true)
	if !ok {
		return false
	}
	artifacts, _, ok := inspectModuleArtifactIngressPathV1(artifactRoot, true, false)
	if !ok {
		return false
	}
	resolved := []string{database, source, artifacts}
	for left := 0; left < len(resolved); left++ {
		for right := left + 1; right < len(resolved); right++ {
			if samePath(resolved[left], resolved[right]) ||
				pathContains(resolved[left], resolved[right]) ||
				pathContains(resolved[right], resolved[left]) {
				return false
			}
		}
	}
	return true
}

func inspectModuleArtifactIngressPathV1(
	input string,
	wantDirectory bool,
	allowMissing bool,
) (string, bool, bool) {
	if input == "" || input != strings.TrimSpace(input) ||
		input != moduleapi.CanonicalText(input) || !filepath.IsAbs(input) ||
		moduleArtifactIngressUnsafeWindowsNamespaceV1(input) {
		return "", false, false
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", false, false
	}
	absolute = filepath.Clean(absolute)
	if !moduleArtifactIngressExactAbsoluteV1(input, absolute) || filepath.Dir(absolute) == absolute {
		return "", false, false
	}
	before, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return absolute, false, true
	}
	if err != nil || before.Mode()&os.ModeSymlink != 0 ||
		moduleArtifactIngressPathSpecialV1(before) || before.IsDir() != wantDirectory ||
		(!wantDirectory && !before.Mode().IsRegular()) {
		return "", false, false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !samePath(absolute, resolved) {
		return "", false, false
	}
	after, err := os.Stat(resolved)
	if err != nil || after.IsDir() != wantDirectory || !os.SameFile(before, after) ||
		moduleArtifactIngressPathSpecialV1(after) ||
		(!wantDirectory && (!after.Mode().IsRegular() || !moduleArtifactIngressSingleLinkFileV1(absolute))) {
		return "", false, false
	}
	return filepath.Clean(resolved), true, true
}

func moduleArtifactIngressExactAbsoluteV1(input, absolute string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(input, absolute)
	}
	return input == absolute
}

func moduleArtifactIngressUnsafeWindowsNamespaceV1(input string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(normalized, `\\`) || strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) || strings.HasPrefix(lower, `\\.\`) ||
		strings.HasPrefix(filepath.VolumeName(normalized), `\\`) ||
		strings.Contains(strings.TrimPrefix(normalized, filepath.VolumeName(normalized)), ":")
}

func runModuleArtifactIngressOperationV1(
	ctx context.Context,
	request moduleartifactingress.RequestV1,
	store moduleArtifactIngressStoreV1,
) (moduleArtifactIngressOperationResultV1, error) {
	result, err := moduleartifactingress.IngressV1(
		ctx,
		request,
		moduleartifactingress.StoreCallbacksV1[
			currentstore.ModuleArtifactIngressBasisV1,
			currentstore.ModuleArtifactV1,
			currentstore.ModuleArtifactAdmissionV1,
		]{
			Existing: func(ctx context.Context, selection moduleartifactingress.SelectionV1) (currentstore.ModuleArtifactV1, currentstore.ModuleArtifactAdmissionV1, moduleartifactingress.ExistingViewV1, bool, error) {
				existingStore, ok := store.(moduleArtifactIngressExistingStoreV1)
				if !ok {
					return currentstore.ModuleArtifactV1{}, currentstore.ModuleArtifactAdmissionV1{}, moduleartifactingress.ExistingViewV1{}, false,
						errors.New("module artifact ingress Store lacks exact admission lookup")
				}
				admission, found, err := existingStore.GetModuleArtifactAdmissionBySelectionV1(
					ctx, currentstore.ModuleArtifactIngressSelectionV1{
						SourceID:       selection.SourceID,
						SnapshotID:     selection.SnapshotID,
						Module:         selection.Module,
						ArtifactDigest: selection.ArtifactDigest,
					},
				)
				if err != nil || !found {
					return currentstore.ModuleArtifactV1{}, currentstore.ModuleArtifactAdmissionV1{}, moduleartifactingress.ExistingViewV1{}, found, err
				}
				installed, err := store.IsModuleArtifactInstalledV1(ctx, admission.Artifact.ArtifactDigest)
				if err != nil {
					return currentstore.ModuleArtifactV1{}, currentstore.ModuleArtifactAdmissionV1{}, moduleartifactingress.ExistingViewV1{}, false, err
				}
				return admission.Artifact, admission, moduleartifactingress.ExistingViewV1{
					Module:            admission.Artifact.Module,
					ArtifactDigest:    admission.Artifact.ArtifactDigest,
					ArtifactSizeBytes: admission.Artifact.ArtifactSizeBytes,
					CoveredFileCount:  admission.Artifact.CoveredFileCount,
					Installed:         installed,
				}, true, nil
			},
			Read: func(ctx context.Context, selection moduleartifactingress.SelectionV1) (currentstore.ModuleArtifactIngressBasisV1, moduleartifactingress.BasisViewV1, error) {
				basis, err := store.ReadModuleArtifactIngressBasisV1(ctx, currentstore.ModuleArtifactIngressSelectionV1{
					SourceID:       selection.SourceID,
					SnapshotID:     selection.SnapshotID,
					Module:         selection.Module,
					ArtifactDigest: selection.ArtifactDigest,
				})
				if err != nil {
					return currentstore.ModuleArtifactIngressBasisV1{}, moduleartifactingress.BasisViewV1{}, err
				}
				installed, err := store.IsModuleArtifactInstalledV1(ctx, basis.Entry.ArtifactDigest)
				if err != nil {
					return currentstore.ModuleArtifactIngressBasisV1{}, moduleartifactingress.BasisViewV1{}, err
				}
				return basis, moduleartifactingress.BasisViewV1{
					SourceID:                    basis.Source.SourceID,
					SourcePolicyID:              basis.Source.PolicyID,
					SourcePolicyCanonical:       append([]byte(nil), basis.Source.PolicyCanonical...),
					SourcePolicyRevision:        basis.Source.PolicyRevision,
					SnapshotID:                  basis.Snapshot.SnapshotID,
					SnapshotSourcePolicyID:      basis.Snapshot.SourcePolicyID,
					SnapshotObservationRevision: basis.Snapshot.ObservationRevision,
					EntryOrdinal:                basis.EntryOrdinal,
					Entry:                       basis.Entry,
					ArtifactInstalled:           installed,
				}, nil
			},
			Commit: store.CommitModuleArtifactIngressV1,
		},
	)
	if err != nil {
		return moduleArtifactIngressOperationResultV1{}, err
	}
	return moduleArtifactIngressOperationResultV1{
		Artifact: result.Artifact, Admission: result.Admission, Reused: result.Reused,
	}, nil
}

func moduleArtifactIngressCommandFailureV1(code string) error {
	return fmt.Errorf("freeagent module-artifact-ingress: failed (%s)", code)
}

func moduleArtifactIngressCommandFailureFromErrorV1(err error) error {
	code := string(moduleartifactingress.FailureCodeOfV1(err))
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = "CANCELLED"
	case errors.Is(err, currentstore.ErrOwnerActive):
		code = "STORE_BUSY"
	case errors.Is(err, currentstore.ErrModuleSourceNotFound),
		errors.Is(err, currentstore.ErrModuleDiscoverySnapshotNotFound):
		code = "SOURCE_NOT_FOUND"
	case errors.Is(err, currentstore.ErrModuleArtifactIngressStale),
		errors.Is(err, currentstore.ErrModuleSourceStale):
		code = "SOURCE_STALE"
	case errors.Is(err, currentstore.ErrModuleArtifactIngressConflict),
		errors.Is(err, currentstore.ErrModuleSourceConflict):
		code = "ARTIFACT_CONFLICT"
	case errors.Is(err, currentstore.ErrModuleArtifactIngressQuota):
		code = "STORE_QUOTA"
	case errors.Is(err, currentstore.ErrInvalidModuleArtifactIngress):
		code = "STORE_INVALID"
	case errors.Is(err, currentstore.ErrModuleArtifactIngressIntegrity):
		code = "STORE_INTEGRITY"
	}
	if strings.TrimSpace(code) == "" {
		code = "INTERNAL_ERROR"
	}
	return moduleArtifactIngressCommandFailureV1(code)
}
