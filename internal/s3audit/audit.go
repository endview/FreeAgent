// Package s3audit recovers aggregate S3-C evidence from one closed Current
// Store. It is an operator-only, read-only observer: it creates no Runtime,
// Store, table, ledger, Run, Attempt, request, or provider call.
package s3audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"math"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
)

const (
	// ReportSchemaVersionV1 identifies historical reports whose fixed
	// MODEL_UNKNOWN bucket set predates boundary observation classes.
	ReportSchemaVersionV1 = "freeagent.s3-store-audit/v1"
	// ReportSchemaVersionV2 freezes the safe boundary-observation buckets and
	// the fixed CHILD/REVIEWER/ROOT role_cache projection.
	ReportSchemaVersionV2 = "freeagent.s3-store-audit/v2"
	// ReportSchemaVersionV3 removes retired monetary observations while
	// preserving token coverage and boundary evidence.
	ReportSchemaVersionV3 = "freeagent.s3-store-audit/v3"
)

const (
	modelUnknownReasonModelUnknown        = "MODEL_UNKNOWN"
	modelUnknownReasonRecoveredPending    = "RECOVERED_PENDING_AFTER_CRASH"
	modelUnknownReasonHostError           = "HOST_ERROR_AFTER_PENDING"
	modelUnknownReasonInvalidResult       = "INVALID_PROVIDER_RESULT"
	modelUnknownReasonInvokeReturnedError = string(modulehost.UnknownClassInvokeReturnedError)
	modelUnknownReasonNoUsableResponse    = string(modulehost.UnknownClassNoUsableResponse)
	modelUnknownReasonBodyReadIncomplete  = string(modulehost.UnknownClassResponseBodyReadIncomplete)
	modelUnknownReasonOther               = "OTHER"
)

var ErrAuditFailed = errors.New("s3audit: closed Store audit failed")

type Expectations struct {
	Families            uint64
	Runs                uint64
	Attempts            uint64
	RequireAllTerminal  bool
	RequireAllSucceeded bool
}

type TokenTotals struct {
	Input         *uint64 `json:"input_tokens"`
	CachedInput   *uint64 `json:"cached_input_tokens"`
	UncachedInput *uint64 `json:"uncached_input_tokens"`
	Output        *uint64 `json:"output_tokens"`
	Reasoning     *uint64 `json:"reasoning_tokens"`
}

type TokenFieldRowCoverage struct {
	KnownRows   uint64 `json:"known_rows"`
	UnknownRows uint64 `json:"unknown_rows"`
}

type TokenFieldCoverage struct {
	Input         TokenFieldRowCoverage `json:"input_tokens"`
	CachedInput   TokenFieldRowCoverage `json:"cached_input_tokens"`
	UncachedInput TokenFieldRowCoverage `json:"uncached_input_tokens"`
	Output        TokenFieldRowCoverage `json:"output_tokens"`
	Reasoning     TokenFieldRowCoverage `json:"reasoning_tokens"`
}

// RoleCacheObservation is a redacted per-role cache projection. Zero Attempt
// counts are facts; absent token facts remain nil and are never synthesized as
// known zero usage.
type RoleCacheObservation struct {
	Attempts                   uint64             `json:"attempts"`
	UsageRowsWithAnyTokenFact  uint64             `json:"usage_rows_with_any_token_fact"`
	UsageRowsWithoutTokenFacts uint64             `json:"usage_rows_without_token_facts"`
	Tokens                     TokenTotals        `json:"tokens"`
	TokenFieldCoverage         TokenFieldCoverage `json:"token_field_coverage"`
	KnownTokenSubtotal         TokenTotals        `json:"known_token_subtotal"`
	CachedInputPositiveRows    uint64             `json:"cached_input_positive_rows"`
	CachedInputZeroRows        uint64             `json:"cached_input_zero_rows"`
	CachedInputUnknownRows     uint64             `json:"cached_input_unknown_rows"`
	CacheHitRatio              *float64           `json:"cache_hit_ratio"`
}

type ExpectedReport struct {
	Families            uint64 `json:"families"`
	Runs                uint64 `json:"runs"`
	Attempts            uint64 `json:"attempts"`
	RequireAllTerminal  bool   `json:"require_all_terminal"`
	RequireAllSucceeded bool   `json:"require_all_succeeded"`
}

