package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
)

type recordingChatUseCase struct {
	result             ChatResult
	err                error
	calls              int
	input              ChatInput
	conversationResult ConversationCreateResult
	conversationErr    error
	conversationCalls  int
	conversationInput  ConversationCreateInput
}

func (chat *recordingChatUseCase) Chat(
	_ context.Context,
	input ChatInput,
) (ChatResult, error) {
	chat.calls++
	chat.input = input
	return chat.result, chat.err
}

func (chat *recordingChatUseCase) CreateConversation(
	_ context.Context,
	input ConversationCreateInput,
) (ConversationCreateResult, error) {
	chat.conversationCalls++
	chat.conversationInput = input
	return chat.conversationResult, chat.conversationErr
}

func newTestHandler(t *testing.T, chat *recordingChatUseCase) *Handler {
	t.Helper()
	handler, err := New(chat)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func localRequest(method string, target string, body []byte) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:32100"
	return request
}

func validChatBody() string {
	return `{
		"tenant":"tenant-1",
		"principal":"principal-1",
		"workspace":"workspace-1",
		"agent":"agent-1",
		"profile":"profile-1",
		"message":"hello world",
		"request_id":"request-1",
		"deadline":"2030-01-02T03:04:05.123456789Z"
	}`
}

func TestHandlerExposesOnlyLoopbackHealthAndChat(t *testing.T) {
	chat := &recordingChatUseCase{}
	handler := newTestHandler(t, chat)

	health := httptest.NewRecorder()
	handler.ServeHTTP(
		health,
		localRequest(http.MethodGet, "/healthz", nil),
	)
	if health.Code != http.StatusOK || health.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	if got := health.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("health cache-control=%q", got)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		remote     string
		wantStatus int
		wantAllow  string
	}{
		{name: "health method", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
		{name: "chat method", method: http.MethodGet, path: "/v1/chat", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
		{name: "conversation method", method: http.MethodGet, path: "/v1/conversations", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
		{name: "unknown", method: http.MethodGet, path: "/other", wantStatus: http.StatusNotFound},
		{name: "old chat route", method: http.MethodPost, path: "/v1/dev/chat", wantStatus: http.StatusNotFound},
		{name: "old polling route", method: http.MethodGet, path: "/v1/dev/tasks/task-1", wantStatus: http.StatusNotFound},
		{name: "remote health", method: http.MethodGet, path: "/healthz", remote: "192.0.2.10:32100", wantStatus: http.StatusForbidden},
		{name: "remote unknown", method: http.MethodGet, path: "/other", remote: "192.0.2.10:32100", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := localRequest(test.method, test.path, nil)
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Fatalf("Allow=%q want=%q", got, test.wantAllow)
			}
		})
	}
	if chat.calls != 0 {
		t.Fatalf("non-POST-chat routes reached service %d times", chat.calls)
	}
	if chat.conversationCalls != 0 {
		t.Fatalf("non-POST-Conversation routes reached service %d times", chat.conversationCalls)
	}
}

