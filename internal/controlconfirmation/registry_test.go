package controlconfirmation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var testNowV1 = time.Unix(1_800_000_000, 123_000_000)

type testClockV1 struct {
	mutex sync.Mutex
	now   time.Time
}

func (clock *testClockV1) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *testClockV1) Set(now time.Time) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = now
}

type testFixtureV1 struct {
	permit  *controlsession.PermitV1
	scope   controlapicontract.ControlScopeV1
	binding ExactBindingV1
}

func newTestFixtureV1(t *testing.T, label string) testFixtureV1 {
	t.Helper()
	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeWorkspaceV1,
			TenantID:      "tenant-a",
			WorkspaceID:   "workspace-a",
		},
	)
	if err != nil {
		t.Fatalf("freeze scope: %v", err)
	}
	statement, statementCanonical, statementDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(
			controlapicontract.ControlConfirmationStatementV1{
				SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
				PrincipalID:               "operator-local",
				Capability:                controlapicontract.CapabilityOperateModulesV1,
				Intent:                    controlapicontract.OperationIntentMutateV1,
				Operation:                 controlapicontract.OperationModuleUpgradeReviewV1,
				Scope:                     scope,
				ScopeDigest:               scopeDigest,
				IdempotencyKeyDigest:      strings.Repeat("8", 64),
				InputDigest:               strings.Repeat("9", 64),
				OperationEvaluationDigest: strings.Repeat("c", 64),
				ExpectedRef: controlapicontract.ExpectedResourceRefV1{
					Kind:       controlapicontract.ResourcePublishedPointerV1,
					ResourceID: "tenant-a",
					Revision:   9,
					Digest:     strings.Repeat("a", 64),
				},
			},
		)
	if err != nil {
		t.Fatalf("freeze statement: %v", err)
	}
	request, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(
			controlapicontract.ControlOperationRequestV1{
				SchemaVersion:             controlapicontract.ControlOperationRequestSchemaVersionV1,
				PrincipalID:               statement.PrincipalID,
				Capability:                statement.Capability,
				Scope:                     statement.Scope,
				ScopeDigest:               statement.ScopeDigest,
				Operation:                 statement.Operation,
				Intent:                    statement.Intent,
				IdempotencyKeyDigest:      statement.IdempotencyKeyDigest,
				InputDigest:               statement.InputDigest,
				OperationEvaluationDigest: statement.OperationEvaluationDigest,
				ExpectedRef:               statement.ExpectedRef,
				ConfirmationDigest:        statementDigest,
			},
		)
	if err != nil {
		t.Fatalf("freeze request: %v", err)
	}
	permit := mintTestPermitV1(
		t,
		label,
		request.PrincipalID,
		3,
		[]controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityObserveV1,
			controlapicontract.CapabilityOperateModulesV1,
		},
		[]controlapicontract.ControlScopeV1{scope},
		request.Capability,
		scope,
		true,
	)
	return testFixtureV1{
		permit: permit,
		scope:  scope,
		binding: ExactBindingV1{
			Authority:          permit,
			StatementCanonical: statementCanonical,
			StatementDigest:    statementDigest,
			RequestCanonical:   requestCanonical,
			RequestDigest:      requestDigest,
		},
	}
}

func mintTestPermitV1(
	t *testing.T,
	label string,
	principalID string,
	authorizationRevision uint64,
	capabilities []controlapicontract.ControlCapabilityV1,
	scopes []controlapicontract.ControlScopeV1,
	admittedCapability controlapicontract.ControlCapabilityV1,
	admittedScope controlapicontract.ControlScopeV1,
	requireCSRF bool,
) *controlsession.PermitV1 {
	t.Helper()
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(scopes)
	if err != nil {
		t.Fatalf("NewAuthorizedScopeSetV1: %v", err)
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(label))
	registry, bootstrap, err := controlsession.NewRegistryV1(
		controlsession.RegistryConfigV1{
			PrincipalID:           principalID,
			AuthorizationRevision: authorizationRevision,
			Capabilities: append(
				[]controlapicontract.ControlCapabilityV1(nil),
				capabilities...,
			),
			ScopeSet: scopeSet,
			// controlsession consumes two blocks for the bootstrap and four
			// more for the session ID, credential, CSRF, and resume proofs.
			Entropy: bytes.NewReader(entropyBlocksV1(
				6,
				hasher.Sum64(),
			)),
			Now: func() time.Time { return testNowV1 },
		},
	)
	if err != nil {
		t.Fatalf("controlsession.NewRegistryV1: %v", err)
	}
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	clear(bootstrap.Capability)
	if err != nil {
		registry.Close()
		t.Fatalf("ExchangeBootstrap: %v", err)
	}
	permit, err := registry.Admit(controlsession.AdmissionV1{
		SessionCredential: issued.SessionCredential,
		CSRFToken:         issued.CSRFToken,
		RequireCSRF:       requireCSRF,
		Capability:        admittedCapability,
		Scope:             admittedScope,
	})
	clear(issued.SessionCredential)
	clear(issued.CSRFToken)
	if err != nil {
		registry.Close()
		t.Fatalf("Admit: %v", err)
	}
	t.Cleanup(func() {
		permit.Release()
		registry.Close()
	})
	return permit
}

