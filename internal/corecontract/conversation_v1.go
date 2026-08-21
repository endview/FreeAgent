package corecontract

import (
	"fmt"
	"math"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ConversationTurnIntentSchemaVersionV1 = "conversation-turn-intent/v1"
	ConversationTurnRefSchemaVersionV1    = "conversation-turn-ref/v1"
)

// ConversationTurnIntentV1 freezes the exact optimistic head observed by one
// caller before Assembly compilation. It is evidence and idempotency input;
// none of these identities may enter model prompt text.
type ConversationTurnIntentV1 struct {
	SchemaVersion                string `json:"schema_version"`
	ConversationID               string `json:"conversation_id"`
	ExpectedConversationRevision uint64 `json:"expected_conversation_revision"`
	ExpectedHeadRunID            string `json:"expected_head_run_id,omitempty"`
}

func (turn ConversationTurnIntentV1) Validate() error {
	if turn.SchemaVersion != ConversationTurnIntentSchemaVersionV1 {
		return fmt.Errorf(
			"corecontract: conversation turn intent schema version must be %q",
			ConversationTurnIntentSchemaVersionV1,
		)
	}
	if !validOpaque(turn.ConversationID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid conversation ID")
	}
	if turn.ExpectedConversationRevision > math.MaxInt64 {
		return fmt.Errorf("corecontract: conversation revision exceeds SQLite range")
	}
	if turn.ExpectedConversationRevision == 0 {
		if turn.ExpectedHeadRunID != "" {
			return fmt.Errorf(
				"corecontract: first conversation turn cannot name a head Run",
			)
		}
		return nil
	}
	if !validOpaque(turn.ExpectedHeadRunID, maxOpaqueIDBytes) {
		return fmt.Errorf(
			"corecontract: continued conversation turn requires a valid head Run ID",
		)
	}
	return nil
}

// ConversationTurnRefV1 is the immutable RunManifest projection for one
// linear Conversation turn. TurnIndex is one-based; the predecessor is absent
// only on turn one.
type ConversationTurnRefV1 struct {
	SchemaVersion    string `json:"schema_version"`
	ConversationID   string `json:"conversation_id"`
	PrincipalID      string `json:"principal_id"`
	TurnIndex        uint64 `json:"turn_index"`
	PredecessorRunID string `json:"predecessor_run_id,omitempty"`
}

func (turn ConversationTurnRefV1) Validate() error {
	if turn.SchemaVersion != ConversationTurnRefSchemaVersionV1 {
		return fmt.Errorf(
			"corecontract: conversation turn ref schema version must be %q",
			ConversationTurnRefSchemaVersionV1,
		)
	}
	if !validOpaque(turn.ConversationID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid conversation ID")
	}
	if !validOpaque(turn.PrincipalID, maxOpaqueIDBytes) {
		return fmt.Errorf("corecontract: invalid conversation principal ID")
	}
	if turn.TurnIndex == 0 || turn.TurnIndex > math.MaxInt64 {
		return fmt.Errorf(
			"corecontract: conversation turn index must fit a positive SQLite integer",
		)
	}
	if turn.TurnIndex == 1 {
		if turn.PredecessorRunID != "" {
			return fmt.Errorf(
				"corecontract: first conversation turn cannot name a predecessor Run",
			)
		}
		return nil
	}
	if !validOpaque(turn.PredecessorRunID, moduleapi.MaxOpaqueIDBytes) {
		return fmt.Errorf(
			"corecontract: continued conversation turn requires a valid predecessor Run ID",
		)
	}
	return nil
}

func cloneConversationTurnIntentV1(
	turn *ConversationTurnIntentV1,
) *ConversationTurnIntentV1 {
	if turn == nil {
		return nil
	}
	cloned := *turn
	return &cloned
}

func cloneConversationTurnRefV1(
	turn *ConversationTurnRefV1,
) *ConversationTurnRefV1 {
	if turn == nil {
		return nil
	}
	cloned := *turn
	return &cloned
}
