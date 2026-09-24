package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidPublication identifies malformed canonical input, references or
	// pointer revisions before a write transaction begins.
	ErrInvalidPublication = errors.New("currentstore: invalid Control/Catalog publication")

	// ErrPublicationConflict identifies a stale pointer, broken closure or
	// immutable row that differs from the requested exact publication.
	ErrPublicationConflict = errors.New("currentstore: Control/Catalog publication conflict")
)

// denyAllAuthorityCeilingCanonicalV1 is the one pre-E4 inert authority value
// retained for bootstrap compatibility. It is accepted by legacy Model and
// declarative Context bindings only as exact canonical bytes; it is not a
// wildcard for other authority schemas.
const denyAllAuthorityCeilingCanonicalV1 = `{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`

// Retain the historical internal name for existing package-level contract
// fixtures. Production verification uses the scope-neutral constant above.
const declarativeDenyAllAuthorityCeilingCanonicalV1 = denyAllAuthorityCeilingCanonicalV1

// PublishControlCatalogInput is one exact pointer transition. Revision zero is
// reserved for the absence of a current pointer; NewPointerRevision must be
// ExpectedPointerRevision+1.
type PublishControlCatalogInput struct {
	ExpectedPointerRevision uint64
	NewPointerRevision      uint64
	ControlRef              controlcontract.ControlSnapshotRef
	ControlCanonical        []byte
	CatalogRef              controlcontract.CatalogGenerationRef
	CatalogCanonical        []byte
}

type currentControlPointer struct {
	TenantID            string
	SnapshotID          string
	CatalogGenerationID string
	PointerRevision     uint64
}

type preparedControlCatalogPublication struct {
	input            PublishControlCatalogInput
	control          controlcontract.ControlSnapshot
	controlCanonical []byte
	catalog          controlcontract.CatalogGeneration
	catalogCanonical []byte
	basis            controlcontract.PublishedBasis
}

// PublishControlCatalog verifies and publishes one immutable Control/Catalog
// pair. Module activation records may exist before this call, but only an exact
// Catalog entry reachable through control_current makes an activation
// available.
func (store *Store) PublishControlCatalog(
	ctx context.Context,
	input PublishControlCatalogInput,
) (controlcontract.PublishedBasis, error) {
	if ctx == nil {
		return controlcontract.PublishedBasis{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidPublication,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return controlcontract.PublishedBasis{}, err
	}
	defer unlock()

	prepared, err := prepareControlCatalogPublication(input)
	if err != nil {
		return controlcontract.PublishedBasis{}, err
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return controlcontract.PublishedBasis{}, fmt.Errorf(
			"currentstore: acquire publication connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return controlcontract.PublishedBasis{}, fmt.Errorf(
			"currentstore: begin Control/Catalog publication: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	if _, err := publishControlCatalogInTransaction(ctx, connection, prepared); err != nil {
		return controlcontract.PublishedBasis{}, err
	}

	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return controlcontract.PublishedBasis{}, fmt.Errorf(
			"currentstore: commit Control/Catalog publication: %w",
			err,
		)
	}
	committed = true
	return prepared.basis, nil
}

func prepareControlCatalogPublication(
	input PublishControlCatalogInput,
) (preparedControlCatalogPublication, error) {
	if input.ExpectedPointerRevision >= math.MaxInt64 ||
		input.NewPointerRevision == 0 ||
		input.NewPointerRevision > math.MaxInt64 ||
		input.NewPointerRevision != input.ExpectedPointerRevision+1 {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: new pointer revision must equal expected revision plus one",
			ErrInvalidPublication,
		)
	}

	// Strict restore and digest verification intentionally occur before
	// BEGIN IMMEDIATE. Caller-owned byte slices cannot be mutated underneath
	// the transaction after this point.
	controlCanonical := bytes.Clone(input.ControlCanonical)
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		input.ControlRef,
	)
	if err != nil {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: restore ControlSnapshot: %v",
			ErrInvalidPublication,
			err,
		)
	}
	catalogCanonical := bytes.Clone(input.CatalogCanonical)
	catalog, err := controlcontract.RestoreCatalogGeneration(
		catalogCanonical,
		input.CatalogRef,
	)
	if err != nil {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: restore CatalogGeneration: %v",
			ErrInvalidPublication,
			err,
		)
	}
	if input.ControlRef.Revision > math.MaxInt64 ||
		input.CatalogRef.Generation > math.MaxInt64 {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: Control/Catalog revisions exceed SQLite INTEGER",
			ErrInvalidPublication,
		)
	}
	if control.TenantID != catalog.TenantID ||
		catalog.ControlSnapshotID != input.ControlRef.SnapshotID ||
		catalog.ControlSnapshotDigest != input.ControlRef.Digest {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: Control and Catalog do not form one tenant-scoped pair",
			ErrInvalidPublication,
		)
	}
	basis := controlcontract.PublishedBasis{
		TenantID:        control.TenantID,
		PointerRevision: input.NewPointerRevision,
		Control:         input.ControlRef,
		Catalog:         input.CatalogRef,
	}
	if err := basis.Validate(); err != nil {
		return preparedControlCatalogPublication{}, fmt.Errorf(
			"%w: result basis: %v",
			ErrInvalidPublication,
			err,
		)
	}
	return preparedControlCatalogPublication{
		input:            input,
		control:          control,
		controlCanonical: controlCanonical,
		catalog:          catalog,
		catalogCanonical: catalogCanonical,
		basis:            basis,
	}, nil
}

