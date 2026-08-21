package controlhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	controlOriginSchemeV1 = "http://"
	correlationBytesV1    = 16
	maximumSafeMicrosV1   = int64(1<<53 - 1)

	controlCSPV1 = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"connect-src 'self'; img-src 'self' data:; object-src 'none'; " +
		"base-uri 'none'; frame-ancestors 'none'; form-action 'none'; font-src 'none'; " +
		"worker-src 'none'; child-src 'none'; manifest-src 'none'; media-src 'none'"
	controlPermissionsPolicyV1 = "camera=(), geolocation=(), microphone=(), payment=(), usb=()"
)

var rejectedForwardingHeadersV1 = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Forwarded-Proto",
	"X-Forwarded-Server",
	"X-Real-IP",
}

var rejectedMethodOverrideHeadersV1 = []string{
	"X-HTTP-Method",
	"X-HTTP-Method-Override",
	"X-Method-Override",
}

// HandlerV1 is safe to share between requests after construction.
type HandlerV1 struct {
	authority                 string
	origin                    string
	registry                  *controlsession.RegistryV1
	staticAssets              StaticAssetResolverV1
	overview                  OverviewServiceV1
	modules                   ModulesServiceV1
	moduleDisableDryRun       ModuleDisableDryRunServiceV1
	moduleDisableConfirmation ModuleDisableConfirmationServiceV1
	moduleDisableMutation     ModuleDisableMutationServiceV1
	cursor                    ModulesCursorCodecV1
	now                       func() time.Time

	correlationPrefix string
	correlationCount  atomic.Uint64
	panicWriteMutex   sync.Mutex
	transportSlots    chan struct{}
}

type commitTrackingResponseWriterV1 struct {
	http.ResponseWriter
	committed bool
}

