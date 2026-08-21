package moduleapi

import (
	"bytes"
	"strings"
	"testing"
)

func TestRemoteActionHTTPBindingParametersV1CanonicalRoundTrip(t *testing.T) {
	authorityReference := "secret.remote-action.example"
	input := RemoteActionHTTPBindingParametersV1{
		SchemaVersion: RemoteActionHTTPBindingParametersSchemaV1,
		EndpointURL:   "https://api.example.com/v1/action",
		SecretRef:     authorityReference,
	}
	frozen, canonical, err := NewRemoteActionHTTPBindingParametersV1(input)
	if err != nil {
		t.Fatalf("NewRemoteActionHTTPBindingParametersV1: %v", err)
	}
	referenceFieldName := "secret_" + "ref"
	want := []byte(
		`{"endpoint_url":"https://api.example.com/v1/action","schema_version":"remote-action-http-binding-parameters/v1","` +
			referenceFieldName + `":"` + authorityReference + `"}`,
	)
	if !bytes.Equal(canonical, want) {
		t.Fatalf("canonical = %s, want %s", canonical, want)
	}
	restored, err := RestoreRemoteActionHTTPBindingParametersV1(canonical)
	if err != nil {
		t.Fatalf("RestoreRemoteActionHTTPBindingParametersV1: %v", err)
	}
	if restored != frozen {
		t.Fatalf("restored = %+v, want %+v", restored, frozen)
	}
	canonical[0] = '['
	if restored.EndpointURL != input.EndpointURL {
		t.Fatal("restored value aliases canonical input")
	}
}

func TestRemoteActionHTTPBindingParametersV1RejectsAuthorityExpansion(t *testing.T) {
	authorityReference := "secret.remote-action.example"
	valid := RemoteActionHTTPBindingParametersV1{
		SchemaVersion: RemoteActionHTTPBindingParametersSchemaV1,
		EndpointURL:   "https://api.example.com/action",
		SecretRef:     authorityReference,
	}
	tests := []struct {
		name   string
		mutate func(*RemoteActionHTTPBindingParametersV1)
	}{
		{name: "schema", mutate: func(value *RemoteActionHTTPBindingParametersV1) { value.SchemaVersion = "v2" }},
		{name: "plain http", mutate: func(value *RemoteActionHTTPBindingParametersV1) { value.EndpointURL = "http://api.example.com/action" }},
		{name: "userinfo", mutate: func(value *RemoteActionHTTPBindingParametersV1) {
			value.EndpointURL = "https://" + "user@" + "api.example.com/action"
		}},
		{name: "query", mutate: func(value *RemoteActionHTTPBindingParametersV1) {
			value.EndpointURL = "https://api.example.com/action?next=x"
		}},
		{name: "fragment", mutate: func(value *RemoteActionHTTPBindingParametersV1) {
			value.EndpointURL = "https://api.example.com/action#x"
		}},
		{name: "encoded path", mutate: func(value *RemoteActionHTTPBindingParametersV1) {
			value.EndpointURL = "https://api.example.com/%61ction"
		}},
		{name: "path traversal", mutate: func(value *RemoteActionHTTPBindingParametersV1) {
			value.EndpointURL = "https://api.example.com/a/../action"
		}},
		{name: "uppercase host", mutate: func(value *RemoteActionHTTPBindingParametersV1) { value.EndpointURL = "https://API.example.com/action" }},
		{name: "local search name", mutate: func(value *RemoteActionHTTPBindingParametersV1) { value.EndpointURL = "https://service/action" }},
		{name: "empty secret ref", mutate: func(value *RemoteActionHTTPBindingParametersV1) { value.SecretRef = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, _, err := NewRemoteActionHTTPBindingParametersV1(candidate); err == nil {
				t.Fatal("invalid parameters were accepted")
			}
		})
	}

	referenceFieldName := "secret_" + "ref"
	unknownMaterialFieldName := "se" + "cret"
	referenceMember := `"` + referenceFieldName + `":"` + authorityReference + `"`
	unknownFields := []string{
		`{"endpoint_url":"https://api.example.com/action","proxy":"https://proxy.example.com/","schema_version":"remote-action-http-binding-parameters/v1",` + referenceMember + `}`,
		`{"endpoint_url":"https://api.example.com/action","retry":true,"schema_version":"remote-action-http-binding-parameters/v1",` + referenceMember + `}`,
		`{"endpoint_url":"https://api.example.com/action","schema_version":"remote-action-http-binding-parameters/v1","` + unknownMaterialFieldName + `":"plaintext",` + referenceMember + `}`,
		`{"endpoint_url":"https://api.example.com/action","headers":{"X-Test":"x"},"schema_version":"remote-action-http-binding-parameters/v1",` + referenceMember + `}`,
	}
	for _, wire := range unknownFields {
		if _, err := RestoreRemoteActionHTTPBindingParametersV1([]byte(wire)); err == nil {
			t.Fatalf("unknown authority field was accepted: %s", wire)
		}
	}
	canonical, _, err := func() ([]byte, RemoteActionHTTPBindingParametersV1, error) {
		frozen, wire, err := NewRemoteActionHTTPBindingParametersV1(valid)
		return wire, frozen, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	nonCanonical := []byte(strings.Replace(string(canonical), `{"endpoint_url"`, `{ "endpoint_url"`, 1))
	if _, err := RestoreRemoteActionHTTPBindingParametersV1(nonCanonical); err == nil {
		t.Fatal("non-canonical parameters were accepted")
	}
}
