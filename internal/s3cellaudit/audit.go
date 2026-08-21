// Package s3cellaudit verifies one completed S3-C operator cell and emits
// aggregate-only evidence. It opens no writable Store or Runtime, performs no
// network operation, and uses the existing read-only verifier for the archived
// Store snapshot. It never copies source identities or model content to output.
package s3cellaudit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekcost"
	"github.com/endview/freeagent/internal/s3audit"
	"github.com/endview/freeagent/internal/s3eval"
)

const (
	// ReportSchemaVersionV1 identifies historical reports that did not
	// independently cross-check role/cache facts against the archived Store.
	ReportSchemaVersionV1 = "freeagent.s3-cell-audit/v1"
	// ReportSchemaVersionV2 freezes the fixed CHILD/REVIEWER/ROOT role_cache
	// projection and its read-only archived-Store cross-check.
	ReportSchemaVersionV2 = "freeagent.s3-cell-audit/v2"
	cellReportSchemaV1    = "freeagent.s3-eval-report/v1"
	cellExitSchemaV1      = "freeagent.s3c-real-cell-exit/v1"
	maximumReportBytes    = 128 << 20
	maximumExitBytes      = 1 << 20
)

var ErrAuditFailed = errors.New("s3cellaudit: cell audit failed")

type ExperimentSummary struct {
	RepetitionsRequested uint64 `json:"repetitions_requested"`
	RepetitionsAttempted uint64 `json:"repetitions_attempted"`
}

type RepetitionSummary struct {
	Reports           uint64 `json:"reports"`
	Errors            uint64 `json:"errors"`
	FirstErrorPresent bool   `json:"first_error_present"`
}

type CountSummary struct {
	Families uint64 `json:"families"`
	Attempts uint64 `json:"attempts"`
	Results  uint64 `json:"results"`
	// ReviewerRuns counts FamilyReport.Reviewer records. It does not imply
	// that a model Attempt or terminal result exists.
	ReviewerRuns     uint64 `json:"reviewer_runs"`
	ReviewerAttempts uint64 `json:"reviewer_attempts"`
	ReviewerResults  uint64 `json:"reviewer_results"`
	ReviewerVerdicts uint64 `json:"reviewer_verdicts"`
}

type SchedulerSummary struct {
	Enabled                bool   `json:"enabled"`
	GlobalWorkers          uint32 `json:"global_workers"`
	WorkspaceWorkers       uint32 `json:"workspace_workers"`
	CompositeFamilyWorkers uint32 `json:"composite_family_workers"`
}

type FloatStats struct {
	Known uint64   `json:"known"`
	Min   *float64 `json:"min"`
	Max   *float64 `json:"max"`
	Mean  *float64 `json:"mean"`
}

type UintStats struct {
	Count uint64   `json:"count"`
	Min   *uint64  `json:"min"`
	Max   *uint64  `json:"max"`
	Total *uint64  `json:"total"`
	Mean  *float64 `json:"mean"`
}

type FairnessSummary struct {
	Reports                 uint64     `json:"reports"`
	JainIndex               FloatStats `json:"jain_index"`
	StarvationReports       uint64     `json:"starvation_reports"`
	StarvedWorkspaceEntries uint64     `json:"starved_workspace_entries"`
	LongestConsecutive      UintStats  `json:"longest_consecutive"`
	// Prefix imbalance is max(count)-min(count) after each service event;
	// PrefixGapArea sums that gap over the full observed order.
	MaxPrefixImbalance UintStats `json:"max_prefix_imbalance"`
	PrefixGapArea      UintStats `json:"prefix_gap_area"`
	// LatestFirstServiceOrder is unknown for a repetition with starvation.
	LatestFirstServiceOrder UintStats `json:"latest_first_service_order"`
}

type LatencySummary struct {
	FamilyWallNanoseconds UintStats `json:"family_wall_nanoseconds"`
	AttemptNanoseconds    UintStats `json:"attempt_nanoseconds"`
}

type TokenTotals struct {
	Input         *uint64 `json:"input_tokens"`
	CachedInput   *uint64 `json:"cached_input_tokens"`
	UncachedInput *uint64 `json:"uncached_input_tokens"`
	Output        *uint64 `json:"output_tokens"`
	Reasoning     *uint64 `json:"reasoning_tokens"`
}

type FieldCoverage struct {
	Known      uint64 `json:"known"`
	Unknown    uint64 `json:"unknown"`
	Overflowed bool   `json:"overflowed"`
}

type TokenCoverage struct {
	Input         FieldCoverage `json:"input_tokens"`
	CachedInput   FieldCoverage `json:"cached_input_tokens"`
	UncachedInput FieldCoverage `json:"uncached_input_tokens"`
	Output        FieldCoverage `json:"output_tokens"`
	Reasoning     FieldCoverage `json:"reasoning_tokens"`
}

type TokenRowCoverage struct {
	KnownRows   uint64 `json:"known_rows"`
	UnknownRows uint64 `json:"unknown_rows"`
}

type RoleTokenCoverage struct {
	Input         TokenRowCoverage `json:"input_tokens"`
	CachedInput   TokenRowCoverage `json:"cached_input_tokens"`
	UncachedInput TokenRowCoverage `json:"uncached_input_tokens"`
	Output        TokenRowCoverage `json:"output_tokens"`
	Reasoning     TokenRowCoverage `json:"reasoning_tokens"`
}

// RoleCacheSummary is the cell-audit/v2 contract. It intentionally owns its
// JSON shape instead of aliasing the independently versioned Store audit DTO.
type RoleCacheSummary struct {
	Attempts                   uint64            `json:"attempts"`
	UsageRowsWithAnyTokenFact  uint64            `json:"usage_rows_with_any_token_fact"`
	UsageRowsWithoutTokenFacts uint64            `json:"usage_rows_without_token_facts"`
	Tokens                     TokenTotals       `json:"tokens"`
	TokenFieldCoverage         RoleTokenCoverage `json:"token_field_coverage"`
	KnownTokenSubtotal         TokenTotals       `json:"known_token_subtotal"`
	CachedInputPositiveRows    uint64            `json:"cached_input_positive_rows"`
	CachedInputZeroRows        uint64            `json:"cached_input_zero_rows"`
	CachedInputUnknownRows     uint64            `json:"cached_input_unknown_rows"`
	CacheHitRatio              *float64          `json:"cache_hit_ratio"`
}

type TokenPerAttempt struct {
	Input         UintStats `json:"input_tokens"`
	CachedInput   UintStats `json:"cached_input_tokens"`
	UncachedInput UintStats `json:"uncached_input_tokens"`
	Output        UintStats `json:"output_tokens"`
}

type TokenSummary struct {
	Attempts uint64 `json:"attempts"`
	// KnownUsageAttempts requires the four billing/cache fields (input,
	// cached input, uncached input, output); reasoning remains independent.
	KnownUsageAttempts          uint64          `json:"known_usage_attempts"`
	CachedInputPositiveAttempts uint64          `json:"cached_input_positive_attempts"`
	Totals                      TokenTotals     `json:"complete_totals"`
	KnownSubtotals              TokenTotals     `json:"known_subtotals"`
	Coverage                    TokenCoverage   `json:"coverage"`
	PerAttempt                  TokenPerAttempt `json:"per_attempt"`
	CacheHitRatio               *float64        `json:"cache_hit_ratio"`
}

type CostTotal struct {
	Status     string   `json:"status"`
	Value      *string  `json:"value"`
	Currency   string   `json:"currency"`
	Currencies []string `json:"currencies"`
}

type CostCoverage struct {
	Entries       uint64 `json:"entries"`
	Known         uint64 `json:"known"`
	Unknown       uint64 `json:"unknown"`
	MixedCurrency uint64 `json:"mixed_currency"`
	Invalid       uint64 `json:"invalid"`
}

type CostAggregate struct {
	Total         CostTotal    `json:"total"`
	KnownSubtotal CostTotal    `json:"known_subtotal"`
	Coverage      CostCoverage `json:"coverage"`
}

type CostSummary struct {
	// The first three coverage counts are family-level projections. Derived
	// coverage counts the per-Attempt frozen-price estimates in the report.
	Estimated        CostAggregate `json:"estimated"`
	ProviderReported CostAggregate `json:"provider_reported"`
	Reconciled       CostAggregate `json:"reconciled"`
	Derived          CostAggregate `json:"derived"`
}

