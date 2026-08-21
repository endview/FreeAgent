package controlcontract

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type CatalogEntry struct {
	Activation moduleapi.ActivatedModuleRef `json:"activation"`
	Provides   []moduleapi.PortRef          `json:"provides"`
}

type CatalogGeneration struct {
	SchemaVersion         string         `json:"schema_version"`
	GenerationID          string         `json:"generation_id"`
	Generation            uint64         `json:"generation"`
	TenantID              string         `json:"tenant_id"`
	ControlSnapshotID     string         `json:"control_snapshot_id"`
	ControlSnapshotDigest string         `json:"control_snapshot_digest"`
	Entries               []CatalogEntry `json:"entries"`
	Digest                string         `json:"digest,omitempty"`
}

func NewCatalogGeneration(
	input CatalogGeneration,
) (CatalogGeneration, CatalogGenerationRef, []byte, error) {
	if input.SchemaVersion != CatalogGenerationSchemaVersionV1 {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf(
				"controlcontract: catalog schema_version must be %q",
				CatalogGenerationSchemaVersionV1,
			)
	}
	if !validOpaque(input.GenerationID) {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf("controlcontract: invalid catalog generation ID")
	}
	if input.Generation == 0 {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf("controlcontract: catalog generation must be positive")
	}
	if !validOpaque(input.TenantID) {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf("controlcontract: invalid catalog tenant ID")
	}
	if !validOpaque(input.ControlSnapshotID) {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf("controlcontract: invalid catalog control snapshot ID")
	}
	if !moduleapi.ValidSHA256(input.ControlSnapshotDigest) {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil,
			fmt.Errorf("controlcontract: invalid catalog control snapshot digest")
	}
	entries, err := canonicalCatalogEntries(input.Entries)
	if err != nil {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil, err
	}

	frozen := CatalogGeneration{
		SchemaVersion:         input.SchemaVersion,
		GenerationID:          input.GenerationID,
		Generation:            input.Generation,
		TenantID:              input.TenantID,
		ControlSnapshotID:     input.ControlSnapshotID,
		ControlSnapshotDigest: input.ControlSnapshotDigest,
		Entries:               entries,
	}
	identityCanonical, err := canonicalJSON(frozen)
	if err != nil {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil, err
	}
	frozen.Digest = moduleapi.Digest(
		runtimeCatalogDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return CatalogGeneration{}, CatalogGenerationRef{}, nil, err
	}
	ref := CatalogGenerationRef{
		GenerationID: frozen.GenerationID,
		Generation:   frozen.Generation,
		Digest:       frozen.Digest,
	}
	return cloneCatalogGeneration(frozen), ref, bytes.Clone(canonical), nil
}

func RestoreCatalogGeneration(
	canonical []byte,
	ref CatalogGenerationRef,
) (CatalogGeneration, error) {
	if err := ref.Validate(); err != nil {
		return CatalogGeneration{}, err
	}
	if err := requireCanonicalObject(canonical); err != nil {
		return CatalogGeneration{}, err
	}
	var decoded CatalogGeneration
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CatalogGeneration{}, err
	}
	rebuilt, rebuiltRef, rebuiltCanonical, err := NewCatalogGeneration(decoded)
	if err != nil {
		return CatalogGeneration{}, err
	}
	if rebuiltRef != ref ||
		decoded.Digest != rebuilt.Digest ||
		!bytes.Equal(canonical, rebuiltCanonical) {
		return CatalogGeneration{},
			fmt.Errorf("controlcontract: catalog generation does not match reference")
	}
	return rebuilt, nil
}

func (catalog CatalogGeneration) FindInstance(
	instanceID string,
) (CatalogEntry, bool) {
	for _, entry := range catalog.Entries {
		if entry.Activation.InstanceID == instanceID {
			return cloneCatalogEntry(entry), true
		}
	}
	return CatalogEntry{}, false
}

func canonicalCatalogEntries(input []CatalogEntry) ([]CatalogEntry, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: catalog may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	entries := make([]CatalogEntry, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, entry := range input {
		if err := entry.Activation.Validate(); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: catalog entry %d activation: %w",
				index,
				err,
			)
		}
		if err := validateS1ExecutionClass(
			entry.Activation.ExecutionClass,
		); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: catalog entry %d: %w",
				index,
				err,
			)
		}
		if _, duplicate := seen[entry.Activation.InstanceID]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate catalog instance ID %q",
				entry.Activation.InstanceID,
			)
		}
		seen[entry.Activation.InstanceID] = struct{}{}
		provides, err := canonicalPorts(entry.Provides, true)
		if err != nil {
			return nil, fmt.Errorf(
				"controlcontract: catalog entry %d: %w",
				index,
				err,
			)
		}
		entry.Provides = provides
		entries[index] = entry
	}
	sort.Slice(entries, func(left, right int) bool {
		return compareText(
			entries[left].Activation.InstanceID,
			entries[right].Activation.InstanceID,
		) < 0
	})
	return entries, nil
}

func cloneCatalogEntry(entry CatalogEntry) CatalogEntry {
	provides := make([]moduleapi.PortRef, len(entry.Provides))
	copy(provides, entry.Provides)
	entry.Provides = provides
	return entry
}

func cloneCatalogGeneration(catalog CatalogGeneration) CatalogGeneration {
	entries := make([]CatalogEntry, len(catalog.Entries))
	for index, entry := range catalog.Entries {
		entries[index] = cloneCatalogEntry(entry)
	}
	catalog.Entries = entries
	return catalog
}
