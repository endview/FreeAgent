package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// PermissionKnowledgeReadV1 is the narrow, untrusted permission request used
	// by a governed Knowledge manifest. The declaration carries no authority;
	// Core and Operator policy still close the effective grant.
	PermissionKnowledgeReadV1 Permission = "knowledge.read"

	KnowledgeContextBindingSchemaV1   = "knowledge-context-binding/v1"
	KnowledgeRoutingPolicySchemaV1    = "knowledge-routing-policy/v1"
	KnowledgeAuthorityCeilingSchemaV1 = "knowledge-authority-ceiling/v1"
	KnowledgeContextRequestSchemaV1   = "knowledge-context-request/v1"
	KnowledgeContextOutputSchemaV1    = "knowledge-context-output/v1"
	KnowledgeSourceSchemaV1           = "knowledge-source/v1"

	KnowledgeContextRequestDigestDomainV1 = "freeagent.knowledge-context-request/v1"
	KnowledgeContextOutputDigestDomainV1  = "freeagent.knowledge-context-output/v1"
	KnowledgeContextBindingDigestDomainV1 = "freeagent.knowledge-context-binding/v1"
	KnowledgeChunkDigestDomainV1          = "freeagent.knowledge-chunk/v1"
	KnowledgeSourceDigestDomainV1         = "freeagent.knowledge-source/v1"

	// The first built-in RAG slice is deliberately bounded. Remote, paid or
	// larger retrieval requires a later Attempt/Usage contract.
	MaxKnowledgeHitsV1           = 32
	MaxKnowledgeHitTextBytesV1   = 16 << 10
	MaxKnowledgeTotalTextBytesV1 = 64 << 10
	MaxKnowledgeSourceChunksV1   = MaxManifestEntries
	MaxKnowledgeSourceBytesV1    = MaxTextBytes

	// Routing is consumer-owned configuration, not provider authority. The
	// small fixed limits keep deterministic task classification cheaper than
	// retrieval and prevent a Binding from becoming a second knowledge base.
	MaxKnowledgeCollectionTagsV1      = 32
	MaxKnowledgeMatchTermsV1          = 64
	MaxKnowledgeRoutingValueBytesV1   = MaxOpaqueIDBytes
	MaxKnowledgeReuseCountThresholdV1 = uint64(1<<53 - 1)
	MaxKnowledgeReuseLookbackTurnsV1  = 256
	// Reuse times are represented as safe-integer Unix milliseconds. Keeping
	// the configured seconds below this quotient makes conversion bounded;
	// EvaluateReuseV1 still compares elapsed time without adding timestamps.
	MaxKnowledgeReuseTTLSecondsV1 = MaxKnowledgeReuseCountThresholdV1 / 1000
)

// KnowledgeManifestShapeV1 identifies the only two Knowledge package
// declaration shapes accepted by the E5-A runtime. The governed declaration
// requests authority; it does not grant knowledge access by itself.
type KnowledgeManifestShapeV1 string

const (
	KnowledgeManifestLegacyPermissionlessV1 KnowledgeManifestShapeV1 = "LEGACY_PERMISSIONLESS"
	KnowledgeManifestGovernedV1             KnowledgeManifestShapeV1 = "GOVERNED"
)

// KnowledgeManifestDeclarationV1 is the format-independent projection used
// by conformance, Apply, Current Store publication, Backup and runtime load.
// Keeping the projection in moduleapi prevents those paths from drifting on
// partial Requires/requested_permissions declarations.
type KnowledgeManifestDeclarationV1 struct {
	RuntimeMode          RuntimeModeRequest
	RuntimeProtocol      string
	Entrypoint           string
	Provides             []PortRef
	Requires             []PortRef
	RequestedPermissions []Permission
}

// ClassifyExactKnowledgeManifestV1 accepts either the immutable legacy
// permissionless package or the governed E5-A package. Every other partial or
// expanded shape is rejected until a later exact protocol version defines it.
func ClassifyExactKnowledgeManifestV1(
	manifest ModuleManifestV1,
) (KnowledgeManifestShapeV1, error) {
	return ClassifyExactKnowledgeManifestDeclarationV1(
		KnowledgeManifestDeclarationV1{
			RuntimeMode:          manifest.Runtime.Mode,
			RuntimeProtocol:      manifest.Runtime.Protocol,
			Entrypoint:           manifest.Runtime.Entrypoint,
			Provides:             manifest.Provides,
			Requires:             manifest.Requires,
			RequestedPermissions: manifest.RequestedPermissions,
		},
	)
}

// ClassifyExactKnowledgeManifestDeclarationV1 is the shared classifier for a
// parsed Manifest and a format-only conformance projection.
func ClassifyExactKnowledgeManifestDeclarationV1(
	declaration KnowledgeManifestDeclarationV1,
) (KnowledgeManifestShapeV1, error) {
	if declaration.RuntimeMode != RuntimeModeRequestTrustedInProcess ||
		declaration.RuntimeProtocol != RuntimeProtocolGoInProcessV1 {
		return "", fmt.Errorf(
			"Knowledge manifest must request exact TRUSTED_IN_PROCESS/go-in-process/v1 runtime",
		)
	}
	entrypoint, err := NormalizeArtifactPath(declaration.Entrypoint)
	if err != nil {
		return "", fmt.Errorf("Knowledge manifest entrypoint: %w", err)
	}
	if entrypoint != declaration.Entrypoint ||
		!strings.HasPrefix(entrypoint, "content/") {
		return "", fmt.Errorf(
			"Knowledge manifest entrypoint must be a canonical content/ path",
		)
	}
	if !slices.Equal(declaration.Provides, ExactKnowledgeManifestProvidesV1()) {
		return "", fmt.Errorf(
			"Knowledge manifest must provide only exact context.provide/v1",
		)
	}

	if len(declaration.Requires) == 0 &&
		len(declaration.RequestedPermissions) == 0 {
		return KnowledgeManifestLegacyPermissionlessV1, nil
	}
	if slices.Equal(
		declaration.Requires,
		[]PortRef{ExactModelGeneratePortV1()},
	) && slices.Equal(
		declaration.RequestedPermissions,
		[]Permission{PermissionKnowledgeReadV1},
	) {
		return KnowledgeManifestGovernedV1, nil
	}
	return "", fmt.Errorf(
		"Knowledge manifest must be exactly legacy permissionless or governed with requires model.generate/v1 and permission knowledge.read",
	)
}

