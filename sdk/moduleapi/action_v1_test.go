package moduleapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestActionBindingAndAuthorityV1RoundTripAndOwnership(t *testing.T) {
	configInput := ActionBindingConfigV1{
		SchemaVersion: ActionBindingConfigSchemaV1,
		Actions: []ActionBindingMappingV1{
			{
				PublicActionID:   "text.stats",
				ProviderActionID: "builtin.text_stats",
				LocalEffectClass: EffectNone,
				MaxResultBytes:   2048,
			},
			{
				PublicActionID:   "files.preview",
				ProviderActionID: "builtin.file_preview",
				LocalEffectClass: EffectReadOnly,
				MaxResultBytes:   4096,
			},
		},
		Parameters: json.RawMessage(`{ "language": "zh", "limit": 4 }`),
	}
	config, canonical, err := NewActionBindingConfigV1(configInput)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(config.Parameters); got != `{"language":"zh","limit":4}` {
		t.Fatalf("parameters=%s", got)
	}
	if config.Actions[0].PublicActionID != "text.stats" ||
		config.Actions[1].PublicActionID != "files.preview" {
		t.Fatalf("semantic action order changed: %+v", config.Actions)
	}
	configInput.Actions[0].PublicActionID = "mutated"
	configInput.Parameters[2] = 'x'
	if config.Actions[0].PublicActionID != "text.stats" ||
		string(config.Parameters) != `{"language":"zh","limit":4}` {
		t.Fatal("action binding config aliases caller memory")
	}
	restoredConfig, err := RestoreActionBindingConfigV1(canonical)
	if err != nil || !reflect.DeepEqual(config, restoredConfig) {
		t.Fatalf("restored=%+v err=%v", restoredConfig, err)
	}

	ceilingInput := ActionAuthorityCeilingV1{
		SchemaVersion:            ActionAuthorityCeilingSchemaV1,
		TenantID:                 "tenant-main",
		AllowedWorkspaceIDs:      []string{"workspace.z", "workspace.a"},
		AllowedProviderActionIDs: []string{"builtin.text_stats", "builtin.file_preview"},
		MaxEffectClass:           EffectReadOnly,
		MaxResultBytes:           4096,
	}
	ceiling, ceilingCanonical, err := NewActionAuthorityCeilingV1(ceilingInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		ceiling.AllowedWorkspaceIDs,
		[]string{"workspace.a", "workspace.z"},
	) || !reflect.DeepEqual(
		ceiling.AllowedProviderActionIDs,
		[]string{"builtin.file_preview", "builtin.text_stats"},
	) {
		t.Fatalf("ceiling sets were not frozen canonically: %+v", ceiling)
	}
	ceilingInput.AllowedWorkspaceIDs[0] = "mutated"
	ceilingInput.AllowedProviderActionIDs[0] = "mutated"
	if ceiling.AllowedWorkspaceIDs[0] != "workspace.a" ||
		ceiling.AllowedProviderActionIDs[0] != "builtin.file_preview" {
		t.Fatal("action authority ceiling aliases caller memory")
	}
	if restored, err := RestoreActionAuthorityCeilingV1(ceilingCanonical); err != nil ||
		!reflect.DeepEqual(ceiling, restored) {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestActionBindingAndAuthorityV1RejectInvalidAuthority(t *testing.T) {
	baseConfig := ActionBindingConfigV1{
		SchemaVersion: ActionBindingConfigSchemaV1,
		Actions: []ActionBindingMappingV1{{
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text_stats",
			LocalEffectClass: EffectNone,
			MaxResultBytes:   1024,
		}},
		Parameters: json.RawMessage(`{}`),
	}
	duplicatePublic := baseConfig
	duplicatePublic.Actions = append(
		append([]ActionBindingMappingV1(nil), baseConfig.Actions...),
		baseConfig.Actions[0],
	)
	duplicatePublic.Actions[1].ProviderActionID = "builtin.other"
	if _, _, err := NewActionBindingConfigV1(duplicatePublic); err == nil {
		t.Fatal("duplicate public action ID accepted")
	}
	duplicateProvider := baseConfig
	duplicateProvider.Actions = append(
		append([]ActionBindingMappingV1(nil), baseConfig.Actions...),
		baseConfig.Actions[0],
	)
	duplicateProvider.Actions[1].PublicActionID = "text.other"
	if _, _, err := NewActionBindingConfigV1(duplicateProvider); err == nil {
		t.Fatal("duplicate provider action ID accepted within one binding")
	}
	if _, _, err := NewActionBindingConfigV1(ActionBindingConfigV1{
		SchemaVersion: ActionBindingConfigSchemaV1,
	}); err == nil {
		t.Fatal("empty action binding accepted")
	}

	baseCeiling := ActionAuthorityCeilingV1{
		SchemaVersion:            ActionAuthorityCeilingSchemaV1,
		TenantID:                 "tenant-main",
		AllowedWorkspaceIDs:      []string{"workspace-main"},
		AllowedProviderActionIDs: []string{"builtin.text_stats"},
		MaxEffectClass:           EffectNone,
		MaxResultBytes:           1024,
	}
	tests := []struct {
		name   string
		mutate func(*ActionAuthorityCeilingV1)
	}{
		{name: "wildcard tenant", mutate: func(value *ActionAuthorityCeilingV1) { value.TenantID = "*" }},
		{name: "mixed workspace wildcard", mutate: func(value *ActionAuthorityCeilingV1) {
			value.AllowedWorkspaceIDs = []string{"*", "workspace-main"}
		}},
		{name: "empty provider set", mutate: func(value *ActionAuthorityCeilingV1) {
			value.AllowedProviderActionIDs = nil
		}},
		{name: "invalid effect", mutate: func(value *ActionAuthorityCeilingV1) {
			value.MaxEffectClass = "write"
		}},
		{name: "oversized result", mutate: func(value *ActionAuthorityCeilingV1) {
			value.MaxResultBytes = MaxActionResultBytesV1 + 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := baseCeiling
			test.mutate(&value)
			if _, _, err := NewActionAuthorityCeilingV1(value); err == nil {
				t.Fatalf("invalid authority accepted: %+v", value)
			}
		})
	}

	unknown := []byte(`{"actions":[{"local_effect_class":"none","max_result_bytes":1024,"provider_action_id":"builtin.text_stats","public_action_id":"text.stats"}],"extra":true,"parameters":{},"schema_version":"action-binding-config/v1"}`)
	if _, err := RestoreActionBindingConfigV1(unknown); err == nil {
		t.Fatal("unknown binding config field accepted")
	}
}

func TestActionSchemaV1ValidatesBoundedSubsetAndInput(t *testing.T) {
	schema := validActionSchemaV1(t)
	if err := ValidateActionInputSchemaV1(schema); err != nil {
		t.Fatal(err)
	}
	input, err := CanonicalizeAndValidateActionInputV1(
		schema,
		json.RawMessage(`{ "tags": ["go"], "mode": "safe", "count": 2 }`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(input); got != `{"count":2,"mode":"safe","tags":["go"]}` {
		t.Fatalf("canonical input=%s", got)
	}
	if err := ValidateActionInputV1(schema, input); err != nil {
		t.Fatal(err)
	}

	invalidInputs := []json.RawMessage{
		json.RawMessage(`{"mode":"safe"}`),
		json.RawMessage(`{"count":2,"extra":true,"mode":"safe"}`),
		json.RawMessage(`{"count":2.5,"mode":"safe"}`),
		json.RawMessage(`{"count":11,"mode":"safe"}`),
		json.RawMessage(`{"count":2,"mode":"other"}`),
		json.RawMessage(`{"count":2,"mode":"safe","tags":["a","b","c"]}`),
		json.RawMessage(`{"count":2,"mode":"safe","tags":["toolong"]}`),
	}
	for index, invalid := range invalidInputs {
		if err := ValidateActionInputV1(schema, invalid); err == nil {
			t.Fatalf("invalid input %d accepted: %s", index, invalid)
		}
	}
	if err := ValidateActionInputV1(
		schema,
		json.RawMessage(`{"mode":"safe","count":2}`),
	); err == nil {
		t.Fatal("non-canonical input accepted")
	}
}

func TestActionSchemaV1RejectsUnsupportedOrUnboundedShapes(t *testing.T) {
	invalid := []string{
		`{"type":"string","maxLength":4}`,
		`{"additionalProperties":true,"properties":{},"type":"object"}`,
		`{"properties":{},"type":"object"}`,
		`{"additionalProperties":false,"properties":{"x":{"type":"string"}},"type":"object"}`,
		`{"additionalProperties":false,"properties":{"x":{"items":{"type":"boolean"},"type":"array"}},"type":"object"}`,
		`{"additionalProperties":false,"pattern":".*","properties":{},"type":"object"}`,
		`{"$ref":"#","additionalProperties":false,"properties":{},"type":"object"}`,
		`{"additionalProperties":false,"oneOf":[],"properties":{},"type":"object"}`,
		`{"additionalProperties":false,"properties":{"x":{"maxLength":4,"pattern":"x","type":"string"}},"type":"object"}`,
		`{"additionalProperties":false,"properties":{"a":{"maxLength":4,"type":"string"},"b":{"maxLength":4,"type":"string"}},"required":["b","a"],"type":"object"}`,
		`{"additionalProperties":false,"properties":{"x":{"enum":["a","a"],"maxLength":4,"type":"string"}},"type":"object"}`,
	}
	for index, raw := range invalid {
		if _, err := CanonicalizeActionInputSchemaV1(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid schema %d accepted: %s", index, raw)
		}
	}

	properties := make(map[string]any, MaxActionSchemaPropertiesV1+1)
	for index := 0; index <= MaxActionSchemaPropertiesV1; index++ {
		properties[string(rune('a'+index/26))+string(rune('a'+index%26))] = map[string]any{
			"type":      "string",
			"maxLength": 4,
		}
	}
	tooManyProperties, err := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalizeActionInputSchemaV1(tooManyProperties); err == nil {
		t.Fatal("schema with too many properties accepted")
	}

	child := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
	for depth := 0; depth < MaxActionSchemaDepthV1; depth++ {
		child = map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"child": child},
			"additionalProperties": false,
		}
	}
	tooDeep, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalizeActionInputSchemaV1(tooDeep); err == nil {
		t.Fatal("schema exceeding semantic depth accepted")
	}
}

func TestActionDescribeDefinitionAndRequestV1StrictWires(t *testing.T) {
	describe, describeCanonical, err := NewActionDescribeRequestV1(
		ActionDescribeRequestV1{
			SchemaVersion: ActionDescribeRequestSchemaV1,
			Parameters:    json.RawMessage(`{ "locale": "zh" }`),
		},
	)
	if err != nil || string(describe.Parameters) != `{"locale":"zh"}` {
		t.Fatalf("describe=%+v err=%v", describe, err)
	}
	if _, err := RestoreActionDescribeRequestV1(describeCanonical); err != nil {
		t.Fatal(err)
	}

	schema := validActionSchemaV1(t)
	definitionInput := ActionDefinitionV1{
		ProviderActionID:        "builtin.text_stats",
		Description:             "Count deterministic text statistics.",
		InputSchema:             schema,
		RequestedEffectClass:    EffectNone,
		RequestedMaxResultBytes: 2048,
	}
	definition, definitionCanonical, err := NewActionDefinitionV1(definitionInput)
	if err != nil {
		t.Fatal(err)
	}
	definitionInput.InputSchema[0] = '['
	if definition.InputSchema[0] != '{' {
		t.Fatal("action definition aliases caller schema")
	}
	if _, err := RestoreActionDefinitionV1(definitionCanonical); err != nil {
		t.Fatal(err)
	}
	definitions, err := FreezeActionDefinitionsV1([]ActionDefinitionV1{definition})
	if err != nil || len(definitions) != 1 {
		t.Fatalf("definitions=%+v err=%v", definitions, err)
	}
	definitions[0].InputSchema[0] = '['
	if definition.InputSchema[0] != '{' {
		t.Fatal("frozen definition list aliases input")
	}

	digest := strings.Repeat("a", SHA256HexLength)
	request, canonical, err := NewActionRequestV1(ActionRequestV1{
		SchemaVersion:    ActionRequestSchemaV1,
		PublicActionID:   "text.stats",
		ProviderActionID: "builtin.text_stats",
		DefinitionDigest: digest,
		CanonicalInput:   json.RawMessage(`{"count":2,"mode":"safe"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreActionRequestV1(canonical); err != nil {
		t.Fatal(err)
	}
	if request.CanonicalInput[0] != '{' {
		t.Fatalf("unexpected request: %+v", request)
	}
	if _, _, err := NewActionRequestV1(ActionRequestV1{
		SchemaVersion:    ActionRequestSchemaV1,
		PublicActionID:   "text.stats",
		ProviderActionID: "builtin.text_stats",
		DefinitionDigest: digest,
		CanonicalInput:   json.RawMessage(`{ "mode": "safe" }`),
	}); err == nil {
		t.Fatal("non-canonical ActionRequest input accepted")
	}
}

func TestActionExecutionV1RoundTripAndTerminalCombinations(t *testing.T) {
	digest := strings.Repeat("a", SHA256HexLength)
	request, requestCanonical, err := NewActionExecutionRequestV1(
		ActionExecutionRequestV1{
			SchemaVersion:    ActionExecutionRequestSchemaV1,
			AttemptID:        "attempt-1",
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text_stats",
			DefinitionDigest: digest,
			MaxResultBytes:   1024,
			PreparedPayload:  json.RawMessage(`{ "text": "hello", "mode": "words" }`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(request.PreparedPayload); got != `{"mode":"words","text":"hello"}` {
		t.Fatalf("prepared payload=%s", got)
	}
	if _, err := RestoreActionExecutionRequestV1(requestCanonical); err != nil {
		t.Fatal(err)
	}

	results := []ActionExecutionResultV1{
		{
			SchemaVersion:       ActionExecutionResultSchemaV1,
			AttemptID:           "attempt-1",
			Outcome:             ActionExecutionSucceeded,
			CanonicalResult:     json.RawMessage(`{"characters":5}`),
			ProviderReceipt:     json.RawMessage(`{ "receipt": "r1" }`),
			ExternalOperationID: "operation-1",
		},
		{
			SchemaVersion:       ActionExecutionResultSchemaV1,
			AttemptID:           "attempt-1",
			Outcome:             ActionExecutionFailed,
			ErrorClassification: "EXECUTION_REJECTED",
		},
		{
			SchemaVersion: ActionExecutionResultSchemaV1,
			AttemptID:     "attempt-1",
			Outcome:       ActionExecutionUnknown,
			UnknownReason: "Provider terminal state could not be proven.",
		},
	}
	for index, input := range results {
		result, canonical, err := NewActionExecutionResultV1(input)
		if err != nil {
			t.Fatalf("result %d: %v", index, err)
		}
		if _, err := RestoreActionExecutionResultV1(canonical); err != nil {
			t.Fatalf("restore result %d: %v", index, err)
		}
		if err := ValidateActionExecutionResultForRequestV1(request, result); err != nil {
			t.Fatalf("close result %d: %v", index, err)
		}
	}

	invalid := []ActionExecutionResultV1{
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionSucceeded},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionSucceeded, CanonicalResult: json.RawMessage(`{ "ok": true }`)},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionSucceeded, CanonicalResult: json.RawMessage(`{"ok":true}`), ErrorClassification: "ERROR"},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionFailed},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionFailed, CanonicalResult: json.RawMessage(`null`), ErrorClassification: "ERROR"},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionUnknown},
		{SchemaVersion: ActionExecutionResultSchemaV1, AttemptID: "attempt-1", Outcome: ActionExecutionUnknown, ErrorClassification: "ERROR", UnknownReason: "unknown"},
	}
	for index, result := range invalid {
		if _, _, err := NewActionExecutionResultV1(result); err == nil {
			t.Fatalf("invalid terminal combination %d accepted: %+v", index, result)
		}
	}

	wrongAttempt := results[1]
	wrongAttempt.AttemptID = "attempt-2"
	if err := ValidateActionExecutionResultForRequestV1(request, wrongAttempt); err == nil {
		t.Fatal("result for wrong attempt accepted")
	}
	smallRequest := request
	smallRequest.MaxResultBytes = 4
	if err := ValidateActionExecutionResultForRequestV1(smallRequest, results[0]); err == nil {
		t.Fatal("result exceeding effective request limit accepted")
	}

	tooLargeResult := json.RawMessage(`"` + strings.Repeat("x", MaxActionResultBytesV1) + `"`)
	if _, _, err := NewActionExecutionResultV1(ActionExecutionResultV1{
		SchemaVersion:   ActionExecutionResultSchemaV1,
		AttemptID:       "attempt-1",
		Outcome:         ActionExecutionSucceeded,
		CanonicalResult: tooLargeResult,
	}); err == nil {
		t.Fatal("oversized action result accepted")
	}
	tooLargePrepared := json.RawMessage(`{"x":"` +
		strings.Repeat("x", MaxActionPreparedPayloadBytesV1) + `"}`)
	if _, err := CanonicalizeActionPreparedPayloadV1(tooLargePrepared); err == nil {
		t.Fatal("oversized prepared payload accepted")
	}
}

func TestActionEffectOrderV1(t *testing.T) {
	allowed, err := ActionEffectAtMostV1(EffectReadOnly, EffectReversibleWrite)
	if err != nil || !allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	allowed, err = ActionEffectAtMostV1(EffectIrreversibleWrite, EffectReadOnly)
	if err != nil || allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	higher, err := HigherActionEffectV1(EffectNone, EffectReversibleWrite)
	if err != nil || higher != EffectReversibleWrite {
		t.Fatalf("higher=%q err=%v", higher, err)
	}
	if _, err := HigherActionEffectV1(EffectNone, "write"); err == nil {
		t.Fatal("unknown effect accepted")
	}
}

func TestActionExecutionRestoreRejectsUnknownFields(t *testing.T) {
	canonical := []byte(`{"attempt_id":"attempt-1","extra":true,"outcome":"FAILED","error_classification":"ERROR","schema_version":"action-execution-result/v1"}`)
	canonical, err := CanonicalJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreActionExecutionResultV1(canonical); err == nil {
		t.Fatal("unknown action execution result field accepted")
	}
}

func validActionSchemaV1(t *testing.T) json.RawMessage {
	t.Helper()
	schema, err := CanonicalizeActionInputSchemaV1(json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"count": {"type": "integer", "minimum": 0, "maximum": 10},
			"mode": {"type": "string", "minLength": 1, "maxLength": 8, "enum": ["fast", "safe"]},
			"tags": {"type": "array", "minItems": 0, "maxItems": 2, "items": {"type": "string", "maxLength": 4}}
		},
		"required": ["count", "mode"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(schema)
}
