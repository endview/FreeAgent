package corecontract

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ModelStreamEventSchemaVersionV1 = "model-stream-event/v1"

type ModelStreamEventKindV1 string

const (
	ModelStreamDeltaV1     ModelStreamEventKindV1 = "DELTA"
	ModelStreamUsageV1     ModelStreamEventKindV1 = "USAGE"
	ModelStreamCompletedV1 ModelStreamEventKindV1 = "COMPLETED"
	ModelStreamErrorV1     ModelStreamEventKindV1 = "ERROR"
)

type ModelStreamFinishReasonV1 string

const (
	ModelStreamFinishStopV1      ModelStreamFinishReasonV1 = "STOP"
	ModelStreamFinishLengthV1    ModelStreamFinishReasonV1 = "LENGTH"
	ModelStreamFinishToolV1      ModelStreamFinishReasonV1 = "TOOL"
	ModelStreamFinishCancelledV1 ModelStreamFinishReasonV1 = "CANCELLED"
)

func (reason ModelStreamFinishReasonV1) Validate() error {
	switch reason {
	case ModelStreamFinishStopV1, ModelStreamFinishLengthV1,
		ModelStreamFinishToolV1, ModelStreamFinishCancelledV1:
		return nil
	default:
		return fmt.Errorf("corecontract: invalid model stream finish reason %q", reason)
	}
}

// ModelStreamDeliveryV1 is deliberately separate from the terminal outcome.
// A stream can end without a terminal frame even though the request may have
// reached the provider.
type ModelStreamDeliveryV1 string

const (
	ModelStreamNotSentV1      ModelStreamDeliveryV1 = "NOT_SENT"
	ModelStreamResponseSeenV1 ModelStreamDeliveryV1 = "RESPONSE_SEEN"
	ModelStreamUnknownV1      ModelStreamDeliveryV1 = "UNKNOWN"
)

func (delivery ModelStreamDeliveryV1) Validate() error {
	switch delivery {
	case ModelStreamNotSentV1, ModelStreamResponseSeenV1, ModelStreamUnknownV1:
		return nil
	default:
		return fmt.Errorf("corecontract: invalid model stream delivery %q", delivery)
	}
}

type ModelStreamErrorClassV1 string

const (
	ModelStreamErrorCancelledV1   ModelStreamErrorClassV1 = "CANCELLED"
	ModelStreamErrorProtocolV1    ModelStreamErrorClassV1 = "PROTOCOL"
	ModelStreamErrorProviderV1    ModelStreamErrorClassV1 = "PROVIDER"
	ModelStreamErrorTransportV1   ModelStreamErrorClassV1 = "TRANSPORT"
	ModelStreamErrorUnavailableV1 ModelStreamErrorClassV1 = "UNAVAILABLE"
)

func (class ModelStreamErrorClassV1) Validate() error {
	switch class {
	case ModelStreamErrorCancelledV1, ModelStreamErrorProtocolV1,
		ModelStreamErrorProviderV1, ModelStreamErrorTransportV1,
		ModelStreamErrorUnavailableV1:
		return nil
	default:
		return fmt.Errorf("corecontract: invalid model stream error class %q", class)
	}
}

// ModelStreamEventV1 is the only event shape shared by provider adapters and
// the model consumer. Provider frames, response bodies, credentials, and
// provider-specific IDs do not cross this boundary.
type ModelStreamEventV1 struct {
	SchemaVersion string                    `json:"schema_version"`
	Kind          ModelStreamEventKindV1    `json:"kind"`
	Delta         string                    `json:"delta,omitempty"`
	Usage         *UsageTokens              `json:"usage,omitempty"`
	FinishReason  ModelStreamFinishReasonV1 `json:"finish_reason,omitempty"`
	ErrorClass    ModelStreamErrorClassV1   `json:"error_class,omitempty"`
	Delivery      ModelStreamDeliveryV1     `json:"delivery,omitempty"`
}

