package currentstore

import (
	"errors"
	"fmt"
	"sync"
)

type processFileLock interface {
	release() error
}

type ownerLease struct {
	key  string
	lock processFileLock
	once sync.Once
	err  error
}

var ownerRegistry = struct {
	sync.Mutex
	active map[string]struct{}
}{
	active: make(map[string]struct{}),
}

func acquireOwner(path string) (*ownerLease, error) {
	key := ownerPathKey(path)
	ownerRegistry.Lock()
	if _, exists := ownerRegistry.active[key]; exists {
		ownerRegistry.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrOwnerActive, path)
	}
	ownerRegistry.active[key] = struct{}{}
	ownerRegistry.Unlock()

	lock, err := acquireProcessFileLock(path + ".freeagent.owner.lock")
	if err != nil {
		ownerRegistry.Lock()
		delete(ownerRegistry.active, key)
		ownerRegistry.Unlock()
		if errors.Is(err, ErrOwnerActive) {
			return nil, err
		}
		return nil, fmt.Errorf("currentstore: acquire owner fence: %w", err)
	}
	return &ownerLease{key: key, lock: lock}, nil
}

func (owner *ownerLease) release() error {
	if owner == nil {
		return nil
	}
	owner.once.Do(func() {
		owner.err = owner.lock.release()
		ownerRegistry.Lock()
		delete(ownerRegistry.active, owner.key)
		ownerRegistry.Unlock()
	})
	return owner.err
}