func newTestRegistryV1(
	t *testing.T,
	clock *testClockV1,
	entropy io.Reader,
) *RegistryV1 {
	t.Helper()
	registry, err := NewRegistryV1(RegistryConfigV1{
		Entropy: entropy,
		Now:     clock.Now,
	})
	if err != nil {
		t.Fatalf("NewRegistryV1: %v", err)
	}
	return registry
}

func entropyBlocksV1(count int, start uint64) []byte {
	result := make([]byte, count*ProofBytesV1)
	for index := 0; index < count; index++ {
		block := result[index*ProofBytesV1 : (index+1)*ProofBytesV1]
		binary.LittleEndian.PutUint64(block, start+uint64(index))
		for position := 8; position < len(block); position++ {
			block[position] = byte(position*17 + index%251)
		}
	}
	return result
}

func cloneBindingV1(input ExactBindingV1) ExactBindingV1 {
	result := input
	result.StatementCanonical = append([]byte(nil), input.StatementCanonical...)
	result.RequestCanonical = append([]byte(nil), input.RequestCanonical...)
	return result
}

func TestIssueClaimConsumeLifecycleV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	entropy := entropyBlocksV1(1, 41)
	registry := newTestRegistryV1(t, clock, bytes.NewReader(entropy))
	fixture := newTestFixtureV1(t, "session-001")

	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(issued.Proof) != ProofBytesV1 ||
		!bytes.Equal(issued.Proof, entropy[:ProofBytesV1]) {
		t.Fatalf("Issue returned wrong raw proof")
	}
	wantExpiry := uint64(testNowV1.Add(ProofLifetimeV1).UnixMicro())
	if issued.ExpiresAtUnixMicros != wantExpiry {
		t.Fatalf("expiry = %d, want %d", issued.ExpiresAtUnixMicros, wantExpiry)
	}
	claim, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := registry.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("concurrent second Claim error = %v", err)
	}
	claim.Consume()
	claim.Consume()
	if _, err := registry.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("consumed proof Claim error = %v", err)
	}
}

func TestIssueLifetimeIsClampedToLiveSessionV1(t *testing.T) {
	fixture := newTestFixtureV1(t, "session-near-expiry")
	sessionExpiryMicros := fixture.permit.Session().ExpiresAtUnixMicros
	sessionExpiry := time.UnixMicro(int64(sessionExpiryMicros))
	clock := &testClockV1{now: sessionExpiry.Add(-45 * time.Second)}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 42)),
	)

	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.ExpiresAtUnixMicros != sessionExpiryMicros {
		t.Fatalf(
			"expiry = %d, want session expiry %d",
			issued.ExpiresAtUnixMicros,
			sessionExpiryMicros,
		)
	}
	clock.Set(sessionExpiry)
	if _, err := registry.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("Claim at session expiry error = %v", err)
	}
}

func TestReleaseNoEffectRestoresOnlyLiveClaimV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 1)),
	)
	fixture := newTestFixtureV1(t, "session-release")
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claim, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	claim.ReleaseNoEffect()
	claim.ReleaseNoEffect()
	reclaimed, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim after ReleaseNoEffect: %v", err)
	}
	reclaimed.Consume()
}

func TestClaimValueCopiesShareOneResolutionV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(2, 6)),
	)
	fixture := newTestFixtureV1(t, "session-claim-copy")
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claim, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	copyOfClaim := *claim
	claim.Consume()
	copyOfClaim.ReleaseNoEffect()
	if _, err := registry.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("copied Claim revived consumed proof: %v", err)
	}

	second, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("second Issue: %v", err)
	}
	secondClaim, err := registry.Claim(second.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	secondCopy := *secondClaim
	secondClaim.ReleaseNoEffect()
	secondCopy.Consume()
	reclaimed, err := registry.Claim(second.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("ReleaseNoEffect did not remain the single decision: %v", err)
	}
	reclaimed.Consume()
}

