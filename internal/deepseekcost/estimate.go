// Package deepseekcost interprets the versioned DeepSeek pricing document
// stored inside a ModelPriceSnapshot. It is a read-only calculator: it owns no
// budget, Usage ledger, Store write path, Runtime, or provider dispatch.
package deepseekcost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"

	"github.com/endview/freeagent/internal/corecontract"
)

const (
	PricingSchemaVersionV1 = "deepseek-token-pricing/v1"
	ProviderDeepSeekV1     = "deepseek"
	EstimateFormulaV1      = "deepseek-proportional-token-cost/v1"

	modelV4Flash = "deepseek-v4-flash"
	modelV4Pro   = "deepseek-v4-pro"
)

var ErrInvalidEstimate = errors.New("deepseekcost: invalid estimate input")

// Status distinguishes a known proportional estimate from missing token or
// price facts. UNKNOWN is never rewritten as a numeric zero.
type Status string

const (
	StatusKnown   Status = "KNOWN"
	StatusUnknown Status = "UNKNOWN"
)

// PricingV1 is denominated in currency microunits per one million tokens.
type PricingV1 struct {
	SchemaVersion                     string `json:"schema_version"`
	CachedInputPerMillionMicrounits   uint64 `json:"cached_input_per_million_microunits"`
	UncachedInputPerMillionMicrounits uint64 `json:"uncached_input_per_million_microunits"`
	OutputPerMillionMicrounits        uint64 `json:"output_per_million_microunits"`
}

// Estimate is a detached observer result. Value is expressed in the frozen
// snapshot currency (for example CNY), not in microunits. It is present only
// for KNOWN and uses an exact finite decimal with at most twelve places.
type Estimate struct {
	Status              Status  `json:"status"`
	Value               *string `json:"value"`
	Currency            string  `json:"currency"`
	PriceSnapshotID     string  `json:"price_snapshot_id"`
	PriceSnapshotDigest string  `json:"price_snapshot_digest"`
	FormulaVersion      string  `json:"formula_version"`
}

// Calculate validates one frozen DeepSeek price snapshot and derives a
// proportional token estimate. Reasoning tokens are not charged separately:
// DeepSeek reports them as a subset of completion/output tokens.
func Calculate(
	snapshot corecontract.ModelPriceSnapshotV1,
	tokens corecontract.UsageTokens,
) (Estimate, error) {
	base := Estimate{
		Status:              StatusUnknown,
		Currency:            snapshot.Currency,
		PriceSnapshotID:     snapshot.PriceSnapshotID,
		PriceSnapshotDigest: snapshot.Digest,
		FormulaVersion:      EstimateFormulaV1,
	}
	frozen, _, err := corecontract.NewModelPriceSnapshotV1(snapshot)
	if err != nil || frozen.Digest != snapshot.Digest {
		return Estimate{}, fmt.Errorf(
			"%w: price snapshot is not an intact frozen value",
			ErrInvalidEstimate,
		)
	}
	if snapshot.Provider != ProviderDeepSeekV1 ||
		(snapshot.Model != modelV4Flash && snapshot.Model != modelV4Pro) {
		return Estimate{}, fmt.Errorf(
			"%w: price snapshot is not an exact supported DeepSeek model",
			ErrInvalidEstimate,
		)
	}
	if err := tokens.Validate(); err != nil {
		return Estimate{}, fmt.Errorf("%w: Usage: %v", ErrInvalidEstimate, err)
	}
	if tokens.Reasoning != nil && tokens.Output != nil &&
		*tokens.Reasoning > *tokens.Output {
		return Estimate{}, fmt.Errorf(
			"%w: DeepSeek reasoning tokens exceed output tokens",
			ErrInvalidEstimate,
		)
	}
	if snapshot.PricingStatus == corecontract.PricingUnknown ||
		tokens.CachedInput == nil || tokens.UncachedInput == nil ||
		tokens.Output == nil {
		return base, nil
	}
	if snapshot.PricingStatus != corecontract.PricingKnown {
		return Estimate{}, fmt.Errorf(
			"%w: unsupported pricing status",
			ErrInvalidEstimate,
		)
	}
	pricing, err := parsePricingV1(snapshot.Pricing)
	if err != nil {
		return Estimate{}, err
	}

	// rate is microunits / 1,000,000 tokens. Dividing the weighted
	// numerator by another 1,000,000 converts microunits to currency units.
	numerator := new(big.Int)
	addProduct(numerator, *tokens.CachedInput, pricing.CachedInputPerMillionMicrounits)
	addProduct(numerator, *tokens.UncachedInput, pricing.UncachedInputPerMillionMicrounits)
	addProduct(numerator, *tokens.Output, pricing.OutputPerMillionMicrounits)
	value := finiteDecimal(numerator, 12)
	base.Status = StatusKnown
	base.Value = &value
	return base, nil
}

