package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidModule identifies an invalid install or activation request.
	ErrInvalidModule = errors.New("currentstore: invalid module")

	// ErrModuleConflict identifies an immutable installation or activation
	// identity that is already bound to different facts.
	ErrModuleConflict = errors.New("currentstore: module identity conflict")

	// ErrModuleInstallationNotFound distinguishes a missing installation from
	// an invalid request or storage failure.
	ErrModuleInstallationNotFound = errors.New(
		"currentstore: module installation not found",
	)

	// ErrModuleActivationNotFound distinguishes a missing activation from an
	// invalid request or storage failure.
	ErrModuleActivationNotFound = errors.New(
		"currentstore: module activation not found",
	)
)

const moduleManifestMediaType = "application/json"

// InstallModuleInput contains the immutable package identity accepted by the
// Current Store. ExpectedManifestRef is the caller's assertion of the
// MODULE_MANIFEST ContentRecord key and is independently recomputed.
type InstallModuleInput struct {
	InstallationID      string
	ModuleID            string
	ExactVersion        string
	ExpectedManifestRef string
	ManifestBytes       []byte
	ArtifactDigest      string
}

// ModuleInstallation is the immutable installed package record together with
// its recoverable canonical manifest bytes.
type ModuleInstallation struct {
	InstallationID string
	ModuleID       string
	ExactVersion   string
	ManifestRef    string
	ManifestBytes  []byte
	ArtifactDigest string
	InstalledAt    time.Time
}

// ActivateModuleInput contains only Core-assigned activation facts. The
// module manifest is deliberately unable to supply any of these final values.
type ActivateModuleInput struct {
	ActivationID       string
	TenantID           string
	InstanceID         string
	InstallationID     string
	ActivationRevision uint64
	ExecutionClass     moduleapi.ExecutionClass
	AdapterIdentity    string
}

// ModuleActivation is an immutable locally assigned execution instance.
type ModuleActivation struct {
	ActivationID       string
	TenantID           string
	InstanceID         string
	InstallationID     string
	ActivationRevision uint64
	ExecutionClass     moduleapi.ExecutionClass
	AdapterIdentity    string
	ActivatedAt        time.Time
}

