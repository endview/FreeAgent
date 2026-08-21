package controlsession

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
)

var registryTestEpochV1 = time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)

func TestNilPermitExposesNoAuthorizationFacts(t *testing.T) {
	var permit *PermitV1
	if permit.Digest() != "" || permit.Session().SessionID != "" ||
		permit.Scopes() != nil || permit.Allows(testTenantScopeV1("tenant-a")) {
		t.Fatal("nil Permit exposed authorization facts")
	}
	permit.Release()
}

func TestPermitCurrentAuthorizationAndCopiedReleaseAreLinearV1(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	admission := testAdmissionV1(issued, true)
	permit, err := registry.Admit(admission)
	if err != nil {
		t.Fatal(err)
	}
	session, scopeSetDigest, err := permit.AuthorizeCurrentV1(
		admission.Capability,
		admission.Scope,
		true,
	)
	if err != nil || session.SessionID != issued.Metadata.SessionID ||
		scopeSetDigest != issued.Metadata.ScopeSetDigest {
		t.Fatalf("current authorization drifted: session=%+v digest=%q err=%v", session, scopeSetDigest, err)
	}
	if _, _, err := permit.AuthorizeCurrentV1(
		controlapicontract.CapabilityRunLearningV1,
		admission.Scope,
		true,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("different capability error = %v", err)
	}
	otherScope := admission.Scope
	otherScope.WorkspaceID = "workspace-other"
	if _, _, err := permit.AuthorizeCurrentV1(
		admission.Capability,
		otherScope,
		true,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("different admitted scope error = %v", err)
	}
	if _, _, err := permit.AuthorizeCurrentV1(
		admission.Capability,
		admission.Scope,
		false,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("different CSRF admission mode error = %v", err)
	}
	safeRead := admission
	safeRead.RequireCSRF = false
	safeRead.CSRFToken = nil
	safePermit, err := registry.Admit(safeRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := safePermit.AuthorizeCurrentV1(
		safeRead.Capability,
		safeRead.Scope,
		true,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-CSRF admission authorized mutation boundary: %v", err)
	}
	safePermit.Release()

	copied := *permit
	copied.Release()
	permit.Release()
	if registry.globalActive != 0 {
		t.Fatalf("copied Permit double release left active=%d", registry.globalActive)
	}
	if permit.Session().SessionID != "" || permit.Digest() != "" ||
		permit.Scopes() != nil || permit.Allows(admission.Scope) {
		t.Fatal("released Permit retained usable authorization facts")
	}
	if _, _, err := permit.AuthorizeCurrentV1(
		admission.Capability,
		admission.Scope,
		true,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("released Permit authorization error = %v", err)
	}
}

func TestPermitCurrentAuthorizationFailsClosedAfterExpiryAndRegistryCloseV1(t *testing.T) {
	clock := &testClockV1{value: registryTestEpochV1}
	registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	if err != nil {
		t.Fatal(err)
	}
	admission := testAdmissionV1(issued, true)
	permit, err := registry.Admit(admission)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(registryTestEpochV1.Add(SessionIdleLifetimeV1))
	if _, _, err := permit.AuthorizeCurrentV1(
		admission.Capability,
		admission.Scope,
		true,
	); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired Permit authorization error = %v", err)
	}
	permit.Release()

	registry2, issued2 := newIssuedTestSessionV1(t)
	admission2 := testAdmissionV1(issued2, true)
	permit2, err := registry2.Admit(admission2)
	if err != nil {
		t.Fatal(err)
	}
	registry2.Close()
	if _, _, err := permit2.AuthorizeCurrentV1(
		admission2.Capability,
		admission2.Scope,
		true,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("closed Registry Permit authorization error = %v", err)
	}
	permit2.Release()
}

