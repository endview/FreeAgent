package controlapicontract

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const testMicrosV1 = uint64(1_700_000_000_000_000)

func TestSevenContractCanonicalAndDigestCanaries(t *testing.T) {
	scope, scopeCanonical, scopeDigest := testScopeV1(t)
	session, sessionCanonical, sessionDigest := testSessionV1(t)
	view, viewCanonical, viewDigest := testViewV1(t, scope)
	statement, statementCanonical, statementDigest := testConfirmationStatementV1(t, scope)
	request, requestCanonical, requestDigest := testRequestV1(t, scope)
	receipt, receiptCanonical, receiptDigest := testUnknownReceiptV1(
		t,
		request,
		requestDigest,
	)
	event, eventCanonical, eventDigest := testEventV1(
		t,
		scopeDigest,
		viewDigest,
	)

	tests := []struct {
		name      string
		canonical []byte
		digest    string
		wantWire  string
		wantID    string
		restore   func([]byte, string) error
	}{
		{
			name: "session", canonical: sessionCanonical, digest: sessionDigest,
			wantWire: `{"authorization_revision":3,"boot_id":"boot-001","capabilities":["OBSERVE","OPERATE_MODULES"],"expires_at_unix_micros":1700003600000000,"issued_at_unix_micros":1700000000000000,"principal_id":"operator-local","schema_version":"control-session/v1","scope_set_digest":"1111111111111111111111111111111111111111111111111111111111111111","session_id":"session-001"}`,
			wantID:   "d6c64978ba1459e300d86843ada8e679b83fa582f242659f6146bdc2346974cf",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlSessionV1(wire, digest)
				return err
			},
		},
		{
			name: "scope", canonical: scopeCanonical, digest: scopeDigest,
			wantWire: `{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-a"}`,
			wantID:   "2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlScopeV1(wire, digest)
				return err
			},
		},
		{
			name: "view", canonical: viewCanonical, digest: viewDigest,
			wantWire: `{"basis":{"catalog":{"digest":"3333333333333333333333333333333333333333333333333333333333333333","id":"catalog-009","revision":8},"control":{"digest":"2222222222222222222222222222222222222222222222222222222222222222","id":"control-009","revision":7},"pointer_revision":9,"tenant_id":"tenant-a"},"observed_at_unix_micros":1700000000000010,"schema_version":"control-view-snapshot/v1","scope":{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-a"},"scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e","sections":[{"item_count":2,"kind":"MODULES","source_digest":"6666666666666666666666666666666666666666666666666666666666666666","source_revision":4,"truncated":false},{"item_count":2,"kind":"RUNS","source_digest":"7777777777777777777777777777777777777777777777777777777777777777","source_revision":4,"truncated":false}]}`,
			wantID:   "9d8fbe3534ded272898a0c6f240c7168106f54078daa93fa9879a7823f9b511e",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlViewSnapshotV1(wire, digest)
				return err
			},
		},
		{
			name: "confirmation statement", canonical: statementCanonical, digest: statementDigest,
			wantWire: `{"capability":"OPERATE_MODULES","expected_ref":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"PUBLISHED_POINTER","resource_id":"tenant-a","revision":9},"idempotency_key_digest":"8888888888888888888888888888888888888888888888888888888888888888","input_digest":"9999999999999999999999999999999999999999999999999999999999999999","intent":"MUTATE","operation":"MODULE_UPGRADE_APPLY","operation_evaluation_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","principal_id":"operator-local","schema_version":"control-confirmation-statement/v1","scope":{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-a"},"scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e"}`,
			wantID:   "83abbb93cbfbaf6aad618a39803726aa2ef38eeb90837b721bfd22667d4f20e5",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlConfirmationStatementV1(wire, digest)
				return err
			},
		},
		{
			name: "request", canonical: requestCanonical, digest: requestDigest,
			wantWire: `{"capability":"OPERATE_MODULES","confirmation_digest":"83abbb93cbfbaf6aad618a39803726aa2ef38eeb90837b721bfd22667d4f20e5","expected_ref":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"PUBLISHED_POINTER","resource_id":"tenant-a","revision":9},"idempotency_key_digest":"8888888888888888888888888888888888888888888888888888888888888888","input_digest":"9999999999999999999999999999999999999999999999999999999999999999","intent":"MUTATE","operation":"MODULE_UPGRADE_APPLY","operation_evaluation_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","principal_id":"operator-local","schema_version":"control-operation-request/v1","scope":{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-a"},"scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e"}`,
			wantID:   "de410967cc65f43cf0137e645506ea9164f5e4fba06b9e32f2fbe2fa0e275e89",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlOperationRequestV1(wire, digest)
				return err
			},
		},
		{
			name: "receipt", canonical: receiptCanonical, digest: receiptDigest,
			wantWire: `{"completed_at_unix_micros":1700000000000030,"domain_receipt":{"digest":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","id":"unknown-001","kind":"OUTCOME_UNKNOWN"},"error_code":"OUTCOME_UNKNOWN","idempotency_key_digest":"8888888888888888888888888888888888888888888888888888888888888888","intent":"MUTATE","operation":"MODULE_UPGRADE_APPLY","pre_ref":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"PUBLISHED_POINTER","resource_id":"tenant-a","revision":9},"principal_id":"operator-local","replay_disposition":"EXACT_RECEIPT_ONLY_NO_REPLAY","request_digest":"de410967cc65f43cf0137e645506ea9164f5e4fba06b9e32f2fbe2fa0e275e89","schema_version":"control-operation-receipt/v1","scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e","status":"UNKNOWN"}`,
			wantID:   "65564f8849865bd6110525001e37ce83aee471391af16e2e726f5d4646237880",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlOperationReceiptV1(wire, digest)
				return err
			},
		},
		{
			name: "event", canonical: eventCanonical, digest: eventDigest,
			wantWire: `{"boot_id":"boot-001","event_revision":11,"issued_at_unix_micros":1700000000000040,"schema_version":"control-event-cursor/v1","scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e","sources":[{"digest":"8888888888888888888888888888888888888888888888888888888888888888","revision":5,"source":"MODULES"},{"digest":"9999999999999999999999999999999999999999999999999999999999999999","revision":5,"source":"RUNS"}],"view_snapshot_digest":"9d8fbe3534ded272898a0c6f240c7168106f54078daa93fa9879a7823f9b511e"}`,
			wantID:   "941f199edeaf91d0ac11b388d0b71150022d31a104bb441a030a8db19e23659a",
			restore: func(wire []byte, digest string) error {
				_, err := RestoreControlEventCursorV1(wire, digest)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if string(test.canonical) != test.wantWire || test.digest != test.wantID {
				t.Errorf(
					"canary mismatch\nwire: %s\nid: %s",
					test.canonical,
					test.digest,
				)
			}
			if err := test.restore(bytes.Clone(test.canonical), test.digest); err != nil {
				t.Fatalf("restore exact canary: %v", err)
			}
			if err := test.restore(
				append(bytes.Clone(test.canonical), '\n'),
				test.digest,
			); err == nil {
				t.Fatal("restore accepted non-exact canonical bytes")
			}
			if err := test.restore(
				bytes.Clone(test.canonical),
				strings.Repeat("f", 64),
			); err == nil {
				t.Fatal("restore accepted wrong exact digest")
			}
		})
	}

	_ = session
	_ = view
	_ = statement
	_ = receipt
	_ = event
}

