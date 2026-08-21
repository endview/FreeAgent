package controlapicontract

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type ControlOperationV1 string

const (
	OperationModuleApplyV1   ControlOperationV1 = "MODULE_APPLY"
	OperationModuleDisableV1 ControlOperationV1 = "MODULE_DISABLE"
	// OperationModuleUpgradeReviewV1 creates or restores the inert U3
	// Candidate/Review fact against one exact PublishedBasis. It never records
	// the separate Operator APPROVE/REJECT decision and grants no Apply right.
	OperationModuleUpgradeReviewV1    ControlOperationV1 = "MODULE_UPGRADE_REVIEW"
	OperationModuleUpgradeApplyV1     ControlOperationV1 = "MODULE_UPGRADE_APPLY"
	OperationLearningProposalReviewV1 ControlOperationV1 = "LEARNING_PROPOSAL_REVIEW"
	OperationLearningCycleRunV1       ControlOperationV1 = "LEARNING_CYCLE_RUN"
)

// ControlOperationIntentV1 distinguishes an effect-free evaluation from a
// request that may commit a governed mutation. Intent is part of the semantic
// request and receipt; transport correlation IDs and arrival times are not.
type ControlOperationIntentV1 string

const (
	OperationIntentDryRunV1 ControlOperationIntentV1 = "DRY_RUN"
	OperationIntentMutateV1 ControlOperationIntentV1 = "MUTATE"
)

func (intent ControlOperationIntentV1) validate() error {
	switch intent {
	case OperationIntentDryRunV1, OperationIntentMutateV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported operation intent %q",
			intent,
		)
	}
}

func validateIntentDigestsV1(
	intent ControlOperationIntentV1,
	idempotencyKeyDigest string,
	operationEvaluationDigest string,
	confirmationDigest string,
) error {
	if err := intent.validate(); err != nil {
		return err
	}
	switch intent {
	case OperationIntentDryRunV1:
		if idempotencyKeyDigest != "" || operationEvaluationDigest != "" ||
			confirmationDigest != "" {
			return fmt.Errorf(
				"controlapicontract: DRY_RUN intent cannot carry mutation digests",
			)
		}
	case OperationIntentMutateV1:
		if !moduleapi.ValidSHA256(idempotencyKeyDigest) ||
			!moduleapi.ValidSHA256(operationEvaluationDigest) ||
			!moduleapi.ValidSHA256(confirmationDigest) {
			return fmt.Errorf(
				"controlapicontract: MUTATE intent requires valid idempotency, evaluation, and confirmation digests",
			)
		}
	}
	return nil
}

func validateReceiptIntentKeyV1(
	intent ControlOperationIntentV1,
	idempotencyKeyDigest string,
) error {
	if err := intent.validate(); err != nil {
		return err
	}
	switch intent {
	case OperationIntentDryRunV1:
		if idempotencyKeyDigest != "" {
			return fmt.Errorf(
				"controlapicontract: DRY_RUN receipt cannot carry an idempotency digest",
			)
		}
	case OperationIntentMutateV1:
		if !moduleapi.ValidSHA256(idempotencyKeyDigest) {
			return fmt.Errorf(
				"controlapicontract: MUTATE receipt requires a valid idempotency digest",
			)
		}
	}
	return nil
}

func (operation ControlOperationV1) requiredCapability() (
	ControlCapabilityV1,
	error,
) {
	switch operation {
	case OperationModuleApplyV1, OperationModuleDisableV1,
		OperationModuleUpgradeReviewV1, OperationModuleUpgradeApplyV1:
		return CapabilityOperateModulesV1, nil
	case OperationLearningProposalReviewV1:
		return CapabilityReviewLearningV1, nil
	case OperationLearningCycleRunV1:
		return CapabilityRunLearningV1, nil
	default:
		return "", fmt.Errorf(
			"controlapicontract: unsupported operation %q",
			operation,
		)
	}
}

type ControlResourceKindV1 string

const (
	ResourcePublishedPointerV1   ControlResourceKindV1 = "PUBLISHED_POINTER"
	ResourceModuleInstallationV1 ControlResourceKindV1 = "MODULE_INSTALLATION"
	ResourceModuleActivationV1   ControlResourceKindV1 = "MODULE_ACTIVATION"
	ResourceLearningProposalV1   ControlResourceKindV1 = "LEARNING_PROPOSAL"
	ResourceLearningScheduleV1   ControlResourceKindV1 = "LEARNING_SCHEDULE"
)

