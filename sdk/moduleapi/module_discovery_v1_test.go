package moduleapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestModuleDiscoveryIndexV1UsesOpaqueVersionAndRejectsConflicts(t *testing.T) {
	entries := []ModuleDiscoveryEntryV1{
		testModuleDiscoveryEntryV1("vendor.tool", "build-2", 'b'),
		testModuleDiscoveryEntryV1("vendor.tool", "build-10", 'a'),
	}
	index, canonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries:       entries,
		},
	)
	if err != nil {
		t.Fatalf("NewModuleDiscoveryIndexV1() error = %v", err)
	}
	if got := index.Entries[0].Module.Version; got != "build-10" {
		t.Fatalf("first exact opaque version = %q, want build-10", got)
	}
	entries[0].Module.ID = "mutated"
	if index.Entries[1].Module.ID != "vendor.tool" {
		t.Fatal("frozen discovery index aliases caller entries")
	}
	restored, err := RestoreModuleDiscoveryIndexV1(canonical, indexID)
	if err != nil {
		t.Fatalf("RestoreModuleDiscoveryIndexV1() error = %v", err)
	}
	restored.Entries[0].PackagePath = "changed"
	if bytes.Contains(canonical, []byte("changed")) {
		t.Fatal("restored discovery index aliases canonical bytes")
	}

	conflict := []ModuleDiscoveryEntryV1{
		testModuleDiscoveryEntryV1("vendor.tool", "build-2", 'a'),
		testModuleDiscoveryEntryV1("vendor.tool", "build-2", 'b'),
	}
	if _, _, _, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries:       conflict,
		},
	); err == nil {
		t.Fatal("same module ID/version with different artifact digest accepted")
	}
	oversizedPath := testModuleDiscoveryEntryV1("vendor.tool", "build-3", 'c')
	oversizedPath.PackagePath = strings.Repeat("a/", MaxModulePackagePathBytesV1/2) + "x"
	if _, _, _, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries:       []ModuleDiscoveryEntryV1{oversizedPath},
		},
	); err == nil {
		t.Fatal("oversized package path accepted")
	}
}

func TestParseModuleDiscoveryIndexV1RequiresExactCanonicalAndReturnsOwnedValues(t *testing.T) {
	wantIndex, canonical, wantIndexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries: []ModuleDiscoveryEntryV1{
				testModuleDiscoveryEntryV1("vendor.second", "build-2", 'b'),
				testModuleDiscoveryEntryV1("vendor.first", "build-1", 'a'),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := bytes.Clone(canonical)
	input := bytes.Clone(canonical)

	gotIndex, gotCanonical, gotIndexID, err := ParseModuleDiscoveryIndexV1(input)
	if err != nil {
		t.Fatalf("ParseModuleDiscoveryIndexV1() error = %v", err)
	}
	wantDerivedIndexID := Digest(moduleDiscoveryIndexIDDomainV1, wantCanonical)
	if !reflect.DeepEqual(gotIndex, wantIndex) || gotIndexID != wantIndexID ||
		gotIndexID != wantDerivedIndexID || !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatalf(
			"parsed index = %+v canonical=%s index_id=%s\nwant index = %+v canonical=%s index_id=%s",
			gotIndex,
			gotCanonical,
			gotIndexID,
			wantIndex,
			wantCanonical,
			wantDerivedIndexID,
		)
	}

	input[0] = '['
	if !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatal("parsed discovery index canonical aliases caller input")
	}
	gotCanonical[0] = '['
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatal("parsed discovery index canonical aliases constructor output")
	}
	gotIndex.Entries[0].PackagePath = "changed"
	if wantIndex.Entries[0].PackagePath == "changed" ||
		bytes.Contains(canonical, []byte("changed")) {
		t.Fatal("parsed discovery index aliases another frozen value")
	}

	if _, _, _, err := ParseModuleDiscoveryIndexV1(
		append(bytes.Clone(wantCanonical), '\n'),
	); err == nil {
		t.Fatal("discovery index parser accepted non-exact canonical bytes")
	}
	if _, _, _, err := ParseModuleDiscoveryIndexV1(
		moduleSupplyAddUnknownField(t, wantCanonical),
	); err == nil {
		t.Fatal("discovery index parser accepted an unknown field")
	}
	if _, _, _, err := ParseModuleDiscoveryIndexV1(
		bytes.Repeat([]byte{'x'}, MaxModuleDiscoveryIndexBytesV1+1),
	); err == nil {
		t.Fatal("discovery index parser accepted an oversized wire")
	}
}

