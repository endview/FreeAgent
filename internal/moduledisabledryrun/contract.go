// Package moduledisabledryrun evaluates one already-frozen MODULE_DISABLE
// plan against an exact published Control/Catalog basis.
//
// It deliberately does not parse, canonicalize, or hash module-apply-plan/v1.
// The caller owns that wire contract and supplies its already-frozen safe
// projection together with the existing plan digest. This package performs no
// Store mutation, filesystem access, recovery, provider call, or network I/O.
package moduledisabledryrun

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type StatusV1 string

const (
	StatusAlreadyAppliedV1 StatusV1 = "ALREADY_APPLIED"
	StatusNoChangeV1       StatusV1 = "NO_CHANGE"
	StatusWouldApplyV1     StatusV1 = "WOULD_APPLY"
)

type TargetKindV1 string

const (
	TargetProfileV1                  TargetKindV1 = "PROFILE"
	TargetWorkspaceChannelEndpointV1 TargetKindV1 = "WORKSPACE_CHANNEL_ENDPOINT"
)

type CatalogChangeV1 string

const (
	CatalogChangeNoneV1           CatalogChangeV1 = "NONE"
	CatalogChangeRetainInstanceV1 CatalogChangeV1 = "RETAIN_INSTANCE"
	CatalogChangeRemoveInstanceV1 CatalogChangeV1 = "REMOVE_INSTANCE"
)

type FailureCodeV1 string

const (
	FailureInvalidInputV1    FailureCodeV1 = "INVALID_INPUT"
	FailureArtifactInvalidV1 FailureCodeV1 = "ARTIFACT_INVALID"
	FailureStoreInvalidV1    FailureCodeV1 = "STORE_INVALID"
	FailurePointerConflictV1 FailureCodeV1 = "POINTER_CONFLICT"
	FailureTargetConflictV1  FailureCodeV1 = "TARGET_CONFLICT"
	FailureCancelledV1       FailureCodeV1 = "CANCELLED"
	FailureInternalV1        FailureCodeV1 = "INTERNAL_ERROR"
)

type FailureV1 struct {
	code  FailureCodeV1
	cause error
}

func (failure *FailureV1) Error() string {
	if failure == nil {
		return "module disable dry-run failed"
	}
	return "module disable dry-run failed: " + string(failure.code)
}

func (failure *FailureV1) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func (failure *FailureV1) Code() FailureCodeV1 {
	if failure == nil {
		return ""
	}
	return failure.code
}

func newFailureV1(code FailureCodeV1, cause error) error {
	if errors.Is(cause, context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) {
		code = FailureCancelledV1
	}
	return &FailureV1{code: code, cause: cause}
}

