package currentstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCommitResolveModuleDisableNoChangeReceiptV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	advanceControlReceiptFixtureToDisabledV1(t, &fixture)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	if built.metadata.status != controlapicontract.OperationStatusNoChangeV1 {
		t.Fatalf("built status=%s, want NO_CHANGE", built.metadata.status)
	}
	input := noChangeCommitInputFromBuiltV1(built)

	record, created, err := fixture.store.CommitModuleDisableControlReceiptV1(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("CommitModuleDisableControlReceiptV1: %v", err)
	}
	if !created || record.ReceiptDigest != built.metadata.receiptDigest ||
		record.RequestDigest != built.metadata.requestDigest ||
		record.InputDigest != built.metadata.inputDigest ||
		record.PlanDigest != built.planDigest ||
		record.Status != controlapicontract.OperationStatusNoChangeV1 ||
		record.DomainReceipt != nil {
		t.Fatalf("stored record=%+v created=%v", record, created)
	}
	if err := VerifyControlOperationReceiptSemanticClosureV1(
		context.Background(),
		fixture.store.db,
	); err != nil {
		t.Fatalf("VerifyControlOperationReceiptSemanticClosureV1: %v", err)
	}

	// Same Request must resolve before any fresh audit/evaluation/basis fields
	// are inspected. This is the lost-response retry path.
	retryInput := CommitModuleDisableControlReceiptInputV1{
		RequestCanonical: bytes.Clone(input.RequestCanonical),
		RequestDigest:    input.RequestDigest,
	}
	retried, retryCreated, err := fixture.store.CommitModuleDisableControlReceiptV1(
		context.Background(),
		retryInput,
	)
	if err != nil || retryCreated ||
		retried.ReceiptDigest != record.ReceiptDigest ||
		!bytes.Equal(retried.ControlReceiptCanonical, record.ControlReceiptCanonical) {
		t.Fatalf("exact retry=(%+v,%v,%v)", retried, retryCreated, err)
	}

	resolved, err := fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		record.RequestCanonical,
		record.RequestDigest,
	)
	if err != nil || resolved.ReceiptDigest != record.ReceiptDigest {
		t.Fatalf("ResolveControlOperationReceiptV1=(%+v,%v)", resolved, err)
	}
	resolved.RequestCanonical[0] = '['
	resolved.InputCanonical[0] = '['
	resolved.PlanCanonical[0] = '['
	resolved.Evaluation.Projection.PlanDigest = strings.Repeat("0", 64)
	again, err := fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		record.RequestCanonical,
		record.RequestDigest,
	)
	if err != nil || again.RequestCanonical[0] != '{' ||
		again.InputCanonical[0] != '{' || again.PlanCanonical[0] != '{' ||
		again.Evaluation.Projection.PlanDigest != built.planDigest {
		t.Fatalf("resolved DTO aliases prior result: %+v err=%v", again, err)
	}
}

