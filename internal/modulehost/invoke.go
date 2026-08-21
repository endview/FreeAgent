package modulehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maxInvocationInputDepth = 128
)

var ErrCurrentActivationDenied = errors.New(
	"modulehost: current activation denied",
)

// ModuleInvocation is the immutable identity and input of one call through a
// frozen member snapshot. The caller cannot supply a PortBinding directly.
type ModuleInvocation struct {
	InvocationID         string            `json:"invocation_id"`
	RunID                string            `json:"run_id"`
	MemberID             string            `json:"member_id"`
	MemberSnapshotDigest string            `json:"member_snapshot_digest"`
	Port                 moduleapi.PortRef `json:"port"`
	BindingIndex         uint32            `json:"binding_index"`
	Input                json.RawMessage   `json:"input"`
	Deadline             time.Time         `json:"deadline"`
}

// InvocationOutcome carries post-dispatch certainty explicitly. In
// particular, UNKNOWN must not be converted into an ordinary error and must
// never trigger a retry or another binding.
type InvocationOutcome string

const (
	InvocationSucceeded InvocationOutcome = "SUCCEEDED"
	InvocationFailed    InvocationOutcome = "FAILED"
	InvocationUnknown   InvocationOutcome = "UNKNOWN"
)

func (outcome InvocationOutcome) validate() error {
	switch outcome {
	case InvocationSucceeded, InvocationFailed, InvocationUnknown:
		return nil
	default:
		return fmt.Errorf("modulehost: unsupported invocation outcome %q", outcome)
	}
}

// InvocationResult reports the exact provider that ran. Output and
// UsageReceipt remain port/provider payloads; their normalization belongs to
// the owning Core consumer.
type InvocationResult struct {
	InvocationID string                       `json:"invocation_id"`
	Provider     moduleapi.ActivatedModuleRef `json:"provider"`
	Outcome      InvocationOutcome            `json:"outcome"`
	Output       json.RawMessage              `json:"output,omitempty"`
	UsageReceipt json.RawMessage              `json:"usage_receipt,omitempty"`
	UnknownClass InvocationUnknownClass       `json:"unknown_class,omitempty"`
}

// PreparedInvocation is produced only by the Core-owned InvocationGate. The
// gate must resolve the exact binding identified by snapshot digest, port and
// binding index and perform the policy checks and scoped injection required by
// that binding.
type PreparedInvocation struct {
	Invocation         ModuleInvocation      `json:"invocation"`
	Binding            moduleapi.PortBinding `json:"binding"`
	ConfigCanonical    json.RawMessage       `json:"config_canonical,omitempty"`
	AuthorityCanonical json.RawMessage       `json:"authority_canonical,omitempty"`
}

// InvocationGate is the sole authority-resolution dependency of
// InvocationHost. It deliberately exposes no provider search or enumeration
// operation.
type InvocationGate interface {
	ResolveAuthorized(context.Context, ModuleInvocation) (PreparedInvocation, error)
}

// ExactAdapterRegistry resolves one implementation only by the frozen
// ArtifactDigest and AdapterIdentity. Module ID, version and Port are
// deliberately absent so callers cannot perform a best-match lookup.
type ExactAdapterRegistry interface {
	ResolveExact(
		context.Context,
		string,
		string,
	) (ModuleInvoker, error)
}

// CurrentActivationChecker is a Core-owned, deny-only read of the current
// Control/Catalog pointer. It may reject a frozen Provider that is no longer
// active for the exact Port, but it cannot return another Provider, mutate the
// frozen Binding, enumerate adapters, or expose Store access to a module.
type CurrentActivationChecker interface {
	CheckCurrentActivation(
		context.Context,
		string,
		moduleapi.PortRef,
		moduleapi.ActivatedModuleRef,
	) error
}

// ModuleInvoker executes one already authorized, exact prepared invocation.
// Once dispatch may have begun, it must express certainty through
// InvocationResult.Outcome rather than asking InvocationHost to infer it from
// an error.
type ModuleInvoker interface {
	Invoke(context.Context, PreparedInvocation) (InvocationResult, error)
}

// InvocationHost invokes exactly the binding frozen in a member snapshot. It
// never selects a provider, changes a binding, retries an invocation or
// performs Install, Activate or Bind.
type InvocationHost struct {
	gate              InvocationGate
	registry          ExactAdapterRegistry
	currentActivation CurrentActivationChecker
}

