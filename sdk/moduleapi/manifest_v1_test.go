package moduleapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseModuleManifestV1PreservesOrderAndReturnsOwnedCanonicalBytes(t *testing.T) {
	input := []byte(
		`{"api_version":"freeagent.module/v1","id":"context.static","provides":[{"exact_version":"v1","name":"context.provide"},{"exact_version":"v2","name":"context.provide"}],"requested_permissions":["workspace.read","network.http"],"requires":[{"exact_version":"v1","name":"model.generate"}],"runtime":{"entrypoint":"content/role.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"v1"}`,
	)
	original := bytes.Clone(input)

	manifest, canonical, err := ParseModuleManifestV1(input)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1() error = %v", err)
	}
	if !bytes.Equal(canonical, original) {
		t.Fatalf("canonical bytes changed:\n got %s\nwant %s", canonical, original)
	}
	if got := []string{
		manifest.Provides[0].ExactVersion,
		manifest.Provides[1].ExactVersion,
	}; !reflect.DeepEqual(got, []string{"v1", "v2"}) {
		t.Fatalf("provides order = %v", got)
	}
	if !reflect.DeepEqual(
		manifest.RequestedPermissions,
		[]Permission{"workspace.read", "network.http"},
	) {
		t.Fatalf(
			"requested permission order = %v",
			manifest.RequestedPermissions,
		)
	}
	if manifest.ConfigSchema != nil ||
		manifest.Lifecycle != nil ||
		manifest.Health != nil {
		t.Fatalf("missing optional objects were not nil")
	}

	input[0] = '['
	if !bytes.Equal(canonical, original) {
		t.Fatalf("returned canonical bytes alias input")
	}
	manifest.Provides[0].Name = "changed"
	manifest.RequestedPermissions[0] = "changed"
	if bytes.Contains(canonical, []byte("changed")) {
		t.Fatalf("canonical bytes alias returned manifest")
	}
}

func TestParseModuleManifestV1AcceptsTrustedRequestAndInertObjects(t *testing.T) {
	input := []byte(
		`{"api_version":"freeagent.module/v1","config_schema":{"properties":{"temperature":{"type":"number"}},"type":"object"},"health":{"probe":"adapter"},"id":"model.provider","lifecycle":{"close":true},"provides":[{"exact_version":"v1","name":"model.generate"}],"runtime":{"entrypoint":"builtin.deepseek","mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"version":"v4"}`,
	)

	manifest, _, err := ParseModuleManifestV1(input)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1() error = %v", err)
	}
	if manifest.Runtime.Mode != RuntimeModeRequestTrustedInProcess {
		t.Fatalf("runtime mode = %q", manifest.Runtime.Mode)
	}
	if string(manifest.ConfigSchema) !=
		`{"properties":{"temperature":{"type":"number"}},"type":"object"}` {
		t.Fatalf("config schema = %s", manifest.ConfigSchema)
	}
	if reflect.TypeOf(manifest.Runtime.Mode) ==
		reflect.TypeOf(ExecutionClass("")) {
		t.Fatalf("runtime request and ExecutionClass unexpectedly share a type")
	}
}

