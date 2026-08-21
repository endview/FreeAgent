package modulehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type actionExecutorCompileAssertion struct{}

func (actionExecutorCompileAssertion) ExecutePrepared(
	context.Context,
	PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	return moduleapi.ActionExecutionResultV1{}, nil
}

var _ ActionExecutor = actionExecutorCompileAssertion{}

func TestPreparedActionExecutionV1ValidatesRefsAndDetachesCallerBytes(
	t *testing.T,
) {
	input := testPreparedActionExecution(t)
	frozen, err := NewPreparedActionExecutionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := bytes.Clone(frozen.Request.PreparedPayload)
	wantConfig := bytes.Clone(frozen.ConfigCanonical)
	wantAuthority := bytes.Clone(frozen.AuthorityCanonical)
	input.Request.PreparedPayload[0] = '['
	input.ConfigCanonical[0] = '['
	input.AuthorityCanonical[0] = '['
	if !bytes.Equal(frozen.Request.PreparedPayload, wantPayload) ||
		!bytes.Equal(frozen.ConfigCanonical, wantConfig) ||
		!bytes.Equal(frozen.AuthorityCanonical, wantAuthority) {
		t.Fatal("prepared Action execution aliases caller-owned bytes")
	}

	for _, mutate := range []func(*PreparedActionExecutionV1){
		func(value *PreparedActionExecutionV1) {
			value.Binding.ConfigRef = strings.Repeat("0", moduleapi.SHA256HexLength)
		},
		func(value *PreparedActionExecutionV1) {
			value.Binding.AuthorityCeilingRef = strings.Repeat("1", moduleapi.SHA256HexLength)
		},
		func(value *PreparedActionExecutionV1) {
			value.Request.ProviderActionID = "text.other"
		},
	} {
		candidate := testPreparedActionExecution(t)
		mutate(&candidate)
		if _, err := NewPreparedActionExecutionV1(candidate); !errors.Is(
			err,
			ErrInvalidPreparedActionExecution,
		) {
			t.Fatalf("mutated closure error = %v", err)
		}
	}
}

func testPreparedActionExecution(t *testing.T) PreparedActionExecutionV1 {
	t.Helper()
	request, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "modulehost-action-attempt",
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text.stats",
			DefinitionDigest: strings.Repeat("d", moduleapi.SHA256HexLength),
			MaxResultBytes:   256,
			PreparedPayload:  json.RawMessage(`{"text":"hello"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
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
			TenantID:                 "modulehost-test-tenant",
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{request.ProviderActionID},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           request.MaxResultBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return PreparedActionExecutionV1{
		Request: request,
		Binding: moduleapi.PortBinding{
			Provider: moduleapi.ActivatedModuleRef{
				ModuleID:           "builtin.action.text",
				Version:            "1.0.0",
				ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
				InstanceID:         "modulehost-text-action",
				ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
				AdapterIdentity:    "builtin.action.text.v1",
				ActivationRevision: 1,
			},
			ConfigRef: actionContentRecordDigestV1(
				"CONFIG",
				configCanonical,
			),
			AuthorityCeilingRef: actionContentRecordDigestV1(
				"AUTHORITY_CEILING",
				authorityCanonical,
			),
			FailurePolicy: moduleapi.FailureRequired,
		},
		ConfigCanonical:    configCanonical,
		AuthorityCanonical: authorityCanonical,
	}
}
