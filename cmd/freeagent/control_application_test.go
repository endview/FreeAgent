package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestProductionControlModuleDisableDryRunExactRequestReceiptAndRetryV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare Control Disable: result=%+v err=%v", result, err)
	}

	store := openControlTestStoreV1(t, databasePath)
	basis, _, _, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := controlapp.PublishedPointerRefV1(
		controlapp.PublishedBasisRefV1(basis),
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, permit := newControlTestPermitV1(t, controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      defaultTenantID,
	})
	defer registry.Close()
	defer permit.Release()
	body := controlapp.ModuleDisableDryRunBodyV1{
		SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
		ExpectedPointerRevision: basis.PointerRevision,
		BindingTarget: controlapp.ModuleBindingTargetV1{
			Kind:      controlapp.ModuleBindingTargetProfileV1,
			ProfileID: moduleApplyTestProfileID,
		},
		InstanceID: fixture.InstanceID,
		Port:       productionContextPort,
	}
	service := &productionModuleDisableDryRunServiceV1{
		store:        store,
		artifactRoot: artifactRoot,
	}
	input := controlapp.ModuleDisableDryRunInputV1{
		Authorization:   permit,
		Scope:           permit.Scopes()[0],
		ExpectedPointer: expected,
		Body:            body,
		ObservedAt:      uint64(time.Now().UTC().UnixMicro()),
	}
	result, err := service.DryRunModuleDisableV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != controlapp.ModuleDisableDryRunResultSchemaVersionV1 ||
		result.Projection.Disposition != controlapp.ModuleDisableWouldApplyV1 ||
		result.Projection.CandidateState != controlapp.ModuleDisableProjectedNotReservedV1 ||
		result.Projection.BindingRemoval == nil ||
		result.Request.Operation != controlapicontract.OperationModuleDisableV1 ||
		result.Request.Intent != controlapicontract.OperationIntentDryRunV1 ||
		result.Request.ExpectedRef != expected ||
		result.Request.PrincipalID != permit.Session().PrincipalID ||
		result.Receipt.Status != controlapicontract.OperationStatusDryRunV1 ||
		result.Receipt.PreRef == nil || *result.Receipt.PreRef != expected ||
		result.Receipt.PostRef != nil || result.Receipt.DomainReceipt != nil ||
		result.Receipt.RequestDigest != result.RequestDigest ||
		result.RequestDigest == "" || result.ReceiptDigest == "" {
		t.Fatalf("unsafe Control Disable result: %+v", result)
	}
	after, _, _, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil || after != basis {
		t.Fatalf("Dry-run changed PublishedBasis: before=%+v after=%+v err=%v", basis, after, err)
	}

	wrong := input
	wrong.ExpectedPointer.Digest = moduleapi.Digest(
		"test.control-wrong-if-match/v1",
		[]byte("wrong"),
	)
	if _, err := service.DryRunModuleDisableV1(context.Background(), wrong); !errors.Is(err, controlapp.ErrRevisionConflict) {
		t.Fatalf("wrong If-Match error=%v", err)
	}
	flipping := &flippingControlAuthorizationV1{next: permit}
	changedAuthorization := input
	changedAuthorization.Authorization = flipping
	if _, err := service.DryRunModuleDisableV1(
		context.Background(),
		changedAuthorization,
	); !errors.Is(err, controlapp.ErrForbidden) {
		t.Fatalf("final authorization recheck error=%v", err)
	}
	expiredSession := permit.Session()
	expiredSession.ExpiresAtUnixMicros = expiredSession.IssuedAtUnixMicros + 1
	expiredAuthorization := &sessionOverrideControlAuthorizationV1{
		next:    permit,
		session: expiredSession,
	}
	expiredInput := input
	expiredInput.Authorization = expiredAuthorization
	expiredInput.ObservedAt = expiredSession.ExpiresAtUnixMicros
	if _, err := service.DryRunModuleDisableV1(
		context.Background(),
		expiredInput,
	); !errors.Is(err, controlapp.ErrSessionExpired) {
		t.Fatalf("expired Control session error=%v", err)
	}
	invalidSession := permit.Session()
	invalidSession.Capabilities = append(
		invalidSession.Capabilities,
		controlapicontract.CapabilityOperateModulesV1,
	)
	invalidAuthorization := &sessionOverrideControlAuthorizationV1{
		next:    permit,
		session: invalidSession,
	}
	invalidInput := input
	invalidInput.Authorization = invalidAuthorization
	if _, err := service.DryRunModuleDisableV1(
		context.Background(),
		invalidInput,
	); !errors.Is(err, controlapp.ErrInvalidRequest) {
		t.Fatalf("noncanonical Control session error=%v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	disableCanonical := newDisabledDeclarativeModuleApplyPlanV1(
		t,
		fixture.InstanceID,
		basis.PointerRevision,
	)
	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		disableCanonical,
	)
	if applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	); err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("apply Disable: result=%+v err=%v", applied, err)
	}
	store = openControlTestStoreV1(t, databasePath)
	service.store = store
	retry, err := service.DryRunModuleDisableV1(context.Background(), input)
	if err != nil {
		t.Fatalf("adjacent exact Dry-run retry: %v", err)
	}
	if retry.Projection.Disposition != controlapp.ModuleDisableAlreadyAppliedV1 ||
		retry.Projection.PreconditionBasis != controlapp.PublishedBasisRefV1(basis) ||
		retry.Receipt.PreRef == nil || *retry.Receipt.PreRef != expected {
		t.Fatalf("retry lost exact predecessor: %+v", retry)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionControlModuleDisableScopeAndStoreErrorsV1(t *testing.T) {
	for _, test := range []struct {
		input error
		want  error
	}{
		{context.Canceled, controlapp.ErrCancelled},
		{currentstore.ErrInvalidPublishedBasis, controlapp.ErrInvalidRequest},
		{currentstore.ErrPublishedBasisNotFound, controlapp.ErrNotFound},
		{currentstore.ErrPublishedBasisIntegrity, controlapp.ErrIntegrityFailure},
		{currentstore.ErrAdmissionIntegrity, controlapp.ErrIntegrityFailure},
		{currentstore.ErrOwnerActive, controlapp.ErrStoreBusy},
		{currentstore.ErrStoreClosed, controlapp.ErrStoreUnavailable},
		{errors.New("private sqlite path"), controlapp.ErrStoreUnavailable},
	} {
		if got := mapControlStoreReadErrorV1(test.input); !errors.Is(got, test.want) {
			t.Fatalf("map %v=%v want %v", test.input, got, test.want)
		}
	}
	for _, test := range []struct {
		input error
		want  error
	}{
		{currentstore.ErrOwnerActive, controlapp.ErrStoreBusy},
		{currentstore.ErrStoreClosed, controlapp.ErrStoreUnavailable},
		{currentstore.ErrPublishedBasisIntegrity, controlapp.ErrIntegrityFailure},
		{currentstore.ErrAdmissionIntegrity, controlapp.ErrIntegrityFailure},
		{currentstore.ErrInvalidPublishedBasis, controlapp.ErrInvalidRequest},
	} {
		if got := mapControlDisableFailureV1(test.input); !errors.Is(got, test.want) {
			t.Fatalf("map Disable %v=%v want %v", test.input, got, test.want)
		}
	}

	workspace := controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeWorkspaceV1,
		TenantID:      defaultTenantID,
		WorkspaceID:   "workspace-a",
	}
	profile := controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	}
	if err := authorizeModuleDisableTargetV1(workspace, profile); !errors.Is(err, controlapp.ErrNotFound) {
		t.Fatalf("Workspace/Profile target error=%v", err)
	}
	endpoint := controlapp.ModuleBindingTargetV1{
		Kind:        controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1,
		WorkspaceID: "workspace-b",
		EndpointID:  "endpoint-a",
	}
	if err := authorizeModuleDisableTargetV1(workspace, endpoint); !errors.Is(err, controlapp.ErrNotFound) {
		t.Fatalf("cross-Workspace endpoint error=%v", err)
	}
}