func TestConstructorsDefensivelyCopyCollectionsAndReferences(t *testing.T) {
	scope, _, _ := testScopeV1(t)

	capabilities := []ControlCapabilityV1{
		CapabilityOperateModulesV1,
		CapabilityObserveV1,
	}
	sessionInput := sessionInputV1(capabilities)
	frozenSession, sessionCanonical, _, err := NewControlSessionV1(sessionInput)
	if err != nil {
		t.Fatalf("NewControlSessionV1: %v", err)
	}
	capabilities[0] = CapabilityRunLearningV1
	if frozenSession.Capabilities[0] != CapabilityObserveV1 ||
		!bytes.Contains(sessionCanonical, []byte(`"OBSERVE"`)) {
		t.Fatal("session aliases caller capability storage")
	}

	sections := []ControlViewSectionV1{
		viewSectionV1(ViewSectionRunsV1, '7'),
		viewSectionV1(ViewSectionModulesV1, '6'),
	}
	viewInput := viewInputV1(scope, sections)
	frozenView, viewCanonical, _, err := NewControlViewSnapshotV1(viewInput)
	if err != nil {
		t.Fatalf("NewControlViewSnapshotV1: %v", err)
	}
	sections[0].SourceDigest = strings.Repeat("0", 64)
	if frozenView.Sections[1].SourceDigest != strings.Repeat("7", 64) ||
		bytes.Contains(viewCanonical, []byte(strings.Repeat("0", 64))) {
		t.Fatal("view aliases caller section storage")
	}

	sources := []ControlSourceRevisionV1{
		eventSourceV1(EventSourceRunsV1, '9'),
		eventSourceV1(EventSourceModulesV1, '8'),
	}
	eventInput := eventInputV1(
		strings.Repeat("1", 64),
		strings.Repeat("2", 64),
		sources,
	)
	frozenEvent, _, _, err := NewControlEventCursorV1(eventInput)
	if err != nil {
		t.Fatalf("NewControlEventCursorV1: %v", err)
	}
	sources[0].Digest = strings.Repeat("0", 64)
	if frozenEvent.Sources[1].Digest != strings.Repeat("9", 64) {
		t.Fatal("event cursor aliases caller source storage")
	}

	request, _, requestDigest := testRequestV1(t, scope)
	pre := request.ExpectedRef
	domain := DomainReceiptRefV1{
		Kind: DomainReceiptOutcomeUnknownV1,
		ID:   "unknown-001", Digest: strings.Repeat("e", 64),
	}
	receiptInput := unknownReceiptInputV1(request, requestDigest, &pre, &domain)
	frozenReceipt, receiptCanonical, _, err := NewControlOperationReceiptV1(receiptInput)
	if err != nil {
		t.Fatalf("NewControlOperationReceiptV1: %v", err)
	}
	pre.ResourceID = "changed"
	domain.ID = "changed"
	if frozenReceipt.PreRef.ResourceID != "tenant-a" ||
		frozenReceipt.DomainReceipt.ID != "unknown-001" ||
		bytes.Contains(receiptCanonical, []byte("changed")) {
		t.Fatal("operation receipt aliases caller reference storage")
	}
}

func TestControlViewSnapshotAcceptsWorkspacesSectionV1(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	frozen, _, _, err := NewControlViewSnapshotV1(viewInputV1(
		scope,
		[]ControlViewSectionV1{viewSectionV1(ViewSectionWorkspacesV1, '5')},
	))
	if err != nil {
		t.Fatalf("NewControlViewSnapshotV1 WORKSPACES: %v", err)
	}
	if len(frozen.Sections) != 1 || frozen.Sections[0].Kind != ViewSectionWorkspacesV1 {
		t.Fatalf("WORKSPACES section=%+v", frozen.Sections)
	}
}

func TestContractsRejectUnknownFieldsClosedEnumsAndInvalidBounds(t *testing.T) {
	_, scopeCanonical, _ := testScopeV1(t)
	unknown := bytes.Replace(
		scopeCanonical,
		[]byte(`{"kind":`),
		[]byte(`{"extra":true,"kind":`),
		1,
	)
	unknownDigest := moduleapi.Digest(controlScopeDigestDomainV1, unknown)
	if _, err := RestoreControlScopeV1(unknown, unknownDigest); err == nil {
		t.Fatal("scope restore accepted unknown field")
	}

	badScope := ControlScopeV1{
		SchemaVersion: ControlScopeSchemaVersionV1,
		Kind:          "GLOBAL", TenantID: "tenant-a",
	}
	if _, _, _, err := NewControlScopeV1(badScope); err == nil {
		t.Fatal("scope accepted open enum")
	}
	badScope = ControlScopeV1{
		SchemaVersion: ControlScopeSchemaVersionV1,
		Kind:          ScopeTenantV1,
		TenantID:      strings.Repeat("x", maxOpaqueIDBytesV1+1),
	}
	if _, _, _, err := NewControlScopeV1(badScope); err == nil {
		t.Fatal("scope accepted an oversized identity")
	}

	badSession := sessionInputV1([]ControlCapabilityV1{"ADMIN"})
	if _, _, _, err := NewControlSessionV1(badSession); err == nil {
		t.Fatal("session accepted open capability")
	}
	badSession = sessionInputV1([]ControlCapabilityV1{CapabilityObserveV1})
	badSession.ExpiresAtUnixMicros = badSession.IssuedAtUnixMicros +
		MaxControlSessionLifetimeMicrosV1 + 1
	if _, _, _, err := NewControlSessionV1(badSession); err == nil {
		t.Fatal("session accepted excessive lifetime")
	}

	if err := (PageQueryV1{Limit: MaxControlPageSizeV1 + 1}).Validate(); err == nil {
		t.Fatal("page query accepted excessive page size")
	}
	if err := (PageQueryV1{Limit: 1, After: &PageTokenRefV1{
		ScopeDigest: "event-cursor",
	}}).Validate(); err == nil {
		t.Fatal("page query accepted event cursor as page-token digest")
	}
	if err := (PageQueryV1{
		Limit: 1,
		After: &PageTokenRefV1{
			ScopeDigest:        strings.Repeat("1", 64),
			ViewSnapshotDigest: strings.Repeat("2", 64),
			Collection:         "UNKNOWN",
			PositionDigest:     strings.Repeat("3", 64),
		},
	}).Validate(); err == nil {
		t.Fatal("page query accepted an open collection enum")
	}
	if err := ErrorCodeV1("RETRY").Validate(); err == nil {
		t.Fatal("error code accepted an open enum")
	}
}

func TestViewAndEventCardinalityAndRevisionBounds(t *testing.T) {
	scope, _, scopeDigest := testScopeV1(t)
	sections := make([]ControlViewSectionV1, MaxControlViewSectionsV1+1)
	for index := range sections {
		sections[index] = viewSectionV1(ViewSectionRunsV1, '7')
	}
	if _, _, _, err := NewControlViewSnapshotV1(
		viewInputV1(scope, sections),
	); err == nil {
		t.Fatal("view accepted excessive section cardinality")
	}

	validView, _, viewDigest := testViewV1(t, scope)
	invalidView := validView
	invalidView.Basis.PointerRevision = maxSafeJSONIntegerV1 + 1
	if _, _, _, err := NewControlViewSnapshotV1(invalidView); err == nil {
		t.Fatal("view accepted a non-JSON-safe revision")
	}

	sources := make([]ControlSourceRevisionV1, MaxControlSourceRevisionsV1+1)
	for index := range sources {
		sources[index] = eventSourceV1(EventSourceRunsV1, '9')
	}
	if _, _, _, err := NewControlEventCursorV1(eventInputV1(
		scopeDigest,
		viewDigest,
		sources,
	)); err == nil {
		t.Fatal("event cursor accepted excessive source cardinality")
	}

	badTime := eventInputV1(
		scopeDigest,
		viewDigest,
		[]ControlSourceRevisionV1{eventSourceV1(EventSourceRunsV1, '9')},
	)
	badTime.IssuedAtUnixMicros = maxSafeJSONIntegerV1 + 1
	if _, _, _, err := NewControlEventCursorV1(badTime); err == nil {
		t.Fatal("event cursor accepted a non-JSON-safe timestamp")
	}
}