func TestParseModuleDiscoveryIndexV1RejectsCanonicalButUnfrozenEntryOrder(t *testing.T) {
	first := testModuleDiscoveryEntryV1("vendor.alpha", "v1", 'a')
	second := testModuleDiscoveryEntryV1("vendor.zeta", "v1", 'b')
	encoded, err := json.Marshal(ModuleDiscoveryIndexV1{
		SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      "local.modules",
		Entries:       []ModuleDiscoveryEntryV1{second, first},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ParseModuleDiscoveryIndexV1(canonical); err == nil {
		t.Fatal("canonical discovery index with unfrozen entry order accepted")
	}
}

func TestRestoreModuleDiscoveryIndexV1RejectsWrongExpectedID(t *testing.T) {
	_, canonical, _, err := NewModuleDiscoveryIndexV1(ModuleDiscoveryIndexV1{
		SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      "local.modules",
		Entries:       []ModuleDiscoveryEntryV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreModuleDiscoveryIndexV1(canonical, strings.Repeat("f", 64)); err == nil {
		t.Fatal("discovery index restored with a wrong expected ID")
	}
}

func TestModuleDiscoverySnapshotV1ProvesExactPolicyAndIndex(t *testing.T) {
	policy, policyCanonical, policyID := testModuleSourcePolicyV1(t, true)
	entry := testModuleDiscoveryEntryV1("vendor.tool", "release-2", 'd')
	_, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       []ModuleDiscoveryEntryV1{entry},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, canonical, snapshotID, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		t.Fatalf("NewModuleDiscoverySnapshotV1() error = %v", err)
	}
	if snapshot.SourcePolicyID != policyID || snapshot.IndexID != indexID ||
		len(snapshot.Entries) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	restored, err := RestoreModuleDiscoverySnapshotV1(
		canonical,
		snapshotID,
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		t.Fatalf("RestoreModuleDiscoverySnapshotV1() error = %v", err)
	}
	restored.Entries[0].Module.ID = "changed"
	if bytes.Contains(canonical, []byte("changed")) {
		t.Fatal("restored discovery snapshot aliases canonical bytes")
	}

	unsigned := entry
	unsigned.SignatureID = ""
	_, unsignedIndexCanonical, unsignedIndexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       []ModuleDiscoveryEntryV1{unsigned},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		unsignedIndexCanonical,
		unsignedIndexID,
	); err == nil {
		t.Fatal("unsigned entry accepted by signature-required policy")
	}

	outside := testModuleDiscoveryEntryV1("other.tool", "v1", 'e')
	_, outsideIndexCanonical, outsideIndexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       []ModuleDiscoveryEntryV1{outside},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		outsideIndexCanonical,
		outsideIndexID,
	); err == nil {
		t.Fatal("entry outside allowed module ID prefixes accepted")
	}
}

func TestModuleDiscoverySnapshotV1EnforcesAggregateAdvertisedPackageBytes(t *testing.T) {
	if MaxModuleDiscoveryAdvertisedPackageBytesV1 != 1<<30 {
		t.Fatalf(
			"aggregate advertised package ceiling = %d, want %d",
			MaxModuleDiscoveryAdvertisedPackageBytesV1,
			uint64(1<<30),
		)
	}
	policy, _, _ := testModuleSourcePolicyV1(t, true)
	policy.MaxPackageBytes = MaxModuleSourcePackageBytesV1
	policy.MaxCandidates = 5
	_, policyCanonical, policyID, err := NewModuleSourcePolicyV1(policy)
	if err != nil {
		t.Fatal(err)
	}

	quarterCeiling := MaxModuleDiscoveryAdvertisedPackageBytesV1 / 4
	boundaryEntries := []ModuleDiscoveryEntryV1{
		testModuleDiscoveryEntryV1("vendor.first", "v1", 'a'),
		testModuleDiscoveryEntryV1("vendor.second", "v1", 'b'),
		testModuleDiscoveryEntryV1("vendor.third", "v1", 'c'),
		testModuleDiscoveryEntryV1("vendor.fourth", "v1", 'd'),
	}
	for index := range boundaryEntries {
		boundaryEntries[index].ArtifactSizeBytes = quarterCeiling
	}
	_, boundaryCanonical, boundaryID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       boundaryEntries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		boundaryCanonical,
		boundaryID,
	); err != nil {
		t.Fatalf("exact aggregate advertised package ceiling rejected: %v", err)
	}

	overEntries := append([]ModuleDiscoveryEntryV1(nil), boundaryEntries...)
	over := testModuleDiscoveryEntryV1("vendor.fifth", "v1", 'e')
	over.ArtifactSizeBytes = 1
	overEntries = append(overEntries, over)
	overIndex, overCanonical, overID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       overEntries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		overCanonical,
		overID,
	); err == nil || !strings.Contains(err.Error(), "aggregate advertised package bytes") {
		t.Fatalf("over-ceiling aggregate error = %v", err)
	}

	overSnapshotCanonical, err := marshalCanonicalModuleSupplyV1(
		ModuleDiscoverySnapshotV1{
			SchemaVersion:  ModuleDiscoverySnapshotSchemaVersionV1,
			SourceID:       policy.SourceID,
			SourcePolicyID: policyID,
			IndexID:        overID,
			Entries:        cloneModuleDiscoveryEntriesV1(overIndex.Entries),
		},
		MaxModuleDiscoverySnapshotBytesV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	overSnapshotID := Digest(
		moduleDiscoverySnapshotIDDomainV1,
		overSnapshotCanonical,
	)
	if _, err := RestoreModuleDiscoverySnapshotV1(
		overSnapshotCanonical,
		overSnapshotID,
		policyCanonical,
		policyID,
		overCanonical,
		overID,
	); err == nil || !strings.Contains(err.Error(), "aggregate advertised package bytes") {
		t.Fatalf("over-ceiling aggregate restore error = %v", err)
	}
}

