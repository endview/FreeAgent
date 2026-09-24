package s3cellaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/s3audit"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/loopapi"
)

func TestAuditCellPassesAndEmitsAggregateOnlyEvidence(t *testing.T) {
	privateMarker := "sk-" + strings.Repeat("a", 32)
	cell := writeSyntheticCell(t, syntheticSourceReport(privateMarker), "COMPLETE", "")

	report, err := auditSyntheticCell(cell)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != ReportSchemaVersionV3 || report.Status != "PASS" ||
		!report.Archive.ExitComplete ||
		!report.Archive.ReportSHA256Verified || !report.Archive.ArchiveVerified ||
		!report.Archive.BundleVerified || !report.Archive.BundleUnchanged ||
		!report.Archive.StoreAuditVerified || !report.Archive.RoleCacheCrossChecked ||
		!report.Archive.SourceUnchanged {
		t.Fatalf("archive=%+v status=%q failures=%v", report.Archive, report.Status, report.FailureCodes)
	}
	if report.Experiment.RepetitionsRequested != 1 ||
		report.Experiment.RepetitionsAttempted != 1 ||
		report.Counts.Families != 3 || report.Counts.Attempts != 3 ||
		report.Counts.Results != 3 || report.Models["deepseek-v4-flash"] != 3 ||
		report.AttemptRoles["ROOT"] != 3 || report.AttemptStates["SUCCEEDED"] != 3 {
		t.Fatalf("unexpected aggregate report: %+v", report)
	}
	if report.Tokens.Totals.Input == nil || *report.Tokens.Totals.Input != 300 ||
		report.Tokens.Totals.CachedInput == nil || *report.Tokens.Totals.CachedInput != 75 ||
		report.Tokens.CacheHitRatio == nil || *report.Tokens.CacheHitRatio != 0.25 ||
		report.Tokens.KnownUsageAttempts != 3 ||
		report.Tokens.CachedInputPositiveAttempts != 3 ||
		report.Tokens.PerAttempt.Input.Count != 3 ||
		report.Tokens.PerAttempt.Input.Min == nil || *report.Tokens.PerAttempt.Input.Min != 100 ||
		report.Tokens.PerAttempt.CachedInput.Mean == nil ||
		*report.Tokens.PerAttempt.CachedInput.Mean != 25 {
		t.Fatalf("tokens=%+v", report.Tokens)
	}
	rootCache := report.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
	if len(report.RoleCache) != 3 || rootCache.Attempts != 3 ||
		rootCache.Tokens.Input == nil || *rootCache.Tokens.Input != 300 ||
		rootCache.CacheHitRatio == nil || *rootCache.CacheHitRatio != 0.25 ||
		report.RoleCache[string(corecontract.CompositeRunRoleChildV1)].Attempts != 0 ||
		report.RoleCache[string(corecontract.CompositeRunRoleReviewerV1)].Attempts != 0 {
		t.Fatalf("role cache=%+v", report.RoleCache)
	}
	if report.Fairness.MaxPrefixImbalance.Max == nil ||
		*report.Fairness.MaxPrefixImbalance.Max != 1 ||
		report.Fairness.PrefixGapArea.Total == nil ||
		*report.Fairness.PrefixGapArea.Total != 2 ||
		report.Fairness.LatestFirstServiceOrder.Max == nil ||
		*report.Fairness.LatestFirstServiceOrder.Max != 3 {
		t.Fatalf("fairness=%+v", report.Fairness)
	}

	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	output := string(payload)
	for _, forbidden := range []string{
		privateMarker,
		"prompt containing",
		"reply containing",
		"result containing",
		"request_id",
		"run_id",
		"attempt_id",
		"digest",
		"receipt",
		"header",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("aggregate output exposed forbidden source data %q: %s", forbidden, output)
		}
	}
}

func TestAuditCellRecomputesAndRejectsDeclaredFairnessMismatch(t *testing.T) {
	for _, mutate := range []func(*s3eval.FairnessReport){
		func(fairness *s3eval.FairnessReport) {
			wrong := 0.5
			fairness.JainIndex = &wrong
		},
		func(fairness *s3eval.FairnessReport) {
			fairness.Starvation = true
			fairness.StarvedWorkspaces = append(fairness.StarvedWorkspaces, "private-3")
		},
		func(fairness *s3eval.FairnessReport) {
			fairness.LongestConsecutiveCount++
		},
	} {
		source := syntheticSourceReport("private")
		mutate(&source.RepetitionReports[0].Report.Fairness)
		cell := writeSyntheticCell(t, source, "COMPLETE", "")
		report, err := auditSyntheticCell(cell)
		if err == nil || !contains(report.FailureCodes, "FAIRNESS_MISMATCH") {
			t.Fatalf("error=%v report=%+v", err, report)
		}
	}
}

func TestPrefixFairnessMetricsPreserveStarvationAsUnknownLatest(t *testing.T) {
	metrics, ok := computePrefixFairness(
		[]string{"workspace-a", "workspace-b", "workspace-c"},
		[]string{"workspace-a", "workspace-a", "workspace-b"},
	)
	if !ok || metrics.MaxImbalance != 2 || metrics.GapArea != 5 ||
		metrics.LatestFirstServiceOrder != nil {
		t.Fatalf("prefix metrics=%+v ok=%v", metrics, ok)
	}
}

