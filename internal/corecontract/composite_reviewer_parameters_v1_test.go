package corecontract

import (
	"encoding/json"
	"testing"
)

func TestTightenReviewerModelParametersV1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		parameters json.RawMessage
		ceiling    uint32
		want       string
		wantError  bool
	}{
		{name: "insert ceiling", parameters: json.RawMessage(`{}`), ceiling: 512, want: `{"max_tokens":512}`},
		{name: "keep tighter", parameters: json.RawMessage(`{"temperature":0,"max_tokens":128}`), ceiling: 512, want: `{"max_tokens":128,"temperature":0}`},
		{name: "tighten larger", parameters: json.RawMessage(`{"max_tokens":2048}`), ceiling: 512, want: `{"max_tokens":512}`},
		{name: "zero max tokens", parameters: json.RawMessage(`{"max_tokens":0}`), ceiling: 512, wantError: true},
		{name: "fraction max tokens", parameters: json.RawMessage(`{"max_tokens":1.5}`), ceiling: 512, wantError: true},
		{name: "string max tokens", parameters: json.RawMessage(`{"max_tokens":"128"}`), ceiling: 512, wantError: true},
		{name: "overflow max tokens", parameters: json.RawMessage(`{"max_tokens":18446744073709551616}`), ceiling: 512, wantError: true},
		{name: "non object", parameters: json.RawMessage(`[]`), ceiling: 512, wantError: true},
		{name: "zero ceiling", parameters: json.RawMessage(`{}`), ceiling: 0, wantError: true},
		{name: "ceiling above protocol maximum", parameters: json.RawMessage(`{}`), ceiling: CompositeReviewerMaxOutputTokensV1 + 1, wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := TightenReviewerModelParametersV1(
				test.parameters,
				test.ceiling,
			)
			if test.wantError {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}
