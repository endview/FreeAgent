package moduledisablecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModuleDisablePublicationReceiptSchemaVersionV1 = "module-disable-publication-receipt/v1"
	MaximumModuleDisablePublicationReceiptBytesV1  = 128 << 10

	moduleDisablePublicationIDDomainV1      = "freeagent.module-disable-publication-id/v1"
	moduleDisablePublicationReceiptDomainV1 = "freeagent.module-disable-publication-receipt/v1"
)

// ModuleDisablePublicationReceiptV1 is identity-free immutable evidence for
// one adjacent MODULE_DISABLE publication. DisabledPlan is the exact narrow
// module-apply-plan/v1 object.
type ModuleDisablePublicationReceiptV1 struct {
	SchemaVersion  string                                   `json:"schema_version"`
	DisabledPlan   json.RawMessage                          `json:"disabled_plan"`
	PlanDigest     string                                   `json:"plan_digest"`
	PreBasis       controlapicontract.PublishedBasisRefV1   `json:"pre_basis"`
	PostBasis      controlapicontract.PublishedBasisRefV1   `json:"post_basis"`
	RemovedBinding ModuleDisablePublicationRemovedBindingV1 `json:"removed_binding"`
	CatalogChange  ModuleDisableCatalogChangeV1             `json:"catalog_change"`
}

