package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/moduleconformance"
)

func TestRunDispatchesModuleVerifyWithStableSafeJSON(t *testing.T) {
	t.Parallel()
	artifact := moduleVerifyFixture(t, "declarative-role")
	args := []string{"module-verify", "--artifact", artifact}

	var first bytes.Buffer
	var firstStderr bytes.Buffer
	if err := run(context.Background(), args, &first, &firstStderr); err != nil {
		t.Fatal(err)
	}
	if firstStderr.Len() != 0 {
		t.Fatalf("successful verification wrote stderr: %q", firstStderr.String())
	}
	var report moduleconformance.Report
	if err := json.Unmarshal(first.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, first.String())
	}
	if report.SchemaVersion != moduleconformance.ReportSchemaVersionV1 ||
		report.Module.ID != "freeagent.compat.role" ||
		report.ArtifactDigest == "" || report.ArtifactSizeBytes == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if bytes.Count(first.Bytes(), []byte("\n")) != 1 ||
		bytes.Contains(first.Bytes(), []byte(filepath.Clean(artifact))) {
		t.Fatalf("unstable or path-leaking output: %s", first.String())
	}
	const golden = `{"schema_version":"freeagent.module-package-verification/v1","package_api_version":"freeagent.module/v1","module":{"id":"freeagent.compat.role","exact_version":"1.0.0"},"artifact_digest":"a67bb9fc8d4eeffa8fed0a60bb398592b2d68b4eabe945ab6990623b72df8883","artifact_size_bytes":355,"covered_file_count":2,"runtime_request":{"mode":"DECLARATIVE","protocol":"static/v1","entrypoint":"content/context.json"},"provides":[{"name":"context.provide","exact_version":"v1"}],"requires":[],"requested_permissions":[]}` + "\n"
	if first.String() != golden {
		t.Fatalf("module verification golden changed:\ngot  %s\nwant %s", first.String(), golden)
	}
	for _, forbidden := range []string{
		`"manifest"`, `"config_schema"`, `"lifecycle"`, `"health"`,
		`"execution_class"`, `"authority"`, `"activation"`,
	} {
		if bytes.Contains(first.Bytes(), []byte(forbidden)) {
			t.Fatalf("output contains forbidden field %s: %s", forbidden, first.String())
		}
	}

	var second bytes.Buffer
	if err := run(context.Background(), args, &second, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("repeated command changed bytes:\nfirst=%s\nsecond=%s", first.String(), second.String())
	}
}

func TestRunModuleVerifyValidatesArgumentsBeforeDependency(t *testing.T) {
	t.Parallel()
	calls := 0
	verify := func(context.Context, string) (moduleconformance.Report, error) {
		calls++
		return moduleconformance.Report{}, nil
	}
	tests := [][]string{
		nil,
		{"--artifact", ""},
		{"--artifact", "   "},
		{"--artifact", "artifact", "trailing"},
		{"--artifact"},
		{"--Authorization-sk-example-secret"},
		{"--help"},
	}
	for _, args := range tests {
		var output bytes.Buffer
		var stderr bytes.Buffer
		if err := runModuleVerifyWithDependency(
			context.Background(), args, &output, &stderr, verify,
		); err == nil {
			t.Fatalf("invalid args accepted: %#v", args)
		}
		if output.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("invalid args wrote output=%q stderr=%q", output.String(), stderr.String())
		}
	}
	if calls != 0 {
		t.Fatalf("verify called %d times for invalid args", calls)
	}
}

func TestRunModuleVerifyDependencyBoundaryAndWriteFailure(t *testing.T) {
	t.Parallel()
	if err := runModuleVerifyWithDependency(
		nil, nil, io.Discard, io.Discard,
		func(context.Context, string) (moduleconformance.Report, error) {
			return moduleconformance.Report{}, nil
		},
	); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("nil context error=%v", err)
	}
	if err := runModuleVerifyWithDependency(
		context.Background(), nil, io.Discard, io.Discard, nil,
	); err == nil || !strings.Contains(err.Error(), "dependency is nil") {
		t.Fatalf("nil dependency error=%v", err)
	}

	wantPath := "opaque-artifact"
	privateSentinel := "C:" + "/private/Authorization-sk-example-secret/module.yaml"
	wantErr := errors.New(privateSentinel)
	called := 0
	verify := func(ctx context.Context, path string) (moduleconformance.Report, error) {
		called++
		if ctx == nil || path != wantPath {
			t.Fatalf("verify ctx/path=%v/%q", ctx, path)
		}
		return moduleconformance.Report{}, wantErr
	}
	var output bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleVerifyWithDependency(
		context.Background(),
		[]string{"--artifact", wantPath},
		&output,
		&stderr,
		verify,
	)
	if err == nil || err.Error() !=
		"freeagent module-verify: verification failed (INTERNAL_ERROR)" ||
		called != 1 || output.Len() != 0 || stderr.Len() != 0 ||
		strings.Contains(err.Error(), privateSentinel) {
		t.Fatalf("dependency failure err=%v calls=%d output=%q", err, called, output.String())
	}

	valid := moduleconformance.Report{
		SchemaVersion:     moduleconformance.ReportSchemaVersionV1,
		PackageAPIVersion: "freeagent.module/v1",
		Module: moduleconformance.ModuleIdentity{
			ID:           "freeagent.compat.synthetic",
			ExactVersion: "1.0.0",
		},
		ArtifactDigest:       strings.Repeat("a", 64),
		ArtifactSizeBytes:    1,
		CoveredFileCount:     1,
		Provides:             []moduleconformance.PortRef{},
		Requires:             []moduleconformance.PortRef{},
		RequestedPermissions: []string{},
	}
	writerErr := errors.New("C:" + "/private/output-with-secret stopped")
	err = runModuleVerifyWithDependency(
		context.Background(),
		[]string{"--artifact", wantPath},
		failingModuleVerifyWriter{err: writerErr},
		io.Discard,
		func(context.Context, string) (moduleconformance.Report, error) {
			return valid, nil
		},
	)
	if err == nil || err.Error() != "freeagent module-verify: write report failed" ||
		strings.Contains(err.Error(), writerErr.Error()) {
		t.Fatalf("writer error=%v", err)
	}
}

func TestRunModuleVerifyHidesCauseForKnownVerificationCode(t *testing.T) {
	t.Parallel()
	const sentinel = "Authorization-sk-example-secret-private-manifest"
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "module.yaml"),
		[]byte(`{"api_version":"freeagent.module/v1","private":"`+sentinel+`"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleVerifyWithDependency(
		context.Background(),
		[]string{"--artifact", root},
		&stdout,
		&stderr,
		moduleconformance.VerifyDirectory,
	)
	if err == nil || err.Error() !=
		"freeagent module-verify: verification failed (MANIFEST_INVALID)" ||
		stdout.Len() != 0 || stderr.Len() != 0 ||
		strings.Contains(err.Error(), sentinel) {
		t.Fatalf("known-code failure err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestModuleVerifyIsListedInUsage(t *testing.T) {
	t.Parallel()
	err := commandUsageError()
	if err == nil || !strings.Contains(err.Error(), "|module-verify|") {
		t.Fatalf("usage error=%v", err)
	}
}

type failingModuleVerifyWriter struct{ err error }

func (writer failingModuleVerifyWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func moduleVerifyFixture(t *testing.T, name string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(
		filepath.Dir(current),
		"..", "..", "sdk", "moduleapi", "testdata", "compat", "v1", name,
	)
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}
