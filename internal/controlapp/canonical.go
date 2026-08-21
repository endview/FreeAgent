package controlapp

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maximumControlAppCanonicalBytesV1 = 64 << 20
	maximumControlAppCanonicalNodesV1 = 1 << 20

	modulesEmptyFilterDigestDomainV1 = "freeagent.control-modules-empty-filter/v1"
	modulesSourceDigestDomainV1      = "freeagent.control-modules-source/v1"
	modulesPositionDigestDomainV1    = "freeagent.control-modules-position/v1"
	modulesPageProjectionDomainV1    = "freeagent.control-modules-page/v1"
	moduleDetailProjectionDomainV1   = "freeagent.control-module-detail/v1"
	modulesPageETagDomainV1          = "freeagent.control-modules-page-etag/v1"
	moduleDetailETagDomainV1         = "freeagent.control-module-detail-etag/v1"
)

func modulesEmptyFilterDigestV1() string {
	return moduleapi.Digest(
		modulesEmptyFilterDigestDomainV1,
		[]byte(`{"filters":[]}`),
	)
}

func canonicalDigestV1(domain string, value any) ([]byte, string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumControlAppCanonicalBytesV1,
			MaxDepth: 32,
			MaxNodes: maximumControlAppCanonicalNodesV1,
		},
	)
	if err != nil {
		return nil, "", err
	}
	return bytes.Clone(canonical), moduleapi.Digest(domain, canonical), nil
}

func validOpaqueIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validPositiveJSONIntegerV1(value uint64) bool {
	return value > 0 && value <= uint64(1<<53-1) && value <= math.MaxInt64
}

func equalScopeV1(
	left controlapicontract.ControlScopeV1,
	right controlapicontract.ControlScopeV1,
) bool {
	return left == right
}