func TestAuditCellSeparatesCompleteReviewerStages(t *testing.T) {
	source := syntheticSourceReport("private")
	addCompleteReviewers(&source, "private")
	cell := writeSyntheticCell(t, source, "COMPLETE", "")
	report, err := auditSyntheticCell(cell)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.ReviewerRuns != 3 || report.Counts.ReviewerAttempts != 3 ||
		report.Counts.ReviewerResults != 3 || report.Counts.ReviewerVerdicts != 3 ||
		report.Verdicts["APPROVE"] != 3 {
		t.Fatalf("counts=%+v verdicts=%v", report.Counts, report.Verdicts)
	}
	if report.RoleCache[string(corecontract.CompositeRunRoleReviewerV1)].Attempts != 3 ||
		!report.Archive.RoleCacheCrossChecked {
		t.Fatalf("archive=%+v role cache=%+v", report.Archive, report.RoleCache)
	}
}

func TestAuditCellPartialModelUnknownPreservesKnownCoverage(t *testing.T) {
	source := syntheticSourceReport("private")
	repetition := &source.RepetitionReports[0]
	repetition.Error = "MODEL_UNKNOWN"
	source.FirstError = "MODEL_UNKNOWN"
	repetition.Report.Families[0].Reviewer = &s3eval.DispositionFact{
		RunID:       "private-reviewer-run",
		Disposition: loopapi.DispositionWaitingReconciliation,
	}
	repetition.Report.Families[0].Attempts[0].State = corecontract.ModelAttemptUnknown
	repetition.Report.Families[0].Attempts[0].Tokens = corecontract.UsageTokens{}
	repetition.Report.Families[0].Results = nil
	repetition.Report.ServiceOrder[0].State = corecontract.ModelAttemptUnknown
	repetition.Report.ServiceOrder[0].Tokens = corecontract.UsageTokens{}
	cell := writeSyntheticCell(t, source, "PARTIAL", "")

	report, err := auditSyntheticCell(cell)
	if err == nil || report.Status != "FAIL" ||
		!contains(report.FailureCodes, "EXIT_NOT_COMPLETE") {
		t.Fatalf("error=%v report=%+v", err, report)
	}
	if report.Counts.ReviewerRuns != 1 || report.Counts.ReviewerAttempts != 0 ||
		report.Counts.ReviewerResults != 0 || report.Counts.ReviewerVerdicts != 0 {
		t.Fatalf("reviewer stage counts=%+v", report.Counts)
	}
	rootCache := report.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
	if !report.Archive.StoreAuditVerified || !report.Archive.RoleCacheCrossChecked ||
		rootCache.Attempts != 3 || rootCache.Tokens.Input != nil ||
		rootCache.KnownTokenSubtotal.Input == nil ||
		*rootCache.KnownTokenSubtotal.Input != 200 ||
		rootCache.CachedInputUnknownRows != 1 || rootCache.CacheHitRatio != nil {
		t.Fatalf("archive=%+v role cache=%+v", report.Archive, report.RoleCache)
	}
	if report.Tokens.Totals.Input != nil || report.Tokens.KnownSubtotals.Input == nil ||
		*report.Tokens.KnownSubtotals.Input != 200 ||
		report.Tokens.Coverage.Input.Known != 2 || report.Tokens.Coverage.Input.Unknown != 1 ||
		report.Tokens.KnownUsageAttempts != 2 ||
		report.Tokens.CachedInputPositiveAttempts != 2 ||
		report.Tokens.PerAttempt.Input.Count != 2 {
		t.Fatalf("tokens=%+v", report.Tokens)
	}
}

func TestAuditCellPreAdmissionPartialCanCrossCheckAnEmptyStore(t *testing.T) {
	source := syntheticSourceReport("private")
	repetition := &source.RepetitionReports[0]
	source.FirstError = "ADMISSION_STOPPED"
	repetition.Error = "ADMISSION_STOPPED"
	for index := range repetition.Report.Families {
		family := &repetition.Report.Families[index]
		family.RootRunID = ""
		family.Root = s3eval.DispositionFact{}
		family.Children = []s3eval.DispositionFact{}
		family.Reviewer = nil
		family.Attempts = []s3eval.AttemptFact{}
		family.Results = []s3eval.ResultFact{}
	}
	repetition.Report.ServiceOrder = []s3eval.AttemptFact{}
	workspaceIDs := make([]string, 0, len(source.Scenario.Tasks))
	for _, task := range source.Scenario.Tasks {
		workspaceIDs = append(workspaceIDs, task.WorkspaceID)
	}
	repetition.Report.Fairness, _ = s3eval.ComputeFairness(workspaceIDs, nil)
	cell := writeSyntheticCell(t, source, "PARTIAL", "")

	report, err := auditCellWithDependencies(
		context.Background(), cell,
		func(context.Context, string) (currentbackup.Manifest, error) {
			return currentbackup.Manifest{
				Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
				ManifestDigest: "stable",
			}, nil
		},
		func(
			_ context.Context,
			_ string,
			expectations s3audit.Expectations,
		) (s3audit.Report, error) {
			if expectations != (s3audit.Expectations{}) {
				return s3audit.Report{}, errors.New("PARTIAL must not assert a complete Store inventory")
			}
			return s3audit.Report{
				SchemaVersion:        s3audit.ReportSchemaVersionV3,
				Status:               "PASS",
				CurrentStoreVerified: true,
				NoSidecarsVerified:   true,
				SourceUnchanged:      true,
				Observed: s3audit.ObservedReport{
					RoleCache: map[string]s3audit.RoleCacheObservation{
						string(corecontract.CompositeRunRoleChildV1):    {},
						string(corecontract.CompositeRunRoleReviewerV1): {},
						string(corecontract.CompositeRunRoleRootV1):     {},
					},
				},
				FailureCodes: []string{},
			}, nil
		},
	)
	if err == nil || report.Status != "FAIL" ||
		!contains(report.FailureCodes, "EXIT_NOT_COMPLETE") ||
		contains(report.FailureCodes, "ARCHIVE_STORE_AUDIT_FAILED") ||
		contains(report.FailureCodes, "ARCHIVE_ROLE_CACHE_MISMATCH") ||
		!report.Archive.StoreAuditVerified || !report.Archive.RoleCacheCrossChecked {
		t.Fatalf("error=%v archive=%+v failures=%v", err, report.Archive, report.FailureCodes)
	}
}

