// Package memorycore contains the deterministic, I/O-free algorithms used by
// the first Memory vertical slice. Callers own persistence and are expected to
// invoke BuildSuccessfulRevision only from the first successful terminal
// transition of a model Attempt.
package memorycore

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	// SuccessfulRevisionAlgorithmV1 is recorded on every Core-derived entry.
	// Changing extraction, matching, pruning, or merge semantics requires a new
	// version instead of silently changing an existing revision.
	SuccessfulRevisionAlgorithmV1 = "freeagent.memory-successful-revision/v1"

	// These are state-evolution limits, not retrieval limits. In particular,
	// changing a Binding's MaxItems must not rewrite or silently prune durable
	// memory. A future change to these values requires a new algorithm version.
	MaxTaskSummariesPerWorkspaceV1  = 16
	MaxCategoryCountsPerWorkspaceV1 = 32
	MaxRepeatedTermsPerWorkspaceV1  = 64

	derivedEntryIDDomainV1 = "freeagent.memory-derived-entry-id/v1"

	// Source text is already bounded by the public wire. This second bound keeps
	// adversarial all-unique token input from creating an unbounded working map.
	maxExtractedDistinctTermsV1 = moduleapi.MaxMemoryEntriesV1 * moduleapi.MaxMemoryItemsV1
)

var (
	ErrInvalidInput = errors.New("memorycore: invalid input")
	ErrOverflow     = errors.New("memorycore: bounded state overflow")
)

// SuccessfulRevisionSource closes the two immutable ContentRecord inputs used
// by the extractive update. Text is required only to derive bounded summaries
// and counters; it is never copied into a knowledge entry.
type SuccessfulRevisionSource struct {
	TaskRef    string
	TaskText   string
	ResultRef  string
	ResultText string
}

// SuccessfulAttempt carries persisted successful-terminal facts. CompletedAt
// is deliberately supplied by the caller; this package never reads a clock.
type SuccessfulAttempt struct {
	ID                string
	CompletedAtUnixMS uint64
}

// SuccessfulRevisionInput contains every frozen value needed to recompute a
// successful update byte-for-byte. CurrentSnapshotRef must identify current;
// its ContentRecord digest becomes the new snapshot's parent digest.
type SuccessfulRevisionInput struct {
	CurrentSnapshotRef moduleapi.MemorySnapshotRefV1
	Source             SuccessfulRevisionSource
	Agent              moduleapi.MemoryObjectRefV1
	Workspace          moduleapi.MemoryObjectRefV1
	Attempt            SuccessfulAttempt
	Config             moduleapi.MemoryContextBindingV1
}

// FilterCandidates applies exact owner and Workspace authority, configured
// kinds, TTL, item and text limits. It returns a stable priority order and
// never mutates the snapshot or any input slice.
func FilterCandidates(
	snapshot moduleapi.AgentMemorySnapshotV1,
	snapshotRef moduleapi.MemorySnapshotRefV1,
	scope moduleapi.MemoryQueryScopeV1,
	config moduleapi.MemoryContextBindingV1,
	authority moduleapi.MemoryAuthorityCeilingV1,
	evaluatedAtUnixMS uint64,
) ([]moduleapi.MemoryCandidateV1, moduleapi.MemoryResolvedAuthorityV1, error) {
	frozenSnapshot, _, err := moduleapi.NewAgentMemorySnapshotV1(snapshot)
	if err != nil {
		return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalid("snapshot", err)
	}
	if err := snapshotRef.Validate(); err != nil {
		return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalid("snapshot reference", err)
	}
	if frozenSnapshot.TenantID != snapshotRef.TenantID ||
		frozenSnapshot.AgentID != snapshotRef.AgentID ||
		frozenSnapshot.Revision != snapshotRef.Revision {
		return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalidf(
			"snapshot body differs from exact snapshot reference",
		)
	}
	if evaluatedAtUnixMS == 0 || evaluatedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
		return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalidf(
			"evaluated_at_unix_ms must be between 1 and %d",
			moduleapi.MaxMemorySafeIntegerV1,
		)
	}
	resolved, err := moduleapi.ResolveMemoryAuthorityV1(
		config,
		authority,
		snapshotRef,
		scope,
	)
	if err != nil {
		return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalid("authority", err)
	}

	type rankedCandidate struct {
		entry     moduleapi.MemoryEntryV1
		candidate moduleapi.MemoryCandidateV1
	}
	ranked := make([]rankedCandidate, 0, len(frozenSnapshot.Entries))
	for index, entry := range frozenSnapshot.Entries {
		if !containsKind(resolved.Kinds, entry.Kind) ||
			!moduleapi.MemoryEntryVisibleToWorkspaceV1(entry, scope.Workspace.ID) {
			continue
		}
		if entry.CreatedAtUnixMS > evaluatedAtUnixMS {
			return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalidf(
				"snapshot entry %d was created after evaluated_at_unix_ms",
				index,
			)
		}
		if entry.ExpiresAtUnixMS != 0 && entry.ExpiresAtUnixMS <= evaluatedAtUnixMS {
			continue
		}
		candidate, err := moduleapi.NewMemoryCandidateV1(entry)
		if err != nil {
			return nil, moduleapi.MemoryResolvedAuthorityV1{}, invalid(
				fmt.Sprintf("snapshot entry %d", index),
				err,
			)
		}
		ranked = append(ranked, rankedCandidate{entry: entry, candidate: candidate})
	}
	sort.Slice(ranked, func(left, right int) bool {
		return compareCandidatePriority(ranked[left].entry, ranked[right].entry) < 0
	})

	selected := make([]moduleapi.MemoryCandidateV1, 0, minInt(len(ranked), int(resolved.MaxItems)))
	var textBytes uint64
	for _, item := range ranked {
		if uint32(len(selected)) == resolved.MaxItems {
			break
		}
		candidateBytes := uint64(len(item.candidate.Text))
		if candidateBytes > uint64(resolved.MaxTotalTextBytes)-textBytes {
			continue
		}
		textBytes += candidateBytes
		selected = append(selected, item.candidate)
	}
	if selected == nil {
		selected = []moduleapi.MemoryCandidateV1{}
	}
	return selected, resolved, nil
}

