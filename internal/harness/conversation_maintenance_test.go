package harness

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIdleConversationMaintenanceDoesNotBlockForegroundWork(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, core)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// An idle sweep must not take either the attachment lock or the global
	// execution lease: both would reject unrelated foreground management.
	core.Store.attachmentMu.Lock()
	var unlocked sync.Once
	defer unlocked.Do(core.Store.attachmentMu.Unlock)
	done := make(chan error, 1)
	go func() { done <- core.Store.expireConversations(ctx, time.Now()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		unlocked.Do(core.Store.attachmentMu.Unlock)
		<-done
		t.Fatal("idle conversation maintenance waited for an unrelated attachment operation")
	}
	release, err := core.Store.runMu.acquire(chat, true)
	if err != nil {
		t.Fatal("idle maintenance retained the foreground lease", err)
	}
	release()
}