func TestPostConversationCreatesAndExactlyReusesConversation(t *testing.T) {
	createdAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	chat := &recordingChatUseCase{conversationResult: ConversationCreateResult{
		ConversationID: "conversation-http-1",
		TenantID:       "tenant-1",
		PrincipalID:    "principal-1",
		WorkspaceID:    "workspace-1",
		AgentID:        "agent-1",
		ProfileID:      "profile-1",
		Revision:       0,
		Created:        true,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}}
	handler := newTestHandler(t, chat)
	body := []byte(`{
		"conversation_id":"conversation-http-1",
		"tenant":"tenant-1",
		"principal":"principal-1",
		"workspace":"workspace-1",
		"agent":"agent-1",
		"profile":"profile-1"
	}`)
	request := localRequest(http.MethodPost, "/v1/conversations", body)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	wantInput := ConversationCreateInput{
		ConversationID: "conversation-http-1",
		TenantID:       "tenant-1",
		PrincipalID:    "principal-1",
		WorkspaceID:    "workspace-1",
		AgentID:        "agent-1",
		ProfileID:      "profile-1",
	}
	if chat.conversationCalls != 1 || chat.conversationInput != wantInput {
		t.Fatalf("Conversation calls=%d input=%+v want=%+v", chat.conversationCalls, chat.conversationInput, wantInput)
	}
	for _, fragment := range []string{
		`"conversation_id":"conversation-http-1"`,
		`"revision":0`,
		`"created":true`,
		`"created_at":"2030-01-02T03:04:05Z"`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("create response missing %q: %s", fragment, response.Body.String())
		}
	}

	chat.conversationResult.Created = false
	retry := httptest.NewRecorder()
	retryRequest := localRequest(http.MethodPost, "/v1/conversations", body)
	retryRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(retry, retryRequest)
	if retry.Code != http.StatusOK ||
		!strings.Contains(retry.Body.String(), `"created":false`) {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestPostConversationRejectsMalformedAndMapsErrors(t *testing.T) {
	valid := `{"conversation_id":"conversation-http-1","tenant":"tenant-1","principal":"principal-1","workspace":"workspace-1","agent":"agent-1","profile":"profile-1"}`
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown", body: strings.Replace(valid, `"profile":`, `"unknown":"x","profile":`, 1)},
		{name: "duplicate", body: strings.Replace(valid, `"tenant":`, `"tenant":"other","tenant":`, 1)},
		{name: "missing", body: strings.Replace(valid, `"agent":"agent-1",`, "", 1)},
		{name: "wrong type", body: strings.Replace(valid, `"profile":"profile-1"`, `"profile":1`, 1)},
		{name: "trailing", body: valid + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			chat := &recordingChatUseCase{}
			handler := newTestHandler(t, chat)
			request := localRequest(http.MethodPost, "/v1/conversations", []byte(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || chat.conversationCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, chat.conversationCalls, response.Body.String())
			}
		})
	}

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "invalid", err: fmtError(currentstore.ErrInvalidConversation, "sensitive value"), wantStatus: http.StatusBadRequest, wantError: "invalid conversation request"},
		{name: "conflict", err: fmtError(currentstore.ErrConversationConflict, "sensitive scope"), wantStatus: http.StatusConflict, wantError: "conversation ID belongs to another scope"},
		{name: "internal", err: errors.New("sensitive path"), wantStatus: http.StatusInternalServerError, wantError: "conversation create failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			chat := &recordingChatUseCase{conversationErr: test.err}
			handler := newTestHandler(t, chat)
			request := localRequest(http.MethodPost, "/v1/conversations", []byte(valid))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error != test.wantError || strings.Contains(response.Body.String(), "sensitive") {
				t.Fatalf("error response=%s want=%q", response.Body.String(), test.wantError)
			}
		})
	}
}

func TestPostChatCallsServiceAndReturnsCompleteSynchronousResult(t *testing.T) {
	adoptedDeadline := time.Date(
		2030, time.January, 2, 3, 4, 5, 123456789, time.UTC,
	)
	chat := &recordingChatUseCase{result: ChatResult{
		RequestID: "request-1",
		Deadline:  adoptedDeadline,
		RunID:     "run-1",
		LoopResult: loopapi.RunResult{
			RunID:         "run-1",
			Disposition:   loopapi.DispositionTerminated,
			FrameRevision: 3,
			ReasonCode:    "MODEL_SUCCEEDED",
		},
		Reply: "answer",
	}}
	handler := newTestHandler(t, chat)
	request := localRequest(
		http.MethodPost,
		"/v1/chat",
		[]byte(validChatBody()),
	)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if chat.calls != 1 {
		t.Fatalf("Chat calls=%d", chat.calls)
	}
	wantInput := ChatInput{
		TenantID:    "tenant-1",
		PrincipalID: "principal-1",
		WorkspaceID: "workspace-1",
		AgentID:     "agent-1",
		ProfileID:   "profile-1",
		Message:     "hello world",
		RequestID:   "request-1",
		Deadline:    adoptedDeadline,
	}
	if chat.input != wantInput {
		t.Fatalf("Chat input=%+v want=%+v", chat.input, wantInput)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"request_id":  "request-1",
		"deadline":    "2030-01-02T03:04:05.123456789Z",
		"run_id":      "run-1",
		"disposition": "TERMINATED",
		"reason":      "MODEL_SUCCEEDED",
		"reply":       "answer",
		"failure":     "",
	}
	if len(body) != len(want) {
		t.Fatalf("response fields=%#v", body)
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("response[%q]=%q want=%q", key, body[key], value)
		}
	}
}