// publishControlCatalogInTransaction performs the immutable writes and
// pointer CAS on an already-open BEGIN IMMEDIATE connection. alreadyPublished
// is true only when the exact new pointer existed before this call.
func publishControlCatalogInTransaction(
	ctx context.Context,
	connection *sql.Conn,
	prepared preparedControlCatalogPublication,
) (alreadyPublished bool, err error) {
	input := prepared.input
	control := prepared.control
	catalog := prepared.catalog
	current, found, err := queryCurrentControlPointer(
		ctx,
		connection,
		control.TenantID,
	)
	if err != nil {
		return false, err
	}

	if found &&
		current.PointerRevision == input.NewPointerRevision &&
		current.SnapshotID == input.ControlRef.SnapshotID &&
		current.CatalogGenerationID == input.CatalogRef.GenerationID {
		if err := verifyStoredControlSnapshot(
			ctx, connection, control, input.ControlRef, prepared.controlCanonical,
		); err != nil {
			return false, err
		}
		if err := verifyStoredCatalogGeneration(
			ctx, connection, catalog, input.CatalogRef, prepared.catalogCanonical,
		); err != nil {
			return false, err
		}
		if err := verifyControlCatalogPublicationClosureV1(
			ctx,
			connection,
			control,
			catalog,
		); err != nil {
			return false, err
		}
		if err := verifyCurrentOverviewBasisProjectionV1(
			ctx, connection, control.TenantID,
		); err != nil {
			return false, err
		}
		return true, nil
	}

	if input.ExpectedPointerRevision == 0 {
		if found {
			return false, fmt.Errorf(
				"%w: tenant %s already has pointer revision %d",
				ErrPublicationConflict,
				control.TenantID,
				current.PointerRevision,
			)
		}
	} else if !found || current.PointerRevision != input.ExpectedPointerRevision {
		actual := uint64(0)
		if found {
			actual = current.PointerRevision
		}
		return false, fmt.Errorf(
			"%w: tenant %s pointer revision is %d, expected %d",
			ErrPublicationConflict,
			control.TenantID,
			actual,
			input.ExpectedPointerRevision,
		)
	}

	if err := verifyControlCatalogPublicationClosureV1(
		ctx,
		connection,
		control,
		catalog,
	); err != nil {
		return false, err
	}

	publishedAt := nowUnixMicro()
	if publishedAt <= 0 {
		return false, fmt.Errorf(
			"%w: invalid publication time",
			ErrPublicationConflict,
		)
	}
	if err := insertOrVerifyControlSnapshot(
		ctx,
		connection,
		control,
		input.ControlRef,
		prepared.controlCanonical,
		publishedAt,
	); err != nil {
		return false, err
	}
	if err := insertOrVerifyCatalogGeneration(
		ctx,
		connection,
		catalog,
		input.CatalogRef,
		prepared.catalogCanonical,
		publishedAt,
	); err != nil {
		return false, err
	}

	if input.ExpectedPointerRevision == 0 {
		result, err := connection.ExecContext(ctx, `
			INSERT INTO control_current(
				tenant_id,
				snapshot_id,
				catalog_generation_id,
				pointer_revision
			) VALUES(?, ?, ?, ?)
			ON CONFLICT(tenant_id) DO NOTHING
		`,
			control.TenantID,
			input.ControlRef.SnapshotID,
			input.CatalogRef.GenerationID,
			int64(input.NewPointerRevision),
		)
		if err != nil {
			return false, fmt.Errorf(
				"currentstore: insert current Control/Catalog pointer: %w",
				err,
			)
		}
		if err := requireOnePublicationRow(result, "insert current pointer"); err != nil {
			return false, err
		}
	} else {
		result, err := connection.ExecContext(ctx, `
			UPDATE control_current
			SET snapshot_id=?,
			    catalog_generation_id=?,
			    pointer_revision=?
			WHERE tenant_id=? AND pointer_revision=?
		`,
			input.ControlRef.SnapshotID,
			input.CatalogRef.GenerationID,
			int64(input.NewPointerRevision),
			control.TenantID,
			int64(input.ExpectedPointerRevision),
		)
		if err != nil {
			return false, fmt.Errorf(
				"currentstore: CAS current Control/Catalog pointer: %w",
				err,
			)
		}
		if err := requireOnePublicationRow(result, "CAS current pointer"); err != nil {
			return false, err
		}
	}

	current, found, err = queryCurrentControlPointer(ctx, connection, control.TenantID)
	if err != nil {
		return false, err
	}
	if !found ||
		current.PointerRevision != prepared.basis.PointerRevision ||
		current.SnapshotID != prepared.basis.Control.SnapshotID ||
		current.CatalogGenerationID != prepared.basis.Catalog.GenerationID {
		return false, fmt.Errorf(
			"%w: current pointer reread differs after CAS",
			ErrPublicationConflict,
		)
	}
	var controlPublishedAt, catalogPublishedAt int64
	if err := connection.QueryRowContext(ctx, `SELECT control.published_at,catalog.published_at
		FROM control_snapshots AS control JOIN runtime_catalog_generations AS catalog
		ON catalog.generation_id=? AND catalog.control_snapshot_id=control.snapshot_id
		WHERE control.snapshot_id=? AND control.tenant_id=? AND catalog.tenant_id=?`,
		prepared.basis.Catalog.GenerationID, prepared.basis.Control.SnapshotID,
		prepared.basis.TenantID, prepared.basis.TenantID,
	).Scan(&controlPublishedAt, &catalogPublishedAt); err != nil {
		return false, fmt.Errorf("currentstore: read Overview publication times: %w", err)
	}
	if err := publishOverviewBasisProjectionV1(
		ctx, connection, prepared, controlPublishedAt, catalogPublishedAt,
	); err != nil {
		return false, err
	}
	if err := verifyStoredControlSnapshot(
		ctx, connection, control, input.ControlRef, prepared.controlCanonical,
	); err != nil {
		return false, err
	}
	if err := verifyStoredCatalogGeneration(
		ctx, connection, catalog, input.CatalogRef, prepared.catalogCanonical,
	); err != nil {
		return false, err
	}
	return false, nil
}

