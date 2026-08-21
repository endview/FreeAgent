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
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w4LearningCycleFakeCredential = "w4-l4-non-secret-test-credential"

func TestW4L4FakeDeepSeekLearningCycleKeepsStrictResultContract(t *testing.T) {
	tests := []struct {
		name      string
		malformed bool
		wantState learningcontract.LearningCycleTaskStateV1
	}{
		{
			name:      "strict skill proposal",
			wantState: learningcontract.LearningCycleTaskProposalSubmittedV1,
		},
		{
			name:      "missing empty union arm remains invalid",
			malformed: true,
			wantState: learningcontract.LearningCycleTaskInvalidResultV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			fake := &w4LearningCycleRoundTripper{malformed: test.malformed}
			composition, schedule, firstDue := openW4LearningCycleFakeComposition(
				t,
				ctx,
				fake,
				strings.ReplaceAll(test.name, " ", "-"),
			)
			defer func() {
				if err := composition.Close(); err != nil {
					t.Errorf("close fake Learning cycle composition: %v", err)
				}
			}()

			service, err := localchat.NewLearningCycleService(composition.chat)
			if err != nil {
				t.Fatalf("construct fake Learning cycle service: %v", err)
			}
			first, err := service.Tick(ctx, localchat.LearningCycleTickInput{
				TenantID:   schedule.Schedule.TenantID,
				ScheduleID: schedule.Schedule.ScheduleID,
				ObservedAt: firstDue,
			})
			if err != nil {
				t.Fatalf("execute fake DeepSeek Learning Tick: %v", err)
			}
			if !first.Due || !first.TaskCreated || !first.AdmissionCreated ||
				!first.FinalizeApplied || first.Task.State != test.wantState ||
				first.Task.RunID == "" || first.Task.AttemptID == "" ||
				first.LoopResult == nil ||
				first.LoopResult.Disposition != loopapi.DispositionTerminated {
				t.Fatalf(
					"fake Tick receipts=(due=%t task_created=%t admission_created=%t finalize_applied=%t state=%s)",
					first.Due,
					first.TaskCreated,
					first.AdmissionCreated,
					first.FinalizeApplied,
					first.Task.State,
				)
			}
			if test.malformed {
				if first.Task.ProposalID != "" {
					t.Fatalf("invalid result created Proposal %q", first.Task.ProposalID)
				}
			} else if first.Task.ProposalID == "" {
				t.Fatal("strict valid Skill result did not create a Proposal")
			}

			calls := fake.snapshot()
			if len(calls) != 1 || calls[0].MessageCount != 1 ||
				calls[0].LastRole != "user" ||
				calls[0].Request.RequestDigest != first.Task.RequestDigest ||
				calls[0].Request.Kind != learningcontract.ProposalKindSkillV1 ||
				calls[0].Request.Objective != schedule.Schedule.Objective {
				t.Fatalf("fake provider call closure=%+v", calls)
			}
			assertW4LearningCycleInstructionSkeleton(
				t,
				calls[0].Request.Instructions,
			)
			if strings.Contains(calls[0].Request.Objective, "Return ") ||
				strings.Contains(calls[0].Request.Objective, "Propose ") {
				t.Fatalf("fake objective contains an instruction: %q", calls[0].Request.Objective)
			}

			dispatch, err := composition.store.GetModelDispatchRecord(
				ctx,
				first.Task.AttemptID,
			)
			if err != nil {
				t.Fatalf("read fake DeepSeek Attempt/Usage: %v", err)
			}
			if dispatch.Attempt.State != corecontract.ModelAttemptSucceeded ||
				dispatch.Attempt.RunID != first.Task.RunID ||
				dispatch.Usage.Tokens.Input == nil ||
				dispatch.Usage.Tokens.Output == nil ||
				dispatch.Usage.EstimatedCost == nil {
				t.Fatalf("fake DeepSeek Attempt/Usage=%+v", dispatch)
			}

			retry, err := service.Tick(ctx, localchat.LearningCycleTickInput{
				TenantID:   schedule.Schedule.TenantID,
				ScheduleID: schedule.Schedule.ScheduleID,
				ObservedAt: firstDue,
			})
			if err != nil || retry.Due || retry.TaskCreated ||
				retry.AdmissionCreated || retry.LoopResult != nil ||
				len(fake.snapshot()) != 1 {
				t.Fatalf(
					"same-window fake retry=(due=%t task_created=%t admission_created=%t calls=%d err=%v)",
					retry.Due,
					retry.TaskCreated,
					retry.AdmissionCreated,
					len(fake.snapshot()),
					err,
				)
			}
		})
	}
}

