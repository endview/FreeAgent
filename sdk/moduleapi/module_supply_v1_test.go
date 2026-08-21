package moduleapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestModulePublisherKeyV1CanonicalCanaryAndRestore(t *testing.T) {
	_, publicKey, publisher, canonical, keyID := testModulePublisherKeyV1(t, 7)
	expected := `{"algorithm":"ED25519","public_key_base64":"` +
		base64.StdEncoding.EncodeToString(publicKey) +
		`","publisher_key_id":"` + keyID +
		`","schema_version":"module-publisher-key/v1"}`
	if string(canonical) != expected {
		t.Fatalf("publisher canonical = %s\nwant = %s", canonical, expected)
	}
	restored, err := RestoreModulePublisherKeyV1(canonical, keyID)
	if err != nil {
		t.Fatalf("RestoreModulePublisherKeyV1() error = %v", err)
	}
	if restored != publisher {
		t.Fatalf("restored publisher = %+v\nwant = %+v", restored, publisher)
	}

	mutated := publisher
	mutated.PublicKeyBase64 = base64.StdEncoding.EncodeToString(publicKey[:31])
	if _, _, _, err := NewModulePublisherKeyV1(mutated); err == nil {
		t.Fatal("short publisher key accepted")
	}
	if _, err := RestoreModulePublisherKeyV1(
		append([]byte{' '}, canonical...),
		keyID,
	); err == nil {
		t.Fatal("non-canonical publisher key accepted")
	}
}

func TestModuleSignatureV1Ed25519PositiveAndNegativeMatrix(t *testing.T) {
	privateKey, _, publisher, _, _ := testModulePublisherKeyV1(t, 11)
	artifactDigest := strings.Repeat("a", SHA256HexLength)
	message, err := ModuleSignatureInputV1(artifactDigest)
	if err != nil {
		t.Fatalf("ModuleSignatureInputV1() error = %v", err)
	}
	signatureBytes := ed25519.Sign(privateKey, message)
	signature, canonical, signatureID, err := NewModuleSignatureV1(
		ModuleSignatureV1{
			SchemaVersion:   ModuleSignatureSchemaVersionV1,
			Algorithm:       ModuleSignatureAlgorithmEd25519V1,
			PublisherKeyID:  publisher.PublisherKeyID,
			ArtifactDigest:  artifactDigest,
			SignatureBase64: base64.StdEncoding.EncodeToString(signatureBytes),
		},
	)
	if err != nil {
		t.Fatalf("NewModuleSignatureV1() error = %v", err)
	}
	if err := VerifyModuleSignatureV1(signature, publisher); err != nil {
		t.Fatalf("VerifyModuleSignatureV1() error = %v", err)
	}
	if _, err := RestoreModuleSignatureV1(canonical, signatureID); err != nil {
		t.Fatalf("RestoreModuleSignatureV1() error = %v", err)
	}

	_, _, wrongPublisher, _, _ := testModulePublisherKeyV1(t, 12)
	if err := VerifyModuleSignatureV1(signature, wrongPublisher); err == nil {
		t.Fatal("wrong publisher key accepted")
	}

	wrongDigest := signature
	wrongDigest.ArtifactDigest = strings.Repeat("b", SHA256HexLength)
	if err := VerifyModuleSignatureV1(wrongDigest, publisher); err == nil {
		t.Fatal("signature accepted for wrong artifact digest")
	}

	truncated := signature
	truncated.SignatureBase64 = base64.StdEncoding.EncodeToString(signatureBytes[:63])
	if _, _, _, err := NewModuleSignatureV1(truncated); err == nil {
		t.Fatal("truncated signature accepted")
	}

	replacedKeyID := signature
	replacedKeyID.PublisherKeyID = wrongPublisher.PublisherKeyID
	if err := VerifyModuleSignatureV1(replacedKeyID, wrongPublisher); err == nil {
		t.Fatal("publisher key ID substitution accepted")
	}

	if string(message) != moduleSignatureInputDomainV1+"\x00"+artifactDigest {
		t.Fatalf("signature input = %q", message)
	}
	message[0] = 'X'
	again, _ := ModuleSignatureInputV1(artifactDigest)
	if bytes.Equal(message, again) {
		t.Fatal("signature input aliases a shared buffer")
	}
}