// KnowledgeReuseCounterProofV1 is the local, non-wire proof that the current
// Memory head contains both bounded routing counters required by exact-question
// Knowledge reuse. The complete entries are returned so Core can verify their
// algorithm, Config and Workspace closure before projecting only
// MemoryCandidateV1 into persisted evidence. Counter values are gates only;
// they do not grant authority and must never be treated as Knowledge content.
type KnowledgeReuseCounterProofV1 struct {
	CategoryCounter     moduleapi.MemoryEntryV1
	RepeatedTermCounter moduleapi.MemoryEntryV1
}

// SelectKnowledgeReuseCounterProofV1 selects one CATEGORY_COUNT and one
// REPEATED_TERM_COUNT from the current snapshot. It deliberately starts with
// FilterCandidates so the existing owner, Workspace authority, kind, TTL,
// MaxItems and MaxTotalTextBytes closure remains the sole read boundary.
//
// Collection tags and matched terms are exact, lowercase NFC keys supplied by
// the already-frozen Knowledge routing decision. Both thresholds are required.
// A valid input that simply lacks either threshold returns found=false without
// an error. Malformed snapshots, references, scopes, Config, Authority or key
// sets fail closed with ErrInvalidInput.
func SelectKnowledgeReuseCounterProofV1(
	snapshot moduleapi.AgentMemorySnapshotV1,
	snapshotRef moduleapi.MemorySnapshotRefV1,
	scope moduleapi.MemoryQueryScopeV1,
	config moduleapi.MemoryContextBindingV1,
	authority moduleapi.MemoryAuthorityCeilingV1,
	evaluatedAtUnixMS uint64,
	collectionTags []string,
	matchedTerms []string,
	minCategoryCount uint64,
	minRepeatedTermCount uint64,
) (KnowledgeReuseCounterProofV1, bool, error) {
	if minCategoryCount == 0 ||
		minCategoryCount > moduleapi.MaxKnowledgeReuseCountThresholdV1 {
		return KnowledgeReuseCounterProofV1{}, false, invalidf(
			"minimum category count must be between 1 and %d",
			moduleapi.MaxKnowledgeReuseCountThresholdV1,
		)
	}
	if minRepeatedTermCount == 0 ||
		minRepeatedTermCount > moduleapi.MaxKnowledgeReuseCountThresholdV1 {
		return KnowledgeReuseCounterProofV1{}, false, invalidf(
			"minimum repeated-term count must be between 1 and %d",
			moduleapi.MaxKnowledgeReuseCountThresholdV1,
		)
	}
	tags, err := normalizeKnowledgeReuseCounterKeys(
		"collection tag",
		collectionTags,
		moduleapi.MaxKnowledgeCollectionTagsV1,
	)
	if err != nil {
		return KnowledgeReuseCounterProofV1{}, false, err
	}
	terms, err := normalizeKnowledgeReuseCounterKeys(
		"matched term",
		matchedTerms,
		moduleapi.MaxKnowledgeMatchTermsV1,
	)
	if err != nil {
		return KnowledgeReuseCounterProofV1{}, false, err
	}

	// Do not reproduce any part of the read closure here. Its output is the
	// complete eligible set, including the configured/authorized MaxItems cut.
	candidates, _, err := FilterCandidates(
		snapshot,
		snapshotRef,
		scope,
		config,
		authority,
		evaluatedAtUnixMS,
	)
	if err != nil {
		return KnowledgeReuseCounterProofV1{}, false, err
	}
	frozenSnapshot, _, err := moduleapi.NewAgentMemorySnapshotV1(snapshot)
	if err != nil {
		return KnowledgeReuseCounterProofV1{}, false, invalid("snapshot", err)
	}
	configDigest, err := moduleapi.ComputeMemoryContextBindingDigestV1(config)
	if err != nil {
		return KnowledgeReuseCounterProofV1{}, false, invalid("config digest", err)
	}

	entriesByDigest := make(map[string]moduleapi.MemoryEntryV1, len(frozenSnapshot.Entries))
	for _, entry := range frozenSnapshot.Entries {
		entriesByDigest[entry.EntryDigest] = entry
	}
	eligible := make([]moduleapi.MemoryEntryV1, 0, len(candidates))
	for index, candidate := range candidates {
		entry, exists := entriesByDigest[candidate.EntryDigest]
		if !exists {
			return KnowledgeReuseCounterProofV1{}, false, invalidf(
				"filtered candidate %d is absent from the exact snapshot",
				index,
			)
		}
		if entry.Kind != candidate.Kind || entry.Key != candidate.Key ||
			entry.Count != candidate.Count {
			return KnowledgeReuseCounterProofV1{}, false, invalidf(
				"filtered candidate %d differs from its exact snapshot entry",
				index,
			)
		}
		if !isCount(entry.Kind) ||
			!exactDerivedWorkspace(entry, scope.Workspace.ID) ||
			entry.AlgorithmVersion != SuccessfulRevisionAlgorithmV1 ||
			entry.AlgorithmConfigDigest != configDigest {
			continue
		}
		switch entry.Kind {
		case moduleapi.MemoryEntryCategoryCount:
			if !containsSortedString(tags, entry.Key) || entry.Count < minCategoryCount {
				continue
			}
		case moduleapi.MemoryEntryRepeatedTermCount:
			if !containsSortedString(terms, entry.Key) || entry.Count < minRepeatedTermCount {
				continue
			}
		default:
			continue
		}
		eligible = append(eligible, entry)
	}
	sort.Slice(eligible, func(left, right int) bool {
		return compareKnowledgeReuseCounterCandidate(
			eligible[left],
			eligible[right],
		) < 0
	})

	var proof KnowledgeReuseCounterProofV1
	for _, entry := range eligible {
		switch entry.Kind {
		case moduleapi.MemoryEntryCategoryCount:
			if proof.CategoryCounter.EntryDigest == "" {
				proof.CategoryCounter = entry
			}
		case moduleapi.MemoryEntryRepeatedTermCount:
			if proof.RepeatedTermCounter.EntryDigest == "" {
				proof.RepeatedTermCounter = entry
			}
		}
	}
	if proof.CategoryCounter.EntryDigest == "" ||
		proof.RepeatedTermCounter.EntryDigest == "" {
		return KnowledgeReuseCounterProofV1{}, false, nil
	}
	return proof, true, nil
}

