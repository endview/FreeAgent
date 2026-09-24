package coreloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestInvocationGateConsumesExactGrantOnce(t *testing.T) {
	result := validBeginResult(t)
	gate, err := ArmInvocationGate(result)
	if err != nil {
		t.Fatalf("ArmInvocationGate: %v", err)
	}

	expected := invocationFromResult(result)
	prepared, err := gate.ResolveAuthorized(context.Background(), expected)
	if err != nil {
		t.Fatalf("ResolveAuthorized: %v", err)
	}
	if !sameInvocation(prepared.Invocation, expected) {
		t.Fatal("prepared invocation differs from frozen Attempt")
	}
	if prepared.Binding.Provider != result.Attempt.Binding.Provider ||
		prepared.Binding.ConfigRef != result.Attempt.Binding.ConfigRef ||
		prepared.Binding.AuthorityCeilingRef !=
			result.Attempt.Binding.AuthorityCeilingRef ||
		prepared.Binding.FailurePolicy !=
			result.Attempt.Binding.FailurePolicy {
		t.Fatal("prepared binding differs from frozen Binding")
	}
	if !bytes.Equal(
		prepared.ConfigCanonical,
		result.Attempt.ModelConfigCanonical,
	) {
		t.Fatal("prepared model config differs from frozen ConfigRef content")
	}

	prepared.Invocation.Input[0] ^= 0xff
	if bytes.Equal(prepared.Invocation.Input, result.Attempt.Request.CanonicalBytes) {
		t.Fatal("prepared invocation aliases caller-owned request bytes")
	}
	prepared.ConfigCanonical[0] ^= 0xff
	if bytes.Equal(
		prepared.ConfigCanonical,
		result.Attempt.ModelConfigCanonical,
	) {
		t.Fatal("prepared model config aliases caller-owned config bytes")
	}
	if _, err := gate.ResolveAuthorized(
		context.Background(),
		expected,
	); !errors.Is(err, ErrInvocationNotAuthorized) {
		t.Fatalf("second resolve error = %v, want authorization rejection", err)
	}
	beyondTail := newRealBeginResultAtLeaseTail(t, time.Microsecond)
	if _, err := ArmInvocationGate(beyondTail); !errors.Is(
		err,
		ErrInvalidInvocationGrant,
	) {
		t.Fatalf("real permit beyond lease tail error=%v", err)
	}
}

func TestInvocationGateCannotRearmCopiedOrForgedBeginResult(t *testing.T) {
	real := validBeginResult(t)
	copy := real
	if _, err := ArmInvocationGate(real); err != nil {
		t.Fatalf("first arm: %v", err)
	}
	if _, err := ArmInvocationGate(copy); !errors.Is(
		err,
		ErrInvalidInvocationGrant,
	) {
		t.Fatalf("copied result rearm error=%v", err)
	}

	real = validBeginResult(t)
	forged := currentstore.BeginModelDispatchResult{
		Attempt:       real.Attempt,
		Lease:         real.Lease,
		Created:       true,
		InvokeAllowed: true,
	}
	if _, err := ArmInvocationGate(forged); !errors.Is(
		err,
		ErrInvalidInvocationGrant,
	) {
		t.Fatalf("forged result arm error=%v", err)
	}

	real = validBeginResult(t)
	changed := real
	changed.Attempt.AttemptID += "-changed"
	if _, err := ArmInvocationGate(changed); !errors.Is(
		err,
		ErrInvalidInvocationGrant,
	) {
		t.Fatalf("changed closure arm error=%v", err)
	}
	if _, err := ArmInvocationGate(real); err != nil {
		t.Fatalf("changed closure consumed real permit: %v", err)
	}
}

