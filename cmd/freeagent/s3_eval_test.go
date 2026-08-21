package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekcost"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/loopapi"
)

type s3EvalTestUsageReader struct{}

func (*s3EvalTestUsageReader) GetCompositeFamilyUsageProjection(
	context.Context,
	string,
) (currentstore.CompositeFamilyUsageProjectionV1, error) {
	return currentstore.CompositeFamilyUsageProjectionV1{}, nil
}

func (*s3EvalTestUsageReader) GetTerminalRunResult(
	context.Context,
	string,
) (currentstore.TerminalRunResult, error) {
	return currentstore.TerminalRunResult{}, errors.New("terminal result unavailable")
}

type s3EvalTestPriceReader struct {
	records map[string]currentstore.ModelPriceSnapshotRecord
	err     error
}

func (reader *s3EvalTestPriceReader) GetModelPriceSnapshot(
	_ context.Context,
	priceSnapshotID string,
) (currentstore.ModelPriceSnapshotRecord, error) {
	if reader.err != nil {
		return currentstore.ModelPriceSnapshotRecord{}, reader.err
	}
	record, ok := reader.records[priceSnapshotID]
	if !ok {
		return currentstore.ModelPriceSnapshotRecord{}, errors.New("unexpected price snapshot")
	}
	return record, nil
}

func TestS3EvalUsesOneInjectedCompositionAndDefaultsRemainOff(t *testing.T) {
	scenarioPath := writeS3EvalTestScenario(t)
	reader := &s3EvalTestUsageReader{}
	openCalls := 0
	chatCalls := 0
	runCalls := 0
	closeCalls := 0
	var output bytes.Buffer
	err := runS3EvalWithDependencies(
		context.Background(),
		[]string{
			"--db", "experiment.sqlite",
			"--scenario", scenarioPath,
			"--repetitions", "2",
			"--tenant", "tenant-s3c",
			"--principal", "principal-s3c",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				_ context.Context,
				databasePath string,
				artifactRoot string,
				tenantID string,
				options productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				openCalls++
				if databasePath != "experiment.sqlite" ||
					artifactRoot != "experiment.sqlite.artifacts" ||
					tenantID != "tenant-s3c" {
					t.Fatalf(
						"open scope=%q/%q/%q",
						databasePath,
						artifactRoot,
						tenantID,
					)
				}
				if options.FairScheduler != nil || options.DeepSeek != nil {
					t.Fatalf("default options=%+v", options)
				}
				return &s3EvalRuntime{
					close: func() error {
						closeCalls++
						return nil
					},
					chat: func(
						context.Context,
						localchat.ChatInput,
					) (localchat.CompositeChatResult, error) {
						chatCalls++
						return localchat.CompositeChatResult{}, nil
					},
					usage: reader,
				}, nil
			},
			run: func(
				ctx context.Context,
				input s3eval.ExperimentInput,
				chat s3eval.ChatFunc,
				usage s3eval.CompositeUsageReader,
			) (s3eval.ExperimentReport, error) {
				runCalls++
				if usage != reader {
					t.Fatal("runner did not receive the opened composition's Store view")
				}
				for index, task := range input.Tasks {
					if task.ChatInput.TenantID != "tenant-s3c" ||
						task.ChatInput.PrincipalID != "principal-s3c" ||
						task.ChatInput.WorkspaceID != "workspace-"+string(rune('a'+index)) ||
						task.ChatInput.AgentID != "s3c-architect" ||
						task.ChatInput.ProfileID != "s3c-coordinator" {
						t.Fatalf("scenario task %d input=%+v", index, task.ChatInput)
					}
				}
				_, _ = chat(ctx, input.Tasks[0].ChatInput)
				return successfulS3EvalCommandTestReport(strconv.Itoa(runCalls)), nil
			},
		},
	)
	if err != nil {
		t.Fatalf("s3-eval: %v", err)
	}
	if openCalls != 1 || runCalls != 2 || chatCalls != 2 || closeCalls != 1 {
		t.Fatalf(
			"calls open=%d run=%d chat=%d close=%d",
			openCalls,
			runCalls,
			chatCalls,
			closeCalls,
		)
	}
	var report s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, output.String())
	}
	if report.SchemaVersion != s3EvalReportSchemaVersionV1 ||
		report.Experiment.ID != "s3c-experiment" ||
		report.Experiment.RepetitionsRequested != 2 ||
		report.Experiment.RepetitionsAttempted != 2 ||
		report.Scheduler.Enabled || report.Runtime.DeepSeekEnabled ||
		len(report.RepetitionReports) != 2 || report.FirstError != "" {
		t.Fatalf("unexpected report=%+v", report)
	}
}