func TestAggregateOverflowNeverBecomesCompleteTotal(t *testing.T) {
	maximum := uint64(math.MaxUint64)
	one := uint64(1)
	tokens := newTokenAccumulator()
	if !tokens.add(corecontract.UsageTokens{Input: &maximum}) ||
		tokens.add(corecontract.UsageTokens{Input: &one}) {
		t.Fatal("token overflow was not detected")
	}
	tokenReport := tokens.report()
	if tokenReport.Totals.Input != nil || tokenReport.KnownSubtotals.Input != nil ||
		!tokenReport.Coverage.Input.Overflowed {
		t.Fatalf("token report=%+v", tokenReport)
	}

	total := uint64(math.MaxUint64)
	if addUint(&total, 1) {
		t.Fatal("prefix/aggregate uint overflow was not detected")
	}
}

func TestTokenPerAttemptStatsUseKnownValuesOnly(t *testing.T) {
	inputA, cachedA, uncachedA, outputA := uint64(100), uint64(0), uint64(100), uint64(20)
	inputB, cachedB, uncachedB, outputB := uint64(200), uint64(50), uint64(150), uint64(40)
	tokens := newTokenAccumulator()
	if !tokens.add(corecontract.UsageTokens{
		Input: &inputA, CachedInput: &cachedA, UncachedInput: &uncachedA, Output: &outputA,
	}) || !tokens.add(corecontract.UsageTokens{
		Input: &inputB, CachedInput: &cachedB, UncachedInput: &uncachedB, Output: &outputB,
	}) || !tokens.add(corecontract.UsageTokens{}) {
		t.Fatal("valid token samples failed")
	}
	report := tokens.report()
	if report.KnownUsageAttempts != 2 || report.CachedInputPositiveAttempts != 1 ||
		report.PerAttempt.Input.Count != 2 || report.PerAttempt.Input.Min == nil ||
		*report.PerAttempt.Input.Min != 100 || report.PerAttempt.Input.Max == nil ||
		*report.PerAttempt.Input.Max != 200 || report.PerAttempt.Input.Mean == nil ||
		*report.PerAttempt.Input.Mean != 150 || report.Coverage.Input.Unknown != 1 ||
		report.Totals.Input != nil || report.KnownSubtotals.Input == nil ||
		*report.KnownSubtotals.Input != 300 {
		t.Fatalf("token stats=%+v", report)
	}
}

func TestCacheHitRatioDoesNotBecomeUnknownOnUint64DenominatorOverflow(t *testing.T) {
	maximum := uint64(math.MaxUint64)
	ratio := cacheHitRatio(TokenTotals{CachedInput: &maximum, UncachedInput: &maximum})
	if ratio == nil || *ratio != 0.5 {
		t.Fatalf("ratio=%v", ratio)
	}
}

func TestRoleCacheCoverageRejectsBrokenClassification(t *testing.T) {
	input, cached, uncached, output := uint64(100), uint64(25), uint64(75), uint64(20)
	global := newTokenAccumulator()
	roles := newRoleCacheAccumulators()
	tokens := corecontract.UsageTokens{
		Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
	}
	if !global.add(tokens) ||
		!roles[string(corecontract.CompositeRunRoleRootV1)].add(tokens) {
		t.Fatal("valid role/cache sample failed")
	}
	report := roles.report()
	root := report[string(corecontract.CompositeRunRoleRootV1)]
	root.CachedInputUnknownRows = 1
	report[string(corecontract.CompositeRunRoleRootV1)] = root
	if validRoleCacheCoverage(roles, global, report) {
		t.Fatalf("broken classification accepted: %+v", report)
	}
}

func TestAuditCellRejectsReportSHA256Mismatch(t *testing.T) {
	cell := writeSyntheticCell(
		t,
		syntheticSourceReport("private"),
		"COMPLETE",
		strings.Repeat("0", 64),
	)
	report, err := auditSyntheticCell(cell)
	if err == nil || report.Status != "FAIL" ||
		!contains(report.FailureCodes, "REPORT_SHA256_MISMATCH") ||
		report.Archive.ReportSHA256Verified {
		t.Fatalf("error=%v report=%+v", err, report)
	}
}

func TestAuditCellRejectsBundleMutationOrVerificationFailure(t *testing.T) {
	cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
	calls := 0
	report, err := auditCellWithDependencies(
		context.Background(), cell,
		func(context.Context, string) (currentbackup.Manifest, error) {
			calls++
			return currentbackup.Manifest{
				Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
				ManifestDigest: fmt.Sprintf("version-%d", calls),
			}, nil
		},
		matchingSyntheticStoreAudit(cell),
	)
	if err == nil || !contains(report.FailureCodes, "BUNDLE_CHANGED_DURING_AUDIT") ||
		report.Archive.BundleUnchanged {
		t.Fatalf("error=%v report=%+v", err, report)
	}

	storeCalled := false
	report, err = auditCellWithDependencies(
		context.Background(), cell,
		func(context.Context, string) (currentbackup.Manifest, error) {
			return currentbackup.Manifest{}, errors.New("private bundle diagnostic")
		},
		func(context.Context, string, s3audit.Expectations) (s3audit.Report, error) {
			storeCalled = true
			return s3audit.Report{}, errors.New("must not be called")
		},
	)
	if err == nil || !contains(report.FailureCodes, "BUNDLE_VERIFY_FAILED") ||
		report.Archive.BundleVerified || storeCalled {
		t.Fatalf("error=%v report=%+v", err, report)
	}
}