func parsePricingV1(raw json.RawMessage) (PricingV1, error) {
	type pricingWire struct {
		SchemaVersion                     string  `json:"schema_version"`
		CachedInputPerMillionMicrounits   *uint64 `json:"cached_input_per_million_microunits"`
		UncachedInputPerMillionMicrounits *uint64 `json:"uncached_input_per_million_microunits"`
		OutputPerMillionMicrounits        *uint64 `json:"output_per_million_microunits"`
	}
	var wire pricingWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return PricingV1{}, fmt.Errorf(
			"%w: decode pricing document: %v",
			ErrInvalidEstimate,
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return PricingV1{}, fmt.Errorf(
			"%w: trailing pricing JSON",
			ErrInvalidEstimate,
		)
	}
	if wire.SchemaVersion != PricingSchemaVersionV1 {
		return PricingV1{}, fmt.Errorf(
			"%w: pricing schema_version must be %q",
			ErrInvalidEstimate,
			PricingSchemaVersionV1,
		)
	}
	if wire.CachedInputPerMillionMicrounits == nil ||
		wire.UncachedInputPerMillionMicrounits == nil ||
		wire.OutputPerMillionMicrounits == nil {
		return PricingV1{}, fmt.Errorf(
			"%w: all pricing rates are required",
			ErrInvalidEstimate,
		)
	}
	pricing := PricingV1{
		SchemaVersion:                     wire.SchemaVersion,
		CachedInputPerMillionMicrounits:   *wire.CachedInputPerMillionMicrounits,
		UncachedInputPerMillionMicrounits: *wire.UncachedInputPerMillionMicrounits,
		OutputPerMillionMicrounits:        *wire.OutputPerMillionMicrounits,
	}
	for name, rate := range map[string]uint64{
		"cached input rate":   pricing.CachedInputPerMillionMicrounits,
		"uncached input rate": pricing.UncachedInputPerMillionMicrounits,
		"output rate":         pricing.OutputPerMillionMicrounits,
	} {
		if rate > math.MaxInt64 {
			return PricingV1{}, fmt.Errorf(
				"%w: %s exceeds the bounded calculator range",
				ErrInvalidEstimate,
				name,
			)
		}
	}
	return pricing, nil
}

func addProduct(total *big.Int, tokens uint64, rate uint64) {
	product := new(big.Int).Mul(
		new(big.Int).SetUint64(tokens),
		new(big.Int).SetUint64(rate),
	)
	total.Add(total, product)
}

func finiteDecimal(numerator *big.Int, scale int) string {
	if numerator.Sign() == 0 {
		return "0"
	}
	digits := numerator.String()
	if len(digits) <= scale {
		digits = string(bytes.Repeat([]byte{'0'}, scale-len(digits)+1)) + digits
	}
	integer := digits[:len(digits)-scale]
	fraction := bytes.TrimRight([]byte(digits[len(digits)-scale:]), "0")
	if len(fraction) == 0 {
		return integer
	}
	return integer + "." + string(fraction)
}
