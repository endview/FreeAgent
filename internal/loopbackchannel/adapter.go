// Package loopbackchannel implements the first-party, loopback-only HTTP
// adapter for channel.transport/v1. It owns no listener, Store, Run or retry
// worker; a trusted composition boundary may call DecodeInbound, while only
// the private Core Gateway may call ExecutePrepared.
package loopbackchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdapterIdentityV1 = "freeagent.adapter.channel.loopback-http/v1"
)

var (
	ErrInvalidAdapter    = errors.New("loopbackchannel: invalid adapter")
	ErrGenericInvocation = errors.New("loopbackchannel: generic invocation is forbidden")
	ErrInboundDecode     = errors.New("loopbackchannel: inbound request rejected")
	ErrPrepareSend       = errors.New("loopbackchannel: prepare send rejected")
	ErrExecute           = errors.New("loopbackchannel: execution rejected before dispatch")
)

// Adapter is immutable after construction. It stores the frozen non-secret
// Endpoint target plus a SecretRef, and resolves secret material for one
// inbound or outbound operation only.
type Adapter struct {
	provider       moduleapi.ActivatedModuleRef
	endpointID     string
	accountID      string
	conversationID string
	secretRef      string
	parameters     parametersV1
	outboundURL    *url.URL
	resolver       SecretResolver
	client         *http.Client
}

var _ modulehost.ModuleInvoker = (*Adapter)(nil)
var _ moduleapi.ChannelTransportV1 = (*Adapter)(nil)
var _ modulehost.ChannelExecutor = (*Adapter)(nil)

func New(
	provider moduleapi.ActivatedModuleRef,
	endpointID string,
	accountID string,
	conversationID string,
	config moduleapi.ChannelBindingConfigV1,
	resolver SecretResolver,
) (*Adapter, error) {
	if err := provider.Validate(); err != nil {
		return nil, fmt.Errorf("%w: provider: %v", ErrInvalidAdapter, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != AdapterIdentityV1 {
		return nil, fmt.Errorf("%w: provider is not the exact trusted loopback adapter", ErrInvalidAdapter)
	}
	if err := validateOpaqueValue("endpoint_id", endpointID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	if err := validateOpaqueValue("account_id", accountID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	if err := validateOpaqueValue("conversation_id", conversationID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	if isNilDependency(resolver) {
		return nil, fmt.Errorf("%w: secret resolver is nil", ErrInvalidAdapter)
	}
	frozenConfig, _, err := moduleapi.NewChannelBindingConfigV1(config)
	if err != nil {
		return nil, fmt.Errorf("%w: config: %v", ErrInvalidAdapter, err)
	}
	if frozenConfig.AdapterProtocol != AdapterProtocolV1 {
		return nil, fmt.Errorf("%w: adapter_protocol must be %q", ErrInvalidAdapter, AdapterProtocolV1)
	}
	parameters, err := restoreParameters(frozenConfig.Parameters)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	outboundURL, err := parseLoopbackEndpoint(parameters.OutboundURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	client, err := newLoopbackHTTPClient(time.Duration(parameters.RequestTimeoutMS) * time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	return &Adapter{
		provider:       provider,
		endpointID:     endpointID,
		accountID:      accountID,
		conversationID: conversationID,
		secretRef:      frozenConfig.SecretRef,
		parameters:     parameters,
		outboundURL:    cloneURL(outboundURL),
		resolver:       resolver,
		client:         client,
	}, nil
}

// InboundPath exposes only the non-secret exact mount path frozen at
// construction. It performs no resolution or I/O and lets the trusted
// composition root mount this endpoint without reparsing provider config.
func (adapter *Adapter) InboundPath() string {
	if adapter == nil {
		return ""
	}
	return adapter.parameters.InboundPath
}

func (*Adapter) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, ErrGenericInvocation
}

// PrepareSend is pure and performs no secret resolution or network I/O.
func (adapter *Adapter) PrepareSend(
	ctx context.Context,
	request moduleapi.ChannelPrepareSendRequestV1,
) (json.RawMessage, error) {
	if err := validateAdapterContext(adapter, ctx, ErrPrepareSend); err != nil {
		return nil, err
	}
	frozen, _, err := moduleapi.NewChannelPrepareSendRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrPrepareSend, err)
	}
	if frozen.EndpointID != adapter.endpointID {
		return nil, fmt.Errorf("%w: endpoint identity does not match adapter", ErrPrepareSend)
	}
	if err := adapter.validateReplyTarget(frozen.ReplyTarget); err != nil {
		return nil, fmt.Errorf("%w: reply target does not match frozen Endpoint", ErrPrepareSend)
	}
	payload, err := canonicalWire(preparedPayloadV1{
		SchemaVersion: preparedPayloadSchemaV1,
		Message:       frozen.AssistantText,
	}, moduleapi.MaxChannelPreparedPayloadBytesV1)
	if err != nil {
		return nil, fmt.Errorf("%w: prepared payload: %v", ErrPrepareSend, err)
	}
	prepared, err := moduleapi.CanonicalizeChannelPreparedPayloadV1(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: prepared payload: %v", ErrPrepareSend, err)
	}
	return bytes.Clone(prepared), nil
}

// DecodeInbound authenticates and strictly normalizes one already-accepted
// HTTP request. It does not route, admit a Run, mutate Cursor state or start a
// listener. Invalid authentication and malformed wires return no envelope.
func (adapter *Adapter) DecodeInbound(
	ctx context.Context,
	request *http.Request,
) (moduleapi.ChannelInboundEnvelopeV1, error) {
	if err := validateAdapterContext(adapter, ctx, ErrInboundDecode); err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, err
	}
	if err := validateInboundHTTPRequest(request, adapter.parameters.InboundPath); err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: %v", ErrInboundDecode, err)
	}
	resolved, err := resolveSecret(ctx, adapter.resolver, adapter.secretRef)
	if err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: authentication unavailable", ErrInboundDecode)
	}
	secret := resolved
	defer clear(secret)
	if err := authenticateBearer(request.Header.Values("Authorization"), secret); err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: authentication failed", ErrInboundDecode)
	}
	body, err := readBoundedAndClose(request.Body, maximumInboundBytes)
	if err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: body is unavailable or exceeds its bound", ErrInboundDecode)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		body,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumInboundBytes,
			MaxDepth: 64,
			MaxNodes: maximumInboundBytes,
		},
	)
	if err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: malformed JSON", ErrInboundDecode)
	}
	var wire inboundWireV1
	if err := decodeStrictCanonical(canonical, maximumInboundBytes, &wire); err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: malformed wire", ErrInboundDecode)
	}
	if wire.SchemaVersion != inboundWireSchemaV1 || wire.EndpointID != adapter.endpointID {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: wire identity does not match adapter", ErrInboundDecode)
	}
	if err := adapter.validateReplyTarget(wire.ReplyTarget); err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf(
			"%w: reply target does not match frozen Endpoint",
			ErrInboundDecode,
		)
	}
	frozen, _, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      wire.EndpointID,
			ProviderEventID: wire.ProviderEventID,
			ExternalUserID:  wire.ExternalUserID,
			Message:         wire.Message,
			ReplyTarget:     bytes.Clone(wire.ReplyTarget),
			CursorBefore:    bytes.Clone(wire.CursorBefore),
			CursorAfter:     bytes.Clone(wire.CursorAfter),
		},
	)
	if err != nil {
		return moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf("%w: envelope: %v", ErrInboundDecode, err)
	}
	return cloneInboundEnvelope(frozen), nil
}

