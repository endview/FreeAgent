package moduleapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelBindingConfigV2CanonicalRoundTripAndDefensiveCopy(
	t *testing.T,
) {
	sourceParameters := json.RawMessage(
		`{ "top_p": 1, "nested": { "enabled": true }, "temperature": 0 }`,
	)
	config, canonical, err := NewModelBindingConfigV2(
		ModelBindingConfigV2{
			SchemaVersion: ModelBindingConfigSchemaV2,
			Provider:      "provider-local",
			Model:         "model-v1",
			ModelBuildID:  "model-v1-build-2026-08-03",
			Parameters:    sourceParameters,
		},
	)
	if err != nil {
		t.Fatalf("NewModelBindingConfigV2: %v", err)
	}
	const wantParameters = `{"nested":{"enabled":true},"temperature":0,"top_p":1}`
	if string(config.Parameters) != wantParameters {
		t.Fatalf("parameters=%s want %s", config.Parameters, wantParameters)
	}
	sourceParameters[0] ^= 0xff
	config.Parameters[0] ^= 0xff

	restored, err := RestoreModelBindingConfigV2(canonical)
	if err != nil {
		t.Fatalf("RestoreModelBindingConfigV2: %v", err)
	}
	if restored.SchemaVersion != ModelBindingConfigSchemaV2 ||
		restored.Provider != "provider-local" ||
		restored.Model != "model-v1" ||
		restored.ModelBuildID != "model-v1-build-2026-08-03" ||
		string(restored.Parameters) != wantParameters {
		t.Fatalf("restored=%+v", restored)
	}
	wantCanonical := bytes.Clone(canonical)
	canonical[0] ^= 0xff
	if string(restored.Parameters) != wantParameters {
		t.Fatal("restored parameters alias canonical input")
	}
	restored.Parameters[0] ^= 0xff

	_, rebuilt, err := NewModelBindingConfigV2(
		ModelBindingConfigV2{
			SchemaVersion: ModelBindingConfigSchemaV2,
			Provider:      "provider-local",
			Model:         "model-v1",
			ModelBuildID:  "model-v1-build-2026-08-03",
			Parameters:    json.RawMessage(wantParameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rebuilt, wantCanonical) {
		t.Fatalf("rebuilt=%s want %s", rebuilt, wantCanonical)
	}
}

func TestModelBindingConfigV2DefaultsEmptyParametersToObject(t *testing.T) {
	config, canonical, err := NewModelBindingConfigV2(
		validModelBindingConfigV2(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(config.Parameters) != `{}` {
		t.Fatalf("parameters=%s want {}", config.Parameters)
	}
	restored, err := RestoreModelBindingConfigV2(canonical)
	if err != nil || string(restored.Parameters) != `{}` {
		t.Fatalf("restored=%+v error=%v", restored, err)
	}
}

func TestModelBindingConfigV2RejectsInvalidOpaqueFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ModelBindingConfigV2)
	}{
		{
			name: "wrong schema",
			mutate: func(value *ModelBindingConfigV2) {
				value.SchemaVersion = "model-binding-config/v1"
			},
		},
		{
			name: "empty provider",
			mutate: func(value *ModelBindingConfigV2) {
				value.Provider = ""
			},
		},
		{
			name: "untrimmed model",
			mutate: func(value *ModelBindingConfigV2) {
				value.Model = " model-v1"
			},
		},
		{
			name: "empty model build ID",
			mutate: func(value *ModelBindingConfigV2) {
				value.ModelBuildID = ""
			},
		},
		{
			name: "untrimmed model build ID",
			mutate: func(value *ModelBindingConfigV2) {
				value.ModelBuildID = " model-v1-build"
			},
		},
		{
			name: "overlong provider",
			mutate: func(value *ModelBindingConfigV2) {
				value.Provider = strings.Repeat("p", MaxOpaqueIDBytes+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validModelBindingConfigV2()
			test.mutate(&input)
			if _, _, err := NewModelBindingConfigV2(input); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestModelBindingConfigV2RequiresBoundedParametersObject(t *testing.T) {
	forbiddenParameter := func(path ...string) json.RawMessage {
		var value any = "forbidden"
		for index := len(path) - 1; index >= 0; index-- {
			value = map[string]any{path[index]: value}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode forbidden parameter: %v", err)
		}
		return encoded
	}
	tests := []struct {
		name       string
		parameters json.RawMessage
	}{
		{name: "array", parameters: json.RawMessage(`[]`)},
		{name: "null", parameters: json.RawMessage(`null`)},
		{name: "string", parameters: json.RawMessage(`"temperature"`)},
		{name: "number", parameters: json.RawMessage(`1`)},
		{name: "malformed", parameters: json.RawMessage(`{"temperature":`)},
		{
			name:       "oversize whitespace",
			parameters: json.RawMessage(strings.Repeat(" ", MaxConfigBytes+1)),
		},
		{
			name: "canonical record becomes oversize",
			parameters: json.RawMessage(
				`{"value":"` +
					strings.Repeat("x", MaxConfigBytes-32) +
					`"}`,
			),
		},
		{
			name: "excessive depth",
			parameters: json.RawMessage(
				strings.Repeat(`{"value":`, maxModelBindingConfigDepth+1) +
					`0` +
					strings.Repeat(`}`, maxModelBindingConfigDepth+1),
			),
		},
		{
			name:       "API key",
			parameters: forbiddenParameter("api_" + "key"),
		},
		{
			name:       "nested x API key",
			parameters: forbiddenParameter("transport", "headers", "x-api-key"),
		},
		{
			name:       "nested provider API key",
			parameters: forbiddenParameter("provider", "openai_api_key"),
		},
		{
			name:       "nested API base",
			parameters: forbiddenParameter("transport", "api_base"),
		},
		{
			name:       "nested API URL",
			parameters: forbiddenParameter("transport", "api_url"),
		},
		{
			name:       "nested server URL",
			parameters: forbiddenParameter("transport", "server_url"),
		},
		{
			name:       "nested provider URL",
			parameters: forbiddenParameter("transport", "provider_url"),
		},
		{
			name:       "nested provider host",
			parameters: forbiddenParameter("transport", "provider_host"),
		},
		{
			name:       "nested authorization header",
			parameters: forbiddenParameter("transport", "authorization_header"),
		},
		{
			name:       "camel case provider API key",
			parameters: forbiddenParameter("provider", "openaiApiKey"),
		},
		{
			name: "nested endpoint",
			parameters: json.RawMessage(
				`{"transport":{"endpoint":"https://forbidden.invalid"}}`,
			),
		},
		{
			name:       "dynamic token",
			parameters: forbiddenParameter("auth", "access"+"Token"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validModelBindingConfigV2()
			input.Parameters = test.parameters
			if _, _, err := NewModelBindingConfigV2(input); err == nil {
				t.Fatal("invalid parameters accepted")
			}
		})
	}
}

func TestModelBindingConfigV2AllowsOrdinaryTokenAndBusinessKeys(t *testing.T) {
	input := validModelBindingConfigV2()
	input.Parameters = json.RawMessage(
		`{"api_latency_ms":12,"image_url":"https://content.invalid/image.png","key_rotation_count":3,"max_tokens":512,"secretary_id":"employee-1","token_budget":1024}`,
	)
	frozen, canonical, err := NewModelBindingConfigV2(input)
	if err != nil {
		t.Fatalf("ordinary generation parameters were rejected: %v", err)
	}
	if len(frozen.Parameters) == 0 || len(canonical) == 0 {
		t.Fatal("ordinary generation parameters were not frozen")
	}
	if _, err := RestoreModelBindingConfigV2(canonical); err != nil {
		t.Fatalf("restore ordinary generation parameters: %v", err)
	}
}

func TestRestoreModelBindingConfigV2RejectsNonExactWire(t *testing.T) {
	_, canonical, err := NewModelBindingConfigV2(
		validModelBindingConfigV2(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(canonical, &document); err != nil {
		t.Fatal(err)
	}
	var forbiddenFields [][]byte
	for _, field := range []string{
		"api_key",
		"endpoint",
		"billing_version",
		"price_snapshot_id",
	} {
		withForbidden := make(map[string]any, len(document)+1)
		for key, value := range document {
			withForbidden[key] = value
		}
		withForbidden[field] = "must-use-controlled-injection"
		encoded, err := json.Marshal(withForbidden)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err = CanonicalJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		forbiddenFields = append(forbiddenFields, encoded)
	}
	tests := [][]byte{
		append(bytes.Clone(canonical), ' '),
		[]byte(`{}`),
		bytes.Repeat([]byte{' '}, MaxConfigBytes+1),
	}
	tests = append(tests, forbiddenFields...)
	for _, wire := range tests {
		if _, err := RestoreModelBindingConfigV2(wire); err == nil {
			t.Fatalf("non-exact wire accepted: %.256q", wire)
		}
	}
}

func validModelBindingConfigV2() ModelBindingConfigV2 {
	return ModelBindingConfigV2{
		SchemaVersion: ModelBindingConfigSchemaV2,
		Provider:      "provider-local",
		Model:         "model-v1",
		ModelBuildID:  "model-v1-build-2026-08-03",
	}
}
