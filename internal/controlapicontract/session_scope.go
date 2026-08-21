package controlapicontract

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const MaxControlSessionLifetimeMicrosV1 = uint64(8 * 60 * 60 * 1_000_000)

type ControlCapabilityV1 string

const (
	CapabilityObserveV1        ControlCapabilityV1 = "OBSERVE"
	CapabilityOperateModulesV1 ControlCapabilityV1 = "OPERATE_MODULES"
	CapabilityReviewLearningV1 ControlCapabilityV1 = "REVIEW_LEARNING"
	CapabilityRunLearningV1    ControlCapabilityV1 = "RUN_LEARNING"
)

func (capability ControlCapabilityV1) validate() error {
	switch capability {
	case CapabilityObserveV1, CapabilityOperateModulesV1,
		CapabilityReviewLearningV1, CapabilityRunLearningV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported capability %q",
			capability,
		)
	}
}

// ControlSessionV1 is safe session metadata. It deliberately excludes the
// bootstrap capability, cookie value, CSRF token, and all other credentials.
// PrincipalID, capabilities, and scope-set identity are server-established.
type ControlSessionV1 struct {
	SchemaVersion         string                `json:"schema_version"`
	BootID                string                `json:"boot_id"`
	SessionID             string                `json:"session_id"`
	PrincipalID           string                `json:"principal_id"`
	Capabilities          []ControlCapabilityV1 `json:"capabilities"`
	ScopeSetDigest        string                `json:"scope_set_digest"`
	AuthorizationRevision uint64                `json:"authorization_revision"`
	IssuedAtUnixMicros    uint64                `json:"issued_at_unix_micros"`
	ExpiresAtUnixMicros   uint64                `json:"expires_at_unix_micros"`
}

func NewControlSessionV1(
	input ControlSessionV1,
) (ControlSessionV1, []byte, string, error) {
	if input.SchemaVersion != ControlSessionSchemaVersionV1 {
		return ControlSessionV1{}, nil, "", fmt.Errorf(
			"controlapicontract: session schema_version must be %q",
			ControlSessionSchemaVersionV1,
		)
	}
	if !validOpaqueIDV1(input.BootID) || !validOpaqueIDV1(input.SessionID) ||
		!validOpaqueIDV1(input.PrincipalID) {
		return ControlSessionV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid session or principal identity",
		)
	}
	if len(input.Capabilities) == 0 ||
		len(input.Capabilities) > MaxControlCapabilitiesV1 {
		return ControlSessionV1{}, nil, "", fmt.Errorf(
			"controlapicontract: session must carry between 1 and %d capabilities",
			MaxControlCapabilitiesV1,
		)
	}
	capabilities := sortStringsV1(input.Capabilities)
	for _, capability := range capabilities {
		if err := capability.validate(); err != nil {
			return ControlSessionV1{}, nil, "", err
		}
	}
	if err := rejectDuplicateStringsV1("capability", capabilities); err != nil {
		return ControlSessionV1{}, nil, "", err
	}
	if !moduleapi.ValidSHA256(input.ScopeSetDigest) {
		return ControlSessionV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid session scope-set digest",
		)
	}
	if err := validateRevisionV1(
		"authorization revision",
		input.AuthorizationRevision,
	); err != nil {
		return ControlSessionV1{}, nil, "", err
	}
	if err := validateTimeV1("issued time", input.IssuedAtUnixMicros); err != nil {
		return ControlSessionV1{}, nil, "", err
	}
	if err := validateTimeV1("expiry time", input.ExpiresAtUnixMicros); err != nil {
		return ControlSessionV1{}, nil, "", err
	}
	if input.ExpiresAtUnixMicros <= input.IssuedAtUnixMicros ||
		input.ExpiresAtUnixMicros-input.IssuedAtUnixMicros >
			MaxControlSessionLifetimeMicrosV1 {
		return ControlSessionV1{}, nil, "", fmt.Errorf(
			"controlapicontract: session expiry must be after issue time and within %d microseconds",
			MaxControlSessionLifetimeMicrosV1,
		)
	}
	frozen := input
	frozen.Capabilities = append([]ControlCapabilityV1(nil), capabilities...)
	canonical, digest, err := freezeV1(
		frozen,
		controlSessionDigestDomainV1,
		MaxControlSessionWireBytesV1,
		256,
	)
	if err != nil {
		return ControlSessionV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlSessionV1(
	canonical []byte,
	expectedDigest string,
) (ControlSessionV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlSessionWireBytesV1,
		256,
	); err != nil {
		return ControlSessionV1{}, err
	}
	if err := verifyDigestV1(
		controlSessionDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlSessionV1{}, err
	}
	var decoded ControlSessionV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlSessionV1{}, err
	}
	restored, rebuilt, digest, err := NewControlSessionV1(decoded)
	if err != nil {
		return ControlSessionV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlSessionV1{}, fmt.Errorf(
			"controlapicontract: session is not frozen canonically",
		)
	}
	return restored, nil
}

type ControlScopeKindV1 string

const (
	ScopeTenantV1    ControlScopeKindV1 = "TENANT"
	ScopeWorkspaceV1 ControlScopeKindV1 = "WORKSPACE"
)

// ControlScopeV1 is an exact authorization selection, not a filesystem path.
type ControlScopeV1 struct {
	SchemaVersion string             `json:"schema_version"`
	Kind          ControlScopeKindV1 `json:"kind"`
	TenantID      string             `json:"tenant_id"`
	WorkspaceID   string             `json:"workspace_id,omitempty"`
}

func NewControlScopeV1(
	input ControlScopeV1,
) (ControlScopeV1, []byte, string, error) {
	if input.SchemaVersion != ControlScopeSchemaVersionV1 {
		return ControlScopeV1{}, nil, "", fmt.Errorf(
			"controlapicontract: scope schema_version must be %q",
			ControlScopeSchemaVersionV1,
		)
	}
	if !validOpaqueIDV1(input.TenantID) {
		return ControlScopeV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid scope tenant ID",
		)
	}
	switch input.Kind {
	case ScopeTenantV1:
		if input.WorkspaceID != "" {
			return ControlScopeV1{}, nil, "", fmt.Errorf(
				"controlapicontract: tenant scope cannot carry a workspace ID",
			)
		}
	case ScopeWorkspaceV1:
		if !validOpaqueIDV1(input.WorkspaceID) {
			return ControlScopeV1{}, nil, "", fmt.Errorf(
				"controlapicontract: workspace scope requires a valid workspace ID",
			)
		}
	default:
		return ControlScopeV1{}, nil, "", fmt.Errorf(
			"controlapicontract: unsupported scope kind %q",
			input.Kind,
		)
	}
	canonical, digest, err := freezeV1(
		input,
		controlScopeDigestDomainV1,
		MaxControlScopeWireBytesV1,
		32,
	)
	if err != nil {
		return ControlScopeV1{}, nil, "", err
	}
	return input, canonical, digest, nil
}

func RestoreControlScopeV1(
	canonical []byte,
	expectedDigest string,
) (ControlScopeV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlScopeWireBytesV1,
		32,
	); err != nil {
		return ControlScopeV1{}, err
	}
	if err := verifyDigestV1(
		controlScopeDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlScopeV1{}, err
	}
	var decoded ControlScopeV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlScopeV1{}, err
	}
	restored, rebuilt, digest, err := NewControlScopeV1(decoded)
	if err != nil {
		return ControlScopeV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlScopeV1{}, fmt.Errorf(
			"controlapicontract: scope is not frozen canonically",
		)
	}
	return restored, nil
}
