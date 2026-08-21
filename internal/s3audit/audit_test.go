package s3audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekcost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type fakeObserver struct {
	roots       []string
	projections map[string]currentstore.CompositeFamilyUsageProjectionV1
	terminals   map[string]currentstore.TerminalRunResult
	prices      map[string]currentstore.ModelPriceSnapshotRecord
	lifecycles  map[string]currentstore.FairRunTargetView
	unknowns    map[string]string
}

func (observer fakeObserver) ListCompositeRootRunIDs(context.Context) ([]string, error) {
	return append([]string(nil), observer.roots...), nil
}

func (observer fakeObserver) GetCompositeFamilyUsageProjection(
	_ context.Context,
	rootRunID string,
) (currentstore.CompositeFamilyUsageProjectionV1, error) {
	projection, found := observer.projections[rootRunID]
	if !found {
		return currentstore.CompositeFamilyUsageProjectionV1{}, errors.New("missing")
	}
	return projection, nil
}

func (observer fakeObserver) GetTerminalRunResult(
	_ context.Context,
	runID string,
) (currentstore.TerminalRunResult, error) {
	terminal, found := observer.terminals[runID]
	if !found {
		return currentstore.TerminalRunResult{}, errors.New("not terminal")
	}
	return terminal, nil
}

func (observer fakeObserver) GetModelPriceSnapshot(
	_ context.Context,
	priceSnapshotID string,
) (currentstore.ModelPriceSnapshotRecord, error) {
	price, found := observer.prices[priceSnapshotID]
	if !found {
		return currentstore.ModelPriceSnapshotRecord{}, errors.New("missing")
	}
	return price, nil
}

func (observer fakeObserver) GetFairRunTargetView(
	_ context.Context,
	runID string,
) (currentstore.FairRunTargetView, error) {
	lifecycle, found := observer.lifecycles[runID]
	if !found {
		return currentstore.FairRunTargetView{}, errors.New("missing")
	}
	return lifecycle, nil
}

func (observer fakeObserver) GetModelUnknownReason(
	_ context.Context,
	attemptID string,
) (string, error) {
	reason, found := observer.unknowns[attemptID]
	if !found {
		return "", errors.New("missing")
	}
	return reason, nil
}