func TestModuleDiscoveryAdvertisedPackageBytesCheckIsOverflowSafe(t *testing.T) {
	if moduleDiscoveryAdvertisedPackageBytesExceedV1(
		MaxModuleDiscoveryAdvertisedPackageBytesV1-1,
		1,
	) {
		t.Fatal("exact aggregate advertised package ceiling reported as exceeded")
	}
	if !moduleDiscoveryAdvertisedPackageBytesExceedV1(
		MaxModuleDiscoveryAdvertisedPackageBytesV1,
		1,
	) {
		t.Fatal("one byte above aggregate advertised package ceiling accepted")
	}
	if !moduleDiscoveryAdvertisedPackageBytesExceedV1(1, ^uint64(0)) {
		t.Fatal("overflow-like aggregate advertised package size accepted")
	}
}

func TestModuleDiscoverySnapshotV1AllowsAnExplicitEmptyObservation(t *testing.T) {
	policy, policyCanonical, policyID := testModuleSourcePolicyV1(t, true)
	index, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
			Entries:       nil,
		},
	)
	if err != nil {
		t.Fatalf("NewModuleDiscoveryIndexV1(empty) error = %v", err)
	}
	if index.Entries == nil || string(indexCanonical) !=
		`{"entries":[],"schema_version":"module-discovery-index/v1","source_id":"local.modules"}` {
		t.Fatalf("empty index = %+v canonical=%s", index, indexCanonical)
	}
	snapshot, snapshotCanonical, snapshotID, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		t.Fatalf("NewModuleDiscoverySnapshotV1(empty) error = %v", err)
	}
	if snapshot.Entries == nil || len(snapshot.Entries) != 0 {
		t.Fatalf("empty snapshot entries = %#v", snapshot.Entries)
	}
	if _, err := RestoreModuleDiscoverySnapshotV1(
		snapshotCanonical,
		snapshotID,
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	); err != nil {
		t.Fatalf("RestoreModuleDiscoverySnapshotV1(empty) error = %v", err)
	}
}