// InstallModule strictly parses the package manifest, writes its immutable
// MODULE_MANIFEST ContentRecord, and inserts the installation in one
// BEGIN IMMEDIATE transaction. No manifest request grants execution authority.
func (store *Store) InstallModule(
	ctx context.Context,
	input InstallModuleInput,
) (ModuleInstallation, error) {
	if ctx == nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleInstallation{}, err
	}
	defer unlock()

	if err := validateOpaqueModuleValue(
		"installation ID",
		input.InstallationID,
		256,
	); err != nil {
		return ModuleInstallation{}, err
	}
	if err := validateDigest(input.ArtifactDigest); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: artifact digest: %v",
			ErrInvalidModule,
			err,
		)
	}
	manifest, canonicalManifest, err := moduleapi.ParseModuleManifestV1(
		bytes.Clone(input.ManifestBytes),
	)
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: parse manifest: %v",
			ErrInvalidModule,
			err,
		)
	}
	if manifest.ID != input.ModuleID ||
		manifest.Version != input.ExactVersion {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: manifest identity %s@%s does not match install identity %s@%s",
			ErrInvalidModule,
			manifest.ID,
			manifest.Version,
			input.ModuleID,
			input.ExactVersion,
		)
	}
	manifestDigest, err := ComputeContentDigest(
		ContentModuleManifest,
		moduleManifestMediaType,
		canonicalManifest,
	)
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: compute manifest digest: %v",
			ErrInvalidModule,
			err,
		)
	}
	if err := validateDigest(input.ExpectedManifestRef); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: expected manifest ref: %v",
			ErrInvalidModule,
			err,
		)
	}
	if input.ExpectedManifestRef != manifestDigest {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: expected manifest ref %s does not match computed ref %s",
			ErrInvalidModule,
			input.ExpectedManifestRef,
			manifestDigest,
		)
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: acquire install connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: begin InstallModule: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	// A materialized Learning Version owns the same global ModuleRef identity.
	// Check that closure before even writing reusable manifest content so a
	// conflicting install is a zero-write failure in either operation order.
	if err := requireInstallationCompatibleWithLearningMaterialization(
		ctx,
		connection,
		moduleapi.Ref{ID: input.ModuleID, Version: input.ExactVersion},
		canonicalManifest,
		input.ArtifactDigest,
	); err != nil {
		return ModuleInstallation{}, err
	}
	if err := requireModuleRefCompatibleWithDiscovery(
		ctx,
		connection,
		moduleapi.Ref{ID: input.ModuleID, Version: input.ExactVersion},
		input.ArtifactDigest,
	); err != nil {
		return ModuleInstallation{}, err
	}

	installedAtMicros := nowUnixMicro()
	installedAt, _ := timeFromUnixMicro(installedAtMicros)
	if err := putManifestContent(
		ctx,
		connection,
		manifestDigest,
		canonicalManifest,
		installedAt,
	); err != nil {
		return ModuleInstallation{}, err
	}
	result, err := connection.ExecContext(ctx, `
		INSERT INTO module_installations(
			installation_id,
			module_id,
			exact_version,
			manifest_ref,
			artifact_digest,
			installed_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`,
		input.InstallationID,
		input.ModuleID,
		input.ExactVersion,
		manifestDigest,
		input.ArtifactDigest,
		installedAtMicros,
	)
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: insert module installation: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: inspect module installation insert: %w",
			err,
		)
	}

	var installation ModuleInstallation
	switch affected {
	case 1:
		installation = ModuleInstallation{
			InstallationID: input.InstallationID,
			ModuleID:       input.ModuleID,
			ExactVersion:   input.ExactVersion,
			ManifestRef:    manifestDigest,
			ManifestBytes:  bytes.Clone(canonicalManifest),
			ArtifactDigest: input.ArtifactDigest,
			InstalledAt:    installedAt,
		}
	case 0:
		installation, err = queryModuleInstallationByIdentity(
			ctx,
			connection,
			input.ModuleID,
			input.ExactVersion,
		)
		if errors.Is(err, ErrModuleInstallationNotFound) {
			if _, idErr := queryModuleInstallationByID(
				ctx,
				connection,
				input.InstallationID,
			); idErr == nil {
				return ModuleInstallation{}, fmt.Errorf(
					"%w: installation ID %s is already used",
					ErrModuleConflict,
					input.InstallationID,
				)
			} else if !errors.Is(idErr, ErrModuleInstallationNotFound) {
				return ModuleInstallation{}, idErr
			}
			return ModuleInstallation{}, fmt.Errorf(
				"%w: insertion was rejected without an existing identity",
				ErrModuleConflict,
			)
		}
		if err != nil {
			return ModuleInstallation{}, err
		}
		if installation.InstallationID != input.InstallationID ||
			installation.ManifestRef != manifestDigest ||
			installation.ArtifactDigest != input.ArtifactDigest ||
			!bytes.Equal(installation.ManifestBytes, canonicalManifest) {
			return ModuleInstallation{}, fmt.Errorf(
				"%w: module %s@%s is already installed with different bytes",
				ErrModuleConflict,
				input.ModuleID,
				input.ExactVersion,
			)
		}
	default:
		return ModuleInstallation{}, fmt.Errorf(
			"%w: install affected %d rows",
			ErrModuleConflict,
			affected,
		)
	}

	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: commit InstallModule: %w",
			err,
		)
	}
	committed = true
	return cloneModuleInstallation(installation), nil
}

// GetModuleInstallation returns one installation and a detached copy of its
// canonical manifest bytes.
func (store *Store) GetModuleInstallation(
	ctx context.Context,
	installationID string,
) (ModuleInstallation, error) {
	if ctx == nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleInstallation{}, err
	}
	defer unlock()
	if err := validateOpaqueModuleValue(
		"installation ID",
		installationID,
		256,
	); err != nil {
		return ModuleInstallation{}, err
	}
	installation, err := queryModuleInstallationByID(
		ctx,
		store.db,
		installationID,
	)
	if err != nil {
		return ModuleInstallation{}, err
	}
	return cloneModuleInstallation(installation), nil
}

// GetModuleInstallationByIdentity returns the one immutable installation for
// an exact module ID and version together with a detached copy of its
// canonical manifest bytes.
func (store *Store) GetModuleInstallationByIdentity(
	ctx context.Context,
	moduleID string,
	exactVersion string,
) (ModuleInstallation, error) {
	if ctx == nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleInstallation{}, err
	}
	defer unlock()
	identity := moduleapi.Ref{ID: moduleID, Version: exactVersion}
	if err := identity.Validate(); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: module identity: %v",
			ErrInvalidModule,
			err,
		)
	}
	installation, err := queryModuleInstallationByIdentity(
		ctx,
		store.db,
		moduleID,
		exactVersion,
	)
	if err != nil {
		return ModuleInstallation{}, err
	}
	return cloneModuleInstallation(installation), nil
}