func TestAuditObserverProducesAggregateOnlyEvidence(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	report := newReport(Expectations{
		Families: 3, Runs: 12, Attempts: 12,
		RequireAllTerminal: true, RequireAllSucceeded: true,
	})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	report.CurrentStoreVerified = true
	report.NoSidecarsVerified = true
	report.SourceUnchanged = true
	report.applyExpectations()
	report = report.finish()
	if report.Status != "PASS" || len(report.FailureCodes) != 0 {
		t.Fatalf("report status=%q failures=%v", report.Status, report.FailureCodes)
	}
	observed := report.Observed
	if observed.Families != 3 || observed.Runs != 12 ||
		observed.TerminalRuns != 12 || observed.VerifiedResults != 12 ||
		observed.Attempts != 12 || observed.UsageRows != 12 ||
		observed.Roles["CHILD"] != 9 || observed.Roles["ROOT"] != 3 ||
		observed.AttemptStates["SUCCEEDED"] != 12 ||
		observed.Providers[deepseekcost.ProviderDeepSeekV1] != 12 ||
		observed.Models["deepseek-v4-flash"] != 12 {
		t.Fatalf("observed counts=%+v", observed)
	}
	if observed.UsageRowsWithAnyTokenFact != 12 ||
		observed.UsageRowsWithoutTokenFacts != 0 ||
		len(observed.ModelUnknownReasons) != 8 {
		t.Fatalf("coverage=%+v reasons=%v", observed, observed.ModelUnknownReasons)
	}
	if observed.TokenFieldCoverage.Input.KnownRows != 12 ||
		observed.TokenFieldCoverage.Input.UnknownRows != 0 ||
		observed.TokenFieldCoverage.Reasoning.KnownRows != 12 ||
		observed.TokenFieldCoverage.Reasoning.UnknownRows != 0 {
		t.Fatalf("token field coverage=%+v", observed.TokenFieldCoverage)
	}
	if uintValue(observed.Tokens.Input) != 120 ||
		uintValue(observed.Tokens.CachedInput) != 48 ||
		uintValue(observed.Tokens.UncachedInput) != 72 ||
		uintValue(observed.Tokens.Output) != 24 ||
		uintValue(observed.Tokens.Reasoning) != 12 ||
		observed.CacheHitRatio == nil || *observed.CacheHitRatio != 0.4 {
		t.Fatalf("token evidence=%+v ratio=%v", observed.Tokens, observed.CacheHitRatio)
	}
	if uintValue(observed.KnownTokenSubtotal.Input) != 120 ||
		uintValue(observed.KnownTokenSubtotal.Reasoning) != 12 {
		t.Fatalf("known token subtotal=%+v", observed.KnownTokenSubtotal)
	}
	child := observed.RoleCache[string(corecontract.CompositeRunRoleChildV1)]
	reviewer := observed.RoleCache[string(corecontract.CompositeRunRoleReviewerV1)]
	root := observed.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
	if len(observed.RoleCache) != 3 || child.Attempts != 9 ||
		uintValue(child.Tokens.Input) != 90 ||
		uintValue(child.Tokens.CachedInput) != 36 ||
		uintValue(child.Tokens.UncachedInput) != 54 ||
		uintValue(child.Tokens.Output) != 18 ||
		uintValue(child.Tokens.Reasoning) != 9 ||
		child.CachedInputPositiveRows != 9 ||
		child.CachedInputZeroRows != 0 ||
		child.CachedInputUnknownRows != 0 ||
		child.CacheHitRatio == nil || *child.CacheHitRatio != 0.4 ||
		root.Attempts != 3 || uintValue(root.Tokens.CachedInput) != 12 ||
		uintValue(root.Tokens.UncachedInput) != 18 ||
		root.CacheHitRatio == nil || *root.CacheHitRatio != 0.4 ||
		reviewer.Attempts != 0 || !tokenTotalsAllNil(reviewer.Tokens) ||
		!tokenTotalsAllNil(reviewer.KnownTokenSubtotal) ||
		reviewer.CacheHitRatio != nil {
		t.Fatalf("role cache=%+v", observed.RoleCache)
	}
	assertCost(t, observed.EstimatedCost, "1.2")
	assertCost(t, observed.ProviderReportedCost, "2.4")
	if observed.ReconciledCost.Status != "UNKNOWN" {
		t.Fatalf("reconciled cost=%+v", observed.ReconciledCost)
	}
	assertCost(t, observed.DerivedDeepSeekCost, "0.00012096")

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{
		"root-private-", "run-private-", "attempt-private-",
		"secret-response", "request_digest", "price_snapshot_digest",
		"raw_receipt", "assistant_text",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("aggregate report leaked forbidden marker %q", forbidden)
		}
	}
}

func TestAuditObserverClassifiesModelUnknownReasonWithoutLeakingRawValue(
	t *testing.T,
) {
	reader := newSuccessfulFakeObserver(t)
	reasons := []string{
		modelUnknownReasonBodyReadIncomplete,
		"private-free-form-reason secret-marker",
	}
	for index, rootRunID := range reader.roots[:2] {
		projection := reader.projections[rootRunID]
		run := projection.Runs[0]
		run.Attempt.State = corecontract.ModelAttemptUnknown
		run.Attempt.Usage.ReconciliationStatus = "PENDING_RECONCILIATION"
		projection.Runs[0] = run
		reader.projections[rootRunID] = projection
		delete(reader.terminals, run.RunID)
		lifecycle := reader.lifecycles[run.RunID]
		lifecycle.Disposition = corecontract.WaitingReconciliationLoopStep
		reader.lifecycles[run.RunID] = lifecycle
		reader.unknowns[run.Attempt.AttemptID] = reasons[index]
	}

	report := newReport(Expectations{Families: 3, Runs: 12, Attempts: 12})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	counts := report.Observed.ModelUnknownReasons
	if counts[modelUnknownReasonBodyReadIncomplete] != 1 ||
		counts[modelUnknownReasonOther] != 1 ||
		len(counts) != 8 {
		t.Fatalf("MODEL_UNKNOWN reason counts=%v", counts)
	}
	if report.Observed.UsageStatuses["PENDING_RECONCILIATION"] != 2 {
		t.Fatalf("Usage statuses=%v", report.Observed.UsageStatuses)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), reasons[1]) ||
		strings.Contains(string(encoded), "secret-marker") {
		t.Fatalf("aggregate report leaked raw MODEL_UNKNOWN reason: %s", encoded)
	}
}

