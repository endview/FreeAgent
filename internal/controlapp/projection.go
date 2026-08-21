package controlapp

import (
	"bytes"
	"sort"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func projectVisibleModulesV1(
	scope controlapicontract.ControlScopeV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	authorizeResource func() error,
) ([]ModuleDetailV1, error) {
	catalogByInstance := make(
		map[string]controlcontract.CatalogEntry,
		len(catalog.Entries),
	)
	for _, entry := range catalog.Entries {
		catalogByInstance[entry.Activation.InstanceID] = entry
	}
	bindingsByInstance := make(
		map[string][]ModuleBindingSummaryV1,
		len(catalog.Entries),
	)
	visibleInstances := make(map[string]struct{}, len(catalog.Entries))
	bindingCount := 0

	addBinding := func(
		target ModuleBindingTargetV1,
		binding controlcontract.BindingSpec,
		portBindingIndex uint16,
	) error {
		if _, exists := catalogByInstance[binding.InstanceID]; !exists {
			return ErrIntegrityFailure
		}
		bindingCount++
		if bindingCount > maximumVisibleBindingsV1 {
			return ErrResourceExhausted
		}
		visibleInstances[binding.InstanceID] = struct{}{}
		bindingsByInstance[binding.InstanceID] = append(
			bindingsByInstance[binding.InstanceID],
			ModuleBindingSummaryV1{
				Target:              target,
				Port:                binding.Port,
				PortBindingIndex:    portBindingIndex,
				ConfigRef:           binding.ConfigRef,
				AuthorityCeilingRef: binding.AuthorityCeilingRef,
				StaticContextRefs: append(
					[]string{},
					binding.StaticContextRefs...,
				),
				FailurePolicy: binding.FailurePolicy,
			},
		)
		return nil
	}

	switch scope.Kind {
	case controlapicontract.ScopeTenantV1:
		for instanceID := range catalogByInstance {
			visibleInstances[instanceID] = struct{}{}
		}
		for _, profile := range control.Profiles {
			portOrdinals := make(map[string]uint16)
			for _, binding := range profile.Bindings {
				portKey, err := binding.Port.CanonicalKey()
				if err != nil {
					return nil, ErrIntegrityFailure
				}
				ordinal := portOrdinals[portKey]
				portOrdinals[portKey] = ordinal + 1
				if err := addBinding(
					ModuleBindingTargetV1{
						Kind:      ModuleBindingTargetProfileV1,
						ProfileID: profile.Profile.ID,
					},
					binding,
					ordinal,
				); err != nil {
					return nil, err
				}
			}
		}
		for _, workspace := range control.Workspaces {
			for _, endpoint := range workspace.ChannelEndpoints {
				if err := addBinding(
					ModuleBindingTargetV1{
						Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
						WorkspaceID: workspace.Workspace.ID,
						EndpointID:  endpoint.EndpointID,
					},
					endpoint.Binding,
					0,
				); err != nil {
					return nil, err
				}
			}
		}
	case controlapicontract.ScopeWorkspaceV1:
		workspace, found := control.FindWorkspace(scope.WorkspaceID)
		if !found {
			return nil, ErrNotFound
		}
		for _, endpoint := range workspace.ChannelEndpoints {
			if err := addBinding(
				ModuleBindingTargetV1{
					Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
					WorkspaceID: workspace.Workspace.ID,
					EndpointID:  endpoint.EndpointID,
				},
				endpoint.Binding,
				0,
			); err != nil {
				return nil, err
			}
		}
	default:
		return nil, ErrInvalidRequest
	}

	modules := make([]ModuleDetailV1, 0, len(visibleInstances))
	for _, entry := range catalog.Entries {
		if _, visible := visibleInstances[entry.Activation.InstanceID]; !visible {
			continue
		}
		bindings := append(
			[]ModuleBindingSummaryV1{},
			bindingsByInstance[entry.Activation.InstanceID]...,
		)
		sort.Slice(bindings, func(left, right int) bool {
			return moduleBindingLessV1(bindings[left], bindings[right])
		})
		detail := ModuleDetailV1{
			Summary: ModuleSummaryV1{
				InstanceID:          entry.Activation.InstanceID,
				ModuleID:            entry.Activation.ModuleID,
				ExactVersion:        entry.Activation.Version,
				ArtifactDigest:      entry.Activation.ArtifactDigest,
				ExecutionClass:      entry.Activation.ExecutionClass,
				AdapterIdentity:     entry.Activation.AdapterIdentity,
				ActivationRevision:  entry.Activation.ActivationRevision,
				Provides:            append([]moduleapi.PortRef{}, entry.Provides...),
				VisibleBindingCount: uint32(len(bindings)),
			},
			Bindings: bindings,
		}
		if err := validateModuleOwnershipV1(scope, detail); err != nil {
			return nil, err
		}
		if authorizeResource == nil {
			return nil, ErrForbidden
		}
		if err := authorizeResource(); err != nil {
			return nil, err
		}
		modules = append(modules, detail)
	}
	sort.Slice(modules, func(left, right int) bool {
		return compareBinaryTextV1(
			modules[left].Summary.InstanceID,
			modules[right].Summary.InstanceID,
		) < 0
	})
	return modules, nil
}

func validateModuleOwnershipV1(
	scope controlapicontract.ControlScopeV1,
	module ModuleDetailV1,
) error {
	if module.Summary.VisibleBindingCount != uint32(len(module.Bindings)) {
		return ErrIntegrityFailure
	}
	switch scope.Kind {
	case controlapicontract.ScopeTenantV1:
		for _, binding := range module.Bindings {
			switch binding.Target.Kind {
			case ModuleBindingTargetProfileV1:
				if !validOpaqueIDV1(binding.Target.ProfileID) ||
					binding.Target.WorkspaceID != "" ||
					binding.Target.EndpointID != "" {
					return ErrIntegrityFailure
				}
			case ModuleBindingTargetWorkspaceChannelEndpointV1:
				if binding.Target.ProfileID != "" ||
					!validOpaqueIDV1(binding.Target.WorkspaceID) ||
					!validOpaqueIDV1(binding.Target.EndpointID) {
					return ErrIntegrityFailure
				}
			default:
				return ErrIntegrityFailure
			}
		}
		return nil
	case controlapicontract.ScopeWorkspaceV1:
		if len(module.Bindings) == 0 {
			return ErrIntegrityFailure
		}
		for _, binding := range module.Bindings {
			if binding.Target.Kind !=
				ModuleBindingTargetWorkspaceChannelEndpointV1 ||
				binding.Target.ProfileID != "" ||
				binding.Target.WorkspaceID != scope.WorkspaceID ||
				!validOpaqueIDV1(binding.Target.EndpointID) {
				return ErrIntegrityFailure
			}
		}
		return nil
	default:
		return ErrInvalidRequest
	}
}

func moduleBindingLessV1(
	left ModuleBindingSummaryV1,
	right ModuleBindingSummaryV1,
) bool {
	leftTarget := []string{
		string(left.Target.Kind),
		left.Target.ProfileID,
		left.Target.WorkspaceID,
		left.Target.EndpointID,
	}
	rightTarget := []string{
		string(right.Target.Kind),
		right.Target.ProfileID,
		right.Target.WorkspaceID,
		right.Target.EndpointID,
	}
	for index := range leftTarget {
		if comparison := compareBinaryTextV1(
			leftTarget[index],
			rightTarget[index],
		); comparison != 0 {
			return comparison < 0
		}
	}
	if comparison := compareBinaryTextV1(
		left.Port.Name,
		right.Port.Name,
	); comparison != 0 {
		return comparison < 0
	}
	if comparison := compareBinaryTextV1(
		left.Port.ExactVersion,
		right.Port.ExactVersion,
	); comparison != 0 {
		return comparison < 0
	}
	return left.PortBindingIndex < right.PortBindingIndex
}

func compareBinaryTextV1(left, right string) int {
	return bytes.Compare([]byte(left), []byte(right))
}

func findModuleV1(
	modules []ModuleDetailV1,
	instanceID string,
) (int, bool) {
	index := sort.Search(len(modules), func(index int) bool {
		return compareBinaryTextV1(
			modules[index].Summary.InstanceID,
			instanceID,
		) >= 0
	})
	return index, index < len(modules) &&
		modules[index].Summary.InstanceID == instanceID
}
