// Package currentbackup implements the explicit offline full-backup and
// restore path for the FAC1 Current Store. It is an operator tool package, not
// a Runtime, Store implementation, migration path, or startup hook.
package currentbackup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	FormatVersionV1 = "freeagent.current-store-backup/v1"

	manifestName       = "manifest.json"
	databaseName       = "database.sqlite"
	artifactsDirectory = "artifacts"

	manifestDigestDomain = "freeagent.backup-manifest/v1"
	maxManifestBytes     = 16 << 20
	maxCurrentCount      = 100000
	maxArtifactCount     = int(moduleartifactstore.MaxPhysicalArtifactsV1)
	maxArtifactFiles     = 1000000
	maxArtifactFileBytes = int64(64 << 20)

	// One artifact must remain admissible by the module artifact scanner and
	// Store. The larger aggregate limit is only the closure budget for all
	// artifact directories in one backup.
	maxArtifactBytes          = moduleapi.DefaultArtifactMaxTotalBytes
	maxArtifactAggregateBytes = int64(512 << 20)
	maxDatabaseBytes          = int64(64 << 30)

	// The preflight scanner runs before canonicalization or typed decoding and
	// never materializes a JSON value tree. The global ceiling explicitly fits
	// every declared Current and Artifact entry at its exact contract shape plus
	// the fixed manifest envelope. The smaller per-value budgets keep invalid or
	// unknown values from using that global allowance as an object-allocation
	// amplifier in the canonicalizer.
	manifestCurrentPublicationJSONTokens = 18
	manifestArtifactJSONTokens           = 8
	maxManifestPreflightEnvelopeTokens   = 256
	maxManifestPreflightTokens           = maxCurrentCount*manifestCurrentPublicationJSONTokens +
		maxArtifactCount*manifestArtifactJSONTokens +
		maxManifestPreflightEnvelopeTokens
	maxManifestPreflightDepth            = 128
	maxManifestPreflightTopLevelMembers  = 64
	maxManifestPreflightFixedValueTokens = 128
	maxManifestPreflightUnknownTokens    = 4096
	maxManifestPreflightCurrentTokens    = 64
	maxManifestPreflightArtifactTokens   = 32
)

var (
	ErrInvalidInput  = errors.New("currentbackup: invalid input")
	ErrSourceActive  = errors.New("currentbackup: source Store owner is active")
	ErrInvalidBundle = errors.New("currentbackup: invalid backup bundle")
	ErrTargetExists  = errors.New("currentbackup: restore or backup target exists")
	ErrIntegrity     = errors.New("currentbackup: backup integrity violation")
)