func (writer *commitTrackingResponseWriterV1) WriteHeader(status int) {
	if writer.committed {
		return
	}
	writer.committed = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *commitTrackingResponseWriterV1) Write(payload []byte) (int, error) {
	if !writer.committed {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(payload)
}

func NewHandlerV1(config ConfigV1) (*HandlerV1, error) {
	confirmationMissing := nilInterfaceV1(config.ModuleDisableConfirmation)
	mutationMissing := nilInterfaceV1(config.ModuleDisableMutation)
	if !validAuthorityV1(config.Authority) || config.Registry == nil ||
		nilInterfaceV1(config.Modules) || nilInterfaceV1(config.ModuleDisableDryRun) ||
		nilInterfaceV1(config.Cursor) || confirmationMissing != mutationMissing {
		return nil, ErrInvalidConfiguration
	}
	confirmation := config.ModuleDisableConfirmation
	mutation := config.ModuleDisableMutation
	overview := config.Overview
	if nilInterfaceV1(overview) {
		overview = nil
	}
	staticAssets := config.StaticAssets
	if nilInterfaceV1(staticAssets) {
		staticAssets = nil
	}
	if confirmationMissing {
		confirmation = nil
		mutation = nil
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	entropy := config.Entropy
	if nilInterfaceV1(entropy) {
		entropy = rand.Reader
	}
	prefixBytes := make([]byte, correlationBytesV1)
	if _, err := io.ReadFull(entropy, prefixBytes); err != nil {
		clear(prefixBytes)
		return nil, ErrInvalidConfiguration
	}
	prefix := base64.RawURLEncoding.EncodeToString(prefixBytes)
	clear(prefixBytes)
	return &HandlerV1{
		authority:                 config.Authority,
		origin:                    controlOriginSchemeV1 + config.Authority,
		registry:                  config.Registry,
		staticAssets:              staticAssets,
		overview:                  overview,
		modules:                   config.Modules,
		moduleDisableDryRun:       config.ModuleDisableDryRun,
		moduleDisableConfirmation: confirmation,
		moduleDisableMutation:     mutation,
		cursor:                    config.Cursor,
		now:                       now,
		correlationPrefix:         prefix,
		transportSlots:            make(chan struct{}, MaximumTransportAdmissionsV1),
	}, nil
}

func (handler *HandlerV1) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if writer == nil {
		return
	}
	setSecurityHeadersV1(writer.Header())
	if handler == nil || request == nil {
		writeStaticInternalErrorV1(writer)
		return
	}
	select {
	case handler.transportSlots <- struct{}{}:
		defer func() { <-handler.transportSlots }()
	default:
		handler.writeErrorV1(
			writer,
			controlapicontract.ErrorResourceExhaustedV1,
			0,
		)
		return
	}
	tracked := &commitTrackingResponseWriterV1{ResponseWriter: writer}
	writer = tracked
	ctx, cancel := context.WithTimeout(request.Context(), RequestTimeoutV1)
	defer cancel()
	request = request.WithContext(ctx)

	// W6-1 has no streaming, websocket, SSE, push, or raw-connection route, so
	// preserving Flusher, Hijacker, Pusher, or ReaderFrom would widen the
	// transport contract without a consumer.
	defer func() {
		if recovered := recover(); recovered != nil {
			if tracked.committed {
				// net/http owns post-commit panic recovery and closes the
				// truncated connection. Returning here would make a partial 200
				// look complete to an in-process caller.
				panic(recovered)
			}
			// Serialize the extremely rare panic fallback so a broken custom
			// ResponseWriter cannot turn recovery into recursive concurrent writes.
			handler.panicWriteMutex.Lock()
			defer handler.panicWriteMutex.Unlock()
			handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		}
	}()

	if code := handler.validateRequestEnvelopeV1(request); code != "" {
		handler.writeErrorV1(writer, code, 0)
		return
	}

	path := request.URL.Path
	switch {
	case isStaticUIPathV1(path):
		if handler.staticAssets == nil {
			handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveStaticAssetV1(writer, request)
	case path == BootstrapPathV1:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveBootstrapV1(writer, request)
	case path == SessionResumePathV1:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveSessionResumeV1(writer, request)
	case path == ModulesPathV1:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveModulesListV1(writer, request)
	case path == OverviewPathV1:
		if handler.overview == nil {
			handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
			return
		}
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveOverviewV1(writer, request)
	case path == ModuleDisableDryRunPathV1:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveModuleDisableDryRunV1(writer, request)
	case path == ModuleDisableConfirmationPathV1:
		if handler.moduleDisableConfirmation == nil {
			handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
			return
		}
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveModuleDisableConfirmationV1(writer, request)
	case path == ModuleDisableMutatePathV1:
		if handler.moduleDisableMutation == nil {
			handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
			return
		}
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		handler.serveModuleDisableMutateV1(writer, request)
	case strings.HasPrefix(path, ModuleDisableConfirmationPathV1+"/") ||
		strings.HasPrefix(path, ModuleDisableMutatePathV1+"/"):
		// These are exact non-resource operation paths. Never reinterpret a
		// sibling as a Module instance detail or inspect its authority/body.
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
	case strings.HasPrefix(path, ModulesPathV1+"/"):
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		instanceIDEncoding, encodingPresent, headerOK := exactHeaderValueV1(
			request.Header,
			ModuleInstanceIDEncodingHeaderV1,
			false,
		)
		if !headerOK {
			handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
			return
		}
		if !encodingPresent {
			instanceIDEncoding = ""
		}
		instanceID, ok := decodePathInstanceIDV1(
			strings.TrimPrefix(path, ModulesPathV1+"/"),
			instanceIDEncoding,
		)
		if !ok {
			handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
			return
		}
		handler.serveModuleDetailV1(writer, request, instanceID)
	default:
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
	}
}

func (handler *HandlerV1) serveModuleDisableDryRunV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !exactContentTypeV1(request.Header, true) ||
		headerPresentV1(request.Header, "Idempotency-Key") ||
		headerPresentV1(request.Header, ConfirmationHeaderV1) ||
		headerPresentV1(request.Header, ModuleInstanceIDEncodingHeaderV1) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitModuleOperationV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()
	ifMatchDigest, err := requiredIfMatchDigestV1(request.Header)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	body, err := readBoundedBodyV1(request, MaximumOperationBodyBytesV1)
	if err != nil {
		code := controlapicontract.ErrorInvalidRequestV1
		if errors.Is(err, errBodyTooLargeV1) {
			code = controlapicontract.ErrorResourceExhaustedV1
		}
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	defer clear(body)
	var inputBody controlapp.ModuleDisableDryRunBodyV1
	if err := decodeExactJSONV1(
		body,
		&inputBody,
		MaximumOperationBodyBytesV1,
		128,
	); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	frozenBody, canonicalBody, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(inputBody)
	clear(canonicalBody)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	expected := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: scope.TenantID,
		Revision:   frozenBody.ExpectedPointerRevision,
		Digest:     ifMatchDigest,
	}
	if err := expected.Validate(); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	result, err := handler.moduleDisableDryRun.DryRunModuleDisableV1(
		request.Context(),
		controlapp.ModuleDisableDryRunInputV1{
			Authorization:   permit,
			Scope:           scope,
			ExpectedPointer: expected,
			Body:            frozenBody,
			ObservedAt:      observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if !validModuleDisableDryRunResultV1(
		result,
		permit,
		scope,
		expected,
		frozenBody,
		inputDigest,
		observedAt,
	) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) validateRequestEnvelopeV1(
	request *http.Request,
) controlapicontract.ErrorCodeV1 {
	expectedTarget := ""
	if request.URL != nil {
		expectedTarget = request.URL.Path
		if request.URL.RawQuery != "" {
			expectedTarget += "?" + request.URL.RawQuery
		}
	}
	if len(request.RequestURI) > MaximumRequestTargetBytesV1 {
		return controlapicontract.ErrorResourceExhaustedV1
	}
	if request.Host != handler.authority || request.URL == nil ||
		request.URL.IsAbs() || request.URL.Host != "" || request.URL.Fragment != "" ||
		request.RequestURI == "" ||
		request.RequestURI != expectedTarget ||
		request.URL.Path == "" || request.URL.EscapedPath() != request.URL.Path ||
		strings.ContainsRune(request.URL.Path, '\\') {
		return controlapicontract.ErrorInvalidRequestV1
	}
	count, bytes, ok := requestHeaderSizeV1(request)
	if !ok || count > MaximumRequestHeaderCountV1 ||
		bytes > MaximumRequestHeaderBytesV1 {
		return controlapicontract.ErrorResourceExhaustedV1
	}
	for _, name := range rejectedForwardingHeadersV1 {
		if headerPresentV1(request.Header, name) {
			return controlapicontract.ErrorInvalidRequestV1
		}
	}
	for _, name := range rejectedMethodOverrideHeadersV1 {
		if headerPresentV1(request.Header, name) {
			return controlapicontract.ErrorInvalidRequestV1
		}
	}
	origins := request.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 &&
		(origins[0] != handler.origin || strings.Contains(origins[0], ","))) {
		return controlapicontract.ErrorForbiddenV1
	}
	if request.Method == http.MethodPost &&
		(len(origins) != 1 || origins[0] != handler.origin) {
		return controlapicontract.ErrorForbiddenV1
	}
	return ""
}

func (handler *HandlerV1) serveBootstrapV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !validBootstrapCookieEnvelopeV1(request) ||
		!exactContentTypeV1(request.Header, true) ||
		hasAmbientAuthorityHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	body, err := readBoundedBodyV1(request, MaximumBootstrapBodyBytesV1)
	if err != nil {
		code := controlapicontract.ErrorInvalidRequestV1
		if errors.Is(err, errBodyTooLargeV1) {
			code = controlapicontract.ErrorResourceExhaustedV1
		}
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	defer clear(body)
	var input bootstrapExchangeRequestV1
	if err := decodeExactJSONV1(body, &input, MaximumBootstrapBodyBytesV1, 32); err != nil ||
		input.SchemaVersion != BootstrapRequestSchemaV1 {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	capability, ok := decodeCredentialV1(input.Capability)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorUnauthenticatedV1, 0)
		return true
	}
	defer clear(capability)
	issued, err := handler.registry.ExchangeBootstrap(capability)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer clear(issued.SessionCredential)
	defer clear(issued.CSRFToken)
	defer clear(issued.ResumeCredential)
	if !validSessionProjectionV1(issued.Metadata, issued.AuthorizedScopes) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	csrfProof := encodeCredentialV1(issued.CSRFToken)
	resumeProof := encodeCredentialV1(issued.ResumeCredential)
	response := bootstrapExchangeResponseV2{
		SchemaVersion:    BootstrapResponseSchemaV2,
		Session:          issued.Metadata,
		AuthorizedScopes: append([]controlapicontract.ControlScopeV1{}, issued.AuthorizedScopes...),
		CSRFToken:        csrfProof,
		ResumeCredential: resumeProof,
	}
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	defer clear(encoded)
	http.SetCookie(writer, &http.Cookie{
		Name:     SessionCookieV1,
		Value:    encodeCredentialV1(issued.SessionCredential),
		Path:     "/control/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) serveSessionResumeV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !exactContentTypeV1(request.Header, true) ||
		hasAmbientAuthorityHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	body, err := readBoundedBodyV1(request, MaximumSessionResumeBodyBytesV1)
	if err != nil {
		code := controlapicontract.ErrorInvalidRequestV1
		if errors.Is(err, errBodyTooLargeV1) {
			code = controlapicontract.ErrorResourceExhaustedV1
		}
		handler.writeErrorV1(writer, code, 0)
		return true
	}
	defer clear(body)
	var input sessionResumeRequestV1
	if err := decodeExactJSONV1(
		body,
		&input,
		MaximumSessionResumeBodyBytesV1,
		16,
	); err != nil || input.SchemaVersion != SessionResumeRequestSchemaV1 {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	resumeCredential, ok := decodeCredentialV1(input.ResumeCredential)
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorUnauthenticatedV1, 0)
		return true
	}
	defer clear(resumeCredential)
	sessionCredential, err := sessionCredentialV1(request)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorUnauthenticatedV1, 0)
		return true
	}
	defer clear(sessionCredential)

	resumed, err := handler.registry.ResumeSessionV1(
		sessionCredential,
		resumeCredential,
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer clear(resumed.CSRFToken)
	defer clear(resumed.ResumeCredential)
	if !validSessionProjectionV1(resumed.Metadata, resumed.AuthorizedScopes) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	csrfProof := encodeCredentialV1(resumed.CSRFToken)
	resumeProof := encodeCredentialV1(resumed.ResumeCredential)
	response := sessionResumeResponseV1{
		SchemaVersion:    SessionResumeResponseSchemaV1,
		Session:          resumed.Metadata,
		AuthorizedScopes: append([]controlapicontract.ControlScopeV1{}, resumed.AuthorizedScopes...),
		CSRFToken:        csrfProof,
		ResumeCredential: resumeProof,
	}
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	defer clear(encoded)
	// The host-only cookie credential is unchanged across a resume. Emitting no
	// Set-Cookie is part of the protocol and avoids silently changing its scope.
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) serveModulesListV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if !emptyRequestBodyV1(request) || !exactContentTypeV1(request.Header, false) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) ||
		headerPresentV1(request.Header, ModuleInstanceIDEncodingHeaderV1) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	limit, cursorEnvelope, err := parseListQueryV1(request.URL.RawQuery)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitReadV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()

	var cursor *controlapp.DecodedModulesCursorV1
	if cursorEnvelope != "" {
		decoded, decodeErr := handler.cursor.DecodeModulesCursorV1(cursorEnvelope)
		if decodeErr != nil {
			if handler.cursor.IsStaleModulesCursorErrorV1(decodeErr) {
				handler.writeErrorV1(writer, controlapicontract.ErrorCursorStaleV1, 0)
			} else {
				handler.writeErrorV1(writer, controlapicontract.ErrorCursorInvalidV1, 0)
			}
			return true
		}
		cursor = &decoded
	}
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	page, err := handler.modules.ListModulesV1(
		request.Context(),
		controlapp.ListModulesInputV1{
			Authorization: permit,
			Scope:         scope,
			Page: controlapicontract.PageQueryV1{
				Limit: limit,
			},
			Cursor:     cursor,
			ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if !validStrongETagV1(page.StrongETag) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	nextCursor := ""
	if page.NextCursor != nil {
		nextCursor, err = handler.cursor.EncodeModulesCursorV1(*page.NextCursor)
		if err != nil || nextCursor == "" || len(nextCursor) > MaximumCursorTokenBytesV1 ||
			!rawURLTokenV1(nextCursor) {
			handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
			return true
		}
	}
	response := newModulesPageResponseV1(page, nextCursor)
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-modules-page-etag/v1",
		page.StrongETag,
		encoded,
	)
	writer.Header().Set("ETag", httpETag)
	if notModified, err := matchesIfNoneMatchV1(request.Header, httpETag); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) serveModuleDetailV1(
	writer http.ResponseWriter,
	request *http.Request,
	instanceID string,
) bool {
	if request.URL.RawQuery != "" || !emptyRequestBodyV1(request) ||
		!exactContentTypeV1(request.Header, false) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitReadV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	result, err := handler.modules.GetModuleV1(
		request.Context(),
		controlapp.GetModuleInputV1{
			Authorization: permit,
			Scope:         scope,
			InstanceID:    instanceID,
			ObservedAt:    observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if !validStrongETagV1(result.StrongETag) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-module-detail-etag/v1",
		result.StrongETag,
		encoded,
	)
	writer.Header().Set("ETag", httpETag)
	if notModified, err := matchesIfNoneMatchV1(request.Header, httpETag); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) admitReadV1(
	request *http.Request,
	scope controlapicontract.ControlScopeV1,
) (*controlsession.PermitV1, error) {
	credential, err := sessionCredentialV1(request)
	if err != nil {
		return nil, controlsession.ErrUnauthenticated
	}
	defer clear(credential)
	csrf, err := optionalCSRFV1(request.Header)
	if err != nil || len(csrf) == 0 {
		clear(csrf)
		return nil, controlsession.ErrUnauthenticated
	}
	defer clear(csrf)
	return handler.registry.Admit(controlsession.AdmissionV1{
		SessionCredential: credential,
		CSRFToken:         csrf,
		RequireCSRF:       true,
		Capability:        controlapicontract.CapabilityObserveV1,
		Scope:             scope,
	})
}

func (handler *HandlerV1) admitModuleOperationV1(
	request *http.Request,
	scope controlapicontract.ControlScopeV1,
) (*controlsession.PermitV1, error) {
	credential, err := sessionCredentialV1(request)
	if err != nil {
		return nil, controlsession.ErrUnauthenticated
	}
	defer clear(credential)
	csrf, err := optionalCSRFV1(request.Header)
	if err != nil || len(csrf) == 0 {
		clear(csrf)
		return nil, controlsession.ErrUnauthenticated
	}
	defer clear(csrf)
	return handler.registry.Admit(controlsession.AdmissionV1{
		SessionCredential: credential,
		CSRFToken:         csrf,
		RequireCSRF:       true,
		Capability:        controlapicontract.CapabilityOperateModulesV1,
		Scope:             scope,
	})
}

func requiredIfMatchDigestV1(header http.Header) (string, error) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		return "", controlapp.ErrPreconditionRequired
	}
	if len(values) != 1 || !validStrongETagV1(values[0]) {
		return "", controlapp.ErrInvalidRequest
	}
	return values[0][1 : len(values[0])-1], nil
}

