package harness

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const ControlTelegramAccess = "telegram.set_access"
const telegramAccessPrefix = "telegram/access/"
const telegramRequestTTL = time.Hour
const telegramRequestCapacity = 32
const telegramRequestNoticeInterval = time.Minute

type TelegramAccessRequest struct {
	ID          string        `json:"id"`
	Code        string        `json:"code"`
	Target      ChannelTarget `json:"target"`
	Kind        string        `json:"kind"`
	PersonID    string        `json:"person_id"`
	DisplayName string        `json:"display_name"`
	CreatedAt   time.Time     `json:"created_at"`
	ExpiresAt   time.Time     `json:"expires_at"`
	notifiedAt  time.Time
}

type TelegramAccess struct {
	Target               ChannelTarget             `json:"target"`
	SpaceID              string                    `json:"space_id"`
	Kind                 string                    `json:"kind"`
	Enabled              bool                      `json:"enabled"`
	WholeGroup           bool                      `json:"whole_group"`
	Members              []string                  `json:"members"`
	Revision             uint64                    `json:"revision"`
	UpdatedAt            string                    `json:"updated_at"`
	VerifiedPeople       []string                  `json:"verified_people,omitempty"`
	SpacePermission      PermissionMode            `json:"space_permission,omitempty"`
	People               []PersonPermission        `json:"people,omitempty"`
	EffectivePermissions map[string]PermissionMode `json:"effective_permissions,omitempty"`
}

type TelegramAccessChange struct {
	RequestCode string        `json:"request_code,omitempty"`
	RequestID   string        `json:"request_id,omitempty"`
	Target      ChannelTarget `json:"target"`
	Kind        string        `json:"kind"`
	Enabled     bool          `json:"enabled"`
	WholeGroup  bool          `json:"whole_group"`
	Members     []string      `json:"members"`
	Title       string        `json:"title,omitempty"`
}

func telegramNumeric(value string, positive bool) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n != 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == value
}

func validTelegramPerson(person string) bool {
	return strings.HasPrefix(person, "telegram:user:") && telegramNumeric(strings.TrimPrefix(person, "telegram:user:"), true)
}

func validTelegramTarget(t ChannelTarget) bool {
	return t.Source == SpaceSourceTelegram && telegramNumeric(t.ApplicationID, true) && telegramNumeric(t.ChatID, false)
}

func telegramAccessKey(target ChannelTarget) string { return telegramAccessPrefix + target.key() }

func (store *Store) telegramIdentity(bot string) {
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	if store.telegramBot != bot {
		store.telegramBot = bot
		store.telegramBots = map[string]bool{bot: true}
		store.telegramRequests = map[string]TelegramAccessRequest{}
	}
}

func (store *Store) telegramIdentities(bots []string) {
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	next := map[string]bool{}
	for _, bot := range bots {
		next[bot] = true
	}
	store.telegramBots = next
	for id, r := range store.telegramRequests {
		if !next[r.Target.ApplicationID] {
			delete(store.telegramRequests, id)
		}
	}
}

func (store *Store) expireTelegramRequests(now time.Time) {
	for id, request := range store.telegramRequests {
		if !now.Before(request.ExpiresAt) {
			delete(store.telegramRequests, id)
		}
	}
}

func (tasks *TaskService) requestTelegramAccess(e channelEvent, now time.Time) (TelegramAccessRequest, bool, error) {
	if !validTelegramTarget(e.Target) || !validTelegramPerson(e.Person) || e.Kind != "private" && e.Kind != "group" {
		return TelegramAccessRequest{}, false, ErrPermissionDenied
	}
	store := tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	if !store.telegramBots[e.Target.ApplicationID] {
		return TelegramAccessRequest{}, false, ErrPermissionDenied
	}
	store.expireTelegramRequests(now)
	for id, request := range store.telegramRequests {
		if request.Target == e.Target && request.Kind == e.Kind && request.PersonID == e.Person {
			notify := now.Sub(request.notifiedAt) >= telegramRequestNoticeInterval
			if notify {
				request.notifiedAt = now
				store.telegramRequests[id] = request
			}
			return request, notify, nil
		}
	}
	if len(store.telegramRequests) >= telegramRequestCapacity {
		return TelegramAccessRequest{}, false, errors.New("telegram: access request capacity reached; try again after an application expires")
	}
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	code := ""
	for {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return TelegramAccessRequest{}, false, err
		}
		for i := range raw {
			raw[i] = alphabet[int(raw[i])%len(alphabet)]
		}
		code = string(raw[:])
		duplicate := false
		for _, r := range store.telegramRequests {
			if r.Code == code {
				duplicate = true
			}
		}
		if !duplicate {
			break
		}
	}
	name := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, e.Name)
	name = string([]rune(name)[:min(len([]rune(name)), 128)])
	r := TelegramAccessRequest{ID: newID(), Code: code, Target: e.Target, Kind: e.Kind, PersonID: e.Person, DisplayName: name, CreatedAt: now, ExpiresAt: now.Add(telegramRequestTTL), notifiedAt: now}
	if store.telegramRequests == nil {
		store.telegramRequests = map[string]TelegramAccessRequest{}
	}
	store.telegramRequests[r.ID] = r
	return r, true, nil
}