type ArchiveSummary struct {
	ExitComplete          bool `json:"exit_complete"`
	ReportSHA256Verified  bool `json:"report_sha256_verified"`
	ArchiveVerified       bool `json:"archive_verified"`
	BundleVerified        bool `json:"bundle_verified"`
	BundleUnchanged       bool `json:"bundle_unchanged"`
	StoreAuditVerified    bool `json:"store_audit_verified"`
	RoleCacheCrossChecked bool `json:"role_cache_cross_checked"`
	SourceUnchanged       bool `json:"source_unchanged"`
}

// Report deliberately has no cell, experiment, task, Workspace, Request, Run,
// Attempt, slot or snapshot identity; no digest; and no source text, reply,
// result, header, receipt, raw provider value, or secret.
type Report struct {
	SchemaVersion string                      `json:"schema_version"`
	Status        string                      `json:"status"`
	Archive       ArchiveSummary              `json:"archive"`
	Experiment    ExperimentSummary           `json:"experiment"`
	Repetitions   RepetitionSummary           `json:"repetitions"`
	Counts        CountSummary                `json:"counts"`
	Scheduler     SchedulerSummary            `json:"scheduler"`
	Models        map[string]uint64           `json:"models"`
	AttemptRoles  map[string]uint64           `json:"attempt_roles"`
	AttemptStates map[string]uint64           `json:"attempt_states"`
	Verdicts      map[string]uint64           `json:"verdicts"`
	Fairness      FairnessSummary             `json:"fairness"`
	Latency       LatencySummary              `json:"latency"`
	Tokens        TokenSummary                `json:"tokens"`
	RoleCache     map[string]RoleCacheSummary `json:"role_cache"`
	Costs         CostSummary                 `json:"costs"`
	FailureCodes  []string                    `json:"failure_codes"`
}

type sourceExperiment struct {
	ID                   string `json:"id"`
	RepetitionsRequested uint64 `json:"repetitions_requested"`
	RepetitionsAttempted uint64 `json:"repetitions_attempted"`
}

type sourceScheduler struct {
	Enabled                bool   `json:"enabled"`
	GlobalWorkers          uint32 `json:"global_workers"`
	WorkspaceWorkers       uint32 `json:"workspace_workers"`
	CompositeFamilyWorkers uint32 `json:"composite_family_workers"`
}

type sourceRuntime struct {
	DeepSeekEnabled bool `json:"deepseek_enabled"`
}

type sourceDerivedCost struct {
	WorkspaceID string                          `json:"workspace_id"`
	RootRunID   string                          `json:"root_run_id"`
	RunID       string                          `json:"run_id"`
	Role        corecontract.CompositeRunRoleV1 `json:"role"`
	AttemptID   string                          `json:"attempt_id"`
	Model       string                          `json:"model"`
	Estimate    deepseekcost.Estimate           `json:"estimate"`
}

type sourceRepetition struct {
	Repetition           uint64                  `json:"repetition"`
	Report               s3eval.ExperimentReport `json:"report"`
	DerivedCostEstimates []sourceDerivedCost     `json:"derived_cost_estimates"`
	Error                string                  `json:"error"`
}

type sourceReport struct {
	SchemaVersion     string             `json:"schema_version"`
	Experiment        sourceExperiment   `json:"experiment"`
	Scenario          s3eval.ScenarioV1  `json:"scenario"`
	Scheduler         sourceScheduler    `json:"scheduler"`
	Runtime           sourceRuntime      `json:"runtime"`
	RepetitionReports []sourceRepetition `json:"repetition_reports"`
	FirstError        string             `json:"first_error"`
}

type sourcePhases struct {
	InitExitCode              *int  `json:"init_exit_code"`
	InitTimedOut              *bool `json:"init_timed_out"`
	InitCaptureFailed         *bool `json:"init_capture_failed"`
	S3EvalExitCode            *int  `json:"s3_eval_exit_code"`
	S3EvalTimedOut            *bool `json:"s3_eval_timed_out"`
	S3EvalCaptureFailed       *bool `json:"s3_eval_capture_failed"`
	BackupExitCode            *int  `json:"backup_exit_code"`
	BackupTimedOut            *bool `json:"backup_timed_out"`
	BackupCaptureFailed       *bool `json:"backup_capture_failed"`
	BackupVerifyExitCode      *int  `json:"backup_verify_exit_code"`
	BackupVerifyTimedOut      *bool `json:"backup_verify_timed_out"`
	BackupVerifyCaptureFailed *bool `json:"backup_verify_capture_failed"`
}

type sourceExit struct {
	SchemaVersion                 string       `json:"schema_version"`
	CellID                        string       `json:"cell_id"`
	StartedAtUTC                  string       `json:"started_at_utc"`
	FinishedAtUTC                 string       `json:"finished_at_utc"`
	Status                        string       `json:"status"`
	FailureCode                   *string      `json:"failure_code"`
	FailureExceptionType          *string      `json:"failure_exception_type"`
	Phases                        sourcePhases `json:"phases"`
	ReportCommitted               *bool        `json:"report_committed"`
	ReportSHA256                  string       `json:"report_sha256"`
	ArchiveVerified               *bool        `json:"archive_verified"`
	AutomaticRetry                *bool        `json:"automatic_retry"`
	ManualReviewRequired          *bool        `json:"manual_review_required"`
	RawWorkRetained               *bool        `json:"raw_work_retained"`
	SensitiveCaptureCleanupFailed *bool        `json:"sensitive_capture_cleanup_failed"`
}

// AuditCell reads report.json and exit.json, independently invokes the
// read-only currentbackup verifier for archive/store.bundle, and cross-checks
// report-derived role/cache facts against the verified archived Store. Sources
// are read or verified again before success to detect ordinary concurrent
// changes; the Operator must still stop every writer before auditing.
func AuditCell(ctx context.Context, cellPath string) (Report, error) {
	return auditCellWithDependencies(
		ctx,
		cellPath,
		currentbackup.VerifyBundle,
		s3audit.AuditClosedStore,
	)
}

type bundleVerifyFunc func(context.Context, string) (currentbackup.Manifest, error)
type storeAuditFunc func(
	context.Context,
	string,
	s3audit.Expectations,
) (s3audit.Report, error)

func auditCellWithDependencies(
	ctx context.Context,
	cellPath string,
	verifyBundle bundleVerifyFunc,
	auditStore storeAuditFunc,
) (Report, error) {
	report := newReport()
	if ctx == nil || strings.TrimSpace(cellPath) == "" || verifyBundle == nil ||
		auditStore == nil {
		report.addFailure("INVALID_INPUT")
		return report.finish(), ErrAuditFailed
	}
	resolved, ok := resolvePlainDirectory(cellPath)
	if !ok {
		report.addFailure("CELL_PATH_INVALID")
		return report.finish(), ErrAuditFailed
	}
	reportBytes, ok := readPlainFile(filepath.Join(resolved, "report.json"), maximumReportBytes)
	if !ok {
		report.addFailure("REPORT_READ_FAILED")
		return report.finish(), ErrAuditFailed
	}
	exitBytes, ok := readPlainFile(filepath.Join(resolved, "exit.json"), maximumExitBytes)
	if !ok {
		report.addFailure("EXIT_READ_FAILED")
		return report.finish(), ErrAuditFailed
	}
	bundlePath := filepath.Join(resolved, "archive", "store.bundle")
	bundleBefore, bundleErr := verifyBundle(ctx, bundlePath)
	if bundleErr != nil {
		report.addFailure("BUNDLE_VERIFY_FAILED")
	}

	var source sourceReport
	reportJSONOK := strictDecode(reportBytes, &source)
	if !reportJSONOK {
		report.addFailure("REPORT_JSON_INVALID")
	}
	var exit sourceExit
	exitJSONOK := strictDecode(exitBytes, &exit)
	if !exitJSONOK {
		report.addFailure("EXIT_JSON_INVALID")
	}
	if reportJSONOK && exitJSONOK {
		report.observe(source)
		report.verifyExit(exit, reportBytes, filepath.Base(resolved))
	}
	if bundleErr == nil && reportJSONOK && exitJSONOK {
		expectations, ok := storeExpectations(source, exit.Status == "COMPLETE")
		if !ok {
			report.addFailure("ARCHIVE_STORE_AUDIT_FAILED")
		} else {
			databasePath := filepath.Join(bundlePath, bundleBefore.Database.Path)
			storeReport, storeErr := auditStore(ctx, databasePath, expectations)
			storeAuditOK := storeErr == nil &&
				storeReport.SchemaVersion == s3audit.ReportSchemaVersionV2 &&
				storeReport.Status == "PASS" &&
				storeReport.CurrentStoreVerified &&
				storeReport.NoSidecarsVerified &&
				storeReport.SourceUnchanged &&
				len(storeReport.FailureCodes) == 0 &&
				storeAuditMatchesExpectations(storeReport, expectations)
			report.Archive.StoreAuditVerified = storeAuditOK
			if !storeAuditOK {
				report.addFailure("ARCHIVE_STORE_AUDIT_FAILED")
			} else if !sameStoreRoleCache(
				report.RoleCache,
				storeReport.Observed.RoleCache,
			) {
				report.addFailure("ARCHIVE_ROLE_CACHE_MISMATCH")
			} else {
				report.Archive.RoleCacheCrossChecked = true
			}
		}
	}

	reportAgain, reportOK := readPlainFile(filepath.Join(resolved, "report.json"), maximumReportBytes)
	exitAgain, exitOK := readPlainFile(filepath.Join(resolved, "exit.json"), maximumExitBytes)
	report.Archive.SourceUnchanged = reportOK && exitOK &&
		sha256.Sum256(reportAgain) == sha256.Sum256(reportBytes) &&
		sha256.Sum256(exitAgain) == sha256.Sum256(exitBytes)
	if !report.Archive.SourceUnchanged {
		report.addFailure("SOURCE_CHANGED_DURING_AUDIT")
	}
	if bundleErr == nil {
		bundleAfter, afterErr := verifyBundle(ctx, bundlePath)
		report.Archive.BundleVerified = afterErr == nil
		report.Archive.BundleUnchanged = afterErr == nil && reflect.DeepEqual(bundleBefore, bundleAfter)
		if afterErr != nil {
			report.addFailure("BUNDLE_VERIFY_FAILED")
		} else if !report.Archive.BundleUnchanged {
			report.addFailure("BUNDLE_CHANGED_DURING_AUDIT")
		}
	}
	report = report.finish()
	if report.Status != "PASS" {
		return report, ErrAuditFailed
	}
	return report, nil
}

