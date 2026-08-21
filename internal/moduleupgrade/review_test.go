package moduleupgrade

import (
	"bytes"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type reviewFixtureV1 struct {
	review             ReviewV1
	candidateCanonical []byte
	snapshotCanonical  []byte
}

func TestReviewV1CanonicalAndIDCanary(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	_, canonical, reviewID, err := NewReviewV1(
		fixture.review,
		fixture.candidateCanonical,
		fixture.snapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	const wantCanonical = `{"binding_impacts":[{"authority_ceiling_ref":"2222222222222222222222222222222222222222222222222222222222222222","binding_target":{"kind":"PROFILE","profile_id":"profile-1"},"config_ref":"1111111111111111111111111111111111111111111111111111111111111111","current_instance_id":"instance-1","failure_policy":"REQUIRED","port":{"exact_version":"v1","name":"action.provider"},"port_binding_index":0,"static_context_refs":[],"target_instance_id":"instance-2"}],"binding_target":{"kind":"PROFILE","profile_id":"profile-1"},"candidate_id":"c728c643d3eadb85c8153d0df74ab5c72234a5603e9dc1bd17155ab101824156","conclusion":"WOULD_APPLY","current":{"activation":{"activation_revision":1,"adapter_identity":"freeagent.adapter.action.wasm/v1","artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","execution_class":"WASM","instance_id":"instance-1","module_id":"vendor.tool","version":"build-1"},"installation_id":"installation-1","manifest":{"module":{"id":"vendor.tool","version":"build-1"},"provides":[{"exact_version":"v1","name":"action.provider"}],"requested_permissions":[],"requires":[],"runtime":{"entrypoint":"content/action.wasm","mode":"WASM","protocol":"freeagent-action-wasm/v1"}},"manifest_ref":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},"diff":{"permissions_added":[],"permissions_removed":[],"provides_added":[],"provides_removed":[],"requires_added":[],"requires_removed":[],"runtime_changed":false},"handler":{"adapter_identity":"freeagent.adapter.action.wasm/v1","execution_class":"WASM","kind":"WASM_ACTION","status":"SUPPORTED"},"port":{"exact_version":"v1","name":"action.provider"},"port_binding_index":0,"published_basis":{"catalog":{"digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","generation":1,"generation_id":"catalog-1"},"control":{"digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","revision":1,"snapshot_id":"control-1"},"pointer_revision":1,"tenant_id":"tenant-1"},"reason_codes":["OPERATOR_GRANT_REQUIRED"],"required_grants":[{"kind":"WASM_ACTION_ARTIFACT","reference_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"review_key":"e6d367675372b7c8e457fafd492eed0a2696a1e13e53ee680afa9f7a6385357a","schema_version":"module-upgrade-review/v1","supply_basis":{"index_id":"0f8b9a2352fd6a529758d26eaa756c0e1e1c0a7c159ce63fac6d5c722fe088e2","observation_revision":1,"signature_status":"NOT_REQUIRED","snapshot_id":"c9a475947bf0909bb6d648e3827b8da075ad072cb3717b6a89ded8fdc7665873","source_id":"local.review-canary","source_policy_id":"51b22cc2e64b8834e8572865fb4e7069cbf4c0556e42123f412e1af9484106bb","source_policy_revision":1},"target":{"artifact_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifact_size_bytes":4096,"manifest":{"module":{"id":"vendor.tool","version":"build-2"},"provides":[{"exact_version":"v1","name":"action.provider"}],"requested_permissions":[],"requires":[],"runtime":{"entrypoint":"content/action.wasm","mode":"WASM","protocol":"freeagent-action-wasm/v1"}},"manifest_ref":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff","module":{"id":"vendor.tool","version":"build-2"}},"target_instance_id":"instance-2","tenant_id":"tenant-1"}`
	const wantReviewID = "eccde4c6702f8579dba29182e826be0eb1e9737de50694f5dbb8aa01136159ee"
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical = %s\nReviewID = %s", canonical, reviewID)
	}
	if reviewID != wantReviewID {
		t.Fatalf("ReviewID = %s", reviewID)
	}
}

func TestReviewV1RestoreRejectsUnknownNonCanonicalAndWrongID(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	_, canonical, reviewID, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreReviewV1(canonical, reviewID, fixture.candidateCanonical, fixture.snapshotCanonical); err != nil {
		t.Fatalf("restore exact: %v", err)
	}
	unknown := append([]byte(nil), canonical...)
	unknown = append(unknown[:len(unknown)-1], []byte(`,"unknown":true}`)...)
	if _, err := RestoreReviewV1(unknown, reviewID, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
		t.Fatal("unknown field accepted")
	}
	nonCanonical := append([]byte(`{ `), canonical[1:]...)
	if _, err := RestoreReviewV1(nonCanonical, reviewID, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
		t.Fatal("non-canonical review accepted")
	}
	if _, err := RestoreReviewV1(canonical, strings.Repeat("9", moduleapi.SHA256HexLength), fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
		t.Fatal("wrong ReviewID accepted")
	}
}

func TestReviewV1SortsDiffImpactsGrantsAndReasons(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	second := fixture.review.BindingImpacts[0]
	second.BindingTarget.ProfileID = "profile-0"
	second.PortBindingIndex = 2
	fixture.review.BindingImpacts = append(fixture.review.BindingImpacts, second)
	fixture.review.BindingImpacts[0], fixture.review.BindingImpacts[1] = fixture.review.BindingImpacts[1], fixture.review.BindingImpacts[0]
	fixture.review.ReasonCodes = []ReasonCodeV1{ReasonOperatorRequestedRollbackV1, ReasonOperatorGrantRequiredV1}
	fixture.review.RequiredGrants = []RequiredGrantV1{
		{Kind: GrantRemoteEndpointDigestV1, ReferenceDigest: strings.Repeat("7", moduleapi.SHA256HexLength)},
		{Kind: GrantRemoteActionArtifactV1, ReferenceDigest: fixture.review.Target.ArtifactDigest},
	}
	fixture.review.Handler.ExecutionClass = moduleapi.ExecutionRemote
	fixture.review.Handler.Kind = "REMOTE_ACTION_HTTP"
	fixture.review.Handler.AdapterIdentity = "freeagent.adapter.action.remote-http/v1"
	fixture.review.Target.Manifest.Runtime = RuntimeSummaryV1{
		Mode:       moduleapi.RuntimeModeRequestRemote,
		Protocol:   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
		Entrypoint: "content/action-http.json",
	}
	frozen, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.BindingImpacts[0].BindingTarget.ProfileID != "profile-0" ||
		frozen.RequiredGrants[0].Kind != GrantRemoteActionArtifactV1 ||
		frozen.ReasonCodes[0] != ReasonOperatorGrantRequiredV1 {
		t.Fatalf("collections not canonical: %+v %+v %+v", frozen.BindingImpacts, frozen.RequiredGrants, frozen.ReasonCodes)
	}
}

func TestReviewV1AcceptsCompleteRemoteAndModelGrantFamilies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		runtime    RuntimeSummaryV1
		handler    HandlerAssessmentV1
		grantKinds []RequiredGrantKindV1
	}{
		{
			name:       "REMOTE",
			runtime:    RuntimeSummaryV1{Mode: moduleapi.RuntimeModeRequestRemote, Protocol: moduleapi.RuntimeProtocolFreeAgentActionHTTPV1, Entrypoint: "content/action-http.json"},
			handler:    HandlerAssessmentV1{Status: HandlerSupportedV1, Kind: "REMOTE_ACTION_HTTP", ExecutionClass: moduleapi.ExecutionRemote, AdapterIdentity: "freeagent.adapter.action.remote-http/v1"},
			grantKinds: []RequiredGrantKindV1{GrantRemoteCredentialDigestV1, GrantRemoteEndpointDigestV1, GrantRemoteActionArtifactV1},
		},
		{
			name:       "MODEL",
			runtime:    RuntimeSummaryV1{Mode: moduleapi.RuntimeModeRequestTrustedInProcess, Protocol: moduleapi.RuntimeProtocolGoInProcessV1, Entrypoint: "deepseek"},
			handler:    HandlerAssessmentV1{Status: HandlerSupportedV1, Kind: "DEEPSEEK_MODEL", ExecutionClass: moduleapi.ExecutionTrustedInProcess, AdapterIdentity: "freeagent.adapter.model.deepseek/v1"},
			grantKinds: []RequiredGrantKindV1{GrantModelCredentialDigestV1, GrantTrustedInProcessArtifactV1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newReviewFixtureV1(t)
			fixture.review.Handler = testCase.handler
			fixture.review.Target.Manifest.Runtime = testCase.runtime
			fixture.review.RequiredGrants = make([]RequiredGrantV1, len(testCase.grantKinds))
			for index, kind := range testCase.grantKinds {
				digest := strings.Repeat(string(rune('3'+index)), moduleapi.SHA256HexLength)
				switch kind {
				case GrantLocalProcessArtifactV1, GrantTrustedInProcessArtifactV1, GrantRemoteActionArtifactV1, GrantWASMActionArtifactV1:
					digest = fixture.review.Target.ArtifactDigest
				}
				fixture.review.RequiredGrants[index] = RequiredGrantV1{Kind: kind, ReferenceDigest: digest}
			}
			frozen, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical)
			if err != nil {
				t.Fatal(err)
			}
			if len(frozen.RequiredGrants) != len(testCase.grantKinds) {
				t.Fatalf("grants=%+v", frozen.RequiredGrants)
			}
			for index := 1; index < len(frozen.RequiredGrants); index++ {
				if grantKey(frozen.RequiredGrants[index-1]) >= grantKey(frozen.RequiredGrants[index]) {
					t.Fatalf("grants are not strictly sorted: %+v", frozen.RequiredGrants)
				}
			}
		})
	}
}