// ActivateModule stages a Core-assigned execution class and adapter
// identity. The immutable row grants no runtime availability until an exact
// CatalogGeneration is published. The requested runtime mode bounds which
// execution class Core may assign; it does not grant trust, control, failure
// policy, adapter identity or any other authority.
func (store *Store) ActivateModule(
	ctx context.Context,
	input ActivateModuleInput,
) (ModuleActivation, error) {
	if ctx == nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleActivation{}, err
	}
	defer unlock()
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "activation ID", value: input.ActivationID},
		{label: "tenant ID", value: input.TenantID},
		{label: "instance ID", value: input.InstanceID},
		{label: "installation ID", value: input.InstallationID},
		{label: "adapter identity", value: input.AdapterIdentity},
	} {
		if err := validateOpaqueModuleValue(
			field.label,
			field.value,
			256,
		); err != nil {
			return ModuleActivation{}, err
		}
	}
	if input.ActivationRevision == 0 ||
		input.ActivationRevision > math.MaxInt64 {
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation revision must be between 1 and %d",
			ErrInvalidModule,
			uint64(math.MaxInt64),
		)
	}
	switch input.ExecutionClass {
	case moduleapi.ExecutionDeclarative,
		moduleapi.ExecutionTrustedInProcess,
		moduleapi.ExecutionLocalProcess,
		moduleapi.ExecutionRemote,
		moduleapi.ExecutionWASM:
	default:
		return ModuleActivation{}, fmt.Errorf(
			"%w: execution class %q is not supported",
			ErrInvalidModule,
			input.ExecutionClass,
		)
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: acquire activation connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: begin ActivateModule: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	installation, err := queryModuleInstallationByID(
		ctx,
		connection,
		input.InstallationID,
	)
	if err != nil {
		return ModuleActivation{}, err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: installed module manifest is invalid: %v",
			ErrModuleConflict,
			err,
		)
	}
	expectedClass, supported := executionClassForRuntimeRequest(
		manifest.Runtime.Mode,
	)
	if !supported {
		return ModuleActivation{}, fmt.Errorf(
			"%w: runtime request %q cannot be activated",
			ErrInvalidModule,
			manifest.Runtime.Mode,
		)
	}
	if input.ExecutionClass != expectedClass {
		return ModuleActivation{}, fmt.Errorf(
			"%w: runtime request %q requires execution class %q, got %q",
			ErrInvalidModule,
			manifest.Runtime.Mode,
			expectedClass,
			input.ExecutionClass,
		)
	}

	activatedAtMicros := nowUnixMicro()
	activatedAt, _ := timeFromUnixMicro(activatedAtMicros)
	result, err := connection.ExecContext(ctx, `
		INSERT INTO module_activations(
			activation_id,
			tenant_id,
			instance_id,
			installation_id,
			activation_revision,
			execution_class,
			adapter_identity,
			activated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`,
		input.ActivationID,
		input.TenantID,
		input.InstanceID,
		input.InstallationID,
		int64(input.ActivationRevision),
		string(input.ExecutionClass),
		input.AdapterIdentity,
		activatedAtMicros,
	)
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: insert module activation: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: inspect module activation insert: %w",
			err,
		)
	}

	var activation ModuleActivation
	switch affected {
	case 1:
		activation = ModuleActivation{
			ActivationID:       input.ActivationID,
			TenantID:           input.TenantID,
			InstanceID:         input.InstanceID,
			InstallationID:     input.InstallationID,
			ActivationRevision: input.ActivationRevision,
			ExecutionClass:     input.ExecutionClass,
			AdapterIdentity:    input.AdapterIdentity,
			ActivatedAt:        activatedAt,
		}
	case 0:
		activation, err = queryModuleActivationByIdentity(
			ctx,
			connection,
			input.TenantID,
			input.InstanceID,
			input.ActivationRevision,
		)
		if errors.Is(err, ErrModuleActivationNotFound) {
			if _, idErr := queryModuleActivationByID(
				ctx,
				connection,
				input.ActivationID,
			); idErr == nil {
				return ModuleActivation{}, fmt.Errorf(
					"%w: activation ID %s is already used",
					ErrModuleConflict,
					input.ActivationID,
				)
			} else if !errors.Is(idErr, ErrModuleActivationNotFound) {
				return ModuleActivation{}, idErr
			}
			return ModuleActivation{}, fmt.Errorf(
				"%w: activation insertion was rejected without an existing identity",
				ErrModuleConflict,
			)
		}
		if err != nil {
			return ModuleActivation{}, err
		}
		if activation.ActivationID != input.ActivationID ||
			activation.InstallationID != input.InstallationID ||
			activation.ExecutionClass != input.ExecutionClass ||
			activation.AdapterIdentity != input.AdapterIdentity {
			return ModuleActivation{}, fmt.Errorf(
				"%w: activation %s/%s/%d already has different Core assignments",
				ErrModuleConflict,
				input.TenantID,
				input.InstanceID,
				input.ActivationRevision,
			)
		}
	default:
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation affected %d rows",
			ErrModuleConflict,
			affected,
		)
	}

	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: commit ActivateModule: %w",
			err,
		)
	}
	committed = true
	return activation, nil
}

