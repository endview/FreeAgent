package controlapp

import (
	"context"
	"strings"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	testTenantIDV1       = "tenant-a"
	testWorkspaceAV1     = "workspace-a"
	testWorkspaceBV1     = "workspace-b"
	testWorkspaceEmptyV1 = "workspace-empty"
	testProfileInstance  = "instance-profile"
	testChannelAInstance = "instance-channel-a"
	testChannelBInstance = "instance-channel-b"
	testUnboundInstance  = "instance-unbound"
	testObservedAtV1     = uint64(2_000)
)

type testAuthorizationV1 struct {
	session     controlapicontract.ControlSessionV1
	digest      string
	allowed     map[controlapicontract.ControlScopeV1]bool
	denyAtAllow int

	sessionCalls int
	digestCalls  int
	allowCalls   int
}

func (authorization *testAuthorizationV1) Session() controlapicontract.ControlSessionV1 {
	if authorization == nil {
		return controlapicontract.ControlSessionV1{}
	}
	authorization.sessionCalls++
	result := authorization.session
	result.Capabilities = append(
		[]controlapicontract.ControlCapabilityV1(nil),
		authorization.session.Capabilities...,
	)
	return result
}

func (authorization *testAuthorizationV1) Digest() string {
	if authorization == nil {
		return ""
	}
	authorization.digestCalls++
	return authorization.digest
}

func (authorization *testAuthorizationV1) Allows(
	scope controlapicontract.ControlScopeV1,
) bool {
	if authorization == nil {
		return false
	}
	authorization.allowCalls++
	if authorization.denyAtAllow > 0 &&
		authorization.allowCalls >= authorization.denyAtAllow {
		return false
	}
	return authorization.allowed[scope]
}

type testPublishedBasisReaderV1 struct {
	basis   controlcontract.PublishedBasis
	control controlcontract.ControlSnapshot
	catalog controlcontract.CatalogGeneration

	loadErr     error
	verifyErr   error
	afterLoad   func()
	afterVerify func()

	loadCalls   int
	verifyCalls int
	lastTenant  string
}

func (reader *testPublishedBasisReaderV1) LoadPublishedBasis(
	_ context.Context,
	tenantID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	reader.loadCalls++
	reader.lastTenant = tenantID
	if reader.afterLoad != nil {
		reader.afterLoad()
	}
	if reader.loadErr != nil {
		return controlcontract.PublishedBasis{},
			controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			reader.loadErr
	}
	return reader.basis, reader.control, reader.catalog, nil
}

func (reader *testPublishedBasisReaderV1) VerifyPublishedControlCatalogClosureV1(
	_ context.Context,
	_ controlcontract.ControlSnapshot,
	_ controlcontract.CatalogGeneration,
) error {
	reader.verifyCalls++
	if reader.afterVerify != nil {
		reader.afterVerify()
	}
	return reader.verifyErr
}

func newTestPublishedBasisReaderV1() *testPublishedBasisReaderV1 {
	controlInput := controlcontract.ControlSnapshot{
		SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
		SnapshotID:    "control-a",
		TenantID:      testTenantIDV1,
		Revision:      7,
		Agents: []corecontract.AgentRef{
			testAgentRefV1("agent-a", "1"),
		},
		Profiles: []controlcontract.ProfileDefinition{
			{
				Profile:          testProfileRefV1("profile-a", "2"),
				ContextPolicy:    testPolicyRefV1("context-policy", "3"),
				SchedulingPolicy: testPolicyRefV1("schedule-policy", "5"),
				Bindings: []controlcontract.BindingSpec{
					testBindingV1(
						moduleapi.PortNameModelGenerate,
						testProfileInstance,
						"6",
					),
				},
			},
		},
		Workspaces: []controlcontract.WorkspaceDefinition{
			testWorkspaceV1(testWorkspaceAV1, testChannelAInstance, "endpoint-a", "7"),
			testWorkspaceV1(testWorkspaceBV1, testChannelBInstance, "endpoint-b", "8"),
			{
				Workspace: testWorkspaceRefV1(testWorkspaceEmptyV1, "9"),
			},
		},
	}
	control, controlRef, _, err := controlcontract.NewControlSnapshot(controlInput)
	if err != nil {
		panic(err)
	}
	catalogInput := controlcontract.CatalogGeneration{
		SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
		GenerationID:          "catalog-a",
		Generation:            11,
		TenantID:              testTenantIDV1,
		ControlSnapshotID:     control.SnapshotID,
		ControlSnapshotDigest: control.Digest,
		Entries: []controlcontract.CatalogEntry{
			testCatalogEntryV1(
				"fixture.unbound",
				testUnboundInstance,
				moduleapi.PortNameContextProvide,
				"b",
			),
			testCatalogEntryV1(
				"fixture.channel.b",
				testChannelBInstance,
				moduleapi.PortNameChannelTransport,
				"c",
			),
			testCatalogEntryV1(
				"fixture.profile",
				testProfileInstance,
				moduleapi.PortNameModelGenerate,
				"d",
			),
			testCatalogEntryV1(
				"fixture.channel.a",
				testChannelAInstance,
				moduleapi.PortNameChannelTransport,
				"e",
			),
		},
	}
	catalog, catalogRef, _, err := controlcontract.NewCatalogGeneration(catalogInput)
	if err != nil {
		panic(err)
	}
	return &testPublishedBasisReaderV1{
		basis: controlcontract.PublishedBasis{
			TenantID:        testTenantIDV1,
			PointerRevision: 13,
			Control:         controlRef,
			Catalog:         catalogRef,
		},
		control: control,
		catalog: catalog,
	}
}

