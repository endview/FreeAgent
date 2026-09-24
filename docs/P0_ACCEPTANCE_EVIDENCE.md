# P0 Acceptance Evidence

Status: `P0_DONE_DEVELOPMENT_SCOPE`

Recorded: 2026-09-22 (Asia/Shanghai)

Branch: `codex/remove-money-budget`

This record covers the P0 money-budget removal candidate after the FAC2
rebuild. It is evidence for the current working tree and does not authorize a
push, tag, public release, or `RELEASE_READY` decision.

## D0: Baseline freeze

Status: `PASS`

- The FAC1 source, tool, and backup identities remain protected by
  [`P0_BASELINE_FREEZE`](P0_BASELINE_FREEZE.md).
- The FAC1 bootstrap migration remains byte-frozen and is not used as a
  FAC1-to-FAC2 runtime migration.
- The new runtime rejects FAC1 by `application_id` without modifying the
  database. The focused test is
  `TestFAC1StoreIsRejectedReadOnlyWithoutModification`.

## D1: Contracts and usage

Status: `PASS`

- Money-bearing runtime contracts and provider/action/channel money gates were
  removed. Token usage, output limits, deadlines, permissions, durable
  Attempts, UNKNOWN, and exact retry remain in the active contracts.
- `Test-MoneyBanList.ps1` passed:
  `MONEY_BAN_GATE_OK files=2140 tokens=26 exemptions=19`.
- The denylist is wired into `release-gate.ps1` as a permanent gate and has a
  negative canary test for reintroduced money identifiers.

## D2: Store and execution

Status: `PASS`

Current FAC2 identity:

| Fact | Value |
| --- | --- |
| Schema identity | `github.com/endview/freeagent/current-store-v2` |
| Application ID | `1178682162` (`FAC2`) |
| UserVersion | `1` |
| Tables / explicit indexes / triggers | `42 / 25 / 64` |
| Schema fingerprint | `9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561` |
| Migration | `internal/currentstore/migrations/fac2/0001_current.sql` |
| Migration bytes / SHA-256 | `149239 / dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a` |

Focused current-store and backup tests passed, including schema/fingerprint
freezes, explicit migration fencing, FAC1 rejection, and read-only
verification.

## D3: Consumers and configuration

Status: `PASS`

- `go build ./...` passed.
- The full Go regression passed with the two long 50-turn tests isolated:
  `go test ./... -count=1 -timeout=60m -skip
  '^(TestRunConversationFiftyTurnsAcrossReentryBackupRestore|TestRunConversationFiftyTurnsAcrossNormalProcessRestart)$'`.
- Both isolated tests passed independently:
  `TestRunConversationFiftyTurnsAcrossNormalProcessRestart` and
  `TestRunConversationFiftyTurnsAcrossReentryBackupRestore`.
- `Test-CapabilityMatrix.ps1` passed with 49 declared items.
- Updated FAC2 seeds and 2.0.0 bootstrap artifacts are included in the
  distributed-asset manifest.

## D4: Backup and generated facts

Status: `PASS`

- `currentbackup` round-trip, tamper, restore, and semantic-closure tests
  passed in the focused run and in the full regression.
- Backup/verify/restore paths preserve the FAC2 identity and reject foreign or
  stale store identities before mutation.
- `go generate ./...` produced the current FAC2 runtime facts. The generated
  JSON and Markdown record 42 tables, the FAC2 fingerprint, and the FAC2
  migration digest.
- `Test-Docs.Tests.ps1 -GofmtPath <absolute-go-toolchain>/gofmt.exe` passed
  with `DOCS_SELFTEST_OK cases=162`.

## D5: Acceptance gates

Status: `PASS_FOR_P0_SCOPE`

- `Test-PublicTree.Tests.ps1`: 257 assertions passed.
- Real repository PublicTree: `PUBLIC_TREE_PASS`.
- `Test-Branding.ps1`: passed.
- `Test-Docs.ps1`: `DOCS_GATE_OK markdown=60`.
- `Test-License.ps1`: 36 Go dependencies, 72 npm dependencies, 95
  distributed assets passed.
- `release-gate.Tests.ps1`: `RELEASE_GATE_SELFTEST_PASS cases=16 assertions=318`.
  The clean-staging fixture now includes the four permanent gates in order:
  PublicTree, License, Docs, MoneyBanList.
- `go vet ./...`, `go mod verify`, and `gofmt -l .` passed.

The following are deliberately outside the P0 local acceptance claim and
remain Beta-gate work: native Linux execution, Linux race, Windows/Linux
matrix runs on separate hosts, a second real Provider, and the final owner
decision `RELEASE_READY`.

## Handoff

P0 is complete for the current development scope. The next executable stage is
P1/W6.6: server-owned Upgrade Review. It must consume FAC2 server-owned inert
Artifacts, persist Review and Decision, and retain the rule that approval does
not Install, Activate, Bind, Grant, Apply, or Execute.
