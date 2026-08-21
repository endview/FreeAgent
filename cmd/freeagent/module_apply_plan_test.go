package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRestoreModuleApplyPlanV1EnabledAndDisabledAreCanonicalDetached(t *testing.T) {
	t.Parallel()

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()
		input := canonicalModuleApplyPlanTestJSON(t, enabledModuleApplyPlanTestValue())
		original := bytes.Clone(input)
		plan, canonical, digest, err := restoreModuleApplyPlanV1(input)
		if err != nil {
			t.Fatalf("restoreModuleApplyPlanV1() error = %v", err)
		}
		if plan.SchemaVersion != moduleApplyPlanSchemaV1 ||
			plan.DesiredState != moduleApplyEnabledV1 ||
			plan.TenantID != "tenant-1" ||
			plan.BindingTarget != (moduleApplyBindingTargetV1{
				Kind:      moduleApplyBindingTargetProfileV1,
				ProfileID: "profile-1",
			}) ||
			plan.InstanceID != "instance-1" ||
			plan.Port != productionActionPort ||
			plan.ExpectedPointerRevision != 7 || plan.Module == nil ||
			plan.Binding == nil || plan.Binding.PortBindingIndex != 0 ||
			plan.Binding.FailurePolicy != moduleapi.FailureRequired {
			t.Fatalf("restored enabled plan = %+v", plan)
		}
		if plan.Module.ID != "example.tool" ||
			plan.Module.ExactVersion != "1.0.0" ||
			plan.Module.ArtifactDigest != strings.Repeat("a", 64) ||
			plan.Module.ArtifactSizeBytes != 1234 ||
			plan.Module.ExpectedRuntimeRequest !=
				(moduleApplyExpectedRuntimeRequestV1{
					Mode:     moduleapi.RuntimeModeRequestLocalProcess,
					Protocol: moduleapi.RuntimeProtocolMCPStdio20251125,
				}) {
			t.Fatalf("restored enabled module = %+v", plan.Module)
		}
		if !bytes.Equal(canonical, original) {
			t.Fatalf("canonical bytes changed: %s", canonical)
		}
		if digest != independentModuleApplyPlanDigest(canonical) ||
			!moduleapi.ValidSHA256(digest) {
			t.Fatalf("plan digest = %q", digest)
		}

		// The caller input, canonical result, Config, and Authority must not
		// share mutable storage.
		input[0] = '['
		if !bytes.Equal(canonical, original) {
			t.Fatal("canonical result aliases caller input")
		}
		configBefore := bytes.Clone(plan.Binding.Config)
		authorityBefore := bytes.Clone(plan.Binding.AuthorityCeiling)
		canonical[0] = '['
		if !bytes.Equal(plan.Binding.Config, configBefore) ||
			!bytes.Equal(plan.Binding.AuthorityCeiling, authorityBefore) {
			t.Fatal("plan payload aliases returned canonical bytes")
		}
		plan.Binding.Config[0] = '['
		if plan.Binding.AuthorityCeiling[0] != '{' {
			t.Fatal("Config aliases Authority payload")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		input := canonicalModuleApplyPlanTestJSON(t, disabledModuleApplyPlanTestValue())
		plan, canonical, digest, err := restoreModuleApplyPlanV1(input)
		if err != nil {
			t.Fatalf("restoreModuleApplyPlanV1() error = %v", err)
		}
		if plan.DesiredState != moduleApplyDisabledV1 || plan.Module != nil ||
			plan.Binding != nil || !bytes.Equal(canonical, input) ||
			digest != independentModuleApplyPlanDigest(input) {
			t.Fatalf("restored disabled plan = %+v digest=%q", plan, digest)
		}
	})
}

