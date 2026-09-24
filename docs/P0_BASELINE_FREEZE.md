# P0 Money-Budget Removal Baseline Freeze

Status: `P0_D0_BASELINE_FROZEN`  
Recorded: 2026-09-21 (Asia/Shanghai)  
Source commit: `80daddef278fa6834ac8e0c748bedf1cae11f002`  
Implementation branch: `codex/remove-money-budget`

This record freezes the pre-removal FAC1 development baseline. It does not
approve release, claim a clean full-suite baseline, or make external archives
part of the source tree. Do not commit databases, credentials, logs, backup
bundles, compiled binaries, or this machine's external archive contents.

## Reproducible source and tool archives

The archives live in an operator-held directory outside this repository, named
`freeagent-p0-baseline-20260921-80dadde`. Its absolute location is deliberately
not recorded here; the sizes and digests below are the portable evidence.

| Item | Size | SHA-256 |
| --- | ---: | --- |
| `freeagent-80dadde.bundle` | 4,721,896 | `c66f8a2dac06e5e916634bc7a21ad29ca7a8456241650915b26170849a1eda89` |
| `bin/freeagent-80dadde-windows-amd64.exe` | 27,973,120 | `d166408ba0426b933aebfc71fe0ec900e584060cbae24dcffc6b060759bf6e50` |

`git bundle verify` reports complete history and exposes
`refs/heads/main` at the source commit. The binary reports:

```text
freeagent version=v0.1.0-dev.2 commit=80daddef278fa6834ac8e0c748bedf1cae11f002 target=windows/amd64
```

It was built with the repository-pinned Go toolchain:

```text
go version go1.26.5 windows/amd64
```

The previously packaged Windows amd64 tool remains in the operator-held
`freeagent-v0.1.0-dev.2-release` archive at
`artifact-windows-amd64/bin/freeagent-windows-amd64.exe`.
It reports commit `2b0b2b36ead98e3a4a99f0521bba01934bf2b28b`, has size
38,311,936 bytes, and SHA-256
`2692125476405c0db8760d850330637895c3e6164a55a25ea4502103f96b4b1a`.

## FAC1 backup retained for historical verification

The existing backup remains in place and was not copied, restored, migrated,
or modified by D0. It is the `backup` directory inside the operator-held
`freeagent-review-20260920-cf00e93dcf3c466988dc36d3d4e891b1` review tree,
outside this repository.

| Fact | Value |
| --- | --- |
| Format | `freeagent.current-store-backup/v1` |
| Application ID | `1178682161` (`FAC1`) |
| UserVersion | `1` |
| Schema identity | `github.com/endview/freeagent/current-store-v1` |
| Schema fingerprint | `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d` |
| Store instance ID | `73b1ded976e8fa744349c3df9b34863d` |
| Manifest SHA-256 | `01fda568d8ac0341d2a86672158446921a99f117d772a62abd4c8d8e8f0096fe` |
| Database size | 991,232 bytes |
| Database SHA-256 | `46c4123801e756c05ddfc08adae7bc66726e9fef7c1800bd5e24e096463925c2` |
| Artifact count | `2` |
| Pending/UNKNOWN model, action, and channel counts | all `0` |

Both the packaged `2b0b2b3` tool and the newly built `80dadde` tool completed
`backup-verify` successfully against this bundle. After the P0 cutover, the
frozen FAC1 tool remains the full verifier/restorer for this historical
generation. The new runtime must identify and reject FAC1 without modifying
the source or destination.

## Protected historical source identities

The following source blobs were recorded before implementation. The FAC1
bootstrap migration and historical acceptance/specification evidence are not
rewrite targets for P0:

| File | Git blob ID at `80dadde` |
| --- | --- |
| `internal/currentstore/migrations/0001_current.sql` | `3d12dd928b4149eab60fb22faa382caece777f8d` |
| `docs/CUTOVER_ACCEPTANCE.md` | `ed9ecee28f5b7da1bb61f81d63783586b79ea801` |
| `docs/specs/CURRENT_STORE_V1.md` | `71e6ac87027d96365c5ee7bd686548b8a8d597cc` |
| `docs/specs/CORE_RUNTIME_V1.md` | `9ccd62a096d4086c38de34089265700dfa724158` |

`docs/CURRENT_CAPABILITIES.md` and generated runtime facts describe the active
runtime and therefore will change only by append/current-generation updates;
they must not rewrite the historical accepted sections or hashes.

## Baseline verification result

Command:

```powershell
go mod verify
go test -count=1 -timeout=30m ./...
```

Environment: Go `1.26.5`, Windows amd64. `go mod verify` passed. The full suite
completed in about 13 minutes and failed because existing service fixtures did
not declare the now-required explicit model output limit:

- `internal/channelservice`: 2 tests failed.
- `internal/localchat`: shared fixtures caused the chat/composite/scheduler
  integration tests to fail.
- The common error was `model parameters: max_tokens must be explicit`.
- `cmd/freeagent` passed in 794.427 seconds; `internal/currentbackup` passed in
  315.273 seconds; `internal/currentstore` passed in 216.734 seconds. All other
  packages shown by the command passed.

The failure predates P0 implementation. It is not permission to weaken the
explicit output limit. The fixtures must declare a bounded `max_tokens` value
within their frozen ContextPolicy before contract removal work can use the
suite as a clean regression baseline.

## D0 exit and P0 handoff

D0 preserves enough material to inspect or restore the FAC1 development
generation without keeping its money-budget code in the new runtime. No data
was deleted and no external archive was pushed.

P0 implementation and D5 acceptance are now recorded in
[`P0_ACCEPTANCE_EVIDENCE`](P0_ACCEPTANCE_EVIDENCE.md). The FAC2 identity and
money-field denylist are the active baseline; the FAC1 material above remains
historical protection only.

Next: P1/W6.6 server-owned Upgrade Review. P1 must use the FAC2 contracts and
rebuild its W6.5/U3 fixtures before adding Review/Decision service behavior.