func storeAuditMatchesExpectations(
	report s3audit.Report,
	expectations s3audit.Expectations,
) bool {
	if report.Expected.Families != expectations.Families ||
		report.Expected.Runs != expectations.Runs ||
		report.Expected.Attempts != expectations.Attempts ||
		report.Expected.RequireAllTerminal != expectations.RequireAllTerminal ||
		report.Expected.RequireAllSucceeded != expectations.RequireAllSucceeded {
		return false
	}
	return (expectations.Families == 0 || report.Observed.Families == expectations.Families) &&
		(expectations.Runs == 0 || report.Observed.Runs == expectations.Runs) &&
		(expectations.Attempts == 0 || report.Observed.Attempts == expectations.Attempts)
}

func storeExpectations(
	source sourceReport,
	requireComplete bool,
) (s3audit.Expectations, bool) {
	// A PARTIAL report can legitimately stop before or during admission and is
	// not a complete inventory of Store Runs. Keep Store integrity verification
	// independent and let the fixed role_cache compare the Attempt facts that
	// the report actually claims. A PARTIAL cell can never become PASS here.
	if !requireComplete {
		return s3audit.Expectations{}, true
	}
	var families, runs, attempts uint64
	for _, repetition := range source.RepetitionReports {
		for _, family := range repetition.Report.Families {
			if strings.TrimSpace(family.RootRunID) == "" {
				return s3audit.Expectations{}, false
			}
			if family.Root.RunID != family.RootRunID {
				return s3audit.Expectations{}, false
			}
			if !addUint(&families, 1) || !addUint(&runs, 1) ||
				!addUint(&attempts, uint64(len(family.Attempts))) {
				return s3audit.Expectations{}, false
			}
			for _, child := range family.Children {
				if strings.TrimSpace(child.RunID) == "" {
					return s3audit.Expectations{}, false
				}
				if !addUint(&runs, 1) {
					return s3audit.Expectations{}, false
				}
			}
			if family.Reviewer != nil {
				if strings.TrimSpace(family.Reviewer.RunID) == "" {
					return s3audit.Expectations{}, false
				} else if !addUint(&runs, 1) {
					return s3audit.Expectations{}, false
				}
			}
		}
	}
	return s3audit.Expectations{
		Families:            families,
		Runs:                runs,
		Attempts:            attempts,
		RequireAllTerminal:  requireComplete,
		RequireAllSucceeded: requireComplete,
	}, true
}

