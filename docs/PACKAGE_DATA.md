# Runtime data

This directory is reserved for local evaluation data created by the operator.
The release archive contains no database, backup, credential, session, or
Control handoff.

Prefer a separate owner-controlled directory for durable evaluation data. If
you use this directory, create a new child directory per smoke run and never
reuse a database or artifact root from a failed or interrupted run.

Stop `serve` before backup or restore. Never place provider secrets in the
database path, artifact root, backup directory, or Control handoff.