func NewInvocationHost(
	gate InvocationGate,
	registry ExactAdapterRegistry,
) (*InvocationHost, error) {
	return newInvocationHost(gate, registry, nil)
}

// NewInvocationHostWithCurrentActivation constructs the production Host. The
// checker is called after the exact frozen Binding is recovered and before any
// registry lookup or Adapter call. NewInvocationHost remains available only
// for isolated protocol/adapter tests that have no Current Store.
func NewInvocationHostWithCurrentActivation(
	gate InvocationGate,
	registry ExactAdapterRegistry,
	currentActivation CurrentActivationChecker,
) (*InvocationHost, error) {
	if isNilInvocationDependency(currentActivation) {
		return nil, fmt.Errorf(
			"modulehost: current activation checker is nil",
		)
	}
	return newInvocationHost(gate, registry, currentActivation)
}

func newInvocationHost(
	gate InvocationGate,
	registry ExactAdapterRegistry,
	currentActivation CurrentActivationChecker,
) (*InvocationHost, error) {
	if isNilInvocationDependency(gate) {
		return nil, fmt.Errorf("modulehost: invocation gate is nil")
	}
	if isNilInvocationDependency(registry) {
		return nil, fmt.Errorf("modulehost: exact adapter registry is nil")
	}
	return &InvocationHost{
		gate:              gate,
		registry:          registry,
		currentActivation: currentActivation,
	}, nil
}

// Invoke validates the caller-supplied envelope, obtains exactly one frozen
// binding from the gate, resolves its exact adapter, and invokes it once.
func (host *InvocationHost) Invoke(
	ctx context.Context,
	invocation ModuleInvocation,
) (InvocationResult, error) {
	if host == nil {
		return InvocationResult{}, fmt.Errorf("modulehost: invocation host is nil")
	}
	if isNilInvocationDependency(host.gate) {
		return InvocationResult{}, fmt.Errorf("modulehost: invocation gate is nil")
	}
	if isNilInvocationDependency(host.registry) {
		return InvocationResult{}, fmt.Errorf("modulehost: exact adapter registry is nil")
	}
	if ctx == nil {
		return InvocationResult{}, fmt.Errorf("modulehost: invocation context is nil")
	}

	expected := cloneInvocation(invocation)
	if err := validateModuleInvocation(expected, time.Now().UTC()); err != nil {
		return InvocationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return InvocationResult{}, fmt.Errorf("modulehost: invocation context: %w", err)
	}

	callContext, cancel := context.WithDeadline(ctx, expected.Deadline)
	defer cancel()

	prepared, err := host.gate.ResolveAuthorized(
		callContext,
		cloneInvocation(expected),
	)
	if err != nil {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: resolve authorized binding: %w",
			err,
		)
	}
	if !sameInvocation(prepared.Invocation, expected) {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: invocation gate returned a binding for a different invocation",
		)
	}
	frozenPlan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     expected.Port,
		Bindings: []moduleapi.PortBinding{prepared.Binding},
	})
	if err != nil {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: invalid frozen port binding: %w",
			err,
		)
	}
	prepared.Binding = frozenPlan.Bindings[0]
	prepared.ConfigCanonical = bytes.Clone(prepared.ConfigCanonical)
	prepared.AuthorityCanonical = bytes.Clone(prepared.AuthorityCanonical)
	switch prepared.Binding.Provider.ExecutionClass {
	case moduleapi.ExecutionDeclarative,
		moduleapi.ExecutionTrustedInProcess:
		// These are the execution classes implemented by the current runtime.
	case moduleapi.ExecutionLocalProcess, moduleapi.ExecutionRemote,
		moduleapi.ExecutionWASM:
		return InvocationResult{}, fmt.Errorf(
			"modulehost: execution class %q is not supported by the current runtime",
			prepared.Binding.Provider.ExecutionClass,
		)
	default:
		return InvocationResult{}, fmt.Errorf(
			"modulehost: execution class %q is not supported by the current runtime",
			prepared.Binding.Provider.ExecutionClass,
		)
	}
	if host.currentActivation != nil {
		if err := host.currentActivation.CheckCurrentActivation(
			callContext,
			expected.RunID,
			expected.Port,
			prepared.Binding.Provider,
		); err != nil {
			return InvocationResult{}, fmt.Errorf(
				"%w: %w",
				ErrCurrentActivationDenied,
				err,
			)
		}
	}
	if err := callContext.Err(); err != nil {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: invocation expired before adapter resolution: %w",
			err,
		)
	}

	invoker, err := host.registry.ResolveExact(
		callContext,
		prepared.Binding.Provider.ArtifactDigest,
		prepared.Binding.Provider.AdapterIdentity,
	)
	if err != nil {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: resolve exact adapter: %w",
			err,
		)
	}
	if isNilInvocationDependency(invoker) {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: exact adapter registry returned a nil invoker",
		)
	}
	if err := callContext.Err(); err != nil {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: invocation expired before adapter dispatch: %w",
			err,
		)
	}

	prepared.Invocation = cloneInvocation(expected)
	result, err := invoker.Invoke(callContext, prepared)
	if err != nil {
		// InvocationHost deliberately makes no retry or fallback decision.
		return InvocationResult{}, fmt.Errorf("modulehost: invoke exact adapter: %w", err)
	}
	if err := result.Outcome.validate(); err != nil {
		return InvocationResult{}, err
	}
	if err := ValidateInvocationUnknownClass(
		result.Outcome,
		result.UnknownClass,
	); err != nil {
		return InvocationResult{}, err
	}
	if result.Provider != prepared.Binding.Provider {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: adapter result provider does not match frozen binding",
		)
	}
	if result.InvocationID != "" &&
		result.InvocationID != expected.InvocationID {
		return InvocationResult{}, fmt.Errorf(
			"modulehost: adapter result invocation identity does not match dispatch",
		)
	}

	result.InvocationID = expected.InvocationID
	result.Output = append(json.RawMessage(nil), result.Output...)
	result.UsageReceipt = append(json.RawMessage(nil), result.UsageReceipt...)
	return result, nil
}

