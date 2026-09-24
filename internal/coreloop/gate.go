// Package coreloop owns the narrow in-memory authority needed to advance one
// already-persisted Universal Loop step.
package coreloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidInvocationGrant means the value presented to ArmInvocationGate
	// is not the unique, newly-created pre-network grant returned by
	// BeginModelDispatch.
	ErrInvalidInvocationGrant = errors.New(
		"coreloop: invalid model invocation grant",
	)

	// ErrInvocationNotAuthorized is a pre-dispatch rejection. It is returned
	// for a different envelope and after the sole grant has been consumed.
	ErrInvocationNotAuthorized = errors.New(
		"coreloop: module invocation is not authorized",
	)

	// ErrInvalidContextReadGrant identifies a malformed or non-local dynamic
	// context invocation. Context reads are deliberately not model permits:
	// this grant is available only for the deterministic, read-only,
	// TRUSTED_IN_PROCESS RAG slice before BeginModelDispatch.
	ErrInvalidContextReadGrant = errors.New(
		"coreloop: invalid context read grant",
	)
)

var modelGeneratePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameModelGenerate,
	ExactVersion: moduleapi.PortVersionV2,
}

var contextProvidePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameContextProvide,
	ExactVersion: moduleapi.PortVersionV1,
}

type invocationGrant struct {
	expected           modulehost.ModuleInvocation
	binding            moduleapi.PortBinding
	configCanonical    []byte
	authorityCanonical []byte
}

// InvocationGate is a one-shot, process-local capability. It carries no
// provider search, fallback, persistence or model execution behavior.
//
// A gate can only be armed through ArmInvocationGate from the transaction
// result that first created a PENDING model Attempt. Its zero value is closed.
type InvocationGate struct {
	mu    sync.Mutex
	grant *invocationGrant
}

var _ modulehost.InvocationGate = (*InvocationGate)(nil)

// ArmInvocationGate converts the sole pre-network grant emitted by
// BeginModelDispatch into a one-use InvocationGate. Exact retries have
// InvokeAllowed=false and therefore cannot recreate a network permission.
func ArmInvocationGate(
	result currentstore.BeginModelDispatchResult,
) (*InvocationGate, error) {
	if !result.Created || !result.InvokeAllowed {
		return nil, fmt.Errorf(
			"%w: BeginModelDispatch did not create the PENDING Attempt",
			ErrInvalidInvocationGrant,
		)
	}
	attempt := result.Attempt
	if attempt.State != corecontract.ModelAttemptPending {
		return nil, fmt.Errorf(
			"%w: Attempt state is %q, not PENDING",
			ErrInvalidInvocationGrant,
			attempt.State,
		)
	}
	if !validGateID(attempt.AttemptID) ||
		!validGateID(attempt.RunID) ||
		!validGateID(attempt.MemberID) ||
		!moduleapi.ValidSHA256(attempt.MemberSnapshotDigest) {
		return nil, fmt.Errorf(
			"%w: Attempt recovery identity is invalid",
			ErrInvalidInvocationGrant,
		)
	}
	now := time.Now().UTC()
	if result.Lease.RunID != attempt.RunID ||
		!validGateID(result.Lease.OwnerID) ||
		result.Lease.LeaseEpoch == 0 ||
		result.Lease.ExpiresAt.IsZero() ||
		!result.Lease.ExpiresAt.After(now) {
		return nil, fmt.Errorf(
			"%w: Run lease does not close the Attempt",
			ErrInvalidInvocationGrant,
		)
	}
	if attempt.Request.Kind != currentstore.ContentModelRequest ||
		attempt.Request.Digest == "" ||
		!moduleapi.ValidSHA256(attempt.Request.Digest) ||
		len(attempt.Request.CanonicalBytes) == 0 {
		return nil, fmt.Errorf(
			"%w: model request content is invalid",
			ErrInvalidInvocationGrant,
		)
	}
	if _, err := moduleapi.RestoreModelGenerateRequestV1(
		attempt.Request.CanonicalBytes,
	); err != nil {
		return nil, fmt.Errorf(
			"%w: model request: %v",
			ErrInvalidInvocationGrant,
			err,
		)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		attempt.Request.MediaType,
		attempt.Request.CanonicalBytes,
	)
	if err != nil || digest != attempt.Request.Digest {
		return nil, fmt.Errorf(
			"%w: model request digest does not close its bytes",
			ErrInvalidInvocationGrant,
		)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     modelGeneratePortV1,
		Bindings: []moduleapi.PortBinding{attempt.Binding},
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: frozen model binding: %v",
			ErrInvalidInvocationGrant,
			err,
		)
	}
	if !bindingCanonicalMatches(attempt.BindingCanonical, plan.Bindings[0]) {
		return nil, fmt.Errorf(
			"%w: model binding canonical bytes do not close the binding",
			ErrInvalidInvocationGrant,
		)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV2(
		attempt.ModelConfigCanonical,
	)
	modelConfigDigest, digestErr := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		"application/json",
		attempt.ModelConfigCanonical,
	)
	if err != nil ||
		digestErr != nil ||
		modelConfigDigest != plan.Bindings[0].ConfigRef ||
		modelConfig.Provider != attempt.Provider ||
		modelConfig.Model != attempt.Model {
		return nil, fmt.Errorf(
			"%w: model Binding config does not close the Attempt",
			ErrInvalidInvocationGrant,
		)
	}
	authorityDigest, authorityDigestErr := currentstore.ComputeContentDigest(
		currentstore.ContentAuthorityCeiling,
		"application/json",
		attempt.ModelAuthorityCanonical,
	)
	if authorityDigestErr != nil ||
		authorityDigest != plan.Bindings[0].AuthorityCeilingRef {
		return nil, fmt.Errorf(
			"%w: model Binding authority does not close the Attempt",
			ErrInvalidInvocationGrant,
		)
	}
	if attempt.Deadline.IsZero() ||
		attempt.Deadline.Location() != time.UTC ||
		attempt.Deadline.Nanosecond()%int(time.Microsecond) != 0 ||
		!attempt.Deadline.After(now) ||
		attempt.Deadline.After(
			result.Lease.ExpiresAt.Add(-leaseTailGrace),
		) {
		return nil, fmt.Errorf(
			"%w: Attempt deadline is invalid or expired",
			ErrInvalidInvocationGrant,
		)
	}
	if !result.ConsumeModelInvocationPermit() {
		return nil, fmt.Errorf(
			"%w: process-local invocation permit is absent, changed, or consumed",
			ErrInvalidInvocationGrant,
		)
	}

	expected := modulehost.ModuleInvocation{
		InvocationID:         attempt.AttemptID,
		RunID:                attempt.RunID,
		MemberID:             attempt.MemberID,
		MemberSnapshotDigest: attempt.MemberSnapshotDigest,
		Port:                 modelGeneratePortV1,
		BindingIndex:         0,
		Input:                bytes.Clone(attempt.Request.CanonicalBytes),
		Deadline:             attempt.Deadline,
	}
	return &InvocationGate{grant: &invocationGrant{
		expected:           expected,
		binding:            cloneBinding(plan.Bindings[0]),
		configCanonical:    bytes.Clone(attempt.ModelConfigCanonical),
		authorityCanonical: bytes.Clone(attempt.ModelAuthorityCanonical),
	}}, nil
}

