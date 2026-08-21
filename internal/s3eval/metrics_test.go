package s3eval

import (
	"errors"
	"math"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
)

func TestComputeFairnessBalancedAndStarved(t *testing.T) {
	t.Parallel()

	balanced, err := ComputeFairness(
		[]string{"workspace-a", "workspace-b", "workspace-c"},
		[]string{
			"workspace-a", "workspace-b", "workspace-c",
			"workspace-a", "workspace-b", "workspace-c",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if balanced.JainIndex == nil || *balanced.JainIndex != 1 ||
		balanced.Starvation ||
		balanced.FirstServedWorkspace != "workspace-a" ||
		balanced.LongestConsecutiveWorkspace != "workspace-a" ||
		balanced.LongestConsecutiveCount != 1 {
		t.Fatalf("balanced fairness=%+v", balanced)
	}
	for index, workspace := range balanced.Workspaces {
		if workspace.ServiceCount != 2 ||
			workspace.FirstServiceOrder == nil ||
			*workspace.FirstServiceOrder != uint64(index+1) {
			t.Fatalf("balanced Workspace %d=%+v", index, workspace)
		}
	}

	starved, err := ComputeFairness(
		[]string{"workspace-a", "workspace-b", "workspace-c"},
		[]string{"workspace-a", "workspace-a", "workspace-b"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if starved.JainIndex == nil || math.Abs(*starved.JainIndex-0.6) > 1e-12 ||
		!starved.Starvation ||
		len(starved.StarvedWorkspaces) != 1 ||
		starved.StarvedWorkspaces[0] != "workspace-c" ||
		starved.LongestConsecutiveWorkspace != "workspace-a" ||
		starved.LongestConsecutiveCount != 2 ||
		starved.Workspaces[2].FirstServiceOrder != nil {
		t.Fatalf("starved fairness=%+v", starved)
	}
}

func TestComputeKnownCacheHitRatioPreservesUnknown(t *testing.T) {
	t.Parallel()

	input := uint64(100)
	cached := uint64(25)
	uncached := uint64(75)
	ratio, err := ComputeKnownCacheHitRatio(
		currentstore.CompositeFamilyTokenTotalsV1{
			Input:         &input,
			CachedInput:   &cached,
			UncachedInput: &uncached,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ratio == nil || *ratio != 0.25 {
		t.Fatalf("known ratio=%v", ratio)
	}

	unknown, err := ComputeKnownCacheHitRatio(
		currentstore.CompositeFamilyTokenTotalsV1{Input: &input},
	)
	if err != nil {
		t.Fatal(err)
	}
	if unknown != nil {
		t.Fatalf("UNKNOWN cache tokens produced ratio %v", *unknown)
	}

	badCached := uint64(101)
	_, err = ComputeKnownCacheHitRatio(
		currentstore.CompositeFamilyTokenTotalsV1{
			Input:       &input,
			CachedInput: &badCached,
		},
	)
	if !errors.Is(err, ErrMetricIntegrity) {
		t.Fatalf("inconsistent cache tokens error=%v", err)
	}
}
