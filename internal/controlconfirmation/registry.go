// Package controlconfirmation owns one process-local, short-lived proof
// authority for governed Control mutations. It has no transport, filesystem,
// Current Store, Backup, or durable persistence responsibility.
package controlconfirmation

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ProofBytesV1    = 32
	ProofLifetimeV1 = 2 * time.Minute

	MaximumGlobalProofsV1   = 256
	MaximumSessionProofsV1  = 8
	maximumSafeUnixMicrosV1 = int64(1<<53 - 1)
	proofDigestDomainV1     = "freeagent.control-confirmation-proof/v1"
	sessionCountKeyDomainV1 = "freeagent.control-confirmation-session-capacity/v1"
)

var (
	ErrInvalidConfiguration = errors.New("controlconfirmation: invalid configuration")
	ErrInvalidInput         = errors.New("controlconfirmation: invalid input")
	ErrEntropyUnavailable   = errors.New("controlconfirmation: entropy unavailable")
	ErrResourceExhausted    = errors.New("controlconfirmation: resource exhausted")
	// ErrProofRejected deliberately covers a missing, malformed, expired,
	// already claimed/consumed, or differently bound proof.
	ErrProofRejected = errors.New("controlconfirmation: proof rejected")
	ErrClosed        = errors.New("controlconfirmation: registry closed")
)

// RegistryConfigV1 exposes deterministic test seams. Nil (including a typed
// nil Reader) selects crypto/rand.Reader; a nil clock selects time.Now.
type RegistryConfigV1 struct {
	Entropy io.Reader
	Now     func() time.Time
}

// ExactBindingV1 carries bounded, exact canonical contracts. RegistryV1
// restores both contracts and retains neither canonical byte slice.
type ExactBindingV1 struct {
	Authority          *controlsession.PermitV1
	StatementCanonical []byte
	StatementDigest    string
	RequestCanonical   []byte
	RequestDigest      string
}

// IssuedProofV1 is the caller's only copy of the raw proof. RegistryV1 stores
// only its domain-separated digest.
type IssuedProofV1 struct {
	Proof               []byte
	ExpiresAtUnixMicros uint64
}

type proofStateV1 uint8

const (
	proofIssuedV1 proofStateV1 = iota + 1
	proofClaimedV1
)

type frozenBindingV1 struct {
	bootID                string
	sessionID             string
	principalID           string
	authorizationRevision uint64
	scopeSetDigest        string
	sessionDigest         string
	sessionExpiresAt      uint64
	capability            controlapicontract.ControlCapabilityV1
	scope                 controlapicontract.ControlScopeV1
	scopeDigest           string
	intent                controlapicontract.ControlOperationIntentV1
	operation             controlapicontract.ControlOperationV1
	statementDigest       string
	requestDigest         string
}

type proofRecordV1 struct {
	proofDigest string
	binding     frozenBindingV1
	sessionKey  string
	issuedAt    time.Time
	expiresAt   time.Time
	state       proofStateV1
}

// RegistryV1 is deliberately process-local. A fresh RegistryV1 after a
// restart has no record that can accept a proof from an earlier instance.
type RegistryV1 struct {
	mutex sync.Mutex

	closed  bool
	entropy io.Reader
	now     func() time.Time
	lastNow time.Time

	records       map[string]*proofRecordV1
	sessionCounts map[string]int
}

// ClaimV1 owns the sole in-process claim on one proof. The mutation caller
// must resolve it exactly once: Consume after any possible persistent effect,
// or ReleaseNoEffect only after proving that no persistent effect occurred.
type ClaimV1 struct {
	resolution *claimResolutionV1
}

// claimResolutionV1 is shared by every accidental value-copy of ClaimV1 so
// Consume and ReleaseNoEffect still form one irreversible decision.
type claimResolutionV1 struct {
	registry *RegistryV1
	record   *proofRecordV1
	once     sync.Once
}