func (kind ControlResourceKindV1) validate() error {
	switch kind {
	case ResourcePublishedPointerV1, ResourceModuleInstallationV1,
		ResourceModuleActivationV1, ResourceLearningProposalV1,
		ResourceLearningScheduleV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported resource kind %q",
			kind,
		)
	}
}

// ExpectedResourceRefV1 is the application-level If-Match precondition. HTTP
// adapters may quote Digest as an ETag, but the wire itself is transport-free.
// A mismatch is a conflict; it never authorizes automatic rebase.
type ExpectedResourceRefV1 struct {
	Kind       ControlResourceKindV1 `json:"kind"`
	ResourceID string                `json:"resource_id"`
	Revision   uint64                `json:"revision"`
	Digest     string                `json:"digest"`
}

func (ref ExpectedResourceRefV1) Validate() error {
	if err := ref.Kind.validate(); err != nil {
		return err
	}
	if !validOpaqueIDV1(ref.ResourceID) {
		return fmt.Errorf("controlapicontract: invalid expected resource ID")
	}
	// Revision zero is a real persisted state for a newly submitted Learning
	// Proposal. Whether zero is admissible is therefore an operation-level
	// rule, while this transport-free reference only enforces JSON safety.
	if ref.Revision > maxSafeJSONIntegerV1 {
		return fmt.Errorf(
			"controlapicontract: expected resource revision must be a JSON-safe integer",
		)
	}
	if !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("controlapicontract: invalid expected resource digest")
	}
	return nil
}

func expectedKindForOperationV1(
	operation ControlOperationV1,
) (ControlResourceKindV1, error) {
	switch operation {
	case OperationModuleApplyV1, OperationModuleDisableV1,
		OperationModuleUpgradeReviewV1, OperationModuleUpgradeApplyV1:
		return ResourcePublishedPointerV1, nil
	case OperationLearningProposalReviewV1:
		return ResourceLearningProposalV1, nil
	case OperationLearningCycleRunV1:
		return ResourceLearningScheduleV1, nil
	default:
		return "", fmt.Errorf(
			"controlapicontract: unsupported operation %q",
			operation,
		)
	}
}

func validateExpectedRevisionForOperationV1(
	operation ControlOperationV1,
	revision uint64,
) error {
	if operation == OperationLearningProposalReviewV1 || revision != 0 {
		return nil
	}
	return fmt.Errorf(
		"controlapicontract: operation %q requires a positive expected resource revision",
		operation,
	)
}

func validateExpectedResourceScopeV1(
	operation ControlOperationV1,
	scope ControlScopeV1,
	ref ExpectedResourceRefV1,
) error {
	switch operation {
	case OperationModuleApplyV1, OperationModuleDisableV1,
		OperationModuleUpgradeReviewV1, OperationModuleUpgradeApplyV1:
		if ref.ResourceID != scope.TenantID {
			return fmt.Errorf(
				"controlapicontract: module operation expected Published Pointer is outside its exact scope tenant",
			)
		}
	}
	if operation == OperationModuleDisableV1 && scope.Kind != ScopeTenantV1 {
		return fmt.Errorf(
			"controlapicontract: MODULE_DISABLE mutation requires exact Tenant scope",
		)
	}
	return nil
}

func domainReceiptKindForOperationV1(
	operation ControlOperationV1,
) (DomainReceiptKindV1, error) {
	switch operation {
	case OperationModuleApplyV1, OperationModuleUpgradeApplyV1:
		return DomainReceiptModuleApplyV1, nil
	case OperationModuleDisableV1:
		return DomainReceiptModuleDisableV1, nil
	case OperationModuleUpgradeReviewV1:
		return DomainReceiptModuleReviewV1, nil
	case OperationLearningProposalReviewV1:
		return DomainReceiptLearningReviewV1, nil
	case OperationLearningCycleRunV1:
		return DomainReceiptLearningCycleV1, nil
	default:
		return "", fmt.Errorf(
			"controlapicontract: unsupported operation %q",
			operation,
		)
	}
}