type ObservedReport struct {
	Families           uint64 `json:"families"`
	Runs               uint64 `json:"runs"`
	TerminalRuns       uint64 `json:"terminal_runs"`
	NonterminalRuns    uint64 `json:"nonterminal_runs"`
	InvalidRunClosures uint64 `json:"invalid_run_closures"`
	VerifiedResults    uint64 `json:"verified_results"`
	RunsWithoutAttempt uint64 `json:"runs_without_attempt"`
	Attempts           uint64 `json:"attempts"`
	UsageRows          uint64 `json:"usage_rows"`
	// A row is "with any token fact" when at least one of the five independent
	// token fields is present. TokenFieldCoverage preserves per-field UNKNOWN;
	// KnownTokenSubtotal never fills a missing field with zero.
	UsageRowsWithAnyTokenFact  uint64                          `json:"usage_rows_with_any_token_fact"`
	UsageRowsWithoutTokenFacts uint64                          `json:"usage_rows_without_token_facts"`
	Roles                      map[string]uint64               `json:"roles"`
	AttemptStates              map[string]uint64               `json:"attempt_states"`
	Providers                  map[string]uint64               `json:"providers"`
	Models                     map[string]uint64               `json:"models"`
	UsageStatuses              map[string]uint64               `json:"usage_statuses"`
	ModelUnknownReasons        map[string]uint64               `json:"model_unknown_reasons"`
	RoleCache                  map[string]RoleCacheObservation `json:"role_cache"`
	Tokens                     TokenTotals                     `json:"tokens"`
	TokenFieldCoverage         TokenFieldCoverage              `json:"token_field_coverage"`
	KnownTokenSubtotal         TokenTotals                     `json:"known_token_subtotal"`
	CacheHitRatio              *float64                        `json:"cache_hit_ratio"`
}

// Report intentionally contains no Run/Attempt/workspace ID, content digest,
// request/result text, header, receipt, or raw provider payload.
type Report struct {
	SchemaVersion        string         `json:"schema_version"`
	Status               string         `json:"status"`
	CurrentStoreVerified bool           `json:"current_store_verified"`
	NoSidecarsVerified   bool           `json:"no_sidecars_verified"`
	SourceUnchanged      bool           `json:"source_unchanged"`
	Expected             ExpectedReport `json:"expected"`
	Observed             ObservedReport `json:"observed"`
	FailureCodes         []string       `json:"failure_codes"`
}

type observer interface {
	ListCompositeRootRunIDs(context.Context) ([]string, error)
	GetCompositeFamilyUsageProjection(
		context.Context,
		string,
	) (currentstore.CompositeFamilyUsageProjectionV1, error)
	GetTerminalRunResult(
		context.Context,
		string,
	) (currentstore.TerminalRunResult, error)
	GetFairRunTargetView(
		context.Context,
		string,
	) (currentstore.FairRunTargetView, error)
	GetModelUnknownReason(context.Context, string) (string, error)
}

// closedStoreObserver adds one narrowly scoped immutable query to the public
// Current Store observer. It exists only because the aggregate Composite Usage
// projection intentionally does not expose model_dispatch_attempts.unknown_reason.
// The raw value is classified immediately and never leaves this package.
type closedStoreObserver struct {
	*currentstore.ReadOnlyObserver
	reasonDatabase *sql.DB
}

func openClosedStoreObserver(
	ctx context.Context,
	path string,
) (*closedStoreObserver, error) {
	storeObserver, err := currentstore.OpenReadOnlyObserver(ctx, path)
	if err != nil {
		return nil, err
	}
	reasonDatabase, err := openImmutableReasonDatabase(ctx, path)
	if err != nil {
		_ = storeObserver.Close()
		return nil, err
	}
	return &closedStoreObserver{
		ReadOnlyObserver: storeObserver,
		reasonDatabase:   reasonDatabase,
	}, nil
}

func (observer *closedStoreObserver) Close() error {
	if observer == nil {
		return nil
	}
	var reasonErr error
	if observer.reasonDatabase != nil {
		reasonErr = observer.reasonDatabase.Close()
		observer.reasonDatabase = nil
	}
	var storeErr error
	if observer.ReadOnlyObserver != nil {
		storeErr = observer.ReadOnlyObserver.Close()
		observer.ReadOnlyObserver = nil
	}
	return errors.Join(reasonErr, storeErr)
}