func openW4LearningCycleFakeComposition(
	t *testing.T,
	ctx context.Context,
	transport http.RoundTripper,
	suffix string,
) (
	*productionComposition,
	currentstore.LearningCycleScheduleRecord,
	time.Time,
) {
	t.Helper()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize fake Learning cycle data: %v", err)
	}
	_, configCanonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        deepseekmodel.ProviderNameV1,
			Model:           deepseekmodel.ModelV4Flash,
			ModelBuildID:    localDeepSeekFlashBuild,
			BillingVersion:  "deepseek-public-price-2026-08-04",
			PriceSnapshotID: "price-deepseek-v4-flash-2026-08-04",
			Parameters: json.RawMessage(
				`{"max_tokens":512,"response_format":{"type":"json_object"},"temperature":0,"thinking":{"type":"disabled"}}`,
			),
		},
	)
	if err != nil {
		t.Fatalf("freeze fake Learning cycle model Config: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open fake Learning cycle Store: %v", err)
	}
	profileID := publishW4LearningCycleModelOnlyProfile(
		t,
		ctx,
		store,
		"deepseek-chat",
		"w4-l4-fake-cycle-profile-"+suffix,
		configCanonical,
	)
	firstDue := time.Now().UTC().Truncate(time.Microsecond)
	scheduleInput := learningCycleCLITestSchedule(
		"w4-l4-fake-cycle-"+suffix,
		firstDue,
	)
	scheduleInput.ProfileID = profileID
	scheduleInput.MaxOutputTokens = 512
	scheduleInput.Objective = "Candidate reusable skill content: compare the " +
		"exact canonical module configuration digest before and after review " +
		"to detect configuration drift."
	created, err := store.CreateLearningCycleSchedule(ctx, scheduleInput)
	if err != nil || !created.Created {
		_ = store.Close()
		t.Fatalf("create fake Learning cycle Schedule: %+v error=%v", created, err)
	}
	enabled, err := store.SetLearningCycleScheduleEnabled(
		ctx,
		currentstore.SetLearningCycleScheduleEnabledInput{
			TenantID:         scheduleInput.TenantID,
			ScheduleID:       scheduleInput.ScheduleID,
			ExpectedRevision: 0,
			Enabled:          true,
		},
	)
	if err != nil || !enabled.Applied || !enabled.Record.Enabled {
		_ = store.Close()
		t.Fatalf("enable fake Learning cycle Schedule: %+v error=%v", enabled, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close prepared fake Learning cycle Store: %v", err)
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
			return nil, errors.New("unexpected fake Learning cycle model identity")
		}
		return []byte(w4LearningCycleFakeCredential), nil
	})
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{DeepSeek: &productionDeepSeekRuntimeConfig{
			APIKeyResolver: keyResolver,
			HTTPClient: &http.Client{
				Transport: transport,
				Timeout:   5 * time.Second,
			},
		}},
	)
	if err != nil {
		t.Fatalf("open fake Learning cycle composition: %v", err)
	}
	return composition, enabled.Record, firstDue
}

type w4LearningCycleCall struct {
	MessageCount int
	LastRole     string
	Request      learningcontract.LearningCycleRequestV1
}

type w4LearningCycleRoundTripper struct {
	mu        sync.Mutex
	malformed bool
	calls     []w4LearningCycleCall
}

func (fake *w4LearningCycleRoundTripper) snapshot() []w4LearningCycleCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]w4LearningCycleCall(nil), fake.calls...)
}