// executionClassForRuntimeRequest is a protocol-shape check, not an authority
// assignment. Core still owns the activation row and adapter identity.
func executionClassForRuntimeRequest(
	mode moduleapi.RuntimeModeRequest,
) (moduleapi.ExecutionClass, bool) {
	switch mode {
	case moduleapi.RuntimeModeRequestDeclarative:
		return moduleapi.ExecutionDeclarative, true
	case moduleapi.RuntimeModeRequestTrustedInProcess:
		return moduleapi.ExecutionTrustedInProcess, true
	case moduleapi.RuntimeModeRequestLocalProcess:
		return moduleapi.ExecutionLocalProcess, true
	case moduleapi.RuntimeModeRequestRemote:
		return moduleapi.ExecutionRemote, true
	case moduleapi.RuntimeModeRequestWASM:
		return moduleapi.ExecutionWASM, true
	default:
		return "", false
	}
}

// GetModuleActivation returns one immutable local activation.
func (store *Store) GetModuleActivation(
	ctx context.Context,
	activationID string,
) (ModuleActivation, error) {
	if ctx == nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleActivation{}, err
	}
	defer unlock()
	if err := validateOpaqueModuleValue(
		"activation ID",
		activationID,
		256,
	); err != nil {
		return ModuleActivation{}, err
	}
	activation, err := queryModuleActivationByID(
		ctx,
		store.db,
		activationID,
	)
	if err != nil {
		return ModuleActivation{}, err
	}
	if err := verifyModuleActivationInstallationClosure(
		ctx,
		store.db,
		activation,
	); err != nil {
		return ModuleActivation{}, err
	}
	return activation, nil
}

// GetModuleActivationByIdentity returns one exact immutable activation for a
// tenant-scoped instance and revision. A later unbound activation is inert and
// does not replace the exact revision frozen into a Catalog entry.
func (store *Store) GetModuleActivationByIdentity(
	ctx context.Context,
	tenantID string,
	instanceID string,
	activationRevision uint64,
) (ModuleActivation, error) {
	if ctx == nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleActivation{}, err
	}
	defer unlock()
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "tenant ID", value: tenantID},
		{label: "instance ID", value: instanceID},
	} {
		if err := validateOpaqueModuleValue(
			field.label,
			field.value,
			256,
		); err != nil {
			return ModuleActivation{}, err
		}
	}
	if activationRevision == 0 || activationRevision > math.MaxInt64 {
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation revision must be between 1 and %d",
			ErrInvalidModule,
			uint64(math.MaxInt64),
		)
	}
	activation, err := queryModuleActivationByIdentity(
		ctx,
		store.db,
		tenantID,
		instanceID,
		activationRevision,
	)
	if err != nil {
		return ModuleActivation{}, err
	}
	if err := verifyModuleActivationInstallationClosure(
		ctx,
		store.db,
		activation,
	); err != nil {
		return ModuleActivation{}, err
	}
	return activation, nil
}

