package controlapp

import (
	"fmt"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
)

const (
	ModuleDisableDryRunBodySchemaVersionV1   = moduledisablecontract.ModuleDisableDryRunBodySchemaVersionV1
	ModuleDisableDryRunResultSchemaVersionV1 = "control-module-disable-dry-run-result/v1"
	ModuleDisableEvaluationSchemaVersionV1   = moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1
	ModuleDisableProjectedNotReservedV1      = moduledisablecontract.ModuleDisableProjectedNotReservedV1
	MaximumModuleDisableDryRunBodyBytesV1    = moduledisablecontract.MaximumModuleDisableDryRunBodyBytesV1
	MaximumModuleDisableEvaluationBytesV1    = moduledisablecontract.MaximumModuleDisableEvaluationBytesV1
)

type ModuleDisableDryRunBodyV1 = moduledisablecontract.ModuleDisableDryRunBodyV1

// ModuleDisableDryRunInputV1 is process-local application input. Authorization
// must be one immutable permit returned by the session registry.
type ModuleDisableDryRunInputV1 struct {
	Authorization   AuthorizationContextV1
	Scope           controlapicontract.ControlScopeV1
	ExpectedPointer controlapicontract.ExpectedResourceRefV1
	Body            ModuleDisableDryRunBodyV1
	ObservedAt      uint64
}

type ModuleDisableDispositionV1 = moduledisablecontract.ModuleDisableDispositionV1

const (
	ModuleDisableAlreadyAppliedV1 = moduledisablecontract.ModuleDisableAlreadyAppliedV1
	ModuleDisableNoChangeV1       = moduledisablecontract.ModuleDisableNoChangeV1
	ModuleDisableWouldApplyV1     = moduledisablecontract.ModuleDisableWouldApplyV1
)

type ModuleDisableCatalogChangeV1 = moduledisablecontract.ModuleDisableCatalogChangeV1

const (
	ModuleDisableCatalogNoneV1           = moduledisablecontract.ModuleDisableCatalogNoneV1
	ModuleDisableCatalogRetainInstanceV1 = moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
	ModuleDisableCatalogRemoveInstanceV1 = moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
)

type ModuleDisableProjectionV1 = moduledisablecontract.ModuleDisableProjectionV1
type ModuleDisableBindingRemovalV1 = moduledisablecontract.ModuleDisableBindingRemovalV1
type ModuleDisableEvaluationV1 = moduledisablecontract.ModuleDisableEvaluationV1

type ModuleDisableDryRunResultV1 struct {
	SchemaVersion string                                       `json:"schema_version"`
	Request       controlapicontract.ControlOperationRequestV1 `json:"request"`
	RequestDigest string                                       `json:"request_digest"`
	Receipt       controlapicontract.ControlOperationReceiptV1 `json:"receipt"`
	ReceiptDigest string                                       `json:"receipt_digest"`
	Projection    ModuleDisableProjectionV1                    `json:"projection"`
}

// NewModuleDisableDryRunBodyV1 is the compatibility application facade over
// the sole stable wire implementation in moduledisablecontract.
func NewModuleDisableDryRunBodyV1(
	input ModuleDisableDryRunBodyV1,
) (ModuleDisableDryRunBodyV1, []byte, string, error) {
	frozen, canonical, digest, err :=
		moduledisablecontract.NewModuleDisableDryRunBodyV1(input)
	if err != nil {
		return ModuleDisableDryRunBodyV1{}, nil, "", ErrInvalidRequest
	}
	return frozen, canonical, digest, nil
}

func RestoreModuleDisableDryRunBodyV1(
	canonical []byte,
	expectedDigest string,
) (ModuleDisableDryRunBodyV1, error) {
	restored, err := moduledisablecontract.RestoreModuleDisableDryRunBodyV1(
		canonical,
		expectedDigest,
	)
	if err != nil {
		return ModuleDisableDryRunBodyV1{}, ErrInvalidRequest
	}
	return restored, nil
}

// NewModuleDisableEvaluationV1 is the compatibility application facade over
// the sole stable wire implementation in moduledisablecontract.
func NewModuleDisableEvaluationV1(
	input ModuleDisableEvaluationV1,
) (ModuleDisableEvaluationV1, []byte, string, error) {
	frozen, canonical, digest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(input)
	if err != nil {
		return ModuleDisableEvaluationV1{}, nil, "", ErrInvalidRequest
	}
	return frozen, canonical, digest, nil
}

func RestoreModuleDisableEvaluationV1(
	canonical []byte,
	expectedDigest string,
) (ModuleDisableEvaluationV1, error) {
	restored, err := moduledisablecontract.RestoreModuleDisableEvaluationV1(
		canonical,
		expectedDigest,
	)
	if err != nil {
		return ModuleDisableEvaluationV1{}, ErrInvalidRequest
	}
	return restored, nil
}

// NewModuleDisableDryRunReceiptV1 constructs an effect-free receipt only from
// one exact frozen request. Repeating principal/scope/operation fields cannot
// drift independently from RequestDigest.
func NewModuleDisableDryRunReceiptV1(
	requestCanonical []byte,
	requestDigest string,
	completedAtUnixMicros uint64,
) (controlapicontract.ControlOperationReceiptV1, []byte, string, error) {
	request, err := controlapicontract.RestoreControlOperationRequestV1(
		requestCanonical,
		requestDigest,
	)
	if err != nil || request.Operation != controlapicontract.OperationModuleDisableV1 ||
		request.Intent != controlapicontract.OperationIntentDryRunV1 {
		return controlapicontract.ControlOperationReceiptV1{}, nil, "", ErrIntegrityFailure
	}
	precondition := request.ExpectedRef
	receipt, canonical, digest, err := controlapicontract.NewControlOperationReceiptV1(
		controlapicontract.ControlOperationReceiptV1{
			SchemaVersion:         controlapicontract.ControlOperationReceiptSchemaVersionV1,
			RequestDigest:         requestDigest,
			Intent:                request.Intent,
			PrincipalID:           request.PrincipalID,
			ScopeDigest:           request.ScopeDigest,
			Operation:             request.Operation,
			Status:                controlapicontract.OperationStatusDryRunV1,
			ErrorCode:             controlapicontract.ErrorNoneV1,
			PreRef:                &precondition,
			ReplayDisposition:     controlapicontract.ReplayNoRetryV1,
			CompletedAtUnixMicros: completedAtUnixMicros,
		},
	)
	if err != nil {
		return controlapicontract.ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
			"%w: construct module Disable Dry-run receipt",
			ErrIntegrityFailure,
		)
	}
	return receipt, canonical, digest, nil
}
