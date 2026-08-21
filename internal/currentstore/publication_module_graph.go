package currentstore

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// boundProfileModuleNodeV1 is reconstructed only for a Binding that is
// actually present in one Profile. Catalog-only activations remain inert:
// their declarations cannot request a dependency or permission grant.
type boundProfileModuleNodeV1 struct {
	bindingIndex uint32
	binding      controlcontract.BindingSpec
	entry        controlcontract.CatalogEntry
	manifest     moduleapi.ModuleManifestV1
	// manifestCanonical is the exact installed Manifest selected through the
	// Binding -> Catalog -> Activation -> Installation chain. Permission
	// requests are module-instance declarations, so bindings may be grouped
	// only when both InstanceID and these immutable bytes are identical.
	manifestCanonical []byte
	dependencies      []int
}

// verifyBoundProfileModuleGraphV1 closes the governed module graph entirely
// from immutable publication facts. It writes no derived dependency or grant
// state.
func verifyBoundProfileModuleGraphV1(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	graphs := make([][]boundProfileModuleNodeV1, len(control.Profiles))
	for profileIndex, profile := range control.Profiles {
		nodes := make([]boundProfileModuleNodeV1, len(profile.Bindings))
		for bindingIndex, binding := range profile.Bindings {
			entry, manifest, manifestCanonical, err := resolveBoundManifestV1(
				ctx,
				queryer,
				control.TenantID,
				catalog,
				binding,
			)
			if err != nil {
				return fmt.Errorf(
					"%w: profile %s Binding %d module closure: %v",
					ErrPublicationConflict,
					profile.Profile.ID,
					bindingIndex,
					err,
				)
			}
			node := boundProfileModuleNodeV1{
				bindingIndex:      uint32(bindingIndex),
				binding:           binding,
				entry:             entry,
				manifest:          manifest,
				manifestCanonical: manifestCanonical,
			}
			nodes[bindingIndex] = node
		}
		graphs[profileIndex] = nodes
	}

	for profileIndex, profile := range control.Profiles {
		nodes := graphs[profileIndex]
		if err := verifyProfileDocumentInsightManifestShapesV1(
			profile.Profile.ID,
			nodes,
		); err != nil {
			return err
		}
		if err := verifyProfileModulePermissionRequestsV1(
			ctx,
			queryer,
			control,
			profile.Profile.ID,
			nodes,
		); err != nil {
			return err
		}
		for nodeIndex := range nodes {
			requires := nodes[nodeIndex].manifest.Requires
			nodes[nodeIndex].dependencies = make([]int, len(requires))
			for requireIndex, requiredPort := range requires {
				matches := exactPortBindingIndexesV1(
					profile.Bindings,
					requiredPort,
				)
				switch len(matches) {
				case 0:
					if exactPortExistsOutsideProfileV1(
						control,
						profileIndex,
						requiredPort,
					) {
						return fmt.Errorf(
							"%w: profile %s Binding %d has cross-Profile Require %s",
							ErrPublicationConflict,
							profile.Profile.ID,
							nodes[nodeIndex].bindingIndex,
							exactPortTextV1(requiredPort),
						)
					}
					return fmt.Errorf(
						"%w: profile %s Binding %d has missing exact Require %s",
						ErrPublicationConflict,
						profile.Profile.ID,
						nodes[nodeIndex].bindingIndex,
						exactPortTextV1(requiredPort),
					)
				case 1:
					nodes[nodeIndex].dependencies[requireIndex] = matches[0]
				default:
					return fmt.Errorf(
						"%w: profile %s Binding %d has ambiguous exact Require %s (%d Bindings)",
						ErrPublicationConflict,
						profile.Profile.ID,
						nodes[nodeIndex].bindingIndex,
						exactPortTextV1(requiredPort),
						len(matches),
					)
				}
			}
		}
		graphs[profileIndex] = nodes
		if err := verifyProfileModuleGraphAcyclicV1(
			profile.Profile.ID,
			nodes,
		); err != nil {
			return err
		}
		if err := verifyProfileKnowledgeManifestShapesV1(
			ctx,
			queryer,
			profile.Profile.ID,
			nodes,
		); err != nil {
			return err
		}
	}
	return nil
}

