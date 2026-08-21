package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyCommitBoundaryFailureCodesOverrideCancellation(t *testing.T) {
	t.Parallel()

	for _, code := range []moduleApplyFailureCodeV1{
		moduleApplyFailureOutcomeUnknown,
		moduleApplyFailurePointer,
	} {
		err := newModuleApplyFailureV1(
			code,
			errors.Join(context.Canceled, errors.New("commit boundary evidence")),
		)
		if got := moduleApplyFailureCodeOfV1(err); got != code {
			t.Fatalf("failure code=%q want %q", got, code)
		}
	}
}

func TestModuleApplyRejectsDuplicateExistingPublicActionIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Store: %v", err)
		}
	}()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "duplicate.action",
				ProviderActionID: "provider.action",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
			}},
			Parameters: []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		moduleApplyJSONMediaType,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutContent(ctx, currentstore.ContentInput{
		Digest:         configRef,
		Kind:           currentstore.ContentConfig,
		MediaType:      moduleApplyJSONMediaType,
		CanonicalBytes: config,
	}); err != nil {
		t.Fatal(err)
	}
	bindings := []controlcontract.BindingSpec{
		{Port: productionActionPort, InstanceID: "one", ConfigRef: configRef},
		{Port: productionActionPort, InstanceID: "two", ConfigRef: configRef},
	}
	err = rejectDuplicateModuleApplyPublicActionsV1(
		ctx,
		store,
		bindings,
		config,
	)
	if err == nil || !strings.Contains(err.Error(), "existing public Action ID") {
		t.Fatalf("duplicate existing public Action IDs error=%v", err)
	}
}

func TestModuleApplyAuthorityAcceptsSoleWorkspaceWildcard(t *testing.T) {
	t.Parallel()

	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "text.stats",
				ProviderActionID: "text.stats",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
			}},
			Parameters: []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := moduleApplyPlanV1{
		TenantID: defaultTenantID,
		Port:     productionActionPort,
		Binding: &moduleApplyBindingV1{
			Config:           config,
			AuthorityCeiling: authority,
			FailurePolicy:    moduleapi.FailureRequired,
		},
	}
	if err := validateModuleApplyAuthorityV1(
		plan,
		controlcontract.ControlSnapshot{},
	); err != nil {
		t.Fatalf("sole Workspace wildcard was rejected: %v", err)
	}
}

func TestModuleApplyPreCommitCancellationUsesCancelledCode(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		errors.Join(errors.New("wrapped cancellation"), context.Canceled),
		errors.Join(errors.New("wrapped deadline"), context.DeadlineExceeded),
	} {
		err := newModuleApplyFailureV1(moduleApplyFailureArtifact, cause)
		if got := moduleApplyFailureCodeOfV1(err); got != moduleApplyFailureCancelled {
			t.Fatalf("failure code=%q want %q for cause %v", got, moduleApplyFailureCancelled, cause)
		}
	}
}