func TestModuleUpgradeCandidateV1BindsExactChangeWithoutVersionOrdering(t *testing.T) {
	_, policyCanonical, policyID := testModuleSourcePolicyV1(t, true)
	target := testModuleDiscoveryEntryV1("vendor.tool", "opaque-next", 'f')
	_, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries:       []ModuleDiscoveryEntryV1{target},
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
	current := &ExactModuleArtifactV1{
		Module:         Ref{ID: "vendor.tool", Version: "opaque-current"},
		ArtifactDigest: strings.Repeat("1", SHA256HexLength),
	}
	candidate, canonical, candidateID, err := NewModuleUpgradeCandidateV1(
		ModuleUpgradeCandidateV1{
			SchemaVersion:  ModuleUpgradeCandidateSchemaVersionV1,
			SourceID:       "local.modules",
			SourcePolicyID: policyID,
			SnapshotID:     snapshotID,
			Change:         ModuleCandidateChangeExactVersionV1,
			Current:        current,
			Target:         target,
		},
		snapshotCanonical,
		snapshotID,
	)
	if err != nil {
		t.Fatalf("NewModuleUpgradeCandidateV1() error = %v", err)
	}
	current.Module.ID = "mutated"
	if candidate.Current == nil || candidate.Current.Module.ID != "vendor.tool" {
		t.Fatal("frozen upgrade candidate aliases current input")
	}
	if !ValidSHA256(candidate.ReviewKey) {
		t.Fatalf("candidate review key = %q", candidate.ReviewKey)
	}
	if _, err := RestoreModuleUpgradeCandidateV1(
		canonical,
		candidateID,
		snapshotCanonical,
		snapshotID,
	); err != nil {
		t.Fatalf("RestoreModuleUpgradeCandidateV1() error = %v", err)
	}

	conflict := candidate
	conflict.Current = &ExactModuleArtifactV1{
		Module:         target.Module,
		ArtifactDigest: strings.Repeat("2", SHA256HexLength),
	}
	if _, _, _, err := NewModuleUpgradeCandidateV1(
		conflict,
		snapshotCanonical,
		snapshotID,
	); err == nil {
		t.Fatal("same exact module version with changed artifact accepted")
	}
	noCurrent := candidate
	noCurrent.Current = nil
	if _, _, _, err := NewModuleUpgradeCandidateV1(
		noCurrent,
		snapshotCanonical,
		snapshotID,
	); err == nil {
		t.Fatal("exact version change without current artifact accepted")
	}
	wrongReviewKey := candidate
	wrongReviewKey.ReviewKey = strings.Repeat("3", SHA256HexLength)
	if _, _, _, err := NewModuleUpgradeCandidateV1(
		wrongReviewKey,
		snapshotCanonical,
		snapshotID,
	); err == nil {
		t.Fatal("candidate with substituted review key accepted")
	}
	absentTarget := candidate
	absentTarget.ReviewKey = ""
	absentTarget.Target = testModuleDiscoveryEntryV1("vendor.tool", "not-observed", '6')
	if _, _, _, err := NewModuleUpgradeCandidateV1(
		absentTarget,
		snapshotCanonical,
		snapshotID,
	); err == nil {
		t.Fatal("candidate target absent from exact snapshot accepted")
	}
	extra := testModuleDiscoveryEntryV1("vendor.extra", "v1", '5')
	_, secondIndexCanonical, secondIndexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "local.modules",
			Entries:       []ModuleDiscoveryEntryV1{extra, target},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, secondSnapshotCanonical, secondSnapshotID, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		secondIndexCanonical,
		secondIndexID,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate, _, _, err := NewModuleUpgradeCandidateV1(
		ModuleUpgradeCandidateV1{
			SchemaVersion: ModuleUpgradeCandidateSchemaVersionV1,
			Change:        ModuleCandidateChangeInstallV1,
			Target:        target,
		},
		secondSnapshotCanonical,
		secondSnapshotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondSnapshotID == snapshotID || secondCandidate.ReviewKey != candidate.ReviewKey {
		t.Fatalf(
			"review key did not remain stable across snapshots: first=%s second=%s",
			candidate.ReviewKey,
			secondCandidate.ReviewKey,
		)
	}
}

func TestModuleCandidateDecisionV1IsExactTerminalReviewData(t *testing.T) {
	input := ModuleCandidateDecisionV1{
		SchemaVersion:       ModuleCandidateDecisionSchemaVersionV1,
		CandidateID:         strings.Repeat("9", SHA256HexLength),
		Decision:            ModuleCandidateDecisionRejectV1,
		OperatorPrincipalID: "operator.local",
		Reason:              "source evidence is incomplete",
	}
	decision, canonical, decisionID, err := NewModuleCandidateDecisionV1(input)
	if err != nil {
		t.Fatalf("NewModuleCandidateDecisionV1() error = %v", err)
	}
	if decision != input {
		t.Fatalf("decision = %+v\nwant = %+v", decision, input)
	}
	if _, err := RestoreModuleCandidateDecisionV1(canonical, decisionID); err != nil {
		t.Fatalf("RestoreModuleCandidateDecisionV1() error = %v", err)
	}

	padded := input
	padded.Reason = " reason "
	if _, _, _, err := NewModuleCandidateDecisionV1(padded); err == nil {
		t.Fatal("padded decision reason accepted")
	}
	unknown := input
	unknown.Decision = "RETRY"
	if _, _, _, err := NewModuleCandidateDecisionV1(unknown); err == nil {
		t.Fatal("unknown candidate decision accepted")
	}
	invalidUTF8 := input
	invalidUTF8.Reason = string([]byte{0xff})
	if _, _, _, err := NewModuleCandidateDecisionV1(invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 decision reason accepted")
	}
	control := input
	control.Reason = "line one\nline two"
	if _, _, _, err := NewModuleCandidateDecisionV1(control); err == nil {
		t.Fatal("decision reason with control character accepted")
	}
}

func TestModuleDiscoveryV1RejectsUnknownFieldsInEveryWire(t *testing.T) {
	policy, policyCanonical, policyID := testModuleSourcePolicyV1(t, true)
	entry := testModuleDiscoveryEntryV1("vendor.tool", "v2", '7')
	_, indexCanonical, indexID, err := NewModuleDiscoveryIndexV1(
		ModuleDiscoveryIndexV1{
			SchemaVersion: ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      policy.SourceID,
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
			SchemaVersion:  ModuleUpgradeCandidateSchemaVersionV1,
			SourceID:       policy.SourceID,
			SourcePolicyID: policyID,
			SnapshotID:     snapshotID,
			Change:         ModuleCandidateChangeInstallV1,
			Target:         entry,
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
			OperatorPrincipalID: "operator.local",
			Reason:              "verified exact candidate",
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
			name:      "index",
			canonical: indexCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleDiscoveryIndexV1(value, indexID)
				return err
			},
		},
		{
			name:      "snapshot",
			canonical: snapshotCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleDiscoverySnapshotV1(
					value,
					snapshotID,
					policyCanonical,
					policyID,
					indexCanonical,
					indexID,
				)
				return err
			},
		},
		{
			name:      "candidate",
			canonical: candidateCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleUpgradeCandidateV1(
					value,
					candidateID,
					snapshotCanonical,
					snapshotID,
				)
				return err
			},
		},
		{
			name:      "decision",
			canonical: decisionCanonical,
			restore: func(value []byte) error {
				_, err := RestoreModuleCandidateDecisionV1(value, decisionID)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.restore(moduleSupplyAddUnknownField(t, test.canonical)); err == nil {
				t.Fatal("unknown field accepted")
			}
		})
	}

	var nested map[string]any
	if err := json.Unmarshal(indexCanonical, &nested); err != nil {
		t.Fatal(err)
	}
	nested["entries"].([]any)[0].(map[string]any)["authority"] = "forbidden"
	encoded, err := json.Marshal(nested)
	if err != nil {
		t.Fatal(err)
	}
	canonicalNested, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreModuleDiscoveryIndexV1(canonicalNested, indexID); err == nil {
		t.Fatal("nested unknown authority field accepted")
	}
}

