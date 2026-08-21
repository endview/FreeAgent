package corecontract

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CompositeRepairActivatedEventKind            = "COMPOSITE_REPAIR_ACTIVATED"
	CompositeRepairActivatedEventSchemaVersionV1 = "composite-repair-activated-event/v1"
)

// CompositeRepairActivatedEventV1 is the authoritative transition that makes
// one pre-frozen round-one repair Run schedulable. The physical ParentSlotID
// binds both Specialist and Reviewer repair Runs without overloading their
// logical Specialist slot identities.
type CompositeRepairActivatedEventV1 struct {
	SchemaVersion      string `json:"schema_version"`
	RunID              string `json:"run_id"`
	RootRunID          string `json:"root_run_id"`
	RootManifestDigest string `json:"root_manifest_digest"`
	ParentSlotID       string `json:"parent_slot_id"`
	RepairRound        uint32 `json:"repair_round"`
	SourceVerdictRef   string `json:"source_verdict_ref"`
}

// NewCompositeRepairActivatedEventV1 freezes one strict activation payload.
// Store remains responsible for atomically checking that every identity is
// present in the admitted root Decision plan before appending this RunEvent.
func NewCompositeRepairActivatedEventV1(
	input CompositeRepairActivatedEventV1,
) (CompositeRepairActivatedEventV1, []byte, error) {
	if input.SchemaVersion != CompositeRepairActivatedEventSchemaVersionV1 {
		return CompositeRepairActivatedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair activation schema version must be %q",
			CompositeRepairActivatedEventSchemaVersionV1,
		)
	}
	if !validOpaque(input.RunID, maxOpaqueIDBytes) ||
		!validOpaque(input.RootRunID, maxOpaqueIDBytes) ||
		input.RunID == input.RootRunID {
		return CompositeRepairActivatedEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid composite repair activation Run identity",
		)
	}
	if !validOpaque(input.ParentSlotID, maxOpaqueIDBytes) ||
		input.ParentSlotID == CompositeReviewerParentSlotIDV1 {
		return CompositeRepairActivatedEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid composite repair activation physical ParentSlotID",
		)
	}
	if input.RepairRound != CompositeRepairRoundOneV1 {
		return CompositeRepairActivatedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair activation must target repair round one",
		)
	}
	if !moduleapi.ValidSHA256(input.RootManifestDigest) ||
		!moduleapi.ValidSHA256(input.SourceVerdictRef) {
		return CompositeRepairActivatedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair activation requires root and verdict digests",
		)
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return CompositeRepairActivatedEventV1{}, nil, err
	}
	return input, canonical, nil
}

// RestoreCompositeRepairActivatedEventV1 accepts only the exact canonical
// wire emitted by NewCompositeRepairActivatedEventV1.
func RestoreCompositeRepairActivatedEventV1(
	canonical []byte,
) (CompositeRepairActivatedEventV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return CompositeRepairActivatedEventV1{}, err
	}
	var decoded CompositeRepairActivatedEventV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CompositeRepairActivatedEventV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewCompositeRepairActivatedEventV1(decoded)
	if err != nil {
		return CompositeRepairActivatedEventV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return CompositeRepairActivatedEventV1{}, fmt.Errorf(
			"corecontract: composite repair activation is not frozen canonically",
		)
	}
	return rebuilt, nil
}
