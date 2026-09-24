package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const (
	// ModelAuthorityCeilingSchemaV1 is the exact Core-owned authority schema
	// referenced by a model.generate/v2 PortBinding.AuthorityCeilingRef.
	ModelAuthorityCeilingSchemaV1 = "model-authority-ceiling/v1"

	modelAuthorityWireMaxDepthV1 = 8
	modelAuthorityWireMaxNodesV1 = 32
)

// ModelAuthorityCeilingV1 freezes the non-secret identity needed for one
// model provider to use its compiled official endpoint. SecretRef is an opaque
// local resolver identity, never credential material. The endpoint itself is
// deliberately absent and cannot be selected by this contract.
type ModelAuthorityCeilingV1 struct {
	SchemaVersion                 string `json:"schema_version"`
	TenantID                      string `json:"tenant_id"`
	Provider                      string `json:"provider"`
	SecretRef                     string `json:"secret_ref"`
	AllowOfficialProviderEndpoint bool   `json:"allow_official_provider_endpoint"`
}

// Validate verifies the exact v1 authority without changing it.
func (ceiling ModelAuthorityCeilingV1) Validate() error {
	_, _, err := NewModelAuthorityCeilingV1(ceiling)
	return err
}

// NewModelAuthorityCeilingV1 validates and canonically freezes one model
// authority ceiling. It grants only use of the provider endpoint compiled
// into the selected trusted adapter; arbitrary endpoints are not representable.
func NewModelAuthorityCeilingV1(
	input ModelAuthorityCeilingV1,
) (ModelAuthorityCeilingV1, []byte, error) {
	if input.SchemaVersion != ModelAuthorityCeilingSchemaV1 {
		return ModelAuthorityCeilingV1{}, nil, fmt.Errorf(
			"model authority ceiling schema_version must be %q",
			ModelAuthorityCeilingSchemaV1,
		)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "tenant_id", value: input.TenantID},
		{name: "provider", value: input.Provider},
		{name: "secret_ref", value: input.SecretRef},
	} {
		if err := validateOpaqueID(
			"model authority ceiling "+field.name,
			field.value,
		); err != nil {
			return ModelAuthorityCeilingV1{}, nil, err
		}
	}
	if !input.AllowOfficialProviderEndpoint {
		return ModelAuthorityCeilingV1{}, nil, fmt.Errorf(
			"model authority ceiling allow_official_provider_endpoint must be true",
		)
	}
	canonical, err := marshalCanonicalModelAuthorityCeilingV1(input)
	if err != nil {
		return ModelAuthorityCeilingV1{}, nil, err
	}
	return input, bytes.Clone(canonical), nil
}

// RestoreModelAuthorityCeilingV1 accepts only the exact canonical wire emitted
// by NewModelAuthorityCeilingV1. Unknown fields, including credential values or
// endpoint overrides, are rejected by the strict decoder.
func RestoreModelAuthorityCeilingV1(
	canonical []byte,
) (ModelAuthorityCeilingV1, error) {
	if len(canonical) == 0 || len(canonical) > MaxConfigBytes {
		return ModelAuthorityCeilingV1{}, fmt.Errorf(
			"model authority ceiling must contain between 1 and %d canonical bytes",
			MaxConfigBytes,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: modelAuthorityWireMaxDepthV1,
			MaxNodes: modelAuthorityWireMaxNodesV1,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return ModelAuthorityCeilingV1{}, fmt.Errorf(
			"model authority ceiling is not canonical JSON",
		)
	}

	var decoded ModelAuthorityCeilingV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return ModelAuthorityCeilingV1{}, fmt.Errorf(
			"decode model authority ceiling: %w",
			err,
		)
	}
	restored, rebuilt, err := NewModelAuthorityCeilingV1(decoded)
	if err != nil {
		return ModelAuthorityCeilingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ModelAuthorityCeilingV1{}, fmt.Errorf(
			"model authority ceiling is not frozen canonically",
		)
	}
	return restored, nil
}

func marshalCanonicalModelAuthorityCeilingV1(
	ceiling ModelAuthorityCeilingV1,
) ([]byte, error) {
	encoded, err := json.Marshal(ceiling)
	if err != nil {
		return nil, fmt.Errorf("marshal model authority ceiling: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: modelAuthorityWireMaxDepthV1,
			MaxNodes: modelAuthorityWireMaxNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("canonicalize model authority ceiling: %w", err)
	}
	return bytes.Clone(canonical), nil
}