func TestS3EvalModelUnknownStopsSemanticRepetitions(t *testing.T) {
	scenarioPath := writeS3EvalTestScenario(t)
	runCalls := 0
	var output bytes.Buffer
	err := runS3EvalWithDependencies(
		context.Background(),
		[]string{
			"--db", "experiment.sqlite",
			"--scenario", scenarioPath,
			"--repetitions", "3",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				context.Context,
				string,
				string,
				string,
				productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				return &s3EvalRuntime{
					close: func() error { return nil },
					chat: func(
						context.Context,
						localchat.ChatInput,
					) (localchat.CompositeChatResult, error) {
						return localchat.CompositeChatResult{}, nil
					},
					usage: &s3EvalTestUsageReader{},
				}, nil
			},
			run: func(
				context.Context,
				s3eval.ExperimentInput,
				s3eval.ChatFunc,
				s3eval.CompositeUsageReader,
			) (s3eval.ExperimentReport, error) {
				runCalls++
				report := successfulS3EvalCommandTestReport()
				report.Families[1].Attempts[0].State = corecontract.ModelAttemptUnknown
				return report, nil
			},
		},
	)
	if err == nil || !errors.Is(err, errS3EvalUnsafeSemanticRepetition) {
		t.Fatalf("MODEL_UNKNOWN error=%v", err)
	}
	if runCalls != 1 {
		t.Fatalf("MODEL_UNKNOWN caused %d semantic submissions, want 1", runCalls)
	}
	var report s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode MODEL_UNKNOWN report: %v\n%s", err, output.String())
	}
	if report.Experiment.RepetitionsAttempted != 1 ||
		len(report.RepetitionReports) != 1 ||
		!strings.Contains(report.FirstError, string(corecontract.ModelAttemptUnknown)) {
		t.Fatalf("MODEL_UNKNOWN partial report=%+v", report)
	}
}

func successfulS3EvalCommandTestReport(suffixes ...string) s3eval.ExperimentReport {
	suffix := ""
	if len(suffixes) != 0 {
		suffix = "-" + suffixes[0]
	}
	report := s3eval.ExperimentReport{
		Families: make([]s3eval.FamilyReport, s3eval.WorkspaceCount),
	}
	for index := range report.Families {
		workspaceID := "workspace-" + string(rune('a'+index))
		rootRunID := "run-" + workspaceID + suffix
		childRunIDs := []string{
			"child-frontend-" + workspaceID + suffix,
			"child-backend-" + workspaceID + suffix,
		}
		report.Families[index] = s3eval.FamilyReport{
			WorkspaceID: workspaceID,
			RequestID:   "request-" + workspaceID + suffix,
			RootRunID:   rootRunID,
			Children: []s3eval.DispositionFact{
				{RunID: childRunIDs[0], Disposition: loopapi.DispositionTerminated},
				{RunID: childRunIDs[1], Disposition: loopapi.DispositionTerminated},
			},
			Root: s3eval.DispositionFact{
				RunID:       rootRunID,
				Disposition: loopapi.DispositionTerminated,
			},
			Reply: "complete",
			Attempts: []s3eval.AttemptFact{
				{
					WorkspaceID: workspaceID, RootRunID: rootRunID,
					RunID: childRunIDs[0], Role: corecontract.CompositeRunRoleChildV1,
					AttemptID: "attempt-frontend-" + workspaceID + suffix,
					State:     corecontract.ModelAttemptSucceeded, Provider: "echo",
				},
				{
					WorkspaceID: workspaceID, RootRunID: rootRunID,
					RunID: childRunIDs[1], Role: corecontract.CompositeRunRoleChildV1,
					AttemptID: "attempt-backend-" + workspaceID + suffix,
					State:     corecontract.ModelAttemptSucceeded, Provider: "echo",
				},
				{
					WorkspaceID: workspaceID, RootRunID: rootRunID,
					RunID: rootRunID, Role: corecontract.CompositeRunRoleRootV1,
					AttemptID: "attempt-root-" + workspaceID + suffix,
					State:     corecontract.ModelAttemptSucceeded, Provider: "echo",
				},
			},
			Results: []s3eval.ResultFact{
				{
					RunID: childRunIDs[0], Role: corecontract.CompositeRunRoleChildV1,
					AttemptID:     "attempt-frontend-" + workspaceID + suffix,
					ResultDigest:  "digest-frontend-" + workspaceID + suffix,
					AssistantText: "frontend complete",
				},
				{
					RunID: childRunIDs[1], Role: corecontract.CompositeRunRoleChildV1,
					AttemptID:     "attempt-backend-" + workspaceID + suffix,
					ResultDigest:  "digest-backend-" + workspaceID + suffix,
					AssistantText: "backend complete",
				},
				{
					RunID: rootRunID, Role: corecontract.CompositeRunRoleRootV1,
					AttemptID:     "attempt-root-" + workspaceID + suffix,
					ResultDigest:  "digest-root-" + workspaceID + suffix,
					AssistantText: "complete",
				},
			},
		}
	}
	return report
}