func TestRegistryRejectsInvalidServerConfiguration(t *testing.T) {
	scopeSet, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{
		testTenantScopeV1("tenant-a"),
	})
	if err != nil {
		t.Fatal(err)
	}
	base := RegistryConfigV1{
		PrincipalID:           "operator-local",
		AuthorizationRevision: 1,
		Capabilities: []controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityObserveV1,
		},
		ScopeSet: scopeSet,
		Entropy:  newIncrementingReaderV1(),
		Now:      func() time.Time { return registryTestEpochV1 },
	}
	tests := []struct {
		name   string
		mutate func(*RegistryConfigV1)
	}{
		{"nil ScopeSet", func(config *RegistryConfigV1) { config.ScopeSet = nil }},
		{"empty principal", func(config *RegistryConfigV1) { config.PrincipalID = "" }},
		{"zero authorization revision", func(config *RegistryConfigV1) { config.AuthorizationRevision = 0 }},
		{"empty capabilities", func(config *RegistryConfigV1) { config.Capabilities = nil }},
		{"open capability enum", func(config *RegistryConfigV1) {
			config.Capabilities = []controlapicontract.ControlCapabilityV1{"ADMIN"}
		}},
		{"invalid clock", func(config *RegistryConfigV1) {
			config.Now = func() time.Time { return time.Time{} }
		}},
		{"short entropy", func(config *RegistryConfigV1) {
			config.Entropy = bytes.NewReader(make([]byte, 4))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.Capabilities = append(
				[]controlapicontract.ControlCapabilityV1(nil),
				base.Capabilities...,
			)
			test.mutate(&config)
			if _, material, err := NewRegistryV1(config); err == nil ||
				material.BootID != "" || material.Capability != nil {
				t.Fatalf("invalid configuration result: material=%+v err=%v", material, err)
			}
		})
	}
}

func TestRegistryBootstrapSessionAndAdmission(t *testing.T) {
	clock := &testClockV1{value: registryTestEpochV1}
	registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
	if registry.BootID() == "" || registry.BootID() != bootstrap.BootID ||
		len(bootstrap.Capability) != CredentialBytesV1 {
		t.Fatalf("invalid bootstrap material: %+v", bootstrap)
	}
	if got := bootstrap.ExpiresAtUnixMicros; got != uint64(
		registryTestEpochV1.Add(BootstrapLifetimeV1).UnixMicro(),
	) {
		t.Fatalf("bootstrap expiry = %d", got)
	}
	if bytes.Contains([]byte(registry.bootstrapDigest), bootstrap.Capability) {
		t.Fatal("bootstrap registry retained raw capability bytes")
	}

	bootstrapDone := registry.BootstrapDone()
	select {
	case <-bootstrapDone:
		t.Fatal("bootstrap completion signaled before exchange")
	default:
	}
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-bootstrapDone:
	default:
		t.Fatal("bootstrap completion did not signal successful exchange")
	}
	if len(issued.SessionCredential) != CredentialBytesV1 ||
		len(issued.CSRFToken) != CredentialBytesV1 ||
		len(issued.ResumeCredential) != CredentialBytesV1 ||
		issued.AuthorizedScopes == nil || len(issued.AuthorizedScopes) != 2 ||
		bytes.Equal(issued.SessionCredential, issued.CSRFToken) ||
		bytes.Equal(issued.SessionCredential, issued.ResumeCredential) ||
		bytes.Equal(issued.CSRFToken, issued.ResumeCredential) ||
		bytes.Equal(issued.SessionCredential, bootstrap.Capability) ||
		issued.Metadata.SessionID == "" ||
		issued.Metadata.SessionID == issued.Metadata.BootID {
		t.Fatalf("session material was not independently generated: %+v", issued.Metadata)
	}
	if issued.Metadata.BootID != bootstrap.BootID ||
		issued.Metadata.PrincipalID != "operator-local" ||
		issued.Metadata.AuthorizationRevision != 7 ||
		issued.Metadata.ScopeSetDigest != registry.scopeSet.Digest() ||
		issued.Metadata.ExpiresAtUnixMicros-issued.Metadata.IssuedAtUnixMicros !=
			uint64(SessionAbsoluteLifetimeV1/time.Microsecond) {
		t.Fatalf("safe session metadata drifted: %+v", issued.Metadata)
	}
	issuedScopeSet, err := NewAuthorizedScopeSetV1(issued.AuthorizedScopes)
	if err != nil || issuedScopeSet.Digest() != issued.Metadata.ScopeSetDigest {
		t.Fatalf("bootstrap authorized scope digest drifted: digest=%q err=%v", issuedScopeSet.Digest(), err)
	}
	record := registry.sessionsByCredential[digestCredentialV1(
		sessionCredentialDomainV1,
		issued.SessionCredential,
	)]
	if record == nil || record.resumeDigest != digestCredentialV1(
		resumeCredentialDigestDomainV1,
		issued.ResumeCredential,
	) || record.resumeDigest == digestCredentialV1(
		csrfProofDigestDomainV1,
		issued.ResumeCredential,
	) {
		t.Fatal("resume credential was not retained only as the exact domain-separated digest")
	}
	if _, err := registry.ExchangeBootstrap(bootstrap.Capability); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("second bootstrap exchange error = %v", err)
	}

	permit, err := registry.Admit(testAdmissionV1(issued, true))
	if err != nil {
		t.Fatal(err)
	}
	if permit.Session().SessionID != issued.Metadata.SessionID ||
		permit.Digest() != issued.Metadata.ScopeSetDigest ||
		!permit.Allows(testWorkspaceScopeV1("tenant-a", "workspace-b")) {
		t.Fatal("permit did not freeze safe session and scope facts")
	}
	metadata := permit.Session()
	metadata.Capabilities[0] = controlapicontract.CapabilityRunLearningV1
	scopes := permit.Scopes()
	scopes[0].TenantID = "mutated"
	if permit.Session().Capabilities[0] != controlapicontract.CapabilityObserveV1 ||
		permit.Digest() != issued.Metadata.ScopeSetDigest ||
		permit.Scopes()[0].TenantID == "mutated" {
		t.Fatal("permit getters alias internal authorization facts")
	}
	permit.Release()
	permit.Release()
	if registry.globalActive != 0 {
		t.Fatalf("idempotent release left %d active permits", registry.globalActive)
	}

	safeRead := testAdmissionV1(issued, false)
	safeRead.CSRFToken = nil
	permit, err = registry.Admit(safeRead)
	if err != nil {
		t.Fatalf("safe read without CSRF: %v", err)
	}
	permit.Release()
}