// verifyProfileDocumentInsightManifestShapesV1 recognizes the reserved product
// by its Core-assigned provider identity, not by a caller-controlled Provides
// list or by whichever subset of Ports a Profile happens to bind. It therefore
// runs before permission and dependency evaluation, including for Action-only
// assemblies and coordinated Manifest/Catalog downgrade attempts.
func verifyProfileDocumentInsightManifestShapesV1(
	profileID string,
	nodes []boundProfileModuleNodeV1,
) error {
	classifiedInstances := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.entry.Activation.ModuleID != moduleapi.DocumentInsightModuleIDV1 {
			continue
		}
		if _, classified := classifiedInstances[node.binding.InstanceID]; classified {
			continue
		}
		if err := moduleapi.ClassifyExactDocumentInsightProviderV1(
			node.entry.Activation,
		); err != nil {
			return fmt.Errorf(
				"%w: profile %s instance %s Document Insight provider identity: %v",
				ErrPublicationConflict,
				profileID,
				node.binding.InstanceID,
				err,
			)
		}
		if err := moduleapi.ClassifyExactDocumentInsightManifestV1(
			node.manifest,
		); err != nil {
			return fmt.Errorf(
				"%w: profile %s instance %s Document Insight Manifest shape: %v",
				ErrPublicationConflict,
				profileID,
				node.binding.InstanceID,
				err,
			)
		}
		classifiedInstances[node.binding.InstanceID] = struct{}{}
	}
	return nil
}

// verifyProfileKnowledgeManifestShapesV1 binds the shared moduleapi shape
// classifier to actual Knowledge consumers. Other context providers, such as
// Memory, keep their own exact protocol and are not reclassified as Knowledge.
func verifyProfileKnowledgeManifestShapesV1(
	ctx context.Context,
	queryer publicationQueryer,
	profileID string,
	nodes []boundProfileModuleNodeV1,
) error {
	getContent := func(digest string) (ContentRecord, error) {
		return queryContent(ctx, queryer, digest)
	}
	for _, node := range nodes {
		if node.binding.Port != moduleapi.ExactContextProvidePortV1() ||
			node.entry.Activation.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
			continue
		}
		binding := moduleapi.PortBinding{
			Provider:            node.entry.Activation,
			ConfigRef:           node.binding.ConfigRef,
			AuthorityCeilingRef: node.binding.AuthorityCeilingRef,
			StaticContextRefs: append(
				[]string(nil),
				node.binding.StaticContextRefs...,
			),
			FailurePolicy: node.binding.FailurePolicy,
		}
		protocol, err := contextBindingProtocolV1(binding, getContent)
		if err != nil {
			return fmt.Errorf(
				"%w: profile %s Binding %d context protocol: %v",
				ErrPublicationConflict,
				profileID,
				node.bindingIndex,
				err,
			)
		}
		if protocol != moduleapi.KnowledgeContextBindingSchemaV1 {
			continue
		}
		var shapeErr error
		switch {
		case slices.Equal(
			node.manifest.Provides,
			moduleapi.ExactKnowledgeManifestProvidesV1(),
		):
			_, shapeErr = moduleapi.ClassifyExactKnowledgeManifestV1(
				node.manifest,
			)
		case node.entry.Activation.ModuleID ==
			moduleapi.DocumentInsightModuleIDV1:
			// Already classified once per exact bound instance above.
		default:
			shapeErr = fmt.Errorf(
				"Knowledge context provider must use the exact single-Port Knowledge or dual-Port Document Insight Manifest family",
			)
		}
		if shapeErr != nil {
			return fmt.Errorf(
				"%w: profile %s Binding %d Knowledge Manifest shape: %v",
				ErrPublicationConflict,
				profileID,
				node.bindingIndex,
				shapeErr,
			)
		}
	}
	return nil
}

