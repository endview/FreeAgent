# Zhipu GLM model adapter artifact

This package requests the exact trusted in-process adapter implemented by
`internal/zhipumodel`. It contains no API key or endpoint override. Runtime
credentials are resolved separately by the local composition root.

The single `model_build_id` identifies an observed public alias. It does not
claim access to a provider-internal weight build identifier, and it does not
authorize other models returned by the provider's model-list endpoint.