func TestViewSchemaCannotCollideWithAuthoritativeControlSnapshot(t *testing.T) {
	if ControlViewSnapshotSchemaVersionV1 == "control-snapshot/v1" {
		t.Fatal("sanitized Control API view collides with authoritative Core snapshot")
	}
}

func TestOperationRequestRequiresServerIdentityExactPreconditionAndDigests(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	input := requestInputV1(scope)

	bad := input
	bad.PrincipalID = ""
	if _, _, _, err := NewControlOperationRequestV1(bad); err == nil {
		t.Fatal("operation request accepted absent server principal")
	}
	bad = input
	bad.Capability = CapabilityObserveV1
	if _, _, _, err := NewControlOperationRequestV1(bad); err == nil {
		t.Fatal("operation request accepted capability that cannot authorize operation")
	}
	bad = input
	bad.ExpectedRef.Revision = 0
	if _, _, _, err := NewControlOperationRequestV1(bad); err == nil {
		t.Fatal("operation request accepted missing expected revision")
	}
	bad = input
	bad.IdempotencyKeyDigest = "raw-key"
	if _, _, _, err := NewControlOperationRequestV1(bad); err == nil {
		t.Fatal("operation request accepted raw idempotency key")
	}
	bad = input
	bad.ScopeDigest = strings.Repeat("0", 64)
	if _, _, _, err := NewControlOperationRequestV1(bad); err == nil {
		t.Fatal("operation request accepted scope digest drift")
	}
}

func TestConfirmationStatementRequiresExactStableMutationMeaning(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	request := requestInputV1(scope)
	input := confirmationStatementFromRequestV1(request)
	frozen, canonical, digest, err := NewControlConfirmationStatementV1(input)
	if err != nil {
		t.Fatalf("valid confirmation statement: %v", err)
	}
	input.Scope.TenantID = "changed-after-freeze"
	if frozen.Scope.TenantID != "tenant-a" ||
		bytes.Contains(canonical, []byte("changed-after-freeze")) {
		t.Fatal("confirmation statement aliases caller scope storage")
	}
	if _, err := RestoreControlConfirmationStatementV1(
		bytes.Clone(canonical),
		digest,
	); err != nil {
		t.Fatalf("restore exact confirmation statement: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(*ControlConfirmationStatementV1)
	}{
		{"schema", func(value *ControlConfirmationStatementV1) { value.SchemaVersion = "confirmation/v2" }},
		{"principal", func(value *ControlConfirmationStatementV1) { value.PrincipalID = "" }},
		{"capability", func(value *ControlConfirmationStatementV1) { value.Capability = CapabilityObserveV1 }},
		{"intent", func(value *ControlConfirmationStatementV1) { value.Intent = OperationIntentDryRunV1 }},
		{"operation", func(value *ControlConfirmationStatementV1) { value.Operation = "DELETE_ALL" }},
		{"absent scope digest", func(value *ControlConfirmationStatementV1) { value.ScopeDigest = "" }},
		{"scope digest", func(value *ControlConfirmationStatementV1) { value.ScopeDigest = strings.Repeat("0", 64) }},
		{"idempotency", func(value *ControlConfirmationStatementV1) { value.IdempotencyKeyDigest = "raw-key" }},
		{"input", func(value *ControlConfirmationStatementV1) { value.InputDigest = "body" }},
		{"evaluation", func(value *ControlConfirmationStatementV1) { value.OperationEvaluationDigest = "" }},
		{"expected kind", func(value *ControlConfirmationStatementV1) { value.ExpectedRef.Kind = ResourceModuleActivationV1 }},
		{"expected cross tenant", func(value *ControlConfirmationStatementV1) { value.ExpectedRef.ResourceID = "tenant-other" }},
		{"expected revision", func(value *ControlConfirmationStatementV1) { value.ExpectedRef.Revision = 0 }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := confirmationStatementFromRequestV1(request)
			test.mutate(&candidate)
			if _, _, _, err := NewControlConfirmationStatementV1(candidate); err == nil {
				t.Fatal("invalid confirmation statement was accepted")
			}
		})
	}

	typeOf := reflect.TypeOf(ControlConfirmationStatementV1{})
	for _, forbidden := range []string{
		"BootID", "SessionID", "AuthorizationRevision", "IssuedAtUnixMicros",
		"ExpiresAtUnixMicros", "Challenge", "Proof", "RawProof",
	} {
		if _, found := typeOf.FieldByName(forbidden); found {
			t.Fatalf("stable confirmation statement retained dynamic field %s", forbidden)
		}
	}
	for _, forbidden := range []string{
		"boot_id", "session_id", "authorization_revision", "issued_at_unix_micros",
		"expires_at_unix_micros", "challenge", "proof",
	} {
		if bytes.Contains(canonical, []byte(forbidden)) {
			t.Fatalf("stable confirmation canonical retained %s", forbidden)
		}
	}
}

func TestOperationRequestConfirmationDigestBindsEveryStableMutationFact(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	baseline := requestInputV1(scope)
	mutations := []struct {
		name   string
		mutate func(*ControlOperationRequestV1)
	}{
		{"principal", func(value *ControlOperationRequestV1) { value.PrincipalID = "operator-next" }},
		{"operation", func(value *ControlOperationRequestV1) { value.Operation = OperationModuleDisableV1 }},
		{"scope", func(value *ControlOperationRequestV1) {
			value.Scope.WorkspaceID = "workspace-next"
			_, _, value.ScopeDigest, _ = NewControlScopeV1(value.Scope)
		}},
		{"idempotency", func(value *ControlOperationRequestV1) { value.IdempotencyKeyDigest = strings.Repeat("1", 64) }},
		{"input", func(value *ControlOperationRequestV1) { value.InputDigest = strings.Repeat("2", 64) }},
		{"evaluation", func(value *ControlOperationRequestV1) { value.OperationEvaluationDigest = strings.Repeat("3", 64) }},
		{"expected identity", func(value *ControlOperationRequestV1) { value.ExpectedRef.ResourceID = "tenant-a-pointer-next" }},
		{"expected revision", func(value *ControlOperationRequestV1) { value.ExpectedRef.Revision++ }},
		{"expected digest", func(value *ControlOperationRequestV1) { value.ExpectedRef.Digest = strings.Repeat("4", 64) }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := baseline
			test.mutate(&candidate)
			if _, _, _, err := NewControlOperationRequestV1(candidate); err == nil {
				t.Fatal("request accepted stale confirmation digest after stable fact changed")
			}
		})
	}
}

