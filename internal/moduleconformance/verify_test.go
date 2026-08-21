package moduleconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestVerifyDirectoryReturnsStableFormatOnlyReport(t *testing.T) {
	t.Parallel()
	root := compatFixture(t, "declarative-role")

	first, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated verification changed report:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.SchemaVersion != ReportSchemaVersionV1 ||
		first.PackageAPIVersion != moduleapi.ModuleManifestAPIVersionV1 ||
		first.Module != (ModuleIdentity{ID: "freeagent.compat.role", ExactVersion: "1.0.0"}) ||
		!moduleapi.ValidSHA256(first.ArtifactDigest) ||
		first.ArtifactSizeBytes == 0 || first.CoveredFileCount != 2 ||
		first.RuntimeRequest.Mode != string(moduleapi.RuntimeModeRequestDeclarative) ||
		len(first.Provides) != 1 || len(first.Requires) != 0 ||
		len(first.RequestedPermissions) != 0 {
		t.Fatalf("unexpected report: %+v", first)
	}

	manifest, err := moduleapi.ReadArtifactManifestV1FromDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(root, moduleapi.ArtifactMetadataPaths{})
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	wantSize, err := coveredSize(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	if first.ArtifactDigest != wantDigest || first.ArtifactSizeBytes != wantSize {
		t.Fatalf("report digest/size=%s/%d want=%s/%d", first.ArtifactDigest, first.ArtifactSizeBytes, wantDigest, wantSize)
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		filepath.Clean(root), `"manifest"`, `"config_schema"`, `"lifecycle"`,
		`"health"`, `"authority"`, `"trust"`, `"execution_class"`,
		`"activation"`, `"failure_policy"`,
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("report exposes forbidden value %q: %s", forbidden, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(`"requires":[]`)) ||
		!bytes.Contains(encoded, []byte(`"requested_permissions":[]`)) {
		t.Fatalf("empty report arrays are not stable: %s", encoded)
	}
}

func TestVerifyDirectoryWithPackageLimitEvidenceReturnsDigestManifestDefensively(t *testing.T) {
	t.Parallel()
	root := compatFixture(t, "declarative-role")
	report, manifestCanonical, err := VerifyDirectoryWithPackageLimitEvidence(
		context.Background(),
		root,
		8<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantManifest, err := moduleapi.ReadArtifactManifestV1FromDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(root, moduleapi.ArtifactMetadataPaths{})
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(manifestCanonical, wantManifest) || report.ArtifactDigest != wantDigest {
		t.Fatalf("evidence/report mismatch: report=%+v manifest=%s", report, manifestCanonical)
	}
	manifestCanonical[0] = '['
	secondReport, secondManifest, err := VerifyDirectoryWithPackageLimitEvidence(
		context.Background(),
		root,
		8<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, secondReport) || !bytes.Equal(secondManifest, wantManifest) {
		t.Fatal("returned Manifest evidence aliases verifier state")
	}
	legacy, err := VerifyDirectoryWithPackageLimit(context.Background(), root, 8<<20)
	if err != nil || !reflect.DeepEqual(legacy, report) {
		t.Fatalf("legacy Report wire changed: report=%+v error=%v", legacy, err)
	}
}

func TestVerifyDirectoryAcceptsOpaqueRuntimeAndFuturePortRequests(t *testing.T) {
	t.Parallel()

	trusted, err := VerifyDirectory(
		context.Background(),
		compatFixture(t, "go-action-provider"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.RuntimeRequest.Mode != string(moduleapi.RuntimeModeRequestTrustedInProcess) ||
		trusted.CoveredFileCount != 2 ||
		trusted.ArtifactDigest != "280be99c041baf761af004b0ade08197861a29e623c022a8afded1eca2957c35" ||
		trusted.ArtifactSizeBytes != 871 {
		t.Fatalf("trusted report=%+v", trusted)
	}

	remoteRoot := t.TempDir()
	writeExactFile(t, filepath.Join(remoteRoot, "module.yaml"), []byte(
		`{"api_version":"freeagent.module/v1","id":"freeagent.compat.remote","provides":[{"exact_version":"v9","name":"future.capability"}],"runtime":{"entrypoint":"remote.adapter.alpha","mode":"REMOTE","protocol":"custom-rpc/v7"},"version":"1.0.0"}`,
	))
	remote, err := VerifyDirectory(context.Background(), remoteRoot)
	if err != nil {
		t.Fatalf("format-valid future REMOTE request was rejected: %v", err)
	}
	if remote.RuntimeRequest.Mode != string(moduleapi.RuntimeModeRequestRemote) ||
		len(remote.Provides) != 1 || remote.Provides[0].Name != "future.capability" {
		t.Fatalf("remote report=%+v", remote)
	}
}

func TestVerifyDirectoryReportOmitsManifestObjectsAndFileContent(t *testing.T) {
	t.Parallel()
	const sentinel = "Authorization-sk-example-secret-private-schema"
	root := t.TempDir()
	writeExactFile(t, filepath.Join(root, "module.yaml"), []byte(
		`{"api_version":"freeagent.module/v1","config_schema":{"private":"`+sentinel+`"},"id":"freeagent.compat.private","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/context.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"1.0.0"}`,
	))
	writeExactFile(
		t,
		filepath.Join(root, "content", "context.json"),
		[]byte(sentinel),
	)
	report, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(sentinel)) {
		t.Fatalf("report leaked manifest object or file content: %s", encoded)
	}
}

func TestVerifyDirectoryRequiresExactFileEntrypoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		manifest  string
		directory bool
	}{
		{
			name:     "declarative missing",
			manifest: `{"api_version":"freeagent.module/v1","id":"freeagent.compat.missing","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/missing.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"1.0.0"}`,
		},
		{
			name:      "local process directory",
			manifest:  `{"api_version":"freeagent.module/v1","id":"freeagent.compat.local","provides":[{"exact_version":"v1","name":"action.provider"}],"runtime":{"entrypoint":"content/mcp.json","mode":"LOCAL_PROCESS","protocol":"mcp-stdio/2025-11-25"},"version":"1.0.0"}`,
			directory: true,
		},
		{
			name:     "current remote descriptor missing",
			manifest: `{"api_version":"freeagent.module/v1","id":"freeagent.compat.remote","provides":[{"exact_version":"v1","name":"action.provider"}],"runtime":{"entrypoint":"content/action-http.json","mode":"REMOTE","protocol":"freeagent-action-http/v1"},"version":"1.0.0"}`,
		},
		{
			name:     "current WASM descriptor missing",
			manifest: `{"api_version":"freeagent.module/v1","id":"freeagent.compat.wasm","provides":[{"exact_version":"v1","name":"action.provider"}],"runtime":{"entrypoint":"content/action-wasm.json","mode":"WASM","protocol":"freeagent-action-wasm/v1"},"version":"1.0.0"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeExactFile(t, filepath.Join(root, "module.yaml"), []byte(test.manifest))
			if test.directory {
				if err := os.MkdirAll(filepath.Join(root, "content", "mcp.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := VerifyDirectory(context.Background(), root); err == nil ||
				!strings.Contains(err.Error(), "entrypoint") ||
				FailureCodeOf(err) != FailureEntrypointAbsent {
				t.Fatalf("missing file entrypoint error=%v", err)
			}
		})
	}
}

func TestVerifyDirectoryRejectsUnsafeOrSelfAuthorizingPackages(t *testing.T) {
	t.Parallel()

	t.Run("unknown authority field", func(t *testing.T) {
		root := t.TempDir()
		writeExactFile(t, filepath.Join(root, "module.yaml"), []byte(
			`{"api_version":"freeagent.module/v1","execution_class":"TRUSTED_IN_PROCESS","id":"freeagent.compat.bad","provides":[{"exact_version":"v1","name":"action.provider"}],"runtime":{"entrypoint":"bad.adapter","mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"version":"1.0.0"}`,
		))
		if _, err := VerifyDirectory(context.Background(), root); err == nil ||
			FailureCodeOf(err) != FailureManifestInvalid {
			t.Fatal("self-authorizing field was accepted")
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		root := copyCompatFixture(t, "declarative-role")
		original := filepath.Join(root, "content", "context.json")
		if err := os.Link(original, filepath.Join(root, "content", "alias.json")); err != nil {
			t.Skipf("hardlinks unavailable: %v", err)
		}
		if _, err := VerifyDirectory(context.Background(), root); err == nil ||
			FailureCodeOf(err) != FailureArtifactInvalid {
			t.Fatal("hardlinked artifact was accepted")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := copyCompatFixture(t, "declarative-role")
		if err := os.Symlink(
			filepath.Join(root, "content", "context.json"),
			filepath.Join(root, "content", "alias.json"),
		); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := VerifyDirectory(context.Background(), root); err == nil {
			t.Fatal("symlinked artifact was accepted")
		}
	})
}

func TestVerifyDirectoryDoesNotImplicitlyExcludeEnvelopeLikeFiles(t *testing.T) {
	t.Parallel()
	root := copyCompatFixture(t, "declarative-role")
	withoutSignature, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	signature := []byte("synthetic-detached-signature")
	writeExactFile(t, filepath.Join(root, "module.sig"), signature)
	withSignature, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if withSignature.ArtifactDigest == withoutSignature.ArtifactDigest ||
		withSignature.CoveredFileCount != withoutSignature.CoveredFileCount+1 ||
		withSignature.ArtifactSizeBytes !=
			withoutSignature.ArtifactSizeBytes+uint64(len(signature)) {
		t.Fatalf(
			"signature-like file was implicitly excluded:\nwithout=%+v\nwith=%+v",
			withoutSignature,
			withSignature,
		)
	}
}

func TestVerifyDirectoryFinalPassRejectsMutationAndNeverExecutesEntrypoint(t *testing.T) {
	root := copyCompatFixture(t, "declarative-role")
	before := snapshotFiles(t, root)
	marker := filepath.Join(root, "executed.marker")

	report, err := VerifyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Module.ID == "" {
		t.Fatal("empty report")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verification created an execution marker: %v", err)
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("verification changed package files:\nbefore=%v\nafter=%v", before, after)
	}

	localRoot := t.TempDir()
	writeExactFile(t, filepath.Join(localRoot, "module.yaml"), []byte(
		`{"api_version":"freeagent.module/v1","id":"freeagent.compat.no-execute","provides":[{"exact_version":"v1","name":"action.provider"}],"runtime":{"entrypoint":"content/mcp.json","mode":"LOCAL_PROCESS","protocol":"mcp-stdio/2025-11-25"},"version":"1.0.0"}`,
	))
	localMarker := filepath.Join(localRoot, "local-process-executed.marker")
	writeExactFile(
		t,
		filepath.Join(localRoot, "content", "mcp.json"),
		[]byte(`{"command":"create `+filepath.ToSlash(localMarker)+`"}`),
	)
	if _, err := VerifyDirectory(context.Background(), localRoot); err != nil {
		t.Fatalf("format-valid LOCAL_PROCESS package: %v", err)
	}
	if _, err := os.Stat(localMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LOCAL_PROCESS entrypoint was executed: %v", err)
	}

	_, err = verifyDirectory(
		context.Background(),
		root,
		func(
			ctx context.Context,
			artifactRoot string,
			metadata moduleapi.ArtifactMetadataPaths,
			digest string,
			size uint64,
		) error {
			path := filepath.Join(artifactRoot, "content", "context.json")
			if err := os.WriteFile(path, []byte(`{"changed":true}`), 0o600); err != nil {
				return err
			}
			return moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
				ctx, artifactRoot, metadata, digest, size,
			)
		},
	)
	if err == nil || FailureCodeOf(err) != FailureArtifactDrift {
		t.Fatalf("final-pass mutation error=%v", err)
	}

	addedRoot := copyCompatFixture(t, "declarative-role")
	_, err = verifyDirectory(
		context.Background(),
		addedRoot,
		func(
			ctx context.Context,
			artifactRoot string,
			metadata moduleapi.ArtifactMetadataPaths,
			digest string,
			size uint64,
		) error {
			if err := os.WriteFile(
				filepath.Join(artifactRoot, "content", "added.json"),
				[]byte(`{}`),
				0o600,
			); err != nil {
				return err
			}
			return moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
				ctx, artifactRoot, metadata, digest, size,
			)
		},
	)
	if err == nil || FailureCodeOf(err) != FailureArtifactDrift {
		t.Fatalf("final-pass added-file error=%v", err)
	}
}

func TestVerifyDirectoryRejectsNilCancelledAndNilVerifier(t *testing.T) {
	t.Parallel()
	root := compatFixture(t, "declarative-role")
	if _, err := VerifyDirectory(nil, root); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifyDirectory(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error=%v", err)
	} else if FailureCodeOf(err) != FailureCancelled {
		t.Fatalf("cancelled context code=%s", FailureCodeOf(err))
	}
	if _, err := verifyDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("nil final verifier accepted")
	}
	if FailureCodeOf(nil) != "" ||
		FailureCodeOf(errors.New("unclassified")) != FailureInternal {
		t.Fatal("failure-code fallback is not closed")
	}
}

func TestFailureCodeOfIsClosedAndPreservesOnlyKnownWrappedCodes(t *testing.T) {
	t.Parallel()
	const privateCause = "Authorization-sk-example-secret-private-path"
	known := []FailureCode{
		FailureManifestInvalid,
		FailureArtifactInvalid,
		FailureEntrypointAbsent,
		FailureArtifactDrift,
		FailureCancelled,
		FailureInternal,
	}
	for _, code := range known {
		err := verificationFailure(code, "fixed safe label", errors.New(privateCause))
		wrapped := fmt.Errorf("outer wrapper: %w", err)
		if got := FailureCodeOf(wrapped); got != code {
			t.Fatalf("wrapped code=%s want=%s", got, code)
		}
		if strings.Contains(err.Error(), privateCause) {
			t.Fatalf("public verification error leaked cause for %s", code)
		}
	}
	unknownCode := verificationFailure(
		FailureCode("FUTURE_UNREVIEWED"),
		"fixed safe label",
		errors.New(privateCause),
	)
	if FailureCodeOf(unknownCode) != FailureInternal ||
		FailureCodeOf(errors.New(privateCause)) != FailureInternal ||
		FailureCodeOf((*VerificationError)(nil)) != FailureInternal ||
		FailureCodeOf(nil) != "" {
		t.Fatal("failure code closure accepted an unknown or typed-nil value")
	}
}

func compatFixture(t *testing.T, name string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(
		filepath.Dir(current),
		"..", "..", "sdk", "moduleapi", "testdata", "compat", "v1", name,
	)
	resolved, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func copyCompatFixture(t *testing.T, name string) string {
	t.Helper()
	source := compatFixture(t, name)
	destination := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return destination
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeExactFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
