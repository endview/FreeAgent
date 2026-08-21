package modulehandler

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	matrixTenantIDV1    = "tenant-a"
	matrixWorkspaceIDV1 = "workspace-a"
)

func TestBindingMatrixResolvesAllGenericAndReservedHandlersExactly(t *testing.T) {
	t.Parallel()

	modelConfig, modelAuthority := modelBindingV1(t, matrixTenantIDV1)
	declarativeConfig, declarativeAuthority := matrixDeclarativeBindingV1(t)
	knowledgeConfig, knowledgeAuthority := knowledgeBindingV1(t, matrixTenantIDV1)
	memoryConfig, memoryAuthority := matrixMemoryBindingV1(t, matrixTenantIDV1)
	mcpConfig, mcpAuthority := matrixActionBindingV1(
		t,
		matrixTenantIDV1,
		"mcp.echo",
		"example.mcp.echo",
		moduleapi.EffectNone,
		256,
		json.RawMessage(`{}`),
	)
	remoteConfig, remoteAuthority := remoteBindingV1(t, matrixTenantIDV1)
	wasmConfig, wasmAuthority := matrixActionBindingV1(
		t,
		matrixTenantIDV1,
		"wasm.echo",
		"example.wasm.echo",
		moduleapi.EffectNone,
		256,
		json.RawMessage(`{}`),
	)
	textStatsConfig, textStatsAuthority := matrixTextStatsBindingV1(
		t,
		matrixTenantIDV1,
		TextStatsMaxResultBytesV1,
	)
	channelConfig, channelAuthority := matrixLoopbackBindingV1(t, matrixTenantIDV1)

	generic := GenericTableV1()
	tests := []struct {
		name  string
		input BindingV1
		want  PolicyV1
	}{
		{
			name: "DeepSeek Model",
			input: matrixBindingInputV1(
				modelPortV1(), modelConfig, modelAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				&ModuleIdentityV1{
					ID:             DeepSeekModuleIDV1,
					ExactVersion:   DeepSeekVersionV1,
					ArtifactDigest: DeepSeekArtifactDigestV1,
				},
			),
			want: generic[0],
		},
		{
			name: "declarative Context",
			input: matrixBindingInputV1(
				contextPortV1(), declarativeConfig, declarativeAuthority,
				moduleapi.RuntimeModeRequestDeclarative,
				moduleapi.RuntimeProtocolStaticV1,
				matrixGenericModuleV1("declarative"),
			),
			want: generic[1],
		},
		{
			name: "Knowledge Context",
			input: matrixBindingInputV1(
				contextPortV1(), knowledgeConfig, knowledgeAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("knowledge"),
			),
			want: generic[2],
		},
		{
			name: "Memory Context",
			input: matrixBindingInputV1(
				contextPortV1(), memoryConfig, memoryAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("memory"),
			),
			want: generic[3],
		},
		{
			name: "MCP Action",
			input: matrixBindingInputV1(
				actionPortV1(), mcpConfig, mcpAuthority,
				moduleapi.RuntimeModeRequestLocalProcess,
				moduleapi.RuntimeProtocolMCPStdio20251125,
				matrixGenericModuleV1("mcp"),
			),
			want: generic[4],
		},
		{
			name: "REMOTE Action",
			input: matrixBindingInputV1(
				actionPortV1(), remoteConfig, remoteAuthority,
				moduleapi.RuntimeModeRequestRemote,
				moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
				matrixGenericModuleV1("remote"),
			),
			want: generic[5],
		},
		{
			name: "WASM Action",
			input: matrixBindingInputV1(
				actionPortV1(), wasmConfig, wasmAuthority,
				moduleapi.RuntimeModeRequestWASM,
				moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
				matrixGenericModuleV1("wasm"),
			),
			want: generic[6],
		},
		{
			name: "text.stats Action",
			input: matrixBindingInputV1(
				actionPortV1(), textStatsConfig, textStatsAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("text-stats"),
			),
			want: generic[7],
		},
		{
			name: "loopback Channel",
			input: matrixBindingInputV1(
				channelPortV1(), channelConfig, channelAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("loopback"),
			),
			want: generic[8],
		},
	}
	if len(tests) != len(generic) {
		t.Fatalf("generic matrix rows=%d want=%d", len(tests), len(generic))
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			matrixAssertSupportedPolicyV1(t, test.input, test.want)
		})
	}

	documentInsight := &ModuleIdentityV1{
		ID:             moduleapi.DocumentInsightModuleIDV1,
		ExactVersion:   moduleapi.DocumentInsightVersionV1,
		ArtifactDigest: moduleapi.DocumentInsightArtifactDigestV1,
	}
	reserved := ExactSelectorTableV1()
	reservedTests := []struct {
		name  string
		input BindingV1
		want  PolicyV1
	}{
		{
			name: "Document Insight Context",
			input: matrixBindingInputV1(
				contextPortV1(), knowledgeConfig, knowledgeAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				documentInsight,
			),
			want: reserved[0],
		},
		{
			name: "Document Insight Action",
			input: matrixBindingInputV1(
				actionPortV1(), textStatsConfig, textStatsAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				documentInsight,
			),
			want: reserved[1],
		},
	}
	for _, test := range reservedTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			matrixAssertSupportedPolicyV1(t, test.input, test.want)
		})
	}
}

