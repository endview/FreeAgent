package moduleapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestModuleSupplyV1CanonicalCanaries(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(
		bytes.Repeat([]byte{42}, ed25519.SeedSize),
	)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	publisher, publisherCanonical, publisherID, err := NewModulePublisherKeyV1(
		ModulePublisherKeyV1{
			SchemaVersion:   ModulePublisherKeySchemaVersionV1,
			Algorithm:       ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := strings.Repeat("a", SHA256HexLength)
	message, err := ModuleSignatureInputV1(artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
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
	originDigest, err := ModuleSourceOriginDigestV1(
		ModuleSourceKindLocalDirectoryV1,
		[]byte("local-root/canary"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := NewModuleSourcePolicyV1(
		ModuleSourcePolicyV1{
			SchemaVersion:           ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.canary",
			Kind:                    ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 ModuleSourceNetworkDenyV1,
			SignatureRequired:       true,
			PublisherKeyID:          publisher.PublisherKeyID,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           65536,
			MaxPackageBytes:         8388608,
			MaxCandidates:           16,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := ModuleDiscoveryEntryV1{
		Module: Ref{
			ID:      "vendor.tool",
			Version: "build-10",
		},
		ArtifactDigest:    artifactDigest,
		ArtifactSizeBytes: 4096,
		PackagePath:       "vendor.tool/build-10.zip",
		SignatureID:       signatureID,
	}
	_, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.canary",
			Entries:       []ModuleDiscoveryEntryV1{entry},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshotCanonical, snapshotID, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, candidateCanonical, candidateID, err := NewModuleUpgradeCandidateV1(
		ModuleUpgradeCandidateV1{
			SchemaVersion: ModuleUpgradeCandidateSchemaVersionV1,
			Change:        ModuleCandidateChangeInstallV1,
			Target:        entry,
		},
		snapshotCanonical,
		snapshotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, decisionCanonical, decisionID, err := NewModuleCandidateDecisionV1(
		ModuleCandidateDecisionV1{
			SchemaVersion:       ModuleCandidateDecisionSchemaVersionV1,
			CandidateID:         candidateID,
			Decision:            ModuleCandidateDecisionApproveV1,
			OperatorPrincipalID: "operator.canary",
			Reason:              "verified exact candidate",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	canaries := []struct {
		name      string
		canonical []byte
		want      string
		id        string
		wantID    string
	}{
		{
			name:      "publisher",
			canonical: publisherCanonical,
			want:      `{"algorithm":"ED25519","public_key_base64":"GX9rI+FshTLGq8g4+s1ep4m+DHaykgM0A5v6iz02jWE=","publisher_key_id":"ea3ba29f43ddfc58cc1e515ff94f3b9eb568c87fd0564e334fd7f060eae5260e","schema_version":"module-publisher-key/v1"}`,
			id:        publisherID,
			wantID:    "ea3ba29f43ddfc58cc1e515ff94f3b9eb568c87fd0564e334fd7f060eae5260e",
		},
		{
			name:      "signature",
			canonical: signatureCanonical,
			want:      `{"algorithm":"ED25519","artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","publisher_key_id":"ea3ba29f43ddfc58cc1e515ff94f3b9eb568c87fd0564e334fd7f060eae5260e","schema_version":"module-signature/v1","signature_base64":"4hOAi/ZVFCCMV4y6UgLeULyzByytkba0/273mXp4KDKmRWrK8eHnH5QIWz2sqh6ymZ7h1xC0UBIWB8geDS5lBg=="}`,
			id:        signatureID,
			wantID:    "a9834168d81f69efe2199a7f55b040edc30af110c82d811c134dc29dceb6a365",
		},
		{
			name:      "policy",
			canonical: policyCanonical,
			want:      `{"allowed_module_id_prefixes":["vendor"],"kind":"LOCAL_DIRECTORY","max_candidates":16,"max_index_bytes":65536,"max_package_bytes":8388608,"network":"DENY","origin_digest":"bb0ae0ba97c6365be58681601b0ea4d57dd1dbec91773c3bc1ceed1a37ad7970","publisher_key_id":"ea3ba29f43ddfc58cc1e515ff94f3b9eb568c87fd0564e334fd7f060eae5260e","schema_version":"module-source-policy/v1","signature_required":true,"source_id":"local.canary"}`,
			id:        policyID,
			wantID:    "cdc4f798011b970a185413106503dade0066389c14329f6dcca73a6588f6bb77",
		},
		{
			name:      "index",
			canonical: indexCanonical,
			want:      `{"entries":[{"artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifact_size_bytes":4096,"module":{"id":"vendor.tool","version":"build-10"},"package_path":"vendor.tool/build-10.zip","signature_id":"a9834168d81f69efe2199a7f55b040edc30af110c82d811c134dc29dceb6a365"}],"schema_version":"module-discovery-index/v1","source_id":"local.canary"}`,
			id:        indexID,
			wantID:    "079229493701158339888c0a83bbde39b8c98f32630ce8a0efb76679eaf85974",
		},
		{
			name:      "snapshot",
			canonical: snapshotCanonical,
			want:      `{"entries":[{"artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifact_size_bytes":4096,"module":{"id":"vendor.tool","version":"build-10"},"package_path":"vendor.tool/build-10.zip","signature_id":"a9834168d81f69efe2199a7f55b040edc30af110c82d811c134dc29dceb6a365"}],"index_id":"079229493701158339888c0a83bbde39b8c98f32630ce8a0efb76679eaf85974","schema_version":"module-discovery-snapshot/v1","source_id":"local.canary","source_policy_id":"cdc4f798011b970a185413106503dade0066389c14329f6dcca73a6588f6bb77"}`,
			id:        snapshotID,
			wantID:    "7decc5e466bec57a3bb88caf7a95a89f15004fb4b693a6af03c50ac6d467a509",
		},
		{
			name:      "candidate",
			canonical: candidateCanonical,
			want:      `{"change":"INSTALL","review_key":"7114d26b026373fcb792e4214caf26cc0fcbcefc2dfe1ab0690aaa17255bc2f9","schema_version":"module-upgrade-candidate/v1","snapshot_id":"7decc5e466bec57a3bb88caf7a95a89f15004fb4b693a6af03c50ac6d467a509","source_id":"local.canary","source_policy_id":"cdc4f798011b970a185413106503dade0066389c14329f6dcca73a6588f6bb77","target":{"artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifact_size_bytes":4096,"module":{"id":"vendor.tool","version":"build-10"},"package_path":"vendor.tool/build-10.zip","signature_id":"a9834168d81f69efe2199a7f55b040edc30af110c82d811c134dc29dceb6a365"}}`,
			id:        candidateID,
			wantID:    "e79b9ce210d4882850c69c4d4fae6fd64f0e331d8adb521b2215df829e44b17e",
		},
		{
			name:      "decision",
			canonical: decisionCanonical,
			want:      `{"candidate_id":"e79b9ce210d4882850c69c4d4fae6fd64f0e331d8adb521b2215df829e44b17e","decision":"APPROVE","operator_principal_id":"operator.canary","reason":"verified exact candidate","schema_version":"module-candidate-decision/v1"}`,
			id:        decisionID,
			wantID:    "a359e10b1762bb682a96a8ed0fe7ae6c13c4d366f38110a2a4880cf639386d26",
		},
	}
	for _, canary := range canaries {
		t.Run(canary.name, func(t *testing.T) {
			if string(canary.canonical) != canary.want {
				t.Fatalf("canonical = %s\nwant = %s", canary.canonical, canary.want)
			}
			if canary.id != canary.wantID {
				t.Fatalf("ID = %s, want %s", canary.id, canary.wantID)
			}
		})
	}
}
