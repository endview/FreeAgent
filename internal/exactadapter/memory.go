package exactadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidMemoryProvider = errors.New(
		"exactadapter: invalid deterministic memory provider",
	)
	ErrMemoryInvocation = errors.New(
		"exactadapter: deterministic memory provider rejected invocation",
	)
)

// DeterministicMemory is a stateless local selector over the candidates that
// Core has already filtered by Tenant, Agent, Workspace, TTL and authority.
// It owns no state and cannot widen the request candidate set.
type DeterministicMemory struct {
	provider reusableProviderIdentity
}

var _ modulehost.ModuleInvoker = (*DeterministicMemory)(nil)

func NewDeterministicMemory(
	provider moduleapi.ActivatedModuleRef,
) (*DeterministicMemory, error) {
	identity, err := newReusableProviderIdentity(provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMemoryProvider, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return nil, fmt.Errorf(
			"%w: execution class must be %q",
			ErrInvalidMemoryProvider,
			moduleapi.ExecutionTrustedInProcess,
		)
	}
	return &DeterministicMemory{provider: identity}, nil
}

func (memory *DeterministicMemory) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if memory == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: adapter is nil",
			ErrMemoryInvocation,
		)
	}
	if ctx == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrMemoryInvocation,
		)
	}
	if err := ctx.Err(); err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context: %w",
			ErrMemoryInvocation,
			err,
		)
	}
	if prepared.Invocation.Port != contextProvidePortV1 {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Port must be context.provide/v1",
			ErrMemoryInvocation,
		)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     contextProvidePortV1,
		Bindings: []moduleapi.PortBinding{prepared.Binding},
	})
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: frozen Binding: %v",
			ErrMemoryInvocation,
			err,
		)
	}
	provider := plan.Bindings[0].Provider
	if !memory.provider.matches(provider) {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Provider does not match the frozen memory adapter",
			ErrMemoryInvocation,
		)
	}
	request, requestDigest, err := moduleapi.RestoreMemoryContextRequestV1(
		append([]byte(nil), prepared.Invocation.Input...),
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: restore request: %v",
			ErrMemoryInvocation,
			err,
		)
	}

	ranked := make([]memoryCandidateRank, len(request.Candidates))
	queryTerms := lexicalTerms(request.QueryText)
	for index, candidate := range request.Candidates {
		ranked[index] = memoryCandidateRank{
			candidate: candidate,
			score:     memoryCandidateScore(queryTerms, candidate),
		}
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score > ranked[right].score
		}
		if ranked[left].candidate.Kind != ranked[right].candidate.Kind {
			return ranked[left].candidate.Kind < ranked[right].candidate.Kind
		}
		if ranked[left].candidate.Key != ranked[right].candidate.Key {
			return ranked[left].candidate.Key < ranked[right].candidate.Key
		}
		return ranked[left].candidate.EntryDigest <
			ranked[right].candidate.EntryDigest
	})
	selected := make([]string, 0, request.MaxItems)
	var totalTextBytes uint32
	for _, candidate := range ranked {
		if uint32(len(selected)) == request.MaxItems {
			break
		}
		textBytes := uint32(len(candidate.candidate.Text))
		if textBytes > request.MaxTotalTextBytes-totalTextBytes {
			continue
		}
		totalTextBytes += textBytes
		selected = append(selected, candidate.candidate.EntryDigest)
	}
	_, outputCanonical, _, err := moduleapi.NewMemoryContextOutputV1(
		moduleapi.MemoryContextOutputV1{
			SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest:        requestDigest,
			Snapshot:             request.Snapshot,
			SelectedEntryDigests: selected,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct output: %v",
			ErrMemoryInvocation,
			err,
		)
	}
	return modulehost.InvocationResult{
		Provider: provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   append(json.RawMessage(nil), outputCanonical...),
	}, nil
}

type memoryCandidateRank struct {
	candidate moduleapi.MemoryCandidateV1
	score     uint64
}

func memoryCandidateScore(
	queryTerms []string,
	candidate moduleapi.MemoryCandidateV1,
) uint64 {
	var base uint64
	switch candidate.Kind {
	case moduleapi.MemoryEntryPreference:
		base = 5
	case moduleapi.MemoryEntryFact:
		base = 4
	case moduleapi.MemoryEntryTaskSummary:
		base = 3
	case moduleapi.MemoryEntryCategoryCount:
		base = 2
	case moduleapi.MemoryEntryRepeatedTermCount:
		base = 1
	}
	score := base + 10*lexicalScore(
		queryTerms,
		candidate.Key+" "+candidate.Text,
	)
	if candidate.Count != 0 {
		// A logarithmic integer bucket prevents a hot counter from dominating
		// all textual matches and keeps the ranking platform-independent.
		for value := candidate.Count; value > 1; value >>= 1 {
			score++
		}
	}
	return score
}