func TestAuditObserverRetainsPartialUsageAndDerivedCostCoverage(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	firstRoot := reader.roots[0]
	projection := reader.projections[firstRoot]
	projection.Runs[0].Attempt.Usage.Tokens.Reasoning = nil
	projection.Runs[1].Attempt.Usage.Tokens = corecontract.UsageTokens{}
	reader.projections[firstRoot] = projection

	report := newReport(Expectations{Families: 3, Runs: 12, Attempts: 12})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	observed := report.Observed
	if observed.UsageRowsWithAnyTokenFact != 11 ||
		observed.UsageRowsWithoutTokenFacts != 1 {
		t.Fatalf(
			"Usage rows with/without token facts=%d/%d",
			observed.UsageRowsWithAnyTokenFact,
			observed.UsageRowsWithoutTokenFacts,
		)
	}
	coverage := observed.TokenFieldCoverage
	if coverage.Input.KnownRows != 11 || coverage.Input.UnknownRows != 1 ||
		coverage.CachedInput.KnownRows != 11 ||
		coverage.CachedInput.UnknownRows != 1 ||
		coverage.UncachedInput.KnownRows != 11 ||
		coverage.UncachedInput.UnknownRows != 1 ||
		coverage.Output.KnownRows != 11 || coverage.Output.UnknownRows != 1 ||
		coverage.Reasoning.KnownRows != 10 ||
		coverage.Reasoning.UnknownRows != 2 {
		t.Fatalf("token field coverage=%+v", coverage)
	}
	if observed.Tokens.Input != nil || observed.Tokens.Reasoning != nil ||
		uintValue(observed.KnownTokenSubtotal.Input) != 110 ||
		uintValue(observed.KnownTokenSubtotal.CachedInput) != 44 ||
		uintValue(observed.KnownTokenSubtotal.UncachedInput) != 66 ||
		uintValue(observed.KnownTokenSubtotal.Output) != 22 ||
		uintValue(observed.KnownTokenSubtotal.Reasoning) != 10 {
		t.Fatalf(
			"totals=%+v known subtotal=%+v",
			observed.Tokens,
			observed.KnownTokenSubtotal,
		)
	}
	child := observed.RoleCache[string(corecontract.CompositeRunRoleChildV1)]
	root := observed.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
	if child.Attempts != 9 || child.Tokens.Input != nil ||
		child.Tokens.CachedInput != nil ||
		child.Tokens.UncachedInput != nil || child.CacheHitRatio != nil ||
		uintValue(child.KnownTokenSubtotal.Input) != 80 ||
		uintValue(child.KnownTokenSubtotal.CachedInput) != 32 ||
		uintValue(child.KnownTokenSubtotal.UncachedInput) != 48 ||
		uintValue(child.KnownTokenSubtotal.Output) != 16 ||
		uintValue(child.KnownTokenSubtotal.Reasoning) != 7 ||
		child.TokenFieldCoverage.CachedInput.KnownRows != 8 ||
		child.TokenFieldCoverage.CachedInput.UnknownRows != 1 ||
		child.CachedInputPositiveRows != 8 ||
		child.CachedInputUnknownRows != 1 ||
		root.Attempts != 3 || root.CacheHitRatio == nil ||
		*root.CacheHitRatio != 0.4 {
		t.Fatalf("partial role cache=%+v", observed.RoleCache)
	}
	derived := observed.DerivedDeepSeekCost
	if derived.Status != "UNKNOWN" || derived.Value != nil ||
		derived.KnownEstimates != 11 || derived.UnknownEstimates != 1 ||
		derived.KnownSubtotal.Status != "KNOWN" ||
		derived.KnownSubtotal.Value == nil ||
		*derived.KnownSubtotal.Value != "0.00011088" ||
		derived.KnownSubtotal.Currency != "CNY" {
		t.Fatalf("derived cost coverage=%+v", derived)
	}
}

