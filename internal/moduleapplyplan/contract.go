// Package moduleapplyplan owns the narrow, inert wire contract for the first
// Profile Context Disable slice. It performs no Store, filesystem, recovery,
// provider, module, process, clock, randomness, or network operation.
package moduleapplyplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	SchemaVersionV1 = "module-apply-plan/v1"
	DigestDomainV1  = "freeagent.module-apply-plan/v1"

	MaxCanonicalBytesV1 = 8 << 10

	CandidateControlSnapshotIDPrefixV1   = "module-apply-control-v1-"
	CandidateCatalogGenerationIDPrefixV1 = "module-apply-catalog-v1-"
)

type DesiredStateV1 string

const DesiredStateDisabledV1 DesiredStateV1 = "DISABLED"

type BindingTargetKindV1 string

const BindingTargetProfileV1 BindingTargetKindV1 = "PROFILE"

// ProfileContextDisableInputV1 contains the only caller-selected values in
// this narrow plan. FreezeProfileContextDisableV1 supplies all invariant wire
// values itself.
type ProfileContextDisableInputV1 struct {
	TenantID                string
	ExpectedPointerRevision uint64
	ProfileID               string
	InstanceID              string
}

type ProfileContextDisableBindingTargetV1 struct {
	Kind      BindingTargetKindV1 `json:"kind"`
	ProfileID string              `json:"profile_id"`
}

// ProfileContextDisablePlanV1 is the exact seven-field closed wire shape for
// DISABLED, PROFILE, context.provide/v1. It grants no authority and is not a
// mutation instruction by itself.
type ProfileContextDisablePlanV1 struct {
	SchemaVersion           string                               `json:"schema_version"`
	DesiredState            DesiredStateV1                       `json:"desired_state"`
	TenantID                string                               `json:"tenant_id"`
	ExpectedPointerRevision uint64                               `json:"expected_pointer_revision"`
	BindingTarget           ProfileContextDisableBindingTargetV1 `json:"binding_target"`
	InstanceID              string                               `json:"instance_id"`
	Port                    moduleapi.PortRef                    `json:"port"`
}

// CandidateIDsV1 are the two deterministic names already used by the broad
// Apply implementation. They are derived only from an exact plan digest.
type CandidateIDsV1 struct {
	ControlSnapshotID   string
	CatalogGenerationID string
}

type profileContextDisablePlanWireV1 struct {
	SchemaVersion           *string                                   `json:"schema_version"`
	DesiredState            *DesiredStateV1                           `json:"desired_state"`
	TenantID                *string                                   `json:"tenant_id"`
	ExpectedPointerRevision *uint64                                   `json:"expected_pointer_revision"`
	BindingTarget           *profileContextDisableBindingTargetWireV1 `json:"binding_target"`
	InstanceID              *string                                   `json:"instance_id"`
	Port                    *profileContextDisablePortWireV1          `json:"port"`
}

type profileContextDisableBindingTargetWireV1 struct {
	Kind      *BindingTargetKindV1 `json:"kind"`
	ProfileID *string              `json:"profile_id"`
}

type profileContextDisablePortWireV1 struct {
	Name         *string `json:"name"`
	ExactVersion *string `json:"exact_version"`
}

// FreezeProfileContextDisableV1 constructs and freezes one exact narrow plan.
func FreezeProfileContextDisableV1(
	input ProfileContextDisableInputV1,
) (ProfileContextDisablePlanV1, []byte, string, error) {
	plan := ProfileContextDisablePlanV1{
		SchemaVersion:           SchemaVersionV1,
		DesiredState:            DesiredStateDisabledV1,
		TenantID:                input.TenantID,
		ExpectedPointerRevision: input.ExpectedPointerRevision,
		BindingTarget: ProfileContextDisableBindingTargetV1{
			Kind:      BindingTargetProfileV1,
			ProfileID: input.ProfileID,
		},
		InstanceID: input.InstanceID,
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		},
	}
	if err := validatePlanV1(plan); err != nil {
		return ProfileContextDisablePlanV1{}, nil, "", err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return ProfileContextDisablePlanV1{}, nil, "", fmt.Errorf(
			"moduleapplyplan: encode Profile Context Disable plan: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		canonicalLimitsV1(),
	)
	if err != nil {
		return ProfileContextDisablePlanV1{}, nil, "", fmt.Errorf(
			"moduleapplyplan: canonicalize Profile Context Disable plan: %w",
			err,
		)
	}
	restored, err := restoreCanonicalV1(canonical)
	if err != nil {
		return ProfileContextDisablePlanV1{}, nil, "", err
	}
	detached := bytes.Clone(canonical)
	return restored, detached, moduleapi.Digest(DigestDomainV1, detached), nil
}

