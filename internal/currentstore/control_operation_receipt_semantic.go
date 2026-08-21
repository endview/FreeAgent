package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyControlOperationReceiptSemanticClosureV1 restores and replays every
// durable Control receipt from one coherent queryer snapshot. It enumerates
// bounded metadata first and loads at most one capped canonical payload at a
// time. The verifier is pure Store inspection: it performs no publication,
// recovery, filesystem, artifact, provider, Adapter, process, or network I/O.
func VerifyControlOperationReceiptSemanticClosureV1(
	ctx context.Context,
	q moduleDiscoveryQueryer,
) error {
	if ctx == nil || q == nil {
		return fmt.Errorf(
			"%w: nil semantic verifier input",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var rowCount int64
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM control_operation_receipts
	`).Scan(&rowCount); err != nil {
		return controlReceiptIntegrityV1("count receipts", err)
	}
	if rowCount < 0 || rowCount > maximumStoredControlReceiptsV1 {
		return controlReceiptIntegrityV1("Store receipt quota exceeded", nil)
	}

	tenantRows, err := q.QueryContext(ctx, `
		SELECT tenant_id, COUNT(*)
		FROM control_operation_receipts
		GROUP BY tenant_id
		ORDER BY tenant_id COLLATE BINARY
	`)
	if err != nil {
		return controlReceiptIntegrityV1("list Tenant quotas", err)
	}
	for tenantRows.Next() {
		var tenantID string
		var count int64
		if err := tenantRows.Scan(&tenantID, &count); err != nil {
			_ = tenantRows.Close()
			return controlReceiptIntegrityV1("scan Tenant quota", err)
		}
		if !validControlReceiptOpaqueIDV1(tenantID) || count <= 0 ||
			count > maximumStoredControlReceiptsPerTenantV1 {
			_ = tenantRows.Close()
			return controlReceiptIntegrityV1("Tenant receipt quota invalid", nil)
		}
	}
	if err := tenantRows.Err(); err != nil {
		_ = tenantRows.Close()
		return controlReceiptIntegrityV1("iterate Tenant quotas", err)
	}
	if err := tenantRows.Close(); err != nil {
		return controlReceiptIntegrityV1("close Tenant quotas", err)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT
			receipt_digest, tenant_id, principal_id, scope_digest,
			operation, idempotency_key_digest, authorization_revision,
			scope_set_digest, status,
			request_digest, request_size_bytes,
			input_digest, input_size_bytes,
			evaluation_digest, evaluation_size_bytes,
			control_receipt_size_bytes,
			pre_basis_digest, pre_basis_size_bytes,
			post_basis_digest, post_basis_size_bytes,
			domain_receipt_kind, domain_receipt_id, domain_receipt_digest,
			domain_receipt_size_bytes, canonical_total_size_bytes,
			pre_control_snapshot_id, pre_catalog_generation_id,
			post_control_snapshot_id, post_catalog_generation_id
		FROM control_operation_receipts
		ORDER BY receipt_digest COLLATE BINARY
	`)
	if err != nil {
		return controlReceiptIntegrityV1("list receipt metadata", err)
	}
	metadata := make([]controlOperationReceiptMetadataV1, 0, int(rowCount))
	identities := make(map[ControlOperationReceiptIdentityV1]struct{}, int(rowCount))
	domainIDs := make(map[string]struct{})
	domainDigests := make(map[string]struct{})
	for rows.Next() {
		item, found, err := scanControlOperationReceiptMetadataOptionalV1(rows)
		if err != nil || !found {
			_ = rows.Close()
			return controlReceiptIntegrityV1("scan receipt metadata", err)
		}
		identity := controlOperationReceiptIdentityFromMetadataV1(item)
		if _, duplicate := identities[identity]; duplicate {
			_ = rows.Close()
			return controlReceiptIntegrityV1("duplicate receipt identity", nil)
		}
		identities[identity] = struct{}{}
		if item.domainID.Valid {
			if _, duplicate := domainIDs[item.domainID.String]; duplicate {
				_ = rows.Close()
				return controlReceiptIntegrityV1("duplicate domain receipt ID", nil)
			}
			if _, duplicate := domainDigests[item.domainDigest.String]; duplicate {
				_ = rows.Close()
				return controlReceiptIntegrityV1("duplicate domain receipt digest", nil)
			}
			domainIDs[item.domainID.String] = struct{}{}
			domainDigests[item.domainDigest.String] = struct{}{}
		}
		metadata = append(metadata, item)
		if len(metadata) > maximumStoredControlReceiptsV1 {
			_ = rows.Close()
			return controlReceiptIntegrityV1("receipt enumeration overflow", nil)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return controlReceiptIntegrityV1("iterate receipt metadata", err)
	}
	if err := rows.Close(); err != nil {
		return controlReceiptIntegrityV1("close receipt metadata", err)
	}
	if int64(len(metadata)) != rowCount {
		return controlReceiptIntegrityV1("receipt count changed during snapshot", nil)
	}

	for _, item := range metadata {
		if _, err := restoreStoredControlOperationReceiptV1(ctx, q, item); err != nil {
			return err
		}
	}
	return nil
}

func restoreControlOperationReceiptFactsV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	metadata controlOperationReceiptMetadataV1,
	payload controlOperationReceiptPayloadV1,
) (StoredControlOperationReceiptV1, error) {
	if ctx == nil || q == nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("nil row verifier input", nil)
	}
	if err := validateControlOperationReceiptMetadataV1(metadata); err != nil {
		return StoredControlOperationReceiptV1{}, err
	}
	request, err := controlapicontract.RestoreControlOperationRequestV1(
		payload.request,
		metadata.requestDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore Request", err)
	}
	input, err := moduledisablecontract.RestoreModuleDisableDryRunBodyV1(
		payload.input,
		metadata.inputDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore typed input", err)
	}
	if input.BindingTarget.Kind != moduledisablecontract.ModuleBindingTargetProfileV1 ||
		input.BindingTarget.ProfileID == "" ||
		input.BindingTarget.WorkspaceID != "" ||
		input.BindingTarget.EndpointID != "" {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("typed input exceeds Profile slice", nil)
	}
	plan, planCanonical, planDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(
			moduleapplyplan.ProfileContextDisableInputV1{
				TenantID:                request.Scope.TenantID,
				ExpectedPointerRevision: input.ExpectedPointerRevision,
				ProfileID:               input.BindingTarget.ProfileID,
				InstanceID:              input.InstanceID,
			},
		)
	if err != nil || input.Port != plan.Port {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("derive narrow Disable plan", err)
	}
	evaluation, err := moduledisablecontract.RestoreModuleDisableEvaluationV1(
		payload.evaluation,
		metadata.evaluationDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore operation evaluation", err)
	}
	controlReceipt, err := controlapicontract.RestoreControlOperationReceiptV1(
		payload.controlReceipt,
		metadata.receiptDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore Control receipt", err)
	}
	preBasis, err := controlapicontract.RestorePublishedBasisRefV1(
		payload.preBasis,
		metadata.preBasisDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore pre PublishedBasis", err)
	}
	postBasis, err := controlapicontract.RestorePublishedBasisRefV1(
		payload.postBasis,
		metadata.postBasisDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{},
			controlReceiptIntegrityV1("restore post PublishedBasis", err)
	}

	var domainReceipt *moduledisablecontract.ModuleDisablePublicationReceiptV1
	var domainRef *controlapicontract.DomainReceiptRefV1
	if metadata.domainKind.Valid {
		ref := controlapicontract.DomainReceiptRefV1{
			Kind: controlapicontract.DomainReceiptKindV1(
				metadata.domainKind.String,
			),
			ID:     metadata.domainID.String,
			Digest: metadata.domainDigest.String,
		}
		restored, err := moduledisablecontract.RestoreModuleDisablePublicationReceiptV1(
			payload.domainReceipt,
			ref,
		)
		if err != nil {
			return StoredControlOperationReceiptV1{},
				controlReceiptIntegrityV1("restore domain receipt", err)
		}
		domainReceipt = &restored
		domainRef = &ref
	}

	if err := verifyControlOperationReceiptCrossClosureV1(
		ctx,
		q,
		metadata,
		payload,
		request,
		input,
		plan,
		planCanonical,
		planDigest,
		evaluation,
		controlReceipt,
		preBasis,
		postBasis,
		domainReceipt,
		domainRef,
	); err != nil {
		return StoredControlOperationReceiptV1{}, err
	}

	policyRevision := uint64(metadata.policyRevision)
	record := StoredControlOperationReceiptV1{
		ReceiptDigest:           metadata.receiptDigest,
		TenantID:                metadata.tenantID,
		Identity:                controlOperationReceiptIdentityFromMetadataV1(metadata),
		AuthorizationRevision:   policyRevision,
		ScopeSetDigest:          metadata.scopeSetDigest,
		Status:                  metadata.status,
		Request:                 request,
		RequestCanonical:        bytes.Clone(payload.request),
		RequestDigest:           metadata.requestDigest,
		Input:                   input,
		InputCanonical:          bytes.Clone(payload.input),
		InputDigest:             metadata.inputDigest,
		Plan:                    plan,
		PlanCanonical:           bytes.Clone(planCanonical),
		PlanDigest:              planDigest,
		Evaluation:              evaluation,
		EvaluationCanonical:     bytes.Clone(payload.evaluation),
		EvaluationDigest:        metadata.evaluationDigest,
		ControlReceipt:          controlReceipt,
		ControlReceiptCanonical: bytes.Clone(payload.controlReceipt),
		PreBasis:                preBasis,
		PreBasisCanonical:       bytes.Clone(payload.preBasis),
		PreBasisDigest:          metadata.preBasisDigest,
		PostBasis:               postBasis,
		PostBasisCanonical:      bytes.Clone(payload.postBasis),
		PostBasisDigest:         metadata.postBasisDigest,
		DomainReceiptCanonical:  bytes.Clone(payload.domainReceipt),
	}
	if domainReceipt != nil {
		copiedReceipt := *domainReceipt
		copiedReceipt.DisabledPlan = bytes.Clone(domainReceipt.DisabledPlan)
		copiedReceipt.RemovedBinding.StaticContextRefs = append(
			[]string{},
			domainReceipt.RemovedBinding.StaticContextRefs...,
		)
		copiedRef := *domainRef
		record.DomainReceipt = &copiedReceipt
		record.DomainReceiptRef = &copiedRef
	}
	return record, nil
}