// resolveBoundManifestV1 follows the complete governed identity chain for one
// bound node: Binding -> Catalog -> Activation -> Installation -> canonical
// Manifest. No caller-supplied provider fact bypasses this path.
func resolveBoundManifestV1(
	ctx context.Context,
	queryer publicationQueryer,
	tenantID string,
	catalog controlcontract.CatalogGeneration,
	binding controlcontract.BindingSpec,
) (
	controlcontract.CatalogEntry,
	moduleapi.ModuleManifestV1,
	[]byte,
	error,
) {
	entry, found := catalog.FindInstance(binding.InstanceID)
	if !found || !containsExactPort(entry.Provides, binding.Port) {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf(
				"Binding does not resolve exact Catalog port %s on instance %s",
				exactPortTextV1(binding.Port),
				binding.InstanceID,
			)
	}
	activation, err := queryModuleActivationByIdentity(
		ctx,
		queryer,
		tenantID,
		entry.Activation.InstanceID,
		entry.Activation.ActivationRevision,
	)
	if err != nil {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("resolve Activation: %w", err)
	}
	if activation.TenantID != tenantID ||
		activation.InstanceID != entry.Activation.InstanceID ||
		activation.ActivationRevision != entry.Activation.ActivationRevision ||
		activation.ExecutionClass != entry.Activation.ExecutionClass ||
		activation.AdapterIdentity != entry.Activation.AdapterIdentity {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("Catalog and stored Activation differ")
	}
	installation, err := queryModuleInstallationByID(
		ctx,
		queryer,
		activation.InstallationID,
	)
	if err != nil {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("resolve Installation: %w", err)
	}
	if installation.ModuleID != entry.Activation.ModuleID ||
		installation.ExactVersion != entry.Activation.Version ||
		installation.ArtifactDigest != entry.Activation.ArtifactDigest {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("Catalog Activation and Installation identity differ")
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("restore canonical Manifest: %w", err)
	}
	if manifest.ID != installation.ModuleID ||
		manifest.Version != installation.ExactVersion ||
		!sameExactPortSet(manifest.Provides, entry.Provides) ||
		!containsExactPort(manifest.Provides, binding.Port) {
		return controlcontract.CatalogEntry{}, moduleapi.ModuleManifestV1{}, nil,
			fmt.Errorf("Manifest identity or Provides differ from bound Catalog entry")
	}
	return entry, manifest, manifestCanonical, nil
}

// verifyWorkspaceChannelEndpointModuleDeclarationsV1 closes every Workspace
// ChannelEndpoint Binding through the same governed module identity chain.
// E5-A defines dependency and grant semantics only for Profile Bindings, so a
// Channel Manifest with either declaration is rejected rather than ignored.
func verifyWorkspaceChannelEndpointModuleDeclarationsV1(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	for workspaceIndex, workspace := range control.Workspaces {
		for endpointIndex, endpoint := range workspace.ChannelEndpoints {
			_, manifest, _, err := resolveBoundManifestV1(
				ctx,
				queryer,
				control.TenantID,
				catalog,
				endpoint.Binding,
			)
			if err != nil {
				return fmt.Errorf(
					"%w: Workspace %s ChannelEndpoint %s Binding module closure: %v",
					ErrPublicationConflict,
					workspace.Workspace.ID,
					endpoint.EndpointID,
					err,
				)
			}
			if len(manifest.Requires) != 0 ||
				len(manifest.RequestedPermissions) != 0 {
				return fmt.Errorf(
					"%w: ChannelEndpoint Manifest for Workspace %d %s Endpoint %d %s declares %d Requires and %d requested_permissions; E5-A Channel Bindings support neither",
					ErrPublicationConflict,
					workspaceIndex,
					workspace.Workspace.ID,
					endpointIndex,
					endpoint.EndpointID,
					len(manifest.Requires),
					len(manifest.RequestedPermissions),
				)
			}
		}
	}
	return nil
}