func (fake *w4LearningCycleRoundTripper) RoundTrip(
	httpRequest *http.Request,
) (*http.Response, error) {
	if httpRequest == nil || httpRequest.Method != http.MethodPost ||
		httpRequest.URL.String() != deepSeekPureChatOfficialURL ||
		httpRequest.Header.Get("Authorization") !=
			"Bearer "+w4LearningCycleFakeCredential {
		return nil, errors.New("unexpected fake Learning cycle HTTP request")
	}
	body, err := io.ReadAll(httpRequest.Body)
	if err != nil {
		return nil, errors.New("read fake Learning cycle HTTP request")
	}
	if err := httpRequest.Body.Close(); err != nil {
		return nil, errors.New("close fake Learning cycle HTTP request")
	}
	var wire struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil ||
		wire.Model != deepseekmodel.ModelV4Flash || len(wire.Messages) == 0 {
		return nil, errors.New("decode fake Learning cycle HTTP request")
	}
	last := wire.Messages[len(wire.Messages)-1]
	var decoded learningcontract.LearningCycleRequestV1
	if err := json.Unmarshal([]byte(last.Content), &decoded); err != nil {
		return nil, errors.New("decode fake Learning cycle request content")
	}
	restored, err := learningcontract.RestoreLearningCycleRequestV1(
		[]byte(last.Content),
		decoded.RequestDigest,
	)
	if err != nil {
		return nil, errors.New("restore fake Learning cycle request content")
	}
	fake.mu.Lock()
	fake.calls = append(fake.calls, w4LearningCycleCall{
		MessageCount: len(wire.Messages),
		LastRole:     last.Role,
		Request:      restored,
	})
	malformed := fake.malformed
	fake.mu.Unlock()

	var assistant string
	if malformed {
		encoded, err := json.Marshal(struct {
			SchemaVersion string                                         `json:"schema_version"`
			RequestDigest string                                         `json:"request_digest"`
			Decision      learningcontract.LearningCycleResultDecisionV1 `json:"decision"`
			SkillText     string                                         `json:"skill_text"`
		}{
			SchemaVersion: learningcontract.LearningCycleResultSchemaVersionV1,
			RequestDigest: restored.RequestDigest,
			Decision:      learningcontract.LearningCycleResultProposeV1,
			SkillText: "Compare the exact canonical configuration digest " +
				"before and after review.",
		})
		if err != nil {
			return nil, errors.New("encode malformed fake Learning result")
		}
		assistant = string(encoded)
	} else {
		_, canonical, _, err := learningcontract.NewLearningCycleResultV1(
			learningcontract.LearningCycleResultV1{
				SchemaVersion:   learningcontract.LearningCycleResultSchemaVersionV1,
				RequestDigest:   restored.RequestDigest,
				Decision:        learningcontract.LearningCycleResultProposeV1,
				KnowledgeChunks: []string{},
				SkillText: "Compare the exact canonical configuration digest " +
					"before and after review.",
			},
		)
		if err != nil {
			return nil, errors.New("encode strict fake Learning result")
		}
		assistant = string(canonical)
	}
	responseBody, err := json.Marshal(map[string]any{
		"id":      "w4-l4-fake-cycle-response",
		"object":  "chat.completion",
		"created": int64(1785800000),
		"model":   deepseekmodel.ModelV4Flash,
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": assistant,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":             120,
			"prompt_cache_hit_tokens":   40,
			"prompt_cache_miss_tokens":  80,
			"completion_tokens":         30,
			"completion_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens":              150,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode fake Learning response: %w", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(bytes.NewReader(responseBody)),
		ContentLength: int64(len(responseBody)),
		Request:       httpRequest,
	}, nil
}

func assertW4LearningCycleInstructionSkeleton(
	t *testing.T,
	instructions string,
) {
	t.Helper()
	lower := strings.ToLower(instructions)
	for _, phrase := range []string{"exactly five", "no other keys"} {
		if !strings.Contains(lower, phrase) {
			t.Fatalf("Learning instructions omit %q", phrase)
		}
	}
	for _, fragment := range []string{
		`"schema_version":"learning-cycle-result/v1"`,
		`"request_digest":"<REQUEST_DIGEST>"`,
		`"knowledge_chunks":[]`,
		`"skill_text":""`,
		`"decision":"NO_CHANGE"`,
	} {
		if !strings.Contains(instructions, fragment) {
			t.Fatalf("Learning instructions omit exact skeleton fragment %q", fragment)
		}
	}
}
