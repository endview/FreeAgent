package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maxModelResponseBytes = 8 << 20

// OpenAICompatibleClient holds one resolved key for the lifetime of one
// dispatch; it prints as [redacted] so the key cannot leak through fmt.
type OpenAICompatibleClient struct {
	baseURL string
	model   string
	key     ResolvedKey
	client  *http.Client
	caps    ModelCapabilities
}

func (OpenAICompatibleClient) String() string   { return redactedText }
func (OpenAICompatibleClient) GoString() string { return redactedText }
func (OpenAICompatibleClient) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, redactedText)
}

// NewOpenAICompatibleClient sends requests to <key.BaseURL>/chat/completions.
// The key's base URL must already be normalized. A key without a secret sends
// no Authorization header.
func NewOpenAICompatibleClient(config ModelConfig, key ResolvedKey, httpClient *http.Client) (*OpenAICompatibleClient, error) {
	if config.Provider != "" && config.Provider != "openai-compatible" {
		return nil, fmt.Errorf("harness: unsupported model provider %q", config.Provider)
	}
	if strings.TrimSpace(config.Model) == "" || !validModelName(config.Model) {
		return nil, errors.New("harness: model name is required")
	}
	if err := config.Capabilities.validate(); err != nil {
		return nil, err
	}
	if base, err := NormalizeBaseURL(key.BaseURL); err != nil || base != key.BaseURL {
		return nil, errors.New("harness: model base URL is required")
	}
	caps := config.EffectiveCapabilities()
	if caps.Protocol != "chat" {
		return nil, fmt.Errorf("%w: only Chat Completions is implemented", ErrModelCapability)
	}
	client := http.Client{Timeout: 2 * time.Minute}
	if httpClient != nil {
		client = *httpClient
		if client.Timeout == 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	// Never forward the credential to a redirect target, even on the same host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAICompatibleClient{baseURL: key.BaseURL, model: config.Model, key: key, client: &client, caps: caps}, nil
}

// BaseURL lets dispatch confirm that the client still targets the address
// recorded in the run's model binding.
func (client *OpenAICompatibleClient) BaseURL() string { return client.baseURL }

func (client *OpenAICompatibleClient) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	config := ModelConfig{Provider: "openai-compatible", Capabilities: client.caps}
	if err := validateModelRequest(config, request); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "capability", Message: "model cannot accept the complete message/tool group"}
	}
	wire := openAIRequest{Model: client.model, Stream: client.caps.Streaming != "none"}
	if client.caps.TokenLimit == "max_completion_tokens" {
		wire.MaxCompletionTokens = request.MaxOutputTokens
	} else {
		wire.MaxTokens = request.MaxOutputTokens
	}
	if wire.Stream && client.caps.Usage != "none" {
		wire.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}
	if len(request.Tools) != 0 {
		parallel := client.caps.ParallelTools != "none"
		wire.ParallelToolCalls = &parallel
	}
	for _, message := range request.Messages {
		wire.Messages = append(wire.Messages, openAIMessage{
			Role: message.Role, Content: message.Content, Name: message.Name,
			ToolCallID: message.ToolCallID, ToolCalls: toOpenAIToolCalls(message.ToolCalls),
		})
	}
	for _, tool := range request.Tools {
		wire.Tools = append(wire.Tools, openAITool{Type: "function", Function: openAIFunction{
			Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema,
		}})
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "cannot encode model request"}
	}
	key := client.key.secret
	if err := ctx.Err(); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "cancelled", Message: "cancelled before model dispatch"}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "invalid model base URL"}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream, application/json")
	if key != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.client.Do(httpRequest)
	if err != nil {
		// Transport errors can contain endpoint or credential material.
		category := "transport"
		if ctx.Err() != nil {
			category = "cancelled"
		} else if failure, ok := err.(interface{ Timeout() bool }); ok && failure.Timeout() {
			category = "timeout"
		}
		return ModelResponse{}, &CallError{Category: category, Message: "model transport interrupted; response unknown"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		var decoded openAIEnvelope
		if len(raw) <= 64<<10 && json.Unmarshal(raw, &decoded) == nil {
			return ModelResponse{}, normalizeProviderError(response.StatusCode, decoded.Error, key)
		}
		return ModelResponse{}, normalizeProviderError(response.StatusCode, nil, key)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	var result ModelResponse
	switch kind {
	case "text/event-stream":
		if !wire.Stream {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "unexpected stream for a JSON-only model"}
		}
		result, err = decodeModelStream(response.Body, key, emit)
	case "application/json":
		result, err = decodeModelJSON(response.Body, key)
	default:
		return ModelResponse{}, &CallError{Category: "protocol", Message: "unsupported model response content type"}
	}
	if err == nil && client.caps.Usage == "required" && result.Usage == nil {
		err = &CallError{Certain: true, Category: "usage", Message: "model omitted required token usage; no tool was dispatched"}
	}
	if err == nil && client.caps.ParallelTools == "none" && len(result.ToolCalls) > 1 {
		err = &CallError{Certain: true, Category: "capability", Message: "model returned unsupported parallel tools; no tool was dispatched"}
	}
	if err != nil {
		result.ToolCalls = nil
	}
	if err == nil && !result.Provider.Streamed && emit != nil {
		emit(result.Content)
	}
	return result, err
}

