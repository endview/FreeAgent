package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type s3CFailureMode string

const (
	s3CFailureReviewerReject    s3CFailureMode = "REVIEWER_REJECT"
	s3CFailureSpecialistUnknown s3CFailureMode = "SPECIALIST_TRANSPORT_UNKNOWN"
)

func TestS3EvalCLIReviewerRejectStopsRepetitionsWithoutRootMerge(
	t *testing.T,
) {
	result := runS3CProductionFailureCLI(t, s3CFailureReviewerReject)
	report := result.command.RepetitionReports[0].Report

	roles := countS3CFailureCalls(result.calls)
	if len(result.calls) != 12 ||
		roles[s3CDeepSeekRoleSpecialist] != 9 ||
		roles[s3CDeepSeekRoleReviewer] != 3 ||
		roles[s3CDeepSeekRoleRoot] != 0 {
		t.Fatalf("REJECT provider calls=%d roles=%v", len(result.calls), roles)
	}
	if len(report.Families) != s3eval.WorkspaceCount ||
		len(report.ServiceOrder) != 12 {
		t.Fatalf(
			"REJECT family/service counts=%d/%d",
			len(report.Families),
			len(report.ServiceOrder),
		)
	}

	for _, family := range report.Families {
		if family.Error != "" ||
			family.Failure != corecontract.CompositeReviewRejectedReasonV1 ||
			family.Reply != "" || len(family.Children) != 3 ||
			family.Reviewer == nil ||
			family.Reviewer.Disposition != loopapi.DispositionTerminated ||
			family.Reviewer.ReasonCode != "MODEL_SUCCEEDED" ||
			family.Root.Disposition != loopapi.DispositionTerminated ||
			family.Root.ReasonCode != corecontract.CompositeReviewRejectedReasonV1 ||
			len(family.Attempts) != 4 || len(family.Results) != 4 {
			t.Fatalf("REJECT family %s=%+v", family.WorkspaceID, family)
		}
		for _, child := range family.Children {
			if child.Disposition != loopapi.DispositionTerminated ||
				child.ReasonCode != "MODEL_SUCCEEDED" {
				t.Fatalf("REJECT Child=%+v", child)
			}
		}

		attemptRoles := map[corecontract.CompositeRunRoleV1]int{}
		for _, attempt := range family.Attempts {
			attemptRoles[attempt.Role]++
			if attempt.State != corecontract.ModelAttemptSucceeded ||
				attempt.Provider != deepseekmodel.ProviderNameV1 ||
				attempt.Model != deepseekmodel.ModelV4Flash {
				t.Fatalf("REJECT Attempt=%+v", attempt)
			}
		}
		if attemptRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			attemptRoles[corecontract.CompositeRunRoleReviewerV1] != 1 ||
			attemptRoles[corecontract.CompositeRunRoleRootV1] != 0 {
			t.Fatalf("REJECT Attempt roles=%v", attemptRoles)
		}

		resultRoles := map[corecontract.CompositeRunRoleV1]int{}
		for _, fact := range family.Results {
			resultRoles[fact.Role]++
			if fact.AssistantText == "" ||
				!moduleapi.ValidSHA256(fact.ResultDigest) {
				t.Fatalf("REJECT Result=%+v", fact)
			}
			if fact.Role == corecontract.CompositeRunRoleReviewerV1 {
				if fact.ReviewerVerdict == nil ||
					fact.ReviewerVerdict.Decision != corecontract.ReviewDecisionRejectV1 ||
					len(fact.ReviewerVerdict.IssueCodes) != 1 ||
					fact.ReviewerVerdict.IssueCodes[0] !=
						corecontract.ReviewIssueMissingEvidenceV1 ||
					len(fact.ReviewerVerdict.AffectedSlotIDs) != 1 ||
					fact.ReviewerVerdict.AffectedSlotIDs[0] != "backend" {
					t.Fatalf("canonical REJECT verdict=%+v", fact.ReviewerVerdict)
				}
			}
		}
		if resultRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			resultRoles[corecontract.CompositeRunRoleReviewerV1] != 1 ||
			resultRoles[corecontract.CompositeRunRoleRootV1] != 0 {
			t.Fatalf("REJECT Result roles=%v", resultRoles)
		}
	}
}

