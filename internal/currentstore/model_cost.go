package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/deepseekcost"
)

// frozenModelEstimatedCost derives only the estimate supported by the exact
// frozen Provider and PriceSnapshot carried by an Attempt. The boolean is
// false for providers whose pricing semantics are not implemented here; those
// providers retain their existing NULL estimate rather than being assigned a
// fabricated zero.
func frozenModelEstimatedCost(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attempt ModelDispatchAttemptRecord,
	tokens corecontract.UsageTokens,
) (*string, bool, error) {
	if attempt.Provider != deepseekcost.ProviderDeepSeekV1 {
		return nil, false, nil
	}
	price, err := queryModelPriceSnapshot(
		ctx,
		queryer,
		attempt.PriceSnapshotID,
	)
	if err != nil {
		return nil, true, fmt.Errorf("restore frozen price snapshot: %w", err)
	}
	if price.Snapshot.Provider != attempt.Provider ||
		price.Snapshot.Model != attempt.Model ||
		price.Snapshot.BillingVersion != attempt.BillingVersion {
		return nil, true, fmt.Errorf("frozen price snapshot identity mismatch")
	}
	estimate, err := deepseekcost.Calculate(price.Snapshot, tokens)
	if err != nil {
		// ModelPriceSnapshot is intentionally provider-neutral. A historical or
		// future DeepSeek snapshot whose pricing document is not the exact
		// calculator schema remains explicitly unestimated instead of making the
		// already-observed model outcome uncommittable.
		if errors.Is(err, deepseekcost.ErrInvalidEstimate) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("derive frozen DeepSeek estimate: %w", err)
	}
	switch estimate.Status {
	case deepseekcost.StatusKnown:
		if estimate.Value == nil {
			return nil, true, fmt.Errorf("known frozen estimate has no value")
		}
		value := *estimate.Value
		return &value, true, nil
	case deepseekcost.StatusUnknown:
		if estimate.Value != nil {
			return nil, true, fmt.Errorf("unknown frozen estimate has a value")
		}
		return nil, true, nil
	default:
		return nil, true, fmt.Errorf(
			"unsupported frozen estimate status %q",
			estimate.Status,
		)
	}
}

func applyFrozenModelEstimatedCost(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	current ModelDispatchRecord,
	merged *ModelDispatchRecord,
) error {
	derived, supported, err := frozenModelEstimatedCost(
		ctx,
		queryer,
		current.Attempt,
		merged.Usage.Tokens,
	)
	if err != nil {
		return err
	}
	if !supported {
		return nil
	}
	if current.Usage.EstimatedCost != nil &&
		(derived == nil || *current.Usage.EstimatedCost != *derived) {
		return fmt.Errorf("known frozen estimate would be removed or changed")
	}
	merged.Usage.EstimatedCost = cloneModelString(derived)
	return nil
}

func validateFrozenModelEstimatedCost(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	record ModelDispatchRecord,
) error {
	derived, supported, err := frozenModelEstimatedCost(
		ctx,
		queryer,
		record.Attempt,
		record.Usage.Tokens,
	)
	if err != nil {
		return err
	}
	if supported && !equalModelStringPointer(
		record.Usage.EstimatedCost,
		derived,
	) {
		return fmt.Errorf("stored estimate differs from frozen derivation")
	}
	return nil
}