func (client *OpenAICompatibleClient) Embed(ctx context.Context, model string, inputs []string) (EmbeddingResponse, error) {
	if client.caps.Embeddings != "supported" {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "capability",
			Message: "model does not advertise embedding support"}
	}
	if model == "" {
		model = client.model
	}
	if model != client.model || len(inputs) == 0 || len(inputs) > 128 {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
			Message: "invalid embedding request"}
	}
	totalBytes := 0
	for _, input := range inputs {
		if !utf8.ValidString(input) || len(input) == 0 || len(input) > 1<<20 {
			return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
				Message: "invalid embedding input"}
		}
		totalBytes += len(input)
		if totalBytes > 4<<20 {
			return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
				Message: "embedding inputs exceed the limit"}
		}
	}
	body, err := json.Marshal(openAIEmbeddingRequest{Model: model, Input: inputs})
	if err != nil {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
			Message: "cannot encode embedding request"}
	}
	if err := ctx.Err(); err != nil {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "cancelled",
			Message: "cancelled before embedding dispatch"}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		client.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
			Message: "invalid embedding base URL"}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if client.key.secret != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+client.key.secret)
	}
	response, err := client.client.Do(httpRequest)
	if err != nil {
		category := "transport"
		if ctx.Err() != nil {
			category = "cancelled"
		} else if failure, ok := err.(interface{ Timeout() bool }); ok && failure.Timeout() {
			category = "timeout"
		}
		return EmbeddingResponse{}, &CallError{Category: category,
			Message: "embedding transport interrupted; response unknown"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		var envelope openAIEmbeddingEnvelope
		if len(raw) <= 64<<10 {
			_ = json.Unmarshal(raw, &envelope)
		}
		return EmbeddingResponse{}, normalizeProviderError(response.StatusCode, envelope.Error, client.key.secret)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxModelResponseBytes+1))
	if err != nil || len(raw) > maxModelResponseBytes || !utf8.Valid(raw) {
		return EmbeddingResponse{}, &CallError{Category: "transport",
			Message: "embedding response interrupted or oversized"}
	}
	var envelope openAIEmbeddingEnvelope
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) != len(inputs) {
		return EmbeddingResponse{}, &CallError{Category: "protocol",
			Message: "invalid embedding response"}
	}
	vectors := make([][]float64, len(envelope.Data))
	dimension := 0
	for _, item := range envelope.Data {
		if item.Index < 0 || item.Index >= len(inputs) || len(item.Embedding) == 0 ||
			len(item.Embedding) > 8192 {
			return EmbeddingResponse{}, &CallError{Category: "protocol",
				Message: "invalid embedding vector"}
		}
		if dimension == 0 {
			dimension = len(item.Embedding)
		} else if dimension != len(item.Embedding) {
			return EmbeddingResponse{}, &CallError{Category: "protocol",
				Message: "embedding dimensions changed"}
		}
		for _, value := range item.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return EmbeddingResponse{}, &CallError{Category: "protocol",
					Message: "embedding vector contains a non-finite value"}
			}
		}
		if vectors[item.Index] != nil {
			return EmbeddingResponse{}, &CallError{Category: "protocol",
				Message: "embedding indices repeated"}
		}
		vectors[item.Index] = item.Embedding
	}
	for _, vector := range vectors {
		if len(vector) == 0 {
			return EmbeddingResponse{}, &CallError{Category: "protocol",
				Message: "embedding indices are not contiguous"}
		}
	}
	usage, usageErr := embeddingUsage(envelope.Usage)
	if usageErr != nil {
		return EmbeddingResponse{}, usageErr
	}
	usageStatus := "missing"
	if usage != nil {
		usageStatus = "reported"
	}
	return EmbeddingResponse{Vectors: vectors, Usage: usage,
		Provider: &ProviderResponse{Protocol: "embeddings",
			Model: redact(envelope.Model, client.key.secret), UsageStatus: usageStatus}}, nil
}