func TestResumeSessionRotatesProofsAndKeepsCookieV1(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	originalCSRF := bytes.Clone(issued.CSRFToken)
	originalResume := bytes.Clone(issued.ResumeCredential)
	issued.AuthorizedScopes[0].TenantID = "caller-mutated"
	resumed, err := registry.ResumeSessionV1(
		issued.SessionCredential,
		issued.ResumeCredential,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(resumed.CSRFToken)
	defer clear(resumed.ResumeCredential)
	if resumed.Metadata.SessionID != issued.Metadata.SessionID ||
		len(resumed.CSRFToken) != CredentialBytesV1 ||
		len(resumed.ResumeCredential) != CredentialBytesV1 ||
		resumed.AuthorizedScopes == nil ||
		resumed.AuthorizedScopes[0].TenantID == "caller-mutated" ||
		bytes.Equal(resumed.CSRFToken, originalCSRF) ||
		bytes.Equal(resumed.ResumeCredential, originalResume) {
		t.Fatalf("resume did not rotate the browser proofs: %+v", resumed.Metadata)
	}
	scopeSet, err := NewAuthorizedScopeSetV1(resumed.AuthorizedScopes)
	if err != nil || scopeSet.Digest() != resumed.Metadata.ScopeSetDigest {
		t.Fatalf("resumed scope digest drifted: digest=%q err=%v", scopeSet.Digest(), err)
	}

	oldCSRF := testAdmissionV1(issued, true)
	if _, err := registry.Admit(oldCSRF); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old CSRF survived resume: %v", err)
	}
	newCSRF := testAdmissionV1(issued, true)
	newCSRF.CSRFToken = resumed.CSRFToken
	permit, err := registry.Admit(newCSRF)
	if err != nil {
		t.Fatalf("unchanged cookie plus new CSRF was rejected: %v", err)
	}
	permit.Release()
	if _, err := registry.ResumeSessionV1(
		issued.SessionCredential,
		originalResume,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("used resume credential survived rotation: %v", err)
	}
	second, err := registry.ResumeSessionV1(
		issued.SessionCredential,
		resumed.ResumeCredential,
	)
	if err != nil {
		t.Fatalf("new resume credential was not usable: %v", err)
	}
	clear(second.CSRFToken)
	clear(second.ResumeCredential)
	clear(originalCSRF)
	clear(originalResume)
}