func newTestPublishedBasisReaderForTenantV1(
	tenantID string,
) *testPublishedBasisReaderV1 {
	reader := newTestPublishedBasisReaderV1()
	controlInput := reader.control
	controlInput.TenantID = tenantID
	control, controlRef, _, err := controlcontract.NewControlSnapshot(controlInput)
	if err != nil {
		panic(err)
	}
	catalogInput := reader.catalog
	catalogInput.TenantID = tenantID
	catalogInput.ControlSnapshotID = control.SnapshotID
	catalogInput.ControlSnapshotDigest = control.Digest
	catalog, catalogRef, _, err := controlcontract.NewCatalogGeneration(catalogInput)
	if err != nil {
		panic(err)
	}
	reader.basis = controlcontract.PublishedBasis{
		TenantID:        tenantID,
		PointerRevision: reader.basis.PointerRevision,
		Control:         controlRef,
		Catalog:         catalogRef,
	}
	reader.control = control
	reader.catalog = catalog
	return reader
}

func newTestAuthorizationV1(
	principalID string,
	allowed ...controlapicontract.ControlScopeV1,
) *testAuthorizationV1 {
	digest := testHashV1("f")
	session, _, _, err := controlapicontract.NewControlSessionV1(
		controlapicontract.ControlSessionV1{
			SchemaVersion: controlapicontract.ControlSessionSchemaVersionV1,
			BootID:        "boot-a",
			SessionID:     "session-a",
			PrincipalID:   principalID,
			Capabilities: []controlapicontract.ControlCapabilityV1{
				controlapicontract.CapabilityObserveV1,
			},
			ScopeSetDigest:        digest,
			AuthorizationRevision: 3,
			IssuedAtUnixMicros:    1_000,
			ExpiresAtUnixMicros:   10_000,
		},
	)
	if err != nil {
		panic(err)
	}
	allowedSet := make(map[controlapicontract.ControlScopeV1]bool, len(allowed))
	for _, scope := range allowed {
		allowedSet[scope] = true
	}
	return &testAuthorizationV1{
		session: session,
		digest:  digest,
		allowed: allowedSet,
	}
}

func testTenantScopeV1() controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      testTenantIDV1,
	}
}

func testWorkspaceScopeV1(workspaceID string) controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeWorkspaceV1,
		TenantID:      testTenantIDV1,
		WorkspaceID:   workspaceID,
	}
}

func testWorkspaceV1(
	workspaceID string,
	instanceID string,
	endpointID string,
	digestCharacter string,
) controlcontract.WorkspaceDefinition {
	return controlcontract.WorkspaceDefinition{
		Workspace: testWorkspaceRefV1(workspaceID, digestCharacter),
		ChannelEndpoints: []controlcontract.ChannelEndpointDefinition{
			{
				SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
				EndpointID:      endpointID,
				Channel:         "loopback-http",
				AccountID:       "account-a",
				ConversationID:  "conversation-" + endpointID,
				TargetAgentID:   "agent-a",
				TargetProfileID: "profile-a",
				CursorScopeKey:  "cursor-" + endpointID,
				Enabled:         false,
				Binding: testBindingV1(
					moduleapi.PortNameChannelTransport,
					instanceID,
					digestCharacter,
				),
			},
		},
	}
}

func testBindingV1(
	portName string,
	instanceID string,
	digestCharacter string,
) controlcontract.BindingSpec {
	version := moduleapi.PortVersionV1
	if portName == moduleapi.PortNameModelGenerate {
		version = moduleapi.PortVersionV2
	}
	return controlcontract.BindingSpec{
		Port: moduleapi.PortRef{
			Name:         portName,
			ExactVersion: version,
		},
		InstanceID:          instanceID,
		ConfigRef:           testHashV1(digestCharacter),
		AuthorityCeilingRef: testHashV1("0"),
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
}

func testCatalogEntryV1(
	moduleID string,
	instanceID string,
	portName string,
	digestCharacter string,
) controlcontract.CatalogEntry {
	version := moduleapi.PortVersionV1
	if portName == moduleapi.PortNameModelGenerate {
		version = moduleapi.PortVersionV2
	}
	return controlcontract.CatalogEntry{
		Activation: moduleapi.ActivatedModuleRef{
			ModuleID:           moduleID,
			Version:            "v1",
			ArtifactDigest:     testHashV1(digestCharacter),
			InstanceID:         instanceID,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "fixture-adapter-" + instanceID,
			ActivationRevision: 1,
		},
		Provides: []moduleapi.PortRef{
			{Name: portName, ExactVersion: version},
		},
	}
}

func testAgentRefV1(id, digestCharacter string) corecontract.AgentRef {
	return corecontract.AgentRef{ID: id, Version: "v1", Digest: testHashV1(digestCharacter)}
}

func testProfileRefV1(id, digestCharacter string) corecontract.ProfileRef {
	return corecontract.ProfileRef{ID: id, Version: "v1", Digest: testHashV1(digestCharacter)}
}

func testWorkspaceRefV1(id, digestCharacter string) corecontract.WorkspaceRef {
	return corecontract.WorkspaceRef{ID: id, Version: "v1", Digest: testHashV1(digestCharacter)}
}

func testPolicyRefV1(id, digestCharacter string) corecontract.PolicyRef {
	return corecontract.PolicyRef{ID: id, Version: "v1", Digest: testHashV1(digestCharacter)}
}

func testHashV1(character string) string {
	return strings.Repeat(strings.ToLower(character), moduleapi.SHA256HexLength)
}
