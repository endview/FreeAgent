package currentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type publicationFixture struct {
	store       *Store
	tenantID    string
	activation  moduleapi.ActivatedModuleRef
	port        moduleapi.PortRef
	modelConfig moduleapi.ModelBindingConfigV2
	context     corecontract.PolicyRef
	scheduling  corecontract.PolicyRef
	config      string
	authority   string
	wrongConfig string
}

func TestPublishControlCatalogInitialRetryCASAndUnixMicro(t *testing.T) {
	fixture := newPublicationFixture(t)
	first := fixture.input(t, "snapshot-1", 1, "catalog-1", 1, 0, 1, nil)

	basis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	)
	if err != nil {
		t.Fatalf("initial PublishControlCatalog: %v", err)
	}
	if basis.TenantID != fixture.tenantID ||
		basis.PointerRevision != 1 ||
		basis.Control != first.ControlRef ||
		basis.Catalog != first.CatalogRef {
		t.Fatalf("initial basis=%+v", basis)
	}
	initialCounts := publicationRowCounts(t, fixture.store)

	retry, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	)
	if err != nil {
		t.Fatalf("exact retry PublishControlCatalog: %v", err)
	}
	if retry != basis {
		t.Fatalf("retry basis=%+v want %+v", retry, basis)
	}
	if got := publicationRowCounts(t, fixture.store); got != initialCounts {
		t.Fatalf("exact retry changed rows: got=%v want=%v", got, initialCounts)
	}

	for _, table := range []string{
		"control_snapshots",
		"runtime_catalog_generations",
	} {
		var storageClass string
		var publishedAt int64
		query := fmt.Sprintf(
			"SELECT typeof(published_at), published_at FROM %s",
			table,
		)
		if err := fixture.store.db.QueryRow(query).Scan(
			&storageClass,
			&publishedAt,
		); err != nil {
			t.Fatalf("read %s published_at: %v", table, err)
		}
		if storageClass != "integer" || publishedAt <= 0 {
			t.Fatalf(
				"%s published_at=(%q,%d), want positive INTEGER Unix microseconds",
				table,
				storageClass,
				publishedAt,
			)
		}
	}

	second := fixture.input(t, "snapshot-2", 2, "catalog-2", 2, 1, 2, nil)
	secondBasis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		second,
	)
	if err != nil {
		t.Fatalf("CAS PublishControlCatalog: %v", err)
	}
	if secondBasis.PointerRevision != 2 ||
		secondBasis.Control != second.ControlRef ||
		secondBasis.Catalog != second.CatalogRef {
		t.Fatalf("CAS basis=%+v", secondBasis)
	}
	var pointerRevision int64
	var snapshotID, generationID string
	if err := fixture.store.db.QueryRow(`
		SELECT snapshot_id, catalog_generation_id, pointer_revision
		FROM control_current
		WHERE tenant_id=?
	`, fixture.tenantID).Scan(
		&snapshotID,
		&generationID,
		&pointerRevision,
	); err != nil {
		t.Fatalf("read advanced pointer: %v", err)
	}
	if snapshotID != second.ControlRef.SnapshotID ||
		generationID != second.CatalogRef.GenerationID ||
		pointerRevision != 2 {
		t.Fatalf(
			"advanced pointer=(%q,%q,%d)",
			snapshotID,
			generationID,
			pointerRevision,
		)
	}
}

func TestPublishControlCatalogStaleConflictWritesNothing(t *testing.T) {
	fixture := newPublicationFixture(t)
	first := fixture.input(t, "snapshot-live", 1, "catalog-live", 1, 0, 1, nil)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("initial PublishControlCatalog: %v", err)
	}
	before := publicationRowCounts(t, fixture.store)

	stale := fixture.input(t, "snapshot-stale", 2, "catalog-stale", 2, 0, 1, nil)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		stale,
	); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("stale publication error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != before {
		t.Fatalf("stale publication changed rows: before=%v after=%v", before, after)
	}
}