func NewRegistryV1(config RegistryConfigV1) (*RegistryV1, error) {
	entropy := config.Entropy
	if nilInterfaceV1(entropy) {
		entropy = rand.Reader
	}
	nowFunction := config.Now
	if nowFunction == nil {
		nowFunction = time.Now
	}
	now := nowFunction()
	if _, ok := safeUnixMicrosV1(now); !ok {
		return nil, ErrInvalidConfiguration
	}
	return &RegistryV1{
		entropy:       entropy,
		now:           nowFunction,
		lastNow:       now,
		records:       make(map[string]*proofRecordV1),
		sessionCounts: make(map[string]int),
	}, nil
}

// Issue restores and cross-checks one exact stable confirmation statement and
// its final MUTATE request before generating a proof. Capacity is checked
// before entropy is read, and no retry loop can grow work without bound.
func (registry *RegistryV1) Issue(input ExactBindingV1) (IssuedProofV1, error) {
	if registry == nil {
		return IssuedProofV1{}, ErrClosed
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return IssuedProofV1{}, ErrClosed
	}
	now, err := registry.nowLocked()
	if err != nil {
		return IssuedProofV1{}, err
	}
	binding, err := freezeBindingV1(input, now)
	if err != nil {
		return IssuedProofV1{}, ErrInvalidInput
	}
	registry.purgeExpiredLocked(now)
	sessionKey := sessionCountKeyV1(binding.bootID, binding.sessionID)
	if len(registry.records) >= MaximumGlobalProofsV1 ||
		registry.sessionCounts[sessionKey] >= MaximumSessionProofsV1 {
		return IssuedProofV1{}, ErrResourceExhausted
	}
	raw, err := readEntropyV1(registry.entropy, ProofBytesV1)
	if err != nil {
		return IssuedProofV1{}, err
	}
	defer clear(raw)
	proofDigest := moduleapi.Digest(proofDigestDomainV1, raw)
	if _, collision := registry.records[proofDigest]; collision {
		return IssuedProofV1{}, ErrEntropyUnavailable
	}
	expiresAt := now.Add(ProofLifetimeV1)
	sessionExpiresAt := time.UnixMicro(int64(binding.sessionExpiresAt))
	if sessionExpiresAt.Before(expiresAt) {
		expiresAt = sessionExpiresAt
	}
	expiresMicros, ok := safeUnixMicrosV1(expiresAt)
	if !ok || !expiresAt.After(now) {
		return IssuedProofV1{}, ErrInvalidConfiguration
	}
	registry.records[proofDigest] = &proofRecordV1{
		proofDigest: proofDigest,
		binding:     binding,
		sessionKey:  sessionKey,
		issuedAt:    now,
		expiresAt:   expiresAt,
		state:       proofIssuedV1,
	}
	registry.sessionCounts[sessionKey]++
	return IssuedProofV1{
		Proof:               append([]byte(nil), raw...),
		ExpiresAtUnixMicros: expiresMicros,
	}, nil
}

// Claim atomically changes exactly one matching proof from ISSUED to CLAIMED.
// Every proof or binding rejection has the same externally comparable error.
func (registry *RegistryV1) Claim(
	proof []byte,
	input ExactBindingV1,
) (*ClaimV1, error) {
	if registry == nil {
		return nil, ErrProofRejected
	}
	if len(proof) != ProofBytesV1 {
		return nil, ErrProofRejected
	}
	proofDigest := moduleapi.Digest(proofDigestDomainV1, proof)

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return nil, ErrProofRejected
	}
	now, err := registry.nowLocked()
	if err != nil {
		return nil, err
	}
	// Always map malformed contracts and authority drift to the same proof
	// rejection as an unknown proof.
	binding, bindingErr := freezeBindingV1(input, now)
	record := registry.records[proofDigest]
	if bindingErr != nil || record == nil || record.state != proofIssuedV1 ||
		now.Before(record.issuedAt) || !now.Before(record.expiresAt) ||
		!equalBindingV1(record.binding, binding) {
		if record != nil && record.state == proofIssuedV1 &&
			!now.Before(record.expiresAt) {
			registry.deleteRecordLocked(proofDigest, record)
		}
		return nil, ErrProofRejected
	}
	record.state = proofClaimedV1
	resolution := &claimResolutionV1{
		registry: registry,
		record:   record,
	}
	return &ClaimV1{resolution: resolution}, nil
}

