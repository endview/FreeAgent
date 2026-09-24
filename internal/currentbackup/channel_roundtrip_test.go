package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type channelBackupFixture struct {
	databasePath string
	artifactRoot string
	networkCalls *atomic.Uint32
}

func TestChannelBundleRoundTripPreservesCursorAndUnknownWithoutExternalAccess(
	t *testing.T,
) {
	fixture := newChannelBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "channel.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-channel-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Channel): %v", err)
	}
	if manifest.AttemptCounts.ChannelIngressReceipts != 3 ||
		manifest.AttemptCounts.ChannelCursorScopes != 1 ||
		manifest.AttemptCounts.ChannelSendPending != 0 ||
		manifest.AttemptCounts.ChannelSendUnknown != 1 {
		t.Fatalf("Channel manifest counts=%+v", manifest.AttemptCounts)
	}
	if got := fixture.networkCalls.Load(); got != 0 {
		t.Fatalf("CreateBundle accessed Channel network: %d", got)
	}
	verified, err := VerifyBundle(context.Background(), bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Channel): %v", err)
	}
	if verified.AttemptCounts != manifest.AttemptCounts ||
		verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verified Channel manifest=%+v want %+v", verified, manifest)
	}
	if got := fixture.networkCalls.Load(); got != 0 {
		t.Fatalf("VerifyBundle accessed Channel network: %d", got)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle(Channel): %v", err)
	}
	if got := fixture.networkCalls.Load(); got != 0 {
		t.Fatalf("RestoreBundle accessed Channel network: %d", got)
	}
	restoredState, err := inspectSnapshot(context.Background(), restoredDatabase)
	if err != nil {
		t.Fatalf("inspect restored Channel snapshot: %v", err)
	}
	if restoredState.AttemptCounts != manifest.AttemptCounts {
		t.Fatalf(
			"restored Channel counts=%+v want %+v",
			restoredState.AttemptCounts,
			manifest.AttemptCounts,
		)
	}
	assertRestoredUnknownChannelClosure(t, restoredDatabase)
	if got := fixture.networkCalls.Load(); got != 0 {
		t.Fatalf("read-only restored inspection accessed Channel network: %d", got)
	}
}

func TestChannelBundleAcceptsMonotonicFactsAfterUnknownReconciliation(
	t *testing.T,
) {
	for _, outcome := range []moduleapi.ChannelExecutionOutcomeV1{
		moduleapi.ChannelExecutionSucceeded,
		moduleapi.ChannelExecutionFailed,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			fixture := newChannelBackupFixtureWithOutcome(t, outcome)
			bundle := filepath.Join(t.TempDir(), "channel-terminal.bundle")
			if _, err := CreateBundle(
				context.Background(),
				fixture.databasePath,
				fixture.artifactRoot,
				bundle,
				"currentbackup-channel-test/v1",
			); err != nil {
				t.Fatalf("CreateBundle(%s after UNKNOWN): %v", outcome, err)
			}
			if _, err := VerifyBundle(context.Background(), bundle); err != nil {
				t.Fatalf("VerifyBundle(%s after UNKNOWN): %v", outcome, err)
			}
			if got := fixture.networkCalls.Load(); got != 0 {
				t.Fatalf("backup verification accessed Channel network: %d", got)
			}
		})
	}
}

func TestChannelBundleAcceptsPendingRuntimeProjection(t *testing.T) {
	fixture := newChannelBackupPendingFixture(t)
	bundle := filepath.Join(t.TempDir(), "channel-pending.bundle")
	manifest, err := CreateBundle(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-channel-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(PENDING Channel): %v", err)
	}
	if manifest.AttemptCounts.ChannelSendPending != 1 ||
		manifest.AttemptCounts.ChannelSendUnknown != 0 {
		t.Fatalf("PENDING Channel counts=%+v", manifest.AttemptCounts)
	}
}

func TestChannelManifestDeclarationsFailClosedForCurrentBackup(t *testing.T) {
	fixture := newChannelBackupFixture(t)
	tests := []struct {
		name      string
		configure func(*moduleapi.ModuleManifestV1)
	}{
		{
			name: "Requires",
			configure: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = []moduleapi.PortRef{{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV2,
				}}
			},
		},
		{
			name: "requested permissions",
			configure: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = []moduleapi.Permission{
					moduleapi.PermissionKnowledgeReadV1,
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "declared-channel.sqlite")
			copyTestFile(t, fixture.databasePath, copyPath)
			database, err := sql.Open("sqlite", sqliteFileURI(copyPath, "rw"))
			if err != nil {
				t.Fatal(err)
			}
			tamperChannelBackupManifestDeclarations(
				t,
				database,
				test.configure,
			)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			before := channelPublicationAndCursorRowCounts(t, copyPath)
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				copyPath,
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), "ChannelEndpoint Manifest") {
				t.Fatalf(
					"VerifyCurrentStoreSemanticClosure(Channel declarations) error=%v",
					err,
				)
			}

			bundle := filepath.Join(t.TempDir(), "rejected.bundle")
			if _, err := CreateBundle(
				context.Background(),
				copyPath,
				fixture.artifactRoot,
				bundle,
				"currentbackup-channel-test/v1",
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), "ChannelEndpoint Manifest") {
				t.Fatalf("CreateBundle(Channel declarations) error=%v", err)
			}
			if _, err := os.Stat(bundle); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed Channel backup published destination: %v", err)
			}
			if after := channelPublicationAndCursorRowCounts(t, copyPath); after != before {
				t.Fatalf(
					"failed Channel backup wrote publication/cursor rows: before=%v after=%v",
					before,
					after,
				)
			}
		})
	}
}

