package currentbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyArtifactScanErrorSeparatesCancellationFromIntegrity(t *testing.T) {
	t.Parallel()

	for _, cancellation := range []error{
		context.Canceled,
		context.DeadlineExceeded,
	} {
		classified := classifyArtifactScanError(cancellation)
		if !errors.Is(classified, cancellation) {
			t.Fatalf("classified error = %v, want %v", classified, cancellation)
		}
		if errors.Is(classified, ErrIntegrity) {
			t.Fatalf("cancellation was classified as integrity failure: %v", classified)
		}
	}

	scanFailure := errors.New("scan failed")
	classified := classifyArtifactScanError(scanFailure)
	if !errors.Is(classified, ErrIntegrity) ||
		!errors.Is(classified, scanFailure) {
		t.Fatalf("ordinary scan error classification = %v", classified)
	}
}

func TestClassifyArtifactManifestReadErrorSeparatesCancellationFromIntegrity(t *testing.T) {
	t.Parallel()

	for _, cancellation := range []error{
		context.Canceled,
		context.DeadlineExceeded,
	} {
		classified := classifyArtifactManifestReadError(cancellation)
		if !errors.Is(classified, cancellation) {
			t.Fatalf("classified error = %v, want %v", classified, cancellation)
		}
		if errors.Is(classified, ErrIntegrity) {
			t.Fatalf("cancellation was classified as integrity failure: %v", classified)
		}
	}

	readFailure := errors.New("read failed")
	classified := classifyArtifactManifestReadError(readFailure)
	if !errors.Is(classified, ErrIntegrity) ||
		errors.Is(classified, context.Canceled) ||
		errors.Is(classified, context.DeadlineExceeded) {
		t.Fatalf("ordinary manifest read error classification = %v", classified)
	}
}

func TestCopyVerifiedArtifactDoesNotEscapeReplacedSourceDescendant(t *testing.T) {
	local := newBackupLocalMCPArtifact(t)
	verified, err := verifyArtifactDirectory(local.directory, local.digest)
	if err != nil {
		t.Fatalf("verify source artifact: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(outside, "external-marker.txt"),
		[]byte("external-marker"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	restore, err := replaceCurrentBackupTestDirectoryWithExternalLink(
		filepath.Join(local.directory, "content"),
		outside,
	)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "copied")
	err = copyVerifiedArtifact(
		context.Background(),
		local.directory,
		destination,
		verified,
	)
	restore()
	if err == nil {
		t.Fatal("copy accepted a replaced source descendant")
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed copy left destination behind: %v", statErr)
	}
}
