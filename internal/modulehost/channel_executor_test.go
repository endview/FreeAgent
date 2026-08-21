package modulehost

import (
	"context"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type channelExecutorCompileAssertion struct{}

func (channelExecutorCompileAssertion) ExecutePrepared(
	context.Context,
	moduleapi.ChannelExecutionRequestV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	return moduleapi.ChannelExecutionResultV1{}, nil
}

var _ ChannelExecutor = channelExecutorCompileAssertion{}