func TestS3EvalIdentityReplayStopsExperimentalCounting(t *testing.T) {
	scenarioPath := writeS3EvalTestScenario(t)
	runCalls := 0
	var output bytes.Buffer
	err := runS3EvalWithDependencies(
		context.Background(),
		[]string{
			"--db", "experiment.sqlite",
			"--scenario", scenarioPath,
			"--repetitions", "3",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				context.Context,
				string,
				string,
				string,
				productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				return &s3EvalRuntime{
					close: func() error { return nil },
					chat: func(
						context.Context,
						localchat.ChatInput,
					) (localchat.CompositeChatResult, error) {
						return localchat.CompositeChatResult{}, nil
					},
					usage: &s3EvalTestUsageReader{},
				}, nil
			},
			run: func(
				context.Context,
				s3eval.ExperimentInput,
				s3eval.ChatFunc,
				s3eval.CompositeUsageReader,
			) (s3eval.ExperimentReport, error) {
				runCalls++
				return successfulS3EvalCommandTestReport("same"), nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "replayed across repetitions") {
		t.Fatalf("identity replay error=%v", err)
	}
	if runCalls != 2 {
		t.Fatalf("identity replay caused %d submissions, want 2", runCalls)
	}
	var report s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode identity replay report: %v\n%s", err, output.String())
	}
	if report.Experiment.RepetitionsAttempted != 2 ||
		len(report.RepetitionReports) != 2 ||
		!strings.Contains(report.FirstError, "replayed across repetitions") {
		t.Fatalf("identity replay report=%+v", report)
	}
}

func TestS3EvalSemanticRepetitionBlockerRejectsIncompleteOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*s3eval.ExperimentReport)
	}{
		{"missing family", func(report *s3eval.ExperimentReport) {
			report.Families = report.Families[:2]
		}},
		{"review reject", func(report *s3eval.ExperimentReport) {
			report.Families[0].Failure = string(corecontract.CompositeReviewRejectedReasonV1)
		}},
		{"reviewer verdict is not approve", func(report *s3eval.ExperimentReport) {
			family := &report.Families[0]
			reviewerRunID := "reviewer-blocker"
			reviewerAttemptID := "attempt-reviewer-blocker"
			family.Reviewer = &s3eval.DispositionFact{
				RunID:       reviewerRunID,
				Disposition: loopapi.DispositionTerminated,
			}
			family.Attempts = append(family.Attempts, s3eval.AttemptFact{
				WorkspaceID: family.WorkspaceID,
				RootRunID:   family.RootRunID,
				RunID:       reviewerRunID,
				Role:        corecontract.CompositeRunRoleReviewerV1,
				AttemptID:   reviewerAttemptID,
				State:       corecontract.ModelAttemptSucceeded,
			})
			family.Results = append(family.Results, s3eval.ResultFact{
				RunID:         reviewerRunID,
				Role:          corecontract.CompositeRunRoleReviewerV1,
				AttemptID:     reviewerAttemptID,
				ResultDigest:  "reviewer-result-digest",
				AssistantText: "canonical reject",
				ReviewerVerdict: &corecontract.ReviewVerdictV1{
					Decision: corecontract.ReviewDecisionRejectV1,
				},
			})
		}},
		{"family error", func(report *s3eval.ExperimentReport) {
			report.Families[0].Error = "metric integrity"
		}},
		{"no attempt", func(report *s3eval.ExperimentReport) {
			report.Families[0].Attempts = nil
		}},
		{"missing terminal result", func(report *s3eval.ExperimentReport) {
			report.Families[0].Results = report.Families[0].Results[:2]
		}},
		{"failed attempt", func(report *s3eval.ExperimentReport) {
			report.Families[0].Attempts[0].State = corecontract.ModelAttemptFailed
		}},
		{"unknown attempt", func(report *s3eval.ExperimentReport) {
			report.Families[0].Attempts[0].State = corecontract.ModelAttemptUnknown
		}},
		{"pending attempt", func(report *s3eval.ExperimentReport) {
			report.Families[0].Attempts[0].State = corecontract.ModelAttemptPending
		}},
		{"empty reply", func(report *s3eval.ExperimentReport) {
			report.Families[0].Reply = ""
		}},
		{"nonterminal root", func(report *s3eval.ExperimentReport) {
			report.Families[0].Root.Disposition = loopapi.DispositionWaitingExternal
		}},
		{"nonterminal child", func(report *s3eval.ExperimentReport) {
			report.Families[0].Children = []s3eval.DispositionFact{{
				RunID: "child", Disposition: loopapi.DispositionWaitingExternal,
			}}
		}},
		{"nonterminal reviewer", func(report *s3eval.ExperimentReport) {
			report.Families[0].Reviewer = &s3eval.DispositionFact{
				RunID: "reviewer", Disposition: loopapi.DispositionWaitingExternal,
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := successfulS3EvalCommandTestReport("blocker")
			test.mutate(&report)
			err := s3EvalSemanticRepetitionBlocker(report)
			if err == nil || !errors.Is(err, errS3EvalUnsafeSemanticRepetition) {
				t.Fatalf("blocker error=%v", err)
			}
		})
	}
}