func TestAuditCellRejectsReportStoreRoleCacheMismatch(t *testing.T) {
	source := syntheticSourceReport("private")
	for familyIndex := range source.RepetitionReports[0].Report.Families {
		familyAttempt := &source.RepetitionReports[0].Report.Families[familyIndex].Attempts[0]
		serviceAttempt := &source.RepetitionReports[0].Report.ServiceOrder[familyIndex]
		cached, uncached := uint64(26), uint64(74)
		familyAttempt.Tokens.CachedInput = &cached
		familyAttempt.Tokens.UncachedInput = &uncached
		serviceAttempt.Tokens.CachedInput = &cached
		serviceAttempt.Tokens.UncachedInput = &uncached
	}
	cell := writeSyntheticCell(t, source, "COMPLETE", "")

	report, err := auditSyntheticCell(cell)
	if err == nil || !contains(report.FailureCodes, "ARCHIVE_ROLE_CACHE_MISMATCH") ||
		!report.Archive.StoreAuditVerified || report.Archive.RoleCacheCrossChecked {
		t.Fatalf("error=%v archive=%+v failures=%v", err, report.Archive, report.FailureCodes)
	}
}

func TestAuditCellDistinguishesUnknownFromKnownZeroCache(t *testing.T) {
	source := syntheticSourceReport("private")
	repetition := &source.RepetitionReports[0]
	repetition.Error = "MODEL_UNKNOWN"
	source.FirstError = "MODEL_UNKNOWN"
	repetition.Report.Families[0].Reviewer = &s3eval.DispositionFact{
		RunID: "private-reviewer-run", Disposition: loopapi.DispositionWaitingReconciliation,
	}
	repetition.Report.Families[0].Attempts[0].State = corecontract.ModelAttemptUnknown
	repetition.Report.Families[0].Attempts[0].Tokens = corecontract.UsageTokens{}
	repetition.Report.Families[0].Results = nil
	repetition.Report.ServiceOrder[0].State = corecontract.ModelAttemptUnknown
	repetition.Report.ServiceOrder[0].Tokens = corecontract.UsageTokens{}
	cell := writeSyntheticCell(t, source, "PARTIAL", "")

	storeAudit := matchingSyntheticStoreAudit(cell)
	report, err := auditCellWithDependencies(
		context.Background(), cell,
		func(context.Context, string) (currentbackup.Manifest, error) {
			return currentbackup.Manifest{
				Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
				ManifestDigest: "stable",
			}, nil
		},
		func(ctx context.Context, path string, expectations s3audit.Expectations) (s3audit.Report, error) {
			storeReport, storeErr := storeAudit(ctx, path, expectations)
			root := storeReport.Observed.RoleCache[string(corecontract.CompositeRunRoleRootV1)]
			knownCached := uint64(50)
			root.Tokens.CachedInput = &knownCached
			root.TokenFieldCoverage.CachedInput = s3audit.TokenFieldRowCoverage{KnownRows: 3}
			root.CachedInputZeroRows = 1
			root.CachedInputUnknownRows = 0
			storeReport.Observed.RoleCache[string(corecontract.CompositeRunRoleRootV1)] = root
			return storeReport, storeErr
		},
	)
	if err == nil || !contains(report.FailureCodes, "ARCHIVE_ROLE_CACHE_MISMATCH") ||
		!report.Archive.StoreAuditVerified || report.Archive.RoleCacheCrossChecked {
		t.Fatalf("error=%v archive=%+v failures=%v", err, report.Archive, report.FailureCodes)
	}
}

func TestAuditCellRedactsStoreAuditFailureAndDynamicRole(t *testing.T) {
	privateMarker := "sk-" + strings.Repeat("c", 32)
	cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
	tests := map[string]storeAuditFunc{
		"private error": func(context.Context, string, s3audit.Expectations) (s3audit.Report, error) {
			return s3audit.Report{
				SchemaVersion: s3audit.ReportSchemaVersionV3,
				Status:        "FAIL", FailureCodes: []string{privateMarker},
			}, errors.New(privateMarker)
		},
		"old schema": func(context.Context, string, s3audit.Expectations) (s3audit.Report, error) {
			return s3audit.Report{
				SchemaVersion: s3audit.ReportSchemaVersionV1,
				Status:        "PASS", CurrentStoreVerified: true,
				NoSidecarsVerified: true, SourceUnchanged: true,
			}, nil
		},
		"dynamic role": func(
			ctx context.Context,
			path string,
			expectations s3audit.Expectations,
		) (s3audit.Report, error) {
			storeReport, storeErr := matchingSyntheticStoreAudit(cell)(ctx, path, expectations)
			storeReport.Observed.RoleCache[privateMarker] = s3audit.RoleCacheObservation{}
			return storeReport, storeErr
		},
	}
	for name, storeAudit := range tests {
		t.Run(name, func(t *testing.T) {
			report, err := auditCellWithDependencies(
				context.Background(), cell,
				func(context.Context, string) (currentbackup.Manifest, error) {
					return currentbackup.Manifest{
						Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
						ManifestDigest: "stable",
					}, nil
				},
				storeAudit,
			)
			if err == nil {
				t.Fatalf("audit unexpectedly passed: %+v", report)
			}
			payload, marshalErr := json.Marshal(report)
			if marshalErr != nil || strings.Contains(string(payload), privateMarker) ||
				strings.Contains(err.Error(), privateMarker) {
				t.Fatalf("private Store data leaked: marshal=%v error=%v output=%s", marshalErr, err, payload)
			}
			if name == "dynamic role" {
				if !contains(report.FailureCodes, "ARCHIVE_ROLE_CACHE_MISMATCH") {
					t.Fatalf("failures=%v", report.FailureCodes)
				}
			} else if !contains(report.FailureCodes, "ARCHIVE_STORE_AUDIT_FAILED") {
				t.Fatalf("failures=%v", report.FailureCodes)
			}
		})
	}
}