// GetLatestModuleActivationForInstance returns the activation with the
// greatest revision for one exact tenant-scoped instance. The activation is
// still immutable and need not be reachable from the current Catalog.
func (store *Store) GetLatestModuleActivationForInstance(
	ctx context.Context,
	tenantID string,
	instanceID string,
) (ModuleActivation, error) {
	if ctx == nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModule,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleActivation{}, err
	}
	defer unlock()
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "tenant ID", value: tenantID},
		{label: "instance ID", value: instanceID},
	} {
		if err := validateOpaqueModuleValue(
			field.label,
			field.value,
			256,
		); err != nil {
			return ModuleActivation{}, err
		}
	}
	activation, err := queryLatestModuleActivationForInstance(
		ctx,
		store.db,
		tenantID,
		instanceID,
	)
	if err != nil {
		return ModuleActivation{}, err
	}
	if err := verifyModuleActivationInstallationClosure(
		ctx,
		store.db,
		activation,
	); err != nil {
		return ModuleActivation{}, err
	}
	return activation, nil
}

func putManifestContent(
	ctx context.Context,
	connection *sql.Conn,
	digest string,
	canonical []byte,
	createdAt time.Time,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO content_records(
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(content_digest) DO NOTHING
	`,
		digest,
		string(ContentModuleManifest),
		moduleManifestMediaType,
		canonical,
		len(canonical),
		createdAt.UnixMicro(),
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert module manifest content: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect module manifest insert: %w",
			err,
		)
	}
	switch affected {
	case 1:
		return nil
	case 0:
		record, err := queryContent(ctx, connection, digest)
		if err != nil {
			return err
		}
		if record.Kind != ContentModuleManifest ||
			record.MediaType != moduleManifestMediaType ||
			record.SizeBytes != int64(len(canonical)) ||
			!bytes.Equal(record.CanonicalBytes, canonical) {
			return fmt.Errorf(
				"%w: stored module manifest %s differs",
				ErrContentIntegrity,
				digest,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: module manifest insert affected %d rows",
			ErrContentIntegrity,
			affected,
		)
	}
}

func queryModuleInstallationByID(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	installationID string,
) (ModuleInstallation, error) {
	return queryModuleInstallation(
		ctx,
		queryer,
		`installation_id=?`,
		installationID,
	)
}

func queryModuleInstallationByIdentity(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	moduleID string,
	exactVersion string,
) (ModuleInstallation, error) {
	return queryModuleInstallation(
		ctx,
		queryer,
		`module_id=? AND exact_version=?`,
		moduleID,
		exactVersion,
	)
}

func queryModuleInstallation(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	predicate string,
	arguments ...any,
) (ModuleInstallation, error) {
	var (
		installation ModuleInstallation
		installedAt  int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			installation_id,
			module_id,
			exact_version,
			manifest_ref,
			artifact_digest,
			installed_at
		FROM module_installations
		WHERE `+predicate,
		arguments...,
	).Scan(
		&installation.InstallationID,
		&installation.ModuleID,
		&installation.ExactVersion,
		&installation.ManifestRef,
		&installation.ArtifactDigest,
		&installedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModuleInstallation{}, ErrModuleInstallationNotFound
	}
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"currentstore: read module installation: %w",
			err,
		)
	}
	content, err := queryContent(ctx, queryer, installation.ManifestRef)
	if errors.Is(err, ErrContentNotFound) {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s manifest content is missing",
			ErrContentIntegrity,
			installation.InstallationID,
		)
	}
	if err != nil {
		return ModuleInstallation{}, err
	}
	if content.Kind != ContentModuleManifest ||
		content.MediaType != moduleManifestMediaType {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s has an invalid manifest content record",
			ErrContentIntegrity,
			installation.InstallationID,
		)
	}
	installation.ManifestBytes = bytes.Clone(content.CanonicalBytes)
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil ||
		!bytes.Equal(canonical, installation.ManifestBytes) ||
		manifest.ID != installation.ModuleID ||
		manifest.Version != installation.ExactVersion {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s manifest does not match its identity",
			ErrModuleConflict,
			installation.InstallationID,
		)
	}
	computedDigest, err := ComputeContentDigest(
		ContentModuleManifest,
		moduleManifestMediaType,
		installation.ManifestBytes,
	)
	if err != nil || computedDigest != installation.ManifestRef {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s manifest digest is invalid",
			ErrModuleConflict,
			installation.InstallationID,
		)
	}
	if err := validateDigest(installation.ArtifactDigest); err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s artifact digest is invalid",
			ErrModuleConflict,
			installation.InstallationID,
		)
	}
	installation.InstalledAt, err = timeFromUnixMicro(installedAt)
	if err != nil {
		return ModuleInstallation{}, fmt.Errorf(
			"%w: installation %s installed_at is invalid",
			ErrModuleConflict,
			installation.InstallationID,
		)
	}
	return cloneModuleInstallation(installation), nil
}

