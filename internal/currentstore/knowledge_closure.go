package currentstore

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/knowledgecore"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func verifyControlKnowledgeBindings(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	getContent := func(digest string) (ContentRecord, error) {
		return queryContent(ctx, queryer, digest)
	}
	for _, profile := range control.Profiles {
		contextBindings := make([]moduleapi.PortBinding, 0)
		memoryBindingCount := 0
		for index, request := range profile.Bindings {
			if request.Port.Name != moduleapi.PortNameContextProvide ||
				request.Port.ExactVersion != moduleapi.PortVersionV1 {
				continue
			}
			entry, found := catalog.FindInstance(request.InstanceID)
			if !found || !containsExactPort(entry.Provides, request.Port) {
				return fmt.Errorf(
					"%w: profile %s dynamic context Binding %d is absent from Catalog",
					ErrPublicationConflict,
					profile.Profile.ID,
					index,
				)
			}
			binding := moduleapi.PortBinding{
				Provider:            entry.Activation,
				ConfigRef:           request.ConfigRef,
				AuthorityCeilingRef: request.AuthorityCeilingRef,
				StaticContextRefs: append(
					[]string(nil),
					request.StaticContextRefs...,
				),
				FailurePolicy: request.FailurePolicy,
			}
			contextBindings = append(contextBindings, binding)
			if _, _, dynamic, err := validateContextBindingDefinitionV1(
				request.Port,
				binding,
				getContent,
			); err != nil {
				return fmt.Errorf(
					"%w: profile %s context Binding %d: %v",
					ErrPublicationConflict,
					profile.Profile.ID,
					index,
					err,
				)
			} else if dynamic {
				protocol, err := contextBindingProtocolV1(binding, getContent)
				if err != nil {
					return fmt.Errorf(
						"%w: profile %s context Binding %d protocol: %v",
						ErrPublicationConflict,
						profile.Profile.ID,
						index,
						err,
					)
				}
				if protocol == moduleapi.MemoryContextBindingSchemaV1 {
					memoryBindingCount++
					if memoryBindingCount > 1 {
						return fmt.Errorf(
							"%w: profile %s permits at most one Memory Binding",
							ErrPublicationConflict,
							profile.Profile.ID,
						)
					}
					if err := validatePublishedMemoryBindingV1(
						control,
						binding,
						getContent,
					); err != nil {
						return fmt.Errorf(
							"%w: profile %s Memory Binding %d: %v",
							ErrPublicationConflict,
							profile.Profile.ID,
							index,
							err,
						)
					}
				}
			}
		}
		if len(contextBindings) != 0 {
			if _, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				},
				Bindings: contextBindings,
			}); err != nil {
				return fmt.Errorf(
					"%w: profile %s context.provide/v1 PortPlan: %v",
					ErrPublicationConflict,
					profile.Profile.ID,
					err,
				)
			}
		}
	}
	return nil
}

// validatePublishedMemoryBindingV1 proves that a package-level Memory ceiling
// names an owner and Workspace universe present in the Control snapshot and
// can authorize the requested kinds/limits. The exact Task/Agent/Workspace
// tuple is selected later and re-proved at Admission, Begin and recovery.
func validatePublishedMemoryBindingV1(
	control controlcontract.ControlSnapshot,
	binding moduleapi.PortBinding,
	getContent knowledgeContentGetter,
) error {
	configRecord, err := getContent(binding.ConfigRef)
	if err != nil {
		return fmt.Errorf("Memory Config is unavailable")
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("restore Memory Config: %w", err)
	}
	config, _, err := moduleapi.RestoreMemoryContextBindingParametersV1(
		contextConfig,
	)
	if err != nil {
		return fmt.Errorf("restore Memory parameters: %w", err)
	}
	authorityRecord, err := getContent(binding.AuthorityCeilingRef)
	if err != nil {
		return fmt.Errorf("Memory authority is unavailable")
	}
	authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("restore Memory authority: %w", err)
	}
	if authority.TenantID != control.TenantID {
		return fmt.Errorf("Memory authority tenant is outside Control")
	}
	agent, found := control.FindAgent(authority.AgentID)
	if !found {
		return fmt.Errorf("Memory authority Agent is outside Control")
	}
	var workspace corecontract.WorkspaceRef
	if len(authority.AllowedWorkspaceIDs) == 1 &&
		authority.AllowedWorkspaceIDs[0] == "*" {
		if len(control.Workspaces) == 0 {
			return fmt.Errorf("Memory authority has no Control Workspace")
		}
		workspace = control.Workspaces[0].Workspace
	} else {
		for index, workspaceID := range authority.AllowedWorkspaceIDs {
			definition, found := control.FindWorkspace(workspaceID)
			if !found {
				return fmt.Errorf(
					"Memory authority Workspace %q is outside Control",
					workspaceID,
				)
			}
			if index == 0 {
				workspace = definition.Workspace
			}
		}
	}
	scope := moduleapi.MemoryQueryScopeV1{
		TenantID: control.TenantID,
		Workspace: moduleapi.MemoryObjectRefV1{
			ID: workspace.ID, Version: workspace.Version, Digest: workspace.Digest,
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID: agent.ID, Version: agent.Version, Digest: agent.Digest,
		},
		TaskInputRef: agent.Digest,
	}
	_, err = moduleapi.ResolveMemoryAuthorityV1(
		config,
		authority,
		moduleapi.MemorySnapshotRefV1{
			TenantID: control.TenantID,
			AgentID:  agent.ID,
			Revision: 1,
			Digest:   agent.Digest,
		},
		scope,
	)
	if err != nil {
		return fmt.Errorf("Memory authority cannot grant requested shape: %w", err)
	}
	return nil
}

// knowledgeContentGetter is deliberately narrower than Store. Dynamic RAG
// validation consumes only immutable content addressed by the frozen Binding.
type knowledgeContentGetter func(string) (ContentRecord, error)

type frozenKnowledgeBinding struct {
	BindingIndex uint32
	Binding      moduleapi.PortBinding
	Config       moduleapi.KnowledgeContextBindingV1
	Authority    moduleapi.KnowledgeAuthorityCeilingV1
	Scope        moduleapi.KnowledgeQueryScopeV1
	MaxHits      uint32
	MaxTextBytes uint32
}

type frozenMemoryBinding struct {
	BindingIndex uint32
	Binding      moduleapi.PortBinding
	Config       moduleapi.MemoryContextBindingV1
	Authority    moduleapi.MemoryAuthorityCeilingV1
	Scope        moduleapi.MemoryQueryScopeV1
}

// validateContextBindingDefinitionV1 preserves the existing DECLARATIVE
// context path and validates the only dynamic context.provide/v1 shape. It
// performs no lookup for non-context ports or declarative Bindings.
func validateContextBindingDefinitionV1(
	port moduleapi.PortRef,
	binding moduleapi.PortBinding,
	getContent knowledgeContentGetter,
) (
	moduleapi.KnowledgeContextBindingV1,
	moduleapi.KnowledgeAuthorityCeilingV1,
	bool,
	error,
) {
	if port.Name != moduleapi.PortNameContextProvide ||
		port.ExactVersion != moduleapi.PortVersionV1 {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false, nil
	}
	switch binding.Provider.ExecutionClass {
	case moduleapi.ExecutionDeclarative:
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false, nil
	case moduleapi.ExecutionTrustedInProcess:
		// Continue below. The first RAG slice deliberately has no remote,
		// local-process, retry or fallback execution semantics.
	default:
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf(
				"dynamic context.provide/v1 Binding must be TRUSTED_IN_PROCESS",
			)
	}
	if binding.FailurePolicy != moduleapi.FailureRequired {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("dynamic context.provide/v1 Binding must be REQUIRED")
	}
	if len(binding.StaticContextRefs) != 0 {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf(
				"dynamic context.provide/v1 Binding cannot contain static context refs",
			)
	}

	configRecord, err := getContent(binding.ConfigRef)
	if err != nil || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("dynamic context Binding CONFIG is unavailable")
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("dynamic context Binding CONFIG: %w", err)
	}
	authorityRecord, err := getContent(binding.AuthorityCeilingRef)
	if err != nil || authorityRecord.Kind != ContentAuthorityCeiling ||
		authorityRecord.MediaType != admissionJSONMediaType {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("dynamic context Binding authority ceiling is unavailable")
	}
	protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(
		contextConfig,
	)
	if err != nil {
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("dynamic context Binding parameters protocol: %w", err)
	}
	switch protocol {
	case moduleapi.KnowledgeContextBindingSchemaV1:
		knowledgeConfig, err :=
			moduleapi.RestoreKnowledgeContextBindingParametersV1(contextConfig)
		if err != nil {
			return moduleapi.KnowledgeContextBindingV1{},
				moduleapi.KnowledgeAuthorityCeilingV1{}, false,
				fmt.Errorf("dynamic context Binding parameters: %w", err)
		}
		authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
			authorityRecord.CanonicalBytes,
		)
		if err != nil {
			return moduleapi.KnowledgeContextBindingV1{},
				moduleapi.KnowledgeAuthorityCeilingV1{}, false,
				fmt.Errorf("dynamic context Binding authority ceiling: %w", err)
		}
		if knowledgeConfig.Source != authority.Source {
			return moduleapi.KnowledgeContextBindingV1{},
				moduleapi.KnowledgeAuthorityCeilingV1{}, false,
				fmt.Errorf("dynamic context Binding Config and authority source differ")
		}
		return knowledgeConfig, authority, true, nil
	case moduleapi.MemoryContextBindingSchemaV1:
		if _, _, err :=
			moduleapi.RestoreMemoryContextBindingParametersV1(contextConfig); err != nil {
			return moduleapi.KnowledgeContextBindingV1{},
				moduleapi.KnowledgeAuthorityCeilingV1{}, false,
				fmt.Errorf("dynamic Memory Binding parameters: %w", err)
		}
		if _, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
			authorityRecord.CanonicalBytes,
		); err != nil {
			return moduleapi.KnowledgeContextBindingV1{},
				moduleapi.KnowledgeAuthorityCeilingV1{}, false,
				fmt.Errorf("dynamic Memory Binding authority ceiling: %w", err)
		}
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, true, nil
	default:
		return moduleapi.KnowledgeContextBindingV1{},
			moduleapi.KnowledgeAuthorityCeilingV1{}, false,
			fmt.Errorf("unsupported dynamic context parameters schema %q", protocol)
	}
}

func frozenKnowledgeBindingsForRun(
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	getContent knowledgeContentGetter,
) ([]frozenKnowledgeBinding, error) {
	scope := knowledgeQueryScopeForRun(manifest, member)
	bindings := make([]frozenKnowledgeBinding, 0)
	for _, plan := range member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		for index, binding := range plan.Bindings {
			config, authority, dynamic, err :=
				validateContextBindingDefinitionV1(
					plan.Port,
					binding,
					getContent,
				)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d: %w",
					index,
					err,
				)
			}
			if !dynamic {
				continue
			}
			protocol, err := contextBindingProtocolV1(binding, getContent)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d protocol: %w",
					index,
					err,
				)
			}
			if protocol != moduleapi.KnowledgeContextBindingSchemaV1 {
				continue
			}
			maxHits, maxTextBytes, err := moduleapi.ResolveKnowledgeLimitsV1(
				config,
				authority,
				scope,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d exact scope: %w",
					index,
					err,
				)
			}
			bindings = append(bindings, frozenKnowledgeBinding{
				BindingIndex: uint32(index),
				Binding:      binding,
				Config:       config,
				Authority:    authority,
				Scope:        scope,
				MaxHits:      maxHits,
				MaxTextBytes: maxTextBytes,
			})
		}
	}
	return bindings, nil
}

