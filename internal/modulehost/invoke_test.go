package modulehost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestInvocationHostInvokesExactFrozenBindingOnce(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	invoker := &recordingModuleInvoker{
		result: InvocationResult{
			Provider:     binding.Provider,
			Outcome:      InvocationSucceeded,
			Output:       json.RawMessage(`{"text":"ok"}`),
			UsageReceipt: json.RawMessage(`{"output_tokens":1}`),
		},
	}
	gate := &recordingInvocationGate{
		prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		},
	}
	registry := &recordingExactRegistry{invoker: invoker}
	host := mustInvocationHost(t, gate, registry)

	result, err := host.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.InvocationID != invocation.InvocationID ||
		result.Outcome != InvocationSucceeded ||
		result.Provider != binding.Provider {
		t.Fatalf("Invoke() result = %+v", result)
	}
	if gate.calls != 1 || registry.calls != 1 || invoker.calls != 1 {
		t.Fatalf(
			"calls gate/registry/invoker = %d/%d/%d, want 1/1/1",
			gate.calls,
			registry.calls,
			invoker.calls,
		)
	}
	if registry.artifactDigest != binding.Provider.ArtifactDigest ||
		registry.adapterIdentity != binding.Provider.AdapterIdentity {
		t.Fatalf(
			"ResolveExact() got digest/adapter %q/%q",
			registry.artifactDigest,
			registry.adapterIdentity,
		)
	}
	if !reflect.DeepEqual(invoker.prepared.Binding, binding) ||
		!sameInvocation(invoker.prepared.Invocation, invocation) {
		t.Fatalf("invoker received different prepared invocation")
	}
}

func TestInvocationHostChecksCurrentActivationBeforeRegistryAndAdapter(
	t *testing.T,
) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	invoker := &recordingModuleInvoker{
		result: InvocationResult{
			Provider: binding.Provider,
			Outcome:  InvocationSucceeded,
		},
	}
	gate := &recordingInvocationGate{prepared: PreparedInvocation{
		Invocation: invocation,
		Binding:    binding,
	}}
	registry := &recordingExactRegistry{invoker: invoker}
	current := &recordingCurrentActivationChecker{}
	host, err := NewInvocationHostWithCurrentActivation(
		gate,
		registry,
		current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Invoke(context.Background(), invocation); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if current.calls != 1 ||
		current.runID != invocation.RunID ||
		current.port != invocation.Port ||
		current.provider != binding.Provider ||
		registry.calls != 1 || invoker.calls != 1 {
		t.Fatalf(
			"current=%+v registry/invoker=%d/%d",
			current,
			registry.calls,
			invoker.calls,
		)
	}
}

func TestInvocationHostCurrentActivationDenialSkipsRegistryAndAdapter(
	t *testing.T,
) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	invoker := &recordingModuleInvoker{}
	gate := &recordingInvocationGate{prepared: PreparedInvocation{
		Invocation: invocation,
		Binding:    binding,
	}}
	registry := &recordingExactRegistry{invoker: invoker}
	current := &recordingCurrentActivationChecker{
		err: errors.New("revoked from current Catalog"),
	}
	host, err := NewInvocationHostWithCurrentActivation(
		gate,
		registry,
		current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Invoke(
		context.Background(),
		invocation,
	); !errors.Is(err, ErrCurrentActivationDenied) {
		t.Fatalf("Invoke() error = %v", err)
	}
	if gate.calls != 1 || current.calls != 1 ||
		registry.calls != 0 || invoker.calls != 0 {
		t.Fatalf(
			"gate/current/registry/invoker=%d/%d/%d/%d",
			gate.calls,
			current.calls,
			registry.calls,
			invoker.calls,
		)
	}
}