func TestResumeSessionWrongProofDoesNotConsumeCurrentProofV1(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	wrongCookie := bytes.Repeat([]byte{0xcc}, CredentialBytesV1)
	wrongResume := bytes.Repeat([]byte{0xdd}, CredentialBytesV1)
	if _, err := registry.ResumeSessionV1(
		wrongCookie,
		issued.ResumeCredential,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong cookie error = %v", err)
	}
	if _, err := registry.ResumeSessionV1(
		issued.SessionCredential,
		wrongResume,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong resume error = %v", err)
	}
	if _, err := registry.ResumeSessionV1(
		issued.SessionCredential[:CredentialBytesV1-1],
		issued.ResumeCredential,
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("malformed cookie error = %v", err)
	}
	resumed, err := registry.ResumeSessionV1(
		issued.SessionCredential,
		issued.ResumeCredential,
	)
	if err != nil {
		t.Fatalf("wrong proof consumed current resume credential: %v", err)
	}
	clear(resumed.CSRFToken)
	clear(resumed.ResumeCredential)
	clear(wrongCookie)
	clear(wrongResume)
}

func TestResumeSessionConcurrentUseHasExactlyOneWinnerV1(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	const contenders = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			resumed, err := registry.ResumeSessionV1(
				issued.SessionCredential,
				issued.ResumeCredential,
			)
			switch {
			case err == nil:
				successes.Add(1)
				clear(resumed.CSRFToken)
				clear(resumed.ResumeCredential)
			case errors.Is(err, ErrUnauthenticated):
			default:
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("successes=%d unexpected=%d", successes.Load(), unexpected.Load())
	}
}

func TestResumeEntropyFailureAndCollisionKeepOldProofUsableV1(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure func(IssuedSessionV1) io.Reader
	}{
		{
			name: "short entropy",
			failure: func(IssuedSessionV1) io.Reader {
				return bytes.NewReader([]byte{1})
			},
		},
		{
			name: "repeated proof collision",
			failure: func(issued IssuedSessionV1) io.Reader {
				return bytes.NewReader(append(
					bytes.Clone(issued.CSRFToken),
					issued.ResumeCredential...,
				))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := &testClockV1{value: registryTestEpochV1}
			entropy := &switchReaderV1{reader: newIncrementingReaderV1()}
			registry, bootstrap := newTestRegistryV1(t, clock, entropy)
			issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
			if err != nil {
				t.Fatal(err)
			}
			entropy.Set(test.failure(issued))
			if _, err := registry.ResumeSessionV1(
				issued.SessionCredential,
				issued.ResumeCredential,
			); !errors.Is(err, ErrEntropyUnavailable) {
				t.Fatalf("failure error = %v", err)
			}
			entropy.Set(&incrementingReaderV1{next: 20_000})
			resumed, err := registry.ResumeSessionV1(
				issued.SessionCredential,
				issued.ResumeCredential,
			)
			if err != nil {
				t.Fatalf("old proof was not preserved: %v", err)
			}
			clear(resumed.CSRFToken)
			clear(resumed.ResumeCredential)
		})
	}
}

func TestResumeSessionExpiryAuthorizationDriftAndRestartV1(t *testing.T) {
	t.Run("idle expiry clears record", func(t *testing.T) {
		clock := &testClockV1{value: registryTestEpochV1}
		registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
		issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(registryTestEpochV1.Add(SessionIdleLifetimeV1))
		if _, err := registry.ResumeSessionV1(
			issued.SessionCredential,
			issued.ResumeCredential,
		); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("idle boundary error = %v", err)
		}
		if len(registry.sessionsByCredential) != 0 {
			t.Fatal("expired resume retained a live record")
		}
	})

	t.Run("authorization revision drift", func(t *testing.T) {
		registry, issued := newIssuedTestSessionV1(t)
		registry.authorizationRevision++
		if _, err := registry.ResumeSessionV1(
			issued.SessionCredential,
			issued.ResumeCredential,
		); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("authorization drift error = %v", err)
		}
		if len(registry.sessionsByCredential) != 0 {
			t.Fatal("authorization drift retained a live record")
		}
	})

	t.Run("scope digest drift", func(t *testing.T) {
		registry, issued := newIssuedTestSessionV1(t)
		drifted, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{
			testTenantScopeV1("tenant-other"),
		})
		if err != nil {
			t.Fatal(err)
		}
		registry.scopeSet = drifted
		if _, err := registry.ResumeSessionV1(
			issued.SessionCredential,
			issued.ResumeCredential,
		); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("scope drift error = %v", err)
		}
	})

	t.Run("new process rejects old proofs", func(t *testing.T) {
		_, issued := newIssuedTestSessionV1(t)
		newRegistry, _ := newTestRegistryV1(
			t,
			&testClockV1{value: registryTestEpochV1},
			&incrementingReaderV1{next: 50_000},
		)
		if _, err := newRegistry.ResumeSessionV1(
			issued.SessionCredential,
			issued.ResumeCredential,
		); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("old process proofs survived restart: %v", err)
		}
	})
}

