package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

const (
	ModelGenerateRequestSchemaV1 = "model-generate-request/v1"
	ModelGenerateOutputSchemaV1  = "model-generate-output/v1"
	ModelUsageReceiptSchemaV2    = "model-usage-receipt/v2"
)

// ModelMessageRole is the closed role set for model.generate/v2.
type ModelMessageRole string

const (
	ModelRoleSystem    ModelMessageRole = "SYSTEM"
	ModelRoleUser      ModelMessageRole = "USER"
	ModelRoleAssistant ModelMessageRole = "ASSISTANT"
)

func (role ModelMessageRole) Validate() error {
	switch role {
	case ModelRoleSystem, ModelRoleUser, ModelRoleAssistant:
		return nil
	default:
		return fmt.Errorf("unsupported model message role %q", role)
	}
}

// ModelMessageV1 is deliberately free of Run, Attempt, timestamp and routing
// metadata. Stable system/context messages can therefore remain a byte-stable
// prompt prefix while task and History are appended in semantic order.
type ModelMessageV1 struct {
	Role    ModelMessageRole `json:"role"`
	Content string           `json:"content"`
}

// ModelActionDefinitionV1 is the deliberately reduced Action view exposed to
// a model. Provider identity, effect, authority, Binding and result limits
// remain in Core's frozen member snapshot.
type ModelActionDefinitionV1 struct {
	ActionID    string          `json:"action_id"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ModelActionRequestV1 is model output only. Core resolves ActionID against
// one frozen definition and validates CanonicalInput against that exact schema
// before Prepare.
type ModelActionRequestV1 struct {
	ActionID       string          `json:"action_id"`
	CanonicalInput json.RawMessage `json:"canonical_input"`
}

// ModelGenerateRequestV1 is the exact model.generate/v2 port input.
// Provider/model selection and authority stay in the frozen PortBinding.
type ModelGenerateRequestV1 struct {
	SchemaVersion string                    `json:"schema_version"`
	Messages      []ModelMessageV1          `json:"messages"`
	Parameters    json.RawMessage           `json:"parameters"`
	Actions       []ModelActionDefinitionV1 `json:"actions,omitempty"`
}

func NewModelGenerateRequestV1(
	input ModelGenerateRequestV1,
) (ModelGenerateRequestV1, []byte, error) {
	if input.SchemaVersion != ModelGenerateRequestSchemaV1 {
		return ModelGenerateRequestV1{}, nil, fmt.Errorf(
			"model request schema_version must be %q",
			ModelGenerateRequestSchemaV1,
		)
	}
	if len(input.Messages) == 0 ||
		len(input.Messages) > MaxManifestEntries {
		return ModelGenerateRequestV1{}, nil, fmt.Errorf(
			"model request must contain between 1 and %d messages",
			MaxManifestEntries,
		)
	}
	messages := make([]ModelMessageV1, len(input.Messages))
	for index, message := range input.Messages {
		if err := message.Role.Validate(); err != nil {
			return ModelGenerateRequestV1{}, nil, fmt.Errorf(
				"model request message %d: %w",
				index,
				err,
			)
		}
		if err := validateBoundedText(
			"model message content",
			message.Content,
			MaxTextBytes,
			false,
		); err != nil {
			return ModelGenerateRequestV1{}, nil, fmt.Errorf(
				"model request message %d: %w",
				index,
				err,
			)
		}
		if message.Content != CanonicalText(message.Content) {
			return ModelGenerateRequestV1{}, nil, fmt.Errorf(
				"model request message %d content must use Unicode NFC",
				index,
			)
		}
		messages[index] = message
	}
	parameters, err := canonicalConfig(input.Parameters)
	if err != nil {
		return ModelGenerateRequestV1{}, nil, fmt.Errorf(
			"model request parameters: %w",
			err,
		)
	}
	actions, err := freezeModelActionDefinitionsV1(input.Actions)
	if err != nil {
		return ModelGenerateRequestV1{}, nil, err
	}
	frozen := input
	frozen.Messages = messages
	frozen.Parameters = parameters
	frozen.Actions = actions
	canonical, err := marshalCanonicalModelWire(frozen)
	if err != nil {
		return ModelGenerateRequestV1{}, nil, err
	}
	if len(canonical) > MaxTextBytes {
		return ModelGenerateRequestV1{}, nil, fmt.Errorf(
			"model request exceeds %d canonical bytes",
			MaxTextBytes,
		)
	}
	return frozen, canonical, nil
}

func RestoreModelGenerateRequestV1(
	canonical []byte,
) (ModelGenerateRequestV1, error) {
	var decoded ModelGenerateRequestV1
	if err := decodeExactModelWire(canonical, &decoded); err != nil {
		return ModelGenerateRequestV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewModelGenerateRequestV1(decoded)
	if err != nil {
		return ModelGenerateRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return ModelGenerateRequestV1{}, fmt.Errorf(
			"model request is not frozen canonically",
		)
	}
	return rebuilt, nil
}

// ModelGenerateOutputV1 is the normalized successful model.generate/v2
// output. Provider raw data belongs in ModelUsageReceiptV2, not prompt text.
type ModelGenerateOutputV1 struct {
	SchemaVersion     string                `json:"schema_version"`
	AssistantText     string                `json:"assistant_text"`
	ActionRequest     *ModelActionRequestV1 `json:"action_request,omitempty"`
	ProviderRequestID string                `json:"provider_request_id,omitempty"`
}

func NewModelGenerateOutputV1(
	input ModelGenerateOutputV1,
) (ModelGenerateOutputV1, []byte, error) {
	if input.SchemaVersion != ModelGenerateOutputSchemaV1 {
		return ModelGenerateOutputV1{}, nil, fmt.Errorf(
			"model output schema_version must be %q",
			ModelGenerateOutputSchemaV1,
		)
	}
	frozen := input
	if input.ActionRequest == nil {
		if err := validateBoundedText(
			"model assistant text",
			input.AssistantText,
			MaxTextBytes,
			false,
		); err != nil {
			return ModelGenerateOutputV1{}, nil, err
		}
		if input.AssistantText != CanonicalText(input.AssistantText) {
			return ModelGenerateOutputV1{}, nil, fmt.Errorf(
				"model assistant text must use Unicode NFC",
			)
		}
	} else {
		if input.AssistantText != "" {
			return ModelGenerateOutputV1{}, nil, fmt.Errorf(
				"model output must contain exactly one of assistant_text or action_request",
			)
		}
		if !validDottedIdentifier(
			input.ActionRequest.ActionID,
			MaxIdentifierBytes,
		) {
			return ModelGenerateOutputV1{}, nil, fmt.Errorf(
				"model action_request action_id must be a dotted identifier",
			)
		}
		canonicalInput, err := validateCanonicalActionObjectV1(
			"model action_request canonical_input",
			input.ActionRequest.CanonicalInput,
			MaxTextBytes,
			128,
		)
		if err != nil {
			return ModelGenerateOutputV1{}, nil, err
		}
		frozen.ActionRequest = &ModelActionRequestV1{
			ActionID:       input.ActionRequest.ActionID,
			CanonicalInput: bytes.Clone(canonicalInput),
		}
	}
	if input.ProviderRequestID != "" {
		if err := validateOpaqueID(
			"model provider_request_id",
			input.ProviderRequestID,
		); err != nil {
			return ModelGenerateOutputV1{}, nil, err
		}
	}
	canonical, err := marshalCanonicalModelWire(frozen)
	if err != nil {
		return ModelGenerateOutputV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreModelGenerateOutputV1(
	canonical []byte,
) (ModelGenerateOutputV1, error) {
	var decoded ModelGenerateOutputV1
	if err := decodeExactModelWire(canonical, &decoded); err != nil {
		return ModelGenerateOutputV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewModelGenerateOutputV1(decoded)
	if err != nil {
		return ModelGenerateOutputV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return ModelGenerateOutputV1{}, fmt.Errorf(
			"model output is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func freezeModelActionDefinitionsV1(
	input []ModelActionDefinitionV1,
) ([]ModelActionDefinitionV1, error) {
	if len(input) == 0 {
		// Preserve the historical no-Action wire exactly: a caller-supplied
		// empty slice is normalized to nil so json omits the new field.
		return nil, nil
	}
	if len(input) > MaxActionsPerMemberV1 {
		return nil, fmt.Errorf(
			"model request may contain at most %d actions",
			MaxActionsPerMemberV1,
		)
	}
	frozen := make([]ModelActionDefinitionV1, len(input))
	aggregateBytes := 0
	previousActionID := ""
	for index, definition := range input {
		if !validDottedIdentifier(definition.ActionID, MaxIdentifierBytes) {
			return nil, fmt.Errorf(
				"model request action %d action_id must be a dotted identifier",
				index,
			)
		}
		if index > 0 && definition.ActionID <= previousActionID {
			return nil, fmt.Errorf(
				"model request actions must be strictly sorted and unique by action_id",
			)
		}
		if err := validateActionSchemaTextV1(
			"model action description",
			definition.Description,
			MaxActionDescriptionBytesV1,
			false,
		); err != nil {
			return nil, fmt.Errorf("model request action %d: %w", index, err)
		}
		if err := ValidateActionInputSchemaV1(definition.InputSchema); err != nil {
			return nil, fmt.Errorf(
				"model request action %d input_schema: %w",
				index,
				err,
			)
		}
		aggregateBytes += len(definition.Description) + len(definition.InputSchema)
		if aggregateBytes > MaxActionDefinitionAggregateBytesV1 {
			return nil, fmt.Errorf(
				"model request action descriptions and schemas exceed %d aggregate bytes",
				MaxActionDefinitionAggregateBytesV1,
			)
		}
		frozen[index] = definition
		frozen[index].InputSchema = bytes.Clone(definition.InputSchema)
		previousActionID = definition.ActionID
	}
	return frozen, nil
}

// ModelUsageReceiptV2 carries normalized token semantics plus the exact raw
// provider receipt. nil means unknown; a pointer to zero means reported zero.
type ModelUsageReceiptV2 struct {
	SchemaVersion       string          `json:"schema_version"`
	InputTokens         *uint64         `json:"input_tokens"`
	CachedInputTokens   *uint64         `json:"cached_input_tokens"`
	UncachedInputTokens *uint64         `json:"uncached_input_tokens"`
	OutputTokens        *uint64         `json:"output_tokens"`
	ReasoningTokens     *uint64         `json:"reasoning_tokens"`
	NormalizationNote   string          `json:"normalization_note,omitempty"`
	RawReceipt          json.RawMessage `json:"raw_receipt"`
}

func NewModelUsageReceiptV2(
	input ModelUsageReceiptV2,
) (ModelUsageReceiptV2, []byte, error) {
	if input.SchemaVersion != ModelUsageReceiptSchemaV2 {
		return ModelUsageReceiptV2{}, nil, fmt.Errorf(
			"model usage receipt schema_version must be %q",
			ModelUsageReceiptSchemaV2,
		)
	}
	for name, value := range map[string]*uint64{
		"input_tokens":          input.InputTokens,
		"cached_input_tokens":   input.CachedInputTokens,
		"uncached_input_tokens": input.UncachedInputTokens,
		"output_tokens":         input.OutputTokens,
		"reasoning_tokens":      input.ReasoningTokens,
	} {
		if value != nil && *value > math.MaxInt64 {
			return ModelUsageReceiptV2{}, nil, fmt.Errorf(
				"%s exceeds Current Store integer range",
				name,
			)
		}
	}
	if input.InputTokens != nil &&
		input.CachedInputTokens != nil &&
		input.UncachedInputTokens != nil &&
		*input.InputTokens !=
			*input.CachedInputTokens+*input.UncachedInputTokens {
		return ModelUsageReceiptV2{}, nil, fmt.Errorf(
			"input_tokens must equal cached plus uncached input",
		)
	}
	if input.NormalizationNote != "" {
		if err := validateBoundedText(
			"normalization_note",
			input.NormalizationNote,
			MaxTextBytes,
			true,
		); err != nil {
			return ModelUsageReceiptV2{}, nil, err
		}
	}
	raw := input.RawReceipt
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	rawCanonical, err := CanonicalJSONWithLimits(
		raw,
		CanonicalJSONLimits{
			MaxBytes: MaxTextBytes,
			MaxDepth: 128,
			MaxNodes: MaxTextBytes,
		},
	)
	if err != nil {
		return ModelUsageReceiptV2{}, nil, fmt.Errorf(
			"model raw usage receipt: %w",
			err,
		)
	}
	frozen := input
	frozen.RawReceipt = bytes.Clone(rawCanonical)
	frozen.InputTokens = cloneModelUint64(input.InputTokens)
	frozen.CachedInputTokens = cloneModelUint64(input.CachedInputTokens)
	frozen.UncachedInputTokens = cloneModelUint64(input.UncachedInputTokens)
	frozen.OutputTokens = cloneModelUint64(input.OutputTokens)
	frozen.ReasoningTokens = cloneModelUint64(input.ReasoningTokens)
	canonical, err := marshalCanonicalModelWire(frozen)
	if err != nil {
		return ModelUsageReceiptV2{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreModelUsageReceiptV2(
	canonical []byte,
) (ModelUsageReceiptV2, error) {
	var decoded ModelUsageReceiptV2
	if err := decodeExactModelWire(canonical, &decoded); err != nil {
		return ModelUsageReceiptV2{}, err
	}
	rebuilt, rebuiltCanonical, err := NewModelUsageReceiptV2(decoded)
	if err != nil {
		return ModelUsageReceiptV2{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return ModelUsageReceiptV2{}, fmt.Errorf(
			"model usage receipt is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func marshalCanonicalModelWire(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal model.generate/v2 wire value: %w", err)
	}
	canonical, err := CanonicalJSON(encoded)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func decodeExactModelWire(canonical []byte, target any) error {
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: MaxTextBytes,
			MaxDepth: 128,
			MaxNodes: MaxTextBytes,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("model.generate/v2 wire value is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode model.generate/v2 wire value: %w", err)
	}
	return nil
}

func cloneModelUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
