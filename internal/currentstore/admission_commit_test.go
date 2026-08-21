package currentstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type admissionCommitFixture struct {
	store            *Store
	basis            controlcontract.PublishedBasis
	controlCanonical []byte
	catalogCanonical []byte
	modelProfile     *corecontract.ModelProfileRef
	intent           corecontract.AdmissionIntentV1
	input            CommitRunAdmissionInput
	task             ContentInput
	staticContext    ContentInput
}

func TestCommitRunAdmissionPublishesClosureAndRetriesBeforeCurrentBasis(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	beforeCreate := admissionCommitCounts(t, fixture.store)

	created, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitRunAdmission: %v", err)
	}
	if !created.Created ||
		created.RunID != "run-admitted" ||
		created.AdmissionIntentDigest != fixture.input.IntentDigest ||
		created.ManifestDigest == "" ||
		created.MemberSnapshotDigest == "" {
		t.Fatalf("created result=%+v", created)
	}
	// The publication now closes over the immutable declarative static
	// context, so admission adds only task input plus its two run documents.
	wantCounts := [6]int{1, 1, 1, 1, 1, beforeCreate[5] + 2}
	if got := admissionCommitCounts(t, fixture.store); got != wantCounts {
		t.Fatalf("admitted closure row counts=%v", got)
	}
	resolved, found, err := fixture.store.ResolveAdmission(
		context.Background(),
		fixture.intent.TenantID,
		fixture.intent.AdmissionKey,
		fixture.input.IntentDigest,
	)
	if err != nil || !found {
		t.Fatalf("ResolveAdmission found=%v error=%v", found, err)
	}
	wantResolved := created
	wantResolved.Created = false
	if resolved != wantResolved {
		t.Fatalf("resolved=%+v want %+v", resolved, wantResolved)
	}

	fixture.basis = advancePublishedBasisForTestV1(
		t, fixture.store, fixture.basis, fixture.controlCanonical, fixture.catalogCanonical,
	)
	beforeRetry := admissionCommitCounts(t, fixture.store)
	retried, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("same-intent retry after basis advance: %v", err)
	}
	if retried != wantResolved {
		t.Fatalf("retried=%+v want %+v", retried, wantResolved)
	}
	if afterRetry := admissionCommitCounts(t, fixture.store); afterRetry != beforeRetry {
		t.Fatalf("retry changed rows: before=%v after=%v", beforeRetry, afterRetry)
	}
}

func TestPureChatAdmissionKeepsCompositeOptionalPathEmpty(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	created, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitRunAdmission: %v", err)
	}
	var parentRunID, parentManifestDigest, parentSlotID, cancelRef string
	if err := fixture.store.db.QueryRow(`
		SELECT
			COALESCE(parent_run_id, ''),
			COALESCE(parent_manifest_digest, ''),
			COALESCE(parent_slot_id, ''),
			COALESCE(cancel_request_ref, '')
		FROM runs
		WHERE run_id=?
	`, created.RunID).Scan(
		&parentRunID,
		&parentManifestDigest,
		&parentSlotID,
		&cancelRef,
	); err != nil {
		t.Fatalf("read Pure Chat optional projection: %v", err)
	}
	if parentRunID != "" || parentManifestDigest != "" ||
		parentSlotID != "" || cancelRef != "" {
		t.Fatalf(
			"Pure Chat persisted Composite state parent=%q/%q/%q cancel=%q",
			parentRunID,
			parentManifestDigest,
			parentSlotID,
			cancelRef,
		)
	}
	lease, err := fixture.store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID: created.RunID, OwnerID: "pure-chat-composite-regression", TTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease: %v", err)
	}
	run, loadErr := fixture.store.LoadRunForLoop(context.Background(), lease)
	releaseErr := fixture.store.ReleaseRunLease(context.Background(), lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf("Load/Release Pure Chat Run: load=%v release=%v", loadErr, releaseErr)
	}
	if run.Manifest.Composite != nil || run.Manifest.ParentRunID != "" ||
		run.CompositeChildren != nil || run.CancellationRef != "" ||
		run.CancellationRequest != nil || run.WorkspaceTransfer != nil {
		t.Fatalf("Pure Chat loaded Composite optional closure: %+v", run)
	}
	for _, kind := range []ContentKind{
		ContentWorkspaceTransferPayload,
		ContentWorkspaceTransferEnvelope,
	} {
		var count int
		if err := fixture.store.db.QueryRow(
			`SELECT COUNT(*) FROM content_records WHERE kind=?`,
			string(kind),
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Pure Chat %s count=%d want 0 error=%v", kind, count, err)
		}
	}
}

