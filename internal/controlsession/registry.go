package controlsession

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CredentialBytesV1 = 32

	BootstrapLifetimeV1       = 5 * time.Minute
	SessionAbsoluteLifetimeV1 = 8 * time.Hour
	SessionIdleLifetimeV1     = 30 * time.Minute

	MaximumGlobalAdmissionsV1  = 32
	MaximumSessionAdmissionsV1 = 8

	bootstrapDigestDomainV1        = "freeagent.control-bootstrap-capability/v1"
	sessionCredentialDomainV1      = "freeagent.control-session-credential/v1"
	csrfProofDigestDomainV1        = "freeagent.control-csrf-token/v1"
	resumeCredentialDigestDomainV1 = "freeagent.control-session-resume/v1"
	maximumSafeUnixMicrosecondsV1  = int64(1<<53 - 1)
)

var (
	ErrEntropyUnavailable = errors.New("controlsession: entropy unavailable")
	ErrUnauthenticated    = errors.New("controlsession: unauthenticated")
	ErrSessionExpired     = errors.New("controlsession: session expired")
	ErrForbidden          = errors.New("controlsession: forbidden")
	ErrResourceExhausted  = errors.New("controlsession: resource exhausted")
	ErrClosed             = errors.New("controlsession: registry closed")
)

// RegistryConfigV1 contains only server-established authorization inputs.
// Entropy and Now are dependency-injection seams for deterministic tests; nil
// selects crypto/rand.Reader and time.Now.
type RegistryConfigV1 struct {
	PrincipalID           string
	AuthorizationRevision uint64
	Capabilities          []controlapicontract.ControlCapabilityV1
	ScopeSet              *AuthorizedScopeSetV1
	Entropy               io.Reader
	Now                   func() time.Time
}

// BootstrapMaterialV1 is the caller-owned, one-time handoff material. The raw
// Capability is never retained by RegistryV1.
type BootstrapMaterialV1 struct {
	BootID              string
	Capability          []byte
	ExpiresAtUnixMicros uint64
}

// IssuedSessionV1 gives the transport layer its one copy of each raw
// credential. Metadata contains only the safe control-session/v1 projection.
type IssuedSessionV1 struct {
	Metadata          controlapicontract.ControlSessionV1
	SessionCredential []byte
	CSRFToken         []byte
	ResumeCredential  []byte
	AuthorizedScopes  []controlapicontract.ControlScopeV1
}

// ResumedSessionV1 gives the transport layer the newly rotated browser-held
// proofs and the safe authorization projection. The unchanged session cookie
// credential is deliberately not copied into this result.
type ResumedSessionV1 struct {
	Metadata         controlapicontract.ControlSessionV1
	CSRFToken        []byte
	ResumeCredential []byte
	AuthorizedScopes []controlapicontract.ControlScopeV1
}

type sessionRecordV1 struct {
	credentialDigest string
	csrfDigest       string
	resumeDigest     string
	metadata         controlapicontract.ControlSessionV1
	scopeSet         *AuthorizedScopeSetV1
	expiresAt        time.Time
	lastSeen         time.Time
	active           int
	revoked          bool
}

// RegistryV1 is a process-local bootstrap and session authority. It stores
// domain-separated credential digests only and has no persistence hooks.
type RegistryV1 struct {
	mutex     sync.Mutex
	entropyMu sync.Mutex

	closed bool

	entropy io.Reader
	now     func() time.Time

	bootID                string
	principalID           string
	authorizationRevision uint64
	capabilities          []controlapicontract.ControlCapabilityV1
	scopeSet              *AuthorizedScopeSetV1

	bootstrapDigest    string
	bootstrapExpiresAt time.Time
	bootstrapConsumed  bool
	bootstrapAttempts  uint8
	bootstrapDone      chan struct{}
	bootstrapTimer     *time.Timer

	sessionsByCredential map[string]*sessionRecordV1
	sessionIDs           map[string]struct{}
	globalActive         int
}