func TestOperationRequestExpectedResourceAndZeroRevisionMatrix(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	tests := []struct {
		name       string
		operation  ControlOperationV1
		capability ControlCapabilityV1
		kind       ControlResourceKindV1
		allowZero  bool
	}{
		{
			name: "module apply", operation: OperationModuleApplyV1,
			capability: CapabilityOperateModulesV1,
			kind:       ResourcePublishedPointerV1,
		},
		{
			name:       "module disable uses published pointer",
			operation:  OperationModuleDisableV1,
			capability: CapabilityOperateModulesV1,
			kind:       ResourcePublishedPointerV1,
		},
		{
			name:       "module upgrade review creates review against published pointer",
			operation:  OperationModuleUpgradeReviewV1,
			capability: CapabilityOperateModulesV1,
			kind:       ResourcePublishedPointerV1,
		},
		{
			name: "module upgrade apply", operation: OperationModuleUpgradeApplyV1,
			capability: CapabilityOperateModulesV1,
			kind:       ResourcePublishedPointerV1,
		},
		{
			name: "learning proposal review", operation: OperationLearningProposalReviewV1,
			capability: CapabilityReviewLearningV1,
			kind:       ResourceLearningProposalV1,
			allowZero:  true,
		},
		{
			name: "learning cycle run", operation: OperationLearningCycleRunV1,
			capability: CapabilityRunLearningV1,
			kind:       ResourceLearningScheduleV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := requestInputV1(scope)
			input.Operation = test.operation
			input.Capability = test.capability
			if test.operation == OperationModuleDisableV1 {
				input.Scope.Kind = ScopeTenantV1
				input.Scope.WorkspaceID = ""
				input.ScopeDigest = ""
			}
			input.ExpectedRef.Kind = test.kind
			if test.kind == ResourcePublishedPointerV1 {
				input.ExpectedRef.ResourceID = input.Scope.TenantID
			} else {
				input.ExpectedRef.ResourceID = "resource-for-" + string(test.operation)
			}
			input.ExpectedRef.Revision = 1
			bindRequestConfirmationForTestV1(&input)
			if _, _, _, err := NewControlOperationRequestV1(input); err != nil {
				t.Fatalf("valid expected resource: %v", err)
			}

			zero := input
			zero.ExpectedRef.Revision = 0
			if test.allowZero {
				bindRequestConfirmationForTestV1(&zero)
			}
			_, _, _, zeroErr := NewControlOperationRequestV1(zero)
			if test.allowZero && zeroErr != nil {
				t.Fatalf("revision-zero Proposal was rejected: %v", zeroErr)
			}
			if !test.allowZero && zeroErr == nil {
				t.Fatal("non-Proposal operation accepted revision zero")
			}

			wrongKind := input
			wrongKind.ExpectedRef.Kind = ResourceModuleActivationV1
			if test.operation == OperationModuleUpgradeReviewV1 {
				wrongKind.ExpectedRef.Kind = ResourceModuleInstallationV1
			}
			if _, _, _, err := NewControlOperationRequestV1(wrongKind); err == nil {
				t.Fatal("operation accepted another expected resource kind")
			}
		})
	}
}

func TestOperationRequestIntentClosesIdempotencyAndConfirmationShapes(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	mutate := requestInputV1(scope)
	_, _, mutateDigest, err := NewControlOperationRequestV1(mutate)
	if err != nil {
		t.Fatalf("valid MUTATE request: %v", err)
	}

	dryRun := mutate
	dryRun.Intent = OperationIntentDryRunV1
	dryRun.IdempotencyKeyDigest = ""
	dryRun.OperationEvaluationDigest = ""
	dryRun.ConfirmationDigest = ""
	_, dryCanonical, dryDigest, err := NewControlOperationRequestV1(dryRun)
	if err != nil {
		t.Fatalf("valid DRY_RUN request: %v", err)
	}
	if bytes.Contains(dryCanonical, []byte("idempotency_key_digest")) ||
		bytes.Contains(dryCanonical, []byte("confirmation_digest")) ||
		bytes.Contains(dryCanonical, []byte("operation_evaluation_digest")) {
		t.Fatal("DRY_RUN canonical request retained absent mutation digests")
	}
	if dryDigest == mutateDigest {
		t.Fatal("DRY_RUN and MUTATE intents produced the same semantic digest")
	}

	tests := []struct {
		name   string
		mutate func(*ControlOperationRequestV1)
	}{
		{
			name: "open intent",
			mutate: func(value *ControlOperationRequestV1) {
				value.Intent = "INSPECT"
			},
		},
		{
			name: "dry-run key",
			mutate: func(value *ControlOperationRequestV1) {
				value.Intent = OperationIntentDryRunV1
				value.OperationEvaluationDigest = ""
				value.ConfirmationDigest = ""
			},
		},
		{
			name: "dry-run confirmation",
			mutate: func(value *ControlOperationRequestV1) {
				value.Intent = OperationIntentDryRunV1
				value.IdempotencyKeyDigest = ""
				value.OperationEvaluationDigest = ""
			},
		},
		{
			name: "mutate missing key",
			mutate: func(value *ControlOperationRequestV1) {
				value.IdempotencyKeyDigest = ""
			},
		},
		{
			name: "mutate missing evaluation",
			mutate: func(value *ControlOperationRequestV1) {
				value.OperationEvaluationDigest = ""
			},
		},
		{
			name: "mutate missing confirmation",
			mutate: func(value *ControlOperationRequestV1) {
				value.ConfirmationDigest = ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := mutate
			test.mutate(&candidate)
			if _, _, _, err := NewControlOperationRequestV1(candidate); err == nil {
				t.Fatal("invalid operation intent/digest shape was accepted")
			}
		})
	}

	drift := mutate
	drift.InputDigest = strings.Repeat("0", 64)
	if _, _, _, err := NewControlOperationRequestV1(drift); err == nil {
		t.Fatal("MUTATE request accepted a confirmation statement binding drift")
	}
}

func TestModuleDisableMutationRequiresTenantScopeWhileDryRunRemainsScopedV1(t *testing.T) {
	workspace, _, _ := testScopeV1(t)
	mutate := requestInputV1(workspace)
	mutate.Operation = OperationModuleDisableV1
	mutate.Capability = CapabilityOperateModulesV1
	mutate.ExpectedRef.ResourceID = workspace.TenantID
	// A stable confirmation statement is itself a mutation contract and must
	// reject Workspace scope before any proof can be issued.
	if _, _, _, err := NewControlConfirmationStatementV1(
		confirmationStatementFromRequestV1(mutate),
	); err == nil {
		t.Fatal("MODULE_DISABLE confirmation accepted Workspace scope")
	}

	dryRun := mutate
	dryRun.Intent = OperationIntentDryRunV1
	dryRun.IdempotencyKeyDigest = ""
	dryRun.OperationEvaluationDigest = ""
	dryRun.ConfirmationDigest = ""
	if _, _, _, err := NewControlOperationRequestV1(dryRun); err != nil {
		t.Fatalf("effect-free Workspace Disable Dry-run contract changed: %v", err)
	}

	tenant := workspace
	tenant.Kind = ScopeTenantV1
	tenant.WorkspaceID = ""
	mutate.Scope = tenant
	mutate.ScopeDigest = ""
	bindRequestConfirmationForTestV1(&mutate)
	if _, _, _, err := NewControlOperationRequestV1(mutate); err != nil {
		t.Fatalf("Tenant MODULE_DISABLE mutation contract rejected: %v", err)
	}
}

func TestOperationRequestSemanticWireExcludesDynamicTransportMetadata(t *testing.T) {
	typeOf := reflect.TypeOf(ControlOperationRequestV1{})
	for _, forbidden := range []string{
		"RequestID", "RequestedAtUnixMicros", "BootID", "SessionID",
		"AuthorizationRevision", "IssuedAtUnixMicros", "ExpiresAtUnixMicros",
		"Challenge", "Proof", "RawProof",
	} {
		if _, found := typeOf.FieldByName(forbidden); found {
			t.Fatalf("semantic request retained dynamic field %s", forbidden)
		}
	}
	scope, _, _ := testScopeV1(t)
	input := requestInputV1(scope)
	_, canonical, digest, err := NewControlOperationRequestV1(input)
	if err != nil {
		t.Fatalf("NewControlOperationRequestV1: %v", err)
	}
	for _, forbidden := range []string{
		"request_id", "requested_at_unix_micros", "boot_id", "session_id",
		"authorization_revision", "issued_at_unix_micros", "expires_at_unix_micros",
		"challenge", "proof",
	} {
		if bytes.Contains(canonical, []byte(forbidden)) {
			t.Fatalf("semantic canonical request retained %s", forbidden)
		}
	}

	transportAttempts := []struct {
		requestID string
		arrivedAt uint64
	}{
		{requestID: "transport-a", arrivedAt: 1},
		{requestID: "transport-b", arrivedAt: maxSafeJSONIntegerV1},
	}
	for _, attempt := range transportAttempts {
		if attempt.requestID == "" || attempt.arrivedAt == 0 {
			t.Fatal("invalid test transport metadata")
		}
		_, rebuilt, rebuiltDigest, err := NewControlOperationRequestV1(input)
		if err != nil {
			t.Fatalf("rebuild semantic request: %v", err)
		}
		if !bytes.Equal(rebuilt, canonical) || rebuiltDigest != digest {
			t.Fatal("transport metadata changed semantic request identity")
		}
	}
}