func TestKnownHandlerSpecificBindingConflictsStayDistinctFromUnsupported(t *testing.T) {
	t.Parallel()

	_, remoteParameters, err := moduleapi.NewRemoteActionHTTPBindingParametersV1(
		moduleapi.RemoteActionHTTPBindingParametersV1{
			SchemaVersion: moduleapi.RemoteActionHTTPBindingParametersSchemaV1,
			EndpointURL:   "https://api.example.com/actions",
			SecretRef:     "placeholder",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	mcpConfig, mcpAuthority := matrixActionBindingV1(
		t,
		matrixTenantIDV1,
		"mcp.echo",
		"example.mcp.echo",
		moduleapi.EffectNone,
		256,
		remoteParameters,
	)
	wasmConfig, wasmAuthority := matrixActionBindingV1(
		t,
		matrixTenantIDV1,
		"wasm.read",
		"example.wasm.read",
		moduleapi.EffectReadOnly,
		256,
		json.RawMessage(`{}`),
	)
	textStatsConfig, textStatsAuthority := matrixTextStatsBindingV1(
		t,
		matrixTenantIDV1,
		TextStatsMaxResultBytesV1+1,
	)
	generic := GenericTableV1()
	tests := []struct {
		name  string
		input BindingV1
		want  PolicyV1
	}{
		{
			name: "MCP rejects non-empty provider parameters",
			input: matrixBindingInputV1(
				actionPortV1(), mcpConfig, mcpAuthority,
				moduleapi.RuntimeModeRequestLocalProcess,
				moduleapi.RuntimeProtocolMCPStdio20251125,
				matrixGenericModuleV1("mcp-conflict"),
			),
			want: generic[4],
		},
		{
			name: "WASM rejects non-none effect",
			input: matrixBindingInputV1(
				actionPortV1(), wasmConfig, wasmAuthority,
				moduleapi.RuntimeModeRequestWASM,
				moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
				matrixGenericModuleV1("wasm-conflict"),
			),
			want: generic[6],
		},
		{
			name: "text.stats rejects widened result ceiling",
			input: matrixBindingInputV1(
				actionPortV1(), textStatsConfig, textStatsAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("text-stats-conflict"),
			),
			want: generic[7],
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policyOnly, err := ResolvePolicyOnlyV1(test.input)
			if err != nil {
				t.Fatalf("ResolvePolicyOnlyV1: %v", err)
			}
			if policyOnly != test.want {
				t.Fatalf("policy-only resolution=%+v want=%+v", policyOnly, test.want)
			}
			if _, err := ResolveBindingV1(test.input); err == nil {
				t.Fatal("incompatible exact Binding resolved")
			}
			assessment, err := AssessBindingV1(test.input)
			if err != nil {
				t.Fatalf("AssessBindingV1: %v", err)
			}
			if assessment.Status != AssessmentConflictV1 || assessment.Policy != test.want {
				t.Fatalf("assessment=%+v want status=%q policy=%+v", assessment, AssessmentConflictV1, test.want)
			}
		})
	}
}

func TestUnknownHandlerTupleAndMalformedEnvelopeClassifyFailClosed(t *testing.T) {
	t.Parallel()

	textStatsConfig, textStatsAuthority := matrixTextStatsBindingV1(
		t,
		matrixTenantIDV1,
		TextStatsMaxResultBytesV1,
	)
	declarativeConfig, declarativeAuthority := matrixDeclarativeBindingV1(t)
	tests := []struct {
		name  string
		input BindingV1
	}{
		{
			name: "unknown runtime",
			input: matrixBindingInputV1(
				actionPortV1(), textStatsConfig, textStatsAuthority,
				moduleapi.RuntimeModeRequest("FUTURE"),
				"future-action/v1",
				matrixGenericModuleV1("future-runtime"),
			),
		},
		{
			name: "known runtime but unknown consumer tuple",
			input: matrixBindingInputV1(
				contextPortV1(), declarativeConfig, declarativeAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				matrixGenericModuleV1("unknown-tuple"),
			),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ResolveBindingV1(test.input); err == nil {
				t.Fatal("unknown handler tuple resolved")
			}
			assessment, err := AssessBindingV1(test.input)
			if err != nil {
				t.Fatalf("AssessBindingV1: %v", err)
			}
			if assessment.Status != AssessmentUnsupportedV1 || assessment.Policy != (PolicyV1{}) {
				t.Fatalf("assessment=%+v want status=%q with no policy", assessment, AssessmentUnsupportedV1)
			}
		})
	}

	malformed := matrixBindingInputV1(
		actionPortV1(), []byte(`{"truncated":`), textStatsAuthority,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		moduleapi.RuntimeProtocolGoInProcessV1,
		matrixGenericModuleV1("malformed"),
	)
	if _, err := ResolveBindingV1(malformed); err == nil {
		t.Fatal("malformed Binding resolved")
	}
	assessment, err := AssessBindingV1(malformed)
	if err == nil {
		t.Fatalf("malformed Binding was downgraded to assessment=%+v", assessment)
	}
	if assessment != (AssessmentV1{}) {
		t.Fatalf("malformed Binding returned partial assessment=%+v error=%v", assessment, err)
	}
}

