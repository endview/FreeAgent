package zhipumodel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type zhipuMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func buildChatCompletionsRequest(model string, messages []moduleapi.ModelMessageV1, parameters validatedParameters, stream bool) ([]byte, error) {
	wireMessages := make([]zhipuMessage, len(messages))
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
		wireMessages[index] = zhipuMessage{Role: role, Content: message.Content}
	}
	body := make(map[string]json.RawMessage, len(parameters)+3)
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	messagesJSON, err := json.Marshal(wireMessages)
	if err != nil {
		return nil, err
	}
	body["model"] = modelJSON
	body["messages"] = messagesJSON
	if stream {
		body["stream"] = json.RawMessage(`true`)
	} else {
		body["stream"] = json.RawMessage(`false`)
	}
	for key, value := range parameters {
		body[key] = append(json.RawMessage(nil), value...)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return moduleapi.CanonicalJSONWithLimits(encoded, moduleapi.CanonicalJSONLimits{MaxBytes: moduleapi.MaxTextBytes + moduleapi.MaxConfigBytes, MaxDepth: 128, MaxNodes: moduleapi.MaxTextBytes + moduleapi.MaxConfigBytes})
}

type zhipuResponse struct {
	ID      string        `json:"id"`
	Choices []zhipuChoice `json:"choices"`
	Model   string        `json:"model"`
	Usage   *zhipuUsage   `json:"usage"`
}

type zhipuChoice struct {
	Index        int64               `json:"index"`
	Message      zhipuMessageContent `json:"message"`
	Delta        zhipuMessageContent `json:"delta"`
	FinishReason *string             `json:"finish_reason"`
}

type zhipuMessageContent struct {
	Role             string          `json:"role"`
	Content          *string         `json:"content"`
	ReasoningContent json.RawMessage `json:"reasoning_content"`
}

type zhipuUsage struct {
	PromptTokens           *uint64                      `json:"prompt_tokens"`
	CompletionTokens       *uint64                      `json:"completion_tokens"`
	TotalTokens            *uint64                      `json:"total_tokens"`
	PromptTokensDetails    *zhipuPromptTokenDetails     `json:"prompt_tokens_details"`
	CompletionTokenDetails *zhipuCompletionTokenDetails `json:"completion_tokens_details"`
}

type zhipuPromptTokenDetails struct {
	CachedTokens *uint64 `json:"cached_tokens"`
}
type zhipuCompletionTokenDetails struct {
	ReasoningTokens *uint64 `json:"reasoning_tokens"`
}

type safeProviderReceipt struct {
	ProviderRequestID string      `json:"provider_request_id"`
	Model             string      `json:"model"`
	FinishReason      string      `json:"finish_reason"`
	Usage             *zhipuUsage `json:"usage,omitempty"`
}

func normalizeChatCompletionsResponse(header http.Header, body []byte, expectedModel string) ([]byte, []byte, error) {
	if err := validateJSONContentType(header); err != nil {
		return nil, nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(body, moduleapi.CanonicalJSONLimits{MaxBytes: maximumResponseBytes, MaxDepth: 128, MaxNodes: maximumResponseBytes})
	if err != nil {
		return nil, nil, fmt.Errorf("provider response is not bounded JSON")
	}
	var response zhipuResponse
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&response); err != nil {
		return nil, nil, fmt.Errorf("decode provider response")
	}
	if err := validateZhipuResponse(response, expectedModel); err != nil {
		return nil, nil, err
	}
	choice := response.Choices[0]
	output := moduleapi.ModelGenerateOutputV1{SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1, AssistantText: moduleapi.CanonicalText(*choice.Message.Content), ProviderRequestID: response.ID}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(output)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize provider output")
	}
	usageCanonical, err := normalizeUsage(response.ID, response.Model, "stop", response.Usage)
	if err != nil {
		return nil, nil, err
	}
	return outputCanonical, usageCanonical, nil
}

func validateZhipuResponse(response zhipuResponse, expectedModel string) error {
	if err := validateSafeMetadata("response id", response.ID, 256, false); err != nil {
		return err
	}
	if len(response.Choices) != 1 || response.Choices[0].Index != 0 {
		return errors.New("provider response must contain exactly choice index 0")
	}
	choice := response.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content == nil {
		return errors.New("provider response must contain assistant content")
	}
	if *choice.Message.Content == "" {
		return errors.New("provider response assistant content is empty")
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		return errors.New("provider response finish_reason is not supported")
	}
	if err := validateSafeMetadata("response model", response.Model, 256, false); err != nil {
		return err
	}
	if response.Model != expectedModel {
		return errors.New("provider response model differs from the frozen request")
	}
	return validateZhipuUsage(response.Usage)
}