// ControlConfirmationStatementV1 is the exact stable meaning that an
// operator confirms before a governed mutation. It deliberately excludes the
// raw proof, challenge, transport metadata, Boot/session identity,
// authorization revision, and time. Those dynamic facts are validated by the
// confirmation authority and never change this semantic digest.
type ControlConfirmationStatementV1 struct {
	SchemaVersion             string                   `json:"schema_version"`
	PrincipalID               string                   `json:"principal_id"`
	Capability                ControlCapabilityV1      `json:"capability"`
	Intent                    ControlOperationIntentV1 `json:"intent"`
	Operation                 ControlOperationV1       `json:"operation"`
	Scope                     ControlScopeV1           `json:"scope"`
	ScopeDigest               string                   `json:"scope_digest"`
	IdempotencyKeyDigest      string                   `json:"idempotency_key_digest"`
	InputDigest               string                   `json:"input_digest"`
	OperationEvaluationDigest string                   `json:"operation_evaluation_digest"`
	ExpectedRef               ExpectedResourceRefV1    `json:"expected_ref"`
}

func NewControlConfirmationStatementV1(
	input ControlConfirmationStatementV1,
) (ControlConfirmationStatementV1, []byte, string, error) {
	if input.SchemaVersion != ControlConfirmationStatementSchemaVersionV1 {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: confirmation statement schema_version must be %q",
			ControlConfirmationStatementSchemaVersionV1,
		)
	}
	if !validOpaqueIDV1(input.PrincipalID) {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid confirmation principal identity",
		)
	}
	required, err := input.Operation.requiredCapability()
	if err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	if input.Capability != required {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation %q requires capability %q",
			input.Operation,
			required,
		)
	}
	if input.Intent != OperationIntentMutateV1 {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: confirmation statement intent must be MUTATE",
		)
	}
	scope, _, scopeDigest, err := NewControlScopeV1(input.Scope)
	if err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	if input.ScopeDigest != scopeDigest {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: confirmation statement scope digest mismatch",
		)
	}
	if !moduleapi.ValidSHA256(input.IdempotencyKeyDigest) ||
		!moduleapi.ValidSHA256(input.InputDigest) ||
		!moduleapi.ValidSHA256(input.OperationEvaluationDigest) {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid confirmation semantic digest",
		)
	}
	if err := input.ExpectedRef.Validate(); err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	expectedKind, err := expectedKindForOperationV1(input.Operation)
	if err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	if input.ExpectedRef.Kind != expectedKind {
		return ControlConfirmationStatementV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation %q requires expected resource kind %q",
			input.Operation,
			expectedKind,
		)
	}
	if err := validateExpectedResourceScopeV1(
		input.Operation,
		scope,
		input.ExpectedRef,
	); err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	if err := validateExpectedRevisionForOperationV1(
		input.Operation,
		input.ExpectedRef.Revision,
	); err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	frozen := input
	frozen.Scope = scope
	frozen.ScopeDigest = scopeDigest
	canonical, digest, err := freezeV1(
		frozen,
		controlConfirmationStatementDigestDomainV1,
		MaxControlConfirmationStatementWireBytesV1,
		256,
	)
	if err != nil {
		return ControlConfirmationStatementV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlConfirmationStatementV1(
	canonical []byte,
	expectedDigest string,
) (ControlConfirmationStatementV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlConfirmationStatementWireBytesV1,
		256,
	); err != nil {
		return ControlConfirmationStatementV1{}, err
	}
	if err := verifyDigestV1(
		controlConfirmationStatementDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlConfirmationStatementV1{}, err
	}
	var decoded ControlConfirmationStatementV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlConfirmationStatementV1{}, err
	}
	restored, rebuilt, digest, err := NewControlConfirmationStatementV1(decoded)
	if err != nil {
		return ControlConfirmationStatementV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlConfirmationStatementV1{}, fmt.Errorf(
			"controlapicontract: confirmation statement is not frozen canonically",
		)
	}
	return restored, nil
}

func confirmationStatementFromRequestV1(
	request ControlOperationRequestV1,
) ControlConfirmationStatementV1 {
	return ControlConfirmationStatementV1{
		SchemaVersion:             ControlConfirmationStatementSchemaVersionV1,
		PrincipalID:               request.PrincipalID,
		Capability:                request.Capability,
		Intent:                    request.Intent,
		Operation:                 request.Operation,
		Scope:                     request.Scope,
		ScopeDigest:               request.ScopeDigest,
		IdempotencyKeyDigest:      request.IdempotencyKeyDigest,
		InputDigest:               request.InputDigest,
		OperationEvaluationDigest: request.OperationEvaluationDigest,
		ExpectedRef:               request.ExpectedRef,
	}
}

