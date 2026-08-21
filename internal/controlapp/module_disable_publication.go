package controlapp

import (
	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
)

const (
	ModuleDisablePublicationReceiptSchemaVersionV1 = moduledisablecontract.ModuleDisablePublicationReceiptSchemaVersionV1
	MaximumModuleDisablePublicationReceiptBytesV1  = moduledisablecontract.MaximumModuleDisablePublicationReceiptBytesV1
)

type ModuleDisablePublicationReceiptV1 = moduledisablecontract.ModuleDisablePublicationReceiptV1
type ModuleDisablePublicationRemovedBindingV1 = moduledisablecontract.ModuleDisablePublicationRemovedBindingV1

// NewModuleDisablePublicationReceiptV1 is the compatibility application facade
// over the sole stable domain receipt implementation.
func NewModuleDisablePublicationReceiptV1(
	input ModuleDisablePublicationReceiptV1,
) (
	ModuleDisablePublicationReceiptV1,
	[]byte,
	controlapicontract.DomainReceiptRefV1,
	error,
) {
	frozen, canonical, ref, err :=
		moduledisablecontract.NewModuleDisablePublicationReceiptV1(input)
	if err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, ErrInvalidRequest
	}
	return frozen, canonical, ref, nil
}

func RestoreModuleDisablePublicationReceiptV1(
	canonical []byte,
	expectedRef controlapicontract.DomainReceiptRefV1,
) (ModuleDisablePublicationReceiptV1, error) {
	restored, err := moduledisablecontract.RestoreModuleDisablePublicationReceiptV1(
		canonical,
		expectedRef,
	)
	if err != nil {
		return ModuleDisablePublicationReceiptV1{}, ErrInvalidRequest
	}
	return restored, nil
}
