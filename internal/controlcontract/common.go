package controlcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ControlSnapshotSchemaVersionV2   = "control-snapshot/v2"
	CatalogGenerationSchemaVersionV1 = "runtime-catalog/v1"

	controlSnapshotDigestDomain = "freeagent.control-snapshot/v1"
	runtimeCatalogDigestDomain  = "freeagent.runtime-catalog/v1"

	maxOpaqueIDBytes = 256
)

// ControlSnapshotRef is the exact immutable identity returned with a frozen
// control snapshot.
type ControlSnapshotRef struct {
	SnapshotID string `json:"snapshot_id"`
	Revision   uint64 `json:"revision"`
	Digest     string `json:"digest"`
}

func (ref ControlSnapshotRef) Validate() error {
	if !validOpaque(ref.SnapshotID) {
		return fmt.Errorf("controlcontract: invalid control snapshot ID")
	}
	if ref.Revision == 0 {
		return fmt.Errorf("controlcontract: control snapshot revision must be positive")
	}
	if !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("controlcontract: invalid control snapshot digest")
	}
	return nil
}

// CatalogGenerationRef is the exact immutable identity returned with a frozen
// runtime catalog generation.
type CatalogGenerationRef struct {
	GenerationID string `json:"generation_id"`
	Generation   uint64 `json:"generation"`
	Digest       string `json:"digest"`
}

func (ref CatalogGenerationRef) Validate() error {
	if !validOpaque(ref.GenerationID) {
		return fmt.Errorf("controlcontract: invalid catalog generation ID")
	}
	if ref.Generation == 0 {
		return fmt.Errorf("controlcontract: catalog generation must be positive")
	}
	if !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("controlcontract: invalid catalog generation digest")
	}
	return nil
}

// PublishedBasis is the exact Control/Catalog pointer observed before
// compilation. Current Store must compare all fields again inside the
// Admission transaction.
type PublishedBasis struct {
	TenantID        string               `json:"tenant_id"`
	PointerRevision uint64               `json:"pointer_revision"`
	Control         ControlSnapshotRef   `json:"control"`
	Catalog         CatalogGenerationRef `json:"catalog"`
}

func (basis PublishedBasis) Validate() error {
	if !validOpaque(basis.TenantID) {
		return fmt.Errorf("controlcontract: invalid published basis tenant ID")
	}
	if basis.PointerRevision == 0 {
		return fmt.Errorf(
			"controlcontract: published basis pointer revision must be positive",
		)
	}
	if err := basis.Control.Validate(); err != nil {
		return err
	}
	if err := basis.Catalog.Validate(); err != nil {
		return err
	}
	return nil
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("controlcontract: encode canonical object: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("controlcontract: canonicalize object: %w", err)
	}
	return canonical, nil
}

func requireCanonicalObject(canonical []byte) error {
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) ||
		len(canonical) == 0 || canonical[0] != '{' {
		return fmt.Errorf("controlcontract: object is not exact canonical JSON")
	}
	return nil
}

func decodeStrict(canonical []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("controlcontract: decode object: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("controlcontract: object has trailing JSON")
		}
		return fmt.Errorf("controlcontract: decode trailing object data: %w", err)
	}
	return nil
}

func validOpaque(value string) bool {
	if value == "" ||
		len(value) > maxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateS1Port(port moduleapi.PortRef) error {
	if err := port.Validate(); err != nil {
		return err
	}
	for _, supported := range moduleapi.S1PortRefs() {
		if supported == port {
			return nil
		}
	}
	return fmt.Errorf(
		"controlcontract: port %s/%s is not registered in S1",
		port.Name,
		port.ExactVersion,
	)
}

func validateS1ExecutionClass(class moduleapi.ExecutionClass) error {
	switch class {
	case moduleapi.ExecutionDeclarative,
		moduleapi.ExecutionTrustedInProcess,
		moduleapi.ExecutionLocalProcess,
		moduleapi.ExecutionRemote,
		moduleapi.ExecutionWASM:
		return nil
	default:
		return fmt.Errorf(
			"controlcontract: execution class %q is not supported",
			class,
		)
	}
}

func compareText(left, right string) int {
	return bytes.Compare([]byte(left), []byte(right))
}

func lessTypedRef(
	leftID string,
	leftVersion string,
	leftDigest string,
	rightID string,
	rightVersion string,
	rightDigest string,
) bool {
	if comparison := compareText(leftID, rightID); comparison != 0 {
		return comparison < 0
	}
	if comparison := compareText(leftVersion, rightVersion); comparison != 0 {
		return comparison < 0
	}
	return compareText(leftDigest, rightDigest) < 0
}

func lessPort(left, right moduleapi.PortRef) bool {
	if comparison := compareText(left.Name, right.Name); comparison != 0 {
		return comparison < 0
	}
	return compareText(left.ExactVersion, right.ExactVersion) < 0
}

func canonicalPorts(
	input []moduleapi.PortRef,
	requireNonEmpty bool,
) ([]moduleapi.PortRef, error) {
	if requireNonEmpty && len(input) == 0 {
		return nil, fmt.Errorf("controlcontract: catalog entry provides no ports")
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: port list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	ports := make([]moduleapi.PortRef, len(input))
	copy(ports, input)
	seen := make(map[string]struct{}, len(ports))
	for index, port := range ports {
		if err := validateS1Port(port); err != nil {
			return nil, fmt.Errorf("controlcontract: port %d: %w", index, err)
		}
		key, _ := port.CanonicalKey()
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate exact port %s/%s",
				port.Name,
				port.ExactVersion,
			)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(ports, func(left, right int) bool {
		return lessPort(ports[left], ports[right])
	})
	return ports, nil
}
