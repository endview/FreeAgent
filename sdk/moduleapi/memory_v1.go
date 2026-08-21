package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MemoryContextBindingSchemaV1   = "memory-context-binding/v1"
	MemoryAuthorityCeilingSchemaV1 = "memory-authority-ceiling/v1"
	MemorySnapshotSchemaV1         = "memory-snapshot/v1"
	MemoryContextRequestSchemaV1   = "memory-context-request/v1"
	MemoryContextOutputSchemaV1    = "memory-context-output/v1"

	MemoryContextBindingDigestDomainV1 = "freeagent.memory-context-binding/v1"
	MemoryEntryDigestDomainV1          = "freeagent.memory-entry/v1"
	MemoryContextRequestDigestDomainV1 = "freeagent.memory-context-request/v1"
	MemoryContextOutputDigestDomainV1  = "freeagent.memory-context-output/v1"

	// Memory is deliberately small state, not a replacement knowledge base.
	MaxMemoryEntriesV1             = 256
	MaxMemoryItemsV1               = 64
	MaxMemoryEntryTextBytesV1      = 4 << 10
	MaxMemoryTotalTextBytesV1      = 64 << 10
	MaxMemorySnapshotBytesV1       = 64 << 10
	MaxMemorySourceRefsV1          = 16
	MaxMemoryCategoryRulesV1       = 64
	MaxMemoryTermsPerCategoryV1    = 64
	MaxMemoryStopTermsV1           = 256
	MaxMemoryTermBytesV1           = 256
	MaxMemoryAlgorithmVersionBytes = 128
	// Four bytes guarantee that an extractive summary can retain at least one
	// valid UTF-8 code point, including supplementary-plane characters.
	MinMemorySummaryTextBytesV1 uint32 = 4

	MaxMemorySafeIntegerV1 uint64 = 1<<53 - 1
	MaxMemoryTTLSecondsV1  uint64 = MaxMemorySafeIntegerV1 / 1000
)

// MemoryEntryKindV1 is the complete first-slice memory taxonomy. Domain
// knowledge remains in RAG and cannot be represented as another entry kind.
type MemoryEntryKindV1 string

const (
	MemoryEntryFact              MemoryEntryKindV1 = "FACT"
	MemoryEntryPreference        MemoryEntryKindV1 = "PREFERENCE"
	MemoryEntryTaskSummary       MemoryEntryKindV1 = "TASK_SUMMARY"
	MemoryEntryCategoryCount     MemoryEntryKindV1 = "CATEGORY_COUNT"
	MemoryEntryRepeatedTermCount MemoryEntryKindV1 = "REPEATED_TERM_COUNT"
)

func (kind MemoryEntryKindV1) Validate() error {
	switch kind {
	case MemoryEntryFact,
		MemoryEntryPreference,
		MemoryEntryTaskSummary,
		MemoryEntryCategoryCount,
		MemoryEntryRepeatedTermCount:
		return nil
	default:
		return fmt.Errorf("unsupported memory entry kind %q", kind)
	}
}

func (kind MemoryEntryKindV1) isText() bool {
	return kind == MemoryEntryFact ||
		kind == MemoryEntryPreference ||
		kind == MemoryEntryTaskSummary
}

func (kind MemoryEntryKindV1) isDerived() bool {
	return kind == MemoryEntryTaskSummary ||
		kind == MemoryEntryCategoryCount ||
		kind == MemoryEntryRepeatedTermCount
}

// MemoryCategoryRuleV1 freezes one deterministic category dictionary. It is
// consumer configuration and carries no authority.
type MemoryCategoryRuleV1 struct {
	Key   string   `json:"key"`
	Terms []string `json:"terms"`
}

func (rule MemoryCategoryRuleV1) Validate() error {
	_, err := normalizeMemoryCategoryRuleV1(rule)
	return err
}

// MemoryContextBindingV1 requests both read limits and the bounded,
// deterministic update algorithm configuration used by Core. The digest of
// this exact inner wire is recorded by derived entries as AlgorithmConfigDigest.
type MemoryContextBindingV1 struct {
	SchemaVersion       string                 `json:"schema_version"`
	Kinds               []MemoryEntryKindV1    `json:"kinds"`
	MaxItems            uint32                 `json:"max_items"`
	MaxTotalTextBytes   uint32                 `json:"max_total_text_bytes"`
	CategoryRules       []MemoryCategoryRuleV1 `json:"category_rules"`
	StopTerms           []string               `json:"stop_terms"`
	SummaryMaxTextBytes uint32                 `json:"summary_max_text_bytes"`
	EntryTTLSeconds     uint64                 `json:"entry_ttl_seconds"`
}

func (binding MemoryContextBindingV1) Validate() error {
	_, _, _, err := newMemoryContextBindingV1(binding)
	return err
}

func NewMemoryContextBindingV1(
	input MemoryContextBindingV1,
) (MemoryContextBindingV1, []byte, string, error) {
	return newMemoryContextBindingV1(input)
}

