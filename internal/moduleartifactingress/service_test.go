package moduleartifactingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestIngressV1PublishesBeforeCommitAndExactRetryReuses(t *testing.T) {
	fixture := newIngressServiceFixtureV1(t, false)
	reads, commits := 0, 0
	callbacks := StoreCallbacksV1[int, string, string]{
		Existing: noExistingV1[string, string],
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			reads++
			return 73, fixture.view, nil
		},
		Commit: func(ctx context.Context, basis int, manifest []byte, count uint64) (string, string, error) {
			commits++
			if basis != 73 || count != 2 || len(manifest) == 0 {
				t.Fatalf("commit evidence = basis %d count %d manifest %d", basis, count, len(manifest))
			}
			if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
				ctx, filepath.Join(fixture.artifactRoot, fixture.selection.ArtifactDigest),
				moduleapi.ArtifactMetadataPaths{}, fixture.selection.ArtifactDigest,
				fixture.view.Entry.ArtifactSizeBytes,
			); err != nil {
				t.Fatalf("artifact was not durable before commit: %v", err)
			}
			return "artifact", "admission", nil
		},
	}
	request := RequestV1{
		SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
		Selection: fixture.selection,
	}
	first, err := IngressV1(context.Background(), request, callbacks)
	if err != nil {
		t.Fatalf("IngressV1(first) error = %v", err)
	}
	if first.Artifact != "artifact" || first.Admission != "admission" || first.Reused {
		t.Fatalf("first = %#v", first)
	}
	second, err := IngressV1(context.Background(), request, callbacks)
	if err != nil {
		t.Fatalf("IngressV1(retry) error = %v", err)
	}
	if !second.Reused || reads != 2 || commits != 2 {
		t.Fatalf("retry = %#v reads=%d commits=%d", second, reads, commits)
	}
}

func TestIngressV1NeverCommitsRejectedSourceOrFailedPublication(t *testing.T) {
	t.Run("signed policy", func(t *testing.T) {
		fixture := newIngressServiceFixtureV1(t, true)
		commits := 0
		_, err := IngressV1(context.Background(), RequestV1{
			SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
			Selection: fixture.selection,
		}, StoreCallbacksV1[int, int, int]{
			Existing: noExistingV1[int, int],
			Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
				return 1, fixture.view, nil
			},
			Commit: func(context.Context, int, []byte, uint64) (int, int, error) {
				commits++
				return 0, 0, nil
			},
		})
		if FailureCodeOfV1(err) != FailureSourceDeniedV1 || commits != 0 {
			t.Fatalf("error=%v code=%s commits=%d", err, FailureCodeOfV1(err), commits)
		}
	})

	t.Run("conflicting digest target", func(t *testing.T) {
		fixture := newIngressServiceFixtureV1(t, false)
		if err := os.Mkdir(filepath.Join(fixture.artifactRoot, fixture.selection.ArtifactDigest), 0o700); err != nil {
			t.Fatal(err)
		}
		commits := 0
		_, err := IngressV1(context.Background(), RequestV1{
			SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
			Selection: fixture.selection,
		}, StoreCallbacksV1[int, int, int]{
			Existing: noExistingV1[int, int],
			Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
				return 1, fixture.view, nil
			},
			Commit: func(context.Context, int, []byte, uint64) (int, int, error) {
				commits++
				return 0, 0, nil
			},
		})
		if FailureCodeOfV1(err) != FailurePublishV1 || commits != 0 {
			t.Fatalf("error=%v code=%s commits=%d", err, FailureCodeOfV1(err), commits)
		}
	})
}