// NewRegistryV1 creates one fresh Boot identity and one bootstrap capability.
// No session exists until ExchangeBootstrap succeeds.
func NewRegistryV1(
	config RegistryConfigV1,
) (*RegistryV1, BootstrapMaterialV1, error) {
	scopeSet := config.ScopeSet.clone()
	if scopeSet == nil || scopeSet.Digest() == "" {
		return nil, BootstrapMaterialV1{}, ErrInvalidConfiguration
	}
	validated, _, _, err := controlapicontract.NewControlSessionV1(
		controlapicontract.ControlSessionV1{
			SchemaVersion:         controlapicontract.ControlSessionSchemaVersionV1,
			BootID:                "configuration-validation-boot",
			SessionID:             "configuration-validation-session",
			PrincipalID:           config.PrincipalID,
			Capabilities:          append([]controlapicontract.ControlCapabilityV1(nil), config.Capabilities...),
			ScopeSetDigest:        scopeSet.Digest(),
			AuthorizationRevision: config.AuthorizationRevision,
			IssuedAtUnixMicros:    1,
			ExpiresAtUnixMicros:   2,
		},
	)
	if err != nil {
		return nil, BootstrapMaterialV1{}, ErrInvalidConfiguration
	}
	entropy := config.Entropy
	if nilInterfaceV1(entropy) {
		entropy = rand.Reader
	}
	nowFunction := config.Now
	if nowFunction == nil {
		nowFunction = time.Now
	}
	now := nowFunction()
	expiresAt := now.Add(BootstrapLifetimeV1)
	expiresMicros, ok := safeUnixMicrosV1(expiresAt)
	if _, nowOK := safeUnixMicrosV1(now); !nowOK || !ok || !expiresAt.After(now) {
		return nil, BootstrapMaterialV1{}, ErrInvalidConfiguration
	}
	random, err := readEntropyV1(entropy, CredentialBytesV1*2)
	if err != nil {
		return nil, BootstrapMaterialV1{}, err
	}
	bootID := base64.RawURLEncoding.EncodeToString(random[:CredentialBytesV1])
	capability := append([]byte(nil), random[CredentialBytesV1:]...)
	clear(random)
	registry := &RegistryV1{
		entropy:               entropy,
		now:                   nowFunction,
		bootID:                bootID,
		principalID:           validated.PrincipalID,
		authorizationRevision: validated.AuthorizationRevision,
		capabilities: append(
			[]controlapicontract.ControlCapabilityV1(nil),
			validated.Capabilities...,
		),
		scopeSet:             scopeSet,
		bootstrapDigest:      digestCredentialV1(bootstrapDigestDomainV1, capability),
		bootstrapExpiresAt:   expiresAt,
		bootstrapDone:        make(chan struct{}),
		sessionsByCredential: make(map[string]*sessionRecordV1),
		sessionIDs:           make(map[string]struct{}),
	}
	registry.bootstrapTimer = time.AfterFunc(
		BootstrapLifetimeV1,
		registry.expireBootstrapV1,
	)
	return registry, BootstrapMaterialV1{
		BootID:              bootID,
		Capability:          capability,
		ExpiresAtUnixMicros: expiresMicros,
	}, nil
}