func (observer *closedStoreObserver) GetModelUnknownReason(
	ctx context.Context,
	attemptID string,
) (string, error) {
	if observer == nil || observer.reasonDatabase == nil {
		return "", errors.New("s3audit: unknown-reason observer is closed")
	}
	var reason sql.NullString
	if err := observer.reasonDatabase.QueryRowContext(ctx, `
		SELECT unknown_reason
		FROM model_dispatch_attempts
		WHERE attempt_id=? AND state='MODEL_UNKNOWN'
	`, attemptID).Scan(&reason); err != nil {
		return "", errors.New("s3audit: MODEL_UNKNOWN reason projection failed")
	}
	if !reason.Valid {
		return "", nil
	}
	return reason.String, nil
}

// AuditClosedStore observes one operator-declared closed, self-contained
// SQLite Current Store. It rejects WAL/SHM/journal sidecars, compares source
// bytes before and after, and opens only immutable, query-only observers.
// No-sidecar and unchanged-byte checks do not prove that an otherwise idle
// writer process is absent.
func AuditClosedStore(
	ctx context.Context,
	path string,
	expectations Expectations,
) (Report, error) {
	report := newReport(expectations)
	if ctx == nil || strings.TrimSpace(path) == "" {
		report.addFailure("INVALID_INPUT")
		return report.finish(), ErrAuditFailed
	}
	if !selfContainedSQLiteSource(path) {
		report.addFailure("SOURCE_NOT_CLOSED")
		return report.finish(), ErrAuditFailed
	}
	before, err := fileSHA256(path)
	if err != nil {
		report.addFailure("SOURCE_HASH_FAILED")
		return report.finish(), ErrAuditFailed
	}
	reader, err := openClosedStoreObserver(ctx, path)
	if err != nil {
		report.addFailure("CURRENT_STORE_VERIFY_FAILED")
		return report.finish(), ErrAuditFailed
	}
	report.CurrentStoreVerified = true
	report.NoSidecarsVerified = true
	auditErr := auditObserver(ctx, reader, &report)
	closeErr := reader.Close()
	after, hashErr := fileSHA256(path)
	report.SourceUnchanged = hashErr == nil && before == after &&
		selfContainedSQLiteSource(path)
	if !report.SourceUnchanged {
		report.addFailure("SOURCE_CHANGED_DURING_AUDIT")
	}
	if closeErr != nil {
		report.addFailure("OBSERVER_CLOSE_FAILED")
	}
	report.applyExpectations()
	report = report.finish()
	if auditErr != nil || closeErr != nil || hashErr != nil ||
		report.Status != "PASS" {
		return report, ErrAuditFailed
	}
	return report, nil
}

