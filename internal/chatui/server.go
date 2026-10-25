package chatui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

type Stream struct {
	Attempt     string `json:"attempt"`
	Run         string `json:"run"`
	Raw         string `json:"raw"`
	Interrupted bool   `json:"interrupted"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// Server owns only transport state; TaskService owns execution and persistence.
type Server struct {
	tasks         *harness.TaskService
	origin        string
	host          string
	token         string
	attachToken   string
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	active        string
	activeBinding string
	activeCancel  context.CancelFunc
	stopping      bool
	closed        bool
	streams       map[string]Stream
	listeners     map[chan []byte]bool
	generation    string
	eventSeq      uint64
	eventLog      []harness.Event
	wg            sync.WaitGroup
	stopRequested chan struct{}
	stopOnce      sync.Once
	ownerInfo     handoff.EngineInfo
}

func NewToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}

func NewServer(ctx context.Context, tasks *harness.TaskService, origin, token string) (*Server, error) {
	return newServer(ctx, tasks, origin, token, token)
}

func NewServerWithAttach(ctx context.Context, tasks *harness.TaskService, origin, token, attachToken string) (*Server, error) {
	return newServer(ctx, tasks, origin, token, attachToken)
}

func newServer(ctx context.Context, tasks *harness.TaskService, origin, token, attachToken string) (*Server, error) {
	address, err := url.Parse(origin)
	if err != nil || address.Scheme != "http" || address.Path != "" || address.RawQuery != "" ||
		address.User != nil || address.Fragment != "" || len(token) < 32 || len(attachToken) < 32 || ctx == nil || tasks == nil {
		return nil, errors.New("chat: explicit loopback origin, token, tasks and context are required")
	}
	ip := net.ParseIP(address.Hostname())
	if ip == nil || !ip.IsLoopback() || address.Port() == "" {
		return nil, errors.New("chat: only numeric loopback listeners are supported")
	}
	lifetime, cancel := context.WithCancel(ctx)
	server := &Server{tasks: tasks, origin: origin, host: address.Host, token: token, attachToken: attachToken,
		ctx: lifetime, cancel: cancel, streams: map[string]Stream{}, listeners: map[chan []byte]bool{},
		stopRequested: make(chan struct{})}
	server.generation = NewToken()
	tasks.SetApproval(nil)
	tasks.SetEvents(server.onEvent)
	return server, nil
}

func (server *Server) SetGeneration(generation string) {
	if strings.TrimSpace(generation) == "" {
		return
	}
	server.mu.Lock()
	server.generation = generation
	server.eventSeq = 0
	server.eventLog = nil
	server.mu.Unlock()
}

// SetOwnerInfo adds discovery metadata to attach state without exposing either
// capability secret. It is intentionally kept as transport state; the owner
// lock and persisted Harness records remain authoritative.
func (server *Server) SetOwnerInfo(info handoff.EngineInfo) {
	server.mu.Lock()
	server.ownerInfo = info
	server.mu.Unlock()
}

// StopRequested is closed when an attached client has requested a graceful
// owner handoff. The process that owns the Store performs final shutdown and
// releases owner.lock.
func (server *Server) StopRequested() <-chan struct{} { return server.stopRequested }

func (server *Server) requestStopLocked() {
	server.stopOnce.Do(func() { close(server.stopRequested) })
}

func (server *Server) Close() {
	server.mu.Lock()
	server.closed = true
	if server.activeCancel != nil {
		server.activeCancel()
	}
	server.mu.Unlock()
	server.cancel()
	server.tasks.Cancel()
	server.wg.Wait()
}

func (server *Server) onEvent(event harness.Event) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.eventSeq++
	event.Seq = server.eventSeq
	event.Generation = server.generation
	server.eventLog = append(server.eventLog, event)
	if len(server.eventLog) > 256 {
		server.eventLog = server.eventLog[len(server.eventLog)-256:]
	}
	if event.Purpose == "" && event.AttemptID != "" {
		key := event.AttemptID
		stream := server.streams[key]
		switch event.Kind {
		case "model_started":
			// One foreground caller; completed streams are represented by messages.
			server.streams = map[string]Stream{key: {Attempt: key, Run: event.RunID}}
		case "model_text":
			if len(stream.Raw)+len(event.Message) <= 1<<20 {
				stream.Raw += event.Message
			} else {
				stream.Truncated = true
			}
			server.streams[key] = stream
		case "model_finished":
			delete(server.streams, key)
		case "model_interrupted":
			stream.Interrupted = true
			server.streams[key] = stream
		}
	}
	if len(event.Message) > 64<<10 {
		event.Message, event.Kind = "", "refresh"
	}
	raw, _ := json.Marshal(event)
	server.broadcastLocked(raw)
}

func (server *Server) broadcastLocked(raw []byte) {
	for listener := range server.listeners {
		select {
		case listener <- raw:
		default:
			// Backpressure closes this stream. Reconnect reads a snapshot.
			close(listener)
			delete(server.listeners, listener)
		}
	}
}

func (server *Server) notify(kind, chat, message string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.eventSeq++
	event := harness.Event{Seq: server.eventSeq, Generation: server.generation, Kind: kind, ConversationID: chat, Message: message}
	server.eventLog = append(server.eventLog, event)
	if len(server.eventLog) > 256 {
		server.eventLog = server.eventLog[len(server.eventLog)-256:]
	}
	raw, _ := json.Marshal(event)
	server.broadcastLocked(raw)
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	header := writer.Header()
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if request.Host != server.host || request.Header.Get("Origin") != "" && request.Header.Get("Origin") != server.origin ||
		request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		server.writeError(writer, http.StatusForbidden, "origin", "请从本地 Chat 地址打开页面。")
		return
	}
	path := request.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		http.FileServerFS(Assets()).ServeHTTP(writer, request)
		return
	}
	key := request.Header.Get("X-FreeAgent-Key")
	attachKey := request.Header.Get("X-FreeAgent-Attach")
	if request.Method == http.MethodGet && key == "" {
		if cookie, err := request.Cookie("freeagent-chat"); err == nil {
			key = cookie.Value
		}
	}
	keyOK := subtle.ConstantTimeCompare([]byte(key), []byte(server.token)) == 1
	attachOK := subtle.ConstantTimeCompare([]byte(attachKey), []byte(server.attachToken)) == 1
	if !keyOK && !attachOK {
		server.writeError(writer, http.StatusUnauthorized, "auth", "需要本次启动的 Chat 地址。")
		return
	}
	if attachOK && !keyOK && (request.URL.Path == "/api/control/plan" || request.URL.Path == "/api/control/confirm" || request.URL.Path == "/api/control/reject" || request.URL.Path == "/api/control/revert-plan") {
		server.writeError(writer, http.StatusForbidden, "user_interaction_required", "需要当前用户界面的交互确认。")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodPatch {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if path == "/api/connect" && request.Method == http.MethodPost {
		http.SetCookie(writer, &http.Cookie{Name: "freeagent-chat", Value: server.token, Path: "/api/",
			HttpOnly: true, SameSite: http.SameSiteStrictMode})
		server.writeJSON(writer, http.StatusOK, map[string]any{"connected": true})
		return
	}
	if path == "/api/events" && request.Method == http.MethodGet {
		server.events(writer, request)
		return
	}
	if server.attachAPI(writer, request) {
		return
	}
	// The attach capability is intentionally narrower than the interactive
	// Chat capability. All non-attach routes require the current launch key;
	// this prevents a token-only observer from reaching ordinary settings,
	// key, permission or conversation mutation handlers.
	if attachOK && !keyOK {
		server.writeError(writer, http.StatusForbidden, "user_interaction_required", "需要当前用户界面的交互确认。")
		return
	}
	if server.controlAPI(writer, request) {
		return
	}
	if server.commandsAPI(writer, request) {
		return
	}
	if server.settingsAPI(writer, request) {
		return
	}
	if server.conversationsAPI(writer, request) {
		return
	}
	if path == "/api/environment" && request.Method == http.MethodGet {
		config := server.tasks.Config()
		models := map[string]any{}
		keys := server.tasks.Keys()
		for id, model := range config.Models {
			entry := map[string]any{"provider": model.Provider, "model": model.Model, "capabilities": model.Capabilities,
				"key": model.Key, "key_name": "", "missing": false}
			if model.Key != "" && keys != nil {
				profile, err := keys.Find(model.Key)
				if errors.Is(err, harness.ErrKeyNotFound) {
					entry["missing"] = true
				} else if err != nil {
					server.keyFailure(writer, err, http.StatusConflict)
					return
				} else {
					entry["key_name"] = profile.Name
				}
			}
			models[id] = entry
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{
			"workspace": config.Workspace, "environment": server.tasks.EnvironmentID(),
			"models": models, "auto": config.Auto.Enabled, "default_model": config.DefaultModel,
			"permission_modes": []harness.PermissionMode{harness.PermissionAsk, harness.PermissionWorkspace, harness.PermissionFull},
			"capabilities":     server.tasks.CapabilityCatalog(),
			"max_prompt_bytes": config.MaxContextBytes,
		})
		return
	}
	if server.mcpAPI(writer, request) {
		return
	}
	if server.skillsAPI(writer, request) {
		return
	}
	if server.extensionsAPI(writer, request) {
		return
	}
	if server.keysAPI(writer, request) {
		return
	}
	if path == "/api/chats" {
		if request.Method == http.MethodGet {
			chats, err := server.tasks.Conversations(request.Context())
			if err != nil {
				server.fail(writer, err)
				return
			}
			filtered := make([]harness.Conversation, 0)
			for _, chat := range chats {
				if chat.Workspace == server.tasks.Config().Workspace {
					filtered = append(filtered, chat)
				}
			}
			server.writeJSON(writer, http.StatusOK, filtered)
			return
		}
		var body struct {
			Title string `json:"title"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if len([]rune(body.Title)) > 80 {
			server.writeError(writer, http.StatusBadRequest, "input", "对话名称过长。")
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			body.Title = "新对话"
		}
		chat, err := server.tasks.NewConversation(request.Context(), body.Title)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusCreated, chat)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/chats/"), "/")
	if !strings.HasPrefix(path, "/api/chats/") || len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	chat := parts[0]
	if len(parts) == 2 && parts[1] == "raw" && request.Method == http.MethodGet {
		raw, err := server.tasks.ChatRaw(request.Context(), chat, request.URL.Query().Get("kind"), request.URL.Query().Get("key"))
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]string{"raw": raw})
		return
	}
	if len(parts) == 2 && parts[1] == "limits" && request.Method == http.MethodGet {
		if _, err := server.tasks.Chat(request.Context(), chat, ""); err != nil {
			server.fail(writer, err)
			return
		}
		run, err := server.tasks.Latest(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		limits, err := server.tasks.Limits(request.Context(), run.ID)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, limits)
		return
	}
	if len(parts) == 2 && parts[1] == "memory" && request.Method == http.MethodGet {
		selection, err := server.tasks.SelectedMemory(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		type memoryEntry struct {
			Scope   harness.MemoryScope `json:"scope"`
			EntryID string              `json:"entry_id"`
			Title   string              `json:"title"`
			State   string              `json:"state"`
		}
		entries := make([]memoryEntry, 0, len(selection.Entries))
		for _, reference := range selection.Entries {
			entry, readErr := server.tasks.ReadMemory(request.Context(), chat, reference.Scope, reference.EntryID)
			item := memoryEntry{Scope: reference.Scope, EntryID: reference.EntryID, State: "unavailable"}
			if readErr == nil {
				item.Title, item.State = entry.Title, entry.State
			}
			entries = append(entries, item)
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{
			"entries": entries, "knowledge": selection.Knowledge, "task_private": selection.TaskPrivate,
		})
		return
	}
	if len(parts) == 1 && request.Method == http.MethodGet {
		view, err := server.tasks.Chat(request.Context(), chat, request.URL.Query().Get("before"))
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.mu.Lock()
		streams := make([]Stream, 0)
		for _, stream := range server.streams {
			for _, turn := range view.Turns {
				if stream.Run == turn.Key {
					streams = append(streams, stream)
				}
			}
		}
		activeChat := server.active
		busy, stopping, elsewhere := activeChat == chat, server.stopping && activeChat == chat, activeChat != "" && activeChat != chat
		server.mu.Unlock()
		server.writeJSON(writer, http.StatusOK, map[string]any{"view": view, "streams": streams, "busy": busy,
			"stopping": stopping, "running_elsewhere": elsewhere, "active_chat": activeChat})
		return
	}
	if request.Method != http.MethodPost || len(parts) != 2 {
		http.NotFound(writer, request)
		return
	}
	// Resolve workspace and Chat existence before every mutation.
	if _, err := server.tasks.Chat(request.Context(), chat, ""); err != nil {
		server.fail(writer, err)
		return
	}
	switch parts[1] {
	case "pin":
		var body struct {
			Pinned bool `json:"pinned"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if _, err := server.applyDirectControl(request.Context(), "", chat, harness.ControlPinConversation, map[string]any{
			"conversation_id": chat, "pinned": body.Pinned,
		}); err != nil {
			server.fail(writer, err)
			return
		}
		updated, err := server.tasks.Chat(request.Context(), chat, "")
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"pinned": updated.Conversation.Pinned, "pinned_at": updated.Conversation.PinnedAt})
	case "title":
		var body struct {
			Title string `json:"title"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if _, err := server.applyDirectControl(request.Context(), "", chat, harness.ControlRenameConversation, map[string]any{
			"conversation_id": chat, "title": body.Title,
		}); err != nil {
			server.fail(writer, err)
			return
		}
		updated, err := server.tasks.Chat(request.Context(), chat, "")
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]string{"id": chat, "title": updated.Conversation.Title})
	case "exclude-memory":
		var body struct {
			Entry     *harness.MemoryReference `json:"entry"`
			Knowledge *harness.MemoryScope     `json:"knowledge"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if body.Entry == nil && body.Knowledge == nil || body.Entry != nil && body.Knowledge != nil {
			server.writeError(writer, http.StatusBadRequest, "input", "记忆排除请求必须只指定一个目标。")
			return
		}
		removed, err := server.tasks.ExcludeMemory(request.Context(), chat, body.Entry, body.Knowledge)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"removed": removed})
	case "model":
		var body struct {
			Model string `json:"model"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if _, err := server.applyDirectControl(request.Context(), "", chat, harness.ControlSetConversationModel, map[string]any{
			"conversation_id": chat, "model_id": body.Model,
		}); err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"saved": true})
	case "permission":
		var body struct {
			Mode      harness.PermissionMode `json:"mode"`
			Confirmed bool                   `json:"confirmed"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		selected, err := server.tasks.SelectedPermission(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		if body.Mode == harness.PermissionFull && selected.Mode != harness.PermissionFull && !body.Confirmed {
			server.fail(writer, harness.ErrFullAccessConfirmation)
			return
		}
		if _, applyErr := server.applyDirectControl(request.Context(), "", chat, harness.ControlSetConversationPermission, map[string]any{
			"conversation_id": chat, "mode": body.Mode,
		}); applyErr != nil {
			server.fail(writer, applyErr)
			return
		}
		selected, err = server.tasks.SelectedPermission(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, selected)
	case "capabilities":
		var body harness.CapabilitySelection
		if !server.readBody(writer, request, &body) {
			return
		}
		if _, err := server.applyDirectControl(request.Context(), "", chat, harness.ControlSetConversationCapabilities, map[string]any{
			"conversation_id": chat, "capabilities": body,
		}); err != nil {
			server.fail(writer, err)
			return
		}
		selected, err := server.tasks.SelectedCapabilities(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, selected)
	case "workflow":
		var body struct {
			Extension  string          `json:"extension"`
			Workflow   string          `json:"workflow"`
			Input      json.RawMessage `json:"input"`
			RequestKey string          `json:"request_key"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if strings.TrimSpace(body.Extension) == "" || strings.TrimSpace(body.Workflow) == "" ||
			body.RequestKey == "" || len(body.RequestKey) > 128 {
			server.writeError(writer, http.StatusBadRequest, "input", "workflow、扩展名称或提交标识无效。")
			return
		}
		var input any
		if len(body.Input) != 0 {
			if err := json.Unmarshal(body.Input, &input); err != nil {
				server.writeError(writer, http.StatusBadRequest, "input", "workflow 输入必须是有效 JSON。")
				return
			}
		}
		server.start(writer, chat, func(ctx context.Context) (harness.RunResult, error) {
			return server.tasks.RunWorkflow(ctx, chat, body.RequestKey, body.Extension, body.Workflow, input)
		})
	case "submit":
		var body struct {
			Prompt            string                       `json:"prompt"`
			RequestKey        string                       `json:"request_key"`
			Permission        harness.PermissionMode       `json:"permission"`
			ConfirmFullAccess bool                         `json:"confirm_full_access"`
			Capabilities      *harness.CapabilitySelection `json:"capabilities,omitempty"`
		}
		if !server.readBody(writer, request, &body) {
			return
		}
		if strings.TrimSpace(body.Prompt) == "" || body.RequestKey == "" || len(body.RequestKey) > 128 ||
			len(body.Prompt) > server.tasks.Config().MaxContextBytes {
			server.writeError(writer, http.StatusBadRequest, "input", "任务为空、过长或缺少提交标识。")
			return
		}
		if body.Permission != "" {
			if err := body.Permission.Validate(); err != nil {
				server.writeError(writer, http.StatusBadRequest, "input", "权限模式无效。")
				return
			}
			if body.Permission == harness.PermissionFull && !body.ConfirmFullAccess {
				selected, err := server.tasks.SelectedPermission(request.Context(), chat)
				if err != nil {
					server.fail(writer, err)
					return
				}
				if selected.Mode != harness.PermissionFull {
					server.fail(writer, harness.ErrFullAccessConfirmation)
					return
				}
			}
		}
		server.start(writer, chat, func(ctx context.Context) (harness.RunResult, error) {
			// This first Chat caller is explicitly single-Agent. Optional compositions
			// remain in the CLI, not silently enabled by a hidden preference.
			off := harness.DefaultCollaborationSettings()
			off.Mode = "off"
			return server.tasks.SubmitWithOptions(ctx, chat, body.RequestKey, body.Prompt, harness.RunOptions{
				Collaboration: &off, Permission: body.Permission, ConfirmFullAccess: body.ConfirmFullAccess,
				Capabilities: body.Capabilities,
			})
		})
	case "stop":
		server.mu.Lock()
		active := server.active == chat
		if active {
			server.stopping = true
			server.activeCancel()
			server.tasks.Cancel()
		}
		server.mu.Unlock()
		server.writeJSON(writer, http.StatusOK, map[string]bool{"requested": active})
	case "continue", "preview", "confirm", "end", "limits":
		run, err := server.tasks.Latest(request.Context(), chat)
		if err != nil {
			server.fail(writer, err)
			return
		}
		switch parts[1] {
		case "continue":
			server.start(writer, chat, func(ctx context.Context) (harness.RunResult, error) {
				return server.tasks.Continue(ctx, run.ID)
			})
		case "preview":
			preview, err := server.tasks.Preview(request.Context(), run.ID)
			if err != nil {
				server.fail(writer, err)
				return
			}
			server.writeJSON(writer, http.StatusOK, preview)
		case "confirm":
			var body struct {
				Confirmation harness.Confirmation `json:"confirmation"`
				Approved     bool                 `json:"approved"`
				Code         string               `json:"code"`
			}
			if !server.readBody(writer, request, &body) {
				return
			}
			if body.Confirmation.RunID != run.ID {
				server.fail(writer, harness.ErrApprovalChanged)
				return
			}
			server.start(writer, chat, func(ctx context.Context) (harness.RunResult, error) {
				if body.Confirmation.ControlOperationID != "" {
					return server.confirmControlProposal(ctx, chat, run.ID, body.Confirmation, body.Code, body.Approved)
				}
				return server.tasks.Confirm(ctx, run.ID, body.Confirmation, body.Approved)
			})
		case "end":
			var body struct {
				Acknowledge bool `json:"acknowledge"`
			}
			if !server.readBody(writer, request, &body) {
				return
			}
			if !body.Acknowledge {
				server.writeError(writer, http.StatusBadRequest, "confirmation", "结束任务需明确确认；已有作用和未知结果会保留。")
				return
			}
			if err := server.tasks.Abandon(request.Context(), run.ID); err != nil {
				server.fail(writer, err)
				return
			}
			server.writeJSON(writer, http.StatusOK, map[string]bool{"ended": true})
		case "limits":
			var body struct {
				Steps   int `json:"steps"`
				Seconds int `json:"seconds"`
			}
			if !server.readBody(writer, request, &body) {
				return
			}
			limits, err := server.tasks.Limits(request.Context(), run.ID)
			if err == nil {
				limits.MaxSteps, limits.MaxRunSeconds = body.Steps, body.Seconds
				err = server.tasks.IncreaseLimits(request.Context(), run.ID, limits)
			}
			if err != nil {
				server.fail(writer, err)
				return
			}
			server.writeJSON(writer, http.StatusOK, map[string]bool{"saved": true})
		}
	default:
		http.NotFound(writer, request)
	}
}

