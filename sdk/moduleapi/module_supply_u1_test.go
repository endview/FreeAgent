package moduleapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestParseModulePublisherKeyV1RequiresExactCanonicalAndReturnsOwnedValues(t *testing.T) {
	_, publicKey, wantPublisher, canonical, wantKeyID :=
		testModulePublisherKeyV1(t, 41)
	wantCanonical := bytes.Clone(canonical)
	input := bytes.Clone(canonical)

	gotPublisher, gotCanonical, gotKeyID, err := ParseModulePublisherKeyV1(input)
	if err != nil {
		t.Fatalf("ParseModulePublisherKeyV1() error = %v", err)
	}
	wantDerivedKeyID := Digest(
		modulePublisherKeyIDDomainV1,
		append(
			[]byte(string(ModuleSignatureAlgorithmEd25519V1)+"\x00"),
			publicKey...,
		),
	)
	if gotPublisher != wantPublisher || gotKeyID != wantKeyID ||
		gotKeyID != wantDerivedKeyID || !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatalf(
			"parsed publisher = %+v canonical=%s key_id=%s\nwant publisher = %+v canonical=%s key_id=%s",
			gotPublisher,
			gotCanonical,
			gotKeyID,
			wantPublisher,
			wantCanonical,
			wantDerivedKeyID,
		)
	}

	input[0] = '['
	if !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatal("parsed publisher canonical aliases caller input")
	}
	gotCanonical[0] = '['
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatal("parsed publisher canonical aliases constructor output")
	}

	if _, _, _, err := ParseModulePublisherKeyV1(
		append(bytes.Clone(wantCanonical), '\n'),
	); err == nil {
		t.Fatal("publisher parser accepted non-exact canonical bytes")
	}
}

func TestParseModuleSignatureV1RequiresExactCanonicalAndReturnsOwnedValues(t *testing.T) {
	privateKey, _, publisher, _, _ := testModulePublisherKeyV1(t, 43)
	artifactDigest := strings.Repeat("a", SHA256HexLength)
	message, err := ModuleSignatureInputV1(artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	wantSignature, canonical, wantSignatureID, err := NewModuleSignatureV1(
		ModuleSignatureV1{
			SchemaVersion:  ModuleSignatureSchemaVersionV1,
			Algorithm:      ModuleSignatureAlgorithmEd25519V1,
			PublisherKeyID: publisher.PublisherKeyID,
			ArtifactDigest: artifactDigest,
			SignatureBase64: base64.StdEncoding.EncodeToString(
				ed25519.Sign(privateKey, message),
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := bytes.Clone(canonical)
	input := bytes.Clone(canonical)

	gotSignature, gotCanonical, gotSignatureID, err := ParseModuleSignatureV1(input)
	if err != nil {
		t.Fatalf("ParseModuleSignatureV1() error = %v", err)
	}
	wantDerivedSignatureID := Digest(moduleSignatureIDDomainV1, wantCanonical)
	if gotSignature != wantSignature || gotSignatureID != wantSignatureID ||
		gotSignatureID != wantDerivedSignatureID ||
		!bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatalf(
			"parsed signature = %+v canonical=%s signature_id=%s\nwant signature = %+v canonical=%s signature_id=%s",
			gotSignature,
			gotCanonical,
			gotSignatureID,
			wantSignature,
			wantCanonical,
			wantDerivedSignatureID,
		)
	}

	input[0] = '['
	if !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatal("parsed signature canonical aliases caller input")
	}
	gotCanonical[0] = '['
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatal("parsed signature canonical aliases constructor output")
	}

	if _, _, _, err := ParseModuleSignatureV1(
		append([]byte{' '}, wantCanonical...),
	); err == nil {
		t.Fatal("signature parser accepted non-exact canonical bytes")
	}
}

func TestParseModuleSourcePolicyV1RequiresExactCanonicalAndReturnsOwnedValues(t *testing.T) {
	wantPolicy, canonical, wantPolicyID := testModuleSourcePolicyV1(t, true)
	wantCanonical := bytes.Clone(canonical)
	input := bytes.Clone(canonical)

	gotPolicy, gotCanonical, gotPolicyID, err := ParseModuleSourcePolicyV1(input)
	if err != nil {
		t.Fatalf("ParseModuleSourcePolicyV1() error = %v", err)
	}
	wantDerivedPolicyID := Digest(moduleSourcePolicyIDDomainV1, wantCanonical)
	if !reflect.DeepEqual(gotPolicy, wantPolicy) || gotPolicyID != wantPolicyID ||
		gotPolicyID != wantDerivedPolicyID || !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatalf(
			"parsed policy = %+v canonical=%s policy_id=%s\nwant policy = %+v canonical=%s policy_id=%s",
			gotPolicy,
			gotCanonical,
			gotPolicyID,
			wantPolicy,
			wantCanonical,
			wantDerivedPolicyID,
		)
	}

	input[0] = '['
	if !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatal("parsed source policy canonical aliases caller input")
	}
	gotCanonical[0] = '['
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatal("parsed source policy canonical aliases constructor output")
	}
	gotPolicy.AllowedModuleIDPrefixes[0] = "changed"
	if wantPolicy.AllowedModuleIDPrefixes[0] == "changed" ||
		bytes.Contains(canonical, []byte("changed")) {
		t.Fatal("parsed source policy aliases another frozen value")
	}

	if _, _, _, err := ParseModuleSourcePolicyV1(
		append(bytes.Clone(wantCanonical), ' '),
	); err == nil {
		t.Fatal("source policy parser accepted non-exact canonical bytes")
	}
}