// verifyProfileModulePermissionRequestsV1 evaluates a Manifest's permission
// requests once per exact module instance in one Profile. A multi-Port module
// therefore cannot gain authority merely because the same declaration is
// repeated through multiple Bindings, and a Binding in another Profile cannot
// contribute a grant.
func verifyProfileModulePermissionRequestsV1(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	profileID string,
	nodes []boundProfileModuleNodeV1,
) error {
	type instanceGroup struct {
		instanceID        string
		manifestCanonical []byte
		nodes             []boundProfileModuleNodeV1
	}
	groupIndexByInstance := make(map[string]int, len(nodes))
	groups := make([]instanceGroup, 0, len(nodes))
	for _, node := range nodes {
		groupIndex, found := groupIndexByInstance[node.binding.InstanceID]
		if !found {
			groupIndexByInstance[node.binding.InstanceID] = len(groups)
			groups = append(groups, instanceGroup{
				instanceID:        node.binding.InstanceID,
				manifestCanonical: bytes.Clone(node.manifestCanonical),
				nodes:             []boundProfileModuleNodeV1{node},
			})
			continue
		}
		group := &groups[groupIndex]
		if !bytes.Equal(group.manifestCanonical, node.manifestCanonical) {
			return fmt.Errorf(
				"%w: profile %s instance %s resolves more than one exact Manifest",
				ErrPublicationConflict,
				profileID,
				node.binding.InstanceID,
			)
		}
		group.nodes = append(group.nodes, node)
	}

	for _, group := range groups {
		manifest := group.nodes[0].manifest
		for _, permission := range manifest.RequestedPermissions {
			switch permission {
			case moduleapi.PermissionKnowledgeReadV1:
				if err := verifyKnowledgeReadGrantV1(
					ctx,
					queryer,
					control,
					group.nodes,
				); err != nil {
					return fmt.Errorf(
						"%w: profile %s instance %s permission closure: %v",
						ErrPublicationConflict,
						profileID,
						group.instanceID,
						err,
					)
				}
			default:
				return fmt.Errorf(
					"%w: profile %s instance %s permission closure: unsupported requested permission %q",
					ErrPublicationConflict,
					profileID,
					group.instanceID,
					permission,
				)
			}
		}
	}
	return nil
}

func verifyKnowledgeReadGrantV1(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	nodes []boundProfileModuleNodeV1,
) error {
	knowledgeNodes := make([]boundProfileModuleNodeV1, 0, 1)
	getContent := func(digest string) (ContentRecord, error) {
		return queryContent(ctx, queryer, digest)
	}
	for _, node := range nodes {
		if node.binding.Port != moduleapi.ExactContextProvidePortV1() {
			continue
		}
		providerBinding := moduleapi.PortBinding{
			Provider:            node.entry.Activation,
			ConfigRef:           node.binding.ConfigRef,
			AuthorityCeilingRef: node.binding.AuthorityCeilingRef,
			StaticContextRefs: append(
				[]string(nil),
				node.binding.StaticContextRefs...,
			),
			FailurePolicy: node.binding.FailurePolicy,
		}
		protocol, err := contextBindingProtocolV1(providerBinding, getContent)
		if err != nil {
			return fmt.Errorf(
				"knowledge.read context.provide/v1 Binding %d protocol: %w",
				node.bindingIndex,
				err,
			)
		}
		if protocol == moduleapi.KnowledgeContextBindingSchemaV1 {
			knowledgeNodes = append(knowledgeNodes, node)
		}
	}
	if len(knowledgeNodes) != 1 {
		return fmt.Errorf(
			"knowledge.read requires a Knowledge context.provide/v1 Binding; exactly one same-instance Binding must exist in the same Profile, found %d",
			len(knowledgeNodes),
		)
	}
	node := knowledgeNodes[0]
	providerBinding := moduleapi.PortBinding{
		Provider:            node.entry.Activation,
		ConfigRef:           node.binding.ConfigRef,
		AuthorityCeilingRef: node.binding.AuthorityCeilingRef,
		StaticContextRefs: append(
			[]string(nil),
			node.binding.StaticContextRefs...,
		),
		FailurePolicy: node.binding.FailurePolicy,
	}
	config, authority, dynamic, err := validateContextBindingDefinitionV1(
		node.binding.Port,
		providerBinding,
		getContent,
	)
	if err != nil || !dynamic {
		return fmt.Errorf(
			"knowledge.read requires a Knowledge context.provide/v1 Binding: %v",
			err,
		)
	}
	protocol, err := contextBindingProtocolV1(providerBinding, getContent)
	if err != nil || protocol != moduleapi.KnowledgeContextBindingSchemaV1 {
		return fmt.Errorf(
			"knowledge.read requires a Knowledge context.provide/v1 Binding",
		)
	}
	if config.Source != authority.Source {
		return fmt.Errorf("knowledge.read Config and authority lack one exact source")
	}
	if err := config.Source.Validate(); err != nil {
		return fmt.Errorf("knowledge.read source is not exact: %w", err)
	}
	for scopeIndex, rule := range authority.AllowedScopes {
		if rule.TenantID != control.TenantID {
			return fmt.Errorf(
				"knowledge.read scope %d Tenant scope is outside Control",
				scopeIndex,
			)
		}
		if rule.WorkspaceID == "*" {
			if len(control.Workspaces) == 0 {
				return fmt.Errorf(
					"knowledge.read scope %d Workspace scope is outside Control",
					scopeIndex,
				)
			}
		} else if _, found := control.FindWorkspace(rule.WorkspaceID); !found {
			return fmt.Errorf(
				"knowledge.read scope %d Workspace scope is outside Control",
				scopeIndex,
			)
		}
		if rule.AgentID == "*" {
			if len(control.Agents) == 0 {
				return fmt.Errorf(
					"knowledge.read scope %d Agent scope is outside Control",
					scopeIndex,
				)
			}
		} else if _, found := control.FindAgent(rule.AgentID); !found {
			return fmt.Errorf(
				"knowledge.read scope %d Agent scope is outside Control",
				scopeIndex,
			)
		}
	}
	return nil
}

