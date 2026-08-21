package moduleapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ModuleSignatureSchemaVersionV1    = "module-signature/v1"
	ModulePublisherKeySchemaVersionV1 = "module-publisher-key/v1"
	ModuleSourcePolicySchemaVersionV1 = "module-source-policy/v1"

	ModuleSignatureAlgorithmEd25519V1 ModuleSignatureAlgorithmV1 = "ED25519"

	ModuleSourceKindLocalDirectoryV1 ModuleSourceKindV1 = "LOCAL_DIRECTORY"
	ModuleSourceKindHTTPSIndexV1     ModuleSourceKindV1 = "HTTPS_INDEX"

	ModuleSourceNetworkDenyV1       ModuleSourceNetworkV1 = "DENY"
	ModuleSourceNetworkExactHTTPSV1 ModuleSourceNetworkV1 = "EXACT_HTTPS_ONLY"

	MaxModuleSupplyWireBytesV1        = 64 << 10
	MaxModuleSourceOriginBytesV1      = 4096
	MaxModuleSourceIDPrefixesV1       = 64
	MaxModuleDiscoveryIndexBytesV1    = MaxTextBytes
	MaxModuleDiscoverySnapshotBytesV1 = MaxModuleDiscoveryIndexBytesV1 + MaxModuleSupplyWireBytesV1
	MaxModuleDiscoveryCandidatesV1    = 256
	MaxModulePackagePathBytesV1       = 4096
	MaxModuleSourcePackageBytesV1     = uint64(DefaultArtifactMaxTotalBytes)
	// MaxModuleDiscoveryAdvertisedPackageBytesV1 is the Core-owned ceiling
	// for the sum of package sizes advertised by one accepted snapshot.
	MaxModuleDiscoveryAdvertisedPackageBytesV1 uint64 = 1 << 30

	modulePublisherKeyIDDomainV1 = "freeagent.module-publisher-key-id/v1"
	moduleSignatureInputDomainV1 = "freeagent.module-signature/v1"
	moduleSignatureIDDomainV1    = "freeagent.module-signature-envelope/v1"
	moduleSourceOriginDomainV1   = "freeagent.module-source-origin/v1"
	moduleSourcePolicyIDDomainV1 = "freeagent.module-source-policy/v1"

	maxModuleSupplyDepthV1 = 32
)

// ModuleSignatureAlgorithmV1 is deliberately closed to one standard-library
// algorithm in v1. Adding another algorithm requires a new reviewed contract.
type ModuleSignatureAlgorithmV1 string

// ModulePublisherKeyV1 is authority-free public key material imported by an
// Operator. A package may name PublisherKeyID, but it cannot import, trust,
// revoke, or grant authority to this key.
type ModulePublisherKeyV1 struct {
	SchemaVersion   string                     `json:"schema_version"`
	Algorithm       ModuleSignatureAlgorithmV1 `json:"algorithm"`
	PublisherKeyID  string                     `json:"publisher_key_id"`
	PublicKeyBase64 string                     `json:"public_key_base64"`
}

// ModuleSignatureV1 is a detached signature envelope. The signature input is
// only the domain-separated exact ArtifactDigest. ArtifactDigest already
// covers the manifest identity/version and all ordinary payload files.
type ModuleSignatureV1 struct {
	SchemaVersion   string                     `json:"schema_version"`
	Algorithm       ModuleSignatureAlgorithmV1 `json:"algorithm"`
	PublisherKeyID  string                     `json:"publisher_key_id"`
	ArtifactDigest  string                     `json:"artifact_digest"`
	SignatureBase64 string                     `json:"signature_base64"`
}

type ModuleSourceKindV1 string

type ModuleSourceNetworkV1 string

// ModuleSourcePolicyV1 is a local Operator constraint, never a declaration by
// the module package. OriginDigest binds the provider-normalized exact local
// root or HTTPS origin without persisting its potentially sensitive text.
// The policy grants no install, activation, binding, execution, or effect.
type ModuleSourcePolicyV1 struct {
	SchemaVersion           string                `json:"schema_version"`
	SourceID                string                `json:"source_id"`
	Kind                    ModuleSourceKindV1    `json:"kind"`
	OriginDigest            string                `json:"origin_digest"`
	Network                 ModuleSourceNetworkV1 `json:"network"`
	SignatureRequired       bool                  `json:"signature_required"`
	PublisherKeyID          string                `json:"publisher_key_id,omitempty"`
	AllowedModuleIDPrefixes []string              `json:"allowed_module_id_prefixes"`
	MaxIndexBytes           uint64                `json:"max_index_bytes"`
	MaxPackageBytes         uint64                `json:"max_package_bytes"`
	MaxCandidates           uint32                `json:"max_candidates"`
}

