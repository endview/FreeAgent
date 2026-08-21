package corecontract

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CompositeRepairSkippedEventKind            = "COMPOSITE_REPAIR_SKIPPED"
	CompositeRepairSkippedEventSchemaVersionV1 = "composite-repair-skipped-event/v1"
)

// CompositeRepairSkippedEventV1 permanently closes one unused pre-frozen
// repair Run. It binds the same family, physical slot and exact round-zero
// Reviewer result as activation; the fixed reason is also carried by the
// terminal Loop continuation.
type CompositeRepairSkippedEventV1 struct {
	SchemaVersion      string `json:"schema_version"`
	RunID              string `json:"run_id"`
	RootRunID          string `json:"root_run_id"`
	RootManifestDigest string `json:"root_manifest_digest"`
	ParentSlotID       string `json:"parent_slot_id"`
	RepairRound        uint32 `json:"repair_round"`
	SourceVerdictRef   string `json:"source_verdict_ref"`
	Reason             string `json:"reason"`
}

func NewCompositeRepairSkippedEventV1(
	input CompositeRepairSkippedEventV1,
) (CompositeRepairSkippedEventV1, []byte, error) {
	if input.SchemaVersion != CompositeRepairSkippedEventSchemaVersionV1 {
		return CompositeRepairSkippedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair skip schema version must be %q",
			CompositeRepairSkippedEventSchemaVersionV1,
		)
	}
	if !validOpaque(input.RunID, maxOpaqueIDBytes) ||
		!validOpaque(input.RootRunID, maxOpaqueIDBytes) ||
		input.RunID == input.RootRunID {
		return CompositeRepairSkippedEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid composite repair skip Run identity",
		)
	}
	if !validOpaque(input.ParentSlotID, maxOpaqueIDBytes) ||
		input.ParentSlotID == CompositeReviewerParentSlotIDV1 {
		return CompositeRepairSkippedEventV1{}, nil, fmt.Errorf(
			"corecontract: invalid composite repair skip physical ParentSlotID",
		)
	}
	if input.RepairRound != CompositeRepairRoundOneV1 ||
		input.Reason != CompositeRepairSkippedReasonV1 {
		return CompositeRepairSkippedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair skip identity differs",
		)
	}
	if !moduleapi.ValidSHA256(input.RootManifestDigest) ||
		!moduleapi.ValidSHA256(input.SourceVerdictRef) {
		return CompositeRepairSkippedEventV1{}, nil, fmt.Errorf(
			"corecontract: composite repair skip requires root and verdict digests",
		)
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return CompositeRepairSkippedEventV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreCompositeRepairSkippedEventV1(
	canonical []byte,
) (CompositeRepairSkippedEventV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return CompositeRepairSkippedEventV1{}, err
	}
	var decoded CompositeRepairSkippedEventV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return CompositeRepairSkippedEventV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewCompositeRepairSkippedEventV1(decoded)
	if err != nil {
		return CompositeRepairSkippedEventV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return CompositeRepairSkippedEventV1{}, fmt.Errorf(
			"corecontract: composite repair skip is not frozen canonically",
		)
	}
	return rebuilt, nil
}