func queryModuleActivationByID(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	activationID string,
) (ModuleActivation, error) {
	return queryModuleActivation(
		ctx,
		queryer,
		`activation_id=?`,
		activationID,
	)
}

func queryModuleActivationByIdentity(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	instanceID string,
	revision uint64,
) (ModuleActivation, error) {
	return queryModuleActivation(
		ctx,
		queryer,
		`tenant_id=? AND instance_id=? AND activation_revision=?`,
		tenantID,
		instanceID,
		int64(revision),
	)
}

func queryLatestModuleActivationForInstance(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	instanceID string,
) (ModuleActivation, error) {
	return queryModuleActivation(
		ctx,
		queryer,
		`tenant_id=? AND instance_id=?
		 ORDER BY activation_revision DESC
		 LIMIT 1`,
		tenantID,
		instanceID,
	)
}

func queryModuleActivation(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	predicate string,
	arguments ...any,
) (ModuleActivation, error) {
	var (
		activation  ModuleActivation
		revision    int64
		class       string
		activatedAt int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			activation_id,
			tenant_id,
			instance_id,
			installation_id,
			activation_revision,
			execution_class,
			adapter_identity,
			activated_at
		FROM module_activations
		WHERE `+predicate,
		arguments...,
	).Scan(
		&activation.ActivationID,
		&activation.TenantID,
		&activation.InstanceID,
		&activation.InstallationID,
		&revision,
		&class,
		&activation.AdapterIdentity,
		&activatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModuleActivation{}, ErrModuleActivationNotFound
	}
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"currentstore: read module activation: %w",
			err,
		)
	}
	if revision <= 0 {
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation %s has invalid revision",
			ErrModuleConflict,
			activation.ActivationID,
		)
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "activation ID", value: activation.ActivationID},
		{label: "tenant ID", value: activation.TenantID},
		{label: "instance ID", value: activation.InstanceID},
		{label: "installation ID", value: activation.InstallationID},
		{label: "adapter identity", value: activation.AdapterIdentity},
	} {
		if err := validateOpaqueModuleValue(
			field.label,
			field.value,
			256,
		); err != nil {
			return ModuleActivation{}, fmt.Errorf(
				"%w: activation %s has invalid persisted %s",
				ErrModuleConflict,
				activation.ActivationID,
				field.label,
			)
		}
	}
	activation.ActivationRevision = uint64(revision)
	activation.ExecutionClass = moduleapi.ExecutionClass(class)
	switch activation.ExecutionClass {
	case moduleapi.ExecutionDeclarative,
		moduleapi.ExecutionTrustedInProcess,
		moduleapi.ExecutionLocalProcess,
		moduleapi.ExecutionRemote,
		moduleapi.ExecutionWASM:
	default:
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation %s has unsupported execution class",
			ErrModuleConflict,
			activation.ActivationID,
		)
	}
	activation.ActivatedAt, err = timeFromUnixMicro(activatedAt)
	if err != nil {
		return ModuleActivation{}, fmt.Errorf(
			"%w: activation %s activated_at is invalid",
			ErrModuleConflict,
			activation.ActivationID,
		)
	}
	return activation, nil
}

func verifyModuleActivationInstallationClosure(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	activation ModuleActivation,
) error {
	if _, err := queryModuleInstallationByID(
		ctx,
		queryer,
		activation.InstallationID,
	); err != nil {
		return fmt.Errorf(
			"%w: activation %s installation closure is invalid: %v",
			ErrModuleConflict,
			activation.ActivationID,
			err,
		)
	}
	return nil
}

func cloneModuleInstallation(
	installation ModuleInstallation,
) ModuleInstallation {
	installation.ManifestBytes = bytes.Clone(installation.ManifestBytes)
	return installation
}

func validateOpaqueModuleValue(
	label string,
	value string,
	maximumBytes int,
) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > maximumBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"%w: %s must be canonical UTF-8, non-empty, trimmed, and at most %d bytes",
			ErrInvalidModule,
			label,
			maximumBytes,
		)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf(
				"%w: %s contains an unsupported control character",
				ErrInvalidModule,
				label,
			)
		}
	}
	return nil
}
