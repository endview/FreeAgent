package deepseekmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type validatedParameters map[string]json.RawMessage

func validateDeepSeekParameters(
	canonical json.RawMessage,
) (validatedParameters, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("parameters must be a JSON object")
	}
	validated := make(validatedParameters, len(object))
	for key, raw := range object {
		var err error
		switch key {
		case "max_tokens":
			_, err = positiveIntegerParameter(key, raw)
		case "temperature":
			err = boundedNumberParameter(key, raw, 0, 2)
		case "top_p":
			err = boundedNumberParameter(key, raw, 0, 1)
		case "frequency_penalty", "presence_penalty":
			err = boundedNumberParameter(key, raw, -2, 2)
		case "stop":
			err = validateStopParameter(raw)
		case "response_format":
			err = validateResponseFormatParameter(raw)
		case "thinking":
			err = validateThinkingParameter(raw)
		case "reasoning_effort":
			err = validateStringEnum(
				key,
				raw,
				map[string]struct{}{"high": {}, "max": {}},
			)
		case "user_id":
			err = validateUserIDParameter(raw)
		default:
			return nil, fmt.Errorf("unsupported parameter %q", key)
		}
		if err != nil {
			return nil, err
		}
		validated[key] = append(json.RawMessage(nil), raw...)
	}
	return validated, nil
}

// validateParameterTightening proves that the request still comes from the
// frozen Binding parameters. The sole permitted mutation is Core's Reviewer
// max_tokens ceiling: it may insert max_tokens or lower an existing value.
func validateParameterTightening(
	config validatedParameters,
	request validatedParameters,
) error {
	for key, configValue := range config {
		requestValue, present := request[key]
		if !present {
			return fmt.Errorf("frozen parameter %q is missing", key)
		}
		if key == "max_tokens" {
			configMaximum, err := positiveIntegerParameter(key, configValue)
			if err != nil {
				return err
			}
			requestMaximum, err := positiveIntegerParameter(key, requestValue)
			if err != nil {
				return err
			}
			if requestMaximum > configMaximum {
				return fmt.Errorf("max_tokens exceeds the frozen value")
			}
			continue
		}
		if !bytes.Equal(configValue, requestValue) {
			return fmt.Errorf("frozen parameter %q changed", key)
		}
	}
	for key := range request {
		if _, present := config[key]; present {
			continue
		}
		if key != "max_tokens" {
			return fmt.Errorf("request added parameter %q", key)
		}
	}
	return nil
}

func positiveIntegerParameter(
	name string,
	raw json.RawMessage,
) (uint64, error) {
	text := string(raw)
	if text == "" || strings.ContainsAny(text, ".eE+-") {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	value, err := strconv.ParseUint(text, 10, 63)
	if err != nil || value == 0 || value > MaximumOutputTokensV1 {
		return 0, fmt.Errorf(
			"%s must be a positive integer no greater than %d",
			name,
			MaximumOutputTokensV1,
		)
	}
	return value, nil
}

func boundedNumberParameter(
	name string,
	raw json.RawMessage,
	minimum float64,
	maximum float64,
) error {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) ||
		value < minimum || value > maximum {
		return fmt.Errorf("%s must be a number between %g and %g", name, minimum, maximum)
	}
	return nil
}

func validateStopParameter(raw json.RawMessage) error {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return validateParameterText("stop", single, 256)
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil ||
		len(multiple) == 0 || len(multiple) > 16 {
		return fmt.Errorf("stop must be a string or 1..16 strings")
	}
	for _, value := range multiple {
		if err := validateParameterText("stop", value, 256); err != nil {
			return err
		}
	}
	return nil
}

func validateResponseFormatParameter(raw json.RawMessage) error {
	var value struct {
		Type string `json:"type"`
	}
	if err := decodeExactParameterObject(raw, &value); err != nil {
		return fmt.Errorf("response_format must contain only type")
	}
	switch value.Type {
	case "text", "json_object":
		return nil
	default:
		return fmt.Errorf("response_format type is unsupported")
	}
}

func validateThinkingParameter(raw json.RawMessage) error {
	var value struct {
		Type string `json:"type"`
	}
	if err := decodeExactParameterObject(raw, &value); err != nil {
		return fmt.Errorf("thinking must contain only type")
	}
	switch value.Type {
	case "enabled", "disabled":
		return nil
	default:
		return fmt.Errorf("thinking type is unsupported")
	}
}

func validateStringEnum(
	name string,
	raw json.RawMessage,
	allowed map[string]struct{},
) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a string", name)
	}
	if _, present := allowed[value]; !present {
		return fmt.Errorf("%s is unsupported", name)
	}
	return nil
}

func validateUserIDParameter(raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil ||
		value == "" || len(value) > 512 {
		return fmt.Errorf("user_id must be a non-empty string of at most 512 bytes")
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' {
			continue
		}
		return fmt.Errorf("user_id contains an unsupported character")
	}
	return nil
}

func decodeExactParameterObject(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func validateParameterText(name, value string, maximum int) error {
	if value == "" || !utf8.ValidString(value) || len(value) > maximum {
		return fmt.Errorf("%s must be non-empty UTF-8 of at most %d bytes", name, maximum)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}