func verifyControlContentClosure(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
) error {
	for _, profile := range control.Profiles {
		for _, policy := range []struct {
			ref  corecontract.PolicyRef
			kind corecontract.PolicyType
		}{
			{ref: profile.ContextPolicy, kind: corecontract.PolicyContext},
			{ref: profile.SchedulingPolicy, kind: corecontract.PolicyScheduling},
		} {
			if err := verifyPublicationPolicy(
				ctx,
				queryer,
				policy.ref,
				policy.kind,
			); err != nil {
				return err
			}
		}
		for _, binding := range profile.Bindings {
			if err := requirePublicationContentKind(
				ctx,
				queryer,
				binding.ConfigRef,
				ContentConfig,
			); err != nil {
				return err
			}
			if err := requirePublicationContentKind(
				ctx,
				queryer,
				binding.AuthorityCeilingRef,
				ContentAuthorityCeiling,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyPublicationPolicy(
	ctx context.Context,
	queryer publicationQueryer,
	ref corecontract.PolicyRef,
	expectedType corecontract.PolicyType,
) error {
	record, err := requirePublicationContent(
		ctx,
		queryer,
		ref.Digest,
		ContentPolicy,
	)
	if err != nil {
		return err
	}
	if record.MediaType != "application/json" {
		return fmt.Errorf(
			"%w: POLICY %s has media type %q",
			ErrPublicationConflict,
			ref.Digest,
			record.MediaType,
		)
	}
	document, err := corecontract.RestorePolicyDocument(
		record.CanonicalBytes,
		ref,
	)
	if err != nil || document.PolicyType != expectedType {
		return fmt.Errorf(
			"%w: PolicyRef %s does not restore as %s",
			ErrPublicationConflict,
			ref.Digest,
			expectedType,
		)
	}
	return nil
}

func requirePublicationContentKind(
	ctx context.Context,
	queryer publicationQueryer,
	digest string,
	kind ContentKind,
) error {
	_, err := requirePublicationContent(ctx, queryer, digest, kind)
	return err
}

func requirePublicationContent(
	ctx context.Context,
	queryer publicationQueryer,
	digest string,
	kind ContentKind,
) (ContentRecord, error) {
	record, err := queryContent(ctx, queryer, digest)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: read %s content %s: %v",
			ErrPublicationConflict,
			kind,
			digest,
			err,
		)
	}
	if record.Kind != kind {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s has kind %q, want %q",
			ErrPublicationConflict,
			digest,
			record.Kind,
			kind,
		)
	}
	return record, nil
}

func verifyCatalogActivationClosure(
	ctx context.Context,
	queryer publicationQueryer,
	catalog controlcontract.CatalogGeneration,
) error {
	for _, entry := range catalog.Entries {
		activation, err := queryModuleActivationByIdentity(
			ctx,
			queryer,
			catalog.TenantID,
			entry.Activation.InstanceID,
			entry.Activation.ActivationRevision,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: resolve activation %s/%d: %v",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
				entry.Activation.ActivationRevision,
				err,
			)
		}
		if activation.TenantID != catalog.TenantID ||
			activation.InstanceID != entry.Activation.InstanceID ||
			activation.ActivationRevision !=
				entry.Activation.ActivationRevision ||
			activation.ExecutionClass != entry.Activation.ExecutionClass ||
			activation.AdapterIdentity != entry.Activation.AdapterIdentity {
			return fmt.Errorf(
				"%w: Catalog activation %s does not match stored activation",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
			)
		}
		installation, err := queryModuleInstallationByID(
			ctx,
			queryer,
			activation.InstallationID,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: resolve installation for %s: %v",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
				err,
			)
		}
		if installation.ModuleID != entry.Activation.ModuleID ||
			installation.ExactVersion != entry.Activation.Version ||
			installation.ArtifactDigest != entry.Activation.ArtifactDigest {
			return fmt.Errorf(
				"%w: Catalog provider identity for %s does not match installation",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
			)
		}
		manifest, _, err := moduleapi.ParseModuleManifestV1(
			installation.ManifestBytes,
		)
		if err != nil ||
			!sameExactPortSet(manifest.Provides, entry.Provides) {
			return fmt.Errorf(
				"%w: Catalog ports for %s do not match its manifest",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
			)
		}
		expectedClass, supported := executionClassForRuntimeRequest(
			manifest.Runtime.Mode,
		)
		if !supported || expectedClass != activation.ExecutionClass ||
			(expectedClass == moduleapi.ExecutionDeclarative &&
				manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1) {
			return fmt.Errorf(
				"%w: Catalog activation %s execution class does not match its manifest runtime",
				ErrPublicationConflict,
				entry.Activation.InstanceID,
			)
		}
	}
	return nil
}

