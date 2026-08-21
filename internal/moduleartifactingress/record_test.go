package moduleartifactingress

import (
	"bytes"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRecordV1RoundTripAndExactIdentity(t *testing.T) {
	input := validRecordV1()
	record, canonical, admissionID, err := NewRecordV1(input)
	if err != nil {
		t.Fatalf("NewRecordV1() error = %v", err)
	}
	if record.SchemaVersion != RecordSchemaVersionV1 ||
		!moduleapi.ValidSHA256(admissionID) || len(canonical) > MaxRecordCanonicalBytesV1 {
		t.Fatalf("unexpected record result: %#v id=%q bytes=%d", record, admissionID, len(canonical))
	}
	restored, err := RestoreRecordV1(canonical, admissionID)
	if err != nil {
		t.Fatalf("RestoreRecordV1() error = %v", err)
	}
	if restored != record {
		t.Fatalf("restored = %#v, want %#v", restored, record)
	}
	canonical[0] = 'x'
	if restored.SourceID != input.SourceID {
		t.Fatal("returned record was coupled to canonical bytes")
	}
}

func TestRestoreRecordV1RejectsNonCanonicalUnknownDuplicateAndWrongID(t *testing.T) {
	_, canonical, admissionID, err := NewRecordV1(validRecordV1())
	if err != nil {
		t.Fatal(err)
	}
	unknownEncoded := bytes.Replace(canonical, []byte(`}`), []byte(`,"unknown":0}`), 1)
	unknownCanonical, canonicalErr := moduleapi.CanonicalJSON(unknownEncoded)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	unknownID := moduleapi.Digest(RecordIDDigestDomainV1, unknownCanonical)
	if _, err := RestoreRecordV1(unknownCanonical, unknownID); err == nil {
		t.Fatal("canonical record with unknown member unexpectedly accepted")
	}
	tests := [][]byte{
		append([]byte(" "), canonical...),
		bytes.Replace(canonical, []byte(`{"artifact_digest":`), []byte(`{"artifact_digest":"`+strings.Repeat("a", 64)+`","artifact_digest":`), 1),
		append(bytes.Clone(canonical), []byte("\n")...),
	}
	for index, candidate := range tests {
		if _, err := RestoreRecordV1(candidate, admissionID); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
	if _, err := RestoreRecordV1(canonical, strings.Repeat("f", 64)); err == nil {
		t.Fatal("wrong admission ID unexpectedly accepted")
	}
}

func TestNewRecordV1RejectsInvalidFields(t *testing.T) {
	tests := []func(*RecordV1){
		func(v *RecordV1) { v.SchemaVersion = "wrong/v1" },
		func(v *RecordV1) { v.SourceID = "Bad" },
		func(v *RecordV1) { v.SourcePolicyRevision = 0 },
		func(v *RecordV1) { v.SourcePolicyRevision = 1 << 53 },
		func(v *RecordV1) { v.SnapshotObservationRevision = 0 },
		func(v *RecordV1) { v.SnapshotObservationRevision = 1 << 53 },
		func(v *RecordV1) { v.PackagePath = "../escape" },
		func(v *RecordV1) { v.PackagePath = `mods\\escape` },
		func(v *RecordV1) { v.Module.ID = "Bad" },
		func(v *RecordV1) { v.ArtifactDigest = strings.Repeat("A", 64) },
		func(v *RecordV1) { v.ArtifactSizeBytes = 0 },
		func(v *RecordV1) { v.ManifestRef = "bad" },
		func(v *RecordV1) { v.CoveredFileCount = 0 },
	}
	for index, mutate := range tests {
		value := validRecordV1()
		mutate(&value)
		if _, _, _, err := NewRecordV1(value); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}

func validRecordV1() RecordV1 {
	return RecordV1{
		SchemaVersion:               RecordSchemaVersionV1,
		SourceID:                    "local.modules",
		SourcePolicyID:              strings.Repeat("1", 64),
		SourcePolicyRevision:        3,
		SnapshotID:                  strings.Repeat("2", 64),
		SnapshotObservationRevision: 4,
		EntryOrdinal:                2,
		PackagePath:                 "packages/text-stats",
		Module: moduleapi.Ref{
			ID:      "text.stats",
			Version: "1.2.3",
		},
		ArtifactDigest:    strings.Repeat("3", 64),
		ArtifactSizeBytes: 1234,
		ManifestRef:       strings.Repeat("4", 64),
		CoveredFileCount:  7,
	}
}