func (report *Report) observe(source sourceReport) {
	if source.SchemaVersion != cellReportSchemaV1 ||
		source.Scenario.SchemaVersion != s3eval.ScenarioSchemaVersionV1 ||
		len(source.Scenario.Tasks) != s3eval.WorkspaceCount {
		report.addFailure("REPORT_SCHEMA_INVALID")
	}
	if source.Experiment.ID == "" || source.Experiment.ID != source.Scenario.ExperimentID ||
		!source.Runtime.DeepSeekEnabled {
		report.addFailure("REPORT_SCOPE_INVALID")
	}
	report.Experiment = ExperimentSummary{
		RepetitionsRequested: source.Experiment.RepetitionsRequested,
		RepetitionsAttempted: source.Experiment.RepetitionsAttempted,
	}
	report.Repetitions = RepetitionSummary{
		Reports:           uint64(len(source.RepetitionReports)),
		FirstErrorPresent: source.FirstError != "",
	}
	report.Scheduler = SchedulerSummary(source.Scheduler)
	if source.Experiment.RepetitionsRequested == 0 ||
		source.Experiment.RepetitionsAttempted != uint64(len(source.RepetitionReports)) ||
		source.Experiment.RepetitionsAttempted != source.Experiment.RepetitionsRequested {
		report.addFailure("REPETITION_COUNT_INVALID")
	}
	if source.FirstError != "" {
		report.addFailure("REPORT_HAS_ERROR")
	}
	if source.Scheduler.Enabled {
		if source.Scheduler.GlobalWorkers == 0 || source.Scheduler.WorkspaceWorkers == 0 ||
			source.Scheduler.CompositeFamilyWorkers == 0 {
			report.addFailure("SCHEDULER_CONFIG_INVALID")
		}
	} else if source.Scheduler.GlobalWorkers != 0 || source.Scheduler.WorkspaceWorkers != 0 ||
		source.Scheduler.CompositeFamilyWorkers != 0 {
		report.addFailure("SCHEDULER_CONFIG_INVALID")
	}

	tokens := newTokenAccumulator()
	roleCache := newRoleCacheAccumulators()
	estimated := newCostAccumulator()
	providerReported := newCostAccumulator()
	reconciled := newCostAccumulator()
	derived := newCostAccumulator()
	wall := newUintAccumulator()
	attemptElapsed := newUintAccumulator()
	jain := newFloatAccumulator()
	longest := newUintAccumulator()
	maxPrefixImbalance := newUintAccumulator()
	prefixGapArea := newUintAccumulator()
	latestFirstServiceOrder := newUintAccumulator()
	workspaceIDs := make([]string, len(source.Scenario.Tasks))
	for index, task := range source.Scenario.Tasks {
		workspaceIDs[index] = task.WorkspaceID
	}

	for index, repetition := range source.RepetitionReports {
		if repetition.Repetition != uint64(index+1) {
			report.addFailure("REPETITION_ORDER_INVALID")
		}
		if repetition.Error != "" {
			report.Repetitions.Errors++
			report.addFailure("REPORT_HAS_ERROR")
		}
		report.Fairness.Reports++
		if len(repetition.Report.Families) != s3eval.WorkspaceCount {
			report.addFailure("FAMILY_COUNT_INVALID")
		}
		serviceWorkspaceOrder := make([]string, len(repetition.Report.ServiceOrder))
		for serviceIndex, attempt := range repetition.Report.ServiceOrder {
			if attempt.ServiceOrder != uint64(serviceIndex+1) {
				report.addFailure("SERVICE_ORDER_INVALID")
			}
			serviceWorkspaceOrder[serviceIndex] = attempt.WorkspaceID
		}
		recomputed, fairnessErr := s3eval.ComputeFairness(workspaceIDs, serviceWorkspaceOrder)
		if fairnessErr != nil {
			report.addFailure("FAIRNESS_RECOMPUTE_FAILED")
		} else {
			if !sameFairness(repetition.Report.Fairness, recomputed) {
				report.addFailure("FAIRNESS_MISMATCH")
			}
			if recomputed.JainIndex != nil && !jain.add(*recomputed.JainIndex) {
				report.addFailure("FAIRNESS_INVALID")
			}
			if recomputed.Starvation {
				report.Fairness.StarvationReports++
			}
			if !addUint(&report.Fairness.StarvedWorkspaceEntries,
				uint64(len(recomputed.StarvedWorkspaces))) {
				report.addFailure("COUNT_OVERFLOW")
			}
			if !longest.add(recomputed.LongestConsecutiveCount) {
				report.addFailure("FAIRNESS_OVERFLOW")
			}
			prefix, prefixOK := computePrefixFairness(workspaceIDs, serviceWorkspaceOrder)
			if !prefixOK || !maxPrefixImbalance.add(prefix.MaxImbalance) ||
				!prefixGapArea.add(prefix.GapArea) {
				report.addFailure("FAIRNESS_OVERFLOW")
			}
			if prefix.LatestFirstServiceOrder != nil &&
				!latestFirstServiceOrder.add(*prefix.LatestFirstServiceOrder) {
				report.addFailure("FAIRNESS_OVERFLOW")
			}
		}

		var attemptCount, succeededAttemptCount uint64
		var reviewerRuns, reviewerAttempts, reviewerResults, reviewerVerdicts uint64
		attemptsByID := make(map[string]s3eval.AttemptFact)
		resultsSeen := make(map[string]struct{})
		for _, family := range repetition.Report.Families {
			report.Counts.Families++
			if family.Reviewer != nil {
				report.Counts.ReviewerRuns++
				reviewerRuns++
			}
			if family.WallElapsed < 0 || !wall.add(uint64(family.WallElapsed)) {
				report.addFailure("LATENCY_INVALID")
			}
			estimated.addSource(family.Costs.Estimated, report)
			providerReported.addSource(family.Costs.ProviderReported, report)
			reconciled.addSource(family.Costs.Reconciled, report)
			for _, attempt := range family.Attempts {
				report.Counts.Attempts++
				attemptCount++
				report.Models[modelBucket(attempt.Model)]++
				role := roleBucket(attempt.Role, report)
				report.AttemptRoles[role]++
				report.AttemptStates[stateBucket(attempt.State, report)]++
				if attempt.Role == corecontract.CompositeRunRoleReviewerV1 {
					report.Counts.ReviewerAttempts++
					reviewerAttempts++
				}
				if attempt.State == corecontract.ModelAttemptSucceeded {
					succeededAttemptCount++
				}
				if attempt.AttemptID == "" {
					report.addFailure("ATTEMPT_IDENTITY_INVALID")
				} else if _, duplicate := attemptsByID[attempt.AttemptID]; duplicate {
					report.addFailure("ATTEMPT_IDENTITY_INVALID")
				} else {
					attemptsByID[attempt.AttemptID] = attempt
				}
				if attempt.Elapsed < 0 || !attemptElapsed.add(uint64(attempt.Elapsed)) {
					report.addFailure("LATENCY_INVALID")
				}
				reasoningInvalid := attempt.Tokens.Reasoning != nil &&
					attempt.Tokens.Output != nil &&
					*attempt.Tokens.Reasoning > *attempt.Tokens.Output
				if err := attempt.Tokens.Validate(); err != nil || reasoningInvalid {
					report.addFailure("TOKENS_INVALID")
				} else {
					globalOK := tokens.add(attempt.Tokens)
					roleAccumulator, roleOK := roleCache[role]
					roleAdded := true
					if roleOK {
						roleAdded = roleAccumulator.add(attempt.Tokens)
					}
					if !globalOK || !roleAdded {
						report.addFailure("TOKENS_INVALID")
					}
				}
			}
			for _, result := range family.Results {
				report.Counts.Results++
				attempt, closesAttempt := attemptsByID[result.AttemptID]
				_, duplicateResult := resultsSeen[result.AttemptID]
				if !closesAttempt || attempt.State != corecontract.ModelAttemptSucceeded ||
					duplicateResult || attempt.RootRunID != family.RootRunID ||
					result.RunID != attempt.RunID || result.Role != attempt.Role ||
					result.SlotID != attempt.SlotID || result.ResultDigest == "" ||
					result.AssistantText == "" {
					report.addFailure("RESULT_CLOSURE_INVALID")
				} else {
					resultsSeen[result.AttemptID] = struct{}{}
				}
				if result.Role == corecontract.CompositeRunRoleReviewerV1 {
					report.Counts.ReviewerResults++
					reviewerResults++
				}
				if result.ReviewerVerdict != nil {
					report.Counts.ReviewerVerdicts++
					reviewerVerdicts++
					if result.Role != corecontract.CompositeRunRoleReviewerV1 {
						report.addFailure("REVIEW_VERDICT_INVALID")
					}
					report.Verdicts[verdictBucket(result.ReviewerVerdict.Decision, report)]++
				} else if result.Role == corecontract.CompositeRunRoleReviewerV1 {
					report.addFailure("REVIEW_VERDICT_INVALID")
				}
			}
			if len(family.Attempts) != len(family.Results) {
				report.addFailure("FAMILY_CLOSURE_INVALID")
			}
		}
		if uint64(len(repetition.Report.ServiceOrder)) != attemptCount ||
			uint64(len(repetition.DerivedCostEstimates)) != attemptCount {
			report.addFailure("ATTEMPT_COUNT_INVALID")
		}
		if reviewerRuns != reviewerAttempts || reviewerAttempts != reviewerResults ||
			reviewerResults != reviewerVerdicts {
			report.addFailure("REVIEWER_CLOSURE_INVALID")
		}
		if uint64(len(resultsSeen)) != succeededAttemptCount {
			report.addFailure("RESULT_CLOSURE_INVALID")
		}
		serviceSeen := make(map[string]struct{}, len(repetition.Report.ServiceOrder))
		for _, serviceAttempt := range repetition.Report.ServiceOrder {
			familyAttempt, exists := attemptsByID[serviceAttempt.AttemptID]
			_, duplicate := serviceSeen[serviceAttempt.AttemptID]
			if !exists || duplicate || !reflect.DeepEqual(familyAttempt, serviceAttempt) {
				report.addFailure("SERVICE_ORDER_INVALID")
				continue
			}
			serviceSeen[serviceAttempt.AttemptID] = struct{}{}
		}
		if len(serviceSeen) != len(attemptsByID) {
			report.addFailure("SERVICE_ORDER_INVALID")
		}
		derivedSeen := make(map[string]struct{}, len(repetition.DerivedCostEstimates))
		for _, estimate := range repetition.DerivedCostEstimates {
			attempt, exists := attemptsByID[estimate.AttemptID]
			_, duplicate := derivedSeen[estimate.AttemptID]
			if !exists || duplicate || estimate.WorkspaceID != attempt.WorkspaceID ||
				estimate.RootRunID != attempt.RootRunID || estimate.RunID != attempt.RunID ||
				estimate.Role != attempt.Role || estimate.Model != attempt.Model {
				report.addFailure("DERIVED_COST_SCOPE_INVALID")
			} else {
				derivedSeen[estimate.AttemptID] = struct{}{}
			}
			derived.addEstimate(estimate.Estimate, report)
		}
		if len(derivedSeen) != len(attemptsByID) {
			report.addFailure("DERIVED_COST_SCOPE_INVALID")
		}
	}

	report.Fairness.JainIndex = jain.report()
	report.Fairness.LongestConsecutive = longest.report()
	report.Fairness.MaxPrefixImbalance = maxPrefixImbalance.report()
	report.Fairness.PrefixGapArea = prefixGapArea.report()
	report.Fairness.LatestFirstServiceOrder = latestFirstServiceOrder.report()
	report.Latency.FamilyWallNanoseconds = wall.report()
	report.Latency.AttemptNanoseconds = attemptElapsed.report()
	report.Tokens = tokens.report()
	report.Tokens.CacheHitRatio = cacheHitRatio(report.Tokens.Totals)
	report.RoleCache = roleCache.report()
	if !validRoleCacheCoverage(roleCache, tokens, report.RoleCache) {
		report.addFailure("ROLE_CACHE_COVERAGE_INCONSISTENT")
	}
	report.Costs = CostSummary{
		Estimated: estimated.report(), ProviderReported: providerReported.report(),
		Reconciled: reconciled.report(), Derived: derived.report(),
	}
	if report.Counts.ReviewerRuns != report.Counts.ReviewerAttempts ||
		report.Counts.ReviewerAttempts != report.Counts.ReviewerResults ||
		report.Counts.ReviewerResults != report.Counts.ReviewerVerdicts {
		report.addFailure("REVIEWER_CLOSURE_INVALID")
	}
}