// ArmContextReadGate constructs one exact, one-shot local read capability
// from an already recovered Run closure and its current lease. It never
// persists a permit and therefore may only authorize the first RAG slice's
// deterministic, unpriced, side-effect-free TRUSTED_IN_PROCESS provider.
// The returned invocation is the only envelope the gate will accept.
func ArmContextReadGate(
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	bindingIndex uint32,
	invocationID string,
	input []byte,
	deadline time.Time,
) (*InvocationGate, modulehost.ModuleInvocation, error) {
	now := time.Now().UTC()
	if run.RunID == "" ||
		run.RunID != lease.RunID ||
		run.Frame.Lease != lease ||
		run.Member.MemberID == "" ||
		!moduleapi.ValidSHA256(run.Member.MemberSnapshotDigest) ||
		!validGateID(invocationID) ||
		lease.OwnerID == "" ||
		lease.LeaseEpoch == 0 ||
		lease.ExpiresAt.IsZero() ||
		!lease.ExpiresAt.After(now) {
		return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
			"%w: recovered Run and lease do not close",
			ErrInvalidContextReadGrant,
		)
	}
	if deadline.IsZero() ||
		deadline.Location() != time.UTC ||
		deadline.Nanosecond()%int(time.Microsecond) != 0 ||
		!deadline.After(now) ||
		deadline.After(lease.ExpiresAt.Add(-leaseTailGrace)) {
		return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
			"%w: deadline is invalid, expired, or outside the lease",
			ErrInvalidContextReadGrant,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxTextBytes,
			MaxDepth: 128,
			MaxNodes: moduleapi.MaxTextBytes,
		},
	)
	if err != nil || !bytes.Equal(canonical, input) {
		return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
			"%w: input must be exact canonical JSON",
			ErrInvalidContextReadGrant,
		)
	}

	var selected *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		plan := run.Member.PortPlans[index]
		if plan.Port != contextProvidePortV1 {
			continue
		}
		if selected != nil {
			return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
				"%w: duplicate context.provide/v1 PortPlan",
				ErrInvalidContextReadGrant,
			)
		}
		frozen, freezeErr := moduleapi.NewPortPlan(plan)
		if freezeErr != nil {
			return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
				"%w: frozen context plan: %v",
				ErrInvalidContextReadGrant,
				freezeErr,
			)
		}
		selected = &frozen
	}
	if selected == nil || uint64(bindingIndex) >= uint64(len(selected.Bindings)) {
		return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
			"%w: exact context Binding is absent",
			ErrInvalidContextReadGrant,
		)
	}
	binding := selected.Bindings[bindingIndex]
	if binding.Provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		len(binding.StaticContextRefs) != 0 ||
		binding.FailurePolicy != moduleapi.FailureRequired {
		return nil, modulehost.ModuleInvocation{}, fmt.Errorf(
			"%w: dynamic context Binding is not REQUIRED TRUSTED_IN_PROCESS read-only shape",
			ErrInvalidContextReadGrant,
		)
	}
	invocation := modulehost.ModuleInvocation{
		InvocationID:         invocationID,
		RunID:                run.RunID,
		MemberID:             run.Member.MemberID,
		MemberSnapshotDigest: run.Member.MemberSnapshotDigest,
		Port:                 contextProvidePortV1,
		BindingIndex:         bindingIndex,
		Input:                bytes.Clone(canonical),
		Deadline:             deadline,
	}
	return &InvocationGate{grant: &invocationGrant{
		expected: cloneInvocation(invocation),
		binding:  cloneBinding(binding),
	}}, cloneInvocation(invocation), nil
}