func TestDocumentInsightIdentityAndTupleDriftNeverFallsBackToGenericHandler(t *testing.T) {
	t.Parallel()

	knowledgeConfig, knowledgeAuthority := knowledgeBindingV1(t, matrixTenantIDV1)
	memoryConfig, memoryAuthority := matrixMemoryBindingV1(t, matrixTenantIDV1)
	modelConfig, modelAuthority := modelBindingV1(t, matrixTenantIDV1)
	exactModule := ModuleIdentityV1{
		ID:             moduleapi.DocumentInsightModuleIDV1,
		ExactVersion:   moduleapi.DocumentInsightVersionV1,
		ArtifactDigest: moduleapi.DocumentInsightArtifactDigestV1,
	}
	reserved := ExactSelectorTableV1()
	tests := []struct {
		name       string
		input      BindingV1
		wantStatus AssessmentStatusV1
		wantPolicy PolicyV1
	}{
		{
			name: "version drift",
			input: matrixBindingInputV1(
				contextPortV1(), knowledgeConfig, knowledgeAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				&ModuleIdentityV1{
					ID:             exactModule.ID,
					ExactVersion:   "1.0.1",
					ArtifactDigest: exactModule.ArtifactDigest,
				},
			),
			wantStatus: AssessmentConflictV1,
			wantPolicy: reserved[0],
		},
		{
			name: "digest drift",
			input: matrixBindingInputV1(
				contextPortV1(), knowledgeConfig, knowledgeAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				&ModuleIdentityV1{
					ID:             exactModule.ID,
					ExactVersion:   exactModule.ExactVersion,
					ArtifactDigest: strings.Repeat("f", moduleapi.SHA256HexLength),
				},
			),
			wantStatus: AssessmentConflictV1,
			wantPolicy: reserved[0],
		},
		{
			name: "Port drift cannot select generic Model",
			input: matrixBindingInputV1(
				modelPortV1(), modelConfig, modelAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				&exactModule,
			),
			wantStatus: AssessmentConflictV1,
			wantPolicy: reserved[0],
		},
		{
			name: "runtime drift cannot select declarative Context",
			input: matrixBindingInputV1(
				contextPortV1(), knowledgeConfig, knowledgeAuthority,
				moduleapi.RuntimeModeRequestDeclarative,
				moduleapi.RuntimeProtocolStaticV1,
				&exactModule,
			),
			wantStatus: AssessmentUnsupportedV1,
		},
		{
			name: "schema drift cannot select generic Memory",
			input: matrixBindingInputV1(
				contextPortV1(), memoryConfig, memoryAuthority,
				moduleapi.RuntimeModeRequestTrustedInProcess,
				moduleapi.RuntimeProtocolGoInProcessV1,
				&exactModule,
			),
			wantStatus: AssessmentConflictV1,
			wantPolicy: reserved[0],
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if policy, err := ResolveBindingV1(test.input); err == nil {
				t.Fatalf("drift resolved through policy=%+v", policy)
			} else if !errors.Is(err, ErrProtocolHandlerNotFoundV1) {
				t.Fatalf("drift failed outside exact selector: %v", err)
			}
			assessment, err := AssessBindingV1(test.input)
			if err != nil {
				t.Fatalf("AssessBindingV1: %v", err)
			}
			if assessment.Status != test.wantStatus || assessment.Policy != test.wantPolicy {
				t.Fatalf(
					"assessment=%+v want status=%q policy=%+v",
					assessment,
					test.wantStatus,
					test.wantPolicy,
				)
			}
			if assessment.Policy.HandlerKind != "" && assessment.Policy.HandlerKind != HandlerDocumentInsightV1 {
				t.Fatalf("reserved identity fell back to generic handler %q", assessment.Policy.HandlerKind)
			}
		})
	}
}