// ReadViewV1 is the complete immutable fact dependency of the evaluator.
// Both the live Current Store owner and the stopped-process observer implement
// it directly; the interface exposes no mutation or transaction capability.
type ReadViewV1 interface {
	LoadPublishedBasis(
		context.Context,
		string,
	) (
		controlcontract.PublishedBasis,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
	VerifyPublishedControlCatalogClosureV1(
		context.Context,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
	) error
	LoadControlCatalogRevision(
		context.Context,
		string,
		uint64,
		uint64,
	) (
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
}

// ArtifactVerifierV1 is supplied by the composition root because artifact
// location and handler-specific integrity belong outside this neutral package.
// excludedInstanceID is non-empty only when the exact target is the final
// Control reference and its Catalog artifact may be absent after Disable.
type ArtifactVerifierV1 func(
	context.Context,
	controlcontract.CatalogGeneration,
	string,
) error

// InputV1 is a safe projection of an already-restored module-apply-plan/v1.
// PlanDigest is consumed as an identity fact; this package never recalculates
// it. The caller also supplies the two candidate IDs derived by the existing
// Apply contract, avoiding a second definition of that naming policy.
type InputV1 struct {
	TenantID                     string
	ExpectedPointerRevision      uint64
	TargetKind                   TargetKindV1
	ProfileID                    string
	WorkspaceID                  string
	EndpointID                   string
	InstanceID                   string
	Port                         moduleapi.PortRef
	PlanDigest                   string
	CandidateControlSnapshotID   string
	CandidateCatalogGenerationID string
}

type BindingRemovalV1 struct {
	TargetKind          TargetKindV1
	ProfileID           string
	WorkspaceID         string
	EndpointID          string
	Port                moduleapi.PortRef
	PortBindingIndex    *uint32
	ConfigRef           string
	AuthorityCeilingRef string
	StaticContextRefs   []string
	FailurePolicy       *moduleapi.FailurePolicy
}

type PublicationV1 struct {
	ExpectedPointerRevision uint64
	NewPointerRevision      uint64
	ControlRef              controlcontract.ControlSnapshotRef
	ControlCanonical        []byte
	CatalogRef              controlcontract.CatalogGenerationRef
	CatalogCanonical        []byte
}

type ResultV1 struct {
	Status StatusV1
	// PreconditionBasis is the exact published basis against which the frozen
	// disable plan was evaluated. For an adjacent exact retry it is the
	// historical predecessor; ObservedBasis is the already-published result.
	PreconditionBasis  controlcontract.PublishedBasis
	ObservedBasis      controlcontract.PublishedBasis
	CandidateBasis     controlcontract.PublishedBasis
	BindingRemoval     BindingRemovalV1
	CatalogChange      CatalogChangeV1
	Publication        *PublicationV1
	ExcludedInstanceID string
}

func (input InputV1) validate() error {
	candidateIDs, candidateErr := moduleapplyplan.DeriveCandidateIDsV1(
		input.PlanDigest,
	)
	if strings.TrimSpace(input.TenantID) == "" ||
		strings.TrimSpace(input.InstanceID) == "" ||
		input.ExpectedPointerRevision == 0 ||
		input.ExpectedPointerRevision >= math.MaxInt64 ||
		candidateErr != nil ||
		input.CandidateControlSnapshotID != candidateIDs.ControlSnapshotID ||
		input.CandidateCatalogGenerationID != candidateIDs.CatalogGenerationID {
		return errors.New("module disable projection identity is invalid")
	}
	if err := input.Port.Validate(); err != nil {
		return fmt.Errorf("module disable projection port: %w", err)
	}
	switch input.TargetKind {
	case TargetProfileV1:
		if strings.TrimSpace(input.ProfileID) == "" ||
			input.WorkspaceID != "" || input.EndpointID != "" {
			return errors.New("PROFILE disable projection target is invalid")
		}
	case TargetWorkspaceChannelEndpointV1:
		if strings.TrimSpace(input.WorkspaceID) == "" ||
			strings.TrimSpace(input.EndpointID) == "" || input.ProfileID != "" {
			return errors.New("Workspace Channel disable projection target is invalid")
		}
		channelPort := moduleapi.PortRef{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		}
		if input.Port != channelPort {
			return errors.New("Workspace Channel disable projection has a non-Channel port")
		}
	default:
		return errors.New("module disable projection target kind is invalid")
	}
	return nil
}

func nilInterfaceV1(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func clonePublicationV1(publication PublicationV1) PublicationV1 {
	publication.ControlCanonical = append([]byte(nil), publication.ControlCanonical...)
	publication.CatalogCanonical = append([]byte(nil), publication.CatalogCanonical...)
	return publication
}

func cloneResultV1(result ResultV1) ResultV1 {
	result.BindingRemoval.StaticContextRefs = append(
		[]string(nil),
		result.BindingRemoval.StaticContextRefs...,
	)
	if result.BindingRemoval.PortBindingIndex != nil {
		index := *result.BindingRemoval.PortBindingIndex
		result.BindingRemoval.PortBindingIndex = &index
	}
	if result.BindingRemoval.FailurePolicy != nil {
		policy := *result.BindingRemoval.FailurePolicy
		result.BindingRemoval.FailurePolicy = &policy
	}
	if result.Publication != nil {
		publication := clonePublicationV1(*result.Publication)
		result.Publication = &publication
	}
	return result
}