func TestBootstrapEntropyFailureDoesNotConsumeCapability(t *testing.T) {
	clock := &testClockV1{value: registryTestEpochV1}
	entropy := &switchReaderV1{reader: bytes.NewReader(testEntropyV1(64))}
	registry, bootstrap := newTestRegistryV1(t, clock, entropy)
	if _, err := registry.ExchangeBootstrap(bootstrap.Capability); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("short entropy error = %v", err)
	}
	if registry.bootstrapConsumed || registry.bootstrapDigest == "" {
		t.Fatal("entropy failure consumed the bootstrap capability")
	}
	entropy.Set(newIncrementingReaderV1())
	if _, err := registry.ExchangeBootstrap(bootstrap.Capability); err != nil {
		t.Fatalf("bootstrap did not survive entropy failure: %v", err)
	}
}

func TestBootstrapConcurrentExchangeHasExactlyOneWinner(t *testing.T) {
	registry, bootstrap := newTestRegistryV1(
		t,
		&testClockV1{value: registryTestEpochV1},
		newIncrementingReaderV1(),
	)
	const contenders = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := registry.ExchangeBootstrap(bootstrap.Capability)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrUnauthenticated):
			default:
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || unexpected.Load() != 0 ||
		len(registry.sessionsByCredential) != 1 {
		t.Fatalf(
			"successes=%d unexpected=%d sessions=%d",
			successes.Load(),
			unexpected.Load(),
			len(registry.sessionsByCredential),
		)
	}
}

func TestBootstrapExpiryIsUniformAndConsumesDigest(t *testing.T) {
	clock := &testClockV1{value: registryTestEpochV1}
	registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
	bootstrapDone := registry.BootstrapDone()
	clock.Set(registryTestEpochV1.Add(BootstrapLifetimeV1))
	if _, err := registry.ExchangeBootstrap(bootstrap.Capability); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expiry error = %v", err)
	}
	if !registry.bootstrapConsumed || registry.bootstrapDigest != "" {
		t.Fatal("expired bootstrap digest was retained")
	}
	select {
	case <-bootstrapDone:
	default:
		t.Fatal("expired bootstrap did not signal handoff cleanup")
	}
	wrong := bytes.Repeat([]byte{0xff}, CredentialBytesV1)
	if _, err := registry.ExchangeBootstrap(wrong); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("consumed bootstrap enumerated its state: %v", err)
	}
}

func TestBootstrapExpiresWithoutARequest(t *testing.T) {
	registry, _ := newTestRegistryV1(
		t,
		&testClockV1{value: registryTestEpochV1},
		newIncrementingReaderV1(),
	)
	done := registry.BootstrapDone()
	registry.mutex.Lock()
	registry.bootstrapTimer.Stop()
	registry.bootstrapTimer = time.AfterFunc(time.Millisecond, registry.expireBootstrapV1)
	registry.mutex.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not expire without a later request")
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if !registry.bootstrapConsumed || registry.bootstrapDigest != "" ||
		registry.bootstrapTimer != nil {
		t.Fatal("timer expiry retained bootstrap authority")
	}
}

func TestSecondBootstrapAttemptAlwaysConsumesAuthority(t *testing.T) {
	tests := []struct {
		name   string
		second func(BootstrapMaterialV1) []byte
	}{
		{
			name: "wrong then correct",
			second: func(material BootstrapMaterialV1) []byte {
				return material.Capability
			},
		},
		{
			name: "wrong then wrong",
			second: func(BootstrapMaterialV1) []byte {
				return bytes.Repeat([]byte{0xdd}, CredentialBytesV1)
			},
		},
		{
			name: "malformed then correct",
			second: func(material BootstrapMaterialV1) []byte {
				return material.Capability
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, material := newTestRegistryV1(
				t,
				&testClockV1{value: registryTestEpochV1},
				newIncrementingReaderV1(),
			)
			first := bytes.Repeat([]byte{0xcc}, CredentialBytesV1)
			if test.name == "malformed then correct" {
				first = []byte{0xcc}
			}
			done := registry.BootstrapDone()
			if _, err := registry.ExchangeBootstrap(first); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("first attempt error = %v", err)
			}
			select {
			case <-done:
				t.Fatal("first failed attempt consumed bootstrap authority")
			default:
			}
			if _, err := registry.ExchangeBootstrap(test.second(material)); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("second attempt error = %v", err)
			}
			select {
			case <-done:
			default:
				t.Fatal("second attempt did not consume bootstrap authority")
			}
			if !registry.bootstrapConsumed || registry.bootstrapDigest != "" ||
				len(registry.sessionsByCredential) != 0 {
				t.Fatal("second attempt retained or granted bootstrap authority")
			}
		})
	}
}

