package moduleartifactstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPublishV1PublishesNormalizesAndReuses(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	packageRoot, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
	if err := os.Chmod(filepath.Join(packageRoot, "content", "entry.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/demo")
	if err != nil {
		t.Fatal(err)
	}
	first, err := PublishV1(context.Background(), PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: size,
	})
	if err != nil {
		t.Fatalf("PublishV1(first) error = %v", err)
	}
	if first.Reused || first.CoveredFileCount != 2 || len(first.ManifestCanonical) == 0 {
		t.Fatalf("first result = %#v", first)
	}
	destination := filepath.Join(artifactRoot, digest)
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		context.Background(), destination, moduleapi.ArtifactMetadataPaths{}, digest, size,
	); err != nil {
		t.Fatalf("published verification: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(destination, "content", "entry.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("published mode = %o, want 0600", info.Mode().Perm())
		}
	}
	originalStagePrefix := stagePrefixV1
	stagePrefixV1 = "forbidden" + string(filepath.Separator) + "stage-"
	defer func() { stagePrefixV1 = originalStagePrefix }()
	second, err := PublishV1(context.Background(), PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: size,
	})
	if err != nil || !second.Reused {
		t.Fatalf("PublishV1(retry) = %#v, %v", second, err)
	}
	assertNoStagesV1(t, artifactRoot)
}

func TestPublishV1ConcurrentSameDigest(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/demo")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan PublishResultV1, 2)
	errorsOut := make(chan error, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			result, err := PublishV1(context.Background(), PublishRequestV1{
				Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
				MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
			})
			results <- result
			errorsOut <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent PublishV1 error = %v", err)
		}
	}
	reused := 0
	for result := range results {
		if result.Reused {
			reused++
		}
	}
	if reused != 1 {
		t.Fatalf("reused results = %d, want 1", reused)
	}
	assertNoStagesV1(t, artifactRoot)
}

func TestPublishV1RejectsConflictingTargetAndBackupIncompatibleFile(t *testing.T) {
	t.Run("conflicting target", func(t *testing.T) {
		sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
		_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
		selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/demo")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(artifactRoot, digest), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := PublishV1(context.Background(), PublishRequestV1{
			Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
			MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
		}); err == nil {
			t.Fatal("conflicting target unexpectedly accepted")
		}
		assertNoStagesV1(t, artifactRoot)
	})

	t.Run("over 64 MiB sparse file", func(t *testing.T) {
		sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
		packageRoot, _, _ := writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
		largePath := filepath.Join(packageRoot, "content", "large.bin")
		file, err := os.Create(largePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(BackupMaxArtifactFileV1 + 1); err != nil {
			file.Close()
			t.Skipf("sparse files unavailable: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/demo")
		if err != nil {
			t.Fatal(err)
		}
		digest := strings.Repeat("a", 64)
		if _, err := PublishV1(context.Background(), PublishRequestV1{
			Source: selection, ArtifactDigest: digest,
			ArtifactSizeBytes: uint64(BackupMaxArtifactFileV1 + 1024),
			MaxPackageBytes:   moduleapi.MaxModuleSourcePackageBytesV1,
		}); err == nil {
			t.Fatal("Backup-incompatible file unexpectedly accepted")
		}
		if _, err := os.Lstat(filepath.Join(artifactRoot, digest)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination exists after rejection: %v", err)
		}
	})
}

func TestPublishV1RejectsDigestExactTargetWithUnsafeModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable FileMode does not expose Windows ACLs")
	}
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/demo")
	if err != nil {
		t.Fatal(err)
	}
	request := PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
	}
	if _, err := PublishV1(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(artifactRoot, digest, "content", "entry.txt")
	if err := os.Chmod(payload, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("digest-exact target with executable mode unexpectedly reused")
	}
	request.ExistingModePolicy = ExistingModeStoreProvenInstalledV1
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("installed declarative target with executable content unexpectedly reused")
	}
	request.ExistingModePolicy = ExistingModeRootCompatibleV1
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("root-compatible declarative target with executable content unexpectedly reused")
	}
}

func TestPublishV1RejectsDigestExactTargetWithHardLinkAlias(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/hardlink")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/hardlink")
	if err != nil {
		t.Fatal(err)
	}
	request := PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
	}
	if _, err := PublishV1(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(artifactRoot, digest, "content", "entry.txt")
	alias := filepath.Join(filepath.Dir(artifactRoot), "payload-hardlink-alias")
	if err := os.Link(payload, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	defer os.Remove(alias)
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("digest-exact target with mutable hard-link alias unexpectedly reused")
	}
}