func validateZhipuUsage(usage *zhipuUsage) error {
	if usage == nil {
		return nil
	}
	if usage.PromptTokens != nil && usage.PromptTokensDetails != nil && usage.PromptTokensDetails.CachedTokens != nil && *usage.PromptTokensDetails.CachedTokens > *usage.PromptTokens {
		return errors.New("provider cached usage exceeds input usage")
	}
	if usage.CompletionTokenDetails != nil && usage.CompletionTokenDetails.ReasoningTokens != nil && usage.CompletionTokens != nil && *usage.CompletionTokenDetails.ReasoningTokens > *usage.CompletionTokens {
		return errors.New("provider reasoning usage exceeds completion usage")
	}
	if usage.TotalTokens != nil && usage.PromptTokens != nil && usage.CompletionTokens != nil && *usage.TotalTokens != *usage.PromptTokens+*usage.CompletionTokens {
		return errors.New("provider total usage is inconsistent")
	}
	return nil
}

func normalizeUsage(id, model, finish string, usage *zhipuUsage) ([]byte, error) {
	var tokens corecontract.UsageTokens
	if usage != nil {
		tokens.Input = cloneUint64(usage.PromptTokens)
		tokens.Output = cloneUint64(usage.CompletionTokens)
		if usage.PromptTokensDetails != nil {
			tokens.CachedInput = cloneUint64(usage.PromptTokensDetails.CachedTokens)
		}
		if usage.CompletionTokenDetails != nil {
			tokens.Reasoning = cloneUint64(usage.CompletionTokenDetails.ReasoningTokens)
		}
	}
	if err := tokens.Validate(); err != nil {
		return nil, fmt.Errorf("normalize provider usage: %w", err)
	}
	raw, err := json.Marshal(safeProviderReceipt{ProviderRequestID: id, Model: model, FinishReason: finish, Usage: cloneUsage(usage)})
	if err != nil {
		return nil, fmt.Errorf("construct safe provider receipt")
	}
	input, cached, uncached, output, reasoning := tokens.Input, tokens.CachedInput, tokens.UncachedInput, tokens.Output, tokens.Reasoning
	_, canonical, err := moduleapi.NewModelUsageReceiptV2(moduleapi.ModelUsageReceiptV2{SchemaVersion: moduleapi.ModelUsageReceiptSchemaV2, InputTokens: input, CachedInputTokens: cached, UncachedInputTokens: uncached, OutputTokens: output, ReasoningTokens: reasoning, NormalizationNote: "Zhipu usage mapped exactly where reported; reasoning content excluded from the receipt.", RawReceipt: raw})
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func validateJSONContentType(header http.Header) error {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return errors.New("provider response Content-Type is missing or repeated")
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return errors.New("provider response Content-Type is not application/json")
	}
	return nil
}

