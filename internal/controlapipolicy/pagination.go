package controlapipolicy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	DefaultPageSizeV1       = 50
	MaximumPageSizeV1       = controlapicontract.MaxControlPageSizeV1
	MaximumPageCursorBytes  = 1024
	MaximumFilterCountV1    = 16
	MaximumFilterNameBytes  = 64
	MaximumFilterValueBytes = 256

	PageCursorSchemaVersionV1 = "control-page-keyset/v1"
	PageCursorPrefixV1        = "fa1."
	filterSetDigestDomainV1   = "freeagent.control-filter-set/v1"
)

// KeysetCursorSpecV1 describes the future cursor codec contract. This package
// deliberately does not mint, sign, verify, decode, or persist cursor tokens.
type KeysetCursorSpecV1 struct {
	SchemaVersion       string
	Prefix              string
	Encoding            string
	Protection          string
	MaximumTokenBytes   int
	ServerIssuedOnly    bool
	OpaqueToClient      bool
	BindsBootID         bool
	BindsPrincipal      bool
	BindsScope          bool
	BindsResourceKind   bool
	BindsFilters        bool
	BindsOrdering       bool
	BindsSortVersion    bool
	BindsSourceRevision bool
	BindsLastKey        bool
	CarriesLastKeyOnly  bool
	ForbidsOffsetPaging bool
}

func DefaultKeysetCursorSpecV1() KeysetCursorSpecV1 {
	return KeysetCursorSpecV1{
		SchemaVersion:       PageCursorSchemaVersionV1,
		Prefix:              PageCursorPrefixV1,
		Encoding:            "BASE64URL_NO_PADDING",
		Protection:          "AUTHENTICATED_OPAQUE",
		MaximumTokenBytes:   MaximumPageCursorBytes,
		ServerIssuedOnly:    true,
		OpaqueToClient:      true,
		BindsBootID:         true,
		BindsPrincipal:      true,
		BindsScope:          true,
		BindsResourceKind:   true,
		BindsFilters:        true,
		BindsOrdering:       true,
		BindsSortVersion:    true,
		BindsSourceRevision: true,
		BindsLastKey:        true,
		CarriesLastKeyOnly:  true,
		ForbidsOffsetPaging: true,
	}
}

func (spec KeysetCursorSpecV1) Validate() error {
	if spec != DefaultKeysetCursorSpecV1() {
		return fmt.Errorf("controlapipolicy: invalid keyset cursor specification")
	}
	return nil
}

// NormalizePageQueryV1 applies the frozen default and then delegates the
// authoritative public page shape validation to controlapicontract.
func NormalizePageQueryV1(
	input controlapicontract.PageQueryV1,
) (controlapicontract.PageQueryV1, error) {
	result := input
	if result.Limit == 0 {
		result.Limit = DefaultPageSizeV1
	}
	if input.After != nil {
		cloned := *input.After
		result.After = &cloned
	}
	if err := result.Validate(); err != nil {
		return controlapicontract.PageQueryV1{}, err
	}
	return result, nil
}

// FilterTermV1 is internal canonical filter metadata, not a second public page
// wire. Only its digest may be placed in controlapicontract.PageQueryV1.
type FilterTermV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DigestFilterTermsV1 validates endpoint-specific exact equality filters and
// returns their canonical server-side metadata plus a domain-separated digest.
// The raw filters stay out of the public PageQueryV1 wire.
func DigestFilterTermsV1(
	input []FilterTermV1,
	allowedFilterNames []string,
) ([]FilterTermV1, string, error) {
	if len(input) > MaximumFilterCountV1 {
		return nil, "", fmt.Errorf(
			"controlapipolicy: at most %d filters are allowed",
			MaximumFilterCountV1,
		)
	}
	allowed, err := canonicalFilterAllowlistV1(allowedFilterNames)
	if err != nil {
		return nil, "", err
	}
	if len(input) == 0 {
		return nil, "", nil
	}
	result := append([]FilterTermV1(nil), input...)
	previous := ""
	for index, filter := range result {
		if !validFilterNameV1(filter.Name) {
			return nil, "", fmt.Errorf(
				"controlapipolicy: filter %d has an invalid name",
				index,
			)
		}
		if _, ok := allowed[filter.Name]; !ok {
			return nil, "", fmt.Errorf(
				"controlapipolicy: filter %q is not allowed",
				filter.Name,
			)
		}
		if filter.Value == "" || len(filter.Value) > MaximumFilterValueBytes ||
			filter.Value != strings.TrimSpace(filter.Value) ||
			filter.Value != moduleapi.CanonicalText(filter.Value) {
			return nil, "", fmt.Errorf(
				"controlapipolicy: filter %q has an invalid value",
				filter.Name,
			)
		}
		for _, character := range filter.Value {
			if character < 0x20 || character == 0x7f {
				return nil, "", fmt.Errorf(
					"controlapipolicy: filter %q has an invalid value",
					filter.Name,
				)
			}
		}
		if index > 0 && filter.Name <= previous {
			return nil, "", fmt.Errorf(
				"controlapipolicy: filters must be unique and sorted by name",
			)
		}
		previous = filter.Name
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("controlapipolicy: encode filter metadata: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumFilterCountV1 *
				(MaximumFilterNameBytes + MaximumFilterValueBytes + 32),
			MaxDepth: 4,
			MaxNodes: 1 + MaximumFilterCountV1*3,
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("controlapipolicy: canonicalize filter metadata: %w", err)
	}
	return result, moduleapi.Digest(filterSetDigestDomainV1, canonical), nil
}

func canonicalFilterAllowlistV1(input []string) (map[string]struct{}, error) {
	copyOfInput := append([]string(nil), input...)
	if !sort.StringsAreSorted(copyOfInput) {
		return nil, fmt.Errorf("controlapipolicy: filter allowlist is not sorted")
	}
	result := make(map[string]struct{}, len(copyOfInput))
	for _, name := range copyOfInput {
		if !validFilterNameV1(name) {
			return nil, fmt.Errorf("controlapipolicy: invalid allowed filter %q", name)
		}
		if _, duplicate := result[name]; duplicate {
			return nil, fmt.Errorf("controlapipolicy: duplicate allowed filter %q", name)
		}
		result[name] = struct{}{}
	}
	return result, nil
}

func validFilterNameV1(value string) bool {
	if value == "" || len(value) > MaximumFilterNameBytes {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9' && index > 0) ||
			(character == '_' && index > 0) {
			continue
		}
		return false
	}
	return true
}