func TestAuditCellStoreExpectationsRespectCompleteBoundary(t *testing.T) {
	for _, test := range []struct {
		name            string
		exitStatus      string
		requireComplete bool
	}{
		{name: "complete", exitStatus: "COMPLETE", requireComplete: true},
		{name: "partial", exitStatus: "PARTIAL", requireComplete: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cell := writeSyntheticCell(t, syntheticSourceReport("private"), test.exitStatus, "")
			matching := matchingSyntheticStoreAudit(cell)
			called := false
			expectationsOK := false
			_, _ = auditCellWithDependencies(
				context.Background(), cell,
				func(context.Context, string) (currentbackup.Manifest, error) {
					return currentbackup.Manifest{
						Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
						ManifestDigest: "stable",
					}, nil
				},
				func(ctx context.Context, path string, expectations s3audit.Expectations) (s3audit.Report, error) {
					called = true
					expectationsOK = expectations.RequireAllTerminal == test.requireComplete &&
						expectations.RequireAllSucceeded == test.requireComplete &&
						(test.requireComplete || expectations.Families == 0 &&
							expectations.Runs == 0 && expectations.Attempts == 0)
					if !expectationsOK {
						return s3audit.Report{}, errors.New("terminal expectation mismatch")
					}
					return matching(ctx, path, expectations)
				},
			)
			if !called || !expectationsOK {
				t.Fatalf("Store audit called=%v expectations_ok=%v", called, expectationsOK)
			}
		})
	}
}

func TestAuditCellRejectsNonCompleteExit(t *testing.T) {
	cell := writeSyntheticCell(t, syntheticSourceReport("private"), "PARTIAL", "")
	report, err := auditSyntheticCell(cell)
	if err == nil || report.Status != "FAIL" ||
		!contains(report.FailureCodes, "EXIT_NOT_COMPLETE") ||
		report.Archive.ExitComplete {
		t.Fatalf("error=%v report=%+v", err, report)
	}
}

func TestAuditCellRejectsIncompleteCompletePhaseEvidence(t *testing.T) {
	tests := map[string]func(*sourceExit){
		"null exit code": func(exit *sourceExit) {
			exit.Phases.InitExitCode = nil
		},
		"nonzero exit code": func(exit *sourceExit) {
			exit.Phases.S3EvalExitCode = intPointer(1)
		},
		"timed out": func(exit *sourceExit) {
			exit.Phases.BackupTimedOut = boolPointer(true)
		},
		"capture failed": func(exit *sourceExit) {
			exit.Phases.BackupVerifyCaptureFailed = boolPointer(true)
		},
		"null timed out": func(exit *sourceExit) { exit.Phases.InitTimedOut = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
			mutateSyntheticExit(t, cell, mutate)
			report, err := auditSyntheticCell(cell)
			if err == nil || !contains(report.FailureCodes, "EXIT_PHASE_INVALID") {
				t.Fatalf("error=%v report=%+v", err, report)
			}
		})
	}
}

func TestAuditCellRejectsInvalidCompleteTerminalMetadata(t *testing.T) {
	tests := map[string]func(*sourceExit){
		"failure code": func(exit *sourceExit) {
			exit.FailureCode = stringPointer("PRIVATE_FAILURE")
		},
		"failure exception": func(exit *sourceExit) {
			exit.FailureExceptionType = stringPointer("private.Type")
		},
		"null failure code":    func(exit *sourceExit) { exit.FailureCode = nil },
		"report not committed": func(exit *sourceExit) { exit.ReportCommitted = boolPointer(false) },
		"archive claim false":  func(exit *sourceExit) { exit.ArchiveVerified = boolPointer(false) },
		"automatic retry":      func(exit *sourceExit) { exit.AutomaticRetry = boolPointer(true) },
		"manual review":        func(exit *sourceExit) { exit.ManualReviewRequired = boolPointer(true) },
		"raw work not retained": func(exit *sourceExit) {
			exit.RawWorkRetained = boolPointer(false)
		},
		"cleanup failed": func(exit *sourceExit) {
			exit.SensitiveCaptureCleanupFailed = boolPointer(true)
		},
		"null automatic retry": func(exit *sourceExit) { exit.AutomaticRetry = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
			mutateSyntheticExit(t, cell, mutate)
			report, err := auditSyntheticCell(cell)
			if err == nil || !contains(report.FailureCodes, "EXIT_TERMINAL_METADATA_INVALID") {
				t.Fatalf("error=%v report=%+v", err, report)
			}
		})
	}
}