// BuildSuccessfulRevision deterministically rebases one successful Attempt on
// the supplied current snapshot. It preserves confirmed facts/preferences,
// creates only bounded Workspace-local derived entries, and returns canonical
// snapshot bytes ready for the caller's atomic append transaction.
//
// FAILED, UNKNOWN, PENDING, and retry semantics are intentionally absent from
// this API. Such paths must not call it.
func BuildSuccessfulRevision(
	current moduleapi.AgentMemorySnapshotV1,
	input SuccessfulRevisionInput,
) (moduleapi.AgentMemorySnapshotV1, []byte, error) {
	frozenCurrent, _, err := moduleapi.NewAgentMemorySnapshotV1(current)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, nil, invalid("current snapshot", err)
	}
	validated, err := validateSuccessfulRevisionInput(frozenCurrent, input)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, nil, err
	}
	if frozenCurrent.Revision == moduleapi.MaxMemorySafeIntegerV1 {
		return moduleapi.AgentMemorySnapshotV1{}, nil, overflowf("snapshot revision")
	}
	if frozenCurrent.SourceAttemptID == validated.Attempt.ID {
		return moduleapi.AgentMemorySnapshotV1{}, nil, invalidf(
			"attempt %q is already the current snapshot source",
			validated.Attempt.ID,
		)
	}

	expiresAt, err := derivedExpiry(
		validated.Attempt.CompletedAtUnixMS,
		validated.Config.EntryTTLSeconds,
	)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, nil, err
	}
	configDigest, err := moduleapi.ComputeMemoryContextBindingDigestV1(validated.Config)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, nil, invalid("config digest", err)
	}
	sourceRefs := normalizedDigestUnion(
		nil,
		validated.Source.TaskRef,
		validated.Source.ResultRef,
	)

	// Expired derived state is safe to discard from the new immutable snapshot;
	// confirmed facts and preferences are preserved byte-for-byte even when an
	// optional expiry makes them invisible to reads.
	entries := make([]moduleapi.MemoryEntryV1, 0, len(frozenCurrent.Entries)+1)
	for _, entry := range frozenCurrent.Entries {
		if isDerived(entry.Kind) && entry.ExpiresAtUnixMS <= validated.Attempt.CompletedAtUnixMS {
			continue
		}
		entries = append(entries, entry)
	}

	if containsKind(validated.Config.Kinds, moduleapi.MemoryEntryTaskSummary) {
		if hasDerivedIdentity(
			entries,
			moduleapi.MemoryEntryTaskSummary,
			validated.Attempt.ID,
			validated.Workspace.ID,
		) {
			return moduleapi.AgentMemorySnapshotV1{}, nil, invalidf(
				"attempt %q already has a task summary in workspace %q",
				validated.Attempt.ID,
				validated.Workspace.ID,
			)
		}
		summary := buildExtractiveSummary(
			validated.Source.TaskText,
			validated.Source.ResultText,
			int(validated.Config.SummaryMaxTextBytes),
		)
		entry, err := newDerivedTextEntry(
			moduleapi.MemoryEntryTaskSummary,
			validated.Attempt.ID,
			summary,
			validated.Workspace.ID,
			sourceRefs,
			configDigest,
			validated.Attempt.CompletedAtUnixMS,
			expiresAt,
		)
		if err != nil {
			return moduleapi.AgentMemorySnapshotV1{}, nil, err
		}
		entries = append(entries, entry)
	}

	var corpus string
	var termCounts map[string]uint64
	if containsKind(validated.Config.Kinds, moduleapi.MemoryEntryCategoryCount) ||
		containsKind(validated.Config.Kinds, moduleapi.MemoryEntryRepeatedTermCount) {
		// Counters describe what the user asks this Agent to handle. Counting
		// generated answers would feed the model's own vocabulary back into the
		// routing signal and let long code responses dominate user intent.
		corpus = foldText(validated.Source.TaskText)
		termCounts, err = extractTermCounts(corpus, validated.Config.StopTerms)
		if err != nil {
			return moduleapi.AgentMemorySnapshotV1{}, nil, err
		}
	}
	if containsKind(validated.Config.Kinds, moduleapi.MemoryEntryCategoryCount) {
		for _, rule := range validated.Config.CategoryRules {
			if !categoryMatches(rule, corpus, termCounts) {
				continue
			}
			entries, err = mergeCountEntry(
				entries,
				moduleapi.MemoryEntryCategoryCount,
				rule.Key,
				1,
				validated.Workspace.ID,
				sourceRefs,
				configDigest,
				validated.Attempt.CompletedAtUnixMS,
				expiresAt,
			)
			if err != nil {
				return moduleapi.AgentMemorySnapshotV1{}, nil, err
			}
		}
	}
	if containsKind(validated.Config.Kinds, moduleapi.MemoryEntryRepeatedTermCount) {
		terms := make([]string, 0, len(termCounts))
		for term := range termCounts {
			terms = append(terms, term)
		}
		sort.Strings(terms)
		for _, term := range terms {
			entries, err = mergeCountEntry(
				entries,
				moduleapi.MemoryEntryRepeatedTermCount,
				term,
				termCounts[term],
				validated.Workspace.ID,
				sourceRefs,
				configDigest,
				validated.Attempt.CompletedAtUnixMS,
				expiresAt,
			)
			if err != nil {
				return moduleapi.AgentMemorySnapshotV1{}, nil, err
			}
		}
	}

	entries = pruneConfiguredDerivedTopK(
		entries,
		validated.Workspace.ID,
		validated.Config.Kinds,
	)
	if len(entries) > moduleapi.MaxMemoryEntriesV1 {
		return moduleapi.AgentMemorySnapshotV1{}, nil, overflowf(
			"snapshot entries exceed %d after preserving existing scopes",
			moduleapi.MaxMemoryEntriesV1,
		)
	}

	next := moduleapi.AgentMemorySnapshotV1{
		SchemaVersion:          moduleapi.MemorySnapshotSchemaV1,
		TenantID:               frozenCurrent.TenantID,
		AgentID:                frozenCurrent.AgentID,
		Revision:               frozenCurrent.Revision + 1,
		PreviousSnapshotDigest: validated.CurrentSnapshotRef.Digest,
		SourceAttemptID:        validated.Attempt.ID,
		Entries:                entries,
	}
	frozen, canonical, err := moduleapi.NewAgentMemorySnapshotV1(next)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, nil, overflow("construct next snapshot", err)
	}
	return frozen, canonical, nil
}

