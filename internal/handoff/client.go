package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

type Client struct {
	Origin     string
	Secret     string
	Generation string
	Header     string
	HTTP       *http.Client
}

type AttachState struct {
	Protocol      string                 `json:"protocol"`
	DataFormat    string                 `json:"data_format"`
	Generation    string                 `json:"generation"`
	OwnerKind     string                 `json:"owner_kind,omitempty"`
	PID           int                    `json:"pid,omitempty"`
	ExpiresAt     string                 `json:"expires_at,omitempty"`
	Stopping      bool                   `json:"stopping"`
	LastSeq       uint64                 `json:"last_seq"`
	ActiveChat    string                 `json:"active_chat"`
	Control       harness.ControlState   `json:"control"`
	Conversations []harness.Conversation `json:"conversations"`
}

type AttachEvents struct {
	Generation string          `json:"generation"`
	NextSeq    uint64          `json:"next_seq"`
	Events     []harness.Event `json:"events"`
}

type AttachRunStatus struct {
	Found      bool        `json:"found"`
	RequestKey string      `json:"request_key"`
	Run        harness.Run `json:"run"`
}

func NewClient(info EngineInfo, secret string) (*Client, error) {
	if info.Product == "" {
		info.Product = Product
	}
	if info.Protocol == "" {
		info.Protocol = Protocol
	}
	if info.DataFormat == "" {
		info.DataFormat = DataFormat
	}
	if err := validateInfo(info); err != nil {
		return nil, err
	}
	if len(secret) < 32 || strings.ContainsAny(secret, "\x00\r\n") {
		return nil, errors.New("handoff: attach secret is invalid")
	}
	return &Client{Origin: strings.TrimRight(info.Origin, "/"), Secret: secret, Generation: info.Generation, Header: "X-FreeAgent-Attach",
		HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

func NewInteractiveClient(info EngineInfo, secret string) (*Client, error) {
	client, err := NewClient(info, secret)
	if err != nil {
		return nil, err
	}
	client.Header = "X-FreeAgent-Key"
	return client, nil
}

func (client *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body *bytes.Reader
	if input == nil {
		body = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.Origin+path, body)
	if err != nil {
		return err
	}
	header := client.Header
	if header == "" {
		header = "X-FreeAgent-Attach"
	}
	request.Header.Set(header, client.Secret)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(response.Body).Decode(&detail)
		return fmt.Errorf("handoff: attach request failed with HTTP %d: %v", response.StatusCode, detail["code"])
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func (client *Client) State(ctx context.Context) (AttachState, error) {
	var state AttachState
	err := client.request(ctx, http.MethodGet, "/api/attach/state", nil, &state)
	return state, err
}

func (client *Client) Events(ctx context.Context, after uint64) (AttachEvents, error) {
	var events AttachEvents
	query := url.Values{}
	query.Set("generation", client.Generation)
	query.Set("after", fmt.Sprintf("%d", after))
	path := "/api/attach/events?" + query.Encode()
	err := client.request(ctx, http.MethodGet, path, nil, &events)
	return events, err
}

func (client *Client) Submit(ctx context.Context, conversationID, requestKey, prompt string) error {
	return client.SubmitWithOptions(ctx, conversationID, requestKey, prompt, harness.RunOptions{})
}

func (client *Client) SubmitWithOptions(ctx context.Context, conversationID, requestKey, prompt string, options harness.RunOptions) error {
	return client.request(ctx, http.MethodPost, "/api/attach/submit", struct {
		ConversationID string             `json:"conversation_id"`
		RequestKey     string             `json:"request_key"`
		Prompt         string             `json:"prompt"`
		Options        harness.RunOptions `json:"options,omitempty"`
	}{conversationID, requestKey, prompt, options}, nil)
}

func (client *Client) NewConversation(ctx context.Context, title string) (harness.Conversation, error) {
	var conversation harness.Conversation
	err := client.request(ctx, http.MethodPost, "/api/attach/conversation", map[string]string{"title": title}, &conversation)
	return conversation, err
}

func (client *Client) Status(ctx context.Context, conversationID, requestKey string) (AttachRunStatus, error) {
	var status AttachRunStatus
	err := client.request(ctx, http.MethodPost, "/api/attach/status", map[string]string{"conversation_id": conversationID, "request_key": requestKey}, &status)
	return status, err
}

func (client *Client) Chat(ctx context.Context, conversationID string) (harness.ChatView, error) {
	var view harness.ChatView
	err := client.request(ctx, http.MethodPost, "/api/attach/chat", map[string]string{"conversation_id": conversationID}, &view)
	return view, err
}

func (client *Client) Inspect(ctx context.Context, runID string) (harness.Run, []harness.Attempt, harness.Evidence, error) {
	var response struct {
		Run      harness.Run       `json:"run"`
		Attempts []harness.Attempt `json:"attempts"`
		Evidence harness.Evidence  `json:"evidence"`
	}
	err := client.request(ctx, http.MethodPost, "/api/attach/inspect", map[string]string{"run_id": runID}, &response)
	return response.Run, response.Attempts, response.Evidence, err
}

func (client *Client) Preview(ctx context.Context, runID string) (harness.Confirmation, error) {
	var confirmation harness.Confirmation
	err := client.request(ctx, http.MethodPost, "/api/attach/preview", map[string]string{"run_id": runID}, &confirmation)
	return confirmation, err
}

func (client *Client) Confirm(ctx context.Context, confirmation harness.Confirmation, approved bool) error {
	return client.request(ctx, http.MethodPost, "/api/attach/confirm", struct {
		Confirmation harness.Confirmation `json:"confirmation"`
		Approved     bool                 `json:"approved"`
	}{confirmation, approved}, nil)
}

// ConfirmControl decides a Run-bound control proposal. It requires an
// interactive client; code is the value shown by ControlConfirmation and is
// ignored when the proposal is rejected.
func (client *Client) ConfirmControl(ctx context.Context, confirmation harness.Confirmation, approved bool, code string) error {
	return client.request(ctx, http.MethodPost, "/api/attach/confirm", struct {
		Confirmation harness.Confirmation `json:"confirmation"`
		Approved     bool                 `json:"approved"`
		Code         string               `json:"code,omitempty"`
	}{confirmation, approved, code}, nil)
}

func (client *Client) Cancel(ctx context.Context, conversationID string) error {
	return client.request(ctx, http.MethodPost, "/api/attach/cancel", map[string]string{"conversation_id": conversationID}, nil)
}

func (client *Client) Continue(ctx context.Context, conversationID, runID string) error {
	return client.request(ctx, http.MethodPost, "/api/attach/continue", map[string]string{"conversation_id": conversationID, "run_id": runID}, nil)
}

// Memory executes one bounded user-selected memory operation in the owner.
// The response is decoded by the CLI into the operation's existing typed
// record; no second process opens the SQLite writer.
func (client *Client) Memory(ctx context.Context, command harness.MemoryCommand) (json.RawMessage, error) {
	var response json.RawMessage
	err := client.request(ctx, http.MethodPost, "/api/attach/memory", command, &response)
	return response, err
}

// Collaboration executes a bounded collaboration CLI action in the owner.
// Collaboration may run provider calls, so it uses a longer request timeout
// than ordinary attach state calls while retaining the same authenticated
// owner boundary.
func (client *Client) Collaboration(ctx context.Context, command harness.CollaborationCommand) (json.RawMessage, error) {
	var response json.RawMessage
	copyClient := *client
	copyClient.HTTP = &http.Client{Timeout: 15 * time.Minute}
	err := copyClient.request(ctx, http.MethodPost, "/api/attach/collaboration", command, &response)
	return response, err
}

// Handoff asks the current owner to stop accepting new work and release the
// owner lock after the active foreground Run settles. When cancelActive is
// true, the owner sends the existing cancellation signal first; unresolved
// external calls retain their normal UNKNOWN recovery semantics.
func (client *Client) Handoff(ctx context.Context, cancelActive bool) error {
	return client.request(ctx, http.MethodPost, "/api/attach/handoff", map[string]bool{"cancel_active": cancelActive}, nil)
}

func (client *Client) ControlState(ctx context.Context) (harness.ControlState, error) {
	var state harness.ControlState
	err := client.request(ctx, http.MethodGet, "/api/control/state", nil, &state)
	return state, err
}

// Config returns the owner-generated diagnostic projection. It is not a raw
// Config: the owner omits prompts, MCP arguments, environment values and
// credential material before the response leaves the core.
func (client *Client) Config(ctx context.Context) (map[string]any, error) {
	var config map[string]any
	err := client.request(ctx, http.MethodGet, "/api/control/config", nil, &config)
	return config, err
}

func (client *Client) ControlOperations(ctx context.Context) ([]harness.ControlOperation, error) {
	var operations []harness.ControlOperation
	err := client.request(ctx, http.MethodGet, "/api/control/operations", nil, &operations)
	return operations, err
}

func (client *Client) ControlPlan(ctx context.Context, kind string, arguments json.RawMessage, conversationID string) (harness.ControlOperation, error) {
	var operation harness.ControlOperation
	err := client.request(ctx, http.MethodPost, "/api/control/plan", map[string]any{"kind": kind, "arguments": arguments, "conversation_id": conversationID}, &operation)
	return operation, err
}

func (client *Client) ControlConfirmation(ctx context.Context, operationID, conversationID string) (harness.ControlConfirmation, error) {
	var confirmation harness.ControlConfirmation
	path := "/api/control/confirmation?operation_id=" + url.QueryEscape(operationID) + "&conversation_id=" + url.QueryEscape(conversationID)
	err := client.request(ctx, http.MethodGet, path, nil, &confirmation)
	return confirmation, err
}

func (client *Client) ControlConfirm(ctx context.Context, operationID, conversationID, code string) (harness.ControlResult, error) {
	var result harness.ControlResult
	err := client.request(ctx, http.MethodPost, "/api/control/confirm", map[string]string{"operation_id": operationID, "conversation_id": conversationID, "code": code}, &result)
	return result, err
}

func (client *Client) ControlReject(ctx context.Context, operationID, conversationID string) (harness.ControlResult, error) {
	var result harness.ControlResult
	err := client.request(ctx, http.MethodPost, "/api/control/reject", map[string]string{"operation_id": operationID, "conversation_id": conversationID}, &result)
	return result, err
}

func (client *Client) ControlRevertPlan(ctx context.Context, operationID, conversationID string) (harness.ControlOperation, error) {
	var operation harness.ControlOperation
	err := client.request(ctx, http.MethodPost, "/api/control/revert-plan", map[string]string{"operation_id": operationID, "conversation_id": conversationID}, &operation)
	return operation, err
}

// ControlKeyMutation sends only to the interactive owner. The secret is a
// short-lived request field and is cleared from the caller's buffer before
// returning; the owner keeps it only until its typed operation is applied.
func (client *Client) ControlKeyMutation(ctx context.Context, request harness.KeyControlRequest) (harness.ControlResult, error) {
	secretBytes := append([]byte(nil), request.Input.Secret...)
	clear(request.Input.Secret)
	defer clear(secretBytes)
	secret := string(secretBytes)
	var result harness.ControlResult
	err := client.request(ctx, http.MethodPost, "/api/control/key-plan", struct {
		Ref           string `json:"ref"`
		Remove        bool   `json:"remove"`
		Force         bool   `json:"force"`
		DisableModels bool   `json:"disable_models"`
		NewDefault    string `json:"new_default,omitempty"`
		Name          string `json:"name"`
		BaseURL       string `json:"base_url"`
		Protocol      string `json:"protocol"`
		Note          string `json:"note"`
		Source        string `json:"source"`
		EnvName       string `json:"env_name"`
		Secret        string `json:"secret,omitempty"`
	}{
		Ref: request.Ref, Remove: request.Remove, Force: request.Force, DisableModels: request.DisableModels,
		NewDefault: request.NewDefault, Name: request.Input.Name, BaseURL: request.Input.BaseURL,
		Protocol: request.Input.Protocol, Note: request.Input.Note, Source: request.Input.Source,
		EnvName: request.Input.EnvName, Secret: secret,
	}, &result)
	return result, err
}

func (client *Client) ControlKeyProbe(ctx context.Context, ref string) (harness.ProbeResult, error) {
	var result harness.ProbeResult
	err := client.request(ctx, http.MethodPost, "/api/control/key-probe", map[string]string{"ref": ref}, &result)
	return result, err
}

func (client *Client) ControlMCPReentry(ctx context.Context, operationID, profileID string, config harness.MCPServerConfig) (harness.ControlOperation, error) {
	var operation harness.ControlOperation
	err := client.request(ctx, http.MethodPost, "/api/control/mcp-reentry", struct {
		OperationID string                  `json:"operation_id"`
		ProfileID   string                  `json:"profile_id"`
		Config      harness.MCPServerConfig `json:"config"`
	}{OperationID: operationID, ProfileID: profileID, Config: config}, &operation)
	return operation, err
}