func validModuleDisableDryRunResultV1(
	result controlapp.ModuleDisableDryRunResultV1,
	permit *controlsession.PermitV1,
	scope controlapicontract.ControlScopeV1,
	expected controlapicontract.ExpectedResourceRefV1,
	body controlapp.ModuleDisableDryRunBodyV1,
	inputDigest string,
	observedAt uint64,
) bool {
	if permit == nil || result.SchemaVersion !=
		controlapp.ModuleDisableDryRunResultSchemaVersionV1 {
		return false
	}
	frozenRequest, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(result.Request)
	clear(requestCanonical)
	if err != nil || frozenRequest != result.Request ||
		requestDigest != result.RequestDigest ||
		result.Request.PrincipalID != permit.Session().PrincipalID ||
		result.Request.Capability != controlapicontract.CapabilityOperateModulesV1 ||
		result.Request.Scope != scope ||
		result.Request.Operation != controlapicontract.OperationModuleDisableV1 ||
		result.Request.Intent != controlapicontract.OperationIntentDryRunV1 ||
		result.Request.IdempotencyKeyDigest != "" ||
		result.Request.ConfirmationDigest != "" ||
		result.Request.InputDigest != inputDigest ||
		result.Request.ExpectedRef != expected {
		return false
	}
	_, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(result.Receipt)
	clear(receiptCanonical)
	if err != nil || receiptDigest != result.ReceiptDigest ||
		result.Receipt.RequestDigest != requestDigest ||
		result.Receipt.PrincipalID != result.Request.PrincipalID ||
		result.Receipt.ScopeDigest != result.Request.ScopeDigest ||
		result.Receipt.Operation != result.Request.Operation ||
		result.Receipt.Intent != result.Request.Intent ||
		result.Receipt.Status != controlapicontract.OperationStatusDryRunV1 ||
		result.Receipt.ErrorCode != controlapicontract.ErrorNoneV1 ||
		result.Receipt.IdempotencyKeyDigest != "" ||
		result.Receipt.PreRef == nil || *result.Receipt.PreRef != expected ||
		result.Receipt.PostRef != nil || result.Receipt.DomainReceipt != nil ||
		result.Receipt.ReplayDisposition != controlapicontract.ReplayNoRetryV1 ||
		result.Receipt.CompletedAtUnixMicros != observedAt {
		return false
	}
	projection := result.Projection
	preconditionPointer, err := controlapp.PublishedPointerRefV1(
		projection.PreconditionBasis,
	)
	if !moduleapi.ValidSHA256(projection.PlanDigest) ||
		projection.InstanceID != body.InstanceID ||
		err != nil || preconditionPointer != expected ||
		projection.PreconditionBasis.Validate() != nil ||
		projection.ObservedBasis.Validate() != nil ||
		projection.CandidateBasis.Validate() != nil ||
		projection.PreconditionBasis.TenantID != scope.TenantID ||
		projection.ObservedBasis.TenantID != scope.TenantID ||
		projection.CandidateBasis.TenantID != scope.TenantID ||
		projection.CandidateState != controlapp.ModuleDisableProjectedNotReservedV1 {
		return false
	}
	switch projection.Disposition {
	case controlapp.ModuleDisableAlreadyAppliedV1,
		controlapp.ModuleDisableNoChangeV1,
		controlapp.ModuleDisableWouldApplyV1:
	default:
		return false
	}
	switch projection.CatalogChange {
	case controlapp.ModuleDisableCatalogNoneV1,
		controlapp.ModuleDisableCatalogRetainInstanceV1,
		controlapp.ModuleDisableCatalogRemoveInstanceV1:
	default:
		return false
	}
	switch projection.Disposition {
	case controlapp.ModuleDisableNoChangeV1:
		if projection.PreconditionBasis != projection.ObservedBasis ||
			projection.ObservedBasis != projection.CandidateBasis ||
			projection.CatalogChange != controlapp.ModuleDisableCatalogNoneV1 ||
			projection.BindingRemoval != nil {
			return false
		}
		return true
	case controlapp.ModuleDisableAlreadyAppliedV1:
		if !nextChangedPublishedBasisV1(
			projection.PreconditionBasis,
			projection.ObservedBasis,
		) || projection.ObservedBasis != projection.CandidateBasis ||
			projection.CatalogChange != controlapp.ModuleDisableCatalogNoneV1 ||
			projection.BindingRemoval != nil {
			return false
		}
		return true
	case controlapp.ModuleDisableWouldApplyV1:
		if projection.PreconditionBasis != projection.ObservedBasis ||
			!nextChangedPublishedBasisV1(
				projection.ObservedBasis,
				projection.CandidateBasis,
			) || projection.BindingRemoval == nil ||
			projection.CatalogChange == controlapp.ModuleDisableCatalogNoneV1 {
			return false
		}
	default:
		return false
	}
	binding := projection.BindingRemoval
	if binding.Target != body.BindingTarget || binding.Port != body.Port ||
		binding.Port.Validate() != nil || binding.FailurePolicy.Validate() != nil ||
		!moduleapi.ValidSHA256(binding.ConfigRef) ||
		!moduleapi.ValidSHA256(binding.AuthorityCeilingRef) {
		return false
	}
	for _, reference := range binding.StaticContextRefs {
		if !moduleapi.ValidSHA256(reference) {
			return false
		}
	}
	switch binding.Target.Kind {
	case controlapp.ModuleBindingTargetProfileV1:
		if binding.Target.ProfileID == "" || binding.Target.WorkspaceID != "" ||
			binding.Target.EndpointID != "" ||
			scope.Kind != controlapicontract.ScopeTenantV1 {
			return false
		}
	case controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1:
		if binding.Target.ProfileID != "" || binding.Target.WorkspaceID == "" ||
			binding.Target.EndpointID == "" || binding.PortBindingIndex != 0 ||
			(scope.Kind == controlapicontract.ScopeWorkspaceV1 &&
				binding.Target.WorkspaceID != scope.WorkspaceID) {
			return false
		}
	default:
		return false
	}
	return true
}

