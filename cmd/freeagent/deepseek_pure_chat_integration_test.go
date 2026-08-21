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
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
)

const (
	deepSeekPureChatOfficialURL = "https://api.deepseek.com/chat/completions"
	deepSeekPureChatTestKey     = "not-a-secret"
)

func TestProductionDeepSeekSingleAgentPureChatPersistsExactUsage(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	fake := &deepSeekPureChatRoundTripper{}
	composition := openDeepSeekPureChatTestComposition(t, ctx, fake)
	defer closeDeepSeekPureChatTestComposition(t, composition)
	assertDeepSeekPureChatHasNoOptionalBindings(t, ctx, composition)

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "deepseek-chat",
		Message:     "Explain the controlled provider path briefly.",
		RequestID:   "deepseek-pure-chat-success-1",
		Deadline:    time.Now().UTC().Round(0).Add(5 * time.Minute),
	}
	result, err := composition.chat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("DeepSeek Pure Chat: %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.TerminalResult == nil ||
		result.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		result.Reply != "controlled DeepSeek Pure Chat response" {
		t.Fatalf("DeepSeek Pure Chat result=%+v", result)
	}

	calls := fake.snapshot()
	if len(calls) != 1 || calls[0].Method != http.MethodPost ||
		calls[0].URL != deepSeekPureChatOfficialURL ||
		calls[0].Model != deepseekmodel.ModelV4Flash ||
		len(calls[0].Messages) != 1 ||
		calls[0].Messages[0].Role != "user" ||
		calls[0].Messages[0].Content != input.Message {
		t.Fatalf("DeepSeek Pure Chat calls=%+v", calls)
	}
	record, err := composition.store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("read DeepSeek Attempt: %v", err)
	}
	if record.Attempt.Provider != deepseekmodel.ProviderNameV1 ||
		record.Attempt.Model != deepseekmodel.ModelV4Flash ||
		record.Attempt.State != corecontract.ModelAttemptSucceeded ||
		record.Usage.RawReceiptRef == "" ||
		!deepSeekTokenEquals(record.Usage.Tokens.Input, 100) ||
		!deepSeekTokenEquals(record.Usage.Tokens.CachedInput, 40) ||
		!deepSeekTokenEquals(record.Usage.Tokens.UncachedInput, 60) ||
		!deepSeekTokenEquals(record.Usage.Tokens.Output, 20) ||
		!deepSeekTokenEquals(record.Usage.Tokens.Reasoning, 5) ||
		record.Usage.EstimatedCost == nil ||
		*record.Usage.EstimatedCost != "0.0001008" {
		t.Fatalf("DeepSeek persisted Attempt/Usage=%+v", record)
	}
	usage, err := readChatCommandUsage(ctx, composition.store, result)
	if err != nil {
		t.Fatalf("read CLI DeepSeek Usage: %v", err)
	}
	if usage == nil || !deepSeekTokenEquals(usage.InputTokens, 100) ||
		!deepSeekTokenEquals(usage.CachedInputTokens, 40) ||
		!deepSeekTokenEquals(usage.UncachedInputTokens, 60) ||
		!deepSeekTokenEquals(usage.OutputTokens, 20) ||
		!deepSeekTokenEquals(usage.ReasoningTokens, 5) ||
		usage.EstimatedCost == nil || *usage.EstimatedCost != "0.0001008" ||
		usage.ProviderReportedCost != nil || usage.ReconciledCost != nil ||
		usage.Status != "PROVIDER_REPORTED" ||
		usage.PriceSnapshotID != record.Attempt.PriceSnapshotID ||
		usage.Currency != "CNY" {
		t.Fatalf("CLI DeepSeek Usage=%+v", usage)
	}

	retry, err := composition.chat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("retry DeepSeek Pure Chat: %v", err)
	}
	retryUsage, err := readChatCommandUsage(ctx, composition.store, retry)
	if err != nil {
		t.Fatalf("read retry CLI DeepSeek Usage: %v", err)
	}
	if retry.RunID != result.RunID || retry.AdmissionCreated ||
		retry.TerminalResult == nil || result.TerminalResult == nil ||
		retry.TerminalResult.AttemptID != result.TerminalResult.AttemptID ||
		!reflect.DeepEqual(retryUsage, usage) {
		t.Fatalf(
			"DeepSeek exact retry changed result/Usage: first=%+v/%+v retry=%+v/%+v",
			result,
			usage,
			retry,
			retryUsage,
		)
	}
	if calls := fake.snapshot(); len(calls) != 1 {
		t.Fatalf("DeepSeek exact retry semantic call count=%d, want 1", len(calls))
	}
}

