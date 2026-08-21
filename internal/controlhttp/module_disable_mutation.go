package controlhttp

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapipolicy"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type moduleDisableConfirmationResponseV1 struct {
	SchemaVersion string `json:"schema_version"`

	Request       controlapicontract.ControlOperationRequestV1 `json:"request"`
	RequestDigest string                                       `json:"request_digest"`

	Evaluation       controlapp.ModuleDisableEvaluationV1 `json:"evaluation"`
	EvaluationDigest string                               `json:"evaluation_digest"`

	Statement       controlapicontract.ControlConfirmationStatementV1 `json:"statement"`
	StatementDigest string                                            `json:"statement_digest"`

	ConfirmationProof   string `json:"confirmation_proof"`
	ExpiresAtUnixMicros uint64 `json:"expires_at_unix_micros"`
}

func (handler *HandlerV1) serveModuleDisableConfirmationV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !exactContentTypeV1(request.Header, true) ||
		hasUnknownModuleDisableAuthorityHeaderV1(request.Header, false, false) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := tenantScopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitModuleOperationV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()

	ifMatchDigest, err := requiredIfMatchDigestV1(request.Header)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	idempotencyKeyDigest, err := requiredIdempotencyKeyDigestV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	body, inputDigest, code := decodeNarrowModuleDisableBodyV1(request)
	if code != "" {
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	expected, ok := moduleDisableExpectedPointerV1(
		scope,
		body.ExpectedPointerRevision,
		ifMatchDigest,
	)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}

	result, err := handler.moduleDisableConfirmation.IssueModuleDisableConfirmationV1(
		request.Context(),
		IssueModuleDisableConfirmationInputV1{
			Authorization:        permit,
			Scope:                scope,
			ExpectedPointer:      expected,
			Body:                 body,
			IdempotencyKeyDigest: idempotencyKeyDigest,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer clear(result.ConfirmationProof)
	nowMicros, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	response, ok := validModuleDisableConfirmationResultV1(
		result,
		permit,
		scope,
		expected,
		body,
		inputDigest,
		idempotencyKeyDigest,
		nowMicros,
	)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) serveModuleDisableMutateV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !exactContentTypeV1(request.Header, true) ||
		hasUnknownModuleDisableAuthorityHeaderV1(request.Header, true, true) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := tenantScopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitModuleOperationV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()

	ifMatchDigest, err := requiredIfMatchDigestV1(request.Header)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	idempotencyKeyDigest, err := requiredIdempotencyKeyDigestV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	body, inputDigest, code := decodeNarrowModuleDisableBodyV1(request)
	if code != "" {
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	expected, ok := moduleDisableExpectedPointerV1(
		scope,
		body.ExpectedPointerRevision,
		ifMatchDigest,
	)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	operationEvaluationDigest, err := requiredSHA256HeaderV1(
		request.Header,
		OperationEvaluationDigestHeaderV1,
	)
	if err != nil {
		code := controlapicontract.ErrorInvalidRequestV1
		if errors.Is(err, controlapp.ErrPreconditionRequired) {
			code = controlapicontract.ErrorPreconditionRequiredV1
		}
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	confirmationProof, err := optionalConfirmationProofV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	defer clear(confirmationProof)

	result, err := handler.moduleDisableMutation.MutateModuleDisableV1(
		request.Context(),
		MutateModuleDisableInputV1{
			Authorization:             permit,
			Scope:                     scope,
			ExpectedPointer:           expected,
			Body:                      body,
			IdempotencyKeyDigest:      idempotencyKeyDigest,
			OperationEvaluationDigest: operationEvaluationDigest,
			ConfirmationProof:         confirmationProof,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	response, ok := validModuleDisableMutationResultV1(
		result,
		permit,
		scope,
		expected,
		inputDigest,
		idempotencyKeyDigest,
		operationEvaluationDigest,
	)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func tenantScopeFromHeadersV1(
	header http.Header,
) (controlapicontract.ControlScopeV1, error) {
	scope, err := scopeFromHeadersV1(header)
	if err != nil || scope.Kind != controlapicontract.ScopeTenantV1 ||
		scope.WorkspaceID != "" {
		return controlapicontract.ControlScopeV1{}, controlapp.ErrInvalidRequest
	}
	return scope, nil
}

func moduleDisableExpectedPointerV1(
	scope controlapicontract.ControlScopeV1,
	revision uint64,
	digest string,
) (controlapicontract.ExpectedResourceRefV1, bool) {
	expected := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: scope.TenantID,
		Revision:   revision,
		Digest:     digest,
	}
	if expected.Validate() != nil {
		return controlapicontract.ExpectedResourceRefV1{}, false
	}
	return expected, true
}

func requiredIdempotencyKeyDigestV1(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 || len(values[0]) < controlapipolicy.MinimumIdempotencyKeyBytesV1 ||
		len(values[0]) > controlapipolicy.MaximumIdempotencyKeyBytesV1 {
		return "", controlapp.ErrInvalidRequest
	}
	for index := 0; index < len(values[0]); index++ {
		if values[0][index] < 0x21 || values[0][index] > 0x7e {
			return "", controlapp.ErrInvalidRequest
		}
	}
	digest, err := controlapipolicy.DigestIdempotencyKeyV1(values[0])
	if err != nil {
		return "", controlapp.ErrInvalidRequest
	}
	return digest, nil
}

func requiredSHA256HeaderV1(header http.Header, name string) (string, error) {
	value, present, ok := exactHeaderValueV1(header, name, false)
	if !present {
		return "", controlapp.ErrPreconditionRequired
	}
	if !ok || !moduleapi.ValidSHA256(value) {
		return "", controlapp.ErrInvalidRequest
	}
	return value, nil
}

func optionalConfirmationProofV1(header http.Header) ([]byte, error) {
	value, present, ok := exactHeaderValueV1(header, ConfirmationHeaderV1, false)
	if !ok {
		return nil, controlapp.ErrInvalidRequest
	}
	if !present {
		return nil, nil
	}
	if len(value) != base64.RawURLEncoding.EncodedLen(ConfirmationProofBytesV1) {
		return nil, controlapp.ErrInvalidRequest
	}
	proof, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(proof) != ConfirmationProofBytesV1 ||
		base64.RawURLEncoding.EncodeToString(proof) != value {
		clear(proof)
		return nil, controlapp.ErrInvalidRequest
	}
	return proof, nil
}

func decodeNarrowModuleDisableBodyV1(
	request *http.Request,
) (controlapp.ModuleDisableDryRunBodyV1, string, controlapicontract.ErrorCodeV1) {
	body, err := readBoundedBodyV1(request, MaximumOperationBodyBytesV1)
	if err != nil {
		if errors.Is(err, errBodyTooLargeV1) {
			return controlapp.ModuleDisableDryRunBodyV1{}, "",
				controlapicontract.ErrorResourceExhaustedV1
		}
		return controlapp.ModuleDisableDryRunBodyV1{}, "",
			controlapicontract.ErrorInvalidRequestV1
	}
	defer clear(body)
	var decoded controlapp.ModuleDisableDryRunBodyV1
	if err := decodeExactJSONV1(
		body,
		&decoded,
		MaximumOperationBodyBytesV1,
		128,
	); err != nil {
		return controlapp.ModuleDisableDryRunBodyV1{}, "",
			controlapicontract.ErrorInvalidRequestV1
	}
	frozen, canonical, digest, err := controlapp.NewModuleDisableDryRunBodyV1(decoded)
	clear(canonical)
	if err != nil || frozen.BindingTarget.Kind != controlapp.ModuleBindingTargetProfileV1 ||
		frozen.BindingTarget.WorkspaceID != "" || frozen.BindingTarget.EndpointID != "" ||
		frozen.Port != (moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		}) {
		return controlapp.ModuleDisableDryRunBodyV1{}, "",
			controlapicontract.ErrorInvalidRequestV1
	}
	return frozen, digest, ""
}

func hasUnknownModuleDisableAuthorityHeaderV1(
	header http.Header,
	allowEvaluation bool,
	allowConfirmation bool,
) bool {
	allowed := map[string]struct{}{
		http.CanonicalHeaderKey(CSRFHeaderV1):            {},
		http.CanonicalHeaderKey(ScopeKindHeaderV1):       {},
		http.CanonicalHeaderKey(ScopeIDEncodingHeaderV1): {},
		http.CanonicalHeaderKey(TenantIDHeaderV1):        {},
		http.CanonicalHeaderKey(WorkspaceIDHeaderV1):     {},
	}
	if allowEvaluation {
		allowed[http.CanonicalHeaderKey(OperationEvaluationDigestHeaderV1)] = struct{}{}
	}
	if allowConfirmation {
		allowed[http.CanonicalHeaderKey(ConfirmationHeaderV1)] = struct{}{}
	}
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if strings.HasPrefix(strings.ToLower(canonical), "x-freeagent-") {
			if _, ok := allowed[canonical]; !ok {
				return true
			}
		}
	}
	return false
}

func validModuleDisableConfirmationResultV1(
	result IssueModuleDisableConfirmationResultV1,
	permit *controlsession.PermitV1,
	scope controlapicontract.ControlScopeV1,
	expected controlapicontract.ExpectedResourceRefV1,
	body controlapp.ModuleDisableDryRunBodyV1,
	inputDigest string,
	idempotencyKeyDigest string,
	nowMicros uint64,
) (moduleDisableConfirmationResponseV1, bool) {
	if permit == nil || result.SchemaVersion !=
		ModuleDisableConfirmationResultSchemaVersionV1 ||
		len(result.ConfirmationProof) != ConfirmationProofBytesV1 ||
		result.ExpiresAtUnixMicros <= nowMicros ||
		result.ExpiresAtUnixMicros-nowMicros > uint64(
			MaximumConfirmationProofLifetimeV1/time.Microsecond,
		) {
		return moduleDisableConfirmationResponseV1{}, false
	}
	session := permit.Session()
	if session.PrincipalID == "" ||
		result.ExpiresAtUnixMicros > session.ExpiresAtUnixMicros {
		return moduleDisableConfirmationResponseV1{}, false
	}
	frozenEvaluation, evaluationCanonical, evaluationDigest, err :=
		controlapp.NewModuleDisableEvaluationV1(result.Evaluation)
	clear(evaluationCanonical)
	if err != nil || evaluationDigest != result.EvaluationDigest ||
		frozenEvaluation.Operation != controlapicontract.OperationModuleDisableV1 ||
		frozenEvaluation.InputDigest != inputDigest ||
		frozenEvaluation.ExpectedRef != expected ||
		!moduleDisableEvaluationMatchesBodyV1(frozenEvaluation, body) {
		return moduleDisableConfirmationResponseV1{}, false
	}
	frozenStatement, statementCanonical, statementDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(result.Statement)
	clear(statementCanonical)
	if err != nil || frozenStatement != result.Statement ||
		statementDigest != result.StatementDigest ||
		frozenStatement.PrincipalID != session.PrincipalID ||
		frozenStatement.Capability != controlapicontract.CapabilityOperateModulesV1 ||
		frozenStatement.Intent != controlapicontract.OperationIntentMutateV1 ||
		frozenStatement.Operation != controlapicontract.OperationModuleDisableV1 ||
		frozenStatement.Scope != scope ||
		frozenStatement.IdempotencyKeyDigest != idempotencyKeyDigest ||
		frozenStatement.InputDigest != inputDigest ||
		frozenStatement.OperationEvaluationDigest != evaluationDigest ||
		frozenStatement.ExpectedRef != expected {
		return moduleDisableConfirmationResponseV1{}, false
	}
	frozenRequest, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(result.Request)
	clear(requestCanonical)
	if err != nil || frozenRequest != result.Request ||
		requestDigest != result.RequestDigest ||
		frozenRequest.PrincipalID != session.PrincipalID ||
		frozenRequest.Capability != controlapicontract.CapabilityOperateModulesV1 ||
		frozenRequest.Scope != scope ||
		frozenRequest.Operation != controlapicontract.OperationModuleDisableV1 ||
		frozenRequest.Intent != controlapicontract.OperationIntentMutateV1 ||
		frozenRequest.IdempotencyKeyDigest != idempotencyKeyDigest ||
		frozenRequest.InputDigest != inputDigest ||
		frozenRequest.OperationEvaluationDigest != evaluationDigest ||
		frozenRequest.ExpectedRef != expected ||
		frozenRequest.ConfirmationDigest != statementDigest {
		return moduleDisableConfirmationResponseV1{}, false
	}
	return moduleDisableConfirmationResponseV1{
		SchemaVersion:       ModuleDisableConfirmationResultSchemaVersionV1,
		Request:             frozenRequest,
		RequestDigest:       requestDigest,
		Evaluation:          frozenEvaluation,
		EvaluationDigest:    evaluationDigest,
		Statement:           frozenStatement,
		StatementDigest:     statementDigest,
		ConfirmationProof:   base64.RawURLEncoding.EncodeToString(result.ConfirmationProof),
		ExpiresAtUnixMicros: result.ExpiresAtUnixMicros,
	}, true
}

func moduleDisableEvaluationMatchesBodyV1(
	evaluation controlapp.ModuleDisableEvaluationV1,
	body controlapp.ModuleDisableDryRunBodyV1,
) bool {
	if evaluation.Projection.InstanceID != body.InstanceID {
		return false
	}
	removal := evaluation.Projection.BindingRemoval
	return removal == nil ||
		(removal.Target == body.BindingTarget && removal.Port == body.Port)
}

func validModuleDisableMutationResultV1(
	result MutateModuleDisableResultV1,
	permit *controlsession.PermitV1,
	scope controlapicontract.ControlScopeV1,
	expected controlapicontract.ExpectedResourceRefV1,
	inputDigest string,
	idempotencyKeyDigest string,
	operationEvaluationDigest string,
) (MutateModuleDisableResultV1, bool) {
	if permit == nil || result.SchemaVersion !=
		ModuleDisableMutationResultSchemaVersionV1 {
		return MutateModuleDisableResultV1{}, false
	}
	session := permit.Session()
	frozenRequest, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(result.Request)
	clear(requestCanonical)
	if err != nil || frozenRequest != result.Request ||
		requestDigest != result.RequestDigest || session.PrincipalID == "" ||
		frozenRequest.PrincipalID != session.PrincipalID ||
		frozenRequest.Capability != controlapicontract.CapabilityOperateModulesV1 ||
		frozenRequest.Scope != scope ||
		frozenRequest.Operation != controlapicontract.OperationModuleDisableV1 ||
		frozenRequest.Intent != controlapicontract.OperationIntentMutateV1 ||
		frozenRequest.IdempotencyKeyDigest != idempotencyKeyDigest ||
		frozenRequest.InputDigest != inputDigest ||
		frozenRequest.OperationEvaluationDigest != operationEvaluationDigest ||
		frozenRequest.ExpectedRef != expected {
		return MutateModuleDisableResultV1{}, false
	}
	frozenReceipt, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(result.Receipt)
	clear(receiptCanonical)
	if err != nil || receiptDigest != result.ReceiptDigest ||
		frozenReceipt.RequestDigest != requestDigest ||
		frozenReceipt.Intent != frozenRequest.Intent ||
		frozenReceipt.IdempotencyKeyDigest != idempotencyKeyDigest ||
		frozenReceipt.PrincipalID != frozenRequest.PrincipalID ||
		frozenReceipt.ScopeDigest != frozenRequest.ScopeDigest ||
		frozenReceipt.Operation != frozenRequest.Operation ||
		frozenReceipt.ErrorCode != controlapicontract.ErrorNoneV1 ||
		frozenReceipt.PreRef == nil || *frozenReceipt.PreRef != expected ||
		frozenReceipt.ReplayDisposition != controlapicontract.ReplayReturnExactReceiptV1 {
		return MutateModuleDisableResultV1{}, false
	}
	switch frozenReceipt.Status {
	case controlapicontract.OperationStatusNoChangeV1,
		controlapicontract.OperationStatusAppliedV1:
	default:
		return MutateModuleDisableResultV1{}, false
	}
	return MutateModuleDisableResultV1{
		SchemaVersion: ModuleDisableMutationResultSchemaVersionV1,
		Request:       frozenRequest,
		RequestDigest: requestDigest,
		Receipt:       frozenReceipt,
		ReceiptDigest: receiptDigest,
	}, true
}
