package controlapicontract

import (
	"bytes"
	"fmt"
)

// NewPublishedBasisRefV1 freezes the authority-free identity of one exact
// published Control/Catalog basis. Its digest is the sole Published Pointer
// digest; callers must not define a second domain for the same identity.
func NewPublishedBasisRefV1(
	input PublishedBasisRefV1,
) (PublishedBasisRefV1, []byte, string, error) {
	if err := input.Validate(); err != nil {
		return PublishedBasisRefV1{}, nil, "", err
	}
	canonical, digest, err := freezeV1(
		input,
		publishedBasisRefDigestDomainV1,
		MaxPublishedBasisRefWireBytesV1,
		32,
	)
	if err != nil {
		return PublishedBasisRefV1{}, nil, "", err
	}
	return input, canonical, digest, nil
}

// RestorePublishedBasisRefV1 accepts only the exact canonical basis and its
// domain-separated Published Pointer digest.
func RestorePublishedBasisRefV1(
	canonical []byte,
	expectedDigest string,
) (PublishedBasisRefV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxPublishedBasisRefWireBytesV1,
		32,
	); err != nil {
		return PublishedBasisRefV1{}, err
	}
	if err := verifyDigestV1(
		publishedBasisRefDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return PublishedBasisRefV1{}, err
	}
	var decoded PublishedBasisRefV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return PublishedBasisRefV1{}, err
	}
	restored, rebuilt, digest, err := NewPublishedBasisRefV1(decoded)
	if err != nil {
		return PublishedBasisRefV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return PublishedBasisRefV1{}, fmt.Errorf(
			"controlapicontract: published basis is not frozen canonically",
		)
	}
	return restored, nil
}