func TestInvocationGateMismatchDoesNotConsumeGrant(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*modulehost.ModuleInvocation)
	}{
		{"attempt", func(value *modulehost.ModuleInvocation) { value.InvocationID += "-other" }},
		{"run", func(value *modulehost.ModuleInvocation) { value.RunID += "-other" }},
		{"member", func(value *modulehost.ModuleInvocation) { value.MemberID += "-other" }},
		{"snapshot", func(value *modulehost.ModuleInvocation) { value.MemberSnapshotDigest = strings.Repeat("f", 64) }},
		{"port name", func(value *modulehost.ModuleInvocation) { value.Port.Name = moduleapi.PortNameContextProvide }},
		{"port version", func(value *modulehost.ModuleInvocation) { value.Port.ExactVersion = "v1" }},
		{"binding index", func(value *modulehost.ModuleInvocation) { value.BindingIndex = 1 }},
		{"request bytes", func(value *modulehost.ModuleInvocation) { value.Input = json.RawMessage(`{"changed":true}`) }},
		{"deadline", func(value *modulehost.ModuleInvocation) { value.Deadline = value.Deadline.Add(time.Microsecond) }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			result := validBeginResult(t)
			gate, err := ArmInvocationGate(result)
			if err != nil {
				t.Fatal(err)
			}
			expected := invocationFromResult(result)
			changed := cloneInvocation(expected)
			test.mutate(&changed)
			if _, err := gate.ResolveAuthorized(
				context.Background(),
				changed,
			); !errors.Is(err, ErrInvocationNotAuthorized) {
				t.Fatalf("mismatch error = %v", err)
			}
			if _, err := gate.ResolveAuthorized(
				context.Background(),
				expected,
			); err != nil {
				t.Fatalf("mismatch consumed grant: %v", err)
			}
		})
	}
}

func TestInvocationGateConcurrentResolveHasOneWinner(t *testing.T) {
	result := validBeginResult(t)
	gate, err := ArmInvocationGate(result)
	if err != nil {
		t.Fatal(err)
	}
	expected := invocationFromResult(result)

	const callers = 64
	start := make(chan struct{})
	errorsByCall := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer group.Done()
			<-start
			_, err := gate.ResolveAuthorized(
				context.Background(),
				expected,
			)
			errorsByCall <- err
		}()
	}
	close(start)
	group.Wait()
	close(errorsByCall)

	succeeded := 0
	for err := range errorsByCall {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrInvocationNotAuthorized) {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful resolves = %d, want 1", succeeded)
	}
}

func TestInvocationGateRejectsAnythingButFreshPendingGrant(t *testing.T) {
	valid := validBeginResult(t)
	tests := []struct {
		name   string
		result currentstore.BeginModelDispatchResult
	}{
		{name: "zero value"},
		{name: "not created", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Created = false
			return value
		}()},
		{name: "invoke denied", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.InvokeAllowed = false
			return value
		}()},
		{name: "not pending", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Attempt.State = corecontract.ModelAttemptSucceeded
			return value
		}()},
		{name: "zero attempt identity", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Attempt.AttemptID = ""
			return value
		}()},
		{name: "zero lease", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Lease = currentstore.RunLease{}
			return value
		}()},
		{name: "binding not frozen", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Attempt.BindingCanonical = nil
			return value
		}()},
		{name: "model config absent", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Attempt.ModelConfigCanonical = nil
			return value
		}()},
		{name: "model config differs from ConfigRef", result: func() currentstore.BeginModelDispatchResult {
			value := valid
			value.Attempt.ModelConfigCanonical = bytes.Clone(
				value.Attempt.ModelConfigCanonical,
			)
			value.Attempt.ModelConfigCanonical[0] ^= 0xff
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ArmInvocationGate(test.result); !errors.Is(
				err,
				ErrInvalidInvocationGrant,
			) {
				t.Fatalf("ArmInvocationGate error = %v", err)
			}
		})
	}

	var zero InvocationGate
	if _, err := zero.ResolveAuthorized(
		context.Background(),
		invocationFromResult(valid),
	); !errors.Is(err, ErrInvocationNotAuthorized) {
		t.Fatalf("zero gate resolve error = %v", err)
	}
}

func TestInvocationGateCanceledContextDoesNotConsume(t *testing.T) {
	result := validBeginResult(t)
	gate, err := ArmInvocationGate(result)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.ResolveAuthorized(
		ctx,
		invocationFromResult(result),
	); !errors.Is(err, ErrInvocationNotAuthorized) {
		t.Fatalf("canceled context error = %v", err)
	}
	if _, err := gate.ResolveAuthorized(
		context.Background(),
		invocationFromResult(result),
	); err != nil {
		t.Fatalf("canceled context consumed grant: %v", err)
	}
}

