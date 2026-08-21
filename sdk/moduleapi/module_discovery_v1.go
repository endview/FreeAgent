package moduleapi

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ModuleDiscoveryIndexSchemaVersionV1    = "module-discovery-index/v1"
	ModuleDiscoverySnapshotSchemaVersionV1 = "module-discovery-snapshot/v1"
	ModuleUpgradeCandidateSchemaVersionV1  = "module-upgrade-candidate/v1"
	ModuleCandidateDecisionSchemaVersionV1 = "module-candidate-decision/v1"

	ModuleCandidateChangeInstallV1      ModuleCandidateChangeV1 = "INSTALL"
	ModuleCandidateChangeExactVersionV1 ModuleCandidateChangeV1 = "EXACT_VERSION_CHANGE"

	ModuleCandidateDecisionApproveV1 ModuleCandidateDecisionValueV1 = "APPROVE"
	ModuleCandidateDecisionRejectV1  ModuleCandidateDecisionValueV1 = "REJECT"

	MaxModuleCandidateReasonBytesV1 = 4096

	moduleDiscoveryIndexIDDomainV1    = "freeagent.module-discovery-index/v1"
	moduleDiscoverySnapshotIDDomainV1 = "freeagent.module-discovery-snapshot/v1"
	moduleUpgradeCandidateIDDomainV1  = "freeagent.module-upgrade-candidate/v1"
	moduleCandidateReviewKeyDomainV1  = "freeagent.module-candidate-review-key/v1"
	moduleCandidateDecisionIDDomainV1 = "freeagent.module-candidate-decision/v1"
)

// ModuleDiscoveryEntryV1 is authority-free source data. PackagePath is a
// canonical source-relative path; it is never interpreted as a host path or
// URL by this contract. Version is an exact opaque version, not SemVer.
type ModuleDiscoveryEntryV1 struct {
	Module            Ref    `json:"module"`
	ArtifactDigest    string `json:"artifact_digest"`
	ArtifactSizeBytes uint64 `json:"artifact_size_bytes"`
	PackagePath       string `json:"package_path"`
	SignatureID       string `json:"signature_id,omitempty"`
}

// ModuleDiscoveryIndexV1 is one canonical source-published candidate list.
// Its content digest is the source revision used by the first discovery
// implementation. Parsing it performs no I/O and grants no authority.
type ModuleDiscoveryIndexV1 struct {
	SchemaVersion string                   `json:"schema_version"`
	SourceID      string                   `json:"source_id"`
	Entries       []ModuleDiscoveryEntryV1 `json:"entries"`
}

// ModuleDiscoverySnapshotV1 freezes one exact local policy and one exact
// observed index. Entries are copied into the snapshot so later source drift
// cannot alter the observation.
type ModuleDiscoverySnapshotV1 struct {
	SchemaVersion  string                   `json:"schema_version"`
	SourceID       string                   `json:"source_id"`
	SourcePolicyID string                   `json:"source_policy_id"`
	IndexID        string                   `json:"index_id"`
	Entries        []ModuleDiscoveryEntryV1 `json:"entries"`
}

// ExactModuleArtifactV1 identifies current installed facts without implying
// an ordering relationship between version strings.
type ExactModuleArtifactV1 struct {
	Module         Ref    `json:"module"`
	ArtifactDigest string `json:"artifact_digest"`
}

type ModuleCandidateChangeV1 string

// ModuleUpgradeCandidateV1 is an immutable proposal to move from Current (or
// no current artifact) to one exact observed target. It does not install,
// activate, bind, grant, or execute anything.
type ModuleUpgradeCandidateV1 struct {
	SchemaVersion  string                  `json:"schema_version"`
	SourceID       string                  `json:"source_id"`
	SourcePolicyID string                  `json:"source_policy_id"`
	SnapshotID     string                  `json:"snapshot_id"`
	ReviewKey      string                  `json:"review_key"`
	Change         ModuleCandidateChangeV1 `json:"change"`
	Current        *ExactModuleArtifactV1  `json:"current,omitempty"`
	Target         ModuleDiscoveryEntryV1  `json:"target"`
}

type ModuleCandidateDecisionValueV1 string