func NewModulePublisherKeyV1(
	input ModulePublisherKeyV1,
) (ModulePublisherKeyV1, []byte, string, error) {
	if input.SchemaVersion != ModulePublisherKeySchemaVersionV1 {
		return ModulePublisherKeyV1{}, nil, "", fmt.Errorf(
			"moduleapi: publisher key schema_version must be %q",
			ModulePublisherKeySchemaVersionV1,
		)
	}
	if input.Algorithm != ModuleSignatureAlgorithmEd25519V1 {
		return ModulePublisherKeyV1{}, nil, "", fmt.Errorf(
			"moduleapi: unsupported publisher key algorithm %q",
			input.Algorithm,
		)
	}
	publicKey, err := decodeCanonicalBase64V1(
		"publisher public key",
		input.PublicKeyBase64,
		ed25519.PublicKeySize,
	)
	if err != nil {
		return ModulePublisherKeyV1{}, nil, "", err
	}
	keyID := Digest(
		modulePublisherKeyIDDomainV1,
		append([]byte(string(input.Algorithm)+"\x00"), publicKey...),
	)
	if input.PublisherKeyID != "" && input.PublisherKeyID != keyID {
		return ModulePublisherKeyV1{}, nil, "", fmt.Errorf(
			"moduleapi: publisher_key_id differs from public key material",
		)
	}
	frozen := input
	frozen.PublisherKeyID = keyID
	frozen.PublicKeyBase64 = base64.StdEncoding.EncodeToString(publicKey)
	canonical, err := marshalCanonicalModuleSupplyV1(frozen, MaxModuleSupplyWireBytesV1)
	if err != nil {
		return ModulePublisherKeyV1{}, nil, "", err
	}
	return frozen, canonical, keyID, nil
}

// ParseModulePublisherKeyV1 accepts one exact canonical publisher-key object
// without trusting an ID supplied out of band. The returned key, canonical
// bytes, and derived key ID are detached caller-owned values. Trust and
// revocation remain local Operator policy decisions.
func ParseModulePublisherKeyV1(
	canonical []byte,
) (ModulePublisherKeyV1, []byte, string, error) {
	var decoded ModulePublisherKeyV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleSupplyWireBytesV1,
		&decoded,
	); err != nil {
		return ModulePublisherKeyV1{}, nil, "", err
	}
	frozen, rebuilt, keyID, err := NewModulePublisherKeyV1(decoded)
	if err != nil {
		return ModulePublisherKeyV1{}, nil, "", err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ModulePublisherKeyV1{}, nil, "", fmt.Errorf(
			"moduleapi: publisher key is not an exact frozen canonical value",
		)
	}
	return frozen, bytes.Clone(rebuilt), keyID, nil
}