// DatabaseFile is the exact standalone SQLite snapshot in a bundle.
type DatabaseFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// StoreIdentity freezes the FAC1 identity and the physical Store instance.
type StoreIdentity struct {
	ApplicationID     int    `json:"application_id"`
	UserVersion       int    `json:"user_version"`
	SchemaIdentity    string `json:"schema_identity"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	GeneratorID       string `json:"generator_id"`
	StoreInstanceID   string `json:"store_instance_id"`
}

// CurrentPublication freezes one tenant's exact current Control/Catalog
// pointer. Entries are sorted by TenantID in canonical manifests.
type CurrentPublication struct {
	TenantID          string `json:"tenant_id"`
	PointerRevision   uint64 `json:"pointer_revision"`
	ControlID         string `json:"control_id"`
	ControlRevision   uint64 `json:"control_revision"`
	ControlDigest     string `json:"control_digest"`
	CatalogID         string `json:"catalog_id"`
	CatalogGeneration uint64 `json:"catalog_generation"`
	CatalogDigest     string `json:"catalog_digest"`
}

// Artifact freezes one content-addressed installed module directory.
type Artifact struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

// AttemptCounts records the exact Channel receipt/cursor population together
// with the distinct unsettled Model, Action, and Channel send states that must
// retain their normal restart/reconciliation handling after restore. Backup
// never mutates them or folds one kind into another.
type AttemptCounts struct {
	ModelPending           int64 `json:"model_pending"`
	ModelUnknown           int64 `json:"model_unknown"`
	ActionPending          int64 `json:"action_pending"`
	ActionUnknown          int64 `json:"action_unknown"`
	ChannelIngressReceipts int64 `json:"channel_ingress_receipts"`
	ChannelCursorScopes    int64 `json:"channel_cursor_scopes"`
	ChannelSendPending     int64 `json:"channel_send_pending"`
	ChannelSendUnknown     int64 `json:"channel_send_unknown"`
}

// Manifest is the sole canonical bundle index. ManifestDigest is computed
// after omitting itself from the canonical identity.
type Manifest struct {
	FormatVersion  string               `json:"format_version"`
	CreatedAt      string               `json:"created_at"`
	ToolVersion    string               `json:"tool_version"`
	Database       DatabaseFile         `json:"database"`
	StoreIdentity  StoreIdentity        `json:"store_identity"`
	Current        []CurrentPublication `json:"current"`
	Artifacts      []Artifact           `json:"artifacts"`
	ArtifactCount  int                  `json:"artifact_count"`
	AttemptCounts  AttemptCounts        `json:"attempt_counts"`
	ManifestDigest string               `json:"manifest_digest,omitempty"`
}

func freezeManifest(input Manifest) (Manifest, []byte, error) {
	if input.FormatVersion != FormatVersionV1 {
		return Manifest{}, nil, fmt.Errorf(
			"%w: format_version must be %q",
			ErrInvalidBundle,
			FormatVersionV1,
		)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, input.CreatedAt)
	if err != nil || createdAt.Location() != time.UTC ||
		createdAt.Format(time.RFC3339Nano) != input.CreatedAt {
		return Manifest{}, nil, fmt.Errorf(
			"%w: created_at must be exact UTC RFC3339Nano",
			ErrInvalidBundle,
		)
	}
	if err := validateOpaque("tool version", input.ToolVersion); err != nil {
		return Manifest{}, nil, err
	}
	if input.Database.Path != databaseName ||
		!moduleapi.ValidSHA256(input.Database.SHA256) ||
		input.Database.SizeBytes <= 0 ||
		input.Database.SizeBytes > maxDatabaseBytes {
		return Manifest{}, nil, fmt.Errorf(
			"%w: database entry is invalid",
			ErrInvalidBundle,
		)
	}
	if err := validateStoreIdentity(input.StoreIdentity); err != nil {
		return Manifest{}, nil, err
	}
	current, err := canonicalCurrent(input.Current)
	if err != nil {
		return Manifest{}, nil, err
	}
	artifacts, err := canonicalArtifacts(input.Artifacts)
	if err != nil {
		return Manifest{}, nil, err
	}
	if input.ArtifactCount != len(artifacts) {
		return Manifest{}, nil, fmt.Errorf(
			"%w: artifact_count does not match artifacts",
			ErrInvalidBundle,
		)
	}
	if input.AttemptCounts.ModelPending < 0 ||
		input.AttemptCounts.ModelUnknown < 0 ||
		input.AttemptCounts.ActionPending < 0 ||
		input.AttemptCounts.ActionUnknown < 0 ||
		input.AttemptCounts.ChannelIngressReceipts < 0 ||
		input.AttemptCounts.ChannelCursorScopes < 0 ||
		input.AttemptCounts.ChannelSendPending < 0 ||
		input.AttemptCounts.ChannelSendUnknown < 0 {
		return Manifest{}, nil, fmt.Errorf(
			"%w: attempt counts must not be negative",
			ErrInvalidBundle,
		)
	}

	frozen := input
	frozen.Current = current
	frozen.Artifacts = artifacts
	frozen.ArtifactCount = len(artifacts)
	frozen.ManifestDigest = ""
	identityCanonical, err := canonicalManifestJSON(frozen)
	if err != nil {
		return Manifest{}, nil, err
	}
	frozen.ManifestDigest = moduleapi.Digest(
		manifestDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalManifestJSON(frozen)
	if err != nil {
		return Manifest{}, nil, err
	}
	return cloneManifest(frozen), bytes.Clone(canonical), nil
}

func restoreManifest(canonical []byte) (Manifest, error) {
	if len(canonical) == 0 || len(canonical) > maxManifestBytes {
		return Manifest{}, fmt.Errorf(
			"%w: manifest size must be between 1 and %d bytes",
			ErrInvalidBundle,
			maxManifestBytes,
		)
	}
	if err := preflightManifestJSON(canonical); err != nil {
		return Manifest{}, err
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maxManifestBytes,
			MaxDepth: maxManifestPreflightDepth,
			MaxNodes: maxManifestPreflightTokens,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) || canonical[0] != '{' {
		return Manifest{}, fmt.Errorf(
			"%w: manifest must be exact RFC 8785 canonical JSON",
			ErrInvalidBundle,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var decoded Manifest
	if err := decoder.Decode(&decoded); err != nil {
		return Manifest{}, fmt.Errorf(
			"%w: decode manifest: %v",
			ErrInvalidBundle,
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Manifest{}, fmt.Errorf(
			"%w: manifest has trailing JSON",
			ErrInvalidBundle,
		)
	}
	rebuilt, rebuiltCanonical, err := freezeManifest(decoded)
	if err != nil {
		return Manifest{}, err
	}
	if decoded.ManifestDigest != rebuilt.ManifestDigest ||
		!bytes.Equal(canonical, rebuiltCanonical) {
		return Manifest{}, fmt.Errorf(
			"%w: manifest digest or canonical order differs",
			ErrIntegrity,
		)
	}
	return rebuilt, nil
}

type manifestPreflightScanner struct {
	decoder *json.Decoder
	tokens  int
}

type manifestPreflightValueKind uint8

const (
	manifestPreflightUnknown manifestPreflightValueKind = iota
	manifestPreflightFixed
	manifestPreflightCurrent
	manifestPreflightArtifacts
)

// preflightManifestJSON rejects manifest-shaped resource amplification before
// CanonicalJSONWithLimits constructs maps/slices and before encoding/json
// allocates the typed Current and Artifacts slices. It intentionally does not
// decide unknown-field or canonical-order semantics: small structurally
// bounded inputs continue to the existing strict path.
func preflightManifestJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	scanner := manifestPreflightScanner{decoder: decoder}

	opening, err := scanner.nextToken()
	if err != nil {
		return invalidManifestPreflight(err)
	}
	if opening != json.Delim('{') {
		return invalidManifestPreflight(errors.New("top-level value is not an object"))
	}

	var seenKnown uint16
	members := 0
	for decoder.More() {
		members++
		if members > maxManifestPreflightTopLevelMembers {
			return invalidManifestPreflight(fmt.Errorf(
				"top-level member count exceeds %d",
				maxManifestPreflightTopLevelMembers,
			))
		}
		keyValue, readErr := scanner.nextToken()
		if readErr != nil {
			return invalidManifestPreflight(readErr)
		}
		key, ok := keyValue.(string)
		if !ok {
			return invalidManifestPreflight(errors.New("top-level object key is not a string"))
		}
		bit, kind := manifestPreflightTopLevelKey(key)
		if bit != 0 {
			if seenKnown&bit != 0 {
				return invalidManifestPreflight(fmt.Errorf(
					"duplicate top-level key %q",
					key,
				))
			}
			seenKnown |= bit
		}

		var valueErr error
		switch kind {
		case manifestPreflightCurrent:
			valueErr = scanner.scanCountedArray(
				2,
				maxCurrentCount,
				maxManifestPreflightCurrentTokens,
				"current",
			)
		case manifestPreflightArtifacts:
			valueErr = scanner.scanCountedArray(
				2,
				maxArtifactCount,
				maxManifestPreflightArtifactTokens,
				"artifacts",
			)
		case manifestPreflightFixed:
			valueErr = scanner.scanBoundedValue(
				2,
				maxManifestPreflightFixedValueTokens,
				"fixed top-level value",
			)
		default:
			valueErr = scanner.scanBoundedValue(
				2,
				maxManifestPreflightUnknownTokens,
				"unknown top-level value",
			)
		}
		if valueErr != nil {
			return invalidManifestPreflight(valueErr)
		}
	}
	closing, err := scanner.nextToken()
	if err != nil {
		return invalidManifestPreflight(err)
	}
	if closing != json.Delim('}') {
		return invalidManifestPreflight(errors.New("top-level object has invalid closing delimiter"))
	}
	if trailing, trailingErr := scanner.nextToken(); !errors.Is(trailingErr, io.EOF) {
		if trailingErr != nil {
			return invalidManifestPreflight(fmt.Errorf("trailing JSON: %w", trailingErr))
		}
		return invalidManifestPreflight(fmt.Errorf("trailing token %v", trailing))
	}
	return nil
}

func manifestPreflightTopLevelKey(
	key string,
) (uint16, manifestPreflightValueKind) {
	switch key {
	case "format_version":
		return 1 << 0, manifestPreflightFixed
	case "created_at":
		return 1 << 1, manifestPreflightFixed
	case "tool_version":
		return 1 << 2, manifestPreflightFixed
	case "database":
		return 1 << 3, manifestPreflightFixed
	case "store_identity":
		return 1 << 4, manifestPreflightFixed
	case "current":
		return 1 << 5, manifestPreflightCurrent
	case "artifacts":
		return 1 << 6, manifestPreflightArtifacts
	case "artifact_count":
		return 1 << 7, manifestPreflightFixed
	case "attempt_counts":
		return 1 << 8, manifestPreflightFixed
	case "manifest_digest":
		return 1 << 9, manifestPreflightFixed
	default:
		return 0, manifestPreflightUnknown
	}
}

func (scanner *manifestPreflightScanner) nextToken() (json.Token, error) {
	value, err := scanner.decoder.Token()
	if err != nil {
		return nil, err
	}
	scanner.tokens++
	if scanner.tokens > maxManifestPreflightTokens {
		return nil, fmt.Errorf(
			"JSON token count exceeds %d",
			maxManifestPreflightTokens,
		)
	}
	return value, nil
}

func (scanner *manifestPreflightScanner) scanCountedArray(
	depth int,
	limit int,
	entryTokenLimit int,
	name string,
) error {
	start := scanner.tokens
	opening, err := scanner.nextBoundedToken(
		start,
		maxManifestPreflightFixedValueTokens,
		name,
	)
	if err != nil {
		return err
	}
	if opening != json.Delim('[') {
		return scanner.scanValueAfterToken(
			opening,
			depth,
			start,
			maxManifestPreflightFixedValueTokens,
			name,
		)
	}
	if depth > maxManifestPreflightDepth {
		return fmt.Errorf(
			"JSON nesting exceeds %d levels",
			maxManifestPreflightDepth,
		)
	}
	count := 0
	for scanner.decoder.More() {
		count++
		if count > limit {
			return fmt.Errorf("%s exceeds %d entries", name, limit)
		}
		if err := scanner.scanBoundedValue(
			depth+1,
			entryTokenLimit,
			name+" entry",
		); err != nil {
			return err
		}
	}
	closing, err := scanner.nextToken()
	if err != nil {
		return err
	}
	if closing != json.Delim(']') {
		return fmt.Errorf("%s has invalid closing delimiter", name)
	}
	return nil
}

func (scanner *manifestPreflightScanner) scanBoundedValue(
	depth int,
	tokenLimit int,
	name string,
) error {
	start := scanner.tokens
	value, err := scanner.nextBoundedToken(start, tokenLimit, name)
	if err != nil {
		return err
	}
	return scanner.scanValueAfterToken(
		value,
		depth,
		start,
		tokenLimit,
		name,
	)
}

func (scanner *manifestPreflightScanner) scanValueAfterToken(
	token json.Token,
	depth int,
	start int,
	tokenLimit int,
	name string,
) error {
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth > maxManifestPreflightDepth {
		return fmt.Errorf(
			"JSON nesting exceeds %d levels",
			maxManifestPreflightDepth,
		)
	}
	var closing json.Delim
	switch delimiter {
	case '{':
		closing = '}'
		for scanner.decoder.More() {
			key, err := scanner.nextBoundedToken(start, tokenLimit, name)
			if err != nil {
				return err
			}
			if _, ok := key.(string); !ok {
				return errors.New("JSON object key is not a string")
			}
			value, err := scanner.nextBoundedToken(start, tokenLimit, name)
			if err != nil {
				return err
			}
			if err := scanner.scanValueAfterToken(
				value,
				depth+1,
				start,
				tokenLimit,
				name,
			); err != nil {
				return err
			}
		}
	case '[':
		closing = ']'
		for scanner.decoder.More() {
			value, err := scanner.nextBoundedToken(start, tokenLimit, name)
			if err != nil {
				return err
			}
			if err := scanner.scanValueAfterToken(
				value,
				depth+1,
				start,
				tokenLimit,
				name,
			); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	actual, err := scanner.nextBoundedToken(start, tokenLimit, name)
	if err != nil {
		return err
	}
	if actual != closing {
		return fmt.Errorf("JSON value has invalid closing delimiter")
	}
	return nil
}

func (scanner *manifestPreflightScanner) nextBoundedToken(
	start int,
	limit int,
	name string,
) (json.Token, error) {
	value, err := scanner.nextToken()
	if err != nil {
		return nil, err
	}
	if scanner.tokens-start > limit {
		return nil, fmt.Errorf("%s exceeds %d JSON tokens", name, limit)
	}
	return value, nil
}

func invalidManifestPreflight(err error) error {
	return fmt.Errorf("%w: manifest JSON preflight: %v", ErrInvalidBundle, err)
}

func canonicalManifestJSON(manifest Manifest) ([]byte, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: encode manifest: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maxManifestBytes,
			MaxDepth: maxManifestPreflightDepth,
			MaxNodes: maxManifestPreflightTokens,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: canonicalize manifest: %w", err)
	}
	return canonical, nil
}

func validateStoreIdentity(identity StoreIdentity) error {
	if identity.ApplicationID != currentstore.ApplicationID ||
		identity.UserVersion != currentstore.UserVersion ||
		identity.SchemaIdentity != currentstore.SchemaIdentity ||
		identity.SchemaFingerprint != currentstore.ExpectedSchemaFingerprint ||
		identity.GeneratorID != currentstore.GeneratorID {
		return fmt.Errorf(
			"%w: Store identity does not match this Current Store release",
			ErrInvalidBundle,
		)
	}
	if err := validateOpaque("store instance ID", identity.StoreInstanceID); err != nil {
		return err
	}
	return nil
}

func canonicalCurrent(input []CurrentPublication) ([]CurrentPublication, error) {
	if len(input) > maxCurrentCount {
		return nil, fmt.Errorf("%w: too many current publications", ErrInvalidBundle)
	}
	result := make([]CurrentPublication, len(input))
	copy(result, input)
	seen := make(map[string]struct{}, len(result))
	for index, current := range result {
		for name, value := range map[string]string{
			"tenant ID":  current.TenantID,
			"Control ID": current.ControlID,
			"Catalog ID": current.CatalogID,
		} {
			if err := validateOpaque(name, value); err != nil {
				return nil, fmt.Errorf("current publication %d: %w", index, err)
			}
		}
		if current.PointerRevision == 0 || current.ControlRevision == 0 ||
			current.CatalogGeneration == 0 ||
			!moduleapi.ValidSHA256(current.ControlDigest) ||
			!moduleapi.ValidSHA256(current.CatalogDigest) {
			return nil, fmt.Errorf(
				"%w: current publication %d has invalid revision or digest",
				ErrInvalidBundle,
				index,
			)
		}
		if _, duplicate := seen[current.TenantID]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate current tenant %q",
				ErrInvalidBundle,
				current.TenantID,
			)
		}
		seen[current.TenantID] = struct{}{}
	}
	sort.Slice(result, func(left, right int) bool {
		return bytes.Compare(
			[]byte(result[left].TenantID),
			[]byte(result[right].TenantID),
		) < 0
	})
	return result, nil
}

func canonicalArtifacts(input []Artifact) ([]Artifact, error) {
	if len(input) > maxArtifactCount {
		return nil, fmt.Errorf(
			"%w: artifact count exceeds %d",
			ErrInvalidBundle,
			maxArtifactCount,
		)
	}
	result := make([]Artifact, len(input))
	copy(result, input)
	seen := make(map[string]struct{}, len(result))
	var total int64
	for index, artifact := range result {
		if !moduleapi.ValidSHA256(artifact.Digest) ||
			artifact.Path != artifactsDirectory+"/"+artifact.Digest ||
			artifact.SizeBytes <= 0 || artifact.SizeBytes > maxArtifactBytes ||
			artifact.SizeBytes > maxArtifactAggregateBytes-total {
			return nil, fmt.Errorf(
				"%w: artifact %d has invalid path, digest or size",
				ErrInvalidBundle,
				index,
			)
		}
		total += artifact.SizeBytes
		if _, duplicate := seen[artifact.Digest]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate artifact digest %s",
				ErrInvalidBundle,
				artifact.Digest,
			)
		}
		seen[artifact.Digest] = struct{}{}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Digest < result[right].Digest
	})
	return result, nil
}

func validateOpaque(name, value string) error {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"%w: %s must be canonical, non-empty and at most %d bytes",
			ErrInvalidBundle,
			name,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf(
				"%w: %s contains a control character",
				ErrInvalidBundle,
				name,
			)
		}
	}
	return nil
}

func cloneManifest(manifest Manifest) Manifest {
	current := make([]CurrentPublication, len(manifest.Current))
	copy(current, manifest.Current)
	manifest.Current = current
	artifacts := make([]Artifact, len(manifest.Artifacts))
	copy(artifacts, manifest.Artifacts)
	manifest.Artifacts = artifacts
	return manifest
}