func TestInvocationHostRejectsAdapterResultForAnotherInvocation(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	invoker := &recordingModuleInvoker{
		result: InvocationResult{
			InvocationID: "another-invocation",
			Provider:     binding.Provider,
			Outcome:      InvocationSucceeded,
			Output:       json.RawMessage(`{"text":"wrong invocation"}`),
		},
	}
	host := mustInvocationHost(
		t,
		&recordingInvocationGate{prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		}},
		&recordingExactRegistry{invoker: invoker},
	)
	if _, err := host.Invoke(
		context.Background(),
		invocation,
	); err == nil {
		t.Fatal("adapter result for another invocation was accepted")
	}
	if invoker.calls != 1 {
		t.Fatalf("adapter calls=%d want 1", invoker.calls)
	}
}

func TestInvocationHostRejectsInvalidEnvelopeBeforeGate(t *testing.T) {
	valid := validModuleInvocation()
	nonUTC := valid.Deadline.In(time.FixedZone("zero-offset", 0))
	tests := []struct {
		name   string
		mutate func(*ModuleInvocation)
	}{
		{
			name: "empty invocation id",
			mutate: func(value *ModuleInvocation) {
				value.InvocationID = ""
			},
		},
		{
			name: "non canonical member id",
			mutate: func(value *ModuleInvocation) {
				value.MemberID = " member-a "
			},
		},
		{
			name: "bad snapshot digest",
			mutate: func(value *ModuleInvocation) {
				value.MemberSnapshotDigest = strings.Repeat("A", 64)
			},
		},
		{
			name: "bad port",
			mutate: func(value *ModuleInvocation) {
				value.Port.Name = "Model.Generate"
			},
		},
		{
			name: "empty input",
			mutate: func(value *ModuleInvocation) {
				value.Input = nil
			},
		},
		{
			name: "malformed input",
			mutate: func(value *ModuleInvocation) {
				value.Input = json.RawMessage(`{"prompt":`)
			},
		},
		{
			name: "non canonical input",
			mutate: func(value *ModuleInvocation) {
				value.Input = json.RawMessage(`{"z":1,"a":2}`)
			},
		},
		{
			name: "zero deadline",
			mutate: func(value *ModuleInvocation) {
				value.Deadline = time.Time{}
			},
		},
		{
			name: "non UTC deadline",
			mutate: func(value *ModuleInvocation) {
				value.Deadline = nonUTC
			},
		},
		{
			name: "expired deadline",
			mutate: func(value *ModuleInvocation) {
				value.Deadline = time.Now().Add(-time.Minute).UTC()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := cloneInvocation(valid)
			test.mutate(&invocation)
			gate := &recordingInvocationGate{}
			registry := &recordingExactRegistry{}
			host := mustInvocationHost(t, gate, registry)

			if _, err := host.Invoke(context.Background(), invocation); err == nil {
				t.Fatalf("Invoke() error = nil")
			}
			if gate.calls != 0 || registry.calls != 0 {
				t.Fatalf(
					"invalid envelope reached gate/registry: %d/%d",
					gate.calls,
					registry.calls,
				)
			}
		})
	}
}

