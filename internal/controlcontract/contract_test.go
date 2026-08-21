package controlcontract

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestControlSnapshotFreezesSetsAndPreservesBindingOrder(t *testing.T) {
	input := validControlSnapshot()
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("control ref validation error = %v", err)
	}
	if got := []string{frozen.Agents[0].ID, frozen.Agents[1].ID}; !reflect.DeepEqual(got, []string{"agent.a", "agent.z"}) {
		t.Fatalf("agent order = %v", got)
	}
	if got := []string{
		frozen.Workspaces[0].Workspace.ID,
		frozen.Workspaces[1].Workspace.ID,
	}; !reflect.DeepEqual(got, []string{"workspace.a", "workspace.z"}) {
		t.Fatalf("workspace order = %v", got)
	}
	if got := []string{
		frozen.Profiles[0].Profile.ID,
		frozen.Profiles[1].Profile.ID,
	}; !reflect.DeepEqual(got, []string{"profile.a", "profile.z"}) {
		t.Fatalf("profile order = %v", got)
	}

	profile, found := frozen.FindProfile("profile.z")
	if !found {
		t.Fatalf("profile.z not found")
	}
	if got := []string{
		profile.Bindings[0].InstanceID,
		profile.Bindings[1].InstanceID,
	}; !reflect.DeepEqual(got, []string{"context-z", "model-a"}) {
		t.Fatalf("binding order changed: %v", got)
	}
	if bytes.Contains(canonical, []byte("catalog_id")) ||
		bytes.Contains(canonical, []byte("catalog_digest")) {
		t.Fatalf("control snapshot contains a reverse Catalog reference: %s", canonical)
	}

	identity := cloneControlSnapshot(frozen)
	identity.Digest = ""
	identityCanonical, err := canonicalJSON(identity)
	if err != nil {
		t.Fatalf("canonicalJSON(identity) error = %v", err)
	}
	wantDigest := moduleapi.Digest(
		controlSnapshotDigestDomain,
		identityCanonical,
	)
	if frozen.Digest != wantDigest || ref.Digest != wantDigest {
		t.Fatalf(
			"control digest frozen/ref/want = %q/%q/%q",
			frozen.Digest,
			ref.Digest,
			wantDigest,
		)
	}

	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil {
		t.Fatalf("RestoreControlSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restored control differs")
	}
}

func TestBindingSpecHasNoProviderIdentity(t *testing.T) {
	kind := reflect.TypeOf(BindingSpec{})
	got := make([]string, kind.NumField())
	for index := range got {
		got[index] = kind.Field(index).Name
	}
	want := []string{
		"Port",
		"InstanceID",
		"ConfigRef",
		"AuthorityCeilingRef",
		"StaticContextRefs",
		"FailurePolicy",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BindingSpec fields = %v, want %v", got, want)
	}
}

func TestControlSnapshotOptionalModelProfileIsCanonicalAndDefensivelyFrozen(
	t *testing.T,
) {
	withoutProfile := validControlSnapshot()
	_, withoutRef, withoutCanonical, err := NewControlSnapshot(withoutProfile)
	if err != nil {
		t.Fatalf("NewControlSnapshot(without model profile) error = %v", err)
	}
	if bytes.Contains(withoutCanonical, []byte(`"model_profile"`)) {
		t.Fatalf("nil model profile changed the control wire: %s", withoutCanonical)
	}
	if _, err := RestoreControlSnapshot(withoutCanonical, withoutRef); err != nil {
		t.Fatalf("restore control without model profile: %v", err)
	}

	modelProfile := &corecontract.ModelProfileRef{
		ID:      "model-profile.chat",
		Version: "1",
		Digest:  hash("d"),
	}
	withProfile := validControlSnapshot()
	withProfile.Profiles[0].ModelProfile = modelProfile
	frozen, ref, canonical, err := NewControlSnapshot(withProfile)
	if err != nil {
		t.Fatalf("NewControlSnapshot(with model profile) error = %v", err)
	}
	if !bytes.Contains(canonical, []byte(`"model_profile":{`)) {
		t.Fatalf("model profile ref is absent from canonical control: %s", canonical)
	}
	modelProfile.Digest = hash("e")
	profile, found := frozen.FindProfile("profile.z")
	if !found || profile.ModelProfile == nil ||
		profile.ModelProfile.Digest != hash("d") {
		t.Fatalf("frozen model profile ref aliases input: %+v", profile.ModelProfile)
	}
	profile.ModelProfile.Digest = hash("f")
	again, found := frozen.FindProfile("profile.z")
	if !found || again.ModelProfile == nil ||
		again.ModelProfile.Digest != hash("d") {
		t.Fatalf("profile lookup exposes the frozen model profile ref")
	}
	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil {
		t.Fatalf("restore control with model profile: %v", err)
	}
	restoredProfile, found := restored.FindProfile("profile.z")
	if !found || restoredProfile.ModelProfile == nil ||
		restoredProfile.ModelProfile.Digest != hash("d") {
		t.Fatalf("restored model profile ref changed: %+v", restoredProfile.ModelProfile)
	}
}