func verifyControlOperationReceiptCrossClosureV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	metadata controlOperationReceiptMetadataV1,
	payload controlOperationReceiptPayloadV1,
	request controlapicontract.ControlOperationRequestV1,
	input moduledisablecontract.ModuleDisableDryRunBodyV1,
	plan moduleapplyplan.ProfileContextDisablePlanV1,
	planCanonical []byte,
	planDigest string,
	evaluation moduledisablecontract.ModuleDisableEvaluationV1,
	receipt controlapicontract.ControlOperationReceiptV1,
	preBasis controlapicontract.PublishedBasisRefV1,
	postBasis controlapicontract.PublishedBasisRefV1,
	domain *moduledisablecontract.ModuleDisablePublicationReceiptV1,
	domainRef *controlapicontract.DomainReceiptRefV1,
) error {
	identity := controlOperationReceiptIdentityFromMetadataV1(metadata)
	if request.Operation != controlapicontract.OperationModuleDisableV1 ||
		request.Intent != controlapicontract.OperationIntentMutateV1 ||
		request.Scope.Kind != controlapicontract.ScopeTenantV1 ||
		request.Scope.TenantID != metadata.tenantID ||
		request.PrincipalID != identity.PrincipalID ||
		request.ScopeDigest != identity.ScopeDigest ||
		request.Operation != identity.Operation ||
		request.IdempotencyKeyDigest != identity.IdempotencyKeyDigest ||
		request.InputDigest != metadata.inputDigest ||
		request.OperationEvaluationDigest != metadata.evaluationDigest ||
		input.ExpectedPointerRevision != preBasis.PointerRevision ||
		plan.TenantID != metadata.tenantID ||
		plan.ExpectedPointerRevision != preBasis.PointerRevision ||
		evaluation.Projection.PlanDigest != planDigest ||
		preBasis.TenantID != metadata.tenantID ||
		postBasis.TenantID != metadata.tenantID ||
		preBasis.Control.ID != metadata.preControlID ||
		preBasis.Catalog.ID != metadata.preCatalogID ||
		postBasis.Control.ID != metadata.postControlID ||
		postBasis.Catalog.ID != metadata.postCatalogID {
		return controlReceiptIntegrityV1("Request/input/basis identity drift", nil)
	}
	preExpected := expectedPublishedPointerRefV1(
		preBasis,
		metadata.preBasisDigest,
	)
	postExpected := expectedPublishedPointerRefV1(
		postBasis,
		metadata.postBasisDigest,
	)
	if request.ExpectedRef != preExpected ||
		evaluation.InputDigest != metadata.inputDigest ||
		evaluation.ExpectedRef != preExpected ||
		evaluation.Operation != request.Operation ||
		receipt.RequestDigest != metadata.requestDigest ||
		receipt.PrincipalID != request.PrincipalID ||
		receipt.ScopeDigest != request.ScopeDigest ||
		receipt.Operation != request.Operation ||
		receipt.Intent != request.Intent ||
		receipt.IdempotencyKeyDigest != request.IdempotencyKeyDigest ||
		receipt.Status != metadata.status || receipt.PreRef == nil ||
		*receipt.PreRef != preExpected || receipt.PostRef == nil ||
		*receipt.PostRef != postExpected {
		return controlReceiptIntegrityV1("Request/evaluation/receipt drift", nil)
	}
	if metadata.status == controlapicontract.OperationStatusNoChangeV1 {
		if domain != nil || domainRef != nil || receipt.DomainReceipt != nil ||
			preBasis != postBasis ||
			!bytes.Equal(payload.preBasis, payload.postBasis) {
			return controlReceiptIntegrityV1("NO_CHANGE carries an effect", nil)
		}
	} else {
		if domain == nil || domainRef == nil || receipt.DomainReceipt == nil ||
			*receipt.DomainReceipt != *domainRef || preBasis == postBasis {
			return controlReceiptIntegrityV1("APPLIED domain reference drift", nil)
		}
	}

	preControl, preCatalog, err := loadControlReceiptBasisParentsV1(
		ctx,
		q,
		preBasis,
	)
	if err != nil {
		return err
	}
	postControl, postCatalog := preControl, preCatalog
	if postBasis != preBasis {
		postControl, postCatalog, err = loadControlReceiptBasisParentsV1(
			ctx,
			q,
			postBasis,
		)
		if err != nil {
			return err
		}
	}

	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		return controlReceiptIntegrityV1("derive candidate identity", err)
	}
	dryRunInput := moduledisabledryrun.InputV1{
		TenantID:                     plan.TenantID,
		ExpectedPointerRevision:      plan.ExpectedPointerRevision,
		TargetKind:                   moduledisabledryrun.TargetProfileV1,
		ProfileID:                    plan.BindingTarget.ProfileID,
		InstanceID:                   plan.InstanceID,
		Port:                         plan.Port,
		PlanDigest:                   planDigest,
		CandidateControlSnapshotID:   candidates.ControlSnapshotID,
		CandidateCatalogGenerationID: candidates.CatalogGenerationID,
	}
	result, err := moduledisabledryrun.EvaluateExactBasisV1(
		dryRunInput,
		controlBasisFromAPIRefV1(preBasis),
		preControl,
		preCatalog,
	)
	if err != nil {
		return controlReceiptIntegrityV1("neutral MODULE_DISABLE replay", err)
	}
	expectedProjection, err := moduleDisableProjectionFromReplayV1(
		planDigest,
		plan.InstanceID,
		result,
	)
	if err != nil {
		return err
	}
	_, expectedEvaluationCanonical, expectedEvaluationDigest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(
			moduledisablecontract.ModuleDisableEvaluationV1{
				SchemaVersion: moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   metadata.inputDigest,
				ExpectedRef:   preExpected,
				Projection:    expectedProjection,
			},
		)
	if err != nil || expectedEvaluationDigest != metadata.evaluationDigest ||
		!bytes.Equal(expectedEvaluationCanonical, payload.evaluation) {
		return controlReceiptIntegrityV1("evaluation differs from neutral replay", err)
	}

	switch metadata.status {
	case controlapicontract.OperationStatusNoChangeV1:
		if result.Status != moduledisabledryrun.StatusNoChangeV1 ||
			result.Publication != nil {
			return controlReceiptIntegrityV1("NO_CHANGE replay produced an effect", nil)
		}
	case controlapicontract.OperationStatusAppliedV1:
		if result.Status != moduledisabledryrun.StatusWouldApplyV1 ||
			result.Publication == nil {
			return controlReceiptIntegrityV1("APPLIED replay has no publication", nil)
		}
		if err := verifyFirstSliceRemovedBindingV1(
			ctx,
			q,
			preControl,
			preCatalog,
			plan,
			result.BindingRemoval,
		); err != nil {
			return err
		}
		_, postControlRef, postControlCanonical, err :=
			controlcontract.NewControlSnapshot(postControl)
		if err != nil {
			return controlReceiptIntegrityV1("freeze post Control", err)
		}
		_, postCatalogRef, postCatalogCanonical, err :=
			controlcontract.NewCatalogGeneration(postCatalog)
		if err != nil {
			return controlReceiptIntegrityV1("freeze post Catalog", err)
		}
		if result.Publication.ControlRef != postControlRef ||
			result.Publication.CatalogRef != postCatalogRef ||
			!bytes.Equal(result.Publication.ControlCanonical, postControlCanonical) ||
			!bytes.Equal(result.Publication.CatalogCanonical, postCatalogCanonical) {
			return controlReceiptIntegrityV1("post publication differs from replay", nil)
		}
		expectedDomain := domainReceiptFromReplayV1(
			planCanonical,
			planDigest,
			preBasis,
			postBasis,
			plan.InstanceID,
			result,
		)
		_, expectedDomainCanonical, expectedDomainRef, err :=
			moduledisablecontract.NewModuleDisablePublicationReceiptV1(expectedDomain)
		if err != nil || expectedDomainRef != *domainRef ||
			!bytes.Equal(expectedDomainCanonical, payload.domainReceipt) {
			return controlReceiptIntegrityV1("domain receipt differs from replay", err)
		}
	default:
		return controlReceiptIntegrityV1("unsupported durable status", nil)
	}
	return nil
}

