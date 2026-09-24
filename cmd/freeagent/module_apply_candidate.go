package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var moduleApplyDenyAllAuthorityCanonicalV1 = modulehandler.DenyAllAuthorityCanonicalV1()

type moduleApplyProtocolHandlerKindV1 = modulehandler.KindV1

const (
	localDocumentInsightModuleID = moduleapi.DocumentInsightModuleIDV1
	localDocumentInsightVersion  = moduleapi.DocumentInsightVersionV2
	localDocumentInsightDigest   = moduleapi.DocumentInsightArtifactDigestV2

	moduleApplyHandlerDeclarativeContextV1 = modulehandler.HandlerDeclarativeContextV1
	moduleApplyHandlerKnowledgeContextV1   = modulehandler.HandlerKnowledgeContextV1
	moduleApplyHandlerMemoryContextV1      = modulehandler.HandlerMemoryContextV1
	moduleApplyHandlerMCPActionV1          = modulehandler.HandlerMCPActionV1
	moduleApplyHandlerRemoteActionHTTPV1   = modulehandler.HandlerRemoteActionHTTPV1
	moduleApplyHandlerWASMActionV1         = modulehandler.HandlerWASMActionV1
	moduleApplyHandlerTextStatsActionV1    = modulehandler.HandlerTextStatsActionV1
	moduleApplyHandlerDocumentInsightV1    = modulehandler.HandlerDocumentInsightV1
	moduleApplyHandlerLoopbackChannelV1    = modulehandler.HandlerLoopbackChannelV1
	moduleApplyHandlerDeepSeekModelV1      = modulehandler.HandlerDeepSeekModelV1
)

type moduleApplyProtocolHandlerKeyV1 = modulehandler.KeyV1

// moduleApplyProtocolHandlerV1 is a temporary command compatibility view of
// the shared immutable PolicyV1. It carries no private registry or rule.
type moduleApplyProtocolHandlerV1 modulehandler.PolicyV1

// moduleApplyLocalPolicyV1 is the resolved, read-only view consumed by the
// existing apply pipeline. Its sole source is one protocol-handler table row.
type moduleApplyLocalPolicyV1 = moduleApplyProtocolHandlerV1

var errModuleApplyProtocolHandlerNotFoundV1 = modulehandler.ErrProtocolHandlerNotFoundV1

// moduleApplyProtocolHandlerTableV1 is the Core-owned generic/Model mapping
// from an exact protocol request to local execution and interpretation policy.
// Reserved product identities are checked first through the separate exact
// selector table below. Both values are rebuilt on every call, so neither a
// package Manifest nor mutable process registration can alter policy.
func moduleApplyProtocolHandlerTableV1() [9]moduleApplyProtocolHandlerV1 {
	shared := modulehandler.GenericTableV1()
	var result [9]moduleApplyProtocolHandlerV1
	for index := range shared {
		result[index] = moduleApplyProtocolHandlerV1(shared[index])
	}
	return result
}

// moduleApplyExactSelectorProtocolHandlerTableV1 is separate from the generic
// protocol table so existing modules keep their exact resolution behavior.
// The Document Insight package ID is reserved: once selected, an incorrect
// version, digest, Port, runtime, or consumer schema cannot fall through to a
// generic Knowledge or text.stats handler.
func moduleApplyExactSelectorProtocolHandlerTableV1() [2]moduleApplyProtocolHandlerV1 {
	shared := modulehandler.ExactSelectorTableV1()
	var result [2]moduleApplyProtocolHandlerV1
	for index := range shared {
		result[index] = moduleApplyProtocolHandlerV1(shared[index])
	}
	return result
}

// Existing command tests retain their local helper spelling, but every key
// and validation decision is delegated to the immutable shared registry.
func (policy moduleApplyProtocolHandlerV1) protocolHandlerKeyV1() moduleApplyProtocolHandlerKeyV1 {
	return modulehandler.PolicyV1(policy).ProtocolHandlerKeyV1()
}

func resolveModuleApplyProtocolHandlerFromTableV1(
	table []moduleApplyLocalPolicyV1,
	key moduleApplyProtocolHandlerKeyV1,
) (moduleApplyLocalPolicyV1, error) {
	shared := make([]modulehandler.PolicyV1, len(table))
	for index := range table {
		shared[index] = modulehandler.PolicyV1(table[index])
	}
	resolved, err := modulehandler.ResolveFromTableV1(shared, key)
	return moduleApplyLocalPolicyV1(resolved), err
}

func validateModuleApplyProtocolHandlerTableV1(
	table []moduleApplyLocalPolicyV1,
) error {
	shared := make([]modulehandler.PolicyV1, len(table))
	for index := range table {
		shared[index] = modulehandler.PolicyV1(table[index])
	}
	return modulehandler.ValidateTableV1(shared)
}

func resolveModuleApplyProtocolHandlerV1(
	key moduleApplyProtocolHandlerKeyV1,
) (moduleApplyLocalPolicyV1, error) {
	resolved, err := modulehandler.ResolveGenericV1(key)
	return moduleApplyLocalPolicyV1(resolved), err
}