func TestParseModuleManifestV1AcceptsExternalRuntimeRequests(t *testing.T) {
	tests := []struct {
		mode       RuntimeModeRequest
		protocol   string
		entrypoint string
	}{
		{
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   RuntimeProtocolMCPStdio20251125,
			entrypoint: "content/mcp-server",
		},
		{
			mode:       RuntimeModeRequestRemote,
			protocol:   RuntimeProtocolFreeAgentActionHTTPV1,
			entrypoint: "content/action-http.json",
		},
		{
			mode:       RuntimeModeRequestRemote,
			protocol:   "custom-rpc/v7",
			entrypoint: "remote.adapter.alpha",
		},
		{
			mode:       RuntimeModeRequestWASM,
			protocol:   RuntimeProtocolFreeAgentActionWASMV1,
			entrypoint: "content/action-wasm.json",
		},
		{
			mode:       RuntimeModeRequestWASM,
			protocol:   "custom-wasm/v7",
			entrypoint: "wasm.adapter.alpha",
		},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			manifest := validManifestStruct()
			manifest.Runtime = RuntimeRequestV1{
				Mode:       test.mode,
				Protocol:   test.protocol,
				Entrypoint: test.entrypoint,
			}
			input := mustCanonicalManifestJSON(t, manifest)
			parsed, _, err := ParseModuleManifestV1(input)
			if err != nil {
				t.Fatalf("ParseModuleManifestV1() error = %v", err)
			}
			if parsed.Runtime != manifest.Runtime {
				t.Fatalf("runtime request = %+v, want %+v", parsed.Runtime, manifest.Runtime)
			}
		})
	}
}

func TestParseModuleManifestV1RequiresCanonicalJSONObject(t *testing.T) {
	tests := map[string][]byte{
		"empty":          nil,
		"yaml mapping":   []byte("api_version: freeagent.module/v1"),
		"array":          []byte(`[]`),
		"null":           []byte(`null`),
		"malformed":      []byte(`{"api_version":`),
		"whitespace":     []byte(` {}`),
		"unordered keys": []byte(`{"version":"v1","api_version":"freeagent.module/v1"}`),
		"duplicate key":  []byte(`{"api_version":"freeagent.module/v1","api_version":"freeagent.module/v1"}`),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseModuleManifestV1(input); err == nil {
				t.Fatalf("ParseModuleManifestV1() error = nil")
			}
		})
	}
}

func TestParseModuleManifestV1RejectsUnknownOldAndSelfAuthorizingFields(t *testing.T) {
	forbidden := []string{
		"kind",
		"optional_requires",
		"conflicts",
		"required_permissions",
		"max_effect_class",
		"manifest_hash",
		"artifact_digest",
		"signature",
		"trust",
		"control_class",
		"effect",
		"failure_policy",
		"execution_class",
		"runtime_authority",
		"unknown",
	}
	for _, field := range forbidden {
		t.Run(field, func(t *testing.T) {
			payload := validManifestValue()
			payload[field] = "forbidden"
			input := mustCanonicalManifestJSON(t, payload)
			if _, _, err := ParseModuleManifestV1(input); err == nil {
				t.Fatalf("field %q accepted", field)
			}
		})
	}
}

func TestParseModuleManifestV1RejectsUnknownNestedFields(t *testing.T) {
	tests := []func(map[string]any){
		func(payload map[string]any) {
			payload["runtime"].(map[string]any)["execution_class"] =
				"TRUSTED_IN_PROCESS"
		},
		func(payload map[string]any) {
			payload["provides"].([]any)[0].(map[string]any)["capability"] =
				"model.generate"
		},
	}
	for index, mutate := range tests {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			payload := validManifestValue()
			mutate(payload)
			if _, _, err := ParseModuleManifestV1(
				mustCanonicalManifestJSON(t, payload),
			); err == nil {
				t.Fatalf("nested unknown field accepted")
			}
		})
	}
}