func loadControlReceiptBasisParentsV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	basis controlapicontract.PublishedBasisRefV1,
) (controlcontract.ControlSnapshot, controlcontract.CatalogGeneration, error) {
	control, catalog, err := loadAdmissionControlCatalog(
		ctx,
		q,
		basis.TenantID,
		basis.Control.ID,
		basis.Catalog.ID,
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			controlReceiptIntegrityV1("load immutable Control/Catalog parents", err)
	}
	if control.Revision != basis.Control.Revision ||
		control.Digest != basis.Control.Digest ||
		catalog.Generation != basis.Catalog.Revision ||
		catalog.Digest != basis.Catalog.Digest {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			controlReceiptIntegrityV1("PublishedBasis parent projection differs", nil)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		ctx,
		q,
		control,
		catalog,
	); err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			controlReceiptIntegrityV1("published parent semantic closure", err)
	}
	return control, catalog, nil
}

func verifyFirstSliceRemovedBindingV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	input moduleapplyplan.ProfileContextDisablePlanV1,
	removed moduledisabledryrun.BindingRemovalV1,
) error {
	if removed.TargetKind != moduledisabledryrun.TargetProfileV1 ||
		removed.ProfileID != input.BindingTarget.ProfileID ||
		removed.WorkspaceID != "" || removed.EndpointID != "" ||
		removed.Port != (moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		}) || removed.PortBindingIndex == nil ||
		removed.FailurePolicy == nil ||
		*removed.FailurePolicy != moduleapi.FailureOptional {
		return controlReceiptIntegrityV1("removed Binding exceeds first slice", nil)
	}
	profile, found := control.FindProfile(input.BindingTarget.ProfileID)
	if !found {
		return controlReceiptIntegrityV1("removed Profile parent is absent", nil)
	}
	portOrdinal := uint32(0)
	matchCount := 0
	for _, binding := range profile.Bindings {
		if binding.Port != input.Port {
			continue
		}
		if binding.InstanceID == input.InstanceID {
			matchCount++
			if portOrdinal != *removed.PortBindingIndex ||
				binding.ConfigRef != removed.ConfigRef ||
				binding.AuthorityCeilingRef != removed.AuthorityCeilingRef ||
				binding.FailurePolicy != *removed.FailurePolicy ||
				!sameStringSliceV1(binding.StaticContextRefs, removed.StaticContextRefs) {
				return controlReceiptIntegrityV1("removed Binding parent differs", nil)
			}
		}
		portOrdinal++
	}
	if matchCount != 1 {
		return controlReceiptIntegrityV1("removed Binding is not unique", nil)
	}
	entry, found := catalog.FindInstance(input.InstanceID)
	if !found || entry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative {
		return controlReceiptIntegrityV1("removed provider is not DECLARATIVE", nil)
	}
	configRecord, err := queryContent(ctx, q, removed.ConfigRef)
	if err != nil || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return controlReceiptIntegrityV1("removed CONFIG is unavailable", err)
	}
	config, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil || config.Placement != moduleapi.ContextPlacementTrustedInstruction ||
		config.AllowSummary || config.AllowDrop {
		return controlReceiptIntegrityV1("removed CONFIG is not trusted-instruction inert", err)
	}
	authority, err := queryContent(ctx, q, removed.AuthorityCeilingRef)
	if err != nil || authority.Kind != ContentAuthorityCeiling ||
		authority.MediaType != admissionJSONMediaType ||
		!bytes.Equal(
			authority.CanonicalBytes,
			[]byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
		) {
		return controlReceiptIntegrityV1("removed Authority is not exact deny-all", err)
	}
	return nil
}