func TestAuditCellRejectsInvalidOrNoncanonicalExitTimes(t *testing.T) {
	tests := map[string]func(*sourceExit){
		"noncanonical Z": func(exit *sourceExit) {
			exit.StartedAtUTC = "2026-08-05T00:00:00.0000000Z"
		},
		"non UTC": func(exit *sourceExit) {
			exit.FinishedAtUTC = "2026-08-05T08:01:00.0000000+08:00"
		},
		"invalid": func(exit *sourceExit) {
			exit.StartedAtUTC = "not-a-time"
		},
		"finished before start": func(exit *sourceExit) {
			exit.FinishedAtUTC = "2026-08-04T23:59:59.0000000+00:00"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
			mutateSyntheticExit(t, cell, mutate)
			report, err := auditSyntheticCell(cell)
			if err == nil || !contains(report.FailureCodes, "EXIT_TIME_INVALID") {
				t.Fatalf("error=%v report=%+v", err, report)
			}
		})
	}
}

func TestAuditCellRejectsEmptyOrMismatchedCellID(t *testing.T) {
	privateCellID := "sk-" + strings.Repeat("b", 32)
	for _, cellID := range []string{"", "another-cell", privateCellID} {
		cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
		mutateSyntheticExit(t, cell, func(exit *sourceExit) { exit.CellID = cellID })
		report, err := auditSyntheticCell(cell)
		if err == nil || !contains(report.FailureCodes, "EXIT_CELL_ID_INVALID") {
			t.Fatalf("cell_id=%q error=%v report=%+v", cellID, err, report)
		}
		payload, marshalErr := json.Marshal(report)
		if marshalErr != nil || (cellID != "" && strings.Contains(string(payload), cellID)) {
			t.Fatalf("cell identity leaked: marshal=%v output=%s", marshalErr, payload)
		}
	}
}

func TestAuditCellRejectsUnknownJSONField(t *testing.T) {
	cell := writeSyntheticCell(t, syntheticSourceReport("private"), "COMPLETE", "")
	path := filepath.Join(cell, "report.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected"] = "must be rejected"
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := auditSyntheticCell(cell)
	if err == nil || !contains(report.FailureCodes, "REPORT_JSON_INVALID") {
		t.Fatalf("error=%v report=%+v", err, report)
	}
}

func syntheticSourceReport(private string) sourceReport {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	source := sourceReport{
		SchemaVersion: cellReportSchemaV2,
		Experiment: sourceExperiment{
			ID: private, RepetitionsRequested: 1, RepetitionsAttempted: 1,
		},
		Scenario: s3eval.ScenarioV1{
			SchemaVersion: s3eval.ScenarioSchemaVersionV1,
			ExperimentID:  private,
			AgentID:       private,
			ProfileID:     private,
			Tasks: []s3eval.ScenarioTaskV1{
				{TaskID: private + "-1", WorkspaceID: private + "-1", CaseKind: s3eval.CaseKindCleanV1, Message: "prompt containing " + private},
				{TaskID: private + "-2", WorkspaceID: private + "-2", CaseKind: s3eval.CaseKindCleanV1, Message: "prompt containing " + private},
				{TaskID: private + "-3", WorkspaceID: private + "-3", CaseKind: s3eval.CaseKindCleanV1, Message: "prompt containing " + private},
			},
		},
		Runtime: s3EvalRuntimeSource(true),
	}
	jain := 1.0
	repetition := sourceRepetition{
		Repetition: 1,
		Report: s3eval.ExperimentReport{
			Fairness: s3eval.FairnessReport{
				JainIndex: &jain, LongestConsecutiveCount: 1,
				StarvedWorkspaces: []string{}, Workspaces: []s3eval.WorkspaceFairnessFact{},
			},
			Families:     []s3eval.FamilyReport{},
			ServiceOrder: []s3eval.AttemptFact{},
		},
	}
	for index := 0; index < 3; index++ {
		workspace := fmt.Sprintf("%s-%d", private, index+1)
		identity := fmt.Sprintf("%s-run-%d", private, index+1)
		input, cached, uncached, output := uint64(100), uint64(25), uint64(75), uint64(20)
		attempt := s3eval.AttemptFact{
			ServiceOrder: uint64(index + 1), WorkspaceID: workspace,
			RootRunID: identity, RunID: identity, Role: corecontract.CompositeRunRoleRootV1,
			SlotID: private, AttemptID: identity, LogicalStepID: private,
			State: corecontract.ModelAttemptSucceeded, Provider: "deepseek",
			Model: "deepseek-v4-flash", RequestDigest: private,
			CreatedAt: now, UpdatedAt: now.Add(time.Second), Elapsed: time.Second,
			Tokens: corecontract.UsageTokens{
				Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
			},
		}
		family := s3eval.FamilyReport{
			WorkspaceID: workspace, RequestID: private, RootRunID: identity,
			StartedAt: now, FinishedAt: now.Add(2 * time.Second), WallElapsed: 2 * time.Second,
			Children: []s3eval.DispositionFact{},
			Root:     s3eval.DispositionFact{RunID: identity, Disposition: loopapi.DispositionTerminated},
			Reply:    "reply containing " + private,
			Tokens:   s3eval.TokenReport{},
			Attempts: []s3eval.AttemptFact{attempt},
			Results: []s3eval.ResultFact{{
				RunID: identity, Role: corecontract.CompositeRunRoleRootV1,
				SlotID: private, AttemptID: identity, ResultDigest: private,
				AssistantText: "result containing " + private,
			}},
		}
		repetition.Report.Families = append(repetition.Report.Families, family)
		repetition.Report.ServiceOrder = append(repetition.Report.ServiceOrder, attempt)
	}
	workspaceIDs := make([]string, 0, len(source.Scenario.Tasks))
	for _, task := range source.Scenario.Tasks {
		workspaceIDs = append(workspaceIDs, task.WorkspaceID)
	}
	order := make([]string, 0, len(repetition.Report.ServiceOrder))
	for _, attempt := range repetition.Report.ServiceOrder {
		order = append(order, attempt.WorkspaceID)
	}
	repetition.Report.Fairness, _ = s3eval.ComputeFairness(workspaceIDs, order)
	source.RepetitionReports = []sourceRepetition{repetition}
	return source
}