func TestIngressV1CommitFailureLeavesOnlyVerifiedInertOrphan(t *testing.T) {
	fixture := newIngressServiceFixtureV1(t, false)
	commitFailure := errors.New("database commit failed at " + strings.Join(
		[]string{"C:", "secret", "store.db"}, string(rune(92)),
	))
	_, err := IngressV1(context.Background(), RequestV1{
		SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
		Selection: fixture.selection,
	}, StoreCallbacksV1[int, int, int]{
		Existing: noExistingV1[int, int],
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			return 1, fixture.view, nil
		},
		Commit: func(context.Context, int, []byte, uint64) (int, int, error) {
			return 0, 0, commitFailure
		},
	})
	if FailureCodeOfV1(err) != FailureStoreCommitV1 {
		t.Fatalf("code = %s, error=%v", FailureCodeOfV1(err), err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("failure leaked host path: %v", err)
	}
	if verifyErr := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		context.Background(), filepath.Join(fixture.artifactRoot, fixture.selection.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{}, fixture.selection.ArtifactDigest,
		fixture.view.Entry.ArtifactSizeBytes,
	); verifyErr != nil {
		t.Fatalf("orphan is not exact: %v", verifyErr)
	}
}

func TestIngressV1RecoversAmbiguousCommitByExactAdmissionLookup(t *testing.T) {
	fixture := newIngressServiceFixtureV1(t, false)
	existingCalls := 0
	committed := false
	result, err := IngressV1(context.Background(), RequestV1{
		SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
		Selection: fixture.selection,
	}, StoreCallbacksV1[int, string, string]{
		Existing: func(context.Context, SelectionV1) (string, string, ExistingViewV1, bool, error) {
			existingCalls++
			if !committed {
				return "", "", ExistingViewV1{}, false, nil
			}
			return "artifact", "admission", ExistingViewV1{
				Module:            fixture.selection.Module,
				ArtifactDigest:    fixture.selection.ArtifactDigest,
				ArtifactSizeBytes: fixture.view.Entry.ArtifactSizeBytes,
				CoveredFileCount:  2,
			}, true, nil
		},
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			return 1, fixture.view, nil
		},
		Commit: func(context.Context, int, []byte, uint64) (string, string, error) {
			committed = true
			return "", "", errors.New("ambiguous commit response")
		},
	})
	if err != nil || result.Artifact != "artifact" || result.Admission != "admission" ||
		result.Reused || existingCalls != 2 {
		t.Fatalf("result=%#v err=%v existingCalls=%d", result, err, existingCalls)
	}
}

func TestIngressV1DetectsPostCommitPhysicalTamper(t *testing.T) {
	fixture := newIngressServiceFixtureV1(t, false)
	_, err := IngressV1(context.Background(), RequestV1{
		SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
		Selection: fixture.selection,
	}, StoreCallbacksV1[int, int, int]{
		Existing: noExistingV1[int, int],
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			return 1, fixture.view, nil
		},
		Commit: func(context.Context, int, []byte, uint64) (int, int, error) {
			path := filepath.Join(
				fixture.artifactRoot, fixture.selection.ArtifactDigest, "content", "entry.txt",
			)
			if writeErr := os.WriteFile(path, []byte("tampered"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return 1, 1, nil
		},
	})
	if FailureCodeOfV1(err) != FailureFinalVerifyV1 {
		t.Fatalf("code = %s, error=%v", FailureCodeOfV1(err), err)
	}
}

func TestIngressV1ExistingAdmissionSkipsSourceAndLiveBasis(t *testing.T) {
	fixture := newIngressServiceFixtureV1(t, false)
	request := RequestV1{
		SourceRoot: fixture.sourceRoot, ArtifactRoot: fixture.artifactRoot,
		Selection: fixture.selection,
	}
	firstCallbacks := StoreCallbacksV1[int, string, string]{
		Existing: noExistingV1[string, string],
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			return 1, fixture.view, nil
		},
		Commit: func(context.Context, int, []byte, uint64) (string, string, error) {
			return "artifact", "admission", nil
		},
	}
	if _, err := IngressV1(context.Background(), request, firstCallbacks); err != nil {
		t.Fatal(err)
	}
	request.SourceRoot = filepath.Join(fixture.sourceRoot, "now-missing")
	readCalled := false
	result, err := IngressV1(context.Background(), request, StoreCallbacksV1[int, string, string]{
		Existing: func(context.Context, SelectionV1) (string, string, ExistingViewV1, bool, error) {
			return "artifact", "admission", ExistingViewV1{
				Module:            fixture.selection.Module,
				ArtifactDigest:    fixture.selection.ArtifactDigest,
				ArtifactSizeBytes: fixture.view.Entry.ArtifactSizeBytes,
				CoveredFileCount:  2,
			}, true, nil
		},
		Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
			readCalled = true
			return 0, BasisViewV1{}, errors.New("live head must not be read")
		},
		Commit: func(context.Context, int, []byte, uint64) (string, string, error) {
			t.Fatal("exact existing admission unexpectedly committed")
			return "", "", nil
		},
	})
	if err != nil || !result.Reused || readCalled {
		t.Fatalf("result=%#v err=%v read=%v", result, err, readCalled)
	}
}