// ControlOperationRequestV1 is constructed only after transport parsing and
// authentication. PrincipalID and Capability are server-injected. InputDigest
// covers the separately validated domain input. OperationEvaluationDigest
// binds the stable evaluation shown for a MUTATE request, and
// ConfirmationDigest must be the exact ControlConfirmationStatementV1 digest
// rebuilt from all other stable mutation fields. No HTTP body, raw
// idempotency key or confirmation proof, credential, path, URL, dynamic
// session metadata, or free-form text is copied into this contract.
type ControlOperationRequestV1 struct {
	SchemaVersion             string                   `json:"schema_version"`
	PrincipalID               string                   `json:"principal_id"`
	Capability                ControlCapabilityV1      `json:"capability"`
	Scope                     ControlScopeV1           `json:"scope"`
	ScopeDigest               string                   `json:"scope_digest"`
	Operation                 ControlOperationV1       `json:"operation"`
	Intent                    ControlOperationIntentV1 `json:"intent"`
	IdempotencyKeyDigest      string                   `json:"idempotency_key_digest,omitempty"`
	InputDigest               string                   `json:"input_digest"`
	OperationEvaluationDigest string                   `json:"operation_evaluation_digest,omitempty"`
	ExpectedRef               ExpectedResourceRefV1    `json:"expected_ref"`
	ConfirmationDigest        string                   `json:"confirmation_digest,omitempty"`
}

func NewControlOperationRequestV1(
	input ControlOperationRequestV1,
) (ControlOperationRequestV1, []byte, string, error) {
	if input.SchemaVersion != ControlOperationRequestSchemaVersionV1 {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation request schema_version must be %q",
			ControlOperationRequestSchemaVersionV1,
		)
	}
	if !validOpaqueIDV1(input.PrincipalID) {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid principal identity",
		)
	}
	required, err := input.Operation.requiredCapability()
	if err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	if input.Capability != required {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation %q requires capability %q",
			input.Operation,
			required,
		)
	}
	scope, _, scopeDigest, err := NewControlScopeV1(input.Scope)
	if err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	if input.ScopeDigest != "" && input.ScopeDigest != scopeDigest {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation request scope digest mismatch",
		)
	}
	if err := validateIntentDigestsV1(
		input.Intent,
		input.IdempotencyKeyDigest,
		input.OperationEvaluationDigest,
		input.ConfirmationDigest,
	); err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	if !moduleapi.ValidSHA256(input.InputDigest) {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid operation input digest",
		)
	}
	if err := input.ExpectedRef.Validate(); err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	expectedKind, err := expectedKindForOperationV1(input.Operation)
	if err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	if input.ExpectedRef.Kind != expectedKind {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation %q requires expected resource kind %q",
			input.Operation,
			expectedKind,
		)
	}
	if input.Intent == OperationIntentMutateV1 {
		if err := validateExpectedResourceScopeV1(
			input.Operation,
			scope,
			input.ExpectedRef,
		); err != nil {
			return ControlOperationRequestV1{}, nil, "", err
		}
	} else if input.ExpectedRef.ResourceID != scope.TenantID &&
		input.ExpectedRef.Kind == ResourcePublishedPointerV1 {
		return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
			"controlapicontract: module operation expected Published Pointer is outside its exact scope tenant",
		)
	}
	if err := validateExpectedRevisionForOperationV1(
		input.Operation,
		input.ExpectedRef.Revision,
	); err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	frozen := input
	frozen.Scope = scope
	frozen.ScopeDigest = scopeDigest
	if frozen.Intent == OperationIntentMutateV1 {
		_, _, confirmationDigest, err := NewControlConfirmationStatementV1(
			confirmationStatementFromRequestV1(frozen),
		)
		if err != nil {
			return ControlOperationRequestV1{}, nil, "", err
		}
		if frozen.ConfirmationDigest != confirmationDigest {
			return ControlOperationRequestV1{}, nil, "", fmt.Errorf(
				"controlapicontract: operation request confirmation digest does not bind its exact stable statement",
			)
		}
	}
	canonical, digest, err := freezeV1(
		frozen,
		controlOperationRequestDigestDomainV1,
		MaxControlOperationRequestWireBytesV1,
		256,
	)
	if err != nil {
		return ControlOperationRequestV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlOperationRequestV1(
	canonical []byte,
	expectedDigest string,
) (ControlOperationRequestV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlOperationRequestWireBytesV1,
		256,
	); err != nil {
		return ControlOperationRequestV1{}, err
	}
	if err := verifyDigestV1(
		controlOperationRequestDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlOperationRequestV1{}, err
	}
	var decoded ControlOperationRequestV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlOperationRequestV1{}, err
	}
	restored, rebuilt, digest, err := NewControlOperationRequestV1(decoded)
	if err != nil {
		return ControlOperationRequestV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlOperationRequestV1{}, fmt.Errorf(
			"controlapicontract: operation request is not frozen canonically",
		)
	}
	return restored, nil
}

