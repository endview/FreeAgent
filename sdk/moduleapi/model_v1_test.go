package moduleapi

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestModelGenerateRequestV1StablePrefixHasNoDynamicIdentity(t *testing.T) {
	request, canonical, err := NewModelGenerateRequestV1(
		ModelGenerateRequestV1{
			SchemaVersion: ModelGenerateRequestSchemaV1,
			Messages: []ModelMessageV1{
				{Role: ModelRoleSystem, Content: "Core constraints."},
				{Role: ModelRoleSystem, Content: "Stable role context."},
				{Role: ModelRoleUser, Content: "Implement the parser."},
			},
			Parameters: json.RawMessage(`{ "temperature": 0 }`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte("run_id")) ||
		bytes.Contains(canonical, []byte("attempt_id")) ||
		string(request.Parameters) != `{"temperature":0}` {
		t.Fatalf("request=%s", canonical)
	}
	restored, err := RestoreModelGenerateRequestV1(canonical)
	if err != nil || len(restored.Messages) != 3 {
		t.Fatalf("restored=%+v error=%v", restored, err)
	}
}

func TestModelGenerateV1NoActionCanonicalBytesRemainCompatible(t *testing.T) {
	_, requestCanonical, err := NewModelGenerateRequestV1(
		ModelGenerateRequestV1{
			SchemaVersion: ModelGenerateRequestSchemaV1,
			Messages: []ModelMessageV1{{
				Role:    ModelRoleUser,
				Content: "hello",
			}},
			Parameters: json.RawMessage(`{}`),
			Actions:    []ModelActionDefinitionV1{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRequest := `{"messages":[{"content":"hello","role":"USER"}],"parameters":{},"schema_version":"model-generate-request/v1"}`
	if string(requestCanonical) != wantRequest {
		t.Fatalf("request bytes changed:\n got %s\nwant %s", requestCanonical, wantRequest)
	}
	if bytes.Contains(requestCanonical, []byte(`"actions"`)) {
		t.Fatal("empty actions field was not omitted")
	}

	_, outputCanonical, err := NewModelGenerateOutputV1(
		ModelGenerateOutputV1{
			SchemaVersion:     ModelGenerateOutputSchemaV1,
			AssistantText:     "Done.",
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := `{"assistant_text":"Done.","provider_request_id":"provider-request-1","schema_version":"model-generate-output/v1"}`
	if string(outputCanonical) != wantOutput {
		t.Fatalf("output bytes changed:\n got %s\nwant %s", outputCanonical, wantOutput)
	}
}

func TestModelGenerateV1ActionDefinitionsAndExactlyOneOutput(t *testing.T) {
	schema := validActionSchemaV1(t)
	requestInput := ModelGenerateRequestV1{
		SchemaVersion: ModelGenerateRequestSchemaV1,
		Messages:      []ModelMessageV1{{Role: ModelRoleUser, Content: "count it"}},
		Parameters:    json.RawMessage(`{}`),
		Actions: []ModelActionDefinitionV1{{
			ActionID:    "text.stats",
			Description: "Count text statistics.",
			InputSchema: schema,
		}},
	}
	request, canonical, err := NewModelGenerateRequestV1(requestInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"actions"`)) || len(request.Actions) != 1 {
		t.Fatalf("request=%s", canonical)
	}
	requestInput.Actions[0].InputSchema[0] = '['
	if request.Actions[0].InputSchema[0] != '{' {
		t.Fatal("model action definition aliases caller schema")
	}
	if _, err := RestoreModelGenerateRequestV1(canonical); err != nil {
		t.Fatal(err)
	}

	actionInput := json.RawMessage(`{"count":2,"mode":"safe"}`)
	output, outputCanonical, err := NewModelGenerateOutputV1(
		ModelGenerateOutputV1{
			SchemaVersion: ModelGenerateOutputSchemaV1,
			ActionRequest: &ModelActionRequestV1{
				ActionID:       "text.stats",
				CanonicalInput: actionInput,
			},
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output.AssistantText != "" || output.ActionRequest == nil ||
		!bytes.Contains(outputCanonical, []byte(`"assistant_text":""`)) {
		t.Fatalf("output=%+v canonical=%s", output, outputCanonical)
	}
	actionInput[0] = '['
	if output.ActionRequest.CanonicalInput[0] != '{' {
		t.Fatal("model action request aliases caller input")
	}
	if _, err := RestoreModelGenerateOutputV1(outputCanonical); err != nil {
		t.Fatal(err)
	}

	if _, _, err := NewModelGenerateOutputV1(ModelGenerateOutputV1{
		SchemaVersion: ModelGenerateOutputSchemaV1,
		AssistantText: "also answer",
		ActionRequest: &ModelActionRequestV1{
			ActionID:       "text.stats",
			CanonicalInput: json.RawMessage(`{}`),
		},
	}); err == nil {
		t.Fatal("model output containing text and action accepted")
	}
	if _, _, err := NewModelGenerateOutputV1(ModelGenerateOutputV1{
		SchemaVersion: ModelGenerateOutputSchemaV1,
	}); err == nil {
		t.Fatal("model output containing neither text nor action accepted")
	}
	if _, _, err := NewModelGenerateOutputV1(ModelGenerateOutputV1{
		SchemaVersion: ModelGenerateOutputSchemaV1,
		ActionRequest: &ModelActionRequestV1{
			ActionID:       "text.stats",
			CanonicalInput: json.RawMessage(`{ "count": 2 }`),
		},
	}); err == nil {
		t.Fatal("non-canonical model action input accepted")
	}
}

func TestModelGenerateV1ActionsMustBeSortedAndUnique(t *testing.T) {
	schema := validActionSchemaV1(t)
	_, _, err := NewModelGenerateRequestV1(ModelGenerateRequestV1{
		SchemaVersion: ModelGenerateRequestSchemaV1,
		Messages:      []ModelMessageV1{{Role: ModelRoleUser, Content: "hello"}},
		Actions: []ModelActionDefinitionV1{
			{ActionID: "z.action", Description: "z", InputSchema: schema},
			{ActionID: "a.action", Description: "a", InputSchema: schema},
		},
	})
	if err == nil {
		t.Fatal("unsorted model actions accepted")
	}
}

func TestModelGenerateOutputV1RoundTrip(t *testing.T) {
	_, canonical, err := NewModelGenerateOutputV1(
		ModelGenerateOutputV1{
			SchemaVersion:     ModelGenerateOutputSchemaV1,
			AssistantText:     "Done.",
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreModelGenerateOutputV1(canonical); err != nil {
		t.Fatal(err)
	}
}

func TestModelUsageReceiptV2PreservesUnknownAndReportedZero(t *testing.T) {
	zero := uint64(0)
	receipt, canonical, err := NewModelUsageReceiptV2(
		ModelUsageReceiptV2{
			SchemaVersion: ModelUsageReceiptSchemaV2,
			InputTokens:   nil,
			OutputTokens:  &zero,
			RawReceipt:    json.RawMessage(`{"output_tokens":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.InputTokens != nil ||
		receipt.OutputTokens == nil ||
		*receipt.OutputTokens != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
	restored, err := RestoreModelUsageReceiptV2(canonical)
	if err != nil || restored.InputTokens != nil {
		t.Fatalf("restored=%+v error=%v", restored, err)
	}
	if restored.OutputTokens == nil || *restored.OutputTokens != 0 {
		t.Fatalf("restored=%+v", restored)
	}
}

func TestRestoreModelUsageReceiptV2RejectsRetiredCostField(t *testing.T) {
	legacy := []byte(
		`{"cached_input_tokens":null,"input_tokens":null,"normalization_note":"","output_tokens":null,"provider_reported_cost":"0","raw_receipt":null,"reasoning_tokens":null,"schema_version":"model-usage-receipt/v2","uncached_input_tokens":null}`,
	)
	if _, err := RestoreModelUsageReceiptV2(legacy); err == nil {
		t.Fatal("retired provider_reported_cost field accepted")
	}
}

func TestModelGenerateV1RejectsInvalidShapes(t *testing.T) {
	if _, _, err := NewModelGenerateRequestV1(
		ModelGenerateRequestV1{
			SchemaVersion: ModelGenerateRequestSchemaV1,
			Messages:      nil,
		},
	); err == nil {
		t.Fatal("empty request accepted")
	}
	if _, _, err := NewModelGenerateRequestV1(
		ModelGenerateRequestV1{
			SchemaVersion: ModelGenerateRequestSchemaV1,
			Messages: []ModelMessageV1{
				{Role: "TOOL", Content: "x"},
			},
		},
	); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, _, err := NewModelGenerateOutputV1(
		ModelGenerateOutputV1{
			SchemaVersion: ModelGenerateOutputSchemaV1,
			AssistantText: "",
		},
	); err == nil {
		t.Fatal("empty output accepted")
	}
	one := uint64(1)
	two := uint64(2)
	if _, _, err := NewModelUsageReceiptV2(
		ModelUsageReceiptV2{
			SchemaVersion:       ModelUsageReceiptSchemaV2,
			InputTokens:         &one,
			CachedInputTokens:   &one,
			UncachedInputTokens: &two,
		},
	); err == nil {
		t.Fatal("inconsistent usage accepted")
	}
}

func TestModelGenerateV1RestoreRejectsUnknownFields(t *testing.T) {
	canonical := []byte(
		`{"messages":[{"content":"hello","role":"USER"}],"parameters":{},"schema_version":"model-generate-request/v1","run_id":"forbidden"}`,
	)
	if _, err := RestoreModelGenerateRequestV1(canonical); err == nil {
		t.Fatal("dynamic unknown field accepted")
	}
}