func verifyControlBindingsAgainstCatalog(
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	for _, profile := range control.Profiles {
		for index, binding := range profile.Bindings {
			entry, found := catalog.FindInstance(binding.InstanceID)
			if !found || !containsExactPort(entry.Provides, binding.Port) {
				return fmt.Errorf(
					"%w: profile %s binding %d does not resolve exact port %s/%s on instance %s",
					ErrPublicationConflict,
					profile.Profile.ID,
					index,
					binding.Port.Name,
					binding.Port.ExactVersion,
					binding.InstanceID,
				)
			}
		}
	}
	return nil
}

// verifyControlPortBindingContracts restores the exact consumer-owned wire
// contracts whose semantics are needed at publication time. This adds no
// mutable policy source: all decisions remain frozen in the referenced
// immutable content and the Catalog activation selected by Control.
func verifyControlPortBindingContracts(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	for _, profile := range control.Profiles {
		for index, binding := range profile.Bindings {
			entry, found := catalog.FindInstance(binding.InstanceID)
			if !found {
				return fmt.Errorf(
					"%w: profile %s binding %d has no Catalog provider",
					ErrPublicationConflict,
					profile.Profile.ID,
					index,
				)
			}

			switch {
			case binding.Port.Name == moduleapi.PortNameModelGenerate &&
				binding.Port.ExactVersion == moduleapi.PortVersionV2:
				if err := verifyModelBindingV1(
					ctx,
					queryer,
					control.TenantID,
					binding,
				); err != nil {
					return fmt.Errorf(
						"%w: profile %s binding %d model contract: %v",
						ErrPublicationConflict,
						profile.Profile.ID,
						index,
						err,
					)
				}
			case binding.Port.Name == moduleapi.PortNameContextProvide &&
				binding.Port.ExactVersion == moduleapi.PortVersionV1 &&
				entry.Activation.ExecutionClass == moduleapi.ExecutionDeclarative:
				if err := verifyDeclarativeContextBindingV1(
					ctx,
					queryer,
					binding,
				); err != nil {
					return fmt.Errorf(
						"%w: profile %s binding %d declarative context contract: %v",
						ErrPublicationConflict,
						profile.Profile.ID,
						index,
						err,
					)
				}
			case binding.Port.Name == moduleapi.PortNameActionProvider &&
				binding.Port.ExactVersion == moduleapi.PortVersionV1:
				if err := verifyActionBindingV1(
					ctx,
					queryer,
					binding,
				); err != nil {
					return fmt.Errorf(
						"%w: profile %s binding %d action contract: %v",
						ErrPublicationConflict,
						profile.Profile.ID,
						index,
						err,
					)
				}
			}
		}
	}
	return nil
}