func TestS3EvalExplicitRuntimeOptionsAndPartialFailureAreReported(t *testing.T) {
	scenarioPath := writeS3EvalTestScenario(t)
	runCalls := 0
	closed := false
	var output bytes.Buffer
	wantErr := errors.New("observed family failure")
	err := runS3EvalWithDependencies(
		context.Background(),
		[]string{
			"--db", "experiment.sqlite",
			"--scenario", scenarioPath,
			"--repetitions", "3",
			"--enable-fair-scheduler",
			"--scheduler-global-workers", "2",
			"--scheduler-workspace-workers", "1",
			"--scheduler-family-workers", "1",
			"--enable-deepseek",
			"--deepseek-api-key-env", "TEST_DEEPSEEK_KEY",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				_ context.Context,
				_ string,
				_ string,
				_ string,
				options productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				if options.FairScheduler == nil ||
					options.FairScheduler.Limits.GlobalWorkers != 2 ||
					options.FairScheduler.Limits.MaxActivePerWorkspace != 1 ||
					options.FairScheduler.Limits.MaxActivePerFamily != 1 ||
					options.DeepSeek == nil || options.DeepSeek.APIKeyResolver == nil {
					t.Fatalf("explicit options=%+v", options)
				}
				return &s3EvalRuntime{
					close: func() error {
						closed = true
						return nil
					},
					chat: func(
						context.Context,
						localchat.ChatInput,
					) (localchat.CompositeChatResult, error) {
						return localchat.CompositeChatResult{}, nil
					},
					usage: &s3EvalTestUsageReader{},
				}, nil
			},
			run: func(
				context.Context,
				s3eval.ExperimentInput,
				s3eval.ChatFunc,
				s3eval.CompositeUsageReader,
			) (s3eval.ExperimentReport, error) {
				runCalls++
				return s3eval.ExperimentReport{
					Fairness: s3eval.FairnessReport{Starvation: true},
				}, wantErr
			},
		},
	)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("partial failure error=%v", err)
	}
	if runCalls != 1 || !closed {
		t.Fatalf("partial failure run calls=%d closed=%v", runCalls, closed)
	}
	var report s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode partial report: %v\n%s", err, output.String())
	}
	if !report.Scheduler.Enabled || report.Scheduler.GlobalWorkers != 2 ||
		!report.Runtime.DeepSeekEnabled ||
		report.Experiment.RepetitionsRequested != 3 ||
		report.Experiment.RepetitionsAttempted != 1 ||
		len(report.RepetitionReports) != 1 ||
		!strings.Contains(report.RepetitionReports[0].Error, wantErr.Error()) ||
		!strings.Contains(report.FirstError, wantErr.Error()) ||
		!report.RepetitionReports[0].Report.Fairness.Starvation {
		t.Fatalf("partial report=%+v", report)
	}
}

