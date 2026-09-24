package corecontract

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestAdmissionIntentV1GoldenAndRestore(t *testing.T) {
	input := validAdmissionIntent()
	frozen, canonical, digest, err := NewAdmissionIntentV1(input)
	if err != nil {
		t.Fatal(err)
	}
	const wantDigest = "be038e3cbeac738df80eac7334fcfaaf91132f98d31afb0cf2423896840e46c3"
	if digest != wantDigest {
		t.Fatalf("admission intent digest=%s want %s", digest, wantDigest)
	}
	if frozen.Deadline.Location() != time.UTC {
		t.Fatalf("deadline location=%s want UTC", frozen.Deadline.Location())
	}
	restored, err := RestoreAdmissionIntentV1(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	if restored.AdmissionKey != input.AdmissionKey {
		t.Fatalf("restored admission key=%q", restored.AdmissionKey)
	}
}

func TestAdmissionIntentV1TreatsRequestedPortsAsSet(t *testing.T) {
	first := validAdmissionIntent()
	second := validAdmissionIntent()
	second.RequestedPorts[0], second.RequestedPorts[1] =
		second.RequestedPorts[1], second.RequestedPorts[0]
	_, firstCanonical, firstDigest, err := NewAdmissionIntentV1(first)
	if err != nil {
		t.Fatal(err)
	}
	_, secondCanonical, secondDigest, err := NewAdmissionIntentV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) ||
		firstDigest != secondDigest {
		t.Fatal("requested port input order changed stable intent identity")
	}

	second = validAdmissionIntent()
	second.RequestedPorts = append(
		second.RequestedPorts,
		second.RequestedPorts[0],
	)
	if _, _, _, err := NewAdmissionIntentV1(second); err == nil {
		t.Fatal("duplicate requested port was accepted")
	}
}

func TestAdmissionIntentV1RejectsUnknownWireAndDigestDrift(t *testing.T) {
	_, canonical, digest, err := NewAdmissionIntentV1(validAdmissionIntent())
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(
		canonical,
		[]byte(`"schema_version"`),
		[]byte(`"generated_run_id":"run-should-not-exist","schema_version"`),
		1,
	)
	if _, err := RestoreAdmissionIntentV1(unknown, digest); err == nil {
		t.Fatal("unknown generated identity field was accepted")
	}
	if _, err := RestoreAdmissionIntentV1(
		canonical,
		strings.Repeat("f", 64),
	); err == nil {
		t.Fatal("digest drift was accepted")
	}
}

func TestAdmissionIntentV1OptionalChannelEndpointPreservesPureChatWire(t *testing.T) {
	pure := validAdmissionIntent()
	_, pureCanonical, pureDigest, err := NewAdmissionIntentV1(pure)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pureCanonical, []byte(`"channel_endpoint_id"`)) {
		t.Fatalf("Pure Chat intent contains Channel state: %s", pureCanonical)
	}
	channel := validAdmissionIntent()
	channel.ChannelEndpointID = "endpoint-loopback"
	frozen, channelCanonical, channelDigest, err := NewAdmissionIntentV1(channel)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ChannelEndpointID != channel.ChannelEndpointID ||
		!bytes.Contains(channelCanonical, []byte(`"channel_endpoint_id":"endpoint-loopback"`)) ||
		channelDigest == pureDigest {
		t.Fatalf("Channel endpoint was not frozen into intent identity: %s", channelCanonical)
	}
	channel.ChannelEndpointID = " bad "
	if _, _, _, err := NewAdmissionIntentV1(channel); err == nil {
		t.Fatal("invalid Channel endpoint ID was accepted")
	}
}

func validAdmissionIntent() AdmissionIntentV1 {
	return AdmissionIntentV1{
		SchemaVersion: AdmissionIntentSchemaVersionV1,
		TenantID:      "tenant-1",
		AdmissionKey:  "admission-1",
		PrincipalID:   "principal-1",
		WorkspaceID:   "workspace.main",
		AgentID:       "agent.chat",
		ProfileID:     "profile.chat",
		TaskInputRef:  strings.Repeat("a", 64),
		RequestedPorts: []moduleapi.PortRef{
			{
				Name:         moduleapi.PortNameModelGenerate,
				ExactVersion: moduleapi.PortVersionV2,
			},
			{Name: "knowledge.retrieve", ExactVersion: "v1"},
		},
		Deadline: time.Date(
			2026,
			time.July,
			30,
			18,
			30,
			0,
			0,
			time.FixedZone("CST", 8*60*60),
		),
		CancellationScope: "run",
		ExplicitLimits:    []byte(`{"max_steps":4}`),
	}
}