func validateModuleInvocation(invocation ModuleInvocation, now time.Time) error {
	for _, value := range []struct {
		name string
		id   string
	}{
		{name: "invocation_id", id: invocation.InvocationID},
		{name: "run_id", id: invocation.RunID},
		{name: "member_id", id: invocation.MemberID},
	} {
		if err := validateInvocationID(value.name, value.id); err != nil {
			return err
		}
	}
	if !moduleapi.ValidSHA256(invocation.MemberSnapshotDigest) {
		return fmt.Errorf(
			"modulehost: member_snapshot_digest must be a lowercase SHA-256 digest",
		)
	}
	if err := invocation.Port.Validate(); err != nil {
		return fmt.Errorf("modulehost: invocation port: %w", err)
	}
	if len(invocation.Input) == 0 {
		return fmt.Errorf("modulehost: invocation input must be non-empty canonical JSON")
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		invocation.Input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxTextBytes,
			MaxDepth: maxInvocationInputDepth,
			MaxNodes: moduleapi.MaxTextBytes,
		},
	)
	if err != nil {
		return fmt.Errorf("modulehost: invocation input: %w", err)
	}
	if !bytes.Equal(canonical, invocation.Input) {
		return fmt.Errorf("modulehost: invocation input must use RFC 8785 canonical JSON")
	}
	if invocation.Deadline.IsZero() {
		return fmt.Errorf("modulehost: invocation deadline must be set")
	}
	if invocation.Deadline.Location() != time.UTC {
		return fmt.Errorf("modulehost: invocation deadline must use UTC")
	}
	if !invocation.Deadline.After(now) {
		return fmt.Errorf("modulehost: invocation deadline has expired")
	}
	return nil
}

func validateInvocationID(name, value string) error {
	if value == "" ||
		value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) {
		return fmt.Errorf(
			"modulehost: %s must be canonical UTF-8 containing between 1 and %d bytes",
			name,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	if value != moduleapi.CanonicalText(value) {
		return fmt.Errorf("modulehost: %s must use Unicode NFC", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf(
				"modulehost: %s contains an unsupported control character",
				name,
			)
		}
	}
	return nil
}

func sameInvocation(left, right ModuleInvocation) bool {
	return left.InvocationID == right.InvocationID &&
		left.RunID == right.RunID &&
		left.MemberID == right.MemberID &&
		left.MemberSnapshotDigest == right.MemberSnapshotDigest &&
		left.Port == right.Port &&
		left.BindingIndex == right.BindingIndex &&
		bytes.Equal(left.Input, right.Input) &&
		left.Deadline == right.Deadline
}

func cloneInvocation(invocation ModuleInvocation) ModuleInvocation {
	invocation.Input = append(json.RawMessage(nil), invocation.Input...)
	return invocation
}

func isNilInvocationDependency(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}
