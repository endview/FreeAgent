package harness

import (
	"bufio"
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

// ResponsesClient implements the OpenAI Responses wire format used by
// DeepSeek. The API is stateless, so the full input history is rebuilt from
// the existing Harness messages and saved provider output items on every call.
type ResponsesClient struct {
	baseURL   string
	model     string
	key       ResolvedKey
	client    *http.Client
	caps      ModelCapabilities
	webSearch bool
}

func (ResponsesClient) String() string   { return redactedText }
func (ResponsesClient) GoString() string { return redactedText }
func (ResponsesClient) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, redactedText)
}

func NewResponsesClient(config ModelConfig, key ResolvedKey, httpClient *http.Client) (*ResponsesClient, error) {
	if config.Provider != "" && config.Provider != "openai-compatible" {
		return nil, fmt.Errorf("harness: unsupported Responses provider %q", config.Provider)
	}
	if strings.TrimSpace(config.Model) == "" || !validModelName(config.Model) {
		return nil, errors.New("harness: model name is required")
	}
	if err := config.Capabilities.validate(); err != nil {
		return nil, err
	}
	if key.Protocol != "" && key.Protocol != KeyProtocolOpenAICompatible {
		return nil, errors.New("harness: Responses client needs an OpenAI-compatible key protocol")
	}
	if base, err := NormalizeBaseURL(key.BaseURL); err != nil || base != key.BaseURL {
		return nil, errors.New("harness: model base URL is required")
	}
	caps := config.EffectiveCapabilities()
	if caps.Protocol != "responses" {
		return nil, fmt.Errorf("%w: Responses client needs the responses protocol", ErrModelCapability)
	}
	if caps.Embeddings == "supported" {
		return nil, fmt.Errorf("%w: Responses embeddings are not implemented", ErrModelCapability)
	}
	client := http.Client{Timeout: 2 * time.Minute}
	if httpClient != nil {
		client = *httpClient
		if client.Timeout == 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ResponsesClient{
		baseURL:   key.BaseURL,
		model:     config.Model,
		key:       key,
		client:    &client,
		caps:      caps,
		webSearch: caps.WebSearch == "supported",
	}, nil
}

func (client *ResponsesClient) BaseURL() string { return client.baseURL }

func (client *ResponsesClient) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	config := ModelConfig{Provider: "openai-compatible", Model: client.model, Capabilities: client.caps}
	if err := validateModelRequest(config, request); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "capability", Message: "model cannot accept the complete message/tool group"}
	}
	wire, err := responsesRequestFrom(request, client.model, client.caps.Streaming != "none", client.webSearch)
	if err != nil {
		return ModelResponse{}, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "cannot encode Responses request"}
	}
	if err := ctx.Err(); err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "cancelled", Message: "cancelled before model dispatch"}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return ModelResponse{}, &CallError{Certain: true, Category: "request", Message: "invalid model base URL"}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream, application/json")
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
		return ModelResponse{}, &CallError{Category: category, Message: "model transport interrupted; response unknown"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		var decoded responsesEnvelope
		if len(raw) <= 64<<10 && json.Unmarshal(raw, &decoded) == nil && decoded.Error != nil {
			return ModelResponse{}, normalizeResponsesError(response.StatusCode, decoded.Error, client.key.secret)
		}
		return ModelResponse{}, normalizeProviderError(response.StatusCode, nil, client.key.secret)
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	var result ModelResponse
	switch kind {
	case "application/json":
		result, err = decodeResponsesJSON(response.Body, client.key.secret)
	case "text/event-stream":
		if !wire.Stream {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "unexpected stream for a JSON-only Responses model"}
		}
		result, err = decodeResponsesStream(response.Body, client.key.secret, emit)
	default:
		return ModelResponse{}, &CallError{Category: "protocol", Message: "unsupported Responses response content type"}
	}
	if err == nil && client.caps.Usage == "required" && result.Usage == nil {
		err = &CallError{Certain: true, Category: "usage", Message: "Responses omitted required token usage; no tool was dispatched"}
	}
	if err == nil && client.caps.ParallelTools == "none" && len(result.ToolCalls) > 1 {
		err = &CallError{Certain: true, Category: "capability", Message: "Responses returned unsupported parallel tools; no tool was dispatched"}
	}
	if err != nil {
		result.ToolCalls = nil
	}
	if err == nil && result.Provider != nil && !result.Provider.Streamed && emit != nil {
		emit(result.Content)
	}
	return result, err
}

type responsesRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           []json.RawMessage `json:"input"`
	Tools           []responsesTool   `json:"tools,omitempty"`
	Stream          bool              `json:"stream"`
	MaxOutputTokens int               `json:"max_output_tokens"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func responsesRequestFrom(request ModelRequest, model string, stream, webSearch bool) (responsesRequest, error) {
	instructions, input, err := responsesInputFromMessages(request.Messages)
	if err != nil {
		return responsesRequest{}, err
	}
	wire := responsesRequest{Model: model, Instructions: instructions, Input: input, Stream: stream, MaxOutputTokens: request.MaxOutputTokens}
	toolNames := make(map[string]struct{}, len(request.Tools))
	for _, tool := range request.Tools {
		if !validResponsesFunctionName(tool.Name) {
			return responsesRequest{}, &CallError{Certain: true, Category: "request", Message: "invalid Responses function name"}
		}
		if _, exists := toolNames[tool.Name]; exists {
			return responsesRequest{}, &CallError{Certain: true, Category: "request", Message: "duplicate Responses function name"}
		}
		toolNames[tool.Name] = struct{}{}
		wire.Tools = append(wire.Tools, responsesTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema})
	}
	if webSearch {
		wire.Tools = append(wire.Tools, responsesTool{Type: "web_search"})
	}
	if len(wire.Input) == 0 && strings.TrimSpace(wire.Instructions) == "" {
		return responsesRequest{}, &CallError{Certain: true, Category: "request", Message: "Responses request needs input or instructions"}
	}
	return wire, nil
}

func responsesInputFromMessages(messages []ModelMessage) (string, []json.RawMessage, error) {
	var instructions string
	input := make([]json.RawMessage, 0, len(messages))
	for index, message := range messages {
		if index == 0 && message.Role == "system" {
			instructions = message.Content
			continue
		}
		if len(message.ProviderItems) != 0 {
			for _, raw := range message.ProviderItems {
				if len(raw) == 0 || len(raw) > 1<<20 || !json.Valid(raw) {
					return "", nil, &CallError{Certain: true, Category: "protocol", Message: "invalid saved Responses output item"}
				}
				input = append(input, append(json.RawMessage(nil), raw...))
			}
			continue
		}
		switch message.Role {
		case "system", "user", "assistant":
			item := map[string]any{"type": "message", "role": message.Role, "content": message.Content}
			if message.Content == "" && len(message.ToolCalls) != 0 {
				delete(item, "content")
			}
			raw, _ := json.Marshal(item)
			input = append(input, raw)
			for _, call := range message.ToolCalls {
				if call.ID == "" || !validResponsesFunctionName(call.Name) || len(call.Arguments) > 2*maxFileBytes {
					return "", nil, &CallError{Certain: true, Category: "protocol", Message: "invalid saved function call"}
				}
				raw, _ := json.Marshal(map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
				input = append(input, raw)
			}
		case "tool":
			if message.ToolCallID == "" {
				return "", nil, &CallError{Certain: true, Category: "protocol", Message: "saved tool result has no call id"}
			}
			raw, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
			input = append(input, raw)
		default:
			return "", nil, &CallError{Certain: true, Category: "protocol", Message: "unsupported saved Responses message role"}
		}
	}
	return instructions, input, nil
}

func validResponsesFunctionName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range []byte(name) {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

type responsesEnvelope struct {
	ID               string               `json:"id,omitempty"`
	Status           string               `json:"status,omitempty"`
	CreatedAt        int64                `json:"created_at,omitempty"`
	Model            string               `json:"model,omitempty"`
	Output           []json.RawMessage    `json:"output,omitempty"`
	Usage            *responsesUsage      `json:"usage,omitempty"`
	Error            *responsesError      `json:"error,omitempty"`
	IncompleteDetail *responsesIncomplete `json:"incomplete_details,omitempty"`
}

type responsesOutputItem struct {
	Type      string             `json:"type,omitempty"`
	ID        string             `json:"id,omitempty"`
	Status    string             `json:"status,omitempty"`
	Role      string             `json:"role,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Query     string             `json:"query,omitempty"`
	Content   []responsesContent `json:"content,omitempty"`
	Action    *responsesAction   `json:"action,omitempty"`
}

type responsesContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type responsesAction struct {
	Type    string   `json:"type,omitempty"`
	Query   string   `json:"query,omitempty"`
	Queries []string `json:"queries,omitempty"`
	URL     string   `json:"url,omitempty"`
}

type responsesUsage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

type responsesError struct {
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type responsesIncomplete struct {
	Reason string `json:"reason,omitempty"`
}

func decodeResponsesJSON(reader io.Reader, key string) (ModelResponse, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxModelResponseBytes+1))
	if err != nil || len(raw) > maxModelResponseBytes || !utf8.Valid(raw) {
		return ModelResponse{}, &CallError{Category: "transport", Message: "Responses response interrupted or oversized"}
	}
	var envelope responsesEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid Responses response"}
	}
	return normalizeResponsesEnvelope(envelope, raw, key, false, nil)
}

type responsesStreamEvent struct {
	Type           string               `json:"type,omitempty"`
	SequenceNumber *int64               `json:"sequence_number,omitempty"`
	Delta          string               `json:"delta,omitempty"`
	ItemID         string               `json:"item_id,omitempty"`
	Item           *responsesOutputItem `json:"item,omitempty"`
	Response       *responsesEnvelope   `json:"response,omitempty"`
}

func decodeResponsesStream(reader io.Reader, key string, emit func(string)) (ModelResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxModelResponseBytes+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var eventName string
	var eventData []string
	var total int
	var lastSequence int64 = -1
	finalType := ""
	var finalResponse *responsesEnvelope
	var searchEvents []ProviderWebSearchCall
	upsertSearchEvent := func(call ProviderWebSearchCall) {
		if call.ID == "" {
			return
		}
		for index := range searchEvents {
			if searchEvents[index].ID == call.ID {
				searchEvents[index] = mergeWebSearchCall(searchEvents[index], call)
				return
			}
		}
		searchEvents = append(searchEvents, call)
	}
	process := func() error {
		if len(eventData) == 0 {
			return nil
		}
		data := strings.Join(eventData, "\n")
		var event responsesStreamEvent
		if json.Unmarshal([]byte(data), &event) != nil {
			return &CallError{Category: "protocol", Message: "invalid Responses stream event"}
		}
		if eventName != "" && event.Type != "" && eventName != event.Type {
			return &CallError{Category: "protocol", Message: "Responses event type mismatch"}
		}
		if event.SequenceNumber != nil {
			if *event.SequenceNumber <= lastSequence {
				return &CallError{Category: "protocol", Message: "Responses sequence number regressed"}
			}
			lastSequence = *event.SequenceNumber
		}
		switch event.Type {
		case "response.output_text.delta":
			if len(event.Delta) > 1<<20 {
				return &CallError{Category: "protocol", Message: "Responses output delta exceeded limits"}
			}
			if emit != nil && event.Delta != "" {
				emit(redact(event.Delta, key))
			}
		case "response.output_item.added", "response.output_item.done":
			if event.Item != nil && event.Item.Type == "web_search_call" {
				upsertSearchEvent(webSearchCallFrom(event.Item.ID, event.Item.Status, event.Item, key))
			}
		case "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed":
			status := strings.TrimPrefix(event.Type, "response.web_search_call.")
			id := event.ItemID
			if event.Item != nil {
				id = firstNonEmpty(id, event.Item.ID)
			}
			upsertSearchEvent(webSearchCallFrom(id, status, event.Item, key))
		case "response.completed", "response.incomplete", "response.failed":
			finalType = event.Type
			finalResponse = event.Response
		}
		eventName, eventData = "", nil
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxModelResponseBytes || !utf8.ValidString(line) {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "Responses stream exceeded limits or was not UTF-8"}
		}
		if line == "" {
			if err := process(); err != nil {
				return ModelResponse{}, err
			}
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		if hasColon {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			eventData = append(eventData, value)
		}
	}
	if scanner.Err() != nil {
		return ModelResponse{}, &CallError{Category: "transport", Message: "Responses stream interrupted; final response unknown"}
	}
	if err := process(); err != nil {
		return ModelResponse{}, err
	}
	if finalType == "" || finalResponse == nil {
		return ModelResponse{}, &CallError{Category: "transport", Message: "Responses stream incomplete; final response unknown"}
	}
	result, err := normalizeResponsesEnvelope(*finalResponse, nil, key, true, searchEvents)
	if err == nil && finalType == "response.failed" {
		return result, &CallError{Category: "provider", Message: "Responses provider returned a failed response"}
	}
	return result, err
}

