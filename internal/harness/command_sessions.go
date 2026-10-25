package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/winlaunch"
)

const commandMetaPrefix = "command/process/"
const directCommandPrefix = "command/direct/"

var ErrCommandBusy = errors.New("harness: execution_profile_busy; an owned command is still active")

type CommandOutput struct {
	Stream string `json:"stream"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	Text   string `json:"text"`
}
type CommandRecord struct {
	ID             string          `json:"id"`
	RunID          string          `json:"run_id"`
	ConversationID string          `json:"conversation_id"`
	Mode           string          `json:"mode"`
	Generation     string          `json:"owner_generation"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	Reason         string          `json:"stop_reason,omitempty"`
	Receipt        ToolReceipt     `json:"receipt"`
	ProfileHash    string          `json:"profile_sha256"`
	StartedAt      string          `json:"started_at"`
	ExpiresAt      string          `json:"expires_at"`
	ObservedAt     string          `json:"observed_at"`
	Output         []CommandOutput `json:"output,omitempty"`
	FirstCursor    int64           `json:"first_cursor"`
	NextCursor     int64           `json:"next_cursor"`
	TotalBytes     int64           `json:"total_bytes"`
	OutputGap      bool            `json:"output_gap"`
	Reviewed       bool            `json:"reviewed"`
	PID            int             `json:"pid"`
}

func (r CommandRecord) terminal() bool { return r.State != "running" && r.State != "stopping" }

type commandTail struct {
	mu            sync.Mutex
	chunks        []CommandOutput
	pending       [2]string
	bytes         int
	cursor, total int64
	gap           bool
	limit         int
}
type commandStream struct {
	tail   *commandTail
	stream int
}

func (w commandStream) finish() {
	t := w.tail
	t.mu.Lock()
	defer t.mu.Unlock()
	if text := t.pending[w.stream]; text != "" {
		t.gap = true
		t.append(w.stream, strings.ToValidUTF8(text, "\uFFFD"))
		t.pending[w.stream] = ""
	}
}

func (w commandStream) Write(raw []byte) (int, error) {
	t := w.tail
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total += int64(len(raw))
	text := t.pending[w.stream] + string(raw)
	end := len(text)
	for end > 0 && !utf8.ValidString(text[:end]) {
		if len(text)-end >= 4 {
			text = strings.ToValidUTF8(text, "\uFFFD")
			end = len(text)
			t.gap = true
			break
		}
		end--
	}
	t.pending[w.stream] = text[end:]
	t.append(w.stream, text[:end])
	return len(raw), nil
}
func (t *commandTail) append(stream int, text string) {
	if text == "" {
		return
	}
	name := []string{"stdout", "stderr"}[stream]
	start := t.cursor
	t.cursor += int64(len(text))
	if len(t.chunks) > 0 && t.chunks[len(t.chunks)-1].Stream == name && len(t.chunks[len(t.chunks)-1].Text)+len(text) <= 16<<10 {
		last := &t.chunks[len(t.chunks)-1]
		last.Text += text
		last.End = t.cursor
	} else {
		t.chunks = append(t.chunks, CommandOutput{name, start, t.cursor, text})
	}
	t.bytes += len(text)
	for t.bytes > t.limit || len(t.chunks) > 512 {
		excess := t.bytes - t.limit
		if excess > 0 && excess < len(t.chunks[0].Text) && len(t.chunks) <= 512 {
			cut := excess
			for cut < len(t.chunks[0].Text) && !utf8.RuneStart(t.chunks[0].Text[cut]) {
				cut++
			}
			t.chunks[0].Text = t.chunks[0].Text[cut:]
			t.chunks[0].Start += int64(cut)
			t.bytes -= cut
			break
		}
		t.bytes -= len(t.chunks[0].Text)
		t.chunks = t.chunks[1:]
		t.gap = true
	}
}
func (t *commandTail) snapshot(cursor int64, limit int) ([]CommandOutput, int64, int64, int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	first := t.cursor
	if len(t.chunks) > 0 {
		first = t.chunks[0].Start
	}
	gap := t.gap || cursor < first
	cursor = max(cursor, first)
	out := []CommandOutput{}
	size := 0
	next := cursor
	for _, chunk := range t.chunks {
		if chunk.End <= cursor {
			continue
		}
		offset := int(max(0, cursor-chunk.Start))
		for offset < len(chunk.Text) && !utf8.RuneStart(chunk.Text[offset]) {
			offset++
		}
		text := truncateUTF8Bytes(chunk.Text[offset:], max(0, limit-size))
		if text == "" {
			break
		}
		out = append(out, CommandOutput{chunk.Stream, chunk.Start + int64(offset), chunk.Start + int64(offset+len(text)), text})
		size += len(text)
		next = out[len(out)-1].End
		segments := 128
		if limit > 16<<10 {
			segments = 512
		}
		if size >= limit || len(out) >= segments {
			break
		}
	}
	return out, first, next, t.total, gap
}

