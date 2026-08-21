// Package actionmaterializer freezes the admission-time Describe view of an
// optional action.provider/v1 PortPlan. It has no Store writer, Run identity,
// execution permit, Prepare path, Gateway, or executor access.
package actionmaterializer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrInvalidMaterialization = errors.New(
	"actionmaterializer: invalid frozen Action materialization",
)

const contentRecordDigestDomainV1 = "freeagent.content-record/v1\x00"

// BindingMaterialV1 contains only the immutable content bodies referenced by
// one frozen action.provider/v1 Binding. The caller must preserve PortPlan
// Binding order; this package verifies both ContentRecord digests.
type BindingMaterialV1 struct {
	ConfigCanonical    []byte
	AuthorityCanonical []byte
}

// InputV1 is stable admission input. Describe deliberately receives only the
// inert Parameters from Config, never these local scope or authority values.
type InputV1 struct {
	TenantID    string
	WorkspaceID string
	Plan        moduleapi.PortPlan
	Bindings    []BindingMaterialV1
}

// Materializer owns the one exact deployment-local registry capability used
// to find a public ActionProviderV1. The registry still returns a generic
// ModuleInvoker so one adapter identity remains authoritative for every Port.
type Materializer struct {
	registry modulehost.ExactAdapterRegistry
}

func New(registry modulehost.ExactAdapterRegistry) (*Materializer, error) {
	if isNilDependency(registry) {
		return nil, fmt.Errorf("%w: exact adapter registry is nil", ErrInvalidMaterialization)
	}
	return &Materializer{registry: registry}, nil
}

// Materialize performs read-only Describe calls in frozen Binding order and
// intersects Provider requests with consumer Config and local authority. It
// never calls Prepare, generic Invoke, or an Action executor.
func (materializer *Materializer) Materialize(
	ctx context.Context,
	input InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	if materializer == nil || isNilDependency(materializer.registry) {
		return nil, fmt.Errorf("%w: materializer is not initialized", ErrInvalidMaterialization)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidMaterialization)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.TenantID == "" || input.WorkspaceID == "" {
		return nil, fmt.Errorf(
			"%w: exact tenant and workspace identities are required",
			ErrInvalidMaterialization,
		)
	}
	plan, err := moduleapi.NewPortPlan(input.Plan)
	if err != nil {
		return nil, fmt.Errorf("%w: action PortPlan: %v", ErrInvalidMaterialization, err)
	}
	if plan.Port.Name != moduleapi.PortNameActionProvider ||
		plan.Port.ExactVersion != moduleapi.PortVersionV1 {
		return nil, fmt.Errorf(
			"%w: exact Port must be action.provider/v1",
			ErrInvalidMaterialization,
		)
	}
	if len(input.Bindings) != len(plan.Bindings) {
		return nil, fmt.Errorf(
			"%w: Binding material count does not match PortPlan",
			ErrInvalidMaterialization,
		)
	}

	definitions := make([]corecontract.FrozenActionDefinitionV1, 0)
	seenPublicIDs := make(map[string]struct{})
	for bindingIndex, binding := range plan.Bindings {
		if binding.Provider.ExecutionClass !=
			moduleapi.ExecutionTrustedInProcess &&
			binding.Provider.ExecutionClass !=
				moduleapi.ExecutionLocalProcess &&
			binding.Provider.ExecutionClass !=
				moduleapi.ExecutionRemote &&
			binding.Provider.ExecutionClass !=
				moduleapi.ExecutionWASM {
			return nil, fmt.Errorf(
				"%w: Binding %d is not a supported Action execution class",
				ErrInvalidMaterialization,
				bindingIndex,
			)
		}
		material := input.Bindings[bindingIndex]
		config, authority, err := restoreBindingMaterial(
			binding,
			material,
			input.TenantID,
			input.WorkspaceID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Binding %d: %v",
				ErrInvalidMaterialization,
				bindingIndex,
				err,
			)
		}
		invoker, err := materializer.registry.ResolveExact(
			ctx,
			binding.Provider.ArtifactDigest,
			binding.Provider.AdapterIdentity,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Binding %d exact adapter: %v",
				ErrInvalidMaterialization,
				bindingIndex,
				err,
			)
		}
		provider, ok := invoker.(moduleapi.ActionProviderV1)
		if !ok || isNilDependency(provider) {
			return nil, fmt.Errorf(
				"%w: Binding %d adapter does not implement ActionProviderV1",
				ErrInvalidMaterialization,
				bindingIndex,
			)
		}
		describeRequest, _, err := moduleapi.NewActionDescribeRequestV1(
			moduleapi.ActionDescribeRequestV1{
				SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
				Parameters:    bytes.Clone(config.Parameters),
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Binding %d Describe request: %v",
				ErrInvalidMaterialization,
				bindingIndex,
				err,
			)
		}
		described, err := provider.Describe(ctx, describeRequest)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Binding %d Describe: %v",
				ErrInvalidMaterialization,
				bindingIndex,
				err,
			)
		}
		described, err = moduleapi.FreezeActionDefinitionsV1(described)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Binding %d definitions: %v",
				ErrInvalidMaterialization,
				bindingIndex,
				err,
			)
		}
		byProviderID := make(
			map[string]moduleapi.ActionDefinitionV1,
			len(described),
		)
		for _, definition := range described {
			byProviderID[definition.ProviderActionID] = definition
		}
		for mappingIndex, mapping := range config.Actions {
			definition, found := byProviderID[mapping.ProviderActionID]
			if !found {
				return nil, fmt.Errorf(
					"%w: Binding %d mapping %d ProviderActionID %q was not described",
					ErrInvalidMaterialization,
					bindingIndex,
					mappingIndex,
					mapping.ProviderActionID,
				)
			}
			if !contains(authority.AllowedProviderActionIDs, mapping.ProviderActionID) {
				return nil, fmt.Errorf(
					"%w: Binding %d ProviderActionID %q is outside local authority",
					ErrInvalidMaterialization,
					bindingIndex,
					mapping.ProviderActionID,
				)
			}
			effectiveEffect, err := moduleapi.HigherActionEffectV1(
				definition.RequestedEffectClass,
				mapping.LocalEffectClass,
			)
			if err != nil {
				return nil, err
			}
			allowed, err := moduleapi.ActionEffectAtMostV1(
				effectiveEffect,
				authority.MaxEffectClass,
			)
			if err != nil || !allowed {
				return nil, fmt.Errorf(
					"%w: Binding %d Action %q effect %q exceeds authority %q",
					ErrInvalidMaterialization,
					bindingIndex,
					mapping.PublicActionID,
					effectiveEffect,
					authority.MaxEffectClass,
				)
			}
			maximum := minimumResultBytes(
				definition.RequestedMaxResultBytes,
				mapping.MaxResultBytes,
				authority.MaxResultBytes,
			)
			frozen, _, err := corecontract.NewFrozenActionDefinitionV1(
				corecontract.FrozenActionDefinitionV1{
					PublicActionID:   mapping.PublicActionID,
					ProviderActionID: mapping.ProviderActionID,
					BindingIndex:     uint32(bindingIndex),
					Description:      definition.Description,
					InputSchema:      bytes.Clone(definition.InputSchema),
					EffectClass:      effectiveEffect,
					MaxResultBytes:   maximum,
				},
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: Binding %d Action %q: %v",
					ErrInvalidMaterialization,
					bindingIndex,
					mapping.PublicActionID,
					err,
				)
			}
			if _, duplicate := seenPublicIDs[frozen.PublicActionID]; duplicate {
				return nil, fmt.Errorf(
					"%w: duplicate PublicActionID %q across Bindings",
					ErrInvalidMaterialization,
					frozen.PublicActionID,
				)
			}
			seenPublicIDs[frozen.PublicActionID] = struct{}{}
			definitions = append(definitions, frozen)
			if len(definitions) > moduleapi.MaxActionsPerMemberV1 {
				return nil, fmt.Errorf(
					"%w: member exceeds %d Actions",
					ErrInvalidMaterialization,
					moduleapi.MaxActionsPerMemberV1,
				)
			}
		}
	}
	sort.Slice(definitions, func(left, right int) bool {
		return definitions[left].PublicActionID < definitions[right].PublicActionID
	})
	return definitions, nil
}