func (server *Server) conversationsAPI(writer http.ResponseWriter, request *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/conversations/"), "/")
	if !strings.HasPrefix(request.URL.Path, "/api/conversations/") || len(parts) < 2 || parts[0] == "" {
		return false
	}
	chatID := parts[0]
	if len(parts) == 4 && parts[1] == "requests" && parts[3] == "status" && request.Method == http.MethodGet {
		run, found, err := server.tasks.RequestStatus(request.Context(), chatID, parts[2])
		if err != nil {
			server.fail(writer, err)
			return true
		}
		if !found {
			server.writeJSON(writer, http.StatusOK, map[string]any{"found": false, "request_key": parts[2]})
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"found": true, "request_key": parts[2],
			"run_id": run.ID, "status": run.Status, "updated_at": run.UpdatedAt,
			"message": run.StopReason})
		return true
	}
	if len(parts) != 2 || request.Method != http.MethodPatch {
		return false
	}
	if _, err := server.tasks.Chat(request.Context(), chatID, ""); err != nil {
		server.fail(writer, err)
		return true
	}
	switch parts[1] {
	case "permission":
		var body struct {
			Mode      harness.PermissionMode `json:"mode"`
			Confirmed bool                   `json:"confirmed"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		selected, err := server.tasks.SelectedPermission(request.Context(), chatID)
		if err != nil {
			server.permissionFailure(writer, err)
			return true
		}
		if body.Mode == harness.PermissionFull && selected.Mode != harness.PermissionFull && !body.Confirmed {
			server.permissionFailure(writer, harness.ErrFullAccessConfirmation)
			return true
		}
		if _, applyErr := server.applyDirectControl(request.Context(), "", chatID, harness.ControlSetConversationPermission, map[string]any{
			"conversation_id": chatID, "mode": body.Mode,
		}); applyErr != nil {
			server.permissionFailure(writer, applyErr)
			return true
		}
		selection, err := server.tasks.SelectedPermission(request.Context(), chatID)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"conversation_id": chatID, "permission": selection})
		return true
	case "title":
		var body struct {
			Title string `json:"title"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", chatID, harness.ControlRenameConversation, map[string]any{
			"conversation_id": chatID, "title": body.Title,
		}); err != nil {
			server.fail(writer, err)
			return true
		}
		conversation, err := server.tasks.Conversations(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		for _, item := range conversation {
			if item.ID == chatID {
				server.writeJSON(writer, http.StatusOK, item)
				return true
			}
		}
		server.fail(writer, errors.New("conversation not found"))
		return true
	default:
		return false
	}
}

