//go:build windows

package winlaunch

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

var ErrProfileBusy = errors.New("process launcher: execution_profile_busy; profile has a live or uncertain grant lease")

type profileLease struct {
	profile   *profile
	signature [32]byte
	refs      int
	ready     bool
}

var profileLeases = struct {
	sync.Mutex
	items map[string]*profileLease
}{items: map[string]*profileLease{}}

func acquireProfile(spec Spec) (*profile, error) {
	key := strings.ToLower(spec.AppContainerName)
	raw, _ := json.Marshal(struct {
		Network   NetworkMode
		Grants    []Grant
		Protected []ProtectedPath
	}{spec.Network, spec.Grants, spec.ProtectedPaths})
	signature := sha256.Sum256(raw)
	var lease *profileLease
	if key != "" {
		profileLeases.Lock()
		if current := profileLeases.items[key]; current != nil {
			if !current.ready || current.signature != signature {
				profileLeases.Unlock()
				return nil, ErrProfileBusy
			}
			current.refs++
			p := &profile{name: current.profile.name, sid: current.profile.sid, shared: current}
			profileLeases.Unlock()
			return p, nil
		}
		lease = &profileLease{signature: signature, refs: 1}
		profileLeases.items[key] = lease
		profileLeases.Unlock()
	}
	p, err := newProfile(spec)
	if err == nil {
		err = applyGrants(p, spec.Grants)
	}
	if err == nil {
		err = applyProtectedPaths(p, spec.ProtectedPaths)
	}
	if err != nil {
		cleanup := cleanupProfileRaw(p)
		if lease != nil {
			profileLeases.Lock()
			if cleanup == nil {
				delete(profileLeases.items, key)
			}
			profileLeases.Unlock()
		}
		return nil, errors.Join(err, cleanup)
	}
	if lease == nil {
		return p, nil
	}
	profileLeases.Lock()
	lease.profile, lease.ready = p, true
	profileLeases.Unlock()
	return &profile{name: p.name, sid: p.sid, shared: lease}, nil
}

func cleanupProfile(p *profile) error {
	if p == nil {
		return nil
	}
	p.cleanupOnce.Do(func() { p.cleanupErr = releaseProfile(p) })
	return p.cleanupErr
}

func releaseProfile(p *profile) error {
	if p.shared == nil {
		return cleanupProfileRaw(p)
	}
	profileLeases.Lock()
	lease := p.shared
	lease.refs--
	if lease.refs != 0 {
		profileLeases.Unlock()
		return nil
	}
	lease.ready = false
	profileLeases.Unlock()
	err := cleanupProfileRaw(lease.profile)
	profileLeases.Lock()
	if err == nil {
		delete(profileLeases.items, strings.ToLower(p.name))
	}
	profileLeases.Unlock()
	return err
}