// Consume permanently invalidates the claimed proof. It is idempotent.
func (claim *ClaimV1) Consume() {
	if claim == nil {
		return
	}
	resolution := claim.resolution
	if resolution == nil {
		return
	}
	resolution.once.Do(func() {
		registry := resolution.registry
		if registry == nil {
			return
		}
		registry.mutex.Lock()
		defer registry.mutex.Unlock()
		record := resolution.record
		if record != nil && record.state == proofClaimedV1 &&
			record.proofDigest != "" &&
			registry.records[record.proofDigest] == record {
			registry.deleteRecordLocked(record.proofDigest, record)
		}
	})
}

// ReleaseNoEffect resets CLAIMED to ISSUED only when the caller has proven
// that no persistent effect occurred. An expired proof is deleted instead.
// It is idempotent.
func (claim *ClaimV1) ReleaseNoEffect() {
	if claim == nil {
		return
	}
	resolution := claim.resolution
	if resolution == nil {
		return
	}
	resolution.once.Do(func() {
		registry := resolution.registry
		if registry == nil {
			return
		}
		registry.mutex.Lock()
		defer registry.mutex.Unlock()
		record := resolution.record
		if record != nil && record.state == proofClaimedV1 &&
			record.proofDigest != "" &&
			registry.records[record.proofDigest] == record {
			if registry.closed || registry.now == nil {
				registry.deleteRecordLocked(record.proofDigest, record)
			} else if now, err := registry.nowLocked(); err != nil ||
				!now.Before(record.expiresAt) {
				registry.deleteRecordLocked(record.proofDigest, record)
			} else {
				record.state = proofIssuedV1
			}
		}
	})
}

// Close zeroes all retained binding and digest-derived state and invalidates
// every outstanding proof and claim. It is idempotent.
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
	for digest, record := range registry.records {
		registry.zeroRecordLocked(record)
		delete(registry.records, digest)
	}
	clear(registry.sessionCounts)
	registry.records = nil
	registry.sessionCounts = nil
	registry.entropy = nil
	registry.now = nil
	registry.lastNow = time.Time{}
}

func freezeBindingV1(
	input ExactBindingV1,
	now time.Time,
) (frozenBindingV1, error) {
	statement, err := controlapicontract.RestoreControlConfirmationStatementV1(
		append([]byte(nil), input.StatementCanonical...),
		input.StatementDigest,
	)
	if err != nil {
		return frozenBindingV1{}, err
	}
	request, err := controlapicontract.RestoreControlOperationRequestV1(
		append([]byte(nil), input.RequestCanonical...),
		input.RequestDigest,
	)
	if err != nil {
		return frozenBindingV1{}, err
	}
	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		request.Scope,
	)
	if err != nil || scopeDigest != request.ScopeDigest {
		return frozenBindingV1{}, ErrInvalidInput
	}
	if statement.Intent != controlapicontract.OperationIntentMutateV1 ||
		request.Intent != controlapicontract.OperationIntentMutateV1 ||
		request.ConfirmationDigest != input.StatementDigest ||
		statement.PrincipalID != request.PrincipalID ||
		statement.Capability != request.Capability ||
		statement.Intent != request.Intent ||
		statement.Operation != request.Operation ||
		statement.Scope != request.Scope ||
		statement.ScopeDigest != request.ScopeDigest ||
		statement.IdempotencyKeyDigest != request.IdempotencyKeyDigest ||
		statement.InputDigest != request.InputDigest ||
		statement.OperationEvaluationDigest != request.OperationEvaluationDigest ||
		statement.ExpectedRef != request.ExpectedRef {
		return frozenBindingV1{}, ErrInvalidInput
	}
	sessionInput, scopeSetDigest, err := input.Authority.AuthorizeCurrentV1(
		request.Capability,
		scope,
		true,
	)
	if err != nil {
		return frozenBindingV1{}, ErrInvalidInput
	}
	session, _, sessionDigest, err := controlapicontract.NewControlSessionV1(
		sessionInput,
	)
	if err != nil {
		return frozenBindingV1{}, err
	}
	nowMicros, ok := safeUnixMicrosV1(now)
	if !ok || nowMicros < session.IssuedAtUnixMicros ||
		nowMicros >= session.ExpiresAtUnixMicros ||
		!moduleapi.ValidSHA256(scopeSetDigest) ||
		!equalDigestV1(scopeSetDigest, session.ScopeSetDigest) ||
		!sessionHasCapabilityV1(session, request.Capability) {
		return frozenBindingV1{}, ErrInvalidInput
	}
	if session.PrincipalID != request.PrincipalID ||
		scope != request.Scope || scopeDigest != request.ScopeDigest {
		return frozenBindingV1{}, ErrInvalidInput
	}
	return frozenBindingV1{
		bootID:                session.BootID,
		sessionID:             session.SessionID,
		principalID:           session.PrincipalID,
		authorizationRevision: session.AuthorizationRevision,
		scopeSetDigest:        session.ScopeSetDigest,
		sessionDigest:         sessionDigest,
		sessionExpiresAt:      session.ExpiresAtUnixMicros,
		capability:            request.Capability,
		scope:                 scope,
		scopeDigest:           scopeDigest,
		intent:                statement.Intent,
		operation:             statement.Operation,
		statementDigest:       input.StatementDigest,
		requestDigest:         input.RequestDigest,
	}, nil
}