func TestControlSnapshotOptionalChannelDefinitionsAreCanonicalAndOwned(t *testing.T) {
	pure := validControlSnapshot()
	_, _, pureCanonical, err := NewControlSnapshot(pure)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pureCanonical, []byte(`"channel_endpoints"`)) ||
		bytes.Contains(pureCanonical, []byte(`"channel_identities"`)) {
		t.Fatalf("Channel-disabled Control changed the legacy wire: %s", pureCanonical)
	}

	input := validControlSnapshot()
	input.Workspaces[0].ChannelEndpoints = []ChannelEndpointDefinition{
		channelEndpoint("endpoint-z", "conversation-z", "channel-z", true),
		channelEndpoint("endpoint-a", "conversation-a", "channel-a", false),
	}
	input.Workspaces[0].ChannelIdentities = []ChannelIdentityDefinition{
		{
			Channel:        "loopback-http",
			AccountID:      "account-1",
			ExternalUserID: "user-z",
			PrincipalID:    "principal-z",
			ACLEpoch:       2,
			Active:         true,
		},
		{
			Channel:        "loopback-http",
			AccountID:      "account-1",
			ExternalUserID: "user-a",
			PrincipalID:    "principal-a",
			ACLEpoch:       1,
			Active:         true,
		},
	}
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	workspace, found := frozen.FindWorkspace("workspace.z")
	if !found {
		t.Fatal("workspace.z not found")
	}
	if got := []string{
		workspace.ChannelEndpoints[0].EndpointID,
		workspace.ChannelEndpoints[1].EndpointID,
	}; !reflect.DeepEqual(got, []string{"endpoint-a", "endpoint-z"}) {
		t.Fatalf("Channel endpoints are not canonical: %v", got)
	}
	if got := []string{
		workspace.ChannelIdentities[0].ExternalUserID,
		workspace.ChannelIdentities[1].ExternalUserID,
	}; !reflect.DeepEqual(got, []string{"user-a", "user-z"}) {
		t.Fatalf("Channel identities are not canonical: %v", got)
	}
	if !bytes.Contains(canonical, []byte(`"channel_endpoints":[`)) ||
		!bytes.Contains(canonical, []byte(`"channel_identities":[`)) {
		t.Fatalf("Channel definitions are absent from canonical Control: %s", canonical)
	}
	input.Workspaces[0].ChannelEndpoints[0].Binding.ConfigRef = hash("9")
	workspace.ChannelEndpoints[0].Binding.ConfigRef = hash("8")
	again, found := frozen.FindWorkspace("workspace.z")
	if !found || again.ChannelEndpoints[0].Binding.ConfigRef != hash("a") {
		t.Fatal("frozen Channel endpoint aliases input or lookup output")
	}
	if _, err := RestoreControlSnapshot(canonical, ref); err != nil {
		t.Fatal(err)
	}
}