func TestPublishV1AllowsOnlyDescriptorOwnedExactExecutableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable FileMode does not expose Windows ACL execute state")
	}
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeLocalMCPArtifactFixtureV1(t, sourceRoot, "packages/mcp")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/mcp")
	if err != nil {
		t.Fatal(err)
	}
	request := PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
	}
	if _, err := PublishV1(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(artifactRoot, digest)
	executable := filepath.Join(target, "content", "server.bin")
	descriptor := filepath.Join(target, "content", "host.json")
	if info, err := os.Stat(executable); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new executable mode = %v, %v", info, err)
	}
	installedRequest := request
	installedRequest.ExistingModePolicy = ExistingModeStoreProvenInstalledV1
	if _, err := PublishV1(context.Background(), installedRequest); err == nil {
		t.Fatal("installed LOCAL_PROCESS tree without exact 0700 executable unexpectedly reused")
	}
	compatibleRequest := request
	compatibleRequest.ExistingModePolicy = ExistingModeRootCompatibleV1
	if result, err := PublishV1(context.Background(), compatibleRequest); err != nil || !result.Reused {
		t.Fatalf("all-0600 root-compatible reuse = %#v, %v", result, err)
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("strict inert-only policy unexpectedly reused executable tree")
	}
	if result, err := PublishV1(context.Background(), compatibleRequest); err != nil || !result.Reused {
		t.Fatalf("cross-Store compatible executable reuse = %#v, %v", result, err)
	}
	request.ExistingModePolicy = ExistingModeStoreProvenInstalledV1
	if result, err := PublishV1(context.Background(), request); err != nil || !result.Reused {
		t.Fatalf("descriptor executable reuse = %#v, %v", result, err)
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("world-readable executable mode unexpectedly reused")
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(descriptor, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("second executable file unexpectedly reused")
	}
}

func TestArtifactRootWriteLeasePromotesOnlyExactInstalledExecutable(t *testing.T) {
	ctx := context.Background()
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	packageRoot, digest, size := writeLocalMCPArtifactFixtureV1(
		t, sourceRoot, "packages/promote-installed",
	)
	selection, err := SelectSourcePackageV1(
		sourceRoot, artifactRoot, "packages/promote-installed",
	)
	if err != nil {
		t.Fatal(err)
	}
	published, err := PublishV1(ctx, PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes:    moduleapi.MaxModuleSourcePackageBytesV1,
		ExistingModePolicy: ExistingModeRootCompatibleV1,
	})
	if err != nil || published.Reused {
		t.Fatalf("publish inert LOCAL_PROCESS artifact = %#v, %v", published, err)
	}
	reservation, err := PreparePhysicalArtifactReservationV1(
		ctx, packageRoot, digest, size,
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	var retained *ArtifactRootWriteLeaseV1
	err = WithArtifactRootWriteLeaseV1(
		ctx,
		root,
		func(lease *ArtifactRootWriteLeaseV1) error {
			retained = lease
			if err := lease.PromoteStoreProvenInstalledModesV1(ctx, reservation); err != nil {
				return err
			}
			return lease.PromoteStoreProvenInstalledModesV1(ctx, reservation)
		},
	)
	if err != nil {
		t.Fatalf("promote and exact retry: %v", err)
	}
	if err := retained.PromoteStoreProvenInstalledModesV1(ctx, reservation); err == nil {
		t.Fatal("retained lease promoted modes after callback release")
	}
	if err := VerifyExistingRootV1(
		ctx, root, digest, size, published.CoveredFileCount,
		ExistingModeStoreProvenInstalledV1,
	); err != nil {
		t.Fatalf("verify promoted installed closure: %v", err)
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(artifactRoot, digest)
		for path, want := range map[string]os.FileMode{
			filepath.Join(target, "content", "server.bin"):        0o700,
			filepath.Join(target, "content", "host.json"):         0o600,
			filepath.Join(target, moduleapi.ArtifactManifestPath): 0o600,
		} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("promoted mode %q = %v, %v; want %o", path, info, err, want)
			}
		}
	}
}

func TestPublishV1DoesNotRecreateMissingInstalledLocalTargetAsInert(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeLocalMCPArtifactFixtureV1(t, sourceRoot, "packages/missing-installed")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/missing-installed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes:    moduleapi.MaxModuleSourcePackageBytesV1,
		ExistingModePolicy: ExistingModeStoreProvenInstalledV1,
	}); err == nil {
		t.Fatal("missing installed LOCAL_PROCESS target unexpectedly recreated as inert ingress")
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing installed target was materialized: %v", err)
	}
}

