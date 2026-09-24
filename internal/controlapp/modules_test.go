package controlapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var _ AuthorizationContextV1 = (*controlsession.PermitV1)(nil)

func TestModuleInstanceIDValidationMatchesModuleAPIContractV1(t *testing.T) {
	t.Parallel()
	if !validModuleInstanceIDV1("a\u0080b") {
		t.Fatal("valid canonical module instance C1 ID was rejected")
	}
	for _, value := range []string{"", " instance-a", "a\u001fb", "a\u007fb"} {
		if validModuleInstanceIDV1(value) {
			t.Fatalf("invalid module instance ID accepted: %q", value)
		}
	}
}

func TestModulesServiceTenantAndWorkspaceProjectionV1(t *testing.T) {
	reader := newTestPublishedBasisReaderV1()
	service := mustTestModulesServiceV1(t, reader)
	tenantScope := testTenantScopeV1()
	tenantPermit := newTestAuthorizationV1("operator-a", tenantScope)
	tenantPage, err := service.ListModulesV1(
		context.Background(),
		ListModulesInputV1{
			Authorization: tenantPermit,
			Scope:         tenantScope,
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tenantPage.SchemaVersion != ModulesPageSchemaVersionV1 ||
		tenantPage.View.SchemaVersion !=
			controlapicontract.ControlViewSnapshotSchemaVersionV1 ||
		tenantPage.View.Scope != tenantScope ||
		tenantPage.View.ObservedAtUnixMicros != testObservedAtV1 {
		t.Fatalf("tenant projection metadata drifted: %+v", tenantPage)
	}
	gotIDs := moduleIDsFromPageV1(tenantPage)
	wantIDs := []string{
		testChannelAInstance,
		testChannelBInstance,
		testProfileInstance,
		testUnboundInstance,
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("tenant binary instance order = %v, want %v", gotIDs, wantIDs)
	}
	wantBindingCounts := map[string]uint32{
		testChannelAInstance: 1,
		testChannelBInstance: 1,
		testProfileInstance:  1,
		testUnboundInstance:  0,
	}
	for _, item := range tenantPage.Items {
		if item.VisibleBindingCount != wantBindingCounts[item.InstanceID] ||
			item.Provides == nil {
			t.Fatalf("tenant item changed: %+v", item)
		}
	}

	profileDetail, err := service.GetModuleV1(
		context.Background(),
		GetModuleInputV1{
			Authorization: tenantPermit,
			Scope:         tenantScope,
			InstanceID:    testProfileInstance,
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if profileDetail.SchemaVersion != ModuleDetailSchemaVersionV1 ||
		len(profileDetail.Module.Bindings) != 1 ||
		profileDetail.Module.Bindings[0].Target.Kind !=
			ModuleBindingTargetProfileV1 ||
		profileDetail.Module.Bindings[0].Target.ProfileID != "profile-a" {
		t.Fatalf("tenant profile projection changed: %+v", profileDetail.Module)
	}

	workspaceScope := testWorkspaceScopeV1(testWorkspaceAV1)
	workspacePermit := newTestAuthorizationV1("operator-a", workspaceScope)
	workspacePage, err := service.ListModulesV1(
		context.Background(),
		ListModulesInputV1{
			Authorization: workspacePermit,
			Scope:         workspaceScope,
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleIDsFromPageV1(workspacePage); !reflect.DeepEqual(
		got,
		[]string{testChannelAInstance},
	) {
		t.Fatalf("workspace projection inferred Profile or cross-workspace modules: %v", got)
	}
	workspaceDetail, err := service.GetModuleV1(
		context.Background(),
		GetModuleInputV1{
			Authorization: workspacePermit,
			Scope:         workspaceScope,
			InstanceID:    testChannelAInstance,
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaceDetail.Module.Bindings) != 1 ||
		workspaceDetail.Module.Bindings[0].Target.Kind !=
			ModuleBindingTargetWorkspaceChannelEndpointV1 ||
		workspaceDetail.Module.Bindings[0].Target.WorkspaceID !=
			testWorkspaceAV1 ||
		workspaceDetail.Module.Bindings[0].Target.ProfileID != "" {
		t.Fatalf("workspace binding ownership changed: %+v", workspaceDetail.Module.Bindings)
	}

	for _, instanceID := range []string{testProfileInstance, "absent-instance"} {
		_, err := service.GetModuleV1(
			context.Background(),
			GetModuleInputV1{
				Authorization: workspacePermit,
				Scope:         workspaceScope,
				InstanceID:    instanceID,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("workspace out-of-scope/absent %q error = %v", instanceID, err)
		}
	}

	emptyScope := testWorkspaceScopeV1(testWorkspaceEmptyV1)
	emptyPermit := newTestAuthorizationV1("operator-a", emptyScope)
	emptyPage, err := service.ListModulesV1(
		context.Background(),
		ListModulesInputV1{
			Authorization: emptyPermit,
			Scope:         emptyScope,
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if emptyPage.Items == nil || len(emptyPage.Items) != 0 ||
		emptyPage.HasMore || emptyPage.NextCursor != nil {
		t.Fatalf("empty workspace page is not explicit: %+v", emptyPage)
	}
	encoded, err := json.Marshal(emptyPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"items":[]`) {
		t.Fatalf("empty items encoded as null: %s", encoded)
	}
}

func TestModulesServiceAuthorizationIsSingleAndRepeatedV1(t *testing.T) {
	tenantScope := testTenantScopeV1()

	t.Run("authorization failures precede Store read", func(t *testing.T) {
		tests := []struct {
			name       string
			auth       AuthorizationContextV1
			scope      controlapicontract.ControlScopeV1
			observedAt uint64
			want       error
		}{
			{
				name:       "nil authorization",
				scope:      tenantScope,
				observedAt: testObservedAtV1,
				want:       ErrInvalidRequest,
			},
			{
				name:       "typed nil authorization",
				auth:       (*testAuthorizationV1)(nil),
				scope:      tenantScope,
				observedAt: testObservedAtV1,
				want:       ErrInvalidRequest,
			},
			{
				name:       "invalid scope",
				auth:       newTestAuthorizationV1("operator-a", tenantScope),
				scope:      controlapicontract.ControlScopeV1{},
				observedAt: testObservedAtV1,
				want:       ErrInvalidRequest,
			},
			{
				name:       "expired session",
				auth:       newTestAuthorizationV1("operator-a", tenantScope),
				scope:      tenantScope,
				observedAt: 10_000,
				want:       ErrSessionExpired,
			},
			{
				name:       "denied scope",
				auth:       newTestAuthorizationV1("operator-a"),
				scope:      tenantScope,
				observedAt: testObservedAtV1,
				want:       ErrForbidden,
			},
		}
		mismatched := newTestAuthorizationV1("operator-a", tenantScope)
		mismatched.digest = testHashV1("0")
		tests = append(tests, struct {
			name       string
			auth       AuthorizationContextV1
			scope      controlapicontract.ControlScopeV1
			observedAt uint64
			want       error
		}{"mismatched digest", mismatched, tenantScope, testObservedAtV1, ErrForbidden})
		withoutObserve := newTestAuthorizationV1("operator-a", tenantScope)
		withoutObserve.session.Capabilities = []controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityOperateModulesV1,
		}
		tests = append(tests, struct {
			name       string
			auth       AuthorizationContextV1
			scope      controlapicontract.ControlScopeV1
			observedAt uint64
			want       error
		}{"missing observe capability", withoutObserve, tenantScope, testObservedAtV1, ErrForbidden})

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				reader := newTestPublishedBasisReaderV1()
				service := mustTestModulesServiceV1(t, reader)
				_, err := service.ListModulesV1(
					context.Background(),
					ListModulesInputV1{
						Authorization: test.auth,
						Scope:         test.scope,
						ObservedAt:    test.observedAt,
					},
				)
				if !errors.Is(err, test.want) {
					t.Fatalf("error = %v, want %v", err, test.want)
				}
				if reader.loadCalls != 0 || reader.verifyCalls != 0 {
					t.Fatalf("unauthorized request reached Store: load=%d verify=%d", reader.loadCalls, reader.verifyCalls)
				}
			})
		}
	})

	t.Run("authorization is checked for each projected resource", func(t *testing.T) {
		reader := newTestPublishedBasisReaderV1()
		permit := newTestAuthorizationV1("operator-a", tenantScope)
		permit.denyAtAllow = 3
		service := mustTestModulesServiceV1(t, reader)
		_, err := service.ListModulesV1(
			context.Background(),
			ListModulesInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrForbidden) || permit.allowCalls != 3 ||
			reader.loadCalls != 1 || reader.verifyCalls != 1 {
			t.Fatalf(
				"row recheck result err=%v allows=%d load=%d verify=%d",
				err,
				permit.allowCalls,
				reader.loadCalls,
				reader.verifyCalls,
			)
		}
	})

	t.Run("authorization is checked immediately before success", func(t *testing.T) {
		workspaceScope := testWorkspaceScopeV1(testWorkspaceAV1)
		reader := newTestPublishedBasisReaderV1()
		permit := newTestAuthorizationV1("operator-a", workspaceScope)
		// Initial check, immutable-binding check, one resource check, final check.
		permit.denyAtAllow = 4
		service := mustTestModulesServiceV1(t, reader)
		_, err := service.GetModuleV1(
			context.Background(),
			GetModuleInputV1{
				Authorization: permit,
				Scope:         workspaceScope,
				InstanceID:    testChannelAInstance,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrForbidden) || permit.allowCalls != 4 {
			t.Fatalf("final recheck result err=%v allows=%d", err, permit.allowCalls)
		}
	})

	t.Run("typed nil reader fails closed", func(t *testing.T) {
		var reader *testPublishedBasisReaderV1
		if _, err := NewModulesServiceV1(reader); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("typed nil reader error = %v", err)
		}
	})
}

func TestModulesServiceKeysetPaginationAndCursorDriftV1(t *testing.T) {
	tenantScope := testTenantScopeV1()
	reader := newTestPublishedBasisReaderV1()
	service := mustTestModulesServiceV1(t, reader)
	permit := newTestAuthorizationV1("operator-a", tenantScope)
	first, err := service.ListModulesV1(
		context.Background(),
		ListModulesInputV1{
			Authorization: permit,
			Scope:         tenantScope,
			Page:          controlapicontract.PageQueryV1{Limit: 2},
			ObservedAt:    testObservedAtV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleIDsFromPageV1(first); !reflect.DeepEqual(got, []string{
		testChannelAInstance,
		testChannelBInstance,
	}) || !first.HasMore || first.NextCursor == nil ||
		first.NextCursor.LastInstanceID != testChannelBInstance {
		t.Fatalf("first keyset page changed: %+v", first)
	}
	second, err := service.ListModulesV1(
		context.Background(),
		ListModulesInputV1{
			Authorization: permit,
			Scope:         tenantScope,
			Page:          controlapicontract.PageQueryV1{Limit: 2},
			Cursor:        first.NextCursor,
			ObservedAt:    testObservedAtV1 + 100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleIDsFromPageV1(second); !reflect.DeepEqual(got, []string{
		testProfileInstance,
		testUnboundInstance,
	}) || second.HasMore || second.NextCursor != nil ||
		second.View.ObservedAtUnixMicros != testObservedAtV1 {
		t.Fatalf("second keyset page changed: %+v", second)
	}

	preReadCases := []struct {
		name   string
		mutate func(*DecodedModulesCursorV1)
		want   error
	}{
		{"boot drift", func(cursor *DecodedModulesCursorV1) { cursor.BootID = "boot-b" }, ErrCursorStale},
		{"authorization revision drift", func(cursor *DecodedModulesCursorV1) { cursor.AuthorizationRevision++ }, ErrCursorStale},
		{"principal drift", func(cursor *DecodedModulesCursorV1) { cursor.PrincipalID = "operator-b" }, ErrCursorInvalid},
		{"scope-set drift", func(cursor *DecodedModulesCursorV1) { cursor.ScopeSetDigest = testHashV1("0") }, ErrCursorInvalid},
		{"filter drift", func(cursor *DecodedModulesCursorV1) { cursor.FilterDigest = testHashV1("0") }, ErrCursorInvalid},
		{"sort drift", func(cursor *DecodedModulesCursorV1) { cursor.SortVersion = "other-sort/v1" }, ErrCursorInvalid},
		{"position tamper", func(cursor *DecodedModulesCursorV1) { cursor.PositionDigest = testHashV1("0") }, ErrCursorInvalid},
		{
			"scope drift",
			func(cursor *DecodedModulesCursorV1) {
				cursor.Scope = testWorkspaceScopeV1(testWorkspaceAV1)
				_, _, cursor.ScopeDigest, _ = controlapicontract.NewControlScopeV1(cursor.Scope)
			},
			ErrCursorInvalid,
		},
	}
	for _, test := range preReadCases {
		t.Run(test.name, func(t *testing.T) {
			caseReader := newTestPublishedBasisReaderV1()
			caseService := mustTestModulesServiceV1(t, caseReader)
			cursor := *first.NextCursor
			test.mutate(&cursor)
			casePermit := newTestAuthorizationV1("operator-a", tenantScope)
			_, err := caseService.ListModulesV1(
				context.Background(),
				ListModulesInputV1{
					Authorization: casePermit,
					Scope:         tenantScope,
					Page:          controlapicontract.PageQueryV1{Limit: 2},
					Cursor:        &cursor,
					ObservedAt:    testObservedAtV1,
				},
			)
			if !errors.Is(err, test.want) || caseReader.loadCalls != 0 {
				t.Fatalf("error=%v load=%d, want %v/0", err, caseReader.loadCalls, test.want)
			}
		})
	}

	postReadCases := []struct {
		name         string
		mutateCursor func(*DecodedModulesCursorV1)
		mutateReader func(*testPublishedBasisReaderV1)
		want         error
	}{
		{
			name: "source revision drift",
			mutateReader: func(reader *testPublishedBasisReaderV1) {
				reader.basis.PointerRevision++
			},
			want: ErrCursorStale,
		},
		{
			name: "source digest drift",
			mutateCursor: func(cursor *DecodedModulesCursorV1) {
				cursor.SourceDigest = testHashV1("0")
			},
			want: ErrCursorStale,
		},
		{
			name: "view digest drift",
			mutateCursor: func(cursor *DecodedModulesCursorV1) {
				cursor.ViewSnapshotDigest = testHashV1("0")
			},
			want: ErrCursorStale,
		},
		{
			name: "observation drift",
			mutateCursor: func(cursor *DecodedModulesCursorV1) {
				cursor.ObservedAtUnixMicros++
			},
			want: ErrCursorStale,
		},
		{
			name: "missing exact position",
			mutateCursor: func(cursor *DecodedModulesCursorV1) {
				cursor.LastInstanceID = "instance-missing"
				cursor.PositionDigest, _ = modulesPositionDigestV1(
					cursor.SortVersion,
					cursor.LastInstanceID,
				)
			},
			want: ErrCursorInvalid,
		},
	}
	for _, test := range postReadCases {
		t.Run(test.name, func(t *testing.T) {
			caseReader := newTestPublishedBasisReaderV1()
			if test.mutateReader != nil {
				test.mutateReader(caseReader)
			}
			caseService := mustTestModulesServiceV1(t, caseReader)
			cursor := *first.NextCursor
			if test.mutateCursor != nil {
				test.mutateCursor(&cursor)
			}
			casePermit := newTestAuthorizationV1("operator-a", tenantScope)
			_, err := caseService.ListModulesV1(
				context.Background(),
				ListModulesInputV1{
					Authorization: casePermit,
					Scope:         tenantScope,
					Page:          controlapicontract.PageQueryV1{Limit: 2},
					Cursor:        &cursor,
					ObservedAt:    testObservedAtV1,
				},
			)
			if !errors.Is(err, test.want) || caseReader.loadCalls != 1 {
				t.Fatalf("error=%v load=%d, want %v/1", err, caseReader.loadCalls, test.want)
			}
		})
	}
}

func TestModulesServiceDeterministicDigestsETagsAndCopiesV1(t *testing.T) {
	tenantScope := testTenantScopeV1()
	reader := newTestPublishedBasisReaderV1()
	service := mustTestModulesServiceV1(t, reader)
	permit := newTestAuthorizationV1("operator-a", tenantScope)
	input := ListModulesInputV1{
		Authorization: permit,
		Scope:         tenantScope,
		Page:          controlapicontract.PageQueryV1{Limit: 2},
		ObservedAt:    testObservedAtV1,
	}
	first, err := service.ListModulesV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListModulesV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same immutable inputs produced different projections")
	}
	strongETag := regexp.MustCompile(`^"[0-9a-f]{64}"$`)
	if !moduleapi.ValidSHA256(first.ProjectionDigest) ||
		!strongETag.MatchString(first.StrongETag) ||
		!moduleapi.ValidSHA256(first.SourceDigest) ||
		!moduleapi.ValidSHA256(first.ViewSnapshotDigest) {
		t.Fatalf("invalid digest/strong ETag projection: %+v", first)
	}
	if first.SourceDigest !=
		"efa608e8f102fd61322d991e268530854233495ba3c8bf75920253eb96142894" ||
		first.ViewSnapshotDigest !=
			"d618b2302d4f3390ee9df368b3e90ab6e2a53617d9d70203db2d3882a4bd55b5" ||
		first.ProjectionDigest !=
			"eef95a324b0d7115eda5a01c0a43fe13b096adf35d6d89988a7fd7c215a53db3" ||
		first.StrongETag !=
			`"03cd6c6a8f0aeb6c87866ccdf74eebe7851425ac49ada12a5ee0d73bda395a29"` {
		t.Fatalf("Modules page digest canary changed: %+v", first)
	}

	otherPrincipalInput := input
	otherPermit := newTestAuthorizationV1(
		"operator-b",
		tenantScope,
	)
	otherPrincipalInput.Authorization = otherPermit
	otherPrincipal, err := service.ListModulesV1(
		context.Background(),
		otherPrincipalInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if otherPrincipal.SourceDigest != first.SourceDigest ||
		otherPrincipal.ViewSnapshotDigest != first.ViewSnapshotDigest ||
		otherPrincipal.ProjectionDigest == first.ProjectionDigest ||
		otherPrincipal.StrongETag == first.StrongETag {
		t.Fatal("principal was not isolated in cursor-bearing projection and ETag")
	}

	otherTimeInput := input
	otherTimeInput.ObservedAt++
	otherTime, err := service.ListModulesV1(context.Background(), otherTimeInput)
	if err != nil {
		t.Fatal(err)
	}
	if otherTime.SourceDigest != first.SourceDigest ||
		otherTime.ViewSnapshotDigest == first.ViewSnapshotDigest ||
		otherTime.ProjectionDigest == first.ProjectionDigest ||
		otherTime.StrongETag == first.StrongETag {
		t.Fatal("observation time did not remain distinct from source identity")
	}

	tamperedSchema := first
	tamperedSchema.SchemaVersion = "other-page/v1"
	if _, err := digestModulesPageProjectionV1(tamperedSchema, 2, ""); err == nil {
		t.Fatal("page projection accepted an unfrozen response schema")
	}

	first.Items[0].Provides[0].Name = "mutated.port"
	first.View.Sections[0].SourceDigest = testHashV1("0")
	first.NextCursor.PrincipalID = "mutated-principal"
	again, err := service.ListModulesV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, second) {
		t.Fatal("page result aliases application-service state")
	}

	detailInput := GetModuleInputV1{
		Authorization: permit,
		Scope:         tenantScope,
		InstanceID:    testProfileInstance,
		ObservedAt:    testObservedAtV1,
	}
	detail, err := service.GetModuleV1(context.Background(), detailInput)
	if err != nil {
		t.Fatal(err)
	}
	detailBaseline := detail
	detailBaseline.Module = cloneModuleDetailV1(detail.Module)
	detailBaseline.View = cloneControlViewV1(detail.View)
	if detail.SchemaVersion != ModuleDetailSchemaVersionV1 ||
		!moduleapi.ValidSHA256(detail.ProjectionDigest) ||
		!strongETag.MatchString(detail.StrongETag) {
		t.Fatalf("invalid detail schema/digest/ETag: %+v", detail)
	}
	if detail.ProjectionDigest !=
		"c9b5c4f6638842db2254ec151465c3014450146651274162ae4fde932ceaa3b4" ||
		detail.StrongETag !=
			`"6e0a5fbe2304cd5203be584ef1cddd63416b412c5844e79b36adf6c1666f0173"` {
		t.Fatalf("Module detail digest canary changed: %+v", detail)
	}
	detail.Module.Summary.Provides[0].Name = "mutated.port"
	detail.Module.Bindings[0].StaticContextRefs = append(
		detail.Module.Bindings[0].StaticContextRefs,
		testHashV1("0"),
	)
	detail.View.Sections[0].SourceDigest = testHashV1("0")
	detailAgain, err := service.GetModuleV1(context.Background(), detailInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(detailAgain, detailBaseline) {
		t.Fatal("detail result aliases application-service state")
	}
	tamperedDetailSchema := detailAgain
	tamperedDetailSchema.SchemaVersion = "other-detail/v1"
	if _, err := digestModuleDetailProjectionV1(tamperedDetailSchema); err == nil {
		t.Fatal("detail projection accepted an unfrozen response schema")
	}
}

func TestModulesServiceErrorsLimitsCancellationAndLeakageV1(t *testing.T) {
	tenantScope := testTenantScopeV1()

	t.Run("reader error classification", func(t *testing.T) {
		loadCases := []struct {
			name  string
			input error
			want  error
		}{
			{"not found", fmt.Errorf("load: %w", ErrNotFound), ErrNotFound},
			{"busy", fmt.Errorf("load: %w", ErrStoreBusy), ErrStoreBusy},
			{"integrity", fmt.Errorf("load: %w", ErrIntegrityFailure), ErrIntegrityFailure},
			{"resource", fmt.Errorf("load: %w", ErrResourceExhausted), ErrResourceExhausted},
			{"cancelled sentinel", fmt.Errorf("load: %w", ErrCancelled), ErrCancelled},
			{"context cancelled", context.Canceled, ErrCancelled},
			{"deadline", context.DeadlineExceeded, ErrCancelled},
			{"unknown", errors.New("private store failure"), ErrStoreUnavailable},
		}
		for _, test := range loadCases {
			t.Run("load "+test.name, func(t *testing.T) {
				reader := newTestPublishedBasisReaderV1()
				reader.loadErr = test.input
				service := mustTestModulesServiceV1(t, reader)
				permit := newTestAuthorizationV1("operator-a", tenantScope)
				_, err := service.ListModulesV1(
					context.Background(),
					ListModulesInputV1{
						Authorization: permit,
						Scope:         tenantScope,
						ObservedAt:    testObservedAtV1,
					},
				)
				if !errors.Is(err, test.want) || reader.verifyCalls != 0 {
					t.Fatalf("error=%v verify=%d, want %v/0", err, reader.verifyCalls, test.want)
				}
			})
		}

		verifyCases := []struct {
			name  string
			input error
			want  error
		}{
			{"not found", fmt.Errorf("verify: %w", ErrNotFound), ErrNotFound},
			{"busy", fmt.Errorf("verify: %w", ErrStoreBusy), ErrStoreBusy},
			{"unavailable", fmt.Errorf("verify: %w", ErrStoreUnavailable), ErrStoreUnavailable},
			{"resource", fmt.Errorf("verify: %w", ErrResourceExhausted), ErrResourceExhausted},
			{"context cancelled", context.Canceled, ErrCancelled},
			{"unknown", errors.New("closure mismatch"), ErrIntegrityFailure},
		}
		for _, test := range verifyCases {
			t.Run("verify "+test.name, func(t *testing.T) {
				reader := newTestPublishedBasisReaderV1()
				reader.verifyErr = test.input
				service := mustTestModulesServiceV1(t, reader)
				permit := newTestAuthorizationV1("operator-a", tenantScope)
				_, err := service.ListModulesV1(
					context.Background(),
					ListModulesInputV1{
						Authorization: permit,
						Scope:         tenantScope,
						ObservedAt:    testObservedAtV1,
					},
				)
				if !errors.Is(err, test.want) || reader.verifyCalls != 1 {
					t.Fatalf("error=%v verify=%d, want %v/1", err, reader.verifyCalls, test.want)
				}
			})
		}
	})

	t.Run("invalid basis fails before closure verifier", func(t *testing.T) {
		reader := newTestPublishedBasisReaderV1()
		reader.basis.PointerRevision = 0
		service := mustTestModulesServiceV1(t, reader)
		permit := newTestAuthorizationV1("operator-a", tenantScope)
		_, err := service.ListModulesV1(
			context.Background(),
			ListModulesInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrIntegrityFailure) || reader.verifyCalls != 0 {
			t.Fatalf("invalid basis error=%v verify=%d", err, reader.verifyCalls)
		}
	})

	t.Run("reader cannot substitute another tenant", func(t *testing.T) {
		reader := newTestPublishedBasisReaderForTenantV1("tenant-other")
		service := mustTestModulesServiceV1(t, reader)
		permit := newTestAuthorizationV1("operator-a", tenantScope)
		_, err := service.ListModulesV1(
			context.Background(),
			ListModulesInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrIntegrityFailure) || reader.verifyCalls != 0 {
			t.Fatalf("substituted tenant error=%v verify=%d", err, reader.verifyCalls)
		}
	})

	t.Run("cancelled context never reaches Store", func(t *testing.T) {
		reader := newTestPublishedBasisReaderV1()
		service := mustTestModulesServiceV1(t, reader)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		permit := newTestAuthorizationV1("operator-a", tenantScope)
		_, err := service.ListModulesV1(
			ctx,
			ListModulesInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				ObservedAt:    testObservedAtV1,
			},
		)
		if !errors.Is(err, ErrCancelled) || reader.loadCalls != 0 {
			t.Fatalf("cancel error=%v load=%d", err, reader.loadCalls)
		}
	})

	t.Run("page limits and unsupported generic query facts", func(t *testing.T) {
		if got, err := normalizeModulesPageV1(controlapicontract.PageQueryV1{}); err != nil ||
			got != DefaultModulesPageLimitV1 {
			t.Fatalf("default limit = %d, %v", got, err)
		}
		if got, err := normalizeModulesPageV1(controlapicontract.PageQueryV1{
			Limit: MaximumModulesPageLimitV1,
		}); err != nil || got != MaximumModulesPageLimitV1 {
			t.Fatalf("maximum limit = %d, %v", got, err)
		}
		invalid := []controlapicontract.PageQueryV1{
			{Limit: MaximumModulesPageLimitV1 + 1},
			{Limit: 1, FilterDigest: testHashV1("0")},
			{
				Limit: 1,
				After: &controlapicontract.PageTokenRefV1{
					ScopeDigest:        testHashV1("1"),
					ViewSnapshotDigest: testHashV1("2"),
					Collection:         controlapicontract.PageCollectionModulesV1,
					PositionDigest:     testHashV1("3"),
				},
			},
		}
		for _, query := range invalid {
			if _, err := normalizeModulesPageV1(query); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("unsupported query %+v error = %v", query, err)
			}
		}
	})

	t.Run("DTOs contain refs but no bodies secrets paths or URLs", func(t *testing.T) {
		reader := newTestPublishedBasisReaderV1()
		service := mustTestModulesServiceV1(t, reader)
		permit := newTestAuthorizationV1("operator-a", tenantScope)
		page, err := service.ListModulesV1(
			context.Background(),
			ListModulesInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				ObservedAt:    testObservedAtV1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := service.GetModuleV1(
			context.Background(),
			GetModuleInputV1{
				Authorization: permit,
				Scope:         tenantScope,
				InstanceID:    testUnboundInstance,
				ObservedAt:    testObservedAtV1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Module.Bindings == nil || len(detail.Module.Bindings) != 0 {
			t.Fatalf("unbound detail bindings are not an explicit empty array: %+v", detail.Module)
		}
		for name, value := range map[string]any{"page": page, "detail": detail} {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			wire := strings.ToLower(string(encoded))
			for _, forbidden := range []string{
				`"body"`,
				`"content"`,
				`"secret"`,
				`"path"`,
				`"url"`,
				"super-secret-material",
				"c:" + `\\`,
				"https://",
			} {
				if strings.Contains(wire, forbidden) {
					t.Fatalf("%s projection leaked forbidden %q: %s", name, forbidden, encoded)
				}
			}
		}
	})
}

func mustTestModulesServiceV1(
	t *testing.T,
	reader PublishedBasisReaderV1,
) *ModulesServiceV1 {
	t.Helper()
	service, err := NewModulesServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func moduleIDsFromPageV1(page ModulesPageV1) []string {
	result := make([]string, len(page.Items))
	for index := range page.Items {
		result[index] = page.Items[index].InstanceID
	}
	return result
}
