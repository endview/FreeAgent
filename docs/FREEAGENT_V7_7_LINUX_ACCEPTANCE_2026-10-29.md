# FreeAgent v7.7 Linux portable acceptance

Candidate: v7.7.0-dev.1, linux/amd64, unsigned development preview.
The package adds a small shell launcher to the existing CLI and embedded Web UI.
No Linux native desktop, new sandbox, installation service, compatibility layer,
or independent execution core was added. Existing uncommitted work is retained.

## Actual target

- Debian GNU/Linux 12 (Bookworm), amd64; kernel 6.1.0-53-cloud-amd64.
- glibc 2.36; test toolchain Go 1.26.5 and GCC 12.2.0.
- Tests ran as uid/gid 65534 using an isolated temporary directory and clean
  environment. SSH administration was used only to prepare and supervise tests.
- The application is cross-built with CGO disabled; the race suite uses CGO and
  the server's GCC. No Go installation, firewall change or system service is
  needed for the delivered archive.

## Results

| Check | Actual result |
|---|---|
| Complete Debian 12 Go race suite | PASS; Harness 318.898 s, CLI 130.180 s, ChatUI 39.538 s; exit 0 and no race report |
| Tested runtime source versus final working tree | PASS; 324 Go/module/embedded-asset files byte-matched the tested snapshot |
| Initial Linux portable package | PASS; checksums, static amd64 ELF, execute bits, LF launcher, spaces/shell punctuation/Unicode in package path |
| Fresh startup and shared records | PASS; loopback Web, None/Echo chat, Web→CLI→Web, duplicate request refusal, restart |
| Concurrent runs | PASS; two loopback Provider requests overlap, third/same-chat refusal, stop A keeps B, UNKNOWN cannot replay |
| Key storage | PASS; synthetic saved key, package-local path, mode 0600, honest plaintext metadata |
| Actual commands | PASS; unsupported sandbox receipt with no host fallback; explicit Full/host writes a proof file |
| Background lifecycle | PASS; explicit stop and Owner SIGTERM stop the owned writer, retaining existing side effects |
| Recovery | PASS; backup/verify/restore, repeated-init refusal, invalid manifest refusal, no user-config fallback |
| Final frozen archive gate | PASS; 16 checks in a new directory, exit 0; no remaining unknown test execution |
| Launcher and test tooling | PASS; `/bin/sh -n`, Python compile, help and malformed arguments leave no state |

Race command: `go test -race -p 2 -count=1 -timeout=10m -skip 'Live|RealProvider' ./...`.
The portable gate is `scripts/test-linux-portable.py`; it exercises the actual
extracted executable and HTTP boundary, with a synthetic loopback Provider.
Python syntax checks pass. Early test-script errors were corrected: attached
config commands need a confirmation answer, online end-task uses the existing
Owner endpoint, and Debian's shell allowlist needs the actual regular file.

Logs are in `output/v77-debian12-race.log`,
`output/v77-debian12-environment.txt` and the final portable JSON receipt.
The archive's sibling SHA256SUMS.txt, manifest and source inventory identify the
tested package. No credentials or runtime data are bundled.

Final archive:
`bin/v77-linux-debian12-delivery-20261029/FreeAgent-v7.7.0-dev.1-linux-amd64-portable.tar.gz`
(13,598,169 bytes). SHA256:
`aad9cc4a2b9ad4093b92300c21bf6435b3454ba86f3bd0afa02d98771fe25d9c`.
The sibling `LOCAL_ACCEPTANCE.json`, `DEBIAN12_RACE.log` and
`DEBIAN12_ENVIRONMENT.txt` contain the actual results. The final receipt retains
UNKNOWN for the intentionally interrupted model Attempt after abandonment and
Owner restart; there is no unresolved test process. Checks confirmed no test
FreeAgent process remained running.

The build is based on commit `6b9e0aee2c52b7432aa75d9fbea835d20043812b` with
`source_dirty=true`; source inventory SHA256:
`a3b48a5512a6ac5272f8c1b6928cd5161e790d49ed94815eb5837cb78834f079`.
This acceptance document was updated after the frozen build. The archive and
compiled runtime source were not changed after final acceptance.

Server copy: `/tmp/freeagent-v77-debian12-e87TvbFj/delivery/`. It contains the
archive, checksums, acceptance receipt, environment/race logs and acceptance
script. Temporary source, SDK, caches and runtime stores were removed after
verification; no background FreeAgent service was installed or left running.

## Remaining boundaries

- Real Provider and Telegram/Lark/QQ acceptance: NOT_RUN. This task used no
  production API or channel credential and sent no message to external people.
- Native Linux desktop: NOT_SUPPORTED. Use CLI or the embedded Web UI, with SSH
  port forwarding for this headless server. Automated HTTP checks are not a
  manual browser accessibility/rendering inspection.
- Linux AppContainer command isolation: NOT_SUPPORTED. A sandbox request must
  refuse; Full/host executes as the current user and is not containment evidence.
- Linux saved keys are plaintext in a mode-0600 file. Environment references are
  available. This is not Windows DPAPI encryption.
- One-shot `abandon --run` does not attach to a running Owner; use the Web end
  action or `/abandon` in interactive CLI, or stop the Owner first.
- Stop preserves prior effects. Crash-time process-tree containment, extra CPU
  architectures and other Linux distributions were not accepted by these tests.
