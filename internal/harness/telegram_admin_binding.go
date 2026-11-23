package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// AdministratorEpoch is supplied by the Owner's account service. Neither
// channels nor Run snapshots accept it as input.
type AdministratorEpoch struct{ AccountID, Epoch string }

type administratorGuard func() (AdministratorEpoch, func(), error)

func (tasks *TaskService) SetAdministratorGuard(guard func() (AdministratorEpoch, func(), error)) {
	current := administratorGuard(guard)
	tasks.core.Store.adminGuard.Store(&current)
}

func (tasks *TaskService) lockAdministrator() (AdministratorEpoch, func(), error) {
	if guard := tasks.core.Store.adminGuard.Load(); guard != nil && *guard != nil {
		return (*guard)()
	}
	return AdministratorEpoch{}, nil, ErrPermissionDenied
}

type telegramAdminBinding struct {
	User     string `json:"user,omitempty"`
	Account  string `json:"account,omitempty"`
	Epoch    string `json:"epoch,omitempty"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
}

type TelegramAdminView struct {
	User     string   `json:"user,omitempty"`
	State    string   `json:"state"`
	Revision int64    `json:"revision"`
	Choices  []string `json:"choices"`
}

func telegramAdminKey(bot string) string { return "connector/telegram/" + bot + "/admin-binding" }

func (tasks *TaskService) telegramAdminBinding(ctx context.Context, bot string) (telegramAdminBinding, error) {
	result := telegramAdminBinding{State: "unbound"}
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, telegramAdminKey(bot)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Revision <= 0 ||
		(result.State != "active" && result.State != "paused" && result.State != "unbound") ||
		(result.State != "unbound" && (!telegramNumeric(result.User, true) || result.Account == "" || result.Epoch == "")) {
		return result, ErrStoreFormat
	}
	return result, nil
}

func (tasks *TaskService) telegramAdminChoices(ctx context.Context, bot string) ([]string, error) {
	grants, err := tasks.TelegramAccessList(ctx, bot)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, grant := range grants {
		user := grant.Target.ChatID
		if !grant.Enabled || grant.Kind != "private" || !telegramNumeric(user, true) {
			continue
		}
		person := "telegram:user:" + user
		if _, err := tasks.CheckTelegramAccess(ctx, grant.Target, person); err != nil {
			if !errors.Is(err, ErrPermissionDenied) {
				return nil, err
			}
			continue
		}
		for _, verified := range grant.VerifiedPeople {
			if verified == person {
				result = append(result, user)
				break
			}
		}
	}
	return result, nil
}

func (tasks *TaskService) TelegramAdminBinding(ctx context.Context, bot string) (TelegramAdminView, error) {
	if _, err := tasks.confirmerKey(bot); err != nil {
		return TelegramAdminView{}, err
	}
	binding, err := tasks.telegramAdminBinding(ctx, bot)
	if err != nil {
		return TelegramAdminView{}, err
	}
	view := TelegramAdminView{User: binding.User, Revision: binding.Revision, State: binding.State}
	view.Choices, err = tasks.telegramAdminChoices(ctx, bot)
	if err != nil {
		return TelegramAdminView{}, err
	}
	if view.State == "active" {
		current, release, err := tasks.lockAdministrator()
		if err != nil {
			view.State = "paused"
		} else {
			if binding.Account != current.AccountID || binding.Epoch != current.Epoch {
				view.State = "paused"
			}
			release()
		}
	}
	return view, nil
}

func (tasks *TaskService) ApplyTelegramAdminBinding(ctx context.Context, bot, action, user string, revision int64) error {
	releaseSession, err := BeginPublicSettingsDecision(ctx, true)
	if err != nil {
		return err
	}
	defer releaseSession()
	if _, err := tasks.confirmerKey(bot); err != nil {
		return err
	}
	identity, release, err := tasks.lockAdministrator()
	if err != nil {
		return err
	}
	defer release()
	if administrator, remote := publicAdministratorFrom(ctx); remote && administrator.id != identity.AccountID {
		return ErrPermissionDenied
	}
	tasks.core.Store.telegramMu.Lock()
	defer tasks.core.Store.telegramMu.Unlock()
	old, err := tasks.telegramAdminBinding(ctx, bot)
	if err != nil {
		return err
	}
	if old.Revision != revision {
		return ErrControlStaleRevision
	}
	next := old
	switch action {
	case "bind":
		if !telegramNumeric(user, true) {
			return ErrPermissionDenied
		}
		ok := false
		choices, err := tasks.telegramAdminChoices(ctx, bot)
		if err != nil {
			return err
		}
		for _, choice := range choices {
			ok = ok || choice == user
		}
		if !ok {
			return ErrPermissionDenied
		}
		next = telegramAdminBinding{User: user, Account: identity.AccountID, Epoch: identity.Epoch, State: "active"}
	case "pause":
		if old.State == "unbound" || user != "" {
			return ErrPermissionDenied
		}
		next.State = "paused"
	case "revoke":
		if old.State == "unbound" || user != "" {
			return ErrPermissionDenied
		}
		next = telegramAdminBinding{State: "unbound"}
	default:
		return ErrRequestKey
	}
	next.Revision = old.Revision + 1
	key := telegramAdminKey(bot)
	tasks.core.Store.securityMu.Lock()
	defer tasks.core.Store.securityMu.Unlock()
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(mustJSON(next))); err != nil {
		return err
	}
	entry := "local:"
	if _, remote := PublicEntryFrom(ctx); remote {
		entry = "public:"
	}
	event := PublicSecurityEvent{At: nowText(), Source: entry + identity.AccountID, Reason: "telegram_admin_" + action + ":" + bot}
	if err := appendPublicSecurityEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(ErrUnknownResult, err)
	}
	return nil
}

func (tasks *TaskService) telegramAdminRunAuthority(ctx context.Context, run Run, identity AdministratorEpoch) (telegramAdminBinding, error) {
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || !validTelegramTarget(origin.ChannelTarget) || origin.ThreadID != "" || run.Permission == nil ||
		!telegramNumeric(origin.ChatID, true) || run.Permission.PersonID != "telegram:user:"+origin.ChatID {
		return telegramAdminBinding{}, ErrPermissionDenied
	}
	grant, err := tasks.CheckTelegramAccess(ctx, origin.ChannelTarget, run.Permission.PersonID)
	if err != nil || grant.Kind != "private" || grant.Revision != origin.AccessRevision {
		return telegramAdminBinding{}, ErrPermissionDenied
	}
	binding, err := tasks.telegramAdminBinding(ctx, origin.ApplicationID)
	if err != nil {
		return telegramAdminBinding{}, err
	}
	if binding.State != "active" || binding.User != strings.TrimPrefix(run.Permission.PersonID, "telegram:user:") ||
		binding.Account != identity.AccountID || binding.Epoch != identity.Epoch {
		return telegramAdminBinding{}, ErrPermissionDenied
	}
	return binding, nil
}
