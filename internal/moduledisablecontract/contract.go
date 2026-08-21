// Package moduledisablecontract owns the pure, stable wire contracts shared by
// MODULE_DISABLE application, Store, and Backup boundaries. It performs no
// Store, filesystem, clock, session, proof, provider, process, or network I/O.
package moduledisablecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModuleDisableDryRunBodySchemaVersionV1 = "control-module-disable-dry-run-input/v1"
	ModuleDisableEvaluationSchemaVersionV1 = "control-module-disable-evaluation/v1"
	ModuleDisableProjectedNotReservedV1    = "PROJECTED_NOT_RESERVED"

	MaximumModuleDisableDryRunBodyBytesV1 = 1 << 20
	MaximumModuleDisableEvaluationBytesV1 = 64 << 10

	ModuleDisableDryRunInputDigestDomainV1 = "freeagent.control-module-disable-dry-run-input/v1"

	moduleDisableEvaluationDigestDomainV1 = "freeagent.control-module-disable-evaluation/v1"
)

type ModuleBindingTargetKindV1 string

const (
	ModuleBindingTargetProfileV1                  ModuleBindingTargetKindV1 = "PROFILE"
	ModuleBindingTargetWorkspaceChannelEndpointV1 ModuleBindingTargetKindV1 = "WORKSPACE_CHANNEL_ENDPOINT"
)

// ModuleBindingTargetV1 identifies only the consumer-owned target. A PROFILE
// deliberately has no inferred Workspace ownership.
type ModuleBindingTargetV1 struct {
	Kind        ModuleBindingTargetKindV1 `json:"kind"`
	ProfileID   string                    `json:"profile_id,omitempty"`
	WorkspaceID string                    `json:"workspace_id,omitempty"`
	EndpointID  string                    `json:"endpoint_id,omitempty"`
}

// ModuleDisableDryRunBodyV1 is the complete caller-selected semantic input.
// Principal, capability, scope, intent, and the Published Pointer digest are
// injected by the authenticated transport/application boundary and cannot be
// self-asserted in this wire.
type ModuleDisableDryRunBodyV1 struct {
	SchemaVersion           string                `json:"schema_version"`
	ExpectedPointerRevision uint64                `json:"expected_pointer_revision"`
	BindingTarget           ModuleBindingTargetV1 `json:"binding_target"`
	InstanceID              string                `json:"instance_id"`
	Port                    moduleapi.PortRef     `json:"port"`
}

type ModuleDisableDispositionV1 string

const (
	ModuleDisableAlreadyAppliedV1 ModuleDisableDispositionV1 = "ALREADY_APPLIED"
	ModuleDisableNoChangeV1       ModuleDisableDispositionV1 = "NO_CHANGE"
	ModuleDisableWouldApplyV1     ModuleDisableDispositionV1 = "WOULD_APPLY"
)

type ModuleDisableCatalogChangeV1 string

const (
	ModuleDisableCatalogNoneV1           ModuleDisableCatalogChangeV1 = "NONE"
	ModuleDisableCatalogRetainInstanceV1 ModuleDisableCatalogChangeV1 = "RETAIN_INSTANCE"
	ModuleDisableCatalogRemoveInstanceV1 ModuleDisableCatalogChangeV1 = "REMOVE_INSTANCE"
)

// ModuleDisableProjectionV1 contains only inert references and allowlisted
// binding metadata. It never carries candidate Control/Catalog canonical
// bytes, an artifact path, or a reservation/grant.
type ModuleDisableProjectionV1 struct {
	Disposition       ModuleDisableDispositionV1             `json:"disposition"`
	PlanDigest        string                                 `json:"plan_digest"`
	InstanceID        string                                 `json:"instance_id"`
	PreconditionBasis controlapicontract.PublishedBasisRefV1 `json:"precondition_basis"`
	ObservedBasis     controlapicontract.PublishedBasisRefV1 `json:"observed_basis"`
	CandidateBasis    controlapicontract.PublishedBasisRefV1 `json:"candidate_basis"`
	CandidateState    string                                 `json:"candidate_state"`
	BindingRemoval    *ModuleDisableBindingRemovalV1         `json:"binding_removal,omitempty"`
	CatalogChange     ModuleDisableCatalogChangeV1           `json:"catalog_change"`
}