func TestInvocationHostRejectsGateSubstitution(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionDeclarative)
	tests := []struct {
		name   string
		mutate func(*ModuleInvocation)
	}{
		{
			name: "snapshot",
			mutate: func(value *ModuleInvocation) {
				value.MemberSnapshotDigest = strings.Repeat("f", 64)
			},
		},
		{
			name: "port",
			mutate: func(value *ModuleInvocation) {
				value.Port = moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				}
			},
		},
		{
			name: "binding index",
			mutate: func(value *ModuleInvocation) {
				value.BindingIndex++
			},
		},
		{
			name: "input",
			mutate: func(value *ModuleInvocation) {
				value.Input = json.RawMessage(`{"prompt":"different"}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			substituted := cloneInvocation(invocation)
			test.mutate(&substituted)
			gate := &recordingInvocationGate{
				prepared: PreparedInvocation{
					Invocation: substituted,
					Binding:    binding,
				},
			}
			registry := &recordingExactRegistry{}
			host := mustInvocationHost(t, gate, registry)

			if _, err := host.Invoke(context.Background(), invocation); err == nil {
				t.Fatalf("Invoke() error = nil")
			}
			if gate.calls != 1 || registry.calls != 0 {
				t.Fatalf(
					"calls gate/registry = %d/%d, want 1/0",
					gate.calls,
					registry.calls,
				)
			}
		})
	}
}

func TestInvocationHostRejectsUnsupportedExecutionClassesWithoutDowngrade(t *testing.T) {
	for _, class := range []moduleapi.ExecutionClass{
		moduleapi.ExecutionLocalProcess,
		moduleapi.ExecutionRemote,
		moduleapi.ExecutionWASM,
		"SANDBOX",
	} {
		t.Run(string(class), func(t *testing.T) {
			invocation := validModuleInvocation()
			binding := validInvocationBinding(class)
			gate := &recordingInvocationGate{
				prepared: PreparedInvocation{
					Invocation: invocation,
					Binding:    binding,
				},
			}
			registry := &recordingExactRegistry{}
			host := mustInvocationHost(t, gate, registry)

			if _, err := host.Invoke(context.Background(), invocation); err == nil {
				t.Fatalf("Invoke() error = nil")
			}
			if registry.calls != 0 {
				t.Fatalf("unsupported class reached registry %d times", registry.calls)
			}
		})
	}
}

func TestInvocationHostRejectsInvalidBindingBeforeRegistry(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	binding.Provider.ArtifactDigest = strings.Repeat("A", 64)
	gate := &recordingInvocationGate{
		prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		},
	}
	registry := &recordingExactRegistry{}
	host := mustInvocationHost(t, gate, registry)

	if _, err := host.Invoke(context.Background(), invocation); err == nil {
		t.Fatalf("Invoke() error = nil")
	}
	if registry.calls != 0 {
		t.Fatalf("invalid binding reached registry %d times", registry.calls)
	}
}

func TestInvocationHostRejectsProviderSubstitution(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	substitute := binding.Provider
	substitute.InstanceID = "other-instance"
	invoker := &recordingModuleInvoker{
		result: InvocationResult{
			Provider: substitute,
			Outcome:  InvocationSucceeded,
		},
	}
	gate := &recordingInvocationGate{
		prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		},
	}
	registry := &recordingExactRegistry{invoker: invoker}
	host := mustInvocationHost(t, gate, registry)

	if _, err := host.Invoke(context.Background(), invocation); err == nil {
		t.Fatalf("Invoke() error = nil")
	}
	if invoker.calls != 1 {
		t.Fatalf("invoker calls = %d, want 1", invoker.calls)
	}
}

func TestInvocationHostReturnsUnknownWithoutRetryOrBindingSwitch(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	invoker := &recordingModuleInvoker{
		result: InvocationResult{
			Provider: binding.Provider,
			Outcome:  InvocationUnknown,
		},
	}
	gate := &recordingInvocationGate{
		prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		},
	}
	registry := &recordingExactRegistry{invoker: invoker}
	host := mustInvocationHost(t, gate, registry)

	result, err := host.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.InvocationID != invocation.InvocationID ||
		result.Outcome != InvocationUnknown {
		t.Fatalf("outcome = %q, want UNKNOWN", result.Outcome)
	}
	if gate.calls != 1 || registry.calls != 1 || invoker.calls != 1 {
		t.Fatalf(
			"UNKNOWN calls gate/registry/invoker = %d/%d/%d, want 1/1/1",
			gate.calls,
			registry.calls,
			invoker.calls,
		)
	}
}

func TestInvocationHostRejectsInvalidUnknownClassContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		result InvocationResult
	}{
		{
			name: "free form UNKNOWN class",
			result: InvocationResult{
				Outcome:      InvocationUnknown,
				UnknownClass: "private transport failure",
			},
		},
		{
			name: "certain outcome with UNKNOWN class",
			result: InvocationResult{
				Outcome:      InvocationSucceeded,
				UnknownClass: UnknownClassInvokeReturnedError,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation := validModuleInvocation()
			binding := validInvocationBinding(
				moduleapi.ExecutionTrustedInProcess,
			)
			test.result.Provider = binding.Provider
			invoker := &recordingModuleInvoker{result: test.result}
			host := mustInvocationHost(
				t,
				&recordingInvocationGate{prepared: PreparedInvocation{
					Invocation: invocation,
					Binding:    binding,
				}},
				&recordingExactRegistry{invoker: invoker},
			)
			if _, err := host.Invoke(
				context.Background(),
				invocation,
			); err == nil {
				t.Fatal("invalid UNKNOWN class contract was accepted")
			}
			if invoker.calls != 1 {
				t.Fatalf("invoker calls=%d want 1", invoker.calls)
			}
		})
	}
}

func TestInvocationHostDoesNotRetryDependencyOrInvokerErrors(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	dependencyError := errors.New("definite test failure")
	tests := []struct {
		name     string
		gate     *recordingInvocationGate
		registry *recordingExactRegistry
	}{
		{
			name: "gate",
			gate: &recordingInvocationGate{err: dependencyError},
			registry: &recordingExactRegistry{
				invoker: &recordingModuleInvoker{},
			},
		},
		{
			name: "registry",
			gate: &recordingInvocationGate{
				prepared: PreparedInvocation{
					Invocation: invocation,
					Binding:    binding,
				},
			},
			registry: &recordingExactRegistry{err: dependencyError},
		},
		{
			name: "invoker",
			gate: &recordingInvocationGate{
				prepared: PreparedInvocation{
					Invocation: invocation,
					Binding:    binding,
				},
			},
			registry: &recordingExactRegistry{
				invoker: &recordingModuleInvoker{err: dependencyError},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := mustInvocationHost(t, test.gate, test.registry)
			if _, err := host.Invoke(context.Background(), invocation); err == nil {
				t.Fatalf("Invoke() error = nil")
			}
			if test.gate.calls > 1 || test.registry.calls > 1 {
				t.Fatalf(
					"dependency retried: gate/registry = %d/%d",
					test.gate.calls,
					test.registry.calls,
				)
			}
			if test.registry.invoker != nil {
				if local, ok := test.registry.invoker.(*recordingModuleInvoker); ok &&
					local.calls > 1 {
					t.Fatalf("invoker retried %d times", local.calls)
				}
			}
		})
	}
}

func TestInvocationHostRejectsCanceledContextBeforeGate(t *testing.T) {
	invocation := validModuleInvocation()
	gate := &recordingInvocationGate{}
	registry := &recordingExactRegistry{}
	host := mustInvocationHost(t, gate, registry)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := host.Invoke(ctx, invocation); err == nil {
		t.Fatalf("Invoke() error = nil")
	}
	if gate.calls != 0 || registry.calls != 0 {
		t.Fatalf("canceled call reached gate/registry")
	}
}

func TestNewInvocationHostRejectsNilDependencies(t *testing.T) {
	gate := &recordingInvocationGate{}
	registry := &recordingExactRegistry{}
	if _, err := NewInvocationHost(nil, registry); err == nil {
		t.Fatalf("nil gate accepted")
	}
	if _, err := NewInvocationHost(gate, nil); err == nil {
		t.Fatalf("nil registry accepted")
	}
	var typedNilGate *recordingInvocationGate
	if _, err := NewInvocationHost(typedNilGate, registry); err == nil {
		t.Fatalf("typed nil gate accepted")
	}
	var typedNilRegistry *recordingExactRegistry
	if _, err := NewInvocationHost(gate, typedNilRegistry); err == nil {
		t.Fatalf("typed nil registry accepted")
	}
	var typedNilCurrent *recordingCurrentActivationChecker
	if _, err := NewInvocationHostWithCurrentActivation(
		gate,
		registry,
		typedNilCurrent,
	); err == nil {
		t.Fatalf("typed nil current activation checker accepted")
	}
}

func TestInvocationHostRejectsTypedNilInvoker(t *testing.T) {
	invocation := validModuleInvocation()
	binding := validInvocationBinding(moduleapi.ExecutionTrustedInProcess)
	gate := &recordingInvocationGate{
		prepared: PreparedInvocation{
			Invocation: invocation,
			Binding:    binding,
		},
	}
	var typedNilInvoker *recordingModuleInvoker
	registry := &recordingExactRegistry{invoker: typedNilInvoker}
	host := mustInvocationHost(t, gate, registry)

	if _, err := host.Invoke(context.Background(), invocation); err == nil {
		t.Fatalf("typed nil invoker accepted")
	}
}

type recordingInvocationGate struct {
	calls    int
	prepared PreparedInvocation
	err      error
}

func (gate *recordingInvocationGate) ResolveAuthorized(
	_ context.Context,
	invocation ModuleInvocation,
) (PreparedInvocation, error) {
	gate.calls++
	if gate.err != nil {
		return PreparedInvocation{}, gate.err
	}
	prepared := gate.prepared
	if prepared.Invocation.InvocationID == "" {
		prepared.Invocation = cloneInvocation(invocation)
	}
	return prepared, nil
}

type recordingExactRegistry struct {
	calls           int
	artifactDigest  string
	adapterIdentity string
	invoker         ModuleInvoker
	err             error
}

type recordingCurrentActivationChecker struct {
	calls    int
	runID    string
	port     moduleapi.PortRef
	provider moduleapi.ActivatedModuleRef
	err      error
}

func (checker *recordingCurrentActivationChecker) CheckCurrentActivation(
	_ context.Context,
	runID string,
	port moduleapi.PortRef,
	provider moduleapi.ActivatedModuleRef,
) error {
	checker.calls++
	checker.runID = runID
	checker.port = port
	checker.provider = provider
	return checker.err
}

func (registry *recordingExactRegistry) ResolveExact(
	_ context.Context,
	artifactDigest string,
	adapterIdentity string,
) (ModuleInvoker, error) {
	registry.calls++
	registry.artifactDigest = artifactDigest
	registry.adapterIdentity = adapterIdentity
	if registry.err != nil {
		return nil, registry.err
	}
	return registry.invoker, nil
}

type recordingModuleInvoker struct {
	calls    int
	prepared PreparedInvocation
	result   InvocationResult
	err      error
}

func (invoker *recordingModuleInvoker) Invoke(
	_ context.Context,
	prepared PreparedInvocation,
) (InvocationResult, error) {
	invoker.calls++
	invoker.prepared = prepared
	return invoker.result, invoker.err
}

func mustInvocationHost(
	t *testing.T,
	gate InvocationGate,
	registry ExactAdapterRegistry,
) *InvocationHost {
	t.Helper()
	host, err := NewInvocationHost(gate, registry)
	if err != nil {
		t.Fatalf("NewInvocationHost() error = %v", err)
	}
	return host
}

func validModuleInvocation() ModuleInvocation {
	return ModuleInvocation{
		InvocationID:         "invocation-a",
		RunID:                "run-a",
		MemberID:             "member-a",
		MemberSnapshotDigest: strings.Repeat("a", 64),
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV2,
		},
		BindingIndex: 0,
		Input:        json.RawMessage(`{"prompt":"hello"}`),
		Deadline:     time.Now().Add(time.Hour).Round(0).UTC(),
	}
}

func validInvocationBinding(
	executionClass moduleapi.ExecutionClass,
) moduleapi.PortBinding {
	return moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           "model.provider",
			Version:            "v1",
			ArtifactDigest:     strings.Repeat("b", 64),
			InstanceID:         "model-instance",
			ExecutionClass:     executionClass,
			AdapterIdentity:    "builtin.model-provider",
			ActivationRevision: 1,
		},
		ConfigRef:           strings.Repeat("c", 64),
		AuthorityCeilingRef: strings.Repeat("d", 64),
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
}