// ModuleCandidateDecisionV1 records an Operator's decision about one exact
// candidate. OperatorPrincipalID is attribution only; authorization is proven
// by the owning Store workflow. A REJECT workflow must resolve the Candidate's
// ReviewKey and suppress that exact source/module/artifact tuple across later
// snapshots; CandidateID alone is only this observation's immutable identity.
type ModuleCandidateDecisionV1 struct {
	SchemaVersion       string                         `json:"schema_version"`
	CandidateID         string                         `json:"candidate_id"`
	Decision            ModuleCandidateDecisionValueV1 `json:"decision"`
	OperatorPrincipalID string                         `json:"operator_principal_id"`
	Reason              string                         `json:"reason"`
}

func NewModuleDiscoveryIndexV1(
	input ModuleDiscoveryIndexV1,
) (ModuleDiscoveryIndexV1, []byte, string, error) {
	if input.SchemaVersion != ModuleDiscoveryIndexSchemaVersionV1 {
		return ModuleDiscoveryIndexV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery index schema_version must be %q",
			ModuleDiscoveryIndexSchemaVersionV1,
		)
	}
	if !validDottedIdentifier(input.SourceID, MaxIdentifierBytes) {
		return ModuleDiscoveryIndexV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery index source_id is invalid",
		)
	}
	entries, err := normalizeModuleDiscoveryEntriesV1(input.Entries)
	if err != nil {
		return ModuleDiscoveryIndexV1{}, nil, "", err
	}
	frozen := input
	frozen.Entries = entries
	canonical, err := marshalCanonicalModuleSupplyV1(
		frozen,
		MaxModuleDiscoveryIndexBytesV1,
	)
	if err != nil {
		return ModuleDiscoveryIndexV1{}, nil, "", err
	}
	indexID := Digest(moduleDiscoveryIndexIDDomainV1, canonical)
	return frozen, canonical, indexID, nil
}

// ParseModuleDiscoveryIndexV1 accepts one exact canonical source-published
// index and derives its content identity. Parsing performs no source I/O and
// returns detached caller-owned values.
func ParseModuleDiscoveryIndexV1(
	canonical []byte,
) (ModuleDiscoveryIndexV1, []byte, string, error) {
	var decoded ModuleDiscoveryIndexV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleDiscoveryIndexBytesV1,
		&decoded,
	); err != nil {
		return ModuleDiscoveryIndexV1{}, nil, "", err
	}
	frozen, rebuilt, indexID, err := NewModuleDiscoveryIndexV1(decoded)
	if err != nil {
		return ModuleDiscoveryIndexV1{}, nil, "", err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ModuleDiscoveryIndexV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery index is not an exact frozen canonical value",
		)
	}
	return frozen, bytes.Clone(rebuilt), indexID, nil
}

func RestoreModuleDiscoveryIndexV1(
	canonical []byte,
	expectedIndexID string,
) (ModuleDiscoveryIndexV1, error) {
	if !ValidSHA256(expectedIndexID) {
		return ModuleDiscoveryIndexV1{}, fmt.Errorf(
			"moduleapi: expected discovery index ID must be SHA-256",
		)
	}
	restored, _, indexID, err := ParseModuleDiscoveryIndexV1(canonical)
	if err != nil {
		return ModuleDiscoveryIndexV1{}, err
	}
	if indexID != expectedIndexID {
		return ModuleDiscoveryIndexV1{}, fmt.Errorf(
			"moduleapi: discovery index is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

// NewModuleDiscoverySnapshotV1 proves and freezes the exact policy/index
// parents. It deliberately performs no source refresh itself.
func NewModuleDiscoverySnapshotV1(
	policyCanonical []byte,
	policyID string,
	indexCanonical []byte,
	indexID string,
) (ModuleDiscoverySnapshotV1, []byte, string, error) {
	policy, err := RestoreModuleSourcePolicyV1(policyCanonical, policyID)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery snapshot policy: %w",
			err,
		)
	}
	index, err := RestoreModuleDiscoveryIndexV1(indexCanonical, indexID)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery snapshot index: %w",
			err,
		)
	}
	if policy.SourceID != index.SourceID {
		return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery snapshot source differs between policy and index",
		)
	}
	if uint64(len(indexCanonical)) > policy.MaxIndexBytes ||
		uint32(len(index.Entries)) > policy.MaxCandidates {
		return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
			"moduleapi: discovery index exceeds source policy limits",
		)
	}
	var advertisedPackageBytes uint64
	for indexPosition, entry := range index.Entries {
		if moduleDiscoveryAdvertisedPackageBytesExceedV1(
			advertisedPackageBytes,
			entry.ArtifactSizeBytes,
		) {
			return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
				"moduleapi: discovery entries exceed %d aggregate advertised package bytes",
				MaxModuleDiscoveryAdvertisedPackageBytesV1,
			)
		}
		advertisedPackageBytes += entry.ArtifactSizeBytes
		if err := ValidateModuleSourceCandidateV1(
			policy,
			entry.Module,
			entry.ArtifactSizeBytes,
			entry.SignatureID != "",
		); err != nil {
			return ModuleDiscoverySnapshotV1{}, nil, "", fmt.Errorf(
				"moduleapi: discovery entry %d violates source policy: %w",
				indexPosition,
				err,
			)
		}
	}
	frozen := ModuleDiscoverySnapshotV1{
		SchemaVersion:  ModuleDiscoverySnapshotSchemaVersionV1,
		SourceID:       policy.SourceID,
		SourcePolicyID: policyID,
		IndexID:        indexID,
		Entries:        cloneModuleDiscoveryEntriesV1(index.Entries),
	}
	canonical, err := marshalCanonicalModuleSupplyV1(
		frozen,
		MaxModuleDiscoverySnapshotBytesV1,
	)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, nil, "", err
	}
	snapshotID := Digest(moduleDiscoverySnapshotIDDomainV1, canonical)
	return frozen, canonical, snapshotID, nil
}

