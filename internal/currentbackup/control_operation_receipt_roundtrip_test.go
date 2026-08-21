package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestControlOperationReceiptWholeBundleRoundTrip(t *testing.T) {
	fixture := newBackupFixture(t)
	want := seedNoChangeControlOperationReceipt(t, fixture.databasePath)

	bundle := filepath.Join(t.TempDir(), "receipt.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-receipt-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	if _, err := VerifyBundle(context.Background(), bundle); err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.ResolveControlOperationReceiptV1(
		context.Background(),
		want.RequestCanonical,
		want.RequestDigest,
	)
	if err != nil {
		t.Fatalf("ResolveControlOperationReceiptV1() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored durable receipt differs:\ngot=%#v\nwant=%#v", got, want)
	}
}

func TestAppliedControlOperationReceiptWholeBundleRoundTripV1(t *testing.T) {
	fixture := newBackupFixture(t)
	want := seedAppliedControlOperationReceiptV1(t, fixture.databasePath)
	if want.Status != controlapicontract.OperationStatusAppliedV1 ||
		want.DomainReceipt == nil || want.DomainReceiptRef == nil ||
		want.PreBasis == want.PostBasis {
		t.Fatalf("seeded APPLIED receipt=%+v", want)
	}

	bundle := filepath.Join(t.TempDir(), "applied-receipt.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-applied-receipt-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle() error = %v", err)
	}
	if _, err := VerifyBundle(context.Background(), bundle); err != nil {
		t.Fatalf("VerifyBundle() error = %v", err)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle() error = %v", err)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.ResolveControlOperationReceiptV1(
		context.Background(),
		bytes.Clone(want.RequestCanonical),
		want.RequestDigest,
	)
	if err != nil {
		t.Fatalf("ResolveControlOperationReceiptV1() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored APPLIED receipt differs:\ngot=%#v\nwant=%#v", got, want)
	}

	retried, created, err := store.CommitModuleDisableControlOperationV1(
		context.Background(),
		currentstore.CommitModuleDisableControlOperationInputV1{
			RequestCanonical: bytes.Clone(want.RequestCanonical),
			RequestDigest:    want.RequestDigest,
			// Exact durable lookup must precede all malformed fresh facts.
			InputCanonical:      []byte(`not-json`),
			EvaluationCanonical: []byte(`not-json`),
		},
	)
	if err != nil || created || !reflect.DeepEqual(retried, want) {
		t.Fatalf("restored exact retry=(%#v,%v,%v)", retried, created, err)
	}
	current, _, _, err := store.LoadPublishedBasis(
		context.Background(),
		want.TenantID,
	)
	if err != nil || controlOperationAPIBasis(current) != want.PostBasis {
		t.Fatalf("restored current basis=%+v error=%v", current, err)
	}
}

func TestControlOperationReceiptSemanticTamperMatrix(t *testing.T) {
	fixture := newBackupFixture(t)
	seedNoChangeControlOperationReceipt(t, fixture.databasePath)
	if err := VerifyCurrentStoreSemanticClosure(
		context.Background(),
		fixture.databasePath,
	); err != nil {
		t.Fatalf("valid receipt semantic closure error = %v", err)
	}

	tests := []struct {
		name   string
		tamper string
	}{
		{
			name: "canonical",
			tamper: `
				UPDATE control_operation_receipts
				SET evaluation_canonical = CAST(replace(
					CAST(evaluation_canonical AS TEXT),
					'NO_CHANGE',
					'NO_CHANGF'
				) AS BLOB)
			`,
		},
		{
			name: "digest",
			tamper: `
				UPDATE control_operation_receipts
				SET evaluation_digest = '` + strings.Repeat("a", 64) + `'
			`,
		},
		{
			name: "parent",
			tamper: `
				UPDATE control_operation_receipts
				SET pre_control_snapshot_id = 'missing-receipt-parent',
				    post_control_snapshot_id = 'missing-receipt-parent'
			`,
		},
		{
			name: "domain",
			tamper: `
				UPDATE control_operation_receipts
				SET domain_receipt_kind = 'MODULE_DISABLE',
				    domain_receipt_id = '` + strings.Repeat("b", 64) + `',
				    domain_receipt_digest = '` + strings.Repeat("c", 64) + `',
				    domain_receipt_canonical = CAST('{}' AS BLOB),
				    domain_receipt_size_bytes = 2,
				    canonical_total_size_bytes = canonical_total_size_bytes + 2
			`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyReceiptDatabase(t, fixture.databasePath, copyPath)
			tamperControlOperationReceipt(t, copyPath, test.tamper)

			semanticErr := verifyControlReceiptSemanticOnly(t, copyPath)
			if !errors.Is(
				semanticErr,
				currentstore.ErrControlOperationReceiptIntegrityV1,
			) {
				t.Fatalf(
					"VerifyControlOperationReceiptSemanticClosureV1() error = %v, want receipt integrity error",
					semanticErr,
				)
			}
			err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				copyPath,
			)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf(
					"VerifyCurrentStoreSemanticClosure() error = %v, want ErrIntegrity",
					err,
				)
			}
		})
	}
}