func TestControlSnapshotRejectsAmbiguousOrMisownedChannelDefinitions(t *testing.T) {
	base := validControlSnapshot()
	base.Workspaces[0].ChannelEndpoints = []ChannelEndpointDefinition{
		channelEndpoint("endpoint-1", "conversation-1", "channel-1", true),
	}
	base.Workspaces[0].ChannelIdentities = []ChannelIdentityDefinition{
		channelIdentity("user-1"),
	}
	if _, _, _, err := NewControlSnapshot(base); err != nil {
		t.Fatalf("valid Channel closure failed: %v", err)
	}

	missingTarget := base
	missingTarget.Workspaces = append([]WorkspaceDefinition(nil), base.Workspaces...)
	missingTarget.Workspaces[0] = cloneWorkspaceDefinition(base.Workspaces[0])
	missingTarget.Workspaces[0].ChannelEndpoints[0].TargetAgentID = "agent.absent"
	if _, _, _, err := NewControlSnapshot(missingTarget); err == nil {
		t.Fatal("Channel endpoint with absent target Agent was accepted")
	}

	duplicate := base
	duplicate.Workspaces = append([]WorkspaceDefinition(nil), base.Workspaces...)
	duplicate.Workspaces[1] = cloneWorkspaceDefinition(base.Workspaces[1])
	second := channelEndpoint("endpoint-1", "conversation-2", "channel-2", false)
	second.TargetAgentID = "agent.a"
	second.TargetProfileID = "profile.a"
	duplicate.Workspaces[1].ChannelEndpoints = []ChannelEndpointDefinition{second}
	if _, _, _, err := NewControlSnapshot(duplicate); err == nil {
		t.Fatal("tenant-duplicate Channel endpoint ID was accepted")
	}

	profileOwned := validControlSnapshot()
	profileOwned.Profiles[0].Bindings = append(
		profileOwned.Profiles[0].Bindings,
		channelEndpoint("endpoint-x", "conversation-x", "channel-x", false).Binding,
	)
	if _, _, _, err := NewControlSnapshot(profileOwned); err == nil {
		t.Fatal("Profile-owned channel.transport/v1 binding was accepted")
	}

	badIdentity := base
	badIdentity.Workspaces = append([]WorkspaceDefinition(nil), base.Workspaces...)
	badIdentity.Workspaces[0] = cloneWorkspaceDefinition(base.Workspaces[0])
	badIdentity.Workspaces[0].ChannelIdentities[0].ACLEpoch = 0
	if _, _, _, err := NewControlSnapshot(badIdentity); err == nil {
		t.Fatal("Channel identity with zero ACL epoch was accepted")
	}
}

func TestControlSnapshotRejectsInvalidModelProfileRef(t *testing.T) {
	input := validControlSnapshot()
	input.Profiles[0].ModelProfile = &corecontract.ModelProfileRef{
		ID:      "model-profile.chat",
		Version: "1",
		Digest:  "not-a-digest",
	}
	if _, _, _, err := NewControlSnapshot(input); err == nil {
		t.Fatal("invalid model profile ref was accepted")
	}
}

func TestPublishedBasisRequiresExactPointerIdentity(t *testing.T) {
	basis := PublishedBasis{
		TenantID:        "tenant-a",
		PointerRevision: 4,
		Control: ControlSnapshotRef{
			SnapshotID: "control-4",
			Revision:   4,
			Digest:     hash("a"),
		},
		Catalog: CatalogGenerationRef{
			GenerationID: "catalog-7",
			Generation:   7,
			Digest:       hash("b"),
		},
	}
	if err := basis.Validate(); err != nil {
		t.Fatalf("PublishedBasis.Validate() error = %v", err)
	}
	basis.PointerRevision = 0
	if err := basis.Validate(); err == nil {
		t.Fatal("zero pointer revision was accepted")
	}
}

func TestBindingOrderAllowsSameInstanceWithDifferentConsumerConfig(t *testing.T) {
	input := validControlSnapshot()
	profile := &input.Profiles[0]
	second := profile.Bindings[0]
	second.ConfigRef = hash("9")
	profile.Bindings = append(profile.Bindings, second)

	frozen, _, _, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}
	got, found := frozen.FindProfile(profile.Profile.ID)
	if !found || len(got.Bindings) != len(profile.Bindings) {
		t.Fatalf("same instance with different config was not preserved")
	}

	input = validControlSnapshot()
	input.Profiles[0].Bindings = append(
		input.Profiles[0].Bindings,
		input.Profiles[0].Bindings[0],
	)
	if _, _, _, err := NewControlSnapshot(input); err == nil {
		t.Fatalf("exact duplicate binding was accepted")
	}
}