func TestCommitRunAdmissionSameKeyDifferentIntentConflicts(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("seed CommitRunAdmission: %v", err)
	}
	before := admissionCommitCounts(t, fixture.store)

	changedIntent := fixture.intent
	changedIntent.PrincipalID = "principal-other"
	_, changedCanonical, changedDigest, err :=
		corecontract.NewAdmissionIntentV1(changedIntent)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := fixture.compileInput(
		t,
		changedCanonical,
		changedDigest,
		"run-conflicting",
	)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		conflicting,
	); !errors.Is(err, ErrAdmissionConflict) {
		t.Fatalf("different intent error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("conflict changed rows: before=%v after=%v", before, after)
	}
}

func TestCommitRunAdmissionRejectsEveryLearningCycleIdentityNamespaceWithoutWrites(
	t *testing.T,
) {
	reservedSuffix := strings.Repeat("a", 64)
	tests := []struct {
		name            string
		admissionKey    string
		runID           string
		memberID        string
		recoveryRootRef string
	}{
		{
			name:         "AdmissionKey",
			admissionKey: learningCycleAdmissionKeyPrefix + reservedSuffix,
		},
		{
			name:  "RunID",
			runID: learningCycleRunIDPrefix + reservedSuffix,
		},
		{
			name:     "MemberID",
			memberID: learningCycleMemberIDPrefix + reservedSuffix,
		},
		{
			name:            "RecoveryRootRef",
			recoveryRootRef: learningCycleRecoveryRootPrefix + reservedSuffix,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionCommitFixture(t)
			intent := fixture.intent
			if test.admissionKey != "" {
				intent.AdmissionKey = test.admissionKey
			}
			_, intentCanonical, intentDigest, err :=
				corecontract.NewAdmissionIntentV1(intent)
			if err != nil {
				t.Fatal(err)
			}
			runID := "run-cycle-namespace-negative"
			if test.runID != "" {
				runID = test.runID
			}
			memberID := "member-cycle-namespace-negative"
			if test.memberID != "" {
				memberID = test.memberID
			}
			recoveryRootRef := "recovery/cycle-namespace-negative"
			if test.recoveryRootRef != "" {
				recoveryRootRef = test.recoveryRootRef
			}
			input := fixture.compileInputWithIdentities(
				t,
				intentCanonical,
				intentDigest,
				runID,
				memberID,
				recoveryRootRef,
			)
			before := admissionCommitCounts(t, fixture.store)
			if _, err := fixture.store.CommitRunAdmission(
				context.Background(),
				input,
			); !errors.Is(err, ErrInvalidAdmission) {
				t.Fatalf("reserved %s error = %v", test.name, err)
			}
			if after := admissionCommitCounts(t, fixture.store); after != before {
				t.Fatalf("reserved %s changed rows: before=%v after=%v",
					test.name, before, after)
			}
		})
	}
}

func TestCommitRunAdmissionRejectsStaleOrMissingClosureWithoutWrites(
	t *testing.T,
) {
	t.Run("stale basis", func(t *testing.T) {
		fixture := newAdmissionCommitFixture(t)
		before := admissionCommitCounts(t, fixture.store)
		fixture.basis = advancePublishedBasisForTestV1(
			t, fixture.store, fixture.basis,
			fixture.controlCanonical, fixture.catalogCanonical,
		)
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(),
			fixture.input,
		); !errors.Is(err, ErrAdmissionConflict) {
			t.Fatalf("stale basis error=%v", err)
		}
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("stale basis changed rows: before=%v after=%v", before, after)
		}
	})

	t.Run("missing task input", func(t *testing.T) {
		fixture := newAdmissionCommitFixture(t)
		before := admissionCommitCounts(t, fixture.store)
		input := fixture.input
		input.Contents = []ContentInput{fixture.staticContext}
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(),
			input,
		); !errors.Is(err, ErrAdmissionIntegrity) {
			t.Fatalf("missing content error=%v", err)
		}
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("missing content changed rows: before=%v after=%v", before, after)
		}
	})

	t.Run("missing frozen static context", func(t *testing.T) {
		fixture := newAdmissionCommitFixture(t)
		execClosedFileTamperV1(
			t,
			fixture.store,
			[]string{"content_records_reject_delete"},
			`
			DELETE FROM content_records WHERE content_digest=?
		`,
			fixture.staticContext.Digest,
		)
		before := admissionCommitCounts(t, fixture.store)
		input := fixture.input
		input.Contents = []ContentInput{fixture.task}
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(),
			input,
		); !errors.Is(err, ErrAdmissionIntegrity) {
			t.Fatalf("missing static context error=%v", err)
		}
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf(
				"missing static context changed rows: before=%v after=%v",
				before,
				after,
			)
		}
	})
}