// RestoreProfileContextDisableV1 accepts only the exact RFC 8785 canonical
// seven-field wire and binds it to the supplied domain-separated digest.
func RestoreProfileContextDisableV1(
	canonical []byte,
	expectedDigest string,
) (ProfileContextDisablePlanV1, error) {
	if !moduleapi.ValidSHA256(expectedDigest) {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: expected plan digest must be lowercase SHA-256",
		)
	}
	input := bytes.Clone(canonical)
	plan, err := restoreCanonicalV1(input)
	if err != nil {
		return ProfileContextDisablePlanV1{}, err
	}
	if moduleapi.Digest(DigestDomainV1, input) != expectedDigest {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: plan digest mismatch",
		)
	}
	return plan, nil
}

// DigestCanonicalV1 validates the complete narrow plan before returning its
// domain-separated identity. Arbitrary canonical JSON is never accepted as a
// module-apply-plan/v1 identity.
func DigestCanonicalV1(canonical []byte) (string, error) {
	input := bytes.Clone(canonical)
	if _, err := restoreCanonicalV1(input); err != nil {
		return "", err
	}
	return moduleapi.Digest(DigestDomainV1, input), nil
}

// DeriveCandidateIDsV1 preserves the exact candidate naming policy of the
// broad Apply path without accepting caller-supplied candidate identities.
func DeriveCandidateIDsV1(planDigest string) (CandidateIDsV1, error) {
	if !moduleapi.ValidSHA256(planDigest) {
		return CandidateIDsV1{}, errors.New(
			"moduleapplyplan: plan digest must be lowercase SHA-256",
		)
	}
	return CandidateIDsV1{
		ControlSnapshotID:   CandidateControlSnapshotIDPrefixV1 + planDigest,
		CatalogGenerationID: CandidateCatalogGenerationIDPrefixV1 + planDigest,
	}, nil
}

func restoreCanonicalV1(canonical []byte) (ProfileContextDisablePlanV1, error) {
	if len(canonical) == 0 || len(canonical) > MaxCanonicalBytesV1 {
		return ProfileContextDisablePlanV1{}, fmt.Errorf(
			"moduleapplyplan: canonical plan must contain 1-%d bytes",
			MaxCanonicalBytesV1,
		)
	}
	rebuilt, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		canonicalLimitsV1(),
	)
	if err != nil {
		return ProfileContextDisablePlanV1{}, fmt.Errorf(
			"moduleapplyplan: canonicalize Profile Context Disable plan: %w",
			err,
		)
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: plan must be exact RFC 8785 canonical JSON",
		)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil || fields == nil {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: plan must be a JSON object",
		)
	}
	required := [...]string{
		"binding_target",
		"desired_state",
		"expected_pointer_revision",
		"instance_id",
		"port",
		"schema_version",
		"tenant_id",
	}
	if err := requireExactObjectFieldsV1(fields, required[:], "plan"); err != nil {
		return ProfileContextDisablePlanV1{}, err
	}

	var wire profileContextDisablePlanWireV1
	if err := decodeStrictV1(canonical, &wire); err != nil {
		return ProfileContextDisablePlanV1{}, err
	}
	if wire.SchemaVersion == nil || wire.DesiredState == nil ||
		wire.TenantID == nil || wire.ExpectedPointerRevision == nil ||
		wire.BindingTarget == nil || wire.InstanceID == nil || wire.Port == nil {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: plan fields must be present and non-null",
		)
	}

	var targetFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["binding_target"], &targetFields); err != nil || targetFields == nil {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: binding_target must be an object",
		)
	}
	if err := requireExactObjectFieldsV1(
		targetFields,
		[]string{"kind", "profile_id"},
		"binding_target",
	); err != nil {
		return ProfileContextDisablePlanV1{}, err
	}
	var portFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["port"], &portFields); err != nil || portFields == nil {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: port must be an object",
		)
	}
	if err := requireExactObjectFieldsV1(
		portFields,
		[]string{"exact_version", "name"},
		"port",
	); err != nil {
		return ProfileContextDisablePlanV1{}, err
	}
	if wire.BindingTarget.Kind == nil || wire.BindingTarget.ProfileID == nil ||
		wire.Port.Name == nil || wire.Port.ExactVersion == nil {
		return ProfileContextDisablePlanV1{}, errors.New(
			"moduleapplyplan: nested plan fields must be present and non-null",
		)
	}

	plan := ProfileContextDisablePlanV1{
		SchemaVersion:           *wire.SchemaVersion,
		DesiredState:            *wire.DesiredState,
		TenantID:                *wire.TenantID,
		ExpectedPointerRevision: *wire.ExpectedPointerRevision,
		BindingTarget: ProfileContextDisableBindingTargetV1{
			Kind:      *wire.BindingTarget.Kind,
			ProfileID: *wire.BindingTarget.ProfileID,
		},
		InstanceID: *wire.InstanceID,
		Port: moduleapi.PortRef{
			Name:         *wire.Port.Name,
			ExactVersion: *wire.Port.ExactVersion,
		},
	}
	if err := validatePlanV1(plan); err != nil {
		return ProfileContextDisablePlanV1{}, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return ProfileContextDisablePlanV1{}, fmt.Errorf(
			"moduleapplyplan: rebuild Profile Context Disable plan: %w",
			err,
		)
	}
	recanonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		canonicalLimitsV1(),
	)
	if err != nil || !bytes.Equal(recanonical, canonical) {
		return ProfileContextDisablePlanV1{}, errors.Join(
			err,
			errors.New("moduleapplyplan: plan is not the exact frozen narrow wire"),
		)
	}
	return plan, nil
}

