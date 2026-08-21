package corecontract

// The first Action-capable Loop slice uses one fixed semantic step chain.
// Store and Core Loop both import these identities so callers cannot invent a
// source step or drift across the persistence/runtime boundary.
const (
	PureChatModelLogicalStepIDV1 = "s1.pure-chat.model-generate.1"
	FirstModelLogicalStepIDV1    = "model-1"
	FirstActionLogicalStepIDV1   = "action-1"
	SecondModelLogicalStepIDV1   = "model-2"
	ChannelSendLogicalStepIDV1   = "channel-send-1"
)