func TestS3EvalCLISpecialistTransportUnknownStopsWithoutReviewOrMerge(
	t *testing.T,
) {
	result := runS3CProductionFailureCLI(t, s3CFailureSpecialistUnknown)
	repetition := result.command.RepetitionReports[0]
	report := repetition.Report

	roles := countS3CFailureCalls(result.calls)
	if len(result.calls) != 9 ||
		roles[s3CDeepSeekRoleSpecialist] != 9 ||
		roles[s3CDeepSeekRoleReviewer] != 0 ||
		roles[s3CDeepSeekRoleRoot] != 0 {
		t.Fatalf("UNKNOWN provider calls=%d roles=%v", len(result.calls), roles)
	}
	if len(report.Families) != s3eval.WorkspaceCount ||
		len(report.ServiceOrder) != 9 {
		t.Fatalf(
			"UNKNOWN family/service counts=%d/%d",
			len(report.Families),
			len(report.ServiceOrder),
		)
	}

	unknownAttemptIDs := make([]string, 0, s3eval.WorkspaceCount)
	for _, family := range report.Families {
		if family.Error != "" || family.Failure != "" || family.Reply != "" ||
			len(family.Children) != 3 || family.Reviewer == nil ||
			family.Reviewer.Disposition != loopapi.DispositionWaitingReconciliation ||
			family.Reviewer.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
			family.Root.Disposition != loopapi.DispositionWaitingReconciliation ||
			family.Root.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
			len(family.Attempts) != 3 || len(family.Results) != 2 {
			t.Fatalf("UNKNOWN family %s=%+v", family.WorkspaceID, family)
		}

		attemptStates := map[corecontract.ModelAttemptState]int{}
		attemptRoles := map[corecontract.CompositeRunRoleV1]int{}
		for _, attempt := range family.Attempts {
			attemptStates[attempt.State]++
			attemptRoles[attempt.Role]++
			if attempt.Role != corecontract.CompositeRunRoleChildV1 ||
				attempt.Provider != deepseekmodel.ProviderNameV1 ||
				attempt.Model != deepseekmodel.ModelV4Flash {
				t.Fatalf("UNKNOWN Attempt=%+v", attempt)
			}
			if attempt.SlotID == "network" {
				if attempt.State != corecontract.ModelAttemptUnknown ||
					!usageTokensAllUnknown(attempt.Tokens) {
					t.Fatalf("network UNKNOWN Attempt=%+v", attempt)
				}
				unknownAttemptIDs = append(unknownAttemptIDs, attempt.AttemptID)
			} else if attempt.State != corecontract.ModelAttemptSucceeded {
				t.Fatalf("successful Specialist Attempt=%+v", attempt)
			}
		}
		if attemptStates[corecontract.ModelAttemptSucceeded] != 2 ||
			attemptStates[corecontract.ModelAttemptUnknown] != 1 ||
			attemptRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			attemptRoles[corecontract.CompositeRunRoleReviewerV1] != 0 ||
			attemptRoles[corecontract.CompositeRunRoleRootV1] != 0 {
			t.Fatalf(
				"UNKNOWN Attempt states/roles=%v/%v",
				attemptStates,
				attemptRoles,
			)
		}

		for _, fact := range family.Results {
			if fact.Role != corecontract.CompositeRunRoleChildV1 ||
				fact.SlotID == "network" || fact.ReviewerVerdict != nil ||
				fact.AssistantText == "" {
				t.Fatalf("UNKNOWN partial Result=%+v", fact)
			}
		}
		if family.Tokens.Totals.Input != nil ||
			family.Tokens.Totals.CachedInput != nil ||
			family.Tokens.Totals.UncachedInput != nil ||
			family.Tokens.Totals.Output != nil ||
			family.Tokens.Totals.Reasoning != nil ||
			family.Tokens.CacheHitRatio != nil {
			t.Fatalf("UNKNOWN family tokens became numeric: %+v", family.Tokens)
		}
	}
	if len(unknownAttemptIDs) != s3eval.WorkspaceCount {
		t.Fatalf("UNKNOWN Attempt IDs=%v", unknownAttemptIDs)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		result.databasePath,
	)
	if err != nil {
		t.Fatalf("reopen UNKNOWN Store: %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close UNKNOWN Store: %v", closeErr)
		}
	}()
	for _, attemptID := range unknownAttemptIDs {
		record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
		if err != nil {
			t.Fatalf("load UNKNOWN Attempt %s: %v", attemptID, err)
		}
		if record.Attempt.State != corecontract.ModelAttemptUnknown ||
			record.Attempt.UnknownReason != string(
				modulehost.UnknownClassInvokeReturnedError,
			) ||
			record.Attempt.ErrorClassification != "" ||
			!usageTokensAllUnknown(record.Usage.Tokens) {
			t.Fatalf("persisted UNKNOWN Attempt=%+v Usage=%+v", record.Attempt, record.Usage)
		}
	}
}

