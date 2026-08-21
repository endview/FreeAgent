package controlhttp

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func isStaticUIPathV1(path string) bool {
	_, _, ok := staticUIRouteV1(path)
	return ok
}

func staticUIRouteV1(path string) (string, string, bool) {
	switch path {
	case StaticUIRootPathV1, StaticUIIndexPathV1:
		return "index.html", "text/html; charset=utf-8", true
	case StaticUIAppCSSPathV1:
		return "assets/app.css", "text/css; charset=utf-8", true
	case StaticUIAppJSPathV1:
		return "assets/app.js", "text/javascript; charset=utf-8", true
	case StaticUIReactJSPathV1:
		return "assets/react.js", "text/javascript; charset=utf-8", true
	case StaticUITanStackQueryJSPathV1:
		return "assets/tanstack-query.js", "text/javascript; charset=utf-8", true
	default:
		return "", "", false
	}
}

func (handler *HandlerV1) serveStaticAssetV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	assetPath, mediaType, routed := staticUIRouteV1(request.URL.Path)
	if !routed || handler == nil || handler.staticAssets == nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
		return true
	}
	if request.URL.RawQuery != "" || !emptyRequestBodyV1(request) ||
		headerPresentV1(request.Header, "Content-Type") ||
		hasStaticAuthorityOrMutationHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	if _, _, err := exactStrongIfNoneMatchV1(request.Header); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}

	asset, found, err := handler.staticAssets.ResolveStaticAssetV1(assetPath)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	if !found {
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
		return true
	}
	payload := bytes.Clone(asset.Bytes)
	defer clear(payload)
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	if asset.Path != assetPath || asset.MediaType != mediaType || asset.Size == 0 ||
		asset.Size > MaximumStaticAssetBytesV1 || asset.Size != uint64(len(payload)) ||
		!moduleapi.ValidSHA256(asset.SHA256) || digest != asset.SHA256 {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}

	etag := `"` + asset.SHA256 + `"`
	writer.Header().Set("Content-Type", mediaType)
	writer.Header().Set("Content-Length", strconv.FormatUint(asset.Size, 10))
	writer.Header().Set("ETag", etag)
	if notModified, matchErr := matchesIfNoneMatchV1(request.Header, etag); matchErr != nil {
		writer.Header().Del("Content-Length")
		writer.Header().Del("Content-Type")
		writer.Header().Del("ETag")
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.Header().Del("Content-Length")
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return true
	}
	_, _ = writer.Write(payload)
	return true
}

func hasStaticAuthorityOrMutationHeaderV1(header http.Header) bool {
	for _, name := range []string{
		"Authorization",
		"Proxy-Authorization",
		"Idempotency-Key",
		"If-Match",
		"If-Modified-Since",
		"If-Range",
		"If-Unmodified-Since",
		"Range",
		"Content-Range",
	} {
		if headerPresentV1(header, name) {
			return true
		}
	}
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "x-freeagent-") {
			return true
		}
	}
	return false
}
