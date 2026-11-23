package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const telegramAdminEntry = "telegram_admin"

func recordTelegramAdminDecision(record *controlRecord, auth ControlAuthorization) {
	if auth.telegramAdmin {
		record.Operation.DecidedBy = record.Operation.RequestedBy
		record.Operation.DecisionEntry = telegramAdminEntry
	}
}

var ErrTelegramAdminChanged = errors.New("harness: the original Telegram administrator delegation is no longer valid")

type telegramAdminProposal struct {
	BindingRevision int64  `json:"binding_revision"`
	Epoch           string `json:"epoch"`
	Digest          string `json:"digest"`
	Route           string `json:"route"`
	Provider        string `json:"provider_fingerprint,omitempty"`
}

func (service *ControlService) telegramAdminOperation(id, attempt string) (controlRecord, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[id]
	return record, ok && attempt != "" && record.Operation.AttemptID == attempt && record.Operation.RequestEntry == telegramAdminEntry
}

func adminProposalKey(target ChannelTarget, operation string) string {
	return "connector/telegram/" + target.ApplicationID + "/admin-proposal/" + operation
}

func (tasks *TaskService) readAdminProposal(ctx context.Context, target ChannelTarget, operation string) (telegramAdminProposal, error) {
	var raw string
	var result telegramAdminProposal
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, adminProposalKey(target, operation)).Scan(&raw); err != nil {
		return result, err
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.BindingRevision <= 0 || result.Epoch == "" || result.Digest == "" ||
		(result.Route != "telegram" && result.Route != "web" && result.Route != "local") {
		return result, ErrStoreFormat
	}
	return result, nil
}