func testModuleSourcePolicyV1(
	t *testing.T,
	signatureRequired bool,
) (ModuleSourcePolicyV1, []byte, string) {
	t.Helper()
	_, _, publisher, _, _ := testModulePublisherKeyV1(t, 31)
	originDigest, err := ModuleSourceOriginDigestV1(
		ModuleSourceKindLocalDirectoryV1,
		[]byte(`local-root/module-source`),
	)
	if err != nil {
		t.Fatal(err)
	}
	publisherKeyID := ""
	if signatureRequired {
		publisherKeyID = publisher.PublisherKeyID
	}
	policy, canonical, policyID, err := NewModuleSourcePolicyV1(
		ModuleSourcePolicyV1{
			SchemaVersion:           ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.modules",
			Kind:                    ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 ModuleSourceNetworkDenyV1,
			SignatureRequired:       signatureRequired,
			PublisherKeyID:          publisherKeyID,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           64 << 10,
			MaxPackageBytes:         8 << 20,
			MaxCandidates:           16,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return policy, canonical, policyID
}

func testModuleDiscoveryEntryV1(
	moduleID string,
	version string,
	digestByte byte,
) ModuleDiscoveryEntryV1 {
	return ModuleDiscoveryEntryV1{
		Module: Ref{
			ID:      moduleID,
			Version: version,
		},
		ArtifactDigest:    strings.Repeat(string(digestByte), SHA256HexLength),
		ArtifactSizeBytes: 4096,
		PackagePath:       moduleID + "/" + version + ".zip",
		SignatureID:       strings.Repeat("8", SHA256HexLength),
	}
}