func decodeModelJSON(reader io.Reader, key string) (ModelResponse, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxModelResponseBytes+1))
	if err != nil || len(raw) > maxModelResponseBytes {
		return ModelResponse{}, &CallError{Category: "transport", Message: "model response interrupted or oversized"}
	}
	var decoded openAIEnvelope
	if !utf8.Valid(raw) || json.Unmarshal(raw, &decoded) != nil {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid model response"}
	}
	if decoded.Error != nil {
		return ModelResponse{}, normalizeProviderError(0, decoded.Error, key)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Index != 0 || decoded.Choices[0].Message.Role != "assistant" {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid model response choice"}
	}
	choice := decoded.Choices[0]
	return finalizeModelOutput(choice.Message, choice.FinishReason, decoded, false, key)
}

func decodeModelStream(reader io.Reader, key string, emit func(string)) (ModelResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxModelResponseBytes+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	message := openAIMessage{Role: "assistant"}
	calls := map[int]openAIToolCall{}
	finish := ""
	done := false
	bytesRead := 0
	var streamBuffer string
	envelope := openAIEnvelope{}
	// Hold a credential-sized tail so a secret split across deltas is redacted.
	emitSafe := func(text string, flush bool) {
		streamBuffer += text
		streamBuffer = redact(streamBuffer, key)
		count := len(streamBuffer)
		if !flush && key != "" {
			count = max(0, count-len(key))
		}
		for count > 0 && !utf8.ValidString(streamBuffer[:count]) {
			count--
		}
		if emit != nil && count > 0 {
			emit(streamBuffer[:count])
		}
		streamBuffer = streamBuffer[count:]
	}
	processEvent := func(data string) error {
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			return nil
		}
		var chunk openAIEnvelope
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) > 1 {
			return &CallError{Category: "protocol", Message: "invalid model stream event"}
		}
		if chunk.Error != nil {
			return normalizeProviderError(0, chunk.Error, key)
		}
		if err := mergeStreamMetadata(&envelope, chunk); err != nil {
			return err
		}
		if chunk.Usage != nil {
			if envelope.Usage != nil {
				return &CallError{Category: "protocol", Message: "multiple model usage events"}
			}
			envelope.Usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			return nil // Usage-only final chunks.
		}
		if finish != "" {
			return &CallError{Category: "protocol", Message: "model stream continued after its finish event"}
		}
		choice := chunk.Choices[0]
		if choice.Index != 0 {
			return &CallError{Category: "protocol", Message: "unsupported model choice index"}
		}
		delta := choice.Delta
		if delta.Role != "" && delta.Role != "assistant" {
			return &CallError{Category: "protocol", Message: "invalid model stream role"}
		}
		message.Content += delta.Content
		message.Refusal += delta.Refusal
		if len(message.Content) > 1<<20 || len(message.Refusal) > 1<<20 {
			return &CallError{Category: "protocol", Message: "model stream text exceeded limits"}
		}
		emitSafe(delta.Content, false)
		for _, part := range delta.ToolCalls {
			if part.Index < 0 || part.Index >= 32 {
				return &CallError{Category: "protocol", Message: "too many streamed tool calls"}
			}
			call := calls[part.Index]
			call.ID += part.ID
			if part.Type != "" {
				if call.Type != "" && call.Type != part.Type {
					return &CallError{Category: "protocol", Message: "streamed tool type changed"}
				}
				call.Type = part.Type
			}
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
			if len(call.Function.Arguments) > 2*maxFileBytes || len(call.Function.Name) > 128 || len(call.ID) > 256 {
				return &CallError{Category: "protocol", Message: "streamed tool call exceeded limits"}
			}
			calls[part.Index] = call
		}
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
		return nil
	}
	var eventData []string
	eventBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line) + 1
		if bytesRead > maxModelResponseBytes || !utf8.ValidString(line) {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "model stream exceeded limits or was not UTF-8"}
		}
		if line == "" {
			if len(eventData) != 0 {
				if err := processEvent(strings.Join(eventData, "\n")); err != nil {
					return ModelResponse{}, err
				}
				eventData, eventBytes = nil, 0
				if done {
					break
				}
			}
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		if field == "data" {
			if !hasColon {
				value = ""
			}
			value = strings.TrimPrefix(value, " ")
			eventBytes += len(value) + 1
			if eventBytes > 1<<20 {
				return ModelResponse{}, &CallError{Category: "protocol", Message: "model stream event exceeded limits"}
			}
			eventData = append(eventData, value)
		}
	}
	emitSafe("", true)
	if scanner.Err() != nil || !done || finish == "" {
		return ModelResponse{}, &CallError{Category: "transport", Message: "model stream incomplete; partial text is not a final answer"}
	}
	for index := 0; index < len(calls); index++ {
		call, ok := calls[index]
		if !ok {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "non-contiguous streamed tool indices"}
		}
		call.Index = index
		message.ToolCalls = append(message.ToolCalls, call)
	}
	return finalizeModelOutput(message, finish, envelope, true, key)
}

