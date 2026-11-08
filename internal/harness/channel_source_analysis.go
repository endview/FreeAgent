package harness

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/endview/freeagent/internal/channelwire"
)

const sourceAnalysisCommandHelp = "已选记录分析（先安装、启用并授权相应 Skill）：\n" +
	"/analyze preview SKILL RUN_ID[,RUN_ID] [起始消息ID 结束消息ID]\n" +
	"核对来源后：/analyze start DIGEST SKILL RUN_ID[,RUN_ID] [起始消息ID 结束消息ID]\n" +
	"仅当前对话的公开来源；只生成草稿，不执行工具，不自动发布。保存与固定请在 GUI/CLI 使用现有确认入口。"

// Only in-flight starts are held here. Durable deduplication reads the normal
// Run snapshot, so restart does not require another analysis state store.
var sourceAnalysisChannelStarts sync.Map

func sourceAnalysisCommandSelection(args []string) (SourceAnalysisSelection, error) {
	selection := SourceAnalysisSelection{}
	if len(args) < 2 || !validCapabilityID(args[0]) {
		return selection, ErrRequestKey
	}
	selection.Skill, args = args[0], args[1:]
	if len(args) != 1 && len(args) != 3 {
		return selection, ErrRequestKey
	}
	selection.RunIDs = strings.Split(args[0], ",")
	if len(selection.RunIDs) > 16 {
		return selection, ErrRequestKey
	}
	seen := map[string]bool{}
	for _, id := range selection.RunIDs {
		if !memoryID(id) || seen[id] {
			return selection, ErrRequestKey
		}
		seen[id] = true
	}
	if len(args) == 3 {
		var err error
		selection.FromMessage, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil || selection.FromMessage < 1 {
			return selection, ErrRequestKey
		}
		selection.ThroughMessage, err = strconv.ParseInt(args[2], 10, 64)
		if err != nil || selection.ThroughMessage < selection.FromMessage {
			return selection, ErrRequestKey
		}
	}
	return selection, nil
}

// The existing channel dispatcher has already authenticated the event and
// recorded the command. Recheck scope here and at Run binding; commands never
// enter a nested Agent loop or acquire a local Owner's publication authority.
func (s *channelSession) sourceAnalysisCommand(ctx context.Context, e channelEvent, chat string, args []string) error {
	if s.source != SpaceSourceTelegram || e.Target.Source != SpaceSourceTelegram {
		return s.plain(ctx, e, "此分析命令仅用于已授权 TG 空间；其他入口请使用 GUI 或 CLI。")
	}
	grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
	if err != nil {
		return err
	}
	conversation, err := s.tasks.core.Store.GetConversation(ctx, chat)
	if err != nil || conversation.SpaceID != grant.SpaceID || conversation.Workspace != s.tasks.Config().Workspace {
		return ErrConversationSpace
	}
	if len(args) == 0 || len(args) == 1 && args[0] == "help" {
		return s.plain(ctx, e, sourceAnalysisCommandHelp)
	}
	action := args[0]
	if action != "preview" && action != "start" {
		return s.plain(ctx, e, sourceAnalysisCommandHelp)
	}
	values := args[1:]
	digest := ""
	if action == "start" {
		if len(values) == 0 {
			return s.plain(ctx, e, sourceAnalysisCommandHelp)
		}
		hash, err := hex.DecodeString(values[0])
		if err != nil || len(hash) != 32 || strings.ToLower(values[0]) != values[0] {
			return s.plain(ctx, e, sourceAnalysisCommandHelp)
		}
		digest, values = values[0], values[1:]
	}
	selection, err := sourceAnalysisCommandSelection(values)
	if err != nil {
		return s.plain(ctx, e, sourceAnalysisCommandHelp)
	}
	preview, err := s.tasks.PreviewSourceAnalysis(ctx, chat, selection)
	if err != nil {
		if errors.Is(err, ErrConversationSpace) {
			return s.plain(ctx, e, "来源不属于当前对话；请在来源所属的已授权空间与对话重新选择。")
		}
		return s.plain(ctx, e, "来源未采用："+safeError(err))
	}
	if action == "preview" {
		for _, part := range channelwire.SplitTextParts(channelwire.TextPart{Text: preview.Source}, 3800) {
			if err := s.plain(ctx, e, part.Text); err != nil {
				return err
			}
		}
		rangeArgs := ""
		if selection.FromMessage > 0 {
			rangeArgs = fmt.Sprintf(" %d %d", selection.FromMessage, selection.ThroughMessage)
		}
		return s.plain(ctx, e, fmt.Sprintf("来源预览：%d 字节，digest=%s。\n核对上述来源后，明确发送：\n/analyze start %s %s %s%s\n只生成草稿；保存、替换、启用与固定仍需现有确认。", preview.Bytes, preview.Digest, preview.Digest, selection.Skill, strings.Join(selection.RunIDs, ","), rangeArgs))
	}
	if digest != preview.Digest {
		return s.plain(ctx, e, "来源或 Skill 已变化，尚未开始；请重新 /analyze preview 并核对 digest。")
	}
	source := ChannelMessageSource{ChannelTarget: e.Target, MessageID: e.Message}
	var input int64
	if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT id FROM fa.messages WHERE conversation_id=$1 AND request_key=$2 AND run_id IS NULL`, chat, source.requestKey()).Scan(&input); err != nil {
		return err
	}
	origin := &ChannelRunOrigin{ChannelTarget: e.Target, InputMessageID: input, HistoryThrough: input, RequestKey: source.requestKey(), AccessRevision: grant.Revision}
	return s.launch(ctx, e, chat, func(ctx context.Context) (RunResult, error) {
		startKey := struct {
			store *Store
			chat  string
		}{s.tasks.core.Store, chat}
		if _, active := sourceAnalysisChannelStarts.LoadOrStore(startKey, true); active {
			return RunResult{}, ErrActiveRun
		}
		defer sourceAnalysisChannelStarts.Delete(startKey)
		current, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
		if err != nil || current.Revision != origin.AccessRevision {
			return RunResult{}, ErrPermissionRevoked
		}
		prior, err := scanRun(s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND config_doc #>> '{analysis_source,digest}'=$2 AND config_doc #>> '{Permission,person_id}'=$3 AND config_doc #>> '{Channel,access_revision}'=$4 ORDER BY seq LIMIT 1`, chat, digest, e.Person, strconv.FormatUint(current.Revision, 10)))
		if err == nil {
			evidence, err := s.tasks.core.Store.Evidence(ctx, prior.ID)
			return RunResult{Run: prior, Answer: prior.Output, Duplicate: true, Evidence: evidence}, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return RunResult{}, err
		}
		// Core atomically associates the received command with the new Run;
		// that association must retain the transport's original request key.
		return s.tasks.analyzeSources(ctx, chat, source.requestKey(), selection, digest, RunOptions{channel: origin, personID: e.Person})
	})
}
