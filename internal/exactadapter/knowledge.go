package exactadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidKnowledgeProvider identifies a provider or immutable source
	// that cannot be frozen into the built-in deterministic knowledge adapter.
	ErrInvalidKnowledgeProvider = errors.New(
		"exactadapter: invalid deterministic knowledge provider",
	)

	// ErrKnowledgeInvocation identifies an invocation that is not the exact
	// context.provide/v1 request, provider, and source frozen into this adapter.
	ErrKnowledgeInvocation = errors.New(
		"exactadapter: deterministic knowledge provider rejected invocation",
	)
)

var contextProvidePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameContextProvide,
	ExactVersion: moduleapi.PortVersionV1,
}

// DeterministicKnowledge is a stateless, read-only context.provide/v1
// provider over one immutable knowledge-source/v1 corpus. It performs no I/O
// after construction and uses integer-only lexical ranking.
type DeterministicKnowledge struct {
	provider  reusableProviderIdentity
	source    moduleapi.KnowledgeSourceV1
	sourceRef moduleapi.KnowledgeSourceRefV1
}

var _ modulehost.ModuleInvoker = (*DeterministicKnowledge)(nil)

// NewDeterministicKnowledge strictly restores and freezes one local source.
// The first RAG slice deliberately accepts only TRUSTED_IN_PROCESS providers.
func NewDeterministicKnowledge(
	provider moduleapi.ActivatedModuleRef,
	sourceCanonical []byte,
) (*DeterministicKnowledge, error) {
	identity, err := newReusableProviderIdentity(provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKnowledgeProvider, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return nil, fmt.Errorf(
			"%w: execution class must be %q",
			ErrInvalidKnowledgeProvider,
			moduleapi.ExecutionTrustedInProcess,
		)
	}
	source, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(
		append([]byte(nil), sourceCanonical...),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: restore knowledge source: %v",
			ErrInvalidKnowledgeProvider,
			err,
		)
	}
	return &DeterministicKnowledge{
		provider:  identity,
		source:    source,
		sourceRef: sourceRef,
	}, nil
}

// Invoke restores one exact knowledge-context-request/v1, filters the frozen
// source by its four-level visibility rules, ranks matching chunks without
// floating point, and returns canonical knowledge-context-output/v1 bytes.
func (knowledge *DeterministicKnowledge) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if knowledge == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: adapter is nil",
			ErrKnowledgeInvocation,
		)
	}
	if ctx == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrKnowledgeInvocation,
		)
	}
	if err := ctx.Err(); err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context: %w",
			ErrKnowledgeInvocation,
			err,
		)
	}
	if prepared.Invocation.Port != contextProvidePortV1 {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Port must be context.provide/v1",
			ErrKnowledgeInvocation,
		)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     contextProvidePortV1,
		Bindings: []moduleapi.PortBinding{prepared.Binding},
	})
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: frozen Binding: %v",
			ErrKnowledgeInvocation,
			err,
		)
	}
	provider := plan.Bindings[0].Provider
	if !knowledge.provider.matches(provider) {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Provider does not match the frozen knowledge artifact adapter",
			ErrKnowledgeInvocation,
		)
	}

	request, requestDigest, err :=
		moduleapi.RestoreKnowledgeContextRequestV1(
			append([]byte(nil), prepared.Invocation.Input...),
		)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: restore request: %v",
			ErrKnowledgeInvocation,
			err,
		)
	}
	if request.Source != knowledge.sourceRef {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: request source does not match the frozen knowledge source",
			ErrKnowledgeInvocation,
		)
	}

	candidates := make([]knowledgeCandidate, 0, len(knowledge.source.Chunks))
	queryTerms := lexicalTerms(request.QueryText)
	for _, chunk := range knowledge.source.Chunks {
		if !knowledgeChunkVisible(chunk, request.Scope) {
			continue
		}
		score := lexicalScore(queryTerms, chunk.Text)
		if score == 0 {
			continue
		}
		candidates = append(candidates, knowledgeCandidate{
			chunk: chunk,
			score: score,
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score > candidates[right].score
		}
		return knowledgeChunkTieKey(candidates[left].chunk) <
			knowledgeChunkTieKey(candidates[right].chunk)
	})

	hits := make([]moduleapi.KnowledgeHitV1, 0, request.MaxHits)
	totalTextBytes := uint32(0)
	for _, candidate := range candidates {
		if uint32(len(hits)) == request.MaxHits {
			break
		}
		textBytes := uint32(len(candidate.chunk.Text))
		if textBytes > request.MaxTotalTextBytes-totalTextBytes {
			continue
		}
		totalTextBytes += textBytes
		hits = append(hits, moduleapi.KnowledgeHitV1{
			Rank:        uint32(len(hits) + 1),
			Document:    candidate.chunk.Document,
			ChunkID:     candidate.chunk.ChunkID,
			ChunkDigest: candidate.chunk.ChunkDigest,
			Text:        candidate.chunk.Text,
			VisibleTo: append(
				[]moduleapi.KnowledgeScopeRuleV1(nil),
				candidate.chunk.VisibleTo...,
			),
		})
	}
	output, outputCanonical, _, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: requestDigest,
			Source:        knowledge.sourceRef,
			Hits:          hits,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct output: %v",
			ErrKnowledgeInvocation,
			err,
		)
	}
	if err := moduleapi.ValidateKnowledgeContextOutputForRequestV1(
		request,
		output,
	); err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: validate output: %v",
			ErrKnowledgeInvocation,
			err,
		)
	}
	return modulehost.InvocationResult{
		Provider: provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   append(json.RawMessage(nil), outputCanonical...),
	}, nil
}

type knowledgeCandidate struct {
	chunk moduleapi.KnowledgeChunkV1
	score uint64
}

func knowledgeChunkVisible(
	chunk moduleapi.KnowledgeChunkV1,
	scope moduleapi.KnowledgeQueryScopeV1,
) bool {
	for _, rule := range chunk.VisibleTo {
		if moduleapi.KnowledgeScopeAllowsV1(rule, scope) {
			return true
		}
	}
	return false
}

// lexicalTerms returns a deterministic unique set. For unsegmented text such
// as Chinese, the complete letter/digit run remains a searchable substring.
func lexicalTerms(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(text), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value)
	})
	seen := make(map[string]struct{}, len(parts))
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	sort.Strings(terms)
	return terms
}

func lexicalScore(terms []string, text string) uint64 {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	var score uint64
	for _, term := range terms {
		score += uint64(strings.Count(lower, term))
	}
	return score
}

func knowledgeChunkTieKey(chunk moduleapi.KnowledgeChunkV1) string {
	return chunk.Document.ID + "\x00" + chunk.Document.Version + "\x00" +
		chunk.Document.Digest + "\x00" + chunk.ChunkID + "\x00" +
		chunk.ChunkDigest
}
