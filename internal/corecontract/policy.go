package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	contentRecordDomain = "freeagent.content-record/v1"
	policyMediaType     = "application/json"
	policyKind          = "POLICY"
)

type PolicyType string

const (
	PolicyPermission    PolicyType = "PERMISSION"
	PolicyResource      PolicyType = "RESOURCE"
	PolicyActivation    PolicyType = "ACTIVATION"
	PolicyContext       PolicyType = "CONTEXT"
	PolicyCost          PolicyType = "COST"
	PolicyScheduling    PolicyType = "SCHEDULING"
	PolicyConfiguration PolicyType = "CONFIGURATION"
)

func (kind PolicyType) Validate() error {
	switch kind {
	case PolicyPermission,
		PolicyResource,
		PolicyActivation,
		PolicyContext,
		PolicyCost,
		PolicyScheduling,
		PolicyConfiguration:
		return nil
	default:
		return fmt.Errorf("corecontract: unsupported policy type %q", kind)
	}
}

// PolicyDocument is the single S1 policy wire. Body is canonical JSON and
// aliases never enter this value.
type PolicyDocument struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	PolicyType PolicyType      `json:"policy_type"`
	Body       json.RawMessage `json:"body"`
}

// NewPolicyDocument freezes one policy and returns its exact POLICY
// ContentRecord reference and canonical bytes.
func NewPolicyDocument(
	id string,
	version string,
	policyType PolicyType,
	body json.RawMessage,
) (PolicyDocument, PolicyRef, []byte, error) {
	if !validOpaque(id, maxOpaqueIDBytes) {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: invalid policy ID")
	}
	if !validOpaque(version, maxVersionBytes) {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: invalid policy version")
	}
	if err := policyType.Validate(); err != nil {
		return PolicyDocument{}, PolicyRef{}, nil, err
	}
	canonicalBody, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: canonicalize policy body: %w", err)
	}
	if len(canonicalBody) == 0 || canonicalBody[0] != '{' {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: policy body must be a JSON object")
	}
	document := PolicyDocument{
		ID:         id,
		Version:    version,
		PolicyType: policyType,
		Body:       bytes.Clone(canonicalBody),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: encode policy document: %w", err)
	}
	canonicalDocument, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return PolicyDocument{}, PolicyRef{}, nil,
			fmt.Errorf("corecontract: canonicalize policy document: %w", err)
	}
	digest := contentDigest(policyKind, policyMediaType, canonicalDocument)
	ref := PolicyRef{ID: id, Version: version, Digest: digest}
	return document, ref, canonicalDocument, nil
}

// RestorePolicyDocument accepts only exact canonical bytes and verifies that
// the typed ref is the ContentRecord identity of those bytes.
func RestorePolicyDocument(
	canonical []byte,
	ref PolicyRef,
) (PolicyDocument, error) {
	if err := ref.Validate(); err != nil {
		return PolicyDocument{}, err
	}
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) {
		return PolicyDocument{},
			fmt.Errorf("corecontract: policy document is not canonical")
	}
	var document PolicyDocument
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return PolicyDocument{},
			fmt.Errorf("corecontract: decode policy document: %w", err)
	}
	rebuilt, rebuiltRef, rebuiltCanonical, err := NewPolicyDocument(
		document.ID,
		document.Version,
		document.PolicyType,
		document.Body,
	)
	if err != nil {
		return PolicyDocument{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) || rebuiltRef != ref {
		return PolicyDocument{},
			fmt.Errorf("corecontract: policy document does not match reference")
	}
	return rebuilt, nil
}

func contentDigest(kind, mediaType string, canonical []byte) string {
	preimage := make([]byte, 0, len(kind)+len(mediaType)+len(canonical)+2)
	preimage = append(preimage, kind...)
	preimage = append(preimage, 0)
	preimage = append(preimage, mediaType...)
	preimage = append(preimage, 0)
	preimage = append(preimage, canonical...)
	return moduleapi.Digest(contentRecordDomain, preimage)
}