func TestS3EvalValidatesRepetitionAndScenarioFile(t *testing.T) {
	scenarioPath := writeS3EvalTestScenario(t)
	dependencies := s3EvalCommandDependencies{
		open: func(
			context.Context,
			string,
			string,
			string,
			productionCompositionOptions,
		) (*s3EvalRuntime, error) {
			t.Fatal("invalid input opened production composition")
			return nil, nil
		},
		run: func(
			context.Context,
			s3eval.ExperimentInput,
			s3eval.ChatFunc,
			s3eval.CompositeUsageReader,
		) (s3eval.ExperimentReport, error) {
			return s3eval.ExperimentReport{}, nil
		},
	}
	for _, repetitions := range []string{"0", "101"} {
		err := runS3EvalWithDependencies(
			context.Background(),
			[]string{
				"--db", "unused.sqlite",
				"--scenario", scenarioPath,
				"--repetitions", repetitions,
			},
			io.Discard,
			io.Discard,
			dependencies,
		)
		if err == nil || !strings.Contains(err.Error(), "between 1 and 100") {
			t.Fatalf("repetitions %s error=%v", repetitions, err)
		}
	}

	err := runS3EvalWithDependencies(
		context.Background(),
		[]string{
			"--db", "unused.sqlite",
			"--scenario", t.TempDir(),
		},
		io.Discard,
		io.Discard,
		dependencies,
	)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory scenario error=%v", err)
	}

	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	unknown := []byte(`{
		"schema_version":"freeagent.s3-eval-scenario/v1",
		"experiment_id":"experiment","agent_id":"agent","profile_id":"profile",
		"tasks":[],"unknown":true
	}`)
	if err := os.WriteFile(unknownPath, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readS3EvalScenario(unknownPath); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown scenario field error=%v", err)
	}
}