func normalizeResponsesEnvelope(envelope responsesEnvelope, raw []byte, key string, streamed bool, searchEvents []ProviderWebSearchCall) (ModelResponse, error) {
	if envelope.Error != nil {
		return ModelResponse{}, normalizeResponsesError(0, envelope.Error, key)
	}
	status := envelope.Status
	if status == "failed" {
		return ModelResponse{}, normalizeResponsesError(0, &responsesError{Message: "Responses provider returned a failed response"}, key)
	}
	if status != "completed" && status != "incomplete" && status != "" {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "Responses response did not finish"}
	}
	result := ModelResponse{}
	var text strings.Builder
	var calls []ToolCall
	var providerItems []json.RawMessage
	webSearch := map[string]ProviderWebSearchCall{}
	var webSearchOrder []string
	upsertWebSearch := func(call ProviderWebSearchCall) {
		if call.ID == "" {
			return
		}
		current, exists := webSearch[call.ID]
		if !exists {
			webSearchOrder = append(webSearchOrder, call.ID)
		}
		if current.ID == "" {
			current.ID = call.ID
		}
		webSearch[call.ID] = mergeWebSearchCall(current, call)
	}
	for index, rawItem := range envelope.Output {
		if len(rawItem) == 0 || len(rawItem) > 1<<20 || !json.Valid(rawItem) {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "Responses output item exceeded limits"}
		}
		var item responsesOutputItem
		if json.Unmarshal(rawItem, &item) != nil {
			return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid Responses output item"}
		}
		itemRaw := json.RawMessage(redact(string(rawItem), key))
		providerItems = append(providerItems, itemRaw)
		switch item.Type {
		case "message":
			if item.Role != "" && item.Role != "assistant" {
				return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid Responses message role"}
			}
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(redact(part.Text, key))
				}
			}
		case "reasoning":
			// Reasoning is preserved in ProviderItems for stateless continuation,
			// but is never emitted as the user-facing answer.
		case "function_call":
			if item.CallID == "" || !validResponsesFunctionName(item.Name) || len(item.Arguments) > 2*maxFileBytes {
				return ModelResponse{}, &CallError{Category: "protocol", Message: "invalid Responses function call"}
			}
			arguments := json.RawMessage(item.Arguments)
			if !json.Valid(arguments) {
				arguments = json.RawMessage(`null`)
			}
			calls = append(calls, ToolCall{ID: redact(item.CallID, key), Name: redact(item.Name, key), Arguments: arguments,
				Provider: &ProviderToolItem{Index: index, Type: "function"}})
		case "web_search_call":
			id := item.ID
			if id == "" {
				id = fmt.Sprintf("web-search-%d", index)
			}
			upsertWebSearch(webSearchCallFrom(id, item.Status, &item, key))
		case "":
			return ModelResponse{}, &CallError{Category: "protocol", Message: "Responses output item has no type"}
		default:
			// Preserve unknown provider items for a later compatible continuation,
			// but they cannot become local tool calls.
		}
	}
	for _, call := range searchEvents {
		upsertWebSearch(call)
	}
	usage, err := responsesUsageValue(envelope.Usage)
	if err != nil {
		return ModelResponse{}, err
	}
	result.Content = text.String()
	result.ToolCalls = calls
	result.ProviderItems = providerItems
	result.Usage = usage
	meta := &ProviderResponse{Protocol: "responses", ID: redact(envelope.ID, key), Model: redact(envelope.Model, key),
		Created: envelope.CreatedAt, Streamed: streamed, UsageStatus: "missing", ResponseStatus: status}
	if len(calls) != 0 {
		meta.FinishReason = "tool_calls"
	} else if status == "incomplete" {
		if envelope.IncompleteDetail != nil && envelope.IncompleteDetail.Reason == "content_filter" {
			meta.FinishReason = "content_filter"
		} else {
			meta.FinishReason = "output_limit"
		}
	} else {
		meta.FinishReason = "stop"
	}
	if usage != nil {
		meta.UsageStatus = "reported"
	}
	for _, id := range webSearchOrder {
		meta.WebSearchCalls = append(meta.WebSearchCalls, webSearch[id])
	}
	result.Provider = meta
	if raw != nil && len(raw) > maxModelResponseBytes {
		return ModelResponse{}, &CallError{Category: "protocol", Message: "Responses response exceeded limits"}
	}
	if status == "incomplete" {
		category := "output_limit"
		if envelope.IncompleteDetail != nil && envelope.IncompleteDetail.Reason == "content_filter" {
			category = "content_filter"
		}
		return result, &CallError{Certain: true, Category: category, Message: "Responses returned an incomplete response"}
	}
	return result, nil
}

