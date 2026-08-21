package exactadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	documentInsightArtifactDigestV1 = "838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea"
	documentInsightArtifactSizeV1   = uint64(1097)
)

func TestDocumentInsightArtifactClosesManifestSourceAndAdapter(t *testing.T) {
	artifact := filepath.Join(
		"..",
		"..",
		"examples",
		"bootstrap-artifacts",
		"freeagent.builtin.document-insight",
		"1.0.0",
	)
	report, err := moduleconformance.VerifyDirectory(
		context.Background(),
		artifact,
	)
	if err != nil {
		t.Fatalf("VerifyDirectory: %v", err)
	}
	if report.Module.ID != "freeagent.builtin.document-insight" ||
		report.Module.ExactVersion != "1.0.0" ||
		report.ArtifactDigest != documentInsightArtifactDigestV1 ||
		report.ArtifactSizeBytes != documentInsightArtifactSizeV1 ||
		report.CoveredFileCount != 2 ||
		report.RuntimeRequest.Mode !=
			string(moduleapi.RuntimeModeRequestTrustedInProcess) ||
		report.RuntimeRequest.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		report.RuntimeRequest.Entrypoint != "content/source.json" ||
		len(report.Provides) != 2 ||
		report.Provides[0].Name != moduleapi.PortNameActionProvider ||
		report.Provides[1].Name != moduleapi.PortNameContextProvide ||
		len(report.Requires) != 1 ||
		report.Requires[0].Name != moduleapi.PortNameModelGenerate ||
		len(report.RequestedPermissions) != 1 ||
		report.RequestedPermissions[0] !=
			string(moduleapi.PermissionKnowledgeReadV1) {
		t.Fatalf("artifact report=%+v", report)
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectory(
		artifact,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := moduleapi.ClassifyExactDocumentInsightManifestV1(
		manifest,
	); err != nil {
		t.Fatalf("ClassifyExactDocumentInsightManifestV1: %v", err)
	}
	sourceCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		context.Background(),
		artifact,
		"content/source.json",
		int64(moduleapi.MaxKnowledgeSourceBytesV1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.RestoreKnowledgeSourceV1(
		sourceCanonical,
	); err != nil {
		t.Fatalf("RestoreKnowledgeSourceV1: %v", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           report.Module.ID,
		Version:            report.Module.ExactVersion,
		ArtifactDigest:     report.ArtifactDigest,
		InstanceID:         "document-insight-artifact-test",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    DocumentInsightAdapterIdentityV1,
		ActivationRevision: 1,
	}
	if _, err := NewDocumentInsight(
		provider,
		sourceCanonical,
	); err != nil {
		t.Fatalf("NewDocumentInsight from artifact: %v", err)
	}
	t.Logf(
		"artifact_digest=%s artifact_size_bytes=%d covered_file_count=%d",
		report.ArtifactDigest,
		report.ArtifactSizeBytes,
		report.CoveredFileCount,
	)
}

func TestDocumentInsightServesContextAndActionFromOneExactAdapter(t *testing.T) {
	provider, sourceCanonical, sourceRef, scope := documentInsightFixture(t)
	adapter, err := NewDocumentInsight(provider, sourceCanonical)
	if err != nil {
		t.Fatalf("NewDocumentInsight: %v", err)
	}

	contextRequest := knowledgeRequestCanonical(
		t,
		sourceRef,
		scope,
		"Go agent",
		3,
		moduleapi.MaxKnowledgeTotalTextBytesV1,
	)
	contextResult, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(provider, contextRequest),
	)
	if err != nil {
		t.Fatalf("Invoke context: %v", err)
	}
	contextOutput, _, err := moduleapi.RestoreKnowledgeContextOutputV1(
		contextResult.Output,
	)
	if err != nil || len(contextOutput.Hits) != 2 ||
		contextResult.Provider != provider ||
		contextResult.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("context result=%+v output=%+v error=%v", contextResult, contextOutput, err)
	}

	describe := moduleapi.ActionDescribeRequestV1{
		SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
		Parameters:    json.RawMessage(`{}`),
	}
	definitions, err := adapter.Describe(context.Background(), describe)
	if err != nil || len(definitions) != 1 ||
		definitions[0].ProviderActionID != TextStatsActionIDV1 {
		t.Fatalf("Describe definitions=%+v error=%v", definitions, err)
	}
	canonicalInput, err := moduleapi.CanonicalizeAndValidateActionInputV1(
		definitions[0].InputSchema,
		json.RawMessage(`{"text":"Go agent"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	actionRequest, _, err := moduleapi.NewActionRequestV1(
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   TextStatsActionIDV1,
			ProviderActionID: TextStatsActionIDV1,
			DefinitionDigest: strings.Repeat("a", moduleapi.SHA256HexLength),
			CanonicalInput:   canonicalInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := adapter.Prepare(context.Background(), actionRequest)
	if err != nil || !bytes.Equal(prepared, canonicalInput) {
		t.Fatalf("Prepare payload=%s error=%v", prepared, err)
	}
	executionRequest, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "document-insight-attempt-1",
			PublicActionID:   TextStatsActionIDV1,
			ProviderActionID: TextStatsActionIDV1,
			DefinitionDigest: strings.Repeat("a", moduleapi.SHA256HexLength),
			MaxResultBytes:   TextStatsMaxResultBytesV1,
			PreparedPayload:  prepared,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := adapter.ExecutePrepared(
		context.Background(),
		documentActionExecution(t, provider, executionRequest),
	)
	if err != nil || execution.Outcome != moduleapi.ActionExecutionSucceeded ||
		string(execution.CanonicalResult) !=
			`{"bytes":8,"lines":1,"runes":8,"words":2}` {
		t.Fatalf("ExecutePrepared result=%+v error=%v", execution, err)
	}
}

func TestDocumentInsightGenericActionInvocationRemainsForbidden(t *testing.T) {
	provider, sourceCanonical, _, _ := documentInsightFixture(t)
	adapter, err := NewDocumentInsight(provider, sourceCanonical)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{
			Invocation: modulehost.ModuleInvocation{Port: actionProviderPortV1},
		},
	)
	if !errors.Is(err, ErrTextStatsGenericInvocation) ||
		result.InvocationID != "" ||
		result.Provider != (moduleapi.ActivatedModuleRef{}) ||
		result.Outcome != "" || len(result.Output) != 0 ||
		len(result.UsageReceipt) != 0 || result.UnknownClass != "" {
		t.Fatalf("generic Action result=%+v error=%v", result, err)
	}

	unsupported := modulehost.PreparedInvocation{
		Invocation: modulehost.ModuleInvocation{
			Port: moduleapi.PortRef{
				Name:         moduleapi.PortNameModelGenerate,
				ExactVersion: moduleapi.PortVersionV1,
			},
		},
	}
	if _, err := adapter.Invoke(
		context.Background(),
		unsupported,
	); !errors.Is(err, ErrDocumentInsightInvocation) {
		t.Fatalf("unsupported Port error=%v", err)
	}
}

func TestNewDocumentInsightFailsClosedOnIdentityClassAndSource(t *testing.T) {
	provider, sourceCanonical, _, _ := documentInsightFixture(t)

	wrongIdentity := provider
	wrongIdentity.AdapterIdentity = "freeagent.adapter.other/v1"
	if _, err := NewDocumentInsight(
		wrongIdentity,
		sourceCanonical,
	); !errors.Is(err, ErrInvalidDocumentInsightProvider) {
		t.Fatalf("wrong identity error=%v", err)
	}

	wrongClass := provider
	wrongClass.ExecutionClass = moduleapi.ExecutionRemote
	if _, err := NewDocumentInsight(
		wrongClass,
		sourceCanonical,
	); !errors.Is(err, ErrInvalidDocumentInsightProvider) {
		t.Fatalf("wrong class error=%v", err)
	}

	var source map[string]any
	if err := json.Unmarshal(sourceCanonical, &source); err != nil {
		t.Fatal(err)
	}
	source["unknown"] = true
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	unknownCanonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDocumentInsight(
		provider,
		unknownCanonical,
	); !errors.Is(err, ErrInvalidDocumentInsightProvider) {
		t.Fatalf("unknown source field error=%v", err)
	}
}

func TestDocumentInsightRejectsZeroValueAndPreservesContextIdentity(t *testing.T) {
	var zero *DocumentInsight
	if _, err := zero.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{},
	); !errors.Is(err, ErrDocumentInsightInvocation) {
		t.Fatalf("nil Invoke error=%v", err)
	}
	if _, err := zero.Describe(
		context.Background(),
		moduleapi.ActionDescribeRequestV1{},
	); !errors.Is(err, ErrTextStatsDescribe) {
		t.Fatalf("nil Describe error=%v", err)
	}

	provider, sourceCanonical, sourceRef, scope := documentInsightFixture(t)
	adapter, err := NewDocumentInsight(provider, sourceCanonical)
	if err != nil {
		t.Fatal(err)
	}
	second := provider
	second.InstanceID = "document-insight-secondary"
	second.ActivationRevision = 9
	request := knowledgeRequestCanonical(
		t,
		sourceRef,
		scope,
		"agent",
		2,
		1024,
	)
	result, err := adapter.Invoke(
		context.Background(),
		knowledgePrepared(second, request),
	)
	if err != nil || result.Provider != second {
		t.Fatalf("shared instance result=%+v error=%v", result, err)
	}
}

func documentInsightFixture(
	t *testing.T,
) (
	moduleapi.ActivatedModuleRef,
	[]byte,
	moduleapi.KnowledgeSourceRefV1,
	moduleapi.KnowledgeQueryScopeV1,
) {
	t.Helper()
	provider, sourceCanonical, sourceRef, scope := knowledgeFixture(t)
	provider.ModuleID = "freeagent.builtin.document-insight"
	provider.Version = "1.0.0"
	provider.InstanceID = "document-insight-main"
	provider.AdapterIdentity = DocumentInsightAdapterIdentityV1
	return provider, sourceCanonical, sourceRef, scope
}

func documentActionExecution(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	request moduleapi.ActionExecutionRequestV1,
) modulehost.PreparedActionExecutionV1 {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   request.PublicActionID,
				ProviderActionID: request.ProviderActionID,
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   request.MaxResultBytes,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 "document-insight-test-tenant",
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{request.ProviderActionID},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           request.MaxResultBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := modulehost.NewPreparedActionExecutionV1(
		modulehost.PreparedActionExecutionV1{
			Request: request,
			Binding: moduleapi.PortBinding{
				Provider:            provider,
				ConfigRef:           documentActionContentDigest("CONFIG", configCanonical),
				AuthorityCeilingRef: documentActionContentDigest("AUTHORITY_CEILING", authorityCanonical),
				FailurePolicy:       moduleapi.FailureRequired,
			},
			ConfigCanonical:    configCanonical,
			AuthorityCanonical: authorityCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func documentActionContentDigest(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("freeagent.content-record/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}