func TestDeriveS3EvalDeepSeekCostsSupportsMixedModelsPerAttempt(t *testing.T) {
	flash, flashCanonical, err := corecontract.NewModelPriceSnapshotV1(
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "price-deepseek-test",
			Provider:        "deepseek",
			Model:           "deepseek-v4-flash",
			BillingVersion:  "deepseek-public-price-test",
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
	pro, proCanonical, err := corecontract.NewModelPriceSnapshotV1(
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "price-deepseek-pro-test",
			Provider:        "deepseek",
			Model:           "deepseek-v4-pro",
			BillingVersion:  "deepseek-public-price-test",
			Currency:        "CNY",
			PricingStatus:   corecontract.PricingKnown,
			Pricing: json.RawMessage(
				`{"cached_input_per_million_microunits":25000,"output_per_million_microunits":6000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":3000000}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input := uint64(1500000)
	cached := uint64(500000)
	uncached := uint64(1000000)
	output := uint64(250000)
	reasoning := uint64(200000)
	report := s3eval.ExperimentReport{Families: []s3eval.FamilyReport{{
		WorkspaceID: "workspace-a",
		RootRunID:   "root-a",
		Tokens: s3eval.TokenReport{Totals: s3eval.TokenTotalsReport{
			Input: &input, CachedInput: &cached, UncachedInput: &uncached,
			Output: &output, Reasoning: &reasoning,
		}},
		Attempts: []s3eval.AttemptFact{
			{
				RunID: "child-a", Role: corecontract.CompositeRunRoleChildV1,
				AttemptID: "attempt-flash", Provider: flash.Provider,
				Model: flash.Model, PriceSnapshotID: flash.PriceSnapshotID,
				PriceSnapshotDigest: flash.Digest, Currency: flash.Currency,
				Tokens: corecontract.UsageTokens{
					Input: &input, CachedInput: &cached, UncachedInput: &uncached,
					Output: &output, Reasoning: &reasoning,
				},
			},
			{
				RunID: "root-a", Role: corecontract.CompositeRunRoleRootV1,
				AttemptID: "attempt-pro", Provider: pro.Provider,
				Model: pro.Model, PriceSnapshotID: pro.PriceSnapshotID,
				PriceSnapshotDigest: pro.Digest, Currency: pro.Currency,
				Tokens: corecontract.UsageTokens{
					Input: &input, CachedInput: &cached, UncachedInput: &uncached,
					Output: &output, Reasoning: &reasoning,
				},
			},
		},
	}}}
	estimates, err := deriveS3EvalDeepSeekCosts(
		context.Background(),
		report,
		&s3EvalTestPriceReader{records: map[string]currentstore.ModelPriceSnapshotRecord{
			flash.PriceSnapshotID: {Snapshot: flash, CanonicalJSON: flashCanonical},
			pro.PriceSnapshotID:   {Snapshot: pro, CanonicalJSON: proCanonical},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(estimates) != 2 || estimates[0].Estimate.Value == nil ||
		*estimates[0].Estimate.Value != "1.51" ||
		estimates[0].Estimate.Currency != "CNY" ||
		estimates[0].Model != "deepseek-v4-flash" ||
		estimates[1].Estimate.Value == nil ||
		*estimates[1].Estimate.Value != "4.5125" ||
		estimates[1].Model != "deepseek-v4-pro" {
		t.Fatalf("estimates=%+v", estimates)
	}
}

func TestDeriveS3EvalDeepSeekCostsRetainsValidPartialEvidence(t *testing.T) {
	snapshot, canonical, err := corecontract.NewModelPriceSnapshotV1(
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: "price-deepseek-partial-test",
			Provider:        "deepseek",
			Model:           "deepseek-v4-flash",
			BillingVersion:  "deepseek-public-price-test",
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
	zero := uint64(0)
	report := s3eval.ExperimentReport{Families: []s3eval.FamilyReport{{
		WorkspaceID: "workspace-a",
		RootRunID:   "root-a",
		Attempts: []s3eval.AttemptFact{
			{
				AttemptID: "attempt-incomplete", Provider: "deepseek",
				Model: snapshot.Model,
			},
			{
				AttemptID: "attempt-unknown-usage", Provider: snapshot.Provider,
				Model: snapshot.Model, PriceSnapshotID: snapshot.PriceSnapshotID,
				PriceSnapshotDigest: snapshot.Digest, Currency: snapshot.Currency,
			},
			{
				AttemptID: "attempt-known-zero", Provider: snapshot.Provider,
				Model: snapshot.Model, PriceSnapshotID: snapshot.PriceSnapshotID,
				PriceSnapshotDigest: snapshot.Digest, Currency: snapshot.Currency,
				Tokens: corecontract.UsageTokens{
					Input: &zero, CachedInput: &zero, UncachedInput: &zero,
					Output: &zero,
				},
			},
		},
	}}}
	estimates, err := deriveS3EvalDeepSeekCosts(
		context.Background(),
		report,
		&s3EvalTestPriceReader{records: map[string]currentstore.ModelPriceSnapshotRecord{
			snapshot.PriceSnapshotID: {Snapshot: snapshot, CanonicalJSON: canonical},
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "attempt-incomplete") {
		t.Fatalf("partial cost error=%v", err)
	}
	if len(estimates) != 2 ||
		estimates[0].AttemptID != "attempt-unknown-usage" ||
		estimates[0].Estimate.Status != deepseekcost.StatusUnknown ||
		estimates[0].Estimate.Value != nil ||
		estimates[1].AttemptID != "attempt-known-zero" ||
		estimates[1].Estimate.Status != deepseekcost.StatusKnown ||
		estimates[1].Estimate.Value == nil ||
		*estimates[1].Estimate.Value != "0" {
		t.Fatalf("partial estimates=%+v", estimates)
	}
}

func writeS3EvalTestScenario(t *testing.T) string {
	t.Helper()
	scenario := s3eval.ScenarioV1{
		SchemaVersion: s3eval.ScenarioSchemaVersionV1,
		ExperimentID:  "s3c-experiment",
		AgentID:       "s3c-architect",
		ProfileID:     "s3c-coordinator",
		Tasks: []s3eval.ScenarioTaskV1{
			{
				TaskID:      "task-a",
				WorkspaceID: "workspace-a",
				CaseKind:    s3eval.CaseKindCleanV1,
				Message:     "Design the frontend boundary.",
			},
			{
				TaskID:      "task-b",
				WorkspaceID: "workspace-b",
				CaseKind:    s3eval.CaseKindTrapV1,
				Message:     "Design the backend boundary.",
			},
			{
				TaskID:      "task-c",
				WorkspaceID: "workspace-c",
				CaseKind:    s3eval.CaseKindCleanV1,
				Message:     "Design the network boundary.",
			},
		},
	}
	payload, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
