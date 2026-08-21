// Package controlapicontract defines the authority-free, transport-independent
// wire facts used by the optional local Control API. It does not authenticate,
// authorize, access the Current Store, or grant a capability.
package controlapicontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ControlSessionSchemaVersionV1               = "control-session/v1"
	ControlScopeSchemaVersionV1                 = "control-scope/v1"
	ControlViewSnapshotSchemaVersionV1          = "control-view-snapshot/v1"
	ControlConfirmationStatementSchemaVersionV1 = "control-confirmation-statement/v1"
	ControlOperationRequestSchemaVersionV1      = "control-operation-request/v1"
	ControlOperationReceiptSchemaVersionV1      = "control-operation-receipt/v1"
	ControlEventCursorSchemaVersionV1           = "control-event-cursor/v1"

	MaxControlSessionWireBytesV1               = 16 << 10
	MaxControlScopeWireBytesV1                 = 1 << 10
	MaxControlViewSnapshotWireBytesV1          = 16 << 10
	MaxControlConfirmationStatementWireBytesV1 = 8 << 10
	MaxControlOperationRequestWireBytesV1      = 8 << 10
	MaxControlOperationReceiptWireBytesV1      = 16 << 10
	MaxControlEventCursorWireBytesV1           = 16 << 10
	MaxPublishedBasisRefWireBytesV1            = 8 << 10
	MaxControlCapabilitiesV1                   = 32
	MaxControlViewSectionsV1                   = 32
	MaxControlSourceRevisionsV1                = 32
	MaxControlPageSizeV1                       = 100

	maxControlJSONDepthV1 = 16
	maxSafeJSONIntegerV1  = uint64(1<<53 - 1)
	maxOpaqueIDBytesV1    = moduleapi.MaxOpaqueIDBytes

	controlSessionDigestDomainV1               = "freeagent.control-session/v1"
	controlScopeDigestDomainV1                 = "freeagent.control-scope/v1"
	controlViewSnapshotDigestDomainV1          = "freeagent.control-view-snapshot/v1"
	controlConfirmationStatementDigestDomainV1 = "freeagent.control-confirmation-statement/v1"
	controlOperationRequestDigestDomainV1      = "freeagent.control-operation-request/v1"
	controlOperationReceiptDigestDomainV1      = "freeagent.control-operation-receipt/v1"
	controlEventCursorDigestDomainV1           = "freeagent.control-event-cursor/v1"
	publishedBasisRefDigestDomainV1            = "freeagent.control-published-pointer-ref/v1"
)

// ErrorCodeV1 is the finite error vocabulary shared by future Control API
// adapters. Details remain local diagnostics and are never copied into a wire
// contract as arbitrary text.
type ErrorCodeV1 string

const (
	ErrorNoneV1                 ErrorCodeV1 = "NONE"
	ErrorInvalidRequestV1       ErrorCodeV1 = "INVALID_REQUEST"
	ErrorUnauthenticatedV1      ErrorCodeV1 = "UNAUTHENTICATED"
	ErrorSessionExpiredV1       ErrorCodeV1 = "SESSION_EXPIRED"
	ErrorForbiddenV1            ErrorCodeV1 = "FORBIDDEN"
	ErrorNotFoundV1             ErrorCodeV1 = "NOT_FOUND"
	ErrorConflictV1             ErrorCodeV1 = "CONFLICT"
	ErrorPreconditionRequiredV1 ErrorCodeV1 = "PRECONDITION_REQUIRED"
	ErrorRevisionConflictV1     ErrorCodeV1 = "REVISION_CONFLICT"
	ErrorIdempotencyConflictV1  ErrorCodeV1 = "IDEMPOTENCY_CONFLICT"
	ErrorCursorInvalidV1        ErrorCodeV1 = "CURSOR_INVALID"
	ErrorCursorStaleV1          ErrorCodeV1 = "CURSOR_STALE"
	ErrorResourceExhaustedV1    ErrorCodeV1 = "RESOURCE_EXHAUSTED"
	ErrorStoreBusyV1            ErrorCodeV1 = "STORE_BUSY"
	ErrorStoreUnavailableV1     ErrorCodeV1 = "STORE_UNAVAILABLE"
	ErrorIntegrityFailureV1     ErrorCodeV1 = "INTEGRITY_FAILURE"
	ErrorOutcomeUnknownV1       ErrorCodeV1 = "OUTCOME_UNKNOWN"
	ErrorCancelledV1            ErrorCodeV1 = "CANCELLED"
	ErrorInternalV1             ErrorCodeV1 = "INTERNAL_ERROR"
)

func (code ErrorCodeV1) Validate() error {
	switch code {
	case ErrorNoneV1, ErrorInvalidRequestV1, ErrorUnauthenticatedV1,
		ErrorSessionExpiredV1, ErrorForbiddenV1, ErrorNotFoundV1,
		ErrorConflictV1, ErrorPreconditionRequiredV1, ErrorRevisionConflictV1,
		ErrorIdempotencyConflictV1, ErrorCursorInvalidV1, ErrorCursorStaleV1,
		ErrorResourceExhaustedV1, ErrorStoreBusyV1, ErrorStoreUnavailableV1,
		ErrorIntegrityFailureV1, ErrorOutcomeUnknownV1, ErrorCancelledV1,
		ErrorInternalV1:
		return nil
	default:
		return fmt.Errorf("controlapicontract: unsupported error code %q", code)
	}
}