// ModuleDisablePublicationRemovedBindingV1 is the complete allowlisted
// identity of the removed Profile Context binding.
type ModuleDisablePublicationRemovedBindingV1 struct {
	Target              ModuleBindingTargetV1   `json:"target"`
	InstanceID          string                  `json:"instance_id"`
	Port                moduleapi.PortRef       `json:"port"`
	PortBindingIndex    uint32                  `json:"port_binding_index"`
	ConfigRef           string                  `json:"config_ref"`
	AuthorityCeilingRef string                  `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy `json:"failure_policy"`
}

// NewModuleDisablePublicationReceiptV1 freezes one exact domain receipt. Its
// ID and digest derive from the same identity-free bytes under distinct fixed
// domains.
func NewModuleDisablePublicationReceiptV1(
	input ModuleDisablePublicationReceiptV1,
) (
	ModuleDisablePublicationReceiptV1,
	[]byte,
	controlapicontract.DomainReceiptRefV1,
	error,
) {
	if input.SchemaVersion != ModuleDisablePublicationReceiptSchemaVersionV1 ||
		len(input.DisabledPlan) == 0 ||
		len(input.DisabledPlan) > MaximumModuleDisablePublicationReceiptBytesV1 ||
		!moduleapi.ValidSHA256(input.PlanDigest) {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	plan, err := moduleapplyplan.RestoreProfileContextDisableV1(
		input.DisabledPlan,
		input.PlanDigest,
	)
	if err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	if _, _, _, err := controlapicontract.NewPublishedBasisRefV1(input.PreBasis); err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	if _, _, _, err := controlapicontract.NewPublishedBasisRefV1(input.PostBasis); err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(input.PlanDigest)
	if err != nil ||
		plan.TenantID != input.PreBasis.TenantID ||
		plan.TenantID != input.PostBasis.TenantID ||
		plan.ExpectedPointerRevision != input.PreBasis.PointerRevision ||
		!nextModuleDisableBasisV1(input.PreBasis, input.PostBasis) ||
		input.PostBasis.Control.ID != candidates.ControlSnapshotID ||
		input.PostBasis.Catalog.ID != candidates.CatalogGenerationID ||
		!validModuleDisablePublicationBindingV1(
			input.RemovedBinding,
			plan.BindingTarget.ProfileID,
			plan.InstanceID,
			plan.Port,
		) ||
		(input.CatalogChange != ModuleDisableCatalogRetainInstanceV1 &&
			input.CatalogChange != ModuleDisableCatalogRemoveInstanceV1) {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}

	frozen := cloneModuleDisablePublicationReceiptV1(input)
	canonical, err := canonicalModuleDisablePublicationReceiptV1(frozen)
	if err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	ref := controlapicontract.DomainReceiptRefV1{
		Kind: controlapicontract.DomainReceiptModuleDisableV1,
		ID: moduleapi.Digest(
			moduleDisablePublicationIDDomainV1,
			canonical,
		),
		Digest: moduleapi.Digest(
			moduleDisablePublicationReceiptDomainV1,
			canonical,
		),
	}
	if err := ref.Validate(); err != nil {
		return ModuleDisablePublicationReceiptV1{}, nil,
			controlapicontract.DomainReceiptRefV1{}, errInvalidContractV1
	}
	return frozen, canonical, ref, nil
}

// RestoreModuleDisablePublicationReceiptV1 restores only exact canonical
// bytes bound to the complete MODULE_DISABLE domain receipt reference.
func RestoreModuleDisablePublicationReceiptV1(
	canonical []byte,
	expectedRef controlapicontract.DomainReceiptRefV1,
) (ModuleDisablePublicationReceiptV1, error) {
	if len(canonical) == 0 ||
		len(canonical) > MaximumModuleDisablePublicationReceiptBytesV1 ||
		expectedRef.Kind != controlapicontract.DomainReceiptModuleDisableV1 ||
		expectedRef.Validate() != nil {
		return ModuleDisablePublicationReceiptV1{}, errInvalidContractV1
	}
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleDisablePublicationCanonicalLimitsV1(),
	)
	if err != nil || !bytes.Equal(normalized, canonical) ||
		moduleapi.Digest(moduleDisablePublicationIDDomainV1, canonical) != expectedRef.ID ||
		moduleapi.Digest(moduleDisablePublicationReceiptDomainV1, canonical) != expectedRef.Digest {
		return ModuleDisablePublicationReceiptV1{}, errInvalidContractV1
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.Clone(canonical)))
	decoder.DisallowUnknownFields()
	var decoded ModuleDisablePublicationReceiptV1
	if err := decoder.Decode(&decoded); err != nil {
		return ModuleDisablePublicationReceiptV1{}, errInvalidContractV1
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ModuleDisablePublicationReceiptV1{}, errInvalidContractV1
	}
	restored, rebuilt, ref, err := NewModuleDisablePublicationReceiptV1(decoded)
	if err != nil || ref != expectedRef || !bytes.Equal(rebuilt, canonical) {
		return ModuleDisablePublicationReceiptV1{}, errInvalidContractV1
	}
	return restored, nil
}

func canonicalModuleDisablePublicationReceiptV1(
	value ModuleDisablePublicationReceiptV1,
) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleDisablePublicationCanonicalLimitsV1(),
	)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return nil, errInvalidContractV1
	}
	return bytes.Clone(canonical), nil
}

func moduleDisablePublicationCanonicalLimitsV1() moduleapi.CanonicalJSONLimits {
	return moduleapi.CanonicalJSONLimits{
		MaxBytes: MaximumModuleDisablePublicationReceiptBytesV1,
		MaxDepth: 32,
		MaxNodes: 2048,
	}
}

func validModuleDisablePublicationBindingV1(
	binding ModuleDisablePublicationRemovedBindingV1,
	profileID string,
	instanceID string,
	port moduleapi.PortRef,
) bool {
	projectionBinding := ModuleDisableBindingRemovalV1{
		Target:              binding.Target,
		Port:                binding.Port,
		PortBindingIndex:    binding.PortBindingIndex,
		ConfigRef:           binding.ConfigRef,
		AuthorityCeilingRef: binding.AuthorityCeilingRef,
		StaticContextRefs:   binding.StaticContextRefs,
		FailurePolicy:       binding.FailurePolicy,
	}
	return validModuleInstanceIDV1(binding.InstanceID) &&
		binding.InstanceID == instanceID &&
		validModuleDisableBindingRemovalV1(projectionBinding) &&
		binding.Target == (ModuleBindingTargetV1{
			Kind:      ModuleBindingTargetProfileV1,
			ProfileID: profileID,
		}) &&
		binding.Port == port
}

func cloneModuleDisablePublicationReceiptV1(
	input ModuleDisablePublicationReceiptV1,
) ModuleDisablePublicationReceiptV1 {
	cloned := input
	cloned.DisabledPlan = bytes.Clone(input.DisabledPlan)
	cloned.RemovedBinding.StaticContextRefs = append(
		[]string{},
		input.RemovedBinding.StaticContextRefs...,
	)
	return cloned
}