func addCompleteReviewers(source *sourceReport, private string) {
	repetition := &source.RepetitionReports[0]
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	for index := range repetition.Report.Families {
		family := &repetition.Report.Families[index]
		identity := fmt.Sprintf("%s-reviewer-%d", private, index+1)
		family.Reviewer = &s3eval.DispositionFact{
			RunID: identity, Disposition: loopapi.DispositionTerminated,
		}
		input, cached, uncached, output := uint64(100), uint64(25), uint64(75), uint64(20)
		attempt := s3eval.AttemptFact{
			ServiceOrder: uint64(len(repetition.Report.ServiceOrder) + 1),
			WorkspaceID:  family.WorkspaceID, RootRunID: family.RootRunID,
			RunID: identity, Role: corecontract.CompositeRunRoleReviewerV1,
			SlotID: private, AttemptID: identity, LogicalStepID: private,
			State: corecontract.ModelAttemptSucceeded, Provider: "deepseek",
			Model: "deepseek-v4-flash", RequestDigest: private,
			CreatedAt: now, UpdatedAt: now.Add(time.Second), Elapsed: time.Second,
			Tokens: corecontract.UsageTokens{
				Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
			},
		}
		family.Attempts = append(family.Attempts, attempt)
		family.Results = append(family.Results, s3eval.ResultFact{
			RunID: identity, Role: corecontract.CompositeRunRoleReviewerV1,
			SlotID: private, AttemptID: identity, ResultDigest: private,
			AssistantText: "reviewer result containing " + private,
			ReviewerVerdict: &corecontract.ReviewVerdictV1{
				Decision: corecontract.ReviewDecisionApproveV1,
			},
		})
		repetition.Report.ServiceOrder = append(repetition.Report.ServiceOrder, attempt)
	}
	workspaceIDs := make([]string, 0, len(source.Scenario.Tasks))
	for _, task := range source.Scenario.Tasks {
		workspaceIDs = append(workspaceIDs, task.WorkspaceID)
	}
	order := make([]string, 0, len(repetition.Report.ServiceOrder))
	for _, attempt := range repetition.Report.ServiceOrder {
		order = append(order, attempt.WorkspaceID)
	}
	repetition.Report.Fairness, _ = s3eval.ComputeFairness(workspaceIDs, order)
}

func s3EvalRuntimeSource(enabled bool) sourceRuntime {
	return sourceRuntime{DeepSeekEnabled: enabled}
}

