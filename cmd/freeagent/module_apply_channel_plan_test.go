package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyChannelPlanV1FreezesExplicitWorkspaceEndpoint(t *testing.T) {
	value := validModuleApplyChannelPlanValueV1(t)
	canonical := canonicalModuleApplyPlanTestJSON(t, value)
	plan, restored, _, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatalf("restore enabled Channel plan: %v", err)
	}
	if string(restored) != string(canonical) ||
		plan.BindingTarget.Kind != moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		plan.BindingTarget.WorkspaceID != defaultWorkspaceID ||
		plan.BindingTarget.EndpointID != "endpoint-apply" ||
		plan.BindingTarget.ProfileID != "" || plan.ChannelEndpoint == nil ||
		plan.ChannelEndpoint.TargetAgentID != defaultAgentID ||
		plan.ChannelEndpoint.TargetProfileID != defaultProfileID ||
		string(plan.CursorSeed) != `{"offset":0}` || plan.Binding == nil ||
		plan.Binding.PortBindingIndex != 0 ||
		plan.Binding.FailurePolicy != moduleapi.FailureRequired {
		t.Fatalf("restored enabled Channel plan=%+v", plan)
	}

	disabled := map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": uint64(7),
		"binding_target": map[string]any{
			"kind":         string(moduleApplyBindingTargetWorkspaceChannelEndpointV1),
			"workspace_id": defaultWorkspaceID,
			"endpoint_id":  "endpoint-apply",
		},
		"instance_id": "channel-apply",
		"port": map[string]any{
			"name":          productionChannelPort.Name,
			"exact_version": productionChannelPort.ExactVersion,
		},
	}
	if _, _, _, err := restoreModuleApplyPlanV1(
		canonicalModuleApplyPlanTestJSON(t, disabled),
	); err != nil {
		t.Fatalf("restore disabled Channel plan: %v", err)
	}
}

func TestModuleApplyChannelPlanV1RejectsTargetAndPayloadAmbiguity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "mixed target",
			mutate: func(value map[string]any) {
				value["binding_target"].(map[string]any)["profile_id"] = defaultProfileID
			},
		},
		{
			name: "wrong port",
			mutate: func(value map[string]any) {
				value["port"] = map[string]any{
					"name": productionActionPort.Name, "exact_version": productionActionPort.ExactVersion,
				}
			},
		},
		{
			name: "missing endpoint body",
			mutate: func(value map[string]any) {
				delete(value, "channel_endpoint")
			},
		},
		{
			name: "missing cursor seed",
			mutate: func(value map[string]any) {
				delete(value, "cursor_seed")
			},
		},
		{
			name: "nonzero index",
			mutate: func(value map[string]any) {
				value["binding"].(map[string]any)["port_binding_index"] = 1
			},
		},
		{
			name: "optional failure",
			mutate: func(value map[string]any) {
				value["binding"].(map[string]any)["failure_policy"] = string(moduleapi.FailureOptional)
			},
		},
		{
			name: "invalid endpoint route",
			mutate: func(value map[string]any) {
				value["channel_endpoint"].(map[string]any)["cursor_scope_key"] = " cursor"
			},
		},
		{
			name: "oversized cursor",
			mutate: func(value map[string]any) {
				value["cursor_seed"] = strings.Repeat("x", moduleapi.MaxChannelCursorBytesV1+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validModuleApplyChannelPlanValueV1(t)
			test.mutate(value)
			if _, _, _, err := restoreModuleApplyPlanV1(
				canonicalModuleApplyPlanTestJSON(t, value),
			); err == nil {
				t.Fatal("ambiguous/invalid Channel plan was accepted")
			}
		})
	}

	profile := validModuleApplyChannelPlanValueV1(t)
	profile["binding_target"] = moduleApplyProfileBindingTargetTestValue(defaultProfileID)
	profile["port"] = map[string]any{
		"name": productionActionPort.Name, "exact_version": productionActionPort.ExactVersion,
	}
	if _, _, _, err := restoreModuleApplyPlanV1(
		canonicalModuleApplyPlanTestJSON(t, profile),
	); err == nil {
		t.Fatal("Profile plan carrying Channel route and Cursor payload was accepted")
	}
}

func validModuleApplyChannelPlanValueV1(t *testing.T) map[string]any {
	t.Helper()
	parameters, err := moduleapi.CanonicalJSON([]byte(
		`{"schema_version":"loopback-http-parameters/v1","inbound_path":"/channel/apply","outbound_url":"http://127.0.0.1:18080/channel/outbound","request_timeout_ms":1000}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: loopbackchannel.AdapterProtocolV1,
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            defaultTenantID,
			AllowedWorkspaceIDs: []string{defaultWorkspaceID},
			AllowedEndpointIDs:  []string{"endpoint-apply"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": uint64(7),
		"binding_target": map[string]any{
			"kind":         string(moduleApplyBindingTargetWorkspaceChannelEndpointV1),
			"workspace_id": defaultWorkspaceID,
			"endpoint_id":  "endpoint-apply",
		},
		"instance_id": "channel-apply",
		"port": map[string]any{
			"name":          productionChannelPort.Name,
			"exact_version": productionChannelPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localLoopbackChannelModuleID,
			"exact_version":       localLoopbackChannelVersion,
			"artifact_digest":     strings.Repeat("a", 64),
			"artifact_size_bytes": uint64(1),
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
		"channel_endpoint": map[string]any{
			"channel":           localLoopbackChannelName,
			"account_id":        "account-apply",
			"conversation_id":   "conversation-apply",
			"target_agent_id":   defaultAgentID,
			"target_profile_id": defaultProfileID,
			"cursor_scope_key":  "cursor/channel-apply",
		},
		"cursor_seed": map[string]any{"offset": 0},
	}
}