func TestAppliedControlOperationReceiptBackupRejectsTamperV1(t *testing.T) {
	fixture := newBackupFixture(t)
	want := seedAppliedControlOperationReceiptV1(t, fixture.databasePath)

	tests := []struct {
		name   string
		tamper func(*testing.T, string, currentstore.StoredControlOperationReceiptV1)
	}{
		{
			name: "missing immutable post parent",
			tamper: func(t *testing.T, path string, _ currentstore.StoredControlOperationReceiptV1) {
				tamperControlOperationReceiptWithTriggers(t, path,
					[]string{"control_snapshots_reject_delete"}, `
					DELETE FROM control_snapshots
					WHERE snapshot_id=(
						SELECT post_control_snapshot_id
						FROM control_operation_receipts
						LIMIT 1
					)
				`)
			},
		},
		{
			name:   "self-consistent evaluation replay drift",
			tamper: tamperAppliedEvaluationReplayV1,
		},
		{
			name:   "self-consistent domain receipt drift",
			tamper: tamperAppliedDomainReceiptV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyReceiptDatabase(t, fixture.databasePath, copyPath)
			test.tamper(t, copyPath, want)

			semanticErr := verifyControlReceiptSemanticOnly(t, copyPath)
			if !errors.Is(
				semanticErr,
				currentstore.ErrControlOperationReceiptIntegrityV1,
			) {
				t.Fatalf(
					"VerifyControlOperationReceiptSemanticClosureV1() error = %v, want receipt integrity error",
					semanticErr,
				)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				copyPath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf(
					"VerifyCurrentStoreSemanticClosure() error = %v, want ErrIntegrity",
					err,
				)
			}
			_, err := CreateBundle(
				context.Background(),
				copyPath,
				fixture.artifactRoot,
				filepath.Join(t.TempDir(), "tampered.bundle"),
				"currentbackup-applied-tamper-test/v1",
			)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle() tamper error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func verifyControlReceiptSemanticOnly(t *testing.T, path string) error {
	t.Helper()
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
	}()
	return currentstore.VerifyControlOperationReceiptSemanticClosureV1(
		context.Background(),
		connection,
	)
}

func seedAppliedControlOperationReceiptV1(
	t *testing.T,
	databasePath string,
) currentstore.StoredControlOperationReceiptV1 {
	t.Helper()
	ctx := context.Background()
	tenantID := firstCurrentTenantID(t, databasePath)
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()

	basis, control, catalog, err := store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	profileIndex, bindingIndex := -1, -1
	for candidateProfile := range control.Profiles {
		for candidateBinding := range control.Profiles[candidateProfile].Bindings {
			binding := control.Profiles[candidateProfile].Bindings[candidateBinding]
			entry, found := catalog.FindInstance(binding.InstanceID)
			if binding.Port != moduleapi.ExactContextProvidePortV1() || !found ||
				entry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative {
				continue
			}
			if profileIndex >= 0 {
				t.Fatal("APPLIED receipt fixture has multiple declarative Profile Context bindings")
			}
			profileIndex, bindingIndex = candidateProfile, candidateBinding
		}
	}
	if profileIndex < 0 || bindingIndex < 0 {
		t.Fatal("APPLIED receipt fixture has no declarative Profile Context binding")
	}
	targetProfileID := control.Profiles[profileIndex].Profile.ID
	targetInstanceID := control.Profiles[profileIndex].Bindings[bindingIndex].InstanceID
	control.Profiles[profileIndex].Bindings[bindingIndex].FailurePolicy =
		moduleapi.FailureOptional
	control.SnapshotID = "control-backup-applied-receipt-pre-v1"
	control.Revision = basis.Control.Revision + 1
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze eligible APPLIED Control: %v", err)
	}
	catalog.GenerationID = "catalog-backup-applied-receipt-pre-v1"
	catalog.Generation = basis.Catalog.Generation + 1
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze eligible APPLIED Catalog: %v", err)
	}
	basis, err = store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish eligible APPLIED predecessor: %v", err)
	}

	apiBasis := controlOperationAPIBasis(basis)
	_, _, basisDigest, err := controlapicontract.NewPublishedBasisRefV1(apiBasis)
	if err != nil {
		t.Fatal(err)
	}
	expected := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: tenantID,
		Revision:   basis.PointerRevision,
		Digest:     basisDigest,
	}
	_, inputCanonical, inputDigest, err :=
		moduledisablecontract.NewModuleDisableDryRunBodyV1(
			moduledisablecontract.ModuleDisableDryRunBodyV1{
				SchemaVersion: moduledisablecontract.
					ModuleDisableDryRunBodySchemaVersionV1,
				ExpectedPointerRevision: basis.PointerRevision,
				BindingTarget: moduledisablecontract.ModuleBindingTargetV1{
					Kind:      moduledisablecontract.ModuleBindingTargetProfileV1,
					ProfileID: targetProfileID,
				},
				InstanceID: targetInstanceID,
				Port:       moduleapi.ExactContextProvidePortV1(),
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := store.EvaluateModuleDisableControlOperationV1(
		ctx,
		currentstore.EvaluateModuleDisableControlOperationInputV1{
			TenantID:        tenantID,
			ExpectedPointer: expected,
			InputCanonical:  inputCanonical,
			InputDigest:     inputDigest,
		},
	)
	if err != nil {
		t.Fatalf("EvaluateModuleDisableControlOperationV1() error = %v", err)
	}
	if evaluation.Evaluation.Projection.Disposition !=
		moduledisablecontract.ModuleDisableWouldApplyV1 {
		t.Fatalf("APPLIED fixture evaluation=%+v", evaluation.Evaluation)
	}

	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeTenantV1,
			TenantID:      tenantID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	idempotencyDigest := moduleapi.Digest(
		"freeagent.currentbackup.applied-receipt-idempotency/v1",
		[]byte(tenantID),
	)
	statement := controlapicontract.ControlConfirmationStatementV1{
		SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
		PrincipalID:               "backup-applied-receipt-operator",
		Capability:                controlapicontract.CapabilityOperateModulesV1,
		Intent:                    controlapicontract.OperationIntentMutateV1,
		Operation:                 controlapicontract.OperationModuleDisableV1,
		Scope:                     scope,
		ScopeDigest:               scopeDigest,
		IdempotencyKeyDigest:      idempotencyDigest,
		InputDigest:               inputDigest,
		OperationEvaluationDigest: evaluation.EvaluationDigest,
		ExpectedRef:               expected,
	}
	_, _, confirmationDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(statement)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(
			controlapicontract.ControlOperationRequestV1{
				SchemaVersion:             controlapicontract.ControlOperationRequestSchemaVersionV1,
				PrincipalID:               statement.PrincipalID,
				Capability:                statement.Capability,
				Scope:                     statement.Scope,
				ScopeDigest:               statement.ScopeDigest,
				Operation:                 statement.Operation,
				Intent:                    statement.Intent,
				IdempotencyKeyDigest:      statement.IdempotencyKeyDigest,
				InputDigest:               statement.InputDigest,
				OperationEvaluationDigest: statement.OperationEvaluationDigest,
				ExpectedRef:               statement.ExpectedRef,
				ConfirmationDigest:        confirmationDigest,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	record, created, err := store.CommitModuleDisableControlOperationV1(
		ctx,
		currentstore.CommitModuleDisableControlOperationInputV1{
			AuthorizationRevision: 21,
			ScopeSetDigest: moduleapi.Digest(
				"freeagent.currentbackup.applied-receipt-scope-set/v1",
				[]byte(scopeDigest),
			),
			RequestCanonical:    requestCanonical,
			RequestDigest:       requestDigest,
			InputCanonical:      inputCanonical,
			EvaluationCanonical: evaluation.EvaluationCanonical,
		},
	)
	if err != nil || !created ||
		record.Status != controlapicontract.OperationStatusAppliedV1 ||
		record.DomainReceipt == nil || record.DomainReceiptRef == nil {
		t.Fatalf("CommitModuleDisableControlOperationV1()=(%+v,%v,%v)", record, created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return record
}

func tamperAppliedEvaluationReplayV1(
	t *testing.T,
	path string,
	want currentstore.StoredControlOperationReceiptV1,
) {
	t.Helper()
	evaluation := want.Evaluation
	if evaluation.Projection.CatalogChange ==
		moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1 {
		evaluation.Projection.CatalogChange =
			moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
	} else {
		evaluation.Projection.CatalogChange =
			moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
	}
	_, evaluationCanonical, evaluationDigest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	request := want.Request
	request.OperationEvaluationDigest = evaluationDigest
	statement := controlapicontract.ControlConfirmationStatementV1{
		SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
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
	_, _, confirmationDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(statement)
	if err != nil {
		t.Fatal(err)
	}
	request.ConfirmationDigest = confirmationDigest
	_, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt := want.ControlReceipt
	receipt.RequestDigest = requestDigest
	_, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(receipt)
	if err != nil {
		t.Fatal(err)
	}
	totalSize := len(requestCanonical) + len(want.InputCanonical) +
		len(evaluationCanonical) + len(receiptCanonical) +
		len(want.PreBasisCanonical) + len(want.PostBasisCanonical) +
		len(want.DomainReceiptCanonical)
	tamperControlOperationReceipt(t, path, `
		UPDATE control_operation_receipts
		SET receipt_digest=?,
		    request_digest=?, request_canonical=?, request_size_bytes=?,
		    evaluation_digest=?, evaluation_canonical=?, evaluation_size_bytes=?,
		    control_receipt_canonical=?, control_receipt_size_bytes=?,
		    canonical_total_size_bytes=?
	`,
		receiptDigest,
		requestDigest, requestCanonical, len(requestCanonical),
		evaluationDigest, evaluationCanonical, len(evaluationCanonical),
		receiptCanonical, len(receiptCanonical), totalSize,
	)
}

func tamperAppliedDomainReceiptV1(
	t *testing.T,
	path string,
	want currentstore.StoredControlOperationReceiptV1,
) {
	t.Helper()
	if want.DomainReceipt == nil {
		t.Fatal("APPLIED tamper fixture has no domain receipt")
	}
	domain := *want.DomainReceipt
	domain.DisabledPlan = bytes.Clone(want.DomainReceipt.DisabledPlan)
	domain.RemovedBinding.StaticContextRefs = append(
		[]string{},
		want.DomainReceipt.RemovedBinding.StaticContextRefs...,
	)
	if domain.CatalogChange ==
		moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1 {
		domain.CatalogChange =
			moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
	} else {
		domain.CatalogChange =
			moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
	}
	_, domainCanonical, domainRef, err :=
		moduledisablecontract.NewModuleDisablePublicationReceiptV1(domain)
	if err != nil {
		t.Fatal(err)
	}
	receipt := want.ControlReceipt
	receipt.DomainReceipt = &domainRef
	_, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(receipt)
	if err != nil {
		t.Fatal(err)
	}
	totalSize := len(want.RequestCanonical) + len(want.InputCanonical) +
		len(want.EvaluationCanonical) + len(receiptCanonical) +
		len(want.PreBasisCanonical) + len(want.PostBasisCanonical) +
		len(domainCanonical)
	tamperControlOperationReceipt(t, path, `
		UPDATE control_operation_receipts
		SET receipt_digest=?,
		    control_receipt_canonical=?, control_receipt_size_bytes=?,
		    domain_receipt_kind=?, domain_receipt_id=?, domain_receipt_digest=?,
		    domain_receipt_canonical=?, domain_receipt_size_bytes=?,
		    canonical_total_size_bytes=?
	`,
		receiptDigest,
		receiptCanonical, len(receiptCanonical),
		string(domainRef.Kind), domainRef.ID, domainRef.Digest,
		domainCanonical, len(domainCanonical), totalSize,
	)
}

func seedNoChangeControlOperationReceipt(
	t *testing.T,
	databasePath string,
) currentstore.StoredControlOperationReceiptV1 {
	t.Helper()
	ctx := context.Background()
	tenantID := firstCurrentTenantID(t, databasePath)
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()

	basis, control, _, err := store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Profiles) == 0 {
		t.Fatal("receipt fixture has no Profile")
	}
	plan, _, planDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(
			moduleapplyplan.ProfileContextDisableInputV1{
				TenantID:                tenantID,
				ExpectedPointerRevision: basis.PointerRevision,
				ProfileID:               control.Profiles[0].Profile.ID,
				InstanceID:              "absent-context-provider",
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	_, inputCanonical, inputDigest, err :=
		controlapp.NewModuleDisableDryRunBodyV1(
			controlapp.ModuleDisableDryRunBodyV1{
				SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
				ExpectedPointerRevision: plan.ExpectedPointerRevision,
				BindingTarget: controlapp.ModuleBindingTargetV1{
					Kind:      controlapp.ModuleBindingTargetProfileV1,
					ProfileID: plan.BindingTarget.ProfileID,
				},
				InstanceID: plan.InstanceID,
				Port:       plan.Port,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	apiBasis := controlOperationAPIBasis(basis)
	_, basisCanonical, basisDigest, err :=
		controlapicontract.NewPublishedBasisRefV1(apiBasis)
	if err != nil {
		t.Fatal(err)
	}
	expected := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: tenantID,
		Revision:   basis.PointerRevision,
		Digest:     basisDigest,
	}
	_, evaluationCanonical, evaluationDigest, err :=
		controlapp.NewModuleDisableEvaluationV1(
			controlapp.ModuleDisableEvaluationV1{
				SchemaVersion: controlapp.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   inputDigest,
				ExpectedRef:   expected,
				Projection: controlapp.ModuleDisableProjectionV1{
					Disposition:       controlapp.ModuleDisableNoChangeV1,
					PlanDigest:        planDigest,
					InstanceID:        plan.InstanceID,
					PreconditionBasis: apiBasis,
					ObservedBasis:     apiBasis,
					CandidateBasis:    apiBasis,
					CandidateState:    controlapp.ModuleDisableProjectedNotReservedV1,
					CatalogChange:     controlapp.ModuleDisableCatalogNoneV1,
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeTenantV1,
			TenantID:      tenantID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	idempotencyDigest := moduleapi.Digest(
		"freeagent.currentbackup.receipt-test-idempotency/v1",
		[]byte(tenantID),
	)
	statement := controlapicontract.ControlConfirmationStatementV1{
		SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
		PrincipalID:               "backup-receipt-operator",
		Capability:                controlapicontract.CapabilityOperateModulesV1,
		Intent:                    controlapicontract.OperationIntentMutateV1,
		Operation:                 controlapicontract.OperationModuleDisableV1,
		Scope:                     scope,
		ScopeDigest:               scopeDigest,
		IdempotencyKeyDigest:      idempotencyDigest,
		InputDigest:               inputDigest,
		OperationEvaluationDigest: evaluationDigest,
		ExpectedRef:               expected,
	}
	_, _, confirmationDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(statement)
	if err != nil {
		t.Fatal(err)
	}
	request, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(
			controlapicontract.ControlOperationRequestV1{
				SchemaVersion:             controlapicontract.ControlOperationRequestSchemaVersionV1,
				PrincipalID:               statement.PrincipalID,
				Capability:                statement.Capability,
				Scope:                     scope,
				ScopeDigest:               scopeDigest,
				Operation:                 statement.Operation,
				Intent:                    statement.Intent,
				IdempotencyKeyDigest:      idempotencyDigest,
				InputDigest:               inputDigest,
				OperationEvaluationDigest: evaluationDigest,
				ExpectedRef:               expected,
				ConfirmationDigest:        confirmationDigest,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	pre, post := expected, expected
	_, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(
			controlapicontract.ControlOperationReceiptV1{
				SchemaVersion:         controlapicontract.ControlOperationReceiptSchemaVersionV1,
				RequestDigest:         requestDigest,
				Intent:                request.Intent,
				IdempotencyKeyDigest:  request.IdempotencyKeyDigest,
				PrincipalID:           request.PrincipalID,
				ScopeDigest:           request.ScopeDigest,
				Operation:             request.Operation,
				Status:                controlapicontract.OperationStatusNoChangeV1,
				ErrorCode:             controlapicontract.ErrorNoneV1,
				PreRef:                &pre,
				PostRef:               &post,
				ReplayDisposition:     controlapicontract.ReplayReturnExactReceiptV1,
				CompletedAtUnixMicros: 1,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	record, created, err := store.CommitModuleDisableControlReceiptV1(
		ctx,
		currentstore.CommitModuleDisableControlReceiptInputV1{
			AuthorizationRevision:   1,
			ScopeSetDigest:          moduleapi.Digest("freeagent.currentbackup.receipt-test-scope-set/v1", []byte(scopeDigest)),
			RequestCanonical:        requestCanonical,
			RequestDigest:           requestDigest,
			InputCanonical:          inputCanonical,
			InputDigest:             inputDigest,
			EvaluationCanonical:     evaluationCanonical,
			EvaluationDigest:        evaluationDigest,
			ControlReceiptCanonical: receiptCanonical,
			ControlReceiptDigest:    receiptDigest,
			PublishedBasisCanonical: basisCanonical,
			PublishedBasisDigest:    basisDigest,
		},
	)
	if err != nil || !created {
		t.Fatalf("CommitModuleDisableControlReceiptV1() = created %v, error %v", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return record
}

func controlOperationAPIBasis(
	basis controlcontract.PublishedBasis,
) controlapicontract.PublishedBasisRefV1 {
	return controlapicontract.PublishedBasisRefV1{
		TenantID:        basis.TenantID,
		PointerRevision: basis.PointerRevision,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID:       basis.Control.SnapshotID,
			Revision: basis.Control.Revision,
			Digest:   basis.Control.Digest,
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID:       basis.Catalog.GenerationID,
			Revision: basis.Catalog.Generation,
			Digest:   basis.Catalog.Digest,
		},
	}
}

func firstCurrentTenantID(t *testing.T, databasePath string) string {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var tenantID string
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT tenant_id FROM control_current ORDER BY tenant_id LIMIT 1`,
	).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	return tenantID
}

func copyReceiptDatabase(t *testing.T, source, destination string) {
	t.Helper()
	identity, err := hashRegularFileContext(
		context.Background(),
		source,
		maxDatabaseBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyRegularFile(
		context.Background(),
		source,
		destination,
		identity,
	); err != nil {
		t.Fatal(err)
	}
}

func tamperControlOperationReceipt(
	t *testing.T,
	path string,
	statement string,
	arguments ...any,
) {
	t.Helper()
	tamperControlOperationReceiptWithTriggers(
		t,
		path,
		[]string{"control_operation_receipts_reject_update"},
		statement,
		arguments...,
	)
}

func tamperControlOperationReceiptWithTriggers(
	t *testing.T,
	path string,
	triggers []string,
	statement string,
	arguments ...any,
) {
	t.Helper()
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(
			path,
			"rw",
			"trusted_schema(0)",
			"busy_timeout(0)",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	result := execClosedFileTamperV1(
		t,
		database,
		triggers,
		statement,
		arguments...,
	)
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("tamper changed %d rows, error %v", changed, err)
	}
}
