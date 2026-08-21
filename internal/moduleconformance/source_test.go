package moduleconformance

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestVerifySourceCandidateDirectoryAcceptsExactSignedAndUnsignedInputs(t *testing.T) {
	t.Parallel()

	for _, signed := range []bool{true, false} {
		signed := signed
		t.Run(map[bool]string{true: "signed", false: "unsigned"}[signed], func(t *testing.T) {
			t.Parallel()
			fixture := newSourceFixture(t, "vendor.module", signed, []string{"vendor"})

			got, err := VerifySourceCandidateDirectory(context.Background(), fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, fixture.report) {
				t.Fatalf("source report changed legacy report:\ngot=%+v\nwant=%+v", got, fixture.report)
			}
		})
	}
}

func TestVerifySourceCandidateDirectoryRequiresExactExternalAnchors(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", true, []string{"vendor"})
	otherKey, otherKeyCanonical, otherKeyID, _ := newPublisherKey(t, 0x42)
	_ = otherKey

	tests := []struct {
		name string
		edit func(*SourceCandidateInput)
		code FailureCode
	}{
		{
			name: "missing policy ID",
			edit: func(input *SourceCandidateInput) { input.SourcePolicyID = "" },
			code: FailureSourceInput,
		},
		{
			name: "wrong policy ID",
			edit: func(input *SourceCandidateInput) { input.SourcePolicyID = strings.Repeat("a", 64) },
			code: FailureSourceInput,
		},
		{
			name: "missing policy canonical",
			edit: func(input *SourceCandidateInput) { input.SourcePolicyCanonical = nil },
			code: FailureSourceInput,
		},
		{
			name: "missing publisher key ID",
			edit: func(input *SourceCandidateInput) { input.PublisherKeyID = "" },
			code: FailureSignatureNeeded,
		},
		{
			name: "missing publisher key canonical",
			edit: func(input *SourceCandidateInput) { input.PublisherKeyCanonical = nil },
			code: FailureSignatureNeeded,
		},
		{
			name: "missing signature ID",
			edit: func(input *SourceCandidateInput) { input.SignatureID = "" },
			code: FailureSignatureNeeded,
		},
		{
			name: "missing signature canonical",
			edit: func(input *SourceCandidateInput) { input.SignatureCanonical = nil },
			code: FailureSignatureNeeded,
		},
		{
			name: "wrong publisher key ID",
			edit: func(input *SourceCandidateInput) {
				input.PublisherKeyID = otherKeyID
				input.PublisherKeyCanonical = bytes.Clone(otherKeyCanonical)
			},
			code: FailureSignatureInvalid,
		},
		{
			name: "wrong publisher key material under expected anchor",
			edit: func(input *SourceCandidateInput) {
				input.PublisherKeyCanonical = bytes.Clone(otherKeyCanonical)
			},
			code: FailureSignatureInvalid,
		},
		{
			name: "wrong signature ID",
			edit: func(input *SourceCandidateInput) { input.SignatureID = strings.Repeat("b", 64) },
			code: FailureSignatureInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneSourceCandidateInput(fixture.input)
			test.edit(&input)
			_, err := VerifySourceCandidateDirectory(context.Background(), input)
			requireSourceFailure(t, err, test.code)
		})
	}
}