// executeAttachedOperation runs an attached owner operation that may contain
// Provider work while keeping it visible to cancellation and handoff. The
// response remains synchronous so the attached CLI receives the existing
// typed result instead of inventing a second polling protocol.
func (server *Server) executeAttachedOperation(writer http.ResponseWriter, chat string, execute func(context.Context) (any, error)) {
	server.mu.Lock()
	if server.active != "" || server.closed || server.stopping {
		server.mu.Unlock()
		server.fail(writer, harness.ErrActiveRun)
		return
	}
	operation, cancel := context.WithCancel(server.ctx)
	server.active, server.activeCancel = chat, cancel
	server.wg.Add(1)
	server.mu.Unlock()
	defer func() {
		server.wg.Done()
		cancel()
		server.mu.Lock()
		stopping := server.stopping
		server.active = ""
		server.activeCancel = nil
		if stopping {
			server.requestStopLocked()
		}
		server.mu.Unlock()
	}()
	result, err := execute(operation)
	if err != nil {
		server.fail(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, result)
}

func (server *Server) start(writer http.ResponseWriter, chat string, execute func(context.Context) (harness.RunResult, error)) {
	server.startBound(writer, chat, "", execute)
}

func (server *Server) startBound(writer http.ResponseWriter, chat, binding string, execute func(context.Context) (harness.RunResult, error)) {
	server.mu.Lock()
	if server.active != "" || server.closed || server.stopping {
		server.mu.Unlock()
		server.fail(writer, harness.ErrActiveRun)
		return
	}
	server.active, server.stopping = chat, false
	server.activeBinding = binding
	operation, cancel := context.WithCancel(server.ctx)
	server.activeCancel = cancel
	server.wg.Add(1)
	server.mu.Unlock()
	go func() {
		defer server.wg.Done()
		defer cancel()
		result, err := execute(operation)
		server.mu.Lock()
		stopping := server.stopping
		server.active = ""
		server.activeBinding = ""
		server.activeCancel = nil
		server.mu.Unlock()
		if stopping {
			server.mu.Lock()
			server.requestStopLocked()
			server.mu.Unlock()
		}
		if err != nil {
			code, text := userError(err)
			// Classification is transport feedback, never an authoritative Run state.
			server.notify("request_error", chat, code+": "+text)
		}
		server.notify("refresh", chat, result.Run.Status)
	}()
	server.writeJSON(writer, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (server *Server) events(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	listener := make(chan []byte, 32)
	server.mu.Lock()
	if len(server.listeners) >= 8 {
		server.mu.Unlock()
		writer.WriteHeader(http.StatusTooManyRequests)
		return
	}
	server.listeners[listener] = true
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.listeners, listener)
		server.mu.Unlock()
	}()
	writer.Header().Set("Content-Type", "text/event-stream")
	controller := http.NewResponseController(writer)
	send := func(raw []byte) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(writer, "data: %s\n\n", raw)
		flusher.Flush()
		return err
	}
	if send([]byte(`{"kind":"reconnect"}`)) != nil {
		return
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-server.ctx.Done():
			return
		case <-request.Context().Done():
			return
		case raw, ok := <-listener:
			if !ok || send(raw) != nil {
				return
			}
		case <-tick.C:
			if send([]byte(`{"kind":"heartbeat"}`)) != nil {
				return
			}
		}
	}
}