type ControlPageCollectionV1 string

const (
	PageCollectionRunsV1     ControlPageCollectionV1 = "RUNS"
	PageCollectionModulesV1  ControlPageCollectionV1 = "MODULES"
	PageCollectionLearningV1 ControlPageCollectionV1 = "LEARNING"
	PageCollectionUsageV1    ControlPageCollectionV1 = "USAGE"
	PageCollectionAuditV1    ControlPageCollectionV1 = "AUDIT"
)

func (collection ControlPageCollectionV1) validate() error {
	switch collection {
	case PageCollectionRunsV1, PageCollectionModulesV1,
		PageCollectionLearningV1, PageCollectionUsageV1,
		PageCollectionAuditV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported page collection %q",
			collection,
		)
	}
}

// PageTokenRefV1 is keyset metadata, not an event cursor or bearer token.
// PositionDigest binds a server-validated stable sort tuple without copying it.
type PageTokenRefV1 struct {
	ScopeDigest        string                  `json:"scope_digest"`
	ViewSnapshotDigest string                  `json:"view_snapshot_digest"`
	Collection         ControlPageCollectionV1 `json:"collection"`
	PositionDigest     string                  `json:"position_digest"`
}

func (ref PageTokenRefV1) Validate() error {
	if !moduleapi.ValidSHA256(ref.ScopeDigest) ||
		!moduleapi.ValidSHA256(ref.ViewSnapshotDigest) ||
		!moduleapi.ValidSHA256(ref.PositionDigest) {
		return fmt.Errorf("controlapicontract: invalid page-token digest metadata")
	}
	return ref.Collection.validate()
}

// PageQueryV1 is a bounded keyset-page request. After and FilterDigest
// identify separately validated metadata; neither is a control-event-cursor,
// raw filter, URL, filesystem path, credential, or bearer secret.
type PageQueryV1 struct {
	Limit        uint16          `json:"limit"`
	After        *PageTokenRefV1 `json:"after,omitempty"`
	FilterDigest string          `json:"filter_digest,omitempty"`
}

func (query PageQueryV1) Validate() error {
	if query.Limit == 0 || query.Limit > MaxControlPageSizeV1 {
		return fmt.Errorf(
			"controlapicontract: page limit must be between 1 and %d",
			MaxControlPageSizeV1,
		)
	}
	if query.After != nil {
		if err := query.After.Validate(); err != nil {
			return err
		}
	}
	if query.FilterDigest != "" && !moduleapi.ValidSHA256(query.FilterDigest) {
		return fmt.Errorf("controlapicontract: invalid filter digest")
	}
	return nil
}

func freezeV1(
	value any,
	domain string,
	maxBytes int,
	maxNodes int,
) ([]byte, string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("controlapicontract: encode object: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maxBytes,
			MaxDepth: maxControlJSONDepthV1,
			MaxNodes: maxNodes,
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"controlapicontract: canonicalize object: %w",
			err,
		)
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return nil, "", fmt.Errorf(
			"controlapicontract: canonical value is not a JSON object",
		)
	}
	return bytes.Clone(canonical), moduleapi.Digest(domain, canonical), nil
}

func requireExactCanonicalV1(
	canonical []byte,
	maxBytes int,
	maxNodes int,
) error {
	if len(canonical) == 0 || len(canonical) > maxBytes ||
		canonical[0] != '{' {
		return fmt.Errorf("controlapicontract: object is not bounded canonical JSON")
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		bytes.Clone(canonical),
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maxBytes,
			MaxDepth: maxControlJSONDepthV1,
			MaxNodes: maxNodes,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("controlapicontract: object is not exact canonical JSON")
	}
	return nil
}

func decodeStrictV1(canonical []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(bytes.Clone(canonical)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("controlapicontract: decode object: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("controlapicontract: object has trailing JSON")
		}
		return fmt.Errorf("controlapicontract: decode trailing data: %w", err)
	}
	return nil
}

func verifyDigestV1(domain string, canonical []byte, expected string) error {
	if !moduleapi.ValidSHA256(expected) ||
		moduleapi.Digest(domain, canonical) != expected {
		return fmt.Errorf("controlapicontract: exact digest mismatch")
	}
	return nil
}

func validOpaqueIDV1(value string) bool {
	if value == "" || len(value) > maxOpaqueIDBytesV1 ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateRevisionV1(name string, revision uint64) error {
	if revision == 0 || revision > maxSafeJSONIntegerV1 {
		return fmt.Errorf(
			"controlapicontract: %s must be a positive JSON-safe integer",
			name,
		)
	}
	return nil
}

func validateTimeV1(name string, micros uint64) error {
	if micros == 0 || micros > maxSafeJSONIntegerV1 {
		return fmt.Errorf(
			"controlapicontract: %s must be a positive JSON-safe Unix microsecond",
			name,
		)
	}
	return nil
}

func sortStringsV1[T ~string](values []T) []T {
	cloned := append([]T(nil), values...)
	sort.Slice(cloned, func(left, right int) bool {
		return string(cloned[left]) < string(cloned[right])
	})
	return cloned
}

func rejectDuplicateStringsV1[T ~string](name string, values []T) error {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return fmt.Errorf("controlapicontract: duplicate %s %q", name, values[index])
		}
	}
	return nil
}