func (tasks *TaskService) TelegramAccessRequests() []TelegramAccessRequest {
	store := tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	store.expireTelegramRequests(time.Now())
	result := make([]TelegramAccessRequest, 0, len(store.telegramRequests))
	for _, r := range store.telegramRequests {
		result = append(result, r)
	}
	slices.SortFunc(result, func(a, b TelegramAccessRequest) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return result
}

func (tasks *TaskService) TelegramRequest(code string) (TelegramAccessRequest, error) {
	for _, r := range tasks.TelegramAccessRequests() {
		if r.Code == strings.ToUpper(strings.TrimSpace(code)) || r.ID == code {
			return r, nil
		}
	}
	return TelegramAccessRequest{}, ErrControlExpired
}

func telegramAccessRaw(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, target ChannelTarget) (TelegramAccess, string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, telegramAccessKey(target)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramAccess{Target: target}, "", nil
	}
	var grant TelegramAccess
	if err == nil && (json.Unmarshal([]byte(raw), &grant) != nil || grant.Target != target || grant.Revision == 0) {
		err = ErrStoreFormat
	}
	return grant, raw, err
}

func (tasks *TaskService) CheckTelegramAccess(ctx context.Context, target ChannelTarget, person string) (TelegramAccess, error) {
	if target.Source != SpaceSourceTelegram {
		return TelegramAccess{}, nil
	}
	if !validTelegramTarget(target) || !validTelegramPerson(person) {
		return TelegramAccess{}, ErrPermissionDenied
	}
	grant, _, err := telegramAccessRaw(ctx, tasks.core.Store.db, target)
	if err != nil {
		return grant, err
	}
	if !grant.Enabled || !grant.WholeGroup && !slices.Contains(grant.Members, person) {
		return grant, ErrPermissionDenied
	}
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil || space.ID != grant.SpaceID || space.ChatKind != grant.Kind {
		return grant, ErrPermissionDenied
	}
	return grant, nil
}

