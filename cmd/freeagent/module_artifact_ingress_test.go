package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func windowsHostPathForModuleArtifactIngressTestV1(segments ...string) string {
	return strings.Join(append([]string{"C:"}, segments...), string(rune(92)))
}

func TestModuleArtifactIngressCommandIsDefaultOffAndRejectsCallerPackagePath(t *testing.T) {
	opened := 0
	dependencies := moduleArtifactIngressDependenciesV1{
		openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
			opened++
			return &fakeModuleArtifactIngressStoreV1{}, nil
		},
		ingress: func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
			t.Fatal("ingress unexpectedly called")
			return moduleArtifactIngressOperationResultV1{}, nil
		},
	}
	if err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), validModuleArtifactIngressArgsV1(t)[1:], &bytes.Buffer{}, &bytes.Buffer{}, dependencies,
	); err == nil || !strings.Contains(err.Error(), "INGRESS_DISABLED") {
		t.Fatalf("disabled error = %v", err)
	}
	args := append(validModuleArtifactIngressArgsV1(t), "--package-path", "attacker/chosen")
	if err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, dependencies,
	); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("invented package path error = %v", err)
	}
	if opened != 0 {
		t.Fatalf("Store opened %d times for rejected commands", opened)
	}
}

func TestModuleArtifactIngressCommandOpensOnceReturnsCanonicalPathFreeResult(t *testing.T) {
	selection := validModuleArtifactIngressSelectionV1()
	store := &fakeModuleArtifactIngressStoreV1{}
	opened, ingressed := 0, 0
	dependencies := moduleArtifactIngressDependenciesV1{
		openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
			opened++
			return store, nil
		},
		ingress: func(_ context.Context, request moduleartifactingress.RequestV1, got moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
			ingressed++
			if got != store || request.Selection != selection {
				t.Fatalf("request = %#v store=%T", request, got)
			}
			artifact := currentstore.ModuleArtifactV1{
				ArtifactDigest:    selection.ArtifactDigest,
				Module:            selection.Module,
				ManifestRef:       strings.Repeat("4", 64),
				ManifestCanonical: []byte(`{"api_version":"freeagent.module/v1"}`),
				ArtifactSizeBytes: 123, CoveredFileCount: 2,
				IngressedAt: time.Unix(10, 0).UTC(),
			}
			admission := currentstore.ModuleArtifactAdmissionV1{
				AdmissionID: strings.Repeat("5", 64),
				Record: moduleartifactingress.RecordV1{
					SchemaVersion: moduleartifactingress.RecordSchemaVersionV1,
					SourceID:      selection.SourceID, SnapshotID: selection.SnapshotID,
					Module: selection.Module, ArtifactDigest: selection.ArtifactDigest,
				},
				Artifact: artifact,
			}
			return moduleArtifactIngressOperationResultV1{
				Artifact: artifact, Admission: admission, Reused: true,
			}, nil
		},
	}
	var output bytes.Buffer
	args := validModuleArtifactIngressArgsV1(t)
	if err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), args, &output, &bytes.Buffer{}, dependencies,
	); err != nil {
		t.Fatalf("command error = %v", err)
	}
	if opened != 1 || ingressed != 1 || store.closeCalls != 1 {
		t.Fatalf("opened=%d ingressed=%d closed=%d", opened, ingressed, store.closeCalls)
	}
	text := output.String()
	if !strings.Contains(text, `"schema_version":"`+moduleArtifactIngressResultSchemaV1+`"`) ||
		!strings.Contains(text, `"physical_reused":true`) ||
		strings.Contains(text, windowsHostPathForModuleArtifactIngressTestV1("source")) ||
		strings.Contains(text, windowsHostPathForModuleArtifactIngressTestV1("artifacts")) {
		t.Fatalf("unexpected output %q", text)
	}
}