func validateSuccessfulRevisionInput(
	current moduleapi.AgentMemorySnapshotV1,
	input SuccessfulRevisionInput,
) (SuccessfulRevisionInput, error) {
	if err := input.CurrentSnapshotRef.Validate(); err != nil {
		return SuccessfulRevisionInput{}, invalid("current snapshot reference", err)
	}
	if current.TenantID != input.CurrentSnapshotRef.TenantID ||
		current.AgentID != input.CurrentSnapshotRef.AgentID ||
		current.Revision != input.CurrentSnapshotRef.Revision {
		return SuccessfulRevisionInput{}, invalidf(
			"current snapshot body differs from exact current snapshot reference",
		)
	}
	if err := input.Agent.Validate(); err != nil {
		return SuccessfulRevisionInput{}, invalid("exact Agent", err)
	}
	if input.Agent.ID == "*" {
		return SuccessfulRevisionInput{}, invalidf(
			"exact Agent ID must not use the Memory wildcard",
		)
	}
	if err := input.Workspace.Validate(); err != nil {
		return SuccessfulRevisionInput{}, invalid("exact Workspace", err)
	}
	if input.Workspace.ID == "*" {
		return SuccessfulRevisionInput{}, invalidf(
			"exact Workspace ID must not use the Memory wildcard",
		)
	}
	if input.Agent.ID != current.AgentID {
		return SuccessfulRevisionInput{}, invalidf(
			"exact Agent %q differs from memory owner %q",
			input.Agent.ID,
			current.AgentID,
		)
	}
	if !moduleapi.ValidSHA256(input.Source.TaskRef) ||
		!moduleapi.ValidSHA256(input.Source.ResultRef) {
		return SuccessfulRevisionInput{}, invalidf(
			"task_ref and result_ref must be lowercase SHA-256 content digests",
		)
	}
	if err := validateSourceText("task text", input.Source.TaskText); err != nil {
		return SuccessfulRevisionInput{}, err
	}
	if err := validateSourceText("result text", input.Source.ResultText); err != nil {
		return SuccessfulRevisionInput{}, err
	}
	if err := validateOpaque("attempt ID", input.Attempt.ID); err != nil {
		return SuccessfulRevisionInput{}, err
	}
	if input.Attempt.CompletedAtUnixMS == 0 ||
		input.Attempt.CompletedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
		return SuccessfulRevisionInput{}, invalidf(
			"attempt completed_at_unix_ms must be between 1 and %d",
			moduleapi.MaxMemorySafeIntegerV1,
		)
	}
	frozenConfig, _, _, err := moduleapi.NewMemoryContextBindingV1(input.Config)
	if err != nil {
		return SuccessfulRevisionInput{}, invalid("config", err)
	}
	input.Config = frozenConfig
	input.Source.TaskText = moduleapi.CanonicalText(input.Source.TaskText)
	input.Source.ResultText = moduleapi.CanonicalText(input.Source.ResultText)
	return input, nil
}