// ExchangeBootstrap validates and counts the one-time attempt before touching
// session entropy, then generates and inserts session material while holding
// the same authority mutex. Entropy failure rolls the valid attempt back and
// therefore cannot consume the capability.
func (registry *RegistryV1) ExchangeBootstrap(
	capability []byte,
) (IssuedSessionV1, error) {
	if registry == nil {
		return IssuedSessionV1{}, ErrUnauthenticated
	}
	candidateDigest := digestCredentialV1(bootstrapDigestDomainV1, capability)

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return IssuedSessionV1{}, ErrClosed
	}
	now, err := registry.nowLocked()
	if err != nil {
		return IssuedSessionV1{}, ErrInvalidConfiguration
	}
	if !now.Before(registry.bootstrapExpiresAt) {
		registry.consumeBootstrapLocked()
		return IssuedSessionV1{}, ErrUnauthenticated
	}
	if registry.bootstrapConsumed || registry.bootstrapDigest == "" {
		return IssuedSessionV1{}, ErrUnauthenticated
	}
	registry.bootstrapAttempts++
	if registry.bootstrapAttempts > 1 {
		registry.consumeBootstrapLocked()
		return IssuedSessionV1{}, ErrUnauthenticated
	}
	if len(capability) != CredentialBytesV1 ||
		!equalDigestV1(candidateDigest, registry.bootstrapDigest) {
		return IssuedSessionV1{}, ErrUnauthenticated
	}
	sessionID, credential, csrf, resume, err := registry.generateSessionMaterial()
	if err != nil {
		registry.bootstrapAttempts--
		return IssuedSessionV1{}, err
	}
	keepMaterial := false
	defer func() {
		if !keepMaterial {
			clear(credential)
			clear(csrf)
			clear(resume)
		}
	}()
	credentialDigest := digestCredentialV1(sessionCredentialDomainV1, credential)
	csrfDigest := digestCredentialV1(csrfProofDigestDomainV1, csrf)
	resumeDigest := digestCredentialV1(resumeCredentialDigestDomainV1, resume)
	if _, exists := registry.sessionsByCredential[credentialDigest]; exists {
		return IssuedSessionV1{}, ErrEntropyUnavailable
	}
	if _, exists := registry.sessionIDs[sessionID]; exists {
		return IssuedSessionV1{}, ErrEntropyUnavailable
	}
	expiresAt := now.Add(SessionAbsoluteLifetimeV1)
	issuedMicros, issuedOK := safeUnixMicrosV1(now)
	expiresMicros, expiresOK := safeUnixMicrosV1(expiresAt)
	if !issuedOK || !expiresOK || !expiresAt.After(now) {
		return IssuedSessionV1{}, ErrInvalidConfiguration
	}
	metadata, _, _, err := controlapicontract.NewControlSessionV1(
		controlapicontract.ControlSessionV1{
			SchemaVersion: controlapicontract.ControlSessionSchemaVersionV1,
			BootID:        registry.bootID,
			SessionID:     sessionID,
			PrincipalID:   registry.principalID,
			Capabilities: append(
				[]controlapicontract.ControlCapabilityV1(nil),
				registry.capabilities...,
			),
			ScopeSetDigest:        registry.scopeSet.Digest(),
			AuthorizationRevision: registry.authorizationRevision,
			IssuedAtUnixMicros:    issuedMicros,
			ExpiresAtUnixMicros:   expiresMicros,
		},
	)
	if err != nil {
		return IssuedSessionV1{}, ErrInvalidConfiguration
	}
	record := &sessionRecordV1{
		credentialDigest: credentialDigest,
		csrfDigest:       csrfDigest,
		resumeDigest:     resumeDigest,
		metadata:         cloneSessionV1(metadata),
		scopeSet:         registry.scopeSet.clone(),
		expiresAt:        expiresAt,
		lastSeen:         now,
	}
	registry.sessionsByCredential[credentialDigest] = record
	registry.sessionIDs[sessionID] = struct{}{}
	registry.consumeBootstrapLocked()
	keepMaterial = true
	return IssuedSessionV1{
		Metadata:          cloneSessionV1(metadata),
		SessionCredential: credential,
		CSRFToken:         csrf,
		ResumeCredential:  resume,
		AuthorizedScopes:  registry.scopeSet.Scopes(),
	}, nil
}