func TestModuleArtifactIngressCommandRedactsUnderlyingPaths(t *testing.T) {
	store := &fakeModuleArtifactIngressStoreV1{}
	dependencies := moduleArtifactIngressDependenciesV1{
		openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
			return store, nil
		},
		ingress: func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
			return moduleArtifactIngressOperationResultV1{}, errors.Join(
				currentstore.ErrModuleArtifactIngressStale,
				errors.New("sensitive path "+windowsHostPathForModuleArtifactIngressTestV1("source", "private")),
			)
		},
	}
	err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), validModuleArtifactIngressArgsV1(t), &bytes.Buffer{}, &bytes.Buffer{}, dependencies,
	)
	if err == nil || !strings.Contains(err.Error(), "SOURCE_STALE") ||
		strings.Contains(err.Error(), "sensitive") ||
		strings.Contains(err.Error(), windowsHostPathForModuleArtifactIngressTestV1("source")) {
		t.Fatalf("redacted error = %v", err)
	}
}

func TestModuleArtifactIngressCommandClosesBeforeSuccessOutput(t *testing.T) {
	selection := validModuleArtifactIngressSelectionV1()
	store := &fakeModuleArtifactIngressStoreV1{closeErr: errors.New(
		"sensitive close " + windowsHostPathForModuleArtifactIngressTestV1("store", "current.db"),
	)}
	dependencies := moduleArtifactIngressDependenciesV1{
		openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
			return store, nil
		},
		ingress: func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
			artifact := currentstore.ModuleArtifactV1{
				ArtifactDigest: selection.ArtifactDigest,
				Module:         selection.Module,
			}
			return moduleArtifactIngressOperationResultV1{
				Artifact: artifact,
				Admission: currentstore.ModuleArtifactAdmissionV1{
					AdmissionID: strings.Repeat("5", 64),
					Record: moduleartifactingress.RecordV1{
						SourceID: selection.SourceID, SnapshotID: selection.SnapshotID,
						Module: selection.Module, ArtifactDigest: selection.ArtifactDigest,
					},
					Artifact: artifact,
				},
			}, nil
		},
	}
	var output bytes.Buffer
	err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), validModuleArtifactIngressArgsV1(t), &output, &bytes.Buffer{}, dependencies,
	)
	if err == nil || !strings.Contains(err.Error(), "STORE_INVALID") ||
		strings.Contains(err.Error(), "sensitive") || output.Len() != 0 || store.closeCalls != 1 {
		t.Fatalf("close failure err=%v stdout=%q closeCalls=%d", err, output.String(), store.closeCalls)
	}
}

func TestModuleArtifactIngressCommandRejectsOverlappingStateRootsBeforeOpen(t *testing.T) {
	root := t.TempDir()
	selection := validModuleArtifactIngressSelectionV1()
	base := []string{
		"--enable-module-artifact-ingress",
		"--source-id", selection.SourceID,
		"--snapshot-id", selection.SnapshotID,
		"--module-id", selection.Module.ID,
		"--exact-version", selection.Module.Version,
		"--artifact-digest", selection.ArtifactDigest,
	}
	opened := 0
	dependencies := moduleArtifactIngressDependenciesV1{
		openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
			opened++
			return &fakeModuleArtifactIngressStoreV1{}, nil
		},
		ingress: func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
			t.Fatal("ingress unexpectedly called")
			return moduleArtifactIngressOperationResultV1{}, nil
		},
	}
	for name, paths := range map[string][]string{
		"database inside artifact": {filepath.Join(root, "artifacts", "current.db"), filepath.Join(root, "source"), filepath.Join(root, "artifacts")},
		"artifact inside database": {filepath.Join(root, "state"), filepath.Join(root, "source"), filepath.Join(root, "state", "artifacts")},
		"database inside source":   {filepath.Join(root, "source", "current.db"), filepath.Join(root, "source"), filepath.Join(root, "artifacts")},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string(nil), base...)
			args = append(args, "--db", paths[0], "--source-root", paths[1], "--artifact-root", paths[2])
			if err := runModuleArtifactIngressWithDependenciesV1(
				context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, dependencies,
			); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
				t.Fatalf("overlap error = %v", err)
			}
		})
	}
	if opened != 0 {
		t.Fatalf("Store opened %d times for overlapping roots", opened)
	}
}

