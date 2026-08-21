// Package controlsession owns the process-local authorization facts and
// credentials used by the optional Control API. It has no HTTP, filesystem,
// listener, Current Store, or durable persistence responsibility.
package controlsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AuthorizedScopeSetSchemaVersionV1 = "control-scope-set/v1"
	MaximumAuthorizedScopesV1         = 256

	authorizedScopeSetDigestDomainV1 = "freeagent.control-scope-set/v1"
	maximumAuthorizedScopeSetBytesV1 = MaximumAuthorizedScopesV1*controlapicontract.MaxControlScopeWireBytesV1 + 4096
)

var ErrInvalidConfiguration = errors.New("controlsession: invalid configuration")

type authorizedScopeSetWireV1 struct {
	SchemaVersion string                              `json:"schema_version"`
	Scopes        []controlapicontract.ControlScopeV1 `json:"scopes"`
}

// AuthorizedScopeSetV1 is the immutable, server-derived scope ceiling for one
// Control principal. It is not a public transport contract and grants no
// authority unless it is retained by a live RegistryV1 session.
type AuthorizedScopeSetV1 struct {
	scopes    []controlapicontract.ControlScopeV1
	canonical []byte
	digest    string
}

// NewAuthorizedScopeSetV1 validates, orders, and freezes exact Control scopes.
// Duplicate declarations fail closed; they are not silently deduplicated.
func NewAuthorizedScopeSetV1(
	input []controlapicontract.ControlScopeV1,
) (*AuthorizedScopeSetV1, error) {
	if len(input) == 0 || len(input) > MaximumAuthorizedScopesV1 {
		return nil, ErrInvalidConfiguration
	}
	scopes := make([]controlapicontract.ControlScopeV1, len(input))
	for index, scope := range input {
		frozen, _, _, err := controlapicontract.NewControlScopeV1(scope)
		if err != nil {
			return nil, ErrInvalidConfiguration
		}
		scopes[index] = frozen
	}
	sort.Slice(scopes, func(left, right int) bool {
		return scopeLessV1(scopes[left], scopes[right])
	})
	for index := 1; index < len(scopes); index++ {
		if scopes[index] == scopes[index-1] {
			return nil, ErrInvalidConfiguration
		}
	}
	wire := authorizedScopeSetWireV1{
		SchemaVersion: AuthorizedScopeSetSchemaVersionV1,
		Scopes:        scopes,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumAuthorizedScopeSetBytesV1,
			MaxDepth: 8,
			MaxNodes: 1 + MaximumAuthorizedScopesV1*8,
		},
	)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return nil, ErrInvalidConfiguration
	}
	return &AuthorizedScopeSetV1{
		scopes:    append([]controlapicontract.ControlScopeV1(nil), scopes...),
		canonical: bytes.Clone(canonical),
		digest: moduleapi.Digest(
			authorizedScopeSetDigestDomainV1,
			canonical,
		),
	}, nil
}

func scopeLessV1(
	left controlapicontract.ControlScopeV1,
	right controlapicontract.ControlScopeV1,
) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return left.WorkspaceID < right.WorkspaceID
}

// Canonical returns a defensive copy of the exact server-derived ScopeSet.
func (set *AuthorizedScopeSetV1) Canonical() []byte {
	if set == nil {
		return nil
	}
	return bytes.Clone(set.canonical)
}

func (set *AuthorizedScopeSetV1) Digest() string {
	if set == nil {
		return ""
	}
	return set.digest
}

func (set *AuthorizedScopeSetV1) Scopes() []controlapicontract.ControlScopeV1 {
	if set == nil {
		return nil
	}
	return append([]controlapicontract.ControlScopeV1(nil), set.scopes...)
}

// Allows applies the frozen ceiling semantics. A TENANT grant covers that
// exact Tenant and its Workspace scopes; a WORKSPACE grant covers only the
// exact Workspace.
func (set *AuthorizedScopeSetV1) Allows(
	requested controlapicontract.ControlScopeV1,
) bool {
	if set == nil {
		return false
	}
	frozen, _, _, err := controlapicontract.NewControlScopeV1(requested)
	if err != nil {
		return false
	}
	for _, granted := range set.scopes {
		if granted.TenantID != frozen.TenantID {
			continue
		}
		if granted.Kind == controlapicontract.ScopeTenantV1 || granted == frozen {
			return true
		}
	}
	return false
}

func (set *AuthorizedScopeSetV1) clone() *AuthorizedScopeSetV1 {
	if set == nil {
		return nil
	}
	return &AuthorizedScopeSetV1{
		scopes:    append([]controlapicontract.ControlScopeV1(nil), set.scopes...),
		canonical: bytes.Clone(set.canonical),
		digest:    set.digest,
	}
}