func resolveModuleApplyExactSelectorProtocolHandlerV1(
	key moduleApplyProtocolHandlerKeyV1,
) (moduleApplyLocalPolicyV1, error) {
	resolved, err := modulehandler.ResolveExactSelectorV1(key)
	return moduleApplyLocalPolicyV1(resolved), err
}

type moduleApplyCandidateV1 struct {
	Policy                 moduleApplyLocalPolicyV1
	ManifestCanonical      []byte
	Resolver               *activationresolver.Resolver
	ProviderTemplate       moduleapi.ActivatedModuleRef
	StaticContextCanonical []byte
	StaticContextRef       string
}

type moduleApplyCandidateModeV1 string

const (
	moduleApplyCandidateForApplyV1  moduleApplyCandidateModeV1 = "APPLY"
	moduleApplyCandidateForDryRunV1 moduleApplyCandidateModeV1 = "DRY_RUN"
)

// moduleApplyExactAdapterAvailabilityProbeV1 is one inert, immutable proof
// used only while projecting a Dry-run activation. It has no registration,
// lookup, fallback, enumeration, or Provider construction surface.
type moduleApplyExactAdapterAvailabilityProbeV1 struct {
	ArtifactDigest  string
	AdapterIdentity string
}

func (probe moduleApplyExactAdapterAvailabilityProbeV1) IsRegistered(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (bool, error) {
	if ctx == nil {
		return false, errors.New("module apply exact adapter probe context is nil")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return artifactDigest == probe.ArtifactDigest &&
		adapterIdentity == probe.AdapterIdentity, nil
}

func moduleApplyCandidateStaticRefsV1(
	candidate moduleApplyCandidateV1,
) []string {
	if candidate.StaticContextRef == "" {
		return []string{}
	}
	return []string{candidate.StaticContextRef}
}

// verifyModuleApplyCandidateArtifactV1 dispatches final-target verification
// solely by the already-resolved Core handler identity. In particular, a
// trusted Action can never fall through to the MCP verifier.
func verifyModuleApplyCandidateArtifactV1(
	ctx context.Context,
	plan moduleApplyPlanV1,
	candidate moduleApplyCandidateV1,
	artifactDirectory string,
) error {
	if plan.Module == nil || plan.Binding == nil {
		return errors.New("enabled module or binding is absent")
	}
	switch candidate.Policy.HandlerKind {
	case moduleApplyHandlerMCPActionV1:
		_, err := loadMCPInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		return err

	case moduleApplyHandlerRemoteActionHTTPV1:
		descriptor, err := remoteactionhttp.LoadDescriptorFromArtifact(
			ctx,
			candidate.ProviderTemplate,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return err
		}
		manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
			ctx,
			artifactDirectory,
		)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("REMOTE Action artifact differs from candidate"),
			)
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return err
		}
		return validateModuleApplyRemoteDescriptorMappingsV1(config, descriptor)

	case moduleApplyHandlerWASMActionV1:
		manifest, descriptor, err := validateModuleApplyWASMActionArtifactV1(
			ctx,
			plan,
			candidate,
			artifactDirectory,
		)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("WASM Action artifact differs from candidate"),
			)
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return err
		}
		return validateModuleApplyWASMDescriptorMappingsV1(config, descriptor)

	case moduleApplyHandlerTextStatsActionV1:
		verified, err := validateTextStatsArtifactFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil || !bytes.Equal(
			verified.ManifestCanonical,
			candidate.ManifestCanonical,
		) {
			return errors.Join(
				err,
				errors.New("text.stats artifact differs from candidate"),
			)
		}
		return nil

	case moduleApplyHandlerDocumentInsightV1:
		verified, err := validateDocumentInsightArtifactFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil || !bytes.Equal(
			verified.ManifestCanonical,
			candidate.ManifestCanonical,
		) {
			return errors.Join(
				err,
				errors.New("Document Insight artifact differs from candidate"),
			)
		}
		if candidate.Policy.Port == productionContextPort {
			return validateModuleApplyKnowledgeSourceV1(plan, verified.SourceRef)
		}
		return nil

	case moduleApplyHandlerDeclarativeContextV1:
		manifest, staticContext, err := readModuleApplyDeclarativeMetadataV1(
			ctx,
			artifactDirectory,
		)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) ||
			!bytes.Equal(staticContext, candidate.StaticContextCanonical) {
			return errors.Join(
				err,
				errors.New("declarative artifact differs from candidate"),
			)
		}
		return nil

	case moduleApplyHandlerKnowledgeContextV1:
		manifest, sourceRef, err := readModuleApplyKnowledgeMetadataV1(
			ctx,
			artifactDirectory,
		)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("Knowledge artifact differs from candidate"),
			)
		}
		if err := validateModuleApplyKnowledgeSourceV1(plan, sourceRef); err != nil {
			return err
		}
		_, err = loadKnowledgeInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		return err

	case moduleApplyHandlerMemoryContextV1:
		manifest, err := readModuleApplyMemoryMetadataV1(ctx, artifactDirectory)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("Memory artifact differs from candidate"),
			)
		}
		_, err = loadMemoryInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		return err

	case moduleApplyHandlerLoopbackChannelV1:
		manifest, err := readModuleApplyLoopbackChannelMetadataV1(ctx, artifactDirectory)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("loopback Channel artifact differs from candidate"),
			)
		}
		return nil

	case moduleApplyHandlerDeepSeekModelV1:
		if err := validateDeepSeekArtifact(
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			candidate.ProviderTemplate,
		); err != nil {
			return err
		}
		manifest, _, err := readArtifact(artifactDirectory)
		if err != nil || !bytes.Equal(manifest, candidate.ManifestCanonical) {
			return errors.Join(
				err,
				errors.New("DeepSeek Model artifact differs from candidate"),
			)
		}
		return nil

	default:
		return fmt.Errorf(
			"Core protocol handler kind %q has no candidate artifact verifier",
			candidate.Policy.HandlerKind,
		)
	}
}