func verifyModelBindingV1(
	ctx context.Context,
	queryer publicationQueryer,
	tenantID string,
	binding controlcontract.BindingSpec,
) error {
	if binding.FailurePolicy != moduleapi.FailureRequired ||
		len(binding.StaticContextRefs) != 0 {
		return errors.New("model Binding must be REQUIRED without static Context")
	}
	configRecord, err := requirePublicationContent(
		ctx,
		queryer,
		binding.ConfigRef,
		ContentConfig,
	)
	if err != nil || configRecord.MediaType != admissionJSONMediaType {
		return errors.Join(err, errors.New("model CONFIG is unavailable"))
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(configRecord.CanonicalBytes)
	if err != nil {
		return err
	}
	authorityRecord, err := requirePublicationContent(
		ctx,
		queryer,
		binding.AuthorityCeilingRef,
		ContentAuthorityCeiling,
	)
	if err != nil || authorityRecord.MediaType != admissionJSONMediaType {
		return errors.Join(err, errors.New("model authority ceiling is unavailable"))
	}
	var authorityHeader struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(authorityRecord.CanonicalBytes, &authorityHeader); err != nil {
		return fmt.Errorf("decode model authority header: %w", err)
	}
	switch {
	case authorityHeader.SchemaVersion == moduleapi.ModelAuthorityCeilingSchemaV1:
		authority, err := moduleapi.RestoreModelAuthorityCeilingV1(
			authorityRecord.CanonicalBytes,
		)
		if err != nil {
			return err
		}
		if authority.TenantID != tenantID || authority.Provider != config.Provider {
			return errors.New("model authority tenant/provider differs from Control and CONFIG")
		}
	case bytes.Equal(
		authorityRecord.CanonicalBytes,
		[]byte(denyAllAuthorityCeilingCanonicalV1),
	):
		// Existing bootstrap bindings used this exact inert value. New Model
		// Apply plans are required to use model-authority-ceiling/v1.
	default:
		return errors.New(
			"model authority ceiling is neither model-authority-ceiling/v1 nor the exact legacy deny-all value",
		)
	}
	return nil
}

func verifyDeclarativeContextBindingV1(
	ctx context.Context,
	queryer publicationQueryer,
	binding controlcontract.BindingSpec,
) error {
	configRecord, err := requirePublicationJSONContent(
		ctx,
		queryer,
		binding.ConfigRef,
		ContentConfig,
	)
	if err != nil {
		return err
	}
	config, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("CONFIG is not context-binding-config/v1")
	}
	if config.AllowSummary || config.AllowDrop {
		return fmt.Errorf(
			"CONFIG grants retention mutation to declarative context",
		)
	}

	authorityRecord, err := requirePublicationJSONContent(
		ctx,
		queryer,
		binding.AuthorityCeilingRef,
		ContentAuthorityCeiling,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(
		authorityRecord.CanonicalBytes,
		[]byte(denyAllAuthorityCeilingCanonicalV1),
	) {
		return fmt.Errorf("authority ceiling is not the exact deny-all v1 value")
	}

	if len(binding.StaticContextRefs) == 0 {
		return fmt.Errorf("at least one static context reference is required")
	}
	for index, digest := range binding.StaticContextRefs {
		record, err := requirePublicationJSONContent(
			ctx,
			queryer,
			digest,
			ContentStaticContext,
		)
		if err != nil {
			return fmt.Errorf("static context %d: %w", index, err)
		}
		if _, err := corecontract.RestoreStaticContextV1(
			record.CanonicalBytes,
		); err != nil {
			return fmt.Errorf(
				"static context %d is not static-context/v1",
				index,
			)
		}
	}
	return nil
}

func verifyActionBindingV1(
	ctx context.Context,
	queryer publicationQueryer,
	binding controlcontract.BindingSpec,
) error {
	configRecord, err := requirePublicationJSONContent(
		ctx,
		queryer,
		binding.ConfigRef,
		ContentConfig,
	)
	if err != nil {
		return err
	}
	if _, err := moduleapi.RestoreActionBindingConfigV1(
		configRecord.CanonicalBytes,
	); err != nil {
		return fmt.Errorf("CONFIG is not action-binding-config/v1")
	}

	authorityRecord, err := requirePublicationJSONContent(
		ctx,
		queryer,
		binding.AuthorityCeilingRef,
		ContentAuthorityCeiling,
	)
	if err != nil {
		return err
	}
	if _, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	); err != nil {
		return fmt.Errorf(
			"authority ceiling is not action-authority-ceiling/v1",
		)
	}
	return nil
}