func frozenMemoryBindingsForRun(
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	getContent knowledgeContentGetter,
) ([]frozenMemoryBinding, error) {
	scope := memoryQueryScopeForRun(manifest, member)
	bindings := make([]frozenMemoryBinding, 0)
	for _, plan := range member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		for index, binding := range plan.Bindings {
			_, _, dynamic, err := validateContextBindingDefinitionV1(
				plan.Port,
				binding,
				getContent,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d: %w",
					index,
					err,
				)
			}
			if !dynamic {
				continue
			}
			protocol, err := contextBindingProtocolV1(binding, getContent)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d protocol: %w",
					index,
					err,
				)
			}
			if protocol != moduleapi.MemoryContextBindingSchemaV1 {
				continue
			}
			configRecord, err := getContent(binding.ConfigRef)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d Memory Config: %w",
					index,
					err,
				)
			}
			contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
				configRecord.CanonicalBytes,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d Memory Config: %w",
					index,
					err,
				)
			}
			config, _, err :=
				moduleapi.RestoreMemoryContextBindingParametersV1(contextConfig)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d Memory parameters: %w",
					index,
					err,
				)
			}
			authorityRecord, err := getContent(binding.AuthorityCeilingRef)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d Memory authority: %w",
					index,
					err,
				)
			}
			authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
				authorityRecord.CanonicalBytes,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"context Binding %d Memory authority: %w",
					index,
					err,
				)
			}
			// Publication and Admission have no mutable Memory head to consult,
			// but they can and must prove the exact frozen Run scope, requested
			// kinds and limits against the authority ceiling. The synthetic ref
			// supplies only the owner fields needed by authority resolution; a
			// real exact revision is still required at dispatch time.
			if _, err := moduleapi.ResolveMemoryAuthorityV1(
				config,
				authority,
				moduleapi.MemorySnapshotRefV1{
					TenantID: scope.TenantID,
					AgentID:  scope.Agent.ID,
					Revision: 1,
					Digest:   scope.TaskInputRef,
				},
				scope,
			); err != nil {
				return nil, fmt.Errorf(
					"context Binding %d exact Memory scope: %w",
					index,
					err,
				)
			}
			if len(bindings) != 0 {
				return nil, fmt.Errorf(
					"context Binding %d: the first Memory slice permits at most one Memory Binding per member",
					index,
				)
			}
			bindings = append(bindings, frozenMemoryBinding{
				BindingIndex: uint32(index),
				Binding:      binding,
				Config:       config,
				Authority:    authority,
				Scope:        scope,
			})
		}
	}
	return bindings, nil
}

func contextBindingProtocolV1(
	binding moduleapi.PortBinding,
	getContent knowledgeContentGetter,
) (string, error) {
	configRecord, err := getContent(binding.ConfigRef)
	if err != nil || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return "", fmt.Errorf("dynamic context Binding CONFIG is unavailable")
	}
	config, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return "", err
	}
	return moduleapi.ContextBindingParametersSchemaVersionV1(config)
}

func knowledgeQueryScopeForRun(
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) moduleapi.KnowledgeQueryScopeV1 {
	return moduleapi.KnowledgeQueryScopeV1{
		TenantID: manifest.TenantID,
		Workspace: moduleapi.KnowledgeObjectRefV1{
			ID:      member.Workspace.ID,
			Version: member.Workspace.Version,
			Digest:  member.Workspace.Digest,
		},
		Agent: moduleapi.KnowledgeObjectRefV1{
			ID:      member.Agent.ID,
			Version: member.Agent.Version,
			Digest:  member.Agent.Digest,
		},
		TaskInputRef: manifest.TaskInputRef,
	}
}

func memoryQueryScopeForRun(
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) moduleapi.MemoryQueryScopeV1 {
	return moduleapi.MemoryQueryScopeV1{
		TenantID: manifest.TenantID,
		Workspace: moduleapi.MemoryObjectRefV1{
			ID:      member.Workspace.ID,
			Version: member.Workspace.Version,
			Digest:  member.Workspace.Digest,
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID:      member.Agent.ID,
			Version: member.Agent.Version,
			Digest:  member.Agent.Digest,
		},
		TaskInputRef: manifest.TaskInputRef,
	}
}

func validateKnowledgeRetrievalsForRun(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	bindings []frozenKnowledgeBinding,
) error {
	if len(compilation.KnowledgeRetrievals)+
		len(compilation.KnowledgeReuses)+
		len(compilation.KnowledgeShortcuts) != len(bindings) {
		return fmt.Errorf(
			"Knowledge retrieval, reuse, and shortcut evidence count does not match frozen dynamic Bindings",
		)
	}
	if len(bindings) == 0 {
		return nil
	}
	_, taskText, _, err := workspaceTransferCompilerInputForRunV1(run)
	if err != nil {
		return fmt.Errorf("restore frozen Knowledge query Task: %w", err)
	}
	decisionSet, decisionSetDigest, err := decideFrozenKnowledgeBindings(
		taskText,
		bindings,
	)
	if err != nil {
		return err
	}
	decisions := make(
		map[uint32]knowledgecore.BindingDecision,
		len(decisionSet.Decisions),
	)
	for _, decision := range decisionSet.Decisions {
		if _, duplicate := decisions[decision.BindingIndex]; duplicate {
			return fmt.Errorf(
				"duplicate Knowledge routing decision for Binding %d",
				decision.BindingIndex,
			)
		}
		decisions[decision.BindingIndex] = decision
	}
	retrievals := make(
		map[uint32]corecontract.KnowledgeRetrievalEvidenceV1,
		len(compilation.KnowledgeRetrievals),
	)
	for _, evidence := range compilation.KnowledgeRetrievals {
		if _, duplicate := retrievals[evidence.BindingIndex]; duplicate {
			return fmt.Errorf(
				"duplicate Knowledge retrieval for Binding %d",
				evidence.BindingIndex,
			)
		}
		retrievals[evidence.BindingIndex] = evidence
	}
	reuses := make(
		map[uint32]corecontract.KnowledgeReuseEvidenceV1,
		len(compilation.KnowledgeReuses),
	)
	for _, evidence := range compilation.KnowledgeReuses {
		bindingIndex := evidence.FreshRetrieval.BindingIndex
		if _, duplicate := reuses[bindingIndex]; duplicate {
			return fmt.Errorf(
				"duplicate Knowledge reuse for Binding %d",
				bindingIndex,
			)
		}
		reuses[bindingIndex] = evidence
	}
	shortcuts := make(
		map[uint32]corecontract.KnowledgeShortcutEvidenceV1,
		len(compilation.KnowledgeShortcuts),
	)
	for _, evidence := range compilation.KnowledgeShortcuts {
		if _, duplicate := shortcuts[evidence.BindingIndex]; duplicate {
			return fmt.Errorf(
				"duplicate Knowledge shortcut for Binding %d",
				evidence.BindingIndex,
			)
		}
		shortcuts[evidence.BindingIndex] = evidence
	}
	expectedMessages := make(
		[]moduleapi.ModelMessageV1,
		0,
		len(compilation.KnowledgeRetrievals)+len(compilation.KnowledgeReuses),
	)
	qualifiedBindings := qualifiedKnowledgeReuseBindingCount(decisionSet)
	var memoryBindings []frozenMemoryBinding
	if len(compilation.KnowledgeReuses) != 0 {
		memoryBindings, err = frozenMemoryBindingsForRun(
			run.Manifest,
			run.Member,
			runKnowledgeContentGetter(run),
		)
		if err != nil {
			return fmt.Errorf("frozen Knowledge reuse Memory closure: %w", err)
		}
	}
	for index, binding := range bindings {
		decision, found := decisions[binding.BindingIndex]
		if !found {
			return fmt.Errorf(
				"Knowledge Binding %d has no deterministic routing decision",
				binding.BindingIndex,
			)
		}
		retrieval, hasRetrieval := retrievals[binding.BindingIndex]
		reuse, hasReuse := reuses[binding.BindingIndex]
		shortcut, hasShortcut := shortcuts[binding.BindingIndex]
		outcomeCount := 0
		if hasRetrieval {
			outcomeCount++
		}
		if hasReuse {
			outcomeCount++
		}
		if hasShortcut {
			outcomeCount++
		}
		if outcomeCount != 1 {
			return fmt.Errorf(
				"Knowledge Binding %d must have exactly one retrieval, reuse, or shortcut outcome",
				binding.BindingIndex,
			)
		}
		switch decision.Decision {
		case knowledgecore.DecisionNotSelected:
			if !hasShortcut {
				return fmt.Errorf(
					"Knowledge Binding %d deterministic NOT_SELECTED outcome is not a shortcut",
					binding.BindingIndex,
				)
			}
			if shortcut.BindingIndex != binding.BindingIndex ||
				shortcut.ConfigRef != binding.Binding.ConfigRef ||
				shortcut.AuthorityCeilingRef !=
					binding.Binding.AuthorityCeilingRef ||
				shortcut.Scope != binding.Scope ||
				shortcut.Source != binding.Config.Source ||
				shortcut.DecisionSetDigest != decisionSetDigest ||
				shortcut.ExactQuestionFingerprint !=
					decision.ExactQuestionFingerprint ||
				!slices.Equal(
					shortcut.CollectionTags,
					decision.CollectionTags,
				) ||
				!slices.Equal(
					shortcut.MatchedTerms,
					decision.MatchedTerms,
				) ||
				shortcut.MinMatchTerms != decision.MinMatchTerms ||
				shortcut.Mode !=
					corecontract.KnowledgeShortcutNotSelectedV1 {
				return fmt.Errorf(
					"Knowledge shortcut %d does not match its exact frozen Binding and routing decision",
					index,
				)
			}
			continue
		case knowledgecore.DecisionFreshRAG:
			if !hasRetrieval && !hasReuse {
				return fmt.Errorf(
					"Knowledge Binding %d deterministic FRESH_RAG outcome is not a retrieval or reuse",
					binding.BindingIndex,
				)
			}
		default:
			return fmt.Errorf(
				"Knowledge Binding %d has unsupported routing decision %q",
				binding.BindingIndex,
				decision.Decision,
			)
		}
		if hasRetrieval {
			validated, err := validateFreshKnowledgeRetrievalForBinding(
				retrieval,
				binding,
				taskText,
			)
			if err != nil {
				return fmt.Errorf("Knowledge retrieval %d: %w", index, err)
			}
			if err := validateFreshKnowledgeProvenanceForBinding(
				retrieval,
				binding,
				decisionSetDigest,
			); err != nil {
				return fmt.Errorf("Knowledge retrieval %d: %w", index, err)
			}
			expectedMessages = append(expectedMessages, validated.Message)
			continue
		}
		validated, err := validateKnowledgeReuseForRun(
			compilation,
			run,
			taskText,
			decisionSetDigest,
			qualifiedBindings,
			binding,
			decision,
			reuse,
			memoryBindings,
			nil,
		)
		if err != nil {
			return fmt.Errorf("Knowledge reuse %d: %w", index, err)
		}
		expectedMessages = append(expectedMessages, validated.Message)
	}
	if !containsKnowledgeMessagesInOrder(request.Messages, expectedMessages) {
		return fmt.Errorf(
			"final model request does not contain frozen Knowledge evidence in Binding order",
		)
	}
	return nil
}

type validatedFreshKnowledgeV1 struct {
	Request moduleapi.KnowledgeContextRequestV1
	Output  moduleapi.KnowledgeContextOutputV1
	Message moduleapi.ModelMessageV1
}

