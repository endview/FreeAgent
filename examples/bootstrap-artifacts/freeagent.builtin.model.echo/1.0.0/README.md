# FreeAgent development Echo model

This package identifies the deterministic, in-process Echo adapter used only
for local S1 bootstrap and recovery tests. Its manifest requests no permission
and provides only the exact `model.generate/v1` Port.

The manifest entrypoint and `implementation/adapter.json` are untrusted
requests. They do not grant trusted execution. The local FreeAgent binary must
independently map the exact ArtifactDigest and its own AdapterIdentity through
the compiled allowlist and exact registry before activation.