func auditObserver(ctx context.Context, reader observer, report *Report) error {
	rootRunIDs, err := reader.ListCompositeRootRunIDs(ctx)
	if err != nil {
		report.addFailure("ROOT_DISCOVERY_FAILED")
		return ErrAuditFailed
	}
	report.Observed.Families = uint64(len(rootRunIDs))
	tokens := newTokenAccumulator()
	roleCache := newRoleCacheAccumulators()
	for _, rootRunID := range rootRunIDs {
		projection, err := reader.GetCompositeFamilyUsageProjection(ctx, rootRunID)
		if err != nil {
			report.addFailure("FAMILY_PROJECTION_INVALID")
			continue
		}
		for _, run := range projection.Runs {
			report.Observed.Runs++
			roleName := string(run.Role)
			roleAccumulator, roleKnown := roleCache[roleName]
			if !roleKnown {
				report.addFailure("ROLE_CACHE_ROLE_INVALID")
				continue
			}
			report.Observed.Roles[roleName]++
			lifecycle, lifecycleErr := reader.GetFairRunTargetView(ctx, run.RunID)
			if lifecycleErr != nil {
				report.Observed.InvalidRunClosures++
				report.addFailure("RUN_LIFECYCLE_INVALID")
			} else if lifecycle.Disposition != corecontract.TerminatedLoopStep {
				// WAITING_RECONCILIATION, including MODEL_UNKNOWN, is a
				// legitimate non-terminal state. Do not ask the terminal
				// projection to reinterpret it as an integrity failure.
				report.Observed.NonterminalRuns++
			} else {
				terminal, terminalErr := reader.GetTerminalRunResult(ctx, run.RunID)
				if terminalErr != nil || !validTerminalClosure(run, terminal) {
					report.Observed.InvalidRunClosures++
					report.addFailure("RUN_TERMINAL_CLOSURE_INVALID")
				} else {
					report.Observed.TerminalRuns++
					if run.Attempt != nil &&
						run.Attempt.State == corecontract.ModelAttemptSucceeded {
						report.Observed.VerifiedResults++
					}
				}
			}
			if run.Attempt == nil {
				report.Observed.RunsWithoutAttempt++
				continue
			}
			attempt := run.Attempt
			report.Observed.Attempts++
			report.Observed.UsageRows++
			roleAccumulator.add(attempt.Usage.Tokens)
			report.Observed.AttemptStates[string(attempt.State)]++
			report.Observed.Providers[attempt.Provider]++
			report.Observed.Models[attempt.Model]++
			report.Observed.UsageStatuses[attempt.Usage.UsageStatus]++
			if attempt.State == corecontract.ModelAttemptUnknown {
				reason, reasonErr := reader.GetModelUnknownReason(
					ctx,
					attempt.AttemptID,
				)
				if reasonErr != nil {
					report.addFailure("MODEL_UNKNOWN_REASON_INVALID")
					reason = ""
				}
				report.Observed.ModelUnknownReasons[classifyModelUnknownReason(reason)]++
			}
			tokens.add(attempt.Usage.Tokens)
		}
	}
	report.Observed.Tokens = tokens.report()
	report.Observed.RoleCache = roleCache.report()
	report.Observed.UsageRowsWithAnyTokenFact = tokens.rowsWithAnyFact
	report.Observed.UsageRowsWithoutTokenFacts =
		tokens.count - tokens.rowsWithAnyFact
	report.Observed.TokenFieldCoverage = tokens.coverage()
	report.Observed.KnownTokenSubtotal = tokens.knownSubtotal()
	if tokens.overflow {
		report.addFailure("TOKEN_TOTAL_OVERFLOW")
	}
	if !validRowCoverage(
		report.Observed.UsageRowsWithAnyTokenFact,
		report.Observed.UsageRowsWithoutTokenFacts,
		report.Observed.UsageRows,
	) ||
		!validTokenFieldCoverage(
			report.Observed.TokenFieldCoverage,
			report.Observed.UsageRows,
		) {
		report.addFailure("USAGE_COVERAGE_INCONSISTENT")
	}
	if !validRoleCacheCoverage(
		report.Observed.RoleCache,
		report.Observed.Attempts,
		report.Observed.UsageRowsWithAnyTokenFact,
		report.Observed.UsageRowsWithoutTokenFacts,
		report.Observed.TokenFieldCoverage,
		report.Observed.KnownTokenSubtotal,
		report.Observed.Roles,
	) {
		report.addFailure("ROLE_CACHE_COVERAGE_INCONSISTENT")
	}
	if !validModelUnknownReasonCoverage(
		report.Observed.ModelUnknownReasons,
		report.Observed.AttemptStates[string(corecontract.ModelAttemptUnknown)],
	) {
		report.addFailure("MODEL_UNKNOWN_REASON_COVERAGE_INCONSISTENT")
	}
	report.Observed.CacheHitRatio = cacheHitRatio(report.Observed.Tokens)
	if len(report.FailureCodes) != 0 {
		return ErrAuditFailed
	}
	return nil
}

func validTerminalClosure(
	run currentstore.CompositeFamilyRunUsageFactV1,
	terminal currentstore.TerminalRunResult,
) bool {
	if terminal.RunID != run.RunID || terminal.ReasonCode == "" {
		return false
	}
	if run.Attempt == nil {
		return terminal.AttemptID == "" && terminal.AttemptKind == "" &&
			terminal.ErrorClassification != "" &&
			terminal.ReasonCode == terminal.ErrorClassification
	}
	attempt := run.Attempt
	if terminal.AttemptID != attempt.AttemptID ||
		terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != attempt.State || terminal.ModelState != attempt.State {
		return false
	}
	switch attempt.State {
	case corecontract.ModelAttemptSucceeded:
		return terminal.ErrorClassification == "" &&
			terminal.ReasonCode == "MODEL_SUCCEEDED" &&
			len(terminal.OutputCanonical) != 0 &&
			terminal.Output.ActionRequest == nil
	case corecontract.ModelAttemptFailed:
		return terminal.ErrorClassification != "" &&
			terminal.ReasonCode == "MODEL_FAILED" &&
			len(terminal.OutputCanonical) == 0
	default:
		return false
	}
}

