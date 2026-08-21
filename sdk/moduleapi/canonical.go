package moduleapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	SHA256HexLength    = sha256.Size * 2
	MaxIdentifierBytes = 128
	MaxVersionBytes    = 64
	MaxManifestEntries = 256
	MaxConfigBytes     = 64 << 10
	MaxTextBytes       = 1 << 20
	MaxOpaqueIDBytes   = 256

	maxCanonicalJSONDepth = 10000
)

// CanonicalJSONLimits are fixed resource limits applied before and while
// parsing JSON for RFC 8785 canonicalization.
type CanonicalJSONLimits struct {
	MaxBytes int
	MaxDepth int
	MaxNodes int
}

// CanonicalText returns the NFC representation used for textual identities.
func CanonicalText(value string) string {
	return norm.NFC.String(value)
}

// CanonicalJSON implements the JSON Canonicalization Scheme in RFC 8785.
func CanonicalJSON(payload []byte) ([]byte, error) {
	return canonicalJSONWithLimits(payload, CanonicalJSONLimits{
		MaxBytes: len(payload) + 1,
		MaxDepth: maxCanonicalJSONDepth,
		MaxNodes: int(^uint(0) >> 1),
	})
}

// CanonicalJSONWithLimits implements the JSON Canonicalization Scheme in
// RFC 8785 while enforcing fixed byte, nesting-depth, and decoded-value limits.
func CanonicalJSONWithLimits(
	payload []byte,
	limits CanonicalJSONLimits,
) ([]byte, error) {
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxNodes <= 0 {
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: limits must be positive")
	}
	if len(payload) > limits.MaxBytes {
		return nil, fmt.Errorf(
			"canonicalize RFC 8785 JSON: input exceeds %d bytes",
			limits.MaxBytes,
		)
	}
	return canonicalJSONWithLimits(payload, limits)
}

func canonicalJSONWithLimits(
	payload []byte,
	limits CanonicalJSONLimits,
) ([]byte, error) {
	if !utf8.Valid(payload) {
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: input is not valid UTF-8")
	}
	if err := validateJSONUnicodeEscapes(payload); err != nil {
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeCanonicalValue(
		decoder,
		0,
		limits.MaxDepth,
		limits.MaxNodes,
		&nodes,
	)
	if err != nil {
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: %w", err)
	}
	if lexeme, trailingErr := decoder.Token(); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return nil, fmt.Errorf("canonicalize RFC 8785 JSON: trailing token %v", lexeme)
		}
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: trailing data: %w", trailingErr)
	}
	var canonical bytes.Buffer
	if err := appendCanonicalValue(&canonical, value); err != nil {
		return nil, fmt.Errorf("canonicalize RFC 8785 JSON: %w", err)
	}
	return canonical.Bytes(), nil
}

// Digest returns a lowercase SHA-256 digest with an unambiguous domain prefix.
func Digest(domain string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}

// canonicalConfig normalizes one bounded JSON object used by a current wire
// contract. It is intentionally private: configuration ownership belongs to
// the binding that embeds the canonical bytes, not to a second attachment
// protocol.
func canonicalConfig(config json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage(`{}`)
	}
	if len(config) > MaxConfigBytes {
		return nil, fmt.Errorf("module config exceeds %d bytes", MaxConfigBytes)
	}
	canonical, err := CanonicalJSON(config)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return nil, fmt.Errorf("module config must be a JSON object")
	}
	if len(canonical) > MaxConfigBytes {
		return nil, fmt.Errorf(
			"canonical module config exceeds %d bytes",
			MaxConfigBytes,
		)
	}
	return json.RawMessage(canonical), nil
}