// validateModuleApplyWASMActionArtifactV1 performs the complete offline
// artifact check shared by staged-candidate and final-directory verification.
// It parses and compiles the exact ABI, but never creates an Adapter,
// instantiates guest memory, or executes guest instructions.
func validateModuleApplyWASMActionArtifactV1(
	ctx context.Context,
	plan moduleApplyPlanV1,
	candidate moduleApplyCandidateV1,
	artifactDirectory string,
) ([]byte, wasmaction.DescriptorV1, error) {
	if plan.Module == nil {
		return nil, wasmaction.DescriptorV1{}, errors.New(
			"WASM Action module plan is absent",
		)
	}
	descriptor, wasm, err := wasmaction.LoadArtifactFromDirectory(
		ctx,
		candidate.ProviderTemplate,
		artifactDirectory,
		plan.Module.ArtifactSizeBytes,
	)
	if err != nil {
		return nil, wasmaction.DescriptorV1{}, err
	}
	if err := wasmaction.ValidateModuleV1(ctx, wasm); err != nil {
		return nil, wasmaction.DescriptorV1{}, err
	}
	manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, wasmaction.DescriptorV1{}, err
	}
	return bytes.Clone(manifest), descriptor, nil
}

func validateModuleApplyWASMDescriptorMappingsV1(
	config moduleapi.ActionBindingConfigV1,
	descriptor wasmaction.DescriptorV1,
) error {
	actions := make(
		map[string]moduleapi.ActionDefinitionV1,
		len(descriptor.Actions),
	)
	for _, action := range descriptor.Actions {
		if action.RequestedEffectClass != moduleapi.EffectNone {
			return errors.New("WASM Action descriptor effects must all be none")
		}
		actions[action.ProviderActionID] = action
	}
	for _, mapping := range config.Actions {
		action, found := actions[mapping.ProviderActionID]
		if !found {
			return fmt.Errorf(
				"Action config references absent WASM descriptor action %q",
				mapping.ProviderActionID,
			)
		}
		if mapping.LocalEffectClass != moduleapi.EffectNone ||
			mapping.MaxResultBytes > action.RequestedMaxResultBytes {
			return fmt.Errorf(
				"Action config exceeds WASM descriptor mapping %q",
				mapping.ProviderActionID,
			)
		}
	}
	return nil
}

func validateModuleApplyPortV1(port moduleapi.PortRef) error {
	if err := port.Validate(); err != nil {
		return err
	}
	switch port {
	case productionModelPort, productionActionPort, productionContextPort, productionChannelPort:
		return nil
	default:
		return errors.New(
			"only model.generate/v2, action.provider/v1, context.provide/v1, and channel.transport/v1 are supported",
		)
	}
}

func resolveEnabledModuleApplyPolicyV1(
	plan moduleApplyPlanV1,
) (moduleApplyLocalPolicyV1, error) {
	if plan.Module == nil || plan.Binding == nil {
		return moduleApplyLocalPolicyV1{}, errors.New(
			"enabled module apply module and binding are required",
		)
	}
	return resolveModuleApplyBindingPolicyV1(
		plan.Port,
		plan.TenantID,
		*plan.Binding,
		plan.Module.ExpectedRuntimeRequest,
		plan.Module,
	)
}

func resolveModuleApplyBindingPolicyV1(
	port moduleapi.PortRef,
	tenantID string,
	binding moduleApplyBindingV1,
	expectedRuntime moduleApplyExpectedRuntimeRequestV1,
	module *moduleApplyModuleV1,
) (moduleApplyLocalPolicyV1, error) {
	sharedModule := sharedModuleApplyIdentityV1(module)
	policy, err := modulehandler.ResolveBindingV1(modulehandler.BindingV1{
		TenantID: tenantID, Port: port,
		ConfigCanonical: binding.Config, AuthorityCanonical: binding.AuthorityCeiling,
		FailurePolicy:  binding.FailurePolicy,
		RuntimeRequest: modulehandler.RuntimeRequestV1{Mode: expectedRuntime.Mode, Protocol: expectedRuntime.Protocol},
		Module:         sharedModule,
	})
	return moduleApplyLocalPolicyV1(policy), err
}