func mergeStreamMetadata(target *openAIEnvelope, chunk openAIEnvelope) error {
	for _, pair := range []struct {
		target *string
		value  string
	}{{&target.ID, chunk.ID}, {&target.Model, chunk.Model}, {&target.SystemFingerprint, chunk.SystemFingerprint}} {
		if pair.value == "" {
			continue
		}
		if len(pair.value) > 256 || *pair.target != "" && *pair.target != pair.value {
			return &CallError{Category: "protocol", Message: "model stream response identity changed or exceeded limits"}
		}
		*pair.target = pair.value
	}
	if chunk.Created != 0 {
		if chunk.Created < 0 || target.Created != 0 && target.Created != chunk.Created {
			return &CallError{Category: "protocol", Message: "model stream creation timestamp changed"}
		}
		target.Created = chunk.Created
	}
	return nil
}

func finalizeModelOutput(message openAIMessage, finish string, envelope openAIEnvelope, streamed bool, key string) (ModelResponse, error) {
	result, outputErr := normalizeModelOutput(message, finish, key)
	usage, usageErr := modelUsage(envelope.Usage)
	result.Usage = usage
	meta := &ProviderResponse{Protocol: "chat", Streamed: streamed, ID: redact(envelope.ID, key),
		Model: redact(envelope.Model, key), Created: envelope.Created, SystemFingerprint: redact(envelope.SystemFingerprint, key),
		RawFinishReason: redact(finish, key), UsageStatus: "missing"}
	switch finish {
	case "stop", "tool_calls", "length", "content_filter":
		meta.FinishReason = finish
	default:
		meta.FinishReason = "unsupported"
	}
	if message.Refusal != "" {
		meta.FinishReason = "refusal"
	}
	if usage != nil {
		meta.UsageStatus = "reported"
	}
	result.Provider = meta
	if len(meta.ID) > 256 || len(meta.Model) > 256 || len(meta.SystemFingerprint) > 256 ||
		meta.Created < 0 || len(meta.RawFinishReason) > 64 {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid provider response metadata"}
	}
	if err := errors.Join(outputErr, usageErr); err != nil {
		result.ToolCalls = nil
		return result, err
	}
	return result, nil
}

func modelUsage(usage *openAIUsage) (*ModelUsage, error) {
	if usage == nil {
		return nil, nil
	}
	if usage.PromptTokens == nil || usage.CompletionTokens == nil || usage.TotalTokens == nil ||
		*usage.PromptTokens < 0 || *usage.CompletionTokens < 0 || *usage.TotalTokens < 0 ||
		*usage.PromptTokens > 1<<50 || *usage.CompletionTokens > 1<<50 ||
		*usage.TotalTokens != *usage.PromptTokens+*usage.CompletionTokens {
		return nil, &CallError{Category: "protocol", Message: "invalid model usage"}
	}
	return &ModelUsage{InputTokens: *usage.PromptTokens, OutputTokens: *usage.CompletionTokens, TotalTokens: *usage.TotalTokens}, nil
}

func normalizeModelOutput(message openAIMessage, finish, key string) (ModelResponse, error) {
	result := ModelResponse{Content: redact(message.Content, key)}
	if message.Refusal != "" {
		return result, &CallError{Certain: true, Category: "refusal", Message: "model refused this request; no tool was dispatched"}
	}
	if finish == "length" || finish == "content_filter" {
		category := "output_limit"
		if finish == "content_filter" {
			category = "content_filter"
		}
		return result, &CallError{Certain: true, Category: category, Message: "model returned " + finish + "; partial output is not a completed answer"}
	}
	if finish != "stop" && finish != "tool_calls" {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "model did not finish; no tool was dispatched"}
	}
	if len(message.ToolCalls) > 32 {
		return ModelResponse{}, &CallError{Message: "model returned too many tool calls"}
	}
	for index, call := range message.ToolCalls {
		if call.Type != "function" || call.ID == "" || call.Function.Name == "" ||
			(key != "" && (strings.Contains(call.Function.Arguments, key) ||
				strings.Contains(call.ID, key) || strings.Contains(call.Function.Name, key))) {
			return ModelResponse{}, &CallError{Message: "invalid or secret-bearing tool call"}
		}
		// Malformed arguments become a bounded validation result, never an execution.
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			arguments = json.RawMessage(`null`)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments,
			Provider: &ProviderToolItem{Index: index, Type: call.Type}})
	}
	if (finish == "tool_calls") != (len(result.ToolCalls) > 0) {
		return ModelResponse{}, &CallError{Message: "model tool calls do not match finish reason"}
	}
	if err := validateResponse(result); err != nil {
		return ModelResponse{}, &CallError{Message: "model response exceeded limits or contained duplicate calls"}
	}
	return result, nil
}