// deepSeekSearchMarker is an internal call reference DeepSeek appends to
// search queries and opened page URLs. It is kept in the replayed raw item
// but is not a user-visible query or page.
const deepSeekSearchMarker = "ws_call_id="

// webSearchCallFrom builds bounded display metadata from a web_search_call
// item. The raw item is replayed unchanged; only this summary is cleaned.
// DeepSeek reports "search" with a queries list and "open_page" with a url.
func webSearchCallFrom(id, status string, item *responsesOutputItem, key string) ProviderWebSearchCall {
	call := ProviderWebSearchCall{ID: truncateUTF8Bytes(redact(id, key), 256), Status: truncateUTF8Bytes(redact(status, key), 32)}
	if item == nil {
		return call
	}
	queries := []string{item.Query}
	if action := item.Action; action != nil {
		call.Action = truncateUTF8Bytes(redact(action.Type, key), 32)
		queries = append(append(queries, action.Query), action.Queries...)
		page, fragment, _ := strings.Cut(action.URL, "#")
		if fragment != "" && !strings.HasPrefix(fragment, deepSeekSearchMarker) {
			page = action.URL
		}
		call.URL = truncateUTF8Bytes(redact(page, key), 2048)
	}
	kept := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query != "" && !strings.HasPrefix(query, deepSeekSearchMarker) {
			kept = append(kept, query)
		}
	}
	call.Query = truncateUTF8Bytes(redact(strings.Join(kept, "; "), key), 4096)
	return call
}

func mergeWebSearchCall(current, next ProviderWebSearchCall) ProviderWebSearchCall {
	if current.ID == "" {
		current.ID = next.ID
	}
	for _, field := range []struct {
		target *string
		value  string
	}{
		{&current.Status, next.Status}, {&current.Action, next.Action},
		{&current.Query, next.Query}, {&current.URL, next.URL},
	} {
		if field.value != "" {
			*field.target = field.value
		}
	}
	return current
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func responsesUsageValue(usage *responsesUsage) (*ModelUsage, error) {
	if usage == nil {
		return nil, nil
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil ||
		*usage.InputTokens < 0 || *usage.OutputTokens < 0 || *usage.TotalTokens < 0 ||
		*usage.InputTokens > 1<<50 || *usage.OutputTokens > 1<<50 ||
		*usage.TotalTokens != *usage.InputTokens+*usage.OutputTokens {
		return nil, &CallError{Category: "protocol", Message: "invalid Responses usage"}
	}
	return &ModelUsage{InputTokens: *usage.InputTokens, OutputTokens: *usage.OutputTokens, TotalTokens: *usage.TotalTokens}, nil
}

func normalizeResponsesError(status int, detail *responsesError, key string) *CallError {
	provider := &openAIError{}
	if detail != nil {
		provider.Code = detail.Code
		provider.Type = detail.Type
	}
	err := normalizeProviderError(status, provider, key)
	if detail != nil && detail.Message != "" && !strings.Contains(detail.Message, key) {
		err.Message = redact(detail.Message, key)
	}
	return err
}