func moduleDisableProjectionFromReplayV1(
	planDigest string,
	instanceID string,
	result moduledisabledryrun.ResultV1,
) (moduledisablecontract.ModuleDisableProjectionV1, error) {
	projection := moduledisablecontract.ModuleDisableProjectionV1{
		PlanDigest:        planDigest,
		InstanceID:        instanceID,
		PreconditionBasis: apiBasisFromControlBasisV1(result.PreconditionBasis),
		ObservedBasis:     apiBasisFromControlBasisV1(result.ObservedBasis),
		CandidateBasis:    apiBasisFromControlBasisV1(result.CandidateBasis),
		CandidateState:    moduledisablecontract.ModuleDisableProjectedNotReservedV1,
	}
	switch result.Status {
	case moduledisabledryrun.StatusNoChangeV1:
		projection.Disposition = moduledisablecontract.ModuleDisableNoChangeV1
		projection.CatalogChange = moduledisablecontract.ModuleDisableCatalogNoneV1
	case moduledisabledryrun.StatusAlreadyAppliedV1:
		projection.Disposition = moduledisablecontract.ModuleDisableAlreadyAppliedV1
		projection.CatalogChange = moduledisablecontract.ModuleDisableCatalogNoneV1
	case moduledisabledryrun.StatusWouldApplyV1:
		projection.Disposition = moduledisablecontract.ModuleDisableWouldApplyV1
		binding, err := moduleDisableContractBindingFromReplayV1(result.BindingRemoval)
		if err != nil {
			return moduledisablecontract.ModuleDisableProjectionV1{}, err
		}
		projection.BindingRemoval = &binding
		switch result.CatalogChange {
		case moduledisabledryrun.CatalogChangeRetainInstanceV1:
			projection.CatalogChange = moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
		case moduledisabledryrun.CatalogChangeRemoveInstanceV1:
			projection.CatalogChange = moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
		default:
			return moduledisablecontract.ModuleDisableProjectionV1{},
				controlReceiptIntegrityV1("replay Catalog change is invalid", nil)
		}
	default:
		return moduledisablecontract.ModuleDisableProjectionV1{},
			controlReceiptIntegrityV1("replay status is invalid", nil)
	}
	return projection, nil
}