func derivedExpiry(createdAtUnixMS, ttlSeconds uint64) (uint64, error) {
	if ttlSeconds == 0 {
		return 0, nil
	}
	if ttlSeconds > moduleapi.MaxMemorySafeIntegerV1/1000 {
		return 0, overflowf("entry TTL milliseconds")
	}
	ttlMS := ttlSeconds * 1000
	if createdAtUnixMS > moduleapi.MaxMemorySafeIntegerV1-ttlMS {
		return 0, overflowf("entry expiry")
	}
	return createdAtUnixMS + ttlMS, nil
}

func newDerivedTextEntry(
	kind moduleapi.MemoryEntryKindV1,
	key string,
	text string,
	workspaceID string,
	sourceRefs []string,
	configDigest string,
	createdAtUnixMS uint64,
	expiresAtUnixMS uint64,
) (moduleapi.MemoryEntryV1, error) {
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               derivedEntryID(kind, key, workspaceID),
		Kind:                  kind,
		Key:                   key,
		Text:                  text,
		VisibleWorkspaceIDs:   []string{workspaceID},
		SourceRefs:            append([]string(nil), sourceRefs...),
		AlgorithmVersion:      SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: configDigest,
		CreatedAtUnixMS:       createdAtUnixMS,
		ExpiresAtUnixMS:       expiresAtUnixMS,
	})
	if err != nil {
		return moduleapi.MemoryEntryV1{}, invalid("derived task summary", err)
	}
	return entry, nil
}