type ownedCommand struct {
	mu          sync.Mutex
	record      CommandRecord
	process     *winlaunch.Process
	cancel      context.CancelFunc
	done        chan struct{}
	tail        *commandTail
	deadline    time.Time
	drains      sync.WaitGroup
	releaseHost func()
	stopped     bool
}
type commandSupervisor struct {
	emit       EventFunc
	mu         sync.Mutex
	store      *Store
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	active     map[string]*ownedCommand
	closed     bool
}

func (s *commandSupervisor) event(r CommandRecord) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(Event{Kind: "command_changed", Purpose: "command", ConversationID: r.ConversationID, RunID: r.RunID, AttemptID: r.ID, Message: r.State})
	}
}

func newCommandSupervisor(ctx context.Context, store *Store) (*commandSupervisor, error) {
	owner, cancel := context.WithCancel(ctx)
	s := &commandSupervisor{store: store, generation: newID(), ctx: owner, cancel: cancel, active: map[string]*ownedCommand{}}
	rows, err := store.db.QueryContext(ctx, `SELECT key,value FROM meta WHERE key LIKE 'command/process/%'`)
	if err != nil {
		cancel()
		return nil, err
	}
	type recovery struct {
		key    string
		record CommandRecord
	}
	var pending []recovery
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			cancel()
			return nil, err
		}
		var r CommandRecord
		if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &r) != nil || r.ID == "" {
			rows.Close()
			cancel()
			return nil, ErrStoreFormat
		}
		if !r.terminal() {
			r.State, r.Reason, r.Receipt.Status = "unknown", "owner_lost", "unknown"
			r.Receipt.Cleanup = "unknown"
			r.OutputGap = true
			r.Reviewed = false
			r.Revision++
			r.ObservedAt = nowText()
			pending = append(pending, recovery{key, r})
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		cancel()
		return nil, err
	}
	for _, item := range pending {
		if err := s.save(ctx, item.record); err != nil {
			cancel()
			return nil, err
		}
	}
	return s, nil
}
func (s *commandSupervisor) save(ctx context.Context, r CommandRecord) error {
	raw := mustJSON(r)
	if len(raw) > 2<<20 {
		return ErrContextLimit
	}
	_, err := s.store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, commandMetaPrefix+r.ID, string(raw))
	return err
}
func (s *commandSupervisor) reserve(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.active) != 0 {
		return nil, ErrCommandBusy
	}
	s.active[id] = nil
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }) }, nil
}
func (s *commandSupervisor) list(ctx context.Context) ([]CommandRecord, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'command/process/%' ORDER BY rowid DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	var records []CommandRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var r CommandRecord
		if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &r) != nil {
			rows.Close()
			return nil, ErrStoreFormat
		}
		records = append(records, r)
		records[len(records)-1].Output = nil
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	current := make(map[string]*ownedCommand, len(s.active))
	for id, p := range s.active {
		current[id] = p
	}
	s.mu.Unlock()
	seen := map[string]bool{}
	for i := range records {
		seen[records[i].ID] = true
		if p := current[records[i].ID]; p != nil {
			p.mu.Lock()
			records[i] = p.record
			p.mu.Unlock()
			records[i].Output = nil
		}
	}
	for id, p := range current {
		if p != nil && !seen[id] {
			p.mu.Lock()
			r := p.record
			p.mu.Unlock()
			r.Output = nil
			records = append(records, r)
		}
	}
	if records == nil {
		records = []CommandRecord{}
	}
	return records, nil
}
func (s *commandSupervisor) poll(ctx context.Context, id string, cursor int64, limit int) (CommandRecord, error) {
	if cursor < 0 || limit < 1 || limit > 16<<10 {
		return CommandRecord{}, errors.New("invalid output cursor or page bound")
	}
	s.mu.Lock()
	p := s.active[id]
	s.mu.Unlock()
	if p != nil {
		p.mu.Lock()
		r := p.record
		p.mu.Unlock()
		r.Output, r.FirstCursor, r.NextCursor, r.TotalBytes, r.OutputGap = p.tail.snapshot(cursor, limit)
		r.ObservedAt = nowText()
		return r, nil
	}
	var raw string
	if err := s.store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, commandMetaPrefix+id).Scan(&raw); err != nil {
		return CommandRecord{}, err
	}
	var r CommandRecord
	if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &r) != nil {
		return r, ErrStoreFormat
	}
	chunks := r.Output
	r.Output = nil
	size := 0
	r.OutputGap = r.OutputGap || cursor < r.FirstCursor
	r.NextCursor = max(cursor, r.FirstCursor)
	for _, chunk := range chunks {
		if chunk.End <= cursor {
			continue
		}
		offset := int(max(0, cursor-chunk.Start))
		for offset < len(chunk.Text) && !utf8.RuneStart(chunk.Text[offset]) {
			offset++
		}
		text := truncateUTF8Bytes(chunk.Text[offset:], limit-size)
		if text == "" {
			break
		}
		chunk.Start += int64(offset)
		chunk.Text = text
		chunk.End = chunk.Start + int64(len(text))
		r.Output = append(r.Output, chunk)
		r.NextCursor = chunk.End
		size += len(text)
		if size >= limit || len(r.Output) >= 128 {
			break
		}
	}
	return r, nil
}
func (s *commandSupervisor) stop(ctx context.Context, id, reason string) (CommandRecord, error) {
	s.mu.Lock()
	p := s.active[id]
	s.mu.Unlock()
	if p == nil {
		return s.poll(ctx, id, 0, 16<<10)
	}
	p.mu.Lock()
	if !p.record.terminal() && !p.stopped {
		p.stopped = true
		p.record.State = "stopping"
		p.record.Reason = reason
		p.record.Revision++
		p.cancel()
	}
	done := p.done
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return CommandRecord{}, ctx.Err()
	case <-done:
		return s.poll(ctx, id, 0, 16<<10)
	}
}
func (s *commandSupervisor) close(reason string) error {
	s.mu.Lock()
	s.closed = true
	ids := make([]string, 0, len(s.active))
	for id, p := range s.active {
		if p != nil {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	sort.Strings(ids)
	var failures []error
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		r, err := s.stop(ctx, id, reason)
		cancel()
		if err != nil || r.State == "unknown" {
			failures = append(failures, errors.Join(err, ErrUnknownResult))
		}
	}
	s.cancel()
	return errors.Join(failures...)
}
func (s *commandSupervisor) hasActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active) > 0
}

