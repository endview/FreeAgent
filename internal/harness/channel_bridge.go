package harness

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

// ChannelBridge contains the Core's authorized business sessions and delivery
// receipts. It never opens platform connections or gives a plugin DB access.
type ChannelBridge struct {
	// Optional in-process transport preparation seam. Default products use
	// the authenticated channel process; tests can supply a controlled adapter.
	preparer                   func(context.Context, channelwire.Operation) (channelwire.Prepared, error)
	tasks                      *TaskService
	mu                         sync.Mutex
	connections                map[string]*channelConnection
	coreID, generation, origin string
	dispatch                   ChannelDispatch
}
type channelConnection struct {
	capabilities channelwire.Capabilities
	inboundMu    sync.Mutex
	flightMu     sync.Mutex
	currentEvent string
	flights      map[string]bool
	config       channelwire.Connection
	token        string
	session      *channelSession
	lastSeen     time.Time
}

func NewChannelBridge(tasks *TaskService, origin, generation string, dispatch ChannelDispatch) (*ChannelBridge, error) {
	if tasks == nil || generation == "" {
		return nil, errors.New("channel: Core identity is required")
	}
	b := &ChannelBridge{tasks: tasks, origin: origin, generation: generation, dispatch: dispatch, connections: map[string]*channelConnection{}}
	if err := tasks.core.Store.db.QueryRowContext(context.Background(), `SELECT value FROM fa.meta WHERE key='core/id'`).Scan(&b.coreID); err != nil {
		return nil, err
	}
	if err := b.Configure(); err != nil {
		return nil, err
	}
	tasks.core.Store.channelBridge.Store(b)
	return b, nil
}

func (b *ChannelBridge) Configure() error {
	config := b.tasks.Config().Channels
	if err := config.validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, c := range b.connections {
		keep := false
		for _, next := range config.Connections {
			if next == c.config && next.Enabled {
				keep = true
			}
		}
		if !keep {
			delete(b.connections, id)
			_ = os.Remove(filepath.Join(b.tasks.DataDir(), "channel-runtime", id+".json"))
		}
	}
	for _, c := range config.Connections {
		if !c.Enabled {
			continue
		}
		if _, ok := b.connections[c.ID]; ok {
			continue
		}
		profile, err := b.tasks.ChannelKeys().Find(c.Credential)
		if err != nil {
			return err
		}
		if profile.Protocol != c.Source {
			return ErrPermissionDenied
		}
		conn := &channelConnection{config: c, token: newID() + newID(), flights: map[string]bool{}}
		conn.session = newChannelSession(b.tasks, c.Source, func(ctx context.Context, target ChannelTarget, reply, text string, buttons []channelButton) (string, error) {
			op := channelwire.Operation{Kind: "text", Target: channelwire.Target(target), Reply: reply, Text: text}
			for _, button := range buttons {
				op.Buttons = append(op.Buttons, channelwire.Button{Text: button.Text, Token: button.Token})
			}
			result, err := b.perform(ctx, c.ID, op)
			if isChannelQueued(err) {
				return result.ID, err
			}
			return result.MessageID, err
		})
		conn.session.reset(c.ApplicationID)
		conn.session.remote = true
		if b.dispatch != nil {
			conn.session.setDispatch(func(chat string, call func(context.Context) (RunResult, error)) error {
				conn.flightMu.Lock()
				event := conn.currentEvent
				conn.flights[event] = true
				conn.flightMu.Unlock()
				err := b.dispatch(chat, func(ctx context.Context) (RunResult, error) {
					defer func() { conn.flightMu.Lock(); delete(conn.flights, event); conn.flightMu.Unlock() }()
					ctx = context.WithValue(ctx, channelEventKey{}, c.ID+"/"+event)
					result, err := call(ctx)
					b.completeInbound(c.ID, event, result, err)
					return result, err
				})
				if err != nil {
					conn.flightMu.Lock()
					delete(conn.flights, event)
					conn.flightMu.Unlock()
				}
				return err
			})
		}
		conn.session.buttons = c.Source != "qq"
		conn.session.download = func(ctx context.Context, e channelEvent, media channelwire.MediaRef, limit int64) ([]byte, error) {
			op := channelwire.Operation{Kind: "download", Target: channelwire.Target(e.Target), Reply: e.Message, Media: &media, Limit: limit}
			result, err := b.perform(ctx, c.ID, op)
			return result.Data, err
		}
		conn.session.sendFile = func(ctx context.Context, target ChannelTarget, reply string, ref AttachmentRef, _ []byte) (string, error) {
			space, err := b.tasks.ChannelSpace(ctx, target)
			if err != nil {
				return "", err
			}
			op := channelwire.Operation{Kind: "file", Target: channelwire.Target(target), Reply: reply, Name: ref.Name, AttachmentID: ref.ID, ConversationID: space.ActiveConversationID}
			result, err := b.perform(ctx, c.ID, op)
			if isChannelQueued(err) {
				return result.ID, err
			}
			return result.MessageID, err
		}
		b.connections[c.ID] = conn
		bootstrap := channelwire.Bootstrap{Version: channelwire.Version, CoreID: b.coreID, Generation: b.generation, Origin: b.origin, Connection: c, Token: conn.token}
		dir := filepath.Join(b.tasks.DataDir(), "channel-runtime")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, c.ID+".json"), mustJSON(bootstrap), 0600); err != nil {
			return err
		}
	}
	var bots []string
	for _, conn := range b.connections {
		if conn.config.Source == SpaceSourceTelegram {
			bots = append(bots, conn.config.ApplicationID)
		}
	}
	b.tasks.core.Store.telegramIdentities(bots)
	return nil
}