func TestPostChatCanDelegateAutomaticRequestIdentity(t *testing.T) {
	adoptedDeadline := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
	chat := &recordingChatUseCase{result: ChatResult{
		RequestID: "request-generated",
		Deadline:  adoptedDeadline,
		RunID:     "run-generated",
		LoopResult: loopapi.RunResult{
			RunID:         "run-generated",
			Disposition:   loopapi.DispositionTerminated,
			FrameRevision: 3,
			ReasonCode:    "MODEL_SUCCEEDED",
		},
		Reply: "generated answer",
	}}
	handler := newTestHandler(t, chat)
	body := `{"tenant":"tenant-1","principal":"principal-1","workspace":"workspace-1","agent":"agent-1","profile":"profile-1","message":"hello"}`
	request := localRequest(http.MethodPost, "/v1/chat", []byte(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if chat.calls != 1 || chat.input.RequestID != "" || !chat.input.Deadline.IsZero() {
		t.Fatalf("calls=%d input=%+v", chat.calls, chat.input)
	}
	for _, fragment := range []string{
		`"request_id":"request-generated"`,
		`"deadline":"2030-02-03T04:05:06Z"`,
		`"run_id":"run-generated"`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("response missing %q: %s", fragment, response.Body.String())
		}
	}
}

func TestPostChatCarriesExactConversationCASAndReturnsNewRevision(t *testing.T) {
	deadline := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	chat := &recordingChatUseCase{result: ChatResult{
		RequestID:            "request-conversation-2",
		Deadline:             deadline,
		RunID:                "run-conversation-2",
		ConversationID:       "conversation-1",
		ConversationRevision: 2,
		LoopResult: loopapi.RunResult{
			RunID:       "run-conversation-2",
			Disposition: loopapi.DispositionTerminated,
			ReasonCode:  "MODEL_SUCCEEDED",
		},
		Reply: "second answer",
	}}
	handler := newTestHandler(t, chat)
	body := `{
		"tenant":"tenant-1",
		"principal":"principal-1",
		"workspace":"workspace-1",
		"agent":"agent-1",
		"profile":"profile-1",
		"message":"second turn",
		"request_id":"request-conversation-2",
		"deadline":"2030-01-02T03:04:05Z",
		"conversation_id":"conversation-1",
		"conversation_revision":1,
		"conversation_head_run_id":"run-conversation-1"
	}`
	request := localRequest(http.MethodPost, "/v1/chat", []byte(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if chat.input.ConversationID != "conversation-1" ||
		chat.input.ExpectedConversationRevision != 1 ||
		chat.input.ExpectedHeadRunID != "run-conversation-1" {
		t.Fatalf("Conversation input=%+v", chat.input)
	}
	for _, fragment := range []string{
		`"conversation_id":"conversation-1"`,
		`"conversation_revision":2`,
		`"reply":"second answer"`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("response missing %q: %s", fragment, response.Body.String())
		}
	}
}

func TestPostChatReturnsWaitingUnchangedWithHTTP200(t *testing.T) {
	chat := &recordingChatUseCase{result: ChatResult{
		RequestID: "request-1",
		Deadline:  time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		RunID:     "run-1",
		LoopResult: loopapi.RunResult{
			RunID:       "run-1",
			Disposition: loopapi.DispositionWaitingReconciliation,
			ReasonCode:  "MODEL_OUTCOME_UNKNOWN",
		},
	}}
	handler := newTestHandler(t, chat)
	request := localRequest(http.MethodPost, "/v1/chat", []byte(validChatBody()))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, fragment := range []string{
		`"disposition":"WAITING_RECONCILIATION"`,
		`"reason":"MODEL_OUTCOME_UNKNOWN"`,
		`"reply":""`,
		`"failure":""`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("response missing %q: %s", fragment, response.Body.String())
		}
	}
}

func TestPostChatRejectsMalformedInputBeforeService(t *testing.T) {
	valid := validChatBody()
	credentialLikeField := `"api_` + `key":"not-a-secret","message":`
	tests := []struct {
		name        string
		body        []byte
		contentType string
		remote      string
	}{
		{name: "remote", body: []byte(valid), contentType: "application/json", remote: "192.0.2.10:1234"},
		{name: "content type", body: []byte(valid), contentType: "text/plain"},
		{name: "empty", body: nil, contentType: "application/json"},
		{name: "not object", body: []byte(`[]`), contentType: "application/json"},
		{name: "unknown", body: []byte(strings.Replace(valid, `"message":`, `"unexpected":"value","message":`, 1)), contentType: "application/json"},
		{name: "credential-like unknown", body: []byte(strings.Replace(valid, `"message":`, credentialLikeField, 1)), contentType: "application/json"},
		{name: "duplicate", body: []byte(strings.Replace(valid, `"message":`, `"message":"first","message":`, 1)), contentType: "application/json"},
		{name: "trailing", body: []byte(valid + `{}`), contentType: "application/json"},
		{name: "wrong type", body: []byte(strings.Replace(valid, `"tenant":"tenant-1"`, `"tenant":1`, 1)), contentType: "application/json"},
		{name: "missing", body: []byte(strings.Replace(valid, `"profile":"profile-1",`, ``, 1)), contentType: "application/json"},
		{name: "empty field", body: []byte(strings.Replace(valid, `"agent":"agent-1"`, `"agent":""`, 1)), contentType: "application/json"},
		{name: "blank message", body: []byte(strings.Replace(valid, `"hello world"`, `"  "`, 1)), contentType: "application/json"},
		{name: "bad deadline", body: []byte(strings.Replace(valid, `2030-01-02T03:04:05.123456789Z`, `tomorrow`, 1)), contentType: "application/json"},
		{name: "request id without deadline", body: []byte(`{"tenant":"t","principal":"p","workspace":"w","agent":"a","profile":"p","message":"m","request_id":"r"}`), contentType: "application/json"},
		{name: "deadline without request id", body: []byte(`{"tenant":"t","principal":"p","workspace":"w","agent":"a","profile":"p","message":"m","deadline":"2030-01-02T03:04:05Z"}`), contentType: "application/json"},
		{name: "invalid utf8", body: append([]byte(`{"tenant":"`), 0xff), contentType: "application/json"},
		{name: "oversized message", body: []byte(strings.Replace(valid, `hello world`, strings.Repeat("x", maxHTTPMessageBytes+1), 1)), contentType: "application/json"},
		{name: "oversized request", body: []byte(strings.Repeat(" ", maxHTTPRequestBytes+1)), contentType: "application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chat := &recordingChatUseCase{}
			handler := newTestHandler(t, chat)
			request := localRequest(http.MethodPost, "/v1/chat", test.body)
			request.Header.Set("Content-Type", test.contentType)
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			wantStatus := http.StatusBadRequest
			if test.remote != "" {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
			}
			if chat.calls != 0 {
				t.Fatalf("invalid request reached Chat %d times", chat.calls)
			}
			if strings.Contains(response.Body.String(), "value") {
				t.Fatalf("input value leaked: %s", response.Body.String())
			}
		})
	}
}