func mergeCountEntry(
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	delta uint64,
	workspaceID string,
	sourceRefs []string,
	configDigest string,
	createdAtUnixMS uint64,
	expiresAtUnixMS uint64,
) ([]moduleapi.MemoryEntryV1, error) {
	if delta == 0 {
		return entries, nil
	}
	index := findDerivedIdentity(entries, kind, key, workspaceID)
	entryID := derivedEntryID(kind, key, workspaceID)
	count := delta
	created := createdAtUnixMS
	refs := append([]string(nil), sourceRefs...)
	if index >= 0 {
		existing := entries[index]
		entryID = existing.EntryID
		// A count produced under another algorithm/config is not the same
		// measurement series. Replace it from the current delta instead of
		// silently mixing incompatible semantics under the new digest.
		if existing.AlgorithmVersion == SuccessfulRevisionAlgorithmV1 &&
			existing.AlgorithmConfigDigest == configDigest {
			if existing.Count > moduleapi.MaxMemorySafeIntegerV1-delta {
				return nil, overflowf("%s count for key %q", kind, key)
			}
			count = existing.Count + delta
			if existing.CreatedAtUnixMS < created {
				created = existing.CreatedAtUnixMS
			}
			if existing.ExpiresAtUnixMS > expiresAtUnixMS {
				expiresAtUnixMS = existing.ExpiresAtUnixMS
			}
			refs = normalizedDigestUnion(existing.SourceRefs, sourceRefs...)
		}
	}
	updated, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               entryID,
		Kind:                  kind,
		Key:                   key,
		Count:                 count,
		VisibleWorkspaceIDs:   []string{workspaceID},
		SourceRefs:            refs,
		AlgorithmVersion:      SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: configDigest,
		CreatedAtUnixMS:       created,
		ExpiresAtUnixMS:       expiresAtUnixMS,
	})
	if err != nil {
		return nil, invalid(fmt.Sprintf("derived %s entry", kind), err)
	}
	if index >= 0 {
		entries[index] = updated
	} else {
		entries = append(entries, updated)
	}
	return entries, nil
}

func buildExtractiveSummary(taskText, resultText string, maximum int) string {
	originalTask := firstSentence(strings.TrimSpace(taskText))
	originalResult := firstSentence(strings.TrimSpace(resultText))
	if maximum <= 1 || originalResult == "" {
		return truncateUTF8(originalTask, maximum)
	}
	// Reserve bounded space for both immutable sources instead of allowing a
	// long task to starve the successful result completely.
	taskBudget := (maximum - 1 + 1) / 2
	resultBudget := maximum - 1 - taskBudget
	task := truncateUTF8(originalTask, taskBudget)
	result := truncateUTF8(originalResult, resultBudget)
	switch {
	case task == "" && result == "":
		// A legal UTF-8 code point can require four bytes. With the smallest
		// legal summary budget, the balanced sub-budgets can therefore retain
		// neither side. Fall back to an un-split original source; never retry a
		// value that was already truncated to empty.
		if fallback := truncateUTF8(originalTask, maximum); fallback != "" {
			return fallback
		}
		return truncateUTF8(originalResult, maximum)
	case task == "":
		return truncateUTF8(result, maximum)
	case result == "":
		return truncateUTF8(task, maximum)
	default:
		return moduleapi.CanonicalText(task + "\n" + result)
	}
}

func firstSentence(text string) string {
	for index, value := range text {
		switch value {
		case '.', '!', '?', '\n', '\u3002', '\uff01', '\uff1f':
			end := index + utf8.RuneLen(value)
			return strings.TrimSpace(text[:end])
		}
	}
	return strings.TrimSpace(text)
}

func truncateUTF8(text string, maximum int) string {
	text = strings.TrimSpace(text)
	if maximum <= 0 {
		return ""
	}
	if len(text) <= maximum {
		return text
	}
	end := maximum
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return strings.TrimSpace(text[:end])
}

func extractTermCounts(corpus string, stopTerms []string) (map[string]uint64, error) {
	stops := make(map[string]struct{}, len(stopTerms))
	for _, term := range stopTerms {
		stops[foldText(term)] = struct{}{}
	}
	counts := make(map[string]uint64)
	for _, term := range lexicalTerms(corpus) {
		term = strings.TrimSpace(moduleapi.CanonicalText(term))
		if term == "" || len(term) > moduleapi.MaxMemoryTermBytesV1 {
			continue
		}
		if _, stopped := stops[term]; stopped {
			continue
		}
		if _, exists := counts[term]; !exists && len(counts) == maxExtractedDistinctTermsV1 {
			return nil, overflowf(
				"distinct repeated terms exceed %d",
				maxExtractedDistinctTermsV1,
			)
		}
		if counts[term] == moduleapi.MaxMemorySafeIntegerV1 {
			return nil, overflowf("term count for %q", term)
		}
		counts[term]++
	}
	return counts, nil
}