func TestResolveControlOperationReceiptTypedMissAndConflictV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	advanceControlReceiptFixtureToDisabledV1(t, &fixture)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	input := noChangeCommitInputFromBuiltV1(built)
	record, _, err := fixture.store.CommitModuleDisableControlReceiptV1(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}

	missingRequest := mutateControlReceiptRequestV1(
		t,
		built.request,
		func(request *controlapicontract.ControlOperationRequestV1) {
			request.IdempotencyKeyDigest = strings.Repeat("f", 64)
		},
	)
	_, missingIdentity, err := preflightControlOperationReceiptRequestV1(
		missingRequest.canonical,
		missingRequest.digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		missingRequest.canonical,
		missingRequest.digest,
	)
	var missing *ControlOperationReceiptNotFoundErrorV1
	if !errors.Is(err, ErrControlOperationReceiptNotFoundV1) ||
		!errors.As(err, &missing) || missing.Identity != missingIdentity {
		t.Fatalf("not-found error=%T %v", err, err)
	}

	alternateRequest := alternateControlReceiptRequestV1(t, built.request)
	_, err = fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		alternateRequest.canonical,
		alternateRequest.digest,
	)
	var conflict *ControlOperationReceiptConflictErrorV1
	if !errors.Is(err, ErrControlOperationReceiptConflictV1) ||
		!errors.As(err, &conflict) || conflict.Identity != record.Identity {
		t.Fatalf("conflict error=%T %v", err, err)
	}

	_, _, err = fixture.store.CommitModuleDisableControlReceiptV1(
		context.Background(),
		CommitModuleDisableControlReceiptInputV1{
			RequestCanonical: alternateRequest.canonical,
			RequestDigest:    alternateRequest.digest,
		},
	)
	if !errors.Is(err, ErrControlOperationReceiptConflictV1) {
		t.Fatalf("alternate Request same identity error=%v", err)
	}

	malformed := append([]byte{' '}, record.RequestCanonical...)
	_, err = fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		malformed,
		record.RequestDigest,
	)
	if !errors.Is(err, ErrInvalidControlOperationReceiptV1) {
		t.Fatalf("malformed Request canonical error=%v", err)
	}
}

func TestAppliedControlOperationReceiptSemanticReplayV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	if built.metadata.status != controlapicontract.OperationStatusAppliedV1 ||
		built.result.Publication == nil || !built.metadata.domainKind.Valid {
		t.Fatalf("built APPLIED row=%+v", built.metadata)
	}
	commitAppliedControlReceiptFixtureV1(t, fixture.store, built)

	if err := VerifyControlOperationReceiptSemanticClosureV1(
		context.Background(),
		fixture.store.db,
	); err != nil {
		t.Fatalf("Verify APPLIED receipt: %v", err)
	}
	record, err := fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		built.payload.request,
		built.metadata.requestDigest,
	)
	if err != nil || record.DomainReceipt == nil ||
		record.DomainReceiptRef == nil ||
		record.DomainReceipt.PlanDigest != built.planDigest ||
		record.PostBasis != apiBasisFromControlBasisV1(
			built.result.CandidateBasis,
		) {
		t.Fatalf("resolved APPLIED=(%+v,%v)", record, err)
	}
	record.DomainReceipt.DisabledPlan[0] = '['
	record.DomainReceipt.RemovedBinding.StaticContextRefs[0] = strings.Repeat("0", 64)
	again, err := fixture.store.ResolveControlOperationReceiptV1(
		context.Background(),
		record.RequestCanonical,
		record.RequestDigest,
	)
	if err != nil || again.DomainReceipt.DisabledPlan[0] != '{' ||
		again.DomainReceipt.RemovedBinding.StaticContextRefs[0] == strings.Repeat("0", 64) {
		t.Fatalf("domain DTO aliases prior result: %+v err=%v", again, err)
	}
}

func TestControlOperationReceiptSemanticVerifierRejectsTamperV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	advanceControlReceiptFixtureToDisabledV1(t, &fixture)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	built.metadata.requestDigest = strings.Repeat("e", 64)
	insertRawControlReceiptFixtureV1(t, fixture.store, built)
	if err := VerifyControlOperationReceiptSemanticClosureV1(
		context.Background(),
		fixture.store.db,
	); !errors.Is(err, ErrControlOperationReceiptIntegrityV1) {
		t.Fatalf("tampered Request digest verifier error=%v", err)
	}
}

func TestAppliedControlOperationReceiptRejectsBroaderContextSliceV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementUntrustedData,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	prepared, err := prepareControlCatalogPublication(
		publishInputFromDisableResultV1(t, built.result),
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), `ROLLBACK`)
	if _, err := publishControlCatalogInTransaction(
		context.Background(),
		connection,
		prepared,
	); err != nil {
		t.Fatal(err)
	}
	if err := insertPreparedControlOperationReceiptV1(
		context.Background(),
		connection,
		built.metadata,
		built.payload,
	); !errors.Is(err, ErrControlOperationReceiptIntegrityV1) {
		t.Fatalf("broader UNTRUSTED_DATA slice insert error=%v", err)
	}
}