func TestAuditObserverKeepsReviewerRunSeparateFromReviewerAttempts(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	rootRunID := reader.roots[0]
	projection := reader.projections[rootRunID]
	reviewer := projection.Runs[0]
	reviewer.Role = corecontract.CompositeRunRoleReviewerV1
	reviewer.Attempt = nil
	projection.Runs[0] = reviewer
	reader.projections[rootRunID] = projection
	delete(reader.terminals, reviewer.RunID)
	lifecycle := reader.lifecycles[reviewer.RunID]
	lifecycle.Disposition = corecontract.InitialLoopStep
	reader.lifecycles[reviewer.RunID] = lifecycle

	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	observed := report.Observed
	reviewerCache := observed.RoleCache[string(
		corecontract.CompositeRunRoleReviewerV1,
	)]
	if observed.Roles[string(corecontract.CompositeRunRoleReviewerV1)] != 1 ||
		reviewerCache.Attempts != 0 ||
		!tokenTotalsAllNil(reviewerCache.Tokens) ||
		!tokenTotalsAllNil(reviewerCache.KnownTokenSubtotal) ||
		reviewerCache.CacheHitRatio != nil || observed.Attempts != 11 {
		t.Fatalf("observed=%+v", observed)
	}
}

func TestAuditObserverRoleCacheRatioIgnoresNonCacheUnknowns(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	rootRunID := reader.roots[0]
	projection := reader.projections[rootRunID]
	projection.Runs[0].Attempt.Usage.Tokens.Output = nil
	projection.Runs[0].Attempt.Usage.Tokens.Reasoning = nil
	reader.projections[rootRunID] = projection

	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	child := report.Observed.RoleCache[string(corecontract.CompositeRunRoleChildV1)]
	if child.Tokens.Output != nil || child.Tokens.Reasoning != nil ||
		child.Tokens.CachedInput == nil || child.Tokens.UncachedInput == nil ||
		child.CacheHitRatio == nil || *child.CacheHitRatio != 0.4 {
		t.Fatalf("child role cache=%+v", child)
	}
}

func TestAuditObserverRoleCacheDistinguishesKnownZeroFromNoAttempt(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	for _, rootRunID := range reader.roots {
		projection := reader.projections[rootRunID]
		for index := range projection.Runs {
			if projection.Runs[index].Role !=
				corecontract.CompositeRunRoleRootV1 {
				continue
			}
			zero, ten := uint64(0), uint64(10)
			projection.Runs[index].Attempt.Usage.Tokens.CachedInput = &zero
			projection.Runs[index].Attempt.Usage.Tokens.UncachedInput = &ten
		}
		reader.projections[rootRunID] = projection
	}

	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	root := report.Observed.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
	reviewer := report.Observed.RoleCache[string(
		corecontract.CompositeRunRoleReviewerV1,
	)]
	if root.Attempts != 3 || root.Tokens.CachedInput == nil ||
		*root.Tokens.CachedInput != 0 ||
		uintValue(root.Tokens.UncachedInput) != 30 ||
		root.KnownTokenSubtotal.CachedInput == nil ||
		*root.KnownTokenSubtotal.CachedInput != 0 ||
		root.CachedInputPositiveRows != 0 ||
		root.CachedInputZeroRows != 3 ||
		root.CachedInputUnknownRows != 0 ||
		root.CacheHitRatio == nil || *root.CacheHitRatio != 0 ||
		reviewer.Attempts != 0 || reviewer.Tokens.CachedInput != nil ||
		reviewer.KnownTokenSubtotal.CachedInput != nil ||
		reviewer.CacheHitRatio != nil {
		t.Fatalf("role cache=%+v", report.Observed.RoleCache)
	}
}

