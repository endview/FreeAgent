package s3eval

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
)

var (
	ErrInvalidExperiment = errors.New("s3eval: invalid experiment")
	ErrMetricIntegrity   = errors.New("s3eval: metric integrity violation")
)

// ComputeKnownCacheHitRatio derives cached/(cached+uncached) when both
// components are known, or cached/input when those are the only known pair.
// UNKNOWN fields remain UNKNOWN and a zero denominator has no ratio.
func ComputeKnownCacheHitRatio(
	tokens currentstore.CompositeFamilyTokenTotalsV1,
) (*float64, error) {
	if tokens.CachedInput == nil {
		return nil, nil
	}
	cached := *tokens.CachedInput
	var denominator uint64
	switch {
	case tokens.UncachedInput != nil:
		uncached := *tokens.UncachedInput
		if math.MaxUint64-cached < uncached {
			return nil, fmt.Errorf(
				"%w: cache-token denominator overflows uint64",
				ErrMetricIntegrity,
			)
		}
		denominator = cached + uncached
		if tokens.Input != nil && *tokens.Input != denominator {
			return nil, fmt.Errorf(
				"%w: input tokens differ from cached plus uncached input",
				ErrMetricIntegrity,
			)
		}
	case tokens.Input != nil:
		denominator = *tokens.Input
		if cached > denominator {
			return nil, fmt.Errorf(
				"%w: cached input exceeds input tokens",
				ErrMetricIntegrity,
			)
		}
	default:
		return nil, nil
	}
	if denominator == 0 {
		return nil, nil
	}
	ratio := float64(cached) / float64(denominator)
	return &ratio, nil
}

// ComputeFairness calculates service-count Jain fairness plus order-sensitive
// starvation and burst facts. The service slice must already be in observed
// dispatch order. A Workspace is starved exactly when it has no persisted
// model Attempt in that observation; no time threshold is invented.
func ComputeFairness(
	workspaceIDs []string,
	serviceWorkspaceOrder []string,
) (FairnessReport, error) {
	if len(workspaceIDs) == 0 {
		return FairnessReport{}, fmt.Errorf(
			"%w: at least one Workspace is required",
			ErrInvalidExperiment,
		)
	}
	workspaceIndex := make(map[string]int, len(workspaceIDs))
	result := FairnessReport{
		Workspaces: make([]WorkspaceFairnessFact, len(workspaceIDs)),
	}
	for index, workspaceID := range workspaceIDs {
		if workspaceID == "" || workspaceID != strings.TrimSpace(workspaceID) {
			return FairnessReport{}, fmt.Errorf(
				"%w: invalid Workspace ID at index %d",
				ErrInvalidExperiment,
				index,
			)
		}
		if _, exists := workspaceIndex[workspaceID]; exists {
			return FairnessReport{}, fmt.Errorf(
				"%w: duplicate Workspace ID %q",
				ErrInvalidExperiment,
				workspaceID,
			)
		}
		workspaceIndex[workspaceID] = index
		result.Workspaces[index].WorkspaceID = workspaceID
	}

	var activeWorkspace string
	var activeCount uint64
	for index, workspaceID := range serviceWorkspaceOrder {
		workspacePosition, exists := workspaceIndex[workspaceID]
		if !exists {
			return FairnessReport{}, fmt.Errorf(
				"%w: service order references unknown Workspace %q",
				ErrMetricIntegrity,
				workspaceID,
			)
		}
		workspace := &result.Workspaces[workspacePosition]
		workspace.ServiceCount++
		if workspace.FirstServiceOrder == nil {
			order := uint64(index + 1)
			workspace.FirstServiceOrder = &order
		}
		if index == 0 {
			result.FirstServedWorkspace = workspaceID
		}
		if workspaceID == activeWorkspace {
			activeCount++
		} else {
			activeWorkspace = workspaceID
			activeCount = 1
		}
		if activeCount > result.LongestConsecutiveCount {
			result.LongestConsecutiveWorkspace = workspaceID
			result.LongestConsecutiveCount = activeCount
		}
	}

	var total float64
	var squareTotal float64
	for _, workspace := range result.Workspaces {
		count := float64(workspace.ServiceCount)
		total += count
		squareTotal += count * count
		if workspace.ServiceCount == 0 {
			result.StarvedWorkspaces = append(
				result.StarvedWorkspaces,
				workspace.WorkspaceID,
			)
		}
	}
	result.Starvation = len(result.StarvedWorkspaces) != 0
	if squareTotal != 0 {
		jain := total * total / (float64(len(result.Workspaces)) * squareTotal)
		result.JainIndex = &jain
	}
	return result, nil
}

func cloneTokenTotals(
	tokens currentstore.CompositeFamilyTokenTotalsV1,
) currentstore.CompositeFamilyTokenTotalsV1 {
	return currentstore.CompositeFamilyTokenTotalsV1{
		Input:         cloneUint64(tokens.Input),
		CachedInput:   cloneUint64(tokens.CachedInput),
		UncachedInput: cloneUint64(tokens.UncachedInput),
		Output:        cloneUint64(tokens.Output),
		Reasoning:     cloneUint64(tokens.Reasoning),
	}
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