type ControlOperationStatusV1 string

const (
	OperationStatusDryRunV1   ControlOperationStatusV1 = "DRY_RUN"
	OperationStatusNoChangeV1 ControlOperationStatusV1 = "NO_CHANGE"
	OperationStatusAppliedV1  ControlOperationStatusV1 = "APPLIED"
	OperationStatusRejectedV1 ControlOperationStatusV1 = "REJECTED"
	OperationStatusUnknownV1  ControlOperationStatusV1 = "UNKNOWN"
)

type ControlReplayDispositionV1 string

const (
	ReplayReturnExactReceiptV1       ControlReplayDispositionV1 = "RETURN_EXACT_RECEIPT"
	ReplayNoRetryV1                  ControlReplayDispositionV1 = "NO_RETRY"
	ReplayExactReceiptOnlyNoReplayV1 ControlReplayDispositionV1 = "EXACT_RECEIPT_ONLY_NO_REPLAY"
)

type DomainReceiptKindV1 string

const (
	DomainReceiptModuleApplyV1    DomainReceiptKindV1 = "MODULE_APPLY"
	DomainReceiptModuleDisableV1  DomainReceiptKindV1 = "MODULE_DISABLE"
	DomainReceiptModuleReviewV1   DomainReceiptKindV1 = "MODULE_UPGRADE_REVIEW"
	DomainReceiptLearningReviewV1 DomainReceiptKindV1 = "LEARNING_REVIEW"
	DomainReceiptLearningCycleV1  DomainReceiptKindV1 = "LEARNING_CYCLE"
	DomainReceiptOutcomeUnknownV1 DomainReceiptKindV1 = "OUTCOME_UNKNOWN"
)

func (kind DomainReceiptKindV1) validate() error {
	switch kind {
	case DomainReceiptModuleApplyV1, DomainReceiptModuleDisableV1,
		DomainReceiptModuleReviewV1, DomainReceiptLearningReviewV1,
		DomainReceiptLearningCycleV1, DomainReceiptOutcomeUnknownV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported domain receipt kind %q",
			kind,
		)
	}
}

type DomainReceiptRefV1 struct {
	Kind   DomainReceiptKindV1 `json:"kind"`
	ID     string              `json:"id"`
	Digest string              `json:"digest"`
}

func (ref DomainReceiptRefV1) Validate() error {
	if err := ref.Kind.validate(); err != nil {
		return err
	}
	if !validOpaqueIDV1(ref.ID) || !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("controlapicontract: invalid domain receipt reference")
	}
	return nil
}

func validateAppliedTransitionV1(
	operation ControlOperationV1,
	pre ExpectedResourceRefV1,
	post ExpectedResourceRefV1,
) error {
	if post.Kind != pre.Kind || post.ResourceID != pre.ResourceID {
		return fmt.Errorf(
			"controlapicontract: APPLIED post-reference changed resource identity",
		)
	}
	exact := post == pre
	adjacentChanged := post.Revision == pre.Revision+1 &&
		post.Digest != pre.Digest

	switch operation {
	case OperationModuleApplyV1, OperationModuleDisableV1,
		OperationModuleUpgradeApplyV1:
		if !adjacentChanged {
			return fmt.Errorf(
				"controlapicontract: operation %q requires an adjacent changed Published Pointer",
				operation,
			)
		}
	case OperationModuleUpgradeReviewV1:
		// Review creation is append-only and inert. The exact Review is carried
		// by DomainReceipt; its Published Pointer concurrency basis is unchanged.
		if !exact {
			return fmt.Errorf(
				"controlapicontract: MODULE_UPGRADE_REVIEW cannot change its Published Pointer basis",
			)
		}
	case OperationLearningProposalReviewV1:
		validTransition :=
			(pre.Revision == 0 && post.Revision == 2) ||
				(pre.Revision == 1 && post.Revision == 2) ||
				(pre.Revision == 2 && post.Revision == 3)
		if !validTransition || post.Digest == pre.Digest {
			return fmt.Errorf(
				"controlapicontract: LEARNING_PROPOSAL_REVIEW transition is not a known terminal projection",
			)
		}
	case OperationLearningCycleRunV1:
		// A new due window advances the Schedule once. Resuming the one open
		// Task advances only that Task/Run, so the Schedule basis stays exact.
		if !exact && !adjacentChanged {
			return fmt.Errorf(
				"controlapicontract: LEARNING_CYCLE_RUN requires an exact or adjacent changed Schedule basis",
			)
		}
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported operation %q",
			operation,
		)
	}
	return nil
}

