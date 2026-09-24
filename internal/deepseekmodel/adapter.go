// Package deepseekmodel implements the trusted in-process DeepSeek
// model.generate/v2 adapter. It owns no Store, scheduler, retry loop, or
// credential value. The trusted composition boundary injects one exact
// provider identity, a bounded model-build allowlist, a credential resolver,
// and (optionally) an HTTP client.
package deepseekmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdapterIdentityV1 = "freeagent.adapter.model.deepseek/v1"
	ProviderNameV1    = "deepseek"

	ModelV4Flash = "deepseek-v4-flash"
	ModelV4Pro   = "deepseek-v4-pro"
	// MaximumOutputTokensV1 is the current public DeepSeek v4 output limit.
	// Keeping the bound in the exact adapter prevents an artifact schema or
	// caller from expanding provider authority at runtime.
	MaximumOutputTokensV1 = 384000

	officialChatCompletionsURL = "https://api.deepseek.com/chat/completions"
	maximumResolvedAPIKeyBytes = 4096
	maximumResponseBytes       = 8 << 20
)

var (
	ErrInvalidAdapter = errors.New("deepseekmodel: invalid adapter")
	ErrInvocation     = errors.New(
		"deepseekmodel: invocation rejected before dispatch",
	)
	ErrAPIKeyResolve = errors.New("deepseekmodel: API key resolution failed")
)

var modelGeneratePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameModelGenerate,
	ExactVersion: moduleapi.PortVersionV2,
}

// APIKeyIdentity contains only frozen, non-secret model identity. A resolver
// may use it to select a separately configured credential, but it must not
// derive credential selection from Run, Member, Invocation, or task data.
type APIKeyIdentity struct {
	Provider     string
	Model        string
	ModelBuildID string
	SecretRef    string
}

// APIKeyResolver is injected by the trusted composition boundary. The
// adapter resolves a fresh owned copy for one dispatch and clears it before
// returning. Resolver diagnostics are never propagated because they may
// accidentally contain credential material.
type APIKeyResolver interface {
	ResolveAPIKey(context.Context, APIKeyIdentity) ([]byte, error)
}

// APIKeyResolverFunc adapts a function to APIKeyResolver.
type APIKeyResolverFunc func(
	context.Context,
	APIKeyIdentity,
) ([]byte, error)

func (resolve APIKeyResolverFunc) ResolveAPIKey(
	ctx context.Context,
	identity APIKeyIdentity,
) ([]byte, error) {
	return resolve(ctx, identity)
}

// Options are trusted process configuration, not module-authored Binding
// parameters. AllowedModelBuildIDs maps each public model alias to the exact,
// honestly labelled alias observation frozen by the composition root.
type Options struct {
	Provider             moduleapi.ActivatedModuleRef
	AllowedModelBuildIDs map[string]string
	APIKeyResolver       APIKeyResolver
	HTTPClient           *http.Client
}

// Adapter is immutable after construction. It stores no API key and exposes
// no provider search, fallback, or retry behavior.
type Adapter struct {
	provider             reusableProviderIdentity
	allowedModelBuildIDs map[string]string
	apiKeyResolver       APIKeyResolver
	client               *http.Client
}

// ValidateBindingConfigV1 applies the exact provider, model-build and
// generation-parameter checks used by the trusted adapter without resolving a
// credential or performing network I/O. Control-plane candidates use it
// before publishing a model.generate/v2 Binding.
func ValidateBindingConfigV1(
	config moduleapi.ModelBindingConfigV2,
	allowedModelBuildIDs map[string]string,
) error {
	if config.Provider != ProviderNameV1 {
		return fmt.Errorf("%w: provider is not DeepSeek", ErrInvocation)
	}
	allowed, err := freezeAllowedModelBuildIDs(allowedModelBuildIDs)
	if err != nil {
		return fmt.Errorf("%w: invalid model build policy: %v", ErrInvocation, err)
	}
	expectedBuild, found := allowed[config.Model]
	if !found || expectedBuild != config.ModelBuildID {
		return fmt.Errorf("%w: model build is not in the exact allowlist", ErrInvocation)
	}
	if _, err := validateDeepSeekParameters(config.Parameters); err != nil {
		return fmt.Errorf("%w: model parameters: %v", ErrInvocation, err)
	}
	return nil
}

