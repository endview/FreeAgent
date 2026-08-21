package deepseekcost

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestCalculateKnownProportionalCostWithoutDoubleChargingReasoning(t *testing.T) {
	snapshot := testSnapshot(t, corecontract.PricingKnown, json.RawMessage(
		`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
	))
	input := uint64(1500000)
	cached := uint64(500000)
	uncached := uint64(1000000)
	output := uint64(250000)
	reasoning := uint64(200000)
	estimate, err := Calculate(snapshot, corecontract.UsageTokens{
		Input: &input, CachedInput: &cached, UncachedInput: &uncached,
		Output: &output, Reasoning: &reasoning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Status != StatusKnown || estimate.Value == nil ||
		*estimate.Value != "1.51" || estimate.Currency != "CNY" ||
		estimate.PriceSnapshotDigest != snapshot.Digest {
		t.Fatalf("estimate=%+v", estimate)
	}
}

func TestCalculateKnownProPriceAndKnownZero(t *testing.T) {
	pricing := json.RawMessage(
		`{"cached_input_per_million_microunits":25000,"output_per_million_microunits":6000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":3000000}`,
	)
	pro := testSnapshot(t, corecontract.PricingKnown, pricing)
	pro.PriceSnapshotID = "price-deepseek-v4-pro-test"
	pro.Model = modelV4Pro
	pro, _, err := corecontract.NewModelPriceSnapshotV1(pro)
	if err != nil {
		t.Fatal(err)
	}
	cached := uint64(500000)
	uncached := uint64(500000)
	output := uint64(100000)
	input := cached + uncached
	estimate, err := Calculate(pro, corecontract.UsageTokens{
		Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Status != StatusKnown || estimate.Value == nil ||
		*estimate.Value != "2.1125" || estimate.Currency != "CNY" {
		t.Fatalf("Pro estimate=%+v", estimate)
	}

	zero := uint64(0)
	zeroEstimate, err := Calculate(pro, corecontract.UsageTokens{
		Input: &zero, CachedInput: &zero, UncachedInput: &zero, Output: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	if zeroEstimate.Status != StatusKnown || zeroEstimate.Value == nil ||
		*zeroEstimate.Value != "0" {
		t.Fatalf("known-zero estimate=%+v", zeroEstimate)
	}
}

func TestCalculateRejectsReasoningAboveCompletion(t *testing.T) {
	snapshot := testSnapshot(t, corecontract.PricingKnown, json.RawMessage(
		`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
	))
	zero := uint64(0)
	output := uint64(1)
	reasoning := uint64(2)
	_, err := Calculate(snapshot, corecontract.UsageTokens{
		Input: &zero, CachedInput: &zero, UncachedInput: &zero,
		Output: &output, Reasoning: &reasoning,
	})
	if !errors.Is(err, ErrInvalidEstimate) {
		t.Fatalf("reasoning above completion error=%v", err)
	}
}

func TestCalculatePreservesUnknownPriceOrUsage(t *testing.T) {
	knownPricing := json.RawMessage(
		`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
	)
	zero := uint64(0)
	for _, input := range []struct {
		name     string
		snapshot corecontract.ModelPriceSnapshotV1
		tokens   corecontract.UsageTokens
	}{
		{
			name: "unknown pricing",
			snapshot: testSnapshot(
				t,
				corecontract.PricingUnknown,
				json.RawMessage(`{"reason":"provider price unavailable"}`),
			),
			tokens: corecontract.UsageTokens{
				CachedInput: &zero, UncachedInput: &zero, Output: &zero,
			},
		},
		{
			name:     "unknown cached tokens",
			snapshot: testSnapshot(t, corecontract.PricingKnown, knownPricing),
			tokens: corecontract.UsageTokens{
				UncachedInput: &zero, Output: &zero,
			},
		},
	} {
		t.Run(input.name, func(t *testing.T) {
			estimate, err := Calculate(input.snapshot, input.tokens)
			if err != nil {
				t.Fatal(err)
			}
			if estimate.Status != StatusUnknown || estimate.Value != nil {
				t.Fatalf("estimate=%+v", estimate)
			}
		})
	}
}

func TestCalculateRejectsWrongProviderSchemaAndTamperedSnapshot(t *testing.T) {
	pricing := json.RawMessage(
		`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
	)
	zero := uint64(0)
	tokens := corecontract.UsageTokens{
		CachedInput: &zero, UncachedInput: &zero, Output: &zero,
	}
	wrongProvider := testSnapshot(t, corecontract.PricingKnown, pricing)
	wrongProvider.Provider = "other"
	wrongSchema := testSnapshot(t, corecontract.PricingKnown, pricing)
	wrongSchema.Pricing = json.RawMessage(
		`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"other/v1","uncached_input_per_million_microunits":1000000}`,
	)
	wrongSchema, _, _ = corecontract.NewModelPriceSnapshotV1(wrongSchema)
	missingRate := testSnapshot(
		t,
		corecontract.PricingKnown,
		json.RawMessage(
			`{"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
		),
	)
	for _, snapshot := range []corecontract.ModelPriceSnapshotV1{
		wrongProvider,
		wrongSchema,
		missingRate,
	} {
		if _, err := Calculate(snapshot, tokens); !errors.Is(err, ErrInvalidEstimate) {
			t.Fatalf("Calculate error=%v", err)
		}
	}
}

func testSnapshot(
	t *testing.T,
	status corecontract.PricingStatus,
	pricing json.RawMessage,
) corecontract.ModelPriceSnapshotV1 {
	t.Helper()
	snapshot, _, err := corecontract.NewModelPriceSnapshotV1(
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "price-deepseek-v4-flash-test",
			Provider:        ProviderDeepSeekV1,
			Model:           modelV4Flash,
			BillingVersion:  "test-v1",
			Currency:        "CNY",
			PricingStatus:   status,
			Pricing:         pricing,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