func (s *commandSupervisor) launch(parent context.Context, tools *CommandTools, args commandArguments, receipt ToolReceipt, inv commandInvocation) (ToolResult, error) {
	if inv.attempt == "" || inv.service.guard != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "background commands require an ordinary owner Run"}
	}
	release, err := s.reserve(inv.attempt)
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
	}
	keep := false
	defer func() {
		if !keep {
			release()
		}
	}()
	if parent.Err() != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cancelled before background start"}
	}
	ctx, cancel := context.WithCancel(s.ctx)
	if tools.executionMode() == CommandExecutionHostExplicit && tools.Gate != nil {
		if err := tools.Gate.beginHost(); err != nil {
			cancel()
			return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
		}
	}
	releaseHost := func() {
		if tools.executionMode() == CommandExecutionHostExplicit && tools.Gate != nil {
			tools.Gate.endHost()
		}
	}
	process, err := tools.startProcess(ctx, args, receipt)
	if err != nil {
		cancel()
		releaseHost()
		return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
	}
	run, err := s.store.GetRun(context.Background(), inv.run)
	if err != nil {
		process.Kill()
		process.Close()
		cancel()
		releaseHost()
		return ToolResult{}, &CallError{Message: "background started but Run metadata unavailable"}
	}
	now := time.Now()
	receipt.Status = "started"
	receipt.CommandID = inv.attempt
	receipt.Cleanup = "owned"
	r := CommandRecord{ID: inv.attempt, RunID: run.ID, ConversationID: run.ConversationID, Mode: "background", Generation: s.generation, Revision: 1, State: "running", Receipt: receipt, ProfileHash: contentHash(mustJSON(tools.Profile)), StartedAt: now.UTC().Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Duration(tools.Config.TimeoutSecs) * time.Second).UTC().Format(time.RFC3339Nano), ObservedAt: nowText()}
	p := &ownedCommand{record: r, process: process, cancel: cancel, done: make(chan struct{}), tail: &commandTail{limit: min(tools.Config.MaxOutput, 256<<10)}, deadline: now.Add(time.Duration(tools.Config.TimeoutSecs) * time.Second), releaseHost: releaseHost}
	p.record.PID = process.PID()
	r = p.record
	s.mu.Lock()
	s.active[r.ID] = p
	s.mu.Unlock()
	p.drains.Add(2)
	for i, reader := range []io.Reader{process.Stdout(), process.Stderr()} {
		go func(stream int, reader io.Reader) {
			defer p.drains.Done()
			writer := commandStream{p.tail, stream}
			defer writer.finish()
			_, _ = io.Copy(writer, reader)
		}(i, reader)
	}
	_ = process.Stdin().Close()
	persist, done := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.save(persist, r)
	done()
	keep = true
	go s.supervise(ctx, p, release)
	if err != nil {
		p.mu.Lock()
		p.record.Reason = "startup_persistence_failed"
		p.mu.Unlock()
		cancel()
		return ToolResult{Receipts: []ToolReceipt{receipt}}, &CallError{Message: "background startup projection could not be persisted; cleanup requested"}
	}
	s.event(r)
	return ToolResult{Content: string(mustJSON(r)), Receipts: []ToolReceipt{receipt}}, nil
}
func (s *commandSupervisor) supervise(ctx context.Context, p *ownedCommand, release func()) {
	defer close(p.done)
	waitCtx, stop := context.WithDeadline(ctx, p.deadline)
	exit, err := p.process.Wait(waitCtx)
	stop()
	p.mu.Lock()
	reason := p.record.Reason
	if reason == "" {
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "deadline"
		} else if err != nil {
			reason = "owner_shutdown"
		} else {
			reason = "process_exit"
		}
	}
	p.record.State = "stopping"
	p.record.Reason = reason
	p.mu.Unlock()
	if err != nil {
		p.process.Terminate()
		grace, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, _ = p.process.Wait(grace)
		cancel()
	}
	cleanup := p.process.Kill()
	final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	observed, waitErr := p.process.Wait(final)
	cleanup = errors.Join(cleanup, p.process.WaitTree(final))
	cancel()
	if waitErr == nil {
		exit = observed
	}
	drained := make(chan struct{})
	go func() { p.drains.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		cleanup = errors.Join(cleanup, errors.New("output not drained"))
	}
	cleanup = errors.Join(cleanup, p.process.Close())
	p.cancel()
	p.mu.Lock()
	p.record.Receipt.Cleanup = "confirmed"
	p.record.State = "completed"
	if exit.Code != 0 {
		p.record.State = "failed"
	}
	if reason != "process_exit" {
		p.record.State = "stopped"
	}
	if reason == "deadline" {
		p.record.State = "timed_out"
	}
	if waitErr != nil || cleanup != nil {
		p.record.State = "unknown"
		p.record.Receipt.Cleanup = "unknown"
	} else {
		p.record.Receipt.ExitCode = &exit.Code
	}
	p.record.Receipt.Status = p.record.State
	p.record.Revision++
	p.record.ObservedAt = nowText()
	p.record.Output, p.record.FirstCursor, p.record.NextCursor, p.record.TotalBytes, p.record.OutputGap = p.tail.snapshot(0, 256<<10)
	r := p.record
	p.mu.Unlock()
	persist, done := context.WithTimeout(context.Background(), 5*time.Second)
	saveErr := s.save(persist, r)
	done()
	if saveErr != nil {
		p.mu.Lock()
		p.record.State = "unknown"
		p.record.Reason = "terminal_persistence_failed"
		p.mu.Unlock()
	}
	if r.State != "unknown" && saveErr == nil {
		p.releaseHost()
		release()
	}
	s.event(r)
}

