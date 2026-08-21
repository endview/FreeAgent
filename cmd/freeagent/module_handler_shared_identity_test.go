package main

import (
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestSharedModuleHandlerIdentityMatchesProductionAdaptersV1(t *testing.T) {
	t.Run("fixed identities", func(t *testing.T) {
		stringCases := []struct {
			name string
			got  string
			want string
		}{
			{"DeepSeek Module ID", modulehandler.DeepSeekModuleIDV1, localDeepSeekModuleID},
			{"DeepSeek version", modulehandler.DeepSeekVersionV1, localDeepSeekVersion},
			{"DeepSeek artifact digest", modulehandler.DeepSeekArtifactDigestV1, localDeepSeekDigest},
			{"DeepSeek provider", modulehandler.DeepSeekProviderNameV1, deepseekmodel.ProviderNameV1},
			{"DeepSeek flash model", modulehandler.DeepSeekModelV4FlashV1, deepseekmodel.ModelV4Flash},
			{"DeepSeek pro model", modulehandler.DeepSeekModelV4ProV1, deepseekmodel.ModelV4Pro},
			{"DeepSeek flash build", modulehandler.DeepSeekFlashBuildV1, localDeepSeekFlashBuild},
			{"DeepSeek pro build", modulehandler.DeepSeekProBuildV1, localDeepSeekProBuild},
			{"DeepSeek adapter", modulehandler.DeepSeekAdapterIdentityV1, deepseekmodel.AdapterIdentityV1},
			{"declarative adapter", modulehandler.DeclarativeAdapterIdentityV1, declarativeAdapterID},
			{"knowledge adapter", modulehandler.KnowledgeAdapterIdentityV1, localKnowledgeAdapterID},
			{"memory adapter", modulehandler.MemoryAdapterIdentityV1, localMemoryAdapterID},
			{"text.stats adapter", modulehandler.TextStatsAdapterIdentityV1, localTextStatsAdapterID},
			{"MCP stdio adapter", modulehandler.MCPStdioAdapterIdentityV1, mcpstdio.AdapterIdentityV1},
			{"REMOTE Action adapter", modulehandler.RemoteActionAdapterIdentityV1, remoteactionhttp.AdapterIdentityV1},
			{"WASM Action adapter", modulehandler.WASMActionAdapterIdentityV1, wasmaction.AdapterIdentityV1},
			{"loopback Channel adapter", modulehandler.LoopbackAdapterIdentityV1, loopbackchannel.AdapterIdentityV1},
			{"loopback Channel protocol", modulehandler.LoopbackAdapterProtocolV1, loopbackchannel.AdapterProtocolV1},
			{"Document Insight adapter", modulehandler.DocumentInsightAdapterIdentityV1, exactadapter.DocumentInsightAdapterIdentityV1},
			{"text.stats Action ID", modulehandler.TextStatsActionIDV1, exactadapter.TextStatsActionIDV1},
		}
		for _, testCase := range stringCases {
			t.Run(testCase.name, func(t *testing.T) {
				if testCase.got != testCase.want {
					t.Fatalf("shared identity=%q production identity=%q", testCase.got, testCase.want)
				}
			})
		}
		if modulehandler.DeepSeekMaxOutputTokensV1 != uint64(deepseekmodel.MaximumOutputTokensV1) {
			t.Fatalf(
				"DeepSeek maximum output tokens=%d production=%d",
				modulehandler.DeepSeekMaxOutputTokensV1,
				deepseekmodel.MaximumOutputTokensV1,
			)
		}
		if modulehandler.TextStatsMaxResultBytesV1 != uint32(exactadapter.TextStatsMaxResultBytesV1) {
			t.Fatalf(
				"text.stats maximum result bytes=%d production=%d",
				modulehandler.TextStatsMaxResultBytesV1,
				exactadapter.TextStatsMaxResultBytesV1,
			)
		}

		wantBuilds := map[string]string{
			deepseekmodel.ModelV4Flash: localDeepSeekFlashBuild,
			deepseekmodel.ModelV4Pro:   localDeepSeekProBuild,
		}
		if got := modulehandler.DeepSeekBuildsV1(); !reflect.DeepEqual(got, wantBuilds) {
			t.Fatalf("shared DeepSeek builds=%v production builds=%v", got, wantBuilds)
		}
	})

	modelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	actionPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
	channelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}

	t.Run("ordered generic policies", func(t *testing.T) {
		want := [9]modulehandler.PolicyV1{
			{
				Port:                                  modelPort,
				RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:                        moduleapi.ModelBindingConfigSchemaV1,
				ModuleID:                              localDeepSeekModuleID,
				ExactVersion:                          localDeepSeekVersion,
				ArtifactDigest:                        localDeepSeekDigest,
				ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:                       deepseekmodel.AdapterIdentityV1,
				HandlerKind:                           modulehandler.KindV1("DEEPSEEK_MODEL"),
				RequiresTrustedInProcessArtifactGrant: true,
			},
			{
				Port:            contextPort,
				RuntimeMode:     moduleapi.RuntimeModeRequestDeclarative,
				RuntimeProtocol: moduleapi.RuntimeProtocolStaticV1,
				ConsumerSchema:  moduleapi.ContextBindingConfigSchemaV1,
				ExecutionClass:  moduleapi.ExecutionDeclarative,
				AdapterIdentity: declarativeAdapterID,
				HandlerKind:     modulehandler.KindV1("DECLARATIVE_CONTEXT"),
			},
			{
				Port:            contextPort,
				RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:  moduleapi.KnowledgeContextBindingSchemaV1,
				ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity: localKnowledgeAdapterID,
				HandlerKind:     modulehandler.KindV1("KNOWLEDGE_CONTEXT"),
			},
			{
				Port:            contextPort,
				RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:  moduleapi.MemoryContextBindingSchemaV1,
				ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity: localMemoryAdapterID,
				HandlerKind:     modulehandler.KindV1("MEMORY_CONTEXT"),
			},
			{
				Port:                  actionPort,
				RuntimeMode:           moduleapi.RuntimeModeRequestLocalProcess,
				RuntimeProtocol:       moduleapi.RuntimeProtocolMCPStdio20251125,
				ConsumerSchema:        moduleapi.ActionBindingConfigSchemaV1,
				ExecutionClass:        moduleapi.ExecutionLocalProcess,
				AdapterIdentity:       mcpstdio.AdapterIdentityV1,
				HandlerKind:           modulehandler.KindV1("MCP_ACTION"),
				RequiresLocalMCPGrant: true,
			},
			{
				Port:                              actionPort,
				RuntimeMode:                       moduleapi.RuntimeModeRequestRemote,
				RuntimeProtocol:                   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
				ConsumerSchema:                    moduleapi.ActionBindingConfigSchemaV1,
				ExecutionClass:                    moduleapi.ExecutionRemote,
				AdapterIdentity:                   remoteactionhttp.AdapterIdentityV1,
				HandlerKind:                       modulehandler.KindV1("REMOTE_ACTION_HTTP"),
				RequiresRemoteActionArtifactGrant: true,
			},
			{
				Port:                            actionPort,
				RuntimeMode:                     moduleapi.RuntimeModeRequestWASM,
				RuntimeProtocol:                 moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
				ConsumerSchema:                  moduleapi.ActionBindingConfigSchemaV1,
				ExecutionClass:                  moduleapi.ExecutionWASM,
				AdapterIdentity:                 wasmaction.AdapterIdentityV1,
				HandlerKind:                     modulehandler.KindV1("WASM_ACTION"),
				RequiresWASMActionArtifactGrant: true,
			},
			{
				Port:                                  actionPort,
				RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:                        moduleapi.ActionBindingConfigSchemaV1,
				ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:                       localTextStatsAdapterID,
				HandlerKind:                           modulehandler.KindV1("TEXT_STATS_ACTION"),
				RequiresTrustedInProcessArtifactGrant: true,
			},
			{
				Port:                                  channelPort,
				RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:                        moduleapi.ChannelBindingConfigSchemaV1,
				ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:                       loopbackchannel.AdapterIdentityV1,
				HandlerKind:                           modulehandler.KindV1("LOOPBACK_CHANNEL"),
				RequiresTrustedInProcessArtifactGrant: true,
			},
		}
		if got := modulehandler.GenericTableV1(); !reflect.DeepEqual(got, want) {
			t.Fatalf("shared generic policies drifted\n got: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("ordered reserved policies", func(t *testing.T) {
		want := [2]modulehandler.PolicyV1{
			{
				Port:                                  contextPort,
				RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:                        moduleapi.KnowledgeContextBindingSchemaV1,
				ModuleID:                              moduleapi.DocumentInsightModuleIDV1,
				ExactVersion:                          moduleapi.DocumentInsightVersionV1,
				ArtifactDigest:                        moduleapi.DocumentInsightArtifactDigestV1,
				ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:                       exactadapter.DocumentInsightAdapterIdentityV1,
				HandlerKind:                           modulehandler.KindV1("DOCUMENT_INSIGHT"),
				RequiresTrustedInProcessArtifactGrant: true,
			},
			{
				Port:                                  actionPort,
				RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
				RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
				ConsumerSchema:                        moduleapi.ActionBindingConfigSchemaV1,
				ModuleID:                              moduleapi.DocumentInsightModuleIDV1,
				ExactVersion:                          moduleapi.DocumentInsightVersionV1,
				ArtifactDigest:                        moduleapi.DocumentInsightArtifactDigestV1,
				ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:                       exactadapter.DocumentInsightAdapterIdentityV1,
				HandlerKind:                           modulehandler.KindV1("DOCUMENT_INSIGHT"),
				RequiresTrustedInProcessArtifactGrant: true,
			},
		}
		if got := modulehandler.ExactSelectorTableV1(); !reflect.DeepEqual(got, want) {
			t.Fatalf("shared reserved policies drifted\n got: %#v\nwant: %#v", got, want)
		}
	})
}

func TestSharedModuleHandlerGrantKindsMatchReviewContractV1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		shared modulehandler.GrantKindV1
		review moduleupgrade.RequiredGrantKindV1
	}{
		{modulehandler.GrantLocalProcessArtifactV1, moduleupgrade.GrantLocalProcessArtifactV1},
		{modulehandler.GrantTrustedInProcessArtifactV1, moduleupgrade.GrantTrustedInProcessArtifactV1},
		{modulehandler.GrantRemoteActionArtifactV1, moduleupgrade.GrantRemoteActionArtifactV1},
		{modulehandler.GrantWASMActionArtifactV1, moduleupgrade.GrantWASMActionArtifactV1},
		{modulehandler.GrantRemoteEndpointDigestV1, moduleupgrade.GrantRemoteEndpointDigestV1},
		{modulehandler.GrantRemoteCredentialDigestV1, moduleupgrade.GrantRemoteCredentialDigestV1},
		{modulehandler.GrantModelCredentialDigestV1, moduleupgrade.GrantModelCredentialDigestV1},
	}
	for _, testCase := range cases {
		if string(testCase.shared) != string(testCase.review) {
			t.Fatalf("shared grant kind %q differs from Review kind %q", testCase.shared, testCase.review)
		}
	}
}