func TestExpiryRejectsAndPurgesIssuedAndAbandonedClaimV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(MaximumSessionProofsV1+1, 1)),
	)
	fixture := newTestFixtureV1(t, "session-expiry")
	proofs := make([]IssuedProofV1, MaximumSessionProofsV1)
	for index := range proofs {
		var err error
		proofs[index], err = registry.Issue(fixture.binding)
		if err != nil {
			t.Fatalf("Issue %d: %v", index, err)
		}
	}
	abandoned, err := registry.Claim(proofs[0].Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim abandoned: %v", err)
	}
	clock.Set(testNowV1.Add(ProofLifetimeV1))
	if _, err := registry.Claim(proofs[1].Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("expired Claim error = %v", err)
	}
	if _, err := registry.Issue(fixture.binding); err != nil {
		t.Fatalf("Issue did not purge expired ISSUED/CLAIMED records: %v", err)
	}
	// The stale handle cannot revive or remove the replacement record.
	abandoned.ReleaseNoEffect()
}

func TestCapacityIsExactAndMultipleIssuesAreIndependentV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	reader := bytes.NewReader(entropyBlocksV1(MaximumSessionProofsV1+1, 50))
	registry := newTestRegistryV1(t, clock, reader)
	fixture := newTestFixtureV1(t, "session-capacity")
	seen := make(map[string]struct{})
	for index := 0; index < MaximumSessionProofsV1; index++ {
		issued, err := registry.Issue(fixture.binding)
		if err != nil {
			t.Fatalf("Issue %d: %v", index, err)
		}
		seen[string(issued.Proof)] = struct{}{}
	}
	if len(seen) != MaximumSessionProofsV1 {
		t.Fatalf("same statement issues were not independent: %d", len(seen))
	}
	if _, err := registry.Issue(fixture.binding); !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("per-session capacity error = %v", err)
	}
	other := newTestFixtureV1(t, "session-capacity-other")
	if _, err := registry.Issue(other.binding); err != nil {
		t.Fatalf("other session should retain capacity: %v", err)
	}
}

func TestGlobalCapacityIsExactV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(MaximumGlobalProofsV1+1, 1000)),
	)
	for index := 0; index < MaximumGlobalProofsV1; index++ {
		fixture := newTestFixtureV1(
			t,
			"global-session-"+strconv.Itoa(index/MaximumSessionProofsV1),
		)
		if _, err := registry.Issue(fixture.binding); err != nil {
			t.Fatalf("Issue %d: %v", index, err)
		}
	}
	extra := newTestFixtureV1(t, "global-session-extra")
	if _, err := registry.Issue(extra.binding); !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("global capacity error = %v", err)
	}
}

func TestEntropyFailureAndCollisionDoNotConsumeCapacityV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	fixture := newTestFixtureV1(t, "session-entropy")
	failing := newTestRegistryV1(t, clock, bytes.NewReader([]byte{1, 2, 3}))
	if _, err := failing.Issue(fixture.binding); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("short entropy error = %v", err)
	}

	block := entropyBlocksV1(1, 71)
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(append(append([]byte(nil), block...), block...)),
	)
	first, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("first Issue: %v", err)
	}
	if _, err := registry.Issue(fixture.binding); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("entropy collision error = %v", err)
	}
	claim, err := registry.Claim(first.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("first proof after collision: %v", err)
	}
	claim.Consume()
}

func TestRestartAndCloseInvalidateWithoutEnumerationV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	fixture := newTestFixtureV1(t, "session-restart")
	first := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 81)),
	)
	issued, err := first.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 82)),
	)
	if _, err := second.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("old proof against restarted registry = %v", err)
	}
	first.Close()
	first.Close()
	if _, err := first.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("closed Claim error = %v", err)
	}
	if _, err := first.Issue(fixture.binding); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Issue error = %v", err)
	}
	var nilRegistry *RegistryV1
	if _, err := nilRegistry.Claim(issued.Proof, fixture.binding); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("nil registry Claim error = %v", err)
	}
}

func TestCloseZeroesClaimedRecordAndOutstandingHandleIsInertV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 86)),
	)
	fixture := newTestFixtureV1(t, "session-close-claim")
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claim, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	record := claim.resolution.record
	registry.Close()
	if record.proofDigest != "" || record.sessionKey != "" ||
		record.state != 0 || record.binding != (frozenBindingV1{}) {
		t.Fatal("Close did not zero the claimed record")
	}
	claim.ReleaseNoEffect()
	claim.Consume()
	var nilClaim *ClaimV1
	nilClaim.Consume()
	nilClaim.ReleaseNoEffect()
}