func RestoreModuleDiscoverySnapshotV1(
	canonical []byte,
	expectedSnapshotID string,
	policyCanonical []byte,
	policyID string,
	indexCanonical []byte,
	indexID string,
) (ModuleDiscoverySnapshotV1, error) {
	if !ValidSHA256(expectedSnapshotID) {
		return ModuleDiscoverySnapshotV1{}, fmt.Errorf(
			"moduleapi: expected discovery snapshot ID must be SHA-256",
		)
	}
	var decoded ModuleDiscoverySnapshotV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleDiscoverySnapshotBytesV1,
		&decoded,
	); err != nil {
		return ModuleDiscoverySnapshotV1{}, err
	}
	restored, rebuilt, snapshotID, err := NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, err
	}
	if decoded.SchemaVersion != ModuleDiscoverySnapshotSchemaVersionV1 ||
		snapshotID != expectedSnapshotID ||
		!bytes.Equal(rebuilt, canonical) {
		return ModuleDiscoverySnapshotV1{}, fmt.Errorf(
			"moduleapi: discovery snapshot is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

func NewModuleUpgradeCandidateV1(
	input ModuleUpgradeCandidateV1,
	snapshotCanonical []byte,
	snapshotID string,
) (ModuleUpgradeCandidateV1, []byte, string, error) {
	if input.SchemaVersion != ModuleUpgradeCandidateSchemaVersionV1 {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate schema_version must be %q",
			ModuleUpgradeCandidateSchemaVersionV1,
		)
	}
	snapshot, err := parseFrozenModuleDiscoverySnapshotV1(
		snapshotCanonical,
		snapshotID,
	)
	if err != nil {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate snapshot: %w",
			err,
		)
	}
	if input.SourceID != "" && input.SourceID != snapshot.SourceID ||
		input.SourcePolicyID != "" && input.SourcePolicyID != snapshot.SourcePolicyID ||
		input.SnapshotID != "" && input.SnapshotID != snapshotID {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate source identities differ from its snapshot",
		)
	}
	target, err := normalizeModuleDiscoveryEntryV1(input.Target)
	if err != nil {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate target: %w",
			err,
		)
	}
	frozen := input
	frozen.SourceID = snapshot.SourceID
	frozen.SourcePolicyID = snapshot.SourcePolicyID
	frozen.SnapshotID = snapshotID
	frozen.Target = target
	if !moduleDiscoverySnapshotContainsEntryV1(snapshot, target) {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate target is absent from its exact snapshot",
		)
	}
	reviewKey, err := moduleCandidateReviewKeyV1(snapshot.SourceID, target)
	if err != nil {
		return ModuleUpgradeCandidateV1{}, nil, "", err
	}
	if input.ReviewKey != "" && input.ReviewKey != reviewKey {
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: upgrade candidate review_key differs from exact source/module/artifact tuple",
		)
	}
	frozen.ReviewKey = reviewKey
	switch input.Change {
	case ModuleCandidateChangeInstallV1:
		if input.Current != nil {
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: INSTALL candidate must not have current artifact",
			)
		}
	case ModuleCandidateChangeExactVersionV1:
		if input.Current == nil {
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: EXACT_VERSION_CHANGE candidate requires current artifact",
			)
		}
		current := *input.Current
		if err := current.Module.Validate(); err != nil ||
			!ValidSHA256(current.ArtifactDigest) {
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: upgrade candidate current artifact is invalid",
			)
		}
		if current.Module.ID != target.Module.ID {
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: upgrade candidate cannot change module ID",
			)
		}
		if current.Module.Version == target.Module.Version {
			if current.ArtifactDigest != target.ArtifactDigest {
				return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
					"moduleapi: same module ID and version has conflicting artifact digest",
				)
			}
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: upgrade candidate is an exact no-op",
			)
		}
		if current.ArtifactDigest == target.ArtifactDigest {
			return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
				"moduleapi: different exact versions cannot share one artifact digest",
			)
		}
		currentCopy := current
		frozen.Current = &currentCopy
	default:
		return ModuleUpgradeCandidateV1{}, nil, "", fmt.Errorf(
			"moduleapi: unsupported upgrade candidate change %q",
			input.Change,
		)
	}
	canonical, err := marshalCanonicalModuleSupplyV1(
		frozen,
		MaxModuleSupplyWireBytesV1,
	)
	if err != nil {
		return ModuleUpgradeCandidateV1{}, nil, "", err
	}
	candidateID := Digest(moduleUpgradeCandidateIDDomainV1, canonical)
	return frozen, canonical, candidateID, nil
}