type s3CFailureCLIResult struct {
	command      s3EvalCommandReport
	calls        []s3CDeepSeekCallFact
	databasePath string
}

func runS3CProductionFailureCLI(
	t *testing.T,
	mode s3CFailureMode,
) s3CFailureCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	exampleRoot := filepath.Dir(exampleSeedPath(t))
	seedPath := filepath.Join(
		exampleRoot,
		"s3c-deepseek-v4-flash-reviewer-on.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize S3-C failure production data: %v", err)
	}

	fake := &s3CFailureRoundTripper{
		model:       deepseekmodel.ModelV4Flash,
		mode:        mode,
		unknownSlot: "network",
	}
	closed := false
	var output bytes.Buffer
	runErr := runS3EvalWithDependencies(
		ctx,
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--scenario", filepath.Join(exampleRoot, "s3c-architecture-clean.scenario.json"),
			"--repetitions", "3",
			"--enable-fair-scheduler",
			"--scheduler-global-workers", "2",
			"--scheduler-workspace-workers", "1",
			"--scheduler-family-workers", "1",
			"--enable-deepseek",
			"--deepseek-api-key-env", "S3C_FAILURE_UNUSED_KEY",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				ctx context.Context,
				databasePath string,
				artifactRoot string,
				tenantID string,
				options productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				if options.FairScheduler == nil || options.DeepSeek == nil {
					return nil, errors.New("failure matrix lost explicit production options")
				}
				keyResolver := deepseekmodel.APIKeyResolverFunc(func(
					ctx context.Context,
					identity deepseekmodel.APIKeyIdentity,
				) ([]byte, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if identity.Provider != deepseekmodel.ProviderNameV1 ||
						identity.Model != deepseekmodel.ModelV4Flash ||
						identity.ModelBuildID != localDeepSeekFlashBuild {
						return nil, fmt.Errorf(
							"unexpected DeepSeek failure identity: %+v",
							identity,
						)
					}
					return []byte(s3CDeepSeekTestKey), nil
				})
				options.DeepSeek = &productionDeepSeekRuntimeConfig{
					APIKeyResolver: keyResolver,
					HTTPClient: &http.Client{
						Transport: fake,
						Timeout:   5 * time.Second,
					},
				}
				runtime, err := openProductionS3EvalRuntime(
					ctx,
					databasePath,
					artifactRoot,
					tenantID,
					options,
				)
				if err != nil {
					return nil, err
				}
				originalClose := runtime.close
				runtime.close = func() error {
					closed = true
					return originalClose()
				}
				return runtime, nil
			},
			run: s3eval.Run,
		},
	)
	if runErr == nil || !errors.Is(runErr, errS3EvalUnsafeSemanticRepetition) {
		t.Fatalf("S3-C failure CLI error=%v\n%s", runErr, output.String())
	}
	if !closed {
		t.Fatal("S3-C failure CLI did not close production composition")
	}
	var command s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &command); err != nil {
		t.Fatalf("decode S3-C failure report: %v\n%s", err, output.String())
	}
	if command.SchemaVersion != s3EvalReportSchemaVersionV2 ||
		command.Experiment.RepetitionsRequested != 3 ||
		command.Experiment.RepetitionsAttempted != 1 ||
		len(command.RepetitionReports) != 1 ||
		command.FirstError == "" || !command.Scheduler.Enabled ||
		!command.Runtime.DeepSeekEnabled ||
		!strings.Contains(
			command.RepetitionReports[0].Error,
			errS3EvalUnsafeSemanticRepetition.Error(),
		) {
		t.Fatalf("S3-C failure command report=%+v", command)
	}
	return s3CFailureCLIResult{
		command:      command,
		calls:        fake.snapshot(),
		databasePath: databasePath,
	}
}