func TestThirtyTwoConcurrentClaimsHaveOneWinnerV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 91)),
	)
	fixture := newTestFixtureV1(t, "session-concurrent")
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	const contenders = 32
	start := make(chan struct{})
	results := make(chan error, contenders)
	claims := make(chan *ClaimV1, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claim, claimErr := registry.Claim(issued.Proof, fixture.binding)
			if claim != nil {
				claims <- claim
			}
			results <- claimErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(claims)
	winners := 0
	for claimErr := range results {
		if claimErr == nil {
			winners++
			continue
		}
		if !errors.Is(claimErr, ErrProofRejected) {
			t.Fatalf("loser error = %v", claimErr)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	for claim := range claims {
		claim.Consume()
	}
}

func TestOnlyLiveExactCSRFMintedPermitCanIssueV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	fixture := newTestFixtureV1(t, "session-authority")
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(2, 101)),
	)
	noCSRF := mintTestPermitV1(
		t,
		"session-authority-no-csrf",
		"operator-local",
		3,
		[]controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityOperateModulesV1,
		},
		[]controlapicontract.ControlScopeV1{fixture.scope},
		controlapicontract.CapabilityOperateModulesV1,
		fixture.scope,
		false,
	)
	bad := cloneBindingV1(fixture.binding)
	bad.Authority = noCSRF
	if _, err := registry.Issue(bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-CSRF permit Issue error = %v", err)
	}

	copiedPermit := *fixture.permit
	copiedPermit.Release()
	if _, err := registry.Issue(fixture.binding); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("released copied permit Issue error = %v", err)
	}
}

func TestClaimRevalidatesEveryAuthorityBindingV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	fixture := newTestFixtureV1(t, "session-drift")
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 111)),
	)
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	capabilities := []controlapicontract.ControlCapabilityV1{
		controlapicontract.CapabilityObserveV1,
		controlapicontract.CapabilityOperateModulesV1,
	}
	otherScope, _, _, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeWorkspaceV1,
			TenantID:      "tenant-a",
			WorkspaceID:   "workspace-other",
		},
	)
	if err != nil {
		t.Fatalf("freeze other scope: %v", err)
	}
	extraScope, _, _, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeTenantV1,
			TenantID:      "tenant-extra",
		},
	)
	if err != nil {
		t.Fatalf("freeze extra scope: %v", err)
	}
	alternates := map[string]*controlsession.PermitV1{
		"boot and session": mintTestPermitV1(
			t, "drift-other-session", "operator-local", 3,
			capabilities, []controlapicontract.ControlScopeV1{fixture.scope},
			controlapicontract.CapabilityOperateModulesV1, fixture.scope, true,
		),
		"principal": mintTestPermitV1(
			t, "drift-principal", "operator-other", 3,
			capabilities, []controlapicontract.ControlScopeV1{fixture.scope},
			controlapicontract.CapabilityOperateModulesV1, fixture.scope, true,
		),
		"authorization revision": mintTestPermitV1(
			t, "drift-revision", "operator-local", 4,
			capabilities, []controlapicontract.ControlScopeV1{fixture.scope},
			controlapicontract.CapabilityOperateModulesV1, fixture.scope, true,
		),
		"scope-set digest": mintTestPermitV1(
			t, "drift-scope-set", "operator-local", 3,
			capabilities,
			[]controlapicontract.ControlScopeV1{fixture.scope, extraScope},
			controlapicontract.CapabilityOperateModulesV1, fixture.scope, true,
		),
		"capability": mintTestPermitV1(
			t, "drift-capability", "operator-local", 3,
			capabilities, []controlapicontract.ControlScopeV1{fixture.scope},
			controlapicontract.CapabilityObserveV1, fixture.scope, true,
		),
		"exact scope": mintTestPermitV1(
			t, "drift-scope", "operator-local", 3,
			capabilities, []controlapicontract.ControlScopeV1{otherScope},
			controlapicontract.CapabilityOperateModulesV1, otherScope, true,
		),
	}
	for name, permit := range alternates {
		t.Run(name, func(t *testing.T) {
			binding := cloneBindingV1(fixture.binding)
			binding.Authority = permit
			if _, claimErr := registry.Claim(issued.Proof, binding); !errors.Is(claimErr, ErrProofRejected) {
				t.Fatalf("Claim error = %v", claimErr)
			}
		})
	}
	claim, err := registry.Claim(issued.Proof, fixture.binding)
	if err != nil {
		t.Fatalf("restored authority Claim: %v", err)
	}
	claim.Consume()
}