func matrixAssertSupportedPolicyV1(t *testing.T, input BindingV1, want PolicyV1) {
	t.Helper()
	policyOnly, err := ResolvePolicyOnlyV1(input)
	if err != nil {
		t.Fatalf("ResolvePolicyOnlyV1: %v", err)
	}
	if policyOnly != want {
		t.Fatalf("policy-only resolution=%+v want=%+v", policyOnly, want)
	}
	resolved, err := ResolveBindingV1(input)
	if err != nil {
		t.Fatalf("ResolveBindingV1: %v", err)
	}
	if resolved != want {
		t.Fatalf("resolved policy=%+v want=%+v", resolved, want)
	}
	assessment, err := AssessBindingV1(input)
	if err != nil {
		t.Fatalf("AssessBindingV1: %v", err)
	}
	if assessment.Status != AssessmentSupportedV1 || assessment.Policy != want {
		t.Fatalf("assessment=%+v want status=%q policy=%+v", assessment, AssessmentSupportedV1, want)
	}
}

func matrixBindingInputV1(
	port moduleapi.PortRef,
	configCanonical []byte,
	authorityCanonical []byte,
	mode moduleapi.RuntimeModeRequest,
	protocol string,
	module *ModuleIdentityV1,
) BindingV1 {
	return BindingV1{
		TenantID:           matrixTenantIDV1,
		Port:               port,
		ConfigCanonical:    append([]byte(nil), configCanonical...),
		AuthorityCanonical: append([]byte(nil), authorityCanonical...),
		FailurePolicy:      moduleapi.FailureRequired,
		RuntimeRequest: RuntimeRequestV1{
			Mode:     mode,
			Protocol: protocol,
		},
		Module: module,
	}
}

func matrixGenericModuleV1(name string) *ModuleIdentityV1 {
	return &ModuleIdentityV1{
		ID:             "example." + name,
		ExactVersion:   "1.0.0",
		ArtifactDigest: moduleapi.Digest("freeagent.modulehandler.matrix/v1", []byte(name)),
	}
}