func RestoreModulePublisherKeyV1(
	canonical []byte,
	expectedKeyID string,
) (ModulePublisherKeyV1, error) {
	if !ValidSHA256(expectedKeyID) {
		return ModulePublisherKeyV1{}, fmt.Errorf(
			"moduleapi: expected publisher key ID must be SHA-256",
		)
	}
	restored, _, keyID, err := ParseModulePublisherKeyV1(canonical)
	if err != nil {
		return ModulePublisherKeyV1{}, err
	}
	if keyID != expectedKeyID {
		return ModulePublisherKeyV1{}, fmt.Errorf(
			"moduleapi: publisher key is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

func NewModuleSignatureV1(
	input ModuleSignatureV1,
) (ModuleSignatureV1, []byte, string, error) {
	if input.SchemaVersion != ModuleSignatureSchemaVersionV1 {
		return ModuleSignatureV1{}, nil, "", fmt.Errorf(
			"moduleapi: signature schema_version must be %q",
			ModuleSignatureSchemaVersionV1,
		)
	}
	if input.Algorithm != ModuleSignatureAlgorithmEd25519V1 {
		return ModuleSignatureV1{}, nil, "", fmt.Errorf(
			"moduleapi: unsupported signature algorithm %q",
			input.Algorithm,
		)
	}
	if !ValidSHA256(input.PublisherKeyID) || !ValidSHA256(input.ArtifactDigest) {
		return ModuleSignatureV1{}, nil, "", fmt.Errorf(
			"moduleapi: signature key and artifact identities must be SHA-256",
		)
	}
	signature, err := decodeCanonicalBase64V1(
		"module signature",
		input.SignatureBase64,
		ed25519.SignatureSize,
	)
	if err != nil {
		return ModuleSignatureV1{}, nil, "", err
	}
	frozen := input
	frozen.SignatureBase64 = base64.StdEncoding.EncodeToString(signature)
	canonical, err := marshalCanonicalModuleSupplyV1(frozen, MaxModuleSupplyWireBytesV1)
	if err != nil {
		return ModuleSignatureV1{}, nil, "", err
	}
	signatureID := Digest(moduleSignatureIDDomainV1, canonical)
	return frozen, canonical, signatureID, nil
}

// ParseModuleSignatureV1 accepts one exact canonical detached-signature
// envelope and derives its content identity. It validates representation only;
// callers must still apply Source Policy, revocation, and cryptographic checks.
func ParseModuleSignatureV1(
	canonical []byte,
) (ModuleSignatureV1, []byte, string, error) {
	var decoded ModuleSignatureV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleSupplyWireBytesV1,
		&decoded,
	); err != nil {
		return ModuleSignatureV1{}, nil, "", err
	}
	frozen, rebuilt, signatureID, err := NewModuleSignatureV1(decoded)
	if err != nil {
		return ModuleSignatureV1{}, nil, "", err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ModuleSignatureV1{}, nil, "", fmt.Errorf(
			"moduleapi: signature is not an exact frozen canonical value",
		)
	}
	return frozen, bytes.Clone(rebuilt), signatureID, nil
}

func RestoreModuleSignatureV1(
	canonical []byte,
	expectedSignatureID string,
) (ModuleSignatureV1, error) {
	if !ValidSHA256(expectedSignatureID) {
		return ModuleSignatureV1{}, fmt.Errorf(
			"moduleapi: expected signature ID must be SHA-256",
		)
	}
	restored, _, signatureID, err := ParseModuleSignatureV1(canonical)
	if err != nil {
		return ModuleSignatureV1{}, err
	}
	if signatureID != expectedSignatureID {
		return ModuleSignatureV1{}, fmt.Errorf(
			"moduleapi: signature is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

// ModuleSignatureInputV1 returns owned, domain-separated bytes suitable for
// ed25519.Sign. It contains no key identity, source, policy, or authority.
func ModuleSignatureInputV1(artifactDigest string) ([]byte, error) {
	if !ValidSHA256(artifactDigest) {
		return nil, fmt.Errorf(
			"moduleapi: artifact digest for signature must be SHA-256",
		)
	}
	return append(
		append([]byte(nil), []byte(moduleSignatureInputDomainV1)...),
		append([]byte{0}, []byte(artifactDigest)...)...,
	), nil
}

// VerifyModuleSignatureV1 verifies only cryptographic binding. Source policy,
// key revocation, admission, and authority remain Core/Operator decisions.
func VerifyModuleSignatureV1(
	signature ModuleSignatureV1,
	publisher ModulePublisherKeyV1,
) error {
	frozenSignature, _, _, err := NewModuleSignatureV1(signature)
	if err != nil {
		return err
	}
	frozenPublisher, _, _, err := NewModulePublisherKeyV1(publisher)
	if err != nil {
		return err
	}
	if frozenSignature.Algorithm != frozenPublisher.Algorithm ||
		frozenSignature.PublisherKeyID != frozenPublisher.PublisherKeyID {
		return fmt.Errorf("moduleapi: signature publisher key does not match")
	}
	publicKey, _ := base64.StdEncoding.DecodeString(frozenPublisher.PublicKeyBase64)
	signatureBytes, _ := base64.StdEncoding.DecodeString(frozenSignature.SignatureBase64)
	message, _ := ModuleSignatureInputV1(frozenSignature.ArtifactDigest)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signatureBytes) {
		return fmt.Errorf("moduleapi: Ed25519 signature verification failed")
	}
	return nil
}

// ModuleSourceOriginDigestV1 binds a provider-normalized exact local root or
// HTTPS origin. Normalization and safe opening are provider responsibilities;
// this pure helper performs no filesystem or network access.
func ModuleSourceOriginDigestV1(
	kind ModuleSourceKindV1,
	canonicalOrigin []byte,
) (string, error) {
	if err := validateModuleSourceKindV1(kind); err != nil {
		return "", err
	}
	origin := bytes.Clone(canonicalOrigin)
	if len(origin) == 0 || len(origin) > MaxModuleSourceOriginBytesV1 ||
		!utf8.Valid(origin) {
		return "", fmt.Errorf(
			"moduleapi: source origin must contain between 1 and %d UTF-8 bytes",
			MaxModuleSourceOriginBytesV1,
		)
	}
	text := string(origin)
	if text != strings.TrimSpace(text) || text != CanonicalText(text) {
		return "", fmt.Errorf(
			"moduleapi: source origin must be trimmed Unicode NFC",
		)
	}
	for _, character := range text {
		if character < 0x20 || character == 0x7f {
			return "", fmt.Errorf(
				"moduleapi: source origin contains a control character",
			)
		}
	}
	payload := append([]byte(string(kind)+"\x00"), origin...)
	return Digest(moduleSourceOriginDomainV1, payload), nil
}

func NewModuleSourcePolicyV1(
	input ModuleSourcePolicyV1,
) (ModuleSourcePolicyV1, []byte, string, error) {
	if input.SchemaVersion != ModuleSourcePolicySchemaVersionV1 {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: source policy schema_version must be %q",
			ModuleSourcePolicySchemaVersionV1,
		)
	}
	if !validDottedIdentifier(input.SourceID, MaxIdentifierBytes) {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: invalid source_id %q",
			input.SourceID,
		)
	}
	if err := validateModuleSourceKindV1(input.Kind); err != nil {
		return ModuleSourcePolicyV1{}, nil, "", err
	}
	if !ValidSHA256(input.OriginDigest) {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: source origin_digest must be SHA-256",
		)
	}
	switch input.Kind {
	case ModuleSourceKindLocalDirectoryV1:
		if input.Network != ModuleSourceNetworkDenyV1 {
			return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
				"moduleapi: local source network policy must be DENY",
			)
		}
	case ModuleSourceKindHTTPSIndexV1:
		if input.Network != ModuleSourceNetworkExactHTTPSV1 {
			return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
				"moduleapi: HTTPS source network policy must be EXACT_HTTPS_ONLY",
			)
		}
	}
	if input.SignatureRequired {
		if !ValidSHA256(input.PublisherKeyID) {
			return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
				"moduleapi: signed source policy requires a publisher key ID",
			)
		}
	} else if input.PublisherKeyID != "" {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: unsigned source policy must not name a publisher key",
		)
	}
	prefixes, err := normalizeModuleIDPrefixesV1(input.AllowedModuleIDPrefixes)
	if err != nil {
		return ModuleSourcePolicyV1{}, nil, "", err
	}
	if input.MaxIndexBytes == 0 ||
		input.MaxIndexBytes > uint64(MaxModuleDiscoveryIndexBytesV1) ||
		input.MaxPackageBytes == 0 ||
		input.MaxPackageBytes > MaxModuleSourcePackageBytesV1 ||
		input.MaxCandidates == 0 ||
		input.MaxCandidates > MaxModuleDiscoveryCandidatesV1 {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: source policy resource limits are outside v1 ceilings",
		)
	}
	frozen := input
	frozen.AllowedModuleIDPrefixes = prefixes
	canonical, err := marshalCanonicalModuleSupplyV1(frozen, MaxModuleSupplyWireBytesV1)
	if err != nil {
		return ModuleSourcePolicyV1{}, nil, "", err
	}
	policyID := Digest(moduleSourcePolicyIDDomainV1, canonical)
	return frozen, canonical, policyID, nil
}