func newMemoryContextBindingV1(
	input MemoryContextBindingV1,
) (MemoryContextBindingV1, []byte, string, error) {
	if input.SchemaVersion != MemoryContextBindingSchemaV1 {
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf(
			"memory context binding schema_version must be %q",
			MemoryContextBindingSchemaV1,
		)
	}
	kinds, err := normalizeMemoryKinds(input.Kinds, false)
	if err != nil {
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding kinds: %w", err)
	}
	if err := validateMemoryLimits("memory context binding", input.MaxItems, input.MaxTotalTextBytes); err != nil {
		return MemoryContextBindingV1{}, nil, "", err
	}
	rules, err := normalizeMemoryCategoryRules(input.CategoryRules)
	if err != nil {
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding category_rules: %w", err)
	}
	stopTerms, err := normalizeMemoryTextSet("memory stop term", input.StopTerms, MaxMemoryStopTermsV1)
	if err != nil {
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding stop_terms: %w", err)
	}

	hasSummary := memoryKindsContain(kinds, MemoryEntryTaskSummary)
	hasCategory := memoryKindsContain(kinds, MemoryEntryCategoryCount)
	hasRepeatedTerms := memoryKindsContain(kinds, MemoryEntryRepeatedTermCount)
	hasDerived := hasSummary || hasCategory || hasRepeatedTerms
	switch {
	case hasSummary && (input.SummaryMaxTextBytes < MinMemorySummaryTextBytesV1 || input.SummaryMaxTextBytes > MaxMemoryEntryTextBytesV1 || input.SummaryMaxTextBytes > input.MaxTotalTextBytes):
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf(
			"memory context binding summary_max_text_bytes must be between %d and min(%d,max_total_text_bytes)",
			MinMemorySummaryTextBytesV1,
			MaxMemoryEntryTextBytesV1,
		)
	case !hasSummary && input.SummaryMaxTextBytes != 0:
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding summary_max_text_bytes requires TASK_SUMMARY")
	case hasCategory && len(rules) == 0:
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding CATEGORY_COUNT requires category_rules")
	case !hasCategory && len(rules) != 0:
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding category_rules require CATEGORY_COUNT")
	case !hasCategory && !hasRepeatedTerms && len(stopTerms) != 0:
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding stop_terms require a count kind")
	case hasDerived && (input.EntryTTLSeconds == 0 || input.EntryTTLSeconds > MaxMemoryTTLSecondsV1):
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding entry_ttl_seconds must be between 1 and %d for derived entries", MaxMemoryTTLSecondsV1)
	case !hasDerived && input.EntryTTLSeconds != 0:
		return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory context binding entry_ttl_seconds requires a derived kind")
	}
	for _, rule := range rules {
		for _, term := range rule.Terms {
			if memorySortedStringsContain(stopTerms, term) {
				return MemoryContextBindingV1{}, nil, "", fmt.Errorf("memory category term %q is also a stop term", term)
			}
		}
	}

	frozen := input
	frozen.Kinds = kinds
	frozen.CategoryRules = rules
	frozen.StopTerms = stopTerms
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxConfigBytes)
	if err != nil {
		return MemoryContextBindingV1{}, nil, "", err
	}
	return frozen, canonical, Digest(MemoryContextBindingDigestDomainV1, canonical), nil
}

func RestoreMemoryContextBindingV1(
	canonical []byte,
) (MemoryContextBindingV1, string, error) {
	var decoded MemoryContextBindingV1
	if err := decodeExactMemoryWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return MemoryContextBindingV1{}, "", err
	}
	restored, rebuilt, digest, err := NewMemoryContextBindingV1(decoded)
	if err != nil {
		return MemoryContextBindingV1{}, "", err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return MemoryContextBindingV1{}, "", fmt.Errorf("memory context binding is not frozen canonically")
	}
	return restored, digest, nil
}

func ComputeMemoryContextBindingDigestV1(input MemoryContextBindingV1) (string, error) {
	_, _, digest, err := NewMemoryContextBindingV1(input)
	return digest, err
}

func RestoreMemoryContextBindingParametersV1(
	config ContextBindingConfigV1,
) (MemoryContextBindingV1, string, error) {
	if config.SchemaVersion != ContextBindingConfigSchemaV1 ||
		config.Placement != ContextPlacementUntrustedData ||
		config.AllowSummary || config.AllowDrop {
		return MemoryContextBindingV1{}, "", fmt.Errorf("dynamic memory context binding must be UNTRUSTED_DATA and non-retainable")
	}
	return RestoreMemoryContextBindingV1(config.Parameters)
}

// MemoryObjectRefV1 deliberately has the same dependency-light shape as the
// public Knowledge scope object reference.
type MemoryObjectRefV1 = KnowledgeObjectRefV1

type MemoryQueryScopeV1 struct {
	TenantID     string            `json:"tenant_id"`
	Workspace    MemoryObjectRefV1 `json:"workspace"`
	Agent        MemoryObjectRefV1 `json:"agent"`
	TaskInputRef string            `json:"task_input_ref"`
}