func (b *ChannelBridge) Authenticate(core, generation, token string) (string, error) {
	if core != b.coreID || generation != b.generation || len(token) != 64 {
		return "", ErrPermissionDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, c := range b.connections {
		if subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) == 1 {
			c.lastSeen = time.Now()
			return id, nil
		}
	}
	return "", ErrPermissionDenied
}
func (b *ChannelBridge) Credential(id string) (channelwire.Credential, error) {
	b.mu.Lock()
	c := b.connections[id]
	b.mu.Unlock()
	if c == nil {
		return channelwire.Credential{}, ErrPermissionDenied
	}
	key, err := b.tasks.ChannelKeys().Resolve(c.config.Credential)
	if err != nil {
		return channelwire.Credential{}, err
	}
	if key.Protocol != c.config.Source {
		return channelwire.Credential{}, ErrPermissionDenied
	}
	return channelwire.Credential{BaseURL: key.BaseURL, Secret: key.secret}, nil
}

func (b *ChannelBridge) Inbound(ctx context.Context, id string, event channelwire.Event) error {
	b.mu.Lock()
	conn := b.connections[id]
	b.mu.Unlock()
	if conn == nil || event.Target.Source != conn.config.Source || event.Target.ApplicationID != conn.config.ApplicationID || !validChannelPart(event.ID) {
		return ErrPermissionDenied
	}
	conn.inboundMu.Lock()
	defer conn.inboundMu.Unlock()
	conn.flightMu.Lock()
	conn.currentEvent = event.ID
	conn.flightMu.Unlock()
	e := channelEvent{Target: ChannelTarget(event.Target), Person: event.Person, Name: event.Name, Message: event.Message, Kind: event.Kind, Text: event.Text, Trigger: event.Trigger, Callback: event.Callback, CallbackMessage: event.CallbackMessage, Media: event.Media, ReplyTo: event.ReplyTo, Sticker: event.Sticker}
	ctx = context.WithValue(ctx, channelEventKey{}, id+"/"+event.ID)
	if event.Reaction != nil && event.Reaction.Anonymous && event.Reaction.CountSnapshot {
		// Anonymous counts have no person to admit. recordReaction checks the
		// enabled Bot/chat grant and the known message, without creating a Run.
		return b.recordReaction(ctx, id, event)
	}
	if event.Target.Source == SpaceSourceTelegram {
		if grant, err := b.tasks.CheckTelegramAccess(ctx, ChannelTarget(event.Target), event.Person); err == nil {
			ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: ChannelTarget(event.Target), Person: event.Person, Revision: grant.Revision})
		} else {
			ctx = context.WithValue(ctx, channelAdmissionKey{}, true)
		}
	}
	if admission, _ := ctx.Value(channelAdmissionKey{}).(bool); admission {
		if !event.Trigger || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(event.Text)), "/start") {
			return nil
		}
		err := conn.session.handle(ctx, e)
		if isChannelQueued(err) {
			return nil
		}
		return err
	}
	if event.Reaction != nil {
		return b.recordReaction(ctx, id, event)
	}
	return b.acceptInbound(ctx, conn, id, event, e)
}

