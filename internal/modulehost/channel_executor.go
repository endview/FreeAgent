package modulehost

import (
	"context"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// ChannelExecutor is the private effect-bearing surface used only after the
// Core-owned Gateway consumes a persisted CHANNEL_SEND DispatchAttempt permit.
// It is intentionally absent from moduleapi.ChannelTransportV1 and cannot be
// resolved through ordinary model or module invocation.
type ChannelExecutor interface {
	ExecutePrepared(
		context.Context,
		moduleapi.ChannelExecutionRequestV1,
	) (moduleapi.ChannelExecutionResultV1, error)
}
