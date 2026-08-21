package main

import "net/http"

// exactChannelHTTPMux performs no path cleaning and emits no implicit
// redirects. Only the exact configured path reaches the Channel decoder;
// every other request keeps the existing local-chat behavior.
type exactChannelHTTPMux struct {
	path     string
	channel  http.Handler
	fallback http.Handler
}

func (mux *exactChannelHTTPMux) ServeHTTP(
	response http.ResponseWriter,
	request *http.Request,
) {
	if mux != nil && request != nil && request.URL != nil &&
		request.URL.Path == mux.path {
		mux.channel.ServeHTTP(response, request)
		return
	}
	mux.fallback.ServeHTTP(response, request)
}
