package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrInvalidModelRecord = errors.New("currentstore: invalid model record")
	ErrModelIntegrity     = errors.New(
		"currentstore: model record integrity violation",
	)
	ErrModelRecordNotFound = errors.New(
		"currentstore: model record not found",
	)
)

// ModelPriceSnapshotRecord is an immutable, detached Current Store value.
type ModelPriceSnapshotRecord struct {
	Snapshot      corecontract.ModelPriceSnapshotV1
	CanonicalJSON []byte
}

// PutModelPriceSnapshot freezes and inserts one immutable price snapshot.
// Exact replay is idempotent; an existing identity with different bytes is an
// integrity failure. UNKNOWN pricing remains unknown and is never rewritten
// as a zero price.
func (store *Store) PutModelPriceSnapshot(
	ctx context.Context,
	input corecontract.ModelPriceSnapshotV1,
) (ModelPriceSnapshotRecord, error) {
	if ctx == nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelRecord,
		)
	}
	snapshot, canonical, err :=
		corecontract.NewModelPriceSnapshotV1(input)
	if err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidModelRecord,
			err,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModelPriceSnapshotRecord{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"currentstore: acquire model price connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"currentstore: begin PutModelPriceSnapshot: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	result, err := connection.ExecContext(ctx, `
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
		ON CONFLICT(price_snapshot_id) DO NOTHING
	`,
		snapshot.PriceSnapshotID,
		snapshot.Provider,
		snapshot.Model,
		snapshot.BillingVersion,
		snapshot.Currency,
		string(snapshot.PricingStatus),
		canonical,
		snapshot.Digest,
	)
	if err != nil {
		return ModelPriceSnapshotRecord{}, modelPriceInsertError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"currentstore: inspect model price insert: %w",
			err,
		)
	}
	if affected != 0 && affected != 1 {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: model price insert affected %d rows",
			ErrModelIntegrity,
			affected,
		)
	}

	stored, err := queryModelPriceSnapshot(
		ctx,
		connection,
		snapshot.PriceSnapshotID,
	)
	if err != nil {
		return ModelPriceSnapshotRecord{}, err
	}
	if stored.Snapshot.Digest != snapshot.Digest ||
		!bytes.Equal(stored.CanonicalJSON, canonical) {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: stored price snapshot %q differs",
			ErrModelIntegrity,
			snapshot.PriceSnapshotID,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"currentstore: commit PutModelPriceSnapshot: %w",
			err,
		)
	}
	committed = true
	return cloneModelPriceSnapshotRecord(stored), nil
}

func modelPriceInsertError(err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) &&
		sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf(
			"%w: insert model price snapshot: %w",
			ErrModelIntegrity,
			err,
		)
	}
	return fmt.Errorf(
		"currentstore: insert model price snapshot: %w",
		err,
	)
}

// GetModelPriceSnapshot restores and verifies one frozen price snapshot.
func (store *Store) GetModelPriceSnapshot(
	ctx context.Context,
	priceSnapshotID string,
) (ModelPriceSnapshotRecord, error) {
	if ctx == nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelRecord,
		)
	}
	if priceSnapshotID == "" {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: price snapshot ID is empty",
			ErrInvalidModelRecord,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModelPriceSnapshotRecord{}, err
	}
	defer unlock()
	record, err := queryModelPriceSnapshot(
		ctx,
		store.db,
		priceSnapshotID,
	)
	if err != nil {
		return ModelPriceSnapshotRecord{}, err
	}
	return cloneModelPriceSnapshotRecord(record), nil
}

func queryModelPriceSnapshot(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	priceSnapshotID string,
) (ModelPriceSnapshotRecord, error) {
	var (
		provider       string
		model          string
		billingVersion string
		currency       string
		pricingStatus  string
		canonical      []byte
		digest         string
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			provider,
			model,
			billing_version,
			currency,
			pricing_status,
			canonical_json,
			digest
		FROM model_price_snapshots
		WHERE price_snapshot_id=?
	`, priceSnapshotID).Scan(
		&provider,
		&model,
		&billingVersion,
		&currency,
		&pricingStatus,
		&canonical,
		&digest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: %s",
			ErrModelRecordNotFound,
			priceSnapshotID,
		)
	}
	if err != nil {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"currentstore: query model price snapshot: %w",
			err,
		)
	}
	snapshot, err :=
		corecontract.RestoreModelPriceSnapshotV1(canonical)
	if err != nil ||
		snapshot.PriceSnapshotID != priceSnapshotID ||
		snapshot.Provider != provider ||
		snapshot.Model != model ||
		snapshot.BillingVersion != billingVersion ||
		snapshot.Currency != currency ||
		string(snapshot.PricingStatus) != pricingStatus ||
		snapshot.Digest != digest {
		return ModelPriceSnapshotRecord{}, fmt.Errorf(
			"%w: price snapshot %q projections do not match canonical bytes",
			ErrModelIntegrity,
			priceSnapshotID,
		)
	}
	return ModelPriceSnapshotRecord{
		Snapshot:      snapshot,
		CanonicalJSON: bytes.Clone(canonical),
	}, nil
}

func cloneModelPriceSnapshotRecord(
	record ModelPriceSnapshotRecord,
) ModelPriceSnapshotRecord {
	record.Snapshot.Pricing = bytes.Clone(record.Snapshot.Pricing)
	record.CanonicalJSON = bytes.Clone(record.CanonicalJSON)
	return record
}