func TestControlSnapshotLookupsReturnDefensiveCopies(t *testing.T) {
	input := validControlSnapshot()
	input.Profiles[0].Bindings[0].StaticContextRefs = []string{
		hash("0"),
		hash("1"),
	}
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}
	input.Profiles[0].Bindings[0].StaticContextRefs[0] = hash("f")
	profile, found := frozen.FindProfile("profile.z")
	if !found {
		t.Fatalf("profile.z not found")
	}
	if got := profile.Bindings[0].StaticContextRefs; !reflect.DeepEqual(
		got,
		[]string{hash("0"), hash("1")},
	) {
		t.Fatalf("static context order changed or aliased input: %v", got)
	}
	profile.Bindings[0].InstanceID = "mutated"
	profile.Bindings[0].StaticContextRefs[0] = hash("e")
	again, found := frozen.FindProfile("profile.z")
	if !found ||
		again.Bindings[0].InstanceID != "context-z" ||
		again.Bindings[0].StaticContextRefs[0] != hash("0") {
		t.Fatalf("profile lookup exposed backing binding slice")
	}
	if !bytes.Contains(
		canonical,
		[]byte(`"static_context_refs":["`+hash("0")+`","`+hash("1")+`"]`),
	) {
		t.Fatalf("static context refs are not frozen canonically: %s", canonical)
	}
	if _, err := RestoreControlSnapshot(canonical, ref); err != nil {
		t.Fatalf("restore static context refs: %v", err)
	}
	if _, found := frozen.FindAgent("agent.a"); !found {
		t.Fatalf("agent.a not found")
	}
	if _, found := frozen.FindWorkspace("workspace.a"); !found {
		t.Fatalf("workspace.a not found")
	}
	if _, found := frozen.FindProfile("missing"); found {
		t.Fatalf("missing profile reported present")
	}
}

func TestEmptyCollectionsFreezeAsArraysNotNull(t *testing.T) {
	control := validControlSnapshot()
	control.Agents = nil
	control.Workspaces = nil
	control.Profiles = nil
	_, controlRef, controlCanonical, err := NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}
	for _, field := range []string{`"agents":[]`, `"workspaces":[]`, `"profiles":[]`} {
		if !bytes.Contains(controlCanonical, []byte(field)) {
			t.Fatalf("control empty collection is not []: %s", controlCanonical)
		}
	}
	if _, err := RestoreControlSnapshot(controlCanonical, controlRef); err != nil {
		t.Fatalf("RestoreControlSnapshot() error = %v", err)
	}

	catalog := validCatalogGeneration()
	catalog.Entries = nil
	_, catalogRef, catalogCanonical, err := NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	if !bytes.Contains(catalogCanonical, []byte(`"entries":[]`)) {
		t.Fatalf("catalog empty entries is not []: %s", catalogCanonical)
	}
	if _, err := RestoreCatalogGeneration(catalogCanonical, catalogRef); err != nil {
		t.Fatalf("RestoreCatalogGeneration() error = %v", err)
	}

	control = validControlSnapshot()
	control.Profiles[0].Bindings = nil
	_, _, controlCanonical, err = NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot(empty bindings) error = %v", err)
	}
	if !bytes.Contains(controlCanonical, []byte(`"bindings":[]`)) {
		t.Fatalf("profile empty bindings is not []: %s", controlCanonical)
	}
}

func TestControlSnapshotRejectsDuplicateDefinitionsAndBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ControlSnapshot)
	}{
		{
			name: "agent id",
			mutate: func(value *ControlSnapshot) {
				duplicate := value.Agents[0]
				duplicate.Version = "v2"
				duplicate.Digest = hash("1")
				value.Agents = append(value.Agents, duplicate)
			},
		},
		{
			name: "workspace id",
			mutate: func(value *ControlSnapshot) {
				duplicate := value.Workspaces[0]
				duplicate.Workspace.Version = "v2"
				duplicate.Workspace.Digest = hash("2")
				value.Workspaces = append(value.Workspaces, duplicate)
			},
		},
		{
			name: "profile id",
			mutate: func(value *ControlSnapshot) {
				duplicate := cloneProfileDefinition(value.Profiles[0])
				duplicate.Profile.Version = "v2"
				duplicate.Profile.Digest = hash("3")
				value.Profiles = append(value.Profiles, duplicate)
			},
		},
		{
			name: "binding port and instance",
			mutate: func(value *ControlSnapshot) {
				value.Profiles[0].Bindings = append(
					value.Profiles[0].Bindings,
					value.Profiles[0].Bindings[0],
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validControlSnapshot()
			test.mutate(&input)
			if _, _, _, err := NewControlSnapshot(input); err == nil {
				t.Fatalf("NewControlSnapshot() error = nil")
			}
		})
	}
}