func (scope MemoryQueryScopeV1) Validate() error {
	if scope.TenantID == "*" {
		return fmt.Errorf("memory query tenant_id must be exact")
	}
	if err := validateMemoryOpaque("memory query tenant_id", scope.TenantID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if err := scope.Workspace.Validate(); err != nil {
		return fmt.Errorf("memory query workspace: %w", err)
	}
	if scope.Workspace.ID == "*" {
		return fmt.Errorf("memory query workspace ID must be exact")
	}
	if err := scope.Agent.Validate(); err != nil {
		return fmt.Errorf("memory query agent: %w", err)
	}
	if scope.Agent.ID == "*" {
		return fmt.Errorf("memory query agent ID must be exact")
	}
	if !ValidSHA256(scope.TaskInputRef) {
		return fmt.Errorf("memory query task_input_ref must be an exact digest")
	}
	return nil
}

// MemorySnapshotRefV1 closes an immutable snapshot selected by Current Store.
// Digest is the MEMORY_SNAPSHOT/application-json ContentRecord digest, not a
// second SDK-specific semantic digest.
type MemorySnapshotRefV1 struct {
	TenantID string `json:"tenant_id"`
	AgentID  string `json:"agent_id"`
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

func (ref MemorySnapshotRefV1) Validate() error {
	if ref.TenantID == "*" {
		return fmt.Errorf("memory snapshot tenant_id must be exact")
	}
	if err := validateMemoryOpaque("memory snapshot tenant_id", ref.TenantID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if ref.AgentID == "*" {
		return fmt.Errorf("memory snapshot agent_id must be exact")
	}
	if err := validateMemoryOpaque("memory snapshot agent_id", ref.AgentID, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if ref.Revision == 0 || ref.Revision > MaxMemorySafeIntegerV1 {
		return fmt.Errorf("memory snapshot revision must be between 1 and %d", MaxMemorySafeIntegerV1)
	}
	if !ValidSHA256(ref.Digest) {
		return fmt.Errorf("memory snapshot digest must be a lowercase SHA-256 content digest")
	}
	return nil
}

// MemoryAuthorityCeilingV1 is Core-owned. Algorithm fields are intentionally
// absent: authority can only reject kinds/scopes or lower read limits and can
// never rewrite the frozen deterministic script configuration.
type MemoryAuthorityCeilingV1 struct {
	SchemaVersion       string              `json:"schema_version"`
	TenantID            string              `json:"tenant_id"`
	AgentID             string              `json:"agent_id"`
	AllowedWorkspaceIDs []string            `json:"allowed_workspace_ids"`
	AllowedKinds        []MemoryEntryKindV1 `json:"allowed_kinds"`
	MaxItems            uint32              `json:"max_items"`
	MaxTotalTextBytes   uint32              `json:"max_total_text_bytes"`
}

func (ceiling MemoryAuthorityCeilingV1) Validate() error {
	_, _, err := NewMemoryAuthorityCeilingV1(ceiling)
	return err
}

func NewMemoryAuthorityCeilingV1(
	input MemoryAuthorityCeilingV1,
) (MemoryAuthorityCeilingV1, []byte, error) {
	if input.SchemaVersion != MemoryAuthorityCeilingSchemaV1 {
		return MemoryAuthorityCeilingV1{}, nil, fmt.Errorf(
			"memory authority ceiling schema_version must be %q",
			MemoryAuthorityCeilingSchemaV1,
		)
	}
	if input.TenantID == "*" {
		return MemoryAuthorityCeilingV1{}, nil, fmt.Errorf("memory authority ceiling tenant_id must be exact")
	}
	if err := validateMemoryOpaque("memory authority ceiling tenant_id", input.TenantID, MaxOpaqueIDBytes); err != nil {
		return MemoryAuthorityCeilingV1{}, nil, err
	}
	if input.AgentID == "*" {
		return MemoryAuthorityCeilingV1{}, nil, fmt.Errorf("memory authority ceiling agent_id must be exact")
	}
	if err := validateMemoryOpaque("memory authority ceiling agent_id", input.AgentID, MaxOpaqueIDBytes); err != nil {
		return MemoryAuthorityCeilingV1{}, nil, err
	}
	workspaceIDs, err := normalizeMemoryWorkspaceIDs(input.AllowedWorkspaceIDs)
	if err != nil {
		return MemoryAuthorityCeilingV1{}, nil, fmt.Errorf("memory authority ceiling allowed_workspace_ids: %w", err)
	}
	kinds, err := normalizeMemoryKinds(input.AllowedKinds, false)
	if err != nil {
		return MemoryAuthorityCeilingV1{}, nil, fmt.Errorf("memory authority ceiling allowed_kinds: %w", err)
	}
	if err := validateMemoryLimits("memory authority ceiling", input.MaxItems, input.MaxTotalTextBytes); err != nil {
		return MemoryAuthorityCeilingV1{}, nil, err
	}
	frozen := input
	frozen.AllowedWorkspaceIDs = workspaceIDs
	frozen.AllowedKinds = kinds
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxConfigBytes)
	if err != nil {
		return MemoryAuthorityCeilingV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreMemoryAuthorityCeilingV1(canonical []byte) (MemoryAuthorityCeilingV1, error) {
	var decoded MemoryAuthorityCeilingV1
	if err := decodeExactMemoryWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return MemoryAuthorityCeilingV1{}, err
	}
	restored, rebuilt, err := NewMemoryAuthorityCeilingV1(decoded)
	if err != nil {
		return MemoryAuthorityCeilingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return MemoryAuthorityCeilingV1{}, fmt.Errorf("memory authority ceiling is not frozen canonically")
	}
	return restored, nil
}

type MemoryResolvedAuthorityV1 struct {
	Kinds             []MemoryEntryKindV1
	MaxItems          uint32
	MaxTotalTextBytes uint32
}

// ResolveMemoryAuthorityV1 proves exact owner/scope authority, verifies that
// every requested kind is authorized, and returns only narrowed limits.
func ResolveMemoryAuthorityV1(
	binding MemoryContextBindingV1,
	ceiling MemoryAuthorityCeilingV1,
	snapshot MemorySnapshotRefV1,
	scope MemoryQueryScopeV1,
) (MemoryResolvedAuthorityV1, error) {
	frozenBinding, _, _, err := NewMemoryContextBindingV1(binding)
	if err != nil {
		return MemoryResolvedAuthorityV1{}, err
	}
	frozenCeiling, _, err := NewMemoryAuthorityCeilingV1(ceiling)
	if err != nil {
		return MemoryResolvedAuthorityV1{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return MemoryResolvedAuthorityV1{}, err
	}
	if err := scope.Validate(); err != nil {
		return MemoryResolvedAuthorityV1{}, err
	}
	if snapshot.TenantID != scope.TenantID || snapshot.AgentID != scope.Agent.ID {
		return MemoryResolvedAuthorityV1{}, fmt.Errorf("memory snapshot owner differs from exact query scope")
	}
	if frozenCeiling.TenantID != scope.TenantID || frozenCeiling.AgentID != scope.Agent.ID {
		return MemoryResolvedAuthorityV1{}, fmt.Errorf("memory authority owner differs from exact query scope")
	}
	if !memoryWorkspaceAllowed(frozenCeiling.AllowedWorkspaceIDs, scope.Workspace.ID) {
		return MemoryResolvedAuthorityV1{}, fmt.Errorf("memory query workspace is not allowed by authority ceiling")
	}
	for _, kind := range frozenBinding.Kinds {
		if !memoryKindsContain(frozenCeiling.AllowedKinds, kind) {
			return MemoryResolvedAuthorityV1{}, fmt.Errorf("memory binding requests unauthorized kind %q", kind)
		}
	}
	return MemoryResolvedAuthorityV1{
		Kinds:             append([]MemoryEntryKindV1(nil), frozenBinding.Kinds...),
		MaxItems:          minMemoryUint32(frozenBinding.MaxItems, frozenCeiling.MaxItems),
		MaxTotalTextBytes: minMemoryUint32(frozenBinding.MaxTotalTextBytes, frozenCeiling.MaxTotalTextBytes),
	}, nil
}

// MemoryEntryV1 is one immutable bounded item. EntryDigest covers every field
// except itself. SourceRefs are exact Task/Result/seed/confirmation content
// digests and VisibleWorkspaceIDs are exact IDs or the sole wildcard.
type MemoryEntryV1 struct {
	EntryID               string            `json:"entry_id"`
	Kind                  MemoryEntryKindV1 `json:"kind"`
	Key                   string            `json:"key"`
	Text                  string            `json:"text,omitempty"`
	Count                 uint64            `json:"count,omitempty"`
	VisibleWorkspaceIDs   []string          `json:"visible_workspace_ids"`
	SourceRefs            []string          `json:"source_refs"`
	AlgorithmVersion      string            `json:"algorithm_version,omitempty"`
	AlgorithmConfigDigest string            `json:"algorithm_config_digest,omitempty"`
	CreatedAtUnixMS       uint64            `json:"created_at_unix_ms"`
	ExpiresAtUnixMS       uint64            `json:"expires_at_unix_ms,omitempty"`
	EntryDigest           string            `json:"entry_digest"`
}

type memoryEntryDigestWireV1 struct {
	EntryID               string            `json:"entry_id"`
	Kind                  MemoryEntryKindV1 `json:"kind"`
	Key                   string            `json:"key"`
	Text                  string            `json:"text,omitempty"`
	Count                 uint64            `json:"count,omitempty"`
	VisibleWorkspaceIDs   []string          `json:"visible_workspace_ids"`
	SourceRefs            []string          `json:"source_refs"`
	AlgorithmVersion      string            `json:"algorithm_version,omitempty"`
	AlgorithmConfigDigest string            `json:"algorithm_config_digest,omitempty"`
	CreatedAtUnixMS       uint64            `json:"created_at_unix_ms"`
	ExpiresAtUnixMS       uint64            `json:"expires_at_unix_ms,omitempty"`
}

func (entry MemoryEntryV1) Validate() error {
	_, _, err := NewMemoryEntryV1(entry)
	return err
}

func NewMemoryEntryV1(input MemoryEntryV1) (MemoryEntryV1, []byte, error) {
	frozen, digest, err := normalizeMemoryEntryV1(input)
	if err != nil {
		return MemoryEntryV1{}, nil, err
	}
	if input.EntryDigest != "" && input.EntryDigest != digest {
		return MemoryEntryV1{}, nil, fmt.Errorf("memory entry digest does not match canonical content")
	}
	frozen.EntryDigest = digest
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxMemorySnapshotBytesV1)
	if err != nil {
		return MemoryEntryV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreMemoryEntryV1(canonical []byte) (MemoryEntryV1, error) {
	var decoded MemoryEntryV1
	if err := decodeExactMemoryWire(canonical, MaxMemorySnapshotBytesV1, &decoded); err != nil {
		return MemoryEntryV1{}, err
	}
	restored, rebuilt, err := NewMemoryEntryV1(decoded)
	if err != nil {
		return MemoryEntryV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return MemoryEntryV1{}, fmt.Errorf("memory entry is not frozen canonically")
	}
	return restored, nil
}

func ComputeMemoryEntryDigestV1(input MemoryEntryV1) (string, error) {
	_, digest, err := normalizeMemoryEntryV1(input)
	return digest, err
}

func normalizeMemoryEntryV1(input MemoryEntryV1) (MemoryEntryV1, string, error) {
	if err := validateMemoryOpaque("memory entry ID", input.EntryID, MaxOpaqueIDBytes); err != nil {
		return MemoryEntryV1{}, "", err
	}
	if err := input.Kind.Validate(); err != nil {
		return MemoryEntryV1{}, "", err
	}
	if err := validateMemoryOpaque("memory entry key", input.Key, MaxOpaqueIDBytes); err != nil {
		return MemoryEntryV1{}, "", err
	}
	if input.CreatedAtUnixMS == 0 || input.CreatedAtUnixMS > MaxMemorySafeIntegerV1 {
		return MemoryEntryV1{}, "", fmt.Errorf("memory entry created_at_unix_ms must be between 1 and %d", MaxMemorySafeIntegerV1)
	}
	if input.ExpiresAtUnixMS > MaxMemorySafeIntegerV1 {
		return MemoryEntryV1{}, "", fmt.Errorf("memory entry expires_at_unix_ms exceeds %d", MaxMemorySafeIntegerV1)
	}
	if input.ExpiresAtUnixMS != 0 && input.ExpiresAtUnixMS <= input.CreatedAtUnixMS {
		return MemoryEntryV1{}, "", fmt.Errorf("memory entry expiry must be later than creation")
	}
	if input.Kind.isText() {
		if input.Count != 0 {
			return MemoryEntryV1{}, "", fmt.Errorf("text memory entry must not contain count")
		}
		if err := validateMemoryText("memory entry text", input.Text, MaxMemoryEntryTextBytesV1, false); err != nil {
			return MemoryEntryV1{}, "", err
		}
	} else {
		if input.Text != "" {
			return MemoryEntryV1{}, "", fmt.Errorf("count memory entry must not contain text")
		}
		if input.Count == 0 || input.Count > MaxMemorySafeIntegerV1 {
			return MemoryEntryV1{}, "", fmt.Errorf("count memory entry count must be between 1 and %d", MaxMemorySafeIntegerV1)
		}
	}

	if input.Kind.isDerived() {
		if err := validateMemoryOpaque(
			"memory entry algorithm_version",
			input.AlgorithmVersion,
			MaxMemoryAlgorithmVersionBytes,
		); err != nil {
			return MemoryEntryV1{}, "", err
		}
		if !ValidSHA256(input.AlgorithmConfigDigest) {
			return MemoryEntryV1{}, "", fmt.Errorf("derived memory entry algorithm_config_digest must be lowercase SHA-256")
		}
		if input.ExpiresAtUnixMS == 0 {
			return MemoryEntryV1{}, "", fmt.Errorf("derived memory entry must have an expiry")
		}
	} else if input.AlgorithmVersion != "" || input.AlgorithmConfigDigest != "" {
		return MemoryEntryV1{}, "", fmt.Errorf("seed or confirmed memory entry must not contain algorithm fields")
	}

	workspaceIDs, err := normalizeMemoryWorkspaceIDs(input.VisibleWorkspaceIDs)
	if err != nil {
		return MemoryEntryV1{}, "", fmt.Errorf("memory entry visible_workspace_ids: %w", err)
	}
	if input.Kind.isDerived() && (len(workspaceIDs) != 1 || workspaceIDs[0] == "*") {
		return MemoryEntryV1{}, "", fmt.Errorf("derived memory entry must be visible to exactly one workspace")
	}
	sourceRefs, err := normalizeMemoryDigestSet("memory source ref", input.SourceRefs, MaxMemorySourceRefsV1)
	if err != nil {
		return MemoryEntryV1{}, "", fmt.Errorf("memory entry source_refs: %w", err)
	}
	frozen := input
	frozen.VisibleWorkspaceIDs = workspaceIDs
	frozen.SourceRefs = sourceRefs
	digestWire := memoryEntryDigestWireV1{
		EntryID:               frozen.EntryID,
		Kind:                  frozen.Kind,
		Key:                   frozen.Key,
		Text:                  frozen.Text,
		Count:                 frozen.Count,
		VisibleWorkspaceIDs:   frozen.VisibleWorkspaceIDs,
		SourceRefs:            frozen.SourceRefs,
		AlgorithmVersion:      frozen.AlgorithmVersion,
		AlgorithmConfigDigest: frozen.AlgorithmConfigDigest,
		CreatedAtUnixMS:       frozen.CreatedAtUnixMS,
		ExpiresAtUnixMS:       frozen.ExpiresAtUnixMS,
	}
	canonical, err := marshalCanonicalMemoryWire(digestWire, MaxMemorySnapshotBytesV1)
	if err != nil {
		return MemoryEntryV1{}, "", err
	}
	return frozen, Digest(MemoryEntryDigestDomainV1, canonical), nil
}

// AgentMemorySnapshotV1 is the immutable semantic body stored by Core as a
// MEMORY_SNAPSHOT ContentRecord. PreviousSnapshotDigest refers to the parent
// ContentRecord digest. SourceAttemptID is provenance for an automatic
// successful-terminal update and is never projected into model context.
type AgentMemorySnapshotV1 struct {
	SchemaVersion          string          `json:"schema_version"`
	TenantID               string          `json:"tenant_id"`
	AgentID                string          `json:"agent_id"`
	Revision               uint64          `json:"revision"`
	PreviousSnapshotDigest string          `json:"previous_snapshot_digest,omitempty"`
	SourceAttemptID        string          `json:"source_attempt_id,omitempty"`
	Entries                []MemoryEntryV1 `json:"entries"`
}

func (snapshot AgentMemorySnapshotV1) Validate() error {
	_, _, err := NewAgentMemorySnapshotV1(snapshot)
	return err
}

func NewAgentMemorySnapshotV1(
	input AgentMemorySnapshotV1,
) (AgentMemorySnapshotV1, []byte, error) {
	if input.SchemaVersion != MemorySnapshotSchemaV1 {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot schema_version must be %q", MemorySnapshotSchemaV1)
	}
	if input.TenantID == "*" {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot tenant_id must be exact")
	}
	if err := validateMemoryOpaque("memory snapshot tenant_id", input.TenantID, MaxOpaqueIDBytes); err != nil {
		return AgentMemorySnapshotV1{}, nil, err
	}
	if input.AgentID == "*" {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot agent_id must be exact")
	}
	if err := validateMemoryOpaque("memory snapshot agent_id", input.AgentID, MaxOpaqueIDBytes); err != nil {
		return AgentMemorySnapshotV1{}, nil, err
	}
	if input.Revision == 0 || input.Revision > MaxMemorySafeIntegerV1 {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot revision must be between 1 and %d", MaxMemorySafeIntegerV1)
	}
	if input.Revision == 1 {
		if input.PreviousSnapshotDigest != "" {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory genesis snapshot must not have previous_snapshot_digest")
		}
		if input.SourceAttemptID != "" {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory genesis snapshot must not have source_attempt_id")
		}
	} else if !ValidSHA256(input.PreviousSnapshotDigest) {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory non-genesis snapshot must have a lowercase SHA-256 parent content digest")
	} else if input.SourceAttemptID == "" {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory non-genesis snapshot must have source_attempt_id")
	}
	if input.SourceAttemptID != "" {
		if err := validateMemoryOpaque("memory snapshot source_attempt_id", input.SourceAttemptID, MaxOpaqueIDBytes); err != nil {
			return AgentMemorySnapshotV1{}, nil, err
		}
	}
	if len(input.Entries) > MaxMemoryEntriesV1 {
		return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot exceeds %d entries", MaxMemoryEntriesV1)
	}

	frozen := input
	frozen.Entries = make([]MemoryEntryV1, len(input.Entries))
	for index, entry := range input.Entries {
		normalized, _, err := NewMemoryEntryV1(entry)
		if err != nil {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot entry %d: %w", index, err)
		}
		frozen.Entries[index] = normalized
	}
	sort.Slice(frozen.Entries, func(left, right int) bool {
		return compareMemoryEntry(frozen.Entries[left], frozen.Entries[right]) < 0
	})
	seenIDs := make(map[string]struct{}, len(frozen.Entries))
	seenDigests := make(map[string]struct{}, len(frozen.Entries))
	for index, entry := range frozen.Entries {
		if index > 0 && memoryEntryIdentityKey(frozen.Entries[index-1]) == memoryEntryIdentityKey(entry) {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot repeats kind/key/visibility identity")
		}
		if _, exists := seenIDs[entry.EntryID]; exists {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot repeats entry_id")
		}
		if _, exists := seenDigests[entry.EntryDigest]; exists {
			return AgentMemorySnapshotV1{}, nil, fmt.Errorf("memory snapshot repeats entry digest")
		}
		seenIDs[entry.EntryID] = struct{}{}
		seenDigests[entry.EntryDigest] = struct{}{}
	}
	if frozen.Entries == nil {
		frozen.Entries = []MemoryEntryV1{}
	}
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxMemorySnapshotBytesV1)
	if err != nil {
		return AgentMemorySnapshotV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreAgentMemorySnapshotV1(canonical []byte) (AgentMemorySnapshotV1, error) {
	var decoded AgentMemorySnapshotV1
	if err := decodeExactMemoryWire(canonical, MaxMemorySnapshotBytesV1, &decoded); err != nil {
		return AgentMemorySnapshotV1{}, err
	}
	restored, rebuilt, err := NewAgentMemorySnapshotV1(decoded)
	if err != nil {
		return AgentMemorySnapshotV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return AgentMemorySnapshotV1{}, fmt.Errorf("memory snapshot is not frozen canonically")
	}
	return restored, nil
}

// MemoryCandidateV1 is the only entry material exposed to the local selector.
// ACL, provenance, timestamps and owner metadata stay in Core evidence.
type MemoryCandidateV1 struct {
	EntryDigest string            `json:"entry_digest"`
	Kind        MemoryEntryKindV1 `json:"kind"`
	Key         string            `json:"key"`
	Text        string            `json:"text,omitempty"`
	Count       uint64            `json:"count,omitempty"`
}

func (candidate MemoryCandidateV1) Validate() error {
	if !ValidSHA256(candidate.EntryDigest) {
		return fmt.Errorf("memory candidate entry_digest must be lowercase SHA-256")
	}
	if err := candidate.Kind.Validate(); err != nil {
		return err
	}
	if err := validateMemoryOpaque("memory candidate key", candidate.Key, MaxOpaqueIDBytes); err != nil {
		return err
	}
	if candidate.Kind.isText() {
		if candidate.Count != 0 {
			return fmt.Errorf("text memory candidate must not contain count")
		}
		return validateMemoryText("memory candidate text", candidate.Text, MaxMemoryEntryTextBytesV1, false)
	}
	if candidate.Text != "" {
		return fmt.Errorf("count memory candidate must not contain text")
	}
	if candidate.Count == 0 || candidate.Count > MaxMemorySafeIntegerV1 {
		return fmt.Errorf("count memory candidate count must be between 1 and %d", MaxMemorySafeIntegerV1)
	}
	return nil
}

// NewMemoryCandidateV1 projects an already validated immutable entry without
// exposing ACL, provenance or timing metadata. Core remains responsible for
// scope, expiry, Config and Authority filtering before this call.
func NewMemoryCandidateV1(entry MemoryEntryV1) (MemoryCandidateV1, error) {
	frozen, _, err := NewMemoryEntryV1(entry)
	if err != nil {
		return MemoryCandidateV1{}, err
	}
	return MemoryCandidateV1{
		EntryDigest: frozen.EntryDigest,
		Kind:        frozen.Kind,
		Key:         frozen.Key,
		Text:        frozen.Text,
		Count:       frozen.Count,
	}, nil
}

// MemoryEntryVisibleToWorkspaceV1 is a deny-by-default exact visibility
// check. Invalid entries and workspace IDs are never considered visible.
func MemoryEntryVisibleToWorkspaceV1(entry MemoryEntryV1, workspaceID string) bool {
	frozen, _, err := NewMemoryEntryV1(entry)
	if err != nil || workspaceID == "*" ||
		validateMemoryOpaque("memory workspace ID", workspaceID, MaxOpaqueIDBytes) != nil {
		return false
	}
	return memoryWorkspaceAllowed(frozen.VisibleWorkspaceIDs, workspaceID)
}

// MemoryContextRequestV1 is the exact context.provide/v1 dynamic input. Its
// candidate set is canonical semantic order; Provider output alone supplies
// rank order.
type MemoryContextRequestV1 struct {
	SchemaVersion     string              `json:"schema_version"`
	Snapshot          MemorySnapshotRefV1 `json:"snapshot"`
	Scope             MemoryQueryScopeV1  `json:"scope"`
	QueryText         string              `json:"query_text"`
	EvaluatedAtUnixMS uint64              `json:"evaluated_at_unix_ms"`
	Candidates        []MemoryCandidateV1 `json:"candidates"`
	MaxItems          uint32              `json:"max_items"`
	MaxTotalTextBytes uint32              `json:"max_total_text_bytes"`
}

func (request MemoryContextRequestV1) Validate() error {
	_, _, _, err := NewMemoryContextRequestV1(request)
	return err
}

func NewMemoryContextRequestV1(
	input MemoryContextRequestV1,
) (MemoryContextRequestV1, []byte, string, error) {
	if input.SchemaVersion != MemoryContextRequestSchemaV1 {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request schema_version must be %q", MemoryContextRequestSchemaV1)
	}
	if err := input.Snapshot.Validate(); err != nil {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request snapshot: %w", err)
	}
	if err := input.Scope.Validate(); err != nil {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request scope: %w", err)
	}
	if input.Snapshot.TenantID != input.Scope.TenantID || input.Snapshot.AgentID != input.Scope.Agent.ID {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request snapshot owner differs from exact scope")
	}
	if err := validateMemoryText("memory query text", input.QueryText, MaxTextBytes, false); err != nil {
		return MemoryContextRequestV1{}, nil, "", err
	}
	if input.EvaluatedAtUnixMS == 0 || input.EvaluatedAtUnixMS > MaxMemorySafeIntegerV1 {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request evaluated_at_unix_ms must be between 1 and %d", MaxMemorySafeIntegerV1)
	}
	if err := validateMemoryLimits("memory context request", input.MaxItems, input.MaxTotalTextBytes); err != nil {
		return MemoryContextRequestV1{}, nil, "", err
	}
	if len(input.Candidates) > MaxMemoryEntriesV1 {
		return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request exceeds %d candidates", MaxMemoryEntriesV1)
	}
	frozen := input
	frozen.Candidates = append([]MemoryCandidateV1(nil), input.Candidates...)
	for index, candidate := range frozen.Candidates {
		if err := candidate.Validate(); err != nil {
			return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory candidate %d: %w", index, err)
		}
	}
	sort.Slice(frozen.Candidates, func(left, right int) bool {
		return compareMemoryCandidate(frozen.Candidates[left], frozen.Candidates[right]) < 0
	})
	seenDigests := make(map[string]struct{}, len(frozen.Candidates))
	totalTextBytes := 0
	for _, candidate := range frozen.Candidates {
		if _, exists := seenDigests[candidate.EntryDigest]; exists {
			return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request repeats candidate digest")
		}
		seenDigests[candidate.EntryDigest] = struct{}{}
		totalTextBytes += len(candidate.Text)
		if totalTextBytes > MaxMemoryTotalTextBytesV1 {
			return MemoryContextRequestV1{}, nil, "", fmt.Errorf("memory context request candidates exceed %d text bytes", MaxMemoryTotalTextBytesV1)
		}
	}
	if frozen.Candidates == nil {
		frozen.Candidates = []MemoryCandidateV1{}
	}
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxTextBytes)
	if err != nil {
		return MemoryContextRequestV1{}, nil, "", err
	}
	return frozen, canonical, Digest(MemoryContextRequestDigestDomainV1, canonical), nil
}

func RestoreMemoryContextRequestV1(
	canonical []byte,
) (MemoryContextRequestV1, string, error) {
	var decoded MemoryContextRequestV1
	if err := decodeExactMemoryWire(canonical, MaxTextBytes, &decoded); err != nil {
		return MemoryContextRequestV1{}, "", err
	}
	restored, rebuilt, digest, err := NewMemoryContextRequestV1(decoded)
	if err != nil {
		return MemoryContextRequestV1{}, "", err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return MemoryContextRequestV1{}, "", fmt.Errorf("memory context request is not frozen canonically")
	}
	return restored, digest, nil
}

func ComputeMemoryContextRequestDigestV1(input MemoryContextRequestV1) (string, error) {
	_, _, digest, err := NewMemoryContextRequestV1(input)
	return digest, err
}

// MemoryContextOutputV1 contains only ranked selections from the exact
// request. An empty array is a successful zero-selection result.
type MemoryContextOutputV1 struct {
	SchemaVersion        string              `json:"schema_version"`
	RequestDigest        string              `json:"request_digest"`
	Snapshot             MemorySnapshotRefV1 `json:"snapshot"`
	SelectedEntryDigests []string            `json:"selected_entry_digests"`
}

func (output MemoryContextOutputV1) Validate() error {
	_, _, _, err := NewMemoryContextOutputV1(output)
	return err
}

func NewMemoryContextOutputV1(
	input MemoryContextOutputV1,
) (MemoryContextOutputV1, []byte, string, error) {
	if input.SchemaVersion != MemoryContextOutputSchemaV1 {
		return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output schema_version must be %q", MemoryContextOutputSchemaV1)
	}
	if !ValidSHA256(input.RequestDigest) {
		return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output request_digest must be lowercase SHA-256")
	}
	if err := input.Snapshot.Validate(); err != nil {
		return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output snapshot: %w", err)
	}
	if len(input.SelectedEntryDigests) > MaxMemoryItemsV1 {
		return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output exceeds %d selected entries", MaxMemoryItemsV1)
	}
	frozen := input
	frozen.SelectedEntryDigests = append([]string(nil), input.SelectedEntryDigests...)
	seen := make(map[string]struct{}, len(frozen.SelectedEntryDigests))
	for index, digest := range frozen.SelectedEntryDigests {
		if !ValidSHA256(digest) {
			return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output selected digest %d must be lowercase SHA-256", index)
		}
		if _, exists := seen[digest]; exists {
			return MemoryContextOutputV1{}, nil, "", fmt.Errorf("memory context output repeats selected entry digest")
		}
		seen[digest] = struct{}{}
	}
	if frozen.SelectedEntryDigests == nil {
		frozen.SelectedEntryDigests = []string{}
	}
	canonical, err := marshalCanonicalMemoryWire(frozen, MaxConfigBytes)
	if err != nil {
		return MemoryContextOutputV1{}, nil, "", err
	}
	return frozen, canonical, Digest(MemoryContextOutputDigestDomainV1, canonical), nil
}

func RestoreMemoryContextOutputV1(
	canonical []byte,
) (MemoryContextOutputV1, string, error) {
	var decoded MemoryContextOutputV1
	if err := decodeExactMemoryWire(canonical, MaxConfigBytes, &decoded); err != nil {
		return MemoryContextOutputV1{}, "", err
	}
	restored, rebuilt, digest, err := NewMemoryContextOutputV1(decoded)
	if err != nil {
		return MemoryContextOutputV1{}, "", err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return MemoryContextOutputV1{}, "", fmt.Errorf("memory context output is not frozen canonically")
	}
	return restored, digest, nil
}

func ComputeMemoryContextOutputDigestV1(input MemoryContextOutputV1) (string, error) {
	_, _, digest, err := NewMemoryContextOutputV1(input)
	return digest, err
}

// ValidateMemoryContextOutputForRequestV1 closes Provider output against the
// exact request, candidate membership, ranked uniqueness and lower limits.
func ValidateMemoryContextOutputForRequestV1(
	request MemoryContextRequestV1,
	output MemoryContextOutputV1,
) error {
	frozenRequest, _, requestDigest, err := NewMemoryContextRequestV1(request)
	if err != nil {
		return err
	}
	frozenOutput, _, _, err := NewMemoryContextOutputV1(output)
	if err != nil {
		return err
	}
	if frozenOutput.RequestDigest != requestDigest {
		return fmt.Errorf("memory output does not close the exact request digest")
	}
	if frozenOutput.Snapshot != frozenRequest.Snapshot {
		return fmt.Errorf("memory output snapshot differs from request snapshot")
	}
	if uint32(len(frozenOutput.SelectedEntryDigests)) > frozenRequest.MaxItems {
		return fmt.Errorf("memory output exceeds request item limit")
	}
	candidates := make(map[string]MemoryCandidateV1, len(frozenRequest.Candidates))
	for _, candidate := range frozenRequest.Candidates {
		candidates[candidate.EntryDigest] = candidate
	}
	totalTextBytes := uint32(0)
	for index, digest := range frozenOutput.SelectedEntryDigests {
		candidate, exists := candidates[digest]
		if !exists {
			return fmt.Errorf("memory output selected digest %d is not a request candidate", index)
		}
		totalTextBytes += uint32(len(candidate.Text))
		if totalTextBytes > frozenRequest.MaxTotalTextBytes {
			return fmt.Errorf("memory output exceeds request text limit")
		}
	}
	return nil
}

func normalizeMemoryCategoryRuleV1(input MemoryCategoryRuleV1) (MemoryCategoryRuleV1, error) {
	if err := validateMemoryOpaque("memory category key", input.Key, MaxOpaqueIDBytes); err != nil {
		return MemoryCategoryRuleV1{}, err
	}
	terms, err := normalizeMemoryTextSet("memory category term", input.Terms, MaxMemoryTermsPerCategoryV1)
	if err != nil {
		return MemoryCategoryRuleV1{}, err
	}
	if len(terms) == 0 {
		return MemoryCategoryRuleV1{}, fmt.Errorf("memory category terms must not be empty")
	}
	return MemoryCategoryRuleV1{Key: input.Key, Terms: terms}, nil
}

func normalizeMemoryCategoryRules(input []MemoryCategoryRuleV1) ([]MemoryCategoryRuleV1, error) {
	if len(input) > MaxMemoryCategoryRulesV1 {
		return nil, fmt.Errorf("category rule count exceeds %d", MaxMemoryCategoryRulesV1)
	}
	rules := make([]MemoryCategoryRuleV1, len(input))
	for index, rule := range input {
		normalized, err := normalizeMemoryCategoryRuleV1(rule)
		if err != nil {
			return nil, fmt.Errorf("category rule %d: %w", index, err)
		}
		rules[index] = normalized
	}
	sort.Slice(rules, func(left, right int) bool { return rules[left].Key < rules[right].Key })
	for index := 1; index < len(rules); index++ {
		if rules[index-1].Key == rules[index].Key {
			return nil, fmt.Errorf("category rule keys must be unique")
		}
	}
	if rules == nil {
		rules = []MemoryCategoryRuleV1{}
	}
	return rules, nil
}

func normalizeMemoryKinds(input []MemoryEntryKindV1, allowEmpty bool) ([]MemoryEntryKindV1, error) {
	if len(input) > 5 {
		return nil, fmt.Errorf("memory kind count exceeds 5")
	}
	if !allowEmpty && len(input) == 0 {
		return nil, fmt.Errorf("memory kinds must not be empty")
	}
	kinds := append([]MemoryEntryKindV1(nil), input...)
	for index, kind := range kinds {
		if err := kind.Validate(); err != nil {
			return nil, fmt.Errorf("memory kind %d: %w", index, err)
		}
	}
	sort.Slice(kinds, func(left, right int) bool { return kinds[left] < kinds[right] })
	for index := 1; index < len(kinds); index++ {
		if kinds[index-1] == kinds[index] {
			return nil, fmt.Errorf("memory kinds must be unique")
		}
	}
	if kinds == nil {
		kinds = []MemoryEntryKindV1{}
	}
	return kinds, nil
}

func normalizeMemoryWorkspaceIDs(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > MaxManifestEntries {
		return nil, fmt.Errorf("workspace IDs must contain between 1 and %d values", MaxManifestEntries)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if value == "*" {
			if len(values) != 1 {
				return nil, fmt.Errorf("workspace wildcard must be the sole value")
			}
			continue
		}
		if err := validateMemoryOpaque("memory workspace ID", value, MaxOpaqueIDBytes); err != nil {
			return nil, fmt.Errorf("workspace ID %d: %w", index, err)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf("workspace IDs must be unique")
		}
	}
	return values, nil
}

func normalizeMemoryTextSet(name string, input []string, maximum int) ([]string, error) {
	if len(input) > maximum {
		return nil, fmt.Errorf("%s count exceeds %d", name, maximum)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if err := validateMemoryText(name, value, MaxMemoryTermBytesV1, false); err != nil {
			return nil, fmt.Errorf("%s %d: %w", name, index, err)
		}
		if value != strings.TrimSpace(value) {
			return nil, fmt.Errorf("%s %d must not have surrounding whitespace", name, index)
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

func normalizeMemoryDigestSet(name string, input []string, maximum int) ([]string, error) {
	if len(input) == 0 || len(input) > maximum {
		return nil, fmt.Errorf("%ss must contain between 1 and %d values", name, maximum)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if !ValidSHA256(value) {
			return nil, fmt.Errorf("%s %d must be lowercase SHA-256", name, index)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, fmt.Errorf("%ss must be unique", name)
		}
	}
	return values, nil
}

func compareMemoryEntry(left, right MemoryEntryV1) int {
	if comparison := strings.Compare(string(left.Kind), string(right.Kind)); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(left.Key, right.Key); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(
		strings.Join(left.VisibleWorkspaceIDs, "\x00"),
		strings.Join(right.VisibleWorkspaceIDs, "\x00"),
	); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.EntryID, right.EntryID)
}

func memoryEntryIdentityKey(entry MemoryEntryV1) string {
	return string(entry.Kind) + "\x00" + entry.Key + "\x00" +
		strings.Join(entry.VisibleWorkspaceIDs, "\x00")
}

func compareMemoryCandidate(left, right MemoryCandidateV1) int {
	if comparison := strings.Compare(string(left.Kind), string(right.Kind)); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(left.Key, right.Key); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.EntryDigest, right.EntryDigest)
}

func memoryKindsContain(kinds []MemoryEntryKindV1, target MemoryEntryKindV1) bool {
	index := sort.Search(len(kinds), func(index int) bool { return kinds[index] >= target })
	return index < len(kinds) && kinds[index] == target
}

func memorySortedStringsContain(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func memoryWorkspaceAllowed(allowed []string, workspaceID string) bool {
	return len(allowed) == 1 && allowed[0] == "*" || memorySortedStringsContain(allowed, workspaceID)
}

func validateMemoryLimits(name string, maxItems, maxBytes uint32) error {
	if maxItems == 0 || maxItems > MaxMemoryItemsV1 {
		return fmt.Errorf("%s max_items must be between 1 and %d", name, MaxMemoryItemsV1)
	}
	if maxBytes == 0 || maxBytes > MaxMemoryTotalTextBytesV1 {
		return fmt.Errorf("%s max_total_text_bytes must be between 1 and %d", name, MaxMemoryTotalTextBytesV1)
	}
	return nil
}

func validateMemoryOpaque(name, value string, maximum int) error {
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

func validateMemoryText(name, value string, maximum int, allowEmpty bool) error {
	if err := validateBoundedText(name, value, maximum, allowEmpty); err != nil {
		return err
	}
	if value != CanonicalText(value) {
		return fmt.Errorf("%s must use Unicode NFC", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func marshalCanonicalMemoryWire(value any, maximum int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal memory wire: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(encoded, CanonicalJSONLimits{
		MaxBytes: maximum,
		MaxDepth: 128,
		MaxNodes: maximum,
	})
	if err != nil {
		return nil, fmt.Errorf("canonicalize memory wire: %w", err)
	}
	return canonical, nil
}

func decodeExactMemoryWire(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf("memory wire must contain between 1 and %d canonical bytes", maximum)
	}
	checked, err := CanonicalJSONWithLimits(canonical, CanonicalJSONLimits{
		MaxBytes: maximum,
		MaxDepth: 128,
		MaxNodes: maximum,
	})
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("memory wire is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode memory wire: %w", err)
	}
	return nil
}

func minMemoryUint32(left, right uint32) uint32 {
	if left < right {
		return left
	}
	return right
}