func validatePlanV1(plan ProfileContextDisablePlanV1) error {
	if plan.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf(
			"moduleapplyplan: schema_version must be %q",
			SchemaVersionV1,
		)
	}
	if plan.DesiredState != DesiredStateDisabledV1 {
		return errors.New("moduleapplyplan: desired_state must be DISABLED")
	}
	if err := validateOpaqueIDV1("tenant_id", plan.TenantID); err != nil {
		return err
	}
	if plan.ExpectedPointerRevision == 0 ||
		plan.ExpectedPointerRevision >= math.MaxInt64 {
		return errors.New(
			"moduleapplyplan: expected_pointer_revision must be between 1 and MaxInt64-1",
		)
	}
	if plan.BindingTarget.Kind != BindingTargetProfileV1 {
		return errors.New("moduleapplyplan: binding_target.kind must be PROFILE")
	}
	if err := validateOpaqueIDV1(
		"binding_target.profile_id",
		plan.BindingTarget.ProfileID,
	); err != nil {
		return err
	}
	if err := validateOpaqueIDV1("instance_id", plan.InstanceID); err != nil {
		return err
	}
	wantedPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	if plan.Port != wantedPort {
		return errors.New(
			"moduleapplyplan: port must be exact context.provide/v1",
		)
	}
	return nil
}

func requireExactObjectFieldsV1(
	fields map[string]json.RawMessage,
	required []string,
	label string,
) error {
	if len(fields) != len(required) {
		return fmt.Errorf(
			"moduleapplyplan: %s must contain exactly %d fields",
			label,
			len(required),
		)
	}
	for _, name := range required {
		value, present := fields[name]
		if !present || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf(
				"moduleapplyplan: %s.%s must be present and non-null",
				label,
				name,
			)
		}
	}
	return nil
}

func decodeStrictV1(canonical []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("moduleapplyplan: decode plan: %w", err)
	}
	if trailerValue, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("moduleapplyplan: plan has trailing token %v", trailerValue)
		}
		return fmt.Errorf("moduleapplyplan: decode plan trailer: %w", err)
	}
	return nil
}

func validateOpaqueIDV1(label string, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"moduleapplyplan: %s must be canonical nonblank UTF-8 of at most %d bytes",
			label,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf(
				"moduleapplyplan: %s contains a control character",
				label,
			)
		}
	}
	return nil
}

func canonicalLimitsV1() moduleapi.CanonicalJSONLimits {
	return moduleapi.CanonicalJSONLimits{
		MaxBytes: MaxCanonicalBytesV1,
		MaxDepth: 128,
		MaxNodes: MaxCanonicalBytesV1,
	}
}