func TestVerifySourceCandidateDirectoryRejectsWrongOriginKeyDigestAndSignature(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", true, []string{"vendor"})

	t.Run("wrong origin", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		policy := fixture.policy
		policy.OriginDigest = strings.Repeat("c", 64)
		setSourcePolicy(t, &input, policy)
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceDenied)
	})

	t.Run("HTTPS policy cannot drive local preflight", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		policy := fixture.policy
		policy.Kind = moduleapi.ModuleSourceKindHTTPSIndexV1
		policy.Network = moduleapi.ModuleSourceNetworkExactHTTPSV1
		setSourcePolicy(t, &input, policy)
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceInput)
	})

	t.Run("signature artifact digest differs", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		wrongDigest := strings.Repeat("d", 64)
		setSignature(t, &input, fixture.privateKey, fixture.publisherID, wrongDigest)
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSignatureInvalid)
	})

	t.Run("signature bytes do not verify", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		_, canonical, signatureID, err := moduleapi.NewModuleSignatureV1(
			moduleapi.ModuleSignatureV1{
				SchemaVersion:   moduleapi.ModuleSignatureSchemaVersionV1,
				Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
				PublisherKeyID:  fixture.publisherID,
				ArtifactDigest:  fixture.report.ArtifactDigest,
				SignatureBase64: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		input.SignatureID = signatureID
		input.SignatureCanonical = canonical
		_, err = VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSignatureInvalid)
	})

	t.Run("artifact changes after signature", func(t *testing.T) {
		separate := newSourceFixture(t, "vendor.module", true, []string{"vendor"})
		path := filepath.Join(separate.input.ArtifactRoot, "content", "data.json")
		if err := os.WriteFile(path, []byte(`{"changed":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := VerifySourceCandidateDirectory(context.Background(), separate.input)
		requireSourceFailure(t, err, FailureSignatureInvalid)
	})
}

func TestVerifySourceCandidateDirectoryRevocationIsExactAndDenyOnly(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", true, []string{"vendor"})
	_, _, unrelatedID, _ := newPublisherKey(t, 0x74)

	t.Run("selected key revoked", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		input.RevokedPublisherKeyIDs = []string{fixture.publisherID}
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailurePublisherRevoked)
	})

	t.Run("unrelated key revoked", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		input.RevokedPublisherKeyIDs = []string{unrelatedID}
		got, err := VerifySourceCandidateDirectory(context.Background(), input)
		if err != nil || !reflect.DeepEqual(got, fixture.report) {
			t.Fatalf("unrelated revocation changed candidate: report=%+v error=%v", got, err)
		}
	})

	t.Run("duplicate revocation", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		input.RevokedPublisherKeyIDs = []string{unrelatedID, unrelatedID}
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceInput)
	})

	t.Run("invalid revocation ID", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		input.RevokedPublisherKeyIDs = []string{"NOT-A-DIGEST"}
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceInput)
	})

	t.Run("revocation set over invocation ceiling", func(t *testing.T) {
		input := cloneSourceCandidateInput(fixture.input)
		for index := 0; index <= int(moduleapi.MaxModuleDiscoveryCandidatesV1); index++ {
			input.RevokedPublisherKeyIDs = append(
				input.RevokedPublisherKeyIDs,
				moduleapi.Digest(
					"freeagent.test-revoked-key/v1",
					[]byte{byte(index >> 8), byte(index)},
				),
			)
		}
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceInput)
	})

	t.Run("unsigned policy rejects signature-only revocation input", func(t *testing.T) {
		unsigned := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
		input := cloneSourceCandidateInput(unsigned.input)
		input.RevokedPublisherKeyIDs = []string{unrelatedID}
		_, err := VerifySourceCandidateDirectory(context.Background(), input)
		requireSourceFailure(t, err, FailureSourceInput)
	})
}

func TestVerifySourceCandidateDirectoryUsesDottedPrefixBoundary(t *testing.T) {
	t.Parallel()

	allowed := newSourceFixture(t, "vendor.tool", false, []string{"vendor"})
	if _, err := VerifySourceCandidateDirectory(context.Background(), allowed.input); err != nil {
		t.Fatalf("dotted child rejected: %v", err)
	}
	exact := newSourceFixture(t, "vendor", false, []string{"vendor"})
	if _, err := VerifySourceCandidateDirectory(context.Background(), exact.input); err != nil {
		t.Fatalf("exact prefix rejected: %v", err)
	}
	outside := newSourceFixture(t, "vendorx.tool", false, []string{"vendor"})
	_, err := VerifySourceCandidateDirectory(context.Background(), outside.input)
	requireSourceFailure(t, err, FailureSourceDenied)
}

func TestVerifySourceCandidateDirectoryPassesPolicyLimitToBothScans(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
	policy := fixture.policy
	policy.MaxPackageBytes = fixture.report.ArtifactSizeBytes
	setSourcePolicy(t, &fixture.input, policy)

	var firstLimit, finalLimit uint64
	got, err := verifySourceCandidateDirectory(
		context.Background(),
		fixture.input,
		func(_ context.Context, root string, maxPackageBytes uint64) (Report, error) {
			if root != fixture.input.ArtifactRoot {
				t.Fatalf("first verifier root=%q want=%q", root, fixture.input.ArtifactRoot)
			}
			firstLimit = maxPackageBytes
			return fixture.report, nil
		},
		func(_ context.Context, root, digest string, size, maxPackageBytes uint64) error {
			if root != fixture.input.ArtifactRoot ||
				digest != fixture.report.ArtifactDigest || size != fixture.report.ArtifactSizeBytes {
				t.Fatalf("final verifier received wrong frozen report facts")
			}
			finalLimit = maxPackageBytes
			return nil
		},
	)
	if err != nil || !reflect.DeepEqual(got, fixture.report) {
		t.Fatalf("source verification report=%+v error=%v", got, err)
	}
	if firstLimit != policy.MaxPackageBytes || finalLimit != policy.MaxPackageBytes {
		t.Fatalf("limits first/final=%d/%d want=%d", firstLimit, finalLimit, policy.MaxPackageBytes)
	}
}

func TestVerifySourceCandidateDirectoryEnforcesExactPackageLimitOnFirstScan(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
	policy := fixture.policy
	policy.MaxPackageBytes = fixture.report.ArtifactSizeBytes
	setSourcePolicy(t, &fixture.input, policy)
	if _, err := VerifySourceCandidateDirectory(context.Background(), fixture.input); err != nil {
		t.Fatalf("package at exact policy limit was rejected: %v", err)
	}

	path := filepath.Join(fixture.input.ArtifactRoot, "content", "data.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = VerifySourceCandidateDirectory(context.Background(), fixture.input)
	requireSourceFailure(t, err, FailureArtifactInvalid)
}

func TestVerifySourceCandidateDirectoryRejectsArtifactOutsideSource(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
	input := cloneSourceCandidateInput(fixture.input)
	input.SourceRoot = t.TempDir()
	_, err := VerifySourceCandidateDirectory(context.Background(), input)
	requireSourceFailure(t, err, FailureSourceInput)

	if sourcePathContains(
		windowsTestPath("safe", "root"),
		windowsTestPath("safe", "root-other"),
	) {
		t.Fatal("textual path prefix was treated as directory containment")
	}
}

func TestResolveStableSourceDirectoryRejectsWindowsNetworkNamespacesBeforeOpen(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows namespace contract")
	}
	for _, path := range []string{
		string([]byte{'\\', '\\'}) + windowsTestTail("server.invalid", "share", "missing"),
		string([]byte{'\\', '\\', '?', '\\', 'C', ':', '\\'}) + "missing",
		string([]byte{'\\', '\\', '.', '\\', 'C', ':', '\\'}) + "missing",
		string([]byte{'\\', '?', '?', '\\', 'C', ':', '\\'}) + "missing",
	} {
		if !unsafeWindowsSourceNamespace(path) {
			t.Fatalf("unsafe namespace was not recognized: %q", path)
		}
		_, err := resolveStableSourceDirectory(path, "source root")
		if err == nil ||
			(!strings.Contains(err.Error(), "UNC or device namespace") &&
				!strings.Contains(err.Error(), "canonical absolute filesystem path")) {
			t.Fatalf("namespace %q reached filesystem resolution: %v", path, err)
		}
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			t.Fatalf("namespace %q reached an OS filesystem call: %v", path, err)
		}
	}
	if unsafeWindowsSourceNamespace(windowsTestPath("safe", "local")) {
		t.Fatal("ordinary drive-local path classified as network namespace")
	}
}

func TestVerifySourceCandidateDirectoryRejectsCancellationAndNilDependencies(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})

	if _, err := VerifySourceCandidateDirectory(nil, fixture.input); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifySourceCandidateDirectory(ctx, fixture.input); !errors.Is(err, context.Canceled) || FailureCodeOf(err) != FailureCancelled {
		t.Fatalf("cancelled source verification error=%v code=%s", err, FailureCodeOf(err))
	}
	noopFinal := func(context.Context, string, string, uint64, uint64) error { return nil }
	noopPackage := func(context.Context, string, uint64) (Report, error) { return fixture.report, nil }
	if _, err := verifySourceCandidateDirectory(
		context.Background(), fixture.input, nil, noopFinal,
	); err == nil {
		t.Fatal("nil package verifier accepted")
	}
	if _, err := verifySourceCandidateDirectory(
		context.Background(), fixture.input, noopPackage, nil,
	); err == nil {
		t.Fatal("nil final verifier accepted")
	}
}

func TestVerifySourceCandidateDirectoryDetectsFinalPassTOCTOU(t *testing.T) {
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
	_, err := verifySourceCandidateDirectory(
		context.Background(),
		fixture.input,
		VerifyDirectoryWithPackageLimit,
		func(ctx context.Context, root, digest string, size, maxPackageBytes uint64) error {
			path := filepath.Join(root, "content", "data.json")
			if err := os.WriteFile(path, []byte(`{"changed-after-first-pass":true}`), 0o600); err != nil {
				return err
			}
			return verifySourceArtifactWithPackageLimit(ctx, root, digest, size, maxPackageBytes)
		},
	)
	requireSourceFailure(t, err, FailureSourceDrift)
}

func TestVerifySourceCandidateDirectoryReturnsDefensiveNonSensitiveReport(t *testing.T) {
	t.Parallel()
	fixture := newSourceFixture(t, "vendor.module", false, []string{"vendor"})
	provided := fixture.report
	provided.Requires = []PortRef{{Name: "memory.read", ExactVersion: "v1"}}
	provided.RequestedPermissions = []string{"synthetic.permission"}
	original := cloneReport(provided)

	got, err := verifySourceCandidateDirectory(
		context.Background(),
		fixture.input,
		func(context.Context, string, uint64) (Report, error) { return provided, nil },
		func(context.Context, string, string, uint64, uint64) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	got.Provides[0].Name = "mutated"
	got.Requires[0].Name = "mutated"
	got.RequestedPermissions[0] = "mutated"
	if !reflect.DeepEqual(provided, original) {
		t.Fatalf("returned report aliases dependency-owned slices:\ngot=%+v\nwant=%+v", provided, original)
	}

	actual, err := VerifySourceCandidateDirectory(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		filepath.Base(fixture.input.SourceRoot),
		fixture.policy.SourceID,
		string(fixture.input.SourcePolicyCanonical),
		string(fixture.input.PublisherKeyCanonical),
		string(fixture.input.SignatureCanonical),
	} {
		if forbidden != "" && bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("source report leaked detached input %q: %s", forbidden, encoded)
		}
	}
}

func TestSourceFailureCodesAreClosedAndNonSensitive(t *testing.T) {
	t.Parallel()
	privateCause := windowsTestPath("private", "source", "Authorization-sk-example")
	for _, code := range []FailureCode{
		FailureSourceInput,
		FailureSourceDenied,
		FailureSignatureNeeded,
		FailureSignatureInvalid,
		FailurePublisherRevoked,
		FailureSourceDrift,
	} {
		err := verificationFailure(code, "fixed safe source label", errors.New(privateCause))
		if got := FailureCodeOf(err); got != code {
			t.Fatalf("source failure code=%s want=%s", got, code)
		}
		if strings.Contains(err.Error(), privateCause) {
			t.Fatalf("source failure %s leaked private cause", code)
		}
	}
}

func windowsTestPath(parts ...string) string {
	return string([]byte{'C', ':', '\\'}) + windowsTestTail(parts...)
}

func windowsTestTail(parts ...string) string {
	return strings.Join(parts, string([]byte{'\\'}))
}

type sourceFixture struct {
	input       SourceCandidateInput
	report      Report
	policy      moduleapi.ModuleSourcePolicyV1
	publisherID string
	privateKey  ed25519.PrivateKey
}

func newSourceFixture(
	t *testing.T,
	moduleID string,
	signed bool,
	prefixes []string,
) sourceFixture {
	t.Helper()
	sourceRoot := t.TempDir()
	artifactRoot := filepath.Join(sourceRoot, "candidate")
	writeExactFile(t, filepath.Join(artifactRoot, "module.yaml"), []byte(
		`{"api_version":"freeagent.module/v1","id":"`+moduleID+`","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/data.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"1.0.0"}`,
	))
	writeExactFile(t, filepath.Join(artifactRoot, "content", "data.json"), []byte(`{"value":"fixture"}`))
	report, err := VerifyDirectory(context.Background(), artifactRoot)
	if err != nil {
		t.Fatal(err)
	}

	resolvedSource, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(resolvedSource)),
	)
	if err != nil {
		t.Fatal(err)
	}
	publisher, publisherCanonical, publisherID, privateKey := newPublisherKey(t, 0x31)
	_ = publisher
	policyInput := moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                "operator.local.source",
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		SignatureRequired:       signed,
		AllowedModuleIDPrefixes: append([]string(nil), prefixes...),
		MaxIndexBytes:           4096,
		MaxPackageBytes:         report.ArtifactSizeBytes + 4096,
		MaxCandidates:           8,
	}
	if signed {
		policyInput.PublisherKeyID = publisherID
	}
	policy, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(policyInput)
	if err != nil {
		t.Fatal(err)
	}
	input := SourceCandidateInput{
		ArtifactRoot:          artifactRoot,
		SourceRoot:            sourceRoot,
		SourcePolicyID:        policyID,
		SourcePolicyCanonical: policyCanonical,
	}
	if signed {
		input.PublisherKeyID = publisherID
		input.PublisherKeyCanonical = publisherCanonical
		setSignature(t, &input, privateKey, publisherID, report.ArtifactDigest)
	}
	return sourceFixture{
		input:       input,
		report:      report,
		policy:      policy,
		publisherID: publisherID,
		privateKey:  privateKey,
	}
}

