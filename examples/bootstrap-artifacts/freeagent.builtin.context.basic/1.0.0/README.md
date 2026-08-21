# FreeAgent basic declarative context

This package supplies the small default system context used by the local S1
bootstrap example. It provides only the exact `context.provide/v1` Port and
contains no executable adapter, dependency, requested permission, secret, or
side effect.

The manifest's `DECLARATIVE/static/v1` request grants no authority. FreeAgent
verifies the complete package digest, validates the canonical static-context
entrypoint, assigns the local declarative execution class, and binds the
content under a deny-all authority ceiling before publication.