func TestRestoreModuleApplyPlanV1DeclarativeContextExactPolicy(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()
		input := canonicalModuleApplyPlanTestJSON(t, declarativeModuleApplyPlanTestValue())
		plan, canonical, digest, err := restoreModuleApplyPlanV1(input)
		if err != nil {
			t.Fatalf("restore declarative module apply plan: %v", err)
		}
		if plan.Port != productionContextPort || plan.Binding == nil ||
			plan.Binding.FailurePolicy != moduleapi.FailureOptional ||
			plan.Module == nil ||
			plan.Module.ExpectedRuntimeRequest !=
				(moduleApplyExpectedRuntimeRequestV1{
					Mode:     moduleapi.RuntimeModeRequestDeclarative,
					Protocol: moduleapi.RuntimeProtocolStaticV1,
				}) ||
			!bytes.Equal(canonical, input) || !moduleapi.ValidSHA256(digest) {
			t.Fatalf("restored declarative plan=%+v digest=%q", plan, digest)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "untrusted placement",
			mutate: func(value map[string]any) {
				moduleApplyTestConfig(value)["placement"] = string(moduleapi.ContextPlacementUntrustedData)
			},
		},
		{
			name: "summary enabled",
			mutate: func(value map[string]any) {
				moduleApplyTestConfig(value)["allow_summary"] = true
			},
		},
		{
			name: "drop enabled",
			mutate: func(value map[string]any) {
				moduleApplyTestConfig(value)["allow_drop"] = true
			},
		},
		{
			name: "nonempty parameters",
			mutate: func(value map[string]any) {
				moduleApplyTestConfig(value)["parameters"] = map[string]any{"mode": "dynamic"}
			},
		},
		{
			name: "authority is not exact deny all",
			mutate: func(value map[string]any) {
				moduleApplyTestAuthority(value)["network_allowlist"] = []any{"example.com"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := declarativeModuleApplyPlanTestValue()
			test.mutate(value)
			if _, _, _, err := restoreModuleApplyPlanV1(
				canonicalModuleApplyPlanTestJSON(t, value),
			); err == nil {
				t.Fatal("restoreModuleApplyPlanV1 accepted non-exact declarative policy")
			}
		})
	}
}

func TestRestoreModuleApplyPlanV1AcceptsExactCoreRuntimeRequests(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		mode     moduleapi.RuntimeModeRequest
		protocol string
	}{
		{
			name:     "trusted in-process",
			mode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			protocol: moduleapi.RuntimeProtocolGoInProcessV1,
		},
		{
			name:     "local MCP process",
			mode:     moduleapi.RuntimeModeRequestLocalProcess,
			protocol: moduleapi.RuntimeProtocolMCPStdio20251125,
		},
		{
			name:     "remote Action HTTP",
			mode:     moduleapi.RuntimeModeRequestRemote,
			protocol: moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := enabledModuleApplyPlanTestValue()
			moduleApplyTestModule(value)["expected_runtime_request"] = map[string]any{
				"mode":     string(test.mode),
				"protocol": test.protocol,
			}
			plan, _, _, err := restoreModuleApplyPlanV1(
				canonicalModuleApplyPlanTestJSON(t, value),
			)
			if err != nil {
				t.Fatalf("restore exact runtime request: %v", err)
			}
			if plan.Module == nil ||
				plan.Module.ExpectedRuntimeRequest.Mode != test.mode ||
				plan.Module.ExpectedRuntimeRequest.Protocol != test.protocol {
				t.Fatalf("restored module=%+v", plan.Module)
			}
		})
	}
}