// ParseModuleSourcePolicyV1 accepts one exact canonical local Operator policy
// and derives its content identity. The policy remains authority-free until a
// Core-owned verifier applies it to one exact candidate observation.
func ParseModuleSourcePolicyV1(
	canonical []byte,
) (ModuleSourcePolicyV1, []byte, string, error) {
	var decoded ModuleSourcePolicyV1
	if err := decodeExactModuleSupplyV1(
		canonical,
		MaxModuleSupplyWireBytesV1,
		&decoded,
	); err != nil {
		return ModuleSourcePolicyV1{}, nil, "", err
	}
	frozen, rebuilt, policyID, err := NewModuleSourcePolicyV1(decoded)
	if err != nil {
		return ModuleSourcePolicyV1{}, nil, "", err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ModuleSourcePolicyV1{}, nil, "", fmt.Errorf(
			"moduleapi: source policy is not an exact frozen canonical value",
		)
	}
	return frozen, bytes.Clone(rebuilt), policyID, nil
}

// ValidateModuleSourceCandidateV1 applies the policy fields shared by direct
// local preflight and Discovery Snapshot construction. It is a pure deny-only
// check: success grants no trust, installation, activation, Binding,
// ExecutionClass, authority, Secret, effect, or runtime access.
func ValidateModuleSourceCandidateV1(
	policy ModuleSourcePolicyV1,
	module Ref,
	artifactSizeBytes uint64,
	hasSignature bool,
) error {
	frozen, _, _, err := NewModuleSourcePolicyV1(policy)
	if err != nil {
		return err
	}
	if err := module.Validate(); err != nil {
		return fmt.Errorf("moduleapi: source candidate module: %w", err)
	}
	if artifactSizeBytes == 0 || artifactSizeBytes > frozen.MaxPackageBytes {
		return fmt.Errorf("moduleapi: source candidate exceeds package limit")
	}
	if !moduleIDAllowedByPrefixesV1(
		module.ID,
		frozen.AllowedModuleIDPrefixes,
	) {
		return fmt.Errorf(
			"moduleapi: source candidate is outside allowed module ID prefixes",
		)
	}
	if frozen.SignatureRequired && !hasSignature {
		return fmt.Errorf("moduleapi: source candidate lacks required signature")
	}
	return nil
}