// ResumeSessionV1 verifies the unchanged host-only session credential and one
// process-local, single-use resume credential under the authority lock. On
// success it atomically rotates both browser-held proofs. Entropy failure and
// every rejected attempt leave the current proofs and idle clock unchanged.
func (registry *RegistryV1) ResumeSessionV1(
	sessionCredential []byte,
	resumeCredential []byte,
) (ResumedSessionV1, error) {
	if registry == nil || len(sessionCredential) != CredentialBytesV1 ||
		len(resumeCredential) != CredentialBytesV1 {
		return ResumedSessionV1{}, ErrUnauthenticated
	}
	credentialDigest := digestCredentialV1(
		sessionCredentialDomainV1,
		sessionCredential,
	)
	resumeDigest := digestCredentialV1(
		resumeCredentialDigestDomainV1,
		resumeCredential,
	)

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return ResumedSessionV1{}, ErrClosed
	}
	now, err := registry.nowLocked()
	if err != nil {
		return ResumedSessionV1{}, ErrSessionExpired
	}
	record := registry.sessionsByCredential[credentialDigest]
	if record == nil || record.revoked ||
		!equalDigestV1(record.credentialDigest, credentialDigest) {
		return ResumedSessionV1{}, ErrUnauthenticated
	}
	if !now.Before(record.expiresAt) || now.Before(record.lastSeen) ||
		now.Sub(record.lastSeen) >= SessionIdleLifetimeV1 {
		registry.revokeSessionLocked(record)
		return ResumedSessionV1{}, ErrSessionExpired
	}
	metadata, current := registry.currentSessionAuthorizationLockedV1(record)
	if !current {
		registry.revokeSessionLocked(record)
		return ResumedSessionV1{}, ErrUnauthenticated
	}
	if record.resumeDigest == "" ||
		!equalDigestV1(resumeDigest, record.resumeDigest) {
		return ResumedSessionV1{}, ErrUnauthenticated
	}

	csrf, resume, err := registry.generateResumeMaterial()
	if err != nil {
		return ResumedSessionV1{}, err
	}
	keepMaterial := false
	defer func() {
		if !keepMaterial {
			clear(csrf)
			clear(resume)
		}
	}()
	csrfDigest := digestCredentialV1(csrfProofDigestDomainV1, csrf)
	nextResumeDigest := digestCredentialV1(
		resumeCredentialDigestDomainV1,
		resume,
	)
	if equalDigestV1(csrfDigest, record.csrfDigest) ||
		equalDigestV1(nextResumeDigest, record.resumeDigest) {
		return ResumedSessionV1{}, ErrEntropyUnavailable
	}

	// This is the proof-rotation linearization point. No failing path above it
	// mutates the record, and the authority mutex admits exactly one winner for
	// concurrent uses of the same resume credential.
	record.csrfDigest = csrfDigest
	record.resumeDigest = nextResumeDigest
	record.lastSeen = now
	keepMaterial = true
	return ResumedSessionV1{
		Metadata:         cloneSessionV1(metadata),
		CSRFToken:        csrf,
		ResumeCredential: resume,
		AuthorizedScopes: record.scopeSet.Scopes(),
	}, nil
}

// AdmissionV1 contains only transport-verified credential material and the
// exact capability/scope required by the selected application service.
type AdmissionV1 struct {
	SessionCredential []byte
	CSRFToken         []byte
	RequireCSRF       bool
	Capability        controlapicontract.ControlCapabilityV1
	Scope             controlapicontract.ControlScopeV1
}

// PermitV1 proves one bounded process-local admission. Every value copy
// shares the same private lease, so Release remains linear and cannot be
// bypassed by copying the exported wrapper.
type PermitV1 struct {
	lease *permitLeaseV1
}

type permitLeaseV1 struct {
	mutex sync.Mutex
	live  bool

	registry       *RegistryV1
	record         *sessionRecordV1
	metadata       controlapicontract.ControlSessionV1
	sessionDigest  string
	scopeSet       *AuthorizedScopeSetV1
	scopeSetDigest string
	capability     controlapicontract.ControlCapabilityV1
	scope          controlapicontract.ControlScopeV1
	requireCSRF    bool
}