// ExactKnowledgeManifestProvidesV1 returns the sole E5-A Knowledge Port.
func ExactKnowledgeManifestProvidesV1() []PortRef {
	return []PortRef{ExactContextProvidePortV1()}
}

// ExactModelGeneratePortV1 returns the exact model dependency used by the
// governed Knowledge declaration.
func ExactModelGeneratePortV1() PortRef {
	return PortRef{Name: PortNameModelGenerate, ExactVersion: PortVersionV1}
}

// ExactContextProvidePortV1 returns the exact Knowledge provider Port.
func ExactContextProvidePortV1() PortRef {
	return PortRef{Name: PortNameContextProvide, ExactVersion: PortVersionV1}
}

// KnowledgeSourceRefV1 identifies one immutable knowledge-source/v1 corpus.
// The digest covers the canonical KnowledgeSourceV1 wire and is independent
// of an Agent or Workspace that elects to bind the source.
type KnowledgeSourceRefV1 struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref KnowledgeSourceRefV1) Validate() error {
	if err := validateKnowledgeOpaque("knowledge source ID", ref.ID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if err := validateKnowledgeOpaque("knowledge source version", ref.Version, MaxVersionBytes); err != nil {
		return err
	}
	if !ValidSHA256(ref.Digest) {
		return fmt.Errorf("knowledge source digest must be lowercase SHA-256")
	}
	return nil
}

// KnowledgeObjectRefV1 is the public, dependency-light exact reference used
// for Workspace and Agent scope. Core maps its typed refs into this wire.
type KnowledgeObjectRefV1 struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref KnowledgeObjectRefV1) Validate() error {
	if err := validateKnowledgeOpaque("knowledge object ID", ref.ID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if err := validateKnowledgeOpaque("knowledge object version", ref.Version, MaxVersionBytes); err != nil {
		return err
	}
	if !ValidSHA256(ref.Digest) {
		return fmt.Errorf("knowledge object digest must be lowercase SHA-256")
	}
	return nil
}

// KnowledgeScopeRuleV1 grants visibility to one exact Tenant and exact or
// wildcard Workspace, Agent and Task. Tenant wildcarding is never allowed.
type KnowledgeScopeRuleV1 struct {
	TenantID     string `json:"tenant_id"`
	WorkspaceID  string `json:"workspace_id"`
	AgentID      string `json:"agent_id"`
	TaskInputRef string `json:"task_input_ref"`
}

func (rule KnowledgeScopeRuleV1) Validate() error {
	if rule.TenantID == "*" {
		return fmt.Errorf("knowledge scope tenant_id must be exact")
	}
	if err := validateKnowledgeOpaque("knowledge scope tenant_id", rule.TenantID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"workspace_id": rule.WorkspaceID,
		"agent_id":     rule.AgentID,
	} {
		if value == "*" {
			continue
		}
		if err := validateKnowledgeOpaque("knowledge scope "+name, value, MaxOpaqueIDBytes); err != nil {
			return err
		}
	}
	if rule.TaskInputRef != "*" && !ValidSHA256(rule.TaskInputRef) {
		return fmt.Errorf("knowledge scope task_input_ref must be an exact digest or wildcard")
	}
	return nil
}

// KnowledgeQueryScopeV1 is constructed only from an already frozen Run.
type KnowledgeQueryScopeV1 struct {
	TenantID     string               `json:"tenant_id"`
	Workspace    KnowledgeObjectRefV1 `json:"workspace"`
	Agent        KnowledgeObjectRefV1 `json:"agent"`
	TaskInputRef string               `json:"task_input_ref"`
}

func (scope KnowledgeQueryScopeV1) Validate() error {
	if scope.TenantID == "*" {
		return fmt.Errorf("knowledge query tenant_id must be exact")
	}
	if err := validateKnowledgeOpaque("knowledge query tenant_id", scope.TenantID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if err := scope.Workspace.Validate(); err != nil {
		return fmt.Errorf("knowledge query workspace: %w", err)
	}
	if err := scope.Agent.Validate(); err != nil {
		return fmt.Errorf("knowledge query agent: %w", err)
	}
	if !ValidSHA256(scope.TaskInputRef) {
		return fmt.Errorf("knowledge query task_input_ref must be an exact digest")
	}
	return nil
}

// KnowledgeScopeAllowsV1 performs the four-level rule match. Invalid values
// are denied; callers that need diagnostic errors should Validate first.
func KnowledgeScopeAllowsV1(
	rule KnowledgeScopeRuleV1,
	scope KnowledgeQueryScopeV1,
) bool {
	if rule.Validate() != nil || scope.Validate() != nil {
		return false
	}
	return rule.TenantID == scope.TenantID &&
		(rule.WorkspaceID == "*" || rule.WorkspaceID == scope.Workspace.ID) &&
		(rule.AgentID == "*" || rule.AgentID == scope.Agent.ID) &&
		(rule.TaskInputRef == "*" || rule.TaskInputRef == scope.TaskInputRef)
}

// KnowledgeReusePolicyV1 is an optional, consumer-owned gate for reusing an
// earlier result instead of retrieving this Source again. Presence enables
// the policy; absence is the default-off state. V1 deliberately permits only
// exact-question reuse. The thresholds are additional gates, not authority.
type KnowledgeReusePolicyV1 struct {
	ExactQuestionOnly    bool   `json:"exact_question_only"`
	MinCategoryCount     uint64 `json:"min_category_count"`
	MinRepeatedTermCount uint64 `json:"min_repeated_term_count"`
	MaxLookbackTurns     uint32 `json:"max_lookback_turns"`
	ReuseTTLSeconds      uint64 `json:"reuse_ttl_seconds"`
}

func (policy KnowledgeReusePolicyV1) Validate() error {
	if !policy.ExactQuestionOnly {
		return fmt.Errorf("knowledge reuse policy v1 requires exact_question_only")
	}
	if policy.MinCategoryCount == 0 ||
		policy.MinCategoryCount > MaxKnowledgeReuseCountThresholdV1 {
		return fmt.Errorf(
			"knowledge reuse policy min_category_count must be between 1 and %d",
			MaxKnowledgeReuseCountThresholdV1,
		)
	}
	if policy.MinRepeatedTermCount == 0 ||
		policy.MinRepeatedTermCount > MaxKnowledgeReuseCountThresholdV1 {
		return fmt.Errorf(
			"knowledge reuse policy min_repeated_term_count must be between 1 and %d",
			MaxKnowledgeReuseCountThresholdV1,
		)
	}
	if policy.MaxLookbackTurns == 0 ||
		policy.MaxLookbackTurns > MaxKnowledgeReuseLookbackTurnsV1 {
		return fmt.Errorf(
			"knowledge reuse policy max_lookback_turns must be between 1 and %d",
			MaxKnowledgeReuseLookbackTurnsV1,
		)
	}
	if policy.ReuseTTLSeconds == 0 ||
		policy.ReuseTTLSeconds > MaxKnowledgeReuseTTLSecondsV1 {
		return fmt.Errorf(
			"knowledge reuse policy reuse_ttl_seconds must be between 1 and %d",
			MaxKnowledgeReuseTTLSecondsV1,
		)
	}
	return nil
}

// KnowledgeRoutingPolicyV1 treats the Binding's one immutable Source as one
// Collection. Tags describe that Collection; match terms and the minimum
// match count are deterministic candidate-selection hints. They cannot grant
// visibility or expand the Binding/authority retrieval limits.
type KnowledgeRoutingPolicyV1 struct {
	SchemaVersion  string                  `json:"schema_version"`
	CollectionTags []string                `json:"collection_tags"`
	MatchTerms     []string                `json:"match_terms"`
	MinMatchTerms  uint32                  `json:"min_match_terms"`
	Reuse          *KnowledgeReusePolicyV1 `json:"reuse,omitempty"`
}

func (policy KnowledgeRoutingPolicyV1) Validate() error {
	_, err := normalizeKnowledgeRoutingPolicyV1(policy)
	return err
}

// KnowledgeContextBindingV1 is the only dynamic Parameters schema accepted
// inside context-binding-config/v1 for the first local RAG slice. Routing is
// optional and last so a nil policy preserves the exact legacy wire.
type KnowledgeContextBindingV1 struct {
	SchemaVersion     string                    `json:"schema_version"`
	Source            KnowledgeSourceRefV1      `json:"source"`
	MaxHits           uint32                    `json:"max_hits"`
	MaxTotalTextBytes uint32                    `json:"max_total_text_bytes"`
	Routing           *KnowledgeRoutingPolicyV1 `json:"routing,omitempty"`
}

func (binding KnowledgeContextBindingV1) Validate() error {
	_, err := normalizeKnowledgeContextBindingV1(binding)
	return err
}

func NewKnowledgeContextBindingV1(
	input KnowledgeContextBindingV1,
) (KnowledgeContextBindingV1, []byte, error) {
	frozen, err := normalizeKnowledgeContextBindingV1(input)
	if err != nil {
		return KnowledgeContextBindingV1{}, nil, err
	}
	canonical, err := marshalCanonicalKnowledgeWire(frozen, MaxConfigBytes)
	if err != nil {
		return KnowledgeContextBindingV1{}, nil, err
	}
	return frozen, canonical, nil
}

// ComputeKnowledgeContextBindingDigestV1 closes the exact normalized Binding
// configuration without changing the established New/Restore signatures.
func ComputeKnowledgeContextBindingDigestV1(
	input KnowledgeContextBindingV1,
) (string, error) {
	_, canonical, err := NewKnowledgeContextBindingV1(input)
	if err != nil {
		return "", err
	}
	return Digest(KnowledgeContextBindingDigestDomainV1, canonical), nil
}

func RestoreKnowledgeContextBindingV1(
	canonical []byte,
) (KnowledgeContextBindingV1, error) {
	var decoded KnowledgeContextBindingV1
	if err := decodeExactKnowledgeWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return KnowledgeContextBindingV1{}, err
	}
	restored, rebuilt, err := NewKnowledgeContextBindingV1(decoded)
	if err != nil {
		return KnowledgeContextBindingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeContextBindingV1{}, fmt.Errorf("knowledge context binding is not frozen canonically")
	}
	return restored, nil
}

// RestoreKnowledgeContextBindingParametersV1 validates the complete consumer
// configuration as a dynamic RAG binding before interpreting Parameters.
func RestoreKnowledgeContextBindingParametersV1(
	config ContextBindingConfigV1,
) (KnowledgeContextBindingV1, error) {
	if config.SchemaVersion != ContextBindingConfigSchemaV1 ||
		config.Placement != ContextPlacementUntrustedData ||
		config.AllowSummary || config.AllowDrop {
		return KnowledgeContextBindingV1{}, fmt.Errorf("dynamic knowledge context binding must be UNTRUSTED_DATA and non-retainable")
	}
	return RestoreKnowledgeContextBindingV1(config.Parameters)
}

// KnowledgeAuthorityCeilingV1 is Core-owned authority. Provider configuration
// may request lower limits, but cannot expand this source or scope set.
type KnowledgeAuthorityCeilingV1 struct {
	SchemaVersion     string                 `json:"schema_version"`
	Source            KnowledgeSourceRefV1   `json:"source"`
	AllowedScopes     []KnowledgeScopeRuleV1 `json:"allowed_scopes"`
	MaxHits           uint32                 `json:"max_hits"`
	MaxTotalTextBytes uint32                 `json:"max_total_text_bytes"`
}

func (ceiling KnowledgeAuthorityCeilingV1) Validate() error {
	if ceiling.SchemaVersion != KnowledgeAuthorityCeilingSchemaV1 {
		return fmt.Errorf("knowledge authority ceiling schema_version must be %q", KnowledgeAuthorityCeilingSchemaV1)
	}
	if err := ceiling.Source.Validate(); err != nil {
		return fmt.Errorf("knowledge authority ceiling source: %w", err)
	}
	if err := validateKnowledgeLimits("knowledge authority ceiling", ceiling.MaxHits, ceiling.MaxTotalTextBytes); err != nil {
		return err
	}
	if len(ceiling.AllowedScopes) == 0 || len(ceiling.AllowedScopes) > MaxManifestEntries {
		return fmt.Errorf("knowledge authority ceiling must contain between 1 and %d scope rules", MaxManifestEntries)
	}
	for index, rule := range ceiling.AllowedScopes {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("knowledge authority ceiling scope %d: %w", index, err)
		}
		if index > 0 && compareKnowledgeScopeRule(ceiling.AllowedScopes[index-1], rule) >= 0 {
			return fmt.Errorf("knowledge authority ceiling scopes must be strictly sorted and unique")
		}
	}
	return nil
}

