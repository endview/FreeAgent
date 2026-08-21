package exactadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	TextStatsActionIDV1       = "text.stats"
	TextStatsMaxInputRunesV1  = 8192
	TextStatsMaxResultBytesV1 = 256
	textStatsDescriptionV1    = "Count UTF-8 bytes, Unicode code points, whitespace-delimited words, and LF-delimited lines."
)

var (
	ErrInvalidTextStatsProvider = errors.New(
		"exactadapter: invalid text.stats provider",
	)
	ErrTextStatsGenericInvocation = errors.New(
		"exactadapter: text.stats rejects generic invocation",
	)
	ErrTextStatsDescribe = errors.New(
		"exactadapter: text.stats rejected Describe",
	)
	ErrTextStatsPrepare = errors.New(
		"exactadapter: text.stats rejected Prepare",
	)
	ErrTextStatsExecution = errors.New(
		"exactadapter: text.stats rejected execution",
	)
)

var textStatsInputSchemaV1 = json.RawMessage(
	`{"additionalProperties":false,"properties":{"text":{"maxLength":8192,"type":"string"}},"required":["text"],"type":"object"}`,
)

// TextStatsAction is the production deterministic built-in for
// action.provider/v1. Describe and Prepare are read-only; only the private
// ActionExecutor entry point computes a result after Gateway grants a permit.
// The generic ModuleInvoker entry point always rejects without parsing input
// or performing work.
type TextStatsAction struct {
	provider reusableProviderIdentity
}

var _ modulehost.ModuleInvoker = (*TextStatsAction)(nil)
var _ moduleapi.ActionProviderV1 = (*TextStatsAction)(nil)
var _ modulehost.ActionExecutor = (*TextStatsAction)(nil)

func NewTextStatsAction(
	provider moduleapi.ActivatedModuleRef,
) (*TextStatsAction, error) {
	identity, err := newReusableProviderIdentity(provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTextStatsProvider, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return nil, fmt.Errorf(
			"%w: execution class must be %s",
			ErrInvalidTextStatsProvider,
			moduleapi.ExecutionTrustedInProcess,
		)
	}
	return &TextStatsAction{provider: identity}, nil
}

// Invoke deliberately cannot reach Describe, Prepare or ExecutePrepared.
// Generic module dispatch therefore cannot bypass the Action Gateway.
func (*TextStatsAction) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, ErrTextStatsGenericInvocation
}

func (adapter *TextStatsAction) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	if err := validateTextStatsContext(adapter, ctx, ErrTextStatsDescribe); err != nil {
		return nil, err
	}
	frozenRequest, _, err := moduleapi.NewActionDescribeRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTextStatsDescribe, err)
	}
	if !bytes.Equal(frozenRequest.Parameters, []byte(`{}`)) {
		return nil, fmt.Errorf(
			"%w: parameters must be an empty object",
			ErrTextStatsDescribe,
		)
	}
	definition, _, err := moduleapi.NewActionDefinitionV1(
		moduleapi.ActionDefinitionV1{
			ProviderActionID:        TextStatsActionIDV1,
			Description:             textStatsDescriptionV1,
			InputSchema:             bytes.Clone(textStatsInputSchemaV1),
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%w: definition: %v", ErrTextStatsDescribe, err)
	}
	return []moduleapi.ActionDefinitionV1{cloneTextStatsDefinition(definition)}, nil
}

func (adapter *TextStatsAction) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if err := validateTextStatsContext(adapter, ctx, ErrTextStatsPrepare); err != nil {
		return nil, err
	}
	frozenRequest, _, err := moduleapi.NewActionRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTextStatsPrepare, err)
	}
	if frozenRequest.ProviderActionID != TextStatsActionIDV1 {
		return nil, fmt.Errorf(
			"%w: provider_action_id must be %q",
			ErrTextStatsPrepare,
			TextStatsActionIDV1,
		)
	}
	if err := moduleapi.ValidateActionInputV1(
		textStatsInputSchemaV1,
		frozenRequest.CanonicalInput,
	); err != nil {
		return nil, fmt.Errorf("%w: input: %v", ErrTextStatsPrepare, err)
	}
	prepared, err := moduleapi.CanonicalizeActionPreparedPayloadV1(
		frozenRequest.CanonicalInput,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrTextStatsPrepare, err)
	}
	return bytes.Clone(prepared), nil
}

func (adapter *TextStatsAction) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if err := validateTextStatsContext(adapter, ctx, ErrTextStatsExecution); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	frozenExecution, err := modulehost.NewPreparedActionExecutionV1(execution)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: execution closure: %v",
			ErrTextStatsExecution,
			err,
		)
	}
	if !adapter.provider.matches(frozenExecution.Binding.Provider) {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: Provider does not match the frozen text.stats artifact adapter",
			ErrTextStatsExecution,
		)
	}
	frozenRequest := frozenExecution.Request
	if frozenRequest.ProviderActionID != TextStatsActionIDV1 {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: provider_action_id must be %q",
			ErrTextStatsExecution,
			TextStatsActionIDV1,
		)
	}
	if err := moduleapi.ValidateActionInputV1(
		textStatsInputSchemaV1,
		frozenRequest.PreparedPayload,
	); err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: prepared payload: %v",
			ErrTextStatsExecution,
			err,
		)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(frozenRequest.PreparedPayload, &payload); err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: decode prepared payload: %v",
			ErrTextStatsExecution,
			err,
		)
	}
	resultCanonical, err := canonicalTextStatsResultV1(payload.Text)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: result: %v",
			ErrTextStatsExecution,
			err,
		)
	}
	result, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:       frozenRequest.AttemptID,
			Outcome:         moduleapi.ActionExecutionSucceeded,
			CanonicalResult: resultCanonical,
		},
	)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: construct result: %v",
			ErrTextStatsExecution,
			err,
		)
	}
	return cloneTextStatsExecutionResult(result), nil
}

func validateTextStatsContext(
	adapter *TextStatsAction,
	ctx context.Context,
	kind error,
) error {
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

func canonicalTextStatsResultV1(text string) (json.RawMessage, error) {
	lines := uint64(0)
	if text != "" {
		lines = uint64(strings.Count(text, "\n") + 1)
	}
	encoded, err := json.Marshal(struct {
		Bytes uint64 `json:"bytes"`
		Runes uint64 `json:"runes"`
		Words uint64 `json:"words"`
		Lines uint64 `json:"lines"`
	}{
		Bytes: uint64(len(text)),
		Runes: uint64(utf8.RuneCountInString(text)),
		Words: uint64(len(strings.Fields(text))),
		Lines: lines,
	})
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, err
	}
	if len(canonical) > TextStatsMaxResultBytesV1 {
		return nil, fmt.Errorf(
			"canonical result exceeds %d bytes",
			TextStatsMaxResultBytesV1,
		)
	}
	return bytes.Clone(canonical), nil
}

func cloneTextStatsDefinition(
	definition moduleapi.ActionDefinitionV1,
) moduleapi.ActionDefinitionV1 {
	cloned := definition
	cloned.InputSchema = bytes.Clone(definition.InputSchema)
	return cloned
}

func cloneTextStatsExecutionResult(
	result moduleapi.ActionExecutionResultV1,
) moduleapi.ActionExecutionResultV1 {
	cloned := result
	cloned.CanonicalResult = bytes.Clone(result.CanonicalResult)
	cloned.ProviderReceipt = bytes.Clone(result.ProviderReceipt)
	return cloned
}