func TestModuleManifestV1ValidatesRuntimeRequestProfile(t *testing.T) {
	tests := []struct {
		name       string
		mode       RuntimeModeRequest
		protocol   string
		entrypoint string
	}{
		{
			name:       "unknown mode",
			mode:       "GPU",
			protocol:   RuntimeProtocolStaticV1,
			entrypoint: "content/value.json",
		},
		{
			name:       "local process blank protocol",
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   "",
			entrypoint: "content/server",
		},
		{
			name:       "local process wrong protocol",
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   "mcp-stdio/v1",
			entrypoint: "content/server",
		},
		{
			name:       "local process traversal",
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   RuntimeProtocolMCPStdio20251125,
			entrypoint: "content/../implementation/server",
		},
		{
			name:       "local process outside content",
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   RuntimeProtocolMCPStdio20251125,
			entrypoint: "implementation/server",
		},
		{
			name:       "local process backslash",
			mode:       RuntimeModeRequestLocalProcess,
			protocol:   RuntimeProtocolMCPStdio20251125,
			entrypoint: `content\server`,
		},
		{
			name:       "remote padded entrypoint",
			mode:       RuntimeModeRequestRemote,
			protocol:   RuntimeProtocolFreeAgentActionHTTPV1,
			entrypoint: " content/action-http.json ",
		},
		{
			name:       "remote endpoint instead of descriptor",
			mode:       RuntimeModeRequestRemote,
			protocol:   RuntimeProtocolFreeAgentActionHTTPV1,
			entrypoint: "https://module.invalid/action",
		},
		{
			name:       "wasm traversal",
			mode:       RuntimeModeRequestWASM,
			protocol:   RuntimeProtocolFreeAgentActionWASMV1,
			entrypoint: "content/../implementation/action.json",
		},
		{
			name:       "wasm outside content",
			mode:       RuntimeModeRequestWASM,
			protocol:   RuntimeProtocolFreeAgentActionWASMV1,
			entrypoint: "implementation/action.json",
		},
		{
			name:       "declarative wrong protocol",
			mode:       RuntimeModeRequestDeclarative,
			protocol:   RuntimeProtocolGoInProcessV1,
			entrypoint: "content/value.json",
		},
		{
			name:       "declarative traversal",
			mode:       RuntimeModeRequestDeclarative,
			protocol:   RuntimeProtocolStaticV1,
			entrypoint: "content/../implementation/code",
		},
		{
			name:       "declarative outside content",
			mode:       RuntimeModeRequestDeclarative,
			protocol:   RuntimeProtocolStaticV1,
			entrypoint: "implementation/code",
		},
		{
			name:       "declarative backslash",
			mode:       RuntimeModeRequestDeclarative,
			protocol:   RuntimeProtocolStaticV1,
			entrypoint: `content\value.json`,
		},
		{
			name:       "trusted wrong protocol",
			mode:       RuntimeModeRequestTrustedInProcess,
			protocol:   RuntimeProtocolStaticV1,
			entrypoint: "builtin.provider",
		},
		{
			name:       "trusted blank adapter",
			mode:       RuntimeModeRequestTrustedInProcess,
			protocol:   RuntimeProtocolGoInProcessV1,
			entrypoint: "",
		},
		{
			name:       "trusted padded adapter",
			mode:       RuntimeModeRequestTrustedInProcess,
			protocol:   RuntimeProtocolGoInProcessV1,
			entrypoint: " builtin.provider ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifestStruct()
			manifest.Runtime = RuntimeRequestV1{
				Mode:       test.mode,
				Protocol:   test.protocol,
				Entrypoint: test.entrypoint,
			}
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Validate() error = nil")
			}
		})
	}
}

func TestModuleManifestV1RejectsDuplicateCrossedAndInvalidPorts(t *testing.T) {
	base := validManifestStruct()
	tests := []struct {
		name   string
		mutate func(*ModuleManifestV1)
	}{
		{
			name: "missing provides",
			mutate: func(value *ModuleManifestV1) {
				value.Provides = nil
			},
		},
		{
			name: "duplicate provides",
			mutate: func(value *ModuleManifestV1) {
				value.Provides = append(value.Provides, value.Provides[0])
			},
		},
		{
			name: "duplicate requires",
			mutate: func(value *ModuleManifestV1) {
				port := PortRef{Name: "context.provide", ExactVersion: "v1"}
				value.Requires = []PortRef{port, port}
			},
		},
		{
			name: "provide require overlap",
			mutate: func(value *ModuleManifestV1) {
				value.Requires = []PortRef{value.Provides[0]}
			},
		},
		{
			name: "invalid provide",
			mutate: func(value *ModuleManifestV1) {
				value.Provides[0].Name = "Model.Generate"
			},
		},
		{
			name: "invalid require",
			mutate: func(value *ModuleManifestV1) {
				value.Requires = []PortRef{
					{Name: "context.provide", ExactVersion: ""},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneModuleManifestV1(base)
			test.mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Validate() error = nil")
			}
		})
	}
}

