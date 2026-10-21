package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type AnthropicClient struct {
	baseURL string
	model   string
	key     ResolvedKey
	client  *http.Client
	caps    ModelCapabilities
}

func (AnthropicClient) String() string   { return redactedText }
func (AnthropicClient) GoString() string { return redactedText }
func (AnthropicClient) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, redactedText)
}

func NewAnthropicClient(config ModelConfig, key ResolvedKey, httpClient *http.Client) (*AnthropicClient, error) {
	if config.Provider != "" && config.Provider != "anthropic" {
		return nil, fmt.Errorf("harness: unsupported model provider %q", config.Provider)
	}
	if strings.TrimSpace(config.Model) == "" || !validModelName(config.Model) {
		return nil, errors.New("harness: model name is required")
	}
	if err := config.Capabilities.validate(); err != nil {
		return nil, err
	}
	if key.Protocol != "" && key.Protocol != KeyProtocolAnthropic {
		return nil, errors.New("harness: anthropic client needs an anthropic key protocol")
	}
	base, err := NormalizeBaseURLForProtocol(key.BaseURL, KeyProtocolAnthropic)
	if err != nil || base != key.BaseURL {
		return nil, errors.New("harness: model base URL is required")
	}
	caps := config.EffectiveCapabilities()
	if caps.Protocol != "chat" {
		return nil, fmt.Errorf("%w: only Messages chat is implemented", ErrModelCapability)
	}
	client := http.Client{Timeout: 2 * time.Minute}
	if httpClient != nil {
		client = *httpClient
		if client.Timeout == 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &AnthropicClient{baseURL: base, model: config.Model, key: key, client: &client, caps: caps}, nil
}

func (client *AnthropicClient) BaseURL() string { return client.baseURL }

func (client *AnthropicClient) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	config := ModelConfig{Provider: "anthropic", Capabilities: client.caps}
	if err := validateModelRequest(config, request); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "capability", Message: "model cannot accept the complete message/tool group"}
	}
	wire, err := anthropicRequestFrom(request, client.model, client.caps)
	if err != nil {
		return ModelResponse{}, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "cannot encode model request"}
	}
	if err := ctx.Err(); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "cancelled", Message: "cancelled before model dispatch"}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "invalid model base URL"}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream, application/json")
	httpRequest.Header.Set("anthropic-version", "2023-06-01")
	if client.key.secret != "" {
		httpRequest.Header.Set("x-api-key", client.key.secret)
	}
	response, err := client.client.Do(httpRequest)
	if err != nil {
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
		return ModelResponse{}, normalizeAnthropicError(response.StatusCode, raw, client.key.secret)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	var result ModelResponse
	switch kind {
	case "text/event-stream":
		if !wire.Stream {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "unexpected stream for a JSON-only model"}
		}
		result, err = decodeAnthropicStream(response.Body, client.key.secret, emit)
	case "application/json":
		result, err = decodeAnthropicJSON(response.Body, client.key.secret)
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
	if err == nil && result.Provider != nil && !result.Provider.Streamed && emit != nil {
		emit(result.Content)
	}
	return result, err
}