func TestAdmissionChecksCSRFScopesCapabilitiesAndCurrentAuthorization(t *testing.T) {
	clock := &testClockV1{value: registryTestEpochV1}
	registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	if err != nil {
		t.Fatal(err)
	}

	wrongCSRF := testAdmissionV1(issued, true)
	wrongProof := bytes.Repeat([]byte{0xee}, CredentialBytesV1)
	wrongCSRF.CSRFToken = wrongProof
	if _, err := registry.Admit(wrongCSRF); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	wrongCapability := testAdmissionV1(issued, true)
	wrongCapability.Capability = controlapicontract.CapabilityRunLearningV1
	if _, err := registry.Admit(wrongCapability); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong capability error = %v", err)
	}
	wrongScope := testAdmissionV1(issued, true)
	wrongScope.Scope = testWorkspaceScopeV1("tenant-b", "workspace-a")
	if _, err := registry.Admit(wrongScope); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-Tenant scope error = %v", err)
	}

	registry.authorizationRevision++
	if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("stale authorization revision error = %v", err)
	}
}

func TestFailedAdmissionDoesNotRefreshIdleLifetime(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*RegistryV1, IssuedSessionV1) []*PermitV1
		mutate     func(*AdmissionV1)
		wantFailed error
	}{
		{
			name: "csrf",
			mutate: func(input *AdmissionV1) {
				wrongProof := bytes.Repeat([]byte{0xcc}, CredentialBytesV1)
				input.CSRFToken = wrongProof
			},
			wantFailed: ErrUnauthenticated,
		},
		{
			name: "capability",
			mutate: func(input *AdmissionV1) {
				input.Capability = controlapicontract.CapabilityRunLearningV1
			},
			wantFailed: ErrForbidden,
		},
		{
			name: "scope",
			mutate: func(input *AdmissionV1) {
				input.Scope = testWorkspaceScopeV1("tenant-other", "workspace-a")
			},
			wantFailed: ErrForbidden,
		},
		{
			name: "permits",
			prepare: func(registry *RegistryV1, issued IssuedSessionV1) []*PermitV1 {
				permits := make([]*PermitV1, 0, MaximumSessionAdmissionsV1)
				for range MaximumSessionAdmissionsV1 {
					permit, err := registry.Admit(testAdmissionV1(issued, true))
					if err != nil {
						t.Fatal(err)
					}
					permits = append(permits, permit)
				}
				return permits
			},
			mutate:     func(*AdmissionV1) {},
			wantFailed: ErrResourceExhausted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &testClockV1{value: registryTestEpochV1}
			registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
			issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
			if err != nil {
				t.Fatal(err)
			}
			var permits []*PermitV1
			if test.prepare != nil {
				permits = test.prepare(registry, issued)
			}
			clock.Set(registryTestEpochV1.Add(SessionIdleLifetimeV1 - time.Minute))
			bad := testAdmissionV1(issued, true)
			test.mutate(&bad)
			if _, err := registry.Admit(bad); !errors.Is(err, test.wantFailed) {
				t.Fatalf("failed admission error = %v, want %v", err, test.wantFailed)
			}
			for _, permit := range permits {
				permit.Release()
			}
			clock.Set(registryTestEpochV1.Add(SessionIdleLifetimeV1))
			if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("failed admission refreshed idle lifetime: %v", err)
			}
		})
	}
}