func newPublisherKey(
	t *testing.T,
	seedByte byte,
) (moduleapi.ModulePublisherKeyV1, []byte, string, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte{seedByte})
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	publisher, canonical, keyID, err := moduleapi.NewModulePublisherKeyV1(
		moduleapi.ModulePublisherKeyV1{
			SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
			Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return publisher, canonical, keyID, privateKey
}

func setSignature(
	t *testing.T,
	input *SourceCandidateInput,
	privateKey ed25519.PrivateKey,
	publisherID string,
	artifactDigest string,
) {
	t.Helper()
	message, err := moduleapi.ModuleSignatureInputV1(artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, signatureID, err := moduleapi.NewModuleSignatureV1(
		moduleapi.ModuleSignatureV1{
			SchemaVersion:   moduleapi.ModuleSignatureSchemaVersionV1,
			Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublisherKeyID:  publisherID,
			ArtifactDigest:  artifactDigest,
			SignatureBase64: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.SignatureID = signatureID
	input.SignatureCanonical = canonical
}

func setSourcePolicy(
	t *testing.T,
	input *SourceCandidateInput,
	policy moduleapi.ModuleSourcePolicyV1,
) {
	t.Helper()
	frozen, canonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	input.SourcePolicyID = policyID
	input.SourcePolicyCanonical = canonical
	_ = frozen
}

func cloneSourceCandidateInput(input SourceCandidateInput) SourceCandidateInput {
	input.SourcePolicyCanonical = bytes.Clone(input.SourcePolicyCanonical)
	input.PublisherKeyCanonical = bytes.Clone(input.PublisherKeyCanonical)
	input.SignatureCanonical = bytes.Clone(input.SignatureCanonical)
	input.RevokedPublisherKeyIDs = append([]string(nil), input.RevokedPublisherKeyIDs...)
	return input
}

func requireSourceFailure(t *testing.T, err error, code FailureCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected source verification failure %s", code)
	}
	if got := FailureCodeOf(err); got != code {
		t.Fatalf("source failure code=%s want=%s error=%v", got, code, err)
	}
}