func TestUnknownReceiptCanOnlyReturnExactUnknownWithoutSemanticReplay(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	request, _, requestDigest := testRequestV1(t, scope)
	pre := request.ExpectedRef
	domain := DomainReceiptRefV1{
		Kind: DomainReceiptOutcomeUnknownV1,
		ID:   "unknown-001", Digest: strings.Repeat("e", 64),
	}
	input := unknownReceiptInputV1(request, requestDigest, &pre, &domain)

	mutations := []func(*ControlOperationReceiptV1){
		func(value *ControlOperationReceiptV1) {
			value.Intent = OperationIntentDryRunV1
			value.IdempotencyKeyDigest = ""
		},
		func(value *ControlOperationReceiptV1) {
			value.PreRef = nil
		},
		func(value *ControlOperationReceiptV1) {
			value.ReplayDisposition = ReplayReturnExactReceiptV1
		},
		func(value *ControlOperationReceiptV1) {
			value.DomainReceipt.Kind = DomainReceiptModuleApplyV1
		},
		func(value *ControlOperationReceiptV1) {
			value.DomainReceipt = nil
		},
		func(value *ControlOperationReceiptV1) {
			post := expectedPostForTestV1(*value)
			value.PostRef = &post
		},
		func(value *ControlOperationReceiptV1) {
			value.ErrorCode = ErrorInternalV1
		},
	}
	for index, mutate := range mutations {
		candidate := input
		preCopy := *input.PreRef
		domainCopy := *input.DomainReceipt
		candidate.PreRef = &preCopy
		candidate.DomainReceipt = &domainCopy
		mutate(&candidate)
		if _, _, _, err := NewControlOperationReceiptV1(candidate); err == nil {
			t.Fatalf("UNKNOWN mutation %d was accepted", index)
		}
	}
}

func TestReceiptStatusIntentAndKeyMatrix(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	mutateRequest, _, mutateDigest := testRequestV1(t, scope)
	pre := mutateRequest.ExpectedRef

	mutateRejected := ControlOperationReceiptV1{
		SchemaVersion:         ControlOperationReceiptSchemaVersionV1,
		RequestDigest:         mutateDigest,
		Intent:                OperationIntentMutateV1,
		IdempotencyKeyDigest:  mutateRequest.IdempotencyKeyDigest,
		PrincipalID:           mutateRequest.PrincipalID,
		ScopeDigest:           mutateRequest.ScopeDigest,
		Operation:             mutateRequest.Operation,
		Status:                OperationStatusRejectedV1,
		ErrorCode:             ErrorRevisionConflictV1,
		PreRef:                &pre,
		ReplayDisposition:     ReplayNoRetryV1,
		CompletedAtUnixMicros: testMicrosV1 + 30,
	}
	if _, _, _, err := NewControlOperationReceiptV1(mutateRejected); err != nil {
		t.Fatalf("valid MUTATE REJECTED receipt: %v", err)
	}

	dryRequestInput := requestInputV1(scope)
	dryRequestInput.Intent = OperationIntentDryRunV1
	dryRequestInput.IdempotencyKeyDigest = ""
	dryRequestInput.OperationEvaluationDigest = ""
	dryRequestInput.ConfirmationDigest = ""
	dryRequest, _, dryDigest, err := NewControlOperationRequestV1(dryRequestInput)
	if err != nil {
		t.Fatalf("valid DRY_RUN request: %v", err)
	}
	dryPre := dryRequest.ExpectedRef
	dryRejected := mutateRejected
	dryRejected.RequestDigest = dryDigest
	dryRejected.Intent = OperationIntentDryRunV1
	dryRejected.IdempotencyKeyDigest = ""
	dryRejected.PreRef = &dryPre
	if _, _, _, err := NewControlOperationReceiptV1(dryRejected); err != nil {
		t.Fatalf("valid DRY_RUN REJECTED receipt: %v", err)
	}

	tests := []struct {
		name    string
		receipt ControlOperationReceiptV1
	}{
		{
			name: "dry-run intent carrying mutate key",
			receipt: func() ControlOperationReceiptV1 {
				value := dryRejected
				value.IdempotencyKeyDigest = strings.Repeat("8", 64)
				return value
			}(),
		},
		{
			name: "mutate intent missing key",
			receipt: func() ControlOperationReceiptV1 {
				value := mutateRejected
				value.IdempotencyKeyDigest = ""
				return value
			}(),
		},
		{
			name: "outcome unknown disguised as rejected",
			receipt: func() ControlOperationReceiptV1 {
				value := mutateRejected
				value.ErrorCode = ErrorOutcomeUnknownV1
				return value
			}(),
		},
		{
			name: "rejected carrying a post effect",
			receipt: func() ControlOperationReceiptV1 {
				value := mutateRejected
				post := pre
				post.Revision++
				post.Digest = strings.Repeat("d", 64)
				value.PostRef = &post
				return value
			}(),
		},
		{
			name: "rejected carrying a domain effect",
			receipt: func() ControlOperationReceiptV1 {
				value := mutateRejected
				value.DomainReceipt = &DomainReceiptRefV1{
					Kind: DomainReceiptModuleApplyV1,
					ID:   "unexpected-applied-domain",
					Digest: strings.Repeat(
						"c",
						64,
					),
				}
				return value
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := NewControlOperationReceiptV1(test.receipt); err == nil {
				t.Fatal("invalid receipt intent/status/key matrix was accepted")
			}
		})
	}
}