func TestContextReadGateAuthorizesOnlyExactRequiredLocalBindingOnce(
	t *testing.T,
) {
	run := pureChatRunFixture(t)
	run.Member.MemberID = "member-context-read"
	run.Member.MemberSnapshotDigest = strings.Repeat("9", 64)
	binding := run.Member.PortPlans[0].Bindings[0]
	binding.Provider.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	binding.Provider.AdapterIdentity = "freeagent.adapter.knowledge.test/v1"
	binding.StaticContextRefs = []string{}
	binding.FailurePolicy = moduleapi.FailureRequired
	run.Member.PortPlans = []moduleapi.PortPlan{{
		Port:     contextProvidePortV1,
		Bindings: []moduleapi.PortBinding{binding},
	}}
	lease := currentstore.RunLease{
		RunID:         run.RunID,
		OwnerID:       "context-read-owner",
		LeaseEpoch:    1,
		RunRevision:   run.RunRevision,
		FrameRevision: run.Frame.Revision,
		ExpiresAt:     time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond),
	}
	run.Frame.Lease = lease
	deadline := lease.ExpiresAt.Add(-leaseTailGrace)
	input := []byte(`{"query":"stable"}`)
	if _, _, err := ArmContextReadGate(
		run,
		lease,
		0,
		"context-read-beyond-tail",
		input,
		deadline.Add(time.Microsecond),
	); !errors.Is(err, ErrInvalidContextReadGrant) {
		t.Fatalf("context read beyond lease tail error = %v", err)
	}
	gate, invocation, err := ArmContextReadGate(
		run,
		lease,
		0,
		"context-read-stable",
		input,
		deadline,
	)
	if err != nil {
		t.Fatalf("ArmContextReadGate: %v", err)
	}
	prepared, err := gate.ResolveAuthorized(context.Background(), invocation)
	if err != nil {
		t.Fatalf("ResolveAuthorized: %v", err)
	}
	if prepared.Binding.Provider.ExecutionClass !=
		moduleapi.ExecutionTrustedInProcess ||
		prepared.Binding.FailurePolicy != moduleapi.FailureRequired ||
		len(prepared.Binding.StaticContextRefs) != 0 {
		t.Fatalf("prepared dynamic context binding = %+v", prepared.Binding)
	}
	if _, err := gate.ResolveAuthorized(
		context.Background(),
		invocation,
	); !errors.Is(err, ErrInvocationNotAuthorized) {
		t.Fatalf("second context read resolve error = %v", err)
	}
}

func TestContextReadGateRejectsDeclarativeOptionalAndRemoteBindings(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		class   moduleapi.ExecutionClass
		failure moduleapi.FailurePolicy
		static  bool
	}{
		{"declarative", moduleapi.ExecutionDeclarative, moduleapi.FailureRequired, true},
		{"optional", moduleapi.ExecutionTrustedInProcess, moduleapi.FailureOptional, false},
		{"remote", moduleapi.ExecutionRemote, moduleapi.FailureRequired, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := pureChatRunFixture(t)
			run.Member.MemberID = "member-context-read-invalid"
			run.Member.MemberSnapshotDigest = strings.Repeat("8", 64)
			binding := run.Member.PortPlans[0].Bindings[0]
			binding.Provider.ExecutionClass = test.class
			binding.FailurePolicy = test.failure
			binding.StaticContextRefs = []string{}
			if test.static {
				binding.StaticContextRefs = []string{strings.Repeat("a", 64)}
			}
			run.Member.PortPlans = []moduleapi.PortPlan{{
				Port: contextProvidePortV1, Bindings: []moduleapi.PortBinding{binding},
			}}
			lease := currentstore.RunLease{
				RunID: run.RunID, OwnerID: "context-owner", LeaseEpoch: 1,
				RunRevision: run.RunRevision, FrameRevision: run.Frame.Revision,
				ExpiresAt: time.Now().UTC().Add(time.Minute),
			}
			run.Frame.Lease = lease
			_, _, err := ArmContextReadGate(
				run, lease, 0, "context-read-invalid", []byte(`{"q":1}`),
				lease.ExpiresAt.Add(-leaseTailGrace-time.Second).
					UTC().Truncate(time.Microsecond),
			)
			if !errors.Is(err, ErrInvalidContextReadGrant) {
				t.Fatalf("ArmContextReadGate error = %v", err)
			}
		})
	}
}

func validBeginResult(t *testing.T) currentstore.BeginModelDispatchResult {
	t.Helper()
	return newRealBeginResult(t)
}

func invocationFromResult(
	result currentstore.BeginModelDispatchResult,
) modulehost.ModuleInvocation {
	return modulehost.ModuleInvocation{
		InvocationID:         result.Attempt.AttemptID,
		RunID:                result.Attempt.RunID,
		MemberID:             result.Attempt.MemberID,
		MemberSnapshotDigest: result.Attempt.MemberSnapshotDigest,
		Port:                 modelGeneratePortV1,
		BindingIndex:         0,
		Input:                bytes.Clone(result.Attempt.Request.CanonicalBytes),
		Deadline:             result.Attempt.Deadline,
	}
}