func TestControlSnapshotRejectsInvalidBindingInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BindingSpec)
	}{
		{
			name: "unregistered port",
			mutate: func(value *BindingSpec) {
				value.Port = moduleapi.PortRef{
					Name:         "memory.read",
					ExactVersion: "v1",
				}
			},
		},
		{
			name: "bad config ref",
			mutate: func(value *BindingSpec) {
				value.ConfigRef = "config"
			},
		},
		{
			name: "bad authority ref",
			mutate: func(value *BindingSpec) {
				value.AuthorityCeilingRef = "authority"
			},
		},
		{
			name: "bad static context ref",
			mutate: func(value *BindingSpec) {
				value.StaticContextRefs = []string{"context"}
			},
		},
		{
			name: "duplicate static context ref",
			mutate: func(value *BindingSpec) {
				value.StaticContextRefs = []string{hash("0"), hash("0")}
			},
		},
		{
			name: "bad failure policy",
			mutate: func(value *BindingSpec) {
				value.FailurePolicy = "FALLBACK"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validControlSnapshot()
			test.mutate(&input.Profiles[0].Bindings[0])
			if _, _, _, err := NewControlSnapshot(input); err == nil {
				t.Fatalf("NewControlSnapshot() error = nil")
			}
		})
	}

	t.Run("model static context ref", func(t *testing.T) {
		input := validControlSnapshot()
		input.Profiles[0].Bindings[1].StaticContextRefs = []string{hash("0")}
		if _, _, _, err := NewControlSnapshot(input); err == nil {
			t.Fatal("model binding accepted static context refs")
		}
	})
}

func TestRestoreControlSnapshotRejectsUnknownNoncanonicalTamperAndUnsorted(t *testing.T) {
	frozen, ref, canonical, err := NewControlSnapshot(validControlSnapshot())
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}

	if _, err := RestoreControlSnapshot(
		append(bytes.Clone(canonical), ' '),
		ref,
	); err == nil {
		t.Fatalf("noncanonical control accepted")
	}

	unknown := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		value["catalog_id"] = "forbidden"
	})
	if _, err := RestoreControlSnapshot(unknown, ref); err == nil {
		t.Fatalf("unknown Catalog field accepted")
	}

	tampered := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		value["tenant_id"] = "tenant.other"
	})
	if _, err := RestoreControlSnapshot(tampered, ref); err == nil {
		t.Fatalf("tampered control accepted")
	}

	providerIdentity := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		profiles := value["profiles"].([]any)
		bindings := profiles[1].(map[string]any)["bindings"].([]any)
		bindings[0].(map[string]any)["artifact_digest"] = hash("f")
	})
	if _, err := RestoreControlSnapshot(providerIdentity, ref); err == nil {
		t.Fatalf("provider identity inside BindingSpec accepted")
	}

	unsorted := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		agents := value["agents"].([]any)
		agents[0], agents[1] = agents[1], agents[0]
	})
	if _, err := RestoreControlSnapshot(unsorted, ref); err == nil {
		t.Fatalf("noncanonical semantic agent order accepted")
	}

	duplicate := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		agents := value["agents"].([]any)
		value["agents"] = append(agents, agents[0])
	})
	if _, err := RestoreControlSnapshot(duplicate, ref); err == nil {
		t.Fatalf("duplicate agent accepted during restore")
	}

	wrongRef := ref
	wrongRef.Digest = hash("0")
	if _, err := RestoreControlSnapshot(canonical, wrongRef); err == nil {
		t.Fatalf("wrong typed control ref accepted")
	}
	if frozen.Digest != ref.Digest {
		t.Fatalf("fixture digest mismatch")
	}
}

