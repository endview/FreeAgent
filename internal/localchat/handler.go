package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/currentstore"
)

const (
	maxHTTPMessageBytes = 64 << 10
	maxHTTPRequestBytes = 128 << 10
)

// ChatUseCase is the complete application authority available to the local
// HTTP adapter. Implementing HTTP does not grant access to the Store, Loop,
// modules, Channels, or provider adapters.
type ChatUseCase interface {
	Chat(context.Context, ChatInput) (ChatResult, error)
	CreateConversation(
		context.Context,
		ConversationCreateInput,
	) (ConversationCreateResult, error)
}

// Handler exposes only the synchronous local health, Conversation creation,
// and Pure Chat routes.
// The listener address must also pass ValidateLoopbackAddress at composition.
type Handler struct {
	chat ChatUseCase
}

func New(chat ChatUseCase) (*Handler, error) {
	if isNilChatDependency(chat) {
		return nil, errors.New("localchat: ChatService is required")
	}
	return &Handler{chat: chat}, nil
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !isLoopbackRemote(request.RemoteAddr) {
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "local HTTP is available only over loopback",
		})
		return
	}

	switch request.URL.Path {
	case "/healthz":
		if request.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
	case "/v1/chat":
		if request.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		handler.postChat(w, request)
	case "/v1/conversations":
		if request.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		handler.postConversation(w, request)
	default:
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	}
}

type chatHTTPRequest struct {
	Tenant               string
	Principal            string
	Workspace            string
	Agent                string
	Profile              string
	Message              string
	RequestID            string
	Deadline             string
	ConversationID       string
	ConversationRevision uint64
	ConversationHeadRun  string
}

type chatHTTPResponse struct {
	RequestID            string `json:"request_id"`
	Deadline             string `json:"deadline"`
	RunID                string `json:"run_id"`
	ConversationID       string `json:"conversation_id,omitempty"`
	ConversationRevision uint64 `json:"conversation_revision,omitempty"`
	Disposition          string `json:"disposition"`
	Reason               string `json:"reason"`
	Reply                string `json:"reply"`
	Failure              string `json:"failure"`
}

type conversationCreateHTTPRequest struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
}

type conversationCreateHTTPResponse struct {
	ConversationID string `json:"conversation_id"`
	TenantID       string `json:"tenant_id"`
	PrincipalID    string `json:"principal_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	ProfileID      string `json:"profile_id"`
	HeadRunID      string `json:"head_run_id,omitempty"`
	Revision       uint64 `json:"revision"`
	Created        bool   `json:"created"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type healthResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (handler *Handler) postChat(w http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeInvalid(w, "Content-Type must be application/json")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(
		w,
		request.Body,
		maxHTTPRequestBytes,
	))
	if err != nil {
		writeInvalid(w, "request body is invalid or too large")
		return
	}
	decoded, err := decodeChatHTTPRequest(payload)
	if err != nil {
		writeInvalid(w, err.Error())
		return
	}
	if !utf8.ValidString(decoded.Message) ||
		strings.TrimSpace(decoded.Message) == "" ||
		len(decoded.Message) > maxHTTPMessageBytes {
		writeInvalid(w, "message must contain between 1 and 65536 UTF-8 bytes")
		return
	}
	var deadline time.Time
	if decoded.RequestID == "" || decoded.Deadline == "" {
		if decoded.RequestID != "" || decoded.Deadline != "" {
			writeInvalid(w, "request_id and deadline must be provided together")
			return
		}
	} else {
		deadline, err = time.Parse(time.RFC3339Nano, decoded.Deadline)
		if err != nil {
			writeInvalid(w, "deadline must be an RFC3339 timestamp")
			return
		}
	}

	result, err := handler.chat.Chat(request.Context(), ChatInput{
		TenantID:                     decoded.Tenant,
		PrincipalID:                  decoded.Principal,
		WorkspaceID:                  decoded.Workspace,
		AgentID:                      decoded.Agent,
		ProfileID:                    decoded.Profile,
		Message:                      decoded.Message,
		RequestID:                    decoded.RequestID,
		Deadline:                     deadline,
		ConversationID:               decoded.ConversationID,
		ExpectedConversationRevision: decoded.ConversationRevision,
		ExpectedHeadRunID:            decoded.ConversationHeadRun,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidChat):
			writeInvalid(w, "invalid chat request")
		case errors.Is(err, currentstore.ErrConversationNotFound):
			writeJSON(w, http.StatusNotFound, errorResponse{
				Error: "conversation not found",
			})
		case errors.Is(err, currentstore.ErrConversationConflict):
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: "conversation head or scope conflicts with this turn",
			})
		case errors.Is(err, currentstore.ErrAdmissionConflict):
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: "request_id conflicts with its stable intent",
			})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{
				Error: "chat failed",
			})
		}
		return
	}

	writeJSON(w, http.StatusOK, chatHTTPResponse{
		RequestID:            result.RequestID,
		Deadline:             result.Deadline.UTC().Format(time.RFC3339Nano),
		RunID:                result.RunID,
		ConversationID:       result.ConversationID,
		ConversationRevision: result.ConversationRevision,
		Disposition:          string(result.LoopResult.Disposition),
		Reason:               result.LoopResult.ReasonCode,
		Reply:                result.Reply,
		Failure:              result.FailureCode,
	})
}