func newReport(expectations Expectations) Report {
	return Report{
		SchemaVersion: ReportSchemaVersionV3,
		Status:        "FAIL",
		Expected: ExpectedReport{
			Families:            expectations.Families,
			Runs:                expectations.Runs,
			Attempts:            expectations.Attempts,
			RequireAllTerminal:  expectations.RequireAllTerminal,
			RequireAllSucceeded: expectations.RequireAllSucceeded,
		},
		Observed: ObservedReport{
			Roles:               newRoleCounts(),
			AttemptStates:       make(map[string]uint64),
			Providers:           make(map[string]uint64),
			Models:              make(map[string]uint64),
			UsageStatuses:       make(map[string]uint64),
			ModelUnknownReasons: newModelUnknownReasonCounts(),
			RoleCache:           newEmptyRoleCacheReport(),
		},
		FailureCodes: make([]string, 0),
	}
}

func newModelUnknownReasonCounts() map[string]uint64 {
	return map[string]uint64{
		modelUnknownReasonModelUnknown:        0,
		modelUnknownReasonRecoveredPending:    0,
		modelUnknownReasonHostError:           0,
		modelUnknownReasonInvalidResult:       0,
		modelUnknownReasonInvokeReturnedError: 0,
		modelUnknownReasonNoUsableResponse:    0,
		modelUnknownReasonBodyReadIncomplete:  0,
		modelUnknownReasonOther:               0,
	}
}

func classifyModelUnknownReason(reason string) string {
	switch reason {
	case modelUnknownReasonModelUnknown,
		modelUnknownReasonRecoveredPending,
		modelUnknownReasonHostError,
		modelUnknownReasonInvalidResult,
		modelUnknownReasonInvokeReturnedError,
		modelUnknownReasonNoUsableResponse,
		modelUnknownReasonBodyReadIncomplete:
		return reason
	default:
		return modelUnknownReasonOther
	}
}

func validModelUnknownReasonCoverage(
	counts map[string]uint64,
	want uint64,
) bool {
	if len(counts) != len(newModelUnknownReasonCounts()) {
		return false
	}
	var observed uint64
	for reason := range newModelUnknownReasonCounts() {
		count, found := counts[reason]
		if !found || math.MaxUint64-observed < count {
			return false
		}
		observed += count
	}
	return observed == want
}

func (report *Report) applyExpectations() {
	if report.Expected.Families != 0 &&
		report.Observed.Families != report.Expected.Families {
		report.addFailure("EXPECTED_FAMILY_COUNT_MISMATCH")
	}
	if report.Expected.Runs != 0 && report.Observed.Runs != report.Expected.Runs {
		report.addFailure("EXPECTED_RUN_COUNT_MISMATCH")
	}
	if report.Expected.Attempts != 0 &&
		report.Observed.Attempts != report.Expected.Attempts {
		report.addFailure("EXPECTED_ATTEMPT_COUNT_MISMATCH")
	}
	if report.Expected.RequireAllTerminal &&
		report.Observed.TerminalRuns != report.Observed.Runs {
		report.addFailure("NOT_ALL_RUNS_TERMINAL")
	}
	if report.Expected.RequireAllSucceeded &&
		report.Observed.AttemptStates[string(corecontract.ModelAttemptSucceeded)] !=
			report.Observed.Attempts {
		report.addFailure("NOT_ALL_ATTEMPTS_SUCCEEDED")
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
	if len(report.FailureCodes) == 0 && report.CurrentStoreVerified &&
		report.NoSidecarsVerified && report.SourceUnchanged {
		report.Status = "PASS"
	} else {
		report.Status = "FAIL"
	}
	return report
}

func selfContainedSQLiteSource(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil ||
			!errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func openImmutableReasonDatabase(ctx context.Context, path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("s3audit: resolve reason observer source")
	}
	normalized := filepath.ToSlash(filepath.Clean(absolute))
	if runtime.GOOS == "windows" && filepath.VolumeName(absolute) != "" &&
		!strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	uri := url.URL{Scheme: "file", Path: normalized}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "trusted_schema(0)")
	query.Add("_pragma", "busy_timeout(0)")
	uri.RawQuery = query.Encode()

	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, errors.New("s3audit: open reason observer")
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, errors.New("s3audit: acquire reason observer")
	}
	valid := true
	for pragma, expected := range map[string]int{
		"query_only":     1,
		"foreign_keys":   1,
		"trusted_schema": 0,
	} {
		var actual int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA "+pragma,
		).Scan(&actual); err != nil || actual != expected {
			valid = false
			break
		}
	}
	closeErr := connection.Close()
	if !valid || closeErr != nil {
		_ = database.Close()
		return nil, errors.New("s3audit: reason observer is not query-only")
	}
	return database, nil
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, err
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