func readResponseBody(response *http.Response) ([]byte, modulehost.InvocationOutcome, modulehost.InvocationUnknownClass) {
	if response == nil || response.Body == nil {
		return nil, modulehost.InvocationUnknown, modulehost.UnknownClassNoUsableResponse
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return nil, modulehost.InvocationUnknown, modulehost.UnknownClassResponseBodyReadIncomplete
	}
	if len(body) > maximumResponseBytes {
		return nil, modulehost.InvocationFailed, ""
	}
	return body, modulehost.InvocationSucceeded, ""
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func validateSafeMetadata(name, value string, maximum int, allowEmpty bool) error {
	if !utf8.ValidString(value) || len(value) > maximum || (!allowEmpty && value == "") || strings.TrimSpace(value) != value {
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

func cloneUint64(input *uint64) *uint64 {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}
func cloneUsage(input *zhipuUsage) *zhipuUsage {
	if input == nil {
		return nil
	}
	cloned := *input
	cloned.PromptTokens = cloneUint64(input.PromptTokens)
	cloned.CompletionTokens = cloneUint64(input.CompletionTokens)
	if input.PromptTokensDetails != nil {
		cloned.PromptTokensDetails = &zhipuPromptTokenDetails{CachedTokens: cloneUint64(input.PromptTokensDetails.CachedTokens)}
	}
	if input.CompletionTokenDetails != nil {
		cloned.CompletionTokenDetails = &zhipuCompletionTokenDetails{ReasoningTokens: cloneUint64(input.CompletionTokenDetails.ReasoningTokens)}
	}
	return &cloned
}

var errStreamEOF = errors.New("zhipumodel: stream ended without terminal event")
var errStreamRead = errors.New("zhipumodel: stream body read incomplete")

type streamParseResult struct {
	Result            corecontract.ModelStreamResultV1
	ProviderRequestID string
	Usage             *zhipuUsage
}

// parseChatCompletionsStream maps provider SSE frames into the shared bounded
// stream state machine. It deliberately ignores reasoning_content and never
// promotes a delta-only stream to a successful result.
func parseChatCompletionsStream(header http.Header, body io.Reader, expectedModel string, maximumTextBytes int) (streamParseResult, error) {
	if err := validateSSEContentType(header); err != nil {
		return streamParseResult{}, err
	}
	accumulator, err := corecontract.NewModelStreamAccumulatorV1(maximumTextBytes)
	if err != nil {
		return streamParseResult{}, err
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maximumResponseBytes)
	dataLines := make([]string, 0, 2)
	providerID := ""
	var usage *zhipuUsage
	process := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if payload == "[DONE]" {
			return nil
		}
		var chunk zhipuResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode provider SSE frame")
		}
		if chunk.ID != "" {
			if err := validateSafeMetadata("stream response id", chunk.ID, 256, false); err != nil {
				return err
			}
			if providerID != "" && providerID != chunk.ID {
				return errors.New("provider stream response id changed")
			}
			providerID = chunk.ID
		}
		if chunk.Model != "" {
			if err := validateSafeMetadata("stream response model", chunk.Model, 256, false); err != nil {
				return err
			}
			if chunk.Model != expectedModel {
				return errors.New("provider stream response model differs from the frozen request")
			}
		}
		if err := validateZhipuUsage(chunk.Usage); err != nil {
			return err
		}
		if chunk.Usage != nil {
			if usage != nil {
				return errors.New("provider stream usage was reported more than once")
			}
			usage = cloneUsage(chunk.Usage)
		}
		if len(chunk.Choices) > 1 {
			return errors.New("provider stream must contain at most one choice")
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		if choice.Index != 0 {
			return errors.New("provider stream choice index is not zero")
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			if err := accumulator.Accept(corecontract.ModelStreamEventV1{SchemaVersion: corecontract.ModelStreamEventSchemaVersionV1, Kind: corecontract.ModelStreamDeltaV1, Delta: *choice.Delta.Content}); err != nil {
				return err
			}
		}
		if choice.FinishReason != nil {
			finish, err := mapFinishReason(*choice.FinishReason)
			if err != nil {
				return err
			}
			event := corecontract.ModelStreamEventV1{SchemaVersion: corecontract.ModelStreamEventSchemaVersionV1, Kind: corecontract.ModelStreamCompletedV1, FinishReason: finish, Delivery: corecontract.ModelStreamResponseSeenV1}
			if chunk.Usage != nil {
				event.Usage = &corecontract.UsageTokens{}
				event.Usage.Input = cloneUint64(chunk.Usage.PromptTokens)
				event.Usage.Output = cloneUint64(chunk.Usage.CompletionTokens)
				if chunk.Usage.PromptTokensDetails != nil {
					event.Usage.CachedInput = cloneUint64(chunk.Usage.PromptTokensDetails.CachedTokens)
				}
				if chunk.Usage.CompletionTokenDetails != nil {
					event.Usage.Reasoning = cloneUint64(chunk.Usage.CompletionTokenDetails.ReasoningTokens)
				}
			}
			if err := accumulator.Accept(event); err != nil {
				return err
			}
		} else if chunk.Usage != nil {
			event := corecontract.ModelStreamEventV1{SchemaVersion: corecontract.ModelStreamEventSchemaVersionV1, Kind: corecontract.ModelStreamUsageV1, Usage: &corecontract.UsageTokens{Input: cloneUint64(chunk.Usage.PromptTokens), Output: cloneUint64(chunk.Usage.CompletionTokens)}}
			if chunk.Usage.PromptTokensDetails != nil {
				event.Usage.CachedInput = cloneUint64(chunk.Usage.PromptTokensDetails.CachedTokens)
			}
			if chunk.Usage.CompletionTokenDetails != nil {
				event.Usage.Reasoning = cloneUint64(chunk.Usage.CompletionTokenDetails.ReasoningTokens)
			}
			if event.Usage.Input != nil || event.Usage.CachedInput != nil || event.Usage.Output != nil || event.Usage.Reasoning != nil {
				if err := accumulator.Accept(event); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return streamParseResult{}, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataLines = append(dataLines, data)
			if len(dataLines[len(dataLines)-1]) > maximumResponseBytes {
				return streamParseResult{}, errors.New("provider SSE data frame exceeds response bound")
			}
			continue
		}
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			continue
		}
		return streamParseResult{}, errors.New("provider SSE contains an unsupported field")
	}
	if err := process(); err != nil {
		return streamParseResult{}, err
	}
	if err := scanner.Err(); err != nil {
		return streamParseResult{}, errStreamRead
	}
	result := accumulator.Result()
	if result.Terminal == "" || result.Terminal == corecontract.ModelStreamTerminalUnknownV1 {
		return streamParseResult{Result: accumulator.EndOfInput(), ProviderRequestID: providerID, Usage: usage}, errStreamEOF
	}
	return streamParseResult{Result: result, ProviderRequestID: providerID, Usage: usage}, nil
}

func validateSSEContentType(header http.Header) error {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return errors.New("provider stream Content-Type is missing or repeated")
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "text/event-stream" {
		return errors.New("provider stream Content-Type is not text/event-stream")
	}
	return nil
}

func mapFinishReason(reason string) (corecontract.ModelStreamFinishReasonV1, error) {
	switch reason {
	case "stop":
		return corecontract.ModelStreamFinishStopV1, nil
	case "length":
		return corecontract.ModelStreamFinishLengthV1, nil
	case "tool_calls", "tool_call":
		return corecontract.ModelStreamFinishToolV1, nil
	default:
		return "", fmt.Errorf("provider stream finish_reason %q is unsupported", reason)
	}
}