// Admit performs the complete credential, CSRF, current authorization,
// capability, and scope check before taking both non-blocking permits. The
// session idle clock advances only after both permits have been acquired.
func (registry *RegistryV1) Admit(input AdmissionV1) (*PermitV1, error) {
	if registry == nil || len(input.SessionCredential) != CredentialBytesV1 {
		return nil, ErrUnauthenticated
	}
	if input.RequireCSRF && len(input.CSRFToken) != CredentialBytesV1 {
		return nil, ErrUnauthenticated
	}
	if !input.RequireCSRF && len(input.CSRFToken) != 0 &&
		len(input.CSRFToken) != CredentialBytesV1 {
		return nil, ErrUnauthenticated
	}
	frozenScope, _, _, err := controlapicontract.NewControlScopeV1(input.Scope)
	if err != nil {
		return nil, ErrForbidden
	}
	credentialDigest := digestCredentialV1(
		sessionCredentialDomainV1,
		input.SessionCredential,
	)
	csrfDigest := ""
	if len(input.CSRFToken) != 0 {
		csrfDigest = digestCredentialV1(csrfProofDigestDomainV1, input.CSRFToken)
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return nil, ErrClosed
	}
	now, err := registry.nowLocked()
	if err != nil {
		return nil, ErrSessionExpired
	}
	record := registry.sessionsByCredential[credentialDigest]
	if record == nil || record.revoked ||
		!equalDigestV1(record.credentialDigest, credentialDigest) {
		return nil, ErrUnauthenticated
	}
	if record.metadata.BootID != registry.bootID ||
		record.metadata.PrincipalID != registry.principalID ||
		record.metadata.AuthorizationRevision != registry.authorizationRevision ||
		record.metadata.ScopeSetDigest != registry.scopeSet.Digest() ||
		record.scopeSet == nil || record.scopeSet.Digest() != registry.scopeSet.Digest() {
		registry.revokeSessionLocked(record)
		return nil, ErrUnauthenticated
	}
	if !now.Before(record.expiresAt) || now.Before(record.lastSeen) ||
		now.Sub(record.lastSeen) >= SessionIdleLifetimeV1 {
		registry.revokeSessionLocked(record)
		return nil, ErrSessionExpired
	}
	if input.RequireCSRF || csrfDigest != "" {
		if csrfDigest == "" || !equalDigestV1(csrfDigest, record.csrfDigest) {
			return nil, ErrUnauthenticated
		}
	}
	if !sessionHasCapabilityV1(record.metadata, input.Capability) ||
		!record.scopeSet.Allows(frozenScope) {
		return nil, ErrForbidden
	}
	if registry.globalActive >= MaximumGlobalAdmissionsV1 ||
		record.active >= MaximumSessionAdmissionsV1 {
		return nil, ErrResourceExhausted
	}
	metadata, _, sessionDigest, err := controlapicontract.NewControlSessionV1(
		record.metadata,
	)
	if err != nil {
		registry.revokeSessionLocked(record)
		return nil, ErrUnauthenticated
	}
	registry.globalActive++
	record.active++
	record.lastSeen = now
	return &PermitV1{
		lease: &permitLeaseV1{
			live:           true,
			registry:       registry,
			record:         record,
			metadata:       metadata,
			sessionDigest:  sessionDigest,
			scopeSet:       record.scopeSet.clone(),
			scopeSetDigest: record.scopeSet.Digest(),
			capability:     input.Capability,
			scope:          frozenScope,
			requireCSRF:    input.RequireCSRF,
		},
	}, nil
}

func (permit *PermitV1) Session() controlapicontract.ControlSessionV1 {
	session, _, ok := permit.currentSnapshotV1()
	if !ok {
		return controlapicontract.ControlSessionV1{}
	}
	return session
}

// Digest returns the immutable server-derived ScopeSet digest bound to this
// admitted session. It deliberately exposes no scope-set storage or mutable
// authority and lets PermitV1 satisfy controlapp.AuthorizationContextV1
// without introducing a package dependency.
func (permit *PermitV1) Digest() string {
	_, scopeSet, ok := permit.currentSnapshotV1()
	if !ok || scopeSet == nil {
		return ""
	}
	return scopeSet.Digest()
}

func (permit *PermitV1) Scopes() []controlapicontract.ControlScopeV1 {
	_, scopeSet, ok := permit.currentSnapshotV1()
	if !ok || scopeSet == nil {
		return nil
	}
	return scopeSet.Scopes()
}