func TestValidateModuleSourceCandidateV1EnforcesPrefixSizeAndSignaturePolicy(t *testing.T) {
	signedPolicy, _, _ := testModuleSourceCandidatePolicyV1(t, true, 4096)
	unsignedPolicy, _, _ := testModuleSourceCandidatePolicyV1(t, false, 4096)

	tests := []struct {
		name         string
		policy       ModuleSourcePolicyV1
		moduleID     string
		size         uint64
		hasSignature bool
		wantAllowed  bool
	}{
		{
			name:         "exact prefix",
			policy:       signedPolicy,
			moduleID:     "vendor",
			size:         signedPolicy.MaxPackageBytes,
			hasSignature: true,
			wantAllowed:  true,
		},
		{
			name:         "dotted child",
			policy:       signedPolicy,
			moduleID:     "vendor.tool",
			size:         1,
			hasSignature: true,
			wantAllowed:  true,
		},
		{
			name:         "plain string prefix is not a boundary",
			policy:       signedPolicy,
			moduleID:     "vendorx",
			size:         1,
			hasSignature: true,
			wantAllowed:  false,
		},
		{
			name:         "hyphen is not a dotted boundary",
			policy:       signedPolicy,
			moduleID:     "vendor-tool",
			size:         1,
			hasSignature: true,
			wantAllowed:  false,
		},
		{
			name:         "zero size",
			policy:       signedPolicy,
			moduleID:     "vendor.tool",
			size:         0,
			hasSignature: true,
			wantAllowed:  false,
		},
		{
			name:         "one beyond maximum",
			policy:       signedPolicy,
			moduleID:     "vendor.tool",
			size:         signedPolicy.MaxPackageBytes + 1,
			hasSignature: true,
			wantAllowed:  false,
		},
		{
			name:         "required signature absent",
			policy:       signedPolicy,
			moduleID:     "vendor.tool",
			size:         1,
			hasSignature: false,
			wantAllowed:  false,
		},
		{
			name:         "optional signature absent",
			policy:       unsignedPolicy,
			moduleID:     "vendor.tool",
			size:         1,
			hasSignature: false,
			wantAllowed:  true,
		},
		{
			name:         "optional signature present",
			policy:       unsignedPolicy,
			moduleID:     "vendor.tool",
			size:         1,
			hasSignature: true,
			wantAllowed:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateModuleSourceCandidateV1(
				test.policy,
				Ref{ID: test.moduleID, Version: "v1"},
				test.size,
				test.hasSignature,
			)
			if gotAllowed := err == nil; gotAllowed != test.wantAllowed {
				t.Fatalf(
					"ValidateModuleSourceCandidateV1() error = %v, allowed=%v want %v",
					err,
					gotAllowed,
					test.wantAllowed,
				)
			}
		})
	}
}