func TestProductionDeepSeekPureChatUnknownNeverSemanticallyReplays(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	fake := &deepSeekPureChatRoundTripper{unknown: true}
	composition := openDeepSeekPureChatTestComposition(t, ctx, fake)
	defer closeDeepSeekPureChatTestComposition(t, composition)
	assertDeepSeekPureChatHasNoOptionalBindings(t, ctx, composition)

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "deepseek-chat",
		Message:     "Do not replay this ambiguous request.",
		RequestID:   "deepseek-pure-chat-unknown-1",
		Deadline:    time.Now().UTC().Round(0).Add(5 * time.Minute),
	}
	first, err := composition.chat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("first ambiguous DeepSeek Chat: %v", err)
	}
	if first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.ReasonCode != string(modulehost.UnknownClassInvokeReturnedError) ||
		first.Reply != "" || first.TerminalResult != nil {
		t.Fatalf("first ambiguous DeepSeek result=%+v", first)
	}
	second, err := composition.chat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("retry ambiguous DeepSeek Chat: %v", err)
	}
	if second.RunID != first.RunID || second.AdmissionCreated ||
		second.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		second.LoopResult.ReasonCode != first.LoopResult.ReasonCode {
		t.Fatalf("retry ambiguous DeepSeek result=%+v first=%+v", second, first)
	}
	if calls := fake.snapshot(); len(calls) != 1 {
		t.Fatalf("ambiguous DeepSeek semantic call count=%d, want 1", len(calls))
	}
	unsettled, err := composition.store.ScanUnsettledModelDispatchRecords(
		ctx,
		first.RunID,
	)
	if err != nil {
		t.Fatalf("scan ambiguous DeepSeek Attempt: %v", err)
	}
	if len(unsettled) != 1 ||
		unsettled[0].Attempt.RunID != first.RunID ||
		unsettled[0].Attempt.State != corecontract.ModelAttemptUnknown ||
		unsettled[0].Attempt.UnknownReason !=
			string(modulehost.UnknownClassInvokeReturnedError) ||
		!deepSeekUsageUnknown(unsettled[0].Usage.Tokens) {
		t.Fatalf("ambiguous DeepSeek persisted record=%+v", unsettled)
	}
}

func openDeepSeekPureChatTestComposition(
	t *testing.T,
	ctx context.Context,
	transport http.RoundTripper,
) *productionComposition {
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
		t.Fatalf("initialize DeepSeek Pure Chat data: %v", err)
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
				"unexpected DeepSeek identity: %+v",
				identity,
			)
		}
		return []byte(deepSeekPureChatTestKey), nil
	})
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			DeepSeek: &productionDeepSeekRuntimeConfig{
				APIKeyResolver: keyResolver,
				HTTPClient: &http.Client{
					Transport: transport,
					Timeout:   5 * time.Second,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("open DeepSeek Pure Chat composition: %v", err)
	}
	return composition
}

func closeDeepSeekPureChatTestComposition(
	t *testing.T,
	composition *productionComposition,
) {
	t.Helper()
	if err := composition.Close(); err != nil {
		t.Errorf("close DeepSeek Pure Chat composition: %v", err)
	}
}

func assertDeepSeekPureChatHasNoOptionalBindings(
	t *testing.T,
	ctx context.Context,
	composition *productionComposition,
) {
	t.Helper()
	_, control, catalog, err := composition.store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load DeepSeek Pure Chat basis: %v", err)
	}
	if len(control.Profiles) != 1 || len(control.Profiles[0].Bindings) != 1 ||
		len(catalog.Entries) != 1 ||
		control.Profiles[0].Bindings[0].Port != productionModelPort {
		t.Fatalf("DeepSeek Pure Chat loaded optional modules: %+v / %+v", control, catalog)
	}
	prepared, err := bootstrapseed.PrepareFile(filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	))
	if err != nil || len(prepared.ModuleAssertions()) != 1 {
		t.Fatalf("DeepSeek Pure Chat seed assertions=%+v err=%v", prepared, err)
	}
}

type deepSeekPureChatWireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepSeekPureChatWireRequest struct {
	Model    string                        `json:"model"`
	Messages []deepSeekPureChatWireMessage `json:"messages"`
}

type deepSeekPureChatCall struct {
	Method   string
	URL      string
	Model    string
	Messages []deepSeekPureChatWireMessage
}

type deepSeekPureChatRoundTripper struct {
	mu      sync.Mutex
	unknown bool
	calls   []deepSeekPureChatCall
}

func (fake *deepSeekPureChatRoundTripper) snapshot() []deepSeekPureChatCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	result := make([]deepSeekPureChatCall, len(fake.calls))
	copy(result, fake.calls)
	for index := range result {
		result[index].Messages = append(
			[]deepSeekPureChatWireMessage(nil),
			result[index].Messages...,
		)
	}
	return result
}

func (fake *deepSeekPureChatRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if request == nil || request.Method != http.MethodPost ||
		request.URL.String() != deepSeekPureChatOfficialURL ||
		request.Header.Get("Authorization") != "Bearer "+deepSeekPureChatTestKey ||
		request.Header.Get("Accept") != "application/json" ||
		request.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("unexpected DeepSeek Pure Chat request")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek Pure Chat request: %w", err)
	}
	if err := request.Body.Close(); err != nil {
		return nil, fmt.Errorf("close DeepSeek Pure Chat request: %w", err)
	}
	var wire deepSeekPureChatWireRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decode DeepSeek Pure Chat request: %w", err)
	}
	call := deepSeekPureChatCall{
		Method:   request.Method,
		URL:      request.URL.String(),
		Model:    wire.Model,
		Messages: append([]deepSeekPureChatWireMessage(nil), wire.Messages...),
	}
	fake.mu.Lock()
	fake.calls = append(fake.calls, call)
	unknown := fake.unknown
	fake.mu.Unlock()
	if unknown {
		return nil, errors.New("synthetic ambiguous transport result after dispatch")
	}

	responseBody, err := json.Marshal(map[string]any{
		"id":                 "deepseek-pure-chat-response-1",
		"object":             "chat.completion",
		"created":            int64(1785800000),
		"model":              deepseekmodel.ModelV4Flash,
		"system_fingerprint": "deepseek-pure-chat-test-fingerprint",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": "controlled DeepSeek Pure Chat response",
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
		return nil, fmt.Errorf("encode DeepSeek Pure Chat response: %w", err)
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

func deepSeekTokenEquals(value *uint64, expected uint64) bool {
	return value != nil && *value == expected
}

func deepSeekUsageUnknown(tokens corecontract.UsageTokens) bool {
	return tokens.Input == nil && tokens.CachedInput == nil &&
		tokens.UncachedInput == nil && tokens.Output == nil &&
		tokens.Reasoning == nil
}