func validateFreshKnowledgeRetrievalForBinding(
	evidence corecontract.KnowledgeRetrievalEvidenceV1,
	binding frozenKnowledgeBinding,
	taskText string,
) (validatedFreshKnowledgeV1, error) {
	if evidence.BindingIndex != binding.BindingIndex ||
		evidence.ConfigRef != binding.Binding.ConfigRef ||
		evidence.AuthorityCeilingRef != binding.Binding.AuthorityCeilingRef ||
		evidence.Scope != binding.Scope ||
		evidence.Source != binding.Config.Source {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"evidence does not match its frozen Binding and scope",
		)
	}
	request, _, requestDigest, err := moduleapi.NewKnowledgeContextRequestV1(
		moduleapi.KnowledgeContextRequestV1{
			SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
			Source:            binding.Config.Source,
			Scope:             binding.Scope,
			QueryText:         taskText,
			MaxHits:           binding.MaxHits,
			MaxTotalTextBytes: binding.MaxTextBytes,
		},
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf("rebuild request: %w", err)
	}
	if evidence.RequestDigest != requestDigest {
		return validatedFreshKnowledgeV1{}, fmt.Errorf("request digest mismatch")
	}
	output, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: evidence.RequestDigest,
			Source:        evidence.Source,
			Hits:          evidence.Hits,
		},
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf("restore output: %w", err)
	}
	if evidence.OutputDigest != outputDigest {
		return validatedFreshKnowledgeV1{}, fmt.Errorf("output digest mismatch")
	}
	if err := moduleapi.ValidateKnowledgeContextOutputForRequestV1(
		request,
		output,
	); err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"request/output closure: %w",
			err,
		)
	}
	message, err := contextcompiler.KnowledgeContextMessageV1(output)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"rebuild model message: %w",
			err,
		)
	}
	return validatedFreshKnowledgeV1{
		Request: request,
		Output:  output,
		Message: message,
	}, nil
}

func validateFreshKnowledgeProvenanceForBinding(
	evidence corecontract.KnowledgeRetrievalEvidenceV1,
	binding frozenKnowledgeBinding,
	decisionSetDigest string,
) error {
	reuseEnabled := binding.Config.Routing != nil &&
		binding.Config.Routing.Reuse != nil
	if !reuseEnabled {
		if evidence.Provenance != nil {
			return fmt.Errorf("provenance is present while reuse is disabled")
		}
		return nil
	}
	if evidence.Provenance == nil {
		return fmt.Errorf("provenance is absent while reuse is enabled")
	}
	if evidence.Provenance.Provider != binding.Binding.Provider ||
		evidence.Provenance.RoutingAlgorithmVersion !=
			knowledgecore.RoutingAlgorithmVersionV1 ||
		evidence.Provenance.DecisionSetDigest != decisionSetDigest {
		return fmt.Errorf(
			"provenance does not match the frozen Provider and routing decision",
		)
	}
	return nil
}

func qualifiedKnowledgeReuseBindingCount(
	set knowledgecore.DecisionSet,
) int {
	qualified := 0
	for _, decision := range set.Decisions {
		if decision.MinMatchTerms != 0 &&
			uint32(len(decision.MatchedTerms)) >= decision.MinMatchTerms {
			qualified++
		}
	}
	return qualified
}

func validateKnowledgeReuseForRun(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	taskText string,
	decisionSetDigest string,
	qualifiedBindings int,
	binding frozenKnowledgeBinding,
	decision knowledgecore.BindingDecision,
	reuse corecontract.KnowledgeReuseEvidenceV1,
	memoryBindings []frozenMemoryBinding,
	evaluatedAtOverride *uint64,
) (validatedFreshKnowledgeV1, error) {
	if binding.Config.Routing == nil || binding.Config.Routing.Reuse == nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"reuse is present while the exact Binding policy is disabled",
		)
	}
	if qualifiedBindings != 1 || decision.MinMatchTerms == 0 ||
		uint32(len(decision.MatchedTerms)) < decision.MinMatchTerms {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"reuse requires exactly one routed Binding above its confidence threshold",
		)
	}
	validated, err := validateFreshKnowledgeRetrievalForBinding(
		reuse.FreshRetrieval,
		binding,
		taskText,
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"nested fresh retrieval: %w",
			err,
		)
	}
	if err := validateFreshKnowledgeProvenanceForBinding(
		reuse.FreshRetrieval,
		binding,
		decisionSetDigest,
	); err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"nested fresh retrieval: %w",
			err,
		)
	}
	turn := run.Manifest.ConversationTurn
	if turn == nil || reuse.SourceConversationID != turn.ConversationID ||
		reuse.SourceTurnIndex >= turn.TurnIndex {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source is outside the current Conversation",
		)
	}
	source, err := latestExactKnowledgeReuseSource(run)
	if err != nil {
		return validatedFreshKnowledgeV1{}, err
	}
	if reuse.SourceTurnIndex != source.TurnIndex ||
		reuse.SourceRunID != source.SourceRunID ||
		reuse.SourceAttemptID != source.SourceAttemptID ||
		source.SourceContextCompilation == nil ||
		reuse.SourceCompilationRef != source.SourceContextCompilation.Digest {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source does not match the latest exact-question successful candidate",
		)
	}
	if source.SourceContextCompilationAttemptID == "" ||
		source.SourceContextCompilationAttemptID != source.SourceAttemptID {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source Compilation does not belong to the successful terminal Attempt",
		)
	}
	if source.SourceContextCompilation.Kind != ContentContextCompilation ||
		source.SourceContextCompilation.MediaType != admissionJSONMediaType {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source Context Compilation identity is invalid",
		)
	}
	sourceCompilation, err := corecontract.RestoreContextCompilationV1(
		source.SourceContextCompilation.CanonicalBytes,
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"restore source Context Compilation: %w",
			err,
		)
	}
	directFresh := false
	for _, retrieval := range sourceCompilation.KnowledgeRetrievals {
		if reflect.DeepEqual(retrieval, reuse.FreshRetrieval) {
			directFresh = true
			break
		}
	}
	if !directFresh {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source Compilation does not contain the exact direct fresh retrieval",
		)
	}

	evaluatedAt, category, repeated, err := knowledgeReuseCounterProofForRun(
		compilation,
		run,
		binding,
		decision,
		reuse,
		memoryBindings,
		evaluatedAtOverride,
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, err
	}
	lookback := turn.TurnIndex - reuse.SourceTurnIndex
	if lookback > uint64(moduleapi.MaxKnowledgeReuseLookbackTurnsV1) {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source is outside the bounded reuse lookback",
		)
	}
	provenance := reuse.FreshRetrieval.Provenance
	if provenance == nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"source fresh retrieval provenance is absent",
		)
	}
	evaluation, err := knowledgecore.EvaluateReuseV1(
		knowledgecore.ReuseEvaluationInputV1{
			Policy:            *binding.Config.Routing.Reuse,
			EvaluatedAtUnixMS: evaluatedAt,
			Current: knowledgecore.ReuseCurrentFactsV1{
				ConversationID:          turn.ConversationID,
				Request:                 validated.Request,
				Provider:                binding.Binding.Provider,
				ConfigDigest:            binding.Binding.ConfigRef,
				AuthorityDigest:         binding.Binding.AuthorityCeilingRef,
				RoutingAlgorithmVersion: knowledgecore.RoutingAlgorithmVersionV1,
				DecisionSetDigest:       decisionSetDigest,
				Decision:                decision,
			},
			Candidate: knowledgecore.ReuseCandidateV1{
				ConversationID:                 reuse.SourceConversationID,
				SourceAttemptID:                reuse.SourceAttemptID,
				SourceCompilationDigest:        reuse.SourceCompilationRef,
				Request:                        validated.Request,
				Output:                         validated.Output,
				Provider:                       provenance.Provider,
				ConfigDigest:                   reuse.FreshRetrieval.ConfigRef,
				AuthorityDigest:                reuse.FreshRetrieval.AuthorityCeilingRef,
				BindingIndex:                   reuse.FreshRetrieval.BindingIndex,
				RoutingAlgorithmVersion:        provenance.RoutingAlgorithmVersion,
				DecisionSetDigest:              provenance.DecisionSetDigest,
				RetrievedAtUnixMS:              provenance.RetrievedAtUnixMS,
				LookbackTurns:                  uint32(lookback),
				IsLatestExactQuestionCandidate: true,
				FreshRetrieval:                 true,
				SourceAttemptSucceeded:         true,
			},
			CategoryCounter:     category,
			RepeatedTermCounter: repeated,
		},
	)
	if err != nil {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"evaluate exact reuse: %w",
			err,
		)
	}
	if evaluation.Decision != knowledgecore.DecisionReuse {
		return validatedFreshKnowledgeV1{}, fmt.Errorf(
			"exact reuse evaluation fell back to fresh retrieval",
		)
	}
	return validated, nil
}

func latestExactKnowledgeReuseSource(
	run RunForLoop,
) (ConversationHistoryTurnRecord, error) {
	if run.Manifest.ConversationTurn == nil {
		return ConversationHistoryTurnRecord{}, fmt.Errorf(
			"reuse requires a Conversation Run",
		)
	}
	for index := len(run.ConversationHistory) - 1; index >= 0; index-- {
		candidate := run.ConversationHistory[index]
		if candidate.UserContent.Digest != run.Manifest.TaskInputRef {
			continue
		}
		return candidate, nil
	}
	return ConversationHistoryTurnRecord{}, fmt.Errorf(
		"latest exact-question candidate is absent",
	)
}

func knowledgeReuseCounterProofForRun(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	knowledgeBinding frozenKnowledgeBinding,
	decision knowledgecore.BindingDecision,
	reuse corecontract.KnowledgeReuseEvidenceV1,
	memoryBindings []frozenMemoryBinding,
	evaluatedAtOverride *uint64,
) (uint64, moduleapi.MemoryCandidateV1, moduleapi.MemoryCandidateV1, error) {
	var memoryBinding *frozenMemoryBinding
	for index := range memoryBindings {
		if memoryBindings[index].BindingIndex == reuse.MemoryBindingIndex {
			memoryBinding = &memoryBindings[index]
			break
		}
	}
	if memoryBinding == nil {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory proof Binding is absent")
	}
	var memoryEvidence *corecontract.MemoryReadEvidenceV1
	for index := range compilation.MemoryReads {
		if compilation.MemoryReads[index].BindingIndex == reuse.MemoryBindingIndex {
			memoryEvidence = &compilation.MemoryReads[index]
			break
		}
	}
	if memoryEvidence == nil ||
		memoryEvidence.ConfigRef != memoryBinding.Binding.ConfigRef ||
		memoryEvidence.AuthorityCeilingRef !=
			memoryBinding.Binding.AuthorityCeilingRef ||
		memoryEvidence.Scope != memoryBinding.Scope {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory proof does not match its frozen Binding and scope")
	}
	snapshotContent, found := run.FindContent(memoryEvidence.Snapshot.Digest)
	if !found || snapshotContent.Kind != ContentMemorySnapshot ||
		snapshotContent.MediaType != admissionJSONMediaType {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory proof snapshot is unavailable")
	}
	snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(
		snapshotContent.CanonicalBytes,
	)
	if err != nil {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("restore Memory proof snapshot: %w", err)
	}
	exactRef := moduleapi.MemorySnapshotRefV1{
		TenantID: snapshot.TenantID,
		AgentID:  snapshot.AgentID,
		Revision: snapshot.Revision,
		Digest:   snapshotContent.Digest,
	}
	if exactRef != memoryEvidence.Snapshot {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory proof snapshot body differs from its evidence ref")
	}
	evaluatedAt := memoryEvidence.EvaluatedAtUnixMS
	if evaluatedAtOverride != nil {
		evaluatedAt = *evaluatedAtOverride
	}
	policy := knowledgeBinding.Config.Routing.Reuse
	proof, found, err := memorycore.SelectKnowledgeReuseCounterProofV1(
		snapshot,
		exactRef,
		memoryBinding.Scope,
		memoryBinding.Config,
		memoryBinding.Authority,
		evaluatedAt,
		decision.CollectionTags,
		decision.MatchedTerms,
		policy.MinCategoryCount,
		policy.MinRepeatedTermCount,
	)
	if err != nil {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("select Memory counter proof: %w", err)
	}
	if !found {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory counter proof is no longer eligible")
	}
	category, err := moduleapi.NewMemoryCandidateV1(proof.CategoryCounter)
	if err != nil {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("project category counter: %w", err)
	}
	repeated, err := moduleapi.NewMemoryCandidateV1(proof.RepeatedTermCounter)
	if err != nil {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("project repeated-term counter: %w", err)
	}
	if category != reuse.CategoryCounter || repeated != reuse.RepeatedTermCounter {
		return 0, moduleapi.MemoryCandidateV1{}, moduleapi.MemoryCandidateV1{},
			fmt.Errorf("Memory counter proof differs from the deterministic selector")
	}
	return evaluatedAt, category, repeated, nil
}