func requirePublicationJSONContent(
	ctx context.Context,
	queryer publicationQueryer,
	digest string,
	kind ContentKind,
) (ContentRecord, error) {
	record, err := requirePublicationContent(ctx, queryer, digest, kind)
	if err != nil {
		return ContentRecord{}, err
	}
	if record.MediaType != admissionJSONMediaType {
		return ContentRecord{}, fmt.Errorf(
			"%s content is not application/json",
			kind,
		)
	}
	return record, nil
}

func verifyControlModelProfiles(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	for _, profileDefinition := range control.Profiles {
		if profileDefinition.ModelProfile == nil {
			continue
		}
		ref := *profileDefinition.ModelProfile
		profileRecord, err := requirePublicationContent(
			ctx,
			queryer,
			ref.Digest,
			ContentConfig,
		)
		if err != nil || profileRecord.MediaType != admissionJSONMediaType {
			return fmt.Errorf(
				"%w: ModelProfile %s CONFIG is unavailable",
				ErrPublicationConflict,
				ref.Digest,
			)
		}
		profile, err := corecontract.RestoreModelProfileV1(
			profileRecord.CanonicalBytes,
			ref,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: ModelProfile %s is not model-profile/v1: %v",
				ErrPublicationConflict,
				ref.Digest,
				err,
			)
		}

		var request *controlcontract.BindingSpec
		for index := range profileDefinition.Bindings {
			candidate := &profileDefinition.Bindings[index]
			if candidate.Port.Name != moduleapi.PortNameModelGenerate ||
				candidate.Port.ExactVersion != moduleapi.PortVersionV2 {
				continue
			}
			if request != nil {
				return fmt.Errorf(
					"%w: profiled assembly %s has more than one model.generate/v2 Binding",
					ErrPublicationConflict,
					profileDefinition.Profile.ID,
				)
			}
			request = candidate
		}
		if request == nil {
			return fmt.Errorf(
				"%w: profiled assembly %s lacks model.generate/v2",
				ErrPublicationConflict,
				profileDefinition.Profile.ID,
			)
		}
		entry, found := catalog.FindInstance(request.InstanceID)
		if !found || !containsExactPort(entry.Provides, request.Port) {
			return fmt.Errorf(
				"%w: profiled model Binding is absent from Catalog",
				ErrPublicationConflict,
			)
		}
		binding := moduleapi.PortBinding{
			Provider:            entry.Activation,
			ConfigRef:           request.ConfigRef,
			AuthorityCeilingRef: request.AuthorityCeilingRef,
			StaticContextRefs: append(
				[]string{},
				request.StaticContextRefs...,
			),
			FailurePolicy: request.FailurePolicy,
		}
		configRecord, err := requirePublicationContent(
			ctx,
			queryer,
			binding.ConfigRef,
			ContentConfig,
		)
		if err != nil || configRecord.MediaType != admissionJSONMediaType {
			return fmt.Errorf(
				"%w: profiled model Binding CONFIG is unavailable",
				ErrPublicationConflict,
			)
		}
		config, err := moduleapi.RestoreModelBindingConfigV2(
			configRecord.CanonicalBytes,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: profiled model Binding CONFIG is invalid: %v",
				ErrPublicationConflict,
				err,
			)
		}
		if err := corecontract.ValidateModelProfileBindingV1(
			profile,
			binding,
			config,
		); err != nil {
			return fmt.Errorf(
				"%w: ModelProfile target mismatch: %v",
				ErrPublicationConflict,
				err,
			)
		}

		policyRecord, err := requirePublicationContent(
			ctx,
			queryer,
			profileDefinition.ContextPolicy.Digest,
			ContentPolicy,
		)
		if err != nil {
			return err
		}
		document, err := corecontract.RestorePolicyDocument(
			policyRecord.CanonicalBytes,
			profileDefinition.ContextPolicy,
		)
		if err != nil || document.PolicyType != corecontract.PolicyContext {
			return fmt.Errorf(
				"%w: profiled ContextPolicy is invalid",
				ErrPublicationConflict,
			)
		}
		contextPolicy, err := corecontract.RestoreContextPolicyV1(document.Body)
		if err != nil {
			return fmt.Errorf(
				"%w: profiled context-policy/v1 is invalid: %v",
				ErrPublicationConflict,
				err,
			)
		}
		if _, err := corecontract.TightenContextPolicyV1ForModelProfile(
			contextPolicy,
			profile,
		); err != nil {
			return fmt.Errorf(
				"%w: ModelProfile context ceiling is invalid: %v",
				ErrPublicationConflict,
				err,
			)
		}
	}
	return nil
}