func (b *ChannelBridge) Deliver(ctx context.Context, result RunResult) error {
	if result.Run.ID == "" {
		return nil
	}
	_, origin, err := b.tasks.ChannelRun(ctx, result.Run.ID)
	if err != nil || origin == nil {
		return err
	}
	b.mu.Lock()
	var session *channelSession
	for _, c := range b.connections {
		if c.config.Source == origin.Source && c.config.ApplicationID == origin.ApplicationID {
			session = c.session
			break
		}
	}
	b.mu.Unlock()
	if session == nil {
		return nil
	}
	if result.Run.Permission != nil && origin.Source == SpaceSourceTelegram {
		ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: origin.ChannelTarget, Person: result.Run.Permission.PersonID, Revision: origin.AccessRevision, LocalOwner: result.Run.Permission.PersonID == ""})
	}
	return session.Deliver(ctx, result)
}
func (b *ChannelBridge) NotifyAccess(ctx context.Context, grant TelegramAccess) error {
	b.mu.Lock()
	var session *channelSession
	for _, c := range b.connections {
		if c.config.Source == grant.Target.Source && c.config.ApplicationID == grant.Target.ApplicationID {
			session = c.session
			break
		}
	}
	b.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.NotifyAccess(ctx, grant)
}

func (b *ChannelBridge) PairCode(source, id string) (string, time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var session *channelSession
	for key, c := range b.connections {
		if c.config.Source == source && (id == "" || key == id) {
			if session != nil {
				return "", time.Time{}, errors.New("channel: select one connection")
			}
			session = c.session
		}
	}
	if session == nil {
		return "", time.Time{}, ErrPermissionDenied
	}
	return session.PairCode()
}
func (b *ChannelBridge) Status(source string) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var states []map[string]any
	for id, c := range b.connections {
		if source == "" || c.config.Source == source {
			states = append(states, map[string]any{"id": id, "source": c.config.Source, "application_id": c.config.ApplicationID, "enabled": true, "running": !c.lastSeen.IsZero() && time.Since(c.lastSeen) < 30*time.Second})
		}
	}
	return map[string]any{"connections": states, "config": b.tasks.Config().Channels, "protocol_version": channelwire.Version}
}

type channelEventKey struct{}
type channelAdmissionKey struct{}

const channelOperationPrefix = "channel/operation/"

