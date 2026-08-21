package corecontract

import (
	"bytes"
	"testing"
)

func TestConversationTurnIntentFreezesExpectedHeadInAdmissionIdentity(
	t *testing.T,
) {
	input := validAdmissionIntent()
	turn := ConversationTurnIntentV1{
		SchemaVersion:                ConversationTurnIntentSchemaVersionV1,
		ConversationID:               "conversation-1",
		ExpectedConversationRevision: 1,
		ExpectedHeadRunID:            "run-previous",
	}
	input.ConversationTurn = &turn
	frozen, canonical, digest, err := NewAdmissionIntentV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ConversationTurn == nil ||
		frozen.ConversationTurn.ExpectedHeadRunID != "run-previous" ||
		!bytes.Contains(canonical, []byte(`"conversation_turn":{`)) {
		t.Fatalf("Conversation intent was not frozen: %+v %s", frozen, canonical)
	}
	turn.ExpectedHeadRunID = "mutated"
	if frozen.ConversationTurn.ExpectedHeadRunID != "run-previous" {
		t.Fatal("frozen AdmissionIntent aliases caller ConversationTurn")
	}
	restored, err := RestoreAdmissionIntentV1(canonical, digest)
	if err != nil || restored.ConversationTurn == nil ||
		restored.ConversationTurn.ConversationID != "conversation-1" {
		t.Fatalf("restore Conversation AdmissionIntent = %+v, %v", restored, err)
	}
}

func TestConversationTurnContractsRejectNonLinearShapes(t *testing.T) {
	invalidIntent := ConversationTurnIntentV1{
		SchemaVersion:                ConversationTurnIntentSchemaVersionV1,
		ConversationID:               "conversation-1",
		ExpectedConversationRevision: 0,
		ExpectedHeadRunID:            "unexpected-head",
	}
	if err := invalidIntent.Validate(); err == nil {
		t.Fatal("first-turn intent with a head Run was accepted")
	}
	invalidRef := ConversationTurnRefV1{
		SchemaVersion:    ConversationTurnRefSchemaVersionV1,
		ConversationID:   "conversation-1",
		PrincipalID:      "principal-1",
		TurnIndex:        2,
		PredecessorRunID: "",
	}
	if err := invalidRef.Validate(); err == nil {
		t.Fatal("continued turn without predecessor was accepted")
	}

	channel := validAdmissionIntent()
	channel.ChannelEndpointID = "channel-1"
	channel.ConversationTurn = &ConversationTurnIntentV1{
		SchemaVersion:  ConversationTurnIntentSchemaVersionV1,
		ConversationID: "conversation-1",
	}
	if _, _, _, err := NewAdmissionIntentV1(channel); err == nil {
		t.Fatal("Channel and Conversation ingress were accepted together")
	}
}

func TestRunManifestFreezesConversationTurnWithoutChangingNilWire(t *testing.T) {
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	baseline, baselineCanonical, err := NewRunManifest(validRunManifestInput(member))
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ConversationTurn != nil ||
		bytes.Contains(baselineCanonical, []byte(`"conversation_turn"`)) {
		t.Fatalf("nil ConversationTurn entered baseline wire: %s", baselineCanonical)
	}

	input := validRunManifestInput(member)
	turn := ConversationTurnRefV1{
		SchemaVersion:    ConversationTurnRefSchemaVersionV1,
		ConversationID:   "conversation-1",
		PrincipalID:      "principal-1",
		TurnIndex:        2,
		PredecessorRunID: "run-previous",
	}
	input.ConversationTurn = &turn
	frozen, canonical, err := NewRunManifest(input)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ConversationTurn == nil ||
		frozen.ConversationTurn.TurnIndex != 2 ||
		frozen.ManifestDigest == baseline.ManifestDigest ||
		!bytes.Contains(canonical, []byte(`"conversation_turn":{`)) {
		t.Fatalf("ConversationTurn was not identity-bearing: %+v %s", frozen, canonical)
	}
	turn.PredecessorRunID = "mutated"
	if frozen.ConversationTurn.PredecessorRunID != "run-previous" {
		t.Fatal("frozen RunManifest aliases caller ConversationTurn")
	}
	restored, err := RestoreRunManifest(canonical)
	if err != nil || restored.ConversationTurn == nil ||
		restored.ConversationTurn.PredecessorRunID != "run-previous" {
		t.Fatalf("restore Conversation RunManifest = %+v, %v", restored, err)
	}

	self := validRunManifestInput(member)
	self.ConversationTurn = &ConversationTurnRefV1{
		SchemaVersion:    ConversationTurnRefSchemaVersionV1,
		ConversationID:   "conversation-1",
		PrincipalID:      "principal-1",
		TurnIndex:        2,
		PredecessorRunID: self.RunID,
	}
	if _, _, err := NewRunManifest(self); err == nil {
		t.Fatal("self-predecessor Conversation Run was accepted")
	}
}