func TestMissingInstalledLocalDecisionUsesCapturedBytes(t *testing.T) {
	sourceRoot := t.TempDir()
	packageRoot, digest, size := writeLocalMCPArtifactFixtureV1(
		t, sourceRoot, "packages/captured-installed",
	)
	limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureArtifactV1(
		context.Background(), packageRoot, digest, size, limits,
	)
	if err != nil {
		t.Fatal(err)
	}
	// A later source-path read must not influence the installed-mode decision.
	// Replace the live manifest with a non-local runtime after capture; the
	// captured closure must still identify the exact LOCAL_PROCESS executable.
	replacementEncoded, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "captured.installed",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestDeclarative,
			Protocol:   moduleapi.RuntimeProtocolStaticV1,
			Entrypoint: "content/server.bin",
		},
		Provides: []moduleapi.PortRef{{Name: "demo.port", ExactVersion: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := moduleapi.CanonicalJSON(replacementEncoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(packageRoot, moduleapi.ArtifactManifestPath), replacement, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	executable, err := exactLocalMCPExecutableFromCapturedV1(
		context.Background(), captured.manifest, captured.files,
	)
	if err != nil {
		t.Fatal(err)
	}
	if executable != "content/server.bin" {
		t.Fatalf("captured executable = %q", executable)
	}
}

func TestCaptureArtifactV1PreservesOnlyContextTermination(t *testing.T) {
	sourceRoot := t.TempDir()
	packageRoot, digest, size := writeArtifactFixtureV1(
		t,
		sourceRoot,
		"packages/context-termination",
	)
	limits, err := backupCompatibleLimitsV1(moduleapi.MaxModuleSourcePackageBytesV1)
	if err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, cancelDeadline := context.WithTimeout(context.Background(), 0)
	defer cancelDeadline()
	for _, test := range []struct {
		ctx  context.Context
		want error
	}{
		{ctx: canceled, want: context.Canceled},
		{ctx: deadline, want: context.DeadlineExceeded},
	} {
		captured, err := captureArtifactV1(test.ctx, packageRoot, digest, size, limits)
		if err != test.want {
			t.Fatalf("capture error = %v, want exact %v", err, test.want)
		}
		if captured.manifest != nil || captured.files != nil {
			t.Fatalf("terminated capture returned data = %#v", captured)
		}
	}

	const publicMessage = "module artifact store: fixed capture failure"
	if got := captureArtifactFailureV1(
		errors.Join(errors.New("private detail"), context.Canceled),
		publicMessage,
	); got != context.Canceled {
		t.Fatalf("wrapped cancellation = %v, want exact context.Canceled", got)
	}
	if got := captureArtifactFailureV1(
		errors.Join(errors.New("private detail"), context.DeadlineExceeded),
		publicMessage,
	); got != context.DeadlineExceeded {
		t.Fatalf("wrapped deadline = %v, want exact context.DeadlineExceeded", got)
	}
	if got := captureArtifactFailureV1(errors.New("private detail"), publicMessage); got == nil || got.Error() != publicMessage {
		t.Fatalf("ordinary capture failure = %v, want fixed public message", got)
	}
}

func TestFrozenArtifactRootDetectsPrivacyChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows privacy is covered by DACL-specific tests")
	}
	rootPath := privateArtifactTempDirV1(t)
	root, err := SelectArtifactRootV1(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rootPath, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := root.verifyCurrentV1(); err == nil {
		t.Fatal("artifact root privacy change unexpectedly accepted")
	}
}

func TestProvisionArtifactRootV1CreatesOnlyNewEmptyPrivateChild(t *testing.T) {
	parent := privateArtifactTempDirV1(t)
	root, err := ProvisionArtifactRootV1(parent, "module-artifacts")
	if err != nil {
		t.Fatalf("ProvisionArtifactRootV1() error = %v", err)
	}
	if err := root.verifyCurrentV1(); err != nil {
		t.Fatalf("provisioned root verification: %v", err)
	}
	existing := filepath.Join(parent, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProvisionArtifactRootV1(parent, "existing"); err == nil {
		t.Fatal("existing live root unexpectedly adopted")
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("existing live root was modified: %q, %v", content, err)
	}
}

func TestSelectSourcePackageV1RejectsOverlapTraversalAndLink(t *testing.T) {
	sourceRoot := t.TempDir()
	_, _, _ = writeArtifactFixtureV1(t, sourceRoot, "packages/demo")
	if _, err := SelectSourcePackageV1(sourceRoot, sourceRoot, "packages/demo"); err == nil {
		t.Fatal("equal roots unexpectedly accepted")
	}
	if _, err := SelectSourcePackageV1(sourceRoot, filepath.Join(sourceRoot, "artifacts"), "packages/demo"); err == nil {
		t.Fatal("nested artifact root unexpectedly accepted")
	}
	if _, err := SelectSourcePackageV1(sourceRoot, privateArtifactTempDirV1(t), "../escape"); err == nil {
		t.Fatal("traversal unexpectedly accepted")
	}
	link := filepath.Join(sourceRoot, "packages", "linked")
	if err := os.Symlink(filepath.Join(sourceRoot, "packages", "demo"), link); err == nil {
		if _, err := SelectSourcePackageV1(sourceRoot, privateArtifactTempDirV1(t), "packages/linked"); err == nil {
			t.Fatal("linked package unexpectedly accepted")
		}
	}
}

func TestPublishV1PhysicalQuotaRejectsUnknownCrashStageAndInvalidDigestTree(t *testing.T) {
	for name, arrange := range map[string]func(*testing.T, string){
		"unknown direct child": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "operator-notes.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"invalid pseudo-stage": func(t *testing.T, root string) {
			if err := os.Mkdir(filepath.Join(root, stageBasePrefixV1+"crashed"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"digest directory mismatch": func(t *testing.T, root string) {
			_, _, _ = writeArtifactFixtureV1(t, root, strings.Repeat("a", 64))
		},
		"canonical non-manifest": func(t *testing.T, root string) {
			manifest := []byte(`{"not_a_module_manifest":true}`)
			digest, err := moduleapi.ComputeArtifactDigest(manifest, nil)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(root, digest)
			if err := os.Mkdir(artifact, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(artifact, moduleapi.ArtifactManifestPath), manifest, 0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
			_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/quota")
			selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/quota")
			if err != nil {
				t.Fatal(err)
			}
			arrange(t, artifactRoot)
			if _, err := PublishV1(context.Background(), PublishRequestV1{
				Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
				MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
			}); err == nil {
				t.Fatal("invalid physical artifact root unexpectedly accepted")
			}
		})
	}
}

func TestPublishV1RecoversValidCrashStageUnderExclusiveLease(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/recovery")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/recovery")
	if err != nil {
		t.Fatal(err)
	}
	stageName := stageBasePrefixV1 + "2147483646-12345"
	stagePath := filepath.Join(artifactRoot, stageName)
	if err := os.MkdirAll(filepath.Join(stagePath, "partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV1(context.Background(), PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
	}); err != nil {
		t.Fatalf("PublishV1(recover crash stage): %v", err)
	}
	if _, err := os.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered stage remains: %v", err)
	}
}

func TestArtifactRootWriteLeaseRecoversStrictLegacyApplyStage(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		artifactRoot := privateArtifactTempDirV1(t)
		stagePath := filepath.Join(
			artifactRoot,
			legacyModuleApplyStageBasePrefixV1+"4294967295",
		)
		if err := os.MkdirAll(filepath.Join(stagePath, "partial"), 0o700); err != nil {
			t.Fatal(err)
		}
		root, err := SelectArtifactRootV1(artifactRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := WithArtifactRootWriteLeaseV1(
			context.Background(), root,
			func(*ArtifactRootWriteLeaseV1) error { return nil },
		); err != nil {
			t.Fatalf("recover legacy Apply stage: %v", err)
		}
		if _, err := os.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy Apply stage remains: %v", err)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		artifactRoot := privateArtifactTempDirV1(t)
		stagePath := filepath.Join(
			artifactRoot,
			legacyModuleApplyStageBasePrefixV1+"01",
		)
		if err := os.Mkdir(stagePath, 0o700); err != nil {
			t.Fatal(err)
		}
		root, err := SelectArtifactRootV1(artifactRoot)
		if err != nil {
			t.Fatal(err)
		}
		called := false
		err = WithArtifactRootWriteLeaseV1(
			context.Background(), root,
			func(*ArtifactRootWriteLeaseV1) error {
				called = true
				return nil
			},
		)
		if err == nil || called {
			t.Fatalf("malformed legacy stage error=%v callback=%v", err, called)
		}
		if _, err := os.Lstat(stagePath); err != nil {
			t.Fatalf("malformed legacy stage was removed: %v", err)
		}
	})
}

func TestArtifactRootWriteLeaseBoundsDirectEnumerationBeforeCallback(t *testing.T) {
	artifactRoot := privateArtifactTempDirV1(t)
	// Acquisition adds the fixed lease control file. Seeding the full direct
	// entry ceiling therefore makes the bounded recovery enumeration reject
	// the next entry without allocating an unbounded root listing.
	for index := uint64(0); index < maxArtifactRootDirectEntriesV1; index++ {
		if err := os.Mkdir(
			filepath.Join(artifactRoot, fmt.Sprintf("unknown-%03d", index)),
			0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	root, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = WithArtifactRootWriteLeaseV1(
		context.Background(), root,
		func(*ArtifactRootWriteLeaseV1) error {
			called = true
			return nil
		},
	)
	if err == nil || called {
		t.Fatalf("overfull direct root error=%v callback=%v", err, called)
	}
}

func TestArtifactRootControlEntryPredicateAcceptsOnlyExactBasename(t *testing.T) {
	if !IsArtifactRootControlEntryV1(artifactRootLeaseNameV1) {
		t.Fatal("exact lease control basename was not recognized")
	}
	for _, input := range []string{
		"",
		"./" + artifactRootLeaseNameV1,
		"child/" + artifactRootLeaseNameV1,
		strings.ToUpper(artifactRootLeaseNameV1),
		artifactRootLeaseNameV1 + ".other",
	} {
		if IsArtifactRootControlEntryV1(input) {
			t.Fatalf("non-exact lease control name %q was recognized", input)
		}
	}
}

func TestPhysicalArtifactReservationRequiresHeldWriteLease(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	packageRoot, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/reservation")
	reservation, err := PreparePhysicalArtifactReservationV1(
		context.Background(), packageRoot, digest, size,
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	var retained *ArtifactRootWriteLeaseV1
	err = WithArtifactRootWriteLeaseV1(
		context.Background(), root,
		func(lease *ArtifactRootWriteLeaseV1) error {
			retained = lease
			if err := lease.VerifyPhysicalReservationV1(
				context.Background(), reservation, false,
			); err != nil {
				return err
			}
			if err := lease.VerifyPhysicalReservationV1(
				context.Background(), reservation, true,
			); err == nil {
				return errors.New("missing target unexpectedly satisfied required reservation")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.VerifyPhysicalReservationV1(
		context.Background(), reservation, false,
	); err == nil {
		t.Fatal("callback-scoped lease remained usable after release")
	}
}

func TestArtifactRootFileLeaseIsExclusiveAcrossHandles(t *testing.T) {
	artifactRoot := privateArtifactTempDirV1(t)
	path := filepath.Join(artifactRoot, artifactRootLeaseNameV1)
	firstFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := provisionArtifactLeaseFilePrivateV1(path); err != nil {
		firstFile.Close()
		t.Fatal(err)
	}
	secondFile, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		firstFile.Close()
		t.Fatal(err)
	}
	first, busy, err := tryLockArtifactRootFileV1(firstFile)
	if err != nil || busy {
		secondFile.Close()
		firstFile.Close()
		t.Fatalf("first root lease = %v, busy=%v", err, busy)
	}
	if second, busy, err := tryLockArtifactRootFileV1(secondFile); err != nil || !busy || second != nil {
		_ = first.releaseV1()
		secondFile.Close()
		t.Fatalf("overlapping root lease = %#v, busy=%v, err=%v", second, busy, err)
	}
	if err := first.releaseV1(); err != nil {
		secondFile.Close()
		t.Fatal(err)
	}
	second, busy, err := tryLockArtifactRootFileV1(secondFile)
	if err != nil || busy {
		secondFile.Close()
		t.Fatalf("root lease after release = %v, busy=%v", err, busy)
	}
	if err := second.releaseV1(); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactRootLeaseWaitHonorsContextCancellation(t *testing.T) {
	artifactRoot := privateArtifactTempDirV1(t)
	root, err := SelectArtifactRootV1(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancelImmediately := context.WithCancel(context.Background())
	cancelImmediately()
	if _, err := acquireArtifactRootLeaseV1(canceled, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("immediately canceled lease error = %v, want context.Canceled", err)
	}
	key := artifactLeasePathKeyV1(root.directory.path)
	value, _ := artifactRootLock.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	locked := true
	defer func() {
		if locked {
			mutex.Unlock()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := acquireArtifactRootLeaseV1(ctx, root)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lease waiter error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled in-process lease waiter did not return")
	}
	mutex.Unlock()
	locked = false
}

func TestPhysicalQuotaReservationV1BoundsRetainedOrphans(t *testing.T) {
	one := physicalNamespaceV1{paths: 1, files: 1, pathBytes: 1}
	if err := checkPhysicalQuotaReservationV1(
		MaxPhysicalArtifactsV1, 1, physicalNamespaceV1{}, false, 1, one, false,
	); err == nil {
		t.Fatal("257th physical artifact reservation unexpectedly accepted")
	}
	if err := checkPhysicalQuotaReservationV1(
		1, MaxPhysicalCoveredBytesV1-1, one, false, 2, one, false,
	); err == nil {
		t.Fatal("over-limit physical byte reservation unexpectedly accepted")
	}
	if err := checkPhysicalQuotaReservationV1(
		MaxPhysicalArtifactsV1, MaxPhysicalCoveredBytesV1,
		physicalNamespaceV1{
			paths: MaxPhysicalPathsV1, files: MaxPhysicalFilesV1,
			pathBytes: MaxPhysicalPathBytesV1,
		},
		true, 1, physicalNamespaceV1{}, true,
	); err != nil {
		t.Fatalf("exact target at quota unexpectedly rejected: %v", err)
	}
	if err := checkPhysicalQuotaReservationV1(
		0, 0, physicalNamespaceV1{}, false, 1, physicalNamespaceV1{}, true,
	); err == nil {
		t.Fatal("required exact target absence unexpectedly accepted")
	}
	for name, test := range map[string]struct {
		current physicalNamespaceV1
		target  physicalNamespaceV1
	}{
		"paths": {
			current: physicalNamespaceV1{paths: MaxPhysicalPathsV1, files: 1, pathBytes: 1},
			target:  one,
		},
		"files": {
			current: physicalNamespaceV1{paths: 1, files: MaxPhysicalFilesV1, pathBytes: 1},
			target:  one,
		},
		"path bytes": {
			current: physicalNamespaceV1{paths: 1, files: 1, pathBytes: MaxPhysicalPathBytesV1},
			target:  one,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkPhysicalQuotaReservationV1(
				1, 1, test.current, false, 1, test.target, false,
			); err == nil {
				t.Fatal("over-limit physical namespace reservation unexpectedly accepted")
			}
		})
	}
}

func writeArtifactFixtureV1(t *testing.T, sourceRoot, relative string) (string, string, uint64) {
	t.Helper()
	root := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello\n")
	if err := os.WriteFile(filepath.Join(root, "content", "entry.txt"), payload, 0o600); err != nil {
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
	if _, _, err := moduleapi.ParseModuleManifestV1(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, moduleapi.ArtifactManifestPath), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	files := []moduleapi.ArtifactFile{{Path: "content/entry.txt", Content: payload}}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	return root, digest, uint64(len(manifest) + len(payload))
}

func writeLocalMCPArtifactFixtureV1(t *testing.T, sourceRoot, relative string) (string, string, uint64) {
	t.Helper()
	root := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("exact server bytes\n")
	executableSum := sha256.Sum256(executable)
	descriptorValue := mcpstdio.HostDescriptorV1{
		SchemaVersion:    mcpstdio.HostDescriptorSchemaV1,
		ProtocolVersion:  mcpstdio.ProtocolVersionV1,
		Executable:       "content/server.bin",
		ExecutableSHA256: hex.EncodeToString(executableSum[:]),
		WorkingDirectory: "content",
		Tools:            []mcpstdio.ToolBindingV1{},
	}
	descriptorEncoded, err := json.Marshal(descriptorValue)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := moduleapi.CanonicalJSON(descriptorEncoded)
	if err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "demo.mcp", Version: "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestLocalProcess,
			Protocol:   moduleapi.RuntimeProtocolMCPStdio20251125,
			Entrypoint: "content/host.json",
		},
		Provides: []moduleapi.PortRef{{Name: "action.provider", ExactVersion: "1"}},
	}
	manifestEncoded, err := json.Marshal(manifestValue)
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
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file.Path)), file.Content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, moduleapi.ArtifactManifestPath), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, digest, uint64(len(manifest) + len(descriptor) + len(executable))
}

func assertNoStagesV1(t *testing.T, artifactRoot string) {
	t.Helper()
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagePrefixV1) {
			t.Fatalf("stage residue %q", entry.Name())
		}
	}
}