func (b *ChannelBridge) perform(ctx context.Context, id string, op channelwire.Operation) (channelwire.Operation, error) {
	b.mu.Lock()
	c := b.connections[id]
	b.mu.Unlock()
	if c == nil || op.Target.Source != c.config.Source || op.Target.ApplicationID != c.config.ApplicationID {
		return op, ErrPermissionDenied
	}
	if channelwire.PurePreparation(op.Kind) && b.preparer != nil {
		prepared, err := b.preparer(ctx, op)
		if err != nil {
			return op, err
		}
		op.Data = mustJSON(prepared)
		if _, err := validatePrepared(op, op.Data); err != nil {
			return op, err
		}
		return op, nil
	}
	op.Connection = id
	if delivery, ok := ctx.Value(channelDeliveryKey{}).(channelDeliveryInfo); ok {
		op.ClaimKey, op.RunID, op.Maintenance = delivery.Claim, delivery.Run, delivery.Maintenance
		op.Group, op.Sequence, op.Total = delivery.Group, delivery.Sequence, delivery.Total
		if !channelwire.PurePreparation(op.Kind) {
			op.Entities, op.FormatVersion = delivery.Entities, delivery.Format
		}
		op.LogicalPart, op.PlatformPart = delivery.LogicalPart, delivery.PlatformPart
		op.MinGapMS = delivery.MinGapMS
		if op.ConversationID == "" {
			op.ConversationID = delivery.Chat
		}
	}
	if auth, ok := ctx.Value(telegramFileAuthorityKey{}).(telegramFileAuthority); ok {
		op.Person, op.AccessRevision, op.LocalOwner = auth.Person, auth.Revision, auth.LocalOwner
	}
	op.Admission, _ = ctx.Value(channelAdmissionKey{}).(bool)
	if op.Admission && (op.Kind != "text" || len(op.Text) > 2048 || len(op.Buttons) != 0) {
		return op, ErrPermissionDenied
	}
	op.State = "pending"
	if op.Proactive {
		op.State = "scheduled"
	} else if op.Kind == "sticker" && op.RunID != "" && op.Group == "" {
		op.State = "held"
	}
	op.CreatedAt = nowText()
	correlation, _ := ctx.Value(channelEventKey{}).(string)
	if correlation == "" {
		correlation = op.Target.ChatID + "/" + op.Reply
	}
	identity := op
	identity.CreatedAt = ""
	identity.State = ""
	op.ID = contentHash(mustJSON([]any{correlation, identity}))
	if op.CandidateID != "" {
		op.PayloadDigest = contentHash(mustJSON(identity))
		op.ID = contentHash(mustJSON([]any{id, op.ConversationID, op.RunID, op.Extension, op.CandidateID}))
	}
	if op.Group == "" {
		op.Group, op.Sequence, op.Total = op.ID, 1, 1
	}
	if len(op.Group) > 256 || hasControl(op.Group) || op.Total < 1 || op.Total > 256 || op.Sequence < 1 || op.Sequence > op.Total {
		return op, ErrRequestKey
	}
	tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	if err := lockChannelRun(ctx, tx, op.RunID); err != nil {
		return op, err
	}
	if op.Proactive {
		var pending int
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel/proactive-capacity',0))`); err != nil {
			return op, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'proactive'='true' AND document->>'state' IN ('scheduled','reviewing','pending')`).Scan(&pending); err != nil {
			return op, err
		}
		if pending >= 64 {
			return op, errors.New("harness: pending proactive delivery capacity reached")
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id+"/"+op.Group); err != nil {
		return op, err
	}
	prefix := channelOperationPrefix + id + "/"
	var conflict bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key >= $1 AND key < $2 AND document->>'group'=$3 AND (document->'target'<>$4::jsonb OR (document->>'total')::int<>$5 OR (document->>'sequence')::int=$6 AND document->>'id'<>$7))`, prefix, prefix+"\uffff", op.Group, string(mustJSON(op.Target)), op.Total, op.Sequence, op.ID).Scan(&conflict); err != nil {
		return op, err
	}
	if conflict {
		return op, ErrRequestKey
	}
	var paused bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, channelGroupPauseKey(id, op.Group)).Scan(&paused); err != nil {
		return op, err
	}
	if paused {
		op.State = "paused"
	}
	var cancelled bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key IN ($1,$2))`, channelGroupCancelKey(id, op.Group), "channel/run-cancelled/"+op.RunID).Scan(&cancelled); err != nil {
		return op, err
	}
	if cancelled {
		op.State = "cancelled"
	}
	key := channelOperationPrefix + id + "/" + op.ID
	if op.CandidateID != "" {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err == nil {
			var saved channelwire.Operation
			if json.Unmarshal([]byte(raw), &saved) != nil {
				return op, ErrStoreFormat
			}
			if saved.PayloadDigest != op.PayloadDigest {
				return op, ErrRequestKey
			}
			if err := tx.Commit(); err != nil {
				return op, err
			}
			return saved, &channelQueued{operation: saved.ID}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return op, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, string(mustJSON(op))); err != nil {
		return op, err
	}
	if err := associateWakeTx(ctx, tx, op); err != nil {
		return op, err
	}
	if err := tx.Commit(); err != nil {
		return op, err
	}
	if channelwire.PurePreparation(op.Kind) {
		var raw string
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
			return op, err
		}
		if json.Unmarshal([]byte(raw), &op) != nil {
			return op, ErrStoreFormat
		}
		if op.State == "downloaded" {
			data, err := os.ReadFile(filepath.Join(b.tasks.DataDir(), "channel-downloads", op.ID))
			if err != nil {
				return op, err
			}
			if contentHash(data) != op.DataSHA256 || int64(len(data)) != op.DataBytes {
				return op, ErrAttachment
			}
			op.Data = data
			return op, nil
		}
		if op.State == "failed" || op.State == "cancelled" {
			return op, &channelwire.Error{Certain: true}
		}
		return op, &channelQueued{operation: op.ID}
	}
	if op.Kind != "download" && op.Kind != "sticker_set" {
		return op, &channelQueued{operation: op.ID}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var raw string
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
			return op, err
		}
		if json.Unmarshal([]byte(raw), &op) != nil {
			return op, ErrStoreFormat
		}
		switch op.State {
		case "sent":
			return op, nil
		case "downloaded":
			path := filepath.Join(b.tasks.DataDir(), "channel-downloads", op.ID)
			data, err := os.ReadFile(path)
			if err == nil && (contentHash(data) != op.DataSHA256 || int64(len(data)) != op.DataBytes) {
				return op, ErrAttachment
			}
			op.Data = data
			return op, err
		case "failed":
			return op, &channelwire.Error{Certain: true}
		case "unknown":
			return op, &channelwire.Error{}
		}
		select {
		case <-ctx.Done():
			return op, &channelwire.Error{}
		case <-ticker.C:
		}
	}
}

func (b *ChannelBridge) BootstrapPaths() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var paths []string
	for id := range b.connections {
		paths = append(paths, filepath.Join(b.tasks.DataDir(), "channel-runtime", id+".json"))
	}
	return paths
}
func (b *ChannelBridge) Close() error {
	b.tasks.core.Store.channelBridge.CompareAndSwap(b, nil)
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.connections {
		_ = os.Remove(filepath.Join(b.tasks.DataDir(), "channel-runtime", id+".json"))
	}
	b.connections = map[string]*channelConnection{}
	b.tasks.core.Store.telegramIdentities(nil)
	return nil
}