func (permit *PermitV1) Allows(scope controlapicontract.ControlScopeV1) bool {
	if permit == nil || permit.lease == nil {
		return false
	}
	frozen, _, _, err := controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		return false
	}
	lease := permit.lease
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if frozen != lease.scope {
		return false
	}
	_, _, err = currentPermitAuthorizationLockedV1(
		lease,
		lease.capability,
		frozen,
		lease.requireCSRF,
	)
	return err == nil
}

// AuthorizeCurrentV1 revalidates this exact admission immediately before a
// sensitive application boundary. The requested capability and scope must be
// identical to those admitted by RegistryV1; a broader ScopeSet entry cannot
// be substituted after admission. The returned facts are defensive copies.
func (permit *PermitV1) AuthorizeCurrentV1(
	capability controlapicontract.ControlCapabilityV1,
	scope controlapicontract.ControlScopeV1,
	requireCSRF bool,
) (controlapicontract.ControlSessionV1, string, error) {
	if permit == nil || permit.lease == nil {
		return controlapicontract.ControlSessionV1{}, "", ErrUnauthenticated
	}
	frozen, _, _, err := controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		return controlapicontract.ControlSessionV1{}, "", ErrForbidden
	}
	lease := permit.lease
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	return currentPermitAuthorizationLockedV1(
		lease,
		capability,
		frozen,
		requireCSRF,
	)
}

func (permit *PermitV1) Release() {
	if permit == nil || permit.lease == nil {
		return
	}
	lease := permit.lease
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if !lease.live {
		return
	}
	lease.live = false
	registry := lease.registry
	record := lease.record
	if registry != nil && record != nil {
		registry.mutex.Lock()
		if !registry.closed && record.active > 0 && registry.globalActive > 0 {
			record.active--
			registry.globalActive--
		}
		registry.mutex.Unlock()
	}
	lease.registry = nil
	lease.record = nil
	lease.metadata = controlapicontract.ControlSessionV1{}
	lease.sessionDigest = ""
	lease.scopeSet = nil
	lease.scopeSetDigest = ""
	lease.capability = ""
	lease.scope = controlapicontract.ControlScopeV1{}
	lease.requireCSRF = false
}

func (permit *PermitV1) currentSnapshotV1() (
	controlapicontract.ControlSessionV1,
	*AuthorizedScopeSetV1,
	bool,
) {
	if permit == nil || permit.lease == nil {
		return controlapicontract.ControlSessionV1{}, nil, false
	}
	lease := permit.lease
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	session, _, err := currentPermitAuthorizationLockedV1(
		lease,
		lease.capability,
		lease.scope,
		lease.requireCSRF,
	)
	if err != nil || lease.scopeSet == nil {
		return controlapicontract.ControlSessionV1{}, nil, false
	}
	return session, lease.scopeSet.clone(), true
}

// currentPermitAuthorizationLockedV1 requires lease.mutex and then takes the
// registry mutex. No registry path takes a permit lease while holding the
// registry mutex, which freezes the lock order and avoids a shutdown cycle.
func currentPermitAuthorizationLockedV1(
	lease *permitLeaseV1,
	capability controlapicontract.ControlCapabilityV1,
	scope controlapicontract.ControlScopeV1,
	requireCSRF bool,
) (controlapicontract.ControlSessionV1, string, error) {
	if lease == nil || !lease.live || lease.registry == nil ||
		lease.record == nil {
		return controlapicontract.ControlSessionV1{}, "", ErrUnauthenticated
	}
	if capability != lease.capability || scope != lease.scope ||
		requireCSRF != lease.requireCSRF {
		return controlapicontract.ControlSessionV1{}, "", ErrForbidden
	}
	registry := lease.registry
	record := lease.record
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed || record.revoked || record.active == 0 ||
		registry.globalActive == 0 || record.credentialDigest == "" ||
		registry.sessionsByCredential[record.credentialDigest] != record {
		return controlapicontract.ControlSessionV1{}, "", ErrUnauthenticated
	}
	now, err := registry.nowLocked()
	if err != nil || !now.Before(record.expiresAt) || now.Before(record.lastSeen) ||
		now.Sub(record.lastSeen) >= SessionIdleLifetimeV1 {
		registry.revokeSessionLocked(record)
		return controlapicontract.ControlSessionV1{}, "", ErrSessionExpired
	}
	if record.metadata.BootID != registry.bootID ||
		record.metadata.PrincipalID != registry.principalID ||
		record.metadata.AuthorizationRevision != registry.authorizationRevision ||
		record.metadata.ScopeSetDigest != registry.scopeSet.Digest() ||
		record.scopeSet == nil ||
		record.scopeSet.Digest() != registry.scopeSet.Digest() ||
		lease.scopeSet == nil ||
		lease.scopeSetDigest != registry.scopeSet.Digest() ||
		!sessionHasCapabilityV1(record.metadata, capability) ||
		!record.scopeSet.Allows(scope) || !lease.scopeSet.Allows(scope) {
		registry.revokeSessionLocked(record)
		return controlapicontract.ControlSessionV1{}, "", ErrUnauthenticated
	}
	metadata, _, digest, err := controlapicontract.NewControlSessionV1(
		record.metadata,
	)
	if err != nil || digest != lease.sessionDigest {
		registry.revokeSessionLocked(record)
		return controlapicontract.ControlSessionV1{}, "", ErrUnauthenticated
	}
	return metadata, lease.scopeSetDigest, nil
}