func TestAppliedReceiptOperationTransitionMatrix(t *testing.T) {
	pointerPre := operationResourceRefV1(
		ResourcePublishedPointerV1,
		"tenant-a-pointer",
		9,
		'a',
	)
	pointerPost := operationResourceRefV1(
		ResourcePublishedPointerV1,
		"tenant-a-pointer",
		10,
		'd',
	)
	proposalZero := operationResourceRefV1(
		ResourceLearningProposalV1,
		"proposal-a",
		0,
		'a',
	)
	proposalOne := operationResourceRefV1(
		ResourceLearningProposalV1,
		"proposal-a",
		1,
		'b',
	)
	proposalTwo := operationResourceRefV1(
		ResourceLearningProposalV1,
		"proposal-a",
		2,
		'c',
	)
	proposalThree := operationResourceRefV1(
		ResourceLearningProposalV1,
		"proposal-a",
		3,
		'd',
	)
	schedulePre := operationResourceRefV1(
		ResourceLearningScheduleV1,
		"schedule-a",
		4,
		'a',
	)
	schedulePost := operationResourceRefV1(
		ResourceLearningScheduleV1,
		"schedule-a",
		5,
		'd',
	)

	tests := []struct {
		name      string
		operation ControlOperationV1
		pre       ExpectedResourceRefV1
		post      ExpectedResourceRefV1
		domain    DomainReceiptKindV1
	}{
		{
			name:      "module apply advances pointer",
			operation: OperationModuleApplyV1, pre: pointerPre, post: pointerPost,
			domain: DomainReceiptModuleApplyV1,
		},
		{
			name:      "module disable advances pointer",
			operation: OperationModuleDisableV1, pre: pointerPre, post: pointerPost,
			domain: DomainReceiptModuleDisableV1,
		},
		{
			name:      "module upgrade review preserves pointer",
			operation: OperationModuleUpgradeReviewV1, pre: pointerPre, post: pointerPre,
			domain: DomainReceiptModuleReviewV1,
		},
		{
			name:      "module upgrade apply advances pointer",
			operation: OperationModuleUpgradeApplyV1, pre: pointerPre, post: pointerPost,
			domain: DomainReceiptModuleApplyV1,
		},
		{
			name:      "new learning review reaches terminal revision",
			operation: OperationLearningProposalReviewV1, pre: proposalZero, post: proposalTwo,
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name:      "pending learning review reaches terminal revision",
			operation: OperationLearningProposalReviewV1, pre: proposalOne, post: proposalTwo,
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name:      "unknown learning review reconciles original attempt",
			operation: OperationLearningProposalReviewV1, pre: proposalTwo, post: proposalThree,
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name:      "new learning cycle window advances schedule",
			operation: OperationLearningCycleRunV1, pre: schedulePre, post: schedulePost,
			domain: DomainReceiptLearningCycleV1,
		},
		{
			name:      "open learning cycle task preserves schedule",
			operation: OperationLearningCycleRunV1, pre: schedulePre, post: schedulePre,
			domain: DomainReceiptLearningCycleV1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := appliedReceiptInputV1(
				test.operation,
				test.pre,
				test.post,
				test.domain,
			)
			if _, _, _, err := NewControlOperationReceiptV1(input); err != nil {
				t.Fatalf("valid APPLIED transition: %v", err)
			}

			wrongIntent := cloneOperationReceiptRefsV1(input)
			wrongIntent.Intent = OperationIntentDryRunV1
			wrongIntent.IdempotencyKeyDigest = ""
			if _, _, _, err := NewControlOperationReceiptV1(wrongIntent); err == nil {
				t.Fatal("APPLIED receipt accepted DRY_RUN intent")
			}

			wrongDomain := cloneOperationReceiptRefsV1(input)
			wrongDomain.DomainReceipt.Kind = DomainReceiptOutcomeUnknownV1
			if _, _, _, err := NewControlOperationReceiptV1(wrongDomain); err == nil {
				t.Fatal("APPLIED receipt accepted another domain receipt kind")
			}

			wrongResource := cloneOperationReceiptRefsV1(input)
			wrongResource.PostRef.ResourceID += "-other"
			if _, _, _, err := NewControlOperationReceiptV1(wrongResource); err == nil {
				t.Fatal("APPLIED receipt accepted another post resource identity")
			}
		})
	}
}

func TestAppliedReceiptRejectsInvalidOperationTransitions(t *testing.T) {
	pointer := operationResourceRefV1(
		ResourcePublishedPointerV1,
		"tenant-a-pointer",
		9,
		'a',
	)
	proposal := operationResourceRefV1(
		ResourceLearningProposalV1,
		"proposal-a",
		0,
		'a',
	)
	schedule := operationResourceRefV1(
		ResourceLearningScheduleV1,
		"schedule-a",
		4,
		'a',
	)
	tests := []struct {
		name      string
		operation ControlOperationV1
		pre       ExpectedResourceRefV1
		post      ExpectedResourceRefV1
		domain    DomainReceiptKindV1
	}{
		{
			name: "module apply exact basis", operation: OperationModuleApplyV1,
			pre: pointer, post: pointer, domain: DomainReceiptModuleApplyV1,
		},
		{
			name: "module disable skips revision", operation: OperationModuleDisableV1,
			pre: pointer,
			post: operationResourceRefV1(
				ResourcePublishedPointerV1,
				"tenant-a-pointer",
				11,
				'd',
			),
			domain: DomainReceiptModuleDisableV1,
		},
		{
			name: "module upgrade apply retains digest", operation: OperationModuleUpgradeApplyV1,
			pre: pointer,
			post: operationResourceRefV1(
				ResourcePublishedPointerV1,
				"tenant-a-pointer",
				10,
				'a',
			),
			domain: DomainReceiptModuleApplyV1,
		},
		{
			name: "module upgrade review advances pointer", operation: OperationModuleUpgradeReviewV1,
			pre: pointer,
			post: operationResourceRefV1(
				ResourcePublishedPointerV1,
				"tenant-a-pointer",
				10,
				'd',
			),
			domain: DomainReceiptModuleReviewV1,
		},
		{
			name: "module upgrade review changes basis digest", operation: OperationModuleUpgradeReviewV1,
			pre: pointer,
			post: operationResourceRefV1(
				ResourcePublishedPointerV1,
				"tenant-a-pointer",
				9,
				'd',
			),
			domain: DomainReceiptModuleReviewV1,
		},
		{
			name: "new learning review stops at admission", operation: OperationLearningProposalReviewV1,
			pre: proposal,
			post: operationResourceRefV1(
				ResourceLearningProposalV1,
				"proposal-a",
				1,
				'd',
			),
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name: "new learning review skips terminal revision", operation: OperationLearningProposalReviewV1,
			pre: proposal,
			post: operationResourceRefV1(
				ResourceLearningProposalV1,
				"proposal-a",
				3,
				'd',
			),
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name: "learning review retains digest", operation: OperationLearningProposalReviewV1,
			pre: proposal,
			post: operationResourceRefV1(
				ResourceLearningProposalV1,
				"proposal-a",
				2,
				'a',
			),
			domain: DomainReceiptLearningReviewV1,
		},
		{
			name: "learning cycle skips schedule revision", operation: OperationLearningCycleRunV1,
			pre: schedule,
			post: operationResourceRefV1(
				ResourceLearningScheduleV1,
				"schedule-a",
				6,
				'd',
			),
			domain: DomainReceiptLearningCycleV1,
		},
		{
			name: "learning cycle changes digest without revision", operation: OperationLearningCycleRunV1,
			pre: schedule,
			post: operationResourceRefV1(
				ResourceLearningScheduleV1,
				"schedule-a",
				4,
				'd',
			),
			domain: DomainReceiptLearningCycleV1,
		},
		{
			name: "learning cycle adjacent revision retains digest", operation: OperationLearningCycleRunV1,
			pre: schedule,
			post: operationResourceRefV1(
				ResourceLearningScheduleV1,
				"schedule-a",
				5,
				'a',
			),
			domain: DomainReceiptLearningCycleV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := appliedReceiptInputV1(
				test.operation,
				test.pre,
				test.post,
				test.domain,
			)
			if _, _, _, err := NewControlOperationReceiptV1(input); err == nil {
				t.Fatal("invalid APPLIED transition was accepted")
			}
		})
	}
}

func operationResourceRefV1(
	kind ControlResourceKindV1,
	resourceID string,
	revision uint64,
	digestDigit byte,
) ExpectedResourceRefV1 {
	return ExpectedResourceRefV1{
		Kind:       kind,
		ResourceID: resourceID,
		Revision:   revision,
		Digest:     strings.Repeat(string([]byte{digestDigit}), 64),
	}
}

func appliedReceiptInputV1(
	operation ControlOperationV1,
	pre ExpectedResourceRefV1,
	post ExpectedResourceRefV1,
	domainKind DomainReceiptKindV1,
) ControlOperationReceiptV1 {
	return ControlOperationReceiptV1{
		SchemaVersion:        ControlOperationReceiptSchemaVersionV1,
		RequestDigest:        strings.Repeat("1", 64),
		Intent:               OperationIntentMutateV1,
		IdempotencyKeyDigest: strings.Repeat("2", 64),
		PrincipalID:          "operator-local",
		ScopeDigest:          strings.Repeat("3", 64),
		Operation:            operation,
		Status:               OperationStatusAppliedV1,
		ErrorCode:            ErrorNoneV1,
		PreRef:               &pre,
		PostRef:              &post,
		DomainReceipt: &DomainReceiptRefV1{
			Kind:   domainKind,
			ID:     "domain-receipt-for-" + string(operation),
			Digest: strings.Repeat("4", 64),
		},
		ReplayDisposition:     ReplayReturnExactReceiptV1,
		CompletedAtUnixMicros: testMicrosV1 + 30,
	}
}