func TestIngressV1NonInstalledStoreAcceptsOtherStoreExactExecutableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable FileMode does not expose Windows execute state")
	}
	fixture := newLocalMCPIngressServiceFixtureV1(t)
	selected, err := moduleartifactstore.SelectSourcePackageV1(
		fixture.sourceRoot,
		fixture.artifactRoot,
		fixture.view.Entry.PackagePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	published, err := moduleartifactstore.PublishV1(
		context.Background(),
		moduleartifactstore.PublishRequestV1{
			Source:             selected,
			ArtifactDigest:     fixture.selection.ArtifactDigest,
			ArtifactSizeBytes:  fixture.view.Entry.ArtifactSizeBytes,
			MaxPackageBytes:    fixture.view.Entry.ArtifactSizeBytes,
			ExistingModePolicy: moduleartifactstore.ExistingModeRootCompatibleV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(
		fixture.artifactRoot,
		fixture.selection.ArtifactDigest,
		"content",
		"server.bin",
	)
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}

	readCalled := false
	result, err := IngressV1(
		context.Background(),
		RequestV1{
			SourceRoot:   filepath.Join(fixture.sourceRoot, "removed"),
			ArtifactRoot: fixture.artifactRoot,
			Selection:    fixture.selection,
		},
		StoreCallbacksV1[int, string, string]{
			Existing: func(context.Context, SelectionV1) (string, string, ExistingViewV1, bool, error) {
				return "artifact-b", "admission-b", ExistingViewV1{
					Module:            fixture.selection.Module,
					ArtifactDigest:    fixture.selection.ArtifactDigest,
					ArtifactSizeBytes: fixture.view.Entry.ArtifactSizeBytes,
					CoveredFileCount:  published.CoveredFileCount,
					Installed:         false,
				}, true, nil
			},
			Read: func(context.Context, SelectionV1) (int, BasisViewV1, error) {
				readCalled = true
				return 0, BasisViewV1{}, errors.New("live basis must not be read")
			},
			Commit: func(context.Context, int, []byte, uint64) (string, string, error) {
				t.Fatal("existing admission unexpectedly committed")
				return "", "", nil
			},
		},
	)
	if err != nil || !result.Reused || readCalled ||
		result.Artifact != "artifact-b" || result.Admission != "admission-b" {
		t.Fatalf("cross-Store executable reuse=%#v err=%v read=%v", result, err, readCalled)
	}
}

func noExistingV1[Artifact, Admission any](
	context.Context,
	SelectionV1,
) (Artifact, Admission, ExistingViewV1, bool, error) {
	var artifact Artifact
	var admission Admission
	return artifact, admission, ExistingViewV1{}, false, nil
}

func newLocalMCPIngressServiceFixtureV1(t *testing.T) ingressServiceFixtureV1 {
	t.Helper()
	fixture := newIngressServiceFixtureV1(t, false)
	packageRoot := filepath.Join(
		fixture.sourceRoot,
		filepath.FromSlash(fixture.view.Entry.PackagePath),
	)
	contentRoot := filepath.Join(packageRoot, "content")
	if err := os.RemoveAll(contentRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("cross-store exact executable\n")
	executableSum := sha256.Sum256(executable)
	descriptorEncoded, err := json.Marshal(mcpstdio.HostDescriptorV1{
		SchemaVersion:    mcpstdio.HostDescriptorSchemaV1,
		ProtocolVersion:  mcpstdio.ProtocolVersionV1,
		Executable:       "content/server.bin",
		ExecutableSHA256: hex.EncodeToString(executableSum[:]),
		WorkingDirectory: "content",
		Tools:            []mcpstdio.ToolBindingV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := moduleapi.CanonicalJSON(descriptorEncoded)
	if err != nil {
		t.Fatal(err)
	}
	manifestEncoded, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         fixture.selection.Module.ID,
		Version:    fixture.selection.Module.Version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestLocalProcess,
			Protocol:   moduleapi.RuntimeProtocolMCPStdio20251125,
			Entrypoint: "content/host.json",
		},
		Provides: []moduleapi.PortRef{{Name: "action.provider", ExactVersion: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := moduleapi.CanonicalJSON(manifestEncoded)
	if err != nil {
		t.Fatal(err)
	}
	files := []moduleapi.ArtifactFile{
		{Path: "content/host.json", Content: descriptor},
		{Path: "content/server.bin", Content: executable},
	}
	for _, file := range files {
		if err := os.WriteFile(
			filepath.Join(packageRoot, filepath.FromSlash(file.Path)),
			file.Content,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(packageRoot, moduleapi.ArtifactManifestPath),
		manifest,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	fixture.selection.ArtifactDigest = digest
	fixture.view.Entry.ArtifactDigest = digest
	fixture.view.Entry.ArtifactSizeBytes = uint64(
		len(manifest) + len(descriptor) + len(executable),
	)
	return fixture
}

type ingressServiceFixtureV1 struct {
	sourceRoot   string
	artifactRoot string
	selection    SelectionV1
	view         BasisViewV1
}

func newIngressServiceFixtureV1(t *testing.T, signed bool) ingressServiceFixtureV1 {
	t.Helper()
	sourceRoot, artifactRoot := t.TempDir(), privateIngressArtifactTempDirV1(t)
	packagePath := "packages/demo"
	packageRoot := filepath.Join(sourceRoot, filepath.FromSlash(packagePath))
	if err := os.MkdirAll(filepath.Join(packageRoot, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello\n")
	if err := os.WriteFile(filepath.Join(packageRoot, "content", "entry.txt"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "demo.module", Version: "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestDeclarative,
			Protocol:   moduleapi.RuntimeProtocolStaticV1,
			Entrypoint: "content/entry.txt",
		},
		Provides: []moduleapi.PortRef{{Name: "demo.port", ExactVersion: "1"}},
	}
	encoded, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, moduleapi.ArtifactManifestPath), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, []moduleapi.ArtifactFile{{
		Path: "content/entry.txt", Content: payload,
	}})
	if err != nil {
		t.Fatal(err)
	}
	origin, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1, []byte(filepath.ToSlash(sourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	policyInput := moduleapi.ModuleSourcePolicyV1{
		SchemaVersion: moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:      "local.modules", Kind: moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest: origin, Network: moduleapi.ModuleSourceNetworkDenyV1,
		SignatureRequired:       signed,
		AllowedModuleIDPrefixes: []string{"demo"},
		MaxIndexBytes:           64 << 10, MaxPackageBytes: 8 << 20, MaxCandidates: 32,
	}
	entrySignature := ""
	if signed {
		policyInput.PublisherKeyID = strings.Repeat("9", 64)
		entrySignature = strings.Repeat("8", 64)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(policyInput)
	if err != nil {
		t.Fatal(err)
	}
	snapshotID := strings.Repeat("7", 64)
	selection := SelectionV1{
		SourceID: "local.modules", SnapshotID: snapshotID,
		Module:         moduleapi.Ref{ID: "demo.module", Version: "1.0.0"},
		ArtifactDigest: digest,
	}
	entry := moduleapi.ModuleDiscoveryEntryV1{
		Module: selection.Module, ArtifactDigest: digest,
		ArtifactSizeBytes: uint64(len(manifest) + len(payload)),
		PackagePath:       packagePath, SignatureID: entrySignature,
	}
	return ingressServiceFixtureV1{
		sourceRoot: sourceRoot, artifactRoot: artifactRoot, selection: selection,
		view: BasisViewV1{
			SourceID: selection.SourceID, SourcePolicyID: policyID,
			SourcePolicyCanonical: policyCanonical, SourcePolicyRevision: 1,
			SnapshotID: snapshotID, SnapshotSourcePolicyID: policyID,
			SnapshotObservationRevision: 1, EntryOrdinal: 0, Entry: entry,
		},
	}
}
