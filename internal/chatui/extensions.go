package chatui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type extensionUpdateRequest struct {
	Enabled  *bool             `json:"enabled,omitempty"`
	Required *bool             `json:"required,omitempty"`
	Settings map[string]any    `json:"settings,omitempty"`
	EnvFrom  map[string]string `json:"env_from,omitempty"`
}

func (server *Server) extensionsAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if path == "/api/extensions" {
		switch request.Method {
		case http.MethodGet:
			items, err := server.tasks.Extensions(request.Context())
			if err != nil {
				server.extensionFailure(writer, err)
				return true
			}
			server.writeJSON(writer, http.StatusOK, map[string]any{"extensions": items})
		case http.MethodPost:
			var body struct {
				Path string `json:"path"`
			}
			if !server.readBody(writer, request, &body) {
				return true
			}
			body.Path = strings.TrimSpace(body.Path)
			if body.Path == "" || !filepath.IsAbs(body.Path) {
				server.writeError(writer, http.StatusBadRequest, "input", "扩展文件夹路径必须是绝对路径。")
				return true
			}
			result, err := server.applyDirectControl(request.Context(), "", "", harness.ControlImportExtension, map[string]string{"source": body.Path})
			if err != nil {
				server.extensionFailure(writer, err)
				return true
			}
			var item harness.ExtensionInfo
			if err := json.Unmarshal(result.Data, &item); err != nil {
				server.extensionFailure(writer, errors.New("harness: imported Extension result is unavailable"))
				return true
			}
			server.writeJSON(writer, http.StatusCreated, item)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
	}
	if path == "/api/extensions/trust" && request.Method == http.MethodPost {
		var body struct {
			Trusted bool `json:"trusted"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		workspace := server.tasks.Config().Workspace
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetExtensionPolicy, map[string]any{
			"trusted_workspace": workspace, "trusted": body.Trusted,
		}); err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"trusted": body.Trusted})
		return true
	}
	if !strings.HasPrefix(path, "/api/extensions/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/extensions/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return true
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "扩展名称无效。")
		return true
	}
	switch parts[1] {
	case "view":
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		item, err := server.tasks.Extension(request.Context(), id)
		if err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, item)
	case "update":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		var body extensionUpdateRequest
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.Enabled != nil {
			item, err := server.tasks.Extension(request.Context(), id)
			if err != nil {
				server.extensionFailure(writer, err)
				return true
			}
			if !item.Available {
				server.extensionFailure(writer, errors.New("harness: extension is unavailable"))
				return true
			}
			if !*body.Enabled && server.tasks.Config().Extensions.Required[id] {
				server.extensionFailure(writer, errors.New("harness: required extension cannot be disabled"))
				return true
			}
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetExtensionPolicy, map[string]any{
				"id": id, "enabled": *body.Enabled,
			}); err != nil {
				server.extensionFailure(writer, err)
				return true
			}
		}
		if body.Required != nil {
			config := server.tasks.Config()
			if *body.Required && !config.Extensions.Enabled[id] {
				server.extensionFailure(writer, errors.New("harness: required extension must be enabled"))
				return true
			}
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetExtensionPolicy, map[string]any{
				"id": id, "required": *body.Required,
			}); err != nil {
				server.extensionFailure(writer, err)
				return true
			}
		}
		if body.Settings != nil {
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetExtensionPolicy, map[string]any{
				"id": id, "settings": body.Settings,
			}); err != nil {
				server.extensionFailure(writer, err)
				return true
			}
		}
		if body.EnvFrom != nil {
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetExtensionPolicy, map[string]any{
				"id": id, "env_from": body.EnvFrom,
			}); err != nil {
				server.extensionFailure(writer, err)
				return true
			}
		}
		item, err := server.tasks.Extension(request.Context(), id)
		if err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, item)
	case "restart":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRestartExtension, map[string]string{"id": id}); err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"restarted": true})
	case "delete":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRemoveExtension, map[string]string{"id": id}); err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
	case "logs":
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		raw, err := server.tasks.ExtensionLog(id)
		if err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(raw)
	case "clear-data":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlClearExtensionData, map[string]string{"id": id}); err != nil {
			server.extensionFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"cleared": true})
	default:
		http.NotFound(writer, request)
	}
	return true
}

func (server *Server) extensionFailure(writer http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	}
	code, text := userError(err)
	if errors.Is(err, os.ErrNotExist) {
		code, text = "missing", "扩展不存在。"
	} else {
		message := err.Error()
		switch {
		case strings.Contains(message, "workspace extension must be trusted"):
			code, text = "trust_required", "工作区扩展尚未信任，请先信任当前工作区。"
		case strings.Contains(message, "extension is unavailable"):
			code, text = "trust_required", "扩展当前不可用，请先信任当前工作区。"
		case strings.Contains(message, "required extension cannot be disabled"):
			code, text = "required", "必需扩展不能停用，请先取消必需标记。"
		case strings.Contains(message, "required extension must be enabled"):
			code, text = "required", "必需扩展必须先启用。"
		case strings.Contains(message, "extension import path"),
			strings.Contains(message, "invalid extension folder"),
			strings.Contains(message, "extension path"):
			code, text = "input", "扩展文件夹无效，请确认其中包含合法的 extension.json。"
		case strings.Contains(message, "extension already exists"):
			code, text = "exists", "同 ID 扩展已存在，请先移除或更换目录。"
		case strings.Contains(message, "invalid extension identifier"):
			code, text = "input", "扩展名称无效。"
		}
	}
	server.writeError(writer, status, code, text)
}
