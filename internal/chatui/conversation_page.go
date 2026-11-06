package chatui

import (
	"net/http"
	"strconv"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) conversationPage(writer http.ResponseWriter, request *http.Request) {
	values := request.URL.Query()
	limit := 60
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			server.writeError(writer, 400, "input", "会话分页大小必须在 1 至 100 之间。")
			return
		}
		limit = n
	}
	page, err := server.tasks.ConversationPage(request.Context(), harness.ConversationQuery{SpaceID: values.Get("space_id"), Query: values.Get("q"), Cursor: values.Get("cursor"), Limit: limit})
	if err != nil {
		server.fail(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, page)
}