func openControlTestStoreV1(t *testing.T, path string) *currentstore.Store {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newControlTestPermitV1(
	t *testing.T,
	scope controlapicontract.ControlScopeV1,
) (*controlsession.RegistryV1, *controlsession.PermitV1) {
	t.Helper()
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(
		[]controlapicontract.ControlScopeV1{scope},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, bootstrap, err := controlsession.NewRegistryV1(
		controlsession.RegistryConfigV1{
			PrincipalID:           "operator-local",
			AuthorizationRevision: 1,
			Capabilities: []controlapicontract.ControlCapabilityV1{
				controlapicontract.CapabilityObserveV1,
				controlapicontract.CapabilityOperateModulesV1,
			},
			ScopeSet: scopeSet,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	clear(bootstrap.Capability)
	if err != nil {
		registry.Close()
		t.Fatal(err)
	}
	permit, err := registry.Admit(controlsession.AdmissionV1{
		SessionCredential: issued.SessionCredential,
		CSRFToken:         issued.CSRFToken,
		RequireCSRF:       true,
		Capability:        controlapicontract.CapabilityOperateModulesV1,
		Scope:             scope,
	})
	clear(issued.SessionCredential)
	clear(issued.CSRFToken)
	if err != nil {
		registry.Close()
		t.Fatal(err)
	}
	return registry, permit
}

type flippingControlAuthorizationV1 struct {
	next  controlapp.AuthorizationContextV1
	allow atomic.Int32
}

type sessionOverrideControlAuthorizationV1 struct {
	next    controlapp.AuthorizationContextV1
	session controlapicontract.ControlSessionV1
}

func (authorization *sessionOverrideControlAuthorizationV1) Session() controlapicontract.ControlSessionV1 {
	return authorization.session
}

func (authorization *sessionOverrideControlAuthorizationV1) Digest() string {
	return authorization.next.Digest()
}

func (authorization *sessionOverrideControlAuthorizationV1) Allows(
	scope controlapicontract.ControlScopeV1,
) bool {
	return authorization.next.Allows(scope)
}

func (authorization *flippingControlAuthorizationV1) Session() controlapicontract.ControlSessionV1 {
	return authorization.next.Session()
}

func (authorization *flippingControlAuthorizationV1) Digest() string {
	return authorization.next.Digest()
}

func (authorization *flippingControlAuthorizationV1) Allows(
	scope controlapicontract.ControlScopeV1,
) bool {
	return authorization.allow.Add(1) == 1 && authorization.next.Allows(scope)
}