func RestoreModuleSourcePolicyV1(
	canonical []byte,
	expectedPolicyID string,
) (ModuleSourcePolicyV1, error) {
	if !ValidSHA256(expectedPolicyID) {
		return ModuleSourcePolicyV1{}, fmt.Errorf(
			"moduleapi: expected source policy ID must be SHA-256",
		)
	}
	restored, _, policyID, err := ParseModuleSourcePolicyV1(canonical)
	if err != nil {
		return ModuleSourcePolicyV1{}, err
	}
	if policyID != expectedPolicyID {
		return ModuleSourcePolicyV1{}, fmt.Errorf(
			"moduleapi: source policy is not the expected frozen canonical value",
		)
	}
	return restored, nil
}

func validateModuleSourceKindV1(kind ModuleSourceKindV1) error {
	switch kind {
	case ModuleSourceKindLocalDirectoryV1, ModuleSourceKindHTTPSIndexV1:
		return nil
	default:
		return fmt.Errorf("moduleapi: unsupported source kind %q", kind)
	}
}

func normalizeModuleIDPrefixesV1(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > MaxModuleSourceIDPrefixesV1 {
		return nil, fmt.Errorf(
			"moduleapi: allowed module ID prefixes must contain between 1 and %d values",
			MaxModuleSourceIDPrefixesV1,
		)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if !validDottedIdentifier(value, MaxIdentifierBytes) {
			return nil, fmt.Errorf(
				"moduleapi: allowed module ID prefix %d is invalid",
				index,
			)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf(
				"moduleapi: allowed module ID prefixes must be unique",
			)
		}
	}
	return values, nil
}

func decodeCanonicalBase64V1(name, value string, exactBytes int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != exactBytes ||
		base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, fmt.Errorf(
			"moduleapi: %s must be canonical base64 for exactly %d bytes",
			name,
			exactBytes,
		)
	}
	return decoded, nil
}

func marshalCanonicalModuleSupplyV1(value any, maximum int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("moduleapi: marshal module supply v1: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: maxModuleSupplyDepthV1,
			MaxNodes: maximum,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("moduleapi: canonicalize module supply v1: %w", err)
	}
	return bytes.Clone(canonical), nil
}

func decodeExactModuleSupplyV1(canonical []byte, maximum int, target any) error {
	input := bytes.Clone(canonical)
	if len(input) == 0 || len(input) > maximum {
		return fmt.Errorf(
			"moduleapi: module supply v1 wire must contain between 1 and %d bytes",
			maximum,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: maxModuleSupplyDepthV1,
			MaxNodes: maximum,
		},
	)
	if err != nil || !bytes.Equal(checked, input) || input[0] != '{' {
		return fmt.Errorf("moduleapi: module supply v1 wire is not an exact canonical JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("moduleapi: decode module supply v1: %w", err)
	}
	return nil
}