type s3CFailureRoundTripper struct {
	mu           sync.Mutex
	model        string
	mode         s3CFailureMode
	unknownSlot  string
	nextSequence int
	calls        []s3CDeepSeekCallFact
}

func (fake *s3CFailureRoundTripper) snapshot() []s3CDeepSeekCallFact {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]s3CDeepSeekCallFact(nil), fake.calls...)
}

func (fake *s3CFailureRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if request == nil || request.Method != http.MethodPost ||
		request.URL.String() != s3CDeepSeekOfficialURL ||
		request.Header.Get("Authorization") != "Bearer "+s3CDeepSeekTestKey ||
		request.Header.Get("Accept") != "application/json" ||
		request.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("unexpected DeepSeek failure-matrix request")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek failure request: %w", err)
	}
	if err := request.Body.Close(); err != nil {
		return nil, fmt.Errorf("close DeepSeek failure request: %w", err)
	}
	var wire s3CDeepSeekWireRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decode DeepSeek failure request: %w", err)
	}
	if wire.Model != fake.model || len(wire.Messages) == 0 {
		return nil, errors.New("unexpected DeepSeek failure model or messages")
	}

	fake.mu.Lock()
	fake.nextSequence++
	sequence := fake.nextSequence
	fake.mu.Unlock()
	call, assistantText, err := classifyS3CDeepSeekCall(sequence, wire.Messages)
	if err != nil {
		return nil, err
	}
	if fake.mode == s3CFailureReviewerReject &&
		call.Role == s3CDeepSeekRoleReviewer {
		if call.ReviewerPolicy == nil {
			return nil, errors.New("Reviewer REJECT lacks frozen policy")
		}
		_, canonical, err := corecontract.NewReviewVerdictV1(
			corecontract.ReviewVerdictV1{
				SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
				FamilyDigest:           call.ReviewerPolicy.FamilyDigest,
				SpecialistResultDigest: call.ReviewerPolicy.SpecialistResultDigest,
				Decision:               corecontract.ReviewDecisionRejectV1,
				IssueCodes: []corecontract.ReviewIssueCodeV1{
					corecontract.ReviewIssueMissingEvidenceV1,
				},
				AffectedSlotIDs: []string{"backend"},
				BoundedReason:   "Backend evidence is insufficient for merge approval.",
			},
		)
		if err != nil {
			return nil, fmt.Errorf("freeze failure-matrix REJECT: %w", err)
		}
		assistantText = string(canonical)
		call.Output = assistantText
	}

	fake.mu.Lock()
	fake.calls = append(fake.calls, call)
	fake.mu.Unlock()
	if fake.mode == s3CFailureSpecialistUnknown &&
		call.Role == s3CDeepSeekRoleSpecialist &&
		call.SlotID == fake.unknownSlot {
		return nil, errors.New("synthetic transport ambiguity after request dispatch")
	}

	responseBody, err := json.Marshal(map[string]any{
		"id":                 fmt.Sprintf("s3c-failure-%d", sequence),
		"object":             "chat.completion",
		"created":            int64(1785800000),
		"model":              wire.Model,
		"system_fingerprint": "s3c-failure-fingerprint",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": assistantText,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":            100,
			"prompt_cache_hit_tokens":  40,
			"prompt_cache_miss_tokens": 60,
			"completion_tokens":        20,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": 5,
			},
			"total_tokens": 120,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek failure response: %w", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(bytes.NewReader(responseBody)),
		ContentLength: int64(len(responseBody)),
		Request:       request,
	}, nil
}

func countS3CFailureCalls(
	calls []s3CDeepSeekCallFact,
) map[string]int {
	result := make(map[string]int)
	for _, call := range calls {
		result[call.Role]++
	}
	return result
}

func usageTokensAllUnknown(tokens corecontract.UsageTokens) bool {
	return tokens.Input == nil && tokens.CachedInput == nil &&
		tokens.UncachedInput == nil && tokens.Output == nil &&
		tokens.Reasoning == nil
}
