package chatui

import (
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) attachmentsAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path == "/api/attachment-policy" {
		if r.Method == http.MethodPut {
			var policy harness.AttachmentPolicy
			if !server.readBody(w, r, &policy) {
				return true
			}
			if _, err := server.applyDirectControl(r.Context(), "", "", harness.ControlSetAttachmentPolicy, policy); err != nil {
				server.fail(w, err)
				return true
			}
		} else if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		server.writeJSON(w, http.StatusOK, server.tasks.Config().Attachments)
		return true
	}
	if path != "/api/attachments" && !strings.HasPrefix(path, "/api/attachments/") {
		return false
	}
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, http.StatusForbidden, "user_interaction_required", "附件需要当前用户入口。")
		return true
	}
	chat := r.URL.Query().Get("conversation_id")
	if path == "/api/attachments" && r.Method == http.MethodGet {
		refs, err := server.tasks.Attachments(r.Context(), chat)
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, http.StatusOK, refs)
		}
		return true
	}
	if path == "/api/attachments/reply" && r.Method == http.MethodPost {
		var body struct {
			Conversation string `json:"conversation_id"`
			Run          string `json:"run_id"`
			Request      string `json:"request_key"`
		}
		if !server.readBody(w, r, &body) {
			return true
		}
		ref, err := server.tasks.SaveReplyAttachment(r.Context(), body.Conversation, body.Run, body.Request)
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, http.StatusCreated, ref)
		}
		return true
	}
	if path == "/api/attachments/delete" && r.Method == http.MethodPost {
		var body struct {
			Conversation string `json:"conversation_id"`
			ID           string `json:"id"`
			SHA256       string `json:"sha256"`
		}
		if !server.readBody(w, r, &body) {
			return true
		}
		if _, err := server.applyDirectControl(r.Context(), "", body.Conversation, harness.ControlDeleteAttachment, body); err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		}
		return true
	}
	select {
	case server.attachmentSlots <- struct{}{}:
		defer func() { <-server.attachmentSlots }()
	default:
		server.writeError(w, http.StatusTooManyRequests, "attachment_busy", "同时最多处理 4 个文件传输。")
		return true
	}
	if path == "/api/attachments" && r.Method == http.MethodPost {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		limit := server.tasks.Config().Attachments.MaxInputBytes
		if limit == 0 {
			limit = harness.DefaultAttachmentPolicy().MaxInputBytes
		}
		if r.ContentLength > limit {
			server.writeError(w, http.StatusRequestEntityTooLarge, "attachment_limit", "文件超过输入上限。")
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit+1)
		defer r.Body.Close()
		ref, err := server.tasks.UploadAttachmentRequest(r.Context(), chat, r.URL.Query().Get("name"), r.URL.Query().Get("request_key"), r.Body)
		if err != nil {
			server.writeError(w, http.StatusBadRequest, "attachment_input", err.Error())
		} else {
			server.writeJSON(w, http.StatusCreated, ref)
		}
		return true
	}
	if strings.HasPrefix(path, "/api/attachments/") && r.Method == http.MethodGet {
		id := strings.TrimPrefix(path, "/api/attachments/")
		ref, raw, err := server.tasks.Attachment(r.Context(), chat, id)
		if err != nil {
			server.writeError(w, http.StatusNotFound, "attachment_unavailable", "附件不存在、已删除或不属于此对话。")
			return true
		}
		kind, disposition := "application/octet-stream", "attachment"
		if r.URL.Query().Get("inline") == "1" && (ref.MIME == "image/png" || ref.MIME == "image/jpeg") {
			kind, disposition = ref.MIME, "inline"
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": ref.Name}))
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		controller := http.NewResponseController(w)
		defer controller.SetWriteDeadline(time.Time{})
		for len(raw) > 0 {
			select {
			case <-r.Context().Done():
				return true
			default:
			}
			size := min(len(raw), 32<<10)
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write(raw[:size]); err != nil {
				return true
			}
			raw = raw[size:]
		}
		return true
	}
	server.writeError(w, http.StatusMethodNotAllowed, "method", fmt.Sprintf("Unsupported attachment method %s", r.Method))
	return true
}