func TestModuleSourcePolicyV1FreezesLocalOperatorConstraints(t *testing.T) {
	_, _, publisher, _, _ := testModulePublisherKeyV1(t, 19)
	originDigest, err := ModuleSourceOriginDigestV1(
		ModuleSourceKindLocalDirectoryV1,
		[]byte(`local-root/module-source`),
	)
	if err != nil {
		t.Fatalf("ModuleSourceOriginDigestV1() error = %v", err)
	}
	prefixes := []string{"vendor.beta", "vendor.alpha"}
	policy, canonical, policyID, err := NewModuleSourcePolicyV1(
		ModuleSourcePolicyV1{
			SchemaVersion:           ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.modules",
			Kind:                    ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 ModuleSourceNetworkDenyV1,
			SignatureRequired:       true,
			PublisherKeyID:          publisher.PublisherKeyID,
			AllowedModuleIDPrefixes: prefixes,
			MaxIndexBytes:           64 << 10,
			MaxPackageBytes:         8 << 20,
			MaxCandidates:           32,
		},
	)
	if err != nil {
		t.Fatalf("NewModuleSourcePolicyV1() error = %v", err)
	}
	prefixes[0] = "mutated"
	if got := policy.AllowedModuleIDPrefixes; len(got) != 2 ||
		got[0] != "vendor.alpha" || got[1] != "vendor.beta" {
		t.Fatalf("frozen prefixes = %v", got)
	}
	restored, err := RestoreModuleSourcePolicyV1(canonical, policyID)
	if err != nil {
		t.Fatalf("RestoreModuleSourcePolicyV1() error = %v", err)
	}
	restored.AllowedModuleIDPrefixes[0] = "changed"
	if bytes.Contains(canonical, []byte("changed")) {
		t.Fatal("restored source policy aliases canonical bytes")
	}

	wrongNetwork := policy
	wrongNetwork.Network = ModuleSourceNetworkExactHTTPSV1
	if _, _, _, err := NewModuleSourcePolicyV1(wrongNetwork); err == nil {
		t.Fatal("local source with network access accepted")
	}
	unsignedWithKey := policy
	unsignedWithKey.SignatureRequired = false
	if _, _, _, err := NewModuleSourcePolicyV1(unsignedWithKey); err == nil {
		t.Fatal("unsigned source policy with publisher key accepted")
	}
	if _, err := ModuleSourceOriginDigestV1(
		ModuleSourceKindLocalDirectoryV1,
		[]byte(" local-root/module-source "),
	); err == nil {
		t.Fatal("padded source origin accepted")
	}
}

func TestModuleSupplyV1RejectsUnknownFields(t *testing.T) {
	privateKey, _, publisher, publisherCanonical, publisherID :=
		testModulePublisherKeyV1(t, 23)
	artifactDigest := strings.Repeat("c", SHA256HexLength)
	message, _ := ModuleSignatureInputV1(artifactDigest)
	_, signatureCanonical, signatureID, err := NewModuleSignatureV1(
		ModuleSignatureV1{
			SchemaVersion:   ModuleSignatureSchemaVersionV1,
			Algorithm:       ModuleSignatureAlgorithmEd25519V1,
			PublisherKeyID:  publisher.PublisherKeyID,
			ArtifactDigest:  artifactDigest,
			SignatureBase64: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, _ := ModuleSourceOriginDigestV1(
		ModuleSourceKindLocalDirectoryV1,
		[]byte(`local-root/modules`),
	)
	_, policyCanonical, policyID, err := NewModuleSourcePolicyV1(
		ModuleSourcePolicyV1{
			SchemaVersion:           ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.modules",
			Kind:                    ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 ModuleSourceNetworkDenyV1,
			SignatureRequired:       true,
			PublisherKeyID:          publisher.PublisherKeyID,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           1024,
			MaxPackageBytes:         4096,
			MaxCandidates:           2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		canonical []byte
		restore   func([]byte) error
	}{
		{
			name:      "publisher key",
			canonical: publisherCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModulePublisherKeyV1(value, publisherID)
				return err
			},
		},
		{
			name:      "signature",
			canonical: signatureCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleSignatureV1(value, signatureID)
				return err
			},
		},
		{
			name:      "source policy",
			canonical: policyCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleSourcePolicyV1(value, policyID)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withUnknown := moduleSupplyAddUnknownField(t, test.canonical)
			if err := test.restore(withUnknown); err == nil {
				t.Fatal("unknown field accepted")
			}
		})
	}
}

func testModulePublisherKeyV1(
	t *testing.T,
	seedByte byte,
) (ed25519.PrivateKey, ed25519.PublicKey, ModulePublisherKeyV1, []byte, string) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seedByte}, ed25519.SeedSize))
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	publisher, canonical, keyID, err := NewModulePublisherKeyV1(
		ModulePublisherKeyV1{
			SchemaVersion:   ModulePublisherKeySchemaVersionV1,
			Algorithm:       ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		},
	)
	if err != nil {
		t.Fatalf("NewModulePublisherKeyV1() error = %v", err)
	}
	return privateKey, publicKey, publisher, canonical, keyID
}

func moduleSupplyAddUnknownField(t *testing.T, canonical []byte) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	value["unknown"] = true
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