func (service *ControlService) bindAdminProposalLocked(ctx context.Context, run Run, record *controlRecord) error {
	tasks := NewTaskService(service.core)
	identity, release, err := tasks.lockAdministrator()
	if err != nil {
		return ErrTelegramAdminChanged
	}
	defer release()
	binding, err := tasks.telegramAdminRunAuthority(ctx, run, identity)
	if err != nil {
		return ErrTelegramAdminChanged
	}
	origin := channelOriginFromJSON(run.ConfigJSON)
	if err := tasks.checkChannelRunDestination(ctx, run, origin.ChannelTarget); err != nil {
		return err
	}
	if !SettingsEntryRules(record.Operation.Kind).TelegramPropose {
		return ErrPermissionDenied
	}
	route := "web"
	ok, _, provider := service.telegramAdminDirectDiff(*record)
	if ok {
		route = "telegram"
	}
	if hostSettingsWiden(*record, service.core.currentConfig()) {
		route = "local"
		record.Operation.LocalApproval = true
	}
	record.Operation.RequestedBy, record.Operation.RequestEntry = "account:"+identity.AccountID, telegramAdminEntry
	bound := telegramAdminProposal{BindingRevision: binding.Revision, Epoch: identity.Epoch,
		Digest: record.Operation.Digest, Route: route, Provider: provider}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`,
		adminProposalKey(origin.ChannelTarget, record.Operation.ID), string(mustJSON(bound)))
	return err
}

func oneSettingArg(raw json.RawMessage, field string) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && len(fields) == 1 && len(fields[field]) != 0
}

func onlyChanges(before, after json.RawMessage, names ...string) (map[string]json.RawMessage, map[string]json.RawMessage, bool) {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil || len(a) != len(b) {
		return nil, nil, false
	}
	for k, v := range a {
		if bytes.Equal(v, b[k]) {
			continue
		}
		allowed := false
		for _, name := range names {
			allowed = allowed || name == k
		}
		if !allowed || b[k] == nil {
			return nil, nil, false
		}
	}
	return a, b, true
}

func (service *ControlService) telegramAdminDirectDiff(record controlRecord) (bool, string, string) {
	switch SettingsEntryRules(record.Operation.Kind).TelegramDecide {
	case "auto_compact_percent":
		if !oneSettingArg(record.Arguments, "auto_compact_percent") {
			return false, "", ""
		}
		a, b, ok := onlyChanges(record.Before["config.limits"], record.After["config.limits"], "auto_compact_percent")
		var from, to int
		if !ok || json.Unmarshal(a["auto_compact_percent"], &from) != nil || json.Unmarshal(b["auto_compact_percent"], &to) != nil || from == to || to < 50 || to > 95 {
			return false, "", ""
		}
		return true, fmt.Sprintf("全局自动压缩触发比例：%d%% → %d%%。保存后下一轮生效。", from, to), ""
	case "same_saved_provider":
		if !oneSettingArg(record.Arguments, "model_id") {
			return false, "", ""
		}
		a, b, ok := onlyChanges(record.Before["config.models"], record.After["config.models"], "default_model", "default_selection")
		var from, to, selection, nextSelection string
		if !ok || json.Unmarshal(a["default_model"], &from) != nil || json.Unmarshal(b["default_model"], &to) != nil ||
			json.Unmarshal(a["default_selection"], &selection) != nil || json.Unmarshal(b["default_selection"], &nextSelection) != nil ||
			from == to || selection != "" && selection != from || nextSelection != to {
			return false, "", ""
		}
		c := service.core.currentConfig()
		old, oldErr := service.core.newBinding(c, from)
		next, nextErr := service.core.newBinding(c, to)
		if oldErr != nil || nextErr != nil || old.Config.Provider != next.Config.Provider || old.Config.Key == "" ||
			old.Config.Key != next.Config.Key || old.BaseURL != next.BaseURL || service.core.Keys == nil ||
			old.Config.Capabilities.WebSearch != next.Config.Capabilities.WebSearch ||
			old.Config.Capabilities.Protocol != next.Config.Capabilities.Protocol {
			return false, "", ""
		}
		key, err := service.core.Keys.Find(old.Config.Key)
		endpoint, urlErr := url.Parse(old.BaseURL)
		if err != nil || urlErr != nil || endpoint.Scheme != "https" || endpoint.Host == "" || key.Source != KeySourceSaved {
			return false, "", ""
		}
		if _, err := service.core.Keys.Resolve(key.ID); err != nil {
			return false, "", ""
		}
		// Revisions and saved metadata pin the already configured destination
		// and credential; no key value or endpoint is rendered in Telegram.
		fingerprint := contentHash(mustJSON([]any{key.ID, key.BaseURL, key.Protocol, key.UpdatedAt, old.Revision, next.Revision}))
		return true, fmt.Sprintf("全局默认模型：%s → %s。保存后下一轮生效；此会话单独选用的模型不变。", from, to), fingerprint
	default:
		return false, "", ""
	}
}

func (service *ControlService) telegramAdminOrigin(ctx context.Context, record controlRecord) (Run, *ChannelRunOrigin, error) {
	attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
	if err != nil {
		return Run{}, nil, err
	}
	run, err := service.core.Store.GetRun(ctx, attempt.RunID)
	origin := channelOriginFromJSON(run.ConfigJSON)
	if err != nil || origin == nil || run.ConversationID != record.Operation.ConversationID {
		return Run{}, nil, ErrPermissionDenied
	}
	return run, origin, nil
}

func (service *ControlService) lockTelegramAdminDecision(ctx context.Context, record controlRecord, channel bool, apply bool) (func(), error) {
	if record.Operation.RequestEntry != telegramAdminEntry {
		if channel {
			return nil, ErrPermissionDenied
		}
		return func() {}, nil
	}
	tasks := NewTaskService(service.core)
	identity, release, err := tasks.lockAdministrator()
	if err != nil {
		return nil, ErrTelegramAdminChanged
	}
	fail := func(err error) (func(), error) { release(); return nil, err }
	if !record.Operation.Detached || record.Operation.RequestedBy != "account:"+identity.AccountID {
		return fail(ErrTelegramAdminChanged)
	}
	tasks.core.Store.telegramMu.Lock()
	unlockAccount := release
	release = func() { tasks.core.Store.telegramMu.Unlock(); unlockAccount() }
	if _, remote := PublicEntryFrom(ctx); remote {
		admin, verified := publicAdministratorFrom(ctx)
		if !verified || admin.id != identity.AccountID || record.Operation.LocalApproval && apply {
			return fail(ErrPermissionDenied)
		}
	}
	run, origin, err := service.telegramAdminOrigin(ctx, record)
	if err != nil || run.Status != "completed" || run.StopReason != detachedProposalReason {
		return fail(ErrTelegramAdminChanged)
	}
	if err := tasks.checkChannelRunDestination(ctx, run, origin.ChannelTarget); err != nil {
		return fail(ErrTelegramAdminChanged)
	}
	active, err := tasks.telegramAdminRunAuthority(ctx, run, identity)
	if err != nil {
		return fail(ErrTelegramAdminChanged)
	}
	bound, err := tasks.readAdminProposal(ctx, origin.ChannelTarget, record.Operation.ID)
	if err != nil || bound.BindingRevision != active.Revision || bound.Epoch != identity.Epoch || bound.Digest != record.Operation.Digest {
		return fail(ErrTelegramAdminChanged)
	}
	if channel {
		if bound.Route != "telegram" || record.Operation.LocalApproval {
			return fail(ErrPermissionDenied)
		}
	}
	if apply && bound.Route == "telegram" {
		ok, _, fingerprint := service.telegramAdminDirectDiff(record)
		if !ok || bound.Provider != fingerprint {
			return fail(ErrControlStaleRevision)
		}
	}
	return release, nil
}

func adminProposalLink(web PublicWebConfig, id string) string {
	if web.Enabled && web.Origin != "" {
		return "\n" + strings.TrimRight(web.Origin, "/") + "/?view=settings&section=diagnosis&operation=" + url.QueryEscape(id)
	}
	return ""
}