func validateKnowledgeReusesForRunAtEvaluationTime(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
	evaluatedAtUnixMS uint64,
) error {
	if len(compilation.KnowledgeReuses) == 0 {
		return nil
	}
	_, taskText, _, err := workspaceTransferCompilerInputForRunV1(run)
	if err != nil {
		return fmt.Errorf("restore frozen Knowledge reuse Task: %w", err)
	}
	decisionSet, decisionSetDigest, err := decideFrozenKnowledgeBindings(
		taskText,
		knowledgeBindings,
	)
	if err != nil {
		return err
	}
	decisions := make(
		map[uint32]knowledgecore.BindingDecision,
		len(decisionSet.Decisions),
	)
	for _, decision := range decisionSet.Decisions {
		decisions[decision.BindingIndex] = decision
	}
	bindings := make(map[uint32]frozenKnowledgeBinding, len(knowledgeBindings))
	for _, binding := range knowledgeBindings {
		bindings[binding.BindingIndex] = binding
	}
	qualifiedBindings := qualifiedKnowledgeReuseBindingCount(decisionSet)
	for index, reuse := range compilation.KnowledgeReuses {
		bindingIndex := reuse.FreshRetrieval.BindingIndex
		binding, found := bindings[bindingIndex]
		if !found {
			return fmt.Errorf(
				"Knowledge reuse %d has no frozen Binding",
				index,
			)
		}
		decision, found := decisions[bindingIndex]
		if !found {
			return fmt.Errorf(
				"Knowledge reuse %d has no deterministic routing decision",
				index,
			)
		}
		if _, err := validateKnowledgeReuseForRun(
			compilation,
			run,
			taskText,
			decisionSetDigest,
			qualifiedBindings,
			binding,
			decision,
			reuse,
			memoryBindings,
			&evaluatedAtUnixMS,
		); err != nil {
			return fmt.Errorf(
				"Knowledge reuse %d current-head validation: %w",
				index,
				err,
			)
		}
	}
	return nil
}

func checkCurrentKnowledgeReuseActivations(
	ctx context.Context,
	queryer publicationQueryer,
	run RunForLoop,
	record *ContentRecord,
) error {
	if record == nil {
		return nil
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("restore context-compilation/v1: %w", err)
	}
	if len(compilation.KnowledgeReuses) == 0 {
		return nil
	}
	bindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen Knowledge reuse activation closure: %w", err)
	}
	byIndex := make(map[uint32]frozenKnowledgeBinding, len(bindings))
	for _, binding := range bindings {
		byIndex[binding.BindingIndex] = binding
	}
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen Knowledge reuse Memory activation closure: %w", err)
	}
	memoryByIndex := make(map[uint32]frozenMemoryBinding, len(memoryBindings))
	for _, binding := range memoryBindings {
		memoryByIndex[binding.BindingIndex] = binding
	}
	checked := make(map[moduleapi.ActivatedModuleRef]struct{})
	checkProvider := func(provider moduleapi.ActivatedModuleRef) error {
		if _, duplicate := checked[provider]; duplicate {
			return nil
		}
		if err := checkCurrentActivation(
			ctx,
			queryer,
			run.RunID,
			moduleapi.PortRef{
				Name:         moduleapi.PortNameContextProvide,
				ExactVersion: moduleapi.PortVersionV1,
			},
			provider,
		); err != nil {
			return err
		}
		checked[provider] = struct{}{}
		return nil
	}
	for index, reuse := range compilation.KnowledgeReuses {
		binding, found := byIndex[reuse.FreshRetrieval.BindingIndex]
		if !found {
			return fmt.Errorf(
				"Knowledge reuse %d has no frozen Provider",
				index,
			)
		}
		memoryBinding, found := memoryByIndex[reuse.MemoryBindingIndex]
		if !found {
			return fmt.Errorf(
				"Knowledge reuse %d has no frozen Memory proof Provider",
				index,
			)
		}
		if err := checkProvider(binding.Binding.Provider); err != nil {
			return fmt.Errorf(
				"Knowledge reuse Provider is not currently active: %w",
				err,
			)
		}
		if err := checkProvider(memoryBinding.Binding.Provider); err != nil {
			return fmt.Errorf(
				"Knowledge reuse Memory Provider is not currently active: %w",
				err,
			)
		}
	}
	return nil
}

func decideFrozenKnowledgeBindings(
	taskText string,
	bindings []frozenKnowledgeBinding,
) (knowledgecore.DecisionSet, string, error) {
	inputs := make([]knowledgecore.BindingInput, len(bindings))
	for index, binding := range bindings {
		inputs[index] = knowledgecore.BindingInput{
			BindingIndex: binding.BindingIndex,
			Config:       binding.Config,
		}
	}
	set, _, digest, err := knowledgecore.Decide(taskText, inputs)
	if err != nil {
		return knowledgecore.DecisionSet{}, "", fmt.Errorf(
			"recompute frozen Knowledge routing decision: %w",
			err,
		)
	}
	if len(set.Decisions) != len(bindings) {
		return knowledgecore.DecisionSet{}, "", fmt.Errorf(
			"frozen Knowledge routing decision count mismatch",
		)
	}
	return set, digest, nil
}

func validateMemoryReadsForRun(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	bindings []frozenMemoryBinding,
) error {
	if len(compilation.MemoryReads) != len(bindings) {
		return fmt.Errorf(
			"Memory read evidence count does not match frozen dynamic Bindings",
		)
	}
	if len(bindings) == 0 {
		return nil
	}
	_, taskText, _, err := workspaceTransferCompilerInputForRunV1(run)
	if err != nil {
		return fmt.Errorf("restore frozen Memory query Task: %w", err)
	}
	expectedMessages := make([]moduleapi.ModelMessageV1, len(bindings))
	for index, binding := range bindings {
		evidence := compilation.MemoryReads[index]
		if evidence.BindingIndex != binding.BindingIndex ||
			evidence.ConfigRef != binding.Binding.ConfigRef ||
			evidence.AuthorityCeilingRef !=
				binding.Binding.AuthorityCeilingRef ||
			evidence.Scope != binding.Scope {
			return fmt.Errorf(
				"Memory read %d does not match its frozen Binding and scope",
				index,
			)
		}
		snapshotContent, found := run.FindContent(evidence.Snapshot.Digest)
		if !found || snapshotContent.Kind != ContentMemorySnapshot ||
			snapshotContent.MediaType != admissionJSONMediaType {
			return fmt.Errorf(
				"Memory read %d exact snapshot content is unavailable",
				index,
			)
		}
		snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(
			snapshotContent.CanonicalBytes,
		)
		if err != nil {
			return fmt.Errorf(
				"restore Memory read %d snapshot: %w",
				index,
				err,
			)
		}
		exactRef := moduleapi.MemorySnapshotRefV1{
			TenantID: snapshot.TenantID,
			AgentID:  snapshot.AgentID,
			Revision: snapshot.Revision,
			Digest:   snapshotContent.Digest,
		}
		if exactRef != evidence.Snapshot {
			return fmt.Errorf(
				"Memory read %d snapshot body differs from evidence ref",
				index,
			)
		}
		candidates, resolved, err := memorycore.FilterCandidates(
			snapshot,
			exactRef,
			binding.Scope,
			binding.Config,
			binding.Authority,
			evidence.EvaluatedAtUnixMS,
		)
		if err != nil {
			return fmt.Errorf(
				"rebuild Memory read %d candidates: %w",
				index,
				err,
			)
		}
		expectedRequest, _, requestDigest, err :=
			moduleapi.NewMemoryContextRequestV1(
				moduleapi.MemoryContextRequestV1{
					SchemaVersion: moduleapi.
						MemoryContextRequestSchemaV1,
					Snapshot:          exactRef,
					Scope:             binding.Scope,
					QueryText:         taskText,
					EvaluatedAtUnixMS: evidence.EvaluatedAtUnixMS,
					Candidates:        candidates,
					MaxItems:          resolved.MaxItems,
					MaxTotalTextBytes: resolved.MaxTotalTextBytes,
				},
			)
		if err != nil {
			return fmt.Errorf(
				"rebuild Memory read %d request: %w",
				index,
				err,
			)
		}
		if evidence.RequestDigest != requestDigest {
			return fmt.Errorf(
				"Memory read %d request digest mismatch",
				index,
			)
		}
		candidateByDigest := make(
			map[string]moduleapi.MemoryCandidateV1,
			len(expectedRequest.Candidates),
		)
		for _, candidate := range expectedRequest.Candidates {
			candidateByDigest[candidate.EntryDigest] = candidate
		}
		selectedDigests := make([]string, len(evidence.SelectedEntries))
		for selectedIndex, selected := range evidence.SelectedEntries {
			candidate, found := candidateByDigest[selected.EntryDigest]
			if !found || candidate != selected {
				return fmt.Errorf(
					"Memory read %d selected entry %d differs from exact snapshot candidates",
					index,
					selectedIndex,
				)
			}
			selectedDigests[selectedIndex] = selected.EntryDigest
		}
		output, _, outputDigest, err := moduleapi.NewMemoryContextOutputV1(
			moduleapi.MemoryContextOutputV1{
				SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
				RequestDigest:        requestDigest,
				Snapshot:             exactRef,
				SelectedEntryDigests: selectedDigests,
			},
		)
		if err != nil {
			return fmt.Errorf(
				"rebuild Memory read %d output: %w",
				index,
				err,
			)
		}
		if evidence.OutputDigest != outputDigest {
			return fmt.Errorf(
				"Memory read %d output digest mismatch",
				index,
			)
		}
		if err := moduleapi.ValidateMemoryContextOutputForRequestV1(
			expectedRequest,
			output,
		); err != nil {
			return fmt.Errorf(
				"Memory read %d request/output closure: %w",
				index,
				err,
			)
		}
		expectedMessages[index], err =
			contextcompiler.MemoryContextMessageV1(evidence.SelectedEntries)
		if err != nil {
			return fmt.Errorf(
				"rebuild Memory read %d model message: %w",
				index,
				err,
			)
		}
	}
	if !containsKnowledgeMessagesInOrder(request.Messages, expectedMessages) {
		return fmt.Errorf(
			"final model request does not contain frozen Memory evidence in Binding order",
		)
	}
	return nil
}