func sharedModuleApplyIdentityV1(module *moduleApplyModuleV1) *modulehandler.ModuleIdentityV1 {
	if module == nil {
		return nil
	}
	return &modulehandler.ModuleIdentityV1{
		ID: module.ID, ExactVersion: module.ExactVersion,
		ArtifactDigest: module.ArtifactDigest,
	}
}

func validateModuleApplyEmptyActionParametersV1(
	binding moduleApplyBindingV1,
) error {
	return modulehandler.ValidateEmptyActionParametersV1(binding.Config)
}

func validateModuleApplyWASMActionBindingV1(
	tenantID string,
	binding moduleApplyBindingV1,
) error {
	return modulehandler.ValidateWASMActionBindingV1(
		tenantID, binding.Config, binding.AuthorityCeiling, binding.FailurePolicy,
	)
}

func validateModuleApplyRemoteActionBindingV1(
	tenantID string,
	binding moduleApplyBindingV1,
) (moduleapi.RemoteActionHTTPBindingParametersV1, error) {
	return modulehandler.ValidateRemoteActionBindingV1(
		tenantID, binding.Config, binding.AuthorityCeiling, binding.FailurePolicy,
	)
}

func moduleApplyBindingConsumerSchemaV1(
	port moduleapi.PortRef,
	binding moduleApplyBindingV1,
) (string, error) {
	return modulehandler.ConsumerSchemaV1(port, binding.Config)
}

func validateModuleApplyTextStatsBindingV1(binding moduleApplyBindingV1) error {
	return modulehandler.ValidateTextStatsBindingV1(binding.Config, binding.AuthorityCeiling)
}

func validateModuleApplyBindingPolicyV1(
	port moduleapi.PortRef,
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
	failurePolicy moduleapi.FailurePolicy,
) error {
	return modulehandler.ValidateBindingPolicyV1(
		port, tenantID, configCanonical, authorityCanonical, failurePolicy,
	)
}

func validateModuleApplyVerificationReportV1(
	plan moduleApplyPlanV1,
	policy moduleApplyLocalPolicyV1,
	moduleID string,
	exactVersion string,
	artifactDigest string,
	artifactSize uint64,
	runtimeMode string,
	runtimeProtocol string,
	runtimeEntrypoint string,
	provides []moduleapi.PortRef,
	requires []moduleapi.PortRef,
	permissions []moduleapi.Permission,
) (knowledgeManifestShapeV1, error) {
	if plan.Module == nil ||
		moduleID != plan.Module.ID || exactVersion != plan.Module.ExactVersion ||
		artifactDigest != plan.Module.ArtifactDigest ||
		artifactSize != plan.Module.ArtifactSizeBytes ||
		runtimeMode != string(plan.Module.ExpectedRuntimeRequest.Mode) ||
		runtimeProtocol != plan.Module.ExpectedRuntimeRequest.Protocol ||
		runtimeMode != string(policy.RuntimeMode) ||
		runtimeProtocol != policy.RuntimeProtocol {
		return "", errors.New("artifact report does not match the enabled plan")
	}
	if policy.HandlerKind == moduleApplyHandlerKnowledgeContextV1 {
		shape, err := classifyExactKnowledgeManifestDeclarationV1(
			knowledgeManifestDeclarationV1{
				RuntimeMode:          moduleapi.RuntimeModeRequest(runtimeMode),
				RuntimeProtocol:      runtimeProtocol,
				Entrypoint:           runtimeEntrypoint,
				Provides:             provides,
				Requires:             requires,
				RequestedPermissions: permissions,
			},
		)
		if err != nil {
			return "", errors.Join(
				errors.New("artifact report does not match the enabled Knowledge plan"),
				err,
			)
		}
		return shape, nil
	}
	if policy.HandlerKind == moduleApplyHandlerDocumentInsightV1 {
		if err := classifyExactDocumentInsightManifestDeclarationV1(
			knowledgeManifestDeclarationV1{
				RuntimeMode:          moduleapi.RuntimeModeRequest(runtimeMode),
				RuntimeProtocol:      runtimeProtocol,
				Entrypoint:           runtimeEntrypoint,
				Provides:             provides,
				Requires:             requires,
				RequestedPermissions: permissions,
			},
		); err != nil {
			return "", errors.Join(
				errors.New("artifact report does not match the exact Document Insight plan"),
				err,
			)
		}
		return "", nil
	}
	if len(provides) != 1 || provides[0] != policy.Port ||
		len(requires) != 0 || len(permissions) != 0 {
		return "", errors.New("artifact report does not match the enabled plan")
	}
	return "", nil
}