func TestReviewV1DefensiveCopyAndNoSensitiveBodies(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	frozen, canonical, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	fixture.review.Current.Manifest.Provides[0].Name = "mutated.port"
	fixture.review.BindingImpacts[0].StaticContextRefs = append(fixture.review.BindingImpacts[0].StaticContextRefs, strings.Repeat("8", 64))
	fixture.review.ReasonCodes[0] = ReasonHandlerConflictV1
	canonical[0] = '['
	if frozen.Current.Manifest.Provides[0].Name != moduleapi.PortNameActionProvider ||
		len(frozen.BindingImpacts[0].StaticContextRefs) != 0 ||
		frozen.ReasonCodes[0] != ReasonOperatorGrantRequiredV1 {
		t.Fatal("returned review aliases caller input")
	}
	_, clean, _, err := NewReviewV1(frozen, fixture.candidateCanonical, fixture.snapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte(`package_path`), []byte(`signature_base64`),
		[]byte(`public_key_base64`), []byte(`config":{`), []byte(`authority_ceiling":{`),
		[]byte(`http://`), []byte(`https://`),
	} {
		if bytes.Contains(clean, forbidden) {
			t.Fatalf("review contains forbidden body marker %q", forbidden)
		}
	}
}

func TestReviewV1RejectsInvalidClosureAndDuplicates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReviewV1)
	}{
		{name: "same target instance", mutate: func(value *ReviewV1) {
			value.TargetInstanceID = value.Current.Activation.InstanceID
			value.BindingImpacts[0].TargetInstanceID = value.TargetInstanceID
		}},
		{name: "profile owns workspace", mutate: func(value *ReviewV1) { value.BindingTarget.WorkspaceID = "workspace-1" }},
		{name: "candidate target drift", mutate: func(value *ReviewV1) { value.Target.ArtifactDigest = strings.Repeat("6", 64) }},
		{name: "duplicate impact", mutate: func(value *ReviewV1) { value.BindingImpacts = append(value.BindingImpacts, value.BindingImpacts[0]) }},
		{name: "duplicate logical impact with different refs", mutate: func(value *ReviewV1) {
			duplicate := value.BindingImpacts[0]
			duplicate.ConfigRef = strings.Repeat("9", 64)
			value.BindingImpacts = append(value.BindingImpacts, duplicate)
		}},
		{name: "duplicate grant", mutate: func(value *ReviewV1) { value.RequiredGrants = append(value.RequiredGrants, value.RequiredGrants[0]) }},
		{name: "duplicate reason", mutate: func(value *ReviewV1) { value.ReasonCodes = append(value.ReasonCodes, value.ReasonCodes[0]) }},
		{name: "unsupported with handler identity", mutate: func(value *ReviewV1) {
			value.Handler.Status = HandlerUnsupportedV1
			value.Conclusion = ConclusionUnsupportedV1
			value.ReasonCodes = []ReasonCodeV1{ReasonHandlerUnsupportedV1}
		}},
		{name: "unexplained grant", mutate: func(value *ReviewV1) { value.ReasonCodes = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReviewFixtureV1(t)
			test.mutate(&fixture.review)
			if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
}

func TestReviewV1RequiresDerivedConflictReasonsBidirectionally(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReviewV1)
	}{
		{name: "requirements changed without reason", mutate: func(value *ReviewV1) {
			value.Target.Manifest.Requires = []moduleapi.PortRef{{Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1}}
			value.Conclusion = ConclusionConflictV1
		}},
		{name: "requirements reason without diff", mutate: func(value *ReviewV1) {
			value.Conclusion = ConclusionConflictV1
			value.ReasonCodes = append(value.ReasonCodes, ReasonTargetRequirementsChangedV1)
		}},
		{name: "port reason without removal", mutate: func(value *ReviewV1) {
			value.Conclusion = ConclusionConflictV1
			value.ReasonCodes = append(value.ReasonCodes, ReasonTargetPortRemovedV1)
		}},
		{name: "permission reason without expansion", mutate: func(value *ReviewV1) {
			value.Conclusion = ConclusionConflictV1
			value.ReasonCodes = append(value.ReasonCodes, ReasonRequestedPermissionsExpandedV1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReviewFixtureV1(t)
			test.mutate(&fixture.review)
			if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
				t.Fatal("inconsistent derived conflict projection accepted")
			}
		})
	}

	fixture := newReviewFixtureV1(t)
	fixture.review.Target.Manifest.Requires = []moduleapi.PortRef{{Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1}}
	fixture.review.Conclusion = ConclusionConflictV1
	fixture.review.ReasonCodes = append(fixture.review.ReasonCodes, ReasonTargetRequirementsChangedV1)
	if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err != nil {
		t.Fatalf("exact requirements conflict projection rejected: %v", err)
	}
}

