# Contributing

FreeAgent is a local harness, not a module-governance platform. Changes should
improve a concrete task: configure, converse, use tools, inspect, stop or recover.
Keep the implementation small and retain current-run correctness.

- One CLI and execution service; no parallel legacy runtime or data migration.
- Persist dispatch before external execution, then results before dependent work.
- Never implicitly replay an unknown operation or fabricate a success result.
- Keep tool approval, limits and configuration checks at the execution boundary.
- Preserve conversation isolation and complete tool-call/result groups.
- Disabled MCP programs and offline diagnostics must not start external code.
- Test with synthetic data. Real-provider claims require opt-in live evidence.

Before opening a change:

```text
gofmt -w <changed-go-files>
go mod verify
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
pnpm --dir internal/chatui install --frozen-lockfile
pnpm --dir internal/chatui test
pnpm --dir internal/chatui build
```

Add focused failure tests for changes to persistence, cancellation, file access,
dispatch or recovery. Update current user docs, not a compatibility matrix.
Do not commit keys, user conversations, databases, backups, binaries or caches.
Preserve required dependency licenses and notices. Contributions use the
repository's AGPL-3.0-only license.
After changing compiled dependencies, regenerate `THIRD_PARTY_NOTICES.md` with
`./scripts/update-notices.ps1` in PowerShell, review the license text, and include it with
distributed binaries.
The notices script also includes the installed locked frontend production
dependency closure; build the Chat assets first and use `-Check` in CI.
Node 24 and pnpm 11.25.0 match the current frontend gate. Browser checks use
synthetic workspaces and deterministic Providers, never a user's project.
Run `./scripts/smoke.ps1` against a built Windows CLI for offline conversation,
deduplication and backup/restore acceptance. It requires a fresh temporary root
and retains that root for inspection.
