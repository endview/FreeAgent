package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxActionSchemaDepthV1 counts semantic schema nodes. The root object is
	// depth one; JSON containers used to express properties do not consume an
	// additional semantic level.
	MaxActionSchemaDepthV1      = 8
	MaxActionSchemaPropertiesV1 = 64
	MaxActionSchemaArrayItemsV1 = 64
	MaxActionSchemaNodesV1      = 1024

	maxActionInputNodesV1 = 64 << 10
)

type actionSchemaNodeV1 struct {
	typeName string

	properties map[string]*actionSchemaNodeV1
	required   map[string]struct{}
	items      *actionSchemaNodeV1

	minLength *int
	maxLength *int
	minItems  *int
	maxItems  *int
	minimum   *float64
	maximum   *float64
	enum      []any
}

// CanonicalizeActionInputSchemaV1 accepts the deliberately small
// action.provider/v1 JSON Schema subset and returns owned RFC 8785 bytes.
// It is not a general JSON Schema compiler: unknown keywords, pattern,
// references and combinators are rejected.
func CanonicalizeActionInputSchemaV1(
	input json.RawMessage,
) (json.RawMessage, error) {
	canonical, node, err := compileActionInputSchemaV1(input, false)
	if err != nil {
		return nil, err
	}
	if node.typeName != "object" {
		return nil, fmt.Errorf("action input schema root type must be object")
	}
	return bytes.Clone(canonical), nil
}

// ValidateActionInputSchemaV1 accepts only the exact canonical form returned
// by CanonicalizeActionInputSchemaV1.
func ValidateActionInputSchemaV1(canonical json.RawMessage) error {
	_, node, err := compileActionInputSchemaV1(canonical, true)
	if err != nil {
		return err
	}
	if node.typeName != "object" {
		return fmt.Errorf("action input schema root type must be object")
	}
	return nil
}

// CanonicalizeAndValidateActionInputV1 canonicalizes an input instance and
// validates it against one canonical action.provider/v1 input schema.
func CanonicalizeAndValidateActionInputV1(
	schema json.RawMessage,
	input json.RawMessage,
) (json.RawMessage, error) {
	_, root, err := compileActionInputSchemaV1(schema, true)
	if err != nil {
		return nil, err
	}
	if root.typeName != "object" {
		return nil, fmt.Errorf("action input schema root type must be object")
	}
	canonical, value, err := canonicalActionInstanceV1(input, false)
	if err != nil {
		return nil, err
	}
	if err := validateActionInstanceNodeV1(root, value, "$", true); err != nil {
		return nil, err
	}
	return bytes.Clone(canonical), nil
}

// ValidateActionInputV1 validates an already canonical input instance. It
// rejects an input whose bytes would change under RFC 8785 canonicalization.
func ValidateActionInputV1(
	schema json.RawMessage,
	canonicalInput json.RawMessage,
) error {
	_, root, err := compileActionInputSchemaV1(schema, true)
	if err != nil {
		return err
	}
	if root.typeName != "object" {
		return fmt.Errorf("action input schema root type must be object")
	}
	_, value, err := canonicalActionInstanceV1(canonicalInput, true)
	if err != nil {
		return err
	}
	return validateActionInstanceNodeV1(root, value, "$", true)
}

func compileActionInputSchemaV1(
	input json.RawMessage,
	requireCanonical bool,
) ([]byte, *actionSchemaNodeV1, error) {
	if len(input) == 0 {
		return nil, nil, fmt.Errorf("action input schema must not be empty")
	}
	canonical, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: MaxActionSchemaBytesV1,
			MaxDepth: 32,
			MaxNodes: MaxActionSchemaNodesV1,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("action input schema: %w", err)
	}
	if len(canonical) > MaxActionSchemaBytesV1 {
		return nil, nil, fmt.Errorf(
			"action input schema exceeds %d canonical bytes",
			MaxActionSchemaBytesV1,
		)
	}
	if requireCanonical && !bytes.Equal(canonical, input) {
		return nil, nil, fmt.Errorf("action input schema is not canonical JSON")
	}
	value, err := decodeActionJSONValueV1(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("decode action input schema: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("action input schema must be a JSON object")
	}
	nodes := 0
	node, err := parseActionSchemaNodeV1(object, 1, &nodes, "$")
	if err != nil {
		return nil, nil, err
	}
	return bytes.Clone(canonical), node, nil
}