func TestModuleManifestV1RejectsDuplicateAndInvalidPermissionRequests(t *testing.T) {
	manifest := validManifestStruct()
	manifest.RequestedPermissions = []Permission{
		"workspace.read",
		"workspace.read",
	}
	if err := manifest.Validate(); err == nil {
		t.Fatalf("duplicate permission accepted")
	}

	manifest = validManifestStruct()
	manifest.RequestedPermissions = []Permission{"Workspace.Read"}
	if err := manifest.Validate(); err == nil {
		t.Fatalf("invalid permission accepted")
	}
}

func TestModuleManifestV1OptionalDeclarationsMustBeCanonicalObjects(t *testing.T) {
	for _, field := range []string{"config_schema", "lifecycle", "health"} {
		for _, invalid := range []json.RawMessage{
			json.RawMessage(`null`),
			json.RawMessage(`[]`),
			json.RawMessage(`"value"`),
			json.RawMessage(`{"z":1,"a":2}`),
		} {
			t.Run(field+"/"+string(invalid), func(t *testing.T) {
				manifest := validManifestStruct()
				switch field {
				case "config_schema":
					manifest.ConfigSchema = invalid
				case "lifecycle":
					manifest.Lifecycle = invalid
				case "health":
					manifest.Health = invalid
				}
				if err := manifest.Validate(); err == nil {
					t.Fatalf("optional declaration %s accepted", invalid)
				}
			})
		}
	}
}

func TestModuleManifestV1DoesNotMarshalAuthorityOrPackageIdentityFields(t *testing.T) {
	manifest := validManifestStruct()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{
		"artifact_digest",
		"signature",
		"manifest_hash",
		"trust",
		"effect",
		"failure_policy",
		"execution_class",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("manifest encoded forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func validManifestStruct() ModuleManifestV1 {
	return ModuleManifestV1{
		APIVersion: ModuleManifestAPIVersionV1,
		ID:         "model.provider",
		Version:    "v1",
		Runtime: RuntimeRequestV1{
			Mode:       RuntimeModeRequestTrustedInProcess,
			Protocol:   RuntimeProtocolGoInProcessV1,
			Entrypoint: "builtin.provider",
		},
		Provides: []PortRef{
			{Name: PortNameModelGenerate, ExactVersion: PortVersionV2},
		},
		RequestedPermissions: []Permission{"network.http"},
	}
}

func validManifestValue() map[string]any {
	return map[string]any{
		"api_version": ModuleManifestAPIVersionV1,
		"id":          "model.provider",
		"version":     "v1",
		"runtime": map[string]any{
			"mode":       string(RuntimeModeRequestTrustedInProcess),
			"protocol":   RuntimeProtocolGoInProcessV1,
			"entrypoint": "builtin.provider",
		},
		"provides": []any{
			map[string]any{
				"name":          PortNameModelGenerate,
				"exact_version": PortVersionV2,
			},
		},
	}
}

func mustCanonicalManifestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	canonical, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	return canonical
}

func TestModuleManifestV1APIVersionAndIdentityAreExact(t *testing.T) {
	manifest := validManifestStruct()
	manifest.APIVersion = "v1"
	if err := manifest.Validate(); err == nil {
		t.Fatalf("wrong api version accepted")
	}

	manifest = validManifestStruct()
	manifest.ID = strings.ToUpper(manifest.ID)
	if err := manifest.Validate(); err == nil {
		t.Fatalf("invalid module ID accepted")
	}

	manifest = validManifestStruct()
	manifest.Version = ""
	if err := manifest.Validate(); err == nil {
		t.Fatalf("empty exact version accepted")
	}
}