func TestPostChatMapsOnlyMinimalErrorClasses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "invalid", err: fmtError(ErrInvalidChat, "sensitive invalid detail"), wantStatus: http.StatusBadRequest, wantError: "invalid chat request"},
		{name: "conversation missing", err: fmtError(currentstore.ErrConversationNotFound, "sensitive id"), wantStatus: http.StatusNotFound, wantError: "conversation not found"},
		{name: "conversation conflict", err: errors.Join(currentstore.ErrConversationConflict, currentstore.ErrAdmissionConflict), wantStatus: http.StatusConflict, wantError: "conversation head or scope conflicts with this turn"},
		{name: "intent conflict", err: fmtError(currentstore.ErrAdmissionConflict, "sensitive digest"), wantStatus: http.StatusConflict, wantError: "request_id conflicts with its stable intent"},
		{name: "internal", err: errors.New("sensitive database path"), wantStatus: http.StatusInternalServerError, wantError: "chat failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chat := &recordingChatUseCase{err: test.err}
			handler := newTestHandler(t, chat)
			request := localRequest(http.MethodPost, "/v1/chat", []byte(validChatBody()))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error != test.wantError {
				t.Fatalf("error=%q want=%q", body.Error, test.wantError)
			}
			if strings.Contains(response.Body.String(), "sensitive") {
				t.Fatalf("internal error leaked: %s", response.Body.String())
			}
		})
	}
}

func TestNewRejectsNilChatUseCase(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New accepted nil ChatUseCase")
	}
	var typedNil *recordingChatUseCase
	if _, err := New(typedNil); err == nil {
		t.Fatal("New accepted typed-nil ChatUseCase")
	}
}

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1:8080",
		"[::1]:8080",
	} {
		if err := ValidateLoopbackAddress(address); err != nil {
			t.Errorf("address %q rejected: %v", address, err)
		}
	}
	for _, address := range []string{
		":8080",
		"0.0.0.0:8080",
		"[::]:8080",
		"192.0.2.1:8080",
		"127.0.0.1",
		"localhost:8080",
	} {
		if err := ValidateLoopbackAddress(address); err == nil {
			t.Errorf("address %q accepted", address)
		}
	}
}

func fmtError(sentinel error, detail string) error {
	return errors.Join(sentinel, errors.New(detail))
}