func parseActionSchemaNodeV1(
	object map[string]any,
	depth int,
	nodes *int,
	path string,
) (*actionSchemaNodeV1, error) {
	if depth > MaxActionSchemaDepthV1 {
		return nil, fmt.Errorf(
			"action input schema %s exceeds semantic depth %d",
			path,
			MaxActionSchemaDepthV1,
		)
	}
	*nodes++
	if *nodes > MaxActionSchemaNodesV1 {
		return nil, fmt.Errorf(
			"action input schema exceeds %d semantic nodes",
			MaxActionSchemaNodesV1,
		)
	}
	typeName, ok := object["type"].(string)
	if !ok {
		return nil, fmt.Errorf("action input schema %s must have string type", path)
	}
	allowed := map[string]struct{}{
		"type": {}, "title": {}, "description": {}, "enum": {},
	}
	switch typeName {
	case "object":
		allowed["properties"] = struct{}{}
		allowed["required"] = struct{}{}
		allowed["additionalProperties"] = struct{}{}
	case "array":
		allowed["items"] = struct{}{}
		allowed["minItems"] = struct{}{}
		allowed["maxItems"] = struct{}{}
	case "string":
		allowed["minLength"] = struct{}{}
		allowed["maxLength"] = struct{}{}
	case "integer", "number":
		allowed["minimum"] = struct{}{}
		allowed["maximum"] = struct{}{}
	case "boolean":
	default:
		return nil, fmt.Errorf(
			"action input schema %s has unsupported type %q",
			path,
			typeName,
		)
	}
	for keyword := range object {
		if _, supported := allowed[keyword]; !supported {
			return nil, fmt.Errorf(
				"action input schema %s has unsupported keyword %q",
				path,
				keyword,
			)
		}
	}
	for _, keyword := range []string{"title", "description"} {
		if raw, present := object[keyword]; present {
			text, valid := raw.(string)
			if !valid {
				return nil, fmt.Errorf(
					"action input schema %s %s must be a string",
					path,
					keyword,
				)
			}
			if err := validateActionSchemaTextV1(
				"action input schema "+path+" "+keyword,
				text,
				MaxActionDescriptionBytesV1,
				true,
			); err != nil {
				return nil, err
			}
		}
	}

	node := &actionSchemaNodeV1{typeName: typeName}
	switch typeName {
	case "object":
		additional, present := object["additionalProperties"]
		allowedAdditional, isBool := additional.(bool)
		if !present || !isBool || allowedAdditional {
			return nil, fmt.Errorf(
				"action input schema %s object must set additionalProperties=false",
				path,
			)
		}
		properties := map[string]any{}
		if raw, present := object["properties"]; present {
			var valid bool
			properties, valid = raw.(map[string]any)
			if !valid {
				return nil, fmt.Errorf(
					"action input schema %s properties must be an object",
					path,
				)
			}
		}
		if len(properties) > MaxActionSchemaPropertiesV1 {
			return nil, fmt.Errorf(
				"action input schema %s has more than %d properties",
				path,
				MaxActionSchemaPropertiesV1,
			)
		}
		node.properties = make(map[string]*actionSchemaNodeV1, len(properties))
		propertyNames := make([]string, 0, len(properties))
		for name := range properties {
			if err := validateActionPropertyNameV1(name); err != nil {
				return nil, fmt.Errorf("action input schema %s: %w", path, err)
			}
			propertyNames = append(propertyNames, name)
		}
		sort.Strings(propertyNames)
		for _, name := range propertyNames {
			childObject, valid := properties[name].(map[string]any)
			if !valid {
				return nil, fmt.Errorf(
					"action input schema %s property %q must be a schema object",
					path,
					name,
				)
			}
			child, childErr := parseActionSchemaNodeV1(
				childObject,
				depth+1,
				nodes,
				path+"."+name,
			)
			if childErr != nil {
				return nil, childErr
			}
			node.properties[name] = child
		}
		node.required = make(map[string]struct{})
		if raw, present := object["required"]; present {
			required, valid := raw.([]any)
			if !valid {
				return nil, fmt.Errorf(
					"action input schema %s required must be an array",
					path,
				)
			}
			if len(required) > len(properties) {
				return nil, fmt.Errorf(
					"action input schema %s required exceeds properties",
					path,
				)
			}
			previous := ""
			for index, item := range required {
				name, valid := item.(string)
				if !valid {
					return nil, fmt.Errorf(
						"action input schema %s required[%d] must be a string",
						path,
						index,
					)
				}
				if _, exists := properties[name]; !exists {
					return nil, fmt.Errorf(
						"action input schema %s required property %q is not declared",
						path,
						name,
					)
				}
				if index > 0 && name <= previous {
					return nil, fmt.Errorf(
						"action input schema %s required must be sorted and unique",
						path,
					)
				}
				previous = name
				node.required[name] = struct{}{}
			}
		}
	case "array":
		items, present := object["items"]
		itemObject, valid := items.(map[string]any)
		if !present || !valid {
			return nil, fmt.Errorf(
				"action input schema %s array must have one items schema",
				path,
			)
		}
		child, childErr := parseActionSchemaNodeV1(
			itemObject,
			depth+1,
			nodes,
			path+"[]",
		)
		if childErr != nil {
			return nil, childErr
		}
		node.items = child
		minItems, err := optionalActionSchemaIntegerV1(
			object,
			"minItems",
			MaxActionSchemaArrayItemsV1,
			path,
		)
		if err != nil {
			return nil, err
		}
		maxItems, err := optionalActionSchemaIntegerV1(
			object,
			"maxItems",
			MaxActionSchemaArrayItemsV1,
			path,
		)
		if err != nil {
			return nil, err
		}
		if maxItems == nil {
			return nil, fmt.Errorf(
				"action input schema %s array must have maxItems",
				path,
			)
		}
		if minItems != nil && *minItems > *maxItems {
			return nil, fmt.Errorf(
				"action input schema %s minItems exceeds maxItems",
				path,
			)
		}
		node.minItems, node.maxItems = minItems, maxItems
	case "string":
		minLength, err := optionalActionSchemaIntegerV1(
			object,
			"minLength",
			MaxTextBytes,
			path,
		)
		if err != nil {
			return nil, err
		}
		maxLength, err := optionalActionSchemaIntegerV1(
			object,
			"maxLength",
			MaxTextBytes,
			path,
		)
		if err != nil {
			return nil, err
		}
		if maxLength == nil {
			return nil, fmt.Errorf(
				"action input schema %s string must have maxLength",
				path,
			)
		}
		if minLength != nil && *minLength > *maxLength {
			return nil, fmt.Errorf(
				"action input schema %s minLength exceeds maxLength",
				path,
			)
		}
		node.minLength, node.maxLength = minLength, maxLength
	case "integer", "number":
		minimum, err := optionalActionSchemaNumberV1(object, "minimum", path)
		if err != nil {
			return nil, err
		}
		maximum, err := optionalActionSchemaNumberV1(object, "maximum", path)
		if err != nil {
			return nil, err
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			return nil, fmt.Errorf(
				"action input schema %s minimum exceeds maximum",
				path,
			)
		}
		node.minimum, node.maximum = minimum, maximum
	}
	if raw, present := object["enum"]; present {
		if typeName == "object" || typeName == "array" {
			return nil, fmt.Errorf(
				"action input schema %s enum is only supported for scalar types",
				path,
			)
		}
		values, valid := raw.([]any)
		if !valid || len(values) == 0 ||
			len(values) > MaxActionSchemaArrayItemsV1 {
			return nil, fmt.Errorf(
				"action input schema %s enum must contain between 1 and %d values",
				path,
				MaxActionSchemaArrayItemsV1,
			)
		}
		seen := make(map[string]struct{}, len(values))
		for index, value := range values {
			if err := validateActionInstanceNodeV1(
				node,
				value,
				fmt.Sprintf("%s.enum[%d]", path, index),
				false,
			); err != nil {
				return nil, err
			}
			key, err := canonicalActionScalarKeyV1(value)
			if err != nil {
				return nil, fmt.Errorf("action input schema %s enum: %w", path, err)
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf(
					"action input schema %s enum contains a duplicate value",
					path,
				)
			}
			seen[key] = struct{}{}
		}
		node.enum = append([]any(nil), values...)
	}
	return node, nil
}

