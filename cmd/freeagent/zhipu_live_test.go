package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/zhipumodel"
	"github.com/endview/freeagent/sdk/loopapi"
)

// This test is opt-in and requires the credential to be injected by the test
// process. It records no raw response or credential; the regular suite never
// contacts the provider.
func TestOptInRealZhipuGLM45Composition(t *testing.T) {
	if os.Getenv("FREEAGENT_RUN_ZHIPU_REAL") != "1" {
		t.Skip("set FREEAGENT_RUN_ZHIPU_REAL=1 to run the redacted Zhipu experiment")
	}
	credential, found := os.LookupEnv("FREEAGENT_ZHIPU_API_KEY")
	if !found || credential == "" {
		t.Fatal("FREEAGENT_ZHIPU_API_KEY is required for the opt-in experiment")
	}
	resolver := zhipumodel.APIKeyResolverFunc(func(ctx context.Context, identity zhipumodel.APIKeyIdentity) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if identity.Provider != zhipumodel.ProviderNameV1 || identity.Model != zhipumodel.ModelGLM45 || identity.ModelBuildID != localZhipuGLM45Build {
			return nil, fmt.Errorf("unexpected Zhipu identity")
		}
		return []byte(credential), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(filepath.Dir(exampleSeedPath(t)), "current-v1.zhipu.bootstrap.seed.json")
	if _, err := initializeProductionData(ctx, initInput{DatabasePath: databasePath, SeedPath: seedPath, ArtifactRoot: artifactRoot}); err != nil {
		t.Fatalf("initialize Zhipu data: %v", err)
	}
	composition, err := openProductionCompositionWithOptions(ctx, databasePath, artifactRoot, defaultTenantID, productionCompositionOptions{
		Zhipu: &productionZhipuRuntimeConfig{
			APIKeyResolver: resolver,
			HTTPClient:     &http.Client{Timeout: 90 * time.Second},
		},
	})
	if err != nil {
		t.Fatalf("open Zhipu composition: %v", err)
	}
	defer func() {
		if err := composition.Close(); err != nil {
			t.Errorf("close composition: %v", err)
		}
	}()
	result, err := composition.chat.Chat(ctx, localchat.ChatInput{
		TenantID: defaultTenantID, PrincipalID: defaultPrincipalID, WorkspaceID: defaultWorkspaceID,
		AgentID: defaultAgentID, ProfileID: "zhipu-chat", Message: "Reply with exactly: FREEAGENT_P3_COMPOSITION_OK",
		RequestID: "zhipu-p3-real-20260922", Deadline: time.Now().UTC().Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("real Zhipu chat: %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionTerminated || result.TerminalResult == nil || result.TerminalResult.State != corecontract.ModelAttemptSucceeded || result.Reply == "" {
		t.Fatalf("real Zhipu result=%+v", result)
	}
	record, err := composition.store.GetModelDispatchRecord(ctx, result.TerminalResult.AttemptID)
	if err != nil {
		t.Fatalf("read Zhipu Attempt: %v", err)
	}
	if record.Attempt.Provider != zhipumodel.ProviderNameV1 || record.Attempt.Model != zhipumodel.ModelGLM45 || record.Attempt.State != corecontract.ModelAttemptSucceeded || record.Usage.Tokens.Input == nil || record.Usage.Tokens.Output == nil {
		t.Fatalf("real Zhipu record=%+v", record)
	}
	t.Logf("redacted Zhipu P3 evidence: outcome=SUCCEEDED provider=%s model=%s input_tokens_present=true output_tokens_present=true reply_bytes=%d", record.Attempt.Provider, record.Attempt.Model, len(result.Reply))
}