func validateDynamicContextMessageOrder(
	compilation corecontract.ContextCompilationV1,
	request moduleapi.ModelGenerateRequestV1,
) error {
	type indexedMessage struct {
		index   uint32
		message moduleapi.ModelMessageV1
	}
	indexed := make(
		[]indexedMessage,
		0,
		len(compilation.KnowledgeRetrievals)+len(compilation.KnowledgeReuses)+
			len(compilation.MemoryReads),
	)
	for _, evidence := range compilation.KnowledgeRetrievals {
		output, _, _, err := moduleapi.NewKnowledgeContextOutputV1(
			moduleapi.KnowledgeContextOutputV1{
				SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
				RequestDigest: evidence.RequestDigest,
				Source:        evidence.Source,
				Hits:          evidence.Hits,
			},
		)
		if err != nil {
			return fmt.Errorf("rebuild Knowledge prompt envelope: %w", err)
		}
		message, err := contextcompiler.KnowledgeContextMessageV1(output)
		if err != nil {
			return err
		}
		indexed = append(indexed, indexedMessage{
			index: evidence.BindingIndex, message: message,
		})
	}
	for _, evidence := range compilation.KnowledgeReuses {
		fresh := evidence.FreshRetrieval
		output, _, _, err := moduleapi.NewKnowledgeContextOutputV1(
			moduleapi.KnowledgeContextOutputV1{
				SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
				RequestDigest: fresh.RequestDigest,
				Source:        fresh.Source,
				Hits:          fresh.Hits,
			},
		)
		if err != nil {
			return fmt.Errorf("rebuild reused Knowledge prompt envelope: %w", err)
		}
		message, err := contextcompiler.KnowledgeContextMessageV1(output)
		if err != nil {
			return err
		}
		indexed = append(indexed, indexedMessage{
			index: fresh.BindingIndex, message: message,
		})
	}
	for _, evidence := range compilation.MemoryReads {
		message, err := contextcompiler.MemoryContextMessageV1(
			evidence.SelectedEntries,
		)
		if err != nil {
			return fmt.Errorf("rebuild Memory prompt envelope: %w", err)
		}
		indexed = append(indexed, indexedMessage{
			index: evidence.BindingIndex, message: message,
		})
	}
	sort.Slice(indexed, func(left, right int) bool {
		return indexed[left].index < indexed[right].index
	})
	expected := make([]moduleapi.ModelMessageV1, len(indexed))
	for index := range indexed {
		expected[index] = indexed[index].message
	}
	if !containsKnowledgeMessagesInOrder(request.Messages, expected) {
		return fmt.Errorf(
			"final model request dynamic context messages differ from Binding order",
		)
	}
	return nil
}

// validateCompilerOutputForNewAttempt closes the final gap between the frozen
// Run closure and the bytes that may first reach model.generate/v1. It feeds
// static material and any already-produced Knowledge evidence through the one
// pure Context Compiler, then requires its complete outputs byte-for-byte.
// Recovery and Store loads deliberately do not call this function.
func validateCompilerOutputForNewAttempt(
	compilation corecontract.ContextCompilationV1,
	compilationCanonical []byte,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	attemptFrameRevision uint64,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
) error {
	// Every new Conversation Attempt is byte-for-byte recompiled because its
	// direct-predecessor summary candidate is an in-memory optimization, never
	// an independently persisted fact. Non-Conversation legacy/declarative-only
	// attempts preserve their existing structural admission boundary.
	if len(knowledgeBindings) == 0 && len(memoryBindings) == 0 &&
		run.Manifest.Composite == nil && run.Manifest.ConversationTurn == nil {
		return nil
	}
	compiled, err := recompileContextForNewAttempt(
		compilation,
		run,
		attemptFrameRevision,
		knowledgeBindings,
		memoryBindings,
	)
	if err != nil {
		return fmt.Errorf("compile frozen context: %w", err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		return fmt.Errorf("rebuild final model request: %w", err)
	}
	if !bytes.Equal(compiled.RequestCanonical, requestCanonical) {
		return fmt.Errorf(
			"final model request is not the exact Context Compiler output",
		)
	}
	if len(compilationCanonical) == 0 {
		if compiled.Compilation != nil ||
			len(compiled.CompilationCanonical) != 0 {
			return fmt.Errorf(
				"context compilation omission differs from the Context Compiler output",
			)
		}
		return nil
	}
	if compiled.Compilation == nil ||
		!bytes.Equal(
			compiled.CompilationCanonical,
			compilationCanonical,
		) {
		return fmt.Errorf(
			"context compilation is not the exact Context Compiler output",
		)
	}
	return nil
}

func recompileContextForNewAttempt(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	attemptFrameRevision uint64,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
) (contextcompiler.CompileResultV1, error) {
	modelBinding, err := exactModelBinding(run.Member)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}
	modelConfig, err := frozenModelBindingConfig(run, modelBinding)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}
	policy, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found || policy.Kind != ContentPolicy ||
		policy.MediaType != admissionJSONMediaType {
		return contextcompiler.CompileResultV1{},
			fmt.Errorf("frozen ContextPolicy is unavailable")
	}
	task, taskText, workspaceTransfers, err :=
		workspaceTransferCompilerInputForRunV1(run)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}

	contextPlan, materials, err :=
		knowledgeCompilerBindingsForRun(
			compilation,
			run,
			taskText,
			knowledgeBindings,
			memoryBindings,
		)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}
	history, conversationHistory, err := knowledgeCompilerHistoryForRun(
		run,
		attemptFrameRevision,
	)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}
	conversationSummaryCandidate, err :=
		ConversationSummaryCandidateForCompilerV1(run)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}

	var modelProfileRef *corecontract.ModelProfileRef
	var modelProfileCanonical []byte
	if run.Member.ModelProfile != nil {
		profile, found := run.FindContent(run.Member.ModelProfile.Digest)
		if !found || profile.Kind != ContentConfig ||
			profile.MediaType != admissionJSONMediaType {
			return contextcompiler.CompileResultV1{},
				fmt.Errorf("frozen ModelProfile is unavailable")
		}
		ref := *run.Member.ModelProfile
		modelProfileRef = &ref
		modelProfileCanonical = bytes.Clone(profile.CanonicalBytes)
	}
	composite, err := compositeCompilerMaterialsForRun(run)
	if err != nil {
		return contextcompiler.CompileResultV1{}, err
	}
	modelParameters := bytes.Clone(modelConfig.Parameters)
	if run.Manifest.Composite != nil &&
		run.Manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1 {
		if run.CompositeRoot == nil || run.CompositeRoot.Composite == nil ||
			run.CompositeRoot.Composite.Plan == nil ||
			run.CompositeRoot.Composite.Plan.Reviewer == nil {
			return contextcompiler.CompileResultV1{}, fmt.Errorf(
				"Reviewer parameter ceiling is absent from the frozen root plan",
			)
		}
		plannedReviewer := run.CompositeRoot.Composite.Plan.Reviewer
		if run.Manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 {
			if run.CompositeRoot.Composite.Plan.Decision == nil {
				return contextcompiler.CompileResultV1{}, fmt.Errorf(
					"repair Reviewer parameter ceiling is absent from the frozen root plan",
				)
			}
			repairReviewer := run.CompositeRoot.Composite.Plan.Decision.RepairReviewer
			plannedReviewer = &repairReviewer
		}
		if plannedReviewer.RunID != run.RunID {
			return contextcompiler.CompileResultV1{}, fmt.Errorf(
				"Reviewer parameter ceiling differs from the frozen participant",
			)
		}
		modelParameters, err = corecontract.TightenReviewerModelParametersV1(
			modelParameters,
			plannedReviewer.MaxOutputTokens,
		)
		if err != nil {
			return contextcompiler.CompileResultV1{}, fmt.Errorf(
				"tighten frozen Reviewer model parameters: %w",
				err,
			)
		}
	}

	return contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                        run.Manifest.TenantID,
		WorkspaceScope:                  run.Member.Workspace,
		AgentScope:                      run.Member.Agent,
		ContextPolicyRef:                run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical:  policy.CanonicalBytes,
		ModelProfileRef:                 modelProfileRef,
		ModelProfileCanonical:           modelProfileCanonical,
		ModelParameters:                 modelParameters,
		ContextPlan:                     contextPlan,
		ContextBindings:                 materials,
		Actions:                         run.Member.Actions,
		HistoryTurns:                    history,
		ConversationHistoryTurns:        conversationHistory,
		ConversationSummaryCandidate:    conversationSummaryCandidate,
		TaskInputRef:                    run.Manifest.TaskInputRef,
		TaskInputCanonical:              task.CanonicalBytes,
		Composite:                       composite.Node,
		CompositeChildResults:           composite.Children,
		CompositeSpecialistResultSet:    composite.SpecialistResultSet,
		CompositeSpecialistResultDigest: composite.SpecialistResultDigest,
		CompositeReviewVerdict:          composite.ReviewVerdict,
		CompositeCollaboration:          composite.Collaboration,
		WorkspaceTransfers:              workspaceTransfers,
	})
}

// workspaceTransferCompilerInputForRunV1 rebuilds the same Host-only task
// view used by CoreLoop. A transferred Child receives the trusted summary as
// dynamic query text while the original TASK_INPUT remains available only to
// the pure compiler's lineage proof. Root and Reviewer inputs include the
// exact persisted RESULT records in effective Child-result order.
func workspaceTransferCompilerInputForRunV1(
	run RunForLoop,
) (ContentRecord, string, []contextcompiler.WorkspaceTransferMaterialV1, error) {
	if run.WorkspaceTransfer != nil {
		record, err := PrepareWorkspaceTransferRequestV1(run)
		if err != nil || record == nil {
			if err == nil {
				err = fmt.Errorf("request transfer record is absent")
			}
			return ContentRecord{}, "", nil, fmt.Errorf(
				"prepare Workspace transfer compiler input: %w",
				err,
			)
		}
		summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
			record.Payload.CanonicalBytes,
		)
		if err != nil {
			return ContentRecord{}, "", nil, fmt.Errorf(
				"restore Workspace transfer compiler task summary: %w",
				err,
			)
		}
		return cloneContentRecord(run.WorkspaceTransfer.RootTaskInput),
			summary.Summary,
			[]contextcompiler.WorkspaceTransferMaterialV1{
				record.ContextMaterialV1(),
			},
			nil
	}

	task, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found || task.Kind != ContentTaskInput ||
		task.MediaType != admissionJSONMediaType {
		return ContentRecord{}, "", nil, fmt.Errorf(
			"frozen TaskInput is unavailable",
		)
	}
	taskValue, err := corecontract.RestoreTaskInputV1(task.CanonicalBytes)
	if err != nil {
		return ContentRecord{}, "", nil, fmt.Errorf(
			"restore frozen TaskInput: %w",
			err,
		)
	}
	var transfers []contextcompiler.WorkspaceTransferMaterialV1
	for index := range run.CompositeChildren {
		if run.CompositeChildren[index].WorkspaceTransfer == nil {
			continue
		}
		transfers = append(
			transfers,
			run.CompositeChildren[index].WorkspaceTransfer.ContextMaterialV1(),
		)
	}
	return task, taskValue.Text, transfers, nil
}

type compositeCompilerMaterialsV1 struct {
	Node                   *corecontract.CompositeRunNodeV1
	Children               []contextcompiler.CompositeChildResultV1
	SpecialistResultSet    *corecontract.CompositeSpecialistResultSetV1
	SpecialistResultDigest string
	ReviewVerdict          *contextcompiler.CompositeReviewVerdictMaterialV1
	Collaboration          *contextcompiler.CompositeCollaborationMaterialV1
}

