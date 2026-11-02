package harness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const MaxForegroundRuns = 2

// executionGate keeps the existing global exclusive boundary for settings,
// backups and workspace changes, while executions lease one conversation.
// It never waits behind a model call and holds no lock during execution.
type executionGate struct {
	mu        sync.Mutex
	exclusive bool
	chats     map[string]bool // true consumes a foreground slot; false is a short edit
}

func (gate *executionGate) TryLock() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.exclusive || len(gate.chats) != 0 {
		return false
	}
	gate.exclusive = true
	return true
}

func (gate *executionGate) Unlock() {
	gate.mu.Lock()
	gate.exclusive = false
	gate.mu.Unlock()
}

func (gate *executionGate) acquire(chat string, execution bool) (func(), error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	_, held := gate.chats[chat]
	count := 0
	for _, executing := range gate.chats {
		if executing {
			count++
		}
	}
	if chat == "" || gate.exclusive || held || execution && count >= MaxForegroundRuns {
		ids := make([]string, 0, len(gate.chats))
		for id := range gate.chats {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%w; foreground limit=%d; blocking conversations: %s", ErrActiveRun, MaxForegroundRuns, strings.Join(ids, ", "))
	}
	if gate.chats == nil {
		gate.chats = make(map[string]bool)
	}
	gate.chats[chat] = execution
	var once sync.Once
	return func() { once.Do(func() { gate.mu.Lock(); delete(gate.chats, chat); gate.mu.Unlock() }) }, nil
}

func (service *Service) acquireConversation(chat string) (func(), error) {
	if service.ownsLease {
		return func() {}, nil
	}
	if chat == "" {
		chat = "draft-" + newID()
	}
	return service.Store.runMu.acquire(chat, true)
}

func (service *Service) acquireParent(ctx context.Context, runID string) (func(), error) {
	if service.ownsLease {
		return func() {}, nil
	}
	run, err := service.Store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return service.acquireConversation(run.ConversationID)
}

// commit permits only the proposing conversation's own lease during an
// interactive confirmation. All other foreground work blocks a global commit.
func (gate *executionGate) commit(allowChat string) (func(), error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	blocked := gate.exclusive
	ids := make([]string, 0, len(gate.chats))
	for id := range gate.chats {
		if id != allowChat || allowChat == "" {
			blocked = true
		}
		ids = append(ids, id)
	}
	if blocked {
		sort.Strings(ids)
		return nil, fmt.Errorf("%w; blocking conversations: %s", ErrActiveRun, strings.Join(ids, ", "))
	}
	gate.exclusive = true
	return gate.Unlock, nil
}
func (store *Store) acquireModel(ctx context.Context) (func(), error) {
	store.modelMu.Lock()
	if store.modelSlots == nil {
		store.modelSlots = make(chan struct{}, 2)
	}
	slots := store.modelSlots
	store.modelMu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