// ResolveAuthorized accepts only the exact invocation frozen at arm time and
// atomically consumes its permission before returning. A mismatch leaves the
// permission untouched; a successful call makes all later calls fail.
func (gate *InvocationGate) ResolveAuthorized(
	ctx context.Context,
	invocation modulehost.ModuleInvocation,
) (modulehost.PreparedInvocation, error) {
	if gate == nil {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: gate is nil",
			ErrInvocationNotAuthorized,
		)
	}
	if ctx == nil {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvocationNotAuthorized,
		)
	}
	if err := ctx.Err(); err != nil {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: context: %v",
			ErrInvocationNotAuthorized,
			err,
		)
	}

	incoming := cloneInvocation(invocation)
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.grant == nil {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: one-shot grant is closed",
			ErrInvocationNotAuthorized,
		)
	}
	if err := ctx.Err(); err != nil {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: context: %v",
			ErrInvocationNotAuthorized,
			err,
		)
	}
	if !time.Now().UTC().Before(gate.grant.expected.Deadline) {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: one-shot grant has expired",
			ErrInvocationNotAuthorized,
		)
	}
	if !sameInvocation(incoming, gate.grant.expected) {
		return modulehost.PreparedInvocation{}, fmt.Errorf(
			"%w: invocation does not match the frozen Attempt",
			ErrInvocationNotAuthorized,
		)
	}

	prepared := modulehost.PreparedInvocation{
		Invocation:         cloneInvocation(gate.grant.expected),
		Binding:            cloneBinding(gate.grant.binding),
		ConfigCanonical:    bytes.Clone(gate.grant.configCanonical),
		AuthorityCanonical: bytes.Clone(gate.grant.authorityCanonical),
	}
	gate.grant = nil
	return prepared, nil
}

func bindingCanonicalMatches(
	canonical []byte,
	binding moduleapi.PortBinding,
) bool {
	if len(canonical) == 0 {
		return false
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return false
	}
	rebuilt, err := moduleapi.CanonicalJSON(encoded)
	return err == nil && bytes.Equal(canonical, rebuilt)
}

func validGateID(value string) bool {
	if value == "" ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		value != strings.TrimSpace(value) ||
		!utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func sameInvocation(
	left modulehost.ModuleInvocation,
	right modulehost.ModuleInvocation,
) bool {
	return left.InvocationID == right.InvocationID &&
		left.RunID == right.RunID &&
		left.MemberID == right.MemberID &&
		left.MemberSnapshotDigest == right.MemberSnapshotDigest &&
		left.Port == right.Port &&
		left.BindingIndex == right.BindingIndex &&
		bytes.Equal(left.Input, right.Input) &&
		left.Deadline == right.Deadline
}

func cloneInvocation(
	invocation modulehost.ModuleInvocation,
) modulehost.ModuleInvocation {
	invocation.Input = bytes.Clone(invocation.Input)
	return invocation
}

func cloneBinding(binding moduleapi.PortBinding) moduleapi.PortBinding {
	binding.StaticContextRefs = append(
		[]string{},
		binding.StaticContextRefs...,
	)
	return binding
}