func buildModuleApplyCandidateV1(
	ctx context.Context,
	plan moduleApplyPlanV1,
	stagedArtifact string,
	mode moduleApplyCandidateModeV1,
) (moduleApplyCandidateV1, error) {
	if plan.Module == nil || plan.Binding == nil {
		return moduleApplyCandidateV1{}, errors.New("enabled plan payload is incomplete")
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return moduleApplyCandidateV1{}, err
	}
	if mode != moduleApplyCandidateForApplyV1 && mode != moduleApplyCandidateForDryRunV1 {
		return moduleApplyCandidateV1{}, errors.New("module apply candidate mode is invalid")
	}
	candidate := moduleApplyCandidateV1{Policy: policy}
	candidate.ProviderTemplate = moduleapi.ActivatedModuleRef{
		ModuleID:           plan.Module.ID,
		Version:            plan.Module.ExactVersion,
		ArtifactDigest:     plan.Module.ArtifactDigest,
		InstanceID:         plan.InstanceID,
		ExecutionClass:     policy.ExecutionClass,
		AdapterIdentity:    policy.AdapterIdentity,
		ActivationRevision: 1,
	}

	var registry activationresolver.ExactAdapterRegistryProbe
	resolverConfig := activationresolver.Config{
		DeclarativeAdapterIdentity: declarativeAdapterID,
	}
	switch policy.HandlerKind {
	case moduleApplyHandlerDeepSeekModelV1:
		if err := normalizeStagedReadOnlyArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if err := validateDeepSeekArtifact(
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			candidate.ProviderTemplate,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		manifest, _, err := readArtifact(stagedArtifact)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = moduleApplyExactAdapterAvailabilityProbeV1{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
		}
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerRemoteActionHTTPV1:
		if err := normalizeStagedReadOnlyArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		descriptor, err := remoteactionhttp.LoadDescriptorFromArtifact(
			ctx,
			candidate.ProviderTemplate,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if err := validateModuleApplyRemoteDescriptorMappingsV1(
			config,
			descriptor,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		manifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
			ctx,
			stagedArtifact,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = moduleApplyExactAdapterAvailabilityProbeV1{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
		}
		resolverConfig.RemoteActionGrants = []activationresolver.RemoteActionGrant{{
			ModuleID:        plan.Module.ID,
			ExactVersion:    plan.Module.ExactVersion,
			ArtifactDigest:  plan.Module.ArtifactDigest,
			Protocol:        policy.RuntimeProtocol,
			AdapterIdentity: policy.AdapterIdentity,
		}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerWASMActionV1:
		if err := normalizeStagedReadOnlyArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		manifest, descriptor, err := validateModuleApplyWASMActionArtifactV1(
			ctx,
			plan,
			candidate,
			stagedArtifact,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if err := validateModuleApplyWASMDescriptorMappingsV1(
			config,
			descriptor,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = moduleApplyExactAdapterAvailabilityProbeV1{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
		}
		resolverConfig.WASMActionGrants = []activationresolver.WASMActionGrant{{
			ModuleID:        plan.Module.ID,
			ExactVersion:    plan.Module.ExactVersion,
			ArtifactDigest:  plan.Module.ArtifactDigest,
			Protocol:        policy.RuntimeProtocol,
			AdapterIdentity: policy.AdapterIdentity,
		}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerMCPActionV1:
		manifest, descriptor, err := normalizeStagedMCPArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if err := validateModuleApplyDescriptorMappingsV1(config, descriptor); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		invoker, err := loadMCPInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		exactRegistry, err := exactadapter.NewRegistry(exactadapter.Registration{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
			Invoker:         invoker,
		})
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = exactRegistry
		resolverConfig.LocalProcessGrants = []activationresolver.LocalProcessGrant{{
			ModuleID:        plan.Module.ID,
			ExactVersion:    plan.Module.ExactVersion,
			ArtifactDigest:  plan.Module.ArtifactDigest,
			Protocol:        policy.RuntimeProtocol,
			AdapterIdentity: policy.AdapterIdentity,
		}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerTextStatsActionV1:
		if err := normalizeStagedReadOnlyArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		verified, err := validateTextStatsArtifactFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if mode == moduleApplyCandidateForApplyV1 {
			invoker, err := loadTextStatsInvokerFromArtifact(
				ctx,
				candidate.ProviderTemplate.ArtifactDigest,
				candidate.ProviderTemplate.AdapterIdentity,
				stagedArtifact,
				plan.Module.ArtifactSizeBytes,
				&candidate.ProviderTemplate,
			)
			if err != nil {
				return moduleApplyCandidateV1{}, err
			}
			exactRegistry, err := exactadapter.NewRegistry(exactadapter.Registration{
				ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
				AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
				Invoker:         invoker,
			})
			if err != nil {
				return moduleApplyCandidateV1{}, err
			}
			registry = exactRegistry
		} else {
			registry = moduleApplyExactAdapterAvailabilityProbeV1{
				ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
				AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
			}
		}
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(verified.ManifestCanonical)

	case moduleApplyHandlerDocumentInsightV1:
		if err := normalizeStagedReadOnlyArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		); err != nil {
			return moduleApplyCandidateV1{}, err
		}
		verified, err := validateDocumentInsightArtifactFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		if policy.Port == productionContextPort {
			if err := validateModuleApplyKnowledgeSourceV1(
				plan,
				verified.SourceRef,
			); err != nil {
				return moduleApplyCandidateV1{}, err
			}
		}
		if mode == moduleApplyCandidateForApplyV1 {
			invoker, err := loadDocumentInsightInvokerFromArtifact(
				ctx,
				candidate.ProviderTemplate.ArtifactDigest,
				candidate.ProviderTemplate.AdapterIdentity,
				stagedArtifact,
				plan.Module.ArtifactSizeBytes,
				&candidate.ProviderTemplate,
			)
			if err != nil {
				return moduleApplyCandidateV1{}, err
			}
			exactRegistry, err := exactadapter.NewRegistry(exactadapter.Registration{
				ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
				AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
				Invoker:         invoker,
			})
			if err != nil {
				return moduleApplyCandidateV1{}, err
			}
			registry = exactRegistry
		} else {
			registry = moduleApplyExactAdapterAvailabilityProbeV1{
				ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
				AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
			}
		}
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(verified.ManifestCanonical)

	case moduleApplyHandlerDeclarativeContextV1:
		manifest, staticContext, err := normalizeStagedDeclarativeArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		staticRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentStaticContext,
			moduleApplyJSONMediaType,
			staticContext,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		candidate.ManifestCanonical = bytes.Clone(manifest)
		candidate.StaticContextCanonical = bytes.Clone(staticContext)
		candidate.StaticContextRef = staticRef

	case moduleApplyHandlerKnowledgeContextV1:
		manifest, err := normalizeStagedKnowledgeArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
			plan,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		invoker, err := loadKnowledgeInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		exactRegistry, err := exactadapter.NewRegistry(exactadapter.Registration{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
			Invoker:         invoker,
		})
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = exactRegistry
		// The Operator-owned exact plan grants only this immutable data artifact
		// to the compiled lexical adapter. The manifest cannot select an adapter,
		// and no package code is loaded or invoked by this assignment.
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerMemoryContextV1:
		manifest, err := normalizeStagedMemoryArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		invoker, err := loadMemoryInvokerFromArtifact(
			ctx,
			candidate.ProviderTemplate.ArtifactDigest,
			candidate.ProviderTemplate.AdapterIdentity,
			stagedArtifact,
			plan.Module.ArtifactSizeBytes,
			&candidate.ProviderTemplate,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		exactRegistry, err := exactadapter.NewRegistry(exactadapter.Registration{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
			Invoker:         invoker,
		})
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = exactRegistry
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	case moduleApplyHandlerLoopbackChannelV1:
		manifest, err := normalizeStagedLoopbackChannelArtifactModesV1(
			ctx,
			stagedArtifact,
			plan.Module.ArtifactDigest,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return moduleApplyCandidateV1{}, err
		}
		registry = moduleApplyExactAdapterAvailabilityProbeV1{
			ArtifactDigest:  candidate.ProviderTemplate.ArtifactDigest,
			AdapterIdentity: candidate.ProviderTemplate.AdapterIdentity,
		}
		resolverConfig.TrustedInProcessAllowlist =
			[]activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        plan.Module.ID,
				ExactVersion:    plan.Module.ExactVersion,
				ArtifactDigest:  plan.Module.ArtifactDigest,
				AdapterIdentity: policy.AdapterIdentity,
			}}
		candidate.ManifestCanonical = bytes.Clone(manifest)

	default:
		return moduleApplyCandidateV1{}, errors.New(
			"enabled module apply policy has no local artifact interpreter",
		)
	}

	resolver, err := activationresolver.New(resolverConfig, registry)
	if err != nil {
		return moduleApplyCandidateV1{}, err
	}
	candidate.Resolver = resolver
	return candidate, nil
}

func normalizeStagedLoopbackChannelArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
) ([]byte, error) {
	manifest, err := readModuleApplyLoopbackChannelMetadataV1(ctx, artifactDirectory)
	if err != nil {
		return nil, err
	}
	if err := normalizeStagedReadOnlyArtifactModesV1(
		ctx,
		artifactDirectory,
		expectedDigest,
		expectedSize,
	); err != nil {
		return nil, err
	}
	return bytes.Clone(manifest), nil
}

func readModuleApplyLoopbackChannelMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) ([]byte, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.ID != localLoopbackChannelModuleID ||
		manifest.Version != localLoopbackChannelVersion ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		manifest.Runtime.Entrypoint != localLoopbackChannelEntrypoint ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != productionChannelPort ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return nil, errors.Join(
			err,
			errors.New("artifact is not the exact compiled loopback channel.transport/v1 module"),
		)
	}
	return bytes.Clone(manifestCanonical), nil
}

func normalizeStagedDeclarativeArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
) ([]byte, []byte, error) {
	manifestCanonical, staticContextCanonical, err :=
		readModuleApplyDeclarativeMetadataV1(ctx, artifactDirectory)
	if err != nil {
		return nil, nil, err
	}
	if err := normalizeStagedReadOnlyArtifactModesV1(
		ctx,
		artifactDirectory,
		expectedDigest,
		expectedSize,
	); err != nil {
		return nil, nil, err
	}
	return bytes.Clone(manifestCanonical), bytes.Clone(staticContextCanonical), nil
}

func normalizeStagedReadOnlyArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
) error {
	files, err := moduleapi.ScanArtifactDirectoryContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return err
	}
	for _, file := range files {
		path := filepath.Join(artifactDirectory, filepath.FromSlash(file.Path))
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
		if err := syncModuleApplyArtifactFileV1(ctx, path); err != nil {
			return err
		}
	}
	manifestPath := filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath)
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		return err
	}
	if err := syncModuleApplyArtifactFileV1(ctx, manifestPath); err != nil {
		return err
	}
	if err := syncInitTreeDirectoriesContext(ctx, artifactDirectory); err != nil {
		return err
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		expectedDigest,
		expectedSize,
	); err != nil {
		return err
	}
	return nil
}

func readModuleApplyDeclarativeMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) ([]byte, []byte, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, nil, err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.Runtime.Mode != moduleapi.RuntimeModeRequestDeclarative ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != productionContextPort ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return nil, nil, errors.Join(
			err,
			errors.New("artifact is not exact DECLARATIVE static context.provide/v1"),
		)
	}
	staticContextCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		artifactDirectory,
		manifest.Runtime.Entrypoint,
		2*moduleapi.MaxTextBytes,
	)
	if err != nil {
		return nil, nil, err
	}
	if _, err := corecontract.RestoreStaticContextV1(staticContextCanonical); err != nil {
		return nil, nil, err
	}
	return bytes.Clone(manifestCanonical), bytes.Clone(staticContextCanonical), nil
}

func normalizeStagedKnowledgeArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
	plan moduleApplyPlanV1,
) ([]byte, error) {
	manifestCanonical, sourceRef, err :=
		readModuleApplyKnowledgeMetadataV1(ctx, artifactDirectory)
	if err != nil {
		return nil, err
	}
	if err := validateModuleApplyKnowledgeSourceV1(plan, sourceRef); err != nil {
		return nil, err
	}
	if err := normalizeStagedReadOnlyArtifactModesV1(
		ctx,
		artifactDirectory,
		expectedDigest,
		expectedSize,
	); err != nil {
		return nil, err
	}
	return bytes.Clone(manifestCanonical), nil
}

