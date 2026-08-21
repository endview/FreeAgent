package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestPutGetModelPriceSnapshotExactReplayAndDefensiveCopy(
	t *testing.T,
) {
	store := openContentTestStore(t)
	input := testModelPriceSnapshot()
	first, err := store.PutModelPriceSnapshot(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutModelPriceSnapshot(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.Digest != second.Snapshot.Digest ||
		string(first.CanonicalJSON) != string(second.CanonicalJSON) {
		t.Fatalf("replay drift first=%+v second=%+v", first, second)
	}

	first.CanonicalJSON[0] = 'x'
	first.Snapshot.Pricing[0] = 'x'
	stored, err := store.GetModelPriceSnapshot(
		context.Background(),
		input.PriceSnapshotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CanonicalJSON[0] != '{' || stored.Snapshot.Pricing[0] != '{' {
		t.Fatal("caller mutated stored model price snapshot")
	}
}

func TestPutModelPriceSnapshotRejectsIdentityConflict(t *testing.T) {
	store := openContentTestStore(t)
	input := testModelPriceSnapshot()
	if _, err := store.PutModelPriceSnapshot(
		context.Background(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	input.Pricing = json.RawMessage(`{"input_per_million":99}`)
	if _, err := store.PutModelPriceSnapshot(
		context.Background(),
		input,
	); !errors.Is(err, ErrModelIntegrity) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestPutModelPriceSnapshotPreservesCanceledContext(t *testing.T) {
	store := openContentTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.PutModelPriceSnapshot(
		ctx,
		testModelPriceSnapshot(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v", err)
	}
	if errors.Is(err, ErrModelIntegrity) {
		t.Fatalf("cancellation was misclassified as integrity: %v", err)
	}

	classified := modelPriceInsertError(context.Canceled)
	if !errors.Is(classified, context.Canceled) {
		t.Fatalf("insert cancellation lost root cause: %v", classified)
	}
	if errors.Is(classified, ErrModelIntegrity) {
		t.Fatalf("insert cancellation classified as integrity: %v", classified)
	}
}

func TestPutModelPriceSnapshotClassifiesConstraintAndPreservesCause(
	t *testing.T,
) {
	store := openContentTestStore(t)
	input := testModelPriceSnapshot()
	snapshot, canonical, err :=
		corecontract.NewModelPriceSnapshotV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO model_price_snapshots(
			price_snapshot_id,
			provider,
			model,
			billing_version,
			currency,
			pricing_status,
			canonical_json,
			digest
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"other-price-id",
		snapshot.Provider,
		snapshot.Model,
		snapshot.BillingVersion,
		snapshot.Currency,
		string(snapshot.PricingStatus),
		canonical,
		snapshot.Digest,
	); err != nil {
		t.Fatal(err)
	}
	_, err = store.PutModelPriceSnapshot(
		context.Background(),
		input,
	)
	if !errors.Is(err, ErrModelIntegrity) {
		t.Fatalf("constraint error=%v", err)
	}
	type sqliteCoder interface {
		Code() int
	}
	var cause sqliteCoder
	if !errors.As(err, &cause) {
		t.Fatalf("SQLite constraint cause was not preserved: %v", err)
	}
}

func TestGetModelPriceSnapshotRejectsProjectionCorruption(t *testing.T) {
	store := openContentTestStore(t)
	input := testModelPriceSnapshot()
	if _, err := store.PutModelPriceSnapshot(
		context.Background(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE model_price_snapshots
		SET currency='EUR'
		WHERE price_snapshot_id=?
	`, input.PriceSnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetModelPriceSnapshot(
		context.Background(),
		input.PriceSnapshotID,
	); !errors.Is(err, ErrModelIntegrity) {
		t.Fatalf("corruption error=%v", err)
	}
}

func TestModelPriceSnapshotLifecycleAndNotFound(t *testing.T) {
	store := openContentTestStore(t)
	if _, err := store.GetModelPriceSnapshot(
		context.Background(),
		"missing",
	); !errors.Is(err, ErrModelRecordNotFound) {
		t.Fatalf("not-found error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutModelPriceSnapshot(
		context.Background(),
		testModelPriceSnapshot(),
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed error=%v", err)
	}
}

func testModelPriceSnapshot() corecontract.ModelPriceSnapshotV1 {
	return corecontract.ModelPriceSnapshotV1{
		SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
		PriceSnapshotID: "price-deepseek-v4",
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		BillingVersion:  "2026-07",
		Currency:        "CNY",
		PricingStatus:   corecontract.PricingKnown,
		Pricing: json.RawMessage(
			`{"input_per_million":4,"output_per_million":16}`,
		),
	}
}