func moduleDisableContractBindingFromReplayV1(
	binding moduledisabledryrun.BindingRemovalV1,
) (moduledisablecontract.ModuleDisableBindingRemovalV1, error) {
	if binding.TargetKind != moduledisabledryrun.TargetProfileV1 ||
		binding.PortBindingIndex == nil || binding.FailurePolicy == nil {
		return moduledisablecontract.ModuleDisableBindingRemovalV1{},
			controlReceiptIntegrityV1("replay Binding removal is incomplete", nil)
	}
	return moduledisablecontract.ModuleDisableBindingRemovalV1{
		Target: moduledisablecontract.ModuleBindingTargetV1{
			Kind:      moduledisablecontract.ModuleBindingTargetProfileV1,
			ProfileID: binding.ProfileID,
		},
		Port:                binding.Port,
		PortBindingIndex:    *binding.PortBindingIndex,
		ConfigRef:           binding.ConfigRef,
		AuthorityCeilingRef: binding.AuthorityCeilingRef,
		StaticContextRefs:   append([]string(nil), binding.StaticContextRefs...),
		FailurePolicy:       *binding.FailurePolicy,
	}, nil
}

func domainReceiptFromReplayV1(
	planCanonical []byte,
	planDigest string,
	preBasis controlapicontract.PublishedBasisRefV1,
	postBasis controlapicontract.PublishedBasisRefV1,
	instanceID string,
	result moduledisabledryrun.ResultV1,
) moduledisablecontract.ModuleDisablePublicationReceiptV1 {
	binding, _ := moduleDisableContractBindingFromReplayV1(result.BindingRemoval)
	return moduledisablecontract.ModuleDisablePublicationReceiptV1{
		SchemaVersion: moduledisablecontract.ModuleDisablePublicationReceiptSchemaVersionV1,
		DisabledPlan:  bytes.Clone(planCanonical),
		PlanDigest:    planDigest,
		PreBasis:      preBasis,
		PostBasis:     postBasis,
		RemovedBinding: moduledisablecontract.ModuleDisablePublicationRemovedBindingV1{
			Target:              binding.Target,
			InstanceID:          instanceID,
			Port:                binding.Port,
			PortBindingIndex:    binding.PortBindingIndex,
			ConfigRef:           binding.ConfigRef,
			AuthorityCeilingRef: binding.AuthorityCeilingRef,
			StaticContextRefs: append(
				[]string{},
				binding.StaticContextRefs...,
			),
			FailurePolicy: binding.FailurePolicy,
		},
		CatalogChange: func() moduledisablecontract.ModuleDisableCatalogChangeV1 {
			if result.CatalogChange == moduledisabledryrun.CatalogChangeRemoveInstanceV1 {
				return moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
			}
			return moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
		}(),
	}
}

func expectedPublishedPointerRefV1(
	basis controlapicontract.PublishedBasisRefV1,
	digest string,
) controlapicontract.ExpectedResourceRefV1 {
	return controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: basis.TenantID,
		Revision:   basis.PointerRevision,
		Digest:     digest,
	}
}

func controlBasisFromAPIRefV1(
	basis controlapicontract.PublishedBasisRefV1,
) controlcontract.PublishedBasis {
	return controlcontract.PublishedBasis{
		TenantID:        basis.TenantID,
		PointerRevision: basis.PointerRevision,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: basis.Control.ID,
			Revision:   basis.Control.Revision,
			Digest:     basis.Control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: basis.Catalog.ID,
			Generation:   basis.Catalog.Revision,
			Digest:       basis.Catalog.Digest,
		},
	}
}

func apiBasisFromControlBasisV1(
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

func sameStringSliceV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func controlReceiptIntegrityV1(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf(
			"%w: %s",
			ErrControlOperationReceiptIntegrityV1,
			message,
		)
	}
	return fmt.Errorf(
		"%w: %s: %v",
		ErrControlOperationReceiptIntegrityV1,
		message,
		cause,
	)
}