type commandInvocation struct {
	service      *Service
	run, attempt string
}
type commandInvocationKey struct{}
type commandInvocationTools struct {
	base ToolRunner
	inv  commandInvocation
}

func (t commandInvocationTools) Definitions() []ToolDefinition {
	defs := append([]ToolDefinition(nil), t.base.Definitions()...)
	if t.inv.service.commands != nil && t.inv.service.guard == nil && !t.inv.service.commandManagementDisabled {
		for _, name := range []string{"command_poll", "command_stop"} {
			effect := EffectRead
			if name == "command_stop" {
				effect = EffectModify
			}
			defs = append(defs, ToolDefinition{Name: name, Effect: effect, Description: "Inspect or stop an owner-bound command by its original Attempt ID. Stop cannot undo effects; UNKNOWN is never replayed.", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"cursor":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1,"maximum":16384}},"required":["id"],"additionalProperties":false}`)})
		}
	}
	return defs
}
func (t commandInvocationTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if call.Name == "command_poll" || call.Name == "command_stop" {
		if t.inv.service.commands == nil || t.inv.service.guard != nil || t.inv.service.commandManagementDisabled {
			return PreparedTool{}, ErrToolDisabled
		}
		var args struct {
			ID     string `json:"id"`
			Cursor int64  `json:"cursor"`
			Max    int    `json:"max_bytes"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return PreparedTool{}, err
		}
		if args.Max == 0 {
			args.Max = 16 << 10
		}
		s := t.inv.service.commands
		r, err := s.poll(ctx, args.ID, args.Cursor, args.Max)
		if err != nil {
			return PreparedTool{}, err
		}
		effect := EffectRead
		if call.Name == "command_stop" {
			effect = EffectModify
		}
		confirm := false
		if call.Name == "command_stop" {
			run, err := s.store.GetRun(ctx, t.inv.run)
			confirm = err != nil || run.ConversationID != r.ConversationID
		}
		return PreparedTool{Effect: effect, userConfirmation: confirm, Preview: fmt.Sprintf("%s %s (%s); effects are not undone", call.Name, r.ID, r.Receipt.Command), Items: []ApprovalItem{{Kind: "command_stop", Target: r.ID, Hash: contentHash(mustJSON(struct{ ID, Generation string }{r.ID, r.Generation}))}}, Execute: func(ctx context.Context) (ToolResult, error) {
			var result CommandRecord
			var err error
			if call.Name == "command_stop" {
				result, err = s.stop(ctx, args.ID, "user_stop")
			} else {
				result, err = s.poll(ctx, args.ID, args.Cursor, args.Max)
			}
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: safeError(err)}
			}
			return jsonResult(result)
		}}, nil
	}
	return t.base.Prepare(context.WithValue(ctx, commandInvocationKey{}, t.inv), call)
}