func (handler *Handler) postConversation(
	w http.ResponseWriter,
	request *http.Request,
) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeInvalid(w, "Content-Type must be application/json")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(
		w,
		request.Body,
		maxHTTPRequestBytes,
	))
	if err != nil {
		writeInvalid(w, "request body is invalid or too large")
		return
	}
	decoded, err := decodeConversationCreateHTTPRequest(payload)
	if err != nil {
		writeInvalid(w, err.Error())
		return
	}

	result, err := handler.chat.CreateConversation(
		request.Context(),
		ConversationCreateInput{
			ConversationID: decoded.ConversationID,
			TenantID:       decoded.TenantID,
			PrincipalID:    decoded.PrincipalID,
			WorkspaceID:    decoded.WorkspaceID,
			AgentID:        decoded.AgentID,
			ProfileID:      decoded.ProfileID,
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, currentstore.ErrInvalidConversation):
			writeInvalid(w, "invalid conversation request")
		case errors.Is(err, currentstore.ErrConversationConflict):
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: "conversation ID belongs to another scope",
			})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{
				Error: "conversation create failed",
			})
		}
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, conversationCreateHTTPResponse{
		ConversationID: result.ConversationID,
		TenantID:       result.TenantID,
		PrincipalID:    result.PrincipalID,
		WorkspaceID:    result.WorkspaceID,
		AgentID:        result.AgentID,
		ProfileID:      result.ProfileID,
		HeadRunID:      result.HeadRunID,
		Revision:       result.Revision,
		Created:        result.Created,
		CreatedAt:      result.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      result.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func decodeChatHTTPRequest(payload []byte) (chatHTTPRequest, error) {
	if len(payload) == 0 {
		return chatHTTPRequest{}, errors.New("request body is empty")
	}
	if !utf8.Valid(payload) {
		return chatHTTPRequest{}, errors.New("request body must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return chatHTTPRequest{}, errors.New("request body must be a JSON object")
	}

	var decoded chatHTTPRequest
	seen := make(map[string]struct{}, 8)
	for decoder.More() {
		rawField, err := decoder.Token()
		if err != nil {
			return chatHTTPRequest{}, errors.New("invalid JSON request")
		}
		field, ok := rawField.(string)
		if !ok {
			return chatHTTPRequest{}, errors.New("invalid JSON request")
		}
		if _, duplicate := seen[field]; duplicate {
			return chatHTTPRequest{}, fmt.Errorf("duplicate JSON field %q", field)
		}
		seen[field] = struct{}{}

		var target *string
		switch field {
		case "tenant":
			target = &decoded.Tenant
		case "principal":
			target = &decoded.Principal
		case "workspace":
			target = &decoded.Workspace
		case "agent":
			target = &decoded.Agent
		case "profile":
			target = &decoded.Profile
		case "message":
			target = &decoded.Message
		case "request_id":
			target = &decoded.RequestID
		case "deadline":
			target = &decoded.Deadline
		case "conversation_id":
			target = &decoded.ConversationID
		case "conversation_revision":
			if err := decoder.Decode(&decoded.ConversationRevision); err != nil {
				return chatHTTPRequest{}, fmt.Errorf(
					"field %q must be an unsigned integer",
					field,
				)
			}
			continue
		case "conversation_head_run_id":
			target = &decoded.ConversationHeadRun
		default:
			return chatHTTPRequest{}, fmt.Errorf("unknown JSON field %q", field)
		}
		if err := decoder.Decode(target); err != nil {
			return chatHTTPRequest{}, fmt.Errorf("field %q must be a string", field)
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return chatHTTPRequest{}, errors.New("invalid JSON request")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return chatHTTPRequest{}, errors.New("request contains trailing JSON")
	}

	for name, value := range map[string]string{
		"tenant":    decoded.Tenant,
		"principal": decoded.Principal,
		"workspace": decoded.Workspace,
		"agent":     decoded.Agent,
		"profile":   decoded.Profile,
		"message":   decoded.Message,
	} {
		if _, present := seen[name]; !present || value == "" {
			return chatHTTPRequest{}, fmt.Errorf("field %q is required", name)
		}
	}
	return decoded, nil
}

func decodeConversationCreateHTTPRequest(
	payload []byte,
) (conversationCreateHTTPRequest, error) {
	if len(payload) == 0 {
		return conversationCreateHTTPRequest{}, errors.New("request body is empty")
	}
	if !utf8.Valid(payload) {
		return conversationCreateHTTPRequest{}, errors.New(
			"request body must be valid UTF-8",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return conversationCreateHTTPRequest{}, errors.New(
			"request body must be a JSON object",
		)
	}

	var decoded conversationCreateHTTPRequest
	seen := make(map[string]struct{}, 6)
	for decoder.More() {
		rawField, err := decoder.Token()
		if err != nil {
			return conversationCreateHTTPRequest{}, errors.New("invalid JSON request")
		}
		field, ok := rawField.(string)
		if !ok {
			return conversationCreateHTTPRequest{}, errors.New("invalid JSON request")
		}
		if _, duplicate := seen[field]; duplicate {
			return conversationCreateHTTPRequest{}, fmt.Errorf(
				"duplicate JSON field %q",
				field,
			)
		}
		seen[field] = struct{}{}

		var target *string
		switch field {
		case "conversation_id":
			target = &decoded.ConversationID
		case "tenant":
			target = &decoded.TenantID
		case "principal":
			target = &decoded.PrincipalID
		case "workspace":
			target = &decoded.WorkspaceID
		case "agent":
			target = &decoded.AgentID
		case "profile":
			target = &decoded.ProfileID
		default:
			return conversationCreateHTTPRequest{}, fmt.Errorf(
				"unknown JSON field %q",
				field,
			)
		}
		if err := decoder.Decode(target); err != nil {
			return conversationCreateHTTPRequest{}, fmt.Errorf(
				"field %q must be a string",
				field,
			)
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return conversationCreateHTTPRequest{}, errors.New("invalid JSON request")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return conversationCreateHTTPRequest{}, errors.New(
			"request contains trailing JSON",
		)
	}

	for name, value := range map[string]string{
		"conversation_id": decoded.ConversationID,
		"tenant":          decoded.TenantID,
		"principal":       decoded.PrincipalID,
		"workspace":       decoded.WorkspaceID,
		"agent":           decoded.AgentID,
		"profile":         decoded.ProfileID,
	} {
		if _, present := seen[name]; !present || value == "" {
			return conversationCreateHTTPRequest{}, fmt.Errorf(
				"field %q is required",
				name,
			)
		}
	}
	return decoded, nil
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
		Error: "method not allowed",
	})
}

func writeInvalid(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: message})
}

func isLoopbackRemote(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if index := strings.LastIndexByte(host, '%'); index >= 0 {
		host = host[:index]
	}
	return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

// ValidateLoopbackAddress rejects wildcard and externally reachable binds.
// Composition must call it before listening; ServeHTTP independently validates
// RemoteAddr so neither proxy headers nor a configuration mistake expand S1.
func ValidateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf(
			"listen address must include an explicit loopback host and port: %w",
			err,
		)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("local HTTP requires a loopback listen address")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