// List includes existing platform bindings without a policy as unauthorized.
// Historical identities are displayed for verification, never inferred from titles.
func (tasks *TaskService) TelegramAccessList(ctx context.Context, bot string) ([]TelegramAccess, error) {
	spaces, err := tasks.Spaces(ctx)
	if err != nil {
		return nil, err
	}
	result := []TelegramAccess{}
	for _, space := range spaces {
		target, ok := channelTarget(space)
		if !ok || target.Source != SpaceSourceTelegram || bot != "" && target.ApplicationID != bot {
			continue
		}
		grant, _, err := telegramAccessRaw(ctx, tasks.core.Store.db, target)
		if err != nil {
			return nil, err
		}
		if grant.SpaceID != space.ID {
			grant = TelegramAccess{Target: target, SpaceID: space.ID, Kind: space.ChatKind}
		}
		rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT DISTINCT (body_json #>> '{Channel,person_id}') FROM fa.messages WHERE (body_json #>> '{Channel,source}')='telegram' AND (body_json #>> '{Channel,application_id}')=$1 AND (body_json #>> '{Channel,chat_id}')=$2 LIMIT 129`, target.ApplicationID, target.ChatID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var person sql.NullString
			if err := rows.Scan(&person); err != nil {
				rows.Close()
				return nil, err
			}
			if person.Valid && validTelegramPerson(person.String) {
				grant.VerifiedPeople = append(grant.VerifiedPeople, person.String)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		slices.Sort(grant.VerifiedPeople)
		permission, err := tasks.SpacePermission(ctx, space.ID)
		if err != nil {
			return nil, err
		}
		grant.SpacePermission = permission.Mode
		grant.EffectivePermissions = map[string]PermissionMode{}
		for _, id := range grant.Members {
			person, err := tasks.PersonPermission(ctx, id)
			if err != nil {
				return nil, err
			}
			grant.People = append(grant.People, person)
			grant.EffectivePermissions[id] = lowerPermission(permission.Mode, person.Mode)
		}
		result = append(result, grant)
	}
	return result, nil
}

func (service *ControlService) buildTelegramAccess(ctx context.Context, record *controlRecord) error {
	var args TelegramAccessChange
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	store := service.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	store.expireTelegramRequests(time.Now())
	if args.RequestCode != "" {
		for _, request := range store.telegramRequests {
			if strings.EqualFold(request.Code, strings.TrimSpace(args.RequestCode)) {
				args.RequestID = request.ID
			}
		}
		if args.RequestID == "" {
			return ErrControlExpired
		}
		args.RequestCode = ""
	}
	if args.RequestID != "" {
		request, ok := store.telegramRequests[args.RequestID]
		if !ok {
			return ErrControlExpired
		}
		if args.Target != (ChannelTarget{}) && args.Target != request.Target {
			return ErrPermissionDenied
		}
		args.Target, args.Kind = request.Target, request.Kind
		if !slices.Contains(args.Members, request.PersonID) {
			args.Members = append(args.Members, request.PersonID)
		}
	}
	if !validTelegramTarget(args.Target) || !store.telegramBots[args.Target.ApplicationID] || args.Kind != "private" && args.Kind != "group" || len(args.Members) > 128 || args.Kind == "private" && args.WholeGroup {
		return ErrPermissionDenied
	}
	slices.Sort(args.Members)
	args.Members = slices.Compact(args.Members)
	for _, member := range args.Members {
		if !validTelegramPerson(member) {
			return ErrPermissionDenied
		}
	}
	if args.Enabled && !args.WholeGroup && len(args.Members) == 0 || args.Kind == "private" && len(args.Members) != 1 {
		return errors.New("telegram: private access requires one verified person; a member-scoped group needs at least one member")
	}
	if args.Title == "" {
		args.Title = "Telegram " + args.Target.ChatID
	}
	if validateSpaceTitle(args.Title) != nil {
		return ErrInvalidSpace
	}
	_, raw, err := telegramAccessRaw(ctx, store.db, args.Target)
	if err != nil {
		return err
	}
	resource := "telegram.access:" + args.Target.key()
	record.Operation.ResourceIDs = []string{resource}
	record.Operation.RevertClass = "none"
	record.Before = map[string]json.RawMessage{resource: mustJSON(raw)}
	record.After = map[string]json.RawMessage{resource: mustJSON(args)}
	record.Arguments = mustJSON(args)
	record.Operation.Preview = fmt.Sprintf("Telegram Bot %s; %s chat %s; enabled=%t; all group members=%t; allowed people=%s. Grants CHAT ONLY; space and person tool permissions still apply. Disabling retains history and prevents further dispatch/delivery.", args.Target.ApplicationID, args.Kind, args.Target.ChatID, args.Enabled, args.WholeGroup, strings.Join(args.Members, ", "))
	return nil
}

func (service *ControlService) applyTelegramAccess(ctx context.Context, record *controlRecord) error {
	var args TelegramAccessChange
	if json.Unmarshal(record.Arguments, &args) != nil {
		return ErrInvalidSpace
	}
	store := service.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	if !store.telegramBots[args.Target.ApplicationID] {
		return ErrControlStaleRevision
	}
	if args.RequestID != "" {
		r, ok := store.telegramRequests[args.RequestID]
		if !ok || !time.Now().Before(r.ExpiresAt) || r.Target != args.Target {
			return ErrControlExpired
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, raw, err := telegramAccessRaw(ctx, tx, args.Target)
	if err != nil {
		return err
	}
	if string(mustJSON(raw)) != string(record.Before[record.Operation.ResourceIDs[0]]) {
		return ErrControlStaleRevision
	}
	space, err := NewTaskService(service.core).bindChannelTx(ctx, tx, args.Target, args.Kind, args.Title)
	if err != nil {
		return err
	}
	if space.ChatKind != args.Kind {
		return ErrInvalidSpace
	}
	if previous.SpaceID == space.ID && previous.Enabled == args.Enabled && previous.WholeGroup == args.WholeGroup && slices.Equal(previous.Members, args.Members) {
		delete(store.telegramRequests, args.RequestID)
		record.ResultData = mustJSON(previous)
		return tx.Commit()
	}
	grant := TelegramAccess{Target: args.Target, SpaceID: space.ID, Kind: args.Kind, Enabled: args.Enabled, WholeGroup: args.WholeGroup, Members: args.Members, Revision: previous.Revision + 1, UpdatedAt: nowText()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, telegramAccessKey(args.Target), string(mustJSON(grant))); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(ErrUnknownResult, err)
	}
	delete(store.telegramRequests, args.RequestID)
	record.ResultData = mustJSON(grant)
	// The new revision is checked before every dispatch and send, including
	// after a re-grant. Old callbacks and frozen Runs cannot become valid again.
	chats, err := NewTaskService(service.core).SpaceConversations(ctx, space.ID)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		NewTaskService(service.core).cancelConversationWithReason(chat.ID, "authorization_revoked")
	}
	if err := NewTaskService(service.core).stopPermissionCommands(ctx, space.ID, ""); err != nil {
		service.core.event(Event{Kind: EventTelegramAccessStopFailed, Message: safeError(err)})
	}
	return nil
}

func (tasks *TaskService) checkTelegramRun(ctx context.Context, run Run) error {
	origin := channelOriginFromJSON(run.ConfigJSON)
	if origin == nil || origin.Source != SpaceSourceTelegram {
		return nil
	}
	var snapshot struct{ Permission *PermissionSelection }
	if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil || snapshot.Permission == nil {
		return ErrPermissionDenied
	}
	// Local administrator tasks retain their own identity. Their result is not
	// automatically published as a Telegram requester's reply.
	if snapshot.Permission.PersonID == "" {
		return nil
	}
	store := tasks.core.Store
	store.telegramMu.Lock()
	active := store.telegramBots[origin.ApplicationID]
	registered := len(store.telegramBots)
	store.telegramMu.Unlock()
	if registered > 0 && !active {
		return ErrPermissionRevoked
	}
	grant, err := tasks.CheckTelegramAccess(ctx, origin.ChannelTarget, snapshot.Permission.PersonID)
	if err != nil || origin.AccessRevision == 0 || grant.Revision != origin.AccessRevision {
		return ErrPermissionRevoked
	}
	return nil
}

func (s *channelSession) sendRunText(ctx context.Context, run Run, target ChannelTarget, reply, text string, buttons []channelButton) (string, error) {
	if target.Source != SpaceSourceTelegram {
		return s.send(ctx, target, reply, text, buttons)
	}
	// Serialize admission commit with the actual transport dispatch. A send
	// already dispatched before revocation may still have side effects.
	store := s.tasks.core.Store
	store.telegramMu.Lock()
	release := store.telegramMu.Unlock
	defer func() {
		if release != nil {
			release()
		}
	}()
	var snapshot struct {
		Permission *PermissionSelection
		Channel    *ChannelRunOrigin
	}
	if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil || snapshot.Permission == nil || snapshot.Channel == nil {
		return "", ErrPermissionDenied
	}
	grant, err := s.tasks.telegramDeliveryGrant(ctx, target, snapshot.Permission.PersonID, snapshot.Permission.PersonID == "")
	if err != nil || snapshot.Channel.AccessRevision != grant.Revision || !store.telegramBots[target.ApplicationID] {
		return "", &CallError{Certain: true, Message: ErrPermissionRevoked.Error()}
	}
	ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: target, Person: snapshot.Permission.PersonID, Revision: grant.Revision, LocalOwner: snapshot.Permission.PersonID == ""})
	if s.remote {
		release()
		release = nil
	}
	return s.send(ctx, target, reply, text, buttons)
}

