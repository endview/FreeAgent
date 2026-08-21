# FreeAgent deterministic text statistics Action

This package requests the built-in `text.stats` implementation for the exact
`action.provider/v1` Port. It counts UTF-8 bytes, Unicode code points, words
and lines without network, filesystem, secret or external effects.

The manifest and adapter request do not grant trusted execution. The local
FreeAgent binary must independently match the exact module, version, artifact
digest and compiled adapter identity before activation. Model-visible Action
definitions are frozen only when a selected Profile binds this module.