var _ modulehost.ModuleInvoker = (*Adapter)(nil)

// New freezes one exact artifact adapter and its permitted public model-build
// identities. Only the official DeepSeek endpoint can be addressed; tests
// substitute a RoundTripper while the request URL remains official.
func New(options Options) (*Adapter, error) {
	identity, err := newReusableProviderIdentity(options.Provider)
	if err != nil {
		return nil, fmt.Errorf("%w: provider: %v", ErrInvalidAdapter, err)
	}
	if options.Provider.ExecutionClass !=
		moduleapi.ExecutionTrustedInProcess ||
		options.Provider.AdapterIdentity != AdapterIdentityV1 {
		return nil, fmt.Errorf(
			"%w: provider is not the exact trusted DeepSeek adapter",
			ErrInvalidAdapter,
		)
	}
	allowed, err := freezeAllowedModelBuildIDs(options.AllowedModelBuildIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	if isNilDependency(options.APIKeyResolver) {
		return nil, fmt.Errorf("%w: API key resolver is nil", ErrInvalidAdapter)
	}
	client := freezeHTTPClient(options.HTTPClient)
	return &Adapter{
		provider:             identity,
		allowedModelBuildIDs: allowed,
		apiKeyResolver:       options.APIKeyResolver,
		client:               client,
	}, nil
}

// Invoke validates the complete pre-dispatch closure before issuing exactly
// one POST. Once client.Do is entered, certainty is returned only through the
// explicit InvocationResult outcome: transport ambiguity is UNKNOWN, an
// explicit HTTP/provider rejection is FAILED, and only a strictly normalized
// 2xx response is SUCCEEDED.
func (adapter *Adapter) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	provider, modelConfig, request, parameters, err :=
		adapter.prepareInvocation(ctx, prepared)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	secretRef, err := modelSecretRefForInvocationV1(
		prepared.AuthorityCanonical,
		modelConfig.Provider,
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: model authority: %v",
			ErrInvocation,
			err,
		)
	}

	body, err := buildChatCompletionsRequest(
		modelConfig.Model,
		request.Messages,
		parameters,
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct request body: %v",
			ErrInvocation,
			err,
		)
	}
	key, err := resolveAPIKey(
		ctx,
		adapter.apiKeyResolver,
		APIKeyIdentity{
			Provider:     modelConfig.Provider,
			Model:        modelConfig.Model,
			ModelBuildID: modelConfig.ModelBuildID,
			SecretRef:    secretRef,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: %w",
			ErrInvocation,
			ErrAPIKeyResolve,
		)
	}
	defer clear(key)
	if err := ctx.Err(); err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context before dispatch: %w",
			ErrInvocation,
			err,
		)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		officialChatCompletionsURL,
		&singleUseReader{reader: bytes.NewReader(body)},
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct HTTP request",
			ErrInvocation,
		)
	}
	httpRequest.ContentLength = int64(len(body))
	httpRequest.GetBody = nil
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+string(key))

	response, callErr := adapter.client.Do(httpRequest) // exactly one POST
	httpRequest.Header.Del("Authorization")
	if callErr != nil {
		closeResponse(response)
		return unknownInvocationOutcome(
			provider,
			modulehost.UnknownClassInvokeReturnedError,
		), nil
	}
	if response == nil {
		return unknownInvocationOutcome(
			provider,
			modulehost.UnknownClassNoUsableResponse,
		), nil
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode > http.StatusMultipleChoices-1 {
		closeResponse(response)
		return invocationOutcome(provider, modulehost.InvocationFailed), nil
	}
	if response.ContentLength > maximumResponseBytes {
		closeResponse(response)
		return invocationOutcome(provider, modulehost.InvocationFailed), nil
	}

	responseBody, readOutcome, unknownClass := readResponseBody(response)
	switch readOutcome {
	case modulehost.InvocationSucceeded:
		// Continue with strict provider normalization.
	case modulehost.InvocationFailed:
		return invocationOutcome(provider, readOutcome), nil
	case modulehost.InvocationUnknown:
		return unknownInvocationOutcome(provider, unknownClass), nil
	default:
		return unknownInvocationOutcome(
			provider,
			modulehost.UnknownClassNoUsableResponse,
		), nil
	}
	output, usage, err := normalizeChatCompletionsResponse(
		response.Header,
		responseBody,
		modelConfig.Model,
	)
	if err != nil {
		// The provider returned a complete 2xx response, but it did not satisfy
		// the frozen adapter contract. That is a known failed invocation, not a
		// reason to replay the semantic model call.
		return invocationOutcome(provider, modulehost.InvocationFailed), nil
	}
	return modulehost.InvocationResult{
		Provider:     provider,
		Outcome:      modulehost.InvocationSucceeded,
		Output:       append(json.RawMessage(nil), output...),
		UsageReceipt: append(json.RawMessage(nil), usage...),
	}, nil
}

