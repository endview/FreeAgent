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

type skillView struct {
	Name             string                  `json:"name"`
	Description      string                  `json:"description"`
	Trigger          string                  `json:"trigger"`
	Source           string                  `json:"source"`
	Enabled          bool                    `json:"enabled"`
	Available        bool                    `json:"available"`
	Trusted          bool                    `json:"trusted"`
	WorkspaceTrusted bool                    `json:"workspace_trusted"`
	SHA256           string                  `json:"sha256"`
	Bytes            int                     `json:"bytes"`
	Files            []harness.SkillFileInfo `json:"files,omitempty"`
	Body             string                  `json:"body,omitempty"`
}

func makeSkillView(item harness.SkillInfo, detail bool) skillView {
	view := skillView{
		Name: item.Name, Description: item.Description, Trigger: item.Trigger,
		Source: item.Source, Enabled: item.Enabled, Available: item.Available,
		Trusted: item.Trusted, WorkspaceTrusted: item.WorkspaceTrusted,
		SHA256: item.SHA256, Bytes: item.Bytes, Files: item.Files,
	}
	if detail {
		view.Body = item.Body
	}
	return view
}

func (server *Server) skillsAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if path == "/api/skills" {
		if request.Method == http.MethodGet {
			items, err := server.tasks.Skills(request.Context())
			if err != nil {
				server.skillFailure(writer, err)
				return true
			}
			views := make([]skillView, 0, len(items))
			for _, item := range items {
				views = append(views, makeSkillView(item, false))
			}
			server.writeJSON(writer, http.StatusOK, map[string]any{"skills": views})
			return true
		}
		if request.Method == http.MethodPost {
			var body struct {
				Path string `json:"path"`
			}
			if !server.readBody(writer, request, &body) {
				return true
			}
			body.Path = strings.TrimSpace(body.Path)
			if body.Path == "" || !filepath.IsAbs(body.Path) {
				server.writeError(writer, http.StatusBadRequest, "input", "Skill 文件夹路径必须是绝对路径。")
				return true
			}
			result, err := server.applyDirectControl(request.Context(), "", "", harness.ControlImportSkill, map[string]string{"source": body.Path})
			if err != nil {
				server.skillFailure(writer, err)
				return true
			}
			var item harness.SkillInfo
			if err := json.Unmarshal(result.Data, &item); err != nil {
				server.skillFailure(writer, errors.New("harness: imported Skill result is unavailable"))
				return true
			}
			if err != nil {
				server.skillFailure(writer, err)
				return true
			}
			server.writeJSON(writer, http.StatusCreated, makeSkillView(item, true))
			return true
		}
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	if path == "/api/skills/trust" && request.Method == http.MethodPost {
		var body struct {
			Trusted bool `json:"trusted"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		workspace := server.tasks.Config().Workspace
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetSkillPolicy, map[string]any{
			"trusted_workspace": workspace, "trusted": body.Trusted,
		}); err != nil {
			server.skillFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"trusted": body.Trusted})
		return true
	}
	if !strings.HasPrefix(path, "/api/skills/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/skills/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return true
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil || name == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "Skill 名称无效。")
		return true
	}
	switch parts[1] {
	case "view":
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		item, err := server.tasks.Skill(request.Context(), name)
		if err != nil {
			server.skillFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, makeSkillView(item, true))
	case "update":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		item, err := server.tasks.Skill(request.Context(), name)
		if err != nil {
			server.skillFailure(writer, err)
			return true
		}
		if body.Enabled && !item.Available {
			server.skillFailure(writer, errors.New("harness: workspace skill must be trusted before enabling"))
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetSkillPolicy, map[string]any{
			"id": name, "enabled": body.Enabled,
		}); err != nil {
			server.skillFailure(writer, err)
			return true
		}
		item, err = server.tasks.Skill(request.Context(), name)
		if err != nil {
			server.skillFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, makeSkillView(item, false))
	case "delete":
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRemoveSkill, map[string]string{"id": name}); err != nil {
			server.skillFailure(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
	default:
		http.NotFound(writer, request)
	}
	return true
}

func (server *Server) skillFailure(writer http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	}
	code, text := userError(err)
	if errors.Is(err, os.ErrNotExist) {
		code, text = "missing", "Skill 不存在。"
	} else {
		switch {
		case strings.Contains(err.Error(), "workspace skill must be trusted"):
			code, text = "trust_required", "工作区 Skill 尚未信任，请先信任当前工作区。"
		case strings.Contains(err.Error(), "invalid skill folder"),
			strings.Contains(err.Error(), "skill import path"),
			strings.Contains(err.Error(), "skill file"),
			strings.Contains(err.Error(), "invalid skill metadata"):
			code, text = "input", "Skill 文件夹无效，请确认其中包含合法的 SKILL.md。"
		case strings.Contains(err.Error(), "skill already exists"):
			code, text = "exists", "同名 Skill 已存在，请先删除或更换名称。"
		case strings.Contains(err.Error(), "invalid skill identifier"):
			code, text = "input", "Skill 名称无效。"
		}
	}
	server.writeError(writer, status, code, text)
}