func cloneOperationReceiptRefsV1(
	input ControlOperationReceiptV1,
) ControlOperationReceiptV1 {
	cloned := input
	if input.PreRef != nil {
		pre := *input.PreRef
		cloned.PreRef = &pre
	}
	if input.PostRef != nil {
		post := *input.PostRef
		cloned.PostRef = &post
	}
	if input.DomainReceipt != nil {
		domain := *input.DomainReceipt
		cloned.DomainReceipt = &domain
	}
	return cloned
}

func TestDryRunAndNoChangeReceiptsRemainEffectFree(t *testing.T) {
	scope, _, _ := testScopeV1(t)
	mutateRequest, _, mutateRequestDigest := testRequestV1(t, scope)
	pre := mutateRequest.ExpectedRef
	mutateBase := ControlOperationReceiptV1{
		SchemaVersion:         ControlOperationReceiptSchemaVersionV1,
		RequestDigest:         mutateRequestDigest,
		Intent:                mutateRequest.Intent,
		IdempotencyKeyDigest:  mutateRequest.IdempotencyKeyDigest,
		PrincipalID:           mutateRequest.PrincipalID,
		ScopeDigest:           mutateRequest.ScopeDigest,
		Operation:             mutateRequest.Operation,
		ErrorCode:             ErrorNoneV1,
		PreRef:                &pre,
		CompletedAtUnixMicros: testMicrosV1 + 30,
	}

	dryRequestInput := requestInputV1(scope)
	dryRequestInput.Intent = OperationIntentDryRunV1
	dryRequestInput.IdempotencyKeyDigest = ""
	dryRequestInput.OperationEvaluationDigest = ""
	dryRequestInput.ConfirmationDigest = ""
	dryRequest, _, dryRequestDigest, err := NewControlOperationRequestV1(
		dryRequestInput,
	)
	if err != nil {
		t.Fatalf("valid DRY_RUN request: %v", err)
	}
	dryPre := dryRequest.ExpectedRef
	dryRun := ControlOperationReceiptV1{
		SchemaVersion:         ControlOperationReceiptSchemaVersionV1,
		RequestDigest:         dryRequestDigest,
		Intent:                dryRequest.Intent,
		PrincipalID:           dryRequest.PrincipalID,
		ScopeDigest:           dryRequest.ScopeDigest,
		Operation:             dryRequest.Operation,
		ErrorCode:             ErrorNoneV1,
		PreRef:                &dryPre,
		CompletedAtUnixMicros: testMicrosV1 + 30,
	}
	dryRun.Status = OperationStatusDryRunV1
	dryRun.ReplayDisposition = ReplayNoRetryV1
	if _, _, _, err := NewControlOperationReceiptV1(dryRun); err != nil {
		t.Fatalf("valid DRY_RUN receipt: %v", err)
	}
	dryRunWrongIntent := dryRun
	dryRunWrongIntent.Intent = OperationIntentMutateV1
	dryRunWrongIntent.IdempotencyKeyDigest = strings.Repeat("8", 64)
	if _, _, _, err := NewControlOperationReceiptV1(dryRunWrongIntent); err == nil {
		t.Fatal("DRY_RUN receipt status accepted MUTATE intent")
	}
	dryRunWithEffect := dryRun
	domain := DomainReceiptRefV1{
		Kind:   DomainReceiptModuleApplyV1,
		ID:     "unexpected-effect",
		Digest: strings.Repeat("c", 64),
	}
	dryRunWithEffect.DomainReceipt = &domain
	if _, _, _, err := NewControlOperationReceiptV1(dryRunWithEffect); err == nil {
		t.Fatal("DRY_RUN receipt accepted a domain effect")
	}

	noChange := mutateBase
	post := pre
	noChange.Status = OperationStatusNoChangeV1
	noChange.PostRef = &post
	noChange.ReplayDisposition = ReplayReturnExactReceiptV1
	if _, _, _, err := NewControlOperationReceiptV1(noChange); err != nil {
		t.Fatalf("valid NO_CHANGE receipt: %v", err)
	}
	noChangeWrongIntent := noChange
	noChangeWrongIntent.Intent = OperationIntentDryRunV1
	noChangeWrongIntent.IdempotencyKeyDigest = ""
	if _, _, _, err := NewControlOperationReceiptV1(noChangeWrongIntent); err == nil {
		t.Fatal("NO_CHANGE receipt accepted DRY_RUN intent")
	}
	changed := noChange
	changedPost := post
	changedPost.Revision++
	changed.PostRef = &changedPost
	if _, _, _, err := NewControlOperationReceiptV1(changed); err == nil {
		t.Fatal("NO_CHANGE receipt accepted different pre/post refs")
	}
	noChangeWithEffect := noChange
	noChangeWithEffect.DomainReceipt = &domain
	if _, _, _, err := NewControlOperationReceiptV1(noChangeWithEffect); err == nil {
		t.Fatal("NO_CHANGE receipt accepted a domain effect")
	}
}

func expectedPostForTestV1(
	receipt ControlOperationReceiptV1,
) ExpectedResourceRefV1 {
	if receipt.PreRef == nil {
		return ExpectedResourceRefV1{}
	}
	post := *receipt.PreRef
	post.Revision++
	post.Digest = strings.Repeat("d", 64)
	return post
}

func TestWireShapesContainNoSecretBodyPathOrURLMaterial(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ControlSessionV1{}),
		reflect.TypeOf(ControlScopeV1{}),
		reflect.TypeOf(ControlViewSnapshotV1{}),
		reflect.TypeOf(ControlConfirmationStatementV1{}),
		reflect.TypeOf(ControlOperationRequestV1{}),
		reflect.TypeOf(ControlOperationReceiptV1{}),
		reflect.TypeOf(ControlEventCursorV1{}),
	}
	seen := make(map[reflect.Type]struct{})
	for _, contract := range types {
		assertSafeWireTypeV1(t, contract, seen)
	}
}

func assertSafeWireTypeV1(
	t *testing.T,
	typeOf reflect.Type,
	seen map[reflect.Type]struct{},
) {
	t.Helper()
	for typeOf.Kind() == reflect.Pointer || typeOf.Kind() == reflect.Slice {
		typeOf = typeOf.Elem()
	}
	if typeOf.Kind() != reflect.Struct {
		return
	}
	if _, present := seen[typeOf]; present {
		return
	}
	seen[typeOf] = struct{}{}
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		name := strings.ToLower(strings.Split(field.Tag.Get("json"), ",")[0])
		for _, forbidden := range []string{
			"secret", "cookie", "password", "body", "text", "path", "url",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("wire field %s.%s exposes forbidden material", typeOf, field.Name)
			}
		}
		if field.Type == reflect.TypeOf([]byte(nil)) {
			t.Fatalf("wire field %s.%s exposes raw bytes", typeOf, field.Name)
		}
		assertSafeWireTypeV1(t, field.Type, seen)
	}
}

func testScopeV1(t *testing.T) (ControlScopeV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewControlScopeV1(ControlScopeV1{
		SchemaVersion: ControlScopeSchemaVersionV1,
		Kind:          ScopeWorkspaceV1, TenantID: "tenant-a", WorkspaceID: "workspace-a",
	})
	if err != nil {
		t.Fatalf("NewControlScopeV1: %v", err)
	}
	return frozen, canonical, digest
}