func TestCatalogGenerationSortsEntriesAndProvidedPorts(t *testing.T) {
	input := validCatalogGeneration()
	frozen, ref, canonical, err := NewCatalogGeneration(input)
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("catalog ref validation error = %v", err)
	}
	if got := []string{
		frozen.Entries[0].Activation.InstanceID,
		frozen.Entries[1].Activation.InstanceID,
	}; !reflect.DeepEqual(got, []string{"instance-a", "instance-z"}) {
		t.Fatalf("catalog entry order = %v", got)
	}
	if got := []string{
		frozen.Entries[1].Provides[0].Name,
		frozen.Entries[1].Provides[1].Name,
	}; !reflect.DeepEqual(got, []string{
		moduleapi.PortNameContextProvide,
		moduleapi.PortNameModelGenerate,
	}) {
		t.Fatalf("provided port order = %v", got)
	}
	if frozen.ControlSnapshotID != input.ControlSnapshotID ||
		frozen.ControlSnapshotDigest != input.ControlSnapshotDigest {
		t.Fatalf("control snapshot reference changed")
	}

	identity := cloneCatalogGeneration(frozen)
	identity.Digest = ""
	identityCanonical, err := canonicalJSON(identity)
	if err != nil {
		t.Fatalf("canonicalJSON(identity) error = %v", err)
	}
	wantDigest := moduleapi.Digest(
		runtimeCatalogDigestDomain,
		identityCanonical,
	)
	if frozen.Digest != wantDigest || ref.Digest != wantDigest {
		t.Fatalf("catalog digest mismatch")
	}

	restored, err := RestoreCatalogGeneration(canonical, ref)
	if err != nil {
		t.Fatalf("RestoreCatalogGeneration() error = %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restored catalog differs")
	}
}

func TestCatalogLookupReturnsDefensiveCopy(t *testing.T) {
	frozen, _, _, err := NewCatalogGeneration(validCatalogGeneration())
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	entry, found := frozen.FindInstance("instance-z")
	if !found {
		t.Fatalf("instance-z not found")
	}
	entry.Provides[0].Name = "mutated"
	again, found := frozen.FindInstance("instance-z")
	if !found ||
		again.Provides[0].Name != moduleapi.PortNameContextProvide {
		t.Fatalf("catalog lookup exposed backing Provides slice")
	}
	if _, found := frozen.FindInstance("missing"); found {
		t.Fatalf("missing catalog instance reported present")
	}
}

func TestCatalogGenerationRejectsDuplicatesUnsupportedAndUnregistered(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CatalogGeneration)
	}{
		{
			name: "duplicate instance",
			mutate: func(value *CatalogGeneration) {
				duplicate := cloneCatalogEntry(value.Entries[0])
				duplicate.Activation.ModuleID = "other.module"
				value.Entries = append(value.Entries, duplicate)
			},
		},
		{
			name: "duplicate provided port",
			mutate: func(value *CatalogGeneration) {
				value.Entries[0].Provides = append(
					value.Entries[0].Provides,
					value.Entries[0].Provides[0],
				)
			},
		},
		{
			name: "empty provided ports",
			mutate: func(value *CatalogGeneration) {
				value.Entries[0].Provides = nil
			},
		},
		{
			name: "unregistered port",
			mutate: func(value *CatalogGeneration) {
				value.Entries[0].Provides[0] = moduleapi.PortRef{
					Name:         "memory.read",
					ExactVersion: "v1",
				}
			},
		},
		{
			name: "unknown class",
			mutate: func(value *CatalogGeneration) {
				value.Entries[0].Activation.ExecutionClass = "SANDBOX"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validCatalogGeneration()
			test.mutate(&input)
			if _, _, _, err := NewCatalogGeneration(input); err == nil {
				t.Fatalf("NewCatalogGeneration() error = nil")
			}
		})
	}
}

func TestCatalogGenerationAcceptsLocalProcessActionProvider(t *testing.T) {
	input := validCatalogGeneration()
	input.Entries[0].Activation.ExecutionClass =
		moduleapi.ExecutionLocalProcess
	input.Entries[0].Provides = []moduleapi.PortRef{{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}}
	created, ref, canonical, err := NewCatalogGeneration(input)
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	entry, found := created.FindInstance("instance-z")
	if !found || entry.Activation.ExecutionClass !=
		moduleapi.ExecutionLocalProcess {
		t.Fatalf("catalog entry = %+v, found=%v", entry, found)
	}
	if _, err := RestoreCatalogGeneration(canonical, ref); err != nil {
		t.Fatalf("RestoreCatalogGeneration() error = %v", err)
	}
}