func TestRoleCacheValidatorRejectsSubtotalTamper(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatal(err)
	}
	childRole := string(corecontract.CompositeRunRoleChildV1)
	child := report.Observed.RoleCache[childRole]
	tampered := uint64(37)
	child.KnownTokenSubtotal.CachedInput = &tampered
	report.Observed.RoleCache[childRole] = child
	if validRoleCacheCoverage(
		report.Observed.RoleCache,
		report.Observed.Attempts,
		report.Observed.UsageRowsWithAnyTokenFact,
		report.Observed.UsageRowsWithoutTokenFacts,
		report.Observed.TokenFieldCoverage,
		report.Observed.KnownTokenSubtotal,
		report.Observed.Roles,
	) {
		t.Fatal("role cache validator accepted a tampered subtotal")
	}
}

func TestAuditObserverRejectsInvalidRoleWithoutLeakingIt(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	rootRunID := reader.roots[0]
	projection := reader.projections[rootRunID]
	projection.Runs[0].Role = corecontract.CompositeRunRoleV1(
		"private-role-secret-marker",
	)
	reader.projections[rootRunID] = projection

	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); !errors.Is(
		err,
		ErrAuditFailed,
	) {
		t.Fatalf("invalid role error=%v", err)
	}
	if !hasCode(report.FailureCodes, "ROLE_CACHE_ROLE_INVALID") {
		t.Fatalf("failure codes=%v", report.FailureCodes)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-role-secret-marker") {
		t.Fatalf("invalid role leaked: %s", encoded)
	}
}

func TestAuditObserverRejectsCrossFamilyTokenOverflow(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	first := reader.projections[reader.roots[0]]
	second := reader.projections[reader.roots[1]]
	maximum := uint64(^uint64(0))
	one := uint64(1)
	first.Runs[0].Attempt.Usage.Tokens.Input = &maximum
	second.Runs[0].Attempt.Usage.Tokens.Input = &one
	reader.projections[reader.roots[0]] = first
	reader.projections[reader.roots[1]] = second

	report := newReport(Expectations{})
	if err := auditObserver(context.Background(), reader, &report); !errors.Is(
		err,
		ErrAuditFailed,
	) {
		t.Fatalf("overflow error=%v", err)
	}
	if !hasCode(report.FailureCodes, "TOKEN_TOTAL_OVERFLOW") ||
		report.Observed.Tokens.Input != nil ||
		report.Observed.KnownTokenSubtotal.Input != nil ||
		report.Observed.UsageRowsWithAnyTokenFact != 12 ||
		report.Observed.UsageRowsWithoutTokenFacts != 0 ||
		report.Observed.TokenFieldCoverage.Input.KnownRows != 12 {
		t.Fatalf("overflow report=%+v", report)
	}
}

