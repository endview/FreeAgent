package corecontract

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	TaskInputSchemaVersionV1     = "task-input/v1"
	StaticContextSchemaVersionV1 = "static-context/v1"
)

// TaskInputV1 is the text-only S1 task wire value. Later multimodal versions
// can add another exact schema without changing RunManifest or PortPlan.
type TaskInputV1 struct {
	SchemaVersion string `json:"schema_version"`
	Text          string `json:"text"`
}

// StaticContextV1 is immutable declarative context referenced in semantic
// order by PortBinding.StaticContextRefs. Provider identity, trust and
// authority remain on the binding instead of being duplicated into content.
type StaticContextV1 struct {
	SchemaVersion string `json:"schema_version"`
	Text          string `json:"text"`
}

func NewTaskInputV1(
	input TaskInputV1,
) (TaskInputV1, []byte, error) {
	if input.SchemaVersion != TaskInputSchemaVersionV1 {
		return TaskInputV1{}, nil, fmt.Errorf(
			"corecontract: task input schema version must be %q",
			TaskInputSchemaVersionV1,
		)
	}
	if err := validateChatText("task input", input.Text); err != nil {
		return TaskInputV1{}, nil, err
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return TaskInputV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreTaskInputV1(
	canonical []byte,
) (TaskInputV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return TaskInputV1{}, err
	}
	var decoded TaskInputV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return TaskInputV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewTaskInputV1(decoded)
	if err != nil {
		return TaskInputV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return TaskInputV1{}, fmt.Errorf(
			"corecontract: task input is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func NewStaticContextV1(
	input StaticContextV1,
) (StaticContextV1, []byte, error) {
	if input.SchemaVersion != StaticContextSchemaVersionV1 {
		return StaticContextV1{}, nil, fmt.Errorf(
			"corecontract: static context schema version must be %q",
			StaticContextSchemaVersionV1,
		)
	}
	if err := validateChatText("static context", input.Text); err != nil {
		return StaticContextV1{}, nil, err
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return StaticContextV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreStaticContextV1(
	canonical []byte,
) (StaticContextV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return StaticContextV1{}, err
	}
	var decoded StaticContextV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return StaticContextV1{}, err
	}
	rebuilt, rebuiltCanonical, err := NewStaticContextV1(decoded)
	if err != nil {
		return StaticContextV1{}, err
	}
	if !bytes.Equal(canonical, rebuiltCanonical) {
		return StaticContextV1{}, fmt.Errorf(
			"corecontract: static context is not frozen canonically",
		)
	}
	return rebuilt, nil
}

func validateChatText(kind string, text string) error {
	if text == "" ||
		len(text) > moduleapi.MaxTextBytes ||
		!utf8.ValidString(text) ||
		text != moduleapi.CanonicalText(text) {
		return fmt.Errorf(
			"corecontract: %s text must be non-empty NFC and at most %d bytes",
			kind,
			moduleapi.MaxTextBytes,
		)
	}
	return nil
}
