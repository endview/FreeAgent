// Package modulehandler owns the immutable Core policy that maps one exact
// Module runtime and Binding request to the only supported local handler.
//
// It is deliberately pure: it does not open the Store, read artifacts, grant
// authority, stage packages, register adapters, or execute providers. Apply,
// upgrade Review, and future application services must all consume this one
// registry instead of maintaining private compatibility tables.
package modulehandler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	DeepSeekModuleIDV1        = "freeagent.builtin.model.deepseek"
	DeepSeekVersionV1         = "2.0.0"
	DeepSeekArtifactDigestV1  = "ebef19d2fd153773f11e331d219edd4a414af069101f9f834fdd6ecf85dce3e2"
	DeepSeekFlashBuildV1      = "deepseek-v4-flash/public-alias-observed-2026-08-04"
	DeepSeekProBuildV1        = "deepseek-v4-pro/public-alias-observed-2026-08-04"
	DeepSeekProviderNameV1    = "deepseek"
	DeepSeekModelV4FlashV1    = "deepseek-v4-flash"
	DeepSeekModelV4ProV1      = "deepseek-v4-pro"
	DeepSeekMaxOutputTokensV1 = uint64(384000)

	DeclarativeAdapterIdentityV1     = "freeagent.adapter.declarative/v1"
	KnowledgeAdapterIdentityV1       = "freeagent.adapter.knowledge.lexical/v1"
	MemoryAdapterIdentityV1          = "freeagent.adapter.memory.deterministic/v1"
	TextStatsAdapterIdentityV1       = "freeagent.adapter.action.text-stats/v1"
	DeepSeekAdapterIdentityV1        = "freeagent.adapter.model.deepseek/v1"
	MCPStdioAdapterIdentityV1        = "freeagent.adapter.mcp.stdio-tools/v1"
	RemoteActionAdapterIdentityV1    = "freeagent.adapter.action.remote-http/v1"
	WASMActionAdapterIdentityV1      = "freeagent.adapter.action.wasm/v1"
	LoopbackAdapterIdentityV1        = "freeagent.adapter.channel.loopback-http/v1"
	LoopbackAdapterProtocolV1        = "loopback-http/v1"
	DocumentInsightAdapterIdentityV1 = "freeagent.adapter.document-insight/v1"
	TextStatsActionIDV1              = "text.stats"
	TextStatsMaxResultBytesV1        = uint32(256)
	loopbackParametersSchemaV1       = "loopback-http-parameters/v1"
	loopbackMaximumTimeoutMSV1       = uint32(30000)
)

const denyAllAuthorityCanonicalV1 = `{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`

// DenyAllAuthorityCanonicalV1 returns an owned copy of the exact declarative
// Context authority ceiling. Callers cannot mutate the registry's policy.
func DenyAllAuthorityCanonicalV1() []byte {
	return []byte(denyAllAuthorityCanonicalV1)
}

// DeepSeekBuildsV1 returns the exact compiled Model build allowlist as a new
// map on every call.
func DeepSeekBuildsV1() map[string]string {
	return map[string]string{
		DeepSeekModelV4FlashV1: DeepSeekFlashBuildV1,
		DeepSeekModelV4ProV1:   DeepSeekProBuildV1,
	}
}

type KindV1 string

const (
	HandlerDeclarativeContextV1 KindV1 = "DECLARATIVE_CONTEXT"
	HandlerKnowledgeContextV1   KindV1 = "KNOWLEDGE_CONTEXT"
	HandlerMemoryContextV1      KindV1 = "MEMORY_CONTEXT"
	HandlerMCPActionV1          KindV1 = "MCP_ACTION"
	HandlerRemoteActionHTTPV1   KindV1 = "REMOTE_ACTION_HTTP"
	HandlerWASMActionV1         KindV1 = "WASM_ACTION"
	HandlerTextStatsActionV1    KindV1 = "TEXT_STATS_ACTION"
	HandlerDocumentInsightV1    KindV1 = "DOCUMENT_INSIGHT"
	HandlerLoopbackChannelV1    KindV1 = "LOOPBACK_CHANNEL"
	HandlerDeepSeekModelV1      KindV1 = "DEEPSEEK_MODEL"
)

type KeyV1 struct {
	Port            moduleapi.PortRef
	RuntimeMode     moduleapi.RuntimeModeRequest
	RuntimeProtocol string
	ConsumerSchema  string
	ModuleID        string
	ExactVersion    string
	ArtifactDigest  string
}

type PolicyV1 struct {
	Port                                  moduleapi.PortRef
	RuntimeMode                           moduleapi.RuntimeModeRequest
	RuntimeProtocol                       string
	ConsumerSchema                        string
	ModuleID                              string
	ExactVersion                          string
	ArtifactDigest                        string
	ExecutionClass                        moduleapi.ExecutionClass
	AdapterIdentity                       string
	HandlerKind                           KindV1
	RequiresLocalMCPGrant                 bool
	RequiresTrustedInProcessArtifactGrant bool
	RequiresRemoteActionArtifactGrant     bool
	RequiresWASMActionArtifactGrant       bool
}

type RuntimeRequestV1 struct {
	Mode     moduleapi.RuntimeModeRequest
	Protocol string
}

type ModuleIdentityV1 struct {
	ID             string
	ExactVersion   string
	ArtifactDigest string
}

type BindingV1 struct {
	TenantID           string
	Port               moduleapi.PortRef
	ConfigCanonical    []byte
	AuthorityCanonical []byte
	FailurePolicy      moduleapi.FailurePolicy
	RuntimeRequest     RuntimeRequestV1
	Module             *ModuleIdentityV1
}

type AssessmentStatusV1 string

const (
	AssessmentSupportedV1   AssessmentStatusV1 = "SUPPORTED"
	AssessmentConflictV1    AssessmentStatusV1 = "CONFLICT"
	AssessmentUnsupportedV1 AssessmentStatusV1 = "UNSUPPORTED"
)