func (event ModelStreamEventV1) Validate() error {
	if event.SchemaVersion != ModelStreamEventSchemaVersionV1 {
		return fmt.Errorf("corecontract: model stream event schema version must be %q", ModelStreamEventSchemaVersionV1)
	}
	if !utf8.ValidString(event.Delta) {
		return errors.New("corecontract: model stream delta is not valid UTF-8")
	}
	switch event.Kind {
	case ModelStreamDeltaV1:
		if event.Delta == "" || event.Usage != nil || event.FinishReason != "" || event.ErrorClass != "" || event.Delivery != "" {
			return errors.New("corecontract: invalid model stream delta event")
		}
	case ModelStreamUsageV1:
		if event.Delta != "" || event.Usage == nil || event.FinishReason != "" || event.ErrorClass != "" || event.Delivery != "" {
			return errors.New("corecontract: invalid model stream usage event")
		}
		if err := event.Usage.Validate(); err != nil {
			return fmt.Errorf("corecontract: model stream usage: %w", err)
		}
		if event.Usage.Input == nil && event.Usage.CachedInput == nil && event.Usage.UncachedInput == nil && event.Usage.Output == nil && event.Usage.Reasoning == nil {
			return errors.New("corecontract: model stream usage event contains no known value")
		}
	case ModelStreamCompletedV1:
		if event.ErrorClass != "" || event.Delivery != ModelStreamResponseSeenV1 {
			return errors.New("corecontract: invalid model stream completed event")
		}
		if err := event.FinishReason.Validate(); err != nil {
			return err
		}
		if event.Usage != nil {
			if err := event.Usage.Validate(); err != nil {
				return fmt.Errorf("corecontract: model stream completion usage: %w", err)
			}
			if event.Usage.Input == nil && event.Usage.CachedInput == nil && event.Usage.UncachedInput == nil && event.Usage.Output == nil && event.Usage.Reasoning == nil {
				return errors.New("corecontract: model stream completion usage contains no known value")
			}
		}
	case ModelStreamErrorV1:
		if event.Delta != "" || event.Usage != nil || event.FinishReason != "" {
			return errors.New("corecontract: invalid model stream error event")
		}
		if err := event.ErrorClass.Validate(); err != nil {
			return err
		}
		if err := event.Delivery.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("corecontract: invalid model stream event kind %q", event.Kind)
	}
	return nil
}

type ModelStreamTerminalStateV1 string

const (
	ModelStreamTerminalCompletedV1 ModelStreamTerminalStateV1 = "COMPLETED"
	ModelStreamTerminalActionV1    ModelStreamTerminalStateV1 = "ACTION_REQUIRED"
	ModelStreamTerminalTruncatedV1 ModelStreamTerminalStateV1 = "TRUNCATED"
	ModelStreamTerminalFailedV1    ModelStreamTerminalStateV1 = "FAILED"
	ModelStreamTerminalCancelledV1 ModelStreamTerminalStateV1 = "CANCELLED"
	ModelStreamTerminalUnknownV1   ModelStreamTerminalStateV1 = "UNKNOWN"
)

type ModelStreamResultV1 struct {
	AssistantText string                     `json:"assistant_text"`
	Usage         *UsageTokens               `json:"usage,omitempty"`
	Terminal      ModelStreamTerminalStateV1 `json:"terminal"`
	FinishReason  ModelStreamFinishReasonV1  `json:"finish_reason,omitempty"`
}

var (
	ErrModelStreamClosed = errors.New("corecontract: model stream is already terminal")
	ErrModelStreamLimit  = errors.New("corecontract: model stream text limit exceeded")
)

// ModelStreamAccumulatorV1 is shared by every adapter. It bounds text before
// appending, preserves omitted Usage fields, and never upgrades a partial
// stream to a successful model output.
type ModelStreamAccumulatorV1 struct {
	maximumBytes int
	text         strings.Builder
	usage        *UsageTokens
	usageSeen    bool
	result       ModelStreamResultV1
	closed       bool
}

func NewModelStreamAccumulatorV1(maximumBytes int) (*ModelStreamAccumulatorV1, error) {
	if maximumBytes < 1 || maximumBytes > 64<<20 {
		return nil, errors.New("corecontract: model stream maximum bytes must be between 1 and 67108864")
	}
	return &ModelStreamAccumulatorV1{maximumBytes: maximumBytes}, nil
}