// Approval is durable before this notification. The receipt prevents an
// uncertain notification from being sent again by a repeated admin request.
func (s *channelSession) NotifyAccess(ctx context.Context, grant TelegramAccess) error {
	if !grant.Enabled || len(grant.Members) == 0 {
		return nil
	}
	key := "telegram/access-notify/" + grant.Target.key() + "/" + strconv.FormatUint(grant.Revision, 10)
	claim, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, `{"state":"dispatching"}`)
	if err != nil {
		return err
	}
	n, _ := claim.RowsAffected()
	if n == 0 {
		return nil
	}
	ctx = context.WithValue(ctx, channelDeliveryKey{}, channelDeliveryInfo{Claim: key})
	id, sendErr := s.plainAccessNotice(ctx, grant, "管理员已批准聊天接入。工具能力仍受空间和人员权限限制；发送 /start 查看帮助。")
	state := "sent"
	if isChannelQueued(sendErr) {
		state, sendErr = "queued", nil
	} else if sendErr != nil || id == "" {
		state = "unknown"
		var call *CallError
		if errors.As(sendErr, &call) && call.Certain {
			state = "failed"
		}
	}
	persist, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_, saveErr := s.tasks.core.Store.db.ExecContext(persist, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND (document #>> '{state}')='dispatching'`, string(mustJSON(map[string]string{"state": state})), key)
	return errors.Join(sendErr, saveErr)
}

func (s *channelSession) plainAccessNotice(ctx context.Context, grant TelegramAccess, text string) (string, error) {
	store := s.tasks.core.Store
	store.telegramMu.Lock()
	release := store.telegramMu.Unlock
	defer func() {
		if release != nil {
			release()
		}
	}()
	current, err := s.tasks.CheckTelegramAccess(ctx, grant.Target, grant.Members[0])
	if err != nil || current.Revision != grant.Revision || !store.telegramBots[grant.Target.ApplicationID] {
		return "", &CallError{Certain: true, Message: ErrPermissionRevoked.Error()}
	}
	ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: grant.Target, Person: grant.Members[0], Revision: grant.Revision})
	if s.remote {
		release()
		release = nil
	}
	return s.send(ctx, grant.Target, "", text, nil)
}