type tokenAccumulator struct {
	count           uint64
	rowsWithAnyFact uint64
	totalSums       [5]uint64
	totalKnown      [5]bool
	subtotalSums    [5]uint64
	fieldKnownRows  [5]uint64
	subtotalValid   [5]bool
	overflow        bool
}

func newTokenAccumulator() *tokenAccumulator {
	return &tokenAccumulator{
		totalKnown:    [5]bool{true, true, true, true, true},
		subtotalValid: [5]bool{true, true, true, true, true},
	}
}

func (accumulator *tokenAccumulator) add(tokens corecontract.UsageTokens) {
	accumulator.count++
	anyFact := false
	values := [5]*uint64{
		tokens.Input,
		tokens.CachedInput,
		tokens.UncachedInput,
		tokens.Output,
		tokens.Reasoning,
	}
	for index, value := range values {
		if value == nil {
			accumulator.totalKnown[index] = false
			continue
		}
		anyFact = true
		accumulator.fieldKnownRows[index]++
		if accumulator.totalKnown[index] {
			if math.MaxUint64-accumulator.totalSums[index] < *value {
				accumulator.totalKnown[index] = false
				accumulator.overflow = true
			} else {
				accumulator.totalSums[index] += *value
			}
		}
		if accumulator.subtotalValid[index] {
			if math.MaxUint64-accumulator.subtotalSums[index] < *value {
				accumulator.subtotalValid[index] = false
				accumulator.overflow = true
			} else {
				accumulator.subtotalSums[index] += *value
			}
		}
	}
	if anyFact {
		accumulator.rowsWithAnyFact++
	}
}

func (accumulator *tokenAccumulator) report() TokenTotals {
	if accumulator.count == 0 {
		return TokenTotals{}
	}
	values := [5]*uint64{}
	for index := range accumulator.totalSums {
		if accumulator.totalKnown[index] {
			value := accumulator.totalSums[index]
			values[index] = &value
		}
	}
	return TokenTotals{
		Input: values[0], CachedInput: values[1], UncachedInput: values[2],
		Output: values[3], Reasoning: values[4],
	}
}

func (accumulator *tokenAccumulator) knownSubtotal() TokenTotals {
	values := [5]*uint64{}
	for index := range accumulator.subtotalSums {
		if accumulator.subtotalValid[index] &&
			accumulator.fieldKnownRows[index] != 0 {
			value := accumulator.subtotalSums[index]
			values[index] = &value
		}
	}
	return TokenTotals{
		Input: values[0], CachedInput: values[1], UncachedInput: values[2],
		Output: values[3], Reasoning: values[4],
	}
}

func (accumulator *tokenAccumulator) coverage() TokenFieldCoverage {
	field := func(index int) TokenFieldRowCoverage {
		known := accumulator.fieldKnownRows[index]
		return TokenFieldRowCoverage{
			KnownRows:   known,
			UnknownRows: accumulator.count - known,
		}
	}
	return TokenFieldCoverage{
		Input:         field(0),
		CachedInput:   field(1),
		UncachedInput: field(2),
		Output:        field(3),
		Reasoning:     field(4),
	}
}

func validRowCoverage(known, unknown, total uint64) bool {
	return known <= total && unknown == total-known
}

func validTokenFieldCoverage(coverage TokenFieldCoverage, rows uint64) bool {
	for _, field := range []TokenFieldRowCoverage{
		coverage.Input,
		coverage.CachedInput,
		coverage.UncachedInput,
		coverage.Output,
		coverage.Reasoning,
	} {
		if !validRowCoverage(field.KnownRows, field.UnknownRows, rows) {
			return false
		}
	}
	return true
}

