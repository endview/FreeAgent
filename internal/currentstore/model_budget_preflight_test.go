package currentstore

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestValidateModelParametersForRunRequiresExplicitBoundedMaxTokens(
	t *testing.T,
) {
	fixture, lease, _ := newModelDispatchFixture(t)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		parameters []byte
		want       uint64
		wantError  string
	}{
		{name: "missing", parameters: []byte(`{"temperature":0}`), wantError: "max_tokens must be explicit"},
		{name: "below reserve", parameters: []byte(`{"max_tokens":63}`), want: 63},
		{name: "equal to reserve", parameters: []byte(`{"max_tokens":64}`), want: 64},
		{name: "above reserve", parameters: []byte(`{"max_tokens":65}`), wantError: "exceeds frozen output reserve 64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateModelParametersForRun(run, test.parameters)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("validation error=%v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("validated max_tokens=%d error=%v", got, err)
			}
		})
	}
}

func TestStoredModelParametersPreservePreBudgetHistoryWithoutAuthorizingDispatch(
	t *testing.T,
) {
	fixture, lease, _ := newModelDispatchFixture(t)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{}`)
	stored, err := storedModelParametersForRun(run, legacy)
	if err != nil || !bytes.Equal(stored, legacy) {
		t.Fatalf("stored legacy parameters=%s error=%v", stored, err)
	}
	if _, err := expectedModelParametersForRun(run, legacy); err == nil ||
		!strings.Contains(err.Error(), "max_tokens must be explicit") {
		t.Fatalf("legacy dispatch validation error=%v", err)
	}
}

func TestValidateModelParametersForRunUsesEffectiveModelProfilePolicy(
	t *testing.T,
) {
	fixture, lease, _ := newModelDispatchFixtureWithModelProfile(t, 100)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := frozenEffectiveContextPolicyForRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if policy.ContextWindowTokens != 100 || policy.ReservedOutputTokens != 64 {
		t.Fatalf("effective ContextPolicy=%+v", policy)
	}
	if _, err := validateModelParametersForRun(
		run,
		[]byte(`{"max_tokens":65}`),
	); err == nil || !strings.Contains(err.Error(), "output reserve 64") {
		t.Fatalf("profile-tightened validation error=%v", err)
	}
}

func TestExpectedReviewerParametersTightenBeforeReserveValidation(
	t *testing.T,
) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	lease := acquireCompositeTestLease(
		t,
		fixture.store,
		fixture.compiled.Reviewer.RunManifest.RunID,
		"reviewer-parameter-test",
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	run.CompositeRoot.Composite.Plan.Reviewer.MaxOutputTokens = 32
	parameters, err := expectedModelParametersForRun(
		run,
		[]byte(`{"max_tokens":128,"temperature":0}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	maximum, present, err := modelParametersMaxTokens(parameters)
	if err != nil || !present || maximum != 32 {
		t.Fatalf("tightened Reviewer parameters=%s max=%d present=%v error=%v", parameters, maximum, present, err)
	}
}

func TestBeginModelDispatchPersistsExactEffectiveParameters(t *testing.T) {
	fixture, _, input := newModelDispatchFixture(t)
	request, err := moduleapi.RestoreModelGenerateRequestV1(input.RequestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(begin.Attempt.ParametersCanonical, request.Parameters) ||
		!bytes.Equal(stored.Attempt.ParametersCanonical, request.Parameters) {
		t.Fatalf(
			"effective parameters request=%s begin=%s stored=%s",
			request.Parameters,
			begin.Attempt.ParametersCanonical,
			stored.Attempt.ParametersCanonical,
		)
	}
}