func (registry *RegistryV1) BootID() string {
	if registry == nil {
		return ""
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return ""
	}
	return registry.bootID
}

// BootstrapDone closes at the bootstrap-consumption linearization point. It
// carries no credential material and lets the composition root remove the
// owner-only handoff after a successful exchange, expiry, or registry close.
// A nil or zero-value registry is already unusable and therefore returns an
// already-closed channel.
func (registry *RegistryV1) BootstrapDone() <-chan struct{} {
	if registry == nil {
		return closedSignalV1()
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.bootstrapDone == nil {
		return closedSignalV1()
	}
	return registry.bootstrapDone
}

// Close revokes every bootstrap/session authority and zeroes all mutable
// credential-derived registry state. Existing Permit releases become no-ops.
func (registry *RegistryV1) Close() {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return
	}
	registry.closed = true
	registry.consumeBootstrapLocked()
	for _, record := range registry.sessionsByCredential {
		record.credentialDigest = ""
		record.csrfDigest = ""
		record.resumeDigest = ""
		record.metadata = controlapicontract.ControlSessionV1{}
		record.scopeSet = nil
		record.expiresAt = time.Time{}
		record.lastSeen = time.Time{}
		record.active = 0
		record.revoked = true
	}
	clear(registry.sessionsByCredential)
	clear(registry.sessionIDs)
	registry.sessionsByCredential = nil
	registry.sessionIDs = nil
	registry.globalActive = 0
	registry.bootID = ""
	registry.principalID = ""
	registry.authorizationRevision = 0
	registry.capabilities = nil
	registry.scopeSet = nil
	registry.bootstrapExpiresAt = time.Time{}
	registry.bootstrapAttempts = 0
}

func (registry *RegistryV1) generateSessionMaterial() (
	string,
	[]byte,
	[]byte,
	[]byte,
	error,
) {
	registry.entropyMu.Lock()
	defer registry.entropyMu.Unlock()
	random, err := readEntropyV1(registry.entropy, CredentialBytesV1*4)
	if err != nil {
		return "", nil, nil, nil, err
	}
	sessionID := base64.RawURLEncoding.EncodeToString(random[:CredentialBytesV1])
	credential := append(
		[]byte(nil),
		random[CredentialBytesV1:CredentialBytesV1*2]...,
	)
	csrf := append(
		[]byte(nil),
		random[CredentialBytesV1*2:CredentialBytesV1*3]...,
	)
	resume := append([]byte(nil), random[CredentialBytesV1*3:]...)
	clear(random)
	return sessionID, credential, csrf, resume, nil
}

func (registry *RegistryV1) generateResumeMaterial() ([]byte, []byte, error) {
	registry.entropyMu.Lock()
	defer registry.entropyMu.Unlock()
	random, err := readEntropyV1(registry.entropy, CredentialBytesV1*2)
	if err != nil {
		return nil, nil, err
	}
	csrf := append([]byte(nil), random[:CredentialBytesV1]...)
	resume := append([]byte(nil), random[CredentialBytesV1:]...)
	clear(random)
	return csrf, resume, nil
}

