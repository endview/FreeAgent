package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const (
	// ModelBindingConfigSchemaV2 is the exact provider-neutral configuration
	// schema referenced by a model.generate/v2 PortBinding.ConfigRef.
	ModelBindingConfigSchemaV2 = "model-binding-config/v2"

	maxModelBindingConfigDepth = 128
)

// ModelBindingConfigV2 binds one model.generate/v2 Binding to its provider,
// exact model build and canonical generation parameters. Dynamic credentials
// and endpoints are
// injected separately and are deliberately absent from this contract.
type ModelBindingConfigV2 struct {
	SchemaVersion string          `json:"schema_version"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	ModelBuildID  string          `json:"model_build_id"`
	Parameters    json.RawMessage `json:"parameters"`
}

// NewModelBindingConfigV2 validates, canonicalizes, and defensively copies one
// provider-neutral model Binding configuration. The complete canonical record,
// including its parameters object, is bounded by MaxConfigBytes.
func NewModelBindingConfigV2(
	input ModelBindingConfigV2,
) (ModelBindingConfigV2, []byte, error) {
	if input.SchemaVersion != ModelBindingConfigSchemaV2 {
		return ModelBindingConfigV2{}, nil, fmt.Errorf(
			"model binding config schema_version must be %q",
			ModelBindingConfigSchemaV2,
		)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "provider", value: input.Provider},
		{name: "model", value: input.Model},
		{name: "model_build_id", value: input.ModelBuildID},
	} {
		if err := validateOpaqueID(
			"model binding config "+field.name,
			field.value,
		); err != nil {
			return ModelBindingConfigV2{}, nil, err
		}
	}
	parameters, err := canonicalModelBindingParameters(input.Parameters)
	if err != nil {
		return ModelBindingConfigV2{}, nil, err
	}
	frozen := input
	frozen.Parameters = bytes.Clone(parameters)
	canonical, err := marshalCanonicalModelBindingConfig(frozen)
	if err != nil {
		return ModelBindingConfigV2{}, nil, err
	}
	return frozen, bytes.Clone(canonical), nil
}

// RestoreModelBindingConfigV2 accepts only the exact canonical representation
// emitted by NewModelBindingConfigV2 and rejects unknown fields.
func RestoreModelBindingConfigV2(
	canonical []byte,
) (ModelBindingConfigV2, error) {
	if len(canonical) == 0 || len(canonical) > MaxConfigBytes {
		return ModelBindingConfigV2{}, fmt.Errorf(
			"model binding config must contain between 1 and %d canonical bytes",
			MaxConfigBytes,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: maxModelBindingConfigDepth,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return ModelBindingConfigV2{}, fmt.Errorf(
			"model binding config is not canonical JSON",
		)
	}
	var decoded ModelBindingConfigV2
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return ModelBindingConfigV2{}, fmt.Errorf(
			"decode model binding config: %w",
			err,
		)
	}
	restored, rebuilt, err := NewModelBindingConfigV2(decoded)
	if err != nil {
		return ModelBindingConfigV2{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ModelBindingConfigV2{}, fmt.Errorf(
			"model binding config is not frozen canonically",
		)
	}
	restored.Parameters = bytes.Clone(restored.Parameters)
	return restored, nil
}

func canonicalModelBindingParameters(
	parameters json.RawMessage,
) (json.RawMessage, error) {
	if len(parameters) > MaxConfigBytes {
		return nil, fmt.Errorf(
			"model binding config parameters exceed %d bytes",
			MaxConfigBytes,
		)
	}
	if len(bytes.TrimSpace(parameters)) == 0 {
		parameters = json.RawMessage(`{}`)
	}
	canonical, err := CanonicalJSONWithLimits(
		parameters,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: maxModelBindingConfigDepth,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"model binding config parameters: %w",
			err,
		)
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return nil, fmt.Errorf(
			"model binding config parameters must be a JSON object",
		)
	}
	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		return nil, fmt.Errorf(
			"decode model binding config parameters: %w",
			err,
		)
	}
	if err := rejectDynamicModelBindingValues(object); err != nil {
		return nil, err
	}
	return bytes.Clone(canonical), nil
}

func rejectDynamicModelBindingValues(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if forbiddenDynamicModelBindingKey(key) {
				return fmt.Errorf(
					"model binding config parameters must not contain dynamic credential or endpoint key %q",
					key,
				)
			}
			if err := rejectDynamicModelBindingValues(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := rejectDynamicModelBindingValues(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func forbiddenDynamicModelBindingKey(key string) bool {
	normalized := normalizeModelBindingParameterKey(key)
	for _, fragment := range []string{
		"apikey",
		"endpoint",
		"baseurl",
		"apibase",
		"apiurl",
		"serverurl",
		"providerurl",
		"apiuri",
		"serveruri",
		"provideruri",
		"apihost",
		"serverhost",
		"providerhost",
		"authorization",
		"credential",
		"password",
		"client" + "secret",
		"accesstoken",
		"bearertoken",
		"authtoken",
		"refreshtoken",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	if normalized == "sec"+"ret" || normalized == "to"+"ken" {
		return true
	}

	segments := modelBindingParameterKeySegments(key)
	for _, part := range segments {
		switch part {
		case "endpoint",
			"authorization",
			"credential",
			"credentials",
			"password",
			"sec" + "ret":
			return true
		}
	}
	for index := 1; index < len(segments); index++ {
		if segments[index-1] == "client" && segments[index] == "secret" {
			return true
		}
		switch segments[index-1] + segments[index] {
		case "apikey",
			"apibase",
			"baseurl",
			"apiurl",
			"serverurl",
			"providerurl",
			"apiuri",
			"serveruri",
			"provideruri",
			"apihost",
			"serverhost",
			"providerhost",
			"accesstoken",
			"bearertoken",
			"authtoken",
			"refreshtoken":
			return true
		}
	}
	return false
}

func normalizeModelBindingParameterKey(key string) string {
	var normalized strings.Builder
	for _, character := range key {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(unicode.ToLower(character))
		}
	}
	return normalized.String()
}

func modelBindingParameterKeySegments(key string) []string {
	runes := []rune(key)
	segments := make([]string, 0, 4)
	var segment strings.Builder
	flush := func() {
		if segment.Len() == 0 {
			return
		}
		segments = append(segments, segment.String())
		segment.Reset()
	}
	for index, character := range runes {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			flush()
			continue
		}
		if segment.Len() != 0 && unicode.IsUpper(character) {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) &&
				unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
				unicode.IsUpper(previous) && nextIsLower {
				flush()
			}
		}
		segment.WriteRune(unicode.ToLower(character))
	}
	flush()
	return segments
}

func marshalCanonicalModelBindingConfig(
	config ModelBindingConfigV2,
) ([]byte, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal model binding config: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: maxModelBindingConfigDepth,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("canonicalize model binding config: %w", err)
	}
	if len(canonical) > MaxConfigBytes {
		return nil, fmt.Errorf(
			"canonical model binding config exceeds %d bytes",
			MaxConfigBytes,
		)
	}
	return bytes.Clone(canonical), nil
}