func TestSessionAbsoluteAndIdleExpiryBoundaries(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		clock := &testClockV1{value: registryTestEpochV1}
		registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
		issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(registryTestEpochV1.Add(SessionIdleLifetimeV1))
		if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("idle boundary error = %v", err)
		}
	})

	t.Run("absolute", func(t *testing.T) {
		clock := &testClockV1{value: registryTestEpochV1}
		registry, bootstrap := newTestRegistryV1(t, clock, newIncrementingReaderV1())
		issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
		if err != nil {
			t.Fatal(err)
		}
		for step := 1; step <= 16; step++ {
			clock.Set(registryTestEpochV1.Add(time.Duration(step) * 29 * time.Minute))
			permit, admitErr := registry.Admit(testAdmissionV1(issued, true))
			if admitErr != nil {
				t.Fatalf("keepalive step %d: %v", step, admitErr)
			}
			permit.Release()
		}
		clock.Set(registryTestEpochV1.Add(SessionAbsoluteLifetimeV1))
		if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("absolute boundary error = %v", err)
		}
	})
}

func TestAdmissionPermitsAreNonBlockingBoundedAndReleased(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	permits := make([]*PermitV1, 0, MaximumSessionAdmissionsV1)
	for range MaximumSessionAdmissionsV1 {
		permit, err := registry.Admit(testAdmissionV1(issued, true))
		if err != nil {
			t.Fatal(err)
		}
		permits = append(permits, permit)
	}
	if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("ninth per-session admission error = %v", err)
	}
	permits[0].Release()
	replacement, err := registry.Admit(testAdmissionV1(issued, true))
	if err != nil {
		t.Fatalf("released permit was not reusable: %v", err)
	}
	replacement.Release()
	for _, permit := range permits[1:] {
		permit.Release()
	}
	if registry.globalActive != 0 {
		t.Fatalf("permit leak: %d", registry.globalActive)
	}
}

func TestGlobalAdmissionLimitAcrossSessions(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	credentials := [][]byte{issued.SessionCredential}
	csrfs := [][]byte{issued.CSRFToken}
	registry.mutex.Lock()
	var template *sessionRecordV1
	for _, record := range registry.sessionsByCredential {
		template = record
	}
	for index := 1; index < 5; index++ {
		credential := bytes.Repeat([]byte{byte(0xa0 + index)}, CredentialBytesV1)
		csrf := bytes.Repeat([]byte{byte(0xb0 + index)}, CredentialBytesV1)
		digest := digestCredentialV1(sessionCredentialDomainV1, credential)
		metadata := cloneSessionV1(template.metadata)
		metadata.SessionID = "test-global-session-" + string(rune('a'+index))
		record := &sessionRecordV1{
			credentialDigest: digest,
			csrfDigest:       digestCredentialV1(csrfProofDigestDomainV1, csrf),
			metadata:         metadata,
			scopeSet:         template.scopeSet.clone(),
			expiresAt:        template.expiresAt,
			lastSeen:         template.lastSeen,
		}
		registry.sessionsByCredential[digest] = record
		registry.sessionIDs[metadata.SessionID] = struct{}{}
		credentials = append(credentials, credential)
		csrfs = append(csrfs, csrf)
	}
	registry.mutex.Unlock()

	var permits []*PermitV1
	for sessionIndex := 0; sessionIndex < 4; sessionIndex++ {
		for range MaximumSessionAdmissionsV1 {
			input := testAdmissionV1(issued, true)
			input.SessionCredential = credentials[sessionIndex]
			input.CSRFToken = csrfs[sessionIndex]
			permit, err := registry.Admit(input)
			if err != nil {
				t.Fatal(err)
			}
			permits = append(permits, permit)
		}
	}
	input := testAdmissionV1(issued, true)
	input.SessionCredential = credentials[4]
	input.CSRFToken = csrfs[4]
	if _, err := registry.Admit(input); !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("global admission 33 error = %v", err)
	}
	for _, permit := range permits {
		permit.Release()
	}
	if registry.globalActive != 0 {
		t.Fatalf("global permits leaked: %d", registry.globalActive)
	}
}

func TestRegistryCloseClearsAuthorityAndInvalidatesOldBoot(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	var record *sessionRecordV1
	for _, candidate := range registry.sessionsByCredential {
		record = candidate
	}
	permit, err := registry.Admit(testAdmissionV1(issued, true))
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
	registry.Close()
	permit.Release()
	if registry.BootID() != "" || registry.bootstrapDigest != "" ||
		registry.scopeSet != nil || registry.capabilities != nil ||
		registry.sessionsByCredential != nil || registry.sessionIDs != nil ||
		registry.globalActive != 0 || record == nil ||
		record.credentialDigest != "" || record.csrfDigest != "" ||
		record.resumeDigest != "" {
		t.Fatal("Close retained process-local authorization state")
	}
	if _, err := registry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed registry admission error = %v", err)
	}

	newRegistry, newBootstrap := newTestRegistryV1(
		t,
		&testClockV1{value: registryTestEpochV1},
		&incrementingReaderV1{next: 10_000},
	)
	if newBootstrap.BootID == issued.Metadata.BootID {
		t.Fatal("test entropy unexpectedly reused BootID")
	}
	if _, err := newRegistry.Admit(testAdmissionV1(issued, true)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old credential survived a new Boot: %v", err)
	}
}