func (registry *RegistryV1) nowLocked() (time.Time, error) {
	now := registry.now()
	if _, ok := safeUnixMicrosV1(now); !ok {
		return time.Time{}, ErrInvalidConfiguration
	}
	return now, nil
}

func (registry *RegistryV1) consumeBootstrapLocked() {
	if registry.bootstrapConsumed {
		return
	}
	registry.bootstrapDigest = ""
	registry.bootstrapConsumed = true
	if registry.bootstrapTimer != nil {
		registry.bootstrapTimer.Stop()
		registry.bootstrapTimer = nil
	}
	if registry.bootstrapDone != nil {
		close(registry.bootstrapDone)
	}
}

// expireBootstrapV1 is the process-local monotonic expiry callback. It does
// not depend on a later request arriving or on the wall clock moving forward.
func (registry *RegistryV1) expireBootstrapV1() {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return
	}
	registry.consumeBootstrapLocked()
}

func closedSignalV1() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (registry *RegistryV1) revokeSessionLocked(record *sessionRecordV1) {
	if record == nil || record.revoked {
		return
	}
	delete(registry.sessionsByCredential, record.credentialDigest)
	delete(registry.sessionIDs, record.metadata.SessionID)
	record.credentialDigest = ""
	record.csrfDigest = ""
	record.resumeDigest = ""
	record.revoked = true
	record.scopeSet = nil
}

func (registry *RegistryV1) currentSessionAuthorizationLockedV1(
	record *sessionRecordV1,
) (controlapicontract.ControlSessionV1, bool) {
	if registry == nil || record == nil || registry.scopeSet == nil ||
		record.scopeSet == nil ||
		record.metadata.BootID != registry.bootID ||
		record.metadata.PrincipalID != registry.principalID ||
		record.metadata.AuthorizationRevision != registry.authorizationRevision ||
		record.metadata.ScopeSetDigest != registry.scopeSet.Digest() ||
		record.scopeSet.Digest() != registry.scopeSet.Digest() ||
		!equalCapabilitiesV1(record.metadata.Capabilities, registry.capabilities) {
		return controlapicontract.ControlSessionV1{}, false
	}
	metadata, _, _, err := controlapicontract.NewControlSessionV1(record.metadata)
	if err != nil {
		return controlapicontract.ControlSessionV1{}, false
	}
	return metadata, true
}

func equalCapabilitiesV1(
	left []controlapicontract.ControlCapabilityV1,
	right []controlapicontract.ControlCapabilityV1,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func readEntropyV1(reader io.Reader, count int) ([]byte, error) {
	if nilInterfaceV1(reader) || count <= 0 {
		return nil, ErrEntropyUnavailable
	}
	result := make([]byte, count)
	if _, err := io.ReadFull(reader, result); err != nil {
		clear(result)
		return nil, ErrEntropyUnavailable
	}
	return result, nil
}

func nilInterfaceV1(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}

func safeUnixMicrosV1(value time.Time) (uint64, bool) {
	micros := value.UnixMicro()
	if micros <= 0 || micros > maximumSafeUnixMicrosecondsV1 {
		return 0, false
	}
	return uint64(micros), true
}

func digestCredentialV1(domain string, credential []byte) string {
	return moduleapi.Digest(domain, credential)
}

func equalDigestV1(left, right string) bool {
	return len(left) == len(right) &&
		subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func sessionHasCapabilityV1(
	session controlapicontract.ControlSessionV1,
	want controlapicontract.ControlCapabilityV1,
) bool {
	for _, capability := range session.Capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func cloneSessionV1(
	input controlapicontract.ControlSessionV1,
) controlapicontract.ControlSessionV1 {
	result := input
	result.Capabilities = append(
		[]controlapicontract.ControlCapabilityV1(nil),
		input.Capabilities...,
	)
	return result
}
