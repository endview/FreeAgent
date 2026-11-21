package telegram

import (
	"encoding/json"
	"testing"
)

func TestOneOptionNeverEnablesCapabilitiesOrDropsExclusions(t *testing.T) {
	before := json.RawMessage(`{"extensions":["ext"],"search":"off","extension_variants":{"ext":"a"}}`)
	for _, after := range []string{
		`{"extensions":["ext","other"],"search":"off","extension_variants":{"ext":"b"}}`,
		`{"extensions":["ext"],"extension_variants":{"ext":"b"}}`,
		`{"extensions":["ext"],"search":"off","extension_variants":{"other":"b"}}`,
		`{"extensions":["ext"],"search":"off","extension_variants":{"ext":"b"},"extension_settings":{"ext":{"mode":"new"}}}`,
	} {
		if _, _, _, err := OneOption(before, json.RawMessage(after)); err == nil {
			t.Fatal("accepted expanded delta", after)
		}
	}
	id, key, value, err := OneOption(before, json.RawMessage(`{"extensions":["ext"],"search":"off","extension_variants":{"ext":"b"}}`))
	if err != nil || id != "ext" || key != "" || value != "b" {
		t.Fatal(id, key, value, err)
	}
}

func TestConfirmersOnlyNumericPeople(t *testing.T) {
	if _, err := Confirmers([]string{"username"}); err == nil {
		t.Fatal("username authorized")
	}
	v, err := Confirmers([]string{"7", "telegram:user:7", "9"})
	if err != nil || len(v) != 2 || v[0] != "telegram:user:7" {
		t.Fatal(v, err)
	}
}