type validatedDocumentInsightArtifactV1 struct {
	ManifestCanonical []byte
	SourceCanonical   []byte
	SourceRef         moduleapi.KnowledgeSourceRefV1
	Provider          moduleapi.ActivatedModuleRef
}

// validateDocumentInsightArtifactFromArtifact is the inert package boundary
// shared by Apply, Dry-run and the production loader. It proves the fixed
// artifact identity and exact dual-Port Manifest without constructing or
// invoking the adapter.
func validateDocumentInsightArtifactFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (validatedDocumentInsightArtifactV1, error) {
	if ctx == nil {
		return validatedDocumentInsightArtifactV1{}, errors.New(
			"Document Insight artifact context is nil",
		)
	}
	if artifactDigest != localDocumentInsightDigest ||
		adapterIdentity != exactadapter.DocumentInsightAdapterIdentityV1 {
		return validatedDocumentInsightArtifactV1{}, errors.New(
			"Document Insight artifact identity is not in the compiled exact adapter set",
		)
	}
	manifestCanonical, sourceCanonical, sourceRef, observedDigest, observedSize, err :=
		inspectModuleApplyDocumentInsightArtifactV1(ctx, artifactDirectory)
	if err != nil {
		return validatedDocumentInsightArtifactV1{}, err
	}
	if observedDigest != artifactDigest ||
		(expectedSize != 0 && observedSize != expectedSize) {
		return validatedDocumentInsightArtifactV1{}, errors.New(
			"Document Insight artifact digest or size mismatch",
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.ID != localDocumentInsightModuleID ||
		manifest.Version != localDocumentInsightVersion {
		return validatedDocumentInsightArtifactV1{}, errors.Join(
			err,
			errors.New("Document Insight artifact Manifest identity differs"),
		)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifest.ID,
		Version:            manifest.Version,
		ArtifactDigest:     artifactDigest,
		InstanceID:         "artifact-adapter-template",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    adapterIdentity,
		ActivationRevision: 1,
	}
	if expectedProvider != nil &&
		(expectedProvider.ModuleID != provider.ModuleID ||
			expectedProvider.Version != provider.Version ||
			expectedProvider.ArtifactDigest != provider.ArtifactDigest ||
			expectedProvider.ExecutionClass != provider.ExecutionClass ||
			expectedProvider.AdapterIdentity != provider.AdapterIdentity) {
		return validatedDocumentInsightArtifactV1{}, errors.New(
			"Document Insight artifact Manifest does not match the expected provider",
		)
	}
	return validatedDocumentInsightArtifactV1{
		ManifestCanonical: bytes.Clone(manifestCanonical),
		SourceCanonical:   bytes.Clone(sourceCanonical),
		SourceRef:         sourceRef,
		Provider:          provider,
	}, nil
}

func loadDocumentInsightInvokerFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	verified, err := validateDocumentInsightArtifactFromArtifact(
		ctx,
		artifactDigest,
		adapterIdentity,
		artifactDirectory,
		expectedSize,
		expectedProvider,
	)
	if err != nil {
		return nil, err
	}
	invoker, err := exactadapter.NewDocumentInsight(
		verified.Provider,
		verified.SourceCanonical,
	)
	if err != nil {
		return nil, err
	}
	return invoker, nil
}

func readModuleApplyDocumentInsightMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) (
	[]byte,
	[]byte,
	moduleapi.KnowledgeSourceRefV1,
	error,
) {
	manifestCanonical, sourceCanonical, sourceRef, _, _, err :=
		inspectModuleApplyDocumentInsightArtifactV1(ctx, artifactDirectory)
	if err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, err
	}
	return manifestCanonical, sourceCanonical, sourceRef, nil
}