func TestClaimRejectsDifferentExactStatementOrFinalRequestV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	fixture := newTestFixtureV1(t, "session-exact")
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 121)),
	)
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	other := newTestFixtureV1(t, "session-exact")
	other.binding.StatementDigest = strings.Repeat("f", 64)
	for name, binding := range map[string]ExactBindingV1{
		"wrong statement digest": other.binding,
		"corrupt statement canonical": func() ExactBindingV1 {
			value := cloneBindingV1(fixture.binding)
			value.StatementCanonical[0] = '['
			return value
		}(),
		"wrong request digest": func() ExactBindingV1 {
			value := cloneBindingV1(fixture.binding)
			value.RequestDigest = strings.Repeat("e", 64)
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, claimErr := registry.Claim(issued.Proof, binding); !errors.Is(claimErr, ErrProofRejected) {
				t.Fatalf("Claim error = %v", claimErr)
			}
		})
	}
}

func TestIssueAndClaimUseDefensiveCopiesV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	entropy := entropyBlocksV1(1, 131)
	registry := newTestRegistryV1(t, clock, bytes.NewReader(entropy))
	fixture := newTestFixtureV1(t, "session-copy")
	originalBinding := cloneBindingV1(fixture.binding)
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	originalProof := append([]byte(nil), issued.Proof...)
	registry.mutex.Lock()
	if len(registry.records) != 1 {
		registry.mutex.Unlock()
		t.Fatalf("records = %d, want 1", len(registry.records))
	}
	wantDigest := moduleapi.Digest(proofDigestDomainV1, originalProof)
	for digest, record := range registry.records {
		if digest != wantDigest || record.proofDigest != wantDigest ||
			digest == string(originalProof) {
			registry.mutex.Unlock()
			t.Fatal("registry did not retain only the domain-separated proof digest")
		}
	}
	registry.mutex.Unlock()
	issued.Proof[0] ^= 0xff
	fixture.binding.StatementCanonical[0] = '['
	fixture.binding.RequestCanonical[0] = '['
	claim, err := registry.Claim(originalProof, originalBinding)
	if err != nil {
		t.Fatalf("Claim after caller mutations: %v", err)
	}
	claim.Consume()
}

func TestTypedNilInputsAndInvalidClockFailClosedV1(t *testing.T) {
	var typedNilReader *bytes.Reader
	registry, err := NewRegistryV1(RegistryConfigV1{
		Entropy: typedNilReader,
		Now:     func() time.Time { return testNowV1 },
	})
	if err != nil {
		t.Fatalf("typed nil entropy should select CSPRNG: %v", err)
	}
	fixture := newTestFixtureV1(t, "session-typed-nil")
	bad := cloneBindingV1(fixture.binding)
	bad.Authority = nil
	if _, err := registry.Issue(bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("typed nil authority Issue error = %v", err)
	}
	if _, err := registry.Claim(make([]byte, ProofBytesV1), bad); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("typed nil authority Claim error = %v", err)
	}
	if _, err := NewRegistryV1(RegistryConfigV1{
		Now: func() time.Time { return time.Time{} },
	}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("zero clock error = %v", err)
	}
}

func TestClockRegressionFailsClosedV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 141)),
	)
	fixture := newTestFixtureV1(t, "session-clock")
	clock.Set(testNowV1.Add(-time.Microsecond))
	if _, err := registry.Issue(fixture.binding); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("regressed clock error = %v", err)
	}
}

func TestFiniteErrorsAndNonEnumeratingProofFailuresV1(t *testing.T) {
	clock := &testClockV1{now: testNowV1}
	registry := newTestRegistryV1(
		t,
		clock,
		bytes.NewReader(entropyBlocksV1(1, 151)),
	)
	fixture := newTestFixtureV1(t, "session-errors")
	issued, err := registry.Issue(fixture.binding)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	wrong := append([]byte(nil), issued.Proof...)
	wrong[0] ^= 0xff
	for name, proof := range map[string][]byte{
		"malformed": nil,
		"wrong":     wrong,
	} {
		t.Run(name, func(t *testing.T) {
			_, claimErr := registry.Claim(proof, fixture.binding)
			if claimErr != ErrProofRejected {
				t.Fatalf("error = %v, want exact ErrProofRejected", claimErr)
			}
		})
	}
}
