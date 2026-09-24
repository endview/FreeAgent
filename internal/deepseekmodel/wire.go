package deepseekmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func buildChatCompletionsRequest(
	model string,
	messages []moduleapi.ModelMessageV1,
	parameters validatedParameters,
) ([]byte, error) {
	wireMessages := make([]deepSeekMessage, len(messages))
	for index, message := range messages {
		role := ""
		switch message.Role {
		case moduleapi.ModelRoleSystem:
			role = "system"
		case moduleapi.ModelRoleUser:
			role = "user"
		case moduleapi.ModelRoleAssistant:
			role = "assistant"
		default:
			return nil, fmt.Errorf("unsupported message role %q", message.Role)
		}
		wireMessages[index] = deepSeekMessage{
			Role:    role,
			Content: message.Content,
		}
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	messagesJSON, err := json.Marshal(wireMessages)
	if err != nil {
		return nil, err
	}
	body := make(map[string]json.RawMessage, len(parameters)+3)
	body["model"] = modelJSON
	body["messages"] = messagesJSON
	body["stream"] = json.RawMessage(`false`)
	for key, value := range parameters {
		body[key] = append(json.RawMessage(nil), value...)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxTextBytes + moduleapi.MaxConfigBytes,
			MaxDepth: 128,
			MaxNodes: moduleapi.MaxTextBytes + moduleapi.MaxConfigBytes,
		},
	)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

type deepSeekResponse struct {
	ID                string           `json:"id"`
	Choices           []deepSeekChoice `json:"choices"`
	Created           *int64           `json:"created"`
	Model             string           `json:"model"`
	Object            string           `json:"object"`
	SystemFingerprint *string          `json:"system_fingerprint"`
	Usage             *deepSeekUsage   `json:"usage"`
}

type deepSeekChoice struct {
	FinishReason string                `json:"finish_reason"`
	Index        int64                 `json:"index"`
	Message      deepSeekChoiceMessage `json:"message"`
}

type deepSeekChoiceMessage struct {
	Role             string          `json:"role"`
	Content          *string         `json:"content"`
	ReasoningContent json.RawMessage `json:"reasoning_content"`
}

type deepSeekUsage struct {
	PromptTokens           *uint64                         `json:"prompt_tokens"`
	PromptCacheHitTokens   *uint64                         `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens  *uint64                         `json:"prompt_cache_miss_tokens"`
	CompletionTokens       *uint64                         `json:"completion_tokens"`
	CompletionTokenDetails *deepSeekCompletionTokenDetails `json:"completion_tokens_details"`
	TotalTokens            *uint64                         `json:"total_tokens"`
}

type deepSeekCompletionTokenDetails struct {
	ReasoningTokens *uint64 `json:"reasoning_tokens"`
}

type safeProviderReceipt struct {
	ProviderRequestID string        `json:"provider_request_id"`
	Model             string        `json:"model,omitempty"`
	SystemFingerprint *string       `json:"system_fingerprint,omitempty"`
	FinishReason      string        `json:"finish_reason"`
	Usage             deepSeekUsage `json:"usage"`
}

func normalizeChatCompletionsResponse(
	header http.Header,
	body []byte,
	expectedModel string,
) ([]byte, []byte, error) {
	if err := validateJSONContentType(header); err != nil {
		return nil, nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		body,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumResponseBytes,
			MaxDepth: 128,
			MaxNodes: maximumResponseBytes,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("provider response is not bounded JSON")
	}
	var response deepSeekResponse
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&response); err != nil {
		return nil, nil, fmt.Errorf("decode provider response")
	}
	if err := validateDeepSeekResponse(response, expectedModel); err != nil {
		return nil, nil, err
	}
	choice := response.Choices[0]
	output := moduleapi.ModelGenerateOutputV1{
		SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
		AssistantText:     moduleapi.CanonicalText(*choice.Message.Content),
		ProviderRequestID: response.ID,
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(output)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize provider output")
	}
	reasoningTokens := (*uint64)(nil)
	if response.Usage.CompletionTokenDetails != nil {
		reasoningTokens = cloneUint64(
			response.Usage.CompletionTokenDetails.ReasoningTokens,
		)
	}
	rawReceipt, err := json.Marshal(safeProviderReceipt{
		ProviderRequestID: response.ID,
		Model:             response.Model,
		SystemFingerprint: cloneString(response.SystemFingerprint),
		FinishReason:      choice.FinishReason,
		Usage:             cloneUsage(*response.Usage),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("construct safe provider receipt")
	}
	_, usageCanonical, err := moduleapi.NewModelUsageReceiptV2(
		moduleapi.ModelUsageReceiptV2{
			SchemaVersion:       moduleapi.ModelUsageReceiptSchemaV2,
			InputTokens:         cloneUint64(response.Usage.PromptTokens),
			CachedInputTokens:   cloneUint64(response.Usage.PromptCacheHitTokens),
			UncachedInputTokens: cloneUint64(response.Usage.PromptCacheMissTokens),
			OutputTokens:        cloneUint64(response.Usage.CompletionTokens),
			ReasoningTokens:     reasoningTokens,
			NormalizationNote:   "DeepSeek usage mapped exactly; response text and private chain-of-thought excluded from raw receipt.",
			RawReceipt:          rawReceipt,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize provider usage")
	}
	return outputCanonical, usageCanonical, nil
}

func validateDeepSeekResponse(
	response deepSeekResponse,
	expectedModel string,
) error {
	if err := validateSafeMetadata("response id", response.ID, 256, false); err != nil {
		return err
	}
	if len(response.Choices) != 1 || response.Choices[0].Index != 0 {
		return fmt.Errorf("provider response must contain exactly choice index 0")
	}
	choice := response.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content == nil {
		return fmt.Errorf("provider response must contain assistant content")
	}
	if choice.FinishReason == "tool_calls" {
		return fmt.Errorf("provider tool calls are unsupported")
	}
	if err := validateSafeMetadata(
		"finish_reason",
		choice.FinishReason,
		64,
		false,
	); err != nil {
		return err
	}
	if err := validateSafeMetadata(
		"response model",
		response.Model,
		256,
		false,
	); err != nil {
		return err
	}
	if response.Model != expectedModel {
		return fmt.Errorf("provider response model differs from the frozen request")
	}
	if response.SystemFingerprint != nil {
		if err := validateSafeMetadata(
			"system_fingerprint",
			*response.SystemFingerprint,
			256,
			false,
		); err != nil {
			return err
		}
	}
	if response.Usage == nil ||
		response.Usage.PromptTokens == nil ||
		response.Usage.PromptCacheHitTokens == nil ||
		response.Usage.PromptCacheMissTokens == nil ||
		response.Usage.CompletionTokens == nil ||
		response.Usage.TotalTokens == nil {
		return fmt.Errorf("provider response is missing required usage fields")
	}
	if *response.Usage.PromptTokens !=
		*response.Usage.PromptCacheHitTokens+
			*response.Usage.PromptCacheMissTokens {
		return fmt.Errorf("provider input usage is inconsistent")
	}
	if *response.Usage.TotalTokens !=
		*response.Usage.PromptTokens+*response.Usage.CompletionTokens {
		return fmt.Errorf("provider total usage is inconsistent")
	}
	if response.Usage.CompletionTokenDetails != nil &&
		response.Usage.CompletionTokenDetails.ReasoningTokens != nil &&
		*response.Usage.CompletionTokenDetails.ReasoningTokens >
			*response.Usage.CompletionTokens {
		return fmt.Errorf("provider reasoning usage exceeds completion usage")
	}
	return nil
}

func validateJSONContentType(header http.Header) error {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return fmt.Errorf("provider response Content-Type is missing or repeated")
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("provider response Content-Type is not application/json")
	}
	return nil
}

func readResponseBody(
	response *http.Response,
) ([]byte, modulehost.InvocationOutcome, modulehost.InvocationUnknownClass) {
	if response == nil || response.Body == nil {
		return nil,
			modulehost.InvocationUnknown,
			modulehost.UnknownClassNoUsableResponse
	}
	defer response.Body.Close()
	body, err := io.ReadAll(
		io.LimitReader(response.Body, maximumResponseBytes+1),
	)
	if err != nil {
		return nil,
			modulehost.InvocationUnknown,
			modulehost.UnknownClassResponseBodyReadIncomplete
	}
	if len(body) > maximumResponseBytes {
		return nil, modulehost.InvocationFailed, ""
	}
	return body, modulehost.InvocationSucceeded, ""
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
}

func validateSafeMetadata(
	name string,
	value string,
	maximum int,
	allowEmpty bool,
) error {
	if !utf8.ValidString(value) || len(value) > maximum ||
		!allowEmpty && value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is not bounded canonical text", name)
	}
	if value != moduleapi.CanonicalText(value) {
		return fmt.Errorf("%s does not use Unicode NFC", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func cloneUsage(input deepSeekUsage) deepSeekUsage {
	cloned := input
	cloned.PromptTokens = cloneUint64(input.PromptTokens)
	cloned.PromptCacheHitTokens = cloneUint64(input.PromptCacheHitTokens)
	cloned.PromptCacheMissTokens = cloneUint64(input.PromptCacheMissTokens)
	cloned.CompletionTokens = cloneUint64(input.CompletionTokens)
	cloned.TotalTokens = cloneUint64(input.TotalTokens)
	if input.CompletionTokenDetails != nil {
		cloned.CompletionTokenDetails = &deepSeekCompletionTokenDetails{
			ReasoningTokens: cloneUint64(
				input.CompletionTokenDetails.ReasoningTokens,
			),
		}
	}
	return cloned
}

func cloneUint64(input *uint64) *uint64 {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func cloneString(input *string) *string {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}
