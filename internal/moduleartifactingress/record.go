// Package moduleartifactingress defines the authority-free provenance record
// and the filesystem-first ingress workflow for server-owned module artifacts.
package moduleartifactingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	RecordSchemaVersionV1     = "module-artifact-ingress-record/v1"
	RecordIDDigestDomainV1    = "freeagent.module-artifact-ingress-record/v1"
	MaxRecordCanonicalBytesV1 = 64 << 10
	maxJSONSafeIntegerV1      = uint64(1<<53 - 1)
)

// RecordV1 is immutable, global artifact-supply provenance. It deliberately
// contains no host path, URL, tenant, signature, installation, activation,
// grant, binding, review, or execution authority.
type RecordV1 struct {
	SchemaVersion               string        `json:"schema_version"`
	SourceID                    string        `json:"source_id"`
	SourcePolicyID              string        `json:"source_policy_id"`
	SourcePolicyRevision        uint64        `json:"source_policy_revision"`
	SnapshotID                  string        `json:"snapshot_id"`
	SnapshotObservationRevision uint64        `json:"snapshot_observation_revision"`
	EntryOrdinal                uint32        `json:"entry_ordinal"`
	PackagePath                 string        `json:"package_path"`
	Module                      moduleapi.Ref `json:"module"`
	ArtifactDigest              string        `json:"artifact_digest"`
	ArtifactSizeBytes           uint64        `json:"artifact_size_bytes"`
	ManifestRef                 string        `json:"manifest_ref"`
	CoveredFileCount            uint64        `json:"covered_file_count"`
}

// NewRecordV1 validates and freezes one ingress record, returning exact RFC
// 8785 canonical bytes and their domain-separated Admission ID.
func NewRecordV1(record RecordV1) (RecordV1, []byte, string, error) {
	if err := validateRecordV1(record); err != nil {
		return RecordV1{}, nil, "", err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return RecordV1{}, nil, "", fmt.Errorf("module artifact ingress: encode record: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(encoded, recordJSONLimitsV1())
	if err != nil {
		return RecordV1{}, nil, "", fmt.Errorf("module artifact ingress: canonicalize record: %w", err)
	}
	if len(canonical) == 0 || canonical[0] != '{' || len(canonical) > MaxRecordCanonicalBytesV1 {
		return RecordV1{}, nil, "", errors.New("module artifact ingress: record exceeds canonical bounds")
	}
	frozen := record
	return frozen, bytes.Clone(canonical), moduleapi.Digest(RecordIDDigestDomainV1, canonical), nil
}

// RestoreRecordV1 accepts only the exact canonical wire for expectedID. JSON
// with unknown or duplicate members, trailing data, or non-canonical spelling
// is rejected before any field can be used as provenance.
func RestoreRecordV1(canonical []byte, expectedID string) (RecordV1, error) {
	if !moduleapi.ValidSHA256(expectedID) {
		return RecordV1{}, errors.New("module artifact ingress: admission ID must be lowercase SHA-256")
	}
	if len(canonical) == 0 || len(canonical) > MaxRecordCanonicalBytesV1 {
		return RecordV1{}, errors.New("module artifact ingress: record canonical bytes are outside bounds")
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(canonical, recordJSONLimitsV1())
	if err != nil || !bytes.Equal(checked, canonical) || canonical[0] != '{' {
		return RecordV1{}, errors.New("module artifact ingress: record must be exact canonical JSON")
	}
	if moduleapi.Digest(RecordIDDigestDomainV1, canonical) != expectedID {
		return RecordV1{}, errors.New("module artifact ingress: admission ID does not match canonical record")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var record RecordV1
	if err := decoder.Decode(&record); err != nil {
		return RecordV1{}, fmt.Errorf("module artifact ingress: decode record: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RecordV1{}, errors.New("module artifact ingress: record has trailing JSON")
	}
	frozen, rebuilt, rebuiltID, err := NewRecordV1(record)
	if err != nil || rebuiltID != expectedID || !bytes.Equal(rebuilt, canonical) {
		return RecordV1{}, errors.New("module artifact ingress: record semantic closure failed")
	}
	return frozen, nil
}

func validateRecordV1(record RecordV1) error {
	if record.SchemaVersion != RecordSchemaVersionV1 {
		return fmt.Errorf("module artifact ingress: schema_version must be %q", RecordSchemaVersionV1)
	}
	if !validDottedIdentifierV1(record.SourceID) {
		return errors.New("module artifact ingress: source_id is invalid")
	}
	if !moduleapi.ValidSHA256(record.SourcePolicyID) ||
		!moduleapi.ValidSHA256(record.SnapshotID) ||
		!moduleapi.ValidSHA256(record.ArtifactDigest) ||
		!moduleapi.ValidSHA256(record.ManifestRef) {
		return errors.New("module artifact ingress: content identity is invalid")
	}
	if record.SourcePolicyRevision == 0 || record.SourcePolicyRevision > maxJSONSafeIntegerV1 ||
		record.SnapshotObservationRevision == 0 ||
		record.SnapshotObservationRevision > maxJSONSafeIntegerV1 {
		return errors.New("module artifact ingress: source revisions must be positive JSON-safe integers")
	}
	if err := record.Module.Validate(); err != nil {
		return fmt.Errorf("module artifact ingress: module identity: %w", err)
	}
	path, err := moduleapi.NormalizeArtifactPath(record.PackagePath)
	if err != nil || path != record.PackagePath || len(record.PackagePath) > moduleapi.MaxModulePackagePathBytesV1 {
		return errors.New("module artifact ingress: package_path must be exact canonical source-relative path")
	}
	if record.ArtifactSizeBytes == 0 || record.ArtifactSizeBytes > moduleapi.MaxModuleSourcePackageBytesV1 {
		return errors.New("module artifact ingress: artifact_size_bytes is outside v1 bounds")
	}
	if record.CoveredFileCount == 0 || record.CoveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) {
		return errors.New("module artifact ingress: covered_file_count is outside v1 bounds")
	}
	return nil
}

func validDottedIdentifierV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxIdentifierBytes ||
		strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	segmentStart := true
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '.' {
			if segmentStart {
				return false
			}
			segmentStart = true
			continue
		}
		if segmentStart {
			if character < 'a' || character > 'z' {
				return false
			}
			segmentStart = false
			continue
		}
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return !segmentStart
}

func recordJSONLimitsV1() moduleapi.CanonicalJSONLimits {
	return moduleapi.CanonicalJSONLimits{
		MaxBytes: MaxRecordCanonicalBytesV1,
		MaxDepth: 16,
		MaxNodes: 64,
	}
}