func (server *Server) readBody(writer http.ResponseWriter, request *http.Request, body any) bool {
	if request.Header.Get("Content-Type") != "application/json" {
		server.writeError(writer, http.StatusUnsupportedMediaType, "input", "请求格式无效。")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil || decoder.Decode(new(any)) != io.EOF {
		server.writeError(writer, http.StatusBadRequest, "input", "请求无效或过长。")
		return false
	}
	return true
}

func userError(err error) (string, string) {
	var failure *harness.CallError
	switch {
	case errors.Is(err, harness.ErrUnknownResult):
		return "unknown", "结果未知。可能已有作用；请核对记录，不要直接重试。"
	case errors.Is(err, harness.ErrApprovalChanged):
		return "reconfirm", "变更已变化。请重新预览并确认。"
	case errors.Is(err, harness.ErrFullAccessConfirmation):
		return "permission_confirmation", "启用 Full access 需要明确确认。"
	case errors.Is(err, harness.ErrConfigChanged):
		return "config", "任务配置已变化，需要先处理原任务。"
	case errors.Is(err, harness.ErrSnapshotIncomplete):
		return "snapshot_incomplete", "运行快照缺少当前版本所需的权限信息，未套用当前配置。"
	case errors.Is(err, harness.ErrModelCapability), errors.Is(err, harness.ErrContextLimit):
		return "capability", "当前模型能力或上下文限额不足。"
	case errors.Is(err, harness.ErrActiveRun):
		return "busy", "存在未完成任务，请先继续、停止或明确结束。"
	case errors.Is(err, sql.ErrNoRows):
		return "missing", "记录不存在，请刷新会话。"
	case errors.Is(err, context.Canceled):
		return "stopped", "停止请求已处理，实际结果以记录为准。"
	case errors.As(err, &failure):
		if !failure.Certain {
			return "unknown", "Provider 结果未知，可能已处理请求；请核对记录，不要直接重试。"
		}
		switch failure.Category {
		case "authentication":
			return "provider_auth", "Provider 拒绝了这个 Key，请在 Key 管理中检查或重新输入。"
		case "permission":
			return "provider_permission", "Provider 拒绝访问，请检查模型与凭据权限。"
		case "quota":
			return "provider_quota", "Provider 额度不足，请处理额度后再发起新任务。"
		case "rate_limit":
			return "provider_rate", "Provider 限流。稍后可显式发起新任务；未自动重试。"
		case "context_limit":
			return "capability", "Provider 上下文限额不足，请缩小输入或选择合适模型。"
		case "capability", "usage":
			return "capability", "Provider 未满足声明的模型能力，未派发返回的工具。"
		case "output_limit":
			return "provider_output", "Provider 输出达到限额，片段不是最终回答，未执行其中的工具。"
		case "refusal", "content_filter":
			return "provider_refusal", "Provider 拒绝或过滤了请求，未执行其中的工具。"
		case "request", "redirect":
			return "provider_request", "Provider 拒绝请求，请检查模型配置与协议。"
		case "unavailable", "timeout":
			return "provider_unavailable", "Provider 暂时不可用。可显式发起新任务；未自动重试。"
		case "cancelled":
			return "stopped", "模型派发前已停止，实际操作记录仍会保留。"
		default:
			return "provider_failure", "Provider 返回确定性失败，请核对运行记录。"
		}
	case errors.Is(err, harness.ErrKeyNotFound):
		return "key_not_found", "找不到这个 Key 配置，可能已被删除。请在 Key 管理中重新选择。"
	case errors.Is(err, harness.ErrKeyNeedsReentry):
		return "key_reentry", "这个 Key 需要重新输入，可能换了 Windows 用户或电脑。请在 Key 管理中重新输入。"
	case errors.Is(err, harness.ErrKeysBusy):
		return "keys_busy", "Key 文件正被另一个 FreeAgent 进程写入，请稍后重试。"
	case errors.Is(err, harness.ErrKeyNameExists):
		return "key_name_exists", "已有同名的 Key 配置，请换一个名称。"
	case errors.Is(err, harness.ErrKeysFormat):
		return "keys_format", "Key 文件格式无效，未做任何修改。"
	case errors.Is(err, harness.ErrPinnedLimit):
		return "pinned_limit", "置顶会话已达到 50 个上限，请先取消其他置顶。"
	case errors.Is(err, harness.ErrInvalidConversationTitle):
		return "invalid_title", "对话名称不能为空，不能超过 80 个字符或包含换行。"
	case errors.Is(err, harness.ErrControlWrongCode), errors.Is(err, harness.ErrControlConfirmationRequired),
		errors.Is(err, harness.ErrControlExpired):
		return "control_code", "控制确认码无效或已过期，请重新显示确认卡。"
	case errors.Is(err, harness.ErrControlRunConfirmation):
		return "control_run", "这项设置变更来自等待中的任务，请在该任务的确认卡中处理。"
	case errors.Is(err, harness.ErrControlStaleRevision):
		return "control_stale", "控制目标已变化，请重新读取并计划。"
	case errors.Is(err, harness.ErrControlBusy):
		return "control_busy", "当前有主机命令正在运行，请稍后确认。"
	case errors.Is(err, harness.ErrControlUserInteractionRequired):
		return "control_interaction", "需要当前用户界面的交互确认。"
	case errors.Is(err, harness.ErrExternalControlChange):
		return "external_control_change", "检测到配置或 Key 被 owner 外部修改，请先审核后再运行。"
	default:
		return "attention", "请求未完成，请核对任务状态和运行记录。"
	}
}

func (server *Server) fail(writer http.ResponseWriter, err error) {
	code, text := userError(err)
	server.writeError(writer, http.StatusConflict, code, text)
}

func (server *Server) writeError(writer http.ResponseWriter, status int, code, text string) {
	server.writeJSON(writer, status, map[string]string{"error": text, "code": code})
}

func (server *Server) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