func sameExactPortSet(
	left []moduleapi.PortRef,
	right []moduleapi.PortRef,
) bool {
	if len(left) != len(right) {
		return false
	}
	for _, port := range left {
		if !containsExactPort(right, port) {
			return false
		}
	}
	return true
}

func containsExactPort(
	ports []moduleapi.PortRef,
	wanted moduleapi.PortRef,
) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func insertOrVerifyControlSnapshot(
	ctx context.Context,
	connection *sql.Conn,
	control controlcontract.ControlSnapshot,
	ref controlcontract.ControlSnapshotRef,
	canonical []byte,
	publishedAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO control_snapshots(
			snapshot_id,
			tenant_id,
			revision,
			canonical_json,
			digest,
			published_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`,
		ref.SnapshotID,
		control.TenantID,
		int64(ref.Revision),
		canonical,
		ref.Digest,
		publishedAt,
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert immutable ControlSnapshot: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect ControlSnapshot insert: %w",
			err,
		)
	}
	if affected != 0 && affected != 1 {
		return fmt.Errorf(
			"%w: ControlSnapshot insert affected %d rows",
			ErrPublicationConflict,
			affected,
		)
	}
	return verifyStoredControlSnapshot(
		ctx,
		connection,
		control,
		ref,
		canonical,
	)
}

func insertOrVerifyCatalogGeneration(
	ctx context.Context,
	connection *sql.Conn,
	catalog controlcontract.CatalogGeneration,
	ref controlcontract.CatalogGenerationRef,
	canonical []byte,
	publishedAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO runtime_catalog_generations(
			generation_id,
			tenant_id,
			generation,
			control_snapshot_id,
			canonical_json,
			digest,
			published_at
		) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`,
		ref.GenerationID,
		catalog.TenantID,
		int64(ref.Generation),
		catalog.ControlSnapshotID,
		canonical,
		ref.Digest,
		publishedAt,
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert immutable CatalogGeneration: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect CatalogGeneration insert: %w",
			err,
		)
	}
	if affected != 0 && affected != 1 {
		return fmt.Errorf(
			"%w: CatalogGeneration insert affected %d rows",
			ErrPublicationConflict,
			affected,
		)
	}
	return verifyStoredCatalogGeneration(
		ctx,
		connection,
		catalog,
		ref,
		canonical,
	)
}