func TestRestoreModuleApplyPlanV1RejectsInvalidWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload func(*testing.T) []byte
	}{
		{
			name: "noncanonical whitespace",
			payload: func(t *testing.T) []byte {
				return append(
					canonicalModuleApplyPlanTestJSON(t, enabledModuleApplyPlanTestValue()),
					'\n',
				)
			},
		},
		{
			name: "noncanonical key order",
			payload: func(*testing.T) []byte {
				return []byte(`{"schema_version":"module-apply-plan/v1","binding_target":{"kind":"PROFILE","profile_id":"profile-1"},"desired_state":"DISABLED","tenant_id":"tenant-1","expected_pointer_revision":7,"instance_id":"instance-1"}`)
			},
		},
		{
			name: "top level array",
			payload: func(t *testing.T) []byte {
				return canonicalModuleApplyPlanTestJSON(t, []any{})
			},
		},
		{
			name: "top level unknown field",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["automatic"] = true
			}),
		},
		{
			name: "wrong schema",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["schema_version"] = "module-apply-plan/v2"
			}),
		},
		{
			name: "unsupported desired state",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["desired_state"] = "AUTO"
			}),
		},
		{
			name: "zero pointer revision",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["expected_pointer_revision"] = 0
			}),
		},
		{
			name: "exhausted pointer revision",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["expected_pointer_revision"] = uint64(math.MaxInt64)
			}),
		},
		{
			name: "blank tenant ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["tenant_id"] = " "
			}),
		},
		{
			name: "trimmed profile ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBindingTarget(value)["profile_id"] = " profile-1"
			}),
		},
		{
			name: "oversize instance ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["instance_id"] = strings.Repeat("i", moduleapi.MaxOpaqueIDBytes+1)
			}),
		},
		{
			name: "non NFC tenant ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["tenant_id"] = "Cafe\u0301"
			}),
		},
		{
			name: "control character in instance ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["instance_id"] = "instance\n1"
			}),
		},
		{
			name: "missing port",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(value, "port")
			}),
		},
		{
			name: "unsupported port",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["port"] = map[string]any{"name": "observer.consume", "exact_version": "v1"}
			}),
		},
		{
			name: "enabled omits module",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(value, "module")
			}),
		},
		{
			name: "enabled null module",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["module"] = nil
			}),
		},
		{
			name: "enabled omits binding",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(value, "binding")
			}),
		},
		{
			name: "enabled null binding",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["binding"] = nil
			}),
		},
		{
			name: "module unknown field",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["execution_class"] = "LOCAL_PROCESS"
			}),
		},
		{
			name: "expected runtime request omitted",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(moduleApplyTestModule(value), "expected_runtime_request")
			}),
		},
		{
			name: "expected runtime request null",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["expected_runtime_request"] = nil
			}),
		},
		{
			name: "expected runtime request blank mode",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["mode"] = ""
			}),
		},
		{
			name: "expected runtime request mode omitted",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(moduleApplyTestExpectedRuntimeRequest(value), "mode")
			}),
		},
		{
			name: "expected runtime request protocol omitted",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(moduleApplyTestExpectedRuntimeRequest(value), "protocol")
			}),
		},
		{
			name: "expected runtime request unsupported remote mode",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				request := moduleApplyTestExpectedRuntimeRequest(value)
				request["mode"] = string(moduleapi.RuntimeModeRequestRemote)
				request["protocol"] = "https-json/v1"
			}),
		},
		{
			name: "expected runtime request mismatched exact pair",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["protocol"] =
					moduleapi.RuntimeProtocolStaticV1
			}),
		},
		{
			name: "expected runtime request noncanonical mode spelling",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["mode"] = "LOCAL_PROCESS "
			}),
		},
		{
			name: "expected runtime request noncanonical protocol spelling",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["protocol"] =
					"mcp-stdio/2025-11-25 "
			}),
		},
		{
			name: "expected runtime request execution field forbidden",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["execution_class"] =
					"LOCAL_PROCESS"
			}),
		},
		{
			name: "expected runtime request adapter field forbidden",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["adapter_identity"] =
					"freeagent.adapter.example/v1"
			}),
		},
		{
			name: "expected runtime request trust field forbidden",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["trust_class"] = "TRUSTED"
			}),
		},
		{
			name: "expected runtime request handler field forbidden",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestExpectedRuntimeRequest(value)["handler"] = "text.stats"
			}),
		},
		{
			name: "invalid module ID",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["id"] = "Example.Tool"
			}),
		},
		{
			name: "invalid exact version",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["exact_version"] = ".1"
			}),
		},
		{
			name: "uppercase artifact digest",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["artifact_digest"] = strings.Repeat("A", 64)
			}),
		},
		{
			name: "zero artifact size",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestModule(value)["artifact_size_bytes"] = 0
			}),
		},
		{
			name: "binding unknown field",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["fallback"] = true
			}),
		},
		{
			name: "binding index omitted",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(moduleApplyTestBinding(value), "port_binding_index")
			}),
		},
		{
			name: "binding index null",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["port_binding_index"] = nil
			}),
		},
		{
			name: "binding index exceeds uint32",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["port_binding_index"] = uint64(math.MaxUint32) + 1
			}),
		},
		{
			name: "binding failure policy omitted",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				delete(moduleApplyTestBinding(value), "failure_policy")
			}),
		},
		{
			name: "Action binding is optional",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["failure_policy"] = string(moduleapi.FailureOptional)
			}),
		},
		{
			name: "null Action config",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["config"] = nil
			}),
		},
		{
			name: "Action config unknown field",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestConfig(value)["fallback"] = true
			}),
		},
		{
			name: "Action config empty actions",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestConfig(value)["actions"] = []any{}
			}),
		},
		{
			name: "Action config nonempty parameters",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestConfig(value)["parameters"] = map[string]any{"temperature": 1}
			}),
		},
		{
			name: "null Action authority",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestBinding(value)["authority_ceiling"] = nil
			}),
		},
		{
			name: "Action authority unknown field",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestAuthority(value)["secret"] = true
			}),
		},
		{
			name: "Action authority tenant mismatch",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				moduleApplyTestAuthority(value)["tenant_id"] = "tenant-2"
			}),
		},
		{
			name: "disabled carries enabled payload",
			payload: mutateEnabledModuleApplyPlanTestValue(func(value map[string]any) {
				value["desired_state"] = "DISABLED"
			}),
		},
		{
			name: "disabled carries null optional fields",
			payload: func(t *testing.T) []byte {
				value := disabledModuleApplyPlanTestValue()
				value["module"] = nil
				value["binding"] = nil
				return canonicalModuleApplyPlanTestJSON(t, value)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, canonical, digest, err := restoreModuleApplyPlanV1(test.payload(t))
			if err == nil {
				t.Fatalf(
					"restoreModuleApplyPlanV1() = %+v, %q, %q; want error",
					plan,
					canonical,
					digest,
				)
			}
			if plan.Module != nil || plan.Binding != nil || canonical != nil || digest != "" {
				t.Fatalf("failed restore leaked partial result: %+v %q %q", plan, canonical, digest)
			}
		})
	}
}

