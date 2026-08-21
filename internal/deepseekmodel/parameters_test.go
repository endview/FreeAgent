package deepseekmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateDeepSeekParametersAcceptsExactAllowlist(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters string
	}{
		{name: "empty", parameters: `{}`},
		{name: "max tokens boundary", parameters: `{"max_tokens":384000}`},
		{name: "sampling", parameters: `{"frequency_penalty":-2,"presence_penalty":2,"temperature":0,"top_p":1}`},
		{name: "stop string", parameters: `{"stop":"END"}`},
		{name: "stop array", parameters: `{"stop":["END","DONE"]}`},
		{name: "text response", parameters: `{"response_format":{"type":"text"}}`},
		{name: "json response", parameters: `{"response_format":{"type":"json_object"}}`},
		{name: "thinking enabled", parameters: `{"thinking":{"type":"enabled"}}`},
		{name: "thinking disabled", parameters: `{"thinking":{"type":"disabled"}}`},
		{name: "reasoning high", parameters: `{"reasoning_effort":"high"}`},
		{name: "reasoning max", parameters: `{"reasoning_effort":"max"}`},
		{name: "stable user", parameters: `{"user_id":"tenant_workspace-01"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateDeepSeekParameters(
				json.RawMessage(test.parameters),
			); err != nil {
				t.Fatalf("validateDeepSeekParameters: %v", err)
			}
		})
	}
}

func TestValidateDeepSeekParametersRejectsExpansionAndMalformedValues(t *testing.T) {
	seventeenStops := make([]string, 17)
	for index := range seventeenStops {
		seventeenStops[index] = fmt.Sprintf("s%d", index)
	}
	encodedStops, err := json.Marshal(map[string]any{"stop": seventeenStops})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		parameters string
	}{
		{name: "zero max tokens", parameters: `{"max_tokens":0}`},
		{name: "above output bound", parameters: `{"max_tokens":384001}`},
		{name: "fractional max tokens", parameters: `{"max_tokens":1.5}`},
		{name: "temperature high", parameters: `{"temperature":2.1}`},
		{name: "top p negative", parameters: `{"top_p":-0.1}`},
		{name: "penalty high", parameters: `{"presence_penalty":3}`},
		{name: "empty stop", parameters: `{"stop":""}`},
		{name: "empty stop array", parameters: `{"stop":[]}`},
		{name: "too many stops", parameters: string(encodedStops)},
		{name: "stop wrong type", parameters: `{"stop":true}`},
		{name: "response format extra", parameters: `{"response_format":{"extra":true,"type":"text"}}`},
		{name: "response format unsupported", parameters: `{"response_format":{"type":"json_schema"}}`},
		{name: "thinking extra", parameters: `{"thinking":{"budget":1,"type":"enabled"}}`},
		{name: "thinking unsupported", parameters: `{"thinking":{"type":"auto"}}`},
		{name: "reasoning effort low", parameters: `{"reasoning_effort":"low"}`},
		{name: "dynamic user", parameters: `{"user_id":"run/123"}`},
		{name: "unknown option", parameters: `{"seed":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateDeepSeekParameters(
				json.RawMessage(test.parameters),
			); err == nil {
				t.Fatal("accepted invalid parameters")
			}
		})
	}
	if _, err := validateDeepSeekParameters(json.RawMessage(
		`{"user_id":"` + strings.Repeat("a", 513) + `"}`,
	)); err == nil {
		t.Fatal("accepted overlong user_id")
	}
}
