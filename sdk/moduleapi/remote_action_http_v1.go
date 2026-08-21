package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const (
	// RemoteActionHTTPBindingParametersSchemaV1 is the only consumer-owned
	// parameters object accepted by freeagent-action-http/v1. It identifies an
	// exact HTTPS endpoint and a SecretRef, never secret material.
	RemoteActionHTTPBindingParametersSchemaV1 = "remote-action-http-binding-parameters/v1"

	MaxRemoteActionHTTPEndpointBytesV1 = 2048
	remoteActionHTTPParametersMaxDepth = 16
)

// RemoteActionHTTPBindingParametersV1 is carried inside
// ActionBindingConfigV1.Parameters. A REMOTE Adapter must restore it for every
// private execution closure; endpoint and SecretRef must not be cached in an
// artifact-scoped Adapter shared by multiple activated Instances.
type RemoteActionHTTPBindingParametersV1 struct {
	SchemaVersion string `json:"schema_version"`
	EndpointURL   string `json:"endpoint_url"`
	SecretRef     string `json:"secret_ref"`
}

func (parameters RemoteActionHTTPBindingParametersV1) Validate() error {
	_, _, err := NewRemoteActionHTTPBindingParametersV1(parameters)
	return err
}

// NewRemoteActionHTTPBindingParametersV1 validates and canonically freezes a
// value. Arbitrary headers, proxy configuration, retry policy, TLS overrides,
// discovery URLs and credential values have no representation in this wire.
func NewRemoteActionHTTPBindingParametersV1(
	input RemoteActionHTTPBindingParametersV1,
) (RemoteActionHTTPBindingParametersV1, []byte, error) {
	if input.SchemaVersion != RemoteActionHTTPBindingParametersSchemaV1 {
		return RemoteActionHTTPBindingParametersV1{}, nil, fmt.Errorf(
			"remote Action HTTP binding parameters schema_version must be %q",
			RemoteActionHTTPBindingParametersSchemaV1,
		)
	}
	if err := validateRemoteActionHTTPEndpointV1(input.EndpointURL); err != nil {
		return RemoteActionHTTPBindingParametersV1{}, nil, err
	}
	if err := validateOpaqueID(
		"remote Action HTTP binding parameters secret_ref",
		input.SecretRef,
	); err != nil {
		return RemoteActionHTTPBindingParametersV1{}, nil, err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return RemoteActionHTTPBindingParametersV1{}, nil, fmt.Errorf(
			"encode remote Action HTTP binding parameters: %w",
			err,
		)
	}
	canonical, err := CanonicalJSONWithLimits(encoded, CanonicalJSONLimits{
		MaxBytes: MaxConfigBytes,
		MaxDepth: remoteActionHTTPParametersMaxDepth,
		MaxNodes: MaxConfigBytes,
	})
	if err != nil {
		return RemoteActionHTTPBindingParametersV1{}, nil, fmt.Errorf(
			"canonicalize remote Action HTTP binding parameters: %w",
			err,
		)
	}
	return input, bytes.Clone(canonical), nil
}

// RestoreRemoteActionHTTPBindingParametersV1 accepts only the exact canonical
// wire emitted by NewRemoteActionHTTPBindingParametersV1. Unknown fields are
// rejected so a package cannot smuggle a secret, custom header, proxy, retry,
// fallback, redirect or TLS-bypass instruction into Parameters.
func RestoreRemoteActionHTTPBindingParametersV1(
	canonical []byte,
) (RemoteActionHTTPBindingParametersV1, error) {
	owned := bytes.Clone(canonical)
	if len(owned) == 0 || len(owned) > MaxConfigBytes {
		return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
			"remote Action HTTP binding parameters must contain between 1 and %d bytes",
			MaxConfigBytes,
		)
	}
	normalized, err := CanonicalJSONWithLimits(owned, CanonicalJSONLimits{
		MaxBytes: MaxConfigBytes,
		MaxDepth: remoteActionHTTPParametersMaxDepth,
		MaxNodes: MaxConfigBytes,
	})
	if err != nil || len(normalized) == 0 || normalized[0] != '{' ||
		!bytes.Equal(owned, normalized) {
		return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
			"remote Action HTTP binding parameters must be an exact canonical JSON object",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(owned))
	decoder.DisallowUnknownFields()
	var decoded RemoteActionHTTPBindingParametersV1
	if err := decoder.Decode(&decoded); err != nil {
		return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
			"decode remote Action HTTP binding parameters: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
				"remote Action HTTP binding parameters contain trailing JSON",
			)
		}
		return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
			"decode remote Action HTTP binding parameters trailing data: %w",
			err,
		)
	}
	frozen, rebuilt, err := NewRemoteActionHTTPBindingParametersV1(decoded)
	if err != nil {
		return RemoteActionHTTPBindingParametersV1{}, err
	}
	if !bytes.Equal(owned, rebuilt) {
		return RemoteActionHTTPBindingParametersV1{}, fmt.Errorf(
			"remote Action HTTP binding parameters are not frozen canonically",
		)
	}
	return frozen, nil
}

func validateRemoteActionHTTPEndpointV1(raw string) error {
	if raw == "" || raw != strings.TrimSpace(raw) ||
		len(raw) > MaxRemoteActionHTTPEndpointBytesV1 {
		return fmt.Errorf(
			"remote Action HTTP endpoint_url must be non-empty, trimmed, and at most %d bytes",
			MaxRemoteActionHTTPEndpointBytesV1,
		)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return fmt.Errorf(
			"remote Action HTTP endpoint_url must be an absolute HTTPS URL without userinfo, query, or fragment",
		)
	}
	if parsed.RawPath != "" || parsed.Path == "" || parsed.Path[0] != '/' ||
		path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.Path, "//") {
		return fmt.Errorf(
			"remote Action HTTP endpoint_url path must be absolute, explicit, and canonical",
		)
	}
	if parsed.String() != raw || parsed.Host != strings.ToLower(parsed.Host) {
		return fmt.Errorf(
			"remote Action HTTP endpoint_url must use its exact canonical spelling",
		)
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.HasSuffix(hostname, ".") ||
		strings.Contains(hostname, "%") || !validRemoteActionHTTPHostV1(hostname) {
		return fmt.Errorf(
			"remote Action HTTP endpoint_url host must be a canonical IP literal or DNS name",
		)
	}
	if portValue := parsed.Port(); portValue != "" {
		portNumber, err := strconv.Atoi(portValue)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("remote Action HTTP endpoint_url port is invalid")
		}
	}
	return nil
}

func validRemoteActionHTTPHostV1(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' ||
			label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' ||
				character >= '0' && character <= '9' || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}