func TestReviewV1RejectsPermissionExpansionAsWouldApply(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	fixture.review.Target.Manifest.RequestedPermissions = []moduleapi.Permission{"network.client"}
	fixture.review.ReasonCodes = []ReasonCodeV1{ReasonOperatorGrantRequiredV1, ReasonRequestedPermissionsExpandedV1}
	if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
		t.Fatal("permission expansion accepted as WOULD_APPLY")
	}
	fixture.review.Conclusion = ConclusionConflictV1
	if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err != nil {
		t.Fatalf("permission expansion conflict projection: %v", err)
	}
}

func TestReviewV1RejectsWouldApplyWhenAnyFanoutPortIsRemoved(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	modelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}
	fixture.review.Current.Manifest.Provides = append(
		fixture.review.Current.Manifest.Provides,
		modelPort,
	)
	second := fixture.review.BindingImpacts[0]
	second.Port = modelPort
	second.PortBindingIndex = 0
	fixture.review.BindingImpacts = append(fixture.review.BindingImpacts, second)
	if _, _, _, err := NewReviewV1(
		fixture.review,
		fixture.candidateCanonical,
		fixture.snapshotCanonical,
	); err == nil {
		t.Fatal("WOULD_APPLY accepted while a non-selected fan-out Port was removed")
	}
	fixture.review.Conclusion = ConclusionConflictV1
	fixture.review.ReasonCodes = append(
		fixture.review.ReasonCodes,
		ReasonTargetPortRemovedV1,
	)
	if _, _, _, err := NewReviewV1(
		fixture.review,
		fixture.candidateCanonical,
		fixture.snapshotCanonical,
	); err != nil {
		t.Fatalf("fan-out Port removal conflict projection: %v", err)
	}
}