func TestModuleDiscoverySnapshotV1UsesSharedSourceCandidatePredicate(t *testing.T) {
	signedPolicy, signedCanonical, signedPolicyID :=
		testModuleSourceCandidatePolicyV1(t, true, 4096)
	unsignedPolicy, unsignedCanonical, unsignedPolicyID :=
		testModuleSourceCandidatePolicyV1(t, false, 4096)

	tests := []struct {
		name            string
		policy          ModuleSourcePolicyV1
		policyCanonical []byte
		policyID        string
		moduleID        string
		size            uint64
		hasSignature    bool
		wantAllowed     bool
	}{
		{
			name:            "exact prefix at exact maximum",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendor",
			size:            signedPolicy.MaxPackageBytes,
			hasSignature:    true,
			wantAllowed:     true,
		},
		{
			name:            "dotted child",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendor.tool",
			size:            1,
			hasSignature:    true,
			wantAllowed:     true,
		},
		{
			name:            "non-dotted string prefix",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendorx",
			size:            1,
			hasSignature:    true,
			wantAllowed:     false,
		},
		{
			name:            "hyphen boundary",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendor-tool",
			size:            1,
			hasSignature:    true,
			wantAllowed:     false,
		},
		{
			name:            "one beyond package maximum",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendor.tool",
			size:            signedPolicy.MaxPackageBytes + 1,
			hasSignature:    true,
			wantAllowed:     false,
		},
		{
			name:            "required signature absent",
			policy:          signedPolicy,
			policyCanonical: signedCanonical,
			policyID:        signedPolicyID,
			moduleID:        "vendor.tool",
			size:            1,
			hasSignature:    false,
			wantAllowed:     false,
		},
		{
			name:            "optional signature absent",
			policy:          unsignedPolicy,
			policyCanonical: unsignedCanonical,
			policyID:        unsignedPolicyID,
			moduleID:        "vendor.tool",
			size:            1,
			hasSignature:    false,
			wantAllowed:     true,
		},
		{
			name:            "optional signature present",
			policy:          unsignedPolicy,
			policyCanonical: unsignedCanonical,
			policyID:        unsignedPolicyID,
			moduleID:        "vendor.tool",
			size:            1,
			hasSignature:    true,
			wantAllowed:     true,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := testModuleDiscoveryEntryV1(test.moduleID, "v1", 'd')
			entry.ArtifactSizeBytes = test.size
			if !test.hasSignature {
				entry.SignatureID = ""
			}

			directErr := ValidateModuleSourceCandidateV1(
				test.policy,
				entry.Module,
				entry.ArtifactSizeBytes,
				entry.SignatureID != "",
			)
			if gotAllowed := directErr == nil; gotAllowed != test.wantAllowed {
				t.Fatalf(
					"direct source candidate error = %v, allowed=%v want %v",
					directErr,
					gotAllowed,
					test.wantAllowed,
				)
			}

			_, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
				ModuleDiscoveryIndexV1{
					SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
					SourceID:      test.policy.SourceID,
					Entries:       []ModuleDiscoveryEntryV1{entry},
				},
			)
			if err != nil {
				t.Fatalf("NewModuleDiscoveryIndexV1(case %d) error = %v", index, err)
			}
			_, _, _, snapshotErr := NewModuleDiscoverySnapshotV1(
				test.policyCanonical,
				test.policyID,
				indexCanonical,
				indexID,
			)
			if gotAllowed := snapshotErr == nil; gotAllowed != test.wantAllowed {
				t.Fatalf(
					"snapshot source candidate error = %v, allowed=%v want %v; direct error = %v",
					snapshotErr,
					gotAllowed,
					test.wantAllowed,
					directErr,
				)
			}
		})
	}
}

func testModuleSourceCandidatePolicyV1(
	t *testing.T,
	signatureRequired bool,
	maxPackageBytes uint64,
) (ModuleSourcePolicyV1, []byte, string) {
	t.Helper()
	policy, _, _ := testModuleSourcePolicyV1(t, signatureRequired)
	policy.MaxPackageBytes = maxPackageBytes
	frozen, canonical, policyID, err := NewModuleSourcePolicyV1(policy)
	if err != nil {
		t.Fatalf("NewModuleSourcePolicyV1() error = %v", err)
	}
	return frozen, canonical, policyID
}