func (tasks *TaskService) Commands(ctx context.Context) ([]CommandRecord, error) {
	if tasks.core.commands == nil {
		return []CommandRecord{}, nil
	}
	return tasks.core.commands.list(ctx)
}

// SetCommandGeneration binds new projections to the published owner identity.
// An owner identity cannot be replaced while it still owns processes.
func (tasks *TaskService) SetCommandGeneration(generation string) {
	if tasks.core.commands == nil || generation == "" {
		return
	}
	s := tasks.core.commands
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) == 0 {
		s.generation = generation
	}
}
func (tasks *TaskService) PollCommand(ctx context.Context, id string, cursor int64, limit int) (CommandRecord, error) {
	if tasks.core.commands == nil {
		return CommandRecord{}, sql.ErrNoRows
	}
	return tasks.core.commands.poll(ctx, id, cursor, limit)
}
func (tasks *TaskService) StopCommand(ctx context.Context, id string) (CommandRecord, error) {
	if tasks.core.commands == nil {
		return CommandRecord{}, sql.ErrNoRows
	}
	return tasks.core.commands.stop(ctx, id, "user_stop")
}
func (tasks *TaskService) HasCommands() bool {
	return tasks.core.commands != nil && tasks.core.commands.hasActive()
}
func (tasks *TaskService) CloseCommands(reason string) error {
	if tasks.core.commands == nil {
		return nil
	}
	return tasks.core.commands.close(reason)
}
