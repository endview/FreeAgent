package zhipumodel

import (
	"encoding/json"
	"testing"
)

func TestValidateZhipuParametersAcceptsFrozenSubset(t *testing.T) {
	for _, input := range []string{
		`{}`, `{"max_tokens":32768}`, `{"temperature":0,"top_p":1}`,
		`{"stop":["END"]}`, `{"response_format":{"type":"json_object"}}`,
		`{"thinking":{"type":"disabled"}}`,
	} {
		if _, err := validateZhipuParameters(json.RawMessage(input)); err != nil {
			t.Fatalf("input %s: %v", input, err)
		}
	}
}

func TestValidateZhipuParametersRejectsExpansion(t *testing.T) {
	for _, input := range []string{
		`{"max_tokens":0}`, `{"max_tokens":32769}`, `{"temperature":1.1}`,
		`{"thinking":{"type":"enabled","budget":1}}`, `{"seed":1}`,
	} {
		if _, err := validateZhipuParameters(json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
