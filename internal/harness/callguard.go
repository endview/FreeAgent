package harness

import (
	"context"
	"database/sql"
	"errors"
)

// Reservation and dispatch share one transaction. A composition cannot send a
// call first and account for its budget later.
type callGuard interface {
	Reserve(context.Context, *sql.Tx, Attempt) error
	Record(context.Context, *sql.Tx, Attempt, int, bool) error
}

func isOutputLimit(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if len(joined.Unwrap()) == 0 {
			return false
		}
		for _, child := range joined.Unwrap() {
			if !isOutputLimit(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return isOutputLimit(wrapped.Unwrap())
	}
	return err == ErrChildBudget || err == ErrParentBudget
}

func (service *Service) recordOutput(ctx context.Context, attempt Attempt, size int, final bool) error {
	if service.guard == nil {
		return nil
	}
	tx, err := service.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	recordErr := service.guard.Record(ctx, tx, attempt, size, final)
	// Overruns are retained even when the caller has to stop. Rolling them back
	// would let failed or interrupted calls escape cumulative accounting.
	return errors.Join(recordErr, tx.Commit())
}