type anthropicRequest struct {
	Model      string               `json:"model"`
	System     string               `json:"system,omitempty"`
	Messages   []anthropicMessage   `json:"messages"`
	MaxTokens  int                  `json:"max_tokens"`
	Stream     bool                 `json:"stream"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	DisableParallelToolUse bool   `json:"disable_parallel_tool_use,omitempty"`
}

func anthropicRequestFrom(request ModelRequest, model string, caps ModelCapabilities) (anthropicRequest, error) {
	result := anthropicRequest{Model: model, MaxTokens: request.MaxOutputTokens, Stream: caps.Streaming != "none"}
	var system []string
	appendMessage := func(role string, blocks []anthropicBlock) {
		if len(blocks) == 0 {
			return
		}
		if len(result.Messages) != 0 && result.Messages[len(result.Messages)-1].Role == role {
			result.Messages[len(result.Messages)-1].Content = append(result.Messages[len(result.Messages)-1].Content, blocks...)
			return
		}
		result.Messages = append(result.Messages, anthropicMessage{Role: role, Content: blocks})
	}
	for _, message := range request.Messages {
		switch message.Role {
		case "system":
			if message.Content != "" {
				system = append(system, message.Content)
			}
		case "user":
			if message.Content != "" {
				appendMessage("user", []anthropicBlock{{Type: "text", Text: message.Content}})
			}
		case "assistant":
			blocks := make([]anthropicBlock, 0, 1+len(message.ToolCalls))
			if message.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				input := call.Arguments
				if !json.Valid(input) {
					input = json.RawMessage(`null`)
				}
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: input})
			}
			appendMessage("assistant", blocks)
		case "tool":
			appendMessage("user", []anthropicBlock{{Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Content}})
		default:
			return anthropicRequest{}, &CallError{Certain: true, Category: "request", Message: "unsupported message role"}
		}
	}
	result.System = strings.Join(system, "\n\n")
	for _, tool := range request.Tools {
		result.Tools = append(result.Tools, anthropicTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	if len(result.Tools) != 0 && caps.ParallelTools == "none" {
		result.ToolChoice = &anthropicToolChoice{Type: "auto", DisableParallelToolUse: true}
	}
	if len(result.Messages) == 0 {
		return anthropicRequest{}, &CallError{Certain: true, Category: "request", Message: "model request has no messages"}
	}
	return result, nil
}

type anthropicEnvelope struct {
	ID         string                   `json:"id"`
	Model      string                   `json:"model"`
	Content    []anthropicResponseBlock `json:"content"`
	StopReason string                   `json:"stop_reason"`
	Usage      *anthropicUsage          `json:"usage"`
	Error      *anthropicErrorBody      `json:"error"`
}

type anthropicResponseBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func decodeAnthropicJSON(reader io.Reader, key string) (ModelResponse, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxModelResponseBytes+1))
	if err != nil || len(raw) > maxModelResponseBytes {
		return ModelResponse{}, &CallError{Category: "transport", Message: "model response interrupted or oversized"}
	}
	var envelope anthropicEnvelope
	if !utf8.Valid(raw) || json.Unmarshal(raw, &envelope) != nil {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid model response"}
	}
	if envelope.Error != nil {
		return ModelResponse{}, normalizeAnthropicError(0, raw, key)
	}
	return finalizeAnthropicOutput(envelope.Content, envelope.StopReason, envelope, false, key)
}

type anthropicStreamBlock struct {
	index int
	block anthropicResponseBlock
	input strings.Builder
}

func decodeAnthropicStream(reader io.Reader, key string, emit func(string)) (ModelResponse, error) {
	blocks := map[int]*anthropicStreamBlock{}
	envelope := anthropicEnvelope{}
	finished := false
	stopReason := ""
	var streamBuffer string
	emitSafe := func(text string, flush bool) {
		streamBuffer += text
		streamBuffer = redact(streamBuffer, key)
		count := len(streamBuffer)
		if !flush && key != "" {
			count = max(0, count-len(key))
		}
		if emit != nil && count > 0 {
			emit(streamBuffer[:count])
		}
		streamBuffer = streamBuffer[count:]
	}
	handle := func(event, data string) error {
		var item struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				ID    string          `json:"id"`
				Model string          `json:"model"`
				Usage *anthropicUsage `json:"usage"`
			} `json:"message"`
			ContentBlock *anthropicResponseBlock `json:"content_block"`
			Delta        *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *anthropicUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &item) != nil {
			return &CallError{Category: "protocol", Message: "invalid model stream event"}
		}
		switch item.Type {
		case "message_start":
			if item.Message != nil {
				envelope.ID, envelope.Model = item.Message.ID, item.Message.Model
				envelope.Usage = item.Message.Usage
			}
		case "content_block_start":
			if item.ContentBlock == nil || item.Index < 0 || item.Index >= 32 || blocks[item.Index] != nil {
				return &CallError{Category: "protocol", Message: "invalid content block start"}
			}
			blocks[item.Index] = &anthropicStreamBlock{index: item.Index, block: *item.ContentBlock}
		case "content_block_delta":
			block := blocks[item.Index]
			if block == nil || item.Delta == nil {
				return &CallError{Category: "protocol", Message: "content block delta has no block"}
			}
			switch item.Delta.Type {
			case "text_delta":
				block.block.Text += item.Delta.Text
				if len(block.block.Text) > 1<<20 {
					return &CallError{Category: "protocol", Message: "model stream text exceeded limits"}
				}
				emitSafe(item.Delta.Text, false)
			case "input_json_delta":
				block.input.WriteString(item.Delta.PartialJSON)
				if block.input.Len() > 2*maxFileBytes {
					return &CallError{Category: "protocol", Message: "tool input exceeded limits"}
				}
			case "thinking_delta":
			default:
				return &CallError{Category: "protocol", Message: "unsupported model stream delta"}
			}
		case "message_delta":
			if item.Delta != nil && item.Delta.StopReason != "" {
				stopReason = item.Delta.StopReason
			}
			if item.Usage != nil {
				if envelope.Usage == nil {
					envelope.Usage = &anthropicUsage{}
				}
				envelope.Usage.OutputTokens = item.Usage.OutputTokens
			}
		case "message_stop":
			finished = true
		case "ping", "content_block_stop":
		default:
			return &CallError{Category: "protocol", Message: "unsupported model stream event"}
		}
		_ = event
		return nil
	}
	if err := scanProviderSSE(reader, handle); err != nil {
		var callErr *CallError
		if errors.As(err, &callErr) {
			return ModelResponse{}, err
		}
		return ModelResponse{}, &CallError{Category: "transport", Message: "model stream interrupted; partial text is not a final answer"}
	}
	emitSafe("", true)
	if !finished || stopReason == "" {
		return ModelResponse{}, &CallError{Category: "transport", Message: "model stream incomplete; partial text is not a final answer"}
	}
	envelope.StopReason = stopReason
	for index := 0; index < len(blocks); index++ {
		block := blocks[index]
		if block == nil {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "non-contiguous content blocks"}
		}
		if block.block.Type == "tool_use" {
			block.block.Input = json.RawMessage(block.input.String())
			if !json.Valid(block.block.Input) {
				block.block.Input = json.RawMessage(`null`)
			}
		}
		envelope.Content = append(envelope.Content, block.block)
	}
	return finalizeAnthropicOutput(envelope.Content, envelope.StopReason, envelope, true, key)
}

func finalizeAnthropicOutput(blocks []anthropicResponseBlock, finish string, envelope anthropicEnvelope, streamed bool, key string) (ModelResponse, error) {
	result := ModelResponse{}
	for index, block := range blocks {
		switch block.Type {
		case "text":
			result.Content += redact(block.Text, key)
		case "tool_use":
			if block.ID == "" || block.Name == "" || len(block.ID) > 256 || len(block.Name) > 128 {
				return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid Anthropic tool call"}
			}
			input := block.Input
			if !json.Valid(input) {
				input = json.RawMessage(`null`)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: redact(block.ID, key), Name: redact(block.Name, key), Arguments: input,
				Provider: &ProviderToolItem{Index: index, Type: "function"}})
		case "thinking":
		default:
			return ModelResponse{}, &CallError{Category: "protocol", Message: "unsupported Anthropic content block"}
		}
	}
	usage, err := anthropicUsageValue(envelope.Usage)
	if err != nil {
		return ModelResponse{}, err
	}
	meta := &ProviderResponse{Protocol: "chat", ID: redact(envelope.ID, key), Model: redact(envelope.Model, key),
		Streamed: streamed, RawFinishReason: redact(finish, key), UsageStatus: "missing"}
	switch finish {
	case "end_turn", "stop_sequence":
		meta.FinishReason = "stop"
	case "tool_use":
		meta.FinishReason = "tool_calls"
	case "max_tokens":
		meta.FinishReason = "length"
	case "refusal":
		meta.FinishReason = "refusal"
	default:
		meta.FinishReason = "unsupported"
	}
	if usage != nil {
		result.Usage = usage
		meta.UsageStatus = "reported"
	}
	result.Provider = meta
	if meta.FinishReason == "length" {
		return result, &CallError{Certain: true, Category: "output_limit", Message: "model returned max_tokens; partial output is not a completed answer"}
	}
	if meta.FinishReason == "refusal" {
		return result, &CallError{Certain: true, Category: "refusal", Message: "model refused this request; no tool was dispatched"}
	}
	if meta.FinishReason != "stop" && meta.FinishReason != "tool_calls" {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "model did not finish; no tool was dispatched"}
	}
	if (meta.FinishReason == "tool_calls") != (len(result.ToolCalls) > 0) {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "model tool calls do not match finish reason"}
	}
	if err := validateResponse(result); err != nil {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "model response exceeded limits or contained duplicate calls"}
	}
	return result, nil
}

func anthropicUsageValue(usage *anthropicUsage) (*ModelUsage, error) {
	if usage == nil {
		return nil, nil
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheCreationInputTokens < 0 ||
		usage.CacheReadInputTokens < 0 || usage.InputTokens > 1<<50 || usage.OutputTokens > 1<<50 {
		return nil, &CallError{Category: "protocol", Message: "invalid model usage"}
	}
	input := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	total := input + usage.OutputTokens
	if input < usage.InputTokens || total < input || total > 1<<50 {
		return nil, &CallError{Category: "protocol", Message: "invalid model usage"}
	}
	return &ModelUsage{InputTokens: input, OutputTokens: usage.OutputTokens, TotalTokens: total}, nil
}

func normalizeAnthropicError(status int, raw []byte, key string) *CallError {
	var envelope struct {
		Type  string              `json:"type"`
		Error *anthropicErrorBody `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	detail := envelope.Error
	if detail == nil && envelope.Type != "" {
		detail = &anthropicErrorBody{Type: envelope.Type}
	}
	err := &CallError{Certain: true, Category: "provider", HTTPStatus: status, Message: fmt.Sprintf("model returned HTTP %d", status)}
	if status == 0 {
		err.Message = "model returned a definitive provider error"
	}
	if detail == nil {
		return err
	}
	message := redact(detail.Message, key)
	if message != "" && len(message) <= 300 {
		err.Message = message
	}
	switch detail.Type {
	case "authentication_error":
		err.Category = "authentication"
	case "permission_error":
		err.Category = "permission"
	case "invalid_request_error":
		err.Category = "request"
	case "rate_limit_error":
		err.Category, err.Retryable = "rate_limit", true
	case "overloaded_error":
		err.Category, err.Retryable = "unavailable", true
	}
	if status == http.StatusUnauthorized {
		err.Category = "authentication"
	}
	if status == http.StatusTooManyRequests {
		err.Category, err.Retryable = "rate_limit", true
	}
	if status >= 500 {
		err.Category, err.Retryable = "unavailable", true
	}
	return err
}