func TestCatalogGenerationAcceptsRemoteActionProvider(t *testing.T) {
	input := validCatalogGeneration()
	input.Entries[0].Activation.ExecutionClass = moduleapi.ExecutionRemote
	input.Entries[0].Provides = []moduleapi.PortRef{{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}}
	created, ref, canonical, err := NewCatalogGeneration(input)
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	entry, found := created.FindInstance("instance-z")
	if !found || entry.Activation.ExecutionClass != moduleapi.ExecutionRemote {
		t.Fatalf("catalog entry = %+v, found=%v", entry, found)
	}
	if _, err := RestoreCatalogGeneration(canonical, ref); err != nil {
		t.Fatalf("RestoreCatalogGeneration() error = %v", err)
	}
}

func TestCatalogGenerationAcceptsWASMActionProvider(t *testing.T) {
	input := validCatalogGeneration()
	input.Entries[0].Activation.ExecutionClass = moduleapi.ExecutionWASM
	input.Entries[0].Provides = []moduleapi.PortRef{{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}}
	created, ref, canonical, err := NewCatalogGeneration(input)
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	entry, found := created.FindInstance("instance-z")
	if !found || entry.Activation.ExecutionClass != moduleapi.ExecutionWASM {
		t.Fatalf("catalog entry = %+v, found=%v", entry, found)
	}
	if _, err := RestoreCatalogGeneration(canonical, ref); err != nil {
		t.Fatalf("RestoreCatalogGeneration() error = %v", err)
	}
}

func TestRestoreCatalogGenerationRejectsUnknownNoncanonicalTamperAndUnsorted(t *testing.T) {
	_, ref, canonical, err := NewCatalogGeneration(validCatalogGeneration())
	if err != nil {
		t.Fatalf("NewCatalogGeneration() error = %v", err)
	}
	if _, err := RestoreCatalogGeneration(
		append(bytes.Clone(canonical), '\n'),
		ref,
	); err == nil {
		t.Fatalf("noncanonical catalog accepted")
	}
	unknown := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		value["provider_search"] = true
	})
	if _, err := RestoreCatalogGeneration(unknown, ref); err == nil {
		t.Fatalf("unknown catalog field accepted")
	}
	tampered := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		value["control_snapshot_digest"] = hash("9")
	})
	if _, err := RestoreCatalogGeneration(tampered, ref); err == nil {
		t.Fatalf("tampered catalog accepted")
	}
	unsorted := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		entries := value["entries"].([]any)
		entries[0], entries[1] = entries[1], entries[0]
	})
	if _, err := RestoreCatalogGeneration(unsorted, ref); err == nil {
		t.Fatalf("noncanonical semantic catalog order accepted")
	}
	duplicate := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		entries := value["entries"].([]any)
		value["entries"] = append(entries, entries[0])
	})
	if _, err := RestoreCatalogGeneration(duplicate, ref); err == nil {
		t.Fatalf("duplicate catalog instance accepted during restore")
	}
}

func validControlSnapshot() ControlSnapshot {
	return ControlSnapshot{
		SchemaVersion: ControlSnapshotSchemaVersionV1,
		SnapshotID:    "control-7",
		TenantID:      "tenant-a",
		Revision:      7,
		Agents: []corecontract.AgentRef{
			{ID: "agent.z", Version: "v1", Digest: hash("a")},
			{ID: "agent.a", Version: "v1", Digest: hash("b")},
		},
		Workspaces: []WorkspaceDefinition{
			{
				Workspace: corecontract.WorkspaceRef{
					ID: "workspace.z", Version: "v1", Digest: hash("c"),
				},
				BudgetPolicy: policy("budget.z", "d"),
			},
			{
				Workspace: corecontract.WorkspaceRef{
					ID: "workspace.a", Version: "v1", Digest: hash("e"),
				},
				BudgetPolicy: policy("budget.a", "f"),
			},
		},
		Profiles: []ProfileDefinition{
			{
				Profile: corecontract.ProfileRef{
					ID: "profile.z", Version: "v1", Digest: hash("1"),
				},
				ContextPolicy:    policy("context.z", "2"),
				CostPolicy:       policy("cost.z", "3"),
				SchedulingPolicy: policy("schedule.z", "4"),
				Bindings: []BindingSpec{
					binding(
						moduleapi.PortNameContextProvide,
						"context-z",
						moduleapi.FailureOptional,
						"5",
					),
					binding(
						moduleapi.PortNameModelGenerate,
						"model-a",
						moduleapi.FailureRequired,
						"6",
					),
				},
			},
			{
				Profile: corecontract.ProfileRef{
					ID: "profile.a", Version: "v1", Digest: hash("7"),
				},
				ContextPolicy:    policy("context.a", "8"),
				CostPolicy:       policy("cost.a", "9"),
				SchedulingPolicy: policy("schedule.a", "a"),
				Bindings: []BindingSpec{
					binding(
						moduleapi.PortNameModelGenerate,
						"model-a",
						moduleapi.FailureRequired,
						"b",
					),
				},
			},
		},
	}
}