func TestCommitRunAdmissionPublishesProfiledRunWithoutAttempt(t *testing.T) {
	fixture := newAdmissionCommitFixtureWithModelProfile(t, 1000)
	before := runAndModelAttemptCounts(t, fixture.store)
	result, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitRunAdmission with ModelProfile: %v", err)
	}
	if !result.Created {
		t.Fatalf("profiled Admission result=%+v", result)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreMemberExecutionSnapshot: %v", err)
	}
	if fixture.modelProfile == nil || member.ModelProfile == nil ||
		*member.ModelProfile != *fixture.modelProfile {
		t.Fatalf(
			"compiled ModelProfile=%+v want %+v",
			member.ModelProfile,
			fixture.modelProfile,
		)
	}
	if record, err := fixture.store.GetContent(
		context.Background(),
		fixture.modelProfile.Digest,
	); err != nil || record.Kind != ContentConfig {
		t.Fatalf("profile CONFIG record=%+v error=%v", record, err)
	}
	want := [2]int{before[0] + 1, before[1]}
	if after := runAndModelAttemptCounts(t, fixture.store); after != want {
		t.Fatalf("profiled Admission Run/Attempt rows=%v want %v", after, want)
	}
}

func TestCommitRunAdmissionRejectsUnavailableModelProfileBeforeWrites(
	t *testing.T,
) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, *admissionCommitFixture)
	}{
		{
			name: "missing CONFIG",
			corrupt: func(t *testing.T, fixture *admissionCommitFixture) {
				execClosedFileTamperV1(
					t,
					fixture.store,
					[]string{"content_records_reject_delete"},
					`
					DELETE FROM content_records WHERE content_digest=?
				`,
					fixture.modelProfile.Digest,
				)
			},
		},
		{
			name: "wrong CONFIG kind",
			corrupt: func(t *testing.T, fixture *admissionCommitFixture) {
				execClosedFileTamperV1(
					t,
					fixture.store,
					[]string{"content_records_reject_update"},
					`
					UPDATE content_records SET kind=? WHERE content_digest=?
				`,
					ContentTaskInput,
					fixture.modelProfile.Digest,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionCommitFixtureWithModelProfile(t, 1000)
			test.corrupt(t, fixture)
			before := runAndModelAttemptCounts(t, fixture.store)
			if _, err := fixture.store.CommitRunAdmission(
				context.Background(),
				fixture.input,
			); !errors.Is(err, ErrAdmissionIntegrity) {
				t.Fatalf("unavailable ModelProfile Admission error=%v", err)
			}
			if after := runAndModelAttemptCounts(t, fixture.store); after != before {
				t.Fatalf(
					"unavailable ModelProfile reached Run/Attempt writes: before=%v after=%v",
					before,
					after,
				)
			}
		})
	}
}

func TestResolveAdmissionRevalidatesFrozenStaticContext(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	created, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitRunAdmission: %v", err)
	}
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"content_records_reject_delete"},
		`
		DELETE FROM content_records WHERE content_digest=?
	`,
		fixture.staticContext.Digest,
	)
	_, found, err := fixture.store.ResolveAdmission(
		context.Background(),
		fixture.intent.TenantID,
		fixture.intent.AdmissionKey,
		fixture.input.IntentDigest,
	)
	if !found || !errors.Is(err, ErrAdmissionIntegrity) {
		t.Fatalf(
			"ResolveAdmission after static context loss run=%s found=%v error=%v",
			created.RunID,
			found,
			err,
		)
	}
}

func newAdmissionCommitFixture(t *testing.T) *admissionCommitFixture {
	return newAdmissionCommitFixtureWithModelProfile(t, 0)
}

