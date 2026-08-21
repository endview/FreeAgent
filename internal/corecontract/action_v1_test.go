package corecontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func validFrozenActionDefinitionV1() FrozenActionDefinitionV1 {
	return FrozenActionDefinitionV1{
		PublicActionID:   "file.read",
		ProviderActionID: "provider.file_read",
		BindingIndex:     0,
		Description:      "Read one bounded file path.",
		InputSchema: json.RawMessage(
			`{"additionalProperties":false,"properties":{"path":{"maxLength":64,"type":"string"}},"required":["path"],"type":"object"}`,
		),
		EffectClass:    moduleapi.EffectReadOnly,
		MaxResultBytes: 128,
	}
}

func TestFrozenActionDefinitionV1CanonicalDigestAndDefensiveCopy(
	t *testing.T,
) {
	input := validFrozenActionDefinitionV1()
	frozen, canonical, err := NewFrozenActionDefinitionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if !moduleapi.ValidSHA256(frozen.DefinitionDigest) {
		t.Fatalf("DefinitionDigest=%q", frozen.DefinitionDigest)
	}
	input.InputSchema[0] = '['
	if frozen.InputSchema[0] != '{' {
		t.Fatal("frozen Action definition aliases caller schema")
	}
	restored, err := RestoreFrozenActionDefinitionV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DefinitionDigest != frozen.DefinitionDigest {
		t.Fatal("restored DefinitionDigest changed")
	}

	otherBinding := validFrozenActionDefinitionV1()
	otherBinding.BindingIndex = 7
	other, otherCanonical, err := NewFrozenActionDefinitionV1(otherBinding)
	if err != nil {
		t.Fatal(err)
	}
	if other.DefinitionDigest != frozen.DefinitionDigest {
		t.Fatal("BindingIndex incorrectly entered DefinitionDigest")
	}
	if bytes.Equal(otherCanonical, canonical) {
		t.Fatal("BindingIndex did not enter frozen definition wire")
	}

	tampered := validFrozenActionDefinitionV1()
	tampered.DefinitionDigest = strings.Repeat("f", 64)
	if _, _, err := NewFrozenActionDefinitionV1(tampered); err == nil {
		t.Fatal("accepted mismatched DefinitionDigest")
	}
}