func matrixDeclarativeBindingV1(t *testing.T) ([]byte, []byte) {
	t.Helper()
	_, config, err := moduleapi.NewContextBindingConfigV1(moduleapi.ContextBindingConfigV1{
		SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
		Placement:     moduleapi.ContextPlacementTrustedInstruction,
		AllowSummary:  false,
		AllowDrop:     false,
		Parameters:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, DenyAllAuthorityCanonicalV1()
}

func matrixMemoryBindingV1(t *testing.T, tenantID string) ([]byte, []byte) {
	t.Helper()
	kinds := []moduleapi.MemoryEntryKindV1{
		moduleapi.MemoryEntryTaskSummary,
		moduleapi.MemoryEntryCategoryCount,
		moduleapi.MemoryEntryRepeatedTermCount,
	}
	_, parameters, _, err := moduleapi.NewMemoryContextBindingV1(moduleapi.MemoryContextBindingV1{
		SchemaVersion:     moduleapi.MemoryContextBindingSchemaV1,
		Kinds:             kinds,
		MaxItems:          16,
		MaxTotalTextBytes: 8192,
		CategoryRules: []moduleapi.MemoryCategoryRuleV1{{
			Key: "programming", Terms: []string{"code", "go"},
		}},
		StopTerms:           []string{"the"},
		SummaryMaxTextBytes: 256,
		EntryTTLSeconds:     86400,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewContextBindingConfigV1(moduleapi.ContextBindingConfigV1{
		SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
		Placement:     moduleapi.ContextPlacementUntrustedData,
		AllowSummary:  false,
		AllowDrop:     false,
		Parameters:    parameters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewMemoryAuthorityCeilingV1(moduleapi.MemoryAuthorityCeilingV1{
		SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
		TenantID:            tenantID,
		AgentID:             "agent-a",
		AllowedWorkspaceIDs: []string{matrixWorkspaceIDV1},
		AllowedKinds:        kinds,
		MaxItems:            32,
		MaxTotalTextBytes:   16384,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}

func matrixActionBindingV1(
	t *testing.T,
	tenantID string,
	publicActionID string,
	providerActionID string,
	effect moduleapi.EffectClass,
	maxResultBytes uint32,
	parameters json.RawMessage,
) ([]byte, []byte) {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(moduleapi.ActionBindingConfigV1{
		SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
		Actions: []moduleapi.ActionBindingMappingV1{{
			PublicActionID:   publicActionID,
			ProviderActionID: providerActionID,
			LocalEffectClass: effect,
			MaxResultBytes:   maxResultBytes,
		}},
		Parameters: parameters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(moduleapi.ActionAuthorityCeilingV1{
		SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
		TenantID:                 tenantID,
		AllowedWorkspaceIDs:      []string{matrixWorkspaceIDV1},
		AllowedProviderActionIDs: []string{providerActionID},
		MaxEffectClass:           effect,
		MaxResultBytes:           maxResultBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}

func matrixTextStatsBindingV1(t *testing.T, tenantID string, maxResultBytes uint32) ([]byte, []byte) {
	t.Helper()
	return matrixActionBindingV1(
		t,
		tenantID,
		TextStatsActionIDV1,
		TextStatsActionIDV1,
		moduleapi.EffectNone,
		maxResultBytes,
		json.RawMessage(`{}`),
	)
}

func matrixLoopbackBindingV1(t *testing.T, tenantID string) ([]byte, []byte) {
	t.Helper()
	parameters, err := moduleapi.CanonicalJSON([]byte(
		`{"schema_version":"loopback-http-parameters/v1","inbound_path":"/channel/modulehandler","outbound_url":"http://127.0.0.1:18080/channel/outbound","request_timeout_ms":1000}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewChannelBindingConfigV1(moduleapi.ChannelBindingConfigV1{
		SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
		AdapterProtocol: LoopbackAdapterProtocolV1,
		SecretRef:       "placeholder",
		Parameters:      parameters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewChannelAuthorityCeilingV1(moduleapi.ChannelAuthorityCeilingV1{
		SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
		TenantID:            tenantID,
		AllowedWorkspaceIDs: []string{matrixWorkspaceIDV1},
		AllowedEndpointIDs:  []string{"endpoint-a"},
		AllowReceive:        true,
		AllowSend:           true,
		MaxMessageBytes:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}

func TestBindingMatrixHelpersProduceDetachedCanonicalWires(t *testing.T) {
	t.Parallel()
	config, authority := matrixTextStatsBindingV1(t, matrixTenantIDV1, TextStatsMaxResultBytesV1)
	input := matrixBindingInputV1(
		actionPortV1(), config, authority,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		moduleapi.RuntimeProtocolGoInProcessV1,
		matrixGenericModuleV1("detached"),
	)
	config[0] ^= 1
	authority[0] ^= 1
	if reflect.DeepEqual(input.ConfigCanonical, config) || reflect.DeepEqual(input.AuthorityCanonical, authority) {
		t.Fatal("matrix Binding retained caller-owned canonical wire")
	}
	matrixAssertSupportedPolicyV1(t, input, GenericTableV1()[7])
}