func TestBootstrapDoneNilZeroAndCloseV1(t *testing.T) {
	var nilRegistry *RegistryV1
	select {
	case <-nilRegistry.BootstrapDone():
	default:
		t.Fatal("nil registry returned an open bootstrap signal")
	}
	zero := &RegistryV1{}
	select {
	case <-zero.BootstrapDone():
	default:
		t.Fatal("zero registry returned an open bootstrap signal")
	}
	registry, _ := newTestRegistryV1(
		t,
		&testClockV1{value: registryTestEpochV1},
		newIncrementingReaderV1(),
	)
	done := registry.BootstrapDone()
	registry.Close()
	select {
	case <-done:
	default:
		t.Fatal("registry close did not signal handoff cleanup")
	}
}

func TestConcurrentAdmitReleaseDoesNotLeakPermits(t *testing.T) {
	registry, issued := newIssuedTestSessionV1(t)
	const workers = 64
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			permit, err := registry.Admit(testAdmissionV1(issued, true))
			if err == nil {
				permit.Release()
				return
			}
			if !errors.Is(err, ErrResourceExhausted) {
				t.Errorf("concurrent admission: %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if registry.globalActive != 0 {
		t.Fatalf("concurrent admissions leaked %d permits", registry.globalActive)
	}
}

func newIssuedTestSessionV1(t *testing.T) (*RegistryV1, IssuedSessionV1) {
	t.Helper()
	registry, bootstrap := newTestRegistryV1(
		t,
		&testClockV1{value: registryTestEpochV1},
		newIncrementingReaderV1(),
	)
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	if err != nil {
		t.Fatal(err)
	}
	return registry, issued
}

func newTestRegistryV1(
	t *testing.T,
	clock *testClockV1,
	entropy io.Reader,
) (*RegistryV1, BootstrapMaterialV1) {
	t.Helper()
	scopeSet, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{
		testTenantScopeV1("tenant-a"),
		testWorkspaceScopeV1("tenant-workspace", "workspace-a"),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, bootstrap, err := NewRegistryV1(RegistryConfigV1{
		PrincipalID:           "operator-local",
		AuthorizationRevision: 7,
		Capabilities: []controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityOperateModulesV1,
			controlapicontract.CapabilityObserveV1,
		},
		ScopeSet: scopeSet,
		Entropy:  entropy,
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	return registry, bootstrap
}

func testAdmissionV1(
	issued IssuedSessionV1,
	requireCSRF bool,
) AdmissionV1 {
	return AdmissionV1{
		SessionCredential: issued.SessionCredential,
		CSRFToken:         issued.CSRFToken,
		RequireCSRF:       requireCSRF,
		Capability:        controlapicontract.CapabilityObserveV1,
		Scope:             testWorkspaceScopeV1("tenant-a", "workspace-b"),
	}
}

type testClockV1 struct {
	mutex sync.Mutex
	value time.Time
}

func (clock *testClockV1) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.value
}

func (clock *testClockV1) Set(value time.Time) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.value = value
}

type incrementingReaderV1 struct {
	mutex sync.Mutex
	next  uint64
}

func newIncrementingReaderV1() *incrementingReaderV1 {
	return &incrementingReaderV1{next: 1}
}

func (reader *incrementingReaderV1) Read(buffer []byte) (int, error) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	for index := range buffer {
		value := reader.next
		buffer[index] = byte(value ^ (value >> 8) ^ (value >> 16))
		reader.next++
	}
	return len(buffer), nil
}

type switchReaderV1 struct {
	mutex  sync.Mutex
	reader io.Reader
}

func (reader *switchReaderV1) Read(buffer []byte) (int, error) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	return reader.reader.Read(buffer)
}

func (reader *switchReaderV1) Set(next io.Reader) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.reader = next
}

func testEntropyV1(count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = byte(index + 1)
	}
	return result
}