func TestActionProposalV1BindsDefinitionAndContentIdentity(t *testing.T) {
	definition, _, err := NewFrozenActionDefinitionV1(
		validFrozenActionDefinitionV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{ "path" : "README.md" }`)
	prepared := json.RawMessage(`{ "absolute_path" : "README.md" }`)
	memberDigest := strings.Repeat("a", 64)
	proposal, canonical, digest, err := NewActionProposalV1(
		memberDigest,
		definition,
		input,
		prepared,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !moduleapi.ValidSHA256(digest) ||
		string(proposal.CanonicalInput) != `{"path":"README.md"}` ||
		string(proposal.PreparedPayload) != `{"absolute_path":"README.md"}` {
		t.Fatalf("proposal=%+v digest=%q", proposal, digest)
	}
	input[0] = '['
	prepared[0] = '['
	if proposal.CanonicalInput[0] != '{' || proposal.PreparedPayload[0] != '{' {
		t.Fatal("Action Proposal aliases caller payload")
	}
	restored, err := RestoreActionProposalV1(
		canonical,
		digest,
		memberDigest,
		definition,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PublicActionID != definition.PublicActionID {
		t.Fatal("restored Action Proposal mapping changed")
	}
	if _, err := RestoreActionProposalV1(
		canonical,
		strings.Repeat("b", 64),
		memberDigest,
		definition,
	); err == nil {
		t.Fatal("accepted wrong Action Proposal ContentDigest")
	}
	if _, _, _, err := NewActionProposalV1(
		memberDigest,
		definition,
		json.RawMessage(`{"unknown":true}`),
		json.RawMessage(`{}`),
	); err == nil {
		t.Fatal("accepted Action Proposal input outside frozen schema")
	}
}

func TestActionResultV1AvailableEnvelopeAndRejectedBoundary(t *testing.T) {
	definition := validFrozenActionDefinitionV1()
	definition.MaxResultBytes = 32
	frozenDefinition, _, err := NewFrozenActionDefinitionV1(definition)
	if err != nil {
		t.Fatal(err)
	}
	available, canonical, digest, err := NewAvailableActionResultV1(
		frozenDefinition,
		json.RawMessage(`{"ok":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !moduleapi.ValidSHA256(digest) {
		t.Fatalf("Action result digest=%q", digest)
	}
	restored, err := RestoreActionResultV1(
		canonical,
		digest,
		frozenDefinition,
	)
	if err != nil {
		t.Fatal(err)
	}
	message, err := BuildUntrustedActionResultEnvelopeV1(
		restored,
		frozenDefinition,
	)
	if err != nil {
		t.Fatal(err)
	}
	if message.Role != moduleapi.ModelRoleUser ||
		!strings.HasPrefix(message.Content, UntrustedActionResultPrefixV1) ||
		strings.Contains(message.Content, "provider.file_read") ||
		strings.Contains(message.Content, "attempt") {
		t.Fatalf("unsafe Action result envelope=%+v", message)
	}
	if err := ValidateUntrustedActionResultEnvelopeV1(
		message,
		available,
		frozenDefinition,
	); err != nil {
		t.Fatal(err)
	}
	message.Content += " "
	if err := ValidateUntrustedActionResultEnvelopeV1(
		message,
		available,
		frozenDefinition,
	); err == nil {
		t.Fatal("accepted drifted Action result envelope")
	}

	if _, _, _, err := NewAvailableActionResultV1(
		frozenDefinition,
		json.RawMessage(`{"ok": true}`),
	); err == nil {
		t.Fatal("accepted non-canonical Action result")
	}
	if _, _, _, err := NewAvailableActionResultV1(
		frozenDefinition,
		json.RawMessage(`"`+strings.Repeat("x", 31)+`"`),
	); err == nil {
		t.Fatal("accepted oversized Action result")
	}

	rejected, rejectedCanonical, rejectedDigest, err :=
		NewRejectedActionResultV1(
			frozenDefinition,
			"RESULT_TOO_LARGE",
		)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rejectedCanonical, []byte(`"result"`)) ||
		!bytes.Contains(rejectedCanonical, []byte(`"error_classification"`)) {
		t.Fatalf("rejected result shape=%s", rejectedCanonical)
	}
	if _, err := RestoreActionResultV1(
		rejectedCanonical,
		rejectedDigest,
		frozenDefinition,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildUntrustedActionResultEnvelopeV1(
		rejected,
		frozenDefinition,
	); err == nil {
		t.Fatal("RESULT_REJECTED entered model context")
	}
}

func TestActionResultReservationV1IncludesNestedMessageEscaping(
	t *testing.T,
) {
	definition := validFrozenActionDefinitionV1()
	definition.MaxResultBytes = 64
	frozen, _, err := NewFrozenActionDefinitionV1(definition)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := NewActionResultReservationV1(
		[]FrozenActionDefinitionV1{frozen},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.MaxEnvelopeBytes == 0 ||
		reservation.EstimatedTokens <= reservation.MaxEnvelopeBytes {
		t.Fatalf("reservation does not include message escaping: %+v", reservation)
	}
	if err := reservation.Validate(); err != nil {
		t.Fatal(err)
	}

	worst, _, _, err := NewAvailableActionResultV1(
		frozen,
		worstCaseCanonicalActionResultV1(frozen.MaxResultBytes),
	)
	if err != nil {
		t.Fatal(err)
	}
	message, err := BuildUntrustedActionResultEnvelopeV1(worst, frozen)
	if err != nil {
		t.Fatal(err)
	}
	estimatorMessage, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.MaxEnvelopeBytes != uint64(len(message.Content)) ||
		reservation.EstimatedTokens != uint64(len(estimatorMessage)+1) {
		t.Fatalf(
			"reservation=%+v envelope=%d messageIncrement=%d",
			reservation,
			len(message.Content),
			len(estimatorMessage)+1,
		)
	}
}