func TestReadModuleApplyPlanV1RequiresBoundedOrdinaryCanonicalFile(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "apply.json")
		input := canonicalModuleApplyPlanTestJSON(t, enabledModuleApplyPlanTestValue())
		if err := os.WriteFile(path, input, 0o600); err != nil {
			t.Fatalf("write plan: %v", err)
		}
		plan, canonical, digest, err := readModuleApplyPlanV1(path)
		if err != nil {
			t.Fatalf("readModuleApplyPlanV1() error = %v", err)
		}
		if plan.Module == nil || !bytes.Equal(canonical, input) ||
			digest != independentModuleApplyPlanDigest(input) {
			t.Fatalf("read result = %+v %q %q", plan, canonical, digest)
		}
	})

	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		if _, _, _, err := readModuleApplyPlanV1(t.TempDir()); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted a directory")
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "empty.json")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write empty plan: %v", err)
		}
		if _, _, _, err := readModuleApplyPlanV1(path); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted an empty file")
		}
	})

	t.Run("oversize", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "large.json")
		if err := os.WriteFile(
			path,
			bytes.Repeat([]byte("x"), maximumModuleApplyPlanBytes+1),
			0o600,
		); err != nil {
			t.Fatalf("write oversize plan: %v", err)
		}
		if _, _, _, err := readModuleApplyPlanV1(path); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted an oversize file")
		}
	})

	t.Run("noncanonical", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pretty.json")
		input := append(
			canonicalModuleApplyPlanTestJSON(t, disabledModuleApplyPlanTestValue()),
			'\n',
		)
		if err := os.WriteFile(path, input, 0o600); err != nil {
			t.Fatalf("write noncanonical plan: %v", err)
		}
		if _, _, _, err := readModuleApplyPlanV1(path); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted noncanonical JSON")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, "target.json")
		link := filepath.Join(root, "link.json")
		input := canonicalModuleApplyPlanTestJSON(t, disabledModuleApplyPlanTestValue())
		if err := os.WriteFile(target, input, 0o600); err != nil {
			t.Fatalf("write symlink target: %v", err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, _, _, err := readModuleApplyPlanV1(link); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted a symlink")
		}
	})

	t.Run("case-only symlink on case-sensitive filesystem", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows paths are case-insensitive")
		}
		root := t.TempDir()
		target := filepath.Join(root, "Plan.json")
		link := filepath.Join(root, "plan.json")
		input := canonicalModuleApplyPlanTestJSON(t, disabledModuleApplyPlanTestValue())
		if err := os.WriteFile(target, input, 0o600); err != nil {
			t.Fatalf("write case-only symlink target: %v", err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, _, _, err := readModuleApplyPlanV1(link); err == nil {
			t.Fatal("readModuleApplyPlanV1 accepted a case-only symlink")
		}
	})
}