func TestImmutableReasonObserverReadsOnlyTheUnknownReasonColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reason-observer.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE model_dispatch_attempts (
			attempt_id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			unknown_reason TEXT
		);
		INSERT INTO model_dispatch_attempts (
			attempt_id, state, unknown_reason
		) VALUES ('attempt-private', 'MODEL_UNKNOWN', 'MODEL_UNKNOWN');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reasonDatabase, err := openImmutableReasonDatabase(
		context.Background(),
		path,
	)
	if err != nil {
		t.Fatal(err)
	}
	observer := &closedStoreObserver{reasonDatabase: reasonDatabase}
	reason, err := observer.GetModelUnknownReason(
		context.Background(),
		"attempt-private",
	)
	if err != nil || reason != modelUnknownReasonModelUnknown {
		_ = observer.Close()
		t.Fatalf("reason=%q error=%v", reason, err)
	}
	if _, err := reasonDatabase.Exec(`
		INSERT INTO model_dispatch_attempts (
			attempt_id, state, unknown_reason
		) VALUES ('write-forbidden', 'MODEL_UNKNOWN', 'MODEL_UNKNOWN')
	`); err == nil {
		_ = observer.Close()
		t.Fatal("immutable reason observer accepted a write")
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageValidatorsRejectOverflowAndNonAllowlistedReasonKey(t *testing.T) {
	maximum := uint64(^uint64(0))
	if validRowCoverage(maximum, 1, maximum) ||
		validCostCoverage(CostTotal{
			KnownEstimates:   maximum,
			UnknownEstimates: 1,
		}, maximum) {
		t.Fatal("coverage validation accepted an overflowing partition")
	}
	reasons := newModelUnknownReasonCounts()
	reasons["private-free-form-reason"] = 1
	if validModelUnknownReasonCoverage(reasons, 1) {
		t.Fatal("MODEL_UNKNOWN coverage accepted a non-allowlisted key")
	}
	ratio := cacheHitRatio(TokenTotals{
		CachedInput:   &maximum,
		UncachedInput: &maximum,
	})
	if ratio == nil || *ratio != 0.5 {
		t.Fatalf("overflow-safe cache ratio=%v", ratio)
	}
}

func TestAuditObserverRetainsUnknownAndNonterminalFailure(t *testing.T) {
	reader := newSuccessfulFakeObserver(t)
	firstRoot := reader.roots[0]
	projection := reader.projections[firstRoot]
	first := projection.Runs[0]
	first.Attempt.State = corecontract.ModelAttemptUnknown
	first.Attempt.Usage.ReconciliationStatus = "PENDING_RECONCILIATION"
	projection.Runs[0] = first
	reader.projections[firstRoot] = projection
	reader.unknowns[first.Attempt.AttemptID] = modelUnknownReasonModelUnknown
	delete(reader.terminals, first.RunID)
	lifecycle := reader.lifecycles[first.RunID]
	lifecycle.Disposition = corecontract.WaitingReconciliationLoopStep
	reader.lifecycles[first.RunID] = lifecycle

	report := newReport(Expectations{
		Families: 3, Runs: 12, Attempts: 12,
		RequireAllTerminal: true, RequireAllSucceeded: true,
	})
	if err := auditObserver(context.Background(), reader, &report); err != nil {
		t.Fatalf("legitimate nonterminal observation failed: %v", err)
	}
	report.CurrentStoreVerified = true
	report.NoSidecarsVerified = true
	report.SourceUnchanged = true
	report.applyExpectations()
	report = report.finish()
	if report.Status != "FAIL" || report.Observed.NonterminalRuns != 1 ||
		report.Observed.InvalidRunClosures != 0 ||
		!hasCode(report.FailureCodes, "NOT_ALL_RUNS_TERMINAL") ||
		!hasCode(report.FailureCodes, "NOT_ALL_ATTEMPTS_SUCCEEDED") {
		t.Fatalf("nonterminal report=%+v", report)
	}
}

func TestFiniteDecimalIsExact(t *testing.T) {
	value, ok := finiteDecimal(new(big.Rat).SetFrac64(151, 100))
	if !ok || value != "1.51" {
		t.Fatalf("finite decimal=%q ok=%v", value, ok)
	}
}

func newSuccessfulFakeObserver(t *testing.T) fakeObserver {
	t.Helper()
	price, canonical, err := corecontract.NewModelPriceSnapshotV1(
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "price-private-flash",
			Provider:        deepseekcost.ProviderDeepSeekV1,
			Model:           "deepseek-v4-flash",
			BillingVersion:  "test-v1",
			Currency:        "CNY",
			PricingStatus:   corecontract.PricingKnown,
			Pricing: json.RawMessage(
				`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := fakeObserver{
		projections: make(map[string]currentstore.CompositeFamilyUsageProjectionV1),
		terminals:   make(map[string]currentstore.TerminalRunResult),
		prices: map[string]currentstore.ModelPriceSnapshotRecord{
			price.PriceSnapshotID: {Snapshot: price, CanonicalJSON: canonical},
		},
		lifecycles: make(map[string]currentstore.FairRunTargetView),
		unknowns:   make(map[string]string),
	}
	for family := 0; family < 3; family++ {
		rootRunID := fmt.Sprintf("root-private-%d", family)
		reader.roots = append(reader.roots, rootRunID)
		projection := currentstore.CompositeFamilyUsageProjectionV1{
			RootRunID: rootRunID,
			Runs:      make([]currentstore.CompositeFamilyRunUsageFactV1, 0, 4),
		}
		for runIndex := 0; runIndex < 4; runIndex++ {
			runID := fmt.Sprintf("run-private-%d-%d", family, runIndex)
			attemptID := fmt.Sprintf("attempt-private-%d-%d", family, runIndex)
			role := corecontract.CompositeRunRoleChildV1
			if runIndex == 3 {
				role = corecontract.CompositeRunRoleRootV1
				runID = rootRunID
			}
			input, cached, uncached, output, reasoning :=
				uint64(10), uint64(4), uint64(6), uint64(2), uint64(1)
			estimated, providerCost := "0.1", "0.2"
			attempt := &currentstore.CompositeFamilyAttemptUsageFactV1{
				AttemptID:           attemptID,
				State:               corecontract.ModelAttemptSucceeded,
				Provider:            deepseekcost.ProviderDeepSeekV1,
				Model:               "deepseek-v4-flash",
				CreatedAt:           time.Unix(int64(family*4+runIndex+1), 0),
				UpdatedAt:           time.Unix(int64(family*4+runIndex+2), 0),
				PriceSnapshotID:     price.PriceSnapshotID,
				PriceSnapshotDigest: price.Digest,
				Currency:            price.Currency,
				Usage: currentstore.ModelUsageRecord{
					AttemptID: attemptID,
					RunID:     runID,
					Tokens: corecontract.UsageTokens{
						Input: &input, CachedInput: &cached, UncachedInput: &uncached,
						Output: &output, Reasoning: &reasoning,
					},
					EstimatedCost:        &estimated,
					ProviderReportedCost: &providerCost,
					ReconciliationStatus: "PROVIDER_REPORTED",
				},
			}
			projection.Runs = append(
				projection.Runs,
				currentstore.CompositeFamilyRunUsageFactV1{
					RunID: runID, Role: role, Attempt: attempt,
				},
			)
			reader.terminals[runID] = currentstore.TerminalRunResult{
				RunID:           runID,
				FrameRevision:   2,
				ReasonCode:      "MODEL_SUCCEEDED",
				AttemptID:       attemptID,
				AttemptKind:     corecontract.AttemptKindModel,
				State:           corecontract.ModelAttemptSucceeded,
				ModelState:      corecontract.ModelAttemptSucceeded,
				Output:          moduleapi.ModelGenerateOutputV1{AssistantText: "secret-response"},
				OutputCanonical: []byte(`{"assistant_text":"secret-response"}`),
			}
			reader.lifecycles[runID] = currentstore.FairRunTargetView{
				RunID: runID, Disposition: corecontract.TerminatedLoopStep,
			}
		}
		reader.projections[rootRunID] = projection
	}
	return reader
}

func uintValue(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func tokenTotalsAllNil(totals TokenTotals) bool {
	return totals.Input == nil && totals.CachedInput == nil &&
		totals.UncachedInput == nil && totals.Output == nil &&
		totals.Reasoning == nil
}

func assertCost(t *testing.T, cost CostTotal, want string) {
	t.Helper()
	if cost.Status != "KNOWN" || cost.Value == nil || *cost.Value != want ||
		cost.Currency != "CNY" || len(cost.Currencies) != 0 ||
		cost.KnownEstimates != 12 || cost.UnknownEstimates != 0 ||
		cost.KnownSubtotal.Status != "KNOWN" ||
		cost.KnownSubtotal.Value == nil ||
		*cost.KnownSubtotal.Value != want ||
		cost.KnownSubtotal.Currency != "CNY" {
		t.Fatalf("cost=%+v want %q CNY", cost, want)
	}
}

func hasCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}