// lexicalTerms keeps letter/digit runs for whitespace-delimited languages and
// emits overlapping Han bigrams for unsegmented Chinese text. The latter is a
// deliberately small deterministic approximation, not a language model or a
// hidden knowledge base.
func lexicalTerms(text string) []string {
	terms := make([]string, 0)
	regular := make([]rune, 0, 32)
	han := make([]rune, 0, 32)
	flushRegular := func() {
		if len(regular) != 0 {
			terms = append(terms, string(regular))
			regular = regular[:0]
		}
	}
	flushHan := func() {
		switch len(han) {
		case 0:
		case 1:
			terms = append(terms, string(han))
		default:
			for index := 0; index+1 < len(han); index++ {
				terms = append(terms, string(han[index:index+2]))
			}
		}
		han = han[:0]
	}
	for _, value := range text {
		switch {
		case unicode.In(value, unicode.Han):
			flushRegular()
			han = append(han, value)
		case unicode.IsLetter(value) || unicode.IsDigit(value):
			flushHan()
			regular = append(regular, value)
		default:
			flushRegular()
			flushHan()
		}
	}
	flushRegular()
	flushHan()
	return terms
}

func categoryMatches(
	rule moduleapi.MemoryCategoryRuleV1,
	corpus string,
	termCounts map[string]uint64,
) bool {
	for _, configured := range rule.Terms {
		term := foldText(configured)
		if termCounts[term] != 0 || strings.Contains(corpus, term) {
			return true
		}
	}
	return false
}

func pruneConfiguredDerivedTopK(
	entries []moduleapi.MemoryEntryV1,
	workspaceID string,
	kinds []moduleapi.MemoryEntryKindV1,
) []moduleapi.MemoryEntryV1 {
	for _, kind := range []moduleapi.MemoryEntryKindV1{
		moduleapi.MemoryEntryTaskSummary,
		moduleapi.MemoryEntryCategoryCount,
		moduleapi.MemoryEntryRepeatedTermCount,
	} {
		if !containsKind(kinds, kind) {
			continue
		}
		limit := derivedTopKLimit(kind)
		matching := make([]moduleapi.MemoryEntryV1, 0)
		remaining := make([]moduleapi.MemoryEntryV1, 0, len(entries))
		for _, entry := range entries {
			if entry.Kind == kind && exactDerivedWorkspace(entry, workspaceID) {
				matching = append(matching, entry)
			} else {
				remaining = append(remaining, entry)
			}
		}
		sort.Slice(matching, func(left, right int) bool {
			return compareDerivedTopK(matching[left], matching[right]) < 0
		})
		if len(matching) > limit {
			matching = matching[:limit]
		}
		entries = append(remaining, matching...)
	}
	return entries
}

func derivedTopKLimit(kind moduleapi.MemoryEntryKindV1) int {
	switch kind {
	case moduleapi.MemoryEntryTaskSummary:
		return MaxTaskSummariesPerWorkspaceV1
	case moduleapi.MemoryEntryCategoryCount:
		return MaxCategoryCountsPerWorkspaceV1
	case moduleapi.MemoryEntryRepeatedTermCount:
		return MaxRepeatedTermsPerWorkspaceV1
	default:
		return 0
	}
}