func (report *Report) verifyExit(exit sourceExit, reportBytes []byte, cellBase string) {
	if exit.SchemaVersion != cellExitSchemaV1 {
		report.addFailure("EXIT_SCHEMA_INVALID")
	}
	report.Archive.ExitComplete = exit.Status == "COMPLETE"
	report.Archive.ArchiveVerified = requiredTrue(exit.ArchiveVerified)
	if !report.Archive.ExitComplete {
		report.addFailure("EXIT_NOT_COMPLETE")
	}
	if exit.CellID == "" || exit.CellID != cellBase {
		report.addFailure("EXIT_CELL_ID_INVALID")
	}
	started, startedOK := parseCanonicalExitUTC(exit.StartedAtUTC)
	finished, finishedOK := parseCanonicalExitUTC(exit.FinishedAtUTC)
	if !startedOK || !finishedOK || finished.Before(started) {
		report.addFailure("EXIT_TIME_INVALID")
	}
	if report.Archive.ExitComplete {
		if !completePhases(exit.Phases) {
			report.addFailure("EXIT_PHASE_INVALID")
		}
		if !requiredEmpty(exit.FailureCode) || !requiredEmpty(exit.FailureExceptionType) ||
			!requiredTrue(exit.ReportCommitted) || !requiredTrue(exit.ArchiveVerified) ||
			!requiredFalse(exit.AutomaticRetry) || !requiredFalse(exit.ManualReviewRequired) ||
			!requiredTrue(exit.RawWorkRetained) ||
			!requiredFalse(exit.SensitiveCaptureCleanupFailed) {
			report.addFailure("EXIT_TERMINAL_METADATA_INVALID")
		}
	}
	if !requiredTrue(exit.ArchiveVerified) {
		report.addFailure("ARCHIVE_NOT_VERIFIED")
	}
	digest := sha256.Sum256(reportBytes)
	decoded, err := hex.DecodeString(exit.ReportSHA256)
	report.Archive.ReportSHA256Verified = err == nil && len(decoded) == sha256.Size &&
		bytes.Equal(decoded, digest[:]) && strings.ToLower(exit.ReportSHA256) == exit.ReportSHA256
	if !report.Archive.ReportSHA256Verified {
		report.addFailure("REPORT_SHA256_MISMATCH")
	}
}

func completePhases(phases sourcePhases) bool {
	for _, code := range []*int{
		phases.InitExitCode,
		phases.S3EvalExitCode,
		phases.BackupExitCode,
		phases.BackupVerifyExitCode,
	} {
		if code == nil || *code != 0 {
			return false
		}
	}
	return requiredFalse(phases.InitTimedOut) && requiredFalse(phases.InitCaptureFailed) &&
		requiredFalse(phases.S3EvalTimedOut) && requiredFalse(phases.S3EvalCaptureFailed) &&
		requiredFalse(phases.BackupTimedOut) && requiredFalse(phases.BackupCaptureFailed) &&
		requiredFalse(phases.BackupVerifyTimedOut) &&
		requiredFalse(phases.BackupVerifyCaptureFailed)
}

func requiredTrue(value *bool) bool  { return value != nil && *value }
func requiredFalse(value *bool) bool { return value != nil && !*value }
func requiredEmpty(value *string) bool {
	return value != nil && *value == ""
}