func decodeCanonicalValue(
	decoder *json.Decoder,
	depth int,
	maxDepth int,
	maxNodes int,
	nodes *int,
) (any, error) {
	if *nodes >= maxNodes {
		return nil, fmt.Errorf("JSON value count exceeds %d nodes", maxNodes)
	}
	*nodes++
	lexeme, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := lexeme.(json.Delim)
	if !isDelimiter {
		switch lexeme.(type) {
		case nil, bool, string, json.Number:
			return lexeme, nil
		default:
			return nil, fmt.Errorf("unsupported JSON token %T", lexeme)
		}
	}
	if depth >= maxDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxDepth)
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyLexeme, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			key, ok := keyLexeme.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("JSON object contains duplicate key %q", key)
			}
			child, childErr := decodeCanonicalValue(
				decoder,
				depth+1,
				maxDepth,
				maxNodes,
				nodes,
			)
			if childErr != nil {
				return nil, childErr
			}
			object[key] = child
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil {
			return nil, closeErr
		}
		if closing != json.Delim('}') {
			return nil, fmt.Errorf("JSON object has invalid closing delimiter")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			child, childErr := decodeCanonicalValue(
				decoder,
				depth+1,
				maxDepth,
				maxNodes,
				nodes,
			)
			if childErr != nil {
				return nil, childErr
			}
			array = append(array, child)
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil {
			return nil, closeErr
		}
		if closing != json.Delim(']') {
			return nil, fmt.Errorf("JSON array has invalid closing delimiter")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func appendCanonicalValue(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		appendCanonicalString(output, typed)
	case json.Number:
		number, err := canonicalNumber(string(typed))
		if err != nil {
			return err
		}
		output.WriteString(number)
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendCanonicalValue(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(left, right int) bool {
			return lessUTF16(keys[left], keys[right])
		})
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			appendCanonicalString(output, key)
			output.WriteByte(':')
			if err := appendCanonicalValue(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported decoded JSON value %T", value)
	}
	return nil
}

func appendCanonicalString(output *bytes.Buffer, value string) {
	output.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteRune(character)
		case '\b':
			output.WriteString(`\b`)
		case '\t':
			output.WriteString(`\t`)
		case '\n':
			output.WriteString(`\n`)
		case '\f':
			output.WriteString(`\f`)
		case '\r':
			output.WriteString(`\r`)
		default:
			if character >= 0 && character <= 0x1f {
				fmt.Fprintf(output, `\u%04x`, character)
			} else {
				output.WriteRune(character)
			}
		}
	}
	output.WriteByte('"')
}

func canonicalNumber(value string) (string, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return "", fmt.Errorf("number %q is not representable as finite IEEE 754 binary64", value)
	}
	if number == 0 {
		return "0", nil
	}
	absolute := math.Abs(number)
	if absolute >= 1e-6 && absolute < 1e21 {
		return strconv.FormatFloat(number, 'f', -1, 64), nil
	}
	scientific := strconv.FormatFloat(number, 'e', -1, 64)
	mantissa, exponent, found := strings.Cut(scientific, "e")
	if !found {
		return "", fmt.Errorf("format JSON number %q", value)
	}
	sign := ""
	if strings.HasPrefix(exponent, "+") || strings.HasPrefix(exponent, "-") {
		sign, exponent = exponent[:1], exponent[1:]
	}
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	return mantissa + "e" + sign + exponent, nil
}

func lessUTF16(left, right string) bool {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] != rightUnits[index] {
			return leftUnits[index] < rightUnits[index]
		}
	}
	return len(leftUnits) < len(rightUnits)
}

func validateJSONUnicodeEscapes(payload []byte) error {
	inString := false
	for index := 0; index < len(payload); index++ {
		switch payload[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			if index+1 >= len(payload) {
				return fmt.Errorf("unterminated JSON escape")
			}
			index++
			if payload[index] != 'u' {
				continue
			}
			codeUnit, err := parseHexCodeUnit(payload, index+1)
			if err != nil {
				return err
			}
			index += 4
			switch {
			case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
				if index+6 >= len(payload) || payload[index+1] != '\\' || payload[index+2] != 'u' {
					return fmt.Errorf("high UTF-16 surrogate must be followed by a low surrogate")
				}
				low, lowErr := parseHexCodeUnit(payload, index+3)
				if lowErr != nil {
					return lowErr
				}
				if low < 0xdc00 || low > 0xdfff {
					return fmt.Errorf("high UTF-16 surrogate must be followed by a low surrogate")
				}
				index += 6
			case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
				return fmt.Errorf("lone low UTF-16 surrogate is invalid")
			}
		}
	}
	return nil
}

func parseHexCodeUnit(payload []byte, start int) (uint16, error) {
	if start+4 > len(payload) {
		return 0, fmt.Errorf("short Unicode escape")
	}
	var value uint16
	for index := start; index < start+4; index++ {
		value <<= 4
		switch character := payload[index]; {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, fmt.Errorf("invalid Unicode escape")
		}
	}
	return value, nil
}
