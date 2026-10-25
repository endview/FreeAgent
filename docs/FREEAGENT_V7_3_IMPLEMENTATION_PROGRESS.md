# FreeAgent v7.3 implementation record

Date: 2026-10-25. Baseline: `aa5259d` (v7.2).

Implemented: one owner supervisor, immutable startup Attempt/Run and bounded
meta projection; independent finite process lifetime; global command exclusion;
UTF-8 cursor tails; idempotent Stop; final exit/output/tree cleanup evidence;
restart UNKNOWN review; direct user start through ordinary Preview/Confirm;
frozen AI management tools, GUI panel and attached CLI using the same owner.
No service table, scheduler, replay or legacy migration was added.

PASS: focused real-process completion, failure, timeout, descendant cleanup,
concurrent Stop, startup-context cancellation, immutable parent, UNKNOWN
recovery/review, snapshot authority and cross-conversation Stop tests.
PASS: authenticated owner API rejects observer mutations. 72 JS tests, UI
production build, vet and notices check. Edge desktop/mobile exercised real
start, output and confirmed Stop; no horizontal overflow at 390x844.

Full Windows ordinary/race runs initially encountered intermittent local TCP
connect timeouts in existing CLI/key fixtures. The affected tests passed three
focused repeats; ordinary package reruns passed, and the final complete serial
Windows race suite passed (Harness 187.876s). Vet/notices also passed.
Portable clean startup/reopen, engine path, integrity and CLI smoke passed for
the intermediate v7.3 gate archive (SHA256
`db0e15de744d64b34d71ad05ec721ac54212f2c855b00fdc521b093c76f509a8`).
That archive predates two small GUI presentation fixes, which are included in
the next final package. Real Providers: NOT_RUN. Linux CI follows the commit.

Protocol and product behavior: [command sessions](COMMAND_SESSIONS.md).