func advancePublishedBasisForTestV1(
	t *testing.T,
	store *Store,
	basis controlcontract.PublishedBasis,
	controlCanonical, catalogCanonical []byte,
) controlcontract.PublishedBasis {
	t.Helper()
	next, err := store.PublishControlCatalog(context.Background(), PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              basis.Control,
		ControlCanonical:        controlCanonical,
		CatalogRef:              basis.Catalog,
		CatalogCanonical:        catalogCanonical,
	})
	if err != nil {
		t.Fatalf("advance PublishedBasis: %v", err)
	}
	return next
}

func newAdmissionCommitFixtureWithModelProfile(
	t *testing.T,
	contextWindowTokens uint64,
) *admissionCommitFixture {
	return newAdmissionCommitFixtureWithModelProfileAndPrice(
		t,
		contextWindowTokens,
		testModelPriceSnapshot(),
	)
}

func newAdmissionCommitFixtureWithModelProfileAndPrice(
	t *testing.T,
	contextWindowTokens uint64,
	price corecontract.ModelPriceSnapshotV1,
) *admissionCommitFixture {
	t.Helper()
	publication := newPublicationFixtureWithPrice(t, price)
	var modelProfile *corecontract.ModelProfileRef
	if contextWindowTokens != 0 {
		ref := putPublicationModelProfile(
			t,
			publication,
			contextWindowTokens,
			nil,
		)
		modelProfile = &ref
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "hello",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := newAdmissionContent(
		t,
		ContentTaskInput,
		taskCanonical,
	)
	_, staticContextCanonical, err :=
		corecontract.NewStaticContextV1(
			corecontract.StaticContextV1{
				SchemaVersion: corecontract.StaticContextSchemaVersionV1,
				Text:          "fixture context",
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	staticContext := newAdmissionContent(
		t,
		ContentStaticContext,
		staticContextCanonical,
	)
	if _, err := publication.store.PutContent(
		context.Background(),
		staticContext,
	); err != nil {
		t.Fatalf("PutContent static context: %v", err)
	}
	budget := putPublicationPolicy(
		t,
		publication.store,
		"budget-policy",
		corecontract.PolicyCost,
	)
	agent := corecontract.AgentRef{
		ID:      "agent-default",
		Version: "v1",
		Digest:  strings.Repeat("2", 64),
	}
	workspace := corecontract.WorkspaceRef{
		ID:      "workspace-default",
		Version: "v1",
		Digest:  strings.Repeat("3", 64),
	}
	profile := corecontract.ProfileRef{
		ID:      "profile-default",
		Version: "v1",
		Digest:  strings.Repeat("1", 64),
	}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	contextManifest := canonicalModuleManifest(
		t,
		"test.context",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		map[string]any{
			"provides": []any{
				map[string]any{
					"name":          contextPort.Name,
					"exact_version": contextPort.ExactVersion,
				},
			},
		},
	)
	contextInstallation, err := publication.store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-context",
			contextManifest,
			strings.Repeat("b", 64),
		),
	)
	if err != nil {
		t.Fatalf("InstallModule context: %v", err)
	}
	contextActivation, err := publication.store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-context",
			TenantID:           publication.tenantID,
			InstanceID:         "instance-context",
			InstallationID:     contextInstallation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "core.context.adapter",
		},
	)
	if err != nil {
		t.Fatalf("ActivateModule context: %v", err)
	}
	contextProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           contextInstallation.ModuleID,
		Version:            contextInstallation.ExactVersion,
		ArtifactDigest:     contextInstallation.ArtifactDigest,
		InstanceID:         contextActivation.InstanceID,
		ExecutionClass:     contextActivation.ExecutionClass,
		AdapterIdentity:    contextActivation.AdapterIdentity,
		ActivationRevision: contextActivation.ActivationRevision,
	}
	_, contextConfigCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementTrustedInstruction,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("NewContextBindingConfigV1: %v", err)
	}
	contextConfig := putPublicationJSON(
		t,
		publication.store,
		ContentConfig,
		contextConfigCanonical,
	)
	contextAuthority := putPublicationJSON(
		t,
		publication.store,
		ContentAuthorityCeiling,
		[]byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
	)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-admission",
			TenantID:      publication.tenantID,
			Revision:      1,
			Agents:        []corecontract.AgentRef{agent},
			Workspaces: []controlcontract.WorkspaceDefinition{
				{Workspace: workspace, BudgetPolicy: budget},
			},
			Profiles: []controlcontract.ProfileDefinition{
				{
					Profile:          profile,
					ContextPolicy:    publication.context,
					CostPolicy:       publication.cost,
					SchedulingPolicy: publication.scheduling,
					ModelProfile:     modelProfile,
					Bindings: []controlcontract.BindingSpec{
						{
							Port:                publication.port,
							InstanceID:          publication.activation.InstanceID,
							ConfigRef:           publication.config,
							AuthorityCeilingRef: publication.authority,
							FailurePolicy:       moduleapi.FailureRequired,
						},
						{
							Port:                contextPort,
							InstanceID:          contextProvider.InstanceID,
							ConfigRef:           contextConfig,
							AuthorityCeilingRef: contextAuthority,
							StaticContextRefs: []string{
								staticContext.Digest,
							},
							FailurePolicy: moduleapi.FailureOptional,
						},
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewControlSnapshot: %v", err)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(
			controlcontract.CatalogGeneration{
				SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
				GenerationID:          "catalog-admission",
				Generation:            1,
				TenantID:              publication.tenantID,
				ControlSnapshotID:     controlRef.SnapshotID,
				ControlSnapshotDigest: controlRef.Digest,
				Entries: []controlcontract.CatalogEntry{
					{
						Activation: publication.activation,
						Provides:   []moduleapi.PortRef{publication.port},
					},
					{
						Activation: contextProvider,
						Provides:   []moduleapi.PortRef{contextPort},
					},
				},
			},
		)
	if err != nil {
		t.Fatalf("NewCatalogGeneration: %v", err)
	}
	basis, err := publication.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: 0,
			NewPointerRevision:      1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("PublishControlCatalog: %v", err)
	}

	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(
			corecontract.AdmissionIntentV1{
				SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
				TenantID:          publication.tenantID,
				AdmissionKey:      "admission-key",
				PrincipalID:       "principal",
				WorkspaceID:       workspace.ID,
				AgentID:           agent.ID,
				ProfileID:         profile.ID,
				TaskInputRef:      task.Digest,
				RequestedPorts:    []moduleapi.PortRef{publication.port},
				Deadline:          time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
				CancellationScope: "run",
				ExplicitLimits:    []byte(`{}`),
			},
		)
	if err != nil {
		t.Fatalf("NewAdmissionIntentV1: %v", err)
	}
	fixture := &admissionCommitFixture{
		store:            publication.store,
		basis:            basis,
		controlCanonical: controlCanonical,
		catalogCanonical: catalogCanonical,
		modelProfile:     modelProfile,
		intent:           intent,
		task:             task,
		staticContext:    staticContext,
	}
	fixture.input = fixture.compileInput(
		t,
		intentCanonical,
		intentDigest,
		"run-admitted",
	)
	return fixture
}