type AssessmentV1 struct {
	Status AssessmentStatusV1
	Policy PolicyV1
}

type GrantKindV1 string

const (
	GrantLocalProcessArtifactV1     GrantKindV1 = "LOCAL_PROCESS_ARTIFACT"
	GrantTrustedInProcessArtifactV1 GrantKindV1 = "TRUSTED_IN_PROCESS_ARTIFACT"
	GrantRemoteActionArtifactV1     GrantKindV1 = "REMOTE_ACTION_ARTIFACT"
	GrantWASMActionArtifactV1       GrantKindV1 = "WASM_ACTION_ARTIFACT"
	GrantRemoteEndpointDigestV1     GrantKindV1 = "REMOTE_ENDPOINT_DIGEST"
	GrantRemoteCredentialDigestV1   GrantKindV1 = "REMOTE_SECRET_REF_DIGEST"
	GrantModelCredentialDigestV1    GrantKindV1 = "MODEL_SECRET_REF_DIGEST"
)

type GrantRequirementV1 struct {
	Kind            GrantKindV1
	ReferenceDigest string
}

var ErrProtocolHandlerNotFoundV1 = errors.New("core protocol handler is not configured")

func modelPortV1() moduleapi.PortRef {
	return moduleapi.PortRef{Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2}
}

func contextPortV1() moduleapi.PortRef {
	return moduleapi.PortRef{Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1}
}

func actionPortV1() moduleapi.PortRef {
	return moduleapi.PortRef{Name: moduleapi.PortNameActionProvider, ExactVersion: moduleapi.PortVersionV1}
}

func channelPortV1() moduleapi.PortRef {
	return moduleapi.PortRef{Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1}
}