func compositeCompilerMaterialsForRun(
	run RunForLoop,
) (compositeCompilerMaterialsV1, error) {
	if run.Manifest.Composite == nil {
		if len(run.CompositeChildren) != 0 || run.CompositeRoot != nil ||
			run.CompositeReviewer != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"non-composite Run exposes composite Child results",
			)
		}
		return compositeCompilerMaterialsV1{}, nil
	}
	nodeValue := cloneCompositeRunNodeForSemanticCompilerV1(
		*run.Manifest.Composite,
	)
	node := &nodeValue
	root, collaboration, err := collaborationRootForSemanticCompilerV1(run)
	if err != nil {
		return compositeCompilerMaterialsV1{}, err
	}
	if collaboration {
		return collaborationCompilerMaterialsForRunV1(run, node, root)
	}
	if node.Role == corecontract.CompositeRunRoleChildV1 {
		if len(run.CompositeChildren) != 0 || run.CompositeRoot != nil ||
			run.CompositeReviewer != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite Child exposes sibling results",
			)
		}
		return compositeCompilerMaterialsV1{Node: node}, nil
	}
	if node.Role != corecontract.CompositeRunRoleRootV1 &&
		node.Role != corecontract.CompositeRunRoleReviewerV1 {
		return compositeCompilerMaterialsV1{}, fmt.Errorf(
			"unsupported composite compiler role %q",
			node.Role,
		)
	}
	var plannedChildren []corecontract.CompositeChildRunRefV1
	familyDigest := ""
	switch node.Role {
	case corecontract.CompositeRunRoleRootV1:
		if node.Plan == nil || run.CompositeRoot != nil ||
			len(run.CompositeChildren) != len(node.Plan.Children) {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite root result set does not match its plan",
			)
		}
		plannedChildren = node.Plan.Children
		familyDigest = run.Manifest.ManifestDigest
	case corecontract.CompositeRunRoleReviewerV1:
		root := run.CompositeRoot
		if node.Plan != nil || node.Assignment != nil || root == nil ||
			root.Composite == nil ||
			root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
			root.Composite.Plan == nil || root.Composite.Plan.Reviewer == nil ||
			root.Composite.Plan.Reviewer.RunID != run.RunID ||
			node.ParentManifestDigest != root.ManifestDigest ||
			len(run.CompositeChildren) != len(root.Composite.Plan.Children) {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite Reviewer result set does not close its root",
			)
		}
		plannedChildren = root.Composite.Plan.Children
		familyDigest = root.ManifestDigest
	}
	results := make(
		[]contextcompiler.CompositeChildResultV1,
		len(run.CompositeChildren),
	)
	setResults := make(
		[]corecontract.CompositeSpecialistResultV1,
		len(run.CompositeChildren),
	)
	for index := range run.CompositeChildren {
		child := run.CompositeChildren[index]
		planned := plannedChildren[index]
		if child.SlotID != planned.SlotID ||
			child.RunID != planned.RunID ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.Assignment != planned.Assignment {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite Child %d differs from the frozen root plan",
				index,
			)
		}
		assignment := planned.Assignment
		if child.State != CompositeChildSucceededV1 ||
			child.ManifestDigest == "" ||
			child.ResultRef == "" || len(child.OutputCanonical) == 0 {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite Child %d is not an exact successful merge input",
				index,
			)
		}
		results[index] = contextcompiler.CompositeChildResultV1{
			SlotID:                child.SlotID,
			RunID:                 child.RunID,
			AdmissionKey:          planned.AdmissionKey,
			ChildManifestDigest:   child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			Assignment:            assignment,
			ResultRef:             child.ResultRef,
			TerminalRevision:      child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
			ResultCanonical:       bytes.Clone(child.OutputCanonical),
		}
		setResults[index] = corecontract.CompositeSpecialistResultV1{
			SlotID:                child.SlotID,
			FocusID:               assignment.FocusID,
			WeightBasisPoints:     assignment.WeightBasisPoints,
			RunID:                 child.RunID,
			ManifestDigest:        child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			ResultRef:             child.ResultRef,
			TerminalRunRevision:   child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
		}
	}
	materials := compositeCompilerMaterialsV1{
		Node:     node,
		Children: results,
	}
	reviewerEnabled := node.Role == corecontract.CompositeRunRoleReviewerV1 ||
		(node.Plan != nil && node.Plan.Reviewer != nil)
	if reviewerEnabled {
		set, _, digest, err := corecontract.NewCompositeSpecialistResultSetV1(
			corecontract.CompositeSpecialistResultSetV1{
				SchemaVersion: corecontract.CompositeSpecialistResultSetSchemaVersionV1,
				FamilyDigest:  familyDigest,
				TaskInputRef:  run.Manifest.TaskInputRef,
				Results:       setResults,
			},
		)
		if err != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"freeze Composite Specialist result set: %w",
				err,
			)
		}
		materials.SpecialistResultSet = &set
		materials.SpecialistResultDigest = digest
	}
	if node.Role == corecontract.CompositeRunRoleReviewerV1 {
		if run.CompositeReviewer != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"composite Reviewer exposes its own result projection",
			)
		}
		return materials, nil
	}
	if node.Plan.Reviewer == nil {
		if run.CompositeReviewer != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"Reviewer-disabled root exposes a Reviewer projection",
			)
		}
		return materials, nil
	}
	reviewer := run.CompositeReviewer
	if reviewer == nil || reviewer.State != CompositeReviewerSucceededV1 ||
		reviewer.Verdict == nil ||
		reviewer.Verdict.Decision != corecontract.ReviewDecisionApproveV1 ||
		reviewer.RunID != node.Plan.Reviewer.RunID ||
		reviewer.MemberSnapshotDigest !=
			node.Plan.Reviewer.MemberSnapshotDigest ||
		reviewer.AttemptID == "" || reviewer.ResultRef == "" ||
		len(reviewer.OutputCanonical) == 0 ||
		len(reviewer.VerdictCanonical) == 0 {
		return compositeCompilerMaterialsV1{}, fmt.Errorf(
			"composite root lacks an exact successful Reviewer APPROVE",
		)
	}
	materials.ReviewVerdict = &contextcompiler.CompositeReviewVerdictMaterialV1{
		ReviewerRunID:          reviewer.RunID,
		ReviewerManifestDigest: reviewer.ManifestDigest,
		MemberSnapshotDigest:   reviewer.MemberSnapshotDigest,
		AttemptID:              reviewer.AttemptID,
		LogicalStepID:          node.Plan.Reviewer.ReviewLogicalStepID,
		ResultRef:              reviewer.ResultRef,
		TerminalRunRevision:    reviewer.RunRevision,
		TerminalFrameRevision:  reviewer.FrameRevision,
		ResultCanonical:        bytes.Clone(reviewer.OutputCanonical),
		VerdictCanonical:       bytes.Clone(reviewer.VerdictCanonical),
	}
	return materials, nil
}

// collaborationCompilerMaterialsForRunV1 projects the exact Store-owned W5
// facts used by the execution compiler. Keeping this projection inside the
// Store semantic verifier prevents a valid request from being rejected by a
// legacy-only sibling-result interpretation during BeginModelDispatch.
func collaborationCompilerMaterialsForRunV1(
	run RunForLoop,
	node *corecontract.CompositeRunNodeV1,
	root corecontract.RunManifest,
) (compositeCompilerMaterialsV1, error) {
	if node == nil || root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return compositeCompilerMaterialsV1{}, fmt.Errorf(
			"collaboration compiler root plan is absent",
		)
	}
	plan := cloneCompositeRunPlanForSemanticCompilerV1(
		*root.Composite.Plan,
	)
	material := &contextcompiler.CompositeCollaborationMaterialV1{
		FamilyDigest:     root.ManifestDigest,
		ParticipantRunID: run.RunID,
		RootPlan:         &plan,
	}

	addRepairLineage := func() error {
		if run.CompositePreviousContributionSet == nil ||
			run.CompositeRepairVerdict == nil ||
			run.CompositeRepairVerdictResult == nil ||
			run.CompositeRepairVerdictRef == "" {
			return fmt.Errorf("collaboration repair lineage is absent")
		}
		previous := cloneCollaborationContributionSetForLoop(
			*run.CompositePreviousContributionSet,
		)
		request, err := collaborationVerdictMaterialForSemanticCompilerV1(
			*run.CompositeRepairVerdictResult,
			plan.Reviewer,
		)
		if err != nil || request.ResultRef != run.CompositeRepairVerdictRef {
			if err == nil {
				err = fmt.Errorf("repair verdict result reference differs")
			}
			return fmt.Errorf("collaboration repair verdict: %w", err)
		}
		material.PreviousContributionSet = &previous
		material.RepairRequestVerdict = request
		return nil
	}

	result := compositeCompilerMaterialsV1{
		Node:          node,
		Collaboration: material,
	}
	switch node.Role {
	case corecontract.CompositeRunRoleChildV1:
		if len(run.CompositeChildren) != 0 ||
			run.CompositeContributionSet != nil ||
			run.CompositeContributionSetDigest != "" ||
			run.CompositeReviewer != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"collaboration Specialist received sibling result material",
			)
		}
		if node.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			if err := addRepairLineage(); err != nil {
				return compositeCompilerMaterialsV1{}, err
			}
			if run.CompositeRepairBasis == nil ||
				len(run.CompositeRepairBasisCanonical) == 0 {
				return compositeCompilerMaterialsV1{}, fmt.Errorf(
					"collaboration repair basis is absent",
				)
			}
			material.RepairBasisCanonical = bytes.Clone(
				run.CompositeRepairBasisCanonical,
			)
		} else if node.RepairRound != 0 ||
			run.CompositePreviousContributionSet != nil ||
			run.CompositeRepairVerdict != nil ||
			run.CompositeRepairVerdictResult != nil ||
			len(run.CompositeRepairBasisCanonical) != 0 {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"initial collaboration Specialist carries repair lineage",
			)
		}
		return result, nil

	case corecontract.CompositeRunRoleReviewerV1,
		corecontract.CompositeRunRoleRootV1:
		if run.CompositeContributionSet == nil ||
			run.CompositeContributionSetDigest == "" {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"collaboration contribution set is absent",
			)
		}
		set := cloneCollaborationContributionSetForLoop(
			*run.CompositeContributionSet,
		)
		if set.RepairRound != run.CompositeRepairRound {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"collaboration contribution-set round differs from Store frontier",
			)
		}
		children, err := collaborationChildMaterialsForSemanticCompilerV1(
			run,
			root,
			set,
		)
		if err != nil {
			return compositeCompilerMaterialsV1{}, err
		}
		result.Children = children
		material.ContributionSet = &set
		material.ContributionSetDigest = run.CompositeContributionSetDigest
		if set.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			if err := addRepairLineage(); err != nil {
				return compositeCompilerMaterialsV1{}, err
			}
		} else if set.RepairRound != 0 ||
			run.CompositePreviousContributionSet != nil ||
			run.CompositeRepairVerdictResult != nil {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"round-zero collaboration set carries repair lineage",
			)
		}
		if node.Role == corecontract.CompositeRunRoleReviewerV1 {
			if run.CompositeReviewer != nil {
				return compositeCompilerMaterialsV1{}, fmt.Errorf(
					"collaboration Reviewer received sibling Reviewer output",
				)
			}
			return result, nil
		}

		reviewer := run.CompositeReviewer
		if reviewer == nil || reviewer.State != CompositeReviewerSucceededV1 ||
			reviewer.CollaborationVerdict == nil ||
			reviewer.CollaborationVerdict.Decision !=
				corecontract.CollaborationReviewDecisionApproveV1 {
			return compositeCompilerMaterialsV1{}, fmt.Errorf(
				"collaboration Root lacks an exact APPROVE result",
			)
		}
		plannedReviewer := plan.Reviewer
		if set.RepairRound == corecontract.CompositeRepairRoundOneV1 {
			plannedReviewer = &plan.Decision.RepairReviewer
		}
		reviewMaterial, err := collaborationVerdictMaterialForSemanticCompilerV1(
			*reviewer,
			plannedReviewer,
		)
		if err != nil {
			return compositeCompilerMaterialsV1{}, err
		}
		material.ReviewVerdict = reviewMaterial
		return result, nil
	default:
		return compositeCompilerMaterialsV1{}, fmt.Errorf(
			"unsupported collaboration role %q",
			node.Role,
		)
	}
}