// ControlOperationReceiptV1 is a compact idempotency/audit projection. It
// references, but never duplicates, the domain receipt or effect state.
// UNKNOWN can only return the exact unknown receipt and explicitly forbids
// semantic replay, replacement attempts, provider substitution, and rebase.
type ControlOperationReceiptV1 struct {
	SchemaVersion         string                     `json:"schema_version"`
	RequestDigest         string                     `json:"request_digest"`
	Intent                ControlOperationIntentV1   `json:"intent"`
	IdempotencyKeyDigest  string                     `json:"idempotency_key_digest,omitempty"`
	PrincipalID           string                     `json:"principal_id"`
	ScopeDigest           string                     `json:"scope_digest"`
	Operation             ControlOperationV1         `json:"operation"`
	Status                ControlOperationStatusV1   `json:"status"`
	ErrorCode             ErrorCodeV1                `json:"error_code"`
	PreRef                *ExpectedResourceRefV1     `json:"pre_ref,omitempty"`
	PostRef               *ExpectedResourceRefV1     `json:"post_ref,omitempty"`
	DomainReceipt         *DomainReceiptRefV1        `json:"domain_receipt,omitempty"`
	ReplayDisposition     ControlReplayDispositionV1 `json:"replay_disposition"`
	CompletedAtUnixMicros uint64                     `json:"completed_at_unix_micros"`
}