func nextPointerBasisV1(
	before controlapicontract.PublishedBasisRefV1,
	after controlapicontract.PublishedBasisRefV1,
) bool {
	return before.TenantID == after.TenantID &&
		after.PointerRevision == before.PointerRevision+1
}

func nextChangedPublishedBasisV1(
	before controlapicontract.PublishedBasisRefV1,
	after controlapicontract.PublishedBasisRefV1,
) bool {
	return nextPointerBasisV1(before, after) &&
		after.Control.Revision == before.Control.Revision+1 &&
		before.Control.ID != after.Control.ID &&
		before.Control.Digest != after.Control.Digest &&
		after.Catalog.Revision == before.Catalog.Revision+1 &&
		before.Catalog.ID != after.Catalog.ID &&
		before.Catalog.Digest != after.Catalog.Digest
}

func (handler *HandlerV1) observedAtV1() (uint64, bool) {
	now := handler.now()
	micros := now.UnixMicro()
	if micros <= 0 || micros > maximumSafeMicrosV1 {
		return 0, false
	}
	return uint64(micros), true
}

func validAuthorityV1(authority string) bool {
	if authority == "" || len(authority) > 64 ||
		authority != strings.TrimSpace(authority) {
		return false
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil || host != "127.0.0.1" || portText == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535 &&
		strconv.Itoa(port) == portText
}

func validPathInstanceIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || strings.ContainsRune(value, '/') ||
		strings.ContainsRune(value, '\\') {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("-._~!$&'()*+,;=:@", rune(character)) {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func validOpaqueInstanceIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func decodePathInstanceIDV1(segment string, encoding string) (string, bool) {
	if encoding == "" {
		if validPathInstanceIDV1(segment) {
			return segment, true
		}
		return "", false
	}
	if encoding != ModuleInstanceIDEncodingBase64URLUTF8V1 {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil || segment == "" ||
		base64.RawURLEncoding.EncodeToString(decoded) != segment {
		return "", false
	}
	instanceID := string(decoded)
	if !validOpaqueInstanceIDV1(instanceID) {
		return "", false
	}
	return instanceID, true
}

func requestHeaderSizeV1(request *http.Request) (int, int, bool) {
	if request == nil || request.Host == "" || strings.ContainsAny(request.Host, "\r\n") {
		return 0, 0, false
	}
	count := 1
	total := len("Host") + 2 + len(request.Host) + 2
	for name, values := range request.Header {
		if name == "" || len(values) == 0 || strings.ContainsAny(name, "\r\n") {
			return 0, 0, false
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				return 0, 0, false
			}
			count++
			total += len(name) + 2 + len(value) + 2
		}
	}
	return count, total, true
}

func headerPresentV1(header http.Header, name string) bool {
	for candidate := range header {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func hasUnknownFreeAgentAuthorityHeaderV1(header http.Header) bool {
	allowed := map[string]struct{}{
		http.CanonicalHeaderKey(CSRFHeaderV1):                     {},
		http.CanonicalHeaderKey(ScopeKindHeaderV1):                {},
		http.CanonicalHeaderKey(ScopeIDEncodingHeaderV1):          {},
		http.CanonicalHeaderKey(ModuleInstanceIDEncodingHeaderV1): {},
		http.CanonicalHeaderKey(TenantIDHeaderV1):                 {},
		http.CanonicalHeaderKey(WorkspaceIDHeaderV1):              {},
	}
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if strings.HasPrefix(strings.ToLower(canonical), "x-freeagent-") {
			if _, ok := allowed[canonical]; !ok {
				return true
			}
		}
	}
	return false
}

func hasAmbientAuthorityHeaderV1(header http.Header) bool {
	rejected := []string{
		"Authorization",
		"Proxy-Authorization",
		"Idempotency-Key",
		"If-Match",
		"If-None-Match",
	}
	for _, name := range rejected {
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

func validSessionProjectionV1(
	metadata controlapicontract.ControlSessionV1,
	authorizedScopes []controlapicontract.ControlScopeV1,
) bool {
	if authorizedScopes == nil {
		return false
	}
	frozenMetadata, _, _, err := controlapicontract.NewControlSessionV1(metadata)
	if err != nil || !reflect.DeepEqual(frozenMetadata, metadata) {
		return false
	}
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(authorizedScopes)
	return err == nil && scopeSet.Digest() == metadata.ScopeSetDigest &&
		reflect.DeepEqual(scopeSet.Scopes(), authorizedScopes)
}

func exactContentTypeV1(header http.Header, required bool) bool {
	values := header.Values("Content-Type")
	if len(values) == 0 {
		return !required
	}
	return len(values) == 1 && values[0] == "application/json"
}

func emptyRequestBodyV1(request *http.Request) bool {
	return request != nil && request.ContentLength == 0 &&
		len(request.TransferEncoding) == 0
}

var errBodyTooLargeV1 = errors.New("controlhttp: body too large")

func readBoundedBodyV1(request *http.Request, maximum int) ([]byte, error) {
	if request == nil || request.Body == nil || maximum <= 0 ||
		request.ContentLength > int64(maximum) || len(request.TransferEncoding) != 0 {
		if request != nil && request.ContentLength > int64(maximum) {
			return nil, errBodyTooLargeV1
		}
		return nil, errors.New("invalid body")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, int64(maximum)+1))
	if err != nil {
		return nil, errors.New("invalid body")
	}
	if len(body) > maximum {
		clear(body)
		return nil, errBodyTooLargeV1
	}
	if len(body) == 0 {
		return nil, errors.New("invalid body")
	}
	return body, nil
}

func decodeExactJSONV1(input []byte, output any, maxBytes, maxNodes int) error {
	if len(input) == 0 || len(input) > maxBytes || output == nil {
		return errors.New("invalid JSON")
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maxBytes,
			MaxDepth: 8,
			MaxNodes: maxNodes,
		},
	)
	if err != nil || !bytes.Equal(canonical, input) {
		clear(canonical)
		return errors.New("invalid JSON")
	}
	clear(canonical)
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.New("invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("invalid JSON")
	}
	return nil
}

func decodeCredentialV1(value string) ([]byte, bool) {
	if value == "" || len(value) != base64.RawURLEncoding.EncodedLen(
		controlsession.CredentialBytesV1,
	) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != controlsession.CredentialBytesV1 ||
		base64.RawURLEncoding.EncodeToString(decoded) != value {
		clear(decoded)
		return nil, false
	}
	return decoded, true
}

func encodeCredentialV1(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func sessionCredentialV1(request *http.Request) ([]byte, error) {
	if request == nil {
		return nil, errors.New("missing session")
	}
	count := 0
	value := ""
	for _, cookie := range request.Cookies() {
		if cookie.Name == SessionCookieV1 {
			count++
			value = cookie.Value
		}
	}
	if count != 1 {
		return nil, errors.New("missing session")
	}
	decoded, ok := decodeCredentialV1(value)
	if !ok {
		return nil, errors.New("invalid session")
	}
	return decoded, nil
}

// validBootstrapCookieEnvelopeV1 permits exactly the browser state that can
// survive a Control restart. Session cookies are host-only, so a browser sends
// the previous process credential to a new loopback port and JavaScript cannot
// remove the HttpOnly value. A valid one-time bootstrap capability may replace
// that sole canonical cookie; every ambient, malformed, or duplicate cookie is
// still rejected. The caller must not emit Set-Cookie until capability exchange
// has succeeded.
func validBootstrapCookieEnvelopeV1(request *http.Request) bool {
	if request == nil {
		return false
	}
	values := request.Header.Values("Cookie")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	prefix := SessionCookieV1 + "="
	value := values[0]
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	credential, ok := decodeCredentialV1(value[len(prefix):])
	clear(credential)
	return ok
}

func optionalCSRFV1(header http.Header) ([]byte, error) {
	values := header.Values(CSRFHeaderV1)
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, errors.New("invalid CSRF")
	}
	decoded, ok := decodeCredentialV1(values[0])
	if !ok {
		return nil, errors.New("invalid CSRF")
	}
	return decoded, nil
}

func scopeFromHeadersV1(header http.Header) (controlapicontract.ControlScopeV1, error) {
	kindValue, _, ok := exactHeaderValueV1(header, ScopeKindHeaderV1, true)
	if !ok {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	encoding, encodingPresent, ok := exactHeaderValueV1(
		header,
		ScopeIDEncodingHeaderV1,
		false,
	)
	if !ok || (encodingPresent && encoding != ScopeIDEncodingBase64URLUTF8V1) {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	tenantID, _, ok := exactHeaderValueV1(header, TenantIDHeaderV1, true)
	if !ok {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	if encodingPresent {
		var decoded bool
		tenantID, decoded = decodeScopeIDHeaderV1(tenantID)
		if !decoded {
			return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
		}
	}
	workspaceID, workspacePresent, ok := exactHeaderValueV1(
		header,
		WorkspaceIDHeaderV1,
		false,
	)
	if !ok {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	if encodingPresent && workspacePresent {
		var decoded bool
		workspaceID, decoded = decodeScopeIDHeaderV1(workspaceID)
		if !decoded {
			return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
		}
	}
	scope := controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ControlScopeKindV1(kindValue),
		TenantID:      tenantID,
		WorkspaceID:   workspaceID,
	}
	if scope.Kind == controlapicontract.ScopeTenantV1 && workspacePresent {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	if scope.Kind == controlapicontract.ScopeWorkspaceV1 && !workspacePresent {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	frozen, _, _, err := controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		return controlapicontract.ControlScopeV1{}, errors.New("invalid scope")
	}
	return frozen, nil
}

func decodeScopeIDHeaderV1(encoded string) (string, bool) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || encoded == "" ||
		base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return "", false
	}
	return string(decoded), true
}

func exactHeaderValueV1(
	header http.Header,
	name string,
	required bool,
) (string, bool, bool) {
	values := header.Values(name)
	if len(values) == 0 {
		return "", false, !required
	}
	if len(values) != 1 || values[0] == "" ||
		values[0] != strings.TrimSpace(values[0]) {
		return "", true, false
	}
	return values[0], true, true
}

func parseListQueryV1(raw string) (uint16, string, error) {
	if raw == "" {
		return 0, "", nil
	}
	if strings.ContainsAny(raw, ";%+") {
		return 0, "", errors.New("invalid query")
	}
	parts := strings.Split(raw, "&")
	if len(parts) == 0 || len(parts) > 2 {
		return 0, "", errors.New("invalid query")
	}
	values := make(map[string]string, len(parts))
	for index, part := range parts {
		if part == "" || strings.Count(part, "=") != 1 {
			return 0, "", errors.New("invalid query")
		}
		key, value, _ := strings.Cut(part, "=")
		if (key != "limit" && key != "cursor") || value == "" {
			return 0, "", errors.New("invalid query")
		}
		if _, duplicate := values[key]; duplicate {
			return 0, "", errors.New("invalid query")
		}
		if index == 0 && len(parts) == 2 && key != "limit" {
			return 0, "", errors.New("invalid query")
		}
		values[key] = value
	}
	limit := uint16(0)
	if rawLimit, present := values["limit"]; present {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 ||
			parsed > controlapicontract.MaxControlPageSizeV1 ||
			strconv.Itoa(parsed) != rawLimit {
			return 0, "", errors.New("invalid query")
		}
		limit = uint16(parsed)
	}
	cursor := ""
	if value, present := values["cursor"]; present {
		cursor = value
		if cursor == "" || len(cursor) > MaximumCursorTokenBytesV1 ||
			!rawURLTokenV1(cursor) {
			return 0, "", errors.New("invalid query")
		}
	}
	return limit, cursor, nil
}

func rawURLTokenV1(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

func matchesIfNoneMatchV1(header http.Header, etag string) (bool, error) {
	value, present, err := exactStrongIfNoneMatchV1(header)
	if err != nil || !present {
		return false, err
	}
	return value == etag, nil
}

func exactStrongIfNoneMatchV1(header http.Header) (string, bool, error) {
	values := header.Values("If-None-Match")
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || !validStrongETagV1(values[0]) {
		return "", true, errors.New("invalid If-None-Match")
	}
	return values[0], true, nil
}

func validStrongETagV1(value string) bool {
	if len(value) != 66 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	return moduleapi.ValidSHA256(value[1 : len(value)-1])
}

func responseStrongETagV1(domain, applicationETag string, semanticBody []byte) string {
	payload := make([]byte, 0, len(applicationETag)+1+len(semanticBody))
	payload = append(payload, applicationETag...)
	payload = append(payload, '\n')
	payload = append(payload, semanticBody...)
	digest := moduleapi.Digest(domain, payload)
	clear(payload)
	return `"` + digest + `"`
}

func nilInterfaceV1(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}

func setSecurityHeadersV1(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", controlCSPV1)
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", controlPermissionsPolicyV1)
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func (handler *HandlerV1) nextCorrelationIDV1() string {
	sequence := handler.correlationCount.Add(1)
	return fmt.Sprintf("control-%s-%d", handler.correlationPrefix, sequence)
}