func normalizeProviderError(status int, detail *openAIError, key string) *CallError {
	err := &CallError{Certain: true, Category: "provider", HTTPStatus: status, Message: fmt.Sprintf("model returned HTTP %d", status)}
	if status == 0 {
		err.Message = "model returned a definitive provider error"
	}
	code, kind := "", ""
	if detail != nil {
		code, kind = detail.Code, detail.Type
		if validProviderCode(code) && (key == "" || !strings.Contains(code, key)) {
			err.Code = code
		}
	}
	switch {
	case code == "context_length_exceeded":
		err.Category = "context_limit"
	case code == "insufficient_quota" || kind == "insufficient_quota" ||
		code == "credit_balance_exhausted" || code == "organization_spend_limit_exceeded" ||
		code == "project_spend_limit_exceeded" || code == "organization_usage_limit_exceeded":
		err.Category = "quota"
	case status == 401 || kind == "authentication_error":
		err.Category = "authentication"
	case status == 403 || kind == "permission_error":
		err.Category = "permission"
	case status == 429 || kind == "rate_limit_error":
		err.Category, err.Retryable = "rate_limit", true
	case status >= 500 || kind == "server_error" || kind == "service_unavailable_error":
		err.Category, err.Retryable = "unavailable", true
	case status == 408:
		err.Category, err.Retryable = "timeout", true
	case status == 400 || status == 422 || kind == "invalid_request_error":
		err.Category = "request"
	case status >= 300 && status < 400:
		err.Category = "redirect"
	}
	return err
}

func validProviderCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, char := range code {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_') {
			return false
		}
	}
	return true
}

func redact(text, key string) string {
	if key != "" {
		return strings.ReplaceAll(text, key, "[REDACTED]")
	}
	return text
}

type openAIRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIMessage      `json:"messages"`
	Tools               []openAITool         `json:"tools,omitempty"`
	Stream              bool                 `json:"stream"`
	MaxTokens           int                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	ParallelToolCalls   *bool                `json:"parallel_tool_calls,omitempty"`
	StreamOptions       *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
}

type openAIMessage struct {
	Role       string           `json:"role,omitempty"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	Refusal    string           `json:"refusal,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function openAICallFunc `json:"function"`
}

type openAICallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIEnvelope struct {
	ID                string       `json:"id,omitempty"`
	Model             string       `json:"model,omitempty"`
	Created           int64        `json:"created,omitempty"`
	SystemFingerprint string       `json:"system_fingerprint,omitempty"`
	Error             *openAIError `json:"error,omitempty"`
	Usage             *openAIUsage `json:"usage,omitempty"`
	Choices           []struct {
		Index        int           `json:"index"`
		Message      openAIMessage `json:"message"`
		Delta        openAIMessage `json:"delta"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingEnvelope struct {
	Model string       `json:"model,omitempty"`
	Error *openAIError `json:"error,omitempty"`
	Data  []struct {
		Object    string    `json:"object,omitempty"`
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage *struct {
		PromptTokens *int64 `json:"prompt_tokens"`
		TotalTokens  *int64 `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func embeddingUsage(usage *struct {
	PromptTokens *int64 `json:"prompt_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}) (*ModelUsage, error) {
	if usage == nil {
		return nil, nil
	}
	if usage.PromptTokens == nil || usage.TotalTokens == nil ||
		*usage.PromptTokens < 0 || *usage.TotalTokens < 0 ||
		*usage.PromptTokens > 1<<50 || *usage.TotalTokens < *usage.PromptTokens {
		return nil, &CallError{Category: "protocol", Message: "invalid embedding usage"}
	}
	return &ModelUsage{InputTokens: *usage.PromptTokens, TotalTokens: *usage.TotalTokens}, nil
}

type openAIError struct {
	Code string `json:"code"`
	Type string `json:"type"`
}

func toOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	result := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, openAIToolCall{ID: call.ID, Type: "function",
			Function: openAICallFunc{Name: call.Name, Arguments: string(call.Arguments)}})
	}
	return result
}
