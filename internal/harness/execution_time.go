package harness

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Only active execution is timed. Pausing persists the elapsed segment before
// waiting for approval, so a restart during user input cannot bill idle time.
type runClock struct {
	parent     context.Context
	store      *Store
	runID      string
	limit      time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	started    time.Time
	since      string
	activity   *executionActivity
	registered bool
}

func (clock *runClock) resume() (returnErr error) {
	if clock.activity != nil {
		if err := clock.activity.acquire(); err != nil {
			return err
		}
		clock.registered = true
		defer func() {
			if returnErr != nil {
				returnErr = errors.Join(returnErr, clock.pause())
			}
		}()
		if clock.runID == clock.activity.clock.runID {
			run, err := clock.store.GetRun(clock.parent, clock.runID)
			if err != nil {
				return err
			}
			remaining := clock.limit - time.Duration(activeElapsed(run))*time.Millisecond
			if remaining <= 0 {
				return ErrTimeLimit
			}
			clock.ctx, clock.cancel = context.WithTimeout(clock.parent, remaining)
			return nil
		}
	}
	if err := clock.parent.Err(); err != nil {
		return err
	}
	run, err := clock.store.GetRun(clock.parent, clock.runID)
	if err != nil {
		return err
	}
	remaining := clock.limit - time.Duration(run.ElapsedMS)*time.Millisecond
	if remaining <= 0 {
		return ErrTimeLimit
	}
	clock.started, clock.since = time.Now(), nowText()
	if _, err := clock.store.db.ExecContext(clock.parent, `UPDATE runs SET active_since=? WHERE id=?`, clock.since, clock.runID); err != nil {
		clock.since = ""
		return err
	}
	clock.ctx, clock.cancel = context.WithTimeout(clock.parent, remaining)
	return nil
}

func (clock *runClock) pause() (returnErr error) {
	defer func() {
		if clock.registered {
			clock.registered = false
			returnErr = errors.Join(returnErr, clock.activity.release())
		}
	}()
	if clock.cancel != nil {
		clock.cancel()
		clock.cancel = nil
	}
	if clock.since == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := clock.store.db.ExecContext(ctx, `UPDATE runs SET elapsed_ms=elapsed_ms+?,active_since=''
		WHERE id=? AND active_since=?`, max(0, time.Since(clock.started).Milliseconds()), clock.runID, clock.since)
	if err == nil {
		clock.since = ""
	}
	return err
}

func activeTimeout(parent context.Context, err error) error {
	if err != nil && errors.Is(context.Cause(parent), ErrTimeLimit) {
		return errors.Join(ErrTimeLimit, err)
	}
	if parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrTimeLimit, err)
	}
	return err
}

// A composition times the union of its active child/auxiliary segments, not
// their sum. When every execution waits for approval, the parent clock pauses.
type executionActivity struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelCauseFunc
	clock   *runClock
	count   int
	segment int
	stop    func() bool
}

func newExecutionActivity(ctx context.Context, store *Store, runID string, seconds int) *executionActivity {
	parent, cancel := context.WithCancelCause(ctx)
	return &executionActivity{ctx: parent, cancel: cancel,
		clock: &runClock{parent: parent, store: store, runID: runID, limit: time.Duration(seconds) * time.Second}}
}

func (activity *executionActivity) acquire() error {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if err := activity.ctx.Err(); err != nil {
		return errors.Join(err, context.Cause(activity.ctx))
	}
	if activity.count == 0 {
		if err := activity.clock.resume(); err != nil {
			return err
		}
		activity.segment++
		segment, ctx := activity.segment, activity.clock.ctx
		activity.stop = context.AfterFunc(ctx, func() {
			activity.mu.Lock()
			defer activity.mu.Unlock()
			if activity.segment == segment && activity.count > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				activity.cancel(ErrTimeLimit)
			}
		})
	}
	activity.count++
	return nil
}

func (activity *executionActivity) release() error {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if activity.count == 0 {
		return errors.New("harness: unbalanced active execution clock")
	}
	activity.count--
	if activity.count != 0 {
		return nil
	}
	if activity.stop != nil {
		activity.stop()
	}
	if errors.Is(activity.clock.ctx.Err(), context.DeadlineExceeded) {
		activity.cancel(ErrTimeLimit)
	}
	activity.segment++
	return activity.clock.pause()
}

func (activity *executionActivity) close() error {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if activity.stop != nil {
		activity.stop()
	}
	activity.segment++
	activity.cancel(context.Canceled)
	if activity.count != 0 {
		return errors.Join(errors.New("harness: execution clock closed with active work"), activity.clock.pause())
	}
	return activity.clock.pause()
}