func restoreBindingMaterial(
	binding moduleapi.PortBinding,
	material BindingMaterialV1,
	tenantID string,
	workspaceID string,
) (moduleapi.ActionBindingConfigV1, moduleapi.ActionAuthorityCeilingV1, error) {
	configDigest := contentRecordDigestV1(
		"CONFIG",
		"application/json",
		material.ConfigCanonical,
	)
	if configDigest != binding.ConfigRef {
		return moduleapi.ActionBindingConfigV1{}, moduleapi.ActionAuthorityCeilingV1{},
			fmt.Errorf("Config does not match ConfigRef")
	}
	authorityDigest := contentRecordDigestV1(
		"AUTHORITY_CEILING",
		"application/json",
		material.AuthorityCanonical,
	)
	if authorityDigest != binding.AuthorityCeilingRef {
		return moduleapi.ActionBindingConfigV1{}, moduleapi.ActionAuthorityCeilingV1{},
			fmt.Errorf("Authority does not match AuthorityCeilingRef")
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(material.ConfigCanonical)
	if err != nil {
		return moduleapi.ActionBindingConfigV1{}, moduleapi.ActionAuthorityCeilingV1{}, err
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		material.AuthorityCanonical,
	)
	if err != nil {
		return moduleapi.ActionBindingConfigV1{}, moduleapi.ActionAuthorityCeilingV1{}, err
	}
	if authority.TenantID != tenantID ||
		!containsOrWildcard(authority.AllowedWorkspaceIDs, workspaceID) {
		return moduleapi.ActionBindingConfigV1{}, moduleapi.ActionAuthorityCeilingV1{},
			fmt.Errorf("Authority does not grant the exact tenant/workspace scope")
	}
	return config, authority, nil
}

func contentRecordDigestV1(kind string, mediaType string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(contentRecordDigestDomainV1))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(mediaType))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}

func minimumResultBytes(values ...uint32) uint32 {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsOrWildcard(values []string, wanted string) bool {
	return contains(values, "*") || contains(values, wanted)
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