func writeSyntheticCell(
	t *testing.T,
	source sourceReport,
	status string,
	digestOverride string,
) string {
	t.Helper()
	cell := filepath.Join(t.TempDir(), "synthetic-cell")
	if err := os.Mkdir(cell, 0o700); err != nil {
		t.Fatal(err)
	}
	reportPayload, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	reportPayload = append(reportPayload, '\n')
	if err := os.WriteFile(filepath.Join(cell, "report.json"), reportPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(reportPayload)
	digestText := hex.EncodeToString(digest[:])
	if digestOverride != "" {
		digestText = digestOverride
	}
	exit := sourceExit{
		SchemaVersion: cellExitSchemaV1,
		CellID:        filepath.Base(cell),
		StartedAtUTC:  "2026-08-05T00:00:00.0000000+00:00",
		FinishedAtUTC: "2026-08-05T00:01:00.0000000+00:00",
		Status:        status, Phases: successfulSourcePhases(),
		FailureCode: stringPointer(""), FailureExceptionType: stringPointer(""),
		ReportCommitted: boolPointer(true), ReportSHA256: digestText,
		ArchiveVerified: boolPointer(true), AutomaticRetry: boolPointer(false),
		ManualReviewRequired: boolPointer(status != "COMPLETE"),
		RawWorkRetained:      boolPointer(true), SensitiveCaptureCleanupFailed: boolPointer(false),
	}
	exitPayload, err := json.Marshal(exit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cell, "exit.json"), append(exitPayload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return cell
}

func successfulSourcePhases() sourcePhases {
	return sourcePhases{
		InitExitCode: intPointer(0), S3EvalExitCode: intPointer(0),
		BackupExitCode: intPointer(0), BackupVerifyExitCode: intPointer(0),
		InitTimedOut: boolPointer(false), InitCaptureFailed: boolPointer(false),
		S3EvalTimedOut: boolPointer(false), S3EvalCaptureFailed: boolPointer(false),
		BackupTimedOut: boolPointer(false), BackupCaptureFailed: boolPointer(false),
		BackupVerifyTimedOut:      boolPointer(false),
		BackupVerifyCaptureFailed: boolPointer(false),
	}
}

func intPointer(value int) *int          { return &value }
func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }

func mutateSyntheticExit(t *testing.T, cell string, mutate func(*sourceExit)) {
	t.Helper()
	path := filepath.Join(cell, "exit.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exit sourceExit
	if err := json.Unmarshal(payload, &exit); err != nil {
		t.Fatal(err)
	}
	mutate(&exit)
	payload, err = json.Marshal(exit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func auditSyntheticCell(cell string) (Report, error) {
	return auditCellWithDependencies(
		context.Background(), cell,
		func(context.Context, string) (currentbackup.Manifest, error) {
			return currentbackup.Manifest{
				Database:       currentbackup.DatabaseFile{Path: "database.sqlite"},
				ManifestDigest: "stable",
			}, nil
		},
		matchingSyntheticStoreAudit(cell),
	)
}

func matchingSyntheticStoreAudit(cell string) storeAuditFunc {
	return func(
		_ context.Context,
		databasePath string,
		expectations s3audit.Expectations,
	) (s3audit.Report, error) {
		wantPath := filepath.Join(cell, "archive", "store.bundle", "database.sqlite")
		if !samePath(databasePath, wantPath) {
			return s3audit.Report{}, errors.New("synthetic Store path mismatch")
		}
		payload, err := os.ReadFile(filepath.Join(cell, "report.json"))
		if err != nil {
			return s3audit.Report{}, errors.New("synthetic report unavailable")
		}
		var source sourceReport
		if !strictDecode(payload, &source) {
			return s3audit.Report{}, errors.New("synthetic report invalid")
		}
		kind := "ROOT"
		if source.FirstError != "" {
			kind = "PARTIAL"
		} else {
			for _, repetition := range source.RepetitionReports {
				for _, family := range repetition.Report.Families {
					if family.Reviewer != nil {
						kind = "REVIEWER"
					}
				}
			}
		}
		return syntheticStoreReport(kind, expectations)
	}
}

func syntheticStoreReport(
	kind string,
	expectations s3audit.Expectations,
) (s3audit.Report, error) {
	wantFamilies, wantRuns, wantAttempts := uint64(3), uint64(3), uint64(3)
	switch kind {
	case "REVIEWER":
		wantRuns, wantAttempts = 6, 6
	case "PARTIAL":
		wantRuns = 4
	case "ROOT":
	default:
		return s3audit.Report{}, errors.New("synthetic Store kind invalid")
	}
	if expectations.Families != 0 && expectations.Families != wantFamilies ||
		expectations.Runs != 0 && expectations.Runs != wantRuns ||
		expectations.Attempts != 0 && expectations.Attempts != wantAttempts {
		return s3audit.Report{}, errors.New("synthetic Store expectations mismatch")
	}
	roleCache := map[string]s3audit.RoleCacheObservation{
		string(corecontract.CompositeRunRoleChildV1):    {},
		string(corecontract.CompositeRunRoleReviewerV1): {},
		string(corecontract.CompositeRunRoleRootV1):     completeStoreRoleCache(),
	}
	if kind == "REVIEWER" {
		roleCache[string(corecontract.CompositeRunRoleReviewerV1)] = completeStoreRoleCache()
	}
	if kind == "PARTIAL" {
		roleCache[string(corecontract.CompositeRunRoleRootV1)] = partialStoreRoleCache()
	}
	return s3audit.Report{
		SchemaVersion:        s3audit.ReportSchemaVersionV3,
		Status:               "PASS",
		CurrentStoreVerified: true,
		NoSidecarsVerified:   true,
		SourceUnchanged:      true,
		Expected: s3audit.ExpectedReport{
			Families:            expectations.Families,
			Runs:                expectations.Runs,
			Attempts:            expectations.Attempts,
			RequireAllTerminal:  expectations.RequireAllTerminal,
			RequireAllSucceeded: expectations.RequireAllSucceeded,
		},
		Observed: s3audit.ObservedReport{
			Families:  wantFamilies,
			Runs:      wantRuns,
			Attempts:  wantAttempts,
			RoleCache: roleCache,
		},
		FailureCodes: []string{},
	}, nil
}

func completeStoreRoleCache() s3audit.RoleCacheObservation {
	input, cached, uncached, output := uint64(300), uint64(75), uint64(225), uint64(60)
	ratio := 0.25
	return s3audit.RoleCacheObservation{
		Attempts:                  3,
		UsageRowsWithAnyTokenFact: 3,
		Tokens: s3audit.TokenTotals{
			Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
		},
		TokenFieldCoverage: s3audit.TokenFieldCoverage{
			Input:         s3audit.TokenFieldRowCoverage{KnownRows: 3},
			CachedInput:   s3audit.TokenFieldRowCoverage{KnownRows: 3},
			UncachedInput: s3audit.TokenFieldRowCoverage{KnownRows: 3},
			Output:        s3audit.TokenFieldRowCoverage{KnownRows: 3},
			Reasoning:     s3audit.TokenFieldRowCoverage{UnknownRows: 3},
		},
		KnownTokenSubtotal: s3audit.TokenTotals{
			Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
		},
		CachedInputPositiveRows: 3,
		CacheHitRatio:           &ratio,
	}
}

func partialStoreRoleCache() s3audit.RoleCacheObservation {
	input, cached, uncached, output := uint64(200), uint64(50), uint64(150), uint64(40)
	return s3audit.RoleCacheObservation{
		Attempts:                   3,
		UsageRowsWithAnyTokenFact:  2,
		UsageRowsWithoutTokenFacts: 1,
		TokenFieldCoverage: s3audit.TokenFieldCoverage{
			Input:         s3audit.TokenFieldRowCoverage{KnownRows: 2, UnknownRows: 1},
			CachedInput:   s3audit.TokenFieldRowCoverage{KnownRows: 2, UnknownRows: 1},
			UncachedInput: s3audit.TokenFieldRowCoverage{KnownRows: 2, UnknownRows: 1},
			Output:        s3audit.TokenFieldRowCoverage{KnownRows: 2, UnknownRows: 1},
			Reasoning:     s3audit.TokenFieldRowCoverage{UnknownRows: 3},
		},
		KnownTokenSubtotal: s3audit.TokenTotals{
			Input: &input, CachedInput: &cached, UncachedInput: &uncached, Output: &output,
		},
		CachedInputPositiveRows: 2,
		CachedInputUnknownRows:  1,
	}
}