func TestChannelSemanticVerifierRejectsBrokenClosure(t *testing.T) {
	fixture := newChannelBackupFixture(t)
	tests := []struct {
		name                  string
		tamper                func(*testing.T, *sql.DB)
		mutationRejectedEarly bool
	}{
		{
			name: "cursor revision gap",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE channel_ingress_receipts
					SET cursor_revision=4
					WHERE disposition='ACCEPTED'
				`)
			},
		},
		{
			name: "accepted principal drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE channel_ingress_receipts
					SET principal_id='another-principal'
					WHERE disposition='ACCEPTED'
				`)
			},
		},
		{
			name: "rejected carries Run",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE channel_ingress_receipts
					SET disposition='REJECTED'
					WHERE disposition='ACCEPTED'
				`)
			},
		},
		{
			name: "channel Binding drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"dispatch_attempts_observation_update_guard"},
					`
					UPDATE dispatch_attempts
					SET binding_json=x'7b7d'
					WHERE dispatch_kind='CHANNEL_SEND'
				`)
			},
		},
		{
			name: "channel evidence kind drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"content_records_reject_update"},
					`
					UPDATE content_records SET kind='POLICY'
					WHERE content_digest=(
						SELECT reconciliation_evidence_ref
						FROM dispatch_attempts
						WHERE dispatch_kind='CHANNEL_SEND'
					)
				`)
			},
		},
		{
			name: "channel Frame waiting projection drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE loop_frames
					SET waiting_reason='FORGED_WAITING_REASON'
					WHERE run_id=(
						SELECT run_id FROM dispatch_attempts
						WHERE dispatch_kind='CHANNEL_SEND'
					)
				`)
			},
		},
		{
			name: "channel Run state projection drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE runs SET state='ADMITTED', disposition=NULL
					WHERE run_id=(
						SELECT run_id FROM dispatch_attempts
						WHERE dispatch_kind='CHANNEL_SEND'
					)
				`)
			},
		},
		{
			name:   "channel latest Event identity drift",
			tamper: tamperLatestChannelEventAttemptIdentity,
		},
		{
			name: "channel assistant History role drift",
			tamper: func(t *testing.T, database *sql.DB) {
				t.Helper()
				execClosedFileTamperV1(t, database, nil, `
					UPDATE history_entries SET role='user'
					WHERE source_attempt_id=(
						SELECT source_model_attempt_id FROM dispatch_attempts
						WHERE dispatch_kind='CHANNEL_SEND'
					)
				`)
			},
		},
		{
			name:                  "accepted Run rejects an additional raw Channel Attempt",
			tamper:                assertAdditionalChannelAttemptRejected,
			mutationRejectedEarly: true,
		},
		{
			name:   "channel evidence is canonical JSON but not an object",
			tamper: tamperChannelEvidenceWithNonObject,
		},
		{
			name:   "channel evidence exceeds bounded object depth",
			tamper: tamperChannelEvidenceWithDeepObject,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.databasePath, copyPath)
			database, err := sql.Open("sqlite", sqliteFileURI(copyPath, "rw"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			test.tamper(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if test.mutationRejectedEarly {
				return
			}
			_, err = CreateBundle(
				context.Background(),
				copyPath,
				fixture.artifactRoot,
				filepath.Join(t.TempDir(), "rejected.bundle"),
				"currentbackup-channel-test/v1",
			)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle(tampered Channel) error=%v, want ErrIntegrity", err)
			}
		})
	}
}

func TestChannelSemanticVerifierRejectsDeletedTerminalSend(t *testing.T) {
	fixture := newChannelBackupFixtureWithOutcome(
		t,
		moduleapi.ChannelExecutionSucceeded,
	)
	copyPath := filepath.Join(t.TempDir(), "deleted-channel-send.sqlite")
	copyTestFile(t, fixture.databasePath, copyPath)
	database, err := sql.Open("sqlite", sqliteFileURI(copyPath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	execClosedFileTamperV1(t, database, nil, `
		DELETE FROM dispatch_attempts WHERE dispatch_kind='CHANNEL_SEND'
	`)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = CreateBundle(
		context.Background(),
		copyPath,
		fixture.artifactRoot,
		filepath.Join(t.TempDir(), "deleted-channel-send.bundle"),
		"currentbackup-channel-test/v1",
	)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("CreateBundle(deleted terminal Channel) error=%v, want ErrIntegrity", err)
	}
}

func newChannelBackupFixture(t *testing.T) channelBackupFixture {
	return newChannelBackupFixtureWithOutcome(
		t,
		moduleapi.ChannelExecutionUnknown,
	)
}

func newChannelBackupFixtureWithOutcome(
	t *testing.T,
	finalOutcome moduleapi.ChannelExecutionOutcomeV1,
) channelBackupFixture {
	return newChannelBackupFixtureWithState(t, finalOutcome, false)
}

func newChannelBackupPendingFixture(t *testing.T) channelBackupFixture {
	return newChannelBackupFixtureWithState(t, "", true)
}

func newChannelBackupFixtureWithState(
	t *testing.T,
	finalOutcome moduleapi.ChannelExecutionOutcomeV1,
	leavePending bool,
) channelBackupFixture {
	t.Helper()
	ctx := context.Background()
	var networkCalls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		networkCalls.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatal(err)
	}
	modelAssertion := prepared.ModelAssertion()
	modelProvider := activatedModuleFromAssertion(modelAssertion)
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
		Invoker:         echo,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist: []activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        modelProvider.ModuleID,
				ExactVersion:    modelProvider.Version,
				ArtifactDigest:  modelProvider.ArtifactDigest,
				AdapterIdentity: modelProvider.AdapterIdentity,
			}},
		},
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "channel.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
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
	if _, err := prepared.Import(ctx, store, resolver); err != nil {
		t.Fatalf("import Channel base seed: %v", err)
	}

	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, assertion := range prepared.ModuleAssertions() {
		verified, err := verifyArtifactDirectory(
			assertion.ArtifactDirectory,
			assertion.ArtifactDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := copyVerifiedArtifact(
			ctx,
			assertion.ArtifactDirectory,
			filepath.Join(artifactRoot, assertion.ArtifactDigest),
			verified,
		); err != nil {
			t.Fatal(err)
		}
	}

	channelManifest := channelBackupModuleManifest(t)
	channelDigest, err := moduleapi.ComputeArtifactDigest(channelManifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	channelArtifact := filepath.Join(artifactRoot, channelDigest)
	if err := os.Mkdir(channelArtifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(channelArtifact, moduleapi.ArtifactManifestPath),
		channelManifest,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		channelManifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      "installation-channel-backup",
		ModuleID:            "freeagent.test.channel.backup",
		ExactVersion:        "1.0.0",
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       channelManifest,
		ArtifactDigest:      channelDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       "activation-channel-backup",
		TenantID:           prepared.DefaultAssembly().TenantID,
		InstanceID:         "channel-backup-instance",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "freeagent.adapter.channel.no-access-test/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}

	_, configCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: "loopback-http/v1",
			SecretRef:       "placeholder",
			Parameters: json.RawMessage(
				`{"endpoint_url":` + mustChannelJSONString(t, server.URL+"/deliver") + `}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := putChannelBackupContent(
		t,
		store,
		currentstore.ContentConfig,
		configCanonical,
	)
	assembly := prepared.DefaultAssembly()
	_, authorityCanonical, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            assembly.TenantID,
			AllowedWorkspaceIDs: []string{assembly.WorkspaceID},
			AllowedEndpointIDs:  []string{"channel-backup-endpoint"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := putChannelBackupContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	port := moduleapi.PortRef{
		Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1,
	}
	bindingSpec := controlcontract.BindingSpec{
		Port:                port,
		InstanceID:          provider.InstanceID,
		ConfigRef:           config.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	binding := moduleapi.PortBinding{
		Provider: provider, ConfigRef: config.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil {
		t.Fatal(err)
	}

	basis, control, catalog, err := store.LoadPublishedBasis(ctx, assembly.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "control-channel-backup-disabled"
	control.Revision++
	control.Digest = ""
	workspace, found := control.FindWorkspace(assembly.WorkspaceID)
	if !found {
		t.Fatal("Channel Workspace is absent")
	}
	workspace.ChannelEndpoints = []controlcontract.ChannelEndpointDefinition{{
		SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
		EndpointID:      "channel-backup-endpoint",
		Channel:         "loopback-http",
		AccountID:       "channel-backup-account",
		ConversationID:  "channel-backup-conversation",
		TargetAgentID:   assembly.AgentID,
		TargetProfileID: assembly.ProfileID,
		CursorScopeKey:  "channel-backup-conversation",
		Enabled:         false,
		Binding:         bindingSpec,
	}}
	workspace.ChannelIdentities = []controlcontract.ChannelIdentityDefinition{{
		Channel:        "loopback-http",
		AccountID:      "channel-backup-account",
		ExternalUserID: "channel-backup-user",
		PrincipalID:    "channel-backup-principal",
		ACLEpoch:       7,
		Active:         true,
	}}
	for index := range control.Workspaces {
		if control.Workspaces[index].Workspace.ID == assembly.WorkspaceID {
			control.Workspaces[index] = workspace
		}
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-channel-backup-disabled"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   []moduleapi.PortRef{port},
	})
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis = publishChannelBackupBasis(
		t,
		store,
		basis,
		controlRef,
		controlCanonical,
		catalogRef,
		catalogCanonical,
	)
	cursor0 := channelBackupContent(
		t,
		currentstore.ContentChannelCursor,
		[]byte(`{"offset":0}`),
	)
	seeded, err := store.SeedChannelCursor(ctx, currentstore.ChannelCursorSeedInput{
		PublishedBasis:        basis,
		TenantID:              assembly.TenantID,
		WorkspaceID:           assembly.WorkspaceID,
		EndpointID:            "channel-backup-endpoint",
		CursorScopeKey:        "channel-backup-conversation",
		EndpointBindingDigest: bindingDigest,
		CursorAfter:           cursor0,
		Reason:                "OPERATOR_SEED",
		EndpointDisabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	control.SnapshotID = "control-channel-backup-enabled"
	control.Revision++
	control.Digest = ""
	for index := range control.Workspaces {
		control.Workspaces[index].ChannelEndpoints[0].Enabled = true
	}
	_, controlRef, controlCanonical, err = controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-channel-backup-enabled"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err = controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis = publishChannelBackupBasis(
		t,
		store,
		basis,
		controlRef,
		controlCanonical,
		catalogRef,
		catalogCanonical,
	)

	accepted := commitChannelBackupAccepted(
		t,
		store,
		basis,
		controlCanonical,
		catalogCanonical,
		assembly,
		bindingDigest,
		seeded,
	)
	commitChannelBackupUnknown(
		t,
		store,
		accepted,
		provider,
		finalOutcome,
		leavePending,
	)
	commitChannelBackupRejected(t, store, basis, assembly, bindingDigest, accepted.Receipt)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return channelBackupFixture{
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		networkCalls: &networkCalls,
	}
}

func commitChannelBackupAccepted(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	controlCanonical, catalogCanonical []byte,
	assembly bootstrapseed.DefaultAssembly,
	bindingDigest string,
	seeded currentstore.ChannelIngressReceipt,
) currentstore.ChannelIngressAdmissionResult {
	t.Helper()
	ctx := context.Background()
	before, err := store.GetContent(ctx, seeded.CursorAfterRef)
	if err != nil {
		t.Fatal(err)
	}
	after := channelBackupContent(
		t,
		currentstore.ContentChannelCursor,
		[]byte(`{"offset":1}`),
	)
	replyTarget := json.RawMessage(`{"conversation":"channel-backup-conversation"}`)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "channel-backup-endpoint",
			ProviderEventID: "channel-backup-event-accepted",
			ExternalUserID:  "channel-backup-user",
			Message:         "verify this backup",
			ReplyTarget:     replyTarget,
			CursorBefore:    json.RawMessage(before.CanonicalBytes),
			CursorAfter:     json.RawMessage(after.CanonicalBytes),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := channelBackupContent(
		t,
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	ingressKey, eventDigest, err := currentstore.ComputeChannelIngressIdentity(
		assembly.TenantID,
		"channel-backup-endpoint",
		"channel-backup-event-accepted",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          "verify this backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := channelBackupContent(t, currentstore.ContentTaskInput, taskCanonical)
	deadline := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Microsecond)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      assembly.TenantID,
			AdmissionKey:  "channel/v1/" + ingressKey,
			PrincipalID:   "channel-backup-principal",
			WorkspaceID:   assembly.WorkspaceID,
			AgentID:       assembly.AgentID,
			ProfileID:     assembly.ProfileID,
			TaskInputRef:  task.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2,
			}},
			ChannelEndpointID: "channel-backup-endpoint",
			Deadline:          deadline,
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "channel-backup-run",
			MemberID:         "channel-backup-member",
			RecoveryRootRef:  "recovery/channel-backup-run",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input := currentstore.CommitChannelIngressAdmissionInput{
		Ingress: currentstore.ChannelIngressEventInput{
			PublishedBasis:         basis,
			TenantID:               assembly.TenantID,
			WorkspaceID:            assembly.WorkspaceID,
			EndpointID:             "channel-backup-endpoint",
			CursorScopeKey:         "channel-backup-conversation",
			ExpectedCursorRevision: seeded.CursorRevision,
			CursorBeforeRef:        seeded.CursorAfterRef,
			CursorAfter:            after,
			EndpointBindingDigest:  bindingDigest,
			IngressKey:             ingressKey,
			ProviderEventIDDigest:  eventDigest,
			Envelope:               envelope,
			Reason:                 "AUTHORIZED",
		},
		PrincipalID: "channel-backup-principal",
		ACLEpoch:    7,
		Admission: currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		},
	}
	result, err := store.CommitChannelIngressAndRunAdmission(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func commitChannelBackupUnknown(
	t *testing.T,
	store *currentstore.Store,
	accepted currentstore.ChannelIngressAdmissionResult,
	channelProvider moduleapi.ActivatedModuleRef,
	finalOutcome moduleapi.ChannelExecutionOutcomeV1,
	leavePending bool,
) {
	t.Helper()
	ctx := context.Background()
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 accepted.Admission.RunID,
		OwnerID:               "channel-backup-loop",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: 0,
		TTL:                   2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled := compileChannelBackupModelRequest(t, store, lease)
	begin, err := store.BeginModelDispatch(ctx, currentstore.BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   "channel-backup-model-attempt",
		LogicalStepID:               corecontract.PureChatModelLogicalStepIDV1,
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline:                    time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	assistantText := "channel backup final answer"
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     assistantText,
			ProviderRequestID: "channel-backup-provider-request",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	zero := uint64(0)
	_, usageCanonical, err := moduleapi.NewModelUsageReceiptV2(
		moduleapi.ModelUsageReceiptV2{
			SchemaVersion:       moduleapi.ModelUsageReceiptSchemaV2,
			InputTokens:         &zero,
			CachedInputTokens:   &zero,
			UncachedInputTokens: &zero,
			OutputTokens:        &zero,
			ReasoningTokens:     &zero,
			RawReceipt:          json.RawMessage(`{"provider":"channel-backup"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRunForLoop(ctx, begin.Lease)
	if err != nil {
		t.Fatal(err)
	}
	_, proposalCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		run.Member.MemberSnapshotDigest,
		accepted.Receipt.EndpointID,
		accepted.Receipt.IngressKey,
		json.RawMessage(`{"conversation":"channel-backup-conversation"}`),
		assistantText,
		json.RawMessage(`{"message":"channel backup final answer"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.CommitModelChannelAndBeginDispatch(
		ctx,
		currentstore.CommitModelChannelAndBeginDispatchInput{
			Lease:                        begin.Lease,
			ModelAttemptID:               begin.Attempt.AttemptID,
			InvocationID:                 begin.Attempt.AttemptID,
			Provider:                     begin.Attempt.Binding.Provider,
			ExpectedModelAttemptRevision: begin.Attempt.Revision,
			OutputCanonical:              outputCanonical,
			UsageReceiptCanonical:        usageCanonical,
			ProviderRequestID:            "channel-backup-provider-request",
			DispatchAttemptID:            "channel-backup-send-attempt",
			ProposalCanonical:            proposalCanonical,
			Deadline:                     time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if leavePending {
		if err := store.ReleaseRunLease(ctx, grant.Lease); err != nil {
			t.Fatal(err)
		}
		return
	}
	unknown, err := store.CommitChannelDispatchOutcome(
		ctx,
		currentstore.CommitChannelDispatchOutcomeInput{
			Lease:                    grant.Lease,
			AttemptID:                grant.Channel.Attempt.AttemptID,
			InvocationID:             grant.Channel.Attempt.AttemptID,
			Provider:                 channelProvider,
			ExpectedAttemptRevision:  grant.Channel.Attempt.Revision,
			Outcome:                  moduleapi.ChannelExecutionUnknown,
			ProviderReceiptCanonical: json.RawMessage(`{"delivery":"uncertain"}`),
			ExternalOperationID:      "channel-backup-operation",
			UnknownReason:            "CHANNEL_RESULT_NOT_OBSERVED",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := store.ReconcileChannelDispatchOutcome(
		ctx,
		currentstore.ReconcileChannelDispatchOutcomeInput{
			Lease:                           unknown.Lease,
			AttemptID:                       unknown.Record.Attempt.AttemptID,
			InvocationID:                    unknown.Record.Attempt.AttemptID,
			Provider:                        channelProvider,
			ExpectedAttemptRevision:         unknown.Record.Attempt.Revision,
			Outcome:                         moduleapi.ChannelExecutionUnknown,
			ProviderReceiptCanonical:        json.RawMessage(`{"delivery":"uncertain"}`),
			ExternalOperationID:             "channel-backup-operation",
			UnknownReason:                   "CHANNEL_RESULT_NOT_OBSERVED",
			ReconciliationEvidenceCanonical: json.RawMessage(`{"checked":true}`),
		},
	)
	if err != nil || !reconciled.Applied ||
		reconciled.Record.Attempt.State != currentstore.DispatchUnknown ||
		reconciled.Record.ReconciliationEvidence == nil {
		t.Fatalf("reconcile Channel UNKNOWN=%+v err=%v", reconciled, err)
	}
	finalLease := reconciled.Lease
	if finalOutcome != moduleapi.ChannelExecutionUnknown {
		finalInput := currentstore.ReconcileChannelDispatchOutcomeInput{
			Lease:                           reconciled.Lease,
			AttemptID:                       reconciled.Record.Attempt.AttemptID,
			InvocationID:                    reconciled.Record.Attempt.AttemptID,
			Provider:                        channelProvider,
			ExpectedAttemptRevision:         reconciled.Record.Attempt.Revision,
			Outcome:                         finalOutcome,
			ProviderReceiptCanonical:        json.RawMessage(`{"delivery":"uncertain"}`),
			ReconciliationEvidenceCanonical: json.RawMessage(`{"checked":true}`),
		}
		switch finalOutcome {
		case moduleapi.ChannelExecutionSucceeded:
			finalInput.ExternalOperationID = "channel-backup-operation"
		case moduleapi.ChannelExecutionFailed:
			finalInput.ErrorClassification = "DELIVERY_REJECTED"
		default:
			t.Fatalf("unsupported final Channel outcome %q", finalOutcome)
		}
		terminal, err := store.ReconcileChannelDispatchOutcome(ctx, finalInput)
		if err != nil || !terminal.Applied ||
			terminal.Record.Attempt.State != currentstore.DispatchState(finalOutcome) ||
			terminal.Record.ReconciliationEvidence == nil ||
			terminal.Record.ProviderReceipt == nil {
			t.Fatalf("reconcile Channel terminal=%+v err=%v", terminal, err)
		}
		finalLease = terminal.Lease
	}
	if err := store.ReleaseRunLease(ctx, finalLease); err != nil {
		t.Fatal(err)
	}
}

func compileChannelBackupModelRequest(
	t *testing.T,
	store *currentstore.Store,
	lease currentstore.RunLease,
) contextcompiler.CompileResultV1 {
	t.Helper()
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	policy, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found {
		t.Fatal("Channel context policy is absent")
	}
	task, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found {
		t.Fatal("Channel task input is absent")
	}
	var modelPlan, contextPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		plan := &run.Member.PortPlans[index]
		switch plan.Port.Name {
		case moduleapi.PortNameModelGenerate:
			modelPlan = plan
		case moduleapi.PortNameContextProvide:
			contextPlan = plan
		}
	}
	if modelPlan == nil || len(modelPlan.Bindings) != 1 {
		t.Fatal("Channel model plan is absent")
	}
	modelConfigContent, found := run.FindContent(modelPlan.Bindings[0].ConfigRef)
	if !found {
		t.Fatal("Channel model config is absent")
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV2(
		modelConfigContent.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	materials := make([]contextcompiler.BindingMaterialV1, 0)
	if contextPlan != nil {
		for _, binding := range contextPlan.Bindings {
			config, found := run.FindContent(binding.ConfigRef)
			if !found {
				t.Fatal("Channel context config is absent")
			}
			material := contextcompiler.BindingMaterialV1{
				ConfigCanonical: bytes.Clone(config.CanonicalBytes),
			}
			if binding.Provider.ExecutionClass != moduleapi.ExecutionDeclarative {
				authority, found := run.FindContent(binding.AuthorityCeilingRef)
				if !found {
					t.Fatal("Channel context authority is absent")
				}
				material.AuthorityCanonical = bytes.Clone(authority.CanonicalBytes)
			}
			for _, ref := range binding.StaticContextRefs {
				static, found := run.FindContent(ref)
				if !found {
					t.Fatal("Channel static context is absent")
				}
				material.StaticContextCanonicals = append(
					material.StaticContextCanonicals,
					bytes.Clone(static.CanonicalBytes),
				)
			}
			materials = append(materials, material)
		}
	}
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: policy.CanonicalBytes,
		ModelProfileRef:                run.Member.ModelProfile,
		ModelParameters:                modelConfig.Parameters,
		ContextPlan:                    contextPlan,
		ContextBindings:                materials,
		Actions:                        run.Member.Actions,
		TaskInputRef:                   task.Digest,
		TaskInputCanonical:             task.CanonicalBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func commitChannelBackupRejected(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	assembly bootstrapseed.DefaultAssembly,
	bindingDigest string,
	current currentstore.ChannelIngressReceipt,
) {
	t.Helper()
	before, err := store.GetContent(context.Background(), current.CursorAfterRef)
	if err != nil {
		t.Fatal(err)
	}
	after := channelBackupContent(
		t,
		currentstore.ContentChannelCursor,
		[]byte(`{"offset":2}`),
	)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "channel-backup-endpoint",
			ProviderEventID: "channel-backup-event-rejected",
			ExternalUserID:  "unknown-user",
			Message:         "rejected message",
			ReplyTarget:     json.RawMessage(`{"conversation":"channel-backup-conversation"}`),
			CursorBefore:    json.RawMessage(before.CanonicalBytes),
			CursorAfter:     json.RawMessage(after.CanonicalBytes),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := channelBackupContent(
		t,
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	ingressKey, eventDigest, err := currentstore.ComputeChannelIngressIdentity(
		assembly.TenantID,
		"channel-backup-endpoint",
		"channel-backup-event-rejected",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitRejectedChannelIngress(
		context.Background(),
		currentstore.ChannelIngressEventInput{
			PublishedBasis:         basis,
			TenantID:               assembly.TenantID,
			WorkspaceID:            assembly.WorkspaceID,
			EndpointID:             "channel-backup-endpoint",
			CursorScopeKey:         "channel-backup-conversation",
			ExpectedCursorRevision: current.CursorRevision,
			CursorBeforeRef:        current.CursorAfterRef,
			CursorAfter:            after,
			EndpointBindingDigest:  bindingDigest,
			IngressKey:             ingressKey,
			ProviderEventIDDigest:  eventDigest,
			Envelope:               envelope,
			Reason:                 "UNAUTHORIZED",
		},
	); err != nil {
		t.Fatal(err)
	}
}

func publishChannelBackupBasis(
	t *testing.T,
	store *currentstore.Store,
	previous controlcontract.PublishedBasis,
	controlRef controlcontract.ControlSnapshotRef,
	controlCanonical []byte,
	catalogRef controlcontract.CatalogGenerationRef,
	catalogCanonical []byte,
) controlcontract.PublishedBasis {
	t.Helper()
	basis, err := store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: previous.PointerRevision,
			NewPointerRevision:      previous.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return basis
}

func channelBackupContent(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentInput {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.ContentInput{
		Digest: digest, Kind: kind, MediaType: "application/json",
		CanonicalBytes: bytes.Clone(canonical),
	}
}

func putChannelBackupContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentRecord {
	t.Helper()
	record, err := store.PutContent(
		context.Background(),
		channelBackupContent(t, kind, canonical),
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func channelBackupModuleManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "freeagent.test.channel.backup",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "freeagent.test.channel.backup/v1",
		},
		Provides: []moduleapi.PortRef{{
			Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatal(err)
	}
	return canonical
}

func tamperChannelBackupManifestDeclarations(
	t *testing.T,
	database *sql.DB,
	configure func(*moduleapi.ModuleManifestV1),
) {
	t.Helper()
	manifest, _, err := moduleapi.ParseModuleManifestV1(
		channelBackupModuleManifest(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	configure(&manifest)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatalf("declared Channel Manifest is invalid: %v", err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		channelJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(currentstore.ContentModuleManifest),
		channelJSONMediaType,
		canonical,
		len(canonical),
		time.Now().UTC().UnixMicro(),
	); err != nil {
		t.Fatalf("insert declared Channel Manifest: %v", err)
	}
	result, err := database.Exec(`
		UPDATE module_installations SET manifest_ref=?
		WHERE installation_id='installation-channel-backup'
	`, digest)
	if err != nil {
		t.Fatalf("replace Channel Manifest: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Channel Manifest affected=%d error=%v", affected, err)
	}
}

func channelPublicationAndCursorRowCounts(
	t *testing.T,
	databasePath string,
) [4]int {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var counts [4]int
	for index, table := range []string{
		"control_snapshots",
		"runtime_catalog_generations",
		"control_current",
		"channel_ingress_receipts",
	} {
		query := "SELECT COUNT(*) FROM " + table
		if err := database.QueryRow(query).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}

func mustChannelJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func tamperLatestChannelEventAttemptIdentity(t *testing.T, database *sql.DB) {
	t.Helper()
	var (
		runID     string
		sequence  int64
		createdAt int64
		canonical []byte
	)
	if err := database.QueryRow(`
		SELECT e.run_id, e.event_sequence, c.created_at, c.canonical_bytes
		FROM run_events AS e
		JOIN loop_frames AS f
		  ON f.run_id=e.run_id AND f.last_authoritative_event=e.event_sequence
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=(
			SELECT run_id FROM dispatch_attempts WHERE dispatch_kind='CHANNEL_SEND'
		)
	`).Scan(&runID, &sequence, &createdAt, &canonical); err != nil {
		t.Fatalf("read latest Channel event: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(canonical, &event); err != nil {
		t.Fatalf("decode latest Channel event: %v", err)
	}
	event["attempt_id"] = "forged-channel-attempt"
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentRunEventPayload,
		channelJSONMediaType,
		forged,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(currentstore.ContentRunEventPayload),
		channelJSONMediaType,
		forged,
		len(forged),
		createdAt,
	); err != nil {
		t.Fatalf("insert forged Channel event: %v", err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=?
	`,
		digest,
		digest,
		runID,
		sequence,
	)
}

func assertAdditionalChannelAttemptRejected(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO model_dispatch_attempts(
			attempt_id, logical_operation_key, run_id, member_id,
			logical_step_id, frame_revision, member_snapshot_digest,
			binding_json, context_compilation_ref, request_ref, request_digest,
			provider, model, parameters_json, deadline, usage_ledger_ref,
			source_dispatch_attempt_id,
			state, provider_request_id, provider_receipt_ref, result_ref,
			error_classification, reconciliation_evidence_ref, unknown_reason,
			revision, created_at, updated_at
		)
		SELECT
			'channel-backup-model-attempt-extra',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			run_id, member_id, 'forged-model-step', frame_revision,
			member_snapshot_digest, binding_json, context_compilation_ref,
			request_ref, request_digest, provider, model, parameters_json,
			deadline, usage_ledger_ref, NULL,
			'FAILED', provider_request_id, provider_receipt_ref, NULL,
			'FORGED_MODEL_FAILURE', reconciliation_evidence_ref, NULL,
			revision, created_at, updated_at
		FROM model_dispatch_attempts
		WHERE attempt_id=(
			SELECT source_model_attempt_id FROM dispatch_attempts
			WHERE dispatch_kind='CHANNEL_SEND'
		)
	`); err == nil {
		t.Fatal("schema accepted a raw Model Attempt without its observation closure")
	}
}

func tamperChannelEvidenceWithNonObject(t *testing.T, database *sql.DB) {
	t.Helper()
	forged := []byte(`[]`)
	tamperChannelEvidenceContent(t, database, forged)
}

func tamperChannelEvidenceWithDeepObject(t *testing.T, database *sql.DB) {
	t.Helper()
	var value any = "leaf"
	for range 40 {
		value = map[string]any{"nested": value}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	tamperChannelEvidenceContent(t, database, forged)
}

func tamperChannelEvidenceContent(
	t *testing.T,
	database *sql.DB,
	forged []byte,
) {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentReconciliationEvidence,
		channelJSONMediaType,
		forged,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(currentstore.ContentReconciliationEvidence),
		channelJSONMediaType,
		forged,
		len(forged),
		time.Now().UTC().UnixMicro(),
	); err != nil {
		t.Fatalf("insert forged Channel evidence: %v", err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"dispatch_attempts_observation_update_guard"},
		`
		UPDATE dispatch_attempts SET reconciliation_evidence_ref=?
		WHERE dispatch_kind='CHANNEL_SEND'
	`,
		digest,
	)
}

func assertRestoredUnknownChannelClosure(t *testing.T, databasePath string) {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var state, externalOperation, receiptRef, evidenceRef, unknownReason string
	var resultRef sql.NullString
	if err := database.QueryRow(`
		SELECT state, external_operation_id, provider_receipt_ref,
		       result_ref, reconciliation_evidence_ref, unknown_reason
		FROM dispatch_attempts
		WHERE dispatch_kind='CHANNEL_SEND'
	`).Scan(
		&state,
		&externalOperation,
		&receiptRef,
		&resultRef,
		&evidenceRef,
		&unknownReason,
	); err != nil {
		t.Fatal(err)
	}
	if state != string(currentstore.DispatchUnknown) ||
		externalOperation != "channel-backup-operation" ||
		!moduleapi.ValidSHA256(receiptRef) || resultRef.Valid ||
		!moduleapi.ValidSHA256(evidenceRef) ||
		unknownReason != "CHANNEL_RESULT_NOT_OBSERVED" {
		t.Fatalf(
			"restored UNKNOWN Channel closure=%q %q %q %+v %q %q",
			state,
			externalOperation,
			receiptRef,
			resultRef,
			evidenceRef,
			unknownReason,
		)
	}
}

func TestChannelBackupVerifierHasNoExecutionDependency(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range [][]byte{
			[]byte(`"net/http"`),
			[]byte(`internal/loopbackchannel`),
			[]byte(`internal/modulehost`),
			[]byte(`SecretResolver`),
		} {
			if bytes.Contains(body, forbidden) {
				t.Fatalf("production backup file %s imports/loads execution dependency %s", entry.Name(), forbidden)
			}
		}
	}
}