func compareCandidatePriority(left, right moduleapi.MemoryEntryV1) int {
	if leftPriority, rightPriority := kindPriority(left.Kind), kindPriority(right.Kind); leftPriority != rightPriority {
		return compareInt(leftPriority, rightPriority)
	}
	if isCount(left.Kind) && left.Count != right.Count {
		if left.Count > right.Count {
			return -1
		}
		return 1
	}
	if left.CreatedAtUnixMS != right.CreatedAtUnixMS {
		if left.CreatedAtUnixMS > right.CreatedAtUnixMS {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(left.Key, right.Key); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(strings.Join(left.VisibleWorkspaceIDs, "\x00"), strings.Join(right.VisibleWorkspaceIDs, "\x00")); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.EntryDigest, right.EntryDigest)
}

func compareDerivedTopK(left, right moduleapi.MemoryEntryV1) int {
	if isCount(left.Kind) && left.Count != right.Count {
		if left.Count > right.Count {
			return -1
		}
		return 1
	}
	if left.CreatedAtUnixMS != right.CreatedAtUnixMS {
		if left.CreatedAtUnixMS > right.CreatedAtUnixMS {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(left.Key, right.Key); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.EntryDigest, right.EntryDigest)
}

func compareKnowledgeReuseCounterCandidate(
	left moduleapi.MemoryEntryV1,
	right moduleapi.MemoryEntryV1,
) int {
	if left.Count != right.Count {
		if left.Count > right.Count {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(left.Key, right.Key); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.EntryDigest, right.EntryDigest)
}

func normalizeKnowledgeReuseCounterKeys(
	name string,
	input []string,
	maximum int,
) ([]string, error) {
	if len(input) == 0 || len(input) > maximum {
		return nil, invalidf(
			"%s count must be between 1 and %d",
			name,
			maximum,
		)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if len(value) > moduleapi.MaxKnowledgeRoutingValueBytesV1 {
			return nil, invalidf(
				"%s %d exceeds %d bytes",
				name,
				index,
				moduleapi.MaxKnowledgeRoutingValueBytesV1,
			)
		}
		if err := validateOpaque(name, value); err != nil {
			return nil, invalid(fmt.Sprintf("%s %d", name, index), err)
		}
		if value != foldText(value) {
			return nil, invalidf(
				"%s %d must use lowercase Unicode NFC",
				name,
				index,
			)
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return nil, invalidf("%ss must be unique", name)
		}
	}
	return values, nil
}

func kindPriority(kind moduleapi.MemoryEntryKindV1) int {
	switch kind {
	case moduleapi.MemoryEntryFact:
		return 0
	case moduleapi.MemoryEntryPreference:
		return 1
	case moduleapi.MemoryEntryTaskSummary:
		return 2
	case moduleapi.MemoryEntryCategoryCount:
		return 3
	case moduleapi.MemoryEntryRepeatedTermCount:
		return 4
	default:
		return math.MaxInt
	}
}

func containsKind(kinds []moduleapi.MemoryEntryKindV1, target moduleapi.MemoryEntryKindV1) bool {
	for _, kind := range kinds {
		if kind == target {
			return true
		}
	}
	return false
}

func containsSortedString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func isDerived(kind moduleapi.MemoryEntryKindV1) bool {
	return kind == moduleapi.MemoryEntryTaskSummary ||
		kind == moduleapi.MemoryEntryCategoryCount ||
		kind == moduleapi.MemoryEntryRepeatedTermCount
}

func isCount(kind moduleapi.MemoryEntryKindV1) bool {
	return kind == moduleapi.MemoryEntryCategoryCount ||
		kind == moduleapi.MemoryEntryRepeatedTermCount
}

func exactDerivedWorkspace(entry moduleapi.MemoryEntryV1, workspaceID string) bool {
	return len(entry.VisibleWorkspaceIDs) == 1 && entry.VisibleWorkspaceIDs[0] == workspaceID
}

func findDerivedIdentity(
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspaceID string,
) int {
	for index, entry := range entries {
		if entry.Kind == kind && entry.Key == key && exactDerivedWorkspace(entry, workspaceID) {
			return index
		}
	}
	return -1
}

func hasDerivedIdentity(
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspaceID string,
) bool {
	return findDerivedIdentity(entries, kind, key, workspaceID) >= 0
}

func derivedEntryID(kind moduleapi.MemoryEntryKindV1, key, workspaceID string) string {
	return moduleapi.Digest(
		derivedEntryIDDomainV1,
		[]byte(string(kind)+"\x00"+workspaceID+"\x00"+key),
	)
}

func normalizedDigestUnion(existing []string, additions ...string) []string {
	// Every update must retain the exact Task/Result refs that caused its newest
	// delta. Older provenance fills the remaining bounded slots in lexical order.
	// This is deterministic for a given rebase head and never drops the current
	// successful Attempt's source merely because its digest sorts later.
	additionSet := make(map[string]struct{}, len(additions))
	for _, digest := range additions {
		additionSet[digest] = struct{}{}
	}
	values := make([]string, 0, len(existing)+len(additionSet))
	for digest := range additionSet {
		values = append(values, digest)
	}
	sort.Strings(values)
	if len(values) >= moduleapi.MaxMemorySourceRefsV1 {
		return values[:moduleapi.MaxMemorySourceRefsV1]
	}
	older := make([]string, 0, len(existing))
	for _, digest := range existing {
		if _, isCurrent := additionSet[digest]; !isCurrent {
			older = append(older, digest)
		}
	}
	sort.Strings(older)
	remaining := moduleapi.MaxMemorySourceRefsV1 - len(values)
	if len(older) > remaining {
		older = older[:remaining]
	}
	values = append(values, older...)
	sort.Strings(values)
	return values
}

func foldText(value string) string {
	return moduleapi.CanonicalText(strings.ToLower(moduleapi.CanonicalText(value)))
}

func validateSourceText(name, value string) error {
	if value == "" || len(value) > moduleapi.MaxTextBytes || !utf8.ValidString(value) {
		return invalidf("%s must contain between 1 and %d valid UTF-8 bytes", name, moduleapi.MaxTextBytes)
	}
	if value != moduleapi.CanonicalText(value) {
		return invalidf("%s must use Unicode NFC", name)
	}
	if strings.TrimSpace(value) == "" {
		return invalidf("%s must not be whitespace-only", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return invalidf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func validateOpaque(name, value string) error {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != strings.TrimSpace(value) || value != moduleapi.CanonicalText(value) {
		return invalidf("%s must be canonical UTF-8 containing between 1 and %d bytes", name, moduleapi.MaxOpaqueIDBytes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalidf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func invalid(name string, cause error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidInput, name, cause)
}

func invalidf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, arguments...))
}

func overflow(name string, cause error) error {
	return fmt.Errorf("%w: %s: %v", ErrOverflow, name, cause)
}

func overflowf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrOverflow, fmt.Sprintf(format, arguments...))
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