func exactPortBindingIndexesV1(
	bindings []controlcontract.BindingSpec,
	port moduleapi.PortRef,
) []int {
	matches := make([]int, 0, 1)
	for index, binding := range bindings {
		if binding.Port == port {
			matches = append(matches, index)
		}
	}
	return matches
}

func exactPortExistsOutsideProfileV1(
	control controlcontract.ControlSnapshot,
	profileIndex int,
	port moduleapi.PortRef,
) bool {
	for otherProfileIndex, profile := range control.Profiles {
		if otherProfileIndex == profileIndex {
			continue
		}
		if len(exactPortBindingIndexesV1(profile.Bindings, port)) != 0 {
			return true
		}
	}
	return false
}

func verifyProfileModuleGraphAcyclicV1(
	profileID string,
	nodes []boundProfileModuleNodeV1,
) error {
	states := make([]uint8, len(nodes))
	stack := make([]int, 0, len(nodes))
	stackPositions := make([]int, len(nodes))
	for index := range stackPositions {
		stackPositions[index] = -1
	}
	var visit func(int) error
	visit = func(nodeIndex int) error {
		states[nodeIndex] = 1
		stackPositions[nodeIndex] = len(stack)
		stack = append(stack, nodeIndex)
		for _, dependency := range nodes[nodeIndex].dependencies {
			switch states[dependency] {
			case 0:
				if err := visit(dependency); err != nil {
					return err
				}
			case 1:
				start := stackPositions[dependency]
				cycle := append([]int(nil), stack[start:]...)
				cycle = append(cycle, dependency)
				labels := make([]string, len(cycle))
				for index, cycleNode := range cycle {
					labels[index] = fmt.Sprintf(
						"%s[%s]",
						nodes[cycleNode].binding.InstanceID,
						exactPortTextV1(nodes[cycleNode].binding.Port),
					)
				}
				return fmt.Errorf(
					"%w: profile %s dependency cycle: %s",
					ErrPublicationConflict,
					profileID,
					strings.Join(labels, " -> "),
				)
			}
		}
		stack = stack[:len(stack)-1]
		stackPositions[nodeIndex] = -1
		states[nodeIndex] = 2
		return nil
	}
	for nodeIndex := range nodes {
		if states[nodeIndex] == 0 {
			if err := visit(nodeIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

func exactPortTextV1(port moduleapi.PortRef) string {
	return port.Name + "/" + port.ExactVersion
}