func validCatalogGeneration() CatalogGeneration {
	return CatalogGeneration{
		SchemaVersion:         CatalogGenerationSchemaVersionV1,
		GenerationID:          "catalog-3",
		Generation:            3,
		TenantID:              "tenant-a",
		ControlSnapshotID:     "control-7",
		ControlSnapshotDigest: hash("c"),
		Entries: []CatalogEntry{
			{
				Activation: activation(
					"module.z",
					"instance-z",
					moduleapi.ExecutionTrustedInProcess,
					"d",
				),
				Provides: []moduleapi.PortRef{
					{
						Name:         moduleapi.PortNameModelGenerate,
						ExactVersion: moduleapi.PortVersionV1,
					},
					{
						Name:         moduleapi.PortNameContextProvide,
						ExactVersion: moduleapi.PortVersionV1,
					},
				},
			},
			{
				Activation: activation(
					"module.a",
					"instance-a",
					moduleapi.ExecutionDeclarative,
					"e",
				),
				Provides: []moduleapi.PortRef{
					{
						Name:         moduleapi.PortNameContextProvide,
						ExactVersion: moduleapi.PortVersionV1,
					},
				},
			},
		},
	}
}

func policy(id string, digestCharacter string) corecontract.PolicyRef {
	return corecontract.PolicyRef{
		ID: id, Version: "v1", Digest: hash(digestCharacter),
	}
}

func binding(
	portName string,
	instanceID string,
	failure moduleapi.FailurePolicy,
	digestCharacter string,
) BindingSpec {
	return BindingSpec{
		Port: moduleapi.PortRef{
			Name: portName, ExactVersion: moduleapi.PortVersionV1,
		},
		InstanceID:          instanceID,
		ConfigRef:           hash(digestCharacter),
		AuthorityCeilingRef: hash(strings.ToUpper(digestCharacter)),
		FailurePolicy:       failure,
	}
}

func channelEndpoint(
	endpointID string,
	conversationID string,
	instanceID string,
	enabled bool,
) ChannelEndpointDefinition {
	return ChannelEndpointDefinition{
		SchemaVersion:   ChannelEndpointSchemaVersionV1,
		EndpointID:      endpointID,
		Channel:         "loopback-http",
		AccountID:       "account-1",
		ConversationID:  conversationID,
		TargetAgentID:   "agent.z",
		TargetProfileID: "profile.z",
		CursorScopeKey:  "cursor/" + endpointID,
		Enabled:         enabled,
		Binding: binding(
			moduleapi.PortNameChannelTransport,
			instanceID,
			moduleapi.FailureRequired,
			"a",
		),
	}
}

func channelIdentity(externalUserID string) ChannelIdentityDefinition {
	return ChannelIdentityDefinition{
		Channel:        "loopback-http",
		AccountID:      "account-1",
		ExternalUserID: externalUserID,
		PrincipalID:    "principal-1",
		ACLEpoch:       1,
		Active:         true,
	}
}

func activation(
	moduleID string,
	instanceID string,
	class moduleapi.ExecutionClass,
	digestCharacter string,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           moduleID,
		Version:            "v1",
		ArtifactDigest:     hash(digestCharacter),
		InstanceID:         instanceID,
		ExecutionClass:     class,
		AdapterIdentity:    "builtin." + moduleID,
		ActivationRevision: 1,
	}
}

func hash(character string) string {
	return strings.Repeat(strings.ToLower(character), moduleapi.SHA256HexLength)
}

func mutateCanonicalObject(
	t *testing.T,
	canonical []byte,
	mutate func(map[string]any),
) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	mutate(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	return result
}