func TestPublishControlCatalogRejectsBrokenClosureWithoutWrites(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*publicationFixture, *publicationShape)
	}{
		{
			name: "missing config content",
			mutate: func(_ *publicationFixture, shape *publicationShape) {
				shape.config = strings.Repeat("e", 64)
			},
		},
		{
			name: "wrong config content kind",
			mutate: func(fixture *publicationFixture, shape *publicationShape) {
				shape.config = fixture.wrongConfig
			},
		},
		{
			name: "forged activation provider",
			mutate: func(_ *publicationFixture, shape *publicationShape) {
				shape.activation.ArtifactDigest = strings.Repeat("d", 64)
			},
		},
		{
			name: "forged catalog port",
			mutate: func(_ *publicationFixture, shape *publicationShape) {
				shape.catalogPort = moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				}
			},
		},
		{
			name: "binding does not resolve",
			mutate: func(_ *publicationFixture, shape *publicationShape) {
				shape.bindingInstance = "missing-instance"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			before := publicationRowCounts(t, fixture.store)
			input := fixture.input(
				t,
				"snapshot-broken",
				1,
				"catalog-broken",
				1,
				0,
				1,
				test.mutate,
			)
			if _, err := fixture.store.PublishControlCatalog(
				context.Background(),
				input,
			); !errors.Is(err, ErrPublicationConflict) {
				t.Fatalf("broken closure error=%v", err)
			}
			if after := publicationRowCounts(t, fixture.store); after != before {
				t.Fatalf(
					"broken closure changed publication rows: before=%v after=%v",
					before,
					after,
				)
			}
		})
	}
}