func TestModuleArtifactIngressCommandRejectsSymlinkAliasBeforeOpen(t *testing.T) {
	root := t.TempDir()
	artifactRoot := filepath.Join(root, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceAlias := filepath.Join(root, "source-alias")
	if err := os.Symlink(artifactRoot, sourceAlias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	databasePath := filepath.Join(root, "current.db")
	if err := os.WriteFile(databasePath, []byte("not opened"), 0o600); err != nil {
		t.Fatal(err)
	}
	selection := validModuleArtifactIngressSelectionV1()
	args := []string{
		"--enable-module-artifact-ingress",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--source-root", sourceAlias,
		"--source-id", selection.SourceID,
		"--snapshot-id", selection.SnapshotID,
		"--module-id", selection.Module.ID,
		"--exact-version", selection.Module.Version,
		"--artifact-digest", selection.ArtifactDigest,
	}
	opened := 0
	err := runModuleArtifactIngressWithDependenciesV1(
		context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, moduleArtifactIngressDependenciesV1{
			openStore: func(context.Context, string) (moduleArtifactIngressStoreV1, error) {
				opened++
				return &fakeModuleArtifactIngressStoreV1{}, nil
			},
			ingress: func(context.Context, moduleartifactingress.RequestV1, moduleArtifactIngressStoreV1) (moduleArtifactIngressOperationResultV1, error) {
				t.Fatal("ingress unexpectedly called")
				return moduleArtifactIngressOperationResultV1{}, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") || opened != 0 {
		t.Fatalf("alias error=%v opened=%d", err, opened)
	}
}

type fakeModuleArtifactIngressStoreV1 struct {
	closeCalls int
	closeErr   error
}

func (*fakeModuleArtifactIngressStoreV1) ReadModuleArtifactIngressBasisV1(
	context.Context,
	currentstore.ModuleArtifactIngressSelectionV1,
) (currentstore.ModuleArtifactIngressBasisV1, error) {
	return currentstore.ModuleArtifactIngressBasisV1{}, errors.New("unexpected read")
}

func (*fakeModuleArtifactIngressStoreV1) CommitModuleArtifactIngressV1(
	context.Context,
	currentstore.ModuleArtifactIngressBasisV1,
	[]byte,
	uint64,
) (currentstore.ModuleArtifactV1, currentstore.ModuleArtifactAdmissionV1, error) {
	return currentstore.ModuleArtifactV1{}, currentstore.ModuleArtifactAdmissionV1{}, errors.New("unexpected commit")
}

func (*fakeModuleArtifactIngressStoreV1) IsModuleArtifactInstalledV1(context.Context, string) (bool, error) {
	return false, nil
}

func (store *fakeModuleArtifactIngressStoreV1) Close() error {
	store.closeCalls++
	return store.closeErr
}

func validModuleArtifactIngressSelectionV1() moduleartifactingress.SelectionV1 {
	return moduleartifactingress.SelectionV1{
		SourceID:       "local.modules",
		SnapshotID:     strings.Repeat("2", 64),
		Module:         moduleapi.Ref{ID: "demo.module", Version: "1.0.0"},
		ArtifactDigest: strings.Repeat("3", 64),
	}
}

func validModuleArtifactIngressArgsV1(t *testing.T) []string {
	t.Helper()
	selection := validModuleArtifactIngressSelectionV1()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.db")
	if err := os.WriteFile(databasePath, []byte("test current store"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(root, "source")
	artifactRoot := filepath.Join(root, "artifacts")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{
		"--enable-module-artifact-ingress",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--source-root", sourceRoot,
		"--source-id", selection.SourceID,
		"--snapshot-id", selection.SnapshotID,
		"--module-id", selection.Module.ID,
		"--exact-version", selection.Module.Version,
		"--artifact-digest", selection.ArtifactDigest,
	}
}