func TestReviewV1RuntimeDiffIncludesArtifactEntrypoint(t *testing.T) {
	fixture := newReviewFixtureV1(t)
	fixture.review.Target.Manifest.Runtime.Entrypoint = "content/action-v2.wasm"
	frozen, _, _, err := NewReviewV1(
		fixture.review,
		fixture.candidateCanonical,
		fixture.snapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.Diff.RuntimeChanged {
		t.Fatal("artifact entrypoint change omitted from runtime diff")
	}
	invalid := fixture.review
	invalid.Target.Manifest.Runtime.Entrypoint = "https://example.invalid/action.wasm"
	if _, _, _, err := NewReviewV1(
		invalid,
		fixture.candidateCanonical,
		fixture.snapshotCanonical,
	); err == nil {
		t.Fatal("endpoint URL accepted as WASM artifact entrypoint")
	}
}

func TestReviewV1RejectsUnsafeRuntimeEntrypointProjectionForEveryOpaqueRuntime(t *testing.T) {
	tests := []struct {
		name       string
		mode       moduleapi.RuntimeModeRequest
		protocol   string
		entrypoint string
	}{
		{name: "unknown remote URL", mode: moduleapi.RuntimeModeRequestRemote, protocol: "vendor-rpc/v9", entrypoint: "https://example.invalid/module"},
		{name: "unknown WASM colon path", mode: moduleapi.RuntimeModeRequestWASM, protocol: "vendor-wasm/v9", entrypoint: "volume:module.wasm"},
		{name: "trusted UNC", mode: moduleapi.RuntimeModeRequestTrustedInProcess, protocol: moduleapi.RuntimeProtocolGoInProcessV1, entrypoint: `\\server\share\adapter`},
		{name: "trusted device", mode: moduleapi.RuntimeModeRequestTrustedInProcess, protocol: moduleapi.RuntimeProtocolGoInProcessV1, entrypoint: `\\.\pipe\adapter`},
		{name: "trusted ADS", mode: moduleapi.RuntimeModeRequestTrustedInProcess, protocol: moduleapi.RuntimeProtocolGoInProcessV1, entrypoint: `adapter:private`},
		{name: "unix absolute", mode: moduleapi.RuntimeModeRequestRemote, protocol: "vendor-rpc/v9", entrypoint: "/private/adapter"},
		{name: "unknown remote dot traversal", mode: moduleapi.RuntimeModeRequestRemote, protocol: "vendor-rpc/v9", entrypoint: "./adapter"},
		{name: "unknown WASM parent traversal", mode: moduleapi.RuntimeModeRequestWASM, protocol: "vendor-wasm/v9", entrypoint: "content/../module.wasm"},
		{name: "trusted backslash alias", mode: moduleapi.RuntimeModeRequestTrustedInProcess, protocol: moduleapi.RuntimeProtocolGoInProcessV1, entrypoint: `vendor\adapter`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReviewFixtureV1(t)
			fixture.review.Target.Manifest.Runtime = RuntimeSummaryV1{
				Mode: test.mode, Protocol: test.protocol, Entrypoint: test.entrypoint,
			}
			if _, _, _, err := NewReviewV1(fixture.review, fixture.candidateCanonical, fixture.snapshotCanonical); err == nil {
				t.Fatal("unsafe runtime entrypoint was accepted into Review")
			}
		})
	}
}

