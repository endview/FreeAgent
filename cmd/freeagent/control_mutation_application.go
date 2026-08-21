package main

import (
	"bytes"
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controlmutation"
)

// productionModuleDisableControlServiceV1 is the transport-neutral adapter
// over the one mutation coordinator. It owns no Store, proof authority, or
// lifecycle and is shared by the confirmation and mutation HTTP services.
type productionModuleDisableControlServiceV1 struct {
	coordinator *controlmutation.CoordinatorV1
}

func (service *productionModuleDisableControlServiceV1) IssueModuleDisableConfirmationV1(
	ctx context.Context,
	input controlhttp.IssueModuleDisableConfirmationInputV1,
) (controlhttp.IssueModuleDisableConfirmationResultV1, error) {
	if service == nil || service.coordinator == nil {
		return controlhttp.IssueModuleDisableConfirmationResultV1{},
			controlapp.ErrStoreUnavailable
	}
	result, err := service.coordinator.IssueModuleDisableConfirmationV1(
		ctx,
		controlmutation.IssueModuleDisableConfirmationInputV1{
			Authorization:        input.Authorization,
			Scope:                input.Scope,
			ExpectedPointer:      input.ExpectedPointer,
			Body:                 input.Body,
			IdempotencyKeyDigest: input.IdempotencyKeyDigest,
		},
	)
	defer clear(result.Proof)
	if err != nil {
		return controlhttp.IssueModuleDisableConfirmationResultV1{},
			mapControlMutationErrorV1(err)
	}
	return controlhttp.IssueModuleDisableConfirmationResultV1{
		SchemaVersion:       controlhttp.ModuleDisableConfirmationResultSchemaVersionV1,
		Request:             result.Request,
		RequestDigest:       result.RequestDigest,
		Evaluation:          result.Evaluation,
		EvaluationDigest:    result.EvaluationDigest,
		Statement:           result.Statement,
		StatementDigest:     result.StatementDigest,
		ConfirmationProof:   bytes.Clone(result.Proof),
		ExpiresAtUnixMicros: result.ExpiresAtUnixMicros,
	}, nil
}

func (service *productionModuleDisableControlServiceV1) MutateModuleDisableV1(
	ctx context.Context,
	input controlhttp.MutateModuleDisableInputV1,
) (controlhttp.MutateModuleDisableResultV1, error) {
	if service == nil || service.coordinator == nil {
		return controlhttp.MutateModuleDisableResultV1{},
			controlapp.ErrStoreUnavailable
	}
	proof := bytes.Clone(input.ConfirmationProof)
	defer clear(proof)
	result, err := service.coordinator.MutateModuleDisableV1(
		ctx,
		controlmutation.MutateModuleDisableInputV1{
			Authorization:             input.Authorization,
			Scope:                     input.Scope,
			ExpectedPointer:           input.ExpectedPointer,
			Body:                      input.Body,
			IdempotencyKeyDigest:      input.IdempotencyKeyDigest,
			OperationEvaluationDigest: input.OperationEvaluationDigest,
			Proof:                     proof,
		},
	)
	if err != nil {
		return controlhttp.MutateModuleDisableResultV1{},
			mapControlMutationErrorV1(err)
	}
	return controlhttp.MutateModuleDisableResultV1{
		SchemaVersion: controlhttp.ModuleDisableMutationResultSchemaVersionV1,
		Request:       result.Request,
		RequestDigest: result.RequestDigest,
		Receipt:       result.Receipt,
		ReceiptDigest: result.ReceiptDigest,
	}, nil
}

func mapControlMutationErrorV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, controlmutation.ErrInvalidRequest):
		return controlapp.ErrInvalidRequest
	case errors.Is(err, controlmutation.ErrSessionExpired):
		return controlapp.ErrSessionExpired
	case errors.Is(err, controlmutation.ErrForbidden):
		return controlapp.ErrForbidden
	case errors.Is(err, controlmutation.ErrRevisionConflict):
		return controlapp.ErrRevisionConflict
	case errors.Is(err, controlmutation.ErrIdempotencyConflict):
		return controlhttp.ErrModuleDisableIdempotencyConflictV1
	case errors.Is(err, controlmutation.ErrConfirmationRejected):
		return controlhttp.ErrModuleDisableConfirmationRejectedV1
	case errors.Is(err, controlmutation.ErrMutationIneligible):
		return controlapp.ErrConflict
	case errors.Is(err, controlmutation.ErrResourceExhausted):
		return controlapp.ErrResourceExhausted
	case errors.Is(err, controlmutation.ErrStoreBusy):
		return controlapp.ErrStoreBusy
	case errors.Is(err, controlmutation.ErrStoreUnavailable),
		errors.Is(err, controlmutation.ErrInvalidConfiguration):
		return controlapp.ErrStoreUnavailable
	case errors.Is(err, controlmutation.ErrIntegrityFailure):
		return controlapp.ErrIntegrityFailure
	case errors.Is(err, controlmutation.ErrCancelled),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return controlapp.ErrCancelled
	default:
		return controlapp.ErrStoreUnavailable
	}
}

var _ controlhttp.ModuleDisableConfirmationServiceV1 = (*productionModuleDisableControlServiceV1)(nil)
var _ controlhttp.ModuleDisableMutationServiceV1 = (*productionModuleDisableControlServiceV1)(nil)