func optionalActionSchemaIntegerV1(
	object map[string]any,
	keyword string,
	maximum int,
	path string,
) (*int, error) {
	raw, present := object[keyword]
	if !present {
		return nil, nil
	}
	number, valid := raw.(json.Number)
	if !valid {
		return nil, fmt.Errorf(
			"action input schema %s %s must be an integer",
			path,
			keyword,
		)
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || parsed < 0 || parsed > int64(maximum) {
		return nil, fmt.Errorf(
			"action input schema %s %s must be between 0 and %d",
			path,
			keyword,
			maximum,
		)
	}
	value := int(parsed)
	return &value, nil
}

func optionalActionSchemaNumberV1(
	object map[string]any,
	keyword string,
	path string,
) (*float64, error) {
	raw, present := object[keyword]
	if !present {
		return nil, nil
	}
	number, valid := raw.(json.Number)
	if !valid {
		return nil, fmt.Errorf(
			"action input schema %s %s must be a number",
			path,
			keyword,
		)
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, fmt.Errorf(
			"action input schema %s %s is not a finite number",
			path,
			keyword,
		)
	}
	return &parsed, nil
}

func canonicalActionInstanceV1(
	input json.RawMessage,
	requireCanonical bool,
) ([]byte, any, error) {
	if len(input) == 0 {
		return nil, nil, fmt.Errorf("action input must not be empty")
	}
	canonical, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: MaxTextBytes,
			MaxDepth: MaxActionSchemaDepthV1 + 2,
			MaxNodes: maxActionInputNodesV1,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("action input: %w", err)
	}
	if requireCanonical && !bytes.Equal(canonical, input) {
		return nil, nil, fmt.Errorf("action input is not canonical JSON")
	}
	value, err := decodeActionJSONValueV1(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("decode action input: %w", err)
	}
	if _, object := value.(map[string]any); !object {
		return nil, nil, fmt.Errorf("action input root must be an object")
	}
	return bytes.Clone(canonical), value, nil
}