// GenericTableV1 is the ordered nine-row generic/Model registry. Reserved
// product identities are resolved first through ExactSelectorTableV1.
func GenericTableV1() [9]PolicyV1 {
	return [9]PolicyV1{
		{
			Port:                                  modelPortV1(),
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ModelBindingConfigSchemaV2,
			ModuleID:                              DeepSeekModuleIDV1,
			ExactVersion:                          DeepSeekVersionV1,
			ArtifactDigest:                        DeepSeekArtifactDigestV1,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       DeepSeekAdapterIdentityV1,
			HandlerKind:                           HandlerDeepSeekModelV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
		{
			Port:            contextPortV1(),
			RuntimeMode:     moduleapi.RuntimeModeRequestDeclarative,
			RuntimeProtocol: moduleapi.RuntimeProtocolStaticV1,
			ConsumerSchema:  moduleapi.ContextBindingConfigSchemaV1,
			ExecutionClass:  moduleapi.ExecutionDeclarative,
			AdapterIdentity: DeclarativeAdapterIdentityV1,
			HandlerKind:     HandlerDeclarativeContextV1,
		},
		{
			Port:            contextPortV1(),
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:  moduleapi.KnowledgeContextBindingSchemaV1,
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: KnowledgeAdapterIdentityV1,
			HandlerKind:     HandlerKnowledgeContextV1,
		},
		{
			Port:            contextPortV1(),
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:  moduleapi.MemoryContextBindingSchemaV1,
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: MemoryAdapterIdentityV1,
			HandlerKind:     HandlerMemoryContextV1,
		},
		{
			Port:                  actionPortV1(),
			RuntimeMode:           moduleapi.RuntimeModeRequestLocalProcess,
			RuntimeProtocol:       moduleapi.RuntimeProtocolMCPStdio20251125,
			ConsumerSchema:        moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:        moduleapi.ExecutionLocalProcess,
			AdapterIdentity:       MCPStdioAdapterIdentityV1,
			HandlerKind:           HandlerMCPActionV1,
			RequiresLocalMCPGrant: true,
		},
		{
			Port:                              actionPortV1(),
			RuntimeMode:                       moduleapi.RuntimeModeRequestRemote,
			RuntimeProtocol:                   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			ConsumerSchema:                    moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                    moduleapi.ExecutionRemote,
			AdapterIdentity:                   RemoteActionAdapterIdentityV1,
			HandlerKind:                       HandlerRemoteActionHTTPV1,
			RequiresRemoteActionArtifactGrant: true,
		},
		{
			Port:                            actionPortV1(),
			RuntimeMode:                     moduleapi.RuntimeModeRequestWASM,
			RuntimeProtocol:                 moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			ConsumerSchema:                  moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                  moduleapi.ExecutionWASM,
			AdapterIdentity:                 WASMActionAdapterIdentityV1,
			HandlerKind:                     HandlerWASMActionV1,
			RequiresWASMActionArtifactGrant: true,
		},
		{
			Port:                                  actionPortV1(),
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       TextStatsAdapterIdentityV1,
			HandlerKind:                           HandlerTextStatsActionV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
		{
			Port:                                  channelPortV1(),
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ChannelBindingConfigSchemaV1,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       LoopbackAdapterIdentityV1,
			HandlerKind:                           HandlerLoopbackChannelV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
	}
}

// ExactSelectorTableV1 prevents the reserved Document Insight identity from
// falling through to a generic Knowledge or text.stats handler.
func ExactSelectorTableV1() [2]PolicyV1 {
	return [2]PolicyV1{
		{
			Port:                                  contextPortV1(),
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.KnowledgeContextBindingSchemaV1,
			ModuleID:                              moduleapi.DocumentInsightModuleIDV1,
			ExactVersion:                          moduleapi.DocumentInsightVersionV2,
			ArtifactDigest:                        moduleapi.DocumentInsightArtifactDigestV2,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       DocumentInsightAdapterIdentityV1,
			HandlerKind:                           HandlerDocumentInsightV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
		{
			Port:                                  actionPortV1(),
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ActionBindingConfigSchemaV1,
			ModuleID:                              moduleapi.DocumentInsightModuleIDV1,
			ExactVersion:                          moduleapi.DocumentInsightVersionV2,
			ArtifactDigest:                        moduleapi.DocumentInsightArtifactDigestV2,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       DocumentInsightAdapterIdentityV1,
			HandlerKind:                           HandlerDocumentInsightV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
	}
}

func (policy PolicyV1) KeyV1() KeyV1 {
	return KeyV1{
		Port: policy.Port, RuntimeMode: policy.RuntimeMode,
		RuntimeProtocol: policy.RuntimeProtocol, ConsumerSchema: policy.ConsumerSchema,
		ModuleID: policy.ModuleID, ExactVersion: policy.ExactVersion,
		ArtifactDigest: policy.ArtifactDigest,
	}
}

// ProtocolHandlerKeyV1 is a compatibility spelling used by the existing
// command tests while the sole implementation lives in this package.
func (policy PolicyV1) ProtocolHandlerKeyV1() KeyV1 {
	return policy.KeyV1()
}

func (kind KindV1) validate() error {
	switch kind {
	case HandlerDeclarativeContextV1, HandlerKnowledgeContextV1,
		HandlerMemoryContextV1, HandlerMCPActionV1,
		HandlerRemoteActionHTTPV1, HandlerWASMActionV1,
		HandlerTextStatsActionV1, HandlerDocumentInsightV1,
		HandlerLoopbackChannelV1, HandlerDeepSeekModelV1:
		return nil
	default:
		return fmt.Errorf("unsupported Core protocol handler kind %q", kind)
	}
}

func validatePolicyV1(index int, handler PolicyV1) error {
	if err := handler.Port.Validate(); err != nil {
		return fmt.Errorf("Core protocol handler %d port: %w", index, err)
	}
	switch handler.RuntimeMode {
	case moduleapi.RuntimeModeRequestDeclarative,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		moduleapi.RuntimeModeRequestLocalProcess,
		moduleapi.RuntimeModeRequestRemote,
		moduleapi.RuntimeModeRequestWASM:
	default:
		return fmt.Errorf("Core protocol handler %d has unsupported runtime mode %q", index, handler.RuntimeMode)
	}
	if handler.RuntimeProtocol == "" || handler.RuntimeProtocol != strings.TrimSpace(handler.RuntimeProtocol) {
		return fmt.Errorf("Core protocol handler %d has invalid runtime protocol", index)
	}
	if handler.ConsumerSchema == "" || handler.ConsumerSchema != strings.TrimSpace(handler.ConsumerSchema) {
		return fmt.Errorf("Core protocol handler %d has invalid consumer schema", index)
	}
	selectorSet := handler.ModuleID != "" || handler.ExactVersion != "" || handler.ArtifactDigest != ""
	if selectorSet && (handler.ModuleID == "" || handler.ExactVersion == "" || !moduleapi.ValidSHA256(handler.ArtifactDigest)) {
		return fmt.Errorf("Core protocol handler %d has an incomplete exact module selector", index)
	}
	if err := handler.ExecutionClass.Validate(); err != nil {
		return fmt.Errorf("Core protocol handler %d execution class: %w", index, err)
	}
	if handler.AdapterIdentity == "" || handler.AdapterIdentity != strings.TrimSpace(handler.AdapterIdentity) {
		return fmt.Errorf("Core protocol handler %d has invalid adapter identity", index)
	}
	if err := handler.HandlerKind.validate(); err != nil {
		return fmt.Errorf("Core protocol handler %d: %w", index, err)
	}
	grantKinds := 0
	for _, required := range []bool{
		handler.RequiresLocalMCPGrant,
		handler.RequiresTrustedInProcessArtifactGrant,
		handler.RequiresRemoteActionArtifactGrant,
		handler.RequiresWASMActionArtifactGrant,
	} {
		if required {
			grantKinds++
		}
	}
	if grantKinds > 1 {
		return fmt.Errorf("Core protocol handler %d requests conflicting artifact grants", index)
	}
	known := false
	switch handler.HandlerKind {
	case HandlerDeepSeekModelV1:
		known = handler == GenericTableV1()[0]
	case HandlerDeclarativeContextV1:
		known = handler == GenericTableV1()[1]
	case HandlerKnowledgeContextV1:
		known = handler == GenericTableV1()[2]
	case HandlerMemoryContextV1:
		known = handler == GenericTableV1()[3]
	case HandlerMCPActionV1:
		known = handler == GenericTableV1()[4]
	case HandlerRemoteActionHTTPV1:
		known = handler == GenericTableV1()[5]
	case HandlerWASMActionV1:
		known = handler == GenericTableV1()[6]
	case HandlerTextStatsActionV1:
		known = handler == GenericTableV1()[7]
	case HandlerLoopbackChannelV1:
		known = handler == GenericTableV1()[8]
	case HandlerDocumentInsightV1:
		exact := ExactSelectorTableV1()
		known = handler == exact[0] || handler == exact[1]
	}
	if !known {
		return fmt.Errorf("Core protocol handler %d has an invalid fixed handler combination", index)
	}
	return nil
}

func ValidateTableV1(table []PolicyV1) error {
	seen := make(map[KeyV1]int, len(table))
	for index, handler := range table {
		if err := validatePolicyV1(index, handler); err != nil {
			return err
		}
		key := handler.KeyV1()
		if previous, duplicate := seen[key]; duplicate {
			return fmt.Errorf("Core protocol handler table has duplicate exact key at indexes %d and %d", previous, index)
		}
		seen[key] = index
	}
	return nil
}

func ResolveFromTableV1(table []PolicyV1, key KeyV1) (PolicyV1, error) {
	if err := ValidateTableV1(table); err != nil {
		return PolicyV1{}, err
	}
	for _, handler := range table {
		if handler.KeyV1() == key {
			return handler, nil
		}
	}
	return PolicyV1{}, fmt.Errorf(
		"%w for %s/%s mode %q protocol %q consumer schema %q",
		ErrProtocolHandlerNotFoundV1, key.Port.Name, key.Port.ExactVersion,
		key.RuntimeMode, key.RuntimeProtocol, key.ConsumerSchema,
	)
}

func ResolveGenericV1(key KeyV1) (PolicyV1, error) {
	table := GenericTableV1()
	return ResolveFromTableV1(table[:], key)
}

func ResolveExactSelectorV1(key KeyV1) (PolicyV1, error) {
	table := ExactSelectorTableV1()
	return ResolveFromTableV1(table[:], key)
}

// ResolveBindingV1 performs the complete pure binding compatibility check
// used by Apply and upgrade Review.
func ResolveBindingV1(input BindingV1) (PolicyV1, error) {
	policy, err := ResolvePolicyOnlyV1(input)
	if err != nil {
		return PolicyV1{}, err
	}
	switch policy.HandlerKind {
	case HandlerDeepSeekModelV1:
		if err := ValidateDeepSeekBindingCanonicalV1(input.TenantID, input.ConfigCanonical, input.AuthorityCanonical); err != nil {
			return PolicyV1{}, err
		}
	case HandlerMCPActionV1:
		if err := ValidateEmptyActionParametersV1(input.ConfigCanonical); err != nil {
			return PolicyV1{}, err
		}
	case HandlerRemoteActionHTTPV1:
		if _, err := ValidateRemoteActionBindingV1(input.TenantID, input.ConfigCanonical, input.AuthorityCanonical, input.FailurePolicy); err != nil {
			return PolicyV1{}, err
		}
	case HandlerWASMActionV1:
		if err := ValidateWASMActionBindingV1(input.TenantID, input.ConfigCanonical, input.AuthorityCanonical, input.FailurePolicy); err != nil {
			return PolicyV1{}, err
		}
	case HandlerDeclarativeContextV1, HandlerKnowledgeContextV1,
		HandlerMemoryContextV1, HandlerLoopbackChannelV1:
	case HandlerTextStatsActionV1:
		if err := ValidateTextStatsBindingV1(input.ConfigCanonical, input.AuthorityCanonical); err != nil {
			return PolicyV1{}, err
		}
	case HandlerDocumentInsightV1:
		if policy.Port == actionPortV1() {
			if err := ValidateTextStatsBindingV1(input.ConfigCanonical, input.AuthorityCanonical); err != nil {
				return PolicyV1{}, err
			}
		} else if policy.Port != contextPortV1() {
			return PolicyV1{}, errors.New("Document Insight handler has an unsupported Port")
		}
	default:
		return PolicyV1{}, fmt.Errorf("Core protocol handler kind %q has no binding validator", policy.HandlerKind)
	}
	return policy, nil
}

// AssessBindingV1 is the sole classification used by upgrade Review. A known
// handler with an incompatible exact Binding is CONFLICT; an unknown runtime,
// consumer tuple, or reserved exact selector is UNSUPPORTED. Malformed base
// Binding envelopes still return an error and are never downgraded to a
// reviewable status.
func AssessBindingV1(input BindingV1) (AssessmentV1, error) {
	policy, err := ResolveBindingV1(input)
	if err == nil {
		return AssessmentV1{Status: AssessmentSupportedV1, Policy: policy}, nil
	}
	fallback, policyErr := ResolvePolicyOnlyV1(input)
	if policyErr == nil {
		return AssessmentV1{Status: AssessmentConflictV1, Policy: fallback}, nil
	}
	if (errors.Is(err, ErrProtocolHandlerNotFoundV1) ||
		errors.Is(policyErr, ErrProtocolHandlerNotFoundV1)) &&
		input.Module != nil &&
		input.Module.ID == moduleapi.DocumentInsightModuleIDV1 &&
		input.RuntimeRequest.Mode == moduleapi.RuntimeModeRequestTrustedInProcess &&
		input.RuntimeRequest.Protocol == moduleapi.RuntimeProtocolGoInProcessV1 {
		exact := ExactSelectorTableV1()
		reserved := exact[0]
		if input.Port == actionPortV1() {
			reserved = exact[1]
		}
		return AssessmentV1{Status: AssessmentConflictV1, Policy: reserved}, nil
	}
	if errors.Is(err, ErrProtocolHandlerNotFoundV1) ||
		errors.Is(policyErr, ErrProtocolHandlerNotFoundV1) ||
		ValidateRuntimeRequestV1(input.RuntimeRequest) != nil {
		return AssessmentV1{Status: AssessmentUnsupportedV1}, nil
	}
	return AssessmentV1{}, policyErr
}

// RequiredGrantsV1 derives digest-only Review requirements from the exact
// resolved policy and Binding. It grants nothing and exposes no endpoint or
// credential identifier.
func RequiredGrantsV1(
	policy PolicyV1,
	input BindingV1,
	targetDigest string,
) []GrantRequirementV1 {
	result := make([]GrantRequirementV1, 0, 3)
	appendArtifact := func(kind GrantKindV1) {
		result = append(result, GrantRequirementV1{Kind: kind, ReferenceDigest: targetDigest})
	}
	switch {
	case policy.RequiresLocalMCPGrant:
		appendArtifact(GrantLocalProcessArtifactV1)
	case policy.RequiresTrustedInProcessArtifactGrant:
		appendArtifact(GrantTrustedInProcessArtifactV1)
	case policy.RequiresRemoteActionArtifactGrant:
		appendArtifact(GrantRemoteActionArtifactV1)
	case policy.RequiresWASMActionArtifactGrant:
		appendArtifact(GrantWASMActionArtifactV1)
	}
	if policy.HandlerKind == HandlerRemoteActionHTTPV1 {
		if parameters, err := ValidateRemoteActionBindingV1(
			input.TenantID, input.ConfigCanonical, input.AuthorityCanonical, input.FailurePolicy,
		); err == nil {
			result = append(result,
				GrantRequirementV1{Kind: GrantRemoteEndpointDigestV1, ReferenceDigest: grantReferenceDigestV1(GrantRemoteEndpointDigestV1, parameters.EndpointURL)},
				GrantRequirementV1{Kind: GrantRemoteCredentialDigestV1, ReferenceDigest: grantReferenceDigestV1(GrantRemoteCredentialDigestV1, parameters.SecretRef)},
			)
		}
	}
	if policy.HandlerKind == HandlerDeepSeekModelV1 {
		if authority, err := moduleapi.RestoreModelAuthorityCeilingV1(input.AuthorityCanonical); err == nil {
			result = append(result, GrantRequirementV1{
				Kind:            GrantModelCredentialDigestV1,
				ReferenceDigest: grantReferenceDigestV1(GrantModelCredentialDigestV1, authority.SecretRef),
			})
		}
	}
	return result
}

func grantReferenceDigestV1(kind GrantKindV1, value string) string {
	return moduleapi.Digest(
		"freeagent.module-upgrade-required-grant/v1",
		[]byte(string(kind)+"\x00"+value),
	)
}

// ResolvePolicyOnlyV1 resolves immutable handler identity after validating the
// shared Binding envelope, but deliberately skips handler-specific ceilings.
// Review uses this only to distinguish an unsupported handler from a known
// handler whose exact Config/authority is incompatible.
func ResolvePolicyOnlyV1(input BindingV1) (PolicyV1, error) {
	if err := ValidateBindingPolicyV1(
		input.Port, input.TenantID, input.ConfigCanonical,
		input.AuthorityCanonical, input.FailurePolicy,
	); err != nil {
		return PolicyV1{}, err
	}
	if err := ValidateRuntimeRequestV1(input.RuntimeRequest); err != nil {
		return PolicyV1{}, err
	}
	consumerSchema, err := ConsumerSchemaV1(input.Port, input.ConfigCanonical)
	if err != nil {
		return PolicyV1{}, err
	}
	key := KeyV1{
		Port: input.Port, RuntimeMode: input.RuntimeRequest.Mode,
		RuntimeProtocol: input.RuntimeRequest.Protocol, ConsumerSchema: consumerSchema,
	}
	if input.Module != nil && input.Module.ID == moduleapi.DocumentInsightModuleIDV1 {
		key.ModuleID = input.Module.ID
		key.ExactVersion = input.Module.ExactVersion
		key.ArtifactDigest = input.Module.ArtifactDigest
		return ResolveExactSelectorV1(key)
	}
	if input.Port == modelPortV1() {
		if input.Module == nil {
			return PolicyV1{}, errors.New("Model handler requires an exact module selector")
		}
		key.ModuleID = input.Module.ID
		key.ExactVersion = input.Module.ExactVersion
		key.ArtifactDigest = input.Module.ArtifactDigest
	}
	return ResolveGenericV1(key)
}

func ValidateRuntimeRequestV1(request RuntimeRequestV1) error {
	supported := false
	switch request.Mode {
	case moduleapi.RuntimeModeRequestDeclarative:
		supported = request.Protocol == moduleapi.RuntimeProtocolStaticV1
	case moduleapi.RuntimeModeRequestTrustedInProcess:
		supported = request.Protocol == moduleapi.RuntimeProtocolGoInProcessV1
	case moduleapi.RuntimeModeRequestLocalProcess:
		supported = request.Protocol == moduleapi.RuntimeProtocolMCPStdio20251125
	case moduleapi.RuntimeModeRequestRemote:
		supported = request.Protocol == moduleapi.RuntimeProtocolFreeAgentActionHTTPV1
	case moduleapi.RuntimeModeRequestWASM:
		supported = request.Protocol == moduleapi.RuntimeProtocolFreeAgentActionWASMV1
	}
	if !supported {
		return errors.New("module apply plan expected_runtime_request must be one exact Core-supported mode/protocol pair")
	}
	return nil
}

func ConsumerSchemaV1(port moduleapi.PortRef, configCanonical []byte) (string, error) {
	switch port {
	case modelPortV1():
		if _, err := moduleapi.RestoreModelBindingConfigV2(configCanonical); err != nil {
			return "", fmt.Errorf("module apply plan Model config: %w", err)
		}
		return moduleapi.ModelBindingConfigSchemaV2, nil
	case actionPortV1():
		if _, err := moduleapi.RestoreActionBindingConfigV1(configCanonical); err != nil {
			return "", fmt.Errorf("module apply plan Action config: %w", err)
		}
		return moduleapi.ActionBindingConfigSchemaV1, nil
	case contextPortV1():
		config, err := moduleapi.RestoreContextBindingConfigV1(configCanonical)
		if err != nil {
			return "", fmt.Errorf("module apply plan Context config: %w", err)
		}
		if config.Placement == moduleapi.ContextPlacementTrustedInstruction {
			return moduleapi.ContextBindingConfigSchemaV1, nil
		}
		if config.Placement != moduleapi.ContextPlacementUntrustedData {
			return "", errors.New("module apply plan Context placement is unsupported")
		}
		schema, err := moduleapi.ContextBindingParametersSchemaVersionV1(config)
		if err != nil {
			return "", fmt.Errorf("module apply plan dynamic Context protocol: %w", err)
		}
		return schema, nil
	case channelPortV1():
		config, err := moduleapi.RestoreChannelBindingConfigV1(configCanonical)
		if err != nil {
			return "", fmt.Errorf("module apply plan Channel config: %w", err)
		}
		if config.AdapterProtocol != LoopbackAdapterProtocolV1 {
			return "", errors.New("module apply plan Channel config is not loopback-http/v1")
		}
		if err := validateLoopbackBindingConfigV1(config); err != nil {
			return "", fmt.Errorf("module apply plan Channel config: %w", err)
		}
		return moduleapi.ChannelBindingConfigSchemaV1, nil
	default:
		return "", errors.New("module apply plan Port has no consumer schema")
	}
}

func ValidateEmptyActionParametersV1(configCanonical []byte) error {
	config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan Action config: %w", err)
	}
	if !bytes.Equal(config.Parameters, []byte("{}")) {
		return errors.New("selected non-REMOTE Action handler requires parameters to be exactly {}")
	}
	return nil
}

func ValidateWASMActionBindingV1(
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
	failurePolicy moduleapi.FailurePolicy,
) error {
	if failurePolicy != moduleapi.FailureRequired {
		return errors.New("WASM Action binding must be REQUIRED")
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan WASM Action config: %w", err)
	}
	if !bytes.Equal(config.Parameters, []byte("{}")) {
		return errors.New("WASM Action parameters must be exactly {}")
	}
	for _, action := range config.Actions {
		if action.LocalEffectClass != moduleapi.EffectNone {
			return errors.New("WASM Action config effects must all be none")
		}
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(authorityCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan WASM Action authority ceiling: %w", err)
	}
	if authority.TenantID != tenantID || authority.MaxEffectClass != moduleapi.EffectNone {
		return errors.New("WASM Action authority must name the plan tenant and allow effect none only")
	}
	return nil
}

func ValidateRemoteActionBindingV1(
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
	failurePolicy moduleapi.FailurePolicy,
) (moduleapi.RemoteActionHTTPBindingParametersV1, error) {
	if failurePolicy != moduleapi.FailureRequired {
		return moduleapi.RemoteActionHTTPBindingParametersV1{}, errors.New("module apply plan REMOTE Action failure_policy must be REQUIRED")
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
	if err != nil {
		return moduleapi.RemoteActionHTTPBindingParametersV1{}, fmt.Errorf("module apply plan REMOTE Action config: %w", err)
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(config.Parameters)
	if err != nil {
		return moduleapi.RemoteActionHTTPBindingParametersV1{}, fmt.Errorf("module apply plan REMOTE Action parameters: %w", err)
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(authorityCanonical)
	if err != nil {
		return moduleapi.RemoteActionHTTPBindingParametersV1{}, fmt.Errorf("module apply plan REMOTE Action authority ceiling: %w", err)
	}
	if authority.TenantID != tenantID {
		return moduleapi.RemoteActionHTTPBindingParametersV1{}, errors.New("module apply plan REMOTE Action authority tenant differs from plan tenant")
	}
	return parameters, nil
}

func ValidateTextStatsBindingV1(configCanonical []byte, authorityCanonical []byte) error {
	config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan text.stats config: %w", err)
	}
	if len(config.Actions) != 1 ||
		config.Actions[0].ProviderActionID != TextStatsActionIDV1 ||
		config.Actions[0].LocalEffectClass != moduleapi.EffectNone ||
		config.Actions[0].MaxResultBytes > TextStatsMaxResultBytesV1 ||
		!bytes.Equal(config.Parameters, []byte("{}")) {
		return errors.New("module apply plan text.stats config exceeds the compiled exact adapter ceiling")
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(authorityCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan text.stats authority ceiling: %w", err)
	}
	if len(authority.AllowedProviderActionIDs) != 1 ||
		authority.AllowedProviderActionIDs[0] != TextStatsActionIDV1 ||
		authority.MaxEffectClass != moduleapi.EffectNone ||
		authority.MaxResultBytes > TextStatsMaxResultBytesV1 {
		return errors.New("module apply plan text.stats authority exceeds the compiled exact adapter ceiling")
	}
	return nil
}

func ValidateDeepSeekBindingCanonicalV1(
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
) error {
	config, err := moduleapi.RestoreModelBindingConfigV2(configCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan Model config: %w", err)
	}
	if err := validateDeepSeekBindingConfigV1(config); err != nil {
		return fmt.Errorf("module apply plan DeepSeek config: %w", err)
	}
	authority, err := moduleapi.RestoreModelAuthorityCeilingV1(authorityCanonical)
	if err != nil {
		return fmt.Errorf("module apply plan Model authority ceiling: %w", err)
	}
	if authority.TenantID != tenantID {
		return errors.New("module apply plan Model authority tenant differs from plan tenant")
	}
	if authority.Provider != config.Provider {
		return errors.New("module apply plan Model authority provider differs from config")
	}
	if !authority.AllowOfficialProviderEndpoint {
		return errors.New("module apply plan Model authority does not permit the exact official provider endpoint")
	}
	return nil
}

func validateDeepSeekBindingConfigV1(config moduleapi.ModelBindingConfigV2) error {
	if config.Provider != DeepSeekProviderNameV1 {
		return errors.New("provider is not DeepSeek")
	}
	expectedBuild, found := DeepSeekBuildsV1()[config.Model]
	if !found || expectedBuild != config.ModelBuildID {
		return errors.New("model build is not in the exact allowlist")
	}
	if err := validateDeepSeekParametersV1(config.Parameters); err != nil {
		return fmt.Errorf("model parameters: %w", err)
	}
	return nil
}

func validateDeepSeekParametersV1(canonical json.RawMessage) error {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return errors.New("parameters must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("parameters must contain exactly one JSON object")
	}
	for key, raw := range object {
		var err error
		switch key {
		case "max_tokens":
			_, err = positiveIntegerParameterV1(key, raw)
		case "temperature":
			err = boundedNumberParameterV1(key, raw, 0, 2)
		case "top_p":
			err = boundedNumberParameterV1(key, raw, 0, 1)
		case "frequency_penalty", "presence_penalty":
			err = boundedNumberParameterV1(key, raw, -2, 2)
		case "stop":
			err = validateStopParameterV1(raw)
		case "response_format":
			err = validateTypedObjectParameterV1("response_format", raw, "text", "json_object")
		case "thinking":
			err = validateTypedObjectParameterV1("thinking", raw, "enabled", "disabled")
		case "reasoning_effort":
			err = validateStringEnumV1(key, raw, "high", "max")
		case "user_id":
			err = validateUserIDParameterV1(raw)
		default:
			return fmt.Errorf("unsupported parameter %q", key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func positiveIntegerParameterV1(name string, raw json.RawMessage) (uint64, error) {
	text := string(raw)
	if text == "" || strings.ContainsAny(text, ".eE+-") {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	value, err := strconv.ParseUint(text, 10, 63)
	if err != nil || value == 0 || value > DeepSeekMaxOutputTokensV1 {
		return 0, fmt.Errorf("%s must be a positive integer no greater than %d", name, DeepSeekMaxOutputTokensV1)
	}
	return value, nil
}

func boundedNumberParameterV1(name string, raw json.RawMessage, minimum, maximum float64) error {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		return fmt.Errorf("%s must be a number between %g and %g", name, minimum, maximum)
	}
	return nil
}

func validateStopParameterV1(raw json.RawMessage) error {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return validateParameterTextV1("stop", single, 256)
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil || len(multiple) == 0 || len(multiple) > 16 {
		return errors.New("stop must be a string or 1..16 strings")
	}
	for _, value := range multiple {
		if err := validateParameterTextV1("stop", value, 256); err != nil {
			return err
		}
	}
	return nil
}

func validateTypedObjectParameterV1(name string, raw json.RawMessage, allowed ...string) error {
	var value struct {
		Type string `json:"type"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%s must contain only type", name)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must contain only type", name)
	}
	for _, candidate := range allowed {
		if value.Type == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s type is unsupported", name)
}

func validateStringEnumV1(name string, raw json.RawMessage, allowed ...string) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a string", name)
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s is unsupported", name)
}

func validateUserIDParameterV1(raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" || len(value) > 512 {
		return errors.New("user_id must be a non-empty string of at most 512 bytes")
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return errors.New("user_id contains an unsupported character")
	}
	return nil
}

func validateParameterTextV1(name, value string, maximum int) error {
	if value == "" || !utf8.ValidString(value) || len(value) > maximum {
		return fmt.Errorf("%s must be non-empty UTF-8 of at most %d bytes", name, maximum)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

type loopbackParametersV1 struct {
	SchemaVersion    string `json:"schema_version"`
	InboundPath      string `json:"inbound_path"`
	OutboundURL      string `json:"outbound_url"`
	RequestTimeoutMS uint32 `json:"request_timeout_ms"`
}

func validateLoopbackBindingConfigV1(config moduleapi.ChannelBindingConfigV1) error {
	frozen, _, err := moduleapi.NewChannelBindingConfigV1(config)
	if err != nil {
		return fmt.Errorf("loopbackchannel: invalid protocol value: config: %v", err)
	}
	if frozen.AdapterProtocol != LoopbackAdapterProtocolV1 {
		return fmt.Errorf("loopbackchannel: invalid protocol value: adapter_protocol must be %q", LoopbackAdapterProtocolV1)
	}
	var parameters loopbackParametersV1
	if err := decodeStrictCanonicalV1(frozen.Parameters, moduleapi.MaxConfigBytes, &parameters); err != nil {
		return fmt.Errorf("loopbackchannel: invalid protocol value: parameters: %v", err)
	}
	if parameters.SchemaVersion != loopbackParametersSchemaV1 {
		return fmt.Errorf("loopbackchannel: invalid protocol value: parameters schema_version must be %q", loopbackParametersSchemaV1)
	}
	if parameters.InboundPath == "" || parameters.InboundPath != strings.TrimSpace(parameters.InboundPath) ||
		parameters.InboundPath[0] != '/' || path.Clean(parameters.InboundPath) != parameters.InboundPath ||
		strings.Contains(parameters.InboundPath, "//") || strings.ContainsAny(parameters.InboundPath, "?#") {
		return errors.New("loopbackchannel: invalid protocol value: inbound_path must be an absolute canonical path")
	}
	if err := validateLoopbackEndpointV1(parameters.OutboundURL); err != nil {
		return err
	}
	if parameters.RequestTimeoutMS == 0 || parameters.RequestTimeoutMS > loopbackMaximumTimeoutMSV1 {
		return fmt.Errorf("loopbackchannel: invalid protocol value: request_timeout_ms must be between 1 and %d", loopbackMaximumTimeoutMSV1)
	}
	return nil
}

func validateLoopbackEndpointV1(raw string) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return errors.New("loopbackchannel: invalid loopback endpoint: endpoint is empty or contains surrounding whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("loopbackchannel: invalid loopback endpoint: malformed URL")
	}
	if parsed.Scheme != "http" || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return errors.New("loopbackchannel: invalid loopback endpoint: only plain HTTP authority and path are allowed")
	}
	if parsed.RawPath != "" || parsed.Path == "" || parsed.Path[0] != '/' ||
		path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.Path, "//") {
		return errors.New("loopbackchannel: invalid loopback endpoint: path must be absolute and canonical")
	}
	host := parsed.Hostname()
	if strings.Contains(host, "%") {
		return errors.New("loopbackchannel: invalid loopback endpoint: IPv6 zones are forbidden")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("loopbackchannel: invalid loopback endpoint: host must be a literal loopback address")
	}
	if portValue := parsed.Port(); portValue != "" {
		portNumber, err := strconv.Atoi(portValue)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return errors.New("loopbackchannel: invalid loopback endpoint: invalid port")
		}
	}
	return nil
}

func decodeStrictCanonicalV1(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf("wire must contain between 1 and %d bytes", maximum)
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(canonical, moduleapi.CanonicalJSONLimits{
		MaxBytes: maximum, MaxDepth: 32, MaxNodes: maximum,
	})
	if err != nil {
		return err
	}
	if !bytes.Equal(checked, canonical) {
		return errors.New("wire must use RFC 8785 canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("wire contains trailing JSON")
		}
		return err
	}
	return nil
}

func ValidateBindingPolicyV1(
	port moduleapi.PortRef,
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
	failurePolicy moduleapi.FailurePolicy,
) error {
	if err := validatePortV1(port); err != nil {
		return err
	}
	if err := failurePolicy.Validate(); err != nil {
		return fmt.Errorf("module apply plan failure_policy: %w", err)
	}
	switch port {
	case modelPortV1():
		if failurePolicy != moduleapi.FailureRequired {
			return errors.New("module apply plan Model failure_policy must be REQUIRED")
		}
		return ValidateDeepSeekBindingCanonicalV1(tenantID, configCanonical, authorityCanonical)
	case actionPortV1():
		if failurePolicy != moduleapi.FailureRequired {
			return errors.New("module apply plan Action failure_policy must be REQUIRED")
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
		if err != nil {
			return fmt.Errorf("module apply plan Action config: %w", err)
		}
		if !bytes.Equal(config.Parameters, []byte("{}")) {
			if _, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(config.Parameters); err != nil {
				return fmt.Errorf("module apply plan Action config parameters must be exact {} or remote-action-http-binding-parameters/v1: %w", err)
			}
		}
		authority, err := moduleapi.RestoreActionAuthorityCeilingV1(authorityCanonical)
		if err != nil {
			return fmt.Errorf("module apply plan Action authority ceiling: %w", err)
		}
		if authority.TenantID != tenantID {
			return errors.New("module apply plan Action authority tenant differs from plan tenant")
		}
		return nil
	case contextPortV1():
		contextConfig, err := moduleapi.RestoreContextBindingConfigV1(configCanonical)
		if err != nil {
			return fmt.Errorf("module apply plan Context config: %w", err)
		}
		switch contextConfig.Placement {
		case moduleapi.ContextPlacementTrustedInstruction:
			if contextConfig.AllowSummary || contextConfig.AllowDrop || !bytes.Equal(contextConfig.Parameters, []byte("{}")) {
				return errors.New("module apply plan declarative Context config must be exact TRUSTED_INSTRUCTION with retention disabled and parameters {}")
			}
			if !bytes.Equal(authorityCanonical, []byte(denyAllAuthorityCanonicalV1)) {
				return errors.New("module apply plan declarative Context authority must be exact deny-all authority-ceiling/v1")
			}
			return nil
		case moduleapi.ContextPlacementUntrustedData:
			if failurePolicy != moduleapi.FailureRequired {
				return errors.New("module apply plan dynamic Context failure_policy must be REQUIRED")
			}
			schema, err := moduleapi.ContextBindingParametersSchemaVersionV1(contextConfig)
			if err != nil {
				return fmt.Errorf("module apply plan dynamic Context protocol: %w", err)
			}
			switch schema {
			case moduleapi.KnowledgeContextBindingSchemaV1:
				knowledge, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(contextConfig)
				if err != nil {
					return fmt.Errorf("module apply plan Knowledge config: %w", err)
				}
				authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(authorityCanonical)
				if err != nil {
					return fmt.Errorf("module apply plan Knowledge authority ceiling: %w", err)
				}
				if knowledge.Source != authority.Source {
					return errors.New("module apply plan Knowledge Config and authority source differ")
				}
				for _, scope := range authority.AllowedScopes {
					if scope.TenantID != tenantID {
						return errors.New("module apply plan Knowledge authority tenant differs from plan tenant")
					}
				}
				return nil
			case moduleapi.MemoryContextBindingSchemaV1:
				memory, _, err := moduleapi.RestoreMemoryContextBindingParametersV1(contextConfig)
				if err != nil {
					return fmt.Errorf("module apply plan Memory config: %w", err)
				}
				authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(authorityCanonical)
				if err != nil {
					return fmt.Errorf("module apply plan Memory authority ceiling: %w", err)
				}
				if authority.TenantID != tenantID {
					return errors.New("module apply plan Memory authority tenant differs from plan tenant")
				}
				for _, kind := range memory.Kinds {
					found := false
					for _, allowed := range authority.AllowedKinds {
						if allowed == kind {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("module apply plan Memory requests unauthorized kind %q", kind)
					}
				}
				return nil
			default:
				return fmt.Errorf("module apply plan dynamic Context protocol %q is not supported", schema)
			}
		default:
			return errors.New("module apply plan Context placement is unsupported")
		}
	case channelPortV1():
		if failurePolicy != moduleapi.FailureRequired {
			return errors.New("module apply plan Channel failure_policy must be REQUIRED")
		}
		config, err := moduleapi.RestoreChannelBindingConfigV1(configCanonical)
		if err != nil || config.AdapterProtocol != LoopbackAdapterProtocolV1 {
			return errors.Join(err, errors.New("module apply plan Channel config is not exact loopback-http/v1"))
		}
		if err := validateLoopbackBindingConfigV1(config); err != nil {
			return err
		}
		authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(authorityCanonical)
		if err != nil {
			return fmt.Errorf("module apply plan Channel authority ceiling: %w", err)
		}
		if authority.TenantID != tenantID {
			return errors.New("module apply plan Channel authority tenant differs from plan tenant")
		}
		return nil
	default:
		return errors.New("module apply plan Port is unsupported")
	}
}

func validatePortV1(port moduleapi.PortRef) error {
	if err := port.Validate(); err != nil {
		return err
	}
	switch port {
	case modelPortV1(), actionPortV1(), contextPortV1(), channelPortV1():
		return nil
	default:
		return errors.New("only model.generate/v2, action.provider/v1, context.provide/v1, and channel.transport/v1 are supported")
	}
}