type telegramFileAuthorityKey struct{}

type telegramDispatchLockedKey struct{}

func (service *Service) lockTelegramDispatch(ctx context.Context, runID string) (func(), error) {
	if service.channel == nil || service.channel.Source != SpaceSourceTelegram || service.permission == nil || service.permission.PersonID == "" {
		return func() {}, nil
	}
	store := service.Store
	store.telegramMu.Lock()
	grant, err := NewTaskService(service).CheckTelegramAccess(ctx, service.channel.ChannelTarget, service.permission.PersonID)
	if err != nil || grant.Revision != service.channel.AccessRevision || !store.telegramBots[service.channel.ApplicationID] {
		store.telegramMu.Unlock()
		return nil, ErrPermissionRevoked
	}
	if err := ctx.Err(); err != nil {
		store.telegramMu.Unlock()
		return nil, err
	}
	return store.telegramMu.Unlock, nil
}

type telegramFileAuthority struct {
	LocalOwner bool
	Target     ChannelTarget
	Person     string
	Revision   uint64
}

func (s *channelSession) withTelegramFileAccess(ctx context.Context, target ChannelTarget, binding string, send func() error) error {
	if target.Source != SpaceSourceTelegram {
		return send()
	}
	store := s.tasks.core.Store
	store.telegramMu.Lock()
	release := store.telegramMu.Unlock
	defer func() {
		if release != nil {
			release()
		}
	}()
	authority, ok := ctx.Value(telegramFileAuthorityKey{}).(telegramFileAuthority)
	if !ok {
		run, err := store.GetRun(ctx, binding)
		if err != nil {
			return err
		}
		origin := channelOriginFromJSON(run.ConfigJSON)
		if origin == nil || run.Permission == nil {
			return ErrPermissionDenied
		}
		authority = telegramFileAuthority{Target: origin.ChannelTarget, Person: run.Permission.PersonID, Revision: origin.AccessRevision, LocalOwner: run.Permission.PersonID == ""}
	}
	if authority.Target != target || !store.telegramBots[target.ApplicationID] {
		return &CallError{Certain: true, Message: ErrPermissionRevoked.Error()}
	}
	grant, err := s.tasks.telegramDeliveryGrant(ctx, target, authority.Person, authority.LocalOwner)
	if err != nil || grant.Revision != authority.Revision {
		return &CallError{Certain: true, Message: ErrPermissionRevoked.Error()}
	}
	if s.remote {
		release()
		release = nil
	}
	return send()
}

// A local Owner may continue a channel conversation without impersonating a
// platform person. Publication still requires its current enabled space grant.
func (tasks *TaskService) telegramDeliveryGrant(ctx context.Context, target ChannelTarget, person string, local bool) (TelegramAccess, error) {
	if !local {
		return tasks.CheckTelegramAccess(ctx, target, person)
	}
	if person != "" {
		return TelegramAccess{}, ErrPermissionDenied
	}
	grant, _, err := telegramAccessRaw(ctx, tasks.core.Store.db, target)
	if err != nil {
		return grant, err
	}
	if !grant.Enabled || grant.Revision == 0 {
		return grant, ErrPermissionDenied
	}
	return grant, nil
}
