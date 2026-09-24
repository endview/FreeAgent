package corecontract

import (
	"errors"
	"strings"
	"testing"
)

func TestModelStreamAccumulatorRequiresTerminalEvent(t *testing.T) {
	accumulator, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamDeltaV1,
		Delta:         "partial",
	}); err != nil {
		t.Fatal(err)
	}
	result := accumulator.EndOfInput()
	if result.Terminal != ModelStreamTerminalUnknownV1 || result.AssistantText != "partial" {
		t.Fatalf("incomplete stream became a result: %+v", result)
	}
	if err := accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamDeltaV1,
		Delta:         "late",
	}); !errors.Is(err, ErrModelStreamClosed) {
		t.Fatalf("late fragment error = %v", err)
	}
}

func TestModelStreamAccumulatorPreservesUsageAndLengthTerminal(t *testing.T) {
	input, output := uint64(3), uint64(4)
	accumulator, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	base := ModelStreamEventV1{SchemaVersion: ModelStreamEventSchemaVersionV1}
	base.Kind, base.Delta = ModelStreamDeltaV1, "hello"
	if err := accumulator.Accept(base); err != nil {
		t.Fatal(err)
	}
	usage := UsageTokens{Input: &input, Output: &output}
	if err := accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamCompletedV1,
		Delta:         "!",
		Usage:         &usage,
		FinishReason:  ModelStreamFinishLengthV1,
		Delivery:      ModelStreamResponseSeenV1,
	}); err != nil {
		t.Fatal(err)
	}
	result := accumulator.Result()
	if result.Terminal != ModelStreamTerminalTruncatedV1 || result.FinishReason != ModelStreamFinishLengthV1 || result.AssistantText != "hello!" {
		t.Fatalf("unexpected length result: %+v", result)
	}
	if result.Usage == nil || result.Usage.Input == nil || *result.Usage.Input != input {
		t.Fatalf("usage was not preserved: %+v", result.Usage)
	}
	if result.Usage.Output == nil || *result.Usage.Output != output {
		t.Fatalf("output usage was not preserved: %+v", result.Usage)
	}
}

func TestModelStreamAccumulatorBoundsTextAndDoesNotSucceed(t *testing.T) {
	accumulator, err := NewModelStreamAccumulatorV1(4)
	if err != nil {
		t.Fatal(err)
	}
	err = accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamDeltaV1,
		Delta:         "12345",
	})
	if !errors.Is(err, ErrModelStreamLimit) {
		t.Fatalf("limit error = %v", err)
	}
	if result := accumulator.Result(); result.Terminal != ModelStreamTerminalTruncatedV1 {
		t.Fatalf("oversized stream terminal = %q", result.Terminal)
	}
}

func TestModelStreamErrorSeparatesUnknownFromExplicitFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		delivery ModelStreamDeliveryV1
		want     ModelStreamTerminalStateV1
	}{
		{name: "unknown after dispatch", delivery: ModelStreamUnknownV1, want: ModelStreamTerminalUnknownV1},
		{name: "response seen failure", delivery: ModelStreamResponseSeenV1, want: ModelStreamTerminalFailedV1},
		{name: "not sent failure", delivery: ModelStreamNotSentV1, want: ModelStreamTerminalFailedV1},
	} {
		t.Run(test.name, func(t *testing.T) {
			accumulator, err := NewModelStreamAccumulatorV1(64)
			if err != nil {
				t.Fatal(err)
			}
			err = accumulator.Accept(ModelStreamEventV1{
				SchemaVersion: ModelStreamEventSchemaVersionV1,
				Kind:          ModelStreamErrorV1,
				ErrorClass:    ModelStreamErrorTransportV1,
				Delivery:      test.delivery,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result := accumulator.Result(); result.Terminal != test.want {
				t.Fatalf("terminal = %q, want %q", result.Terminal, test.want)
			}
		})
	}
}

func TestModelStreamToolAndAmbiguousCancellationAreNotTextSuccess(t *testing.T) {
	tool, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := tool.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamCompletedV1,
		FinishReason:  ModelStreamFinishToolV1,
		Delivery:      ModelStreamResponseSeenV1,
	}); err != nil {
		t.Fatal(err)
	}
	if result := tool.Result(); result.Terminal != ModelStreamTerminalActionV1 {
		t.Fatalf("tool terminal = %q", result.Terminal)
	}

	cancelled, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := cancelled.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamErrorV1,
		ErrorClass:    ModelStreamErrorCancelledV1,
		Delivery:      ModelStreamUnknownV1,
	}); err != nil {
		t.Fatal(err)
	}
	if result := cancelled.Result(); result.Terminal != ModelStreamTerminalUnknownV1 {
		t.Fatalf("ambiguous cancellation terminal = %q", result.Terminal)
	}
}

func TestModelStreamRejectsDuplicateUsage(t *testing.T) {
	input := uint64(1)
	usage := UsageTokens{Input: &input}
	accumulator, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		err = accumulator.Accept(ModelStreamEventV1{
			SchemaVersion: ModelStreamEventSchemaVersionV1,
			Kind:          ModelStreamUsageV1,
			Usage:         &usage,
		})
		if index == 0 && err != nil {
			t.Fatal(err)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate usage error = %v", err)
	}
}

func TestModelStreamAccumulatorRejectsInvalidAndLateEvents(t *testing.T) {
	accumulator, err := NewModelStreamAccumulatorV1(64)
	if err != nil {
		t.Fatal(err)
	}
	invalid := ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamDeltaV1,
		Delta:         string([]byte{0xff}),
	}
	if err := accumulator.Accept(invalid); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid event error = %v", err)
	}
	if err := accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamCompletedV1,
		FinishReason:  ModelStreamFinishStopV1,
		Delivery:      ModelStreamResponseSeenV1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Accept(ModelStreamEventV1{
		SchemaVersion: ModelStreamEventSchemaVersionV1,
		Kind:          ModelStreamCompletedV1,
		FinishReason:  ModelStreamFinishStopV1,
		Delivery:      ModelStreamResponseSeenV1,
	}); !errors.Is(err, ErrModelStreamClosed) {
		t.Fatalf("duplicate terminal error = %v", err)
	}
}