func NewControlOperationReceiptV1(
	input ControlOperationReceiptV1,
) (ControlOperationReceiptV1, []byte, string, error) {
	if input.SchemaVersion != ControlOperationReceiptSchemaVersionV1 {
		return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
			"controlapicontract: operation receipt schema_version must be %q",
			ControlOperationReceiptSchemaVersionV1,
		)
	}
	if !moduleapi.ValidSHA256(input.RequestDigest) ||
		!moduleapi.ValidSHA256(input.ScopeDigest) ||
		!validOpaqueIDV1(input.PrincipalID) {
		return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid receipt request, scope, or principal identity",
		)
	}
	if _, err := input.Operation.requiredCapability(); err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	if err := validateReceiptIntentKeyV1(
		input.Intent,
		input.IdempotencyKeyDigest,
	); err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	if err := input.ErrorCode.Validate(); err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	if input.PreRef != nil {
		if err := input.PreRef.Validate(); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	}
	if input.PostRef != nil {
		if err := input.PostRef.Validate(); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	}
	if input.DomainReceipt != nil {
		if err := input.DomainReceipt.Validate(); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	}
	expectedKind, err := expectedKindForOperationV1(input.Operation)
	if err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	if input.PreRef != nil && input.PreRef.Kind != expectedKind {
		return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
			"controlapicontract: receipt precondition kind does not match operation",
		)
	}
	if input.PreRef != nil {
		if err := validateExpectedRevisionForOperationV1(
			input.Operation,
			input.PreRef.Revision,
		); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	}
	if input.PostRef != nil {
		if err := validateExpectedRevisionForOperationV1(
			input.Operation,
			input.PostRef.Revision,
		); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	}
	// These are Control-layer outcomes, not domain business verdicts. A known
	// persisted REVIEW_FAILED/FAILED/INVALID_RESULT terminal is APPLIED; only a
	// terminal that cannot be proven is UNKNOWN. NO_CHANGE and REJECTED cannot
	// carry a domain effect reference.
	switch input.Status {
	case OperationStatusDryRunV1:
		if input.Intent != OperationIntentDryRunV1 ||
			input.ErrorCode != ErrorNoneV1 || input.PreRef == nil ||
			input.PostRef != nil || input.DomainReceipt != nil ||
			input.ReplayDisposition != ReplayNoRetryV1 {
			return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
				"controlapicontract: DRY_RUN receipt requires an exact precondition, no effect refs, and no retry",
			)
		}
	case OperationStatusNoChangeV1:
		if input.Intent != OperationIntentMutateV1 ||
			input.ErrorCode != ErrorNoneV1 || input.PreRef == nil ||
			input.PostRef == nil || *input.PostRef != *input.PreRef ||
			input.DomainReceipt != nil ||
			input.ReplayDisposition != ReplayReturnExactReceiptV1 {
			return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
				"controlapicontract: NO_CHANGE receipt requires identical exact pre/post refs, no domain effect, and exact-receipt replay",
			)
		}
	case OperationStatusAppliedV1:
		domainKind, err := domainReceiptKindForOperationV1(input.Operation)
		if err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
		if input.Intent != OperationIntentMutateV1 ||
			input.ErrorCode != ErrorNoneV1 || input.PreRef == nil ||
			input.PostRef == nil ||
			input.DomainReceipt == nil ||
			input.DomainReceipt.Kind != domainKind ||
			input.ReplayDisposition != ReplayReturnExactReceiptV1 {
			return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
				"controlapicontract: APPLIED receipt requires exact pre/post/domain refs and exact-receipt replay",
			)
		}
		if err := validateAppliedTransitionV1(
			input.Operation,
			*input.PreRef,
			*input.PostRef,
		); err != nil {
			return ControlOperationReceiptV1{}, nil, "", err
		}
	case OperationStatusRejectedV1:
		if input.ErrorCode == ErrorNoneV1 ||
			input.ErrorCode == ErrorOutcomeUnknownV1 ||
			input.PostRef != nil ||
			input.DomainReceipt != nil ||
			input.ReplayDisposition != ReplayNoRetryV1 {
			return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
				"controlapicontract: REJECTED receipt requires an error and no effect refs",
			)
		}
	case OperationStatusUnknownV1:
		if input.Intent != OperationIntentMutateV1 ||
			input.ErrorCode != ErrorOutcomeUnknownV1 || input.PreRef == nil ||
			input.PostRef != nil ||
			input.DomainReceipt == nil ||
			input.DomainReceipt.Kind != DomainReceiptOutcomeUnknownV1 ||
			input.ReplayDisposition != ReplayExactReceiptOnlyNoReplayV1 {
			return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
				"controlapicontract: UNKNOWN requires its exact domain receipt and forbids semantic replay",
			)
		}
	default:
		return ControlOperationReceiptV1{}, nil, "", fmt.Errorf(
			"controlapicontract: unsupported operation status %q",
			input.Status,
		)
	}
	if err := validateTimeV1(
		"operation completion time",
		input.CompletedAtUnixMicros,
	); err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	frozen := input
	if input.PreRef != nil {
		copied := *input.PreRef
		frozen.PreRef = &copied
	}
	if input.PostRef != nil {
		copied := *input.PostRef
		frozen.PostRef = &copied
	}
	if input.DomainReceipt != nil {
		copied := *input.DomainReceipt
		frozen.DomainReceipt = &copied
	}
	canonical, digest, err := freezeV1(
		frozen,
		controlOperationReceiptDigestDomainV1,
		MaxControlOperationReceiptWireBytesV1,
		256,
	)
	if err != nil {
		return ControlOperationReceiptV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlOperationReceiptV1(
	canonical []byte,
	expectedDigest string,
) (ControlOperationReceiptV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlOperationReceiptWireBytesV1,
		256,
	); err != nil {
		return ControlOperationReceiptV1{}, err
	}
	if err := verifyDigestV1(
		controlOperationReceiptDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlOperationReceiptV1{}, err
	}
	var decoded ControlOperationReceiptV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlOperationReceiptV1{}, err
	}
	restored, rebuilt, digest, err := NewControlOperationReceiptV1(decoded)
	if err != nil {
		return ControlOperationReceiptV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlOperationReceiptV1{}, fmt.Errorf(
			"controlapicontract: operation receipt is not frozen canonically",
		)
	}
	return restored, nil
}