func (adapter *Adapter) validateReplyTarget(canonical json.RawMessage) error {
	var target replyTargetV1
	if err := decodeStrictCanonical(
		canonical,
		moduleapi.MaxChannelReplyTargetBytesV1,
		&target,
	); err != nil {
		return err
	}
	if target.AccountID != adapter.accountID ||
		target.ConversationID != adapter.conversationID {
		return errors.New("reply target identity differs")
	}
	if err := validateOpaqueValue("account_id", target.AccountID); err != nil {
		return err
	}
	if err := validateOpaqueValue("conversation_id", target.ConversationID); err != nil {
		return err
	}
	return nil
}

func validateInboundHTTPRequest(request *http.Request, expectedPath string) error {
	if request == nil || request.Body == nil || request.URL == nil {
		return errors.New("request is incomplete")
	}
	if request.Method != http.MethodPost {
		return errors.New("method must be POST")
	}
	if request.URL.Path != expectedPath || request.URL.RawPath != "" ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" {
		return errors.New("request target does not match the exact inbound path")
	}
	if request.URL.Scheme != "" || request.URL.Host != "" || request.RequestURI != expectedPath {
		return errors.New("absolute-form or non-canonical request targets are forbidden")
	}
	if !literalLoopbackAuthority(request.Host) || !literalLoopbackRemote(request.RemoteAddr) {
		return errors.New("request is not an end-to-end loopback request")
	}
	for _, header := range []string{
		"Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
	} {
		if len(request.Header.Values(header)) != 0 {
			return errors.New("forwarded requests are forbidden")
		}
	}
	if len(request.Header.Values("Content-Type")) != 1 {
		return errors.New("Content-Type must appear exactly once")
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		return errors.New("Content-Type must be application/json without parameters")
	}
	if request.Header.Get("Content-Encoding") != "" {
		return errors.New("Content-Encoding is forbidden")
	}
	if request.ContentLength > maximumInboundBytes {
		return errors.New("request body exceeds bound")
	}
	return nil
}

func literalLoopbackAuthority(authority string) bool {
	if authority == "" || strings.Contains(authority, "@") {
		return false
	}
	parsed, err := url.Parse("http://" + authority + "/")
	if err != nil || parsed.Host != authority || parsed.User != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.Contains(host, "%") {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func literalLoopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil || strings.Contains(host, "%") {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAdapterContext(adapter *Adapter, ctx context.Context, kind error) error {
	if adapter == nil {
		return fmt.Errorf("%w: adapter is nil", kind)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", kind)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context: %v", kind, err)
	}
	return nil
}

func validateOpaqueValue(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf("%s must be a canonical opaque ID", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func cloneInboundEnvelope(value moduleapi.ChannelInboundEnvelopeV1) moduleapi.ChannelInboundEnvelopeV1 {
	value.ReplyTarget = bytes.Clone(value.ReplyTarget)
	value.CursorBefore = bytes.Clone(value.CursorBefore)
	value.CursorAfter = bytes.Clone(value.CursorAfter)
	return value
}

func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// singleUseReader deliberately has no rewind method. Together with a nil
// Request.GetBody this prevents net/http from treating the POST as replayable.
type singleUseReader struct {
	reader io.Reader
}

func (reader *singleUseReader) Read(value []byte) (int, error) {
	return reader.reader.Read(value)
}
