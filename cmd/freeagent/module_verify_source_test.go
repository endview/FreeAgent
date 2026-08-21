package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRunModuleVerifySourcePolicyPreservesLegacyReportWire(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsigned", true: "signed"}[signed], func(t *testing.T) {
			fixture := newModuleVerifySupplyFixture(t, signed)
			var legacy bytes.Buffer
			if err := runModuleVerify(
				context.Background(),
				[]string{"--artifact", fixture.artifactRoot},
				&legacy,
				io.Discard,
			); err != nil {
				t.Fatalf("legacy module-verify: %v", err)
			}

			args := []string{
				"--artifact", fixture.artifactRoot,
				"--source-root", fixture.sourceRoot,
				"--source-policy", fixture.policyPath,
				"--source-policy-id", fixture.policyID,
			}
			if signed {
				args = append(args,
					"--publisher-key", fixture.publisherPath,
					"--publisher-key-id", fixture.publisherID,
					"--signature", fixture.signaturePath,
					"--signature-id", fixture.signatureID,
				)
			}
			var governed bytes.Buffer
			if err := runModuleVerify(
				context.Background(),
				args,
				&governed,
				io.Discard,
			); err != nil {
				t.Fatalf("governed module-verify: %v", err)
			}
			if !bytes.Equal(governed.Bytes(), legacy.Bytes()) {
				t.Fatalf(
					"source verification changed report wire:\nsource=%s\nlegacy=%s",
					governed.String(),
					legacy.String(),
				)
			}
		})
	}
}