func collaborationRootForSemanticCompilerV1(
	run RunForLoop,
) (corecontract.RunManifest, bool, error) {
	node := run.Manifest.Composite
	if node == nil {
		if run.CompositeRoot != nil || run.CompositeDecisionFrontier != nil ||
			run.CompositeContributionSet != nil ||
			run.CompositeRepairVerdictResult != nil {
			return corecontract.RunManifest{}, false, fmt.Errorf(
				"non-composite Run exposes collaboration material",
			)
		}
		return corecontract.RunManifest{}, false, nil
	}
	var root corecontract.RunManifest
	switch node.Role {
	case corecontract.CompositeRunRoleRootV1:
		root = run.Manifest
	case corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1:
		if run.CompositeRoot == nil {
			if node.RepairRound == corecontract.CompositeRepairRoundOneV1 {
				return corecontract.RunManifest{}, false, fmt.Errorf(
					"repair participant lacks its frozen Root",
				)
			}
			return corecontract.RunManifest{}, false, nil
		}
		root = *run.CompositeRoot
	default:
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"unsupported composite role %q",
			node.Role,
		)
	}
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil {
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"collaboration Root plan is absent",
		)
	}
	if root.Composite.Plan.Decision == nil {
		return corecontract.RunManifest{}, false, nil
	}
	if root.ManifestDigest == "" ||
		(node.Role != corecontract.CompositeRunRoleRootV1 &&
			(node.RootRunID != root.RunID ||
				node.ParentManifestDigest != root.ManifestDigest)) {
		return corecontract.RunManifest{}, false, fmt.Errorf(
			"collaboration participant differs from its frozen Root",
		)
	}
	return root, true, nil
}

func collaborationChildMaterialsForSemanticCompilerV1(
	run RunForLoop,
	root corecontract.RunManifest,
	set corecontract.CollaborationContributionSetV1,
) ([]contextcompiler.CompositeChildResultV1, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil ||
		len(run.CompositeChildren) != len(set.Contributions) ||
		len(set.Contributions) != len(root.Composite.Plan.Children) {
		return nil, fmt.Errorf(
			"collaboration Child material cardinality does not close",
		)
	}
	results := make(
		[]contextcompiler.CompositeChildResultV1,
		len(run.CompositeChildren),
	)
	for index, child := range run.CompositeChildren {
		entry := set.Contributions[index]
		planned := root.Composite.Plan.Children[index]
		if entry.RunID != planned.RunID {
			planned = root.Composite.Plan.Decision.RepairChildren[index]
		}
		if child.State != CompositeChildSucceededV1 ||
			child.SlotID != entry.SlotID || child.RunID != entry.RunID ||
			child.RunID != planned.RunID ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.Assignment != planned.Assignment ||
			child.ResultRef != entry.ResultRef || child.Contribution == nil ||
			child.ContributionDigest != entry.ContributionDigest ||
			len(child.OutputCanonical) == 0 {
			return nil, fmt.Errorf(
				"collaboration Child %d differs from its exact contribution edge",
				index,
			)
		}
		results[index] = contextcompiler.CompositeChildResultV1{
			SlotID:                child.SlotID,
			RunID:                 child.RunID,
			AdmissionKey:          planned.AdmissionKey,
			ChildManifestDigest:   child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			Assignment:            child.Assignment,
			ResultRef:             child.ResultRef,
			TerminalRevision:      child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
			ResultCanonical:       bytes.Clone(child.OutputCanonical),
		}
	}
	return results, nil
}

func collaborationVerdictMaterialForSemanticCompilerV1(
	record CompositeReviewerResultRecordV1,
	planned *corecontract.CompositeReviewerRunRefV1,
) (*contextcompiler.CompositeCollaborationReviewVerdictMaterialV1, error) {
	if planned == nil || record.State != CompositeReviewerSucceededV1 ||
		record.RunID != planned.RunID ||
		record.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
		record.CollaborationVerdict == nil || record.AttemptID == "" ||
		record.ResultRef == "" || len(record.OutputCanonical) == 0 ||
		len(record.VerdictCanonical) == 0 ||
		!moduleapi.ValidSHA256(record.ManifestDigest) {
		return nil, fmt.Errorf(
			"collaboration Reviewer result is incomplete",
		)
	}
	return &contextcompiler.CompositeCollaborationReviewVerdictMaterialV1{
		ReviewerRunID:          record.RunID,
		ReviewerManifestDigest: record.ManifestDigest,
		MemberSnapshotDigest:   record.MemberSnapshotDigest,
		AttemptID:              record.AttemptID,
		LogicalStepID:          planned.ReviewLogicalStepID,
		ResultRef:              record.ResultRef,
		TerminalRunRevision:    record.RunRevision,
		TerminalFrameRevision:  record.FrameRevision,
		ResultCanonical:        bytes.Clone(record.OutputCanonical),
		VerdictCanonical:       bytes.Clone(record.VerdictCanonical),
	}, nil
}

func cloneCompositeRunNodeForSemanticCompilerV1(
	input corecontract.CompositeRunNodeV1,
) corecontract.CompositeRunNodeV1 {
	if input.Assignment != nil {
		assignment := *input.Assignment
		input.Assignment = &assignment
	}
	if input.Plan != nil {
		plan := cloneCompositeRunPlanForSemanticCompilerV1(*input.Plan)
		input.Plan = &plan
	}
	return input
}

func cloneCompositeRunPlanForSemanticCompilerV1(
	input corecontract.CompositeRunPlanV1,
) corecontract.CompositeRunPlanV1 {
	input.Children = append(
		[]corecontract.CompositeChildRunRefV1(nil),
		input.Children...,
	)
	if input.Reviewer != nil {
		reviewer := *input.Reviewer
		input.Reviewer = &reviewer
	}
	if input.Decision != nil {
		decision := *input.Decision
		decision.RepairChildren = append(
			[]corecontract.CompositeChildRunRefV1(nil),
			input.Decision.RepairChildren...,
		)
		input.Decision = &decision
	}
	return input
}