func (accumulator *ModelStreamAccumulatorV1) Accept(event ModelStreamEventV1) error {
	if accumulator == nil {
		return errors.New("corecontract: model stream accumulator is nil")
	}
	if accumulator.closed {
		return ErrModelStreamClosed
	}
	if err := event.Validate(); err != nil {
		return err
	}
	switch event.Kind {
	case ModelStreamDeltaV1:
		if accumulator.text.Len()+len(event.Delta) > accumulator.maximumBytes {
			accumulator.closed = true
			accumulator.result = ModelStreamResultV1{
				AssistantText: accumulator.text.String(),
				Terminal:      ModelStreamTerminalTruncatedV1,
			}
			return ErrModelStreamLimit
		}
		accumulator.text.WriteString(event.Delta)
	case ModelStreamUsageV1:
		if accumulator.usageSeen {
			return errors.New("corecontract: model stream usage was reported more than once")
		}
		usage := event.Usage.Clone()
		accumulator.usage = &usage
		accumulator.usageSeen = true
	case ModelStreamCompletedV1:
		if accumulator.text.Len()+len(event.Delta) > accumulator.maximumBytes {
			accumulator.closed = true
			accumulator.result = ModelStreamResultV1{
				AssistantText: accumulator.text.String(),
				Usage:         cloneUsageTokens(accumulator.usage),
				Terminal:      ModelStreamTerminalTruncatedV1,
			}
			return ErrModelStreamLimit
		}
		if event.Usage != nil && accumulator.usageSeen {
			return errors.New("corecontract: model stream usage was reported more than once")
		}
		accumulator.text.WriteString(event.Delta)
		if event.Usage != nil {
			usage := event.Usage.Clone()
			accumulator.usage = &usage
			accumulator.usageSeen = true
		}
		accumulator.closed = true
		terminal := ModelStreamTerminalCompletedV1
		switch event.FinishReason {
		case ModelStreamFinishLengthV1:
			terminal = ModelStreamTerminalTruncatedV1
		case ModelStreamFinishToolV1:
			terminal = ModelStreamTerminalActionV1
		case ModelStreamFinishCancelledV1:
			terminal = ModelStreamTerminalCancelledV1
		}
		accumulator.result = ModelStreamResultV1{
			AssistantText: accumulator.text.String(),
			Usage:         cloneUsageTokens(accumulator.usage),
			Terminal:      terminal,
			FinishReason:  event.FinishReason,
		}
	case ModelStreamErrorV1:
		accumulator.closed = true
		terminal := ModelStreamTerminalFailedV1
		if event.Delivery == ModelStreamUnknownV1 {
			terminal = ModelStreamTerminalUnknownV1
		}
		if event.Delivery != ModelStreamUnknownV1 && event.ErrorClass == ModelStreamErrorCancelledV1 {
			terminal = ModelStreamTerminalCancelledV1
		}
		accumulator.result = ModelStreamResultV1{
			AssistantText: accumulator.text.String(),
			Usage:         cloneUsageTokens(accumulator.usage),
			Terminal:      terminal,
		}
	}
	return nil
}

// EndOfInput is used when the provider closes without a terminal event. The
// exact call must be reconciled; the text prefix is diagnostic only.
func (accumulator *ModelStreamAccumulatorV1) EndOfInput() ModelStreamResultV1 {
	if accumulator == nil {
		return ModelStreamResultV1{Terminal: ModelStreamTerminalUnknownV1}
	}
	if !accumulator.closed {
		accumulator.closed = true
		accumulator.result = ModelStreamResultV1{
			AssistantText: accumulator.text.String(),
			Usage:         cloneUsageTokens(accumulator.usage),
			Terminal:      ModelStreamTerminalUnknownV1,
		}
	}
	return accumulator.Result()
}

func (accumulator *ModelStreamAccumulatorV1) Result() ModelStreamResultV1 {
	if accumulator == nil {
		return ModelStreamResultV1{Terminal: ModelStreamTerminalUnknownV1}
	}
	result := accumulator.result
	result.Usage = cloneUsageTokens(result.Usage)
	return result
}

func cloneUsageTokens(usage *UsageTokens) *UsageTokens {
	if usage == nil {
		return nil
	}
	clone := usage.Clone()
	return &clone
}