func equalBindingV1(left, right frozenBindingV1) bool {
	return left.bootID == right.bootID &&
		left.sessionID == right.sessionID &&
		left.principalID == right.principalID &&
		left.authorizationRevision == right.authorizationRevision &&
		equalDigestV1(left.scopeSetDigest, right.scopeSetDigest) &&
		equalDigestV1(left.sessionDigest, right.sessionDigest) &&
		left.sessionExpiresAt == right.sessionExpiresAt &&
		left.capability == right.capability &&
		left.scope == right.scope &&
		equalDigestV1(left.scopeDigest, right.scopeDigest) &&
		left.intent == right.intent &&
		left.operation == right.operation &&
		equalDigestV1(left.statementDigest, right.statementDigest) &&
		equalDigestV1(left.requestDigest, right.requestDigest)
}

func (registry *RegistryV1) nowLocked() (time.Time, error) {
	if registry.now == nil {
		return time.Time{}, ErrInvalidConfiguration
	}
	now := registry.now()
	if _, ok := safeUnixMicrosV1(now); !ok || now.Before(registry.lastNow) {
		return time.Time{}, ErrInvalidConfiguration
	}
	registry.lastNow = now
	return now, nil
}

func (registry *RegistryV1) purgeExpiredLocked(now time.Time) {
	for digest, record := range registry.records {
		if record != nil && !now.Before(record.expiresAt) {
			registry.deleteRecordLocked(digest, record)
		}
	}
}

func (registry *RegistryV1) deleteRecordLocked(
	digest string,
	record *proofRecordV1,
) {
	if record == nil {
		return
	}
	delete(registry.records, digest)
	if count := registry.sessionCounts[record.sessionKey]; count <= 1 {
		delete(registry.sessionCounts, record.sessionKey)
	} else {
		registry.sessionCounts[record.sessionKey] = count - 1
	}
	registry.zeroRecordLocked(record)
}

func (registry *RegistryV1) zeroRecordLocked(record *proofRecordV1) {
	if record == nil {
		return
	}
	record.proofDigest = ""
	record.binding = frozenBindingV1{}
	record.sessionKey = ""
	record.issuedAt = time.Time{}
	record.expiresAt = time.Time{}
	record.state = 0
}

func sessionCountKeyV1(bootID, sessionID string) string {
	return moduleapi.Digest(
		sessionCountKeyDomainV1,
		[]byte(bootID+"\x00"+sessionID),
	)
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
	if micros <= 0 || micros > maximumSafeUnixMicrosV1 {
		return 0, false
	}
	return uint64(micros), true
}

func equalDigestV1(left, right string) bool {
	return len(left) == len(right) &&
		subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