func sessionInputV1(capabilities []ControlCapabilityV1) ControlSessionV1 {
	return ControlSessionV1{
		SchemaVersion: ControlSessionSchemaVersionV1,
		BootID:        "boot-001", SessionID: "session-001", PrincipalID: "operator-local",
		Capabilities:   capabilities,
		ScopeSetDigest: strings.Repeat("1", 64), AuthorizationRevision: 3,
		IssuedAtUnixMicros:  testMicrosV1,
		ExpiresAtUnixMicros: testMicrosV1 + 3_600_000_000,
	}
}

func testSessionV1(t *testing.T) (ControlSessionV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewControlSessionV1(sessionInputV1(
		[]ControlCapabilityV1{CapabilityOperateModulesV1, CapabilityObserveV1},
	))
	if err != nil {
		t.Fatalf("NewControlSessionV1: %v", err)
	}
	return frozen, canonical, digest
}

func viewSectionV1(kind ControlViewSectionKindV1, digit byte) ControlViewSectionV1 {
	return ControlViewSectionV1{
		Kind: kind, SourceRevision: 4,
		SourceDigest: strings.Repeat(string([]byte{digit}), 64), ItemCount: 2,
	}
}

func viewInputV1(
	scope ControlScopeV1,
	sections []ControlViewSectionV1,
) ControlViewSnapshotV1 {
	return ControlViewSnapshotV1{
		SchemaVersion: ControlViewSnapshotSchemaVersionV1,
		Scope:         scope, ObservedAtUnixMicros: testMicrosV1 + 10,
		Basis: PublishedBasisRefV1{
			TenantID: scope.TenantID, PointerRevision: 9,
			Control: RevisionedDigestRefV1{
				ID: "control-009", Revision: 7, Digest: strings.Repeat("2", 64),
			},
			Catalog: RevisionedDigestRefV1{
				ID: "catalog-009", Revision: 8, Digest: strings.Repeat("3", 64),
			},
		},
		Sections: sections,
	}
}

func testViewV1(
	t *testing.T,
	scope ControlScopeV1,
) (ControlViewSnapshotV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewControlViewSnapshotV1(viewInputV1(
		scope,
		[]ControlViewSectionV1{
			viewSectionV1(ViewSectionRunsV1, '7'),
			viewSectionV1(ViewSectionModulesV1, '6'),
		},
	))
	if err != nil {
		t.Fatalf("NewControlViewSnapshotV1: %v", err)
	}
	return frozen, canonical, digest
}

func requestInputV1(scope ControlScopeV1) ControlOperationRequestV1 {
	input := ControlOperationRequestV1{
		SchemaVersion: ControlOperationRequestSchemaVersionV1,
		PrincipalID:   "operator-local",
		Capability:    CapabilityOperateModulesV1, Scope: scope,
		Operation:                 OperationModuleUpgradeApplyV1,
		Intent:                    OperationIntentMutateV1,
		IdempotencyKeyDigest:      strings.Repeat("8", 64),
		InputDigest:               strings.Repeat("9", 64),
		OperationEvaluationDigest: strings.Repeat("c", 64),
		ExpectedRef: ExpectedResourceRefV1{
			Kind: ResourcePublishedPointerV1, ResourceID: "tenant-a",
			Revision: 9, Digest: strings.Repeat("a", 64),
		},
	}
	bindRequestConfirmationForTestV1(&input)
	return input
}

func bindRequestConfirmationForTestV1(input *ControlOperationRequestV1) {
	_, _, scopeDigest, err := NewControlScopeV1(input.Scope)
	if err != nil {
		panic(err)
	}
	input.ScopeDigest = scopeDigest
	_, _, digest, err := NewControlConfirmationStatementV1(
		confirmationStatementFromRequestV1(*input),
	)
	if err != nil {
		panic(err)
	}
	input.ConfirmationDigest = digest
}

func testConfirmationStatementV1(
	t *testing.T,
	scope ControlScopeV1,
) (ControlConfirmationStatementV1, []byte, string) {
	t.Helper()
	request := requestInputV1(scope)
	frozen, canonical, digest, err := NewControlConfirmationStatementV1(
		confirmationStatementFromRequestV1(request),
	)
	if err != nil {
		t.Fatalf("NewControlConfirmationStatementV1: %v", err)
	}
	return frozen, canonical, digest
}

func testRequestV1(
	t *testing.T,
	scope ControlScopeV1,
) (ControlOperationRequestV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewControlOperationRequestV1(
		requestInputV1(scope),
	)
	if err != nil {
		t.Fatalf("NewControlOperationRequestV1: %v", err)
	}
	return frozen, canonical, digest
}

func unknownReceiptInputV1(
	request ControlOperationRequestV1,
	requestDigest string,
	pre *ExpectedResourceRefV1,
	domain *DomainReceiptRefV1,
) ControlOperationReceiptV1 {
	return ControlOperationReceiptV1{
		SchemaVersion:         ControlOperationReceiptSchemaVersionV1,
		RequestDigest:         requestDigest,
		Intent:                request.Intent,
		IdempotencyKeyDigest:  request.IdempotencyKeyDigest,
		PrincipalID:           request.PrincipalID,
		ScopeDigest:           request.ScopeDigest,
		Operation:             request.Operation,
		Status:                OperationStatusUnknownV1,
		ErrorCode:             ErrorOutcomeUnknownV1,
		PreRef:                pre,
		DomainReceipt:         domain,
		ReplayDisposition:     ReplayExactReceiptOnlyNoReplayV1,
		CompletedAtUnixMicros: testMicrosV1 + 30,
	}
}

func testUnknownReceiptV1(
	t *testing.T,
	request ControlOperationRequestV1,
	requestDigest string,
) (ControlOperationReceiptV1, []byte, string) {
	t.Helper()
	pre := request.ExpectedRef
	domain := DomainReceiptRefV1{
		Kind: DomainReceiptOutcomeUnknownV1,
		ID:   "unknown-001", Digest: strings.Repeat("e", 64),
	}
	frozen, canonical, digest, err := NewControlOperationReceiptV1(
		unknownReceiptInputV1(request, requestDigest, &pre, &domain),
	)
	if err != nil {
		t.Fatalf("NewControlOperationReceiptV1: %v", err)
	}
	return frozen, canonical, digest
}

func eventSourceV1(
	source ControlEventSourceV1,
	digit byte,
) ControlSourceRevisionV1 {
	return ControlSourceRevisionV1{
		Source: source, Revision: 5,
		Digest: strings.Repeat(string([]byte{digit}), 64),
	}
}

func eventInputV1(
	scopeDigest string,
	viewDigest string,
	sources []ControlSourceRevisionV1,
) ControlEventCursorV1 {
	return ControlEventCursorV1{
		SchemaVersion: ControlEventCursorSchemaVersionV1,
		BootID:        "boot-001", ScopeDigest: scopeDigest,
		EventRevision: 11, ViewSnapshotDigest: viewDigest,
		Sources: sources, IssuedAtUnixMicros: testMicrosV1 + 40,
	}
}

func testEventV1(
	t *testing.T,
	scopeDigest string,
	viewDigest string,
) (ControlEventCursorV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := NewControlEventCursorV1(eventInputV1(
		scopeDigest,
		viewDigest,
		[]ControlSourceRevisionV1{
			eventSourceV1(EventSourceRunsV1, '9'),
			eventSourceV1(EventSourceModulesV1, '8'),
		},
	))
	if err != nil {
		t.Fatalf("NewControlEventCursorV1: %v", err)
	}
	return frozen, canonical, digest
}
