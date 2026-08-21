package corecontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelPriceSnapshotV1RoundTripAndDigest(t *testing.T) {
	input := ModelPriceSnapshotV1{
		SchemaVersion:   ModelPriceSnapshotSchemaVersionV1,
		PriceSnapshotID: "deepseek-v4-2026-07",
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		BillingVersion:  "2026-07",
		Currency:        "CNY",
		PricingStatus:   PricingKnown,
		Pricing: json.RawMessage(
			`{ "output_per_million": 16, "input_per_million": 4 }`,
		),
	}
	first, canonical, err := NewModelPriceSnapshotV1(input)
	if err != nil {
		t.Fatal(err)
	}
	second, secondCanonical, err := NewModelPriceSnapshotV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest ||
		!bytes.Equal(canonical, secondCanonical) ||
		!strings.Contains(
			string(canonical),
			`"pricing":{"input_per_million":4,"output_per_million":16}`,
		) {
		t.Fatalf(
			"unstable snapshot first=%+v second=%+v canonical=%s",
			first,
			second,
			canonical,
		)
	}
	restored, err := RestoreModelPriceSnapshotV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Digest != first.Digest ||
		restored.PriceSnapshotID != first.PriceSnapshotID {
		t.Fatalf("restored=%+v", restored)
	}
}

func TestModelPriceSnapshotUnknownDoesNotBecomeZero(t *testing.T) {
	snapshot, canonical, err := NewModelPriceSnapshotV1(
		ModelPriceSnapshotV1{
			SchemaVersion:   ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "unknown-price",
			Provider:        "provider",
			Model:           "model",
			BillingVersion:  "unknown",
			Currency:        "USD",
			PricingStatus:   PricingUnknown,
			Pricing:         json.RawMessage(`{"reason":"not-configured"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PricingStatus != PricingUnknown ||
		bytes.Contains(canonical, []byte(`"cost":0`)) {
		t.Fatalf("unknown pricing was changed: %s", canonical)
	}
}

func TestModelPriceSnapshotV1RejectsInvalidAndTamperedValues(t *testing.T) {
	base := ModelPriceSnapshotV1{
		SchemaVersion:   ModelPriceSnapshotSchemaVersionV1,
		PriceSnapshotID: "price-1",
		Provider:        "provider",
		Model:           "model",
		BillingVersion:  "v1",
		Currency:        "USD",
		PricingStatus:   PricingKnown,
		Pricing:         json.RawMessage(`{"input":1}`),
	}
	tests := []struct {
		name   string
		mutate func(*ModelPriceSnapshotV1)
	}{
		{"schema", func(value *ModelPriceSnapshotV1) {
			value.SchemaVersion = "v2"
		}},
		{"id", func(value *ModelPriceSnapshotV1) {
			value.PriceSnapshotID = ""
		}},
		{"provider", func(value *ModelPriceSnapshotV1) {
			value.Provider = " provider "
		}},
		{"status", func(value *ModelPriceSnapshotV1) {
			value.PricingStatus = "FREE"
		}},
		{"pricing", func(value *ModelPriceSnapshotV1) {
			value.Pricing = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if _, _, err := NewModelPriceSnapshotV1(candidate); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}

	_, canonical, err := NewModelPriceSnapshotV1(base)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(
		canonical,
		[]byte(`"input":1`),
		[]byte(`"input":2`),
		1,
	)
	if _, err := RestoreModelPriceSnapshotV1(tampered); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}