func RestoreModuleUpgradeCandidateV1(
	canonical []byte,
	expectedCandidateID string,
	snapshotCanonical []byte,
	snapshotID string,
) (ModuleUpgradeCandidateV1, error) {
	if !ValidSHA256(expectedCandidateID) {
		return ModuleUpgradeCandidateV1{}, fmt.Errorf(
			"moduleapi: expected upgrade candidate ID must be SHA-256",
		)
	}
	var decoded ModuleUpgradeCandidateV1
	if err := decodeExactModuleSupplyV1(canonical, MaxModuleSupplyWireBytesV1, &decoded); err != nil {
		return ModuleUpgradeCandidateV1{}, err
	}
	restored, rebuilt, candidateID, err := NewModuleUpgradeCandidateV1(
		decoded,
		snapshotCanonical,
		snapshotID,
	)
	if err != nil {
		return ModuleUpgradeCandidateV1{}, err
	}
	if candidateID != expectedCandidateID || !bytes.Equal(rebuilt, canonical) {
		return ModuleUpgradeCandidateV1{}, fmt.Errorf(
			"moduleapi: upgrade candidate is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

// parseFrozenModuleDiscoverySnapshotV1 validates the snapshot as one exact
// content-addressed parent without reopening its policy/index ancestors. The
// owning persistence workflow remains responsible for proving that ancestor
// closure before admitting the snapshot ID.
func parseFrozenModuleDiscoverySnapshotV1(
	canonical []byte,
	expectedSnapshotID string,
) (ModuleDiscoverySnapshotV1, error) {
	if !ValidSHA256(expectedSnapshotID) {
		return ModuleDiscoverySnapshotV1{}, fmt.Errorf(
			"expected discovery snapshot ID must be SHA-256",
		)
	}
	var decoded ModuleDiscoverySnapshotV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleDiscoverySnapshotBytesV1,
		&decoded,
	); err != nil {
		return ModuleDiscoverySnapshotV1{}, err
	}
	if decoded.SchemaVersion != ModuleDiscoverySnapshotSchemaVersionV1 ||
		!validDottedIdentifier(decoded.SourceID, MaxIdentifierBytes) ||
		!ValidSHA256(decoded.SourcePolicyID) ||
		!ValidSHA256(decoded.IndexID) {
		return ModuleDiscoverySnapshotV1{}, fmt.Errorf(
			"discovery snapshot identities are invalid",
		)
	}
	entries, err := normalizeModuleDiscoveryEntriesV1(decoded.Entries)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, err
	}
	frozen := decoded
	frozen.Entries = entries
	rebuilt, err := marshalCanonicalModuleSupplyV1(
		frozen,
		MaxModuleDiscoverySnapshotBytesV1,
	)
	if err != nil {
		return ModuleDiscoverySnapshotV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) ||
		Digest(moduleDiscoverySnapshotIDDomainV1, canonical) != expectedSnapshotID {
		return ModuleDiscoverySnapshotV1{}, fmt.Errorf(
			"discovery snapshot is not the expected frozen canonical value",
		)
	}
	return frozen, nil
}

func moduleDiscoverySnapshotContainsEntryV1(
	snapshot ModuleDiscoverySnapshotV1,
	target ModuleDiscoveryEntryV1,
) bool {
	for _, entry := range snapshot.Entries {
		if entry == target {
			return true
		}
	}
	return false
}

type moduleCandidateReviewKeyWireV1 struct {
	SourceID       string `json:"source_id"`
	Module         Ref    `json:"module"`
	ArtifactDigest string `json:"artifact_digest"`
}

func moduleCandidateReviewKeyV1(
	sourceID string,
	target ModuleDiscoveryEntryV1,
) (string, error) {
	canonical, err := marshalCanonicalModuleSupplyV1(
		moduleCandidateReviewKeyWireV1{
			SourceID:       sourceID,
			Module:         target.Module,
			ArtifactDigest: target.ArtifactDigest,
		},
		MaxModuleSupplyWireBytesV1,
	)
	if err != nil {
		return "", fmt.Errorf(
			"moduleapi: build candidate review key: %w",
			err,
		)
	}
	return Digest(moduleCandidateReviewKeyDomainV1, canonical), nil
}

func NewModuleCandidateDecisionV1(
	input ModuleCandidateDecisionV1,
) (ModuleCandidateDecisionV1, []byte, string, error) {
	if input.SchemaVersion != ModuleCandidateDecisionSchemaVersionV1 {
		return ModuleCandidateDecisionV1{}, nil, "", fmt.Errorf(
			"moduleapi: candidate decision schema_version must be %q",
			ModuleCandidateDecisionSchemaVersionV1,
		)
	}
	if !ValidSHA256(input.CandidateID) {
		return ModuleCandidateDecisionV1{}, nil, "", fmt.Errorf(
			"moduleapi: candidate decision candidate_id must be SHA-256",
		)
	}
	switch input.Decision {
	case ModuleCandidateDecisionApproveV1, ModuleCandidateDecisionRejectV1:
	default:
		return ModuleCandidateDecisionV1{}, nil, "", fmt.Errorf(
			"moduleapi: unsupported candidate decision %q",
			input.Decision,
		)
	}
	if err := validateOpaqueID(
		"candidate decision operator_principal_id",
		input.OperatorPrincipalID,
	); err != nil {
		return ModuleCandidateDecisionV1{}, nil, "", err
	}
	if !utf8.ValidString(input.Reason) || input.Reason == "" ||
		input.Reason != strings.TrimSpace(input.Reason) ||
		input.Reason != CanonicalText(input.Reason) ||
		len(input.Reason) > MaxModuleCandidateReasonBytesV1 {
		return ModuleCandidateDecisionV1{}, nil, "", fmt.Errorf(
			"moduleapi: candidate decision reason must be non-empty, trimmed Unicode NFC, and at most %d bytes",
			MaxModuleCandidateReasonBytesV1,
		)
	}
	for _, character := range input.Reason {
		if character < 0x20 || character == 0x7f {
			return ModuleCandidateDecisionV1{}, nil, "", fmt.Errorf(
				"moduleapi: candidate decision reason contains a control character",
			)
		}
	}
	frozen := input
	canonical, err := marshalCanonicalModuleSupplyV1(
		frozen,
		MaxModuleSupplyWireBytesV1,
	)
	if err != nil {
		return ModuleCandidateDecisionV1{}, nil, "", err
	}
	decisionID := Digest(moduleCandidateDecisionIDDomainV1, canonical)
	return frozen, canonical, decisionID, nil
}

func RestoreModuleCandidateDecisionV1(
	canonical []byte,
	expectedDecisionID string,
) (ModuleCandidateDecisionV1, error) {
	if !ValidSHA256(expectedDecisionID) {
		return ModuleCandidateDecisionV1{}, fmt.Errorf(
			"moduleapi: expected candidate decision ID must be SHA-256",
		)
	}
	var decoded ModuleCandidateDecisionV1
	if err := decodeExactModuleSupplyV1(canonical, MaxModuleSupplyWireBytesV1, &decoded); err != nil {
		return ModuleCandidateDecisionV1{}, err
	}
	restored, rebuilt, decisionID, err := NewModuleCandidateDecisionV1(decoded)
	if err != nil {
		return ModuleCandidateDecisionV1{}, err
	}
	if decisionID != expectedDecisionID || !bytes.Equal(rebuilt, canonical) {
		return ModuleCandidateDecisionV1{}, fmt.Errorf(
			"moduleapi: candidate decision is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

func normalizeModuleDiscoveryEntriesV1(
	input []ModuleDiscoveryEntryV1,
) ([]ModuleDiscoveryEntryV1, error) {
	if len(input) > MaxModuleDiscoveryCandidatesV1 {
		return nil, fmt.Errorf(
			"moduleapi: discovery entries must contain at most %d values",
			MaxModuleDiscoveryCandidatesV1,
		)
	}
	entries := make([]ModuleDiscoveryEntryV1, len(input))
	copy(entries, input)
	for index := range entries {
		normalized, err := normalizeModuleDiscoveryEntryV1(entries[index])
		if err != nil {
			return nil, fmt.Errorf(
				"moduleapi: discovery entry %d: %w",
				index,
				err,
			)
		}
		entries[index] = normalized
	}
	sort.Slice(entries, func(left, right int) bool {
		return moduleDiscoveryEntryLessV1(entries[left], entries[right])
	})
	for index := 1; index < len(entries); index++ {
		previous := entries[index-1]
		current := entries[index]
		if previous.Module == current.Module {
			if previous.ArtifactDigest != current.ArtifactDigest {
				return nil, fmt.Errorf(
					"moduleapi: same module ID and version has conflicting artifact digests",
				)
			}
			return nil, fmt.Errorf(
				"moduleapi: discovery entries contain a duplicate exact module version",
			)
		}
	}
	return entries, nil
}

func normalizeModuleDiscoveryEntryV1(
	input ModuleDiscoveryEntryV1,
) (ModuleDiscoveryEntryV1, error) {
	if err := input.Module.Validate(); err != nil {
		return ModuleDiscoveryEntryV1{}, err
	}
	if !ValidSHA256(input.ArtifactDigest) || input.ArtifactSizeBytes == 0 ||
		input.ArtifactSizeBytes > MaxModuleSourcePackageBytesV1 {
		return ModuleDiscoveryEntryV1{}, fmt.Errorf(
			"artifact digest or size is outside v1 bounds",
		)
	}
	path, err := NormalizeArtifactPath(input.PackagePath)
	if err != nil || path != input.PackagePath ||
		len(input.PackagePath) > MaxModulePackagePathBytesV1 {
		return ModuleDiscoveryEntryV1{}, fmt.Errorf(
			"package_path must be an exact canonical source-relative path of at most %d bytes",
			MaxModulePackagePathBytesV1,
		)
	}
	if input.SignatureID != "" && !ValidSHA256(input.SignatureID) {
		return ModuleDiscoveryEntryV1{}, fmt.Errorf(
			"signature_id must be empty or SHA-256",
		)
	}
	return input, nil
}

func moduleDiscoveryEntryLessV1(left, right ModuleDiscoveryEntryV1) bool {
	if left.Module.ID != right.Module.ID {
		return left.Module.ID < right.Module.ID
	}
	if left.Module.Version != right.Module.Version {
		return left.Module.Version < right.Module.Version
	}
	if left.ArtifactDigest != right.ArtifactDigest {
		return left.ArtifactDigest < right.ArtifactDigest
	}
	return left.PackagePath < right.PackagePath
}

func moduleIDAllowedByPrefixesV1(moduleID string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if moduleID == prefix || len(moduleID) > len(prefix) &&
			moduleID[:len(prefix)] == prefix && moduleID[len(prefix)] == '.' {
			return true
		}
	}
	return false
}

func cloneModuleDiscoveryEntriesV1(
	input []ModuleDiscoveryEntryV1,
) []ModuleDiscoveryEntryV1 {
	if input == nil {
		return nil
	}
	output := make([]ModuleDiscoveryEntryV1, len(input))
	copy(output, input)
	return output
}

func moduleDiscoveryAdvertisedPackageBytesExceedV1(
	total uint64,
	next uint64,
) bool {
	return total > MaxModuleDiscoveryAdvertisedPackageBytesV1 ||
		next > MaxModuleDiscoveryAdvertisedPackageBytesV1-total
}