func (fixture *admissionCommitFixture) compileInput(
	t *testing.T,
	intentCanonical []byte,
	intentDigest string,
	runID string,
) CommitRunAdmissionInput {
	t.Helper()
	return fixture.compileInputWithIdentities(
		t,
		intentCanonical,
		intentDigest,
		runID,
		"member-primary",
		"recovery/"+runID,
	)
}

func (fixture *admissionCommitFixture) compileInputWithIdentities(
	t *testing.T,
	intentCanonical []byte,
	intentDigest string,
	runID string,
	memberID string,
	recoveryRootRef string,
) CommitRunAdmissionInput {
	t.Helper()
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            runID,
			MemberID:         memberID,
			RecoveryRootRef:  recoveryRootRef,
			PublishedBasis:   fixture.basis,
			ControlCanonical: fixture.controlCanonical,
			CatalogCanonical: fixture.catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return CommitRunAdmissionInput{
		PublishedBasis:          fixture.basis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents: []ContentInput{
			fixture.task,
			fixture.staticContext,
		},
	}
}

func newAdmissionContent(
	t *testing.T,
	kind ContentKind,
	body []byte,
) ContentInput {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeContentDigest(kind, "application/json", canonical)
	if err != nil {
		t.Fatal(err)
	}
	return ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	}
}

func admissionCommitCounts(t *testing.T, store *Store) [6]int {
	t.Helper()
	var counts [6]int
	for index, table := range []string{
		"runs",
		"member_execution_snapshots",
		"run_manifests",
		"loop_frames",
		"run_events",
		"content_records",
	} {
		if err := store.db.QueryRow(
			"SELECT count(*) FROM " + table,
		).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}