func parseCanonicalExitUTC(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	_, offset := parsed.Zone()
	// Runner writes DateTimeOffset.UtcNow.ToString("O"): exact seven-digit
	// fractional seconds and an explicit +00:00 offset.
	const runnerUTCLayout = "2006-01-02T15:04:05.0000000-07:00"
	if offset != 0 || parsed.UTC().Format(runnerUTCLayout) != value {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func newReport() Report {
	return Report{
		SchemaVersion: ReportSchemaVersionV2,
		Status:        "FAIL",
		Models:        make(map[string]uint64),
		AttemptRoles:  make(map[string]uint64),
		AttemptStates: make(map[string]uint64),
		Verdicts:      make(map[string]uint64),
		RoleCache:     newEmptyRoleCacheReport(),
		FailureCodes:  make([]string, 0),
	}
}

func (report *Report) addFailure(code string) {
	for _, existing := range report.FailureCodes {
		if existing == code {
			return
		}
	}
	report.FailureCodes = append(report.FailureCodes, code)
}

func (report Report) finish() Report {
	sort.Strings(report.FailureCodes)
	if len(report.FailureCodes) == 0 && report.Archive.ExitComplete &&
		report.Archive.ReportSHA256Verified && report.Archive.ArchiveVerified &&
		report.Archive.BundleVerified && report.Archive.BundleUnchanged &&
		report.Archive.StoreAuditVerified &&
		report.Archive.RoleCacheCrossChecked &&
		report.Archive.SourceUnchanged {
		report.Status = "PASS"
	}
	return report
}

func strictDecode(payload []byte, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func resolvePlainDirectory(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !samePath(absolute, resolved) {
		return "", false
	}
	info, err := os.Lstat(resolved)
	return resolved, err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func readPlainFile(path string, maximum int64) ([]byte, bool) {
	linkInfo, err := os.Lstat(path)
	if err != nil || !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 ||
		linkInfo.Size() <= 0 || linkInfo.Size() > maximum {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) {
		_ = file.Close()
		return nil, false
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	return payload, readErr == nil && closeErr == nil && len(payload) > 0 && int64(len(payload)) <= maximum
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func modelBucket(model string) string {
	switch model {
	case "deepseek-v4-flash", "deepseek-v4-pro":
		return model
	default:
		return "OTHER"
	}
}

func roleBucket(role corecontract.CompositeRunRoleV1, report *Report) string {
	switch role {
	case corecontract.CompositeRunRoleRootV1, corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1:
		return string(role)
	default:
		report.addFailure("ATTEMPT_ROLE_INVALID")
		return "OTHER"
	}
}

func stateBucket(state corecontract.ModelAttemptState, report *Report) string {
	if err := state.Validate(); err != nil {
		report.addFailure("ATTEMPT_STATE_INVALID")
		return "OTHER"
	}
	return string(state)
}

func verdictBucket(decision corecontract.ReviewDecisionV1, report *Report) string {
	switch decision {
	case corecontract.ReviewDecisionApproveV1, corecontract.ReviewDecisionRejectV1:
		return string(decision)
	default:
		report.addFailure("REVIEW_VERDICT_INVALID")
		return "OTHER"
	}
}

func sameFairness(left, right s3eval.FairnessReport) bool {
	if !sameOptionalFloat(left.JainIndex, right.JainIndex) ||
		left.FirstServedWorkspace != right.FirstServedWorkspace ||
		left.LongestConsecutiveWorkspace != right.LongestConsecutiveWorkspace ||
		left.LongestConsecutiveCount != right.LongestConsecutiveCount ||
		left.Starvation != right.Starvation ||
		len(left.StarvedWorkspaces) != len(right.StarvedWorkspaces) ||
		len(left.Workspaces) != len(right.Workspaces) {
		return false
	}
	for index := range left.StarvedWorkspaces {
		if left.StarvedWorkspaces[index] != right.StarvedWorkspaces[index] {
			return false
		}
	}
	for index := range left.Workspaces {
		if left.Workspaces[index].WorkspaceID != right.Workspaces[index].WorkspaceID ||
			left.Workspaces[index].ServiceCount != right.Workspaces[index].ServiceCount ||
			!sameOptionalUint(
				left.Workspaces[index].FirstServiceOrder,
				right.Workspaces[index].FirstServiceOrder,
			) {
			return false
		}
	}
	return true
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return !math.IsNaN(*left) && !math.IsNaN(*right) &&
		!math.IsInf(*left, 0) && !math.IsInf(*right, 0) &&
		math.Abs(*left-*right) <= 1e-12
}

func sameOptionalUint(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type prefixFairness struct {
	MaxImbalance            uint64
	GapArea                 uint64
	LatestFirstServiceOrder *uint64
}

// computePrefixFairness observes only per-Workspace counts. Workspace
// identities are temporary lookup keys and never cross the Report boundary.
func computePrefixFairness(
	workspaceIDs []string,
	serviceWorkspaceOrder []string,
) (prefixFairness, bool) {
	if len(workspaceIDs) == 0 {
		return prefixFairness{}, false
	}
	positions := make(map[string]int, len(workspaceIDs))
	for index, workspaceID := range workspaceIDs {
		if workspaceID == "" {
			return prefixFairness{}, false
		}
		if _, duplicate := positions[workspaceID]; duplicate {
			return prefixFairness{}, false
		}
		positions[workspaceID] = index
	}
	counts := make([]uint64, len(workspaceIDs))
	first := make([]uint64, len(workspaceIDs))
	seen := 0
	result := prefixFairness{}
	for index, workspaceID := range serviceWorkspaceOrder {
		position, exists := positions[workspaceID]
		if !exists || counts[position] == math.MaxUint64 {
			return prefixFairness{}, false
		}
		counts[position]++
		if first[position] == 0 {
			first[position] = uint64(index + 1)
			seen++
		}
		minimum, maximum := counts[0], counts[0]
		for _, count := range counts[1:] {
			if count < minimum {
				minimum = count
			}
			if count > maximum {
				maximum = count
			}
		}
		gap := maximum - minimum
		if gap > result.MaxImbalance {
			result.MaxImbalance = gap
		}
		if !addUint(&result.GapArea, gap) {
			return prefixFairness{}, false
		}
	}
	if seen == len(workspaceIDs) {
		latest := first[0]
		for _, order := range first[1:] {
			if order > latest {
				latest = order
			}
		}
		result.LatestFirstServiceOrder = &latest
	}
	return result, true
}

type uintAccumulator struct {
	count uint64
	min   uint64
	max   uint64
	total uint64
	valid bool
}

func newUintAccumulator() *uintAccumulator { return &uintAccumulator{valid: true} }

func (value *uintAccumulator) add(next uint64) bool {
	if !value.valid || value.count == math.MaxUint64 || math.MaxUint64-value.total < next {
		value.valid = false
		return false
	}
	if value.count == 0 || next < value.min {
		value.min = next
	}
	if value.count == 0 || next > value.max {
		value.max = next
	}
	value.count++
	value.total += next
	return true
}

func (value *uintAccumulator) report() UintStats {
	result := UintStats{Count: value.count}
	if value.count == 0 || !value.valid {
		return result
	}
	minimum, maximum, total := value.min, value.max, value.total
	mean := float64(value.total) / float64(value.count)
	result.Min, result.Max, result.Total, result.Mean = &minimum, &maximum, &total, &mean
	return result
}

type floatAccumulator struct {
	count uint64
	min   float64
	max   float64
	total float64
	valid bool
}

func newFloatAccumulator() *floatAccumulator { return &floatAccumulator{valid: true} }

func (value *floatAccumulator) add(next float64) bool {
	if !value.valid || math.IsNaN(next) || math.IsInf(next, 0) || next < 0 || next > 1 {
		value.valid = false
		return false
	}
	if value.count == 0 || next < value.min {
		value.min = next
	}
	if value.count == 0 || next > value.max {
		value.max = next
	}
	value.count++
	value.total += next
	return !math.IsInf(value.total, 0)
}

func (value *floatAccumulator) report() FloatStats {
	result := FloatStats{Known: value.count}
	if value.count == 0 || !value.valid {
		return result
	}
	minimum, maximum, mean := value.min, value.max, value.total/float64(value.count)
	result.Min, result.Max, result.Mean = &minimum, &maximum, &mean
	return result
}

type tokenAccumulator struct {
	count                uint64
	knownUsage           uint64
	cachedInputPositive  uint64
	rowsWithAnyTokenFact uint64
	fields               [5]tokenFieldAccumulator
}

func newTokenAccumulator() *tokenAccumulator {
	return &tokenAccumulator{}
}

func (value *tokenAccumulator) add(tokens corecontract.UsageTokens) bool {
	if value.count == math.MaxUint64 {
		return false
	}
	value.count++
	valid := true
	items := [5]*uint64{tokens.Input, tokens.CachedInput, tokens.UncachedInput, tokens.Output, tokens.Reasoning}
	for _, item := range items {
		if item != nil {
			if value.rowsWithAnyTokenFact == math.MaxUint64 {
				valid = false
			} else {
				value.rowsWithAnyTokenFact++
			}
			break
		}
	}
	if tokens.Input != nil && tokens.CachedInput != nil &&
		tokens.UncachedInput != nil && tokens.Output != nil {
		if value.knownUsage == math.MaxUint64 {
			valid = false
		} else {
			value.knownUsage++
		}
	}
	if tokens.CachedInput != nil && *tokens.CachedInput > 0 {
		if value.cachedInputPositive == math.MaxUint64 {
			valid = false
		} else {
			value.cachedInputPositive++
		}
	}
	for index, item := range items {
		if !value.fields[index].add(item) {
			valid = false
		}
	}
	return valid
}

func (value *tokenAccumulator) report() TokenSummary {
	complete := [5]*uint64{}
	subtotals := [5]*uint64{}
	coverage := [5]FieldCoverage{}
	for index := range value.fields {
		complete[index] = value.fields[index].completeTotal(value.count)
		subtotals[index] = value.fields[index].knownSubtotal()
		coverage[index] = value.fields[index].coverage()
	}
	return TokenSummary{
		Attempts: value.count, KnownUsageAttempts: value.knownUsage,
		CachedInputPositiveAttempts: value.cachedInputPositive,
		Totals: TokenTotals{
			Input: complete[0], CachedInput: complete[1], UncachedInput: complete[2],
			Output: complete[3], Reasoning: complete[4],
		},
		KnownSubtotals: TokenTotals{
			Input: subtotals[0], CachedInput: subtotals[1], UncachedInput: subtotals[2],
			Output: subtotals[3], Reasoning: subtotals[4],
		},
		Coverage: TokenCoverage{
			Input: coverage[0], CachedInput: coverage[1], UncachedInput: coverage[2],
			Output: coverage[3], Reasoning: coverage[4],
		},
		PerAttempt: TokenPerAttempt{
			Input: value.fields[0].stats(), CachedInput: value.fields[1].stats(),
			UncachedInput: value.fields[2].stats(), Output: value.fields[3].stats(),
		},
	}
}

type tokenFieldAccumulator struct {
	known      uint64
	unknown    uint64
	sum        uint64
	min        uint64
	max        uint64
	overflowed bool
}

func (value *tokenFieldAccumulator) add(next *uint64) bool {
	if next == nil {
		if value.unknown == math.MaxUint64 {
			value.overflowed = true
			return false
		}
		value.unknown++
		return true
	}
	if value.known == math.MaxUint64 || math.MaxUint64-value.sum < *next {
		value.overflowed = true
		return false
	}
	if value.known == 0 || *next < value.min {
		value.min = *next
	}
	if value.known == 0 || *next > value.max {
		value.max = *next
	}
	value.known++
	value.sum += *next
	return true
}

func (value tokenFieldAccumulator) completeTotal(attempts uint64) *uint64 {
	if attempts == 0 || value.overflowed || value.known != attempts || value.unknown != 0 {
		return nil
	}
	copy := value.sum
	return &copy
}

func (value tokenFieldAccumulator) knownSubtotal() *uint64 {
	if value.known == 0 || value.overflowed {
		return nil
	}
	copy := value.sum
	return &copy
}

func (value tokenFieldAccumulator) coverage() FieldCoverage {
	return FieldCoverage{Known: value.known, Unknown: value.unknown, Overflowed: value.overflowed}
}

func (value tokenFieldAccumulator) stats() UintStats {
	result := UintStats{Count: value.known}
	if value.known == 0 || value.overflowed {
		return result
	}
	minimum, maximum, total := value.min, value.max, value.sum
	mean := float64(value.sum) / float64(value.known)
	result.Min, result.Max, result.Total, result.Mean = &minimum, &maximum, &total, &mean
	return result
}

func cacheHitRatio(tokens TokenTotals) *float64 {
	if tokens.CachedInput == nil || tokens.UncachedInput == nil {
		return nil
	}
	cached := new(big.Int).SetUint64(*tokens.CachedInput)
	total := new(big.Int).Set(cached)
	total.Add(total, new(big.Int).SetUint64(*tokens.UncachedInput))
	if total.Sign() == 0 {
		return nil
	}
	ratio, _ := new(big.Rat).SetFrac(cached, total).Float64()
	return &ratio
}

type roleCacheAccumulator struct {
	tokens                 *tokenAccumulator
	cachedInputZeroRows    uint64
	cachedInputUnknownRows uint64
}

type roleCacheAccumulators map[string]*roleCacheAccumulator

func fixedRoleNames() []string {
	return []string{
		string(corecontract.CompositeRunRoleChildV1),
		string(corecontract.CompositeRunRoleReviewerV1),
		string(corecontract.CompositeRunRoleRootV1),
	}
}

func newRoleCacheAccumulators() roleCacheAccumulators {
	result := make(roleCacheAccumulators, len(fixedRoleNames()))
	for _, role := range fixedRoleNames() {
		result[role] = &roleCacheAccumulator{tokens: newTokenAccumulator()}
	}
	return result
}

func newEmptyRoleCacheReport() map[string]RoleCacheSummary {
	return newRoleCacheAccumulators().report()
}

func (value *roleCacheAccumulator) add(tokens corecontract.UsageTokens) bool {
	if value == nil || value.tokens == nil || !value.tokens.add(tokens) {
		return false
	}
	switch {
	case tokens.CachedInput == nil:
		if value.cachedInputUnknownRows == math.MaxUint64 {
			return false
		}
		value.cachedInputUnknownRows++
	case *tokens.CachedInput == 0:
		if value.cachedInputZeroRows == math.MaxUint64 {
			return false
		}
		value.cachedInputZeroRows++
	}
	return true
}

func (values roleCacheAccumulators) report() map[string]RoleCacheSummary {
	result := make(map[string]RoleCacheSummary, len(fixedRoleNames()))
	for _, role := range fixedRoleNames() {
		accumulator := values[role]
		if accumulator == nil || accumulator.tokens == nil {
			result[role] = RoleCacheSummary{}
			continue
		}
		tokens := accumulator.tokens.report()
		result[role] = RoleCacheSummary{
			Attempts:                   tokens.Attempts,
			UsageRowsWithAnyTokenFact:  accumulator.tokens.rowsWithAnyTokenFact,
			UsageRowsWithoutTokenFacts: tokens.Attempts - accumulator.tokens.rowsWithAnyTokenFact,
			Tokens:                     tokens.Totals,
			TokenFieldCoverage:         roleTokenCoverage(tokens.Coverage),
			KnownTokenSubtotal:         tokens.KnownSubtotals,
			CachedInputPositiveRows:    tokens.CachedInputPositiveAttempts,
			CachedInputZeroRows:        accumulator.cachedInputZeroRows,
			CachedInputUnknownRows:     accumulator.cachedInputUnknownRows,
			CacheHitRatio:              cacheHitRatio(tokens.Totals),
		}
	}
	return result
}

func roleTokenCoverage(value TokenCoverage) RoleTokenCoverage {
	convert := func(field FieldCoverage) TokenRowCoverage {
		return TokenRowCoverage{KnownRows: field.Known, UnknownRows: field.Unknown}
	}
	return RoleTokenCoverage{
		Input:         convert(value.Input),
		CachedInput:   convert(value.CachedInput),
		UncachedInput: convert(value.UncachedInput),
		Output:        convert(value.Output),
		Reasoning:     convert(value.Reasoning),
	}
}

func validRoleCacheCoverage(
	accumulators roleCacheAccumulators,
	global *tokenAccumulator,
	observed map[string]RoleCacheSummary,
) bool {
	if global == nil || len(accumulators) != len(fixedRoleNames()) ||
		len(observed) != len(fixedRoleNames()) {
		return false
	}
	var attempts, knownUsage, rowsWithFacts, cachedPositive uint64
	var knownRows, unknownRows, subtotals [5]uint64
	var subtotalPresent [5]bool
	for _, role := range fixedRoleNames() {
		accumulator, accumulatorOK := accumulators[role]
		summary, summaryOK := observed[role]
		if !accumulatorOK || !summaryOK || accumulator == nil ||
			accumulator.tokens == nil ||
			!validRoleCacheSummary(summary) ||
			!sameRoleCacheSummary(summary, accumulator.reportOne()) ||
			!addUint(&attempts, accumulator.tokens.count) ||
			!addUint(&knownUsage, accumulator.tokens.knownUsage) ||
			!addUint(&rowsWithFacts, accumulator.tokens.rowsWithAnyTokenFact) ||
			!addUint(&cachedPositive, accumulator.tokens.cachedInputPositive) {
			return false
		}
		for index, field := range accumulator.tokens.fields {
			if field.overflowed || !addUint(&knownRows[index], field.known) ||
				!addUint(&unknownRows[index], field.unknown) {
				return false
			}
			if field.known != 0 {
				subtotalPresent[index] = true
				if !addUint(&subtotals[index], field.sum) {
					return false
				}
			}
		}
	}
	if attempts != global.count || knownUsage != global.knownUsage ||
		rowsWithFacts != global.rowsWithAnyTokenFact ||
		cachedPositive != global.cachedInputPositive {
		return false
	}
	for index, field := range global.fields {
		if field.overflowed || knownRows[index] != field.known ||
			unknownRows[index] != field.unknown ||
			subtotalPresent[index] != (field.known != 0) ||
			field.known != 0 && subtotals[index] != field.sum {
			return false
		}
	}
	return true
}

func (value *roleCacheAccumulator) reportOne() RoleCacheSummary {
	if value == nil || value.tokens == nil {
		return RoleCacheSummary{}
	}
	tokens := value.tokens.report()
	return RoleCacheSummary{
		Attempts:                   tokens.Attempts,
		UsageRowsWithAnyTokenFact:  value.tokens.rowsWithAnyTokenFact,
		UsageRowsWithoutTokenFacts: tokens.Attempts - value.tokens.rowsWithAnyTokenFact,
		Tokens:                     tokens.Totals,
		TokenFieldCoverage:         roleTokenCoverage(tokens.Coverage),
		KnownTokenSubtotal:         tokens.KnownSubtotals,
		CachedInputPositiveRows:    tokens.CachedInputPositiveAttempts,
		CachedInputZeroRows:        value.cachedInputZeroRows,
		CachedInputUnknownRows:     value.cachedInputUnknownRows,
		CacheHitRatio:              cacheHitRatio(tokens.Totals),
	}
}

func validRoleCacheSummary(value RoleCacheSummary) bool {
	rows := value.UsageRowsWithAnyTokenFact
	if !addUint(&rows, value.UsageRowsWithoutTokenFacts) || rows != value.Attempts {
		return false
	}
	coverage := [5]TokenRowCoverage{
		value.TokenFieldCoverage.Input,
		value.TokenFieldCoverage.CachedInput,
		value.TokenFieldCoverage.UncachedInput,
		value.TokenFieldCoverage.Output,
		value.TokenFieldCoverage.Reasoning,
	}
	totals := [5]*uint64{
		value.Tokens.Input,
		value.Tokens.CachedInput,
		value.Tokens.UncachedInput,
		value.Tokens.Output,
		value.Tokens.Reasoning,
	}
	subtotals := [5]*uint64{
		value.KnownTokenSubtotal.Input,
		value.KnownTokenSubtotal.CachedInput,
		value.KnownTokenSubtotal.UncachedInput,
		value.KnownTokenSubtotal.Output,
		value.KnownTokenSubtotal.Reasoning,
	}
	for index, field := range coverage {
		fieldRows := field.KnownRows
		if !addUint(&fieldRows, field.UnknownRows) || fieldRows != value.Attempts ||
			(totals[index] != nil) != (value.Attempts != 0 && field.KnownRows == value.Attempts) ||
			(subtotals[index] != nil) != (field.KnownRows != 0) ||
			totals[index] != nil && *totals[index] != *subtotals[index] {
			return false
		}
	}
	cachedKnown := value.CachedInputPositiveRows
	if !addUint(&cachedKnown, value.CachedInputZeroRows) ||
		cachedKnown != value.TokenFieldCoverage.CachedInput.KnownRows {
		return false
	}
	classified := cachedKnown
	if !addUint(&classified, value.CachedInputUnknownRows) ||
		classified != value.Attempts ||
		value.CachedInputUnknownRows != value.TokenFieldCoverage.CachedInput.UnknownRows {
		return false
	}
	wantRatio := cacheHitRatio(value.Tokens)
	return sameOptionalFloat(wantRatio, value.CacheHitRatio)
}

func sameRoleCacheSummary(left, right RoleCacheSummary) bool {
	return reflect.DeepEqual(left, right)
}

func sameStoreRoleCache(
	cell map[string]RoleCacheSummary,
	store map[string]s3audit.RoleCacheObservation,
) bool {
	if len(cell) != len(fixedRoleNames()) || len(store) != len(fixedRoleNames()) {
		return false
	}
	for _, role := range fixedRoleNames() {
		cellValue, cellOK := cell[role]
		storeValue, storeOK := store[role]
		if !cellOK || !storeOK || !validRoleCacheSummary(cellValue) ||
			!sameRoleCacheSummary(cellValue, roleCacheFromStore(storeValue)) {
			return false
		}
	}
	return true
}

func roleCacheFromStore(value s3audit.RoleCacheObservation) RoleCacheSummary {
	convertTotals := func(source s3audit.TokenTotals) TokenTotals {
		return TokenTotals{
			Input: source.Input, CachedInput: source.CachedInput,
			UncachedInput: source.UncachedInput, Output: source.Output,
			Reasoning: source.Reasoning,
		}
	}
	convertCoverage := func(source s3audit.TokenFieldCoverage) RoleTokenCoverage {
		convert := func(field s3audit.TokenFieldRowCoverage) TokenRowCoverage {
			return TokenRowCoverage{KnownRows: field.KnownRows, UnknownRows: field.UnknownRows}
		}
		return RoleTokenCoverage{
			Input: convert(source.Input), CachedInput: convert(source.CachedInput),
			UncachedInput: convert(source.UncachedInput), Output: convert(source.Output),
			Reasoning: convert(source.Reasoning),
		}
	}
	return RoleCacheSummary{
		Attempts:                   value.Attempts,
		UsageRowsWithAnyTokenFact:  value.UsageRowsWithAnyTokenFact,
		UsageRowsWithoutTokenFacts: value.UsageRowsWithoutTokenFacts,
		Tokens:                     convertTotals(value.Tokens),
		TokenFieldCoverage:         convertCoverage(value.TokenFieldCoverage),
		KnownTokenSubtotal:         convertTotals(value.KnownTokenSubtotal),
		CachedInputPositiveRows:    value.CachedInputPositiveRows,
		CachedInputZeroRows:        value.CachedInputZeroRows,
		CachedInputUnknownRows:     value.CachedInputUnknownRows,
		CacheHitRatio:              value.CacheHitRatio,
	}
}

type costAccumulator struct {
	count           uint64
	coverage        CostCoverage
	knownByCurrency map[string]*big.Rat
	mixedCurrencies map[string]struct{}
}

func newCostAccumulator() *costAccumulator {
	return &costAccumulator{
		knownByCurrency: make(map[string]*big.Rat),
		mixedCurrencies: make(map[string]struct{}),
	}
}

func (value *costAccumulator) addSource(source s3eval.CostTotalReport, report *Report) {
	switch source.Status {
	case currentstore.CompositeFamilyCostUnknownV1:
		if source.Value != nil || source.Currency != "" || len(source.Currencies) != 0 {
			report.addFailure("COST_INVALID")
			value.addInvalid(report)
			return
		}
		value.addUnknown(report)
	case currentstore.CompositeFamilyCostKnownV1:
		if len(source.Currencies) != 0 {
			report.addFailure("COST_INVALID")
			value.addInvalid(report)
			return
		}
		value.addKnown(source.Value, source.Currency, report)
	case currentstore.CompositeFamilyCostMixedCurrencyV1:
		if source.Value != nil || source.Currency != "" || len(source.Currencies) < 2 {
			report.addFailure("COST_INVALID")
			value.addInvalid(report)
			return
		}
		for index, currency := range source.Currencies {
			if !safeCurrency(currency) {
				report.addFailure("COST_INVALID")
				value.addInvalid(report)
				return
			}
			if index != 0 && source.Currencies[index-1] >= currency {
				report.addFailure("COST_INVALID")
				value.addInvalid(report)
				return
			}
		}
		value.addMixed(source.Currencies, report)
	default:
		report.addFailure("COST_INVALID")
		value.addInvalid(report)
	}
}

func (value *costAccumulator) addEstimate(source deepseekcost.Estimate, report *Report) {
	switch source.Status {
	case deepseekcost.StatusUnknown:
		if source.Value != nil {
			report.addFailure("COST_INVALID")
			value.addInvalid(report)
			return
		}
		value.addUnknown(report)
	case deepseekcost.StatusKnown:
		value.addKnown(source.Value, source.Currency, report)
	default:
		report.addFailure("COST_INVALID")
		value.addInvalid(report)
	}
}

func (value *costAccumulator) addKnown(amount *string, currency string, report *Report) {
	if amount == nil || !safeCurrency(currency) || !safeDecimal(pointerText(amount)) {
		report.addFailure("COST_INVALID")
		value.addInvalid(report)
		return
	}
	parsed, ok := new(big.Rat).SetString(*amount)
	if !ok || parsed.Sign() < 0 {
		report.addFailure("COST_INVALID")
		value.addInvalid(report)
		return
	}
	if !value.incrementCount(report) {
		return
	}
	if !incrementCoverage(&value.coverage.Known) {
		report.addFailure("COUNT_OVERFLOW")
		return
	}
	total := value.knownByCurrency[currency]
	if total == nil {
		total = new(big.Rat)
		value.knownByCurrency[currency] = total
	}
	total.Add(total, parsed)
}

func (value *costAccumulator) addUnknown(report *Report) {
	if !value.incrementCount(report) {
		return
	}
	if !incrementCoverage(&value.coverage.Unknown) {
		report.addFailure("COUNT_OVERFLOW")
	}
}

func (value *costAccumulator) addMixed(currencies []string, report *Report) {
	if !value.incrementCount(report) {
		return
	}
	if !incrementCoverage(&value.coverage.MixedCurrency) {
		report.addFailure("COUNT_OVERFLOW")
		return
	}
	for _, currency := range currencies {
		value.mixedCurrencies[currency] = struct{}{}
	}
}

func (value *costAccumulator) addInvalid(report *Report) {
	if !value.incrementCount(report) {
		return
	}
	if !incrementCoverage(&value.coverage.Invalid) {
		report.addFailure("COUNT_OVERFLOW")
	}
}

func (value *costAccumulator) incrementCount(report *Report) bool {
	if value.count == math.MaxUint64 {
		report.addFailure("COUNT_OVERFLOW")
		return false
	}
	value.count++
	return true
}

func incrementCoverage(target *uint64) bool {
	if *target == math.MaxUint64 {
		return false
	}
	*target++
	return true
}

func (value *costAccumulator) report() CostAggregate {
	subtotal := costTotalFromKnown(value.knownByCurrency)
	total := subtotal
	if value.count == 0 || value.coverage.Unknown != 0 || value.coverage.Invalid != 0 {
		total = unknownCostTotal()
	} else if value.coverage.MixedCurrency != 0 {
		currencies := make(map[string]struct{}, len(value.mixedCurrencies)+len(value.knownByCurrency))
		for currency := range value.mixedCurrencies {
			currencies[currency] = struct{}{}
		}
		for currency := range value.knownByCurrency {
			currencies[currency] = struct{}{}
		}
		total = mixedCostTotal(currencies)
	}
	coverage := value.coverage
	coverage.Entries = value.count
	return CostAggregate{Total: total, KnownSubtotal: subtotal, Coverage: coverage}
}

func costTotalFromKnown(values map[string]*big.Rat) CostTotal {
	if len(values) == 0 {
		return unknownCostTotal()
	}
	if len(values) != 1 {
		currencies := make(map[string]struct{}, len(values))
		for currency := range values {
			currencies[currency] = struct{}{}
		}
		return mixedCostTotal(currencies)
	}
	for currency, total := range values {
		amount, ok := finiteDecimal(total)
		if !ok {
			return unknownCostTotal()
		}
		return CostTotal{Status: "KNOWN", Value: &amount, Currency: currency, Currencies: []string{}}
	}
	return unknownCostTotal()
}

func unknownCostTotal() CostTotal {
	return CostTotal{Status: "UNKNOWN", Currencies: []string{}}
}

func mixedCostTotal(values map[string]struct{}) CostTotal {
	currencies := make([]string, 0, len(values))
	for currency := range values {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	return CostTotal{Status: "MIXED_CURRENCY", Currencies: currencies}
}

func pointerText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func safeCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func safeDecimal(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	dot := false
	digitsAfterDot := 0
	for _, character := range value {
		if character == '.' && !dot {
			dot = true
			continue
		}
		if character < '0' || character > '9' {
			return false
		}
		if dot {
			digitsAfterDot++
		}
	}
	return value[0] != '.' && (!dot || digitsAfterDot != 0)
}

func finiteDecimal(value *big.Rat) (string, bool) {
	if value == nil || value.Sign() < 0 {
		return "", false
	}
	denominator := new(big.Int).Set(value.Denom())
	two, five, remainder := big.NewInt(2), big.NewInt(5), new(big.Int)
	twos, fives := 0, 0
	for {
		quotient, rest := new(big.Int).QuoRem(denominator, two, remainder)
		if rest.Sign() != 0 {
			break
		}
		denominator = quotient
		twos++
	}
	for {
		quotient, rest := new(big.Int).QuoRem(denominator, five, remainder)
		if rest.Sign() != 0 {
			break
		}
		denominator = quotient
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", false
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	numerator := new(big.Int).Set(value.Num())
	if twos < scale {
		numerator.Mul(numerator, new(big.Int).Exp(two, big.NewInt(int64(scale-twos)), nil))
	}
	if fives < scale {
		numerator.Mul(numerator, new(big.Int).Exp(five, big.NewInt(int64(scale-fives)), nil))
	}
	digits := numerator.String()
	if scale == 0 {
		return digits, true
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	integer := digits[:len(digits)-scale]
	fraction := strings.TrimRight(digits[len(digits)-scale:], "0")
	if fraction == "" {
		return integer, true
	}
	return integer + "." + fraction, true
}

func addUint(target *uint64, next uint64) bool {
	if math.MaxUint64-*target < next {
		return false
	}
	*target += next
	return true
}