func validateActionInstanceNodeV1(
	node *actionSchemaNodeV1,
	value any,
	path string,
	checkEnum bool,
) error {
	if checkEnum && len(node.enum) != 0 {
		key, err := canonicalActionScalarKeyV1(value)
		if err != nil {
			return fmt.Errorf("action input %s does not match scalar enum", path)
		}
		matched := false
		for _, allowed := range node.enum {
			allowedKey, _ := canonicalActionScalarKeyV1(allowed)
			if key == allowedKey {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("action input %s is not an allowed enum value", path)
		}
	}
	switch node.typeName {
	case "object":
		object, valid := value.(map[string]any)
		if !valid {
			return fmt.Errorf("action input %s must be an object", path)
		}
		for required := range node.required {
			if _, present := object[required]; !present {
				return fmt.Errorf(
					"action input %s is missing required property %q",
					path,
					required,
				)
			}
		}
		keys := make([]string, 0, len(object))
		for name := range object {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			child, declared := node.properties[name]
			if !declared {
				return fmt.Errorf(
					"action input %s contains undeclared property %q",
					path,
					name,
				)
			}
			if err := validateActionInstanceNodeV1(
				child,
				object[name],
				path+"."+name,
				true,
			); err != nil {
				return err
			}
		}
	case "array":
		array, valid := value.([]any)
		if !valid {
			return fmt.Errorf("action input %s must be an array", path)
		}
		if node.minItems != nil && len(array) < *node.minItems {
			return fmt.Errorf("action input %s has fewer than minItems", path)
		}
		if node.maxItems != nil && len(array) > *node.maxItems {
			return fmt.Errorf("action input %s exceeds maxItems", path)
		}
		for index, item := range array {
			if err := validateActionInstanceNodeV1(
				node.items,
				item,
				fmt.Sprintf("%s[%d]", path, index),
				true,
			); err != nil {
				return err
			}
		}
	case "string":
		text, valid := value.(string)
		if !valid {
			return fmt.Errorf("action input %s must be a string", path)
		}
		if text != CanonicalText(text) {
			return fmt.Errorf("action input %s string must use Unicode NFC", path)
		}
		length := utf8.RuneCountInString(text)
		if node.minLength != nil && length < *node.minLength {
			return fmt.Errorf("action input %s is shorter than minLength", path)
		}
		if node.maxLength != nil && length > *node.maxLength {
			return fmt.Errorf("action input %s exceeds maxLength", path)
		}
	case "integer", "number":
		number, valid := value.(json.Number)
		if !valid {
			return fmt.Errorf("action input %s must be a %s", path, node.typeName)
		}
		parsed, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return fmt.Errorf("action input %s must be a finite number", path)
		}
		if node.typeName == "integer" && math.Trunc(parsed) != parsed {
			return fmt.Errorf("action input %s must be an integer", path)
		}
		if node.minimum != nil && parsed < *node.minimum {
			return fmt.Errorf("action input %s is below minimum", path)
		}
		if node.maximum != nil && parsed > *node.maximum {
			return fmt.Errorf("action input %s exceeds maximum", path)
		}
	case "boolean":
		if _, valid := value.(bool); !valid {
			return fmt.Errorf("action input %s must be a boolean", path)
		}
	default:
		return fmt.Errorf("action input %s has unknown compiled type", path)
	}
	return nil
}

func decodeActionJSONValueV1(canonical []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func canonicalActionScalarKeyV1(value any) (string, error) {
	switch value.(type) {
	case bool, string, json.Number:
	default:
		return "", fmt.Errorf("value is not a supported scalar")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	canonical, err := CanonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func validateActionSchemaTextV1(
	name string,
	value string,
	maximum int,
	allowEmpty bool,
) error {
	if err := validateBoundedText(name, value, maximum, allowEmpty); err != nil {
		return err
	}
	if value != CanonicalText(value) {
		return fmt.Errorf("%s must use Unicode NFC", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) &&
			character != '\n' && character != '\r' && character != '\t' {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func validateActionPropertyNameV1(name string) error {
	if err := validateActionSchemaTextV1(
		"action property name",
		name,
		MaxIdentifierBytes,
		false,
	); err != nil {
		return err
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return fmt.Errorf("action property name contains a control character")
		}
	}
	return nil
}
