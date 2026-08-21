package exactadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const TextStatsCompletedAssistantTextV1 = "text.stats completed."

var (
	// ErrInvalidEchoProvider identifies a provider that cannot be frozen into
	// the deterministic Echo adapter.
	ErrInvalidEchoProvider = errors.New(
		"exactadapter: invalid deterministic Echo provider",
	)

	// ErrEchoInvocation identifies an invocation that is not the exact
	// model.generate/v1 request and provider frozen into this adapter.
	ErrEchoInvocation = errors.New(
		"exactadapter: deterministic Echo rejected invocation",
	)
)

var modelGeneratePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameModelGenerate,
	ExactVersion: moduleapi.PortVersionV1,
}

// DeterministicEcho is a stateless local model.generate/v1 adapter for tests
// and local vertical-chain validation. It never estimates or fabricates token,
// cache, reasoning, or cost values.
type DeterministicEcho struct {
	provider reusableProviderIdentity
}

var _ modulehost.ModuleInvoker = (*DeterministicEcho)(nil)

// NewDeterministicEcho freezes the complete provider identity accepted and
// returned by the adapter.
func NewDeterministicEcho(
	provider moduleapi.ActivatedModuleRef,
) (*DeterministicEcho, error) {
	identity, err := newReusableProviderIdentity(provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEchoProvider, err)
	}
	switch provider.ExecutionClass {
	case moduleapi.ExecutionDeclarative,
		moduleapi.ExecutionTrustedInProcess:
	default:
		return nil, fmt.Errorf(
			"%w: execution class %q is not supported in S1",
			ErrInvalidEchoProvider,
			provider.ExecutionClass,
		)
	}
	return &DeterministicEcho{provider: identity}, nil
}

// Invoke strictly restores one canonical ModelGenerateRequestV1 and echoes
// the final semantic message as assistant text. Invocation/Run/Attempt IDs,
// deadlines, and timestamps are never added to the output.
func (echo *DeterministicEcho) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if echo == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: adapter is nil",
			ErrEchoInvocation,
		)
	}
	if ctx == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrEchoInvocation,
		)
	}
	if err := ctx.Err(); err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: context: %w",
			ErrEchoInvocation,
			err,
		)
	}
	if prepared.Invocation.Port != modelGeneratePortV1 {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Port must be model.generate/v1",
			ErrEchoInvocation,
		)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     modelGeneratePortV1,
		Bindings: []moduleapi.PortBinding{prepared.Binding},
	})
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: frozen Binding: %v",
			ErrEchoInvocation,
			err,
		)
	}
	provider := plan.Bindings[0].Provider
	if !echo.provider.matches(provider) {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Provider does not match the frozen Echo artifact adapter",
			ErrEchoInvocation,
		)
	}

	request, err := moduleapi.RestoreModelGenerateRequestV1(
		append([]byte(nil), prepared.Invocation.Input...),
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: restore request: %v",
			ErrEchoInvocation,
			err,
		)
	}
	lastMessage := request.Messages[len(request.Messages)-1]
	modelOutput := moduleapi.ModelGenerateOutputV1{
		SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
		AssistantText: lastMessage.Content,
	}
	if hasTextStatsModelActionV1(request.Actions) {
		if strings.HasPrefix(
			lastMessage.Content,
			"UNTRUSTED_ACTION_RESULT_JSON:",
		) {
			modelOutput.AssistantText = TextStatsCompletedAssistantTextV1
		} else if lastUserContent, found := lastModelUserContentV1(
			request.Messages,
		); found {
			rawInput, marshalErr := marshalTextStatsInputV1(lastUserContent)
			if marshalErr != nil {
				return modulehost.InvocationResult{}, fmt.Errorf(
					"%w: marshal text.stats input: %v",
					ErrEchoInvocation,
					marshalErr,
				)
			}
			canonicalInput, inputErr :=
				moduleapi.CanonicalizeAndValidateActionInputV1(
					textStatsInputSchemaV1,
					rawInput,
				)
			if inputErr != nil {
				return modulehost.InvocationResult{}, fmt.Errorf(
					"%w: construct text.stats input: %v",
					ErrEchoInvocation,
					inputErr,
				)
			}
			modelOutput.AssistantText = ""
			modelOutput.ActionRequest = &moduleapi.ModelActionRequestV1{
				ActionID:       TextStatsActionIDV1,
				CanonicalInput: canonicalInput,
			}
		}
	}
	_, output, err := moduleapi.NewModelGenerateOutputV1(modelOutput)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct output: %v",
			ErrEchoInvocation,
			err,
		)
	}
	_, usage, err := moduleapi.NewModelUsageReceiptV1(
		moduleapi.ModelUsageReceiptV1{
			SchemaVersion: moduleapi.ModelUsageReceiptSchemaV1,
			RawReceipt:    json.RawMessage(`null`),
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: construct usage receipt: %v",
			ErrEchoInvocation,
			err,
		)
	}
	return modulehost.InvocationResult{
		Provider:     provider,
		Outcome:      modulehost.InvocationSucceeded,
		Output:       append(json.RawMessage(nil), output...),
		UsageReceipt: append(json.RawMessage(nil), usage...),
	}, nil
}

func hasTextStatsModelActionV1(
	actions []moduleapi.ModelActionDefinitionV1,
) bool {
	for _, action := range actions {
		if action.ActionID == TextStatsActionIDV1 {
			return true
		}
	}
	return false
}

func lastModelUserContentV1(
	messages []moduleapi.ModelMessageV1,
) (string, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == moduleapi.ModelRoleUser {
			return messages[index].Content, true
		}
	}
	return "", false
}

func marshalTextStatsInputV1(text string) (json.RawMessage, error) {
	encoded, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
