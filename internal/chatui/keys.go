package chatui

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type keyRequest struct {
	Protocol string `json:"protocol"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Note     string `json:"note"`
	Source   string `json:"source"`
	Key      string `json:"key"`
}

type keyDeleteRequest struct {
	DisableModels bool   `json:"disable_models"`
	NewDefault    string `json:"new_default"`
}

type modelEnableRequest struct {
	Key     string `json:"key"`
	Model   string `json:"model"`
	ID      string `json:"id"`
	Default bool   `json:"default"`
}

type modelDisableRequest struct {
	NewDefault string `json:"new_default"`
}

type keyProfileView struct {
	harness.KeyProfileView
	NeedsReentry bool `json:"needs_reentry"`
}

type modelBatchRequest struct {
	Key           string   `json:"key"`
	Models        []string `json:"models"`
	DefaultPolicy string   `json:"default_policy"`
}

type modelDefaultRequest struct {
	ID string `json:"id"`
}

func modelProviderForKey(key harness.KeyProfileView) string {
	if key.Protocol == harness.KeyProtocolAnthropic {
		return "anthropic"
	}
	return "openai-compatible"
}

func (server *Server) keysAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	switch {
	case path == "/api/keys":
		if request.Method == http.MethodGet {
			server.listKeys(writer)
		} else if request.Method == http.MethodPost {
			server.addKey(writer, request)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
	case path == "/api/keys/test" && request.Method == http.MethodPost:
		server.testKey(writer, request)
		return true
	case path == "/api/models/enable" && request.Method == http.MethodPost:
		server.enableModel(writer, request)
		return true
	case path == "/api/models/enable-batch" && request.Method == http.MethodPost:
		server.enableModelsBatch(writer, request)
		return true
	case path == "/api/models/default" && request.Method == http.MethodPost:
		server.setDefaultModel(writer, request)
		return true
	}
	if strings.HasPrefix(path, "/api/keys/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/keys/"), "/")
		if len(parts) == 2 && parts[0] != "" {
			ref, err := url.PathUnescape(parts[0])
			if err != nil {
				server.writeError(writer, http.StatusBadRequest, "input", "请求无效。")
				return true
			}
			switch parts[1] {
			case "update":
				if request.Method == http.MethodPost {
					server.updateKey(writer, request, ref)
				} else {
					writer.WriteHeader(http.StatusMethodNotAllowed)
				}
			case "delete":
				if request.Method == http.MethodPost {
					server.deleteKey(writer, request, ref)
				} else {
					writer.WriteHeader(http.StatusMethodNotAllowed)
				}
			case "models":
				if request.Method == http.MethodPost {
					server.keyModels(writer, request, ref)
				} else {
					writer.WriteHeader(http.StatusMethodNotAllowed)
				}
			default:
				http.NotFound(writer, request)
			}
			return true
		}
		http.NotFound(writer, request)
		return true
	}
	if strings.HasPrefix(path, "/api/models/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/models/"), "/")
		if len(parts) == 2 && parts[1] == "disable" && parts[0] != "" {
			if request.Method == http.MethodPost {
				ref, err := url.PathUnescape(parts[0])
				if err != nil {
					server.writeError(writer, http.StatusBadRequest, "input", "请求无效。")
					return true
				}
				server.disableModel(writer, request, ref)
			} else {
				writer.WriteHeader(http.StatusMethodNotAllowed)
			}
			return true
		}
	}
	return false
}

func (server *Server) listKeys(writer http.ResponseWriter) {
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	profiles, err := store.List()
	if err != nil {
		server.keyFailure(writer, err, http.StatusConflict)
		return
	}
	views := make([]keyProfileView, 0, len(profiles))
	for _, profile := range profiles {
		needsReentry := false
		if _, err := store.Resolve(profile.ID); errors.Is(err, harness.ErrKeyNeedsReentry) {
			needsReentry = true
		} else if err != nil {
			server.keyFailure(writer, err, http.StatusConflict)
			return
		}
		views = append(views, keyProfileView{KeyProfileView: profile, NeedsReentry: needsReentry})
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"profiles": views, "presets": harness.KeyPresets()})
}

func (server *Server) addKey(writer http.ResponseWriter, request *http.Request) {
	var body keyRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	if body.Source != harness.KeySourceSaved && body.Source != harness.KeySourceNone {
		server.writeError(writer, http.StatusBadRequest, "input", "新增 Key 只支持已保存 Key 或无需 Key。")
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	input := harness.KeyInput{
		Name: body.Name, BaseURL: body.BaseURL, Protocol: body.Protocol, Note: body.Note,
		Source: body.Source, Secret: []byte(body.Key),
	}
	if _, err := server.applyDirectKeyControl(request.Context(), harness.KeyControlRequest{Input: input}); err != nil {
		server.keyFailure(writer, err, http.StatusBadRequest)
		return
	}
	view, err := store.Find(body.Name)
	if err != nil {
		server.keyFailure(writer, err, http.StatusConflict)
		return
	}
	server.writeJSON(writer, http.StatusCreated, view)
}

func (server *Server) updateKey(writer http.ResponseWriter, request *http.Request, ref string) {
	var body keyRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	current, err := store.Find(ref)
	if err != nil {
		server.keyFailure(writer, err, http.StatusNotFound)
		return
	}
	if current.Source == harness.KeySourceEnv && (body.Source != "" || body.Key != "") {
		server.writeError(writer, http.StatusBadRequest, "input", "环境变量来源只能修改名称、地址和备注。")
		return
	}
	if body.Protocol != "" && body.Protocol != current.Protocol {
		for _, model := range server.tasks.Config().Models {
			if model.Key == current.ID {
				server.writeError(writer, http.StatusConflict, "key_in_use", "这个 Key 已被模型引用，不能直接切换协议。请先移除引用模型。")
				return
			}
		}
	}
	if body.Source == harness.KeySourceEnv {
		server.writeError(writer, http.StatusBadRequest, "input", "环境变量来源只能由 CLI 创建。")
		return
	}
	input := harness.KeyInput{
		Name: body.Name, BaseURL: body.BaseURL, Protocol: body.Protocol, Note: body.Note,
		Source: body.Source, Secret: []byte(body.Key),
	}
	if input.Name == "" {
		input.Name = current.Name
	}
	if input.BaseURL == "" {
		input.BaseURL = current.BaseURL
	}
	if input.Note == "" {
		input.Note = current.Note
	}
	if input.Protocol == "" {
		input.Protocol = current.Protocol
	}
	if body.Source == "" {
		input.Source = current.Source
		input.EnvName = current.EnvName
	}
	if _, err := server.applyDirectKeyControl(request.Context(), harness.KeyControlRequest{Ref: current.ID, Input: input}); err != nil {
		server.keyFailure(writer, err, http.StatusBadRequest)
		return
	}
	view, err := store.Find(current.ID)
	if err != nil {
		server.keyFailure(writer, err, http.StatusConflict)
		return
	}
	server.writeJSON(writer, http.StatusOK, view)
}

func (server *Server) testKey(writer http.ResponseWriter, request *http.Request) {
	var body keyRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	var (
		resolved harness.ResolvedKey
		err      error
		saved    bool
		ref      = strings.TrimSpace(body.ID)
	)
	if strings.TrimSpace(body.BaseURL) == "" {
		ref = strings.TrimSpace(body.ID)
		if ref == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "请填写服务地址。")
			return
		}
		if strings.TrimSpace(body.Name) != "" || body.Source != "" || body.Key != "" {
			server.writeError(writer, http.StatusBadRequest, "input", "测试已保存配置时只需要 id。")
			return
		}
		resolved, err = store.Resolve(ref)
		saved = true
	} else {
		secret := body.Key
		source := body.Source
		if source == harness.KeySourceEnv {
			server.writeError(writer, http.StatusBadRequest, "input", "界面测试不支持环境变量来源。")
			return
		}
		if secret == "" && body.ID != "" {
			current, findErr := store.Find(body.ID)
			if findErr == nil {
				ref = current.ID
				if body.Protocol == "" {
					body.Protocol = current.Protocol
				}
				if source == "" {
					source = current.Source
				}
				existing, resolveErr := store.Resolve(current.ID)
				if resolveErr != nil {
					err = resolveErr
				} else {
					if source == harness.KeySourceNone {
						secret = ""
					} else {
						resolved, err = existing.WithProtocol(body.Protocol)
						if err == nil {
							resolved, err = resolved.WithBaseURL(body.BaseURL)
						}
					}
				}
			} else if !errors.Is(findErr, harness.ErrKeyNotFound) {
				err = findErr
			}
		}
		if err == nil {
			if source == "" {
				if secret == "" {
					source = harness.KeySourceNone
				} else {
					source = harness.KeySourceSaved
				}
			}
			if source != harness.KeySourceSaved && source != harness.KeySourceNone {
				server.writeError(writer, http.StatusBadRequest, "input", "测试来源无效。")
				return
			}
			if source == harness.KeySourceNone {
				secret = ""
			}
			if resolved.BaseURL == "" {
				resolved, err = harness.NewResolvedKeyForProtocol(body.BaseURL, secret, body.Protocol)
			}
		}
	}
	if err != nil {
		server.keyFailure(writer, err, http.StatusBadRequest)
		return
	}
	var result harness.ProbeResult
	if saved {
		control, controlErr := server.tasks.Control()
		if controlErr != nil {
			server.keyFailure(writer, controlErr, http.StatusConflict)
			return
		}
		result, controlErr = control.ProbeKey(request.Context(), ref, controlPrincipal)
		if controlErr != nil {
			server.keyFailure(writer, controlErr, http.StatusConflict)
			return
		}
	} else {
		result, err = harness.ProbeModelsForProtocol(request.Context(), resolved, nil)
		if err != nil {
			server.keyFailure(writer, err, http.StatusBadRequest)
			return
		}
	}
	server.writeJSON(writer, http.StatusOK, result)
}

func (server *Server) keyModels(writer http.ResponseWriter, request *http.Request, ref string) {
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	view, err := store.Find(ref)
	if err != nil {
		server.keyFailure(writer, err, http.StatusNotFound)
		return
	}
	control, controlErr := server.tasks.Control()
	if controlErr != nil {
		server.keyFailure(writer, controlErr, http.StatusConflict)
		return
	}
	result, err := control.ProbeKey(request.Context(), view.ID, controlPrincipal)
	if err != nil {
		server.keyFailure(writer, err, http.StatusConflict)
		return
	}
	server.writeJSON(writer, http.StatusOK, result)
}

func (server *Server) enableModel(writer http.ResponseWriter, request *http.Request) {
	if server.hasActiveRun() {
		server.configFailure(writer, harness.ErrActiveRun)
		return
	}
	var body modelEnableRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	if strings.TrimSpace(body.Key) == "" || strings.TrimSpace(body.Model) == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "需要 Key 配置和模型名称。")
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	key, err := store.Find(body.Key)
	if err != nil {
		server.keyFailure(writer, err, http.StatusNotFound)
		return
	}
	modelName := strings.TrimSpace(body.Model)
	modelID := strings.TrimSpace(body.ID)
	makeDefault := body.Default
	config := server.tasks.Config()
	if modelID == "" {
		modelID = harness.SuggestModelID(modelName, config)
	}
	if len(config.Models) == 0 {
		makeDefault = true
	}
	capabilities := harness.SuggestedModelCapabilities(key, modelName)
	_, err = server.applyDirectControl(request.Context(), "", "", harness.ControlSetModelProfile, map[string]any{
		"id": modelID, "provider": modelProviderForKey(key), "key": key.ID, "model": modelName,
		"capabilities": capabilities, "default": makeDefault,
	})
	if err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]string{"id": modelID})
}

func (server *Server) enableModelsBatch(writer http.ResponseWriter, request *http.Request) {
	if server.hasActiveRun() {
		server.configFailure(writer, harness.ErrActiveRun)
		return
	}
	var body modelBatchRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	if strings.TrimSpace(body.Key) == "" || len(body.Models) == 0 || len(body.Models) > 64 {
		server.writeError(writer, http.StatusBadRequest, "input", "需要 Key 配置和 1 到 64 个模型。")
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	key, err := store.Find(body.Key)
	if err != nil {
		server.keyFailure(writer, err, http.StatusNotFound)
		return
	}
	modelIDs := make([]string, 0, len(body.Models))
	seen := map[string]bool{}
	for _, raw := range body.Models {
		modelName := strings.TrimSpace(raw)
		if modelName == "" || strings.ContainsAny(modelName, "\x00\r\n") || len(modelName) > 256 || seen[modelName] {
			continue
		}
		seen[modelName] = true
		config := server.tasks.Config()
		found := ""
		for id, model := range config.Models {
			if model.Key == key.ID && model.Model == modelName {
				found = id
				break
			}
		}
		if found == "" {
			found = harness.SuggestModelID(modelName, config)
			makeDefault := len(config.Models) == 0 && len(modelIDs) == 0
			capabilities := harness.SuggestedModelCapabilities(key, modelName)
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetModelProfile, map[string]any{
				"id": found, "provider": modelProviderForKey(key), "key": key.ID, "model": modelName,
				"capabilities": capabilities, "default": makeDefault,
			}); err != nil {
				server.configFailure(writer, err)
				return
			}
		}
		modelIDs = append(modelIDs, found)
	}
	if len(modelIDs) == 0 {
		server.configFailure(writer, errors.New("harness: no valid models were supplied"))
		return
	}
	if err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"ids": modelIDs})
}

func (server *Server) setDefaultModel(writer http.ResponseWriter, request *http.Request) {
	if server.hasActiveRun() {
		server.configFailure(writer, harness.ErrActiveRun)
		return
	}
	var body modelDefaultRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "需要模型 ID。")
		return
	}
	_, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetDefaultModel, map[string]string{"model_id": id})
	if err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]string{"id": id})
}

func (server *Server) disableModel(writer http.ResponseWriter, request *http.Request, ref string) {
	if server.hasActiveRun() {
		server.configFailure(writer, harness.ErrActiveRun)
		return
	}
	var body modelDisableRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	_, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRemoveModel, map[string]string{"id": ref, "new_default": strings.TrimSpace(body.NewDefault)})
	if err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"changes": []string{"models: removed " + ref}})
}

func (server *Server) deleteKey(writer http.ResponseWriter, request *http.Request, ref string) {
	var body keyDeleteRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	store := server.tasks.Keys()
	if store == nil {
		server.keyFailure(writer, errors.New("harness: key store is not configured"), http.StatusConflict)
		return
	}
	profile, err := store.Find(ref)
	if err != nil {
		server.keyFailure(writer, err, http.StatusNotFound)
		return
	}
	config := server.tasks.Config()
	affected := make([]string, 0)
	for id, model := range config.Models {
		if model.Key == profile.ID {
			affected = append(affected, id)
		}
	}
	sort.Strings(affected)
	if len(affected) != 0 && !body.DisableModels {
		server.writeJSON(writer, http.StatusConflict, map[string]any{
			"code": "key_in_use", "error": "这个 Key 仍被模型使用，请先停用这些模型。",
			"affected": affected,
		})
		return
	}
	changes := make([]string, 0, len(affected))
	if body.DisableModels && len(affected) != 0 {
		for _, modelID := range affected {
			if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRemoveModel, map[string]string{
				"id": modelID, "new_default": strings.TrimSpace(body.NewDefault),
			}); err != nil {
				server.configFailure(writer, err)
				return
			}
			changes = append(changes, "models: removed "+modelID)
		}
	}
	if _, err := server.applyDirectKeyControl(request.Context(), harness.KeyControlRequest{Ref: profile.ID, Remove: true}); err != nil {
		server.keyFailure(writer, err, http.StatusConflict)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"affected": affected, "changes": changes})
}

func (server *Server) configFailure(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, harness.ErrActiveRun) || errors.Is(err, harness.ErrKeysBusy) {
		status = http.StatusConflict
	}
	server.keyFailure(writer, err, status)
}

func (server *Server) keyFailure(writer http.ResponseWriter, err error, fallback int) {
	code, text := userError(err)
	status := fallback
	switch {
	case errors.Is(err, harness.ErrKeyNotFound):
		status = http.StatusNotFound
	case errors.Is(err, harness.ErrKeyNeedsReentry), errors.Is(err, harness.ErrKeysBusy),
		errors.Is(err, harness.ErrActiveRun):
		status = http.StatusConflict
	case errors.Is(err, harness.ErrKeyNameExists), errors.Is(err, harness.ErrKeysFormat):
		if status == http.StatusConflict {
			status = http.StatusBadRequest
		}
	}
	server.writeError(writer, status, code, text)
}