func cacheHitRatio(tokens TokenTotals) *float64 {
	if tokens.CachedInput == nil || tokens.UncachedInput == nil {
		return nil
	}
	cached := new(big.Int).SetUint64(*tokens.CachedInput)
	denominator := new(big.Int).Set(cached)
	denominator.Add(
		denominator,
		new(big.Int).SetUint64(*tokens.UncachedInput),
	)
	if denominator.Sign() == 0 {
		return nil
	}
	value, _ := new(big.Rat).SetFrac(cached, denominator).Float64()
	return &value
}

type roleCacheAccumulator struct {
	tokens                  *tokenAccumulator
	cachedInputPositiveRows uint64
	cachedInputZeroRows     uint64
	cachedInputUnknownRows  uint64
}

type roleCacheAccumulators map[string]*roleCacheAccumulator

func fixedRoleCacheNames() []string {
	return []string{
		string(corecontract.CompositeRunRoleChildV1),
		string(corecontract.CompositeRunRoleReviewerV1),
		string(corecontract.CompositeRunRoleRootV1),
	}
}

func newRoleCounts() map[string]uint64 {
	result := make(map[string]uint64, len(fixedRoleCacheNames()))
	for _, role := range fixedRoleCacheNames() {
		result[role] = 0
	}
	return result
}

func newRoleCacheAccumulators() roleCacheAccumulators {
	result := make(roleCacheAccumulators, len(fixedRoleCacheNames()))
	for _, role := range fixedRoleCacheNames() {
		result[role] = &roleCacheAccumulator{tokens: newTokenAccumulator()}
	}
	return result
}

func newEmptyRoleCacheReport() map[string]RoleCacheObservation {
	return newRoleCacheAccumulators().report()
}

func (accumulator *roleCacheAccumulator) add(
	tokens corecontract.UsageTokens,
) {
	accumulator.tokens.add(tokens)
	switch {
	case tokens.CachedInput == nil:
		accumulator.cachedInputUnknownRows++
	case *tokens.CachedInput == 0:
		accumulator.cachedInputZeroRows++
	default:
		accumulator.cachedInputPositiveRows++
	}
}

func (accumulators roleCacheAccumulators) report() map[string]RoleCacheObservation {
	result := make(map[string]RoleCacheObservation, len(fixedRoleCacheNames()))
	for _, role := range fixedRoleCacheNames() {
		accumulator := accumulators[role]
		if accumulator == nil || accumulator.tokens == nil {
			result[role] = RoleCacheObservation{}
			continue
		}
		totals := accumulator.tokens.report()
		result[role] = RoleCacheObservation{
			Attempts:                  accumulator.tokens.count,
			UsageRowsWithAnyTokenFact: accumulator.tokens.rowsWithAnyFact,
			UsageRowsWithoutTokenFacts: accumulator.tokens.count -
				accumulator.tokens.rowsWithAnyFact,
			Tokens:                  totals,
			TokenFieldCoverage:      accumulator.tokens.coverage(),
			KnownTokenSubtotal:      accumulator.tokens.knownSubtotal(),
			CachedInputPositiveRows: accumulator.cachedInputPositiveRows,
			CachedInputZeroRows:     accumulator.cachedInputZeroRows,
			CachedInputUnknownRows:  accumulator.cachedInputUnknownRows,
			CacheHitRatio:           cacheHitRatio(totals),
		}
	}
	return result
}

