package currentstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// OfflineLease is callback-scoped proof that the Current Store writer fence
// is held. It grants no SQL access and cannot be released by callers.
type OfflineLease struct {
	mu     sync.Mutex
	path   string
	owner  *ownerLease
	active bool
}

// WithOfflineLease holds the same single-writer fence used by normal Runtime
// startup for the complete callback. Maintenance operations use it to keep a
// backup and the following migration in one exclusive interval.
func WithOfflineLease(
	ctx context.Context,
	path string,
	action func(*OfflineLease) error,
) (returnErr error) {
	if ctx == nil {
		return errors.New("currentstore: offline lease context is nil")
	}
	if action == nil {
		return errors.New("currentstore: offline lease action is nil")
	}
	canonicalPath, err := existingDatabasePath(path)
	if err != nil {
		return err
	}
	owner, err := acquireOwner(canonicalPath)
	if err != nil {
		return err
	}
	lease := &OfflineLease{path: canonicalPath, owner: owner, active: true}
	defer func() {
		lease.mu.Lock()
		lease.active = false
		lease.path = ""
		lease.owner = nil
		lease.mu.Unlock()
		returnErr = errors.Join(returnErr, owner.release())
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return action(lease)
}

// Path returns the fenced canonical database path while the callback is
// active. A retained lease fails closed after the callback returns.
func (lease *OfflineLease) Path() (string, error) {
	if lease == nil {
		return "", errors.New("currentstore: offline lease is nil")
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if !lease.active || lease.owner == nil || lease.path == "" {
		return "", errors.New("currentstore: offline lease is not active")
	}
	return lease.path, nil
}

func requireOfflineLease(lease *OfflineLease) (string, error) {
	path, err := lease.Path()
	if err != nil {
		return "", err
	}
	canonicalPath, err := existingDatabasePath(path)
	if err != nil {
		return "", err
	}
	if canonicalPath != path {
		return "", fmt.Errorf("currentstore: offline database path changed")
	}
	return path, nil
}
