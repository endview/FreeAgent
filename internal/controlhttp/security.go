package controlhttp

import "net/http"

// WrapSecurityHeadersV1 applies the frozen Control response policy outside
// an admission gate so startup and draining rejections cannot omit it. The
// wrapper does not inspect requests, route, authenticate, or mutate status.
func WrapSecurityHeadersV1(next http.Handler) (http.Handler, error) {
	if nilInterfaceV1(next) {
		return nil, ErrInvalidConfiguration
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writer == nil {
			return
		}
		setSecurityHeadersV1(writer.Header())
		next.ServeHTTP(writer, request)
	}), nil
}