func NewKnowledgeAuthorityCeilingV1(
	input KnowledgeAuthorityCeilingV1,
) (KnowledgeAuthorityCeilingV1, []byte, error) {
	frozen := input
	var err error
	frozen.AllowedScopes, err = normalizeKnowledgeScopeRules(input.AllowedScopes)
	if err != nil {
		return KnowledgeAuthorityCeilingV1{}, nil, fmt.Errorf("knowledge authority ceiling: %w", err)
	}
	if err := frozen.Validate(); err != nil {
		return KnowledgeAuthorityCeilingV1{}, nil, err
	}
	canonical, err := marshalCanonicalKnowledgeWire(frozen, MaxConfigBytes)
	if err != nil {
		return KnowledgeAuthorityCeilingV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreKnowledgeAuthorityCeilingV1(
	canonical []byte,
) (KnowledgeAuthorityCeilingV1, error) {
	var decoded KnowledgeAuthorityCeilingV1
	if err := decodeExactKnowledgeWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return KnowledgeAuthorityCeilingV1{}, err
	}
	restored, rebuilt, err := NewKnowledgeAuthorityCeilingV1(decoded)
	if err != nil {
		return KnowledgeAuthorityCeilingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeAuthorityCeilingV1{}, fmt.Errorf("knowledge authority ceiling is not frozen canonically")
	}
	return restored, nil
}

// ResolveKnowledgeLimitsV1 proves source and scope authority, then returns
// the intersection of consumer configuration and Core authority.
func ResolveKnowledgeLimitsV1(
	binding KnowledgeContextBindingV1,
	ceiling KnowledgeAuthorityCeilingV1,
	scope KnowledgeQueryScopeV1,
) (uint32, uint32, error) {
	if err := binding.Validate(); err != nil {
		return 0, 0, err
	}
	if err := ceiling.Validate(); err != nil {
		return 0, 0, err
	}
	if err := scope.Validate(); err != nil {
		return 0, 0, err
	}
	if binding.Source != ceiling.Source {
		return 0, 0, fmt.Errorf("knowledge binding and authority source differ")
	}
	allowed := false
	for _, rule := range ceiling.AllowedScopes {
		if KnowledgeScopeAllowsV1(rule, scope) {
			allowed = true
			break
		}
	}
	if !allowed {
		return 0, 0, fmt.Errorf("knowledge query scope is not allowed by authority ceiling")
	}
	return minKnowledgeUint32(binding.MaxHits, ceiling.MaxHits),
		minKnowledgeUint32(binding.MaxTotalTextBytes, ceiling.MaxTotalTextBytes), nil
}

// KnowledgeContextRequestV1 is the exact context.provide/v1 dynamic input.
// It deliberately excludes Run, Attempt, invocation, routing and time values.
type KnowledgeContextRequestV1 struct {
	SchemaVersion     string                `json:"schema_version"`
	Source            KnowledgeSourceRefV1  `json:"source"`
	Scope             KnowledgeQueryScopeV1 `json:"scope"`
	QueryText         string                `json:"query_text"`
	MaxHits           uint32                `json:"max_hits"`
	MaxTotalTextBytes uint32                `json:"max_total_text_bytes"`
}

func (request KnowledgeContextRequestV1) Validate() error {
	if request.SchemaVersion != KnowledgeContextRequestSchemaV1 {
		return fmt.Errorf("knowledge context request schema_version must be %q", KnowledgeContextRequestSchemaV1)
	}
	if err := request.Source.Validate(); err != nil {
		return fmt.Errorf("knowledge context request source: %w", err)
	}
	if err := request.Scope.Validate(); err != nil {
		return fmt.Errorf("knowledge context request scope: %w", err)
	}
	if err := validateBoundedText("knowledge query text", request.QueryText, MaxTextBytes, false); err != nil {
		return err
	}
	if request.QueryText != CanonicalText(request.QueryText) {
		return fmt.Errorf("knowledge query text must use Unicode NFC")
	}
	return validateKnowledgeLimits("knowledge context request", request.MaxHits, request.MaxTotalTextBytes)
}

func NewKnowledgeContextRequestV1(
	input KnowledgeContextRequestV1,
) (KnowledgeContextRequestV1, []byte, string, error) {
	if err := input.Validate(); err != nil {
		return KnowledgeContextRequestV1{}, nil, "", err
	}
	canonical, err := marshalCanonicalKnowledgeWire(input, MaxTextBytes)
	if err != nil {
		return KnowledgeContextRequestV1{}, nil, "", err
	}
	return input, canonical, Digest(KnowledgeContextRequestDigestDomainV1, canonical), nil
}

func RestoreKnowledgeContextRequestV1(
	canonical []byte,
) (KnowledgeContextRequestV1, string, error) {
	var decoded KnowledgeContextRequestV1
	if err := decodeExactKnowledgeWire(canonical, MaxTextBytes, &decoded); err != nil {
		return KnowledgeContextRequestV1{}, "", err
	}
	restored, rebuilt, digest, err := NewKnowledgeContextRequestV1(decoded)
	if err != nil {
		return KnowledgeContextRequestV1{}, "", err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeContextRequestV1{}, "", fmt.Errorf("knowledge context request is not frozen canonically")
	}
	return restored, digest, nil
}

func ComputeKnowledgeContextRequestDigestV1(
	input KnowledgeContextRequestV1,
) (string, error) {
	_, _, digest, err := NewKnowledgeContextRequestV1(input)
	return digest, err
}

// KnowledgeDocumentRefV1 identifies the immutable source document from which
// one or more chunks were produced.
type KnowledgeDocumentRefV1 struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (ref KnowledgeDocumentRefV1) Validate() error {
	if err := validateKnowledgeOpaque("knowledge document ID", ref.ID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if err := validateKnowledgeOpaque("knowledge document version", ref.Version, MaxVersionBytes); err != nil {
		return err
	}
	if !ValidSHA256(ref.Digest) {
		return fmt.Errorf("knowledge document digest must be lowercase SHA-256")
	}
	return nil
}

// KnowledgeHitV1 is a ranked result. ChunkDigest excludes Rank, so ranking
// changes cannot disguise modified source content or ACLs.
type KnowledgeHitV1 struct {
	Rank        uint32                 `json:"rank"`
	Document    KnowledgeDocumentRefV1 `json:"document"`
	ChunkID     string                 `json:"chunk_id"`
	ChunkDigest string                 `json:"chunk_digest"`
	Text        string                 `json:"text"`
	VisibleTo   []KnowledgeScopeRuleV1 `json:"visible_to"`
}

func (hit KnowledgeHitV1) Validate() error {
	if hit.Rank == 0 {
		return fmt.Errorf("knowledge hit rank must be positive")
	}
	if !ValidSHA256(hit.ChunkDigest) {
		return fmt.Errorf("knowledge hit chunk_digest must be lowercase SHA-256")
	}
	chunk := KnowledgeChunkV1{
		Document:    hit.Document,
		ChunkID:     hit.ChunkID,
		ChunkDigest: hit.ChunkDigest,
		Text:        hit.Text,
		VisibleTo:   hit.VisibleTo,
	}
	_, _, err := NewKnowledgeChunkV1(chunk)
	return err
}

// KnowledgeContextOutputV1 is the exact normalized provider result. An empty
// Hits array is a successful, auditable zero-hit retrieval.
type KnowledgeContextOutputV1 struct {
	SchemaVersion string               `json:"schema_version"`
	RequestDigest string               `json:"request_digest"`
	Source        KnowledgeSourceRefV1 `json:"source"`
	Hits          []KnowledgeHitV1     `json:"hits"`
}

func (output KnowledgeContextOutputV1) Validate() error {
	_, _, _, err := newKnowledgeContextOutputV1(output)
	return err
}

func NewKnowledgeContextOutputV1(
	input KnowledgeContextOutputV1,
) (KnowledgeContextOutputV1, []byte, string, error) {
	return newKnowledgeContextOutputV1(input)
}

func newKnowledgeContextOutputV1(
	input KnowledgeContextOutputV1,
) (KnowledgeContextOutputV1, []byte, string, error) {
	if input.SchemaVersion != KnowledgeContextOutputSchemaV1 {
		return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output schema_version must be %q", KnowledgeContextOutputSchemaV1)
	}
	if !ValidSHA256(input.RequestDigest) {
		return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output request_digest must be lowercase SHA-256")
	}
	if err := input.Source.Validate(); err != nil {
		return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output source: %w", err)
	}
	if len(input.Hits) > MaxKnowledgeHitsV1 {
		return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output exceeds %d hits", MaxKnowledgeHitsV1)
	}
	frozen := input
	frozen.Hits = make([]KnowledgeHitV1, len(input.Hits))
	chunkKeys := make(map[string]struct{}, len(input.Hits))
	chunkDigests := make(map[string]struct{}, len(input.Hits))
	totalBytes := 0
	for index, hit := range input.Hits {
		if hit.Rank != uint32(index+1) {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge hit rank %d must equal %d", hit.Rank, index+1)
		}
		if !ValidSHA256(hit.ChunkDigest) {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge hit %d chunk_digest must be lowercase SHA-256", index)
		}
		chunk, _, err := NewKnowledgeChunkV1(KnowledgeChunkV1{
			Document:    hit.Document,
			ChunkID:     hit.ChunkID,
			ChunkDigest: hit.ChunkDigest,
			Text:        hit.Text,
			VisibleTo:   hit.VisibleTo,
		})
		if err != nil {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge hit %d: %w", index, err)
		}
		key := knowledgeChunkIdentityKey(chunk)
		if _, exists := chunkKeys[key]; exists {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output repeats chunk identity")
		}
		if _, exists := chunkDigests[chunk.ChunkDigest]; exists {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output repeats chunk digest")
		}
		chunkKeys[key] = struct{}{}
		chunkDigests[chunk.ChunkDigest] = struct{}{}
		totalBytes += len(chunk.Text)
		if totalBytes > MaxKnowledgeTotalTextBytesV1 {
			return KnowledgeContextOutputV1{}, nil, "", fmt.Errorf("knowledge context output exceeds %d text bytes", MaxKnowledgeTotalTextBytesV1)
		}
		frozen.Hits[index] = KnowledgeHitV1{
			Rank:        hit.Rank,
			Document:    chunk.Document,
			ChunkID:     chunk.ChunkID,
			ChunkDigest: chunk.ChunkDigest,
			Text:        chunk.Text,
			VisibleTo:   append([]KnowledgeScopeRuleV1(nil), chunk.VisibleTo...),
		}
	}
	if frozen.Hits == nil {
		frozen.Hits = []KnowledgeHitV1{}
	}
	canonical, err := marshalCanonicalKnowledgeWire(frozen, MaxTextBytes)
	if err != nil {
		return KnowledgeContextOutputV1{}, nil, "", err
	}
	return frozen, canonical, Digest(KnowledgeContextOutputDigestDomainV1, canonical), nil
}

func RestoreKnowledgeContextOutputV1(
	canonical []byte,
) (KnowledgeContextOutputV1, string, error) {
	var decoded KnowledgeContextOutputV1
	if err := decodeExactKnowledgeWire(canonical, MaxTextBytes, &decoded); err != nil {
		return KnowledgeContextOutputV1{}, "", err
	}
	restored, rebuilt, digest, err := NewKnowledgeContextOutputV1(decoded)
	if err != nil {
		return KnowledgeContextOutputV1{}, "", err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeContextOutputV1{}, "", fmt.Errorf("knowledge context output is not frozen canonically")
	}
	return restored, digest, nil
}

func ComputeKnowledgeContextOutputDigestV1(
	input KnowledgeContextOutputV1,
) (string, error) {
	_, _, digest, err := NewKnowledgeContextOutputV1(input)
	return digest, err
}

// ValidateKnowledgeContextOutputForRequestV1 closes the provider response
// against Core's exact request, including current-scope visibility and the
// request's lower limits.
func ValidateKnowledgeContextOutputForRequestV1(
	request KnowledgeContextRequestV1,
	output KnowledgeContextOutputV1,
) error {
	frozenRequest, _, requestDigest, err := NewKnowledgeContextRequestV1(request)
	if err != nil {
		return err
	}
	frozenOutput, _, _, err := NewKnowledgeContextOutputV1(output)
	if err != nil {
		return err
	}
	if frozenOutput.RequestDigest != requestDigest {
		return fmt.Errorf("knowledge output does not close the exact request digest")
	}
	if frozenOutput.Source != frozenRequest.Source {
		return fmt.Errorf("knowledge output source differs from request source")
	}
	if uint32(len(frozenOutput.Hits)) > frozenRequest.MaxHits {
		return fmt.Errorf("knowledge output exceeds request hit limit")
	}
	totalBytes := uint32(0)
	for index, hit := range frozenOutput.Hits {
		totalBytes += uint32(len(hit.Text))
		if totalBytes > frozenRequest.MaxTotalTextBytes {
			return fmt.Errorf("knowledge output exceeds request text limit")
		}
		visible := false
		for _, rule := range hit.VisibleTo {
			if KnowledgeScopeAllowsV1(rule, frozenRequest.Scope) {
				visible = true
				break
			}
		}
		if !visible {
			return fmt.Errorf("knowledge hit %d is not visible to request scope", index)
		}
	}
	return nil
}

// KnowledgeChunkV1 is the immutable content/ACL wire packaged in a local
// module artifact. ChunkDigest covers every field except itself.
type KnowledgeChunkV1 struct {
	Document    KnowledgeDocumentRefV1 `json:"document"`
	ChunkID     string                 `json:"chunk_id"`
	ChunkDigest string                 `json:"chunk_digest"`
	Text        string                 `json:"text"`
	VisibleTo   []KnowledgeScopeRuleV1 `json:"visible_to"`
}

type knowledgeChunkDigestWireV1 struct {
	Document  KnowledgeDocumentRefV1 `json:"document"`
	ChunkID   string                 `json:"chunk_id"`
	Text      string                 `json:"text"`
	VisibleTo []KnowledgeScopeRuleV1 `json:"visible_to"`
}

func NewKnowledgeChunkV1(
	input KnowledgeChunkV1,
) (KnowledgeChunkV1, []byte, error) {
	frozen, digest, err := normalizeKnowledgeChunkV1(input)
	if err != nil {
		return KnowledgeChunkV1{}, nil, err
	}
	if input.ChunkDigest != "" && input.ChunkDigest != digest {
		return KnowledgeChunkV1{}, nil, fmt.Errorf("knowledge chunk digest does not match canonical content")
	}
	frozen.ChunkDigest = digest
	canonical, err := marshalCanonicalKnowledgeWire(frozen, MaxConfigBytes)
	if err != nil {
		return KnowledgeChunkV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreKnowledgeChunkV1(canonical []byte) (KnowledgeChunkV1, error) {
	var decoded KnowledgeChunkV1
	if err := decodeExactKnowledgeWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return KnowledgeChunkV1{}, err
	}
	restored, rebuilt, err := NewKnowledgeChunkV1(decoded)
	if err != nil {
		return KnowledgeChunkV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeChunkV1{}, fmt.Errorf("knowledge chunk is not frozen canonically")
	}
	return restored, nil
}

func ComputeKnowledgeChunkDigestV1(input KnowledgeChunkV1) (string, error) {
	_, digest, err := normalizeKnowledgeChunkV1(input)
	return digest, err
}

func normalizeKnowledgeChunkV1(
	input KnowledgeChunkV1,
) (KnowledgeChunkV1, string, error) {
	if err := input.Document.Validate(); err != nil {
		return KnowledgeChunkV1{}, "", fmt.Errorf("knowledge chunk document: %w", err)
	}
	if err := validateKnowledgeOpaque("knowledge chunk ID", input.ChunkID, MaxOpaqueIDBytes); err != nil {
		return KnowledgeChunkV1{}, "", err
	}
	if err := validateBoundedText("knowledge chunk text", input.Text, MaxKnowledgeHitTextBytesV1, false); err != nil {
		return KnowledgeChunkV1{}, "", err
	}
	if input.Text != CanonicalText(input.Text) {
		return KnowledgeChunkV1{}, "", fmt.Errorf("knowledge chunk text must use Unicode NFC")
	}
	rules, err := normalizeKnowledgeScopeRules(input.VisibleTo)
	if err != nil {
		return KnowledgeChunkV1{}, "", fmt.Errorf("knowledge chunk visible_to: %w", err)
	}
	if len(rules) == 0 {
		return KnowledgeChunkV1{}, "", fmt.Errorf("knowledge chunk visible_to must not be empty")
	}
	frozen := input
	frozen.VisibleTo = rules
	digestWire := knowledgeChunkDigestWireV1{
		Document:  frozen.Document,
		ChunkID:   frozen.ChunkID,
		Text:      frozen.Text,
		VisibleTo: frozen.VisibleTo,
	}
	canonical, err := marshalCanonicalKnowledgeWire(digestWire, MaxConfigBytes)
	if err != nil {
		return KnowledgeChunkV1{}, "", err
	}
	return frozen, Digest(KnowledgeChunkDigestDomainV1, canonical), nil
}

// KnowledgeSourceV1 is the immutable built-in lexical corpus wire. It omits
// its own digest to avoid a circular definition; New/Restore return the exact
// KnowledgeSourceRefV1 derived from its canonical bytes.
type KnowledgeSourceV1 struct {
	SchemaVersion string             `json:"schema_version"`
	ID            string             `json:"id"`
	Version       string             `json:"version"`
	Chunks        []KnowledgeChunkV1 `json:"chunks"`
}

func NewKnowledgeSourceV1(
	input KnowledgeSourceV1,
) (KnowledgeSourceV1, []byte, KnowledgeSourceRefV1, error) {
	if input.SchemaVersion != KnowledgeSourceSchemaV1 {
		return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source schema_version must be %q", KnowledgeSourceSchemaV1)
	}
	if err := validateKnowledgeOpaque("knowledge source ID", input.ID, MaxOpaqueIDBytes); err != nil {
		return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, err
	}
	if err := validateKnowledgeOpaque("knowledge source version", input.Version, MaxVersionBytes); err != nil {
		return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, err
	}
	if len(input.Chunks) == 0 || len(input.Chunks) > MaxKnowledgeSourceChunksV1 {
		return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source must contain between 1 and %d chunks", MaxKnowledgeSourceChunksV1)
	}
	frozen := input
	frozen.Chunks = make([]KnowledgeChunkV1, len(input.Chunks))
	for index, chunk := range input.Chunks {
		normalized, _, err := NewKnowledgeChunkV1(chunk)
		if err != nil {
			return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source chunk %d: %w", index, err)
		}
		frozen.Chunks[index] = normalized
	}
	sort.Slice(frozen.Chunks, func(left, right int) bool {
		return knowledgeChunkIdentityKey(frozen.Chunks[left]) < knowledgeChunkIdentityKey(frozen.Chunks[right])
	})
	seenDigests := make(map[string]struct{}, len(frozen.Chunks))
	for index, chunk := range frozen.Chunks {
		if index > 0 && knowledgeChunkIdentityKey(frozen.Chunks[index-1]) == knowledgeChunkIdentityKey(chunk) {
			return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source repeats chunk identity")
		}
		if _, exists := seenDigests[chunk.ChunkDigest]; exists {
			return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source repeats chunk digest")
		}
		seenDigests[chunk.ChunkDigest] = struct{}{}
	}
	canonical, err := marshalCanonicalKnowledgeWire(frozen, MaxKnowledgeSourceBytesV1)
	if err != nil {
		return KnowledgeSourceV1{}, nil, KnowledgeSourceRefV1{}, err
	}
	ref := KnowledgeSourceRefV1{
		ID:      frozen.ID,
		Version: frozen.Version,
		Digest:  Digest(KnowledgeSourceDigestDomainV1, canonical),
	}
	return frozen, canonical, ref, nil
}

func RestoreKnowledgeSourceV1(
	canonical []byte,
) (KnowledgeSourceV1, KnowledgeSourceRefV1, error) {
	var decoded KnowledgeSourceV1
	if err := decodeExactKnowledgeWire(canonical, MaxKnowledgeSourceBytesV1, &decoded); err != nil {
		return KnowledgeSourceV1{}, KnowledgeSourceRefV1{}, err
	}
	restored, rebuilt, ref, err := NewKnowledgeSourceV1(decoded)
	if err != nil {
		return KnowledgeSourceV1{}, KnowledgeSourceRefV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return KnowledgeSourceV1{}, KnowledgeSourceRefV1{}, fmt.Errorf("knowledge source is not frozen canonically")
	}
	return restored, ref, nil
}

func ComputeKnowledgeSourceDigestV1(input KnowledgeSourceV1) (string, error) {
	_, _, ref, err := NewKnowledgeSourceV1(input)
	return ref.Digest, err
}

func normalizeKnowledgeContextBindingV1(
	input KnowledgeContextBindingV1,
) (KnowledgeContextBindingV1, error) {
	if input.SchemaVersion != KnowledgeContextBindingSchemaV1 {
		return KnowledgeContextBindingV1{}, fmt.Errorf(
			"knowledge context binding schema_version must be %q",
			KnowledgeContextBindingSchemaV1,
		)
	}
	if err := input.Source.Validate(); err != nil {
		return KnowledgeContextBindingV1{}, fmt.Errorf(
			"knowledge context binding source: %w",
			err,
		)
	}
	if err := validateKnowledgeLimits(
		"knowledge context binding",
		input.MaxHits,
		input.MaxTotalTextBytes,
	); err != nil {
		return KnowledgeContextBindingV1{}, err
	}
	frozen := input
	if input.Routing != nil {
		routing, err := normalizeKnowledgeRoutingPolicyV1(*input.Routing)
		if err != nil {
			return KnowledgeContextBindingV1{}, fmt.Errorf(
				"knowledge context binding routing: %w",
				err,
			)
		}
		frozen.Routing = &routing
	}
	return frozen, nil
}

func normalizeKnowledgeRoutingPolicyV1(
	input KnowledgeRoutingPolicyV1,
) (KnowledgeRoutingPolicyV1, error) {
	if input.SchemaVersion != KnowledgeRoutingPolicySchemaV1 {
		return KnowledgeRoutingPolicyV1{}, fmt.Errorf(
			"knowledge routing policy schema_version must be %q",
			KnowledgeRoutingPolicySchemaV1,
		)
	}
	tags, err := normalizeKnowledgeRoutingStrings(
		"knowledge collection tag",
		input.CollectionTags,
		MaxKnowledgeCollectionTagsV1,
	)
	if err != nil {
		return KnowledgeRoutingPolicyV1{}, err
	}
	if len(tags) == 0 {
		return KnowledgeRoutingPolicyV1{}, fmt.Errorf(
			"knowledge collection tags must not be empty",
		)
	}
	terms, err := normalizeKnowledgeRoutingStrings(
		"knowledge match term",
		input.MatchTerms,
		MaxKnowledgeMatchTermsV1,
	)
	if err != nil {
		return KnowledgeRoutingPolicyV1{}, err
	}
	if len(terms) == 0 {
		return KnowledgeRoutingPolicyV1{}, fmt.Errorf(
			"knowledge match terms must not be empty",
		)
	}
	if input.MinMatchTerms == 0 || int(input.MinMatchTerms) > len(terms) {
		return KnowledgeRoutingPolicyV1{}, fmt.Errorf(
			"knowledge routing policy min_match_terms must be between 1 and %d",
			len(terms),
		)
	}
	frozen := input
	frozen.CollectionTags = tags
	frozen.MatchTerms = terms
	if input.Reuse != nil {
		if err := input.Reuse.Validate(); err != nil {
			return KnowledgeRoutingPolicyV1{}, err
		}
		reuse := *input.Reuse
		frozen.Reuse = &reuse
	}
	return frozen, nil
}

func normalizeKnowledgeRoutingStrings(
	name string,
	input []string,
	maximum int,
) ([]string, error) {
	if len(input) > maximum {
		return nil, fmt.Errorf("%s count exceeds %d", name, maximum)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if err := validateKnowledgeOpaque(
			name,
			value,
			MaxKnowledgeRoutingValueBytesV1,
		); err != nil {
			return nil, fmt.Errorf("%s %d: %w", name, index, err)
		}
		if value != CanonicalText(strings.ToLower(CanonicalText(value))) {
			return nil, fmt.Errorf(
				"%s %d must use lowercase Unicode NFC",
				name,
				index,
			)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf("%ss must be unique", name)
		}
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func normalizeKnowledgeScopeRules(
	input []KnowledgeScopeRuleV1,
) ([]KnowledgeScopeRuleV1, error) {
	if len(input) > MaxManifestEntries {
		return nil, fmt.Errorf("scope rule count exceeds %d", MaxManifestEntries)
	}
	rules := append([]KnowledgeScopeRuleV1(nil), input...)
	for index, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, fmt.Errorf("scope rule %d: %w", index, err)
		}
	}
	sort.Slice(rules, func(left, right int) bool {
		return compareKnowledgeScopeRule(rules[left], rules[right]) < 0
	})
	for index := 1; index < len(rules); index++ {
		if compareKnowledgeScopeRule(rules[index-1], rules[index]) == 0 {
			return nil, fmt.Errorf("scope rules must be unique")
		}
	}
	if rules == nil {
		rules = []KnowledgeScopeRuleV1{}
	}
	return rules, nil
}

func compareKnowledgeScopeRule(left, right KnowledgeScopeRuleV1) int {
	leftParts := [...]string{left.TenantID, left.WorkspaceID, left.AgentID, left.TaskInputRef}
	rightParts := [...]string{right.TenantID, right.WorkspaceID, right.AgentID, right.TaskInputRef}
	for index := range leftParts {
		if comparison := strings.Compare(leftParts[index], rightParts[index]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func knowledgeChunkIdentityKey(chunk KnowledgeChunkV1) string {
	return chunk.Document.ID + "\x00" + chunk.Document.Version + "\x00" +
		chunk.Document.Digest + "\x00" + chunk.ChunkID
}

func validateKnowledgeLimits(name string, maxHits, maxBytes uint32) error {
	if maxHits == 0 || maxHits > MaxKnowledgeHitsV1 {
		return fmt.Errorf("%s max_hits must be between 1 and %d", name, MaxKnowledgeHitsV1)
	}
	if maxBytes == 0 || maxBytes > MaxKnowledgeTotalTextBytesV1 {
		return fmt.Errorf("%s max_total_text_bytes must be between 1 and %d", name, MaxKnowledgeTotalTextBytesV1)
	}
	return nil
}

func validateKnowledgeOpaque(name, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) ||
		value != strings.TrimSpace(value) || value != CanonicalText(value) {
		return fmt.Errorf("%s must be canonical UTF-8 containing between 1 and %d bytes", name, maximum)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func marshalCanonicalKnowledgeWire(value any, maximum int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal knowledge wire: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(encoded, CanonicalJSONLimits{
		MaxBytes: maximum,
		MaxDepth: 128,
		MaxNodes: maximum,
	})
	if err != nil {
		return nil, fmt.Errorf("canonicalize knowledge wire: %w", err)
	}
	return canonical, nil
}

func decodeExactKnowledgeWire(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf("knowledge wire must contain between 1 and %d canonical bytes", maximum)
	}
	checked, err := CanonicalJSONWithLimits(canonical, CanonicalJSONLimits{
		MaxBytes: maximum,
		MaxDepth: 128,
		MaxNodes: maximum,
	})
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("knowledge wire is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode knowledge wire: %w", err)
	}
	return nil
}

func minKnowledgeUint32(left, right uint32) uint32 {
	if left < right {
		return left
	}
	return right
}
