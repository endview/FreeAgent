package main

import (
	"bytes"

	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controlweb"
)

// productionControlStaticAssetResolverV1 adapts the immutable embedded shell
// to the transport-neutral HTTP contract. It owns no Store or listener.
type productionControlStaticAssetResolverV1 struct{}

func (productionControlStaticAssetResolverV1) ResolveStaticAssetV1(
	path string,
) (controlhttp.StaticAssetV1, bool, error) {
	asset, found, err := controlweb.ResolveAsset(path)
	if err != nil || !found {
		return controlhttp.StaticAssetV1{}, found, err
	}
	payload := bytes.Clone(asset.Bytes)
	return controlhttp.StaticAssetV1{
		Path: asset.Path, MediaType: asset.MediaType,
		Size: uint64(len(payload)), SHA256: asset.SHA256, Bytes: payload,
	}, true, nil
}

var newControlStaticAssetResolverV1 = func() (
	controlhttp.StaticAssetResolverV1,
	error,
) {
	return productionControlStaticAssetResolverV1{}, nil
}

var _ controlhttp.StaticAssetResolverV1 = productionControlStaticAssetResolverV1{}