func enabledModuleApplyPlanTestValue() map[string]any {
	return map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 "tenant-1",
		"expected_pointer_revision": uint64(7),
		"binding_target": map[string]any{
			"kind":       string(moduleApplyBindingTargetProfileV1),
			"profile_id": "profile-1",
		},
		"instance_id": "instance-1",
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  "example.tool",
			"exact_version":       "1.0.0",
			"artifact_digest":     strings.Repeat("a", 64),
			"artifact_size_bytes": uint64(1234),
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestLocalProcess),
				"protocol": moduleapi.RuntimeProtocolMCPStdio20251125,
			},
		},
		"binding": map[string]any{
			"port_binding_index": uint32(0),
			"failure_policy":     string(moduleapi.FailureRequired),
			"config": map[string]any{
				"schema_version": moduleapi.ActionBindingConfigSchemaV1,
				"actions": []any{map[string]any{
					"public_action_id":   "text.stats",
					"provider_action_id": "text.stats",
					"local_effect_class": string(moduleapi.EffectReadOnly),
					"max_result_bytes":   uint32(1024),
				}},
				"parameters": map[string]any{},
			},
			"authority_ceiling": map[string]any{
				"schema_version":              moduleapi.ActionAuthorityCeilingSchemaV1,
				"tenant_id":                   "tenant-1",
				"allowed_workspace_ids":       []any{"workspace-1"},
				"allowed_provider_action_ids": []any{"text.stats"},
				"max_effect_class":            string(moduleapi.EffectReadOnly),
				"max_result_bytes":            uint32(1024),
			},
		},
	}
}

func declarativeModuleApplyPlanTestValue() map[string]any {
	value := enabledModuleApplyPlanTestValue()
	moduleApplyTestModule(value)["expected_runtime_request"] = map[string]any{
		"mode":     string(moduleapi.RuntimeModeRequestDeclarative),
		"protocol": moduleapi.RuntimeProtocolStaticV1,
	}
	value["port"] = map[string]any{
		"name":          productionContextPort.Name,
		"exact_version": productionContextPort.ExactVersion,
	}
	binding := moduleApplyTestBinding(value)
	binding["failure_policy"] = string(moduleapi.FailureOptional)
	binding["config"] = map[string]any{
		"schema_version": moduleapi.ContextBindingConfigSchemaV1,
		"placement":      string(moduleapi.ContextPlacementTrustedInstruction),
		"allow_summary":  false,
		"allow_drop":     false,
		"parameters":     map[string]any{},
	}
	binding["authority_ceiling"] = map[string]any{
		"schema_version":    "authority-ceiling/v1",
		"effects":           []any{},
		"filesystem_roots":  []any{},
		"network_allowlist": []any{},
		"secret_refs":       []any{},
	}
	return value
}

func disabledModuleApplyPlanTestValue() map[string]any {
	return map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 "tenant-1",
		"expected_pointer_revision": uint64(7),
		"binding_target": map[string]any{
			"kind":       string(moduleApplyBindingTargetProfileV1),
			"profile_id": "profile-1",
		},
		"instance_id": "instance-1",
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
	}
}

func mutateEnabledModuleApplyPlanTestValue(
	mutate func(map[string]any),
) func(*testing.T) []byte {
	return func(t *testing.T) []byte {
		value := enabledModuleApplyPlanTestValue()
		mutate(value)
		return canonicalModuleApplyPlanTestJSON(t, value)
	}
}

func moduleApplyTestModule(value map[string]any) map[string]any {
	return value["module"].(map[string]any)
}

func moduleApplyTestBinding(value map[string]any) map[string]any {
	return value["binding"].(map[string]any)
}

func moduleApplyTestBindingTarget(value map[string]any) map[string]any {
	return value["binding_target"].(map[string]any)
}

func moduleApplyTestExpectedRuntimeRequest(
	value map[string]any,
) map[string]any {
	return moduleApplyTestModule(value)["expected_runtime_request"].(map[string]any)
}

func moduleApplyTestConfig(value map[string]any) map[string]any {
	return moduleApplyTestBinding(value)["config"].(map[string]any)
}

func moduleApplyTestAuthority(value map[string]any) map[string]any {
	return moduleApplyTestBinding(value)["authority_ceiling"].(map[string]any)
}

func canonicalModuleApplyPlanTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal module apply plan fixture: %v", err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatalf("canonicalize module apply plan fixture: %v", err)
	}
	return canonical
}

func independentModuleApplyPlanDigest(canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(moduleApplyPlanDigestDomainV1))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}