func knowledgeCompilerBindingsForRun(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	taskText string,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
) (*moduleapi.PortPlan, []contextcompiler.BindingMaterialV1, error) {
	var selected *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		plan := run.Member.PortPlans[index]
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		if selected != nil {
			return nil, nil,
				fmt.Errorf("context.provide/v1 has more than one PortPlan")
		}
		frozen, err := moduleapi.NewPortPlan(plan)
		if err != nil {
			return nil, nil, fmt.Errorf("restore context PortPlan: %w", err)
		}
		selected = &frozen
	}
	if selected == nil {
		if len(knowledgeBindings) != 0 || len(memoryBindings) != 0 ||
			len(compilation.KnowledgeRetrievals) != 0 ||
			len(compilation.KnowledgeReuses) != 0 ||
			len(compilation.KnowledgeShortcuts) != 0 ||
			len(compilation.MemoryReads) != 0 {
			return nil, nil, fmt.Errorf(
				"dynamic Knowledge evidence has no context.provide/v1 PortPlan",
			)
		}
		return nil, nil, nil
	}

	evidenceByBinding := make(
		map[uint32]corecontract.KnowledgeRetrievalEvidenceV1,
		len(compilation.KnowledgeRetrievals),
	)
	for _, evidence := range compilation.KnowledgeRetrievals {
		if _, duplicate := evidenceByBinding[evidence.BindingIndex]; duplicate {
			return nil, nil, fmt.Errorf(
				"duplicate Knowledge evidence for Binding %d",
				evidence.BindingIndex,
			)
		}
		evidenceByBinding[evidence.BindingIndex] = evidence
	}
	reuseByBinding := make(
		map[uint32]corecontract.KnowledgeReuseEvidenceV1,
		len(compilation.KnowledgeReuses),
	)
	for _, evidence := range compilation.KnowledgeReuses {
		bindingIndex := evidence.FreshRetrieval.BindingIndex
		if _, duplicate := reuseByBinding[bindingIndex]; duplicate {
			return nil, nil, fmt.Errorf(
				"duplicate Knowledge reuse for Binding %d",
				bindingIndex,
			)
		}
		reuseByBinding[bindingIndex] = evidence
	}
	shortcutByBinding := make(
		map[uint32]corecontract.KnowledgeShortcutEvidenceV1,
		len(compilation.KnowledgeShortcuts),
	)
	for _, evidence := range compilation.KnowledgeShortcuts {
		if _, duplicate := shortcutByBinding[evidence.BindingIndex]; duplicate {
			return nil, nil, fmt.Errorf(
				"duplicate Knowledge shortcut for Binding %d",
				evidence.BindingIndex,
			)
		}
		shortcutByBinding[evidence.BindingIndex] = evidence
	}
	frozenByBinding := make(
		map[uint32]frozenKnowledgeBinding,
		len(knowledgeBindings),
	)
	for _, binding := range knowledgeBindings {
		frozenByBinding[binding.BindingIndex] = binding
	}
	memoryEvidenceByBinding := make(
		map[uint32]corecontract.MemoryReadEvidenceV1,
		len(compilation.MemoryReads),
	)
	for _, evidence := range compilation.MemoryReads {
		if _, duplicate := memoryEvidenceByBinding[evidence.BindingIndex]; duplicate {
			return nil, nil, fmt.Errorf(
				"duplicate Memory evidence for Binding %d",
				evidence.BindingIndex,
			)
		}
		memoryEvidenceByBinding[evidence.BindingIndex] = evidence
	}
	frozenMemoryByBinding := make(
		map[uint32]frozenMemoryBinding,
		len(memoryBindings),
	)
	for _, binding := range memoryBindings {
		frozenMemoryByBinding[binding.BindingIndex] = binding
	}
	materials := make(
		[]contextcompiler.BindingMaterialV1,
		len(selected.Bindings),
	)
	for bindingIndex, binding := range selected.Bindings {
		config, found := run.FindContent(binding.ConfigRef)
		if !found || config.Kind != ContentConfig ||
			config.MediaType != admissionJSONMediaType {
			return nil, nil, fmt.Errorf(
				"context Binding %d Config is unavailable",
				bindingIndex,
			)
		}
		material := contextcompiler.BindingMaterialV1{
			ConfigCanonical: bytes.Clone(config.CanonicalBytes),
			StaticContextCanonicals: make(
				[][]byte,
				len(binding.StaticContextRefs),
			),
		}
		for refIndex, ref := range binding.StaticContextRefs {
			content, found := run.FindContent(ref)
			if !found || content.Kind != ContentStaticContext ||
				content.MediaType != admissionJSONMediaType {
				return nil, nil, fmt.Errorf(
					"context Binding %d static ref %d is unavailable",
					bindingIndex,
					refIndex,
				)
			}
			material.StaticContextCanonicals[refIndex] =
				bytes.Clone(content.CanonicalBytes)
		}
		if binding.Provider.ExecutionClass ==
			moduleapi.ExecutionTrustedInProcess {
			authority, found := run.FindContent(
				binding.AuthorityCeilingRef,
			)
			if !found || authority.Kind != ContentAuthorityCeiling ||
				authority.MediaType != admissionJSONMediaType {
				return nil, nil, fmt.Errorf(
					"context Binding %d authority is unavailable",
					bindingIndex,
				)
			}
			contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
				config.CanonicalBytes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"restore context Binding %d Config: %w",
					bindingIndex,
					err,
				)
			}
			protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(
				contextConfig,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"restore context Binding %d protocol: %w",
					bindingIndex,
					err,
				)
			}
			switch protocol {
			case moduleapi.KnowledgeContextBindingSchemaV1:
				index := uint32(bindingIndex)
				evidence, hasRetrieval := evidenceByBinding[index]
				reuse, hasReuse := reuseByBinding[index]
				_, hasShortcut := shortcutByBinding[index]
				outcomeCount := 0
				if hasRetrieval {
					outcomeCount++
				}
				if hasReuse {
					outcomeCount++
				}
				if hasShortcut {
					outcomeCount++
				}
				if outcomeCount != 1 {
					return nil, nil, fmt.Errorf(
						"context Binding %d must have exactly one Knowledge retrieval, reuse, or shortcut",
						bindingIndex,
					)
				}
				frozenBinding, found := frozenByBinding[index]
				if !found {
					return nil, nil, fmt.Errorf(
						"context Binding %d has no frozen Knowledge closure",
						bindingIndex,
					)
				}
				if hasRetrieval {
					_, requestCanonical, _, err :=
						moduleapi.NewKnowledgeContextRequestV1(
							moduleapi.KnowledgeContextRequestV1{
								SchemaVersion: moduleapi.
									KnowledgeContextRequestSchemaV1,
								Source:            frozenBinding.Config.Source,
								Scope:             frozenBinding.Scope,
								QueryText:         taskText,
								MaxHits:           frozenBinding.MaxHits,
								MaxTotalTextBytes: frozenBinding.MaxTextBytes,
							},
						)
					if err != nil {
						return nil, nil, fmt.Errorf(
							"rebuild context Binding %d request: %w",
							bindingIndex,
							err,
						)
					}
					_, outputCanonical, _, err :=
						moduleapi.NewKnowledgeContextOutputV1(
							moduleapi.KnowledgeContextOutputV1{
								SchemaVersion: moduleapi.
									KnowledgeContextOutputSchemaV1,
								RequestDigest: evidence.RequestDigest,
								Source:        evidence.Source,
								Hits:          evidence.Hits,
							},
						)
					if err != nil {
						return nil, nil, fmt.Errorf(
							"rebuild context Binding %d output: %w",
							bindingIndex,
							err,
						)
					}
					material.DynamicRequestCanonical = bytes.Clone(
						requestCanonical,
					)
					material.DynamicOutputCanonical = bytes.Clone(
						outputCanonical,
					)
					if evidence.Provenance != nil {
						provenance := *evidence.Provenance
						material.KnowledgeProvenance = &provenance
					}
				} else if hasReuse {
					reuseCopy := cloneKnowledgeReuseEvidenceForCompiler(reuse)
					material.KnowledgeReuse = &reuseCopy
				}
			case moduleapi.MemoryContextBindingSchemaV1:
				evidence, found := memoryEvidenceByBinding[uint32(bindingIndex)]
				if !found {
					return nil, nil, fmt.Errorf(
						"context Binding %d has no Memory evidence",
						bindingIndex,
					)
				}
				frozenBinding, found := frozenMemoryByBinding[uint32(bindingIndex)]
				if !found {
					return nil, nil, fmt.Errorf(
						"context Binding %d has no frozen Memory closure",
						bindingIndex,
					)
				}
				snapshotContent, found := run.FindContent(evidence.Snapshot.Digest)
				if !found || snapshotContent.Kind != ContentMemorySnapshot ||
					snapshotContent.MediaType != admissionJSONMediaType {
					return nil, nil, fmt.Errorf(
						"context Binding %d Memory snapshot is unavailable",
						bindingIndex,
					)
				}
				snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(
					snapshotContent.CanonicalBytes,
				)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"restore context Binding %d Memory snapshot: %w",
						bindingIndex,
						err,
					)
				}
				candidates, resolved, err := memorycore.FilterCandidates(
					snapshot,
					evidence.Snapshot,
					frozenBinding.Scope,
					frozenBinding.Config,
					frozenBinding.Authority,
					evidence.EvaluatedAtUnixMS,
				)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"rebuild context Binding %d Memory candidates: %w",
						bindingIndex,
						err,
					)
				}
				_, requestCanonical, requestDigest, err :=
					moduleapi.NewMemoryContextRequestV1(
						moduleapi.MemoryContextRequestV1{
							SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
							Snapshot:          evidence.Snapshot,
							Scope:             frozenBinding.Scope,
							QueryText:         taskText,
							EvaluatedAtUnixMS: evidence.EvaluatedAtUnixMS,
							Candidates:        candidates,
							MaxItems:          resolved.MaxItems,
							MaxTotalTextBytes: resolved.MaxTotalTextBytes,
						},
					)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"rebuild context Binding %d Memory request: %w",
						bindingIndex,
						err,
					)
				}
				if requestDigest != evidence.RequestDigest {
					return nil, nil, fmt.Errorf(
						"context Binding %d Memory request digest mismatch",
						bindingIndex,
					)
				}
				selectedDigests := make([]string, len(evidence.SelectedEntries))
				for index, selected := range evidence.SelectedEntries {
					selectedDigests[index] = selected.EntryDigest
				}
				_, outputCanonical, outputDigest, err :=
					moduleapi.NewMemoryContextOutputV1(
						moduleapi.MemoryContextOutputV1{
							SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
							RequestDigest:        requestDigest,
							Snapshot:             evidence.Snapshot,
							SelectedEntryDigests: selectedDigests,
						},
					)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"rebuild context Binding %d Memory output: %w",
						bindingIndex,
						err,
					)
				}
				if outputDigest != evidence.OutputDigest {
					return nil, nil, fmt.Errorf(
						"context Binding %d Memory output digest mismatch",
						bindingIndex,
					)
				}
				material.DynamicStateCanonical = bytes.Clone(
					snapshotContent.CanonicalBytes,
				)
				material.DynamicRequestCanonical = bytes.Clone(requestCanonical)
				material.DynamicOutputCanonical = bytes.Clone(outputCanonical)
			default:
				return nil, nil, fmt.Errorf(
					"context Binding %d has unsupported protocol %q",
					bindingIndex,
					protocol,
				)
			}
			material.AuthorityCanonical = bytes.Clone(authority.CanonicalBytes)
		}
		materials[bindingIndex] = material
	}
	return selected, materials, nil
}

func cloneKnowledgeReuseEvidenceForCompiler(
	input corecontract.KnowledgeReuseEvidenceV1,
) corecontract.KnowledgeReuseEvidenceV1 {
	cloned := input
	cloned.FreshRetrieval.Hits = append(
		[]moduleapi.KnowledgeHitV1(nil),
		input.FreshRetrieval.Hits...,
	)
	for index := range cloned.FreshRetrieval.Hits {
		cloned.FreshRetrieval.Hits[index].VisibleTo = append(
			[]moduleapi.KnowledgeScopeRuleV1(nil),
			input.FreshRetrieval.Hits[index].VisibleTo...,
		)
	}
	if input.FreshRetrieval.Provenance != nil {
		provenance := *input.FreshRetrieval.Provenance
		cloned.FreshRetrieval.Provenance = &provenance
	}
	return cloned
}

func knowledgeCompilerHistoryForRun(
	run RunForLoop,
	attemptFrameRevision uint64,
) (
	[]contextcompiler.HistoryTurnV1,
	[]contextcompiler.ConversationHistoryTurnV1,
	error,
) {
	if run.Manifest.ConversationTurn != nil {
		if len(run.History) != 0 {
			return nil, nil, fmt.Errorf(
				"current Conversation Run cannot reuse its own History as predecessor turns",
			)
		}
		turn := run.Manifest.ConversationTurn
		if turn.TurnIndex == 0 {
			return nil, nil, fmt.Errorf(
				"frozen Conversation turn index is invalid",
			)
		}
		wantTurns := turn.TurnIndex - 1
		if uint64(len(run.ConversationHistory)) != wantTurns {
			return nil, nil, fmt.Errorf(
				"Conversation History contains %d turns, want %d",
				len(run.ConversationHistory),
				wantTurns,
			)
		}
		history := make(
			[]contextcompiler.ConversationHistoryTurnV1,
			len(run.ConversationHistory),
		)
		for index, entry := range run.ConversationHistory {
			expectedIndex := uint64(index + 1)
			if entry.TurnIndex != expectedIndex || entry.SourceRunID == "" ||
				entry.SourceAttemptID == "" {
				return nil, nil, fmt.Errorf(
					"frozen Conversation History turn %d has invalid identity",
					index,
				)
			}
			if entry.UserContent.Kind != ContentTaskInput {
				return nil, nil, fmt.Errorf(
					"frozen Conversation History turn %d USER kind is %q",
					index,
					entry.UserContent.Kind,
				)
			}
			user, err := corecontract.RestoreTaskInputV1(
				entry.UserContent.CanonicalBytes,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"restore frozen Conversation USER turn %d: %w",
					index,
					err,
				)
			}
			if entry.AssistantContent.Kind != ContentModelResult {
				return nil, nil, fmt.Errorf(
					"frozen Conversation History turn %d ASSISTANT kind is %q",
					index,
					entry.AssistantContent.Kind,
				)
			}
			assistant, err := moduleapi.RestoreModelGenerateOutputV1(
				entry.AssistantContent.CanonicalBytes,
			)
			if err != nil || assistant.ActionRequest != nil ||
				assistant.AssistantText == "" {
				return nil, nil, fmt.Errorf(
					"restore frozen Conversation ASSISTANT turn %d: %v",
					index,
					err,
				)
			}
			history[index] = contextcompiler.ConversationHistoryTurnV1{
				TurnIndex:                    entry.TurnIndex,
				UserSourceContentDigest:      entry.UserContent.Digest,
				AssistantSourceContentDigest: entry.AssistantContent.Digest,
				UserMessage: moduleapi.ModelMessageV1{
					Role:    moduleapi.ModelRoleUser,
					Content: user.Text,
				},
				AssistantMessage: moduleapi.ModelMessageV1{
					Role:    moduleapi.ModelRoleAssistant,
					Content: assistant.AssistantText,
				},
			}
		}
		return nil, history, nil
	}
	if len(run.ConversationHistory) != 0 {
		return nil, nil, fmt.Errorf(
			"non-Conversation Run contains Conversation History evidence",
		)
	}
	attemptFrames := make(map[string]uint64, len(run.ModelDispatches))
	for _, dispatch := range run.ModelDispatches {
		attemptFrames[dispatch.Attempt.AttemptID] =
			dispatch.Attempt.FrameRevision
	}
	history := make([]contextcompiler.HistoryTurnV1, 0, len(run.History))
	for _, entry := range run.History {
		frameRevision, found := attemptFrames[entry.SourceAttemptID]
		if !found {
			return nil, nil, fmt.Errorf("frozen History source Attempt is unavailable")
		}
		if frameRevision >= attemptFrameRevision {
			continue
		}
		if entry.Sequence != uint64(len(history)+1) ||
			entry.Role != string(moduleapi.ModelRoleAssistant) ||
			entry.Content.Kind != ContentModelResult {
			return nil, nil, fmt.Errorf("frozen History is not a contiguous ASSISTANT sequence")
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.Content.CanonicalBytes,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("restore frozen History: %w", err)
		}
		history = append(history, contextcompiler.HistoryTurnV1{
			Sequence:            entry.Sequence,
			SourceContentDigest: entry.Content.Digest,
			Message: moduleapi.ModelMessageV1{
				Role:    moduleapi.ModelRoleAssistant,
				Content: output.AssistantText,
			},
		})
	}
	return history, nil, nil
}

func containsKnowledgeMessagesInOrder(
	messages []moduleapi.ModelMessageV1,
	expected []moduleapi.ModelMessageV1,
) bool {
	next := 0
	for _, message := range messages {
		if next < len(expected) && message == expected[next] {
			next++
		}
	}
	return next == len(expected)
}

func runKnowledgeContentGetter(run RunForLoop) knowledgeContentGetter {
	return func(digest string) (ContentRecord, error) {
		record, found := run.FindContent(digest)
		if !found {
			return ContentRecord{}, fmt.Errorf("content %s is unavailable", digest)
		}
		return record, nil
	}
}