type ModuleDisableBindingRemovalV1 struct {
	Target              ModuleBindingTargetV1   `json:"target"`
	Port                moduleapi.PortRef       `json:"port"`
	PortBindingIndex    uint32                  `json:"port_binding_index"`
	ConfigRef           string                  `json:"config_ref"`
	AuthorityCeilingRef string                  `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy `json:"failure_policy"`
}

// ModuleDisableEvaluationV1 is the stable, effect-free candidate confirmed
// before the first narrowly governed MODULE_DISABLE MUTATE request. It is
// limited to an OPTIONAL Profile context.provide/v1 binding and excludes
// request/receipt times, session/Boot identity, and raw confirmation material.
type ModuleDisableEvaluationV1 struct {
	SchemaVersion string                                   `json:"schema_version"`
	Operation     controlapicontract.ControlOperationV1    `json:"operation"`
	InputDigest   string                                   `json:"input_digest"`
	ExpectedRef   controlapicontract.ExpectedResourceRefV1 `json:"expected_ref"`
	Projection    ModuleDisableProjectionV1                `json:"projection"`
}

// NewModuleDisableDryRunBodyV1 freezes one exact typed operation input.
func NewModuleDisableDryRunBodyV1(
	input ModuleDisableDryRunBodyV1,
) (ModuleDisableDryRunBodyV1, []byte, string, error) {
	if input.SchemaVersion != ModuleDisableDryRunBodySchemaVersionV1 ||
		!validPositiveJSONIntegerV1(input.ExpectedPointerRevision) ||
		!validModuleInstanceIDV1(input.InstanceID) {
		return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
	}
	if err := input.Port.Validate(); err != nil {
		return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
	}
	switch input.BindingTarget.Kind {
	case ModuleBindingTargetProfileV1:
		if !validOpaqueIDV1(input.BindingTarget.ProfileID) ||
			input.BindingTarget.WorkspaceID != "" || input.BindingTarget.EndpointID != "" {
			return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
		}
	case ModuleBindingTargetWorkspaceChannelEndpointV1:
		if input.BindingTarget.ProfileID != "" ||
			!validOpaqueIDV1(input.BindingTarget.WorkspaceID) ||
			!validOpaqueIDV1(input.BindingTarget.EndpointID) ||
			input.Port != (moduleapi.PortRef{
				Name:         moduleapi.PortNameChannelTransport,
				ExactVersion: moduleapi.PortVersionV1,
			}) {
			return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
		}
	default:
		return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
	}
	canonical, digest, err := canonicalDigestV1(
		ModuleDisableDryRunInputDigestDomainV1,
		input,
		MaximumModuleDisableDryRunBodyBytesV1,
		128,
	)
	if err != nil {
		return ModuleDisableDryRunBodyV1{}, nil, "", errInvalidContractV1
	}
	return input, canonical, digest, nil
}

// RestoreModuleDisableDryRunBodyV1 accepts only the exact canonical input and
// its domain-separated digest.
func RestoreModuleDisableDryRunBodyV1(
	canonical []byte,
	expectedDigest string,
) (ModuleDisableDryRunBodyV1, error) {
	if len(canonical) == 0 || len(canonical) > MaximumModuleDisableDryRunBodyBytesV1 ||
		!moduleapi.ValidSHA256(expectedDigest) {
		return ModuleDisableDryRunBodyV1{}, errInvalidContractV1
	}
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumModuleDisableDryRunBodyBytesV1,
			MaxDepth: 16,
			MaxNodes: 128,
		},
	)
	if err != nil || !bytes.Equal(normalized, canonical) ||
		moduleapi.Digest(ModuleDisableDryRunInputDigestDomainV1, canonical) != expectedDigest {
		return ModuleDisableDryRunBodyV1{}, errInvalidContractV1
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var decoded ModuleDisableDryRunBodyV1
	if err := decoder.Decode(&decoded); err != nil {
		return ModuleDisableDryRunBodyV1{}, errInvalidContractV1
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ModuleDisableDryRunBodyV1{}, errInvalidContractV1
	}
	frozen, rebuilt, digest, err := NewModuleDisableDryRunBodyV1(decoded)
	if err != nil || digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ModuleDisableDryRunBodyV1{}, errInvalidContractV1
	}
	return frozen, nil
}

// NewModuleDisableEvaluationV1 freezes one exact stable evaluation.
func NewModuleDisableEvaluationV1(
	input ModuleDisableEvaluationV1,
) (ModuleDisableEvaluationV1, []byte, string, error) {
	if input.SchemaVersion != ModuleDisableEvaluationSchemaVersionV1 ||
		input.Operation != controlapicontract.OperationModuleDisableV1 ||
		!moduleapi.ValidSHA256(input.InputDigest) ||
		input.ExpectedRef.Kind != controlapicontract.ResourcePublishedPointerV1 ||
		input.ExpectedRef.Validate() != nil ||
		!validModuleDisableEvaluationProjectionV1(input.Projection, input.ExpectedRef) {
		return ModuleDisableEvaluationV1{}, nil, "", errInvalidContractV1
	}
	frozen := input
	frozen.Projection = cloneModuleDisableProjectionV1(input.Projection)
	canonical, digest, err := canonicalDigestV1(
		moduleDisableEvaluationDigestDomainV1,
		frozen,
		MaximumModuleDisableEvaluationBytesV1,
		1<<20,
	)
	if err != nil {
		return ModuleDisableEvaluationV1{}, nil, "", errInvalidContractV1
	}
	return frozen, canonical, digest, nil
}

// RestoreModuleDisableEvaluationV1 accepts only the exact canonical
// evaluation and its domain-separated digest.
func RestoreModuleDisableEvaluationV1(
	canonical []byte,
	expectedDigest string,
) (ModuleDisableEvaluationV1, error) {
	if len(canonical) == 0 || len(canonical) > MaximumModuleDisableEvaluationBytesV1 ||
		!moduleapi.ValidSHA256(expectedDigest) {
		return ModuleDisableEvaluationV1{}, errInvalidContractV1
	}
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumModuleDisableEvaluationBytesV1,
			MaxDepth: 32,
			MaxNodes: 1 << 20,
		},
	)
	if err != nil || !bytes.Equal(normalized, canonical) ||
		moduleapi.Digest(moduleDisableEvaluationDigestDomainV1, canonical) != expectedDigest {
		return ModuleDisableEvaluationV1{}, errInvalidContractV1
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var decoded ModuleDisableEvaluationV1
	if err := decoder.Decode(&decoded); err != nil {
		return ModuleDisableEvaluationV1{}, errInvalidContractV1
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ModuleDisableEvaluationV1{}, errInvalidContractV1
	}
	frozen, rebuilt, digest, err := NewModuleDisableEvaluationV1(decoded)
	if err != nil || digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ModuleDisableEvaluationV1{}, errInvalidContractV1
	}
	return frozen, nil
}

func validModuleDisableEvaluationProjectionV1(
	projection ModuleDisableProjectionV1,
	expected controlapicontract.ExpectedResourceRefV1,
) bool {
	if !moduleapi.ValidSHA256(projection.PlanDigest) ||
		!validModuleInstanceIDV1(projection.InstanceID) ||
		projection.CandidateState != ModuleDisableProjectedNotReservedV1 ||
		projection.PreconditionBasis.Validate() != nil ||
		projection.ObservedBasis.Validate() != nil ||
		projection.CandidateBasis.Validate() != nil ||
		projection.PreconditionBasis.TenantID != expected.ResourceID ||
		projection.ObservedBasis.TenantID != expected.ResourceID ||
		projection.CandidateBasis.TenantID != expected.ResourceID {
		return false
	}
	precondition, err := publishedPointerRefV1(projection.PreconditionBasis)
	if err != nil || precondition != expected {
		return false
	}
	switch projection.Disposition {
	case ModuleDisableNoChangeV1:
		return projection.PreconditionBasis == projection.ObservedBasis &&
			projection.ObservedBasis == projection.CandidateBasis &&
			projection.CatalogChange == ModuleDisableCatalogNoneV1 &&
			projection.BindingRemoval == nil
	case ModuleDisableAlreadyAppliedV1:
		return nextModuleDisableBasisV1(
			projection.PreconditionBasis,
			projection.ObservedBasis,
		) && projection.ObservedBasis == projection.CandidateBasis &&
			projection.CatalogChange == ModuleDisableCatalogNoneV1 &&
			projection.BindingRemoval == nil
	case ModuleDisableWouldApplyV1:
		if projection.PreconditionBasis != projection.ObservedBasis ||
			!nextModuleDisableBasisV1(projection.ObservedBasis, projection.CandidateBasis) ||
			projection.BindingRemoval == nil ||
			(projection.CatalogChange != ModuleDisableCatalogRetainInstanceV1 &&
				projection.CatalogChange != ModuleDisableCatalogRemoveInstanceV1) {
			return false
		}
		return validModuleDisableBindingRemovalV1(*projection.BindingRemoval)
	default:
		return false
	}
}

func nextModuleDisableBasisV1(
	before controlapicontract.PublishedBasisRefV1,
	after controlapicontract.PublishedBasisRefV1,
) bool {
	return before.TenantID == after.TenantID &&
		after.PointerRevision == before.PointerRevision+1 &&
		after.Control.Revision == before.Control.Revision+1 &&
		after.Catalog.Revision == before.Catalog.Revision+1 &&
		after.Control.ID != before.Control.ID &&
		after.Control.Digest != before.Control.Digest &&
		after.Catalog.ID != before.Catalog.ID &&
		after.Catalog.Digest != before.Catalog.Digest
}

func validModuleDisableBindingRemovalV1(binding ModuleDisableBindingRemovalV1) bool {
	if binding.Port.Validate() != nil || binding.FailurePolicy.Validate() != nil ||
		binding.Port != (moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		}) || binding.FailurePolicy != moduleapi.FailureOptional ||
		!moduleapi.ValidSHA256(binding.ConfigRef) ||
		!moduleapi.ValidSHA256(binding.AuthorityCeilingRef) {
		return false
	}
	if binding.Target.Kind != ModuleBindingTargetProfileV1 ||
		!validOpaqueIDV1(binding.Target.ProfileID) ||
		binding.Target.WorkspaceID != "" || binding.Target.EndpointID != "" ||
		binding.PortBindingIndex >= uint32(moduleapi.MaxManifestEntries) {
		return false
	}
	if len(binding.StaticContextRefs) > moduleapi.MaxManifestEntries {
		return false
	}
	seen := make(map[string]struct{}, len(binding.StaticContextRefs))
	for _, reference := range binding.StaticContextRefs {
		if !moduleapi.ValidSHA256(reference) {
			return false
		}
		if _, duplicate := seen[reference]; duplicate {
			return false
		}
		seen[reference] = struct{}{}
	}
	return true
}

func cloneModuleDisableProjectionV1(input ModuleDisableProjectionV1) ModuleDisableProjectionV1 {
	cloned := input
	if input.BindingRemoval != nil {
		binding := *input.BindingRemoval
		binding.StaticContextRefs = append([]string{}, input.BindingRemoval.StaticContextRefs...)
		cloned.BindingRemoval = &binding
	}
	return cloned
}

func publishedPointerRefV1(
	basis controlapicontract.PublishedBasisRefV1,
) (controlapicontract.ExpectedResourceRefV1, error) {
	_, _, digest, err := controlapicontract.NewPublishedBasisRefV1(basis)
	if err != nil {
		return controlapicontract.ExpectedResourceRefV1{}, err
	}
	ref := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: basis.TenantID,
		Revision:   basis.PointerRevision,
		Digest:     digest,
	}
	if err := ref.Validate(); err != nil {
		return controlapicontract.ExpectedResourceRefV1{}, err
	}
	return ref, nil
}

func canonicalDigestV1(
	domain string,
	value any,
	maximumBytes int,
	maximumNodes int,
) ([]byte, string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumBytes,
			MaxDepth: 32,
			MaxNodes: maximumNodes,
		},
	)
	if err != nil || len(canonical) == 0 || len(canonical) > maximumBytes ||
		canonical[0] != '{' {
		return nil, "", errInvalidContractV1
	}
	return bytes.Clone(canonical), moduleapi.Digest(domain, canonical), nil
}

func validOpaqueIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validModuleInstanceIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validPositiveJSONIntegerV1(value uint64) bool {
	return value > 0 && value <= uint64(1<<53-1) && value <= math.MaxInt64
}

var errInvalidContractV1 = errors.New("moduledisablecontract: invalid contract")