func TestPublishControlCatalogRejectsUnknownModelAuthoritySchemaWithoutWrites(
	t *testing.T,
) {
	fixture := newPublicationFixture(t)
	unknownAuthority := putPublicationJSON(
		t,
		fixture.store,
		ContentAuthorityCeiling,
		[]byte(`{"network":false}`),
	)
	before := publicationRowCounts(t, fixture.store)
	input := fixture.input(
		t,
		"snapshot-unknown-model-authority",
		1,
		"catalog-unknown-model-authority",
		1,
		0,
		1,
		func(_ *publicationFixture, shape *publicationShape) {
			shape.authority = unknownAuthority
		},
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("unknown Model authority publication error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != before {
		t.Fatalf(
			"unknown Model authority changed publication rows: before=%v after=%v",
			before,
			after,
		)
	}
}

func TestPublishControlCatalogRejectsInvalidModelProfileBeforeRun(t *testing.T) {
	tests := []struct {
		name   string
		attach func(*testing.T, *publicationFixture, *publicationShape)
	}{
		{
			name: "missing CONFIG",
			attach: func(
				_ *testing.T,
				_ *publicationFixture,
				shape *publicationShape,
			) {
				shape.modelProfile = &corecontract.ModelProfileRef{
					ID:      "model-profile-missing",
					Version: "v1",
					Digest:  strings.Repeat("f", 64),
				}
			},
		},
		{
			name: "wrong content kind",
			attach: func(
				_ *testing.T,
				fixture *publicationFixture,
				shape *publicationShape,
			) {
				shape.modelProfile = &corecontract.ModelProfileRef{
					ID:      "model-profile-wrong-kind",
					Version: "v1",
					Digest:  fixture.wrongConfig,
				}
			},
		},
		{
			name: "wrong CONFIG payload",
			attach: func(
				_ *testing.T,
				fixture *publicationFixture,
				shape *publicationShape,
			) {
				shape.modelProfile = &corecontract.ModelProfileRef{
					ID:      "model-profile-wrong-payload",
					Version: "v1",
					Digest:  fixture.config,
				}
			},
		},
		{
			name: "provider mismatch",
			attach: modelProfilePublicationMutation(func(
				profile *corecontract.ModelProfileV1,
			) {
				profile.Provider = "other-provider"
			}),
		},
		{
			name: "model mismatch",
			attach: modelProfilePublicationMutation(func(
				profile *corecontract.ModelProfileV1,
			) {
				profile.Model = "other-model"
			}),
		},
		{
			name: "model build mismatch",
			attach: modelProfilePublicationMutation(func(
				profile *corecontract.ModelProfileV1,
			) {
				profile.ModelBuildID = "other-build"
			}),
		},
		{
			name: "adapter mismatch",
			attach: modelProfilePublicationMutation(func(
				profile *corecontract.ModelProfileV1,
			) {
				profile.AdapterArtifactDigest = strings.Repeat("b", 64)
				profile.AdapterIdentity = "other.adapter"
			}),
		},
		{
			name: "config ref mismatch",
			attach: modelProfilePublicationMutation(func(
				profile *corecontract.ModelProfileV1,
			) {
				profile.ModelConfigRef = strings.Repeat("d", 64)
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			beforePublication := publicationRowCounts(t, fixture.store)
			beforeRuntime := runAndModelAttemptCounts(t, fixture.store)
			input := fixture.input(
				t,
				"snapshot-profile-invalid",
				1,
				"catalog-profile-invalid",
				1,
				0,
				1,
				func(
					fixture *publicationFixture,
					shape *publicationShape,
				) {
					test.attach(t, fixture, shape)
				},
			)
			if _, err := fixture.store.PublishControlCatalog(
				context.Background(),
				input,
			); !errors.Is(err, ErrPublicationConflict) {
				t.Fatalf("invalid ModelProfile publication error=%v", err)
			}
			if after := publicationRowCounts(t, fixture.store); after != beforePublication {
				t.Fatalf(
					"invalid ModelProfile changed publication rows: before=%v after=%v",
					beforePublication,
					after,
				)
			}
			if after := runAndModelAttemptCounts(t, fixture.store); after != beforeRuntime {
				t.Fatalf(
					"invalid ModelProfile reached Run/Attempt writes: before=%v after=%v",
					beforeRuntime,
					after,
				)
			}
		})
	}
}

func TestPublishControlCatalogRejectsModelProfileBelowReservedOutput(
	t *testing.T,
) {
	fixture := newPublicationFixture(t)
	fixture.context = putPublicationContextPolicyWithLimits(
		t,
		fixture.store,
		1000,
		100,
	)
	beforePublication := publicationRowCounts(t, fixture.store)
	beforeRuntime := runAndModelAttemptCounts(t, fixture.store)
	input := fixture.input(
		t,
		"snapshot-profile-too-small",
		1,
		"catalog-profile-too-small",
		1,
		0,
		1,
		func(fixture *publicationFixture, shape *publicationShape) {
			ref := putPublicationModelProfile(t, fixture, 100, nil)
			shape.modelProfile = &ref
		},
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("too-small ModelProfile publication error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != beforePublication {
		t.Fatalf(
			"too-small ModelProfile changed publication rows: before=%v after=%v",
			beforePublication,
			after,
		)
	}
	if after := runAndModelAttemptCounts(t, fixture.store); after != beforeRuntime {
		t.Fatalf(
			"too-small ModelProfile reached Run/Attempt writes: before=%v after=%v",
			beforeRuntime,
			after,
		)
	}
}

func TestPublishControlCatalogConflictRollsBackControlInsert(t *testing.T) {
	fixture := newPublicationFixture(t)
	now := int64(1)
	if _, err := fixture.store.db.Exec(`
		INSERT INTO control_snapshots(
			snapshot_id, tenant_id, revision, canonical_json, digest, published_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		"seed-snapshot",
		fixture.tenantID,
		99,
		[]byte(`{"seed":true}`),
		strings.Repeat("8", 64),
		now,
	); err != nil {
		t.Fatalf("insert seed ControlSnapshot: %v", err)
	}
	if _, err := fixture.store.db.Exec(`
		INSERT INTO runtime_catalog_generations(
			generation_id, tenant_id, generation, control_snapshot_id,
			canonical_json, digest, published_at
		) VALUES(?, ?, ?, ?, ?, ?, ?)
	`,
		"seed-catalog",
		fixture.tenantID,
		1,
		"seed-snapshot",
		[]byte(`{"seed":true}`),
		strings.Repeat("9", 64),
		now,
	); err != nil {
		t.Fatalf("insert seed CatalogGeneration: %v", err)
	}
	before := publicationRowCounts(t, fixture.store)
	input := fixture.input(
		t,
		"snapshot-must-rollback",
		1,
		"catalog-conflict",
		1,
		0,
		1,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("conflicting publication error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != before {
		t.Fatalf("conflict did not fully roll back: before=%v after=%v", before, after)
	}
	var inserted int
	if err := fixture.store.db.QueryRow(`
		SELECT count(*) FROM control_snapshots WHERE snapshot_id=?
	`, input.ControlRef.SnapshotID).Scan(&inserted); err != nil {
		t.Fatal(err)
	}
	if inserted != 0 {
		t.Fatalf("rolled-back ControlSnapshot count=%d", inserted)
	}
}

type publicationShape struct {
	activation      moduleapi.ActivatedModuleRef
	catalogPort     moduleapi.PortRef
	bindingPort     moduleapi.PortRef
	bindingInstance string
	config          string
	authority       string
	modelProfile    *corecontract.ModelProfileRef
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	ctx := context.Background()
	store := openModuleTestStore(t)
	modelConfig, modelConfigCanonical, err :=
		moduleapi.NewModelBindingConfigV2(
			moduleapi.ModelBindingConfigV2{
				SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
				Provider:      "deepseek",
				Model:         "deepseek-v4-pro",
				ModelBuildID:  "deepseek-v4-pro-build-test",
				Parameters:    []byte(`{"max_tokens":64,"temperature":0}`),
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &publicationFixture{
		store:       store,
		tenantID:    "tenant-publish",
		port:        moduleapi.PortRef{Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2},
		modelConfig: modelConfig,
		config:      putPublicationJSON(t, store, ContentConfig, modelConfigCanonical),
		authority: putPublicationJSON(
			t,
			store,
			ContentAuthorityCeiling,
			[]byte(denyAllAuthorityCeilingCanonicalV1),
		),
	}
	fixture.wrongConfig = putPublicationJSON(
		t,
		store,
		ContentTaskInput,
		[]byte(`{"temperature":0}`),
	)
	fixture.context = putPublicationContextPolicy(t, store)
	fixture.scheduling = putPublicationPolicy(
		t,
		store,
		"scheduling-policy",
		corecontract.PolicyScheduling,
	)

	manifest := canonicalModuleManifest(
		t,
		"test.model",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		nil,
	)
	artifactDigest := strings.Repeat("a", 64)
	installation, err := store.InstallModule(
		ctx,
		installInput(t, "installation-model", manifest, artifactDigest),
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	activation, err := store.ActivateModule(ctx, ActivateModuleInput{
		ActivationID:       "activation-model",
		TenantID:           fixture.tenantID,
		InstanceID:         "instance-model",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "core.model.adapter",
	})
	if err != nil {
		t.Fatalf("ActivateModule: %v", err)
	}
	fixture.activation = moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	return fixture
}

func (fixture *publicationFixture) input(
	t *testing.T,
	snapshotID string,
	controlRevision uint64,
	generationID string,
	catalogGeneration uint64,
	expectedPointer uint64,
	newPointer uint64,
	mutate func(*publicationFixture, *publicationShape),
) PublishControlCatalogInput {
	t.Helper()
	shape := publicationShape{
		activation:      fixture.activation,
		catalogPort:     fixture.port,
		bindingPort:     fixture.port,
		bindingInstance: fixture.activation.InstanceID,
		config:          fixture.config,
		authority:       fixture.authority,
	}
	if mutate != nil {
		mutate(fixture, &shape)
	}
	control, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
			SnapshotID:    snapshotID,
			TenantID:      fixture.tenantID,
			Revision:      controlRevision,
			Profiles: []controlcontract.ProfileDefinition{
				{
					Profile: corecontract.ProfileRef{
						ID:      "profile-default",
						Version: "v1",
						Digest:  strings.Repeat("1", 64),
					},
					ContextPolicy:    fixture.context,
					SchedulingPolicy: fixture.scheduling,
					ModelProfile:     shape.modelProfile,
					Bindings: []controlcontract.BindingSpec{
						{
							Port:                shape.bindingPort,
							InstanceID:          shape.bindingInstance,
							ConfigRef:           shape.config,
							AuthorityCeilingRef: shape.authority,
							FailurePolicy:       moduleapi.FailureRequired,
						},
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewControlSnapshot: %v", err)
	}
	_ = control
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(
		controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          generationID,
			Generation:            catalogGeneration,
			TenantID:              fixture.tenantID,
			ControlSnapshotID:     controlRef.SnapshotID,
			ControlSnapshotDigest: controlRef.Digest,
			Entries: []controlcontract.CatalogEntry{
				{
					Activation: shape.activation,
					Provides:   []moduleapi.PortRef{shape.catalogPort},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewCatalogGeneration: %v", err)
	}
	return PublishControlCatalogInput{
		ExpectedPointerRevision: expectedPointer,
		NewPointerRevision:      newPointer,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}
}

func putPublicationPolicy(
	t *testing.T,
	store *Store,
	id string,
	policyType corecontract.PolicyType,
) corecontract.PolicyRef {
	t.Helper()
	_, ref, canonical, err := corecontract.NewPolicyDocument(
		id,
		"v1",
		policyType,
		[]byte(`{"enabled":true}`),
	)
	if err != nil {
		t.Fatalf("NewPolicyDocument(%s): %v", id, err)
	}
	record, err := store.PutContent(context.Background(), ContentInput{
		Digest:         ref.Digest,
		Kind:           ContentPolicy,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	})
	if err != nil {
		t.Fatalf("PutContent policy %s: %v", id, err)
	}
	if record.Digest != ref.Digest {
		t.Fatalf("policy digest=%s want %s", record.Digest, ref.Digest)
	}
	return ref
}

func putPublicationContextPolicy(
	t *testing.T,
	store *Store,
) corecontract.PolicyRef {
	return putPublicationContextPolicyWithLimits(t, store, 1000, 64)
}

func putPublicationContextPolicyWithLimits(
	t *testing.T,
	store *Store,
	contextWindowTokens uint64,
	reservedOutputTokens uint64,
) corecontract.PolicyRef {
	t.Helper()
	_, body, err := corecontract.NewContextPolicyV1(
		corecontract.ContextPolicyV1{
			SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
			ContextWindowTokens:  contextWindowTokens,
			ReservedOutputTokens: reservedOutputTokens,
			RecentHistoryTurns:   0,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		},
	)
	if err != nil {
		t.Fatalf("NewContextPolicyV1: %v", err)
	}
	_, ref, canonical, err := corecontract.NewPolicyDocument(
		"context-policy",
		"v1",
		corecontract.PolicyContext,
		body,
	)
	if err != nil {
		t.Fatalf("NewPolicyDocument(context-policy): %v", err)
	}
	if _, err := store.PutContent(context.Background(), ContentInput{
		Digest:         ref.Digest,
		Kind:           ContentPolicy,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("PutContent context policy: %v", err)
	}
	return ref
}

func putPublicationModelProfile(
	t *testing.T,
	fixture *publicationFixture,
	contextWindowTokens uint64,
	mutate func(*corecontract.ModelProfileV1),
) corecontract.ModelProfileRef {
	t.Helper()
	profile := corecontract.ModelProfileV1{
		SchemaVersion:          corecontract.ModelProfileSchemaVersionV1,
		ID:                     "model-profile-deepseek-v4-pro",
		Version:                "v1",
		Provider:               fixture.modelConfig.Provider,
		Model:                  fixture.modelConfig.Model,
		ModelBuildID:           fixture.modelConfig.ModelBuildID,
		ModelConfigRef:         fixture.config,
		AdapterArtifactDigest:  fixture.activation.ArtifactDigest,
		AdapterIdentity:        fixture.activation.AdapterIdentity,
		ContextWindowTokens:    contextWindowTokens,
		EvaluationSuite:        "currentstore-gate-suite",
		EvaluationVersion:      "v1",
		EvaluationResultDigest: strings.Repeat("e", 64),
		CapabilityTendencies: []corecontract.ModelTendencyV1{
			{MetricID: "coding", ScoreBasisPoints: 9000},
		},
		ReliabilityTendencies: []corecontract.ModelTendencyV1{
			{MetricID: "hallucination-resistance", ScoreBasisPoints: 8000},
		},
	}
	if mutate != nil {
		mutate(&profile)
	}
	_, ref, canonical, err := corecontract.NewModelProfileV1(profile)
	if err != nil {
		t.Fatalf("NewModelProfileV1: %v", err)
	}
	if _, err := fixture.store.PutContent(context.Background(), ContentInput{
		Digest:         ref.Digest,
		Kind:           ContentConfig,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("PutContent ModelProfile: %v", err)
	}
	return ref
}

func modelProfilePublicationMutation(
	mutate func(*corecontract.ModelProfileV1),
) func(*testing.T, *publicationFixture, *publicationShape) {
	return func(
		t *testing.T,
		fixture *publicationFixture,
		shape *publicationShape,
	) {
		ref := putPublicationModelProfile(t, fixture, 1000, mutate)
		shape.modelProfile = &ref
	}
}

func putPublicationJSON(
	t *testing.T,
	store *Store,
	kind ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("CanonicalJSON(%s): %v", kind, err)
	}
	digest, err := ComputeContentDigest(kind, "application/json", canonical)
	if err != nil {
		t.Fatalf("ComputeContentDigest(%s): %v", kind, err)
	}
	if _, err := store.PutContent(context.Background(), ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("PutContent(%s): %v", kind, err)
	}
	return digest
}

func publicationRowCounts(t *testing.T, store *Store) [3]int {
	t.Helper()
	var counts [3]int
	for index, table := range []string{
		"control_snapshots",
		"runtime_catalog_generations",
		"control_current",
	} {
		query := fmt.Sprintf("SELECT count(*) FROM %s", table)
		if err := store.db.QueryRow(query).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}

func runAndModelAttemptCounts(t *testing.T, store *Store) [2]int {
	t.Helper()
	var counts [2]int
	for index, table := range []string{"runs", "model_dispatch_attempts"} {
		query := fmt.Sprintf("SELECT count(*) FROM %s", table)
		if err := store.db.QueryRow(query).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}