func verifyStoredControlSnapshot(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	ref controlcontract.ControlSnapshotRef,
	canonical []byte,
) error {
	var (
		tenant      string
		revision    int64
		stored      []byte
		digest      string
		publishedAt int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id, revision, canonical_json, digest, published_at
		FROM control_snapshots
		WHERE snapshot_id=?
	`, ref.SnapshotID).Scan(
		&tenant,
		&revision,
		&stored,
		&digest,
		&publishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf(
			"%w: immutable ControlSnapshot %s is absent",
			ErrPublicationConflict,
			ref.SnapshotID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: read immutable ControlSnapshot: %w",
			err,
		)
	}
	if tenant != control.TenantID ||
		revision != int64(ref.Revision) ||
		digest != ref.Digest ||
		publishedAt <= 0 ||
		!bytes.Equal(stored, canonical) {
		return fmt.Errorf(
			"%w: immutable ControlSnapshot %s differs",
			ErrPublicationConflict,
			ref.SnapshotID,
		)
	}
	return nil
}

func verifyStoredCatalogGeneration(
	ctx context.Context,
	queryer publicationQueryer,
	catalog controlcontract.CatalogGeneration,
	ref controlcontract.CatalogGenerationRef,
	canonical []byte,
) error {
	var (
		tenant            string
		generation        int64
		controlSnapshotID string
		stored            []byte
		digest            string
		publishedAt       int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id, generation, control_snapshot_id,
		       canonical_json, digest, published_at
		FROM runtime_catalog_generations
		WHERE generation_id=?
	`, ref.GenerationID).Scan(
		&tenant,
		&generation,
		&controlSnapshotID,
		&stored,
		&digest,
		&publishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf(
			"%w: immutable CatalogGeneration %s is absent",
			ErrPublicationConflict,
			ref.GenerationID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: read immutable CatalogGeneration: %w",
			err,
		)
	}
	if tenant != catalog.TenantID ||
		generation != int64(ref.Generation) ||
		controlSnapshotID != catalog.ControlSnapshotID ||
		digest != ref.Digest ||
		publishedAt <= 0 ||
		!bytes.Equal(stored, canonical) {
		return fmt.Errorf(
			"%w: immutable CatalogGeneration %s differs",
			ErrPublicationConflict,
			ref.GenerationID,
		)
	}
	return nil
}

func queryCurrentControlPointer(
	ctx context.Context,
	queryer publicationQueryer,
	tenantID string,
) (currentControlPointer, bool, error) {
	var (
		pointer  currentControlPointer
		revision int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id, snapshot_id, catalog_generation_id, pointer_revision
		FROM control_current
		WHERE tenant_id=?
	`, tenantID).Scan(
		&pointer.TenantID,
		&pointer.SnapshotID,
		&pointer.CatalogGenerationID,
		&revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return currentControlPointer{}, false, nil
	}
	if err != nil {
		return currentControlPointer{}, false, fmt.Errorf(
			"currentstore: read current Control/Catalog pointer: %w",
			err,
		)
	}
	if revision <= 0 {
		return currentControlPointer{}, false, fmt.Errorf(
			"%w: current pointer has invalid revision",
			ErrPublicationConflict,
		)
	}
	pointer.PointerRevision = uint64(revision)
	return pointer, true, nil
}

func requireOnePublicationRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("currentstore: inspect %s: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf(
			"%w: %s affected %d rows",
			ErrPublicationConflict,
			operation,
			affected,
		)
	}
	return nil
}

type publicationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// verifyControlCatalogPublicationClosureV1 is the sole no-write semantic gate
// shared by first publication, exact retry, public verification and Backup.
// It derives Requires and permission grants from existing immutable facts and
// never persists a second dependency or grant source.
func verifyControlCatalogPublicationClosureV1(
	ctx context.Context,
	queryer publicationQueryer,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if control.TenantID == "" || control.TenantID != catalog.TenantID ||
		catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest {
		return fmt.Errorf(
			"%w: current Control/Catalog identity closure differs",
			ErrPublicationConflict,
		)
	}
	if err := verifyControlContentClosure(ctx, queryer, control); err != nil {
		return err
	}
	if err := verifyCatalogActivationClosure(ctx, queryer, catalog); err != nil {
		return err
	}
	if err := verifyControlBindingsAgainstCatalog(control, catalog); err != nil {
		return err
	}
	if err := verifyControlPortBindingContracts(ctx, queryer, control, catalog); err != nil {
		return err
	}
	if err := verifyControlKnowledgeBindings(ctx, queryer, control, catalog); err != nil {
		return err
	}
	if err := verifyControlModelProfiles(ctx, queryer, control, catalog); err != nil {
		return err
	}
	if err := verifyBoundProfileModuleGraphV1(ctx, queryer, control, catalog); err != nil {
		return err
	}
	if err := verifyWorkspaceChannelEndpointModuleDeclarationsV1(
		ctx,
		queryer,
		control,
		catalog,
	); err != nil {
		return err
	}
	return nil
}

// VerifyPublishedControlCatalogClosureV1 replays the complete read-only
// publication gate for an already-current Control/Catalog pair. Backup and
// restore use it so a current binding that has not produced a Run yet still
// proves its Content, activation, Port, authority, Knowledge, and optional
// ModelProfile closure. It performs no writes and constructs no Adapter.
func VerifyPublishedControlCatalogClosureV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if ctx == nil || queryer == nil {
		return fmt.Errorf("%w: publication verification input is nil", ErrInvalidPublication)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return verifyControlCatalogPublicationClosureV1(
		ctx,
		queryer,
		control,
		catalog,
	)
}