var legacyDenyAllModelAuthorityV1 = []byte(
	`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`,
)

func modelSecretRefForInvocationV1(
	canonical []byte,
	provider string,
) (string, error) {
	// Existing bootstrap stores used this exact inert authority before E4.
	// It remains a compatibility identity only; every newly applied Model
	// binding must use model-authority-ceiling/v1 and therefore returns an
	// explicit SecretRef below.
	if bytes.Equal(canonical, legacyDenyAllModelAuthorityV1) {
		return "", nil
	}
	authority, err := moduleapi.RestoreModelAuthorityCeilingV1(canonical)
	if err != nil {
		return "", err
	}
	if authority.Provider != provider ||
		!authority.AllowOfficialProviderEndpoint {
		return "", errors.New("authority does not permit the exact provider endpoint")
	}
	return authority.SecretRef, nil
}

func (adapter *Adapter) prepareInvocation(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (
	moduleapi.ActivatedModuleRef,
	moduleapi.ModelBindingConfigV2,
	moduleapi.ModelGenerateRequestV1,
	validatedParameters,
	error,
) {
	if adapter == nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: adapter is nil", ErrInvocation)
	}
	if ctx == nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: context is nil", ErrInvocation)
	}
	if err := ctx.Err(); err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: context: %w", ErrInvocation, err)
	}
	if prepared.Invocation.Port != modelGeneratePortV1 {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: Port must be model.generate/v2", ErrInvocation)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     modelGeneratePortV1,
		Bindings: []moduleapi.PortBinding{prepared.Binding},
	})
	if err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: frozen Binding: %v", ErrInvocation, err)
	}
	provider := plan.Bindings[0].Provider
	if !adapter.provider.matches(provider) {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: Provider does not match adapter", ErrInvocation)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV2(
		append([]byte(nil), prepared.ConfigCanonical...),
	)
	if err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: restore model Binding config: %v", ErrInvocation, err)
	}
	if err := adapter.validateModelConfig(modelConfig); err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: %v", ErrInvocation, err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		append([]byte(nil), prepared.Invocation.Input...),
	)
	if err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: restore request: %v", ErrInvocation, err)
	}
	if len(request.Actions) != 0 {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: Actions are not supported by adapter v1", ErrInvocation)
	}
	configParameters, err := validateDeepSeekParameters(modelConfig.Parameters)
	if err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: frozen model parameters: %v", ErrInvocation, err)
	}
	requestParameters, err := validateDeepSeekParameters(request.Parameters)
	if err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: request parameters: %v", ErrInvocation, err)
	}
	if err := validateParameterTightening(
		configParameters,
		requestParameters,
	); err != nil {
		return moduleapi.ActivatedModuleRef{},
			moduleapi.ModelBindingConfigV2{},
			moduleapi.ModelGenerateRequestV1{}, nil,
			fmt.Errorf("%w: request parameters: %v", ErrInvocation, err)
	}
	return provider, modelConfig, request, requestParameters, nil
}

func (adapter *Adapter) validateModelConfig(
	config moduleapi.ModelBindingConfigV2,
) error {
	if config.Provider != ProviderNameV1 {
		return fmt.Errorf("provider must be %q", ProviderNameV1)
	}
	expectedBuild, present := adapter.allowedModelBuildIDs[config.Model]
	if !present || expectedBuild != config.ModelBuildID {
		return fmt.Errorf("model and model_build_id are not in the trust allowlist")
	}
	return nil
}