func TestAppliedControlOperationReceiptRejectsManifestRuntimeRewireV1(t *testing.T) {
	for _, mode := range []moduleapi.RuntimeModeRequest{
		moduleapi.RuntimeModeRequestLocalProcess,
		moduleapi.RuntimeModeRequestRemote,
	} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newControlReceiptFixtureV1(
				t,
				moduleapi.ContextPlacementTrustedInstruction,
			)
			built := buildControlReceiptRowV1(
				t,
				fixture,
				fixture.basis,
				fixture.control,
				fixture.catalog,
			)
			commitAppliedControlReceiptFixtureV1(t, fixture.store, built)

			var installationID, moduleID, version string
			if err := fixture.store.db.QueryRowContext(context.Background(), `
				SELECT installation.installation_id,
				       installation.module_id,
				       installation.exact_version
				FROM module_activations AS activation
				JOIN module_installations AS installation
				  ON installation.installation_id=activation.installation_id
				WHERE activation.tenant_id=? AND activation.instance_id=?
				  AND activation.activation_revision=?
			`, fixture.basis.TenantID, built.result.ExcludedInstanceID, 1).Scan(
				&installationID,
				&moduleID,
				&version,
			); err != nil {
				t.Fatal(err)
			}
			manifest := canonicalModuleManifest(
				t,
				moduleID,
				version,
				mode,
				map[string]any{
					"provides": []any{
						map[string]any{
							"name":          moduleapi.PortNameContextProvide,
							"exact_version": moduleapi.PortVersionV1,
						},
					},
				},
			)
			manifestRef := putPublicationJSON(
				t,
				fixture.store,
				ContentModuleManifest,
				manifest,
			)
			result, err := fixture.store.db.ExecContext(context.Background(), `
				UPDATE module_installations
				SET manifest_ref=?
				WHERE installation_id=?
			`, manifestRef, installationID)
			if err != nil {
				t.Fatal(err)
			}
			if changed, err := result.RowsAffected(); err != nil || changed != 1 {
				t.Fatalf("manifest runtime rewire changed %d rows, error %v", changed, err)
			}

			if err := VerifyPublishedControlCatalogClosureV1(
				context.Background(),
				fixture.store.db,
				fixture.control,
				fixture.catalog,
			); err == nil {
				t.Fatal("publication closure accepted Manifest runtime rewire")
			}
			if err := VerifyControlOperationReceiptSemanticClosureV1(
				context.Background(),
				fixture.store.db,
			); !errors.Is(err, ErrControlOperationReceiptIntegrityV1) {
				t.Fatalf("receipt closure error=%v, want receipt integrity", err)
			}
		})
	}
}

type controlReceiptFixtureV1 struct {
	store   *Store
	basis   controlcontract.PublishedBasis
	control controlcontract.ControlSnapshot
	catalog controlcontract.CatalogGeneration
}