func newReviewFixtureV1(t *testing.T) reviewFixtureV1 {
	t.Helper()
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(moduleapi.ModuleSourceKindLocalDirectoryV1, []byte("review-canary-root"))
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                "local.review-canary",
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		AllowedModuleIDPrefixes: []string{"vendor"},
		MaxIndexBytes:           65536,
		MaxPackageBytes:         8388608,
		MaxCandidates:           16,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetEntry := moduleapi.ModuleDiscoveryEntryV1{
		Module:         moduleapi.Ref{ID: "vendor.tool", Version: "build-2"},
		ArtifactDigest: strings.Repeat("b", 64), ArtifactSizeBytes: 4096,
		PackagePath: "vendor.tool/build-2.zip",
	}
	_, indexCanonical, indexID, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{
		SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      "local.review-canary", Entries: []moduleapi.ModuleDiscoveryEntryV1{targetEntry},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, snapshotCanonical, snapshotID, err := moduleapi.NewModuleDiscoverySnapshotV1(policyCanonical, policyID, indexCanonical, indexID)
	if err != nil {
		t.Fatal(err)
	}
	currentArtifact := moduleapi.ExactModuleArtifactV1{
		Module:         moduleapi.Ref{ID: "vendor.tool", Version: "build-1"},
		ArtifactDigest: strings.Repeat("a", 64),
	}
	candidate, candidateCanonical, candidateID, err := moduleapi.NewModuleUpgradeCandidateV1(
		moduleapi.ModuleUpgradeCandidateV1{
			SchemaVersion: moduleapi.ModuleUpgradeCandidateSchemaVersionV1,
			Change:        moduleapi.ModuleCandidateChangeExactVersionV1,
			Current:       &currentArtifact, Target: targetEntry,
		}, snapshotCanonical, snapshotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	actionPort := moduleapi.PortRef{Name: moduleapi.PortNameActionProvider, ExactVersion: moduleapi.PortVersionV1}
	runtime := RuntimeSummaryV1{
		Mode:       moduleapi.RuntimeModeRequestWASM,
		Protocol:   moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
		Entrypoint: "content/action.wasm",
	}
	currentManifest := ManifestSummaryV1{
		Module: currentArtifact.Module, Runtime: runtime,
		Provides: []moduleapi.PortRef{actionPort}, Requires: []moduleapi.PortRef{}, RequestedPermissions: []moduleapi.Permission{},
	}
	targetManifest := ManifestSummaryV1{
		Module: targetEntry.Module, Runtime: runtime,
		Provides: []moduleapi.PortRef{actionPort}, Requires: []moduleapi.PortRef{}, RequestedPermissions: []moduleapi.Permission{},
	}
	target := BindingTargetV1{Kind: BindingTargetProfileV1, ProfileID: "profile-1"}
	review := ReviewV1{
		SchemaVersion: ReviewSchemaVersionV1,
		CandidateID:   candidateID, ReviewKey: candidate.ReviewKey, TenantID: "tenant-1",
		BindingTarget: target, Port: actionPort, PortBindingIndex: 0, TargetInstanceID: "instance-2",
		SupplyBasis: SupplyBasisV1{
			SourceID: candidate.SourceID, SourcePolicyID: candidate.SourcePolicyID,
			SourcePolicyRevision: 1, SnapshotID: snapshotID, IndexID: indexID,
			ObservationRevision: 1, SignatureStatus: SignatureNotRequiredV1,
		},
		PublishedBasis: controlcontract.PublishedBasis{
			TenantID: "tenant-1", PointerRevision: 1,
			Control: controlcontract.ControlSnapshotRef{SnapshotID: "control-1", Revision: 1, Digest: strings.Repeat("c", 64)},
			Catalog: controlcontract.CatalogGenerationRef{GenerationID: "catalog-1", Generation: 1, Digest: strings.Repeat("d", 64)},
		},
		Current: CurrentExactV1{
			Activation: moduleapi.ActivatedModuleRef{
				ModuleID: currentArtifact.Module.ID, Version: currentArtifact.Module.Version,
				ArtifactDigest: currentArtifact.ArtifactDigest, InstanceID: "instance-1",
				ExecutionClass:  moduleapi.ExecutionWASM,
				AdapterIdentity: "freeagent.adapter.action.wasm/v1", ActivationRevision: 1,
			},
			InstallationID: "installation-1", ManifestRef: strings.Repeat("e", 64), Manifest: currentManifest,
		},
		Target: TargetEvidenceV1{
			Module: targetEntry.Module, ArtifactDigest: targetEntry.ArtifactDigest,
			ArtifactSizeBytes: targetEntry.ArtifactSizeBytes, ManifestRef: strings.Repeat("f", 64), Manifest: targetManifest,
		},
		Handler: HandlerAssessmentV1{
			Status: HandlerSupportedV1, Kind: "WASM_ACTION", ExecutionClass: moduleapi.ExecutionWASM,
			AdapterIdentity: "freeagent.adapter.action.wasm/v1",
		},
		BindingImpacts: []BindingImpactV1{{
			BindingTarget: target, Port: actionPort, PortBindingIndex: 0,
			CurrentInstanceID: "instance-1", TargetInstanceID: "instance-2",
			ConfigRef: strings.Repeat("1", 64), AuthorityCeilingRef: strings.Repeat("2", 64),
			StaticContextRefs: []string{}, FailurePolicy: moduleapi.FailureRequired,
		}},
		RequiredGrants: []RequiredGrantV1{{Kind: GrantWASMActionArtifactV1, ReferenceDigest: targetEntry.ArtifactDigest}},
		Conclusion:     ConclusionWouldApplyV1,
		ReasonCodes:    []ReasonCodeV1{ReasonOperatorGrantRequiredV1},
	}
	return reviewFixtureV1{review: review, candidateCanonical: candidateCanonical, snapshotCanonical: snapshotCanonical}
}
