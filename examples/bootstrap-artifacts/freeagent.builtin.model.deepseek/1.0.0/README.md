# DeepSeek model adapter artifact

This package requests the exact trusted in-process adapter implemented by
`internal/deepseekmodel`. It contains no API key or endpoint override. Runtime
credentials are resolved separately by the local composition root.

The `model_build_id` values deliberately identify observed public aliases;
they do not claim access to a provider-internal weight build identifier.