func newControlReceiptFixtureV1(
	t *testing.T,
	placement moduleapi.ContextPlacementV1,
) controlReceiptFixtureV1 {
	t.Helper()
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     placement,
			Parameters:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := newBindingContractPublication(
		t,
		moduleapi.RuntimeModeRequestDeclarative,
		moduleapi.ExecutionDeclarative,
		moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		},
		configCanonical,
		[]byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
		[][]byte{validStaticContext(t)},
	)
	control, err := controlcontract.RestoreControlSnapshot(
		publication.input.ControlCanonical,
		publication.input.ControlRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.Profiles[0].Bindings[0].FailurePolicy = moduleapi.FailureOptional
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		publication.input.CatalogCanonical,
		publication.input.CatalogRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication.input.ControlRef = controlRef
	publication.input.ControlCanonical = controlCanonical
	publication.input.CatalogRef = catalogRef
	publication.input.CatalogCanonical = catalogCanonical
	basis, err := publication.store.PublishControlCatalog(
		context.Background(),
		publication.input,
	)
	if err != nil {
		t.Fatalf("Publish initial receipt basis: %v", err)
	}
	basis, control, catalog, err = publication.store.LoadPublishedBasis(
		context.Background(),
		publication.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return controlReceiptFixtureV1{
		store: publication.store, basis: basis, control: control, catalog: catalog,
	}
}

func advanceControlReceiptFixtureToDisabledV1(
	t *testing.T,
	fixture *controlReceiptFixtureV1,
) {
	t.Helper()
	built := buildControlReceiptRowV1(
		t,
		*fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	if built.result.Status != moduledisabledryrun.StatusWouldApplyV1 {
		t.Fatalf("advance replay status=%s", built.result.Status)
	}
	input := publishInputFromDisableResultV1(t, built.result)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("publish disabled basis: %v", err)
	}
	var err error
	fixture.basis, fixture.control, fixture.catalog, err =
		fixture.store.LoadPublishedBasis(
			context.Background(),
			fixture.basis.TenantID,
		)
	if err != nil {
		t.Fatal(err)
	}
}

type builtControlReceiptRowV1 struct {
	metadata    controlOperationReceiptMetadataV1
	payload     controlOperationReceiptPayloadV1
	request     controlapicontract.ControlOperationRequestV1
	planDigest  string
	result      moduledisabledryrun.ResultV1
	publication PublishControlCatalogInput
}

func buildControlReceiptRowV1(
	t *testing.T,
	fixture controlReceiptFixtureV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) builtControlReceiptRowV1 {
	t.Helper()
	body, inputCanonical, inputDigest, err :=
		controlapp.NewModuleDisableDryRunBodyV1(
			controlapp.ModuleDisableDryRunBodyV1{
				SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
				ExpectedPointerRevision: basis.PointerRevision,
				BindingTarget: controlapp.ModuleBindingTargetV1{
					Kind:      controlapp.ModuleBindingTargetProfileV1,
					ProfileID: "profile-binding-contract",
				},
				InstanceID: "instance-binding-contract",
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	plan, planCanonical, planDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(
			moduleapplyplan.ProfileContextDisableInputV1{
				TenantID:                basis.TenantID,
				ExpectedPointerRevision: body.ExpectedPointerRevision,
				ProfileID:               body.BindingTarget.ProfileID,
				InstanceID:              body.InstanceID,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := moduledisabledryrun.EvaluateExactBasisV1(
		moduledisabledryrun.InputV1{
			TenantID:                     plan.TenantID,
			ExpectedPointerRevision:      plan.ExpectedPointerRevision,
			TargetKind:                   moduledisabledryrun.TargetProfileV1,
			ProfileID:                    plan.BindingTarget.ProfileID,
			InstanceID:                   plan.InstanceID,
			Port:                         plan.Port,
			PlanDigest:                   planDigest,
			CandidateControlSnapshotID:   candidates.ControlSnapshotID,
			CandidateCatalogGenerationID: candidates.CatalogGenerationID,
		},
		basis,
		control,
		catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	preBasis := apiBasisFromControlBasisV1(basis)
	_, preBasisCanonical, preBasisDigest, err :=
		controlapicontract.NewPublishedBasisRefV1(preBasis)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := moduleDisableProjectionFromReplayV1(
		planDigest,
		plan.InstanceID,
		result,
	)
	if err != nil {
		t.Fatal(err)
	}
	preExpected := expectedPublishedPointerRefV1(preBasis, preBasisDigest)
	_, evaluationCanonical, evaluationDigest, err :=
		controlapp.NewModuleDisableEvaluationV1(
			controlapp.ModuleDisableEvaluationV1{
				SchemaVersion: controlapp.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   inputDigest,
				ExpectedRef:   preExpected,
				Projection:    projection,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		controlapicontract.ControlScopeV1{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeTenantV1,
			TenantID:      basis.TenantID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	idempotencyDigest := strings.Repeat("8", 64)
	statement := controlapicontract.ControlConfirmationStatementV1{
		SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
		PrincipalID:               "operator-receipt",
		Capability:                controlapicontract.CapabilityOperateModulesV1,
		Intent:                    controlapicontract.OperationIntentMutateV1,
		Operation:                 controlapicontract.OperationModuleDisableV1,
		Scope:                     scope,
		ScopeDigest:               scopeDigest,
		IdempotencyKeyDigest:      idempotencyDigest,
		InputDigest:               inputDigest,
		OperationEvaluationDigest: evaluationDigest,
		ExpectedRef:               preExpected,
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
				ExpectedRef:               preExpected,
				ConfirmationDigest:        confirmationDigest,
			},
		)
	if err != nil {
		t.Fatal(err)
	}

	status := controlapicontract.OperationStatusNoChangeV1
	postBasis := preBasis
	postBasisCanonical := preBasisCanonical
	postBasisDigest := preBasisDigest
	var domainCanonical []byte
	var domainRef *controlapicontract.DomainReceiptRefV1
	if result.Status == moduledisabledryrun.StatusWouldApplyV1 {
		status = controlapicontract.OperationStatusAppliedV1
		postBasis = apiBasisFromControlBasisV1(result.CandidateBasis)
		_, postBasisCanonical, postBasisDigest, err =
			controlapicontract.NewPublishedBasisRefV1(postBasis)
		if err != nil {
			t.Fatal(err)
		}
		domain := domainReceiptFromReplayV1(
			planCanonical,
			planDigest,
			preBasis,
			postBasis,
			plan.InstanceID,
			result,
		)
		var frozenRef controlapicontract.DomainReceiptRefV1
		_, domainCanonical, frozenRef, err =
			controlapp.NewModuleDisablePublicationReceiptV1(domain)
		if err != nil {
			t.Fatal(err)
		}
		domainRef = &frozenRef
	}
	receiptInput := controlapicontract.ControlOperationReceiptV1{
		SchemaVersion:        controlapicontract.ControlOperationReceiptSchemaVersionV1,
		RequestDigest:        requestDigest,
		Intent:               request.Intent,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		PrincipalID:          request.PrincipalID,
		ScopeDigest:          request.ScopeDigest,
		Operation:            request.Operation,
		Status:               status,
		ErrorCode:            controlapicontract.ErrorNoneV1,
		PreRef:               &preExpected,
		PostRef: func() *controlapicontract.ExpectedResourceRefV1 {
			value := expectedPublishedPointerRefV1(postBasis, postBasisDigest)
			return &value
		}(),
		DomainReceipt:         domainRef,
		ReplayDisposition:     controlapicontract.ReplayReturnExactReceiptV1,
		CompletedAtUnixMicros: 1_800_000_000_000_000,
	}
	_, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(receiptInput)
	if err != nil {
		t.Fatal(err)
	}
	metadata := controlOperationReceiptMetadataV1{
		receiptDigest:        receiptDigest,
		tenantID:             basis.TenantID,
		principalID:          request.PrincipalID,
		scopeDigest:          request.ScopeDigest,
		operation:            request.Operation,
		idempotencyKeyDigest: request.IdempotencyKeyDigest,
		policyRevision:       7,
		scopeSetDigest:       strings.Repeat("9", 64),
		status:               status,
		requestDigest:        requestDigest,
		requestSize:          int64(len(requestCanonical)),
		inputDigest:          inputDigest,
		inputSize:            int64(len(inputCanonical)),
		evaluationDigest:     evaluationDigest,
		evaluationSize:       int64(len(evaluationCanonical)),
		controlReceiptSize:   int64(len(receiptCanonical)),
		preBasisDigest:       preBasisDigest,
		preBasisSize:         int64(len(preBasisCanonical)),
		postBasisDigest:      postBasisDigest,
		postBasisSize:        int64(len(postBasisCanonical)),
		preControlID:         preBasis.Control.ID,
		preCatalogID:         preBasis.Catalog.ID,
		postControlID:        postBasis.Control.ID,
		postCatalogID:        postBasis.Catalog.ID,
	}
	payload := controlOperationReceiptPayloadV1{
		request:        requestCanonical,
		input:          inputCanonical,
		evaluation:     evaluationCanonical,
		controlReceipt: receiptCanonical,
		preBasis:       preBasisCanonical,
		postBasis:      postBasisCanonical,
		domainReceipt:  domainCanonical,
	}
	if domainRef != nil {
		metadata.domainKind.Valid = true
		metadata.domainKind.String = string(domainRef.Kind)
		metadata.domainID.Valid = true
		metadata.domainID.String = domainRef.ID
		metadata.domainDigest.Valid = true
		metadata.domainDigest.String = domainRef.Digest
		metadata.domainSize.Valid = true
		metadata.domainSize.Int64 = int64(len(domainCanonical))
	}
	metadata.totalSize = metadata.requestSize + metadata.inputSize +
		metadata.evaluationSize + metadata.controlReceiptSize +
		metadata.preBasisSize + metadata.postBasisSize
	if metadata.domainSize.Valid {
		metadata.totalSize += metadata.domainSize.Int64
	}
	if err := validateControlOperationReceiptMetadataV1(metadata); err != nil {
		t.Fatal(err)
	}
	return builtControlReceiptRowV1{
		metadata:   metadata,
		payload:    payload,
		request:    request,
		planDigest: planDigest,
		result:     result,
	}
}

func noChangeCommitInputFromBuiltV1(
	built builtControlReceiptRowV1,
) CommitModuleDisableControlReceiptInputV1 {
	policyRevision := uint64(built.metadata.policyRevision)
	return CommitModuleDisableControlReceiptInputV1{
		AuthorizationRevision:   policyRevision,
		ScopeSetDigest:          built.metadata.scopeSetDigest,
		RequestCanonical:        bytes.Clone(built.payload.request),
		RequestDigest:           built.metadata.requestDigest,
		InputCanonical:          bytes.Clone(built.payload.input),
		InputDigest:             built.metadata.inputDigest,
		EvaluationCanonical:     bytes.Clone(built.payload.evaluation),
		EvaluationDigest:        built.metadata.evaluationDigest,
		ControlReceiptCanonical: bytes.Clone(built.payload.controlReceipt),
		ControlReceiptDigest:    built.metadata.receiptDigest,
		PublishedBasisCanonical: bytes.Clone(built.payload.preBasis),
		PublishedBasisDigest:    built.metadata.preBasisDigest,
	}
}

func publishInputFromDisableResultV1(
	t *testing.T,
	result moduledisabledryrun.ResultV1,
) PublishControlCatalogInput {
	t.Helper()
	if result.Publication == nil {
		t.Fatal("Disable replay has no publication")
	}
	return PublishControlCatalogInput{
		ExpectedPointerRevision: result.Publication.ExpectedPointerRevision,
		NewPointerRevision:      result.Publication.NewPointerRevision,
		ControlRef:              result.Publication.ControlRef,
		ControlCanonical:        bytes.Clone(result.Publication.ControlCanonical),
		CatalogRef:              result.Publication.CatalogRef,
		CatalogCanonical:        bytes.Clone(result.Publication.CatalogCanonical),
	}
}

func commitAppliedControlReceiptFixtureV1(
	t *testing.T,
	store *Store,
	built builtControlReceiptRowV1,
) {
	t.Helper()
	prepared, err := prepareControlCatalogPublication(
		publishInputFromDisableResultV1(t, built.result),
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if _, err := publishControlCatalogInTransaction(
		context.Background(),
		connection,
		prepared,
	); err != nil {
		t.Fatal(err)
	}
	if err := insertPreparedControlOperationReceiptV1(
		context.Background(),
		connection,
		built.metadata,
		built.payload,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		t.Fatal(err)
	}
	committed = true
}

func insertRawControlReceiptFixtureV1(
	t *testing.T,
	store *Store,
	built builtControlReceiptRowV1,
) {
	t.Helper()
	metadata := built.metadata
	payload := built.payload
	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO control_operation_receipts(
			receipt_digest, tenant_id, principal_id, scope_digest,
			operation, idempotency_key_digest, authorization_revision,
			scope_set_digest, status,
			request_digest, request_canonical, request_size_bytes,
			input_digest, input_canonical, input_size_bytes,
			evaluation_digest, evaluation_canonical, evaluation_size_bytes,
			control_receipt_canonical, control_receipt_size_bytes,
			pre_basis_digest, pre_basis_canonical, pre_basis_size_bytes,
			post_basis_digest, post_basis_canonical, post_basis_size_bytes,
			domain_receipt_kind, domain_receipt_id, domain_receipt_digest,
			domain_receipt_canonical, domain_receipt_size_bytes,
			canonical_total_size_bytes,
			pre_control_snapshot_id, pre_catalog_generation_id,
			post_control_snapshot_id, post_catalog_generation_id
		) VALUES(
			?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?,?,
			NULL,NULL,NULL,NULL,NULL, ?,?,?,?,?
		)
	`,
		metadata.receiptDigest,
		metadata.tenantID,
		metadata.principalID,
		metadata.scopeDigest,
		string(metadata.operation),
		metadata.idempotencyKeyDigest,
		metadata.policyRevision,
		metadata.scopeSetDigest,
		string(metadata.status),
		metadata.requestDigest,
		payload.request,
		metadata.requestSize,
		metadata.inputDigest,
		payload.input,
		metadata.inputSize,
		metadata.evaluationDigest,
		payload.evaluation,
		metadata.evaluationSize,
		payload.controlReceipt,
		metadata.controlReceiptSize,
		metadata.preBasisDigest,
		payload.preBasis,
		metadata.preBasisSize,
		metadata.postBasisDigest,
		payload.postBasis,
		metadata.postBasisSize,
		metadata.totalSize,
		metadata.preControlID,
		metadata.preCatalogID,
		metadata.postControlID,
		metadata.postCatalogID,
	)
	if err != nil {
		t.Fatalf("insert raw receipt fixture: %v", err)
	}
}

type frozenControlReceiptRequestV1 struct {
	canonical []byte
	digest    string
}

func alternateControlReceiptRequestV1(
	t *testing.T,
	base controlapicontract.ControlOperationRequestV1,
) frozenControlReceiptRequestV1 {
	t.Helper()
	return mutateControlReceiptRequestV1(
		t,
		base,
		func(request *controlapicontract.ControlOperationRequestV1) {
			request.InputDigest = strings.Repeat("a", 64)
			request.OperationEvaluationDigest = strings.Repeat("b", 64)
		},
	)
}

func mutateControlReceiptRequestV1(
	t *testing.T,
	base controlapicontract.ControlOperationRequestV1,
	mutate func(*controlapicontract.ControlOperationRequestV1),
) frozenControlReceiptRequestV1 {
	t.Helper()
	mutate(&base)
	statement := controlapicontract.ControlConfirmationStatementV1{
		SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
		PrincipalID:               base.PrincipalID,
		Capability:                base.Capability,
		Intent:                    base.Intent,
		Operation:                 base.Operation,
		Scope:                     base.Scope,
		ScopeDigest:               base.ScopeDigest,
		IdempotencyKeyDigest:      base.IdempotencyKeyDigest,
		InputDigest:               base.InputDigest,
		OperationEvaluationDigest: base.OperationEvaluationDigest,
		ExpectedRef:               base.ExpectedRef,
	}
	_, _, confirmationDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(statement)
	if err != nil {
		t.Fatal(err)
	}
	base.ConfirmationDigest = confirmationDigest
	_, canonical, digest, err :=
		controlapicontract.NewControlOperationRequestV1(base)
	if err != nil {
		t.Fatal(err)
	}
	return frozenControlReceiptRequestV1{canonical: canonical, digest: digest}
}