type reusableProviderIdentity struct {
	moduleID        string
	version         string
	artifactDigest  string
	executionClass  moduleapi.ExecutionClass
	adapterIdentity string
}

func newReusableProviderIdentity(
	provider moduleapi.ActivatedModuleRef,
) (reusableProviderIdentity, error) {
	if err := provider.Validate(); err != nil {
		return reusableProviderIdentity{}, err
	}
	return reusableProviderIdentity{
		moduleID:        provider.ModuleID,
		version:         provider.Version,
		artifactDigest:  provider.ArtifactDigest,
		executionClass:  provider.ExecutionClass,
		adapterIdentity: provider.AdapterIdentity,
	}, nil
}

func (identity reusableProviderIdentity) matches(
	provider moduleapi.ActivatedModuleRef,
) bool {
	if err := provider.Validate(); err != nil {
		return false
	}
	return provider.ModuleID == identity.moduleID &&
		provider.Version == identity.version &&
		provider.ArtifactDigest == identity.artifactDigest &&
		provider.ExecutionClass == identity.executionClass &&
		provider.AdapterIdentity == identity.adapterIdentity
}

func freezeAllowedModelBuildIDs(
	input map[string]string,
) (map[string]string, error) {
	if len(input) == 0 || len(input) > 2 {
		return nil, fmt.Errorf("allowed model-build map must contain 1 or 2 entries")
	}
	frozen := make(map[string]string, len(input))
	for model, buildID := range input {
		switch model {
		case ModelV4Flash, ModelV4Pro:
		default:
			return nil, fmt.Errorf("unsupported model alias %q", model)
		}
		candidate := moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      ProviderNameV1,
			Model:         model,
			ModelBuildID:  buildID,
			Parameters:    json.RawMessage(`{}`),
		}
		if _, _, err := moduleapi.NewModelBindingConfigV2(candidate); err != nil {
			return nil, fmt.Errorf("invalid model-build allowlist entry: %v", err)
		}
		frozen[model] = buildID
	}
	return frozen, nil
}

func freezeHTTPClient(input *http.Client) *http.Client {
	if input == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		input = &http.Client{Transport: transport}
	}
	frozen := *input
	frozen.CheckRedirect = func(*http.Request, []*http.Request) error {
		// Preserve the first response so Invoke can classify every 3xx as an
		// explicit FAILED outcome without issuing a second request.
		return http.ErrUseLastResponse
	}
	return &frozen
}

func resolveAPIKey(
	ctx context.Context,
	resolver APIKeyResolver,
	identity APIKeyIdentity,
) ([]byte, error) {
	if ctx == nil || isNilDependency(resolver) {
		return nil, ErrAPIKeyResolve
	}
	resolved, err := resolver.ResolveAPIKey(ctx, identity)
	if err != nil {
		return nil, ErrAPIKeyResolve
	}
	if len(resolved) == 0 || len(resolved) > maximumResolvedAPIKeyBytes {
		return nil, ErrAPIKeyResolve
	}
	owned := append([]byte(nil), resolved...)
	for _, character := range owned {
		if character < 0x21 || character > 0x7e {
			clear(owned)
			return nil, ErrAPIKeyResolve
		}
	}
	return owned, nil
}

func invocationOutcome(
	provider moduleapi.ActivatedModuleRef,
	outcome modulehost.InvocationOutcome,
) modulehost.InvocationResult {
	return modulehost.InvocationResult{
		Provider: provider,
		Outcome:  outcome,
	}
}

func unknownInvocationOutcome(
	provider moduleapi.ActivatedModuleRef,
	class modulehost.InvocationUnknownClass,
) modulehost.InvocationResult {
	return modulehost.InvocationResult{
		Provider:     provider,
		Outcome:      modulehost.InvocationUnknown,
		UnknownClass: class,
	}
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}

type singleUseReader struct {
	reader *bytes.Reader
}

func (reader *singleUseReader) Read(target []byte) (int, error) {
	return reader.reader.Read(target)
}