func validRoleCacheCoverage(
	byRole map[string]RoleCacheObservation,
	wantAttempts uint64,
	wantRowsWithFacts uint64,
	wantRowsWithoutFacts uint64,
	wantCoverage TokenFieldCoverage,
	wantKnownSubtotal TokenTotals,
	wantRunsByRole map[string]uint64,
) bool {
	if len(byRole) != len(fixedRoleCacheNames()) ||
		len(wantRunsByRole) != len(fixedRoleCacheNames()) {
		return false
	}
	var attempts uint64
	var rowsWithFacts uint64
	var rowsWithoutFacts uint64
	var knownRows [5]uint64
	var unknownRows [5]uint64
	var knownSubtotalSums [5]uint64
	var knownSubtotalPresent [5]bool
	for _, role := range fixedRoleCacheNames() {
		observation, found := byRole[role]
		runCount, runRoleFound := wantRunsByRole[role]
		if !found || !runRoleFound || observation.Attempts > runCount ||
			!validRoleCacheObservation(observation) ||
			!safeAddUint64(&attempts, observation.Attempts) ||
			!safeAddUint64(
				&rowsWithFacts,
				observation.UsageRowsWithAnyTokenFact,
			) || !safeAddUint64(
			&rowsWithoutFacts,
			observation.UsageRowsWithoutTokenFacts,
		) {
			return false
		}
		fields := tokenCoverageFields(observation.TokenFieldCoverage)
		subtotals := tokenTotalFields(observation.KnownTokenSubtotal)
		for index, field := range fields {
			if !safeAddUint64(&knownRows[index], field.KnownRows) ||
				!safeAddUint64(&unknownRows[index], field.UnknownRows) {
				return false
			}
			if subtotals[index] != nil {
				knownSubtotalPresent[index] = true
				if !safeAddUint64(
					&knownSubtotalSums[index],
					*subtotals[index],
				) {
					return false
				}
			}
		}
	}
	if attempts != wantAttempts || rowsWithFacts != wantRowsWithFacts ||
		rowsWithoutFacts != wantRowsWithoutFacts {
		return false
	}
	wantFields := tokenCoverageFields(wantCoverage)
	wantSubtotals := tokenTotalFields(wantKnownSubtotal)
	for index, field := range wantFields {
		if knownRows[index] != field.KnownRows ||
			unknownRows[index] != field.UnknownRows {
			return false
		}
		if knownSubtotalPresent[index] != (wantSubtotals[index] != nil) ||
			wantSubtotals[index] != nil &&
				knownSubtotalSums[index] != *wantSubtotals[index] {
			return false
		}
	}
	return true
}

func validRoleCacheObservation(observation RoleCacheObservation) bool {
	if !validRowCoverage(
		observation.UsageRowsWithAnyTokenFact,
		observation.UsageRowsWithoutTokenFacts,
		observation.Attempts,
	) || !validTokenFieldCoverage(
		observation.TokenFieldCoverage,
		observation.Attempts,
	) || !validTokenProjection(
		observation.Tokens,
		observation.KnownTokenSubtotal,
		observation.TokenFieldCoverage,
		observation.Attempts,
	) {
		return false
	}
	cachedKnown := observation.TokenFieldCoverage.CachedInput.KnownRows
	cachedUnknown := observation.TokenFieldCoverage.CachedInput.UnknownRows
	knownClassified := observation.CachedInputPositiveRows
	if !safeAddUint64(&knownClassified, observation.CachedInputZeroRows) ||
		knownClassified != cachedKnown ||
		observation.CachedInputUnknownRows != cachedUnknown {
		return false
	}
	classified := knownClassified
	if !safeAddUint64(&classified, observation.CachedInputUnknownRows) ||
		classified != observation.Attempts {
		return false
	}
	wantRatio := cacheHitRatio(observation.Tokens)
	if wantRatio == nil || observation.CacheHitRatio == nil {
		return wantRatio == nil && observation.CacheHitRatio == nil
	}
	return *wantRatio == *observation.CacheHitRatio
}

func validTokenProjection(
	totals TokenTotals,
	subtotals TokenTotals,
	coverage TokenFieldCoverage,
	rows uint64,
) bool {
	totalFields := tokenTotalFields(totals)
	subtotalFields := tokenTotalFields(subtotals)
	coverageFields := tokenCoverageFields(coverage)
	for index, field := range coverageFields {
		totalExpected := rows != 0 && field.KnownRows == rows
		subtotalExpected := field.KnownRows != 0
		if (totalFields[index] != nil) != totalExpected ||
			(subtotalFields[index] != nil) != subtotalExpected {
			return false
		}
		if totalFields[index] != nil &&
			*totalFields[index] != *subtotalFields[index] {
			return false
		}
	}
	return true
}

func tokenTotalFields(totals TokenTotals) [5]*uint64 {
	return [5]*uint64{
		totals.Input,
		totals.CachedInput,
		totals.UncachedInput,
		totals.Output,
		totals.Reasoning,
	}
}

func tokenCoverageFields(
	coverage TokenFieldCoverage,
) [5]TokenFieldRowCoverage {
	return [5]TokenFieldRowCoverage{
		coverage.Input,
		coverage.CachedInput,
		coverage.UncachedInput,
		coverage.Output,
		coverage.Reasoning,
	}
}

func safeAddUint64(total *uint64, value uint64) bool {
	if math.MaxUint64-*total < value {
		return false
	}
	*total += value
	return true
}