func inspectModuleApplyDocumentInsightArtifactV1(
	ctx context.Context,
	artifactDirectory string,
) (
	[]byte,
	[]byte,
	moduleapi.KnowledgeSourceRefV1,
	string,
	uint64,
	error,
) {
	manifestCanonical, files, err := readArtifactContext(ctx, artifactDirectory)
	if err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, err
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, err
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0,
				errors.New("Document Insight artifact size overflow")
		}
		size += uint64(len(file.Content))
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, err
	}
	if err := classifyExactDocumentInsightManifestV1(manifest); err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, errors.Join(
			err,
			errors.New("artifact is not the exact governed dual-Port Document Insight Manifest"),
		)
	}
	var sourceCanonical []byte
	for _, file := range files {
		if file.Path == manifest.Runtime.Entrypoint {
			sourceCanonical = bytes.Clone(file.Content)
			break
		}
	}
	if sourceCanonical == nil ||
		len(sourceCanonical) > moduleapi.MaxKnowledgeSourceBytesV1 {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, errors.New(
			"Document Insight artifact source is absent or exceeds its bound",
		)
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		return nil, nil, moduleapi.KnowledgeSourceRefV1{}, "", 0, err
	}
	return bytes.Clone(manifestCanonical), sourceCanonical, sourceRef, digest, size, nil
}

func readModuleApplyKnowledgeMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) ([]byte, moduleapi.KnowledgeSourceRefV1, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, moduleapi.KnowledgeSourceRefV1{}, err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return nil, moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New(
				"artifact is not exact TRUSTED_IN_PROCESS local knowledge context.provide/v1",
			),
		)
	}
	if _, err := classifyExactKnowledgeManifestV1(manifest); err != nil {
		return nil, moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New(
				"artifact is not an exact supported local Knowledge manifest shape",
			),
		)
	}
	entrypoint := manifest.Runtime.Entrypoint
	sourceCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		artifactDirectory,
		entrypoint,
		moduleapi.MaxKnowledgeSourceBytesV1,
	)
	if err != nil {
		return nil, moduleapi.KnowledgeSourceRefV1{}, err
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		return nil, moduleapi.KnowledgeSourceRefV1{}, err
	}
	return bytes.Clone(manifestCanonical), sourceRef, nil
}

func validateModuleApplyKnowledgeSourceV1(
	plan moduleApplyPlanV1,
	sourceRef moduleapi.KnowledgeSourceRefV1,
) error {
	if plan.Binding == nil {
		return errors.New("enabled Knowledge binding is absent")
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		plan.Binding.Config,
	)
	if err != nil {
		return err
	}
	config, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(
		contextConfig,
	)
	if err != nil {
		return err
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return err
	}
	if config.Source != sourceRef || authority.Source != sourceRef {
		return errors.New(
			"knowledge artifact source differs from the exact Config and authority source",
		)
	}
	return nil
}

func moduleApplyStaticContextFromArtifactV1(
	ctx context.Context,
	artifactRoot string,
	plan moduleApplyPlanV1,
) ([]string, []byte, error) {
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return nil, nil, err
	}
	if policy.HandlerKind != moduleApplyHandlerDeclarativeContextV1 {
		return []string{}, nil, nil
	}
	if plan.Module == nil {
		return nil, nil, errors.New("enabled module is absent")
	}
	_, staticCanonical, err := readModuleApplyDeclarativeMetadataV1(
		ctx,
		filepath.Join(artifactRoot, plan.Module.ArtifactDigest),
	)
	if err != nil {
		return nil, nil, err
	}
	staticRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentStaticContext,
		moduleApplyJSONMediaType,
		staticCanonical,
	)
	if err != nil {
		return nil, nil, err
	}
	return []string{staticRef}, bytes.Clone(staticCanonical), nil
}