func TestRunModuleVerifySourceFlagsFailBeforeDependencies(t *testing.T) {
	t.Parallel()
	calls := 0
	dependencies := moduleVerifyDependencies{
		verifyLegacy: func(context.Context, string) (moduleconformance.Report, error) {
			calls++
			return moduleconformance.Report{}, nil
		},
		verifySource: func(
			context.Context,
			moduleconformance.SourceCandidateInput,
		) (moduleconformance.Report, error) {
			calls++
			return moduleconformance.Report{}, nil
		},
		readCanonical: func(context.Context, string) ([]byte, error) {
			calls++
			return nil, nil
		},
	}
	invalid := [][]string{
		{"--artifact", "artifact", "--source-root", "source"},
		{"--artifact", "artifact", "--source-policy", "policy"},
		{
			"--artifact", "artifact",
			"--source-root", "source",
			"--source-policy", "policy",
			"--source-policy-id", strings.Repeat("A", 64),
		},
		{
			"--artifact", "artifact",
			"--source-root", "source",
			"--source-policy", "policy",
			"--source-policy-id", strings.Repeat("a", 64),
			"--publisher-key", "key",
		},
		{
			"--artifact", "artifact",
			"--source-root", "source",
			"--source-policy", "policy",
			"--source-policy-id", strings.Repeat("a", 64),
			"--revoked-publisher-key-id", "invalid",
		},
	}
	for _, args := range invalid {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		err := runModuleVerifyWithDependencies(
			context.Background(),
			args,
			&stdout,
			&stderr,
			dependencies,
		)
		if err == nil || err.Error() !=
			"freeagent module-verify: verification failed (SOURCE_INPUT_INVALID)" {
			t.Fatalf("args=%#v error=%v", args, err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("invalid flags wrote stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid source flags called dependencies %d times", calls)
	}
}

func TestRunModuleVerifyRejectsRemoteAndRevokedPolicyBeforeKeyReads(t *testing.T) {
	t.Parallel()
	localRoot := t.TempDir()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{91}, ed25519.SeedSize))
	publisher, _, publisherID, err := moduleapi.NewModulePublisherKeyV1(
		moduleapi.ModulePublisherKeyV1{
			SchemaVersion: moduleapi.ModulePublisherKeySchemaVersionV1,
			Algorithm:     moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(
				privateKey.Public().(ed25519.PublicKey),
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	signatureID := strings.Repeat("b", 64)

	tests := []struct {
		name     string
		kind     moduleapi.ModuleSourceKindV1
		network  moduleapi.ModuleSourceNetworkV1
		origin   string
		revoked  bool
		wantCode string
	}{
		{
			name:     "https",
			kind:     moduleapi.ModuleSourceKindHTTPSIndexV1,
			network:  moduleapi.ModuleSourceNetworkExactHTTPSV1,
			origin:   "https://modules.example.invalid/index.json",
			wantCode: "SOURCE_INPUT_INVALID",
		},
		{
			name:     "revoked",
			kind:     moduleapi.ModuleSourceKindLocalDirectoryV1,
			network:  moduleapi.ModuleSourceNetworkDenyV1,
			origin:   filepath.ToSlash(localRoot),
			revoked:  true,
			wantCode: "PUBLISHER_KEY_REVOKED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
				test.kind,
				[]byte(test.origin),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
				moduleapi.ModuleSourcePolicyV1{
					SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
					SourceID:                "source.precheck",
					Kind:                    test.kind,
					OriginDigest:            originDigest,
					Network:                 test.network,
					SignatureRequired:       true,
					PublisherKeyID:          publisher.PublisherKeyID,
					AllowedModuleIDPrefixes: []string{"freeagent"},
					MaxIndexBytes:           4096,
					MaxPackageBytes:         4096,
					MaxCandidates:           1,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			reads := []string{}
			sourceCalls := 0
			dependencies := moduleVerifyDependencies{
				verifyLegacy: func(context.Context, string) (moduleconformance.Report, error) {
					t.Fatal("legacy verifier called")
					return moduleconformance.Report{}, nil
				},
				verifySource: func(
					context.Context,
					moduleconformance.SourceCandidateInput,
				) (moduleconformance.Report, error) {
					sourceCalls++
					return moduleconformance.Report{}, nil
				},
				readCanonical: func(_ context.Context, path string) ([]byte, error) {
					reads = append(reads, path)
					if path != "policy" {
						t.Fatalf("key/signature read before policy rejection: %q", path)
					}
					return bytes.Clone(policyCanonical), nil
				},
			}
			args := []string{
				"--artifact", "artifact",
				"--source-root", localRoot,
				"--source-policy", "policy",
				"--source-policy-id", policyID,
				"--publisher-key", "key",
				"--publisher-key-id", publisherID,
				"--signature", "signature",
				"--signature-id", signatureID,
			}
			if test.revoked {
				args = append(args, "--revoked-publisher-key-id", publisherID)
			}
			err = runModuleVerifyWithDependencies(
				context.Background(),
				args,
				io.Discard,
				io.Discard,
				dependencies,
			)
			want := "freeagent module-verify: verification failed (" + test.wantCode + ")"
			if err == nil || err.Error() != want || sourceCalls != 0 ||
				!reflect.DeepEqual(reads, []string{"policy"}) {
				t.Fatalf("err=%v sourceCalls=%d reads=%v", err, sourceCalls, reads)
			}
		})
	}
}

func TestRunModuleVerifyClassifiesCanonicalReadCancellation(t *testing.T) {
	t.Parallel()
	sourceCalls := 0
	localRoot := t.TempDir()
	err := runModuleVerifyWithDependencies(
		context.Background(),
		[]string{
			"--artifact", "artifact",
			"--source-root", localRoot,
			"--source-policy", "policy",
			"--source-policy-id", strings.Repeat("a", 64),
		},
		io.Discard,
		io.Discard,
		moduleVerifyDependencies{
			verifyLegacy: func(context.Context, string) (moduleconformance.Report, error) {
				t.Fatal("legacy verifier called")
				return moduleconformance.Report{}, nil
			},
			verifySource: func(
				context.Context,
				moduleconformance.SourceCandidateInput,
			) (moduleconformance.Report, error) {
				sourceCalls++
				return moduleconformance.Report{}, nil
			},
			readCanonical: func(context.Context, string) ([]byte, error) {
				return nil, context.Canceled
			},
		},
	)
	if err == nil || err.Error() !=
		"freeagent module-verify: verification failed (VERIFY_CANCELLED)" ||
		sourceCalls != 0 {
		t.Fatalf("cancelled read error=%v sourceCalls=%d", err, sourceCalls)
	}
}

func TestReadModuleVerifyCanonicalFileIsBoundedStableAndCancellable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	validPath := filepath.Join(root, "policy.json")
	valid := []byte(`{"schema_version":"test/v1"}`)
	if err := os.WriteFile(validPath, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readModuleVerifyCanonicalFile(context.Background(), validPath)
	if err != nil || !bytes.Equal(got, valid) {
		t.Fatalf("valid canonical file got=%q err=%v", got, err)
	}
	got[0] = 'X'
	again, err := readModuleVerifyCanonicalFile(context.Background(), validPath)
	if err != nil || !bytes.Equal(again, valid) {
		t.Fatal("canonical reader returned aliased bytes")
	}

	oversize := filepath.Join(root, "oversize.json")
	if err := os.WriteFile(
		oversize,
		bytes.Repeat([]byte{'x'}, moduleVerifyCanonicalFileMaxBytes+1),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readModuleVerifyCanonicalFile(context.Background(), oversize); err == nil {
		t.Fatal("oversize canonical file accepted")
	}
	if runtime.GOOS == "windows" {
		adsPath := validPath + ":policy"
		if !unsafeModuleVerifyWindowsPath(adsPath) {
			t.Fatal("NTFS alternate data stream path was not classified as unsafe")
		}
		if _, err := readModuleVerifyCanonicalFile(context.Background(), adsPath); err == nil {
			t.Fatal("NTFS alternate data stream was accepted")
		}
	}

	symlink := filepath.Join(root, "policy-link.json")
	if err := os.Symlink(validPath, symlink); err == nil {
		if _, err := readModuleVerifyCanonicalFile(context.Background(), symlink); err == nil {
			t.Fatal("symlinked canonical file accepted")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readModuleVerifyCanonicalFile(cancelled, validPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error=%v", err)
	}
}

type moduleVerifySupplyFixture struct {
	sourceRoot    string
	artifactRoot  string
	policyPath    string
	policyID      string
	publisherPath string
	publisherID   string
	signaturePath string
	signatureID   string
}

func newModuleVerifySupplyFixture(
	t *testing.T,
	signed bool,
) moduleVerifySupplyFixture {
	t.Helper()
	sourceRoot := t.TempDir()
	artifactRoot := filepath.Join(sourceRoot, "package")
	copyModuleVerifyFixture(t, moduleVerifyFixture(t, "declarative-role"), artifactRoot)
	report, err := moduleconformance.VerifyDirectory(context.Background(), artifactRoot)
	if err != nil {
		t.Fatal(err)
	}

	var (
		privateKey         ed25519.PrivateKey
		publisher          moduleapi.ModulePublisherKeyV1
		publisherCanonical []byte
		publisherID        string
	)
	if signed {
		privateKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{77}, ed25519.SeedSize))
		publisher, publisherCanonical, publisherID, err =
			moduleapi.NewModulePublisherKeyV1(moduleapi.ModulePublisherKeyV1{
				SchemaVersion: moduleapi.ModulePublisherKeySchemaVersionV1,
				Algorithm:     moduleapi.ModuleSignatureAlgorithmEd25519V1,
				PublicKeyBase64: base64.StdEncoding.EncodeToString(
					privateKey.Public().(ed25519.PublicKey),
				),
			})
		if err != nil {
			t.Fatal(err)
		}
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(sourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.module-verify",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			SignatureRequired:       signed,
			PublisherKeyID:          publisher.PublisherKeyID,
			AllowedModuleIDPrefixes: []string{"freeagent.compat"},
			MaxIndexBytes:           64 << 10,
			MaxPackageBytes:         1 << 20,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelopeRoot := t.TempDir()
	fixture := moduleVerifySupplyFixture{
		sourceRoot:   sourceRoot,
		artifactRoot: artifactRoot,
		policyPath:   filepath.Join(envelopeRoot, "source-policy.json"),
		policyID:     policyID,
	}
	writeModuleVerifyEnvelope(t, fixture.policyPath, policyCanonical)
	if !signed {
		return fixture
	}
	message, err := moduleapi.ModuleSignatureInputV1(report.ArtifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	_, signatureCanonical, signatureID, err := moduleapi.NewModuleSignatureV1(
		moduleapi.ModuleSignatureV1{
			SchemaVersion:  moduleapi.ModuleSignatureSchemaVersionV1,
			Algorithm:      moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublisherKeyID: publisherID,
			ArtifactDigest: report.ArtifactDigest,
			SignatureBase64: base64.StdEncoding.EncodeToString(
				ed25519.Sign(privateKey, message),
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.publisherPath = filepath.Join(envelopeRoot, "publisher-key.json")
	fixture.publisherID = publisherID
	fixture.signaturePath = filepath.Join(envelopeRoot, "signature.json")
	fixture.signatureID = signatureID
	writeModuleVerifyEnvelope(t, fixture.publisherPath, publisherCanonical)
	writeModuleVerifyEnvelope(t, fixture.signaturePath, signatureCanonical)
	return fixture
}

func copyModuleVerifyFixture(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(
		source,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			if relative == "." {
				return os.MkdirAll(destination, 0o700)
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
		},
	); err != nil {
		t.Fatal(err)
	}
}

func writeModuleVerifyEnvelope(t *testing.T, path string, canonical []byte) {
	t.Helper()
	if err := os.WriteFile(path, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
}
