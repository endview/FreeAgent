package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModelPriceSnapshotSchemaVersionV1 = "model-price-snapshot/v1"
	modelPriceSnapshotDigestDomain    = "freeagent.model-price-snapshot/v1"
)

// PricingStatus states whether Core can calculate an estimate from the frozen
// pricing document. UNKNOWN is a real value; it never means a zero price.
type PricingStatus string

const (
	PricingKnown   PricingStatus = "KNOWN"
	PricingUnknown PricingStatus = "UNKNOWN"
)

func (status PricingStatus) Validate() error {
	switch status {
	case PricingKnown, PricingUnknown:
		return nil
	default:
		return fmt.Errorf(
			"corecontract: unsupported pricing status %q",
			status,
		)
	}
}

// ModelPriceSnapshotV1 freezes the billing semantics used by one or more
// attempts. Pricing is provider-neutral canonical JSON interpreted only by a
// matching cost calculator, not by the Universal Loop.
type ModelPriceSnapshotV1 struct {
	SchemaVersion   string          `json:"schema_version"`
	PriceSnapshotID string          `json:"price_snapshot_id"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	BillingVersion  string          `json:"billing_version"`
	Currency        string          `json:"currency"`
	PricingStatus   PricingStatus   `json:"pricing_status"`
	Pricing         json.RawMessage `json:"pricing"`
	Digest          string          `json:"digest"`
}

// NewModelPriceSnapshotV1 validates and freezes one price snapshot. Digest is
// self-excluding, so the same pricing semantics always produce the same
// identity independently of database insertion time.
func NewModelPriceSnapshotV1(
	input ModelPriceSnapshotV1,
) (ModelPriceSnapshotV1, []byte, error) {
	if input.SchemaVersion != ModelPriceSnapshotSchemaVersionV1 {
		return ModelPriceSnapshotV1{}, nil, fmt.Errorf(
			"corecontract: model price snapshot schema version must be %q",
			ModelPriceSnapshotSchemaVersionV1,
		)
	}
	for name, value := range map[string]string{
		"price snapshot ID": input.PriceSnapshotID,
		"provider":          input.Provider,
		"model":             input.Model,
		"billing version":   input.BillingVersion,
		"currency":          input.Currency,
	} {
		if !validOpaque(value, maxOpaqueIDBytes) {
			return ModelPriceSnapshotV1{}, nil, fmt.Errorf(
				"corecontract: invalid %s",
				name,
			)
		}
	}
	if err := input.PricingStatus.Validate(); err != nil {
		return ModelPriceSnapshotV1{}, nil, err
	}
	if len(input.Pricing) == 0 {
		return ModelPriceSnapshotV1{}, nil, fmt.Errorf(
			"corecontract: model pricing document is required",
		)
	}
	pricing, err := moduleapi.CanonicalJSONWithLimits(
		input.Pricing,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxTextBytes,
			MaxDepth: 128,
			MaxNodes: moduleapi.MaxTextBytes,
		},
	)
	if err != nil {
		return ModelPriceSnapshotV1{}, nil, fmt.Errorf(
			"corecontract: model pricing document: %w",
			err,
		)
	}

	frozen := input
	frozen.Pricing = json.RawMessage(bytes.Clone(pricing))
	frozen.Digest = ""
	identityCanonical, err := canonicalModelPriceSnapshotV1(frozen, false)
	if err != nil {
		return ModelPriceSnapshotV1{}, nil, err
	}
	frozen.Digest = moduleapi.Digest(
		modelPriceSnapshotDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalModelPriceSnapshotV1(frozen, true)
	if err != nil {
		return ModelPriceSnapshotV1{}, nil, err
	}
	return frozen, canonical, nil
}

// RestoreModelPriceSnapshotV1 accepts only the exact frozen representation.
func RestoreModelPriceSnapshotV1(
	canonical []byte,
) (ModelPriceSnapshotV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ModelPriceSnapshotV1{}, err
	}
	var decoded ModelPriceSnapshotV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ModelPriceSnapshotV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewModelPriceSnapshotV1(decoded)
	if err != nil {
		return ModelPriceSnapshotV1{}, err
	}
	if rebuilt.Digest != decoded.Digest ||
		!bytes.Equal(rebuiltCanonical, canonical) {
		return ModelPriceSnapshotV1{}, fmt.Errorf(
			"corecontract: model price snapshot is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func canonicalModelPriceSnapshotV1(
	snapshot ModelPriceSnapshotV1,
	includeDigest bool,
) ([]byte, error) {
	type wire struct {
		SchemaVersion   string          `json:"schema_version"`
		PriceSnapshotID string          `json:"price_snapshot_id"`
		Provider        string          `json:"provider"`
		Model           string          `json:"model"`
		BillingVersion  string          `json:"billing_version"`
		Currency        string          `json:"currency"`
		PricingStatus   PricingStatus   `json:"pricing_status"`
		Pricing         json.RawMessage `json:"pricing"`
		Digest          string          `json:"digest,omitempty"`
	}
	value := wire{
		SchemaVersion:   snapshot.SchemaVersion,
		PriceSnapshotID: snapshot.PriceSnapshotID,
		Provider:        snapshot.Provider,
		Model:           snapshot.Model,
		BillingVersion:  snapshot.BillingVersion,
		Currency:        snapshot.Currency,
		PricingStatus:   snapshot.PricingStatus,
		Pricing:         snapshot.Pricing,
	}
	if includeDigest {
		value.Digest = snapshot.Digest
	}
	return canonicalJSON(value)
}
