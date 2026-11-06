package chatui

import (
	"context"

	"github.com/endview/freeagent/internal/buildinfo"
	"github.com/endview/freeagent/internal/harness"
)

// Chat's repeatable-read transaction must not be paired with a later idle
// execution slot. This affects confirmation as well as model generation.
// Retry a bounded number of times without holding the transport mutex over SQL.
func (server *Server) chatSnapshot(ctx context.Context, chat, before string) (map[string]any, error) {
	return server.readChatSnapshot(ctx, chat, before, server.tasks.Chat)
}

func (server *Server) readChatSnapshot(ctx context.Context, chat, before string,
	read func(context.Context, string, string) (harness.ChatView, error)) (map[string]any, error) {
	for retry := 0; retry < 3; retry++ {
		server.mu.Lock()
		sequence, execution := server.stateSeq, server.running[chat]
		server.mu.Unlock()
		view, err := read(ctx, chat, before)
		if err != nil {
			return nil, err
		}
		server.mu.Lock()
		stable := execution == server.running[chat] && sequence == server.stateSeq
		if !stable && retry < 2 {
			server.mu.Unlock()
			continue
		}
		streams := make([]Stream, 0)
		for _, stream := range server.streams {
			for _, turn := range view.Turns {
				if stream.Run == turn.Key {
					streams = append(streams, stream)
				}
			}
		}
		activeChat, _ := server.firstActiveLocked()
		activeChats := server.activeChatsLocked()
		busy := server.running[chat] != nil
		payload := map[string]any{"view": view, "streams": streams, "busy": busy || !stable,
			"stopping": server.stopping && busy, "running_elsewhere": len(server.running) >= harness.MaxForegroundRuns && !busy,
			"active_chat": activeChat, "active_chats": activeChats, "foreground_capacity": harness.MaxForegroundRuns,
			"generation": server.generation, "snapshot_seq": server.eventSeq, "snapshot_stable": stable, "server_version": buildinfo.Version}
		server.mu.Unlock()
		payload["background_active"] = server.tasks.HasCommands()
		return payload, nil
	}
	panic("unreachable snapshot retry")
}
